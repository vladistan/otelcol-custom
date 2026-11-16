// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// errShort means a buffer is shorter than the fields a decoder needs. Longer buffers are fine:
// unknown tail bytes are ignored.
var errShort = errors.New("buffer is shorter than the structure")

// buf reads little-endian fields and returns zero for a field outside the buffer, so no decoder
// reads past the end.
type buf []byte

func (b buf) u8(o int) uint8 {
	if o < 0 || o+1 > len(b) {
		return 0
	}
	return b[o]
}

func (b buf) u16(o int) uint16 {
	if o < 0 || o+2 > len(b) {
		return 0
	}
	return binary.LittleEndian.Uint16(b[o:])
}

func (b buf) u32(o int) uint32 {
	if o < 0 || o+4 > len(b) {
		return 0
	}
	return binary.LittleEndian.Uint32(b[o:])
}

func (b buf) u64(o int) uint64 {
	if o < 0 || o+8 > len(b) {
		return 0
	}
	return binary.LittleEndian.Uint64(b[o:])
}

// text returns the printable part of n bytes at o, trimmed of NULs and spaces.
func (b buf) text(o, n int) string {
	if o < 0 || o >= len(b) {
		return ""
	}
	end := min(o+n, len(b))
	var sb strings.Builder
	for _, c := range b[o:end] {
		if c == 0 {
			break
		}
		if c >= 0x20 && c < 0x7f {
			sb.WriteByte(c)
		}
	}
	return strings.TrimSpace(sb.String())
}

func need(b []byte, n int, what string) error {
	if len(b) < n {
		return fmt.Errorf("%w: %s has %d bytes, need %d", errShort, what, len(b), n)
	}
	return nil
}

// ---- Controller (CTRL_GETINFO). Layout of struct mfi_ctrl_info in the FreeBSD header mfireg.h
// (BSD licence) and the Linux megaraid_sas.h; each offset below was checked on captures of a 2208 and
// a 3108. Where the counts, the memory size and the drive temperature (offset 402) differed, the bytes won.
const (
	ctrlPCIVendorOff   = 0
	ctrlPCIDeviceOff   = 2
	ctrlImageCountOff  = 180
	ctrlImageOff       = 184
	ctrlImageSize      = 72
	ctrlImageMax       = 8
	ctrlMaxLDsOff      = 1343
	ctrlProductOff     = 1344
	ctrlProductLen     = 80
	ctrlSerialOff      = 1424
	ctrlSerialLen      = 32
	ctrlHWPresentOff   = 1456
	ctrlLDDegradedOff  = 1474
	ctrlLDOfflineOff   = 1476
	ctrlPDPresentOff   = 1478
	ctrlPDPredFailOff  = 1482
	ctrlPDFailedOff    = 1484
	ctrlMemorySizeOff  = 1488
	ctrlMinLen         = 1500
	ctrlTempROCOff     = 0x7C9
	ctrlTempBoardOff   = 0x7CA
	hwPresentBBU       = 0x01
	extendedLDsAbove   = 64
	extendedPDsAbove   = 240
	pciVendorBroadcom  = 0x1000
	pciDevice2208      = 0x005B
	pciDevice3108      = 0x005D
	ctrlInfoRequestLen = 2048
)

type ctrlInfo struct {
	PCIVendor, PCIDevice uint16
	Product, Serial      string
	Firmware             string
	MemoryMB             int
	MaxLDs               int
	HasBBU               bool
	LDOffline            int
	LDDegraded           int
	PDPresent            int
	PDPredFail           int
	PDFailed             int
	// TempROC and TempBoard are 0 when the controller reports none.
	TempROC, TempBoard int
}

func decodeCtrlInfo(raw []byte) (ctrlInfo, error) {
	if err := need(raw, ctrlMinLen, "controller info"); err != nil {
		return ctrlInfo{}, err
	}
	b := buf(raw)
	c := ctrlInfo{
		PCIVendor:  b.u16(ctrlPCIVendorOff),
		PCIDevice:  b.u16(ctrlPCIDeviceOff),
		Product:    b.text(ctrlProductOff, ctrlProductLen),
		Serial:     b.text(ctrlSerialOff, ctrlSerialLen),
		MemoryMB:   int(b.u16(ctrlMemorySizeOff)),
		MaxLDs:     int(b.u8(ctrlMaxLDsOff)),
		HasBBU:     b.u32(ctrlHWPresentOff)&hwPresentBBU != 0,
		LDOffline:  int(b.u16(ctrlLDOfflineOff)),
		LDDegraded: int(b.u16(ctrlLDDegradedOff)),
		PDPresent:  int(b.u16(ctrlPDPresentOff)),
		PDPredFail: int(b.u16(ctrlPDPredFailOff)),
		PDFailed:   int(b.u16(ctrlPDFailedOff)),
	}
	count := min(int(b.u32(ctrlImageCountOff)), ctrlImageMax)
	for i := 0; i < count; i++ {
		name := b.text(ctrlImageOff+i*ctrlImageSize, 8)
		version := b.text(ctrlImageOff+i*ctrlImageSize+8, 32)
		if version == "" {
			continue
		}
		if name == "APP" || c.Firmware == "" {
			c.Firmware = version
		}
	}
	if len(raw) > ctrlTempBoardOff {
		c.TempROC = plausibleTemp(b.u8(ctrlTempROCOff))
		c.TempBoard = plausibleTemp(b.u8(ctrlTempBoardOff))
	}
	return c, nil
}

// plausibleTemp drops 0 and 0xFF, which mean the controller reports no temperature.
func plausibleTemp(v uint8) int {
	if v == 0 || v == 0xFF {
		return 0
	}
	return int(v)
}

// supported reports whether the controller is one of the two generations the docs declare.
func (c ctrlInfo) supported() bool {
	return c.PCIVendor == pciVendorBroadcom && (c.PCIDevice == pciDevice2208 || c.PCIDevice == pciDevice3108)
}

// extendedLDs and extendedPDs choose the LD_LIST_QUERY and PD_LIST_QUERY commands: the plain lists
// hold 64 and 240 entries. The controller's capability bit is not used, because its offset is not
// confirmed; the logical drive limit and the drive count stand in for it.
func (c ctrlInfo) extendedLDs() bool { return c.MaxLDs > extendedLDsAbove }
func (c ctrlInfo) extendedPDs() bool { return c.PDPresent > extendedPDsAbove }

// ---- Drive list (PD_GET_LIST, PD_LIST_QUERY): struct mfi_pd_list of mfireg.h, an 8 byte header
// (size, count) and 24 byte struct mfi_pd_address entries.
const (
	pdListHeader = 8
	pdEntrySize  = 24
	maxPDEntries = 240
)

type pdAddr struct {
	DeviceID  uint16
	EnclID    uint16
	EnclIndex uint8
	Slot      uint8
	SCSIType  uint8
}

func decodePDList(raw []byte) ([]pdAddr, error) {
	if err := need(raw, pdListHeader, "drive list"); err != nil {
		return nil, err
	}
	b := buf(raw)
	n := min(int(b.u32(4)), maxPDEntries)
	var out []pdAddr
	seen := map[uint16]bool{}
	for i := 0; i < n && pdListHeader+(i+1)*pdEntrySize <= len(raw); i++ {
		o := pdListHeader + i*pdEntrySize
		a := pdAddr{DeviceID: b.u16(o), EnclID: b.u16(o + 2), EnclIndex: b.u8(o + 4), Slot: b.u8(o + 5), SCSIType: b.u8(o + 6)}
		if seen[a.DeviceID] {
			continue
		}
		seen[a.DeviceID] = true
		out = append(out, a)
	}
	return out, nil
}

// ---- Logical drive list (LD_GET_LIST): struct mfi_ld_list of mfireg.h, an 8 byte header (count,
// size) and 16 byte entries that start with the target id. LD_LIST_QUERY answers with the Linux
// MR_LD_TARGETID_LIST: size, count, three pad bytes, then one byte per target id.
const (
	ldListHeader   = 8
	ldEntrySize    = 16
	maxLDEntries   = 64
	ldQueryHeader  = 11
	maxLDExtended  = 256
	ldQueryReqLen  = 512
	ldListReqLen   = 1032
	pdListReqLen   = 5768
	ldInfoReqLen   = 384
	ldPropReqLen   = 32
	pdInfoReqLen   = 512
	bbuStatusLen   = 64
	bbuCapacityLen = 48
	bbuPropLen     = 256
	pdQueryExposed = 5 // MR_PD_QUERY_TYPE_EXPOSED_TO_HOST
	ldQueryAll     = 0
)

func decodeLDList(raw []byte) ([]uint8, error) {
	if err := need(raw, ldListHeader, "logical drive list"); err != nil {
		return nil, err
	}
	b := buf(raw)
	n := min(int(b.u32(0)), maxLDEntries)
	var out []uint8
	seen := map[uint8]bool{}
	for i := 0; i < n && ldListHeader+(i+1)*ldEntrySize <= len(raw); i++ {
		id := b.u8(ldListHeader + i*ldEntrySize)
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

func decodeLDQuery(raw []byte) ([]uint8, error) {
	if err := need(raw, ldQueryHeader, "logical drive query"); err != nil {
		return nil, err
	}
	b := buf(raw)
	n := min(int(b.u32(4)), maxLDExtended)
	var out []uint8
	seen := map[uint8]bool{}
	for i := 0; i < n && ldQueryHeader+i < len(raw); i++ {
		id := b.u8(ldQueryHeader + i)
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// ---- Logical drive info (LD_GET_INFO, LD_GET_PROP): struct mfi_ld_info of mfireg.h. The first 32
// bytes are struct mfi_ld_props (target, name, cache policies), the next 32 struct mfi_ld_params.
const (
	ldTargetOff      = 0
	ldNameOff        = 4
	ldNameLen        = 16
	ldCurCacheOff    = 23
	ldLevelOff       = 32
	ldQualifierOff   = 33
	ldStripeOff      = 35
	ldDrivesOff      = 36
	ldSpanDepthOff   = 37
	ldStateOff       = 38
	ldInitOff        = 39
	ldConsistentOff  = 40
	ldSizeOff        = 256
	ldProgressOff    = 264
	ldInfoMinLen     = 284
	ldPropMinLen     = 24
	ldBlockSize      = 512
	ldStateOffline   = 0
	ldStatePartial   = 1
	ldStateDegraded  = 2
	ldStateOptimal   = 3
	progressDenomMFI = 65535.0
)

type ldInfo struct {
	Target     uint8
	Name       string
	Level      uint8
	Qualifier  uint8
	StripeLog2 uint8
	Drives     int
	SpanDepth  int
	State      uint8
	Consistent bool
	SizeBytes  uint64
	Cache      uint8
	Progress   map[string]float64 // operation name to 0..1
}

func decodeLDInfo(raw []byte) (ldInfo, error) {
	if err := need(raw, ldInfoMinLen, "logical drive info"); err != nil {
		return ldInfo{}, err
	}
	b := buf(raw)
	l := ldInfo{
		Target:     b.u8(ldTargetOff),
		Name:       b.text(ldNameOff, ldNameLen),
		Level:      b.u8(ldLevelOff),
		Qualifier:  b.u8(ldQualifierOff),
		StripeLog2: b.u8(ldStripeOff),
		Drives:     int(b.u8(ldDrivesOff)),
		SpanDepth:  int(b.u8(ldSpanDepthOff)),
		State:      b.u8(ldStateOff),
		Consistent: b.u8(ldConsistentOff) != 0,
		SizeBytes:  b.u64(ldSizeOff) * ldBlockSize,
		Cache:      b.u8(ldCurCacheOff),
		Progress:   map[string]float64{},
	}
	// struct mfi_ld_progress: active flags, then check consistency, background init, foreground
	// init and reconstruction as (progress u16, elapsed seconds u16).
	active := b.u32(ldProgressOff)
	for i, op := range []string{opCheck, opBackgroundInit, opForegroundInit, opReconstruction} {
		if active&(1<<i) != 0 {
			l.Progress[op] = ratio16(b.u16(ldProgressOff + 4 + 4*i))
		}
	}
	return l, nil
}

// decodeLDProps reads struct mfi_ld_props (LD_GET_PROP): the name and the current cache policy.
func decodeLDProps(raw []byte) (name string, cache uint8, err error) {
	if err = need(raw, ldPropMinLen, "logical drive properties"); err != nil {
		return "", 0, err
	}
	b := buf(raw)
	return b.text(ldNameOff, ldNameLen), b.u8(ldCurCacheOff), nil
}

func ratio16(v uint16) float64 { return float64(v) / progressDenomMFI }

// Names of the raid.operation attribute.
const (
	opRebuild        = "rebuild"
	opPatrol         = "patrol"
	opCheck          = "check"
	opBackgroundInit = "init"
	opForegroundInit = "init_foreground"
	opReconstruction = "reconstruction"
	opClear          = "clear"
	opCopyback       = "copyback"
	opErase          = "erase"
)

// ---- Drive info (PD_GET_INFO): struct mfi_pd_info of mfireg.h with the Linux MR_PD_INFO tail.
// The scrubber of the capture tool uses the same inquiry, VPD and SAS address offsets.
const (
	pdInquiryOff     = 4
	pdVendorOff      = pdInquiryOff + 8
	pdProductOff     = pdInquiryOff + 16
	pdSerialOff      = pdInquiryOff + 36
	pdSerialLen      = 20
	pdMediaErrOff    = 168
	pdOtherErrOff    = 172
	pdPredFailOff    = 176
	pdFWStateOff     = 184
	pdLinkSpeedOff   = 187
	pdCoercedOff     = 248
	pdEnclIDOff      = 256
	pdSlotOff        = 259
	pdProgressOff    = 260
	pdTemperatureOff = 402
	pdInfoMinLen     = 294
)

// Firmware states of a physical drive (MFI_PD_STATE_*).
const (
	pdUnconfiguredGood uint16 = 0x00
	pdUnconfiguredBad  uint16 = 0x01
	pdHotSpare         uint16 = 0x02
	pdOffline          uint16 = 0x10
	pdFailed           uint16 = 0x11
	pdRebuild          uint16 = 0x14
	pdOnline           uint16 = 0x18
	pdCopyback         uint16 = 0x20
	pdSystem           uint16 = 0x40
)

type pdInfo struct {
	DeviceID    uint16
	Vendor      string
	Product     string
	Serial      string
	FWState     uint16
	MediaErrors uint32
	OtherErrors uint32
	PredFail    uint32
	SizeBytes   uint64
	EnclID      uint16
	Slot        uint8
	// Temperature is 0 when the drive reports none.
	Temperature int
	Progress    map[string]float64
}

func decodePDInfo(raw []byte) (pdInfo, error) {
	if err := need(raw, pdInfoMinLen, "drive info"); err != nil {
		return pdInfo{}, err
	}
	b := buf(raw)
	p := pdInfo{
		DeviceID:    b.u16(0),
		Vendor:      b.text(pdVendorOff, 8),
		Product:     b.text(pdProductOff, 16),
		Serial:      b.text(pdSerialOff, pdSerialLen),
		FWState:     b.u16(pdFWStateOff),
		MediaErrors: b.u32(pdMediaErrOff),
		OtherErrors: b.u32(pdOtherErrOff),
		PredFail:    b.u32(pdPredFailOff),
		SizeBytes:   b.u64(pdCoercedOff) * ldBlockSize,
		EnclID:      b.u16(pdEnclIDOff),
		Slot:        b.u8(pdSlotOff),
		Progress:    map[string]float64{},
	}
	if len(raw) > pdTemperatureOff {
		p.Temperature = plausibleTemp(b.u8(pdTemperatureOff))
	}
	// struct mfi_pd_progress: active flags (rebuild, patrol, clear, copyback, erase), then the
	// rebuild, patrol and clear or erase counters as (progress u16, elapsed seconds u16).
	active := b.u32(pdProgressOff)
	for i, op := range []string{opRebuild, opPatrol, opClear, opCopyback, opErase} {
		if active&(1<<i) == 0 {
			continue
		}
		switch op {
		case opRebuild:
			p.Progress[op] = ratio16(b.u16(pdProgressOff + 4))
		case opPatrol:
			p.Progress[op] = ratio16(b.u16(pdProgressOff + 8))
		case opClear, opErase:
			p.Progress[op] = ratio16(b.u16(pdProgressOff + 12))
		case opCopyback:
			p.Progress[op] = ratio16(b.u16(pdProgressOff + 4))
		}
	}
	return p, nil
}

// ---- Battery: struct mfi_bbu_status, struct mfi_bbu_capacity_info and struct mfi_bbu_properties
// of mfireg.h. The CacheVault values are not in the header and stay unconfirmed.
const (
	bbuStatusMin   = 12
	bbuCapacityMin = 16
	bbuPropMin     = 8

	bbuPackMissing  = 0x01
	bbuVoltageLow   = 0x02
	bbuTempHigh     = 0x04
	bbuLearnFailed  = 0x80
	bbuLearnTimeout = 0x100
	bbuI2CError     = 0x200
	bbuReplace      = 0x400
)

type bbuInfo struct {
	Type         uint8
	VoltageMV    int
	CurrentMA    int
	Temperature  int
	Status       uint32
	ChargePct    int
	CycleCount   int
	RunTimeMin   int
	NextLearn    uint32 // seconds since 2000-01-01 UTC; 0 when not reported
	hasCapacity  bool
	hasNextLearn bool
}

func decodeBBU(status, capacity, props []byte) (bbuInfo, error) {
	if err := need(status, bbuStatusMin, "battery status"); err != nil {
		return bbuInfo{}, err
	}
	s := buf(status)
	out := bbuInfo{
		Type:        s.u8(0),
		VoltageMV:   int(s.u16(2)),
		CurrentMA:   int(int16(s.u16(4))), //nolint:gosec // signed current in mA
		Temperature: int(s.u16(6)),
		Status:      s.u32(8),
	}
	if len(capacity) >= bbuCapacityMin {
		c := buf(capacity)
		out.hasCapacity = true
		out.ChargePct = int(c.u16(0))
		out.RunTimeMin = int(c.u16(8))
		out.CycleCount = int(c.u16(14))
	}
	if len(props) >= bbuPropMin {
		out.NextLearn = buf(props).u32(4)
		out.hasNextLearn = out.NextLearn != 0
	}
	return out, nil
}

// mfiEpoch is 2000-01-01 00:00:00 UTC in Unix seconds, the origin of the controller clock.
const mfiEpoch = 946684800

// ---- Events: EVENT_GET_INFO is struct mfi_evt_log_state (five u32 sequence numbers); EVENT_GET
// answers struct mfi_evt_list (count, reserved, then 256 byte struct mfi_evt_detail entries).
const (
	evtInfoMin   = 8
	evtHeader    = 8
	evtSize      = 256
	evtDescOff   = 128
	evtDescSize  = 128
	maxEvents    = (maxDataLen - evtHeader) / evtSize
	evtInfoLen   = 20
	evtLocaleAll = 0xFFFF
)

// Event classes (MFI_EVT_CLASS_*).
const (
	classDebug    int8 = -2
	classProgress int8 = -1
	classInfo     int8 = 0
	classWarning  int8 = 1
	classCritical int8 = 2
	classFatal    int8 = 3
	classDead     int8 = 4
)

type eventState struct{ Newest, Oldest uint32 }

func decodeEventState(raw []byte) (eventState, error) {
	if err := need(raw, evtInfoMin, "event log state"); err != nil {
		return eventState{}, err
	}
	b := buf(raw)
	return eventState{Newest: b.u32(0), Oldest: b.u32(4)}, nil
}

type event struct {
	Sequence uint32
	Time     uint32
	Code     uint32
	Locale   uint16
	Class    int8
	Text     string
}

func decodeEvents(raw []byte) ([]event, error) {
	if err := need(raw, evtHeader, "event list"); err != nil {
		return nil, err
	}
	b := buf(raw)
	n := min(int(b.u32(0)), maxEvents)
	var out []event
	for i := 0; i < n && evtHeader+(i+1)*evtSize <= len(raw); i++ {
		o := evtHeader + i*evtSize
		out = append(out, event{
			Sequence: b.u32(o),
			Time:     b.u32(o + 4),
			Code:     b.u32(o + 8),
			Locale:   b.u16(o + 12),
			Class:    int8(b.u8(o + 15)), //nolint:gosec // class is a signed byte
			Text:     b.text(o+evtDescOff, evtDescSize),
		})
	}
	return out, nil
}

// className names an event class, or "unknown".
func className(c int8) string {
	switch c {
	case classDebug:
		return "debug"
	case classProgress:
		return "progress"
	case classInfo:
		return "info"
	case classWarning:
		return "warning"
	case classCritical:
		return "critical"
	case classFatal:
		return "fatal"
	case classDead:
		return "dead"
	}
	return "unknown"
}

// parseClass maps a min_class name to its class value.
func parseClass(name string) (int8, bool) {
	for c := classDebug; c <= classDead; c++ {
		if className(c) == name {
			return c, true
		}
	}
	return 0, false
}
