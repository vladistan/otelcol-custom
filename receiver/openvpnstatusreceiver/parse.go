package openvpnstatusreceiver

import (
	"bytes"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

const (
	statusTimeLayout    = "2006-01-02 15:04:05"
	oldStatusTimeLayout = "Mon Jan _2 15:04:05 2006"

	byteOrderMark = "\xef\xbb\xbf"

	v1Title         = "OpenVPN CLIENT LIST"
	v1ClientHeader  = "Common Name,Real Address,Bytes Received,Bytes Sent,Connected Since"
	v1RoutingTitle  = "ROUTING TABLE"
	v1RoutingHeader = "Virtual Address,Common Name,Real Address,Last Ref"
	v1StatsTitle    = "GLOBAL STATS"
	endLine         = "END"
)

var (
	errEmpty        = errors.New("status file is empty")
	errIncomplete   = errors.New("snapshot has no END line")
	errUnknownShape = errors.New("unknown status line shape")
)

type client struct {
	commonName string
	// realAddress is the address and port as written, the key of the routing join.
	realAddress string
	// ip is realAddress without the port; metrics carry only this form.
	ip string
	// port is the port of realAddress, or 0 when the row gives none; logs carry it as client.port.
	port           int64
	connectedSince string
	// connectedAt is zero when the file gives no readable start time.
	connectedAt   time.Time
	bytesReceived int64
	bytesSent     int64
}

type route struct {
	virtualAddress string
	commonName     string
	realAddress    string
	lastRef        string
}

type snapshot struct {
	identity string
	at       time.Time
	clients  []client
	routes   []route
	// skipped describes each row left out because it could not be parsed.
	skipped []string
}

// noteSkip records a row-level error; the row is dropped, the snapshot stays.
func (s *snapshot) noteSkip(err error) {
	if err != nil {
		s.skipped = append(s.skipped, err.Error())
	}
}

// parseSnapshot returns a complete snapshot or an error; a caller never sees a
// half-parsed one, so a file cut mid-write yields no records. A row that does
// not parse is skipped and listed in skipped.
func parseSnapshot(data []byte) (snapshot, error) {
	data = bytes.TrimPrefix(data, []byte(byteOrderMark))
	lines := nonBlankLines(data)
	if len(lines) == 0 {
		return snapshot{}, errEmpty
	}
	first := lines[0]
	switch {
	case first == v1Title:
		return parseV1(lines)
	case strings.HasPrefix(first, "TITLE"), strings.HasPrefix(first, "TIME"), strings.HasPrefix(first, "HEADER"):
		return parseV2(lines)
	default:
		return snapshot{}, shapeError("first line %q", first)
	}
}

func shapeError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errUnknownShape, fmt.Sprintf(format, args...))
}

func nonBlankLines(data []byte) []string {
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func parseV1(lines []string) (snapshot, error) {
	var snap snapshot
	section := v1Title
	for _, line := range lines {
		switch line {
		case v1Title, v1RoutingTitle, v1StatsTitle:
			section = line
			continue
		case endLine:
			if snap.identity == "" {
				return snapshot{}, shapeError("no Updated line before END")
			}
			return snap, nil
		}
		if err := snap.addV1Line(section, line); err != nil {
			return snapshot{}, err
		}
	}
	return snapshot{}, errIncomplete
}

func (s *snapshot) addV1Line(section, line string) error {
	switch section {
	case v1Title:
		if updated, ok := strings.CutPrefix(line, "Updated,"); ok && s.identity == "" {
			return s.setTime(updated)
		}
		if line == v1ClientHeader {
			return nil
		}
		s.noteSkip(s.addClient(strings.Split(line, ","), ""))
	case v1RoutingTitle:
		if line == v1RoutingHeader {
			return nil
		}
		s.noteSkip(s.addRoute(strings.Split(line, ",")))
	}
	return nil
}

// setTime reads a zone-less server-local date in either layout; the text is
// also the identity.
func (s *snapshot) setTime(value string) error {
	at, err := time.ParseInLocation(statusTimeLayout, value, time.Local)
	if err != nil {
		at, err = time.ParseInLocation(oldStatusTimeLayout, value, time.Local)
	}
	if err != nil {
		return shapeError("time %q", value)
	}
	s.identity, s.at = value, at
	return nil
}

// addClient takes the five client columns in v1 order; epoch is the optional
// "Connected Since (time_t)" column of v2 and v3, empty in v1.
func (s *snapshot) addClient(f []string, epoch string) error {
	if len(f) != 5 {
		return shapeError("client row with %d fields", len(f))
	}
	received, errR := strconv.ParseInt(f[2], 10, 64)
	sent, errS := strconv.ParseInt(f[3], 10, 64)
	if errR != nil || errS != nil {
		return shapeError("byte counts %q, %q", f[2], f[3])
	}
	ip, err := ipOf(f[1])
	if err != nil {
		return shapeError("real address is not an IP address")
	}
	s.clients = append(s.clients, client{
		commonName: f[0], realAddress: f[1], ip: ip, port: portOf(f[1]), bytesReceived: received, bytesSent: sent,
		connectedSince: f[4], connectedAt: connectedAt(f[4], epoch),
	})
	return nil
}

// connectedAt prefers the epoch column, which carries no zone ambiguity; else it
// reads the date text as server-local time, like the snapshot time. An unreadable
// value gives the zero time, and the client is still kept.
func connectedAt(text, epoch string) time.Time {
	if seconds, err := strconv.ParseInt(epoch, 10, 64); err == nil && seconds > 0 {
		return time.Unix(seconds, 0)
	}
	for _, layout := range []string{statusTimeLayout, oldStatusTimeLayout} {
		if at, err := time.ParseInLocation(layout, text, time.Local); err == nil {
			return at
		}
	}
	return time.Time{}
}

// ipOf returns the address without the port, an IPv4-mapped IPv6 address as
// plain IPv4. OpenVPN writes ip:port, [ip6]:port and, in some builds, a protocol
// prefix such as udp4: before either.
func ipOf(address string) (string, error) {
	for candidate, tries := address, 0; tries < 2; tries++ {
		if ap, err := netip.ParseAddrPort(candidate); err == nil {
			return ap.Addr().Unmap().String(), nil
		}
		if ip, err := netip.ParseAddr(candidate); err == nil {
			return ip.Unmap().String(), nil
		}
		prefix, rest, found := strings.Cut(candidate, ":")
		if !found || !isProtocolToken(prefix) {
			break
		}
		candidate = rest
	}
	return "", fmt.Errorf("not an IP address: %q", address)
}

// portOf returns the port of an address written as ip:port or [ip6]:port, after the same protocol prefix
// ipOf accepts. It is 0 when the address has no port.
func portOf(address string) int64 {
	for candidate, tries := address, 0; tries < 2; tries++ {
		if ap, err := netip.ParseAddrPort(candidate); err == nil {
			return int64(ap.Port())
		}
		prefix, rest, found := strings.Cut(candidate, ":")
		if !found || !isProtocolToken(prefix) {
			break
		}
		candidate = rest
	}
	return 0
}

func isProtocolToken(token string) bool {
	if token == "" {
		return false
	}
	for _, r := range token {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// addRoute takes the four routing columns in v1 order.
func (s *snapshot) addRoute(f []string) error {
	if len(f) != 4 {
		return shapeError("routing row with %d fields", len(f))
	}
	s.routes = append(s.routes, route{virtualAddress: f[0], commonName: f[1], realAddress: f[2], lastRef: f[3]})
	return nil
}

// parseV2 reads status versions 2 and 3. Both use the same rows; only the
// separator differs, so columns are found by their HEADER names.
func parseV2(lines []string) (snapshot, error) {
	sep := ","
	if strings.Contains(lines[0], "\t") {
		sep = "\t"
	}
	columns := map[string]map[string]int{}
	var snap snapshot
	for _, line := range lines {
		f := strings.Split(line, sep)
		switch f[0] {
		case "TITLE", "GLOBAL_STATS":
		case "TIME":
			if err := snap.setEpochTime(f); err != nil {
				return snapshot{}, err
			}
		case "HEADER":
			snap.noteSkip(recordColumns(columns, f))
		case "CLIENT_LIST":
			snap.noteSkip(snap.addClientRow(f, columns[f[0]]))
		case "ROUTING_TABLE":
			snap.noteSkip(snap.addRouteRow(f, columns[f[0]]))
		case endLine:
			if snap.identity == "" {
				return snapshot{}, shapeError("no TIME row before END")
			}
			return snap, nil
		default:
			snap.noteSkip(shapeError("row type %q", f[0]))
		}
	}
	return snapshot{}, errIncomplete
}

// setEpochTime prefers the epoch column: the date column carries no zone.
func (s *snapshot) setEpochTime(f []string) error {
	if len(f) < 3 {
		return s.setTime(f[len(f)-1])
	}
	epoch, err := strconv.ParseInt(f[2], 10, 64)
	if err != nil {
		return shapeError("TIME epoch %q", f[2])
	}
	s.identity, s.at = f[2], time.Unix(epoch, 0)
	return nil
}

// recordColumns maps a HEADER row's names to indexes in the data rows, which
// lack the leading HEADER cell.
func recordColumns(columns map[string]map[string]int, header []string) error {
	if len(header) < 2 {
		return shapeError("HEADER row without a table name")
	}
	names := make(map[string]int, len(header))
	for i, name := range header[2:] {
		names[name] = i + 1
	}
	columns[header[1]] = names
	return nil
}

func pick(f []string, columns map[string]int, names ...string) ([]string, error) {
	if columns == nil {
		return nil, shapeError("%s row before its HEADER row", f[0])
	}
	picked := make([]string, 0, len(names))
	for _, name := range names {
		i, ok := columns[name]
		if !ok || i >= len(f) {
			return nil, shapeError("%s row has no %q column", f[0], name)
		}
		picked = append(picked, f[i])
	}
	return picked, nil
}

func (s *snapshot) addClientRow(f []string, columns map[string]int) error {
	v, err := pick(f, columns, "Common Name", "Real Address", "Bytes Received", "Bytes Sent", "Connected Since")
	if err != nil {
		return err
	}
	return s.addClient(v, optionalColumn(f, columns, "Connected Since (time_t)"))
}

// optionalColumn is the cell of a column a version may lack, or "".
func optionalColumn(f []string, columns map[string]int, name string) string {
	if i, ok := columns[name]; ok && i < len(f) {
		return f[i]
	}
	return ""
}

func (s *snapshot) addRouteRow(f []string, columns map[string]int) error {
	v, err := pick(f, columns, "Virtual Address", "Common Name", "Real Address", "Last Ref")
	if err != nil {
		return err
	}
	return s.addRoute(v)
}
