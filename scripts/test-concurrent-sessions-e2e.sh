#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/lib/gate2-concurrent-sessions.sh
. "$ROOT/scripts/lib/gate2-concurrent-sessions.sh"
# shellcheck source=scripts/lib/gate2-concurrent-performance.sh
. "$ROOT/scripts/lib/gate2-concurrent-performance.sh"

mode="local-fast"
require_real=0
out="${HIDEOUT_034_EVIDENCE_DIR:-$ROOT/.hideout-release-evidence/034-concurrent-sessions}"
baseline_commit="2f0cddebc5b0215989b04e1f94955e84f1926929"
samples=30
warmups=3

usage() {
  cat <<'USAGE'
Usage: scripts/test-concurrent-sessions-e2e.sh [options]

  --local-fast                 run mechanics-only evidence (default)
  --real-gate2                 run real macOS arm64 Lima isolation/performance
  --require-real               fail instead of emitting supporting not-run evidence
  --baseline-commit <commit>   exact pre-034 comparison commit
  --samples <n>                measured samples (real gate requires at least 30)
  --warmups <n>                excluded warm-up samples
  --out <dir>                  evidence output directory
USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --local-fast) mode="local-fast"; shift ;;
    --real-gate2) mode="real-gate2"; shift ;;
    --require-real) require_real=1; shift ;;
    --baseline-commit) baseline_commit="${2:-}"; shift 2 ;;
    --samples) samples="${2:-}"; shift 2 ;;
    --warmups) warmups="${2:-}"; shift 2 ;;
    --out) out="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "concurrent-sessions e2e: unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

case "$samples" in ''|*[!0-9]*) echo "concurrent-sessions e2e: --samples must be an integer" >&2; exit 2 ;; esac
case "$warmups" in ''|*[!0-9]*) echo "concurrent-sessions e2e: --warmups must be an integer" >&2; exit 2 ;; esac
if [ "$mode" = "real-gate2" ] && [ "$samples" -lt 30 ]; then
  echo "concurrent-sessions e2e: real evidence requires at least 30 samples" >&2
  exit 2
fi
if ! printf '%s' "$baseline_commit" | grep -Eq '^[0-9a-f]{40}$' ||
  ! git cat-file -e "$baseline_commit^{commit}" 2>/dev/null; then
  echo "concurrent-sessions e2e: baseline commit must be an available full commit" >&2
  exit 2
fi

mkdir -p "$out/logs" "$out/reports"
out="$(cd "$out" && pwd -P)"
manifest="$out/product-hardening-evidence.json"
registry="$out/reports/proof-registry.json"
go run ./cmd/hideout support proof-registry --json >"$registry"

sha256_file() { gate2_034_sha256 "$1"; }
git_commit() { git rev-parse HEAD; }
git_dirty() { gate2_034_dirty; }

artifact_json() {
  local rel="$1" description="$2"
  jq -n --arg path "$rel" --arg sha "$(sha256_file "$out/$rel")" --arg description "$description" \
    '{kind:"log",path:$path,sha256:$sha,redactionStatus:"passed",description:$description}'
}

claims_json() {
  local proof_id="$1"
  jq -c --arg id "$proof_id" '
    [.requirements[] | select(.proofId == $id) | .claimIds[] |
      {claimId:.,source:"spec",description:("034 registered contract " + .),scope:"concurrent-session"}]
  ' "$registry"
}

proof_json() {
  local proof_id="$1" status="$2" proof_mode="$3" class="$4" summary="$5"
  local artifact="$6" reason="$7" runtime="${8:-null}"
  local claims
  claims="$(claims_json "$proof_id")"
  [ "$(jq 'length' <<<"$claims")" -gt 0 ] || {
    echo "concurrent-sessions e2e: proof is not registered: $proof_id" >&2
    return 1
  }
  jq -n \
    --arg proofId "$proof_id" --arg status "$status" --arg mode "$proof_mode" \
    --arg class "$class" --arg summary "$summary" --arg reason "$reason" \
    --argjson claims "$claims" --argjson artifact "$artifact" --argjson runtime "$runtime" \
    '{proofId:$proofId,featureId:"034-concurrent-run-sessions",mode:$mode,evidenceClass:$class,
      status:$status,commandSummary:$summary,coveredClaims:$claims,
      prerequisites:(if $status == "not-run" then [{name:"real-macos-arm64-lima",status:"missing",reason:$reason}] else [{name:"required-gate-prerequisites",status:"available"}] end),
      artifacts:(if $artifact == null then [] else [$artifact] end),
      redactionStatus:(if $status == "not-run" then "not-run" else "passed" end)}
      + (if $status == "not-run" then {notRunReason:$reason} else {} end)
      + (if $runtime == null then {} else {runtime:$runtime} end)'
}

write_manifest() {
  local proofs="$1"
  local package_identity="null" commit
  commit="$(git_commit)"
  if [ -n "${HIDEOUT_RUNTIME_PACKAGE_IDENTITY:-}" ]; then
    [ -f "$HIDEOUT_RUNTIME_PACKAGE_IDENTITY" ] || {
      echo "concurrent-sessions e2e: package identity does not exist" >&2
      return 1
    }
    package_identity="$(jq -c . "$HIDEOUT_RUNTIME_PACKAGE_IDENTITY")"
    [ "$(jq -r '.sourceCommit' "$HIDEOUT_RUNTIME_PACKAGE_IDENTITY")" = "$commit" ] || {
      echo "concurrent-sessions e2e: package identity is not bound to the candidate checkout" >&2
      return 1
    }
  fi
  jq -n \
    --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
    --arg commit "$commit" --argjson dirty "$(git_dirty)" \
    --argjson packageIdentity "$package_identity" --slurpfile proofs "$proofs" \
    '{version:"hideout.product-hardening-evidence/v1",generatedAt:$generatedAt,
      commit:$commit,dirty:$dirty,proofs:$proofs[0]}
      + (if $packageIdentity == null then {} else {packageIdentity:$packageIdentity} end)' >"$manifest"
  go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json \
    "$manifest" >"$out/logs/evidence-schema.out" 2>"$out/logs/evidence-schema.err"
}

scan_public_evidence() {
  local forbidden='claim_[0-9a-f]{16,}|cap_[A-Za-z0-9]{12,}|HIDEOUT_SECRET_[A-Z0-9_]+=|socks5://[^[:space:]]+:[^[:space:]]+@|machineId|providerRef'
  if grep -R -E "$forbidden" "$out/logs" "$manifest" >/dev/null 2>&1; then
    echo "concurrent-sessions e2e: evidence contains control-plane material" >&2
    grep -R -n -E "$forbidden" "$out/logs" "$manifest" >&2 || true
    return 1
  fi
}

proofs="$out/reports/proofs.json"
if [ "$mode" = "local-fast" ]; then
  scripts/test-concurrent-sessions-smoke.sh \
    >"$out/logs/local-fast.out" 2>"$out/logs/local-fast.err"
  local_artifact="$(artifact_json logs/local-fast.out '034 local mechanics smoke')"
  jq -s '.' \
    <(proof_json '034.concurrent-sessions.gate0.mechanics' passed local-fast \
      concurrent-sessions-gate0 \
      'validated owner, activation, service, stop, status, and local concurrency mechanics' \
      "$local_artifact" 'local mechanics only; no real isolation claim') >"$proofs"
else
  missing=""
  for tool in go jq limactl shasum ssh perl; do
    command -v "$tool" >/dev/null 2>&1 || missing="$missing $tool"
  done
  [ "$(uname -s)" = "Darwin" ] || missing="$missing macOS"
  [ "$(uname -m)" = "arm64" ] || missing="$missing arm64"
  if [ -n "$missing" ]; then
    reason="real Gate 2 prerequisites unavailable:$missing"
    gate2_concurrent_sessions_not_run "$out"
    cp "$out/result.json" "$out/logs/not-run.json"
    if [ "$require_real" = "1" ]; then
      echo "concurrent-sessions e2e: $reason" >&2
      exit 1
    fi
    notrun_artifact="$(artifact_json logs/not-run.json '034 real Gate 2 not-run reason')"
    jq -s '.' \
      <(proof_json '034.concurrent-sessions.real-gate2.not-run' not-run real-gate \
        concurrent-sessions-not-run 'real Gate 2 was not run' "$notrun_artifact" "$reason") \
      >"$proofs"
  else
    gate2_concurrent_sessions_run "$ROOT" "$out" "$baseline_commit" "$samples" "$warmups"
    performance="$out/logs/performance.json"
    environment_id="$(jq -r '.candidate.environmentId' "$performance")"
    runtime="$(jq -c --arg environmentId "$environment_id" '
      {schema:"hideout.runtime-evidence-binding/v1",family:.runtime.family,
       revision:.runtime.revision,artifactSHA256:.runtime.artifactSHA256,
       environmentId:$environmentId,hostOS:.host.os,hostArch:.host.arch,
       guestArch:"aarch64",buildCommit:.runtime.buildCommit,buildDirty:.runtime.buildDirty}
    ' "$performance")"
    isolation_artifact="$(artifact_json result.json '034 real ordinary-target isolation gate result')"
    performance_artifact="$(artifact_json logs/performance.json '034 30-sample baseline performance comparison')"
    jq -s '.' \
      <(proof_json '034.concurrent-sessions.real-gate2.isolation' passed real-gate \
        concurrent-sessions-real-gate2 \
        'validated real Lima concurrent ownership, isolation, sibling survival, and stop semantics' \
        "$isolation_artifact" 'real macOS arm64 Lima evidence' "$runtime") \
      <(proof_json '034.concurrent-sessions.real-gate2.performance' passed real-gate \
        concurrent-sessions-performance-real-gate2 \
        'validated 30-sample warm attach and static-workspace performance against pre-034 baseline' \
        "$performance_artifact" 'same host, workspace fixture, and runtime digest' "$runtime") \
      >"$proofs"
  fi
fi

write_manifest "$proofs"
scan_public_evidence
rm -f "$proofs"
echo "concurrent-sessions e2e: passed mode=$mode evidence=$manifest"
