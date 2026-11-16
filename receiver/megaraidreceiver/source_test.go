// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// productionSources returns the name and text of every non-test Go file of the package.
func productionSources(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	out := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		body, err := os.ReadFile(f)
		require.NoError(t, err)
		out[f] = string(body)
	}
	require.NotEmpty(t, out)
	return out
}

func TestSourceHasNoExecNoCgoAndOneDevicePath(t *testing.T) {
	devicePath := regexp.MustCompile(`"/(dev|proc)/[^"]*"`)
	for name, src := range productionSources(t) {
		assert.NotContains(t, src, `"os/exec"`, name)
		assert.NotContains(t, src, `import "C"`, name)
		assert.NotContains(t, src, "exec.Command", name)
		for _, m := range devicePath.FindAllString(src, -1) {
			// /proc/devices only lists major numbers, for create_node; no other path is opened.
			assert.Contains(t, []string{`"/dev/megaraid_sas_ioctl_node"`, `"/proc/devices"`}, m, name)
		}
	}
}

func TestOnlyOneFileCallsTheSyscall(t *testing.T) {
	for name, src := range productionSources(t) {
		if name == "ioctl_linux.go" {
			assert.Contains(t, src, "SYS_IOCTL")
			continue
		}
		assert.NotContains(t, src, "SYS_IOCTL", name)
		assert.NotContains(t, src, "syscall.Syscall", name)
	}
}

func TestDeniedOpcodesAppearOnlyInTheDenyTable(t *testing.T) {
	denied := []string{
		"CFG_ADD", "CFG_CLEAR", "CFG_MAKE_SPARE", "CFG_REMOVE_SPARE", "CFG_FOREIGN", "LD_DELETE", "LD_INIT_START",
		"LD_SYNC", "PD_CLEAR", "PD_REBUILD", "PD_STATE_SET", "FLASH_FW", "CTRL_SHUTDOWN", "CTRL_SET_PROPS",
		"LD_SET_PROP", "BBU_SET_PROP", "PR_SET_PROPERTIES", "MFC_DEFAULTS_SET", "CLUSTER", "BBU_START_LEARN",
		"PR_START", "PR_STOP", "PD_LOCATE", "CTRL_FLUSHCACHE", "CTRL_EVENT_WAIT",
	}
	for name, src := range productionSources(t) {
		if name == "allow.go" {
			continue
		}
		for _, d := range denied {
			assert.NotContains(t, src, d, name)
		}
		for op := range denyTable {
			assert.NotContains(t, strings.ToLower(src), strings.ToLower(hex8(op)), name)
		}
	}
	src := productionSources(t)["allow.go"]
	for _, d := range denied {
		assert.Contains(t, src, d, "the deny table names it")
	}
}

func hex8(v uint32) string {
	const digits = "0123456789abcdef"
	b := []byte("0x00000000")
	for i := 9; i >= 2; i-- {
		b[i] = digits[v&0xF]
		v >>= 4
	}
	return string(b)
}

func TestEveryStructLayoutNamesItsSourceInAShortComment(t *testing.T) {
	for name, src := range productionSources(t) {
		for _, line := range strings.Split(src, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				assert.LessOrEqual(t, len(trimmed), 400, "%s: comment line is long", name)
			}
		}
	}
	decode := productionSources(t)["decode.go"]
	assert.Contains(t, decode, "mfireg.h")
	assert.Contains(t, decode, "megaraid_sas.h")
	for _, layout := range []string{"struct mfi_ctrl_info", "struct mfi_pd_list", "struct mfi_ld_list", "struct mfi_ld_info", "struct mfi_pd_info", "struct mfi_bbu_status", "struct mfi_evt_log_state"} {
		assert.Contains(t, decode, layout)
	}
}
