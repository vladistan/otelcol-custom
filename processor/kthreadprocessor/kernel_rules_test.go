// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package kthreadprocessor

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// eAcute is a two-byte character.
const eAcute = "é"

// nameMatches is true when the processor merges a bare resource (no command line,
// no executable path, a user pid) with this executable name.
func nameMatches(t *testing.T, cfg *Config, name string) bool {
	t.Helper()
	out := run(t, cfg, scrape(thread{name: name, pid: 500, user: 1}))
	return len(resourcesNamed(out, name)) == 0
}

// A process with an absent command line is not kernel evidence by itself: operators
// turn the command line off in the process scraper, and a container has its own pid 2.

func TestKernelParentKeepsAContainerProcessThatHasPidTwoAndALiveParent(t *testing.T) {
	// pid 1 of a container's pid namespace started pid 2; the command line is switched off.
	th := thread{name: "app", pid: 2, user: 1, attrs: map[string]any{"process.parent_pid": int64(1)}}

	out := run(t, defaultConfig(), scrape(th))

	assert.Len(t, resourcesNamed(out, "app"), 1)
	assert.Empty(t, resourcesNamed(out, defaultAggregateName))
}

func TestKernelParentKeepsAChildOfPidTwoThatHasAnExecutablePath(t *testing.T) {
	// A child of a container's pid 2, command line switched off, still has its binary path.
	th := thread{name: "worker", pid: 40, user: 1, attrs: map[string]any{
		"process.parent_pid":      int64(2),
		"process.executable.path": "/usr/bin/worker",
	}}

	out := run(t, defaultConfig(), scrape(th))

	assert.Len(t, resourcesNamed(out, "worker"), 1)
	assert.Empty(t, resourcesNamed(out, defaultAggregateName))
}

func TestKernelParentCollapsesAThreadWhoseExecutablePathIsEmpty(t *testing.T) {
	th := kernelThread("kcompactd0", 31, 1)
	th.attrs["process.executable.path"] = ""

	out := run(t, defaultConfig(), scrape(th))

	assert.Empty(t, resourcesNamed(out, "kcompactd0"))
	assert.Len(t, resourcesNamed(out, defaultAggregateName), 1)
}

func TestKernelParentKeepsAProcessWhoseExecutablePathIsNotAString(t *testing.T) {
	th := kernelThread("odd", 31, 1)
	th.attrs["process.executable.path"] = int64(7)

	out := run(t, defaultConfig(), scrape(th))

	assert.Len(t, resourcesNamed(out, "odd"), 1)
}

func TestKernelParentPidTwoRuleNeedsNoParentOrParentZero(t *testing.T) {
	tests := map[string]struct {
		attrs  map[string]any
		kept   int // resources left under the process name
		merged int // resources in the aggregate
	}{
		"no parent pid attribute":     {nil, 0, 1},
		"parent pid 0":                {map[string]any{"process.parent_pid": int64(0)}, 0, 1},
		"parent pid 0 as a string":    {map[string]any{"process.parent_pid": "0"}, 0, 1},
		"parent pid 1":                {map[string]any{"process.parent_pid": int64(1)}, 1, 0},
		"parent pid 1 as a string":    {map[string]any{"process.parent_pid": "1"}, 1, 0},
		"parent pid as a double 0":    {map[string]any{"process.parent_pid": float64(0)}, 1, 0},
		"parent pid as a padded 0":    {map[string]any{"process.parent_pid": " 0"}, 1, 0},
		"linux host":                  {map[string]any{"os.type": "linux"}, 0, 1},
		"freebsd host":                {map[string]any{"os.type": "freebsd"}, 1, 0},
		"windows host":                {map[string]any{"os.type": "windows"}, 1, 0},
		"os type is not a string":     {map[string]any{"os.type": int64(1)}, 1, 0},
		"os type empty":               {map[string]any{"os.type": ""}, 1, 0},
		"linux and a parent pid of 1": {map[string]any{"os.type": "linux", "process.parent_pid": int64(1)}, 1, 0},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			th := thread{name: "kthreadd", pid: 2, user: 1, attrs: tt.attrs}

			out := run(t, defaultConfig(), scrape(th))

			assert.Len(t, resourcesNamed(out, "kthreadd"), tt.kept)
			assert.Len(t, resourcesNamed(out, defaultAggregateName), tt.merged)
		})
	}
}

func TestKernelParentParentRuleIsAlsoLimitedToLinux(t *testing.T) {
	th := kernelThread("worker", 40, 1)
	th.attrs["os.type"] = "freebsd"

	out := run(t, defaultConfig(), scrape(th))

	assert.Len(t, resourcesNamed(out, "worker"), 1)
}

func TestKernelParentReadsPidsOnlyAsPlainDecimalTwo(t *testing.T) {
	for _, value := range []string{"+2", "02", "002", "2 ", " 2", "0x2", "2.0", "2e0", "-2", "٢", "２"} {
		t.Run(value, func(t *testing.T) {
			th := thread{name: "odd", pid: 50, user: 1, attrs: map[string]any{"process.parent_pid": value}}

			out := run(t, defaultConfig(), scrape(th))

			assert.Len(t, resourcesNamed(out, "odd"), 1)
			assert.Empty(t, resourcesNamed(out, defaultAggregateName))
		})
	}
	t.Run("2", func(t *testing.T) {
		th := thread{name: "odd", pid: 50, user: 1, attrs: map[string]any{"process.parent_pid": "2"}}

		out := run(t, defaultConfig(), scrape(th))

		assert.Empty(t, resourcesNamed(out, "odd"))
	})
}

// The 15-byte cut name is a guess for a literal pattern, so it applies only to a
// resource that looks kernel-started: no command line and no executable path.

func cutConfig(kernelParent bool) *Config {
	return &Config{Patterns: []string{"systemd-journald", "rcu_tasks_rude_kthread"}, AggregateName: "k", KernelParent: kernelParent}
}

func TestCutNameDoesNotMatchARealProcessWithACommandLineOrAnExecutablePath(t *testing.T) {
	// "systemd-journal" is the first 15 bytes of the pattern "systemd-journald".
	withCmd := thread{name: "systemd-journal", pid: 300, user: 1, attrs: map[string]any{"process.command_line": "/usr/bin/systemd-journal"}}
	withPath := thread{name: "systemd-journal", pid: 301, user: 1, attrs: map[string]any{"process.executable.path": "/usr/bin/systemd-journal"}}
	for name, th := range map[string]thread{"command line": withCmd, "executable path": withPath} {
		t.Run(name, func(t *testing.T) {
			out := run(t, cutConfig(true), scrape(th))

			assert.Len(t, resourcesNamed(out, "systemd-journal"), 1)
			assert.Empty(t, resourcesNamed(out, "k"))
		})
	}
}

func TestCutNameMatchesAResourceWithNoCommandLineAndNoExecutablePath(t *testing.T) {
	for _, kernelParent := range []bool{true, false} {
		t.Run(map[bool]string{true: "kernel_parent on", false: "kernel_parent off"}[kernelParent], func(t *testing.T) {
			th := thread{name: "rcu_tasks_rude_", pid: 12, user: 1, attrs: map[string]any{"process.parent_pid": int64(1)}}

			out := run(t, cutConfig(kernelParent), scrape(th))

			assert.Empty(t, resourcesNamed(out, "rcu_tasks_rude_"))
			assert.Len(t, resourcesNamed(out, "k"), 1)
		})
	}
}

func TestCutNameDoesNotChangeAFullNameMatchOfARealProcess(t *testing.T) {
	// The full pattern name still matches by name whatever else the resource carries.
	th := thread{name: "systemd-journald", pid: 300, user: 1, attrs: map[string]any{"process.command_line": "/usr/bin/systemd-journald"}}

	out := run(t, cutConfig(true), scrape(th))

	assert.Empty(t, resourcesNamed(out, "systemd-journald"))
}

func TestCutNameStaysOffWhenThePatternSplitsAMultibyteCharacter(t *testing.T) {
	// 10 two-byte characters: byte 15 falls inside the eighth, so the cut is not a valid name.
	pattern := strings.Repeat(eAcute, 10)
	require.Greater(t, len(pattern), commLen)

	_, ok := cutName(pattern)
	assert.False(t, ok)

	p, err := newProcessor(&Config{Patterns: []string{pattern}, AggregateName: "k"}, nil)
	require.NoError(t, err)
	assert.Empty(t, p.cutNames)
}

func TestCutNameKeepsAPatternWhoseFirst15BytesEndOnACharacter(t *testing.T) {
	// 7 two-byte characters then a letter is 15 bytes.
	pattern := strings.Repeat(eAcute, 7) + "a-tail-x"
	require.Equal(t, 15, len(strings.Repeat(eAcute, 7)+"a"))

	cut, ok := cutName(pattern)

	assert.True(t, ok)
	assert.Equal(t, strings.Repeat(eAcute, 7)+"a", cut)
}

func TestConfigAllowsNoPatternsWhenKernelParentIsOn(t *testing.T) {
	cfg := &Config{AggregateName: "kernel_threads", KernelParent: true}

	require.NoError(t, cfg.Validate())

	p, err := newProcessor(cfg, nil)
	require.NoError(t, err)
	in := scrape(kernelThread("rcu_preempt", 14, 1), thread{name: "kworker/0:1", pid: 15, user: 1})
	out := run(t, cfg, in)
	assert.Empty(t, resourcesNamed(out, "rcu_preempt"))
	assert.Len(t, resourcesNamed(out, "kworker/0:1"), 1) // no pattern: name alone does not merge
	assert.NotNil(t, p)
}

func TestConfigStillNeedsPatternsWhenKernelParentIsOff(t *testing.T) {
	cfg := &Config{AggregateName: "kernel_threads"}

	err := cfg.Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "patterns must not be empty")
}
