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
	// maxOIDLen is CTL_MAXNAME, the longest OID the kernel returns.
	maxOIDLen = 24
)

// Next-OID meta sysctls under CTL_SYSCTL (0). CTL_SYSCTL_NEXT skips nodes flagged CTLFLAG_SKIP, and the
// kernel flags every raw kstat that way, among them kstat.zfs.<pool>.misc.state and the txgs node, so a
// walk with it lists no pool. CTL_SYSCTL_NEXTNOSKIP lists them.
var (
	ctlNext       = []int32{0, 2}
	ctlNextNoSkip = []int32{0, 7}
)

// nextMeta returns the no-skip next-OID call when the kernel has it, and the skipping one otherwise.
func nextMeta(lookup func(name string) ([]int32, error)) []int32 {
	if oid, err := lookup("sysctl.nextnoskip"); err == nil && slices.Equal(oid, ctlNextNoSkip) {
		return ctlNextNoSkip
	}
	return ctlNext
}

// rawFn is the system call that reads or writes a sysctl by OID; it returns the bytes written to old.
type rawFn func(mib []int32, old, newp []byte) (int, error)

// nextOIDVia asks the kernel for the OID that follows oid, with the given next-OID meta call.
func nextOIDVia(raw rawFn, meta, oid []int32) ([]int32, error) {
	mib := append(append([]int32{}, meta...), oid...)
	buf := make([]byte, maxOIDLen*4)
	n, err := raw(mib, buf, nil)
	if err != nil {
		return nil, err
	}
	return decodeOID(buf[:n]), nil
}

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
	// oidType names the sysctl type of an OID for a warning; nil means the type is not known.
	oidType  func(name string) (string, error)
	bootTime func() (time.Time, error)
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
	logger    *zap.Logger
	cfg       *Config
	fns       sysctlFns
	ep        *episodes
	isAbsent  bool
	hasFailed bool

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
	s.hasFailed = true
	s.ep.warn(sysctlKey, "ZFS sysctl read failed", zap.String("name", name), zap.Error(err))
}

// read never returns an error: a failed sysctl is a warning and the metrics that could be read are kept.
func (s *sysctlSource) read() (snapshot, error) {
	s.hasFailed = false
	s.mounts, s.mountsRead = nil, false
	snap := s.readTree()
	if !s.hasFailed {
		s.ep.clear(sysctlKey)
	}
	return snap, nil
}

func (s *sysctlSource) readTree() snapshot {
	root, err := s.fns.lookupOID(zfsRoot)
	if notFound(err) {
		if !s.isAbsent {
			s.isAbsent = true
			s.logger.Info("ZFS sysctl tree kstat.zfs not found, no ZFS metrics until it exists")
		}
		return snapshot{}
	}
	if err != nil {
		s.fail(zfsRoot, err)
		return snapshot{}
	}
	s.isAbsent = false

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
		v, ok := s.readUint64(arcPrefix + m.field)
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
		state, ok := s.readString(n)
		if !ok {
			continue
		}
		p := poolStats{name: pool, state: s.checkedState(pool, n, state)}
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

// checkedState returns the trimmed state, or "" with one warning per episode that names the OID
// and its sysctl type when the value is not a state name; the pool keeps its other series.
func (s *sysctlSource) checkedState(pool, oid, raw string) string {
	key := "state\x00" + pool
	state := strings.Trim(raw, " \t\r\n\x00")
	if validState(state) {
		s.ep.clear(key)
		return state
	}
	s.ep.warn(key, "ZFS pool state sysctl holds a value the receiver cannot read",
		zap.String("oid", oid), zap.String("type", s.oidTypeName(oid)))
	return ""
}

func (s *sysctlSource) oidTypeName(oid string) string {
	if s.fns.oidType == nil {
		return "unknown"
	}
	name, err := s.fns.oidType(oid)
	if err != nil || name == "" {
		return "unknown"
	}
	return name
}

// maxStateLen bounds a pool state name; the longest real one is SUSPENDED.
const maxStateLen = 64

// validState reports whether the text is a state name: printable letters, digits, dash and underscore.
func validState(state string) bool {
	if state == "" || len(state) > maxStateLen {
		return false
	}
	for _, c := range state {
		letter := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
		if !letter && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

const (
	rawStringStart = 4096
	rawStringMax   = 1 << 20
)

// readRawString calls read with a buffer that doubles while the kernel answers ENOMEM, up to 1 MiB,
// and returns the text without trailing NUL bytes. Raw kstat nodes give no size to a probing call.
func readRawString(read func(old []byte) (int, error)) (string, error) {
	for size := rawStringStart; ; size *= 2 {
		buf := make([]byte, size)
		n, err := read(buf)
		if errors.Is(err, syscall.ENOMEM) && size < rawStringMax {
			continue
		}
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(buf[:n]), "\x00"), nil
	}
}

// kindName names the sysctl type held in the low four bits of an OID kind.
func kindName(kind uint32) string {
	names := map[uint32]string{
		1: "node", 2: "int", 3: "string", 4: "int64", 5: "opaque", 6: "uint", 7: "long", 8: "ulong",
		9: "uint64", 0xa: "uint8", 0xb: "uint16", 0xc: "int8", 0xd: "int16", 0xe: "int32", 0xf: "uint32",
	}
	if name, ok := names[kind&0xf]; ok {
		return name
	}
	return "unknown"
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
		name, ok := s.readString(base + datasetNameField)
		if !ok || name == "" || !s.cfg.datasetSelected(name) {
			continue
		}
		fields := map[string]uint64{}
		for _, f := range objsetFields {
			if !slices.Contains(found[obj], f) {
				continue
			}
			if v, ok := s.readUint64(base + f); ok {
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
	name := ""
	for _, suffix := range []string{".txgs", ".misc.txgs"} {
		if candidate := poolPrefix + pool + suffix; slices.Contains(names, candidate) {
			name = candidate
			break
		}
	}
	if name == "" {
		s.ep.once(zapcore.InfoLevel, key, "ZFS pool has no txgs sysctl, so no txg metrics", zap.String("pool", pool))
		return nil
	}
	body, ok := s.readString(name)
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

// readUint64 and readString read one sysctl; ok is false for a vanished name and for an error, which is recorded.
func (s *sysctlSource) readUint64(name string) (uint64, bool) {
	v, err := s.fns.getUint64(name)
	return v, s.accept(name, err)
}

func (s *sysctlSource) readString(name string) (string, bool) {
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
