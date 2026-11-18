// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smarttest

import "github.com/vladistan/otelcol-custom/receiver/smartdecode"

// LogParam packs one log parameter: code, control byte, length, data.
func LogParam(code uint16, data ...byte) []byte {
	return append([]byte{byte(code >> 8), byte(code), 0x03, byte(len(data))}, data...)
}

// Counter packs a four byte log parameter.
func Counter(code uint16, v uint32) []byte {
	return LogParam(code, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

// LogPage packs a log page: page code, subpage 0, page length, then the parameters.
func LogPage(code byte, params ...[]byte) []byte {
	var body []byte
	for _, p := range params {
		body = append(body, p...)
	}
	return append([]byte{code, 0, byte(len(body) >> 8), byte(len(body))}, body...)
}

// SAS is a fake SAS drive: it answers LOG SENSE from a table of pages and records the commands. A
// page that is not in the table is refused with ErrUnsupported, as a drive does.
type SAS struct {
	Pages map[uint8][]byte
	Sent  []smartdecode.SCSICommand
}

// SCSI implements smartdecode.SCSITransport.
func (f *SAS) SCSI(cmd smartdecode.SCSICommand) ([]byte, error) {
	f.Sent = append(f.Sent, cmd)
	if err := smartdecode.CheckSCSI(cmd); err != nil {
		return nil, err
	}
	page, ok := f.Pages[cmd.Page]
	if !ok {
		return nil, smartdecode.ErrUnsupported
	}
	return page, nil
}

// HealthySAS is a SAS drive with every page the decoders read: 38 degrees, no exception, a few
// corrected errors, 2 uncorrected read errors and 250 start-stop cycles.
func HealthySAS() *SAS {
	return &SAS{Pages: map[uint8][]byte{
		0x00: LogPage(0x00, []byte{0x00, 0x02, 0x03, 0x05, 0x0D, 0x0E, 0x2F, 0x38}),
		0x0D: LogPage(0x0D, LogParam(0x0000, 0x00, 38)),
		0x2F: LogPage(0x2F, LogParam(0x0000, 0x00, 0x00, 38)),
		0x03: LogPage(0x03, Counter(0x0003, 40), Counter(0x0006, 2)),
		0x02: LogPage(0x02, Counter(0x0003, 1), Counter(0x0006, 0)),
		0x05: LogPage(0x05, Counter(0x0006, 5)),
		0x0E: LogPage(0x0E, Counter(0x0004, 250)),
	}}
}
