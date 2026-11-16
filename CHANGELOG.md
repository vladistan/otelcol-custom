# Changelog

All notable changes to otelcol-custom will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.16] - 2026-11-16

New Linux edge receivers for disk health (`smart`), MegaRAID controllers (`megaraid`) and Dell iDRAC (`idrac`), an `ipmi` receiver and the `pve` and `snmp` receivers in the scrape image, `ethtool` and `zfskstat` additions, and a read-only `megaraid-capture` tool for lab hosts. The Windows edge and the gateway gain no new component.

### Added

- **The scrape image includes the `pve` and `snmp` receivers.** One scrape pod can now poll Proxmox VE nodes and SNMP devices next to the cloud sources; the edge builds keep both. Run one `pve` instance per node and set `guests.node` on each: the guest scraper reads `/nodes/<guests.node>/...` only, and an empty `guests.node` takes the host name of the machine, which in a pod is the pod name and no PVE node. The node never comes from `base_urls`, and a request for another node's path is forwarded by the cluster member that answers it (PVE behaviour, not run against a cluster here). `netflow` stays edge only: it listens on a UDP port for exporters, and adding it costs another 428.0 KiB. `scripts/check-local-replaces.sh` now fails when the scrape manifest lacks the `pve` replace, the `pve` receiver or the `snmp` receiver. A sample is `test/configs/scrape-pve-snmp.yaml`; see `docs/scrape-collector.md`. In the local container build of the scrape manifest (CGO off, stripped; the recipe of the `ipmi` entry in 0.3.15), the two receivers add 491,520 bytes (480.0 KiB), from 156,278,946 to 156,770,466 bytes; with `netflow` as well it would be 157,208,738 bytes. Read these numbers as differences; the release size is in the table below.
- **`iscsi` receiver: per-LUN series.** With `luns` (default `true`) the receiver reports each LUN of each session, read from `/sys/class/scsi_device` and the block device of the path, with no external command: `hw.status` (`hw.type` `logical_disk`, one point for each `hw.state`), `hw.logical_disk.limit` in `By`, and `iscsi.lun.state` (an info gauge with the raw SCSI state, `iscsi.target`, `iscsi.portal`, `iscsi.lun`, `iscsi.device` and, when a device-mapper device sits on the path, `iscsi.holder`). `running` is `ok`; `blocked`, `quiesce` and `created` are `degraded`; `offline` and `transport-offline` are `failed`. A LUN that cannot be read or has no block device gets state `unknown`, no `hw.status` point and one warning for each run of failures. With multipath each path is reported under its own device and the `dm-N` device is not reported. `max_luns` (default 256, at most 4096, checked only with `luns` on) caps the LUNs of a scrape and logs one info line; a LUN is 7 points, so the default cap is 1,792 points. A device that vanishes while it is read gives no series and one info line; the first of several holders is named and the number is counted in `iscsi.holders`; an attribute read takes at most 4 KiB, and a read of the LUNs that does not return in 10 seconds gives one warning, keeps the session series and is not started twice. The receiver also opens `type` of a SCSI device and `size` of its block device; the session series are unchanged, and `luns: false` gives the 0.3.15 output. The receiver module now depends on `hwschema`. The receiver alone grows by 12,650 bytes (12.4 KiB): a probe binary (linux/amd64, CGO off, not stripped) that links only this receiver is 8,036,457 bytes without the LUN code and 8,049,107 bytes with it, and the new figure includes the `hwschema` package, which the edge builds already link; the edge build figure was not measured. See `docs/otel-metrics.md`.
- **`ethtool` receiver: `conntrack_allowance_available` and a statistics list.** When the driver reports `conntrack_allowance_available` (AWS Nitro instances, ENA driver 2.8.1 or later) the receiver emits the gauge `network.allowance.available` in `{connection}` with `network.interface.name` and `allowance` set to `conntrack`; a driver without it gives no point and one info line per interface. The new key `statistics` takes entries of `match` (a regular expression, unanchored: use `^` and `$`) and `type` (`sum` or `gauge`, required) and emits `network.interface.statistic` with the attributes `network.interface.name` and `statistic`; a name that several entries match is emitted once, under the first, and an entry that matches nothing logs one info line. `max_statistics` (default 200) caps the series of one scrape across all interfaces, drops the rest in sorted order and logs one warning while the count stays over the cap. Per-queue names multiply the series by the number of queues, so a pattern for them needs the cap in mind. With no `statistics` entry the receiver emits no such series, `network.allowance.exceeded` is unchanged, and the key `stats_regex` stays rejected. A pattern that does not compile, an entry without a type and a type other than `sum` or `gauge` fail at startup. See the README.
- **`zfskstat`: dataset filters, unlinks and transaction group metrics.** `dataset_include` and `dataset_exclude` list globs on `<pool>/<dataset>` (Go `path.Match` rules: `*` does not match `/`); an empty include list reads every dataset, exclude wins, and both run before the `max_datasets` cap (default 100). With `datasets` on, `zfs.dataset.unlinks` (a monotonic sum in `{unlink}`, from `nunlinks`) joins the two dataset I/O metrics, on Linux and FreeBSD. The new key `txg` (default `true`) emits three gauges per pool from the newest closed row of the `txgs` kstat: `zfs.pool.txg.dirty` and `zfs.pool.txg.written` in `By`, and `zfs.pool.txg.sync.time` in `s`. The kernel fills `txgs` only while the module parameter `zfs_txg_history` is above 0; a pool with no closed row or no `txgs` file gets no txg series and one info line, so a default config now logs one info line per such pool. With `datasets` and `txg` off the output is that of 0.3.15. The FreeBSD sysctl source reads `kstat.zfs.<pool>.misc.txgs` when the tree has it; this was not checked on a FreeBSD host.
- **`zfskstat` on FreeBSD: root dataset space.** The gauges `zfs.dataset.space.used` and `zfs.dataset.space.available` in `By`, with `zfs.pool` and `zfs.dataset` (the pool name), come from `getfsstat` and the `statfs` sizes of the mount of each pool's root dataset, with no external command. They are not the SIZE, ALLOC and FREE of `zpool list`, and pool fragmentation and per-vdev error counts are not reported: they need the libzfs interface, which the receiver does not use. A pool whose root dataset is not mounted gets no space series and one debug line; Linux emits none of these series.
- **`megaraid` receiver for MegaRAID controllers in the Linux edge builds.** The Linux and static Linux edge gain a receiver (own module, development stability) that reads LSI MegaRAID controllers (Dell PERC, Supermicro cards) through the driver's ioctl node `/dev/megaraid_sas_ioctl_node`, with no `megacli`, `storcli` or `smartctl`. It sends controller, logical drive, physical drive and battery state as `hw.status`, `hw.temperature`, `hw.errors`, `hw.physical_disk.size`, `hw.logical_disk.limit`, `hw.battery.charge` and `hw.battery.time_left`, plus `raid.controller.info`, `raid.controller.reachable`, `raid.logical_disk.level`, `.strip_size` and `.cache_policy`, `raid.progress`, `raid.battery.cycles` and `raid.battery.next_learn_time`, and the controller event log as logs (`events.enabled`, default true, with the last position in `file_storage`). `smart: true` (off by default) reads SATA drives behind the controller with the decoders of the `smart` receiver, one drive at a time and at most once per `smart_interval`. Every frame passes one function that allows 19 read-only opcodes and a short list of CDBs; SMART is never switched on and nothing on the controller changes. It needs file access to the node and `CAP_SYS_ADMIN` (a non-root user needs both), and other tools that use the node queue behind a scrape. A call that times out stops further calls to that controller until the kernel returns, so a stuck controller costs one buffer set; a failed event read is logged once per run of failures and counted in `otelcol_megaraid_event_read_errors`, and a failed read of the saved position never replays or drops the backlog. SMART leaves a drive alone when CHECK POWER MODE shows standby or returns no registers. The 2208 and 3108 are the declared generations; the tests decode scrubbed captures of a real 2208 and a real 3108, and the degraded and failed states rest on synthetic buffers. The Windows edge, the FreeBSD edge, the gateway and the scrape collector do not include it. The static Linux edge grows by 188,416 bytes (184.0 KiB), from 195,854,498 to 196,042,914 bytes in the release container build of the same manifest with and without the receiver. See `docs/otel-metrics.md` and `test/configs/edge-megaraid.yaml`.

### Fixed

- **The container builds of the edge binaries set `GOFLAGS=-buildvcs=false`.** The OCB compile step inside the build container asked git for version data and failed on a clean runner, where the checkout is not owned by the container user. The Linux, static Linux, Windows and FreeBSD recipes now pass the setting to the container.

### Binary size

Container builds of this tree with Go 1.26.8, the version of the release workflow, compared with the published 0.3.15 assets. The scrape row compares two builds made here with Go 1.26.8, the 0.3.15 tree and this tree, because the 0.3.15 scrape image is not published as a binary.

| Asset | MiB | Bytes | Change from 0.3.15 |
|---|---|---|---|
| `otelcol-edge-0.3.16-linux-amd64` | 187.2 | 196251810 | +512.0 KiB (+0.27%) |
| `otelcol-edge-0.3.16-linux-amd64-gpu` | 188.4 | 197596616 | +515.8 KiB (+0.27%) |
| `otelcol-edge-0.3.16-windows-amd64.exe` | 130.6 | 136968704 | +2.5 KiB (+0.00%) |
| `otelcol-edge-0.3.16-freebsd-amd64` | 168.4 | 176554156 | +20.0 KiB (+0.01%) |
| `otelcol-gateway-0.3.16-linux-amd64` | 208.1 | 218247330 | +28.0 KiB (+0.01%) |
| `otelcol-scrape` | 149.6 | 156917922 | +2.4 MiB (+1.62%) |

The gateway manifest and sources did not change: a Go 1.26.8 build of the 0.3.15 tree gives the same 218247330 bytes as this tree, 28.0 KiB above the published asset, which was built on another machine. A probe that links one receiver package alone (linux/amd64, CGO off, not stripped) and sums the sizes of the allocated ELF sections gives a finer figure: the `ethtool` additions add 4,087 bytes (3.99 KiB) to 5,468,004, and the `zfskstat` additions on Linux add about 2.1 KiB (the FreeBSD code is not in a Linux build).

## [0.3.15] - 2026-11-16

A FreeBSD edge binary, new Linux edge receivers (`pve`, `snmp`, `netflow`, `hwmon`, `iscsi`, pod discovery with six application receivers), a `cloudwatchmetrics` receiver and per-series aggregation in the scrape collector, an edge container image, and `journald` processor options that replace a `transform` block.


- **`smart` receiver for NVMe and SATA disk health in the Linux edge builds.** The Linux and static Linux edge gain a receiver (own module, development stability) that reads disk health in the collector process with `SG_IO` and NVMe admin ioctls, one disk at a time, with no `smartctl` and no cgo. It emits only `hw.*` names: `hw.status`, `hw.temperature`, `hw.errors`, `hw.physical_disk.endurance_utilization`, `hw.physical_disk.size`, `hw.physical_disk.power_on_time`, `hw.physical_disk.power_cycles`, `hw.physical_disk.io` and `hw.physical_disk.smart` with the added attribute `hw.physical_disk.smart_value_type` (`normalized`, `worst`, `threshold`, `raw`). The default list holds 20 SATA attributes with the normalized and raw values, about 40 `hw.physical_disk.smart` series for a disk; `attributes: all` and the other value types are opt-in. The commands are on a read-only allow list, SMART is never switched on, and startup sends nothing to a disk. Reading needs root, or a user that can read the device files (the `disk` group for SATA, an ACL or udev rule for `/dev/nvmeN`) and holds `CAP_SYS_ADMIN` for NVMe and `CAP_SYS_RAWIO` for SATA, and a unit file that leaves `PrivateDevices` unset or allows `block-sd` and `char-nvme`; a refused open logs a request for read access and a refused command logs the missing capability, and both count as reason `permission`. A command that never returns skips the disk for the interval, and the disk is not opened again until that command returns. Every command passes the allow list only with its exact registers, data length and log length. Disks behind a RAID controller, SAS disks, USB bridges that refuse the command and virtual disks are skipped and counted in `otelcol_smart_devices_skipped`. The decoders sit in a module of their own behind a transport interface, and the `hw.*` names in a shared schema module. The Windows edge, the FreeBSD edge, the gateway and the scrape collector do not include it. The static Linux edge grows by 126,976 bytes (124.0 KiB), from 195,727,522 to 195,854,498 bytes in the release container build of the same manifest with and without the receiver. See `docs/smart.md`.
- **`idrac` receiver in the Linux edge builds.** A new receiver (own module, development stability) reads a Dell iDRAC through the Redfish API, in process and with no external command, and reports health, temperatures, fans, voltages, power, drives, memory and processors with the `hw.*` names of the OpenTelemetry semantic conventions, built through the shared `hwschema` module: `hw.status` for the system, chassis, fans, power supplies, drives, DIMMs, processors and the iDRAC (one point per `hw.state`), `hw.temperature` and `hw.temperature.limit`, `hw.fan.speed` (RPM) and `hw.fan.speed_ratio` (percent), `hw.voltage`, `hw.host.power`, `hw.power_supply.usage`, `.limit` and `.utilization`, `hw.physical_disk.size` and `.endurance_utilization`, `hw.memory.size`, and `hw.bmc.up` (with a `reason` when 0) and `hw.bmc.scrape.duration` per target. A target takes `name` (the `host.name` of the resource), `address` (https), `username`, `password` (`${env:...}` or `${file:...}`, stored as an opaque string and written in no log), `ca_file`, `insecure_skip_verify` (default false), `resources` (default all), `request_timeout` (10 s) and `scrape_timeout` (30 s); the receiver takes `collection_interval` (60 s). It logs in once with a session token, sends `X-Auth-Token` on every request (no `Authorization` header), logs in again once after a 401, and deletes the session at shutdown. Certificate verification is on and never retried without it: a certificate that does not verify gives `hw.bmc.up` 0 with reason `tls`. A path the iDRAC answers 404 for is marked absent with one info line and asked again after 6 hours. The legacy `Thermal` and `Power` are read first (they carry the limits, voltages and host power) and `ThermalSubsystem` and `PowerSubsystem` are the fallback; a BMC that answers 400 or 501 to `$expand` is read member by member for 6 hours; one drive or member that answers an error is skipped with one warning and counted by `otelcol_hw_bmc_skipped_reads`, while a timeout, login or TLS failure ends the scrape; `max_concurrent` (8) limits the targets read at once. After a failed login the iDRAC is not contacted for 15 minutes, then 30, doubling to 6 hours, and after three in a row not until restart; `otelcol_hw_bmc_auth_failures` counts them. The static Linux edge grows by about 120 KiB (195,727,522 to 195,850,402 bytes, measured in 4 KiB pages), about 0.06%, in the container build; the go.mod graph changes no core, contrib or SDK version. Use a read-only iDRAC account (the Login and ReadOnly privileges). Not in the Windows edge, the FreeBSD edge, the gateway or the scrape collector. `test/configs/edge-idrac.yaml` is a sample. See the README and `docs/otel-metrics.md`.
- **`ipmi` receiver in the scrape image.** A new receiver (own module, development stability) reads the sensors of baseboard management controllers over IPMI with go-ipmi v0.9.2 (pinned in the scrape manifest; it is pre-1.0), in process and with no external command. It reports `hw.temperature` and `hw.temperature.limit` (the SDR upper critical and non-critical thresholds), `hw.fan.speed`, `hw.voltage`, `hw.status` for power supplies and chassis intrusion, and optionally `hw.host.power` from DCMI, plus `hw.bmc.up` (with a `reason` when 0) and `hw.bmc.scrape.duration` per BMC; the metric names and units live in the new `hwschema` module, which the `idrac` receiver will share. One goroutine reads each BMC, at most `max_concurrent` (8) at once, with a deadline per exchange (3 s, one retry) and per scrape (20 s) and a start offset from a hash of the BMC name. Each scrape opens one session, reads and closes it. The SDR is cached and read again when its timestamp changes and every 6 hours. The cipher suite is set per BMC and never probed. An authentication failure (a wrong password, a wrong user name, or a role the account may not have; go-ipmi reports these as text and the receiver matches the text, or reads the completion code of the privilege step; a timeout or refused connection in any step never counts) stops that BMC for 15 minutes, then 30, doubling to 6 hours, and after three in a row until restart; `otelcol_hw_bmc_auth_failures` counts them. The sensor session asks for the `user` privilege level by default and a higher level logs a warning; DCMI power needs an administrator session on the lab boards, so it is off by default and read once per `power_interval` (5 minutes) on a session that is closed at once. A failed scrape sends only `hw.bmc.up` 0 and the duration. `test/configs/scrape-ipmi.yaml` runs the receiver alone, `test/configs/scrape-ipmi-deployment.yaml` holds a Deployment of the scrape image with the credentials from a Secret filled by an ExternalSecret and a network policy with UDP 623 egress to one subnet, and `just check-ipmi-deployment` guards that sample. The scrape binary grows by about 1.9 MiB (154,284,194 to 156,274,850 bytes, measured in 4 KiB pages), about 1.3%, in the container build; the go.mod graph changes no core, contrib or AWS SDK version. See `docs/scrape-collector.md`.
- **Per-series aggregation and a project list in the scrape collector's `googlecloudmonitoring` receiver.** The scrape image now builds a local copy of the contrib receiver (v0.164.1) with three new keys. `aggregation.per_series_aligners` (`ALIGN_MIN`, `ALIGN_MAX`, `ALIGN_MEAN`, `ALIGN_SUM`, `ALIGN_COUNT`, `ALIGN_PERCENTILE_50`) with `aggregation.alignment_period` sends one aligned `ListTimeSeries` request per project, metric type and aligner and emits each result as a gauge with the attribute `stat` (`min`, `max`, `mean`, `sum`, `count`, `p50`). With `aggregation` set, `collection_interval` can exceed 23 h, so a 24 h window gives daily min, max, mean, sum, count and median per series. `projects` lists more projects for one instance, so a daily tier needs one instance, not one per project. `ALIGN_PERCENTILE_50` on a metric that is not a distribution sends no request, emits no point and logs one INFO line per metric name in each run. A request Cloud Monitoring rejects is logged as a WARN and the others go on, and a project whose metric descriptors cannot be listed at start is skipped with a WARN instead of stopping the collector (start fails only when no project can be listed). At least one of `project_id` and `projects` is required, and `aggregation.alignment_period` cannot be longer than `collection_interval`. Without these keys the receiver behaves as before, except that a `collection_interval` above 23 h without `aggregation` is now refused at startup instead of being cut to 23 h. See `docs/scrape-collector.md`.
- **`cloudwatchmetrics` receiver in the scrape image.** CloudWatch metrics now come from a new receiver (own module, development stability) instead of the `metrics` block of `awscloudwatch`, which stays for CloudWatch logs. One instance reads every account, region and tier of its config: an account may set `role_arn` and `external_id` and the receiver assumes the role once with the pod's own credentials (no access keys or session token are accepted). A tier sets `collection_interval`, `period`, `delay`, an explicit `stats` list and a series `limit`, and lists metric names per namespace; an empty list reads the whole namespace. Each name is discovered with its own `ListMetrics` call, each series keeps its own dimensions, and `GetMetricData` runs in calls of at most 500 queries. Each statistic is a gauge point named `amazonaws.com/<Namespace>/<MetricName>` with the attribute `stat`. Three gauges, `otelcol_aws_cloudwatch_series_discovered`, `otelcol_aws_cloudwatch_series_limit` and `otelcol_aws_cloudwatch_discovery_truncated`, show series found against the limit, and one warning marks each truncated run. The end of the last window is kept in `file_storage`; after a stop the receiver reads the missed windows oldest first, up to `max_catchup` (72h), and a restart inside a window does not read it again. A daily tier reads the previous complete UTC day. A percentile statistic that a metric does not support gives no point and one info line per metric name per run. The reference scrape config uses it for an operational tier (Average and Maximum every 15 minutes) and a daily tier (Average, Minimum, Maximum, Sum, SampleCount and p50). A config that lists `awscloudwatch` with a `metrics` block needs moving to `cloudwatchmetrics`; `GetMetricData` is billed per metric requested, so check the CloudWatch usage after the first day. A config is rejected at startup when a tier's `period` is not 1, 5, 10 or 30 seconds or a multiple of 60 seconds, when `collection_interval` is not a multiple of `period` (windows would overlap), or when a region, statistic or metric name is listed twice. An account's role is assumed once and shared by its regions, but a region whose STS endpoint fails no longer blocks the account's other regions. See `docs/scrape-collector.md`.
- **FreeBSD edge build.** The release carries a new asset, `otelcol-edge-VERSION-freebsd-amd64`: a static `freebsd/amd64` executable built with CGO off, from the manifest `builder-edge-freebsd.yaml`. It is the 0.3.14 static Linux edge without `journald`, `psi`, `ethtool`, `docker_stats` and `k8s_attributes`, and it does not carry the `pve`, `snmp`, `netflow`, `iscsi`, pod discovery and application receivers of this release; it does carry `hwmon`, which reads temperatures through sysctl. `journald`, `psi` and `ethtool` do not run on FreeBSD; the other two have nothing to read there. The asset is listed in `SHA256SUMS` and on the download page. Release checks read its checksum, build settings and ELF header and do not start it, so run `otelcol-edge validate` on the host first. `docs/freebsd-edge.md` names the install path and the rc.d service, and `test/configs/truenas-core-edge.yaml` is a sample for a TrueNAS CORE host. A guard, `scripts/check-edge-freebsd-manifest.sh`, fails when the manifest differs from the static edge by anything other than that list, so a component added to the static edge needs a FreeBSD decision. The FreeBSD asset is public, so a failure of its build, its `GOOS=freebsd go vet` step or its check stops the whole release before any asset is staged; the log ends with a `FreeBSD edge:` line. Its size is in the table below.
- **`zfskstat` reads ZFS through sysctl on FreeBSD.** The new `source` key takes `auto` (the default; sysctl on FreeBSD, kstat files elsewhere), `proc` or `sysctl`. On FreeBSD the receiver walks the `kstat.zfs` sysctl tree in process, with no external command, and emits `zfs.arc.size`, `zfs.arc.hits`, `zfs.arc.misses` and `zfs.pool.state` with the Linux names and attributes. FreeBSD 13.1 has no per-pool I/O kstat, so `zfs.pool.io.*` is absent there; use the `hostmetrics` `disk` scraper for disk I/O. On a host without ZFS the receiver logs one info line and sends nothing. Linux output is unchanged when `source` is unset.
- **Per-dataset I/O in `zfskstat`.** The new `datasets` key (default `false`) adds `zfs.dataset.io.bytes` and `zfs.dataset.io.operations`, by `zfs.pool`, `zfs.dataset` and `direction`, from the `objset-*` kstats on Linux and FreeBSD, for at most `max_datasets` datasets (default 100, in sorted order; `0` is no cap) with one warning when it drops some. A dataset name with a space is kept on both sources. The cumulative sums start at host boot; a dataset or pool counter that goes down, as after a remount or a pool import, restarts at the scrape time. `kstat_path` is no longer required with `source: sysctl`.
- **Edge container image `ghcr.io/vladistan/otelcol-edge`.** A runtime image of the static Linux edge binary (linux/amd64, alpine base, non-root user 10001, entrypoint `/otelcol-edge`), so the edge collector can run as a Kubernetes DaemonSet. The image carries the same bytes as the `otelcol-edge-<version>-linux-amd64` release asset; `just docker-build-edge-ghcr` checks them against `SHA256SUMS`. New recipes `docker-build-edge-ghcr`, `verify-ghcr-edge-image` and `docker-push-edge-ghcr`; the release workflow publishes it when the repository variable `PUBLISH_EDGE` is `true`. See `docs/public-release.md`.
- **`pve` receiver in the Linux edge builds.** A new receiver (own module, development stability) reads a Proxmox VE cluster through its HTTP API, with no external command. The `resources` scraper reads `/cluster/resources`, `/cluster/status` and `/version` once per interval (default 60 s) and reports every node, guest and storage as `pve.resource.up`, `.cpu.utilization`, `.cpu.limit`, `.memory.usage`, `.memory.limit`, `.disk.usage`, `.disk.limit`, `.network.io`, `.disk.io`, `.uptime` and `pve.storage.shared`, plus the info gauges `pve.guest.info`, `pve.storage.info`, `pve.node.info`, `pve.version.info` and `pve.resource.ha.state`; enable it on one node per cluster; entries of type `sdn` and `pool` are skipped. The `guests` scraper asks the QEMU guest agent of each running guest on its own node, every 15 minutes with a start offset and one request at a time, and reports `pve.guest.filesystem.usage` and `.size` as metrics and the OS, host name and addresses as change-event logs; a guest whose agent is not running is recorded once as unavailable and checked again at a growing delay of up to 24 hours. Each scraper has its own API token (`PVEAuditor` on `/` for `resources`; `VM.Audit` plus `VM.Monitor` on PVE 8 or `VM.GuestAgent.Audit` on PVE 9, on `/vms`, for `guests`); the token secret never appears in a log line, an error or a config dump. `base_urls` lists several addresses: a connect or TLS failure moves on to the next one, and for `resources` so does a 5xx answer or a request timeout; certificates are verified. `Start` makes no network call, so a PVE API that is down does not stop the collector. Four extra calls (`subscription`, `replication`, `backup_info`, `guest_config`) are off by default. It is not in the Windows edge, the gateway or the scrape collector. The static Linux edge grows by 94,208 bytes (173,682,848 to 173,777,056). See the README and `docs/otel-metrics.md`.
- **`snmp` and `netflow` receivers in the Linux edge builds.** The contrib receivers (v0.164.1, both alpha) are in the Linux and static Linux edge builds and not in the Windows edge, the gateway or the scrape collector. `snmp` polls one device per instance by OID over SNMP v1, v2c or v3 (SHA and AES among the options) and turns numeric columns into metrics; string columns, such as interface, system and LUN names, become attribute values. `netflow` turns NetFlow v5, v9, IPFIX and sFlow v5 records into logs, one record per flow. Sample configs `test/configs/edge-snmp.yaml` (a v3 `auth_priv` instance, a QNAP instance with numeric sensors and iSCSI target metrics, and an LLDP neighbour instance) and `test/configs/edge-netflow.yaml` are included, and `scripts/check-snmp-secrets.sh` fails a sample config with a literal `community`, `auth_password` or `privacy_password`. The QNAP iSCSI OIDs and the LLDP index join are unverified until a device is polled; see `docs/snmp-netflow.md`, which also gives the flow volume method, the fields to delete before export and the advice to bind one address, because NetFlow has no authentication. The two receivers together add 806,912 bytes to the static Linux edge (173,682,848 to 174,489,760) and two new modules, `gosnmp` and `goflow2`; no core, contrib or SDK version changes.

### Changed

- **`netflow` is an optional receiver.** A flow port that cannot be bound, for example because the address does not exist on the host or the port is taken, logs one WARN, counts `otelcol_optional_receiver_skipped` and retries the bind in the background; the collector keeps running. The list of optional receivers is now `docker_stats`, `tcp_check` and `netflow`.
- **Pod discovery in the Linux edge builds.** The Linux and static Linux edge gain the contrib `k8s_observer` extension and `receiver_creator` receiver (v0.164.1), for a DaemonSet: the observer lists the pods of its own node, and the creator starts a receiver for each pod that a rule in the config matches. The reference config scrapes pods that carry `prometheus.io/scrape: "true"` with the `prometheus` receiver and leaves out the collector's own pod; `k8s_attributes` after the creator adds pod labels, associated on `k8s.pod.uid`. Annotation discovery stays off, and a guard fails a sample config that turns it on. The edge also compiles in the contrib `redis`, `postgresql`, `mysql`, `mongodb`, `rabbitmq` and `nginx` receivers for rules on those pods, with credentials from environment variables or a mounted file; `memcached`, `kafka_metrics` and `prometheus_simple` are not in the build. A receiver the creator starts fails soft: a bad template logs one Error when a matching pod appears and the collector keeps running. Each matching pod gets its own receiver, and the receiver self-metrics gain a `receiver` label value for each pod and each restart. The gateway, the scrape collector and the Windows edge are unchanged. The size cost on the static Linux edge is in the table below. See `docs/k8s-discovery.md`.

  | Step on the static Linux edge | MiB added | Bytes added |
  |---|---|---|
  | `k8s_observer` and `receiver_creator` | 0.23 | 241,664 |
  | `redis` | 8.09 | 8,486,912 |
  | `postgresql` | 1.70 | 1,781,760 |
  | `mysql` | 1.21 | 1,269,760 |
  | `mongodb` | 3.80 | 3,985,408 |
  | `rabbitmq` | 0.46 | 487,424 |
  | `nginx` | 0.05 | 49,152 |
  | All eight, host Go builds (the rows above, each added to the ones before) | 15.55 | 16,302,080 |
  | All eight, release container builds: 178,106,530 to 194,687,138 bytes (169.86 to 185.67 MiB) | 15.81 | 16,580,608 |

  The rows for single steps come from host Go builds, which compare with each other and not byte for byte with the release container builds; only the last row is a release container figure.
- **`hwmon` receiver in the Linux edge builds.** The new receiver (own module, development stability) reports temperature, fan and voltage sensors with the `hw.*` names of the OpenTelemetry semantic conventions: `hw.temperature` (a double gauge in `Cel`, so the millidegree precision of sysfs is kept), `hw.temperature.limit` (attribute `hw.limit_type`: `max`, `critical`, `low.degraded`, `low.critical`), `hw.fan.speed` (`rpm`) and `hw.voltage` (`V`), each with `hw.id`, `hw.name` and `hw.parent`. It reads `/sys/class/hwmon` and `/sys/class/thermal` in process, with no external command, no cgo and no capability. Keys: `collection_interval` (60 s), `hwmon_path`, `include` and `exclude` (globs on `chip:label`), `limits`, `fans`, `voltages`, `thermal_zones` (all on by default) and `cpu_cores` (off by default, because per-core temperatures are most of the series). `hw.id` is the chip name and the file stem, such as `coretemp/temp3`; two chips of one name, such as two NVMe drives, get the hardware address of their device after an `@` (the PCI address of an NVMe drive, which survives renumbering at the next boot), so adding a second chip of a name changes the `hw.id` of the first. A limit of -273.15 °C or below, which NVMe drives report for a limit they do not have, gives no series. A thermal zone that a hwmon chip of the same name already reports is read once. A sensor that fails to read is skipped with a debug line; a hwmon directory that cannot be listed is a warning, not "no sensors". A host with no sensors gets one info line and no metrics. The source builds for FreeBSD too and reads `dev.cpu.<N>.temperature` and the ACPI thermal zones with their trip points through sysctl; the FreeBSD binary is not published yet. Not in the Windows edge, the gateway or the scrape collector. See the README and `docs/otel-metrics.md`.
- **`iscsi` receiver in the Linux edge builds.** The new receiver (own module, development stability) reports iSCSI initiator sessions from `/sys/class/iscsi_session` and `/sys/class/iscsi_connection`: `iscsi.session.state` (an info gauge of 1 with `state`, `iscsi.target`, `iscsi.portal` and `iscsi.tpgt`), `iscsi.session.count` and `iscsi.connection.state`. It opens only the files `targetname`, `tpgt`, `state`, `persistent_address` and `persistent_port` and never a credential file. Keys: `collection_interval` (60 s) and `sysfs_path`. A host with no sessions gets one info line and no metrics. Creating the receiver fails off Linux. See the README and `docs/otel-metrics.md`.
- **Docs page for Docker hosts on the journald log driver.** `docs/docker-multiline.md` states that a stack trace arrives as one record, how `logrecombine` groups lines per container and how a container is left out, what `containername` needs (`container.name` on the resource first, no later rule that sets `service.name`), why `process.command` stays `dockerd`, and the `docker_stats` settings with an empty `api_version`. On Docker 24.0.9 (API 1.43) an empty `api_version` starts and exports container metrics, and the default `1.44` fails with `client version 1.44 is too new`, after which the collector stays up and retries the start.
- **`journald` processor: `map_extended`, `skip_when_resource_has` and `drop_unmapped_underscore`.** All three are off by default, so a config without them gives the output of 0.3.14. `map_extended` also maps `_EXE`, `_CMDLINE`, `_UID`, `_GID` and `_SYSTEMD_CGROUP` to the resource attributes `process.executable.path`, `process.command_line`, `user.id`, `user.group.id` and `container.id`, and `_BOOT_ID`, `_SYSTEMD_SLICE`, `_SYSTEMD_INVOCATION_ID`, `_TRANSPORT`, `SYSLOG_FACILITY`, `SYSLOG_IDENTIFIER` and `SYSLOG_PID` to the log attributes `boot.id`, `systemd.slice`, `systemd.invocation_id`, `log.source`, `syslog.facility.code`, `syslog.appname` and `syslog.procid`. It also maps `_RUNTIME_SCOPE` to the resource attribute `container.runtime`, and `_SELINUX_CONTEXT` and `_CAP_EFFECTIVE` to the log attributes `selinux.context` and `process.capabilities.effective`. The six resource attributes are set only when the resource lacks them, an empty string counting as missing. A map body that stays a map loses `PRIORITY`, `SYSLOG_FACILITY`, `SYSLOG_IDENTIFIER` and `SYSLOG_PID` once they are read. Each other unmapped key that starts with `_` moves into the log attribute `journald.unknown_fields`, which is absent when none is left, and `_SOURCE_REALTIME_TIMESTAMP`, `__CURSOR`, `__MONOTONIC_TIMESTAMP` and `_STREAM_ID` are dropped. A `transform` block that maps the same fields differs in five ways: it sets `container.runtime`, `selinux.context` and `process.capabilities.effective` only on records without `MESSAGE`; it writes `journald.unknown_fields` as an empty map on those records, where the processor writes the leftover keys on every record that has any, so most rows gain the attribute (four leftover keys add 146 bytes to a 532 byte record); it overwrites the six resource attributes the processor sets only when the resource lacks them; it sets `host.name`, `process.pid`, `process.command` and `service.name` on every record where the processor keeps a value the resource has; and it leaves the severity unset for `PRIORITY` 5 where the processor gives `INFO`. The processor logs a warning at start when `map_extended` runs with `split_resources` off. With `split_resources` on, each record's resource carries that record's own values, which can make many resources per batch; there is no cap. `skip_when_resource_has` lists resource attributes, for example `container.name`; a resource that carries one gets no `process.pid`, `process.command`, `service.name` or `container.id` from the journal. `drop_unmapped_underscore` removes the keys that start with `_` from a map body that has no `MESSAGE`. The processor records the histogram `otelcol_journald_resources_per_batch`. Together the keys let `journald` replace a `transform` block that maps the same fields, about four to five times faster per record on the benchmark. The extras add 4,176 bytes (4.1 KiB) to the gateway binary, from 218,039,840 to 218,044,016 bytes in a host Go build. See the README.

### Fixed

- **`zfskstat` reported no pool metrics on OpenZFS 2.1 and later.** The receiver read the pool file `io` first and skipped the whole pool when it was missing, so a 2.1 host (Proxmox VE 8, Debian 12, Ubuntu 22.04 and later) sent only `zfs.arc.*`: no `zfs.pool.state` and no per-dataset metrics. OpenZFS 2.1 has no pool `io` kstat, so a pool without the file now keeps its `zfs.pool.state` and, with `datasets`, its dataset metrics, and logs no warning. OpenZFS 2.1 and later have no pool-level I/O counters, so `zfs.pool.io.*` appears only where the legacy `io` file exists (OpenZFS 2.0 and older), as it was already absent on FreeBSD; transaction group metrics for these hosts come in a later release. An `io` file that exists but cannot be parsed still skips the pool with one warning. See `docs/otel-metrics.md`.

### Binary size

Release container builds of this tree against the 0.3.14 release assets, with the same recipe; the scrape row compares two builds made here.

| Asset | MiB | Bytes | Change from 0.3.14 |
|---|---|---|---|
| `otelcol-edge-0.3.15-linux-amd64` | 186.7 | 195727522 | +16.8 MiB (+9.89%) |
| `otelcol-edge-0.3.15-linux-amd64-gpu` | 187.9 | 197072488 | +16.8 MiB (+9.82%) |
| `otelcol-edge-0.3.15-windows-amd64.exe` | 130.6 | 136968704 | +0 B (0.00%) |
| `otelcol-edge-0.3.15-freebsd-amd64` | 168.4 | 176533676 | new asset |
| `otelcol-gateway-0.3.15-linux-amd64` | 202.2 | 212066464 | +8.0 KiB (+0.00%) |
| `otelcol-scrape` | 144.4 | 151429280 | +124.0 KiB (+0.08%) |

## [0.3.14] - 2026-11-15

The `docker_stats`, `ethtool` and `zfskstat` receivers and the `k8s_attributes` processor in the Linux edge builds, per-cgroup pressure in the `psi` receiver, metrics from the `openvpnstatus` receiver, and a `logrecombine` fix for Docker container logs. The Windows edge, the gateway and the scrape image gain no new component.

### Added

- **`docker_stats` receiver in the Linux edge builds.** The contrib receiver (v0.164.1) reads container CPU, memory, block I/O and network statistics from the Docker socket, so Docker hosts report each container's limits: `container.memory.usage.limit` (on by default) and `container.cpu.limit` (off by default; the sample config in `test/configs/local-edge.yaml` turns it on). It is in the Linux and static Linux edge builds and not in the Windows edge, the gateway or the scrape collector. The collector must run as root or in the `docker` group to read the socket; without access the receiver logs the error and the collector keeps running. A container with no memory limit reports the host's memory. Set `api_version: ""` in the receiver block: its built-in default is `1.44`, which a Docker daemon whose API stops lower (Docker 24 answers up to `1.43`) rejects, so the receiver fails to start and the collector keeps restarting; empty lets the client negotiate the version. `just check-docker-stats-api-version` fails when a sample config has a `docker_stats` block without it. See the README.
- **Per-cgroup pressure in the `psi` receiver, and `k8s_attributes` in the Linux edge builds.** A new `cgroup` block reads `cpu.pressure`, `memory.pressure` and `io.pressure` of single cgroups on a cgroup v2 host and emits `cgroup.pressure.<cpu|memory|io>.<some|full>.<avg10|avg60|avg300|total>`. It is off by default, a config without it gives the same output as before, and there is no default `include`: a config lists the cgroups it wants as doublestar globs, with `max_depth` (4), `level` (`pod`), `values` (`avg10` and `total`) and `max_cgroups` (500; the count left out is `otelcol_psi_cgroups_skipped`). For Kubernetes paths the resource has `k8s.pod.uid` and `k8s.pod.qos` and the data point has `container.id`. The contrib `k8s_attributes` processor (v1.3.1) is now in the Linux and static Linux edge builds, so those series can carry the pod name and namespace; `test/configs/edge-daemonset.yaml` is a DaemonSet config that does it. It needs the host cgroup tree mounted read only, which some managed clusters do not allow. The Linux edge grows by 570,432 bytes (178,020,584 to 178,591,016) and the static Linux edge by 573,440 bytes (176,677,026 to 177,250,466), about 0.32% each. See `docs/otel-metrics.md`.
- **`ethtool` receiver in the Linux edge builds.** AWS ENA network interfaces count the packets the instance dropped or queued because it exceeded a network allowance. The new `ethtool` receiver reads the five allowance counters in the collector process with the `SIOCETHTOOL` ioctl, with no external command and no cgo, and reports them as the cumulative sum `network.allowance.exceeded` (unit `{packet}`) with the attributes `network.interface.name` and `allowance` (`bandwidth_in`, `bandwidth_out`, `pps`, `conntrack`, `linklocal`). `drivers` (default `ena`) and `interfaces` select the interfaces; interfaces that do not support statistics are skipped. The start time of each point is the host boot time. In a pod it sees only the pod's own interfaces, so it needs `hostNetwork`. It is in the Linux and static Linux edge builds only. See the README.
- **ZFS pool and ARC metrics in the Linux edge builds.** The new `zfskstat` receiver (own module, alpha) reads the OpenZFS kstat files under `/proc/spl/kstat/zfs` in process, with no external command. Per pool it emits I/O bytes and operations by direction, the time the wait and run queues were busy in seconds (`zfs.pool.io.wait.busy.time`, `zfs.pool.io.run.busy.time`; these are queue-busy time like `iostat` utilisation, not latency), queue lengths and the pool state; with `arcstats` on (the default) it also emits the ARC size, hits and misses. Pools are found from the directories under `kstat_path` and can be limited with `pools` and `exclude_pools`. The `io` file is read by field name, so a column added by a newer ZFS release changes nothing. A host without ZFS gets one info line and no metrics, and the receiver looks again at each interval. A pool whose `io` file is empty, short or not numeric is skipped with one warning. The receiver is in the Linux and static Linux edge builds only. See the README and `docs/otel-metrics.md`.
- **Optional receivers no longer stop the collector.** The `docker_stats` and `tcp_check` receivers are optional: if one fails to start, for example because the Docker socket is missing or the API version is refused, the collector logs a WARN naming the receiver and the error, counts it in the `otelcol_optional_receiver_skipped` metric (attribute `receiver`, 0 from the start) and keeps running. It retries the start in the background after 30 s, doubling up to every 5 minutes, logs a WARN on the first failure and every tenth, logs an INFO and reports the status OK when a start succeeds, and reports `RecoverableError` while it fails. Every other receiver, `journald` and `hostmetrics` included, still stops the collector when it fails to start. The list is fixed in code. A configuration that cannot build the receiver at all is still rejected at startup. The counter needs the collector's own metrics enabled, and Sentry does not see the WARN. See the README.

### Changed

- **`openvpnstatus` emits metrics, and only the client IP.** The 0.3.13 receiver emitted one log record per client with the address and port. In a metrics pipeline it now emits `openvpn.client.bytes_received` and `openvpn.client.bytes_sent` (cumulative monotonic sums in bytes, starting when the session started) and `openvpn.client.connected_since` (a gauge of Unix seconds), each with `common_name` and `real_address`. `real_address` is now the IP address without the port, in the metrics and in the log records; an IPv4-mapped IPv6 address is written as IPv4. A receiver in a logs pipeline keeps emitting log records, so a 0.3.13 config works unchanged. A new `output` key (`metrics`, `logs` or `both`) restricts the receiver, and a pipeline of a signal it leaves out fails at start; leave it unset unless that is wanted, since the 0.3.13 binary does not know the key. Two sessions of one name from one IP address share one series. Use `status-version 2` or `3` on the OpenVPN server for metrics; version 1 times are read in the collector's time zone. See `docs/openvpnstatus.md`.

### Fixed

- **`fswatch` notes corrected after a Windows host run.** A file deleted and created again under the same name within about 15 seconds keeps its creation time on NTFS and is not reported again; the earlier text said the live watch reports it. A file moved from one directory of the tree to another is reported as new; the earlier text said moves inside the tree were ignored. The receiver now counts as run on a Windows 11 host with a local NTFS volume. Network shares, ReFS and volume removal are still untested.
- **`logrecombine` no longer merges log lines of different Docker containers.** The processor grouped journald entries by `_PID`, and for Docker's journald driver that is the daemon's PID, the same for every container. A line without a leading timestamp that arrived within `gap_timeout` of another container's line (or of a daemon line) was appended to that record, so the later container's lines showed under the wrong container, or with no container, and their own resource and priority were lost. A new option, `container_identifier` (default `body.CONTAINER_ID`), groups an entry that has that field by container. Entries without it are grouped by `source_identifier` as before, so a daemon line never joins a container's lines. Setting `container_identifier: ""` restores the old grouping. A config that does not mention the key gets the new default. See the README.
- **The `journald` processor no longer replaces resource attributes that are already set.** With `set_resource` on, it copied `_HOSTNAME`, `_MACHINE_ID`, `_PID`, `_COMM` and `_SYSTEMD_UNIT` onto the resource whatever the resource held. On the gateway, logs from Docker containers arrive with `service.name` set from the container name by the edge `containername` processor, and the copy turned it back into `docker.service` (and `process.command` into `dockerd`). The processor now leaves `host.name`, `host.id`, `process.pid`, `process.command` and `service.name` alone when the resource already has them, and fills only the ones it lacks; an empty string counts as lacking. This holds with `split_resources` on and off, and records are no longer split on journal fields whose attributes the resource owns. A resource with none of them is mapped as before, and the default settings are unchanged. One effect to check: the edge's `host.name` now beats the journal's `_HOSTNAME`, so journals relayed from other hosts carry the relaying host's name. The equivalent `transform/journald` block in `test/configs/local-gateway.yaml` has the same guard. Logs that were already stored with `docker.service` are not corrected. The change is in the gateway image only; no edge build includes the processor. See the README.

### Binary size

| Asset | MiB | Bytes | Change from 0.3.13 |
|---|---|---|---|
| `otelcol-edge-0.3.14-linux-amd64` | 169.9 | 178110626 | +1308.0 KiB (+0.76%) |
| `otelcol-edge-0.3.14-linux-amd64-gpu` | 171.1 | 179454664 | +1307.7 KiB (+0.75%) |
| `otelcol-edge-0.3.14-windows-amd64.exe` | 130.6 | 136968704 | +20.0 KiB (+0.01%) |
| `otelcol-gateway-0.3.14-linux-amd64` | 202.2 | 212058272 | +64.0 KiB (+0.03%) |

The Linux edge builds gain `docker_stats`, `ethtool`, `zfskstat` and `k8s_attributes`. The Windows edge and the gateway gain no component; their growth comes from the optional-receiver wrapper in the collector's entrypoint, and for the gateway also from the changed `psi`, `logrecombine` and `journald` modules. Sizes are from the release container builds, and the 0.3.13 gateway size is from the 0.3.13 build of the same recipe. The scrape image gains no component.

## [0.3.13] - 2026-11-15

The `openvpnstatus`, `containername`, `kthread` and `processlifetime` components in the Linux edge builds, the `tightvnc` processor and the `fswatch` receiver in the Windows edge build, and fixes for Sentry noise and the builder. The gateway and the scrape image gain no new component.

### Added

- **`containername` processor in the Linux edge builds.** Docker logs read through the journald driver arrive with `service.name=docker.service`. The new `containername` logs processor sets `service.name` to `container.name` whenever the resource has a non-empty `container.name`, replacing any existing value. Resources without one are unchanged, and a second pass changes nothing. It has no settings and is in the Linux and static Linux edge builds only. Dashboards and alerts keyed on `service.name=docker.service` stop matching those records. A Go benchmark in `test/bench` runs it against the equivalent OTTL `transform` statement on the same batch. See the README.
- **`openvpnstatus` receiver in the Linux edge builds.** It polls an OpenVPN status file and emits one log record per connected client for each new snapshot, with the attributes `common_name`, `real_address`, `bytes_received` and `bytes_sent` (integers) and `connected_since`. The record timestamp is the snapshot time. It reads status versions 1, 2 and 3, and csv or tab separators, from the file content; the v1 `Updated` line is accepted in both date layouts, and a leading byte order mark is ignored. A snapshot without its `END` line is skipped and read again at the next poll, and an unchanged snapshot is emitted once. A row that cannot be parsed, or a v2 or v3 row of an unknown type, is skipped with one warning and the other clients are still emitted. A file over 1 MiB is not loaded. Config keys: `path` (required), `poll_interval` (default `20s`) and `join_routing_table` (default `false`; adds `virtual_address` and `last_ref` from the routing row with the same `common_name` and `real_address`). See `docs/openvpnstatus.md`.
- **TightVNC server log in the Windows edge.** The `tightvnc` processor reads the lines that `filelog` delivers from the TightVNC server log, which the server writes in UTF-16LE. It splits each line into process and thread ids, time, marker and message, sets the record time in the configured `time_zone` (`Local` or `UTC`), sets severity from the message text, and adds `tightvnc.event`, `client.address`, `server.port`, `tightvnc.client.id` and similar attributes. Unmarked debug lines that start with a listed prefix are dropped, unless the line holds an error word; every other line is kept. A line outside the format is kept unchanged with `tightvnc.parse=failed`. A leading byte order mark is removed. The counters `otelcol_tightvnc_records_kept`, `_dropped` and `_unparsed` count every record once. `test/configs/windows-edge.yaml` has the `filelog/tightvnc` receiver and a `logs/tightvnc` pipeline. See `docs/windows-edge.md`.
- **New-file records in the Windows edge.** The `fswatch` receiver (own module, alpha) emits one log record for each file created in or moved into a watched directory, with the path, name, extension, size, directory under the root and creation time. It calls `ReadDirectoryChangesW` directly, with no cgo and no new module in the build. A saved position per root, kept in `file_storage`, reports files created while the collector was down, marked `file.catchup=true`. A buffer overflow or a failed delivery triggers a rescan. When a file's size has stayed the same for `settle` (5 minutes by default), a second record, `file write complete`, carries `file.size_final`; a caught-up file that is already stable gets one record with `file.size_final`. A second record that cannot be delivered is retried and then dropped after 10 attempts or `settle_timeout` (`otelcol_fswatch_writes_dropped`), and after a restart the files created within `settle_timeout` are checked again. Not run on a Windows host yet. See `docs/windows-edge.md` and `receiver/fswatchreceiver/README.md`.
- **`kthread` processor in the Linux edge builds.** On a Linux host the process scraper emits a new series for every pid, and kernel threads such as `kworker` change their pid often. The new `kthread` metrics processor merges every process resource whose `process.executable.name` matches a pattern (default `kworker*`, `ksoftirqd*` and `migration*`) into one resource per scrape, named by `aggregate_name` (default `kernel_threads`) and without `process.pid`. Each metric of the aggregate is the sum of the merged points that share the same attributes and the same scrape (timestamps less than one second apart count as one scrape), so the kernel-thread total stays. A cumulative counter in the aggregate falls when a thread exits, and its start time moves forward, so a cumulative monotonic sum is written non-monotonic and the total should not be read as a rate across an exit. Stale-marked points are skipped, `process.uptime` is not merged, and the resource schema URL is kept when the merged resources agree. A pattern made only of `*` and `?` logs a warning at start. Other resources pass unchanged. It is in the Linux and static Linux edge builds only. Put it before `batch`. See the README.
- **`processlifetime` processor in the Linux edge builds.** `process.cpu.time` and `process.disk.io` are already lifetime totals; the new `processlifetime` metrics processor adds the two ratios that need the age of the process. `process.cpu.lifetime.utilization` (unit `1`) is the CPU seconds over all states divided by `process.uptime`, and `process.disk.io.lifetime.rate` (unit `By/s`, attribute `direction`) is the bytes read or written divided by `process.uptime`. A resource without a positive `process.uptime` gauge gets neither, and the source metrics are not changed. The host metrics process scraper must have `process.cpu.time`, `process.disk.io` and `process.uptime` enabled; `process.uptime` is off by default. It has no settings and is in the Linux and static Linux edge builds only. See the README.

### Fixed

- **Builds no longer pick up a newer `sentry-go`.** Every `builder-*.yaml` now pins `sentry-go` to the version in `go.mod` (v0.29.1) with a `replaces` entry. The builder compiles the generated distribution before the Sentry entrypoint is copied in. Without the pin it took the newest release, and any `svcwrapper.go` left in the output directory by an earlier build stopped compiling (`event.Extra undefined`). `just check-sentry-go-pin` fails when a manifest lacks the pin or names another version, and `just test-custom` runs it.
- **Sentry noise in the error field.** The noise filter now also checks the log line's `error` field, not just its message. Event-log lines such as `process event` carry the cause only in that field, so the `failed to find MUI file` and `use of closed network connection` errors still reached Sentry in 0.3.12.
- **Sentry noise from self-metrics uploads.** A failed upload of the collector's own metrics, logs or traces no longer becomes a Sentry event when its error ends in `: EOF`, or contains `connection reset by peer` or `use of closed network connection`. These are reused connections that the gateway closed, and the next push succeeds. Timeouts, HTTP 5xx and DNS errors still reach Sentry, because they can signal a real outage.
- **Sentry noise from exited processes.** The `process` scraper error for a process that exits between the listing and the read, such as `error reading cpu times for process "example.exe" (pid 7320): The parameter is incorrect.`, no longer becomes a Sentry event. Every `; `-joined part of the error must be such a read; access-denied and other read failures still reach Sentry.
- **Scrape rollback wording.** `docs/scrape-collector.md` said the Recreate strategy applies to the rollback of any workload. A StatefulSet supports only RollingUpdate and OnDelete; with one replica and a ReadWriteOnce volume it also stops the old pod first, leaving a short gap and one reader on the volume at a time. The text now names both workload kinds.

### Binary size

| Asset | MiB | Bytes | Change from 0.3.12 |
|---|---|---|---|
| `otelcol-edge-0.3.13-linux-amd64` | 168.6 | 176771234 | +96.0 KiB (+0.06%) |
| `otelcol-edge-0.3.13-linux-amd64-gpu` | 169.9 | 178115592 | +100.9 KiB (+0.06%) |
| `otelcol-edge-0.3.13-windows-amd64.exe` | 130.6 | 136948224 | +157.5 KiB (+0.12%) |

The Linux edge builds gain `openvpnstatus`, `containername`, `kthread` and `processlifetime`; the Windows build gains `tightvnc` and `fswatch`. Sizes are from the release container builds.

The gateway gains no component. Its asset size comes from the release build and is not listed here. The scrape image gains no component.

## [0.3.12] - 2026-11-12

The `prometheus` receiver in the Linux edge builds, the `gpu` receiver in the Windows edge build, and a fix for duplicate and noisy Sentry events. The gateway and the scrape image gain no new component.

### Added

- **`prometheus` receiver in the Linux edge builds.** The Linux and static Linux edge collectors now ship `prometheusreceiver` v0.164.1, so an edge host can scrape Prometheus exporters on localhost, such as node_exporter, with the usual `scrape_configs`. The scrape job becomes `service.name` and the target `host:port` becomes `service.instance.id`. `test/edge/prometheus.sh` runs the built edge binary against a local exporter and checks the scraped gauge, its job and instance, and `up`. `test/configs/edge-prometheus.yaml` is the config it runs. See the README.
- **GPU metrics in the Windows edge.** The `gpu` receiver is now in the Windows edge build. It calls `nvml.dll` from the NVIDIA driver through `golang.org/x/sys/windows`, without cgo, and emits the same `hw.gpu.*`, `hw.temperature`, `hw.power` and `hw.errors` series as the Linux edge. The optional `library_path` setting names `nvml.dll` by absolute path when the driver keeps it outside System32; it is rejected on other systems and when relative. `test/configs/windows-edge.yaml` has the receiver and a `metrics/gpu` pipeline commented out, and a `gpu` group in `windowhist`. The adapter's tests run without a GPU. The receiver has run on one Windows host with an NVIDIA GPU. See `docs/windows-edge.md`.

### Fixed

- **Sentry noise.** Log lines containing `use of closed network connection` or `failed to find MUI file` no longer become Sentry events. Each ERROR log line was sent twice, by the core wrapper and by a second hook; only the wrapper remains, so each is sent once. The startup event now has a fixed fingerprint and a `server_name` tag, so hosts share one Sentry issue.

### Binary size

| Asset | MiB | Bytes | Change from 0.3.11 |
|---|---|---|---|
| `otelcol-edge-0.3.12-linux-amd64` | 168.5 | 176672930 | +41.5 MiB (+32.7%) |
| `otelcol-edge-0.3.12-linux-amd64-gpu` | 169.8 | 178012232 | +41.5 MiB (+32.4%) |
| `otelcol-edge-0.3.12-windows-amd64.exe` | 130.5 | 136786944 | +94.5 KiB (+0.07%) |

The `prometheus` receiver adds 9 Go modules to each Linux edge build. The Windows build gains the `gpu` receiver.

The gateway gains no component. Its asset size comes from the release build and is not listed here. The scrape collector ships in the image `ghcr.io/vladistan/otelcol-scrape:v0.3.12`; the binary in that image is 147.0 MiB (154136738 bytes), 4 KiB less than in 0.3.11.

## [0.3.11] - 2026-11-09

The `tcp_check` receiver in the edge builds, the `identityfill` processor in the scrape image, and a Sentry DSN override in every binary. The gateway gains no new component.

### Added

- **`OTELCOL_CUSTOM_SENTRY_DSN` overrides the Sentry DSN.** When set to a valid DSN, the collector and the Windows service wrapper send events to that project. Unset or empty keeps the built-in DSN, so default behavior is unchanged. An invalid value is ignored with a warning that does not print the value. The edge, gateway and scrape binaries all carry it. See `docs/otel-metrics.md`.
- **`identityfill` processor in the scrape image.** GCP audit records from the scrape collector had no `host.name`, no `service.name` and an empty body. The processor, already in the gateway image, now ships in the scrape image and fills them. The reference config runs it in `logs/gcp` after `sourcetag/gcp`. `test/scrape/identityfill_test.go` runs a GKE audit entry through the decoder, `sourcetag/gcp` and `identityfill`. Roll the new image before values that name `identityfill`; the old image rejects them. See `docs/scrape-collector.md`.
- **`tcp_check` receiver in the edge builds.** The Linux, static Linux and Windows edge collectors now ship `tcpcheckreceiver` v0.164.1, alpha for metrics, so an edge host can probe TCP targets and report `tcpcheck.status`, `tcpcheck.duration` and `tcpcheck.error`. Targets take IPv4 `host:port` and IPv6 in brackets, `[::1]:port`. `test/edge/tcpcheck.sh` runs the built edge binary against local IPv4 and IPv6 listeners and checks status 1 for an open port and 0 for a closed one. `test/configs/edge-tcpcheck.yaml` is the config it runs. See the README.

### Binary size

| Asset | MiB | Bytes |
|---|---|---|
| `otelcol-edge-0.3.11-linux-amd64` | 127.0 | 133120162 |
| `otelcol-edge-0.3.11-linux-amd64-gpu` | 128.2 | 134454472 |
| `otelcol-edge-0.3.11-windows-amd64.exe` | 130.4 | 136690176 |

The receiver adds about 56 to 64 KiB to each edge binary against 0.3.10. The gateway gains the Sentry DSN override and no component. Its asset size comes from the release build and is not listed here.

The scrape collector is not a release asset; it ships in the image `ghcr.io/vladistan/otelcol-scrape:v0.3.11`. The binary in that image is 147.0 MiB (154140834 bytes), 28 KiB more than in 0.3.10.

## [0.3.10] - 2026-11-08

The `sourcetag` processor in the scrape image, and per-core CPU series in the sample edge configs. The edge and gateway builds are unchanged apart from the version string.

### Added

- **`test/scrape/smoke.sh`** runs the published `otelcol-scrape` image with one source and fake credentials, and checks the version, the health endpoint, that the collector survives a failing receiver, and a clean shutdown.
- **End-to-end test for the ALB access log path.** `test/scrape/alb_e2e_test.go` runs a gzipped `.log.gz` object through a local S3 and SQS stand-in and the built scrape collector, then checks the decoded fields at an OTLP sink and that the SQS message is deleted. A second case shows an object whose key does not end in `.log` or `.log.gz` is dropped with a warning and not parsed. `test/scrape/restart-durability.sh` runs it.
- **Runbook and alert queries.** `docs/windows-edge.md` and `docs/scrape-collector.md` gain a runbook: where the logs are, how to roll back, how to re-read one event-log channel and how to turn summarization off. `docs/alert-queries.sql` holds ClickHouse queries for a stopped Windows service, a metric gap over two send windows, and a spike in dropped or collected events.
- **`sourcetag` processor in the scrape image.** It sets `log.source` and `event.category` and fills cloud resource attributes for `aws`, `gcp` and `azure` records, in Go instead of OTTL. The rules run in the order of the OTTL they replace, so a later rule sees what an earlier one set. It adds the `aws.eks` and `gcp.lb` sources, which the reference config lacked. The counters `otelcol_sourcetag_records_unsourced` and `otelcol_sourcetag_records_uncategorized` count records that leave without `log.source` and without `event.category`, per `provider`. Parity tests run the processor and the OTTL it replaces on the same records and compare every field. See `docs/scrape-collector.md`.

### Changed

- **The scrape reference config uses `sourcetag`** instead of the `transform/aws`, `transform/gcp` and `transform/azure` blocks. Roll the new image before values that name `sourcetag`; the old image rejects them.
- **The sample edge configs request per-core CPU series again.** `hostmetrics` 0.164 made the `cpu` attribute opt-in, so `system.cpu.time` (and `system.cpu.utilization` where enabled) collapsed to one series per state. `local-edge.yaml`, `gpu-edge.yaml` and `windows-edge.yaml` now set `attributes: [cpu, state]`.
- The CHANGELOG lists the binary sizes of every release from 0.3.1 to 0.3.6.

### Binary size

| Asset | MiB | Bytes |
|---|---|---|
| `otelcol-edge-0.3.10-linux-amd64` | 126.9 | 133054626 |
| `otelcol-edge-0.3.10-linux-amd64-gpu` | 128.2 | 134396776 |
| `otelcol-edge-0.3.10-windows-amd64.exe` | 130.3 | 136630272 |
| `otelcol-gateway-0.3.10-linux-amd64` | 208.0 | 218149026 |

The scrape collector is not a release asset; it ships in the image `ghcr.io/vladistan/otelcol-scrape:v0.3.10`. The binary in that image is 147.0 MiB (154112162 bytes).

## [0.3.9] - 2026-11-08

The `prometheus_remote_write` exporter in the gateway image. The edge and scrape builds are unchanged from 0.3.8.

### Added

- **`prometheus_remote_write` exporter in the gateway image.** The 0.3.8 gateway refused a config that used it with "unknown type". `prometheusremotewriteexporter` v0.164.1, beta for metrics, now ships with the gateway, so a collector can push metrics to a Prometheus remote write endpoint. `just verify-gateway` loads `test/configs/gateway-remotewrite.yaml`. See `docs/k8s-cluster.md`.

### Binary size

| Asset | MiB | Bytes |
|---|---|---|
| `otelcol-gateway-0.3.9-linux-amd64` | 202.2 | 211996832 |

## [0.3.8] - 2026-11-08

The `tcp_check` receiver in the gateway image. The edge and scrape builds are unchanged from 0.3.7.

### Added

- **`tcp_check` receiver in the gateway image.** The 0.3.7 gateway refused a config that used `tcp_check` with "unknown receiver". `tcpcheckreceiver` v0.164.1, alpha for metrics, now ships with the gateway, so a cluster collector can run a metrics pipeline that checks TCP targets. `just verify-gateway` loads `test/configs/gateway-tcpcheck.yaml`. See `docs/k8s-cluster.md`.

### Binary size

| Asset | MiB | Bytes |
|---|---|---|
| `otelcol-gateway-0.3.8-linux-amd64` | 201.7 | 211456160 |

## [0.3.7] - 2026-11-07

The `identityfill` processor in the gateway image and `logs.settle_lag` in the scrape image. The edge build is unchanged from 0.3.6.

### Added

- **`logs.settle_lag` for `awscloudwatchreceiver` in the scrape image.** Stock v0.164.1 skips events that CloudWatch ingests late, because an empty window moves the start to now. The image now builds a local copy of the receiver with a `settle_lag` duration, default 0, which ends every window at now minus the lag. With 0 the behaviour is the 0.3.6 behaviour, and the checkpoint format does not change. The sample sets `30s`. See `docs/scrape-collector.md`.
- **`identityfill` gateway processor.** It fills an empty `host.name` and `service.name` on Kubernetes event, pod status and GCP audit logs, and sets an empty GCP audit body from the method and resource names. It never overwrites a value, needs no settings, and is wired into the `otel-cluster` sample.

### Changed

- The scrape sample sets ALB access log `log.source` to `aws.elb`, matching the `aws.elb.*` attributes. Update dashboards and queries that filter on `aws.alb`.

### Binary size

| Asset | MiB | Bytes |
|---|---|---|
| `otelcol-gateway-0.3.7-linux-amd64` | 201.6 | 211402912 |

## [0.3.6] - 2026-11-06

Google Cloud Monitoring in the scrape image. The edge and gateway builds are unchanged from 0.3.5.

### Added

- **`googlecloudmonitoringreceiver` in the scrape image.** It pulls Google Cloud Monitoring metrics, such as the Cloud Run request, latency, CPU, memory and instance-count series. The sample polls two projects, one receiver instance per project, with the project ids read from `GCP_MON_PROJECT_A` and `GCP_MON_PROJECT_B`. Access uses the same default credentials as the Pub/Sub receivers. The identity needs `roles/monitoring.viewer` on each polled project. The receiver is alpha, and delta metrics reach the backend as delta values. See `docs/scrape-collector.md`.

### Binary size

| Asset | MiB | Bytes |
|---|---|---|
| `otelcol-edge-0.3.6-linux-amd64` | 126.9 | 133054626 |
| `otelcol-edge-0.3.6-linux-amd64-gpu` | 128.2 | 134396776 |
| `otelcol-edge-0.3.6-windows-amd64.exe` | 130.3 | 136630272 |
| `otelcol-gateway-0.3.6-linux-amd64` | 207.4 | 217518242 |

## [0.3.5] - 2026-11-05

ALB access logs in the scrape sample, and a scrape sample that no longer carries a site-specific bucket name. The collector code is unchanged from 0.3.4.

### Added

- **ALB access logs in the scrape sample.** The `aws_s3/alb` receiver reads gzipped `.log.gz` objects from S3 through SQS notifications and decodes them with the `elbaccess` format. Records carry `log.source` `aws.alb`, the client address, the status code and the target processing time. The bucket, prefix and queue URL come from `ALB_BUCKET`, `ALB_PREFIX` and `ALB_SQS_URL`.

### Changed

- The scrape sample reads the CloudTrail bucket from `CLOUDTRAIL_BUCKET`.
- The CloudTrail and S3-access receivers in the sample use the `aws_s3` name, so the sample logs no deprecation warning.
- The scrape collector doc covers the current CloudTrail body, a StatefulSet deployment, keyless GCP access under IRSA, CloudTrail digest objects and Pub/Sub stream resets.

### Binary size

| Asset | MiB | Bytes |
|---|---|---|
| `otelcol-edge-0.3.5-linux-amd64` | 126.9 | 133054626 |
| `otelcol-edge-0.3.5-linux-amd64-gpu` | 128.2 | 134396776 |
| `otelcol-edge-0.3.5-windows-amd64.exe` | 130.3 | 136630272 |
| `otelcol-gateway-0.3.5-linux-amd64` | 207.4 | 217518242 |

## [0.3.4] - 2026-11-02

Sentry crash reports for the Windows service, collector base 0.164.1, and a retry-forever edge queue.

### Added

- **Sentry reports for the Windows service.** When the collector service exits with a non-zero code, it sends one event with the exit code and flushes before the process ends. The flush wait is 5 s by default and can be set with `OTELCOL_CUSTOM_SENTRY_FLUSH_TIMEOUT`.

### Fixed

- **`otelcol-scrape` image labels.** The image now carries the source-repository, version and revision labels, so the release workflow can publish it to GHCR.

### Changed

- **Collector base is now 0.164.1** (was 0.154.0). The CloudTrail record body in the scrape sample now comes from `rpc.method`.
- **The Windows edge exporter retries forever and caps its disk queue at 1 GiB.** Before, the default 300 s retry limit dropped data during a long gateway outage. A full queue rejects new data and keeps the oldest.
- **Samples and docs use the current component names.** For example `host_metrics`, `otlp_http`, `resource_detection`, `file_log` and `windows_event_log` replace the deprecated spellings that log a warning at startup.

### Binary size

| Asset | MiB | Bytes |
|---|---|---|
| `otelcol-edge-0.3.4-linux-amd64` | 126.9 | 133054626 |
| `otelcol-edge-0.3.4-linux-amd64-gpu` | 128.2 | 134396776 |
| `otelcol-edge-0.3.4-windows-amd64.exe` | 130.3 | 136630272 |
| `otelcol-gateway-0.3.4-linux-amd64` | 207.4 | 217518242 |

## [0.3.3] - 2026-10-30

Per-window exponential histograms on every edge platform, `k8sevent` parity for `otel-cluster`, and the narrowed `otelcol-scrape` image with the Auth0 receiver.

### Added

- **`windowhist` processor** in every edge build (Linux, GPU and Windows). It turns the samples of one send window into one point per series, with exponential histograms for CPU, memory, disk, network and GPU series and `.rate` series for counters. It is off by default; the sample configs show how to turn it on. See `docs/otel-metrics.md`.
- **`otelcol-scrape` image** with a narrowed component set and the `auth0` receiver, which reads Auth0 events from an SQS queue. The image is published as `ghcr.io/vladistan/otelcol-scrape`.
- **`ottl_parity` option in `k8seventprocessor`**, which reproduces the OTTL event shaping that the cluster collector applies, so `otel-cluster` can run the gateway image.
- **Field conditions in Windows drop rules.** A rule can match event fields with `equals` and `ends_with`, in addition to channel, event ID, level and provider.
- **Hourly drop summary** for the `winlog` processor (`drop_summary`, with `tally_by` per rule). It reports counts of dropped events per rule, event ID and field value, keeps the top 20 values per field and counts the rest as `other`. Off by default.

### Changed

- **Windows default drop list.** The default now drops Security events 4634, 4672 and 5379, and Information and Verbose events 2049 and 2050 from `Microsoft-Windows-SystemDataArchiver/Diagnostic`. Security events 4624, 4658, 5156 and 5158 are no longer dropped by default.
- **Metric types change when summarization is on.** Series in a histogram group are sent as exponential histograms, and counters in those groups add `.rate` series, within a patch release. With `windowhist` out of the pipeline the output is unchanged.
- **Cost of summarization**, measured over one hour with the static edge build: CPU time 1.94x, resident memory +12.7 MiB, egress 2.26x (gzip).
- **`snoozelogprocessor`: `match` is now required.** A config without it fails at startup; there is no built-in default rule.
- **`winlog` severity.** Verbose events (level 5) map to DEBUG, and a severity text that was only a digit is replaced.
- **Narrowed `otelcol-scrape` manifest.** `builder-scrape.yaml` now lists only the components the scrape collector uses.

### Binary size

| Asset | MiB | Bytes |
|---|---|---|
| `otelcol-edge-0.3.3-linux-amd64` | 121.1 | 127021218 |
| `otelcol-edge-0.3.3-linux-amd64-gpu` | 122.4 | 128366312 |
| `otelcol-edge-0.3.3-windows-amd64.exe` | 124.3 | 130305536 |
| `otelcol-gateway-0.3.3-linux-amd64` | 222.4 | 233238690 |

## [0.3.2] - 2026-10-30

Searchable Windows event logs; `journald`, `snoozelog` and health-probe processors in the gateway image.

### Added

- **`winlog` processor** in `otelcol-edge` for Windows. It sets the body to the rendered message, keeps the event XML in `original_message` (capped at 64 KiB by default), sets `severity_*` from the event level, and adds `winlog.*` attributes. Counters `otelcol_winlog_events_read` and `otelcol_winlog_events_dropped` report per channel and rule. See `docs/windows-edge.md`.
- **Drop rules** for Windows events. The default list drops Security events 4624, 4634, 4672, 4658, 5156 and 5158, and Information events from provider `otelcol-edge`. Setting `drop_rules` replaces the list.
- **Gateway processors** `journald`, `snoozelog` and `healthprobe`, plus the `googlecloudmonitoring` receiver and the `googlecloudlogentry_encoding` extension. The `journald` processor maps journald `PRIORITY` 5 to INFO. The sample `test/configs/local-gateway.yaml` defines the three processors without wiring them into a pipeline.

### Changed

- **Security event log** is no longer unfiltered. In 0.3.1 the Security channel passed every event; 0.3.2 applies the default drop rules.

### Binary size

| Asset | MiB | Bytes |
|---|---|---|
| `otelcol-edge-0.3.2-linux-amd64` | 121.0 | 126886050 |
| `otelcol-edge-0.3.2-linux-amd64-gpu` | 122.3 | 128229544 |
| `otelcol-edge-0.3.2-windows-amd64.exe` | 124.1 | 130134016 |
| `otelcol-gateway-0.3.2-linux-amd64` | 222.4 | 233238690 |

## [0.3.1] - 2026-10-27

Windows edge artifact; `otel-main` runs a gateway image built from this tree.

### Added

- **Windows edge** `otelcol-edge-0.3.1-windows-amd64.exe` (windows/amd64, CGO off, unsigned). It collects host metrics, Windows performance counters, Windows event logs and file logs, installs as a Windows service through the OCB service handler, and writes a rotating self-log (10 MiB, 5 backups). The sample config is `test/configs/windows-edge.yaml`.
- **Event logs are on by default** with the Security channel unfiltered; 0.3.2 adds default drop rules for it. The service runs as LocalSystem.
- **Gateway image** `ghcr.io/vladistan/otelcol-gateway:v0.3.1`, built from this tree. Its component set is unchanged from the image `otel-main` runs today.

### Changed

- **Collector base** moves from v0.140.0 to 0.154.0 in the edge, edge-static, gateway and scrape manifests and in every local component module. The gpu receiver calls `scraperhelper.AddMetricsScraper`, since `AddScraper` is deprecated. The Linux edge exports the same metrics and lists the same components as 0.2.0 (checked on macOS against the 0.2.0 build); the `components` command now prints snake_case display names such as `file_log`, and config keys are unchanged.

### Binary size (bytes)

| Build | Bytes |
|---|---|
| 0.2.0 static edge | 120279202 |
| 0.3.1 static edge | 126886050 (+5.49%) |
| 0.2.0 gpu edge | 121624488 |
| 0.3.1 gpu edge | 128229544 (+5.43%) |
| 0.3.1 Windows edge | 130071552 |
| 0.3.1 gateway | 231993506 |

The Linux edge growth comes from the 14-minor base bump.

## [0.2.0] - 2026-10-16

### Added

- **GPU receiver (`gpu`)** in `otelcol-edge`: reads NVIDIA GPU metrics through NVML and emits them with OpenTelemetry semantic-convention names, pinned to semconv v1.44.0 plus three extension metrics. It is a modified copy of `github.com/pmady/otel-gpu-receiver` (Apache-2.0), ported down to collector v0.140.0. See `receiver/gpureceiver/README.md` for the metric table and provenance. Hosts without NVIDIA run the receiver unchanged: NVML loads lazily.
- **Two edge artifacts.** `otelcol-edge-0.2.0-linux-amd64-gpu` includes `gpu`, is built with cgo against glibc, and needs glibc 2.35 or newer. `otelcol-edge-0.2.0-linux-amd64` is the static build without `gpu` and behaves as 0.1.64, for hosts below glibc 2.35. Both are in `SHA256SUMS`.

### Changed

- **Linux edge build** moved into a pinned `ubuntu:22.04` container with a glibc-symbol check that fails the build above 2.35. The gpu artifact is built with `netgo` and `osusergo`; the static artifact is `CGO_ENABLED=0` with no tags, as in 0.1.64.
- **`generate-edge`** creates `build/` first, so a fresh checkout builds without a manual `mkdir`.
- **`verify-public-artifacts`** runs the gpu artifact in `ubuntu:22.04` and the static artifact in `rockylinux:9` (glibc 2.34), `ubuntu:20.04` (2.31) and `centos:7` (2.17).

### Edge binary size (bytes)

| Build | Bytes |
|---|---|
| 0.1.64 published edge | 120344738 |
| 0.2.0 gpu edge | 121624488 (+1.06%) |
| 0.2.0 static edge | 120279202 |

### Hosts below glibc 2.35

Hosts with glibc older than 2.35 use the static artifact.

### Real-GPU verification

Run natively on one NVIDIA L4 (driver 580.126.09, Ubuntu 24.04, glibc 2.39), 10 minutes with the receiver and 10 minutes without.

- **Scrape time:** first scrape 43 ms, slowest 41 ms (limit 2500 ms).
- **CPU:** 0.00108 core with `gpu`, 0.00073 without; the 0.00035 core difference is under the 0.01 limit.
- **Gateway:** all 12 `hw.*` metric names arrived at a 0.1.64 gateway.
- **Against `nvidia-smi`:** power 27.24 W vs 27.14 W, temperature 40 vs 40, memory limit equal, utilization 0 vs 0. Memory usage reads 21641 MiB vs 21171 MiB (470 MiB higher; suspected NVML v1 reserved memory, not confirmed). Clock reads 1770 MHz vs 2040 MHz SM clock at idle (not confirmed).

### Known limits

- The gpu artifact runs only on glibc 2.35 or newer.
- The 470 MiB memory-usage difference and the idle clock difference above are unexplained.
- No GPU host other than the one L4 was tested.

## [0.1.64] - 2026-09-18

### Added

- **ClickHouse exporter**: `clickhouseexporter` v0.140.0 added to the gateway distribution's builder manifest, making `clickhouse` an available exporter in the compiled binary. Validated end to end against a pilot ClickHouse instance: exact row counts and correct field content confirmed for logs, gauge metrics and sum metrics, independently of the collector's own logs.

### Changed

- **Container base images bumped**: builder stage moved from Go 1.23.12 (Alpine 3.22.1-based) to Go 1.26.8 (Debian bookworm); runtime stage moved from Alpine 3.21.7 to Alpine 3.22.5. `GOTOOLCHAIN` pinned to `local` (was `auto`, which was silently fetching go1.26.8 at build time regardless of the declared pin).

### Documentation Gap

- Versions 0.1.7 through 0.1.62 were never given changelog entries, and 0.1.63 (published 2026-09-15, the initial public release) has none either. This gap is not backfilled by this release; it is only noted here.

## [0.1.6] - 2025-12-14

### Changed

- **Sentry source upload**: Source files now uploaded with full build paths to match Go binary stack traces
- **Noise filter expanded**: Added filters for filesystem permission errors (`no such file or directory`, `failed to read usage`)

## [0.1.5] - 2025-12-14

### Added

- **Sentry zap integration**: Collector internal error-level logs are now captured and sent to Sentry
  - Wraps zap logger core to intercept errors
  - Includes structured fields (kind, name, data_type) as Sentry tags
  - Error details included as exception with stack trace
  - Noisy errors (context canceled, connection reset, regex pattern mismatch) filtered out

- **Journald multiline recombine**: Stack traces and multi-line log entries are now consolidated
  - Uses PID-based source identifier for accurate grouping
  - Matches entries starting with ISO timestamp pattern

### Fixed

- **Container parser type safety**: Fixed `invalid operation: int(string)` errors when container log parsers encountered non-container journald entries
  - Container name is first copied to `attributes._container_name` (proper string type)
  - All container parser conditions now use the typed attribute instead of body field

## [0.1.4] - 2025-12-13

### Added

- **Per-container log parsing**: Extract structured fields from Docker container logs
  - Built-in parsers for postgres, gocd, litellm containers
  - Configurable via an `otel_container_log_parsers` variable in the deploying
    site's own configuration management _(pre-publication note: this referred
    to an internal Ansible variable; the deployment mechanism is not part of
    this repository)_
  - OTel severity mapping from log_level field

- **elk-tool CLI** _(pre-publication note: an internal, unpublished sibling
  tool used alongside this collector at the time; not part of this
  repository)_: Python/uv CLI for querying and managing Elasticsearch test data
  - Commands: `query`, `list`, `lift`, `delete`, `cleanup`
  - Auto-discovers credentials from `.envrc` files
  - Supports wildcard index patterns

### Known Issues

- `service.name` cannot be overridden for Docker container logs (upstream issue)

## [0.1.3] - 2025-12-12

### Added

- **Process metrics filtering**: Filter low-resource process metrics to reduce cardinality (93% reduction)
  - Memory < 50MB, CPU < 0.1%, disk I/O < 1MB filtered out

## [0.1.2] - 2025-12-07

### Fixed

- **percpu metrics now report as ratio (0-1)** instead of percentage (0-100), consistent with other OTel utilization metrics. Multiply by 100 in Kibana/Grafana to display as percentage.

## [0.1.1] - 2025-12-06

### Added

- **Per-CPU utilization metrics**: New `process.cpu.utilization.percpu` and `system.cpu.utilization.percpu` metrics that show CPU usage as a percentage of a single core (top-style), making it easier to compare with tools like `top` and `htop`
  - OTel's default utilization metrics are normalized by total CPU capacity
  - New `.percpu` metrics multiply by CPU count × 100 for human-readable percentages

- **Transform processor** added to edge build for future OTTL-based transformations

### Changed

- `normalizedloadprocessor` now handles both normalization (divide by CPU count) and denormalization (multiply by CPU count) operations

## [0.1.0] - 2025-12-06

### Added

- **Edge Collector Profile** (`otelcol-edge`): Lean collector for VMs and bare metal hosts
  - PSI receiver for Linux Pressure Stall Information metrics
  - Normalized load processor (load average / CPU count)
  - journald, filelog, hostmetrics receivers
  - 77MB binary (43% smaller than full collector)

- **Versioning System**: Adopted a version-file-driven pattern from another
  internal tool _(pre-publication note: referred to internally as
  "host-sync-bot", an unpublished sibling tool; not part of this repository)_
  - VERSION file as single source of truth
  - Per-profile service names (otelcol-edge, otelcol-scrape, otelcol-gateway)
  - Version bump commands (`just bump`, `just bump-minor`, `just bump-major`)
  - Build-time injection via ldflags

- **Custom Components**:
  - `psireceiver` - Collects PSI metrics from `/proc/pressure/`
  - `normalizedloadprocessor` - Calculates normalized load averages

- **Build System**:
  - OCB (OpenTelemetry Collector Builder) integration
  - Separate build targets per profile
  - Cross-compilation for Linux amd64
  - Sentry integration for error tracking

### Notes

- Based on OpenTelemetry Collector v0.115.0
- All collector profiles share the same version number (coupled releases)
