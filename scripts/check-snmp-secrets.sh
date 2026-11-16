#!/usr/bin/env bash
# Fails when a sample config holds a literal SNMP secret. The keys community, auth_password and
# privacy_password must take a value of the form ${env:NAME}, bare or in matching quotes; any other value is a
# hit. The key is found after a list dash, inside a flow mapping and in quotes. Comment lines and trailing
# comments are skipped. Scope: every .yaml and .yml file (any case) below DIR.
#
# Usage: check-snmp-secrets.sh [DIR]   (default: test/configs below the tool directory; YAML files only)
# Output: file:line:text per hit, then a count line.
# Exit: 0 clean, 1 a literal secret found, 2 usage error or a failed command.
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
	root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)" || die "cannot resolve the script directory"
	dir="$root/test/configs"
fi
[ -d "$dir" ] || die "$dir is not a directory"

q="'"
# A key anywhere on the line: after a list dash, inside a flow mapping, quoted or not. The value stops at a
# comma or a closing brace, except inside ${...}.
key_re='(^|[^[:alnum:]_])["'"$q"']?(community|auth_password|privacy_password)["'"$q"']?[[:space:]]*:[[:space:]]*((\$\{[^}]*\}|[^,}])*)(.*)$'
# The only accepted value: ${env:NAME}, bare or in matching quotes.
ok_re='^(\$\{env:[A-Za-z_][A-Za-z0-9_]*\}|"\$\{env:[A-Za-z_][A-Za-z0-9_]*\}"|'"$q"'\$\{env:[A-Za-z_][A-Za-z0-9_]*\}'"$q"')$'

hits=0
files=0
while IFS= read -r file; do
	files=$((files + 1))
	lineno=0
	while IFS= read -r line || [ -n "$line" ]; do
		lineno=$((lineno + 1))
		case "$line" in
		*[![:space:]]*) ;;
		*) continue ;;
		esac
		stripped="${line#"${line%%[![:space:]]*}"}"
		case "$stripped" in "#"*) continue ;; esac
		rest="${stripped%%[[:space:]]#*}" # a trailing comment is not scanned
		while [[ "$rest" =~ $key_re ]]; do
			value="${BASH_REMATCH[3]}"
			after="${BASH_REMATCH[5]}"
			value="${value#"${value%%[![:space:]]*}"}"
			value="${value%"${value##*[![:space:]]}"}"
			if [[ ! "$value" =~ $ok_re ]]; then
				echo "$file:$lineno:$stripped"
				hits=$((hits + 1))
				break
			fi
			rest="$after"
		done
	done <"$file"
done < <(find "$dir" -type f \( -iname '*.yaml' -o -iname '*.yml' \) | sort)

if [ "$hits" -ne 0 ]; then
	echo "FAIL: $hits literal SNMP secret(s) in $dir; use \${env:NAME}"
	exit 1
fi
echo "PASS: no literal SNMP secret in $files config file(s) under $dir"
