#!/usr/bin/env bash
set -euo pipefail

repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd -P)"
cd "$repo_root"

umask 077
export LC_ALL=C
export TZ=UTC

tmp_base="${TMPDIR:-/tmp}"
tmp_base="${tmp_base%/}"
out="$repo_root/.artifacts/045/package-lifecycle"
candidate_result="$repo_root/.artifacts/045/package/result.json"
old_package=""
preflight_only=0

usage() {
  printf '%s\n' \
    "Usage: scripts/release/test-package-lifecycle.sh [--preflight]" \
    "       [--candidate-result FILE] [--old-package FILE] [--out DIR]" \
    "" \
    "Consumes (without rebuilding) the exact T158 candidate. It tests a clean" \
    "install, same-candidate reinstall, upgrade from the checked-in current" \
    "public alpha after an explicit exact-scope legacy-data discard, Keychain" \
    "migration guidance, and package absence after normal uninstall." \
    "" \
    "All mutation occurs below a private temporary directory. Normal uninstall" \
    "must preserve durable state. This command never publishes anything."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --preflight)
      preflight_only=1
      shift
      ;;
    --candidate-result)
      if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
        printf 'package-lifecycle: --candidate-result requires a file\n' >&2
        exit 2
      fi
      candidate_result="$2"
      shift 2
      ;;
    --old-package)
      if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
        printf 'package-lifecycle: --old-package requires a file\n' >&2
        exit 2
      fi
      old_package="$2"
      shift 2
      ;;
    --out)
      if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
        printf 'package-lifecycle: --out requires a directory\n' >&2
        exit 2
      fi
      out="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'package-lifecycle: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    printf 'package-lifecycle: missing required command: %s\n' "$1" >&2
    return 1
  }
}

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
    return
  fi
  sha256sum "$1" | awk '{print $1}'
}

file_mode() {
  stat -f '%Lp' "$1" 2>/dev/null ||
    stat -c '%a' "$1" 2>/dev/null
}

file_bytes() {
  stat -f '%z' "$1" 2>/dev/null ||
    stat -c '%s' "$1" 2>/dev/null
}

safe_relative_path() {
  case "$1" in
    "" | /* | . | .. | ../* | */.. | */../* | *$'\n'* | *$'\r'* | *$'\t'*)
      return 1
      ;;
  esac
}

cleanup_tree() {
  local cleanup_target="${1-}" prefix="${2-}"
  case "$cleanup_target" in
    "$tmp_base"/"$prefix".*)
      [ ! -e "$cleanup_target" ] ||
        find "$cleanup_target" -depth -delete
      ;;
    *)
      printf 'package-lifecycle: refusing unexpected cleanup target: %s\n' \
        "$cleanup_target" >&2
      return 1
      ;;
  esac
}

candidate_evidence_root=""
candidate_summary=""
candidate_archive=""
candidate_archive_rel=""
candidate_archive_sha=""
candidate_summary_sha=""
candidate_commit=""
candidate_tree=""
candidate_version=""
candidate_manifest_sha=""

resolve_candidate() {
  local result_path="$1" summary_rel archive_rel
  if [ ! -f "$result_path" ] || [ -L "$result_path" ]; then
    printf 'package-lifecycle: T158 result is missing or unsafe: %s\n' \
      "$result_path" >&2
    return 1
  fi
  result_path="$(cd "$(dirname "$result_path")" && pwd -P)/$(basename "$result_path")"
  candidate_evidence_root="$(dirname "$result_path")"
  if ! jq -e '
    .schema == "hideout.release-package-candidate-pointer/v1" and
    .result == "passed" and
    .source.dirty == false and
    .candidateAcceptance == true and
    .publicationStatus == "local-only" and
    (.source.commit | test("^[a-f0-9]{40}$")) and
    (.source.tree | test("^[a-f0-9]{40}$")) and
    (.summarySHA256 | test("^[a-f0-9]{64}$")) and
    (.archiveSHA256 | test("^[a-f0-9]{64}$"))
  ' "$result_path" >/dev/null; then
    printf 'package-lifecycle: T158 result semantics are invalid\n' >&2
    return 1
  fi
  summary_rel="$(jq -er '.summary' "$result_path")"
  archive_rel="$(jq -er '.archive' "$result_path")"
  if ! safe_relative_path "$summary_rel" ||
    ! safe_relative_path "$archive_rel"; then
    printf 'package-lifecycle: T158 result contains an unsafe path\n' >&2
    return 1
  fi
  candidate_summary="$candidate_evidence_root/$summary_rel"
  candidate_archive="$candidate_evidence_root/$archive_rel"
  if [ ! -f "$candidate_summary" ] || [ -L "$candidate_summary" ] ||
    [ ! -f "$candidate_archive" ] || [ -L "$candidate_archive" ]; then
    printf 'package-lifecycle: T158 summary/archive is missing or unsafe\n' >&2
    return 1
  fi
  candidate_summary_sha="$(sha256_file "$candidate_summary")"
  candidate_archive_sha="$(sha256_file "$candidate_archive")"
  if [ "$candidate_summary_sha" != "$(jq -er '.summarySHA256' "$result_path")" ] ||
    [ "$candidate_archive_sha" != "$(jq -er '.archiveSHA256' "$result_path")" ]; then
    printf 'package-lifecycle: T158 pointer digest binding failed\n' >&2
    return 1
  fi
  candidate_commit="$(jq -er '.source.commit' "$candidate_summary")"
  candidate_tree="$(jq -er '.source.tree' "$candidate_summary")"
  candidate_version="$(jq -er '.candidate.version' "$candidate_summary")"
  candidate_manifest_sha="$(
    jq -er '.candidate.packageManifestSHA256' "$candidate_summary"
  )"
  candidate_archive_rel="$archive_rel"
  if ! jq -e \
    --arg commit "$candidate_commit" \
    --arg tree "$(jq -er '.source.tree' "$result_path")" \
    --arg archive "$archive_rel" \
    --arg archiveSHA256 "$candidate_archive_sha" \
    --arg manifestSHA256 "$candidate_manifest_sha" '
      .schema == "hideout.release-package-candidate/v1" and
      .result == "passed" and
      .source.commit == $commit and
      .source.tree == $tree and
      .source.dirty == false and
      .source.stableAcrossRun == true and
      .candidate.acceptance == true and
      .candidate.archive == $archive and
      .candidate.archiveSHA256 == $archiveSHA256 and
      .candidate.packageManifestSHA256 == $manifestSHA256 and
      .candidate.channel == "developer-preview" and
      .candidate.signingMode == "developer-preview-unsigned" and
      .candidate.publicationStatus == "local-only" and
      .reproducibility.archiveBytesIdentical == true and
      .reproducibility.packageManifestBytesIdentical == true and
      .reproducibility.packageTreeInventoryIdentical == true and
      .validation.binaryVulnerabilityScan == true and
      any(.artifacts[];
        .path == $archive and
        .sha256 == $archiveSHA256 and
        .mode == "0600")
    ' "$candidate_summary" >/dev/null; then
    printf 'package-lifecycle: T158 summary binding failed\n' >&2
    return 1
  fi
  validate_artifact_manifest \
    "$candidate_evidence_root" \
    "$(dirname "$candidate_summary")" \
    "$candidate_summary"
}

validate_lifecycle_summary() {
  local summary_path="$1" expected_commit="$2"
  local expected_archive_sha="$3"
  jq -e \
    --arg commit "$expected_commit" \
    --arg archiveSHA256 "$expected_archive_sha" '
      .schema == "hideout.release-package-lifecycle/v1" and
      .result == "passed" and
      .sourceCandidate.commit == $commit and
      (.sourceCandidate.tree | test("^[a-f0-9]{40}$")) and
      .sourceCandidate.dirty == false and
      .candidate.archiveSHA256 == $archiveSHA256 and
      .candidate.consumedWithoutRebuild == true and
      .candidate.acceptance == true and
      .oldRelease.verifiedPublishedBytes == true and
      .checks.cleanInstall == true and
      .checks.keychainMigrationGuidance == true and
      .checks.currentVersionReinstall == true and
      .checks.oldVersionUpgrade == true and
      .checks.legacyDataDiscarded == true and
      .checks.legacyDiscardExactScope == true and
      .checks.normalUninstall == true and
      .checks.packageAbsenceAfterUninstall == true and
      .checks.durableStatePreservedByNormalUninstall == true and
      .checks.unrelatedFilesPreserved == true and
      .checks.noSecretValueInEvidence == true and
      .publicationStatus == "local-only" and
      (.artifacts | length) > 0 and
      all(.artifacts[];
        (.path | type) == "string" and
        (.path | length) > 0 and
        (.sha256 | test("^[a-f0-9]{64}$")) and
        (.bytes | type) == "number" and
        .bytes >= 0 and
        .bytes == (.bytes | floor) and
        .mode == "0600")
    ' "$summary_path" >/dev/null
}

validate_artifact_manifest() {
  local evidence_root="$1" run_directory="$2" summary_path="$3"
  local expected_list actual_list path digest bytes mode actual_mode
  if [ ! -d "$evidence_root" ] || [ -L "$evidence_root" ] ||
    [ ! -d "$run_directory" ] || [ -L "$run_directory" ]; then
    printf 'package-lifecycle: evidence run directory is unsafe\n' >&2
    return 1
  fi
  evidence_root="$(cd "$evidence_root" && pwd -P)"
  run_directory="$(cd "$run_directory" && pwd -P)"
  if [ "$(cd "$(dirname "$run_directory")" && pwd -P)" != "$evidence_root" ]; then
    printf 'package-lifecycle: evidence run directory escaped its root\n' >&2
    return 1
  fi
  expected_list="$(
    mktemp "$tmp_base/hideout-lifecycle-artifacts-expected.XXXXXX"
  )"
  actual_list="$(
    mktemp "$tmp_base/hideout-lifecycle-artifacts-actual.XXXXXX"
  )"
  jq -r '.artifacts[].path' "$summary_path" | LC_ALL=C sort \
    >"$expected_list"
  (
    cd "$evidence_root"
    find "$(basename "$run_directory")" \
      -type f \
      -print |
      awk -v omit="$(basename "$run_directory")/summary.json" \
        '$0 != omit' |
      LC_ALL=C sort
  ) >"$actual_list"
  if ! cmp -s "$expected_list" "$actual_list"; then
    printf 'package-lifecycle: evidence artifact set is not exact\n' >&2
    diff -u "$expected_list" "$actual_list" >&2 || true
    find "$expected_list" "$actual_list" -depth -delete
    return 1
  fi
  while IFS=$'\t' read -r path digest bytes mode; do
    safe_relative_path "$path" || {
      find "$expected_list" "$actual_list" -depth -delete
      return 1
    }
    if [ ! -f "$evidence_root/$path" ] ||
      [ -L "$evidence_root/$path" ] ||
      [ "$(sha256_file "$evidence_root/$path")" != "$digest" ] ||
      [ "$(file_bytes "$evidence_root/$path")" -ne "$bytes" ]; then
      printf 'package-lifecycle: evidence artifact drifted: %s\n' \
        "$path" >&2
      find "$expected_list" "$actual_list" -depth -delete
      return 1
    fi
    actual_mode="$(file_mode "$evidence_root/$path")"
    if [ "$mode" != "0600" ] || [ "$actual_mode" != "600" ]; then
      printf 'package-lifecycle: evidence artifact mode drifted: %s\n' \
        "$path" >&2
      find "$expected_list" "$actual_list" -depth -delete
      return 1
    fi
  done < <(
    jq -r \
      '.artifacts[] | [.path,.sha256,(.bytes|tostring),.mode] | @tsv' \
      "$summary_path"
  )
  find "$expected_list" "$actual_list" -depth -delete
}

validate_source_identity() {
  local source_root="$1" expected_commit="$2" expected_tree="$3"
  git -C "$source_root" rev-parse --git-dir >/dev/null 2>&1 &&
    [ "$(git -C "$source_root" rev-parse --verify HEAD)" = "$expected_commit" ] &&
    [ "$(git -C "$source_root" rev-parse --verify 'HEAD^{tree}')" = "$expected_tree" ] &&
    [ -z "$(git -C "$source_root" status --porcelain=v1 --untracked-files=all)" ]
}

discard_exact_legacy_store() {
  local store_root="$1" allowed_root="$2"
  local resolved_store resolved_allowed
  if [ ! -d "$store_root" ] || [ -L "$store_root" ] ||
    [ ! -d "$allowed_root" ] || [ -L "$allowed_root" ]; then
    printf 'package-lifecycle: legacy discard scope is missing or unsafe\n' >&2
    return 1
  fi
  resolved_store="$(cd "$store_root" && pwd -P)"
  resolved_allowed="$(cd "$allowed_root" && pwd -P)"
  if [ "$resolved_store" != "$resolved_allowed/old-store" ]; then
    printf 'package-lifecycle: refusing legacy discard outside exact scope\n' \
      >&2
    return 1
  fi
  find "$resolved_store" -depth -delete
  mkdir -m 0700 "$resolved_store"
}

validate_archive_members() {
  local archive="$1" members="$2" entry normalized
  tar -tzf "$archive" >"$members"
  if [ ! -s "$members" ]; then
    printf 'package-lifecycle: archive has no members\n' >&2
    return 1
  fi
  while IFS= read -r entry; do
    normalized="${entry%/}"
    case "$normalized" in
      hideout | hideout/*) ;;
      *)
        printf 'package-lifecycle: archive member escapes package root: %s\n' \
          "$entry" >&2
        return 1
        ;;
    esac
    case "$normalized" in
      *"/../"* | */.. | ../* | /* | *$'\n'* | *$'\t'*)
        printf 'package-lifecycle: archive member path is unsafe: %s\n' \
          "$entry" >&2
        return 1
        ;;
    esac
  done <"$members"
  if [ -n "$(LC_ALL=C sort "$members" | uniq -d)" ]; then
    printf 'package-lifecycle: archive contains duplicate members\n' >&2
    return 1
  fi
}

extract_package() {
  local archive="$1" destination="$2" label="$3"
  validate_archive_members "$archive" "$destination-members.txt"
  mkdir "$destination"
  tar -xzf "$archive" -C "$destination"
  if [ ! -d "$destination/hideout" ] ||
    [ -L "$destination/hideout" ] ||
    find "$destination/hideout" ! -type f ! -type d -print -quit |
      grep -q .; then
    printf 'package-lifecycle: extracted %s package tree is unsafe\n' \
      "$label" >&2
    return 1
  fi
}

for command in awk bash cmp curl diff find git grep jq mktemp sed shasum stat \
  tar uniq; do
  require_command "$command"
done

if [ "$preflight_only" -eq 1 ]; then
  preflight_root="$(
    mktemp -d "$tmp_base/hideout-package-lifecycle-preflight.XXXXXX"
  )"
  # Invoked indirectly by the EXIT trap.
  # shellcheck disable=SC2329
  cleanup_preflight() {
    cleanup_tree \
      "${preflight_root:-}" \
      "hideout-package-lifecycle-preflight"
  }
  trap cleanup_preflight EXIT

  fixture_evidence="$preflight_root/candidate"
  fixture_run="$fixture_evidence/run-fixture"
  mkdir -p "$fixture_run"
  printf 'candidate archive fixture\n' \
    >"$fixture_run/candidate.tar.gz"
  fixture_archive_sha="$(
    sha256_file "$fixture_run/candidate.tar.gz"
  )"
  fixture_archive_bytes="$(file_bytes "$fixture_run/candidate.tar.gz")"
  fixture_manifest_sha="$(
    printf manifest | shasum -a 256 | awk '{print $1}'
  )"
  jq -n \
    --arg archiveSHA256 "$fixture_archive_sha" \
    --arg manifestSHA256 "$fixture_manifest_sha" \
    --argjson archiveBytes "$fixture_archive_bytes" '
      {
        schema:"hideout.release-package-candidate/v1",
        result:"passed",
        source:{
          commit:"0123456789abcdef0123456789abcdef01234567",
          tree:"abcdef0123456789abcdef0123456789abcdef01",
          dirty:false,
          stableAcrossRun:true
        },
        candidate:{
          acceptance:true,
          version:"0.1.0-alpha.4",
          archive:"run-fixture/candidate.tar.gz",
          archiveSHA256:$archiveSHA256,
          packageManifestSHA256:$manifestSHA256,
          channel:"developer-preview",
          signingMode:"developer-preview-unsigned",
          publicationStatus:"local-only"
        },
        reproducibility:{
          archiveBytesIdentical:true,
          packageManifestBytesIdentical:true,
          packageTreeInventoryIdentical:true
        },
        validation:{binaryVulnerabilityScan:true},
        artifacts:[{
          path:"run-fixture/candidate.tar.gz",
          sha256:$archiveSHA256,
          bytes:$archiveBytes,
          mode:"0600"
        }]
      }
    ' >"$fixture_run/summary.json"
  fixture_summary_sha="$(sha256_file "$fixture_run/summary.json")"
  jq -n \
    --arg archiveSHA256 "$fixture_archive_sha" \
    --arg summarySHA256 "$fixture_summary_sha" '
      {
        schema:"hideout.release-package-candidate-pointer/v1",
        result:"passed",
        source:{
          commit:"0123456789abcdef0123456789abcdef01234567",
          tree:"abcdef0123456789abcdef0123456789abcdef01",
          dirty:false
        },
        candidateAcceptance:true,
        publicationStatus:"local-only",
        summary:"run-fixture/summary.json",
        summarySHA256:$summarySHA256,
        archive:"run-fixture/candidate.tar.gz",
        archiveSHA256:$archiveSHA256
      }
    ' >"$fixture_evidence/result.json"
  resolve_candidate "$fixture_evidence/result.json"
  jq '.candidateAcceptance = false' \
    "$fixture_evidence/result.json" \
    >"$fixture_evidence/result-negative.json"
  if resolve_candidate "$fixture_evidence/result-negative.json" \
    >/dev/null 2>&1; then
    printf 'package-lifecycle: rejected T158 pointer fixture was accepted\n' \
      >&2
    exit 1
  fi

  fixture_lifecycle_run="$preflight_root/lifecycle-run"
  mkdir "$fixture_lifecycle_run"
  printf 'lifecycle evidence fixture\n' \
    >"$fixture_lifecycle_run/fixture.log"
  chmod 0600 "$fixture_lifecycle_run/fixture.log"
  fixture_lifecycle_sha="$(
    sha256_file "$fixture_lifecycle_run/fixture.log"
  )"
  fixture_summary="$fixture_lifecycle_run/summary.json"
  jq -n \
    --arg archiveSHA256 "$fixture_archive_sha" \
    --arg artifactSHA256 "$fixture_lifecycle_sha" '
      {
        schema:"hideout.release-package-lifecycle/v1",
        result:"passed",
        sourceCandidate:{
          commit:"0123456789abcdef0123456789abcdef01234567",
          tree:"abcdef0123456789abcdef0123456789abcdef01",
          dirty:false
        },
        candidate:{
          archiveSHA256:$archiveSHA256,
          consumedWithoutRebuild:true,
          acceptance:true
        },
        oldRelease:{verifiedPublishedBytes:true},
        checks:{
          cleanInstall:true,
          keychainMigrationGuidance:true,
          currentVersionReinstall:true,
          oldVersionUpgrade:true,
          legacyDataDiscarded:true,
          legacyDiscardExactScope:true,
          normalUninstall:true,
          packageAbsenceAfterUninstall:true,
          durableStatePreservedByNormalUninstall:true,
          unrelatedFilesPreserved:true,
          noSecretValueInEvidence:true
        },
        publicationStatus:"local-only",
        artifacts:[{
          path:"lifecycle-run/fixture.log",
          sha256:$artifactSHA256,
          bytes:27,
          mode:"0600"
        }]
      }
    ' >"$fixture_summary"
  chmod 0600 "$fixture_summary"
  validate_lifecycle_summary \
    "$fixture_summary" \
    "0123456789abcdef0123456789abcdef01234567" \
    "$fixture_archive_sha"
  validate_artifact_manifest \
    "$preflight_root" \
    "$fixture_lifecycle_run" \
    "$fixture_summary"
  jq '.checks.packageAbsenceAfterUninstall = false' \
    "$fixture_summary" >"$preflight_root/lifecycle-summary-negative.json"
  if validate_lifecycle_summary \
    "$preflight_root/lifecycle-summary-negative.json" \
    "0123456789abcdef0123456789abcdef01234567" \
    "$fixture_archive_sha"; then
    printf 'package-lifecycle: incomplete lifecycle fixture was accepted\n' \
      >&2
    exit 1
  fi
  artifact_negative="$preflight_root/lifecycle-artifact-negative.json"
  jq '.artifacts[0].sha256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"' \
    "$fixture_summary" >"$artifact_negative"
  if validate_artifact_manifest \
    "$preflight_root" \
    "$fixture_lifecycle_run" \
    "$artifact_negative" >/dev/null 2>&1; then
    printf 'package-lifecycle: drifted artifact fixture was accepted\n' >&2
    exit 1
  fi
  find "$artifact_negative" -depth -delete

  discard_fixture="$preflight_root/discard"
  mkdir -p "$discard_fixture/old-store"
  printf 'legacy\n' >"$discard_fixture/old-store/legacy.txt"
  printf 'outside\n' >"$discard_fixture/outside.txt"
  discard_exact_legacy_store \
    "$discard_fixture/old-store" \
    "$discard_fixture"
  if [ -e "$discard_fixture/old-store/legacy.txt" ] ||
    [ ! -f "$discard_fixture/outside.txt" ]; then
    printf 'package-lifecycle: exact discard fixture failed\n' >&2
    exit 1
  fi
  if discard_exact_legacy_store \
    "$discard_fixture" \
    "$discard_fixture" >/dev/null 2>&1; then
    printf 'package-lifecycle: broad discard fixture was accepted\n' >&2
    exit 1
  fi

  source_fixture="$preflight_root/source"
  mkdir "$source_fixture"
  git -C "$source_fixture" init -q
  git -C "$source_fixture" config user.name hideout-release-preflight
  git -C "$source_fixture" config user.email release-preflight@invalid
  printf 'source fixture\n' >"$source_fixture/source.txt"
  git -C "$source_fixture" add source.txt
  git -C "$source_fixture" commit -q -m fixture
  fixture_commit="$(git -C "$source_fixture" rev-parse HEAD)"
  fixture_tree="$(git -C "$source_fixture" rev-parse 'HEAD^{tree}')"
  validate_source_identity "$source_fixture" "$fixture_commit" "$fixture_tree"
  printf 'dirty source fixture\n' >"$source_fixture/source.txt"
  if validate_source_identity \
    "$source_fixture" \
    "$fixture_commit" \
    "$fixture_tree"; then
    printf 'package-lifecycle: dirty source fixture was accepted\n' >&2
    exit 1
  fi

  bash -n scripts/release/test-package-lifecycle.sh
  go test ./internal/app \
    -run 'Test(ContextualHelpIsSuccessfulAndWritesNoState|CommandCatalogMetadataIsCompleteAndSearchable)' \
    -count=1 >/dev/null
  printf 'package-lifecycle: preflight=passed\n'
  exit 0
fi

if [ "$(uname -s)" != "Darwin" ] || [ "$(uname -m)" != "arm64" ]; then
  printf 'package-lifecycle: full gate requires Darwin/arm64\n' >&2
  exit 1
fi

resolve_candidate "$candidate_result"
candidate_archive_initial_sha="$candidate_archive_sha"
if ! validate_source_identity \
  "$repo_root" \
  "$candidate_commit" \
  "$candidate_tree"; then
  printf 'package-lifecycle: source no longer matches the clean T158 candidate\n' \
    >&2
  exit 1
fi

scratch="$(
  mktemp -d "$tmp_base/hideout-package-lifecycle.XXXXXX"
)"
cleanup() {
  cleanup_tree "${scratch:-}" "hideout-package-lifecycle"
}
trap cleanup EXIT
evidence="$scratch/evidence"
mkdir -p "$evidence"

extract_package \
  "$candidate_archive" \
  "$scratch/candidate" \
  candidate
candidate_root="$scratch/candidate/hideout"
candidate_binary="$candidate_root/bin/hideout"
candidate_manifest="$candidate_root/package-manifest.json"
if [ "$(sha256_file "$candidate_manifest")" != "$candidate_manifest_sha" ] ||
  ! jq -e \
    --arg commit "$candidate_commit" \
    --arg version "$candidate_version" '
      .schema == "hideout.package-manifest/v1" and
      .source.commit == $commit and
      .source.dirty == false and
      .release.productVersion == $version and
      .release.tag == ("v" + $version)
    ' "$candidate_manifest" >/dev/null; then
  printf 'package-lifecycle: extracted candidate identity drifted\n' >&2
  exit 1
fi
"$candidate_binary" package verify "$candidate_root" \
  >"$evidence/candidate-package-verify.log"

if ! jq -e '
  .current as $current |
  ($current.version | test("^[0-9]+\\.[0-9]+\\.[0-9]+-[0-9A-Za-z.-]+$")) and
  $current.tag == ("v" + $current.version) and
  $current.platform == "darwin/arm64" and
  $current.package.productVersion == $current.version and
  $current.package.hostOS == "darwin" and
  $current.package.hostArch == "arm64" and
  ($current.package.sourceCommit | test("^[a-f0-9]{40}$")) and
  ($current.package.artifactSHA256 | test("^[a-f0-9]{64}$")) and
  ($current.receiptSHA256 | test("^[a-f0-9]{64}$"))
' releases/current.json >/dev/null; then
  printf 'package-lifecycle: checked-in current release metadata is invalid\n' \
    >&2
  exit 1
fi
current_receipt="$repo_root/releases/receipts/$(
  jq -er '.current.tag' releases/current.json
).json"
if [ ! -f "$current_receipt" ] || [ -L "$current_receipt" ]; then
  printf 'package-lifecycle: checked-in current receipt is missing\n' >&2
  exit 1
fi
old_version="$(jq -er '.current.version' releases/current.json)"
old_tag="$(jq -er '.current.tag' releases/current.json)"
old_archive_name="hideout-v$old_version-darwin-arm64.tar.gz"
old_archive_sha="$(jq -er '.current.package.artifactSHA256' releases/current.json)"
current_receipt_sha="$(sha256_file "$current_receipt")"
expected_receipt_sha="$(jq -er '.current.receiptSHA256' releases/current.json)"
if [ "$current_receipt_sha" != "$expected_receipt_sha" ] ||
  ! jq -e \
    --arg version "$old_version" \
    --arg tag "$old_tag" \
    --arg archive "$old_archive_name" \
    --arg archiveSHA256 "$old_archive_sha" '
      .schema == "hideout.publication-receipt/v1" and
      .status == "public-verified" and
      .version == $version and
      .tag == $tag and
      .immutable == true and
      .package.productVersion == $version and
      .package.artifactSHA256 == $archiveSHA256 and
      any(.assets[];
        .name == $archive and
        .apiSHA256 == $archiveSHA256 and
        .downloadSHA256 == $archiveSHA256)
    ' "$current_receipt" >/dev/null; then
  printf 'package-lifecycle: checked-in old release identity is invalid\n' >&2
  exit 1
fi
if [ "$candidate_version" = "$old_version" ]; then
  printf 'package-lifecycle: old-version upgrade requires a different version\n' \
    >&2
  exit 1
fi

if [ -z "$old_package" ]; then
  old_package="$scratch/$old_archive_name"
  old_url="https://github.com/vibe-agi/hideout/releases/download/$old_tag/$old_archive_name"
  curl \
    --fail \
    --location \
    --proto '=https' \
    --tlsv1.2 \
    --retry 3 \
    --output "$old_package" \
    "$old_url" \
    >"$evidence/old-package-download.log" 2>&1
else
  old_package="$(cd "$(dirname "$old_package")" && pwd -P)/$(basename "$old_package")"
  printf 'old package supplied locally; immutable receipt digest enforced\n' \
    >"$evidence/old-package-download.log"
fi
if [ ! -f "$old_package" ] || [ -L "$old_package" ] ||
  [ "$(sha256_file "$old_package")" != "$old_archive_sha" ]; then
  printf 'package-lifecycle: old package digest is invalid\n' >&2
  exit 1
fi
extract_package "$old_package" "$scratch/old-package" old
old_root="$scratch/old-package/hideout"
old_binary="$old_root/bin/hideout"
"$old_binary" package verify "$old_root" \
  >"$evidence/old-package-verify.log"
if ! jq -e \
  --arg version "$old_version" \
  --arg commit "$(jq -er '.current.package.sourceCommit' releases/current.json)" '
    .release.productVersion == $version and
    .source.commit == $commit and
    .source.dirty == false
  ' "$old_root/package-manifest.json" >/dev/null; then
  printf 'package-lifecycle: old extracted package identity is invalid\n' >&2
  exit 1
fi

mkdir -p \
  "$scratch/clean-prefix" \
  "$scratch/clean-store" \
  "$scratch/old-prefix" \
  "$scratch/old-store"
clean_prefix="$(cd "$scratch/clean-prefix" && pwd -P)"
clean_store="$(cd "$scratch/clean-store" && pwd -P)"
old_prefix="$(cd "$scratch/old-prefix" && pwd -P)"
old_store="$(cd "$scratch/old-store" && pwd -P)"
scratch_real="$(cd "$scratch" && pwd -P)"

"$candidate_root/install.sh" \
  --prefix "$clean_prefix" \
  --store "$clean_store" \
  --skip-init >"$evidence/clean-install.log"
grep -q 'package: install ' "$evidence/clean-install.log"
grep -q 'package: init skipped' "$evidence/clean-install.log"
installed_binary="$clean_prefix/bin/hideout"
installed_state="$clean_prefix/share/hideout/package-manifest.json"
"$installed_binary" package verify "$clean_prefix" \
  >"$evidence/clean-installed-verify.log"
"$installed_binary" version --json \
  >"$evidence/clean-installed-version.json"
installed_binary_sha="$(sha256_file "$installed_binary")"
candidate_binary_sha="$(sha256_file "$candidate_binary")"
if [ "$installed_binary_sha" != "$candidate_binary_sha" ] ||
  ! jq -e \
    --arg commit "$candidate_commit" \
    --arg version "$candidate_version" '
      .sourceCommit == $commit and
      .productVersion == $version
    ' "$evidence/clean-installed-version.json" >/dev/null ||
  ! jq -e \
    --arg commit "$candidate_commit" \
    --arg version "$candidate_version" \
    --arg prefix "$clean_prefix" \
    --arg store "$clean_store" '
      .schema == "hideout.package-install-state/v1" and
      .installPrefix == $prefix and
      .storeRoot == $store and
      .package.source.commit == $commit and
      .package.source.dirty == false and
      .package.release.productVersion == $version
    ' "$installed_state" >/dev/null; then
  printf 'package-lifecycle: clean installed identity is invalid\n' >&2
  exit 1
fi
if [ -e "$clean_store/install-state.json" ] ||
  [ -e "$clean_store/profiles" ]; then
  printf 'package-lifecycle: --skip-init created runtime/profile state\n' >&2
  exit 1
fi
cp "$installed_state" "$evidence/clean-install-state.json"

legacy_secret_canary='socks5://migration-user:migration-pass@127.0.0.1:9'
HIDEOUT_SECRET_LOCAL_PROXY="$legacy_secret_canary" \
  "$installed_binary" secret --help \
  >"$evidence/keychain-migration-help.log"
"$installed_binary" help secret \
  >"$evidence/keychain-contextual-help.log"
for required_text in \
  'macOS' \
  'Keychain' \
  'HIDEOUT_SECRET_<REF>' \
  'not imported automatically' \
  'hideout secret set <ref>' \
  'remove the export' \
  'stopping or recreating the VM is not required'; do
  if ! grep -Fqi "$required_text" \
    "$evidence/keychain-migration-help.log" \
    "$evidence/keychain-contextual-help.log"; then
    printf 'package-lifecycle: Keychain guidance is missing: %s\n' \
      "$required_text" >&2
    exit 1
  fi
done
if grep -FRq "$legacy_secret_canary" "$evidence"; then
  printf 'package-lifecycle: legacy secret value entered evidence\n' >&2
  exit 1
fi

printf 'durable-current-marker\n' >"$clean_store/current-marker"
printf 'operator-owned\n' >"$clean_prefix/operator-note.txt"
current_binary_sha="$(sha256_file "$installed_binary")"
"$candidate_root/install.sh" \
  --prefix "$clean_prefix" \
  --store "$clean_store" \
  --skip-init >"$evidence/current-version-reinstall.log"
grep -q 'package: upgrade ' "$evidence/current-version-reinstall.log"
grep -q 'stale=0' "$evidence/current-version-reinstall.log"
if [ "$(sha256_file "$installed_binary")" != "$current_binary_sha" ] ||
  [ ! -f "$clean_store/current-marker" ] ||
  [ ! -f "$clean_prefix/operator-note.txt" ]; then
  printf 'package-lifecycle: same-candidate reinstall changed unrelated state\n' \
    >&2
  exit 1
fi
"$installed_binary" package verify "$clean_prefix" \
  >"$evidence/current-version-reinstall-verify.log"
if ! jq -s '
  length >= 2 and
  .[0].operation == "install" and
  .[-1].operation == "upgrade" and
  all(.[]; .status == "passed")
' "$clean_store/logs/package-audit.jsonl" >/dev/null; then
  printf 'package-lifecycle: reinstall audit sequence is invalid\n' >&2
  exit 1
fi

"$old_root/install.sh" \
  --prefix "$old_prefix" \
  --store "$old_store" \
  --skip-init >"$evidence/old-version-install.log"
grep -q 'package: install ' "$evidence/old-version-install.log"
printf 'legacy-data-must-be-discarded\n' >"$old_store/legacy-marker"
printf 'outside-discard-must-survive\n' >"$scratch_real/outside-discard.txt"
jq -n \
  --arg candidateVersion "$candidate_version" \
  --arg previousVersion "$old_version" '
    {
      schema:"hideout.release-legacy-discard-plan/v1",
      authorized:true,
      scope:"temporary-old-store-only",
      storeBasename:"old-store",
      candidateUpgradeVersion:$candidateVersion,
      previousVersion:$previousVersion,
      preserveOldData:false
    }
  ' >"$evidence/legacy-discard-plan.json"
discard_exact_legacy_store "$old_store" "$scratch_real"
if [ -e "$old_store/legacy-marker" ] ||
  [ ! -f "$scratch_real/outside-discard.txt" ] ||
  [ ! -x "$old_prefix/bin/hideout" ]; then
  printf 'package-lifecycle: legacy data discard escaped exact scope\n' >&2
  exit 1
fi
"$candidate_root/install.sh" \
  --prefix "$old_prefix" \
  --store "$old_store" \
  --skip-init >"$evidence/old-version-upgrade.log"
grep -q 'package: upgrade ' "$evidence/old-version-upgrade.log"
old_upgraded_binary="$old_prefix/bin/hideout"
"$old_upgraded_binary" version --json \
  >"$evidence/old-version-upgraded-version.json"
"$old_upgraded_binary" package verify "$old_prefix" \
  >"$evidence/old-version-upgraded-verify.log"
if ! jq -e \
  --arg commit "$candidate_commit" \
  --arg version "$candidate_version" '
    .sourceCommit == $commit and
    .productVersion == $version
  ' "$evidence/old-version-upgraded-version.json" >/dev/null ||
  [ -e "$old_store/legacy-marker" ] ||
  [ ! -f "$scratch_real/outside-discard.txt" ] ||
  ! jq -e '
    .operation == "upgrade" and
    .status == "passed"
  ' "$old_store/logs/package-audit.jsonl" >/dev/null; then
  printf 'package-lifecycle: old-version upgrade/discard proof failed\n' >&2
  exit 1
fi

cp "$installed_state" "$evidence/pre-uninstall-state.json"
"$installed_binary" package uninstall \
  --prefix "$clean_prefix" \
  --store "$clean_store" \
  --dry-run >"$evidence/uninstall-dry-run.log"
grep -q 'package: uninstall dry-run ' "$evidence/uninstall-dry-run.log"
grep -q 'durableState=preserved' "$evidence/uninstall-dry-run.log"
"$installed_binary" package uninstall \
  --prefix "$clean_prefix" \
  --store "$clean_store" >"$evidence/uninstall.log"
grep -q 'package: uninstall ' "$evidence/uninstall.log"
grep -q 'durableState=preserved' "$evidence/uninstall.log"
while IFS= read -r installed_path; do
  safe_relative_path "$installed_path" || {
    printf 'package-lifecycle: unsafe installed-state path: %s\n' \
      "$installed_path" >&2
    exit 1
  }
  if [ -e "$clean_prefix/$installed_path" ]; then
    printf 'package-lifecycle: package file survived uninstall: %s\n' \
      "$installed_path" >&2
    exit 1
  fi
done < <(jq -r '.files[].path' "$evidence/pre-uninstall-state.json")
while IFS= read -r installed_dir; do
  case "$installed_dir" in
    "" | . | bin)
      continue
      ;;
  esac
  safe_relative_path "$installed_dir" || {
    printf 'package-lifecycle: unsafe installed-state directory: %s\n' \
      "$installed_dir" >&2
    exit 1
  }
  if [ -d "$clean_prefix/$installed_dir" ]; then
    printf 'package-lifecycle: package directory survived uninstall: %s\n' \
      "$installed_dir" >&2
    exit 1
  fi
done < <(jq -r '.directories[]' "$evidence/pre-uninstall-state.json")
if [ -e "$clean_prefix/share/hideout/package-manifest.json" ] ||
  [ ! -f "$clean_prefix/operator-note.txt" ] ||
  [ ! -f "$clean_store/current-marker" ] ||
  ! jq -s '
    .[-1].operation == "uninstall" and
    .[-1].status == "passed" and
    .[-1].durableAction == "preserved" and
    .[-1].purge == false
  ' "$clean_store/logs/package-audit.jsonl" >/dev/null; then
  printf 'package-lifecycle: normal uninstall absence/preservation failed\n' \
    >&2
  exit 1
fi
find "$clean_prefix" -type f | LC_ALL=C sort \
  >"$evidence/post-uninstall-files.txt"
if grep -Eq '/(bin/hideout|share/hideout/)' \
  "$evidence/post-uninstall-files.txt"; then
  printf 'package-lifecycle: Hideout package residue survived uninstall\n' \
    >&2
  exit 1
fi

candidate_archive_final_sha="$(sha256_file "$candidate_archive")"
if [ "$candidate_archive_final_sha" != "$candidate_archive_initial_sha" ]; then
  printf 'package-lifecycle: consumed candidate archive changed\n' >&2
  exit 1
fi
if ! validate_source_identity \
  "$repo_root" \
  "$candidate_commit" \
  "$candidate_tree"; then
  printf 'package-lifecycle: source changed during lifecycle validation\n' >&2
  exit 1
fi
if grep -FRq "$legacy_secret_canary" "$evidence"; then
  printf 'package-lifecycle: secret value entered retained evidence\n' >&2
  exit 1
fi

cp "$current_receipt" "$evidence/old-release-receipt.json"
cp "$clean_store/logs/package-audit.jsonl" \
  "$evidence/current-lifecycle-audit.jsonl"
cp "$old_store/logs/package-audit.jsonl" \
  "$evidence/old-upgrade-audit.jsonl"
find "$evidence" -type f -exec chmod 0600 {} +

run_id="run-$(date -u +'%Y%m%dT%H%M%SZ')-$$"
if [ -L "$out" ]; then
  printf 'package-lifecycle: evidence root must not be a symlink\n' >&2
  exit 1
fi
mkdir -p "$out"
out="$(cd "$out" && pwd -P)"
chmod 0700 "$out"
run_dir="$out/$run_id"
if [ -e "$run_dir" ]; then
  printf 'package-lifecycle: evidence run already exists\n' >&2
  exit 1
fi
mkdir "$run_dir"
cp -R "$evidence/." "$run_dir/"
find "$run_dir" -type d -exec chmod 0700 {} +
find "$run_dir" -type f -exec chmod 0600 {} +

artifact_lines="$scratch/artifacts.jsonl"
: >"$artifact_lines"
while IFS= read -r evidence_file; do
  relative="${evidence_file#"$out"/}"
  jq -nc \
    --arg path "$relative" \
    --arg sha256 "$(sha256_file "$evidence_file")" \
    --argjson bytes "$(file_bytes "$evidence_file")" '
      {path:$path,sha256:$sha256,bytes:$bytes,mode:"0600"}
    ' >>"$artifact_lines"
done < <(find "$run_dir" -type f | LC_ALL=C sort)
artifacts="$scratch/artifacts.json"
jq -s . "$artifact_lines" >"$artifacts"

summary="$run_dir/summary.json"
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$candidate_commit" \
  --arg tree "$candidate_tree" \
  --arg version "$candidate_version" \
  --arg packageSummarySHA256 "$candidate_summary_sha" \
  --arg archive "$candidate_archive_rel" \
  --arg archiveSHA256 "$candidate_archive_sha" \
  --arg oldVersion "$old_version" \
  --arg oldTag "$old_tag" \
  --arg oldArchiveSHA256 "$old_archive_sha" \
  --argjson artifacts "$(cat "$artifacts")" '
    {
      schema:"hideout.release-package-lifecycle/v1",
      generatedAt:$generatedAt,
      result:"passed",
      sourceCandidate:{
        commit:$commit,
        tree:$tree,
        dirty:false,
        packageSummarySHA256:$packageSummarySHA256
      },
      candidate:{
        version:$version,
        archive:$archive,
        archiveSHA256:$archiveSHA256,
        consumedWithoutRebuild:true,
        acceptance:true
      },
      oldRelease:{
        version:$oldVersion,
        tag:$oldTag,
        archiveSHA256:$oldArchiveSHA256,
        verifiedPublishedBytes:true,
        oldDataPolicy:"explicitly-discarded-in-exact-temporary-store"
      },
      checks:{
        cleanInstall:true,
        keychainMigrationGuidance:true,
        currentVersionReinstall:true,
        oldVersionUpgrade:true,
        legacyDataDiscarded:true,
        legacyDiscardExactScope:true,
        normalUninstall:true,
        packageAbsenceAfterUninstall:true,
        durableStatePreservedByNormalUninstall:true,
        unrelatedFilesPreserved:true,
        noSecretValueInEvidence:true
      },
      publicationStatus:"local-only",
      artifacts:$artifacts,
      limitations:[
        "The legacy-data discard is exercised only against an exact private temporary store.",
        "This gate checks Keychain migration guidance without importing a legacy environment value or retaining a credential."
      ]
    }
  ' >"$summary"
chmod 0600 "$summary"
validate_lifecycle_summary \
  "$summary" \
  "$candidate_commit" \
  "$candidate_archive_sha" || {
  printf 'package-lifecycle: summary semantic validation failed\n' >&2
  exit 1
}
validate_artifact_manifest "$out" "$run_dir" "$summary"

summary_sha="$(sha256_file "$summary")"
pointer_tmp="$out/.result.$$.json"
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$candidate_commit" \
  --arg tree "$candidate_tree" \
  --arg run "$run_id" \
  --arg summary "$run_id/summary.json" \
  --arg summarySHA256 "$summary_sha" \
  --arg archive "$candidate_archive_rel" \
  --arg archiveSHA256 "$candidate_archive_sha" '
    {
      schema:"hideout.release-package-lifecycle-pointer/v1",
      generatedAt:$generatedAt,
      sourceCandidate:{commit:$commit,tree:$tree,dirty:false},
      result:"passed",
      run:$run,
      summary:$summary,
      summarySHA256:$summarySHA256,
      candidateArchive:$archive,
      candidateArchiveSHA256:$archiveSHA256,
      candidateAcceptance:true,
      publicationStatus:"local-only"
    }
  ' >"$pointer_tmp"
chmod 0600 "$pointer_tmp"
mv "$pointer_tmp" "$out/result.json"

printf \
  'package-lifecycle: passed candidate=%s summary=%s\n' \
  "$candidate_archive_sha" \
  "$summary"
