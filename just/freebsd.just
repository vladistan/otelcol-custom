# FreeBSD edge recipes. The binary is cross-built on Linux and never started
# here; the checks read its build settings and ELF header.

edge_freebsd_dir := "build/edge-freebsd"
edge_freebsd_bin := edge_freebsd_dir + "/otelcol-edge"

# Build the FreeBSD edge (freebsd/amd64, CGO off) in the edge build container.
# Same mount and stamps as build-edge-windows; the entrypoint files replace the
# OCB output.
build-edge-freebsd: build-edge-image
    #!/usr/bin/env bash
    set -euo pipefail
    trap 'echo "FreeBSD edge: a step failed (see above); the release stops until it passes" >&2' ERR
    echo "Building otelcol-edge {{version}} for freebsd/amd64..."
    mkdir -p {{edge_freebsd_dir}}
    {{docker_bin}} run --rm -i --platform linux/amd64 \
        -u "$(id -u):$(id -g)" -e HOME=/tmp -e GOCACHE=/tmp/gocache \
        -v "$PWD:$PWD" -w "$PWD" -v {{edge_gomod_volume}}:/go/pkg/mod \
        {{edge_build_image}} bash -euo pipefail -s <<'EOF'
    export CGO_ENABLED=0
    go run go.opentelemetry.io/collector/cmd/builder@v{{ocb_version_v164}} --config builder-edge-freebsd.yaml
    cp cmd/otelcol-custom/main.go cmd/otelcol-custom/svcwrapper.go cmd/otelcol-custom/optional.go {{edge_freebsd_dir}}/
    cd {{edge_freebsd_dir}}
    go get github.com/getsentry/sentry-go@v0.29.1
    go mod tidy
    GOOS=freebsd GOARCH=amd64 go build -buildvcs=false \
        -ldflags "{{base_ldflags}} -X main.serviceName=otelcol-edge" \
        -o otelcol-edge .
    EOF
    echo "Binary size:"
    ls -lh {{edge_freebsd_bin}}

# Fail unless the binary is a CGO-off freebsd/amd64 build stamped with VERSION
# and `file` reports a static FreeBSD ELF. The binary is not run.
check-edge-freebsd binary=edge_freebsd_bin:
    #!/usr/bin/env bash
    set -euo pipefail
    trap 'echo "FreeBSD edge: a step failed (see above); the release stops until it passes" >&2' ERR
    bin="{{binary}}"
    [ -f "$bin" ] || { echo "Missing binary: $bin" >&2; exit 1; }
    info=$(go version -m "$bin")
    for want in GOOS=freebsd GOARCH=amd64 CGO_ENABLED=0; do
        printf '%s\n' "$info" | grep -Eq "build[[:space:]]+${want}\$" \
            || { echo "FAIL: $bin lacks build setting $want" >&2; exit 1; }
    done
    stamped=$(printf '%s\n' "$info" | grep -m1 -o 'main\.version=[^ "]*' | cut -d= -f2 || true)
    [ "$stamped" = "{{version}}" ] \
        || { echo "FAIL: $bin version '${stamped}' != VERSION {{version}}" >&2; exit 1; }
    kind=$(file -b "$bin")
    echo "file: $kind"
    for want in "ELF 64-bit LSB executable" "x86-64" "FreeBSD" "statically linked"; do
        case "$kind" in
            *"$want"*) ;;
            *) echo "FAIL: $bin is not a static FreeBSD ELF (missing: $want)" >&2; exit 1 ;;
        esac
    done
    echo "OK: $bin is a static freebsd/amd64 ELF, CGO off, version {{version}}; not run"

# GOOS=freebsd go vet on every custom module the FreeBSD manifest replaces.
vet-edge-freebsd:
    #!/usr/bin/env bash
    set -euo pipefail
    trap 'echo "FreeBSD edge: a step failed (see above); the release stops until it passes" >&2' ERR
    mapfile -t dirs < <(sed -nE 's#^[[:space:]]*-[[:space:]]+github\.com/vladistan/otelcol-custom/[^[:space:]]+[[:space:]]+=>[[:space:]]+\.\./\.\./([^[:space:]]+).*#\1#p' builder-edge-freebsd.yaml)
    [ "${#dirs[@]}" -gt 0 ] || { echo "No custom modules found in builder-edge-freebsd.yaml" >&2; exit 1; }
    for d in "${dirs[@]}"; do
        echo "GOOS=freebsd go vet $d"
        GOOS=freebsd GOARCH=amd64 CGO_ENABLED=0 go -C "$d" vet ./...
    done
    echo "OK: ${#dirs[@]} custom modules vet clean for freebsd/amd64"

# Build the FreeBSD manifest for the host OS and run `validate` on a config. The
# components load the same keys on every OS, so this proves the config is
# accepted; the FreeBSD binary itself is not started.
validate-edge-freebsd-config config="test/configs/truenas-core-edge.yaml":
    #!/usr/bin/env bash
    set -euo pipefail
    [ -f "{{config}}" ] || { echo "Missing config: {{config}}" >&2; exit 1; }
    mkdir -p {{edge_freebsd_dir}}
    go run go.opentelemetry.io/collector/cmd/builder@v{{ocb_version_v164}} --config builder-edge-freebsd.yaml
    cp cmd/otelcol-custom/main.go cmd/otelcol-custom/svcwrapper.go cmd/otelcol-custom/optional.go {{edge_freebsd_dir}}/
    cd {{edge_freebsd_dir}}
    go get github.com/getsentry/sentry-go@v0.29.1
    go mod tidy
    CGO_ENABLED=0 go build -buildvcs=false \
        -ldflags "{{base_ldflags}} -X main.serviceName=otelcol-edge" \
        -o otelcol-edge-host .
    cd - >/dev/null
    OTELCOL_CUSTOM_DISABLE_TELEMETRY=1 ./{{edge_freebsd_dir}}/otelcol-edge-host validate --config="{{config}}"
    echo "OK: {{config}} validates"

# Fail unless builder-edge-freebsd.yaml differs from builder-edge-static.yaml
# only by the allowed list in the script.
check-edge-freebsd-manifest:
    scripts/check-edge-freebsd-manifest.sh

# Tests for check-edge-freebsd-manifest; no network.
test-check-edge-freebsd-manifest:
    scripts/test-check-edge-freebsd-manifest.sh

# Reference config, release wiring, build check and docs of the FreeBSD edge;
# starts no binary, no network.
test-freebsd-edge:
    scripts/test-freebsd-edge.sh
