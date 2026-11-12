//go:build linux && cgo

package nvmladapter

import "github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"

// NewPort returns the uninitialised adapter. NVML opens at the first Init.
// The library path is a Windows setting and is ignored here; the config
// rejects it on other systems.
func NewPort(string) (port.Port, error) {
	return &adapter{}, nil
}
