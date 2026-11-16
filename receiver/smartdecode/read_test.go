// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartdecode

import (
	"errors"
	"reflect"
	"testing"
)

// fakeATA answers from canned buffers, records the commands it receives and checks each against the
// allow list, as the real transports do.
type fakeATA struct {
	identify   []byte
	data       []byte
	thresholds []byte
	statsPage  []byte
	status     ATARegisters
	powerMode  uint8
	noRegs     bool
	failWith   error
	sent       []ATACommand
	refused    []ATACommand
}

func (f *fakeATA) ATA(cmd ATACommand) (ATAResult, error) {
	f.sent = append(f.sent, cmd)
	if err := CheckATA(cmd); err != nil {
		f.refused = append(f.refused, cmd)
		return ATAResult{}, err
	}
	if f.failWith != nil {
		return ATAResult{}, f.failWith
	}
	switch {
	case cmd.Command == 0xEC:
		return ATAResult{Data: f.identify}, nil
	case cmd.Command == 0xB0 && cmd.Features == 0xD0:
		return ATAResult{Data: f.data}, nil
	case cmd.Command == 0xB0 && cmd.Features == 0xD1:
		return ATAResult{Data: f.thresholds}, nil
	case cmd.Command == 0xB0 && cmd.Features == 0xDA:
		return ATAResult{Registers: f.status, HaveRegisters: !f.noRegs}, nil
	case cmd.Command == 0xE5:
		return ATAResult{Registers: ATARegisters{SectorCount: f.powerMode}, HaveRegisters: !f.noRegs}, nil
	case cmd.Command == 0x2F:
		return ATAResult{Data: f.statsPage}, nil
	}
	return ATAResult{}, errors.New("unexpected command")
}

func (f *fakeATA) commands() []uint8 {
	var out []uint8
	for _, c := range f.sent {
		if c.Command == 0xB0 {
			out = append(out, c.Features)
		} else {
			out = append(out, c.Command)
		}
	}
	return out
}

var healthyAttrs = []rawAttr{
	{id: 5, value: 100, worst: 100, threshold: 10, raw: rawNumber(0)},
	{id: 9, value: 98, worst: 98, raw: rawNumber(100)},
	{id: 194, value: 60, worst: 50, raw: [6]byte{38}},
}

func healthyDisk() *fakeATA {
	return &fakeATA{
		identify: identifyBuffer(identifySpec{
			model: "ACME SSD", serial: "SN1", firmware: "F1", sectors: 1000, rotation: 1,
			smartSupported: true, smartEnabled: true,
		}),
		data:       smartDataBuffer(healthyAttrs),
		thresholds: thresholdBuffer(healthyAttrs),
		status:     ATARegisters{LBAMid: 0x4F, LBAHigh: 0xC2},
		powerMode:  0xFF,
	}
}

func TestReadATA_SendsTheFourReadsInOrderAndDecodes(t *testing.T) {
	disk := healthyDisk()

	got, err := ReadATA(disk, ATAOptions{})

	if err != nil {
		t.Fatal(err)
	}
	if want := []uint8{0xEC, 0xD0, 0xD1, 0xDA}; !reflect.DeepEqual(disk.commands(), want) {
		t.Errorf("commands = %#v, want %#v", disk.commands(), want)
	}
	if got.Identify.Serial != "SN1" || len(got.Attributes) != 3 || got.Health != HealthOK {
		t.Errorf("got %+v", got)
	}
	if got.Attributes[0].Threshold != 10 {
		t.Errorf("attribute 5 threshold = %d, want 10", got.Attributes[0].Threshold)
	}
	if len(disk.refused) != 0 {
		t.Errorf("the allow list refused %v", disk.refused)
	}
}

func TestReadATA_PredictedFailureComesFromTheRegisters(t *testing.T) {
	disk := healthyDisk()
	disk.status = ATARegisters{LBAMid: 0xF4, LBAHigh: 0x2C}

	got, err := ReadATA(disk, ATAOptions{})

	if err != nil || got.Health != HealthPredictedFailure {
		t.Errorf("got health %v, err %v; want predicted failure", got.Health, err)
	}
}

func TestReadATA_NoRegistersLeavesHealthUnknown(t *testing.T) {
	disk := healthyDisk()
	disk.noRegs = true

	got, err := ReadATA(disk, ATAOptions{})

	if err != nil || got.Health != HealthUnknown {
		t.Errorf("got health %v, err %v; want unknown and no error", got.Health, err)
	}
}

func TestReadATA_StandbyDiskGetsOnlyCheckPowerMode(t *testing.T) {
	disk := healthyDisk()
	disk.powerMode = 0x00

	_, err := ReadATA(disk, ATAOptions{SkipIfStandby: true})

	if !errors.Is(err, ErrStandby) {
		t.Fatalf("err = %v, want ErrStandby", err)
	}
	if want := []uint8{0xE5}; !reflect.DeepEqual(disk.commands(), want) {
		t.Errorf("commands = %#v, want only CHECK POWER MODE", disk.commands())
	}
}

func TestReadATA_ActiveDiskIsReadAfterCheckPowerMode(t *testing.T) {
	disk := healthyDisk()

	_, err := ReadATA(disk, ATAOptions{SkipIfStandby: true})

	if err != nil {
		t.Fatal(err)
	}
	if want := []uint8{0xE5, 0xEC, 0xD0, 0xD1, 0xDA}; !reflect.DeepEqual(disk.commands(), want) {
		t.Errorf("commands = %#v, want %#v", disk.commands(), want)
	}
}

func TestReadATA_WithoutSkipIfStandbyNoPowerCheckIsSent(t *testing.T) {
	disk := healthyDisk()
	disk.powerMode = 0x00

	if _, err := ReadATA(disk, ATAOptions{}); err != nil {
		t.Fatal(err)
	}
	if disk.commands()[0] != 0xEC {
		t.Errorf("first command = %#v, want IDENTIFY", disk.commands()[0])
	}
}

func TestReadATA_SmartUnsupportedStopsAfterIdentify(t *testing.T) {
	disk := healthyDisk()
	disk.identify = identifyBuffer(identifySpec{model: "X", sectors: 1})

	_, err := ReadATA(disk, ATAOptions{})

	if !errors.Is(err, ErrSMARTUnsupported) {
		t.Fatalf("err = %v, want ErrSMARTUnsupported", err)
	}
	if want := []uint8{0xEC}; !reflect.DeepEqual(disk.commands(), want) {
		t.Errorf("commands = %#v, want only IDENTIFY", disk.commands())
	}
}

func TestReadATA_SmartDisabledIsNeverEnabled(t *testing.T) {
	disk := healthyDisk()
	disk.identify = identifyBuffer(identifySpec{model: "X", sectors: 1, smartSupported: true})

	_, err := ReadATA(disk, ATAOptions{})

	if !errors.Is(err, ErrSMARTDisabled) {
		t.Fatalf("err = %v, want ErrSMARTDisabled", err)
	}
	for _, c := range disk.sent {
		if c.Command == 0xB0 {
			t.Errorf("a SMART command %#02x was sent to a disk with SMART disabled", c.Features)
		}
	}
}

func TestReadATA_TemperatureFallsBackToDeviceStatistics(t *testing.T) {
	disk := healthyDisk()
	attrs := []rawAttr{{id: 9, value: 98, raw: rawNumber(100)}}
	disk.data, disk.thresholds = smartDataBuffer(attrs), thresholdBuffer(attrs)
	page := make([]byte, 512)
	page[8], page[15] = 33, 0xC0
	disk.statsPage = page

	got, err := ReadATA(disk, ATAOptions{})

	if err != nil {
		t.Fatal(err)
	}
	if !got.HasStatsTemperature || got.StatsTemperature != 33 {
		t.Errorf("stats temperature = %d, %v; want 33, true", got.StatsTemperature, got.HasStatsTemperature)
	}
	if last := disk.sent[len(disk.sent)-1]; last.Command != 0x2F || last.LBAMid != 5 {
		t.Errorf("last command = %+v, want READ LOG EXT of page 5", last)
	}
}

func TestReadATA_TemperatureAttributeMeansNoDeviceStatisticsRead(t *testing.T) {
	disk := healthyDisk()

	got, err := ReadATA(disk, ATAOptions{})

	if err != nil {
		t.Fatal(err)
	}
	if got.HasStatsTemperature {
		t.Error("device statistics were read although attribute 194 is present")
	}
	for _, c := range disk.sent {
		if c.Command == 0x2F {
			t.Error("READ LOG EXT was sent although attribute 194 is present")
		}
	}
}

func TestReadATA_TransportErrorsPassThrough(t *testing.T) {
	for _, want := range []error{ErrTimeout, ErrPermission, ErrUnsupported} {
		disk := healthyDisk()
		disk.failWith = want

		_, err := ReadATA(disk, ATAOptions{})

		if !errors.Is(err, want) {
			t.Errorf("err = %v, want it to wrap %v", err, want)
		}
	}
}

func TestReadATA_ShortIdentifyIsAnError(t *testing.T) {
	disk := healthyDisk()
	disk.identify = make([]byte, 10)

	if _, err := ReadATA(disk, ATAOptions{}); !errors.Is(err, ErrShortBuffer) {
		t.Errorf("err = %v, want ErrShortBuffer", err)
	}
}

// controllerATA stands for a RAID controller layer: it holds the same buffers and answers the same
// commands through another path.
type controllerATA struct{ disk *fakeATA }

func (c controllerATA) ATA(cmd ATACommand) (ATAResult, error) { return c.disk.ATA(cmd) }

func TestReadATA_TheSameBuffersDecodeTheSameThroughAnotherTransport(t *testing.T) {
	direct, err := ReadATA(healthyDisk(), ATAOptions{})
	if err != nil {
		t.Fatal(err)
	}
	behindController, err := ReadATA(controllerATA{healthyDisk()}, ATAOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(direct, behindController) {
		t.Errorf("direct %+v\ncontroller %+v", direct, behindController)
	}
}

type fakeNVMe struct {
	identify []byte
	log      []byte
	failWith error
	sent     []NVMeCommand
}

func (f *fakeNVMe) Admin(cmd NVMeCommand) ([]byte, error) {
	f.sent = append(f.sent, cmd)
	if err := CheckNVMe(cmd); err != nil {
		return nil, err
	}
	if f.failWith != nil {
		return nil, f.failWith
	}
	if cmd.Opcode == 0x06 {
		return f.identify, nil
	}
	return f.log, nil
}

func healthyNVMe() *fakeNVMe {
	return &fakeNVMe{
		identify: nvmeIdentifyBuffer("ACME NVMe", "SN9", "FW9", 1000),
		log:      nvmeSmartBuffer(nvmeSmartSpec{compositeKelvin: 318, percentageUsed: 7, powerOnHours: 100}),
	}
}

func TestReadNVMe_SendsIdentifyThenTheLogPage(t *testing.T) {
	ctrl := healthyNVMe()

	got, err := ReadNVMe(ctrl)

	if err != nil {
		t.Fatal(err)
	}
	if len(ctrl.sent) != 2 || ctrl.sent[0].Opcode != 0x06 || ctrl.sent[1].Opcode != 0x02 {
		t.Errorf("commands = %+v, want identify then get log page", ctrl.sent)
	}
	if got.Identify.Serial != "SN9" || got.Smart.CompositeCelsius != 45 || got.Smart.PercentageUsed != 7 {
		t.Errorf("got %+v", got)
	}
}

func TestReadNVMe_TransportErrorsPassThrough(t *testing.T) {
	for _, want := range []error{ErrTimeout, ErrPermission} {
		ctrl := healthyNVMe()
		ctrl.failWith = want

		if _, err := ReadNVMe(ctrl); !errors.Is(err, want) {
			t.Errorf("err = %v, want it to wrap %v", err, want)
		}
	}
}
