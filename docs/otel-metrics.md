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

Reads the OpenZFS kstats every `collection_interval` (default `60s`). With
`source: proc` (the default on Linux) it reads the files under `kstat_path` (default
`/proc/spl/kstat/zfs`); the file `io` of each pool directory is parsed by field name.
OpenZFS 2.1 and later have no pool-level I/O counters and no `io` file; `zfs.pool.io.*`
appears only where the legacy `io` file exists (OpenZFS 2.0 and older). A pool without
it still gives `zfs.pool.state` and, with `datasets`, the dataset metrics.
With `source: sysctl` (the default on FreeBSD) it reads the `kstat.zfs` sysctl tree.
`source: auto` picks by operating system. Pool metrics carry the attribute `zfs.pool`.
Cumulative sums start at host boot (from `/proc/uptime`, or `kern.boottime` on
FreeBSD), or at the time the receiver started when the boot time cannot be read; the
kstat counters themselves run from the module load or the pool import, so the first
value is not zero.

| Metric | Type | Unit | Attributes | Source |
|---|---|---|---|---|
| `zfs.pool.io.bytes` | monotonic sum | `By` | `zfs.pool`, `direction` (`read`, `write`) | `nread`, `nwritten` |
| `zfs.pool.io.operations` | monotonic sum | `{operation}` | `zfs.pool`, `direction` | `reads`, `writes` |
| `zfs.pool.io.wait.busy.time` | monotonic sum | `s` | `zfs.pool` | `wtime`, nanoseconds converted |
| `zfs.pool.io.run.busy.time` | monotonic sum | `s` | `zfs.pool` | `rtime`, nanoseconds converted |
| `zfs.pool.io.queue.length` | gauge | `{operation}` | `zfs.pool`, `queue` (`wait`, `run`) | `wcnt`, `rcnt` |
| `zfs.pool.state` | gauge, always 1 | `1` | `zfs.pool`, `state` (for example `ONLINE`, `DEGRADED`) | pool `state` file |
| `zfs.dataset.io.bytes` | monotonic sum | `By` | `zfs.pool`, `zfs.dataset`, `direction` | `objset-*` `nread`, `nwritten`; only with `datasets: true` |
| `zfs.dataset.io.operations` | monotonic sum | `{operation}` | `zfs.pool`, `zfs.dataset`, `direction` | `objset-*` `reads`, `writes`; only with `datasets: true` |
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

The three `zfs.arc.*` metrics are on while `arcstats` is `true` (the default). The two
`zfs.dataset.io.*` metrics are off until `datasets` is `true`; they read the
`objset-*` kstat of each dataset on both sources, matching fields by name, and add
one series per dataset and direction. FreeBSD has no per-pool `io` kstat, so the
`sysctl` source emits no `zfs.pool.io.*` metrics and logs no warning for it; it
still emits `zfs.pool.state`, the ARC metrics and, with `datasets`, the dataset
metrics. A host without ZFS has no `kstat_path` (or no `kstat.zfs` sysctl): the
receiver starts, logs one info line, sends nothing and looks again at each interval.
A sysctl error other than not found logs one warning until a read succeeds again.
`max_datasets` (default 100, 0 for no cap) keeps the first datasets in sorted order and
logs one warning until the count fits again. A dataset name may hold spaces on both
sources. The dataset and pool counters restart at the scrape time when a value goes
down, which is how a remounted dataset or a re-imported pool appears; other sums
start at host boot. A pool whose `io` file is empty, short or
not numeric is skipped with one warning until it reads cleanly again. See the [README](../README.md#zfs-pool-and-arc-metrics).

### Proxmox VE — `receiver/pvereceiver`

Reads a Proxmox VE cluster through its HTTP API. It has two scrapers, each switched by its own
`enabled` key and each with its own API token. The `resources` scraper reads `/cluster/resources`,
`/cluster/status` and `/version` every `collection_interval` (default `60s`, one request at a time,
`timeout` 10 s) and covers every node, guest and storage of the cluster (entries of type `sdn` and `pool` are skipped), so enable it on **one node
per cluster**. The `guests` scraper asks the QEMU guest agent of each running guest on **its own
node** (`node`, default the local host name) every 15 minutes with a start offset, one request at a
time with a 5 s timeout, and enable it on every node. All names start with `pve.`.

Attributes on every `resources` point: `pve.resource.type` (`node`, `qemu`, `lxc`, `storage`),
`pve.resource.id` (for example `qemu/100`) and `pve.node`.

| Metric | Type | Unit | Extra attributes |
|---|---|---|---|
| `pve.resource.up` | gauge | `1` | `status` (the raw status: `running`, `stopped`, `online`, `offline`, `available`, `unknown`); 1 for `running`, `online`, `available` |
| `pve.resource.cpu.utilization` | gauge | `1` | none |
| `pve.resource.cpu.limit` | gauge | `{cpu}` | none |
| `pve.resource.memory.usage`, `pve.resource.memory.limit` | gauge | `By` | none |
| `pve.resource.disk.usage`, `pve.resource.disk.limit` | gauge | `By` | none |
| `pve.resource.uptime` | gauge | `s` | none |
| `pve.resource.network.io` | monotonic sum | `By` | `network.io.direction` (`receive`, `transmit`) |
| `pve.resource.disk.io` | monotonic sum | `By` | `disk.io.direction` (`read`, `write`) |
| `pve.storage.shared` | gauge | `1` | none |
| `pve.guest.info` | gauge, always 1 | `1` | `name`, `type`, `tags`, `template` |
| `pve.storage.info` | gauge, always 1 | `1` | `storage`, `content`, `plugintype` |
| `pve.node.info` | gauge, always 1 | `1` | `name`, `nodeid`, `level` |
| `pve.version.info` | gauge, always 1 | `1` | `version`, `release`, `repoid` |
| `pve.resource.ha.state` | gauge, always 1 | `1` | `state`; only for guests the HA manager owns |

The sums start at the guest start (now minus the uptime) when the answer has an uptime. A node that is
down makes PVE report its guests with the status `unknown`: they give `pve.resource.up` of 0 with
`status=unknown` and no other point, and the data of the other nodes is unchanged.

Four calls are off by default and made only when their key under `resources` is `true`:

| Key | Call | Metric | Attributes |
|---|---|---|---|
| `subscription` | `/nodes/{node}/subscription`, online nodes | `pve.subscription.status` (gauge 1) | `pve.node`, `status`, `level` |
| `replication` | `/cluster/replication` | `pve.replication.job` (gauge 1) | `id`, `guest`, `source`, `target` |
| `backup_info` | `/cluster/backup-info/not-backed-up` | `pve.guest.not_backed_up` (gauge 1) | `pve.vmid`, `name`, `type` |
| `guest_config` | `/nodes/{node}/{type}/{id}/config`, one call per guest | `pve.guest.onboot` (gauge) | `pve.vmid`, `pve.node`, `type` |

The `guests` scraper skips guests that are not running. After a failed agent call it leaves that guest
alone for the collection interval, then twice as long after each further failure, never longer than 24
hours. A guest whose agent is not running (HTTP 500 "QEMU guest agent is not running") is a normal
state: it is recorded once as `agent=unavailable` and checked again at its backoff. Containers have no
agent; their addresses come from `/nodes/{node}/lxc/{id}/interfaces`.

| Signal | Name | Type, unit | Attributes |
|---|---|---|---|
| metric | `pve.guest.filesystem.usage` | gauge, `By` | `pve.vmid`, `pve.node`, `mountpoint`, `fstype`, `device` |
| metric | `pve.guest.filesystem.size` | gauge, `By` | same |
| log | `pve guest fact` | change event | `pve.vmid`, `pve.node`, `pve.fact` (`os`, `hostname`, `addresses`, `agent`), `pve.fact.value` |

A fact record is written when a value first appears or changes; a sweep that finds nothing new writes
none. `addresses` is a sorted, comma-joined list without loopback and link-local addresses, which change with a bridge or a container. A fact is counted as delivered only when the logs pipeline took it; a failed send, or a receiver with no logs pipeline, makes the next sweep write it again. A guest that fails (not "agent not running") logs one WARN when it enters backoff.

Series count for the `resources` scraper: `13G + 13N + 5S` for G guests, N nodes and S storages, plus
the info gauges. The `guests` scraper adds two series per mount of each running guest with an agent.
Each guest restart changes no series; a new or removed guest id adds or ends a set. Linux edge builds
only; see the [README](../README.md#proxmox-ve-metrics).

### Hardware sensors — `receiver/hwmonreceiver`

Reads `hwmon_path` (default `/sys/class/hwmon`) and the thermal zones in the sibling
directory `thermal` every `collection_interval` (default `60s`), and on FreeBSD the
CPU and ACPI temperature sysctls. All metrics are gauges with the attributes `hw.id`,
`hw.name` and `hw.parent`; the names follow the `hw.*` semantic conventions.

| Metric | Type | Unit | Extra attribute | Source |
|---|---|---|---|---|
| `hw.temperature` | gauge, double | `Cel` | none | `temp<N>_input` (millidegrees divided by 1000), thermal zone `temp` |
| `hw.temperature.limit` | gauge, double | `Cel` | `hw.limit_type` | `temp<N>_max` (`max`), `_crit` (`critical`), `_min` (`low.degraded`), `_lcrit` (`low.critical`) |
| `hw.fan.speed` | gauge, integer | `rpm` | none | `fan<N>_input` |
| `hw.voltage` | gauge, double | `V` | none | `in<N>_input` (millivolts divided by 1000) |

`hw.parent` is the chip name (`coretemp`, `nvme`, `nct6775`) or the thermal zone type
(`acpitz`). `hw.name` is the sensor label, or the file stem (`temp2`) when there is
no label file. `hw.id` is the chip name and the file stem, for example
`coretemp/temp3`, and stays the same across scrapes. Two chips with one name, such
as two NVMe drives, get the hardware address of their device after an `@`, for
example `nvme@0000:01:00.0/temp1` (the PCI function; for a platform device its
directory name, such as `coretemp@coretemp.1/temp1`). The address stays with the
drive when the kernel numbers the `nvme` or `hwmon` devices differently at the next
boot. A chip with no device link falls back to its `hwmon<N>` directory, which is not
stable across boots. Adding a second chip of a name changes the `hw.id` of the first
one. A thermal zone has the id `thermal/thermal_zone<N>`; a zone whose type is also
the name of a hwmon chip that gave a reading (the kernel registers one chip per zone
with `CONFIG_THERMAL_HWMON`; hyphens in the type become underscores) is not read
again, so the sensor is reported once. The `include` and `exclude` globs match
`<hw.parent>:<hw.name>`, for example `nvme:Composite`.

`hw.temperature` is a double, not the integer of the semantic conventions, so the
millidegree precision of sysfs is kept. The `limits` key adds one
`hw.temperature.limit` series per limit file that exists. A limit of -273.15 °C or
below (NVMe drives report that for a limit they do not have) is a placeholder and gives
no series. The `cpu_cores` key (off by
default) adds the `Core <N>` sensors of the `coretemp` chip, one per physical core.

Series count, from the kernel hwmon ABI and not measured on a fleet host: a
`coretemp` chip gives 1 package series, plus 1 per core with `cpu_cores`; an NVMe
drive gives 1 to 3 temperatures; a SATA disk with `drivetemp` gives 1; an
`nct6775`-type board chip gives up to about 10 fans and voltages. With `limits` each
temperature adds up to 4 series. A dual-socket 8-core host is about 10 to 15 series
with the defaults, about 30 to 40 with `cpu_cores`, and up to about 100 with both and
all limits. A VM has few or none.

On FreeBSD `hw.id` is `cpu<N>` (with `cpu_cores: true`) or `acpi_tz<N>`, `hw.parent` is
`cpu` or `acpi`, and the sysctl values are tenths of a kelvin. The ACPI zones are
always read, whatever `thermal_zones` says. `_CRT` gives `critical`, `_HOT` gives
`high.degraded` and `_PSV` gives `throttled`; a trip point the zone does not have
gives no series. FreeBSD has no fan or voltage metric. A host with no sensor: the
receiver starts, logs one info line, sends nothing and looks again at each interval.
A hwmon directory that exists but cannot be listed (a permission error) is a warning,
once per episode, not "no sensors".
Linux and static Linux edge builds; see the [README](../README.md#hardware-temperature-fan-and-voltage-sensors).

### iSCSI sessions — `receiver/iscsireceiver`

Reads `sysfs_path` (default `/sys/class`) every `collection_interval` (default `60s`):
the directories `iscsi_session/session<N>` and `iscsi_connection/connection<N>:<M>`.
A connection belongs to the session with the same number. The receiver opens only
`targetname`, `tpgt` and `state` of a session and `persistent_address`,
`persistent_port` and `state` of a connection.

| Metric | Type | Unit | Attributes |
|---|---|---|---|
| `iscsi.session.state` | gauge, always 1 | `1` | `state` (`LOGGED_IN`, `FAILED`, `FREE`), `iscsi.target`, `iscsi.portal`, `iscsi.tpgt` |
| `iscsi.session.count` | gauge | `{session}` | none |
| `iscsi.connection.state` | gauge, always 1 | `1` | `state`, `iscsi.target`, `iscsi.portal` |

`iscsi.portal` is `address:port`, with an IPv6 address in brackets. A session with no
connection has an empty portal. Two sessions with the same target, portal and portal
group tag (for example two sessions to one portal through different network
interfaces) have the same attributes, so a batch holds two points for one series; the
receiver does not read `initiatorname` or `ifacename` to tell them apart. The `iscsi.*` prefix, not `hw.iscsi.*`, keeps the
names out of the namespace the semantic conventions own. The state metrics are info
gauges like `zfs.pool.state`: a changed state ends one series and starts another,
so an alert rule should test for `state != LOGGED_IN` or a missing `LOGGED_IN` series.
A host with no session: the receiver starts, logs one info line, sends nothing and
looks again at each interval. Linux edge builds only.

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

### Journald — `processor/journaldprocessor`

| Metric | Type | Description |
|---|---|---|
| `otelcol_journald_resources_per_batch` | histogram, `{resource}` | resources in each batch the processor passes on; buckets 1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000 |

The count grows when `map_extended` and `split_resources` are on, because records that
differ in `_EXE`, `_CMDLINE`, `_UID`, `_GID` or `_SYSTEMD_CGROUP` get their own resource.
The processor sets no cap; a rising count is the early sign of a memory problem.

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
