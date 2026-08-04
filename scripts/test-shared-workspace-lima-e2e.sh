#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/lib/workspace-research.sh
. "$ROOT/scripts/lib/workspace-research.sh"
# shellcheck source=scripts/lib/gate2-shared-workspace.sh
. "$ROOT/scripts/lib/gate2-shared-workspace.sh"
# shellcheck source=scripts/lib/gate2-shared-workspace-path.sh
. "$ROOT/scripts/lib/gate2-shared-workspace-path.sh"
# shellcheck source=scripts/lib/gate2-shared-workspace-performance.sh
. "$ROOT/scripts/lib/gate2-shared-workspace-performance.sh"
# shellcheck source=scripts/lib/gate2-shared-workspace-product-performance.sh
. "$ROOT/scripts/lib/gate2-shared-workspace-product-performance.sh"

mode="real-gate2"
require_real=0
samples=30
out="${HIDEOUT_035_EVIDENCE_DIR:-$ROOT/dist/product-evidence/035}"
package_archive=""
stage="all"
run_behavior=1
run_performance=1

usage() {
  cat <<'USAGE'
Usage: scripts/test-shared-workspace-lima-e2e.sh [options]

  --local-fast                 emit mechanics-only Gate 0 evidence
  --real-gate2                 run the real packaged macOS arm64 Lima gate (default)
  --non-performance            retain real behavior/path/lifecycle evidence and stop before performance
  --performance-only           resume from a retained behavior checkpoint and run only performance
  --require-real               fail instead of emitting supporting not-run evidence
  --samples <n>                performance samples (real performance evidence requires at least 30)
  --package <tar.gz>           reuse an exact package archive instead of building once
  --out <dir>                  evidence output directory
USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --local-fast) mode="local-fast"; shift ;;
    --real-gate2) mode="real-gate2"; shift ;;
    --non-performance) stage="behavior"; shift ;;
    --performance-only) stage="performance"; shift ;;
    --require-real) require_real=1; shift ;;
    --samples) samples="${2:-}"; shift 2 ;;
    --package) package_archive="${2:-}"; shift 2 ;;
    --out) out="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "shared-workspace e2e: unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

case "$stage" in
  all) run_behavior=1; run_performance=1 ;;
  behavior) run_behavior=1; run_performance=0 ;;
  performance) run_behavior=0; run_performance=1 ;;
esac
if [ "$mode" = "local-fast" ] && [ "$stage" = "performance" ]; then
  echo "shared-workspace e2e: --performance-only requires --real-gate2" >&2
  exit 2
fi

case "$samples" in
  ''|*[!0-9]*) echo "shared-workspace e2e: --samples must be an integer" >&2; exit 2 ;;
esac
if [ "$mode" = "real-gate2" ] && [ "$run_performance" = "1" ] && [ "$samples" -lt 30 ]; then
  echo "shared-workspace e2e: real evidence requires at least 30 samples" >&2
  exit 2
fi
if [ -n "$package_archive" ] && [ ! -f "$package_archive" ]; then
  echo "shared-workspace e2e: package archive does not exist" >&2
  exit 2
fi
if [ "$run_behavior" = "0" ] && [ ! -d "$out" ]; then
  echo "shared-workspace e2e: --performance-only requires an existing --out behavior checkpoint" >&2
  exit 2
fi

mkdir -p "$out/logs" "$out/reports" "$out/artifacts"
out="$(cd "$out" && pwd -P)"
manifest="$out/product-hardening-evidence.json"
registry="$out/reports/proof-registry.json"
go run ./cmd/hideout support proof-registry --json >"$registry"

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    sha256sum "$1" | awk '{print $1}'
  fi
}

git_dirty() {
  if [ -n "$(git status --porcelain=v1 --untracked-files=all)" ]; then
    printf true
  else
    printf false
  fi
}

claims_json() {
  local proof_id="$1"
  jq -c --arg id "$proof_id" '
    [.requirements[] | select(.proofId == $id) | .claimIds[] |
      {claimId:.,source:"spec",description:("035 registered contract " + .),scope:"shared-workspace"}]
  ' "$registry"
}

artifact_json() {
	  local relative="$1" kind="$2" description="$3"
	  jq -n --arg path "$relative" --arg sha "$(sha256_file "$out/$relative")" \
	    --arg kind "$kind" --arg description "$description" \
	    '{kind:$kind,path:$path,sha256:$sha,redactionStatus:"passed",description:$description}'
}

digest_json() {
	  local relative="$1"
	  jq -n --arg path "$relative" --arg sha "$(sha256_file "$out/$relative")" \
	    '{path:$path,sha256:$sha}'
}

proof_json() {
  local proof_id="$1" status="$2" proof_mode="$3" class="$4" summary="$5"
  local artifact="$6" reason="$7" runtime="${8:-null}" claims
  claims="$(claims_json "$proof_id")"
  [ "$(jq 'length' <<<"$claims")" -gt 0 ] || {
    echo "shared-workspace e2e: proof is not registered: $proof_id" >&2
    return 1
  }
  jq -n --arg proofId "$proof_id" --arg status "$status" --arg mode "$proof_mode" \
    --arg class "$class" --arg summary "$summary" --arg reason "$reason" \
    --argjson claims "$claims" --argjson artifact "$artifact" --argjson runtime "$runtime" '
    {proofId:$proofId,featureId:"035-shared-default-vm-cross-workspace",mode:$mode,
     evidenceClass:$class,status:$status,commandSummary:$summary,coveredClaims:$claims,
     prerequisites:(if $status == "not-run" then
       [{name:"real-macos-arm64-lima-packaged",status:"missing",reason:$reason}]
       else [{name:"real-macos-arm64-lima-packaged",status:"available"}] end),
	     artifacts:(if $artifact == null then []
	       elif ($artifact | type) == "array" then $artifact else [$artifact] end),
     redactionStatus:(if $status == "not-run" then "not-run" else "passed" end)}
     + (if $status == "not-run" then {notRunReason:$reason} else {} end)
     + (if $runtime == null then {} else {runtime:$runtime} end)'
}

write_manifest() {
  local proofs="$1" package_identity="${2:-null}"
  jq -n --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
    --arg commit "$(git rev-parse HEAD)" --argjson dirty "$(git_dirty)" \
    --argjson packageIdentity "$package_identity" --slurpfile proofs "$proofs" '
    {version:"hideout.product-hardening-evidence/v1",generatedAt:$generatedAt,
     commit:$commit,dirty:$dirty,proofs:$proofs[0]}
     + (if $packageIdentity == null then {} else {packageIdentity:$packageIdentity} end)' \
    >"$manifest"
  go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json \
    "$manifest" >"$out/logs/evidence-schema.out" 2>"$out/logs/evidence-schema.err"
}

scan_public_evidence() {
	  local forbidden='claim_[0-9a-f]{16,}|cap_[A-Za-z0-9]{12,}|HIDEOUT_SECRET_[A-Z0-9_]+=|socks5://[^[:space:]]+:[^[:space:]]+@|machineId|providerRef|canonicalHostRoot|credentialHostPath'
	  local relative
	  while IFS= read -r relative; do
	    if grep -E "$forbidden" "$out/$relative" >/dev/null 2>&1; then
	      echo "shared-workspace e2e: public evidence contains control-plane material: $relative" >&2
	      return 1
	    fi
	  done < <(jq -r '.proofs[].artifacts[].path' "$manifest" | LC_ALL=C sort -u)
	  if grep -E "$forbidden" "$manifest" >/dev/null 2>&1; then
	    echo "shared-workspace e2e: public evidence manifest contains control-plane material" >&2
	    return 1
	  fi
}

verify_checkpoint_artifacts() {
	local checkpoint="$1" relative expected actual
	jq -e '
	  [.proofs[] | select(.proofId == "035.shared-workspace.real-gate2.behavior")] |
	  length == 1 and .[0].status == "passed" and
	  (.[0].artifacts | length) > 0 and
	  all(.[0].artifacts[];
	    (.path | type) == "string" and (.path | length) > 0 and
	    (.sha256 | test("^[0-9a-f]{64}$")))
	' "$checkpoint" >/dev/null || {
	  echo "shared-workspace e2e: behavior checkpoint artifact inventory is invalid" >&2
	  return 1
	}
	while IFS=$'\t' read -r relative expected; do
	  case "$relative" in
	    ""|/*|../*|*/../*|*/..) echo "shared-workspace e2e: checkpoint artifact path escapes output: $relative" >&2; return 1 ;;
	  esac
	  [ -f "$out/$relative" ] || {
	    echo "shared-workspace e2e: checkpoint artifact is missing: $relative" >&2
	    return 1
	  }
	  actual="$(sha256_file "$out/$relative")"
	  [ "$actual" = "$expected" ] || {
	    echo "shared-workspace e2e: checkpoint artifact digest changed: $relative" >&2
	    return 1
	  }
	done < <(jq -r '
	  .proofs[] | select(.proofId == "035.shared-workspace.real-gate2.behavior") |
	  .artifacts[] | [.path,.sha256] | @tsv
	' "$checkpoint")
}

prepare_performance_attempt() {
	local archive
	local relative moved=0
	archive="$out/reports/failed-performance-$(date -u +'%Y%m%dT%H%M%SZ')-$$"
	for relative in performance-candidate performance-evaluation artifacts/performance performance.json; do
	  if [ -e "$out/$relative" ]; then
	    [ "$moved" -eq 1 ] || { mkdir -p "$archive"; moved=1; }
	    mkdir -p "$archive/$(dirname "$relative")"
	    mv "$out/$relative" "$archive/$relative"
	  fi
	done
}

proofs="$out/reports/proofs.json"
resume_checkpoint=""
resume_behavior_proof=""
if [ "$run_behavior" = "0" ]; then
  [ -f "$manifest" ] || {
    echo "shared-workspace e2e: --performance-only requires product-hardening-evidence.json in --out" >&2
    exit 2
  }
  resume_checkpoint="$out/reports/resumed-behavior-checkpoint.json"
  resume_behavior_proof="$out/reports/resumed-behavior-proof.json"
  cp "$manifest" "$resume_checkpoint"
  go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json \
    "$resume_checkpoint" >"$out/logs/resume-schema.out" 2>"$out/logs/resume-schema.err"
  verify_checkpoint_artifacts "$resume_checkpoint"
  jq -e '
    [.proofs[] | select(
      .proofId == "035.shared-workspace.real-gate2.behavior" and
      .status == "passed" and .mode == "real-gate")] | length == 1
  ' "$resume_checkpoint" >/dev/null
  jq -e '
    all(.proofs[]; .proofId != "035.shared-workspace.real-gate2.performance")
  ' "$resume_checkpoint" >/dev/null
  jq '.proofs[] | select(.proofId == "035.shared-workspace.real-gate2.behavior")' \
    "$resume_checkpoint" >"$resume_behavior_proof"
fi
if [ "$mode" = "local-fast" ]; then
  scripts/test-shared-workspace-smoke.sh \
    >"$out/logs/local-fast.out" 2>"$out/logs/local-fast.err"
	  local_artifact="$(artifact_json logs/local-fast.out log '035 deterministic local contracts')"
  jq -s '.' \
    <(proof_json '035.shared-workspace.gate0.mechanics' passed local-fast \
      shared-workspace-gate0 \
      'validated local shared-slot, attachment, lifecycle, surface, schema, redaction, and race contracts' \
      "$local_artifact" 'local contracts only; no real Lima claim') >"$proofs"
  write_manifest "$proofs"
  rm "$proofs"
  echo "shared-workspace e2e: passed mode=local-fast evidence=$manifest"
  exit 0
fi

missing=""
for tool in go git jq limactl python3 shasum tar awk sed; do
  command -v "$tool" >/dev/null 2>&1 || missing="$missing $tool"
done
[ "$(uname -s)" = Darwin ] || missing="$missing macOS"
[ "$(uname -m)" = arm64 ] || missing="$missing arm64"
[ -r "$ROOT/dist/workspace-research/035/decision.json" ] || missing="$missing accepted-035-research-decision"
if [ "$run_performance" = "1" ]; then
  [ -r "$ROOT/dist/workspace-research/035/baseline-static-virtiofs/fixture.sha256" ] || missing="$missing static-virtiofs-baseline"
fi

if [ -n "$missing" ]; then
  reason="real Gate 2 prerequisites unavailable:$missing"
  gate2_shared_workspace_not_run "$out/logs"
  if [ "$require_real" = "1" ]; then
    echo "shared-workspace e2e: $reason" >&2
    exit 1
  fi
	  notrun_artifact="$(artifact_json logs/relations.json manifest '035 real Gate 2 not-run reason')"
  jq -s '.' \
    <(proof_json '035.shared-workspace.real-gate2.not-run' not-run real-gate \
      shared-workspace-real-gate2-not-run 'real shared-workspace Gate 2 was not run' \
      "$notrun_artifact" "$reason") >"$proofs"
  write_manifest "$proofs"
  rm "$proofs"
  echo "shared-workspace e2e: passed mode=real-gate2 status=not-run evidence=$manifest"
  exit 0
fi

short_root="${HIDEOUT_035_SHORT_TMPDIR:-/tmp}"
work="$(mktemp -d "$short_root/hideout-035-gate2.XXXXXX")"
store="$work/store"
lima_home="$work/lima"
fixture_root="$work/fixtures"
install_root="$work/installed"
candidate_archive="$package_archive"
candidate=""

cleanup() {
  if [ -n "$candidate" ] && [ -x "$candidate" ]; then
    HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
      "$candidate" daemon stop >/dev/null 2>&1 || true
  fi
  if [ -d "$lima_home" ]; then
    LIMA_HOME="$lima_home" limactl list --quiet 2>/dev/null | while IFS= read -r instance; do
      [ -n "$instance" ] && LIMA_HOME="$lima_home" limactl delete --force "$instance" >/dev/null 2>&1 || true
    done
  fi
  find "$work" -depth -delete 2>/dev/null || true
}
trap cleanup EXIT

mkdir -p "$store" "$lima_home" "$fixture_root" "$install_root"
chmod 700 "$store" "$lima_home"
if [ -z "$candidate_archive" ]; then
  candidate_archive="$out/artifacts/hideout-035-candidate.tar.gz"
  if [ "$run_behavior" = "0" ]; then
    [ -f "$candidate_archive" ] || {
      echo "shared-workspace e2e: retained candidate archive is missing; pass the exact archive with --package" >&2
      exit 2
    }
  else
    scripts/package-local.sh --out "$candidate_archive" \
      >"$out/logs/package.out" 2>"$out/logs/package.err"
  fi
fi
candidate_archive="$(cd "$(dirname "$candidate_archive")" && pwd -P)/$(basename "$candidate_archive")"
tar -xzf "$candidate_archive" -C "$install_root"
prefix="$install_root/hideout"
candidate="$prefix/bin/hideout"
[ -x "$candidate" ]
"$candidate" package verify "$prefix" \
  >"$out/logs/package-verify.out" 2>"$out/logs/package-verify.err"

arch="$(go env GOARCH)"
portal_helper="$prefix/bin/hideout-workspace-portal-linux-$arch"
portal_manifest="$portal_helper.manifest.json"
[ -x "$portal_helper" ] && [ -f "$portal_manifest" ]
jq -e --arg sha "$(sha256_file "$portal_helper")" '
  .version == "hideout.helper-manifest/v1" and
  .command == "hideout-workspace-portal" and
  .targetOS == "linux" and .sha256 == $sha
' "$portal_manifest" >/dev/null
	jq -e --arg helper "bin/hideout-workspace-portal-linux-$arch" '
  any(.files[]; .path == $helper and .kind == "linux-helper")
' "$prefix/package-manifest.json" >/dev/null

archive_sha="$(sha256_file "$candidate_archive")"
package_identity="$(jq -c --arg archiveSHA "$archive_sha" '{
  name:"hideout",productVersion:.release.productVersion,
  sourceCommit:.source.commit,artifactSHA256:$archiveSHA,
  hostOS:.target.hostOS,hostArch:.target.hostArch
}' "$prefix/package-manifest.json")"
if [ "$run_behavior" = "0" ]; then
  jq -e --arg commit "$(git rev-parse HEAD)" \
    --argjson packageIdentity "$package_identity" '
    .commit == $commit and .packageIdentity == $packageIdentity
  ' "$resume_checkpoint" >/dev/null || {
    echo "shared-workspace e2e: retained behavior checkpoint does not match this commit and package" >&2
    exit 1
  }
fi

HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$candidate" init \
  --template dev --profile gate035 --backend lima --network direct \
  --runtime developer-standard --no-input \
  >"$out/logs/init.out" 2>"$out/logs/init.err"

performance_complete=0
if [ "$run_behavior" = "1" ]; then
  gate2_shared_workspace_relations "$out/relations" "$store" "$lima_home" \
    "$candidate" gate035 "$fixture_root/relations"
  gate2_shared_workspace_path_correctness "$out/path-correctness" "$store" "$lima_home" \
    "$candidate" gate035 "$fixture_root/path-correctness"
  gate2_shared_workspace_lifecycle "$out/lifecycle" "$store" "$lima_home" \
    "$candidate" gate035 "$fixture_root/lifecycle"
else
  gate2_035_path_correctness_judge "$out/artifacts/behavior/path-correctness.json"
  if ! gate2_035_path_negative_fixture_judge "$out/artifacts/behavior/path-negative-fixture.json"; then
    echo "shared-workspace e2e: retained negative path fixture is not the exact divergent-inode case" >&2
    exit 1
  fi
  prepare_performance_attempt
  gate2_shared_workspace_measure_product "$ROOT" "$out/performance-candidate" \
    "$store" "$lima_home" "$candidate" gate035 "$fixture_root/performance-gate" \
    "$samples" "$ROOT/dist/workspace-research/035/baseline-static-virtiofs" \
    "$out/artifacts/behavior/path-correctness.json"
  gate2_shared_workspace_evaluate \
    "$ROOT/dist/workspace-research/035/baseline-static-virtiofs" \
    "$out/performance-candidate/filesystem-control" \
    "$out/performance-candidate" "$out/performance-evaluation"
  performance_complete=1
fi

record="$(find "$store/environments" -name environment.json -type f -print -quit)"
[ -f "$record" ]
catalog="$(find "$prefix" -path '*/runtime/catalog.json' -type f -print -quit)"
[ -f "$catalog" ]
build_commit="$(jq -r '.families[] | select(.id == "developer-standard") |
  .revisions[] | select(.id == "2026.07.0") | .artifacts[] |
  select(.hostOS == "darwin" and .hostArch == "arm64") | .source.buildCommit' "$catalog")"
runtime="$(jq -c --arg buildCommit "$build_commit" '{
  schema:"hideout.runtime-evidence-binding/v1",family:.runtime.family,
  revision:.runtime.revision,artifactSHA256:.runtime.artifactSHA256,
  environmentId:.id,hostOS:.runtime.hostOS,hostArch:.runtime.hostArch,
  guestArch:.runtime.guestArch,buildCommit:$buildCommit,buildDirty:false
}' "$record")"

if [ "$run_behavior" = "1" ]; then
  jq -e '.status == "passed" and .environmentCount == 1 and .instanceCount == 1' \
    "$out/relations/relations.json" >/dev/null
  jq -e '.status == "passed" and all(.checks[]; . == true)' \
    "$out/lifecycle/lifecycle.json" >/dev/null
  gate2_035_path_correctness_judge "$out/path-correctness/path-correctness.json"

	mkdir -p "$out/artifacts/behavior"
	cp "$out/relations/relations.json" "$out/artifacts/behavior/relations.json"
	cp "$out/lifecycle/lifecycle.json" "$out/artifacts/behavior/lifecycle.json"
	cp "$out/path-correctness/path-correctness.json" "$out/artifacts/behavior/path-correctness.json"
	cp "$out/path-correctness/negative-divergent-inode.json" "$out/artifacts/behavior/path-negative-fixture.json"
	cp "$ROOT/dist/workspace-research/035/decision.json" "$out/artifacts/behavior/research-decision.json"
	cp "$prefix/package-manifest.json" "$out/artifacts/behavior/package-manifest.json"
	cp "$portal_manifest" "$out/artifacts/behavior/workspace-portal-helper.manifest.json"

	behavior_digests="$(jq -n \
	  --argjson relations "$(digest_json artifacts/behavior/relations.json)" \
	  --argjson lifecycle "$(digest_json artifacts/behavior/lifecycle.json)" \
	  --argjson pathCorrectness "$(digest_json artifacts/behavior/path-correctness.json)" \
	  --argjson pathNegativeFixture "$(digest_json artifacts/behavior/path-negative-fixture.json)" \
	  --argjson researchDecision "$(digest_json artifacts/behavior/research-decision.json)" \
	  --argjson packageManifest "$(digest_json artifacts/behavior/package-manifest.json)" \
	  --argjson helperManifest "$(digest_json artifacts/behavior/workspace-portal-helper.manifest.json)" \
	  '{relations:$relations,lifecycle:$lifecycle,
	    pathCorrectness:$pathCorrectness,pathNegativeFixture:$pathNegativeFixture,
	    researchDecision:$researchDecision,packageManifest:$packageManifest,
	    workspacePortalHelperManifest:$helperManifest}')"
	jq -n --arg commit "$(git rev-parse HEAD)" --argjson dirty "$(git_dirty)" \
	  --argjson packageIdentity "$package_identity" --argjson runtime "$runtime" \
	  --argjson artifacts "$behavior_digests" '{
	    schema:"hideout.shared-workspace-real-gate2/v2",status:"passed",
	    commit:$commit,dirty:$dirty,backend:"lima",hostOS:"darwin",hostArch:"arm64",
	    transport:"workspace-portal",packageIdentity:$packageIdentity,runtime:$runtime,
	    artifacts:$artifacts,
	    checks:{oneMachineTwoProjects:true,disjointIsolation:true,sameRootLocks:true,
	      nestedAuthority:true,siblingDetach:true,lifecycleIntegration:true,
	      restartNoReadoption:true,packagedHelperVerified:true,hostPathRedacted:true,
	      logicalPhysicalAlias:true,projectStateSeparated:true,siblingPhysicalRootDenied:true,
	      pathJudgeNegativeFixture:true}
	  }' >"$out/behavior.json"
	behavior_artifacts="$(jq -s '.' \
	  <(artifact_json behavior.json manifest '035 packaged real Lima behavior result') \
	  <(artifact_json artifacts/behavior/relations.json manifest 'one-machine relation summary') \
	  <(artifact_json artifacts/behavior/lifecycle.json manifest 'workspace lifecycle summary') \
	  <(artifact_json artifacts/behavior/path-correctness.json manifest 'workspace path identity summary') \
	  <(artifact_json artifacts/behavior/path-negative-fixture.json manifest 'workspace path judge negative fixture') \
	  <(artifact_json artifacts/behavior/research-decision.json manifest 'accepted transport research decision') \
	  <(artifact_json artifacts/behavior/package-manifest.json manifest 'verified candidate package manifest') \
	  <(artifact_json artifacts/behavior/workspace-portal-helper.manifest.json manifest 'packaged workspace portal helper manifest'))"
	jq -s '.' \
	  <(proof_json '035.shared-workspace.real-gate2.behavior' passed real-gate \
	    shared-workspace-real-gate2 \
	    'validated packaged workspace aliases, project-state separation, isolation, lifecycle, and redaction' \
	    "$behavior_artifacts" 'real packaged macOS arm64 Lima non-performance evidence' "$runtime") \
	  >"$proofs"
	write_manifest "$proofs" "$package_identity"
	scan_public_evidence
	else
	  behavior_artifacts="$(jq -c '.artifacts' "$resume_behavior_proof")"
	fi
	if [ "$run_performance" = "0" ]; then
	  rm "$proofs"
	  trap - EXIT
	  cleanup
	  echo "shared-workspace e2e: passed mode=real-gate2-non-performance evidence=$manifest"
	  exit 0
	fi

	# The behavior manifest above is a durable checkpoint. A performance or
	# harness failure below cannot erase independently valid path/lifecycle proof.
	if [ "$performance_complete" = "0" ]; then
	  prepare_performance_attempt
	  gate2_shared_workspace_measure_product "$ROOT" "$out/performance-candidate" \
	    "$store" "$lima_home" "$candidate" gate035 "$fixture_root/performance-gate" \
	    "$samples" "$ROOT/dist/workspace-research/035/baseline-static-virtiofs" \
	    "$out/path-correctness/path-correctness.json"
	  gate2_shared_workspace_evaluate \
	    "$ROOT/dist/workspace-research/035/baseline-static-virtiofs" \
	    "$out/performance-candidate/filesystem-control" \
	    "$out/performance-candidate" "$out/performance-evaluation"
	fi
	jq -e '.result == "passed" and .thresholdsPassed == true' \
	  "$out/performance-evaluation/shared-workspace-evaluation.json" >/dev/null

	mkdir -p "$out/artifacts/performance/candidate" \
	  "$out/artifacts/performance/filesystem-control" \
	  "$out/artifacts/performance/research-baseline"
	for metric in git-status package-scan atomic-host-to-guest atomic-guest-to-host \
	  mount-ready first-byte saturation-metadata; do
	  cp "$out/performance-candidate/raw/$metric.values" \
	    "$out/artifacts/performance/candidate/$metric.values"
	done
		for metric in git-status package-scan; do
		  cp "$out/performance-candidate/filesystem-control/raw/$metric.values" \
		    "$out/artifacts/performance/filesystem-control/$metric.values"
		done
		cp "$out/performance-candidate/raw/filesystem-paired.tsv" \
		  "$out/artifacts/performance/filesystem-control/paired-samples.tsv"
		cp "$ROOT/dist/workspace-research/035/baseline-static-virtiofs/raw/first-byte.values" \
		  "$out/artifacts/performance/research-baseline/first-byte.values"
		cp "$ROOT/dist/workspace-research/035/baseline-static-virtiofs/fixture.sha256" \
		  "$out/artifacts/performance/research-baseline/fixture.sha256"
	cp "$out/performance-candidate/path-correctness.json" "$out/artifacts/performance/path-correctness.json"
	cp "$out/performance-candidate/saturation.json" "$out/artifacts/performance/saturation.json"
	cp "$ROOT/dist/workspace-research/035/decision.json" "$out/artifacts/performance/research-decision.json"

	performance_candidate_digests="$(jq -n \
	  --argjson git "$(digest_json artifacts/performance/candidate/git-status.values)" \
	  --argjson package "$(digest_json artifacts/performance/candidate/package-scan.values)" \
	  --argjson h2g "$(digest_json artifacts/performance/candidate/atomic-host-to-guest.values)" \
	  --argjson g2h "$(digest_json artifacts/performance/candidate/atomic-guest-to-host.values)" \
	  --argjson mount "$(digest_json artifacts/performance/candidate/mount-ready.values)" \
	  --argjson first "$(digest_json artifacts/performance/candidate/first-byte.values)" \
	  --argjson saturation "$(digest_json artifacts/performance/candidate/saturation-metadata.values)" \
	  '{"git-status":$git,"package-scan":$package,"atomic-host-to-guest":$h2g,
	    "atomic-guest-to-host":$g2h,"mount-ready":$mount,"first-byte":$first,
	    "saturation-metadata":$saturation}')"
		performance_control_digests="$(jq -n \
		  --argjson git "$(digest_json artifacts/performance/filesystem-control/git-status.values)" \
		  --argjson package "$(digest_json artifacts/performance/filesystem-control/package-scan.values)" \
		  '{"git-status":$git,"package-scan":$package}')"
		performance_research_digests="$(jq -n \
		  --argjson first "$(digest_json artifacts/performance/research-baseline/first-byte.values)" \
		  '{"first-byte":$first}')"
		paired_samples_digest="$(digest_json artifacts/performance/filesystem-control/paired-samples.tsv)"
		jq -n --arg commit "$(git rev-parse HEAD)" --argjson dirty "$(git_dirty)" \
		  --arg fixture "$(tr -d '\n' <"$out/artifacts/performance/research-baseline/fixture.sha256")" \
		  --argjson samples "$samples" --argjson artifacts "$performance_control_digests" \
		  --argjson pairedSamples "$paired_samples_digest" '{
		    schema:"hideout.shared-workspace-paired-control/v1",commit:$commit,dirty:$dirty,
		    mechanism:"profile-cache-static-virtiofs",
		    guestRoot:"/hideout/profile/cache/035-static-virtiofs-control",
		    fixtureSHA256:$fixture,samples:$samples,warmups:1,sampleOrder:"alternating-pairs",
		    artifacts:$artifacts,pairedSamples:$pairedSamples
		  }' >"$out/artifacts/performance/filesystem-control/manifest.json"
		performance_digests="$(jq -n \
		  --argjson candidate "$performance_candidate_digests" \
		  --argjson filesystemControl "$performance_control_digests" \
		  --argjson researchBaseline "$performance_research_digests" \
		  --argjson fixture "$(digest_json artifacts/performance/research-baseline/fixture.sha256)" \
		  --argjson controlManifest "$(digest_json artifacts/performance/filesystem-control/manifest.json)" \
		  --argjson pairedSamples "$paired_samples_digest" \
		  --argjson pathCorrectness "$(digest_json artifacts/performance/path-correctness.json)" \
	  --argjson saturation "$(digest_json artifacts/performance/saturation.json)" \
	  --argjson researchDecision "$(digest_json artifacts/performance/research-decision.json)" \
		  '{candidate:$candidate,filesystemControl:$filesystemControl,
		    researchBaseline:$researchBaseline,fixture:$fixture,
		    filesystemControlManifest:$controlManifest,pairedSamples:$pairedSamples,
		    pathCorrectness:$pathCorrectness,saturation:$saturation,researchDecision:$researchDecision}')"
		research_commit="$(jq -r '.commit' "$ROOT/dist/workspace-research/035/baseline-static-virtiofs/baseline.json")"
		research_dirty="$(jq -r '.dirty' "$ROOT/dist/workspace-research/035/baseline-static-virtiofs/baseline.json")"
		jq --arg commit "$(git rev-parse HEAD)" --argjson dirty "$(git_dirty)" \
		  --arg researchCommit "$research_commit" --argjson researchDirty "$research_dirty" \
	  --argjson packageIdentity "$package_identity" --argjson runtime "$runtime" \
	  --argjson samples "$samples" --argjson artifacts "$performance_digests" '
		  . + {
		    candidate:{commit:$commit,dirty:$dirty},
		    researchBaseline:{commit:$researchCommit,dirty:$researchDirty},
		    filesystemControl:{commit:$commit,dirty:$dirty,
		      mechanism:"profile-cache-static-virtiofs",
		      guestRoot:"/hideout/profile/cache/035-static-virtiofs-control",
		      sampleOrder:"alternating-pairs"},
		    packageIdentity:$packageIdentity,
		    runtime:$runtime,
		    methodology:{samples:$samples,warmups:1,filesystemSampleOrder:"alternating-pairs",
		      firstByteSampleOrder:"one-warmup-then-measured",
	      gitMedianAbsoluteMs:2000,gitMedianBaselineRatio:2,
	      packageMedianBaselineRatio:3,atomicVisibilityP95Ms:250,mountReadyP95Ms:1000,
	      firstByteAbsoluteAllowanceMs:500,firstByteBaselineAllowance:0.15,
	      saturationTeardownMs:5000},
	    artifacts:$artifacts
	  }' "$out/performance-evaluation/shared-workspace-evaluation.json" >"$out/performance.json"
	performance_artifacts="$(jq -s '.' \
	  <(artifact_json performance.json manifest '035 fixed-fixture real performance result') \
	  <(artifact_json artifacts/performance/candidate/git-status.values log 'candidate git-status raw samples') \
	  <(artifact_json artifacts/performance/candidate/package-scan.values log 'candidate package-scan raw samples') \
	  <(artifact_json artifacts/performance/candidate/atomic-host-to-guest.values log 'candidate host-to-guest raw samples') \
	  <(artifact_json artifacts/performance/candidate/atomic-guest-to-host.values log 'candidate guest-to-host raw samples') \
	  <(artifact_json artifacts/performance/candidate/mount-ready.values log 'candidate mounted-ready raw samples') \
	  <(artifact_json artifacts/performance/candidate/first-byte.values log 'candidate first-byte raw samples') \
	  <(artifact_json artifacts/performance/candidate/saturation-metadata.values log 'candidate saturation raw samples') \
		  <(artifact_json artifacts/performance/filesystem-control/git-status.values log 'paired static-virtiofs git-status raw samples') \
		  <(artifact_json artifacts/performance/filesystem-control/package-scan.values log 'paired static-virtiofs package-scan raw samples') \
		  <(artifact_json artifacts/performance/filesystem-control/paired-samples.tsv log 'alternating candidate and control observations') \
		  <(artifact_json artifacts/performance/filesystem-control/manifest.json manifest 'paired static-virtiofs control identity') \
		  <(artifact_json artifacts/performance/research-baseline/first-byte.values log 'retained research first-byte samples') \
		  <(artifact_json artifacts/performance/research-baseline/fixture.sha256 manifest 'fixed research fixture digest') \
	  <(artifact_json artifacts/performance/path-correctness.json manifest 'independent candidate path-correctness observations') \
	  <(artifact_json artifacts/performance/saturation.json manifest 'candidate saturation teardown observation') \
	  <(artifact_json artifacts/performance/research-decision.json manifest 'accepted transport research decision'))"
	if [ "$run_behavior" = "1" ]; then
	  jq -s '.' \
	    <(proof_json '035.shared-workspace.real-gate2.behavior' passed real-gate \
	      shared-workspace-real-gate2 \
	      'validated one packaged shared machine, immutable project views, isolation, lifecycle, and redaction' \
	      "$behavior_artifacts" 'real packaged macOS arm64 Lima evidence' "$runtime") \
	    <(proof_json '035.shared-workspace.real-gate2.performance' passed real-gate \
	      shared-workspace-performance-real-gate2 \
	      'validated paired Git/package, atomic, attach, first-byte, and independent path-correctness distributions' \
	      "$performance_artifacts" 'same-VM static-virtiofs control plus accepted research identity' "$runtime") \
	    >"$proofs"
	else
	  jq -s '.' \
	    "$resume_behavior_proof" \
	    <(proof_json '035.shared-workspace.real-gate2.performance' passed real-gate \
	      shared-workspace-performance-real-gate2 \
	      'validated paired Git/package, atomic, attach, first-byte, and independent path-correctness distributions' \
	      "$performance_artifacts" 'same-VM static-virtiofs control plus accepted research identity' "$runtime") \
	    >"$proofs"
	fi
write_manifest "$proofs" "$package_identity"
scan_public_evidence
rm "$proofs"
trap - EXIT
cleanup
echo "shared-workspace e2e: passed mode=real-gate2 evidence=$manifest"
