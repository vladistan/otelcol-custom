# Windows edge recipes (plan 08 track T1). cmd/otelcol-custom compiles only
# inside the OCB-generated module, so every check here runs in build/edge-windows.

edge_windows_dir := "build/edge-windows"
edge_windows_exe := edge_windows_dir + "/otelcol-edge.exe"

# Build the Windows edge (windows/amd64, CGO off) in the edge build container.
# Same-path mount keeps embedded source paths equal to the host's (Sentry
# source context); -buildvcs=false because a worktree's .git sits outside the
# mount. Every non-test file in cmd/otelcol-custom replaces the OCB output.
build-edge-windows: build-edge-image
    #!/usr/bin/env bash
    set -euo pipefail
    echo "Building otelcol-edge {{version}} for windows/amd64..."
    mkdir -p {{edge_windows_dir}}
    {{docker_bin}} run --rm -i --platform linux/amd64 \
        -u "$(id -u):$(id -g)" -e HOME=/tmp -e GOCACHE=/tmp/gocache \
        -v "$PWD:$PWD" -w "$PWD" -v {{edge_gomod_volume}}:/go/pkg/mod \
        {{edge_build_image}} bash -euo pipefail -s <<'EOF'
    export CGO_ENABLED=0
    go run go.opentelemetry.io/collector/cmd/builder@v{{ocb_version_v154}} --config builder-edge-windows.yaml
    for f in cmd/otelcol-custom/*.go; do
        case "$f" in
            *_test.go) ;;
            *) cp "$f" {{edge_windows_dir}}/ ;;
        esac
    done
    cd {{edge_windows_dir}}
    go get github.com/getsentry/sentry-go@v0.29.1
    go mod tidy
    GOOS=windows GOARCH=amd64 go build -buildvcs=false \
        -ldflags "{{base_ldflags}} -X main.serviceName=otelcol-edge" \
        -o otelcol-edge.exe .
    EOF
    echo "Binary size:"
    ls -lh {{edge_windows_exe}}

# Fail unless the binary is a CGO-off windows/amd64 build stamped with VERSION,
# and the generated module vets and compiles its tests for windows.
check-edge-windows binary=edge_windows_exe:
    #!/usr/bin/env bash
    set -euo pipefail
    bin="{{binary}}"
    [ -f "$bin" ] || { echo "Missing binary: $bin" >&2; exit 1; }
    info=$(go version -m "$bin")
    for want in GOOS=windows GOARCH=amd64 CGO_ENABLED=0; do
        printf '%s\n' "$info" | grep -Eq "build[[:space:]]+${want}\$" \
            || { echo "FAIL: $bin lacks build setting $want" >&2; exit 1; }
    done
    stamped=$(printf '%s\n' "$info" | grep -m1 -o 'main\.version=[^ "]*' | cut -d= -f2 || true)
    [ "$stamped" = "{{version}}" ] \
        || { echo "FAIL: $bin version '${stamped}' != VERSION {{version}}" >&2; exit 1; }
    for sub in vet "test -c -o /dev/null"; do
        echo "GOOS=windows go $sub ({{edge_windows_dir}})"
        GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go -C {{edge_windows_dir}} $sub .
    done
    echo "OK: $bin is windows/amd64, CGO off, version {{version}}"

# Run the rotating-sink tests inside the generated module (cmd/otelcol-custom
# does not compile alone) and fail under 80% statement coverage of
# rotatingsink.go.
test-rotating-sink: generate-edge-windows
    #!/usr/bin/env bash
    set -euo pipefail
    cp cmd/otelcol-custom/rotatingsink*.go {{edge_windows_dir}}/
    cd {{edge_windows_dir}}
    go get gopkg.in/natefinch/lumberjack.v2
    go test -covermode=set -coverprofile=rotatingsink.cover -run 'Rotating' ./...
    awk '$1 ~ /rotatingsink\.go:/ { total += $2; if ($3 > 0) cov += $2 }
         END { pct = total ? 100 * cov / total : 0
               printf "rotatingsink.go statement coverage: %.1f%%\n", pct
               exit (pct < 80) }' rotatingsink.cover

# Build the Windows manifest for the host OS and run `validate` on a config.
validate-edge-windows-config config:
    #!/usr/bin/env bash
    set -euo pipefail
    [ -f "{{config}}" ] || { echo "Missing config: {{config}}" >&2; exit 1; }
    mkdir -p {{edge_windows_dir}}
    go run go.opentelemetry.io/collector/cmd/builder@v{{ocb_version_v154}} --config builder-edge-windows.yaml
    for f in cmd/otelcol-custom/*.go; do
        case "$f" in
            *_test.go) ;;
            *) cp "$f" {{edge_windows_dir}}/ ;;
        esac
    done
    cd {{edge_windows_dir}}
    go get github.com/getsentry/sentry-go@v0.29.1
    go mod tidy
    CGO_ENABLED=0 go build -buildvcs=false \
        -ldflags "{{base_ldflags}} -X main.serviceName=otelcol-edge" \
        -o otelcol-edge-host .
    cd - >/dev/null
    rc=0
    out=$(OTELCOL_CUSTOM_DISABLE_TELEMETRY=1 ./{{edge_windows_dir}}/otelcol-edge-host validate --config="{{config}}" 2>&1) || rc=$?
    printf '%s\n' "$out"
    [ "$rc" -eq 0 ] && exit 0
    # Config keys and pipeline wiring were accepted; only the host OS refused
    # to create a Windows-only receiver.
    if printf '%s\n' "$out" | grep -q 'failed to build pipelines: .*only supported on Windows'; then
        echo "OK: config accepted; host OS cannot instantiate Windows-only receivers"
        exit 0
    fi
    exit "$rc"
