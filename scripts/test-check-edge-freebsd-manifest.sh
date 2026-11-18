#!/usr/bin/env bash
# Tests check-edge-freebsd-manifest.sh on the committed manifests and on
# scratch copies with one component added or removed. No network.
# Usage: test-check-edge-freebsd-manifest.sh
# Exit: 0 all pass, 1 a case failed.
set -euo pipefail
unset CDPATH

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly ROOT
readonly CHECK="$ROOT/scripts/check-edge-freebsd-manifest.sh"
readonly FREEBSD="$ROOT/builder-edge-freebsd.yaml"
readonly STATIC="$ROOT/builder-edge-static.yaml"
SCRATCH="$(mktemp -d)"
readonly SCRATCH
trap 'rm -rf "$SCRATCH"' EXIT

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
	if grep -q -- "$2" "$SCRATCH/out"; then
		echo "PASS: $1"
	else
		echo "FAIL: $1 (no match for $2)"
		sed 's/^/    /' "$SCRATCH/out"
		failures=$((failures + 1))
	fi
}

expect_exit_code "the committed manifests pass" 0 "$CHECK"
expect_output "success output says PASS" "^PASS"

cp "$FREEBSD" "$SCRATCH/extra.yaml"
sed -i.bak '/^exporters:/a\
  - gomod: go.opentelemetry.io/collector/exporter/nopexporter v0.164.0' "$SCRATCH/extra.yaml"
expect_exit_code "an unlisted added component fails" 1 "$CHECK" "$SCRATCH/extra.yaml" "$STATIC"
expect_output "the added component is named" "nopexporter"

grep -v "filelogreceiver" "$FREEBSD" >"$SCRATCH/missing.yaml"
expect_exit_code "a component missing from the FreeBSD manifest fails" 1 "$CHECK" "$SCRATCH/missing.yaml" "$STATIC"
expect_output "the removed filelog is named" "unexpected removal: .*filelogreceiver"

for name in journaldreceiver dockerstatsreceiver k8sattributesprocessor psireceiver ethtoolreceiver pvereceiver snmpreceiver netflowreceiver receivercreator mongodbreceiver mysqlreceiver nginxreceiver postgresqlreceiver rabbitmqreceiver redisreceiver iscsireceiver smartreceiver idracreceiver megaraidreceiver inventoryreceiver ksmreceiver; do
	cp "$FREEBSD" "$SCRATCH/with-$name.yaml"
	case "$name" in
	*processor) section=processors ;;
	*) section=receivers ;;
	esac
	case "$name" in
	pvereceiver | psireceiver | ethtoolreceiver | iscsireceiver | smartreceiver | idracreceiver | megaraidreceiver | inventoryreceiver | ksmreceiver)
		module="github.com/vladistan/otelcol-custom/${section%s}/$name v0.0.0"
		;;
	*)
		module="github.com/open-telemetry/opentelemetry-collector-contrib/${section%s}/$name v0.164.1"
		;;
	esac
	sed -i.bak "/^$section:/a\\
  - gomod: $module" "$SCRATCH/with-$name.yaml"
	expect_exit_code "$name on the FreeBSD edge fails" 1 "$CHECK" "$SCRATCH/with-$name.yaml" "$STATIC"
	expect_output "$name is named as a missing removal" "expected removal is missing: .*$name"

	grep -v "$name" "$STATIC" >"$SCRATCH/static-no-$name.yaml"
	expect_exit_code "$name no longer in static fails" 1 "$CHECK" "$FREEBSD" "$SCRATCH/static-no-$name.yaml"
	expect_output "the lost removal of $name is named" "expected removal is missing: .*$name"
done

# hwschema is the library idrac builds on: its replace line leaves with idrac.
grep -v "receiver/hwschema" "$STATIC" >"$SCRATCH/static-no-hwschema.yaml"
expect_exit_code "hwschema no longer removed from static fails" 1 "$CHECK" "$FREEBSD" "$SCRATCH/static-no-hwschema.yaml"
expect_output "the missing hwschema removal is named" "expected removal is missing: .*hwschema"

# The patched netflow receiver is a local copy: its replace line leaves with the receiver.
grep -v "netflowreceiver =>" "$STATIC" >"$SCRATCH/static-no-netflow-replace.yaml"
expect_exit_code "the netflow replace no longer removed from static fails" 1 "$CHECK" "$FREEBSD" "$SCRATCH/static-no-netflow-replace.yaml"
expect_output "the missing netflow replace removal is named" "expected removal is missing: replace:.*netflowreceiver"

grep -v "hwmonreceiver" "$FREEBSD" >"$SCRATCH/no-hwmon.yaml"
expect_exit_code "a FreeBSD manifest without hwmon fails" 1 "$CHECK" "$SCRATCH/no-hwmon.yaml" "$STATIC"
expect_output "the missing hwmon is named" "unexpected removal: .*hwmonreceiver"

grep -q "zfskstatreceiver" "$FREEBSD" && echo "PASS: the FreeBSD manifest lists zfskstatreceiver" || {
	echo "FAIL: the FreeBSD manifest does not list zfskstatreceiver"
	failures=$((failures + 1))
}

for other in builder-config.yaml builder-edge-windows.yaml builder-gateway.yaml builder-scrape.yaml; do
	if grep -q "builder-edge-freebsd\|edge-freebsd" "$ROOT/$other"; then
		echo "FAIL: $other mentions the FreeBSD build"
		failures=$((failures + 1))
	else
		echo "PASS: $other does not mention the FreeBSD build"
	fi
done

expect_exit_code "a missing manifest is a usage error" 2 "$CHECK" "$SCRATCH/absent.yaml"
expect_output "usage error names the file" "absent.yaml"

[ "$failures" -eq 0 ]
