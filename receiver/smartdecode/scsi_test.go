// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartdecode

import (
	"errors"
	"testing"
)

// logParam packs one log parameter: code, control byte, length, data.
func logParam(code uint16, data ...byte) []byte {
	return append([]byte{byte(code >> 8), byte(code), 0x03, byte(len(data))}, data...)
}

// logPage packs a log page: page code, subpage 0, page length, then the parameters.
func logPage(code byte, params ...[]byte) []byte {
	var body []byte
	for _, p := range params {
		body = append(body, p...)
	}
	return append([]byte{code, 0, byte(len(body) >> 8), byte(len(body))}, body...)
}

func counter(code uint16, v uint32) []byte {
	return logParam(code, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func TestParseSupportedPagesListsTheCodes(t *testing.T) {
	page := logPage(0x00, []byte{0x00, 0x02, 0x03, 0x05, 0x0D, 0x0E, 0x2F, 0x38})

	got, err := ParseSupportedPages(page)

	require.NoError(t, err)
	assert.Equal(t, []uint8{0x00, 0x02, 0x03, 0x05, 0x0D, 0x0E, 0x2F, 0x38}, got)
}

func TestParseTemperatureReadsTheCurrentTemperature(t *testing.T) {
	page := logPage(0x0D, logParam(0x0000, 0x00, 41), logParam(0x0001, 0x00, 65))

	celsius, ok, err := ParseTemperature(page)

	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 41, celsius)
}

func TestParseTemperatureWithTheNoReadingValueGivesNone(t *testing.T) {
	page := logPage(0x0D, logParam(0x0000, 0x00, 0xFF))

	_, ok, err := ParseTemperature(page)

	require.NoError(t, err)
	assert.False(t, ok)
}

func TestParseInfoExceptionsReadsAHealthyPage(t *testing.T) {
	h, err := ParseInfoExceptions(logPage(0x2F, logParam(0x0000, 0x00, 0x00, 36)))

	require.NoError(t, err)
	assert.False(t, h.PredictedFailure())
	assert.Equal(t, 36, h.Temperature)
	assert.True(t, h.HaveTemperature)
}

func TestParseInfoExceptionsReadsAFailingPage(t *testing.T) {
	f, err := ParseInfoExceptions(logPage(0x2F, logParam(0x0000, 0x5D, 0x10, 0xFF)))

	require.NoError(t, err)
	assert.True(t, f.PredictedFailure())
	assert.Equal(t, uint8(0x5D), f.ASC)
	assert.Equal(t, uint8(0x10), f.ASCQ)
	assert.False(t, f.HaveTemperature, "0xFF is no reading")
}

func TestParseErrorCountersReadsTheTotalsByParameterCode(t *testing.T) {
	page := logPage(0x03,
		counter(0x0000, 7), counter(0x0001, 1), counter(0x0003, 12),
		counter(0x0004, 100), logParam(0x0005, 0, 0, 0, 1, 0, 0, 0, 0), counter(0x0006, 3),
		counter(0x7777, 9), // an unknown parameter is ignored
	)

	c, err := ParseErrorCounters(page, PageReadErrors)

	require.NoError(t, err)
	assert.Equal(t, uint64(12), c.Corrected)
	assert.Equal(t, uint64(3), c.Uncorrected)
	assert.True(t, c.HaveUncorrected)
}

func TestParseErrorCountersWithoutTheUncorrectedParameterHasNoUncorrected(t *testing.T) {
	c, err := ParseErrorCounters(logPage(0x02, counter(0x0003, 5)), PageWriteErrors)

	require.NoError(t, err)
	assert.False(t, c.HaveUncorrected)
	assert.Equal(t, uint64(5), c.Corrected)
}

func TestParseStartStopReadsTheAccumulatedCycles(t *testing.T) {
	page := logPage(0x0E,
		logParam(0x0001, []byte("202101")...), counter(0x0003, 10000), counter(0x0004, 321), counter(0x0006, 77))

	s, ok, err := ParseStartStop(page)

	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, uint64(321), s)
}

func TestParametersOfOddLengthsAreReadAsBigEndian(t *testing.T) {
	page := logPage(0x03, logParam(0x0006, 0x01, 0x02, 0x03))

	c, err := ParseErrorCounters(page, PageReadErrors)

	require.NoError(t, err)
	assert.Equal(t, uint64(0x010203), c.Uncorrected)
}

func TestAParameterOverEightBytesIsIgnored(t *testing.T) {
	page := logPage(0x03, logParam(0x0006, 1, 2, 3, 4, 5, 6, 7, 8, 9), counter(0x0003, 4))

	c, err := ParseErrorCounters(page, PageReadErrors)

	require.NoError(t, err)
	assert.False(t, c.HaveUncorrected)
	assert.Equal(t, uint64(4), c.Corrected)
}

func TestDecodersNeverPanicOnAnyTruncationOrOnLongBuffers(t *testing.T) {
	pages := map[string][]byte{
		"supported": logPage(0x00, []byte{0, 2, 3, 5, 0x0D, 0x0E, 0x2F}),
		"temp":      logPage(0x0D, logParam(0, 0, 40)),
		"ie":        logPage(0x2F, logParam(0, 0, 0, 30)),
		"errors":    logPage(0x03, counter(0x0003, 1), counter(0x0006, 2)),
		"startstop": logPage(0x0E, counter(0x0004, 5)),
	}
	for name, page := range pages {
		for i := 0; i <= len(page); i++ {
			assert.NotPanics(t, func() {
				_, _ = ParseSupportedPages(page[:i])
				_, _, _ = ParseTemperature(page[:i])
				_, _ = ParseInfoExceptions(page[:i])
				_, _ = ParseErrorCounters(page[:i], PageReadErrors)
				_, _, _ = ParseStartStop(page[:i])
			}, "%s cut at %d", name, i)
		}
		long := append(append([]byte{}, page...), make([]byte, 4096)...)
		assert.NotPanics(t, func() { _, _ = ParseErrorCounters(long, PageReadErrors) }, name)
	}
}

func TestAPageWithTheWrongCodeOrNoHeaderIsAnError(t *testing.T) {
	_, err := ParseErrorCounters(logPage(0x02, counter(0x0006, 1)), PageReadErrors)
	assert.Error(t, err, "a write page where a read page was asked for")

	_, err = ParseErrorCounters([]byte{0x03, 0}, PageReadErrors)
	assert.ErrorIs(t, err, ErrShortBuffer)

	_, _, err = ParseTemperature(nil)
	assert.ErrorIs(t, err, ErrShortBuffer)
}

func TestAPageLengthPastTheBufferKeepsTheCompleteParameters(t *testing.T) {
	page := logPage(0x03, counter(0x0003, 8), counter(0x0006, 9))
	page[3] = 200 // claims more than it holds

	c, err := ParseErrorCounters(page, PageReadErrors)

	require.NoError(t, err)
	assert.Equal(t, uint64(9), c.Uncorrected)
}

func TestACutParameterIsDropped(t *testing.T) {
	page := logPage(0x03, counter(0x0003, 8), counter(0x0006, 9))

	c, err := ParseErrorCounters(page[:len(page)-2], PageReadErrors)

	require.NoError(t, err)
	assert.Equal(t, uint64(8), c.Corrected)
	assert.False(t, c.HaveUncorrected)
}

func TestCheckSCSIPassesOnlyTheListedPagesAtTheFixedLength(t *testing.T) {
	listed := map[uint8]bool{0x00: true, 0x02: true, 0x03: true, 0x05: true, 0x0D: true, 0x0E: true, 0x2F: true}

	for page := 0; page < 256; page++ {
		want := map[bool]error{true: nil, false: ErrCommandNotAllowed}[listed[uint8(page)]]
		err := CheckSCSI(SCSICommand{Page: uint8(page), DataLen: LogDataLen})
		assert.ErrorIs(t, err, want, "page %#x", page)
	}
	assert.ErrorIs(t, CheckSCSI(SCSICommand{Page: 0x03, DataLen: LogDataLen + 1}), ErrCommandNotAllowed)
	assert.ErrorIs(t, CheckSCSI(SCSICommand{Page: 0x03, DataLen: 0}), ErrCommandNotAllowed)
}

func TestLogSenseCommandPassesTheCheck(t *testing.T) {
	for _, page := range []uint8{PageSupported, PageWriteErrors, PageReadErrors, PageVerifyErrors, PageTemperature, PageStartStop, PageInfoExceptions} {
		assert.NoError(t, CheckSCSI(LogSenseCommand(page)), "page %#x", page)
	}
}

// fakeSCSI answers LOG SENSE from a table and records every command.
type fakeSCSI struct {
	pages map[uint8][]byte
	errs  map[uint8]error
	sent  []SCSICommand
}

func (f *fakeSCSI) SCSI(cmd SCSICommand) ([]byte, error) {
	f.sent = append(f.sent, cmd)
	if err := f.errs[cmd.Page]; err != nil {
		return nil, err
	}
	page, ok := f.pages[cmd.Page]
	if !ok {
		return nil, ErrUnsupported
	}
	return page, nil
}

func healthySAS() *fakeSCSI {
	return &fakeSCSI{pages: map[uint8][]byte{
		0x00: logPage(0x00, []byte{0x00, 0x02, 0x03, 0x05, 0x0D, 0x0E, 0x2F, 0x38}),
		0x0D: logPage(0x0D, logParam(0x0000, 0x00, 38)),
		0x2F: logPage(0x2F, logParam(0x0000, 0x00, 0x00, 38)),
		0x03: logPage(0x03, counter(0x0003, 40), counter(0x0006, 2)),
		0x02: logPage(0x02, counter(0x0003, 1), counter(0x0006, 0)),
		0x05: logPage(0x05, counter(0x0006, 5)),
		0x0E: logPage(0x0E, counter(0x0004, 250)),
	}}
}

func TestReadSCSIGathersTheHealthTemperatureErrorsAndCycles(t *testing.T) {
	tr := healthySAS()

	d, err := ReadSCSI(tr)

	require.NoError(t, err)
	assert.False(t, d.PredictedFailure)
	assert.Equal(t, 38, d.Temperature)
	assert.True(t, d.HaveTemperature)
	assert.Equal(t, uint64(2), d.Read.Uncorrected)
	assert.Equal(t, uint64(0), d.Write.Uncorrected)
	assert.True(t, d.Write.HaveUncorrected)
	assert.Equal(t, uint64(5), d.Verify.Uncorrected)
	assert.Equal(t, uint64(250), d.StartStopCycles)
	assert.True(t, d.HaveStartStop)
	assert.Zero(t, d.Refused)
}

func TestReadSCSIAsksOnlyForSupportedListedPagesAndEveryCommandPassesTheCheck(t *testing.T) {
	tr := healthySAS()
	tr.pages[0x00] = logPage(0x00, []byte{0x00, 0x0D, 0x38, 0x3E}) // no error pages, no IE, no start-stop

	_, err := ReadSCSI(tr)

	require.NoError(t, err)
	var asked []uint8
	for _, cmd := range tr.sent {
		assert.NoError(t, CheckSCSI(cmd))
		asked = append(asked, cmd.Page)
	}
	assert.Equal(t, []uint8{0x00, 0x0D}, asked)
}

func TestReadSCSIFlagsAPredictedFailure(t *testing.T) {
	tr := healthySAS()
	tr.pages[0x2F] = logPage(0x2F, logParam(0x0000, 0x5D, 0x00, 38))

	d, err := ReadSCSI(tr)

	require.NoError(t, err)
	assert.True(t, d.PredictedFailure)
}

func TestReadSCSISkipsAndCountsARefusedPage(t *testing.T) {
	tr := healthySAS()
	tr.errs = map[uint8]error{0x03: ErrUnsupported, 0x05: ErrTransient}

	d, err := ReadSCSI(tr)

	require.NoError(t, err)
	assert.Equal(t, 2, d.Refused)
	assert.Equal(t, 38, d.Temperature)
	assert.False(t, d.Read.HaveUncorrected)
	assert.True(t, d.Write.HaveUncorrected, "the other pages are still read")
}

func TestReadSCSIReturnsACommandNotAllowedErrorInsteadOfCountingIt(t *testing.T) {
	tr := healthySAS()
	tr.errs = map[uint8]error{0x05: ErrCommandNotAllowed}

	_, err := ReadSCSI(tr)

	assert.ErrorIs(t, err, ErrCommandNotAllowed)
}

func TestReadSCSICountsAMalformedPageAndKeepsGoing(t *testing.T) {
	tr := healthySAS()
	tr.pages[0x0E] = []byte{0x0E}

	d, err := ReadSCSI(tr)

	require.NoError(t, err)
	assert.Equal(t, 1, d.Refused)
	assert.False(t, d.HaveStartStop)
}

func TestReadSCSIFallsBackToTheTemperatureOfTheExceptionsPage(t *testing.T) {
	tr := healthySAS()
	tr.errs = map[uint8]error{0x0D: ErrUnsupported}

	d, err := ReadSCSI(tr)

	require.NoError(t, err)
	assert.Equal(t, 38, d.Temperature)
	assert.True(t, d.HaveTemperature)
}

func TestReadSCSIReturnsTheErrorWhenThePageListIsRefused(t *testing.T) {
	tr := healthySAS()
	tr.errs = map[uint8]error{0x00: ErrUnsupported}

	_, err := ReadSCSI(tr)

	assert.ErrorIs(t, err, ErrUnsupported)
}

func TestReadSCSIReturnsATransportErrorThatIsNotARefusal(t *testing.T) {
	boom := errors.New("drive gone")
	tr := healthySAS()
	tr.errs = map[uint8]error{0x03: boom}

	_, err := ReadSCSI(tr)

	assert.ErrorIs(t, err, boom)
}

func TestReadSCSIWithAnUnreadablePageListIsAnError(t *testing.T) {
	tr := healthySAS()
	tr.pages[0x00] = []byte{0x00}

	_, err := ReadSCSI(tr)

	assert.ErrorIs(t, err, ErrShortBuffer)
}
