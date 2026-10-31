#!/usr/bin/env bash
# Builds a scratch otelcol-scrape from builder-scrape.yaml and
# validates test/configs/scrape.yaml with placeholder env values.
# Compiles a collector, so it takes a few minutes.
#
# Usage: test/scrape/validate-scrape.sh
# Env: OCB_VERSION overrides the builder version (default 0.154.0).
# Exit: 0 the config validates, 1 validation or build failed.
set -euo pipefail
unset CDPATH

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
readonly ROOT
readonly OCB_VERSION="${OCB_VERSION:-0.154.0}"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
cd "$ROOT"
mkdir -p build
sed -E 's#^([[:space:]]*output_path:).*#\1 ./build/scratch-scrape#; /skip_compilation/d' \
	builder-scrape.yaml >"$work/builder.yaml"
go run "go.opentelemetry.io/collector/cmd/builder@v${OCB_VERSION}" --config "$work/builder.yaml"

export OTEL_MAIN_OTLP_ENDPOINT=http://otel-main.example:4318 AWS_REGION=us-east-1
export CLOUDTRAIL_SQS_URL=https://sqs.us-east-1.amazonaws.com/111122223333/cloudtrail
export S3ACCESS_BUCKET=access-bucket S3ACCESS_PREFIX=logs/
export S3ACCESS_SQS_URL=https://sqs.us-east-1.amazonaws.com/111122223333/s3access
export AUTH0_SQS_URL=https://sqs.eu-central-1.amazonaws.com/111122223333/auth0
export AUTH0_SQS_REGION=eu-central-1 AUTH0_ROLE_ARN= AUTH0_EXTERNAL_ID=
export GCP_PROJECT=proj-example
export GCP_CLOUDRUN_SUBSCRIPTION=projects/proj-example/subscriptions/cloudrun
export GCP_AUDIT_SUBSCRIPTION=projects/proj-example/subscriptions/audit
export AZ_EH_CONN='Endpoint=sb://example.servicebus.windows.net/;SharedAccessKeyName=placeholder-name;SharedAccessKey=placeholder-value;EntityPath=placeholder-hub'
build/scratch-scrape/otelcol-scrape validate --config test/configs/scrape.yaml
echo "PASS: test/configs/scrape.yaml validates"
