// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package inventoryreceiver

import (
	"context"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var memTestNow = time.Unix(1700000000, 0)

func meminfoWith(total string) string {
	return "MemFree:         1234567 kB\nBuffers:           10 kB\nMemTotal:" + total + "\nMemAvailable:    999 kB\n"
}

func TestMemoryTotalLinuxConvertsKiBToBytes(t *testing.T) {
	const fourTiBInKiB = int64(4) << 30
	tests := map[string]struct {
		file string
		want int64
	}{
		"plain":            {"MemTotal:       16384000 kB\nMemFree: 1 kB\n", 16384000 * 1024},
		"lines before":     {meminfoWith("      8000 kB"), 8000 * 1024},
		"extra whitespace": {"MemTotal:\t\t   42   kB   \n", 42 * 1024},
		"four TiB":         {"MemTotal: " + strconv.FormatInt(fourTiBInKiB, 10) + " kB\n", int64(4) << 40},
		"no trailing eol":  {"MemTotal: 100 kB", 100 * 1024},
		"crlf":             {"MemTotal: 100 kB\r\n", 100 * 1024},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			f := physicalHost().set(meminfoPath, tc.file)

			inv := readHost(t, f)

			got := oneFact(t, inv, metricMemoryTotal)
			assert.Equal(t, tc.want, got.attrs["memory.total"])
			assert.NotContains(t, got.attrs, "hw.id")
			assert.True(t, inv.status["memory_total"])
		})
	}
}

func TestMemoryTotalLinuxBadInputGivesNoSeriesAndStatusZero(t *testing.T) {
	overflow := strconv.FormatInt(math.MaxInt64/1024+1, 10)
	tests := map[string]string{
		"no MemTotal line":  "MemFree: 5 kB\nBuffers: 1 kB\n",
		"empty file":        "",
		"non numeric":       "MemTotal: lots kB\n",
		"negative":          "MemTotal: -5 kB\n",
		"no number":         "MemTotal: kB\n",
		"no unit":           "MemTotal: 5\n",
		"wrong unit":        "MemTotal: 5 MB\n",
		"zero":              "MemTotal: 0 kB\n",
		"overflows int64":   "MemTotal: " + overflow + " kB\n",
		"overflows uint64":  "MemTotal: 99999999999999999999999 kB\n",
		"name only prefix":  "MemTotalX: 5 kB\n",
		"indented name":     "  MemTotal: 5 kB\n",
		"binary garbage":    "\x00\xff\xfe MemTotal\n",
		"colon missing":     "MemTotal 5 kB\n",
		"name with no data": "MemTotal:\n",
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			f := physicalHost().set(meminfoPath, content)

			var inv inventory
			require.NotPanics(t, func() { inv = readHost(t, f) })

			assert.Empty(t, factsOf(inv, metricMemoryTotal))
			status, known := inv.status["memory_total"]
			assert.True(t, known)
			assert.False(t, status)
			assert.Len(t, factsOf(inv, metricMemory), 2, "the module series are not affected")
		})
	}
}

func TestMemoryTotalLinuxLargestValueThatFitsIsKept(t *testing.T) {
	f := physicalHost().set(meminfoPath, "MemTotal: "+strconv.FormatInt(math.MaxInt64/1024, 10)+" kB\n")

	got := oneFact(t, readHost(t, f), metricMemoryTotal)

	assert.Equal(t, int64(math.MaxInt64/1024)*1024, got.attrs["memory.total"])
}

func TestMemoryTotalLinuxMissingOrDeniedFileGivesStatusZero(t *testing.T) {
	for name, f := range map[string]*testFS{
		"missing": physicalHost().remove(meminfoPath),
		"denied":  physicalHost().denyPath(meminfoPath),
	} {
		t.Run(name, func(t *testing.T) {
			inv := readHost(t, f)

			assert.Empty(t, factsOf(inv, metricMemoryTotal))
			assert.False(t, inv.status["memory_total"])
			assert.Equal(t, hostPhysical, inv.host)
		})
	}
}

func TestMemoryTotalLinuxIsIndependentOfDMI(t *testing.T) {
	f := physicalHost().denyPath(dmiTable)
	for n := range f.files {
		if strings.HasPrefix(n, dmiDir) {
			f.denyPath(n)
		}
	}

	inv := readHost(t, f)

	assert.False(t, inv.status["memory"])
	assert.True(t, inv.dmiUnreadable)
	assert.Equal(t, int64(16384000)*1024, oneFact(t, inv, metricMemoryTotal).attrs["memory.total"])
	assert.True(t, inv.status["memory_total"])
}

func TestMemoryTotalLinuxNeedsNoFirmwareFilesAtAll(t *testing.T) {
	f := newTestFS().set(cpuinfoPath, cpuinfoOneSocket).set(meminfoPath, "MemTotal: 2048 kB\n")

	inv := readHost(t, f)

	assert.Equal(t, int64(2048*1024), oneFact(t, inv, metricMemoryTotal).attrs["memory.total"])
}

func TestMemoryTotalIsOneSeriesPerHostPerPass(t *testing.T) {
	src := &linuxSource{fsys: physicalHost(), maxDrives: 64}

	for i := 0; i < 3; i++ {
		inv := src.read(context.Background())
		assert.Len(t, factsOf(inv, metricMemoryTotal), 1)

		md := buildMetrics(inv, memTestNow)
		count := 0
		sms := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
		for j := 0; j < sms.Len(); j++ {
			if sms.At(j).Name() == "inventory.memory.info" {
				m := sms.At(j)
				assert.Equal(t, "1", m.Unit())
				require.Equal(t, 1, m.Gauge().DataPoints().Len())
				dp := m.Gauge().DataPoints().At(0)
				assert.Equal(t, int64(1), dp.IntValue())
				assert.Equal(t, 1, dp.Attributes().Len())
				v, ok := dp.Attributes().Get("memory.total")
				require.True(t, ok)
				assert.Equal(t, int64(16384000)*1024, v.Int())
				count++
			}
		}
		assert.Equal(t, 1, count)
	}
}

func TestMemoryTotalStatusSeriesIsNamedMemoryTotal(t *testing.T) {
	md := buildMetrics(readHost(t, physicalHost()), memTestNow)

	found := false
	sms := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for j := 0; j < sms.Len(); j++ {
		if sms.At(j).Name() != metricStatus {
			continue
		}
		dps := sms.At(j).Gauge().DataPoints()
		for k := 0; k < dps.Len(); k++ {
			if v, _ := dps.At(k).Attributes().Get("inventory.source"); v.Str() == "memory_total" {
				found = true
				assert.Equal(t, int64(1), dps.At(k).IntValue())
			}
		}
	}
	assert.True(t, found)
}

func TestMemoryTotalVirtualHostAndContainerEmitNothing(t *testing.T) {
	vm := physicalHost().set(cpuinfoPath, "processor\t: 0\nflags\t\t: fpu hypervisor\n")
	ct := physicalHost().set(".dockerenv", "")
	for name, f := range map[string]*testFS{"vm": vm, "container": ct} {
		t.Run(name, func(t *testing.T) {
			inv := readHost(t, f)

			assert.NotEqual(t, hostPhysical, inv.host)
			assert.Empty(t, inv.facts)
			assert.NotContains(t, inv.status, "memory_total")
			assert.NotContains(t, f.openedNames(), meminfoPath, "the file is not read on such a host")
		})
	}
}

func TestMemoryTotalLinuxReadsNoPerProcessPath(t *testing.T) {
	f := physicalHost()

	readHost(t, f)

	perProcess := regexp.MustCompile(`^proc/[0-9]+(/|$)`)
	for _, name := range f.openedNames() {
		if perProcess.MatchString(name) {
			assert.Equal(t, "proc/1/cgroup", name, "only the container check reads a process file")
		}
		assert.NotContains(t, name, "proc/self/status")
		assert.NotContains(t, name, "proc/self/smaps")
	}
	assert.Contains(t, f.openedNames(), meminfoPath)
}

func TestMemoryTotalWindowsGivesOneSeries(t *testing.T) {
	w := physicalWin()
	w.totalMem = 34359738368

	inv := readWin(w)

	got := oneFact(t, inv, metricMemoryTotal)
	assert.Equal(t, int64(34359738368), got.attrs["memory.total"])
	assert.NotContains(t, got.attrs, "hw.id")
	assert.True(t, inv.status["memory_total"])
	assert.Len(t, factsOf(inv, metricMemory), 1, "the module series stay")
}

func TestMemoryTotalWindowsErrorOrZeroGivesNoSeriesAndStatusZero(t *testing.T) {
	tests := map[string]*fakeWin{
		"error":    func() *fakeWin { w := physicalWin(); w.totalMemErr = errors.New("failed"); return w }(),
		"zero":     func() *fakeWin { w := physicalWin(); w.totalMem = 0; return w }(),
		"overflow": func() *fakeWin { w := physicalWin(); w.totalMem = math.MaxUint64; return w }(),
	}
	for name, w := range tests {
		t.Run(name, func(t *testing.T) {
			inv := readWin(w)

			assert.Empty(t, factsOf(inv, metricMemoryTotal))
			status, known := inv.status["memory_total"]
			assert.True(t, known)
			assert.False(t, status)
			assert.Len(t, factsOf(inv, metricCPU), 1, "other sources are not affected")
		})
	}
}

func TestMemoryTotalWindowsIsIndependentOfTheFirmwareTable(t *testing.T) {
	w := physicalWin()
	w.firmware, w.firmErr = nil, errors.New("access denied")
	w.strs[`HARDWARE\DESCRIPTION\System\BIOS|SystemManufacturer`] = "HP"

	inv := readWin(w)

	assert.False(t, inv.status["memory"])
	assert.NotEmpty(t, factsOf(inv, metricMemoryTotal))
}

func TestMemoryTotalWindowsVirtualMachineGivesNothing(t *testing.T) {
	w := physicalWin()
	w.strs[`HARDWARE\DESCRIPTION\System\BIOS|SystemManufacturer`] = "Microsoft Corporation"
	w.strs[`HARDWARE\DESCRIPTION\System\BIOS|SystemProductName`] = "Virtual Machine"
	w.firmware, w.firmErr = nil, errors.New("denied")

	inv := readWin(w)

	assert.Equal(t, hostVirtual, inv.host)
	assert.Empty(t, inv.facts)
}
