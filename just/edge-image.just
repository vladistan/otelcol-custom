# Edge runtime image recipes (linux/amd64, for Kubernetes DaemonSets).
#
# The image holds the static Linux edge binary that stage-public-artifacts
# copies to dist/public/<version>/, so the image and the release asset are the
# same bytes. Dry run without GHCR: see just/scrape.just.

# GHCR repository for the public edge image; release.yml publishes it once
# vars.PUBLISH_EDGE is set.
ghcr_repo_edge := ghcr_registry + "/otelcol-edge"

# Build the public edge image from the staged static binary (run
# stage-public-artifacts first; the binary is checked against SHA256SUMS)
docker-build-edge-ghcr:
    #!/usr/bin/env bash
    set -euo pipefail
    dir="{{dist_public_dir}}"
    name="otelcol-edge-{{version}}-linux-amd64"
    if [ ! -f "$dir/$name" ] || [ ! -f "$dir/SHA256SUMS" ]; then
        echo "Missing $dir/$name or SHA256SUMS - run stage-public-artifacts first" >&2
        exit 1
    fi
    (cd "$dir" && awk -v n="$name" '$2 == n' SHA256SUMS | sha256sum -c -)
    ctx=$(mktemp -d)
    trap 'rm -rf "$ctx"' EXIT
    cp "$dir/$name" "$ctx/otelcol-edge"
    echo "Building public Docker image for otelcol-edge v{{version}}..."
    docker build --platform linux/amd64 \
        --build-arg VERSION={{version}} --build-arg COMMIT={{commit}} \
        -t {{ghcr_repo_edge}}:v{{version}} -f Dockerfile.edge "$ctx"
    echo "Image built: {{ghcr_repo_edge}}:v{{version}}"

# Fail unless the edge image reports VERSION from --version and its
# `components` modules equal the static edge manifest's gomod entries.
verify-ghcr-edge-image: (verify-ghcr-components ghcr_repo_edge + ":v" + version "builder-edge-static.yaml")
    scripts/verify-ghcr-images.sh {{version}} {{ghcr_repo_edge}}:v{{version}}

# Push the public edge image (logs in to ghcr.io through the gh CLI; any other
# registry, such as a local dry-run registry, is used as is)
docker-push-edge-ghcr:
    @case "{{ghcr_registry}}" in \
        ghcr.io/*) echo "Logging into ghcr.io..."; gh auth token | docker login ghcr.io -u vladistan --password-stdin ;; \
        *) echo "Skipping ghcr.io login for {{ghcr_registry}}" ;; \
    esac
    @echo "Pushing {{ghcr_repo_edge}}:v{{version}}..."
    docker push {{ghcr_repo_edge}}:v{{version}}
    @echo "Pushed: {{ghcr_repo_edge}}:v{{version}}"
