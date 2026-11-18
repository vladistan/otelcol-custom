#!/usr/bin/env bash
# Fails when a Linux edge sample config does not send system.processes.count and system.processes.created:
# the receiver named host_metrics must have the `processes` scraper under its scrapers, and host_metrics must
# be listed in a metrics pipeline under service.pipelines. The `process` scraper (one series for each process)
# is another scraper and does not count; host_metrics/<name> copies are other receivers; commented lines do not
# count. Any consistent indent works, and a trailing comment after a key is fine.
#
# Usage: check-edge-processes.sh [CONFIG...]   (default: test/configs/local-edge.yaml and gpu-edge.yaml)
# Exit: 0 PASS, 1 a config fails, 2 usage error.
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
	files=("$root/test/configs/local-edge.yaml" "$root/test/configs/gpu-edge.yaml")
fi

# Prints SCRAPER when the processes scraper is under receivers.host_metrics.scrapers, and PIPELINE when
# host_metrics is a receiver of a metrics pipeline. The files are indented YAML read by indent: a block ends at
# the first line indented no deeper than its key.
scan() {
	awk '
	function indent(s) { match(s, /^ */); return RLENGTH }
	function clean(s) { sub(/[ \t]+#.*$/, "", s); sub(/^[ \t]+/, "", s); sub(/[ \t]+$/, "", s); return s }
	function has_receiver(list,   n, i, parts) {
		n = split(list, parts, /[],[]/)
		for (i = 1; i <= n; i++) { gsub(/^[ \t]+|[ \t]+$/, "", parts[i]); if (parts[i] == "host_metrics") return 1 }
		return 0
	}
	/^[ \t]*(#|$)/ { next }
	{
		ind = indent($0)
		body = clean(substr($0, ind + 1))
		if (ind == 0) {
			top = body
			in_hm = 0; in_pipe = 0; in_list = 0; scrapers = -1
			if (body ~ /^receivers:/) { section = "receivers"; rc_indent = -1 } else if (body ~ /^service:/) { section = "service"; svc_indent = -1; pipes = -1; pid = -1 } else section = ""
			next
		}
		if (section == "receivers") {
			if (rc_indent < 0) rc_indent = ind
			if (ind == rc_indent) { in_hm = (body ~ /^host_metrics:$/); scrapers = -1; child = -1; next }
			if (!in_hm) next
			if (scrapers < 0) { if (body ~ /^scrapers:$/) { scrapers = ind; child = -1 } ; next }
			if (ind <= scrapers) { scrapers = -1; if (body ~ /^scrapers:$/) { scrapers = ind; child = -1 }; next }
			if (child < 0) child = ind
			if (ind == child && body ~ /^processes:/) print "SCRAPER"
			next
		}
		if (section == "service") {
			if (svc_indent < 0) svc_indent = ind
			if (ind == svc_indent) { pipes = (body ~ /^pipelines:$/) ? -2 : -1; pid = -1; in_pipe = 0; next }
			if (pipes == -1) next
			if (pipes == -2) pipes = ind
			if (ind == pipes) { in_pipe = (body ~ /^metrics(\/[^:]*)?:$/); in_list = 0; pid = ind; next }
			if (!in_pipe) next
			if (in_list && ind > list_indent && body ~ /^-/) { item = body; sub(/^-[ \t]*/, "", item); if (item == "host_metrics") print "PIPELINE"; next }
			in_list = 0
			if (body ~ /^receivers:[ \t]*\[/) { list = body; sub(/^receivers:[ \t]*\[/, "", list); if (has_receiver(list)) print "PIPELINE"; next }
			if (body ~ /^receivers:$/) { in_list = 1; list_indent = ind }
		}
	}' "$1" | sort -u
}

status=0
for file in "${files[@]}"; do
	[ -f "$file" ] || die "$file does not exist"
	found="$(scan "$file")"
	ok=1
	if ! grep -q '^SCRAPER$' <<<"$found"; then
		echo "FAIL: $file has no processes scraper in its host_metrics receiver"
		ok=0
	fi
	if ! grep -q '^PIPELINE$' <<<"$found"; then
		echo "FAIL: $file has no metrics pipeline with host_metrics in its receivers"
		ok=0
	fi
	if [ "$ok" -eq 1 ]; then
		echo "PASS: $file sends the processes scraper in a metrics pipeline"
	else
		status=1
	fi
done
exit "$status"
