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

The edge, edge-windows, gateway and scrape profiles build with OCB v0.154.0;
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
- **Receivers**: otlp, hostmetrics, journald
- **Processors**: batch, memory_limiter
- **Exporters**: otlp, debug

### Custom (in-tree)

**Processors** (`processor/`, 32 total — 30 are independently-versioned nested Go
modules with their own `go.mod`; `ipsecprocessor` and `logrusprocessor` are part of
the root module):
assetdbprocessor, charonlogprocessor, ciscologprocessor, containerdlogprocessor,
cronlogprocessor, filterlogprocessor, healthprobesampler, ipextractorprocessor,
ipsecprocessor, journaldprocessor, k8seventprocessor, kafkalogprocessor,
kernellogprocessor, klogprocessor, logrecombineprocessor, logruslogprocessor,
logrusprocessor, macextractorprocessor, nginxlogprocessor,
normalizedloadprocessor, pgbouncerlogprocessor, postgreslogprocessor,
redislogprocessor, resourceversionprocessor, sentrylogprocessor,
snoozelogprocessor, snubalogprocessor, syslogprocessor, tektonlogprocessor,
uvicornlogprocessor, windowhistprocessor, winlogprocessor

Not every processor ships in every build: the builder manifests list which ones
each profile includes. `journaldprocessor`, `snoozelogprocessor`,
`healthprobesampler`, `winlogprocessor` and `windowhistprocessor` are in the tree
from 0.3.1 and join the manifests in later releases.

**Receivers** (`receiver/`): `ciscologreceiver` (root module, parses Cisco device
logs), `psireceiver` (own module, Pressure Stall Information metrics from
`/proc/pressure/`), `gpureceiver` (own module, NVIDIA GPU metrics through NVML),
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
