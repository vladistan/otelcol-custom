// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"encoding/binary"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// skipKernel answers the next-OID meta sysctls over a fakeSysctl tree. The pool state and txgs kstats
// are raw kstats, which the kernel creates with CTLFLAG_SKIP: CTL_SYSCTL_NEXT passes over them and
// CTL_SYSCTL_NEXTNOSKIP lists them.
type skipKernel struct {
	tree      *fakeSysctl
	hasNoSkip bool
}

func (k *skipKernel) isSkipped(name string) bool {
	return strings.HasSuffix(name, ".misc.state") || strings.HasSuffix(name, ".txgs")
}

func (k *skipKernel) raw(mib []int32, old, _ []byte) (int, error) {
	if len(mib) < 3 || mib[0] != 0 || (mib[1] != ctlNext[1] && mib[1] != ctlNextNoSkip[1]) {
		return 0, syscall.ENOENT
	}
	if mib[1] == ctlNextNoSkip[1] && !k.hasNoSkip {
		return 0, syscall.ENOENT
	}
	honorSkip := mib[1] == ctlNext[1]
	idx := 1
	switch oid := mib[2:]; {
	case len(oid) == 1 && oid[0] == 6:
		return 0, syscall.ENOENT // the sibling tree is the last node
	case len(oid) == 2:
		idx = int(oid[1]) + 1
	}
	for ; idx <= len(k.tree.names); idx++ {
		if honorSkip && k.isSkipped(k.tree.names[idx-1]) {
			continue
		}
		return putOID(old, []int32{5, int32(idx)}), nil
	}
	if idx == len(k.tree.names)+1 {
		return putOID(old, []int32{6}), nil // a sibling tree: the walk must stop here
	}
	return 0, syscall.ENOENT
}

func putOID(buf []byte, oid []int32) int {
	for i, w := range oid {
		binary.NativeEndian.PutUint32(buf[i*4:], uint32(w))
	}
	return len(oid) * 4
}

func (k *skipKernel) lookup(name string) ([]int32, error) {
	if name == "sysctl.nextnoskip" && k.hasNoSkip {
		return slices.Clone(ctlNextNoSkip), nil
	}
	return nil, syscall.ENOENT
}

// fns walks the tree through meta, as the real source does with the kernel's own answers.
func (k *skipKernel) fns(meta []int32) sysctlFns {
	fns := k.tree.fns()
	fns.nextOID = func(oid []int32) ([]int32, error) { return nextOIDVia(k.raw, meta, oid) }
	return fns
}

func rawKstatFake() *fakeSysctl {
	f := standardFake()
	for _, p := range []string{"tank", "backup", "scratch"} {
		f.addStr("kstat.zfs."+p+".txgs", txgsFile())
	}
	return f
}

func readWith(t *testing.T, k *skipKernel, meta []int32) snapshot {
	t.Helper()
	cfg := sysctlConfig()
	cfg.Datasets, cfg.Txg = true, true
	cfg.DatasetInclude = []string{"*", "*/*"}
	snap, err := newSysctlSource(zap.NewNop(), cfg, k.fns(meta)).read()
	require.NoError(t, err)
	return snap
}

func poolByName(snap snapshot, name string) (poolStats, bool) {
	for _, p := range snap.pools {
		if p.name == name {
			return p, true
		}
	}
	return poolStats{}, false
}

func TestNextMeta_KernelWithNoSkip_UsesIt(t *testing.T) {
	k := &skipKernel{tree: newFakeSysctl(), hasNoSkip: true}
	assert.Equal(t, ctlNextNoSkip, nextMeta(k.lookup))
}

func TestNextMeta_KernelWithoutNoSkip_FallsBackToNext(t *testing.T) {
	k := &skipKernel{tree: newFakeSysctl()}
	assert.Equal(t, ctlNext, nextMeta(k.lookup))
}

func TestWalkNoSkip_FindsPoolsAndTxgsOfRawKstats(t *testing.T) {
	k := &skipKernel{tree: rawKstatFake(), hasNoSkip: true}
	snap := readWith(t, k, nextMeta(k.lookup))

	require.Len(t, snap.pools, 3)
	tank, ok := poolByName(snap, "tank")
	require.True(t, ok)
	assert.Equal(t, "ONLINE", tank.state)
	assert.Len(t, tank.datasets, 2)
	assert.NotNil(t, tank.txg, "the txgs kstat is a raw kstat too")
	assert.Positive(t, snap.arc["size"])
}

func TestWalkNext_HidesRawKstats(t *testing.T) {
	k := &skipKernel{tree: rawKstatFake(), hasNoSkip: true}
	snap := readWith(t, k, ctlNext)

	assert.Empty(t, snap.pools, "CTL_SYSCTL_NEXT passes over the raw state kstat of every pool")
	assert.Positive(t, snap.arc["size"], "the ARC is read by name and does not depend on the walk")
}

func TestNextOIDVia_StopsAtTheEndOfTheTree(t *testing.T) {
	k := &skipKernel{tree: newFakeSysctl(), hasNoSkip: true}
	k.tree.addPool("tank", "ONLINE")
	next, err := nextOIDVia(k.raw, ctlNextNoSkip, []int32{5, 1})
	require.NoError(t, err)
	assert.Equal(t, []int32{6}, next)
	_, err = nextOIDVia(k.raw, ctlNextNoSkip, []int32{6})
	assert.ErrorIs(t, err, syscall.ENOENT)
}

// rawKstatScraper is a scraper over the walk of a kernel that hides raw kstats from CTL_SYSCTL_NEXT and a
// fake ioctl layer, with the pools tank and vault and their datasets.
func rawKstatScraper(t *testing.T, api *fakeAPI) *scraper {
	t.Helper()
	tree := newFakeSysctl()
	tree.addUint("kstat.zfs.misc.arcstats.size", 4294967296)
	tree.addPool("tank", "ONLINE")
	tree.addPool("vault", "ONLINE")
	k := &skipKernel{tree: tree, hasNoSkip: true}

	cfg := sysctlConfig()
	cfg.Datasets, cfg.Txg = true, false
	cfg.DatasetInclude = []string{"tank", "tank/*", "vault"}
	s := newScraperWith(zap.NewNop(), cfg, newSysctlSource(zap.NewNop(), cfg, k.fns(nextMeta(k.lookup))))
	s.space, _ = newTestSpace(cfg, api)
	return s
}

func twoPoolDatasetAPI(t *testing.T) *fakeAPI {
	api := twoPoolAPI(t)
	api.objs["vault"] = propsNV(t, goodDatasetProps())
	return api
}

func TestAFreeBSDScrapeEmitsPoolAndDatasetSeriesForEachPool(t *testing.T) {
	s := rawKstatScraper(t, twoPoolDatasetAPI(t))

	md := scrapeOnce(t, s)

	for _, pool := range []string{"tank", "vault"} {
		assert.Equal(t, int64(1000), gaugeInt(t, md, "zfs.pool.size", poolAttr(pool)), pool)
	}
	names := metricNames(md)
	for _, want := range []string{"zfs.arc.size", "zfs.pool.state", "zfs.dataset.space.used"} {
		assert.Contains(t, names, want)
	}
	assert.Equal(t, int64(0), gaugeInt(t, md, "zfs.collector.failures", poolCollector))
}

func TestAFreeBSDScrapeWithOneFailingPoolKeepsTheOthersAndCountsTheFailure(t *testing.T) {
	api := twoPoolDatasetAPI(t)
	api.errs["props:tank"] = syscall.EIO
	s := rawKstatScraper(t, api)

	md := scrapeOnce(t, s)

	assert.Equal(t, int64(1000), gaugeInt(t, md, "zfs.pool.size", poolAttr("vault")))
	assert.Equal(t, int64(1), gaugeInt(t, md, "zfs.collector.failures", poolCollector))
	assert.Contains(t, metricNames(md), "zfs.pool.state", "the pool state comes from the sysctl source")
}
