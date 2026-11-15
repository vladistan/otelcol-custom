# OpenVPN status receiver

The `openvpnstatus` receiver polls an OpenVPN status file and emits one log record per connected client for each new snapshot. It is in the Linux edge builds only; the Windows edge build does not carry it.

## Config

```yaml
receivers:
  openvpnstatus:
    path: /var/log/openvpn/status.log
    poll_interval: 20s
    join_routing_table: false
```

| Key | Default | Meaning |
|---|---|---|
| `path` | none, required | The status file the OpenVPN server writes (`status` directive). |
| `poll_interval` | `20s` | How often the file is read. Must be positive. |
| `join_routing_table` | `false` | Adds `virtual_address` and `last_ref` from the routing table to each record. |

The receiver emits logs only. Its stability is development.

## Supported formats

The format is detected from the file content, so no version key is needed.

| Format | Written by | Notes |
|---|---|---|
| v1 | `status` with no version, or `status-version 1` | Comma-separated sections `OpenVPN CLIENT LIST`, `ROUTING TABLE` and `GLOBAL STATS`, ended by `END`. |
| v2 | `status-version 2` | Comma-separated `TITLE`, `TIME`, `HEADER`, `CLIENT_LIST` and `ROUTING_TABLE` rows. |
| v3 | `status-version 3` | The same rows as v2, tab-separated. |

v2 and v3 are read in both csv and tab form. Columns are found by the names in the `HEADER` rows, so a column added by a newer OpenVPN does not move the others.

The v1 `Updated` line is accepted in two layouts: `2026-11-13 10:00:00` and the older `Thu Jan  3 21:40:24 2019`. Both are read in the collector host's local time zone. v2 and v3 carry an epoch on the `TIME` row, which is used as is.

A UTF-8 byte order mark at the start of the file is ignored.

## Records

Each client row becomes one log record with an empty body. The record timestamp is the snapshot time.

| Attribute | Type | Value |
|---|---|---|
| `common_name` | string | The client certificate name; `UNDEF` when OpenVPN has none. |
| `real_address` | string | The client's address and port. |
| `bytes_received` | int | Bytes received from the client. |
| `bytes_sent` | int | Bytes sent to the client. |
| `connected_since` | string | The date text from the file, unchanged. |
| `virtual_address` | string | With `join_routing_table` only. |
| `last_ref` | string | With `join_routing_table` only. |

The join matches a routing row on `common_name` and `real_address`. A client with no row at its own address gets neither attribute, so a second session of one name never takes the first session's row.

## Snapshots and restarts

The file is rewritten in place, so a read can land on a half-written snapshot. A snapshot without its `END` line is skipped and read again at the next poll. A snapshot whose `Updated` text or `TIME` epoch is the same as the last one emitted is not emitted again.

A collector restart forgets the last snapshot, so it emits the current snapshot once. Each restart therefore repeats one snapshot of records downstream.

## Problems and warnings

The receiver logs one warning each time the kind of problem changes, not one per poll.

| Input | Result |
|---|---|
| File missing or unreadable | Nothing emitted; read again at the next poll. |
| Empty file | Nothing emitted, no warning. |
| No `END` line | The whole snapshot is skipped. |
| A row that cannot be parsed, for example a comma in a common name | That row is skipped; the other clients are emitted. |
| A v2 or v3 row type the receiver does not know | That row is skipped; the other clients are emitted. |
| File larger than 1 MiB (1,048,576 bytes) | Not loaded; nothing emitted. A file of exactly 1 MiB is loaded. |

A skipped row leaves its client out of the snapshot, so a record count below the client count is possible. The warning is the only signal. The 1 MiB cap is fixed; it stops a `path` that points at a large log from being read into memory.
