#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

mode="local-fast"
out=""
package_path=""

usage() {
  cat <<'USAGE'
Usage:
  scripts/test-doctor-package-recovery-e2e.sh [--local-fast]
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
      echo "doctor-package-recovery-e2e: unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [ "$mode" != "local-fast" ]; then
  echo "doctor-package-recovery-e2e: unsupported mode: $mode" >&2
  exit 2
fi

if [ -z "$out" ]; then
  out="$(mktemp -d "${TMPDIR:-/tmp}/hideout-doctor-package-recovery-e2e.XXXXXX")"
fi
mkdir -p "$out/logs" "$out/reports/package" "$out/reports/doctor" "$out/reports/recovery"
out="$(cd "$out" && pwd -P)"
manifest="$out/product-hardening-evidence.json"

require_tool() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "doctor-package-recovery-e2e: missing required tool: $1" >&2
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
    echo "doctor-package-recovery-e2e: missing shasum or sha256sum" >&2
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

artifact_obj() {
  local kind="$1"
  local rel="$2"
  local desc="$3"
  local sha=""
  if [ -f "$out/$rel" ]; then
    sha="$(sha256_file "$out/$rel")"
  fi
  jq -n \
    --arg kind "$kind" \
    --arg path "$rel" \
    --arg sha "$sha" \
    --arg desc "$desc" \
    '{
      kind: $kind,
      path: $path,
      sha256: (if $sha == "" then empty else $sha end),
      redactionStatus: "passed",
      description: $desc
    }'
}

artifact_array() {
  jq -s '.' "$@"
}

claim_obj() {
  local id="$1"
  local desc="$2"
  local scope="$3"
  jq -n \
    --arg id "$id" \
    --arg desc "$desc" \
    --arg scope "$scope" \
    '{claimId: $id, source: "spec", description: $desc, scope: $scope}'
}

validate_artifacts() {
  jq -e '
    .obsoleteFile == "share/hideout/README.zh-CN.md" and
    .verifyBefore == "failed" and
    .dryRunRemoved == false and
    .applyRemovedObsolete == true and
    .verifyAfter == "passed" and
    .durableStatePreserved == true and
    .unrelatedFilePreserved == true
  ' "$out/reports/package/package-repair-summary.json" >/dev/null

  grep -q 'package repair --prefix' "$out/reports/package/package-stale-verify.err"
  grep -q 'package: repair dry-run' "$out/reports/package/package-repair-dry.out"
  grep -q 'removed share/hideout/README.zh-CN.md' "$out/reports/package/package-repair.out"
  grep -q 'package: ok mode=installed' "$out/reports/package/package-repaired-verify.out"

  jq -e '
    .level == "deep" and
    ([.findings[] | select(.checkId | startswith("feature-"))] | length) >= 10
  ' "$out/reports/doctor/doctor-deep.json" >/dev/null
  jq -e '
    ([.findings[] | select(.checkId == "feature-packaging" and (.nextActions | tostring | contains("hideout package repair")))] | length) == 1
  ' "$out/reports/doctor/doctor-packaging.json" >/dev/null
  jq -e '
    ([.findings[] | select(.checkId == "feature-dns" and (.details.gateRequired | length >= 1))] | length) == 1
  ' "$out/reports/doctor/doctor-dns.json" >/dev/null
  grep -q 'Hideout doctor fix plan' "$out/reports/doctor/fix-dry.out"
  grep -q 'task profile.create: applied risk=safe' "$out/reports/doctor/fix-apply.out"
  jq -e '
    .summary.failed == false and
    ([.findings[] | select(.checkId == "profile" and .status == "pass")] | length) == 1
  ' "$out/reports/doctor/fix-apply-doctor.json" >/dev/null
  go run ./cmd/hideout-schema-validate schemas/doctor-report.schema.json "$out/reports/doctor/doctor-evidence.json" >/dev/null
  go run ./cmd/hideout-schema-validate schemas/export-artifact.schema.json "$out/reports/doctor/doctor-export.json" >/dev/null
  jq -e '.provenance.source == "doctor-report" and .body.schema == "hideout.doctor-report/v1"' "$out/reports/doctor/doctor-export.json" >/dev/null
}

write_recovery_summaries() {
  jq -n \
    --slurpfile package "$out/reports/package/package-repair-summary.json" \
    --slurpfile doctor "$out/reports/doctor/fix-apply-doctor.json" \
    '{
      packageRepair: $package[0],
      doctorFix: {
        dryRunMutated: false,
        applyCreatedProfile: true,
        applyCreatedInstallState: true,
        rerunFailed: $doctor[0].summary.failed
      },
      releaseReadiness: "not-claimed"
    }' >"$out/reports/recovery/recovery-summary.json"
  jq -n \
    --slurpfile dns "$out/reports/doctor/doctor-dns.json" \
    --slurpfile packaging "$out/reports/doctor/doctor-packaging.json" \
    '{
      guidanceOnly: true,
      fixed: false,
      checks: [
        {
          checkId: "feature-dns",
          gateRequired: ($dns[0].findings[] | select(.checkId == "feature-dns") | .details.gateRequired),
          nextActions: ($dns[0].findings[] | select(.checkId == "feature-dns") | .nextActions)
        },
        {
          checkId: "feature-packaging",
          nextActions: ($packaging[0].findings[] | select(.checkId == "feature-packaging") | .nextActions)
        }
      ]
    }' >"$out/reports/recovery/guidance-summary.json"
}

scan_redaction() {
  if grep -R -E 'HIDEOUT_SECRET_[A-Z0-9_]+=|cap_[A-Za-z0-9]{12,}|ui_[A-Za-z0-9]{12,}|providerRef|claim_[0-9a-f]{16,}|socks5://[^[:space:]]+:[^[:space:]]+@|machineId' "$out" >/dev/null 2>&1; then
    echo "doctor-package-recovery-e2e: public evidence contains control-plane material" >&2
    grep -R -n -E 'HIDEOUT_SECRET_[A-Z0-9_]+=|cap_[A-Za-z0-9]{12,}|ui_[A-Za-z0-9]{12,}|providerRef|claim_[0-9a-f]{16,}|socks5://[^[:space:]]+:[^[:space:]]+@|machineId' "$out" >&2 || true
    exit 1
  fi
  jq -n \
    --arg result "passed" \
    '{
      result: $result,
      scanned: ["logs", "reports/package", "reports/doctor", "reports/recovery"],
      forbiddenCategories: ["control-plane secrets", "claim tokens", "provider refs", "raw proxy URLs", "machine ids"]
    }' >"$out/reports/recovery/redaction-summary.json"
}

write_manifest() {
  local package_artifacts doctor_artifacts guidance_artifacts redaction_artifacts
  package_artifacts="$(mktemp)"
	doctor_artifacts="$(mktemp)"
	guidance_artifacts="$(mktemp)"
	redaction_artifacts="$(mktemp)"

	artifact_array \
    <(artifact_obj "event-summary" "reports/package/package-repair-summary.json" "package stale verify/dry-run/apply/verify summary") \
    <(artifact_obj "log" "reports/package/package-stale-verify.err" "package verify stale failure") \
    <(artifact_obj "log" "reports/package/package-repair-dry.out" "package repair dry-run output") \
    <(artifact_obj "log" "reports/package/package-repair.out" "package repair apply output") \
    <(artifact_obj "log" "reports/package/package-repaired-verify.out" "package verify after repair") >"$package_artifacts"
  artifact_array \
    <(artifact_obj "event-summary" "reports/recovery/recovery-summary.json" "doctor safe repair summary") \
    <(artifact_obj "log" "reports/doctor/fix-dry.out" "doctor fix dry-run output") \
    <(artifact_obj "log" "reports/doctor/fix-apply.out" "doctor fix apply output") \
    <(artifact_obj "schema" "reports/doctor/fix-apply-doctor.json" "doctor rerun JSON report") >"$doctor_artifacts"
  artifact_array \
    <(artifact_obj "event-summary" "reports/recovery/guidance-summary.json" "guidance-only doctor summary") \
    <(artifact_obj "schema" "reports/doctor/doctor-deep.json" "doctor deep JSON report") \
    <(artifact_obj "schema" "reports/doctor/doctor-dns.json" "doctor DNS gate-required report") \
    <(artifact_obj "schema" "reports/doctor/doctor-packaging.json" "doctor packaging report") >"$guidance_artifacts"
  artifact_array \
    <(artifact_obj "event-summary" "reports/recovery/redaction-summary.json" "recovery artifact redaction scan") \
    <(artifact_obj "schema" "reports/doctor/doctor-evidence.json" "selected doctor report evidence") \
    <(artifact_obj "schema" "reports/doctor/doctor-export.json" "exported doctor report") >"$redaction_artifacts"

  jq -n \
    --arg generated "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
    --arg commit "$(git_commit)" \
    --argjson dirty "$(git_dirty)" \
    --slurpfile packageArtifacts "$package_artifacts" \
    --slurpfile doctorArtifacts "$doctor_artifacts" \
    --slurpfile guidanceArtifacts "$guidance_artifacts" \
    --slurpfile redactionArtifacts "$redaction_artifacts" \
    --arg packageSource "${package_path:-built-local}" \
    '{
      version: "hideout.product-hardening-evidence/v1",
      generatedAt: $generated,
      commit: $commit,
      dirty: $dirty,
      proofs: [
        {
          proofId: "024.recovery.package.repair-loop",
          featureId: "024-doctor-package-recovery-e2e",
          mode: "local-fast",
          evidenceClass: "doctor-package-recovery-e2e",
          status: "passed",
          commandSummary: "package repair loop via existing package smoke",
          coveredClaims: [
            {claimId: "024.FR-002", source: "spec", description: "Package verify detects obsolete package-owned leftovers", scope: "package"},
            {claimId: "024.FR-005", source: "spec", description: "Package repair preserves durable state and unrelated files", scope: "package"},
            {claimId: "024.FR-012", source: "spec", description: "Local recovery evidence is not release readiness", scope: "boundary"}
          ],
          prerequisites: [{name: "package-smoke", status: "available", reason: $packageSource}],
          artifacts: $packageArtifacts[0],
          redactionStatus: "passed"
        },
        {
          proofId: "024.recovery.doctor.safe-fix-loop",
          featureId: "024-doctor-package-recovery-e2e",
          mode: "local-fast",
          evidenceClass: "doctor-package-recovery-e2e",
          status: "passed",
          commandSummary: "doctor safe fix loop via existing doctor smoke",
          coveredClaims: [
            {claimId: "024.FR-008", source: "spec", description: "Doctor fix apply performs only typed safe repairs", scope: "doctor"},
            {claimId: "024.FR-012", source: "spec", description: "Local recovery evidence is not release readiness", scope: "boundary"}
          ],
          prerequisites: [{name: "doctor-smoke", status: "available"}],
          artifacts: $doctorArtifacts[0],
          redactionStatus: "passed"
        },
        {
          proofId: "024.recovery.doctor.guidance-only",
          featureId: "024-doctor-package-recovery-e2e",
          mode: "local-fast",
          evidenceClass: "doctor-package-recovery-e2e",
          status: "passed",
          commandSummary: "doctor guidance-only findings remain unfixed and gate-required",
          coveredClaims: [
            {claimId: "024.FR-006", source: "spec", description: "Doctor deep emits facts, next actions, and gate-required markers", scope: "doctor"},
            {claimId: "024.FR-012", source: "spec", description: "Local recovery evidence is not release readiness", scope: "boundary"}
          ],
          prerequisites: [{name: "doctor-feature-diagnostics", status: "available"}],
          artifacts: $guidanceArtifacts[0],
          redactionStatus: "passed",
          notes: ["guidance findings are not counted as fixed"]
        },
        {
          proofId: "024.recovery.redaction",
          featureId: "024-doctor-package-recovery-e2e",
          mode: "local-fast",
          evidenceClass: "doctor-package-recovery-e2e",
          status: "passed",
          commandSummary: "recovery logs, doctor report export, and product evidence redaction scan",
          coveredClaims: [
            {claimId: "024.FR-010", source: "spec", description: "Selected doctor report export validates through export schema", scope: "export"},
            {claimId: "024.FR-011", source: "spec", description: "Public recovery artifacts omit control-plane material", scope: "redaction"}
          ],
          prerequisites: [{name: "recovery-redaction-scan", status: "available"}],
          artifacts: $redactionArtifacts[0],
          redactionStatus: "passed"
        }
	      ]
	    }' >"$manifest"
	rm -f "$package_artifacts" "$doctor_artifacts" "$guidance_artifacts" "$redaction_artifacts"
}

validate_manifest() {
  jq -e '
    .version == "hideout.product-hardening-evidence/v1" and
    ([.proofs[].proofId] | sort) == ([
      "024.recovery.doctor.guidance-only",
      "024.recovery.doctor.safe-fix-loop",
      "024.recovery.package.repair-loop",
      "024.recovery.redaction"
    ] | sort) and
    all(.proofs[]; .featureId == "024-doctor-package-recovery-e2e" and .status == "passed" and .redactionStatus == "passed")
  ' "$manifest" >"$out/logs/evidence-content.out"
  go run ./cmd/hideout-schema-validate \
    schemas/product-hardening-evidence.schema.json \
    "$manifest" >"$out/logs/evidence-schema.out" 2>"$out/logs/evidence-schema.err"
  scan_redaction
}

local_fast() {
  HIDEOUT_PACKAGE_SMOKE_ARTIFACT_DIR="$out/reports/package" \
    scripts/test-package-smoke.sh >"$out/logs/package-smoke.out" 2>"$out/logs/package-smoke.err"
  HIDEOUT_DOCTOR_SMOKE_ARTIFACT_DIR="$out/reports/doctor" \
    scripts/test-doctor-smoke.sh >"$out/logs/doctor-smoke.out" 2>"$out/logs/doctor-smoke.err"
  validate_artifacts
  write_recovery_summaries
  scan_redaction
  write_manifest
  validate_manifest
  echo "doctor-package-recovery-e2e: local-fast passed evidence=$manifest"
}

local_fast
