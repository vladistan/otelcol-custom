# Scrape Collector

## Key review at v0.154.0

Sources read from the Go module cache at tag v0.154.0 of opentelemetry-collector-contrib.

| Component | Result | Source |
|---|---|---|
| `awslogsencodingextension` | present; CloudTrail and S3 access-log formats exist | `internal/unmarshaler/cloudtraillog`, `internal/unmarshaler/s3-access-log` |
| `googlecloudlogentryencodingextension` | present | `log_entry.go` |
| `googlecloudpubsubreceiver` | present; keys `project`, `subscription`, `encoding`, `ignore_encoding_error` | `config.go:20-38` |
| `googlecloudmonitoringreceiver` | present at v0.164.1, stability alpha; one `project_id` per instance | `config.go:23` |
| `awscloudwatchreceiver` logs | present; `logs.groups.autodiscover` (`prefix` or `pattern`, not both) or `logs.groups.named`, not both modes | `config.go:84-120` |
| `awscloudwatchmetricsreceiver` | **missing at v0.154.0**, last tag v0.133.0 | see below |
| `resourcedetectionprocessor` `gcp` detector | present, name `gcp` | `factory.go:81`, `internal/gcp/gcp.go:27` |

### awscloudwatchmetricsreceiver

Removed in contrib v0.134.0 as unmaintained ("`receiver/awscloudwatchmetrics`: Remove unmaintained component", PR #42238). Its v0.133.0 `metadata.yaml` already set `unmaintained: [metrics]`.

`awscloudwatchreceiver` v0.154.0 has a `metrics` block (stability alpha for logs and metrics, `README.md:9`). It was the candidate replacement; the scrape image now uses its own `cloudwatchmetrics` receiver instead (see "CloudWatch metrics" below). Keys: `metrics.queries[]` (`namespace`, `metric_name`, `dimensions`, `stats`) or `metrics.discovery` (`filters`, `limit`, `stats`), plus `period` (5m) and `delay` (10m) (`config.go:38-82`). The old `metrics.named` key has no equivalent name. Output follows the CloudWatch Metric Streams OpenTelemetry 1.0.0 format. Check it against your data before relying on it as a replacement.

#### awscloudwatchreceiver metrics keys

Stability is alpha for both logs and metrics (`README.md:9`). The block is optional and off unless `queries` or `discovery` is set (`config.go:135-140`). `region` is required (`config.go:144`).

| Key | Type, default | Rule | Source |
|---|---|---|---|
| `metrics.collection_interval` | duration, 5m | at least 1s, and not below `period` | `config.go:162,171`, `README.md:75` |
| `metrics.period` | duration, 5m | at least 1s | `config.go:44,165`, `README.md:76` |
| `metrics.delay` | duration, 10m | at least 1s | `config.go:48,168`, `README.md:77` |
| `metrics.queries[].namespace` | string | required | `config.go:78,200` |
| `metrics.queries[].metric_name` | string | required | `config.go:79,203` |
| `metrics.queries[].dimensions` | map, none | optional, key casing kept | `config.go:80`, `README.md:87` |
| `metrics.queries[].stats` | list, none | no empty names | `config.go:81,206` |
| `metrics.discovery.filters.namespace` | string, all | optional | `config.go:66` |
| `metrics.discovery.filters.metric_name` | string, all | optional | `config.go:67` |
| `metrics.discovery.limit` | int, 100 | greater than 0 | `config.go:57,183` |
| `metrics.discovery.stats` | list, none | no empty names | `config.go:60,186` |

`queries` and `discovery` are mutually exclusive (`config.go:179`). With no `stats`, the receiver fetches Sum, SampleCount, Minimum and Maximum and emits one Summary point; with `stats`, it emits one Gauge point per statistic with a `stat` attribute (`README.md:106-107`). Metric names are `amazonaws.com/{Namespace}/{MetricName}`; resource attributes are `cloud.provider=aws` and `cloud.region`; data points carry `Namespace`, `MetricName` and `Dimensions` (`README.md:116-118`).

### CloudTrail `Records[]` split

Confirmed. `processRecords` walks the `Records` array and appends one log record per entry (`cloudtraillog/unmarshaler.go:270-324`). No splitting step is needed.

### GCP log entry mapping

| LogEntry field | Output key | Source |
|---|---|---|
| `httpRequest.requestUrl` | `url.path` (path part) | `log_entry.go:229` |
| `httpRequest.status` | `http.response.status_code` | `log_entry.go:252` |
| `httpRequest.userAgent` | `user_agent.original` | `log_entry.go:254` |
| `logName` project | resource `gcp.project` | `log_entry.go:32,478` |
| `resource.type` | resource `gcp.resource_type` | `README.md:73` |

The encoding sets no `service.name`, `cloud.region` or `cloud.account.id` from `resource.labels` for ordinary entries. The reference config derives `cloud.account.id` from `gcp.project`, and reads region and service from the label keys in the fixtures. `cloud.region` appears in the README only for one format (`README.md:467`, `location`).

### resource_detection gcp detector

Present. It sets `cloud.provider`, `cloud.account.id` (project id) and `cloud.platform`. On GKE it also sets `k8s.cluster.name` and the zone or region (`internal/gcp/gcp.go:80-89`). On other platforms it sets `cloud.region` from the platform-specific region callable (`gcp.go:104-138`).

## Auth0 receiver delivery

The receiver deletes a queue message only after the next consumer accepts its batch. If the consumer refuses, nothing is deleted and the queue redelivers after the visibility timeout. If a delete fails after the consumer accepted, the message is delivered again, so downstream sees a duplicate. Duplicates are accepted: each record keeps `auth0.log_id` so a later step can drop them.

A message that is not an Auth0 EventBridge envelope is never deleted. It logs one WARN with the message id and raises `auth0.messages.invalid`. Without a redrive policy on the queue, the same message returns on every poll, so the queue needs one.

Credentials come from the AWS default chain. Setting `role_arn` (and optionally `external_id`) makes the receiver assume that role through STS; with no `role_arn` it makes no STS call. `endpoint` redirects both SQS and STS and is for tests only.

## Reference config

The reference config and a narrowed builder manifest were checked with a scratch build: `otelcol-scrape validate` exits 0 on the config with placeholder env values. `test/scrape/validate-scrape.sh` repeats that check against `builder-scrape.yaml` and `test/configs/scrape.yaml`.

### Reference config key sources

Every receiver key in the reference config has a row. Sources are v0.154.0 files in the Go module cache, and `receiver/auth0receiver/config.go` for `auth0`.

| Component | Key | Source |
|---|---|---|
| `aws_s3` | `s3downloader.region` | `awss3receiver/config.go:19` |
| `aws_s3` | `s3downloader.s3_bucket` | `awss3receiver/config.go:20`, required even with `sqs` (`config.go:94`) |
| `aws_s3` | `s3downloader.s3_prefix` | `awss3receiver/config.go:21` |
| `aws_s3` | `sqs.queue_url` | `awss3receiver/config.go:36` |
| `aws_s3` | `sqs.region` | `awss3receiver/config.go:38` |
| `aws_s3` | `encodings[].extension` | `awss3receiver/config.go:59` |
| `aws_s3` | `encodings[].suffix` | `awss3receiver/config.go:60`, matched by `HasSuffix` after `.gz` is stripped (`receiver.go:128-139,311-318`) |
| `awscloudwatch` | `region` | `awscloudwatchreceiver/config.go:30` |
| `awscloudwatch` | `storage` | `awscloudwatchreceiver/config.go:35` |
| `awscloudwatch` | `logs.poll_interval` | `awscloudwatchreceiver/config.go:87` |
| `awscloudwatch` | `logs.settle_lag` | `receiver/awscloudwatchreceiver/config.go` (local copy) |
| `awscloudwatch` | `logs.groups.autodiscover.prefix` | `awscloudwatchreceiver/config.go:100` |
| `awscloudwatch` | `logs.groups.autodiscover.limit` | `awscloudwatchreceiver/config.go:102` |
| `awscloudwatch` | `metrics.collection_interval` | `awscloudwatchreceiver/config.go:42,162` |
| `awscloudwatch` | `metrics.period` | `awscloudwatchreceiver/config.go:44` |
| `awscloudwatch` | `metrics.delay` | `awscloudwatchreceiver/config.go:48` |
| `awscloudwatch` | `metrics.queries[].namespace` | `awscloudwatchreceiver/config.go:78` |
| `awscloudwatch` | `metrics.queries[].metric_name` | `awscloudwatchreceiver/config.go:79` |
| `awscloudwatch` | `metrics.queries[].stats` | `awscloudwatchreceiver/config.go:81` |
| `cloudwatchmetrics` | `storage` | `cloudwatchmetricsreceiver/config.go:21`, optional; names a storage extension that keeps the end of the last window, and without it the state is lost at restart |
| `cloudwatchmetrics` | `max_catchup` | `cloudwatchmetricsreceiver/config.go:23`, default 72h (`config.go:16`), must not be negative (`config.go:54`) |
| `cloudwatchmetrics` | `accounts[].name` | `cloudwatchmetricsreceiver/config.go:31`, required and unique (`config.go:78-84`) |
| `cloudwatchmetrics` | `accounts[].role_arn` | `cloudwatchmetricsreceiver/config.go:32`, optional role to assume |
| `cloudwatchmetrics` | `accounts[].external_id` | `cloudwatchmetricsreceiver/config.go:33`, needs `role_arn` (`config.go:98`) |
| `cloudwatchmetrics` | `accounts[].regions` | `cloudwatchmetricsreceiver/config.go:34`, a non-empty list without empty or repeated names (`config.go:85-97`) |
| `cloudwatchmetrics` | `tiers.operational.collection_interval` | `cloudwatchmetricsreceiver/config.go:39`, positive, not below `period` and a multiple of it (`config.go:108,114,116`) |
| `cloudwatchmetrics` | `tiers.operational.period` | `cloudwatchmetricsreceiver/config.go:40`, whole seconds and 1, 5, 10 or 30 seconds or a multiple of 60 seconds (`config.go:106-113,155-163`) |
| `cloudwatchmetrics` | `tiers.operational.delay` | `cloudwatchmetricsreceiver/config.go:41`, not negative (`config.go:118`) |
| `cloudwatchmetrics` | `tiers.operational.stats` | `cloudwatchmetricsreceiver/config.go:43`, never defaulted: a non-empty list of CloudWatch statistic names without empty or repeated entries (`config.go:122,127-136`) |
| `cloudwatchmetrics` | `tiers.operational.limit` | `cloudwatchmetricsreceiver/config.go:45`, most series read per account and region, must be positive (`config.go:120`); the rest are counted, not read |
| `cloudwatchmetrics` | `tiers.operational.namespaces.AWS/EC2` | `cloudwatchmetricsreceiver/config.go:47`, the metric names to read in the namespace; an empty list reads the whole namespace; the map must not be empty and names must not repeat (`config.go:124,137-150`) |
| `cloudwatchmetrics` | `tiers.operational.namespaces.AWS/RDS` | same rule as `AWS/EC2` (`cloudwatchmetricsreceiver/config.go:47`) |
| `cloudwatchmetrics` | `tiers.operational.namespaces.AWS/Lambda` | same rule as `AWS/EC2` (`cloudwatchmetricsreceiver/config.go:47`) |
| `cloudwatchmetrics` | `tiers.operational.namespaces.AWS/ApplicationELB` | same rule as `AWS/EC2` (`cloudwatchmetricsreceiver/config.go:47`) |
| `cloudwatchmetrics` | `tiers.daily.collection_interval` | same rule as `tiers.operational.collection_interval` (`cloudwatchmetricsreceiver/config.go:39`); tiers are keyed by name and every tier is read for every account and region (`config.go:25-26`) |
| `cloudwatchmetrics` | `tiers.daily.period` | same rule as `tiers.operational.period` (`cloudwatchmetricsreceiver/config.go:40`) |
| `cloudwatchmetrics` | `tiers.daily.delay` | same rule as `tiers.operational.delay` (`cloudwatchmetricsreceiver/config.go:41`) |
| `cloudwatchmetrics` | `tiers.daily.stats` | same rule as `tiers.operational.stats` (`cloudwatchmetricsreceiver/config.go:43`) |
| `cloudwatchmetrics` | `tiers.daily.limit` | same rule as `tiers.operational.limit` (`cloudwatchmetricsreceiver/config.go:45`) |
| `googlecloudpubsub` | `project` | `googlecloudpubsubreceiver/config.go:20` |
| `googlecloudpubsub` | `subscription` | `googlecloudpubsubreceiver/config.go:31`, must match `projects/<project>/subscriptions/<name>` (`config.go:72`) |
| `googlecloudpubsub` | `encoding` | `googlecloudpubsubreceiver/config.go:33` |
| `googlecloudpubsub` | `ignore_encoding_error` | `googlecloudpubsubreceiver/config.go:38` |
| `googlecloudmonitoring` | `project_id` | `googlecloudmonitoringreceiver/config.go:23` |
| `googlecloudmonitoring` | `collection_interval` | `scraperhelper.ControllerConfig`; default 5m, at least 60s (`googlecloudmonitoringreceiver/config.go:15-16,42`) |
| `googlecloudmonitoring` | `metrics_list[].metric_name` | `googlecloudmonitoringreceiver/config.go:31,35`, list required (`config.go:46`) |
| `googlecloudmonitoring` | `projects` | `receiver/googlecloudmonitoringreceiver/config.go:27` (local copy), more projects for the same instance, added to `project_id`; no empty name, and one of `project_id` or `projects` is required (`config.go:62-70`) |
| `googlecloudmonitoring` | `aggregation.alignment_period` | `receiver/googlecloudmonitoringreceiver/aggregation.go:49` (local copy), at least 1 minute (`aggregation.go:57`) and not longer than `collection_interval` (`config.go:82`) |
| `googlecloudmonitoring` | `aggregation.per_series_aligners` | `receiver/googlecloudmonitoringreceiver/aggregation.go:52` (local copy), a non-empty list of `ALIGN_MIN`, `ALIGN_MAX`, `ALIGN_MEAN`, `ALIGN_SUM`, `ALIGN_COUNT` and `ALIGN_PERCENTILE_50` without repeats (`aggregation.go:60-72`); the percentile aligner reads distribution metrics only |
| `azure_event_hub` | `connection` | `azureeventhubreceiver/config.go:28`, needs `SharedAccessKeyName` |
| `azure_event_hub` | `storage` | `azureeventhubreceiver/config.go:32` |
| `azure_event_hub` | `format` | `azureeventhubreceiver/config.go:34` |
| `auth0` | `queue_url` | `auth0receiver/config.go:21` |
| `auth0` | `region` | `auth0receiver/config.go:22` |
| `auth0` | `role_arn` | `auth0receiver/config.go:24` |
| `auth0` | `external_id` | `auth0receiver/config.go:25` |
| `aws_logs_encoding` | `format` | `awslogsencodingextension/config.go:34`, values `cloudtrail`, `s3access` and `elbaccess` |

The receiver type is `aws_s3`. The old name `awss3` still loads and logs a deprecation warning at startup.

### Where the sources differ

- The extension types are `aws_logs_encoding` and `google_cloud_logentry_encoding`, not `awslogs_encoding` and `googlecloudlogentry_encoding` (`metadata.yaml:2`). Pub/Sub `encoding` takes the extension ID.
- The CloudTrail decoder leaves the record body empty. The `sourcetag/aws` processor sets it from `rpc.method`, which holds `eventSource/eventName`, and replaces `/` with a space (for example `ec2.amazonaws.com StartInstances`). The value depends on the `extension.encoding.awslogsencoding.EmitV1RPCConventions` feature gate, which is on by default; with it off, `rpc.method` is the bare `eventName`. A record with no `rpc.method` gets an empty body. Errors appear as `aws.error.code`.
- The `googlecloudlogentry` decoder already sets `http.response.status_code`, `url.path` and `user_agent.original` from `httpRequest` (`log_entry.go:229,252,254`). `sourcetag/gcp` maps `gcp.label.service_name`, `gcp.label.location` and `gcp.label.project_id` to `service.name`, `cloud.region` and `cloud.account.id`.
- The `googlecloudlogentry` decoder rejects pretty-printed JSON. Pub/Sub delivers one compact entry per message, so only the test fixtures are compacted.
- `jsonPayload` stays a map body. `event.category` values (`audit`, `access`, `application`) are a choice the sources do not fix.

### Source tagging

The `sourcetag` processor sets `log.source` and `event.category` and fills cloud resource attributes. It takes one setting, `provider`, which is `aws`, `gcp` or `azure` and has no default. The processor also sets the resource `cloud.service.name` from `log.source` when every sourced record of the resource agrees on one source and the resource has none (`cloudtrail`, `s3`, `elasticloadbalancing`, `eks`, `logs` and `auth0` on AWS; `cloudaudit.googleapis.com`, `run.googleapis.com` and `loadbalancing.googleapis.com` on GCP; `monitor` on Azure). The `auth0` receiver sets `cloud.provider` to `auth0`, with `cloud.account.id` and `cloud.region` from the envelope when it has them. `sourcetag/gcp` takes `cloud.region` from `gcp.label.location`, then `gcp.label.region`, then `gcp.label.zone` with the one-letter zone suffix dropped (`europe-west3-b` gives `europe-west3`, `global` stays), for every GCP source including `gcp.audit` and `gcp.lb`, and gives a record with no severity INFO; `sourcetag/azure` does the same for severity. Load balancer logs from S3 (ALB and NLB) get `cloud.region` and `cloud.account.id` from the target group or certificate ARN in their lines; a classic load balancer line holds no ARN, so those two stay out. The reference config runs one instance per provider in its logs pipeline, and holds no OTTL for tagging:

```yaml
processors:
  sourcetag/aws:
    provider: aws
  sourcetag/gcp:
    provider: gcp
  sourcetag/azure:
    provider: azure
service:
  pipelines:
    logs/aws:
      processors: [memory_limiter, sourcetag/aws, resource_detection]
    logs/gcp:
      processors: [memory_limiter, sourcetag/gcp, resource_detection, batch]
    logs/azure:
      processors: [memory_limiter, sourcetag/azure, resource_detection, batch]
```

| Provider | `log.source` (`event.category`) | Resource attributes |
|---|---|---|
| `aws` | `aws.cloudtrail` (audit), `aws.s3access` and `aws.elb` (access), `aws.eks` (audit for the `kube-apiserver-audit` stream, else application), `aws.cloudwatch` (application) | `cloud.provider` and `cloud.region` from `aws.region`, when unset |
| `gcp` | `gcp.audit` (audit), `gcp.cloudrun` (application), `gcp.lb` (access) | `cloud.provider`, `service.name`, `cloud.region`, `cloud.account.id` |
| `azure` | `azure.activity` (audit) | `cloud.provider` |

The `aws.eks` source needs the resource attributes `cloudwatch.log.group.name` (starting with `/aws/eks/`) and `cloudwatch.log.stream`. A record that already has a `log.source`, such as `auth0`, keeps it, except for `aws.cloudtrail`, `aws.s3access` and `aws.elb` (when the scope `encoding.format` matches), `gcp.audit` and `azure.activity`, which overwrite it. `aws.eks`, `aws.cloudwatch`, `gcp.cloudrun` and `gcp.lb` only fill a missing one. Severity is INFO when unset, WARN for a CloudTrail record with `aws.error.code`.

`otelcol_sourcetag_records_unsourced` counts records that leave the processor without `log.source`, per `provider`. A rise after a roll means a source stopped matching its rule.

`otelcol_sourcetag_records_uncategorized` counts records that leave without `event.category`, per `provider`. Every `aws`, `gcp` and `azure` record is expected to carry one, so a rise means a source kept its `log.source` but lost its category rule. The `auth0` receiver sets its own category.

The processor gives the same output as the `transformprocessor` blocks it replaces, and running both in a row changes nothing. Roll the new image first, then change the values: the old image rejects a `sourcetag` entry at config load, and the new image still runs the old OTTL.

### Identity fill

A GCP audit record leaves the decoder with no `host.name`, no `service.name` and an empty body. The `identityfill` processor, in the image from 0.3.11, fills them. It needs no settings:

```yaml
processors:
  identityfill: {}
service:
  pipelines:
    logs/gcp:
      processors: [memory_limiter, sourcetag/gcp, identityfill, resource_detection, batch]
```

| Value | Default |
|---|---|
| `host.name` | the first non-empty of `k8s.node.name`, `k8s.cluster.name`, `gcp.label.cluster_name` |
| `service.name` | `gcp-audit` for an audit record, `k8s-events` for a Kubernetes event, `k8s-pod-status` for a pod status record |
| body | for an audit record with an empty body, `gcp.audit.method.name` and `gcp.audit.resource.name` joined by a space |

A value the record already has is kept. The reference config runs it after `sourcetag/gcp`, in `logs/gcp` only. AWS and Azure records carry none of the attributes it reads, so `logs/aws` and `logs/azure` do not run it. `otelcol_identityfill_records_filled` counts the values it sets, per rule. `test/scrape/identityfill_test.go` runs a GKE audit entry through the decoder, `sourcetag/gcp` and `identityfill`.

### Staged enablement and rollout

A receiver and its pipeline go into the deployed config only when its Secret exists. A pipeline with no receivers fails config load. A Deployment uses the Recreate strategy with one replica and a ReadWriteOnce volume for `file_storage`, so no second pod runs against the same sources.

A StatefulSet with one replica and a `volumeClaimTemplate` mounted at `/var/lib/otel/storage` fits the same need. The image runs as uid and gid 10001, so set `fsGroup: 10001` on the pod. Run one replica per set of sources. Health checks use `GET /` on port 13133.

Secrets and files the pod needs:

- `AZ_EH_CONN` for Event Hub.
- `GOOGLE_APPLICATION_CREDENTIALS` file for Pub/Sub. Keyless access uses an `external_account` JSON file. Its AWS credential source (`environment_id: aws1`) reads only the AWS environment keys or the instance metadata service. Under IRSA, set `credential_source.file` to the projected service-account token and use an OIDC-type workload identity provider.
- AWS credentials through the SDK default chain; `AUTH0_ROLE_ARN` and `AUTH0_EXTERNAL_ID` when Auth0 is cross-account.
- `CLOUDTRAIL_BUCKET` for CloudTrail (required): the bucket that holds the organization trail.
- `GCP_MON_PROJECT_A` and `GCP_MON_PROJECT_B` for Cloud Run metrics: one project id per receiver instance.
- Queue URLs, bucket and subscription names as plain env values (list at the top of the config).

Size the volume and memory for your event rate.

### CloudTrail digest objects

Digest files under `CloudTrail-Digest/` pass the `s3_prefix` and `.json` suffix checks and parse as one log record each, with `aws.cloudtrail.digest.*` attributes. The receiver has no exclude option. To drop them, add a filter processor on `aws.cloudtrail.digest.s3_object`, or narrow the S3 notification rules to the `CloudTrail/` prefix so they never reach the queue. The notification change was not verified against a live bucket.

### Pub/Sub stream resets

The audit receiver can log `response stream breaking ... InvalidArgument` and reconnect; pending acknowledgements on that stream are lost and their messages are redelivered. The cause and a fix are unknown. Settings to try are `trigger_ack_batch_duration`, `max_outstanding_messages` and `client_id`.

### ALB access logs

The `aws_s3/alb` receiver reads Application Load Balancer access logs from S3 through SQS notifications. It needs three env values: `ALB_BUCKET`, `ALB_PREFIX` and `ALB_SQS_URL`.

- Objects are `.log.gz` files under `AWSLogs/<account>/elasticloadbalancing/<region>/`. The receiver strips `.gz` before it matches the encoding suffix, so the suffix is `.log`.
- `ALB_PREFIX` is the prefix configured on the load balancer, for example `elb/`. It must be a prefix of every object key, with the trailing slash.
- The S3 event notification needs the same prefix and the suffix `.log.gz`, sent to the SQS queue. Give the queue a redrive policy: a message the receiver cannot process returns on every poll until it moves.
- The decoder is `aws_logs_encoding/elbaccess`. It sets `client.address`, `aws.elb.status.code`, `aws.elb.target_processing_time` and the other request fields. The record body is empty. `sourcetag/aws` labels the records `log.source=aws.elb` and `event.category=access`. A deployed config must list `sourcetag` in the `logs/aws` pipeline, or `log.source` stays empty.
- The collector role needs `s3:GetObject` on the prefix, `s3:ListBucket`, `sqs:ReceiveMessage`, `sqs:DeleteMessage` and `sqs:GetQueueAttributes`.

`alb_test.go` runs a synthetic line through the real decoder and `sourcetag/aws`. `alb_e2e_test.go` covers the gunzip and SQS path: it puts a gzipped log under an `.../elasticloadbalancing/.../*.log.gz` key in moto, sends the S3 notification to SQS, runs the built collector with the `aws_s3/alb` block, and checks the records at an OTLP sink and that the SQS message is deleted. A `.txt` object is dropped with an `Unsupported file format` warning, and its notification is deleted too. `test/scrape/restart-durability.sh` runs both tests; see the next section for the requirements.

### CloudWatch log settle lag

Stock `awscloudwatchreceiver` v0.164.1 reads each window up to now, and an empty window moves the start to now. An event that CloudWatch ingests a few seconds after its timestamp is then skipped for good. On Lambda groups, a replay of eight days of data at a one-minute poll missed 4.1% and 5.7% of events in two groups; a 10 s lag and longer missed none.

The scrape image carries a local copy of the receiver, `receiver/awscloudwatchreceiver`, joined by a `replace` in `builder-scrape.yaml`. It adds `logs.settle_lag`, a duration that defaults to 0. With a value, every window ends at now minus the lag, for the request `EndTime` and for the empty-window advance. The checkpoint format is unchanged. If the lag is longer than the time since the start position, the poll for that group waits. The sample sets `30s`. A negative value is refused at startup. Everything else in the receiver is the upstream v0.164.1 code.

### CloudWatch metrics

The `cloudwatchmetrics` receiver reads CloudWatch metrics for every account, region and tier of one config block and feeds the `metrics/aws` pipeline. `awscloudwatch` stays for CloudWatch logs. The receiver is in development stability and is in the scrape image only.

```yaml
cloudwatchmetrics:
  storage: file_storage         # keeps the end of the last window; without it a restart reads only the latest window
  max_catchup: 72h              # how far back missed windows are read after a stop
  accounts:
    - name: prod                # becomes cloud.account.name on every resource
      role_arn: arn:aws:iam::111111111111:role/metrics-reader   # optional; assumed with the pod's own credentials
      external_id: ${env:CW_EXTERNAL_ID}                        # optional, needs role_arn
      regions: [us-east-1, eu-west-1]
  tiers:
    operational:
      collection_interval: 15m
      period: 15m
      delay: 10m
      stats: [Average, Maximum]
      limit: 1000
      namespaces:
        AWS/EC2: [CPUUtilization, NetworkIn]
    daily:
      collection_interval: 24h
      period: 24h
      delay: 1h
      stats: [Average, Minimum, Maximum, Sum, SampleCount, p50]
      limit: 10000
      namespaces:
        AWS/RDS: []             # an empty list reads every metric in the namespace
```

- Every account, region and tier pair is polled on its own schedule. A pair whose role is refused logs one warning, is tried again at the next poll, and does not stop the others. The config takes no access keys or session token: credentials come from the pod's role, and `role_arn` with `external_id` assumes another role once per account.
- A tier must set `collection_interval`, `period`, `stats`, `limit` and `namespaces` (at least one). `stats` is never defaulted: each statistic is one query, so a tier reads only what it lists. `period` is a whole number of seconds that CloudWatch accepts: 1, 5, 10 or 30 seconds, or a multiple of 60 seconds. `collection_interval` must be a multiple of `period`, and not below it, so windows never overlap. `delay` shifts the window back for CloudWatch's publication lag. A region listed twice for an account, a statistic listed twice in a tier, and a metric name listed twice in a namespace are rejected at startup.
- For each listed metric name the receiver calls `ListMetrics` with that namespace and name only, and keeps each series with its own dimension set. Reading goes through `GetMetricData` in calls of at most 500 queries.
- Each statistic of a series is one gauge point named `amazonaws.com/<Namespace>/<MetricName>` with the attributes `Namespace`, `MetricName`, `Dimensions` and `stat`. Resources carry `cloud.provider`, `cloud.region` and `cloud.account.name`.
- Discovery keeps paging past `limit`, so the whole total is counted, but only the first `limit` series are read. Three gauges, each with the attributes `account`, `region` and `tier`, show it: `otelcol_aws_cloudwatch_series_discovered`, `otelcol_aws_cloudwatch_series_limit` and `otelcol_aws_cloudwatch_discovery_truncated` (1 or 0). One warning is logged each time a run of truncated polls starts. The three gauges are the collector's own telemetry, not points in the `metrics/aws` pipeline: they reach a backend only when `service::telemetry::metrics` exports them.
- Windows are aligned to the period in UTC. A daily tier (`period: 24h`) reads the previous complete UTC day. After each delivered window the receiver saves its end in the storage extension. After a stop it reads every whole missed window, oldest first, up to `max_catchup`; a restart inside a window reads nothing again. Each missed window is its own set of requests, billed as such: a 15-minute tier stopped for the full 72 hours reads 288 windows. A missing or unreadable entry counts as no state, and only the latest window is read.
- `p50` and other percentile statistics are not available for every metric. When a metric returns data for the other statistics but none for a percentile, no point is emitted, nothing is logged as an error, and one info line per metric name is logged for the run.
- `GetMetricData` is billed per metric requested, and a tier's request count is its series times its scrapes a day. Watch the CloudWatch usage in the bill after the first day.

### BMC sensors over IPMI

The `ipmi` receiver reads temperatures, fans, voltages, power supply and chassis intrusion states from baseboard
management controllers (BMCs) over IPMI, with the `hw.*` names of the OpenTelemetry semantic conventions. It is in the
scrape image only and is meant for a Deployment of its own: the lab BMC network, the shared BMC account and the risk of
locking that account out should not sit in the pod that holds cloud credentials. `test/configs/scrape-ipmi.yaml` runs
the receiver alone and `test/configs/scrape-ipmi-deployment.yaml` has the Deployment, an ExternalSecret that fills the
credentials from a parameter store, and a network policy with UDP 623 egress to one subnet. No credential is in the
repository; `just check-ipmi-deployment` fails on a plain Secret object or a literal credential.

```yaml
ipmi:
  collection_interval: 60s
  max_concurrent: 8        # BMCs read at once
  exchange_timeout: 3s     # one request and its answer; one retry follows a timeout
  scrape_timeout: 20s      # the whole read of one BMC
  bmcs:
    - name: bmc-01         # becomes host.name
      address: 192.0.2.11  # host or host:port, port 623 by default
      username: ${env:IPMI_USERNAME}
      password: ${env:IPMI_PASSWORD}
      vendor: supermicro   # supermicro or dell
      cipher_suite: 3      # required for IPMI 2.0; never probed
      ipmi_version: "2.0"  # 2.0 (default), 1.5 or auto
      privilege_level: user      # user (default), operator or administrator
      dcmi_power: false          # default
      power_interval: 5m         # default
      fru_interval: 24h          # default, at least 1h; reads the BMC FRU inventory
```

| Metric | Unit | Source |
|---|---|---|
| `hw.temperature` | `Cel` | Temperature sensors |
| `hw.temperature.limit` | `Cel` | The SDR upper critical (`critical`) and upper non-critical (`high.degraded`) thresholds |
| `hw.fan.speed` | `rpm` | Fan sensors that read in RPM |
| `hw.voltage` | `V` | Voltage sensors |
| `hw.status` | `1` | Power supply status sensors (`hw.type` `power_supply`) and the chassis intrusion sensor (`enclosure`, name `chassis intrusion`); one point per `hw.state`, 1 for the current state |
| `hw.host.power` | `W` | DCMI power, only with `dcmi_power: true` |
| `hw.bmc.up` | `1` | 1 when the scrape worked; 0 with a `reason` (`timeout`, `auth`, `refused`, `backoff`) when not |
| `hw.bmc.scrape.duration` | `s` | Time of the scrape |
| `inventory.bmc.fru.info` | `1` | FRU board, product and chassis records, and the DIMM and processor FRU devices, once per `fru_interval`; attributes in `docs/otel-metrics.md` |
| `inventory.collection.status` | `1` | 1 when the FRU read worked, 0 when it did not (`inventory.source` `fru`), once per `fru_interval` |

Every resource has `host.name` (the BMC `name`), `hw.vendor`, `server.address` (the BMC address without the port),
`hw.bmc.protocol` (`ipmi`) and `hw.firmware_version` from the device ID. The collector's own telemetry has the counter
`otelcol_hw_bmc_auth_failures`, by `bmc`.

How a scrape runs:

- One goroutine per BMC, at most `max_concurrent` reading at once. The first scrape of each BMC starts after an offset
  taken from a hash of its name, so the boards are not hit in the same second.
- Each scrape opens one session, reads and closes it. No keepalive runs, so no session is left on a board that has few
  session slots.
- The SDR is read once per BMC and cached. It is read again when the repository timestamp changes and every 6 hours.
  Each scrape calls Get Sensor Reading for the selected sensors only. A sensor with no reading, one marked not present
  and a sensor type outside the table give no point and no error.
- The session uses `privilege_level`; sensors and the SDR need only `user`, and a higher value logs one warning at
  start. The cipher suite is the one set per BMC. The receiver never tries other suites: each try is one more failed
  login on an account the boards share.
- `ipmi_version: 1.5` and `auto` use IPMI 1.5 session authentication, which has no session integrity check. Use them
  for old boards only; `2.0` with a cipher suite is the default. The login-failure back-off covers IPMI 2.0 only:
  with 1.5 a wrong user name or password is answered with a completion code, not recognised as a login failure, so
  the receiver tries again at every interval and counts no authentication failure (not tested on a real board).
  Check the account before you use 1.5 on a shared board.
- The receiver sends no chassis, power, user or configuration command, and its own requests are reads (the FRU read uses Get FRU Inventory Area Info and Read FRU Data of the storage
  network function, and the SDR read for the FRU device records). The go-ipmi
  library itself sends the session setup and close commands (Set Session Privilege Level, Close Session) as part of
  connecting, and those are the only other commands on the wire.
- DCMI power needs an administrator session on the lab boards (it is denied at user and operator level). With
  `dcmi_power: true` the receiver opens one administrator session per `power_interval`, makes one power read and closes
  the session at once. Off by default. `power_interval` must be at least 1m. An account that cannot open an administrator session stops the power reads at
  the first refusal and does not count as a failed login.

| Case | Result |
|---|---|
| Wrong password, wrong user name, or a role the account may not have | `hw.bmc.up` 0, reason `auth` (go-ipmi reports the failure as text, and the receiver matches the text); the BMC is not contacted for 15 minutes, then 30, doubling to 6 hours; after three failures in a row it stays stopped until restart; the counter `otelcol_hw_bmc_auth_failures` grows |
| BMC slow or hung | The read stops at `scrape_timeout`, the session is closed, `hw.bmc.up` 0, reason `timeout`; the other BMCs are not delayed |
| Connection refused or another error | `hw.bmc.up` 0, reason `refused`; tried again at the next interval |
| A BMC in back-off or stopped | `hw.bmc.up` 0, reason `backoff`, with no contact |
| Failed scrape | Only `hw.bmc.up` 0 and `hw.bmc.scrape.duration` are sent: no value of an earlier scrape is sent again |
| DCMI power: invalid command | The BMC has no DCMI power; the receiver does not ask again until restart |
| DCMI power: `0xD2` (initializing) | Tried again at the next `power_interval` |
| DCMI power: a reply that is too short or is not a DCMI answer | One warning for the BMC until restart; no power point for that interval; read again at the next `power_interval` |
| DCMI power: `0xD4` (insufficient privilege), or a role refusal when the administrator session opens | One warning that the account is not an administrator; power reads stop until restart; a role refusal is not counted as a failed login |

Alert rules: `hw.bmc.up` equal to 0 for 15 minutes, and no `hw.bmc.up` series at all for three collection intervals (a
wedged receiver stops sending `up` too). The Deployment's health check and the collector's receiver metrics cover the
process.

### Proxmox VE and SNMP pollers

The scrape image also holds the `pve` and `snmp` receivers, so one scrape pod can poll Proxmox VE nodes and SNMP
devices over the network; the edge builds keep them too. Both run in the pod that already reads CloudTrail and
Lambda. A pod reaches the lab only if its network policy allows it: TCP 8006 to the PVE nodes and UDP 161 to the
devices. `netflow` stays in the edge builds only: it listens on a UDP port for exporters, which the scrape pod
does not expose. A sample with both is `test/configs/scrape-pve-snmp.yaml`.

**`pve`.** Run one instance per node, such as `pve/pve12`, and set `guests.node` on every instance. The guest
scraper reads `/nodes/<guests.node>/...` and nothing else. When `guests.node` is empty it takes the short host name
of the machine the collector runs on, which in a pod is the pod name and no PVE node: every call then fails with
an error from PVE and no guest series appear. Each instance logs one info line at start with the node it chose
(`node`, and `source`: `guests.node` or `host name`). The node name never comes from `base_urls`. Point `base_urls` at that
node, or at any other member of its cluster: PVE answers a request for `/nodes/<other>/...` on a member by
forwarding it to the node named in the path, so one endpoint can serve the guests of several nodes, at the cost of
one more hop. That forwarding is PVE behaviour that this receiver relies on and that was not run against a cluster
here. A token is cluster-wide, so one guests token serves every instance. Enable `resources` on one instance only:
it reads the whole cluster, and a second instance doubles the series.

**Secret and Deployment example.** `test/configs/scrape-pve-deployment.yaml` holds a Secret with the two API
tokens (placeholders), a Deployment that reads them into `PVE_RESOURCES_TOKEN` and `PVE_GUESTS_TOKEN`, the PVE CA
mounted at `/etc/otelcol/pve-ca.pem`, and a network policy that allows TCP 8006 to the PVE nodes and DNS. Create the
real Secret from a parameter store or with `kubectl create secret`; do not commit token values.

**Proxmox VE API permissions.** On PVE 8 the guest scrape (`guests`) needs `VM.Audit` and `VM.Monitor` on `/vms`:
without `VM.Monitor` the guest agent calls answer 403 (seen on PVE 8.0.3). An ACL set on `/vms` replaces the role the
user inherits from `/`; it does not add to it. The role on `/vms` must therefore include `VM.Audit` itself, or the
guest information and the I/O metrics disappear while the node metrics stay. Check the grant with the token you
give the receiver, not with an administrator account.

**`snmp`.** One instance per device, as on the edge. Secrets (`community`, `auth_password`, `privacy_password`)
come from `${env:NAME}`; `scripts/check-snmp-secrets.sh` fails the build when a sample holds a literal value.

### Cloud Run metrics

The `googlecloudmonitoring` receiver polls Cloud Monitoring and feeds the `metrics/gcp` pipeline. It is alpha.

- `project_id` takes one project, so the sample has one named instance per project: `googlecloudmonitoring/cloudrun-a` and `googlecloudmonitoring/cloudrun-b`. Their projects come from `GCP_MON_PROJECT_A` and `GCP_MON_PROJECT_B`.
- The sample polls `run.googleapis.com/request_count`, `request_latencies`, `container/cpu/utilizations`, `container/memory/utilizations` and `container/instance_count`.
- `collection_interval` defaults to 5m and must be at least 60s. Every scrape calls the Cloud Monitoring API, which has quotas and charges.
- Credentials come from the Google default credentials, as for Pub/Sub, with the `monitoring.read` scope. The Cloud Monitoring API must be enabled on each polled project.
- A project the identity cannot read is skipped with a WARN naming the project, and the receiver keeps the others; it is not tried again until the collector restarts. Receiver start fails only when no listed project can be read, or when the credentials are missing, and then the collector does not start. A pipeline with no credentials must be removed from the deployed config, like any other source.
- Metric names are the Cloud Monitoring metric types. A delta metric stays delta in the OTLP output, so check that the receiving backend accepts delta temporality. This was not verified.

#### Daily tier with aggregation

The scrape image carries a local copy of the receiver, `receiver/googlecloudmonitoringreceiver`, joined by a `replace` in `builder-scrape.yaml`. It adds three keys. Without them the receiver behaves as the upstream v0.164.1 code, with one `project_id`, one request per metric and a window of at most 23 h.

- `projects` is a list of project names. It adds to `project_id`, so one instance covers every project and no per-project instance is needed. At least one of `project_id` and `projects` is required. Metric descriptors are read from each project and kept for that project, so one metric type with different descriptors in two projects is handled for each, and a type found in one project only is asked for in that project only.
- `aggregation.alignment_period` is the width of the bucket Cloud Monitoring aligns each series over, at least one minute and not longer than `collection_interval`. `24h` gives one point per series for a daily window.
- `aggregation.per_series_aligners` lists the aligners, each sent as its own request: `ALIGN_MIN`, `ALIGN_MAX`, `ALIGN_MEAN`, `ALIGN_SUM`, `ALIGN_COUNT` and `ALIGN_PERCENTILE_50`. Any other value is refused at startup. The receiver sends one `ListTimeSeries` request per project, metric type and aligner, and emits each aligned result as a gauge with the attribute `stat` set to `min`, `max`, `mean`, `sum`, `count` or `p50`. A `count` result has the unit `1`.
- With `aggregation` set, `collection_interval` can be longer than 23 h; the window is the whole interval, ending at the metric's ingest delay (60 s when the descriptor gives none). Without `aggregation`, a `collection_interval` above 23 h is refused at startup.
- `ALIGN_PERCENTILE_50` applies to distribution metrics only. For a metric that is not a distribution the receiver sends no request, emits no point and logs no error; it logs one INFO line per metric name in each run.
- When Cloud Monitoring rejects a request, for example an aligner that does not fit the metric kind or a project that denies access, the receiver logs a WARN with the project, metric and aligner and goes on with the other requests; a run without aggregation skips the failing project the same way and reports an error only when every project failed. Whether a given metric type accepts a given aligner was not checked for every type.
- Each request counts against the Cloud Monitoring quota: a daily instance with 2 projects, 2 metrics and 6 aligners sends 24 requests a day, fewer when `ALIGN_PERCENTILE_50` skips non-distribution metrics.

```yaml
receivers:
  googlecloudmonitoring/daily:
    projects: [project-a, project-b]
    collection_interval: 24h
    aggregation:
      alignment_period: 24h
      per_series_aligners: [ALIGN_MIN, ALIGN_MAX, ALIGN_MEAN, ALIGN_SUM, ALIGN_COUNT, ALIGN_PERCENTILE_50]
    metrics_list:
      - metric_name: run.googleapis.com/container/cpu/utilizations
```

`test/configs/scrape.yaml` has this instance in the `metrics/gcp-daily` pipeline.

### API permissions

| Receiver | Permissions needed |
|---|---|
| `aws_s3` | `s3:GetObject` on the prefix, `s3:ListBucket`, `sqs:ReceiveMessage`, `sqs:DeleteMessage`, `sqs:GetQueueAttributes` |
| `awscloudwatch` logs | `logs:DescribeLogGroups`, `logs:FilterLogEvents` |
| `cloudwatchmetrics` | `cloudwatch:ListMetrics` and `cloudwatch:GetMetricData` in each account; `sts:AssumeRole` on each `role_arn` for the pod's role |
| `googlecloudpubsub` | Pub/Sub subscriber role on each subscription |
| `googlecloudmonitoring` | `roles/monitoring.viewer` on each polled project (`monitoring.timeSeries.list` and `monitoring.metricDescriptors.list`); role contents are from Google's IAM documentation |
| `azure_event_hub` | Listen on the Event Hub through the connection string |
| `auth0` | `sqs:ReceiveMessage` and `sqs:DeleteMessage` on its queue; `sts:AssumeRole` on `role_arn` when set |

## Restart durability and credential scan

`test/scrape/restart-durability.sh` builds the scratch collector for linux/amd64 and runs `TestRestartDurability` and the kill-at-intake tests below (build tag `durability`). It needs Docker and pulls `motoserver/moto` and `alpine`.

The test stands in moto for S3 and SQS, because LocalStack now refuses to start without a paid auth token. It seeds 3 CloudTrail objects with 3 records each and one SQS notification per object. The collector config is built from the reference config: same `file_storage`, `memory_limiter`, `sourcetag/aws`, `batch` and `otlp_http` blocks, with the receiver pointed at moto. A named volume holds `file_storage`. The OTLP sink runs on the host and answers 503 until released.

The test runs this procedure:

1. With the sink refusing, all 9 records reach the sink as attempts and none are accepted.
2. `docker kill` stops the collector. The volume still holds queue files.
3. After `docker start` and 5 seconds, still none are accepted.
4. After the sink is released, all 9 records arrive with `log.source=aws.cloudtrail`.

With `storage: file_storage` removed from `sending_queue`, the test fails at step 2, so it does detect a missing disk queue.

Credential scan: the collector ran with fake `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` and `AWS_SESSION_TOKEN` values. None appeared in the container logs, which include `debug` exporter output at `detailed` verbosity, or in the zpages pages `pipelinez`, `servicez`, `extensionz` and `featurez`.

### Kill at intake

The `aws_s3` receiver deletes an SQS message once the next consumer accepts the objects (`s3sqsreader.go:278`). A `batch` processor accepts them before the exporter queue holds them, so a kill inside its timeout loses records that SQS no longer has. The persistent queue is therefore the first buffer. `logs/aws` has no `batch` processor, and each S3 object goes out as its own OTLP request.

- `TestKillAtIntakeLosesNothing` kills the collector as soon as every SQS message is deleted. All 9 records arrive.
- `TestKillAtIntakeWithBatchLosesRecords` is the control. It adds a `batch` processor with a 10 s timeout ahead of the queue and kills at the same point. 0 of 9 records arrive, so the kill test does detect the loss.
- `sending_queue.batch` exists at v0.154.0 (`exporterhelper/internal/queuebatch/config.go:46`). It is not enabled and was not tested.

`logs/gcp`, `logs/azure` and `metrics/aws` still have `batch`. Whether their sources acknowledge messages before the queue holds the data was not checked.

## Runbook

### Where to look

- Logs: the container's stdout and stderr, for example `kubectl logs <pod>`. The collector writes no log file.
- Health: `GET /` on port 13133 inside the pod.
- Queue and read positions: the `file_storage` volume at `/var/lib/otel/storage`. Keep it across restarts, or the receivers re-read their sources.

### Roll back

Set the Deployment or StatefulSet image to the previous tag and apply it. A Deployment uses the Recreate strategy and a StatefulSet uses RollingUpdate. With one replica and a ReadWriteOnce volume, both stop the old pod before the new one starts, so there is a short gap and one pod at a time reads the sources. Tags exist for v0.3.1 to v0.3.11. Keep the volume, so the queue and the read positions carry over.

### Alerts

`docs/alert-queries.sql` covers the Windows edge collector only. A silent scrape collector shows as a pod that is not ready or restarting, so alert on that in the cluster monitoring.
