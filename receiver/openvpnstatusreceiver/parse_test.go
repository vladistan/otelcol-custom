package openvpnstatusreceiver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return data
}

func localTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.ParseInLocation(statusTimeLayout, value, time.Local)
	require.NoError(t, err)
	return parsed
}

func TestParseSnapshotV1TwoClientsReturnsBothClients(t *testing.T) {
	snap, err := parseSnapshot(readFixture(t, "v1-two-clients.txt"))
	require.NoError(t, err)

	assert.Equal(t, "2026-11-13 10:00:00", snap.identity)
	assert.True(t, localTime(t, "2026-11-13 10:00:00").Equal(snap.at))
	assert.Equal(t, []client{
		{commonName: "alice", realAddress: "203.0.113.5:51234", ip: "203.0.113.5", port: 51234, bytesReceived: 1000, bytesSent: 2000,
			connectedSince: "2026-11-13 09:00:00", connectedAt: localTime(t, "2026-11-13 09:00:00")},
		{commonName: "bob", realAddress: "198.51.100.7:40222", ip: "198.51.100.7", port: 40222, bytesReceived: 3000, bytesSent: 4000,
			connectedSince: "2026-11-13 09:30:00", connectedAt: localTime(t, "2026-11-13 09:30:00")},
	}, snap.clients)
	assert.Equal(t, []route{
		{virtualAddress: "10.8.0.2", commonName: "alice", realAddress: "203.0.113.5:51234", lastRef: "2026-11-13 09:59:50"},
		{virtualAddress: "10.8.0.3", commonName: "bob", realAddress: "198.51.100.7:40222", lastRef: "2026-11-13 09:59:55"},
	}, snap.routes)
}

func TestParseSnapshotV2AndV3EveryLayoutMatchesTheV1Clients(t *testing.T) {
	for _, name := range []string{"v2-csv.txt", "v2-tsv.txt", "v3-csv.txt", "v3-tsv.txt"} {
		t.Run(name, func(t *testing.T) {
			snap, err := parseSnapshot(readFixture(t, name))
			require.NoError(t, err)

			assert.Equal(t, "1794564000", snap.identity)
			assert.True(t, time.Unix(1794564000, 0).Equal(snap.at))
			require.Len(t, snap.clients, 2)
			assert.Equal(t, client{
				commonName: "alice", realAddress: "203.0.113.5:51234", ip: "203.0.113.5", port: 51234,
				bytesReceived: 1000, bytesSent: 2000, connectedSince: "2026-11-13 09:00:00",
				connectedAt: time.Unix(1794560400, 0),
			}, snap.clients[0])
			assert.Equal(t, client{
				commonName: "bob", realAddress: "198.51.100.7:40222", ip: "198.51.100.7", port: 40222,
				bytesReceived: 3000, bytesSent: 4000, connectedSince: "2026-11-13 09:30:00",
				connectedAt: time.Unix(1794562200, 0),
			}, snap.clients[1])
			require.Len(t, snap.routes, 2)
			assert.Equal(t, route{
				virtualAddress: "10.8.0.2", commonName: "alice",
				realAddress: "203.0.113.5:51234", lastRef: "2026-11-13 09:59:50",
			}, snap.routes[0])
		})
	}
}

func TestParseSnapshotZeroClientsKeepsTheIdentity(t *testing.T) {
	snap, err := parseSnapshot(readFixture(t, "v1-zero-clients.txt"))
	require.NoError(t, err)

	assert.Equal(t, "2026-11-13 10:01:00", snap.identity)
	assert.Empty(t, snap.clients)
}

func TestParseSnapshotUndefClientKeepsTheName(t *testing.T) {
	snap, err := parseSnapshot(readFixture(t, "v1-undef.txt"))
	require.NoError(t, err)

	require.Len(t, snap.clients, 1)
	assert.Equal(t, "UNDEF", snap.clients[0].commonName)
}

func TestParseSnapshotSkippedInputReturnsItsError(t *testing.T) {
	tests := []struct {
		fixture string
		wantErr error
	}{
		{"v1-empty.txt", errEmpty},
		{"v1-header-only.txt", errIncomplete},
		{"v1-partial.txt", errIncomplete},
		{"v1-footer-only.txt", errUnknownShape},
		{"unknown-shape.txt", errUnknownShape},
		{"v1-bad-updated.txt", errUnknownShape},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			snap, err := parseSnapshot(readFixture(t, tt.fixture))

			require.ErrorIs(t, err, tt.wantErr)
			assert.Empty(t, snap.clients)
		})
	}
}

func TestParseSnapshotV1BothUpdatedLayoutsParse(t *testing.T) {
	snap, err := parseSnapshot(readFixture(t, "v1-old-updated.txt"))
	require.NoError(t, err)

	want, ipErr := time.ParseInLocation("Mon Jan _2 15:04:05 2006", "Thu Jan  3 21:40:24 2019", time.Local)
	require.NoError(t, ipErr)
	assert.True(t, want.Equal(snap.at))
	assert.Equal(t, "Thu Jan  3 21:40:24 2019", snap.identity)
	require.Len(t, snap.clients, 2)
	assert.Equal(t, "alice", snap.clients[0].commonName)
}

func TestParseSnapshotBadRowIsSkippedAndTheRestKept(t *testing.T) {
	tests := []struct {
		fixture     string
		wantClients []string
		wantSkipped int
	}{
		{"v1-six-field-row.txt", []string{"alice", "carol"}, 1},
		{"v1-bad-bytes.txt", nil, 1},
		{"v1-short-row.txt", nil, 1},
		{"v2-new-section.txt", []string{"alice", "bob"}, 1},
		{"v2-unknown-row.txt", nil, 1},
		{"v2-no-header.txt", nil, 1},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			snap, err := parseSnapshot(readFixture(t, tt.fixture))
			require.NoError(t, err)

			var names []string
			for _, c := range snap.clients {
				names = append(names, c.commonName)
			}
			assert.Equal(t, tt.wantClients, names)
			assert.Len(t, snap.skipped, tt.wantSkipped)
		})
	}
}

func TestParseSnapshotCleanFileSkipsNothing(t *testing.T) {
	snap, err := parseSnapshot(readFixture(t, "v1-two-clients.txt"))
	require.NoError(t, err)

	assert.Empty(t, snap.skipped)
}

func TestParseSnapshotLeadingByteOrderMarkIsIgnored(t *testing.T) {
	for _, name := range []string{"v1-two-clients.txt", "v2-csv.txt"} {
		t.Run(name, func(t *testing.T) {
			plain, err := parseSnapshot(readFixture(t, name))
			require.NoError(t, err)

			marked, err := parseSnapshot(append([]byte("\xef\xbb\xbf"), readFixture(t, name)...))
			require.NoError(t, err)

			assert.Equal(t, plain, marked)
		})
	}
}

func TestParseSnapshotCRLFLineEndingsParseLikeLF(t *testing.T) {
	data := readFixture(t, "v1-two-clients.txt")
	crlf := make([]byte, 0, len(data)+32)
	for _, b := range data {
		if b == '\n' {
			crlf = append(crlf, '\r')
		}
		crlf = append(crlf, b)
	}

	snap, err := parseSnapshot(crlf)
	require.NoError(t, err)

	require.Len(t, snap.clients, 2)
	assert.Equal(t, "2026-11-13 09:30:00", snap.clients[1].connectedSince)
}

func TestIPOfStripsThePortAndTheProtocolPrefix(t *testing.T) {
	tests := map[string]string{
		"203.0.113.5:51234":         "203.0.113.5",
		"[2001:db8::1]:1194":        "2001:db8::1",
		"udp4:192.0.2.9:5000":       "192.0.2.9",
		"udp6:[2001:db8::2]:1195":   "2001:db8::2",
		"tcp4-server:192.0.2.9:80":  "192.0.2.9",
		"203.0.113.5":               "203.0.113.5",
		"2001:db8::3":               "2001:db8::3",
		"::ffff:192.0.2.1":          "192.0.2.1",
		"[::ffff:192.0.2.1]:1194":   "192.0.2.1",
		"udp6:[::ffff:192.0.2.1]:1": "192.0.2.1",
	}
	for in, want := range tests {
		t.Run(in, func(t *testing.T) {
			got, err := ipOf(in)

			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
}

func TestIPOfRejectsWhatIsNotAnAddress(t *testing.T) {
	for _, in := range []string{"", "not-an-address", "host.example:1194", ":1194", "203.0.113.5:port", "999.1.1.1:5"} {
		t.Run(in, func(t *testing.T) {
			_, err := ipOf(in)

			assert.Error(t, err)
		})
	}
}

func TestParseSnapshotClientWithAnUnreadableAddressIsSkippedAndTheOthersStay(t *testing.T) {
	snap, err := parseSnapshot(readFixture(t, "v1-bad-address.txt"))
	require.NoError(t, err)

	require.Len(t, snap.clients, 1)
	assert.Equal(t, "alice", snap.clients[0].commonName)
	require.Len(t, snap.skipped, 1)
	assert.Contains(t, snap.skipped[0], "real address")
	assert.NotContains(t, snap.skipped[0], "not-an-address", "the warning carries no address text")
}

func TestParseSnapshotIPv6AndPrefixedAddressesGiveTheBareIP(t *testing.T) {
	snap, err := parseSnapshot(readFixture(t, "v1-ipv6.txt"))
	require.NoError(t, err)

	require.Len(t, snap.clients, 3)
	assert.Equal(t, "2001:db8::1", snap.clients[0].ip)
	assert.Equal(t, "2001:db8::2", snap.clients[1].ip)
	assert.Equal(t, "192.0.2.9", snap.clients[2].ip)
	assert.Equal(t, "[2001:db8::1]:1194", snap.clients[0].realAddress, "the routing join keeps the full address")
}

func TestParseSnapshotUnreadableConnectedSinceKeepsTheClientWithoutAStart(t *testing.T) {
	snap, err := parseSnapshot(readFixture(t, "v1-bad-since.txt"))
	require.NoError(t, err)

	require.Len(t, snap.clients, 1)
	assert.True(t, snap.clients[0].connectedAt.IsZero())
	assert.Equal(t, "yesterday", snap.clients[0].connectedSince)
	assert.Empty(t, snap.skipped)
}
