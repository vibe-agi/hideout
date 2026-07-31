#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/lib/gate-result.sh
. "$ROOT/scripts/lib/gate-result.sh"
gate_completed=0
. "$ROOT/scripts/lib/daemon-temp.sh"

tmp="$(hideout_mktemp_daemon_store)"
cleanup() {
  local exit_status=$?
  find "$tmp" -depth -delete
  if [ "$exit_status" -eq 0 ]; then
    gate_require_completion "install-smoke"
  fi
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
  "$prefix/bin/hideout-session-supervisor-linux-$arch" \
  "$prefix/bin/hideout-observer-linux-$arch" \
  "$prefix/bin/hideout-workspace-portal-linux-$arch"
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
test -f "$prefix/bin/hideout-observer-linux-$arch.manifest.json"
test -f "$prefix/bin/hideout-workspace-portal-linux-$arch.manifest.json"
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
grep -q '^Hideout doctor: Needs attention$' "$tmp/doctor.out"
grep -q '^Profile: default$' "$tmp/doctor.out"
grep -q '^Isolation: native development harness; no VM isolation$' "$tmp/doctor.out"
grep -q '^Details: hideout doctor --verbose$' "$tmp/doctor.out"
if grep -q 'manager: ok' "$tmp/doctor.out"; then
  echo "install-smoke: default doctor unexpectedly rendered detailed findings" >&2
  exit 1
fi
HIDEOUT_STORE_ROOT="$store" "$prefix/bin/hideout" doctor --backend native \
  --workspace "$workspace" --verbose >"$tmp/doctor-verbose.out"
grep -q 'store: ok writable' "$tmp/doctor-verbose.out"
grep -q 'profile: ok default' "$tmp/doctor-verbose.out"
grep -q 'manager: ok' "$tmp/doctor-verbose.out"

if HIDEOUT_STORE_ROOT="$store" "$prefix/bin/hideout" init --no-input --profile default --template dev --backend native --network direct >"$tmp/init2.out" 2>"$tmp/init2.err"; then
  echo "install-smoke: second template init unexpectedly succeeded" >&2
  cat "$tmp/init2.out" >&2
  exit 1
fi
grep -q 'already exists' "$tmp/init2.err"
HIDEOUT_STORE_ROOT="$store" "$prefix/bin/hideout" daemon stop >"$tmp/daemon-stop.out"

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
HIDEOUT_STORE_ROOT="$proxy_store" "$proxy_prefix/bin/hideout" daemon stop >"$tmp/proxy-daemon-stop.out"

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
grep -q 'required Linux helper hideout-session-supervisor is missing' "$tmp/missing-stage.err"

corrupt_stage="$tmp/package-stage-corrupt-supervisor"
cp -R "$package_stage" "$corrupt_stage"
printf '\ncorrupt-for-smoke\n' >>"$corrupt_stage/hideout/bin/hideout-session-supervisor-linux-$arch"
if scripts/package-local.sh --finalize "$corrupt_stage" --out "$tmp/corrupt-supervisor.tar.gz" >"$tmp/corrupt-stage.out" 2>"$tmp/corrupt-stage.err"; then
  echo "install-smoke: package finalization accepted a corrupt session supervisor" >&2
  exit 1
fi
grep -q 'Linux helper hideout-session-supervisor checksum mismatch' "$tmp/corrupt-stage.err"

missing_observer_stage="$tmp/package-stage-missing-observer"
cp -R "$package_stage" "$missing_observer_stage"
rm "$missing_observer_stage/hideout/bin/hideout-observer-linux-$arch"
if scripts/package-local.sh --finalize "$missing_observer_stage" \
  --out "$tmp/missing-observer.tar.gz" \
  >"$tmp/missing-observer-stage.out" \
  2>"$tmp/missing-observer-stage.err"; then
  echo "install-smoke: package finalization accepted a missing observer" >&2
  exit 1
fi
grep -q 'required Linux helper hideout-observer is missing' \
  "$tmp/missing-observer-stage.err"

scripts/package-local.sh --finalize "$package_stage" --out "$package_archive" >/dev/null
jq -e --arg arch "$arch" '
  (.layout.binaries | index("bin/hideout-session-supervisor-linux-" + $arch)) and
  (.layout.binaries | index("bin/hideout-observer-linux-" + $arch)) and
  (.layout.binaries | index("bin/hideout-workspace-portal-linux-" + $arch)) and
  any(.files[];
    .path == "bin/hideout-session-supervisor-linux-" + $arch and
    .kind == "linux-helper" and .executable == true) and
  any(.files[];
    .path == "bin/hideout-session-supervisor-linux-" + $arch + ".manifest.json" and
    .kind == "helper-manifest") and
  any(.files[];
    .path == "bin/hideout-observer-linux-" + $arch and
    .kind == "linux-helper" and .executable == true) and
  any(.files[];
    .path == "bin/hideout-observer-linux-" + $arch + ".manifest.json" and
    .kind == "helper-manifest") and
  any(.files[];
    .path == "bin/hideout-workspace-portal-linux-" + $arch and
    .kind == "linux-helper" and .executable == true) and
  any(.files[];
    .path == "bin/hideout-workspace-portal-linux-" + $arch + ".manifest.json" and
    .kind == "helper-manifest") and
  any(.files[];
    .path == "runtime/package-components.json" and
    .kind == "runtime-contract") and
  any(.files[];
    .path == "runtime/browser-console.assets.json" and
    .kind == "embedded-asset-manifest") and
  .embeddedAssets[0].manifest == "runtime/browser-console.assets.json"
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

gate_completed=1
echo "install-smoke: passed"
