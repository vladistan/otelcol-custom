#!/usr/bin/env bash
# Runs each image with --version and fails unless the output contains
# VERSION. Every image is checked, then the exit code
# reports whether all passed.
#
# Usage: verify-ghcr-images.sh VERSION IMAGE...
# Env: DOCKER overrides the docker binary (tests).
# Exit: 0 all images report VERSION, 1 an image does not, 2 usage error.
set -euo pipefail
unset CDPATH

readonly PROGRAM="${0##*/}"
readonly DOCKER_BIN="${DOCKER:-docker}"

if [ $# -lt 2 ]; then
	echo "usage: $PROGRAM VERSION IMAGE..." >&2
	exit 2
fi
version="$1"
shift

status=0
for image in "$@"; do
	run_status=0
	output="$("$DOCKER_BIN" run --rm --platform linux/amd64 "$image" --version 2>&1)" || run_status=$?
	if [ "$run_status" -ne 0 ]; then
		echo "FAIL: $image --version failed (exit $run_status): $output"
		status=1
	elif ! grep -qF -- "$version" <<<"$output"; then
		echo "FAIL: $image --version output lacks $version: $output"
		status=1
	else
		echo "PASS: $image reports $version"
	fi
done
exit "$status"
