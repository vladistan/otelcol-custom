package psireceiver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"go.uber.org/zap/zaptest"
)

func TestParsePSILine(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		expected psiData
		wantErr  bool
	}{
		{
			name: "some line",
			line: "some avg10=1.23 avg60=4.56 avg300=7.89 total=123456",
			expected: psiData{
				SomeAvg10:  1.23,
				SomeAvg60:  4.56,
				SomeAvg300: 7.89,
				SomeTotal:  123456,
			},
		},
		{
			name: "full line",
			line: "full avg10=0.50 avg60=1.00 avg300=2.00 total=789",
			expected: psiData{
				FullAvg10:  0.50,
				FullAvg60:  1.00,
				FullAvg300: 2.00,
				FullTotal:  789,
				HasFull:    true,
			},
		},
		{
			name:    "invalid format",
			line:    "invalid",
			wantErr: true,
		},
		{
			name:    "empty line",
			line:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := &psiData{}
			err := parsePSILine(tt.line, data)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected.SomeAvg10, data.SomeAvg10)
			assert.Equal(t, tt.expected.SomeAvg60, data.SomeAvg60)
			assert.Equal(t, tt.expected.SomeAvg300, data.SomeAvg300)
			assert.Equal(t, tt.expected.SomeTotal, data.SomeTotal)
			assert.Equal(t, tt.expected.FullAvg10, data.FullAvg10)
			assert.Equal(t, tt.expected.FullAvg60, data.FullAvg60)
			assert.Equal(t, tt.expected.FullAvg300, data.FullAvg300)
			assert.Equal(t, tt.expected.FullTotal, data.FullTotal)
			assert.Equal(t, tt.expected.HasFull, data.HasFull)
		})
	}
}

func TestReadPSI(t *testing.T) {
	// Create temp directory structure mimicking /proc/pressure
	tmpDir := t.TempDir()
	pressureDir := filepath.Join(tmpDir, "pressure")
	require.NoError(t, os.MkdirAll(pressureDir, 0755))

	// Write test CPU PSI file (no "full" line)
	cpuContent := `some avg10=0.00 avg60=0.00 avg300=0.00 total=0
`
	require.NoError(t, os.WriteFile(filepath.Join(pressureDir, "cpu"), []byte(cpuContent), 0644))

	// Write test memory PSI file (has "full" line)
	memContent := `some avg10=1.23 avg60=2.34 avg300=3.45 total=123456
full avg10=0.12 avg60=0.23 avg300=0.34 total=7890
`
	require.NoError(t, os.WriteFile(filepath.Join(pressureDir, "memory"), []byte(memContent), 0644))

	// Write test io PSI file
	ioContent := `some avg10=5.00 avg60=10.00 avg300=15.00 total=500000
full avg10=2.50 avg60=5.00 avg300=7.50 total=250000
`
	require.NoError(t, os.WriteFile(filepath.Join(pressureDir, "io"), []byte(ioContent), 0644))

	// Create scraper with test proc path
	scraper := &psiScraper{
		logger:   zaptest.NewLogger(t),
		procPath: tmpDir,
	}

	t.Run("cpu", func(t *testing.T) {
		data, err := scraper.readPSI("cpu")
		require.NoError(t, err)
		assert.Equal(t, 0.0, data.SomeAvg10)
		assert.False(t, data.HasFull, "CPU should not have full metrics")
	})

	t.Run("memory", func(t *testing.T) {
		data, err := scraper.readPSI("memory")
		require.NoError(t, err)
		assert.Equal(t, 1.23, data.SomeAvg10)
		assert.Equal(t, 2.34, data.SomeAvg60)
		assert.Equal(t, 3.45, data.SomeAvg300)
		assert.True(t, data.HasFull)
		assert.Equal(t, 0.12, data.FullAvg10)
	})

	t.Run("io", func(t *testing.T) {
		data, err := scraper.readPSI("io")
		require.NoError(t, err)
		assert.Equal(t, 5.0, data.SomeAvg10)
		assert.True(t, data.HasFull)
		assert.Equal(t, 2.5, data.FullAvg10)
	})
}

func TestScrape(t *testing.T) {
	// Create temp directory structure
	tmpDir := t.TempDir()
	pressureDir := filepath.Join(tmpDir, "pressure")
	require.NoError(t, os.MkdirAll(pressureDir, 0755))

	// Write test files
	require.NoError(t, os.WriteFile(
		filepath.Join(pressureDir, "cpu"),
		[]byte("some avg10=1.00 avg60=2.00 avg300=3.00 total=100\n"),
		0644))
	require.NoError(t, os.WriteFile(
		filepath.Join(pressureDir, "memory"),
		[]byte("some avg10=4.00 avg60=5.00 avg300=6.00 total=200\nfull avg10=1.00 avg60=2.00 avg300=3.00 total=100\n"),
		0644))
	require.NoError(t, os.WriteFile(
		filepath.Join(pressureDir, "io"),
		[]byte("some avg10=7.00 avg60=8.00 avg300=9.00 total=300\nfull avg10=4.00 avg60=5.00 avg300=6.00 total=200\n"),
		0644))

	// Create scraper
	logger := zaptest.NewLogger(t)
	cfg := &Config{ProcPath: tmpDir}
	scraper, err := newPSIScraper(logger, cfg)
	require.NoError(t, err)

	// Run scrape
	metrics, err := scraper.scrape(context.Background())
	require.NoError(t, err)

	// Verify metrics
	rm := metrics.ResourceMetrics()
	require.Equal(t, 1, rm.Len())

	sm := rm.At(0).ScopeMetrics()
	require.Equal(t, 1, sm.Len())

	// Count metrics: 3 resources x 3 "some" metrics + 2 resources x 3 "full" metrics = 15
	ms := sm.At(0).Metrics()
	assert.Equal(t, 15, ms.Len(), "Expected 15 PSI metrics")

	// Verify specific metric exists and has correct value
	found := false
	for i := 0; i < ms.Len(); i++ {
		m := ms.At(i)
		if m.Name() == "system.pressure.cpu.some.avg10" {
			found = true
			assert.Equal(t, 1.0, m.Gauge().DataPoints().At(0).DoubleValue())
		}
	}
	assert.True(t, found, "Expected to find system.pressure.cpu.some.avg10 metric")
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: Config{
				CollectionInterval: 60 * time.Second,
				ProcPath:           "/proc",
			},
			wantErr: false,
		},
		{
			name: "zero collection interval",
			cfg: Config{
				CollectionInterval: 0,
				ProcPath:           "/proc",
			},
			wantErr: true,
		},
		{
			name: "too short collection interval",
			cfg: Config{
				CollectionInterval: 500 * time.Millisecond,
				ProcPath:           "/proc",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestReceiverStartStop(t *testing.T) {
	// Create temp directory structure
	tmpDir := t.TempDir()
	pressureDir := filepath.Join(tmpDir, "pressure")
	require.NoError(t, os.MkdirAll(pressureDir, 0755))

	// Write minimal test files
	require.NoError(t, os.WriteFile(
		filepath.Join(pressureDir, "cpu"),
		[]byte("some avg10=0.00 avg60=0.00 avg300=0.00 total=0\n"),
		0644))
	require.NoError(t, os.WriteFile(
		filepath.Join(pressureDir, "memory"),
		[]byte("some avg10=0.00 avg60=0.00 avg300=0.00 total=0\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n"),
		0644))
	require.NoError(t, os.WriteFile(
		filepath.Join(pressureDir, "io"),
		[]byte("some avg10=0.00 avg60=0.00 avg300=0.00 total=0\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n"),
		0644))

	// Create receiver
	settings := receivertest.NewNopSettings()
	settings.Logger = zaptest.NewLogger(t)
	cfg := &Config{
		CollectionInterval: time.Second,
		ProcPath:           tmpDir,
	}
	sink := &consumertest.MetricsSink{}

	recv, err := newPSIReceiver(settings, cfg, sink)
	require.NoError(t, err)

	// Start receiver
	ctx := context.Background()
	err = recv.Start(ctx, nil)
	require.NoError(t, err)

	// Wait briefly to ensure collection happens
	time.Sleep(100 * time.Millisecond)

	// Stop receiver
	err = recv.Shutdown(ctx)
	require.NoError(t, err)

	// Verify metrics were collected
	assert.Greater(t, sink.DataPointCount(), 0, "Expected metrics to be collected")
}
