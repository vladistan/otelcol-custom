#!/usr/bin/env bash
# Runs the built edge collector with test/configs/edge-prometheus.yaml against a
# local exporter that serves Prometheus text, and checks that the scraped gauge
# arrives with its value, the job (service.name) and the instance
# (service.instance.id), and that the synthetic "up" metric is 1.
#
# Usage: prometheus.sh [EDGE_BINARY]
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
readonly CONFIG="$ROOT/test/configs/edge-prometheus.yaml"
readonly JOB="local-exporter"
readonly GAUGE="edge_probe_value"
readonly GAUGE_VALUE=42

die() {
	echo "$PROGRAM: $*" >&2
	exit 2
}

[ -x "$BINARY" ] || die "$BINARY is not an executable; build it with: just build-edge"
for tool in python3 jq; do
	command -v "$tool" >/dev/null || die "$tool is required"
done

work="$(mktemp -d)"
exporter_pid=""
collector_pid=""
cleanup() {
	[ -z "$collector_pid" ] || kill "$collector_pid" 2>/dev/null || true
	[ -z "$exporter_pid" ] || kill "$exporter_pid" 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup EXIT

# The exporter serves one gauge on an OS-chosen port and writes "READY host:port".
python3 -I - "$work/exporter" "$GAUGE" "$GAUGE_VALUE" >"$work/exporter.log" 2>&1 <<'PY' &
# cspell:ignore wfile
import http.server, sys

path, name, value = sys.argv[1], sys.argv[2], sys.argv[3]
body = ("# TYPE %s gauge\n%s %s\n" % (name, name, value)).encode()

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Type", "text/plain; version=0.0.4")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass

server = http.server.HTTPServer(("127.0.0.1", 0), Handler)
with open(path, "w") as out:
    out.write("READY 127.0.0.1:%d\n" % server.server_address[1])
server.serve_forever()
PY
exporter_pid=$!

for _ in $(seq 1 50); do
	grep -q '^READY ' "$work/exporter" 2>/dev/null && break
	sleep 0.1
done
grep -q '^READY ' "$work/exporter" 2>/dev/null || die "exporter did not start: $(cat "$work/exporter.log")"

target="$(cut -d' ' -f2 "$work/exporter")"
export PROM_TARGET="$target" PROM_JOB="$JOB" PROM_OUT="$work/metrics.json"

"$BINARY" validate --config "$CONFIG" || {
	echo "FAIL: the edge binary rejects $CONFIG"
	exit 1
}

"$BINARY" --config "$CONFIG" >"$work/collector.log" 2>&1 &
collector_pid=$!

# Print the latest value of metric $1 whose resource carries job and instance, or "absent"
read_metric_value() {
	jq -rs --arg name "$1" --arg job "$JOB" --arg instance "$target" '
		[.[] | .resourceMetrics[]?
		 | select(any(.resource.attributes[]?; .key == "service.name" and .value.stringValue == $job))
		 | select(any(.resource.attributes[]?; .key == "service.instance.id" and .value.stringValue == $instance))
		 | .scopeMetrics[]?.metrics[]? | select(.name == $name)
		 | .gauge.dataPoints[]? | (.asInt // .asDouble)] | last // "absent"' "$work/metrics.json" 2>/dev/null || echo absent
}

wait_for() {
	local name="$1" want="$2" got="absent"
	for _ in $(seq 1 100); do
		got="$(read_metric_value "$name")"
		[ "$got" = "$want" ] && return 0
		kill -0 "$collector_pid" 2>/dev/null || break
		sleep 0.2
	done
	echo "FAIL: $name for job=$JOB instance=$target is $got, want $want"
	return 1
}

failures=0
for pair in "$GAUGE $GAUGE_VALUE" "up 1"; do
	read -r name want <<<"$pair"
	if wait_for "$name" "$want"; then
		echo "PASS: $name=$want with job=$JOB instance=$target"
	else
		failures=$((failures + 1))
	fi
done

if [ "$failures" -ne 0 ]; then
	echo "collector log tail:"
	tail -n 20 "$work/collector.log"
	exit 1
fi
echo "PASS: edge prometheus receiver scrapes a local exporter"
