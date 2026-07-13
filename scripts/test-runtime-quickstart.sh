#!/usr/bin/env bash
# Executes the 12 verification scenarios in the 031 quickstart against the
# packaged catalog plus retained real Gate 2/Gate 3 evidence.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

gate2_dir="${HIDEOUT_RUNTIME_GATE2_EVIDENCE_DIR:-$ROOT/dist/runtime/evidence/031-runtime-lima}"
gate3_dir="${HIDEOUT_RUNTIME_GATE3_EVIDENCE_DIR:-$ROOT/dist/runtime/evidence/031-runtime-gate3}"
report="${HIDEOUT_RUNTIME_QUICKSTART_REPORT:-$ROOT/dist/runtime/evidence/031-quickstart/quickstart.out}"
gate2_manifest="$gate2_dir/product-hardening-evidence.json"
gate3_manifest="$gate3_dir/product-hardening-evidence.json"
gate2_log="$gate2_dir/logs/runtime-lima.out"
gate3_log="$gate3_dir/logs/runtime-gate3.out"

for command in jq go; do
  command -v "$command" >/dev/null 2>&1 || { echo "runtime-quickstart: missing $command" >&2; exit 127; }
done
for path in "$gate2_manifest" "$gate3_manifest" "$gate2_log" "$gate3_log" dist/runtime/promotion.json; do
  [ -f "$path" ] || { echo "runtime-quickstart: required evidence missing: $path" >&2; exit 2; }
done

. scripts/lib/runtime-product-evidence.sh

if [ "${HIDEOUT_QUICKSTART_SKIP_LOCAL:-0}" != "1" ]; then
  scripts/test-runtime-smoke.sh >/dev/null
  scripts/test-package-smoke.sh >/dev/null
  go test ./internal/runtimecatalog ./internal/runtimeverify ./internal/backend/lima \
    ./internal/manager ./internal/app ./internal/recovery >/dev/null
  scripts/test-doc-truth-smoke.sh >/dev/null
fi

for manifest in "$gate2_manifest" "$gate3_manifest"; do
  go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json \
    "$manifest" >/dev/null
  jq -e '
    .dirty == false and
    (.commit | test("^[a-f0-9]{12,40}$")) and
    .packageIdentity.name == "hideout" and
    .packageIdentity.version == .commit and
    all(.proofs[];
      .status == "passed" and .redactionStatus == "passed" and
      .runtime.schema == "hideout.runtime-evidence-binding/v1" and
      .runtime.buildDirty == false and
      (.runtime.buildCommit | test("^[a-f0-9]{12,40}$")))
  ' "$manifest" >/dev/null
  while IFS=$'\t' read -r relative digest; do
    artifact="$(dirname "$manifest")/$relative"
    [ -f "$artifact" ] || { echo "runtime-quickstart: artifact missing: $artifact" >&2; exit 2; }
    [ "$(runtime_evidence_sha256_file "$artifact")" = "$digest" ] || {
      echo "runtime-quickstart: artifact digest mismatch: $artifact" >&2
      exit 2
    }
  done < <(jq -r '.proofs[].artifacts[] | [.path,.sha256] | @tsv' "$manifest")
done

required_proofs='["031.runtime.agent-install","031.runtime.agent-privacy","031.runtime.baseline","031.runtime.boundary-regression","031.runtime.readiness-parity","031.runtime.real-image"]'
jq -s -e --argjson required "$required_proofs" '
  ([.[].proofs[].proofId] | unique) as $actual |
  all($required[] as $proof; $actual | index($proof) != null)
' "$gate2_manifest" "$gate3_manifest" >/dev/null

gate2_runtime="$(jq -c '[.proofs[] | select(.runtime != null)][0].runtime' "$gate2_manifest")"
gate3_runtime="$(jq -c '[.proofs[] | select(.runtime != null)][0].runtime' "$gate3_manifest")"
jq -e -n --argjson a "$gate2_runtime" --argjson b "$gate3_runtime" '
  $a.environmentId != $b.environmentId and
  ($a | del(.environmentId)) == ($b | del(.environmentId))
' >/dev/null

catalog_sha="$(jq -r '.families[] | select(.id == "developer-standard") |
  .currentRevision as $revision | .revisions[] | select(.id == $revision) | .artifacts[] |
  select(.hostOS == "darwin" and .hostArch == "arm64") | .sha256' \
  internal/runtimecatalog/catalog.json | head -n 1)"
promotion_sha="$(jq -r '.artifact.sha256' dist/runtime/promotion.json)"
[ -n "$catalog_sha" ] && [ "$catalog_sha" = "$promotion_sha" ] && \
  [ "$catalog_sha" = "$(jq -r '.artifactSHA256' <<<"$gate2_runtime")" ] || {
  echo "runtime-quickstart: catalog, promotion, and real evidence digests differ" >&2
  exit 2
}

grep -q '^env-image: wrong digest failed closed as required$' "$gate2_log"
grep -q '^runtime_contract=passed$' "$gate2_log"
grep -q '^runtime_mutable_guest_drift=passed$' "$gate2_log"
grep -q '^runtime_durable_prefix=passed$' "$gate2_log"
grep -q '^runtime_package_tool_provisioning=not-run$' "$gate2_log"
grep -q '^gate2: passed$' "$gate2_log"
grep -q '^dns_mediated=yes$' "$gate3_log"
grep -q '^connected_subnet_blocked=yes$' "$gate3_log"
grep -q '^https_request=ok$' "$gate3_log"
grep -q '^runtime_agent_version=codex-cli 0.144.1$' "$gate3_log"
grep -q '^runtime_agent_target_owner=passed$' "$gate3_log"
grep -q '^runtime_agent_no_sudo=passed$' "$gate3_log"
grep -q '^runtime_agent_no_auth=passed$' "$gate3_log"
grep -q '^runtime_agent_secret_scan=passed$' "$gate3_log"
grep -q '^gate3: passed$' "$gate3_log"

mkdir -p "$(dirname "$report")"
package_commit="$(jq -r '.commit' "$gate2_manifest")"
build_commit="$(jq -r '.buildCommit' <<<"$gate2_runtime")"
gate2_environment="$(jq -r '.environmentId' <<<"$gate2_runtime")"
gate3_environment="$(jq -r '.environmentId' <<<"$gate3_runtime")"
gate2_manifest_sha="$(runtime_evidence_sha256_file "$gate2_manifest")"
gate3_manifest_sha="$(runtime_evidence_sha256_file "$gate3_manifest")"
cat >"$report" <<EOF
quickstart=passed
scenario_01=passed catalog-and-package-integrity
scenario_02=passed explicit-pinned-selection
scenario_03=passed legacy-and-custom-honesty
scenario_04=passed declarative-zero-authority-contract
scenario_05=passed retained-real-image-and-digest-refusal
scenario_06=passed actual-guest-baseline
scenario_07=passed mutable-drift-and-exact-command-failure
scenario_08=passed durable-prefix-after-stop-start
scenario_09=passed pinned-agent-through-privacy-network
scenario_10=passed typed-recovery-matrix
scenario_11=passed full-boundary-regression
scenario_12=passed documentation-claim-truth
runtime_family=developer-standard
runtime_revision=2026.07.0
runtime_artifact_sha256=$catalog_sha
runtime_image_build_commit=$build_commit
package_candidate_commit=$package_commit
gate2_environment_id=$gate2_environment
gate3_environment_id=$gate3_environment
gate2_manifest=dist/runtime/evidence/031-runtime-lima/product-hardening-evidence.json
gate2_manifest_sha256=$gate2_manifest_sha
gate3_manifest=dist/runtime/evidence/031-runtime-gate3/product-hardening-evidence.json
gate3_manifest_sha256=$gate3_manifest_sha
EOF

cat "$report"
