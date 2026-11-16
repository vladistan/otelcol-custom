#!/usr/bin/env bash
# Tests check-local-replaces.sh on the committed tree and on scratch copies with a
# replace removed, pointed at a missing directory or at a module of another name.
# No network.
# Usage: test-check-local-replaces.sh
# Exit: 0 all pass, 1 a case failed.
set -euo pipefail
unset CDPATH

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly ROOT
readonly CHECK="$ROOT/scripts/check-local-replaces.sh"
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

# expect_output_absent NAME PATTERN : the last command output does not contain PATTERN.
expect_output_absent() {
	if grep -q -- "$2" "$SCRATCH/out"; then
		echo "FAIL: $1 (unexpected match for $2)"
		sed 's/^/    /' "$SCRATCH/out"
		failures=$((failures + 1))
	else
		echo "PASS: $1"
	fi
}

# scratch_tree DIR : the committed manifests and the go.mod of every module directory they point to.
scratch_tree() {
	mkdir -p "$1"
	cp "$ROOT"/builder-*.yaml "$1/"
	local target
	while read -r target; do
		mkdir -p "$1/$target"
		cp "$ROOT/$target/go.mod" "$1/$target/go.mod"
	done < <(sed -nE 's#^[[:space:]]*-[[:space:]]+[^[:space:]]+[[:space:]]+=>[[:space:]]+\.\./\.\./([^[:space:]]+)[[:space:]]*$#\1#p' "$ROOT"/builder-*.yaml | sort -u)
}

expect_exit_code "the committed tree passes" 0 "$CHECK"
expect_output "success output says PASS" "^PASS"

scratch_tree "$SCRATCH/copy"
expect_exit_code "a scratch copy of the tree passes" 0 "$CHECK" "$SCRATCH/copy"

scratch_tree "$SCRATCH/no-gcp"
grep -v "googlecloudmonitoringreceiver =>" "$ROOT/builder-scrape.yaml" >"$SCRATCH/no-gcp/builder-scrape.yaml"
expect_exit_code "a scrape manifest without the Cloud Monitoring replace fails" 1 "$CHECK" "$SCRATCH/no-gcp"
expect_output "the missing replace names the module" "googlecloudmonitoringreceiver"

scratch_tree "$SCRATCH/no-aws"
grep -v "awscloudwatchreceiver =>" "$ROOT/builder-scrape.yaml" >"$SCRATCH/no-aws/builder-scrape.yaml"
expect_exit_code "a scrape manifest without the CloudWatch replace fails" 1 "$CHECK" "$SCRATCH/no-aws"
expect_output "the missing CloudWatch replace names the module" "awscloudwatchreceiver"

scratch_tree "$SCRATCH/no-pve-replace"
grep -v "pvereceiver =>" "$ROOT/builder-scrape.yaml" >"$SCRATCH/no-pve-replace/builder-scrape.yaml"
expect_exit_code "a scrape manifest without the pve replace fails" 1 "$CHECK" "$SCRATCH/no-pve-replace"
expect_output "the missing pve replace is named as a replace" "does not replace .*pvereceiver"
expect_output_absent "the pve receiver line is still found" "does not list the receiver .*pvereceiver"

scratch_tree "$SCRATCH/no-pve"
grep -v "gomod: .*pvereceiver" "$ROOT/builder-scrape.yaml" >"$SCRATCH/no-pve/builder-scrape.yaml"
expect_exit_code "a scrape manifest without the pve receiver entry fails" 1 "$CHECK" "$SCRATCH/no-pve"
expect_output "the missing pve entry is named as a receiver" "does not list the receiver .*pvereceiver"
expect_output_absent "the pve replace is still found" "does not replace .*pvereceiver"

scratch_tree "$SCRATCH/no-snmp"
grep -v "snmpreceiver" "$ROOT/builder-scrape.yaml" >"$SCRATCH/no-snmp/builder-scrape.yaml"
expect_exit_code "a scrape manifest without the snmp receiver fails" 1 "$CHECK" "$SCRATCH/no-snmp"
expect_output "the missing snmp entry names the receiver" "snmpreceiver"

scratch_tree "$SCRATCH/gone"
rm -rf "$SCRATCH/gone/receiver/googlecloudmonitoringreceiver"
expect_exit_code "a replace to a missing directory fails" 1 "$CHECK" "$SCRATCH/gone"
expect_output "the missing directory has no go.mod" "has no go.mod"

scratch_tree "$SCRATCH/renamed"
sed -i.bak 's#^module .*#module example.com/other#' "$SCRATCH/renamed/receiver/googlecloudmonitoringreceiver/go.mod"
expect_exit_code "a module of another name fails" 1 "$CHECK" "$SCRATCH/renamed"
expect_output "the other name is shown" "example.com/other"

scratch_tree "$SCRATCH/empty"
rm -f "$SCRATCH/empty"/builder-*.yaml
expect_exit_code "a directory with no manifests is a usage error" 2 "$CHECK" "$SCRATCH/empty"
expect_output "usage error says no manifests" "no builder-"

expect_exit_code "a missing directory is a usage error" 2 "$CHECK" "$SCRATCH/absent"
expect_output "usage error names the directory" "absent"

[ "$failures" -eq 0 ]
