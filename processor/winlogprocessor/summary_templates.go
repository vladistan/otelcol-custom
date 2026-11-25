// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.opentelemetry.io/collector/pdata/plog"
)

const (
	fullMessageKey = "vstan.winlog.message"
	// scriptBlockIDChars is how much of a ScriptBlockId the summary shows.
	scriptBlockIDChars = 8
	// pairValueChars cuts a short value (a name or a device) in a summary.
	pairValueChars = 32
)

// eventData is the event data of one record: fields in receiver order, found
// by name with case, spaces and punctuation ignored, or by positional name
// (param1, param2, ...).
type eventData struct {
	fields []dataField
	byName map[string]string
	// message is the rendered message, for the few summaries that read a line of it.
	message string
}

func newEventData(fields []dataField) *eventData {
	d := &eventData{fields: make([]dataField, len(fields)), byName: make(map[string]string, len(fields))}
	for i, f := range fields {
		f.value = validUTF8(f.value)
		d.fields[i] = f
		key := normalizeName(f.key)
		if _, dup := d.byName[key]; !dup {
			d.byName[key] = f.value
		}
	}
	return d
}

func normalizeName(name string) string {
	var sb strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(unicode.ToLower(r))
		}
	}
	return sb.String()
}

// get returns the first non-empty value among the names.
func (d *eventData) get(names ...string) string {
	for _, name := range names {
		if v := strings.TrimSpace(d.byName[normalizeName(name)]); v != "" {
			return v
		}
	}
	return ""
}

// messageLineValue returns the text after prefix on the first line of the
// rendered message that starts with it (case aside), without the final period.
func (d *eventData) messageLineValue(prefix string) string {
	for _, line := range strings.Split(unescapeControls(d.message), "\n") {
		line = strings.TrimSpace(line)
		if len(line) > len(prefix) && strings.EqualFold(line[:len(prefix)], prefix) {
			return strings.TrimRight(strings.TrimSpace(line[len(prefix):]), ". ")
		}
	}
	return ""
}

// unescapeControls turns the literal sequences \r\n, \n, \r and \t into the
// control characters, except inside a path token (a UNC path or a drive path,
// up to the next space, quote, comma, semicolon or closing parenthesis), where
// the backslashes are part of the name.
func unescapeControls(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	boundary := true // the previous byte ends a token or was an unescaped sequence
	for i := 0; i < len(s); {
		if boundary && pathStart(s[i:]) {
			end := i + 1
			for end < len(s) && !strings.ContainsRune(" \t\r\n\"'),;", rune(s[end])) {
				end++
			}
			sb.WriteString(s[i:end])
			i, boundary = end, false
			continue
		}
		if s[i] == '\\' && i+1 < len(s) {
			if rest := s[i+1:]; strings.HasPrefix(rest, "r\\n") {
				sb.WriteString("\r\n")
				i, boundary = i+4, true
				continue
			}
			if c, ok := controlFor(s[i+1]); ok {
				sb.WriteByte(c)
				i, boundary = i+2, true
				continue
			}
		}
		sb.WriteByte(s[i])
		boundary = strings.IndexByte(" \t\r\n\"'(=,;", s[i]) >= 0
		i++
	}
	return sb.String()
}

func controlFor(b byte) (byte, bool) {
	switch b {
	case 'n':
		return '\n', true
	case 'r':
		return '\r', true
	case 't':
		return '\t', true
	}
	return 0, false
}

// pathStart reports a UNC path (\\server) or a drive path (C:\) at the start of s.
func pathStart(s string) bool {
	if len(s) >= 3 && s[0] == '\\' && s[1] == '\\' && s[2] != '\\' && s[2] != ' ' {
		return true
	}
	return len(s) >= 3 && isASCIILetter(s[0]) && s[1] == ':' && s[2] == '\\'
}

func isASCIILetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

// validUTF8 replaces each byte that is not part of a valid UTF-8 sequence with
// U+FFFD and returns a valid string unchanged.
func validUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s) + 8)
	for _, r := range s {
		sb.WriteRune(r) // an invalid byte decodes to U+FFFD, width 1
	}
	return sb.String()
}

// oneLine joins the lines of text with single spaces and cuts it at max runes,
// marking the cut with "...". It stops reading at the cut, so a very long
// script costs no more than a short one.
func oneLine(text string, max int) string {
	var sb strings.Builder
	sb.Grow(min(len(text), max*4+3))
	n, cut, pendingSpace := 0, false, false
	for _, r := range text {
		if unicode.IsSpace(r) {
			pendingSpace = n > 0
			continue
		}
		if pendingSpace {
			if n == max {
				cut = true
				break
			}
			sb.WriteByte(' ')
			n++
			pendingSpace = false
		}
		if n == max {
			cut = true
			break
		}
		sb.WriteRune(r)
		n++
	}
	if cut {
		return strings.TrimRight(sb.String(), " ") + "..."
	}
	return sb.String()
}

type summaryFunc func(d *eventData, s *summarizer) string

type summaryDef struct {
	id       int64
	matches  func(shortProvider string) bool
	severity plog.SeverityNumber // unspecified keeps the severity from the level
	text     string
	// severityFn sets the severity from the event data when it returns ok.
	severityFn func(*eventData) (plog.SeverityNumber, string, bool)
	format     summaryFunc
	// noFullText skips the copy of the message in vstan.winlog.message, for an
	// event whose own event data already holds the large text.
	noFullText bool
}

func providerIs(name string) func(string) bool {
	return func(short string) bool { return short == name }
}

// defaultSummaries is the code table of the seven summarized events. The
// provider is compared in lower case without the Microsoft-Windows- prefix.
// The field names of all seven events come from real event rows. Override a
// template when the events name them otherwise.
var defaultSummaries = []summaryDef{
	{id: 5310, matches: providerIs("grouppolicy"), format: groupPolicy5310},
	{id: 5774, matches: providerIs("netlogon"), severity: plog.SeverityNumberError, text: "ERROR", format: netlogon5774},
	{id: 4104, matches: providerIs("powershell"), severity: plog.SeverityNumberWarn, text: "WARN", format: powerShell4104, noFullText: true},
	{id: 33205, matches: func(short string) bool { return strings.HasPrefix(short, "mssql") }, format: mssqlAudit33205},
	{id: 1151, matches: providerIs("windows defender"), format: defender1151},
	{id: 105, matches: providerIs("kernel-cache"), format: kernelCache105},
	{id: 505, matches: providerIs("storport"), severityFn: storPortSeverity, format: storPort505},
}

// summarizer builds the one-line summaries: the code table, the config
// templates that add to or replace it, the MSSQL action table and the DN
// contraction list.
type summarizer struct {
	templates map[string]string // "provider/id" in lower case; "" disables
	actions   map[string]string
	dnSuffix  []string
	ids       map[int64]struct{} // event ids that have a template, to skip the rest at once
}

func newSummarizer(cfg *Config) (*summarizer, error) {
	s := &summarizer{
		templates: make(map[string]string, len(cfg.Summaries)),
		actions:   defaultMSSQLActions(),
		dnSuffix:  cfg.DNContractions,
		ids:       make(map[int64]struct{}, len(defaultSummaries)+len(cfg.Summaries)),
	}
	for i := range defaultSummaries {
		s.ids[defaultSummaries[i].id] = struct{}{}
	}
	for key, tpl := range cfg.Summaries {
		norm, err := summaryKey(key)
		if err != nil {
			return nil, err
		}
		if err := checkTemplate(tpl); err != nil {
			return nil, fmt.Errorf("summaries[%q]: %w", key, err)
		}
		if _, dup := s.templates[norm]; dup {
			return nil, fmt.Errorf("summaries[%q]: another key names the same provider and event id %q", key, norm)
		}
		s.templates[norm] = tpl
		_, idText, _ := strings.Cut(norm, "/")
		id, _ := strconv.ParseInt(idText, 10, 64)
		s.ids[id] = struct{}{}
	}
	for code, text := range cfg.MSSQLActions {
		if code == "" {
			return nil, fmt.Errorf("mssql_actions: an action code is empty")
		}
		s.actions[code] = text
	}
	for _, suffix := range cfg.DNContractions {
		if suffix == "" {
			return nil, fmt.Errorf("dn_contractions: a suffix is empty")
		}
	}
	return s, nil
}

// summaryKey normalizes "provider/id": the provider in lower case without the
// Microsoft-Windows- prefix.
func summaryKey(key string) (string, error) {
	provider, idText, ok := strings.Cut(key, "/")
	if !ok || strings.TrimSpace(provider) == "" {
		return "", fmt.Errorf("summaries key %q is not provider/event id", key)
	}
	id, err := strconv.ParseUint(strings.TrimSpace(idText), 10, 32)
	if err != nil {
		return "", fmt.Errorf("summaries key %q: invalid event id %q", key, idText)
	}
	return shortProvider(provider) + "/" + strconv.FormatUint(id, 10), nil
}

func shortProvider(provider string) string {
	lower := strings.ToLower(strings.TrimSpace(provider))
	return strings.TrimPrefix(lower, strings.ToLower(windowsProviderPrefix))
}

func checkTemplate(tpl string) error {
	if strings.Count(tpl, "{") != strings.Count(tpl, "}") {
		return fmt.Errorf("template %q has an unbalanced brace", tpl)
	}
	return nil
}

// summaryFor finds the config template or the code definition of an event.
// found is false for an event with neither.
func (s *summarizer) summaryFor(provider string, id int64) (tpl string, def *summaryDef, found bool) {
	if _, ok := s.ids[id]; !ok {
		return "", nil, false
	}
	short := shortProvider(provider)
	if tpl, ok := s.templates[short+"/"+strconv.FormatInt(id, 10)]; ok {
		return tpl, nil, true
	}
	for i := range defaultSummaries {
		if defaultSummaries[i].id == id && defaultSummaries[i].matches(short) {
			return "", &defaultSummaries[i], true
		}
	}
	return "", nil, false
}

// summarize returns the one-line summary of an event. ok is false when the
// event has no template, the template is switched off, or the template fills
// none of its fields.
func (s *summarizer) summarize(provider string, id int64, data []dataField, message string) (string, bool) {
	tpl, def, found := s.summaryFor(provider, id)
	if !found {
		return "", false
	}
	d := newEventData(data)
	d.message = message
	if def == nil {
		line, ok := s.render(tpl, d)
		return line, ok
	}
	line := strings.TrimSpace(def.format(d, s))
	return line, line != ""
}

// keepsFullText reports whether the rendered message is copied to
// vstan.winlog.message when a summary replaces the body. PowerShell 4104 is not:
// ScriptBlockText already holds the script, and a second copy would double it.
func (s *summarizer) keepsFullText(provider string, id int64) bool {
	short := shortProvider(provider)
	for i := range defaultSummaries {
		if defaultSummaries[i].id == id && defaultSummaries[i].matches(short) {
			return !defaultSummaries[i].noFullText
		}
	}
	return true
}

// severityFor returns the severity the code table sets for an event by its
// provider and id (and, for a rule that reads it, the event data), whatever the
// summary did. ok is false for other events.
func (s *summarizer) severityFor(provider string, id int64, data []dataField) (plog.SeverityNumber, string, bool) {
	if _, ok := s.ids[id]; !ok {
		return plog.SeverityNumberUnspecified, "", false
	}
	short := shortProvider(provider)
	for i := range defaultSummaries {
		def := &defaultSummaries[i]
		if def.id != id || !def.matches(short) {
			continue
		}
		if def.severity != plog.SeverityNumberUnspecified {
			return def.severity, def.text, true
		}
		if def.severityFn != nil {
			return def.severityFn(newEventData(data))
		}
	}
	return plog.SeverityNumberUnspecified, "", false
}

// render fills {Field} and {Field|dn} in a config template from the event
// data. ok is false when the template has placeholders and none found a value,
// so the message stays; a template of plain text is always used.
func (s *summarizer) render(tpl string, d *eventData) (line string, ok bool) {
	var sb strings.Builder
	placeholders, filled := 0, 0
	for {
		open := strings.IndexByte(tpl, '{')
		if open < 0 {
			sb.WriteString(tpl)
			break
		}
		closing := strings.IndexByte(tpl[open:], '}')
		if closing < 0 {
			sb.WriteString(tpl)
			break
		}
		sb.WriteString(tpl[:open])
		name, mode, _ := strings.Cut(tpl[open+1:open+closing], "|")
		value := d.get(name)
		if mode == "dn" {
			value = s.contractDN(value)
		}
		placeholders++
		if value != "" {
			filled++
		}
		sb.WriteString(value)
		tpl = tpl[open+closing+1:]
	}
	line = strings.TrimSpace(sb.String())
	return line, line != "" && (placeholders == 0 || filled > 0)
}

// contractDN replaces a listed suffix of a distinguished name with ",...".
func (s *summarizer) contractDN(dn string) string {
	for _, suffix := range s.dnSuffix {
		if len(dn) > len(suffix) && strings.EqualFold(dn[len(dn)-len(suffix):], suffix) {
			return dn[:len(dn)-len(suffix)] + ",..."
		}
	}
	return dn
}

func groupPolicy5310(d *eventData, s *summarizer) string {
	dn := d.get("PrincipalCNName")
	if dn == "" {
		return ""
	}
	line := "Account details: " + s.contractDN(dn)
	if dc := d.get("DCName"); dc != "" {
		line += " via DC " + dc
	}
	return line
}

// netlogon5774 reads the positional parameters of the real event: param1 is
// the DNS record, param2 a message id placeholder (never shown), param3 the DNS
// server address (stays in the event data), param4 the RCODE and param5 the
// status code. The status text is the "Error Value:" line of the message.
func netlogon5774(d *eventData, _ *summarizer) string {
	record, rcode, status := d.get("param1"), d.get("param4"), d.get("param5")
	if record == "" || (rcode == "" && status == "") {
		return ""
	}
	name, rtype := record, ""
	if fields := strings.Fields(record); len(fields) > 2 {
		for i, f := range fields {
			if f == "IN" && i+1 < len(fields) {
				name, rtype = fields[0], fields[i+1]
				break
			}
		}
	}
	line := "DNS registration failed: " + oneLine(name, pairValueChars*4)
	if rtype != "" {
		line += " " + rtype
	}
	var parts []string
	if rcode != "" {
		parts = append(parts, "RCODE "+rcode)
	}
	text := d.messageLineValue("Error Value:")
	switch {
	case status != "" && text != "":
		parts = append(parts, "status "+status+": "+text)
	case status != "":
		parts = append(parts, "status "+status)
	case text != "":
		parts = append(parts, text)
	}
	return line + " (" + strings.Join(parts, ", ") + ")"
}

// powerShell4104 names the script block and the chunk. One script arrives as
// MessageTotal records that share ScriptBlockId, so the summary never carries
// script text: it can hold anything. The text stays in ScriptBlockText.
func powerShell4104(d *eventData, _ *summarizer) string {
	id := strings.Trim(d.get("ScriptBlockId"), "{}")
	if len(id) > scriptBlockIDChars {
		id = id[:scriptBlockIDChars]
	}
	n, total := d.get("MessageNumber"), d.get("MessageTotal")
	if id == "" && (n == "" || total == "") {
		return ""
	}
	line := "PowerShell script block"
	if id != "" {
		line += " " + id
	}
	if n != "" && total != "" {
		line += " chunk " + n + " of " + total
	}
	if path := d.get("Path"); path != "" {
		line += " (" + oneLine(path, pairValueChars*2) + ")"
	}
	return line
}

// auditFields are the few values a SQL Server audit event adds to the summary.
type auditFields struct {
	code, succeeded, principal, client, app string
}

// parseAudit reads the key:value text of an audit event (one pair for each
// line; statement and additional_information run over several lines and hold
// anything). The event, login and client fields come before the statement, so
// they are read only up to the line that starts it and the first one counts.
// application_name comes after the free text, so the last one counts: a line
// inside the statement cannot stand in for either. Nothing else is read.
func parseAudit(text string) auditFields {
	var a auditFields
	inFreeText := false
	for _, line := range strings.Split(unescapeControls(text), "\n") {
		line = strings.TrimRight(line, "\r ")
		if strings.HasPrefix(line, "statement:") {
			inFreeText = true
		}
		if v, ok := strings.CutPrefix(line, "application_name:"); ok {
			a.app = strings.TrimSpace(v)
			continue
		}
		if inFreeText {
			continue
		}
		for _, f := range []struct {
			key string
			dst *string
		}{
			{"action_id:", &a.code}, {"succeeded:", &a.succeeded},
			{"server_principal_name:", &a.principal}, {"client_ip:", &a.client},
		} {
			if v, ok := strings.CutPrefix(line, f.key); ok && *f.dst == "" {
				*f.dst = strings.TrimSpace(v)
			}
		}
	}
	return a
}

// mssqlAudit33205 reads the one parameter of the real event, which holds the
// whole key:value text (the message when the parameter is missing). A failed
// action whose text does not already say so gets "failed"; an action code the
// table lacks is shown alone.
func mssqlAudit33205(d *eventData, s *summarizer) string {
	text := d.get("param1")
	if text == "" {
		text = d.message
	}
	a := parseAudit(text)
	if a.code == "" && a.principal == "" {
		return ""
	}
	failed := strings.EqualFold(a.succeeded, "false")
	var line string
	switch action := s.actions[a.code]; {
	case a.code == "":
		line = "Audit action"
	case action == "":
		line = a.code
		if failed {
			line += " failed"
		}
	default:
		line = action + " (" + a.code + ")"
		lower := strings.ToLower(action)
		if failed && !strings.Contains(lower, "fail") && !strings.Contains(lower, "succeed") {
			line += " failed"
		}
	}
	if a.principal != "" {
		line += " " + a.principal
	}
	if a.app != "" {
		line += " app=" + a.app
	}
	if a.client != "" {
		line += " client=" + a.client
	}
	return line
}

func defaultMSSQLActions() map[string]string {
	return map[string]string{
		"LGIS": "Login succeeded", "LGIF": "Login failed", "LGO": "Logout",
		"DBAS": "Database authentication succeeded", "DBAF": "Database authentication failed",
		"SL": "Select", "IN": "Insert", "UP": "Update", "DL": "Delete", "EX": "Execute",
		"CR": "Create", "AL": "Alter", "DR": "Drop", "BA": "Backup", "RS": "Restore",
	}
}

// defender1151 summarizes the Defender health report in one line: the product
// status, the four protection states, the age of the definitions and the age of
// the last quick scan, both in days. The event data has one key per line of the
// message, with spaces in the names; the age keys are "Last AV security
// intelligence age" (or the AS one when the AV one is absent), not the body
// labels. A value of 4294967295 (0xFFFFFFFF) means the scan never ran and shows
// as "never". With none of these fields the body stays.
func defender1151(d *eventData, _ *summarizer) string {
	var parts []string
	if status := d.get("Product status"); status != "" {
		parts = append(parts, "product status "+oneLine(status, pairValueChars))
	}
	states := []struct{ label, value string }{
		{"RTP", d.get("RTP state")},
		{"OA", d.get("OA state")},
		{"IOAV", d.get("IOAV state")},
		{"BM", d.get("BM state")},
	}
	if protection := joinStates(states); protection != "" {
		parts = append(parts, protection)
	}
	if age, ok := defenderAge(d.get("Last AV security intelligence age", "Last AS security intelligence age")); ok {
		parts = append(parts, "definitions age "+strings.TrimSuffix(age, " ago"))
	}
	if age, ok := defenderAge(d.get("Last quick scan age")); ok {
		parts = append(parts, "last quick scan "+age)
	}
	if len(parts) == 0 {
		return ""
	}
	return "Defender " + strings.Join(parts, ", ")
}

// defenderAge renders an age in days from a Defender report: "4 d ago", or
// "never" for the sentinel 4294967295 that means the scan never ran. A value that
// is not a number gives ok false, so no junk reaches the summary.
func defenderAge(value string) (text string, ok bool) {
	if value == "" {
		return "", false
	}
	if strings.EqualFold(value, "0xffffffff") {
		return "never", true
	}
	days, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return "", false
	}
	if days == 4294967295 {
		return "never", true
	}
	return strconv.FormatUint(days, 10) + " d ago", true
}

// joinStates writes "RTP/OA/IOAV/BM Enabled" when every state agrees and
// "RTP Enabled, OA Disabled" otherwise; states the event lacks are left out.
func joinStates(states []struct{ label, value string }) string {
	var labels, items []string
	same := true
	for _, st := range states {
		if st.value == "" {
			continue
		}
		labels = append(labels, st.label)
		items = append(items, st.label+" "+st.value)
		same = same && st.value == states[firstSet(states)].value
	}
	switch {
	case len(labels) == 0:
		return ""
	case same:
		return strings.Join(labels, "/") + " " + states[firstSet(states)].value
	}
	return strings.Join(items, ", ")
}

func firstSet(states []struct{ label, value string }) int {
	for i, st := range states {
		if st.value != "" {
			return i
		}
	}
	return 0
}

// cacheBucketLabels are the 12 latency buckets of a Kernel-Cache 105 event, used
// when the message does not list them.
var cacheBucketLabels = []string{
	"256us", "1ms", "4ms", "16ms", "64ms", "128ms", "256ms",
	"2000ms", "6000ms", "10000ms", "20000ms", "20000+ms",
}

// storPort505 summarizes the periodic StorPort device report in one line: the
// port, path, target and LUN, the trimmed vendor and product, the total IO, the
// bytes read and written, the worst latency in ms (the event counts 100 ns
// ticks) and, when above 0, the failed IO. The serial number is never shown.
// A missing or non-numeric port number, total or optional figure gives no
// summary, so the body stays.
func storPort505(d *eventData, _ *summarizer) string {
	var ids [4]int64
	for i, key := range []string{"PortNumber", "PathID", "TargetID", "LUN"} {
		n, ok := nonNegative(d.get(key))
		if !ok {
			return ""
		}
		ids[i] = n
	}
	total, ok := nonNegative(d.get("TotalIoCount"))
	if !ok {
		return ""
	}
	failed, ok := storPortFailedIO(d)
	if !ok {
		return ""
	}
	line := fmt.Sprintf("StorPort %d:%d:%d:%d", ids[0], ids[1], ids[2], ids[3])
	if device := strings.TrimSpace(oneLine(d.get("VendorId"), pairValueChars) + " " + oneLine(d.get("ProductId"), pairValueChars)); device != "" {
		line += " " + device
	}
	line += fmt.Sprintf(": %d IO", total)
	for _, part := range []struct{ key, label string }{{"TotalReadBytes", "read"}, {"TotalWriteBytes", "written"}} {
		raw := d.get(part.key)
		if raw == "" {
			continue
		}
		n, ok := nonNegative(raw)
		if !ok {
			return ""
		}
		line += ", " + part.label + " " + formatBinaryBytes(n)
	}
	if raw := d.get("MaxReadWriteLatency_100ns"); raw != "" {
		ticks, ok := nonNegative(raw)
		if !ok {
			return ""
		}
		line += fmt.Sprintf(", max latency %.1f ms", float64(ticks)/10000)
	}
	if failed > 0 {
		line += fmt.Sprintf(", %d failed IO", failed)
	}
	return line
}

// storPortFailedIO sums the 12 failed IO buckets. A missing bucket counts as
// 0; a bucket that is not a non-negative number makes ok false.
func storPortFailedIO(d *eventData) (total int64, ok bool) {
	for i := 1; i <= 12; i++ {
		raw := d.get("BucketIoFailed" + strconv.Itoa(i))
		if raw == "" {
			continue
		}
		n, valid := nonNegative(raw)
		if !valid {
			return 0, false
		}
		total += n
	}
	return total, true
}

// storPortSeverity raises a StorPort 505 report with failed I/O above zero to
// WARN. Without failed I/O, or when a count cannot be read, the severity of
// the event level stays.
func storPortSeverity(d *eventData) (plog.SeverityNumber, string, bool) {
	if failed, ok := storPortFailedIO(d); ok && failed > 0 {
		return plog.SeverityNumberWarn, "WARN", true
	}
	return plog.SeverityNumberUnspecified, "", false
}

// nonNegative parses a decimal count of at least 0.
func nonNegative(s string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n, err == nil && n >= 0
}

// kernelCache105 summarizes the periodic cache write latency report: the period
// in seconds, the blocking sync write count and the slowest bucket with a count.
// A missing or non-numeric field gives no summary. The 48 bucket values stay in
// the event data.
func kernelCache105(d *eventData, _ *summarizer) string {
	micros, err := strconv.ParseInt(d.get("PeriodDurationMicroSec"), 10, 64)
	if err != nil || micros < 0 {
		return ""
	}
	var total int64
	slowest := -1
	for i := range cacheBucketLabels {
		n, err := strconv.ParseInt(d.get("SynchronousWriteIoCountsBucket"+strconv.Itoa(i+1)), 10, 64)
		if err != nil || n < 0 {
			return ""
		}
		total += n
		if n > 0 {
			slowest = i
		}
	}
	head := fmt.Sprintf("Cache write latency over %d s: ", micros/1_000_000)
	if total == 0 {
		return head + "no write IO"
	}
	return fmt.Sprintf("%s%d sync writes, slowest bucket %s", head, total, bucketLabel(d, slowest))
}

// bucketLabel is the label of bucket i from the message's "Latency Bucket
// Values" list, or from the fixed table when the message lacks it.
func bucketLabel(d *eventData, i int) string {
	list := strings.Trim(d.messageLineValue("Latency Bucket Values:"), "[] ")
	if labels := strings.Split(list, ","); len(labels) == len(cacheBucketLabels) {
		if label := strings.TrimSpace(labels[i]); label != "" {
			return label
		}
	}
	return cacheBucketLabels[i]
}

// formatBinaryBytes writes n with a binary unit and one decimal: "108.0 KiB".
// Under 1024 it is a plain "512 B".
func formatBinaryBytes(n int64) string {
	if n < 1024 {
		return strconv.FormatInt(n, 10) + " B"
	}
	value, unit := float64(n), "B"
	for _, u := range []string{"KiB", "MiB", "GiB", "TiB"} {
		value /= 1024
		unit = u
		if value < 1024 {
			break
		}
	}
	return strconv.FormatFloat(value, 'f', 1, 64) + " " + unit
}
