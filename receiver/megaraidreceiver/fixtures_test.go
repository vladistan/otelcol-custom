// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// The buffers below are built from the struct layouts of mfireg.h (see decode.go), so every
// decoder test runs without a controller. Real capture bundles load through loadBundle.

func put16(b []byte, o int, v uint16) { binary.LittleEndian.PutUint16(b[o:], v) }
func put32(b []byte, o int, v uint32) { binary.LittleEndian.PutUint32(b[o:], v) }
func put64(b []byte, o int, v uint64) { binary.LittleEndian.PutUint64(b[o:], v) }

func putText(b []byte, o int, s string) { copy(b[o:], s) }

type ctrlOpts struct {
	device                          uint16
	product, serial, fw             string
	memMB                           uint16
	maxLDs                          uint8
	bbu                             bool
	ldOffline, ldDegraded           uint16
	pdPresent, pdPredFail, pdFailed uint16
	roc, board                      uint8
}

func ctrlBuf(o ctrlOpts) []byte {
	b := make([]byte, ctrlInfoRequestLen)
	put16(b, ctrlPCIVendorOff, pciVendorBroadcom)
	put16(b, ctrlPCIDeviceOff, o.device)
	put32(b, ctrlImageCountOff, 2)
	putText(b, ctrlImageOff, "BOOT")
	putText(b, ctrlImageOff+8, "boot-1.0")
	putText(b, ctrlImageOff+ctrlImageSize, "APP")
	putText(b, ctrlImageOff+ctrlImageSize+8, o.fw)
	b[ctrlMaxLDsOff] = o.maxLDs
	putText(b, ctrlProductOff, o.product)
	putText(b, ctrlSerialOff, o.serial)
	if o.bbu {
		put32(b, ctrlHWPresentOff, hwPresentBBU)
	}
	put16(b, ctrlLDOfflineOff, o.ldOffline)
	put16(b, ctrlLDDegradedOff, o.ldDegraded)
	put16(b, ctrlPDPresentOff, o.pdPresent)
	put16(b, ctrlPDPredFailOff, o.pdPredFail)
	put16(b, ctrlPDFailedOff, o.pdFailed)
	put16(b, ctrlMemorySizeOff, o.memMB)
	b[ctrlTempROCOff] = o.roc
	b[ctrlTempBoardOff] = o.board
	return b
}

func pdListBuf(entries ...pdAddr) []byte {
	b := make([]byte, pdListReqLen)
	put32(b, 0, uint32(pdListHeader+len(entries)*pdEntrySize))
	put32(b, 4, uint32(len(entries)))
	for i, e := range entries {
		o := pdListHeader + i*pdEntrySize
		put16(b, o, e.DeviceID)
		put16(b, o+2, e.EnclID)
		b[o+4], b[o+5], b[o+6] = e.EnclIndex, e.Slot, e.SCSIType
	}
	return b
}

func ldListBuf(targets ...uint8) []byte {
	b := make([]byte, ldListReqLen)
	put32(b, 0, uint32(len(targets)))
	for i, t := range targets {
		b[ldListHeader+i*ldEntrySize] = t
	}
	return b
}

func ldQueryBuf(targets ...uint8) []byte {
	b := make([]byte, ldQueryReqLen)
	put32(b, 4, uint32(len(targets)))
	copy(b[ldQueryHeader:], targets)
	return b
}

type ldSpec struct {
	target           uint8
	name             string
	level, spans     uint8
	stripeLog2       uint8
	drives           uint8
	state            uint8
	cache            uint8
	blocks           uint64
	checkPct, bgiPct uint16 // 0 = not running
	reconPct         uint16
}

func ldInfoBuf(s ldSpec) []byte {
	b := make([]byte, ldInfoReqLen)
	b[ldTargetOff] = s.target
	putText(b, ldNameOff, s.name)
	b[ldCurCacheOff] = s.cache
	b[ldLevelOff] = s.level
	b[ldStripeOff] = s.stripeLog2
	b[ldDrivesOff] = s.drives
	b[ldSpanDepthOff] = s.spans
	b[ldStateOff] = s.state
	b[ldConsistentOff] = 1
	put64(b, ldSizeOff, s.blocks)
	var active uint32
	for i, pct := range []uint16{s.checkPct, s.bgiPct, 0, s.reconPct} {
		if pct != 0 {
			active |= 1 << i
			put16(b, ldProgressOff+4+4*i, pct)
		}
	}
	put32(b, ldProgressOff, active)
	return b
}

func ldPropBuf(s ldSpec) []byte {
	b := make([]byte, ldPropReqLen)
	b[ldTargetOff] = s.target
	putText(b, ldNameOff, s.name)
	b[ldCurCacheOff] = s.cache
	return b
}

type pdSpec struct {
	id                      uint16
	vendor, product, serial string
	fw                      uint16
	media, other, pred      uint32
	blocks                  uint64
	encl                    uint16
	slot                    uint8
	temp                    uint8
	rebuildPct, patrolPct   uint16
}

func pdInfoBuf(s pdSpec) []byte {
	b := make([]byte, pdInfoReqLen)
	put16(b, 0, s.id)
	putText(b, pdVendorOff, fmt.Sprintf("%-8s", s.vendor))
	putText(b, pdProductOff, fmt.Sprintf("%-16s", s.product))
	putText(b, pdSerialOff, s.serial)
	put32(b, pdMediaErrOff, s.media)
	put32(b, pdOtherErrOff, s.other)
	put32(b, pdPredFailOff, s.pred)
	put16(b, pdFWStateOff, s.fw)
	put64(b, pdCoercedOff, s.blocks)
	put16(b, pdEnclIDOff, s.encl)
	b[pdSlotOff] = s.slot
	b[pdTemperatureOff] = s.temp
	var active uint32
	if s.rebuildPct != 0 {
		active |= 1
		put16(b, pdProgressOff+4, s.rebuildPct)
	}
	if s.patrolPct != 0 {
		active |= 2
		put16(b, pdProgressOff+8, s.patrolPct)
	}
	put32(b, pdProgressOff, active)
	return b
}

type bbuSpec struct {
	status              uint32
	voltage, temp       uint16
	pct, cycles, runMin uint16
	nextLearn           uint32
}

func bbuBuffers(s bbuSpec) (status, capacity, props []byte) {
	status = make([]byte, bbuStatusLen)
	status[0] = 1
	put16(status, 2, s.voltage)
	put16(status, 6, s.temp)
	put32(status, 8, s.status)
	capacity = make([]byte, bbuCapacityLen)
	put16(capacity, 0, s.pct)
	put16(capacity, 8, s.runMin)
	put16(capacity, 14, s.cycles)
	props = make([]byte, bbuPropLen)
	put32(props, 4, s.nextLearn)
	return status, capacity, props
}

func evtStateBuf(newest, oldest uint32) []byte {
	b := make([]byte, evtInfoLen)
	put32(b, 0, newest)
	put32(b, 4, oldest)
	return b
}

func evtListBuf(events ...event) []byte {
	b := make([]byte, evtHeader+evtSize*len(events))
	put32(b, 0, uint32(len(events)))
	for i, e := range events {
		o := evtHeader + i*evtSize
		put32(b, o, e.Sequence)
		put32(b, o+4, e.Time)
		put32(b, o+8, e.Code)
		put16(b, o+12, e.Locale)
		b[o+15] = byte(e.Class)
		putText(b, o+evtDescOff, e.Text)
	}
	return b
}

// fakeDevice answers DCMDs from a table and records what it was sent. It fails the test when it is
// sent an opcode outside the allow-list.
type fakeDevice struct {
	t       *testing.T
	mu      sync.Mutex
	byKey   map[string]response
	byOp    map[uint32]response
	dynamic map[uint32]func(request) (response, error)
	fail    map[uint32]error
	sent    []request
	passes  []passRequest
	passFn  func(passRequest) (response, error)
}

func newFake(t *testing.T) *fakeDevice {
	return &fakeDevice{t: t, byKey: map[string]response{}, byOp: map[uint32]response{}, dynamic: map[uint32]func(request) (response, error){}, fail: map[uint32]error{}}
}

func key(op uint32, mb [mboxSize]byte) string { return fmt.Sprintf("%08x/%x", op, mb) }

// set answers an opcode for any mailbox; setMbox answers one mailbox only.
func (f *fakeDevice) set(op uint32, data []byte) { f.byOp[op] = response{data: data} }
func (f *fakeDevice) setStatus(op uint32, status uint8) {
	f.byOp[op] = response{status: status}
}
func (f *fakeDevice) setMbox(op uint32, mb [mboxSize]byte, data []byte) {
	f.byKey[key(op, mb)] = response{data: data}
}

func (f *fakeDevice) exec(req request) (response, error) {
	f.mu.Lock()
	if _, ok := allowed[req.opcode]; !ok {
		f.t.Errorf("opcode %#08x is outside the allow-list", req.opcode)
	}
	f.sent = append(f.sent, req)
	fn := f.dynamic[req.opcode]
	f.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail[req.opcode]; err != nil {
		return response{}, err
	}
	if r, ok := f.byKey[key(req.opcode, req.mbox)]; ok {
		return r, nil
	}
	if r, ok := f.byOp[req.opcode]; ok {
		return r, nil
	}
	return response{status: statusInvalidDCMD}, nil
}

func (f *fakeDevice) pass(req passRequest) (response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.passes = append(f.passes, req)
	if f.passFn == nil {
		return response{status: statusDeviceMissing}, nil
	}
	return f.passFn(req)
}

func (f *fakeDevice) count(op uint32) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.sent {
		if r.opcode == op {
			n++
		}
	}
	return n
}

// labFake is a 2208 controller with a RAID 5 logical drive, two SATA drives, a SAS drive and a battery.
func labFake(t *testing.T) *fakeDevice {
	f := newFake(t)
	f.set(opCtrlGetInfo, ctrlBuf(ctrlOpts{
		device: pciDevice2208, product: "PERC H710P Mini", serial: "CONTROLLER0001", fw: "3.130.05-1234", memMB: 1024,
		maxLDs: 64, bbu: true, pdPresent: 3, roc: 61, board: 44,
	}))
	f.set(opPDGetList, pdListBuf(
		pdAddr{DeviceID: 4, EnclID: 32, Slot: 0},
		pdAddr{DeviceID: 5, EnclID: 32, Slot: 1},
		pdAddr{DeviceID: 6, EnclID: 32, Slot: 2},
		pdAddr{DeviceID: 32, EnclID: 32, SCSIType: 0x0d},
	))
	f.set(opLDGetList, ldListBuf(0))
	ld := ldSpec{target: 0, name: "data", level: 5, spans: 1, stripeLog2: 7, drives: 3, state: ldStateOptimal, cache: 0x03, blocks: 1 << 21}
	f.setMbox(opLDGetInfo, mailbox(0), ldInfoBuf(ld))
	f.setMbox(opLDGetProp, mailbox(0), ldPropBuf(ld))
	for _, p := range []pdSpec{
		{id: 4, vendor: "ATA", product: "ST4000NM0033", serial: "ZC1AAAA1", fw: pdOnline, media: 2, blocks: 1 << 22, encl: 32, slot: 0, temp: 34},
		{id: 5, vendor: "ATA", product: "ST4000NM0033", serial: "ZC1AAAA2", fw: pdOnline, blocks: 1 << 22, encl: 32, slot: 1, temp: 35},
		{id: 6, vendor: "SEAGATE", product: "ST300MM0006", serial: "S0M1BBB3", fw: pdOnline, blocks: 1 << 20, encl: 32, slot: 2, temp: 38},
	} {
		f.setMbox(opPDGetInfo, mailbox(byte(p.id), byte(p.id>>8)), pdInfoBuf(p))
	}
	st, capa, props := bbuBuffers(bbuSpec{status: 0, voltage: 4050, temp: 30, pct: 97, cycles: 41, runMin: 600, nextLearn: 600000000})
	f.set(opBBUGetStatus, st)
	f.set(opBBUGetCapacityInfo, capa)
	f.set(opBBUGetProp, props)
	return f
}

// bundle is a capture bundle written by cmd/megaraid-capture: manifest.json and one file for each
// command that answered.
type bundleManifest struct {
	Label    string `json:"label"`
	HostNo   int    `json:"host_no"`
	PCI      string `json:"pci"`
	Commands []struct {
		Name    string `json:"name"`
		Opcode  string `json:"opcode"`
		Mailbox string `json:"mailbox"`
		Status  int    `json:"status"`
		File    string `json:"file"`
	} `json:"commands"`
}

// loadBundle reads a capture bundle directory into a fake device that answers by opcode and mailbox.
func loadBundle(t *testing.T, dir string) (*fakeDevice, bundleManifest) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	require.NoError(t, err)
	var m bundleManifest
	require.NoError(t, json.Unmarshal(body, &m))
	f := newFake(t)
	for _, c := range m.Commands {
		op, err := strconv.ParseUint(strings.TrimPrefix(c.Opcode, "0x"), 16, 32)
		require.NoError(t, err)
		var mb [mboxSize]byte
		if c.Mailbox != "" {
			raw, err := hex.DecodeString(c.Mailbox)
			require.NoError(t, err)
			copy(mb[:], raw)
		}
		var data []byte
		if c.File != "" {
			data, err = os.ReadFile(filepath.Join(dir, c.File))
			require.NoError(t, err)
		}
		f.byKey[key(uint32(op), mb)] = response{status: uint8(c.Status), data: data}
		if _, ok := f.byOp[uint32(op)]; !ok {
			f.byOp[uint32(op)] = response{status: uint8(c.Status), data: data}
		}
	}
	return f, m
}
