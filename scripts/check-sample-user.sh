#!/usr/bin/env bash
# Checks that every sample Kubernetes object that sets runAsNonRoot also sets a
# numeric runAsUser and runAsGroup equal to the numeric user of the image.
# The images name their user (USER otel); the kubelet cannot verify a name
# against runAsNonRoot and refuses to start the container
# (CreateContainerConfigError).
#
# Usage: check-sample-user.sh [DOCKERFILE [SAMPLE...]]
#   default: Dockerfile.scrape and the two scrape-*-deployment.yaml samples
# Exit: 0 PASS, 1 a rule fails, 2 usage error.
set -euo pipefail
unset CDPATH

readonly PROGRAM="${0##*/}"
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly ROOT

dockerfile="${1:-$ROOT/Dockerfile.scrape}"
if [ "$#" -gt 1 ]; then
	shift
	samples=("$@")
else
	samples=("$ROOT/test/configs/scrape-pve-deployment.yaml" "$ROOT/test/configs/scrape-ipmi-deployment.yaml")
fi

for f in "$dockerfile" "${samples[@]}"; do
	if [ ! -f "$f" ]; then
		echo "$PROGRAM: cannot read $f" >&2
		exit 2
	fi
done

status=0
fail() {
	echo "FAIL: $*"
	status=1
}

# The numeric user of the image is the id its adduser line creates.
uid="$(sed -nE 's/^[[:space:]]*(RUN[[:space:]]+.*)?adduser[[:space:]].*-u[[:space:]]+([0-9]+).*$/\2/p' "$dockerfile" | head -n 1)"
if [ -z "$uid" ]; then
	fail "$dockerfile creates no user with a numeric id (adduser -u N)"
fi

for sample in "${samples[@]}"; do
	grep -qE '^[[:space:]]*runAsNonRoot:[[:space:]]*true[[:space:]]*$' "$sample" || continue
	user="$(sed -nE 's/^[[:space:]]*runAsUser:[[:space:]]*([^[:space:]#]+).*$/\1/p' "$sample" | head -n 1)"
	group="$(sed -nE 's/^[[:space:]]*runAsGroup:[[:space:]]*([^[:space:]#]+).*$/\1/p' "$sample" | head -n 1)"
	if ! [[ "$user" =~ ^[0-9]+$ ]]; then
		fail "$sample sets runAsNonRoot without a numeric runAsUser (image user id ${uid:-unknown})"
	elif [ -n "$uid" ] && [ "$user" != "$uid" ]; then
		fail "$sample runAsUser $user differs from the image user id $uid"
	fi
	if ! [[ "$group" =~ ^[0-9]+$ ]]; then
		fail "$sample sets runAsNonRoot without a numeric runAsGroup (image group id ${uid:-unknown})"
	fi
done

[ "$status" -eq 0 ] && echo "PASS: samples set the numeric image user ${uid}"
exit "$status"
