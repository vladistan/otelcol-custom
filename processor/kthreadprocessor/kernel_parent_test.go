// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package kthreadprocessor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// kernelThread is a process the kernel started: parent pid 2 and no command line.
func kernelThread(name string, pid int64, user float64) thread {
	return thread{name: name, pid: pid, user: user, system: 1, attrs: map[string]any{"process.parent_pid": int64(2)}}
}

func TestKernelParentCollapsesThreadsNoNamePatternCovers(t *testing.T) {
	names := []string{
		"rcu_preempt", "rcu_tasks_rude_", "rcu_tasks_kthread", "ecryptfs-kthread",
		"jbd2/sda1-8", "xfsaild/sda3", "z_wr_iss", "vhost-2295", "khugepaged", "scsi_eh_0",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			in := scrape(kernelThread(name, 400, 2), thread{name: "nginx", pid: 900, user: 5})

			out := run(t, defaultConfig(), in)

			assert.Empty(t, resourcesNamed(out, name))
			assert.Len(t, resourcesNamed(out, "nginx"), 1)
			agg := resourcesNamed(out, defaultAggregateName)
			require.Len(t, agg, 1)
			assert.InDelta(t, 2, pointWith(t, metricNamed(t, agg[0], "process.cpu.time"), "state", "user").DoubleValue(), 1e-9)
		})
	}
}

func TestKernelParentCollapsesKthreaddItself(t *testing.T) {
	// kthreadd is pid 2 with parent pid 0 and no command line.
	in := scrape(thread{name: "kthreadd", pid: 2, user: 1, attrs: map[string]any{"process.parent_pid": int64(0)}})

	out := run(t, defaultConfig(), in)

	assert.Empty(t, resourcesNamed(out, "kthreadd"))
	assert.Len(t, resourcesNamed(out, defaultAggregateName), 1)
}

func TestKernelParentSumsNameMatchesAndParentMatchesInOneAggregate(t *testing.T) {
	in := scrape(
		thread{name: "kworker/0:1", pid: 11, user: 1, system: 1},
		kernelThread("rcu_sched", 12, 3),
		kernelThread("jbd2/sda2-8", 13, 4),
		thread{name: "nginx", pid: 900, user: 5},
	)

	out := run(t, defaultConfig(), in)

	agg := resourcesNamed(out, defaultAggregateName)
	require.Len(t, agg, 1)
	cpu := metricNamed(t, agg[0], "process.cpu.time")
	assert.InDelta(t, 8, pointWith(t, cpu, "state", "user").DoubleValue(), 1e-9)
	assert.InDelta(t, 3, pointWith(t, cpu, "state", "system").DoubleValue(), 1e-9)
	assert.Equal(t, 2, out.ResourceMetrics().Len())
}

func TestKernelParentKeepsAUserProcessWithAParentOfTwoAndACommandLine(t *testing.T) {
	th := thread{name: "worker", pid: 700, user: 1, attrs: map[string]any{
		"process.parent_pid":   int64(2),
		"process.command_line": "/usr/bin/worker --serve",
	}}

	out := run(t, defaultConfig(), scrape(th))

	assert.Len(t, resourcesNamed(out, "worker"), 1)
	assert.Empty(t, resourcesNamed(out, defaultAggregateName))
}

func TestKernelParentKeepsAProcessWithPidTwoAndACommandLine(t *testing.T) {
	// A process in its own pid namespace can have pid 2.
	th := thread{name: "app", pid: 2, user: 1, attrs: map[string]any{
		"process.parent_pid":   int64(1),
		"process.command_line": "app run",
	}}

	out := run(t, defaultConfig(), scrape(th))

	assert.Len(t, resourcesNamed(out, "app"), 1)
}

func TestKernelParentCollapsesAParentOfTwoWithAnEmptyCommandLine(t *testing.T) {
	th := kernelThread("kcompactd0", 31, 1)
	th.attrs["process.command_line"] = ""

	out := run(t, defaultConfig(), scrape(th))

	assert.Empty(t, resourcesNamed(out, "kcompactd0"))
	assert.Len(t, resourcesNamed(out, defaultAggregateName), 1)
}

func TestKernelParentIgnoresResourcesWithoutAParentPid(t *testing.T) {
	in := scrape(thread{name: "rcu_preempt", pid: 14, user: 1})

	out := run(t, defaultConfig(), in)

	assert.Len(t, resourcesNamed(out, "rcu_preempt"), 1)
	assert.Empty(t, resourcesNamed(out, defaultAggregateName))
}

func TestKernelParentOffLeavesOnlyTheNamePatterns(t *testing.T) {
	cfg := defaultConfig()
	cfg.KernelParent = false
	in := scrape(kernelThread("rcu_preempt", 14, 1), thread{name: "kworker/1:0", pid: 15, user: 1})

	out := run(t, cfg, in)

	assert.Len(t, resourcesNamed(out, "rcu_preempt"), 1)
	assert.Empty(t, resourcesNamed(out, "kworker/1:0"))
	assert.Len(t, resourcesNamed(out, defaultAggregateName), 1)
}

func TestKernelParentReadsPidAttributesOfOtherTypesWithoutPanic(t *testing.T) {
	tests := map[string]struct {
		attrs  map[string]any
		kept   int // resources left under the process name
		merged int // resources in the aggregate
	}{
		"parent pid as a decimal string": {map[string]any{"process.parent_pid": "2"}, 0, 1},
		"parent pid as a padded string":  {map[string]any{"process.parent_pid": " 2 "}, 1, 0},
		"parent pid as a word":           {map[string]any{"process.parent_pid": "two"}, 1, 0},
		"parent pid as a double":         {map[string]any{"process.parent_pid": float64(2)}, 1, 0},
		"parent pid as a bool":           {map[string]any{"process.parent_pid": true}, 1, 0},
		"parent pid other than two":      {map[string]any{"process.parent_pid": int64(3)}, 1, 0},
		"command line as an int":         {map[string]any{"process.parent_pid": int64(2), "process.command_line": int64(5)}, 1, 0},
		"command line as a bool":         {map[string]any{"process.parent_pid": int64(2), "process.command_line": true}, 1, 0},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			th := thread{name: "odd", pid: 50, user: 1, attrs: tt.attrs}

			out := run(t, defaultConfig(), scrape(th))

			assert.Len(t, resourcesNamed(out, "odd"), tt.kept)
			assert.Len(t, resourcesNamed(out, defaultAggregateName), tt.merged)
		})
	}
}

func TestKernelParentCollapsesPidTwoWithoutAParentPidAttribute(t *testing.T) {
	th := thread{name: "kthreadd", pid: 2, user: 1}
	out := run(t, defaultConfig(), scrape(th))

	assert.Empty(t, resourcesNamed(out, "kthreadd"))
}

func TestMatchesAlsoTakesTheNameTheKernelCutAt15Characters(t *testing.T) {
	cfg := &Config{
		Patterns:      []string{"rcu_tasks_rude_kthread", "rcu_exp_gp_kthread_worker", "ecryptfs-kthread", "short"},
		AggregateName: "k",
	}
	tests := map[string]bool{
		"rcu_tasks_rude_kthread":    true,  // the full name
		"rcu_tasks_rude_":           true,  // cut at 15 characters
		"rcu_exp_gp_kthr":           true,  // cut at 15 characters
		"ecryptfs-kthrea":           true,  // cut at 15 characters
		"ecryptfs-kthre":            false, // 14 characters: not a cut
		"ecryptfs-kthrex":           false, // 15 characters but not the cut of the pattern
		"rcu_tasks_rude_kthreadX":   false, // longer than the pattern
		"shor":                      false, // a short pattern has no cut name
		"short":                     true,
		"nginx":                     false,
		"rcu_exp_par_gp_kthread_wo": false, // another pattern's start
	}

	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, want, nameMatches(t, cfg, name))
		})
	}
}

func TestPatternWithAWildcardInItsFirst15CharactersMatchesAsBefore(t *testing.T) {
	cfg := &Config{Patterns: []string{"rcu_*", "kworker/?:?H-kblockd"}, AggregateName: "k"}

	assert.True(t, nameMatches(t, cfg, "rcu_tasks_rude_"))
	assert.True(t, nameMatches(t, cfg, "rcu_exp_par_gp_kthread_worker/0"))
	assert.True(t, nameMatches(t, cfg, "kworker/0:1H-kblockd"))
	assert.False(t, nameMatches(t, cfg, "kworker/0:1H-kbloc"))
}

func TestKernelParentDefaultsToTrue(t *testing.T) {
	cfg := createDefaultConfig().(*Config)

	assert.True(t, cfg.KernelParent)
	assert.NoError(t, cfg.Validate())
}

func TestKernelParentAggregateDropsTheParentPidAndCommandLine(t *testing.T) {
	out := run(t, defaultConfig(), scrape(kernelThread("rcu_gp", 61, 1), kernelThread("rcu_par_gp", 62, 1)))

	agg := resourcesNamed(out, defaultAggregateName)
	require.Len(t, agg, 1)
	_, hasParent := agg[0].Resource().Attributes().Get("process.parent_pid")
	_, hasPid := agg[0].Resource().Attributes().Get("process.pid")
	assert.False(t, hasParent)
	assert.False(t, hasPid)
}
