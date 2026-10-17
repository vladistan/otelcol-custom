//go:build !(linux && cgo)

package nvmladapter

import "github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"

// NewPort reports ErrPlatformUnsupported on this build.
func NewPort() (port.Port, error) {
	return nil, ErrPlatformUnsupported
}
