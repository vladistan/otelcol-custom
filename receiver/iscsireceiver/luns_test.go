// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package iscsireceiver

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// lunSpec describes the device directory of one LUN. An empty state, sectors or device leaves that
// file or directory out; an empty typ means a disk (0).
type lunSpec struct {
	session, host, target, id int
	state, typ, device        string
	sectors, holder           string
}

func (s lunSpec) name() string { return fmt.Sprintf("%d:0:%d:%d", s.host, s.target, s.id) }

func (c *classTree) writeAbs(p, content string) {
	c.t.Helper()
	require.NoError(c.t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(c.t, os.WriteFile(p, []byte(content+"\n"), 0o644))
}

// lun builds the device directory of an iSCSI LUN below <tmp>/devices and the link
// class/scsi_device/<H:C:T:L> to it, the way sysfs lays them out.
func (c *classTree) lun(s lunSpec) {
	c.t.Helper()
	devices := filepath.Join(filepath.Dir(c.root), "devices")
	dir := filepath.Join(devices, "platform", fmt.Sprintf("host%d", s.host), fmt.Sprintf("session%d", s.session),
		fmt.Sprintf("target%d:0:%d", s.host, s.target), s.name())
	c.device(dir, s)
}

// localDisk adds a SCSI disk that is not behind iSCSI.
func (c *classTree) localDisk() {
	c.t.Helper()
	dir := filepath.Join(filepath.Dir(c.root), "devices", "pci0000:00", "ata1", "host0", "target0:0:0", "0:0:0:0")
	c.device(dir, lunSpec{host: 0, state: "running", device: "sda", sectors: "1000"})
}

func (c *classTree) device(dir string, s lunSpec) {
	c.t.Helper()
	typ := s.typ
	if typ == "" {
		typ = "0"
	}
	c.writeAbs(filepath.Join(dir, "type"), typ)
	if s.state != "" {
		c.writeAbs(filepath.Join(dir, "state"), s.state)
	}
	if s.device != "" {
		blk := filepath.Join(dir, "block", s.device)
		require.NoError(c.t, os.MkdirAll(blk, 0o755))
		if s.sectors != "" {
			c.writeAbs(filepath.Join(blk, "size"), s.sectors)
		}
		for _, h := range strings.Split(s.holder, ",") { // a comma separated list of holders
			if h != "" {
				require.NoError(c.t, os.MkdirAll(filepath.Join(blk, "holders", h), 0o755))
			}
		}
	}
	entry := filepath.Join(dir, "scsi_device", s.name())
	require.NoError(c.t, os.MkdirAll(entry, 0o755))
	require.NoError(c.t, os.Symlink("../..", filepath.Join(entry, "device")))
	link := filepath.Join(c.root, "scsi_device", s.name())
	require.NoError(c.t, os.MkdirAll(filepath.Dir(link), 0o755))
	rel, err := filepath.Rel(filepath.Dir(link), entry)
	require.NoError(c.t, err)
	require.NoError(c.t, os.Symlink(rel, link))
}

func TestReadsTheLUNOfASession(t *testing.T) {
	c := newClassTree(t)
	c.lun(lunSpec{session: 1, host: 3, id: 1, state: "running", device: "sdb", sectors: "2048"})

	got, err := newTestReader(c).luns()

	require.NoError(t, err)
	assert.Equal(t, []lun{{session: 1, id: 1, device: "sdb", state: "running", sizeBytes: 2048 * 512, hasSize: true}}, got)
}

func TestMultipathPathsAreEachReportedUnderTheirOwnDeviceAndNotTheMapper(t *testing.T) {
	c := newClassTree(t)
	c.lun(lunSpec{session: 1, host: 3, id: 1, state: "running", device: "sdb", sectors: "2048", holder: "dm-0"})
	c.lun(lunSpec{session: 2, host: 4, id: 1, state: "running", device: "sdc", sectors: "2048", holder: "dm-0"})

	got, err := newTestReader(c).luns()

	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "sdb", got[0].device)
	assert.Equal(t, "sdc", got[1].device)
	assert.Equal(t, "dm-0", got[0].holder)
	assert.Equal(t, "dm-0", got[1].holder)
	for _, l := range got {
		assert.NotEqual(t, "dm-0", l.device, "the mapper device is not a LUN of its own")
	}
}

func TestADeviceThatIsNotBehindISCSIIsLeftOut(t *testing.T) {
	c := newClassTree(t)
	c.localDisk()
	c.lun(lunSpec{session: 1, host: 3, id: 0, state: "running", device: "sdb", sectors: "8"})

	got, err := newTestReader(c).luns()

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "sdb", got[0].device)
}

func TestADeviceThatIsNotADiskIsLeftOutWithoutAnError(t *testing.T) {
	c := newClassTree(t)
	c.lun(lunSpec{session: 1, host: 3, id: 0, state: "running", typ: "12"}) // storage array controller
	c.lun(lunSpec{session: 1, host: 3, id: 1, state: "running", device: "sdb", sectors: "8"})

	got, err := newTestReader(c).luns()

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, 1, got[0].id)
	assert.NoError(t, got[0].err)
}

func TestAnUnreadableStateIsUnknownAndKeepsTheSize(t *testing.T) {
	c := newClassTree(t)
	c.lun(lunSpec{session: 1, host: 3, id: 1, device: "sdb", sectors: "8"})

	got, err := newTestReader(c).luns()

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "unknown", got[0].state)
	assert.Error(t, got[0].err)
	assert.True(t, got[0].hasSize)
}

func TestAnUnmappedLUNWithoutABlockDeviceIsUnknownWhateverItsState(t *testing.T) {
	c := newClassTree(t)
	c.lun(lunSpec{session: 1, host: 3, id: 1, state: "running"})

	got, err := newTestReader(c).luns()

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "unknown", got[0].state)
	assert.Empty(t, got[0].device)
	assert.Error(t, got[0].err)
	assert.False(t, got[0].hasSize)
}

func TestAnEmptyBlockDirectoryIsNoBlockDevice(t *testing.T) {
	c := newClassTree(t)
	c.lun(lunSpec{session: 1, host: 3, id: 1, state: "running"})
	dir := filepath.Join(filepath.Dir(c.root), "devices", "platform", "host3", "session1", "target3:0:0", "3:0:0:1", "block")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	got, err := newTestReader(c).luns()

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.ErrorIs(t, got[0].err, errNoBlockDevice)
	assert.Equal(t, "unknown", got[0].state)
}

func TestAnUnreadableSizeKeepsTheStateAndDropsTheSize(t *testing.T) {
	c := newClassTree(t)
	c.lun(lunSpec{session: 1, host: 3, id: 1, state: "blocked", device: "sdb"})

	got, err := newTestReader(c).luns()

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "blocked", got[0].state)
	assert.False(t, got[0].hasSize)
	assert.Error(t, got[0].err)
}

func TestASizeThatIsNotANumberIsAnError(t *testing.T) {
	c := newClassTree(t)
	c.lun(lunSpec{session: 1, host: 3, id: 1, state: "running", device: "sdb", sectors: "lots"})

	got, err := newTestReader(c).luns()

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.False(t, got[0].hasSize)
	assert.Error(t, got[0].err)
}

func TestATypeThatIsNotANumberIsAnErrorAndNotADiskSkip(t *testing.T) {
	c := newClassTree(t)
	c.lun(lunSpec{session: 1, host: 3, id: 1, state: "running", typ: "disk", device: "sdb", sectors: "8"})

	got, err := newTestReader(c).luns()

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Error(t, got[0].err)
	assert.Equal(t, "running", got[0].state)
}

func TestHoldersThatCannotBeListedAreAnError(t *testing.T) {
	c := newClassTree(t)
	c.lun(lunSpec{session: 1, host: 3, id: 1, state: "running", device: "sdb", sectors: "8", holder: "dm-0"})
	r := newTestReader(c)
	inner := r.readDir
	r.readDir = func(p string) ([]os.DirEntry, error) {
		if filepath.Base(p) == "holders" {
			return nil, &os.PathError{Op: "open", Path: p, Err: syscall.EACCES}
		}
		return inner(p)
	}

	got, err := r.luns()

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.ErrorIs(t, got[0].err, syscall.EACCES)
	assert.Empty(t, got[0].holder)
	assert.Equal(t, "running", got[0].state)
}

func TestEveryHolderIsCountedAndTheFirstIsNamed(t *testing.T) {
	c := newClassTree(t)
	c.lun(lunSpec{session: 1, host: 3, id: 1, state: "running", device: "sdb", sectors: "8", holder: "dm-1,dm-0,dm-2"})
	c.lun(lunSpec{session: 1, host: 3, id: 2, state: "running", device: "sdc", sectors: "8", holder: "dm-3"})
	c.lun(lunSpec{session: 1, host: 3, id: 3, state: "running", device: "sdd", sectors: "8"})

	got, err := newTestReader(c).luns()

	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "dm-0", got[0].holder)
	assert.Equal(t, 3, got[0].holders)
	assert.Equal(t, 1, got[1].holders)
	assert.Zero(t, got[2].holders)
}

// goneReader makes the type file of one device fail with err.
func goneReader(c *classTree, device string, err error) *sysfsReader {
	r := newTestReader(c)
	inner := r.readFile
	r.readFile = func(p string) ([]byte, error) {
		if filepath.Base(p) == "type" && strings.Contains(p, device) {
			return nil, &os.PathError{Op: "read", Path: p, Err: err}
		}
		return inner(p)
	}
	return r
}

func TestADeviceThatVanishesWhileItIsReadMakesNoLUNAndOneInfoLine(t *testing.T) {
	for name, gone := range map[string]error{"ENODEV": syscall.ENODEV, "ESTALE": syscall.ESTALE, "ENOENT": syscall.ENOENT} {
		t.Run(name, func(t *testing.T) {
			c := newClassTree(t)
			c.lun(lunSpec{session: 1, host: 3, id: 1, state: "running", device: "sdb", sectors: "8"})
			c.lun(lunSpec{session: 1, host: 3, id: 2, state: "running", device: "sdc", sectors: "8"})
			core, logs := observer.New(zapcore.InfoLevel)
			r := goneReader(c, "3:0:0:1", gone)
			r.logger = zap.New(core)

			var got []lun
			for range 3 {
				var err error
				got, err = r.luns()
				require.NoError(t, err)
			}

			require.Len(t, got, 1, "no ghost LUN for the vanished device")
			assert.Equal(t, "sdc", got[0].device)
			assert.Equal(t, 1, logs.FilterMessage("A SCSI device disappeared while it was read and is not reported").Len())
		})
	}
}

func TestAVanishedDeviceIsLoggedAgainAfterItCameBack(t *testing.T) {
	c := newClassTree(t)
	c.lun(lunSpec{session: 1, host: 3, id: 1, state: "running", device: "sdb", sectors: "8"})
	core, logs := observer.New(zapcore.InfoLevel)
	r := goneReader(c, "3:0:0:1", syscall.ENODEV)
	r.logger = zap.New(core)
	good := newTestReader(c)

	_, _ = r.luns()
	r.readFile = good.readFile
	back, _ := r.luns()
	r.readFile = goneReader(c, "3:0:0:1", syscall.ENODEV).readFile
	_, _ = r.luns()

	assert.Len(t, back, 1)
	assert.Equal(t, 2, logs.FilterMessage("A SCSI device disappeared while it was read and is not reported").Len())
}

func TestATypeThatFailsToReadForAnotherReasonKeepsTheLUNAsUnknown(t *testing.T) {
	c := newClassTree(t)
	c.lun(lunSpec{session: 1, host: 3, id: 1, state: "running", device: "sdb", sectors: "8"})

	got, err := goneReader(c, "3:0:0:1", syscall.EIO).luns()

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.ErrorIs(t, got[0].err, syscall.EIO)
}

func TestAnAttributeReadIsCappedAtOnePage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("x", 10*maxAttrBytes)), 0o644))

	got, err := readBounded(path)

	require.NoError(t, err)
	assert.Len(t, got, maxAttrBytes)
	_, err = readBounded(filepath.Join(t.TempDir(), "missing"))
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

func TestTheSizeSaturatesInsteadOfWrapping(t *testing.T) {
	assert.Equal(t, uint64(math.MaxUint64), sectorsToBytes(math.MaxUint64))
	assert.Equal(t, uint64(math.MaxUint64), sectorsToBytes(math.MaxUint64/sectorBytes+1))
	assert.Equal(t, uint64(1024), sectorsToBytes(2))
	assert.Zero(t, sectorsToBytes(0))
}

func TestNoSCSIDeviceClassGivesNoLUNsAndNoError(t *testing.T) {
	c := newClassTree(t)

	got, err := newTestReader(c).luns()

	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestAnUnreadableSCSIDeviceClassIsAnError(t *testing.T) {
	c := newClassTree(t)
	r := newTestReader(c)
	r.readDir = func(string) ([]os.DirEntry, error) {
		return nil, &os.PathError{Op: "open", Path: c.root, Err: syscall.EACCES}
	}

	_, err := r.luns()

	require.Error(t, err)
	assert.ErrorIs(t, err, syscall.EACCES)
}

func TestALinkThatCannotBeFollowedIsSkipped(t *testing.T) {
	c := newClassTree(t)
	c.lun(lunSpec{session: 1, host: 3, id: 1, state: "running", device: "sdb", sectors: "8"})
	c.lun(lunSpec{session: 1, host: 3, id: 2, state: "running", device: "sdc", sectors: "8"})
	r := newTestReader(c)
	inner := r.readlink
	r.readlink = func(p string) (string, error) {
		if filepath.Base(p) == "3:0:0:1" {
			return "", errors.New("input/output error")
		}
		return inner(p)
	}

	got, err := r.luns()

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "sdc", got[0].device)
}

func TestLUNsComeInSessionAndLUNOrder(t *testing.T) {
	c := newClassTree(t)
	c.lun(lunSpec{session: 10, host: 5, id: 1, state: "running", device: "sdd", sectors: "8"})
	c.lun(lunSpec{session: 2, host: 4, id: 10, state: "running", device: "sdc", sectors: "8"})
	c.lun(lunSpec{session: 2, host: 4, id: 2, state: "running", device: "sdb", sectors: "8"})

	got, err := newTestReader(c).luns()

	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, []string{"sdb", "sdc", "sdd"}, []string{got[0].device, got[1].device, got[2].device})
}

func TestTheLUNReaderOpensOnlyTheListedFiles(t *testing.T) {
	c := newClassTree(t)
	c.lun(lunSpec{session: 1, host: 3, id: 1, state: "running", device: "sdb", sectors: "8", holder: "dm-0"})
	c.writeAbs(filepath.Join(filepath.Dir(c.root), "devices", "platform", "host3", "session1", "target3:0:0", "3:0:0:1", "vendor"), "ACME")
	c.writeAbs(filepath.Join(filepath.Dir(c.root), "devices", "platform", "host3", "session1", "target3:0:0", "3:0:0:1", "rev"), "1.0")
	r := newTestReader(c)
	var opened []string
	inner := r.readFile
	r.readFile = func(p string) ([]byte, error) {
		opened = append(opened, filepath.Base(p))
		return inner(p)
	}

	_, err := r.luns()

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"type", "state", "size"}, opened)
}
