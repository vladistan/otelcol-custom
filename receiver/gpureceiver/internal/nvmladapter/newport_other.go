//go:build !(linux && cgo) && !windows

package nvmladapter

import "github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"

// NewPort reports ErrPlatformUnsupported on this build.
func NewPort(string) (port.Port, error) {
	return nil, ErrPlatformUnsupported
}
