// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ipmireceiver

import (
	"errors"
	"strings"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
)

// FRU decoding per the IPMI Platform Management FRU Information Storage
// Definition. Every offset and length is checked against the image.

const (
	fruMaxBytes    = 4096 // largest image read
	fruChunk       = 16   // bytes asked of one Read FRU Data request
	fruMaxRequests = 2 * (fruMaxBytes/fruChunk + 8)
	fruMaxFields   = 32
	fruHeaderSize  = 8
	fruMaxText     = 128
)

var errFRUCorrupt = errors.New("FRU data is damaged or short")

// errFRUUnsupported marks a FRU device the receiver does not read (word access, or a format it does
// not decode). It is not a failure of the FRU source.
var errFRUUnsupported = errors.New("FRU device is not supported")

// fruRecord holds the decoded areas; an area that is absent or damaged is nil.
type fruRecord struct {
	board, product, chassis map[string]string
}

// fruReader makes one Read FRU Data request and may return fewer bytes than asked.
type fruReader func(offset, count int) ([]byte, error)

// validHeader checks the common header: version 1 and a zero checksum.
func validHeader(h []byte) bool {
	if len(h) < fruHeaderSize || h[0]&0x0F != 1 {
		return false
	}
	var sum byte
	for _, b := range h[:fruHeaderSize] {
		sum += b
	}
	return sum == 0
}

// readFRUImage reads the part of the FRU that holds the chassis, board and
// product areas: the header first, then the length of each area, then the bytes up to the end of the last.
func readFRUImage(size int, rd fruReader) ([]byte, error) {
	if size < fruHeaderSize {
		return nil, errFRUCorrupt
	}
	size = min(size, fruMaxBytes)
	requests := 0
	readFull := func(off, n int) ([]byte, error) {
		out := make([]byte, 0, n)
		for len(out) < n {
			if requests++; requests > fruMaxRequests {
				return nil, errFRUCorrupt
			}
			chunk, err := rd(off+len(out), min(fruChunk, n-len(out)))
			if err != nil {
				return nil, err
			}
			if len(chunk) == 0 {
				return nil, errFRUCorrupt
			}
			out = append(out, chunk[:min(len(chunk), n-len(out))]...)
		}
		return out, nil
	}
	hdr, err := readFull(0, fruHeaderSize)
	if err != nil {
		return nil, err
	}
	if !validHeader(hdr) {
		return nil, errFRUCorrupt
	}
	need := fruHeaderSize
	for _, idx := range []int{2, 3, 4} {
		off := int(hdr[idx]) * 8
		if off < fruHeaderSize || off+2 > size {
			continue
		}
		a, err := readFull(off, 2)
		if err != nil {
			return nil, err
		}
		if end := off + int(a[1])*8; end <= size {
			need = max(need, end)
		}
	}
	return readFull(0, need)
}

// decodeFRU reads the chassis, board and product areas of an image. A bad
// common header is an error; a bad area is left out.
func decodeFRU(img []byte) (fruRecord, error) {
	if !validHeader(img) {
		return fruRecord{}, errFRUCorrupt
	}
	var rec fruRecord
	rec.chassis = decodeArea(img, int(img[2])*8, areaChassis)
	rec.board = decodeArea(img, int(img[3])*8, areaBoard)
	rec.product = decodeArea(img, int(img[4])*8, areaProduct)
	return rec, nil
}

type areaKind int

const (
	areaChassis areaKind = iota
	areaBoard
	areaProduct
)

var fruKeys = map[areaKind][]string{
	areaChassis: {hwschema.AttrPartNumber, hwschema.AttrSerialNumber},
	areaBoard:   {hwschema.AttrVendor, hwschema.AttrModel, hwschema.AttrSerialNumber, hwschema.AttrPartNumber},
	areaProduct: {hwschema.AttrVendor, hwschema.AttrModel, hwschema.AttrPartNumber, hwschema.AttrVersion, hwschema.AttrSerialNumber, hwschema.AttrAssetTag},
}

var fruChassisTypes = map[byte]string{
	0x03: "Desktop", 0x07: "Tower", 0x08: "Portable", 0x09: "Laptop", 0x0A: "Notebook", 0x11: "Main Server Chassis",
	0x17: "Rack Mount Chassis", 0x1C: "Blade", 0x1D: "Blade Enclosure",
}

func decodeArea(img []byte, off int, kind areaKind) map[string]string {
	if off < fruHeaderSize || off+2 > len(img) || img[off]&0x0F != 1 {
		return nil
	}
	length := int(img[off+1]) * 8
	if length < 8 || off+length > len(img) {
		return nil
	}
	area := img[off : off+length]
	var sum byte
	for _, b := range area {
		sum += b
	}
	if sum != 0 {
		return nil
	}
	body := area[:length-1]
	start := 3
	if kind == areaBoard {
		start = 6
	}
	fields, ok := readFields(body, start)
	if !ok {
		return nil
	}
	out := map[string]string{}
	if kind == areaChassis {
		if t := fruChassisTypes[body[2]&0x7F]; t != "" {
			out["chassis.type"] = t
		}
	}
	for i, key := range fruKeys[kind] {
		if i < len(fields) && fields[i] != "" {
			out[key] = fields[i]
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// readFields decodes the type/length fields from pos up to the end marker.
func readFields(body []byte, pos int) ([]string, bool) {
	var out []string
	for pos < len(body) && len(out) < fruMaxFields {
		b := body[pos]
		if b == 0xC1 || b == 0x00 {
			break
		}
		pos++
		n := int(b & 0x3F)
		if pos+n > len(body) {
			return nil, false
		}
		out = append(out, decodeText(b>>6, body[pos:pos+n]))
		pos += n
	}
	return out, true
}

func decodeText(typ byte, data []byte) string {
	var s string
	switch typ {
	case 3:
		s = string(data)
	case 2:
		var b strings.Builder
		var bits uint32
		var n uint
		for _, c := range data {
			bits |= uint32(c) << n
			n += 8
			for n >= 6 {
				b.WriteByte(byte(bits&0x3F) + 0x20)
				bits >>= 6
				n -= 6
			}
		}
		s = b.String()
	case 1:
		var b strings.Builder
		for _, c := range data {
			for _, v := range []byte{c >> 4, c & 0x0F} { // high nibble first
				switch {
				case v <= 9:
					b.WriteByte('0' + v)
				case v == 0xA:
					b.WriteByte(' ')
				case v == 0xB:
					b.WriteByte('-')
				case v == 0xC:
					b.WriteByte('.')
				}
			}
		}
		s = b.String()
	}
	return cleanFRUText(s)
}

var fruPlaceholders = map[string]bool{
	"to be filled by o.e.m.": true, "to be filled by oem": true, "default string": true, "not specified": true,
	"not applicable": true, "not available": true, "n/a": true, "na": true, "none": true, "unknown": true,
	"undefined": true, "unspecified": true, "o.e.m.": true, "oem": true, "empty": true, "default": true,
	"serial number": true, "123456789": true, "0123456789": true,
}

// cleanFRUText keeps printable ASCII and returns "" for empty, fill and placeholder text.
func cleanFRUText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s) && b.Len() < fruMaxText; i++ {
		if c := s[i]; c >= 0x20 && c <= 0x7E {
			b.WriteByte(c)
		}
	}
	t := strings.TrimSpace(b.String())
	low := strings.ToLower(t)
	if t == "" || fruPlaceholders[low] || strings.HasPrefix(low, "to be filled") {
		return ""
	}
	if strings.ContainsRune("0fFxX-._*?#", rune(t[0])) && strings.Trim(t, t[:1]) == "" {
		return ""
	}
	return t
}
