#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
. "$ROOT/scripts/lib/public-alpha-cleanup.sh"
. "$ROOT/scripts/lib/gate-result.sh"

tag=""
package=""
signing=""
notarization=""
candidate_observation=""
out=""
upload=0
declare -a product_evidence=()

usage() {
  cat <<'USAGE'
Usage: scripts/test-public-alpha-candidate.sh \
  --tag <vSemVer> --package <tar.gz> \
  --signing-observation <json> --notarization-observation <json> \
  --candidate-observation <candidate.json> \
  --product-evidence <json> [--product-evidence <json> ...] \
  --out <dir> [--upload-draft]

Runs the exact packaged binary through clean install and the real Gate 2/3
lanes, evaluates all registered release-candidate proofs, assembles the exact
four public assets, and optionally replaces the assets of an existing private
draft. It never publishes a release. Gate 3 also requires an executable
operator-supplied HIDEOUT_LINUX_TUN2SOCKS_PATH.
USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --tag) tag="${2:-}"; shift 2 ;;
    --package) package="${2:-}"; shift 2 ;;
    --signing-observation) signing="${2:-}"; shift 2 ;;
    --notarization-observation) notarization="${2:-}"; shift 2 ;;
    --candidate-observation) candidate_observation="${2:-}"; shift 2 ;;
    --product-evidence) product_evidence+=("${2:-}"); shift 2 ;;
    --out) out="${2:-}"; shift 2 ;;
    --upload-draft) upload=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "public-alpha-candidate: unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

[ -n "$tag" ] && [ -f "$package" ] && [ -f "$signing" ] && [ -f "$notarization" ] &&
  [ -f "$candidate_observation" ] && [ -n "$out" ] || {
  usage >&2
  exit 2
}
case "$tag" in
  v[0-9]*.[0-9]*.[0-9]*-*) ;;
  *) echo "public-alpha-candidate: invalid prerelease tag: $tag" >&2; exit 2 ;;
esac
version="${tag#v}"
expected_package="hideout-v${version}-darwin-arm64.tar.gz"
[ "$(basename "$package")" = "$expected_package" ] || {
  echo "public-alpha-candidate: package basename must be $expected_package" >&2
  exit 2
}
for command in go jq git shasum gh limactl; do
  command -v "$command" >/dev/null 2>&1 || { echo "public-alpha-candidate: missing $command" >&2; exit 127; }
done
[ "$(uname -s)/$(uname -m)" = "Darwin/arm64" ] || {
  echo "public-alpha-candidate: real candidate requires macOS arm64" >&2
  exit 2
}
[ -n "${HIDEOUT_SECRET_DEFAULT_PROXY:-}" ] || {
  echo "public-alpha-candidate: HIDEOUT_SECRET_DEFAULT_PROXY is required for real Gate 3" >&2
  exit 2
}
[ -x "${HIDEOUT_LINUX_TUN2SOCKS_PATH:-}" ] || {
  echo "public-alpha-candidate: executable HIDEOUT_LINUX_TUN2SOCKS_PATH is required for real Gate 3" >&2
  exit 2
}

source_commit="$(git rev-parse HEAD)"
if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then
  echo "public-alpha-candidate: exact source checkout must be clean" >&2
  exit 2
fi

out="$(mkdir -p "$out" && cd "$out" && pwd -P)"
candidate_parent_tmp="${TMPDIR:-/tmp}"
candidate_short_tmp="${HIDEOUT_RELEASE_SHORT_TMPDIR:-/tmp}"
# Real Lima lanes append instance names and socket suffixes below TMPDIR. Keep
# the candidate-owned resource domain short enough for macOS UNIX_PATH_MAX.
work="$(mktemp -d "$candidate_short_tmp/hpa.XXXXXX")"
candidate_tmp="$work/t"
mkdir -p "$candidate_tmp"
export TMPDIR="$candidate_tmp"
export HIDEOUT_LIMA_SHORT_TMPDIR="$work"
cleanup_complete=0
gate_completed=0

perform_cleanup() {
  [ "$cleanup_complete" -eq 0 ] || return 0
  cleanup_complete=1
  local cleanup_status=0
  public_alpha_cleanup_root "$work" "$out/cleanup-report.json" || cleanup_status=$?
  export TMPDIR="$candidate_parent_tmp"
  return "$cleanup_status"
}

cleanup_on_exit() {
  local exit_status=$?
  trap - EXIT
  if ! perform_cleanup; then
    exit_status=1
  fi
  if [ "$gate_completed" != "1" ]; then
    gate_require_completion public-alpha-candidate
  fi
  exit "$exit_status"
}
trap cleanup_on_exit EXIT

hideout="$work/package/hideout/bin/hideout"
mkdir -p "$work/package"
tar -xzf "$package" -C "$work/package"
[ -x "$hideout" ] || { echo "public-alpha-candidate: package hideout binary missing" >&2; exit 1; }
"$hideout" package verify "$work/package/hideout" >/dev/null
"$hideout" support release package-identity --archive "$package" \
  --out "$out/package-identity.json" >/dev/null
package_sha="$(jq -r '.artifactSHA256' "$out/package-identity.json")"
validate_retained_gate0_candidate "$candidate_observation" "$source_commit" "$package_sha"
jq -e --arg tag "$tag" --arg version "$version" '
  .tag == $tag and .version == $version
' "$candidate_observation" >/dev/null || {
  echo "public-alpha-candidate: workflow candidate tag or version mismatch" >&2
  exit 2
}
"$hideout" support release observe-package-verification \
  --package-root "$work/package/hideout" --package-identity "$out/package-identity.json" \
  --out "$out/package-verify.json" >/dev/null
jq -e --arg version "$version" --arg commit "$source_commit" '
  .productVersion == $version and .sourceCommit == $commit and
  .hostOS == "darwin" and .hostArch == "arm64"
' "$out/package-identity.json" >/dev/null
"$hideout" support release validate-signing --package-root "$work/package/hideout" \
  --observation "$signing" >/dev/null
"$hideout" support release validate-notarization --package-root "$work/package/hideout" \
  --observation "$notarization" >/dev/null

cp "$signing" "$out/signing-observation.json"
cp "$notarization" "$out/notarization-observation.json"

HIDEOUT_REQUIRE_RUNTIME_CACHE=1 scripts/test-public-alpha-clean-install.sh --package "$package" --real-lima \
  --out "$out/clean-install.json"

arch="$(jq -r '.hostArch' "$out/package-identity.json")"
export HIDEOUT_RELEASE_BINARY="$hideout"
export HIDEOUT_LINUX_SHIM_PATH="$work/package/hideout/bin/hideout-shim-linux-$arch"
export HIDEOUT_LINUX_HOSTFSD_PATH="$work/package/hideout/bin/hideout-hostfsd-linux-$arch"
export HIDEOUT_LINUX_SESSION_SUPERVISOR_PATH="$work/package/hideout/bin/hideout-session-supervisor-linux-$arch"
export HIDEOUT_LINUX_WORKSPACE_PORTAL_PATH="$work/package/hideout/bin/hideout-workspace-portal-linux-$arch"
export HIDEOUT_LINUX_DNS_STUB_PATH="$work/package/hideout/bin/hideout-dns-stub-linux-$arch"
export HIDEOUT_RUNTIME_PACKAGE_IDENTITY="$out/package-identity.json"
export HIDEOUT_RELEASE_EVIDENCE_DIR="$out/phase1"
export HIDEOUT_RUNTIME_EVIDENCE_OUT="$out/runtime-gate2"
export HIDEOUT_GATE2_REQUIRE_PROJECTION=1
export HIDEOUT_GATE2_EXTERNAL_HOST_APP_PACK="$ROOT/test/host-app-packs/gate2-external"
mkdir -p "$HIDEOUT_RELEASE_EVIDENCE_DIR"

scripts/test-runtime-lima.sh >"$out/runtime-gate2.out" 2>"$out/runtime-gate2.err"
product_evidence+=("$out/runtime-gate2/product-hardening-evidence.json")
runtime_build_provenance="$out/runtime-gate2/build-provenance.json"
[ -f "$runtime_build_provenance" ] || { echo "public-alpha-candidate: runtime build provenance missing" >&2; exit 1; }
export HIDEOUT_RUNTIME_BUILD_PROVENANCE="$runtime_build_provenance"
if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then
  echo "public-alpha-candidate: real runtime gate changed the candidate checkout" >&2
  git status --short >&2
  exit 1
fi

# Reuse the one retained real Gate 2 run for every feature-level proof. Each
# consumer independently verifies the same-commit envelope, artifact digest,
# and its own required markers; no local or hand-written evidence is promoted.
scripts/test-hostfs-visibility-e2e.sh --real-gate2 --require-real \
  --gate2-evidence "$out/runtime-gate2/product-hardening-evidence.json" \
  --out "$out/hostfs-visibility-gate2"
product_evidence+=("$out/hostfs-visibility-gate2/product-hardening-evidence.json")
scripts/test-host-capability-projection-e2e.sh --real-gate2 --require-real \
  --gate2-evidence "$out/runtime-gate2/product-hardening-evidence.json" \
  --out "$out/projection-gate2"
product_evidence+=("$out/projection-gate2/product-hardening-evidence.json")
scripts/test-host-app-pack-e2e.sh --real-gate2 --require-real \
  --gate2-evidence "$out/runtime-gate2/product-hardening-evidence.json" \
  --out "$out/host-app-pack-gate2"
product_evidence+=("$out/host-app-pack-gate2/product-hardening-evidence.json")
unset HIDEOUT_GATE2_EXTERNAL_HOST_APP_PACK

scripts/test-concurrent-sessions-e2e.sh --real-gate2 --require-real \
  --baseline-commit 2f0cddebc5b0215989b04e1f94955e84f1926929 \
  --out "$out/concurrent-sessions-gate2"
product_evidence+=("$out/concurrent-sessions-gate2/product-hardening-evidence.json")

export HIDEOUT_RUNTIME_EVIDENCE_OUT="$out/runtime-gate3"
HIDEOUT_PHASE1_RETAINED_GATE0_CANDIDATE="$candidate_observation" \
  HIDEOUT_PHASE1_RETAINED_GATE0_PACKAGE_SHA256="$package_sha" \
  HIDEOUT_PHASE1_RETAINED_GATE2_OUTPUT="$out/runtime-gate2/logs/gate2.out" \
  scripts/test-phase1.sh --release-candidate >"$out/phase1.out" 2>"$out/phase1.err"
product_evidence+=("$out/runtime-gate3/product-hardening-evidence.json")
if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then
  echo "public-alpha-candidate: release gates changed the candidate checkout" >&2
  git status --short >&2
  exit 1
fi

gate2="$out/phase1/gates/gate2-lima.json"
gate3="$out/phase1/gates/gate3-hidden-proxy.json"
[ -f "$gate2" ] && [ -f "$gate3" ] || {
  echo "public-alpha-candidate: phase1 did not retain Gate 2 and Gate 3 results" >&2
  exit 1
}
jq -e '.result == "passed" and .backend == "lima"' "$gate2" "$gate3" >/dev/null

docs_candidate_raw="$work/docs-candidate.raw"
scripts/test-doc-truth-smoke.sh >"$docs_candidate_raw"
"$hideout" support release redact-public-evidence \
  --input "$docs_candidate_raw" --out "$out/docs-candidate.out" >/dev/null
if grep -En 'public package is available|current public alpha' README.md README.zh-CN.md docs/STATUS.md >/dev/null 2>&1; then
  echo "public-alpha-candidate: candidate docs claim publication before a receipt" >&2
  exit 1
fi

proof_dir="$out/proof-033"
mkdir -p "$proof_dir/artifacts"
cp "$out/package-identity.json" "$proof_dir/artifacts/package-identity.json"
cp "$out/signing-observation.json" "$proof_dir/artifacts/signing-observation.json"
cp "$out/notarization-observation.json" "$proof_dir/artifacts/notarization-observation.json"
cp "$out/clean-install.json" "$proof_dir/artifacts/clean-install.json"
jq 'del(.auditPath)' "$gate2" >"$proof_dir/artifacts/gate2.json"
jq 'del(.auditPath)' "$gate3" >"$proof_dir/artifacts/gate3.json"
cp "$out/docs-candidate.out" "$proof_dir/artifacts/docs-candidate.out"
"$hideout" support proof-registry --json >"$out/proof-registry.json"
runtime_binding="$(jq -c '[.proofs[] | select(.runtime != null)][0].runtime' "$out/runtime-gate2/product-hardening-evidence.json")"
jq -n \
  --arg generatedAt "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
  --arg commit "$source_commit" \
  --slurpfile package "$out/package-identity.json" \
  --slurpfile registry "$out/proof-registry.json" \
  --argjson runtime "$runtime_binding" '
  def claims($id): [$registry[0].requirements[] | select(.proofId == $id) | .claimIds[] |
    {claimId:.,source:"spec",description:"033 exact-candidate release proof"}];
  def artifact($path;$kind): {kind:$kind,path:$path,sha256:"",redactionStatus:"passed"};
  {
    version:"hideout.product-hardening-evidence/v1",
    generatedAt:$generatedAt,
    commit:$commit,
    dirty:false,
    packageIdentity:$package[0],
    proofs:[
      {proofId:"033.release.package-identity",featureId:"033-public-alpha-release-channel",
       mode:"local-fast",evidenceClass:"release-package-identity",status:"passed",
       commandSummary:"canonical package identity derived from exact archive",
       coveredClaims:claims("033.release.package-identity"),prerequisites:[],
       artifacts:[artifact("artifacts/package-identity.json";"manifest")],redactionStatus:"passed"},
      {proofId:"033.release.signing-notarization",featureId:"033-public-alpha-release-channel",
       mode:"local-fast",evidenceClass:"release-signing-notarization",status:"passed",
       commandSummary:"independent Developer ID and accepted notarization observations",
       coveredClaims:claims("033.release.signing-notarization"),prerequisites:[],
       artifacts:[artifact("artifacts/signing-observation.json";"manifest"),artifact("artifacts/notarization-observation.json";"manifest")],redactionStatus:"passed"},
      {proofId:"033.release.clean-install",featureId:"033-public-alpha-release-channel",
       mode:"real-gate",evidenceClass:"release-clean-install",status:"passed",
       commandSummary:"fresh packaged install and direct Lima first success",
       coveredClaims:claims("033.release.clean-install"),prerequisites:[{name:"real-macos-arm64-lima",status:"available"}],
       artifacts:[artifact("artifacts/clean-install.json";"manifest")],redactionStatus:"passed"},
      {proofId:"033.release.real-gate-binding",featureId:"033-public-alpha-release-channel",
       mode:"real-gate",evidenceClass:"release-real-gate-binding",status:"passed",
       commandSummary:"exact package and runtime bound Gate 2 and Gate 3",
       coveredClaims:claims("033.release.real-gate-binding"),prerequisites:[{name:"real-macos-arm64-lima",status:"available"}],
       artifacts:[artifact("artifacts/gate2.json";"manifest"),artifact("artifacts/gate3.json";"manifest")],
       redactionStatus:"passed",runtime:$runtime},
      {proofId:"033.release.docs-candidate-truth",featureId:"033-public-alpha-release-channel",
       mode:"docs",evidenceClass:"release-docs-candidate-truth",status:"passed",
       commandSummary:"candidate docs make no public availability claim",
       coveredClaims:claims("033.release.docs-candidate-truth"),prerequisites:[],
       artifacts:[artifact("artifacts/docs-candidate.out";"docs-report")],redactionStatus:"passed"}
    ]
  }' >"$proof_dir/manifest.json"
# Fill artifact digests after the shape is fixed.
jq '(.proofs[].artifacts[]) |= (.sha256 = "pending")' "$proof_dir/manifest.json" >"$proof_dir/manifest.tmp"
mv "$proof_dir/manifest.tmp" "$proof_dir/manifest.json"
while IFS= read -r path; do
  digest="$(shasum -a 256 "$proof_dir/$path" | awk '{print $1}')"
  jq --arg path "$path" --arg digest "$digest" '
    (.proofs[].artifacts[] | select(.path == $path) | .sha256) = $digest
  ' "$proof_dir/manifest.json" >"$proof_dir/manifest.tmp"
  mv "$proof_dir/manifest.tmp" "$proof_dir/manifest.json"
done < <(jq -r '.proofs[].artifacts[].path' "$proof_dir/manifest.json")
"$hideout" support proof-registry --json >/dev/null
go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json \
  "$proof_dir/manifest.json" >/dev/null
product_evidence+=("$proof_dir/manifest.json")

readiness_raw="$work/release-readiness.raw.json"
readiness_args=(
  support readiness --mode release-candidate --out "$readiness_raw"
  --gate2-evidence "$gate2" --gate3-evidence "$gate3"
  --runtime-family developer-standard --package-artifact "$package"
  --signing-observation "$signing" --notarization-observation "$notarization"
  --commit "$source_commit"
)
for manifest in "${product_evidence[@]}"; do
  [ -f "$manifest" ] || { echo "public-alpha-candidate: missing product evidence: $manifest" >&2; exit 2; }
  readiness_args+=(--product-evidence "$manifest")
done
"$hideout" "${readiness_args[@]}"
jq -e '.releaseReady == true and .mode == "release-candidate"' "$readiness_raw" >/dev/null
"$hideout" support release redact-public-evidence \
  --input "$readiness_raw" --out "$out/release-readiness.json" >/dev/null

evidence_root="$work/evidence"
mkdir -p "$evidence_root/proofs" "$evidence_root/package" "$evidence_root/signing" \
  "$evidence_root/notarization" "$evidence_root/runtime" "$evidence_root/gates"
cp "$out/package-identity.json" "$evidence_root/candidate-identity.json"
cp "$out/release-readiness.json" "$evidence_root/release-readiness.json"
cp "$work/package/hideout/package-manifest.json" "$evidence_root/package/package-manifest.json"
cp "$out/package-verify.json" "$evidence_root/package/verify.json"
cp "$signing" "$evidence_root/signing/observation.json"
cp "$notarization" "$evidence_root/notarization/observation.json"
cp "$proof_dir/artifacts/gate2.json" "$evidence_root/gates/gate2.json"
cp "$proof_dir/artifacts/gate3.json" "$evidence_root/gates/gate3.json"
cp "$runtime_build_provenance" "$evidence_root/runtime/build-provenance.json"
cp "$out/proof-registry.json" "$evidence_root/proof-registry.json"

copy_evidence_manifest() {
  local source="$1" index="$2" source_dir destination relative component current
  source_dir="$(cd "$(dirname "$source")" && pwd -P)"
  destination="$evidence_root/proofs/$index"
  mkdir -p "$destination"
  cp "$source" "$destination/manifest.json"
  while IFS= read -r relative; do
    case "$relative" in /*|../*|*/../*|*/..) echo "public-alpha-candidate: unsafe evidence artifact path: $relative" >&2; exit 1;; esac
    current="$source_dir"
    IFS='/' read -r -a components <<<"$relative"
    for component in "${components[@]}"; do
      current="$current/$component"
      [ ! -L "$current" ] || { echo "public-alpha-candidate: symlinked evidence artifact: $relative" >&2; exit 1; }
    done
    [ -f "$source_dir/$relative" ] || { echo "public-alpha-candidate: missing evidence artifact: $relative" >&2; exit 1; }
    mkdir -p "$destination/$(dirname "$relative")"
    cp -P "$source_dir/$relative" "$destination/$relative"
  done < <(jq -r '.proofs[].artifacts[].path' "$source" | LC_ALL=C sort -u)
}

index=0
for manifest in "${product_evidence[@]}"; do
  index=$((index + 1))
  copy_evidence_manifest "$manifest" "$(printf '%03d' "$index")"
done

evidence_name="hideout-v${version}-evidence.tar.gz"
evidence_archive="$out/$evidence_name"
"$hideout" support release build-evidence --root "$evidence_root" \
  --package-identity "$out/package-identity.json" --out "$evidence_archive" >/dev/null
"$hideout" support release validate-evidence --archive "$evidence_archive" >/dev/null

package_bytes="$(wc -c <"$package" | tr -d '[:space:]')"
evidence_sha="$(shasum -a 256 "$evidence_archive" | awk '{print $1}')"
evidence_bytes="$(wc -c <"$evidence_archive" | tr -d '[:space:]')"
package_manifest_sha="$(shasum -a 256 "$work/package/hideout/package-manifest.json" | awk '{print $1}')"
bundle_manifest_sha="$(shasum -a 256 "$evidence_root/bundle-manifest.json" | awk '{print $1}')"
readiness_sha="$(shasum -a 256 "$out/release-readiness.json" | awk '{print $1}')"
signing_sha="$(shasum -a 256 "$signing" | awk '{print $1}')"
support_matrix="$out/support-matrix.json"
"$hideout" support matrix --json >"$support_matrix"

release_name="hideout-v${version}-release.json"
release_manifest="$out/$release_name"
jq -n \
  --arg version "$version" --arg tag "$tag" --arg commit "$source_commit" \
  --arg generatedAt "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
  --arg packageName "$expected_package" --arg packageSHA "$package_sha" --argjson packageBytes "$package_bytes" \
  --arg packageManifestSHA "$package_manifest_sha" --arg evidenceName "$evidence_name" \
  --arg evidenceSHA "$evidence_sha" --argjson evidenceBytes "$evidence_bytes" \
  --arg bundleManifestSHA "$bundle_manifest_sha" --arg readinessSHA "$readiness_sha" \
  --arg signingSHA "$signing_sha" \
  --slurpfile packageManifest "$work/package/hideout/package-manifest.json" \
  --slurpfile signing "$signing" --slurpfile notarization "$notarization" \
  --slurpfile matrix "$support_matrix" '
  {
    schema:"hideout.public-release/v1",version:$version,channel:"alpha",
    maturity:"public-supervised-alpha",tag:$tag,
    source:{repository:"https://github.com/vibe-agi/hideout",commit:$commit,dirty:false},
    license:{spdx:"Apache-2.0",thirdPartyNotices:"THIRD_PARTY_NOTICES.md"},
    artifacts:[
      {kind:"package",name:$packageName,hostOS:"darwin",hostArch:"arm64",sha256:$packageSHA,
       bytes:$packageBytes,packageManifestSHA256:$packageManifestSHA},
      {kind:"evidence",name:$evidenceName,sha256:$evidenceSHA,bytes:$evidenceBytes,
       bundleManifestSHA256:$bundleManifestSHA,readinessSHA256:$readinessSHA,status:"passed"}
    ],
    signing:{status:$signing[0].status,teamId:$signing[0].teamId,commonName:$signing[0].commonName,
      observationSHA256:$signingSHA},
    notarization:{status:$notarization[0].status,submissionId:$notarization[0].submissionId,
      submissionSHA256:$notarization[0].submissionSHA256,ticketMode:$notarization[0].ticketMode,
      stapleStatus:$notarization[0].stapleStatus},
    runtime:{family:$packageManifest[0].runtime.family,revision:$packageManifest[0].runtime.revision,
      catalogFileSHA256:$packageManifest[0].runtime.catalogFileSHA256,
      artifactSHA256:$packageManifest[0].runtime.artifactSHA256},
    checksums:{name:"SHA256SUMS",covers:([$packageName,$evidenceName,("hideout-v"+$version+"-release.json")]|sort)},
    supportMatrixVersion:$matrix[0].version,
    nonClaims:[$matrix[0].nonClaims[].id],
    generatedAt:$generatedAt
  }' >"$release_manifest"

cp "$package" "$out/$expected_package"
(cd "$out" && for file in "$expected_package" "$evidence_name" "$release_name"; do
  printf '%s  %s\n' "$(shasum -a 256 "$file" | awk '{print $1}')" "$file"
done | LC_ALL=C sort -k2 >SHA256SUMS)
"$hideout" support release validate --manifest "$release_manifest" --asset-root "$out" >/dev/null

# All expensive lanes above inherit the candidate-owned TMPDIR. Verify and
# remove that complete resource domain before any optional draft upload.
perform_cleanup
trap - EXIT
trap 'gate_require_completion public-alpha-candidate' EXIT

release_id=""
if [ "$upload" -eq 1 ]; then
  [ -n "${GH_TOKEN:-${GITHUB_TOKEN:-}}" ] || { echo "public-alpha-candidate: --upload-draft requires GH_TOKEN" >&2; exit 2; }
  release_json="$(gh api --paginate "repos/vibe-agi/hideout/releases?per_page=100" | jq -s --arg tag "$tag" '[.[].[] | select(.tag_name == $tag)] | if length == 1 then .[0] else error("expected exactly one draft") end')"
  jq -e --arg commit "$source_commit" '.draft == true and .target_commitish == $commit' <<<"$release_json" >/dev/null
  release_id="$(jq -r '.id' <<<"$release_json")"
  while IFS= read -r asset; do
    gh release delete-asset "$tag" "$asset" --repo vibe-agi/hideout --yes
  done < <(jq -r '.assets[].name' <<<"$release_json")
  gh release upload "$tag" --repo vibe-agi/hideout \
    "$out/$expected_package" "$evidence_archive" "$release_manifest" "$out/SHA256SUMS"
fi

jq -n --arg tag "$tag" --arg version "$version" --arg commit "$source_commit" \
  --argjson releaseId "${release_id:-null}" --arg packageSHA "$package_sha" \
  --arg evidenceSHA "$evidence_sha" --arg releaseSHA "$(shasum -a 256 "$release_manifest" | awk '{print $1}')" \
  --slurpfile cleanup "$out/cleanup-report.json" \
  '{schema:"hideout.public-alpha-promotion-request/v1",tag:$tag,version:$version,
    sourceCommit:$commit,releaseId:$releaseId,cleanup:$cleanup[0],assets:{packageSHA256:$packageSHA,
      evidenceSHA256:$evidenceSHA,releaseManifestSHA256:$releaseSHA}}' \
  >"$out/promotion-request.json"

gate_completed=1
echo "public-alpha-candidate: candidate assets ready; publication not performed"
