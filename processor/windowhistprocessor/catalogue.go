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

// catalogue lists the built-in groups (SPARC 05, default histogram groups).
// The plain group lists every scraper that co-scrapes a plain metric.
var catalogue = map[string]catalogueEntry{
	"cpu": {
		Scrapers: []string{"cpu"},
		Mode:     ModeHistogram,
		Metrics:  []string{"system.cpu.utilization"},
	},
	"memory": {
		Scrapers: []string{"memory"},
		Mode:     ModeHistogram,
		Metrics:  []string{"system.memory.usage", "system.memory.utilization"},
	},
	"disk_latency": {
		Scrapers: []string{"disk", "perfcounters/physicaldisk"},
		Mode:     ModeHistogram,
		Metrics: []string{
			"system.disk.pending_operations",
			"windows.physicaldisk.queue_length",
			"windows.physicaldisk.avg_queue_length",
			"windows.physicaldisk.operation_latency",
			"windows.physicaldisk.idle_time",
		},
	},
	"disk_io_rate": {
		Scrapers:       []string{"disk", "perfcounters/physicaldisk"},
		Mode:           ModeHistogram,
		CounterSourced: true,
		Metrics: []string{
			"system.disk.io",
			"system.disk.operations",
			"windows.physicaldisk.io",
			"windows.physicaldisk.transfers",
		},
	},
	"network_rate": {
		Scrapers:       []string{"network"},
		Mode:           ModeHistogram,
		CounterSourced: true,
		Metrics: []string{
			"system.network.io",
			"system.network.packets",
			"system.network.errors",
			"system.network.dropped",
		},
	},
	"cpu_pressure": {
		Scrapers: []string{"perfcounters/system"},
		Mode:     ModeHistogram,
		Metrics:  []string{"windows.system.processor_queue_length"},
	},
	"memory_pages": {
		Scrapers: []string{"perfcounters/memory"},
		Mode:     ModeHistogram,
		Metrics:  []string{"windows.memory.pages"},
	},
	"network_pressure": {
		Scrapers: []string{"perfcounters/network"},
		Mode:     ModeHistogram,
		Metrics: []string{
			"windows.tcp.retransmitted_segments",
			"windows.network_interface.output_queue_length",
		},
	},
	"gpu": {
		Scrapers: []string{"gpu"},
		Mode:     ModeHistogram,
	},
	"psi": {
		Scrapers: []string{"psi"},
		Mode:     ModePlain,
	},
	"process": {
		Scrapers: []string{"process"},
		Mode:     ModePlain,
	},
	"filesystem": {
		Scrapers: []string{"filesystem"},
		Mode:     ModePlain,
	},
	"plain": {
		Scrapers: []string{"cpu", "network", "paging", "load", "perfcounters/memory"},
		Mode:     ModePlain,
		Metrics: []string{
			"system.cpu.time",
			"system.network.connections",
			"windows.memory.committed",
			"windows.memory.commit_limit",
		},
	},
}
