#!/usr/bin/env bash
# Guards the pod discovery sample configs and the builder manifests:
#   - no sample config turns annotation discovery on (a discovery block with enabled: true);
#   - every receiver_creator block names watch_observers, and each observer it names is listed in
#     service.extensions;
#   - no builder manifest compiles prometheus_simple; pods are scraped with the prometheus receiver;
#   - the redis, postgresql, mysql, mongodb, rabbitmq and nginx receivers are compiled only into
#     builder-edge.yaml and builder-edge-static.yaml, and no manifest compiles memcached or kafka_metrics.
# Annotation discovery lets anyone who can edit a pod annotation start receivers, so only rules
# in the config start them.
#
# Usage: check-k8s-discovery-config.sh [--manifest FILE]... [CONFIG...]
#   CONFIG  default: test/configs/*.yaml
#   --manifest  default: builder-*.yaml
# Exit: 0 PASS, 1 a check failed, 2 usage error.
set -euo pipefail
unset CDPATH

readonly PROGRAM="${0##*/}"

die() {
	echo "$PROGRAM: $*" >&2
	exit 2
}

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)" || die "cannot resolve the script directory"

manifests=()
files=()
while [ $# -gt 0 ]; do
	case "$1" in
	--manifest)
		[ $# -ge 2 ] || die "--manifest needs a file"
		manifests+=("$2")
		shift 2
		;;
	-*) die "unknown option $1" ;;
	*)
		files+=("$1")
		shift
		;;
	esac
done
[ ${#files[@]} -gt 0 ] || files=("$root"/test/configs/*.yaml)
[ ${#manifests[@]} -gt 0 ] || manifests=("$root"/builder-*.yaml)
for file in "${files[@]}" "${manifests[@]}"; do
	[ -f "$file" ] || die "$file does not exist"
done

# Prints "creator NAME" for each receiver_creator block, and "watch NAME OBSERVER" for each entry of its
# watch_observers, inline ([a, b]) or as a block list. A block runs to the next line indented no deeper
# than its key.
creators() {
	awk '
		function strip(text) {
			sub(/[ ]+#.*$/, "", text)
			sub(/^[ ]+/, "", text)
			sub(/[ ]+$/, "", text)
			return text
		}
		function emit(list,    n, i, parts) {
			gsub(/[\[\]\047"]/, "", list)
			n = split(list, parts, /[ ,]+/)
			for (i = 1; i <= n; i++) if (parts[i] != "") print "watch " name " " parts[i]
		}
		/^[ ]*(#.*)?$/ { next }
		{
			text = strip($0)
			if (text ~ /^["\047]/) { sub(/^["\047]/, "", text); sub(/["\047]:/, ":", text) }
			lead = $0; sub(/^[ ]+/, "", lead); indent = length($0) - length(lead)
			if (in_creator && indent <= creator_indent) { in_creator = 0; in_watch = 0 }
			if (!in_creator && text ~ /^receiver_creator(\/[^:[:space:]]+)?:/) {
				in_creator = 1; creator_indent = indent
				name = text; sub(/:.*/, "", name)
				print "creator " name
				next
			}
			if (!in_creator) next
			if (in_watch && indent <= watch_indent) in_watch = 0
			if (text ~ /^watch_observers:/) {
				rest = text; sub(/^watch_observers:[ ]*/, "", rest)
				if (rest != "") emit(rest)
				else { in_watch = 1; watch_indent = indent }
				next
			}
			if (in_watch && text ~ /^-/) { item = text; sub(/^-[ ]*/, "", item); emit(item) }
		}
	' "$1"
}

# Prints each name under service.extensions, inline or as a block list.
service_extensions() {
	awk '
		function strip(text) {
			sub(/[ ]+#.*$/, "", text)
			sub(/^[ ]+/, "", text)
			sub(/[ ]+$/, "", text)
			return text
		}
		function emit(list,    n, i, parts) {
			gsub(/[\[\]\047"]/, "", list)
			n = split(list, parts, /[ ,]+/)
			for (i = 1; i <= n; i++) if (parts[i] != "") print parts[i]
		}
		/^[ ]*(#.*)?$/ { next }
		{
			text = strip($0)
			lead = $0; sub(/^[ ]+/, "", lead); indent = length($0) - length(lead)
			if (indent == 0) { in_service = (text ~ /^service:/); in_ext = 0; next }
			if (!in_service) next
			if (in_ext && indent <= ext_indent) in_ext = 0
			if (text ~ /^extensions:/) {
				rest = text; sub(/^extensions:[ ]*/, "", rest)
				if (rest != "") emit(rest)
				else { in_ext = 1; ext_indent = indent }
				next
			}
			if (in_ext && text ~ /^-/) { item = text; sub(/^-[ ]*/, "", item); emit(item) }
		}
	' "$1"
}

# Prints a line when a discovery block has enabled: true, in block or flow style, with the value
# bare or quoted, in any case of true.
discovery_on() {
	awk '
		function is_true(value) {
			sub(/[ ]+#.*$/, "", value)
			gsub(/^[ ]+|[ ]+$/, "", value)
			gsub(/^["\047]|["\047]$/, "", value)
			return tolower(value) == "true"
		}
		/^[ ]*(#.*)?$/ { next }
		{
			stripped = $0
			sub(/^[ ]+/, "", stripped)
			indent = length($0) - length(stripped)
			if (in_block && indent <= key_indent) in_block = 0
			if (!in_block && stripped ~ /^["\047]?discovery["\047]?:/) {
				rest = stripped
				sub(/^["\047]?discovery["\047]?:[ ]*/, "", rest)
				if (rest ~ /^\{/) {
					if (match(rest, /enabled["\047]?[ ]*:[ ]*[^,}]+/)) {
						value = substr(rest, RSTART, RLENGTH)
						sub(/^enabled["\047]?[ ]*:[ ]*/, "", value)
						if (is_true(value)) print "on"
					}
				} else { in_block = 1; key_indent = indent }
				next
			}
			if (in_block && stripped ~ /^["\047]?enabled["\047]?:/) {
				value = stripped
				sub(/^["\047]?enabled["\047]?:[ ]*/, "", value)
				if (is_true(value)) { print "on"; in_block = 0 }
			}
		}
	' "$1"
}

failed=0
creator_files=0
for file in "${files[@]}"; do
	base="${file##*/}"
	if [ -n "$(discovery_on "$file")" ]; then
		echo "FAIL: $base: discovery.enabled is on; annotation discovery lets pod annotations start receivers"
		failed=1
	fi
	listing="$(creators "$file")"
	[ -n "$listing" ] || continue
	creator_files=$((creator_files + 1))
	extensions="$(service_extensions "$file")"
	while read -r kind name observer; do
		case "$kind" in
		creator)
			if ! grep -q "^watch $name " <<<"$listing"; then
				echo "FAIL: $base: $name has no watch_observers"
				failed=1
			fi
			;;
		watch)
			if ! grep -qx -- "$observer" <<<"$extensions"; then
				echo "FAIL: $base: $observer is not in service.extensions"
				failed=1
			fi
			;;
		esac
	done <<<"$listing"
done

readonly APP_RECEIVERS='receiver/(redis|postgresql|mysql|mongodb|rabbitmq|nginx)receiver([[:space:]]|$)'
readonly BANNED_RECEIVERS='receiver/(memcached|kafkametrics)receiver([[:space:]]|$)'

for manifest in "${manifests[@]}"; do
	base="${manifest##*/}"
	if grep -q 'prometheussimple\|prometheus_simple' "$manifest"; then
		echo "FAIL: $base: compiles prometheus_simple; scrape pods with the prometheus receiver"
		failed=1
	fi
	case "$base" in
	builder-edge.yaml | builder-edge-static.yaml) ;;
	*)
		if found="$(grep -oE "$APP_RECEIVERS" "$manifest")"; then
			echo "FAIL: $base: compiles an application receiver (${found//$'\n'/, }); only the Linux edge manifests carry them"
			failed=1
		fi
		;;
	esac
	if found="$(grep -oE "$BANNED_RECEIVERS" "$manifest")"; then
		echo "FAIL: $base: compiles ${found//$'\n'/, }; no manifest carries memcached or kafka_metrics"
		failed=1
	fi
done

if [ "$failed" -ne 0 ]; then
	exit 1
fi
echo "PASS: $creator_files receiver_creator configs name their observers, no discovery.enabled, ${#manifests[@]} manifests within the receiver limits"
