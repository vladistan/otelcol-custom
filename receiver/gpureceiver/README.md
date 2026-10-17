<!-- Modified by Vlad Korolev, 2026-10: rewritten for semconv metrics, lazy NVML init and the otelcol-edge build.
Original: https://github.com/pmady/otel-gpu-receiver@ac756935ca040923d98af5a0d0547dca02a3b23b/README.md -->

# GPU receiver (`gpu`)

Collects NVIDIA GPU metrics through NVML and emits them with OpenTelemetry
semantic-convention names. It is part of `otelcol-edge` only. Status: alpha.

The receiver is functional in Linux builds with cgo. Elsewhere the `gpu`
component is registered as a stub: a config that puts it in a pipeline fails
startup with `platform unsupported`.

## Provenance

This module is a modified copy of an upstream project, used under the
Apache License 2.0.

- Upstream: https://github.com/pmady/otel-gpu-receiver
- Upstream commit: `ac756935ca040923d98af5a0d0547dca02a3b23b` (committed 2026-05-19)
- Vendored: 2026-10-15, first as an unchanged copy (commit `8f4614a`), then moved here and changed.
- Licence: `LICENSE` is the upstream file, unchanged. Upstream ships no NOTICE file.
- Contrib check: no NVML or DCGM receiver exists in opentelemetry-collector-contrib at v0.140.0 (checked 2026-10-14). A DCGM receiver would also need the `nv-hostengine` daemon.
- Base choice: pmady, pinned to the commit above. Its code was ported down from collector v0.147 to v0.140.0, and its metric layer was replaced.

Every file below carries a `Modified by` and an `Original:` line in its first
lines. `scripts/check-change-notices.sh` checks this table against those lines.

| Upstream file | Module file |
|---|---|
| `config.go` | `config.go` |
| `config_test.go` | `config_test.go` |
| `doc.go` | `doc.go` |
| `factory.go` | `factory.go` |
| `factory_test.go` | `factory_test.go` |
| `metadata.go` | `metadata.go` |
| `scraper.go` | `scraper.go` |
| `scraper_test.go` | `scraper_test.go` |
| `internal/nvml/real.go` | `internal/nvmladapter/adapter_linux_cgo.go` |
| `README.md` | `README.md` |

Not carried over: `go.mod`, `go.sum`, `internal/nvml/nvml.go`,
`internal/nvml/mock.go`, `Makefile`, `.golangci.yml`, `.gitignore`,
`CODE_OF_CONDUCT.md`, `CONTRIBUTING.md`, `SECURITY.md`, `testdata/config.yaml`
and `.github/`.

Upstream reads only `UtilizationRates.Gpu` (`internal/nvml/real.go`), so it
never emitted memory utilization from NVML. This module computes
`hw.gpu.memory.utilization` as used memory divided by total memory.

## Metrics

Semantic conventions are pinned to v1.44.0. The three extension metrics are
not in semconv; a proposal for them is drafted separately and not yet posted.
Every data point carries `hw.id` (GPU UUID), `hw.name` (`gpu<index>`),
`hw.model`, `hw.type=gpu`, `hw.vendor=nvidia` and `hw.driver_version`, except
`hw.temperature`.

| Metric | Instrument | Unit | Extra attributes | Class |
|---|---|---|---|---|
| `hw.gpu.utilization` | gauge | `1` | `hw.gpu.task` = general, encoder, decoder | semconv |
| `hw.gpu.memory.usage` | up-down counter | `By` | none | semconv |
| `hw.gpu.memory.limit` | up-down counter | `By` | none | semconv |
| `hw.gpu.memory.utilization` | gauge | `1` | none | semconv |
| `hw.gpu.io` | counter | `By` | `network.io.direction` = receive, transmit | semconv |
| `hw.errors` | counter | `{error}` | `error.type` = corrected, uncorrected | semconv |
| `hw.status` | up-down counter | `1` | `hw.state` = ok, degraded, failed | semconv |
| `hw.temperature` | gauge | `Cel` | sensor attributes, see below | semconv |
| `hw.power` | gauge | `W` | none | semconv |
| `hw.gpu.clock` | gauge | `Hz` | `hw.gpu.clock.domain` = graphics, sm, memory, video | extension, not semconv |
| `hw.gpu.power.limit` | gauge | `W` | none | extension, not semconv |
| `hw.gpu.pcie.throughput` | gauge | `By/s` | `network.io.direction` | extension, not semconv |

## Configuration

Every metric is enabled by default. Keys under `metrics` are the metric names
above; an unknown key fails config validation. A disabled metric skips its
NVML call.

```yaml
receivers:
  gpu:
    collection_interval: 10s
    metrics:
      hw.gpu.pcie.throughput:
        enabled: false

service:
  pipelines:
    metrics:
      receivers: [hostmetrics, gpu]
      processors: [resourcedetection, batch]
      exporters: [otlp]
```

Put `gpu` in a pipeline that runs `resourcedetection`, which adds the host
identity to the resource. The receiver itself sets no resource attributes.

## Operator notes

- **`hw.gpu.io` is approximate.** NVML reports a sampled PCIe rate in KiB/s (1 KiB = 1024 bytes), not a byte counter. The receiver multiplies each sample by the measured time since the previous sample and sums it. The first sample emits 0. A gap longer than three collection intervals adds nothing and starts a new baseline.
- **ECC counters** are the volatile NVML counters, emitted as the change since the first sample. If the raw value drops (driver reload), the new value is added so the series keeps rising.
- **MIG** instances are not reported. Metrics are for the physical GPU only.
- **Restart after a driver load.** NVML is opened once, on the first scrape. If it fails (no library, no driver, no permission, version mismatch), the receiver logs one warning and returns nothing until the collector restarts. A GPU that is lost stays lost until restart.
- **First scrape** includes the NVML initialisation, which can take seconds without persistence mode. Later scrapes do no initialisation.
- **Two `gpu` instances** are not supported. Each would emit the same series.
- **`hw.temperature`** points carry only `hw.id` (`<uuid>:temperature`), `hw.parent` (the GPU UUID), `hw.name` (`gpu<index> temperature`) and `hw.sensor_location=gpu`. Join to the GPU attributes through `hw.parent`.
- **`hw.status`** is ok=1 when at least one enabled call succeeded and none reported a lost GPU, failed=1 for a lost GPU, and degraded is always 0. All three at 0 means the state is unknown for that scrape.
- **Errors** appear in the collector log and in `hw.status`. The collector's own `errored_metric_points` stays 0 by design.
- **glibc 2.35 floor.** Linux builds use cgo and link glibc dynamically, so the edge binary needs glibc 2.35 or newer. musl systems are not supported. NVML itself is loaded at run time and is not linked.

## Development

```bash
go test ./...
```

Tests run without a GPU against `internal/fakeport`. The swap suite in
`internal/collect` runs the same scenarios over a list of port constructors,
so the real NVML adapter can be added as a second case on a GPU host.
