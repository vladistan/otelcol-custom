# Windows Edge Collector

`otelcol-edge` for `windows/amd64` collects host metrics, Windows performance counters and event logs. It sends OTLP over HTTP to the gateway endpoint and keeps a persistent queue on disk. Release 0.3.1 sends metrics and raw event logs with no local processors.

The sample config is `test/configs/windows-edge.yaml`. Every receiver collects at the send interval (60s, filesystem 15m) because 0.3.1 has no summarizer.

## Supported Windows

Windows Server 2022, and Windows 11 and newer.

## Service account and paths

The service is named `otelcol-edge` and runs as `LocalSystem`. A compromised collector therefore has full control of the host.

| Item | Path |
|---|---|
| Binary | `%ProgramFiles%\otelcol-edge\otelcol-edge.exe` |
| Config | `%ProgramData%\otelcol-edge\config.yaml` |
| Queue and bookmarks | `%ProgramData%\otelcol-edge\storage\` |
| Self-logs | `%ProgramData%\otelcol-edge\logs\otelcol-edge.log` |

An upgrade replaces only the binary. Self-logs rotate at 10 MiB and keep 5 backups (60 MiB cap). The collector never reads its own log directory.

The health endpoint is `127.0.0.1:13133` and zpages is `127.0.0.1:55679`. Neither accepts remote connections.

## Metrics

Hostmetrics names and units are the ones the receiver emits at v0.154.0, the same as on Linux.

| Metric | Unit | Source |
|---|---|---|
| `system.cpu.time` | `s` | hostmetrics `cpu` |
| `system.cpu.utilization` | `1` | hostmetrics `cpu` |
| `system.memory.usage` | `By` | hostmetrics `memory` |
| `system.memory.utilization` | `1` | hostmetrics `memory` |
| `system.paging.usage` | `By` | hostmetrics `paging` |
| `system.paging.operations` | `{operations}` | hostmetrics `paging` |
| `system.paging.faults` | `{faults}` | hostmetrics `paging` |
| `system.cpu.load_average.1m`, `.5m`, `.15m` | `{thread}` | hostmetrics `load` |
| `system.filesystem.usage` | `By` | hostmetrics `filesystem` (15m) |
| `system.network.io` | `By` | hostmetrics `network` |
| `system.network.packets` | `{packets}` | hostmetrics `network` |
| `system.network.errors` | `{errors}` | hostmetrics `network` |
| `system.network.dropped` | `{packets}` | hostmetrics `network` |
| `system.network.connections` | `{connections}` | hostmetrics `network` |
| `process.cpu.time` | `s` | hostmetrics `process` |
| `process.cpu.utilization` | `1` | hostmetrics `process` |
| `process.memory.usage` (working set) | `By` | hostmetrics `process` |
| `process.memory.virtual` (private bytes) | `By` | hostmetrics `process` |
| `process.disk.io` | `By` | hostmetrics `process` |
| `process.threads` | `{threads}` | hostmetrics `process` |
| `process.handles` | `{count}` | hostmetrics `process` |

Per-process series carry `process.executable.name`, `process.executable.path`, `process.pid` and `process.owner`. Series below 50 MiB of memory or 0.001 CPU utilization are dropped, as on Linux.

The disk scraper is off. PhysicalDisk counters replace it.

## Performance-counter extensions

Perf counters are named `windows.<object>.<counter>`. Retransmits are collected for `TCPv4`.

| Metric | Unit | Counter |
|---|---|---|
| `windows.physical_disk.queue_length.current` | `{request}` | PhysicalDisk, Current Disk Queue Length |
| `windows.physical_disk.queue_length.average` | `{request}` | PhysicalDisk, Avg. Disk Queue Length |
| `windows.physical_disk.read.time` | `s` | PhysicalDisk, Avg. Disk sec/Read |
| `windows.physical_disk.write.time` | `s` | PhysicalDisk, Avg. Disk sec/Write |
| `windows.physical_disk.idle.time` | `%` | PhysicalDisk, % Idle Time |
| `windows.physical_disk.transfers` | `{transfer}/s` | PhysicalDisk, Disk Transfers/sec |
| `windows.physical_disk.read.rate` | `By/s` | PhysicalDisk, Disk Read Bytes/sec |
| `windows.physical_disk.write.rate` | `By/s` | PhysicalDisk, Disk Write Bytes/sec |
| `windows.memory.committed` | `By` | Memory, Committed Bytes |
| `windows.memory.commit_limit` | `By` | Memory, Commit Limit |
| `windows.memory.pages` | `{page}/s` | Memory, Pages/sec |
| `windows.tcp.retransmits` | `{segment}/s` | TCPv4, Segments Retransmitted/sec |
| `windows.network_interface.output_queue_length` | `{packet}` | Network Interface, Output Queue Length |
| `windows.system.processor_queue_length` | `{thread}` | System, Processor Queue Length |

Counters use English names through PDH, so localized Windows hosts resolve them. A missing counter logs one warning and the other metrics still export.

## Event logs

Event logs are on by default: System, Application, Security and one bucket receiver for other channels. Security is unfiltered in 0.3.1. Each record keeps the event XML in `original_message`, cut at 64 KiB. Read positions persist in `file_storage`.

A channel created after the last role run is collected after the next role run.

## Log rotation limit

File logs are held open only while read. Upstream documents that Move/Create rotation on Windows may lose data (`fileconsumer/design.md`); the rename-rotation test on the Windows test host decides whether this limit applies. Copy-truncate rotation loses lines written after the last poll.

## Not verified without a Windows host

- The `include_fs_types` filter on `NTFS` and `ReFS` as the fixed-drive rule.
- The `host.ip` resource attribute from the system detector.
