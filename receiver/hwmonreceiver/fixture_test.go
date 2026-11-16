// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwmonreceiver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// tree is a fake /sys: files live under <root>/class/hwmon and <root>/class/thermal.
type tree struct {
	t    *testing.T
	root string
}

func newTree(t *testing.T) *tree {
	t.Helper()
	return &tree{t: t, root: t.TempDir()}
}

func (f *tree) hwmonPath() string { return filepath.Join(f.root, "class", "hwmon") }

// write creates a file below class/, with a trailing newline like sysfs.
func (f *tree) write(rel, content string) {
	f.t.Helper()
	p := filepath.Join(f.root, "class", rel)
	require.NoError(f.t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(f.t, os.WriteFile(p, []byte(content+"\n"), 0o644))
}

// chip writes hwmon<dir>/name and the given files.
func (f *tree) chip(dir, name string, files map[string]string) {
	f.t.Helper()
	f.write("hwmon/"+dir+"/name", name)
	for file, content := range files {
		f.write("hwmon/"+dir+"/"+file, content)
	}
}

// nvme builds what the kernel has for an NVMe drive: hwmon<dir>/device is the
// class device <className>, whose device link reaches the PCI function. The
// class name can change between boots; the PCI address stays with the drive.
func (f *tree) nvme(hwmonDir, className, pci string) {
	f.t.Helper()
	pciDir := filepath.Join(f.root, "devices", "pci0000:00", pci)
	classDir := filepath.Join(f.root, "class", "nvme", className)
	require.NoError(f.t, os.MkdirAll(pciDir, 0o755))
	require.NoError(f.t, os.MkdirAll(classDir, 0o755))
	require.NoError(f.t, os.Symlink("../../../devices/pci0000:00/"+pci, filepath.Join(classDir, "device")))
	require.NoError(f.t, os.Symlink("../../nvme/"+className, filepath.Join(f.root, "class", "hwmon", hwmonDir, "device")))
}

func byID(readings []reading) map[string]reading {
	m := make(map[string]reading, len(readings))
	for _, r := range readings {
		m[r.id] = r
	}
	return m
}
