package collect

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/fakeport"
	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/port"
	"github.com/vladistan/otelcol-custom/receiver/gpureceiver/internal/semconv"
)

// portCase builds an uninitialised port. The real adapter
// case is appended from a linux&&cgo file; it skips when Init fails.
type portCase struct {
	name string
	new  func(t *testing.T) port.Port
	// skipIfUnavailable skips the case when Init fails, as on a host
	// without a GPU. The fake must always initialise.
	skipIfUnavailable bool
}

var portCases = []portCase{
	{name: "fake", new: newSuiteFake},
}

func newSuiteFake(t *testing.T) port.Port {
	t.Helper()
	p := fakeport.New().SetDriverVersion("550.54")
	fullDevice(p, "GPU-0", "A100")
	fullDevice(p, "GPU-1", "A100")
	return p
}

type scenario struct {
	name  string
	check func(t *testing.T, scrapes [][]point)
}

var scenarios = []scenario{
	{"every GPU reports an ok status", checkStatusOk},
	{"non-temperature points carry the identity attributes", checkIdentity},
	{"io totals never decrease", checkIoMonotonic},
	{"values are finite and non-negative", checkValuesSane},
	{"every semconv metric is present", checkMetricsPresent},
}

func scrapeThrice(t *testing.T, p port.Port) [][]point {
	t.Helper()
	c := New(p, identities(t, p), nil, t0, zap.NewNop())
	var scrapes [][]point
	for i := range 3 {
		scrapes = append(scrapes, flatten(c.Collect(t0.Add(time.Duration(i)*10*time.Second))))
	}
	return scrapes
}

func TestSwapSuite(t *testing.T) {
	for _, pc := range portCases {
		t.Run(pc.name, func(t *testing.T) {
			p := pc.new(t)
			if err := p.Init(); err != nil {
				if !pc.skipIfUnavailable {
					require.NoError(t, err)
				}
				t.Skipf("port %s unavailable: %v", pc.name, err)
			}
			defer p.Shutdown()
			scrapes := scrapeThrice(t, p)
			for _, sc := range scenarios {
				t.Run(sc.name, func(t *testing.T) { sc.check(t, scrapes) })
			}
		})
	}
}

func checkStatusOk(t *testing.T, scrapes [][]point) {
	for _, points := range scrapes {
		oks := 0
		for _, p := range pickAll(points, semconv.MetricStatus, semconv.AttrState, semconv.StateOK) {
			assert.Equal(t, int64(1), p.intVal)
			oks++
		}
		assert.NotZero(t, oks)
	}
}

func checkIdentity(t *testing.T, scrapes [][]point) {
	for _, points := range scrapes {
		require.NotEmpty(t, points)
		for _, p := range points {
			if p.name == semconv.MetricTemperature {
				continue
			}
			for _, key := range []string{
				semconv.AttrID, semconv.AttrName, semconv.AttrModel, semconv.AttrType, semconv.AttrVendor,
			} {
				assert.NotEmpty(t, p.attrs[key], "%s on %s", key, p.name)
			}
		}
	}
}

func checkIoMonotonic(t *testing.T, scrapes [][]point) {
	last := map[string]int64{}
	for _, points := range scrapes {
		for _, p := range named(points, semconv.MetricIO) {
			series := p.attrs[semconv.AttrID] + "/" + p.attrs[semconv.AttrNetworkDir]
			assert.GreaterOrEqual(t, p.intVal, last[series], series)
			last[series] = p.intVal
		}
	}
	assert.NotEmpty(t, last)
}

func checkValuesSane(t *testing.T, scrapes [][]point) {
	for _, points := range scrapes {
		require.NotEmpty(t, points)
		for _, p := range points {
			v := p.value()
			assert.False(t, math.IsNaN(v) || math.IsInf(v, 0), p.name)
			assert.GreaterOrEqual(t, v, 0.0, p.name)
		}
	}
}

func checkMetricsPresent(t *testing.T, scrapes [][]point) {
	seen := map[string]bool{}
	for _, p := range scrapes[0] {
		seen[p.name] = true
	}
	for _, name := range semconv.ToggleKeys() {
		assert.True(t, seen[name], name)
	}
}
