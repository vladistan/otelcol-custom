#!/usr/bin/env bash
# Tests check-logshape-in-builds.sh on fixture manifests with a known result (complete, no processor,
# no processor replace, no helper replace, no manifests) and on the committed tree. No network.
# Usage: test-check-logshape-in-builds.sh
# Exit: 0 all pass, 1 a case failed.
set -euo pipefail
unset CDPATH

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly ROOT
readonly CHECK="$ROOT/scripts/check-logshape-in-builds.sh"
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

# fixture_tree DIR : a go.mod with two helper replaces and one manifest that carries everything.
fixture_tree() {
	mkdir -p "$1/processor/logshapeprocessor"
	cat >"$1/processor/logshapeprocessor/go.mod" <<'EOF'
module github.com/vladistan/otelcol-custom/processor/logshapeprocessor

go 1.25

require (
	github.com/vladistan/otelcol-custom/shape/alpha v0.0.0
	github.com/vladistan/otelcol-custom/shape/beta v0.0.0
)

replace github.com/vladistan/otelcol-custom/shape/alpha => ../../shape/alpha

replace github.com/vladistan/otelcol-custom/shape/beta => ../../shape/beta

replace example.com/other => ../../other
EOF
	cat >"$1/builder-one.yaml" <<'EOF'
dist:
  name: one
replaces:
  - github.com/vladistan/otelcol-custom/processor/logshapeprocessor => ../../processor/logshapeprocessor
  - github.com/vladistan/otelcol-custom/shape/alpha => ../../shape/alpha
  - github.com/vladistan/otelcol-custom/shape/beta => ../../shape/beta
receivers:
  - gomod: example.com/receiver v0.0.0
processors:
  # Log shaping
  - gomod: github.com/vladistan/otelcol-custom/processor/logshapeprocessor v0.0.0
exporters:
  - gomod: example.com/exporter v0.0.0
EOF
	sed 's/name: one/name: two/' "$1/builder-one.yaml" >"$1/builder-two.yaml"
}

expect_exit_code "the committed tree passes" 0 "$CHECK"
expect_output "success output says PASS" "^PASS"
expect_output "every committed manifest is named" "builder-edge-windows.yaml"

fixture_tree "$SCRATCH/ok"
expect_exit_code "complete fixture manifests pass" 0 "$CHECK" "$SCRATCH/ok"
expect_output "each fixture manifest has a line" "PASS: builder-two.yaml"

fixture_tree "$SCRATCH/default-build"
printf 'dist:\n  name: default\nprocessors:\n  - gomod: example.com/processor v0.0.0\n' >"$SCRATCH/default-build/builder-config.yaml"
expect_exit_code "builder-config.yaml, the local default build, is skipped" 0 "$CHECK" "$SCRATCH/default-build"

fixture_tree "$SCRATCH/no-processor"
grep -v "gomod: .*logshapeprocessor v0" "$SCRATCH/ok/builder-two.yaml" >"$SCRATCH/no-processor/builder-two.yaml"
expect_exit_code "a manifest without the processor entry fails" 1 "$CHECK" "$SCRATCH/no-processor"
expect_output "the failure names the manifest and the entry" "FAIL: builder-two.yaml lacks the processors entry"
expect_output "the other manifest still passes" "PASS: builder-one.yaml"

fixture_tree "$SCRATCH/entry-outside"
cat >"$SCRATCH/entry-outside/builder-two.yaml" <<'EOF'
replaces:
  - github.com/vladistan/otelcol-custom/processor/logshapeprocessor => ../../processor/logshapeprocessor
  - github.com/vladistan/otelcol-custom/shape/alpha => ../../shape/alpha
  - github.com/vladistan/otelcol-custom/shape/beta => ../../shape/beta
receivers:
  - gomod: github.com/vladistan/otelcol-custom/processor/logshapeprocessor v0.0.0
processors:
  - gomod: example.com/processor v0.0.0
EOF
expect_exit_code "an entry outside the processors list does not count" 1 "$CHECK" "$SCRATCH/entry-outside"

fixture_tree "$SCRATCH/no-replace"
grep -v "processor/logshapeprocessor =>" "$SCRATCH/ok/builder-one.yaml" >"$SCRATCH/no-replace/builder-one.yaml"
expect_exit_code "a manifest without the processor replace fails" 1 "$CHECK" "$SCRATCH/no-replace"
expect_output "the failure names the manifest and the replace" "FAIL: builder-one.yaml lacks the replace of .*logshapeprocessor"

fixture_tree "$SCRATCH/no-helper"
grep -v "shape/beta =>" "$SCRATCH/ok/builder-two.yaml" >"$SCRATCH/no-helper/builder-two.yaml"
expect_exit_code "a manifest without one helper replace fails" 1 "$CHECK" "$SCRATCH/no-helper"
expect_output "the failure names the helper" "FAIL: builder-two.yaml lacks the replace of .*shape/beta"

fixture_tree "$SCRATCH/empty"
rm -f "$SCRATCH/empty"/builder-*.yaml
expect_exit_code "a directory with no manifests is a usage error" 2 "$CHECK" "$SCRATCH/empty"
expect_output "usage error says no manifests" "no builder-"

fixture_tree "$SCRATCH/no-gomod"
rm "$SCRATCH/no-gomod/processor/logshapeprocessor/go.mod"
expect_exit_code "a missing go.mod is a usage error" 2 "$CHECK" "$SCRATCH/no-gomod"

expect_exit_code "a missing directory is a usage error" 2 "$CHECK" "$SCRATCH/absent"
expect_exit_code "two arguments are a usage error" 2 "$CHECK" a b

if [ "$failures" -ne 0 ]; then
	echo "$failures case(s) failed"
	exit 1
fi
echo "all cases passed"
