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
		{commonName: "alice", realAddress: "203.0.113.5:51234", bytesReceived: 1000, bytesSent: 2000, connectedSince: "2026-11-13 09:00:00"},
		{commonName: "bob", realAddress: "198.51.100.7:40222", bytesReceived: 3000, bytesSent: 4000, connectedSince: "2026-11-13 09:30:00"},
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
				commonName: "alice", realAddress: "203.0.113.5:51234",
				bytesReceived: 1000, bytesSent: 2000, connectedSince: "2026-11-13 09:00:00",
			}, snap.clients[0])
			assert.Equal(t, client{
				commonName: "bob", realAddress: "198.51.100.7:40222",
				bytesReceived: 3000, bytesSent: 4000, connectedSince: "2026-11-13 09:30:00",
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

	want, perr := time.ParseInLocation("Mon Jan _2 15:04:05 2006", "Thu Jan  3 21:40:24 2019", time.Local)
	require.NoError(t, perr)
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
