#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

mode="local-fast"
require_real=0
out=""
gate2_evidence=""

usage() {
  cat <<'USAGE'
Usage:
  scripts/test-hostfs-visibility-e2e.sh [--local-fast|--real-gate2]
                                           [--require-real]
                                           [--gate2-evidence <manifest>]
                                           [--out <dir>]
USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --local-fast)
      mode="local-fast"
      shift
      ;;
    --real-gate2)
      mode="real-gate2"
      shift
      ;;
    --require-real)
      require_real=1
      shift
      ;;
    --gate2-evidence)
      gate2_evidence="${2:-}"
      shift 2
      ;;
    --out)
      out="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "hostfs-visibility-e2e: unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [ -z "$out" ]; then
  out="$(mktemp -d "${TMPDIR:-/tmp}/hideout-hostfs-visibility-e2e.XXXXXX")"
fi
mkdir -p "$out/logs" "$out/reports"
out="$(cd "$out" && pwd -P)"
manifest="$out/product-hardening-evidence.json"

require_tool() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "hostfs-visibility-e2e: missing required tool: $1" >&2
    exit 127
  fi
}

require_tool jq
require_tool go

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    echo "hostfs-visibility-e2e: missing shasum or sha256sum" >&2
    exit 127
  fi
}

git_commit() {
  git rev-parse HEAD 2>/dev/null || printf 'unknown'
}

git_dirty() {
  if [ -n "$(git status --porcelain --untracked-files=normal 2>/dev/null)" ]; then
    printf 'true'
  else
    printf 'false'
  fi
}

validate_manifest() {
  go run ./cmd/hideout-schema-validate \
    schemas/product-hardening-evidence.schema.json \
    "$manifest" >"$out/logs/evidence-schema.out" 2>"$out/logs/evidence-schema.err"
  jq -e '
    .version == "hideout.product-hardening-evidence/v1" and
    (.proofs | length > 0) and
    all(.proofs[];
      .featureId == "029-hostfs-discoverable-namespace" and
      (.proofId | startswith("029.hostfs-visibility.")) and
      (.status == "passed" or .status == "failed" or .status == "not-run")
    )
  ' "$manifest" >"$out/logs/evidence-content.out"
  go run ./cmd/hideout support proof-registry --json >"$out/logs/proof-registry.json"
  jq -e --slurpfile registry "$out/logs/proof-registry.json" '
    ($registry[0].requirements |
      map({key: .proofId, value: (.claimIds | sort)}) | from_entries) as $registered |
    all(.proofs[];
      ([.coveredClaims[].claimId] | sort) == ($registered[.proofId] // []))
  ' "$manifest" >"$out/logs/registered-claims.out"
  if grep -R -E 'claim_[0-9a-f]{16,}|cap_[A-Za-z0-9]{12,}|HIDEOUT_SECRET_[A-Z0-9_]+=|hostfs-read/(grants|state|owner|provider)|TOP-SECRET-FILE-CONTENT-029|0123456789abcdef0123456789abcdef|proxy-password-029' "$out" >/dev/null 2>&1; then
    echo "hostfs-visibility-e2e: evidence leaked injected private/control-plane material" >&2
    grep -R -n -E 'claim_[0-9a-f]{16,}|cap_[A-Za-z0-9]{12,}|HIDEOUT_SECRET_[A-Z0-9_]+=|hostfs-read/(grants|state|owner|provider)|TOP-SECRET-FILE-CONTENT-029|0123456789abcdef0123456789abcdef|proxy-password-029' "$out" >&2 || true
    exit 1
  fi
}

local_fast() {
  local log="logs/local-fast.out"
  {
	go test -count=1 -v ./internal/hostfs ./internal/broker ./internal/profile \
	  -run 'Test(ParseDiscoverSelectorsAndRejectsGlobsAndLegacyList|VisibilityScopesAreExactOneLevelAndRecursive|ServiceReturnsCoarseLockedDiscoverResults|ServiceSeeDirIsOneLevelAndExactDirectoryIsNotEnumerable|ServiceDiscoverListIsCompleteOrError|ServiceDiscoverListOmitsDiscoverDeniedEvenWithExactContentGrant|ServiceManualHomeTreeImplicitlyHidesCatalogRootsWithoutRevokingExactRead|ServiceContentTreeListCannotBypassDiscoverDeny|ServiceDiscoverTreeDepthLimitIsExplicitIncomplete|ExplicitDiscoverWriteWithoutOverlayGrantReturnsUnauthorizedWithOverlayPresent|BrokerHostFSDiscoveryOmitsFullMetadataAndUsesTypedDenial|BrokerHostFSReadProposalPublishesReferencesOnlyForRealDecisions|BrokerExplicitReadDenyNeverCallsDecisionProvider|HostFSErrorVocabularyAndSchema|LoadRejectsLegacyListOnlyProfileWithTypedMigrationError)'
	go test -count=1 -v ./internal/decision ./internal/manager \
	  -run 'Test(StoreHostFSReadProviderReopenAndActivationFailureAreNarrow|HostFSReadProviderDeduplicatesAcrossInstancesWithoutExtendingDeadline|HostFSReadProviderEnforcesPendingAndRollingRateLimitsWithoutFalseReference|SanitizeHostFSReadReasonStripsTerminalControlsAndPreservesUTF8Boundary|HostFSReadApprovalActivatesSameRunningServiceAndReopenRequiresLiveOwner|HostFSReadGrantRejectsSymlinkRetargetPolicyDenyExpiryAndMalformedState|HostFSReadOperatorSurfacesOmitSecretsContentSymlinkTargetAndPrivateState)'
  } >"$out/$log" 2>"$out/logs/local-fast.err"
  local sum
  sum="$(sha256_file "$out/$log")"
  jq -n \
    --arg generated "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
    --arg commit "$(git_commit)" \
    --argjson dirty "$(git_dirty)" \
    --arg log "$log" \
    --arg sha "$sum" \
    '{
      version: "hideout.product-hardening-evidence/v1",
      generatedAt: $generated,
      commit: $commit,
      dirty: $dirty,
      proofs: [
        {
          proofId: "029.hostfs-visibility.unit.policy",
          featureId: "029-hostfs-discoverable-namespace",
          mode: "unit",
          evidenceClass: "hostfs-visibility-policy",
          status: "passed",
          commandSummary: "targeted HostFS visibility policy and complete-listing tests",
          coveredClaims: [
            "029.FR-001", "029.FR-003", "029.FR-004", "029.FR-005", "029.FR-006"
          ] | map({claimId: ., source: "spec", description: "Per-root discover selectors and bounded complete listing", scope: "hostfs"}),
          prerequisites: [{name: "go", status: "available"}],
          artifacts: [],
          redactionStatus: "passed",
          notes: ["local mechanism proof only; does not claim guest FUSE behavior"]
        },
        {
          proofId: "029.hostfs-visibility.unit.typed-errno",
          featureId: "029-hostfs-discoverable-namespace",
          mode: "unit",
          evidenceClass: "hostfs-typed-error",
          status: "passed",
          commandSummary: "targeted broker typed-error vocabulary and mapping tests",
          coveredClaims: [
            "029.FR-007", "029.FR-008", "029.FR-009"
          ] | map({claimId: ., source: "spec", description: "Typed HostFS error vocabulary is validated independently of prose", scope: "broker"}),
          prerequisites: [{name: "go", status: "available"}],
          artifacts: [],
          redactionStatus: "passed",
          notes: ["local mechanism proof only; does not claim real errno delivery"]
        },
        {
          proofId: "029.hostfs-visibility.local-fast.decision-lifecycle",
          featureId: "029-hostfs-discoverable-namespace",
          mode: "local-fast",
          evidenceClass: "hostfs-read-decision",
          status: "passed",
          commandSummary: "targeted provider dedup, limits, approval, reopen, and invalidation tests",
          coveredClaims: [
            "029.FR-014", "029.FR-015", "029.FR-016", "029.FR-017",
            "029.FR-018", "029.FR-019", "029.FR-020"
          ] | map({claimId: ., source: "spec", description: "Equivalent requests share one bounded decision lifecycle", scope: "decision"}),
          prerequisites: [{name: "go", status: "available"}],
          artifacts: [{kind: "log", path: $log, sha256: $sha, redactionStatus: "passed", description: "targeted local-fast test log"}],
          redactionStatus: "passed",
          notes: ["does not satisfy real Gate 2 namespace or live-grant proof"]
        },
        {
          proofId: "029.hostfs-visibility.local-fast.redaction",
          featureId: "029-hostfs-discoverable-namespace",
          mode: "local-fast",
          evidenceClass: "hostfs-read-redaction",
          status: "passed",
          commandSummary: "real secret, token, content, symlink-target, and private-path injection test",
          coveredClaims: [
            "029.FR-024", "029.FR-025", "029.FR-026"
          ] | map({claimId: ., source: "spec", description: "Operator evidence omits injected private and control-plane values", scope: "evidence"}),
          prerequisites: [{name: "go", status: "available"}],
          artifacts: [{kind: "log", path: $log, sha256: $sha, redactionStatus: "passed", description: "redaction injection test log"}],
          redactionStatus: "passed",
          notes: ["injected values are asserted absent and are not copied into this artifact"]
        }
      ]
    }' >"$manifest"
  validate_manifest
  jq -e '
    [.proofs[].proofId] as $ids |
    all([
      "029.hostfs-visibility.unit.policy",
      "029.hostfs-visibility.unit.typed-errno",
      "029.hostfs-visibility.local-fast.decision-lifecycle",
      "029.hostfs-visibility.local-fast.redaction"
    ][]; . as $id | $ids | index($id)) and
    all(.proofs[]; .proofId != "029.hostfs-visibility.real-gate2.namespace" and .proofId != "029.hostfs-visibility.real-gate2.live-grant")
  ' "$manifest" >"$out/logs/required-proofs.out"
  echo "hostfs-visibility-e2e: local-fast passed evidence=$manifest"
}

write_real_not_run() {
  local reason="$1"
  local report="reports/real-gate2-prerequisites.txt"
  printf '%s\n' "$reason" >"$out/$report"
  local sum
  sum="$(sha256_file "$out/$report")"
  jq -n \
    --arg generated "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
    --arg commit "$(git_commit)" \
    --argjson dirty "$(git_dirty)" \
    --arg reason "$reason" \
    --arg sha "$sum" \
    '{
      version: "hideout.product-hardening-evidence/v1",
      generatedAt: $generated,
      commit: $commit,
      dirty: $dirty,
      proofs: [{
        proofId: "029.hostfs-visibility.real-gate2.not-run",
        featureId: "029-hostfs-discoverable-namespace",
        mode: "real-gate",
        evidenceClass: "hostfs-visibility-e2e",
        status: "not-run",
        commandSummary: "real Gate 2 HostFS visibility prerequisites unavailable",
        coveredClaims: [{claimId: "029.FR-027", source: "spec", description: "Unavailable real prerequisites are recorded without promoting local evidence", scope: "backend"}],
        prerequisites: [{name: "real-gate2", status: "missing", reason: $reason}],
        artifacts: [{kind: "docs-report", path: $report, sha256: $sha, redactionStatus: "passed", description: "real Gate 2 prerequisite report"}],
        redactionStatus: "not-run",
        notRunReason: $reason
      }]
    }' >"$manifest"
}

real_gate2() {
  local missing=()
  local retained_gate2_log=""
  if [ -n "$gate2_evidence" ]; then
    . scripts/lib/retained-gate2-evidence.sh
    if ! retained_gate2_log="$(resolve_retained_gate2_log "$gate2_evidence" "$(git_commit)")"; then
      missing+=("valid retained Gate 2 evidence $gate2_evidence")
    fi
  else
    if [ "${HIDEOUT_029_RUN_REAL_GATE2:-}" != "1" ]; then
      missing+=("HIDEOUT_029_RUN_REAL_GATE2=1")
    fi
    command -v limactl >/dev/null 2>&1 || missing+=("limactl")
  fi
  if [ "${#missing[@]}" -gt 0 ]; then
    local reason="missing real Gate 2 prerequisites: ${missing[*]}"
    write_real_not_run "$reason"
    validate_manifest
    echo "hostfs-visibility-e2e: real-gate2 not-run evidence=$manifest"
    if [ "$require_real" -eq 1 ]; then
      exit 1
    fi
    return
  fi

  if [ -n "$retained_gate2_log" ]; then
    cp "$retained_gate2_log" "$out/logs/gate2.out"
    : >"$out/logs/gate2.err"
  else
    scripts/test-gate2-lima.sh >"$out/logs/gate2.out" 2>"$out/logs/gate2.err"
  fi
  grep -q '^gate2: passed$' "$out/logs/gate2.out"
  local i
  for i in $(seq 1 20); do
    grep -q "hostfs_visibility_${i}=passed" "$out/logs/gate2.out"
  done
  local sum
  sum="$(sha256_file "$out/logs/gate2.out")"
  jq -n \
    --arg generated "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
    --arg commit "$(git_commit)" \
    --argjson dirty "$(git_dirty)" \
    --arg sha "$sum" \
    '{
      version: "hideout.product-hardening-evidence/v1",
      generatedAt: $generated,
      commit: $commit,
      dirty: $dirty,
      proofs: [
        {
          proofId: "029.hostfs-visibility.real-gate2.namespace",
          featureId: "029-hostfs-discoverable-namespace",
          mode: "real-gate",
          evidenceClass: "hostfs-visibility-e2e",
          status: "passed",
          commandSummary: "real Lima Gate 2 HostFS discoverable namespace assertions 1-6 and 14-17",
          coveredClaims: [
            "029.SC-001", "029.SC-002", "029.SC-003", "029.SC-004"
          ] | map({claimId: ., source: "spec", description: "Real guest receives bounded exact, one-level, and recursive namespace semantics", scope: "lima"}),
          prerequisites: [{name: "real-gate2", status: "available"}],
          artifacts: [{kind: "log", path: "logs/gate2.out", sha256: $sha, redactionStatus: "passed", description: "real Gate 2 machine assertions"}],
          redactionStatus: "passed"
        },
        {
          proofId: "029.hostfs-visibility.real-gate2.live-grant",
          featureId: "029-hostfs-discoverable-namespace",
          mode: "real-gate",
          evidenceClass: "hostfs-read-decision-e2e",
          status: "passed",
          commandSummary: "real Lima Gate 2 separate-process approval and same-session retry assertions 7-13 and 18-20",
          coveredClaims: [
            "029.SC-005", "029.SC-006", "029.SC-007", "029.SC-008"
          ] | map({claimId: ., source: "spec", description: "Separate control process activates exact read authority for the same live guest", scope: "lima"}),
          prerequisites: [{name: "real-gate2", status: "available"}],
          artifacts: [{kind: "log", path: "logs/gate2.out", sha256: $sha, redactionStatus: "passed", description: "real Gate 2 live-grant assertions"}],
          redactionStatus: "passed"
        }
      ]
    }' >"$manifest"
  validate_manifest
  echo "hostfs-visibility-e2e: real-gate2 passed evidence=$manifest"
}

case "$mode" in
  local-fast)
    local_fast
    ;;
  real-gate2)
    real_gate2
    ;;
  *)
    echo "hostfs-visibility-e2e: unsupported mode: $mode" >&2
    exit 2
    ;;
esac
