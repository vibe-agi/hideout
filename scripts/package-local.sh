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
    install.sh
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

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    echo "package-local: missing shasum or sha256sum" >&2
    exit 127
  fi
}

stage="$tmp/stage"
prefix="$stage/hideout"
mkdir -p "$prefix"

"$source/scripts/install-local.sh" --prefix "$prefix" --store "$tmp/store" --source "$source" --skip-init >/dev/null
cp "$source/packaging/install-package.sh" "$prefix/install.sh"
chmod 0755 "$prefix/install.sh"
cp "$source/README.md" "$prefix/README.md"
cp "$source/README.zh-CN.md" "$prefix/README.zh-CN.md"
cp -R "$source/schemas" "$prefix/schemas"
cp -R "$source/docs" "$prefix/docs"
mkdir -p "$prefix/packaging"
cp -R "$source/packaging/homebrew" "$prefix/packaging/homebrew"

host_os="$(go env GOOS)"
host_arch="$(go env GOARCH)"
linux_guest_arch="$host_arch"
git_commit="$(git -C "$source" rev-parse --short=12 HEAD 2>/dev/null || printf 'unknown')"
git_dirty=false
if git -C "$source" rev-parse --is-inside-work-tree >/dev/null 2>&1 && [ -n "$(git -C "$source" status --porcelain)" ]; then
  git_dirty=true
fi
built_at="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
shim_linux="bin/hideout-shim-linux-$linux_guest_arch"
hostfsd_linux="bin/hideout-hostfsd-linux-$linux_guest_arch"
dns_stub_linux="bin/hideout-dns-stub-linux-$linux_guest_arch"
shim_linux_manifest="$shim_linux.manifest.json"
hostfsd_linux_manifest="$hostfsd_linux.manifest.json"
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
      "$shim_linux",
      "$hostfsd_linux",
      "$dns_stub_linux"
    ],
    "entrypoints": [
      "install.sh",
      "README.md",
      "README.zh-CN.md"
    ],
    "directories": [
      "schemas",
      "docs",
      "packaging"
    ]
  },
  "files": [
    {
      "path": "bin/hideout",
      "kind": "binary",
      "sha256": "$(sha256_file "$prefix/bin/hideout")"
    },
    {
      "path": "bin/hideout-shim",
      "kind": "binary",
      "sha256": "$(sha256_file "$prefix/bin/hideout-shim")"
    },
    {
      "path": "$shim_linux",
      "kind": "linux-helper",
      "sha256": "$(sha256_file "$prefix/$shim_linux")"
    },
    {
      "path": "$shim_linux_manifest",
      "kind": "helper-manifest",
      "sha256": "$(sha256_file "$prefix/$shim_linux_manifest")"
    },
    {
      "path": "$hostfsd_linux",
      "kind": "linux-helper",
      "sha256": "$(sha256_file "$prefix/$hostfsd_linux")"
    },
    {
      "path": "$hostfsd_linux_manifest",
      "kind": "helper-manifest",
      "sha256": "$(sha256_file "$prefix/$hostfsd_linux_manifest")"
    },
    {
      "path": "$dns_stub_linux",
      "kind": "linux-helper",
      "sha256": "$(sha256_file "$prefix/$dns_stub_linux")"
    },
    {
      "path": "install.sh",
      "kind": "installer",
      "sha256": "$(sha256_file "$prefix/install.sh")"
    },
    {
      "path": "README.md",
      "kind": "entrypoint",
      "sha256": "$(sha256_file "$prefix/README.md")"
    },
    {
      "path": "README.zh-CN.md",
      "kind": "entrypoint",
      "sha256": "$(sha256_file "$prefix/README.zh-CN.md")"
    },
    {
      "path": "schemas/package-manifest.schema.json",
      "kind": "schema",
      "sha256": "$(sha256_file "$prefix/schemas/package-manifest.schema.json")"
    },
    {
      "path": "schemas/release-dogfood.schema.json",
      "kind": "schema",
      "sha256": "$(sha256_file "$prefix/schemas/release-dogfood.schema.json")"
    }
  ]
}
EOF

mkdir -p "$(dirname "$out")"
(
  cd "$stage"
  tar -czf "$out" hideout
)
echo "$out"
