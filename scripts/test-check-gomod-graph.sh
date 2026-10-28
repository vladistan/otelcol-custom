#!/usr/bin/env bash
# Tests check-gomod-graph.sh against scratch go.mod files built from the
# committed baselines. No network, no OCB run.
# Usage: test-check-gomod-graph.sh
# Exit: 0 all pass, 1 a case failed.
set -euo pipefail
unset CDPATH

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly ROOT
readonly CHECK="$ROOT/scripts/check-gomod-graph.sh"
readonly BASELINES="$ROOT/test/baselines/gomod"
SCRATCH="$(mktemp -d)"
readonly SCRATCH
trap 'rm -rf "$SCRATCH"' EXIT

failures=0

# Writes a go.mod holding the given "path version" lines from stdin.
write_gomod() {
	{
		printf 'module scratch\n\ngo 1.25.0\n\nrequire (\n'
		while read -r path version; do
			printf '\t%s %s\n' "$path" "$version"
		done
		printf ')\n'
	} >"$1"
}

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

write_gomod "$SCRATCH/same.mod" <"$BASELINES/gateway.txt"
expect_exit_code "unchanged go.mod matches the gateway baseline" 0 \
	"$CHECK" --gomod "$SCRATCH/same.mod" gateway
expect_output "success output says no version change" "no version change: gateway"

awk '/opentelemetry-collector-contrib/ && !is_raised {
	n = split($2, v, "."); v[n] = v[n] + 1
	$2 = v[1]; for (i = 2; i <= n; i++) $2 = $2 "." v[i]
	is_raised = 1
} { print }' "$BASELINES/gateway.txt" | write_gomod "$SCRATCH/raised.mod"
expect_exit_code "a raised contrib pin fails the gateway check" 1 \
	"$CHECK" --gomod "$SCRATCH/raised.mod" gateway
expect_output "failure output names the contrib module" "opentelemetry-collector-contrib"

awk '/go-expohisto/ { n = split($2, v, "."); v[n] = v[n] + 1
	$2 = v[1]; for (i = 2; i <= n; i++) $2 = $2 "." v[i] } { print }' \
	"$BASELINES/edge.txt" | write_gomod "$SCRATCH/histo.mod"
expect_exit_code "a raised go-expohisto pin fails the edge check" 1 \
	"$CHECK" --gomod "$SCRATCH/histo.mod" edge
expect_output "the go-expohisto module is named" "go-expohisto"

# edge-windows compares shared modules only: one extra and one missing are fine.
{
	sed '1d' "$BASELINES/edge-static.txt"
	echo "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/windowsperfcountersreceiver v0.0.1"
} | write_gomod "$SCRATCH/win-ok.mod"
expect_exit_code "edge-windows tolerates added and removed modules" 0 \
	"$CHECK" --gomod "$SCRATCH/win-ok.mod" edge-windows

awk 'NR == 2 { n = split($2, v, "."); v[n] = v[n] + 1
	$2 = v[1]; for (i = 2; i <= n; i++) $2 = $2 "." v[i] } { print }' \
	"$BASELINES/edge-static.txt" | write_gomod "$SCRATCH/win-bad.mod"
expect_exit_code "edge-windows fails on a changed shared version" 1 \
	"$CHECK" --gomod "$SCRATCH/win-bad.mod" edge-windows

expect_exit_code "unknown manifest is a usage error" 2 "$CHECK" bogus
expect_output "unknown manifest message names it" "unknown manifest bogus"

expect_exit_code "--gomod with two manifests is a usage error" 2 \
	"$CHECK" --gomod "$SCRATCH/same.mod" gateway scrape
expect_output "two-manifest message" "exactly one manifest"

expect_exit_code "generating without OCB_VERSION is a usage error" 2 \
	env -u OCB_VERSION "$CHECK" gateway
expect_output "missing OCB_VERSION message" "OCB_VERSION is not set"

mkdir "$SCRATCH/empty-baselines"
expect_exit_code "a missing baseline is an error" 2 \
	env GOMOD_BASELINES="$SCRATCH/empty-baselines" "$CHECK" --gomod "$SCRATCH/same.mod" gateway
expect_output "missing baseline message" "does not exist"

# A go.mod with no matching modules must not overwrite a baseline.
mkdir "$SCRATCH/copy-baselines"
cp "$BASELINES/gateway.txt" "$SCRATCH/copy-baselines/gateway.txt"
printf 'module scratch\n\ngo 1.25.0\n' >"$SCRATCH/no-modules.mod"
expect_exit_code "--update refuses a go.mod with no matching modules" 2 \
	env GOMOD_BASELINES="$SCRATCH/copy-baselines" "$CHECK" --update --gomod "$SCRATCH/no-modules.mod" gateway
if cmp -s "$BASELINES/gateway.txt" "$SCRATCH/copy-baselines/gateway.txt"; then
	echo "PASS: the baseline is unchanged after the refused update"
else
	echo "FAIL: the baseline changed after the refused update"
	failures=$((failures + 1))
fi

expect_exit_code "--update rewrites the baseline from a go.mod" 0 \
	env GOMOD_BASELINES="$SCRATCH/copy-baselines" "$CHECK" --update --gomod "$SCRATCH/raised.mod" gateway
expect_exit_code "the rewritten baseline now matches the raised go.mod" 0 \
	env GOMOD_BASELINES="$SCRATCH/copy-baselines" "$CHECK" --gomod "$SCRATCH/raised.mod" gateway

# Local modules: every collector pin must match a baseline version.
pdata_line="$(grep '^go.opentelemetry.io/collector/pdata ' "$BASELINES/gateway.txt" || true)"
if [ -z "$pdata_line" ]; then
	echo "FAIL: the gateway baseline has no collector/pdata line"
	exit 1
fi
mkdir -p "$SCRATCH/tree/processor/a" "$SCRATCH/tree/receiver/b" "$SCRATCH/tree/test/c"
echo "$pdata_line" | write_gomod "$SCRATCH/tree/processor/a/go.mod"
echo "$pdata_line" | write_gomod "$SCRATCH/tree/receiver/b/go.mod"
write_gomod "$SCRATCH/tree/test/c/go.mod" </dev/null
expect_exit_code "local modules on the baseline versions pass" 0 \
	env GOMOD_LOCAL_ROOT="$SCRATCH/tree" "$CHECK" local
expect_output "local success names the module count" "no version change: local (3 modules)"

echo "$pdata_line" | awk '{ n = split($2, v, "."); v[n] = v[n] + 1
	$2 = v[1]; for (i = 2; i <= n; i++) $2 = $2 "." v[i]; print }' |
	write_gomod "$SCRATCH/tree/receiver/b/go.mod"
expect_exit_code "a drifted local module fails" 1 \
	env GOMOD_LOCAL_ROOT="$SCRATCH/tree" "$CHECK" local
expect_output "the drifted module and dependency are named" "receiver/b/go.mod: go.opentelemetry.io/collector/pdata has"

expect_exit_code "--update with local is a usage error" 2 "$CHECK" --update local
expect_output "--update local message" "local has no baseline"
expect_exit_code "--gomod with local is a usage error" 2 "$CHECK" --gomod "$SCRATCH/same.mod" local
expect_output "--gomod local message" "does not apply to local"

# The scan covers stanza/ and skips build/ output.
mkdir -p "$SCRATCH/tree/build/gen" "$SCRATCH/tree/processor/build/gen"
echo "$pdata_line" | write_gomod "$SCRATCH/tree/receiver/b/go.mod"
echo "$pdata_line" | awk '{ n = split($2, v, "."); v[n] = v[n] + 1
	$2 = v[1]; for (i = 2; i <= n; i++) $2 = $2 "." v[i]; print }' |
	write_gomod "$SCRATCH/tree/processor/build/gen/go.mod"
expect_exit_code "a drifted go.mod under build/ is skipped" 0 \
	env GOMOD_LOCAL_ROOT="$SCRATCH/tree" "$CHECK" local
mkdir -p "$SCRATCH/tree/stanza/d"
echo "$pdata_line" | awk '{ n = split($2, v, "."); v[n] = v[n] + 1
	$2 = v[1]; for (i = 2; i <= n; i++) $2 = $2 "." v[i]; print }' |
	write_gomod "$SCRATCH/tree/stanza/d/go.mod"
expect_exit_code "a drifted module under stanza/ fails" 1 \
	env GOMOD_LOCAL_ROOT="$SCRATCH/tree" "$CHECK" local
expect_output "the stanza module is named" "stanza/d/go.mod"

mkdir "$SCRATCH/no-modules-tree"
expect_exit_code "a tree with no go.mod is an error" 2 \
	env GOMOD_LOCAL_ROOT="$SCRATCH/no-modules-tree" "$CHECK" local
expect_output "empty tree message" "no local go.mod files"

mkdir -p "$SCRATCH/bad-tree/processor/x"
echo "not a go.mod" >"$SCRATCH/bad-tree/processor/x/go.mod"
expect_exit_code "an unreadable go.mod is an error" 2 \
	env GOMOD_LOCAL_ROOT="$SCRATCH/bad-tree" "$CHECK" local
expect_output "unreadable go.mod message" "cannot read"

expect_exit_code "local without baselines is an error" 2 \
	env GOMOD_BASELINES="$SCRATCH/empty-baselines" GOMOD_LOCAL_ROOT="$SCRATCH/tree" "$CHECK" local
expect_output "local without baselines message" "no baselines in"

[ "$failures" -eq 0 ]
