// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartdecode

import (
	"errors"
	"fmt"
	"slices"
)

// Log pages of a SCSI or SAS drive that the decoders read with LOG SENSE.
const (
	PageSupported      uint8 = 0x00
	PageWriteErrors    uint8 = 0x02
	PageReadErrors     uint8 = 0x03
	PageVerifyErrors   uint8 = 0x05
	PageTemperature    uint8 = 0x0D
	PageStartStop      uint8 = 0x0E
	PageInfoExceptions uint8 = 0x2F

	// LogDataLen is the allocation length of every LOG SENSE the decoders send.
	LogDataLen = 512
)

// ErrBadPage means a log page is not the page that was asked for.
var ErrBadPage = errors.New("log page is not the one asked for")

// ErrTransient means the drive answered CHECK CONDITION with a state that passes: not ready, a unit
// attention after a reset, or an aborted command. The read is tried again.
var ErrTransient = errors.New("drive is not ready for the command")

// Sense keys the decoders tell apart.
const (
	senseNotReady           = 0x02
	senseIllegalReq         = 0x05
	senseUnitAttn           = 0x06
	senseAbortedCmd         = 0x0B
	senseKeyMask            = 0x0F
	senseDescriptorCurrent  = 0x72
	senseDescriptorDeferred = 0x73
)

// SenseError turns the sense bytes of a failed command into an error. ILLEGAL REQUEST, or no sense
// bytes, is ErrUnsupported: the drive refuses the page. NOT READY, UNIT ATTENTION and ABORTED COMMAND
// are ErrTransient. Any other key is an error that is neither.
func SenseError(sense []byte) error {
	if len(sense) < 3 {
		return fmt.Errorf("%w: no sense data", ErrUnsupported)
	}
	key := sense[2] & senseKeyMask
	if sense[0]&0x7F == senseDescriptorCurrent || sense[0]&0x7F == senseDescriptorDeferred {
		key = sense[1] & senseKeyMask
	}
	switch key {
	case senseIllegalReq:
		return fmt.Errorf("%w: illegal request", ErrUnsupported)
	case senseNotReady, senseUnitAttn, senseAbortedCmd:
		return fmt.Errorf("%w: sense key %#x", ErrTransient, key)
	}
	return fmt.Errorf("drive answered CHECK CONDITION, sense key %#x", key)
}

// logPages are the pages the allow-list lets through, in the order they are read after the page list.
var logPages = []uint8{PageTemperature, PageInfoExceptions, PageReadErrors, PageWriteErrors, PageVerifyErrors, PageStartStop}

// SCSICommand is one LOG SENSE of a log page, in the cumulative values, with subpage 0.
type SCSICommand struct {
	Page    uint8
	DataLen int
}

// SCSITransport sends one allow-listed LOG SENSE to a drive and returns the page. A drive that
// refuses the page gives ErrUnsupported.
type SCSITransport interface {
	SCSI(cmd SCSICommand) ([]byte, error)
}

// LogSenseCommand is LOG SENSE of one page.
func LogSenseCommand(page uint8) SCSICommand { return SCSICommand{Page: page, DataLen: LogDataLen} }

// CheckSCSI returns nil for LOG SENSE of one of the listed pages at the fixed length and
// ErrCommandNotAllowed for anything else.
func CheckSCSI(cmd SCSICommand) error {
	if cmd.DataLen == LogDataLen && (cmd.Page == PageSupported || slices.Contains(logPages, cmd.Page)) {
		return nil
	}
	return fmt.Errorf("%w: LOG SENSE page %#02x length %d", ErrCommandNotAllowed, cmd.Page, cmd.DataLen)
}

// checkHeader accepts a base page of the given code: no subpage format, subpage 0.
func checkHeader(page []byte, code uint8) error {
	if len(page) < 4 {
		return ErrShortBuffer
	}
	if page[0]&0x3F != code || page[0]&0x40 != 0 || page[1] != 0 {
		return fmt.Errorf("%w: got %#02x/%#02x, want %#02x", ErrBadPage, page[0], page[1], code)
	}
	return nil
}

// logParams calls fn for each complete parameter of a log page. A page length past the buffer means
// the allocation length cut the page: the walk ends at the last complete parameter. A parameter that
// does not fit the page length, or bytes too few for a parameter header, are ErrShortBuffer.
func logParams(page []byte, code uint8, fn func(code uint16, data []byte)) error {
	if err := checkHeader(page, code); err != nil {
		return err
	}
	declared := 4 + int(page[2])<<8 + int(page[3])
	end := min(declared, len(page))
	cut := declared > len(page)
	pos := 4
	for pos+4 <= end {
		size := int(page[pos+3])
		if pos+4+size > end {
			if cut {
				return nil
			}
			return ErrShortBuffer
		}
		fn(uint16(page[pos])<<8|uint16(page[pos+1]), page[pos+4:pos+4+size])
		pos += 4 + size
	}
	if pos < end && !cut {
		return ErrShortBuffer
	}
	return nil
}

// bigEndian reads a parameter of 1 to 8 bytes.
func bigEndian(b []byte) (uint64, bool) {
	if len(b) == 0 || len(b) > 8 {
		return 0, false
	}
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	return v, true
}

// ParseSupportedPages returns the page codes the drive lists.
func ParseSupportedPages(page []byte) ([]uint8, error) {
	if err := checkHeader(page, PageSupported); err != nil {
		return nil, err
	}
	end := min(4+int(page[2])<<8+int(page[3]), len(page))
	return append([]uint8(nil), page[4:end]...), nil
}

const noTemperature = 0xFF

// ParseTemperature returns the current temperature in Celsius; ok is false when the drive reports none.
func ParseTemperature(page []byte) (celsius int, ok bool, err error) {
	err = logParams(page, PageTemperature, func(code uint16, data []byte) {
		if code == 0 && len(data) >= 2 && data[1] != noTemperature {
			celsius, ok = int(data[1]), true
		}
	})
	return celsius, ok, err
}

// InfoExceptions is the informational exceptions page: the additional sense code of the newest
// exception and the most recent temperature.
type InfoExceptions struct {
	ASC, ASCQ       uint8
	Temperature     int
	HaveTemperature bool
}

// ascFailurePrediction is the additional sense code "failure prediction threshold exceeded". Other
// codes on the page, such as a temperature warning (0Bh), are not a failure prediction.
const ascFailurePrediction = 0x5D

// PredictedFailure is true when the newest exception is a failure prediction.
func (i InfoExceptions) PredictedFailure() bool { return i.ASC == ascFailurePrediction }

// ParseInfoExceptions decodes the informational exceptions page.
func ParseInfoExceptions(page []byte) (InfoExceptions, error) {
	var out InfoExceptions
	found := false
	err := logParams(page, PageInfoExceptions, func(code uint16, data []byte) {
		if code != 0 || len(data) < 2 {
			return
		}
		found = true
		out.ASC, out.ASCQ = data[0], data[1]
		if len(data) >= 3 && data[2] != noTemperature {
			out.Temperature, out.HaveTemperature = int(data[2]), true
		}
	})
	if err == nil && !found {
		err = ErrShortBuffer
	}
	return out, err
}

// ErrorCounters holds the totals of an error counter page.
type ErrorCounters struct {
	Corrected, Uncorrected uint64
	HaveUncorrected        bool
}

// Parameter codes of the error counter pages.
const (
	paramTotalCorrected   = 0x0003
	paramTotalUncorrected = 0x0006
	paramStartStopCycles  = 0x0004
)

// ParseErrorCounters decodes a read, write or verify error counter page.
func ParseErrorCounters(page []byte, code uint8) (ErrorCounters, error) {
	var out ErrorCounters
	err := logParams(page, code, func(param uint16, data []byte) {
		v, ok := bigEndian(data)
		if !ok {
			return
		}
		switch param {
		case paramTotalCorrected:
			out.Corrected = v
		case paramTotalUncorrected:
			out.Uncorrected, out.HaveUncorrected = v, true
		}
	})
	return out, err
}

// ParseStartStop returns the accumulated start-stop cycles.
func ParseStartStop(page []byte) (cycles uint64, ok bool, err error) {
	err = logParams(page, PageStartStop, func(code uint16, data []byte) {
		if v, good := bigEndian(data); good && code == paramStartStopCycles {
			cycles, ok = v, true
		}
	})
	return cycles, ok, err
}

// SCSIData is what the log pages of one SAS drive give.
type SCSIData struct {
	PredictedFailure    bool
	Temperature         int
	HaveTemperature     bool
	Read, Write, Verify ErrorCounters
	StartStopCycles     uint64
	HaveStartStop       bool
	Refused             int // pages the drive refused or sent malformed
}

// ReadSCSI reads the page list of a drive and then each listed page the decoders know. A refused or
// malformed page is skipped and counted. ErrCommandNotAllowed is a bug in the caller's allow-list
// and, like any other transport error, ends the read.
func ReadSCSI(tr SCSITransport) (SCSIData, error) {
	var d SCSIData
	raw, err := logSense(tr, PageSupported)
	if err != nil {
		return d, err
	}
	supported, err := ParseSupportedPages(raw)
	if err != nil {
		return d, err
	}
	have := map[uint8]bool{}
	for _, p := range supported {
		have[p] = true
	}
	var ie InfoExceptions
	hasIE := false
	for _, code := range logPages {
		if !have[code] {
			continue
		}
		raw, err := logSense(tr, code)
		if errors.Is(err, ErrCommandNotAllowed) {
			return d, fmt.Errorf("LOG SENSE of page %#02x: %w", code, err)
		}
		if errors.Is(err, ErrUnsupported) || errors.Is(err, ErrTransient) {
			d.Refused++
			continue
		}
		if err != nil {
			return d, err
		}
		if !d.apply(code, raw, &ie, &hasIE) {
			d.Refused++
		}
	}
	if hasIE {
		d.PredictedFailure = ie.PredictedFailure()
		if !d.HaveTemperature && ie.HaveTemperature {
			d.Temperature, d.HaveTemperature = ie.Temperature, true
		}
	}
	return d, nil
}

// logSense reads one page and tries once more when the drive says it is not ready.
func logSense(tr SCSITransport, page uint8) ([]byte, error) {
	raw, err := tr.SCSI(LogSenseCommand(page))
	if errors.Is(err, ErrTransient) {
		raw, err = tr.SCSI(LogSenseCommand(page))
	}
	return raw, err
}

// apply decodes one page into d and reports whether the page was well formed.
func (d *SCSIData) apply(code uint8, page []byte, ie *InfoExceptions, hasIE *bool) bool {
	var err error
	switch code {
	case PageTemperature:
		d.Temperature, d.HaveTemperature, err = ParseTemperature(page)
	case PageInfoExceptions:
		*ie, err = ParseInfoExceptions(page)
		*hasIE = err == nil
	case PageReadErrors:
		d.Read, err = ParseErrorCounters(page, code)
	case PageWriteErrors:
		d.Write, err = ParseErrorCounters(page, code)
	case PageVerifyErrors:
		d.Verify, err = ParseErrorCounters(page, code)
	case PageStartStop:
		d.StartStopCycles, d.HaveStartStop, err = ParseStartStop(page)
	}
	return err == nil
}
