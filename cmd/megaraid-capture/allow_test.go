// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// mfiregOpcodes is every MFI_DCMD_* value of the FreeBSD header mfireg.h, plus the two list
// queries the Linux driver header adds. The test owns this copy so the tables cannot drift unseen.
var mfiregOpcodes = map[string]uint32{
	"CTRL_GETINFO":            0x01010000,
	"CTRL_MFI_HOST_MEM_ALLOC": 0x0100e100,
	"CTRL_MFC_DEFAULTS_GET":   0x010e0201,
	"CTRL_MFC_DEFAULTS_SET":   0x010e0202,
	"CTRL_FLUSHCACHE":         0x01101000,
	"CTRL_GET_PROPS":          0x01020100,
	"CTRL_SET_PROPS":          0x01020200,
	"CTRL_SHUTDOWN":           0x01050000,
	"CTRL_EVENT_GETINFO":      0x01040100,
	"CTRL_EVENT_GET":          0x01040300,
	"CTRL_EVENT_WAIT":         0x01040500,
	"PR_GET_STATUS":           0x01070100,
	"PR_GET_PROPERTIES":       0x01070200,
	"PR_SET_PROPERTIES":       0x01070300,
	"PR_START":                0x01070400,
	"PR_STOP":                 0x01070500,
	"TIME_SECS_GET":           0x01080201,
	"FLASH_FW_OPEN":           0x010f0100,
	"FLASH_FW_DOWNLOAD":       0x010f0200,
	"FLASH_FW_FLASH":          0x010f0300,
	"FLASH_FW_CLOSE":          0x010f0400,
	"PD_GET_LIST":             0x02010000,
	"PD_LIST_QUERY":           0x02010100,
	"PD_GET_INFO":             0x02020000,
	"PD_STATE_SET":            0x02030100,
	"PD_REBUILD_START":        0x02040100,
	"PD_REBUILD_ABORT":        0x02040200,
	"PD_CLEAR_START":          0x02050100,
	"PD_CLEAR_ABORT":          0x02050200,
	"PD_GET_PROGRESS":         0x02060000,
	"PD_LOCATE_START":         0x02070100,
	"PD_LOCATE_STOP":          0x02070200,
	"LD_MAP_GET_INFO":         0x0300e101,
	"LD_SYNC":                 0x0300e102,
	"LD_GET_LIST":             0x03010000,
	"LD_LIST_QUERY":           0x03010100,
	"LD_GET_INFO":             0x03020000,
	"LD_GET_PROP":             0x03030000,
	"LD_SET_PROP":             0x03040000,
	"LD_INIT_START":           0x03060100,
	"LD_DELETE":               0x03090000,
	"CFG_READ":                0x04010000,
	"CFG_ADD":                 0x04020000,
	"CFG_CLEAR":               0x04030000,
	"CFG_MAKE_SPARE":          0x04040000,
	"CFG_REMOVE_SPARE":        0x04050000,
	"CFG_FOREIGN_SCAN":        0x04060100,
	"CFG_FOREIGN_DISPLAY":     0x04060200,
	"CFG_FOREIGN_PREVIEW":     0x04060300,
	"CFG_FOREIGN_IMPORT":      0x04060400,
	"CFG_FOREIGN_CLEAR":       0x04060500,
	"BBU_GET_STATUS":          0x05010000,
	"BBU_GET_CAPACITY_INFO":   0x05020000,
	"BBU_GET_DESIGN_INFO":     0x05030000,
	"BBU_START_LEARN":         0x05040000,
	"BBU_GET_PROP":            0x05050100,
	"BBU_SET_PROP":            0x05050200,
	"CLUSTER":                 0x08000000,
	"CLUSTER_RESET_ALL":       0x08010100,
	"CLUSTER_RESET_LD":        0x08010200,
}

// wantAllowed is the list of the nineteen read commands the tool may send.
var wantAllowed = []string{
	"CTRL_GETINFO", "CTRL_GET_PROPS", "PR_GET_STATUS", "PR_GET_PROPERTIES",
	"PD_GET_LIST", "PD_LIST_QUERY", "PD_GET_INFO", "PD_GET_PROGRESS",
	"LD_GET_LIST", "LD_LIST_QUERY", "LD_GET_INFO", "LD_GET_PROP", "CFG_READ",
	"BBU_GET_STATUS", "BBU_GET_CAPACITY_INFO", "BBU_GET_DESIGN_INFO", "BBU_GET_PROP",
	"CTRL_EVENT_GETINFO", "CTRL_EVENT_GET",
}

func TestExactlyTheNineteenListedOpcodesAreAllowed(t *testing.T) {
	want := map[uint32]string{}
	for _, name := range wantAllowed {
		want[mfiregOpcodes[name]] = name
	}
	if len(want) != 19 {
		t.Fatalf("test list has %d opcodes, want 19", len(want))
	}
	if len(allowed) != 19 {
		t.Fatalf("allowed has %d entries, want 19", len(allowed))
	}
	for op, name := range want {
		if _, ok := allowed[op]; !ok {
			t.Errorf("%s (%#x) is not allowed", name, op)
		}
	}
}

func TestEveryOpcodeOfTheHeaderPassesOnlyWhenListed(t *testing.T) {
	listed := map[string]bool{}
	for _, name := range wantAllowed {
		listed[name] = true
	}
	for name, op := range mfiregOpcodes {
		p := buildPacket(0, request{opcode: op, dataLen: 512}, 0x1000)
		err := checkPacket(p)
		if listed[name] && err != nil {
			t.Errorf("%s (%#x) is on the list but was refused: %v", name, op, err)
		}
		if !listed[name] && err == nil {
			t.Errorf("%s (%#x) is not on the list but passed", name, op)
		}
	}
}

func TestEveryUnlistedHeaderOpcodeIsNamedInTheDenyTable(t *testing.T) {
	listed := map[uint32]bool{}
	for _, name := range wantAllowed {
		listed[mfiregOpcodes[name]] = true
	}
	for name, op := range mfiregOpcodes {
		if listed[op] {
			if _, denied := denyTable[op]; denied {
				t.Errorf("%s (%#x) is both allowed and denied", name, op)
			}
			continue
		}
		if _, ok := denyTable[op]; !ok {
			t.Errorf("%s (%#x) is missing from the deny table", name, op)
		}
	}
}

func TestDenyTableKeepsTheDangerousOpcodesDenied(t *testing.T) {
	for _, name := range []string{
		"CFG_ADD", "CFG_CLEAR", "CFG_MAKE_SPARE", "CFG_REMOVE_SPARE", "CFG_FOREIGN_IMPORT", "CFG_FOREIGN_CLEAR",
		"LD_DELETE", "LD_INIT_START", "LD_SYNC", "PD_CLEAR_START", "PD_REBUILD_START", "PD_STATE_SET",
		"FLASH_FW_OPEN", "FLASH_FW_DOWNLOAD", "FLASH_FW_FLASH", "FLASH_FW_CLOSE",
		"CTRL_SHUTDOWN", "CTRL_SET_PROPS", "LD_SET_PROP", "BBU_SET_PROP", "PR_SET_PROPERTIES",
		"BBU_START_LEARN", "PR_START", "PR_STOP", "PD_LOCATE_START", "CTRL_FLUSHCACHE",
		"CTRL_EVENT_WAIT", "CLUSTER_RESET_ALL",
	} {
		if _, ok := denyTable[mfiregOpcodes[name]]; !ok {
			t.Errorf("%s is not in the deny table", name)
		}
	}
}

func TestOpcodeNamesAreAvailableForTheManifest(t *testing.T) {
	for op, name := range allowed {
		if name == "" {
			t.Errorf("opcode %#x has no name", op)
		}
	}
	if allowed[0x01010000] != "CTRL_GETINFO" || allowed[0x01040300] != "CTRL_EVENT_GET" {
		t.Errorf("names are wrong: %q %q", allowed[0x01010000], allowed[0x01040300])
	}
}

func TestOpcodeLiteralsAppearOnlyInTheirOwnTable(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ToLower(string(body))
		for name, op := range mfiregOpcodes {
			literal := strings.ToLower(fmt.Sprintf("0x%08x", op))
			isAllowed := false
			for _, a := range wantAllowed {
				if a == name {
					isAllowed = true
				}
			}
			table := "deny.go"
			if isAllowed {
				table = "allow.go"
			}
			if strings.Contains(text, literal) && e.Name() != table {
				t.Errorf("%s holds the opcode %s of %s; it belongs in %s only", e.Name(), literal, name, table)
			}
		}
	}
}

func TestSourceUsesNoExecNoCgoAndOnlyTheTwoProbeDeviceNodes(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var devPaths []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", e.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if path == "os"+"/exec" || path == "C" {
				t.Errorf("%s imports %q", e.Name(), path)
			}
		}
		body, _ := os.ReadFile(e.Name())
		for _, field := range strings.Fields(strings.NewReplacer("\"", " ", "`", " ").Replace(string(body))) {
			if strings.HasPrefix(field, "/dev/") {
				devPaths = append(devPaths, field)
			}
		}
	}
	probes := map[string]bool{"/dev/megaraid_sas_ioctl_node": false, "/dev/megaraid_sas": false}
	sort.Strings(devPaths)
	for _, p := range devPaths {
		if _, ok := probes[p]; !ok {
			t.Errorf("source names the device path %q", p)
		}
		probes[p] = true
	}
	for p, seen := range probes {
		if !seen {
			t.Errorf("the probe path %s is not in the source", p)
		}
	}
}
