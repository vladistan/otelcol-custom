// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartdecode

import (
	"errors"
	"testing"
)

func TestCheckNVMe_OnlyIdentifyControllerAndTheSmartLogPass(t *testing.T) {
	for name, cmd := range map[string]NVMeCommand{
		"identify controller": IdentifyControllerCommand(),
		"smart log":           SmartLogCommand(),
	} {
		if err := CheckNVMe(cmd); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	// Every opcode and every value of the low CDW10 byte, on each allowed command as the template.
	passes := 0
	for _, template := range []NVMeCommand{IdentifyControllerCommand(), SmartLogCommand()} {
		for opcode := 0; opcode < 256; opcode++ {
			for low := uint32(0); low < 256; low++ {
				cmd := template
				cmd.Opcode = uint8(opcode)
				cmd.CDW10 = template.CDW10&^0xFF | low
				if CheckNVMe(cmd) == nil {
					passes++
				}
			}
		}
	}
	if passes != 2 {
		t.Fatalf("%d opcode and CDW10 pairs pass, want 2", passes)
	}
}

func TestCheckNVMe_AnyOtherValueInAnyFieldIsRefused(t *testing.T) {
	for name, base := range map[string]NVMeCommand{"identify controller": IdentifyControllerCommand(), "smart log": SmartLogCommand()} {
		for _, nsid := range []uint32{0, 1, 2, 0xFFFFFFFE, 0xFFFFFFFF} {
			cmd := base
			cmd.NSID = nsid
			if got := CheckNVMe(cmd) == nil; got != (cmd == base) {
				t.Errorf("%s with NSID=%#x: allowed = %v", name, nsid, got)
			}
		}
		for numd := uint32(0); numd < 0x1000; numd++ { // CDW10 bits 27:16, the 32-bit words asked for, less one
			cmd := base
			cmd.CDW10 = base.CDW10&0xF000FFFF | numd<<16
			if got := CheckNVMe(cmd) == nil; got != (cmd == base) {
				t.Errorf("%s with NUMD=%d: allowed = %v", name, numd, got)
			}
		}
		for _, cdw11 := range []uint32{1, 0x100, 0x10000, 0x80000000} {
			cmd := base
			cmd.CDW11 = cdw11
			if CheckNVMe(cmd) == nil {
				t.Errorf("%s with CDW11=%#x is allowed", name, cdw11)
			}
		}
		for _, dataLen := range []int{-1, 0, 1, 511, 513, 4095, 4097, 1 << 30} {
			cmd := base
			cmd.DataLen = dataLen
			if got := CheckNVMe(cmd) == nil; got != (cmd == base) {
				t.Errorf("%s with DataLen=%d: allowed = %v", name, dataLen, got)
			}
		}
	}
}

func TestCheckNVMe_WriteOpcodesAreRefused(t *testing.T) {
	for name, opcode := range map[string]uint8{"format": 0x80, "firmware commit": 0x10, "sanitize": 0x84, "set features": 0x09, "delete queue": 0x00} {
		err := CheckNVMe(NVMeCommand{Opcode: opcode})
		if !errors.Is(err, ErrCommandNotAllowed) {
			t.Errorf("%s: err = %v, want ErrCommandNotAllowed", name, err)
		}
	}
}

func TestParseNVMeIdentify_ReadsIdentityAndCapacity(t *testing.T) {
	buf := nvmeIdentifyBuffer("ACME NVMe 1TB", "S0123456", "FW 1.0", 1000204886016)

	got, err := ParseNVMeIdentify(buf)

	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "ACME NVMe 1TB" || got.Serial != "S0123456" || got.Firmware != "FW 1.0" {
		t.Errorf("identity = %q %q %q", got.Model, got.Serial, got.Firmware)
	}
	if got.SizeBytes != 1000204886016 {
		t.Errorf("size = %d", got.SizeBytes)
	}
}

func TestParseNVMeIdentify_ShortBufferIsAnError(t *testing.T) {
	if _, err := ParseNVMeIdentify(make([]byte, 100)); !errors.Is(err, ErrShortBuffer) {
		t.Errorf("err = %v, want ErrShortBuffer", err)
	}
}

func TestParseNVMeSmartLog_TemperaturesAreCelsiusAndZeroSensorsAreAbsent(t *testing.T) {
	buf := nvmeSmartBuffer(nvmeSmartSpec{compositeKelvin: 318, sensorsKelvin: [8]uint16{320, 305}})

	got, err := ParseNVMeSmartLog(buf)

	if err != nil {
		t.Fatal(err)
	}
	if got.CompositeCelsius != 45 {
		t.Errorf("composite = %d, want 45", got.CompositeCelsius)
	}
	if len(got.SensorsCelsius) != 2 || got.SensorsCelsius[0].Index != 1 || got.SensorsCelsius[0].Celsius != 47 ||
		got.SensorsCelsius[1].Index != 2 || got.SensorsCelsius[1].Celsius != 32 {
		t.Errorf("sensors = %+v, want sensor 1 at 47 and sensor 2 at 32 only", got.SensorsCelsius)
	}
}

func TestParseNVMeSmartLog_Counters(t *testing.T) {
	buf := nvmeSmartBuffer(nvmeSmartSpec{
		percentageUsed: 7, availableSpare: 98, spareThreshold: 10, unitsRead: 20, unitsWritten: 10, powerCycles: 12, powerOnHours: 100,
		unsafeShutdowns: 3, mediaErrors: 3,
	})

	got, err := ParseNVMeSmartLog(buf)

	if err != nil {
		t.Fatal(err)
	}
	if got.AvailableSpare != 98 || got.SpareThreshold != 10 {
		t.Errorf("spare = %d threshold %d, want 98 and 10", got.AvailableSpare, got.SpareThreshold)
	}
	if got.PercentageUsed != 7 || got.PowerCycles != 12 || got.PowerOnHours != 100 || got.UnsafeShutdowns != 3 || got.MediaErrors != 3 {
		t.Errorf("counters = %+v", got)
	}
	if got.BytesRead() != 20*512000 || got.BytesWritten() != 5120000 {
		t.Errorf("bytes read %d written %d, want 10240000 and 5120000", got.BytesRead(), got.BytesWritten())
	}
}

func TestParseNVMeSmartLog_HighHalfOfA128BitCounterIsKept(t *testing.T) {
	buf := nvmeSmartBuffer(nvmeSmartSpec{})
	buf[128+8] = 1 // power-on hours, bit 64

	got, err := ParseNVMeSmartLog(buf)

	if err != nil {
		t.Fatal(err)
	}
	if got.PowerOnHours != 1<<63 && got.PowerOnHours != ^uint64(0) {
		t.Errorf("a counter above 64 bits must saturate, got %d", got.PowerOnHours)
	}
}

func TestParseNVMeSmartLog_ShortBufferIsAnError(t *testing.T) {
	if _, err := ParseNVMeSmartLog(make([]byte, 100)); !errors.Is(err, ErrShortBuffer) {
		t.Errorf("err = %v, want ErrShortBuffer", err)
	}
}

func TestNVMeState_CriticalWarningBits(t *testing.T) {
	cases := []struct {
		warning byte
		want    string
	}{
		{0x00, "ok"},
		{0x02, "degraded"},
		{0x10, "degraded"},
		{0x20, "degraded"},
		{0x01, "predicted_failure"},
		{0x04, "predicted_failure"},
		{0x08, "failed"},
		{0x09, "failed"},
		{0x03, "predicted_failure"},
	}
	for _, c := range cases {
		if got := NVMeState(c.warning); got != c.want {
			t.Errorf("warning %#02x = %s, want %s", c.warning, got, c.want)
		}
	}
}
