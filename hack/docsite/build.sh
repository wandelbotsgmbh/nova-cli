#!/usr/bin/env bash
# Builds the CLI reference site (GitHub Pages) into ./_site:
#   1. generates fresh markdown from the cobra commands (into a temp dir, docs/ stays untouched)
#   2. renders it with the NOVA design system template (hack/docsite)
#   3. vendors design tokens + fonts from npm, so the site loads nothing from third-party CDNs
#
# usage: hack/docsite/build.sh [version]   (run from the repo root; needs go + npm)
set -euo pipefail

VERSION=${1:-dev}
OUT=${OUT:-_site}
DESIGN_TOKENS_VERSION=3.5.0
FONTS_VERSION=1.2.0

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

go run . gendoc --dir "$tmp/md"

rm -rf "$OUT"
go run ./hack/docsite -src "$tmp/md" -out "$OUT" -version "$VERSION"

(cd "$tmp" && npm pack --silent \
  "@wandelbots/design-tokens@$DESIGN_TOKENS_VERSION" \
  "@wandelbots/fonts@$FONTS_VERSION" >/dev/null)
mkdir -p "$tmp/tokens" "$tmp/fonts"
tar -xzf "$tmp/wandelbots-design-tokens-$DESIGN_TOKENS_VERSION.tgz" -C "$tmp/tokens"
tar -xzf "$tmp/wandelbots-fonts-$FONTS_VERSION.tgz" -C "$tmp/fonts"

vendor="$OUT/assets/vendor"
mkdir -p "$vendor/design-tokens" "$vendor/fonts"
cp "$tmp/tokens/package/dist/css/variables.css" "$vendor/design-tokens/"
cp -R "$tmp/fonts/package/dist/inter.css" "$tmp/fonts/package/dist/files" "$tmp/fonts/package/LICENSE.md" "$tmp/fonts/package/licenses" "$vendor/fonts/"

echo "site written to $OUT"
