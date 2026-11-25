// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"errors"
	"math"
	"path"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

const (
	defaultSpaceTimeout = 20 * time.Second
	maxDatasetVisits    = 50000
	maxDatasetDepth     = 64
)

// Positions in the vdev statistics array of a pool reply (vdev_stat_t).
const (
	vsReadErrors     = 20
	vsWriteErrors    = 21
	vsChecksumErrors = 22
	scanStateIndex   = 1
)

var (
	errSpaceTimeout = errors.New("ZFS space read is slower than its time limit; its result is used when it finishes")
	errSpaceBusy    = errors.New("a slow ZFS space read is still running")
)

// suspendedHealthCode is the code of a pool that stopped I/O after a failure; the pool reply says so
// in a key of its own and not in the vdev state.
const suspendedHealthCode = 6

// Vdev states and aux codes the health code depends on (vdev_state_t, vdev_aux_t).
const (
	vdevStateCantOpen = 4
	vdevAuxCorrupt    = 2
	vdevAuxBadLog     = 13
	vdevAuxSplitPool  = 15
	vsAuxIndex        = 2
)

// Scan functions of the scan_stats array (pool_scan_func_t).
const (
	scanFuncNone     = 0
	scanFuncScrub    = 1
	scanFuncResilver = 2
)

// healthStates maps the vdev state a pool reports to its name and the code of zfs.pool.health, which
// is the numbering of zfs_exporter. A root vdev that cannot open is UNAVAIL here and FAULTED when its
// aux code says the data is corrupt; healthFor decides.
var healthStates = map[uint64]struct {
	name string
	code int64
}{
	7: {"ONLINE", 0}, 6: {"DEGRADED", 1}, 5: {"FAULTED", 2}, 2: {"OFFLINE", 3}, 1: {"OFFLINE", 3}, 4: {"UNAVAIL", 4}, 3: {"REMOVED", 5},
}

// healthFor returns the code of a root vdev state as zpool names it; ok is false for a state or a
// split pool that has no code.
func healthFor(state, aux uint64) (code int64, ok bool) {
	if state == vdevStateCantOpen {
		switch aux {
		case vdevAuxCorrupt, vdevAuxBadLog:
			return 2, true
		case vdevAuxSplitPool:
			return 0, false
		}
	}
	h, known := healthStates[state]
	return h.code, known
}

// zfsAPI is the part of the client the reader uses.
type zfsAPI interface {
	poolStats(pool string) (*nvList, error)
	poolProps(pool string) (*nvList, error)
	objsetStats(name string) (*nvList, error)
	children(parent string, fn func(name string, props func() (*nvList, error)) bool) error
	close() error
}

type poolSpace struct {
	name     string
	props    map[string]uint64
	health   int64
	hasState bool
	aux      uint64    // aux code of the root vdev
	errs     [3]uint64 // read, write, checksum
	hasErrs  bool
	scan     uint64 // state of the newest scan
	scanFunc uint64
	hasScan  bool
}

type datasetSpace struct {
	pool, name string
	isVolume   bool
	props      map[string]uint64
}

// spaceResult is one scrape of the ioctl layer; isSupported is false on a system that has none.
type spaceResult struct {
	isSupported           bool
	pools                 []poolSpace
	datasets              []datasetSpace
	poolOK, datasetOK     bool
	datasetRan            bool
	poolObjs, datasetObjs int
	poolTime, datasetTime time.Duration
	// Reads that failed in this scrape: one for each pool read or statistics call, dataset read or child
	// list. An object that vanished between the list and the read is not a failure.
	poolFailures, datasetFailures int
}

// spaceSource reads pool and dataset space through the control device.
type spaceSource struct {
	logger      *zap.Logger
	cfg         *Config
	open        func(string) (zfsDevice, error)
	ep          *episodes
	now         func() time.Time
	timeout     time.Duration
	mu          sync.Mutex // held by a read
	api         zfsAPI
	needsReopen bool // the device went away; close it and open it again

	poolFails, datasetFails int // failed reads of the scrape in progress

	lateMu sync.Mutex
	late   chan *spaceResult // the read that outran the timeout
}

func newSpaceSource(logger *zap.Logger, cfg *Config, open func(string) (zfsDevice, error)) *spaceSource {
	return &spaceSource{
		logger: logger, cfg: cfg, open: open, ep: newEpisodes(logger),
		now: time.Now, timeout: defaultSpaceTimeout,
	}
}

// scan runs read in its own goroutine so a hung pool costs one goroutine and not the scrape loop.
// A read that outruns the timeout keeps running and its result is returned by a later scan, so a
// walk longer than the timeout still completes; until then scans return errSpaceBusy.
func (s *spaceSource) scan(pools []string) (*spaceResult, error) {
	s.lateMu.Lock()
	defer s.lateMu.Unlock()
	if s.late != nil {
		select {
		case r := <-s.late:
			s.late = nil
			return r, nil
		default:
			return nil, errSpaceBusy
		}
	}
	done := make(chan *spaceResult, 1)
	go func() { done <- s.read(pools) }()
	select {
	case r := <-done:
		return r, nil
	case <-time.After(s.timeout):
		s.late = done
		return nil, errSpaceTimeout
	}
}

// close closes the device unless a read is using it.
func (s *spaceSource) close() {
	if !s.mu.TryLock() {
		return
	}
	defer s.mu.Unlock()
	if s.api != nil {
		_ = s.api.close()
		s.api = nil
	}
}

func (s *spaceSource) datasetsOn() bool { return len(s.cfg.DatasetInclude) > 0 }

const openKey = "device"

// connect opens the device once; a failed open is tried again on the next scrape.
// It reports false on a failure, and unsupported when the system has no such device.
func (s *spaceSource) connect() (ok, unsupported bool) {
	if s.api != nil {
		return true, false
	}
	path := s.cfg.IoctlDevice
	dev, err := s.open(path)
	if errors.Is(err, errIoctlUnsupported) {
		return false, true
	}
	if err != nil {
		s.ep.warn(openKey, "Cannot open the ZFS control device, so no pool or dataset space series",
			zap.String("device", path), zap.Error(err))
		return false, false
	}
	s.ep.clear(openKey)
	s.api = newZfsClient(dev)
	return true, false
}

// read returns the pool series and, with dataset_include set, the dataset series of the named pools.
func (s *spaceSource) read(pools []string) *spaceResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.poolFails, s.datasetFails = 0, 0
	r := &spaceResult{isSupported: true, datasetRan: s.datasetsOn()}
	ok, unsupported := s.connect()
	if unsupported {
		r.isSupported = false
		return r
	}
	if !ok {
		return r
	}
	s.readPools(r, pools)
	if r.datasetRan {
		s.readDatasets(r)
	}
	if s.needsReopen {
		_ = s.api.close()
		s.api, s.needsReopen = nil, false
	}
	return r
}

func isPoolOrDatasetGone(err error) bool {
	return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ESRCH)
}

// noteErr marks the device for a new open when an error says it is gone, as after a module reload.
func (s *spaceSource) noteErr(err error) {
	if errors.Is(err, syscall.ENXIO) || errors.Is(err, syscall.EBADF) || errors.Is(err, syscall.ENODEV) {
		s.needsReopen = true
	}
}

func (s *spaceSource) readPools(r *spaceResult, pools []string) {
	start := s.now()
	failed := 0
	for _, name := range pools {
		ps, err := s.readPool(name)
		switch {
		case err == nil:
			r.pools = append(r.pools, ps)
		case isPoolOrDatasetGone(err):
			s.logger.Debug("ZFS pool is gone between the list and the read", zap.String("zfs.pool", name))
		default:
			failed++
			s.poolFails++
			s.noteErr(err)
			s.ep.warn("pool:"+name, "Cannot read ZFS pool space", zap.String("zfs.pool", name), zap.Error(err))
		}
	}
	r.poolOK = failed == 0 || failed < len(pools)
	r.poolObjs = len(r.pools)
	r.poolFailures = s.poolFails
	r.poolTime = s.now().Sub(start)
}

var poolPropKeys = []string{"size", "allocated", "free", "freeing", "leaked", "fragmentation", "dedupratio", "health", "readonly"}

// propValue reads the integer under "value" in the list the kernel keeps for each property.
func propValue(props *nvList, name string) (uint64, bool) {
	inner, ok := props.subList(name)
	if !ok {
		return 0, false
	}
	return inner.uint64Value("value")
}

func readProps(props *nvList, keys []string) map[string]uint64 {
	out := make(map[string]uint64, len(keys))
	for _, k := range keys {
		if v, ok := propValue(props, k); ok {
			out[k] = v
		}
	}
	return out
}

func (s *spaceSource) readPool(name string) (poolSpace, error) {
	props, err := s.api.poolProps(name)
	if err != nil {
		return poolSpace{}, err
	}
	s.ep.clear("pool:" + name)
	ps := poolSpace{name: name, props: readProps(props, poolPropKeys)}
	suspended := false
	stats, err := s.api.poolStats(name)
	switch {
	case err == nil:
		s.ep.clear("stats:" + name)
		suspended = applyPoolStats(&ps, stats)
	case isPoolOrDatasetGone(err):
		s.logger.Debug("ZFS pool is gone before its statistics", zap.String("zfs.pool", name))
	default:
		s.poolFails++
		s.noteErr(err)
		s.ep.warn("stats:"+name, "Cannot read ZFS pool statistics, so no vdev error or scrub series",
			zap.String("zfs.pool", name), zap.Error(err))
		// A partial reply still says whether the pool is suspended.
		v, ok := stats.uint64Value("suspended")
		suspended = ok && v != 0
	}
	s.setHealth(&ps, suspended)
	return ps, nil
}

// applyPoolStats reads the root aux code, the vdev error and the scan series from a pool reply and
// reports whether the pool is suspended.
func applyPoolStats(ps *poolSpace, stats *nvList) bool {
	tree, hasTree := stats.subList("vdev_tree")
	if hasTree {
		ps.errs, ps.hasErrs = vdevErrors(tree)
		if root := tree.uint64Values("vdev_stats"); len(root) > vsAuxIndex {
			ps.aux = root[vsAuxIndex]
		}
	}
	scan := stats.uint64Values("scan_stats")
	if hasTree && len(scan) == 0 {
		scan = tree.uint64Values("scan_stats")
	}
	if len(scan) > scanStateIndex {
		ps.scanFunc, ps.scan, ps.hasScan = scan[0], scan[scanStateIndex], true
	}
	v, ok := stats.uint64Value("suspended")
	return ok && v != 0
}

// setHealth sets the health code from the state of the root vdev as zpool names it, or SUSPENDED
// when the pool reports it.
func (s *spaceSource) setHealth(ps *poolSpace, suspended bool) {
	key := "health:" + ps.name
	if suspended {
		ps.health, ps.hasState = suspendedHealthCode, true
		s.ep.clear(key)
		return
	}
	state, ok := ps.props["health"]
	if !ok {
		return
	}
	if code, known := healthFor(state, ps.aux); known {
		ps.health, ps.hasState = code, true
		s.ep.clear(key)
		return
	}
	s.ep.warn(key, "ZFS pool health state is not in the code table",
		zap.String("zfs.pool", ps.name), zap.String("state", strconv.FormatUint(state, 10)),
		zap.String("aux", strconv.FormatUint(ps.aux, 10)))
}

// vdevErrors sums the error counters of the leaf vdevs under a vdev tree.
func vdevErrors(node *nvList) (sum [3]uint64, ok bool) {
	kids := node.subLists("children")
	if len(kids) == 0 {
		stats := node.uint64Values("vdev_stats")
		if len(stats) <= vsChecksumErrors {
			return sum, false
		}
		return [3]uint64{stats[vsReadErrors], stats[vsWriteErrors], stats[vsChecksumErrors]}, true
	}
	for _, kid := range kids {
		part, found := vdevErrors(kid)
		if !found {
			continue
		}
		ok = true
		for i := range sum {
			sum[i] += part[i]
		}
	}
	return sum, ok
}

var datasetPropKeys = []string{
	"type", "used", "available", "quota", "refquota", "referenced", "written", "logicalused",
	"usedbydataset", "usedbysnapshots", "usedbychildren", "reservation", "refreservation", "compressratio", "volsize",
}

const (
	zfsTypeFilesystem = 1
	zfsTypeVolume     = 4
)

func newDataset(pool, name string, props *nvList) datasetSpace {
	d := datasetSpace{pool: pool, name: name, props: readProps(props, datasetPropKeys)}
	_, hasSize := d.props["volsize"]
	switch d.props["type"] {
	case zfsTypeVolume:
		d.isVolume = true
	case zfsTypeFilesystem:
	default:
		d.isVolume = hasSize
	}
	return d
}

func (s *spaceSource) readDatasets(r *spaceResult) {
	start := s.now()
	r.datasetOK = true
	visits := 0
	for _, p := range r.pools {
		s.walk(r, p.name, &visits)
	}
	r.datasets = s.capDatasets(r.datasets)
	r.datasetObjs = len(r.datasets)
	r.datasetFailures = s.datasetFails
	r.datasetTime = s.now().Sub(start)
}

// walk reads the root dataset of a pool and then every child, selecting by the include and exclude globs.
func (s *spaceSource) walk(r *spaceResult, pool string, visits *int) {
	if s.cfg.datasetSelected(pool) {
		props, err := s.api.objsetStats(pool)
		s.addDataset(r, pool, pool, props, err)
	}
	var visit func(parent string, depth int) bool
	visit = func(parent string, depth int) bool {
		if depth > maxDatasetDepth {
			s.ep.warn("depth", "ZFS dataset tree is deeper than the limit", zap.Int("limit", maxDatasetDepth))
			r.datasetOK = false
			return false
		}
		if !couldMatchBelow(s.cfg.DatasetInclude, parent) {
			return true
		}
		// Only a selected child is decoded; the others cost a name.
		type child struct {
			name     string
			props    *nvList
			err      error
			selected bool
		}
		var kids []child
		err := s.api.children(parent, func(name string, decode func() (*nvList, error)) bool {
			k := child{name: name}
			if k.selected = s.cfg.datasetSelected(name); k.selected {
				k.props, k.err = decode()
			}
			kids = append(kids, k)
			return true
		})
		for _, k := range kids {
			if *visits++; *visits > maxDatasetVisits {
				s.ep.warn("visits", "ZFS dataset walk stopped at its limit", zap.Int("limit", maxDatasetVisits))
				r.datasetOK = false
				return false
			}
			if !strings.HasPrefix(k.name, parent+"/") {
				s.ep.warn("childname:"+parent, "ZFS child dataset name does not start with its parent",
					zap.String("zfs.dataset", parent), zap.String("child", k.name))
				continue
			}
			if k.selected {
				s.addDataset(r, pool, k.name, k.props, k.err)
			}
			if !visit(k.name, depth+1) {
				return false
			}
		}
		if err != nil && !isPoolOrDatasetGone(err) {
			s.datasetFails++
			s.noteErr(err)
			s.ep.warn("children:"+parent, "Cannot list the children of a ZFS dataset",
				zap.String("zfs.dataset", parent), zap.Error(err))
			r.datasetOK = false
			return false
		}
		return true
	}
	visit(pool, 0)
}

func (s *spaceSource) addDataset(r *spaceResult, pool, name string, props *nvList, err error) {
	key := "dataset:" + name
	switch {
	case isPoolOrDatasetGone(err):
		s.logger.Debug("ZFS dataset is gone between the list and the read", zap.String("zfs.dataset", name))
	case err != nil:
		s.datasetFails++
		s.noteErr(err)
		s.ep.warn(key, "Cannot read ZFS dataset space", zap.String("zfs.dataset", name), zap.Error(err))
	case props == nil:
		s.datasetFails++
		s.ep.warn(key, "ZFS dataset reply has no properties", zap.String("zfs.dataset", name))
	default:
		s.ep.clear(key)
		r.datasets = append(r.datasets, newDataset(pool, name, props))
	}
}

// couldMatchBelow reports whether a descendant of the dataset can match an include glob. A glob
// matches one path segment per slash-separated part, so a dataset below name matches only a glob that
// has more parts than name and whose leading parts match the parts of name.
func couldMatchBelow(include []string, name string) bool {
	parts := strings.Split(name, "/")
	for _, pattern := range include {
		patternParts := strings.Split(pattern, "/")
		if len(patternParts) <= len(parts) {
			continue
		}
		matches := true
		for i, part := range parts {
			if ok, err := path.Match(patternParts[i], part); err != nil || !ok {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

// capDatasets keeps the first max_datasets in sorted order.
func (s *spaceSource) capDatasets(all []datasetSpace) []datasetSpace {
	return capSorted(s.ep, s.cfg.MaxDatasets, all, func(d datasetSpace) (string, string) { return d.pool, d.name })
}

type bytesSeries struct{ name, prop, description string }

var poolBytesSeries = []bytesSeries{
	{"zfs.pool.size", "size", "Total size of the pool, as SIZE of zpool list."},
	{"zfs.pool.allocated", "allocated", "Space allocated in the pool, as ALLOC of zpool list."},
	{"zfs.pool.free", "free", "Free space in the pool, as FREE of zpool list."},
	{"zfs.pool.freeing", "freeing", "Space of destroyed data the pool has not yet freed."},
	{"zfs.pool.leaked", "leaked", "Space the pool has allocated and no object refers to."},
}

var datasetBytesSeries = []bytesSeries{
	{"zfs.dataset.space.used", "used", "Space the dataset and its descendants use, as USED of zfs list."},
	{"zfs.dataset.space.available", "available", "Space the dataset can still use, as AVAIL of zfs list."},
	{"zfs.dataset.space.quota", "quota", "Quota of the dataset; 0 means none."},
	{"zfs.dataset.space.refquota", "refquota", "Reference quota of the dataset; 0 means none."},
	{"zfs.dataset.space.referenced", "referenced", "Space the dataset itself references, as REFER of zfs list."},
	{"zfs.dataset.space.written", "written", "Space written since the last snapshot."},
	{"zfs.dataset.space.logical_used", "logicalused", "Logical space the dataset uses, before compression."},
	{"zfs.dataset.space.used_by_dataset", "usedbydataset", "Space the dataset itself uses."},
	{"zfs.dataset.space.used_by_snapshots", "usedbysnapshots", "Space its snapshots use."},
	{"zfs.dataset.space.used_by_children", "usedbychildren", "Space its descendants use."},
	{"zfs.dataset.space.reservation", "reservation", "Space reserved for the dataset and its descendants."},
	{"zfs.dataset.space.refreservation", "refreservation", "Space reserved for the dataset itself."},
}

func newGauge(sm pmetric.ScopeMetrics, name, unit, description string) pmetric.NumberDataPointSlice {
	return newMetric(sm, name, unit, description, true)
}

func emitIoctlSpace(sm pmetric.ScopeMetrics, r *spaceResult, tr *resetTracker, boot, now pcommon.Timestamp) {
	if r == nil || !r.isSupported {
		return
	}
	emitPoolSpace(sm, r.pools, tr, boot, now)
	emitDatasetSpace(sm, r.datasets, now)
	emitCollector(sm, r, now)
}

// poolPoints appends one point per pool that has a value; the metric is created with its first point.
func poolPoints(sm pmetric.ScopeMetrics, name, unit, description string, pools []poolSpace,
	now pcommon.Timestamp, value func(poolSpace) (float64, uint64, bool, bool)) {
	var dps pmetric.NumberDataPointSlice
	hasMetric := false
	for _, p := range pools {
		f, u, isWhole, ok := value(p)
		if !ok {
			continue
		}
		if !hasMetric {
			dps, hasMetric = newGauge(sm, name, unit, description), true
		}
		dp := dps.AppendEmpty()
		dp.Attributes().PutStr("zfs.pool", p.name)
		dp.SetTimestamp(now)
		if isWhole {
			setValue(dp, u, 1)
		} else {
			dp.SetDoubleValue(f)
		}
	}
}

func propInt(prop string) func(poolSpace) (float64, uint64, bool, bool) {
	return func(p poolSpace) (float64, uint64, bool, bool) {
		v, ok := p.props[prop]
		return 0, v, true, ok
	}
}

func propRatio(prop string, skipMax bool) func(poolSpace) (float64, uint64, bool, bool) {
	return func(p poolSpace) (float64, uint64, bool, bool) {
		v, ok := p.props[prop]
		if skipMax && v == math.MaxUint64 {
			ok = false
		}
		return float64(v) / 100, 0, false, ok
	}
}

func emitPoolSpace(sm pmetric.ScopeMetrics, pools []poolSpace, tr *resetTracker, boot, now pcommon.Timestamp) {
	for _, m := range poolBytesSeries {
		poolPoints(sm, m.name, "By", m.description, pools, now, propInt(m.prop))
	}
	poolPoints(sm, "zfs.pool.fragmentation", "1", "Pool fragmentation as a ratio from 0 to 1.", pools, now, propRatio("fragmentation", true))
	poolPoints(sm, "zfs.pool.capacity", "1", "Allocated over size as a ratio from 0 to 1.", pools, now,
		func(p poolSpace) (float64, uint64, bool, bool) {
			size, okSize := p.props["size"]
			alloc, okAlloc := p.props["allocated"]
			if !okSize || !okAlloc {
				return 0, 0, false, false
			}
			if size == 0 {
				return 0, 0, false, true
			}
			return float64(alloc) / float64(size), 0, false, true
		})
	poolPoints(sm, "zfs.pool.dedup.ratio", "1", "Deduplication ratio of the pool; 1 means none.", pools, now, propRatio("dedupratio", false))
	poolPoints(sm, "zfs.pool.health", "1", "Pool health code: 0 ONLINE, 1 DEGRADED, 2 FAULTED, 3 OFFLINE, 4 UNAVAIL, 5 REMOVED.", pools, now,
		func(p poolSpace) (float64, uint64, bool, bool) { return 0, uint64(p.health), true, p.hasState })
	poolPoints(sm, "zfs.pool.readonly", "1", "1 when the pool is read-only, else 0.", pools, now, propInt("readonly"))
	poolPoints(sm, "zfs.pool.scrub.state", "1", "Scrub state of the pool: 0 idle, 1 scanning, 2 finished, 3 canceled.", pools, now,
		func(p poolSpace) (float64, uint64, bool, bool) {
			return 0, p.scan, true, p.hasScan && (p.scanFunc == scanFuncNone || p.scanFunc == scanFuncScrub)
		})
	poolPoints(sm, "zfs.pool.resilver.state", "1", "Resilver state of the pool: 0 idle, 1 scanning, 2 finished, 3 canceled.", pools, now,
		func(p poolSpace) (float64, uint64, bool, bool) {
			return 0, p.scan, true, p.hasScan && p.scanFunc == scanFuncResilver
		})
	emitVdevErrors(sm, pools, tr, boot, now)
}

var errorTypes = [3]string{"read", "write", "checksum"}

func emitVdevErrors(sm pmetric.ScopeMetrics, pools []poolSpace, tr *resetTracker, boot, now pcommon.Timestamp) {
	var dps pmetric.NumberDataPointSlice
	hasMetric := false
	for _, p := range pools {
		if !p.hasErrs {
			continue
		}
		for i, typ := range errorTypes {
			if !hasMetric {
				dps, hasMetric = newMetric(sm, "zfs.pool.vdev.errors", "{error}", "Read, write and checksum errors summed over the leaf vdevs of the pool.", false), true
			}
			dp := dps.AppendEmpty()
			dp.Attributes().PutStr("zfs.pool", p.name)
			dp.Attributes().PutStr("error.type", typ)
			dp.SetStartTimestamp(tr.startFor("zfs.pool.vdev.errors\x00"+p.name+"\x00"+typ, p.errs[i], boot, now))
			dp.SetTimestamp(now)
			setValue(dp, p.errs[i], 1)
		}
	}
}

func datasetKind(d datasetSpace) string {
	if d.isVolume {
		return "volume"
	}
	return "filesystem"
}

func putDatasetAttrs(dp pmetric.NumberDataPoint, d datasetSpace) {
	dp.Attributes().PutStr("zfs.pool", d.pool)
	dp.Attributes().PutStr("zfs.dataset", d.name)
	dp.Attributes().PutStr("zfs.dataset.type", datasetKind(d))
}

func emitDatasetSpace(sm pmetric.ScopeMetrics, datasets []datasetSpace, now pcommon.Timestamp) {
	for _, m := range datasetBytesSeries {
		emitDatasetBytes(sm, m.name, m.description, m.prop, datasets, now, false)
	}
	emitDatasetBytes(sm, "zfs.dataset.volume.size", "Size of the volume.", "volsize", datasets, now, true)
	var dps pmetric.NumberDataPointSlice
	hasMetric := false
	for _, d := range datasets {
		v, ok := d.props["compressratio"]
		if !ok {
			continue
		}
		if !hasMetric {
			dps, hasMetric = newGauge(sm, "zfs.dataset.compression.ratio", "1", "Compression ratio of the dataset; 1 means none."), true
		}
		dp := dps.AppendEmpty()
		putDatasetAttrs(dp, d)
		dp.SetTimestamp(now)
		dp.SetDoubleValue(float64(v) / 100)
	}
}

func emitDatasetBytes(sm pmetric.ScopeMetrics, name, description, prop string, datasets []datasetSpace, now pcommon.Timestamp, volumesOnly bool) {
	var dps pmetric.NumberDataPointSlice
	hasMetric := false
	for _, d := range datasets {
		v, ok := d.props[prop]
		if !ok || (volumesOnly && !d.isVolume) {
			continue
		}
		if !hasMetric {
			dps, hasMetric = newGauge(sm, name, "By", description), true
		}
		dp := dps.AppendEmpty()
		putDatasetAttrs(dp, d)
		dp.SetTimestamp(now)
		setValue(dp, v, 1)
	}
}

func emitCollector(sm pmetric.ScopeMetrics, r *spaceResult, now pcommon.Timestamp) {
	type row struct {
		collector string
		ok        bool
		objects   int
		took      time.Duration
		failures  int
	}
	rows := []row{{"pool", r.poolOK, r.poolObjs, r.poolTime, r.poolFailures}}
	if r.datasetRan {
		rows = append(rows, row{"dataset", r.datasetOK, r.datasetObjs, r.datasetTime, r.datasetFailures})
	}
	success := newGauge(sm, "zfs.collector.success", "1", "1 when the read of the collector ended without a failure, else 0.")
	duration := newGauge(sm, "zfs.collector.duration", "s", "Time the read of the collector took.")
	objects := newGauge(sm, "zfs.collector.objects", "{object}", "Pools or datasets the collector read.")
	failures := newGauge(sm, "zfs.collector.failures", "{failure}", "Reads of the collector that failed in this scrape; the other objects still report.")
	for _, c := range rows {
		ok := int64(0)
		if c.ok {
			ok = 1
		}
		for _, dp := range []pmetric.NumberDataPoint{success.AppendEmpty(), duration.AppendEmpty(), objects.AppendEmpty(), failures.AppendEmpty()} {
			dp.Attributes().PutStr("collector", c.collector)
			dp.SetTimestamp(now)
		}
		success.At(success.Len() - 1).SetIntValue(ok)
		duration.At(duration.Len() - 1).SetDoubleValue(c.took.Seconds())
		objects.At(objects.Len() - 1).SetIntValue(int64(c.objects))
		failures.At(failures.Len() - 1).SetIntValue(int64(c.failures))
	}
}
