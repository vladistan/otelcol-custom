#!/usr/bin/env bash
# Smoke test for the published otelcol-scrape image.
# Runs the image with one source (auth0) and fake AWS credentials, then checks
# the version, the health endpoint, that the collector survives the failing
# receiver, and a clean shutdown. The config is the Auth0 source of
# test/configs/scrape.yaml with every other source and pipeline removed,
# because a pipeline without credentials must not be deployed.
#
# Usage: test/scrape/smoke.sh
# Env: SCRAPE_IMAGE overrides the image (default ghcr.io/vladistan/otelcol-scrape:v0.3.10).
#      WANT_VERSION is the version `--version` must print (default: the image tag).
#      SMOKE_LOG is the log file (default build/scrape-smoke.log).
# Needs Docker and a network that can pull the image.
# Exit: 0 all checks pass, 1 a check failed.
set -euo pipefail
unset CDPATH

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
readonly ROOT
readonly IMAGE="${SCRAPE_IMAGE:-ghcr.io/vladistan/otelcol-scrape:v0.3.10}"
readonly WANT_VERSION="${WANT_VERSION:-${IMAGE##*:v}}"
readonly LOG="${SMOKE_LOG:-$ROOT/build/scrape-smoke.log}"
readonly NAME="scrape-smoke-$$"

work="$(mktemp -d)"
cleanup() {
	docker rm -f "$NAME" >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT

fail() {
	echo "FAIL: $*" | tee -a "$LOG" >&2
	exit 1
}

mkdir -p "$(dirname "$LOG")"
: >"$LOG"

cat >"$work/config.yaml" <<'EOF'
extensions:
  health_check:
    endpoint: 0.0.0.0:13133
  file_storage:
    directory: /tmp/otel-storage
    create_directory: true
receivers:
  auth0:
    queue_url: ${env:AUTH0_SQS_URL}
    region: ${env:AUTH0_SQS_REGION}
processors:
  memory_limiter:
    check_interval: 1s
    limit_percentage: 75
    spike_limit_percentage: 20
exporters:
  otlp_http:
    endpoint: ${env:OTEL_MAIN_OTLP_ENDPOINT}
    sending_queue:
      enabled: true
      queue_size: 10000
      storage: file_storage
service:
  extensions: [health_check, file_storage]
  pipelines:
    logs/aws:
      receivers: [auth0]
      processors: [memory_limiter]
      exporters: [otlp_http]
EOF
chmod 755 "$work"
chmod 644 "$work/config.yaml"

echo "== 1. version" | tee -a "$LOG"
version_out="$(docker run --rm "$IMAGE" --version 2>&1)"
echo "$version_out" >>"$LOG"
grep -Fq "version ${WANT_VERSION}" <<<"$version_out" || fail "--version does not print ${WANT_VERSION}"
echo "ok: --version prints ${WANT_VERSION}" | tee -a "$LOG"

echo "== 2. start with one source and fake credentials" | tee -a "$LOG"
fake_id="smoke-fake-id"
fake_val="smoke-fake-val"
docker run -d --name "$NAME" -p 127.0.0.1::13133 \
	-v "$work":/etc/otel:ro \
	-e OTEL_MAIN_OTLP_ENDPOINT=http://otel-main.example:4318 \
	-e AUTH0_SQS_URL=https://sqs.eu-central-1.amazonaws.com/111122223333/auth0 \
	-e AUTH0_SQS_REGION=eu-central-1 \
	-e AWS_ACCESS_KEY_ID="$fake_id" -e AWS_SECRET_ACCESS_KEY="$fake_val" \
	"$IMAGE" >/dev/null
port="$(docker port "$NAME" 13133/tcp | head -n1 | sed 's/.*://')"

echo "== 3. health endpoint answers 200" | tee -a "$LOG"
code=000
for _ in $(seq 1 30); do
	code="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${port}/" || true)"
	[ "$code" = 200 ] && break
	sleep 1
done
[ "$code" = 200 ] || fail "health endpoint answered ${code}"
echo "ok: health endpoint answered 200" | tee -a "$LOG"

echo "== 4. receiver error is logged and the collector stays up" | tee -a "$LOG"
found=
for _ in $(seq 1 60); do
	logs="$(docker logs "$NAME" 2>&1)"
	if grep -Fq "auth0 poll failed" <<<"$logs"; then
		found=1
		break
	fi
	sleep 1
done
echo "$logs" >>"$LOG"
[ -n "$found" ] || fail "no 'auth0 poll failed' line within 60 s"
[ "$(docker inspect -f '{{.State.Running}}' "$NAME")" = true ] || fail "collector exited after the receiver error"
sleep 5
[ "$(docker inspect -f '{{.State.Running}}' "$NAME")" = true ] || fail "collector exited 5 s after the receiver error"
echo "ok: 'auth0 poll failed' logged, collector still running" | tee -a "$LOG"

echo "== 5. clean shutdown" | tee -a "$LOG"
docker stop -t 30 "$NAME" >/dev/null
docker logs "$NAME" >>"$LOG" 2>&1
exit_code="$(docker inspect -f '{{.State.ExitCode}}' "$NAME")"
[ "$exit_code" = 0 ] || fail "exit code ${exit_code} after SIGTERM"
echo "ok: exit code 0 after SIGTERM" | tee -a "$LOG"
echo "PASS: ${IMAGE} smoke test; log in ${LOG}" | tee -a "$LOG"
