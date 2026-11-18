// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package pvereceiver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

const (
	maxBackoff       = 24 * time.Hour
	factBody         = "pve guest fact"
	agentUnavailable = "unavailable"
	agentAvailable   = "available"
)

// initialDelay is the wait before the first run of a scraper, so Start returns before any network call.
// A variable so tests can shorten or lengthen it.
var initialDelay = time.Second

// startOffset is initialDelay plus up to a tenth of the interval, so scrapers do not start together.
func startOffset(interval time.Duration, rnd *rand.Rand) time.Duration {
	spread := int64(interval / 10)
	if spread <= 0 {
		return initialDelay
	}
	return initialDelay + time.Duration(rnd.Int63n(spread))
}

// backoffFor is the wait after the n-th failure in a row: the interval, doubling, never over a day.
func backoffFor(base time.Duration, failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	d := base
	for i := 1; i < failures; i++ {
		d *= 2
		if d >= maxBackoff || d <= 0 {
			return maxBackoff
		}
	}
	return min(d, maxBackoff)
}

func shortHostname(host string) string {
	name, _, _ := strings.Cut(host, ".")
	return name
}

// hostnameFn is os.Hostname; a variable so a test can make it fail.
var hostnameFn = os.Hostname

// localNode is the short host name; it is an error when the host name cannot be read or is empty.
func localNode() (string, error) {
	host, err := hostnameFn()
	if err != nil {
		return "", fmt.Errorf("read the host name: %w", err)
	}
	node := shortHostname(host)
	if node == "" {
		return "", errors.New("the host name is empty")
	}
	return node, nil
}

type guestState struct {
	failures    int
	nextTry     time.Time
	facts       map[string]string // facts delivered so far
	unavailable bool              // whether agent=unavailable was delivered
}

// guestPending holds what one sweep found and changes the delivered state only after commit: a lost send
// then repeats the change records at the next sweep instead of losing them.
type guestPending struct {
	facts         map[string]string
	unavailableTo *bool
}

type guestEntry struct {
	VMID   int             `json:"vmid"`
	Name   string          `json:"name"`
	Status string          `json:"status"`
	Lock   json.RawMessage `json:"lock"` // config lock held by a task such as backup; absent when none
}

// lockState returns the trimmed lock name, or "" when there is none or the value is not a string.
func (g guestEntry) lockState() string {
	var s string
	if json.Unmarshal(g.Lock, &s) != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

type guestsScraper struct {
	logger   *zap.Logger
	client   *apiClient
	node     string
	interval time.Duration
	timeout  time.Duration
	state    map[int]*guestState
	pending  map[int]*guestPending
}

func newGuestsScraper(logger *zap.Logger, cfg *Config, client *apiClient, node string) *guestsScraper {
	return &guestsScraper{
		logger:   logger,
		client:   client,
		node:     node,
		interval: cfg.Guests.CollectionInterval,
		timeout:  cfg.Guests.Timeout,
		state:    map[int]*guestState{},
		pending:  map[int]*guestPending{},
	}
}

func (s *guestsScraper) pendingFor(vmid int) *guestPending {
	p, ok := s.pending[vmid]
	if !ok {
		p = &guestPending{facts: map[string]string{}}
		s.pending[vmid] = p
	}
	return p
}

// commit records the facts of the last sweep as delivered.
func (s *guestsScraper) commit() {
	for vmid, p := range s.pending {
		st := s.stateFor(vmid)
		for k, v := range p.facts {
			st.facts[k] = v
		}
		if p.unavailableTo != nil {
			st.unavailable = *p.unavailableTo
		}
	}
	s.pending = map[int]*guestPending{}
}

// discard forgets the facts of the last sweep, which were not delivered, so the next sweep finds them again.
// A guest whose news was only the agent state is tried again at the next sweep, not after its backoff.
func (s *guestsScraper) discard() {
	for vmid, p := range s.pending {
		if p.unavailableTo != nil {
			s.stateFor(vmid).nextTry = time.Time{}
		}
	}
	s.pending = map[int]*guestPending{}
}

// nodePath builds a path below /nodes/<node>, with the node name escaped.
func (s *guestsScraper) nodePath(format string, args ...any) string {
	return "/nodes/" + url.PathEscape(s.node) + fmt.Sprintf(format, args...)
}

func (s *guestsScraper) stateFor(vmid int) *guestState {
	st, ok := s.state[vmid]
	if !ok {
		st = &guestState{facts: map[string]string{}}
		s.state[vmid] = st
	}
	return st
}

type agentFS struct {
	Name       string `json:"name"`
	Mountpoint string `json:"mountpoint"`
	Type       string `json:"type"`
	Used       *int64 `json:"used-bytes"`
	Total      *int64 `json:"total-bytes"`
}

// observation is what one guest reported in one sweep.
type observation struct {
	facts map[string]string
	fs    []agentFS
}

func isAuthError(err error) bool {
	var apiErr *apiError
	return errors.As(err, &apiErr) && (apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusForbidden)
}

func isAgentNotRunning(err error) bool {
	var apiErr *apiError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusInternalServerError && strings.Contains(strings.ToLower(apiErr.Message), "not running")
}

// agentGet reads one guest agent call; the answer holds the payload under data.result.
func (s *guestsScraper) agentGet(ctx context.Context, vmid int, call string, out any) error {
	var env struct {
		Data struct {
			Result json.RawMessage `json:"result"`
		} `json:"data"`
	}
	path := s.nodePath("/qemu/%d/agent/%s", vmid, call)
	if err := s.client.get(ctx, path, s.timeout, false, &env); err != nil {
		return err
	}
	if len(env.Data.Result) == 0 || string(env.Data.Result) == "null" {
		return nil
	}
	return json.Unmarshal(env.Data.Result, out)
}

func (s *guestsScraper) readAgent(ctx context.Context, vmid int) (observation, error) {
	var obs observation
	obs.facts = map[string]string{}

	var osinfo struct {
		Name   string `json:"name"`
		Pretty string `json:"pretty-name"`
		Ver    string `json:"version"`
	}
	if err := s.agentGet(ctx, vmid, "get-osinfo", &osinfo); err != nil {
		return obs, err
	}
	switch {
	case osinfo.Pretty != "":
		obs.facts["os"] = osinfo.Pretty
	case osinfo.Name != "":
		obs.facts["os"] = strings.TrimSpace(osinfo.Name + " " + osinfo.Ver)
	}

	var host struct {
		Name string `json:"host-name"`
	}
	if err := s.agentGet(ctx, vmid, "get-host-name", &host); err != nil {
		return obs, err
	}
	if host.Name != "" {
		obs.facts["hostname"] = host.Name
	}

	var ifaces []struct {
		Addresses []struct {
			IP string `json:"ip-address"`
		} `json:"ip-addresses"`
	}
	if err := s.agentGet(ctx, vmid, "network-get-interfaces", &ifaces); err != nil {
		return obs, err
	}
	var addrs []string
	for _, iface := range ifaces {
		for _, a := range iface.Addresses {
			addrs = append(addrs, a.IP)
		}
	}
	if joined := joinAddresses(addrs); joined != "" {
		obs.facts["addresses"] = joined
	}

	if err := s.agentGet(ctx, vmid, "get-fsinfo", &obs.fs); err != nil {
		return obs, err
	}
	return obs, nil
}

// joinAddresses returns the sorted, comma-joined addresses without prefix lengths, loopback or link-local
// ones: those change with a bridge or a container and say nothing about the guest.
func joinAddresses(addrs []string) string {
	seen := map[string]bool{}
	var out []string
	for _, a := range addrs {
		a, _, _ = strings.Cut(a, "/")
		if a == "" || seen[a] {
			continue
		}
		if ip, err := netip.ParseAddr(a); err == nil && (ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func (s *guestsScraper) readLXC(ctx context.Context, vmid int) (observation, error) {
	obs := observation{facts: map[string]string{}}
	var out struct {
		Data []struct {
			Inet  string `json:"inet"`
			Inet6 string `json:"inet6"`
		} `json:"data"`
	}
	path := s.nodePath("/lxc/%d/interfaces", vmid)
	if err := s.client.get(ctx, path, s.timeout, false, &out); err != nil {
		return obs, err
	}
	var addrs []string
	for _, iface := range out.Data {
		addrs = append(addrs, iface.Inet, iface.Inet6)
	}
	if joined := joinAddresses(addrs); joined != "" {
		obs.facts["addresses"] = joined
	}
	return obs, nil
}

// sweep reads the running guests of the node, one request at a time. It returns the filesystem metrics and
// the change-event records, and the lock series of every locked guest. A 401 or 403 stops the sweep and is returned; any other failure only backs
// that guest off.
func (s *guestsScraper) sweep(ctx context.Context, now time.Time) (pmetric.Metrics, plog.Logs, error) {
	md := pmetric.NewMetrics()
	lg := plog.NewLogs()
	s.pending = map[int]*guestPending{}

	qemu, err := s.list(ctx, "qemu")
	if err != nil {
		return md, lg, fmt.Errorf("list qemu guests: %w", err)
	}
	lxc, lxcErr := s.list(ctx, "lxc")

	b := newMetricBuilder(md, pcommon.NewTimestampFromTime(now))
	sm := lg.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty()
	sm.Scope().SetName(scopeName)

	kinds := []struct {
		name   string
		guests []guestEntry
		read   func(context.Context, int) (observation, error)
	}{{"qemu", qemu, s.readAgent}, {"lxc", lxc, s.readLXC}}
	// The lock comes with the guest list, so it costs no request. It is read for every guest, running or
	// not, in a pass of its own so that a failing guest read cannot drop it.
	for _, kind := range kinds {
		sort.Slice(kind.guests, func(i, j int) bool { return kind.guests[i].VMID < kind.guests[j].VMID })
		for _, g := range kind.guests {
			if lock := g.lockState(); lock != "" {
				b.gauge("pve.guest.lock", "1", 1, "pve.vmid", strconv.Itoa(g.VMID), "pve.node", s.node, "type", kind.name, "state", lock)
			}
		}
	}
	for _, kind := range kinds {
		for _, g := range kind.guests {
			if g.Status != "running" {
				continue
			}
			st := s.stateFor(g.VMID)
			if now.Before(st.nextTry) {
				continue
			}
			obs, err := kind.read(ctx, g.VMID)
			if err != nil {
				if isAuthError(err) {
					return md, lg, err
				}
				s.fail(sm, st, g.VMID, now, err)
				continue
			}
			s.succeed(sm, st, g.VMID, now, obs, b)
		}
	}
	if lxcErr != nil {
		return md, lg, fmt.Errorf("list lxc guests: %w", lxcErr)
	}
	return md, lg, nil
}

func (s *guestsScraper) list(ctx context.Context, kind string) ([]guestEntry, error) {
	var out struct {
		Data []guestEntry `json:"data"`
	}
	if err := s.client.get(ctx, s.nodePath("/%s", kind), s.timeout, false, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (s *guestsScraper) fail(sm plog.ScopeLogs, st *guestState, vmid int, now time.Time, err error) {
	st.failures++
	st.nextTry = now.Add(backoffFor(s.interval, st.failures))
	if isAgentNotRunning(err) {
		if !st.unavailable {
			unavailable := true
			s.pendingFor(vmid).unavailableTo = &unavailable
			s.record(sm, vmid, now, "agent", agentUnavailable)
		}
		return
	}
	if st.failures == 1 {
		// One WARN when a guest enters backoff; the next failures in a row stay at debug.
		s.logger.Warn("guest agent read failed; backing the guest off", zap.Int("vmid", vmid),
			zap.Time("next_try", st.nextTry), zap.Error(err))
		return
	}
	s.logger.Debug("guest read failed; backing off", zap.Int("vmid", vmid), zap.Time("next_try", st.nextTry), zap.Error(err))
}

func (s *guestsScraper) succeed(sm plog.ScopeLogs, st *guestState, vmid int, now time.Time, obs observation, b *metricBuilder) {
	st.failures = 0
	st.nextTry = time.Time{}
	if st.unavailable {
		available := false
		s.pendingFor(vmid).unavailableTo = &available
		s.record(sm, vmid, now, "agent", agentAvailable)
	}
	keys := make([]string, 0, len(obs.facts))
	for k := range obs.facts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if st.facts[k] != obs.facts[k] {
			s.pendingFor(vmid).facts[k] = obs.facts[k]
			s.record(sm, vmid, now, k, obs.facts[k])
		}
	}
	id := strconv.Itoa(vmid)
	for _, f := range obs.fs {
		attrs := []string{"pve.vmid", id, "pve.node", s.node, "mountpoint", f.Mountpoint, "fstype", f.Type, "device", f.Name}
		if f.Used != nil {
			b.gauge("pve.guest.filesystem.usage", "By", float64(*f.Used), attrs...)
		}
		if f.Total != nil {
			b.gauge("pve.guest.filesystem.size", "By", float64(*f.Total), attrs...)
		}
	}
}

func (s *guestsScraper) record(sm plog.ScopeLogs, vmid int, now time.Time, fact, value string) {
	rec := sm.LogRecords().AppendEmpty()
	rec.SetTimestamp(pcommon.NewTimestampFromTime(now))
	rec.Body().SetStr(factBody)
	rec.Attributes().PutStr("pve.vmid", strconv.Itoa(vmid))
	rec.Attributes().PutStr("pve.node", s.node)
	rec.Attributes().PutStr("pve.fact", fact)
	rec.Attributes().PutStr("pve.fact.value", value)
}

var missingPrivilege = regexp.MustCompile(`Permission check failed \([^,]+, ([^)]+)\)`)

// privilegeMessage names what a 403 on a guest agent call lacks. The PVE answer names the privilege when
// it can; the text also lists both names, because the name differs by PVE version.
func privilegeMessage(err *apiError) string {
	msg := "the guests token needs VM.Audit plus VM.Monitor (PVE 8 and earlier) or VM.GuestAgent.Audit (PVE 9) on /vms"
	if m := missingPrivilege.FindStringSubmatch(err.Message); m != nil {
		return "PVE reports the missing privilege " + m[1] + "; " + msg
	}
	return msg
}
