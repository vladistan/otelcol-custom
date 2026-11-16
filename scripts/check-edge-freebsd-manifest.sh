#!/usr/bin/env bash
# Guards builder-edge-freebsd.yaml: its components may differ from
# builder-edge-static.yaml only by the REMOVED list below and adds nothing. The
# list is explicit on purpose: a component that joins the
# static edge fails this guard until someone decides for FreeBSD, by adding it
# to builder-edge-freebsd.yaml (it runs there) or to REMOVED (it does not).
# Deriving REMOVED from the static manifest would make the check pass whatever
# the difference is.
# Components are the "gomod:" module paths and the "replace:" left sides.
#
# Usage: check-edge-freebsd-manifest.sh [FREEBSD_MANIFEST [STATIC_MANIFEST]]
# Exit: 0 PASS, 1 unexpected difference, 2 usage error or a failed command.
set -euo pipefail
unset CDPATH

readonly PROGRAM="${0##*/}"
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)" || {
	echo "$PROGRAM: cannot resolve the script directory" >&2
	exit 2
}
readonly ROOT
readonly CONTRIB="github.com/open-telemetry/opentelemetry-collector-contrib"
readonly LOCAL="github.com/vladistan/otelcol-custom"
readonly REMOVED=(
	"$CONTRIB/receiver/journaldreceiver"
	"$CONTRIB/receiver/dockerstatsreceiver"
	"$CONTRIB/processor/k8sattributesprocessor"
	"$LOCAL/receiver/psireceiver"
	"replace:$LOCAL/receiver/psireceiver"
	"$LOCAL/receiver/ethtoolreceiver"
	"replace:$LOCAL/receiver/ethtoolreceiver"
	"$LOCAL/receiver/pvereceiver"
	"replace:$LOCAL/receiver/pvereceiver"
	"$CONTRIB/receiver/snmpreceiver"
	"$CONTRIB/receiver/netflowreceiver"
	"$CONTRIB/extension/observer/k8sobserver"
	"$CONTRIB/receiver/receivercreator"
	"$CONTRIB/receiver/mongodbreceiver"
	"$CONTRIB/receiver/mysqlreceiver"
	"$CONTRIB/receiver/nginxreceiver"
	"$CONTRIB/receiver/postgresqlreceiver"
	"$CONTRIB/receiver/rabbitmqreceiver"
	"$CONTRIB/receiver/redisreceiver"
	"$LOCAL/receiver/iscsireceiver"
	"replace:$LOCAL/receiver/iscsireceiver"
	"$LOCAL/receiver/smartreceiver"
	"replace:$LOCAL/receiver/smartreceiver"
	"replace:$LOCAL/receiver/smartdecode"
	"$LOCAL/receiver/idracreceiver"
	"replace:$LOCAL/receiver/idracreceiver"
	"$LOCAL/receiver/megaraidreceiver"
	"replace:$LOCAL/receiver/megaraidreceiver"
	"replace:$LOCAL/receiver/hwschema"
)

die() {
	echo "$PROGRAM: $*" >&2
	exit 2
}

list_components() {
	sed -nE \
		-e 's/^[[:space:]]*-[[:space:]]+gomod:[[:space:]]+([^[:space:]]+).*/\1/p' \
		-e 's/^[[:space:]]*-[[:space:]]+([^[:space:]]+)[[:space:]]+=>.*/replace:\1/p' "$1" |
		LC_ALL=C sort -u
}

sort_lines() {
	printf '%s\n' "$@" | LC_ALL=C sort -u
}

freebsd_manifest="${1:-$ROOT/builder-edge-freebsd.yaml}"
static_manifest="${2:-$ROOT/builder-edge-static.yaml}"
[ $# -le 2 ] || die "usage: $PROGRAM [FREEBSD_MANIFEST [STATIC_MANIFEST]]"
[ -f "$freebsd_manifest" ] || die "$freebsd_manifest does not exist"
[ -f "$static_manifest" ] || die "$static_manifest does not exist"

freebsd_components="$(list_components "$freebsd_manifest")" || die "cannot read $freebsd_manifest"
static_components="$(list_components "$static_manifest")" || die "cannot read $static_manifest"
removed_components="$(comm -13 <(printf '%s\n' "$freebsd_components") <(printf '%s\n' "$static_components"))"
added_components="$(comm -13 <(printf '%s\n' "$static_components") <(printf '%s\n' "$freebsd_components"))"

status=0
# unexpected: in actual, not in allowed. missing: in allowed, not in actual.
unexpected() {
	local label="$1" actual="$2" allowed="$3" line
	while IFS= read -r line; do
		[ -n "$line" ] || continue
		echo "FAIL: unexpected $label: $line"
		status=1
	done < <(comm -23 <(printf '%s\n' "$actual") <(printf '%s\n' "$allowed"))
}
missing() {
	local label="$1" actual="$2" allowed="$3" line
	while IFS= read -r line; do
		[ -n "$line" ] || continue
		echo "FAIL: expected $label is missing: $line"
		status=1
	done < <(comm -13 <(printf '%s\n' "$actual") <(printf '%s\n' "$allowed"))
}
removed_expected="$(sort_lines "${REMOVED[@]}")"
unexpected "removal" "$removed_components" "$removed_expected"
missing "removal" "$removed_components" "$removed_expected"
unexpected "addition" "$added_components" ""

[ "$status" -eq 0 ] && echo "PASS: edge-freebsd differs from edge-static only by the allowed list"
exit "$status"
