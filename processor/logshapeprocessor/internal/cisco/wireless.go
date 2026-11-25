// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package cisco

import (
	"regexp"
	"strings"
)

// Wireless holds the facts an APF controller message carries about a client and its radio.
type Wireless struct {
	ClientMAC string // lower-case colon form; empty when the message names no client
	APName    string
	WLANID    string
	SSID      string
	Radio     string
	// Text is the message without the facts written as labels.
	Text string
	// MACInText reports that the client MAC follows "from" in Text, so Body does not repeat it.
	MACInText bool
}

// macPattern matches a MAC in colon, hyphen or dotted form.
const macPattern = `(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}|(?:[0-9A-Fa-f]{2}-){5}[0-9A-Fa-f]{2}|[0-9A-Fa-f]{4}\.[0-9A-Fa-f]{4}\.[0-9A-Fa-f]{4}`

var (
	wirelessClientRe = regexp.MustCompile(`(?:Client|Mobile) MAC:\s*(` + macPattern + `)`)
	wirelessFromRe   = regexp.MustCompile(`\bfrom\s+(` + macPattern + `)`)
	wirelessWLANRe   = regexp.MustCompile(`WLAN(?: Id)?:\s*(\d+)`)
	wirelessSSIDRe   = regexp.MustCompile(`\bSSID:\s*([^,]+)`)
	macExactRe       = regexp.MustCompile(`^(?:` + macPattern + `)$`)
	repeatedComma    = regexp.MustCompile(`(?:\s*,){2,}`)
	repeatedSpace    = regexp.MustCompile(`\s{2,}`)
)

// NormalizeMAC returns a MAC in colon, hyphen or dotted form as lower-case colon pairs.
func NormalizeMAC(s string) (string, bool) {
	if !macExactRe.MatchString(s) {
		return "", false
	}
	hex := strings.ToLower(strings.NewReplacer(":", "", "-", "", ".", "").Replace(s))
	var b strings.Builder
	for i := 0; i < len(hex); i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(hex[i : i+2])
	}
	return b.String(), true
}

// ParseWireless reads the client, AP, WLAN, SSID and radio facts from the message of an APF event.
// The client is the MAC after "Client MAC:" or "Mobile MAC:", else the MAC after "from".
func ParseWireless(content string) Wireless {
	var w Wireless
	text := content

	if m := wirelessClientRe.FindStringSubmatch(text); m != nil {
		w.ClientMAC, _ = NormalizeMAC(m[1])
		text = wirelessClientRe.ReplaceAllString(text, "")
	} else if m := wirelessFromRe.FindStringSubmatch(text); m != nil {
		w.ClientMAC, _ = NormalizeMAC(m[1])
		w.MACInText = true
		text = wirelessFromRe.ReplaceAllString(text, "from "+w.ClientMAC)
	}
	if m := wlcAPNameRegex.FindStringSubmatch(text); m != nil {
		w.APName = strings.TrimRight(strings.TrimSpace(m[1]), " .,")
		text = wlcAPNameRegex.ReplaceAllString(text, "")
	}
	if m := wlcRadioRegex.FindStringSubmatch(text); m != nil {
		w.Radio = strings.TrimRight(strings.TrimSpace(m[1]), " .,")
		text = wlcRadioRegex.ReplaceAllString(text, "")
	}
	if m := wirelessWLANRe.FindStringSubmatch(text); m != nil {
		w.WLANID = m[1]
		text = wirelessWLANRe.ReplaceAllString(text, "")
	}
	if m := wirelessSSIDRe.FindStringSubmatch(text); m != nil {
		w.SSID = strings.TrimSuffix(strings.TrimSpace(m[1]), ".")
		text = wirelessSSIDRe.ReplaceAllString(text, "")
	}

	text = repeatedComma.ReplaceAllString(text, ",")
	text = repeatedSpace.ReplaceAllString(text, " ")
	text = strings.TrimRight(text, " ,.:")
	w.Text = strings.TrimLeft(text, " ,")
	return w
}

// Body is the text followed by the facts present, in the order client MAC, ap, wlan, ssid, radio.
// The ssid is the one the caller settled on: the message's own or one from a map.
func (w Wireless) Body(ssid string) string {
	parts := []string{w.Text}
	if w.ClientMAC != "" && !w.MACInText {
		parts = append(parts, w.ClientMAC)
	}
	if w.APName != "" {
		parts = append(parts, "ap "+w.APName)
	}
	if w.WLANID != "" {
		parts = append(parts, "wlan "+w.WLANID)
	}
	if ssid != "" {
		parts = append(parts, "ssid "+ssid)
	}
	if w.Radio != "" {
		parts = append(parts, "radio "+w.Radio)
	}
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}
