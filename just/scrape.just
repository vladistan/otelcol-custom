# Scrape collector release recipes.
#
# Dry run without GHCR: start `docker run -d -p 127.0.0.1:5055:5000 registry:2`,
# then pass `--set ghcr_registry localhost:5055` to the build, push and verify
# recipes (port 5000 is taken by AirPlay on macOS).

# GHCR repository for the public scrape image; release.yml publishes it once
# vars.PUBLISH_SCRAPE is set.
ghcr_repo_scrape := ghcr_registry + "/otelcol-scrape"

# Build the public scrape image for GHCR (linux/amd64, version tag only)
docker-build-scrape-ghcr:
    @echo "Building public Docker image for otelcol-scrape v{{version}}..."
    docker build --platform linux/amd64 \
        --build-arg VERSION={{version}} --build-arg COMMIT={{commit}} \
        -t {{ghcr_repo_scrape}}:v{{version}} -f Dockerfile.scrape .
    @echo "Image built: {{ghcr_repo_scrape}}:v{{version}}"

# Push the public scrape image (logs in to ghcr.io through the gh CLI; any other
# registry, such as a local dry-run registry, is used as is)
docker-push-scrape-ghcr:
    @case "{{ghcr_registry}}" in \
        ghcr.io/*) echo "Logging into ghcr.io..."; gh auth token | docker login ghcr.io -u vladistan --password-stdin ;; \
        *) echo "Skipping ghcr.io login for {{ghcr_registry}}" ;; \
    esac
    @echo "Pushing {{ghcr_repo_scrape}}:v{{version}}..."
    docker push {{ghcr_repo_scrape}}:v{{version}}
    @echo "Pushed: {{ghcr_repo_scrape}}:v{{version}}"

# Fail unless both images report VERSION from --version and list exactly the
# modules their builder manifest names in `components`. Run
# after the builds and before the scrape push.
verify-ghcr-images: (verify-ghcr-components ghcr_repo_gateway + ":v" + version "builder-gateway.yaml") (verify-ghcr-components ghcr_repo_scrape + ":v" + version "builder-scrape.yaml")
    scripts/verify-ghcr-images.sh {{version}} {{ghcr_repo_gateway}}:v{{version}} {{ghcr_repo_scrape}}:v{{version}}

# Fail unless the image's `components` modules equal the manifest's gomod entries.
verify-ghcr-components image manifest:
    #!/usr/bin/env bash
    set -euo pipefail
    want=$(sed -n -E 's/^[[:space:]]*-[[:space:]]+gomod:[[:space:]]+([^[:space:]]+[[:space:]]+[^[:space:]]+).*/\1/p' {{manifest}} | sort)
    got=$(docker run --rm --platform linux/amd64 {{image}} components 2>/dev/null \
        | sed -n -E 's/^[[:space:]]+module:[[:space:]]+([^[:space:]]+[[:space:]]+[^[:space:]]+)[[:space:]]*$/\1/p' | sort)
    if [ -z "$want" ] || [ "$want" != "$got" ]; then
        echo "FAIL: {{image}} components differ from {{manifest}} (< manifest, > image):"
        diff <(echo "$want") <(echo "$got") || true
        exit 1
    fi
    echo "PASS: {{image}} components match {{manifest}} ($(echo "$want" | wc -l | tr -d ' ') modules)"

# Tests for verify-ghcr-images against a stub docker; no daemon needed.
test-verify-ghcr-images:
    scripts/test-verify-ghcr-images.sh
