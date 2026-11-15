#!/usr/bin/env bash
# Tests check-docker-stats-api-version.sh on the committed sample configs and on scratch configs.
# Usage: test-check-docker-stats-api-version.sh
# Exit: 0 all pass, 1 a case failed.
set -euo pipefail
unset CDPATH

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly ROOT
readonly CHECK="$ROOT/scripts/check-docker-stats-api-version.sh"
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
	if grep -q -- "$2" "$SCRATCH/out"; then
		echo "PASS: $1"
	else
		echo "FAIL: $1 (no match for $2)"
		sed 's/^/    /' "$SCRATCH/out"
		failures=$((failures + 1))
	fi
}

write() {
	cat >"$SCRATCH/$1"
}

expect_exit_code "the committed sample configs pass" 0 "$CHECK"
expect_output "success output says PASS" "^PASS"

write empty-version.yaml <<'EOF'
receivers:
  docker_stats:
    endpoint: unix:///var/run/docker.sock
    api_version: ""
    collection_interval: 60s
EOF
expect_exit_code "an empty api_version passes" 0 "$CHECK" "$SCRATCH/empty-version.yaml"

write single-quoted.yaml <<'EOF'
receivers:
  docker_stats/lab:
    api_version: ''
EOF
expect_exit_code "a single-quoted empty api_version passes" 0 "$CHECK" "$SCRATCH/single-quoted.yaml"

write missing.yaml <<'EOF'
receivers:
  docker_stats:
    endpoint: unix:///var/run/docker.sock
    collection_interval: 60s
service:
  pipelines: {}
EOF
expect_exit_code "a docker_stats block without api_version fails" 1 "$CHECK" "$SCRATCH/missing.yaml"
expect_output "the file is named" "missing.yaml"

write pinned.yaml <<'EOF'
receivers:
  docker_stats:
    api_version: "1.44"
EOF
expect_exit_code "a pinned 1.44 fails" 1 "$CHECK" "$SCRATCH/pinned.yaml"

write other-receiver.yaml <<'EOF'
receivers:
  docker_stats:
    endpoint: unix:///var/run/docker.sock
  other:
    api_version: ""
EOF
expect_exit_code "an api_version under another receiver does not count" 1 "$CHECK" "$SCRATCH/other-receiver.yaml"

write no-docker.yaml <<'EOF'
receivers:
  psi: {}
EOF
expect_exit_code "a config without docker_stats passes" 0 "$CHECK" "$SCRATCH/no-docker.yaml"

write two-blocks.yaml <<'EOF'
receivers:
  docker_stats:
    api_version: ""
  docker_stats/second:
    endpoint: unix:///var/run/docker.sock
EOF
expect_exit_code "a second block without api_version fails" 1 "$CHECK" "$SCRATCH/two-blocks.yaml"

expect_exit_code "a missing file is a usage error" 2 "$CHECK" "$SCRATCH/absent.yaml"

[ "$failures" -eq 0 ]
