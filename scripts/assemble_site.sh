#!/usr/bin/env bash
#
# Assemble the otelcol-custom download site from whatever a GitHub Release
# actually holds, and verify what was published by reading response bodies.
#
# Plan 02 (publish-artifacts), Phase 4. Adapted from
# .claude/templates/desktop/release/assemble_site.sh.reference for the
# otelcol-edge/otelcol-gateway binary shape instead of DMGs. Two properties
# carry over unchanged:
#
#   1. The page is a PROJECTION of the release. Which artifacts exist is
#      never written down here — it is derived from the asset filenames
#      present at run time. An artifact not yet uploaded produces no
#      download entry, never one that 404s.
#   2. Running it twice is normal, and must be byte-identical for an
#      unchanged asset set: no timestamps, no run ids, sorted ordering
#      throughout.
#
# Usage:
#   assemble_site.sh assemble --assets DIR --out DIR --tag vX.Y.Z
#   assemble_site.sh verify   --base-url URL --site DIR
#
set -euo pipefail

readonly PROGRAM="${0##*/}"

die() {
    printf '%s: %s\n' "$PROGRAM" "$*" >&2
    exit 1
}

note() {
    printf '%s: %s\n' "$PROGRAM" "$*" >&2
}

# --------------------------------------------------------------------------
# sha256, portable between macOS and Linux
# --------------------------------------------------------------------------

sha256_of() {
    local file="$1"
    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$file" | awk '{print $1}'
    else
        sha256sum "$file" | awk '{print $1}'
    fi
}

byte_size_of() {
    # stat's flags differ between BSD and GNU; wc is the same everywhere.
    wc -c < "$1" | tr -d ' '
}

# --------------------------------------------------------------------------
# HTML escaping — asset names reach the page, so they are escaped, not trusted
# --------------------------------------------------------------------------

html_escape() {
    printf '%s' "$1" |
        sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g' -e 's/"/\&quot;/g'
}

human_size() {
    local bytes="$1"
    awk -v b="$bytes" 'BEGIN { printf "%.1f MiB", b / 1048576 }'
}

# --------------------------------------------------------------------------
# assemble
# --------------------------------------------------------------------------

cmd_assemble() {
    local assets="" out="" tag=""
    while [ $# -gt 0 ]; do
        case "$1" in
            --assets) assets="${2:-}"; shift 2 ;;
            --out) out="${2:-}"; shift 2 ;;
            --tag) tag="${2:-}"; shift 2 ;;
            *) die "unknown argument: $1" ;;
        esac
    done
    [ -n "$assets" ] || die "--assets is required"
    [ -n "$out" ] || die "--out is required"
    [ -n "$tag" ] || die "--tag is required"
    [ -d "$assets" ] || die "asset directory does not exist: $assets"

    # A stale output directory is what makes a re-run non-idempotent: an
    # asset removed from the release would otherwise survive in the site.
    rm -rf "$out"
    mkdir -p "$out"

    # Sorted so two runs over the same assets emit identical bytes.
    local files=()
    while IFS= read -r path; do
        [ -n "$path" ] && files+=("$path")
    done < <(find "$assets" -maxdepth 1 -type f ! -name 'SHA256SUMS' | LC_ALL=C sort)

    # Zero assets is a hard failure, never a warning: a download page with no
    # downloads is never the thing anyone wanted published.
    if [ "${#files[@]}" -eq 0 ]; then
        die "no assets found in $assets — refusing to assemble an empty download page"
    fi

    ASSET_NAMES=()
    ASSET_SUMS=()
    ASSET_SIZES=()
    local f name
    for f in "${files[@]}"; do
        name="${f##*/}"
        cp "$f" "$out/$name"
        ASSET_NAMES+=("$name")
        ASSET_SUMS+=("$(sha256_of "$out/$name")")
        ASSET_SIZES+=("$(byte_size_of "$out/$name")")
    done

    # SHA256SUMS is generated HERE, from the files that were actually
    # downloaded — never copied from the checksum manifest a build job
    # uploaded. A checksum that travelled with its artifact cannot detect an
    # artifact that was corrupted in transit.
    local i
    : > "$out/SHA256SUMS"
    for i in "${!ASSET_NAMES[@]}"; do
        printf '%s  %s\n' "${ASSET_SUMS[$i]}" "${ASSET_NAMES[$i]}" >> "$out/SHA256SUMS"
    done

    render_index "$out" "$tag"

    note "assembled $out for $tag with ${#ASSET_NAMES[@]} artifact(s)"
}

# Reads the ASSET_* globals filled by cmd_assemble.
render_index() {
    local out="$1" tag="$2"

    local version="${tag#v}"
    local index="$out/index.html"

    {
        cat <<HTML
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>otelcol-custom $(html_escape "$version")</title>
<style>
:root { color-scheme: light dark; --fg: #16181d; --bg: #ffffff; --muted: #5c6370; --line: #e3e6ea; --accent: #2d6cdf; }
@media (prefers-color-scheme: dark) {
  :root { --fg: #e8eaed; --bg: #14161a; --muted: #9aa3b0; --line: #2a2f36; --accent: #6ea1ff; }
}
* { box-sizing: border-box; }
body { margin: 0; padding: 3rem 1.25rem; background: var(--bg); color: var(--fg);
       font: 16px/1.6 -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
main { max-width: 42rem; margin: 0 auto; }
h1 { font-size: 2rem; margin: 0 0 .25rem; letter-spacing: -.02em; }
.version { color: var(--muted); margin: 0 0 2.5rem; }
.dl { display: block; border: 1px solid var(--line); border-radius: .75rem;
      padding: 1rem 1.25rem; margin-bottom: .75rem; text-decoration: none; color: inherit; }
.dl:hover { border-color: var(--accent); }
.dl .name { font-weight: 600; }
.dl .meta { color: var(--muted); font-size: .875rem; }
.sha { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .8125rem;
       color: var(--muted); word-break: break-all; }
pre { background: var(--line); border-radius: .5rem; padding: .75rem 1rem; overflow-x: auto; }
footer { margin-top: 2.5rem; padding-top: 1.25rem; border-top: 1px solid var(--line);
         color: var(--muted); font-size: .875rem; }
a { color: var(--accent); }
</style>
</head>
<body>
<main>
<h1>otelcol-custom</h1>
<p class="version">Version $(html_escape "$version")</p>
HTML

        local i
        for i in "${!ASSET_NAMES[@]}"; do
            local n s z
            n="$(html_escape "${ASSET_NAMES[$i]}")"
            s="$(html_escape "${ASSET_SUMS[$i]}")"
            z="$(html_escape "$(human_size "${ASSET_SIZES[$i]}")")"
            cat <<HTML
<a class="dl" href="$n" download>
  <span class="name">$n</span>
  <div class="meta">$z</div>
  <div class="sha">sha256 $s</div>
</a>
HTML
        done
        printf '<p class="meta">All checksums: <a href="SHA256SUMS">SHA256SUMS</a></p>\n'

        cat <<'HTML'
<section id="install">
<h2>Install</h2>
<p>Download the binary for your platform, verify its checksum against
<a href="SHA256SUMS">SHA256SUMS</a>, then make it executable:</p>
<pre>curl -fsSLO &lt;url-of-binary&gt;
curl -fsSLO &lt;url-of-SHA256SUMS&gt;
sha256sum -c SHA256SUMS --ignore-missing
chmod +x otelcol-edge-*-linux-amd64   # or otelcol-gateway-*-linux-amd64</pre>
</section>
HTML

        cat <<HTML
<footer>These builds are published as-is, straight from the tagged release.
Source: <a href="https://github.com/vladistan/otelcol-custom">github.com/vladistan/otelcol-custom</a></footer>
</main>
</body>
</html>
HTML
    } > "$index"
}

# --------------------------------------------------------------------------
# verify — reads bodies, never status codes
# --------------------------------------------------------------------------

cmd_verify() {
    local base_url="" site=""
    while [ $# -gt 0 ]; do
        case "$1" in
            --base-url) base_url="${2:-}"; shift 2 ;;
            --site) site="${2:-}"; shift 2 ;;
            *) die "unknown argument: $1" ;;
        esac
    done
    [ -n "$base_url" ] || die "--base-url is required"
    [ -n "$site" ] || die "--site is required"
    [ -s "$site/SHA256SUMS" ] || die "no (or empty) SHA256SUMS in $site — assemble first"
    [ -f "$site/index.html" ] || die "no index.html in $site — assemble first"

    base_url="${base_url%/}"

    local scratch
    scratch="$(mktemp -d)"
    # shellcheck disable=SC2064
    trap "rm -rf '$scratch'" EXIT

    local failures=0 checked=0
    local expected_sum name

    while read -r expected_sum name; do
        [ -n "$name" ] || continue
        checked=$((checked + 1))

        # A 200 with a short error body is the failure this exists to catch,
        # and only reading the body proves it. The body is fetched and hashed
        # either way, never just the status.
        if ! curl -fsSL --retry 3 --retry-delay 2 -o "$scratch/$name" "$base_url/$name"; then
            note "FAIL $name: could not be fetched from $base_url/$name"
            failures=$((failures + 1))
            continue
        fi

        local actual_sum expected_size actual_size
        actual_sum="$(sha256_of "$scratch/$name")"
        expected_size="$(byte_size_of "$site/$name")"
        actual_size="$(byte_size_of "$scratch/$name")"

        if [ "$actual_size" != "$expected_size" ]; then
            note "FAIL $name: published body is $actual_size bytes, expected $expected_size"
            failures=$((failures + 1))
            continue
        fi
        if [ "$actual_sum" != "$expected_sum" ]; then
            note "FAIL $name: sha256 $actual_sum, expected $expected_sum"
            failures=$((failures + 1))
            continue
        fi
        note "ok   $name ($actual_size bytes, sha256 matches)"
    done < "$site/SHA256SUMS"

    # The page itself is an artifact: artifacts published behind a stale page
    # is exactly the half-published state this step prevents.
    local version
    version="$(sed -n 's/.*<p class="version">Version \(.*\)<\/p>.*/\1/p' "$site/index.html" | head -1)"
    [ -n "$version" ] || die "could not read the expected version out of $site/index.html"

    if ! curl -fsSL --retry 3 --retry-delay 2 -o "$scratch/index.html" "$base_url/index.html"; then
        note "FAIL index: could not be fetched from $base_url/index.html"
        failures=$((failures + 1))
    elif ! grep -q "Version $version" "$scratch/index.html"; then
        note "FAIL index: published page does not contain 'Version $version'"
        failures=$((failures + 1))
    else
        note "ok   index.html (contains Version $version)"
    fi

    if curl -fsSL --retry 1 -o "$scratch/root.html" "$base_url/" 2>/dev/null &&
        [ -s "$scratch/root.html" ] &&
        grep -qi '<html' "$scratch/root.html"; then
        if grep -q "Version $version" "$scratch/root.html"; then
            note "ok   / (contains Version $version)"
        else
            note "FAIL /: the root URL serves a page without 'Version $version'"
            failures=$((failures + 1))
        fi
    else
        note "skip /: the root URL does not serve a page here"
    fi

    if [ "$failures" -gt 0 ]; then
        die "$failures verification failure(s) across $checked artifact(s) plus the index"
    fi
    note "verified $checked artifact(s) and the index against $base_url"
}

# --------------------------------------------------------------------------

main() {
    local command="${1:-}"
    [ $# -gt 0 ] && shift
    case "$command" in
        assemble) cmd_assemble "$@" ;;
        verify) cmd_verify "$@" ;;
        "" | -h | --help)
            cat <<USAGE
usage:
  $PROGRAM assemble --assets DIR --out DIR --tag vX.Y.Z
  $PROGRAM verify   --base-url URL --site DIR

assemble renders the download page from whatever assets are present in
--assets (a clean directory downloaded from the GitHub Release), generating
SHA256SUMS from those files. Zero assets is a hard failure. verify fetches
every published artifact and the index page and compares bodies, not status
codes.
USAGE
            ;;
        *) die "unknown command: $command (try --help)" ;;
    esac
}

main "$@"
