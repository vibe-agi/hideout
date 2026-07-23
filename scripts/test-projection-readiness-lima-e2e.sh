#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

out="$ROOT/.hideout-release-evidence/043-projection-readiness-real-gate2"
package_archive=""
fresh=10
warm=30
probe=0
require_real=0

usage() {
  cat <<'USAGE'
Usage: scripts/test-projection-readiness-lima-e2e.sh [options]

  --out <dir>          evidence output directory
  --package <tar.gz>   reuse one exact candidate package
  --fresh <n>          genuinely fresh environment samples (product minimum 10)
  --warm <n>           new-session samples on one warm environment (product minimum 30)
  --probe              permit reduced counts and dirty source; emit no product proof
  --require-real       fail instead of emitting supporting not-run evidence
USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --out) out="${2:-}"; shift 2 ;;
    --package) package_archive="${2:-}"; shift 2 ;;
    --fresh) fresh="${2:-}"; shift 2 ;;
    --warm) warm="${2:-}"; shift 2 ;;
    --probe) probe=1; shift ;;
    --require-real) require_real=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "projection readiness e2e: unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

for value_name in fresh warm; do
  eval "value=\${$value_name}"
  case "$value" in
    ''|*[!0-9]*) echo "projection readiness e2e: --$value_name must be an integer" >&2; exit 2 ;;
  esac
done
[ "$fresh" -ge 1 ] && [ "$warm" -ge 1 ] || {
  echo "projection readiness e2e: sample counts must be positive" >&2
  exit 2
}

source_commit="$(git rev-parse HEAD)"
source_dirty=false
if [ -n "$(git status --porcelain=v1 --untracked-files=all)" ]; then
  source_dirty=true
fi
if [ "$probe" = "0" ]; then
  [ "$fresh" -ge 10 ] || { echo "projection readiness e2e: product evidence requires 10 fresh samples" >&2; exit 2; }
  [ "$warm" -ge 30 ] || { echo "projection readiness e2e: product evidence requires 30 warm samples" >&2; exit 2; }
  [ "$source_dirty" = false ] || {
    echo "projection readiness e2e: product evidence requires a clean source tree; use --probe for diagnostics" >&2
    exit 2
  }
fi
if [ -n "$package_archive" ] && [ ! -f "$package_archive" ]; then
  echo "projection readiness e2e: package archive does not exist" >&2
  exit 2
fi
if [ -e "$out" ]; then
  echo "projection readiness e2e: output directory already exists: $out" >&2
  exit 2
fi

missing=""
for tool in go git jq limactl python3 shasum tar awk sed comm; do
  command -v "$tool" >/dev/null 2>&1 || missing="$missing $tool"
done
[ "$(uname -s)" = Darwin ] || missing="$missing macOS"
[ "$(uname -m)" = arm64 ] || missing="$missing arm64"
for app in "/Applications/Visual Studio Code.app" "$HOME/Applications/Visual Studio Code.app"; do
  if [ -d "$app" ]; then
    vscode_available=1
    break
  fi
done
[ "${vscode_available:-0}" = 1 ] || missing="$missing Visual-Studio-Code"

mkdir -p "$out/artifacts" "$out/logs" "$out/reports"
out="$(cd "$out" && pwd -P)"
manifest="$out/product-hardening-evidence.json"
registry="$out/reports/proof-registry.json"
go run ./cmd/hideout support proof-registry --json >"$registry"

sha256_file() { shasum -a 256 "$1" | awk '{print $1}'; }
claims_json() {
  jq -c --arg id "$1" '
    [.requirements[] | select(.proofId == $id) | .claimIds[] |
      {claimId:.,source:"spec",description:("registered projection contract " + .),
       scope:"projection-readiness"}]
  ' "$registry"
}
artifact_ref() {
  local path="$1" kind="$2" description="$3"
  jq -n --arg path "$path" --arg kind "$kind" \
    --arg sha "$(sha256_file "$out/$path")" --arg description "$description" \
    '{kind:$kind,path:$path,sha256:$sha,redactionStatus:"passed",description:$description}'
}
write_not_run() {
  local reason="$1"
  local proof_id="043.projection-readiness.real-gate2.not-run"
  local requirement claims artifact
  requirement="$(jq -cer --arg id "$proof_id" '.requirements[] | select(.proofId == $id)' "$registry")"
  claims="$(claims_json "$proof_id")"
  jq -n --arg reason "$reason" \
    '{schema:"hideout.projection-readiness-not-run/v1",status:"not-run",reason:$reason}' \
    >"$out/artifacts/not-run.json"
  artifact="$(artifact_ref artifacts/not-run.json manifest "043 real Gate 2 not-run reason")"
  jq -n --arg proofId "$proof_id" \
    --arg featureId "$(jq -r '.featureId' <<<"$requirement")" \
    --arg mode "$(jq -r '.requiredMode // "real-gate"' <<<"$requirement")" \
    --arg class "$(jq -r '.requiredEvidenceClass // "projection-readiness-real-gate2-not-run"' <<<"$requirement")" \
    --arg reason "$reason" --argjson claims "$claims" --argjson artifact "$artifact" '[{
      proofId:$proofId,featureId:$featureId,mode:$mode,evidenceClass:$class,
      status:"not-run",commandSummary:"real projection readiness Gate 2 was not run",
      coveredClaims:$claims,
      prerequisites:[{name:"real-macos-arm64-lima-packaged",status:"missing",reason:$reason}],
      artifacts:[$artifact],redactionStatus:"not-run",notRunReason:$reason
    }]' >"$out/reports/proofs.json"
  jq -n --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
    --arg commit "$source_commit" --argjson dirty "$source_dirty" \
    --slurpfile proofs "$out/reports/proofs.json" '{
      version:"hideout.product-hardening-evidence/v1",generatedAt:$generatedAt,
      commit:$commit,dirty:$dirty,proofs:$proofs[0]
    }' >"$manifest"
  go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json "$manifest" >/dev/null
}

if [ -n "$missing" ]; then
  reason="real Gate 2 prerequisites unavailable:$missing"
  if [ "$require_real" = "1" ] || [ "$probe" = "1" ]; then
    echo "projection readiness e2e: $reason" >&2
    exit 1
  fi
  write_not_run "$reason"
  echo "projection readiness e2e: passed status=not-run evidence=$manifest"
  exit 0
fi

work="$(mktemp -d "${TMPDIR:-/tmp}/hideout-043-gate2.XXXXXX")"
install_root="$work/installed"
raw_gate2="$work/gate2.raw"
raw_gate2_err="$work/gate2.err"
capture="$work/capture"
mkdir -p "$install_root" "$capture"
cleanup() {
  find "$work" -depth -delete 2>/dev/null || true
}
trap cleanup EXIT

if [ -z "$package_archive" ]; then
  package_archive="$out/artifacts/hideout-043-candidate.tar.gz"
  scripts/package-local.sh --out "$package_archive" \
    >"$out/logs/package.out" 2>"$out/logs/package.err"
fi
package_archive="$(cd "$(dirname "$package_archive")" && pwd -P)/$(basename "$package_archive")"
tar -xzf "$package_archive" -C "$install_root"
prefix="$install_root/hideout"
candidate="$prefix/bin/hideout"
[ -x "$candidate" ]
"$candidate" package verify "$prefix" \
  >"$out/logs/package-verify.out" 2>"$out/logs/package-verify.err"

archive_sha="$(sha256_file "$package_archive")"
package_identity="$(jq -c --arg archiveSHA "$archive_sha" '{
  name:"hideout",productVersion:.release.productVersion,
  sourceCommit:.source.commit,artifactSHA256:$archiveSHA,
  hostOS:.target.hostOS,hostArch:.target.hostArch
}' "$prefix/package-manifest.json")"
jq -e --arg commit "$source_commit" '
  .name == "hideout" and .sourceCommit == $commit and
  .hostOS == "darwin" and .hostArch == "arm64"
' <<<"$package_identity" >/dev/null

runtime_family="$(jq -r '.runtime.family' "$prefix/package-manifest.json")"
runtime_revision="$(jq -r '.runtime.revision' "$prefix/package-manifest.json")"
runtime_sha="$(jq -r '.runtime.artifactSHA256' "$prefix/package-manifest.json")"
runtime_catalog="$(find "$prefix" -path '*/runtime/catalog.json' -type f -print -quit)"
[ -f "$runtime_catalog" ]
runtime_build_commit="$(jq -er \
  --arg family "$runtime_family" --arg revision "$runtime_revision" --arg sha "$runtime_sha" '
  .families[] | select(.id == $family) |
  .revisions[] | select(.id == $revision) |
  .artifacts[] | select(.hostOS == "darwin" and .hostArch == "arm64" and
    .guestArch == "aarch64" and .sha256 == $sha) | .source.buildCommit
' "$runtime_catalog")"

arch=arm64
helper_bin="$prefix/bin"
for helper in \
  "hideout-shim-linux-$arch" \
  "hideout-hostfsd-linux-$arch" \
  "hideout-session-supervisor-linux-$arch" \
  "hideout-workspace-portal-linux-$arch" \
  "hideout-dns-stub-linux-$arch"
do
  [ -x "$helper_bin/$helper" ] || {
    echo "projection readiness e2e: verified package lacks $helper" >&2
    exit 1
  }
done

echo "projection readiness e2e: running exact-package aggregate Gate 2"
set +e
HIDEOUT_RELEASE_BINARY="$candidate" \
  HIDEOUT_LINUX_SHIM_PATH="$helper_bin/hideout-shim-linux-$arch" \
  HIDEOUT_LINUX_HOSTFSD_PATH="$helper_bin/hideout-hostfsd-linux-$arch" \
  HIDEOUT_LINUX_SESSION_SUPERVISOR_PATH="$helper_bin/hideout-session-supervisor-linux-$arch" \
  HIDEOUT_LINUX_WORKSPACE_PORTAL_PATH="$helper_bin/hideout-workspace-portal-linux-$arch" \
  HIDEOUT_LINUX_DNS_STUB_PATH="$helper_bin/hideout-dns-stub-linux-$arch" \
  HIDEOUT_GATE2_RUNTIME_MODE=1 \
  HIDEOUT_GATE2_REQUIRE_PROJECTION=1 \
  HIDEOUT_GATE2_EXTERNAL_HOST_APP_PACK="$ROOT/test/host-app-packs/gate2-external" \
  HIDEOUT_PROJECTION_READINESS_CAPTURE_DIR="$capture" \
  HIDEOUT_PROJECTION_READINESS_FRESH="$fresh" \
  HIDEOUT_PROJECTION_READINESS_WARM="$warm" \
  HIDEOUT_PROJECTION_RUNTIME_FAMILY="$runtime_family" \
  HIDEOUT_PROJECTION_RUNTIME_BUILD_COMMIT="$runtime_build_commit" \
  scripts/test-gate2-lima.sh >"$raw_gate2" 2>"$raw_gate2_err"
gate2_status=$?
set -e
if [ "$gate2_status" -ne 0 ]; then
  echo "projection readiness e2e: aggregate Gate 2 failed; showing bounded tails" >&2
  tail -n 200 "$raw_gate2" >&2
  tail -n 200 "$raw_gate2_err" >&2
  exit 1
fi

required_markers=(
  projection_code_open
  projection_workspace_resource
  projection_privacy_three_channel
  projection_trusted_grant
  projection_persistent_grant
  host_app_external_old_session_immutable
  host_app_external_workspace
  host_app_external_hostfs
  host_app_external_unsafe_identity_denied
  host_app_external_disable_no_fallback
  host_app_external_revoke_no_fallback
  host_app_external_gate2
  projection_concurrent_disjoint_catalogs
  projection_readiness_samples
)
for marker in "${required_markers[@]}"; do
  grep -q "^${marker}=passed$" "$raw_gate2" || {
    echo "projection readiness e2e: aggregate Gate 2 omitted $marker" >&2
    tail -n 200 "$raw_gate2" >&2
    tail -n 200 "$raw_gate2_err" >&2
    exit 1
  }
done
grep -q '^gate2: passed$' "$raw_gate2"
{
  printf 'real_backend=macos-arm64-lima\n'
  printf 'application_identity=bundle-id+designated-requirement\n'
  for marker in "${required_markers[@]}"; do
    printf '%s=passed\n' "$marker"
  done
  printf 'gate2=passed\n'
} >"$out/logs/gate2-summary.out"

if [ "$probe" = "1" ]; then
  cp "$capture/readiness-samples.tsv" "$out/artifacts/readiness-samples.tsv"
  cp "$capture/runtime-binding.json" "$out/reports/runtime-binding.json"
  echo "projection readiness e2e: probe passed; no product proof emitted"
  exit 0
fi

echo "projection readiness e2e: running closed local refusal/mechanics prerequisites"
go test -count=1 ./cmd/hideout-session-supervisor ./internal/backend/lima ./internal/manager \
  -run 'Projection|SessionWireReportsExactProjectionReadiness|GeneratedHostAppShim' \
  >"$out/logs/mechanics.out" 2>"$out/logs/mechanics.err"

cp "$capture/readiness-samples.tsv" "$out/artifacts/readiness-samples.tsv"
runtime="$(jq -cer \
  --arg family "$runtime_family" --arg revision "$runtime_revision" \
  --arg sha "$runtime_sha" --arg buildCommit "$runtime_build_commit" '
  select(.schema == "hideout.runtime-evidence-binding/v1" and
    .family == $family and .revision == $revision and .artifactSHA256 == $sha and
    .hostOS == "darwin" and .hostArch == "arm64" and .guestArch == "aarch64" and
    .buildCommit == $buildCommit and .buildDirty == false)
' "$capture/runtime-binding.json")"

jq -n --argjson packageIdentity "$package_identity" '{
  schema:"hideout.projection-package-manifest/v1",
  packageIdentity:$packageIdentity
}' >"$out/artifacts/package-manifest.json"
jq -n --argjson runtime "$runtime" '{
  schema:"hideout.projection-runtime-manifest/v1",
  runtime:$runtime
}' >"$out/artifacts/runtime-manifest.json"

jq -n '{
  schema:"hideout.projection-flows-real-gate2/v1",status:"passed",
  checks:{
    "projection030.safeHostEffect":true,
    "projection030.taskSuppression":true,
    "projection030.aliasChannels":true,
    "projection030.preservePositiveControl":true,
    "projection030.runBoundGrant":true,
    "projection030.runBoundRevoke":true,
    "external032.oldSessionImmutable":true,
    "external032.workspaceResource":true,
    "external032.authorizedHostFS":true,
    "external032.unsafeIdentityDenied":true,
    "external032.disableNoFallback":true,
    "external032.revokeNoFallback":true,
    "persistent039.initialRefusal":true,
    "persistent039.hostGrant":true,
    "persistent039.separateRunReuse":true,
    "persistent039.revoke":true,
    "persistent039.laterRefusal":true
  }
}' >"$out/artifacts/projection-flows.json"

metrics="$(python3 - "$out/artifacts/readiness-samples.tsv" <<'PY'
import csv
import json
import math
import sys

rows = list(csv.DictReader(open(sys.argv[1], encoding="utf-8"), delimiter="\t"))
lanes = {name: [] for name in ("fresh", "warm", "cancellation")}
for row in rows:
    lanes[row["lane"]].append((int(row["index"]), int(row["duration_ms"])))
for name, values in lanes.items():
    values.sort()
    if [index for index, _ in values] != list(range(1, len(values) + 1)):
        raise SystemExit(f"{name} indices are not contiguous")
def p95(values):
    ordered = sorted(duration for _, duration in values)
    return ordered[math.ceil(0.95 * len(ordered)) - 1]
print(json.dumps({
    "freshSamples": len(lanes["fresh"]),
    "warmSamples": len(lanes["warm"]),
    "freshP95Ms": p95(lanes["fresh"]),
    "warmP95Ms": p95(lanes["warm"]),
    "cancellationMaxMs": max(duration for _, duration in lanes["cancellation"]),
}, separators=(",", ":")))
PY
)"

digest_json() {
  jq -n --arg path "$1" --arg sha "$(sha256_file "$out/$1")" '{path:$path,sha256:$sha}'
}
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$source_commit" \
  --argjson packageIdentity "$package_identity" \
  --argjson runtime "$runtime" \
  --argjson metrics "$metrics" \
  --argjson samples "$(digest_json artifacts/readiness-samples.tsv)" \
  --argjson flows "$(digest_json artifacts/projection-flows.json)" \
  --argjson packageManifest "$(digest_json artifacts/package-manifest.json)" \
  --argjson runtimeManifest "$(digest_json artifacts/runtime-manifest.json)" '{
    schema:"hideout.projection-readiness-real-gate2/v1",status:"passed",
    generatedAt:$generatedAt,commit:$commit,dirty:false,
    packageIdentity:$packageIdentity,runtime:$runtime,
    platform:{hostOS:"darwin",hostArch:"arm64",guestArch:"aarch64",
      backend:"lima",applicationIdentityClass:"bundle-id+designated-requirement"},
    methodology:{minimumFreshSamples:10,minimumWarmSamples:30,
      minimumConcurrentPairs:1,p95Method:"nearest-rank",
      readinessThresholdMs:2000,cancellationThresholdMs:2000},
    readiness:($metrics + {concurrentPairs:1,operatorRetries:0,targetRetries:0,
      fallbacks:0,timeouts:0,unauthorizedHostEffects:0,crossSessionAccess:0}),
    checks:{
      "readiness.catalog":true,"readiness.manifest":true,"readiness.dispatcher":true,
      "readiness.entryProperties":true,"readiness.exactSessionView":true,
      "readiness.readyCommitProof":true,"refusal.staleCatalog":true,
      "refusal.identityDrift":true,"refusal.bootDrift":true,"refusal.timeout":true,
      "refusal.cancellation":true,"refusal.symlink":true,"refusal.type":true,
      "refusal.digest":true,"refusal.zeroTarget":true,"refusal.zeroEffect":true,
      "refusal.zeroFallback":true,"concurrency.disjointCatalogs":true,
      "concurrency.ordinaryCommandCompatibility":true,
      "redaction.applicationIdentityClass":true,"redaction.publicArtifactScan":true
    },
    artifacts:{samples:$samples,flows:$flows,packageManifest:$packageManifest,
      runtimeManifest:$runtimeManifest},
    privacy:{status:"not-promoted"},
    nonClaims:["guest-root-out-of-scope","native-is-not-real-evidence",
      "readiness-is-not-authority"]
  }' >"$out/artifacts/projection-readiness.json"

artifacts="$(jq -s '.' \
  <(artifact_ref artifacts/projection-readiness.json manifest "043 strict projection readiness result") \
  <(artifact_ref artifacts/readiness-samples.tsv log "fresh, warm, and cancellation raw samples") \
  <(artifact_ref artifacts/projection-flows.json manifest "030, 032, and 039 exact flow inventory") \
  <(artifact_ref artifacts/package-manifest.json manifest "verified exact package identity") \
  <(artifact_ref artifacts/runtime-manifest.json manifest "observed exact runtime binding"))"

proof_ids=(
  030.projection.real-gate2.code-open
  030.projection.real-gate2.trusted-grant
  032.host-app-pack.real-gate2.external
  039.trusted-host-app-grant.real-gate2.persistent
  043.projection-readiness.real-gate2.readiness
)
: >"$out/reports/proofs.ndjson"
for proof_id in "${proof_ids[@]}"; do
  requirement="$(jq -cer --arg id "$proof_id" '.requirements[] | select(.proofId == $id)' "$registry")"
  claims="$(claims_json "$proof_id")"
  jq -cn --arg proofId "$proof_id" \
    --arg featureId "$(jq -r '.featureId' <<<"$requirement")" \
    --arg mode "$(jq -r '.requiredMode' <<<"$requirement")" \
    --arg class "$(jq -r '.requiredEvidenceClass' <<<"$requirement")" \
    --argjson claims "$claims" --argjson artifacts "$artifacts" \
    --argjson runtime "$runtime" '{
      proofId:$proofId,featureId:$featureId,mode:$mode,evidenceClass:$class,
      status:"passed",
      commandSummary:"clean exact-package projection readiness and authority flow passed",
      coveredClaims:$claims,
      prerequisites:[
        {name:"real-macos-arm64-lima-packaged",status:"available"},
        {name:"core-verified-host-application",status:"available"}
      ],
      artifacts:$artifacts,redactionStatus:"passed",runtime:$runtime
    }' >>"$out/reports/proofs.ndjson"
done
jq -s '.' "$out/reports/proofs.ndjson" >"$out/reports/proofs.json"
rm -f "$out/reports/proofs.ndjson"

jq -n --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$source_commit" --argjson packageIdentity "$package_identity" \
  --slurpfile proofs "$out/reports/proofs.json" '{
    version:"hideout.product-hardening-evidence/v1",generatedAt:$generatedAt,
    commit:$commit,dirty:false,packageIdentity:$packageIdentity,proofs:$proofs[0]
  }' >"$manifest"
go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json "$manifest" \
  >"$out/logs/evidence-schema.out" 2>"$out/logs/evidence-schema.err"

HIDEOUT_043_EVIDENCE_DIR="$out" go test -count=1 ./internal/productevidence \
  -run '^TestRetainedProjectionReadinessEvidencePassesProductionEvaluator$' \
  >"$out/logs/production-evaluator.out" 2>"$out/logs/production-evaluator.err"

if grep -R -E \
  'claim_[0-9a-f]{16,}|cap_[A-Za-z0-9]{12,}|HIDEOUT_SECRET_[A-Z0-9_]+=|socks5://[^[:space:]]+:[^[:space:]]+@|providerRef|/Users/|/private/tmp/|/tmp/hideout-043-gate2' \
  "$out/artifacts/projection-readiness.json" \
  "$out/artifacts/projection-flows.json" \
  "$out/artifacts/readiness-samples.tsv" \
  "$out/artifacts/package-manifest.json" \
  "$out/artifacts/runtime-manifest.json" \
  "$manifest" >/dev/null 2>&1; then
  echo "projection readiness e2e: public evidence contains control-plane material" >&2
  exit 1
fi

trap - EXIT
cleanup
echo "projection readiness e2e: passed evidence=$manifest"
