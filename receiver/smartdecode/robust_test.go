// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartdecode

import (
	"errors"
	"math"
	"testing"
)

func FuzzParseIdentify(f *testing.F) {
	f.Add(identifyBuffer(identifySpec{model: "ACME", serial: "SN1", firmware: "FW", sectors: 1 << 20, rotation: 1, smartSupported: true, smartEnabled: true}))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, buf []byte) {
		_, err := ParseIdentify(buf)
		if len(buf) < 512 && !errors.Is(err, ErrShortBuffer) {
			t.Fatalf("%d bytes: err = %v, want ErrShortBuffer", len(buf), err)
		}
	})
}

func FuzzParseSmartData(f *testing.F) {
	attrs := []rawAttr{{id: 194, value: 100, worst: 90, raw: rawNumber(35)}, {id: 5, value: 100, worst: 100}}
	f.Add(smartDataBuffer(attrs), thresholdBuffer(attrs))
	f.Add([]byte{}, []byte{})
	f.Fuzz(func(t *testing.T, data, thresholds []byte) {
		got, err := ParseSmartData(data, thresholds)
		if len(data) < 512 && !errors.Is(err, ErrShortBuffer) {
			t.Fatalf("%d bytes of data: err = %v, want ErrShortBuffer", len(data), err)
		}
		if err == nil && len(got) > 30 {
			t.Fatalf("%d attributes, the table holds 30", len(got))
		}
	})
}

func FuzzParseATASense(f *testing.F) {
	f.Add(make([]byte, 32))
	f.Add([]byte{0x72, 0, 0, 0, 0, 0, 0, 14, 9, 12})
	f.Fuzz(func(t *testing.T, sense []byte) { _, _ = ParseATASense(sense) })
}

func FuzzParseDeviceStatisticsTemperature(f *testing.F) {
	f.Add(make([]byte, 512))
	f.Fuzz(func(t *testing.T, page []byte) { _, _ = ParseDeviceStatisticsTemperature(page) })
}

func FuzzParseNVMeIdentify(f *testing.F) {
	f.Add(nvmeIdentifyBuffer("ACME", "SN1", "FW", 1<<40))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, buf []byte) {
		_, err := ParseNVMeIdentify(buf)
		if len(buf) < 4096 && !errors.Is(err, ErrShortBuffer) {
			t.Fatalf("%d bytes: err = %v, want ErrShortBuffer", len(buf), err)
		}
	})
}

func FuzzParseNVMeSmartLog(f *testing.F) {
	f.Add(nvmeSmartBuffer(nvmeSmartSpec{compositeKelvin: 310, unitsRead: 1 << 40}))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, buf []byte) {
		got, err := ParseNVMeSmartLog(buf)
		if len(buf) < 512 {
			if !errors.Is(err, ErrShortBuffer) {
				t.Fatalf("%d bytes: err = %v, want ErrShortBuffer", len(buf), err)
			}
			return
		}
		if got.BytesRead() < got.DataUnitsRead || got.BytesWritten() < got.DataUnitsWritten {
			t.Fatalf("a byte count is below its unit count: %+v", got)
		}
	})
}

func FuzzCheckATAPassesOnlyTheListedCommands(f *testing.F) {
	f.Add(uint8(0xEC), uint8(0), uint8(1), uint8(0), uint8(0), uint8(0), uint8(0), uint8(1), 512, true)
	f.Fuzz(func(t *testing.T, command, features, count, low, mid, high, device, protocol uint8, dataLen int, wantRegs bool) {
		cmd := ATACommand{
			Command: command, Features: features, SectorCount: count, LBALow: low, LBAMid: mid, LBAHigh: high,
			Device: device, Protocol: Protocol(protocol), DataLen: dataLen, WantRegisters: wantRegs,
		}
		if CheckATA(cmd) == nil && !isAllowedATA(cmd) {
			t.Fatalf("CheckATA allowed %+v", cmd)
		}
	})
}

func FuzzCheckNVMePassesOnlyTheListedCommands(f *testing.F) {
	f.Add(uint8(6), uint32(0), uint32(1), uint32(0), 4096)
	f.Fuzz(func(t *testing.T, opcode uint8, nsid, cdw10, cdw11 uint32, dataLen int) {
		cmd := NVMeCommand{Opcode: opcode, NSID: nsid, CDW10: cdw10, CDW11: cdw11, DataLen: dataLen}
		if CheckNVMe(cmd) != nil {
			return
		}
		if cmd != IdentifyControllerCommand() && cmd != SmartLogCommand() {
			t.Fatalf("CheckNVMe allowed %+v", cmd)
		}
	})
}

func TestNVMeSmartLog_AZeroCompositeIsNotATemperature(t *testing.T) {
	got, err := ParseNVMeSmartLog(nvmeSmartBuffer(nvmeSmartSpec{compositeKelvin: 0}))

	if err != nil {
		t.Fatal(err)
	}
	if got.HasComposite {
		t.Errorf("HasComposite = true for 0 K, CompositeCelsius = %d", got.CompositeCelsius)
	}
}

func TestNVMeSmartLog_ACompositeInKelvinIsConverted(t *testing.T) {
	got, err := ParseNVMeSmartLog(nvmeSmartBuffer(nvmeSmartSpec{compositeKelvin: 310}))

	if err != nil {
		t.Fatal(err)
	}
	if !got.HasComposite || got.CompositeCelsius != 37 {
		t.Errorf("composite = %d, present = %v, want 37 and true", got.CompositeCelsius, got.HasComposite)
	}
}

func TestNVMeSmart_ByteCountsSaturateInsteadOfWrapping(t *testing.T) {
	huge := NVMeSmart{DataUnitsRead: math.MaxUint64 / 1000, DataUnitsWritten: math.MaxUint64}

	if got := huge.BytesRead(); got != math.MaxUint64 {
		t.Errorf("BytesRead = %d, want the largest value", got)
	}
	if got := huge.BytesWritten(); got != math.MaxUint64 {
		t.Errorf("BytesWritten = %d, want the largest value", got)
	}
	if got := (NVMeSmart{DataUnitsRead: 2}).BytesRead(); got != 1024000 {
		t.Errorf("BytesRead of 2 units = %d, want 1024000", got)
	}
}
