
# flowlog exporter

The `flowlog` exporter writes the records of the `netflow` receiver as typed rows into a ClickHouse table, `flowlogs.flow_log` by default. The contrib ClickHouse exporter writes only the OpenTelemetry log schema, so it cannot fill this table. The field mapping is Go code in this module, not an OTTL or `transform` block.

One delivered batch is one prepared native batch with one append per row and one send. The `INSERT` statement names only the columns that some row of the batch fills; every other column, `loaded_at` included, takes the table default. The exporter issues `INSERT` statements and nothing else.

## Configuration

| Key | Default | Meaning |
|---|---|---|
| `endpoint` | none | `host:port` of the ClickHouse native protocol |
| `database`, `table` | `flowlogs`, `flow_log` | the flow table |
| `username`, `password` | none | an insert-only account; set the password as `${env:NAME}` |
| `password_variable` | none | the name `NAME` of that variable, used only to name it in the warning for an empty password; a name, never the value |
| `loader` | `flowlogexporter` | written to the `loader` column |
| `collector` | the process host name | written to the `collector` column; the gateway pod name |
| `routers` | none | site data per exporter address: `address`, `account_id`, `region`, `sampling_rate`, `direction` |
| `sending_queue`, `retry_on_failure`, `timeout` | standard | the queue is bounded; a retry has a time limit |

`routers` is held in memory from the collector configuration. A record from an address that is not listed is written with empty `account_id`, `region` and `flow_direction` and sampling rate 1, counted in `otelcol_flowlog_unknown_exporter`, with one warning per address.

## Mapping

| Column | Source |
|---|---|
| `srcaddr`, `dstaddr`, `srcport`, `dstport` | `source.address`, `destination.address`, `source.port`, `destination.port` |
| `protocol` | `network.transport`, a protocol name or a number, to the IANA number; an unknown name gives 0 |
| `bytes`, `packets` | `flow.io.bytes`, `flow.io.packets`, as exported (not multiplied by the sampling rate) |
| `input_interface`, `output_interface` | `flow.in_if`, `flow.out_if` |
| `start_time`, `end_time` | `flow.start`, `flow.end`, cut to the second; a zero or out-of-range value gives the second of `flow.time_received` for both |
| `received_at` | `flow.time_received` |
| `dscp` | `flow.ip_tos` shifted right by 2 |
| `sampling_rate` | `flow.sampling_rate` when above 0, else the router value, else 1 |
| `action` | `flow.forwarding_status`: top two bits 1 give `ACCEPT`, 2 give `REJECT`, anything else is empty |
| `exporter` | `flow.sampler_address` |
| `flow_type` | `flow.type`: `netflow_v5`, `netflow_v9`, `ipfix` |
| `ip_version` | 4 or 6, from the source address |
| `cloud` | the constant `onprem` |
| `account_id`, `region`, `flow_direction` | the router entry; `flow.direction` wins for the direction |
| `loader`, `collector` | the configuration |
| `tcp_flags`, `vlan_id`, `next_hop`, `src_asn`, `dst_asn` | `flow.tcp_flags`, `flow.vlan_id`, `flow.next_hop`, `source.as.number`, `destination.as.number` |
| `sequence_num`, `src_mac`, `dst_mac`, `icmp_type`, `icmp_code` | `flow.sequence_num`, `flow.src_mac`, `flow.dst_mac`, `flow.icmp_type`, `flow.icmp_code`, filled only when the attribute is present |

The receiver fields without a column (prefix lengths, TTL, IP flags, fragment fields, flow label, observation ids and next hop AS) are dropped.

## Counters

| Counter | Labels | Counts |
|---|---|---|
| `otelcol_flowlog_rows_inserted` | none | rows inserted |
| `otelcol_flowlog_rows_dropped` | `reason`: `queue_full`, `insert_failed`, `schema`, `invalid` | rows dropped |
| `otelcol_flowlog_unknown_exporter` | none | records from an address not in `routers` |
| `otelcol_flowlog_mapping_warnings` | `reason`: `bad_time`, `unknown_protocol` | records mapped with a default |
| `otelcol_flowlog_config_warnings` | `reason`: `empty_password` | configuration warnings at start |

A full queue drops the batch at once, so the receiver and the other pipelines keep running. A failed insert is retried with back-off until `retry_on_failure.max_elapsed_time`, then dropped. A schema error (an unknown column, a type mismatch) drops the batch at once and warns once until the next success. A warning about dropped rows holds the row count and the error, never row content.

## Start and secrets

The exporter starts when ClickHouse is not reachable: it logs one warning, connects in the background and inserts once it can; rows wait in the queue. An empty password logs one warning and counts `empty_password`; the exporter still starts. With `password_variable` set, the warning names that variable; without it the warning points at the password setting. The password is held as a `configopaque.String` and appears in no log, error, configuration dump or debug output.

## Table and grant

The table, the router lookup table and the writer account are created outside this repository. The writer needs `INSERT` on `flowlogs.flow_log` and nothing else.
