// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartreceiver

import (
	"encoding/binary"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vladistan/otelcol-custom/receiver/smartdecode/smarttest"
)

func TestDiscoverSumsTheNamespaceSizesOfAnNVMeController(t *testing.T) {
	fsys := sysfsWith([]string{"loop0"}, []string{"nvme0", "nvme1"}) // loop0 is skipped; it keeps /sys/block present
	fsys["class/nvme/nvme0/nvme0n1/size"] = &fstest.MapFile{Data: []byte("1000\n")}
	fsys["class/nvme/nvme0/nvme0n2/size"] = &fstest.MapFile{Data: []byte("24\n")}
	fsys["class/nvme/nvme0/nvme0c0n1/size"] = &fstest.MapFile{Data: []byte("999999\n")} // a path of a multipath namespace
	fsys["class/nvme/nvme1/nvme1n1/size"] = &fstest.MapFile{Data: []byte("not a number\n")}

	devs, err := discover(fsys)

	require.ErrorIs(t, err, errPartialList, "a size that does not parse is reported")
	assert.ErrorContains(t, err, "nvme1n1")
	require.Len(t, devs, 2, "the controllers are still listed")
	assert.Equal(t, uint64(1024*512), devs[0].nsBytes, "two namespaces, in 512 byte sectors")
	assert.Zero(t, devs[1].nsBytes, "an unreadable size gives none")
}

func TestNVMeSizeComesFromTheNamespacesWhenTheControllerReportsNone(t *testing.T) {
	nvme := smarttest.HealthyNVMe("SN9")
	binary.LittleEndian.PutUint64(nvme.Identify[280:], 0) // TNVMCAP is 0 on many consumer drives
	bus := newBus()
	bus.devs = append(bus.devs, device{name: "nvme0", kind: kindNVMe, nsBytes: 512110190592})
	bus.nvme["nvme0"] = nvme

	md := newHarness(t, bus).scrape()

	assert.InDelta(t, 512110190592, single(t, points(md, "hw.physical_disk.size"), "size").value, 0)
}

func TestTheControllerSizeWinsOverTheNamespaceSize(t *testing.T) {
	bus := newBus()
	bus.devs = append(bus.devs, device{name: "nvme0", kind: kindNVMe, nsBytes: 5})
	bus.nvme["nvme0"] = smarttest.HealthyNVMe("SN9")

	md := newHarness(t, bus).scrape()

	assert.InDelta(t, 1000204886016, single(t, points(md, "hw.physical_disk.size"), "size").value, 0)
}

func TestNoSizeAtAllGivesNoSizePoint(t *testing.T) {
	nvme := smarttest.HealthyNVMe("SN9")
	binary.LittleEndian.PutUint64(nvme.Identify[280:], 0)
	bus := newBus()
	bus.addNVMe("nvme0", nvme)

	md := newHarness(t, bus).scrape()

	assert.Empty(t, points(md, "hw.physical_disk.size"))
}

func TestNVMeVendorComesFromThePCIVendorID(t *testing.T) {
	nvme := smarttest.HealthyNVMe("SN9")
	nvme.Identify[0], nvme.Identify[1] = 0x4d, 0x14
	bus := newBus()
	bus.addNVMe("nvme0", nvme)

	md := newHarness(t, bus).scrape()

	temps := points(md, "hw.temperature")
	require.NotEmpty(t, temps)
	assert.Equal(t, "Samsung", temps[0].attrs["hw.vendor"])
}

func TestAnUnknownNVMeVendorLeavesTheAttributeOut(t *testing.T) {
	nvme := smarttest.HealthyNVMe("SN9")
	nvme.Identify[0], nvme.Identify[1] = 0xcd, 0xab
	bus := newBus()
	bus.addNVMe("nvme0", nvme)

	md := newHarness(t, bus).scrape()

	temps := points(md, "hw.temperature")
	require.NotEmpty(t, temps)
	assert.NotContains(t, temps[0].attrs, "hw.vendor")
}

func TestATAVendorComesFromTheModelPrefix(t *testing.T) {
	ata := smarttest.HealthyATA("S1")
	ata.Identify = smarttest.IdentifyBuffer(smarttest.IdentifySpec{
		Model: "Samsung SSD 860 EVO", Serial: "S1", Firmware: "F1", Sectors: 1000, Rotation: 1,
		SMARTSupported: true, SMARTEnabled: true,
	})
	bus := newBus()
	bus.addATA("sda", ata)

	md := newHarness(t, bus).scrape()

	temps := points(md, "hw.temperature")
	require.NotEmpty(t, temps)
	assert.Equal(t, "Samsung", temps[0].attrs["hw.vendor"])
}

func TestAnATAModelNotInTheTableLeavesTheVendorOut(t *testing.T) {
	bus := newBus()
	bus.addATA("sda", smarttest.HealthyATA("S1")) // model "ACME SSD"

	md := newHarness(t, bus).scrape()

	temps := points(md, "hw.temperature")
	require.NotEmpty(t, temps)
	assert.NotContains(t, temps[0].attrs, "hw.vendor")
}
