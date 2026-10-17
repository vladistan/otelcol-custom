//go:build linux && cgo

package nvmladapter

import "github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"

// NewPort returns the uninitialised adapter. NVML opens at the first Init.
func NewPort() (port.Port, error) {
	return &adapter{}, nil
}
