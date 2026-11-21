# Pod discovery on the Linux edge and the gateway

The Linux edge and the static Linux edge can start receivers for the pods of their own node. It is for a DaemonSet: each collector lists the pods on its node and starts a receiver for every pod that a rule matches. The gateway carries the same components for a Deployment of one replica that watches the pods of the whole cluster (see [The gateway](#the-gateway)). The scrape collector and the Windows edge do not carry them.

| Component | Type | Role |
|---|---|---|
| `k8s_observer` | extension | Lists pods and reports them as endpoints: the pods of one node on the edge, of the cluster on the gateway. |
| `receiver_creator` | receiver | Starts a receiver for each endpoint that one of its rules matches, and stops it when the endpoint goes. |

Both are contrib components at v0.164.1. The reference configs are `test/configs/edge-k8s-discovery.yaml` (scrape annotated pods) and `test/configs/edge-k8s-apps.yaml` (one rule for each application receiver) for the edge, and `test/configs/gateway-k8s-discovery.yaml` (one Redis rule) and `test/configs/gateway-k8s-apps.yaml` (one rule for each application receiver) for the gateway.

## What is compiled in

| Receiver | Type | Reads |
|---|---|---|
| `prometheus` | receiver | Metrics a pod serves in the Prometheus text format. |
| `redis` | receiver | Redis `INFO`. |
| `postgresql` | receiver | PostgreSQL statistics. |
| `mysql` | receiver | MySQL status and InnoDB statistics. |
| `mongodb` | receiver | MongoDB server status. |
| `rabbitmq` | receiver | The RabbitMQ management API. |
| `nginx` | receiver | The nginx `stub_status` page. |

The edge and the gateway compile in all seven. `memcached` and `kafka_metrics` are not compiled in. `prometheus_simple` is not either: pods are scraped with the full `prometheus` receiver and a templated scrape config. A rule that names a type the build lacks logs `unable to lookup factory` at Error when a pod matches, and the collector keeps running.

## Rules

A rule starts a receiver when its expression is true for an endpoint. One rule starts the `prometheus` receiver for pods that carry the scrape annotation:

```yaml
receivers:
  receiver_creator/pods:
    watch_observers: [k8s_observer]
    receivers:
      prometheus:
        rule: type == "pod" && annotations["prometheus.io/scrape"] == "true" && labels["app.kubernetes.io/component"] != "opentelemetry-collector"
        config:
          config:
            scrape_configs:
              - job_name: pod
                scrape_interval: 60s
                metrics_path: '`"prometheus.io/path" in annotations ? annotations["prometheus.io/path"] : "/metrics"`'
                static_configs:
                  - targets: ['`joinHostPort(endpoint, "prometheus.io/port" in annotations ? annotations["prometheus.io/port"] : "9090")`']
```

`watch_observers` names the observer, and the observer must be listed in `service.extensions`. The rule leaves out the collector's own pod through the label the DaemonSet puts on it. A guard (`just check-k8s-discovery-config`) fails a sample config where `receiver_creator` has no `watch_observers`, where the observer is missing from `service.extensions`, or where a `discovery` block turns on annotation discovery.

Rule variables for a `pod` endpoint are `name`, `namespace`, `uid`, `labels` and `annotations`. A `port` endpoint adds `port`, `name`, `protocol` and the pod's values as `pod.labels`, `pod.name` and so on.

### Application receivers

`edge-k8s-apps.yaml` holds one rule for each application receiver. A rule matches a `port` endpoint by its number and by the pod label `app.kubernetes.io/name`:

```yaml
redis:
  rule: type == "port" && port == 6379 && pod.labels["app.kubernetes.io/name"] == "redis"
  config:
    endpoint: '`endpoint`'
    collection_interval: 60s
    password: ${env:REDIS_PASSWORD}
```

The five other rules follow the same shape in `test/configs/edge-k8s-apps.yaml`, each with the port in the table below and the endpoint expression the receiver needs (`http://` and `/status` around it for `nginx`, `http://` for `rabbitmq`, a `hosts` list for `mongodb`).

Credentials never go in the file. Give them to the DaemonSet as environment variables from a Secret, or reference a mounted Secret file with `${file:/path}`. The collector replaces `${env:...}` when it loads the config; the backtick expressions are replaced for each endpoint.

The receivers differ in what they need:

| Receiver | Port | Needs |
|---|---|---|
| `redis` | 6379 | A password if the server sets one. |
| `postgresql` | 5432 | A user with `pg_monitor`; `tls.insecure: true` in the sample, to match a server without TLS. |
| `mysql` | 3306 | A user with `PROCESS` and `REPLICATION CLIENT`. |
| `mongodb` | 27017 | A monitoring user; `tls.insecure: true` in the sample. |
| `rabbitmq` | 15672 | The management plugin and a monitoring user. |
| `nginx` | 80 or 8080 | `stub_status` enabled at `/status`. |

## Pod labels on the metrics

The creator puts `k8s.pod.uid`, `k8s.pod.name` and `k8s.namespace.name` on the resource of everything a started receiver produces. Pod labels and annotations are not added. Run `k8s_attributes` after the creator, associated on `k8s.pod.uid`, to add them:

```yaml
processors:
  k8s_attributes:
    pod_association:
      - sources:
          - from: resource_attribute
            name: k8s.pod.uid
```

## Settings the DaemonSet needs

- `K8S_NODE_NAME` from the downward API (`spec.nodeName`). The observer config sets `node: ${env:K8S_NODE_NAME}`, so it lists only the pods of that node. Without it every collector would list every pod in the cluster and scrape them all.
- A service account that can `get`, `list` and `watch` pods:

```yaml
rules:
  - apiGroups: [""]
    resources: [pods]
    verbs: [get, list, watch]
```

The `k8s_attributes` block of the edge reference configs extracts the pod, namespace and node names and a pod label, all read from the pod itself, and its `node_from_env_var` filter is a field selector on pods, so it needs no more than the pod grant. Extra rules widen the grant: labels or annotations of a namespace (or the cluster uid) need `namespaces`, labels or annotations of a node (or `k8s.node.uid`) need `nodes`, and rules that read a ReplicaSet, Deployment, StatefulSet, DaemonSet, Job or CronJob need that resource, each with `get`, `list` and `watch`. With `namespaces: [a, b]` in the observer config, a namespaced `Role` and `RoleBinding` is enough for the observer.

## The gateway

The gateway image carries `k8s_observer`, `receiver_creator` and the six application receivers, so a gateway Deployment can scrape the pods of a cluster itself. The edge rules above apply; these points differ:

- **One replica, the whole cluster.** The reference configs (`gateway-k8s-discovery.yaml`, `gateway-k8s-apps.yaml`) set `observe_pods: true` as the only `observe_*` key and no `node` key, so the observer lists the pods of every namespace. A second replica would list the same pods and scrape each one twice, so the Deployment keeps one replica. The edge DaemonSet keeps the local-node limit: its observer sets `node: ${env:K8S_NODE_NAME}`, and a DaemonSet that scraped cluster-wide would scrape every pod once for each node.
- **Reference rules.** `gateway-k8s-discovery.yaml` has one rule, for Redis on port 6379. It leaves out the collector's own pod through the `app.kubernetes.io/component` label and matches any other pod that declares the port, so narrow it with a label or namespace test before pointing it at a cluster. `gateway-k8s-apps.yaml` has one rule for each of the six receivers, each matched by the pod label `app.kubernetes.io/name`.
- **Credentials.** Redis reads `${env:REDIS_PASSWORD}`; leave the variable unset for a server without a password. The other five read their user and password from environment variables too. Nothing is written in the config.
- **Annotation discovery stays off**, as on the edge, and the same guard checks the gateway configs.

### RBAC

The observer needs `get`, `list` and `watch` on pods across the cluster. The `k8s_attributes` block of the reference configs extracts only the pod, namespace and node names, which come from the pod itself, so it needs the same pod grant and nothing more. Granting it is the cluster owner's job; the gateway image does not create it. A cluster role for the reference configs:

```yaml
rules:
  - apiGroups: [""]
    resources: [pods]
    verbs: [get, list, watch]
```

Bind it to the service account of the gateway Deployment with a `ClusterRoleBinding`. Extra extraction rules widen the grant: labels or annotations of a namespace (or the cluster uid) need `namespaces`, labels or annotations of a node (or `k8s.node.uid`) need `nodes`, and rules that read a ReplicaSet, Deployment, StatefulSet, DaemonSet, Job or CronJob need that resource, each with `get`, `list` and `watch`. With `namespaces: [a, b]` in the observer config, a namespaced `Role` on pods in those namespaces is enough for the observer.

### Failures on the gateway

When the service account cannot list pods, the observer logs the informer's forbidden errors and retries, repeatedly. The gateway adds no warning of its own and no counter for this: the lines are the ones the upstream informer writes. No endpoint arises, no receiver starts, and the gateway keeps running. A Redis target that refuses the connection or needs a password the template does not supply makes the started receiver log a scrape error at each interval, and the gateway keeps running.

## Which pods make endpoints

Only Running pods make endpoints. A pod yields one `pod` endpoint at its IP, one `pod.container` endpoint for each running container, and one `port` endpoint for each container port the pod spec declares. A port the spec does not declare gives no endpoint, so an application rule never fires for it. A pod that is not Running, or has no IP yet, gives nothing.

The observer compares the endpoint set every second. A change to a pod restarts the receivers started for it.

## What goes wrong

| Case | Result | Stops the collector? |
|---|---|---|
| A `rule` that does not parse | Config load error | Yes, at start |
| `watch_observers` names an extension that is not configured | `receiver_creator` fails to start | Yes |
| `k8s_observer` cannot build a client (`serviceAccount` outside a cluster) | Extension fails to start | Yes |
| `k8s_observer` with every `observe_*` set to false (`observe_pods` is true by default) | Config validation error | Yes |
| RBAC missing for pods | The informer logs forbidden errors and retries; no endpoints | No |
| A template whose receiver config is invalid | One Error in the log when a matching pod appears; not found by `validate` | No |
| A template whose receiver fails to start | `failed to start receiver` at Error; no retry until the endpoint changes | No |
| The target refuses the connection or rejects the login (wrong password, no listener) | The receiver starts; one scrape error is logged each interval | No |
| A receiver type the build lacks | `unable to lookup factory` at Error | No |
| A rule that fails to evaluate | Logged; the next rule runs | No |
| A pod that is not Running, or a port the spec does not declare | No endpoint | No |

A receiver started by the creator fails soft: its failure is logged and the collector keeps running. The six application receivers connect lazily: they start even when the target refuses connections or rejects the credentials, and each interval logs one scrape error until the target answers. Bad credentials therefore show as scrape errors, not as a failed start, and the repository tests all six against a refused port. A typo in a template shows only when a matching pod appears, so the repository tests every template in `test/configs/edge-k8s-apps.yaml` against its receiver's config, and runs the Prometheus template through a fake observer.

## Annotation discovery stays off

`receiver_creator` can also start receivers from pod annotations (`discovery.enabled: true`). The reference configs leave it off, and the guard fails a sample config that turns it on. With it on, anyone who can edit a pod annotation can start any compiled-in receiver and point its config at any address the collector can reach. An invalid annotation also stops the other pods found in the same second from getting a receiver. Rules in the config start receivers; annotations only select pods for the rule.

## Cardinality

- Every pod a rule matches gets its own receiver, and its own scrape manager for `prometheus`. The count per node is the number of matching pods, so keep rules narrow.
- The generated id of a started receiver carries the endpoint and the pod uid. The receiver self-metrics (`otelcol_receiver_*`) and logs therefore gain one `receiver` label value for each pod, and a new one after each pod restart.
- A recreated pod gets a new `k8s.pod.uid`, and a new `k8s.pod.name` unless a StatefulSet owns it: the old series stop and new ones start. A container restart keeps both. Do not use the pod name as a metric label.
- Each scraped target adds the Prometheus automatic series (`up`, `scrape_duration_seconds`, `scrape_samples_scraped` and others).

## Binary size

Cost on the static Linux edge, each step added to the one before. Sizes are bytes of the `otelcol-edge` binary built from OCB v0.164.0 sources.

| Step | Size | Added |
|---|---|---|
| The static edge without these components | 173,678,752 | |
| `k8s_observer` and `receiver_creator` | 173,920,416 | 241,664 |
| `redis` | 182,407,328 | 8,486,912 |
| `postgresql` | 184,189,088 | 1,781,760 |
| `mysql` | 185,458,848 | 1,269,760 |
| `mongodb` | 189,444,256 | 3,985,408 |
| `rabbitmq` | 189,931,680 | 487,424 |
| `nginx` | 189,980,832 | 49,152 |

The eight together add 16,302,080 bytes (9.4%) in this series of builds. The release container build of the same manifests gives 178,106,530 bytes without them and 194,687,138 with all eight, which is 16,580,608 bytes more (9.3%).

The first rows of the table come from a host Go toolchain build (`CGO_ENABLED=0`, `-s -w`, linux/amd64), so they compare with each other and not byte for byte with the release container figures. The order matters: `redis` is the first receiver to bring client libraries for managed services (its cloud authentication), and the receivers after it share them, so each of those costs less than it would alone. The observer and the creator stay under the 0.5 MB limit on their own (`client-go` was already in through `k8s_attributes`).

### Gateway

Cost on the gateway, each step added to the one before. Sizes are bytes of the `otelcol-gateway` binary (`CGO_ENABLED=0`, `-trimpath`, `-s -w`, linux/amd64, host Go toolchain) built from OCB v0.164.0 sources with the repository entrypoint, the way `just build-gateway-linux` builds it.

| Step | Size | Added |
|---|---|---|
| The gateway without these components | 217,968,802 | |
| `k8s_observer` and `receiver_creator` | 218,214,562 | 245,760 |
| `redis` | 226,742,434 | 8,527,872 |
| `postgresql` | 228,368,546 | 1,626,112 |
| `mysql` | 229,671,074 | 1,302,528 |
| `mongodb` | 233,775,266 | 4,104,192 |
| `rabbitmq` | 234,299,554 | 524,288 |
| `nginx` | 234,348,706 | 49,152 |

The eight together add 16,379,904 bytes (7.5%). The costs follow the edge series: `redis` is the first receiver to bring client libraries for managed services, and the receivers after it share them. The observer and the creator add 245,760 bytes on their own.

The release container build (`Dockerfile.gateway`, linux/amd64, the builder stage compiles the binary) gives the same two binary sizes, 217,968,802 and 234,348,706 bytes. The image grows with the binary: its layers add up to 229,801,984 bytes without the components and 246,181,888 with them (`docker history --human=false`, the sum of the sizes, base image included), 16,379,904 bytes more. The compressed image, as `docker save` writes it, is 60,057,600 bytes without them and 64,802,816 with them, 4,745,216 bytes more. `docker image inspect --format '{{.Size}}'` is not used: with the containerd image store its total exceeds the layer sum by about the size of the compressed image (60,040,430 and 64,785,983 bytes).
