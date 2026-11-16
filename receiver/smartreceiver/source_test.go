// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartreceiver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// productionSources returns the text of the non-test Go files of the given directories.
func productionSources(t *testing.T, dirs ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, dir := range dirs {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		require.NoError(t, err)
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f)
			require.NoError(t, err)
			out[f] = string(b)
		}
	}
	require.NotEmpty(t, out)
	return out
}

func TestSource_NoExecAndNoCgoInTheReceiverAndTheDecoders(t *testing.T) {
	for file, text := range productionSources(t, ".", "../smartdecode", "../smartdecode/smarttest") {
		assert.NotContains(t, text, `"os/exec"`, file)
		assert.NotContains(t, text, `import "C"`, file)
		assert.NotContains(t, text, "smartctl\"", file)
		for _, call := range []string{"os.StartProcess", "syscall.Exec", "syscall.ForkExec", "unix.Exec", "unix.ForkExec"} {
			assert.NotContains(t, text, call, file)
		}
	}
}

func TestSource_TheIoctlCallsGoThroughXSysUnix(t *testing.T) {
	var all strings.Builder
	for _, text := range productionSources(t, ".") {
		all.WriteString(text)
	}
	text := all.String()

	assert.Contains(t, text, `"golang.org/x/sys/unix"`)
	assert.Contains(t, text, "unix.Syscall(unix.SYS_IOCTL")
	assert.Contains(t, text, "SG_IO")
	assert.Contains(t, text, "NVME_IOCTL_ADMIN_CMD")
	assert.NotContains(t, text, "syscall.Syscall")
}

func TestSource_NoHwmonPathInTheReceiver(t *testing.T) {
	for file, text := range productionSources(t, ".") {
		assert.NotContains(t, strings.ToLower(text), "hwmon", file)
	}
}

func TestSource_NoSmartctlMetricNameExists(t *testing.T) {
	for file, text := range productionSources(t, ".", "../smartdecode", "../hwschema") {
		assert.NotContains(t, text, "smartctl_", file)
	}
}

func TestSource_MetricNamesLiveInHwschemaOnly(t *testing.T) {
	for file, text := range productionSources(t, ".") {
		if strings.HasSuffix(file, "telemetry.go") {
			continue
		}
		assert.NotContains(t, text, `"hw.`, file)
	}
}
