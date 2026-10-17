//go:build linux && cgo

package collect

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/nvmladapter"
	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
)

func init() {
	portCases = append(portCases, portCase{
		name: "nvml",
		new: func(t *testing.T) port.Port {
			t.Helper()
			p, err := nvmladapter.NewPort()
			require.NoError(t, err)
			return p
		},
		skipIfUnavailable: true,
	})
}
