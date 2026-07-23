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
      echo "usage: scripts/test-host-app-pack-e2e.sh [--local-fast|--real-gate2] [--require-real] [--gate2-evidence <manifest>] [--out <dir>]"
      exit 0
      ;;
    *) echo "host-app-pack-e2e: unknown argument: $1" >&2; exit 2 ;;
  esac
done

if [ -z "$out" ]; then
  out="$(mktemp -d "${TMPDIR:-/tmp}/hideout-host-app-pack-e2e.XXXXXX")"
fi
mkdir -p "$out/logs"
out="$(cd "$out" && pwd -P)"
manifest="$out/product-hardening-evidence.json"
log="$out/logs/host-app-pack-e2e.out"
err_log="$out/logs/host-app-pack-e2e.err"
raw_real_log=""
raw_real_err=""
strict_tmp=""
cleanup() {
  rm -f "${raw_real_log:-}" "${raw_real_err:-}"
  if [ -n "${strict_tmp:-}" ] && [ -d "$strict_tmp" ]; then
    find "$strict_tmp" -depth -delete 2>/dev/null || true
  fi
}
trap cleanup EXIT

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
  local status="$1" reason="$2" evidence_class="$3"
  local artifact_sha
  artifact_sha="$(sha256_file "$log")"
  jq -n \
    --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --arg commit "$(git_commit)" \
    --argjson dirty "$(git_dirty)" \
    --arg status "$status" \
    --arg reason "$reason" \
    --arg evidenceClass "$evidence_class" \
    --arg artifactSHA "$artifact_sha" \
    '{
      version:"hideout.product-hardening-evidence/v1",
      generatedAt:$generatedAt,
      commit:$commit,
      dirty:$dirty,
      proofs:[{
        proofId:"032.host-app-pack.real-gate2.external",
        featureId:"032-community-host-app-recipes",
        mode:(if $status == "passed" then "real-gate" else "local-fast" end),
        evidenceClass:$evidenceClass,
        status:$status,
        commandSummary:(if $status == "passed" then
          "real macOS arm64 Lima external host-app pack lifecycle and host effect"
        else "local mechanics cannot satisfy external host-app real Gate 2" end),
        coveredClaims:[
          {claimId:"032.FR-033",source:"spec",description:"External pack real-provider completion contract"},
          {claimId:"032.SC-001",source:"spec",description:"External workspace command lifecycle"},
          {claimId:"032.SC-002",source:"spec",description:"Existing HostFS authority consumption"},
          {claimId:"032.SC-003",source:"spec",description:"Core-observed app identity and content trust"},
          {claimId:"032.SC-004",source:"spec",description:"Core safety floor and unsafe identity refusal"},
          {claimId:"032.SC-005",source:"spec",description:"Exact permission difference and acceptance"},
          {claimId:"032.SC-006",source:"spec",description:"Old-session immutability and lifecycle invalidation"},
          {claimId:"032.SC-007",source:"spec",description:"No generic fallback after disable or revoke"},
          {claimId:"032.SC-008",source:"spec",description:"Generic binding and decision scope"},
          {claimId:"032.SC-009",source:"spec",description:"Bounded deterministic lifecycle"},
          {claimId:"032.SC-010",source:"spec",description:"Public evidence redaction"},
          {claimId:"032.SC-011",source:"spec",description:"Contributor pack uses existing Core provider"},
          {claimId:"032.SC-012",source:"spec",description:"Manager and CLI lifecycle parity"},
          {claimId:"032.SC-013",source:"spec",description:"Built-in and external generic path parity"},
          {claimId:"032.SC-014",source:"spec",description:"Only retained real-backend evidence satisfies completion"}
        ],
        prerequisites:(if $status == "passed" then [
          {name:"host",status:"available",reason:"Darwin arm64"},
          {name:"backend",status:"available",reason:"real Lima"},
          {name:"host-application",status:"available",reason:"Core-verified Visual Studio Code identity"}
        ] else [{name:"real-gate2",status:"missing",reason:$reason}] end),
        artifacts:[{kind:"log",path:"logs/host-app-pack-e2e.out",sha256:$artifactSHA,redactionStatus:"passed",description:"032 external host-app Gate 2 output"}],
        redactionStatus:(if $status == "passed" then "passed" else "not-run" end),
        notes:[$reason]
      }]
    } | if $status == "not-run" then .proofs[0].notRunReason = $reason else . end' >"$manifest"
  go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json "$manifest" >/dev/null
}

validate_public_log() {
  local secret_pattern='claim_[0-9a-f]{16,}|cap_[A-Za-z0-9]{12,}|ui_[A-Za-z0-9]{12,}|HIDEOUT_SECRET_[A-Z0-9_]+=|socks5://[^[:space:]]+:[^[:space:]]+@|providerRef'
  if grep -E "$secret_pattern" "$log" >/dev/null 2>&1; then
    echo "host-app-pack-e2e: public log contains control-plane material" >&2
    return 1
  fi
  if [ "$mode" = "real-gate2" ] && {
    grep -F -- "$HOME/" "$log" >/dev/null 2>&1 ||
      grep -F -- "USER=$(id -un)" "$log" >/dev/null 2>&1 ||
      grep -F -- "LOGNAME=$(id -un)" "$log" >/dev/null 2>&1 ||
      grep -E '(^|[[:space:]=])/(Applications|System|Users|Volumes|private|tmp|var)/' "$log" >/dev/null 2>&1;
  }; then
    echo "host-app-pack-e2e: public log contains host identity or an absolute host path" >&2
    return 1
  fi
}

if [ "$mode" = "local-fast" ]; then
  scripts/test-host-app-pack-smoke.sh >"$log" 2>"$err_log"
  printf 'real_external_host_effect=not-run\n' >>"$log"
  validate_public_log
  write_manifest not-run "local, native, and package self-test evidence cannot satisfy real external-pack Gate 2" "host-app-pack-local-mechanics"
  echo "host-app-pack-e2e: passed mode=local-fast status=not-run evidence=$manifest"
  exit 0
fi

reason=""
strict_root=""
if [ -n "$gate2_evidence" ]; then
  if ! strict_root="$(strict_projection_evidence_root "$gate2_evidence")" ||
    ! validate_strict_projection_evidence "$strict_root"; then
    strict_root=""
    reason="retained Gate 2 evidence is not strict 043 exact-package evidence: $gate2_evidence"
  fi
elif [ "$(uname -s)" != "Darwin" ] || [ "$(uname -m)" != "arm64" ]; then
  reason="real Gate 2 requires a macOS arm64 host"
elif ! command -v limactl >/dev/null 2>&1; then
  reason="limactl is unavailable"
elif [ ! -d "/Applications/Visual Studio Code.app" ] && [ ! -d "$HOME/Applications/Visual Studio Code.app" ]; then
  reason="Visual Studio Code is unavailable in a supported application root"
fi
if [ -n "$reason" ]; then
  printf '%s\n' "$reason" >"$log"
  if [ "$require_real" = "1" ]; then
    echo "host-app-pack-e2e: $reason" >&2
    exit 1
  fi
  write_manifest not-run "$reason" "host-app-pack-real-gate-not-run"
  echo "host-app-pack-e2e: passed mode=real-gate2 status=not-run evidence=$manifest"
  exit 0
fi

if [ -z "$strict_root" ]; then
  strict_tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-host-app-pack-strict.XXXXXX")"
  if ! scripts/test-projection-readiness-lima-e2e.sh --require-real \
    --out "$strict_tmp/evidence" >"$out/logs/strict-producer.out" 2>"$out/logs/strict-producer.err"; then
    echo "host-app-pack-e2e: strict projection producer failed" >&2
    tail -n 160 "$out/logs/strict-producer.out" >&2
    tail -n 160 "$out/logs/strict-producer.err" >&2
    exit 1
  fi
  strict_root="$strict_tmp/evidence"
fi
copy_strict_projection_feature "$strict_root" "$out" "032-community-host-app-recipes"
printf 'strict_projection_readiness=passed\n' >"$log"
: >"$err_log"
validate_public_log
echo "host-app-pack-e2e: passed mode=real-gate2 status=passed evidence=$manifest"
