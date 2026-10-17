package fakeport

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
)

func TestScriptedErrorsArriveOnTheScheduledCall(t *testing.T) {
	dev := New().AddDevice("GPU-0", "A100")
	dev.Set(CallTemperature, uint32(60)).
		Queue(CallTemperature, Ok(uint32(61)), Fail(port.NotSupported), Fail(port.GPULost))

	first, err := dev.Temperature()
	require.NoError(t, err)
	assert.Equal(t, uint32(61), first)

	_, err = dev.Temperature()
	assert.Equal(t, port.NotSupported, port.KindOf(err))

	_, err = dev.Temperature()
	assert.Equal(t, port.GPULost, port.KindOf(err))

	steady, err := dev.Temperature()
	require.NoError(t, err)
	assert.Equal(t, uint32(60), steady)
	assert.Equal(t, 4, dev.Count(CallTemperature))
}

func TestTransientErrorKindIsReturned(t *testing.T) {
	dev := New().AddDevice("GPU-0", "A100")
	dev.Queue(CallPowerUsage, Fail(port.Other))

	_, err := dev.PowerUsage()

	assert.Equal(t, port.Other, port.KindOf(err))
}

func TestScriptedInitFailureReturnsChosenKind(t *testing.T) {
	for _, kind := range []port.Kind{
		port.LibraryNotFound, port.DriverNotLoaded, port.NoPermission, port.VersionMismatch, port.Other,
	} {
		t.Run(kind.String(), func(t *testing.T) {
			p := New().FailInit(kind)

			assert.Equal(t, kind, port.KindOf(p.Init()))
			assert.Equal(t, 1, p.Calls("Init"))
		})
	}
}

func TestUnscriptedCallIsNotSupported(t *testing.T) {
	dev := New().AddDevice("GPU-0", "A100")

	_, err := dev.MemoryInfo()

	assert.Equal(t, port.NotSupported, port.KindOf(err))
}

func TestWrongScriptedTypeIsAnOtherError(t *testing.T) {
	dev := New().AddDevice("GPU-0", "A100")
	dev.Set(CallTemperature, "hot")

	_, err := dev.Temperature()

	assert.Equal(t, port.Other, port.KindOf(err))
	assert.ErrorContains(t, err, "scripted as string")
}

func TestPortEnumeratesDevicesAndDriver(t *testing.T) {
	p := New().SetDriverVersion("550.54")
	p.AddDevice("GPU-0", "A100")
	p.AddDevice("GPU-1", "H100")

	version, err := p.DriverVersion()
	require.NoError(t, err)
	count, err := p.DeviceCount()
	require.NoError(t, err)
	dev, err := p.Device(1)
	require.NoError(t, err)
	name, err := dev.Name()
	require.NoError(t, err)

	assert.Equal(t, "550.54", version)
	assert.Equal(t, 2, count)
	assert.Equal(t, "H100", name)
}

func TestPortFailuresReturnChosenKinds(t *testing.T) {
	p := New().FailDeviceCount(port.Other).FailDevice(0, port.GPULost)
	p.AddDevice("GPU-0", "A100")

	_, countErr := p.DeviceCount()
	_, deviceErr := p.Device(0)
	_, missingErr := p.Device(5)

	assert.Equal(t, port.Other, port.KindOf(countErr))
	assert.Equal(t, port.GPULost, port.KindOf(deviceErr))
	assert.Equal(t, port.Other, port.KindOf(missingErr))
}

func TestIdentityFailuresReturnChosenKinds(t *testing.T) {
	dev := New().AddDevice("GPU-0", "A100").FailUUID(port.Other).FailName(port.GPULost)

	_, uuidErr := dev.UUID()
	_, nameErr := dev.Name()

	assert.Equal(t, port.Other, port.KindOf(uuidErr))
	assert.Equal(t, port.GPULost, port.KindOf(nameErr))
}

func TestDirectionKindAndDomainSelectTheirScripts(t *testing.T) {
	dev := New().AddDevice("GPU-0", "A100")
	dev.Set(CallPCIeReceive, uint32(1)).Set(CallPCIeTransmit, uint32(2)).
		Set(CallECCCorrected, uint64(3)).Set(CallECCUncorrected, uint64(4)).
		Set(CallClockGraphics, uint32(5)).Set(CallClockSM, uint32(6)).
		Set(CallClockMemory, uint32(7)).Set(CallClockVideo, uint32(8))

	rx, _ := dev.PCIeThroughput(port.Receive)
	tx, _ := dev.PCIeThroughput(port.Transmit)
	corrected, _ := dev.ECCCount(port.Corrected)
	uncorrected, _ := dev.ECCCount(port.Uncorrected)
	graphics, _ := dev.Clock(port.ClockGraphics)
	sm, _ := dev.Clock(port.ClockSM)
	memory, _ := dev.Clock(port.ClockMemory)
	video, _ := dev.Clock(port.ClockVideo)

	assert.Equal(t, []uint32{1, 2, 5, 6, 7, 8}, []uint32{rx, tx, graphics, sm, memory, video})
	assert.Equal(t, []uint64{3, 4}, []uint64{corrected, uncorrected})
}

func TestScriptedUtilizationAndMemoryRoundTrip(t *testing.T) {
	dev := New().AddDevice("GPU-0", "A100")
	dev.Set(CallUtilization, port.Utilization{GPU: 75, Memory: 50}).
		Set(CallMemoryInfo, port.MemoryInfo{Used: 1, Total: 2}).
		Set(CallEncoderUtil, uint32(10)).Set(CallDecoderUtil, uint32(20)).
		Set(CallEnforcedPowerLimit, uint32(400000))

	util, _ := dev.Utilization()
	mem, _ := dev.MemoryInfo()
	enc, _ := dev.EncoderUtil()
	dec, _ := dev.DecoderUtil()
	limit, _ := dev.EnforcedPowerLimit()

	assert.Equal(t, port.Utilization{GPU: 75, Memory: 50}, util)
	assert.Equal(t, port.MemoryInfo{Used: 1, Total: 2}, mem)
	assert.Equal(t, []uint32{10, 20, 400000}, []uint32{enc, dec, limit})
}

func TestShutdownIsCounted(t *testing.T) {
	p := New()

	p.Shutdown()

	assert.Equal(t, 1, p.Calls("Shutdown"))
}
