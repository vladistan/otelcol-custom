#!/usr/bin/env bash
# Fails unless builder-gateway.yaml still lists logshapeprocessor and the nine standalone processors
# it replaces, and docs/log-shaping.md and CHANGELOG.md each say when those nine go: in the release
# after production validation, with 0.3.19 named as the earliest, on the line that holds the phrase.
#
# Usage: check-logshape-retired-processors.sh [DIR]   (DIR holds builder-gateway.yaml, CHANGELOG.md, docs/)
# Exit: 0 PASS, 1 a processor or a statement is missing, 2 usage error or unreadable input.
set -euo pipefail
unset CDPATH

readonly PROGRAM="${0##*/}"
readonly PROCESSOR_PREFIX="github.com/vladistan/otelcol-custom/processor/"
readonly PROCESSORS=(
	logshapeprocessor
	klogprocessor
	uvicornlogprocessor
	logruslogprocessor
	postgreslogprocessor
	redislogprocessor
	nginxlogprocessor
	sentrylogprocessor
	ciscologprocessor
	syslogprocessor
)
readonly PHRASE="the release after production validation"
readonly VERSION="0.3.19"
readonly STATEMENT_FILES=(docs/log-shaping.md CHANGELOG.md)

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

gateway="$dir/builder-gateway.yaml"
[ -r "$gateway" ] || die "cannot read $gateway"
for file in "${STATEMENT_FILES[@]}"; do
	[ -r "$dir/$file" ] || die "cannot read $dir/$file"
done

failed=0
for processor in "${PROCESSORS[@]}"; do
	module="$PROCESSOR_PREFIX$processor"
	if ! grep -Eq -- "^[[:space:]]*-[[:space:]]+gomod:[[:space:]]+${module//./\\.}[[:space:]]" "$gateway"; then
		echo "FAIL: builder-gateway.yaml does not list $module"
		failed=1
	fi
done

for file in "${STATEMENT_FILES[@]}"; do
	lines="$(grep -F -- "$PHRASE" "$dir/$file" || true)"
	if [ -z "$lines" ]; then
		echo "FAIL: $file does not say \"$PHRASE\""
		failed=1
	elif ! grep -qF -- "$VERSION" <<<"$lines"; then
		echo "FAIL: $file says \"$PHRASE\" without naming $VERSION"
		failed=1
	fi
done

if [ "$failed" -ne 0 ]; then
	exit 1
fi
echo "PASS: the gateway lists ${#PROCESSORS[@]} processors and both documents date their removal"
