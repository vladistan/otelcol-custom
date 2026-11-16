# FreeBSD edge

The release carries one FreeBSD binary, `otelcol-edge-VERSION-freebsd-amd64`: a static
`freebsd/amd64` executable built with CGO off. Release checks read its checksum, build
settings and ELF header; they do not start it. Run `otelcol-edge validate` on the host
before the first start.

## Install

| Item | Path |
|---|---|
| Binary | `/usr/local/bin/otelcol-edge` |
| Config | `/usr/local/etc/otelcol-edge/config.yaml` |
| Storage | `/var/db/otelcol-edge/storage` |
| rc.d script | `/usr/local/etc/rc.d/otelcol_edge` (service `otelcol_edge`) |

```sh
fetch https://<download-host>/otelcol-edge-VERSION-freebsd-amd64
sha256 otelcol-edge-VERSION-freebsd-amd64   # compare with SHA256SUMS
install -m 0755 otelcol-edge-VERSION-freebsd-amd64 /usr/local/bin/otelcol-edge
```

The service script runs the binary in the foreground under `daemon(8)`:

```sh
#!/bin/sh
# PROVIDE: otelcol_edge
# REQUIRE: NETWORKING
# KEYWORD: shutdown

. /etc/rc.subr

name="otelcol_edge"
rcvar="otelcol_edge_enable"
pidfile="/var/run/${name}.pid"
otelcol_edge_config="/usr/local/etc/otelcol-edge/config.yaml"
command="/usr/sbin/daemon"
command_args="-P ${pidfile} -o /var/log/otelcol-edge.log /usr/local/bin/otelcol-edge --config=${otelcol_edge_config}"

load_rc_config $name
: ${otelcol_edge_enable:="NO"}
run_rc_command "$1"
```

Enable it with `sysrc otelcol_edge_enable=YES`, then `service otelcol_edge start`.

## What runs

The FreeBSD build is the static Linux edge without five components:

| Component | On FreeBSD |
|---|---|
| `journald` | Not in the build. Read `/var/log/messages` with `filelog`. |
| `psi` | Not in the build. FreeBSD has no pressure stall files. |
| `ethtool` | Not in the build. It reads Linux ioctls. |
| `docker_stats` | Not in the build. There is no Docker socket on the host. |
| `k8s_attributes` | Not in the build. |
| `pve`, `snmp`, `netflow`, `iscsi` | Not in the build. They are Linux edge receivers. |
| `k8s_observer`, `receiver_creator` and the six application receivers | Not in the build. |
| `hwmon` | In the build. It reads CPU and ACPI thermal-zone temperatures through sysctl; there are no fan or voltage metrics on FreeBSD. |

Everything else in the static edge is in the build. `hostmetrics` works with these notes:

- `system.network.connections` needs `lsof`; disable it.
- Exclude the CAM devices `pass0` to `pass5` from `disk`, the types `devfs`, `fdescfs`, `nullfs` and `tmpfs` from `filesystem`, and `pflog0*` from `network`.
- The `process` scraper calls `ps`; leave it off on a busy host.

`zfskstat` reads ZFS through sysctl when `source` is `sysctl` (the default on FreeBSD). It
reports `zfs.arc.*` and `zfs.pool.state` with the Linux names. FreeBSD 13.1 has no per-pool
I/O kstat, so `zfs.pool.io.*` is absent; use the `hostmetrics` `disk` scraper for disk I/O.
Set `datasets: true` for per-dataset I/O.

`test/configs/truenas-core-edge.yaml` is a complete sample for a TrueNAS CORE host.
