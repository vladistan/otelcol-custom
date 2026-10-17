//go:build linux && cgo

package nvmladapter

import (
	"testing"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/assert"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
)

func TestMappingReturnCodeToKind(t *testing.T) {
	tests := []struct {
		ret  gonvml.Return
		want port.Kind
	}{
		{gonvml.ERROR_NOT_SUPPORTED, port.NotSupported},
		{gonvml.ERROR_FREQ_NOT_SUPPORTED, port.NotSupported},
		{gonvml.ERROR_GPU_IS_LOST, port.GPULost},
		{gonvml.ERROR_LIBRARY_NOT_FOUND, port.LibraryNotFound},
		{gonvml.ERROR_DRIVER_NOT_LOADED, port.DriverNotLoaded},
		{gonvml.ERROR_NO_PERMISSION, port.NoPermission},
		{gonvml.ERROR_LIB_RM_VERSION_MISMATCH, port.VersionMismatch},
		{gonvml.ERROR_ARGUMENT_VERSION_MISMATCH, port.VersionMismatch},
		{gonvml.ERROR_UNINITIALIZED, port.Other},
		{gonvml.ERROR_INVALID_ARGUMENT, port.Other},
		{gonvml.ERROR_TIMEOUT, port.Other},
		{gonvml.ERROR_UNKNOWN, port.Other},
	}
	for _, tt := range tests {
		t.Run(tt.ret.Error(), func(t *testing.T) {
			assert.Equal(t, tt.want, port.KindOf(check(tt.ret)))
		})
	}
}

func TestCheckPassesSuccessAsNil(t *testing.T) {
	assert.NoError(t, check(gonvml.SUCCESS))
}

func TestNewPortReturnsUninitialisedAdapter(t *testing.T) {
	p, err := NewPort()

	assert.NoError(t, err)
	assert.NotNil(t, p)
}
