// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ippreceiver

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/url"
)

// opGetPrinterAttributes is the only operation the receiver sends; it reads and changes nothing.
const (
	opGetPrinterAttributes uint16 = 0x000B

	tagOperationAttributes byte = 0x01
	tagEnd                 byte = 0x03
	tagPrinterAttributes   byte = 0x04

	tagInteger  byte = 0x21
	tagBoolean  byte = 0x22
	tagEnum     byte = 0x23
	tagKeyword  byte = 0x44
	tagURI      byte = 0x45
	tagCharset  byte = 0x47
	tagLanguage byte = 0x48

	// statusSuccessMax is the last status code of the successful class (0x0000-0x00FF).
	statusSuccessMax uint16 = 0x00FF
	// firstValueTag separates delimiter tags (below) from value tags.
	firstValueTag byte = 0x10

	defaultPort = "631"
)

// requestedAttributes are the attributes one poll asks for.
var requestedAttributes = []string{
	"printer-state",
	"printer-state-reasons",
	"printer-is-accepting-jobs",
	"queued-job-count",
	"marker-levels",
	"marker-names",
	"marker-types",
}

var errTruncated = errors.New("truncated IPP message")

func putU16(b []byte, v int) []byte {
	return append(b, byte(v>>8), byte(v)) //nolint:gosec // a length below 65536
}

func putAttribute(b []byte, tag byte, name, val string) []byte {
	b = append(b, tag)
	b = putU16(b, len(name))
	b = append(b, name...)
	b = putU16(b, len(val))
	return append(b, val...)
}

// encodeGetPrinterAttributes builds an IPP 1.1 Get-Printer-Attributes request.
func encodeGetPrinterAttributes(printerURI string, requestID uint32) []byte {
	b := []byte{1, 1}
	b = putU16(b, int(opGetPrinterAttributes))
	b = binary.BigEndian.AppendUint32(b, requestID)
	b = append(b, tagOperationAttributes)
	b = putAttribute(b, tagCharset, "attributes-charset", "utf-8")
	b = putAttribute(b, tagLanguage, "attributes-natural-language", "en")
	b = putAttribute(b, tagURI, "printer-uri", printerURI)
	for i, name := range requestedAttributes {
		attr := "requested-attributes"
		if i > 0 {
			attr = ""
		}
		b = putAttribute(b, tagKeyword, attr, name)
	}
	return append(b, tagEnd)
}

// value is one attribute value with its tag.
type value struct {
	tag  byte
	data []byte
}

type attribute struct {
	name   string
	values []value
}

type group struct {
	tag   byte
	attrs []attribute
}

// message is a decoded IPP request or response; code is the operation or status.
type message struct {
	code   uint16
	groups []group
}

// decodeMessage parses the groups and attributes of an IPP message.
func decodeMessage(b []byte) (message, error) {
	if len(b) < 9 {
		return message{}, errTruncated
	}
	m := message{code: binary.BigEndian.Uint16(b[2:4])}
	pos := 8
	for pos < len(b) {
		tag := b[pos]
		pos++
		switch {
		case tag == tagEnd:
			return m, nil
		case tag < firstValueTag:
			m.groups = append(m.groups, group{tag: tag})
		default:
			if len(m.groups) == 0 {
				return message{}, errors.New("IPP attribute before any group")
			}
			n, err := m.groups[len(m.groups)-1].readAttribute(tag, b[pos:])
			if err != nil {
				return message{}, err
			}
			pos += n
		}
	}
	return message{}, errTruncated
}

// readAttribute reads one name and value after tag and returns the bytes it used. An
// empty name adds a value to the previous attribute.
func (g *group) readAttribute(tag byte, b []byte) (int, error) {
	if len(b) < 2 {
		return 0, errTruncated
	}
	nameLen := int(binary.BigEndian.Uint16(b))
	if len(b) < 2+nameLen+2 {
		return 0, errTruncated
	}
	name := string(b[2 : 2+nameLen])
	valLen := int(binary.BigEndian.Uint16(b[2+nameLen:]))
	end := 2 + nameLen + 2 + valLen
	if len(b) < end {
		return 0, errTruncated
	}
	v := value{tag: tag, data: b[2+nameLen+2 : end]}
	switch {
	case nameLen > 0:
		g.attrs = append(g.attrs, attribute{name: name, values: []value{v}})
	case len(g.attrs) == 0:
		return 0, errors.New("IPP additional value without an attribute")
	default:
		last := &g.attrs[len(g.attrs)-1]
		last.values = append(last.values, v)
	}
	return end, nil
}

// printerAttributes returns the attributes of the first printer group.
func (m message) printerAttributes() (map[string][]value, bool) {
	for _, g := range m.groups {
		if g.tag != tagPrinterAttributes {
			continue
		}
		out := make(map[string][]value, len(g.attrs))
		for _, a := range g.attrs {
			out[a.name] = a.values
		}
		return out, true
	}
	return nil, false
}

func (v value) asInt() (int32, bool) {
	if (v.tag != tagInteger && v.tag != tagEnum) || len(v.data) != 4 {
		return 0, false
	}
	return int32(binary.BigEndian.Uint32(v.data)), true //nolint:gosec // IPP integers are signed 32-bit
}

func (v value) asBool() (bool, bool) {
	if v.tag != tagBoolean || len(v.data) != 1 {
		return false, false
	}
	return v.data[0] != 0, true
}

func (v value) asString() string { return string(v.data) }

// httpURL maps an ipp:// or ipps:// URI to the http:// or https:// URL that carries it.
func httpURL(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse endpoint: %w", err)
	}
	switch u.Scheme {
	case "ipp":
		u.Scheme = "http"
	case "ipps":
		u.Scheme = "https"
	default:
		return "", fmt.Errorf("endpoint scheme %q is not ipp or ipps", u.Scheme)
	}
	if u.Port() == "" {
		u.Host = net.JoinHostPort(u.Hostname(), defaultPort)
	}
	return u.String(), nil
}
