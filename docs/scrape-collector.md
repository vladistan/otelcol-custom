# Scrape Collector

## Key review at v0.154.0

Sources read from the Go module cache at tag v0.154.0 of opentelemetry-collector-contrib.

| Component | Result | Source |
|---|---|---|
| `awslogsencodingextension` | present; CloudTrail and S3 access-log formats exist | `internal/unmarshaler/cloudtraillog`, `internal/unmarshaler/s3-access-log` |
| `googlecloudlogentryencodingextension` | present | `log_entry.go` |
| `googlecloudpubsubreceiver` | present; keys `project`, `subscription`, `encoding`, `ignore_encoding_error` | `config.go:20-38` |
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

### resourcedetection gcp detector

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
| `awss3` | `s3downloader.region` | `awss3receiver/config.go:19` |
| `awss3` | `s3downloader.s3_bucket` | `awss3receiver/config.go:20`, required even with `sqs` (`config.go:94`) |
| `awss3` | `s3downloader.s3_prefix` | `awss3receiver/config.go:21` |
| `awss3` | `sqs.queue_url` | `awss3receiver/config.go:36` |
| `awss3` | `sqs.region` | `awss3receiver/config.go:38` |
| `awss3` | `encodings[].extension` | `awss3receiver/config.go:59` |
| `awss3` | `encodings[].suffix` | `awss3receiver/config.go:60`, matched by `HasSuffix` after `.gz` is stripped (`receiver.go:128-139,311-318`) |
| `awscloudwatch` | `region` | `awscloudwatchreceiver/config.go:30` |
| `awscloudwatch` | `storage` | `awscloudwatchreceiver/config.go:35` |
| `awscloudwatch` | `logs.poll_interval` | `awscloudwatchreceiver/config.go:87` |
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
| `azure_event_hub` | `connection` | `azureeventhubreceiver/config.go:28`, needs `SharedAccessKeyName` |
| `azure_event_hub` | `storage` | `azureeventhubreceiver/config.go:32` |
| `azure_event_hub` | `format` | `azureeventhubreceiver/config.go:34` |
| `auth0` | `queue_url` | `auth0receiver/config.go:21` |
| `auth0` | `region` | `auth0receiver/config.go:22` |
| `auth0` | `role_arn` | `auth0receiver/config.go:24` |
| `auth0` | `external_id` | `auth0receiver/config.go:25` |
| `aws_logs_encoding` | `format` | `awslogsencodingextension/config.go:34`, values `cloudtrail` and `s3access` |

### Where the sources differ

- The extension types are `aws_logs_encoding` and `google_cloud_logentry_encoding`, not `awslogs_encoding` and `googlecloudlogentry_encoding` (`metadata.yaml:2`). Pub/Sub `encoding` takes the extension ID.
- The CloudTrail decoder leaves the record body empty. The `transform/aws` block sets it to `<service> <method>`. Errors appear as `aws.error.code`, not `errorCode`.
- The `googlecloudlogentry` decoder already sets `http.response.status_code`, `url.path` and `user_agent.original` from `httpRequest` (`log_entry.go:229,252,254`). The transform adds only the resource mapping: `gcp.label.service_name`, `gcp.label.location` and `gcp.label.project_id` become `service.name`, `cloud.region` and `cloud.account.id`.
- The `googlecloudlogentry` decoder rejects pretty-printed JSON. Pub/Sub delivers one compact entry per message, so only the test fixtures are compacted.
- `jsonPayload` stays a map body. `event.category` values (`audit`, `access`, `application`) are a choice the sources do not fix.

### Staged enablement and rollout

A receiver and its pipeline go into the deployed config only when its Secret exists. A pipeline with no receivers fails config load. A Deployment uses the Recreate strategy with one replica and a ReadWriteOnce volume for `file_storage`, so no second pod runs against the same sources.

Secrets and files the pod needs:

- `AZ_EH_CONN` for Event Hub.
- `GOOGLE_APPLICATION_CREDENTIALS` file for Pub/Sub.
- AWS credentials through the SDK default chain; `AUTH0_ROLE_ARN` and `AUTH0_EXTERNAL_ID` when Auth0 is cross-account.
- Queue URLs, bucket and subscription names as plain env values (list at the top of the config).

Size the volume and memory for your event rate.

### API permissions

| Receiver | Permissions needed |
|---|---|
| `awss3` | `s3:GetObject` on the prefix, `s3:ListBucket`, `sqs:ReceiveMessage`, `sqs:DeleteMessage`, `sqs:GetQueueAttributes` |
| `awscloudwatch` logs | `logs:DescribeLogGroups`, `logs:FilterLogEvents` |
| `awscloudwatch` metrics | `cloudwatch:GetMetricData` (`metrics.queries` only; no discovery, so no `cloudwatch:ListMetrics`) |
| `googlecloudpubsub` | Pub/Sub subscriber role on each subscription |
| `azure_event_hub` | Listen on the Event Hub through the connection string |
| `auth0` | `sqs:ReceiveMessage` and `sqs:DeleteMessage` on its queue; `sts:AssumeRole` on `role_arn` when set |

## Restart durability and credential scan

`test/scrape/restart-durability.sh` builds the scratch collector for linux/amd64 and runs `TestRestartDurability` and the kill-at-intake tests below (build tag `durability`). It needs Docker and pulls `motoserver/moto` and `alpine`.

The test stands in moto for S3 and SQS, because LocalStack now refuses to start without a paid auth token. It seeds 3 CloudTrail objects with 3 records each and one SQS notification per object. The collector config is built from the reference config: same `file_storage`, `memory_limiter`, `transform/aws`, `batch` and `otlphttp` blocks, with the receiver pointed at moto. A named volume holds `file_storage`. The OTLP sink runs on the host and answers 503 until released.

The test runs this procedure:

1. With the sink refusing, all 9 records reach the sink as attempts and none are accepted.
2. `docker kill` stops the collector. The volume still holds queue files.
3. After `docker start` and 5 seconds, still none are accepted.
4. After the sink is released, all 9 records arrive with `log.source=aws.cloudtrail`.

With `storage: file_storage` removed from `sending_queue`, the test fails at step 2, so it does detect a missing disk queue.

Credential scan: the collector ran with fake `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` and `AWS_SESSION_TOKEN` values. None appeared in the container logs, which include `debug` exporter output at `detailed` verbosity, or in the zpages pages `pipelinez`, `servicez`, `extensionz` and `featurez`.

### Kill at intake

The `awss3` receiver deletes an SQS message once the next consumer accepts the objects (`s3sqsreader.go:278`). A `batch` processor accepts them before the exporter queue holds them, so a kill inside its timeout loses records that SQS no longer has. The persistent queue is therefore the first buffer. `logs/aws` has no `batch` processor, and each S3 object goes out as its own OTLP request.

- `TestKillAtIntakeLosesNothing` kills the collector as soon as every SQS message is deleted. All 9 records arrive.
- `TestKillAtIntakeWithBatchLosesRecords` is the control. It adds a `batch` processor with a 10 s timeout ahead of the queue and kills at the same point. 0 of 9 records arrive, so the kill test does detect the loss.
- `sending_queue.batch` exists at v0.154.0 (`exporterhelper/internal/queuebatch/config.go:46`). It is not enabled and was not tested.

`logs/gcp`, `logs/azure` and `metrics/aws` still have `batch`. Whether their sources acknowledge messages before the queue holds the data was not checked.
