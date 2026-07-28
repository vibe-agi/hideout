#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

sh -n install.sh
grep -F 'https://github.com/$repository/releases/download/$tag/$package' install.sh >/dev/null
grep -F 'package SHA-256 does not match the published inventory' install.sh >/dev/null
grep -F '/usr/bin/codesign --verify --strict' install.sh >/dev/null
grep -F '"$package_root/install.sh" --prefix "$prefix" --store "$store" --skip-init' install.sh >/dev/null

package="${HIDEOUT_TEST_RELEASE_PACKAGE:-}"
if [ -z "$package" ]; then
  candidate="$ROOT/.hideout-release-evidence/044-alpha2-exact-candidate-9f8f9d8/public-anonymous/hideout-v0.1.0-alpha.2-darwin-arm64.tar.gz"
  if [ -f "$candidate" ]; then
    package="$candidate"
  fi
fi

if [ "$(uname -s)" != Darwin ] || [ "$(uname -m)" != arm64 ] || [ -z "$package" ]; then
  echo "standalone-install: contract-only passed"
  exit 0
fi

tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-standalone-install.XXXXXX")"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup 0 HUP INT TERM

HIDEOUT_INSTALL_INVENTORY_FILE="$ROOT/releases/current.json" \
HIDEOUT_INSTALL_PACKAGE_FILE="$package" \
  "$ROOT/install.sh" --prefix "$tmp/prefix" --store "$tmp/store" \
  >"$tmp/install.out"

"$tmp/prefix/bin/hideout" version >"$tmp/version.out"
grep -Fx 'hideout 0.1.0-alpha.2' "$tmp/version.out" >/dev/null
"$tmp/prefix/bin/hideout" package verify "$tmp/prefix" >/dev/null
test ! -e "$tmp/store/profiles/default/profile.json"
grep -F "Hideout 0.1.0-alpha.2 installed at $tmp/prefix/bin/hideout" "$tmp/install.out" >/dev/null
grep -F "$tmp/prefix/bin/hideout setup" "$tmp/install.out" >/dev/null

jq '.current.package.artifactSHA256 = ("0" * 64)' releases/current.json >"$tmp/bad-current.json"
if HIDEOUT_INSTALL_INVENTORY_FILE="$tmp/bad-current.json" \
  HIDEOUT_INSTALL_PACKAGE_FILE="$package" \
  "$ROOT/install.sh" --prefix "$tmp/rejected-prefix" --store "$tmp/rejected-store" \
  >"$tmp/rejected.out" 2>"$tmp/rejected.err"; then
  echo "standalone-install: mismatched package digest was accepted" >&2
  exit 1
fi
grep -F 'package SHA-256 does not match the published inventory' "$tmp/rejected.err" >/dev/null
test ! -e "$tmp/rejected-prefix"

echo "standalone-install: passed"
