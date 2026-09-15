package psireceiver

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

// psiScraper scrapes PSI metrics from /proc/pressure.
type psiScraper struct {
	logger   *zap.Logger
	procPath string
}

// psiData holds parsed PSI values for a single resource type.
type psiData struct {
	SomeAvg10  float64
	SomeAvg60  float64
	SomeAvg300 float64
	SomeTotal  uint64
	FullAvg10  float64
	FullAvg60  float64
	FullAvg300 float64
	FullTotal  uint64
	HasFull    bool
}

func newPSIScraper(logger *zap.Logger, cfg *Config) (*psiScraper, error) {
	return &psiScraper{
		logger:   logger,
		procPath: cfg.ProcPath,
	}, nil
}

func (s *psiScraper) scrape(ctx context.Context) (pmetric.Metrics, error) {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	sm := rm.ScopeMetrics().AppendEmpty()
	sm.Scope().SetName("github.com/vladistan/otelcol-custom/receiver/psireceiver")
	sm.Scope().SetVersion("0.1.0")

	now := pcommon.NewTimestampFromTime(time.Now())

	resources := []string{"cpu", "memory", "io"}
	for _, resource := range resources {
		data, err := s.readPSI(resource)
		if err != nil {
			s.logger.Warn("Failed to read PSI data",
				zap.String("resource", resource),
				zap.Error(err))
			continue
		}

		// Add "some" metrics (all resources have this)
		s.addGaugeMetric(sm, fmt.Sprintf("system.pressure.%s.some.avg10", resource), data.SomeAvg10, now)
		s.addGaugeMetric(sm, fmt.Sprintf("system.pressure.%s.some.avg60", resource), data.SomeAvg60, now)
		s.addGaugeMetric(sm, fmt.Sprintf("system.pressure.%s.some.avg300", resource), data.SomeAvg300, now)

		// Add "full" metrics (memory and io only, not cpu)
		if data.HasFull {
			s.addGaugeMetric(sm, fmt.Sprintf("system.pressure.%s.full.avg10", resource), data.FullAvg10, now)
			s.addGaugeMetric(sm, fmt.Sprintf("system.pressure.%s.full.avg60", resource), data.FullAvg60, now)
			s.addGaugeMetric(sm, fmt.Sprintf("system.pressure.%s.full.avg300", resource), data.FullAvg300, now)
		}
	}

	return md, nil
}

func (s *psiScraper) addGaugeMetric(sm pmetric.ScopeMetrics, name string, value float64, ts pcommon.Timestamp) {
	m := sm.Metrics().AppendEmpty()
	m.SetName(name)
	m.SetUnit("%")
	m.SetDescription("Pressure stall information percentage")
	g := m.SetEmptyGauge()
	dp := g.DataPoints().AppendEmpty()
	dp.SetTimestamp(ts)
	dp.SetDoubleValue(value)
}

// readPSI reads and parses a PSI file (cpu, memory, or io).
// Format example:
//
//	some avg10=0.00 avg60=0.00 avg300=0.00 total=123456
//	full avg10=0.00 avg60=0.00 avg300=0.00 total=123456
func (s *psiScraper) readPSI(resource string) (*psiData, error) {
	path := filepath.Join(s.procPath, "pressure", resource)
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			s.logger.Debug("Failed to close PSI file",
				zap.String("path", path),
				zap.Error(cerr))
		}
	}()

	data := &psiData{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if err := parsePSILine(line, data); err != nil {
			s.logger.Debug("Failed to parse PSI line",
				zap.String("line", line),
				zap.Error(err))
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}

	return data, nil
}

// parsePSILine parses a single line from a PSI file.
// Example: "some avg10=0.00 avg60=0.00 avg300=0.00 total=123456"
func parsePSILine(line string, data *psiData) error {
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return fmt.Errorf("invalid PSI line format: %s", line)
	}

	lineType := fields[0]
	isFull := lineType == "full"
	if isFull {
		data.HasFull = true
	}

	for _, field := range fields[1:] {
		parts := strings.SplitN(field, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, valStr := parts[0], parts[1]

		switch key {
		case "avg10":
			val, err := strconv.ParseFloat(valStr, 64)
			if err != nil {
				return err
			}
			if isFull {
				data.FullAvg10 = val
			} else {
				data.SomeAvg10 = val
			}
		case "avg60":
			val, err := strconv.ParseFloat(valStr, 64)
			if err != nil {
				return err
			}
			if isFull {
				data.FullAvg60 = val
			} else {
				data.SomeAvg60 = val
			}
		case "avg300":
			val, err := strconv.ParseFloat(valStr, 64)
			if err != nil {
				return err
			}
			if isFull {
				data.FullAvg300 = val
			} else {
				data.SomeAvg300 = val
			}
		case "total":
			val, err := strconv.ParseUint(valStr, 10, 64)
			if err != nil {
				return err
			}
			if isFull {
				data.FullTotal = val
			} else {
				data.SomeTotal = val
			}
		}
	}

	return nil
}
