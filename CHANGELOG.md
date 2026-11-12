# Changelog

All notable changes to otelcol-custom will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.12] - 2026-11-12

The `prometheus` receiver in the Linux edge builds, the `gpu` receiver in the Windows edge build, and a fix for duplicate and noisy Sentry events. The gateway and the scrape image gain no new component.

### Added

- **`prometheus` receiver in the Linux edge builds.** The Linux and static Linux edge collectors now ship `prometheusreceiver` v0.164.1, so an edge host can scrape Prometheus exporters on localhost, such as node_exporter, with the usual `scrape_configs`. The scrape job becomes `service.name` and the target `host:port` becomes `service.instance.id`. `test/edge/prometheus.sh` runs the built edge binary against a local exporter and checks the scraped gauge, its job and instance, and `up`. `test/configs/edge-prometheus.yaml` is the config it runs. See the README.
- **GPU metrics in the Windows edge.** The `gpu` receiver is now in the Windows edge build. It calls `nvml.dll` from the NVIDIA driver through `golang.org/x/sys/windows`, without cgo, and emits the same `hw.gpu.*`, `hw.temperature`, `hw.power` and `hw.errors` series as the Linux edge. The optional `library_path` setting names `nvml.dll` by absolute path when the driver keeps it outside System32; it is rejected on other systems and when relative. `test/configs/windows-edge.yaml` has the receiver and a `metrics/gpu` pipeline commented out, and a `gpu` group in `windowhist`. The adapter's tests run without a GPU. Not run on a host with a GPU yet. See `docs/windows-edge.md`.

### Fixed

- **Sentry noise.** Log lines containing `use of closed network connection` or `failed to find MUI file` no longer become Sentry events. Each ERROR log line was sent twice, by the core wrapper and by a second hook; only the wrapper remains, so each is sent once. The startup event now has a fixed fingerprint and a `server_name` tag, so hosts share one Sentry issue.

### Binary size

| Asset | MiB | Bytes | Change from 0.3.11 |
|---|---|---|---|
| `otelcol-edge-0.3.12-linux-amd64` | 168.5 | 176672930 | +41.5 MiB (+32.7%) |
| `otelcol-edge-0.3.12-linux-amd64-gpu` | 169.8 | 178016328 | +41.5 MiB (+32.4%) |
| `otelcol-edge-0.3.12-windows-amd64.exe` | 130.5 | 136788992 | +96.5 KiB (+0.07%) |

The `prometheus` receiver adds 9 Go modules to each Linux edge build. The Windows build gains the `gpu` receiver.

The gateway gains no component. Its asset size comes from the release build and is not listed here. The scrape collector ships in the image `ghcr.io/vladistan/otelcol-scrape:v0.3.12`; the binary in that image is 147.0 MiB (154136738 bytes), 4 KiB less than in 0.3.11.

## [0.3.11] - 2026-11-09

The `tcp_check` receiver in the edge builds, the `identityfill` processor in the scrape image, and a Sentry DSN override in every binary. The gateway gains no new component.

### Added

- **`OTELCOL_CUSTOM_SENTRY_DSN` overrides the Sentry DSN.** When set to a valid DSN, the collector and the Windows service wrapper send events to that project. Unset or empty keeps the built-in DSN, so default behavior is unchanged. An invalid value is ignored with a warning that does not print the value. The edge, gateway and scrape binaries all carry it. See `docs/otel-metrics.md`.
- **`identityfill` processor in the scrape image.** GCP audit records from the scrape collector had no `host.name`, no `service.name` and an empty body. The processor, already in the gateway image, now ships in the scrape image and fills them. The reference config runs it in `logs/gcp` after `sourcetag/gcp`. `test/scrape/identityfill_test.go` runs a GKE audit entry through the decoder, `sourcetag/gcp` and `identityfill`. Roll the new image before values that name `identityfill`; the old image rejects them. See `docs/scrape-collector.md`.
- **`tcp_check` receiver in the edge builds.** The Linux, static Linux and Windows edge collectors now ship `tcpcheckreceiver` v0.164.1, alpha for metrics, so an edge host can probe TCP targets and report `tcpcheck.status`, `tcpcheck.duration` and `tcpcheck.error`. Targets take IPv4 `host:port` and IPv6 in brackets, `[::1]:port`. `test/edge/tcpcheck.sh` runs the built edge binary against local IPv4 and IPv6 listeners and checks status 1 for an open port and 0 for a closed one. `test/configs/edge-tcpcheck.yaml` is the config it runs. See the README.

### Binary size

| Asset | MiB | Bytes |
|---|---|---|
| `otelcol-edge-0.3.11-linux-amd64` | 127.0 | 133120162 |
| `otelcol-edge-0.3.11-linux-amd64-gpu` | 128.2 | 134454472 |
| `otelcol-edge-0.3.11-windows-amd64.exe` | 130.4 | 136690176 |

The receiver adds about 56 to 64 KiB to each edge binary against 0.3.10. The gateway gains the Sentry DSN override and no component. Its asset size comes from the release build and is not listed here.

The scrape collector is not a release asset; it ships in the image `ghcr.io/vladistan/otelcol-scrape:v0.3.11`. The binary in that image is 147.0 MiB (154140834 bytes), 28 KiB more than in 0.3.10.

## [0.3.10] - 2026-11-08

The `sourcetag` processor in the scrape image, and per-core CPU series in the sample edge configs. The edge and gateway builds are unchanged apart from the version string.

### Added

- **`test/scrape/smoke.sh`** runs the published `otelcol-scrape` image with one source and fake credentials, and checks the version, the health endpoint, that the collector survives a failing receiver, and a clean shutdown.
- **End-to-end test for the ALB access log path.** `test/scrape/alb_e2e_test.go` runs a gzipped `.log.gz` object through a local S3 and SQS stand-in and the built scrape collector, then checks the decoded fields at an OTLP sink and that the SQS message is deleted. A second case shows an object whose key does not end in `.log` or `.log.gz` is dropped with a warning and not parsed. `test/scrape/restart-durability.sh` runs it.
- **Runbook and alert queries.** `docs/windows-edge.md` and `docs/scrape-collector.md` gain a runbook: where the logs are, how to roll back, how to re-read one event-log channel and how to turn summarization off. `docs/alert-queries.sql` holds ClickHouse queries for a stopped Windows service, a metric gap over two send windows, and a spike in dropped or collected events.
- **`sourcetag` processor in the scrape image.** It sets `log.source` and `event.category` and fills cloud resource attributes for `aws`, `gcp` and `azure` records, in Go instead of OTTL. The rules run in the order of the OTTL they replace, so a later rule sees what an earlier one set. It adds the `aws.eks` and `gcp.lb` sources, which the reference config lacked. The counters `otelcol_sourcetag_records_unsourced` and `otelcol_sourcetag_records_uncategorized` count records that leave without `log.source` and without `event.category`, per `provider`. Parity tests run the processor and the OTTL it replaces on the same records and compare every field. See `docs/scrape-collector.md`.

### Changed

- **The scrape reference config uses `sourcetag`** instead of the `transform/aws`, `transform/gcp` and `transform/azure` blocks. Roll the new image before values that name `sourcetag`; the old image rejects them.
- **The sample edge configs request per-core CPU series again.** `hostmetrics` 0.164 made the `cpu` attribute opt-in, so `system.cpu.time` (and `system.cpu.utilization` where enabled) collapsed to one series per state. `local-edge.yaml`, `gpu-edge.yaml` and `windows-edge.yaml` now set `attributes: [cpu, state]`.
- The CHANGELOG lists the binary sizes of every release from 0.3.1 to 0.3.6.

### Binary size

| Asset | MiB | Bytes |
|---|---|---|
| `otelcol-edge-0.3.10-linux-amd64` | 126.9 | 133054626 |
| `otelcol-edge-0.3.10-linux-amd64-gpu` | 128.2 | 134396776 |
| `otelcol-edge-0.3.10-windows-amd64.exe` | 130.3 | 136630272 |
| `otelcol-gateway-0.3.10-linux-amd64` | 208.0 | 218149026 |

The scrape collector is not a release asset; it ships in the image `ghcr.io/vladistan/otelcol-scrape:v0.3.10`. The binary in that image is 147.0 MiB (154112162 bytes).

## [0.3.9] - 2026-11-08

The `prometheus_remote_write` exporter in the gateway image. The edge and scrape builds are unchanged from 0.3.8.

### Added

- **`prometheus_remote_write` exporter in the gateway image.** The 0.3.8 gateway refused a config that used it with "unknown type". `prometheusremotewriteexporter` v0.164.1, beta for metrics, now ships with the gateway, so a collector can push metrics to a Prometheus remote write endpoint. `just verify-gateway` loads `test/configs/gateway-remotewrite.yaml`. See `docs/k8s-cluster.md`.

### Binary size

| Asset | MiB | Bytes |
|---|---|---|
| `otelcol-gateway-0.3.9-linux-amd64` | 202.2 | 211996832 |

## [0.3.8] - 2026-11-08

The `tcp_check` receiver in the gateway image. The edge and scrape builds are unchanged from 0.3.7.

### Added

- **`tcp_check` receiver in the gateway image.** The 0.3.7 gateway refused a config that used `tcp_check` with "unknown receiver". `tcpcheckreceiver` v0.164.1, alpha for metrics, now ships with the gateway, so a cluster collector can run a metrics pipeline that checks TCP targets. `just verify-gateway` loads `test/configs/gateway-tcpcheck.yaml`. See `docs/k8s-cluster.md`.

### Binary size

| Asset | MiB | Bytes |
|---|---|---|
| `otelcol-gateway-0.3.8-linux-amd64` | 201.7 | 211456160 |

## [0.3.7] - 2026-11-07

The `identityfill` processor in the gateway image and `logs.settle_lag` in the scrape image. The edge build is unchanged from 0.3.6.

### Added

- **`logs.settle_lag` for `awscloudwatchreceiver` in the scrape image.** Stock v0.164.1 skips events that CloudWatch ingests late, because an empty window moves the start to now. The image now builds a local copy of the receiver with a `settle_lag` duration, default 0, which ends every window at now minus the lag. With 0 the behaviour is the 0.3.6 behaviour, and the checkpoint format does not change. The sample sets `30s`. See `docs/scrape-collector.md`.
- **`identityfill` gateway processor.** It fills an empty `host.name` and `service.name` on Kubernetes event, pod status and GCP audit logs, and sets an empty GCP audit body from the method and resource names. It never overwrites a value, needs no settings, and is wired into the `otel-cluster` sample.

### Changed

- The scrape sample sets ALB access log `log.source` to `aws.elb`, matching the `aws.elb.*` attributes. Update dashboards and queries that filter on `aws.alb`.

### Binary size

| Asset | MiB | Bytes |
|---|---|---|
| `otelcol-gateway-0.3.7-linux-amd64` | 201.6 | 211402912 |

## [0.3.6] - 2026-11-06

Google Cloud Monitoring in the scrape image. The edge and gateway builds are unchanged from 0.3.5.

### Added

- **`googlecloudmonitoringreceiver` in the scrape image.** It pulls Google Cloud Monitoring metrics, such as the Cloud Run request, latency, CPU, memory and instance-count series. The sample polls two projects, one receiver instance per project, with the project ids read from `GCP_MON_PROJECT_A` and `GCP_MON_PROJECT_B`. Access uses the same default credentials as the Pub/Sub receivers. The identity needs `roles/monitoring.viewer` on each polled project. The receiver is alpha, and delta metrics reach the backend as delta values. See `docs/scrape-collector.md`.

### Binary size

| Asset | MiB | Bytes |
|---|---|---|
| `otelcol-edge-0.3.6-linux-amd64` | 126.9 | 133054626 |
| `otelcol-edge-0.3.6-linux-amd64-gpu` | 128.2 | 134396776 |
| `otelcol-edge-0.3.6-windows-amd64.exe` | 130.3 | 136630272 |
| `otelcol-gateway-0.3.6-linux-amd64` | 207.4 | 217518242 |

## [0.3.5] - 2026-11-05

ALB access logs in the scrape sample, and a scrape sample that no longer carries a site-specific bucket name. The collector code is unchanged from 0.3.4.

### Added

- **ALB access logs in the scrape sample.** The `aws_s3/alb` receiver reads gzipped `.log.gz` objects from S3 through SQS notifications and decodes them with the `elbaccess` format. Records carry `log.source` `aws.alb`, the client address, the status code and the target processing time. The bucket, prefix and queue URL come from `ALB_BUCKET`, `ALB_PREFIX` and `ALB_SQS_URL`.

### Changed

- The scrape sample reads the CloudTrail bucket from `CLOUDTRAIL_BUCKET`.
- The CloudTrail and S3-access receivers in the sample use the `aws_s3` name, so the sample logs no deprecation warning.
- The scrape collector doc covers the current CloudTrail body, a StatefulSet deployment, keyless GCP access under IRSA, CloudTrail digest objects and Pub/Sub stream resets.

### Binary size

| Asset | MiB | Bytes |
|---|---|---|
| `otelcol-edge-0.3.5-linux-amd64` | 126.9 | 133054626 |
| `otelcol-edge-0.3.5-linux-amd64-gpu` | 128.2 | 134396776 |
| `otelcol-edge-0.3.5-windows-amd64.exe` | 130.3 | 136630272 |
| `otelcol-gateway-0.3.5-linux-amd64` | 207.4 | 217518242 |

## [0.3.4] - 2026-11-02

Sentry crash reports for the Windows service, collector base 0.164.1, and a retry-forever edge queue.

### Added

- **Sentry reports for the Windows service.** When the collector service exits with a non-zero code, it sends one event with the exit code and flushes before the process ends. The flush wait is 5 s by default and can be set with `OTELCOL_CUSTOM_SENTRY_FLUSH_TIMEOUT`.

### Fixed

- **`otelcol-scrape` image labels.** The image now carries the source-repository, version and revision labels, so the release workflow can publish it to GHCR.

### Changed

- **Collector base is now 0.164.1** (was 0.154.0). The CloudTrail record body in the scrape sample now comes from `rpc.method`.
- **The Windows edge exporter retries forever and caps its disk queue at 1 GiB.** Before, the default 300 s retry limit dropped data during a long gateway outage. A full queue rejects new data and keeps the oldest.
- **Samples and docs use the current component names.** For example `host_metrics`, `otlp_http`, `resource_detection`, `file_log` and `windows_event_log` replace the deprecated spellings that log a warning at startup.

### Binary size

| Asset | MiB | Bytes |
|---|---|---|
| `otelcol-edge-0.3.4-linux-amd64` | 126.9 | 133054626 |
| `otelcol-edge-0.3.4-linux-amd64-gpu` | 128.2 | 134396776 |
| `otelcol-edge-0.3.4-windows-amd64.exe` | 130.3 | 136630272 |
| `otelcol-gateway-0.3.4-linux-amd64` | 207.4 | 217518242 |

## [0.3.3] - 2026-10-30

Per-window exponential histograms on every edge platform, `k8sevent` parity for `otel-cluster`, and the narrowed `otelcol-scrape` image with the Auth0 receiver.

### Added

- **`windowhist` processor** in every edge build (Linux, GPU and Windows). It turns the samples of one send window into one point per series, with exponential histograms for CPU, memory, disk, network and GPU series and `.rate` series for counters. It is off by default; the sample configs show how to turn it on. See `docs/otel-metrics.md`.
- **`otelcol-scrape` image** with a narrowed component set and the `auth0` receiver, which reads Auth0 events from an SQS queue. The image is published as `ghcr.io/vladistan/otelcol-scrape`.
- **`ottl_parity` option in `k8seventprocessor`**, which reproduces the OTTL event shaping that the cluster collector applies, so `otel-cluster` can run the gateway image.
- **Field conditions in Windows drop rules.** A rule can match event fields with `equals` and `ends_with`, in addition to channel, event ID, level and provider.
- **Hourly drop summary** for the `winlog` processor (`drop_summary`, with `tally_by` per rule). It reports counts of dropped events per rule, event ID and field value, keeps the top 20 values per field and counts the rest as `other`. Off by default.

### Changed

- **Windows default drop list.** The default now drops Security events 4634, 4672 and 5379, and Information and Verbose events 2049 and 2050 from `Microsoft-Windows-SystemDataArchiver/Diagnostic`. Security events 4624, 4658, 5156 and 5158 are no longer dropped by default.
- **Metric types change when summarization is on.** Series in a histogram group are sent as exponential histograms, and counters in those groups add `.rate` series, within a patch release. With `windowhist` out of the pipeline the output is unchanged.
- **Cost of summarization**, measured over one hour with the static edge build: CPU time 1.94x, resident memory +12.7 MiB, egress 2.26x (gzip).
- **`snoozelogprocessor`: `match` is now required.** A config without it fails at startup; there is no built-in default rule.
- **`winlog` severity.** Verbose events (level 5) map to DEBUG, and a severity text that was only a digit is replaced.
- **Narrowed `otelcol-scrape` manifest.** `builder-scrape.yaml` now lists only the components the scrape collector uses.

### Binary size

| Asset | MiB | Bytes |
|---|---|---|
| `otelcol-edge-0.3.3-linux-amd64` | 121.1 | 127021218 |
| `otelcol-edge-0.3.3-linux-amd64-gpu` | 122.4 | 128366312 |
| `otelcol-edge-0.3.3-windows-amd64.exe` | 124.3 | 130305536 |
| `otelcol-gateway-0.3.3-linux-amd64` | 222.4 | 233238690 |

## [0.3.2] - 2026-10-30

Searchable Windows event logs; `journald`, `snoozelog` and health-probe processors in the gateway image.

### Added

- **`winlog` processor** in `otelcol-edge` for Windows. It sets the body to the rendered message, keeps the event XML in `original_message` (capped at 64 KiB by default), sets `severity_*` from the event level, and adds `winlog.*` attributes. Counters `otelcol_winlog_events_read` and `otelcol_winlog_events_dropped` report per channel and rule. See `docs/windows-edge.md`.
- **Drop rules** for Windows events. The default list drops Security events 4624, 4634, 4672, 4658, 5156 and 5158, and Information events from provider `otelcol-edge`. Setting `drop_rules` replaces the list.
- **Gateway processors** `journald`, `snoozelog` and `healthprobe`, plus the `googlecloudmonitoring` receiver and the `googlecloudlogentry_encoding` extension. The `journald` processor maps journald `PRIORITY` 5 to INFO. The sample `test/configs/local-gateway.yaml` defines the three processors without wiring them into a pipeline.

### Changed

- **Security event log** is no longer unfiltered. In 0.3.1 the Security channel passed every event; 0.3.2 applies the default drop rules.

### Binary size

| Asset | MiB | Bytes |
|---|---|---|
| `otelcol-edge-0.3.2-linux-amd64` | 121.0 | 126886050 |
| `otelcol-edge-0.3.2-linux-amd64-gpu` | 122.3 | 128229544 |
| `otelcol-edge-0.3.2-windows-amd64.exe` | 124.1 | 130134016 |
| `otelcol-gateway-0.3.2-linux-amd64` | 222.4 | 233238690 |

## [0.3.1] - 2026-10-27

Windows edge artifact; `otel-main` runs a gateway image built from this tree.

### Added

- **Windows edge** `otelcol-edge-0.3.1-windows-amd64.exe` (windows/amd64, CGO off, unsigned). It collects host metrics, Windows performance counters, Windows event logs and file logs, installs as a Windows service through the OCB service handler, and writes a rotating self-log (10 MiB, 5 backups). The sample config is `test/configs/windows-edge.yaml`.
- **Event logs are on by default** with the Security channel unfiltered; 0.3.2 adds default drop rules for it. The service runs as LocalSystem.
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
| 0.3.1 Windows edge | 130071552 |
| 0.3.1 gateway | 231993506 |

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
