#!/usr/bin/env bash
#
# Prints host, glibc version and musl yes/no for each edge host, over SSH.
# localhost runs locally. Input for the glibc 2.35 floor check (PEND-002).
#
# Usage:
#   audit-edge-hosts.sh HOST [HOST...]
set -euo pipefail

readonly PROGRAM="${0##*/}"

[ $# -gt 0 ] || { echo "usage: $PROGRAM HOST [HOST...]" >&2; exit 2; }

# Runs on the target host; prints "glibc=<version> musl=<yes|no>".
probe() {
	local banner glibc=none musl=no
	# musl's ldd exits 1 on --version, so only the banner decides.
	banner=$(ldd --version 2>&1 | head -1) || true
	case "$banner" in
	*"GNU libc"* | *GLIBC*) glibc=${banner##* } ;;
	esac
	if /bin/ls /lib/ld-musl-* >/dev/null 2>&1; then
		musl=yes
	fi
	echo "glibc=$glibc musl=$musl"
}

status=0
for host in "$@"; do
	case "$host" in
	localhost | 127.0.0.1)
		result=$(probe)
		;;
	*)
		if ! result=$(ssh -o BatchMode=yes -o ConnectTimeout=10 "$host" \
			"$(declare -f probe); probe"); then
			echo "$host unreachable" >&2
			status=1
			continue
		fi
		;;
	esac
	echo "host=$host $result"
done
exit "$status"
