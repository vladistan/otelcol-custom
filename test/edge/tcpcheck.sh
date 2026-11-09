#!/usr/bin/env bash
# Runs the built edge collector with test/configs/edge-tcpcheck.yaml against local
# listeners and checks tcpcheck.status: 1 for an open port, 0 for a closed one,
# over IPv4 and over bracketed IPv6. The IPv6 cases are skipped, with a message,
# when the host has no ::1.
#
# Usage: tcpcheck.sh [EDGE_BINARY]
# Exit: 0 PASS, 1 a check failed, 2 usage error or missing tool.
set -euo pipefail
unset CDPATH

readonly PROGRAM="${0##*/}"
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)" || {
	echo "$PROGRAM: cannot resolve the script directory" >&2
	exit 2
}
readonly ROOT
readonly BINARY="${1:-$ROOT/build/edge/otelcol-edge}"
readonly CONFIG="$ROOT/test/configs/edge-tcpcheck.yaml"

die() {
	echo "$PROGRAM: $*" >&2
	exit 2
}

[ -x "$BINARY" ] || die "$BINARY is not an executable; build it with: just build-edge"
for tool in python3 jq; do
	command -v "$tool" >/dev/null || die "$tool is required"
done

work="$(mktemp -d)"
listener_pid=""
collector_pid=""
cleanup() {
	[ -z "$collector_pid" ] || kill "$collector_pid" 2>/dev/null || true
	[ -z "$listener_pid" ] || kill "$listener_pid" 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup EXIT

# The listener holds one open port per family and reports a closed port per family
# (bound, then released) as "name host:port" lines.
python3 -I - "$work/ports" >"$work/listener.log" 2>&1 <<'PY' &
import socket, sys, time

def bind(family, host):
    s = socket.socket(family, socket.SOCK_STREAM)
    s.bind((host, 0))
    return s

out = open(sys.argv[1], "w")
keep = []
for tag, family, host, fmt in (
    ("V4", socket.AF_INET, "127.0.0.1", "127.0.0.1:%d"),
    ("V6", socket.AF_INET6, "::1", "[::1]:%d"),
):
    try:
        opened = bind(family, host)
    except OSError:
        continue
    opened.listen(8)
    keep.append(opened)
    closed = bind(family, host)
    closed_port = closed.getsockname()[1]
    closed.close()
    out.write("TCP_%s_OPEN %s\n" % (tag, fmt % opened.getsockname()[1]))
    out.write("TCP_%s_CLOSED %s\n" % (tag, fmt % closed_port))
out.write("READY\n")
out.close()
while True:
    time.sleep(60)
PY
listener_pid=$!

for _ in $(seq 1 50); do
	grep -q '^READY$' "$work/ports" 2>/dev/null && break
	sleep 0.1
done
grep -q '^READY$' "$work/ports" 2>/dev/null || die "listener did not start: $(cat "$work/listener.log")"

have_v6=true
grep -q '^TCP_V6_OPEN ' "$work/ports" || have_v6=false
if [ "$have_v6" = false ]; then
	echo "SKIP: no ::1 on this host; IPv6 checks not run"
	# Unset targets fall back to documentation addresses, which are never dialed
	# successfully; the IPv6 assertions below are skipped.
fi

while read -r name value; do
	[ "$name" = READY ] || export "$name=$value"
done <"$work/ports"
export TCP_OUT="$work/metrics.json"

"$BINARY" validate --config "$CONFIG" || {
	echo "FAIL: the edge binary rejects $CONFIG"
	exit 1
}

"$BINARY" --config "$CONFIG" >"$work/collector.log" 2>&1 &
collector_pid=$!

# status of one endpoint in the latest export, or "absent"
status_of() {
	jq -rs --arg ep "$1" '
		[.[] | .resourceMetrics[]?.scopeMetrics[]?.metrics[]?
		 | select(.name == "tcpcheck.status") | .gauge.dataPoints[]?
		 | select(any(.attributes[]?; .key == "tcpcheck.endpoint" and .value.stringValue == $ep))
		 | (.asInt // .asDouble)] | last // "absent"' "$work/metrics.json" 2>/dev/null || echo absent
}

wait_for() {
	local endpoint="$1" want="$2" got="absent"
	for _ in $(seq 1 100); do
		got="$(status_of "$endpoint")"
		[ "$got" = "$want" ] && return 0
		kill -0 "$collector_pid" 2>/dev/null || break
		sleep 0.2
	done
	echo "FAIL: tcpcheck.status for $endpoint is $got, want $want"
	return 1
}

failures=0
check() {
	local var="$1" want="$2" endpoint
	endpoint="$(grep "^$var " "$work/ports" | cut -d' ' -f2)"
	if wait_for "$endpoint" "$want"; then
		echo "PASS: $var $endpoint status=$want"
	else
		failures=$((failures + 1))
	fi
}

check TCP_V4_OPEN 1
check TCP_V4_CLOSED 0
if [ "$have_v6" = true ]; then
	check TCP_V6_OPEN 1
	check TCP_V6_CLOSED 0
fi

for metric in tcpcheck.status tcpcheck.duration tcpcheck.error; do
	if grep -q "\"name\":\"$metric\"" "$work/metrics.json"; then
		echo "PASS: metric $metric present"
	else
		echo "FAIL: metric $metric missing"
		failures=$((failures + 1))
	fi
done

if [ "$failures" -ne 0 ]; then
	echo "collector log tail:"
	tail -n 20 "$work/collector.log"
	exit 1
fi
echo "PASS: edge tcp_check reports IPv4 and IPv6 targets"
