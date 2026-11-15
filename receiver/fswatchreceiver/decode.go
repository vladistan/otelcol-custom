// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package fswatchreceiver

import (
	"encoding/binary"
	"unicode/utf16"
)

type action int

const (
	actionOther action = iota
	actionAdded
	actionRemoved
	actionModified
	actionRenamedOld
	actionRenamedNew
)

type rawEvent struct {
	action action
	name   string
}

// watchMsg is one read of a directory watch: its events, an overflow marker, or the error that ended it.
type watchMsg struct {
	events   []rawEvent
	overflow bool
	err      error
}

const notifyHeader = 12

// decodeNotifications reads the FILE_NOTIFY_INFORMATION records of one ReadDirectoryChangesW result.
// A record that does not fit the buffer ends the list.
func decodeNotifications(buf []byte) []rawEvent {
	var out []rawEvent
	offset := 0
	for offset+notifyHeader <= len(buf) {
		rec := buf[offset:]
		next := int(binary.LittleEndian.Uint32(rec[0:]))
		act := action(binary.LittleEndian.Uint32(rec[4:]))
		nameLen := int(binary.LittleEndian.Uint32(rec[8:]))
		if nameLen%2 != 0 || notifyHeader+nameLen > len(rec) {
			break
		}
		units := make([]uint16, nameLen/2)
		for i := range units {
			units[i] = binary.LittleEndian.Uint16(rec[notifyHeader+2*i:])
		}
		if act < actionAdded || act > actionRenamedNew {
			act = actionOther
		}
		out = append(out, rawEvent{action: act, name: string(utf16.Decode(units))})
		if next == 0 {
			break
		}
		offset += next
	}
	return out
}
