package collect

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBatchPanicsOnMetricWithoutSpec(t *testing.T) {
	b := newBatch(t0, t0)

	assert.PanicsWithValue(t, "collect: metric has no spec: hw.unknown", func() { b.metric("hw.unknown") })
}
