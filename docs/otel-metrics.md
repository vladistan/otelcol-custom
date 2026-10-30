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

### Normalized/denormalized load — `processor/normalizedloadprocessor`

Post-processes standard `hostmetrics` output, adding derived metrics
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

## Resource-attribute enrichment (not new metrics)

Two processors tag existing telemetry (metrics, logs, and traces) with
resource attributes rather than emitting metrics of their own:

- **`processor/resourceversionprocessor`** — sets a resource attribute (key
  configurable, `role`-scoped) to the running collector's version, via
  `SetCollectorVersion` at startup. Applies to all three signal types.
- **`processor/assetdbprocessor`** — enriches telemetry with asset-database
  lookups keyed off a configurable `source_attribute`, backed by a local
  `database_path`.

## Standard `hostmetrics` metrics

Both profiles' local test configs (`test/configs/local-edge.yaml`,
`test/configs/local-gateway.yaml`) scrape standard `hostmetrics` (`cpu`,
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

All three are read once at process start; there is no live-reload. Sentry
itself is only initialized if a non-placeholder DSN is compiled into the
binary and `OTELCOL_CUSTOM_DISABLE_TELEMETRY` is unset.
