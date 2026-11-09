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

`awscloudwatchreceiver` v0.154.0 has a `metrics` block (stability alpha for logs and metrics, `README.md:9`). It is the likely replacement. Keys: `metrics.queries[]` (`namespace`, `metric_name`, `dimensions`, `stats`) or `metrics.discovery` (`filters`, `limit`, `stats`), plus `period` (5m) and `delay` (10m) (`config.go:38-82`). The old `metrics.named` key has no equivalent name. Output follows the CloudWatch Metric Streams OpenTelemetry 1.0.0 format. Check it against your data before relying on it as a replacement.

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
| `googlecloudpubsub` | `project` | `googlecloudpubsubreceiver/config.go:20` |
| `googlecloudpubsub` | `subscription` | `googlecloudpubsubreceiver/config.go:31`, must match `projects/<project>/subscriptions/<name>` (`config.go:72`) |
| `googlecloudpubsub` | `encoding` | `googlecloudpubsubreceiver/config.go:33` |
| `googlecloudpubsub` | `ignore_encoding_error` | `googlecloudpubsubreceiver/config.go:38` |
| `googlecloudmonitoring` | `project_id` | `googlecloudmonitoringreceiver/config.go:23` |
| `googlecloudmonitoring` | `collection_interval` | `scraperhelper.ControllerConfig`; default 5m, at least 60s (`googlecloudmonitoringreceiver/config.go:15-16,42`) |
| `googlecloudmonitoring` | `metrics_list[].metric_name` | `googlecloudmonitoringreceiver/config.go:31,35`, list required (`config.go:46`) |
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

The `sourcetag` processor sets `log.source` and `event.category` and fills cloud resource attributes. It takes one setting, `provider`, which is `aws`, `gcp` or `azure` and has no default. The reference config runs one instance per provider in its logs pipeline, and holds no OTTL for tagging:

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

### Cloud Run metrics

The `googlecloudmonitoring` receiver polls Cloud Monitoring and feeds the `metrics/gcp` pipeline. It is alpha.

- `project_id` takes one project, so the sample has one named instance per project: `googlecloudmonitoring/cloudrun-a` and `googlecloudmonitoring/cloudrun-b`. Their projects come from `GCP_MON_PROJECT_A` and `GCP_MON_PROJECT_B`.
- The sample polls `run.googleapis.com/request_count`, `request_latencies`, `container/cpu/utilizations`, `container/memory/utilizations` and `container/instance_count`.
- `collection_interval` defaults to 5m and must be at least 60s. Every scrape calls the Cloud Monitoring API, which has quotas and charges.
- Credentials come from the Google default credentials, as for Pub/Sub, with the `monitoring.read` scope. The Cloud Monitoring API must be enabled on each polled project.
- A project the identity cannot read fails receiver start, and the collector does not start. A pipeline with no credentials must be removed from the deployed config, like any other source.
- Metric names are the Cloud Monitoring metric types. A delta metric stays delta in the OTLP output, so check that the receiving backend accepts delta temporality. This was not verified.

### API permissions

| Receiver | Permissions needed |
|---|---|
| `aws_s3` | `s3:GetObject` on the prefix, `s3:ListBucket`, `sqs:ReceiveMessage`, `sqs:DeleteMessage`, `sqs:GetQueueAttributes` |
| `awscloudwatch` logs | `logs:DescribeLogGroups`, `logs:FilterLogEvents` |
| `awscloudwatch` metrics | `cloudwatch:GetMetricData` (`metrics.queries` only; no discovery, so no `cloudwatch:ListMetrics`) |
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

Set the Deployment or StatefulSet image to the previous tag and apply it. The Recreate strategy stops the old pod before the new one starts, so no second pod reads the same sources. Tags exist for v0.3.1 to v0.3.10. Keep the volume, so the queue and the read positions carry over.

### Alerts

`docs/alert-queries.sql` covers the Windows edge collector only. A silent scrape collector shows as a pod that is not ready or restarting, so alert on that in the cluster monitoring.
