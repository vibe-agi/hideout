#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

gate2_evidence=""
gate3_evidence=""
gate3_result=""
out=""

usage() {
  cat <<'USAGE'
Usage: scripts/promote-projection-privacy.sh \
  --gate2-evidence <product-hardening-evidence.json> \
  --gate3-evidence <product-hardening-evidence.json> \
  --gate3-result <gate3-hidden-proxy.json> \
  --out <dir>

Promotes the 030/043 alias-privacy proofs only when a clean exact-package
projection Gate 2 manifest and an independently executed Gate 3 manifest bind
to the same source, package, and runtime artifact/build.
USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --gate2-evidence) gate2_evidence="${2:-}"; shift 2 ;;
    --gate3-evidence) gate3_evidence="${2:-}"; shift 2 ;;
    --gate3-result) gate3_result="${2:-}"; shift 2 ;;
    --out) out="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "projection privacy: unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

[ -f "$gate2_evidence" ] && [ -f "$gate3_evidence" ] && [ -f "$gate3_result" ] &&
  [ -n "$out" ] || {
  usage >&2
  exit 2
}
for command in go jq shasum find cp grep awk mv; do
  command -v "$command" >/dev/null 2>&1 || {
    echo "projection privacy: missing required command: $command" >&2
    exit 127
  }
done
[ ! -e "$out" ] || {
  echo "projection privacy: output already exists: $out" >&2
  exit 2
}

gate2_evidence="$(cd "$(dirname "$gate2_evidence")" && pwd -P)/$(basename "$gate2_evidence")"
gate3_evidence="$(cd "$(dirname "$gate3_evidence")" && pwd -P)/$(basename "$gate3_evidence")"
gate3_result="$(cd "$(dirname "$gate3_result")" && pwd -P)/$(basename "$gate3_result")"
gate2_root="$(dirname "$gate2_evidence")"
gate3_root="$(dirname "$gate3_evidence")"

[ "$(basename "$gate2_evidence")" = product-hardening-evidence.json ] || {
  echo "projection privacy: Gate 2 input must be a canonical evidence manifest" >&2
  exit 2
}
if find "$gate2_root" -type l -print -quit | grep -q .; then
  echo "projection privacy: Gate 2 evidence contains a symlink" >&2
  exit 1
fi
go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json \
  "$gate2_evidence" >/dev/null
go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json \
  "$gate3_evidence" >/dev/null
jq -e '
  .result == "passed" and .backend == "lima" and
  .id == "gate3-hidden-proxy"
' "$gate3_result" >/dev/null || {
  echo "projection privacy: Gate 3 result is not a passed Lima gate" >&2
  exit 1
}

source_commit="$(jq -er '.commit' "$gate2_evidence")"
jq -e --arg commit "$source_commit" --slurpfile gate2 "$gate2_evidence" '
  .commit == $commit and .dirty == false and
  .packageIdentity == $gate2[0].packageIdentity and
  any(.proofs[];
    .proofId == "031.runtime.agent-install" and .status == "passed" and
    .mode == "real-gate" and .redactionStatus == "passed") and
  any(.proofs[];
    .proofId == "031.runtime.agent-privacy" and .status == "passed" and
    .mode == "real-gate" and .redactionStatus == "passed")
' "$gate3_evidence" >/dev/null || {
  echo "projection privacy: Gate 3 evidence does not match the clean candidate/package" >&2
  exit 1
}

parent_runtime="$(jq -cer '
  [.proofs[] |
    select(.proofId == "043.projection-readiness.real-gate2.readiness" and
      .status == "passed" and .mode == "real-gate" and
      .redactionStatus == "passed")][0].runtime
' "$gate2_evidence")"
gate3_runtime="$(jq -cer '
  [.proofs[] |
    select(.proofId == "031.runtime.agent-privacy" and
      .status == "passed")][0].runtime
' "$gate3_evidence")"
jq -e -n --argjson gate2 "$parent_runtime" --argjson gate3 "$gate3_runtime" '
  ($gate2 | del(.environmentId)) == ($gate3 | del(.environmentId))
' >/dev/null || {
  echo "projection privacy: Gate 2 and Gate 3 runtime artifact/build identities differ" >&2
  exit 1
}

gate3_log_rel="$(jq -er '
  [.proofs[] | select(.proofId == "031.runtime.agent-privacy")][0].artifacts[] |
  select(.path == "logs/runtime-gate3.out") | .path
' "$gate3_evidence")"
case "$gate3_log_rel" in
  /*|../*|*/../*|*/..) echo "projection privacy: unsafe Gate 3 artifact path" >&2; exit 1 ;;
esac
gate3_log="$gate3_root/$gate3_log_rel"
[ -f "$gate3_log" ] && [ ! -L "$gate3_log" ] || {
  echo "projection privacy: retained Gate 3 public log is missing or symlinked" >&2
  exit 1
}
expected_gate3_log_sha="$(jq -er --arg path "$gate3_log_rel" '
  [.proofs[] | select(.proofId == "031.runtime.agent-privacy")][0].artifacts[] |
  select(.path == $path) | .sha256
' "$gate3_evidence")"
actual_gate3_log_sha="$(shasum -a 256 "$gate3_log" | awk '{print $1}')"
[ "$actual_gate3_log_sha" = "$expected_gate3_log_sha" ] || {
  echo "projection privacy: retained Gate 3 public log digest mismatch" >&2
  exit 1
}
for marker in \
  guest_workspace=/workspace \
  proxy_env_absent=yes \
  dns_mediated=yes \
  connected_subnet_blocked=yes \
  dns_forward=ok \
  https_request=ok \
  privilege_status=enforced \
  privileged_setup=network \
  projection_alias_gate3=passed \
  gateway_forward_path=passed; do
  grep -F "$marker" "$gate3_log" >/dev/null || {
    echo "projection privacy: Gate 3 log is missing marker: $marker" >&2
    exit 1
  }
done
if grep -E \
  'HIDEOUT_SECRET_[A-Z0-9_]+[=:]|(cap|ui|claim)_[0-9a-f]{16,}|hostfs-overlay/objects/|socks5h?://[^/@[:space:]]+@' \
  "$gate3_log" >/dev/null 2>&1; then
  echo "projection privacy: Gate 3 public log contains protected material" >&2
  exit 1
fi

mkdir -p "$out"
cp -R "$gate2_root"/. "$out"/
out="$(cd "$out" && pwd -P)"
privacy_rel="artifacts/projection-privacy-gate3.json"
privacy="$out/$privacy_rel"
package_identity="$(jq -cer '.packageIdentity' "$gate2_evidence")"
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$source_commit" \
  --argjson packageIdentity "$package_identity" \
  --argjson runtime "$gate3_runtime" '{
    schema:"hideout.projection-privacy-real-gate3/v1",
    status:"passed",
    generatedAt:$generatedAt,
    commit:$commit,
    dirty:false,
    packageIdentity:$packageIdentity,
    runtime:$runtime,
    checks:{
      guestWorkspaceAlias:true,
      proxyEnvAbsent:true,
      dnsMediated:true,
      connectedSubnetBlocked:true,
      httpsRequest:true,
      privilegeSeparation:true,
      publicEvidenceRedacted:true
    }
  }' >"$privacy"
privacy_sha="$(shasum -a 256 "$privacy" | awk '{print $1}')"

readiness="$out/artifacts/projection-readiness.json"
[ -f "$readiness" ] && [ ! -L "$readiness" ] || {
  echo "projection privacy: Gate 2 readiness artifact is missing or symlinked" >&2
  exit 1
}
jq --arg path "$privacy_rel" --arg sha "$privacy_sha" '
  .privacy = {status:"promoted",artifact:{path:$path,sha256:$sha}}
' "$readiness" >"$out/.projection-readiness.tmp"
mv "$out/.projection-readiness.tmp" "$readiness"
readiness_sha="$(shasum -a 256 "$readiness" | awk '{print $1}')"

artifact_refs="$out/.artifact-refs.json"
jq -n \
  --arg readinessSHA "$readiness_sha" \
  --arg samplesSHA "$(shasum -a 256 "$out/artifacts/readiness-samples.tsv" | awk '{print $1}')" \
  --arg flowsSHA "$(shasum -a 256 "$out/artifacts/projection-flows.json" | awk '{print $1}')" \
  --arg packageSHA "$(shasum -a 256 "$out/artifacts/package-manifest.json" | awk '{print $1}')" \
  --arg runtimeSHA "$(shasum -a 256 "$out/artifacts/runtime-manifest.json" | awk '{print $1}')" \
  --arg privacySHA "$privacy_sha" '[
    {kind:"manifest",path:"artifacts/projection-readiness.json",sha256:$readinessSHA,
     redactionStatus:"passed",description:"043 strict projection readiness result"},
    {kind:"log",path:"artifacts/readiness-samples.tsv",sha256:$samplesSHA,
     redactionStatus:"passed",description:"fresh, warm, and cancellation raw samples"},
    {kind:"manifest",path:"artifacts/projection-flows.json",sha256:$flowsSHA,
     redactionStatus:"passed",description:"030, 032, and 039 exact flow inventory"},
    {kind:"manifest",path:"artifacts/package-manifest.json",sha256:$packageSHA,
     redactionStatus:"passed",description:"verified exact package identity"},
    {kind:"manifest",path:"artifacts/runtime-manifest.json",sha256:$runtimeSHA,
     redactionStatus:"passed",description:"observed exact runtime binding"},
    {kind:"manifest",path:"artifacts/projection-privacy-gate3.json",sha256:$privacySHA,
     redactionStatus:"passed",description:"matching exact-package Gate 3 privacy result"}
  ]' >"$artifact_refs"

registry="$out/reports/proof-registry.json"
go run ./cmd/hideout support proof-registry --json >"$registry"
manifest="$out/product-hardening-evidence.json"
jq \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --argjson artifacts "$(jq -c . "$artifact_refs")" \
  --argjson runtime "$gate3_runtime" \
  --slurpfile registry "$registry" '
  def requirement($id): [$registry[0].requirements[] | select(.proofId == $id)][0];
  def claims($id): [requirement($id).claimIds[] |
    {claimId:.,source:"spec",description:("registered privacy contract " + .),
     scope:"projection-privacy"}];
  def privacyProof($id):
    (requirement($id)) as $requirement |
    {
      proofId:$id,
      featureId:$requirement.featureId,
      mode:$requirement.requiredMode,
      evidenceClass:$requirement.requiredEvidenceClass,
      status:"passed",
      commandSummary:"matching clean exact-package Gate 3 alias privacy passed",
      coveredClaims:claims($id),
      prerequisites:[
        {name:"real-macos-arm64-lima-packaged",status:"available"},
        {name:"matching-gate3-privacy",status:"available"}
      ],
      artifacts:$artifacts,
      redactionStatus:"passed",
      runtime:$runtime
    };
  .generatedAt = $generatedAt |
  .dirty = false |
  .proofs |= map(.artifacts = $artifacts) |
  .proofs += [
    privacyProof("030.projection.real-gate2.privacy-three-channel"),
    privacyProof("043.projection-readiness.real-gate3.privacy")
  ]
' "$manifest" >"$out/.manifest.tmp"
mv "$out/.manifest.tmp" "$manifest"
rm -f "$artifact_refs"

go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json \
  "$manifest" >/dev/null
go run ./internal/productevidence/cmd/validate-043 "$manifest" >/dev/null
echo "projection privacy: passed evidence=$manifest"
