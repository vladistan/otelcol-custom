// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
)

// The two bundles under testdata are scrubbed captures of real controllers: a Supermicro 2208 with
// two RAID 1 volumes and no battery, and an Avago 3108 with six single-drive RAID 0 volumes and a
// CacheVault module. The values below are what those bytes decode to.

func readBin(t *testing.T, bundle, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", bundle, name))
	require.NoError(t, err)
	return raw
}

func TestReal3108ControllerInfo(t *testing.T) {
	c, err := decodeCtrlInfo(readBin(t, "c3108-a-host0", "ctrl_getinfo.bin"))

	require.NoError(t, err)
	assert.True(t, c.supported())
	assert.Equal(t, uint16(0x005D), c.PCIDevice)
	assert.Equal(t, "AVAGO 3108 MegaRAID", c.Product)
	assert.Equal(t, "4.680.00-8290", c.Firmware)
	assert.Equal(t, 2048, c.MemoryMB)
	assert.True(t, c.HasBBU)
	assert.Equal(t, 55, c.TempROC)
	assert.Zero(t, c.TempBoard, "this firmware reports no board temperature")
	assert.Equal(t, 7, c.PDPresent, "six drives and the enclosure")
	assert.Zero(t, c.LDDegraded+c.LDOffline+c.PDPredFail+c.PDFailed)
	assert.Equal(t, hwschema.StateOK, controllerState(c))
	assert.False(t, c.extendedLDs())
}

func TestReal2208ControllerInfo(t *testing.T) {
	c, err := decodeCtrlInfo(readBin(t, "c2208-a-host0", "ctrl_getinfo.bin"))

	require.NoError(t, err)
	assert.True(t, c.supported())
	assert.Equal(t, uint16(0x005B), c.PCIDevice)
	assert.Equal(t, "Supermicro SMC2208", c.Product)
	assert.Equal(t, "3.240.05-2282", c.Firmware)
	assert.Equal(t, 1024, c.MemoryMB)
	assert.False(t, c.HasBBU, "this card has no battery or CacheVault")
	assert.Equal(t, 50, c.TempROC)
	assert.Equal(t, 5, c.PDPresent, "four drives and the enclosure")
	assert.Equal(t, hwschema.StateOK, controllerState(c))
}

func TestRealListsAndDriveInfo(t *testing.T) {
	list, err := decodePDList(readBin(t, "c3108-a-host0", "pd_get_list.bin"))
	require.NoError(t, err)
	var ids []uint16
	for _, a := range list {
		ids = append(ids, a.DeviceID)
	}
	assert.ElementsMatch(t, []uint16{13, 15, 17, 19, 20, 21, 252}, ids, "six drives and the enclosure 252")
	lds, err := decodeLDList(readBin(t, "c3108-a-host0", "ld_get_list.bin"))
	require.NoError(t, err)
	assert.ElementsMatch(t, []uint8{0, 1, 2, 3, 4, 6}, lds)

	p, err := decodePDInfo(readBin(t, "c3108-a-host0", "pd_get_info_15.bin"))
	require.NoError(t, err)
	assert.Equal(t, uint16(15), p.DeviceID)
	assert.Equal(t, "STEC", p.Vendor)
	assert.Equal(t, "Z16IZF2E-800UCZ", p.Product)
	assert.Equal(t, pdOnline, p.FWState)
	assert.Equal(t, uint64(799602114560), p.SizeBytes)
	assert.Equal(t, uint8(1), p.Slot, "event log: PD 0f(e0xfc/s1)")
	assert.Equal(t, uint16(252), p.EnclID)
	assert.Equal(t, 40, p.Temperature)
	hdd, err := decodePDInfo(readBin(t, "c3108-a-host0", "pd_get_info_13.bin"))
	require.NoError(t, err)
	assert.Equal(t, "ATA", hdd.Vendor)
	assert.Equal(t, 25, hdd.Temperature)
}

func TestRealLogicalDrives(t *testing.T) {
	l, err := decodeLDInfo(readBin(t, "c3108-a-host0", "ld_get_info_3.bin"))
	require.NoError(t, err)
	assert.Equal(t, "raid0", raidLevel(l.Level, l.SpanDepth))
	assert.Equal(t, uint64(262144), stripBytes(l.StripeLog2))
	assert.Equal(t, uint8(ldStateOptimal), l.State)
	assert.Equal(t, uint64(1599774654464), l.SizeBytes)
	assert.Empty(t, l.Progress)

	l, err = decodeLDInfo(readBin(t, "c2208-a-host0", "ld_get_info_0.bin"))
	require.NoError(t, err)
	assert.Equal(t, "raid1", raidLevel(l.Level, l.SpanDepth))
	assert.Equal(t, uint64(65536), stripBytes(l.StripeLog2))
	assert.Equal(t, uint64(3999999721472), l.SizeBytes)
	_, cache, err := decodeLDProps(readBin(t, "c2208-a-host0", "ld_get_prop_0.bin"))
	require.NoError(t, err)
	assert.Equal(t, uint8(0x74), cache)
}

func TestReal3108BatteryAndTheCapacityCommandThatIsNotSupported(t *testing.T) {
	status := readBin(t, "c3108-a-host0", "bbu_get_status.bin")
	props := readBin(t, "c3108-a-host0", "bbu_get_prop.bin")

	b, err := decodeBBU(status, nil, props)

	require.NoError(t, err)
	assert.Equal(t, 32, b.Temperature)
	assert.Equal(t, hwschema.StateOK, bbuState(b.Status))
	assert.True(t, b.hasNextLearn)
	assert.False(t, b.hasCapacity)

	dev, m := loadBundle(t, filepath.Join("testdata", "c3108-a-host0"))
	s, logs, _ := newTestScraper(t, testConfig(), dev, controller{HostNo: m.HostNo, PCI: m.PCI})
	md := s.scrape(context.Background()).metrics
	assert.Equal(t, hwschema.StateOK, current(t, md, typeBattery, "0000:04:00.0/bbu"))
	_, charge := find(md, metricBatteryCharge)
	assert.False(t, charge, "no charge without the capacity command")
	assert.Zero(t, logs.FilterLevelExact(zapcore.WarnLevel).Len(), "status 61 is quiet")
	assert.Equal(t, 1, dev.count(opBBUGetCapacityInfo))
	s.scrape(context.Background())
	assert.Equal(t, 2, dev.count(opBBUGetCapacityInfo), "and asked again at the next scrape")
}

func TestReal3108ScrapeGivesTheLabHost(t *testing.T) {
	dev, m := loadBundle(t, filepath.Join("testdata", "c3108-a-host0"))
	s, logs, _ := newTestScraper(t, testConfig(), dev, controller{HostNo: m.HostNo, PCI: m.PCI})

	md := s.scrape(context.Background()).metrics

	assert.Zero(t, logs.FilterMessage("This MegaRAID product is outside the supported list and is read best-effort").Len())
	assert.Equal(t, hwschema.StateOK, current(t, md, typeController, "0000:04:00.0"))
	for _, ld := range []string{"ld0", "ld1", "ld2", "ld3", "ld4", "ld6"} {
		assert.Equal(t, hwschema.StateOK, current(t, md, typeLogicalDisk, "0000:04:00.0/"+ld))
	}
	var drives int
	for _, p := range points(md, metricPDSize) {
		assert.Equal(t, "252", p.attrs[attrEnclosure])
		drives++
	}
	assert.Equal(t, 6, drives)
	info, _ := find(md, metricCtrlInfo)
	assert.Equal(t, "2048", info.attrs[attrMemory])
	assert.Equal(t, "4.680.00-8290", info.attrs[hwschema.AttrFirmwareVersion])
	assert.Equal(t, 55.0, func() float64 { p, _ := find(md, metricTemperature, attrLocation, sensorROC); return p.val }())
	assert.Zero(t, dev.count(opEventGet), "events are off in this scrape")
}

func TestReal2208ScrapeSendsNoBatteryCommand(t *testing.T) {
	dev, m := loadBundle(t, filepath.Join("testdata", "c2208-a-host0"))
	s, _, _ := newTestScraper(t, testConfig(), dev, controller{HostNo: m.HostNo, PCI: m.PCI})

	md := s.scrape(context.Background()).metrics

	assert.Equal(t, hwschema.StateOK, current(t, md, typeController, "0000:06:00.0"))
	assert.Equal(t, hwschema.StateOK, current(t, md, typeLogicalDisk, "0000:06:00.0/ld1"))
	for _, op := range []uint32{opBBUGetStatus, opBBUGetCapacityInfo, opBBUGetDesignInfo, opBBUGetProp} {
		assert.Zero(t, dev.count(op), allowed[op])
	}
	_, hasBattery := find(md, metricStatus, hwschema.AttrType, typeBattery)
	assert.False(t, hasBattery)
	var temps int
	for _, p := range points(md, metricTemperature) {
		if p.attrs[attrLocation] == sensorDrive {
			temps++
		}
	}
	assert.Equal(t, 4, temps, "a temperature for each of the four drives")
}

func TestRealEventLog(t *testing.T) {
	state, err := decodeEventState(readBin(t, "c3108-a-host0", "event_get_info.bin"))
	require.NoError(t, err)
	events, err := decodeEvents(readBin(t, "c3108-a-host0", "event_get.bin"))
	require.NoError(t, err)

	require.Len(t, events, 100, "the 25,608 byte answer holds 100 events")
	assert.Equal(t, state.Newest, events[len(events)-1].Sequence, "the capture ends at the newest event")
	for i := 1; i < len(events); i++ {
		assert.Greater(t, events[i].Sequence, events[i-1].Sequence, "sequence numbers rise")
	}
	var warn, info int
	for _, e := range events {
		assert.NotEmpty(t, e.Text)
		switch e.Class {
		case classInfo:
			info++
		case classWarning:
			warn++
		}
	}
	assert.NotZero(t, info)
	t.Logf("events: %d info, %d warning", info, warn)
}

func TestRealEventsFlowThroughTheScraper(t *testing.T) {
	dev, m := loadBundle(t, filepath.Join("testdata", "c3108-a-host0"))
	ctrl := controller{HostNo: m.HostNo, PCI: m.PCI}
	events, err := decodeEvents(readBin(t, "c3108-a-host0", "event_get.bin"))
	require.NoError(t, err)
	last := events[len(events)-1].Sequence
	store := newMemStore()
	store.data[cursorKey(ctrl)] = []byte(strconv.FormatUint(uint64(last-2), 10))
	cfg := createDefaultConfig().(*Config)
	cfg.Events.MinClass = "debug"
	s, _, _ := newTestScraper(t, cfg, dev, ctrl)
	s.events = &eventCursor{store: store}

	out := s.scrape(context.Background())

	recs := records(out.logs)
	require.Len(t, recs, 2, "the two events after the saved position")
	seq, _ := recs[1].Attributes().Get("raid.event.sequence")
	assert.Equal(t, int64(last), seq.Int())
	commitAll(t, out)
	assert.Equal(t, last, store.seq(ctrl))
}
