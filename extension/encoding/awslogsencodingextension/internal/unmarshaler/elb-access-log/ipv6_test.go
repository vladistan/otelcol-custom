// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package elbaccesslogs

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/open-telemetry/opentelemetry-collector-contrib/extension/encoding"
)

// albLine builds one ALB access log line in the shape AWS writes it, with the client and target fields given.
// Addresses in these tests come from the documentation ranges (192.0.2.0/24, 198.51.100.0/24, 2001:db8::/32).
func albLine(client, target string) string {
	return fmt.Sprintf(`https 2018-07-02T22:23:00.186641Z app/my-loadbalancer/50dc6c495c0c9188 %s %s 0.086 0.048 0.037 200 200 0 57 `+
		`"GET https://www.example.com:443/ HTTP/1.1" "curl/7.46.0" ECDHE-RSA-AES128-GCM-SHA256 TLSv1.2 `+
		`arn:aws:elasticloadbalancing:us-east-2:123456789012:targetgroup/my-targets/73e2d6bc24d8a067 "Root=1-58337281-1d84f3d73c47ec4e58577259" `+
		`"www.example.com" "arn:aws:acm:us-east-2:123456789012:certificate/12345678-1234-1234-1234-123456789012" 1 `+
		`2018-07-02T22:22:48.364000Z "authenticate,forward" "-" "-" "10.0.0.1:80" "200" "-" "-" TID_1234abcd5678ef90`, client, target)
}

func decodeALB(t *testing.T, lines ...string) (plog.Logs, *observer.ObservedLogs, error) {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	u := NewELBAccessLogUnmarshaler(component.BuildInfo{Version: "test"}, zap.New(core))
	out, err := u.UnmarshalAWSLogs(bytes.NewReader([]byte(strings.Join(lines, "\n") + "\n")))
	return out, logs, err
}

type recordView struct {
	client, destination string
	clientPort          int64
	destinationPort     int64
	hasDestination      bool
}

func views(ld plog.Logs) []recordView {
	var out []recordView
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		sls := ld.ResourceLogs().At(i).ScopeLogs()
		for j := 0; j < sls.Len(); j++ {
			recs := sls.At(j).LogRecords()
			for k := 0; k < recs.Len(); k++ {
				attrs := recs.At(k).Attributes()
				var v recordView
				if c, ok := attrs.Get("client.address"); ok {
					v.client = c.Str()
				}
				if c, ok := attrs.Get("client.port"); ok {
					v.clientPort = c.Int()
				}
				if d, ok := attrs.Get("destination.address"); ok {
					v.destination, v.hasDestination = d.Str(), true
				}
				if d, ok := attrs.Get("destination.port"); ok {
					v.destinationPort = d.Int()
				}
				out = append(out, v)
			}
		}
	}
	return out
}

func TestALB_AnIPv6ClientWrittenWithoutBracketsIsDecoded(t *testing.T) {
	ld, logs, err := decodeALB(t, albLine("2001:db8:0:1::bea9:49649", "10.0.0.1:80"))
	require.NoError(t, err)
	got := views(ld)
	require.Len(t, got, 1)
	assert.Equal(t, "2001:db8:0:1::bea9", got[0].client)
	assert.Equal(t, int64(49649), got[0].clientPort)
	assert.Zero(t, logs.Len(), "a good line logs nothing")
}

func TestALB_AnIPv6TargetWrittenWithoutBracketsIsDecoded(t *testing.T) {
	ld, _, err := decodeALB(t, albLine("192.0.2.10:2817", "2001:db8:0:2::10:8080"))
	require.NoError(t, err)
	got := views(ld)
	require.Len(t, got, 1)
	assert.Equal(t, "2001:db8:0:2::10", got[0].destination)
	assert.Equal(t, int64(8080), got[0].destinationPort)
}

func TestALB_TheBracketedAndIPv4FormsStillDecode(t *testing.T) {
	ld, _, err := decodeALB(t,
		albLine("192.0.2.10:2817", "198.51.100.7:80"),
		albLine("[2001:db8::1]:443", "[2001:db8::2]:80"),
	)
	require.NoError(t, err)
	got := views(ld)
	require.Len(t, got, 2)
	assert.Equal(t, recordView{client: "192.0.2.10", clientPort: 2817, destination: "198.51.100.7", destinationPort: 80, hasDestination: true}, got[0])
	assert.Equal(t, recordView{client: "2001:db8::1", clientPort: 443, destination: "2001:db8::2", destinationPort: 80, hasDestination: true}, got[1])
}

func TestALB_ADashTargetGivesNoDestination(t *testing.T) {
	ld, _, err := decodeALB(t, albLine("2001:db8::7:5555", "-"))
	require.NoError(t, err)
	got := views(ld)
	require.Len(t, got, 1)
	assert.False(t, got[0].hasDestination)
	assert.Equal(t, "2001:db8::7", got[0].client)
}

func TestALB_AMalformedLineIsSkippedAndTheRestOfTheObjectIsKept(t *testing.T) {
	ld, logs, err := decodeALB(t,
		albLine("192.0.2.10:2817", "198.51.100.7:80"),
		albLine("not-an-address", "198.51.100.7:80"),
		albLine("2001:db8::bea9:49649", "-"),
	)
	require.NoError(t, err, "one bad line does not fail the object")
	assert.Len(t, views(ld), 2)

	warns := logs.FilterLevelExact(zapcore.WarnLevel).All()
	require.Len(t, warns, 1, "one warning for the object")
	assert.Equal(t, int64(1), warns[0].ContextMap()["skipped_lines"])
	for _, e := range logs.All() {
		text := e.Message + fmt.Sprint(e.ContextMap())
		assert.NotContains(t, text, "not-an-address", "no line content in logs")
		assert.NotContains(t, text, "2001:db8", "no address in logs")
		assert.NotContains(t, text, "192.0.2", "no address in logs")
	}
}

func TestALB_SeveralBadLinesGiveOneWarningWithTheCount(t *testing.T) {
	ld, logs, err := decodeALB(t,
		albLine("bad-one", "-"),
		albLine("192.0.2.10:2817", "-"),
		albLine("bad-two", "-"),
		"https 2018-07-02T22:23:00Z too few fields",
		albLine("198.51.100.8:3000", "-"),
	)
	require.NoError(t, err)
	assert.Len(t, views(ld), 2)
	warns := logs.FilterLevelExact(zapcore.WarnLevel).All()
	require.Len(t, warns, 1)
	assert.Equal(t, int64(3), warns[0].ContextMap()["skipped_lines"])
}

func TestALB_AnObjectOfOnlyBadLinesIsEmptyNotAnError(t *testing.T) {
	ld, logs, err := decodeALB(t, albLine("bad", "-"), albLine("worse", "-"))
	if err != nil {
		assert.ErrorIs(t, err, io.EOF, "an object with no decodable line ends like an empty one")
	}
	assert.Zero(t, ld.LogRecordCount())
	assert.Len(t, logs.FilterLevelExact(zapcore.WarnLevel).All(), 1)
}

func TestALB_StreamingKeepsGoingPastABadLine(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	u := NewELBAccessLogUnmarshaler(component.BuildInfo{Version: "test"}, zap.New(core))
	input := strings.Join([]string{
		albLine("192.0.2.10:1", "-"),
		albLine("bad", "-"),
		albLine("2001:db8::9:2", "-"),
		albLine("198.51.100.8:3", "-"),
	}, "\n") + "\n"
	dec, err := u.NewLogsDecoder(bytes.NewReader([]byte(input)), encoding.WithFlushItems(2))
	require.NoError(t, err)

	var total int
	for {
		ld, err := dec.DecodeLogs()
		total += ld.LogRecordCount()
		if err != nil {
			break
		}
	}
	assert.Equal(t, 3, total)
	assert.GreaterOrEqual(t, len(logs.FilterLevelExact(zapcore.WarnLevel).All()), 1)
}

// nlbLine builds one NLB access log line in the documented field order (22 fields) with the client and
// destination fields given. INVENTED from the documented layout, not a capture: no real NLB log was available.
func nlbLine(client, destination string) string {
	return fmt.Sprintf("tls 2.0 2018-12-20T02:59:40 net/my-network-loadbalancer/c6e77e28c25b2234 g3d4b5e8bb8464cd %s %s "+
		"5 2 98 246 - arn:aws:acm:us-east-2:123456789012:certificate/2a108f19-aded-46b0-8493-c63eb1ef4a99 - ECDHE-RSA-AES128-SHA TLSv1.2 - "+
		"my-network-loadbalancer-c6e77e28c25b2234.elb.us-east-2.amazonaws.com - - - 2018-12-20T02:59:30", client, destination)
}

// clbLine builds one CLB access log line in the documented field order with the client and backend fields given.
// INVENTED from the documented layout, not a capture: no real CLB log was available, and no live CLB line with an
// IPv6 address is on record, so CLB lines keep the upstream address handling (IPv4 and bracketed forms only).
func clbLine(client, backend string) string {
	return fmt.Sprintf(`2015-05-13T23:39:43.945958Z my-loadbalancer %s %s 0.000086 0.001048 0.001337 200 200 0 57 `+
		`"GET https://www.example.com:443/ HTTP/1.1" "curl/7.38.0" DHE-RSA-AES128-SHA TLSv1.2`, client, backend)
}

func TestNLB_AnIPv6ClientAndDestinationWrittenWithoutBracketsAreDecoded(t *testing.T) {
	ld, logs, err := decodeALB(t, nlbLine("2001:db8:0:1::bea9:51341", "2001:db8:0:2::10:443"))
	require.NoError(t, err)
	got := views(ld)
	require.Len(t, got, 1)
	assert.Equal(t, recordView{client: "2001:db8:0:1::bea9", clientPort: 51341, destination: "2001:db8:0:2::10", destinationPort: 443, hasDestination: true}, got[0])
	assert.Zero(t, logs.Len(), "a good line logs nothing")
}

func TestNLB_TheBracketedAndIPv4FormsStillDecode(t *testing.T) {
	ld, _, err := decodeALB(t,
		nlbLine("192.0.2.10:2817", "198.51.100.7:443"),
		nlbLine("[2001:db8::1]:51341", "[2001:db8::2]:443"),
	)
	require.NoError(t, err)
	got := views(ld)
	require.Len(t, got, 2)
	assert.Equal(t, recordView{client: "192.0.2.10", clientPort: 2817, destination: "198.51.100.7", destinationPort: 443, hasDestination: true}, got[0])
	assert.Equal(t, recordView{client: "2001:db8::1", clientPort: 51341, destination: "2001:db8::2", destinationPort: 443, hasDestination: true}, got[1])
}

// A CLB line with an unbracketed IPv6 address is a bad line: CLB is outside the IPv6 fix until a live CLB line with
// an IPv6 address is on record. The line is skipped and counted, and the rest of the object is kept.
func TestCLB_AnUnbracketedIPv6ClientOrBackendIsSkippedAndCounted(t *testing.T) {
	cases := map[string]string{
		"client":  clbLine("2001:db8:0:1::bea9:2817", "198.51.100.7:80"),
		"backend": clbLine("192.0.2.10:2817", "2001:db8:0:2::10:80"),
		"both":    clbLine("2001:db8:0:1::bea9:2817", "2001:db8:0:2::10:80"),
		"mapped":  clbLine("::ffff:192.0.2.1:2817", "198.51.100.7:80"),
	}
	for name, bad := range cases {
		ld, logs, err := decodeALB(t, clbLine("192.0.2.10:1", "198.51.100.7:80"), bad, clbLine("192.0.2.10:3", "-"))
		require.NoError(t, err, name)
		assert.Len(t, views(ld), 2, name+": the good lines are kept")

		warns := logs.FilterLevelExact(zapcore.WarnLevel).All()
		require.Len(t, warns, 1, name+": one warning for the object")
		assert.Equal(t, int64(1), warns[0].ContextMap()["skipped_lines"], name)
		for _, e := range logs.All() {
			text := e.Message + fmt.Sprint(e.ContextMap())
			assert.NotContains(t, text, "2001:db8", name+": no line content in logs")
		}
	}
}

func TestCLB_TheBracketedAndIPv4FormsStillDecode(t *testing.T) {
	ld, _, err := decodeALB(t,
		clbLine("192.0.2.10:2817", "198.51.100.7:80"),
		clbLine("[2001:db8::1]:2817", "[2001:db8::2]:80"),
	)
	require.NoError(t, err)
	got := views(ld)
	require.Len(t, got, 2)
	assert.Equal(t, recordView{client: "192.0.2.10", clientPort: 2817, destination: "198.51.100.7", destinationPort: 80, hasDestination: true}, got[0])
	assert.Equal(t, recordView{client: "2001:db8::1", clientPort: 2817, destination: "2001:db8::2", destinationPort: 80, hasDestination: true}, got[1])
}

func TestCLB_ADashBackendGivesNoDestination(t *testing.T) {
	ld, _, err := decodeALB(t, clbLine("192.0.2.10:5555", "-"), clbLine("[2001:db8::7]:5555", "-"))
	require.NoError(t, err)
	got := views(ld)
	require.Len(t, got, 2)
	assert.False(t, got[0].hasDestination)
	assert.Equal(t, "192.0.2.10", got[0].client)
	assert.False(t, got[1].hasDestination)
	assert.Equal(t, "2001:db8::7", got[1].client)
}

func TestNLBAndCLB_AMalformedLineIsSkippedAndTheRestOfTheObjectIsKept(t *testing.T) {
	cases := map[string][]string{
		"nlb": {nlbLine("192.0.2.10:2817", "198.51.100.7:443"), nlbLine("not-an-address", "198.51.100.7:443"), nlbLine("2001:db8::bea9:51341", "[2001:db8::2]:443")},
		"clb": {clbLine("192.0.2.10:2817", "198.51.100.7:80"), clbLine("not-an-address", "198.51.100.7:80"), clbLine("[2001:db8::bea9]:2817", "-")},
	}
	for name, lines := range cases {
		ld, logs, err := decodeALB(t, lines...)
		require.NoError(t, err, name)
		assert.Len(t, views(ld), 2, name)

		warns := logs.FilterLevelExact(zapcore.WarnLevel).All()
		require.Len(t, warns, 1, name+": one warning for the object")
		assert.Equal(t, int64(1), warns[0].ContextMap()["skipped_lines"], name)
		for _, e := range logs.All() {
			text := e.Message + fmt.Sprint(e.ContextMap())
			assert.NotContains(t, text, "not-an-address", name)
			assert.NotContains(t, text, "2001:db8", name)
			assert.NotContains(t, text, "192.0.2", name)
		}
	}
}

func TestNLBAndCLB_BadBackendOrDestinationFieldIsSkippedToo(t *testing.T) {
	cases := map[string][]string{
		"nlb": {nlbLine("192.0.2.10:1", "198.51.100.7:443"), nlbLine("192.0.2.10:2", "bad"), nlbLine("192.0.2.10:3", "198.51.100.7:443")},
		"clb": {clbLine("192.0.2.10:1", "198.51.100.7:80"), clbLine("192.0.2.10:2", "bad"), clbLine("192.0.2.10:3", "198.51.100.7:80")},
	}
	for name, lines := range cases {
		ld, logs, err := decodeALB(t, lines...)
		require.NoError(t, err, name)
		assert.Len(t, views(ld), 2, name)
		warns := logs.FilterLevelExact(zapcore.WarnLevel).All()
		require.Len(t, warns, 1, name)
		assert.Equal(t, int64(1), warns[0].ContextMap()["skipped_lines"], name)
	}
}
