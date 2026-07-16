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
  "$prefix/bin/hideout-hostfsd-linux-$arch" \
  "$prefix/bin/hideout-session-supervisor-linux-$arch"
do
  if [ ! -x "$path" ]; then
    echo "install-smoke: expected executable is missing: $path" >&2
    cat "$tmp/install.out" >&2
    exit 1
  fi
done

test -f "$prefix/bin/hideout-shim-linux-$arch.manifest.json"
test -f "$prefix/bin/hideout-hostfsd-linux-$arch.manifest.json"
test -f "$prefix/bin/hideout-session-supervisor-linux-$arch.manifest.json"
test -f "$store/install-state.json"
test -f "$store/logs/init-audit.jsonl"
test -f "$store/profiles/default/profile.json"

commit="$(git rev-parse HEAD 2>/dev/null || printf 'unknown')"
"$prefix/bin/hideout" version >"$tmp/version.out"
grep -q '^hideout dev$' "$tmp/version.out"
grep -q "^commit: $commit$" "$tmp/version.out"
grep -Eq '^builtAt: [0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$' "$tmp/version.out"
grep -q "^platform: $(go env GOOS)/$(go env GOARCH)$" "$tmp/version.out"

HIDEOUT_STORE_ROOT="$store" "$prefix/bin/hideout" doctor --backend native --workspace "$workspace" >"$tmp/doctor.out"
grep -q 'store: ok writable' "$tmp/doctor.out"
grep -q 'profile: ok default' "$tmp/doctor.out"
grep -q 'manager: ok' "$tmp/doctor.out"

if HIDEOUT_STORE_ROOT="$store" "$prefix/bin/hideout" init --no-input --profile default --template dev --backend native --network direct >"$tmp/init2.out" 2>"$tmp/init2.err"; then
  echo "install-smoke: second template init unexpectedly succeeded" >&2
  cat "$tmp/init2.out" >&2
  exit 1
fi
grep -q 'already exists' "$tmp/init2.err"

fix_store="$tmp/fix-store"
HIDEOUT_STORE_ROOT="$fix_store" "$prefix/bin/hideout" doctor --fix --dry-run --backend native >"$tmp/doctor-fix-dry.out"
grep -q 'Hideout doctor fix plan' "$tmp/doctor-fix-dry.out"
grep -q 'task profile.create: pending' "$tmp/doctor-fix-dry.out"
if [ -e "$fix_store/profiles/default/profile.json" ]; then
  echo "install-smoke: doctor --fix --dry-run created profile state" >&2
  cat "$tmp/doctor-fix-dry.out" >&2
  exit 1
fi

HIDEOUT_STORE_ROOT="$fix_store" "$prefix/bin/hideout" doctor --fix --apply --backend native >"$tmp/doctor-fix.out"
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
jq -e '.network.mediatedResolver == "1.1.1.1" and .metadata.templateId == "privacy"' "$proxy_store/profiles/default/profile.json" >/dev/null
if grep -R 'socks5://user:pass@127.0.0.1:7890' "$proxy_store" >/dev/null 2>&1; then
  echo "install-smoke: proxy install persisted raw proxy URL" >&2
  exit 1
fi

package_stage="$tmp/package-stage"
package_archive="$tmp/hideout-test.tar.gz"
scripts/package-local.sh --stage "$package_stage" --version 0.1.0-test.0 >/dev/null

missing_stage="$tmp/package-stage-missing-supervisor"
cp -R "$package_stage" "$missing_stage"
rm "$missing_stage/hideout/bin/hideout-session-supervisor-linux-$arch"
if scripts/package-local.sh --finalize "$missing_stage" --out "$tmp/missing-supervisor.tar.gz" >"$tmp/missing-stage.out" 2>"$tmp/missing-stage.err"; then
  echo "install-smoke: package finalization accepted a missing session supervisor" >&2
  exit 1
fi
grep -q 'required Linux session supervisor is missing' "$tmp/missing-stage.err"

corrupt_stage="$tmp/package-stage-corrupt-supervisor"
cp -R "$package_stage" "$corrupt_stage"
printf '\ncorrupt-for-smoke\n' >>"$corrupt_stage/hideout/bin/hideout-session-supervisor-linux-$arch"
if scripts/package-local.sh --finalize "$corrupt_stage" --out "$tmp/corrupt-supervisor.tar.gz" >"$tmp/corrupt-stage.out" 2>"$tmp/corrupt-stage.err"; then
  echo "install-smoke: package finalization accepted a corrupt session supervisor" >&2
  exit 1
fi
grep -q 'Linux session supervisor checksum mismatch' "$tmp/corrupt-stage.err"

scripts/package-local.sh --finalize "$package_stage" --out "$package_archive" >/dev/null
jq -e --arg arch "$arch" '
  (.layout.binaries | index("bin/hideout-session-supervisor-linux-" + $arch)) and
  any(.files[];
    .path == "bin/hideout-session-supervisor-linux-" + $arch and
    .kind == "linux-helper" and .executable == true) and
  any(.files[];
    .path == "bin/hideout-session-supervisor-linux-" + $arch + ".manifest.json" and
    .kind == "helper-manifest")
' "$package_stage/hideout/package-manifest.json" >/dev/null

missing_helper="$tmp/package-missing-supervisor"
cp -R "$package_stage/hideout" "$missing_helper"
rm "$missing_helper/bin/hideout-session-supervisor-linux-$arch"
if "$prefix/bin/hideout" package verify "$missing_helper" >"$tmp/missing-helper.out" 2>"$tmp/missing-helper.err"; then
  echo "install-smoke: package verification accepted a missing session supervisor" >&2
  exit 1
fi
grep -q 'hideout-session-supervisor-linux-' "$tmp/missing-helper.err"

corrupt_helper="$tmp/package-corrupt-supervisor"
cp -R "$package_stage/hideout" "$corrupt_helper"
printf '\ncorrupt-for-smoke\n' >>"$corrupt_helper/bin/hideout-session-supervisor-linux-$arch"
if "$prefix/bin/hideout" package verify "$corrupt_helper" >"$tmp/corrupt-helper.out" 2>"$tmp/corrupt-helper.err"; then
  echo "install-smoke: package verification accepted a corrupt session supervisor" >&2
  exit 1
fi
grep -q 'package checksum mismatch for bin/hideout-session-supervisor-linux-' "$tmp/corrupt-helper.err"

echo "install-smoke: passed"
