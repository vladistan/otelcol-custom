// Modified: lazy NVML init on first scrape; metric building moved behind port.Collector.
// Original: https://github.com/pmady/otel-gpu-receiver@ac756935ca040923d98af5a0d0547dca02a3b23b/scraper.go

package gpureceiver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
)

type gpuScraper struct {
	logger *zap.Logger
	config *Config
	port   port.Port
	build  collectorBuilder
	now    func() time.Time

	startedAt time.Time
	initOnce  sync.Once

	mu        sync.Mutex
	initOK    bool
	collector port.Collector
}

func newGPUScraper(logger *zap.Logger, cfg *Config, p port.Port, build collectorBuilder) *gpuScraper {
	return &gpuScraper{
		logger: logger,
		config: cfg,
		port:   p,
		build:  build,
		now:    time.Now,
	}
}

// start records the start time only; NVML is opened by the first scrape.
func (s *gpuScraper) start(_ context.Context, _ component.Host) error {
	s.startedAt = s.now()
	return nil
}

// shutdown closes NVML only when the first scrape initialised it.
func (s *gpuScraper) shutdown(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initOK {
		s.port.Shutdown()
		s.initOK = false
	}
	return nil
}

// scrape never returns an error: a non-partial error would drop the batch.
func (s *gpuScraper) scrape(_ context.Context) (pmetric.Metrics, error) {
	s.initOnce.Do(s.initialise)

	s.mu.Lock()
	c := s.collector
	s.mu.Unlock()
	if c == nil {
		return pmetric.NewMetrics(), nil
	}
	return c.Collect(s.now()), nil
}

func (s *gpuScraper) initialise() {
	if err := s.port.Init(); err != nil {
		s.logger.Warn("NVML init failed; GPU receiver disabled until restart",
			zap.String("reason", port.KindOf(err).String()), zap.Error(err))
		return
	}
	s.mu.Lock()
	s.initOK = true
	s.mu.Unlock()

	version, err := s.port.DriverVersion()
	if err != nil {
		s.logger.Debug("driver version unavailable", zap.Error(err))
	}

	count, err := s.port.DeviceCount()
	if err != nil {
		s.logger.Warn("NVML device count failed; GPU receiver disabled until restart",
			zap.String("reason", port.KindOf(err).String()), zap.Error(err))
		return
	}
	if count == 0 {
		s.logger.Warn("No NVIDIA GPU devices found")
		return
	}

	devices := s.enumerate(count, version)
	if len(devices) == 0 {
		s.logger.Warn("No usable NVIDIA GPU devices")
		return
	}
	s.logger.Info("GPU receiver started",
		zap.Int("devices", len(devices)), zap.String("driver_version", version))

	collector := s.build(s.port, devices, s.config.Toggles(), s.startedAt, s.logger)
	s.mu.Lock()
	s.collector = collector
	s.mu.Unlock()
}

func (s *gpuScraper) enumerate(count int, driverVersion string) []port.DeviceIdentity {
	devices := make([]port.DeviceIdentity, 0, count)
	for i := range count {
		dev, err := s.port.Device(i)
		if err != nil {
			s.warnExcluded(i, err)
			continue
		}
		uuid, err := dev.UUID()
		if err != nil {
			s.warnExcluded(i, err)
			continue
		}
		model, err := dev.Name()
		if err != nil {
			s.warnExcluded(i, err)
			continue
		}
		devices = append(devices, port.DeviceIdentity{
			Index:         i,
			UUID:          uuid,
			Model:         model,
			Name:          fmt.Sprintf("gpu%d", i),
			DriverVersion: driverVersion,
		})
	}
	return devices
}

func (s *gpuScraper) warnExcluded(index int, err error) {
	s.logger.Warn("GPU excluded: enumeration failed",
		zap.Int("gpu_index", index),
		zap.String("reason", port.KindOf(err).String()), zap.Error(err))
}
