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
| `zfs.pool.size` | gauge | `By` | `zfs.pool` | pool property `size`; read through `/dev/zfs` |
| `zfs.pool.allocated` | gauge | `By` | `zfs.pool` | pool property `allocated` |
| `zfs.pool.free` | gauge | `By` | `zfs.pool` | pool property `free` |
| `zfs.pool.freeing` | gauge | `By` | `zfs.pool` | pool property `freeing` |
| `zfs.pool.leaked` | gauge | `By` | `zfs.pool` | pool property `leaked` |
| `zfs.pool.fragmentation` | gauge | `1` | `zfs.pool` | pool property `fragmentation` as a ratio from 0 to 1; no point when the pool reports none |
| `zfs.pool.capacity` | gauge | `1` | `zfs.pool` | allocated over size, from 0 to 1 |
| `zfs.pool.dedup.ratio` | gauge | `1` | `zfs.pool` | pool property `dedupratio`; 1 means no deduplication |
| `zfs.pool.health` | gauge | `1` | `zfs.pool` | health code, see below |
| `zfs.pool.readonly` | gauge | `1` | `zfs.pool` | pool property `readonly`, 0 or 1 |
| `zfs.pool.vdev.errors` | monotonic sum | `{error}` | `zfs.pool`, `error.type` (`read`, `write`, `checksum`) | error counters summed over the leaf vdevs |
| `zfs.pool.scrub.state` | gauge | `1` | `zfs.pool` | 0 idle, 1 scanning, 2 finished, 3 canceled; only while the newest scan is a scrub or there is none |
| `zfs.pool.resilver.state` | gauge | `1` | `zfs.pool` | same states, while the newest scan is a resilver |
| `zfs.dataset.space.used` | gauge | `By` | `zfs.pool`, `zfs.dataset`, `zfs.dataset.type` | dataset property `used`; FreeBSD without the device: `statfs` of the root dataset |
| `zfs.dataset.space.available` | gauge | `By` | `zfs.pool`, `zfs.dataset`, `zfs.dataset.type` | `available`; same FreeBSD fallback |
| `zfs.dataset.space.quota` | gauge | `By` | same three | `quota`; 0 means none |
| `zfs.dataset.space.refquota` | gauge | `By` | same three | `refquota`; 0 means none |
| `zfs.dataset.space.referenced` | gauge | `By` | same three | `referenced` |
| `zfs.dataset.space.written` | gauge | `By` | same three | `written` |
| `zfs.dataset.space.logical_used` | gauge | `By` | same three | `logicalused` |
| `zfs.dataset.space.used_by_dataset` | gauge | `By` | same three | `usedbydataset` |
| `zfs.dataset.space.used_by_snapshots` | gauge | `By` | same three | `usedbysnapshots` |
| `zfs.dataset.space.used_by_children` | gauge | `By` | same three | `usedbychildren` |
| `zfs.dataset.space.reservation` | gauge | `By` | same three | `reservation` |
| `zfs.dataset.space.refreservation` | gauge | `By` | same three | `refreservation` |
| `zfs.dataset.compression.ratio` | gauge | `1` | same three | `compressratio`; 1 means none |
| `zfs.dataset.volume.size` | gauge | `By` | same three | `volsize`; volumes only |
| `zfs.collector.success` | gauge | `1` | `collector` (`pool`, `dataset`) | 1 when the read ended without a failure, else 0 |
| `zfs.collector.duration` | gauge | `s` | `collector` | time the read took |
| `zfs.collector.objects` | gauge | `{object}` | `collector` | pools or datasets read |
| `zfs.collector.failures` | gauge | `{failure}` | `collector` | pool and dataset reads that failed in the scrape; objects that vanished are not counted |
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
FreeBSD sysctl source reads `kstat.zfs.<pool>.txgs`, the node FreeBSD 13.1 has, and falls
back to `kstat.zfs.<pool>.misc.txgs` when only that exists. It reads each text node into a
buffer of its own, since a raw kstat node answers a size probe with nothing. A pool state
node whose value is not a state name gives no `zfs.pool.state` point and one warning per
episode that names the node and its sysctl type; the pool's other series continue. With
`datasets` false no `dataset.objset-*` node is read.

The pool and dataset space series come from read-only ioctls on `/dev/zfs` (`ioctl_device`,
empty turns them off), sent in the collector process on Linux and FreeBSD: no `zfs` or
`zpool` command runs and no cgo code is linked. A fixed list of four request numbers is
sent (pool statistics, pool properties, dataset properties, next child dataset); any
other number is refused before the call. The receiver needs read access to the node
only. The pool series match `zpool list` (`zfs.pool.size` is SIZE, `.allocated` ALLOC,
`.free` FREE, `.fragmentation` FRAG as a ratio, `.capacity` CAP as a ratio); the dataset
series match `zfs list`. A quota or refquota of 0 means none and is emitted as 0. The
pool series are emitted whatever `source` is, for every selected pool. The dataset
series are emitted only for datasets matched by a non-empty `dataset_include`, and do
not need `datasets: true`; `dataset_exclude` and `max_datasets` apply. A glob matches one
path part per slash, so the receiver lists the children of a dataset only when a glob has more
parts and its leading parts match, and it reads the properties of the datasets a glob selects
and nothing else: `tank/*` costs one listing of `tank`, not a walk of the pool. Every dataset
series carries `zfs.dataset.type`, `filesystem` or `volume`. The receiver keeps no
per-dataset state, so a volume created or destroyed between scrapes starts or ends its
series with no warning. No snapshot series is emitted.

`zfs.pool.health` uses the codes of `zfs_exporter`: 0 ONLINE, 1 DEGRADED, 2 FAULTED,
3 OFFLINE, 4 UNAVAIL, 5 REMOVED and 6 SUSPENDED, so the alert is `zfs.pool.health != 0`.
The code follows the name `zpool` shows: a root vdev that cannot open is FAULTED when its
data is corrupt or its log is bad and UNAVAIL otherwise, a closed one is OFFLINE, and a
pool that reports itself suspended is 6. `zfs.pool.state` is unchanged. A state outside
the table, such as a split pool, gives no `zfs.pool.health` point and one warning until it
clears. `zfs.pool.vdev.errors`, `zfs.pool.scrub.state` and `zfs.pool.resilver.state` appear
only when the pool reply holds them, and a pool statistics call that fails or carries an
error in its reply gives none of them and one warning. A failed ioctl for
one pool or dataset skips that object with one warning and adds one to
`zfs.collector.failures`; the other objects still report. `zfs.collector.success` is 0
when the device cannot be opened, every pool read failed or a dataset listing broke;
`zfs.collector.objects` shows the pool and dataset counts read. A host with no pools
sends none of these. When `/dev/zfs` cannot be opened one warning per episode names the
node and the kstat series continue; on FreeBSD the `statfs` root-dataset space
(`zfs.dataset.space.used` and `.available`, from `getfsstat`, the mount of the root
dataset) then stays. A read that takes longer than 20 seconds, for example on a suspended
pool, does not hold up the scrape: it keeps running in one goroutine, one warning says the
read is slower than its limit, and the scrape after it finishes sends its result. When
the device goes away, as after a module reload, it is opened again on the next scrape.

Mapping from `zfs_exporter` 2.3.4:

| Exporter series | Receiver series |
|---|---|
| `zfs_pool_allocated_bytes`, `_free_bytes`, `_size_bytes`, `_freeing_bytes`, `_leaked_bytes` | `zfs.pool.allocated`, `.free`, `.size`, `.freeing`, `.leaked` |
| `zfs_pool_fragmentation_ratio`, `_deduplication_ratio` | `zfs.pool.fragmentation`, `zfs.pool.dedup.ratio` |
| `zfs_pool_health`, `zfs_pool_readonly` | `zfs.pool.health`, `zfs.pool.readonly` |
| `zfs_dataset_available_bytes`, `_logical_used_bytes`, `_quota_bytes`, `_referenced_bytes`, `_used_by_dataset_bytes`, `_used_bytes`, `_written_bytes` | `zfs.dataset.space.available`, `.logical_used`, `.quota`, `.referenced`, `.used_by_dataset`, `.used`, `.written` |
| `zfs_dataset_volume_size_bytes` | `zfs.dataset.volume.size` |
| `zfs_scrape_collector_success`, `_duration_seconds` | `zfs.collector.success`, `zfs.collector.duration` |

The nine alert rules of the exporter, as conditions on the receiver series: pool health
above 0 (`zfs.pool.health`); read-only (`zfs.pool.readonly` is 1); leaked bytes above 0
(`zfs.pool.leaked`); allocated over size above 0.8 and above 0.9 (`zfs.pool.capacity`);
the linear prediction of `zfs.pool.free` four hours ahead below 0; fragmentation above
0.70 (`zfs.pool.fragmentation`); dataset used over quota where quota is above 0
(`zfs.dataset.space.used` over `zfs.dataset.space.quota`, quota above 0); collector
success equal to 0 (`zfs.collector.success`). The exporter snapshot collector is off
and the receiver has no snapshot series.

The ioctl numbers and the layout of the replies follow the OpenZFS 2.1 headers; they
were not checked against a live pool on every supported system, so compare the first
values with `zpool list` and `zfs list` before relying on them. See the [README](../README.md#zfs-pool-and-arc-metrics).

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

The `guests` scraper makes no agent call for guests that are not running. After a failed agent call it leaves that guest
alone for the collection interval, then twice as long after each further failure, never longer than 24
hours. A guest whose agent is not running (HTTP 500 "QEMU guest agent is not running") is a normal
state: it is recorded once as `agent=unavailable` and checked again at its backoff. Containers have no
agent; their addresses come from `/nodes/{node}/lxc/{id}/interfaces`.

| Signal | Name | Type, unit | Attributes |
|---|---|---|---|
| metric | `pve.guest.filesystem.usage` | gauge, `By` | `pve.vmid`, `pve.node`, `mountpoint`, `fstype`, `device` |
| metric | `pve.guest.filesystem.size` | gauge, `By` | same |
| metric | `pve.guest.lock` | gauge, always 1, `1` | `pve.vmid`, `pve.node`, `type`, `state` |
| log | `pve guest fact` | change event | `pve.vmid`, `pve.node`, `pve.fact` (`os`, `hostname`, `addresses`, `agent`), `pve.fact.value` |

A fact record is written when a value first appears or changes; a sweep that finds nothing new writes
none. `addresses` is a sorted, comma-joined list without loopback and link-local addresses, which change with a bridge or a container. A fact is counted as delivered only when the logs pipeline took it; a failed send, or a receiver with no logs pipeline, makes the next sweep write it again. A guest that fails (not "agent not running") logs one WARN when it enters backoff.

`pve.guest.lock` exists only while a guest is locked: a guest with no lock has no series, and no point
with value 0 is sent. The series ends at the first sweep after the lock clears. `state` is the lock value
the node's guest list gives (`backup`, `migrate`, `clone`, `snapshot`, `snapshot-delete`, `rollback`,
`create`, `suspending`, `suspended`); a value outside this list is sent as PVE gave it. The lock is read
for running and stopped guests from the `qemu` and `lxc` lists the scraper already reads, so it adds no
request. It is read once per `guests` collection interval (15 minutes by default); lower the interval to
see short locks. A node that is down gives no guests and no lock series. To alert on a lock other than
`backup`, `migrate`, `clone` and `suspended` that lasts over 30 minutes (Prometheus names):

```promql
min_over_time(pve_guest_lock{state!~"backup|migrate|clone|suspended"}[30m]) == 1
```

Series count for the `resources` scraper: `13G + 13N + 5S` for G guests, N nodes and S storages, plus
the info gauges. The `guests` scraper adds two series per mount of each running guest with an agent.
Each guest restart changes no series; a new or removed guest id adds or ends a set. Linux edge builds
only; see the [README](../README.md#proxmox-ve-metrics).

### Hardware sensors — `receiver/hwmonreceiver`

Reads `hwmon_path` (default `/sys/class/hwmon`) and the thermal zones in the sibling
directory `thermal` every `collection_interval` (default `60s`), and on FreeBSD the
CPU and ACPI temperature sysctls. All metrics are gauges with the attributes `hw.id`,
`hw.name`, `hw.parent` and `hw.type`; the names follow the `hw.*` semantic conventions.
`hw.type` comes from the sensor kind and has the OpenTelemetry values `temperature`
(`hw.temperature` and `hw.temperature.limit`), `fan` (`hw.fan.speed`) and `voltage`
(`hw.voltage`), so these rows match the RAID rows that already carry it. The value
`power_supply` is reserved for a power supply sensor; the receiver reads none yet.
A kind with no `hw.type` value gives its metrics without the attribute and one debug line.

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

The state comes from the SCSI `state` file of the device
(`/sys/class/scsi_device/<H:C:T:L>/device/state`); the size, `hw.name` and `iscsi.device`
come from the block device under it. `iscsi.lun.state` exists for every LUN the receiver
reports, with state `unknown` when the state is unknown. `hw.status` exists only for a LUN
whose state is in the table, and `hw.logical_disk.limit` only for a LUN with a block
device whose size can be read. A host that sends only `iscsi.lun.state` for its LUNs
therefore has LUNs with state `unknown`: no block device, a `state` file that cannot be
read, or a state that is not in the table. Look at `iscsi.device` and the state value, and
at `/sys/class/scsi_device/<H:C:T:L>/device/state` and `.../device/block` on the host.

A LUN with no block device always has state `unknown`, whatever its `state` file says. A
LUN with a block device whose `state` file cannot be read also has state `unknown` and no
`hw.status` point, but keeps its `hw.logical_disk.limit` when the size can be read. A
warning is logged once for each run of failures; an alert should test for
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

### Hardware inventory — `receiver/inventoryreceiver`

Reports the hardware of a physical host once a day, on the first start and then every
`collection_interval` (default `24h`, at least `1m`). Each fact is an info gauge of value
`1` in `1`, and the facts are its attributes. The series go through the metrics
pipeline into the existing gauge table (`otel_metrics_gauge`); there is no table of their own and no exporter
of their own. A fact the host does not give is left out of its series and is never a warning.
Linux edge, static Linux edge and Windows edge builds.

| Metric | Attributes |
|---|---|
| `inventory.cpu.info` | one series per socket: `hw.id` (`cpu/<socket>`), `hw.vendor`, `hw.model`, `cpu.socket`, `cpu.sockets`, `cpu.cores` and `cpu.threads` (per socket), `cpu.frequency.nominal` and `cpu.frequency.max` (Hz) |
| `inventory.memory_module.info` | one series per populated slot: `hw.id` (the slot name), `hw.vendor`, `hw.model` (part number), `hw.serial_number`, `hw.asset_tag`, `memory.size` (bytes), `memory.type` (for example `DDR4`), `memory.speed` (MT/s), `memory.configured_speed` (MT/s), `memory.form_factor`, `memory.bank` |
| `inventory.memory.info` | one series per host: `memory.total` (bytes of physical RAM; no `hw.id`, like the board and BIOS series). Linux reads `MemTotal` of `/proc/meminfo` (kB, times 1024), needs no root and does not depend on SMBIOS; Windows reads `ullTotalPhys` of `GlobalMemoryStatusEx`. A missing file or line, a bad, zero or too large number gives no series |
| `inventory.board.info` | `hw.vendor`, `hw.model`, `hw.version`, `hw.serial_number`, `hw.asset_tag` |
| `inventory.bios.info` | `hw.vendor`, `hw.firmware_version`, `bios.date` |
| `inventory.system.info` | `hw.vendor`, `hw.model`, `hw.version`, `hw.serial_number`, `system.family`, `chassis.vendor`, `chassis.type`, `chassis.serial_number`, `chassis.asset_tag` |
| `inventory.drive.info` | one series per drive: `hw.id` (serial number, else WWID, else on Linux the place on the bus such as the PCI address, else the device name), `hw.vendor`, `hw.model`, `hw.firmware_version`, `hw.serial_number`, `hw.wwid` (Linux), `drive.size` (bytes), `drive.type` (`HDD` or `SSD`), `drive.bus` (`nvme`, `sata`, `sas`, `mmc`, `usb`, and others on Windows) |
| `inventory.gpu.info` | one series per GPU (the video controller of a BMC, such as ASPEED or Matrox G200e, is not a GPU and is left out): `hw.id` (PCI address on Linux), `hw.model` (Windows), `hw.driver` (Linux), `hw.driver_version` (Windows), `gpu.vendor_id`, `gpu.device_id`, `gpu.memory` (bytes) |
| `inventory.collection.status` | gauge `1` or `0` with `inventory.source` (`cpu`, `memory`, `memory_total`, `board`, `bios`, `system`, `drive`, `gpu`, `fru`); `1` when the source was read, `0` when it was not readable |

Placeholder text that firmware writes where it has no value (`To be filled by O.E.M.`,
`Default string`, `Not Specified`, all-zero serial numbers and the like) is treated as absent. A series with no
fact besides its identity is not sent. A series keeps the same attributes from one day to the next.

**Serial numbers are sensitive.** They are sent as read, on the series of the board, system, memory module and
drive, so restrict who can query these series. The inventory series arrive once a day: a query on them needs a
window of at least 25 h. The receiver sends no uptime or boot time series; uptime comes from a continuous metric.
How long the history stays depends on the retention (TTL) of the gauge table in ClickHouse, which is set on the
ClickHouse side and not by the receiver.

**Physical hosts only.** A virtual machine or a container sends no inventory series and the receiver logs one info
line at start. On Linux a host is not physical when the CPU flags show a hypervisor, `/sys/hypervisor` names one, `/proc/device-tree`
has a hypervisor node (`kvm`, `xen`), a `fw-cfg` node or a QEMU machine name (ARM guests have no DMI), the firmware strings
name one (QEMU, KVM, SeaBIOS, VMware, VirtualBox, Hyper-V, Xen, an Amazon EC2 type that is not `.metal`, Google Compute Engine
and a few more), `/.dockerenv`, `/run/.containerenv` or a non-empty `/run/systemd/container` exists (LXD and systemd-nspawn
containers have the host's firmware strings), the collector's own environment has `container=`, or the cgroup of
process 1 names `docker`, `kubepods`, `containerd`, `libpod`, `crio` or `lxc`. A Xen control domain (dom0), marked by
`/sys/hypervisor/start_flags/initdomain` (`1`, `true` or `yes`), is physical even though its CPU flags show a hypervisor.
An ARM guest booted through ACPI has no device tree and is reported as physical. A virtual machine whose firmware strings and flags are all hidden cannot be
told apart and is reported as physical. On Windows the firmware strings decide. When none of
these can be read the host is treated as physical, with one info line. A collector that runs in a container on a physical host
reports nothing; run it on the host itself.

**Sources.** Linux reads `/proc/cpuinfo`, `/sys/devices/system/cpu` (topology and cpufreq),
`/proc/meminfo` (total RAM), `/sys/class/dmi/id`, the SMBIOS table `/sys/firmware/dmi/tables/DMI` (memory modules, type 17), `/sys/block`
and `/sys/bus/pci/devices`. The DMI table and the serial number files are readable by root only: without access
those facts are left out, `inventory.collection.status` is `0` for `memory` (and for the DMI sources when
`/sys/class/dmi/id` is missing as well), and one info line is logged for each start. Windows reads the SMBIOS
firmware table (`RSMB`), the registry (`HARDWARE\DESCRIPTION\System`, and the display adapter class), processor
topology from `GetLogicalProcessorInformationEx` and the storage properties of `\\.\PhysicalDrive<N>`; all of it in
process. No external program runs and there is no cgo. The total RAM series comes with the other facts in the same
collection pass (`collection_interval`); its status source is `memory_total`, apart from `memory` (the DMI modules).

`memory.speed` and `memory.configured_speed` are data rates in MT/s. For SMBIOS before 3.1 the configured speed may be a
clock in MHz; for DDR, DDR2, DDR3 and DDR4 a value under the lowest data rate of the type (200, 400, 800 and 1600) is taken as a
clock and doubled. This is a heuristic: firmware on older versions often reports MT/s already, and such a value is kept.
Speeds above 65534 MT/s come from the extended fields of SMBIOS 3.3. If the CPU list of `/proc/cpuinfo` is longer than
16 MiB or 4096 processors it is cut: the CPU series then carry no socket, core or thread counts, with a debug line.
On Windows a drive whose size cannot be read keeps its other facts and a debug line says so.

Not available from these sources: GPU model names on Linux (the receiver has no PCI id table, so only the ids are
sent), GPU memory on Linux except for `amdgpu`, the maximum CPU frequency on Windows, and drive serial numbers
the kernel does not expose. `max_drives` (default 64, at most 1024) caps the drive series; more drives log one info line.

The first version covers CPU, memory, drives, GPU, board, BIOS and system, plus the BMC FRU series below. Network cards,
power supplies and firmware versions of other devices are not collected yet.

Example queries that build one table per fact class, one row per host and item:

```sql
-- CPUs and memory modules
SELECT HostName, Attributes['hw.model'] AS cpu, Attributes['cpu.sockets'] AS sockets,
       Attributes['cpu.cores'] AS cores_per_socket, Attributes['cpu.threads'] AS threads_per_socket
FROM otel.otel_metrics_gauge
WHERE MetricName = 'inventory.cpu.info' AND TimeUnix > now() - INTERVAL 25 HOUR
GROUP BY HostName, cpu, sockets, cores_per_socket, threads_per_socket;

SELECT HostName, Attributes['hw.id'] AS slot, Attributes['memory.type'] AS type,
       toUInt64OrZero(Attributes['memory.size']) / 1048576 AS size_mib, Attributes['hw.model'] AS part
FROM otel.otel_metrics_gauge
WHERE MetricName = 'inventory.memory_module.info' AND TimeUnix > now() - INTERVAL 25 HOUR
GROUP BY HostName, slot, type, size_mib, part;

-- Drives, GPUs and boards
SELECT HostName, Attributes['hw.model'] AS model, Attributes['hw.serial_number'] AS serial,
       toUInt64OrZero(Attributes['drive.size']) / 1e9 AS size_gb
FROM otel.otel_metrics_gauge
WHERE MetricName = 'inventory.drive.info' AND TimeUnix > now() - INTERVAL 25 HOUR
GROUP BY HostName, model, serial, size_gb;

SELECT HostName, Attributes['gpu.vendor_id'] AS vendor, Attributes['gpu.device_id'] AS device, Attributes['hw.model'] AS model
FROM otel.otel_metrics_gauge
WHERE MetricName = 'inventory.gpu.info' AND TimeUnix > now() - INTERVAL 25 HOUR
GROUP BY HostName, vendor, device, model;

SELECT HostName, Attributes['hw.vendor'] AS vendor, Attributes['hw.model'] AS board, Attributes['hw.serial_number'] AS serial
FROM otel.otel_metrics_gauge
WHERE MetricName = 'inventory.board.info' AND TimeUnix > now() - INTERVAL 25 HOUR
GROUP BY HostName, vendor, board, serial;
```

Check which facts are missing with `inventory.collection.status`: a host whose status is `0` for a source
could not read it.

**BMC FRU series** come from the `ipmi` receiver of the scrape image, not from this receiver. Once per `fru_interval`
(default `24h`, at least `1h`) for each BMC, on the same user session as the sensors, it reads the FRU inventory with the
two read-only IPMI storage commands Get FRU Inventory Area Info and Read FRU Data, and the SDR records that list the
DIMM and processor FRU devices. It sends `inventory.bmc.fru.info` (info gauge, `1`) with `fru.type` (`board`,
`product`, `chassis`, `dimm`, `cpu`), `hw.id` (`fru/<device>`), `hw.name` (the SDR name of a DIMM or processor) and the fields of
the record that exist: `hw.vendor` (manufacturer), `hw.model` (product name), `hw.part_number`, `hw.version`,
`hw.serial_number`, `hw.asset_tag`, `chassis.type`. The resource carries the BMC (`host.name`, `server.address`, `hw.vendor`).
A DIMM or processor series uses the product area, or the board area when there is none. A record the BMC does not
have gives no series. A failed read gives `inventory.collection.status` `0` with `inventory.source` `fru`, one
warning for each run of failures and a retry in an hour; it never changes `hw.bmc.up`. A device that is not read is no
failure: one that another controller holds (the adapter cannot bridge to it), one read by words, and a DIMM that holds
JEDEC SPD instead of an IPMI FRU image. They are counted in one info line and the status stays `1`. The FRU pass has its own
time budget and is not part of `hw.bmc.scrape.duration`. Reads are limited to 4 KiB
for each device and 64 DIMM and processor devices for each BMC. The same remarks on serial numbers and the 25 h window apply.

### Dell iDRAC — `receiver/idracreceiver` and `receiver/hwschema`

Reads a Dell iDRAC through Redfish every `collection_interval` (default `60s`) per target. The legacy `Thermal` and `Power` resources are read first; `ThermalSubsystem` and `PowerSubsystem` only when the iDRAC no longer serves them, and they have no temperature limits, voltages or host power. A scrape makes about 10 requests, so set `scrape_timeout` above the time of that many requests on a slow iDRAC; `max_concurrent` (default 8) limits the targets read at once; the storage reads have their own limit of the same size, so a slow storage read never holds the slot of a base scrape. The base scrape takes about 3.8 s on the iDRAC it was measured on. Storage controllers, volumes and drives are read on their own timer every `storage_interval` (default `5m`, or `collection_interval` if that is longer; it may equal `collection_interval` and may not be below it), so a slow storage read never delays the base scrape. `storage_max_drives` (default `256`) limits the drives read in one storage read. A storage read has `scrape_timeout` plus 1 second for each drive it finds (at most 10 minutes, or `scrape_timeout` when that is longer), and each request has `request_timeout`; there is no key for the 1 second. A read that runs out of time sends what it has read and counts one error. The metric names, units and
attribute keys are in the `hwschema` module, which the `ipmi` receiver of the scrape image shares, so one query covers
both. All metrics are gauges.

| Metric | Unit | Redfish source |
|---|---|---|
| `hw.status` | `1` | `Status.Health` of the system (`host`), chassis (`enclosure`), fans (`fan`), power supplies (`power_supply`), DIMMs (`memory`), processors (`cpu`) and the BMC (`bmc`); and, on the storage interval, of each storage resource (`storage`), controller (`disk_controller`), volume (`logical_disk`) and drive (`physical_disk`); one point per `hw.state` (`ok`, `degraded`, `failed`, `needs_cleaning`, `predicted_failure`), 1 for the current one. `OK` is `ok`, `Warning` `degraded`, `Critical` `failed`; an unknown or null health gives no point. The state of a drive follows its `Status.Health` only; `FailurePredicted` is in `hw.physical_disk.predicted_failure` and never changes the state |
| `hw.temperature` | `Cel` | `ReadingCelsius` |
| `hw.temperature.limit` | `Cel` | `UpperThresholdCritical` (`critical`) and `UpperThresholdNonCritical` (`high.degraded`) |
| `hw.fan.speed` | `rpm` | A fan `Reading` whose `ReadingUnits` is RPM, or `SpeedPercent.SpeedRPM` |
| `hw.fan.speed_ratio` | `1` | A fan reading in percent, or `SpeedPercent`, divided by 100 |
| `hw.voltage` | `V` | `ReadingVolts` |
| `hw.host.power` | `W` | `PowerConsumedWatts` of the legacy `Power` resource; not available from `PowerSubsystem` |
| `hw.power_supply.usage`, `.limit`, `.utilization` | `W`, `W`, `1` | `LastPowerOutputWatts` (or `OutputPowerWatts` of the supply `Metrics` link), `PowerCapacityWatts`, and their ratio |
| `hw.physical_disk.size` | `By` | `CapacityBytes` of a drive (storage interval) |
| `hw.physical_disk.endurance_utilization` | `1` | 1 minus `PredictedMediaLifeLeftPercent` divided by 100, with `hw.physical_disk.state` `used`; a drive with no life field gives no point (storage interval) |
| `hw.physical_disk.indicator` | `1` | 1 when the locate LED of a drive is lit or blinking (`LocationIndicatorActive`, or `IndicatorLED` on older firmware), else 0; no point when the field is missing |
| `hw.physical_disk.predicted_failure` | `1` | `FailurePredicted` of a drive: 1 or 0; no point when the field is missing or null (storage interval) |
| `hw.disk_controller.speed` | `Mbit/s` | `SpeedGbps` of a controller times 1000 |
| `hw.storage.drive_count` | `{drive}` | Number of drives the `Drives` list of a storage resource holds, with `hw.id` `storage/<storage>`; 0 for an empty list, no point when there is no list (storage interval) |
| `hw.logical_disk.limit` | `By` | `CapacityBytes` of a volume, with `hw.logical_disk.raid_level` (`RAIDType`), `hw.logical_disk.volume_type` where the iDRAC reports them |
| `hw.logical_disk.media_span_count` | `{span}` | Dell `MediaSpanCount` of a volume; no point when the iDRAC does not report it. It is a series and not an attribute, so an alert can use it and a change of the value does not start a new series of `hw.status` |
| `hw.memory.size` | `By` | `CapacityMiB` times 1,048,576 |
| `hw.bmc.up` | `1` | 1 when the scrape worked; 0 with a `reason` (`timeout`, `auth`, `refused`, `backoff`, `tls`, `error`) when not |
| `hw.bmc.scrape.duration` | `s` | Time of the scrape |
| `hw.bmc.scrape.errors` | `{error}` | Running count (monotonic sum) of failed storage reads, by `path_group` (`storage`, `controller`, `drive`, `volume`), sent for all four groups from the first read (0 until a failure) so `increase()` sees the first one; a read that runs out of time counts once, under the group being read; a path that answers 404 counts once, when it first answers it, and again only after it answered normally in between |

The status points of a controller carry `hw.model`, `hw.firmware_version` and `hw.parent` (the storage id); those of a volume carry the RAID level, volume type and `hw.parent`. The id of a controller (`hw.id` `disk_controller/<storage>/<controller>`) is the `Id` of the controller resource (the `Controllers` link). Where the storage lists one controller in `StorageControllers` with no `Id`, it is the id of the storage, which is the `Id` the `Controllers` link gives, so both layouts give one id; then comes the `MemberId`, then the position in the list. Each drive is its own resource: the resource of the iDRAC plus `hw.serial_number`, `hw.model` and `hw.firmware_version` of the drive, `hw.physical_disk.type` (the Redfish `MediaType`) and `hw.physical_disk.protocol`; its points carry `hw.id` (`physical_disk/<storage>/<drive>`), `hw.name` and `hw.parent`. The storage series of a target use a resource with the iDRAC identity only (no model, serial or firmware of the server), so that it does not change when the iDRAC firmware does.

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
| A path answers 404 (older firmware, or a reset in progress) | Marked absent, one info line, asked again after 6 hours; the rest is read. Storage paths differ: the `Storage` collection is asked again after 1 hour, and every path below it (storage, controller, volume and drive) is asked on each storage read, so a drive that was pulled and replaced is reported at the next read |
| `$expand` answers 400 or 501 | The collection is read again without the query, and the query is not sent again to that iDRAC |
| A drive or a collection member answers an error | That drive or member is skipped with one warning until a scrape without one; the rest is read and `hw.bmc.up` is 1 |
| Another resource answers 500 or an error | `hw.bmc.up` 0, reason `refused`, and only that and the duration are sent |
| A storage path answers 401, 403, 404, 405 or 5xx, times out, returns a body that is not JSON, or a field of the wrong type | That part is skipped, `hw.bmc.scrape.errors` counts it by `path_group`, one warning until a read without a failure, and the rest of the storage read, the base scrape and `hw.bmc.up` are unchanged |
| An older iDRAC has no `Storage` collection, or a storage has no volumes or no controller list | No series for what is missing; a 404 is marked absent with one info line |
| A controller has no drives, or a drive has no `Status` or no life field | The controller keeps its status; the drive keeps its size and endurance without a status point, or its status without endurance |
| A link that is not under the system `Storage` resource, or more drives than `storage_max_drives` | Not requested; counted under `drive` with one warning |
| An entry of the `Drives` list is a link with extra annotations (`@odata.type`) | Fetched as a link. An entry is a drive the list holds itself only when it has drive fields (`Status`, `CapacityBytes`, `Model`, `MediaType`, `Protocol`, `Id` or `Name`) |
| An entry of the `Drives` list has no drive field and no link, or a drive has no id | Counted under `drive` with one warning |
| A storage read runs past its budget (`scrape_timeout` plus 1 second for each drive) | The points read so far are sent, one failure is counted under the group being read, and one warning is logged until a read without a failure. A group that was not read in that run keeps its warning state |
| A decoder panics in the base scrape | Recovered; one error log per target; `hw.bmc.up` 0 with reason `error` for that scrape; the next scrape runs |
| A decoder panics in the storage read | Recovered; one error log per target; the read is counted in `hw.bmc.scrape.errors` and sends no storage points; `hw.bmc.up` is left to the base scrape; the next read runs |

Alert rules: `hw.bmc.up` equal to 0 for 15 minutes, and no `hw.bmc.up` series for three collection intervals. A scrape
reads about 12 to 20 resources one after the other, one request at a time. Linux and static Linux edge builds only; see
the [README](../README.md#dell-idrac-health-and-sensors).

#### Storage series of `idrac_exporter`

| `idrac_exporter` series | Receiver series |
|---|---|
| `idrac_storage_health` | `hw.status` with `hw.type` `storage` |
| `idrac_storage_controller_health`, `_info`, `_speed_mbps` | `hw.status` with `hw.type` `disk_controller` (`hw.model`, `hw.firmware_version` on its points), `hw.disk_controller.speed` |
| `idrac_storage_drive_health`, `_info` | `hw.status` with `hw.type` `physical_disk` (from `Status.Health` only); the drive resource holds `hw.serial_number`, `hw.model`, `hw.firmware_version`, `hw.physical_disk.type`, `hw.physical_disk.protocol`; `hw.storage.drive_count` counts the drives of a storage. The exporter has no series for `FailurePredicted`; the receiver adds `hw.physical_disk.predicted_failure` |
| `idrac_storage_drive_capacity_bytes` | `hw.physical_disk.size` |
| `idrac_storage_drive_life_left_percent` | `hw.physical_disk.endurance_utilization` (used fraction: life left is 100 times (1 minus the value)) |
| `idrac_storage_drive_indicator_active` | `hw.physical_disk.indicator` |
| `idrac_storage_volume_health`, `_info` | `hw.status` with `hw.type` `logical_disk` (RAID level and volume type as attributes) |
| `idrac_storage_volume_capacity_bytes`, `_media_span_count` | `hw.logical_disk.limit`; `hw.logical_disk.media_span_count` (a numeric series, unit `{span}`) |

The exporter health value 0 means `OK`; the receiver gives `hw.state` `ok` for it. The exporter rule `IdracDriveMissing` counts `idrac_storage_drive_info`; the receiver has the series `hw.storage.drive_count`, one for each storage resource, so a missing drive alert is a fall of `hw_storage_drive_count` below its value of an earlier time, for example `hw_storage_drive_count < (hw_storage_drive_count offset 1h)`; counting the `hw.status` series of the drives works too, for example `count by (host_name, hw_parent) (hw_status{hw_type="physical_disk", hw_state="ok"})`. The storage rules port as follows (label names depend on the exporter in use): unhealthy storage, controller, volume or drive is `hw_status{hw_type=~"storage|disk_controller|logical_disk|physical_disk", hw_state=~"degraded|failed|predicted_failure"} == 1`, and a drive the iDRAC predicts to fail is `hw_physical_disk_predicted_failure == 1`; drive life low or critical is `(1 - hw_physical_disk_endurance_utilization) * 100` below the low or critical percentage. Keep `idrac_exporter` running until the series agree on your hosts.

### Raw bytes in hardware labels — `receiver/hwschema` and the hardware receivers

Drive, controller, BMC and GPU strings can hold bytes that are not valid UTF-8, and Prometheus rejects such a label value. Each byte outside a valid UTF-8 sequence is replaced by U+FFFD (one replacement for each byte), the label is kept, and valid text is not changed. This covers every text attribute the shared `hwschema` builder writes: `hw.id`, `hw.name`, `hw.parent`, `hw.vendor`, `hw.model`, `hw.serial_number`, `hw.firmware_version`, the host and server attributes, and the extra attributes of status, volume and FRU points. It reaches the `idrac`, `ipmi` (SDR sensor names), `megaraid`, `smart` and `windiskhealth` receivers through that builder. The `iscsi` receiver (targets, portals, devices) and the `gpu` receiver (UUID, name, model, driver version) apply the same rule in their own code, and the `inventory` receiver replaces invalid bytes the same way and also removes control characters. Control characters such as NUL are valid UTF-8 and are not changed by this rule. A series whose label had a raw byte carries a different label value after the fix, so it shows as a new series.

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
| `hw.errors` | counter | `{error}` | drive `hw.id`, `error.type` (`media`, `other`, `predictive_failure`, `uncorrected`); `uncorrected` adds `raid.error.direction` (`read`, `write`, `verify`) |
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
| `raid.physical_disk.smart_state` | gauge, always 1 | `1` | `raid.smart_state` (`ok`, `predicted_failure`, `disabled`, `unsupported`, `standby`, `unavailable`); only with `smart: true` |

The controller `hw.id` is its PCI address. A drive `hw.id` is its serial number, so it survives a
move between slots. The controller state is `failed` when a logical drive is offline or a drive has
failed, and `degraded` when a logical drive is degraded or a drive predicts failure. Logical drive
states: optimal `ok`, partially degraded and degraded `degraded`, offline `failed`. Drive firmware
states: online, hot spare, unconfigured good and JBOD `ok`; rebuild and copyback `degraded`; failed,
offline and unconfigured bad `failed`; a predictive-failure count above 0 on an otherwise healthy
drive gives `predicted_failure`. A controller with no battery gets no battery command.

Keys: `collection_interval` (60s), `smart` (false), `smart_interval` (15m, at least 60s),
`smart_when_power_unknown` (false; only with `smart`),
`create_node` (false; makes the device node from `/proc/devices`, needs `CAP_MKNOD`) and an `events`
block with `enabled` (true), `storage` (a `file_storage` extension), `min_class` (`info`) and
`max_per_scrape` (200).

**Events.** With `events.enabled` the receiver reads the controller event log as logs: the body is
the description, with the attributes `raid.event.sequence`, `raid.event.class`, `raid.event.code` and
`raid.event.locale`. The last sequence number of each controller is kept in the `file_storage`
extension named by `events.storage` and saved after the logs reached the next consumer. With no saved
number the first start reads from the newest event, so no old event is sent; a saved number above
the newest one (log cleared) is reset to the newest. Events below `min_class` are skipped and at most
`max_per_scrape` are read in one scrape. Without a usable `file_storage` the receiver sends no events and keeps sending metrics. When `events.enabled` is
not set and `events.storage` is empty it logs one info line saying events wait for `events.storage`; with
`events.enabled: true` set in the file, or a `storage` name that no extension answers, it logs one error
that names `file_storage`. A metrics-only pipeline reads no events.

A command the controller reports as not supported is asked again at every scrape, so a refusal that lasted only
while the battery learned does not hide its metrics. It is logged once per episode, and again if it works and
is refused later. A command the controller answers as an invalid command is remembered and not sent again until
the receiver restarts.

**SMART through the controller.** With `smart: true` each SATA drive gets IDENTIFY DEVICE, SMART READ
DATA, SMART READ THRESHOLDS and SMART RETURN STATUS (and CHECK POWER MODE first) as ATA PASS-THROUGH
frames, decoded by the same code as the `smart` receiver, so a SATA disk behind the controller gives
the same `hw.physical_disk.*` series as one on a plain port. A drive is read at most once per
`smart_interval`, one at a time, never while it rebuilds, copies back, has failed or runs a patrol
read. SMART is never switched on or off and no off-line test starts; a drive with SMART off shows
`raid.smart_state` `disabled`. A JBOD drive that
also shows as a plain disk is read by both receivers; the receiver does not match them by serial yet.

**Drives with no power state.** Some controllers return no power state for CHECK POWER MODE (no ATA
registers), so the receiver cannot tell whether the drive is spinning. Those drives are skipped unless the
option is on: by default the receiver sends no SMART command, logs one warning for the drive, and emits no
SMART series for it. With `smart_when_power_unknown: true`, set on the receiver of the host that opts in,
the receiver reads SMART for such a drive like any other and logs no warning. A drive that answers
standby is skipped either way. Risk: a spun-down drive can wake at every `smart_interval`. The belief
that RAID-backed drives never spin down is unverified.

**SAS drives.** With `smart: true` a drive whose vendor is not `ATA` is read with SCSI `LOG SENSE`
(opcode 4Dh, the cumulative values) of seven log pages: the page list, temperature, informational
exceptions, read, write and verify errors, and start-stop cycles. The decoders read by page code
and ignore unknown parameters. The drive gives `hw.status` (`predicted_failure` when the
informational exceptions page holds additional sense code 5Dh, failure prediction, else the RAID
state; a temperature warning (0Bh) does not count), `hw.temperature` when the drive info had none,
`hw.errors` with `error.type` `uncorrected` and `raid.error.direction` `read`, `write` or `verify`
(the uncorrected totals of the three error pages), `hw.physical_disk.power_cycles` (accumulated
start-stop cycles) and `raid.smart_state` `ok` or `predicted_failure`. The frame check allows
`LOG SENSE` for those pages at one length and no other new command. The sense key of a refusal
decides: ILLEGAL REQUEST skips that page, counted in one warning per drive and read again at the
next interval, and a drive that refuses the page list shows `unsupported`. NOT READY, UNIT
ATTENTION and ABORTED COMMAND are tried once more in the same read; if the drive is still not ready,
a page counts as refused and the page list shows `unavailable` and is read at the next scrape, not
after a full `smart_interval`. A page that is not a base page, or whose length does not fit its
parameters, counts as refused. A SAS drive in standby is not detected: `skip_if_standby` applies to
SATA drives only, and `LOG SENSE` may spin up a drive that sleeps, so leave `smart` off for
controllers whose SAS drives are spun down. Grown defects and power-on hours are not read: they are not in these
pages. Direct SAS disks on an HBA stay out; the page codes and values follow the SCSI standard and
were not compared with a capture of a drive behind a 2208 controller.

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

**Drives that sleep.** Before any SMART read the receiver sends CHECK POWER MODE. A drive in standby is
left alone and shows `raid.smart_state` `standby`. A drive for which the controller returns no ATA status
registers is also left alone and shows `unavailable`, unless `smart_when_power_unknown` is true: then
SMART is read and the read can wake a spun-down drive. With the option off, no SMART command is sent to
such a drive, which is stricter than the `smart` receiver, which reads a drive whose power state it cannot tell.

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

### KSM — `receiver/ksmreceiver`

Reads the Kernel Samepage Merging counters under `ksm_path` (default `/sys/kernel/mm/ksm`) every
`collection_interval` (default `60s`) and emits them under the `linux.ksm.stats.*` names of the older
host metrics set. It opens seven files read-only, runs no command and needs no privilege beyond read
access. List it on PVE hypervisors only; it is in the Linux and static Linux edge builds and in no
other. See `test/configs/edge-ksm.yaml`.

| Metric | Type | Unit | File |
|---|---|---|---|
| `linux.ksm.stats.pages_shared` | gauge | `{page}` | `pages_shared` |
| `linux.ksm.stats.pages_sharing` | gauge | `{page}` | `pages_sharing` |
| `linux.ksm.stats.pages_unshared` | gauge | `{page}` | `pages_unshared` |
| `linux.ksm.stats.pages_volatile` | gauge | `{page}` | `pages_volatile` |
| `linux.ksm.stats.full_scans` | monotonic sum | `{scan}` | `full_scans` |
| `linux.ksm.stats.stable_node_chains` | gauge | `{node}` | `stable_node_chains` |
| `linux.ksm.stats.stable_node_dups` | gauge | `{node}` | `stable_node_dups` |

The two `stable_node_*` series are new; a kernel without either file gives no series for it. A
missing `ksm_path` gives no series and one info line until it exists; a file that cannot be read or
is not a number is skipped with one warning per file until it reads cleanly again. A `ksm_path` that
is not a directory, or a parent that cannot be read, gives one warning. When the directory exists and
no file in it can be read, the warning "None of the KSM files could be read" repeats every 60 scrapes.

`linux.ksm.stats.full_scans` is a monotonic cumulative sum, a counter: a Prometheus exporter may add
`_total` to its name. The older Metricbeat series was a gauge, so a dashboard that read it as a gauge
needs a `rate()` or `increase()`. The sum starts at the receiver start. It restarts when the counter
goes down, and when the file was missing or unreadable (or the directory was gone) between two
scrapes, so a KSM restart that climbs back above the old value inside one gap still restarts it.
A restart that finishes between two clean scrapes at a higher value cannot be told from a normal rise.

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
  `SetCollectorVersion` at startup. Applies to all three signal types. With
  `short_host_name: true` (default `false`) it also cuts `host.name` of each
  resource at the first dot, so `web01.example.net` becomes `web01`. A value that
  is an IP address, has no dot or starts with a dot stays as it is, and so does a
  `host.name` that is not a string. When it cuts the name it keeps the full
  original name in `host.fqdn`; a name that stays as it is gets no `host.fqdn`.
  `host.id` and other attributes are not touched. Put the processor after
  `resource_detection`.
  A sample is `test/configs/edge-short-host-name.yaml`.
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
