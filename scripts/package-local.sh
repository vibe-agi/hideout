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
