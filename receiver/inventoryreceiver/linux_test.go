// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package inventoryreceiver

// cspell:ignore GXNX QGXA

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	dmiDir      = "sys/class/dmi/id/"
	dmiTable    = "sys/firmware/dmi/tables/DMI"
	cpuinfoPath = "proc/cpuinfo"
)

const cpuinfoOneSocket = `processor	: 0
vendor_id	: GenuineIntel
model name	: Intel(R) Xeon(R) CPU E5-2620 v4 @ 2.10GHz
cpu MHz		: 1199.987
physical id	: 0
core id		: 0
flags		: fpu vme sse avx

processor	: 1
vendor_id	: GenuineIntel
model name	: Intel(R) Xeon(R) CPU E5-2620 v4 @ 2.10GHz
cpu MHz		: 2100.000
physical id	: 0
core id		: 1
flags		: fpu vme sse avx

processor	: 2
vendor_id	: GenuineIntel
model name	: Intel(R) Xeon(R) CPU E5-2620 v4 @ 2.10GHz
physical id	: 0
core id		: 0
flags		: fpu vme sse avx

processor	: 3
vendor_id	: GenuineIntel
model name	: Intel(R) Xeon(R) CPU E5-2620 v4 @ 2.10GHz
physical id	: 0
core id		: 1
flags		: fpu vme sse avx
`

// physicalHost is a fixture of a bare-metal server with every source present.
func physicalHost() *testFS {
	f := newTestFS()
	f.set(cpuinfoPath, cpuinfoOneSocket)
	f.set("sys/devices/system/cpu/cpu0/cpufreq/cpuinfo_max_freq", "3000000\n")
	f.set("sys/devices/system/cpu/cpu0/cpufreq/base_frequency", "2100000\n")
	for k, v := range map[string]string{
		"sys_vendor": "Dell Inc.", "product_name": "PowerEdge R730", "product_version": "1.0", "product_serial": "SVC-TEST-01",
		"product_family": "PowerEdge", "board_vendor": "Dell Inc.", "board_name": "0599V5", "board_version": "A05",
		"board_serial": "BRD-TEST-02", "board_asset_tag": "ASSET-3", "bios_vendor": "Dell Inc.", "bios_version": "2.13.0",
		"bios_date": "03/13/2022", "chassis_vendor": "Dell Inc.", "chassis_type": "23", "chassis_serial": "CHS-TEST-04",
		"chassis_asset_tag": "ASSET-5",
	} {
		f.set(dmiDir+k, v+"\n")
	}
	empty := dimmStructure(dimmSpec{size: 0, locator: 1}, "DIMM_A2")
	f.bytes(dmiTable, joinTable(goodDimm(), empty,
		dimmStructure(dimmSpec{size: 8192, memType: 0x22, form: 0x09, speed: 4800, locator: 1, mfr: 2, part: 3}, "DIMM_B1", "Hynix", "PART-0002"),
		endMarker()))
	f.set("sys/block/sda/size", "7814037168\n")
	f.set("sys/block/sda/queue/rotational", "1\n")
	f.set("sys/block/sda/device/model", "ST4000NM0035-1V4\x20\x20\n")
	f.set("sys/block/sda/device/vendor", "ATA     \n")
	f.set("sys/block/sda/device/rev", "TN03\n")
	f.bytes("sys/block/sda/device/vpd_pg80", append([]byte{0x00, 0x80, 0x00, 0x08}, []byte("ZC10ABCD")...))
	f.set("sys/block/sda/device/vpd_pg89", "x")
	f.set("sys/block/sda/device/wwid", "naa.5000c500a1b2c3d4\n")
	f.set("sys/block/nvme0n1/size", "1953525168\n")
	f.set("sys/block/nvme0n1/queue/rotational", "0\n")
	f.set("sys/block/nvme0n1/device/model", "Samsung SSD 980 PRO 1TB\n")
	f.set("sys/block/nvme0n1/device/serial", "S5GXNX0T123456A\n")
	f.set("sys/block/nvme0n1/device/firmware_rev", "5B2QGXA7\n")
	for _, skipped := range []string{"loop0", "ram0", "dm-0", "zram0", "sr0", "md0", "nbd0", "fd0"} {
		f.set("sys/block/"+skipped+"/size", "2048\n")
		f.set("sys/block/"+skipped+"/device/model", "Skipped\n")
	}
	f.set("sys/bus/pci/devices/0000:01:00.0/class", "0x030000\n")
	f.set("sys/bus/pci/devices/0000:01:00.0/vendor", "0x10de\n")
	f.set("sys/bus/pci/devices/0000:01:00.0/device", "0x2204\n")
	f.link("sys/bus/pci/devices/0000:01:00.0/driver", "../../../bus/pci/drivers/nvidia")
	f.set("sys/bus/pci/devices/0000:00:1f.2/class", "0x010601\n")
	f.set("sys/bus/pci/devices/0000:00:1f.2/vendor", "0x8086\n")
	f.set("sys/bus/pci/devices/0000:00:1f.2/device", "0xa102\n")
	f.set("proc/1/cgroup", "0::/init.scope\n")
	f.set(meminfoPath, "MemTotal:       16384000 kB\nMemFree:         1000 kB\n")
	f.bytes("sys/firmware/dmi/tables/smbios_entry_point", append([]byte("_SM3_"), 0, 0x18, 3, 3, 0, 1))
	return f
}

func readHost(t *testing.T, f *testFS) inventory {
	t.Helper()
	return (&linuxSource{fsys: f, maxDrives: 64}).read(context.Background())
}

func factsOf(inv inventory, metric string) []fact {
	var out []fact
	for _, f := range inv.facts {
		if f.metric == metric {
			out = append(out, f)
		}
	}
	return out
}

func oneFact(t *testing.T, inv inventory, metric string) fact {
	t.Helper()
	got := factsOf(inv, metric)
	require.Len(t, got, 1, metric)
	return got[0]
}

func TestLinuxPhysicalHostGivesEveryFact(t *testing.T) {
	inv := readHost(t, physicalHost())

	assert.Equal(t, hostPhysical, inv.host)

	cpu := oneFact(t, inv, metricCPU)
	assert.Equal(t, "Intel(R) Xeon(R) CPU E5-2620 v4 @ 2.10GHz", cpu.attrs["hw.model"])
	assert.Equal(t, "GenuineIntel", cpu.attrs["hw.vendor"])
	assert.Equal(t, int64(1), cpu.attrs["cpu.sockets"])
	assert.Equal(t, int64(2), cpu.attrs["cpu.cores"])
	assert.Equal(t, int64(4), cpu.attrs["cpu.threads"])
	assert.Equal(t, int64(2_100_000_000), cpu.attrs["cpu.frequency.nominal"])
	assert.Equal(t, int64(3_000_000_000), cpu.attrs["cpu.frequency.max"])
	assert.NotContains(t, cpu.attrs, "cpu.frequency.current", "no time-varying value")

	mem := factsOf(inv, metricMemory)
	require.Len(t, mem, 2, "the empty slot gives no series")
	assert.Equal(t, "DIMM_A1", mem[0].attrs["hw.id"])
	assert.Equal(t, "M393A2K43DB3", mem[0].attrs["hw.model"])
	assert.Equal(t, "SN-TEST-0001", mem[0].attrs["hw.serial_number"])
	assert.Equal(t, int64(16384)<<20, mem[0].attrs["memory.size"])
	assert.Equal(t, "DDR4", mem[0].attrs["memory.type"])
	assert.Equal(t, "DDR5", mem[1].attrs["memory.type"])

	board := oneFact(t, inv, metricBoard)
	assert.Equal(t, "0599V5", board.attrs["hw.model"])
	assert.Equal(t, "BRD-TEST-02", board.attrs["hw.serial_number"])
	assert.Equal(t, "ASSET-3", board.attrs["hw.asset_tag"])
	bios := oneFact(t, inv, metricBIOS)
	assert.Equal(t, "2.13.0", bios.attrs["hw.firmware_version"])
	assert.Equal(t, "03/13/2022", bios.attrs["bios.date"])
	sys := oneFact(t, inv, metricSystem)
	assert.Equal(t, "PowerEdge R730", sys.attrs["hw.model"])
	assert.Equal(t, "SVC-TEST-01", sys.attrs["hw.serial_number"])
	assert.Equal(t, "Rack Mount Chassis", sys.attrs["chassis.type"])
	assert.Equal(t, "CHS-TEST-04", sys.attrs["chassis.serial_number"])

	drives := factsOf(inv, metricDrive)
	require.Len(t, drives, 2, "loop, ram, dm, zram, sr, md, nbd and fd devices are skipped")
	byID := map[any]fact{}
	for _, d := range drives {
		byID[d.attrs["hw.serial_number"]] = d
	}
	hdd := byID["ZC10ABCD"]
	assert.Equal(t, "ST4000NM0035-1V4", hdd.attrs["hw.model"])
	assert.Equal(t, "TN03", hdd.attrs["hw.firmware_version"])
	assert.Equal(t, int64(7814037168)*512, hdd.attrs["drive.size"])
	assert.Equal(t, "HDD", hdd.attrs["drive.type"])
	assert.Equal(t, "sata", hdd.attrs["drive.bus"])
	assert.Equal(t, "naa.5000c500a1b2c3d4", hdd.attrs["hw.wwid"])
	assert.NotContains(t, hdd.attrs, "hw.vendor", "the ATA vendor field is not a vendor")
	nvme := byID["S5GXNX0T123456A"]
	assert.Equal(t, "Samsung SSD 980 PRO 1TB", nvme.attrs["hw.model"])
	assert.Equal(t, "5B2QGXA7", nvme.attrs["hw.firmware_version"])
	assert.Equal(t, "SSD", nvme.attrs["drive.type"])
	assert.Equal(t, "nvme", nvme.attrs["drive.bus"])

	gpu := oneFact(t, inv, metricGPU)
	assert.Equal(t, "0000:01:00.0", gpu.attrs["hw.id"])
	assert.Equal(t, "10de", gpu.attrs["gpu.vendor_id"])
	assert.Equal(t, "2204", gpu.attrs["gpu.device_id"])
	assert.Equal(t, "nvidia", gpu.attrs["hw.driver"])
	assert.NotContains(t, gpu.attrs, "hw.model", "no local name table: ids only")

	for _, src := range []string{"cpu", "memory", "board", "bios", "system", "drive", "gpu"} {
		assert.True(t, inv.status[src], src)
	}
	assert.False(t, inv.dmiUnreadable)
}

func TestLinuxEverySeriesHasOnlyStringAndIntegerAttributes(t *testing.T) {
	inv := readHost(t, physicalHost())

	for _, f := range inv.facts {
		for k, v := range f.attrs {
			switch v.(type) {
			case string, int64:
			default:
				t.Errorf("%s %s has type %T", f.metric, k, v)
			}
		}
	}
}

func TestLinuxHostWithNoGPUGivesNoGPUSeriesAndStatusOne(t *testing.T) {
	f := physicalHost()
	f.remove("sys/bus/pci/devices/0000:01:00.0/class")

	inv := readHost(t, f)

	assert.Empty(t, factsOf(inv, metricGPU))
	assert.True(t, inv.status["gpu"])
}

func TestLinuxPartialFactsGiveWhatIsThere(t *testing.T) {
	f := newTestFS()
	f.set(cpuinfoPath, cpuinfoOneSocket)

	inv := readHost(t, f)

	assert.Len(t, factsOf(inv, metricCPU), 1)
	assert.Empty(t, factsOf(inv, metricMemory))
	assert.Empty(t, factsOf(inv, metricBoard))
	assert.True(t, inv.status["cpu"])
	for _, src := range []string{"memory", "board", "bios", "system", "drive", "gpu"} {
		assert.False(t, inv.status[src], src)
	}
	assert.True(t, inv.dmiUnreadable)
}

func TestLinuxPlaceholderValuesAreAbsent(t *testing.T) {
	f := physicalHost()
	for _, k := range []string{"board_serial", "board_asset_tag", "product_serial", "chassis_serial"} {
		f.set(dmiDir+k, "To be filled by O.E.M.\n")
	}
	f.set(dmiDir+"board_name", "Default string\n")
	f.set(dmiDir+"board_version", "Not Specified\n")
	f.set(dmiDir+"product_version", "System Version\n")

	inv := readHost(t, f)

	board := oneFact(t, inv, metricBoard)
	assert.NotContains(t, board.attrs, "hw.serial_number")
	assert.NotContains(t, board.attrs, "hw.asset_tag")
	assert.NotContains(t, board.attrs, "hw.model")
	assert.NotContains(t, board.attrs, "hw.version")
	assert.Equal(t, "Dell Inc.", board.attrs["hw.vendor"])
	sys := oneFact(t, inv, metricSystem)
	assert.NotContains(t, sys.attrs, "hw.serial_number")
	assert.NotContains(t, sys.attrs, "hw.version")
	assert.NotContains(t, sys.attrs, "chassis.serial_number")
	for _, f := range inv.facts {
		for _, v := range f.attrs {
			if s, ok := v.(string); ok {
				assert.NotContains(t, []string{"", "unknown", "Unknown", "To be filled by O.E.M."}, s)
			}
		}
	}
}

func TestLinuxEveryFileFailingWithEACCESOrENOENTDegradesWithoutPanic(t *testing.T) {
	base := physicalHost()
	var names []string
	for n := range base.files {
		names = append(names, n)
	}
	for _, name := range names {
		for _, mode := range []string{"denied", "missing"} {
			t.Run(mode+" "+name, func(t *testing.T) {
				f := physicalHost()
				if mode == "denied" {
					f.denyPath(name)
				} else {
					f.remove(name)
				}

				var inv inventory
				require.NotPanics(t, func() { inv = readHost(t, f) })

				assert.Equal(t, hostPhysical, inv.host)
				for _, fa := range inv.facts {
					for k, v := range fa.attrs {
						if s, ok := v.(string); ok {
							assert.NotEmpty(t, s, "%s %s", fa.metric, k)
						}
					}
				}
			})
		}
	}
}

func TestLinuxUnreadableSerialsLeaveTheOtherFields(t *testing.T) {
	f := physicalHost()
	for _, k := range []string{"product_serial", "board_serial", "chassis_serial"} {
		f.denyPath(dmiDir + k)
	}
	f.denyPath(dmiTable)

	inv := readHost(t, f)

	assert.NotContains(t, oneFact(t, inv, metricSystem).attrs, "hw.serial_number")
	assert.Equal(t, "PowerEdge R730", oneFact(t, inv, metricSystem).attrs["hw.model"])
	assert.Empty(t, factsOf(inv, metricMemory))
	assert.False(t, inv.status["memory"])
	assert.True(t, inv.status["board"])
	assert.True(t, inv.status["cpu"])
	assert.True(t, inv.dmiUnreadable)
}

func TestLinuxUnreadableDMIEverywhereGivesStatusZeroForTheDMISourcesAndOneForCPU(t *testing.T) {
	f := physicalHost()
	for n := range f.files {
		if strings.HasPrefix(n, "sys/class/dmi") {
			f.remove(n)
		}
	}
	f.remove(dmiTable)

	inv := readHost(t, f)

	for _, src := range []string{"memory", "board", "bios", "system"} {
		assert.False(t, inv.status[src], src)
	}
	assert.True(t, inv.status["cpu"])
	assert.True(t, inv.dmiUnreadable)
	assert.Len(t, factsOf(inv, metricCPU), 1)
}

func TestLinuxMalformedTableGivesNoModuleAndNoPanic(t *testing.T) {
	f := physicalHost()
	f.bytes(dmiTable, []byte{17, 0xFF, 0, 0, 1, 2, 3})

	inv := readHost(t, f)

	assert.Empty(t, factsOf(inv, metricMemory))
}

type hostCase struct {
	mutate func(*testFS)
	want   hostKind
}

// hostCasesOf returns the detection cases whose expected kind is one of kinds.
func hostCasesOf(kinds ...hostKind) map[string]hostCase {
	out := map[string]hostCase{}
	for name, tt := range hostDetectionCases() {
		if slices.Contains(kinds, tt.want) {
			out[name] = tt
		}
	}
	return out
}

func hostDetectionCases() map[string]hostCase {
	withDMI := func(vendor, product string) func(*testFS) {
		return func(f *testFS) {
			f.set(dmiDir+"sys_vendor", vendor+"\n")
			f.set(dmiDir+"product_name", product+"\n")
		}
	}
	return map[string]hostCase{
		"bare metal":      {func(*testFS) {}, hostPhysical},
		"hypervisor flag": {func(f *testFS) { f.set(cpuinfoPath, "processor\t: 0\nflags\t\t: fpu hypervisor\n") }, hostVirtual},
		"qemu":            {withDMI("QEMU", "Standard PC (Q35 + ICH9, 2009)"), hostVirtual},
		"kvm product":     {withDMI("Red Hat", "KVM"), hostVirtual},
		"vmware":          {withDMI("VMware, Inc.", "VMware7,1"), hostVirtual},
		"virtualbox":      {withDMI("innotek GmbH", "VirtualBox"), hostVirtual},
		"hyper-v":         {withDMI("Microsoft Corporation", "Virtual Machine"), hostVirtual},
		"amazon instance": {withDMI("Amazon EC2", "m5.large"), hostVirtual},
		"amazon metal":    {withDMI("Amazon EC2", "m5.metal"), hostPhysical},
		"google":          {withDMI("Google", "Google Compute Engine"), hostVirtual},
		"xen guest":       {func(f *testFS) { f.set("sys/hypervisor/type", "xen\n") }, hostVirtual},
		"xen control": {func(f *testFS) {
			f.set("sys/hypervisor/type", "xen\n").set("sys/hypervisor/start_flags/initdomain", "1\n")
		}, hostPhysical},
		"xen control with the word true": {func(f *testFS) {
			f.set("sys/hypervisor/type", "xen\n").set("sys/hypervisor/start_flags/initdomain", "True\n")
		}, hostPhysical},
		"xen control with yes": {func(f *testFS) {
			f.set("sys/hypervisor/type", "xen\n").set("sys/hypervisor/start_flags/initdomain", "YES\n")
		}, hostPhysical},
		"xen guest with initdomain false": {func(f *testFS) {
			f.set("sys/hypervisor/type", "xen\n").set("sys/hypervisor/start_flags/initdomain", "0\n")
		}, hostVirtual},
		"xen guest with an unknown initdomain value": {func(f *testFS) {
			f.set("sys/hypervisor/type", "xen\n").set("sys/hypervisor/start_flags/initdomain", "maybe\n")
		}, hostVirtual},
		"xen guest with guest types only": {func(f *testFS) {
			f.set("sys/hypervisor/type", "xen\n").set("sys/hypervisor/properties/capabilities", "xen-3.0-x86_64 control_d\n")
		}, hostVirtual},
		"initdomain without the xen type": {func(f *testFS) {
			f.set("sys/hypervisor/type", "kvm\n").set("sys/hypervisor/start_flags/initdomain", "1\n")
		}, hostVirtual},
		"docker cgroup":   {func(f *testFS) { f.set("proc/1/cgroup", "12:cpu:/docker/abc\n") }, hostContainer},
		"kubernetes":      {func(f *testFS) { f.set("proc/1/cgroup", "0::/kubepods/burstable/pod1/c1\n") }, hostContainer},
		"containerd":      {func(f *testFS) { f.set("proc/1/cgroup", "0::/system.slice/containerd.service/x\n") }, hostContainer},
		"dockerenv file":  {func(f *testFS) { f.set(".dockerenv", "") }, hostContainer},
		"podman file":     {func(f *testFS) { f.set("run/.containerenv", "") }, hostContainer},
		"host init scope": {func(f *testFS) { f.set("proc/1/cgroup", "0::/init.scope\n") }, hostPhysical},
		"xen control domain with the hypervisor flag": {func(f *testFS) {
			f.set(cpuinfoPath, "processor\t: 0\nflags\t\t: fpu hypervisor\n")
			f.set("sys/hypervisor/type", "xen\n").set("sys/hypervisor/start_flags/initdomain", "1\n")
		}, hostPhysical},
		"xen guest with the hypervisor flag": {func(f *testFS) {
			f.set(cpuinfoPath, "processor\t: 0\nflags\t\t: fpu hypervisor\n")
			f.set("sys/hypervisor/type", "xen\n").set("sys/hypervisor/properties/capabilities", "xen-3.0-x86_64\n").set("sys/hypervisor/start_flags/initdomain", "0\n")
		}, hostVirtual},
		"lxc by the environment of the collector":  {func(f *testFS) { f.set("proc/self/environ", "HOME=/root\x00container=lxc\x00PATH=/bin\x00") }, hostContainer},
		"no container variable in the environment": {func(f *testFS) { f.set("proc/self/environ", "HOME=/root\x00PATH=/bin\x00") }, hostPhysical},
		"nspawn by the runtime file on cgroup v2":  {func(f *testFS) { f.set("run/systemd/container", "systemd-nspawn\n").set("proc/1/cgroup", "0::/\n") }, hostContainer},
		"arm kvm guest by the hypervisor node":     {func(f *testFS) { f.set("proc/device-tree/hypervisor/compatible", "linux,kvm\x00") }, hostVirtual},
		"arm xen guest by the hypervisor node": {func(f *testFS) {
			f.set("proc/device-tree/hypervisor/compatible", "xen,xen-4.17\x00xen,xen\x00")
		}, hostVirtual},
		"arm guest with a fw-cfg node": {func(f *testFS) {
			f.set("proc/device-tree/fw-cfg@9020000/compatible", "qemu,fw-cfg-mmio\x00")
		}, hostVirtual},
		"arm board with a node named like fw-cfg only in a file": {func(f *testFS) {
			f.set("proc/device-tree/fw-cfg", "x")
		}, hostPhysical},
		"sysfs device tree is not read": {func(f *testFS) {
			f.set("sys/firmware/devicetree/base/hypervisor/compatible", "linux,kvm\x00")
		}, hostPhysical},
		"qemu virt machine by the root compatible":  {func(f *testFS) { f.set("proc/device-tree/compatible", "linux,dummy-virt\x00") }, hostVirtual},
		"arm board with an ordinary compatible":     {func(f *testFS) { f.set("proc/device-tree/compatible", "raspberrypi,4-model-b\x00brcm,bcm2711\x00") }, hostPhysical},
		"seabios firmware with other strings blank": {func(f *testFS) { f.set(dmiDir+"bios_vendor", "SeaBIOS\n") }, hostVirtual},
		"cannot be decided": {func(f *testFS) {
			f.remove(cpuinfoPath)
			for n := range f.files {
				if strings.HasPrefix(n, "sys/class/dmi") {
					f.remove(n)
				}
			}
		}, hostUndecided},
	}
}

func TestLinuxHostDetection(t *testing.T) {
	for name, tt := range hostDetectionCases() {
		t.Run(name, func(t *testing.T) {
			f := physicalHost()
			tt.mutate(f)

			assert.Equal(t, tt.want, readHost(t, f).host)
		})
	}
}

func TestLinuxVirtualHostAndContainerReportNothing(t *testing.T) {
	for name, tt := range hostCasesOf(hostVirtual, hostContainer) {
		t.Run(name, func(t *testing.T) {
			f := physicalHost()
			tt.mutate(f)

			inv := readHost(t, f)

			assert.Empty(t, inv.facts)
			assert.Empty(t, inv.status)
			assert.NotEmpty(t, inv.hostReason)
		})
	}
}

func TestLinuxPhysicalAndUndecidedHostReportFacts(t *testing.T) {
	for name, tt := range hostCasesOf(hostPhysical, hostUndecided) {
		t.Run(name, func(t *testing.T) {
			f := physicalHost()
			tt.mutate(f)

			assert.NotEmpty(t, readHost(t, f).facts)
		})
	}
}

func TestLinuxManyDrivesAreCappedAndSorted(t *testing.T) {
	f := newTestFS()
	f.set(cpuinfoPath, cpuinfoOneSocket)
	for i := 0; i < 200; i++ {
		n := fmt.Sprintf("sdx%03d", i)
		f.set("sys/block/"+n+"/size", "2048\n")
		f.set("sys/block/"+n+"/device/model", "Disk\n")
		f.set("sys/block/"+n+"/device/serial", fmt.Sprintf("SER%04d\n", i))
	}

	inv := (&linuxSource{fsys: f, maxDrives: 10}).read(context.Background())

	drives := factsOf(inv, metricDrive)
	require.Len(t, drives, 10)
	assert.Equal(t, "SER0000", drives[0].attrs["hw.serial_number"])
	assert.True(t, inv.drivesCapped)
	assert.True(t, inv.status["drive"])
}

func TestLinuxDriveWithNoMediaOrNoIdentityIsSkipped(t *testing.T) {
	f := newTestFS()
	f.set(cpuinfoPath, cpuinfoOneSocket)
	f.set("sys/block/sdb/size", "0\n")
	f.set("sys/block/sdb/device/model", "Card reader\n")
	f.set("sys/block/sdc/size", "2048\n")

	inv := readHost(t, f)

	assert.Empty(t, factsOf(inv, metricDrive))
}

func TestLinuxTwoReadsGiveTheSameAttributeSets(t *testing.T) {
	f := physicalHost()

	first := readHost(t, f)
	second := readHost(t, f)

	assert.Equal(t, first.facts, second.facts)
	assert.Equal(t, first.status, second.status)
}

func TestLinuxTwoSocketHostGivesOneCPUSeriesPerSocket(t *testing.T) {
	f := physicalHost()
	f.set(cpuinfoPath, `processor	: 0
model name	: AMD EPYC 7551 32-Core Processor
vendor_id	: AuthenticAMD
physical id	: 0
core id		: 0

processor	: 1
model name	: AMD EPYC 7551 32-Core Processor
vendor_id	: AuthenticAMD
physical id	: 1
core id		: 0
`)
	f.set("sys/devices/system/cpu/cpu1/cpufreq/cpuinfo_max_freq", "2000000\n")
	f.remove("sys/devices/system/cpu/cpu0/cpufreq/base_frequency")

	inv := readHost(t, f)

	cpus := factsOf(inv, metricCPU)
	require.Len(t, cpus, 2)
	assert.Equal(t, int64(2), cpus[0].attrs["cpu.sockets"])
	assert.Equal(t, int64(1), cpus[0].attrs["cpu.cores"])
	assert.NotEqual(t, cpus[0].attrs["hw.id"], cpus[1].attrs["hw.id"])
	assert.NotContains(t, cpus[0].attrs, "cpu.frequency.nominal", "no frequency in the model name or cpufreq")
}

func TestLinuxCPUWithNoSocketIDsFallsBackToTopologyThenLeavesCountsOut(t *testing.T) {
	f := newTestFS()
	f.set(cpuinfoPath, "processor\t: 0\nmodel name\t: Test CPU\n\nprocessor\t: 1\nmodel name\t: Test CPU\n")
	f.set("sys/devices/system/cpu/cpu0/topology/physical_package_id", "0\n")
	f.set("sys/devices/system/cpu/cpu0/topology/core_id", "0\n")
	f.set("sys/devices/system/cpu/cpu1/topology/physical_package_id", "0\n")
	f.set("sys/devices/system/cpu/cpu1/topology/core_id", "1\n")

	cpu := oneFact(t, readHost(t, f), metricCPU)

	assert.Equal(t, int64(2), cpu.attrs["cpu.cores"])
	assert.Equal(t, int64(2), cpu.attrs["cpu.threads"])

	bare := newTestFS().set(cpuinfoPath, "processor\t: 0\nmodel name\t: Test CPU\n")
	cpu = oneFact(t, readHost(t, bare), metricCPU)
	assert.NotContains(t, cpu.attrs, "cpu.cores")
	assert.NotContains(t, cpu.attrs, "cpu.sockets")
	assert.Equal(t, int64(1), cpu.attrs["cpu.threads"])
}

func TestLinuxReadsOnlyTheListedPaths(t *testing.T) {
	f := physicalHost()

	readHost(t, f)

	allowed := regexp.MustCompile(`^(proc/cpuinfo|proc/meminfo|proc/1/cgroup|proc/self/environ|\.dockerenv|run/\.containerenv|run/systemd/container|` +
		`sys/hypervisor(/type|/properties/capabilities)?|` +
		`proc/device-tree(/compatible|/hypervisor(/compatible)?|/fw-cfg[^/]*)?|sys/hypervisor/start_flags(/initdomain)?|` +
		`sys/class/dmi/id(/[a-z_]+)?|sys/firmware/dmi/tables/(DMI|smbios_entry_point)|` +
		`sys/devices/system/cpu(/cpu\d+(/cpufreq(/[a-z_]+)?|/topology(/[a-z_]+)?)?)?|` +
		`sys/block(/[a-z0-9-]+(/size|/queue|/queue/rotational|/device(/(model|vendor|rev|serial|firmware_rev|wwid|vpd_pg80|vpd_pg89|sas_address))?)?)?|` +
		`sys/bus/pci/devices(/[0-9a-f:.]+(/(class|vendor|device|driver|mem_info_vram_total))?)?|\.)$`)
	for _, name := range f.openedNames() {
		assert.Regexp(t, allowed, name)
	}
	assert.NotContains(t, f.openedNames(), "sys/class/dmi/id/product_uuid")
	assert.NotContains(t, f.openedNames(), "proc/1/environ")
}

// cpuinfoBlocks builds the /proc/cpuinfo of n threads on two sockets, each with a long flags line.
func cpuinfoBlocks(n int) string {
	var b strings.Builder
	flags := strings.Repeat("fpu ", 500)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "processor\t: %d\nvendor_id\t: GenuineIntel\nmodel name\t: Test CPU\nphysical id\t: %d\ncore id\t\t: %d\nflags\t\t: %s\n\n",
			i, i/(n/2), (i%(n/2))/2, flags)
	}
	return b.String()
}

func TestLinuxHostOf512ThreadsIsCountedInFull(t *testing.T) {
	f := physicalHost()
	raw := cpuinfoBlocks(512)
	require.Greater(t, len(raw), 1<<20, "the fixture is larger than the old read limit")
	f.set(cpuinfoPath, raw)

	inv := readHost(t, f)

	cpus := factsOf(inv, metricCPU)
	require.Len(t, cpus, 2)
	assert.Equal(t, int64(256), cpus[0].attrs["cpu.threads"])
	assert.Equal(t, int64(256), cpus[1].attrs["cpu.threads"])
	assert.Equal(t, int64(128), cpus[0].attrs["cpu.cores"])
}

func TestLinuxCPUInfoCutAtTheLimitGivesNoCountsAndIsNotCountedAsReal(t *testing.T) {
	f := physicalHost()
	f.set(cpuinfoPath, cpuinfoBlocks(64))

	inv := (&linuxSource{fsys: f, maxDrives: 64, cpuInfoCap: 20_000}).read(context.Background())

	cpus := factsOf(inv, metricCPU)
	require.NotEmpty(t, cpus)
	for _, c := range cpus {
		assert.NotContains(t, c.attrs, "cpu.threads")
		assert.NotContains(t, c.attrs, "cpu.cores")
		assert.NotContains(t, c.attrs, "cpu.sockets")
		assert.Equal(t, "Test CPU", c.attrs["hw.model"])
	}
	assert.Contains(t, inv.notes, "the CPU list was cut at the read limit; counts are left out")
}

func TestLinuxCPUInfoBelowTheLimitIsNotTruncated(t *testing.T) {
	inv := (&linuxSource{fsys: physicalHost(), maxDrives: 64, cpuInfoCap: int64(len(cpuinfoOneSocket))}).read(context.Background())

	assert.Equal(t, int64(4), oneFact(t, inv, metricCPU).attrs["cpu.threads"], "a file of exactly the limit is complete")
	assert.Empty(t, inv.notes)
}

func TestLinuxMMCBootAndReplayProtectedPartitionsAreNotDrives(t *testing.T) {
	f := physicalHost()
	for _, n := range []string{"mmcblk0", "mmcblk0boot0", "mmcblk0boot1", "mmcblk0rpmb"} {
		f.set("sys/block/"+n+"/size", "7634944\n")
		f.set("sys/block/"+n+"/device/model", "SEM16G\n")
		f.set("sys/block/"+n+"/device/serial", "0x1234abcd\n")
	}

	inv := readHost(t, f)

	var mmc []fact
	for _, d := range factsOf(inv, metricDrive) {
		if d.attrs["drive.bus"] == "mmc" {
			mmc = append(mmc, d)
		}
	}
	require.Len(t, mmc, 1)
	assert.Equal(t, "0x1234abcd", mmc[0].attrs["hw.id"])
}

func TestLinuxDriveWithNoSerialOrWWIDKeepsAnIDThatSurvivesARename(t *testing.T) {
	const path = "../devices/pci0000:00/0000:00:1f.2/ata1/host0/target0:0:0/0:0:0:0/block/"
	build := func(name string) fact {
		f := newTestFS().set(cpuinfoPath, cpuinfoOneSocket)
		f.set("sys/block/"+name+"/size", "2048\n")
		f.set("sys/block/"+name+"/device/model", "Disk\n")
		f.link("sys/block/"+name, path+name)
		return oneFact(t, readHost(t, f), metricDrive)
	}

	first, renamed := build("sda"), build("sdb")

	assert.Equal(t, "pci0000:00/0000:00:1f.2/ata1/host0/target0:0:0/0:0:0:0", first.attrs["hw.id"])
	assert.Equal(t, first.attrs["hw.id"], renamed.attrs["hw.id"])
}

func TestLinuxNVMeWithNoSerialUsesItsPCIAddressNotTheControllerName(t *testing.T) {
	f := newTestFS().set(cpuinfoPath, cpuinfoOneSocket)
	f.set("sys/block/nvme3n1/size", "2048\n")
	f.set("sys/block/nvme3n1/device/model", "Disk\n")
	f.link("sys/block/nvme3n1", "../devices/pci0000:00/0000:00:1d.0/0000:3b:00.0/nvme/nvme3/nvme3n1")

	assert.Equal(t, "pci0000:00/0000:00:1d.0/0000:3b:00.0", oneFact(t, readHost(t, f), metricDrive).attrs["hw.id"])
}

func TestLinuxDriveWithNoSerialWWIDOrBusPathFallsBackToTheKernelName(t *testing.T) {
	f := newTestFS().set(cpuinfoPath, cpuinfoOneSocket)
	f.set("sys/block/sdc/size", "2048\n")
	f.set("sys/block/sdc/device/model", "Disk\n")

	assert.Equal(t, "sdc", oneFact(t, readHost(t, f), metricDrive).attrs["hw.id"])
}

func TestLinuxBMCVideoControllersAreNotGPUs(t *testing.T) {
	f := physicalHost()
	for addr, ids := range map[string][2]string{
		"0000:03:00.0": {"0x1a03", "0x2000"}, // ASPEED AST2xxx
		"0000:04:00.0": {"0x102b", "0x0534"}, // Matrox G200eR2
		"0000:05:00.0": {"0x102b", "0x0532"}, // Matrox G200eW
		"0000:06:00.0": {"0x1002", "0x67df"}, // AMD, a real GPU
		"0000:07:00.0": {"0x1234", "0x5678"}, // unknown ids, bound to the BMC video driver
	} {
		f.set("sys/bus/pci/devices/"+addr+"/class", "0x030000\n")
		f.set("sys/bus/pci/devices/"+addr+"/vendor", ids[0]+"\n")
		f.set("sys/bus/pci/devices/"+addr+"/device", ids[1]+"\n")
	}

	f.link("sys/bus/pci/devices/0000:07:00.0/driver", "../../../bus/pci/drivers/ast")

	inv := readHost(t, f)

	var got []any
	for _, g := range factsOf(inv, metricGPU) {
		got = append(got, g.attrs["hw.id"])
	}
	assert.ElementsMatch(t, []any{"0000:01:00.0", "0000:06:00.0"}, got)
	assert.True(t, inv.status["gpu"])
}

func TestLinuxStopsWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	inv := (&linuxSource{fsys: physicalHost(), maxDrives: 64}).read(ctx)

	assert.Empty(t, factsOf(inv, metricDrive))
}
