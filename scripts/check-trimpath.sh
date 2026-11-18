#!/usr/bin/env bash
# Fails when a shipped build compiles without -trimpath. Without it the build
# path is embedded in the binary, so two builds of one commit in different
# directories give different checksums.
#
# Scans the justfile, *.just, Makefile, *.mk, every Dockerfile, every *.sh and
# the YAML files under .github (workflows and composite actions), skipping
# build/, node_modules/ and .git/. Each logical command line (continuation
# lines joined) is cut at ; && || | ( ) and command substitutions, comments and
# quoted strings are dropped, and every `go build`, `go install` and `go run`
# in it is checked on its own. The go command may be written go, $(GO), ${GO},
# $GO, {{go}}, an absolute path ending in /go, and may carry `-C dir`.
#
# A command passes when it carries -trimpath (or -trimpath=true), or when the
# line above it is a comment `# trimpath-exempt: <reason>` with a non-empty
# reason. `go install` and `go run` of a pinned tool (a package@version) build
# a helper, not a shipped binary, and need neither.
#
# Usage: check-trimpath.sh [DIR]   (default: the tool root)
# Exit: 0 PASS, 1 a build lacks -trimpath, 2 usage error.
set -euo pipefail
unset CDPATH

readonly PROGRAM="${0##*/}"
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly ROOT
dir="${1:-$ROOT}"

if [ ! -d "$dir" ]; then
	echo "$PROGRAM: $dir is not a directory" >&2
	exit 2
fi

files=()
while IFS= read -r f; do
	files+=("$f")
done < <(
	find "$dir" \( -path "$dir/build" -o -name node_modules -o -name .git \) -prune -o -type f \
		\( -name justfile -o -name '*.just' -o -name Makefile -o -name '*.mk' -o -name 'Dockerfile' -o -name 'Dockerfile.*' -o -name '*.sh' \
		-o \( -path '*/.github/*' \( -name '*.yml' -o -name '*.yaml' \) \) \) \
		! -name check-trimpath.sh ! -name test-check-trimpath.sh -print | sort
)

status=0
for f in "${files[@]+"${files[@]}"}"; do
	rel="${f#"$dir"/}"
	out="$(awk -v file="$rel" '
		# code drops comments and the inside of quoted strings (each becomes Q,
		# or Q@ when the string held an @, which marks a pinned package@version).
		function code(s,   i, n, c, q, out, prev, at) {
			n = length(s); q = ""; out = ""; at = 0
			for (i = 1; i <= n; i++) {
				c = substr(s, i, 1)
				if (q == "\"") {
					if (c == "@") at = 1
					if (c == "\\") i++
					else if (c == "\"") { q = ""; if (at) out = out "@" }
					continue
				}
				if (q == "\047") {
					if (c == "@") at = 1
					if (c == "\047") { q = ""; if (at) out = out "@" }
					continue
				}
				if (c == "\"" || c == "\047") { q = c; at = 0; out = out "Q"; continue }
				if (c == "#") {
					prev = substr(out, length(out), 1)
					if (out == "" || prev == " " || prev == "\t") break
				}
				out = out c
			}
			return out
		}
		# norm spells every go command as go and turns command separators into ;.
		function norm(s) {
			gsub(/\{\{ *go *\}\}/, "go", s)
			gsub(/\$\(GO\)/, "go", s)
			gsub(/\$\{GO\}/, "go", s)
			gsub(/\$GO( |\t|$)/, "go ", s)
			gsub(/\$\([A-Za-z_][A-Za-z0-9_]*\)/, "V", s)
			gsub(/\$\(/, ";", s)
			gsub(/`/, ";", s)
			gsub(/[()]/, ";", s)
			gsub(/&&|\|\|/, ";", s)
			gsub(/[|&]/, ";", s)
			return s
		}
		function skippable(tok) {
			return tok == "" || tok ~ /^[A-Za-z_][A-Za-z0-9_]*=/ || tok == "RUN" || tok == "run:" || tok == "-" || tok == "!" \
				|| tok == "time" || tok == "exec" || tok == "env" || tok == "sudo" || tok == "command" || tok == "nice" \
				|| tok == "then" || tok == "do" || tok == "else"
		}
		function check(cmd, start,   count, parts, k, nt, t, i, j, verb, m, trim, pinned) {
			count = split(norm(code(cmd)), parts, ";")
			for (k = 1; k <= count; k++) {
				nt = split(parts[k], t, /[ \t]+/)
				for (i = 1; i <= nt; i++) sub(/^@+/, "", t[i])
				i = 1
				while (i <= nt && skippable(t[i])) i++
				if (i > nt) continue
				if (!(t[i] == "go" || t[i] ~ /\/go$/)) continue
				j = i + 1
				if (t[j] == "-C") j += 2
				else if (t[j] ~ /^-C./) j++
				verb = t[j]
				if (verb != "build" && verb != "install" && verb != "run") continue
				trim = 0; pinned = 0
				for (m = j + 1; m <= nt; m++) {
					if (t[m] == "-trimpath" || t[m] == "-trimpath=true") trim = 1
					if (verb != "build" && t[m] ~ /@/) pinned = 1
				}
				if (trim || pinned || exempt) continue
				printf "FAIL: %s:%d: go %s without -trimpath\n", file, start, verb
				bad = 1
			}
		}
		function flush() {
			if (cmd != "") check(cmd, start)
			cmd = ""
		}
		{
			line = $0
			stripped = line
			sub(/^[[:space:]]+/, "", stripped)
			if (cmd == "") {
				if (stripped ~ /^#/) {
					exempt = (stripped ~ /^# *trimpath-exempt: *[^ ]/)
					next
				}
				start = NR
			}
			joined = line
			if (joined ~ /\\[[:space:]]*$/) {
				sub(/\\[[:space:]]*$/, "", joined)
				cmd = cmd " " joined
				next
			}
			cmd = cmd " " joined
			flush()
			exempt = 0
		}
		END { flush(); exit bad }
	' "$f")" || status=1
	[ -n "$out" ] && echo "$out"
done

if [ "$status" -eq 0 ]; then
	echo "PASS: every go build, install and run passes -trimpath or is exempt"
fi
exit "$status"
