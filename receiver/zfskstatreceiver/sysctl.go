// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"errors"
	"slices"
	"strings"
	"syscall"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	zfsRoot     = "kstat.zfs"
	poolPrefix  = zfsRoot + "."
	stateSuffix = ".misc.state"
	arcPrefix   = zfsRoot + ".misc.arcstats."
	sysctlKey   = "\x00sysctl"
	// maxWalk bounds the entries one walk of the kstat tree visits.
	maxWalk = 1 << 16
)

// objsetFields are the numeric objset fields the dataset metrics use.
var objsetFields = []string{"nread", "nwritten", "reads", "writes", "nunlinks"}

// sysctlFns holds the system calls the sysctl source makes, so tests can replace them.
// Each returns an error matching syscall.ENOENT when the name or OID does not exist.
type sysctlFns struct {
	lookupOID func(name string) ([]int32, error)
	nextOID   func(oid []int32) ([]int32, error)
	oidName   func(oid []int32) (string, error)
	getUint64 func(name string) (uint64, error)
	getString func(name string) (string, error)
	bootTime  func() (time.Time, error)
	// getfsstat lists the mounted file systems; nil, or an error matching ENOENT, means there is no list.
	getfsstat func() ([]mount, error)
}

// mount is one entry of the mounted file system list, with its statfs sizes in blocks.
type mount struct {
	from, fsType                   string
	blockSize, blocks, free, avail uint64
}

// sysctlSource reads the kstat.zfs sysctl tree.
type sysctlSource struct {
	logger *zap.Logger
	cfg    *Config
	fns    sysctlFns
	ep     *episodes
	absent bool
	failed bool

	mounts     []mount // read once per scrape, when the first pool needs it
	mountsRead bool
}

func newSysctlSource(logger *zap.Logger, cfg *Config, fns sysctlFns) *sysctlSource {
	return &sysctlSource{logger: logger, cfg: cfg, fns: fns, ep: newEpisodes(logger)}
}

func notFound(err error) bool { return errors.Is(err, syscall.ENOENT) }

func (s *sysctlSource) bootTime(now time.Time) time.Time {
	boot, err := s.fns.bootTime()
	if err != nil || boot.After(now) {
		return now
	}
	return boot
}

// fail records an error other than not found; the first one of an episode is logged as a warning.
func (s *sysctlSource) fail(name string, err error) {
	s.failed = true
	s.ep.warn(sysctlKey, "ZFS sysctl read failed", zap.String("name", name), zap.Error(err))
}

// read never returns an error: a failed sysctl is a warning and the metrics that could be read are kept.
func (s *sysctlSource) read() (snapshot, error) {
	s.failed = false
	s.mounts, s.mountsRead = nil, false
	snap := s.readTree()
	if !s.failed {
		s.ep.clear(sysctlKey)
	}
	return snap, nil
}

func (s *sysctlSource) readTree() snapshot {
	root, err := s.fns.lookupOID(zfsRoot)
	if notFound(err) {
		if !s.absent {
			s.absent = true
			s.logger.Info("ZFS sysctl tree kstat.zfs not found, no ZFS metrics until it exists")
		}
		return snapshot{}
	}
	if err != nil {
		s.fail(zfsRoot, err)
		return snapshot{}
	}
	s.absent = false

	var snap snapshot
	if s.cfg.ArcStats {
		snap.arc = s.readArc()
	}
	snap.pools = s.readPools(s.walk(root))
	return snap
}

// walk lists the names under root in OID order; an entry that vanishes mid-walk is skipped.
func (s *sysctlSource) walk(root []int32) []string {
	var names []string
	oid := root
	for range maxWalk {
		next, err := s.fns.nextOID(oid)
		if err != nil {
			if !notFound(err) {
				s.fail(zfsRoot, err)
			}
			break
		}
		if len(next) <= len(root) || !slices.Equal(next[:len(root)], root) {
			break
		}
		oid = next
		name, err := s.fns.oidName(oid)
		if notFound(err) {
			continue
		}
		if err != nil {
			s.fail(zfsRoot, err)
			break
		}
		names = append(names, name)
	}
	return names
}

func (s *sysctlSource) readArc() map[string]uint64 {
	arc := map[string]uint64{}
	for _, m := range arcMetrics {
		v, ok := s.uint64(arcPrefix + m.field)
		if ok {
			arc[m.field] = v
		}
	}
	return arc
}

// readPools reads the state of each selected pool named in the walk; a pool that vanished is skipped.
func (s *sysctlSource) readPools(names []string) []poolStats {
	var pools []poolStats
	for _, n := range names {
		if len(n) <= len(poolPrefix)+len(stateSuffix) || !strings.HasPrefix(n, poolPrefix) || !strings.HasSuffix(n, stateSuffix) {
			continue
		}
		pool := n[len(poolPrefix) : len(n)-len(stateSuffix)]
		if !s.cfg.selected(pool) {
			continue
		}
		state, ok := s.string(n)
		if !ok {
			continue
		}
		p := poolStats{name: pool, state: strings.TrimSpace(state)}
		if s.cfg.Datasets {
			p.datasets = s.readDatasets(pool, names)
		}
		if s.cfg.Txg {
			p.txg = s.readTxg(pool, names)
		}
		p.space = s.readSpace(pool)
		pools = append(pools, p)
	}
	return pools
}

// readDatasets reads the objset-* nodes of a pool; fields are matched by name and unknown names are ignored.
func (s *sysctlSource) readDatasets(pool string, names []string) []datasetStats {
	prefix := poolPrefix + pool + ".dataset." + objsetPrefix
	var order []string
	found := map[string][]string{}
	for _, n := range names {
		if !strings.HasPrefix(n, prefix) {
			continue
		}
		obj, field, ok := strings.Cut(n[len(poolPrefix+pool+".dataset."):], ".")
		if !ok {
			continue
		}
		if _, seen := found[obj]; !seen {
			order = append(order, obj)
		}
		found[obj] = append(found[obj], field)
	}

	var out []datasetStats
	for _, obj := range order {
		base := poolPrefix + pool + ".dataset." + obj + "."
		if !slices.Contains(found[obj], datasetNameField) {
			continue
		}
		name, ok := s.string(base + datasetNameField)
		if !ok || name == "" || !s.cfg.datasetSelected(name) {
			continue
		}
		fields := map[string]uint64{}
		for _, f := range objsetFields {
			if !slices.Contains(found[obj], f) {
				continue
			}
			if v, ok := s.uint64(base + f); ok {
				fields[f] = v
			}
		}
		out = append(out, datasetStats{pool: pool, name: name, fields: fields})
	}
	return out
}

// readTxg reads the txgs kstat of a pool when the tree has one; nil, with one info line per pool, when it has no closed row.
func (s *sysctlSource) readTxg(pool string, names []string) *txgRow {
	key := "txg\x00" + pool
	name := poolPrefix + pool + ".misc.txgs"
	if !slices.Contains(names, name) {
		s.ep.once(zapcore.InfoLevel, key, "ZFS pool has no txgs sysctl, so no txg metrics", zap.String("pool", pool))
		return nil
	}
	body, ok := s.string(name)
	if !ok {
		return nil
	}
	row, found := parseTxgs(body)
	if !found {
		s.ep.once(zapcore.InfoLevel, key, "ZFS pool txgs has no closed transaction group, so no txg metrics", zap.String("pool", pool))
		return nil
	}
	s.ep.clear(key)
	return &row
}

// readSpace returns the statfs sizes of the mount of the pool's root dataset: the zfs entry whose source name is the pool.
func (s *sysctlSource) readSpace(pool string) *spaceStats {
	if !s.mountsRead {
		s.mountsRead = true
		s.mounts = s.listMounts()
	}
	key := "space\x00" + pool
	for _, m := range s.mounts {
		if m.fsType == "zfs" && m.from == pool {
			s.ep.clear(key)
			return &spaceStats{
				used:      (m.blocks - min(m.free, m.blocks)) * m.blockSize,
				available: m.avail * m.blockSize,
			}
		}
	}
	if len(s.mounts) > 0 {
		s.ep.once(zapcore.DebugLevel, key, "ZFS pool root dataset is not mounted, so no space metrics", zap.String("pool", pool))
	}
	return nil
}

func (s *sysctlSource) listMounts() []mount {
	if s.fns.getfsstat == nil {
		return nil
	}
	mounts, err := s.fns.getfsstat()
	if notFound(err) {
		return nil
	}
	if err != nil {
		s.fail("getfsstat", err)
		return nil
	}
	return mounts
}

// uint64 and string read one sysctl; ok is false for a vanished name and for an error, which is recorded.
func (s *sysctlSource) uint64(name string) (uint64, bool) {
	v, err := s.fns.getUint64(name)
	return v, s.accept(name, err)
}

func (s *sysctlSource) string(name string) (string, bool) {
	v, err := s.fns.getString(name)
	return v, s.accept(name, err)
}

func (s *sysctlSource) accept(name string, err error) bool {
	switch {
	case err == nil:
		return true
	case notFound(err):
		s.logger.Debug("ZFS sysctl not found, skipping", zap.String("name", name))
	default:
		s.fail(name, err)
	}
	return false
}
