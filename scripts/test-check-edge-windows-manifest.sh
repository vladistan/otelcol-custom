#!/usr/bin/env bash
# Tests check-edge-windows-manifest.sh on the committed manifests and on
# scratch copies with one component added or removed. No network.
# Usage: test-check-edge-windows-manifest.sh
# Exit: 0 all pass, 1 a case failed.
set -euo pipefail
unset CDPATH

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly ROOT
readonly CHECK="$ROOT/scripts/check-edge-windows-manifest.sh"
readonly WINDOWS="$ROOT/builder-edge-windows.yaml"
readonly STATIC="$ROOT/builder-edge-static.yaml"
SCRATCH="$(mktemp -d)"
readonly SCRATCH
trap 'rm -rf "$SCRATCH"' EXIT

failures=0

# expect_exit_code NAME WANT_CODE CMD... : runs CMD, compares its exit code.
# The command output stays in $SCRATCH/out for expect_output.
expect_exit_code() {
	local name="$1" want="$2" got=0
	shift 2
	"$@" >"$SCRATCH/out" 2>&1 || got=$?
	if [ "$got" -eq "$want" ]; then
		echo "PASS: $name"
	else
		echo "FAIL: $name (want exit code $want, got $got)"
		sed 's/^/    /' "$SCRATCH/out"
		failures=$((failures + 1))
	fi
}

# expect_output NAME PATTERN : the last command output contains PATTERN.
expect_output() {
	if grep -q -- "$2" "$SCRATCH/out"; then
		echo "PASS: $1"
	else
		echo "FAIL: $1 (no match for $2)"
		sed 's/^/    /' "$SCRATCH/out"
		failures=$((failures + 1))
	fi
}

expect_exit_code "the committed manifests pass" 0 "$CHECK"
expect_output "success output says PASS" "^PASS"

cp "$WINDOWS" "$SCRATCH/extra.yaml"
sed -i.bak '/^exporters:/a\
  - gomod: go.opentelemetry.io/collector/exporter/nopexporter v0.164.0' "$SCRATCH/extra.yaml"
expect_exit_code "an unlisted added component fails" 1 "$CHECK" "$SCRATCH/extra.yaml" "$STATIC"
expect_output "the added component is named" "nopexporter"

grep -v "windowseventlogreceiver" "$WINDOWS" >"$SCRATCH/missing.yaml"
expect_exit_code "a missing expected component fails" 1 "$CHECK" "$SCRATCH/missing.yaml" "$STATIC"
expect_output "the missing component is named" "windowseventlogreceiver"

grep -v "gpureceiver" "$WINDOWS" >"$SCRATCH/no-gpu.yaml"
expect_exit_code "a Windows manifest without gpureceiver fails" 1 "$CHECK" "$SCRATCH/no-gpu.yaml" "$STATIC"
expect_output "the missing gpureceiver is named" "expected addition is missing: .*gpureceiver"

grep -v "fswatchreceiver" "$WINDOWS" >"$SCRATCH/no-fswatch.yaml"
expect_exit_code "a Windows manifest without fswatchreceiver fails" 1 "$CHECK" "$SCRATCH/no-fswatch.yaml" "$STATIC"
expect_output "the missing fswatchreceiver is named" "expected addition is missing: .*fswatchreceiver"

grep -v "tightvncprocessor" "$WINDOWS" >"$SCRATCH/no-tightvnc.yaml"
expect_exit_code "a Windows manifest without tightvncprocessor fails" 1 "$CHECK" "$SCRATCH/no-tightvnc.yaml" "$STATIC"
expect_output "the missing tightvncprocessor is named" "expected addition is missing: .*tightvncprocessor"

for linux in builder-config.yaml builder-edge.yaml builder-edge-static.yaml builder-gateway.yaml builder-scrape.yaml; do
	if grep -q "fswatch" "$ROOT/$linux"; then
		echo "FAIL: $linux lists fswatch; only the Windows edge manifest may"
		failures=$((failures + 1))
	else
		echo "PASS: $linux has no fswatch entry"
	fi
	if grep -q "tightvnc" "$ROOT/$linux"; then
		echo "FAIL: $linux lists tightvnc; only the Windows edge manifest may"
		failures=$((failures + 1))
	else
		echo "PASS: $linux has no tightvnc entry"
	fi
done

grep -v "journaldreceiver" "$STATIC" >"$SCRATCH/static-no-journald.yaml"
expect_exit_code "journaldreceiver no longer removed from static fails" 1 \
	"$CHECK" "$WINDOWS" "$SCRATCH/static-no-journald.yaml"
expect_output "the missing removal is named" "expected removal is missing: .*journaldreceiver"

grep -v "containernameprocessor" "$STATIC" >"$SCRATCH/static-no-containername.yaml"
expect_exit_code "containernameprocessor no longer removed from static fails" 1 \
	"$CHECK" "$WINDOWS" "$SCRATCH/static-no-containername.yaml"
expect_output "the missing containername removal is named" "expected removal is missing: .*containernameprocessor"

cp "$WINDOWS" "$SCRATCH/windows-with-containername.yaml"
sed -i.bak '/^processors:/a\
  - gomod: github.com/vladistan/otelcol-custom/processor/containernameprocessor v0.0.0' "$SCRATCH/windows-with-containername.yaml"
expect_exit_code "containernameprocessor on the Windows edge fails" 1 \
	"$CHECK" "$SCRATCH/windows-with-containername.yaml" "$STATIC"
expect_output "the Windows containername is named" "containernameprocessor"

grep -v "kthreadprocessor" "$STATIC" >"$SCRATCH/static-no-kthread.yaml"
expect_exit_code "kthreadprocessor no longer removed from static fails" 1 \
	"$CHECK" "$WINDOWS" "$SCRATCH/static-no-kthread.yaml"
expect_output "the missing kthread removal is named" "expected removal is missing: .*kthreadprocessor"

cp "$WINDOWS" "$SCRATCH/windows-with-kthread.yaml"
sed -i.bak '/^processors:/a\
  - gomod: github.com/vladistan/otelcol-custom/processor/kthreadprocessor v0.0.0' "$SCRATCH/windows-with-kthread.yaml"
expect_exit_code "kthreadprocessor on the Windows edge fails" 1 \
	"$CHECK" "$SCRATCH/windows-with-kthread.yaml" "$STATIC"
expect_output "the Windows kthread is named" "kthreadprocessor"

grep -v "processlifetimeprocessor" "$STATIC" >"$SCRATCH/static-no-processlifetime.yaml"
expect_exit_code "processlifetimeprocessor no longer removed from static fails" 1 \
	"$CHECK" "$WINDOWS" "$SCRATCH/static-no-processlifetime.yaml"
expect_output "the missing processlifetime removal is named" "expected removal is missing: .*processlifetimeprocessor"

cp "$WINDOWS" "$SCRATCH/windows-with-processlifetime.yaml"
sed -i.bak '/^processors:/a\
  - gomod: github.com/vladistan/otelcol-custom/processor/processlifetimeprocessor v0.0.0' "$SCRATCH/windows-with-processlifetime.yaml"
expect_exit_code "processlifetimeprocessor on the Windows edge fails" 1 \
	"$CHECK" "$SCRATCH/windows-with-processlifetime.yaml" "$STATIC"
expect_output "the Windows processlifetime is named" "processlifetimeprocessor"
grep -v "dockerstatsreceiver" "$STATIC" >"$SCRATCH/static-no-docker.yaml"
expect_exit_code "dockerstatsreceiver no longer removed from static fails" 1 \
	"$CHECK" "$WINDOWS" "$SCRATCH/static-no-docker.yaml"
expect_output "the missing docker_stats removal is named" "expected removal is missing: .*dockerstatsreceiver"

cp "$WINDOWS" "$SCRATCH/windows-with-docker.yaml"
sed -i.bak '/^receivers:/a\
  - gomod: github.com/open-telemetry/opentelemetry-collector-contrib/receiver/dockerstatsreceiver v0.164.1' "$SCRATCH/windows-with-docker.yaml"
expect_exit_code "dockerstatsreceiver on the Windows edge fails" 1 \
	"$CHECK" "$SCRATCH/windows-with-docker.yaml" "$STATIC"
expect_output "the Windows docker_stats is named" "dockerstatsreceiver"

for linux in builder-edge.yaml builder-edge-static.yaml; do
	if grep -q "dockerstatsreceiver" "$ROOT/$linux"; then
		echo "PASS: $linux lists dockerstatsreceiver"
	else
		echo "FAIL: $linux does not list dockerstatsreceiver"
		failures=$((failures + 1))
	fi
done
for other in builder-config.yaml builder-edge-windows.yaml builder-gateway.yaml builder-scrape.yaml; do
	if grep -q "dockerstatsreceiver" "$ROOT/$other"; then
		echo "FAIL: $other lists dockerstatsreceiver; only the Linux edge manifests may"
		failures=$((failures + 1))
	else
		echo "PASS: $other has no dockerstatsreceiver entry"
	fi
done

grep -v "k8sattributesprocessor" "$STATIC" >"$SCRATCH/static-no-k8sattributes.yaml"
expect_exit_code "k8sattributesprocessor no longer removed from static fails" 1 \
	"$CHECK" "$WINDOWS" "$SCRATCH/static-no-k8sattributes.yaml"
expect_output "the missing k8sattributes removal is named" "expected removal is missing: .*k8sattributesprocessor"

cp "$WINDOWS" "$SCRATCH/windows-with-k8sattributes.yaml"
sed -i.bak '/^processors:/a\
  - gomod: github.com/open-telemetry/opentelemetry-collector-contrib/processor/k8sattributesprocessor v0.164.1' "$SCRATCH/windows-with-k8sattributes.yaml"
expect_exit_code "k8sattributesprocessor on the Windows edge fails" 1 \
	"$CHECK" "$SCRATCH/windows-with-k8sattributes.yaml" "$STATIC"
expect_output "the Windows k8sattributes is named" "k8sattributesprocessor"

for linux in builder-edge.yaml builder-edge-static.yaml; do
	if grep -q "k8sattributesprocessor" "$ROOT/$linux"; then
		echo "PASS: $linux lists k8sattributesprocessor"
	else
		echo "FAIL: $linux does not list k8sattributesprocessor"
		failures=$((failures + 1))
	fi
done
# The gateway and the default manifest carry k8sattributes already; the Windows edge and the scrape build must not.
for other in builder-edge-windows.yaml builder-scrape.yaml; do
	if grep -q "k8sattributesprocessor" "$ROOT/$other"; then
		echo "FAIL: $other lists k8sattributesprocessor; the edge builds on Linux are the only edge builds that may"
		failures=$((failures + 1))
	else
		echo "PASS: $other has no k8sattributesprocessor entry"
	fi
done
grep -v "ethtoolreceiver" "$STATIC" >"$SCRATCH/static-no-ethtool.yaml"
expect_exit_code "ethtoolreceiver no longer removed from static fails" 1 \
	"$CHECK" "$WINDOWS" "$SCRATCH/static-no-ethtool.yaml"
expect_output "the missing ethtool removal is named" "expected removal is missing: .*ethtoolreceiver"

cp "$WINDOWS" "$SCRATCH/windows-with-ethtool.yaml"
sed -i.bak '/^receivers:/a\
  - gomod: github.com/vladistan/otelcol-custom/receiver/ethtoolreceiver v0.0.0' "$SCRATCH/windows-with-ethtool.yaml"
expect_exit_code "ethtoolreceiver on the Windows edge fails" 1 \
	"$CHECK" "$SCRATCH/windows-with-ethtool.yaml" "$STATIC"
expect_output "the Windows ethtool is named" "ethtoolreceiver"

grep -v "zfskstatreceiver" "$STATIC" >"$SCRATCH/static-no-zfskstat.yaml"
expect_exit_code "zfskstatreceiver no longer removed from static fails" 1 \
	"$CHECK" "$WINDOWS" "$SCRATCH/static-no-zfskstat.yaml"
expect_output "the missing zfskstat removal is named" "expected removal is missing: .*zfskstatreceiver"

cp "$WINDOWS" "$SCRATCH/windows-with-zfskstat.yaml"
sed -i.bak '/^receivers:/a\
  - gomod: github.com/vladistan/otelcol-custom/receiver/zfskstatreceiver v0.0.0' "$SCRATCH/windows-with-zfskstat.yaml"
expect_exit_code "zfskstatreceiver on the Windows edge fails" 1 \
	"$CHECK" "$SCRATCH/windows-with-zfskstat.yaml" "$STATIC"
expect_output "the Windows zfskstat is named" "zfskstatreceiver"

grep -v "zfskstatreceiver" "$ROOT/builder-edge.yaml" >"$SCRATCH/edge-no-zfskstat.yaml"
if cmp -s "$ROOT/builder-edge.yaml" "$SCRATCH/edge-no-zfskstat.yaml"; then
	echo "FAIL: builder-edge.yaml does not list zfskstatreceiver"
	failures=$((failures + 1))
else
	echo "PASS: builder-edge.yaml lists zfskstatreceiver"
fi

for other in builder-config.yaml builder-gateway.yaml builder-scrape.yaml; do
	if grep -q "zfskstat" "$ROOT/$other"; then
		echo "FAIL: $other lists zfskstat; only the Linux edge manifests may"
		failures=$((failures + 1))
	else
		echo "PASS: $other has no zfskstat entry"
	fi
done

expect_exit_code "a missing manifest is a usage error" 2 "$CHECK" "$SCRATCH/absent.yaml"
expect_output "usage error names the file" "absent.yaml"

[ "$failures" -eq 0 ]
