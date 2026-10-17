//go:build linux && cgo

package collect

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/nvmladapter"
)

const (
	realScrapes   = 10
	maxScrapeTime = 2500 * time.Millisecond
)

// The first scrape may overrun (D-12), so the bound covers scrapes 2..10.
func TestRealScrapeTiming(t *testing.T) {
	p, err := nvmladapter.NewPort()
	require.NoError(t, err)
	if err := p.Init(); err != nil {
		t.Skipf("nvml unavailable: %v", err)
	}
	defer p.Shutdown()

	c := New(p, identities(t, p), nil, time.Now(), zap.NewNop())
	var first, worst time.Duration
	for i := range realScrapes {
		begin := time.Now()
		c.Collect(begin)
		elapsed := time.Since(begin)
		if i == 0 {
			first = elapsed
		} else {
			worst = max(worst, elapsed)
		}
	}
	t.Logf("scrape_first_ms=%d scrape_max_ms=%d", first.Milliseconds(), worst.Milliseconds())
	require.LessOrEqual(t, worst, maxScrapeTime)
}
