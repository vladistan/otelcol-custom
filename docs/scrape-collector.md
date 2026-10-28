# Scrape Collector

## Key review at v0.154.0 (Step 1.9)

Sources read from the Go module cache at tag v0.154.0 of opentelemetry-collector-contrib.

| Component | Result | Source |
|---|---|---|
| `awslogsencodingextension` | present; CloudTrail and S3 access-log formats exist | `internal/unmarshaler/cloudtraillog`, `internal/unmarshaler/s3-access-log` |
| `googlecloudlogentryencodingextension` | present | `log_entry.go` |
| `googlecloudpubsubreceiver` | present; keys `project`, `subscription`, `encoding`, `ignore_encoding_error` | `config.go:20-38` |
| `awscloudwatchreceiver` logs | present; `logs.groups.autodiscover` (`prefix` or `pattern`, not both) or `logs.groups.named`, not both modes | `config.go:84-120` |
| `awscloudwatchmetricsreceiver` | **missing at v0.154.0**, last tag v0.133.0 (stop rule 07-S2) | see below |
| `resourcedetectionprocessor` `gcp` detector | present, name `gcp` | `factory.go:81`, `internal/gcp/gcp.go:27` |

### awscloudwatchmetricsreceiver

Removed in contrib v0.134.0 as unmaintained ("`receiver/awscloudwatchmetrics`: Remove unmaintained component", PR #42238). Its v0.133.0 `metadata.yaml` already set `unmaintained: [metrics]`.

`awscloudwatchreceiver` v0.154.0 has a `metrics` block (stability alpha for logs and metrics, `README.md:9`). It is the likely replacement. Keys: `metrics.queries[]` (`namespace`, `metric_name`, `dimensions`, `stats`) or `metrics.discovery` (`filters`, `limit`, `stats`), plus `period` (5m) and `delay` (10m) (`config.go:38-82`). The old `metrics.named` key has no equivalent name. Output follows the CloudWatch Metric Streams OpenTelemetry 1.0.0 format. The ruling on 07-S2 is open.

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

Confirmed. `processRecords` walks the `Records` array and appends one log record per entry (`cloudtraillog/unmarshaler.go:270-324`). No splitting step is needed in Step 5.6, so the contingency does not apply.

### GCP log entry mapping

| LogEntry field | Output key | Source |
|---|---|---|
| `httpRequest.requestUrl` | `url.path` (path part) | `log_entry.go:229` |
| `httpRequest.status` | `http.response.status_code` | `log_entry.go:252` |
| `httpRequest.userAgent` | `user_agent.original` | `log_entry.go:254` |
| `logName` project | resource `gcp.project` | `log_entry.go:32,478` |
| `resource.type` | resource `gcp.resource_type` | `README.md:73` |

The encoding sets no `service.name`, `cloud.region` or `cloud.account.id` from `resource.labels` for ordinary entries. Step 5.6 must derive `cloud.account.id` from `gcp.project`, and read region and service from the label keys in the fixtures. `cloud.region` appears in the README only for one format (`README.md:467`, `location`).

### resourcedetection gcp detector

Present. It sets `cloud.provider`, `cloud.account.id` (project id) and `cloud.platform`. On GKE it also sets `k8s.cluster.name` and the zone or region (`internal/gcp/gcp.go:80-89`). On other platforms it sets `cloud.region` from the platform-specific region callable (`gcp.go:104-138`).
