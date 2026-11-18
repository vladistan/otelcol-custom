// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Data types of an nvpair, as numbered by OpenZFS; only these are decoded.
const (
	nvUint64      = 8
	nvString      = 9
	nvUint64Array = 16
	nvNVList      = 19
	nvNVListArray = 20
)

const (
	nvHeaderLen  = 12 // encoding, endian, two reserved bytes, list version and flags
	nvPairHeader = 16 // size, name size, reserved, element count, type
	nvListStruct = 24 // the nvlist_t that sits in the value of a nested list
	maxNVDepth   = 16
	// One list holds a few hundred pairs at most; the whole tree of a pool with hundreds of vdevs
	// holds tens of thousands, so the two limits differ.
	maxNVPairsPerList = 4096
	maxNVPairsTotal   = 1 << 18
	nvLittleEndian    = 1
	nvEncodeNative    = 0
)

var (
	errNVDepth   = errors.New("nvlist nested too deep")
	errNVTooMany = errors.New("nvlist has too many pairs")
	errNVShort   = errors.New("nvlist is cut short")
)

// nvValue is one decoded pair; only the field of its type is set.
type nvValue struct {
	kind  int32
	u     uint64
	s     string
	arr   []uint64
	list  *nvList
	lists []*nvList
}

// nvList is a decoded nvlist; lookups by name are safe on a nil list.
type nvList struct {
	m map[string]nvValue
}

func (l *nvList) get(name string, kind int32) (nvValue, bool) {
	if l == nil {
		return nvValue{}, false
	}
	v, ok := l.m[name]
	if !ok || v.kind != kind {
		return nvValue{}, false
	}
	return v, true
}

func (l *nvList) uint64Value(name string) (uint64, bool) {
	v, ok := l.get(name, nvUint64)
	return v.u, ok
}

func (l *nvList) stringValue(name string) (string, bool) {
	v, ok := l.get(name, nvString)
	return v.s, ok
}

func (l *nvList) uint64Values(name string) []uint64 {
	v, _ := l.get(name, nvUint64Array)
	return v.arr
}

func (l *nvList) subList(name string) (*nvList, bool) {
	v, ok := l.get(name, nvNVList)
	return v.list, ok
}

func (l *nvList) subLists(name string) []*nvList {
	v, _ := l.get(name, nvNVListArray)
	return v.lists
}

// nvReader walks one packed buffer; it never reads outside it.
type nvReader struct {
	buf   []byte
	pairs int
}

// decodeNVList decodes a natively packed nvlist. Pairs of a type it does not
// know are skipped by their size, so a newer release only adds keys.
func decodeNVList(buf []byte) (*nvList, error) {
	if len(buf) < nvHeaderLen {
		return nil, errNVShort
	}
	if buf[0] != nvEncodeNative {
		return nil, fmt.Errorf("nvlist encoding %d is not native", buf[0])
	}
	if hostLittleEndian() != (buf[1] == nvLittleEndian) {
		return nil, errors.New("nvlist byte order differs from the host")
	}
	if v := binary.NativeEndian.Uint32(buf[4:]); v != 0 {
		return nil, fmt.Errorf("nvlist version %d is not supported", v)
	}
	r := &nvReader{buf: buf}
	l, _, err := r.list(nvHeaderLen, 0)
	return l, err
}

func hostLittleEndian() bool {
	return binary.NativeEndian.Uint16([]byte{1, 0}) == 1
}

// list reads pairs from pos up to the zero end marker and returns the position after it.
func (r *nvReader) list(pos, depth int) (*nvList, int, error) {
	if depth > maxNVDepth {
		return nil, 0, errNVDepth
	}
	out := &nvList{m: map[string]nvValue{}}
	inList := 0
	for {
		if pos < 0 || pos+4 > len(r.buf) {
			return nil, 0, errNVShort
		}
		size := int(int32(binary.NativeEndian.Uint32(r.buf[pos:])))
		if size == 0 {
			return out, pos + 4, nil
		}
		inList++
		if r.pairs++; r.pairs > maxNVPairsTotal || inList > maxNVPairsPerList {
			return nil, 0, errNVTooMany
		}
		name, val, err := r.pair(pos, size)
		if err != nil {
			return nil, 0, err
		}
		pos += size
		if val.kind == nvNVList || val.kind == nvNVListArray {
			if pos, err = r.nested(&val, pos, depth); err != nil {
				return nil, 0, err
			}
		}
		out.m[name] = val
	}
}

// nested reads the lists that follow a nested-list pair and returns the position after them.
func (r *nvReader) nested(val *nvValue, pos, depth int) (int, error) {
	count := 1
	if val.kind == nvNVListArray {
		count = len(val.lists)
	}
	lists := make([]*nvList, 0, count)
	for i := 0; i < count; i++ {
		l, next, err := r.list(pos, depth+1)
		if err != nil {
			return 0, err
		}
		lists, pos = append(lists, l), next
	}
	if val.kind == nvNVList {
		val.list = lists[0]
	} else {
		val.lists = lists
	}
	return pos, nil
}

// pair checks the header of the pair at pos against its stated size and decodes its value.
func (r *nvReader) pair(pos, size int) (string, nvValue, error) {
	if size < nvPairHeader || size > len(r.buf)-pos {
		return "", nvValue{}, fmt.Errorf("nvpair size %d does not fit", size)
	}
	p := r.buf[pos : pos+size]
	nameSz := int(binary.NativeEndian.Uint16(p[4:]))
	elem := int(int32(binary.NativeEndian.Uint32(p[8:])))
	kind := int32(binary.NativeEndian.Uint32(p[12:]))
	if nameSz < 1 || nvPairHeader+nameSz > size || p[nvPairHeader+nameSz-1] != 0 {
		return "", nvValue{}, errors.New("nvpair name is not valid")
	}
	name := string(p[nvPairHeader : nvPairHeader+nameSz-1])
	valOff := (nvPairHeader + nameSz + 7) &^ 7
	if valOff > size {
		valOff = size
	}
	val, err := decodeValue(kind, elem, p[valOff:])
	return name, val, err
}

func decodeValue(kind int32, elem int, v []byte) (nvValue, error) {
	out := nvValue{kind: kind}
	switch kind {
	case nvUint64:
		if len(v) < 8 {
			return out, errors.New("nvpair integer is short")
		}
		out.u = binary.NativeEndian.Uint64(v)
	case nvString:
		end := indexNUL(v)
		if end < 0 {
			return out, errors.New("nvpair string has no terminator")
		}
		out.s = string(v[:end])
	case nvUint64Array:
		if elem < 0 || elem > len(v)/8 {
			return out, errors.New("nvpair array is longer than its pair")
		}
		out.arr = make([]uint64, elem)
		for i := range out.arr {
			out.arr[i] = binary.NativeEndian.Uint64(v[8*i:])
		}
	case nvNVList:
		if len(v) < nvListStruct {
			return out, errors.New("nvpair list is short")
		}
	case nvNVListArray:
		if elem < 0 || elem > len(v)/(8+nvListStruct) {
			return out, errors.New("nvpair list array is longer than its pair")
		}
		out.lists = make([]*nvList, elem)
	}
	return out, nil
}

func indexNUL(b []byte) int {
	for i, c := range b {
		if c == 0 {
			return i
		}
	}
	return -1
}
