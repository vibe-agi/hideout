#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

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

git_commit() { git rev-parse --short=12 HEAD 2>/dev/null || printf unknown; }
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
  retained_gate2_log=""
  if [ -n "$gate2_evidence" ]; then
    . scripts/lib/retained-gate2-evidence.sh
    if ! retained_gate2_log="$(resolve_retained_gate2_log "$gate2_evidence" "$(git_commit)")"; then
      reason="retained Gate 2 evidence is invalid: $gate2_evidence"
    fi
  fi
  if [ -n "$gate2_evidence" ] && [ -z "$retained_gate2_log" ]; then
    printf '%s\n' "$reason" >"$out/$log"
    if [ "$require_real" = "1" ]; then echo "projection-e2e: $reason" >&2; exit 1; fi
    notrun_artifact="$(artifact_json "$log" "030 real Gate 2 not-run reason")"
    jq -s '.' <(proof_json "030.projection.real-gate2.not-run" not-run real-gate projection-real-gate \
      "projection real Gate 2 was not run" "$notrun_artifact" "$reason" "030.SC-008") >"$proofs_file"
  elif [ -z "$gate2_evidence" ] && { ! command -v limactl >/dev/null 2>&1 || ! command -v go >/dev/null 2>&1; }; then
    reason="real Lima prerequisites unavailable"
    printf '%s\n' "$reason" >"$out/$log"
    if [ "$require_real" = "1" ]; then echo "projection-e2e: $reason" >&2; exit 1; fi
    notrun_artifact="$(artifact_json "$log" "030 real Gate 2 not-run reason")"
    jq -s '.' <(proof_json "030.projection.real-gate2.not-run" not-run real-gate projection-real-gate \
      "projection real Gate 2 was not run" "$notrun_artifact" "$reason" "030.SC-008") >"$proofs_file"
  else
    if [ -n "$retained_gate2_log" ]; then
      cp "$retained_gate2_log" "$out/$log"
      : >"$out/logs/real-gate2.err"
    else
      if ! HIDEOUT_GATE2_REQUIRE_PROJECTION=1 scripts/test-gate2-lima.sh >"$out/$log" 2>"$out/logs/real-gate2.err"; then
        cat "$out/$log" "$out/logs/real-gate2.err" >&2
        exit 1
      fi
    fi
    grep -q '^gate2: passed$' "$out/$log"
    for marker in projection_code_open projection_privacy_three_channel projection_trusted_grant; do
      grep -q "^${marker}=passed$" "$out/$log"
    done
    real_artifact="$(artifact_json "$log" "030 real macOS arm64 Lima projection log")"
    jq -s '.' \
      <(proof_json "030.projection.real-gate2.code-open" passed real-gate projection-code-open \
        "real guest code shim opened the mapped host resource in safe mode" "$real_artifact" "real backend proof" \
        "030.SC-001 030.SC-002 030.SC-004") \
      <(proof_json "030.projection.real-gate2.privacy-three-channel" passed real-gate projection-alias-privacy \
        "alias identity, Git, and mount metadata probes passed with preserve control" "$real_artifact" "real backend proof" \
        "030.FR-014 030.FR-015 030.FR-016 030.SC-005") \
      <(proof_json "030.projection.real-gate2.trusted-grant" passed real-gate projection-trusted-grant \
        "same-session trusted decision approve and revoke lifecycle passed" "$real_artifact" "real backend proof" \
        "030.FR-010 030.FR-012 030.SC-006") >"$proofs_file"
  fi
fi

validate_public_logs
write_manifest "$proofs_file"
rm -f "$proofs_file"
echo "projection-e2e: passed mode=$mode evidence=$manifest"
