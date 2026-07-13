#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

mode="local-fast"
require_real=0
out=""
operation="replace"
package_path=""

usage() {
  cat <<'USAGE'
Usage:
  scripts/test-hostfs-decision-e2e.sh [--local-fast|--real-gate2]
                                      [--require-real]
                                      [--operation <name>]
                                      [--out <dir>]
                                      [--package <path>]
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
    --operation)
      operation="${2:-}"
      shift 2
      ;;
    --out)
      out="${2:-}"
      shift 2
      ;;
    --package)
      package_path="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "hostfs-decision-e2e: unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [ -z "$out" ]; then
  out="$(mktemp -d "${TMPDIR:-/tmp}/hideout-hostfs-decision-e2e.XXXXXX")"
fi
mkdir -p "$out/logs" "$out/reports"
out="$(cd "$out" && pwd -P)"
manifest="$out/product-hardening-evidence.json"

require_tool() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "hostfs-decision-e2e: missing required tool: $1" >&2
    exit 127
  fi
}

require_tool jq

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    echo "hostfs-decision-e2e: missing shasum or sha256sum" >&2
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

artifact_json() {
  local kind="$1"
  local rel="$2"
  local desc="$3"
  local sum=""
  if [ -n "$rel" ] && [ -f "$out/$rel" ]; then
    sum="$(sha256_file "$out/$rel")"
  fi
  jq -n \
    --arg kind "$kind" \
    --arg path "$rel" \
    --arg sha "$sum" \
    --arg desc "$desc" \
    '[{
      kind: $kind,
      path: $path,
      sha256: (if $sha == "" then empty else $sha end),
      redactionStatus: "passed",
      description: $desc
    }]'
}

claim_json() {
  local id="$1"
  local desc="$2"
  local scope="$3"
  jq -n \
    --arg id "$id" \
    --arg desc "$desc" \
    --arg scope "$scope" \
    '[{claimId: $id, source: "spec", description: $desc, scope: $scope}]'
}

write_real_not_run() {
  local reason="$1"
  local report="reports/real-gate2-prerequisites.txt"
  printf '%s\n' "$reason" >"$out/$report"
  local sha
  sha="$(sha256_file "$out/$report")"
  jq -n \
    --arg generated "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
    --arg commit "$(git_commit)" \
    --argjson dirty "$(git_dirty)" \
    --arg reason "$reason" \
    --arg sha "$sha" \
    '{
      version: "hideout.product-hardening-evidence/v1",
      generatedAt: $generated,
      commit: $commit,
      dirty: $dirty,
      proofs: [{
        proofId: "023.hostfs-decision.real-gate2.not-run",
        featureId: "023-hostfs-decision-e2e",
        mode: "real-gate",
        evidenceClass: "hostfs-decision-e2e",
        status: "not-run",
        commandSummary: "real Gate 2 HostFS prerequisites unavailable",
        coveredClaims: [{
          claimId: "023.SC-003",
          source: "spec",
          description: "Missing real Gate 2 prerequisites record not-run evidence",
          scope: "backend"
        }],
        prerequisites: [{
          name: "real-gate2",
          status: "missing",
          reason: $reason
        }],
        artifacts: [{
          kind: "docs-report",
          path: "reports/real-gate2-prerequisites.txt",
          sha256: $sha,
          redactionStatus: "passed",
          description: "real Gate 2 prerequisite report"
        }],
        redactionStatus: "not-run",
        notRunReason: $reason
      }]
    }' >"$manifest"
}

validate_manifest() {
  jq -e '
    .version == "hideout.product-hardening-evidence/v1" and
    (.proofs | length > 0) and
    all(.proofs[];
      (.featureId == "023-hostfs-decision-e2e") and
      (.proofId | startswith("023.")) and
      (.status == "passed" or .status == "failed" or .status == "not-run") and
      (.coveredClaims | length > 0) and
      (.redactionStatus == "passed" or .redactionStatus == "failed" or .redactionStatus == "not-run")
    )
  ' "$manifest" >"$out/logs/evidence-content.out"
  go run ./cmd/hideout-schema-validate \
    schemas/product-hardening-evidence.schema.json \
    "$manifest" >"$out/logs/evidence-schema.out" 2>"$out/logs/evidence-schema.err"
  if grep -R -E 'claim_[0-9a-f]{16,}|hfwobj_|providerRef|HIDEOUT_SECRET_[A-Z0-9_]+=|cap_[A-Za-z0-9]{12,}|socks5://[^[:space:]]+:[^[:space:]]+@' "$out" >/dev/null 2>&1; then
    echo "hostfs-decision-e2e: public evidence contains private/control-plane material" >&2
    grep -R -n -E 'claim_[0-9a-f]{16,}|hfwobj_|providerRef|HIDEOUT_SECRET_[A-Z0-9_]+=|cap_[A-Za-z0-9]{12,}|socks5://[^[:space:]]+:[^[:space:]]+@' "$out" >&2 || true
    exit 1
  fi
}

local_fast() {
  HIDEOUT_HOSTFS_DECISION_E2E_OUT="$out" \
    go test -count=1 ./test/e2e/hostfsdecision \
    >"$out/logs/go-test.out" 2>"$out/logs/go-test.err"
  validate_manifest
  jq -e '
    [.proofs[].proofId] as $ids |
    all([
      "023.hostfs-decision.local-fast.lifecycle",
      "023.hostfs-decision.local-fast.claim-race",
      "023.hostfs-decision.local-fast.timeout",
      "023.hostfs-decision.local-fast.visibility",
      "023.hostfs-decision.local-fast.redaction"
    ][]; . as $id | $ids | index($id))
  ' "$manifest" >"$out/logs/required-proofs.out"
  echo "hostfs-decision-e2e: local-fast passed evidence=$manifest"
}

real_gate2() {
  local missing=()
  if [ "${HIDEOUT_023_RUN_REAL_GATE2:-}" != "1" ]; then
    missing+=("HIDEOUT_023_RUN_REAL_GATE2=1")
  fi
  command -v limactl >/dev/null 2>&1 || missing+=("limactl")
  if [ "${#missing[@]}" -gt 0 ]; then
    local reason="missing real Gate 2 prerequisites: ${missing[*]}"
    write_real_not_run "$reason"
    validate_manifest
    echo "hostfs-decision-e2e: real-gate2 not-run evidence=$manifest"
    if [ "$require_real" -eq 1 ]; then
      exit 1
    fi
    return
  fi
  scripts/test-gate2-lima.sh >"$out/logs/gate2.out" 2>"$out/logs/gate2.err"
  grep -q 'hostfs_write_overlay=applied' "$out/logs/gate2.out"
  grep -q 'hostfs_write_dir_overlay=applied' "$out/logs/gate2.out"
  local log_sha
  log_sha="$(sha256_file "$out/logs/gate2.out")"
  jq -n \
    --arg generated "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
    --arg commit "$(git_commit)" \
    --argjson dirty "$(git_dirty)" \
    --arg log_sha "$log_sha" \
    '{
      version: "hideout.product-hardening-evidence/v1",
      generatedAt: $generated,
      commit: $commit,
      dirty: $dirty,
      proofs: [{
        proofId: "023.hostfs-decision.real-gate2.lifecycle",
        featureId: "023-hostfs-decision-e2e",
        mode: "real-gate",
        evidenceClass: "hostfs-decision-e2e",
        status: "passed",
        commandSummary: "real Gate 2 HostFS write overlay lifecycle",
        coveredClaims: [
          {claimId: "023.FR-002", source: "spec", description: "Target reads staged content before apply", scope: "hostfs"},
          {claimId: "023.FR-003", source: "spec", description: "Host lower remains unchanged before apply", scope: "hostfs"},
          {claimId: "023.FR-004", source: "spec", description: "Operator apply mutates planned host path", scope: "hostfs"}
        ],
        prerequisites: [{name: "real-gate2", status: "available"}],
        artifacts: [{
          kind: "log",
          path: "logs/gate2.out",
          sha256: $log_sha,
          redactionStatus: "passed",
          description: "Gate 2 HostFS write output"
        }],
        redactionStatus: "passed",
        notes: ["representative real Gate 2 coverage: replace operation and mkdir directory operation"]
      }]
    }' >"$manifest"
  validate_manifest
  echo "hostfs-decision-e2e: real-gate2 passed evidence=$manifest"
}

case "$operation" in
  create|replace|append|truncate|mkdir|delete|rename|chmod|chown|all)
    ;;
  *)
    echo "hostfs-decision-e2e: unsupported operation: $operation" >&2
    exit 2
    ;;
esac

case "$mode" in
  local-fast)
    local_fast
    ;;
  real-gate2)
    real_gate2
    ;;
  *)
    echo "hostfs-decision-e2e: unknown mode: $mode" >&2
    exit 2
    ;;
esac
