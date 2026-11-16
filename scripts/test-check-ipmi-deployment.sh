#!/usr/bin/env bash
# Tests check-ipmi-deployment.sh against the sample and against mutated copies of it.
# Usage: test-check-ipmi-deployment.sh
# Exit: 0 all pass, 1 a case failed.
set -euo pipefail
unset CDPATH

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly ROOT
readonly CHECK="$ROOT/scripts/check-ipmi-deployment.sh"
readonly SAMPLE="$ROOT/test/configs/scrape-ipmi-deployment.yaml"
SCRATCH="$(mktemp -d)"
readonly SCRATCH
trap 'rm -rf "$SCRATCH"' EXIT

failures=0

# expect_exit_code NAME WANT_CODE CMD... : runs CMD, compares its exit code; the output stays in $SCRATCH/out.
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

expect_output() {
	if grep -qF -- "$2" "$SCRATCH/out"; then
		echo "PASS: $1"
	else
		echo "FAIL: $1 (no match for $2)"
		sed 's/^/    /' "$SCRATCH/out"
		failures=$((failures + 1))
	fi
}

# mutate NAME SED_PROGRAM : writes a copy of the sample changed by the program.
mutate() {
	sed -e "$2" "$SAMPLE" >"$SCRATCH/$1.yaml"
	echo "$SCRATCH/$1.yaml"
}

expect_exit_code "the sample passes" 0 "$CHECK" "$SAMPLE"

expect_exit_code "two replicas fail" 1 "$CHECK" "$(mutate replicas 's/replicas: 1/replicas: 2/')"
expect_output "the replica count is named" "replicas"

expect_exit_code "a username that does not come from a Secret fails" 1 \
	"$CHECK" "$(mutate nouser 's/name: IPMI_USERNAME/name: IPMI_USER/')"
expect_output "the missing secret reference is named" "IPMI_USERNAME"

expect_exit_code "a plain Secret object fails" 1 \
	"$CHECK" "$(mutate secret 's/^kind: ExternalSecret/kind: Secret/')"
expect_output "the Secret object is named" "plain Secret"

expect_exit_code "a stringData block fails" 1 "$CHECK" "$(mutate stringdata 's/^  refreshInterval: 1h/  stringData: {password: x}/')"

expect_exit_code "a wider port fails" 1 "$CHECK" "$(mutate port 's/port: 623/port: 6230/')"
expect_output "the missing UDP 623 rule is named" "UDP 623"

expect_exit_code "TCP in place of UDP on 623 fails" 1 "$CHECK" "$(mutate tcp '1,/protocol: UDP/s/protocol: UDP/protocol: TCP/')"
expect_output "the missing UDP rule is named" "UDP 623"

expect_exit_code "a second subnet fails" 1 \
	"$CHECK" "$(mutate subnets $'s#cidr: 192.0.2.0/24#&\\\n            cidr: 198.51.100.0/24#')"
expect_output "the subnet count is named" "one subnet"

expect_exit_code "no NetworkPolicy fails" 1 "$CHECK" "$(mutate nopolicy 's/^kind: NetworkPolicy/kind: Service/')"

expect_exit_code "a missing file is a usage error" 2 "$CHECK" "$SCRATCH/absent.yaml"
expect_output "usage error names the file" "absent.yaml"

[ "$failures" -eq 0 ]
