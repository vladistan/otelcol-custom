// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartdecode

import "encoding/binary"

// putATAString writes s into the identify words first..last, two characters a word with the first
// character in the high byte, padded with spaces.
func putATAString(buf []byte, firstWord, lastWord int, s string) {
	n := (lastWord - firstWord + 1) * 2
	padded := []byte(s)
	for len(padded) < n {
		padded = append(padded, ' ')
	}
	for i := 0; i < n; i += 2 {
		buf[firstWord*2+i] = padded[i+1]
		buf[firstWord*2+i+1] = padded[i]
	}
}

func putWord(buf []byte, word int, v uint16) { binary.LittleEndian.PutUint16(buf[word*2:], v) }

type identifySpec struct {
	model, serial, firmware string
	sectors                 uint64
	rotation                uint16
	wwn                     uint64
	smartSupported          bool
	smartEnabled            bool
	logicalSectorBytes      int
}

// identifyBuffer builds a 512 byte IDENTIFY DEVICE answer.
func identifyBuffer(s identifySpec) []byte {
	buf := make([]byte, 512)
	putATAString(buf, 27, 46, s.model)
	putATAString(buf, 10, 19, s.serial)
	putATAString(buf, 23, 26, s.firmware)
	putWord(buf, 83, 1<<14|1<<10) // valid, LBA48
	binary.LittleEndian.PutUint64(buf[100*2:], s.sectors)
	putWord(buf, 217, s.rotation)
	if s.wwn != 0 {
		putWord(buf, 87, 1<<14|1<<8)
		putWord(buf, 108, uint16(s.wwn>>48))
		putWord(buf, 109, uint16(s.wwn>>32))
		putWord(buf, 110, uint16(s.wwn>>16))
		putWord(buf, 111, uint16(s.wwn))
	}
	if s.smartSupported {
		putWord(buf, 82, 1)
	}
	if s.smartEnabled {
		putWord(buf, 85, 1)
	}
	if s.logicalSectorBytes > 512 {
		putWord(buf, 106, 1<<14|1<<12)
		binary.LittleEndian.PutUint32(buf[117*2:], uint32(s.logicalSectorBytes/2))
	}
	return buf
}

type rawAttr struct {
	id                      uint8
	value, worst, threshold uint8
	raw                     [6]byte
}

// smartDataBuffer builds the 512 byte SMART READ DATA answer.
func smartDataBuffer(attrs []rawAttr) []byte {
	buf := make([]byte, 512)
	for i, a := range attrs {
		off := 2 + 12*i
		buf[off] = a.id
		buf[off+3] = a.value
		buf[off+4] = a.worst
		copy(buf[off+5:off+11], a.raw[:])
	}
	return buf
}

// thresholdBuffer builds the 512 byte SMART READ THRESHOLDS answer.
func thresholdBuffer(attrs []rawAttr) []byte {
	buf := make([]byte, 512)
	for i, a := range attrs {
		off := 2 + 12*i
		buf[off] = a.id
		buf[off+1] = a.threshold
	}
	return buf
}

func rawNumber(n uint64) [6]byte {
	var out [6]byte
	for i := range out {
		out[i] = byte(n >> (8 * i))
	}
	return out
}

// nvmeSmartBuffer builds a 512 byte SMART/Health log page.
type nvmeSmartSpec struct {
	criticalWarning byte
	compositeKelvin uint16
	percentageUsed  byte
	availableSpare  byte
	spareThreshold  byte
	unitsRead       uint64
	unitsWritten    uint64
	powerCycles     uint64
	powerOnHours    uint64
	unsafeShutdowns uint64
	mediaErrors     uint64
	sensorsKelvin   [8]uint16
}

func nvmeSmartBuffer(s nvmeSmartSpec) []byte {
	buf := make([]byte, 512)
	buf[0] = s.criticalWarning
	binary.LittleEndian.PutUint16(buf[1:], s.compositeKelvin)
	buf[3] = s.availableSpare
	buf[4] = s.spareThreshold
	buf[5] = s.percentageUsed
	binary.LittleEndian.PutUint64(buf[32:], s.unitsRead)
	binary.LittleEndian.PutUint64(buf[48:], s.unitsWritten)
	binary.LittleEndian.PutUint64(buf[112:], s.powerCycles)
	binary.LittleEndian.PutUint64(buf[128:], s.powerOnHours)
	binary.LittleEndian.PutUint64(buf[144:], s.unsafeShutdowns)
	binary.LittleEndian.PutUint64(buf[160:], s.mediaErrors)
	for i, k := range s.sensorsKelvin {
		binary.LittleEndian.PutUint16(buf[200+2*i:], k)
	}
	return buf
}

// nvmeIdentifyBuffer builds a 4096 byte Identify Controller answer.
func nvmeIdentifyBuffer(model, serial, firmware string, capacity uint64) []byte {
	buf := make([]byte, 4096)
	copyPadded(buf[4:24], serial)
	copyPadded(buf[24:64], model)
	copyPadded(buf[64:72], firmware)
	binary.LittleEndian.PutUint64(buf[280:], capacity)
	return buf
}

func copyPadded(dst []byte, s string) {
	for i := range dst {
		dst[i] = ' '
	}
	copy(dst, s)
}
