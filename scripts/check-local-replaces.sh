#!/usr/bin/env bash
# Fails unless every local replace in a builder-*.yaml points to a directory whose
# go.mod names the replaced module, and builder-scrape.yaml replaces the two
# vendored cloud receivers and the patched AWS logs encoding extension, and the two Linux edge
# manifests replace the patched netflow receiver. A replace line looks like:
#   - github.com/x/y => ../../receiver/y
# The path is relative to build/<name>, so "../../" is the tool directory.
#
# Usage: check-local-replaces.sh [DIR]   (DIR holds builder-*.yaml and the module directories)
# Exit: 0 PASS, 1 a replace is broken or missing, 2 usage error or a failed command.
set -euo pipefail
unset CDPATH

readonly PROGRAM="${0##*/}"
readonly CONTRIB="github.com/open-telemetry/opentelemetry-collector-contrib/receiver"
readonly REQUIRED_IN_SCRAPE=(
	"$CONTRIB/awscloudwatchreceiver"
	"$CONTRIB/googlecloudmonitoringreceiver"
	"github.com/vladistan/otelcol-custom/receiver/pvereceiver"
	"github.com/open-telemetry/opentelemetry-collector-contrib/extension/encoding/awslogsencodingextension"
)
# The Linux edge builds carry the patched copy of the contrib netflow receiver.
readonly REQUIRED_IN_EDGE=(
	"$CONTRIB/netflowreceiver"
)
readonly EDGE_MANIFESTS=(builder-edge.yaml builder-edge-static.yaml)
# Receivers the scrape image must list: the Proxmox VE and SNMP pollers run in the scrape pod.
readonly REQUIRED_RECEIVERS_IN_SCRAPE=(
	"github.com/vladistan/otelcol-custom/receiver/pvereceiver"
	"$CONTRIB/snmpreceiver"
)

die() {
	echo "$PROGRAM: $*" >&2
	exit 2
}

[ $# -le 1 ] || die "usage: $PROGRAM [DIR]"
if [ $# -eq 1 ]; then
	dir="$1"
else
	dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)" || die "cannot resolve the script directory"
fi
[ -d "$dir" ] || die "$dir is not a directory"

manifests=("$dir"/builder-*.yaml)
[ -e "${manifests[0]}" ] || die "no builder-*.yaml in $dir"

failed=0
checked=0
for manifest in "${manifests[@]}"; do
	name="${manifest##*/}"
	while read -r module target; do
		checked=$((checked + 1))
		path="$dir/${target#../../}"
		if [ ! -f "$path/go.mod" ]; then
			echo "FAIL: $name replaces $module with $target, which has no go.mod"
			failed=1
			continue
		fi
		have="$(sed -nE 's#^module[[:space:]]+([^[:space:]]+).*#\1#p' "$path/go.mod" | head -1)"
		if [ "$have" != "$module" ]; then
			echo "FAIL: $name replaces $module with $target, whose go.mod is module $have"
			failed=1
		fi
	done < <(sed -nE 's#^[[:space:]]*-[[:space:]]+([^[:space:]]+)[[:space:]]+=>[[:space:]]+(\.\./\.\./[^[:space:]]+)[[:space:]]*$#\1 \2#p' "$manifest")
done

scrape="$dir/builder-scrape.yaml"
if [ -f "$scrape" ]; then
	for module in "${REQUIRED_IN_SCRAPE[@]}"; do
		if ! grep -Eq -- "^[[:space:]]*-[[:space:]]+${module//./\\.}[[:space:]]+=>[[:space:]]+\.\./\.\./" "$scrape"; then
			echo "FAIL: builder-scrape.yaml does not replace $module with a local copy"
			failed=1
		fi
	done
fi

for edge in "${EDGE_MANIFESTS[@]}"; do
	if [ -f "$dir/$edge" ]; then
		for module in "${REQUIRED_IN_EDGE[@]}"; do
			if ! grep -Eq -- "^[[:space:]]*-[[:space:]]+${module//./\\.}[[:space:]]+=>[[:space:]]+\.\./\.\./" "$dir/$edge"; then
				echo "FAIL: $edge does not replace $module with a local copy"
				failed=1
			fi
		done
	fi
done

if [ -f "$scrape" ]; then
	for module in "${REQUIRED_RECEIVERS_IN_SCRAPE[@]}"; do
		if ! grep -Eq -- "^[[:space:]]*-[[:space:]]+gomod:[[:space:]]+${module//./\\.}[[:space:]]+" "$scrape"; then
			echo "FAIL: builder-scrape.yaml does not list the receiver $module"
			failed=1
		fi
	done
fi

if [ "$failed" -ne 0 ]; then
	exit 1
fi
echo "PASS: $checked local replaces in ${#manifests[@]} manifests name their modules"
