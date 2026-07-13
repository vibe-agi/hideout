#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"

usage() {
  cat <<'USAGE'
Usage:
  scripts/install-local.sh [--prefix <dir>] [--store <dir>] [--source <dir>]
                           [--backend native|lima|auto] [--network direct|tun2socks]
                           [--proxy-secret <ref>] [--mediated-resolver <ip>]
                           [--skip-init]

Build and install Hideout from the current source tree, then run a typed
Manager-owned init plan unless --skip-init is set.
USAGE
}

prefix="${HIDEOUT_INSTALL_PREFIX:-${HOME:-}/.local}"
store="${HIDEOUT_STORE_ROOT:-${HOME:-}/.hideout}"
source="$ROOT"
backend="auto"
network="direct"
proxy_secret=""
mediated_resolver="1.1.1.1"
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
    --source)
      source="${2:-}"
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
    --mediated-resolver)
      mediated_resolver="${2:-}"
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
      echo "install-local: unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [ -z "$prefix" ] || [ -z "$store" ]; then
  echo "install-local: --prefix and --store require absolute or home-based paths" >&2
  exit 2
fi

mkdir -p "$prefix/bin" "$store"
prefix="$(cd "$prefix" && pwd -P)"
store="$(cd "$store" && pwd -P)"
source="$(cd "$source" && pwd -P)"

hideout="$prefix/bin/hideout"
shim="$prefix/bin/hideout-shim"
arch="$(go env GOARCH)"
build_version="${HIDEOUT_VERSION:-dev}"
git_commit="$(git -C "$source" rev-parse HEAD 2>/dev/null || printf 'unknown')"
built_at="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
ldflags=(
  "-X" "github.com/vibe-agi/hideout/internal/app.Version=$build_version"
  "-X" "github.com/vibe-agi/hideout/internal/app.Commit=$git_commit"
  "-X" "github.com/vibe-agi/hideout/internal/app.BuildTime=$built_at"
)

echo "install-local: building hideout into $hideout"
go build -trimpath -ldflags "${ldflags[*]}" -o "$hideout" ./cmd/hideout

echo "install-local: building host command shim into $shim"
go build -trimpath -o "$shim" ./cmd/hideout-shim

echo "install-local: building linux guest shim for $arch"
"$hideout" shim build-linux \
  --out "$prefix/bin/hideout-shim-linux-$arch" \
  --goarch "$arch" \
  --source "$source" >/dev/null

echo "install-local: building linux hostfsd for $arch"
"$hideout" hostfsd build-linux \
  --out "$prefix/bin/hideout-hostfsd-linux-$arch" \
  --goarch "$arch" \
  --source "$source" >/dev/null

echo "install-local: building linux DoH DNS stub for $arch"
GOOS=linux GOARCH="$arch" CGO_ENABLED=0 \
  go build -trimpath -o "$prefix/bin/hideout-dns-stub-linux-$arch" ./cmd/hideout-dns-stub >/dev/null

if [ "$run_init" -eq 1 ]; then
  echo "install-local: running hideout init --no-input"
  template="dev"
  if [ "$network" = "tun2socks" ]; then
    template="privacy"
  fi
  init_args=(init --no-input --profile default --template "$template" --backend "$backend" --network "$network")
  if [ -n "$proxy_secret" ]; then
    init_args+=(--proxy-secret "$proxy_secret")
  fi
  if [ "$network" = "tun2socks" ]; then
    init_args+=(--mediated-resolver "$mediated_resolver")
  fi
  HIDEOUT_STORE_ROOT="$store" "$hideout" "${init_args[@]}"
else
  echo "install-local: init skipped"
fi

cat <<EOF
install-local: installed
  bin:   $prefix/bin
  store: $store
  next:  PATH="$prefix/bin:\$PATH" hideout doctor --backend $backend
EOF
