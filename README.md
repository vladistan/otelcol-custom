# otelcol-custom

Custom OpenTelemetry Collector distribution with extended log-parsing processors.

## Overview

This collector includes:
- Standard components from otelcol-contrib
- Custom receivers and processors for structured log parsing and enrichment

## Building

Prerequisites:
- Go 1.25+ for the edge, edge-windows, gateway and scrape builds (Go 1.23+ for the default profile)
- OpenTelemetry Collector Builder (OCB)

```bash
# Install OCB for the default profile
go install go.opentelemetry.io/collector/cmd/builder@v0.115.0

# Build the collector
builder --config builder-config.yaml
```

The edge, edge-windows, gateway and scrape profiles build with OCB v0.164.0;
the `justfile` recipes (`just build-edge`, `just generate-edge-windows`, ...)
pin the right version for each.

## Installation

Prebuilt `edge` binaries (Linux, Windows and FreeBSD), a `gateway` binary and a gateway
container image are published for each release — no Go toolchain required.

### Binary download (edge/gateway, linux/amd64)

```bash
version=$(curl -fsSL https://api.github.com/repos/vladistan/otelcol-custom/releases/latest | jq -r .tag_name)
curl -fsSLO "https://github.com/vladistan/otelcol-custom/releases/download/${version}/otelcol-edge-${version#v}-linux-amd64"
curl -fsSLO "https://github.com/vladistan/otelcol-custom/releases/download/${version}/otelcol-gateway-${version#v}-linux-amd64"
curl -fsSLO "https://github.com/vladistan/otelcol-custom/releases/download/${version}/SHA256SUMS"
sha256sum -c SHA256SUMS --ignore-missing
```

### Windows edge (windows/amd64)

From 0.3.1 the release also carries `otelcol-edge-<version>-windows-amd64.exe`,
listed in the same `SHA256SUMS`. The binary is unsigned and runs as a Windows
service under `LocalSystem`, so review that choice for your hosts. See
[Windows edge collector](docs/windows-edge.md)
for the metrics, the event-log defaults and the sample config
(`test/configs/windows-edge.yaml`).

### FreeBSD edge (freebsd/amd64)

The release also carries `otelcol-edge-<version>-freebsd-amd64`, a static binary
listed in the same `SHA256SUMS`. Download it the same way as the Linux edge, then
see [FreeBSD edge collector](docs/freebsd-edge.md) for the install path, the rc.d
service and the sample config (`test/configs/truenas-core-edge.yaml`). The release
checks do not start it, so run `otelcol-edge validate` on the host first.

Releases and assets are also browsable at
https://github.com/vladistan/otelcol-custom/releases.

### Gateway container image

```bash
version=$(curl -fsSL https://api.github.com/repos/vladistan/otelcol-custom/releases/latest | jq -r .tag_name)
docker pull "ghcr.io/vladistan/otelcol-gateway:${version}"
docker run --rm "ghcr.io/vladistan/otelcol-gateway:${version}" --version
```

## Deployment Profiles

- **default** (`otelcol-custom`) - full component set, built via `builder-config.yaml`.
- **edge** (`otelcol-edge`) - lean collector for VMs and bare metal, built via
  `builder-edge.yaml`. Meant to run as a host binary managed by your own
  configuration management (e.g. Ansible, systemd), **not** as a container -
  there is intentionally no `Dockerfile.edge`.
- **gateway** (`otelcol-gateway`) - central aggregation and processing, built
  via `builder-gateway.yaml`. Ships as a container image built from
  `Dockerfile.gateway`.
- **scrape** (`otelcol-scrape`) - cloud source scraping, built via
  `builder-scrape.yaml`. Ships as a container image built from
  `Dockerfile.scrape`.

Both container images are real multi-stage builds that compile the collector
inside the builder stage - `docker build -f Dockerfile.gateway .` (or
`Dockerfile.scrape`) needs nothing beyond a clean checkout.

## Components

### Standard (from otelcol-contrib)
- **Receivers**: otlp, host_metrics, journald, tcp_check (edge builds from 0.3.11), prometheus (Linux edge builds from 0.3.12), docker_stats (Linux edge builds from 0.3.14), snmp and netflow (Linux edge builds from 0.3.15)

- **Receivers**: otlp, host_metrics, journald, tcp_check (edge builds from 0.3.11), prometheus (Linux edge builds from 0.3.12), docker_stats (Linux edge builds from 0.3.14), receiver_creator with the k8s_observer extension and the redis,
  postgresql, mysql, mongodb, rabbitmq and nginx receivers (Linux edge builds from 0.3.15)
- **Processors**: batch, memory_limiter
- **Exporters**: otlp_grpc, debug

### TCP checks on edge hosts

The `tcp_check` receiver is in the Linux, static Linux and Windows edge builds from
0.3.11. It opens a TCP connection to each target every collection interval and
reports `tcpcheck.status` (1 connected, 0 not), `tcpcheck.duration` and
`tcpcheck.error`. It is alpha for metrics.

A target is `host:port`. Write an IPv6 address in brackets:

```yaml
receivers:
  tcp_check:
    collection_interval: 30s
    targets:
      - endpoint: 192.0.2.11:8006
        dialer:
          timeout: 5s
      - endpoint: "[2001:db8::11]:8006"
        dialer:
          timeout: 5s
```

`test/configs/edge-tcpcheck.yaml` is the full config, and `test/edge/tcpcheck.sh
build/edge/otelcol-edge` runs it against local IPv4 and IPv6 listeners.

### Optional receivers

A receiver that fails to start normally stops the collector. Three receivers are
optional: `docker_stats`, `tcp_check` and `netflow`. If one of them fails to
start, for example because Docker is not running yet or the flow port is already
in use, the collector keeps running without it. The list is fixed in the code
and cannot be changed in the configuration. Every other receiver, `journald` and
`hostmetrics` included, still stops the collector on a failed start. A
configuration that cannot build the receiver at all is still rejected at
startup.

What you see when an optional receiver is skipped:

- A WARN log line `optional receiver did not start and is skipped`, with the
  `receiver` ID and the `error`. Credentials in a URL in the error text are
  replaced with `***`.
- The counter `otelcol_optional_receiver_skipped` with the attribute `receiver`.
  It is 0 from the start and goes to 1 on a skip. It is a collector self-metric,
  so it appears only when the collector's own metrics are enabled
  (`service.telemetry.metrics.level` is not `none`).
- The component status of the receiver becomes `RecoverableError` after the
  first retry (30 s), so an extension that watches component status, such as
  `health_check`, can report it. Until then the status is OK.
- Sentry does not see the WARN. Only errors that stop the collector reach it.

The collector retries the start in the background after 30 s, then after 60 s,
120 s and so on up to every 5 minutes. A WARN is logged on the first failure and
on every tenth failure after it. When a start succeeds the collector logs an
INFO line and the status returns to OK. The retry stops at shutdown.

### Proxmox VE metrics

The `pve` receiver is in the Linux and static Linux edge builds and in the scrape image (from 0.3.16; the
scrape pod sets `guests.node` on one instance per node, see
[Proxmox VE and SNMP pollers](docs/scrape-collector.md#proxmox-ve-and-snmp-pollers)). It reads a Proxmox VE cluster through
the HTTP API with two scrapers; the metric names and attributes are in
[Metrics and runtime environment](docs/otel-metrics.md#proxmox-ve--receiverpvereceiver). It runs no
external command.

```yaml
receivers:
  pve:
    base_urls: [https://pve1:8006, https://pve2:8006]
    ca_file: /etc/otelcol/pve-ca.pem
    resources:                      # one node per cluster
      enabled: true
      token_id: otel@pve!resources
      token_secret: ${env:PVE_RESOURCES_TOKEN}
    guests:                         # every node, its own guests only
      enabled: true
      token_id: otel@pve!guests
      token_secret: ${env:PVE_GUESTS_TOKEN}
      node: pve1                    # required in a scrape pod; the host name is used when empty
```

- `base_urls` is a list. A connect or TLS failure moves on to the next address; for `resources` so does a 5xx
  answer or a request timeout. For `guests` a timeout is the answer: a stuck guest costs one timeout, not one
  per address. Certificates are verified. Put the cluster CA in `ca_file`; `insecure_skip_verify: true` is an
  explicit opt-in and logs one WARN at start.
- `resources` needs a token with the `PVEAuditor` role on `/`. `guests` needs a token whose role holds
  `VM.Audit` and the agent privilege on `/vms`: `VM.Monitor` on PVE 8 and earlier, `VM.GuestAgent.Audit`
  on PVE 9. `PVEAuditor` does not carry `VM.Monitor`, so on PVE 8 `guests` needs a custom role. From the
  PVE manual, unverified: `pveum role add OtelGuestAgent --privs "VM.Audit VM.Monitor"`, then
  `pveum acl modify /vms --tokens 'otel@pve!guests' --roles OtelGuestAgent`, and a token created with
  privilege separation. PVE shows the secret once; keep it in an environment variable or a file, not in
  the config text.
- The token secret never appears in a log line, an error or a config dump.
- `Start` makes no network call. With the API down the collector starts and each interval logs one
  error. A 401 or 403 logs one WARN per interval; a 403 on an agent call names the missing privilege.
- The `guests` scraper polls every 15 minutes by default because the facts change rarely and the agent
  calls take PVE API worker time. It runs one request at a time, skips guests that are not running and
  backs off a guest that fails.
- `pve.guest.lock` (value 1, attribute `state`) is sent for a guest only while it holds a lock such as
  `backup` or `migrate`; an unlocked guest has no series. It comes from the guest lists the `guests`
  scraper already reads, so it adds no request. See the metrics page for an alert query.
- The metrics and the `pve guest fact` change-event logs come from the same receiver; wire it into a
  metrics pipeline and a logs pipeline.

### SNMP and NetFlow

The Linux and static Linux edge builds include the contrib `snmp` and `netflow` receivers (both alpha); the
scrape image includes `snmp` only, from 0.3.16.
`snmp` polls one device per instance, by OID; `netflow` turns NetFlow, IPFIX and sFlow records into logs,
one record per flow. See [SNMP and NetFlow on the Linux edge](docs/snmp-netflow.md) for the keys, the
unverified QNAP iSCSI and LLDP OIDs, how to measure flow volume, and the advice to bind one address because
NetFlow has no authentication. Samples: `test/configs/edge-snmp.yaml` and `test/configs/edge-netflow.yaml`.

### Prometheus scraping on edge hosts

The `prometheus` receiver is in the Linux and static Linux edge builds from 0.3.12.
It scrapes Prometheus exporters on the host, such as node_exporter, with the same
`scrape_configs` as Prometheus. The scrape job becomes `service.name` and the
target `host:port` becomes `service.instance.id`. The Windows edge build does not
include it.

```yaml
receivers:
  prometheus:
    config:
      scrape_configs:
        - job_name: node
          scrape_interval: 30s
          static_configs:
            - targets: ["127.0.0.1:9100"]
```

`test/configs/edge-prometheus.yaml` is the full config, and
`test/edge/prometheus.sh build/edge/otelcol-edge` runs it against a local exporter.

### Docker container metrics

The `docker_stats` receiver is in the Linux and static Linux edge builds from 0.3.14.
It reads container statistics and settings from the Docker socket: CPU and memory
use, block I/O and network traffic, plus the limits each container runs with. The
Windows edge, the gateway and the scrape collector do not include it. The Docker log
driver does not matter, because the receiver uses the stats and inspect API.

```yaml
receivers:
  docker_stats:
    endpoint: unix:///var/run/docker.sock
    api_version: ""
    collection_interval: 60s
    metrics:
      container.cpu.limit:
        enabled: true
      container.memory.usage.limit:
        enabled: true
```

- Keep `api_version` empty. In v0.164.1 the receiver's default is `1.44`, although its
  README says it negotiates. A Docker daemon whose API stops below 1.44 (Docker 24
  answers up to 1.43) rejects that version, the receiver fails to start, and the
  collector keeps restarting. An empty value lets the Docker client negotiate the
  version with the daemon on its first request. `just check-docker-stats-api-version`
  fails when a sample config has a `docker_stats` block without it.

- `container.memory.usage.limit` is on by default. A container with no memory limit
  reports the host's memory, so a value near the host total means no limit.
- `container.cpu.limit` is off by default and the block above turns it on. It comes
  from `--cpus` (or compose `cpus:`), then the cpuset size, then the CPU quota. A
  container with no CPU limit reports nothing.
- The socket belongs to root and the `docker` group. The collector must run as root
  or as a member of that group. Without access the receiver logs the error and the
  collector keeps running.
- Each metric carries `container.id`, `container.name`, `container.image.name`,
  `container.hostname` and `container.runtime`, the names the log path already uses.
- Series grow with container churn: every new container id is a new series. With the
  limit metrics on, a container sends 14 points per scrape.

`test/configs/local-edge.yaml` has the block in a full edge config.

### Network allowance counters on AWS hosts

The `ethtool` receiver is in the Linux and static Linux edge builds. An AWS ENA
network interface counts the packets the instance dropped or queued because it
exceeded a network allowance. The receiver reads the five counters in the collector
process with the `SIOCETHTOOL` ioctl; it starts no external command and needs no cgo.

```yaml
receivers:
  ethtool:
    collection_interval: 60s   # default
    drivers: [ena]             # default
    interfaces: []             # empty: every interface with a matching driver
    max_statistics: 200        # default; cap on network.interface.statistic series per scrape
    statistics: []             # default: none; see "Other statistics" below
```

It emits one cumulative sum, `network.allowance.exceeded` (unit `{packet}`), with
the attributes `network.interface.name` and `allowance`:

| `allowance` | ENA counter |
|---|---|
| `bandwidth_in` | `bw_in_allowance_exceeded` |
| `bandwidth_out` | `bw_out_allowance_exceeded` |
| `pps` | `pps_allowance_exceeded` |
| `conntrack` | `conntrack_allowance_exceeded` |
| `linklocal` | `linklocal_allowance_exceeded` |

When the driver reports `conntrack_allowance_available` (AWS Nitro instances with ENA
driver 2.8.1 or later), the receiver also emits the gauge `network.allowance.available`
(unit `{connection}`) with `network.interface.name` and `allowance` set to `conntrack`: the
tracked connections the instance can still create before it reaches its allowance. The
allowance itself is not in the ioctl output, so an alert uses a floor on `available`, not a
ratio. A driver that does not report it gives no point and one info line per interface.

#### Other statistics

With no `statistics` entry the receiver reads nothing beyond the six names above. Each
entry of `statistics` selects more driver statistics:

```yaml
receivers:
  ethtool:
    statistics:
      - match: '^queue_[0-9]+_tx_cnt$'      # a regular expression, unanchored, use ^ and $
        type: sum                           # sum for a counter, gauge for a value that moves both ways
      - match: '^queue_[0-9]+_tx_queue_stop$'
        type: gauge
    max_statistics: 200
```

Each match becomes one point of `network.interface.statistic` (unit `1`) with the
attributes `network.interface.name` and `statistic`, the full name from the driver. An
entry of type `sum` makes a monotonic cumulative sum and one of type `gauge` a gauge;
both are metrics of that one name. A name that several entries match is emitted once,
under the first. `ethtool` does not say whether a statistic is a counter or a gauge, so
the type is required, and a pattern that does not compile, an entry without a type and
any type but `sum` or `gauge` fail at startup. The old key `stats_regex` stays rejected.
An entry that matches no statistic logs one info line.

The ENA statistics AWS documents are `bw_in_allowance_exceeded`, `bw_out_allowance_exceeded`,
`pps_allowance_exceeded`, `conntrack_allowance_exceeded`, `linklocal_allowance_exceeded` and
`conntrack_allowance_available`. The driver also reports per-queue statistics whose
names carry the queue number, such as `queue_0_tx_cnt` and `queue_0_rx_cnt` (the names
differ by driver version and were not checked on a host for this release). A pattern that
matches per-queue names multiplies the series by the number of queues: a 32-queue instance
with a dozen statistics per queue is several hundred series per interface. `max_statistics`
(default 200) caps the series of `network.interface.statistic` in one scrape across all
interfaces; the names past the cap, in sorted order by interface and then name, are dropped
with one warning for as long as the count stays over the cap. Other drivers than `ena`
work when listed in `drivers`, but their statistic names were not checked. One interface
can use the whole cap, so the interfaces sorted later get no series.

An interface that does not support statistics (a bridge, `veth` or loopback) is skipped without a
warning. A matching interface whose driver lacks the counters gets one warning, as
does a permission error. The read commands need no capability, so a permission error
means a seccomp or security profile of the container blocks the ioctl; the warning names
the interface. The counters count from the driver load, so the start time of each
point is the boot time of the host, as in the host metrics receiver; a backend that
turns a cumulative sum into a rate sees the first point as the total since boot.
With no interface of a matching driver the receiver logs one info line and emits
nothing. In a pod the receiver sees only the interfaces of the pod's network
namespace, so the DaemonSet needs `hostNetwork: true` to see the host's ENA interface.

### Container names in Docker logs

The `containername` processor is in the Linux, static Linux and FreeBSD edge builds.
Docker containers that log through the journald driver reach the collector with
`service.name` set to `docker.service`, the unit that runs the daemon. When a
resource has a non-empty `container.name`, the processor sets `service.name` to that
value, replacing whatever was there. A resource without `container.name`, or with an
empty one, is left as it was, and running the processor twice changes nothing. It has
no settings. The same rule applies to logs, metrics and traces: the `container.*`
series of `docker_stats` carry `container.name` on the resource and no `service.name`,
so list the processor in the metrics pipeline that holds `docker_stats` to give them one:

```yaml
processors:
  containername: {}

service:
  pipelines:
    logs:
      processors: [memory_limiter, containername, batch]
    metrics:
      receivers: [docker_stats]
      processors: [memory_limiter, containername, batch]
```

Dashboards and alerts that match logs on `service.name=docker.service` stop matching
those records. The Windows edge, the gateway and the scrape collector do not include
the processor. A rule further down the pipeline, or in a gateway, that sets
`service.name` replaces the value this processor set.

#### Multi-line Docker logs

The `logrecombine` processor joins log lines that arrive within `gap_timeout` and do
not start with a timestamp. It groups entries by `source_identifier` (`body._PID`). For
Docker's journald driver that PID belongs to the daemon and is the same for every
container, so `container_identifier` (default `body.CONTAINER_ID`) groups an entry that
has that field by container instead. A daemon line has no `CONTAINER_ID`, so it is never
joined to a container's lines, and two containers are never joined to each other.
Entries that do not carry the field are grouped by PID as before; `container_identifier: ""`
turns the container grouping off.

```yaml
processors:
  logrecombine:
    container_identifier: body.CONTAINER_ID   # default
```

### Journald resource attributes

With `set_resource` on (the default), the `journald` processor of the gateway maps
the journal fields `_HOSTNAME`, `_MACHINE_ID`, `_PID`, `_COMM` and `_SYSTEMD_UNIT` to
the resource attributes `host.name`, `host.id`, `process.pid`, `process.command` and
`service.name`. It sets an attribute only when the resource does not already have a
value for it; an empty string counts as no value, so a blank name from a failed
detector or an unset role variable is filled from the journal. A value put there by an
earlier stage, such as `service.name` from a container name (see the section above),
is kept, because that stage knew more than the journal does: the unit of a container's
log line is always the Docker daemon's. The same rule applies to the other four
attributes, so a host name or process command added by the edge also wins.

Two consequences to check in a role. The edge's `host.name` now beats the journal's
`_HOSTNAME`: when the gateway receives journals that a log forwarder relays from other
hosts, the records carry the forwarder's name, as the resource says, and no longer the
origin's. And a Linux edge that puts its own `service.name` on every log resource keeps
it on the journal records too, instead of the unit name.

Attributes the processor sets itself still follow the records it reads;
`split_resources` (default on) gives each distinct set of journal fields its own
resource, comparing only the fields of attributes the resource does not own, and does
not split at all when the resource owns all five. With it off, the last record read
sets the attributes the resource does not own.

#### Extended mapping

Three more keys, all off by default, so a config without them gives the output of 0.3.14.

```yaml
processors:
  journald:
    set_resource: true
    split_resources: true
    map_severity: true
    body_from_message: true
    keep_fields: false
    map_extended: true
    skip_when_resource_has: [container.name]
    drop_unmapped_underscore: true
```

`map_extended` maps fifteen more journal fields. Six go to resource attributes and
follow the same no-overwrite rule as the first five, with `split_resources` on and off:

| Journal field | Resource attribute |
|---|---|
| `_EXE` | `process.executable.path` |
| `_CMDLINE` | `process.command_line` |
| `_UID` | `user.id` |
| `_GID` | `user.group.id` |
| `_SYSTEMD_CGROUP` | `container.id` (the cgroup path, as given) |
| `_RUNTIME_SCOPE` | `container.runtime` |

Nine go to log attributes, set on each record:

| Journal field | Log attribute |
|---|---|
| `_BOOT_ID` | `boot.id` |
| `_SYSTEMD_SLICE` | `systemd.slice` |
| `_SYSTEMD_INVOCATION_ID` | `systemd.invocation_id` |
| `_TRANSPORT` | `log.source` |
| `SYSLOG_FACILITY` | `syslog.facility.code` |
| `SYSLOG_IDENTIFIER` | `syslog.appname` |
| `SYSLOG_PID` | `syslog.procid` |
| `_SELINUX_CONTEXT` | `selinux.context` (newlines kept) |
| `_CAP_EFFECTIVE` | `process.capabilities.effective` |

All values are strings, as the journal gives them. A field a record lacks sets nothing.
The mapped fields no longer appear under `journald.fields` when `keep_fields` is on.
`map_extended` needs `set_resource`, and logs a warning at start when `split_resources`
is off: records of different units that share a resource then get the resource attributes
of one record.

With `map_extended` the processor also cleans what is left of a map body and of
`journald.fields`:

- `PRIORITY`, `SYSLOG_FACILITY`, `SYSLOG_IDENTIFIER` and `SYSLOG_PID` are removed from a map
  body that stays a map, after the severity and the attributes were read from them.
- Each other key that starts with `_` and that no mapping read moves into the log attribute
  `journald.unknown_fields`, a map. It is absent when no such key is left. The keys leave
  `journald.fields` and the body.
- `_SOURCE_REALTIME_TIMESTAMP`, `__CURSOR`, `__MONOTONIC_TIMESTAMP` and `_STREAM_ID` are
  dropped from the body, `journald.fields` and `journald.unknown_fields`.

The mapped `_` keys such as `_HOSTNAME` stay in a map body unless `drop_unmapped_underscore`
is on.

##### Differences from the `transform` block it replaces

A `transform` block that maps the same fields behaves differently in five ways:

- It writes `container.runtime`, `selinux.context` and `process.capabilities.effective` only
  for a record without `MESSAGE`; the block turns the body into a string first, and its later
  statements then do not run. The processor sets all three on every record that has the field.
- It writes `journald.unknown_fields` as an empty map, and only on a record without `MESSAGE`.
  The processor writes the keys that were left over, on every record that has any, with or
  without `MESSAGE`. Real journal records carry several such keys (`_AUDIT_SESSION`,
  `_AUDIT_LOGINUID`, `_SYSTEMD_USER_SLICE` and others), so after the swap most rows gain this
  attribute. Four leftover keys add 146 bytes to a record that is 532 bytes in the block's output,
  and about 1.8 microseconds to the processor's time per record on the benchmark, which still
  runs at about half the time of the block on that fixture.
- It overwrites the resource attributes `process.executable.path`, `process.command_line`,
  `user.id`, `user.group.id`, `container.id` and `container.runtime`; the processor sets them
  only when the resource lacks them, an empty string counting as missing. A resource that
  carries one of them from a detector keeps its value.
- It sets `host.name`, `process.pid`, `process.command` and `service.name` on every record
  (the last three unless the resource has `container.name`). The processor keeps a non-empty
  value that the resource already has.
- It leaves the severity unset for `PRIORITY` 5 (notice). The processor gives `INFO`.

The five resource fields differ per process, so with `split_resources` on the processor
makes one resource for each distinct combination in a batch. It sets no cap on that
number. The histogram `otelcol_journald_resources_per_batch` shows how many resources
each batch has; watch it, and the memory of the collector, after turning `map_extended` on.

`skip_when_resource_has` lists resource attributes. A resource that carries any of them,
with a non-empty value, gets none of `process.pid`, `process.command`, `service.name` and
`container.id` from the journal. With `container.name` listed, a container's logs keep the
resource the edge gave them and do not take the Docker daemon's pid, command and unit. The
default is an empty list.

`drop_unmapped_underscore` removes every key that starts with `_` from a map body that has
no `MESSAGE`; such a body is otherwise passed on whole. The mapping and `keep_fields` read
the fields first, so only the body is stripped.

### Kernel threads in process metrics

The `kthread` processor is in the Linux and static Linux edge builds. The process
scraper emits one resource per process, and Linux kernel threads such as `kworker`
change their pid often, so each new pid starts a new series. The processor merges
every resource whose `process.executable.name` matches a pattern into one aggregate
resource per scrape. Each metric of the aggregate is the sum of the merged
resources' points that share the same attributes (for `process.cpu.time`, one point
per `state`). Resources that match no pattern, or have no `process.executable.name`,
pass through unchanged.

```yaml
processors:
  kthread:
    patterns: [kworker*, ksoftirqd*, migration*]   # defaults
    aggregate_name: kernel_threads                  # default
    kernel_parent: true                             # default

service:
  pipelines:
    metrics:
      processors: [memory_limiter, kthread, filter/process, batch]
```

`*` matches any run of characters, `/` included, so `kworker*` matches
`kworker/0:1H-kblockd`; `?` matches one character. A user process whose name matches
a pattern is merged as well.

With `kernel_parent` (default `true`) the processor also merges a resource that has no
`process.command_line` and no `process.executable.path` (empty or absent) and whose
`process.parent_pid` is 2, or whose `process.pid` is 2 with a `process.parent_pid` of 0
or none (`kthreadd` itself). Those are the threads `kthreadd` starts, such as
`rcu_preempt`, `khugepaged`, `jbd2/sda1-8`, `scsi_eh_0` and the ZFS `z_*` threads. The
rule applies only when `os.type` is absent or `linux`. An absent command line alone is
no proof, because operators switch it off, so a process that has either attribute stays
unchanged, and so does a pid 2 with a live parent (a container's own pid 2). A pid
counts only as an integer or the plain decimal string `2` (`+2`, `02` and ` 2` do not).
With a configuration that removes both `process.command_line` and
`process.executable.path`, a child of a container's pid 2 looks like a kernel thread;
set `kernel_parent: false` there. With `kernel_parent` on, `patterns` may be empty.
Set `kernel_parent: false` to merge by name patterns only, as before. The kernel keeps
the first 15 bytes of a process name, so `rcu_tasks_rude_kthread` arrives as
`rcu_tasks_rude_`. A pattern without a wildcard in its first 15 bytes also matches that
cut name, but only for a resource that has no command line and no executable path, so a
real process named like the first 15 bytes of a longer pattern is not merged. A pattern
whose first 15 bytes end inside a multibyte character has no cut name. The aggregate resource has `process.executable.name`
set to `aggregate_name`. It has no per-process attributes (`process.pid`,
`process.parent_pid`, `process.command`, `process.command_line`,
`process.executable.path`), and keeps any other attribute that all merged resources
share, and the resource schema URL when they all agree. Put the processor before
filters that look at process metrics, so the aggregate counts every kernel thread,
and before `batch`.

The aggregate is a sum over the threads present in each scrape. Points are summed
only when they share attributes and belong to the same scrape. The process scraper
stamps each process when it reads it, so one scrape spans some milliseconds;
timestamps less than one second apart count as one scrape, and the aggregate point
takes the latest of them. A batch that holds two scrapes gives two points per
attribute set, one per scrape; scrapes closer than one second cannot be told
apart. A point flagged
as having no recorded value is skipped, not summed. A cumulative counter such as
`process.cpu.time` falls when a thread exits between two scrapes, and the start time
of the aggregate point (the earliest start among the threads present) moves forward
at the same moment. The processor writes such a cumulative sum as non-monotonic so a
consumer does not read the fall as a counter reset; a consumer that treats a start
time change as a reset sees one reset at each thread exit, so do not `rate()` the
kernel-thread total across an exit. Sums and gauges are merged; histogram and summary
metrics of a merged resource are left out. `process.uptime` is not merged, since a
total of process ages has no meaning and would distort ratios computed afterwards.
Two hosts in one batch merge into one aggregate, so run the processor per host
scraper.

A pattern made only of `*` and `?` (such as `*`) matches every process name, and all
process metrics merge into one resource. The processor accepts it and logs a warning
at start.

### Process lifetime metrics

The `processlifetime` processor is in the Linux and static Linux edge builds. The
process scraper already reports lifetime totals: `process.cpu.time` is cumulative
CPU seconds and `process.disk.io` is cumulative bytes read and written. The
processor adds the two ratios that need the age of the process:

| Metric | Unit | Value |
|---|---|---|
| `process.cpu.lifetime.utilization` | `1` | `process.cpu.time` summed over all states, divided by `process.uptime` |
| `process.disk.io.lifetime.rate` | `By/s` | `process.disk.io` per `direction`, divided by `process.uptime` |

Both are gauges added to the scope that holds the source metric. The source
metrics are not changed. A resource without a `process.uptime` gauge, or with a
value of zero or less, gets neither ratio, and so does a source that is not a
cumulative sum. A second pass adds nothing. It has no settings.

The role must enable all three metrics on the host metrics process scraper:
`process.cpu.time`, `process.disk.io` and `process.uptime`. `process.uptime` is off
by default, and without it the processor does nothing.

```yaml
receivers:
  host_metrics:
    scrapers:
      process:
        metrics:
          process.disk.io:
            enabled: true
          process.uptime:
            enabled: true

processors:
  processlifetime: {}

service:
  pipelines:
    metrics:
      processors: [memory_limiter, processlifetime, kthread, batch]
```

Put `processlifetime` before `kthread`: each kernel thread then gets its own ratio,
and `kthread` sums them into one aggregate ratio per host.

### ZFS pool and ARC metrics

The `zfskstat` receiver reads the OpenZFS kstats in process, with no external
command and no privilege beyond read access. Per pool it reports I/O bytes and
operations by direction, the time the wait and run queues were busy (in seconds, not a
latency), queue lengths and the pool state; it also reports the ARC size, hits and
misses. With `datasets: true` it adds I/O bytes, operations and unlinks per dataset, and
by default it reports the newest closed transaction group of each pool (`zfs.pool.txg.*`).
On Linux and FreeBSD it also reads pool and dataset space (size, allocated, free,
fragmentation, health, quota, referenced and more) through read-only ioctls on `/dev/zfs`
in the collector process, with no `zfs` or `zpool` command; the series match `zpool list`
and `zfs list`. The pool series are always on, the dataset series follow `dataset_include`.
The metric names, types and units are in `docs/otel-metrics.md`, which also maps each
series of `zfs_exporter` 2.3.4 to its receiver series.

```yaml
receivers:
  zfskstat:
    collection_interval: 60s              # default
    source: auto                          # auto | proc | sysctl; auto is sysctl on FreeBSD, proc elsewhere
    kstat_path: /proc/spl/kstat/zfs       # default; proc source only
    pools: []                             # empty means every pool
    exclude_pools: []
    arcstats: true                        # default
    datasets: false                       # default; true adds zfs.dataset.io.* and zfs.dataset.unlinks
    dataset_include: []                   # globs on <pool>/<dataset>; empty reads every dataset of the I/O series and none of the space series
    dataset_exclude: []                   # globs on <pool>/<dataset>, applied after the include list
    ioctl_device: /dev/zfs                # default; read-only ioctls for the space series; empty turns them off
    max_datasets: 100                     # default; datasets above it are dropped in sorted order, 0 is no cap
    txg: true                             # default; false turns off zfs.pool.txg.*

service:
  pipelines:
    metrics:
      receivers: [host_metrics, zfskstat]
```

The `proc` source reads the files under `kstat_path`. The `sysctl` source reads the
`kstat.zfs` sysctl tree and ignores `kstat_path`. FreeBSD 13 has no per-pool `io`
kstat, so the `sysctl` source reports no `zfs.pool.io.*` metrics there; the pool state,
the ARC metrics and the dataset metrics remain. OpenZFS 2.1 and later have no
pool-level I/O counters on any system: `zfs.pool.io.*` appears only where the legacy
`io` file exists (OpenZFS 2.0 and older), and a pool without it still reports
`zfs.pool.state`, the ARC metrics and, with `datasets`, the dataset metrics. Transaction
group metrics for these hosts come in a later release. On a host without ZFS the path or the
sysctl tree does not exist. The receiver starts, logs one info line and sends nothing,
then looks again at each interval, so a module loaded later is picked up. A sysctl
error other than not found logs one warning until a read succeeds again. In a
container, mount the host `/proc/spl` and point `kstat_path` at it. Counters restart
from zero when a pool is imported again. The sums start at host boot, read from
`/proc/uptime` (`kern.boottime` on FreeBSD), or at the time the collector started when
that cannot be read.

The dataset globs follow the Go `path.Match` rules: `*` does not match `/`, so `tank/*`
selects the children of `tank` and `tank/*/*` the next level. A dataset is read only
while it is mounted or open, because ZFS keeps its kstat only then. The include and
exclude lists run before the `max_datasets` cap. The three `zfs.pool.txg.*` gauges come
from the newest closed row of the pool's `txgs` kstat; the kernel fills that file only
while the module parameter `zfs_txg_history` is above 0, and a pool with no closed row
or no `txgs` file gets no txg series and one info line. FreeBSD 13 may not expose `txgs`
through sysctl; this was not checked on a host.

On FreeBSD the receiver reads the mount of each pool's root dataset with `getfsstat`
and reports its used and available bytes as `zfs.dataset.space.used` and `.available`.
These are the sizes of the root dataset, which `statfs` reports for the whole pool tree
without the parity and metadata overhead that `zpool list` counts in SIZE, ALLOC and
FREE, so they do not equal those columns. Pool fragmentation and per-vdev error counts
are not available: they need the libzfs interface, which the receiver does not use. A
pool whose root dataset is not mounted gets no space series and one debug line, and
Linux hosts get no `zfs.dataset.space.*` series.
The Windows edge, the gateway and the scrape collector do not include the receiver.

### Hardware temperature, fan and voltage sensors

The `hwmon` receiver is in the Linux and static Linux edge builds. It reports the
sensors of the host with the `hw.*` names of the OpenTelemetry semantic conventions
(`hw.temperature`, `hw.temperature.limit`, `hw.fan.speed`, `hw.voltage`). It reads
`/sys/class/hwmon` and the thermal zones in `/sys/class/thermal` in process, with no
external command and no privilege beyond read access. Names, units and attributes are
in `docs/otel-metrics.md`.

```yaml
receivers:
  hwmon:
    collection_interval: 60s          # default, at least 1s
    hwmon_path: /sys/class/hwmon      # default; thermal zones are read from ../thermal
    include: []                       # globs on "chip:label"; empty means every sensor
    exclude: []
    limits: true                      # default: max, crit, min and lcrit of each temperature
    fans: true                        # default
    voltages: true                    # default
    thermal_zones: true               # default
    cpu_cores: false                  # default: no per-core CPU temperatures

service:
  pipelines:
    metrics:
      receivers: [host_metrics, hwmon]
```

Per-core CPU temperatures (the `Core <N>` sensors of the `coretemp` chip) are off by
default because they are most of the series; the package sensor stays. Set
`cpu_cores: true` for them. A sensor that returns an error, as a sleeping device does,
is skipped with a debug line and the others are still sent. A VM usually has few or no
sensors: with none the receiver starts, logs one info line and sends nothing, and looks
again at each interval. In a container, mount the host `/sys` and point `hwmon_path` at
`<mount>/class/hwmon`.

On FreeBSD the receiver reads the sysctls `dev.cpu.<N>.temperature` (only with
`cpu_cores: true`) and `hw.acpi.thermal.tz<N>.temperature` with its `_CRT`, `_HOT` and
`_PSV` trip points, and sends no fan or voltage series. The Windows edge, the gateway
and the scrape collector do not include the receiver.

### iSCSI initiator sessions

The `iscsi` receiver is in the Linux and static Linux edge builds. It reports the iSCSI
sessions of an initiator host from `/sys/class/iscsi_session` and
`/sys/class/iscsi_connection`: a session gauge per session (with its state, target,
portal and portal group tag), a count and a gauge per connection. With `luns` on (the
default) it also reports each LUN as `hw.status`, `hw.logical_disk.limit` and
`iscsi.lun.state`, from `/sys/class/scsi_device` and the block device of each path:
`iscsi.lun.state` exists for every LUN, `hw.status` only for a LUN whose SCSI state is
known (a LUN with no block device or an unreadable `state` file has state `unknown`), and
`hw.logical_disk.limit` only for a LUN with a block device whose size can be read. It
opens only the files `targetname`, `tpgt`, `state`, `persistent_address`,
`persistent_port`, `type` and `size` and never a credential file such as `password`.

```yaml
receivers:
  iscsi:
    collection_interval: 60s          # default
    sysfs_path: /sys/class            # default
    luns: true                        # default; false keeps the session series only
    max_luns: 256                     # default; at most 4096; 7 points per LUN

service:
  pipelines:
    metrics:
      receivers: [iscsi]
```

A host with no iSCSI sessions has no session directory: the receiver starts, logs one
info line and sends nothing, and looks again at each interval. A session that is
`FAILED` or `FREE` is reported through the `state` attribute, so an alert rule should
test `state != "LOGGED_IN"` or a missing `LOGGED_IN` series. A LUN that cannot be read has
state `unknown` in `iscsi.lun.state`, no `hw.status` point and one warning; with multipath
each path is reported under its own device, never the `dm-N` device. Beyond `max_luns` the
rest are left out with one info line. See `docs/otel-metrics.md`. The Windows edge, the
gateway and the scrape collector do not include the receiver, and the receiver cannot
be created off Linux.

### Hardware inventory

The `inventory` receiver is in the Linux, static Linux and Windows edge builds. Once a day it reports the hardware
of a physical host as info gauges (value 1, the facts as attributes): CPU, total RAM, memory modules, board, BIOS,
system, drives and GPU, plus `inventory.collection.status` for each source. It reads `/proc`, `/sys` and the SMBIOS table
on Linux, and the SMBIOS firmware table, the registry and the physical drives on Windows, in process, with no
external command. The series go to the existing gauge table. Names and attributes are in `docs/otel-metrics.md`.

```yaml
receivers:
  inventory:
    collection_interval: 24h          # default; collects once at start, then at each interval
    max_drives: 64                    # default; at most 1024

service:
  pipelines:
    metrics:
      receivers: [inventory]
```

A virtual machine or a container (Docker, Podman, LXD, systemd-nspawn, Kubernetes) reports nothing and logs one info
line; a Xen control domain (`initdomain` in `/sys/hypervisor/start_flags`) counts as physical; an ARM guest booted through ACPI has no device tree and counts as physical. The video controller of a BMC (ASPEED, Matrox G200e) is not reported as
a GPU. A fact the host does not give is left out;
firmware placeholders such as `To be filled by O.E.M.` count as absent. The SMBIOS table and the serial number files
need root on Linux; without them those facts are left out and `inventory.collection.status` shows `0`. Serial numbers
are sent as read and are sensitive. Queries on these series need a window of at least 25 h, because the series
arrive once a day. There is no uptime series. The first version does not cover network cards, power supplies or
firmware versions. The `ipmi` receiver of the scrape image adds `inventory.bmc.fru.info` from the BMC FRU, once
per `fru_interval` (default `24h`); FRU devices on other controllers, read by words, or holding JEDEC SPD are not
read and do not make the status `0`. `test/configs/edge-inventory.yaml` is a sample.

### MegaRAID controllers

The `megaraid` receiver is in the Linux and static Linux edge builds. It reads LSI MegaRAID
controllers (Dell PERC, Supermicro LSI cards) through `/dev/megaraid_sas_ioctl_node` and reports
controller, logical drive, physical drive and battery state as `hw.*` and `raid.*` metrics, and the
controller event log as logs. Only read-only commands are sent from a fixed list, with no `megacli`
or `storcli`, and nothing on the controller changes.

```yaml
extensions:
  file_storage/megaraid:
    directory: /var/lib/otelcol/megaraid

receivers:
  megaraid:
    collection_interval: 60s         # default
    smart: false                     # default; true reads SATA drives through the controller
    events:
      enabled: true                  # default
      storage: file_storage/megaraid # keeps the last read event
      min_class: info                # default

service:
  extensions: [file_storage/megaraid]
  pipelines:
    metrics:
      receivers: [megaraid]
    logs:
      receivers: [megaraid]
```

It needs file access to the node (owner, group and mode) and `CAP_SYS_ADMIN`: root has both, a
non-root user needs both (in a container, the device node and the capability). Other tools
that use the node queue behind a scrape. The 2208 and 3108 controllers are the declared generations;
others are read best-effort. The tests ran on buffers built from the driver headers, not on a
controller; the metric table and what is unverified are in `docs/otel-metrics.md`.

### KSM (Kernel Samepage Merging)

The `ksm` receiver reads `/sys/kernel/mm/ksm` in process and reports the page counters of KSM
(`linux.ksm.stats.pages_shared`, `pages_sharing`, `pages_unshared`, `pages_volatile`,
`stable_node_chains`, `stable_node_dups`) and the `full_scans` sum. It is in the Linux and static
Linux edge builds; list it on PVE hypervisors only. It opens seven files read-only and runs no
command. The names, units and the sample config `test/configs/edge-ksm.yaml` are in
`docs/otel-metrics.md`.

```yaml
receivers:
  ksm:
    collection_interval: 60s       # default
    ksm_path: /sys/kernel/mm/ksm   # default
```

### Disk health (SMART)

The `smart` receiver is in the Linux and static Linux edge builds. It reads the health of NVMe and
SATA disks from the collector process and emits `hw.*` metrics: `hw.status`, `hw.temperature`,
`hw.errors`, `hw.physical_disk.endurance_utilization`, `hw.physical_disk.size`, power-on time, power
cycles, data moved, and the SATA attribute table as `hw.physical_disk.smart` with the attribute
`hw.physical_disk.smart_value_type`. It sends `SG_IO` and NVMe admin ioctls itself, one disk at a
time, and never runs `smartctl`.

```yaml
receivers:
  smart:
    collection_interval: 5m          # default
    attributes: default              # default (20 ids) | all | [5, 9, 194]
    value_types: [normalized, raw]   # default; worst and threshold are opt-in

service:
  pipelines:
    metrics:
      receivers: [smart]
```

It needs root, or a user that can read the device files (the `disk` group for SATA, an ACL or udev rule
for `/dev/nvmeN`) and holds `CAP_SYS_ADMIN` (NVMe) and `CAP_SYS_RAWIO` (SATA), and a unit file that leaves
`PrivateDevices` unset or allows `block-sd` and `char-nvme`. A disk that cannot be opened gives one error
that asks for read access, a command the kernel refuses gives one error that names the capability, and
the others are still read. The commands are read only,
SMART is never switched on, and disks behind a RAID controller are skipped. The metric table, the
series budget and the failure modes are in `docs/smart.md`.

### Dell iDRAC health and sensors

The `idrac` receiver is in the Linux and static Linux edge builds. It reads a Dell iDRAC through the Redfish API and
reports health, temperatures, fans, voltages, power, drives, memory and processors with the `hw.*` names of the
OpenTelemetry semantic conventions (see `docs/otel-metrics.md`). It logs in with a session token (one `POST` to
`/redfish/v1/SessionService/Sessions`, then `X-Auth-Token` on each request), logs in again once after a 401, and deletes
the session at shutdown. `test/configs/edge-idrac.yaml` is a sample.

```yaml
receivers:
  idrac:
    collection_interval: 60s
    max_concurrent: 8                   # targets read at once
    targets:
      - name: db-1                          # host.name of the resource
        address: https://192.0.2.5
        username: ${env:IDRAC_USERNAME}
        password: ${file:/etc/otelcol/idrac.password}
        ca_file: /etc/otelcol/idrac-ca.pem  # pins the iDRAC CA
        insecure_skip_verify: false         # default
        resources: [system, thermal, power, storage, memory, processors, bmc]   # default: all
        request_timeout: 10s                # default
        scrape_timeout: 30s                 # default
        storage_interval: 5m                # default; 5m, or collection_interval if that is longer; not below it
        storage_max_drives: 256             # default; most drives read in one storage read
```

Use an iDRAC account with a read-only role: the Login and ReadOnly privileges and nothing else. The password comes from
`${env:...}` or `${file:...}`; the receiver stores it as an opaque string and writes it, and the session token, in no
log. Certificate verification is on: a certificate that does not verify gives `hw.bmc.up` 0 with reason `tls`, and the
receiver never retries without verification. Set `ca_file` to the iDRAC CA. A Redfish path the iDRAC answers 404 for
is marked absent, logs one info line and is asked again after 6 hours. The legacy `Thermal` and `Power` are read first,
because they carry the temperature limits, the voltages and the host power; `ThermalSubsystem` and `PowerSubsystem` are
the fallback for firmware that no longer serves them, and give readings, fans and supplies without those three. A
BMC that answers 400 or 501 to `$expand` is read member by member for 6 hours, then asked again. One drive or member that
answers an error is skipped with one warning until a scrape without one, and `otelcol_hw_bmc_skipped_reads` counts each
skip by target and part (the scrape still reports `hw.bmc.up` 1); a timeout, a login or a TLS failure still ends the scrape.
A scrape makes about 10 requests; `max_concurrent` (default 8) limits the targets read at once. After a failed login the iDRAC is not
contacted for 15 minutes, then 30, doubling to 6 hours, and after three in a row not again until restart. The Windows
edge, the FreeBSD edge, the gateway and the scrape collector do not include the receiver.

Storage controllers, volumes and drives are read apart from the other resources, on their own timer every
`storage_interval` (default 5m; it may equal `collection_interval` and may not be below it). The base scrape takes
about 3.8 s on the iDRAC this was measured on, and a slow storage read does not delay it or take a `max_concurrent`
slot of a base scrape. The storage read sends
`hw.status` for the storage rollup, each controller, volume and drive, the drive size and life left, the
controller speed, the volume capacity and span count, the drive count of each storage, the locate LED and the
predicted-failure flag of each drive, with the same names as the other hardware receivers (see
`docs/otel-metrics.md`). A drive state follows `Status.Health` only; `hw.physical_disk.predicted_failure` carries the
flag. Every Redfish read of the storage resources is a `GET` on a path under the system's `Storage`
resource; a link that points elsewhere is not requested. A path that fails (401, 403, 404, 5xx, a timeout, a body
that is not JSON or a field of the wrong type) is skipped, counted in `hw.bmc.scrape.errors` by path group (all four
groups are sent from the first read, at 0), and
logged once until a read without a failure; the other storage paths and the base scrape go on, and `hw.bmc.up` is
unchanged. A path below the `Storage` collection that answers 404 is asked again at the next read; the collection
itself is asked again after an hour. A storage read has `scrape_timeout` plus 1 second for each drive it finds, up
to 10 minutes; one that runs out of time sends what it read and counts one error. `storage_max_drives` (default 256)
limits the drives read in one storage read; past it the rest are skipped with one warning and one count.
Drive serial numbers are resource attributes of the drive series and are not written to any log.

### OpenVPN client traffic

The `openvpnstatus` receiver is in the Linux and static Linux edge builds. It reads the
status file an OpenVPN server writes and reports, for each connected client, the bytes
received and sent and the time the session started:

```yaml
receivers:
  openvpnstatus:
    path: /var/log/openvpn/status.log
```

The metrics are `openvpn.client.bytes_received` and `openvpn.client.bytes_sent`
(cumulative sums in bytes, starting at the session start) and
`openvpn.client.connected_since` (a gauge of Unix seconds), each with the attributes
`common_name` and `real_address`. `real_address` is the client's IP address without
the port. The receiver emits the signal of each pipeline it is in, so a 0.3.13 config with it
in a logs pipeline keeps emitting log records, and a metrics pipeline gets the metrics.
The `output` key (`metrics`, `logs` or `both`) restricts it. Use status version 2 or 3
for metrics. See `docs/openvpnstatus.md`.

### Pod discovery on the Linux edge

The Linux and static Linux edge builds can start receivers for the pods of their own
node, for a DaemonSet. `k8s_observer` lists the pods of one node, and `receiver_creator`
starts a receiver for each pod that a rule in the config matches. One rule scrapes
pods that carry `prometheus.io/scrape: "true"` with the `prometheus` receiver; the
reference config is `test/configs/edge-k8s-discovery.yaml`. The edge also compiles in
the `redis`, `postgresql`, `mysql`, `mongodb`, `rabbitmq` and `nginx` receivers for
rules on those pods (`test/configs/edge-k8s-apps.yaml`), with credentials from
environment variables or a mounted file. `memcached` and `kafka_metrics` are not in
the build.

Annotation discovery stays off: only rules in the config start receivers, and a guard
fails a sample config that turns it on. The creator puts `k8s.pod.uid`, `k8s.pod.name`
and `k8s.namespace.name` on what the receivers produce; `k8s_attributes` after the
creator adds labels and annotations. Each matching pod gets its own receiver, and a
new `receiver` label value on the receiver self-metrics after every pod restart. A
receiver that fails to start is logged and the collector keeps running. The gateway,
the scrape collector and the Windows edge are unchanged. See `docs/k8s-discovery.md`
for the RBAC, the failure table and the size of each receiver.

### Custom (in-tree)

**Processors** (`processor/`, 37 total — 35 are independently-versioned nested Go
modules with their own `go.mod`; `ipsecprocessor` and `logrusprocessor` are part of
the root module):
assetdbprocessor, charonlogprocessor, ciscologprocessor, containerdlogprocessor,
containernameprocessor, cronlogprocessor, filterlogprocessor, healthprobesampler, identityfillprocessor,
ipextractorprocessor, ipsecprocessor, journaldprocessor, k8seventprocessor,
kafkalogprocessor, kernellogprocessor, klogprocessor, kthreadprocessor, logrecombineprocessor,
logruslogprocessor, logrusprocessor, macextractorprocessor, nginxlogprocessor,
normalizedloadprocessor, pgbouncerlogprocessor, postgreslogprocessor,
processlifetimeprocessor, redislogprocessor, resourceversionprocessor, sentrylogprocessor,
snoozelogprocessor, snubalogprocessor, sourcetagprocessor, syslogprocessor, tektonlogprocessor,
tightvncprocessor, uvicornlogprocessor, windowhistprocessor, winlogprocessor

Not every processor ships in every build: the builder manifests list which ones
each profile includes. `journaldprocessor`, `snoozelogprocessor`,
`healthprobesampler`, `winlogprocessor` and `windowhistprocessor` are in the tree
from 0.3.1 and join the manifests in later releases.

**Receivers** (`receiver/`): `ciscologreceiver` (root module, parses Cisco device
logs), `psireceiver` (own module, Pressure Stall Information metrics from
`/proc/pressure/`), `ethtoolreceiver` (own module, AWS ENA allowance counters by ioctl,
Linux edge only), `pvereceiver` (own module, Proxmox VE cluster resources and guest agent facts,
Linux edge only), `gpureceiver` (own module, NVIDIA GPU metrics through NVML),
`fswatchreceiver` (own module, one log record per new file, Windows edge only),
`zfskstatreceiver` (own module, ZFS pool I/O, pool state, ARC and dataset metrics, Linux and FreeBSD edge),

`zfskstatreceiver` (own module, ZFS pool I/O, pool state and ARC metrics, Linux edge only),
`hwmonreceiver` (own module, temperature, fan and voltage sensors, Linux edge; FreeBSD by build tag),
`iscsireceiver` (own module, iSCSI initiator sessions from sysfs, Linux edge only),
`idracreceiver` (own module, Dell iDRAC health and sensors through Redfish, Linux edge only),
`inventoryreceiver` (own module, daily hardware facts as info gauges, Linux and Windows edge),
`ipmireceiver` (own module, BMC sensors and FRU inventory over IPMI, scrape image only),
`hwschema` (own module, the `hw.*` vocabulary and metric builder of the two BMC receivers),
`megaraidreceiver` (own module, MegaRAID status and events through the megaraid_sas ioctl node, Linux edge only),
`openvpnstatusreceiver` (own module, OpenVPN client traffic from the status file, Linux edge only),
`cloudwatchmetricsreceiver` (own module, CloudWatch metrics for many accounts, regions and tiers, scrape image only),
`auth0receiver` (own module, Auth0 events from SQS; in the tree from 0.3.1 and
joins the scrape build in a later release)

**Stanza transformer operators** (`stanza/operator/transformer/`): `ansistrip`,
`arraytostring`

## Development

Standard Go workflow from the module root:

```bash
go build ./...
go test ./...
```

Several processors live in independently-versioned nested Go modules with
their own `go.mod` (see [Components](#components) below) — run `go test ./...`
from within a nested module's directory to test it in isolation.

## Observability

See [Metrics and Runtime Environment](docs/otel-metrics.md) for the metrics this
distribution emits and the runtime environment variables it reads.

## Documentation

- [Windows edge collector](docs/windows-edge.md)
- [FreeBSD edge collector](docs/freebsd-edge.md)
- [Metrics and runtime environment](docs/otel-metrics.md)
- [otel-cluster on the gateway image](docs/k8s-cluster.md)
- [Scrape collector](docs/scrape-collector.md)
- [SNMP and NetFlow on the Linux edge](docs/snmp-netflow.md)

- [Docker logs and metrics on journald-driver hosts](docs/docker-multiline.md)

## Notes on Project Conventions

A few choices here are intentional properties of building on OCB (OpenTelemetry
Collector Builder), not gaps to be closed:

- **testify for test assertions** — testify is the de facto standard across the
  OpenTelemetry Collector ecosystem, and the upstream components this
  distribution embeds already use it, so tests here follow the same convention
  rather than introducing a second one.
- **No conventional CLI flag surface** — the CLI is generated by OCB, which
  imposes its own Cobra command structure. The entrypoint is not hand-written,
  so it doesn't expose a hand-rolled flag set.
- **Build output path is fixed by OCB** — the builder controls where it emits
  binaries; this isn't independently configurable.
- **No hand-written service scaffolding** — this is an OCB distribution rather
  than a hand-rolled Go service, so there's no separate configuration loader or
  service lifecycle file; the collector framework owns both.
