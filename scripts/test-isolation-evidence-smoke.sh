#!/usr/bin/env bash
# Isolation-evidence unit smoke (no Lima required): exercises the per-gate
# result emission (scripts/lib/gate-result.sh), the manifest aggregation shape,
# and the release-dogfood schema, so the machine-readable evidence contract is
# repeatably verified without a backend. The real-Lima gate runs are validated
# separately by the operator.
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

command -v jq >/dev/null 2>&1 || { echo "isolation-evidence-smoke: jq required" >&2; exit 127; }

. scripts/lib/gate-result.sh

tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-isolation-evidence.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT
export HIDEOUT_RELEASE_EVIDENCE_DIR="$tmp"

# Negative contract: helper rejects invalid results.
if emit_gate_result "gate2-lima" "native" "passed" 2>/dev/null; then
  echo "isolation-evidence-smoke: native must not pass an isolation claim" >&2
  exit 1
fi
if emit_gate_result "gate4-host-escape" "lima" "not-run" 2>/dev/null; then
  echo "isolation-evidence-smoke: not-run must require a reason" >&2
  exit 1
fi
if HIDEOUT_RUNTIME_EVIDENCE_REQUIRED=1 emit_gate_result "gate3-hidden-proxy" "lima" "passed" 2>/dev/null; then
  echo "isolation-evidence-smoke: incomplete runtime evidence must fail closed" >&2
  exit 1
fi

# Runtime marker identity comes from explicit build provenance, not a caller's
# current checkout or relabeling environment variables.
receipt="$tmp/runtime-verification.json"
provenance="$tmp/build-provenance.json"
jq -n '{provenance:{family:"developer-standard",revision:"2026.07.0",artifactSHA256:("a"*64),hostOS:"darwin",hostArch:"arm64",guestArch:"aarch64"},environmentId:"env_20260711t000000z0123456789abcdef0123"}' >"$receipt"
jq -n '{schema:"hideout.runtime-build-provenance/v1",source:{commit:("1"*40),dirty:true},output:{sha256:("a"*64)}}' >"$provenance"
marker_out="$(
  HIDEOUT_RUNTIME_BUILD_PROVENANCE="$provenance" \
    HIDEOUT_RUNTIME_CANDIDATE_COMMIT=ffffffffffff \
    HIDEOUT_RUNTIME_CANDIDATE_DIRTY=false \
    runtime_evidence_markers "$receipt"
)"
printf '%s\n' "$marker_out" | grep -qx "runtime_candidate_commit=$(printf '1%.0s' {1..40})"
printf '%s\n' "$marker_out" | grep -qx 'runtime_candidate_dirty=true'
jq '.output.sha256=("b"*64)' "$provenance" >"$tmp/build-provenance-mismatch.json"
if HIDEOUT_RUNTIME_BUILD_PROVENANCE="$tmp/build-provenance-mismatch.json" runtime_evidence_markers "$receipt" >/dev/null 2>&1; then
  echo "isolation-evidence-smoke: mismatched build provenance was accepted" >&2
  exit 1
fi

# Positive: emit a passed gate and an explicit not-run gate.
emit_gate_result "gate2-lima" "lima" "passed" "" "$tmp/audit.jsonl" "boundary-ref" "auto-env-1"
HIDEOUT_RUNTIME_EVIDENCE_REQUIRED=1 \
  HIDEOUT_RUNTIME_EVIDENCE_FAMILY=developer-standard \
  HIDEOUT_RUNTIME_EVIDENCE_REVISION=2026.07.0 \
  HIDEOUT_RUNTIME_EVIDENCE_ARTIFACT_SHA256=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
  HIDEOUT_RUNTIME_EVIDENCE_ENVIRONMENT_ID=env_20260711t000000z0123456789abcdef0123 \
  HIDEOUT_RUNTIME_EVIDENCE_HOST_OS=darwin \
  HIDEOUT_RUNTIME_EVIDENCE_HOST_ARCH=arm64 \
  HIDEOUT_RUNTIME_EVIDENCE_GUEST_ARCH=aarch64 \
  HIDEOUT_RUNTIME_EVIDENCE_CANDIDATE_COMMIT=0123456789ab \
  HIDEOUT_RUNTIME_EVIDENCE_CANDIDATE_DIRTY=false \
  emit_gate_result "gate3-hidden-proxy" "lima" "passed" "" "$tmp/audit.jsonl" "boundary-ref" "auto-env-2"
emit_gate_result "env-image" "lima" "not-run" "no image URL declared"

test -f "$tmp/gates/gate2-lima.json"
test -f "$tmp/gates/env-image.json"

# Reconciliation must account for every expected gate: the two not yet emitted
# (gate3, gate4) become explicit not-run, so no expected gate is silently absent.
reconcile_isolation_gates gate2-lima gate3-hidden-proxy gate4-host-escape env-image
for id in gate2-lima gate3-hidden-proxy gate4-host-escape env-image; do
  test -f "$tmp/gates/$id.json" || { echo "isolation-evidence-smoke: $id not accounted for after reconcile" >&2; exit 1; }
done
# Reconcile must not overwrite the already-recorded passed gate.
jq -e '.result == "passed"' "$tmp/gates/gate2-lima.json" >/dev/null \
  || { echo "isolation-evidence-smoke: reconcile clobbered a recorded result" >&2; exit 1; }
jq -e '.runtime.schema == "hideout.runtime-evidence-binding/v1" and .runtime.candidateDirty == false' "$tmp/gates/gate3-hidden-proxy.json" >/dev/null \
  || { echo "isolation-evidence-smoke: typed runtime binding missing" >&2; exit 1; }

# Aggregate exactly as write_manifest does, then validate the full manifest.
isolation_gates=$(jq -s '.' "$tmp"/gates/*.json)
manifest="$tmp/manifest.json"
jq -n --argjson ig "$isolation_gates" '{
  schema: "hideout.release-dogfood.v1",
  status: "passed",
  exitCode: 0,
  startedAt: "2026-07-07T00:00:00Z",
  endedAt: "2026-07-07T00:01:00Z",
  command: "scripts/test-phase1.sh --isolation-evidence",
  evidence: { directory: "/e", log: "test-release-dogfood.log" },
  git: { commit: "abc", dirty: false },
  host: { uname: "Darwin", macOSProductVersion: "15" },
  tools: { go: "go1.25", limactl: "1", jq: "1.7" },
  operatorProxy: { provided: true, scheme: "socks5", url: "redacted" },
  browser: { realBrowserRequired: true, browserPathProvided: true, browserApp: "C" },
  gates: ["gate0-static-contract","gate1-native-smoke","gate2-lima-e2e","gate3-hidden-proxy-operator","gate4-host-escape-real-browser","capability-probe-smoke","generic-cli-dogfood-smoke"],
  cleanup: { gate4BrowserProcesses: 0, gate4TempDirs: 0, hideoutLimaInstances: 0 },
  isolationGates: $ig,
  environmentSnapshot: { proxyMode: "tun2socks", hostPrerequisites: { gate4Browser: true, envImageURL: false }, externalContext: "smoke" }
}' >"$manifest"

go run ./cmd/hideout-schema-validate schemas/release-dogfood.schema.json "$manifest"

# The not-run gate carries a reason; native never appears passed.
jq -e '.isolationGates[] | select(.result == "not-run") | .reason | length > 0' "$manifest" >/dev/null \
  || { echo "isolation-evidence-smoke: not-run gate missing reason" >&2; exit 1; }
jq -e '[.isolationGates[] | select(.result == "passed" and .backend == "native")] | length == 0' "$manifest" >/dev/null \
  || { echo "isolation-evidence-smoke: native isolation claim marked passed" >&2; exit 1; }

echo "isolation-evidence-smoke: passed"
