#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-package-smoke.XXXXXX")"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT

pkg="$tmp/hideout.tar.gz"
store="$tmp/store"
workspace="$tmp/workspace"
mkdir -p "$workspace" "$tmp/install"

scripts/package-local.sh --out "$pkg" >"$tmp/package.out"
test -f "$pkg"

tar -xzf "$pkg" -C "$tmp/install"
prefix="$tmp/install/hideout"
arch="$(go env GOARCH)"

for path in \
  "$prefix/bin/hideout" \
  "$prefix/bin/hideout-shim" \
  "$prefix/bin/hideout-shim-linux-$arch" \
  "$prefix/bin/hideout-hostfsd-linux-$arch" \
  "$prefix/schemas/profile.schema.json" \
  "$prefix/schemas/run-plan.schema.json" \
  "$prefix/docs/privacy-run-design.md" \
  "$prefix/packaging/homebrew/hideout.rb"
do
  if [ ! -e "$path" ]; then
    echo "package-smoke: expected package artifact is missing: $path" >&2
    exit 1
  fi
done

test -f "$prefix/bin/hideout-shim-linux-$arch.manifest.json"
test -f "$prefix/bin/hideout-hostfsd-linux-$arch.manifest.json"

HIDEOUT_STORE_ROOT="$store" "$prefix/bin/hideout" init --no-input --backend native --network direct >"$tmp/init.out"
grep -q 'Hideout init' "$tmp/init.out"
test -f "$store/install-state.json"
test -f "$store/logs/init-audit.jsonl"
test -f "$store/profiles/default/profile.json"

HIDEOUT_STORE_ROOT="$store" "$prefix/bin/hideout" doctor --backend native --workspace "$workspace" >"$tmp/doctor.out"
grep -q 'store: ok writable' "$tmp/doctor.out"
grep -q 'profile: ok default' "$tmp/doctor.out"
grep -q 'manager: ok' "$tmp/doctor.out"

echo "package-smoke: passed"
