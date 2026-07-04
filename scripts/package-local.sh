#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"

usage() {
  cat <<'USAGE'
Usage:
  scripts/package-local.sh [--out <tar.gz>] [--source <dir>]

Build a local release-like tarball with Hideout binaries, schemas, docs, and
packaging metadata. The archive layout is:

  hideout/
    bin/
    package-manifest.json
    README.md
    README.zh-CN.md
    schemas/
    docs/
    packaging/
USAGE
}

source="$ROOT"
out=""

while [ "$#" -gt 0 ]; do
  case "$1" in
    --out)
      out="${2:-}"
      shift 2
      ;;
    --source)
      source="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "package-local: unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

source="$(cd "$source" && pwd -P)"
if [ -z "$out" ]; then
  out="$source/dist/hideout-$(go env GOOS)-$(go env GOARCH).tar.gz"
fi
case "$out" in
  /*) ;;
  *) out="$PWD/$out" ;;
esac

tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-package.XXXXXX")"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT

stage="$tmp/stage"
prefix="$stage/hideout"
mkdir -p "$prefix"

"$source/scripts/install-local.sh" --prefix "$prefix" --store "$tmp/store" --source "$source" --skip-init >/dev/null
host_os="$(go env GOOS)"
host_arch="$(go env GOARCH)"
linux_guest_arch="$host_arch"
git_commit="$(git -C "$source" rev-parse --short=12 HEAD 2>/dev/null || printf 'unknown')"
git_dirty=false
if git -C "$source" rev-parse --is-inside-work-tree >/dev/null 2>&1 && [ -n "$(git -C "$source" status --porcelain)" ]; then
  git_dirty=true
fi
built_at="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
cat >"$prefix/package-manifest.json" <<EOF
{
  "schema": "hideout.package-manifest.v1",
  "builtAt": "$built_at",
  "git": {
    "commit": "$git_commit",
    "dirty": $git_dirty
  },
  "target": {
    "hostOS": "$host_os",
    "hostArch": "$host_arch",
    "linuxGuestArch": "$linux_guest_arch"
  },
  "layout": {
    "root": "hideout",
    "binaries": [
      "bin/hideout",
      "bin/hideout-shim",
      "bin/hideout-shim-linux-$linux_guest_arch",
      "bin/hideout-hostfsd-linux-$linux_guest_arch"
    ],
    "entrypoints": [
      "README.md",
      "README.zh-CN.md"
    ],
    "directories": [
      "schemas",
      "docs",
      "packaging"
    ]
  }
}
EOF
cp "$source/README.md" "$prefix/README.md"
cp "$source/README.zh-CN.md" "$prefix/README.zh-CN.md"
cp -R "$source/schemas" "$prefix/schemas"
cp -R "$source/docs" "$prefix/docs"
mkdir -p "$prefix/packaging"
cp -R "$source/packaging/homebrew" "$prefix/packaging/homebrew"

mkdir -p "$(dirname "$out")"
(
  cd "$stage"
  tar -czf "$out" hideout
)
echo "$out"
