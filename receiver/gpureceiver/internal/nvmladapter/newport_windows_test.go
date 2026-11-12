//go:build windows

package nvmladapter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
)

func TestNewPortReturnsUninitialisedAdapter(t *testing.T) {
	p, err := NewPort("")

	require.NoError(t, err)
	assert.NotNil(t, p)
}

func TestNewPortRejectsRelativeLibraryPath(t *testing.T) {
	p, err := NewPort(`nvml.dll`)

	assert.Nil(t, p)
	assert.ErrorContains(t, err, "absolute")
}

func TestInitReportsLibraryNotFoundForMissingDLL(t *testing.T) {
	p, err := NewPort(`C:\does-not-exist\nvml.dll`)
	require.NoError(t, err)

	err = p.Init()

	assert.Equal(t, port.LibraryNotFound, port.KindOf(err))
}

func TestShutdownBeforeInitIsSafe(t *testing.T) {
	p, err := NewPort(`C:\does-not-exist\nvml.dll`)
	require.NoError(t, err)

	assert.NotPanics(t, p.Shutdown)
}

func TestDeviceCallBeforeInitFailsWithoutPanic(t *testing.T) {
	p, err := NewPort(`C:\does-not-exist\nvml.dll`)
	require.NoError(t, err)

	_, err = p.DeviceCount()

	assert.Error(t, err)
}
