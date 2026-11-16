// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package pvereceiver

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

// resource is one entry of /cluster/resources. Fields a type does not have stay nil or empty.
type resource struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	Node      string   `json:"node"`
	VMID      int      `json:"vmid"`
	Name      string   `json:"name"`
	Status    string   `json:"status"`
	Storage   string   `json:"storage"`
	Content   string   `json:"content"`
	Plugin    string   `json:"plugintype"`
	Tags      string   `json:"tags"`
	HAState   string   `json:"hastate"`
	Template  int      `json:"template"`
	Shared    *float64 `json:"shared"`
	CPU       *float64 `json:"cpu"`
	MaxCPU    *float64 `json:"maxcpu"`
	Mem       *float64 `json:"mem"`
	MaxMem    *float64 `json:"maxmem"`
	Disk      *float64 `json:"disk"`
	MaxDisk   *float64 `json:"maxdisk"`
	NetIn     *float64 `json:"netin"`
	NetOut    *float64 `json:"netout"`
	DiskRead  *float64 `json:"diskread"`
	DiskWrite *float64 `json:"diskwrite"`
	Uptime    *float64 `json:"uptime"`
}

type clusterStatusEntry struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	NodeID int    `json:"nodeid"`
	Level  string `json:"level"`
}

type versionData struct {
	Version string `json:"version"`
	Release string `json:"release"`
	RepoID  string `json:"repoid"`
}

// counterStart is the start time of the counters of one resource and the uptime it was taken from.
type counterStart struct {
	uptime float64
	start  time.Time
}

type resourcesScraper struct {
	logger *zap.Logger
	cfg    *Config
	client *apiClient
	start  time.Time
	starts map[string]counterStart
}

func newResourcesScraper(logger *zap.Logger, cfg *Config, client *apiClient, start time.Time) *resourcesScraper {
	return &resourcesScraper{logger: logger, cfg: cfg, client: client, start: start, starts: map[string]counterStart{}}
}

// counterStartFor keeps the start of a resource's counters while its uptime grows. The uptime answer is
// rounded, so a start taken from it each time would creep; only a smaller uptime (a restart) moves it.
func (s *resourcesScraper) counterStartFor(id string, uptime *float64, now time.Time) time.Time {
	if uptime == nil || *uptime <= 0 {
		return s.start
	}
	prev, ok := s.starts[id]
	if !ok || *uptime < prev.uptime {
		prev = counterStart{start: now.Add(-time.Duration(*uptime) * time.Second)}
	}
	prev.uptime = *uptime
	s.starts[id] = prev
	return prev.start
}

func isGuest(r resource) bool { return r.Type == "qemu" || r.Type == "lxc" }

// reported tells the entry types that become metrics: node, guest and storage. PVE also lists sdn and pool
// entries, whose status is not a power state.
func reported(r resource) bool { return r.Type == "node" || isGuest(r) || r.Type == "storage" }

// scrape makes one /cluster/resources, one /cluster/status and one /version request, one after the other,
// then the optional calls the config asks for. The resources answer is required; a failing later call is
// returned as an error beside the metrics that were built.
func (s *resourcesScraper) scrape(ctx context.Context, now time.Time) (pmetric.Metrics, error) {
	md := pmetric.NewMetrics()
	timeout := s.cfg.Resources.Timeout

	var resources struct {
		Data []resource `json:"data"`
	}
	if err := s.client.get(ctx, "/cluster/resources", timeout, true, &resources); err != nil {
		return md, fmt.Errorf("cluster resources: %w", err)
	}
	b := newMetricBuilder(md, pcommon.NewTimestampFromTime(now))
	seen := map[string]bool{}
	for _, r := range resources.Data {
		if !reported(r) {
			continue
		}
		seen[r.ID] = true
		s.resourceMetrics(b, r, now)
	}
	for id := range s.starts {
		if !seen[id] {
			delete(s.starts, id)
		}
	}

	var errs []error
	var status struct {
		Data []clusterStatusEntry `json:"data"`
	}
	if err := s.client.get(ctx, "/cluster/status", timeout, true, &status); err != nil {
		errs = append(errs, fmt.Errorf("cluster status: %w", err))
	}
	for _, e := range status.Data {
		if e.Type == "node" {
			b.gauge("pve.node.info", "1", 1, "name", e.Name, "nodeid", strconv.Itoa(e.NodeID), "level", e.Level)
		}
	}

	var version struct {
		Data versionData `json:"data"`
	}
	if err := s.client.get(ctx, "/version", timeout, true, &version); err != nil {
		errs = append(errs, fmt.Errorf("version: %w", err))
	} else {
		b.gauge("pve.version.info", "1", 1, "version", version.Data.Version, "release", version.Data.Release, "repoid", version.Data.RepoID)
	}

	errs = append(errs, s.optional(ctx, b, resources.Data)...)
	return md, errors.Join(errs...)
}

func statusUp(status string) float64 {
	switch status {
	case "running", "online", "available":
		return 1
	}
	return 0
}

func (s *resourcesScraper) resourceMetrics(b *metricBuilder, r resource, now time.Time) {
	status := r.Status
	if status == "" {
		status = "unknown"
	}
	common := []string{"pve.resource.type", r.Type, "pve.resource.id", r.ID, "pve.node", r.Node}
	with := func(extra ...string) []string { return append(append([]string(nil), common...), extra...) }

	b.gauge("pve.resource.up", "1", statusUp(r.Status), with("status", status)...)
	opt := func(name, unit string, v *float64) {
		if v != nil {
			b.gauge(name, unit, *v, common...)
		}
	}
	opt("pve.resource.cpu.utilization", "1", r.CPU)
	opt("pve.resource.cpu.limit", "{cpu}", r.MaxCPU)
	opt("pve.resource.memory.usage", "By", r.Mem)
	opt("pve.resource.memory.limit", "By", r.MaxMem)
	opt("pve.resource.disk.usage", "By", r.Disk)
	opt("pve.resource.disk.limit", "By", r.MaxDisk)
	if r.Type != "storage" {
		opt("pve.resource.uptime", "s", r.Uptime)
	}

	start := pcommon.NewTimestampFromTime(s.counterStartFor(r.ID, r.Uptime, now))
	if r.NetIn != nil {
		b.counter("pve.resource.network.io", "By", *r.NetIn, start, with("network.io.direction", "receive")...)
	}
	if r.NetOut != nil {
		b.counter("pve.resource.network.io", "By", *r.NetOut, start, with("network.io.direction", "transmit")...)
	}
	if r.DiskRead != nil {
		b.counter("pve.resource.disk.io", "By", *r.DiskRead, start, with("disk.io.direction", "read")...)
	}
	if r.DiskWrite != nil {
		b.counter("pve.resource.disk.io", "By", *r.DiskWrite, start, with("disk.io.direction", "write")...)
	}

	switch {
	case isGuest(r):
		b.gauge("pve.guest.info", "1", 1, with("name", r.Name, "type", r.Type, "tags", r.Tags, "template", strconv.FormatBool(r.Template == 1))...)
	case r.Type == "storage":
		b.gauge("pve.storage.info", "1", 1, with("storage", r.Storage, "content", r.Content, "plugintype", r.Plugin)...)
		if r.Shared != nil {
			b.gauge("pve.storage.shared", "1", *r.Shared, common...)
		}
	}
	if r.HAState != "" {
		b.gauge("pve.resource.ha.state", "1", 1, with("state", r.HAState)...)
	}
}

// optional makes the calls whose key is set and returns their errors.
func (s *resourcesScraper) optional(ctx context.Context, b *metricBuilder, resources []resource) []error {
	var errs []error
	timeout := s.cfg.Resources.Timeout
	if s.cfg.Resources.Subscription {
		for _, r := range resources {
			if r.Type != "node" || r.Status != "online" {
				continue
			}
			var out struct {
				Data struct {
					Status string `json:"status"`
					Level  string `json:"level"`
				} `json:"data"`
			}
			if err := s.client.get(ctx, "/nodes/"+r.Node+"/subscription", timeout, true, &out); err != nil {
				errs = append(errs, fmt.Errorf("subscription of %s: %w", r.Node, err))
				continue
			}
			b.gauge("pve.subscription.status", "1", 1, "pve.node", r.Node, "status", out.Data.Status, "level", out.Data.Level)
		}
	}
	if s.cfg.Resources.Replication {
		var out struct {
			Data []struct {
				ID     string `json:"id"`
				Guest  int    `json:"guest"`
				Source string `json:"source"`
				Target string `json:"target"`
			} `json:"data"`
		}
		if err := s.client.get(ctx, "/cluster/replication", timeout, true, &out); err != nil {
			errs = append(errs, fmt.Errorf("replication: %w", err))
		}
		for _, j := range out.Data {
			b.gauge("pve.replication.job", "1", 1, "id", j.ID, "guest", strconv.Itoa(j.Guest), "source", j.Source, "target", j.Target)
		}
	}
	if s.cfg.Resources.BackupInfo {
		var out struct {
			Data []struct {
				VMID int    `json:"vmid"`
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"data"`
		}
		if err := s.client.get(ctx, "/cluster/backup-info/not-backed-up", timeout, true, &out); err != nil {
			errs = append(errs, fmt.Errorf("backup info: %w", err))
		}
		for _, g := range out.Data {
			b.gauge("pve.guest.not_backed_up", "1", 1, "pve.vmid", strconv.Itoa(g.VMID), "name", g.Name, "type", g.Type)
		}
	}
	if s.cfg.Resources.GuestConfig {
		for _, r := range resources {
			if !isGuest(r) || r.Status == "unknown" || r.Status == "" {
				continue
			}
			var out struct {
				Data struct {
					OnBoot int `json:"onboot"`
				} `json:"data"`
			}
			path := fmt.Sprintf("/nodes/%s/%s/%d/config", r.Node, r.Type, r.VMID)
			if err := s.client.get(ctx, path, timeout, true, &out); err != nil {
				errs = append(errs, fmt.Errorf("config of %s: %w", r.ID, err))
				continue
			}
			b.gauge("pve.guest.onboot", "1", float64(out.Data.OnBoot), "pve.vmid", strconv.Itoa(r.VMID), "pve.node", r.Node, "type", r.Type)
		}
	}
	return errs
}
