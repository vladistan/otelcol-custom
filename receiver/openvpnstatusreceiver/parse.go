package openvpnstatusreceiver

import (
	"bytes"
	"errors"
	"fmt"
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
	commonName     string
	realAddress    string
	connectedSince string
	bytesReceived  int64
	bytesSent      int64
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
		s.noteSkip(s.addClient(strings.Split(line, ",")))
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

// addClient takes the five client columns in v1 order.
func (s *snapshot) addClient(f []string) error {
	if len(f) != 5 {
		return shapeError("client row with %d fields", len(f))
	}
	received, errR := strconv.ParseInt(f[2], 10, 64)
	sent, errS := strconv.ParseInt(f[3], 10, 64)
	if errR != nil || errS != nil {
		return shapeError("byte counts %q, %q", f[2], f[3])
	}
	s.clients = append(s.clients, client{
		commonName: f[0], realAddress: f[1], bytesReceived: received, bytesSent: sent, connectedSince: f[4],
	})
	return nil
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
	return s.addClient(v)
}

func (s *snapshot) addRouteRow(f []string, columns map[string]int) error {
	v, err := pick(f, columns, "Virtual Address", "Common Name", "Real Address", "Last Ref")
	if err != nil {
		return err
	}
	return s.addRoute(v)
}
