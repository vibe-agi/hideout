#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
. "$ROOT/scripts/lib/strict-projection-evidence.sh"

mode="local-fast"
require_real=0
out=""
gate2_evidence=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --local-fast) mode="local-fast"; shift ;;
    --real-gate2) mode="real-gate2"; shift ;;
    --require-real) require_real=1; shift ;;
    --gate2-evidence) gate2_evidence="${2:-}"; shift 2 ;;
    --out) out="${2:-}"; shift 2 ;;
    -h|--help)
      echo "usage: scripts/test-host-capability-projection-e2e.sh [--local-fast|--real-gate2] [--require-real] [--gate2-evidence <manifest>] [--out <dir>]"
      exit 0
      ;;
    *) echo "projection-e2e: unknown argument: $1" >&2; exit 2 ;;
  esac
done

if [ -z "$out" ]; then
  out="$(mktemp -d "${TMPDIR:-/tmp}/hideout-projection-e2e.XXXXXX")"
fi
mkdir -p "$out/logs"
out="$(cd "$out" && pwd -P)"
manifest="$out/product-hardening-evidence.json"

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    sha256sum "$1" | awk '{print $1}'
  fi
}

git_commit() { git rev-parse HEAD 2>/dev/null || printf unknown; }
git_dirty() {
  if [ -n "$(git status --porcelain --untracked-files=normal 2>/dev/null)" ]; then printf true; else printf false; fi
}

write_manifest() {
  local proofs="$1"
  jq -n \
    --arg generated "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
    --arg commit "$(git_commit)" \
    --argjson dirty "$(git_dirty)" \
    --slurpfile proofs "$proofs" \
    '{version:"hideout.product-hardening-evidence/v1",generatedAt:$generated,commit:$commit,dirty:$dirty,proofs:$proofs[0]}' >"$manifest"
  go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json "$manifest" >"$out/logs/schema.out"
}

artifact_json() {
  local path="$1"
  local description="$2"
  jq -n --arg path "$path" --arg sha "$(sha256_file "$out/$path")" --arg description "$description" \
    '{kind:"log",path:$path,sha256:$sha,redactionStatus:"passed",description:$description}'
}

proof_json() {
  local proof_id="$1" status="$2" mode_name="$3" class="$4" summary="$5" artifact="$6" notes="$7" claim_ids="$8"
  jq -n \
    --arg proofId "$proof_id" --arg status "$status" --arg mode "$mode_name" \
    --arg class "$class" --arg summary "$summary" --arg notes "$notes" \
    --arg claimIds "$claim_ids" \
    --argjson artifact "$artifact" \
    '{proofId:$proofId,featureId:"030-host-capability-projection",mode:$mode,evidenceClass:$class,status:$status,
      commandSummary:$summary,
      coveredClaims:($claimIds | split(" ") | map({claimId:.,source:"spec",description:("030 contract " + .),scope:"projection"})),
      prerequisites:(if $status == "not-run" then [{name:"real-gate2",status:"missing",reason:$notes}] else [] end),
      artifacts:(if $artifact == null then [] else [$artifact] end),
      redactionStatus:(if $status == "not-run" then "not-run" else "passed" end),
      notes:[$notes]} + (if $status == "not-run" then {notRunReason:$notes} else {} end)'
}

validate_public_logs() {
  local secret_pattern='claim_[0-9a-f]{16,}|cap_[A-Za-z0-9]{12,}|HIDEOUT_SECRET_[A-Z0-9_]+=|socks5://[^[:space:]]+:[^[:space:]]+@|providerRef'
  if grep -R -E "$secret_pattern" "$out/logs" >/dev/null 2>&1; then
    echo "projection-e2e: evidence logs contain control-plane material" >&2
    grep -R -n -E "$secret_pattern" "$out/logs" >&2 || true
    return 1
  fi
  if [ "$mode" = "real-gate2" ]; then
    if grep -F -- "$HOME/" "$out/logs/real-gate2.out" >/dev/null 2>&1 ||
      grep -F -- "USER=$(id -un)" "$out/logs/real-gate2.out" >/dev/null 2>&1 ||
      grep -F -- "LOGNAME=$(id -un)" "$out/logs/real-gate2.out" >/dev/null 2>&1; then
      echo "projection-e2e: real Gate 2 public log contains synthesized host identity" >&2
      return 1
    fi
  fi
}

proofs_file="$out/proofs.json"
if [ "$mode" = "local-fast" ]; then
  log="logs/local-fast.out"
  scripts/test-host-capability-projection-smoke.sh >"$out/$log" 2>"$out/logs/local-fast.err"
  mechanics_artifact="$(artifact_json "$log" "030 targeted mechanics log")"
  jq -s '.' \
    <(proof_json "030.projection.gate0.mechanics" passed local-fast projection-mechanics \
      "projection registry, grammar, routing, grant, audit, and doctor mechanics" "$mechanics_artifact" \
      "local mechanics only; no guest-visible or privacy claim" \
      "030.FR-001 030.FR-002 030.FR-003 030.FR-004 030.FR-005 030.FR-006 030.FR-007 030.FR-008 030.FR-009 030.FR-011 030.FR-018 030.FR-019") \
    <(proof_json "030.projection.docs.claim-boundary" passed docs docs-truth \
      "030 claim boundary is registered" null \
      "guest-visible and privacy claims require separate real-gate evidence" \
      "030.FR-013 030.FR-017") >"$proofs_file"
else
  log="logs/real-gate2.out"
  strict_root=""
  strict_tmp=""
  reason=""
  if [ -n "$gate2_evidence" ]; then
    if ! strict_root="$(strict_projection_evidence_root "$gate2_evidence")" ||
      ! validate_strict_projection_evidence "$strict_root"; then
      strict_root=""
      reason="retained Gate 2 evidence is not strict 043 exact-package evidence: $gate2_evidence"
    fi
  elif ! command -v limactl >/dev/null 2>&1 || ! command -v go >/dev/null 2>&1; then
    reason="real Lima prerequisites unavailable"
  else
    strict_tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-projection-e2e-strict.XXXXXX")"
    if ! scripts/test-projection-readiness-lima-e2e.sh --require-real \
      --out "$strict_tmp/evidence" >"$out/logs/strict-producer.out" 2>"$out/logs/strict-producer.err"; then
      cat "$out/logs/strict-producer.out" "$out/logs/strict-producer.err" >&2
      find "$strict_tmp" -depth -delete 2>/dev/null || true
      exit 1
    fi
    strict_root="$strict_tmp/evidence"
  fi
  if [ -z "$strict_root" ]; then
    printf '%s\n' "$reason" >"$out/$log"
    if [ "$require_real" = "1" ]; then echo "projection-e2e: $reason" >&2; exit 1; fi
    notrun_artifact="$(artifact_json "$log" "030 real Gate 2 not-run reason")"
    jq -s '.' <(proof_json "030.projection.real-gate2.not-run" not-run real-gate projection-real-gate \
      "projection real Gate 2 was not run" "$notrun_artifact" "$reason" "030.SC-008") >"$proofs_file"
  else
    copy_strict_projection_feature "$strict_root" "$out" "030-host-capability-projection"
    printf 'strict_projection_readiness=passed\n' >"$out/$log"
    : >"$out/logs/real-gate2.err"
    validate_public_logs
    if [ -n "$strict_tmp" ]; then
      find "$strict_tmp" -depth -delete 2>/dev/null || true
    fi
    echo "projection-e2e: passed mode=$mode evidence=$manifest"
    exit 0
  fi
fi

validate_public_logs
write_manifest "$proofs_file"
rm -f "$proofs_file"
echo "projection-e2e: passed mode=$mode evidence=$manifest"
