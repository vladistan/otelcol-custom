#!/usr/bin/env bash
# Tests check-trimpath.sh against the repository and against small scratch trees.
# Usage: test-check-trimpath.sh
# Exit: 0 all pass, 1 a case failed.
set -euo pipefail
unset CDPATH

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly ROOT
readonly CHECK="$ROOT/scripts/check-trimpath.sh"
SCRATCH="$(mktemp -d)"
readonly SCRATCH
trap 'rm -rf "$SCRATCH"' EXIT

failures=0

# expect_exit_code NAME WANT_CODE CMD... : runs CMD, compares its exit code; the output stays in $SCRATCH/out.
expect_exit_code() {
	local name="$1" want="$2" got=0
	shift 2
	"$@" >"$SCRATCH/out" 2>&1 || got=$?
	if [ "$got" -eq "$want" ]; then
		echo "PASS: $name"
	else
		echo "FAIL: $name (want exit code $want, got $got)"
		sed 's/^/    /' "$SCRATCH/out"
		failures=$((failures + 1))
	fi
}

expect_output() {
	if grep -qF -- "$2" "$SCRATCH/out"; then
		echo "PASS: $1"
	else
		echo "FAIL: $1 (no match for $2)"
		sed 's/^/    /' "$SCRATCH/out"
		failures=$((failures + 1))
	fi
}

# tree NAME : creates an empty scratch tree and prints its path.
tree() {
	local dir="$SCRATCH/$1"
	mkdir -p "$dir/just" "$dir/.github/workflows"
	printf '%s' "$dir"
}

expect_exit_code "the repository passes" 0 "$CHECK"

dir="$(tree ok)"
cat >"$dir/justfile" <<'EOF'
build:
    cd build && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o out .
multi:
    go build -buildvcs=false \
        -trimpath \
        -o out .
EOF
expect_exit_code "a trimpath on one line and on a continuation line pass" 0 "$CHECK" "$dir"

dir="$(tree missing-one-line)"
cat >"$dir/justfile" <<'EOF'
build-gateway-linux:
    cd build/gateway && CGO_ENABLED=0 go build -ldflags "-s -w" -o out .
EOF
expect_exit_code "a one-line go build without trimpath fails" 1 "$CHECK" "$dir"
expect_output "the failure names the file" "justfile"

dir="$(tree missing-multi)"
cat >"$dir/just/freebsd.just" <<'EOF'
build:
    GOOS=freebsd go build -buildvcs=false \
        -ldflags "-s -w" \
        -o out .
EOF
expect_exit_code "a multi-line go build without trimpath fails" 1 "$CHECK" "$dir"
expect_output "the failure names the just file" "freebsd.just"

dir="$(tree dockerfile)"
cat >"$dir/Dockerfile.gateway" <<'EOF'
RUN cd build/gateway && \
    CGO_ENABLED=0 go build \
        -ldflags "-s -w" \
        -o otelcol-gateway .
EOF
expect_exit_code "a Dockerfile go build without trimpath fails" 1 "$CHECK" "$dir"
expect_output "the failure names the Dockerfile" "Dockerfile.gateway"

printf '%s\n' 'RUN CGO_ENABLED=0 go build -trimpath -o x .' >"$dir/Dockerfile.gateway"
expect_exit_code "a Dockerfile go build with trimpath passes" 0 "$CHECK" "$dir"

dir="$(tree workflow)"
cat >"$dir/.github/workflows/release.yml" <<'EOF'
jobs:
  build:
    steps:
      - name: Build the gateway
        run: go build -o otelcol-gateway .
EOF
expect_exit_code "a workflow go build without trimpath fails" 1 "$CHECK" "$dir"
expect_output "the failure names the workflow" "release.yml"

dir="$(tree exempt)"
cat >"$dir/justfile" <<'EOF'
vet:
    # trimpath-exempt: the test binary is never published
    go build ./...
EOF
expect_exit_code "an exempt marker with a reason passes" 0 "$CHECK" "$dir"

dir="$(tree exempt-empty)"
cat >"$dir/justfile" <<'EOF'
vet:
    # trimpath-exempt:
    go build ./...
EOF
expect_exit_code "an exempt marker without a reason fails" 1 "$CHECK" "$dir"

dir="$(tree comment)"
cat >"$dir/justfile" <<'EOF'
# go build is run here without any flag
build:
    echo ok
EOF
expect_exit_code "a comment that mentions go build is ignored" 0 "$CHECK" "$dir"

# one_file NAME PATH BODY : writes a scratch tree with one file holding BODY and prints the tree path.
one_file() {
	local dir
	dir="$(tree "$1")"
	mkdir -p "$(dirname "$dir/$2")"
	printf '%s\n' "$3" >"$dir/$2"
	printf '%s' "$dir"
}

# Every go build, install and run is checked on its own.
dir="$(one_file chain justfile $'build:\n    go build -o a . && go build -trimpath -o b .')"
expect_exit_code "a trimpath on the second of two chained builds does not cover the first" 1 "$CHECK" "$dir"
dir="$(one_file chain2 justfile $'build:\n    go build -trimpath -o a . && go build -o b .')"
expect_exit_code "a trimpath on the first of two chained builds does not cover the second" 1 "$CHECK" "$dir"
dir="$(one_file chain-ok justfile $'build:\n    go build -trimpath -o a . && go build -trimpath -o b .')"
expect_exit_code "two chained builds that both pass trimpath pass" 0 "$CHECK" "$dir"
dir="$(one_file semi justfile $'build:\n    go build -trimpath -o a .; go build -o b .')"
expect_exit_code "a build after a semicolon is checked" 1 "$CHECK" "$dir"
dir="$(one_file subshell justfile $'build:\n    (cd x && go build -o y .)')"
expect_exit_code "a build inside a subshell is checked" 1 "$CHECK" "$dir"

# Comments and quoted text are not code.
dir="$(one_file trailing justfile $'build:\n    go build -o a . # -trimpath is missing on purpose')"
expect_exit_code "a trimpath in a trailing comment does not count" 1 "$CHECK" "$dir"
dir="$(one_file ldflags justfile $'build:\n    go build -ldflags "-X main.note=-trimpath" -o a .')"
expect_exit_code "a trimpath inside an ldflags string does not count" 1 "$CHECK" "$dir"
dir="$(one_file echoed justfile $'say:\n    echo "run go build later"')"
expect_exit_code "go build inside a quoted string is not a build" 0 "$CHECK" "$dir"
dir="$(one_file falsy justfile $'build:\n    go build -trimpath=false -o a .')"
expect_exit_code "trimpath=false fails" 1 "$CHECK" "$dir"
dir="$(one_file truthy justfile $'build:\n    go build -trimpath=true -o a .')"
expect_exit_code "trimpath=true passes" 0 "$CHECK" "$dir"

# Spellings of the go command.
dir="$(one_file chdir justfile $'build:\n    go -C cmd/x build -o a .')"
expect_exit_code "go -C dir build is checked" 1 "$CHECK" "$dir"
dir="$(one_file chdir-ok justfile $'build:\n    go -C cmd/x build -trimpath -o a .')"
expect_exit_code "go -C dir build with trimpath passes" 0 "$CHECK" "$dir"
dir="$(one_file make-var justfile $'build:\n    $(GO) build -o a .')"
expect_exit_code "\$(GO) build is checked" 1 "$CHECK" "$dir"
dir="$(one_file just-var justfile $'build:\n    {{go}} build -o a .')"
expect_exit_code "{{go}} build is checked" 1 "$CHECK" "$dir"
dir="$(one_file just-var2 justfile $'build:\n    {{ go }} build -o a .')"
expect_exit_code "{{ go }} build is checked" 1 "$CHECK" "$dir"
dir="$(one_file sh-var justfile $'build:\n    ${GO} build -o a .')"
expect_exit_code "\${GO} build is checked" 1 "$CHECK" "$dir"
dir="$(one_file sh-var2 justfile $'build:\n    $GO build -o a .')"
expect_exit_code "\$GO build is checked" 1 "$CHECK" "$dir"
dir="$(one_file abs-go justfile $'build:\n    /usr/local/go/bin/go build -o a .')"
expect_exit_code "an absolute path to go is checked" 1 "$CHECK" "$dir"
dir="$(one_file env-go justfile $'build:\n    CGO_ENABLED=0 GOOS=linux /usr/local/go/bin/go build -trimpath -o a .')"
expect_exit_code "an absolute path to go with trimpath and variables passes" 0 "$CHECK" "$dir"

# go install and go run.
dir="$(one_file install justfile $'tools:\n    go install ./cmd/x')"
expect_exit_code "go install of local code without trimpath fails" 1 "$CHECK" "$dir"
dir="$(one_file install-ok justfile $'tools:\n    go install -trimpath ./cmd/x')"
expect_exit_code "go install of local code with trimpath passes" 0 "$CHECK" "$dir"
dir="$(one_file install-tool justfile $'tools:\n    go install example.com/tool/cmd/builder@v1.2.3')"
expect_exit_code "go install of a pinned tool is not a shipped build" 0 "$CHECK" "$dir"
dir="$(one_file run justfile $'gen:\n    go run ./cmd/gen')"
expect_exit_code "go run of local code without trimpath fails" 1 "$CHECK" "$dir"
dir="$(one_file run-tool justfile $'gen:\n    go run example.com/tool/cmd/builder@v1.2.3 --config x.yaml')"
expect_exit_code "go run of a pinned tool is not a shipped build" 0 "$CHECK" "$dir"
dir="$(one_file install-exempt justfile $'tools:\n    # trimpath-exempt: a developer tool, never shipped\n    go install ./cmd/x')"
expect_exit_code "go install with an exempt reason passes" 0 "$CHECK" "$dir"

# Where the check looks.
dir="$(one_file yaml-workflow .github/workflows/ci.yaml $'jobs:\n  b:\n    steps:\n      - run: go build -o x .')"
expect_exit_code "a .yaml workflow is checked" 1 "$CHECK" "$dir"
dir="$(one_file action .github/actions/build/action.yml $'runs:\n  using: composite\n  steps:\n    - shell: bash\n      run: go build -o x .')"
expect_exit_code "a composite action is checked" 1 "$CHECK" "$dir"
expect_output "the failure names the composite action" "action.yml"
dir="$(one_file nested-dockerfile docker/Dockerfile.base 'RUN go build -o x .')"
expect_exit_code "a Dockerfile in a subdirectory is checked" 1 "$CHECK" "$dir"
dir="$(one_file plain-dockerfile Dockerfile 'RUN go build -o x .')"
expect_exit_code "a Dockerfile without a suffix is checked" 1 "$CHECK" "$dir"
dir="$(one_file script scripts/build.sh $'#!/usr/bin/env bash\ngo build -o x .')"
expect_exit_code "a shell script under scripts is checked" 1 "$CHECK" "$dir"
expect_output "the failure names the script" "build.sh"
dir="$(one_file test-script test/scrape/run.sh $'#!/usr/bin/env bash\n(cd p && go build -o x .)')"
expect_exit_code "a shell script under test is checked" 1 "$CHECK" "$dir"
dir="$(one_file generated build/edge/Dockerfile 'RUN go build -o x .')"
expect_exit_code "a generated build directory is skipped" 0 "$CHECK" "$dir"

expect_exit_code "a missing directory is a usage error" 2 "$CHECK" "$SCRATCH/does-not-exist"

if [ "$failures" -ne 0 ]; then
	echo "$failures case(s) failed"
	exit 1
fi
echo "all cases passed"
