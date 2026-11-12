package nvmladapter

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
)

func TestKindOfCode(t *testing.T) {
	tests := []struct {
		name string
		code uint32
		want port.Kind
	}{
		{"not supported", 3, port.NotSupported},
		{"frequency not supported", 24, port.NotSupported},
		{"gpu is lost", 15, port.GPULost},
		{"library not found", 12, port.LibraryNotFound},
		{"driver not loaded", 9, port.DriverNotLoaded},
		{"no permission", 4, port.NoPermission},
		{"rm version mismatch", 18, port.VersionMismatch},
		{"argument version mismatch", 25, port.VersionMismatch},
		{"uninitialized", 1, port.Other},
		{"invalid argument", 2, port.Other},
		{"timeout", 10, port.Other},
		{"unknown", 999, port.Other},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, kindOfCode(tt.code))
		})
	}
}

func TestCheckCodePassesSuccessAsNil(t *testing.T) {
	assert.NoError(t, checkCode(0))
}

func TestCheckCodeCarriesKindAndCode(t *testing.T) {
	err := checkCode(15)

	assert.Equal(t, port.GPULost, port.KindOf(err))
	assert.ErrorContains(t, err, "nvml return code 15")
}

func TestStructLayoutsMatchNVMLHeader(t *testing.T) {
	assert.EqualValues(t, 8, unsafe.Sizeof(utilization{}))
	assert.EqualValues(t, 0, unsafe.Offsetof(utilization{}.gpu))
	assert.EqualValues(t, 4, unsafe.Offsetof(utilization{}.memory))

	assert.EqualValues(t, 24, unsafe.Sizeof(memoryInfo{}))
	assert.EqualValues(t, 0, unsafe.Offsetof(memoryInfo{}.total))
	assert.EqualValues(t, 8, unsafe.Offsetof(memoryInfo{}.free))
	assert.EqualValues(t, 16, unsafe.Offsetof(memoryInfo{}.used))
}

func TestPCIeCounterOf(t *testing.T) {
	tests := []struct {
		dir  port.Direction
		want uint32
		ok   bool
	}{
		{port.Transmit, 0, true},
		{port.Receive, 1, true},
		{port.Direction(0), 0, false},
		{port.Direction(9), 0, false},
	}
	for _, tt := range tests {
		got, ok := pcieCounterOf(tt.dir)

		assert.Equal(t, tt.ok, ok)
		assert.Equal(t, tt.want, got)
	}
}

func TestECCErrorTypeOf(t *testing.T) {
	tests := []struct {
		kind port.ECCKind
		want uint32
		ok   bool
	}{
		{port.Corrected, 0, true},
		{port.Uncorrected, 1, true},
		{port.ECCKind(0), 0, false},
		{port.ECCKind(9), 0, false},
	}
	for _, tt := range tests {
		got, ok := eccErrorTypeOf(tt.kind)

		assert.Equal(t, tt.ok, ok)
		assert.Equal(t, tt.want, got)
	}
}

func TestClockTypeOf(t *testing.T) {
	tests := []struct {
		domain port.ClockDomain
		want   uint32
		ok     bool
	}{
		{port.ClockGraphics, 0, true},
		{port.ClockSM, 1, true},
		{port.ClockMemory, 2, true},
		{port.ClockVideo, 3, true},
		{port.ClockDomain(0), 0, false},
		{port.ClockDomain(9), 0, false},
	}
	for _, tt := range tests {
		got, ok := clockTypeOf(tt.domain)

		assert.Equal(t, tt.ok, ok)
		assert.Equal(t, tt.want, got)
	}
}

func TestCString(t *testing.T) {
	tests := []struct {
		name string
		buf  []byte
		want string
	}{
		{"stops at the first NUL", []byte("NVIDIA L4\x00garbage"), "NVIDIA L4"},
		{"no NUL returns the whole buffer", []byte("abc"), "abc"},
		{"leading NUL is empty", []byte("\x00abc"), ""},
		{"empty buffer", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, cString(tt.buf))
		})
	}
}

func TestIsWindowsAbsPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{`C:\Windows\System32\nvml.dll`, true},
		{`c:/Program Files/NVIDIA Corporation/NVSMI/nvml.dll`, true},
		{`\\server\share\nvml.dll`, true},
		{`nvml.dll`, false},
		{`.\nvml.dll`, false},
		{`C:nvml.dll`, false},
		{`\Windows\System32\nvml.dll`, false},
		{``, false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			assert.Equal(t, tt.want, IsWindowsAbsPath(tt.path))
		})
	}
}
