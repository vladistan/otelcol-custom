# File-watch receiver (`fswatch`)

Emits one log record for each new file under watched directories. It reads no
file contents. It is part of the Windows `otelcol-edge` build only. Status: alpha.

It calls `ReadDirectoryChangesW` through `golang.org/x/sys/windows`, one handle
per root with the subtree on. On other systems the component is registered and a
config that uses it fails at startup with `platform unsupported`. Nothing in
contrib does this: `filelog` reads contents and `filestats` polls metrics.

## Configuration

```yaml
receivers:
  fswatch/cds:
    paths: ['D:\CDSProjects']       # required; drive letter or UNC prefix
    exclude: ['**/~*', '**/*.tmp']   # doublestar globs, root-relative, any case
    start_at: end                    # end | beginning, for a root with no saved state
    storage: file_storage            # optional storage extension
    settle: 5m                       # stable size before the write-complete record; 0 = off
    settle_timeout: 10m              # longest a file is watched for growth
    max_catchup: 168h
    buffer_size: 262144              # bytes per root, 4096 to 1 MiB
    max_rescans: 6                   # per hour, at least 60 s apart
    rescan_interval: 0s              # 0 = only after a loss or reconnect

service:
  pipelines:
    logs/files:
      receivers: [fswatch/cds]
      processors: [resource_detection, batch]
      exporters: [otlp_grpc]
```

`Validate` rejects a relative path, a path with `*`, `?`, `[` or `{`, duplicate
or nested roots, `settle_timeout` below `settle`, a buffer outside 4096 to
1 MiB, and a bad exclude glob. A `storage` ID that names no storage extension
fails Start with the ID in the message. A missing or unreadable root does not:
the receiver logs a warning, retries with backoff from 5 s to 1 min, and scans
when the root opens. A network root has its buffer clamped to 64 KiB.

## Records

Body `file created`, severity INFO. `Timestamp` is the file creation time and
`ObservedTimestamp` is the time the receiver saw it. A file that cannot be read
at event time gets the observed time and no `file.size`. The receiver sets no
resource attributes; run `resource_detection` in the pipeline.

With `settle` set (5 minutes by default) the receiver then watches the file's
size. When it has stayed the same for `settle`, a second record follows with body
`file write complete`, the time of that check as `Timestamp`, the same path
attributes, and `file.size` and `file.size_final` both set to the final size. A
file still changing at `settle_timeout` gets the second record without
`file.size_final`, and `otelcol_fswatch_settle_timeouts` counts it. A file deleted
while watched gets none. Files found by a catch-up scan are watched the same way. A catch-up file whose last write is already older than
`settle` gets one record that carries `file.size_final`, and no second record.

The watch is not saved, but the files whose last record is out are (`done` in
the saved state, kept for `settle_timeout`). After a restart, every other file
created within `settle_timeout` before it is checked: one whose last write is
older than `settle` gets its second record at once, marked `file.catchup=true`;
the rest are watched as usual. Files older than that get no second record.

If a second record cannot be delivered, it is retried at each check. After 10
failed attempts, or `settle_timeout` after the first failure, the receiver drops
it, logs one warning and counts it in `otelcol_fswatch_writes_dropped`.

| Attribute | Value |
|---|---|
| `file.path` | Full path as the event gave it |
| `file.name` | Base name |
| `file.extension` | Extension without the dot |
| `file.size` | Bytes at event time; 0 is normal |
| `file.dir_relative` | Directory under the root, with backslashes; `.` directly under it |
| `file.root` | The configured root that holds the file |
| `file.size_final` | Only on the `file write complete` record |
| `file.catchup` | `true` for a file found by a scan, `false` for a live event |

Counters: `otelcol_fswatch_records_emitted` (attribute `catchup`),
`otelcol_fswatch_excluded`, `otelcol_fswatch_vanished`,
`otelcol_fswatch_overflows`, `otelcol_fswatch_rescans`,
`otelcol_fswatch_rescans_capped` (once an hour while `max_rescans` holds a
rescan back), `otelcol_fswatch_writes_completed`, `otelcol_fswatch_writes_dropped`,
`otelcol_fswatch_settle_timeouts` and `otelcol_fswatch_scan_skipped` (attribute `reason`: `denied`, `future`).

## What counts as new

A file created in the tree, or moved into it from outside. A directory moved in
reports the files inside it. Writes, deletions and renames inside the tree are
ignored. A rename from an excluded name to a name that is not excluded counts as
new, so `~run.tmp` renamed to `run.dx` reports `run.dx`.

## Position, catch-up and rescans

One position per root, kept under the key `fswatch/<lower-cased root>` (backslashes,
no trailing separator, so `C:\Data` and `C:/Data` share it): the
creation time of the newest file reported, plus up to 256 recent files for
duplicate checks. It lives in the storage extension, never under a watched path.

- Start: open the watch, then scan for files created after the position, no
  older than `max_catchup`, oldest first, marked `file.catchup=true`. With no
  saved position, `end` saves the current time and reports nothing, and
  `beginning` reports every existing file once whatever `max_catchup` says.
- A file is not reported twice for the same path and creation time, across a
  restart included. Only the 256 newest files created in the 2 seconds before the
  position are remembered, so a crash or restart can repeat files beyond that.
- The position moves only after a delivery succeeds. It never passes the wall
  clock. A failed `file write complete` delivery is retried and does not hold it.
- After a buffer overflow or a failed delivery the position stays put and the
  tree is scanned again, at most `max_rescans` times an hour and a minute apart.
  A restart during that time can repeat files.
- A file created more than 5 minutes ahead of the clock is reported live and
  skipped by scans, so it does not come back at every restart.

## Not supported

These are known gaps, not defects to fix in a configuration:

- A file moved in from the same volume while the collector is down. It keeps its
  creation time, so a scan cannot tell it from an old file.
- A clock set back. Files created after the jump are below the position; a scan
  misses them and the live watch sees them.
- A restore that keeps the creation time, such as `robocopy /COPY:T`.
- A file deleted and created again under the same name within about 15 s. NTFS
  keeps the old creation time. The live watch reports it; a scan does not.
- Network roots. They are best effort. A service account sees no mapped drive
  letters; use a UNC path and set `rescan_interval`.

## Development

```bash
go test -race ./...
GOOS=windows go vet ./... && GOOS=windows go test -c -o /dev/null .
```

The unit suite runs on any system against a fake watcher and a fake tree. The
files `*_windows_test.go` run only on Windows and need a real NTFS volume.
