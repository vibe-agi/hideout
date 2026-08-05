#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-runtime-smoke.XXXXXX")"
cleanup() { rm -rf "$tmp"; }
trap cleanup EXIT

jq empty \
  schemas/runtime-catalog.schema.json \
  schemas/runtime-verification.schema.json \
  internal/runtimecatalog/catalog.json \
  internal/runtimecatalog/contract.json \
  runtime/developer-standard/sources.lock.json

test -s runtime/developer-standard/packages.txt
test -s runtime/developer-standard/packages.lock
runtime/developer-standard/test-build.sh

# These packages must run their complete test sets. A -run filter previously
# let the catalog package report success with "[no tests to run]".
go test ./internal/runtimecatalog
go test ./internal/runtimeverify ./internal/productevidence ./internal/releasecompat ./internal/app -run 'Runtime'

go build -o "$tmp/hideout" ./cmd/hideout
family_count="$(jq '.families | length' internal/runtimecatalog/catalog.json)"
observation_count="$(jq '.observations | length' internal/runtimecatalog/contract.json)"
if [ "$family_count" -eq 0 ]; then
  [ "$observation_count" -eq 0 ] || {
    echo "runtime-smoke: empty catalog has a non-empty contract" >&2
    exit 1
  }
  "$tmp/hideout" runtime list --json >"$tmp/runtime-list.json"
  jq -e '
    .schema == "hideout.runtime-catalog/v1" and
    .catalogRelease == "development-unpromoted" and
    ((.families // []) | length == 0)
  ' "$tmp/runtime-list.json" >/dev/null
  echo "runtime_catalog_state=not-promoted"
else
  [ "$observation_count" -gt 0 ] || {
    echo "runtime-smoke: promoted catalog has no contract observations" >&2
    exit 1
  }
  "$tmp/hideout" runtime list --json >"$tmp/runtime-list.json"
  jq -e --argjson count "$family_count" '.families | length == $count' "$tmp/runtime-list.json" >/dev/null
  echo "runtime_catalog_state=promoted-usable"
fi

bash -n scripts/lib/runtime-product-evidence.sh scripts/test-runtime-lima.sh scripts/test-runtime-agent-install.sh scripts/test-gate2-lima.sh scripts/test-gate3-hidden-proxy.sh
test -x scripts/test-runtime-lima.sh
test -x scripts/test-runtime-agent-install.sh
grep -q 'HIDEOUT_GATE2_RUNTIME_MODE=1' scripts/test-runtime-lima.sh
grep -q 'prepare_guest_node is forbidden in runtime acceptance mode' scripts/test-gate2-lima.sh
grep -q 'runtime_mutable_guest_drift=passed' scripts/test-runtime-lima.sh
grep -q 'support release redact-public-evidence' scripts/test-runtime-lima.sh
grep -q 'runtime_hideout_system_bootstrap=required-and-run' scripts/test-gate2-lima.sh
grep -q 'runtime_package_tool_provisioning=not-run' scripts/test-gate2-lima.sh
grep -q 'runtime_package_tool_provisioning_check=passed' scripts/test-gate2-lima.sh
if grep -Eq 'runtime_(guest_provisioning=not-run|no_guest_provisioning=passed)' scripts/test-gate2-lima.sh scripts/test-runtime-lima.sh; then
  echo "runtime-smoke: obsolete no-provisioning claim remains" >&2
  exit 1
fi
grep -q '@openai/codex@0.144.1' scripts/test-runtime-agent-install.sh
grep -q 'runtime_agent_secret_scan=passed' scripts/test-runtime-agent-install.sh
grep -q 'HIDEOUT_GATE3_RUNTIME_MODE' scripts/test-gate3-hidden-proxy.sh
grep -q 'runtime_agent_privacy=passed' scripts/test-gate3-hidden-proxy.sh
grep -q 'catalog still contains an unpromoted artifact' scripts/test-runtime-lima.sh
grep -F 'if [[ "$url" == https://example.invalid/* ]] || ! [[ "$sha" =~ ^[0-9a-f]{64}$ ]]; then' \
  scripts/test-runtime-lima.sh >/dev/null
if grep -F '[ "$url" = "https://example.invalid/"* ]' \
  scripts/test-runtime-lima.sh >/dev/null; then
  echo "runtime-smoke: placeholder URL guard uses literal [ ] comparison" >&2
  exit 1
fi
placeholder_runtime_url="https://example.invalid/runtime.qcow2"
[[ "$placeholder_runtime_url" == https://example.invalid/* ]] ||
  { echo "runtime-smoke: placeholder URL pattern fixture was not rejected" >&2; exit 1; }
grep -q 'RuntimePolicyExactReal' internal/productevidence/registry.go
grep -q 'local fixture' internal/productevidence/evaluate_test.go
grep -q 'dirty gate' internal/releasecompat/readiness_test.go

. scripts/lib/runtime-product-evidence.sh
package_commit="$(git rev-parse HEAD)"
package_dirty="$(runtime_evidence_git_dirty)"
[ "$(runtime_evidence_git_commit)" = "$package_commit" ] || {
  echo "runtime-smoke: evidence commit does not match package candidate identity" >&2
  exit 1
}
cat >"$tmp/runtime-markers.out" <<'EOF'
runtime_family=developer-standard
runtime_revision=2026.07.0
runtime_artifact_sha256=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
runtime_environment_id=env_20260713t000000z0123456789abcdef0123
runtime_host_os=darwin
runtime_host_arch=arm64
runtime_guest_arch=aarch64
runtime_build_commit=0123456789ab
runtime_build_dirty=false
EOF
printf 'runtime synthetic evidence\n' >"$tmp/runtime.log"
"$tmp/hideout" support proof-registry --json >"$tmp/proof-registry.json"
runtime_binding="$(runtime_evidence_binding "$tmp/runtime-markers.out")"
runtime_proofs='[]'
runtime_proofs="$(runtime_evidence_add_proof "$runtime_proofs" "$tmp/proof-registry.json" \
  "031.runtime.real-image" "real-gate" "runtime-real-image" "synthetic writer mechanics only" \
  "runtime.log" "$(runtime_evidence_sha256_file "$tmp/runtime.log")" "$runtime_binding")"
jq -n --arg commit "$package_commit" '{
  name:"hideout",
  productVersion:"0.1.0-alpha.1",
  sourceCommit:$commit,
  artifactSHA256:("b" * 64),
  hostOS:"darwin",
  hostArch:"arm64"
}' >"$tmp/package-identity.json"
runtime_evidence_write_manifest "$tmp/product-hardening-evidence.json" "$runtime_proofs" \
  "$tmp/package-identity.json"
go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json \
  "$tmp/product-hardening-evidence.json" >/dev/null
jq -e '
  .proofs | length == 1 and
  .[0].proofId == "031.runtime.real-image" and
  .[0].runtime.buildCommit == "0123456789ab"
' "$tmp/product-hardening-evidence.json" >/dev/null
jq -e --arg commit "$package_commit" --argjson dirty "$package_dirty" '
  .commit == $commit and
  .dirty == $dirty and
  .packageIdentity == {
    name:"hideout",
    productVersion:"0.1.0-alpha.1",
    sourceCommit:$commit,
    artifactSHA256:("b" * 64),
    hostOS:"darwin",
    hostArch:"arm64"
  }
' "$tmp/product-hardening-evidence.json" >/dev/null
selected_runtime_binding="$(
  runtime_evidence_unique_binding "$tmp/product-hardening-evidence.json"
)"
if ! jq -e -n \
  --argjson selected "$selected_runtime_binding" \
  --argjson expected "$runtime_binding" \
  '$selected == $expected' >/dev/null; then
  echo "runtime-smoke: unique runtime binding changed exact identity" >&2
  exit 1
fi
jq '.proofs += [.proofs[0]]' "$tmp/product-hardening-evidence.json" \
  >"$tmp/repeated-runtime-binding.json"
runtime_evidence_unique_binding "$tmp/repeated-runtime-binding.json" >/dev/null
jq '.proofs[1].runtime.environmentId = "env_conflicting-runtime-binding"' \
  "$tmp/repeated-runtime-binding.json" >"$tmp/conflicting-runtime-binding.json"
if runtime_evidence_unique_binding "$tmp/conflicting-runtime-binding.json" \
  >/dev/null 2>&1; then
  echo "runtime-smoke: conflicting runtime bindings were accepted" >&2
  exit 1
fi
jq 'del(.proofs[].runtime)' "$tmp/product-hardening-evidence.json" \
  >"$tmp/missing-runtime-binding.json"
if runtime_evidence_unique_binding "$tmp/missing-runtime-binding.json" \
  >/dev/null 2>&1; then
  echo "runtime-smoke: missing runtime binding was accepted" >&2
  exit 1
fi

echo "runtime-smoke: catalog state and verification contracts passed"
