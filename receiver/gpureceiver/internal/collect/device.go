package collect

import (
	"go.uber.org/zap"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
)

type logKey struct {
	call string
	kind port.Kind
}

// deviceState is the history one GPU needs between scrapes (C-4).
type deviceState struct {
	id          port.DeviceIdentity
	handle      port.Device
	lost        bool
	unsupported map[string]bool
	logged      map[logKey]bool
	io          map[port.Direction]*ioIntegrator
	ecc         map[port.ECCKind]*eccIntegrator
}

func newDeviceState(id port.DeviceIdentity) *deviceState {
	return &deviceState{
		id:          id,
		unsupported: map[string]bool{},
		logged:      map[logKey]bool{},
		io: map[port.Direction]*ioIntegrator{
			port.Receive:  {},
			port.Transmit: {},
		},
		ecc: map[port.ECCKind]*eccIntegrator{
			port.Corrected:   {},
			port.Uncorrected: {},
		},
	}
}

// recordFailure applies the error contract: NotSupported is remembered and
// logged once, GPULost is sticky, anything else warns once per call and kind
// and then logs at debug.
func (d *deviceState) recordFailure(logger *zap.Logger, call string, err error) {
	kind := port.KindOf(err)
	fields := []zap.Field{
		zap.Int("gpu_index", d.id.Index),
		zap.String("gpu_uuid", d.id.UUID),
		zap.String("call", call),
		zap.String("kind", kind.String()),
		zap.Error(err),
	}
	switch kind {
	case port.NotSupported:
		d.unsupported[call] = true
		logger.Info("GPU call not supported; metric omitted for this GPU", fields...)
	case port.GPULost:
		d.lost = true
		logger.Warn("GPU lost; only hw.status is reported from now on", fields...)
	default:
		key := logKey{call: call, kind: kind}
		if d.logged[key] {
			logger.Debug("GPU call failed", fields...)
			return
		}
		d.logged[key] = true
		logger.Warn("GPU call failed; point skipped this scrape", fields...)
	}
}
