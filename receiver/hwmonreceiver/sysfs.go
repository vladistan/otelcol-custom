// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwmonreceiver

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"go.uber.org/zap"
)

// coreChip is the chip whose "Core <N>" sensors cpu_cores switches.
const coreChip = "coretemp"

// coreLabel matches the per-core labels of the coretemp chip.
var coreLabel = regexp.MustCompile(`^Core \d+$`)

// The file stems of the sensors read; "intrusion0" and the like do not match.
var (
	tempStem = regexp.MustCompile(`^temp\d+$`)
	fanStem  = regexp.MustCompile(`^fan\d+$`)
	voltStem = regexp.MustCompile(`^in\d+$`)
)

// placeholderLimit is 0 K as the sysfs integer (-273.15 degrees Celsius times
// 1000); a limit at or below it is a placeholder.
const placeholderLimit = -273150

// limitFiles maps a temperature limit file suffix to hw.limit_type.
var limitFiles = []struct{ suffix, limitType string }{
	{"_max", limitMax},
	{"_crit", limitCritical},
	{"_min", limitLowDegraded},
	{"_lcrit", limitLowCritical},
}

// sysfsSource reads the Linux hwmon and thermal classes. It opens only the
// *_input, *_label and limit files of the sensors it reports, and name.
type sysfsSource struct {
	logger   *zap.Logger
	hwmon    string
	thermal  string
	readFile func(string) ([]byte, error)
	readDir  func(string) ([]os.DirEntry, error)
}

func newSysfsSource(logger *zap.Logger, hwmonPath string) *sysfsSource {
	return &sysfsSource{
		logger:   logger,
		hwmon:    hwmonPath,
		thermal:  filepath.Join(filepath.Dir(hwmonPath), "thermal"),
		readFile: os.ReadFile,
		readDir:  os.ReadDir,
	}
}

// chipDir is one hwmon<N> directory with the chip name read from it.
type chipDir struct {
	dir    string
	name   string
	device string // hardware address of the chip's device, empty when there is none
	key    string // id prefix, set once all chips are known
}

// read reads every chip, then the thermal zones that no hwmon chip already
// reports. A hwmon directory that exists but cannot be listed is an error, not
// "no sensors", unless the zones still give a reading.
func (s *sysfsSource) read(opts readOptions) ([]reading, error) {
	var out []reading
	chips, listErr := s.chips()
	for i, key := range chipKeys(chips) {
		chips[i].key = key
	}
	for _, c := range chips {
		out = append(out, s.readChip(c, opts)...)
	}
	if opts.thermalZones {
		out = append(out, s.readZones(hwmonNames(out))...)
	}
	if len(out) == 0 {
		if listErr != nil {
			return nil, fmt.Errorf("list the hwmon directory: %w", listErr)
		}
		return nil, errNoSensors
	}
	if listErr != nil {
		s.logger.Debug("Cannot list the hwmon directory", zap.String("path", s.hwmon), zap.Error(listErr))
	}
	return out, nil
}

// hwmonNames is the set of chip names that gave a reading.
func hwmonNames(readings []reading) map[string]bool {
	names := map[string]bool{}
	for _, r := range readings {
		names[r.parent] = true
	}
	return names
}

// chips lists the chips that have a name, in directory order. A missing
// directory is no chips; any other listing error is returned.
func (s *sysfsSource) chips() ([]chipDir, error) {
	entries, err := s.readDir(s.hwmon)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var chips []chipDir
	for _, e := range entries {
		dir := filepath.Join(s.hwmon, e.Name())
		name, err := s.text(filepath.Join(dir, "name"))
		if err != nil || name == "" {
			s.logger.Debug("Skipping a hwmon entry without a name", zap.String("dir", dir))
			continue
		}
		chips = append(chips, chipDir{dir: dir, name: name, device: deviceAddress(dir)})
	}
	return chips, nil
}

// deviceAddress names the hardware behind a hwmon chip with something that
// survives renumbering. An NVMe drive's hwmon device is the class device
// (nvme0, nvme1: numbers given at probe time) whose own device link reaches the
// PCI function, so that function's address is used. Any other device is named by
// the directory its link resolves to (0000:00:18.3, coretemp.0). A chip with no
// device link gets "".
func deviceAddress(chipDir string) string {
	link := filepath.Join(chipDir, "device")
	if info, err := os.Lstat(filepath.Join(link, "device")); err == nil && info.Mode()&os.ModeSymlink != 0 {
		if real, err := filepath.EvalSymlinks(filepath.Join(link, "device")); err == nil {
			return filepath.Base(real)
		}
	}
	if real, err := filepath.EvalSymlinks(link); err == nil {
		return filepath.Base(real)
	}
	if target, err := os.Readlink(link); err == nil {
		return filepath.Base(target)
	}
	return ""
}

// chipKeys returns the id prefix of each chip. A name shared by several chips
// gets the device address as a suffix, so two NVMe drives keep two series
// whatever order the kernel numbers them in. A chip with no device falls back
// to its hwmon<N> directory, which is not stable across boots.
func chipKeys(chips []chipDir) []string {
	count := map[string]int{}
	for _, c := range chips {
		count[c.name]++
	}
	keys := make([]string, len(chips))
	used := map[string]int{}
	for i, c := range chips {
		key := c.name
		if count[c.name] > 1 {
			key = c.name + "@" + c.device
			if c.device == "" {
				key = c.name + "@" + filepath.Base(c.dir)
			}
		}
		if used[key]++; used[key] > 1 {
			key += "." + strconv.Itoa(used[key])
		}
		keys[i] = key
	}
	return keys
}

func (s *sysfsSource) readChip(c chipDir, opts readOptions) []reading {
	var out []reading
	files, err := s.readDir(c.dir)
	if err != nil {
		s.logger.Debug("Cannot list a hwmon chip", zap.String("dir", c.dir), zap.Error(err))
		return nil
	}
	for _, f := range files {
		stem, ok := strings.CutSuffix(f.Name(), "_input")
		if !ok {
			continue
		}
		var r reading
		switch {
		case tempStem.MatchString(stem):
			r = reading{kind: kindTemperature}
		case fanStem.MatchString(stem) && opts.fans:
			r = reading{kind: kindFan}
		case voltStem.MatchString(stem) && opts.voltages:
			r = reading{kind: kindVoltage}
		default:
			continue
		}
		raw, ok := s.number(filepath.Join(c.dir, f.Name()))
		if !ok {
			continue
		}
		r.id = c.key + "/" + stem
		r.parent = c.name
		r.name = stem
		if label, err := s.text(filepath.Join(c.dir, stem+"_label")); err == nil && label != "" {
			r.name = label
		}
		if r.kind == kindTemperature && !opts.cpuCores && c.name == coreChip && coreLabel.MatchString(r.name) {
			continue
		}
		r.value = scale(r.kind, raw)
		if r.kind == kindTemperature && opts.limits {
			r.limits = s.limits(c.dir, stem)
		}
		out = append(out, r)
	}
	return out
}

// scale converts the sysfs integer: millidegrees, millivolts, or rpm as is.
func scale(k kind, raw int64) float64 {
	if k == kindFan {
		return float64(raw)
	}
	return float64(raw) / 1000
}

func (s *sysfsSource) limits(dir, stem string) []limit {
	var out []limit
	for _, lf := range limitFiles {
		raw, ok := s.number(filepath.Join(dir, stem+lf.suffix))
		if !ok || raw <= placeholderLimit {
			// NVMe drives report -273150 for a limit they do not have.
			continue
		}
		out = append(out, limit{limitType: lf.limitType, celsius: float64(raw) / 1000})
	}
	return out
}

// readZones reads the thermal zones. A zone whose type is also the name of a
// hwmon chip that gave a reading (CONFIG_THERMAL_HWMON registers one chip per
// zone, named after the type with hyphens as underscores) is skipped, so the
// sensor is reported once.
func (s *sysfsSource) readZones(twins map[string]bool) []reading {
	zones, err := filepath.Glob(filepath.Join(s.thermal, "thermal_zone*"))
	if err != nil {
		return nil
	}
	var out []reading
	for _, dir := range zones {
		raw, ok := s.number(filepath.Join(dir, "temp"))
		if !ok {
			continue
		}
		typ, err := s.text(filepath.Join(dir, "type"))
		if err != nil || typ == "" {
			typ = "unknown"
		}
		if twins[strings.ReplaceAll(typ, "-", "_")] {
			continue
		}
		zone := filepath.Base(dir)
		out = append(out, reading{
			kind:   kindTemperature,
			id:     "thermal/" + zone,
			name:   zone,
			parent: typ,
			value:  float64(raw) / 1000,
		})
	}
	return out
}

// text returns a trimmed file; a missing file is an error without a log.
func (s *sysfsSource) text(p string) (string, error) {
	b, err := s.readFile(p)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// number reads an integer sensor file. A file that is absent is skipped
// silently; one that fails (EIO, ENODATA: a sleeping or missing device) or
// holds no number is skipped with a debug line.
func (s *sysfsSource) number(p string) (int64, bool) {
	txt, err := s.text(p)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.logger.Debug("Skipping a sensor file that cannot be read", zap.String("file", p), zap.Error(err))
		}
		return 0, false
	}
	v, err := strconv.ParseInt(txt, 10, 64)
	if err != nil {
		s.logger.Debug("Skipping a sensor file that is not a number", zap.String("file", p), zap.String("content", txt))
		return 0, false
	}
	return v, true
}
