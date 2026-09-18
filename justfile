# otelcol-custom build commands
# Requires: Go 1.23+, OCB (OpenTelemetry Collector Builder)

# OCB and collector versions
# Two OCB versions are pinned: default uses v0.115.0, edge/gateway/scrape use
# v0.140.0. OCB's generated go.mod tracks the OCB release, not the component
# versions it builds, so a mismatched OCB produces an unresolvable go.mod
# (Phase 10 Steps 10.3/10.4). Each generate recipe pins its own OCB version
# via `go run ...@version` instead of sharing one installed `builder` binary.
ocb_version := "0.115.0"
ocb_version_v140 := "0.140.0"
otel_version := "0.115.0"

# Read version from VERSION file (single source of truth)
version := `cat VERSION | tr -d '\n'`
commit := `git rev-parse --short HEAD 2>/dev/null || echo "unknown"`
build_date := `date -u +%Y-%m-%dT%H:%M:%SZ`

# Base ldflags (version and commit, service name added per-profile)
base_ldflags := "-s -w -X main.version=" + version + " -X main.commit=" + commit

# Staging directory for public release artifacts (Phase 2, gitignored)
dist_public_dir := "dist/public/" + version

# Alpine image used to run staged binaries in verify-public-artifacts. Must
# match the runtime-stage digest pinned in Dockerfile.gateway and
# Dockerfile.scrape - check-base-images-sync (below) fails if it drifts.
alpine_verify_image := "alpine@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce"

# Default recipe - show available commands
default:
    @just --list

# =============================================================================
# Version Management
# =============================================================================

# Show current version
show-version:
    @echo "{{version}}"

# Bump patch version (0.1.0 -> 0.1.1)
bump:
    #!/usr/bin/env bash
    set -euo pipefail
    current=$(cat VERSION | tr -d '\n')
    IFS='.' read -r major minor patch <<< "$current"
    new_patch=$((patch + 1))
    new_version="${major}.${minor}.${new_patch}"
    echo "$new_version" > VERSION
    echo "Bumped version: $current -> $new_version"

# Bump minor version (0.1.0 -> 0.2.0)
bump-minor:
    #!/usr/bin/env bash
    set -euo pipefail
    current=$(cat VERSION | tr -d '\n')
    IFS='.' read -r major minor patch <<< "$current"
    new_minor=$((minor + 1))
    new_version="${major}.${new_minor}.0"
    echo "$new_version" > VERSION
    echo "Bumped version: $current -> $new_version"

# Bump major version (0.1.0 -> 1.0.0)
bump-major:
    #!/usr/bin/env bash
    set -euo pipefail
    current=$(cat VERSION | tr -d '\n')
    IFS='.' read -r major minor patch <<< "$current"
    new_major=$((major + 1))
    new_version="${new_major}.0.0"
    echo "$new_version" > VERSION
    echo "Bumped version: $current -> $new_version"

# =============================================================================
# OCB and Dependencies
# =============================================================================

# Install OCB (OpenTelemetry Collector Builder) matching the default variant.
# Not required by the generate recipes below (each pins its own OCB version via
# `go run`); this is for interactively invoking `builder` by hand.
install-ocb:
    go install go.opentelemetry.io/collector/cmd/builder@v{{ocb_version}}

# =============================================================================
# Full Collector (otelcol-custom) - includes all components
# =============================================================================

# Generate collector code using OCB (pinned to the default variant's OCB version)
generate:
    go run go.opentelemetry.io/collector/cmd/builder@v{{ocb_version}} --config builder-config.yaml

# Build the full collector
build: generate
    @echo "Building otelcol-custom {{version}}..."
    cp cmd/otelcol-custom/main.go build/main.go
    cd build && go get github.com/getsentry/sentry-go@v0.29.1
    cd build && go mod tidy
    cd build && CGO_ENABLED=0 go build -ldflags "{{base_ldflags}} -X main.serviceName=otelcol-custom" -o otelcol-custom .
    @echo "Binary size:"
    ls -lh build/otelcol-custom

# Build full collector for Linux (cross-compile from macOS)
build-linux: generate
    @echo "Building otelcol-custom {{version}} for Linux amd64..."
    cp cmd/otelcol-custom/main.go build/main.go
    cd build && go get github.com/getsentry/sentry-go@v0.29.1
    cd build && go mod tidy
    cd build && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "{{base_ldflags}} -X main.serviceName=otelcol-custom" -o otelcol-custom .
    @echo "Binary size:"
    ls -lh build/otelcol-custom

# Verify the built binary
verify:
    ./build/otelcol-custom --version
    @echo ""
    @echo "Available components:"
    ./build/otelcol-custom components

# =============================================================================
# Edge Collector (otelcol-edge) - lean collector for VMs and bare metal
# =============================================================================

# Generate edge collector code using OCB (pinned to the v0.140.0-component OCB version)
generate-edge:
    go run go.opentelemetry.io/collector/cmd/builder@v{{ocb_version_v140}} --config builder-edge.yaml

# Build the edge collector
build-edge: generate-edge
    @echo "Building otelcol-edge {{version}}..."
    cp cmd/otelcol-custom/main.go build/edge/main.go
    cd build/edge && go get github.com/getsentry/sentry-go@v0.29.1
    cd build/edge && go mod tidy
    cd build/edge && CGO_ENABLED=0 go build -ldflags "{{base_ldflags}} -X main.serviceName=otelcol-edge" -o otelcol-edge .
    @echo "Binary size:"
    ls -lh build/edge/otelcol-edge

# Build edge collector for Linux (cross-compile from macOS)
build-edge-linux: generate-edge
    @echo "Building otelcol-edge {{version}} for Linux amd64..."
    cp cmd/otelcol-custom/main.go build/edge/main.go
    cd build/edge && go get github.com/getsentry/sentry-go@v0.29.1
    cd build/edge && go mod tidy
    cd build/edge && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "{{base_ldflags}} -X main.serviceName=otelcol-edge" -o otelcol-edge .
    @echo "Binary size:"
    ls -lh build/edge/otelcol-edge

# Verify edge collector binary
verify-edge:
    ./build/edge/otelcol-edge --version
    @echo ""
    @echo "Available components:"
    ./build/edge/otelcol-edge components

# Full edge build and verify
all-edge: test-custom build-edge verify-edge

# =============================================================================
# Scrape Collector (otelcol-scrape) - cloud source scraping (future)
# =============================================================================

# Generate scrape collector code using OCB (pinned to the v0.140.0-component OCB version)
generate-scrape:
    go run go.opentelemetry.io/collector/cmd/builder@v{{ocb_version_v140}} --config builder-scrape.yaml

# Build the scrape collector
build-scrape: generate-scrape
    @echo "Building otelcol-scrape {{version}}..."
    cp cmd/otelcol-custom/main.go build/scrape/main.go
    cd build/scrape && go get github.com/getsentry/sentry-go@v0.29.1
    cd build/scrape && go mod tidy
    cd build/scrape && CGO_ENABLED=0 go build -ldflags "{{base_ldflags}} -X main.serviceName=otelcol-scrape" -o otelcol-scrape .
    @echo "Binary size:"
    ls -lh build/scrape/otelcol-scrape

# Build scrape collector for Linux
build-scrape-linux: generate-scrape
    @echo "Building otelcol-scrape {{version}} for Linux amd64..."
    cp cmd/otelcol-custom/main.go build/scrape/main.go
    cd build/scrape && go get github.com/getsentry/sentry-go@v0.29.1
    cd build/scrape && go mod tidy
    cd build/scrape && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "{{base_ldflags}} -X main.serviceName=otelcol-scrape" -o otelcol-scrape .
    @echo "Binary size:"
    ls -lh build/scrape/otelcol-scrape

# Verify scrape collector binary
verify-scrape:
    ./build/scrape/otelcol-scrape --version
    @echo ""
    @echo "Available components:"
    ./build/scrape/otelcol-scrape components

# Build the scrape image, local tag only (FR-028). No registry namespace and
# no push recipe - no scrape publication is authorized by this work. Deliberately
# absent from release-public, release-gateway and release.yml.
docker-build-scrape:
    @echo "Building local Docker image for otelcol-scrape v{{version}}..."
    docker build --platform linux/amd64 \
        --build-arg VERSION={{version}} --build-arg COMMIT={{commit}} \
        -t otelcol-scrape:local -f Dockerfile.scrape .
    @echo "Image built: otelcol-scrape:local"

# =============================================================================
# Gateway Collector (otelcol-gateway) - central aggregation (future)
# =============================================================================

# Generate gateway collector code using OCB (pinned to the v0.140.0-component OCB version)
generate-gateway:
    go run go.opentelemetry.io/collector/cmd/builder@v{{ocb_version_v140}} --config builder-gateway.yaml

# Build the gateway collector
build-gateway: generate-gateway
    @echo "Building otelcol-gateway {{version}}..."
    cp cmd/otelcol-custom/main.go build/gateway/main.go
    cd build/gateway && go get github.com/getsentry/sentry-go@v0.29.1
    cd build/gateway && go mod tidy
    cd build/gateway && CGO_ENABLED=0 go build -ldflags "{{base_ldflags}} -X main.serviceName=otelcol-gateway" -o otelcol-gateway .
    @echo "Binary size:"
    ls -lh build/gateway/otelcol-gateway

# Build gateway collector for Linux
build-gateway-linux: generate-gateway
    @echo "Building otelcol-gateway {{version}} for Linux amd64..."
    cp cmd/otelcol-custom/main.go build/gateway/main.go
    cd build/gateway && go get github.com/getsentry/sentry-go@v0.29.1
    cd build/gateway && go mod tidy
    cd build/gateway && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "{{base_ldflags}} -X main.serviceName=otelcol-gateway" -o otelcol-gateway .
    @echo "Binary size:"
    ls -lh build/gateway/otelcol-gateway

# Verify gateway collector binary
verify-gateway:
    ./build/gateway/otelcol-gateway --version
    @echo ""
    @echo "Available components:"
    ./build/gateway/otelcol-gateway components

# ECR repository for gateway
ecr_registry := "123456789012.dkr.ecr.us-east-1.amazonaws.com"
ecr_repo_gateway := ecr_registry + "/otelcol-gateway"

# GHCR repository for the public gateway image (Phase 5 of the public-release plan)
ghcr_registry := "ghcr.io/vladistan"
ghcr_repo_gateway := ghcr_registry + "/otelcol-gateway"

# Docker build gateway image
docker-build-gateway:
    @echo "Building Docker image for otelcol-gateway v{{version}}..."
    docker build -t {{ecr_repo_gateway}}:v{{version}} -f Dockerfile.gateway .
    @echo "Image built: {{ecr_repo_gateway}}:v{{version}}"

# Push gateway image to ECR
docker-push-gateway:
    @echo "Logging into ECR..."
    aws ecr get-login-password --region us-east-1 | docker login --username AWS --password-stdin {{ecr_registry}}
    @echo "Pushing {{ecr_repo_gateway}}:v{{version}}..."
    docker push {{ecr_repo_gateway}}:v{{version}}
    @echo "Pushed: {{ecr_repo_gateway}}:v{{version}}"

# Build and push gateway image to ECR
release-gateway: build-gateway-linux docker-build-gateway docker-push-gateway
    @echo "Gateway v{{version}} released to ECR"

# Build the public gateway image for GHCR (linux/amd64, version tag only)
docker-build-gateway-ghcr:
    @echo "Building public Docker image for otelcol-gateway v{{version}}..."
    docker build --platform linux/amd64 \
        --build-arg VERSION={{version}} --build-arg COMMIT={{commit}} \
        -t {{ghcr_repo_gateway}}:v{{version}} -f Dockerfile.gateway .
    @echo "Image built: {{ghcr_repo_gateway}}:v{{version}}"

# Push the public gateway image to GHCR (reuses the existing gh CLI login)
docker-push-ghcr:
    @echo "Logging into ghcr.io..."
    gh auth token | docker login ghcr.io -u vladistan --password-stdin
    @echo "Pushing {{ghcr_repo_gateway}}:v{{version}}..."
    docker push {{ghcr_repo_gateway}}:v{{version}}
    @echo "Pushed: {{ghcr_repo_gateway}}:v{{version}}"

# =============================================================================
# Build All Profiles
# =============================================================================

# Build all collector profiles for current OS
build-all: build build-edge
    @echo ""
    @echo "All profiles built for {{version}}"

# Build all collector profiles for Linux
build-all-linux: build-linux build-edge-linux
    @echo ""
    @echo "All profiles built for Linux ({{version}})"

# =============================================================================
# Local Development - Run Collectors Locally
# =============================================================================

# Run gateway collector locally with test config
run-local-gateway: build-gateway
    ./build/gateway/otelcol-gateway --config test/configs/local-gateway.yaml

# Run gateway with hot-reload (requires entr: brew install entr)
run-local-gateway-watch:
    find . -name '*.go' -o -name '*.yaml' | grep -v build/ | entr -r just run-local-gateway

# Run edge collector locally with test config
run-local-edge: build-edge
    ./build/edge/otelcol-edge --config test/configs/local-edge.yaml

# Run edge with hot-reload (requires entr: brew install entr)
run-local-edge-watch:
    find . -name '*.go' -o -name '*.yaml' | grep -v build/ | entr -r just run-local-edge

# Run full collector locally with test config
run-local: build
    ./build/otelcol-custom --config test/configs/local-gateway.yaml

# Run full collector with hot-reload
run-local-watch:
    find . -name '*.go' -o -name '*.yaml' | grep -v build/ | entr -r just run-local

# =============================================================================
# Testing and Quality
# =============================================================================

# Run tests for custom components
test-custom:
    @echo "Testing custom receivers..."
    go test ./receiver/... -v
    @echo ""
    @echo "Testing custom processors..."
    go test ./processor/... -v
    @echo ""
    @echo "Testing custom stanza operators..."
    cd stanza/operator/transformer/arraytostring && go test -v

# Run tests for every nested Go module under processor/, receiver/, stanza/,
# dynamically discovered rather than hardcoded (FR-029).
test-nested-modules:
    #!/usr/bin/env bash
    set -uo pipefail
    modules=$(find processor receiver stanza -name go.mod -exec dirname {} \; | sort)
    if [ -z "$modules" ]; then
        echo "error: no nested Go modules found under processor/, receiver/, stanza/" >&2
        exit 1
    fi
    status=0
    for mod in $modules; do
        echo "=== $mod ==="
        if (cd "$mod" && go test ./...); then
            echo "PASS: $mod"
        else
            echo "FAIL: $mod"
            status=1
        fi
    done
    exit "$status"

# Run tests for generated build
test-build:
    cd build && go test ./...

# Run all tests (custom + build). test-nested-modules is intentionally not
# included here: 3 of its 28 modules carry pre-existing, PUB_MANIFEST.md-
# documented accepted-baseline defects (missing go.sum / a stale assertion),
# so wiring it in would make this aggregate fail by default over issues
# unrelated to whatever change triggered the run. Invoke it directly.
test: test-custom test-build

# 28 nested Go modules under processor/, receiver/, and stanza/ have their own
# go.mod. A root-level glob cannot see them, so each one is checked directly.
# See the "Accepted Baseline Defects" notes in the project's development docs.
_check-all-modules name +cmd:
    #!/usr/bin/env bash
    set -uo pipefail
    status=0
    echo "=== root module (./...) ==="
    {{cmd}} ./...
    rc=$?
    echo "{{name}} exit: $rc"
    [ "$rc" -ne 0 ] && status=1
    modules=$(find processor receiver stanza -name go.mod -exec dirname {} \; | sort)
    if [ -z "$modules" ]; then
        echo "error: no nested Go modules found under processor/, receiver/, stanza/" >&2
        exit 1
    fi
    for mod in $modules; do
        echo "=== $mod ==="
        (cd "$mod" && {{cmd}} ./...)
        rc=$?
        echo "{{name}} exit: $rc"
        [ "$rc" -ne 0 ] && status=1
    done
    exit $status

# Lint custom components, including every nested Go module
lint: (_check-all-modules "lint" "golangci-lint" "run")

# Format custom components, including every nested Go module
fmt: (_check-all-modules "fmt" "go" "fmt")

# Check custom components for issues, including every nested Go module
vet: (_check-all-modules "vet" "go" "vet")

# Full build and verify
all: test-custom build verify

# =============================================================================
# Sentry Release Management
# =============================================================================

# Sentry configuration
sentry_org := "sentry"
sentry_project := "otel-collector"

# Create a new Sentry release and upload source files
sentry-release profile="edge":
    #!/usr/bin/env bash
    set -euo pipefail
    release="otelcol-{{profile}}@{{version}}"
    build_dir="$(pwd)"
    echo "Creating Sentry release: $release"

    # Create release
    sentry-cli releases new "$release"

    # Upload source files with exact build paths for Go source context
    # Go embeds full paths in binaries, so we must match them exactly
    echo "Uploading source files..."

    # Upload main.go from build directory (where it's compiled from)
    sentry-cli releases files "$release" upload \
        "./build/{{profile}}/main.go" \
        "${build_dir}/build/{{profile}}/main.go"

    # Upload custom receivers and processors
    find ./receiver ./processor -name "*.go" -print0 | while IFS= read -r -d '' file; do
        sentry-cli releases files "$release" upload "$file" "${build_dir}/${file#./}"
    done

    # Set commits (if in git repo and has permission)
    if git rev-parse --git-dir > /dev/null 2>&1; then
        echo "Setting commits..."
        sentry-cli releases set-commits "$release" --auto || echo "Warning: Could not set commits (missing permissions)"
    fi

    # Finalize release
    sentry-cli releases finalize "$release"
    echo "Sentry release $release created and finalized"

# Build edge and upload to Sentry
release-edge: build-edge-linux sentry-release

# =============================================================================
# Public Release Artifacts (Phase 2 of the public-release plan)
# =============================================================================

# Stage edge and gateway binaries under public artifact names, with a checksum manifest
stage-public-artifacts: build-edge-linux build-gateway-linux
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p "{{dist_public_dir}}"
    cp build/edge/otelcol-edge "{{dist_public_dir}}/otelcol-edge-{{version}}-linux-amd64"
    cp build/gateway/otelcol-gateway "{{dist_public_dir}}/otelcol-gateway-{{version}}-linux-amd64"
    just checksum-public-artifacts
    echo "Staged public artifacts for {{version}}:"
    ls -lh "{{dist_public_dir}}"

# Generate SHA256SUMS over exactly the files present in the staging directory
checksum-public-artifacts:
    #!/usr/bin/env bash
    set -euo pipefail
    dir="{{dist_public_dir}}"
    if [ ! -d "$dir" ]; then
        echo "No files to checksum in $dir - run stage-public-artifacts first" >&2
        exit 1
    fi
    cd "$dir"
    # Capture the file list before writing SHA256SUMS: the redirect below
    # creates/truncates that file, and a `*` glob evaluated after would
    # race the redirect and pick up the freshly-truncated file itself.
    files=()
    for f in *; do
        [ -f "$f" ] || continue
        [ "$f" = "SHA256SUMS" ] && continue
        files+=("$f")
    done
    if [ ${#files[@]} -eq 0 ]; then
        echo "No files to checksum in $dir - run stage-public-artifacts first" >&2
        exit 1
    fi
    sha256sum -- "${files[@]}" | sort -k2 > SHA256SUMS
    cat SHA256SUMS

# Verify staged binaries run on linux/amd64 and report the VERSION-file version
verify-public-artifacts:
    #!/usr/bin/env bash
    set -euo pipefail
    dir="{{dist_public_dir}}"
    ver="{{version}}"
    for variant in edge gateway; do
        bin="otelcol-${variant}-${ver}-linux-amd64"
        path="${dir}/${bin}"
        if [ ! -f "$path" ]; then
            echo "Missing staged binary: $path - run stage-public-artifacts first" >&2
            exit 1
        fi
        echo "Verifying ${bin}..."
        version_out=$(docker run --rm --platform linux/amd64 -v "$PWD/${dir}:/artifacts:ro" {{alpine_verify_image}} "/artifacts/${bin}" --version)
        echo "$version_out" | grep -q "$ver" || { echo "Version mismatch for ${bin}: expected ${ver}, got: ${version_out}" >&2; exit 1; }
        components_out=$(docker run --rm --platform linux/amd64 -v "$PWD/${dir}:/artifacts:ro" {{alpine_verify_image}} "/artifacts/${bin}" components)
        [ -n "$components_out" ] || { echo "Empty components list for ${bin}" >&2; exit 1; }
        echo "OK: ${bin} reports version ${ver} ($(echo "$components_out" | wc -l | tr -d ' ') component lines)"
    done

# Prove the golang and alpine base-image pins agree everywhere they are
# consumed, by resolving each digest to its real advertised version rather
# than comparing pins as text (FR-027). Reports the drifted site and exits
# non-zero; never mutates a pin.
check-base-images-sync:
    #!/usr/bin/env bash
    set -euo pipefail
    status=0

    ref_justfile="../../.claude/templates/go-cli-release/justfile.reference"
    for f in Dockerfile.gateway Dockerfile.scrape .github/workflows/release.yml "$ref_justfile"; do
        [ -f "$f" ] || { echo "error: expected file not found: $f" >&2; exit 1; }
    done

    extract_digest() {
        grep -m1 -oE "${2}@sha256:[0-9a-f]{64}" "$1"
    }

    gw_go=$(extract_digest Dockerfile.gateway golang)
    sc_go=$(extract_digest Dockerfile.scrape golang)
    gw_alpine=$(extract_digest Dockerfile.gateway alpine)
    sc_alpine=$(extract_digest Dockerfile.scrape alpine)
    jf_alpine=$(grep -m1 -oE 'alpine@sha256:[0-9a-f]{64}' justfile)
    ref_alpine=$(grep -m1 -oE 'alpine@sha256:[0-9a-f]{64}' "$ref_justfile")

    echo "golang pins: Dockerfile.gateway=${gw_go} Dockerfile.scrape=${sc_go}"
    if [ "$sc_go" != "$gw_go" ]; then
        echo "DRIFT: golang digest at Dockerfile.scrape (${sc_go}) disagrees with Dockerfile.gateway (${gw_go})" >&2
        status=1
    fi

    echo "alpine pins: Dockerfile.gateway=${gw_alpine} Dockerfile.scrape=${sc_alpine} justfile=${jf_alpine} justfile.reference=${ref_alpine}"
    for pair in "Dockerfile.scrape:${sc_alpine}" "justfile (alpine_verify_image):${jf_alpine}" "justfile.reference:${ref_alpine}"; do
        site="${pair%%:*}"
        val="${pair#*:}"
        if [ "$val" != "$gw_alpine" ]; then
            echo "DRIFT: alpine digest at ${site} (${val}) disagrees with Dockerfile.gateway (${gw_alpine})" >&2
            status=1
        fi
    done

    # Resolve each digest to its real advertised version by inspecting the
    # image directly, rather than trusting the pin's own inline comment.
    go_resolved=$(docker run --rm --platform linux/amd64 "$gw_go" go version | awk '{print $3}' | sed 's/^go//')
    alpine_resolved=$(docker run --rm --platform linux/amd64 "$gw_alpine" cat /etc/os-release | grep '^VERSION_ID=' | cut -d= -f2 | tr -d '"')
    release_go=$(grep -m1 -oE 'go-version:[[:space:]]*"[0-9.]+"' .github/workflows/release.yml | grep -m1 -oE '[0-9]+\.[0-9]+')

    echo "resolved: golang=${go_resolved} (release.yml declares ${release_go}), alpine=${alpine_resolved}"
    case "$go_resolved" in
        "${release_go}".*|"${release_go}")
            ;;
        *)
            echo "DRIFT: release.yml go-version (${release_go}) does not match the resolved golang image version (${go_resolved})" >&2
            status=1
            ;;
    esac

    exit "$status"

# =============================================================================
# GitHub Release as the Assembly Point (Phase 3 of the public-release plan)
# =============================================================================

# Public repository the GitHub Release targets, per docs/public-release.md
public_repo := "vladistan/otelcol-custom"

# Create the GitHub Release for the current VERSION on the public repo if it
# does not exist yet (cutting the vVERSION tag from the public trunk in the
# same call), or reuse it unchanged if it already exists.
release-tag:
    #!/usr/bin/env bash
    set -euo pipefail
    tag="v{{version}}"
    repo="{{public_repo}}"
    if gh release view "$tag" --repo "$repo" >/dev/null 2>&1; then
        echo "Release $tag already exists on $repo - reusing"
    else
        gh release create "$tag" --repo "$repo" \
            --title "$tag" \
            --notes "otelcol-custom $tag" \
            --target main
        echo "Created release $tag on $repo"
    fi

# Attach the edge binary, gateway binary and SHA256SUMS to the Release for the
# current VERSION. Re-running is idempotent: --clobber overwrites existing
# assets by name in place rather than producing duplicate or suffixed names.
release-upload: release-tag
    #!/usr/bin/env bash
    set -euo pipefail
    dir="{{dist_public_dir}}"
    tag="v{{version}}"
    repo="{{public_repo}}"
    if [ ! -d "$dir" ] || [ -z "$(ls -A "$dir" 2>/dev/null)" ]; then
        echo "Staging directory $dir is missing or empty - run stage-public-artifacts first" >&2
        exit 1
    fi
    gh release upload "$tag" "$dir"/* --repo "$repo" --clobber
    echo "Uploaded assets from $dir to release $tag on $repo:"
    gh release view "$tag" --repo "$repo" --json assets --jq '.assets[].name'

# Download the current VERSION's release assets anonymously (no gh auth) and
# verify them against the locally staged SHA256SUMS
verify-release-download:
    #!/usr/bin/env bash
    set -euo pipefail
    tag="v{{version}}"
    repo="{{public_repo}}"
    local_sums="{{dist_public_dir}}/SHA256SUMS"
    if [ ! -f "$local_sums" ]; then
        echo "Missing $local_sums - run stage-public-artifacts first" >&2
        exit 1
    fi
    tmp=$(mktemp -d)
    trap 'rm -rf "$tmp"' EXIT
    base="https://github.com/${repo}/releases/download/${tag}"
    for name in "otelcol-edge-{{version}}-linux-amd64" "otelcol-gateway-{{version}}-linux-amd64" "SHA256SUMS"; do
        echo "Downloading $name anonymously..."
        curl -fsSL -o "$tmp/$name" "$base/$name"
    done
    if ! diff -q "$tmp/SHA256SUMS" "$local_sums" >/dev/null; then
        echo "Downloaded SHA256SUMS differs from the local staged checksum file:" >&2
        diff "$tmp/SHA256SUMS" "$local_sums" >&2 || true
        exit 1
    fi
    (cd "$tmp" && sha256sum -c SHA256SUMS)
    echo "Anonymous download verified against local checksums for $tag"

# Dry-run the tag/release/upload flow end to end under an rc-suffixed version,
# so the first real version tag is cut only after this has proven clean.
# Leaves the rc release/tag in place; clean up with the printed gh command.
release-rc-dry-run: stage-public-artifacts
    #!/usr/bin/env bash
    set -euo pipefail
    dir="{{dist_public_dir}}"
    rc_version="{{version}}-rc1"
    tag="v${rc_version}"
    repo="{{public_repo}}"
    echo "RC dry run: tagging and uploading under $tag (not a real release)"
    if gh release view "$tag" --repo "$repo" >/dev/null 2>&1; then
        gh release delete "$tag" --repo "$repo" --yes --cleanup-tag
    fi
    gh release create "$tag" --repo "$repo" \
        --title "$tag" \
        --notes "RC dry run for otelcol-custom v{{version}}" \
        --target main \
        --prerelease
    gh release upload "$tag" "$dir"/* --repo "$repo" --clobber
    echo "RC dry run complete: $tag created with assets on $repo"
    echo "Clean up with: gh release delete $tag --repo $repo --yes --cleanup-tag"

# =============================================================================
# Public Binary Downloads on the Website (Phase 4)
# =============================================================================

# Directory the Release's assets are downloaded into and the site is assembled in
site_assets_dir := "dist/site-assets"
site_dir := "_site"

# Assemble and deploy the public download page as a projection of the Release
# (never of local build output), then verify what was actually published by
# reading response bodies rather than trusting a status code. Safe to re-run:
# a second run over an unchanged Release republishes byte-identical artifacts.
publish-site:
    #!/usr/bin/env bash
    set -euo pipefail
    tag="v{{version}}"
    repo="{{public_repo}}"
    assets="{{site_assets_dir}}"
    site="{{site_dir}}"
    tmp="$(mktemp -d)"
    trap 'rm -rf "$tmp"' EXIT

    command -v netlify >/dev/null 2>&1 || { echo "netlify CLI not found - install with: npm install -g netlify-cli" >&2; exit 1; }
    command -v jq >/dev/null 2>&1 || { echo "jq not found - required to parse the netlify deploy output" >&2; exit 1; }
    netlify api getCurrentUser >/dev/null 2>&1 || { echo "not logged in to netlify - run: netlify login - see docs/public-release.md" >&2; exit 1; }
    [ -n "${NETLIFY_SITE_ID:-}" ] || { echo "NETLIFY_SITE_ID is not set - see docs/public-release.md" >&2; exit 1; }

    # Whatever the Release currently holds. Zero assets is a hard failure,
    # never a warning: a download page with no downloads is never the thing
    # anyone wanted published.
    rm -rf "$assets"
    mkdir -p "$assets"
    gh release download "$tag" --repo "$repo" --dir "$assets" --clobber
    if [ -z "$(ls -A "$assets" 2>/dev/null)" ]; then
        echo "No assets downloaded from release $tag on $repo - refusing to publish an empty site" >&2
        exit 1
    fi
    echo "Downloaded assets for $tag:"
    ls -la "$assets"

    # Page and checksum manifest generated from exactly the files downloaded.
    scripts/assemble_site.sh assemble --assets "$assets" --out "$site" --tag "$tag"
    echo "--- SHA256SUMS ---"
    cat "$site/SHA256SUMS"

    netlify deploy --dir="$site" --prod --no-build --json > "$tmp/deploy.json"
    cat "$tmp/deploy.json"
    url="$(jq -r '.url // .deploy_url // empty' "$tmp/deploy.json")"
    [ -n "$url" ] || { echo "netlify reported no deploy URL" >&2; exit 1; }
    echo "Deployed to ${url%/}"

    # Content-based verification: fetch each advertised artifact and the page
    # itself and compare bodies against the manifest generated above, never a
    # status code.
    scripts/assemble_site.sh verify --base-url "${url%/}" --site "$site"

# =============================================================================
# One-Command Public Release (Phase 6 of the public-release plan)
# =============================================================================

# Full public release: stage binaries, upload them to the GitHub Release,
# publish the download site and push the gateway image to GHCR. Distinct from
# release-gateway, which remains the internal ECR path. Safe to re-run after a
# partial failure - each leg is independently idempotent (staging overwrites
# in place, release-upload --clobbers existing assets by name, publish-site
# regenerates the site from the Release and redeploys, and the GHCR
# build/push overwrite the same version tag) - so recovery is running this
# again, never manual repair.
release-public: stage-public-artifacts release-upload publish-site docker-build-gateway-ghcr docker-push-ghcr
    @echo "Public release v{{version}} complete: GitHub Release, download site and GHCR image all reflect the current VERSION"

# =============================================================================
# Cleanup
# =============================================================================

# Clean build artifacts
clean:
    rm -rf build/ dist/

# Show collector components
components: build
    ./build/otelcol-custom components
