#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"

usage() {
  cat <<'USAGE'
Usage:
  ./install.sh [--prefix <dir>] [--store <dir>]
               [--backend native|lima|auto] [--network direct|tun2socks]
               [--proxy-secret <ref>]
               [--skip-init]

Install a release-like Hideout package from its extracted package directory.
This script copies package binaries into the chosen prefix, then runs the same
typed `hideout init --no-input` path unless --skip-init is set.
USAGE
}

prefix="${HIDEOUT_INSTALL_PREFIX:-${HOME:-}/.local}"
store="${HIDEOUT_STORE_ROOT:-${HOME:-}/.hideout}"
backend="auto"
network="direct"
proxy_secret=""
run_init=1

while [ "$#" -gt 0 ]; do
  case "$1" in
    --prefix)
      prefix="${2:-}"
      shift 2
      ;;
    --store)
      store="${2:-}"
      shift 2
      ;;
    --backend)
      backend="${2:-}"
      shift 2
      ;;
    --network)
      network="${2:-}"
      shift 2
      ;;
    --proxy-secret)
      proxy_secret="${2:-}"
      shift 2
      ;;
    --skip-init)
      run_init=0
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "install-package: unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [ -z "$prefix" ] || [ -z "$store" ]; then
  echo "install-package: --prefix and --store require paths" >&2
  exit 2
fi
if [ ! -x "$ROOT/bin/hideout" ] || [ ! -x "$ROOT/bin/hideout-shim" ]; then
  echo "install-package: package bin/ layout is incomplete" >&2
  exit 1
fi

mkdir -p "$prefix/bin" "$store"
prefix="$(cd "$prefix" && pwd -P)"
store="$(cd "$store" && pwd -P)"

echo "install-package: installing package binaries into $prefix/bin"
cp -p "$ROOT"/bin/* "$prefix/bin/"
chmod +x "$prefix/bin/hideout" "$prefix/bin/hideout-shim"
for helper in "$prefix"/bin/hideout-shim-linux* "$prefix"/bin/hideout-hostfsd-linux*; do
  if [ -f "$helper" ] && [ "${helper%.manifest.json}" = "$helper" ]; then
    chmod +x "$helper"
  fi
done

if [ "$run_init" -eq 1 ]; then
  echo "install-package: running hideout init --no-input"
  init_args=(init --no-input --backend "$backend" --network "$network")
  if [ -n "$proxy_secret" ]; then
    init_args+=(--proxy-secret "$proxy_secret")
  fi
  HIDEOUT_STORE_ROOT="$store" "$prefix/bin/hideout" "${init_args[@]}"
else
  echo "install-package: init skipped"
fi

cat <<EOF
install-package: installed
  bin:   $prefix/bin
  store: $store
  next:  PATH="$prefix/bin:\$PATH" hideout doctor --backend $backend
EOF
