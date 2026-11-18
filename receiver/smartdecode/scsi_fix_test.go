// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartdecode

import (
	"errors"
	"testing"
)

func TestOnlyAdditionalSenseCode5DIsAPredictedFailure(t *testing.T) {
	for name, tt := range map[string]struct {
		asc, ascq uint8
		want      bool
	}{
		"no exception":         {0x00, 0x00, false},
		"temperature warning":  {0x0B, 0x01, false},
		"enclosure degraded":   {0x0B, 0x02, false},
		"failure prediction":   {0x5D, 0x00, true},
		"failure, a sub-cause": {0x5D, 0x10, true},
	} {
		assert.Equal(t, tt.want, InfoExceptions{ASC: tt.asc, ASCQ: tt.ascq}.PredictedFailure(), name)
	}
}

func TestReadSCSIDoesNotFlagATemperatureWarningAsAFailure(t *testing.T) {
	tr := healthySAS()
	tr.pages[0x2F] = logPage(0x2F, logParam(0x0000, 0x0B, 0x01, 70))

	d, err := ReadSCSI(tr)

	require.NoError(t, err)
	assert.False(t, d.PredictedFailure)
}

func TestASubpagePageIsNotDecodedAsTheBasePage(t *testing.T) {
	for name, mutate := range map[string]func(p []byte){
		"SPF bit":      func(p []byte) { p[0] |= 0x40 },
		"subpage code": func(p []byte) { p[1] = 1 },
	} {
		temp := logPage(0x0D, logParam(0, 0, 40))
		mutate(temp)
		errs := logPage(0x03, counter(0x0006, 2))
		mutate(errs)
		list := logPage(0x00, []byte{0x03})
		mutate(list)

		_, _, err := ParseTemperature(temp)
		assert.ErrorIs(t, err, ErrBadPage, name)
		_, err = ParseErrorCounters(errs, PageReadErrors)
		assert.ErrorIs(t, err, ErrBadPage, name)
		_, err = ParseSupportedPages(list)
		assert.ErrorIs(t, err, ErrBadPage, name)
	}
}

func TestAMalformedPageIsAnErrorNotAnEmptyResult(t *testing.T) {
	short := logPage(0x03, counter(0x0006, 2))
	short[3] = 2 // the page length ends inside the first parameter
	over := logPage(0x03, counter(0x0003, 8), counter(0x0006, 9))
	over[4+3] = 40 // the first parameter claims more than the page holds
	trailing := append(logPage(0x03, counter(0x0006, 2)), 0, 0)
	trailing[3] += 2 // two bytes after the last parameter, too few for a header

	for name, page := range map[string][]byte{"length inside parameter": short, "overrun": over, "trailing bytes": trailing} {
		_, err := ParseErrorCounters(page, PageReadErrors)
		assert.ErrorIs(t, err, ErrShortBuffer, name)
	}
	_, _, err := ParseTemperature(append(logPage(0x0D, logParam(0, 0, 40)), 0))
	assert.NoError(t, err, "bytes past the page length are not part of the page")
}

func TestAMalformedPageCountsAsRefused(t *testing.T) {
	tr := healthySAS()
	page := logPage(0x0E, counter(0x0004, 250))
	page[3] = 3
	tr.pages[0x0E] = page

	d, err := ReadSCSI(tr)

	require.NoError(t, err)
	assert.Equal(t, 1, d.Refused)
	assert.False(t, d.HaveStartStop)
}

func TestSenseKeysAreClassified(t *testing.T) {
	fixed := func(key, asc uint8) []byte {
		s := make([]byte, 18)
		s[0], s[2], s[12] = 0x70, key, asc
		return s
	}
	desc := func(key, asc uint8) []byte { return []byte{0x72, key, asc, 0, 0, 0, 0, 0} }

	for name, tt := range map[string]struct {
		sense     []byte
		transient bool
		refused   bool
	}{
		"illegal request, fixed":      {fixed(0x05, 0x24), false, true},
		"illegal request, descriptor": {desc(0x05, 0x24), false, true},
		"no sense bytes":              {nil, false, true},
		"not ready":                   {fixed(0x02, 0x04), true, false},
		"unit attention":              {fixed(0x06, 0x29), true, false},
		"unit attention, descriptor":  {desc(0x06, 0x29), true, false},
		"aborted command":             {fixed(0x0B, 0x00), true, false},
		"medium error":                {fixed(0x03, 0x11), false, false},
		"hardware error":              {fixed(0x04, 0x44), false, false},
	} {
		err := SenseError(tt.sense)

		assert.Equal(t, tt.transient, errors.Is(err, ErrTransient), name)
		assert.Equal(t, tt.refused, errors.Is(err, ErrUnsupported), name)
		assert.Error(t, err, name)
	}
}

// flaky fails the first calls of one page with err, then answers like the fake it wraps.
type flaky struct {
	*fakeSCSI
	page  uint8
	err   error
	times int
}

func (f *flaky) SCSI(cmd SCSICommand) ([]byte, error) {
	if cmd.Page == f.page && f.times > 0 {
		f.times--
		f.sent = append(f.sent, cmd)
		return nil, f.err
	}
	return f.fakeSCSI.SCSI(cmd)
}

func TestATransientAnswerIsRetriedOnce(t *testing.T) {
	tr := &flaky{fakeSCSI: healthySAS(), page: PageSupported, err: ErrTransient, times: 1}

	d, err := ReadSCSI(tr)

	require.NoError(t, err)
	assert.Equal(t, 38, d.Temperature)
	assert.Equal(t, 0, d.Refused)
}

func TestATransientAnswerTwiceOnThePageListIsReturned(t *testing.T) {
	tr := &flaky{fakeSCSI: healthySAS(), page: PageSupported, err: ErrTransient, times: 2}

	_, err := ReadSCSI(tr)

	assert.ErrorIs(t, err, ErrTransient)
	assert.False(t, errors.Is(err, ErrUnsupported))
}

func TestATransientPageThatStaysTransientIsCountedAsRefused(t *testing.T) {
	tr := &flaky{fakeSCSI: healthySAS(), page: PageReadErrors, err: ErrTransient, times: 2}

	d, err := ReadSCSI(tr)

	require.NoError(t, err)
	assert.Equal(t, 1, d.Refused)
	assert.False(t, d.Read.HaveUncorrected)
	assert.True(t, d.Write.HaveUncorrected)
}

func TestAReadingOfZeroDegreesIsAReading(t *testing.T) {
	c, ok, err := ParseTemperature(logPage(0x0D, logParam(0, 0, 0)))

	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 0, c)
}

func TestAnOrdinaryErrorIsNotRetried(t *testing.T) {
	boom := errors.New("drive gone")
	tr := &flaky{fakeSCSI: healthySAS(), page: PageSupported, err: boom, times: 5}

	_, err := ReadSCSI(tr)

	assert.ErrorIs(t, err, boom)
	assert.Equal(t, 1, len(tr.sent))
}
