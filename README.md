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

Prebuilt `edge` binaries (Linux and Windows), a `gateway` binary and a gateway
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
- **Receivers**: otlp, host_metrics, journald, tcp_check (edge builds from 0.3.11), prometheus (Linux edge builds from 0.3.12), docker_stats (Linux edge builds from 0.3.14)
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

A receiver that fails to start normally stops the collector. Two receivers are
optional: `docker_stats` and `tcp_check`. If one of them fails to start, for
example because Docker is not running yet, the collector keeps running without
it. The list is fixed in the code and cannot be changed in the configuration.
Every other receiver, `journald` and `hostmetrics` included, still stops the
collector on a failed start. A configuration that cannot build the receiver at
all is still rejected at startup.

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

No other statistic is read, and the receiver has no option for more. An interface
that does not support statistics (a bridge, `veth` or loopback) is skipped without a
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

The `containername` processor is in the Linux and static Linux edge builds. Docker
containers that log through the journald driver reach the collector with
`service.name` set to `docker.service`, the unit that runs the daemon. When a
resource has a non-empty `container.name`, the processor sets `service.name` to that
value, replacing whatever was there. A resource without `container.name`, or with an
empty one, is left as it was, and running the processor twice changes nothing. It has
no settings:

```yaml
processors:
  containername: {}

service:
  pipelines:
    logs:
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

service:
  pipelines:
    metrics:
      processors: [memory_limiter, kthread, filter/process, batch]
```

`*` matches any run of characters, `/` included, so `kworker*` matches
`kworker/0:1H-kblockd`; `?` matches one character. A user process whose name matches
a pattern is merged as well. The aggregate resource has `process.executable.name`
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

The `zfskstat` receiver is in the Linux and static Linux edge builds. It reads the
OpenZFS kstat files in process, with no external command and no privilege beyond
read access. Per pool it reports I/O bytes and operations by direction, the time the
wait and run queues were busy (in seconds, not a latency), queue lengths and the pool
state; it also reports the ARC size, hits and misses. The metric names, types and units are in
`docs/otel-metrics.md`.

```yaml
receivers:
  zfskstat:
    collection_interval: 60s              # default
    kstat_path: /proc/spl/kstat/zfs       # default
    pools: []                             # empty means every pool
    exclude_pools: []
    arcstats: true                        # default

service:
  pipelines:
    metrics:
      receivers: [host_metrics, zfskstat]
```

On a host without the ZFS module the path does not exist. The receiver starts, logs
one info line and sends nothing, then looks again at each interval, so a module
loaded later is picked up. In a container, mount the host `/proc/spl` and point
`kstat_path` at it. Counters restart from zero when a pool is imported again. The
sums start at host boot, read from `/proc/uptime`, or at the time the collector
started when that file cannot be read. Per-dataset counters are not read.
The Windows edge, the gateway and the scrape collector do not include the receiver.

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
Linux edge only), `gpureceiver` (own module, NVIDIA GPU metrics through NVML),
`fswatchreceiver` (own module, one log record per new file, Windows edge only),
`zfskstatreceiver` (own module, ZFS pool I/O, pool state and ARC metrics, Linux edge only),
`openvpnstatusreceiver` (own module, OpenVPN client traffic from the status file, Linux edge only),
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
- [Metrics and runtime environment](docs/otel-metrics.md)
- [otel-cluster on the gateway image](docs/k8s-cluster.md)
- [Scrape collector](docs/scrape-collector.md)

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
