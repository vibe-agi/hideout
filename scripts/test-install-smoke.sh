#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-install-smoke.XXXXXX")"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT

prefix="$tmp/prefix"
store="$tmp/store"
workspace="$tmp/workspace"
mkdir -p "$workspace"

scripts/install-local.sh --prefix "$prefix" --store "$store" --backend native --network direct >"$tmp/install.out"

arch="$(go env GOARCH)"
for path in \
  "$prefix/bin/hideout" \
  "$prefix/bin/hideout-shim" \
  "$prefix/bin/hideout-shim-linux-$arch" \
  "$prefix/bin/hideout-hostfsd-linux-$arch"
do
  if [ ! -x "$path" ]; then
    echo "install-smoke: expected executable is missing: $path" >&2
    cat "$tmp/install.out" >&2
    exit 1
  fi
done

test -f "$prefix/bin/hideout-shim-linux-$arch.manifest.json"
test -f "$prefix/bin/hideout-hostfsd-linux-$arch.manifest.json"
test -f "$store/install-state.json"
test -f "$store/logs/init-audit.jsonl"
test -f "$store/profiles/default/profile.json"

HIDEOUT_STORE_ROOT="$store" "$prefix/bin/hideout" doctor --backend native --workspace "$workspace" >"$tmp/doctor.out"
grep -q 'store: ok writable' "$tmp/doctor.out"
grep -q 'profile: ok default' "$tmp/doctor.out"
grep -q 'manager: ok' "$tmp/doctor.out"

HIDEOUT_STORE_ROOT="$store" "$prefix/bin/hideout" init --no-input --backend native --network direct >"$tmp/init2.out"
if grep -q 'task profile.create: applied' "$tmp/init2.out"; then
  echo "install-smoke: second init recreated profile" >&2
  cat "$tmp/init2.out" >&2
  exit 1
fi

fix_store="$tmp/fix-store"
HIDEOUT_STORE_ROOT="$fix_store" "$prefix/bin/hideout" doctor --fix --dry-run --backend native >"$tmp/doctor-fix-dry.out"
grep -q 'Hideout doctor fix plan' "$tmp/doctor-fix-dry.out"
grep -q 'task profile.create: pending' "$tmp/doctor-fix-dry.out"
if [ -e "$fix_store/profiles/default/profile.json" ]; then
  echo "install-smoke: doctor --fix --dry-run created profile state" >&2
  cat "$tmp/doctor-fix-dry.out" >&2
  exit 1
fi

HIDEOUT_STORE_ROOT="$fix_store" "$prefix/bin/hideout" doctor --fix --backend native >"$tmp/doctor-fix.out"
grep -q 'Hideout doctor fix' "$tmp/doctor-fix.out"
grep -q 'task store.create: applied' "$tmp/doctor-fix.out"
grep -q 'task profile.create: applied' "$tmp/doctor-fix.out"
test -f "$fix_store/install-state.json"
test -f "$fix_store/logs/init-audit.jsonl"
test -f "$fix_store/profiles/default/profile.json"
grep -q '"operation":"doctor.fix.apply"' "$fix_store/logs/init-audit.jsonl"

proxy_store="$tmp/proxy-store"
proxy_prefix="$tmp/proxy-prefix"
HIDEOUT_SECRET_DEFAULT_PROXY="socks5://user:pass@127.0.0.1:7890" \
  scripts/install-local.sh --prefix "$proxy_prefix" --store "$proxy_store" --backend native --network tun2socks --proxy-secret default-proxy >"$tmp/install-proxy.out"
jq -e '.network.mode == "tun2socks" and .network.proxySecretRef == "default-proxy" and (.network.proxyEnvVisible == false)' "$proxy_store/profiles/default/profile.json" >/dev/null
if grep -R 'socks5://user:pass@127.0.0.1:7890' "$proxy_store" >/dev/null 2>&1; then
  echo "install-smoke: proxy install persisted raw proxy URL" >&2
  exit 1
fi

echo "install-smoke: passed"
