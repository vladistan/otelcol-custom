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

# Run tests for generated build
test-build:
    cd build && go test ./...

# Run all tests (custom + build)
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
# Cleanup
# =============================================================================

# Clean build artifacts
clean:
    rm -rf build/

# Show collector components
components: build
    ./build/otelcol-custom components
