#!/usr/bin/env bash
# Checks the FreeBSD edge deliverables without starting any binary: the
# TrueNAS CORE sample config, the release recipes, the checksum and site
# listing of the FreeBSD asset, the build check on a tiny cross-built program,
# and the docs and CHANGELOG text. No network, no Docker.
# Usage: test-freebsd-edge.sh
# Exit: 0 all pass, 1 a case failed.
set -euo pipefail
unset CDPATH

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly ROOT
cd "$ROOT"
VERSION_NOW="$(tr -d '\n' <VERSION)"
readonly VERSION_NOW
readonly CONFIG="$ROOT/test/configs/truenas-core-edge.yaml"
readonly DOC="$ROOT/docs/freebsd-edge.md"
readonly ASSET="otelcol-edge-$VERSION_NOW-freebsd-amd64"
SCRATCH="$(mktemp -d)"
readonly SCRATCH
trap 'rm -rf "$SCRATCH"' EXIT

failures=0
pass() { echo "PASS: $1"; }
fail() {
	echo "FAIL: $1"
	failures=$((failures + 1))
}

# expect_grep NAME PATTERN FILE
expect_grep() {
	if grep -Eq -- "$2" "$3"; then pass "$1"; else fail "$1 (no match for $2 in ${3##*/})"; fi
}
# expect_no_grep NAME PATTERN FILE
expect_no_grep() {
	if grep -Eq -- "$2" "$3"; then fail "$1 (found $2 in ${3##*/})"; else pass "$1"; fi
}

# Reference config.
for pass_dev in pass0 pass1 pass2 pass3 pass4 pass5; do
	expect_grep "disk exclude lists $pass_dev" "devices:.*\b$pass_dev\b" "$CONFIG"
done
for fs in devfs fdescfs nullfs tmpfs; do
	expect_grep "filesystem exclude lists $fs" "fs_types:.*\b$fs\b" "$CONFIG"
done
expect_grep "network exclude lists pflog0*" 'interfaces:.*pflog0' "$CONFIG"
grep -v "^[[:space:]]*#" "$CONFIG" >"$SCRATCH/config-nocomments"
for gone in journald psi ethtool docker_stats k8s_attributes; do
	expect_no_grep "config has no $gone" "^[[:space:]]*(#.*)?[[:space:]]*$gone(/[a-z0-9_-]+)?:|[ ,\[]$gone(/[a-z0-9_-]+)?[],]" "$SCRATCH/config-nocomments"
done
expect_grep "system.network.connections is disabled" 'system\.network\.connections:' "$CONFIG"
awk '/system\.network\.connections:/ { f = 1; next } f && /enabled:/ { print $2; exit }' "$CONFIG" >"$SCRATCH/conn"
if [ "$(cat "$SCRATCH/conn")" = false ]; then pass "connections enabled is false"; else fail "connections enabled is not false"; fi
expect_grep "filelog reads /var/log/messages" 'include:.*/var/log/messages' "$CONFIG"
expect_grep "zfskstat uses the sysctl source" '^[[:space:]]+source:[[:space:]]+sysctl' "$CONFIG"

# Release recipes list the asset.
just -n stage-public-artifacts >"$SCRATCH/stage" 2>&1 || true
expect_grep "the stage recipe copies the FreeBSD asset" "otelcol-edge-.*-freebsd-amd64" "$SCRATCH/stage"
just -n verify-public-artifacts >"$SCRATCH/verify" 2>&1 || true
expect_grep "the verify recipe checks the FreeBSD asset" "otelcol-edge-.*-freebsd-amd64" "$SCRATCH/verify"
expect_grep "the verify recipe runs the FreeBSD build check" 'check-edge-freebsd' "$SCRATCH/verify"
expect_no_grep "the verify recipe does not run the FreeBSD binary" 'docker.*run.*freebsd' "$SCRATCH/verify"
# The OCB step of every container build must not stamp VCS data (clean runners fail on it).
for recipe in build-edge-linux build-edge-static-linux build-edge-windows build-edge-freebsd; do
	just -n "$recipe" >"$SCRATCH/recipe-$recipe" 2>&1 || true
	expect_grep "$recipe passes GOFLAGS=-buildvcs=false to the container" 'GOCACHE=/tmp/gocache -e GOFLAGS=-buildvcs=false' "$SCRATCH/recipe-$recipe"
done

# Checksum and site listing of the asset.
mkdir -p "$SCRATCH/public"
printf 'freebsd bytes\n' >"$SCRATCH/public/$ASSET"
printf 'linux bytes\n' >"$SCRATCH/public/otelcol-edge-$VERSION_NOW-linux-amd64"
just --set dist_public_dir "$SCRATCH/public" checksum-public-artifacts >"$SCRATCH/sums.out" 2>&1 || fail "checksum recipe failed"
want_sum="$(cd "$SCRATCH/public" && sha256sum "$ASSET" | cut -d' ' -f1)"
expect_grep "SHA256SUMS holds the matching line for $ASSET" "^$want_sum  $ASSET\$" "$SCRATCH/public/SHA256SUMS"
scripts/assemble_site.sh assemble --assets "$SCRATCH/public" --out "$SCRATCH/site" --tag "v$VERSION_NOW" >"$SCRATCH/site.out" 2>&1 || fail "site assemble failed"
expect_grep "the site page lists $ASSET" "$ASSET" "$SCRATCH/site/index.html"
expect_grep "the site install snippet names the FreeBSD asset" 'chmod \+x .*otelcol-edge-\*-freebsd-amd64' "$SCRATCH/site/index.html"
expect_grep "the README has a FreeBSD install section" '^### FreeBSD edge \(freebsd/amd64\)' README.md
expect_grep "the release doc says a FreeBSD failure stops the release" 'FreeBSD asset blocks the release' docs/public-release.md
expect_grep "the vet step runs in the release workflow" 'just vet-edge-freebsd' .github/workflows/release.yml

# Build check on a tiny cross-built program.
mkdir -p "$SCRATCH/prog"
cat >"$SCRATCH/prog/main.go" <<'EOF'
package main

var version string

func main() { _ = version }
EOF
printf 'module probe\n\ngo 1.21\n' >"$SCRATCH/prog/go.mod"
(cd "$SCRATCH/prog" && GOOS=freebsd GOARCH=amd64 CGO_ENABLED=0 go build -buildvcs=false -ldflags "-X main.version=$VERSION_NOW" -o "$SCRATCH/probe-freebsd" .) || fail "cannot cross-build the probe"
(cd "$SCRATCH/prog" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -buildvcs=false -ldflags "-X main.version=$VERSION_NOW" -o "$SCRATCH/probe-linux" .) || fail "cannot build the Linux probe"
(cd "$SCRATCH/prog" && GOOS=freebsd GOARCH=amd64 CGO_ENABLED=0 go build -buildvcs=false -ldflags "-X main.version=0.0.0" -o "$SCRATCH/probe-badver" .) || fail "cannot build the bad-version probe"
if just check-edge-freebsd "$SCRATCH/probe-freebsd" >"$SCRATCH/check.out" 2>&1; then pass "check-edge-freebsd accepts a static FreeBSD build"; else
	fail "check-edge-freebsd rejects a static FreeBSD build"
	sed 's/^/    /' "$SCRATCH/check.out"
fi
expect_grep "the check output says the binary was not run" 'not run' "$SCRATCH/check.out"
expect_grep "the check output shows the file type" 'FreeBSD' "$SCRATCH/check.out"
if just check-edge-freebsd "$SCRATCH/probe-linux" >"$SCRATCH/check-linux.out" 2>&1; then fail "check-edge-freebsd accepts a Linux build"; else pass "check-edge-freebsd rejects a Linux build"; fi
if just check-edge-freebsd "$SCRATCH/probe-badver" >"$SCRATCH/check-ver.out" 2>&1; then fail "check-edge-freebsd accepts a wrong version"; else pass "check-edge-freebsd rejects a wrong version"; fi

# Docs and CHANGELOG.
expect_grep "docs name the install path" '/usr/local/bin/otelcol-edge' "$DOC"
expect_grep "docs name the rc.d service" 'otelcol_edge' "$DOC"
# The FreeBSD wording lives in the release that added the build, which a later release pushes down.
cp CHANGELOG.md "$SCRATCH/unreleased"
for name in journald psi ethtool; do
	expect_grep "CHANGELOG names $name as not running on FreeBSD" "FreeBSD.*\`$name\`|\`$name\`.*FreeBSD" "$SCRATCH/unreleased"
	expect_grep "docs name $name as not in the FreeBSD build" "\`$name\`.*Not in the build" "$DOC"
done

[ "$failures" -eq 0 ]
