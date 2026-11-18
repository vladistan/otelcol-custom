// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap/zapcore"
)

const txgsHeader = "26 1 0x01 12 3360 4733209253 8388345122467\n" +
	"txg      birth            state ndirty       nread        nwritten     reads    writes   otime        qtime        wtime        stime\n"

// txgsFile is a txgs file: closed rows 100 and 102, an open row 103 and a syncing row 101.
func txgsFile() string {
	return txgsHeader +
		"100      1000             C     4096         0            8192         0        2        5000         100          200          3000000000\n" +
		"102      3000             C     65536        0            131072       0        9        5000         100          200          1500000000\n" +
		"101      2000             S     1            0            1            0        1        5000         100          200          9\n" +
		"103      4000             O     0            0            0            0        0        0            0            0            0\n"
}

func TestDatasetKstatWithABadFieldOrNoNameIsHandledFieldByField(t *testing.T) {
	cfg := hostConfig()
	cfg.Datasets = true
	cfg.KstatPath = writeTree(t, map[string]string{
		"tank/state":      "ONLINE\n",
		"tank/objset-0x1": objset("tank/home", "writes 4 oops\nnwritten 4 9\n"),
		"tank/objset-0x2": "50 1 0x01 7 2160 5 6\nname type data\nnwritten 4 100\n",
	})
	s, _ := newTestScraper(cfg)

	md := scrapeOnce(t, s)

	bytes := findMetric(t, md, "zfs.dataset.io.bytes")
	assert.Equal(t, 1, dataPoints(bytes).Len(), "only the nwritten of the named dataset")
	assert.NotContains(t, metricNames(md), "zfs.dataset.io.operations", "writes is not a number")
}

func TestTxgMetricsComeFromTheNewestClosedRow(t *testing.T) {
	cfg := hostConfig()
	cfg.KstatPath = writeTree(t, map[string]string{"tank/state": "ONLINE\n", "tank/txgs": txgsFile()})
	s, logs := newTestScraper(cfg)

	md := scrapeOnce(t, s)

	dirty := findMetric(t, md, "zfs.pool.txg.dirty")
	assert.Equal(t, pmetric.MetricTypeGauge, dirty.Type())
	assert.Equal(t, "By", dirty.Unit())
	assert.Equal(t, int64(65536), pointFor(t, dirty, map[string]string{"zfs.pool": "tank"}).IntValue())
	assert.Equal(t, int64(131072), pointFor(t, findMetric(t, md, "zfs.pool.txg.written"), map[string]string{"zfs.pool": "tank"}).IntValue())
	sync := findMetric(t, md, "zfs.pool.txg.sync.time")
	assert.Equal(t, "s", sync.Unit())
	assert.InDelta(t, 1.5, pointFor(t, sync, map[string]string{"zfs.pool": "tank"}).DoubleValue(), 1e-9)
	assert.Zero(t, logs.FilterMessageSnippet("txg").Len())
}

func TestTxgOffEmitsNoTxgMetric(t *testing.T) {
	cfg := hostConfig()
	cfg.Txg = false
	cfg.KstatPath = writeTree(t, map[string]string{"tank/state": "ONLINE\n", "tank/txgs": txgsFile()})
	s, logs := newTestScraper(cfg)

	md := scrapeOnce(t, s)

	for _, n := range metricNames(md) {
		assert.False(t, strings.HasPrefix(n, "zfs.pool.txg."), n)
	}
	assert.Zero(t, logs.FilterMessageSnippet("txg").Len(), "off means no read and no line")
}

func TestTxgIsOnByDefault(t *testing.T) {
	assert.True(t, createDefaultConfig().(*Config).Txg)
}

func TestTxgWithNoClosedRowOrNoFileGivesNoSeriesAndOneInfoLinePerPool(t *testing.T) {
	cfg := hostConfig()
	cfg.KstatPath = writeTree(t, map[string]string{
		"open/state": "ONLINE\n",
		"open/txgs":  txgsHeader + "5 1 O 0 0 0 0 0 0 0 0 0\n",
		"none/state": "ONLINE\n",
	})
	s, logs := newTestScraper(cfg)

	first := scrapeOnce(t, s)
	scrapeOnce(t, s)

	for _, n := range metricNames(first) {
		assert.False(t, strings.HasPrefix(n, "zfs.pool.txg."), n)
	}
	assert.Equal(t, 2, logs.FilterLevelExact(zapcore.InfoLevel).FilterMessageSnippet("txg").Len(), "one line per pool over two scrapes")
}

func TestTxgInfoLineReturnsAfterTheRowAppearsAndGoes(t *testing.T) {
	root := writeTree(t, map[string]string{"tank/state": "ONLINE\n", "tank/txgs": txgsHeader})
	cfg := hostConfig()
	cfg.KstatPath = root
	s, logs := newTestScraper(cfg)
	path := filepath.Join(root, "tank", "txgs")
	scrapeOnce(t, s)
	require.NoError(t, os.WriteFile(path, []byte(txgsFile()), 0o644))
	assert.Contains(t, metricNames(scrapeOnce(t, s)), "zfs.pool.txg.dirty")
	require.NoError(t, os.WriteFile(path, []byte(txgsHeader), 0o644))

	scrapeOnce(t, s)

	assert.Equal(t, 2, logs.FilterMessageSnippet("txg").Len())
}

func TestParseTxgs(t *testing.T) {
	tests := map[string]struct {
		text string
		want txgRow
		ok   bool
	}{
		"closed row":         {txgsHeader + "7 1 C 10 0 20 0 0 0 0 0 30\n", txgRow{10, 20, 30}, true},
		"highest txg wins":   {txgsHeader + "9 1 C 1 0 2 0 0 0 0 0 3\n8 1 C 4 0 5 0 0 0 0 0 6\n", txgRow{1, 2, 3}, true},
		"open row only":      {txgsHeader + "7 1 O 10 0 20 0 0 0 0 0 30\n", txgRow{}, false},
		"empty":              {"", txgRow{}, false},
		"no column line":     {"26 1 0x01 12\n7 1 C 1 0 2 0 0 0 0 0 3\n", txgRow{}, false},
		"short row":          {txgsHeader + "7 1 C 10\n", txgRow{}, false},
		"non numeric number": {txgsHeader + "7 1 C x 0 2 0 0 0 0 0 3\n", txgRow{}, false},
		"missing column":     {"txg birth state ndirty nwritten\n7 1 C 1 2\n", txgRow{}, false},
		"no state column":    {"txg birth ndirty nwritten stime\n7 1 1 2 3\n", txgRow{}, false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, ok := parseTxgs(tt.text)

			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTheArcSeriesCarryNoAttributes(t *testing.T) {
	s, _ := newTestScraper(hostConfig())

	md := scrapeOnce(t, s)

	for _, name := range []string{"zfs.arc.size", "zfs.arc.hits", "zfs.arc.misses"} {
		dps := dataPoints(findMetric(t, md, name))
		require.Equal(t, 1, dps.Len(), name)
		assert.Zero(t, dps.At(0).Attributes().Len(), name)
	}
}

func TestLinuxProcSourceEmitsNoSpaceSeries(t *testing.T) {
	cfg := hostConfig()
	s, _ := newTestScraper(cfg)

	md := scrapeOnce(t, s)

	for _, n := range metricNames(md) {
		assert.False(t, strings.HasPrefix(n, "zfs.dataset.space."), n)
	}
}

func TestDefaultsKeepTheOldOutputWhenDatasetsAndTxgAreOff(t *testing.T) {
	cfg := hostConfig()
	cfg.Txg = false
	s, _ := newTestScraper(cfg)

	names := metricNames(scrapeOnce(t, s))

	for _, n := range names {
		assert.False(t, strings.HasPrefix(n, "zfs.pool.txg.") || strings.HasPrefix(n, "zfs.dataset."), n)
	}
}
