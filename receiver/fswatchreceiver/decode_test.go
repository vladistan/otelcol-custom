// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package fswatchreceiver

import (
	"encoding/binary"
	"reflect"
	"testing"
	"unicode/utf16"
)

type notifyEntry struct {
	action uint32
	name   string
}

// notifyBuffer lays out FILE_NOTIFY_INFORMATION records the way the kernel does:
// each record starts on a 4-byte boundary and the last has NextEntryOffset 0.
func notifyBuffer(entries ...notifyEntry) []byte {
	var buf []byte
	for i, e := range entries {
		name := utf16.Encode([]rune(e.name))
		size := 12 + 2*len(name)
		padded := (size + 3) &^ 3
		rec := make([]byte, padded)
		if i < len(entries)-1 {
			binary.LittleEndian.PutUint32(rec[0:], uint32(padded))
		}
		binary.LittleEndian.PutUint32(rec[4:], e.action)
		binary.LittleEndian.PutUint32(rec[8:], uint32(2*len(name)))
		for j, u := range name {
			binary.LittleEndian.PutUint16(rec[12+2*j:], u)
		}
		buf = append(buf, rec...)
	}
	return buf
}

func TestDecodeNotifications(t *testing.T) {
	tests := []struct {
		name string
		buf  []byte
		want []rawEvent
	}{
		{"empty", nil, nil},
		{"one added", notifyBuffer(notifyEntry{1, `a.dx`}), []rawEvent{{actionAdded, `a.dx`}}},
		{
			"rename pair keeps order",
			notifyBuffer(notifyEntry{4, `~a.tmp`}, notifyEntry{5, `a.dx`}),
			[]rawEvent{{actionRenamedOld, `~a.tmp`}, {actionRenamedNew, `a.dx`}},
		},
		{
			"every action maps",
			notifyBuffer(notifyEntry{1, `a`}, notifyEntry{2, `b`}, notifyEntry{3, `c`}),
			[]rawEvent{{actionAdded, `a`}, {actionRemoved, `b`}, {actionModified, `c`}},
		},
		{"unknown action is kept as other", notifyBuffer(notifyEntry{99, `x`}), []rawEvent{{actionOther, `x`}}},
		{"nested name and non-ASCII", notifyBuffer(notifyEntry{1, "sub\\Ünï\\a.dx"}), []rawEvent{{actionAdded, "sub\\Ünï\\a.dx"}}},
		{"truncated header stops", []byte{1, 2, 3}, nil},
		{
			"name longer than the buffer stops",
			func() []byte {
				b := notifyBuffer(notifyEntry{1, `abcd`})
				binary.LittleEndian.PutUint32(b[8:], 400)
				return b
			}(),
			nil,
		},
		{
			"next offset past the buffer keeps the first record",
			func() []byte {
				b := notifyBuffer(notifyEntry{1, `ab`})
				binary.LittleEndian.PutUint32(b[0:], 4096)
				return b
			}(),
			[]rawEvent{{actionAdded, `ab`}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeNotifications(tt.buf)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
