#!/usr/bin/env bash
# Tests check-snmp-secrets.sh on the committed sample configs and on scratch configs with a literal
# community, auth_password or privacy_password. No network.
# Usage: test-check-snmp-secrets.sh
# Exit: 0 all pass, 1 a case failed.
set -euo pipefail
unset CDPATH

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly ROOT
readonly CHECK="$ROOT/scripts/check-snmp-secrets.sh"
SCRATCH="$(mktemp -d)"
readonly SCRATCH
trap 'rm -rf "$SCRATCH"' EXIT

failures=0

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
	if grep -q -- "$2" "$SCRATCH/out"; then
		echo "PASS: $1"
	else
		echo "FAIL: $1 (no match for $2)"
		sed 's/^/    /' "$SCRATCH/out"
		failures=$((failures + 1))
	fi
}

# scratch_config NAME LINE : a directory with one config holding LINE under an snmp instance.
scratch_config() {
	mkdir -p "$SCRATCH/$1"
	printf 'receivers:\n  snmp:\n    version: v3\n    %s\n' "$2" >"$SCRATCH/$1/c.yaml"
}

expect_exit_code "the committed sample configs pass" 0 "$CHECK"
expect_output "success output says PASS" "^PASS"

scratch_config env-community 'community: ${env:SNMP_COMMUNITY}'
expect_exit_code "an env community passes" 0 "$CHECK" "$SCRATCH/env-community"

scratch_config quoted-env 'auth_password: "${env:SNMP_AUTH}"'
expect_exit_code "a quoted env value passes" 0 "$CHECK" "$SCRATCH/quoted-env"

scratch_config literal-community 'community: public'
expect_exit_code "a literal community fails" 1 "$CHECK" "$SCRATCH/literal-community"
expect_output "the literal community is named with its file" "literal-community/c.yaml:4:community: public"

scratch_config literal-auth 'auth_password: hunter2'
expect_exit_code "a literal auth_password fails" 1 "$CHECK" "$SCRATCH/literal-auth"
expect_output "the literal auth_password is named" "auth_password: hunter2"

scratch_config literal-priv 'privacy_password: "hunter3"' # pragma: allowlist secret
expect_exit_code "a literal quoted privacy_password fails" 1 "$CHECK" "$SCRATCH/literal-priv"
expect_output "the literal privacy_password is named" "privacy_password"

scratch_config partial-env 'community: prefix-${env:SNMP_COMMUNITY}'
expect_exit_code "a value that only contains an env reference fails" 1 "$CHECK" "$SCRATCH/partial-env"

scratch_config commented '# community: public'
expect_exit_code "a commented line passes" 0 "$CHECK" "$SCRATCH/commented"

scratch_config trailing-comment 'community: ${env:SNMP_COMMUNITY} # from the role'
expect_exit_code "an env value with a trailing comment passes" 0 "$CHECK" "$SCRATCH/trailing-comment"

scratch_config dash-literal '- community: public'
expect_exit_code "a literal community after a list dash fails" 1 "$CHECK" "$SCRATCH/dash-literal"
expect_output "the dash form is named" "- community: public"

scratch_config dash-env '- community: ${env:SNMP_COMMUNITY}'
expect_exit_code "an env community after a list dash passes" 0 "$CHECK" "$SCRATCH/dash-env"

scratch_config flow-literal 'x: {community: public}'
expect_exit_code "a literal community in a flow mapping fails" 1 "$CHECK" "$SCRATCH/flow-literal"

scratch_config flow-second 'x: {auth_password: ${env:SNMP_AUTH}, privacy_password: hunter3}'
expect_exit_code "a literal second key of a flow mapping fails" 1 "$CHECK" "$SCRATCH/flow-second"
expect_output "the second key is named" "privacy_password"

scratch_config flow-env 'x: {community: ${env:SNMP_COMMUNITY}, user: otel}'
expect_exit_code "an env value in a flow mapping passes" 0 "$CHECK" "$SCRATCH/flow-env"

scratch_config quoted-key '"community": public'
expect_exit_code "a quoted key with a literal value fails" 1 "$CHECK" "$SCRATCH/quoted-key"

scratch_config single-quoted-key "'privacy_password': hunter3"
expect_exit_code "a single-quoted key with a literal value fails" 1 "$CHECK" "$SCRATCH/single-quoted-key"

scratch_config quoted-key-env '"community": ${env:SNMP_COMMUNITY}'
expect_exit_code "a quoted key with an env value passes" 0 "$CHECK" "$SCRATCH/quoted-key-env"

scratch_config unmatched-quote 'community: ${env:SNMP_COMMUNITY}"'
expect_exit_code "an env value with a stray closing quote fails" 1 "$CHECK" "$SCRATCH/unmatched-quote"

scratch_config single-quoted-env "community: '\${env:SNMP_COMMUNITY}'"
expect_exit_code "a single-quoted env value passes" 0 "$CHECK" "$SCRATCH/single-quoted-env"

scratch_config comment-after-value 'user: otel # community: public'
expect_exit_code "a key named only in a trailing comment passes" 0 "$CHECK" "$SCRATCH/comment-after-value"

mkdir -p "$SCRATCH/upper"
printf 'receivers:\n  snmp:\n    community: public\n' >"$SCRATCH/upper/C.YAML"
expect_exit_code "a file with an upper-case extension is scanned" 1 "$CHECK" "$SCRATCH/upper"

mkdir -p "$SCRATCH/yml"
printf 'receivers:\n  snmp:\n    community: public\n' >"$SCRATCH/yml/c.yml"
expect_exit_code "a .yml file is scanned" 1 "$CHECK" "$SCRATCH/yml"

mkdir -p "$SCRATCH/empty"
expect_exit_code "a directory with no config passes" 0 "$CHECK" "$SCRATCH/empty"

expect_exit_code "a missing directory is a usage error" 2 "$CHECK" "$SCRATCH/absent"
expect_output "usage error names the directory" "absent"

[ "$failures" -eq 0 ]
