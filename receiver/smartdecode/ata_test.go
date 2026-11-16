// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartdecode

import (
	"errors"
	"testing"
)

func allowedATACommands() map[string]ATACommand {
	return map[string]ATACommand{
		"identify":         IdentifyCommand(),
		"read data":        SmartReadDataCommand(),
		"read thresholds":  SmartReadThresholdsCommand(),
		"return status":    SmartReturnStatusCommand(),
		"check power mode": CheckPowerModeCommand(),
		"read log ext":     DeviceStatisticsCommand(5),
	}
}

func TestCheckATA_OnlyTheSixListedCommandsPass(t *testing.T) {
	for name, cmd := range allowedATACommands() {
		if err := CheckATA(cmd); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	// Every command and feature byte, on every allowed command as the template for the other fields;
	// the commands that pass, without repeats, are the six.
	passed := map[ATACommand]bool{}
	for _, template := range allowedATACommands() {
		for command := 0; command < 256; command++ {
			for features := 0; features < 256; features++ {
				cmd := template
				cmd.Command, cmd.Features = uint8(command), uint8(features)
				if CheckATA(cmd) == nil {
					passed[cmd] = true
				}
			}
		}
	}
	if len(passed) != 6 {
		t.Errorf("%d distinct commands pass, want 6 (identify, three SMART reads, check power mode, read log ext)", len(passed))
	}
}

// isAllowedATA is the list written out: one of the six commands, READ LOG EXT with any page.
func isAllowedATA(cmd ATACommand) bool {
	for _, want := range allowedATACommands() {
		if want.Command == cmdReadLogExt {
			want.LBAMid = cmd.LBAMid
		}
		if cmd == want {
			return true
		}
	}
	return false
}

func TestCheckATA_AnyOtherValueInAnyFieldIsRefused(t *testing.T) {
	registers := map[string]func(c *ATACommand, v uint8){
		"Features":    func(c *ATACommand, v uint8) { c.Features = v },
		"SectorCount": func(c *ATACommand, v uint8) { c.SectorCount = v },
		"LBALow":      func(c *ATACommand, v uint8) { c.LBALow = v },
		"LBAMid":      func(c *ATACommand, v uint8) { c.LBAMid = v },
		"LBAHigh":     func(c *ATACommand, v uint8) { c.LBAHigh = v },
		"Device":      func(c *ATACommand, v uint8) { c.Device = v },
	}
	for name, base := range allowedATACommands() {
		for field, set := range registers {
			for v := 0; v < 256; v++ {
				cmd := base
				set(&cmd, uint8(v))
				if got := CheckATA(cmd) == nil; got != isAllowedATA(cmd) {
					t.Errorf("%s with %s=%#02x: allowed = %v", name, field, v, got)
				}
			}
		}
		for _, dataLen := range []int{-1, 0, 1, 511, 513, 4096, 1 << 30} {
			cmd := base
			cmd.DataLen = dataLen
			if got := CheckATA(cmd) == nil; got != isAllowedATA(cmd) {
				t.Errorf("%s with DataLen=%d: allowed = %v", name, dataLen, got)
			}
		}
		flipped := base
		flipped.Protocol = ProtoPIOIn - base.Protocol
		if CheckATA(flipped) == nil {
			t.Errorf("%s with the other protocol is allowed", name)
		}
		flipped = base
		flipped.WantRegisters = !base.WantRegisters
		if CheckATA(flipped) == nil {
			t.Errorf("%s with WantRegisters flipped is allowed", name)
		}
	}
}

func TestATACommand_OnlyReadLogExtIs48Bit(t *testing.T) {
	for name, cmd := range allowedATACommands() {
		if got, want := cmd.Is48Bit(), name == "read log ext"; got != want {
			t.Errorf("%s: Is48Bit = %v, want %v", name, got, want)
		}
	}
}

func TestCheckATA_SmartWritesAreRefused(t *testing.T) {
	for name, features := range map[string]uint8{
		"enable": 0xD8, "disable": 0xD9, "autosave": 0xD2, "offline immediate": 0xD4, "enable operations": 0xD8,
	} {
		cmd := SmartReadDataCommand()
		cmd.Features = features
		if err := CheckATA(cmd); !errors.Is(err, ErrCommandNotAllowed) {
			t.Errorf("SMART %s (feature %#x): err = %v, want ErrCommandNotAllowed", name, features, err)
		}
	}
}

func TestCheckATA_SmartNeedsTheKeyRegisters(t *testing.T) {
	cmd := SmartReadDataCommand()
	cmd.LBAMid = 0
	if err := CheckATA(cmd); !errors.Is(err, ErrCommandNotAllowed) {
		t.Errorf("SMART without 4Fh/C2h: err = %v, want ErrCommandNotAllowed", err)
	}
}

func TestCheckATA_ReadLogExtOnlyForTheDeviceStatisticsLog(t *testing.T) {
	cmd := DeviceStatisticsCommand(1)
	cmd.LBALow = 0x10 // the NCQ error log
	if err := CheckATA(cmd); !errors.Is(err, ErrCommandNotAllowed) {
		t.Errorf("READ LOG EXT of log 10h: err = %v, want ErrCommandNotAllowed", err)
	}
}

func TestParseIdentify_ReadsIdentitySizeAndRotation(t *testing.T) {
	buf := identifyBuffer(identifySpec{
		model: "ACME SSD 1TB", serial: "SN12345", firmware: "FW01",
		sectors: 1953525168, rotation: 1, wwn: 0x5000c5001234abcd,
		smartSupported: true, smartEnabled: true,
	})

	got, err := ParseIdentify(buf)

	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "ACME SSD 1TB" || got.Serial != "SN12345" || got.Firmware != "FW01" {
		t.Errorf("identity = %q %q %q", got.Model, got.Serial, got.Firmware)
	}
	if got.SizeBytes != 1953525168*512 {
		t.Errorf("size = %d, want %d", got.SizeBytes, uint64(1953525168)*512)
	}
	if got.WWN != "5000c5001234abcd" {
		t.Errorf("wwn = %q", got.WWN)
	}
	if !got.SMARTSupported || !got.SMARTEnabled {
		t.Errorf("smart supported %v enabled %v, want both", got.SMARTSupported, got.SMARTEnabled)
	}
	if !got.IsSSD() {
		t.Error("rotation rate 1 must be an SSD")
	}
}

func TestParseIdentify_RotationRateGivesTheDiskType(t *testing.T) {
	for rate, wantSSD := range map[uint16]bool{1: true, 7200: false, 5400: false, 0: false} {
		got, err := ParseIdentify(identifyBuffer(identifySpec{model: "X", sectors: 1, rotation: rate}))
		if err != nil {
			t.Fatal(err)
		}
		if got.IsSSD() != wantSSD {
			t.Errorf("rate %d: IsSSD = %v, want %v", rate, got.IsSSD(), wantSSD)
		}
	}
}

func TestParseIdentify_LogicalSectorSizeScalesTheCapacity(t *testing.T) {
	got, err := ParseIdentify(identifyBuffer(identifySpec{model: "X", sectors: 1000, logicalSectorBytes: 4096}))
	if err != nil {
		t.Fatal(err)
	}
	if got.SizeBytes != 4096000 {
		t.Errorf("size = %d, want 4096000", got.SizeBytes)
	}
}

func TestParseIdentify_NoWWNLeavesItEmpty(t *testing.T) {
	got, _ := ParseIdentify(identifyBuffer(identifySpec{model: "X", sectors: 1}))
	if got.WWN != "" {
		t.Errorf("wwn = %q, want empty", got.WWN)
	}
}

func TestParseIdentify_ShortBufferIsAnError(t *testing.T) {
	if _, err := ParseIdentify(make([]byte, 100)); !errors.Is(err, ErrShortBuffer) {
		t.Errorf("err = %v, want ErrShortBuffer", err)
	}
}

func TestParseSmartData_MergesThresholdsAndSplitsRaw(t *testing.T) {
	attrs := []rawAttr{
		{id: 5, value: 100, worst: 100, threshold: 10, raw: rawNumber(3)},
		{id: 9, value: 98, worst: 98, threshold: 0, raw: rawNumber(100)},
		{id: 194, value: 60, worst: 50, threshold: 0, raw: [6]byte{38, 0, 45, 0, 20, 0}},
	}

	got, err := ParseSmartData(smartDataBuffer(attrs), thresholdBuffer(attrs))

	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d attributes, want 3", len(got))
	}
	if got[0].ID != 5 || got[0].Value != 100 || got[0].Worst != 100 || got[0].Threshold != 10 || got[0].Raw != 3 {
		t.Errorf("attribute 5 = %+v", got[0])
	}
	if got[2].Raw&0xff != 38 {
		t.Errorf("attribute 194 raw low byte = %d, want 38", got[2].Raw&0xff)
	}
}

func TestParseSmartData_RawIsAFortyEightBitNumber(t *testing.T) {
	attrs := []rawAttr{{id: 250, raw: [6]byte{1, 2, 3, 4, 5, 6}}}

	got, err := ParseSmartData(smartDataBuffer(attrs), thresholdBuffer(attrs))

	if err != nil {
		t.Fatal(err)
	}
	if got[0].Raw != 6618611909121 {
		t.Errorf("raw = %d, want 6618611909121", got[0].Raw)
	}
}

func TestParseSmartData_WithoutThresholdsStillDecodes(t *testing.T) {
	attrs := []rawAttr{{id: 5, value: 100, threshold: 10}}

	got, err := ParseSmartData(smartDataBuffer(attrs), nil)

	if err != nil {
		t.Fatal(err)
	}
	if got[0].HasThreshold {
		t.Error("an attribute without a threshold table must not claim one")
	}
}

func TestParseSmartData_ShortBufferIsAnError(t *testing.T) {
	if _, err := ParseSmartData(make([]byte, 10), nil); !errors.Is(err, ErrShortBuffer) {
		t.Errorf("err = %v, want ErrShortBuffer", err)
	}
}

func TestParseReturnStatus(t *testing.T) {
	cases := []struct {
		mid, high uint8
		want      Health
		wantErr   bool
	}{
		{0x4F, 0xC2, HealthOK, false},
		{0xF4, 0x2C, HealthPredictedFailure, false},
		{0x00, 0x00, HealthUnknown, true},
	}
	for _, c := range cases {
		got, err := ParseReturnStatus(ATARegisters{LBAMid: c.mid, LBAHigh: c.high})
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("%02x/%02x: got %v, %v; want %v, error %v", c.mid, c.high, got, err, c.want, c.wantErr)
		}
	}
}

func TestParseATASense_ReadsTheStatusReturnDescriptor(t *testing.T) {
	sense := make([]byte, 32)
	sense[0] = 0x72
	sense[7] = 14 // additional length
	d := sense[8:]
	d[0], d[1] = 0x09, 0x0C
	d[3] = 0x00  // error
	d[5] = 0xFF  // sector count
	d[9] = 0xF4  // lba mid
	d[11] = 0x2C // lba high
	d[13] = 0x50 // status

	regs, ok := ParseATASense(sense)

	if !ok {
		t.Fatal("descriptor not found")
	}
	if regs.LBAMid != 0xF4 || regs.LBAHigh != 0x2C || regs.SectorCount != 0xFF || regs.Status != 0x50 {
		t.Errorf("registers = %+v", regs)
	}
}

func TestParseATASense_NoDescriptorIsNotOK(t *testing.T) {
	if _, ok := ParseATASense(make([]byte, 32)); ok {
		t.Error("an empty sense buffer must not give registers")
	}
	if _, ok := ParseATASense(nil); ok {
		t.Error("a nil sense buffer must not give registers")
	}
}

func TestParseDeviceStatisticsTemperature(t *testing.T) {
	page := make([]byte, 512)
	page[0] = 5
	// current temperature at offset 8: supported (bit 63), valid (bit 62), value 41
	page[8] = 41
	page[15] = 0xC0

	got, ok := ParseDeviceStatisticsTemperature(page)

	if !ok || got != 41 {
		t.Errorf("got %d, %v; want 41, true", got, ok)
	}
}

func TestParseDeviceStatisticsTemperature_InvalidValueIsNotOK(t *testing.T) {
	page := make([]byte, 512)
	page[8] = 41
	page[15] = 0x80 // supported, not valid

	if _, ok := ParseDeviceStatisticsTemperature(page); ok {
		t.Error("a value flagged not valid must be ignored")
	}
}

func TestParseDeviceStatisticsTemperature_NegativeValueIsSigned(t *testing.T) {
	page := make([]byte, 512)
	page[8] = 0xFB // -5
	page[15] = 0xC0

	got, ok := ParseDeviceStatisticsTemperature(page)

	if !ok || got != -5 {
		t.Errorf("got %d, %v; want -5, true", got, ok)
	}
}

func TestAttributeName_KnownAndUnknown(t *testing.T) {
	if got := AttributeName(5); got != "Reallocated_Sector_Ct" {
		t.Errorf("attribute 5 = %q", got)
	}
	if got := AttributeName(250); got != "Attribute_250" {
		t.Errorf("attribute 250 = %q, want Attribute_250", got)
	}
}

func TestDefaultAttributeIDs_AreTheTwentyOfTheDefaultList(t *testing.T) {
	want := []uint8{1, 5, 9, 12, 177, 179, 181, 182, 187, 188, 190, 194, 196, 197, 198, 199, 231, 233, 241, 242}
	got := DefaultAttributeIDs()
	if len(got) != len(want) {
		t.Fatalf("got %d ids, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("id %d = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestIOUnit_KnownOnlyForTheWrittenAndReadCounters(t *testing.T) {
	for _, id := range []uint8{241, 242} {
		if bytes, ok := IOUnitBytes(id); !ok || bytes != 512 {
			t.Errorf("attribute %d unit = %d, %v; want 512, true", id, bytes, ok)
		}
	}
	if _, ok := IOUnitBytes(9); ok {
		t.Error("attribute 9 has no I/O unit")
	}
}
