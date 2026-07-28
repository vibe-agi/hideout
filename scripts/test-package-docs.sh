#!/usr/bin/env bash
set -euo pipefail

SCRIPT="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)/$(basename "$0")"
package_root=""
self_test=0

usage() {
  echo "usage: $0 --package-root <root> [--self-test]" >&2
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --package-root) package_root="${2:-}"; shift 2 ;;
    --self-test) self_test=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage; exit 2 ;;
  esac
done

if [ ! -d "$package_root" ]; then
  usage
  exit 2
fi
package_root="$(cd "$package_root" && pwd -P)"
manifest="$package_root/package-manifest.json"
if [ ! -f "$manifest" ] || [ -L "$manifest" ]; then
  echo "package-docs: package manifest is missing or unsafe" >&2
  exit 1
fi

version="$(jq -er '.release.productVersion' "$manifest")"
tag="$(jq -er '.release.tag' "$manifest")"
host_os="$(jq -er '.target.hostOS' "$manifest")"
host_arch="$(jq -er '.target.hostArch' "$manifest")"
archive="hideout-v${version}-${host_os}-${host_arch}.tar.gz"
archive_token="${archive#hideout-}"
identity="hideout-package-candidate: version=$version tag=$tag archive=$archive"

docs=(
  "README.md"
  "README.zh-CN.md"
  "docs/STATUS.md"
  "docs/first-run-alpha.md"
  "docs/distribution-bootstrap.md"
  "docs/support-matrix.md"
  "CHANGELOG.md"
  "packaging/homebrew/hideout.rb"
  "RELEASE_NOTES.md"
)

paths=()
markdown_paths=()
for rel in "${docs[@]}"; do
  path="$package_root/$rel"
  if [ ! -f "$path" ] || [ -L "$path" ]; then
    echo "package-docs: canonical document is missing or unsafe: $rel" >&2
    exit 1
  fi
  if ! grep -Fq "$identity" "$path"; then
    echo "package-docs: candidate identity is missing or stale: $rel" >&2
    exit 1
  fi
  if ! grep -Fq "$archive" "$path"; then
    echo "package-docs: archive identity is missing: $rel" >&2
    exit 1
  fi
  paths+=("$path")
  case "$rel" in
    *.md) markdown_paths+=("$path") ;;
  esac
done

if [ "${HIDEOUT_PACKAGE_DOCS_SKIP_LINT:-0}" != "1" ] &&
  command -v markdownlint-cli2 >/dev/null 2>&1; then
  markdownlint-cli2 "${markdown_paths[@]}" >/dev/null
fi

if grep -Eh 'github\.com/vibe-agi/hideout/releases/(tag|download)/' "${paths[@]}" >/dev/null; then
  echo "package-docs: candidate documentation links release assets before publication" >&2
  exit 1
fi
if grep -Eh \
  'brew install vibe-agi/tap/hideout|raw\.githubusercontent\.com/vibe-agi/hideout/master/install\.sh|releases/current\.json' \
  "${paths[@]}" >/dev/null; then
  echo "package-docs: candidate documentation routes users to another release channel" >&2
  exit 1
fi

token_file="$(mktemp "${TMPDIR:-/tmp}/hideout-package-doc-tokens.XXXXXX")"
fixture=""
cleanup() {
  rm -f "$token_file"
  if [ -n "$fixture" ]; then
    rm -rf "$fixture"
  fi
}
trap cleanup EXIT
grep -Eho 'v?[0-9]+\.[0-9]+\.[0-9]+-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*' \
  "${paths[@]}" | LC_ALL=C sort -u >"$token_file" || true
while IFS= read -r token; do
  if [ "$token" != "$version" ] && [ "$token" != "$tag" ] &&
    [ "$token" != "$archive_token" ]; then
    echo "package-docs: foreign candidate identity $token" >&2
    exit 1
  fi
done <"$token_file"

if ! grep -Fq 'not an installable Homebrew formula' \
  "$package_root/packaging/homebrew/hideout.rb"; then
  echo "package-docs: candidate Homebrew reference claims installability" >&2
  exit 1
fi
if grep -Eq '^[[:space:]]*(url|sha256)[[:space:]]+"' \
  "$package_root/packaging/homebrew/hideout.rb"; then
  echo "package-docs: candidate Homebrew reference predicts publication identity" >&2
  exit 1
fi

if [ "$self_test" -eq 1 ]; then
  fixture="$(mktemp -d "${TMPDIR:-/tmp}/hideout-package-doc-fixture.XXXXXX")"
  mkdir -p "$fixture/docs" "$fixture/packaging/homebrew"
  cp "$manifest" "$fixture/package-manifest.json"
  for rel in "${docs[@]}"; do
    cp "$package_root/$rel" "$fixture/$rel"
  done
  for rel in "${docs[@]}"; do
    printf '\nForeign current candidate: `v9.9.9-alpha.9`.\n' >>"$fixture/$rel"
    if HIDEOUT_PACKAGE_DOCS_SKIP_LINT=1 \
      "$SCRIPT" --package-root "$fixture" >/dev/null 2>&1; then
      echo "package-docs: stale-candidate fixture was accepted: $rel" >&2
      exit 1
    fi
    cp "$package_root/$rel" "$fixture/$rel"
  done
  echo "package-docs: rejected stale identity in ${#docs[@]} canonical documents"
fi

echo "package-docs: $tag matches $archive"
