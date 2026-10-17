# Add GPU clock, power limit and PCIe throughput metrics to hw.gpu.*

## Problem
`hw.gpu.*` (model/hardware/gpu-metrics.yaml) has io, memory.{limit,usage,utilization}, utilization, errors, status.
It cannot express three signals every NVIDIA monitoring stack exports:
1. Current clock frequency per domain (graphics, SM, memory, video).
2. Power management limit (watts).
3. PCIe throughput as a rate.

## Already covered (no change proposed)
- Power draw: `hw.power{hw.type=gpu}` (gauge, W) and `hw.energy` (NVML total energy counter, mJ -> J).
- Temperature: `hw.temperature` (Cel) with `hw.sensor_location` and `hw.id`/`hw.parent` linking to the GPU.
- ECC: `hw.gpu.errors` already defines `error.type` = corrected | uncorrected. NVML single-bit ECC maps to corrected, double-bit to uncorrected. Memory location and volatile/aggregate split are not covered (see questions).

## Proposed metrics
| Name | Instrument | Unit | Attributes |
|---|---|---|---|
| `hw.gpu.clock` | gauge | Hz | `hw.gpu.clock.domain` (graphics, sm, memory, video) |
| `hw.gpu.clock.limit` | gauge | Hz | `hw.gpu.clock.domain`, `hw.limit_type` (max, throttled) |
| `hw.gpu.power.limit` | gauge | W | `hw.limit_type` (max, throttled; see questions) |
| `hw.gpu.pcie.throughput` | gauge | By/s | `network.io.direction` (receive, transmit) |

Rationale:
- One clock metric with a domain attribute avoids a metric per domain, like `hw.gpu.utilization` + `hw.gpu.task`.
- Hz matches `hw.cpu.speed`. NVML returns MHz; collectors multiply by 1e6.
- `.limit` + `hw.limit_type` follows `hw.cpu.speed.limit` and `hw.temperature.limit`. Pairing with `hw.power` gives power/limit ratios.
- PCIe: `nvmlDeviceGetPcieThroughput` returns KB/s over a short sample window, not a cumulative count. A counter would force collectors to integrate samples (lossy). A By/s gauge keeps the source semantics; `hw.gpu.io` (counter, By) stays for cumulative sources.

## Prior art
NVML:
- `nvmlDeviceGetClockInfo(dev, NVML_CLOCK_GRAPHICS|SM|MEM|VIDEO)` (MHz); `nvmlDeviceGetMaxClockInfo` for max.
- `nvmlDeviceGetPowerManagementLimit` (mW); `nvmlDeviceGetEnforcedPowerLimit`.
- `nvmlDeviceGetPcieThroughput(dev, NVML_PCIE_UTIL_TX_BYTES|RX_BYTES)` (KB/s).
- `nvmlDeviceGetTotalEccErrors` (single/double bit).

DCGM exporter fields: `DCGM_FI_DEV_SM_CLOCK`, `DCGM_FI_DEV_MEM_CLOCK`, `DCGM_FI_DEV_POWER_MGMT_LIMIT`, `DCGM_FI_PROF_PCIE_TX_BYTES` / `DCGM_FI_PROF_PCIE_RX_BYTES` (rate), `DCGM_FI_DEV_POWER_USAGE`, `DCGM_FI_DEV_GPU_TEMP`.

Instrumentations: openlit emits `hw.gpu.speed` (TODO: verify exact attributes before posting). Our NVML collector: TODO link. CONTRIBUTING asks for prototypes in instrumentations; link them in the PR.

Related open issues: #940 (GPU mapping problems incl. frequency naming vs `hw.cpu.speed`, power naming), #2438 (hw.memory extension), #1055 (hw.power vs hw.host.power).

## Open questions
1. Name: `hw.gpu.clock` vs `hw.gpu.speed` (mirrors `hw.cpu.speed`; openlit). Base + `.limit` needs a `metric_namespace_collision` policy exception, as `hw.cpu.speed` does.
2. Power limit: generic `hw.power.limit` (with `hw.type`) instead of `hw.gpu.power.limit`?
3. `hw.limit_type` has no `default`/`enforced` members; extend, or use `max`/`throttled`?
4. Does `hw.gpu.io` mean PCIe, NVLink, or both? Add an interconnect attribute?
5. ECC: add memory location / volatile-vs-aggregate (ties to #940 category request)?
