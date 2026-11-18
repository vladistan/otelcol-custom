// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ipmireceiver

import (
	"errors"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fruText8 is an 8-bit ASCII type/length field.
func fruText8(s string) []byte { return append([]byte{0xC0 | byte(len(s))}, s...) }

// fruTextBCD is a BCD plus field: digits, space (A), dash (B) and period (C).
func fruTextBCD(s string) []byte {
	nib := func(c byte) byte {
		switch {
		case c >= '0' && c <= '9':
			return c - '0'
		case c == ' ':
			return 0xA
		case c == '-':
			return 0xB
		}
		return 0xC
	}
	var out []byte
	for i := 0; i < len(s); i += 2 {
		hi := nib(s[i])
		lo := byte(0)
		if i+1 < len(s) {
			lo = nib(s[i+1])
		}
		out = append(out, hi<<4|lo)
	}
	return append([]byte{0x40 | byte(len(out))}, out...)
}

// fruText6 is a 6-bit ASCII field: characters from 0x20 to 0x5F packed 4 to 3 bytes.
func fruText6(s string) []byte {
	var bits uint32
	var n uint
	var out []byte
	for i := 0; i < len(s); i++ {
		bits |= uint32(s[i]-0x20) << n
		n += 6
		for n >= 8 {
			out = append(out, byte(bits))
			bits >>= 8
			n -= 8
		}
	}
	if n > 0 {
		out = append(out, byte(bits))
	}
	return append([]byte{0x80 | byte(len(out))}, out...)
}

// fruArea wraps the fields in an area: version, length in 8-byte units, the prefix, the fields, the end
// marker, padding and the checksum.
func fruArea(prefix []byte, fields ...[]byte) []byte {
	body := append([]byte{1, 0}, prefix...)
	for _, f := range fields {
		body = append(body, f...)
	}
	body = append(body, 0xC1)
	for (len(body)+1)%8 != 0 {
		body = append(body, 0)
	}
	body = append(body, 0)
	body[1] = byte(len(body) / 8)
	sum := byte(0)
	for _, b := range body[:len(body)-1] {
		sum += b
	}
	body[len(body)-1] = -sum
	return body
}

func fruBoardArea(fields ...[]byte) []byte {
	return fruArea([]byte{0, 0, 0, 0}, fields...)
}

func fruProductArea(fields ...[]byte) []byte { return fruArea([]byte{0}, fields...) }

func fruChassisArea(chassisType byte, fields ...[]byte) []byte {
	return fruArea([]byte{chassisType}, fields...)
}

// fruImage builds a FRU with the header and the given areas, any of which may be nil.
func fruImage(chassis, board, product []byte) []byte {
	hdr := make([]byte, 8)
	hdr[0] = 1
	off := 8
	var rest []byte
	place := func(idx int, area []byte) {
		if area == nil {
			return
		}
		hdr[idx] = byte(off / 8)
		off += len(area)
		rest = append(rest, area...)
	}
	place(2, chassis)
	place(3, board)
	place(4, product)
	sum := byte(0)
	for _, b := range hdr[:7] {
		sum += b
	}
	hdr[7] = -sum
	return append(hdr, rest...)
}

func TestDecodeFRUReadsBoardProductAndChassis(t *testing.T) {
	img := fruImage(
		fruChassisArea(0x17, fruText8("CH-PART-1"), fruText8("CH-SER-2")),
		fruBoardArea(fruText8("BoardMaker"), fruText8("X9DRW-3F"), fruText8("BD-SER-3"), fruText8("BD-PART-4"), fruText8("fileid")),
		fruProductArea(fruText8("ProdMaker"), fruText8("SuperServer"), fruText8("PR-PART-5"), fruText8("v2"), fruText8("PR-SER-6"), fruText8("ASSET-7")),
	)

	rec, err := decodeFRU(img)

	require.NoError(t, err)
	assert.Equal(t, map[string]string{"hw.vendor": "BoardMaker", "hw.model": "X9DRW-3F", "hw.serial_number": "BD-SER-3", "hw.part_number": "BD-PART-4"}, rec.board)
	assert.Equal(t, map[string]string{"hw.vendor": "ProdMaker", "hw.model": "SuperServer", "hw.part_number": "PR-PART-5", "hw.version": "v2", "hw.serial_number": "PR-SER-6", "hw.asset_tag": "ASSET-7"}, rec.product)
	assert.Equal(t, map[string]string{"chassis.type": "Rack Mount Chassis", "hw.part_number": "CH-PART-1", "hw.serial_number": "CH-SER-2"}, rec.chassis)
}

func TestDecodeFRUReadsSixBitAndBCDFields(t *testing.T) {
	img := fruImage(nil, fruBoardArea(fruText6("ACME CORP"), fruText6("MODEL-9"), fruTextBCD("12-34 5678"), fruText8("PART")), nil)

	rec, err := decodeFRU(img)

	require.NoError(t, err)
	assert.Equal(t, "ACME CORP", rec.board["hw.vendor"])
	assert.Equal(t, "MODEL-9", rec.board["hw.model"])
	assert.Equal(t, "12-34 5678", rec.board["hw.serial_number"])
}

func TestDecodeTextBCDPlusPutsTheHighNibbleFirst(t *testing.T) {
	assert.Equal(t, "1234", decodeText(1, []byte{0x12, 0x34}))
	assert.Equal(t, "12-3.4 5", decodeText(1, []byte{0x12, 0xB3, 0xC4, 0xA5}))
}

func TestDecodeFRUAreaStopsAtTheEndMarker(t *testing.T) {
	area := fruProductArea(fruText8("Maker"), []byte{0xC1}, fruText8("AfterTheMarker"))
	img := fruImage(nil, nil, area)

	rec, err := decodeFRU(img)

	require.NoError(t, err)
	assert.Equal(t, map[string]string{"hw.vendor": "Maker"}, rec.product)
}

func TestDecodeFRUPlaceholdersAndEmptyFieldsAreAbsent(t *testing.T) {
	img := fruImage(nil, nil, fruProductArea(fruText8("Maker"), []byte{0xC0}, fruText8("To be filled by O.E.M."), fruText8("v1"), fruText8("0000000"), fruText8("")))

	rec, err := decodeFRU(img)

	require.NoError(t, err)
	assert.Equal(t, map[string]string{"hw.vendor": "Maker", "hw.version": "v1"}, rec.product)
}

func TestDecodeFRUAreaWithNoUsableFieldIsAbsent(t *testing.T) {
	img := fruImage(nil, fruBoardArea([]byte{0xC0}, []byte{0xC0}), nil)

	rec, err := decodeFRU(img)

	require.NoError(t, err)
	assert.Nil(t, rec.board)
}

func TestDecodeFRUHeaderProblemsGiveAnError(t *testing.T) {
	good := fruImage(nil, fruBoardArea(fruText8("A")), nil)
	badSum := append([]byte{}, good...)
	badSum[7]++
	badVersion := append([]byte{}, good...)
	badVersion[0] = 2
	tests := map[string][]byte{
		"empty": nil, "short": good[:5], "checksum": badSum, "version": badVersion,
	}
	for name, img := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := decodeFRU(img)

			assert.ErrorIs(t, err, errFRUCorrupt)
		})
	}
}

func TestDecodeFRUAreaProblemsDropOnlyThatArea(t *testing.T) {
	board := fruBoardArea(fruText8("BoardMaker"))
	product := fruProductArea(fruText8("ProdMaker"))
	badArea := append([]byte{}, board...)
	badArea[len(badArea)-1]++
	tests := map[string]struct {
		img         []byte
		fixSum      bool // the mutation broke the header checksum, which is not what the case tests
		boardGone   bool
		productGone bool
	}{
		"area checksum":       {fruImage(nil, badArea, product), false, true, false},
		"truncated board":     {fruImage(nil, board, product)[:8+len(board)-4], false, true, true},
		"truncated product":   {fruImage(nil, board, product)[:8+len(board)+len(product)-3], false, false, true},
		"huge length byte":    {mutate(fruImage(nil, board, product), 8+1, 0xFF), true, true, false},
		"field beyond area":   {fruImage(nil, fruArea([]byte{0, 0, 0, 0}, []byte{0xFF, 'a'}), product), false, true, false},
		"offset beyond image": {mutate(fruImage(nil, board, product), 3, 0xF0), true, true, false},
		"offset into header":  {mutate(fruImage(nil, board, product), 3, 0), true, true, false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			img := tt.img
			if tt.fixSum {
				fixHeaderSum(img)
			}

			rec, err := decodeFRU(img)

			require.NoError(t, err)
			assert.Equal(t, tt.boardGone, rec.board == nil, "board")
			assert.Equal(t, tt.productGone, rec.product == nil, "product")
		})
	}
}

func mutate(img []byte, at int, v byte) []byte {
	out := append([]byte{}, img...)
	out[at] = v
	return out
}

func fixHeaderSum(img []byte) {
	sum := byte(0)
	for _, b := range img[:7] {
		sum += b
	}
	img[7] = -sum
}

func TestDecodeFRUOfAnAreaWith200FieldsYieldsOnlyTheSixProductKeys(t *testing.T) {
	var fields [][]byte
	for i := 0; i < 200; i++ {
		fields = append(fields, fruText8("ab"))
	}

	rec, err := decodeFRU(fruImage(nil, nil, fruProductArea(fields...)))

	require.NoError(t, err)
	assert.Len(t, rec.product, 6)
}

func TestDecodeFRURandomBytesDoNotPanic(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	good := fruImage(fruChassisArea(0x17, fruText8("A")), fruBoardArea(fruText8("B")), fruProductArea(fruText8("C")))
	for i := 0; i < 3000; i++ {
		data := make([]byte, rng.Intn(200))
		rng.Read(data)
		assert.NotPanics(t, func() { _, _ = decodeFRU(data) })

		m := append([]byte{}, good...)
		for j := 0; j < 1+rng.Intn(5); j++ {
			m[rng.Intn(len(m))] = byte(rng.Intn(256))
		}
		fixHeaderSum(m)
		m = m[:rng.Intn(len(m)+1)]
		assert.NotPanics(t, func() { _, _ = decodeFRU(m) })
	}
}

func FuzzDecodeFRU(f *testing.F) {
	f.Add(fruImage(fruChassisArea(0x17, fruText8("A")), fruBoardArea(fruText8("B"), fruText6("C")), fruProductArea(fruTextBCD("12"))))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = decodeFRU(data)
	})
}

// memReader serves a FRU image in chunks like a BMC and counts the requests.
type memReader struct {
	img      []byte
	maxChunk int
	requests int
	err      error
	empty    bool
}

func (m *memReader) read(off, n int) ([]byte, error) {
	m.requests++
	if m.err != nil {
		return nil, m.err
	}
	if m.empty || off >= len(m.img) {
		return nil, nil
	}
	n = min(n, m.maxChunk, len(m.img)-off)
	return m.img[off : off+n], nil
}

func TestReadFRUImageReadsOnlyToTheEndOfTheLastArea(t *testing.T) {
	product := fruProductArea(fruText8("Maker"), fruText8("Prod"))
	img := fruImage(nil, nil, product)
	padded := append(append([]byte{}, img...), make([]byte, 4000)...)
	rd := &memReader{img: padded, maxChunk: 16}

	got, err := readFRUImage(len(padded), rd.read)

	require.NoError(t, err)
	assert.Equal(t, img, got)
	assert.Less(t, rd.requests, 20)
}

func TestReadFRUImageHandlesAShortAnswerPerRequest(t *testing.T) {
	img := fruImage(nil, fruBoardArea(fruText8("Maker")), nil)
	rd := &memReader{img: img, maxChunk: 3}

	got, err := readFRUImage(len(img), rd.read)

	require.NoError(t, err)
	assert.Equal(t, img, got)
}

func TestReadFRUImageFailsOnAReaderErrorAnEmptyAnswerASmallSizeAndABadHeader(t *testing.T) {
	img := fruImage(nil, fruBoardArea(fruText8("Maker")), nil)
	boom := errors.New("boom")
	tests := map[string]struct {
		size int
		rd   *memReader
		want error
	}{
		"reader error":   {len(img), &memReader{img: img, maxChunk: 16, err: boom}, boom},
		"empty answer":   {len(img), &memReader{img: img, maxChunk: 16, empty: true}, errFRUCorrupt},
		"size too small": {4, &memReader{img: img, maxChunk: 16}, errFRUCorrupt},
		"bad header":     {len(img), &memReader{img: make([]byte, 64), maxChunk: 16}, errFRUCorrupt},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := readFRUImage(tt.size, tt.rd.read)

			assert.ErrorIs(t, err, tt.want)
		})
	}
}

func TestReadFRUImageCapsTheSizeAndTheRequests(t *testing.T) {
	huge := make([]byte, 70000)
	copy(huge, fruImage(nil, nil, fruArea([]byte{0}, fruText8("x"))))
	huge[8+1] = 255 // an area length of 2040 bytes
	rd := &memReader{img: huge, maxChunk: 16}

	got, err := readFRUImage(65535, rd.read)

	require.NoError(t, err)
	assert.LessOrEqual(t, len(got), fruMaxBytes)
	assert.LessOrEqual(t, rd.requests, fruMaxBytes/fruChunk+8)

	slow := &memReader{img: huge, maxChunk: 1}
	_, err = readFRUImage(65535, slow.read)
	assert.ErrorIs(t, err, errFRUCorrupt, "a source that answers one byte at a time hits the request cap")
	assert.LessOrEqual(t, slow.requests, fruMaxRequests+1)
}
