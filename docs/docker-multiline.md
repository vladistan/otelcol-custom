# Docker logs and metrics on journald-driver hosts

This page covers a Docker host whose containers log through the `journald` log
driver, collected by the edge build. It states what arrives in the backend, which
settings produce it, and what is still unproven. The edge build needs 0.3.14 or
later for all of it.

## What was checked

On a Docker host using the journald log driver (Docker 28), the 0.3.14 edge build and
the 0.3.14 gateway produced these results:

| Check | Result |
|-------|--------|
| A logged stack trace | Arrives as one record, with `container.name` on it |
| `service.name` of a container's log record | Equals the container name, not `docker.service` |
| A container that starts and exits with one line | Arrives as its own record, not merged into a daemon line |
| `docker_stats` container metrics | Memory limit for every container; 13 `container.*` metrics |
| Kernel-thread series in process metrics | One `kernel_threads` aggregate, no per-thread series |

## How a stack trace becomes one record

The journald driver writes each line of container output as a separate journal entry,
and the daemon is the process behind all of them. Two settings keep the lines of one
container together and apart from every other container.

- **`logrecombine` with `container_identifier`.** The processor joins lines that
  arrive within `gap_timeout` and do not start with a timestamp. It groups entries
  by `container_identifier`, default `body.CONTAINER_ID`, instead of by process id.
  Every container shares the daemon's process id, so grouping by it would mix
  containers. A daemon line has no `CONTAINER_ID`, so it is never joined to a
  container's lines. Set `container_identifier: ""` to group by process id as
  before.
- **A container that must not be joined.** A container that writes single-line logs
  can be left out of recombination. In the Ansible role this is the list
  `otel_logrecombine_exclude_containers`, which holds container names. The lines of
  a listed container pass through unchanged.

```yaml
processors:
  logrecombine:
    container_identifier: body.CONTAINER_ID   # default
```

## Container names on the log records

Container log records reach the collector with `service.name` set to
`docker.service`, the unit that runs the daemon. The `containername` processor sets
`service.name` to `container.name` when the resource has a non-empty `container.name`.
Three things decide whether it works:

1. **`container.name` must be on the resource before the processor runs.** The
   processor does not look at the journal fields. Put it after the stage that sets
   `container.name`; a resource without one is left as it was.
2. **A later rule that sets `service.name` overrides it.** A gateway rule such as
   `set(resource.attributes["service.name"], body["_SYSTEMD_UNIT"])` turns every
   container record back into `docker.service`. Remove such a rule or guard it so it
   only sets a value that is missing.
3. **The gateway `journald` processor keeps an existing value.** Before 0.3.14 its
   `set_resource` option replaced `service.name` with the journal unit. From 0.3.14
   it sets an attribute only when the resource has no value for it, so the container
   name survives the gateway.

Dashboards and alerts that match `service.name=docker.service` stop matching
container records once this is on.

## What stays `dockerd`

`process.command` and `process.pid` on a journald-driver log record describe the
journal entry's writer, which is the Docker daemon. `process.command=dockerd` is
accurate for these records and stays. Use `container.name` or `service.name` to tell
containers apart.

## Container metrics with `docker_stats`

The Linux edge builds from 0.3.14 include `docker_stats`. It reads the Docker socket,
so the log driver does not matter. Keep `api_version` empty:

```yaml
receivers:
  docker_stats:
    endpoint: unix:///var/run/docker.sock
    api_version: ""
    collection_interval: 60s
    metrics:
      container.cpu.limit:
        enabled: true
```

- With the default version, `1.44`, a daemon whose API stops below 1.44 rejects the
  request. Docker 24 answers up to 1.43. Against Docker 24.0.9 the default fails with
  `client version 1.44 is too new. Maximum supported API version is 1.43`. The
  collector stays up, logs the WARN described below, retries the start, and exports
  no container metrics.
- An empty `api_version` lets the Docker client negotiate with the daemon. It was run
  against Docker 24.0.9 (API 1.43), 28 and 29. On Docker 24.0.9 it starts and exports
  the same container metrics, limits included, as a pinned `api_version: "1.43"`.
- `container.cpu.limit` appears only for containers that have a CPU limit.
  `container.memory.usage.limit` appears for every container; a value near the host
  total means no limit.
- `docker_stats` is an optional receiver. If it cannot start, for example because
  Docker is not running yet, the collector logs a WARN, counts it in
  `otelcol_optional_receiver_skipped`, keeps running, and retries the start after
  30 s, doubling up to every 5 minutes. When Docker comes up, the receiver starts
  without a collector restart. See the README section on optional receivers.

## Open items

- Hosts that use the `json-file` driver were not checked; recombination there depends
  on the log source, not on this page.
