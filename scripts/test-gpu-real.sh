#!/usr/bin/env bash
#
# Real-GPU verification of the gpu receiver, run natively on the host (no
# nvidia container runtime needed). Checks the NVML swap case and scrape
# timing, runs the edge binary with and without the gpu receiver against a
# gateway container, and writes build/gpu-real/REPORT.md.
#
# Usage:
#   test-gpu-real.sh [--minutes N] [--binary PATH] [--gateway-image IMAGE]
#   test-gpu-real.sh --self-test
#
# Environment: NVIDIA_SMI (default nvidia-smi), DOCKER (default docker; e.g.
# "sudo -n docker").
set -euo pipefail

readonly PROGRAM="${0##*/}"
readonly NVIDIA_SMI="${NVIDIA_SMI:-nvidia-smi}"
readonly DOCKER="${DOCKER:-docker}"
readonly ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly OUT="$ROOT/build/gpu-real"
readonly SCRAPE_LIMIT_MS=2500
readonly CPU_LIMIT_CORES=0.01
readonly EXPECTED_METRICS="hw.gpu.utilization hw.gpu.memory.usage hw.gpu.memory.limit
hw.gpu.memory.utilization hw.gpu.io hw.errors hw.status hw.temperature hw.power
hw.gpu.clock hw.gpu.power.limit hw.gpu.pcie.throughput"

minutes=10
binary="$ROOT/build/edge/otelcol-edge"
gateway_image="ghcr.io/vladistan/otelcol-gateway:v0.1.64"
gateway_name="gpu-real-gateway-$$"
edge_pid=""
gpu_run_start=1

die() {
	echo "$PROGRAM: $*" >&2
	exit 1
}

usage() {
	sed -n '/^# Usage:/,/^#$/p' "$0" | sed 's/^# \{0,1\}//' >&2
	exit 2
}

cleanup() {
	[ -z "$edge_pid" ] || kill "$edge_pid" 2>/dev/null || true
	# shellcheck disable=SC2086  # DOCKER may carry a sudo prefix
	$DOCKER rm -f "$gateway_name" >/dev/null 2>&1 || true
}

# Fails before starting anything when the NVIDIA driver is unusable.
require_driver() {
	command -v "$NVIDIA_SMI" >/dev/null 2>&1 || die "no NVIDIA driver: $NVIDIA_SMI not found"
	"$NVIDIA_SMI" -L >/dev/null 2>&1 || die "no NVIDIA driver: $NVIDIA_SMI -L failed"
}

smi() {
	"$NVIDIA_SMI" --query-gpu="$1" --format=csv,noheader,nounits
}

# Utime+stime of a process, in clock ticks.
cpu_ticks() {
	awk '{print $14 + $15}' "/proc/$1/stat"
}

# Runs the edge binary against the gateway for N minutes; prints the average
# CPU use in cores.
run_edge() {
	local config="$1" seconds=$((minutes * 60)) before after
	OTLP_ENDPOINT=127.0.0.1:4317 "$binary" --config "$config" >"$OUT/edge.log" 2>&1 &
	edge_pid=$!
	sleep 15
	before=$(cpu_ticks "$edge_pid")
	sleep "$seconds"
	after=$(cpu_ticks "$edge_pid")
	kill "$edge_pid"
	wait "$edge_pid" 2>/dev/null || true
	edge_pid=""
	awk -v b="$before" -v a="$after" -v s="$seconds" -v hz="$(getconf CLK_TCK)" \
		'BEGIN { printf "%.5f", (a - b) / hz / s }'
}

# Prints "name last-value" for each metric name in the gateway file.
last_values() {
	jq -r '.resourceMetrics[].scopeMetrics[].metrics[]
		| . as $m
		| ((.gauge // .sum).dataPoints // [])
		| select(length > 0) | last
		| "\($m.name) \(.asDouble // (.asInt | tonumber))"' \
		<(tail -n "+$gpu_run_start" "$OUT/metrics.json") 2>/dev/null |
		awk '{v[$1] = $2} END {for (k in v) print k, v[k]}' | sort
}

self_test() {
	local out rc=0
	out=$(env NVIDIA_SMI=/nonexistent/nvidia-smi "$0" 2>&1) || rc=$?
	[ "$rc" -ne 0 ] || die "self-test: expected non-zero exit without a driver"
	case "$out" in
	*"no NVIDIA driver"*) echo "PASS: exits non-zero with 'no NVIDIA driver'" ;;
	*) die "self-test: message missing 'no NVIDIA driver': $out" ;;
	esac
}

main() {
	while [ $# -gt 0 ]; do
		case "$1" in
		--minutes) minutes="${2:?}"; shift 2 ;;
		--binary) binary="${2:?}"; shift 2 ;;
		--gateway-image) gateway_image="${2:?}"; shift 2 ;;
		--self-test) self_test; exit 0 ;;
		*) usage ;;
		esac
	done

	require_driver
	[ -x "$binary" ] || die "edge binary missing: $binary (run just build-edge-linux)"
	command -v jq >/dev/null || die "jq is required"
	trap cleanup EXIT

	rm -rf "$OUT"
	mkdir -p "$OUT"
	chmod 777 "$OUT"

	local driver gpu_count ecc
	driver=$(smi driver_version | head -1)
	gpu_count=$(smi index | wc -l)
	ecc=$(smi ecc.mode.current | sort -u | paste -sd, -)

	echo "== swap suite and scrape timing (real NVML) =="
	local test_log="$OUT/go-test.log"
	(cd "$ROOT/receiver/gpureceiver" &&
		CGO_ENABLED=1 go test -count=1 -v -run 'TestSwapSuite/nvml|TestRealScrapeTiming' ./internal/collect/) |
		tee "$test_log"
	grep -q -- '--- PASS: TestSwapSuite/nvml ' "$test_log" || die "swap suite real case did not pass (skipped?)"
	grep -q -- '--- PASS: TestRealScrapeTiming ' "$test_log" || die "scrape timing test did not pass (skipped?)"
	local scrape_first scrape_max
	scrape_first=$(sed -n 's/.*scrape_first_ms=\([0-9]*\).*/\1/p' "$test_log")
	scrape_max=$(sed -n 's/.*scrape_max_ms=\([0-9]*\).*/\1/p' "$test_log")

	echo "== gateway =="
	# shellcheck disable=SC2086
	$DOCKER run -d --rm --name "$gateway_name" -p 127.0.0.1:4317:4317 \
		-v "$ROOT/test/configs/gpu-gateway.yaml:/etc/otelcol/config.yaml:ro" \
		-v "$OUT:/out" "$gateway_image" --config /etc/otelcol/config.yaml >/dev/null
	sleep 5

	# The health endpoint moves to a free port: 13133 may be taken on the host.
	sed -e 's/0\.0\.0\.0:13133/127.0.0.1:0/' \
		"$ROOT/test/configs/gpu-edge.yaml" >"$OUT/edge-gpu.yaml"
	sed -e '/^  gpu:/,/^$/d' -e '/metrics\/gpu:/,/exporters: \[otlp\]/d' \
		"$OUT/edge-gpu.yaml" >"$OUT/edge-no-gpu.yaml"

	echo "== edge without gpu, $minutes min =="
	local cpu_without cpu_with
	cpu_without=$(run_edge "$OUT/edge-no-gpu.yaml")
	# The gateway container owns metrics.json, so mark where the gpu run starts.
	gpu_run_start=$(($(wc -l <"$OUT/metrics.json") + 1))
	echo "== edge with gpu, $minutes min =="
	cpu_with=$(run_edge "$OUT/edge-gpu.yaml")
	sleep 10

	local cpu_delta
	cpu_delta=$(awk -v a="$cpu_with" -v b="$cpu_without" 'BEGIN { printf "%.5f", a - b }')

	last_values >"$OUT/last-values.txt"
	local missing=""
	for m in $EXPECTED_METRICS; do
		grep -q "^$m " "$OUT/last-values.txt" || missing="$missing $m"
	done

	{
		echo "# gpu-real report"
		echo
		echo "mode: native (host binary, gateway in docker)"
		echo "driver: $driver"
		echo "gpu_count: $gpu_count"
		echo "gpu_model: $(smi name | sort -u | paste -sd, -)"
		echo "ecc: $ecc"
		echo "scrape_first_ms: $scrape_first"
		echo "scrape_max_ms: $scrape_max (limit $SCRAPE_LIMIT_MS)"
		echo "cpu_with_gpu_cores: $cpu_with"
		echo "cpu_without_gpu_cores: $cpu_without"
		echo "cpu_delta: $cpu_delta (limit $CPU_LIMIT_CORES)"
		echo "metrics_missing:${missing:- none}"
		echo
		echo "## last scraped values at the gateway"
		sed 's/^/- /' "$OUT/last-values.txt"
		echo
		echo "## nvidia-smi at the end of the run"
		"$NVIDIA_SMI" --query-gpu=index,utilization.gpu,memory.used,memory.total,power.draw,power.limit,temperature.gpu,clocks.sm,clocks.mem \
			--format=csv | sed 's/^/- /'
	} >"$OUT/REPORT.md"
	cat "$OUT/REPORT.md"

	local status=0
	[ "$scrape_max" -le "$SCRAPE_LIMIT_MS" ] || { echo "FAIL: scrape_max over limit" >&2; status=1; }
	awk -v d="$cpu_delta" -v l="$CPU_LIMIT_CORES" 'BEGIN { exit !(d <= l) }' ||
		{ echo "FAIL: cpu_delta over limit" >&2; status=1; }
	[ -z "$missing" ] || { echo "FAIL: metrics missing:$missing" >&2; status=1; }
	exit "$status"
}

main "$@"
