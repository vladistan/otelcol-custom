# Scrape collector release recipes (plan 08 track T4). Step 0.5 adds the first
# versions; T4 owns this file from Phase 3.

# GHCR repository for the public scrape image; release.yml publishes it from
# Step 6.2, once vars.PUBLISH_SCRAPE is set.
ghcr_repo_scrape := ghcr_registry + "/otelcol-scrape"

# Build the public scrape image for GHCR (linux/amd64, version tag only)
docker-build-scrape-ghcr:
    @echo "Building public Docker image for otelcol-scrape v{{version}}..."
    docker build --platform linux/amd64 \
        --build-arg VERSION={{version}} --build-arg COMMIT={{commit}} \
        -t {{ghcr_repo_scrape}}:v{{version}} -f Dockerfile.scrape .
    @echo "Image built: {{ghcr_repo_scrape}}:v{{version}}"

# Push the public scrape image to GHCR (reuses the existing gh CLI login)
docker-push-scrape-ghcr:
    @echo "Logging into ghcr.io..."
    gh auth token | docker login ghcr.io -u vladistan --password-stdin
    @echo "Pushing {{ghcr_repo_scrape}}:v{{version}}..."
    docker push {{ghcr_repo_scrape}}:v{{version}}
    @echo "Pushed: {{ghcr_repo_scrape}}:v{{version}}"

# Fail unless the gateway and scrape images both report VERSION from --version
# (07 D-21). Run after the builds and before the scrape push.
verify-ghcr-images:
    scripts/verify-ghcr-images.sh {{version}} {{ghcr_repo_gateway}}:v{{version}} {{ghcr_repo_scrape}}:v{{version}}

# Tests for verify-ghcr-images against a stub docker; no daemon needed.
test-verify-ghcr-images:
    scripts/test-verify-ghcr-images.sh
