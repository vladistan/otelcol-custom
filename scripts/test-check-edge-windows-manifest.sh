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
  - gomod: go.opentelemetry.io/collector/exporter/nopexporter v0.154.0' "$SCRATCH/extra.yaml"
expect_exit_code "an unlisted added component fails" 1 "$CHECK" "$SCRATCH/extra.yaml" "$STATIC"
expect_output "the added component is named" "nopexporter"

grep -v "windowseventlogreceiver" "$WINDOWS" >"$SCRATCH/missing.yaml"
expect_exit_code "a missing expected component fails" 1 "$CHECK" "$SCRATCH/missing.yaml" "$STATIC"
expect_output "the missing component is named" "windowseventlogreceiver"

grep -v "journaldreceiver" "$STATIC" >"$SCRATCH/static-no-journald.yaml"
expect_exit_code "journaldreceiver no longer removed from static fails" 1 \
	"$CHECK" "$WINDOWS" "$SCRATCH/static-no-journald.yaml"
expect_output "the missing removal is named" "expected removal is missing: .*journaldreceiver"

expect_exit_code "a missing manifest is a usage error" 2 "$CHECK" "$SCRATCH/absent.yaml"
expect_output "usage error names the file" "absent.yaml"

[ "$failures" -eq 0 ]
