#!/usr/bin/env bash
# Tests check-dockerfile-copies.sh on the committed tree and on scratch copies with a COPY line
# removed, a builder manifest missing or no builder manifest copied at all. No network.
# Usage: test-check-dockerfile-copies.sh
# Exit: 0 all pass, 1 a case failed.
set -euo pipefail
unset CDPATH

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly ROOT
readonly CHECK="$ROOT/scripts/check-dockerfile-copies.sh"
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

# scratch_tree DIR : the committed Dockerfiles and builder manifests.
scratch_tree() {
	mkdir -p "$1"
	cp "$ROOT"/Dockerfile.* "$ROOT"/builder-*.yaml "$1/"
}

# drop_copy DIR FILE TOP : removes the COPY line of the directory TOP from FILE.
drop_copy() {
	grep -Ev "^COPY[[:space:]]+${3}[[:space:]]" "$1/$2" >"$1/$2.new"
	mv "$1/$2.new" "$1/$2"
}

expect_exit_code "the committed tree passes" 0 "$CHECK"
expect_output "success output says PASS" "^PASS"

scratch_tree "$SCRATCH/scrape"
drop_copy "$SCRATCH/scrape" Dockerfile.scrape extension
expect_exit_code "a scrape build without the extension copy fails" 1 "$CHECK" "$SCRATCH/scrape"
expect_output "the failure names the Dockerfile and the directory" "Dockerfile.scrape builds from builder-scrape.yaml.*never copies extension"

scratch_tree "$SCRATCH/gateway"
drop_copy "$SCRATCH/gateway" Dockerfile.gateway receiver
expect_exit_code "a gateway build without the receiver copy fails" 1 "$CHECK" "$SCRATCH/gateway"
expect_output "the failure names the gateway directory" "Dockerfile.gateway.*never copies receiver"

scratch_tree "$SCRATCH/missing"
rm "$SCRATCH/missing/builder-gateway.yaml"
expect_exit_code "a copied manifest that is not there fails" 1 "$CHECK" "$SCRATCH/missing"
expect_output "the failure names the manifest" "builder-gateway.yaml, which is not in"

scratch_tree "$SCRATCH/binary"
rm "$SCRATCH/binary/Dockerfile.gateway" "$SCRATCH/binary/Dockerfile.scrape"
expect_exit_code "a tree where no Dockerfile copies a manifest is a usage error" 2 "$CHECK" "$SCRATCH/binary"

mkdir -p "$SCRATCH/empty"
expect_exit_code "a directory with no Dockerfile is a usage error" 2 "$CHECK" "$SCRATCH/empty"
expect_exit_code "a missing directory is a usage error" 2 "$CHECK" "$SCRATCH/none"
expect_exit_code "two arguments are a usage error" 2 "$CHECK" a b

if [ "$failures" -ne 0 ]; then
	echo "$failures case(s) failed"
	exit 1
fi
echo "all cases passed"
