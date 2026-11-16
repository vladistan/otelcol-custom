// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/binary"
	"fmt"
)

// The values below are invented; they stand for what a controller would report.
const (
	rawCtrlSerial = "SV12345678"
	rawProduct    = "PERC H730 Mini"
	rawHostname   = "labhost-7"
	rawLDName     = "labhost-7-vd0"
	rawMAC        = "00:25:90:ab:cd:ef"
	rawWWN        = "5000c500a1b2c3d4"
	rawDriveSN0   = "ZA1B2C3D4E5F"
	rawDriveSN1   = "WD-WCC4N0XYZ123"
	rawBBUSerial  = 0x4D2B
	rawEventArgs  = "argstext-owner-lab"
)

// rawNAA is an invented 16-byte NAA type-6 volume id; its first nibble is 6.
var rawNAA = []byte{0x60, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0x10, 0x32, 0x54, 0x76, 0x98, 0xba, 0xdc}

var rawSAS = [][8]byte{
	{0x50, 0x00, 0xc5, 0x00, 0xa1, 0xb2, 0xc3, 0xd4},
	{0x50, 0x01, 0x4e, 0xe2, 0x07, 0x65, 0x43, 0x21},
}

func putText(b []byte, off int, s string) { copy(b[off:], s) }

func ctrlInfoBuf(serial string, bbu bool) []byte {
	b := make([]byte, 2048)
	putText(b, 1344, rawProduct)
	putText(b, 1424, serial)
	copy(b[40:], rawSAS[0][:])
	copy(b[112:], rawSAS[0][:])
	copy(b[120:], rawSAS[1][:])
	if bbu {
		binary.LittleEndian.PutUint32(b[1456:], 1)
	}
	binary.LittleEndian.PutUint32(b[1504:], 0x1fff)
	putText(b, 1600, "23.34.0-0017")
	return b
}

func pdListBuf(ids []uint16) []byte {
	b := make([]byte, 5768)
	binary.LittleEndian.PutUint32(b[0:], 5768)
	binary.LittleEndian.PutUint32(b[4:], uint32(len(ids)))
	for i, id := range ids {
		e := b[8+24*i:]
		binary.LittleEndian.PutUint16(e[0:], id)
		binary.LittleEndian.PutUint16(e[2:], 252)
		e[4], e[5] = 1, byte(i)
		copy(e[8:], rawSAS[i%len(rawSAS)][:])
	}
	return b
}

func pdInfoBuf(id uint16, serial string) []byte {
	b := make([]byte, 512)
	binary.LittleEndian.PutUint16(b[0:], id)
	putText(b, 4+8, "SEAGATE ")
	putText(b, 4+16, "ST9300653SS     ")
	putText(b, 4+32, "0004")
	putText(b, 4+36, serial)
	putText(b, 100+4, rawWWN)
	binary.LittleEndian.PutUint32(b[168:], 3) // media errors
	binary.LittleEndian.PutUint16(b[186:], 0x18)
	b[192] = 1
	copy(b[200:], rawSAS[0][:])
	copy(b[208:], rawSAS[1][:])
	putText(b, 294+4, rawWWN)
	return b
}

func ldListBuf(targets []byte) []byte {
	b := make([]byte, 1032)
	binary.LittleEndian.PutUint32(b[0:], uint32(len(targets)))
	for i, id := range targets {
		e := b[8+16*i:]
		e[0] = id
		e[4] = 3
		binary.LittleEndian.PutUint64(e[8:], 1<<30)
	}
	return b
}

func ldInfoBuf(name string) []byte {
	b := make([]byte, 384)
	putText(b, 4, name)
	b[4+16+3] = 3
	copy(b[304:], []byte{0, 0x83, 0, 20, 1, 3, 0, 16})
	copy(b[312:], rawNAA)
	return b
}

func ldPropsBuf(name string) []byte {
	b := make([]byte, 32)
	putText(b, 4, name)
	return b
}

func cfgReadBuf(name string) []byte {
	const arraySize, ldSize = 64, 256
	b := make([]byte, 32+arraySize+ldSize)
	binary.LittleEndian.PutUint32(b[0:], uint32(len(b)))
	binary.LittleEndian.PutUint16(b[4:], 1)
	binary.LittleEndian.PutUint16(b[6:], arraySize)
	binary.LittleEndian.PutUint16(b[8:], 1)
	binary.LittleEndian.PutUint16(b[10:], ldSize)
	putText(b, 32+arraySize+4, name)
	return b
}

func bbuDesignBuf() []byte {
	b := make([]byte, 67)
	binary.LittleEndian.PutUint16(b[10:], rawBBUSerial)
	putText(b, 14, "LSI")
	putText(b, 42, "20140921")
	return b
}

func eventInfoBuf(newest, oldest uint32) []byte {
	b := make([]byte, 20)
	binary.LittleEndian.PutUint32(b[0:], newest)
	binary.LittleEndian.PutUint32(b[4:], oldest)
	return b
}

func eventListBuf(start uint32, texts []string) []byte {
	b := make([]byte, 8+256*len(texts))
	binary.LittleEndian.PutUint32(b[0:], uint32(len(texts)))
	for i, text := range texts {
		e := b[8+256*i:]
		binary.LittleEndian.PutUint32(e[0:], start+uint32(i))
		binary.LittleEndian.PutUint32(e[8:], 0x62)
		putText(e, 32, rawEventArgs)
		putText(e, 128, text)
	}
	return b
}

func eventTexts() []string {
	return []string{
		fmt.Sprintf("PD 08(e0xfc/s1) Path %s reset (Type 03)", rawWWN),
		fmt.Sprintf("Host %s: NIC %s up; ctrl SN %s", rawHostname, rawMAC, rawCtrlSerial),
		fmt.Sprintf("Removed: PD 09 SN %s", rawDriveSN0),
	}
}

// sensitiveValues lists every raw value of the fixtures that must not reach a bundle.
func sensitiveValues() [][]byte {
	values := [][]byte{
		[]byte(rawCtrlSerial), []byte(rawHostname), []byte(rawLDName), []byte(rawMAC),
		[]byte(rawWWN), []byte(rawDriveSN0), []byte(rawDriveSN1), []byte(rawEventArgs),
	}
	for _, a := range rawSAS {
		values = append(values, append([]byte(nil), a[:]...))
	}
	return values
}
