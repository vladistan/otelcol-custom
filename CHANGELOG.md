# Changelog

All notable changes to otelcol-custom will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
