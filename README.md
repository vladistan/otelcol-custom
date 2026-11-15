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
- **Receivers**: otlp, host_metrics, journald, tcp_check (edge builds from 0.3.11), prometheus (Linux edge builds from 0.3.12)
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
the processor.

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
`/proc/pressure/`), `gpureceiver` (own module, NVIDIA GPU metrics through NVML),
`fswatchreceiver` (own module, one log record per new file, Windows edge only),
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
