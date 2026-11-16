// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
)

// writeBundleDir writes the answers of a fake device in the format of cmd/megaraid-capture: one
// .bin file for each command that answered and a manifest.json.
func writeBundleDir(t *testing.T, dir string, f *fakeDevice) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o700))
	var m bundleManifest
	m.Label, m.HostNo, m.PCI = "synthetic", 0, "0000:03:00.0"
	add := func(op uint32, mb [mboxSize]byte, r response) {
		name := fmt.Sprintf("%s_%d", allowed[op], len(m.Commands))
		entry := struct {
			Name    string `json:"name"`
			Opcode  string `json:"opcode"`
			Mailbox string `json:"mailbox"`
			Status  int    `json:"status"`
			File    string `json:"file"`
		}{Name: name, Opcode: fmt.Sprintf("0x%08x", op), Status: int(r.status)}
		if !allZero(mb[:]) {
			entry.Mailbox = hex.EncodeToString(mb[:])
		}
		if r.status == 0 {
			entry.File = name + ".bin"
			require.NoError(t, os.WriteFile(filepath.Join(dir, entry.File), r.data, 0o600))
		}
		m.Commands = append(m.Commands, entry)
	}
	for op, r := range f.byOp {
		add(op, [mboxSize]byte{}, r)
	}
	for k, r := range f.byKey {
		var op uint32
		var mbHex string
		_, err := fmt.Sscanf(k, "%08x/%s", &op, &mbHex)
		require.NoError(t, err)
		raw, err := hex.DecodeString(mbHex)
		require.NoError(t, err)
		var mb [mboxSize]byte
		copy(mb[:], raw)
		add(op, mb, r)
	}
	body, err := json.MarshalIndent(m, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), body, 0o600))
}

func TestABundleLoadsAndScrapesLikeTheFakeItCameFrom(t *testing.T) {
	original := labFake(t)
	dir := filepath.Join(t.TempDir(), "synthetic-host0")
	writeBundleDir(t, dir, original)

	loaded, m := loadBundle(t, dir)
	s, _, _ := newTestScraper(t, testConfig(), loaded)
	md := s.scrape(context.Background()).metrics

	assert.Equal(t, "0000:03:00.0", m.PCI)
	assert.Equal(t, hwschema.StateOK, current(t, md, typeController, "0000:03:00.0"))
	assert.Equal(t, hwschema.StateOK, current(t, md, typeLogicalDisk, "0000:03:00.0/ld0"))
	assert.Equal(t, hwschema.StateOK, current(t, md, typePhysicalDisk, "ZC1AAAA1"))
	assert.Equal(t, hwschema.StateOK, current(t, md, typeBattery, "0000:03:00.0/bbu"))
}

// TestCommittedCaptureBundles runs the receiver against every bundle under testdata. The lab
// captures arrive from the capture sessions; until then the directory is empty and this skips.
func TestCommittedCaptureBundles(t *testing.T) {
	manifests, err := filepath.Glob(filepath.Join("testdata", "*", "manifest.json"))
	require.NoError(t, err)
	if len(manifests) == 0 {
		t.Skip("no capture bundle under testdata yet")
	}
	for _, mf := range manifests {
		dir := filepath.Dir(mf)
		t.Run(filepath.Base(dir), func(t *testing.T) {
			dev, m := loadBundle(t, dir)
			ctrl := controller{HostNo: m.HostNo, PCI: m.PCI}
			s, logs, _ := newTestScraper(t, testConfig(), dev, ctrl)

			md := s.scrape(context.Background()).metrics

			assert.NotZero(t, md.MetricCount(), "the bundle gives metrics")
			_, ok := find(md, metricCtrlInfo)
			assert.True(t, ok, "raid.controller.info")
			assert.Zero(t, logs.FilterMessage("Skipping the scrape of a MegaRAID controller").Len())
			for _, st := range []string{typeController, typeLogicalDisk, typePhysicalDisk} {
				seen := false
				for _, p := range points(md, metricStatus) {
					if p.attrs[hwschema.AttrType] == st {
						seen = true
					}
				}
				assert.True(t, seen, "a %s status", st)
			}
		})
	}
}
