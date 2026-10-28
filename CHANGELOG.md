# Changelog

All notable changes to otelcol-custom will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.4] - Unreleased

Sentry crash reports for the Windows service.

## [0.3.3] - Unreleased

Per-window exponential histograms on every edge platform, `k8sevent` parity for `otel-cluster`, and the narrowed `otelcol-scrape` image with the Auth0 receiver.

## [0.3.2] - Unreleased

Searchable Windows event logs; `journald`, `snoozelog` and health-probe processors in the gateway image.

## [0.3.1] - 2026-10-27

Windows edge artifact; `otel-main` runs a gateway image built from this tree.

### Added

- **Windows edge** `otelcol-edge-0.3.1-windows-amd64.exe` (windows/amd64, CGO off, unsigned). It collects host metrics, Windows performance counters, Windows event logs and file logs, installs as a Windows service through the OCB service handler, and writes a rotating self-log (10 MiB, 5 backups). The sample config is `test/configs/windows-edge.yaml`.
- **Event logs are on by default** with the Security channel unfiltered until 0.3.2 (user-accepted risk, SPARC 05 D-45). The service runs as LocalSystem (D-39).
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
| 0.3.1 Windows edge | 130072064 |
| 0.3.1 gateway | 226672800 |

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
