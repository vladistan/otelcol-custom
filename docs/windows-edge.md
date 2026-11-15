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

The exporter retries forever, so a gateway outage of any length loses nothing while the queue has room. The queue holds at most 1 GiB per host. When it is full, new data is rejected and the oldest data stays. Keep disk headroom beyond 1 GiB, because the queue files need extra space while they compact.

An upgrade replaces only the binary. Self-logs rotate at 10 MiB and keep 5 backups (60 MiB cap). The collector never reads its own log directory.

The health endpoint is `127.0.0.1:13133` and zpages is `127.0.0.1:55679`. Neither accepts remote connections.

## Metrics

Hostmetrics names and units are the ones the receiver emits at v0.164.1, the same as on Linux.

| Metric | Unit | Source |
|---|---|---|
| `system.cpu.time` | `s` | host_metrics `cpu` |
| `system.cpu.utilization` | `1` | host_metrics `cpu` |
| `system.memory.usage` | `By` | host_metrics `memory` |
| `system.memory.utilization` | `1` | host_metrics `memory` |
| `system.paging.usage` | `By` | host_metrics `paging` |
| `system.paging.operations` | `{operations}` | host_metrics `paging` |
| `system.paging.faults` | `{faults}` | host_metrics `paging` |
| `system.cpu.load_average.1m`, `.5m`, `.15m` | `{thread}` | host_metrics `load` |
| `system.filesystem.usage` | `By` | host_metrics `filesystem` (15m) |
| `system.network.io` | `By` | host_metrics `network` |
| `system.network.packets` | `{packets}` | host_metrics `network` |
| `system.network.errors` | `{errors}` | host_metrics `network` |
| `system.network.dropped` | `{packets}` | host_metrics `network` |
| `system.network.connections` | `{connections}` | host_metrics `network` |
| `process.cpu.time` | `s` | host_metrics `process` |
| `process.cpu.utilization` | `1` | host_metrics `process` |
| `process.memory.usage` (working set) | `By` | host_metrics `process` |
| `process.memory.virtual` (private bytes) | `By` | host_metrics `process` |
| `process.disk.io` | `By` | host_metrics `process` |
| `process.threads` | `{threads}` | host_metrics `process` |
| `process.handles` | `{count}` | host_metrics `process` |

Per-process series carry `process.executable.name`, `process.executable.path`, `process.pid` and `process.owner`. Series below 50 MiB of memory or 0.001 CPU utilization are dropped, as on Linux.

The disk scraper is off. PhysicalDisk counters replace it.

## TCP checks

The `tcp_check` receiver is in the Windows edge build from 0.3.11. It reports `tcpcheck.status`, `tcpcheck.duration` and `tcpcheck.error` for each target, over IPv4 and over IPv6 with the address in brackets, such as `[2001:db8::11]:8006`. See the README and `test/configs/edge-tcpcheck.yaml`.

## GPU metrics

The `gpu` receiver is in the Windows edge build from 0.3.12. It reads NVIDIA GPU metrics from `nvml.dll`, which the NVIDIA driver installs, and emits the same `hw.gpu.*`, `hw.temperature`, `hw.power` and `hw.errors` series as the Linux edge. See `receiver/gpureceiver/README.md` for the metric list.

The sample config has the receiver and a `metrics/gpu` pipeline commented out. On a host with an NVIDIA GPU, uncomment both. With no driver or no `nvml.dll`, the receiver logs one warning at the first scrape and sends nothing; the rest of the collector is not affected.

The receiver loads `nvml.dll` from System32. If the driver keeps it elsewhere, such as `C:\Program Files\NVIDIA Corporation\NVSMI\`, set `library_path` to the absolute path. The `windowhist` processor has a `gpu` group; with summarization on, set the receiver's `collection_interval` to 10s.

The Windows driver may not support every NVML call for every GPU mode (WDDM or TCC); a call that is not supported drops that metric for that GPU and logs one info line, `GPU call not supported`.

## New-file records

The `fswatch` receiver reports each new file under a watched directory as one log record. It is in the Windows edge build only, and it is alpha. It reads no file contents.

```yaml
receivers:
  fswatch/cds:
    paths: ['D:\CDSProjects']
    exclude: ['**/~*', '**/*.tmp']
    start_at: end
    storage: file_storage
```

| Key | Default | Meaning |
|---|---|---|
| `paths` | required | Roots to watch, each with a drive letter or a UNC prefix. No glob characters, no duplicate or nested roots. |
| `exclude` | none | Globs (`**` allowed) matched against the path under the root, without regard to case. |
| `start_at` | `end` | For a root with no saved state: `end` reports nothing that exists; `beginning` reports every existing file once. |
| `storage` | none | A storage extension that keeps the position of each root. Without it nothing survives a restart. |
| `settle` | `5m` | After the creation record, the file size is watched. When it has stayed the same for this long, a second record `file write complete` follows with `file.size_final`. `0s` turns this off. |
| `settle_timeout` | `10m` | Longest a file is watched for growth. A file still changing then gets the second record without `file.size_final`. |
| `max_catchup` | `168h` | How far back a restart looks for files created while the collector was down. |
| `buffer_size` | `262144` | Bytes of the Windows change buffer per root, 4096 to 1048576. |
| `max_rescans` | `6` | Rescans an hour after a lost event or a failed delivery. |
| `rescan_interval` | `0s` | When set, scan the tree this often as well. Use it on a network share. |

Each record has the body `file created`, severity INFO, the file creation time as its timestamp, and the time the receiver saw it as its observed timestamp. Attributes: `file.path`, `file.name`, `file.extension` (no dot), `file.size`, `file.dir_relative` (backslashes, `.` for a file directly under the root), `file.root`, `file.size_final` (on the second record only) and `file.catchup`. `file.catchup` is `true` for a file found by a scan and `false` for a live event.

The second record has the body `file write complete`, the time of the stable-size check as its timestamp, and `file.size` and `file.size_final` both set to the final size. A catch-up file whose last write is already older than `settle` gets one record with `file.size_final` and no second record. After a restart, files created within `settle_timeout` whose last record is not out are checked again: a stable one gets its second record at once, with `file.catchup=true`, and a file still being written is watched as usual. A second record that cannot be delivered is retried; after 10 failed attempts, or `settle_timeout` after the first failure, it is dropped with one warning and counted in `otelcol_fswatch_writes_dropped`.

A file counts as new when it is created in the tree or moved into it. Writes, deletions and renames inside the tree are ignored, except a rename from an excluded name, such as `~run.tmp` to `run.dx`, which reports the final name.

### Restarts and lost events

The receiver keeps one position per root, the creation time of the newest file reported. At start it opens the watch first, then scans for files created after that position and reports them with `file.catchup=true`. A file is never reported twice for the same path and creation time, across restarts included. Only the 256 newest files created in the 2 seconds before the position are remembered, so a crash or restart can repeat files beyond that.

If the Windows change buffer overflows, or a delivery fails, the receiver holds the position back and scans the tree again. It scans at most `max_rescans` times an hour and at least a minute apart. While a scan is owed, a restart can repeat files. If the root disappears, the receiver logs a warning, retries from 5 seconds up to a minute, and scans when the root is back. The collector starts without the root.

### Not covered

- A file moved into the tree while the collector is down keeps its old creation time, so the restart scan cannot tell it from an old file.
- A clock set back: files created after the jump have a creation time below the position and are missed by a scan. The live watch still sees them.
- A restore that keeps the creation time, such as `robocopy /COPY:T`, is missed by a scan for the same reason.
- Windows keeps the creation time of a file that is deleted and created again under the same name within about 15 seconds. The live watch reports it. A scan does not.
- Network roots are best effort. A mapped drive letter is not visible to a service account; use a UNC path. Set `rescan_interval` on a share.

Counters: `otelcol_fswatch_records_emitted` (attribute `catchup`), `otelcol_fswatch_excluded`, `otelcol_fswatch_vanished`, `otelcol_fswatch_overflows`, `otelcol_fswatch_rescans`, `otelcol_fswatch_rescans_capped` (once an hour while the limit holds), `otelcol_fswatch_writes_completed`, `otelcol_fswatch_writes_dropped`, `otelcol_fswatch_settle_timeouts` and `otelcol_fswatch_scan_skipped` (attribute `reason`: `denied` or `future`).

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

Severity comes from the numeric `<Level>` and the `<Keywords>` mask in the event XML, read before the XML is cut. Without the XML it falls back to the body `level`, either the rendered name (`Verbose`) or a raw digit (`"5"`). Both give DEBUG. A digit-only severity text left by the receiver, such as `"5"`, is replaced by the mapped text, or cleared when the level is unknown.

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
- `fields` tests event data values. Each entry names one `winlog.event_data.<Name>` field and sets either `equals` (any listed value matches) or `ends_with` (a suffix), not both. Names are case-sensitive, values compare as text, and an event without the field never matches. Every entry in the list must match. An unknown key or a malformed entry fails at config load.

This pair drops service logons and machine-account network logons while real logons pass. It is not in the default list.

```yaml
winlog:
  drop_rules:
    - name: service-logons
      channels: [Security]
      event_ids: ["4624"]
      fields:
        - name: LogonType
          equals: ["5"]
    - name: machine-network-logons
      channels: [Security]
      event_ids: ["4624"]
      fields:
        - name: LogonType
          equals: ["3"]
        - name: TargetUserName
          ends_with: "$"
```

Setting `drop_rules` replaces the default list. The default list drops Security events 4634, 4672 and 5379, Information and Verbose events 2049 and 2050 from the `Microsoft-Windows-SystemDataArchiver/Diagnostic` channel, and Information events from provider `otelcol-edge`. Security 4624 stays collected because a rule cannot tell service logons from interactive ones.

### Drop summary

The summary is off by default. With `drop_summary.enabled: true`, the processor emits one log record an hour that counts the dropped events. The partial hour is emitted at shutdown. The record carries counts only, never the text of a dropped event.

```yaml
winlog:
  drop_summary:
    enabled: true
  drop_rules:
    - name: service-logons
      channels: [Security]
      event_ids: ["4624"]
      tally_by: [AuthenticationPackageName]
      fields:
        - name: LogonType
          equals: ["5"]
```

`tally_by` lists event data names. For each, the summary counts the values of the events that rule dropped, such as `AuthenticationPackageName=Kerberos: 432`. It keeps the 20 most frequent values per rule and field in a window and counts the rest under `other`. After 1000 distinct values in one window, new values count under `other` too. An event without the field counts under `(missing)`.

| Record attribute | Type | Holds |
|---|---|---|
| `log.source` | string | `winlog:drop_summary` |
| `winlog.drop_summary.window_start`, `winlog.drop_summary.window_end` | string | RFC 3339 UTC times of the first drop and of the emit |
| `winlog.drop_summary.total` | int | Events dropped in the window |
| `winlog.drop_summary.rules` | map | Per rule name: `total`, `event_ids` (event ID to count) and `fields` (field name to value to count) |

The record has severity INFO and the body `winlog drop summary: <n> events dropped`. It enters the pipeline after the `winlog` processor, so later processors apply to it.

### Counters

| Counter | Attributes | Counts |
|---|---|---|
| `otelcol_winlog_events_read` | `channel` | Event records seen, before drop rules |
| `otelcol_winlog_events_dropped` | `channel`, `rule` | Records dropped, by the first matching rule |

Both leave through the collector's internal telemetry with the resource attributes in `service.telemetry.resource`.

## TightVNC server log

The TightVNC server, running as a service, writes `C:\WINDOWS\system32\config\systemprofile\AppData\Roaming\TightVNC\tvnserver.log`. The file is UTF-16LE with CRLF line ends and a byte order mark. At log level 9 almost every line is debug output, and a restart of the service renames the file and starts a new one.

The sample config reads it with `filelog/tightvnc` and the `tightvnc` processor in the `logs/tightvnc` pipeline:

```yaml
receivers:
  filelog/tightvnc:
    include: ['C:\WINDOWS\system32\config\systemprofile\AppData\Roaming\TightVNC\tvnserver.log']
    encoding: utf-16le
    start_at: end
    storage: file_storage
processors:
  tightvnc:
    time_zone: Local
```

- `start_at: end` reads nothing of an existing file at the first start. The read position lives in `file_storage`, so a restart of the collector continues where it stopped.
- A host without the file reads nothing.
- The receiver has no operators; the processor does the parsing.
- Only `tvnserver.log` matches `include`, so the renamed files `tvnserver.1.log` and `tvnserver.2.log` are not read again. Lines written to the old file between the last poll and the rename can be lost, which is the limit of `filelog` on Windows.

### Line format

```
[ <pid>/ <tid>] YYYY-MM-DD HH:MM:SS:mmm <marker> <message>
```

The marker is `+`, `-` or `!`, or absent. A line without a marker has `:` or blanks where the marker would be. The marker does not mean severity: `!` marks both `Client #0 connected` and `Exception in control client thread`.

| Field | Result |
|---|---|
| Time | The record time, read in `time_zone`. A time that does not exist in the zone, such as one skipped by a clock change, leaves the observed time. A time in the repeated hour of a clock change takes the first occurrence. |
| Message | The record body, without the line prefix |
| Marker | `tightvnc.marker`, empty when the line has none |
| Process and thread | `tightvnc.pid`, `tightvnc.tid` |

`time_zone` takes `Local` (the default, the zone of the host) or `UTC`. A zone name such as `America/New_York` is rejected, because the Windows build carries no zone database.

A leading byte order mark is removed from the body of every record.

### Severity and event type

Severity comes from the message text:

| Severity | Message |
|---|---|
| ERROR | Starts with `Exception`, or contains `error` or `failed` in any case |
| WARN | Not ERROR, and contains `rejected`, `denied`, `refused` or `warning` in any case |
| INFO | Every other line |

`tightvnc.event` is the first of these that matches the message, in this order:

| Value | Message contains |
|---|---|
| `disconnect` | `disconnect` |
| `auth` | `authenticat` |
| `connect` | `connection from` or `connected` |
| `error` | an ERROR word |
| `lifecycle` | none of the above, and the line has a marker |
| `other` | none of the above |

The word lists are checked against the lines of one host. Real disconnect and failed-authentication lines have not been checked.

| Attribute | Set when the message is |
|---|---|
| `client.address`, `server.port` | `Incoming rfb connection from <ip> to port <port>` |
| `tightvnc.client.id` | a line containing `Client #<n>` |
| `tightvnc.view_only` | `Authenticated with view-only password = <n>` |
| `server.address`, `server.port` | `Rfb server started at <ip>:<port>` |

### Dropped and kept lines

The processor keeps every line with a marker. It drops a line without a marker when the message starts with a prefix from `drop_prefixes` and holds no ERROR word. A line outside the format, an empty line, and a record whose body is not a string are kept unchanged with `tightvnc.parse=failed`.

A new TightVNC debug line that no prefix lists is therefore kept, and the kept counter rises. Add its prefix to `drop_prefixes`. A configured list replaces the default list, which holds these prefixes:

```yaml
tightvnc:
  drop_prefixes:
    - Control client
    - Recieved control message
    - process memory usage
    - new client, process memory usage
    - Shared flag
    - Initial view-only state
    - RFB initialization phase 1 completed
    - Requested to start up extra RFB servers
    - Requested to shut down extra RFB servers
    - Considering to reload extra RFB servers
    - Same Extra Ports configuration
    - Timeout on acquire frame
    - DesktopSrvDispatcher
    - View port
    - Constrained
    - Active console session Id
    - Destination dirty rect
    - update detected
    - Acquire frame
    - get frame buffer
    - Time between
    - UpdateFilter
    - WinVideoRegionUpdaterImpl
```

`Recieved` is the server's own spelling.

An empty entry in `drop_prefixes`, and a `time_zone` other than `Local` or `UTC`, fail the config load. The `time_zone` error names `Local` and `UTC` as the accepted values.

### Counters

| Counter | Counts |
|---|---|
| `otelcol_tightvnc_records_kept` | Parsed records passed on |
| `otelcol_tightvnc_records_dropped` | Records removed as noise |
| `otelcol_tightvnc_records_unparsed` | Records outside the format, passed on unchanged |

Every record is counted once, so the three add up to the records read. They leave through the collector's internal telemetry with the resource attributes in `service.telemetry.resource`.

## Runbook

### Where to look

- Self-logs: `%ProgramData%\otelcol-edge\logs\otelcol-edge.log`. They rotate at 10 MiB and keep 5 backups.
- Service state: `Get-Service otelcol-edge`. A stop or crash also appears as Service Control Manager event 7036 or 7034 in the System log.
- Health: `http://127.0.0.1:13133/` on the host. Zpages is on `127.0.0.1:55679`.
- Queue and read positions: `%ProgramData%\otelcol-edge\storage\`.

### Alert queries

`docs/alert-queries.sql` holds one ClickHouse query per alert. Each returns rows only while its condition holds, so the alert fires on a non-empty result. Run them every 5 minutes.

| Alert | Query | Fires when |
|---|---|---|
| Service stopped | 1 | A Service Control Manager event reports a service stopped (7036) or crashed (7034, 7031) in the last 15 minutes |
| Metric gap | 2 | A Windows host sent no metric for more than 120 s, two send windows. A stopped `otelcol-edge` service and a gateway outage both match |
| Dropped-event spike | 3 | The `otelcol_winlog_events_dropped` growth in 15 minutes is over 100 and over 3 times the average of the 6 hours before |
| Collected-event spike | 3b | Winlog rows in 15 minutes are over 500 and over 3 times the average of the 6 hours before |

Query 3 needs the collector's own counters in ClickHouse, sent through the `service.telemetry` metrics reader in the sample config. A deployment that does not export them returns no rows, so use query 3b there. Query 1 matches every stopped service. Add `AND service IN (...)` to watch named ones.

### Roll back

Set `otel_edge_win_version` to the previous tag and run the role. The role puts the previous binary back and leaves the config and storage in place. It renders only the features of the target version. Tags exist for v0.3.1 to v0.3.11.

### Re-read one event-log channel

`reset_channel: <receiver>` deletes that receiver's storage file. The channel then starts from its `start_at` value on the next service start. With `end`, events written while the service was stopped are not collected.

### Turn summarization and drop summaries off

- `otel_edge_win_summarize_enabled: false` takes `windowhist` out of the metrics pipeline. Each receiver then collects at the send interval, 60s.
- `drop_summary.enabled: false`, the default, stops the hourly drop-count record.
- `otel_edge_win_default_drop_list_enabled: false` keeps the events the default drop list removes.

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
- The `gpu` receiver on other GPU models and driver modes. It has run on one Windows host with an NVIDIA GPU; the adapter's tests run without a GPU.
- The `fswatch` receiver on a Windows host: a real watch handle, shutdown while a read is pending, an overflow at `buffer_size: 4096`, a removed root, NTFS creation times, the events for a directory moved in and for a temp name renamed to a final name. The logic runs in unit tests against a fake watcher and a fake tree.
