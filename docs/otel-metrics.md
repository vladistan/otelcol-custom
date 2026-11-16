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
| `zfs.dataset.unlinks` | monotonic sum | `{unlink}` | `zfs.pool`, `zfs.dataset` | `objset-*` `nunlinks`; only with `datasets: true` |
| `zfs.pool.txg.dirty` | gauge | `By` | `zfs.pool` | `txgs`, `ndirty` of the newest closed row; on while `txg: true` (default) |
| `zfs.pool.txg.written` | gauge | `By` | `zfs.pool` | `txgs`, `nwritten` of the newest closed row |
| `zfs.pool.txg.sync.time` | gauge | `s` | `zfs.pool` | `txgs`, `stime` of the newest closed row, nanoseconds converted |
| `zfs.dataset.space.used` | gauge | `By` | `zfs.pool`, `zfs.dataset` (the pool name) | FreeBSD only: `statfs` of the mount of the root dataset |
| `zfs.dataset.space.available` | gauge | `By` | `zfs.pool`, `zfs.dataset` (the pool name) | FreeBSD only: `statfs` of the mount of the root dataset |
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
`dataset_include` and `dataset_exclude` list globs on `<pool>/<dataset>` (`path.Match`
rules; `*` does not match `/`); an empty include list reads every dataset and exclude
wins. They run before the cap. `max_datasets` (default 100, 0 for no cap) keeps the first
datasets in sorted order and logs one warning until the count fits again. A dataset name may hold spaces on both
sources. The dataset and pool counters restart at the scrape time when a value goes
down, which is how a remounted dataset or a re-imported pool appears; other sums
start at host boot. A pool whose `io` file is empty, short or
not numeric is skipped with one warning until it reads cleanly again.

The three `zfs.pool.txg.*` gauges read the newest closed row (state `C`, highest `txg`)
of the `txgs` kstat, found by column name. The kernel fills `txgs` only while the module
parameter `zfs_txg_history` is above 0; a pool with no closed row or no `txgs` file gets
no txg series and one info line, and `txg: false` turns the metrics and the read off. The
FreeBSD sysctl source reads `kstat.zfs.<pool>.misc.txgs` when the tree has it; whether
FreeBSD 13 exposes it was not checked on a host.

`zfs.dataset.space.used` and `.available` come from `getfsstat` on FreeBSD: the entry of
type `zfs` whose source name equals the pool name is the mount of the root dataset, and
the values are its `statfs` used and available blocks times the block size. They differ
from the SIZE, ALLOC and FREE of `zpool list`, which count the whole pool, and the receiver
reports no fragmentation and no per-vdev errors: those need the libzfs interface, which a
later release may add. A pool whose root dataset is not mounted gets no space series and
one debug line. On Linux, and with the `proc` source, no space series exists. See the [README](../README.md#zfs-pool-and-arc-metrics).

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
`targetname`, `tpgt` and `state` of a session, `persistent_address`,
`persistent_port` and `state` of a connection and, with `luns` on, `type` and
`state` of a SCSI device and `size` of its block device.

| Metric | Type | Unit | Attributes |
|---|---|---|---|
| `iscsi.session.state` | gauge, always 1 | `1` | `state` (`LOGGED_IN`, `FAILED`, `FREE`), `iscsi.target`, `iscsi.portal`, `iscsi.tpgt` |
| `iscsi.session.count` | gauge | `{session}` | none |
| `iscsi.connection.state` | gauge, always 1 | `1` | `state`, `iscsi.target`, `iscsi.portal` |
| `hw.status` | gauge | `1` | `hw.id`, `hw.name`, `hw.type` (`logical_disk`), `hw.parent`, `hw.state`; one point for each state, 1 for the current one |
| `hw.logical_disk.limit` | gauge | `By` | `hw.id`, `hw.name`, `hw.type`, `hw.parent` |
| `iscsi.lun.state` | gauge, always 1 | `1` | `state` (the SCSI device state, or `unknown`), `iscsi.target`, `iscsi.portal`, `iscsi.lun`, `iscsi.device`, `iscsi.holder` (the first device-mapper device on the path, only when there is one), `iscsi.holders` (how many, only when more than one) |

The last three are the per-LUN series, on by default (`luns: true`). A LUN is a SCSI
device behind a session: the receiver follows the links in `scsi_device/<H:C:T:L>` (a
device with no `session<N>` in its path is a local disk and is left out), reads `type`
(only disks, type 0 and 14, are reported), `state` and the `size` of its block device
(512 byte sectors). It takes the semantic-convention names for a logical disk,
`hw.status` with `hw.type` `logical_disk` and `hw.logical_disk.limit`, as the `megaraid`
receiver does; `hw.id` is `<target>/<portal>/<lun>`, `hw.name` the block device and
`hw.parent` the target. `iscsi.lun.state` keeps the raw state, the block device and the
holder. `hw.state` comes from the SCSI state: `running` is `ok`; `blocked`, `quiesce`,
`created` and `created-blocked` (a session that is recovering or a device not yet
usable) are `degraded`; `offline`, `transport-offline`, `cancel` and `deleted` are
`failed`.

A LUN whose `state` cannot be read, whose block device cannot be found or whose state is
not in the table has state `unknown` in `iscsi.lun.state` and no `hw.status` point, and
a warning is logged once for each run of failures; an alert should test for
`state="unknown"`. A size that cannot be read drops only `hw.logical_disk.limit`. A LUN
whose session could not be read is skipped. With multipath the receiver reports each
path under its own device (`sdb`, `sdc`) and never the `dm-N` device on top of them;
`iscsi.holder` names the first one (in name order) and `iscsi.holders` counts them when
there are several, so the paths of one map can be grouped. Each LUN is 7 points: 5 of
`hw.status` (one for each `hw.state`), 1 of `hw.logical_disk.limit` and 1 of
`iscsi.lun.state`. `max_luns` (default 256, at most 4096) caps the LUNs of one scrape, so
256 LUNs are 1,792 points and 4096 are 28,672: the first ones in session and LUN order are
kept and one info line says so. The cap is applied after the LUNs are read, so a host with
many more devices still pays to read them. `max_luns` is checked only when `luns` is on.
Two sessions to one portal give the same `hw.id`, as they do for `iscsi.session.state`.

A device that vanishes while it is read (the read of its `type` file fails with "no
such file", "no such device" or "stale file handle") gives no series, one info line for each such device until it is back, and is not
an `unknown` LUN; a `type` that fails for another reason keeps the LUN as `unknown`. An
attribute read takes at most 4 KiB. The read of all LUNs gives up after 10 seconds with one
warning for each run of failures, sends the session series anyway and does not start a
second read until the first returns, so a wedged sysfs costs one goroutine.

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

### Dell iDRAC — `receiver/idracreceiver` and `receiver/hwschema`

Reads a Dell iDRAC through Redfish every `collection_interval` (default `60s`) per target. The legacy `Thermal` and `Power` resources are read first; `ThermalSubsystem` and `PowerSubsystem` only when the iDRAC no longer serves them, and they have no temperature limits, voltages or host power. A scrape makes about 10 requests plus one per drive, so set `scrape_timeout` above the time of that many requests on a slow iDRAC; `max_concurrent` (default 8) limits the targets read at once. The metric names, units and
attribute keys are in the `hwschema` module, which the `ipmi` receiver of the scrape image shares, so one query covers
both. All metrics are gauges.

| Metric | Unit | Redfish source |
|---|---|---|
| `hw.status` | `1` | `Status.Health` of the system (`host`), chassis (`enclosure`), fans (`fan`), power supplies (`power_supply`), drives (`physical_disk`), DIMMs (`memory`), processors (`cpu`) and the BMC (`bmc`); one point per `hw.state` (`ok`, `degraded`, `failed`, `needs_cleaning`, `predicted_failure`), 1 for the current one. `OK` is `ok`, `Warning` `degraded`, `Critical` `failed`; an unknown or null health gives no point |
| `hw.temperature` | `Cel` | `ReadingCelsius` |
| `hw.temperature.limit` | `Cel` | `UpperThresholdCritical` (`critical`) and `UpperThresholdNonCritical` (`high.degraded`) |
| `hw.fan.speed` | `rpm` | A fan `Reading` whose `ReadingUnits` is RPM, or `SpeedPercent.SpeedRPM` |
| `hw.fan.speed_ratio` | `1` | A fan reading in percent, or `SpeedPercent`, divided by 100 |
| `hw.voltage` | `V` | `ReadingVolts` |
| `hw.host.power` | `W` | `PowerConsumedWatts` of the legacy `Power` resource; not available from `PowerSubsystem` |
| `hw.power_supply.usage`, `.limit`, `.utilization` | `W`, `W`, `1` | `LastPowerOutputWatts` (or `OutputPowerWatts` of the supply `Metrics` link), `PowerCapacityWatts`, and their ratio |
| `hw.physical_disk.size` | `By` | `CapacityBytes` |
| `hw.physical_disk.endurance_utilization` | `1` | 1 minus `PredictedMediaLifeLeftPercent` divided by 100, with `hw.physical_disk.state` `used` |
| `hw.memory.size` | `By` | `CapacityMiB` times 1,048,576 |
| `hw.bmc.up` | `1` | 1 when the scrape worked; 0 with a `reason` (`timeout`, `auth`, `refused`, `backoff`, `tls`) when not |
| `hw.bmc.scrape.duration` | `s` | Time of the scrape |

Every resource has `host.name` (the target `name`), `hw.vendor` (`dell`), `server.address` (the iDRAC host),
`hw.bmc.protocol` (`redfish`) and, where the iDRAC reports them, `hw.model`, `hw.serial_number` (the service tag) and
`hw.firmware_version` (of the iDRAC). A DIMM or processor whose state is `Absent` gives nothing. The counter
`otelcol_hw_bmc_auth_failures`, by `bmc`, is in the collector's own telemetry.

| Case | Result |
|---|---|
| Wrong password | `hw.bmc.up` 0, reason `auth`; not contacted for 15 minutes, then 30, doubling to 6 hours; after three in a row stopped until restart |
| 401 on a request | One new login and one retry; a second 401 is an authentication failure |
| Certificate does not verify | `hw.bmc.up` 0, reason `tls`; no retry without verification |
| iDRAC slow or hung | A request ends at `request_timeout`, the scrape at `scrape_timeout`; `hw.bmc.up` 0, reason `timeout`; the other targets are not delayed |
| A path answers 404 (older firmware, or a reset in progress) | Marked absent, one info line, asked again after 6 hours; the rest is read |
| `$expand` answers 400 or 501 | The collection is read again without the query, and the query is not sent again to that iDRAC |
| A drive or a collection member answers an error | That drive or member is skipped with one warning until a scrape without one; the rest is read and `hw.bmc.up` is 1 |
| Another resource answers 500 or an error | `hw.bmc.up` 0, reason `refused`, and only that and the duration are sent |

Alert rules: `hw.bmc.up` equal to 0 for 15 minutes, and no `hw.bmc.up` series for three collection intervals. A scrape
reads about 12 to 20 resources one after the other, one request at a time. Linux and static Linux edge builds only; see
the [README](../README.md#dell-idrac-health-and-sensors).

### MegaRAID controllers — `receiver/megaraidreceiver`

Reads every controller of the `megaraid_sas` driver (Dell PERC and Supermicro LSI cards) through
`/dev/megaraid_sas_ioctl_node` every `collection_interval` (default `60s`). Controllers come from
`/sys/class/scsi_host/host*/proc_name`; each one is read in turn. Only read-only MFI commands are
sent, and one function checks every frame against a fixed list of 19 opcodes. No `megacli`,
`storcli` or `smartctl` runs, and nothing on the controller is changed.

| Metric | Type | Unit | Attributes |
|---|---|---|---|
| `hw.status` | gauge, one point per state | `1` | `hw.id`, `hw.name`, `hw.type` (`disk_controller`, `logical_disk`, `physical_disk`, `battery`), `hw.state` (`ok`, `degraded`, `predicted_failure`, `failed`), `hw.parent`; drives add `raid.enclosure`, `raid.slot` |
| `hw.temperature` | gauge | `Cel` | `hw.id`, `hw.type`, `hw.sensor_location` (`roc`, `controller`, `drive`, `battery`) |
| `hw.errors` | counter | `{error}` | drive `hw.id`, `error.type` (`media`, `other`, `predictive_failure`) |
| `hw.physical_disk.size` | gauge | `By` | drive identity |
| `hw.logical_disk.limit` | gauge | `By` | `hw.id` (`<controller>/ld<target>`), `hw.name` |
| `hw.battery.charge` | gauge | `1` | `hw.id` (`<controller>/bbu`) |
| `hw.battery.time_left` | gauge | `s` | battery identity |
| `raid.controller.info` | gauge, always 1 | `1` | `hw.model`, `hw.firmware_version`, `hw.serial_number`, `raid.memory.size` (MiB) |
| `raid.controller.reachable` | gauge | `1` | `hw.id`; 0 once when the driver reports the controller unusable |
| `raid.logical_disk.level` | gauge, always 1 | `1` | `raid.level` (`raid0`, `raid1`, `raid5`, `raid6`, `raid10`, `raid50`, `raid60`) |
| `raid.logical_disk.strip_size` | gauge | `By` | logical drive identity |
| `raid.logical_disk.cache_policy` | gauge | `1` | `raid.cache_policy` (the policy byte in hex) |
| `raid.progress` | gauge | `1` | `raid.operation` (`rebuild`, `patrol`, `check`, `init`, `reconstruction`, `copyback`), value 0 to 1 |
| `raid.battery.cycles` | gauge | `{cycle}` | battery identity |
| `raid.battery.next_learn_time` | gauge | `s` | Unix time of the next learn cycle |
| `otelcol_megaraid_event_read_errors` | counter | `{error}` | `hw.id`; event log reads that failed (only with `events.enabled`) |
| `raid.physical_disk.smart_state` | gauge, always 1 | `1` | `raid.smart_state` (`ok`, `predicted_failure`, `disabled`, `unsupported`, `standby`, `not_ata`, `unavailable`); only with `smart: true` |

The controller `hw.id` is its PCI address. A drive `hw.id` is its serial number, so it survives a
move between slots. The controller state is `failed` when a logical drive is offline or a drive has
failed, and `degraded` when a logical drive is degraded or a drive predicts failure. Logical drive
states: optimal `ok`, partially degraded and degraded `degraded`, offline `failed`. Drive firmware
states: online, hot spare, unconfigured good and JBOD `ok`; rebuild and copyback `degraded`; failed,
offline and unconfigured bad `failed`; a predictive-failure count above 0 on an otherwise healthy
drive gives `predicted_failure`. A controller with no battery gets no battery command.

Keys: `collection_interval` (60s), `smart` (false), `smart_interval` (15m, at least 60s),
`create_node` (false; makes the device node from `/proc/devices`, needs `CAP_MKNOD`) and an `events`
block with `enabled` (true), `storage` (a `file_storage` extension), `min_class` (`info`) and
`max_per_scrape` (200).

**Events.** With `events.enabled` the receiver reads the controller event log as logs: the body is
the description, with the attributes `raid.event.sequence`, `raid.event.class`, `raid.event.code` and
`raid.event.locale`. The last sequence number of each controller is kept in the `file_storage`
extension named by `events.storage` and saved after the logs reached the next consumer. With no saved
number the first start reads from the newest event, so no old event is sent; a saved number above
the newest one (log cleared) is reset to the newest. Events below `min_class` are skipped and at most
`max_per_scrape` are read in one scrape. Without a usable `file_storage` the receiver logs one error
that names it, sends no events and keeps sending metrics. A metrics-only pipeline reads no events.

**SMART through the controller.** With `smart: true` each SATA drive gets IDENTIFY DEVICE, SMART READ
DATA, SMART READ THRESHOLDS and SMART RETURN STATUS (and CHECK POWER MODE first) as ATA PASS-THROUGH
frames, decoded by the same code as the `smart` receiver, so a SATA disk behind the controller gives
the same `hw.physical_disk.*` series as one on a plain port. A drive is read at most once per
`smart_interval`, one at a time, never while it rebuilds, copies back, has failed or runs a patrol
read. SMART is never switched on or off and no off-line test starts; a drive with SMART off shows
`raid.smart_state` `disabled`. SAS drives are not read in this stage. A JBOD drive that
also shows as a plain disk is read by both receivers; the receiver does not match them by serial yet.

**Privilege and sharing.** Opening the node needs two things: file access to it (the node is mode 0600
and owned by root when `create_node` made it) and `CAP_SYS_ADMIN`, which the driver checks whatever
the file mode says. A non-root collector needs both; a refused open (`EACCES`) logs the owner and
mode of the node and names both, and a refused call (`EPERM`) names the capability. In a container,
add the device node and the capability. The driver takes one lock for every ioctl of a controller, so
any other tool that uses the node (`megacli`, `storcli`, `smartctl -d megaraid`) queues behind a
scrape and the receiver queues behind it, up to a 5 s limit per call. A call that times out is
not retried: the receiver keeps the node open, sends nothing more to that controller and logs one
line until the kernel returns from the call, then reads again at the next interval. A stuck
controller costs one buffer set, not one for each scrape. Linux edge builds only; the receiver
cannot be created elsewhere. A missing node needs the driver loaded, or `create_node: true`.

**Failed event reads.** A failed read of the event log (a refused or timed-out command, or a failed
read of the saved position) logs one line for each run of failures and raises
`otelcol_megaraid_event_read_errors`. A failed read of the saved position skips that scrape's
events: it is not taken for a first start, so unread events are neither dropped nor replayed. A
saved value that is not a number restarts the log at the newest event. Event timestamps are the
time the receiver read the event, not the controller's own time.

**Drives that sleep.** Before any SMART read the receiver sends CHECK POWER MODE. A drive in standby, or
one for which the controller returns no ATA status registers, is left alone and shows
`raid.smart_state` `standby` or `unavailable`; the receiver never wakes a drive to read it. This is
stricter than the `smart` receiver, which reads a drive whose power state it cannot tell.

**Series per drive.** Stage 1 sends about 10 series per drive (4 status, temperature, 3 error counts,
size, and one per running operation), about 10 per logical drive, 8 for the battery and 8 for the
controller. SMART adds up to 42 series per SATA drive (20 default attributes, two value types, power
on time and power cycles).

**Supported controllers.** The 2208 (Dell PERC H710 and similar, Supermicro 9271 and 9286 cards) and
the 3108 (PERC H730, 9361 and 9363 cards) are the declared generations. Any other product is read
the same way, best-effort, and logs one info line with its name at start.

**What is verified and what is not.** The struct layouts come from the FreeBSD `mfireg.h` and the Linux
`megaraid_sas.h` headers. Two scrubbed captures of real controllers are committed under
`receiver/megaraidreceiver/testdata/` and the tests decode them: a Supermicro 2208 (firmware
3.240.05, two RAID 1 volumes, four drives, no battery) and an Avago 3108 (firmware 4.680.00, six
single-drive RAID 0 volumes, six drives, a CacheVault module). They fixed the drive and logical drive
counts, the memory size, the drive temperature offset and the epoch of the next learn time. No real
controller was available for a live run. The states `degraded`, `failed`, `predicted_failure` and the
rebuild, patrol and check progress rest on synthetic buffers, not on live firmware, and both captures
are healthy. A 3108 answers the battery capacity command with status 61 and no data, so it sends no
`hw.battery.charge`, `hw.battery.time_left` or `raid.battery.cycles`. Unconfirmed on hardware: the
battery status bits, the `PD_SCSI_IO` frame layout used for SMART (sense buffer, status 0x2d), the
logical drive query list layout, the choice of the extended list commands from the controller limits
(the capability bit offset is unknown), and the cache policy bits (the byte is sent as it is). The
firmware version is the `APP` image component, not the package version.

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
