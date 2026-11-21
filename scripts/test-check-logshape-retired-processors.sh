#!/usr/bin/env bash
# Tests check-logshape-retired-processors.sh on fixture trees with a known result (complete, one
# processor missing, phrase missing, version missing) and on the committed tree. No network.
# Usage: test-check-logshape-retired-processors.sh
# Exit: 0 all pass, 1 a case failed.
set -euo pipefail
unset CDPATH

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly ROOT
readonly CHECK="$ROOT/scripts/check-logshape-retired-processors.sh"
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

# fixture_tree DIR : a gateway manifest with the ten processors and two documents that date the removal.
fixture_tree() {
	mkdir -p "$1/docs"
	{
		echo "processors:"
		local name
		for name in logshape klog uvicornlog logruslog postgreslog redislog nginxlog sentrylog ciscolog syslog; do
			echo "  - gomod: github.com/vladistan/otelcol-custom/processor/${name}processor v0.0.0"
		done
	} >"$1/builder-gateway.yaml"
	echo "They are removed in the release after production validation, 0.3.19 at the earliest." >"$1/docs/log-shaping.md"
	echo "- They stay in 0.3.18 and go in the release after production validation, 0.3.19 at the earliest." >"$1/CHANGELOG.md"
}

expect_exit_code "the committed tree passes" 0 "$CHECK"
expect_output "success output says PASS" "^PASS"

fixture_tree "$SCRATCH/ok"
expect_exit_code "a complete fixture passes" 0 "$CHECK" "$SCRATCH/ok"

fixture_tree "$SCRATCH/no-processor"
grep -v "nginxlogprocessor" "$SCRATCH/ok/builder-gateway.yaml" >"$SCRATCH/no-processor/builder-gateway.yaml"
expect_exit_code "a gateway without one old processor fails" 1 "$CHECK" "$SCRATCH/no-processor"
expect_output "the failure names the processor" "FAIL: builder-gateway.yaml does not list .*nginxlogprocessor"

fixture_tree "$SCRATCH/no-logshape"
grep -v "logshapeprocessor" "$SCRATCH/ok/builder-gateway.yaml" >"$SCRATCH/no-logshape/builder-gateway.yaml"
expect_exit_code "a gateway without logshape fails" 1 "$CHECK" "$SCRATCH/no-logshape"
expect_output "the failure names logshape" "does not list .*logshapeprocessor"

fixture_tree "$SCRATCH/no-phrase-docs"
echo "They are removed in a later release." >"$SCRATCH/no-phrase-docs/docs/log-shaping.md"
expect_exit_code "a guide without the phrase fails" 1 "$CHECK" "$SCRATCH/no-phrase-docs"
expect_output "the failure names the guide" "FAIL: docs/log-shaping.md does not say"

fixture_tree "$SCRATCH/no-phrase-log"
echo "- They are removed in a later release, 0.3.19 at the earliest." >"$SCRATCH/no-phrase-log/CHANGELOG.md"
expect_exit_code "a changelog without the phrase fails" 1 "$CHECK" "$SCRATCH/no-phrase-log"
expect_output "the failure names the changelog" "FAIL: CHANGELOG.md does not say"

fixture_tree "$SCRATCH/no-version"
echo "- They go in the release after production validation." >"$SCRATCH/no-version/CHANGELOG.md"
expect_exit_code "a changelog without the version fails" 1 "$CHECK" "$SCRATCH/no-version"
expect_output "the failure names the version" "FAIL: CHANGELOG.md says .* without naming 0.3.19"

fixture_tree "$SCRATCH/no-docs"
rm "$SCRATCH/no-docs/docs/log-shaping.md"
expect_exit_code "a missing guide is a usage error" 2 "$CHECK" "$SCRATCH/no-docs"

expect_exit_code "a missing directory is a usage error" 2 "$CHECK" "$SCRATCH/absent"
expect_exit_code "two arguments are a usage error" 2 "$CHECK" a b

if [ "$failures" -ne 0 ]; then
	echo "$failures case(s) failed"
	exit 1
fi
echo "all cases passed"
