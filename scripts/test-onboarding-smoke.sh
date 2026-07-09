#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

command -v jq >/dev/null 2>&1 || { echo "onboarding-smoke: jq required" >&2; exit 127; }

tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-onboarding.XXXXXX")"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT

run_hideout() {
  HIDEOUT_STORE_ROOT="$store" HIDEOUT_SECRET_PROXY_URL="socks5://127.0.0.1:7890" go run ./cmd/hideout "$@"
}

assert_clean_evidence() {
  path="$1"
  go run ./cmd/hideout-schema-validate schemas/onboarding-evidence.schema.json "$path"
  if grep -E 'HIDEOUT_SECRET|cap_[0-9a-f]{16,}|[0-9a-f]{32}' "$path" >/dev/null; then
    echo "onboarding-smoke: control-plane material leaked in $path" >&2
    cat "$path" >&2
    exit 1
  fi
}

store="$tmp/privacy-store"
run_hideout init \
  --profile alpha-privacy \
  --template privacy \
  --backend native \
  --network tun2socks \
  --proxy-secret proxy-url \
  --mediated-resolver 1.1.1.1 \
  --no-input >"$tmp/privacy.out"
grep -q 'template: privacy' "$tmp/privacy.out"
grep -q 'posture: privacy' "$tmp/privacy.out"
privacy_profile="$store/profiles/alpha-privacy/profile.json"
privacy_evidence="$store/profiles/alpha-privacy/onboarding-evidence.json"
jq -e '
  .metadata.templateId == "privacy" and
  .network.mode == "tun2socks" and
  .network.proxySecretRef == "proxy-url" and
  .network.mediatedResolver == "1.1.1.1" and
  ((.hostfs.grants // []) | length == 0) and
  ((.commandAdapters.adapters // {}) | length == 0)
' "$privacy_profile" >/dev/null
assert_clean_evidence "$privacy_evidence"
jq -e '.template == "privacy" and .hostfsPosture == "none-by-default" and .adapterPackPosture == "none-by-default"' "$privacy_evidence" >/dev/null

store="$tmp/missing-store"
if run_hideout init --no-input >"$tmp/missing.out" 2>"$tmp/missing.err"; then
  echo "onboarding-smoke: missing choices unexpectedly succeeded" >&2
  exit 1
fi
grep -q -- '--template' "$tmp/missing.err"
test ! -e "$store/profiles/default/profile.json"

store="$tmp/hardened-deny-store"
if run_hideout init \
  --profile alpha-hard \
  --template hardened \
  --backend native \
  --network tun2socks \
  --proxy-secret proxy-url \
  --mediated-resolver 1.1.1.1 \
  --privilege-status degraded \
  --no-input >"$tmp/hardened-deny.out" 2>"$tmp/hardened-deny.err"; then
  echo "onboarding-smoke: degraded hardened unexpectedly succeeded" >&2
  exit 1
fi
grep -q 'hardened requires enforced' "$tmp/hardened-deny.err"
test ! -e "$store/profiles/alpha-hard/profile.json"

store="$tmp/hardened-native-enforced-store"
if run_hideout init \
  --profile alpha-hard-native \
  --template hardened \
  --backend native \
  --network tun2socks \
  --proxy-secret proxy-url \
  --mediated-resolver 1.1.1.1 \
  --privilege-status enforced \
  --no-input >"$tmp/hardened-native-enforced.out" 2>"$tmp/hardened-native-enforced.err"; then
  echo "onboarding-smoke: native hardened enforced unexpectedly succeeded" >&2
  exit 1
fi
grep -q 'native backend' "$tmp/hardened-native-enforced.err"
test ! -e "$store/profiles/alpha-hard-native/profile.json"

store="$tmp/hardened-fallback-store"
run_hideout init \
  --profile alpha-hard-degraded \
  --template hardened \
  --backend native \
  --network tun2socks \
  --proxy-secret proxy-url \
  --mediated-resolver 1.1.1.1 \
  --privilege-status degraded \
  --allow-degraded-template \
  --no-input >"$tmp/hardened-fallback.out"
grep -q 'posture: hardened-degraded' "$tmp/hardened-fallback.out"
jq -e '.metadata.templateDegraded == "true" and .metadata.templatePosture == "hardened-degraded"' "$store/profiles/alpha-hard-degraded/profile.json" >/dev/null
jq -e '.effectivePosture == "hardened-degraded" and (.nonClaims[] | contains("not a hardened"))' "$store/profiles/alpha-hard-degraded/onboarding-evidence.json" >/dev/null

store="$tmp/dev-debug-store"
run_hideout init --profile alpha-dev --template dev --backend native --network direct --no-input >"$tmp/dev.out"
run_hideout init --profile alpha-debug --template debug --backend native --network direct --no-input >"$tmp/debug.out"
jq -e '.metadata.templateId == "dev" and .network.mode == "direct"' "$store/profiles/alpha-dev/profile.json" >/dev/null
jq -e '.metadata.templateId == "debug" and .metadata.templatePosture == "debug-local"' "$store/profiles/alpha-debug/profile.json" >/dev/null
jq -e '.effectivePosture == "dev" and (.nonClaims[] | contains("does not claim"))' "$store/profiles/alpha-dev/onboarding-evidence.json" >/dev/null
jq -e '.effectivePosture == "debug-local" and (.nonClaims[] | contains("does not claim"))' "$store/profiles/alpha-debug/onboarding-evidence.json" >/dev/null

grep -q -- '--template privacy' README.md
grep -q -- '--template dev' README.md

echo "onboarding-smoke: passed"
