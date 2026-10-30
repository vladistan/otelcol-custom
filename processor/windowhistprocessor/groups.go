// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package windowhistprocessor

import "fmt"

// Built-in metric lists per group.
// A group's `metrics` setting replaces its list.
var (
	// normalizedload runs ahead of windowhist and derives the .percpu and .normalized
	// names in this and the plain and process lists.
	cpuMetrics = []string{"system.cpu.utilization", "system.cpu.utilization.percpu"}

	memoryMetrics = []string{"system.memory.usage", "system.memory.utilization"}

	diskLatencyMetrics = []string{
		"system.disk.pending_operations",
		"windows.physicaldisk.queue_length",
		"windows.physicaldisk.avg_queue_length",
		"windows.physicaldisk.operation_latency",
		"windows.physicaldisk.idle_time",
	}

	diskIORateMetrics = []string{
		"system.disk.io",
		"system.disk.operations",
		"windows.physicaldisk.io",
		"windows.physicaldisk.transfers",
	}

	networkRateMetrics = []string{
		"system.network.io",
		"system.network.packets",
		"system.network.errors",
		"system.network.dropped",
	}

	cpuPressureMetrics = []string{"windows.system.processor_queue_length"}

	memoryPagesMetrics = []string{"windows.memory.pages"}

	networkPressureMetrics = []string{
		"windows.tcp.retransmitted_segments",
		"windows.network_interface.output_queue_length",
	}

	gpuMetrics = []string{
		"hw.gpu.utilization",
		"hw.gpu.memory.utilization",
		"hw.temperature",
		"hw.power",
	}

	psiMetrics = pressureMetrics()

	processMetrics = []string{
		"process.cpu.time",
		"process.cpu.utilization",
		"process.cpu.utilization.percpu",
		"process.memory.usage",
		"process.memory.virtual",
		"process.disk.io",
		"process.threads",
		"process.handles",
	}

	filesystemMetrics = []string{"system.filesystem.usage", "system.filesystem.utilization"}

	// plainMetrics are the counters, load, paging, committed memory, connection
	// counts and GPU counters that send their last value per window.
	plainMetrics = []string{
		"system.cpu.time",
		"system.cpu.load_average.1m",
		"system.cpu.load_average.5m",
		"system.cpu.load_average.15m",
		"system.cpu.load_average.normalized.1m",
		"system.cpu.load_average.normalized.5m",
		"system.cpu.load_average.normalized.15m",
		"system.disk.io_time",
		"system.disk.weighted_io_time",
		"system.disk.operation_time",
		"system.disk.merged",
		"system.paging.usage",
		"system.paging.utilization",
		"system.paging.operations",
		"system.paging.faults",
		"system.network.connections",
		"windows.memory.committed",
		"windows.memory.commit_limit",
		"hw.gpu.io",
		"hw.errors",
	}
)

// pressureMetrics lists the psi receiver's gauges: some and full for every
// resource. Kernels 5.13 and later report cpu full.
func pressureMetrics() []string {
	var names []string
	for _, resource := range []string{"cpu", "memory", "io"} {
		for _, kind := range []string{"some", "full"} {
			for _, window := range []string{"avg10", "avg60", "avg300"} {
				names = append(names, fmt.Sprintf("system.pressure.%s.%s.%s", resource, kind, window))
			}
		}
	}
	return names
}
