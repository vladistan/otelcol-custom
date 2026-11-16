// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	version = "0.1.0"
	// maxEvents is the most events one EVENT_GET buffer of maxDataLen bytes holds.
	maxEvents = (maxDataLen - evtHeader) / evtSize
	// eventClassLocaleAll asks for every locale and, with class -2 (debug), every class.
	eventClassLocaleAll uint32 = 0xFE00FFFF

	ctrlInfoSize    = 2048
	hwPresentOff    = 1456
	hwPresentBBU    = 0x01
	pdQueryExposed  = 5 // MR_PD_QUERY_TYPE_EXPOSED_TO_HOST
	ldQueryAll      = 0
	maxDriveEntries = 240
	maxLDEntries    = 64
)

// captureOptions selects what one capture reads and how it is scrubbed.
type captureOptions struct {
	label string
	// events is how many of the newest controller events to read; zero or less reads none.
	events int
	scrub  *scrubber
}

type manifestEntry struct {
	Name    string `json:"name"`
	Opcode  string `json:"opcode"`
	Mailbox string `json:"mailbox,omitempty"`
	Status  int    `json:"status"`
	Bytes   int    `json:"bytes"`
	File    string `json:"file,omitempty"`
}

type skipEntry struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type manifest struct {
	Tool     string          `json:"tool"`
	Version  string          `json:"version"`
	Label    string          `json:"label"`
	HostNo   int             `json:"host_no"`
	PCI      string          `json:"pci,omitempty"` // the controller's PCI address is recorded as is
	Device   string          `json:"device,omitempty"`
	Scrubbed bool            `json:"scrubbed"`
	Commands []manifestEntry `json:"commands"`
	Skipped  []skipEntry     `json:"skipped,omitempty"`
}

// bundle is what one controller capture produced: scrubbed files and their manifest.
type bundle struct {
	files    map[string][]byte
	manifest manifest
}

func (b *bundle) dirName() string {
	return fmt.Sprintf("%s-host%d", b.manifest.Label, b.manifest.HostNo)
}

type session struct {
	dev device
	opt captureOptions
	b   *bundle
}

// run sends one request. A transport error ends the capture; a non-zero command status is
// recorded and the capture goes on, without a file for that command.
func (s *session) run(name, kind string, req request) (response, error) {
	resp, err := s.dev.exec(req)
	if err != nil {
		return response{}, fmt.Errorf("%s: %w", name, err)
	}
	entry := manifestEntry{
		Name:   name,
		Opcode: fmt.Sprintf("0x%08x", req.opcode),
		Status: int(resp.status),
	}
	if !allZero(req.mbox[:]) {
		entry.Mailbox = hex.EncodeToString(req.mbox[:])
	}
	if resp.status == 0 {
		data := resp.data
		if kind == kindCfgRead && len(data) >= 4 {
			if n := int(binary.LittleEndian.Uint32(data)); n >= cfgHeader && n <= len(data) {
				data = data[:n]
			}
		}
		if kind == kindEvents && len(data) >= evtHeader {
			n := int(min(binary.LittleEndian.Uint32(data), uint32(maxEvents)))
			data = data[:min(len(data), evtHeader+n*evtSize)]
		}
		entry.Bytes = len(data)
		entry.File = name + ".bin"
		s.b.files[entry.File] = s.opt.scrub.scrub(kind, data)
	}
	s.b.manifest.Commands = append(s.b.manifest.Commands, entry)
	return resp, nil
}

func (s *session) skip(name, reason string) {
	s.b.manifest.Skipped = append(s.b.manifest.Skipped, skipEntry{Name: name, Reason: reason})
}

func mbox(bytes ...byte) [mboxSize]byte {
	var m [mboxSize]byte
	copy(m[:], bytes)
	return m
}

// captureController reads the allow-listed state of one controller into a scrubbed bundle.
func captureController(dev device, hostNo int, opt captureOptions) (*bundle, error) {
	s := &session{dev: dev, opt: opt, b: &bundle{files: map[string][]byte{}, manifest: manifest{
		Tool: "megaraid-capture", Version: version, Label: opt.label, HostNo: hostNo, Scrubbed: true,
	}}}

	info, err := s.run("ctrl_getinfo", kindCtrlInfo, request{opcode: opCtrlGetInfo, dataLen: ctrlInfoSize})
	if err != nil {
		return nil, err
	}
	if info.status != 0 {
		return nil, fmt.Errorf("ctrl_getinfo: the controller answered status %d", info.status)
	}
	hasBBU := len(info.data) >= hwPresentOff+4 && binary.LittleEndian.Uint32(info.data[hwPresentOff:])&hwPresentBBU != 0

	steps := []struct {
		name, kind string
		req        request
	}{
		{"ctrl_get_props", "", request{opcode: opCtrlGetProps, dataLen: 64}},
		{"pr_get_status", "", request{opcode: opPRGetStatus, dataLen: 16}},
		{"pr_get_properties", "", request{opcode: opPRGetProperties, dataLen: 208}},
	}
	for _, st := range steps {
		if _, err := s.run(st.name, st.kind, st.req); err != nil {
			return nil, err
		}
	}

	pdList, err := s.run("pd_get_list", kindPDList, request{opcode: opPDGetList, dataLen: 5768})
	if err != nil {
		return nil, err
	}
	if _, err := s.run("pd_list_query", kindPDList, request{opcode: opPDListQuery, mbox: mbox(pdQueryExposed), dataLen: 5768}); err != nil {
		return nil, err
	}
	ldList, err := s.run("ld_get_list", "", request{opcode: opLDGetList, dataLen: 1032})
	if err != nil {
		return nil, err
	}
	if _, err := s.run("ld_list_query", "", request{opcode: opLDListQuery, mbox: mbox(ldQueryAll), dataLen: 512}); err != nil {
		return nil, err
	}
	if _, err := s.run("cfg_read", kindCfgRead, request{opcode: opCfgRead, dataLen: maxDataLen}); err != nil {
		return nil, err
	}

	if pdList.status == 0 {
		for _, id := range parseDriveIDs(pdList.data) {
			req := request{opcode: opPDGetInfo, mbox: mbox(byte(id), byte(id>>8)), dataLen: 512}
			if _, err := s.run(fmt.Sprintf("pd_get_info_%d", id), kindPDInfo, req); err != nil {
				return nil, err
			}
		}
	}
	if ldList.status == 0 {
		for _, target := range parseLDTargets(ldList.data) {
			for _, st := range []struct {
				name, kind string
				op         uint32
				size       int
			}{
				{fmt.Sprintf("ld_get_info_%d", target), kindLDInfo, opLDGetInfo, 384},
				{fmt.Sprintf("ld_get_prop_%d", target), kindLDProps, opLDGetProp, 32},
			} {
				if _, err := s.run(st.name, st.kind, request{opcode: st.op, mbox: mbox(target), dataLen: st.size}); err != nil {
					return nil, err
				}
			}
		}
	}

	if err := s.battery(hasBBU); err != nil {
		return nil, err
	}
	if err := s.events(); err != nil {
		return nil, err
	}
	return s.b, nil
}

func (s *session) battery(present bool) error {
	if !present {
		s.skip("bbu", "no battery reported by the controller; no battery command is sent")
		return nil
	}
	for _, st := range []struct {
		name, kind string
		op         uint32
		size       int
	}{
		{"bbu_get_status", "", opBBUGetStatus, 64},
		{"bbu_get_capacity_info", "", opBBUGetCapacityInfo, 48},
		{"bbu_get_design_info", kindBBUDesign, opBBUGetDesignInfo, 67},
		{"bbu_get_prop", "", opBBUGetProp, 256},
	} {
		if _, err := s.run(st.name, st.kind, request{opcode: st.op, dataLen: st.size}); err != nil {
			return err
		}
	}
	return nil
}

// events reads the event log state and then the newest events, at most opt.events of them.
func (s *session) events() error {
	state, err := s.run("event_get_info", "", request{opcode: opEventGetInfo, dataLen: 20})
	if err != nil {
		return err
	}
	if s.opt.events <= 0 || state.status != 0 || len(state.data) < 8 {
		return nil
	}
	count := min(s.opt.events, maxEvents)
	newest, oldest := binary.LittleEndian.Uint32(state.data[0:]), binary.LittleEndian.Uint32(state.data[4:])
	start := oldest
	if uint64(newest) >= uint64(count) && newest-uint32(count)+1 > oldest { //nolint:gosec // count is bounded above
		start = newest - uint32(count) + 1 //nolint:gosec // count is bounded above
	}
	var m [mboxSize]byte
	binary.LittleEndian.PutUint32(m[0:], start)
	binary.LittleEndian.PutUint32(m[4:], eventClassLocaleAll)
	_, err = s.run("event_get", kindEvents, request{opcode: opEventGet, mbox: m, dataLen: evtHeader + evtSize*count})
	return err
}

// parseDriveIDs returns the device ids of a drive list, ignoring entries beyond the buffer.
func parseDriveIDs(b []byte) []uint16 {
	if len(b) < pdListHeader {
		return nil
	}
	n := int(min(binary.LittleEndian.Uint32(b[4:]), maxDriveEntries))
	var ids []uint16
	seen := map[uint16]bool{}
	for i := 0; i < n && pdListHeader+(i+1)*pdEntrySize <= len(b); i++ {
		id := binary.LittleEndian.Uint16(b[pdListHeader+i*pdEntrySize:])
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// parseLDTargets returns the target ids of a logical drive list.
func parseLDTargets(b []byte) []byte {
	if len(b) < 8 {
		return nil
	}
	n := int(min(binary.LittleEndian.Uint32(b[0:]), maxLDEntries))
	var out []byte
	seen := map[byte]bool{}
	for i := 0; i < n && 8+(i+1)*16 <= len(b); i++ {
		id := b[8+i*16]
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// writeBundle writes the files and the manifest of a bundle under root/<label>-host<N>. It
// refuses a directory that exists and an output root that is a symbolic link, and the files are
// readable by the owner only.
func writeBundle(root string, b *bundle) (string, error) {
	dir := filepath.Join(root, b.dirName())
	if fi, err := os.Lstat(root); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("output directory %s is a symbolic link; refusing to write through it", root)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	for name, data := range b.files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return "", err
		}
	}
	body, err := json.MarshalIndent(b.manifest, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), append(body, '\n'), 0o600); err != nil {
		return "", err
	}
	return dir, nil
}

// kindOf maps a bundle file name to its buffer kind; "" means no field checks.
func kindOf(name string) string {
	base := strings.TrimSuffix(name, ".bin")
	switch {
	case strings.HasPrefix(base, "pd_get_info_"):
		return kindPDInfo
	case strings.HasPrefix(base, "ld_get_info_"):
		return kindLDInfo
	case strings.HasPrefix(base, "ld_get_prop_"):
		return kindLDProps
	}
	switch base {
	case "ctrl_getinfo", "cfg_read", "bbu_get_design_info", "event_get", "pd_get_list":
		return base
	case "pd_list_query":
		return kindPDList
	}
	return ""
}

// verifyDir checks every file of a bundle directory and returns what is still identifying.
func verifyDir(dir string) []string {
	var problems []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{err.Error()}
	}
	hasManifest := false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if e.Name() == "manifest.json" {
			hasManifest = true
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		for _, p := range verifyBuffer(kindOf(e.Name()), data) {
			problems = append(problems, e.Name()+": "+p)
		}
	}
	if !hasManifest {
		problems = append(problems, "manifest.json is missing")
	}
	return problems
}
