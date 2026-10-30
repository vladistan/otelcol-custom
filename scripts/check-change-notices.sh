#!/usr/bin/env bash
#
# Checks the Apache-2.0 change notices on vendored pmady code. Git rename
# detection cannot find the moved files, so the module README table is the
# authority.
#
# Each README row | `upstream` | `module` | must name a file that exists in K1
# and carries "Modified:" and "Original: <url>@<sha>/<path>" in its first six
# lines. No other file may carry an Original: line. LICENSE, and NOTICE if K1
# has one, must match K1.
#
# Usage:
#   check-change-notices.sh [--module DIR] [--upstream DIR] <k1-sha>
#   check-change-notices.sh --self-test
#
# shellcheck disable=SC2016  # the backticks are literal markdown, not command substitution
set -euo pipefail

readonly PROGRAM="${0##*/}"
readonly UPSTREAM_URL="https://github.com/pmady/otel-gpu-receiver"
readonly DEFAULT_MODULE="tools/otel-collector/receiver/gpureceiver"
readonly DEFAULT_UPSTREAM="tools/otel-collector/third_party/otel-gpu-receiver"
readonly HEADER_LINES=6

failures=0

fail() {
	echo "FAIL: $*" >&2
	failures=$((failures + 1))
}

usage() {
	sed -n '/^# Usage:/,/^#$/p' "$0" | sed 's/^# \{0,1\}//' >&2
	exit 2
}

# Prints capture group 1 (upstream path) or 2 (module path) of a README row.
row_field() {
	printf '%s\n' "$1" | sed -E "s/^\| \`([^\`]+)\` \| \`([^\`]+)\` \|.*/\\$2/"
}

check_equal_to_k1() {
	local name=$1 requirement=$2
	if git cat-file -e "$k1:$upstream/$name" 2>/dev/null; then
		git show "$k1:$upstream/$name" | cmp -s - "$module/$name" ||
			fail "$name is missing or differs from $k1:$upstream/$name"
	elif [ "$requirement" = required ]; then
		fail "$name is not in $k1:$upstream"
	fi
}

check_notices() {
	local readme="$module/README.md"
	[ -f "$readme" ] || { fail "$readme is missing"; return; }

	local sha
	sha=$(sed -n 's/^[-* ]*Upstream commit: `\([0-9a-f]\{40\}\)`.*/\1/p' "$readme" | head -n 1)
	[ -n "$sha" ] || { fail "$readme has no line: Upstream commit: \`<40 hex>\`"; return; }

	local listed="" rows=0 line up mod header
	while IFS= read -r line; do
		rows=$((rows + 1))
		up=$(row_field "$line" 1)
		mod=$(row_field "$line" 2)
		git cat-file -e "$k1:$upstream/$up" 2>/dev/null ||
			fail "$up is not in $k1:$upstream"
		if [ ! -f "$module/$mod" ]; then
			fail "$module/$mod is missing"
			continue
		fi
		header=$(head -n "$HEADER_LINES" "$module/$mod")
		printf '%s\n' "$header" | grep -q 'Modified:' ||
			fail "$mod has no 'Modified:' notice in its first $HEADER_LINES lines"
		printf '%s\n' "$header" | grep -qF "Original: $UPSTREAM_URL@$sha/$up" ||
			fail "$mod has no 'Original: $UPSTREAM_URL@$sha/$up' line"
		listed="$listed$mod"$'\n'
	done < <(grep -E '^\| `[^`]+` \| `[^`]+` \|' "$readme")
	[ "$rows" -gt 0 ] || fail "$readme has no | \`upstream\` | \`module\` | rows"

	local file rel
	while IFS= read -r file; do
		rel=${file#"$module"/}
		printf '%s' "$listed" | grep -qxF "$rel" ||
			fail "$rel carries an Original: notice but is not in the README table"
	done < <(grep -rlF "Original: $UPSTREAM_URL@" "$module" || true)

	check_equal_to_k1 LICENSE required
	check_equal_to_k1 NOTICE optional
	[ "$failures" -gt 0 ] || echo "OK: $rows modified files carry change notices (upstream $sha)"
}

run_check() {
	module=$1 upstream=$2 k1=$3
	cd "$(git rev-parse --show-toplevel)"
	git rev-parse --verify -q "$k1^{commit}" >/dev/null || { fail "$k1 is not a commit"; return 1; }
	check_notices
	[ "$failures" -eq 0 ]
}

FIXTURE_SHA=$(printf 'ab%.0s' {1..20})
readonly FIXTURE_SHA

fixture_notice() {
	printf '// Modified: test.\n// Original: %s@%s/%s\n\npackage x\n' \
		"$UPSTREAM_URL" "${2:-$FIXTURE_SHA}" "$1"
}

build_fixture() {
	local dir=$1
	git init -q "$dir"
	mkdir -p "$dir/u" "$dir/m"
	printf 'package x\n' >"$dir/u/a.go"
	printf 'Apache License\n' >"$dir/u/LICENSE"
	git -C "$dir" add -A
	git -C "$dir" -c user.name=t -c user.email=t@t commit -q -m k1
	cp "$dir/u/LICENSE" "$dir/m/LICENSE"
	fixture_notice a.go >"$dir/m/a.go"
	printf 'Upstream commit: `%s`\n\n| Upstream | Module |\n|---|---|\n| `a.go` | `a.go` |\n' \
		"$FIXTURE_SHA" >"$dir/m/README.md"
}

expect() {
	local label=$1 want=$2 needle=${3:-} out status=0
	out=$(cd "$fixture" && bash "$self" --module m --upstream u "$(git -C "$fixture" rev-parse HEAD)" 2>&1) || status=$?
	if [ "$want" -eq 0 ] && [ "$status" -ne 0 ]; then
		echo "self-test FAIL: $label should pass: $out" >&2
		selftest_failed=1
	elif [ "$want" -ne 0 ] && { [ "$status" -eq 0 ] || ! printf '%s' "$out" | grep -qF "$needle"; }; then
		echo "self-test FAIL: $label should fail naming '$needle' (status $status): $out" >&2
		selftest_failed=1
	else
		echo "self-test ok: $label"
	fi
}

self_test() {
	self=$(cd "$(dirname "$0")" && pwd)/$PROGRAM
	fixture=$(mktemp -d)
	trap 'rm -rf "$fixture"' EXIT
	selftest_failed=0
	build_fixture "$fixture"

	expect "every moved file carries its notice" 0

	printf 'package x\n' >"$fixture/m/a.go"
	expect "moved file without a notice" 1 "a.go has no 'Modified:'"

	fixture_notice a.go deadbeefdeadbeefdeadbeefdeadbeefdeadbeef >"$fixture/m/a.go"
	expect "notice with the wrong upstream sha" 1 "a.go has no 'Original:"

	fixture_notice a.go >"$fixture/m/a.go"
	fixture_notice b.go >"$fixture/m/b.go"
	expect "notice on a file the README does not list" 1 "b.go carries an Original: notice"
	rm "$fixture/m/b.go"

	printf 'Apache License, edited\n' >"$fixture/m/LICENSE"
	expect "edited LICENSE" 1 "LICENSE is missing or differs"
	cp "$fixture/u/LICENSE" "$fixture/m/LICENSE"

	printf '| `gone.go` | `a.go` |\n' >>"$fixture/m/README.md"
	expect "listed upstream file absent from K1" 1 "gone.go is not in"

	return "$selftest_failed"
}

module=$DEFAULT_MODULE
upstream=$DEFAULT_UPSTREAM
k1=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--self-test) self_test; exit $? ;;
	--module) module=${2:?--module needs a value}; shift 2 ;;
	--upstream) upstream=${2:?--upstream needs a value}; shift 2 ;;
	-h | --help) usage ;;
	-*) echo "$PROGRAM: unknown option $1" >&2; usage ;;
	*) k1=$1; shift ;;
	esac
done
[ -n "$k1" ] || usage
run_check "$module" "$upstream" "$k1"
