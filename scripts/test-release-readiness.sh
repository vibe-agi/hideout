#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
. "$ROOT/scripts/lib/release-candidate-source.sh"

usage() {
  cat <<'USAGE'
Usage:
  scripts/test-release-readiness.sh --local-fast [--out <path>]
  scripts/test-release-readiness.sh --negative-fixtures
  scripts/test-release-readiness.sh --release-candidate \
    --package-artifact <tar.gz> \
    --signing-observation <json> --notarization-observation <json> \
    --product-evidence <path> [--product-evidence <path>...] \
    [--runtime-family <id>] [--out <path>]

Local-fast evidence is useful for development but is not release readiness.
Release-candidate mode requires real Gate 2 and Gate 3 evidence through:
  HIDEOUT_GATE2_EVIDENCE
  HIDEOUT_GATE3_EVIDENCE

Release-candidate defaults to runtime family developer-standard. Package and
product evidence are explicit trusted inputs; the script never discovers an
arbitrary nearby artifact or silently omits the product-evidence spine.
The exact package commit must be the clean checkout HEAD and already pushed to
origin/master. Negative-fixtures mode proves those source checks and the
release evidence judges without running Gate 0 recursively.
USAGE
}

mode=""
out=""
runtime_family="${HIDEOUT_RELEASE_RUNTIME_FAMILY:-}"
package_artifact="${HIDEOUT_RELEASE_PACKAGE_ARTIFACT:-}"
signing_observation="${HIDEOUT_RELEASE_SIGNING_OBSERVATION:-}"
notarization_observation="${HIDEOUT_RELEASE_NOTARIZATION_OBSERVATION:-}"
product_evidence=()
while [ "$#" -gt 0 ]; do
  case "$1" in
    --local-fast)
      mode="local-fast"
      shift
      ;;
    --release-candidate)
      mode="release-candidate"
      shift
      ;;
    --negative-fixtures)
      mode="negative-fixtures"
      shift
      ;;
    --out)
      out="${2:-}"
      if [ -z "$out" ]; then
        echo "release-readiness: --out requires a path" >&2
        exit 2
      fi
      shift 2
      ;;
    --runtime-family)
      runtime_family="${2:-}"
      [ -n "$runtime_family" ] || { echo "release-readiness: --runtime-family requires an id" >&2; exit 2; }
      shift 2
      ;;
    --package-artifact)
      package_artifact="${2:-}"
      [ -n "$package_artifact" ] || { echo "release-readiness: --package-artifact requires a path" >&2; exit 2; }
      shift 2
      ;;
    --package-root)
      echo "release-readiness: --package-root cannot prove the downloaded archive digest; use --package-artifact" >&2
      exit 2
      ;;
    --signing-observation)
      signing_observation="${2:-}"
      [ -n "$signing_observation" ] || { echo "release-readiness: --signing-observation requires a path" >&2; exit 2; }
      shift 2
      ;;
    --notarization-observation)
      notarization_observation="${2:-}"
      [ -n "$notarization_observation" ] || { echo "release-readiness: --notarization-observation requires a path" >&2; exit 2; }
      shift 2
      ;;
    --product-evidence)
      evidence_path="${2:-}"
      [ -n "$evidence_path" ] || { echo "release-readiness: --product-evidence requires a path" >&2; exit 2; }
      product_evidence+=("$evidence_path")
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "release-readiness: unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

run_negative_fixtures() {
  local fixture_root=""
  local remote=""
  local repository=""
  local pushed_commit=""
  local private_commit=""

  go test ./internal/releasecompat -count=1 \
    -run 'Test(ReadinessReportsStaleProductHardeningEvidence|ReleaseCandidateMissingEvidenceFailsClosed|ReleaseCandidateRejectsEmptyOrWrongGateEvidence|ReleaseCandidateRejectsNativeGateEvidence|ReleaseCandidateRequiresEveryReleaseProofPackageAndArtifactDigest|ReleaseCandidateRequires044OrdinaryUserUIProof|ValidateReadinessRejectsIdentitySubstitution)$'
  go test ./internal/releasechannel -count=1 \
    -run 'Test(PublicReleaseRejectsIdentityAndAssetMutations|PublicReleaseRejectsChangedPackageBytes|SigningAndNotarizationObservations|DeveloperPreviewCannotSatisfyPublicSigning|PublicationReceiptMatchesRelease)$'
  go test ./internal/productevidence -count=1 \
    -run 'TestRequire044(ReleaseCompleteRejectsMissingRealPrivacyProof|ReleaseCompleteRejectsMissingUIProof|PublicCompleteRejectsMissingReceipt)$'

  fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/hideout-release-source-fixtures.XXXXXX")"
  trap 'rm -rf "$fixture_root"' RETURN
  remote="$fixture_root/origin.git"
  repository="$fixture_root/candidate"
  git init --bare "$remote" >/dev/null
  git init "$repository" >/dev/null
  git -C "$repository" config user.email hideout-release-fixture@example.invalid
  git -C "$repository" config user.name hideout-release-fixture
  git -C "$repository" commit --allow-empty -m "pushed candidate" >/dev/null
  git -C "$repository" branch -M master
  git -C "$repository" remote add origin "$remote"
  git -C "$repository" push -u origin master >/dev/null
  pushed_commit="$(git -C "$repository" rev-parse HEAD)"
  validate_release_candidate_source "$repository" "$pushed_commit"

  : >"$repository/dirty-fixture"
  if validate_release_candidate_source "$repository" "$pushed_commit" >"$fixture_root/dirty.out" 2>&1; then
    echo "release-readiness: dirty candidate fixture unexpectedly passed" >&2
    return 1
  fi
  grep -q 'candidate checkout is dirty' "$fixture_root/dirty.out"
  rm -f "$repository/dirty-fixture"

  git -C "$repository" commit --allow-empty -m "private candidate" >/dev/null
  private_commit="$(git -C "$repository" rev-parse HEAD)"
  if validate_release_candidate_source "$repository" "$private_commit" >"$fixture_root/private.out" 2>&1; then
    echo "release-readiness: unpushed candidate fixture unexpectedly passed" >&2
    return 1
  fi
  grep -q 'candidate commit is not pushed' "$fixture_root/private.out"
  if validate_release_candidate_source "$repository" "$pushed_commit" >"$fixture_root/stale.out" 2>&1; then
    echo "release-readiness: stale package commit fixture unexpectedly passed" >&2
    return 1
  fi
  grep -q 'package commit is not the checked-out HEAD' "$fixture_root/stale.out"

  rm -rf "$fixture_root"
  trap - RETURN
  echo "release-readiness: negative fixtures passed"
}

if [ "$mode" = "negative-fixtures" ]; then
  run_negative_fixtures
  exit 0
fi

if [ -z "$mode" ]; then
  mode="local-fast"
fi
if [ -z "$out" ]; then
  out_dir="$(mktemp -d "${TMPDIR:-/tmp}/hideout-release-readiness.XXXXXX")"
  out="$out_dir/readiness.json"
fi

if [ "$mode" = "release-candidate" ]; then
  runtime_family="${runtime_family:-developer-standard}"
  [ -n "${HIDEOUT_GATE2_EVIDENCE:-}" ] || { echo "release-readiness: release-candidate requires HIDEOUT_GATE2_EVIDENCE" >&2; exit 2; }
  [ -n "${HIDEOUT_GATE3_EVIDENCE:-}" ] || { echo "release-readiness: release-candidate requires HIDEOUT_GATE3_EVIDENCE" >&2; exit 2; }
  [ -n "$runtime_family" ] || { echo "release-readiness: release-candidate requires --runtime-family" >&2; exit 2; }
  [ -f "$package_artifact" ] || { echo "release-readiness: release-candidate requires --package-artifact <tar.gz>" >&2; exit 2; }
  [ -f "$signing_observation" ] || { echo "release-readiness: release-candidate requires --signing-observation <json>" >&2; exit 2; }
  [ -f "$notarization_observation" ] || { echo "release-readiness: release-candidate requires --notarization-observation <json>" >&2; exit 2; }
  [ "${#product_evidence[@]}" -gt 0 ] || { echo "release-readiness: release-candidate requires at least one --product-evidence" >&2; exit 2; }
fi

commit="$(git rev-parse HEAD 2>/dev/null || printf unknown)"
local_status="passed"

if [ "$mode" = "release-candidate" ]; then
  identity_tmp="$(mktemp "${TMPDIR:-/tmp}/hideout-readiness-package-identity.XXXXXX")"
  trap 'rm -f "$identity_tmp"' EXIT
  go run ./cmd/hideout support release package-identity \
    --archive "$package_artifact" --out "$identity_tmp" >/dev/null
  package_commit="$(jq -r '.sourceCommit' "$identity_tmp")"
  validate_release_candidate_source "$ROOT" "$package_commit"
fi

run_local_fast_checks() {
  go build ./... &&
    go vet ./... &&
    test -z "$(gofmt -l internal cmd)" &&
    git diff --check &&
    go test -count=1 ./... &&
    scripts/test-gate0.sh
}

if ! run_local_fast_checks; then
  local_status="failed"
fi

readiness_args=(
  support readiness
  --mode "$mode"
  --out "$out"
  --commit "$commit"
  --local-status "$local_status"
  --gate2-evidence "${HIDEOUT_GATE2_EVIDENCE:-}"
  --gate3-evidence "${HIDEOUT_GATE3_EVIDENCE:-}"
)
if [ -n "$runtime_family" ]; then
  readiness_args+=(--runtime-family "$runtime_family")
fi
if [ -n "$package_artifact" ]; then
  readiness_args+=(--package-artifact "$package_artifact")
fi
if [ -n "$signing_observation" ]; then
  readiness_args+=(--signing-observation "$signing_observation")
fi
if [ -n "$notarization_observation" ]; then
  readiness_args+=(--notarization-observation "$notarization_observation")
fi
for evidence_path in "${product_evidence[@]}"; do
  readiness_args+=(--product-evidence "$evidence_path")
done

set +e
go run ./cmd/hideout "${readiness_args[@]}"
status=$?
set -e

go run ./cmd/hideout-schema-validate schemas/release-readiness.schema.json "$out" >/dev/null
echo "release-readiness: artifact $out"

exit "$status"
