# Semconv proposal drafts for GPU metrics

These files are drafts for the OpenTelemetry semantic conventions repository.
They are not posted. They wait for the otelcol-edge 0.2.0 release, so the
issue can link a working prototype (the `gpu` receiver in
`receiver/gpureceiver`).

| File | Content |
|---|---|
| `issue.md` | Issue text: the gap, prior art, open questions |
| `gpu-metrics.snippet.yaml` | Additions to `model/hardware/gpu-metrics.yaml` and the registry |
| `chloggen-entry.yaml` | Changelog entry for the semconv repository |

The receiver emits three of the metrics proposed here, as extensions outside
semconv v1.44.0: `hw.gpu.clock` (Hz), `hw.gpu.power.limit` (W) and
`hw.gpu.pcie.throughput` (By/s). `hw.gpu.clock.limit` is proposed but not
emitted. Naming follows the `hw.cpu.speed` precedent.

Before posting, replace the two TODOs in `issue.md` (the openlit attributes and
the link to this receiver) and the issue number in `chloggen-entry.yaml`.
