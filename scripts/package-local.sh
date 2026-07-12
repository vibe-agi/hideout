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
    host-app/
    examples/
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
mkdir -p "$prefix/host-app" "$prefix/examples"
cp -R "$source/internal/hostcap/recipes" "$prefix/host-app/recipes"
cp -R "$source/examples/host-app-packs" "$prefix/examples/host-app-packs"
mkdir -p "$prefix/packaging"
cp -R "$source/packaging/homebrew" "$prefix/packaging/homebrew"
mkdir -p "$prefix/runtime"
cp "$source/internal/runtimecatalog/catalog.json" "$prefix/runtime/catalog.json"
cp "$source/internal/runtimecatalog/contract.json" "$prefix/runtime/contract.json"
cp -R "$source/runtime/developer-standard" "$prefix/runtime/developer-standard"

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
      "host-app",
      "examples",
      "packaging",
      "runtime"
    ]
  },
  "migration": {
    "installStateSchema": "hideout.package-install-state.v1",
    "fromInstalledSchemas": [
      "hideout.package-install-state.v1"
    ],
    "minimumPackageSchema": "hideout.package-manifest.v1",
    "maximumPackageSchema": "hideout.package-manifest.v1"
  },
  "files": [
    {
      "path": "bin/hideout",
      "kind": "binary",
      "sha256": "$(sha256_file "$prefix/bin/hideout")",
      "executable": true
    },
    {
      "path": "bin/hideout-shim",
      "kind": "binary",
      "sha256": "$(sha256_file "$prefix/bin/hideout-shim")",
      "executable": true
    },
    {
      "path": "$shim_linux",
      "kind": "linux-helper",
      "sha256": "$(sha256_file "$prefix/$shim_linux")",
      "executable": true
    },
    {
      "path": "$shim_linux_manifest",
      "kind": "helper-manifest",
      "sha256": "$(sha256_file "$prefix/$shim_linux_manifest")",
      "executable": false
    },
    {
      "path": "$hostfsd_linux",
      "kind": "linux-helper",
      "sha256": "$(sha256_file "$prefix/$hostfsd_linux")",
      "executable": true
    },
    {
      "path": "$hostfsd_linux_manifest",
      "kind": "helper-manifest",
      "sha256": "$(sha256_file "$prefix/$hostfsd_linux_manifest")",
      "executable": false
    },
    {
      "path": "$dns_stub_linux",
      "kind": "linux-helper",
      "sha256": "$(sha256_file "$prefix/$dns_stub_linux")",
      "executable": true
    },
    {
      "path": "install.sh",
      "kind": "installer",
      "sha256": "$(sha256_file "$prefix/install.sh")",
      "executable": true
    },
    {
      "path": "README.md",
      "kind": "entrypoint",
      "sha256": "$(sha256_file "$prefix/README.md")",
      "executable": false
    },
    {
      "path": "README.zh-CN.md",
      "kind": "entrypoint",
      "sha256": "$(sha256_file "$prefix/README.zh-CN.md")",
      "executable": false
    },
    {
      "path": "schemas/package-manifest.schema.json",
      "kind": "schema",
      "sha256": "$(sha256_file "$prefix/schemas/package-manifest.schema.json")",
      "executable": false
    },
    {
      "path": "schemas/release-dogfood.schema.json",
      "kind": "schema",
      "sha256": "$(sha256_file "$prefix/schemas/release-dogfood.schema.json")",
      "executable": false
    },
    {
      "path": "schemas/runtime-catalog.schema.json",
      "kind": "schema",
      "sha256": "$(sha256_file "$prefix/schemas/runtime-catalog.schema.json")",
      "executable": false
    },
    {
      "path": "schemas/runtime-verification.schema.json",
      "kind": "schema",
      "sha256": "$(sha256_file "$prefix/schemas/runtime-verification.schema.json")",
      "executable": false
    },
    {
      "path": "schemas/capability-descriptor.schema.json",
      "kind": "schema",
      "sha256": "$(sha256_file "$prefix/schemas/capability-descriptor.schema.json")",
      "executable": false
    },
    {
      "path": "schemas/host-app-pack.schema.json",
      "kind": "schema",
      "sha256": "$(sha256_file "$prefix/schemas/host-app-pack.schema.json")",
      "executable": false
    },
    {
      "path": "schemas/host-app-pack-registry.schema.json",
      "kind": "schema",
      "sha256": "$(sha256_file "$prefix/schemas/host-app-pack-registry.schema.json")",
      "executable": false
    },
    {
      "path": "schemas/host-app-enablement.schema.json",
      "kind": "schema",
      "sha256": "$(sha256_file "$prefix/schemas/host-app-enablement.schema.json")",
      "executable": false
    },
    {
      "path": "schemas/host-app-inspection.schema.json",
      "kind": "schema",
      "sha256": "$(sha256_file "$prefix/schemas/host-app-inspection.schema.json")",
      "executable": false
    },
    {
      "path": "schemas/open-resource-intent.schema.json",
      "kind": "schema",
      "sha256": "$(sha256_file "$prefix/schemas/open-resource-intent.schema.json")",
      "executable": false
    },
    {
      "path": "docs/README.md",
      "kind": "doc",
      "sha256": "$(sha256_file "$prefix/docs/README.md")",
      "executable": false
    },
    {
      "path": "docs/STATUS.md",
      "kind": "doc",
      "sha256": "$(sha256_file "$prefix/docs/STATUS.md")",
      "executable": false
    },
    {
      "path": "host-app/recipes/builtin-vscode.json",
      "kind": "host-app-core-data",
      "sha256": "$(sha256_file "$prefix/host-app/recipes/builtin-vscode.json")",
      "executable": false
    },
    {
      "path": "host-app/recipes/safety-profiles.json",
      "kind": "host-app-core-data",
      "sha256": "$(sha256_file "$prefix/host-app/recipes/safety-profiles.json")",
      "executable": false
    },
    {
      "path": "examples/host-app-packs/README.md",
      "kind": "host-app-example",
      "sha256": "$(sha256_file "$prefix/examples/host-app-packs/README.md")",
      "executable": false
    },
    {
      "path": "examples/host-app-packs/cursor/hideout.host-app-pack.json",
      "kind": "host-app-example",
      "sha256": "$(sha256_file "$prefix/examples/host-app-packs/cursor/hideout.host-app-pack.json")",
      "executable": false
    },
    {
      "path": "examples/host-app-packs/zed/hideout.host-app-pack.json",
      "kind": "host-app-example",
      "sha256": "$(sha256_file "$prefix/examples/host-app-packs/zed/hideout.host-app-pack.json")",
      "executable": false
    },
    {
      "path": "runtime/catalog.json",
      "kind": "runtime-catalog",
      "sha256": "$(sha256_file "$prefix/runtime/catalog.json")",
      "executable": false
    },
    {
      "path": "runtime/contract.json",
      "kind": "runtime-contract",
      "sha256": "$(sha256_file "$prefix/runtime/contract.json")",
      "executable": false
    },
    {
      "path": "runtime/developer-standard/README.md",
      "kind": "runtime-build",
      "sha256": "$(sha256_file "$prefix/runtime/developer-standard/README.md")",
      "executable": false
    },
    {
      "path": "runtime/developer-standard/build-lib.sh",
      "kind": "runtime-build",
      "sha256": "$(sha256_file "$prefix/runtime/developer-standard/build-lib.sh")",
      "executable": true
    },
    {
      "path": "runtime/developer-standard/build.sh",
      "kind": "runtime-build",
      "sha256": "$(sha256_file "$prefix/runtime/developer-standard/build.sh")",
      "executable": true
    },
    {
      "path": "runtime/developer-standard/packages.lock",
      "kind": "runtime-build",
      "sha256": "$(sha256_file "$prefix/runtime/developer-standard/packages.lock")",
      "executable": false
    },
    {
      "path": "runtime/developer-standard/packages.txt",
      "kind": "runtime-build",
      "sha256": "$(sha256_file "$prefix/runtime/developer-standard/packages.txt")",
      "executable": false
    },
    {
      "path": "runtime/developer-standard/sources.lock.json",
      "kind": "runtime-build",
      "sha256": "$(sha256_file "$prefix/runtime/developer-standard/sources.lock.json")",
      "executable": false
    },
    {
      "path": "runtime/developer-standard/test-build.sh",
      "kind": "runtime-build",
      "sha256": "$(sha256_file "$prefix/runtime/developer-standard/test-build.sh")",
      "executable": true
    },
    {
      "path": "runtime/developer-standard/verify-image.sh",
      "kind": "runtime-build",
      "sha256": "$(sha256_file "$prefix/runtime/developer-standard/verify-image.sh")",
      "executable": true
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
