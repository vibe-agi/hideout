#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/lib/gate2-resource-lifecycle-performance.sh
. "$ROOT/scripts/lib/gate2-resource-lifecycle-performance.sh"
# shellcheck source=scripts/lib/gate2-resource-lifecycle.sh
. "$ROOT/scripts/lib/gate2-resource-lifecycle.sh"

mode="local-fast"
require_real=0
probe=0
feature_040=0
out="${HIDEOUT_036_EVIDENCE_DIR:-$ROOT/.hideout-release-evidence/036-resource-lifecycle}"
package_archive=""
baseline_commit="127ef937b120f0faa719611abcb3a1816e331266"
baseline_explicit=0
baseline_040_commit="322c3c6cc9561eea21d4ed20ab78172429654c54"
samples=30
warmups=3
races=100
proof_040_real_lifecycle="040.attach-reservation.real-gate2.lifecycle"
proof_040_real_performance="040.attach-reservation.real-gate2.performance"
proof_040_real_not_run="040.attach-reservation.real-gate2.not-run"

usage() {
  cat <<'USAGE'
Usage: scripts/test-lifecycle-lima-e2e.sh [options]

  --local-fast                 run complete local lifecycle evidence (default)
  --real-gate2 | --all         run the real macOS arm64 Lima lifecycle gate
  --require-real               fail rather than emit supporting not-run evidence
  --feature-040                emit only 040 proof using the exact pre-040 baseline
  --package <tar.gz>           reuse and bind the exact candidate package
  --baseline-commit <commit>   exact feature comparison commit
  --samples <n>                measured samples (real evidence requires at least 30)
  --warmups <n>                excluded warm-up samples (real evidence requires at least 3)
  --iterations <n>             attach/stop races (real evidence requires at least 100)
  --out | --evidence-out <dir> evidence output directory
  --probe                      permit reduced counts and emit diagnostics, never product proof
USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --local-fast) mode="local-fast"; shift ;;
    --real-gate2|--all) mode="real-gate2"; shift ;;
    --require-real) require_real=1; shift ;;
    --feature-040) feature_040=1; shift ;;
    --package) package_archive="${2:-}"; shift 2 ;;
    --baseline-commit) baseline_commit="${2:-}"; baseline_explicit=1; shift 2 ;;
    --samples) samples="${2:-}"; shift 2 ;;
    --warmups) warmups="${2:-}"; shift 2 ;;
    --iterations) races="${2:-}"; shift 2 ;;
    --out|--evidence-out) out="${2:-}"; shift 2 ;;
    --probe) probe=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "resource-lifecycle e2e: unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [ "$feature_040" = "1" ] && [ "$baseline_explicit" = "0" ]; then
  baseline_commit="$baseline_040_commit"
fi

for value_name in samples warmups races; do
  eval "value=\${$value_name}"
  case "$value" in
    ''|*[!0-9]*) echo "resource-lifecycle e2e: --$value_name must be an integer" >&2; exit 2 ;;
  esac
done
if [ "$mode" = "real-gate2" ] && [ "$probe" != "1" ]; then
  [ "$samples" -ge 30 ] || { echo "resource-lifecycle e2e: real evidence requires 30 samples" >&2; exit 2; }
  [ "$warmups" -ge 3 ] || { echo "resource-lifecycle e2e: real evidence requires 3 warmups" >&2; exit 2; }
  [ "$races" -ge 100 ] || { echo "resource-lifecycle e2e: real evidence requires 100 attach/stop races" >&2; exit 2; }
fi
if [ "${HIDEOUT_036_PROBE_SKIP_PROVEN_PREFIX:-0}" = "1" ] && [ "$probe" != "1" ]; then
  echo "resource-lifecycle e2e: prefix skipping is forbidden for product evidence" >&2
  exit 2
fi
if ! printf '%s' "$baseline_commit" | grep -Eq '^[0-9a-f]{40}$' ||
  ! git cat-file -e "$baseline_commit^{commit}" 2>/dev/null; then
  echo "resource-lifecycle e2e: baseline commit must be an available full commit" >&2
  exit 2
fi
if [ -n "$package_archive" ] && [ ! -f "$package_archive" ]; then
  echo "resource-lifecycle e2e: package archive does not exist" >&2
  exit 2
fi

mkdir -p "$out/logs" "$out/reports"
out="$(cd "$out" && pwd -P)"
manifest="$out/product-hardening-evidence.json"
registry="$out/reports/proof-registry.json"
go run ./cmd/hideout support proof-registry --json >"$registry"
for proof_id in "$proof_040_real_lifecycle" "$proof_040_real_performance" "$proof_040_real_not_run"; do
  jq -e --arg id "$proof_id" '.requirements[] | select(.proofId == $id)' "$registry" >/dev/null || {
    echo "resource-lifecycle e2e: 040 proof is not registered: $proof_id" >&2
    exit 1
  }
done

sha256_file() { shasum -a 256 "$1" | awk '{print $1}'; }
git_dirty() {
  if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then printf true; else printf false; fi
}

claims_json() {
  local proof_id="$1"
  jq -c --arg id "$proof_id" '
    [.requirements[] | select(.proofId == $id) | .claimIds[] |
      {claimId:.,source:"spec",description:("registered contract " + .),scope:"resource-lifecycle"}]
  ' "$registry"
}

artifact_json() {
  local relative="$1" description="$2"
  jq -n --arg path "$relative" --arg sha "$(sha256_file "$out/$relative")" \
    --arg description "$description" \
    '{kind:"log",path:$path,sha256:$sha,redactionStatus:"passed",description:$description}'
}

proof_json() {
  local proof_id="$1" status="$2" class="$3" summary="$4" artifact="$5"
  local reason="$6" runtime="${7:-null}" feature_id="${8:-036-resource-lifecycle-final-session-stop}" claims
  claims="$(claims_json "$proof_id")"
  [ "$(jq 'length' <<<"$claims")" -gt 0 ] || {
    echo "resource-lifecycle e2e: proof is not registered: $proof_id" >&2
    return 1
  }
  jq -n --arg proofId "$proof_id" --arg status "$status" --arg class "$class" --arg featureId "$feature_id" \
    --arg summary "$summary" --arg reason "$reason" --argjson claims "$claims" \
    --argjson artifact "$artifact" --argjson runtime "$runtime" '
	{proofId:$proofId,featureId:$featureId,mode:"real-gate",
     evidenceClass:$class,status:$status,commandSummary:$summary,coveredClaims:$claims,
     prerequisites:(if $status == "not-run" then
       [{name:"real-macos-arm64-lima-vscode",status:"missing",reason:$reason}]
       else [{name:"real-macos-arm64-lima-vscode",status:"available"}] end),
     artifacts:(if $artifact == null then [] else [$artifact] end),
     redactionStatus:(if $status == "not-run" then "not-run" else "passed" end)}
     + (if $status == "not-run" then {notRunReason:$reason} else {} end)
     + (if $runtime == null then {} else {runtime:$runtime} end)'
}

write_manifest() {
  local proofs="$1" package_identity="${GATE2_036_PACKAGE_IDENTITY:-null}"
  jq -n --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
    --arg commit "$(git rev-parse HEAD)" --argjson dirty "$(git_dirty)" \
    --argjson packageIdentity "$package_identity" --slurpfile proofs "$proofs" '
    {version:"hideout.product-hardening-evidence/v1",generatedAt:$generatedAt,
     commit:$commit,dirty:$dirty,proofs:$proofs[0]} +
     (if $packageIdentity == null then {} else {packageIdentity:$packageIdentity} end)' >"$manifest"
  go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json "$manifest" \
    >"$out/logs/evidence-schema.out" 2>"$out/logs/evidence-schema.err"
}

scan_public_evidence() {
  local forbidden='claim_[0-9a-f]{16,}|cap_[A-Za-z0-9]{12,}|HIDEOUT_SECRET_[A-Z0-9_]+=|socks5://[^[:space:]]+:[^[:space:]]+@|machineId|providerRef'
  if grep -E "$forbidden" "$out/result.json" "$out/logs/performance.json" "$manifest" >/dev/null 2>&1; then
    echo "resource-lifecycle e2e: public evidence contains control-plane material" >&2
    return 1
  fi
}

if [ "$mode" = "local-fast" ]; then
  [ "$probe" = "0" ] || { echo "resource-lifecycle e2e: --probe is only valid with --real-gate2" >&2; exit 2; }
  exec scripts/test-lifecycle-smoke.sh --out "$out"
fi

missing=""
for tool in go jq limactl shasum perl python3 git ps awk comm; do
  command -v "$tool" >/dev/null 2>&1 || missing="$missing $tool"
done
[ "$(uname -s)" = Darwin ] || missing="$missing macOS"
[ "$(uname -m)" = arm64 ] || missing="$missing arm64"
[ -d "/Applications/Visual Studio Code.app" ] ||
  [ -d "$HOME/Applications/Visual Studio Code.app" ] || missing="$missing Visual-Studio-Code"

proofs="$out/reports/proofs.json"
if [ -n "$missing" ]; then
  reason="real Gate 2 prerequisites unavailable:$missing"
  jq -n --arg reason "$reason" '{schema:"hideout.lifecycle-real-gate2-not-run/v1",status:"not-run",reason:$reason}' \
    >"$out/logs/not-run.json"
  if [ "$require_real" = "1" ] || [ "$probe" = "1" ]; then
    echo "resource-lifecycle e2e: $reason" >&2
    exit 1
  fi
  artifact="$(artifact_json logs/not-run.json 'real Gate 2 not-run reason')"
  if [ "$feature_040" = "1" ]; then
	  jq -s '.' \
	    <(proof_json "$proof_040_real_not_run" not-run \
	      attach-reservation-real-gate2-not-run 'real attach-reservation Gate 2 was not run' "$artifact" "$reason" null \
	      '040-lifecycle-attach-reservation') >"$proofs"
  else
	  jq -s '.' \
	    <(proof_json '036.lifecycle.real-gate2.not-run' not-run \
	      resource-lifecycle-real-gate2-not-run 'real lifecycle Gate 2 was not run' "$artifact" "$reason") \
	    <(proof_json "$proof_040_real_not_run" not-run \
	      attach-reservation-real-gate2-not-run 'real attach-reservation Gate 2 was not run' "$artifact" "$reason" null \
	      '040-lifecycle-attach-reservation') >"$proofs"
  fi
  write_manifest "$proofs"
  rm -f "$proofs"
  echo "resource-lifecycle e2e: passed mode=real-gate2 status=not-run evidence=$manifest"
  exit 0
fi

gate2_resource_lifecycle_run "$ROOT" "$out" "$baseline_commit" "$samples" "$warmups" "$races" "$package_archive"
if [ "$probe" = "1" ]; then
  echo "resource-lifecycle e2e: probe passed; no product proof was emitted"
  exit 0
fi

performance="$out/logs/performance.json"
jq -e '.status == "passed" and .checks.attachWaitsForReconciliation and
  .checks.attachStopRaceSafe and .checks.cancellationBeforeOwnerClean and
  .checks.restartBeforeOwnerClean and .checks.restartAfterOwnerFailClosed and
  .checks.siblingSessionPreserved and .checks.stopUnknownBlocksAttach' "$out/result.json" >/dev/null
jq -e '.status == "passed" and .methodology.samples >= 30 and
  .candidateP95Ms <= 2000 and .candidateWithinTwoSeconds*100 >= .candidateSampleCount*95 and
  .candidate.commit != "" and .candidate.dirty == false and .runtime.buildDirty == false' "$performance" >/dev/null
runtime="$(jq -c --arg environmentId "$GATE2_036_ENV_ID" '
  {schema:"hideout.runtime-evidence-binding/v1",family:.runtime.family,
   revision:.runtime.revision,artifactSHA256:.runtime.artifactSHA256,
   environmentId:$environmentId,hostOS:.host.os,hostArch:.host.arch,
   guestArch:"aarch64",buildCommit:.runtime.buildCommit,buildDirty:.runtime.buildDirty}
' "$performance")"
lifecycle_artifact="$(artifact_json result.json 'real Lima lifecycle result')"
performance_artifact="$(artifact_json logs/performance.json 'user-command performance comparison')"
if [ "$feature_040" = "1" ]; then
	jq -s '.' \
	  <(proof_json "$proof_040_real_lifecycle" passed attach-reservation-real-gate2 \
	    'validated real reconciliation-first, reservation-first, cancellation, restart boundaries, siblings, and redaction' \
	    "$lifecycle_artifact" 'real macOS arm64 Lima attach-reservation evidence' "$runtime" \
	    '040-lifecycle-attach-reservation') \
	  <(proof_json "$proof_040_real_performance" passed attach-reservation-performance-real-gate2 \
	    'validated 30 warm attach samples with nearest-rank p95 and at least 95 percent within two seconds' \
	    "$performance_artifact" 'same fixture, host, exact source, and exact runtime artifact' "$runtime" \
	    '040-lifecycle-attach-reservation') >"$proofs"
else
	jq -s '.' \
	  <(proof_json '036.lifecycle.real-gate2.lifecycle' passed resource-lifecycle-real-gate2 \
	    'validated real final-session stop, races, retained state, handoff, restart, recovery, and observation' \
	    "$lifecycle_artifact" 'real macOS arm64 Lima evidence' "$runtime") \
	  <(proof_json '036.lifecycle.real-gate2.performance' passed resource-lifecycle-performance-real-gate2 \
	    'validated hideout run -- git status --short against the exact pre-036 baseline' \
	    "$performance_artifact" 'same fixture, host, and exact runtime artifact' "$runtime") \
	  <(proof_json "$proof_040_real_lifecycle" passed attach-reservation-real-gate2 \
	    'validated real reconciliation-first, reservation-first, cancellation, restart boundaries, siblings, and redaction' \
	    "$lifecycle_artifact" 'real macOS arm64 Lima attach-reservation evidence' "$runtime" \
	    '040-lifecycle-attach-reservation') \
	  <(proof_json "$proof_040_real_performance" passed attach-reservation-performance-real-gate2 \
	    'validated 30 warm attach samples with nearest-rank p95 and at least 95 percent within two seconds' \
	    "$performance_artifact" 'same fixture, host, exact source, and exact runtime artifact' "$runtime" \
	    '040-lifecycle-attach-reservation') >"$proofs"
fi
write_manifest "$proofs"
scan_public_evidence
rm -f "$proofs"
echo "resource-lifecycle e2e: passed mode=real-gate2 evidence=$manifest"
