
# Central NetFlow flow log

The gateway image reads NetFlow v5, v9 and IPFIX flow records with the `netflow` receiver and writes them as typed rows into the ClickHouse table `flowlogs.flow_log` with the `flowlog` exporter. The flow records of one site then sit in the same table as the cloud flow logs, told apart by the `cloud` column.

## Why a separate exporter

The contrib ClickHouse exporter writes only the OpenTelemetry log schema (one table of log records with attribute maps). The flow table has 60 typed columns, so the `flowlog` exporter maps each record to those columns in Go and writes them with the ClickHouse native protocol. There is no OTTL, `transform` or stanza block in the flow pipeline. The exporter issues `INSERT` statements and nothing else.

## What is decoded

- The receiver runs with scheme `netflow` on UDP 2055. sFlow is not decoded, and no earlier flow history is loaded: the table fills from the day the pipeline runs.
- The flow time comes from the absolute `flowStartMilliseconds` and `flowEndMilliseconds` fields of a Flexible NetFlow v9 export (fields 152 and 153) when the record has them, and otherwise from the first and last switched times relative to the exporter uptime. A v9 record with neither form has start and end 0, and the exporter writes the second of the receive time for both and counts it as `bad_time`.
- A v9 record that carries the IN and OUT counters reports the sum of the two.
- The header time of some exporters runs a second ahead of the receive time. The row takes the flow times, not the header time.

## Reaching the gateway

UDP 2055 reaches the gateway through the `otel-lg` load balancer with `externalTrafficPolicy` set to `Local`. That setting keeps the source address of each exporter, which the router lookup and the `exporter` column need. The Helm change is the cluster owners'.

## Restart gap and one replica

NetFlow v9 and IPFIX data records can be decoded only after the exporter has sent the template of their data set. The receiver keeps no template state across restarts, so after every gateway restart the data records of an exporter are dropped until that exporter sends its templates again. Each such record is counted in `otelcol_netflow_template_not_found` and writes no row. The gap lasts as long as the template refresh interval of the exporter. The gateway stays one replica, because a second replica would see only part of the records of an exporter.

## Table, account and grant

The 60 columns are final. The table, the router lookup table and the writer account are created by the database owners and live outside this repository. The writer needs `INSERT` on `flowlogs.flow_log` and nothing else. The columns the exporter does not fill, `loaded_at` included, take the table default, and the statement does not name them.

## Mapping

| Column | Source |
|---|---|
| `srcaddr`, `dstaddr`, `srcport`, `dstport` | `source.address`, `destination.address`, `source.port`, `destination.port` |
| `protocol` | `network.transport` (a protocol name or a number) as the IANA number; an unknown name gives 0 |
| `bytes`, `packets` | `flow.io.bytes`, `flow.io.packets`, as exported; readers multiply by `sampling_rate` |
| `input_interface`, `output_interface` | `flow.in_if`, `flow.out_if` (interface numbers; names come from an SNMP join later) |
| `start_time`, `end_time` | `flow.start`, `flow.end`, cut to the second |
| `received_at` | `flow.time_received` |
| `dscp` | `flow.ip_tos` shifted right by 2 |
| `sampling_rate` | `flow.sampling_rate` when above 0, else the router value, else 1 |
| `action` | `flow.forwarding_status`: the top two bits 1 give `ACCEPT`, 2 give `REJECT`, anything else is empty |
| `exporter` | `flow.sampler_address` |
| `flow_type` | `netflow_v5`, `netflow_v9` or `ipfix` |
| `ip_version` | 4 or 6, from the source address |
| `cloud` | the constant `onprem` |
| `account_id`, `region`, `flow_direction` | the router entry of the exporter address |
| `loader`, `collector` | the exporter keys `loader` and `collector` (the gateway pod name) |
| `tcp_flags`, `vlan_id`, `next_hop`, `src_asn`, `dst_asn` | `flow.tcp_flags`, `flow.vlan_id`, `flow.next_hop`, `source.as.number`, `destination.as.number` |
| `sequence_num`, `src_mac`, `dst_mac`, `icmp_type`, `icmp_code` | `flow.sequence_num`, `flow.src_mac`, `flow.dst_mac`, `flow.icmp_type`, `flow.icmp_code`, filled only when the attribute is present |

Receiver fields without a column are dropped: prefix lengths, TTL, IP flags, fragment fields, the IPv6 flow label, observation ids and next hop AS.

## Router lookup

The exporter key `routers` lists the exporters by address, each with `account_id`, `region`, `sampling_rate` and `direction`. It is held in memory from the collector configuration. A record from an address that is not listed gets empty `account_id`, `region` and `flow_direction` and sampling rate 1, and is counted in `otelcol_flowlog_unknown_exporter`; the exporter logs one warning per address.

## Counters

| Counter | Labels | Counts |
|---|---|---|
| `otelcol_flowlog_rows_inserted` | none | rows inserted |
| `otelcol_flowlog_rows_dropped` | `reason`: `queue_full`, `insert_failed`, `schema`, `invalid` | rows dropped |
| `otelcol_flowlog_unknown_exporter` | none | records from an address that is not in `routers` |
| `otelcol_flowlog_mapping_warnings` | `reason`: `bad_time`, `unknown_protocol` | records mapped with a default |
| `otelcol_flowlog_config_warnings` | `reason`: `empty_password` | configuration warnings at start |
| `otelcol_netflow_template_not_found` | none | data records dropped for an unknown template |

The pipeline has its own bounded `sending_queue` and a `retry_on_failure` with a time limit. A full queue drops the batch at once, so the receiver and every other pipeline keep running. A failed insert is retried with back-off, then dropped. A schema error drops the batch at once and warns once until the next success.

## Start and password

The gateway starts when ClickHouse is not reachable: the exporter logs one warning, connects in the background and inserts once it can; rows wait in the queue and drop when it is full. The password is a `${env:NAME}` value, delivered as a Kubernetes Secret environment variable like the other exporter credentials. A missing variable resolves to an empty string with a collector warning. The exporter then logs one warning, counts `empty_password` and starts. The warning names the variable when the exporter has `password_variable: NAME` set; without that key it points at the password setting. The password never appears in a log, an error message, a configuration dump or `debug` output.

## Reference config

`test/configs/gateway-flowlog.yaml` holds the three blocks of the flow pipeline: the `netflow` receiver, the `flowlog` exporter and the pipeline `logs/flowlog`, with placeholders for the endpoint, the account and the password variable. Only that pipeline uses the receiver and the exporter.
