#!/usr/bin/env bash
# Fails when a docker_stats block in a sample config does not set api_version to the empty string.
# The contrib receiver (v0.164.1) defaults api_version to 1.44, so it fails to start against a Docker
# daemon whose API tops out below that (Docker 24 answers up to 1.43) and the collector keeps
# crashing. An empty api_version makes the Docker client negotiate the version with the daemon.
# A pinned "1.44" fails too: it is the same default.
#
# Usage: check-docker-stats-api-version.sh [CONFIG...]   (default: test/configs/*.yaml)
# Exit: 0 PASS, 1 a block lacks the empty api_version, 2 usage error.
set -euo pipefail
unset CDPATH

readonly PROGRAM="${0##*/}"

die() {
	echo "$PROGRAM: $*" >&2
	exit 2
}

if [ $# -gt 0 ]; then
	files=("$@")
else
	root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)" || die "cannot resolve the script directory"
	files=("$root"/test/configs/*.yaml)
fi
for file in "${files[@]}"; do
	[ -f "$file" ] || die "$file does not exist"
done

# Prints one line per docker_stats block that has no empty api_version. A block runs from its key to the
# next line indented no deeper than the key.
find_bad_blocks() {
	awk '
		function finish() {
			if (in_block && !found_empty) print block_name
			in_block = 0
		}
		{
			line = $0
			if (line ~ /^[[:space:]]*(#.*)?$/) next
			stripped = line
			sub(/^[ ]+/, "", stripped)
			indent = length(line) - length(stripped)
			if (in_block && indent <= key_indent) finish()
			if (!in_block && line ~ /^[ ]*docker_stats(\/[^:[:space:]]+)?:[[:space:]]*(#.*)?$/) {
				in_block = 1; found_empty = 0; key_indent = indent
				block_name = stripped; sub(/:.*/, "", block_name)
				next
			}
			if (in_block && line ~ /^[ ]*api_version:[[:space:]]*(""|\047\047)[[:space:]]*(#.*)?$/) found_empty = 1
		}
		END { finish() }
	' "$1"
}

failed=0
checked=0
for file in "${files[@]}"; do
	bad="$(find_bad_blocks "$file")"
	if [ -n "$bad" ]; then
		while IFS= read -r name; do
			echo "FAIL: ${file##*/}: $name has no api_version: \"\" (the receiver default 1.44 breaks start on Docker 24)"
		done <<<"$bad"
		failed=1
	fi
	if grep -Eq '^[[:space:]]*docker_stats(/[^:[:space:]]+)?:' "$file"; then
		checked=$((checked + 1))
	fi
done

if [ "$failed" -ne 0 ]; then
	exit 1
fi
echo "PASS: $checked sample configs with docker_stats set api_version to an empty string"
