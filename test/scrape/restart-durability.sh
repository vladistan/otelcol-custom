#!/usr/bin/env bash
# Runs the scrape restart-durability check: CloudTrail
# objects go through moto (S3 and SQS) into a scratch otelcol-scrape container with
# a named volume; the OTLP sink refuses requests, the container is killed and
# started again, then the sink accepts and every record must arrive. Logs and
# zpages are scanned for the fake credential values. A second test kills the
# collector right after the receiver deletes its SQS messages and expects no loss;
# a control with a slow batch processor ahead of the queue must lose records.
#
# Usage: test/scrape/restart-durability.sh
# Needs: Docker (linux/amd64), network access to pull motoserver/moto and alpine.
# Exit: 0 the check passed, 1 it failed.
set -euo pipefail
unset CDPATH

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
readonly ROOT

"$ROOT/test/scrape/validate-scrape.sh"
(cd "$ROOT/build/scratch-scrape" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o otelcol-scrape-linux .)
SCRAPE_LINUX_BIN="$ROOT/build/scratch-scrape/otelcol-scrape-linux" \
	go -C "$ROOT/test/scrape" test -tags durability -run 'Test(RestartDurability|KillAtIntake)' -count=1 -v ./...
