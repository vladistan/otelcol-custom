#!/usr/bin/env bash
# Tests check-sentry-go-pin.sh on the committed manifests and on scratch
# copies with the pin removed, changed or duplicated. No network.
# Usage: test-check-sentry-go-pin.sh
# Exit: 0 all pass, 1 a case failed.
set -euo pipefail
unset CDPATH

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly ROOT
readonly CHECK="$ROOT/scripts/check-sentry-go-pin.sh"
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

# scratch_tree DIR : a go.mod and a copy of every committed builder manifest.
scratch_tree() {
	mkdir -p "$1"
	cp "$ROOT/go.mod" "$1/go.mod"
	cp "$ROOT"/builder-*.yaml "$1/"
}

expect_exit_code "the committed manifests pass" 0 "$CHECK"
expect_output "success output says PASS" "^PASS"

scratch_tree "$SCRATCH/missing"
grep -v "getsentry/sentry-go" "$ROOT/builder-edge.yaml" >"$SCRATCH/missing/builder-edge.yaml"
expect_exit_code "a manifest without the pin fails" 1 "$CHECK" "$SCRATCH/missing"
expect_output "the manifest without the pin is named" "builder-edge.yaml"

scratch_tree "$SCRATCH/wrong"
sed -E 's#(getsentry/sentry-go => github.com/getsentry/sentry-go) v[0-9.]+#\1 v0.50.0#' \
	"$ROOT/builder-gateway.yaml" >"$SCRATCH/wrong/builder-gateway.yaml"
expect_exit_code "a pin that differs from go.mod fails" 1 "$CHECK" "$SCRATCH/wrong"
expect_output "the wrong pin names its manifest" "builder-gateway.yaml"

scratch_tree "$SCRATCH/commented"
sed -E 's#^([[:space:]]*)-[[:space:]]+(github.com/getsentry/sentry-go .*)#\1\# \2#' \
	"$ROOT/builder-scrape.yaml" >"$SCRATCH/commented/builder-scrape.yaml"
expect_exit_code "a commented-out pin fails" 1 "$CHECK" "$SCRATCH/commented"
expect_output "the commented-out pin names its manifest" "builder-scrape.yaml"

scratch_tree "$SCRATCH/other-module"
sed -E 's#getsentry/sentry-go => github.com/getsentry/sentry-go#getsentry/sentry-go => github.com/getsentry/other#' \
	"$ROOT/builder-edge-static.yaml" >"$SCRATCH/other-module/builder-edge-static.yaml"
expect_exit_code "a replace to another module fails" 1 "$CHECK" "$SCRATCH/other-module"
expect_output "the other-module replace names its manifest" "builder-edge-static.yaml"

scratch_tree "$SCRATCH/empty"
rm -f "$SCRATCH/empty"/builder-*.yaml
expect_exit_code "a directory with no manifests is a usage error" 2 "$CHECK" "$SCRATCH/empty"
expect_output "usage error says no manifests" "no builder-"

scratch_tree "$SCRATCH/no-gomod"
rm -f "$SCRATCH/no-gomod/go.mod"
expect_exit_code "a missing go.mod is a usage error" 2 "$CHECK" "$SCRATCH/no-gomod"
expect_output "usage error names go.mod" "go.mod"

expect_exit_code "a missing directory is a usage error" 2 "$CHECK" "$SCRATCH/absent"
expect_output "usage error names the directory" "absent"

[ "$failures" -eq 0 ]
