#!/usr/bin/env bash
# Fails unless a binary's build info carries the release stamp "-tags release_<VERSION>".
# -trimpath removes -ldflags from the build info, so main.version cannot be read from a
# trimmed binary; the build tags stay, and the release builds of the cross-compiled
# binaries (which cannot be run here) pass -tags release_<VERSION>.
#
# Usage: check-release-stamp.sh BINARY VERSION
# Exit: 0 stamped with VERSION, 1 no stamp or another version, 2 usage error or unreadable binary.
set -euo pipefail
unset CDPATH

readonly PROGRAM="${0##*/}"

die() {
	echo "$PROGRAM: $*" >&2
	exit 2
}

[ $# -eq 2 ] || die "usage: $PROGRAM BINARY VERSION"
bin="$1"
want="$2"
[ -f "$bin" ] || die "missing binary: $bin"
info="$(go version -m "$bin")" || die "cannot read the build info of $bin"

# The tags line is "build -tags=a,b"; the stamp is the tag that starts with release_.
stamped="$(printf '%s\n' "$info" | awk '
	$1 == "build" && $2 ~ /^-tags=/ {
		v = $2
		sub(/^-tags=/, "", v)
		gsub(/"/, "", v)
		n = split(v, tags, ",")
		for (i = 1; i <= n; i++) {
			if (tags[i] ~ /^release_/) {
				sub(/^release_/, "", tags[i])
				print tags[i]
				exit
			}
		}
	}')"

if [ -z "$stamped" ]; then
	echo "FAIL: $bin has no release stamp (-tags release_<version>) in its build info" >&2
	exit 1
fi
if [ "$stamped" != "$want" ]; then
	echo "FAIL: $bin is stamped release_$stamped, expected release_$want" >&2
	exit 1
fi
echo "OK: $bin is stamped release_$want"
