#!/usr/bin/env bash
# Fails unless every builder-*.yaml pins sentry-go to the version in go.mod.
# The builder compiles the generated distribution before the Sentry entrypoint
# is copied in, and an unpinned sentry-go resolves to the newest release, which
# no longer compiles against cmd/otelcol-custom. The pin is a replace line:
#   - github.com/getsentry/sentry-go => github.com/getsentry/sentry-go vX.Y.Z
#
# Usage: check-sentry-go-pin.sh [DIR]   (DIR holds go.mod and builder-*.yaml)
# Exit: 0 PASS, 1 a manifest lacks the pin, 2 usage error or a failed command.
set -euo pipefail
unset CDPATH

readonly PROGRAM="${0##*/}"
readonly MODULE="github.com/getsentry/sentry-go"

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
[ -f "$dir/go.mod" ] || die "$dir/go.mod does not exist"

want="$(sed -nE "s#^[[:space:]]*(require[[:space:]]+)?${MODULE}[[:space:]]+(v[^[:space:]]+).*#\\2#p" "$dir/go.mod" | head -1)"
[ -n "$want" ] || die "$dir/go.mod has no $MODULE requirement"

manifests=("$dir"/builder-*.yaml)
[ -e "${manifests[0]}" ] || die "no builder-*.yaml in $dir"

pin_line="^[[:space:]]*-[[:space:]]+${MODULE}[[:space:]]+=>[[:space:]]+${MODULE}[[:space:]]+${want//./\\.}[[:space:]]*$"
failed=0
for manifest in "${manifests[@]}"; do
	if ! grep -Eq -- "$pin_line" "$manifest"; then
		echo "FAIL: ${manifest##*/} does not pin $MODULE to $want"
		failed=1
	fi
done

if [ "$failed" -ne 0 ]; then
	exit 1
fi
echo "PASS: ${#manifests[@]} manifests pin $MODULE to $want"
