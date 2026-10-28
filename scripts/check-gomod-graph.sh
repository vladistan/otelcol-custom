#!/usr/bin/env bash
# Compares the core, contrib, aws-sdk-go-v2 and go-expohisto versions in a
# generated manifest go.mod with the committed baseline (stop rules 05-S1,
# 07-S1).
# Generates with OCB in a scratch output dir; the stale build/<name>/go.mod
# is never read.
#
# Usage: check-gomod-graph.sh [--update] [--gomod FILE] [MANIFEST...]
#   MANIFEST  edge, edge-static, gateway, scrape, edge-windows or local
#             (default: all but edge-windows)
#   --update  rewrite the baseline from the generated go.mod
#   --gomod   read FILE instead of generating (one manifest only)
# Env: OCB_VERSION, e.g. 0.154.0, required unless --gomod is given.
#      GOMOD_BASELINES overrides the baseline directory, GOMOD_LOCAL_ROOT
#      the tree "local" scans (tests).
# "local" compares every go.mod under processor/, receiver/, stanza/ and
# test/ with the baselines: a collector, contrib or aws-sdk-go-v2 pin must
# equal a version some baseline carries.
# edge-windows has no baseline of its own: it is compared with edge-static
# on the modules both have, so added and removed receivers are allowed.
# Exit: 0 no version change, 1 a version changed, 2 usage error or a failed
# command.
set -euo pipefail
unset CDPATH

readonly PROGRAM="${0##*/}"
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)" || {
	echo "$PROGRAM: cannot resolve the script directory" >&2
	exit 2
}
readonly ROOT
readonly BASELINES="${GOMOD_BASELINES:-$ROOT/test/baselines/gomod}"
readonly MODULE_RE='^(go\.opentelemetry\.io/collector|github\.com/open-telemetry/opentelemetry-collector-contrib|github\.com/aws/aws-sdk-go-v2|github\.com/lightstep/go-expohisto)'

die() {
	echo "$PROGRAM: $*" >&2
	exit 2
}

# check_one and check_local run under "|| status=$?", where set -e is off,
# so every command that can fail in them is checked by hand.
WORK_DIR="$(mktemp -d)" || die "cannot create a scratch directory"
readonly WORK_DIR
trap 'rm -rf "$WORK_DIR"' EXIT

# Prints sorted "module version" lines from a go.mod.
extract() {
	go mod edit -json "$1" |
		jq -r --arg re "$MODULE_RE" '.Require[]? | select(.Path | test($re)) | "\(.Path) \(.Version)"' |
		LC_ALL=C sort -u
}

# Generates MANIFEST with OCB into build/gomod-check-MANIFEST and prints
# the path of its go.mod. Output depth matches build/<name>, so the
# manifest's relative replaces resolve.
generate() {
	local name="$1" out="build/gomod-check-$1" patched_config="$WORK_DIR/$1.yaml"
	[ -f "$ROOT/builder-$name.yaml" ] || die "builder-$name.yaml does not exist"
	sed -E "s#^([[:space:]]*output_path:).*#\\1 ./$out#" "$ROOT/builder-$name.yaml" >"$patched_config" ||
		die "cannot patch builder-$name.yaml"
	(cd "$ROOT" && rm -rf "$out" &&
		go run "go.opentelemetry.io/collector/cmd/builder@v$OCB_VERSION" \
			--config "$patched_config" --skip-compilation >&2) ||
		die "OCB failed for $name"
	echo "$ROOT/$out/go.mod"
}

# Prints "module old new" for each module present in both lists with
# different versions.
print_changed_versions() {
	awk 'NR == FNR { base[$1] = $2; next } ($1 in base) && base[$1] != $2 { print $1, base[$1], $2 }' "$1" "$2"
}

check_one() {
	local name="$1" gomod="$2" baseline="$BASELINES/$1.txt" found_file changes diff_status=0
	[ "$name" = edge-windows ] && baseline="$BASELINES/edge-static.txt"
	if [ -z "$gomod" ]; then
		gomod="$(generate "$name")" || exit 2
	fi
	found_file="$WORK_DIR/$name.found"
	extract "$gomod" >"$found_file" || die "cannot read versions from $gomod"
	[ -s "$found_file" ] || die "no matching modules in $gomod"
	if [ "$should_update" = true ]; then
		[ "$name" != edge-windows ] || die "edge-windows has no baseline to update"
		mkdir -p "$BASELINES" || die "cannot create $BASELINES"
		cp "$found_file" "$baseline" || die "cannot write $baseline"
		echo "updated: $baseline ($(wc -l <"$baseline" | tr -d ' ') modules)"
		return 0
	fi
	[ -f "$baseline" ] || die "baseline $baseline does not exist"
	if [ "$name" = edge-windows ]; then
		changes="$(print_changed_versions "$baseline" "$found_file")" || die "cannot compare $name"
	else
		changes="$(diff "$baseline" "$found_file")" || diff_status=$?
		[ "$diff_status" -le 1 ] || die "diff failed for $name"
	fi
	if [ -n "$changes" ]; then
		echo "version change: $name"
		printf '%s\n' "$changes"
		return 1
	fi
	echo "no version change: $name"
}

check_local() {
	local root="${GOMOD_LOCAL_ROOT:-$ROOT}" known="$WORK_DIR/known.txt" list="$WORK_DIR/local.list"
	local baseline_files=("$BASELINES"/*.txt) gomod dir drift="" count=0 found_drift
	[ -e "${baseline_files[0]}" ] || die "no baselines in $BASELINES"
	cat "${baseline_files[@]}" | LC_ALL=C sort -u >"$known" || die "cannot read the baselines"
	: >"$list" || die "cannot write $list"
	for dir in processor receiver stanza test; do
		if [ -d "$root/$dir" ]; then
			find "$root/$dir" -name go.mod -not -path '*/build/*' >>"$list" || die "cannot scan $root/$dir"
		fi
	done
	LC_ALL=C sort -o "$list" "$list" || die "cannot sort $list"
	while IFS= read -r gomod; do
		count=$((count + 1))
		found_drift="$(extract "$gomod" |
			awk -v mod="${gomod#"$root"/}" 'NR == FNR { ok[$1 " " $2] = 1; seen[$1] = 1; next }
				($1 in seen) && !(($1 " " $2) in ok) { print mod ": " $1 " has " $2 }' "$known" -)" ||
			die "cannot read $gomod"
		[ -z "$found_drift" ] || drift="${drift}${found_drift}"$'\n'
	done <"$list"
	[ "$count" -gt 0 ] || die "no local go.mod files under $root"
	if [ -n "$drift" ]; then
		echo "version change: local"
		printf '%s' "$drift"
		return 1
	fi
	echo "no version change: local ($count modules)"
}

should_update=false
gomod=""
manifests=()
while [ $# -gt 0 ]; do
	case "$1" in
	--update) should_update=true ;;
	--gomod)
		[ $# -ge 2 ] || die "--gomod needs a file"
		gomod="$2"
		shift
		;;
	-*) die "unknown option $1" ;;
	edge | edge-static | gateway | scrape | edge-windows | local) manifests+=("$1") ;;
	*) die "unknown manifest $1" ;;
	esac
	shift
done
[ ${#manifests[@]} -gt 0 ] || manifests=(edge edge-static gateway scrape local)
for name in "${manifests[@]}"; do
	if [ "$name" = local ]; then
		[ "$should_update" = false ] || die "local has no baseline to update"
		[ -z "$gomod" ] || die "--gomod does not apply to local"
	fi
done
if [ -n "$gomod" ]; then
	[ ${#manifests[@]} -eq 1 ] || die "--gomod takes exactly one manifest"
	[ -f "$gomod" ] || die "$gomod does not exist"
elif [ "${manifests[*]}" != local ]; then
	[ -n "${OCB_VERSION:-}" ] || die "OCB_VERSION is not set"
fi

status=0
for name in "${manifests[@]}"; do
	if [ "$name" = local ]; then
		check_local || status=$?
	else
		check_one "$name" "$gomod" || status=$?
	fi
done
exit "$status"
