#!/usr/bin/env bash
# Checks the sample Kubernetes objects of the ipmi receiver: one replica, the
# BMC username and password read from a Secret (no Secret object and no literal
# credential in the file), and a network policy with UDP 623 egress to exactly
# one subnet.
#
# Usage: check-ipmi-deployment.sh [FILE]   (default test/configs/scrape-ipmi-deployment.yaml)
# Exit: 0 PASS, 1 a rule fails, 2 usage error.
set -euo pipefail
unset CDPATH

readonly PROGRAM="${0##*/}"
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly ROOT
file="${1:-$ROOT/test/configs/scrape-ipmi-deployment.yaml}"

if [ ! -f "$file" ]; then
	echo "$PROGRAM: cannot read $file" >&2
	exit 2
fi

status=0
fail() {
	echo "FAIL: $*"
	status=1
}

grep -qE '^[[:space:]]+replicas:[[:space:]]*1[[:space:]]*$' "$file" || fail "the Deployment must have replicas: 1"

for var in IPMI_USERNAME IPMI_PASSWORD; do
	# The variable's definition must be followed by a secretKeyRef within four lines.
	if ! grep -A4 -E "name:[[:space:]]*$var[[:space:]]*$" "$file" | grep -q 'secretKeyRef'; then
		fail "$var must come from a secretKeyRef"
	fi
done

if grep -qE '^kind:[[:space:]]*Secret[[:space:]]*$' "$file"; then
	fail "a plain Secret object puts credentials in the repository; use an ExternalSecret"
fi
if grep -qE '^[[:space:]]*stringData:|^data:' "$file"; then
	fail "the file holds a data or stringData block"
fi

grep -qE '^kind:[[:space:]]*NetworkPolicy[[:space:]]*$' "$file" || fail "no NetworkPolicy"

if ! grep -B1 -E 'port:[[:space:]]*623[[:space:]]*$' "$file" | grep -qE 'protocol:[[:space:]]*UDP'; then
	fail "the network policy needs an egress rule for UDP 623"
fi
if [ "$(grep -cE 'port:[[:space:]]*623[[:space:]]*$' "$file")" -ne 1 ]; then
	fail "the network policy must have exactly one rule for UDP 623"
fi
if [ "$(grep -cE '^[[:space:]]+cidr:' "$file")" -ne 1 ]; then
	fail "the network policy must allow UDP 623 to one subnet"
fi

[ "$status" -eq 0 ] && echo "PASS: $file"
exit "$status"
