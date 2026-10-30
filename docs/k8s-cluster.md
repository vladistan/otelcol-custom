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

Everything else in the values stays: receivers, `resource/k8s`, `k8sattributes`, `memory_limiter`, `batch`, exporters and extensions.

## k8sevent output

`k8sevent` with `ottl_parity: true` sets the same fields as the OTTL block on Event and Pod records: `k8s.object.kind`, `k8s.object.action`, `k8s.namespace`, `k8s.name`, the `k8s.event.*` and `k8s.pod.phase` fields, `k8s.container.state` and `k8s.container.exit_code`, the `k8s.labels.*` fields, `message_type`, `message` and `k8s.object.full`. The body equals `message`. Severity is WARN or INFO for Events and INFO for Pods.

`k8sevent` also keeps its own attributes (`k8s.pod.name`, `k8s.pod.restarts`, `k8s.pod.ready`, `k8s.node.name`, `k8s.event.source`). Other object kinds pass unchanged. The cluster name is never set by the processor.

`test/bench/k8sevent_parity_test.go` runs the OTTL block from the kube-v0 values and the processor on the same records and compares every field the block sets.

## Per-cluster values

| Cluster | `k8s.cluster.name` | Notes |
|---|---|---|
| kube-v0 (GCP) | `kube-v0` | Keeps the `googlecloud*` receivers and `resourcedetection/gcp`. |
| apps-eks-v0 (AWS) | `apps-eks-v0` | Parity not yet checked against its values. |
| site-k8s | `site-k8s` | Parity not yet checked against its values. |

For apps-eks-v0 and site-k8s the fragment applies after their own `transform/k8s_objects` block is confirmed to match the kube-v0 block. Until then, parity is proven on kube-v0 only.

## Components

Every receiver, processor, exporter and extension in the stock `otel-cluster` config is in `builder-gateway.yaml` at v0.154.0. The extension prints as `google_cloud_logentry_encoding`; a config accepts both names.

## Sentry attribution

On the gateway image, `otel-cluster` reports to Sentry as `otelcol-gateway@<VERSION>`, not as the stock contrib distribution.
