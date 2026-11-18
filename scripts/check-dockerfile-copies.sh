#!/usr/bin/env bash
# Fails unless each Dockerfile that builds from a builder-*.yaml copies every top-level directory
# that manifest replaces modules from. A replace line looks like:
#   - github.com/x/y => ../../receiver/y
# and the build stage needs "COPY receiver ./receiver" for it, or OCB cannot read the module.
# A Dockerfile that copies no builder manifest (a prebuilt binary image) is skipped.
#
# Usage: check-dockerfile-copies.sh [DIR]   (DIR holds Dockerfile.* and builder-*.yaml)
# Exit: 0 PASS, 1 a copy is missing, 2 usage error or nothing to check.
set -euo pipefail
unset CDPATH

readonly PROGRAM="${0##*/}"

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

dockerfiles=("$dir"/Dockerfile.*)
[ -e "${dockerfiles[0]}" ] || die "no Dockerfile.* in $dir"

failed=0
checked=0
for dockerfile in "${dockerfiles[@]}"; do
	name="${dockerfile##*/}"
	while read -r manifest; do
		if [ ! -f "$dir/$manifest" ]; then
			echo "FAIL: $name copies $manifest, which is not in $dir"
			failed=1
			continue
		fi
		while read -r top; do
			checked=$((checked + 1))
			if ! grep -Eq -- "^COPY[[:space:]]+(--[^[:space:]]+[[:space:]]+)*${top}/?[[:space:]]" "$dockerfile"; then
				echo "FAIL: $name builds from $manifest, which replaces modules in ../../$top, but never copies $top"
				failed=1
			fi
		done < <(sed -nE 's#^[[:space:]]*-[[:space:]]+[^[:space:]]+[[:space:]]+=>[[:space:]]+\.\./\.\./([^/[:space:]]+).*#\1#p' "$dir/$manifest" | sort -u)
	done < <(sed -nE 's#^COPY[[:space:]]+(builder-[^[:space:]]+\.yaml)[[:space:]].*#\1#p' "$dockerfile")
done

if [ "$checked" -eq 0 ]; then
	die "no Dockerfile in $dir copies a builder manifest with local replaces; nothing was checked"
fi
if [ "$failed" -ne 0 ]; then
	exit 1
fi
echo "PASS: $checked replace directories are copied by the Dockerfiles that use them"
