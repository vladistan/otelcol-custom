#!/usr/bin/env bash
# Fails unless every image manifest (builder-*.yaml) lists the logshape processor under "processors:" and replaces
# it and each local module its go.mod replaces, so no image drops the processor or cannot build it.
# The helper modules come from the replace lines of processor/logshapeprocessor/go.mod.
# A replace line looks like:
#   - github.com/x/y => ../../processor/y
#
# Usage: check-logshape-in-builds.sh [DIR]   (DIR holds builder-*.yaml and processor/logshapeprocessor/go.mod)
# Exit: 0 PASS, 1 a manifest lacks the processor or a replace, 2 usage error or unreadable input.
set -euo pipefail
unset CDPATH

readonly PROGRAM="${0##*/}"
readonly PREFIX="github.com/vladistan/otelcol-custom/"
readonly PROCESSOR="${PREFIX}processor/logshapeprocessor"

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

gomod="$dir/processor/logshapeprocessor/go.mod"
[ -r "$gomod" ] || die "cannot read $gomod"

# builder-config.yaml is the local default build, not one of the shipped images, so it is skipped.
manifests=()
for manifest in "$dir"/builder-*.yaml; do
	[ -e "$manifest" ] || continue
	[ "${manifest##*/}" != "builder-config.yaml" ] || continue
	manifests+=("$manifest")
done
[ "${#manifests[@]}" -gt 0 ] || die "no builder-*.yaml in $dir"

# The processor itself, then every local module its go.mod replaces.
required=("$PROCESSOR")
while read -r module; do
	required+=("$module")
done < <(sed -nE "s#^replace[[:space:]]+(${PREFIX//./\\.}[^[:space:]]+)[[:space:]]+=>.*#\1#p" "$gomod")
[ "${#required[@]}" -gt 1 ] || die "$gomod replaces no local module; nothing to check"

failed=0
for manifest in "${manifests[@]}"; do
	name="${manifest##*/}"
	missing=()
	if ! sed -n '/^processors:/,/^[a-z_]*:/p' "$manifest" |
		grep -E -- "^[[:space:]]*-[[:space:]]+gomod:[[:space:]]+${PROCESSOR//./\\.}[[:space:]]" >/dev/null; then
		missing+=("the processors entry for $PROCESSOR")
	fi
	for module in "${required[@]}"; do
		if ! grep -Eq -- "^[[:space:]]*-[[:space:]]+${module//./\\.}[[:space:]]+=>[[:space:]]+\.\./\.\./" "$manifest"; then
			missing+=("the replace of $module")
		fi
	done
	if [ "${#missing[@]}" -eq 0 ]; then
		echo "PASS: $name lists logshapeprocessor and replaces ${#required[@]} modules"
	else
		for item in "${missing[@]}"; do
			echo "FAIL: $name lacks $item"
		done
		failed=1
	fi
done

if [ "$failed" -ne 0 ]; then
	exit 1
fi
echo "PASS: ${#manifests[@]} manifests build logshapeprocessor"
