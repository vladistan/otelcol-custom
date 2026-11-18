#!/usr/bin/env bash
# Tests check-sample-user.sh against the repository samples and against scratch files.
# Usage: test-check-sample-user.sh
# Exit: 0 all pass, 1 a case failed.
set -euo pipefail
unset CDPATH

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly ROOT
readonly CHECK="$ROOT/scripts/check-sample-user.sh"
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

# dockerfile UID : writes a scratch Dockerfile whose user has the given numeric id and prints its path.
dockerfile() {
	printf '%s\n' \
		"RUN addgroup -g $1 -S otel && \\" \
		"    adduser -u $1 -S otel -G otel" \
		"USER otel" >"$SCRATCH/Dockerfile"
	printf '%s' "$SCRATCH/Dockerfile"
}

# sample NAME BODY... : writes a scratch Deployment whose container securityContext holds the body lines.
sample() {
	local name="$1"
	shift
	{
		echo "kind: Deployment"
		echo "spec:"
		echo "  template:"
		echo "    spec:"
		echo "      containers:"
		echo "        - name: otelcol-scrape"
		echo "          securityContext:"
		printf '            %s\n' "$@"
	} >"$SCRATCH/$name.yaml"
	printf '%s' "$SCRATCH/$name.yaml"
}

expect_exit_code "the repository samples pass" 0 "$CHECK"

docker="$(dockerfile 10001)"

expect_exit_code "runAsNonRoot with the image user and group passes" 0 \
	"$CHECK" "$docker" "$(sample ok 'runAsNonRoot: true' 'runAsUser: 10001' 'runAsGroup: 10001')"

expect_exit_code "runAsNonRoot without a numeric user fails" 1 \
	"$CHECK" "$docker" "$(sample nouser 'runAsNonRoot: true' 'runAsGroup: 10001')"
expect_output "the missing user is named" "runAsUser"

expect_exit_code "runAsNonRoot without a numeric group fails" 1 \
	"$CHECK" "$docker" "$(sample nogroup 'runAsNonRoot: true' 'runAsUser: 10001')"
expect_output "the missing group is named" "runAsGroup"

expect_exit_code "a user name instead of a number fails" 1 \
	"$CHECK" "$docker" "$(sample named 'runAsNonRoot: true' 'runAsUser: otel' 'runAsGroup: 10001')"

expect_exit_code "a user that differs from the image fails" 1 \
	"$CHECK" "$docker" "$(sample other 'runAsNonRoot: true' 'runAsUser: 1000' 'runAsGroup: 10001')"
expect_output "the image user is named" "10001"

expect_exit_code "a sample without runAsNonRoot is not checked" 0 \
	"$CHECK" "$docker" "$(sample plain 'readOnlyRootFilesystem: true')"

printf '%s\n' 'USER otel' >"$SCRATCH/Dockerfile.numberless"
expect_exit_code "a Dockerfile without a numeric user id fails" 1 \
	"$CHECK" "$SCRATCH/Dockerfile.numberless" "$(sample ok2 'runAsNonRoot: true' 'runAsUser: 10001' 'runAsGroup: 10001')"

expect_exit_code "a missing sample is a usage error" 2 "$CHECK" "$docker" "$SCRATCH/does-not-exist.yaml"

if [ "$failures" -ne 0 ]; then
	echo "$failures case(s) failed"
	exit 1
fi
echo "all cases passed"
