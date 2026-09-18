# otel-collector Public Release — Confirmed Targets

Source of truth for plan `02-publish-artifacts-IMPLEMENTATION_PLAN.md` Phase 1.
Every fact below is a decision this plan is built against; later phases cite this
file rather than re-deciding anything here.

## Netlify Download Site

- **Site**: a NEW, dedicated Netlify site — not the existing desktop-app download
  site. The two are kept separate rather than sharing one page/section, because
  the desktop-app site's assembler
  (`.claude/templates/desktop/release/assemble_site.sh.reference`) is specific to
  that release's asset shape (DMGs, Gatekeeper notes), and a dedicated site keeps
  the otelcol download page and its own assembler independent of that flow.
- **Repository / configuration**: created in Phase 6 (Step 6.3) as
  `otelcol-custom-downloads` (site id `bf017fea-02b0-4e72-87e5-27feae93681f`,
  https://otelcol-custom-downloads.netlify.app), since it still did not exist
  going into that run — Phase 4 added the `assemble_site.sh`-style script and
  the `publish-site` recipe, but not the Netlify project itself.
  `NETLIFY_SITE_ID` for future `release-public` runs is the id above.
- **Binary placement**: root-level release listing. Since the site is dedicated
  to otelcol, there is no need for a path/section split — `otelcol-edge-<version>-linux-amd64`,
  `otelcol-gateway-<version>-linux-amd64` and `SHA256SUMS` are served directly at
  the site root, mirroring the naming convention already fixed in Phase 2
  (`otelcol-<variant>-<version>-linux-amd64`).
- **Deploy mechanism**: canonical `netlify deploy --dir=_site --prod --no-build`
  CLI invocation (see `.claude/templates/desktop/release/publish.yml.reference` for
  why `--no-build` is load-bearing: without it Netlify runs the *linked site's*
  remote build command instead of uploading the pre-assembled directory). Auth
  is the interactive `netlify login` session already on this machine (verified
  via `netlify api getCurrentUser`) — not a stored token; `NETLIFY_AUTH_TOKEN`
  is CI-only and unused here. The one credential this deploy still needs is
  `NETLIFY_SITE_ID` (see Credential Storage below), since the deploy directory
  is never `netlify link`ed.

- **Addendum (2026-09-16)**: a second custom domain, `otel-custom.vladistan.com`,
  was added to this site outside plan-02's phases. Set via the Netlify API
  (`updateSite`, `custom_domain` field) against site id
  `bf017fea-02b0-4e72-87e5-27feae93681f`. The DNS CNAME to
  `otelcol-custom-downloads.netlify.app` was already provisioned externally
  (`cicd/otel-custom.tf`) before this domain was added. TLS was provisioned via
  `provisionSiteTLSCertificate`.

## GHCR Target

- **Image**: `ghcr.io/vladistan/otelcol-gateway`.
- **Authentication**: `gh auth token`, reusing the existing `gh` CLI login —
  verified locally:

  ```
  gh auth token | docker login ghcr.io -u vladistan --password-stdin
  # Login Succeeded
  ```

  No dedicated PAT is needed. The active `gh` credential is a fine-grained GitHub
  PAT (`github_pat_...`, surfaced as `GH_TOKEN`) and it authenticates against
  `ghcr.io` successfully. A dedicated `write:packages` classic PAT would only be
  required if this login had failed (fine-grained PATs have historically had
  gaps around package-registry scopes) — it did not fail, so the simpler,
  already-present credential is used and a second token is not introduced.
  **Update, Phase 6 Step 6.3 (2026-09-16)**: this predicted trigger fired. The
  login above still succeeds, but `docker push` was refused with
  `permission_denied: The token provided does not match expected scopes` —
  the fine-grained PAT can authenticate to `ghcr.io` but cannot push. The
  classic OAuth token also on file (`gh auth status`, `repo`/`workflow`
  scopes) has the same gap. Resolved without adding a PAT: `gh auth refresh -h
  github.com -s write:packages` adds the scope to the existing OAuth session
  via device flow. Also note: creating the GitHub Release itself needs the
  classic token, not the fine-grained PAT — `release-tag`/`release-upload`
  return `HTTP 403` under `GH_TOKEN` (the fine-grained PAT) and succeed once
  it's unset so `gh` falls back to the keyring token. So the full
  `release-public` chain currently needs `GH_TOKEN` *unset* for the
  release-tag/upload legs and *present* (or refreshed per above) for the GHCR
  login/push legs.

## Credential Storage

- **Netlify**: no token stored anywhere. `publish-site` authenticates via the
  operator's own `netlify login` session (browser OAuth, persisted by the
  `netlify` CLI outside this repo) and checks it with `netlify api
  getCurrentUser` before deploying. `NETLIFY_AUTH_TOKEN` is a CI-only escape
  hatch for headless auth and is not part of this workflow — do not add it to
  an `.envrc` or otherwise wire it in on the strength of the `tools/*-tool`
  `.envrc` convention below; that convention still applies to `NETLIFY_SITE_ID`
  alone.
- **Mechanism** (for `NETLIFY_SITE_ID` only): a per-tool `.envrc` (direnv),
  following the existing convention in this monorepo (`tools/sentry-tool/.envrc`,
  `tools/tsdb-tool/.envrc`, `tools/elk-tool/.envrc` — each holding
  `export TOKEN=...` lines).
- **File**: `tools/otel-collector/.envrc`, holding:

  ```
  export NETLIFY_SITE_ID=...
  ```

  (No `GHCR_TOKEN` entry — GHCR auth reuses `gh auth token` at recipe run time,
  nothing to store for it.)
- Ignored via `.envrc` in `tools/otel-collector/.gitignore` (this tool's own
  gitignore, matching `tools/elk-tool/.gitignore`'s local pattern rather than a
  root-level path entry). No token is committed to the repository at any point.

## GitHub Release (Phase 3)

- **Tag convention**: `v<version>` (e.g. `v0.1.63`), matching the source `VERSION`
  file. Confirmed against `vladistan/otelcol-custom`: no tags or releases exist
  yet, so the convention was free to fix rather than needing to match an
  existing one.
- **Assembly point**: the GitHub Release for the current `VERSION` on
  `vladistan/otelcol-custom` holds `otelcol-edge-<version>-linux-amd64`,
  `otelcol-gateway-<version>-linux-amd64` and `SHA256SUMS` as its three assets —
  the single reconcilable record of what was released.
- **Recipes** (`justfile`):
  - `just release-tag` — creates the Release for the current `VERSION` (cutting
    the `vVERSION` tag from the public `main` in the same call) if absent, or
    reuses it unchanged if present.
  - `just release-upload` — attaches all three assets via `gh release upload
    --clobber`, which overwrites an asset already published under the same name
    rather than producing a duplicate or suffixed one, so re-running is
    idempotent. Fails loudly if the staging directory is missing or empty
    instead of publishing an empty Release.
  - `just verify-release-download` — downloads the three assets from the
    Release anonymously (plain `curl`, no `gh auth`) and checks them against
    the locally staged `SHA256SUMS`.
  - `just release-rc-dry-run` — runs the same tag/create/upload flow under a
    `<version>-rc1` tag marked `--prerelease`, so the flow is proven end to end
    before the first real version tag is cut. Left in place after the dry run;
    the recipe prints the `gh release delete --cleanup-tag` command to remove
    it.
- **Auth**: all recipes use `gh` (`gh release ...`), reusing the `gh auth
  token` login already confirmed working against `ghcr.io` in Phase 1 — no
  separate credential needed for Releases.

## Execution Model

- **Baseline**: human-run `just` flow. CONFIRMED — not CI-triggered.
- **Recipe**: `just release-public` (Phase 6 deliverable) as the single aggregate
  entry point, chaining the legs below. Each leg is also independently runnable
  as its own recipe so a partial failure can be resumed by re-running just that
  step rather than the whole chain:
  - `just stage-public-artifacts` — build + checksum (Phase 2)
  - `just release-upload` — create/update the GitHub Release, attach assets (Phase 3)
  - `just publish-site` — assemble and deploy the Netlify download page (Phase 4)
  - `just docker-build-gateway-ghcr` / `just docker-push-ghcr` — public gateway image (Phase 5)
- Phase 1 is decisions-only for this item: the recipe bodies themselves are
  Phase 2–6 implementation work, not written here.
- GitHub Actions automation was explicitly out of scope for the baseline and
  was revisited as a deliberate accept/defer decision in Phase 7 — see below.

## Automation (Phase 7)

- **Manual flow cost, from the Phase 6.3 run (v0.1.63)**: the chained
  `release-public` recipe itself ran cleanly end to end once credentials were
  right. The recurring friction was credential scoping, not the release logic:
  the ambient fine-grained `GH_TOKEN` could authenticate to `ghcr.io` but
  lacked `write:packages`, and separately lacked permission to create Releases
  on this repo (`403`) — so `release-upload` needed `GH_TOKEN` unset (falling
  back to the classic OAuth keyring token) while `docker-push-ghcr` needed it
  set and refreshed with the extra scope. Each real run meant manually
  toggling that env var between two legs of one command. A one-time cost, not
  recurring: the Netlify site itself only needed creating once.
- **Decision: ACCEPT (2026-09-16)**. Ruling from the coordinator (tavuk) after
  the above cost was reported: implement GitHub Actions automation now, and
  resolve the credential-toggling friction as part of it rather than carrying
  it into a CI job.
- **Implementation**: `.github/workflows/release.yml` (tag-triggered, on
  `push: tags: v*`) chains `stage-public-artifacts` → `release-upload` →
  `verify-release-download` → `docker-build-gateway-ghcr` →
  `docker-push-ghcr`. `.github/workflows/publish.yml` (`workflow_dispatch` and
  `release: published`, separately triggerable and idempotent per the plan's
  wording) runs `publish-site` alone, so a site-only republish never needs to
  rebuild binaries or re-touch the Release.
- **Credential friction resolved**: in Actions, this workflow's own repo *is*
  `vladistan/otelcol-custom` — the same repo `release-upload` targets — so the
  automatic `${{ github.token }}`, scoped via `permissions: contents: write`
  and `permissions: packages: write` on the release job, covers both the
  Release and the GHCR push with one token. No PAT, no manual toggling. `gh
  auth token` (used internally by `docker-push-ghcr`) reads `GH_TOKEN` from
  the step environment automatically, so the justfile recipes needed no
  changes.
- **Left manual by design**: GHCR package visibility (private → public) is
  still a one-time human step after the first push (see "Manual step" under
  GHCR Image below) — `release.yml` does not attempt an anonymous-pull check,
  since that would fail on every run until a human has done this once.

## GHCR Image (Phase 5)

- **Image**: `ghcr.io/vladistan/otelcol-gateway`, built from `Dockerfile.gateway`
  for `linux/amd64` via `just docker-build-gateway-ghcr`, pushed via
  `just docker-push-ghcr`.
- **Authentication**: `gh auth token | docker login ghcr.io -u vladistan --password-stdin`,
  the same credential path confirmed in Phase 1 — no dedicated `GHCR_TOKEN`.
- **Tagging policy**: immutable version tags only (`v<version>`), no moving
  `latest` tag. This mirrors the existing internal `docker-push-gateway` recipe,
  which also only pushes a version tag. A public `latest` tag is a moving target
  nobody asked for; it can be added later without breaking anything already
  published if a future need arises.
- **OCI provenance labels** (runtime stage of `Dockerfile.gateway`):
  - `org.opencontainers.image.source=https://github.com/vladistan/claude-tools`
  - `org.opencontainers.image.version=<VERSION build arg>`
  - `org.opencontainers.image.revision=<COMMIT build arg>`

  These link the GHCR package back to its source repository.
- **Manual step — visibility and anonymous pull verification**: GHCR packages
  are created **private** by default. After the first push, flip the package to
  public via
  `https://github.com/users/vladistan/packages/container/otelcol-gateway/settings`
  (or `gh api`), then verify anonymous pull from a clean Docker config:

  ```bash
  just docker-build-gateway-ghcr && just docker-push-ghcr
  DOCKER_CONFIG=$(mktemp -d) docker pull ghcr.io/vladistan/otelcol-gateway:v$(cat VERSION)
  # Expected: pull succeeds with no credentials configured
  ```

  This step was not run by the worker that authored the recipes above — it
  requires pushing to the real registry and changing live package visibility,
  which is a human/coordinator action, not something scripted blindly in a
  worktree.

## Publish Sequence — v0.1.64 (ClickHouse Exporter + Base-Image Bump)

Written down per plan `03-clickhouse-exporter-IMPLEMENTATION_PLAN.md` Phase 6
Step 6.5, ahead of Phase 7 execution. Follow this order; do not improvise it at
publish time.

1. **Regenerate and sanitize the pub-clone** per `PUB_MANIFEST.md`'s
   `exclude_files` / `replace_patterns` / `strip_blocks` rules, including the
   patterns that already cover this release's changes (no new leak class was
   introduced by the exporter or the base-image bump).
2. **Push public `main`** to `github.com/vladistan/otelcol-custom`, before any
   tag exists, so the tag and trunk name the same commit.
3. **Local pre-flight — before any tag is created**: run both
   `just docker-build-gateway-ghcr` and `just verify-public-artifacts` against
   the now-pinned Alpine 3.22.5 base, locally, and confirm both succeed.
   This step is named explicitly and run first because `release.yml` creates
   the public GitHub Release *before* it builds and pushes the GHCR image — if
   the image step then failed, it would leave a published Release with no
   matching image. Proving the image builds and passes
   `verify-public-artifacts` locally, before the tag exists, is what prevents
   that partial-publish state.
4. **Tag `v0.1.64`** and let `.github/workflows/release.yml` run to
   completion (stage → release-upload → verify-release-download →
   docker-build-gateway-ghcr → docker-push-ghcr).
5. **GHCR visibility**: the `otelcol-gateway` package was already flipped
   private → public once, during the v0.1.63 release (see "Manual step"
   above). That flip does not need repeating per version tag — confirm the
   package is still public and that an anonymous pull of the new tag
   succeeds, rather than repeating the visibility change from scratch:

   ```bash
   DOCKER_CONFIG=$(mktemp -d) docker pull ghcr.io/vladistan/otelcol-gateway:v0.1.64
   # Expected: pull succeeds with no credentials configured
   ```
