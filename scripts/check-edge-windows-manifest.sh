#!/usr/bin/env bash
# Guards builder-edge-windows.yaml: its components may differ from
# builder-edge-static.yaml only by the REMOVED and ADDED lists below. Later
# integration steps extend ADDED (4.1 winlogprocessor, 6.1 windowhistprocessor).
# Components are the "gomod:" module paths and the "replace:" left sides.
#
# Usage: check-edge-windows-manifest.sh [WINDOWS_MANIFEST [STATIC_MANIFEST]]
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
	"$LOCAL/receiver/psireceiver"
	"replace:$LOCAL/receiver/psireceiver"
)
readonly ADDED=(
	"$CONTRIB/receiver/windowseventlogreceiver"
	"$CONTRIB/receiver/windowsperfcountersreceiver"
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

windows_manifest="${1:-$ROOT/builder-edge-windows.yaml}"
static_manifest="${2:-$ROOT/builder-edge-static.yaml}"
[ $# -le 2 ] || die "usage: $PROGRAM [WINDOWS_MANIFEST [STATIC_MANIFEST]]"
[ -f "$windows_manifest" ] || die "$windows_manifest does not exist"
[ -f "$static_manifest" ] || die "$static_manifest does not exist"

windows_components="$(list_components "$windows_manifest")" || die "cannot read $windows_manifest"
static_components="$(list_components "$static_manifest")" || die "cannot read $static_manifest"
removed_components="$(comm -13 <(printf '%s\n' "$windows_components") <(printf '%s\n' "$static_components"))"
added_components="$(comm -13 <(printf '%s\n' "$static_components") <(printf '%s\n' "$windows_components"))"

status=0
report() {
	local label="$1" actual="$2" allowed="$3" line
	while IFS= read -r line; do
		[ -n "$line" ] || continue
		echo "FAIL: unexpected $label: $line"
		status=1
	done < <(comm -23 <(printf '%s\n' "$actual") <(printf '%s\n' "$allowed"))
	while IFS= read -r line; do
		[ -n "$line" ] || continue
		echo "FAIL: expected $label is missing: $line"
		status=1
	done < <(comm -13 <(printf '%s\n' "$actual") <(printf '%s\n' "$allowed"))
}
report "removal" "$removed_components" "$(sort_lines "${REMOVED[@]}")"
report "addition" "$added_components" "$(sort_lines "${ADDED[@]}")"

[ "$status" -eq 0 ] && echo "PASS: edge-windows differs from edge-static only by the allowed list"
exit "$status"
