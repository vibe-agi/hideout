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

Install or upgrade a Hideout package from its extracted package directory.
The shell wrapper delegates package authority to the packaged Go command:

  bin/hideout package install <package-root> ...
USAGE
}

if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
  usage
  exit 0
fi

if [ ! -x "$ROOT/bin/hideout" ]; then
  echo "install-package: package layout is incomplete: missing executable hideout ($ROOT/bin/hideout)" >&2
  exit 1
fi

exec "$ROOT/bin/hideout" package install "$ROOT" "$@"
