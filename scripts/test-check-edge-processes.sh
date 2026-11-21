#!/usr/bin/env bash
# Tests check-edge-processes.sh on fixture configs with a known result, on the committed Linux edge samples,
# and checks the docs and CHANGELOG text about the processes scraper. The fixtures state their expected result;
# nothing here repeats the logic of the guard. No network.
# Usage: test-check-edge-processes.sh
# Exit: 0 all pass, 1 a case failed.
set -euo pipefail
unset CDPATH

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly ROOT
readonly CHECK="$ROOT/scripts/check-edge-processes.sh"
SCRATCH="$(mktemp -d)"
readonly SCRATCH
trap 'rm -rf "$SCRATCH"' EXIT

# The Linux telemetry doc; found by glob so this script holds no internal vocabulary.
TELEMETRY_DOC="$(find "$ROOT/docs" -maxdepth 1 -name 'linux-telemetry-*.md' | head -1)"
readonly TELEMETRY_DOC

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

expect_grep() {
	if grep -Eq -- "$2" "$3"; then echo "PASS: $1"; else
		echo "FAIL: $1 (no match for $2 in ${3##*/})"
		failures=$((failures + 1))
	fi
}

expect_no_grep() {
	if grep -Eq -- "$2" "$3"; then
		echo "FAIL: $1 (found $2 in ${3##*/})"
		failures=$((failures + 1))
	else echo "PASS: $1"; fi
}

expect_exit_code "the committed Linux edge samples pass" 0 "$CHECK"
expect_output "success output says PASS" "^PASS"

# fixture NAME EXPECTED_EXIT TEXT: a whole config in a file, checked against the expected exit code.
fixture() {
	printf '%s\n' "$3" >"$SCRATCH/$1.yaml"
	expect_exit_code "$1" "$2" "$CHECK" "$SCRATCH/$1.yaml"
}

fixture pass-block-form 0 'receivers:
  host_metrics:
    collection_interval: 60s
    scrapers:
      cpu:
      processes:
service:
  pipelines:
    metrics:
      receivers: [host_metrics]
      exporters: [debug]'

fixture pass-flow-form 0 'receivers:
  host_metrics:
    scrapers:
      cpu:
      processes: {}
service:
  pipelines:
    metrics:
      receivers: [psi, host_metrics, docker_stats]
      exporters: [debug]'

fixture pass-four-space-indent 0 'receivers:
    host_metrics:
        scrapers:
            cpu:
            processes: {}
service:
    pipelines:
        metrics/main:
            receivers: [host_metrics]'

fixture pass-comments-on-keys 0 'receivers:
  host_metrics: # the host
    scrapers: # which
      cpu:
      processes: {} # on
service:
  pipelines:
    metrics:
      receivers: [host_metrics] # the one'

fixture pass-block-list-receivers 0 'receivers:
  host_metrics:
    scrapers:
      processes:
service:
  pipelines:
    metrics:
      receivers:
        - psi
        - host_metrics # first-class
      exporters: [debug]'

fixture fail-no-processes 1 'receivers:
  host_metrics:
    scrapers:
      cpu:
      memory:
service:
  pipelines:
    metrics:
      receivers: [host_metrics]'
expect_output "the failing file and the scraper are named" "fail-no-processes.yaml.*processes scraper"

fixture fail-process-not-processes 1 'receivers:
  host_metrics:
    scrapers:
      process:
service:
  pipelines:
    metrics:
      receivers: [host_metrics]'

fixture fail-only-the-second-block 1 'receivers:
  host_metrics:
    scrapers:
      cpu:
  host_metrics/process_hourly:
    scrapers:
      processes:
service:
  pipelines:
    metrics:
      receivers: [host_metrics]'

fixture fail-commented-out 1 'receivers:
  host_metrics:
    scrapers:
      cpu:
      # processes:
service:
  pipelines:
    metrics:
      receivers: [host_metrics]'

fixture fail-no-host-metrics 1 'receivers:
  otlp:
service:
  pipelines:
    metrics:
      receivers: [otlp]'

fixture fail-not-in-a-pipeline 1 'receivers:
  host_metrics:
    scrapers:
      processes:
  psi:
service:
  pipelines:
    metrics:
      receivers: [psi]'
expect_output "the missing pipeline is named" "metrics pipeline"

fixture fail-only-a-logs-pipeline 1 'receivers:
  host_metrics:
    scrapers:
      processes:
service:
  pipelines:
    logs:
      receivers: [host_metrics]'

fixture fail-only-another-instance-in-the-pipeline 1 'receivers:
  host_metrics:
    scrapers:
      processes:
  host_metrics/process_hourly:
    scrapers:
      process:
service:
  pipelines:
    metrics:
      receivers: [host_metrics/process_hourly]'

fixture fail-no-service 1 'receivers:
  host_metrics:
    scrapers:
      processes:'

expect_exit_code "a missing file is a usage error" 2 "$CHECK" "$SCRATCH/absent.yaml"

# Docs and CHANGELOG: the entry stays in the release section it shipped in, so the whole file is read.
readonly CHANGELOG="$ROOT/CHANGELOG.md"
expect_grep "the CHANGELOG names the processes scraper" '`processes` scraper' "$CHANGELOG"
expect_grep "the CHANGELOG says the series reached ClickHouse from one host" 'reached ClickHouse from one host' "$CHANGELOG"
expect_grep "the CHANGELOG names system.processes.count and created" 'system\.processes\.count.*system\.processes\.created' "$CHANGELOG"

# All three phrases must be present; the second case proves the check still fails without them.
changelog_phrases() {
	grep -Eq -- '`processes` scraper' "$1" &&
		grep -Eq -- 'reached ClickHouse from one host' "$1" &&
		grep -Eq -- 'system\.processes\.count.*system\.processes\.created' "$1"
}
grep -Ev -- '`processes` scraper|reached ClickHouse from one host|system\.processes\.count' "$CHANGELOG" >"$SCRATCH/changelog-stripped" || true
expect_exit_code "the CHANGELOG phrases are found in the committed file" 0 changelog_phrases "$CHANGELOG"
expect_exit_code "the CHANGELOG phrases are missing once the entry is removed" 1 changelog_phrases "$SCRATCH/changelog-stripped"
grep 'system\.processes\.count' "$TELEMETRY_DOC" >"$SCRATCH/row" || true
expect_grep "the telemetry doc lists system.processes.count and created in one row" 'system\.processes\.count.*system\.processes\.created' "$SCRATCH/row"
expect_grep "the telemetry doc gives the type as sum" '\| sum \|' "$SCRATCH/row"
expect_no_grep "the telemetry doc does not call them gauges" 'gauge' "$SCRATCH/row"
expect_grep "the telemetry doc says count is not monotonic and created is" 'not monotonic.*monotonic' "$SCRATCH/row"

[ "$failures" -eq 0 ]
