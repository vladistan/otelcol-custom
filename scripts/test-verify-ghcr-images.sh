#!/usr/bin/env bash
# Tests verify-ghcr-images.sh with a stub docker in place of the real one.
# No Docker daemon, no network.
# Usage: test-verify-ghcr-images.sh
# Exit: 0 all pass, 1 a case failed.
set -euo pipefail
unset CDPATH

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly ROOT
readonly VERIFY="$ROOT/scripts/verify-ghcr-images.sh"
SCRATCH="$(mktemp -d)"
readonly SCRATCH
trap 'rm -rf "$SCRATCH"' EXIT

# The stub prints "<image name without tag> version <STUB_VERSION>" and exits
# STUB_EXIT, or prints nothing for an image listed in STUB_SILENT.
cat >"$SCRATCH/docker" <<'EOF'
#!/usr/bin/env bash
image=""
for arg in "$@"; do
	case "$arg" in
	ghcr.io/*) image="$arg" ;;
	esac
done
case " ${STUB_SILENT:-} " in
*" $image "*) exit "${STUB_EXIT:-0}" ;;
esac
name="${image##*/}"
echo "${name%%:*} version ${STUB_VERSION:-0.0.0}"
exit "${STUB_EXIT:-0}"
EOF
chmod +x "$SCRATCH/docker"

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
	if grep -qF -- "$2" "$SCRATCH/out"; then
		echo "PASS: $1"
	else
		echo "FAIL: $1 (no match for $2)"
		sed 's/^/    /' "$SCRATCH/out"
		failures=$((failures + 1))
	fi
}

readonly GATEWAY="ghcr.io/example/otelcol-gateway:v0.3.1"
readonly SCRAPE="ghcr.io/example/otelcol-scrape:v0.3.1"

expect_exit_code "both images report the version" 0 \
	env DOCKER="$SCRATCH/docker" STUB_VERSION=0.3.1 "$VERIFY" 0.3.1 "$GATEWAY" "$SCRAPE"
expect_output "pass output names the image" "PASS: $SCRAPE"

readonly EDGE="ghcr.io/example/otelcol-edge:v0.3.1"

expect_exit_code "the edge image is verified with the others" 0 \
	env DOCKER="$SCRATCH/docker" STUB_VERSION=0.3.1 "$VERIFY" 0.3.1 "$GATEWAY" "$SCRAPE" "$EDGE"
expect_output "pass output names the edge image" "PASS: $EDGE"

expect_exit_code "the edge image alone is verified" 0 \
	env DOCKER="$SCRATCH/docker" STUB_VERSION=0.3.1 "$VERIFY" 0.3.1 "$EDGE"

expect_exit_code "a silent edge image fails and the others still pass" 1 \
	env DOCKER="$SCRATCH/docker" STUB_VERSION=0.3.1 STUB_SILENT="$EDGE" "$VERIFY" 0.3.1 "$GATEWAY" "$EDGE"
expect_output "the edge failure is reported" "FAIL: $EDGE --version output lacks 0.3.1"
expect_output "the gateway still passes" "PASS: $GATEWAY"

expect_exit_code "an image with another version fails" 1 \
	env DOCKER="$SCRATCH/docker" STUB_VERSION=0.2.0 "$VERIFY" 0.3.1 "$GATEWAY" "$SCRAPE"
expect_output "failure output names the image" "FAIL: $GATEWAY"
expect_output "failure output names the wanted version" "lacks 0.3.1"

expect_exit_code "a failing docker run fails the check" 1 \
	env DOCKER="$SCRATCH/docker" STUB_VERSION=0.3.1 STUB_EXIT=125 "$VERIFY" 0.3.1 "$GATEWAY"
expect_output "docker failure message carries the exit code" "failed (exit 125)"

expect_exit_code "an image with no version output fails" 1 \
	env DOCKER="$SCRATCH/docker" STUB_VERSION=0.3.1 STUB_SILENT="$SCRAPE" "$VERIFY" 0.3.1 "$SCRAPE"
expect_output "the silent image is reported as lacking the version" "FAIL: $SCRAPE --version output lacks 0.3.1"

expect_exit_code "every image is checked after a failure" 1 \
	env DOCKER="$SCRATCH/docker" STUB_VERSION=0.3.1 STUB_SILENT="$GATEWAY" "$VERIFY" 0.3.1 "$GATEWAY" "$SCRAPE"
expect_output "the later image is still verified" "PASS: $SCRAPE"

expect_exit_code "no images is a usage error" 2 env DOCKER="$SCRATCH/docker" "$VERIFY" 0.3.1
expect_output "usage message" "usage:"
expect_exit_code "no arguments is a usage error" 2 env DOCKER="$SCRATCH/docker" "$VERIFY"
expect_output "no-arguments usage message" "usage:"

[ "$failures" -eq 0 ]
