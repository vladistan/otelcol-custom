<!-- cspell:ignore vstan -->

# OpenVPN status receiver

The `openvpnstatus` receiver polls an OpenVPN status file and, for each new snapshot, emits metrics for every connected client. Log records, one per client, are available with `output`. It is in the Linux edge builds only; the Windows edge build does not carry it.

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
| `join_routing_table` | `false` | Adds `virtual_address` and `last_ref` from the routing table to each log record. It has no effect on metrics. |
| `output` | unset | Unset, or `metrics`, `logs` or `both`. |

Leave `output` unset unless a pipeline must be refused. Unset, the receiver emits the signal of each pipeline it is in: a logs pipeline gets log records, a metrics pipeline gets metrics, and a receiver in both gets both. A 0.3.13 config, which lists the receiver in a logs pipeline and has no `output`, keeps emitting logs; add a metrics pipeline to get metrics. The 0.3.13 binary does not know the key, so a config that sets `output` cannot be rolled back to it unchanged.

A named `output` restricts the receiver. A pipeline of a signal it leaves out is refused when the collector starts, with a message naming `output`. Each pipeline gets its own copy of the receiver, which reads the file once for itself, so with both signals a problem is logged once for each.

Before 0.3.14 the receiver emitted logs only. Its stability is development.

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

## Metrics

Each new snapshot gives these metrics, one point per client. Every point carries the attributes `user.name` and `client.address`, and its time is the snapshot time. A client that OpenVPN shows as `UNDEF` also carries `unauthenticated` true. The metrics carry no port: the byte totals of all sessions from one address are added into one series.

| Metric | Type | Unit | Value |
|---|---|---|---|
| `openvpn.client.bytes_received` | sum, cumulative, monotonic | `By` | Bytes received from the client in this session. |
| `openvpn.client.bytes_sent` | sum, cumulative, monotonic | `By` | Bytes sent to the client in this session. |
| `openvpn.client.connected_since` | gauge | `s` | Start of the session, in Unix seconds. An absolute time, not a duration; a backend that turns the unit into a name suffix shows `_seconds`. |

`client.address` is the client's IP address. The metric attributes drop the port, a protocol prefix such as `udp4:` is ignored, and an IPv4-mapped IPv6 address (`::ffff:192.0.2.1`) is written as plain IPv4. The warning for a row whose address is not an IP address does not repeat the address. A row whose address is not an IP address is skipped like any row that cannot be parsed.

OpenVPN restarts the byte counts at zero for each session. The two sums therefore start at the session start: the start time of each point is the connect time, so a reconnect shows as a counter reset. The connect time is the `Connected Since (time_t)` column of status versions 2 and 3, and otherwise the `Connected Since` text read as time in the collector host's local time zone. When neither can be read, the points have no start time and the gauge has no point for that client.

Status version 1, the default of the `status` directive, has no epoch: its snapshot time and connect times are zone-less text, read in the collector host's time zone. A collector in another zone than the server shifts every time by the difference, and a change of daylight saving time shifts the start of every session once, which reads as a counter reset. Set `status-version 2` or `3` in the OpenVPN server config for metrics; they carry epochs and avoid both.

The connect time is a gauge, not an attribute, because an attribute would start a new series for both byte counters at every reconnect, and the text in the file has no time zone.

Two sessions of one `user.name` from one IP address, for example clients behind one NAT, share one series: the bytes are added, the start is the earlier connect time. The port was the only thing that told them apart. When the later session ends, the sum falls and the start does not change, so a backend sees a decrease without a reset marker: a Prometheus-style `rate()` reads a reset and counts the remaining total as new traffic once. When the earlier session ends, the start moves forward and the reset is seen.

The IP address is part of the series. A client that changes its address, such as a roaming laptop, starts new series for all three metrics, and the old ones stop.

A snapshot with no clients emits nothing, so the series of a client that left simply stop.

## Logs

With `output: logs` or `both`, each client row becomes one log record with an empty body. The record timestamp is the snapshot time.

| Attribute | Type | Value |
|---|---|---|
| `user.name` | string | The client certificate name; `UNDEF` when OpenVPN has none. |
| `unauthenticated` | bool | true when the name is `UNDEF`; absent otherwise. |
| `client.address` | string | The client's IP address, without the port. |
| `client.port` | int | The port of the client address, when the row has one. |
| `vstan.openvpn.bytes_received` | int | Bytes received from the client. |
| `vstan.openvpn.bytes_sent` | int | Bytes sent to the client. |
| `vstan.openvpn.connected_since` | string | The date text from the file, unchanged. |
| `virtual_address` | string | With `join_routing_table` only. |
| `last_ref` | string | With `join_routing_table` only. |

The join matches a routing row on the client name and the full client address as written in the file, port included, so the join is exact even though the address is emitted without the port. A client with no row at its own address gets neither attribute, so a second session of one name never takes the first session's row.

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
| A row that cannot be parsed, for example a comma in a common name or an address that is not an IP address | That row is skipped; the other clients are emitted. |
| A v2 or v3 row type the receiver does not know | That row is skipped; the other clients are emitted. |
| File larger than 1 MiB (1,048,576 bytes) | Not loaded; nothing emitted. A file of exactly 1 MiB is loaded. |

A skipped row leaves its client out of the snapshot, so a record count below the client count is possible. The warning is the only signal. The 1 MiB cap is fixed; it stops a `path` that points at a large log from being read into memory.
