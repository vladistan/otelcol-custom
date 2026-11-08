# otel-cluster on the Gateway Image

`otel-cluster` runs `ghcr.io/vladistan/otelcol-gateway:v<VERSION>` instead of the stock contrib image. The `k8sevent` processor with `ottl_parity` replaces the `transform/k8s_objects` OTTL block.

## Image tag rule

`otel-cluster` takes a gateway tag only after `otel-main` has run that tag. A tag that fails in `otel-main` never reaches `otel-cluster`. The release notes for each gateway version name the tag to use.

## What changes in the values

| Key | Stock | Gateway |
|---|---|---|
| `image.repository` | `ghcr.io/open-telemetry/opentelemetry-collector-releases/opentelemetry-collector-contrib` | `ghcr.io/vladistan/otelcol-gateway` |
| `command.name` | chart default | `otelcol-gateway` |
| logs processors | `transform/k8s_objects` first | `k8sevent` first |
| cluster name | not set here | `resource/cluster` sets `k8s.cluster.name` |

Everything else in the values stays: receivers, `resource/k8s`, `k8s_attributes`, `memory_limiter`, `batch`, exporters and extensions.

## k8sevent output

`k8sevent` with `ottl_parity: true` sets the same fields as the OTTL block on Event and Pod records: `k8s.object.kind`, `k8s.object.action`, `k8s.namespace`, `k8s.name`, the `k8s.event.*` and `k8s.pod.phase` fields, `k8s.container.state` and `k8s.container.exit_code`, the `k8s.labels.*` fields, `message_type`, `message` and `k8s.object.full`. The body equals `message`. Severity is WARN or INFO for Events and INFO for Pods.

`k8sevent` also keeps its own attributes (`k8s.pod.name`, `k8s.pod.restarts`, `k8s.pod.ready`, `k8s.node.name`, `k8s.event.source`). Other object kinds pass unchanged. The cluster name is never set by the processor.

`test/bench/k8sevent_parity_test.go` runs the OTTL block from the kube-v0 values and the processor on the same records and compares every field the block sets.

## Filling host.name and service.name

Cluster logs reach ClickHouse with an empty `HostName` and `ServiceName` unless the resource carries `host.name` and `service.name`. The `identityfill` processor fills them. It sits after `resource/hostname` and before `batch`. With no settings it applies the three rules below, and it never overwrites a value that is already set. An empty string counts as unset.

| Rule | Applies to | Result |
|---|---|---|
| `host_name` | any record whose resource has no `host.name` | the first non-empty of `k8s.node.name`, `k8s.cluster.name`, `gcp.label.cluster_name` |
| `service_name` | a record with `k8s.event.reason` | `k8s-events` |
| | a record with `k8s.pod.phase` | `k8s-pod-status` |
| | a GCP audit record | `gcp-audit` |
| `audit_body` | a GCP audit record with an empty body | `<gcp.audit.method.name> <gcp.audit.resource.name>`, or whichever of the two exists |

A record is a GCP audit record when `cloud.resource_id` starts with `cloudaudit` or `gcp.audit.method.name` is set.

Each source is read from the log record first and from the resource second. On pod snapshots `k8s.node.name` is a record attribute and `k8s.cluster.name` is a resource attribute. Records on one resource can resolve to different nodes or kinds, and `host.name` and `service.name` are resource attributes, so the processor splits such a resource into one resource per distinct pair. Every copy keeps the original resource attributes, scope and schema URL.

Settings, all optional:

```yaml
identityfill:
  host_name:
    disabled: false
    sources: [k8s.node.name, k8s.cluster.name, gcp.label.cluster_name]
  service_name:
    disabled: false
    k8s_event: k8s-events
    k8s_pod_status: k8s-pod-status
    gcp_audit: gcp-audit
  audit_body:
    disabled: false
```

`sources` and the three names must not be empty while their rule is on. The counter metric is listed in `docs/otel-metrics.md`.

## Per-cluster values

| Cluster | `k8s.cluster.name` | Notes |
|---|---|---|
| kube-v0 (GCP) | `kube-v0` | Keeps the `googlecloud*` receivers and `resource_detection/gcp`. |
| apps-eks-v0 (AWS) | `apps-eks-v0` | Parity not yet checked against its values. |
| site-k8s | `site-k8s` | Parity not yet checked against its values. |

For apps-eks-v0 and site-k8s the fragment applies after their own `transform/k8s_objects` block is confirmed to match the kube-v0 block. Until then, parity is proven on kube-v0 only.

## Components

Every receiver, processor, exporter and extension in the stock `otel-cluster` config is in `builder-gateway.yaml` at v0.164.1. The extension prints as `google_cloud_logentry_encoding`; a config accepts both names.

The `tcp_check` receiver is in the gateway image from 0.3.8. It opens a TCP connection to each target on every collection interval and reports whether it connected, so a metrics pipeline can alert on a path that is down while the cluster looks healthy. It is alpha for metrics. `test/configs/gateway-tcpcheck.yaml` shows the settings and `just verify-gateway` loads it.

## Sentry attribution

On the gateway image, `otel-cluster` reports to Sentry as `otelcol-gateway@<VERSION>`, not as the stock contrib distribution.
