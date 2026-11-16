// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sysfsTree builds /sys/class/scsi_host under a temp dir; each entry is host number to driver name.
func sysfsTree(t *testing.T, hosts map[int]string) string {
	t.Helper()
	root := t.TempDir()
	for n, proc := range hosts {
		pci := filepath.Join(root, "devices", "pci0000:00", fmt.Sprintf("0000:00:1c.%d", n), "0000:03:00.0", fmt.Sprintf("host%d", n), "scsi_host", fmt.Sprintf("host%d", n))
		if err := os.MkdirAll(pci, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pci, "proc_name"), []byte(proc+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		class := filepath.Join(root, "class", "scsi_host")
		if err := os.MkdirAll(class, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(pci, filepath.Join(class, fmt.Sprintf("host%d", n))); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestFindControllersListsOnlyTheMegaraidHostsInOrder(t *testing.T) {
	root := sysfsTree(t, map[int]string{0: "ahci", 2: "megaraid_sas", 5: "megaraid_sas", 7: "usb-storage"})

	got, err := findControllers(root)

	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].HostNo != 2 || got[1].HostNo != 5 {
		t.Fatalf("controllers = %+v", got)
	}
	if got[0].PCI != "0000:03:00.0" {
		t.Errorf("pci = %q", got[0].PCI)
	}
}

func TestFindControllersWithNoneOrNoSysfsReturnsNone(t *testing.T) {
	for name, root := range map[string]string{
		"other drivers": sysfsTree(t, map[int]string{0: "ahci"}),
		"no scsi_host":  t.TempDir(),
	} {
		got, err := findControllers(root)

		if err != nil || len(got) != 0 {
			t.Errorf("%s: %+v, %v", name, got, err)
		}
	}
}

type testEnv struct {
	env     *environment
	opened  []int
	devices map[int]*fakeController
	stdout  bytes.Buffer
	stderr  bytes.Buffer
}

func newTestEnv(t *testing.T, hosts map[int]string) *testEnv {
	t.Helper()
	te := &testEnv{devices: map[int]*fakeController{}}
	te.env = &environment{
		sysfs:    sysfsTree(t, hosts),
		hostname: func() (string, error) { return rawHostname, nil },
		random:   bytes.NewReader(bytes.Repeat([]byte{0x42}, 64)),
		open: func(_ string, hostNo int) (device, func(), error) {
			te.opened = append(te.opened, hostNo)
			dev, ok := te.devices[hostNo]
			if !ok {
				dev = newFakeController()
				te.devices[hostNo] = dev
			}
			return dev, func() {}, nil
		},
	}
	return te
}

func (te *testEnv) run(args ...string) int {
	te.stdout.Reset()
	te.stderr.Reset()
	return run(args, te.env, &te.stdout, &te.stderr)
}

func TestRunWithoutOutIsAUsageError(t *testing.T) {
	te := newTestEnv(t, map[int]string{0: "megaraid_sas"})

	if code := te.run(); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(te.stderr.String(), "-out") {
		t.Errorf("usage text = %q", te.stderr.String())
	}
	if len(te.opened) != 0 {
		t.Error("a usage error must not open a device")
	}
}

func TestRunRejectsABadLabel(t *testing.T) {
	te := newTestEnv(t, map[int]string{0: "megaraid_sas"})

	if code := te.run("-out", filepath.Join(t.TempDir(), "o"), "-label", "../evil"); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestRunWithNoControllerExitsOneAndWritesNothing(t *testing.T) {
	te := newTestEnv(t, map[int]string{0: "ahci"})
	out := filepath.Join(t.TempDir(), "o")

	code := te.run("-out", out)

	if code != 1 || !strings.Contains(te.stderr.String(), "no megaraid_sas controller") {
		t.Fatalf("exit = %d, stderr = %q", code, te.stderr.String())
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("an empty run must not create the output directory")
	}
}

func TestRunCapturesEveryControllerAndPrintsTheDirectories(t *testing.T) {
	te := newTestEnv(t, map[int]string{1: "megaraid_sas", 4: "megaraid_sas"})
	out := filepath.Join(t.TempDir(), "o")

	code := te.run("-out", out, "-label", "lab")

	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, te.stderr.String())
	}
	for _, d := range []string{"lab-host1", "lab-host4"} {
		if _, err := os.Stat(filepath.Join(out, d, "manifest.json")); err != nil {
			t.Errorf("%s: %v", d, err)
		}
		if !strings.Contains(te.stdout.String(), d) {
			t.Errorf("stdout does not name %s: %q", d, te.stdout.String())
		}
	}
}

func TestRunWithHostCapturesOnlyThatController(t *testing.T) {
	te := newTestEnv(t, map[int]string{1: "megaraid_sas", 4: "megaraid_sas"})

	code := te.run("-out", filepath.Join(t.TempDir(), "o"), "-host", "4")

	if code != 0 || len(te.opened) != 1 || te.opened[0] != 4 {
		t.Fatalf("exit = %d, opened = %v", code, te.opened)
	}
}

func TestRunWithAHostThatIsNotAControllerExitsOne(t *testing.T) {
	te := newTestEnv(t, map[int]string{1: "megaraid_sas"})

	if code := te.run("-out", filepath.Join(t.TempDir(), "o"), "-host", "9"); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestRunReportsAFailedControllerAndStillCapturesTheOthers(t *testing.T) {
	te := newTestEnv(t, map[int]string{1: "megaraid_sas", 4: "megaraid_sas"})
	bad := newFakeController()
	bad.failWith = map[uint32]error{opCtrlGetInfo: errNeedsCapability}
	te.devices[1] = bad
	out := filepath.Join(t.TempDir(), "o")

	code := te.run("-out", out, "-label", "lab")

	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(te.stderr.String(), "CAP_SYS_ADMIN") {
		t.Errorf("stderr should name the capability: %q", te.stderr.String())
	}
	if _, err := os.Stat(filepath.Join(out, "lab-host4", "manifest.json")); err != nil {
		t.Error("the healthy controller was not captured")
	}
	if _, err := os.Stat(filepath.Join(out, "lab-host1")); err == nil {
		t.Error("a failed controller must leave no bundle")
	}
}

func TestRunOpenErrorIsReportedPerController(t *testing.T) {
	te := newTestEnv(t, map[int]string{1: "megaraid_sas"})
	te.env.open = func(string, int) (device, func(), error) { return nil, nil, errors.New("node missing") }

	code := te.run("-out", filepath.Join(t.TempDir(), "o"))

	if code != 1 || !strings.Contains(te.stderr.String(), "node missing") {
		t.Fatalf("exit = %d, stderr = %q", code, te.stderr.String())
	}
}

func TestRunScrubsTheHostnameOutOfTheEvents(t *testing.T) {
	te := newTestEnv(t, map[int]string{1: "megaraid_sas"})
	out := filepath.Join(t.TempDir(), "o")

	if code := te.run("-out", out, "-label", "lab"); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, te.stderr.String())
	}

	data, err := os.ReadFile(filepath.Join(out, "lab-host1", "event_get.bin"))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range sensitiveValues() {
		if bytes.Contains(data, v) {
			t.Errorf("event_get.bin holds %q", v)
		}
	}
}

func TestRunSaltGivesRepeatableOutputAndNoSaltDoesNot(t *testing.T) {
	read := func(args ...string) []byte {
		te := newTestEnv(t, map[int]string{1: "megaraid_sas"})
		out := filepath.Join(t.TempDir(), "o")
		if code := te.run(append([]string{"-out", out, "-label", "lab"}, args...)...); code != 0 {
			t.Fatalf("exit = %d, stderr = %q", code, te.stderr.String())
		}
		data, err := os.ReadFile(filepath.Join(out, "lab-host1", "ctrl_getinfo.bin"))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}

	if !bytes.Equal(read("-salt", "abc"), read("-salt", "abc")) {
		t.Error("the same salt must repeat")
	}
	if bytes.Equal(read("-salt", "abc"), read("-salt", "xyz")) {
		t.Error("another salt must differ")
	}
}

func TestRunWithoutSaltReadsRandomBytes(t *testing.T) {
	te := newTestEnv(t, map[int]string{1: "megaraid_sas"})
	te.env.random = io.LimitReader(bytes.NewReader(nil), 0)

	if code := te.run("-out", filepath.Join(t.TempDir(), "o")); code != 1 {
		t.Fatalf("exit = %d, want 1 when no random bytes can be read", code)
	}
}

func TestVerifySubcommand(t *testing.T) {
	te := newTestEnv(t, map[int]string{1: "megaraid_sas"})
	out := filepath.Join(t.TempDir(), "o")
	if code := te.run("-out", out, "-label", "lab"); code != 0 {
		t.Fatalf("capture exit = %d", code)
	}
	dir := filepath.Join(out, "lab-host1")

	if code := te.run("verify", dir); code != 0 {
		t.Fatalf("verify of a scrubbed bundle exited %d: %s", code, te.stderr.String())
	}
	if err := os.WriteFile(filepath.Join(dir, "ctrl_getinfo.bin"), ctrlInfoBuf(rawCtrlSerial, true), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := te.run("verify", dir); code != 1 {
		t.Fatalf("verify of a raw bundle exited %d, want 1", code)
	}
	if code := te.run("verify"); code != 2 {
		t.Fatalf("verify without a directory exited %d, want 2", code)
	}
}

func TestVersionFlag(t *testing.T) {
	te := newTestEnv(t, nil)

	if code := te.run("-version"); code != 0 || !strings.Contains(te.stdout.String(), version) {
		t.Fatalf("exit = %d, stdout = %q", code, te.stdout.String())
	}
}
