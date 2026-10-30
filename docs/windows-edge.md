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

Perf counters are named `windows.<object>.<counter>`. Disk and network series carry the receiver's `instance` attribute.

| Metric | Unit | Attributes | Counter |
|---|---|---|---|
| `windows.physicaldisk.queue_length` | `{operation}` | `instance` | PhysicalDisk, Current Disk Queue Length |
| `windows.physicaldisk.avg_queue_length` | `{operation}` | `instance` | PhysicalDisk, Avg. Disk Queue Length |
| `windows.physicaldisk.operation_latency` | `s` | `instance`, `disk.io.direction` | PhysicalDisk, Avg. Disk sec/Read and sec/Write |
| `windows.physicaldisk.idle_time` | `%` | `instance` | PhysicalDisk, % Idle Time |
| `windows.physicaldisk.transfers` | `{operation}/s` | `instance` | PhysicalDisk, Disk Transfers/sec |
| `windows.physicaldisk.io` | `By/s` | `instance`, `disk.io.direction` | PhysicalDisk, Disk Read Bytes/sec and Write Bytes/sec |
| `windows.memory.committed` | `By` | none | Memory, Committed Bytes |
| `windows.memory.commit_limit` | `By` | none | Memory, Commit Limit |
| `windows.memory.pages` | `{page}/s` | none | Memory, Pages/sec |
| `windows.tcp.retransmitted_segments` | `{segment}/s` | `network.type` (`ipv4`, `ipv6`) | TCPv4 and TCPv6, Segments Retransmitted/sec |
| `windows.network_interface.output_queue_length` | `{packet}` | `instance` | Network Interface, Output Queue Length |
| `windows.system.processor_queue_length` | `{thread}` | none | System, Processor Queue Length |

Counters use English names through PDH, so localized Windows hosts resolve them. A missing counter logs one warning and the other metrics still export.

## Event logs

Event logs are on by default: System, Application, Security and one bucket receiver for other channels. Security is unfiltered in 0.3.1. Read positions persist in `file_storage`.

A channel created after the last role run is collected after the next role run.

### winlog processor (0.3.2)

The `winlog` processor counts every event record, drops the ones a rule matches, and shapes the rest. A record whose body is not a map with a channel passes through untouched.

| Field | Value |
|---|---|
| `body` | Rendered message. If empty: `<provider> event <id> key=value ...` with the event data keys sorted |
| `original_message` | Event XML, cut at `original_message_max_bytes` (default 65536, 0 = no cap) on a character boundary |
| `original_message.truncated` | `true`, only when the XML was cut |
| `log.source` | `winlog:<channel>` |
| `severity_number`, `severity_text` | Level 1 FATAL, 2 ERROR, 3 WARN, 4 INFO, 5 DEBUG, 0 INFO; an Audit Failure event is WARN |

The processor sets these attributes when the event has the value.

| Attribute | Type | Source |
|---|---|---|
| `winlog.channel`, `winlog.provider_name`, `winlog.provider_guid`, `winlog.computer_name` | string | event header |
| `winlog.event_id`, `winlog.record_id` | int | event header |
| `winlog.task`, `winlog.opcode` | string | rendered, raw when not rendered |
| `winlog.keywords` | string array | rendered keywords |
| `winlog.user.identifier` | string | user SID |
| `winlog.process.pid`, `winlog.process.thread.id` | int | `Execution` element |
| `winlog.event_data.<Name>` | string | one per named `Data` element; unnamed elements are `param1`, `param2`, ... |

Severity comes from the numeric `<Level>` and the `<Keywords>` mask in the event XML, read before the XML is cut. Without the XML it falls back to the rendered level name.

### Drop rules

A rule drops an event when every field it sets matches. Values inside a field are alternatives, an unset field matches anything, and the first matching rule in the list names the drop.

```yaml
winlog:
  drop_rules:
    - name: noisy-service-state
      channels: [System]
      event_ids: ["7036", "10000-10010"]
      levels: [4]
      providers: ["Service Control Manager"]
```

- `event_ids` take single IDs and inclusive `lo-hi` ranges.
- `levels` are numeric, 0 to 5. An event with no known level never matches a level rule.
- `channels` and `providers` match case-insensitively.
- Names must be unique and each rule must set at least one field.

Setting `drop_rules` replaces the default list. The default list drops Security events 4624, 4634, 4672, 4658, 5156 and 5158, and Information events from provider `otelcol-edge`.

### Counters

| Counter | Attributes | Counts |
|---|---|---|
| `otelcol_winlog_events_read` | `channel` | Event records seen, before drop rules |
| `otelcol_winlog_events_dropped` | `channel`, `rule` | Records dropped, by the first matching rule |

Both leave through the collector's internal telemetry with the resource attributes in `service.telemetry.resource`.

## Log rotation limit

File logs are held open only while read. Upstream documents that Move/Create rotation on Windows may lose data (`fileconsumer/design.md`); whether this limit applies to rename rotation is unverified. Copy-truncate rotation loses lines written after the last poll.

## Expected messages

- The process scraper needs `mute_process_name_error: true`. Processes such as PID 0, PID 4 and `Registry` have no executable path, and without the setting the scraper logs an error every interval. The sample config sets it.
- Once per service start, an event-log channel may log `EvtFormatMessage ... failed to find MUI file`. A provider with no message resources causes it. The event still ships, and the message is harmless.
- Bookmarks live in the `file_storage` directory as `receiver_<type>_<name>` files, for example `receiver_windowseventlog_security`, with no storage suffix.
- Keep the host clock synced. A host with a free-running clock sends records dated in the future, and they sort after current data in ClickHouse.

## Not verified without a Windows host

- The `include_fs_types` filter on `NTFS` and `ReFS` as the fixed-drive rule.
- The `host.ip` resource attribute from the system detector.
