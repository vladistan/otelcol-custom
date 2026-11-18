# SNMP and NetFlow on the Linux edge

The Linux edge and static Linux edge builds include two contrib receivers at v0.164.1, both alpha:
`snmp` (metrics) and `netflow` (logs). The scrape image includes `snmp` only, from 0.3.16 (see
[the scrape collector](scrape-collector.md#proxmox-ve-and-snmp-pollers)); the Windows edge and the gateway include
neither.
Sample configs: `test/configs/edge-snmp.yaml` and `test/configs/edge-netflow.yaml`.

## snmp

One `snmp` instance polls one device, so a config has one named instance per device (`snmp/firewall`,
`snmp/qnap`). The role writes the instances; there is no discovery.

- **Versions and authentication:** v1, v2c and v3. For v3 set `user`, `security_level` (`no_auth_no_priv`,
  `auth_no_priv`, `auth_priv`), `auth_type` (MD5, SHA, SHA224, SHA256, SHA384, SHA512) and `privacy_type`
  (DES, AES, AES192, AES256, AES192c, AES256c). The sample has an instance with `auth_priv`, SHA and AES.
- **Secrets:** `community`, `auth_password` and `privacy_password` take `${env:NAME}`. A literal value in a
  sample config fails `scripts/check-snmp-secrets.sh`, which runs with the other checks.
- **Metrics:** each metric in the config names its OIDs. A scalar OID ends in `.0`. A column OID gives one
  data point per table row; `attributes` take their value from another column of the same table (the
  receiver reads string columns such as interface names, system names and LUN names as attribute values) or
  from the row index. The metric value itself must be numeric.
- **Load:** `collection_interval` defaults to 10 s, `timeout` to 5 s and `max_repetitions` to 25 (GETBULK,
  v2c and v3). Use 60 s or more for lab devices.
- **A device that is down:** the receiver connects at each scrape, so it starts without the device. Each
  scrape then logs one error and returns no data; the collector keeps running. An OID that cannot be read
  at start is a warning. An invalid config is rejected at load.

### QNAP iSCSI target side

The QNAP instance in the sample reads numeric sensors and iSCSI target metrics (target status, connection
count, LUN capacity and use). **The iSCSI OIDs are unverified.** They come from third-party pages for the
QNAP NAS-MIB (enterprise arc `1.3.6.1.4.1.24681`), which disagree on some paths, and QNAP states that its
SNMP storage data comes from volumes, not from storage pools or LUNs. Entries marked `PLACEHOLDER` in the
sample are column numbers still to be confirmed. Get the MIB for the firmware of the device, run
`snmptranslate` on the iSCSI objects, and poll the device once before using the instance.

### LLDP neighbours

The sample for a switch labels a numeric column of `lldpRemTable` (the time mark) with the string columns
of the same row (system name, port id). The table index is `timemark.localport.index`. **That the receiver
joins the string columns to the numeric one on that index is unverified** until a device is polled.

## netflow

One `netflow` instance listens on one UDP port for one scheme: `netflow` (NetFlow v5, v9 and IPFIX) or
`sflow` (sFlow v5 flow samples; counter samples are not supported). Keys: `scheme`, `hostname` (the address
to bind), `port` (default 2055), `sockets`, `workers`, `queue_size` (at least 1000) and `send_raw`. Each
flow becomes one log record with about 30 attributes, such as `source.address`, `source.port`,
`destination.address`, `destination.port`, `network.transport`, `flow.io.bytes`, `flow.io.packets`,
`flow.in_if`, `flow.out_if` and `flow.sampling_rate`. There is no custom field mapping. A port above 1024
needs no privilege.

**Bytes and packets of NetFlow v9.** The receiver in this collector is the contrib receiver with a local patch
(`receiver/netflowreceiver`). The upstream decoder takes `IN_BYTES` and `OUT_BYTES` (and the packet pair) into
one value, so an exporter that sends the `OUT_*` fields after the `IN_*` fields with 0 in them, as OPNsense
and FreeBSD `ng_netflow` do, gave `flow.io.bytes` 0 and `flow.io.packets` 0 for every flow. The patch adds
the `OUT_*` counter to the `IN_*` counter of the same v9 record. An exporter that sends real values in both
now reports the sum of both. IPFIX records are not changed. Flows recorded before the fix keep their 0.

**NetFlow has no authentication.** Any host that can reach the port can send records. Bind one address with
`hostname` and allow only the exporting device in the firewall.

**A port that is already in use does not stop the collector.** `netflow` is one of the optional receivers:
a failed bind logs one WARN, counts `otelcol_optional_receiver_skipped`, and the bind is retried in the
background (see the README section on optional receivers).

### Flow volume

One record is made per flow, so the volume depends on the traffic. Measure it before sizing storage:

1. Run the receiver for 24 hours with the collector's own metrics on (`service.telemetry.metrics.level`
   above `none`).
2. Read `otelcol_receiver_accepted_log_records{receiver="netflow"}` and take its rate per second.
3. Records per day = the rate per second multiplied by 86,400.

The size of one stored record depends on the store; measure it there.

### Cutting the size of a record

Most flow analysis does not use the fragment, flow-label, TTL, ICMP and ToS fields. Delete them before
export with an `attributes` processor (the sample does) or a `transform` processor:
`flow.fragment_id` and `flow.fragment_offset`, `flow.ipv6_flow_label`, `flow.ip_ttl`, `flow.icmp_type` and
`flow.icmp_code`, `flow.ip_tos` and `flow.ip_flags`. Sampling on the exporting device lowers the rate
without a processor; `flow.sampling_rate` carries the rate.

## Size

The two receivers together add the size in the CHANGELOG to the static Linux edge. They pull in two new
modules, `gosnmp` and `goflow2`, and change no core, contrib or SDK version.
