# Metrics and Runtime Environment

What this distribution emits and collects, where each metric comes from, and
which profile produces it. See [README](../README.md) for build/profile
background.

## Custom metrics

### PSI (Pressure Stall Information) — `receiver/psireceiver`

Reads `/proc/pressure/{cpu,memory,io}` on a `collection_interval` (default
`60s`, configurable via `proc_path`/`collection_interval`) and emits gauges:

| Metric | Resources | Notes |
|---|---|---|
| `system.pressure.<resource>.some.avg10` | cpu, memory, io | 10s rolling average |
| `system.pressure.<resource>.some.avg60` | cpu, memory, io | 60s rolling average |
| `system.pressure.<resource>.some.avg300` | cpu, memory, io | 300s rolling average |
| `system.pressure.<resource>.full.avg10` | cpu, memory, io | cpu `full` needs kernel 5.13 or later |
| `system.pressure.<resource>.full.avg60` | cpu, memory, io | |
| `system.pressure.<resource>.full.avg300` | cpu, memory, io | |

Emitted on the **edge** profile (see `test/configs/local-edge.yaml`, `psi`
receiver in the `metrics` pipeline).

#### Per-cgroup pressure (`cgroup` block, Linux edge builds)

The same receiver can read the pressure files of single cgroups (`cpu.pressure`,
`memory.pressure`, `io.pressure`) on a cgroup v2 host. The block is off by default;
with it off the output is as before. A config turns it on with `cgroup.enabled: true`
and an `include` list; there is no default `include`.

| Key | Default | Meaning |
|---|---|---|
| `enabled` | `false` | Turns the block on. `include` must then list at least one glob. |
| `path` | `/sys/fs/cgroup` | The cgroup v2 root, for example a read-only host mount. |
| `max_depth` | `4` | Levels read below `path`. Depth 4 reaches the container scope of a Kubernetes pod. |
| `level` | `pod` | `pod` (pod cgroups), `container` (container cgroups) or `all` (every cgroup that passes the globs). |
| `include`, `exclude` | none | Doublestar globs matched against the path relative to `path`, such as `kubepods.slice/**`. |
| `values` | `avg10`, `total` | Which of `avg10`, `avg60`, `avg300` and `total` are emitted. |
| `max_cgroups` | `500` | Most cgroups read in one scrape. The first ones by path are read; the rest are counted. |

| Metric | Type, unit | Notes |
|---|---|---|
| `cgroup.pressure.<resource>.some.<avg10\|avg60\|avg300>` | gauge, `%` | resource is `cpu`, `memory` or `io` |
| `cgroup.pressure.<resource>.full.<avg10\|avg60\|avg300>` | gauge, `%` | emitted when the file has a `full` line |
| `cgroup.pressure.<resource>.some.total` | monotonic sum, `us` | stall time since boot |
| `cgroup.pressure.<resource>.full.total` | monotonic sum, `us` | stall time since boot |
| `otelcol_psi_cgroups_skipped` | counter, `{cgroup}` | cgroups left out because a scrape found more than `max_cgroups`; an over-cap cgroup is counted again at every scrape |

The host `system.pressure.*` series are unchanged. The `total` sums count from boot, but
their start time is the time the receiver started, so a pod that ran before then has a
first value above zero; use the difference between two points, not the first value.

Attributes. Every data point has `cgroup.path`, the path below the root. A leaf
named `cri-containerd-<id>.scope`, `crio-<id>.scope`, `docker-<id>.scope` or a bare
64-hex directory gives the data point attribute `container.id`. When the path names
a Kubernetes pod, the resource has `k8s.pod.uid` (the systemd driver writes the uid
with underscores; they are restored to dashes) and `k8s.pod.qos` (`guaranteed`,
`burstable` or `besteffort`). A path that matches no known layout, such as a
`system.slice` service, has only `cgroup.path`. The `k8s_attributes` processor can add
the pod name and namespace from `k8s.pod.uid`; `test/configs/edge-daemonset.yaml` is a
DaemonSet config that does so.

Series count grows with the cgroups selected: each cgroup gives up to 12 series with
the default values (three resources, `some` and `full`, two values).

### ZFS pools and ARC — `receiver/zfskstatreceiver`

Reads the OpenZFS kstat files under `kstat_path` (default `/proc/spl/kstat/zfs`)
every `collection_interval` (default `60s`). The file `io` of each pool directory is
parsed by field name. Pool metrics carry the attribute `zfs.pool`. Cumulative
sums start at host boot (from `/proc/uptime`), or at the time the receiver
started when the uptime cannot be read; the kstat counters themselves run from
the module load or the pool import, so the first value is not zero.

| Metric | Type | Unit | Attributes | Source |
|---|---|---|---|---|
| `zfs.pool.io.bytes` | monotonic sum | `By` | `zfs.pool`, `direction` (`read`, `write`) | `nread`, `nwritten` |
| `zfs.pool.io.operations` | monotonic sum | `{operation}` | `zfs.pool`, `direction` | `reads`, `writes` |
| `zfs.pool.io.wait.busy.time` | monotonic sum | `s` | `zfs.pool` | `wtime`, nanoseconds converted |
| `zfs.pool.io.run.busy.time` | monotonic sum | `s` | `zfs.pool` | `rtime`, nanoseconds converted |
| `zfs.pool.io.queue.length` | gauge | `{operation}` | `zfs.pool`, `queue` (`wait`, `run`) | `wcnt`, `rcnt` |
| `zfs.pool.state` | gauge, always 1 | `1` | `zfs.pool`, `state` (for example `ONLINE`, `DEGRADED`) | pool `state` file |
| `zfs.arc.size` | gauge | `By` | none | `arcstats` `size` |
| `zfs.arc.hits` | monotonic sum | `{hit}` | none | `arcstats` `hits` |
| `zfs.arc.misses` | monotonic sum | `{miss}` | none | `arcstats` `misses` |

The two `busy.time` sums count the time the wait queue or the run queue held at
least one operation, like the utilisation column of `iostat`. The rate of either
is a fraction between 0 and 1. They are not latencies: do not divide them by the
operation count. A per-operation latency needs the length-time fields `wlentime`
and `rlentime`, which the receiver does not read yet.

`zfs.pool.state` is an info metric: one series of value 1 for the current state.
When a pool changes from `ONLINE` to `DEGRADED` the `ONLINE` series stops and a
`DEGRADED` series starts, so an alert rule should test for `state != ONLINE` or
for a missing `ONLINE` series, not for a value of 0.

The three `zfs.arc.*` metrics are on while `arcstats` is `true` (the default). A host
without ZFS has no `kstat_path`: the receiver starts, logs one info line, sends
nothing and looks again at each interval. A pool whose `io` file is empty, short or
not numeric is skipped with one warning until it reads cleanly again. Linux edge
builds only; see the [README](../README.md#zfs-pool-and-arc-metrics).

### Normalized/denormalized load — `processor/normalizedloadprocessor`

Post-processes standard `host_metrics` output, adding derived metrics
alongside the originals (it does not remove the source metric):

| Source metric | Derived metric | Transform |
|---|---|---|
| `system.cpu.load_average.1m` | `system.cpu.load_average.normalized.1m` | divide by CPU count |
| `system.cpu.load_average.5m` | `system.cpu.load_average.normalized.5m` | divide by CPU count |
| `system.cpu.load_average.15m` | `system.cpu.load_average.normalized.15m` | divide by CPU count |
| `process.cpu.utilization` | `process.cpu.utilization.percpu` | multiply by CPU count |
| `system.cpu.utilization` | `system.cpu.utilization.percpu` | multiply by CPU count |

CPU count is auto-detected (`runtime.NumCPU()`) unless overridden via the
`cpu_count` config option. Used on the edge profile to turn OTel's
already-normalized CPU metrics back into single-core ("top-style")
percentages, and to make load averages comparable across hosts with
different core counts.

### Summarization — `processor/windowhistprocessor`

Turns the samples scraped inside one send window into one point per series.
Scrape and send intervals default to 10s and 60s; each group may override
either.

| Group | Mode | Metrics |
|---|---|---|
| `cpu` | histogram | `system.cpu.utilization`, `.percpu` |
| `memory` | histogram | `system.memory.usage`, `.utilization` |
| `disk_latency` | histogram | Linux `system.disk.pending_operations`; Windows `windows.physicaldisk.queue_length`, `avg_queue_length`, `operation_latency`, `idle_time` |
| `disk_io_rate` | histogram | Linux `system.disk.io`, `.operations`; Windows `windows.physicaldisk.io`, `.transfers` |
| `network_rate` | histogram | `system.network.io`, `.packets`, `.errors`, `.dropped` |
| `cpu_pressure`, `memory_pages`, `network_pressure` | histogram | Windows perf counters |
| `gpu` | histogram | `hw.gpu.utilization`, `hw.gpu.memory.utilization`, `hw.temperature`, `hw.power` |
| `psi`, `process`, `filesystem`, `plain` | plain | PSI gauges, per-process metrics including `process.cpu.utilization.percpu`, filesystem space; counters, load and normalized load, paging, `system.disk.io_time`, `.weighted_io_time`, `.operation_time`, `.merged`, committed bytes, commit limit, connection counts, GPU counters |

The `normalizedload` processor runs before `windowhist`, so its derived `.percpu` and
`.normalized` metrics are grouped like their source metrics. A metric no group lists passes
through at the scrape interval.

A plain group sends the last value per window with its own timestamps. A plain
group with scrape = send passes through unchanged, as does any metric no group lists.

**Rate metrics.** A cumulative counter in a histogram group becomes
`<name>.rate` with unit `<unit>/s`. Each rate is the counter difference over the
measured time between two readings. A counter drop or start-time change skips
that pair and re-baselines. With `keep_counter` (default on for counter groups)
the last counter point is sent as well.

**Temporality.** Histograms are delta by default. `temporality: cumulative`
starts each series at its first-seen time. The Elasticsearch exporter drops
cumulative histograms with a WARN; ClickHouse stores both.

**Self-metrics.**

| Metric | Type | Meaning |
|---|---|---|
| `otelcol_processor_windowhist_series_active` | gauge | series buffered now |
| `otelcol_processor_windowhist_samples_received` | counter | grouped data points received |
| `otelcol_processor_windowhist_samples_discarded{reason}` | counter | `late`, `invalid`, `counter_reset` |

A series idle for two windows is removed. Shutdown sends the open window with
its real start and end. One WARN per group says when samples arrive further
than half a scrape interval from the declared one.

**Off switch.** Take `windowhist` out of the pipeline and set each receiver's
`collection_interval` to its group's `send_interval`; output then matches the
unsummarized config.

**Cost.** Measured over one hour with the static edge build in a Linux build
container on a 32-CPU VM. Both runs use the same scrapers and a background
disk-write load. The baseline scrapes every 60s without `windowhist`. The
`windowhist` run scrapes every 10s (filesystem 15m) and sends every 60s.

| Measure | Without `windowhist` | With `windowhist` | Change |
|---|---|---|---|
| CPU time | 3.24 CPU-s (0.09% of one core) | 6.27 CPU-s (0.17%) | 1.94x |
| RSS at the end | 69.7 MiB | 82.4 MiB | +12.7 MiB; the `windowhist` process grew 36.8 MiB over the hour |
| Egress | 460,082 B (122 messages) | 1,037,595 B (61 messages) | 2.26x gzip, 3.28x raw proto |

Egress is the file exporter in proto format with each message gzipped alone.
CPU series make up 77% of the `windowhist` run's bytes: `system.cpu.utilization`
30%, `.percpu` 27.5%, `system.cpu.time` 20%. On this VM each is 256 per-CPU
series, so a host with fewer CPUs costs less. The CPU baseline varied between
3.24 and 4.51 CPU-s across runs, so treat the CPU ratio as approximate.

Put every metric in a group. A metric that no group lists passes through at the
scrape interval, which sent 3.76 times the baseline bytes before the
`normalizedload` outputs were added to groups. A test capture confirms that no
metric passes through at the scrape interval now.

With all groups plain at 60s/60s the output matches the baseline build: 47
metric names, same types and counts.

### Identity fill — `processor/identityfillprocessor`

| Metric | Type | Meaning |
|---|---|---|
| `otelcol_identityfill_records_filled{rule}` | counter | log records given a value by a rule: `host_name`, `service_name`, `audit_body` |

## Resource-attribute enrichment (not new metrics)

Two processors tag existing telemetry (metrics, logs, and traces) with
resource attributes rather than emitting metrics of their own:

- **`processor/resourceversionprocessor`** — sets a resource attribute (key
  configurable, `role`-scoped) to the running collector's version, via
  `SetCollectorVersion` at startup. Applies to all three signal types.
- **`processor/assetdbprocessor`** — enriches telemetry with asset-database
  lookups keyed off a configurable `source_attribute`, backed by a local
  `database_path`.

## Standard `host_metrics` metrics

Both profiles' local test configs (`test/configs/local-edge.yaml`,
`test/configs/local-gateway.yaml`) scrape standard `host_metrics` (`cpu`,
`load`, `memory`, and periodic `process` scrapers); these follow upstream
OpenTelemetry Collector Contrib semantics and are not custom to this
distribution.

## Collector self-telemetry

The collector's own internal metrics (queue sizes, dropped spans, receiver/
exporter counters, etc.) are controlled by the standard `service.telemetry.metrics`
block and are **not hardcoded** by this distribution:

- **Gateway** (`test/configs/local-gateway.yaml`): exposed via a Prometheus
  pull reader on `127.0.0.1:8888`.
- **Edge** (`test/configs/local-edge.yaml`): disabled (`level: none`) in the
  local test config to reduce noise; production Ansible-managed configs (out
  of this tree — see [README](../README.md)) may set this differently.

## Runtime environment variables

Read directly by `cmd/otelcol-custom/main.go` at startup, ahead of Sentry
initialization:

| Variable | Effect | Accepted values | Default |
|---|---|---|---|
| `SENTRY_ENVIRONMENT` | Sets the `environment` tag on all events sent to Sentry | any string | `production` |
| `SENTRY_DEBUG` | Enables the Sentry SDK's own debug logging (`sentry.ClientOptions.Debug`) | `"true"` enables; anything else is treated as disabled | disabled |
| `OTELCOL_CUSTOM_DISABLE_TELEMETRY` | Opts out of Sentry entirely; no client is initialized when set | any non-empty value | unset (telemetry on) |
| `OTELCOL_CUSTOM_SENTRY_DSN` | Sends events to this Sentry project instead of the built-in one | a valid Sentry DSN | unset (built-in DSN) |

All four are read once at process start; there is no live-reload. Sentry
itself is only initialized if a non-placeholder DSN is compiled into the
binary and `OTELCOL_CUSTOM_DISABLE_TELEMETRY` is unset.

<!-- cspell:ignore HKLM -->

An empty `OTELCOL_CUSTOM_SENTRY_DSN` keeps the built-in DSN. A value that does
not parse as a DSN is ignored: the collector logs a warning that names the
variable, not its value, and starts with the built-in DSN. On Windows the
variable goes in the service's `Environment` registry value
(`REG_MULTI_SZ`, one `NAME=value` per line) under
`HKLM\SYSTEM\CurrentControlSet\Services\<service>`; restart the service
afterwards.

## Sentry events

Each collector log line at ERROR or above becomes one Sentry event, grouped by
message and source file. A log line is dropped instead when its message, or
the text of its `error` field, contains one of these strings
(case-insensitive), because it is expected and the collector recovers on its
own:

- `use of closed network connection`: an idle connection the peer closed; the
  exporter retries the upload.
- `failed to find MUI file`: an event-log provider without message resources;
  the event still ships.
- the older entries in `filterNoiseErrors` in `cmd/otelcol-custom/main.go`,
  such as `context canceled` and `connection reset by peer`.

A failed upload of the collector's own telemetry is dropped when its error
text ends in `: EOF`, or contains `connection reset by peer` or
`use of closed network connection`. The log message is
`OpenTelemetry internal telemetry error`, or the error starts with
`failed to upload metrics`, `logs` or `traces`. These are reused connections
the gateway closed; the next push succeeds. Upload failures with other causes,
such as a timeout, an HTTP 5xx or a DNS error, still reach Sentry because they
can signal a real outage.

The `process` scraper error for a process that exited between the listing and
the read is dropped when every `; `-joined part has the form
`error reading <what> for process "<name>" (pid <n>): The parameter is incorrect.`
Access-denied and other read failures still reach Sentry.

At start the collector sends one info event, `<service> started on <host>`. All
hosts share one Sentry issue; the host name is in the `server_name` tag.
