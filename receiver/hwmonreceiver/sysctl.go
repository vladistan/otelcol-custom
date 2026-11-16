// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package hwmonreceiver

import (
	"errors"
	"io/fs"
	"strconv"

	"go.uber.org/zap"
)

// sysctlReader reads one integer sysctl; the FreeBSD build supplies the real
// one and tests supply a fake. A name that does not exist is an error.
type sysctlReader interface {
	uint32(name string) (uint32, error)
}

// acpiLimits maps the ACPI trip points of a thermal zone to hw.limit_type.
var acpiLimits = []struct{ oid, limitType string }{
	{"_CRT", limitCritical},
	{"_HOT", limitHighDegraded},
	{"_PSV", limitThrottled},
}

// sysctlSource reads the FreeBSD temperature sysctls. The values are IK
// (tenths of a kelvin). FreeBSD has no fan or voltage sysctl.
type sysctlSource struct {
	logger *zap.Logger
	sys    sysctlReader
}

func newSysctlSource(logger *zap.Logger, sys sysctlReader) *sysctlSource {
	return &sysctlSource{logger: logger, sys: sys}
}

func (s *sysctlSource) read(opts readOptions) ([]reading, error) {
	var out []reading
	if opts.cpuCores {
		out = append(out, s.cpus()...)
	}
	out = append(out, s.zones(opts.limits)...)
	if len(out) == 0 {
		return nil, errNoSensors
	}
	return out, nil
}

func (s *sysctlSource) cpus() []reading {
	n, err := s.sys.uint32("hw.ncpu")
	if err != nil {
		s.logger.Debug("Cannot read hw.ncpu", zap.Error(err))
		return nil
	}
	var out []reading
	for i := range n {
		id := "cpu" + strconv.FormatUint(uint64(i), 10)
		v, err := s.sys.uint32("dev.cpu." + strconv.FormatUint(uint64(i), 10) + ".temperature")
		if err != nil || !plausible(v) {
			s.logger.Debug("No temperature for a CPU", zap.String("cpu", id))
			continue
		}
		out = append(out, reading{kind: kindTemperature, id: id, name: id, parent: "cpu", value: celsiusFromTenthsKelvin(v)})
	}
	return out
}

// maxZones bounds the zone walk; no machine has this many.
const maxZones = 256

// plausible tells a real IK value from the 0 and -1 (as unsigned) an absent
// trip point or a sensor without a reading gives.
func plausible(v uint32) bool { return v > 0 && v < 10000 }

// zones reads hw.acpi.thermal.tz0, tz1, ... until a zone does not exist. A
// zone that fails to read for another reason is skipped for this scrape.
func (s *sysctlSource) zones(withLimits bool) []reading {
	var out []reading
	for i := range maxZones {
		zone := "hw.acpi.thermal.tz" + strconv.Itoa(i)
		v, err := s.sys.uint32(zone + ".temperature")
		if errors.Is(err, fs.ErrNotExist) {
			return out
		}
		if err != nil {
			s.logger.Debug("Cannot read an ACPI thermal zone", zap.String("zone", zone), zap.Error(err))
			continue
		}
		if !plausible(v) {
			continue
		}
		id := "acpi_tz" + strconv.Itoa(i)
		r := reading{kind: kindTemperature, id: id, name: id, parent: "acpi", value: celsiusFromTenthsKelvin(v)}
		if withLimits {
			r.limits = s.zoneLimits(zone)
		}
		out = append(out, r)
	}
	return out
}

func (s *sysctlSource) zoneLimits(zone string) []limit {
	var out []limit
	for _, l := range acpiLimits {
		v, err := s.sys.uint32(zone + "." + l.oid)
		if err != nil || !plausible(v) {
			continue
		}
		out = append(out, limit{limitType: l.limitType, celsius: celsiusFromTenthsKelvin(v)})
	}
	return out
}
