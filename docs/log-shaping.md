<!-- cspell:ignore slift vstan logfmt NOERROR awselb gcpaudit mosquitto kaudit apparmor eksaudit gcplb gcpjson oauth2proxy coredns stallcheck smartd serialconsole stderrhint lldpd litellm ssmagent UPID vzdump pmkr hostapd CLSM hwmap -->

# Log shaping (`logshape` processor)

`logshape` turns the raw text of known log formats into records with an official OpenTelemetry shape: a one-line body, official attribute names, one severity table per format and the time of the line. A record is claimed by exactly one parser, called a branch. A record no branch claims passes through unchanged.

Put it in a logs pipeline after the receivers and after any processor that joins multi-line records (`logrecombine`), and before the exporters:

```yaml
processors:
  logshape: {}   # every branch on, every default from code

service:
  pipelines:
    logs:
      receivers: [filelog, syslog]
      processors: [logrecombine, logshape]
      exporters: [otlphttp]
```

## How a record is handled

1. The detector offers the record to the branches in the order of the table below. A cheap check per branch (first byte, minimum length, required text, a `log.source` value or an attribute key) skips branches that cannot claim it.
2. The first branch that claims the record parses it on a copy. When the parse works, the copy replaces the record. When it fails, the record stays as it was and `otelcol_logshape_parse_failed` counts it.
3. A record no branch claims passes unchanged and `otelcol_logshape_unmatched` counts it.
4. A branch may change the resource (`host.name`, `service.name`) or the scope name. The processor then puts each record under the resource and scope that carry what the branch left, so two records of one batch never share a value that belongs to only one of them.
5. A panic inside a branch never reaches the collector. The processor recovers it, leaves the record as it was, counts it in `otelcol_logshape_parse_failed` for that branch and logs it once for each branch, with the stack.

The helpers live under `shape/`, each its own Go module. They import nothing from the processor.

| Module | What it holds |
|---|---|
| `severity` | one level table per source (word, klog, mongod, ClickHouse, PostgreSQL, pino, Redis), HTTP status and DNS response code mapping, number to text |
| `strip` | ANSI codes, escaped control characters, serial console clean-up, blank-line and prefix checks |
| `rename` | the table of old names and their official names, the integer attribute list, the deprecated name list |
| `body` | one-line body builders: `[component] text`, HTTP and audit lines, byte and duration formatting |
| `multiline` | start and continuation rules, exception splitting, line joining |
| `k8slift` | Kubernetes pod status and object event parsing, node name from a static pod name |
| `principal` | short service names from principals, account aliases, backend service and load balancer names |
| `httpaccess` | the access line parsers (nginx, Influx, oauth2-proxy, uvicorn) and the first `X-Forwarded-For` entry |

## Branches

The detector tries the branches in this order. The first one that claims a record shapes it.

| Branch | Claims | Replaces |
|---|---|---|
| `flow` | a record with `flow.src_as` or `flow.dst_as` | the AS names of the netflow receiver |
| `klog` | a Kubernetes klog line (`I1119 23:05:39.184859 1 file.go:214] "message"`) | `klogprocessor` |
| `uvicorn` | a line that starts with a uvicorn level prefix | `uvicornlogprocessor` |
| `collector` | a collector console line whose fields name a component (`otelcol.component.id`), also in a syslog wrapper, or a node-logs row that holds the line in `original_message` | none |
| `mongod` | a mongod structured JSON line (`t`, `s`, `c`, `id`, `ctx`, `msg`, `attr`) | none |
| `gcpjson` | a GCP structured JSON line with `severity` and `message` | none |
| `tracing` | a Rust tracing JSON line with `fields`, `level` and `target` | none |
| `pino` | a pino JSON line with a numeric `level` and `time`, with an optional `[TAG]` prefix | none |
| `logrus` | a logrus, zap or slog JSON line, a logrus text line, a zap console line | `logruslogprocessor` |
| `postgres` | a PostgreSQL server line (`2026-11-19 22:42:20.918 UTC [815116] LOG:  text`) | `postgreslogprocessor` |
| `redis` | a Redis server line (`1:M 19 Nov 2026 23:22:37.151 * text`) | `redislogprocessor` |
| `nginx` | a combined access line | `nginxlogprocessor` |
| `sentry` | a Sentry line (`02:30:21 [INFO] logger: text`) | `sentrylogprocessor` |
| `cisco` | a Cisco, HP switch or WLC line, an access point kernel line behind a device clock (`[*11/19/2026 23:50:11.1234] text`), or a record already marked `cisco_facility`; the clock is stripped, and a routine message of the `cisco.access_point_downgrade` list drops from WARN to INFO (a list set in config replaces the built-in one) | `ciscologprocessor` |
| `syslog` | a record with a syslog line in `message`, a `priority`, or a syslog-shaped body; a wrapped line is shaped again by the branch that claims the inner text | `syslogprocessor` |
| `influx` | an InfluxDB `[httpd]` access line | none |
| `oauth2proxy` | an oauth2-proxy access line (client, user, time, upstream, method, path, status, size, duration) | none |
| `coredns` | a CoreDNS query log line | none |
| `gcplb` | a record with `log.source` `gcp.lb` | none |
| `awselb` | a record with `log.source` `aws.elb` | none |
| `s3access` | a record with `log.source` `aws.s3access` and an empty body | none |
| `gcpaudit` | a record with `log.source` `gcp.audit` | none |
| `eksaudit` | a record with `log.source` `aws.eks` whose body is a Kubernetes audit event (JSON with `verb` or `auditID`); other `aws.eks` lines pass through | none |
| `cloudtrail` | a record with `log.source` `aws.cloudtrail` | none |
| `rabbitmq` | a rabbitmq line (`2026-11-20 00:27:56.299641+00:00 [info] <0.1.0> text`), with or without ANSI colour codes | none |
| `mosquitto` | a Mosquitto connection, client or bridge line with a UTC time and no level | none |
| `stallcheck` | the stall-check job line `published StallMinutes=2`, with or without the container runtime prefix | none |
| `kaudit` | a kernel audit line that carries an AppArmor decision | none |
| `smartd` | a smartd line (`Device: /dev/ada3, text`) | none |
| `gocd` | GoCD Java wrapper lines (`jvm 1 \| time LEVEL [thread] Class:line - text`), one or several in a record | none |
| `cron` | a body that starts with `cron: `; the tag becomes the service | none |
| `openvpn` | an `openvpnstatus` record with an empty body, a user, a `bytes_received` and a `bytes_sent` | none |
| `pbs` | a Proxmox Backup Server task log line in a file named by the task id | none |
| `pve` | a whole Proxmox VE task log in one record, ending in a `TASK` result line | none |
| `serialconsole` | a record of a GCE serial console (`cloud.resource_id` names `serialconsole.googleapis.com`) | none |
| `logfmt` | a logfmt line with `msg=`, `level=` or `lvl=`, and `logger=`, `component=`, `module=`, `t=` or `ts=`, but not `time=` | none |
| `clickhouse` | a ClickHouse server line (`2026.11.19 23:01:21.644520 [ 17 ] {} <Information> Logger: text`) | none |
| `calico` | a calico node line (`2026-11-19 22:53:59.699 [INFO][73] felix/summary.go 100: text`) | none |
| `ssmagent` | an amazon-ssm-agent line (`2026-11-19 23:00:14.0871 WARN Component text`) | none |
| `litellm` | a litellm proxy line (`23:01:21 - LiteLLM Proxy:INFO: file.py:12 - text`), with or without colour codes | none |
| `stderrhint` | the last text branch: a journal record of a container (`log.source` `journal`, `container.*` in the resource) with the severity ERROR that no branch found a level for | none |
| `lldpd` | an lldpd neighbour record, a JSON body with `interface`, `chassis_id` and `port_id` | none |
| `k8s` | a Kubernetes pod status line, an event as a JSON object or as `k8s.event.*` attributes, or a record with Kubernetes lift keys | none |

A branch claims by the shape of the line. The old per-service filters are gone.

## Examples

Each example is one record through the full branch table with the default configuration. Addresses and names are replaced with documentation ranges. A severity of "unset" means the record keeps no severity, because the line carries no level. Attributes listed are the ones the branch writes; `log.record.original` is kept when the branch sets it and is left out here.

**`flow`** claims by attributes, and renames the AS fields in place.

```text
in:    flow.src_as=64496  flow.dst_as=64497  flow.type=netflow_v9  source.address=192.0.2.5
body:  (empty)
sev:   unset
attrs: source.as.number=64496  destination.as.number=64497  flow.type=netflow_v9
```

**`klog`**

```text
in:    W1119 23:05:40.001000       1 reflector.go:561] "Watch failed" err="context deadline exceeded"
body:  Watch failed err="context deadline exceeded"
sev:   WARN
attrs: code.file.path=reflector.go  code.line.number=561  process.pid=1
```

**`uvicorn`**

```text
in:    WARNING:  192.0.2.11:8080 - "POST /api/submit HTTP/1.1" 400 Bad Request
body:  POST /api/submit 400
sev:   WARN
attrs: client.address=192.0.2.11  client.port=8080  http.request.method=POST  http.response.status_code=400
       url.path=/api/submit  network.protocol.name=http  network.protocol.version=1.1  vstan.uvicorn.status_text=Bad Request
```

**`collector`**

```text
in:    2026-11-06T21:12:29.299Z	info	otlpreceiver@v0.164.0/otlp.go:175	Starting HTTP server	{"otelcol.component.id": "otlp", "otelcol.component.kind": "receiver", "endpoint": "127.0.0.1:14418"}
body:  Starting HTTP server
sev:   INFO
attrs: code.file.path=otlpreceiver/otlp.go  code.line.number=175  otel.component.name=otlp  vstan.collector.endpoint=127.0.0.1:14418
```

**`mongod`**

```text
in:    {"t":{"$date":"2026-11-19T22:45:01.000+00:00"},"s":"I","c":"ACCESS","id":20250,"ctx":"conn1131181","msg":"Authentication succeeded","attr":{"mechanism":"SCRAM-SHA-256","principalName":"__system","remote":"192.0.2.10:53720"}}
body:  Authentication succeeded principal=__system remote=192.0.2.10:53720
sev:   INFO
attrs: user.name=__system  client.address=192.0.2.10  client.port=53720  vstan.mongod.id=20250  vstan.mongod.ctx=conn1131181
       vstan.mongod.mechanism=SCRAM-SHA-256
```

**`gcpjson`**

```text
in:    {"message":"slow request","severity":"WARNING","timestamp":"2026-11-19T22:42:01Z","logging.googleapis.com/sourceLocation":{"file":"a/b.go","line":"42","function":"pkg.Run"}}
body:  slow request
sev:   WARN
attrs: code.file.path=a/b.go  code.line.number=42  code.function.name=pkg.Run
```

**`tracing`**

```text
in:    {"timestamp":"2026-11-19T22:41:04.100000Z","level":"INFO","fields":{"message":"Inserted 2 rows","rows":2},"target":"rust_consumer::strategies::writer"}
body:  Inserted 2 rows
sev:   INFO
attrs: vstan.tracing.rows=2   (the target becomes the scope name)
```

**`pino`**

```text
in:    [JOB-RUNNER] {"level":50,"time":1795129202193,"pid":19,"hostname":"app-5d4f7","error":{"name":"TypeError","message":"fetch failed","stack":"TypeError: fetch failed\n    at node:internal/deps/undici"}}
body:  fetch failed
sev:   ERROR
attrs: exception.type=TypeError  exception.message=fetch failed  exception.stacktrace=TypeError: fetch failed ...  process.pid=19
```

**`logrus`** reads logrus JSON and text, zap JSON and console lines, and slog JSON.

```text
in:    {"level":"error","ts":"2025-12-31T04:03:58Z","caller":"controller/reconciler.go:123","msg":"Reconciler error","controller":"backups","error":"boom","stacktrace":"goroutine 1"}
body:  Reconciler error: boom
sev:   ERROR
attrs: code.file.path=controller/reconciler.go  code.line.number=123  exception.message=boom  exception.stacktrace=goroutine 1
       vstan.k8s.controller=backups
```

**`postgres`**

```text
in:    2026-11-19 19:47:24.232 UTC [708688] ERROR:  duplicate key value violates unique constraint "example_key"
body:  duplicate key value violates unique constraint "example_key"
sev:   ERROR
attrs: process.pid=708688
```

**`redis`**

```text
in:    1:S 19 Nov 2026 23:22:38.000 # replica lost
body:  replica lost
sev:   WARN
attrs: process.pid=1  vstan.redis.role=slave   (resource: service.name=redis)
```

**`nginx`**

```text
in:    203.0.113.9 - - [20/Nov/2026:00:26:38 +0000] "POST /api/7/envelope/ HTTP/1.1" 200 41 "-" "sentry.python/2.35.0" "198.51.100.23, 203.0.113.50"
body:  POST /api/7/envelope/ 200 41B
sev:   INFO
attrs: client.address=198.51.100.23  network.peer.address=203.0.113.9  http.request.method=POST  http.response.status_code=200
       http.response.body.size=41  url.path=/api/7/envelope/  user_agent.original=sentry.python/2.35.0
       network.protocol.name=http  network.protocol.version=1.1  vstan.nginx.device_type=bot   (resource: service.name=nginx)
```

**`sentry`**

```text
in:    02:30:21 [INFO] sentry.access.api: api.access (method='GET' view='sentry.web.frontend.home.HomeView' response='302' path='/api/0/projects/' caller_ip='192.0.2.7' user_agent='ELB-HealthChecker/2.0' request_duration_seconds='0.012')
body:  GET /api/0/projects/ 302 12ms
sev:   INFO
attrs: client.address=192.0.2.7  http.request.method=GET  http.response.status_code=302  url.path=/api/0/projects/
       user_agent.original=ELB-HealthChecker/2.0  vstan.http.request.duration=0.012  vstan.sentry.message_type=api.access
       vstan.sentry.view=sentry.web.frontend.home.HomeView   (resource: service.name=sentry)
```

**`cisco`**

```text
in:    661: *Dec 16 00:36:36.024: %SW_MATM-4-MACFLAP_NOTIF: Host 6212.24a7.dd56 in vlan 1 is flapping between port Gi4/0/45 and port Gi4/0/43
body:  (the line, unchanged)
sev:   WARN
attrs: network.interface.name=Gi4/0/45  vstan.cisco.facility=SW_MATM  vstan.cisco.mnemonic=MACFLAP_NOTIF  vstan.cisco.severity=4
       (resource: service.name=SW_MATM)

in:    DownAP-Master: *apfMsConnTask_0: Dec 16 00:00:44.695: %APF-5-CLIENT_ASSOCIATE: apf_80211.c:13338 Client Association: Client MAC: 02:00:5e:00:00:01, AP Name: CiscoAP-Down, Radio: 2.4GHz , WLAN Id: 1.
body:  Client Association
sev:   INFO
attrs: code.file.path=apf_80211.c  code.line.number=13338  thread.name=apfMsConnTask_0  vstan.cisco.facility=APF
       vstan.cisco.mnemonic=CLIENT_ASSOCIATE  vstan.cisco.severity=5  vstan.wifi.ap.name=DownAP-Master
       vstan.wifi.client.ap=CiscoAP-Down  vstan.wifi.radio=2.4GHz  vstan.wifi.wlan.id=1
```

An access point kernel line starts with a device clock, `[*11/20/2026 06:11:26.3538] text`. The branch removes the clock and keeps the original line in `log.record.original`. Access points send these lines at WARN, so a routine message is lowered to INFO. The built-in list has two entries. An entry is a fragment, matched anywhere in the text without regard to case, and only a record that is WARN is changed.

| Fragment | Example line (after the clock) | Result | Why |
|---|---|---|---|
| `pmkr0 lookup` | `hostapd:FT: Skip PMKR0 lookup` | INFO | A routine fast-transition message. |
| `client roamed` | `CLSM[<client mac>]: DELETE_MOBILE payload is ignored - client roamed to other radio in same AP` | INFO | A routine message: the client moved to another radio of the same access point. |

Every other access point line keeps WARN: `DOT11_DRV[0]: Stop Radio0 - Begin` is not on the list and stays WARN.

To change the list, set `cisco.access_point_downgrade` to a list of fragments. A non-empty list replaces the built-in one, so list every fragment you want, including the two above. An empty or missing list keeps the built-in one.

**`syslog`** sets the severity from the priority when there is one. A line taken from the body, with no `message` attribute, is marked `vstan.syslog.parse_error: true`.

```text
in:    Dec 31 10:05:00 alpha sshd[42]: Accepted publickey for deploy
body:  Accepted publickey for deploy
sev:   unset
attrs: log.source=syslog  process.pid=42  vstan.syslog.parse_error=true   (resource: host.name=alpha, service.name=sshd)
```

**`influx`**

```text
in:    [httpd] 192.0.2.21 - webuser [20/Nov/2026:00:02:47 +0100] "POST /query?db=metrics HTTP/1.1" 500 31 "-" "InfluxDBClient" 1d2e3f40-aaaa-bbbb-cccc-ddddeeeeffff 20511
body:  POST /query db=metrics 500 31B 20.5ms
sev:   ERROR
attrs: client.address=192.0.2.21  http.request.method=POST  http.response.status_code=500  http.response.body.size=31  url.path=/query
       url.query=db=metrics  user.name=webuser  user_agent.original=InfluxDBClient  vstan.influx.request_id=1d2e3f40-aaaa-bbbb-cccc-ddddeeeeffff
```

The last field of an Influx line is microseconds.

**`oauth2proxy`**

```text
in:    198.51.100.23 - 7f1e2d3c-aaaa-bbbb-cccc-ddddeeeeffff - webuser [2026/11/20 04:42:12] 203.0.113.46:4180 GET - "/app/" HTTP/1.1 "Mozilla/5.0 (X11)" 302 512 0.123
body:  GET /app/ 302 512B 123ms
sev:   INFO
attrs: client.address=198.51.100.23  http.request.method=GET  http.response.status_code=302  http.response.body.size=512  url.path=/app/
       user.name=webuser  user_agent.original=Mozilla/5.0 (X11)  vstan.oauth2proxy.request_id=7f1e2d3c-aaaa-bbbb-cccc-ddddeeeeffff
```

**`coredns`**

```text
in:    [INFO] 198.51.100.42:56818 - 13677 "A IN example.org. udp 24 false 512" REFUSED qr,aa,rd,ra 24 0.000070318s
body:  REFUSED A example.org. 70us
sev:   WARN
attrs: client.address=198.51.100.42  client.port=56818  dns.question.name=example.org.  vstan.dns.question.type=A
       vstan.dns.response_code=REFUSED  vstan.dns.duration=7.0318e-05
```

**`gcplb`** builds the body from the request attributes. `account_aliases` names the account.

```text
in:    log.source=gcp.lb  http.request.method=get  http.response.status_code=404  url.domain=grafana.example.org  url.path=/missing
       http.request.server.duration=0.012  gcp.load_balancing.status.details=response_sent_by_backend
       (resource: gcp.label.project_id=example-project  gcp.label.backend_service_name=k8s1-0a1b2c3d-monitoring-grafana-3000-4e5f6a7b)
body:  GET grafana.example.org/missing 404 12ms
sev:   WARN
attrs: the input attributes, unchanged   (resource: host.name=example-project  service.name=monitoring-grafana-3000)
```

**`awselb`**

```text
in:    log.source=aws.elb  http.request.method=GET  aws.elb.status.code=502  url.domain=app.example.org
       url.full=https://app.example.org:443/api/v1/bus/status?x=1  aws.elb.target_processing_time=-1
       aws.elb.target_group_arn=arn:aws:elasticloadbalancing:eu-west-1:111122223333:targetgroup/app-tg/0123456789abcdef
       (resource: cloud.account.id=111122223333  cloud.resource_id=app/example-lb/50dc6c495c0c9188)
body:  GET app.example.org/api/v1/bus/status 502
sev:   ERROR
attrs: the input attributes, unchanged   (resource: host.name=example-lb  service.name=app-tg)
```

**`s3access`**

```text
in:    log.source=aws.s3access  rpc.method=REST.GET.OBJECT  aws.s3.key=reports/2026/11/summary.csv  http.response.status_code=200
       http.response.body.size=52428  source.address=192.0.2.7  user.id=arn:aws:iam::111122223333:user/backup-sync
       (resource: aws.s3.bucket=example-bucket  cloud.account.id=111122223333)
body:  REST.GET.OBJECT example-bucket/reports/2026/11/summary.csv 200 51.2KiB
sev:   INFO
attrs: client.address=192.0.2.7 (from source.address)  the other input attributes   (resource: host.name=example-bucket  service.name=backup-sync)
```

**`gcpaudit`**

```text
in:    log.source=gcp.audit  gcp.audit.method.name=io.k8s.core.v1.configmaps.get  rpc.status_code=5  user.email=system:addon-manager
       body: io.k8s.core.v1.configmaps.get core/v1/namespaces/kube-system/configmaps/test-config NOT_FOUND
       (resource: gcp.label.project_id=proj-example)
body:  io.k8s.core.v1.configmaps.get core/v1/namespaces/kube-system/configmaps/test-config NOT_FOUND
sev:   ERROR
attrs: the input attributes, unchanged   (resource: host.name=proj-example  service.name=addon-manager)
```

**`eksaudit`**

```text
in:    log.source=aws.eks
       body: {"kind":"Event","level":"Metadata","verb":"update","auditID":"11111111-2222-3333-4444-5555555555ab","user":{"username":"system:serviceaccount:kube-system:test-autoscaler"},"objectRef":{"resource":"leases","namespace":"kube-system","name":"test-autoscaler"},"responseStatus":{"code":409}}
body:  UPDATE leases kube-system/test-autoscaler 409
sev:   WARN
attrs: http.request.method=UPDATE  http.response.status_code=409  vstan.audit.level=Metadata   (resource: host.name=<cluster name>  service.name=test-autoscaler)
```

**`cloudtrail`**

```text
in:    log.source=aws.cloudtrail  rpc.method=ssm.amazonaws.com/GetParameter  aws.principal.arn=arn:aws:sts::111122223333:assumed-role/TestRole/test-session
       aws.request.parameters={"name":"/example/mappings/general"}   (resource: cloud.account.id=111122223333)
body:  ssm:GetParameter /example/mappings/general
sev:   unset (WARN when `aws.error.code` is set)
attrs: the input attributes, unchanged   (resource: host.name=111122223333  service.name=test-session)
```

**`rabbitmq`**

```text
in:    2026-11-20 00:28:01.100000+00:00 [warning] <0.512.0> closing AMQP connection, client unexpectedly disconnected
body:  closing AMQP connection, client unexpectedly disconnected
sev:   WARN
attrs: vstan.rabbitmq.pid=<0.512.0>
```

**`mosquitto`** has no level in the line, so the severity stays unset.

```text
in:    2026-11-19T22:56:54: Client auto-7C019B88 disconnected.
body:  Client auto-7C019B88 disconnected.
sev:   unset
```

**`stallcheck`**

```text
in:    2026-11-19T22:56:53.123456789Z stdout F published StallMinutes=3
body:  published StallMinutes=3
sev:   INFO
attrs: vstan.stallcheck.stall_minutes=3
```

**`kaudit`**

```text
in:    audit: type=1400 audit(1795129202.123:456): apparmor="ALLOWED" operation="file_perm" class="net" profile="snap.example-agent" pid=1234 comm="agent" requested="send" addr=203.0.113.9 port=443
body:  [apparmor] ALLOWED file_perm send, profile snap.example-agent, to 203.0.113.9:443
sev:   INFO
attrs: log.source=kernel  vstan.audit.type=1400  vstan.audit.serial=456   (resource: service.name=audit)
```

**`smartd`**

```text
in:    Device: /dev/ada3, FAILED SMART self-check. BACKUP DATA NOW!
body:  Device: /dev/ada3, FAILED SMART self-check. BACKUP DATA NOW!
sev:   ERROR   (unreadable and uncorrectable sectors are WARN)
attrs: vstan.smartd.device=/dev/ada3
```

**`gocd`**

```text
in:    jvm 1    | 2026-11-19 22:00:02,000 WARN  [qtp1-6] MaterialUpdate:7 - Slow poll
body:  Slow poll
sev:   WARN
attrs: code.function.name=MaterialUpdate  code.line.number=7  thread.name=qtp1-6
```

**`cron`**

```text
in:    cron: (root) CMD (/usr/local/bin/backup.sh)
body:  (root) CMD (/usr/local/bin/backup.sh)
sev:   unset
attrs: (resource: service.name=cron)
```

**`openvpn`** builds the body from the attributes the `openvpnstatus` receiver writes.

```text
in:    user.name=user-a  client.address=198.51.100.7  client.port=51234  vstan.openvpn.bytes_received=32715571
       vstan.openvpn.bytes_sent=46766490  vstan.openvpn.connected_since=2026-11-18 18:58:58   (empty body)
body:  user-a connected since 2026-11-18 18:58:58 from 198.51.100.7, in 31.2MiB, out 44.6MiB
sev:   unset
attrs: the input attributes, unchanged
```

**`pbs`** reads the task, datastore and user from the log file name.

```text
in:    2026-11-13T10:03:24+01:00: TASK ERROR: connection error: bytes remaining on stream
       log.file.name=UPID:backup:0001A2B4:0C0D0E10:65F1A2B4:65F1A2C1:backup:data\x2dstore:backup-user@pbs!sync:
body:  [data-store] TASK ERROR: connection error: bytes remaining on stream
sev:   ERROR
attrs: user.name=backup-user@pbs!sync  vstan.pbs.task=backup  vstan.pbs.datastore=data-store   (resource: service.name=backup)
```

**`pve`** keeps the whole task log as the body and takes the severity from the `TASK` result line.

```text
in:    INFO: starting new backup job: vzdump 100 --mode snapshot
       INFO:  17% (14.9 GiB of 87.8 GiB) in 54s, read: 126.3 MiB/s, write: 125.7 MiB/s
       TASK ERROR: broken pipe
       log.file.path=/var/log/pve/tasks/A/UPID:node1:00001A2B:0C0D0E0F:65F1A2B3:65F1A2C0:vzdump::root@pam:
body:  (the whole log, unchanged)
sev:   ERROR   (TASK OK is INFO, TASK WARNINGS is WARN)
attrs: user.name=root@pam  vstan.pve.task=vzdump
```

**`serialconsole`**

```text
in:    [\x1b[0;1;31mFAILED\x1b[0m] Failed to start Docker Application Container Engine.\r\n
       cloud.resource_id=serialconsole.googleapis.com/serial_port_1_output  gcp.label.compute.googleapis.com/resource_name=host-a
body:  [FAILED] Failed to start Docker Application Container Engine.
sev:   ERROR   ([  OK  ] is INFO, a start job still running is WARN)
attrs: (resource: host.name=host-a)
```

**`logfmt`**

```text
in:    logger=plugin.x t=2026-11-19T22:42:20.918289Z level=info msg="performing QueryData" dsName=a url=/b source=write_handler.go:254
body:  performing QueryData dsName=a url=/b
sev:   INFO
attrs: code.file.path=write_handler.go  code.line.number=254
```

**`clickhouse`**

```text
in:    2026.11.19 23:01:22.100000 [ 17 ] {4b1c-77} <Error> executeQuery: Code: 60. DB::Exception: no table
body:  Code: 60. DB::Exception: no table
sev:   ERROR
attrs: thread.id=17  vstan.clickhouse.query_id=4b1c-77
```

**`calico`**

```text
in:    2026-11-19 22:54:00.100 [WARNING][73] felix/sync.go 12: Resync took longer than expected
body:  Resync took longer than expected
sev:   WARN
attrs: code.file.path=felix/sync.go  code.line.number=12  thread.id=73
```

**`ssmagent`**

```text
in:    2026-11-19 23:00:14.0871 WARN EC2RoleProvider Failed to connect to Systems Manager
body:  Failed to connect to Systems Manager
sev:   WARN
```

**`litellm`**

```text
in:    07:13:58 - LiteLLM Proxy:INFO: policy_registry.py:577 - Synced 0 production policies and 0 draft/published (by ID) from DB to in-memory registry
body:  Synced 0 production policies and 0 draft/published (by ID) from DB to in-memory registry
sev:   INFO
attrs: code.file.path=policy_registry.py  code.line.number=577
```

**`stderrhint`** claims a journal record of a container whose severity is ERROR only because it came from the stderr stream, and clears that severity.

```text
in:    listening on 0.0.0.0:3000   (log.source=journal, container.id in the resource, severity ERROR from stderr)
body:  listening on 0.0.0.0:3000
sev:   unset
```

**`lldpd`**

```text
in:    {"observed_at":"2026-11-20T13:06:13Z","interface":"eno2","via":"LLDP","chassis_id":"00:11:22:33:44:55","chassis_name":"switch-48.example.net","chassis_descr":"...","mgmt_ip":["192.0.2.39"],"port_id":"Gi4/0/18","port_descr":"GigabitEthernet4/0/18"}
body:  eno2 sees switch-48.example.net port Gi4/0/18 192.0.2.39
sev:   unset
attrs: network.interface.name=eno2  client.mac.address=00:11:22:33:44:55  client.address=192.0.2.39  vstan.lldp.* (the other fields; the
       multi-line chassis description stays an attribute)
```

The record time is `observed_at`.

**`k8s`**

```text
in:    Pod team-a/web-1: Running (1/1 ready) on node-b
body:  Running (1/1 ready)
sev:   unset
attrs: k8s.pod.status.phase=Running   (resource: k8s.pod.name=web-1  k8s.namespace.name=team-a  k8s.node.name=node-b  host.name=node-b  service.name=web-1)

in:    {"involvedObject":{"kind":"Pod","name":"web-1","namespace":"team-a"},"reason":"BackOff","message":"Back-off restarting failed container","type":"Warning","source":{"host":"node-b"}}
body:  BackOff: Back-off restarting failed container
sev:   WARN
attrs: (resource: host.name=node-b  k8s.namespace.name=team-a  k8s.pod.name=web-1  service.name=Pod/web-1)
```

## Configuration

```yaml
processors:
  logshape:
    branches: [klog, nginx, cisco]   # default: every branch
    hints:                           # default: none
      nginx: {service: tea-frontend} # service.name, scope name or log.source must equal
    account_aliases:                 # default: none
      "111122223333": prod
    klog:
      year: 0                        # scalar: replaces the year taken from the record time
      static_pod_prefixes: [kube-vip-] # list: extends the defaults
      level_overrides: {W: error}    # map: replaces the default for that key
```

Code holds every default. A scalar in the configuration replaces it, a list extends it, a map replaces it per key. One list is the exception: a non-empty `cisco.access_point_downgrade` replaces the built-in list, so an operator can remove a built-in message from it (an empty list keeps the built-in one).

| Key | Type | Default | Meaning |
|---|---|---|---|
| `branches` | list | every branch | The branches that may claim a record. An unknown name fails validation. |
| `hints` | map of branch to `{service, scope, source}` | none | Narrows a branch to records from one service, scope or source. Every field that is set must match. A hint for an unknown branch fails validation. |
| `account_aliases` | map | none | Cloud account or project id to a display name, for the load balancer, bucket, audit and CloudTrail branches. An empty id or alias fails validation. |
| `gcp_lb_error_values` | list | `unsupported_method`, `invalid_http2_request_content_length` | Load balancer status details that raise a record to WARN. The list extends the built-in one. |
| `service_name_overrides` | map | none | A service name a branch derived, to the name that replaces it. A name not in the map is untouched and an empty derived name is never overridden. Only `service.name` on the resource changes. It applies to every branch. |
| `k8s.static_pod_nodes` | list | none | Extends the node names that static pods are known to run on. |
| `klog.year` | int | `0` | The year of the line; `0` takes it from the record time (klog lines carry month and day only). |
| `klog.static_pod_prefixes` | list | `kube-apiserver-`, `kube-controller-manager-`, `kube-scheduler-`, `etcd-` | Static pod name prefixes used to infer the node from the pod name. The list extends the built-in one. |
| `klog.level_overrides` | map | `I`, `W`, `E`, `F` to a level word | Replaces the level of a klog letter. An unknown level word fails validation. |
| `syslog.source_attribute` | string | `message` | The attribute that holds the raw line. |
| `syslog.set_hostname`, `set_service_name`, `map_severity`, `preserve_original`, `clean_body` | bool | `true` | Set `host.name` from the line, `service.name` from the tag, the severity from the priority, keep the raw line in `log.record.original`, replace the body with the message part. |
| `cisco.source_attribute` | string | `log.record.original` | The attribute that holds the raw line. |
| `cisco.set_hostname`, `clean_body`, `rewrite_messages`, `adjust_severity` | bool | `true` | Set `host.name` from `net.peer.name`, strip the syslog prefix, replace the body of HP and WLC lines with the message part, set the severity. |
| `cisco.port_database_path` | string | empty | An optional YAML file of port aliases per host. |
| `cisco.access_point_downgrade` | list | built-in list | Message fragments of routine access point kernel lines, matched without case, that drop from WARN to INFO. The built-in list is `pmkr0 lookup` and `client roamed`. A non-empty list set here replaces it; an empty list keeps it. |
| `logrus.rewrite_messages`, `adjust_severity`, `parse_timestamp` | bool | `true` | Replace the body with the message plus the error, set the severity, set the record time from the line. |
| `postgres.rewrite_messages`, `extract_details`, `adjust_severity` | bool | `true` | Replace the body, write `DETAIL`, `STATEMENT`, `HINT` and `CONTEXT` as attributes, set the severity. |
| `redis.service_name` | string | `redis` | The `service.name` written when the resource has none. |
| `redis.set_service_name`, `preserve_original`, `rewrite_messages`, `adjust_severity` | bool | `true` | Write the service name, keep the raw line, replace the body, set the severity. |
| `redis.source_attribute` | string | empty (the body) | An attribute that holds the line. |
| `uvicorn.extract_http_fields`, `rewrite_messages`, `adjust_severity` | bool | `true` | Write the request fields, replace the body with `METHOD path status`, set the severity. |
| `nginx.service_name` | string | `nginx` | The `service.name` written when `set_service_name` is on. |
| `nginx.set_service_name`, `parse_timestamp` | bool | `true` | Write the service name, set the record time from the line. |
| `nginx.source_attribute` | string | empty (the body) | An attribute that holds the line. |
| `sentry.service_name` | string | `sentry` | The `service.name` written when `set_service_name` is on. |
| `sentry.set_service_name`, `parse_timestamp`, `preserve_original`, `extract_http_fields` | bool | `true` | Write the service name, set the record time, keep the raw line, write the request fields. |
| `sentry.source_attribute` | string | empty (the body) | An attribute that holds the line. |

The Windows event summaries, the `dn_contractions` list and the `fswatch` `roots` keys (`name`, `exclude`) are not options of this processor. They are described in [windows-edge.md](windows-edge.md).

## Counters

| Metric | Attribute | Meaning |
|---|---|---|
| `otelcol_logshape_unmatched` | `source` (`log.source`, or `none`) | Records no branch claimed. |
| `otelcol_logshape_parse_failed` | `format` (the branch) | Records a branch claimed and could not parse, including a recovered panic. |

Both are counters of records. The processor adds the counts of one batch at the end of the batch.

## Images

All six images list `logshape`: the gateway, the scrape collector, the Linux edge, the static Linux edge, the FreeBSD edge and the Windows edge. Which branches run in an image is set by configuration, not by the build. Size figures below are the release builds, 0.3.17 asset against 0.3.18 build, in bytes for linux/amd64 unless noted (the scrape collector row is the binary in the image). Sizes vary by a few bytes with the embedded commit. The 0.3.18 scrape collector also gains `hwmap`.

| Image | Lists `logshape` | Size 0.3.17 | Size 0.3.18 | Change |
|---|---|---|---|---|
| Gateway | yes | 217,952,418 | 228,860,064 | +5.00% |
| Scrape collector | yes | 156,709,026 | 157,282,466 | +0.37% |
| Linux edge | yes | 197,805,736 | 198,339,784 | +0.27% |
| Static Linux edge | yes | 196,468,898 | 197,001,378 | +0.27% |
| FreeBSD edge | yes | 176,554,156 | 177,074,348 | +0.29% |
| Windows edge | yes | 141,387,776 | 141,987,840 | +0.42% |

`logshape` alone adds 454,656 bytes to the gateway. The rest of the gateway growth is the flow log exporter and the NetFlow receiver, the Kubernetes observer and six application receivers.

The standalone processors it replaces (`klogprocessor`, `uvicornlogprocessor`, `logruslogprocessor`, `postgreslogprocessor`, `redislogprocessor`, `nginxlogprocessor`, `sentrylogprocessor`, `ciscologprocessor`, `syslogprocessor`) stay listed in the gateway manifest in 0.3.18, so existing gateway configs keep working. They are removed in the release after production validation, 0.3.19 at the earliest; the release date is set when that validation is done.

To use `logshape`, list it in the logs pipeline after the processors that join records and before the exporters, and remove from that pipeline the standalone processor of any format `logshape` now shapes. Keep one shaper for a format in a pipeline: two of them would shape the same line twice.

Run the same image tag and the same `logshape` configuration on every gateway that shapes logs. The same line then gives the same record in every deployment. Only the resource follows the account: `account_aliases` decides the display name.

## Names

- An attribute with an official OpenTelemetry name is written under that name.
- An attribute with no official name is `vstan.<branch>.<key>`: `vstan.cisco.facility`, `vstan.wifi.radio`, `vstan.postgres.detail`, `vstan.redis.role`, `vstan.sentry.view`, `vstan.nginx.device_type`, `vstan.uvicorn.status_text`, `vstan.k8s.controller`, `vstan.http.request.duration`, and `vstan.logrus.<key>` for the other keys of a logrus or zap line.
- The raw line goes to `log.record.original`, the logger or component name to the scope name, the process id to `process.pid`, the thread to `thread.id` or `thread.name`, the source location to `code.file.path`, `code.line.number` and `code.function.name`.
- These attributes are integers: `thread.id`, `process.pid`, `client.port`, `source.as.number`, `destination.as.number`, `file.size`, `code.line.number`.
- A renamed attribute is renamed in place. Nothing is written under both names and nothing is backfilled.
- The keys that retention rules use are never renamed: `log.source`, `flow.type`, `http.request.method`, SeverityNumber and Namespace.

Rename table (`shape/rename`): `file.size_final` to `file.size`; `real_address` to `client.address` and `client.port`; `common_name` to `user.name`; `interface` to `network.interface.name`; `client.mac` to `client.mac.address`; `client.ip` to `client.address`; `k8s.pod.phase` to `k8s.pod.status.phase`; `k8s.pod.restarts` to `k8s.container.restart_count`; `otelcol.component` and `otelcol.component.id` to `otel.component.name`; `original_message` to `log.record.original`; `flow.src_as` to `source.as.number`; `flow.dst_as` to `destination.as.number`; `winlog.user.identifier` to `user.id`; `winlog.process.pid` to `process.pid`; `winlog.process.thread.id` to `thread.id`; `aws.region` to `cloud.region`; `machine.type` to `host.type`; `host.display_name` to `host.name`; `gcp.project` to `cloud.account.id`. `winlog.computer_name` is dropped and `host.name` kept. The deprecated names `deployment.environment`, `rpc.system`, `rpc.service`, `rpc.response.status_code`, `event.name`, `code.filepath`, `code.lineno` and `code.function` are not written by the helpers.

## Severity

- A level in the line beats a level derived from the stream or the priority. A stderr or priority hint alone leaves the severity unset.
- An unknown level stays unset. No branch defaults to INFO.
- SeverityText and SeverityNumber come from one table, so they always agree.
- HTTP access lines: 5xx is ERROR, 4xx is WARN, the rest INFO.
- Level sources: the uvicorn prefix, `[LEVEL]` tags, logfmt `level=`, klog I, W, E and F, PostgreSQL LOG, WARNING and ERROR, Redis `.`, `-`, `*` and `#`, logrus, zap and slog `level`.

## Bodies

The body is `[component] text` plus a few `key=value` pairs. Process ids, thread ids, timestamps, level tags and wrapper prefixes leave the body and become attributes. Access lines read `METHOD path status size`, with the verb in uppercase. The client is the first `X-Forwarded-For` entry and the proxy goes to `network.peer.address`; a line without one takes the peer as the client.

The body of a load balancer or bucket line holds no account, project or region, so the same line gives the same body in every deployment. Only the resource follows the account: `host.name`, `service.name` and `cloud.account.name` come from the account or project id and `account_aliases`. The `aws.s3access` encoding does not write `cloud.account.id`. The scrape config must add it to those records (a resource attribute set by the receiver that reads the bucket); without it the `s3access` branch sets no `cloud.account.name`, and the body is unchanged.

## Row counts

Some sources send fewer records or different records after shaping:

- A Python or Java traceback that arrives as many records is merged into one ERROR record by `logrecombine`, and blank or whitespace-only records are dropped there.
- A Windows host that watches a synced folder sends far fewer `fswatch` records once the default exclusions apply. See [windows-edge.md](windows-edge.md).
- A record that `logshape` cannot claim or parse is not dropped. It leaves unchanged.

The CHANGELOG lists each change of a count with its release.

## Known limits

- A Proxmox task log of tens of kilobytes stays one body; the processor sets no size cap.
- A serial console record that holds only `\r\n` keeps an empty body. The blank drop of `logrecombine` removes it when the pipeline sends the record through that processor first.
- Only Python and Java traces are merged into one ERROR record with `exception.*`. A Go panic or a ClickHouse stack trace is joined by the gap rule but gets no severity or exception attributes.

## Changes from the old processors

- The body has no `[LEVEL]` or `[role]` tag; the level is in the severity.
- An unknown level is unset, where the old processors wrote INFO.
- The time of the line sets the record time. Redis and PostgreSQL times are read as UTC, CET or CEST; a PostgreSQL zone the processor does not know leaves the record time alone.
- `error` becomes `exception.message` (and `exception.type` for an error object), joined to the message with one colon.
- A logrus, zap or slog line whose time cannot be read is counted in `otelcol_logshape_parse_failed` and kept as it was. A line with no time is shaped.
- Number and boolean fields keep their type as attributes: `vstan.logrus.count` is an integer when the line has `"count":3`.
- `client.ip` is `client.address`; `source.ip` is `source.address`; `http.response.body.bytes` is `http.response.body.size`; the nginx remote user is `user.name`; the browser OS of an nginx line is `user_agent.os.name`, not `os.name`.
- The `openvpnstatus` receiver writes `user.name`, `client.address`, `client.port` and `vstan.openvpn.*` in place of `common_name`, `real_address`, `bytes_received`, `bytes_sent` and `connected_since`, and marks a client shown as `UNDEF` with `unauthenticated`. The AS numbers of the netflow receiver are renamed by the `flow` branch, because that receiver is an upstream module.

## Storage

The attributes of the logs tables stay in a string map; this release does not change that map or any table. Typed columns today are `HostName`, `Namespace` and `PodName`, with the `k8s.*` resource keys. The integer attributes listed above are stored as strings in the map. A typed column for an integer attribute is added only when a query needs one. The operator of each ClickHouse instance owns its table definition (the DDL owner): the logs table is `otel.otel_logs` on one kind of instance and `default.otel_logs` on the other, and this release changes neither.

## Registry check

The names the branches write were verified against the attribute pages of the OpenTelemetry semantic conventions registry, tag v1.45.0. No name in the lists below rests on memory. In the registry:

- `user_agent.original` is stable. `user_agent.name`, `user_agent.version` and `user_agent.os.name` exist (development). The nginx branch writes the browser OS as `user_agent.os.name`, not `os.name`, which is the host OS.
- `rpc.status_code` is stable. `rpc.response.status_code` and `rpc.grpc.status_code` are deprecated and point to it.
- These names exist: `client.address`, `client.port`, `network.peer.address`, `source.address`, `source.as.number`, `destination.as.number`, `user.name`, `user.id`, `process.pid`, `thread.id`, `thread.name`, `http.request.method`, `http.response.status_code`, `http.response.body.size`, `http.request.header.<key>`, `url.path`, `url.query`, `network.protocol.name`, `network.protocol.version`, `exception.type`, `exception.message`, `exception.stacktrace`, `code.file.path`, `code.line.number`, `code.function.name`, `log.record.original`, `cloud.account.id`, `cloud.region`, `host.name`, `host.type`, `network.interface.name`, `client.mac.address`, `k8s.container.restart_count`, `k8s.pod.status.phase`, `otel.component.name` and `file.size`.
- These are deprecated, with the successor named on the page: `deployment.environment`, `rpc.system`, `rpc.service`, `rpc.response.status_code`, `event.name`, `code.filepath`, `code.lineno` and `code.function`. `enduser.id` is deprecated too and has no successor, so the nginx remote user is written as `user.name`.

The last field of an Influx `[httpd]` line is microseconds. The InfluxDB 1.8 source (`services/httpd/response_logger.go`) computes it with `time.Since(start)/time.Microsecond`, to match the Apache `%D` format.

## Deprecated names in the code

A source check (`sourcecheck_test.go` in the processor) reads every non-test Go file of this repository, leaves out upstream modules, and fails when a file holds one of the deprecated names above. Three standalone processors still write `code.filepath`: `ciscologprocessor`, `klogprocessor` and `logruslogprocessor`. They stay in the manifests until their branch runs in production and are on an explicit allow list in the test. Nothing else writes a deprecated name.

To list the deprecated names stored in ClickHouse, run this query on each instance:

```sql
SELECT k, count() AS n
FROM otel.otel_logs ARRAY JOIN mapKeys(LogAttributes) AS k
WHERE Timestamp > now() - INTERVAL 1 DAY
  AND k IN ('deployment.environment', 'rpc.system', 'rpc.service', 'rpc.response.status_code',
            'event.name', 'code.filepath', 'code.lineno', 'code.function')
GROUP BY k ORDER BY n DESC
```

## Fixtures and benchmarks

`testdata/samples/<format>.txt` holds sample lines of each text format, with documentation-range addresses and no host, account or secret. `testdata/samples-keyed/<branch>.jsonl` holds one JSON record per line for the branches that claim by attributes (`flow`, `gcplb`, `awselb`, `s3access`, `gcpaudit`, `eksaudit`, `cloudtrail`, `openvpn`, `pbs`, `pve`, `serialconsole`, `stderrhint`); each record says if it is `real` (a layout quoted from a capture, with names replaced) or `invented` (written from the format description).

Every sample file starts with one marker line. A text sample has `# provenance: real`, `# provenance: invented` or `# provenance: partly real; real lines: 1, 3-4`, where the numbers count the sample lines and not the comments, and a `# evidence:` sentence that names the quote or says the lines are written from the format. A keyed file has the same marker, with `real records:` and the record numbers in place of `real lines:`, and its per-record `provenance` fields must agree with it. A line is real only when it was captured, with names and addresses replaced; every other line is invented, and a line with no evidence counts as invented. A test fails on a missing, doubled, unknown or inconsistent marker. The text samples are 3 real, 12 partly real and 16 invented; the keyed files are 0 real, 2 partly real and 10 invented.

A test runs every sample through its own branch and through the full table, checks that no other branch takes it, that severity text and number agree, that integer attributes are integers and that no deprecated name is written. A scrub test fails on private addresses, account numbers, e-mail addresses, internal domain suffixes and secrets. `go test -bench Formats` runs 1000-record batches per format.

## Detection cost

`gate.go` lists, for each branch, what its claim needs: a first byte, a minimum length, bytes at fixed positions, required text, a `log.source` value or an attribute key. A record is offered only to the branches whose gate admits it, in the order of the table, so the first claim still wins. A branch with no gate entry is offered every record. A new branch works without one, and costs a claim call per record until its gate is written. A gate must admit everything its branch claims; `TestGate_EveryClaimIsAdmittedAndTheFirstClaimIsTheLinearOne` checks this on every sample line, its variants and the odd inputs of the robustness test. `go test -bench ConsumeUnmatched` measures a batch no branch claims.

## Cost per format

`logshape` costs 1.01 to 1.74 times the block it replaces for each record, depending on the format. The table compares the whole `logshape` table, as it ships, with the standalone processor that handled the same lines before. For `cloudtrail` the block is the OTTL block of the scrape collector, and the `logshape` side includes the source tagger that runs before it.

| Format | Replaced block | Block, ns per record | `logshape`, ns per record | Ratio | One branch alone | Allocations, block to `logshape` |
|---|---|---|---|---|---|---|
| `klog` | `klogprocessor` | 6,908 | 6,961 | 1.01 | 1.01 | 28.4 to 35.9 |
| `nginx` | `nginxlogprocessor` | 15,364 | 16,886 | 1.10 | 1.02 | 34.5 to 39.8 |
| `cloudtrail` | OTTL block | 2,358 | 2,682 | 1.14 | 0.82 | 24.2 to 21.3 |
| `cisco` | `ciscologprocessor` | 10,139 | 11,916 | 1.18 | 1.08 | 22.3 to 29.2 |
| `logrus` | `logruslogprocessor` | 5,056 | 6,265 | 1.24 | 1.12 | 38.9 to 41.9 |
| `sentry` | `sentrylogprocessor` | 4,937 | 6,604 | 1.34 | 1.17 | 20.0 to 23.1 |
| `postgres` | `postgreslogprocessor` | 1,884 | 2,664 | 1.41 | 1.34 | 11.2 to 11.0 |
| `redis` | `redislogprocessor` | 1,872 | 2,770 | 1.48 | 1.41 | 13.6 to 13.0 |
| `syslog` | `syslogprocessor` | 3,128 | 5,132 | 1.64 | 1.13 | 15.7 to 17.1 |
| `uvicorn` | `uvicornlogprocessor` | 1,975 | 3,440 | 1.74 | 1.73 | 13.2 to 18.4 |

The ratio is the `logshape` time divided by the block time. The column "One branch alone" is the same ratio with only that branch enabled, so it leaves out the checks of the other branches. A ratio above 1 means the same batch takes that much longer in `logshape`. A ratio of 1.5 on a block that takes 2 microseconds per record adds 1 microsecond per record, which is 1 millisecond per 1,000 records.

How the figures were made:

- The command is `cd test/bench && go test -run=NONE -bench='LogshapeVsBlock|LogshapeCloudTrail' -benchmem -benchtime=300x -count=10 .`
- Each figure is the median of 10 runs (n=10). A run times a batch of 1,000 records. A fresh copy of the batch is made for each iteration, outside the timer. Nanoseconds per record are the time of the batch divided by 1,000, and allocations are the allocations of the batch divided by 1,000.
- The records are the sample lines of `testdata/samples`, one file for each format, cycled to 1,000 records. A file holds 1 to 8 lines, so a format with few lines is a narrow input.
- Go 1.26.8 on macOS, x86-64. The machine was not idle (load average 8 to 15), so read the ratios, not the absolute times.
- For `nginx`, `sentry` and `logrus` the block runs with the options that `logshape` turns on (timestamp parsing, and keeping the original line for `sentry`), so both sides do the same work. With the default options the block is a little faster than in the table.

What does not compare like for like:

- The `uvicorn` block handles 3 of the 5 sample lines and leaves the other 2 unchanged. `logshape` shapes all 5, so part of its extra time is work the block does not do.
- For `cloudtrail` the `logshape` side runs two processors, the source tagger and the `cloudtrail` branch, against one OTTL block.
- Formats with no block in the repository (`flow`, `openvpn`, `awselb`, `s3access`, `gcplb`, `gcpaudit`, `eksaudit`, `serialconsole`, `stderrhint`, `pbs`, `pve`) are not in the table.

For the two formats whose block takes about 1.9 microseconds per record (`redis` and `postgres`), `logshape` adds about 0.8 to 0.9 microseconds per record. Part of that is the same on every format: a branch works on a copy of the record, and the output is regrouped by resource. The share is large for a cheap block and small for an expensive one, which is why `klog` and `nginx` come out near 1.0.
