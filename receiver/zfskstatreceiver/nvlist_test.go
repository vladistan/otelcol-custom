// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package zfskstatreceiver

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nvEnc builds a native-encoded nvlist the way the kernel packs one, for the tests.
type nvEnc struct{ b []byte }

func newNV() *nvEnc { return &nvEnc{} }

func align8(n int) int { return (n + 7) &^ 7 }

func rawPair(name string, typ, elem int32, value []byte) []byte {
	nameSz := len(name) + 1
	valOff := align8(16 + nameSz)
	size := valOff + align8(len(value))
	b := make([]byte, size)
	binary.NativeEndian.PutUint32(b[0:], uint32(size))
	binary.NativeEndian.PutUint16(b[4:], uint16(nameSz))
	binary.NativeEndian.PutUint32(b[8:], uint32(elem))
	binary.NativeEndian.PutUint32(b[12:], uint32(typ))
	copy(b[16:], name)
	copy(b[valOff:], value)
	return b
}

func (e *nvEnc) add(b []byte) *nvEnc { e.b = append(e.b, b...); return e }

func (e *nvEnc) u64(name string, v uint64) *nvEnc {
	val := make([]byte, 8)
	binary.NativeEndian.PutUint64(val, v)
	return e.add(rawPair(name, nvUint64, 1, val))
}

func (e *nvEnc) str(name, v string) *nvEnc {
	return e.add(rawPair(name, nvString, 1, append([]byte(v), 0)))
}

func (e *nvEnc) u64s(name string, v ...uint64) *nvEnc {
	val := make([]byte, 8*len(v))
	for i, x := range v {
		binary.NativeEndian.PutUint64(val[8*i:], x)
	}
	return e.add(rawPair(name, nvUint64Array, int32(len(v)), val))
}

// embedded packs one nested list: the pair carries the 24-byte nvlist_t, the pairs follow it.
func (e *nvEnc) embedded(f func(*nvEnc)) {
	inner := newNV()
	if f != nil {
		f(inner)
	}
	e.b = append(e.b, inner.b...)
	e.b = append(e.b, 0, 0, 0, 0)
}

func (e *nvEnc) list(name string, f func(*nvEnc)) *nvEnc {
	e.add(rawPair(name, nvNVList, 1, make([]byte, 24)))
	e.embedded(f)
	return e
}

func (e *nvEnc) lists(name string, fs ...func(*nvEnc)) *nvEnc {
	e.add(rawPair(name, nvNVListArray, int32(len(fs)), make([]byte, 8*len(fs)+24*len(fs))))
	for _, f := range fs {
		e.embedded(f)
	}
	return e
}

func (e *nvEnc) bytes() []byte {
	endian := byte(0)
	if binary.NativeEndian.Uint16([]byte{1, 0}) == 1 {
		endian = 1
	}
	out := []byte{0, endian, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	out = append(out, e.b...)
	return append(out, 0, 0, 0, 0)
}

func TestDecodeReadsScalarsStringsAndArrays(t *testing.T) {
	buf := newNV().u64("size", 1<<40).str("name", "tank").u64s("vdev_stats", 1, 2, 3).bytes()

	l, err := decodeNVList(buf)

	require.NoError(t, err)
	v, ok := l.uint64Value("size")
	assert.True(t, ok)
	assert.Equal(t, uint64(1<<40), v)
	s, ok := l.stringValue("name")
	assert.True(t, ok)
	assert.Equal(t, "tank", s)
	assert.Equal(t, []uint64{1, 2, 3}, l.uint64Values("vdev_stats"))
}

func TestDecodeReadsAPropertyListByKeyName(t *testing.T) {
	buf := newNV().
		list("used", func(e *nvEnc) { e.u64("source", 0).u64("value", 4096) }).
		list("quota", func(e *nvEnc) { e.u64("value", 0) }).
		bytes()

	l, err := decodeNVList(buf)

	require.NoError(t, err)
	used, ok := l.subList("used")
	require.True(t, ok)
	v, _ := used.uint64Value("value")
	assert.Equal(t, uint64(4096), v)
	quota, ok := l.subList("quota")
	require.True(t, ok)
	q, ok := quota.uint64Value("value")
	assert.True(t, ok)
	assert.Zero(t, q)
}

func TestDecodeReadsAListArrayInOrder(t *testing.T) {
	buf := newNV().lists("children",
		func(e *nvEnc) { e.str("type", "disk") },
		func(e *nvEnc) { e.str("type", "mirror") },
	).u64("after", 7).bytes()

	l, err := decodeNVList(buf)

	require.NoError(t, err)
	kids := l.subLists("children")
	require.Len(t, kids, 2)
	a, _ := kids[0].stringValue("type")
	b, _ := kids[1].stringValue("type")
	assert.Equal(t, []string{"disk", "mirror"}, []string{a, b})
	after, ok := l.uint64Value("after")
	assert.True(t, ok)
	assert.Equal(t, uint64(7), after)
}

func TestDecodeIgnoresUnknownKeysAndTypes(t *testing.T) {
	buf := newNV().
		add(rawPair("flag", 21, 1, []byte{1, 0, 0, 0})).
		add(rawPair("odd", 99, 1, []byte{1, 2, 3, 4, 5, 6, 7, 8})).
		u64("size", 5).bytes()

	l, err := decodeNVList(buf)

	require.NoError(t, err)
	v, ok := l.uint64Value("size")
	assert.True(t, ok)
	assert.Equal(t, uint64(5), v)
	_, ok = l.uint64Value("flag")
	assert.False(t, ok)
}

func TestDecodeKeepsTheLastOfTwoPairsWithOneName(t *testing.T) {
	l, err := decodeNVList(newNV().u64("x", 1).u64("x", 2).bytes())

	require.NoError(t, err)
	v, _ := l.uint64Value("x")
	assert.Equal(t, uint64(2), v)
}

func TestDecodeAnAbsentKeyOrAWrongTypeReadsAsMissing(t *testing.T) {
	l, err := decodeNVList(newNV().str("name", "tank").bytes())

	require.NoError(t, err)
	_, ok := l.uint64Value("name")
	assert.False(t, ok)
	_, ok = l.stringValue("nope")
	assert.False(t, ok)
	assert.Nil(t, l.uint64Values("name"))
	_, ok = l.subList("name")
	assert.False(t, ok)
	assert.Nil(t, l.subLists("name"))
}

func TestLookupsOnANilListReadAsMissing(t *testing.T) {
	var l *nvList

	_, ok := l.uint64Value("x")
	assert.False(t, ok)
	_, ok = l.stringValue("x")
	assert.False(t, ok)
	assert.Nil(t, l.uint64Values("x"))
	_, ok = l.subList("x")
	assert.False(t, ok)
	assert.Nil(t, l.subLists("x"))
}

func TestDecodeNeverPanicsOnAnyTruncationAndFailsOnEach(t *testing.T) {
	buf := newNV().
		u64("a", 1).str("name", "tank").u64s("arr", 1, 2).
		list("inner", func(e *nvEnc) { e.u64("value", 9) }).
		lists("kids", func(e *nvEnc) { e.u64("v", 1) }).
		bytes()

	for i := 0; i < len(buf); i++ {
		assert.NotPanics(t, func() {
			_, err := decodeNVList(buf[:i])
			assert.Error(t, err, "prefix of %d bytes", i)
		})
	}
	_, err := decodeNVList(buf)
	assert.NoError(t, err)
}

func TestDecodeIgnoresBytesAfterTheEndMarker(t *testing.T) {
	buf := append(newNV().u64("a", 1).bytes(), 0xde, 0xad, 0xbe, 0xef, 1, 2, 3)

	_, err := decodeNVList(buf)

	assert.NoError(t, err)
}

func TestDecodeRejectsABadHeader(t *testing.T) {
	good := newNV().u64("a", 1).bytes()
	xdr := append([]byte{1}, good[1:]...)
	otherEndian := append([]byte{0, good[1] ^ 1}, good[2:]...)
	version := append([]byte{}, good...)
	version[4] = 7

	for name, buf := range map[string][]byte{
		"empty": nil, "short": good[:8], "xdr": xdr, "other endian": otherEndian, "version": version,
	} {
		_, err := decodeNVList(buf)
		assert.Error(t, err, name)
	}
}

func TestDecodeRejectsPairsWhoseSizesLie(t *testing.T) {
	good := rawPair("a", nvUint64, 1, make([]byte, 8))
	tooBig := append([]byte{}, good...)
	binary.NativeEndian.PutUint32(tooBig[0:], uint32(len(good)+64))
	tooSmall := append([]byte{}, good...)
	binary.NativeEndian.PutUint32(tooSmall[0:], 8)
	negative := append([]byte{}, good...)
	binary.NativeEndian.PutUint32(negative[0:], 0xffffff00)
	noName := append([]byte{}, good...)
	binary.NativeEndian.PutUint16(noName[4:], 0)
	longName := append([]byte{}, good...)
	binary.NativeEndian.PutUint16(longName[4:], 500)
	noNul := append([]byte{}, good...)
	noNul[16+1] = 'x'
	shortValue := append([]byte{}, good...)
	binary.NativeEndian.PutUint32(shortValue[0:], uint32(len(good)-8))
	bigArray := rawPair("a", nvUint64Array, 1000, make([]byte, 16))
	negElem := rawPair("a", nvUint64Array, -1, make([]byte, 16))

	for name, pair := range map[string][]byte{
		"size past the buffer": tooBig, "size under the header": tooSmall, "negative size": negative,
		"empty name": noName, "name past the pair": longName, "name without nul": noNul,
		"value short of 8 bytes": shortValue, "array longer than its pair": bigArray, "negative count": negElem,
	} {
		buf := newNV().add(pair).bytes()
		_, err := decodeNVList(buf)
		assert.Error(t, err, name)
	}
}

func TestDecodeRejectsAStringWithoutItsTerminator(t *testing.T) {
	buf := newNV().add(rawPair("s", nvString, 1, []byte{'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h'})).bytes()

	_, err := decodeNVList(buf)

	assert.Error(t, err)
}

func TestDecodeStopsAtTheDepthLimit(t *testing.T) {
	var nest func(n int) func(*nvEnc)
	nest = func(n int) func(*nvEnc) {
		return func(e *nvEnc) {
			if n > 0 {
				e.list("x", nest(n-1))
			}
		}
	}
	shallow := newNV()
	shallow.list("x", nest(3))
	deep := newNV()
	deep.list("x", nest(200))

	_, err := decodeNVList(shallow.bytes())
	assert.NoError(t, err)
	_, err = decodeNVList(deep.bytes())
	assert.ErrorIs(t, err, errNVDepth)
}

func TestDecodeStopsAtThePairLimitOfOneList(t *testing.T) {
	e := newNV()
	for i := 0; i < maxNVPairsPerList+1; i++ {
		e.u64(strings.Repeat("k", 1)+string(rune('a'+i%26)), 1)
	}

	_, err := decodeNVList(e.bytes())

	assert.ErrorIs(t, err, errNVTooMany)
}

// A pool of 400 vdevs holds well over 4096 pairs in all but each list is small.
func TestDecodeCountsPairsPerListAndNotAcrossTheTree(t *testing.T) {
	kids := make([]func(*nvEnc), 400)
	for i := range kids {
		kids[i] = func(e *nvEnc) {
			for j := 0; j < 20; j++ {
				e.u64(fmt.Sprintf("k%d", j), 1)
			}
		}
	}

	l, err := decodeNVList(newNV().lists("children", kids...).bytes())

	require.NoError(t, err)
	assert.Len(t, l.subLists("children"), 400)
}

func TestDecodeStopsAtThePairLimitOfTheWholeTree(t *testing.T) {
	kids := make([]func(*nvEnc), 100)
	for i := range kids {
		kids[i] = func(e *nvEnc) {
			for j := 0; j < 3000; j++ {
				e.u64(fmt.Sprintf("k%d", j), 1)
			}
		}
	}

	_, err := decodeNVList(newNV().lists("children", kids...).bytes())

	assert.ErrorIs(t, err, errNVTooMany)
}
