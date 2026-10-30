// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windowhistprocessor

// Group modes.
const (
	ModeHistogram = "histogram"
	ModePlain     = "plain"
)

// Histogram temporalities.
const (
	TemporalityDelta      = "delta"
	TemporalityCumulative = "cumulative"
)

// catalogueEntry describes one built-in metric group.
type catalogueEntry struct {
	// Scrapers feeding the group; two groups sharing a scraper must share its scrape interval.
	Scrapers       []string
	Mode           string
	CounterSourced bool
	Metrics        []string
}

// catalogue lists the built-in groups.
// The plain group lists every scraper that co-scrapes a plain metric.
var catalogue = map[string]catalogueEntry{
	"cpu": {
		Scrapers: []string{"cpu"},
		Mode:     ModeHistogram,
		Metrics:  cpuMetrics,
	},
	"memory": {
		Scrapers: []string{"memory"},
		Mode:     ModeHistogram,
		Metrics:  memoryMetrics,
	},
	"disk_latency": {
		Scrapers: []string{"disk", "perfcounters/physicaldisk"},
		Mode:     ModeHistogram,
		Metrics:  diskLatencyMetrics,
	},
	"disk_io_rate": {
		Scrapers:       []string{"disk", "perfcounters/physicaldisk"},
		Mode:           ModeHistogram,
		CounterSourced: true,
		Metrics:        diskIORateMetrics,
	},
	"network_rate": {
		Scrapers:       []string{"network"},
		Mode:           ModeHistogram,
		CounterSourced: true,
		Metrics:        networkRateMetrics,
	},
	"cpu_pressure": {
		Scrapers: []string{"perfcounters/system"},
		Mode:     ModeHistogram,
		Metrics:  cpuPressureMetrics,
	},
	"memory_pages": {
		Scrapers: []string{"perfcounters/memory"},
		Mode:     ModeHistogram,
		Metrics:  memoryPagesMetrics,
	},
	"network_pressure": {
		Scrapers: []string{"perfcounters/network"},
		Mode:     ModeHistogram,
		Metrics:  networkPressureMetrics,
	},
	"gpu": {
		Scrapers: []string{"gpu"},
		Mode:     ModeHistogram,
		Metrics:  gpuMetrics,
	},
	"psi": {
		Scrapers: []string{"psi"},
		Mode:     ModePlain,
		Metrics:  psiMetrics,
	},
	"process": {
		Scrapers: []string{"process"},
		Mode:     ModePlain,
		Metrics:  processMetrics,
	},
	"filesystem": {
		Scrapers: []string{"filesystem"},
		Mode:     ModePlain,
		Metrics:  filesystemMetrics,
	},
	"plain": {
		Scrapers: []string{"cpu", "network", "paging", "load", "perfcounters/memory", "gpu"},
		Mode:     ModePlain,
		Metrics:  plainMetrics,
	},
}
