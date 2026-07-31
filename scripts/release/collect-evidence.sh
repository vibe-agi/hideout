#!/usr/bin/env bash
set -euo pipefail

repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd -P)"
cd "$repo_root"
# shellcheck source=scripts/lib/gate-result.sh
. "$repo_root/scripts/lib/gate-result.sh"
gate_completed=0

umask 077
export LC_ALL=C
export TZ=UTC

artifact_root="${HIDEOUT_045_ARTIFACT_ROOT:-$repo_root/.artifacts/045}"
output="$artifact_root/evidence.json"
require_closure=0
preflight_only=0
tmp_base="${TMPDIR:-/tmp}"
tmp_base="${tmp_base%/}"

usage() {
  printf '%s\n' \
    "Usage: scripts/release/collect-evidence.sh [--preflight]" \
    "                                            [--out FILE]" \
    "                                            [--require-closure]" \
    "" \
    "Collects one digest-signed local Feature 045 evidence manifest from an" \
    "exact clean commit and the exact package/gate outputs for that commit." \
    "The collector independently extracts and verifies every packaged file." \
    "" \
    "--require-closure additionally requires exact local-install and" \
    "publication-absence receipts. This command never commits, tags, pushes," \
    "creates a remote release, changes Homebrew, or publishes package bytes."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --preflight)
      preflight_only=1
      shift
      ;;
    --out)
      if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
        printf 'collect-evidence: --out requires a file\n' >&2
        exit 2
      fi
      output="$2"
      shift 2
      ;;
    --require-closure)
      require_closure=1
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'collect-evidence: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

fail() {
  printf 'collect-evidence: %s\n' "$1" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    printf 'collect-evidence: missing required command: %s\n' "$1" >&2
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

normalized_mode() {
  local raw
  raw="$(file_mode "$1")"
  case "$raw" in
    [0-7][0-7][0-7])
      printf '0%s\n' "$raw"
      ;;
    [0-7][0-7][0-7][0-7])
      printf '%s\n' "$raw"
      ;;
    *)
      return 1
      ;;
  esac
}

safe_relative_path() {
  case "$1" in
    "" | /* | . | .. | ../* | */.. | */../* | *$'\n'* | *$'\r'* | *$'\t'*)
      return 1
      ;;
  esac
}

cleanup_tree() {
  local target="$1" prefix="$2"
  if [ -z "$target" ] || [ ! -e "$target" ]; then
    return
  fi
  case "$target" in
    "$tmp_base"/"$prefix".*)
      [ ! -L "$target" ] ||
        fail "refusing symlink cleanup target: $target"
      find "$target" -depth -delete
      ;;
    *)
      printf 'collect-evidence: refusing unexpected cleanup target: %s\n' \
        "$target" >&2
      return 1
      ;;
  esac
}

verify_sha256() {
  local evidence_file="$1" expected="$2"
  [ -f "$evidence_file" ] &&
    [ ! -L "$evidence_file" ] &&
    [[ "$expected" =~ ^[a-f0-9]{64}$ ]] &&
    [ "$(sha256_file "$evidence_file")" = "$expected" ]
}

run_preflight() {
  local fixture digest
  preflight_root="$(
    mktemp -d "$tmp_base/hideout-collect-evidence-preflight.XXXXXX"
  )"
  # Invoked indirectly by the EXIT trap.
  # shellcheck disable=SC2329
  cleanup_preflight() {
    local exit_status=$?
    cleanup_tree \
      "${preflight_root:-}" \
      "hideout-collect-evidence-preflight"
    if [ "$exit_status" -eq 0 ]; then
      gate_require_completion "collect-evidence-preflight"
    fi
  }
  trap cleanup_preflight EXIT

  fixture="$preflight_root/evidence.json"
  printf '%s\n' '{"schema":"hideout.release-evidence/v1"}' >"$fixture"
  chmod 0600 "$fixture"
  digest="$(sha256_file "$fixture")"
  verify_sha256 "$fixture" "$digest" ||
    fail "valid detached digest fixture was rejected"
  printf '%s\n' '{"mutation":true}' >>"$fixture"
  if verify_sha256 "$fixture" "$digest"; then
    fail "mutated evidence fixture retained detached digest validity"
  fi
  safe_relative_path "run-1/summary.json" ||
    fail "safe evidence path was rejected"
  if safe_relative_path "../summary.json" ||
    safe_relative_path "/tmp/summary.json" ||
    safe_relative_path $'run-1/\tsummary.json'; then
    fail "unsafe evidence path was accepted"
  fi
  gate_completed=1
  printf 'collect-evidence: preflight=passed\n'
}

if [ "$preflight_only" -eq 1 ]; then
  run_preflight
  exit 0
fi

for required_command in git go jq tar find sort comm awk sed grep stat cmp; do
  require_command "$required_command" || exit 1
done

[ "$(uname -s)" = "Darwin" ] &&
  [ "$(uname -m)" = "arm64" ] ||
  fail "full collection requires Darwin/arm64"

source_status="$(git status --porcelain=v1 --untracked-files=all)"
[ -z "$source_status" ] ||
  fail "exact evidence collection requires a completely clean source tree"

source_commit="$(git rev-parse HEAD)"
source_tree="$(git rev-parse 'HEAD^{tree}')"
source_epoch="$(git show -s --format=%ct HEAD)"
source_committed_at="$(git show -s --format=%cI HEAD)"
[[ "$source_commit" =~ ^[a-f0-9]{40}$ ]] ||
  fail "source commit identity is invalid"
[[ "$source_tree" =~ ^[a-f0-9]{40}$ ]] ||
  fail "source tree identity is invalid"
[[ "$source_epoch" =~ ^[0-9]+$ ]] ||
  fail "source commit timestamp is invalid"

if [ -L "$artifact_root" ]; then
  fail "artifact root must not be a symlink"
fi
[ -d "$artifact_root" ] ||
  fail "artifact root does not exist"
artifact_root="$(CDPATH='' cd -- "$artifact_root" && pwd -P)"
case "$artifact_root" in
  "$repo_root"/.artifacts/045 | "$repo_root"/.artifacts/045/*)
    ;;
  *)
    fail "artifact root must remain under .artifacts/045"
    ;;
esac

output_parent="$(dirname -- "$output")"
mkdir -p "$output_parent"
if [ -L "$output_parent" ]; then
  fail "evidence output parent must not be a symlink"
fi
output_parent="$(CDPATH='' cd -- "$output_parent" && pwd -P)"
output="$output_parent/$(basename -- "$output")"
case "$output" in
  "$artifact_root"/*)
    ;;
  *)
    fail "evidence output must remain inside the artifact root"
    ;;
esac
chmod 0700 "$output_parent"

scratch="$(
  mktemp -d "$tmp_base/hideout-collect-evidence.XXXXXX"
)"
cleanup() {
  local exit_status=$?
  cleanup_tree "${scratch:-}" "hideout-collect-evidence"
  if [ "$exit_status" -eq 0 ]; then
    gate_require_completion "collect-evidence"
  fi
}
trap cleanup EXIT

require_private_evidence_file() {
  local evidence_file="$1" resolved_dir
  [ -f "$evidence_file" ] &&
    [ ! -L "$evidence_file" ] ||
    fail "missing or unsafe evidence file: $evidence_file"
  resolved_dir="$(
    CDPATH='' cd -- "$(dirname -- "$evidence_file")" && pwd -P
  )"
  case "$resolved_dir/$(basename -- "$evidence_file")" in
    "$artifact_root"/*)
      ;;
    *)
      fail "evidence file escapes artifact root: $evidence_file"
      ;;
  esac
  [ "$(normalized_mode "$evidence_file")" = "0600" ] ||
    fail "evidence file mode is not 0600: $evidence_file"
}

artifact_ref() {
  local evidence_file="$1" relative
  [ -f "$evidence_file" ] &&
    [ ! -L "$evidence_file" ] ||
    fail "cannot reference missing or unsafe file: $evidence_file"
  relative="${evidence_file#"$repo_root"/}"
  [ "$relative" != "$evidence_file" ] ||
    fail "referenced file is outside repository: $evidence_file"
  jq -nc \
    --arg path "$relative" \
    --arg sha256 "$(sha256_file "$evidence_file")" \
    --argjson bytes "$(file_bytes "$evidence_file")" \
    --arg mode "$(normalized_mode "$evidence_file")" \
    '{path:$path,sha256:$sha256,bytes:$bytes,mode:$mode}'
}

validate_fresh_json() {
  local evidence_file="$1"
  jq -e \
    --argjson sourceEpoch "$source_epoch" '
      (.generatedAt | type) == "string" and
      ((.generatedAt | fromdateiso8601) >= $sourceEpoch)
    ' "$evidence_file" >/dev/null ||
    fail "evidence predates the candidate commit: $evidence_file"
}

validate_closure_receipt() {
  local receipt="$1" schema="$2" receipt_root
  local path expected_sha expected_bytes evidence_file
  go run ./cmd/hideout-schema-validate \
    "$schema" "$receipt" >/dev/null ||
    fail "closure receipt failed JSON schema validation: $receipt"
  jq -e '
    ([.artifacts[].path] | length) > 0 and
    ([.artifacts[].path] | unique | length) ==
      ([.artifacts[].path] | length)
  ' "$receipt" >/dev/null ||
    fail "closure receipt artifact paths are empty or duplicated: $receipt"
  receipt_root="$(
    CDPATH='' cd -- "$(dirname -- "$receipt")" && pwd -P
  )"
  while IFS=$'\t' read -r path expected_sha expected_bytes; do
    safe_relative_path "$path" ||
      fail "closure receipt contains an unsafe artifact path: $path"
    evidence_file="$receipt_root/$path"
    require_private_evidence_file "$evidence_file"
    verify_sha256 "$evidence_file" "$expected_sha" ||
      fail "closure receipt artifact digest is invalid: $path"
    [ "$(file_bytes "$evidence_file")" = "$expected_bytes" ] ||
      fail "closure receipt artifact byte count is invalid: $path"
  done < <(
    jq -r \
      '.artifacts[] | [.path,.sha256,(.bytes|tostring)] | @tsv' \
      "$receipt"
  )
}

validate_installed_candidate() {
  local receipt="$1" receipt_prefix receipt_store expected_prefix expected_store
  local installed expected_binary_sha packaged_binary_sha environment_count
  require_command brew ||
    fail "installed-candidate verification requires Homebrew"
  receipt_prefix="$(jq -er '.installation.prefix' "$receipt")"
  receipt_store="$(jq -er '.installation.store' "$receipt")"
  expected_prefix="$(brew --prefix)"
  expected_prefix="$(
    CDPATH='' cd -- "$expected_prefix" && pwd -P
  )"
  expected_store="$(
    CDPATH='' cd -- "$HOME" && printf '%s/.hideout\n' "$(pwd -P)"
  )"
  if [ "$receipt_prefix" != "$expected_prefix" ] ||
    [ "$receipt_store" != "$expected_store" ]; then
    fail "local-install receipt does not target this exact installation/store"
  fi
  if [ ! -d "$receipt_prefix" ] || [ -L "$receipt_prefix" ] ||
    [ ! -d "$receipt_store" ] || [ -L "$receipt_store" ]; then
    fail "installed candidate prefix/store is missing or unsafe"
  fi
  [ "$(normalized_mode "$receipt_store")" = "0700" ] ||
    fail "installed candidate store is not private mode 0700"

  installed="$receipt_prefix/bin/hideout"
  [ -f "$installed" ] && [ ! -L "$installed" ] &&
    [ -x "$installed" ] ||
    fail "installed candidate binary is missing or unsafe"
  expected_binary_sha="$(
    jq -er '.candidate.installedBinarySHA256' "$receipt"
  )"
  packaged_binary_sha="$(sha256_file "$package_root/bin/hideout")"
  if [ "$expected_binary_sha" != "$packaged_binary_sha" ] ||
    ! verify_sha256 "$installed" "$packaged_binary_sha"; then
    fail "installed binary does not match the exact packaged candidate"
  fi

  "$installed" version --json \
    >"$scratch/installed-candidate-version.json"
  jq -e \
    --arg version "$candidate_version" \
    --arg commit "$source_commit" '
      .schema == "hideout.binary-identity/v1" and
      .productVersion == $version and
      .sourceCommit == $commit and
      .hostOS == "darwin" and
      .hostArch == "arm64"
    ' "$scratch/installed-candidate-version.json" >/dev/null ||
    fail "installed candidate binary identity is invalid"
  "$installed" package verify "$receipt_prefix" \
    >"$scratch/installed-candidate-package-verify.out" \
    2>"$scratch/installed-candidate-package-verify.err" ||
    fail "installed candidate package verification failed"
  if "$installed" daemon status \
    >"$scratch/installed-candidate-daemon.out" \
    2>"$scratch/installed-candidate-daemon.err"; then
    fail "installed candidate daemon is not in the required stopped state"
  fi
  "$installed" env list \
    >"$scratch/installed-candidate-environments.out"
  environment_count="$(
    awk -F '\t' \
      '$NF ~ /^env_[A-Za-z0-9_-]+$/ {count++} END {print count+0}' \
      "$scratch/installed-candidate-environments.out"
  )"
  [ "$environment_count" -eq 0 ] ||
    fail "installed candidate still retains an environment"
  "$installed" show connection \
    >"$scratch/installed-candidate-connection.out" \
    2>"$scratch/installed-candidate-connection.err"
  grep -Fqi 'direct' "$scratch/installed-candidate-connection.out" ||
    fail "installed candidate final profile is not direct-network"
}

validate_simple_summary() {
  local evidence_file="$1" expected_schema="$2"
  require_private_evidence_file "$evidence_file"
  jq -e \
    --arg schema "$expected_schema" \
    --arg commit "$source_commit" '
      .schema == $schema and
      .result == "passed" and
      .source.commit == $commit and
      .source.dirty == false
    ' "$evidence_file" >/dev/null ||
    fail "simple gate summary is not an exact passing candidate result: $evidence_file"
  validate_fresh_json "$evidence_file"
}

resolved_summary=""
resolve_source_pointer() {
  local pointer="$1" pointer_schema="$2" summary_schema="$3"
  local summary_relative summary_sha pointer_dir
  require_private_evidence_file "$pointer"
  jq -e \
    --arg schema "$pointer_schema" \
    --arg commit "$source_commit" '
      .schema == $schema and
      .result == "passed" and
      .candidateAcceptance == true and
      .source.commit == $commit and
      .source.dirty == false
    ' "$pointer" >/dev/null ||
    fail "candidate gate pointer is not exact and accepted: $pointer"
  validate_fresh_json "$pointer"
  summary_relative="$(jq -er '.summary' "$pointer")"
  summary_sha="$(jq -er '.summarySHA256' "$pointer")"
  safe_relative_path "$summary_relative" ||
    fail "candidate gate pointer contains an unsafe summary path"
  pointer_dir="$(
    CDPATH='' cd -- "$(dirname -- "$pointer")" && pwd -P
  )"
  resolved_summary="$pointer_dir/$summary_relative"
  require_private_evidence_file "$resolved_summary"
  verify_sha256 "$resolved_summary" "$summary_sha" ||
    fail "candidate gate summary digest does not match pointer: $pointer"
  jq -e \
    --arg schema "$summary_schema" \
    --arg commit "$source_commit" '
      .schema == $schema and
      .result == "passed" and
      .candidateAcceptance == true and
      .source.commit == $commit and
      .source.dirty == false
    ' "$resolved_summary" >/dev/null ||
    fail "candidate gate summary is not exact and accepted: $resolved_summary"
  validate_fresh_json "$resolved_summary"
}

package_pointer="$artifact_root/package/result.json"
package_lifecycle_pointer="$artifact_root/package-lifecycle/result.json"
formal_summary="$artifact_root/formal/summary.json"
local_summary="$artifact_root/local/summary.json"
dependency_summary="$artifact_root/dependencies/summary.json"
component_summary="$artifact_root/package-components/summary.json"
recovery_summary="$artifact_root/recovery/summary.json"
privacy_pointer="$artifact_root/privacy/result.json"
ui_pointer="$artifact_root/ui/result.json"
performance_pointer="$artifact_root/performance/result.json"
lima_pointer="$artifact_root/lima/result.json"
review_file="$repo_root/docs/release/045-code-review.md"
claim_matrix="$repo_root/docs/release/045-claim-matrix.md"
formal_inventory_source="$repo_root/formal/inventory.json"

validate_simple_summary "$formal_summary" "hideout.formal-gate/v1"
jq -e '
  .candidateAcceptance == false and
  .inventory.configurationCount == 12 and
  .inventory.moduleCount == 10 and
  .inventory.invariantCount == 72 and
  .inventory.propertyCount == 18 and
  .inventory.goTestCount == 12 and
  all(.configurations[]; .result == "passed") and
  .goRefinement.result == "passed" and
  all(.goRefinement.tests[]; .result == "passed") and
  all(.negativeJudgeProofs[]; .result == "killed")
' "$formal_summary" >/dev/null ||
  fail "formal gate summary is incomplete"

validate_simple_summary "$local_summary" \
  "hideout.local-release-candidate/v1"
jq -e '
  .candidateAcceptance == false and
  all(.lanes[]; .result == "passed") and
  .statistics.failedLanes == 0
' "$local_summary" >/dev/null ||
  fail "local release aggregate is incomplete"

validate_simple_summary "$dependency_summary" \
  "hideout.dependencies-gate/v1"
jq -e '
  .advisories.reachableImportedPackageFindings == 0 and
  all(.checks[]; . == "passed")
' "$dependency_summary" >/dev/null ||
  fail "dependency/advisory evidence is incomplete"

validate_simple_summary "$component_summary" \
  "hideout.package-components-gate/v1"
jq -e '
  .candidateAcceptance == false and
  all(.checks[]; . == "passed")
' "$component_summary" >/dev/null ||
  fail "package-component evidence is incomplete"

validate_simple_summary "$recovery_summary" \
  "hideout.recovery-gate-evidence/v1"
jq -e '
  .crashMatrix.points == 16 and
  all(.mutationProofs[]; .result == "killed")
' "$recovery_summary" >/dev/null ||
  fail "recovery evidence is incomplete"

resolve_source_pointer \
  "$privacy_pointer" \
  "hideout.release-candidate-privacy-pointer/v1" \
  "hideout.release-candidate-privacy-evidence/v1"
privacy_summary="$resolved_summary"

resolve_source_pointer \
  "$ui_pointer" \
  "hideout.release-candidate-ui-pointer/v1" \
  "hideout.release-candidate-ui-evidence/v1"
ui_summary="$resolved_summary"

resolve_source_pointer \
  "$performance_pointer" \
  "hideout.release-candidate-performance-pointer/v1" \
  "hideout.release-candidate-performance/v1"
performance_summary="$resolved_summary"

resolve_source_pointer \
  "$lima_pointer" \
  "hideout.release-candidate-lima-pointer/v1" \
  "hideout.release-candidate-lima-evidence/v1"
lima_summary="$resolved_summary"

require_private_evidence_file "$package_pointer"
jq -e \
  --arg commit "$source_commit" \
  --arg tree "$source_tree" '
    .schema == "hideout.release-package-candidate-pointer/v1" and
    .result == "passed" and
    .candidateAcceptance == true and
    .publicationStatus == "local-only" and
    .source == {commit:$commit,tree:$tree,dirty:false}
  ' "$package_pointer" >/dev/null ||
  fail "package pointer is not the exact accepted local candidate"
validate_fresh_json "$package_pointer"

package_summary_relative="$(jq -er '.summary' "$package_pointer")"
package_summary_sha="$(jq -er '.summarySHA256' "$package_pointer")"
safe_relative_path "$package_summary_relative" ||
  fail "package pointer contains an unsafe summary path"
package_summary="$artifact_root/package/$package_summary_relative"
require_private_evidence_file "$package_summary"
verify_sha256 "$package_summary" "$package_summary_sha" ||
  fail "package summary digest does not match its pointer"
jq -e \
  --arg commit "$source_commit" \
  --arg tree "$source_tree" '
    .schema == "hideout.release-package-candidate/v1" and
    .result == "passed" and
    .source.commit == $commit and
    .source.tree == $tree and
    .source.dirty == false and
    .candidate.acceptance == true and
    .candidate.publicationStatus == "local-only" and
    .reproducibility.archiveBytesIdentical == true and
    .reproducibility.packageManifestBytesIdentical == true and
    .reproducibility.packageTreeInventoryIdentical == true and
    all(.validation[]; . == true)
  ' "$package_summary" >/dev/null ||
  fail "package summary is not an exact reproducible local candidate"
validate_fresh_json "$package_summary"

candidate_version="$(jq -er '.candidate.version' "$package_summary")"
candidate_archive_relative="$(jq -er '.candidate.archive' "$package_summary")"
candidate_archive_sha="$(jq -er '.candidate.archiveSHA256' "$package_summary")"
package_manifest_relative="$(jq -er '.candidate.packageManifest' "$package_summary")"
package_manifest_sha="$(
  jq -er '.candidate.packageManifestSHA256' "$package_summary"
)"
safe_relative_path "$candidate_archive_relative" ||
  fail "package summary contains an unsafe archive path"
safe_relative_path "$package_manifest_relative" ||
  fail "package summary contains an unsafe manifest path"
candidate_archive="$artifact_root/package/$candidate_archive_relative"
package_manifest_evidence="$artifact_root/package/$package_manifest_relative"
require_private_evidence_file "$candidate_archive"
require_private_evidence_file "$package_manifest_evidence"
verify_sha256 "$candidate_archive" "$candidate_archive_sha" ||
  fail "candidate archive digest does not match package summary"
verify_sha256 "$package_manifest_evidence" "$package_manifest_sha" ||
  fail "package manifest digest does not match package summary"

require_private_evidence_file "$package_lifecycle_pointer"
jq -e \
  --arg commit "$source_commit" \
  --arg tree "$source_tree" \
  --arg archiveSHA256 "$candidate_archive_sha" '
    .schema == "hideout.release-package-lifecycle-pointer/v1" and
    .result == "passed" and
    .candidateAcceptance == true and
    .publicationStatus == "local-only" and
    .sourceCandidate == {commit:$commit,tree:$tree,dirty:false} and
    .candidateArchiveSHA256 == $archiveSHA256
  ' "$package_lifecycle_pointer" >/dev/null ||
  fail "package lifecycle pointer is not bound to the exact candidate"
validate_fresh_json "$package_lifecycle_pointer"
lifecycle_summary_relative="$(
  jq -er '.summary' "$package_lifecycle_pointer"
)"
lifecycle_summary_sha="$(
  jq -er '.summarySHA256' "$package_lifecycle_pointer"
)"
safe_relative_path "$lifecycle_summary_relative" ||
  fail "package lifecycle pointer contains an unsafe summary path"
lifecycle_summary="$artifact_root/package-lifecycle/$lifecycle_summary_relative"
require_private_evidence_file "$lifecycle_summary"
verify_sha256 "$lifecycle_summary" "$lifecycle_summary_sha" ||
  fail "package lifecycle summary digest does not match pointer"
jq -e \
  --arg commit "$source_commit" \
  --arg tree "$source_tree" \
  --arg version "$candidate_version" \
  --arg archiveSHA256 "$candidate_archive_sha" '
    .schema == "hideout.release-package-lifecycle/v1" and
    .result == "passed" and
    .sourceCandidate.commit == $commit and
    .sourceCandidate.tree == $tree and
    .sourceCandidate.dirty == false and
    .candidate.version == $version and
    .candidate.archiveSHA256 == $archiveSHA256 and
    .candidate.consumedWithoutRebuild == true and
    .candidate.acceptance == true and
    .publicationStatus == "local-only" and
    all(.checks[]; . == true)
  ' "$lifecycle_summary" >/dev/null ||
  fail "package lifecycle summary is incomplete or mismatched"
validate_fresh_json "$lifecycle_summary"

tar -tzf "$candidate_archive" >"$scratch/archive-entries.txt"
[ -s "$scratch/archive-entries.txt" ] ||
  fail "candidate archive is empty"
while IFS= read -r archive_entry; do
  case "$archive_entry" in
    hideout | hideout/ | hideout/*)
      ;;
    *)
      fail "candidate archive contains an entry outside hideout/: $archive_entry"
      ;;
  esac
  case "$archive_entry" in
    /* | *'/../'* | ../* | */.. | *$'\n'* | *$'\r'* | *$'\t'*)
      fail "candidate archive contains an unsafe entry: $archive_entry"
      ;;
  esac
done <"$scratch/archive-entries.txt"

mkdir "$scratch/extracted"
tar -xzf "$candidate_archive" -C "$scratch/extracted"
package_root="$scratch/extracted/hideout"
[ -d "$package_root" ] &&
  [ ! -L "$package_root" ] ||
  fail "candidate archive lacks one safe hideout root"
if find "$package_root" -type l -print -quit | grep -q . ||
  find "$package_root" ! -type f ! -type d -print -quit | grep -q .; then
  fail "candidate package contains a symlink or special file"
fi
cmp "$package_manifest_evidence" "$package_root/package-manifest.json" \
  >/dev/null ||
  fail "archive package manifest differs from retained package manifest"
jq -e \
  --arg commit "$source_commit" \
  --arg version "$candidate_version" '
    .schema == "hideout.package-manifest/v1" and
    .release.productVersion == $version and
    .release.tag == ("v" + $version) and
    .source.commit == $commit and
    .source.dirty == false and
    .signingSummary.mode == "developer-preview-unsigned" and
    ([.files[].path] == ([.files[].path] | sort)) and
    ([.files[].path] | unique | length) == (.files | length)
  ' "$package_root/package-manifest.json" >/dev/null ||
  fail "retained package manifest identity is invalid"

jq -r '.files[].path' "$package_root/package-manifest.json" \
  >"$scratch/manifest-files.txt"
(
  cd "$package_root"
  find . -type f ! -name package-manifest.json -print |
    sed 's#^\./##' |
    LC_ALL=C sort
) >"$scratch/package-files.txt"
if [ -n "$(
  comm -3 "$scratch/manifest-files.txt" "$scratch/package-files.txt"
)" ]; then
  fail "candidate package file set differs from package manifest"
fi

: >"$scratch/package-files.jsonl"
while IFS= read -r package_entry; do
  package_path="$(jq -er '.path' <<<"$package_entry")"
  package_kind="$(jq -er '.kind' <<<"$package_entry")"
  package_sha="$(jq -er '.sha256' <<<"$package_entry")"
  package_executable="$(jq -er '.executable' <<<"$package_entry")"
  safe_relative_path "$package_path" ||
    fail "package manifest contains an unsafe path: $package_path"
  packaged_file="$package_root/$package_path"
  [ -f "$packaged_file" ] &&
    [ ! -L "$packaged_file" ] ||
    fail "package manifest file is missing or unsafe: $package_path"
  verify_sha256 "$packaged_file" "$package_sha" ||
    fail "package file digest mismatch: $package_path"
  if [ "$package_executable" = "true" ]; then
    [ -x "$packaged_file" ] ||
      fail "declared executable is not executable: $package_path"
  elif [ -x "$packaged_file" ]; then
    fail "undeclared executable bit is present: $package_path"
  fi
  jq -nc \
    --arg path "$package_path" \
    --arg kind "$package_kind" \
    --arg sha256 "$package_sha" \
    --argjson bytes "$(file_bytes "$packaged_file")" \
    --arg mode "$(normalized_mode "$packaged_file")" \
    --argjson executable "$package_executable" '
      {
        path:$path,
        kind:$kind,
        sha256:$sha256,
        bytes:$bytes,
        mode:$mode,
        executable:$executable
      }
    ' >>"$scratch/package-files.jsonl"
done < <(jq -c '.files[]' "$package_root/package-manifest.json")
jq -s . "$scratch/package-files.jsonl" >"$scratch/package-files.json"

package_ref() {
  local relative_path="$1"
  jq -e -c \
    --arg path "$relative_path" '
      .[] | select(.path == $path)
    ' "$scratch/package-files.json"
}

jq '
  [
    .[] |
    select(.kind == "helper" or .kind == "helper-manifest")
  ]
' "$scratch/package-files.json" >"$scratch/helpers.json"
[ "$(jq '[.[] | select(.kind == "helper-manifest")] | length' \
  "$scratch/helpers.json")" -eq 6 ] ||
  fail "candidate helper manifest count is not six"
while IFS= read -r helper_manifest_relative; do
  helper_manifest="$package_root/$helper_manifest_relative"
  helper_binary_relative="${helper_manifest_relative%.manifest.json}"
  helper_binary="$package_root/$helper_binary_relative"
  [ -f "$helper_binary" ] &&
    [ ! -L "$helper_binary" ] ||
    fail "helper manifest lacks its binary: $helper_manifest_relative"
  jq -e \
    --arg artifact "$(basename -- "$helper_binary_relative")" \
    --arg sha256 "$(sha256_file "$helper_binary")" '
      .version == "hideout.helper-manifest/v1" and
      .artifact == $artifact and
      .sha256 == $sha256 and
      .packageOwned == true
    ' "$helper_manifest" >/dev/null ||
    fail "helper manifest binding is invalid: $helper_manifest_relative"
done < <(
  jq -r \
    '.[] | select(.kind == "helper-manifest") | .path' \
    "$scratch/helpers.json"
)

browser_manifest="$package_root/runtime/browser-console.assets.json"
browser_container="$package_root/bin/hideout"
[ -f "$browser_manifest" ] &&
  [ -f "$browser_container" ] ||
  fail "candidate lacks embedded browser evidence"
"$browser_container" package embedded-assets \
  >"$scratch/browser-live.json"
jq -S . "$browser_manifest" >"$scratch/browser-packaged.sorted.json"
jq -S . "$scratch/browser-live.json" >"$scratch/browser-live.sorted.json"
cmp "$scratch/browser-packaged.sorted.json" \
  "$scratch/browser-live.sorted.json" >/dev/null ||
  fail "embedded browser inventory differs from the packaged binary"
jq -e \
  --arg containerSHA256 "$(sha256_file "$browser_container")" '
    .schema == "hideout.embedded-asset-manifest/v1" and
    .id == "browser-console" and
    .container == "bin/hideout" and
    .containerSHA256 == $containerSHA256 and
    (.assets | length) == 8 and
    ([.assets[].path] | unique | length) == 8 and
    all(.assets[]; .sha256 | test("^[a-f0-9]{64}$"))
  ' "$browser_manifest" >/dev/null ||
  fail "embedded browser manifest is incomplete"

runtime_catalog="$package_root/runtime/catalog.json"
runtime_contract="$package_root/runtime/contract.json"
[ -f "$runtime_catalog" ] &&
  [ -f "$runtime_contract" ] ||
  fail "candidate lacks runtime catalog or contract"
jq -e \
  --arg catalogSHA256 "$(sha256_file "$runtime_catalog")" '
    .runtime.catalogFileSHA256 == $catalogSHA256
  ' "$package_root/package-manifest.json" >/dev/null ||
  fail "package runtime catalog digest is not bound"
runtime_revision="$(
  jq -er '.runtime.revision' "$package_root/package-manifest.json"
)"
runtime_artifact_sha="$(
  jq -er '.runtime.artifactSHA256' "$package_root/package-manifest.json"
)"
runtime_contract_sha="$(sha256_file "$runtime_contract")"
jq -e \
  --arg revision "$runtime_revision" \
  --arg artifactSHA256 "$runtime_artifact_sha" \
  --arg contractSHA256 "$runtime_contract_sha" '
    any(.families[];
      .id == "developer-standard" and
      .currentRevision == $revision and
      any(.revisions[];
        .id == $revision and
        .contractDigest == ("sha256:" + $contractSHA256) and
        any(.artifacts[];
          .hostOS == "darwin" and
          .hostArch == "arm64" and
          .sha256 == $artifactSHA256
        )
      )
    )
  ' "$runtime_catalog" >/dev/null ||
  fail "runtime catalog, contract, and artifact binding is invalid"

formal_inventory_relative="$(jq -er '.inventory.path' "$formal_summary")"
formal_inventory_sha="$(jq -er '.inventory.sha256' "$formal_summary")"
safe_relative_path "$formal_inventory_relative" ||
  fail "formal inventory evidence path is unsafe"
formal_inventory_evidence="$artifact_root/formal/$formal_inventory_relative"
require_private_evidence_file "$formal_inventory_evidence"
verify_sha256 "$formal_inventory_evidence" "$formal_inventory_sha" ||
  fail "formal inventory digest does not match formal summary"
cmp "$formal_inventory_source" "$formal_inventory_evidence" >/dev/null ||
  fail "formal evidence inventory differs from the candidate source"

[ -f "$review_file" ] &&
  [ ! -L "$review_file" ] ||
  fail "final code-review report is missing or unsafe"
[ -f "$claim_matrix" ] &&
  [ ! -L "$claim_matrix" ] ||
  fail "claim matrix is missing or unsafe"
[ "$(grep -Ec '^\| CR045-[0-9]{3} \|' "$review_file")" -eq 10 ] ||
  fail "final code-review report does not contain exactly ten findings"
grep -Fq 'There is no open required review finding.' "$review_file" ||
  fail "final code-review report lacks closed-required disposition"
for finding_number in 001 002 003 004 005 006 007 008 009 010; do
  [ "$(grep -c "| CR045-$finding_number |" "$review_file")" -eq 1 ] ||
    fail "final code-review report has a missing or duplicate finding"
done

source_manifest_relative="$(
  jq -er '.source.manifestSHA256' "$package_summary"
)"
source_manifest_path="$(
  jq -r \
    --arg digest "$source_manifest_relative" '
      .artifacts[] |
      select(
        (.path | endswith("/source-manifest.tsv")) and
        .sha256 == $digest
      ) |
      .path
    ' "$package_summary"
)"
[ -n "$source_manifest_path" ] ||
  fail "package summary lacks the exact source-manifest artifact"
safe_relative_path "$source_manifest_path" ||
  fail "package source-manifest path is unsafe"
source_manifest="$artifact_root/package/$source_manifest_path"
require_private_evidence_file "$source_manifest"
verify_sha256 "$source_manifest" "$source_manifest_relative" ||
  fail "package source-manifest digest is invalid"

gate_lines="$scratch/gates.jsonl"
: >"$gate_lines"
append_gate() {
  local gate_id="$1" scope="$2" acceptance="$3"
  local summary_file="$4" pointer_file="${5:-}"
  local pointer_json
  pointer_json="null"
  if [ -n "$pointer_file" ]; then
    pointer_json="$(artifact_ref "$pointer_file")"
  fi
  jq -nc \
    --arg id "$gate_id" \
    --arg scope "$scope" \
    --argjson candidateAcceptance "$acceptance" \
    --arg schema "$(jq -er '.schema' "$summary_file")" \
    --arg generatedAt "$(jq -er '.generatedAt' "$summary_file")" \
    --argjson evidence "$(artifact_ref "$summary_file")" \
    --argjson pointer "$pointer_json" '
      {
        id:$id,
        scope:$scope,
        schema:$schema,
        generatedAt:$generatedAt,
        result:"passed",
        candidateAcceptance:$candidateAcceptance,
        evidence:$evidence
      }
      + if $pointer == null then {} else {pointer:$pointer} end
    ' >>"$gate_lines"
}

append_gate formal source false "$formal_summary"
append_gate local source false "$local_summary"
append_gate dependencies source false "$dependency_summary"
append_gate package-components source false "$component_summary"
append_gate recovery source false "$recovery_summary"
append_gate privacy candidate true "$privacy_summary" "$privacy_pointer"
append_gate ui candidate true "$ui_summary" "$ui_pointer"
append_gate performance candidate true \
  "$performance_summary" "$performance_pointer"
append_gate lima candidate true "$lima_summary" "$lima_pointer"
append_gate package-build candidate true \
  "$package_summary" "$package_pointer"
append_gate package-lifecycle candidate true \
  "$lifecycle_summary" "$package_lifecycle_pointer"
jq -s . "$gate_lines" >"$scratch/gates.json"

local_install_status="pending"
publication_status="pending"
local_install_ref="null"
publication_ref="null"
local_install_receipt="$artifact_root/local-install/result.json"
publication_receipt="$artifact_root/publication-absence/result.json"

if [ -f "$local_install_receipt" ]; then
  require_private_evidence_file "$local_install_receipt"
  validate_closure_receipt \
    "$local_install_receipt" \
    "schemas/local-install-candidate.schema.json"
  jq -e \
    --arg commit "$source_commit" \
    --arg tree "$source_tree" \
    --arg version "$candidate_version" \
    --arg archiveSHA256 "$candidate_archive_sha" '
      .schema == "hideout.local-install-candidate/v1" and
      .result == "passed" and
      .candidateAcceptance == true and
      .sourceCandidate == {commit:$commit,tree:$tree,dirty:false} and
      .candidate.version == $version and
      .candidate.archiveSHA256 == $archiveSHA256 and
      all(.checks[]; . == true)
    ' "$local_install_receipt" >/dev/null ||
    fail "local-install receipt is stale, failed, or mismatched"
  validate_installed_candidate "$local_install_receipt"
  validate_fresh_json "$local_install_receipt"
  local_install_status="passed"
  local_install_ref="$(artifact_ref "$local_install_receipt")"
fi

if [ -f "$publication_receipt" ]; then
  require_private_evidence_file "$publication_receipt"
  validate_closure_receipt \
    "$publication_receipt" \
    "schemas/publication-absence.schema.json"
  jq -e \
    --arg commit "$source_commit" \
    --arg tree "$source_tree" \
    --arg archiveSHA256 "$candidate_archive_sha" '
      .schema == "hideout.publication-absence/v1" and
      .result == "passed" and
      .sourceCandidate == {commit:$commit,tree:$tree,dirty:false} and
      .candidateArchiveSHA256 == $archiveSHA256 and
      .observations.remoteTagCreated == false and
      .observations.githubReleaseCreated == false and
      .observations.homebrewChanged == false and
      .observations.packagePublished == false
    ' "$publication_receipt" >/dev/null ||
    fail "publication-absence receipt is stale, failed, or mismatched"
  validate_fresh_json "$publication_receipt"
  publication_status="passed"
  publication_ref="$(artifact_ref "$publication_receipt")"
fi

if [ "$require_closure" -eq 1 ] &&
  { [ "$local_install_status" != "passed" ] ||
    [ "$publication_status" != "passed" ]; }; then
  fail "--require-closure needs exact local-install and publication-absence receipts"
fi

stage="package-bound"
release_readiness=false
if [ "$local_install_status" = "passed" ]; then
  stage="installed-local"
fi
if [ "$local_install_status" = "passed" ] &&
  [ "$publication_status" = "passed" ]; then
  stage="final-ready"
  release_readiness=true
fi

jq -n \
  --arg localInstall "$local_install_status" \
  --arg publicationAbsence "$publication_status" \
  --argjson localInstallEvidence "$local_install_ref" \
  --argjson publicationEvidence "$publication_ref" '
    {
      localInstall:{
        status:$localInstall
      } + if $localInstallEvidence == null then {}
          else {evidence:$localInstallEvidence} end,
      publicationAbsence:{
        status:$publicationAbsence
      } + if $publicationEvidence == null then {}
          else {evidence:$publicationEvidence} end
    }
  ' >"$scratch/closure.json"

jq -s '
  [
    .[] |
    (
      .limitations[]?,
      .limitation?,
      .claimBoundary?,
      .packageBinaryBoundary?
    ) |
    select(type == "string" and length > 0)
  ] | unique
' \
  "$package_summary" \
  "$lifecycle_summary" \
  "$privacy_summary" \
  "$ui_summary" \
  "$performance_summary" \
  "$lima_summary" \
  "$dependency_summary" \
  "$component_summary" \
  "$recovery_summary" >"$scratch/input-limitations.json"
jq \
  --arg unsigned \
    "The candidate is local and unsigned; Developer ID signing and notarization are not claimed." \
  --arg coverage \
    "Workload observation is metadata with explicit coverage, not syscall-complete behavior proof or prevention." \
  --arg paths \
    "Authenticated local history shows local paths; reviewed export applies a separate redaction policy." \
  --arg guestRoot \
    "Guest-root containment is not claimed." \
  --arg publication \
    "Remote tag, GitHub Release, Homebrew mutation, and package publication require separate explicit authorization." '
    . + [$unsigned,$coverage,$paths,$guestRoot,$publication] | unique
  ' "$scratch/input-limitations.json" >"$scratch/limitations.json"

jq -n \
  --argjson manifest "$(jq '.runtime' "$package_root/package-manifest.json")" \
  --argjson catalog "$(package_ref "runtime/catalog.json")" \
  --argjson contract "$(package_ref "runtime/contract.json")" '
    $manifest + {catalog:$catalog,contract:$contract}
  ' >"$scratch/runtime.json"

jq -n \
  --argjson manifest "$(package_ref "runtime/browser-console.assets.json")" \
  --argjson container "$(package_ref "bin/hideout")" \
  --argjson inventory "$(cat "$browser_manifest")" '
    {
      manifest:$manifest,
      container:$container,
      inventory:$inventory
    }
  ' >"$scratch/browser.json"

jq -n \
  --argjson inventory "$(artifact_ref "$formal_inventory_evidence")" \
  --argjson inventorySource "$(artifact_ref "$formal_inventory_source")" \
  --argjson configurationCount \
    "$(jq '.inventory.configurationCount' "$formal_summary")" \
  --argjson moduleCount "$(jq '.inventory.moduleCount' "$formal_summary")" \
  --argjson invariantCount \
    "$(jq '.inventory.invariantCount' "$formal_summary")" \
  --argjson propertyCount \
    "$(jq '.inventory.propertyCount' "$formal_summary")" \
  --argjson goTestCount "$(jq '.inventory.goTestCount' "$formal_summary")" '
    {
      inventory:$inventory,
      sourceInventory:$inventorySource,
      configurationCount:$configurationCount,
      moduleCount:$moduleCount,
      invariantCount:$invariantCount,
      propertyCount:$propertyCount,
      goTestCount:$goTestCount
    }
  ' >"$scratch/formal.json"

output_tmp="$output_parent/.evidence.$$.json"
detached_output="$output.sha256"
detached_tmp="$output_parent/.evidence.$$.sha256"
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg stage "$stage" \
  --argjson releaseReadiness "$release_readiness" \
  --arg commit "$source_commit" \
  --arg tree "$source_tree" \
  --arg committedAt "$source_committed_at" \
  --argjson sourceManifest "$(artifact_ref "$source_manifest")" \
  --arg version "$candidate_version" \
  --arg tag "v$candidate_version" \
  --argjson archive "$(artifact_ref "$candidate_archive")" \
  --argjson packageManifest "$(artifact_ref "$package_manifest_evidence")" \
  --argjson packageSummary "$(artifact_ref "$package_summary")" \
  --argjson lifecycleSummary "$(artifact_ref "$lifecycle_summary")" \
  --argjson files "$(cat "$scratch/package-files.json")" \
  --argjson helpers "$(cat "$scratch/helpers.json")" \
  --argjson browserConsole "$(cat "$scratch/browser.json")" \
  --argjson runtime "$(cat "$scratch/runtime.json")" \
  --argjson formal "$(cat "$scratch/formal.json")" \
  --argjson gates "$(cat "$scratch/gates.json")" \
  --argjson review "$(artifact_ref "$review_file")" \
  --argjson claims "$(artifact_ref "$claim_matrix")" \
  --argjson limitations "$(cat "$scratch/limitations.json")" \
  --argjson closure "$(cat "$scratch/closure.json")" \
  --arg detachedPath "${detached_output#"$repo_root"/}" '
    {
      schema:"hideout.release-evidence/v1",
      generatedAt:$generatedAt,
      result:"passed",
      stage:$stage,
      releaseReadiness:$releaseReadiness,
      source:{
        commit:$commit,
        tree:$tree,
        dirty:false,
        committedAt:$committedAt,
        manifest:$sourceManifest
      },
      candidate:{
        version:$version,
        tag:$tag,
        channel:"developer-preview",
        signingMode:"developer-preview-unsigned",
        publicationStatus:"local-only",
        archive:$archive,
        packageManifest:$packageManifest,
        packageSummary:$packageSummary,
        lifecycleSummary:$lifecycleSummary
      },
      package:{
        files:$files,
        helpers:$helpers,
        browserConsole:$browserConsole,
        runtime:$runtime
      },
      formal:$formal,
      gates:$gates,
      review:{
        result:"passed",
        requiredFindings:7,
        openRequiredFindings:0,
        report:$review,
        claimMatrix:$claims
      },
      limitations:$limitations,
      closure:$closure,
      digest:{
        algorithm:"sha256",
        detachedPath:$detachedPath
      }
    }
  ' >"$output_tmp"
chmod 0600 "$output_tmp"

go run ./cmd/hideout-schema-validate \
  schemas/release-evidence.schema.json \
  "$output_tmp" >/dev/null

jq -e \
  --arg commit "$source_commit" \
  --arg tree "$source_tree" \
  --arg version "$candidate_version" \
  --arg archiveSHA256 "$candidate_archive_sha" \
  --arg stage "$stage" \
  --argjson releaseReadiness "$release_readiness" '
    .schema == "hideout.release-evidence/v1" and
    .result == "passed" and
    .stage == $stage and
    .releaseReadiness == $releaseReadiness and
    .source.commit == $commit and
    .source.tree == $tree and
    .source.dirty == false and
    .candidate.version == $version and
    .candidate.archive.sha256 == $archiveSHA256 and
    .candidate.publicationStatus == "local-only" and
    (.package.files | length) >= 100 and
    ([.package.files[].path] | unique | length) ==
      (.package.files | length) and
    ([.package.helpers[] | select(.kind == "helper-manifest")] | length) == 6 and
    (.package.browserConsole.inventory.assets | length) == 8 and
    .formal.configurationCount == 12 and
    .formal.moduleCount == 10 and
    .formal.invariantCount == 72 and
    .formal.propertyCount == 18 and
    .formal.goTestCount == 12 and
    (.gates | length) == 11 and
    ([.gates[].id] | unique | length) == 11 and
    all(.gates[]; .result == "passed") and
    all(.gates[] | select(.scope == "candidate");
      .candidateAcceptance == true) and
    .review.requiredFindings == 7 and
    .review.openRequiredFindings == 0 and
    (.limitations | length) >= 5
  ' "$output_tmp" >/dev/null ||
  fail "final evidence manifest failed semantic validation"

if [ -n "$(git status --porcelain=v1 --untracked-files=all)" ]; then
  fail "source tree changed during evidence collection"
fi

evidence_sha="$(sha256_file "$output_tmp")"
printf '%s  %s\n' "$evidence_sha" "$(basename -- "$output")" \
  >"$detached_tmp"
chmod 0600 "$detached_tmp"
verify_sha256 "$output_tmp" "$evidence_sha" ||
  fail "detached evidence digest did not verify before publication"

mv "$output_tmp" "$output"
mv "$detached_tmp" "$detached_output"
chmod 0600 "$output" "$detached_output"
verify_sha256 "$output" "$evidence_sha" ||
  fail "written evidence manifest does not match detached digest"

gate_completed=1
printf \
  'collect-evidence: passed stage=%s readiness=%s manifest=%s sha256=%s\n' \
  "$stage" \
  "$release_readiness" \
  "$output" \
  "$evidence_sha"
