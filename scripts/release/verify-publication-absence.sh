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
candidate_result="$artifact_root/package/result.json"
out="$artifact_root/publication-absence"
origin="origin"
source_repository="vibe-agi/hideout"
homebrew_repository="vibe-agi/homebrew-tap"
tap_root=""
preflight_only=0
tmp_base="${TMPDIR:-/tmp}"
tmp_base="${tmp_base%/}"

usage() {
  printf '%s\n' \
    "Usage: scripts/release/verify-publication-absence.sh [--preflight]" \
    "       [--candidate-result FILE] [--out DIR] [--origin NAME]" \
    "       [--tap DIR]" \
    "" \
    "Produces a private, point-in-time, read-only receipt proving that the" \
    "exact accepted local candidate has no matching remote Git tag, GitHub" \
    "Release, or Homebrew formula publication. It never commits, tags, pushes," \
    "creates a release, edits a tap, or publishes package bytes."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --preflight)
      preflight_only=1
      shift
      ;;
    --candidate-result)
      [ "$#" -ge 2 ] && [ -n "${2:-}" ] || {
        printf 'publication-absence: --candidate-result requires a file\n' >&2
        exit 2
      }
      candidate_result="$2"
      shift 2
      ;;
    --out)
      [ "$#" -ge 2 ] && [ -n "${2:-}" ] || {
        printf 'publication-absence: --out requires a directory\n' >&2
        exit 2
      }
      out="$2"
      shift 2
      ;;
    --origin)
      [ "$#" -ge 2 ] && [ -n "${2:-}" ] || {
        printf 'publication-absence: --origin requires a remote name\n' >&2
        exit 2
      }
      origin="$2"
      shift 2
      ;;
    --tap)
      [ "$#" -ge 2 ] && [ -n "${2:-}" ] || {
        printf 'publication-absence: --tap requires a directory\n' >&2
        exit 2
      }
      tap_root="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'publication-absence: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

fail() {
  printf 'publication-absence: %s\n' "$1" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    printf 'publication-absence: missing required command: %s\n' "$1" >&2
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
      fail "refusing unexpected cleanup target: $target"
      ;;
  esac
}

verify_sha256() {
  local file="$1" expected="$2"
  [ -f "$file" ] &&
    [ ! -L "$file" ] &&
    [[ "$expected" =~ ^[a-f0-9]{64}$ ]] &&
    [ "$(sha256_file "$file")" = "$expected" ]
}

validate_receipt() {
  local receipt="$1" commit="$2" tree="$3" archive_sha="$4"
  local version="$5" tag="$6"
  jq -e \
    --arg commit "$commit" \
    --arg tree "$tree" \
    --arg archiveSHA256 "$archive_sha" \
    --arg version "$version" \
    --arg tag "$tag" '
      .schema == "hideout.publication-absence/v1" and
      .result == "passed" and
      .sourceCandidate == {commit:$commit,tree:$tree,dirty:false} and
      .candidate == {
        version:$version,
        tag:$tag,
        archiveSHA256:$archiveSHA256
      } and
      .candidateArchiveSHA256 == $archiveSHA256 and
      .observations == {
        remoteTagCreated:false,
        githubReleaseCreated:false,
        homebrewChanged:false,
        packagePublished:false
      } and
      .remote.tagQuery == ("refs/tags/" + $tag) and
      .remote.releaseHTTPStatus == 404 and
      .localTap.cleanBefore == true and
      .localTap.cleanAfter == true and
      .localTap.headBefore == .localTap.headAfter and
      .localTap.treeBefore == .localTap.treeAfter and
      .localTap.formulaSHA256Before == .localTap.formulaSHA256After and
      (.checks | length) == 9 and
      all(.checks[]; . == true) and
      (.artifacts | length) >= 3 and
      all(.artifacts[];
        (.path | type == "string") and
        (.sha256 | test("^[a-f0-9]{64}$")) and
        (.bytes | type == "number") and
        .mode == "0600")
    ' "$receipt" >/dev/null
}

run_preflight() {
  local fixture commit tree digest version tag
  preflight_root="$(
    mktemp -d "$tmp_base/hideout-publication-absence-preflight.XXXXXX"
  )"
  # Invoked indirectly by the EXIT trap.
  # shellcheck disable=SC2329
  cleanup_preflight() {
    local exit_status=$?
    cleanup_tree \
      "${preflight_root:-}" \
      "hideout-publication-absence-preflight"
    if [ "$exit_status" -eq 0 ]; then
      gate_require_completion "publication-absence-preflight"
    fi
  }
  trap cleanup_preflight EXIT
  fixture="$preflight_root/receipt.json"
  commit="0123456789abcdef0123456789abcdef01234567"
  tree="89abcdef0123456789abcdef0123456789abcdef"
  digest="0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
  version="0.1.0-alpha.4"
  tag="v$version"
  jq -n \
    --arg commit "$commit" \
    --arg tree "$tree" \
    --arg digest "$digest" \
    --arg version "$version" \
    --arg tag "$tag" '
      {
        schema:"hideout.publication-absence/v1",
        generatedAt:"2026-07-31T00:00:00Z",
        result:"passed",
        sourceCandidate:{commit:$commit,tree:$tree,dirty:false},
        candidate:{
          version:$version,
          tag:$tag,
          archiveSHA256:$digest
        },
        candidateArchiveSHA256:$digest,
        observations:{
          remoteTagCreated:false,
          githubReleaseCreated:false,
          homebrewChanged:false,
          packagePublished:false
        },
        remote:{
          sourceRepository:"vibe-agi/hideout",
          tagQuery:("refs/tags/" + $tag),
          releaseHTTPStatus:404,
          homebrewRepository:"vibe-agi/homebrew-tap",
          formulaPath:"Formula/hideout.rb",
          formulaSHA256:$digest
        },
        localTap:{
          path:"/fixture/homebrew-tap",
          headBefore:$commit,
          headAfter:$commit,
          treeBefore:$tree,
          treeAfter:$tree,
          formulaSHA256Before:$digest,
          formulaSHA256After:$digest,
          cleanBefore:true,
          cleanAfter:true
        },
        checks:{
          sourceClean:true,
          candidateDigestVerified:true,
          remoteTagAbsent:true,
          githubReleaseAbsent:true,
          homebrewFormulaUnchanged:true,
          homebrewCandidateAbsent:true,
          localTapUnchanged:true,
          noPublicationMutation:true,
          observationsExactlyFalse:true
        },
        artifacts:[
          {path:"run-fixture/tag.json",sha256:$digest,bytes:1,mode:"0600"},
          {path:"run-fixture/release.json",sha256:$digest,bytes:1,mode:"0600"},
          {path:"run-fixture/formula.json",sha256:$digest,bytes:1,mode:"0600"}
        ],
        limitations:["read-only point-in-time observation"]
      }
    ' >"$fixture"
  chmod 0600 "$fixture"
  validate_receipt "$fixture" "$commit" "$tree" "$digest" "$version" "$tag" ||
    fail "valid receipt fixture was rejected"

  jq '.observations.remoteTagCreated = true' "$fixture" >"$fixture.mutated"
  if validate_receipt \
    "$fixture.mutated" "$commit" "$tree" "$digest" "$version" "$tag"; then
    fail "observed remote tag mutation was accepted"
  fi
  jq '.localTap.headAfter = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"' \
    "$fixture" >"$fixture.mutated"
  if validate_receipt \
    "$fixture.mutated" "$commit" "$tree" "$digest" "$version" "$tag"; then
    fail "local tap mutation was accepted"
  fi
  jq '.candidateArchiveSHA256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"' \
    "$fixture" >"$fixture.mutated"
  if validate_receipt \
    "$fixture.mutated" "$commit" "$tree" "$digest" "$version" "$tag"; then
    fail "candidate digest mismatch was accepted"
  fi
  gate_completed=1
  printf 'publication-absence: preflight=passed\n'
}

if [ "$preflight_only" -eq 1 ]; then
  for required_command in jq mktemp find; do
    require_command "$required_command" || exit 1
  done
  run_preflight
  exit 0
fi

for required_command in git gh jq find sort awk stat grep brew; do
  require_command "$required_command" || exit 1
done

[ "$(uname -s)" = "Darwin" ] &&
  [ "$(uname -m)" = "arm64" ] ||
  fail "full verification requires Darwin/arm64"

source_status_before="$(git status --porcelain=v1 --untracked-files=all)"
[ -z "$source_status_before" ] ||
  fail "exact verification requires a completely clean source tree"
source_commit="$(git rev-parse HEAD)"
source_tree="$(git rev-parse 'HEAD^{tree}')"
[[ "$source_commit" =~ ^[a-f0-9]{40}$ ]] ||
  fail "source commit identity is invalid"
[[ "$source_tree" =~ ^[a-f0-9]{40}$ ]] ||
  fail "source tree identity is invalid"
git remote get-url "$origin" >/dev/null 2>&1 ||
  fail "source remote does not exist: $origin"

[ -f "$candidate_result" ] && [ ! -L "$candidate_result" ] ||
  fail "candidate result is missing or unsafe"
candidate_result="$(
  CDPATH='' cd -- "$(dirname -- "$candidate_result")" &&
    printf '%s/%s\n' "$(pwd -P)" "$(basename -- "$candidate_result")"
)"
case "$candidate_result" in
  "$artifact_root"/package/*)
    ;;
  *)
    fail "candidate result must remain under .artifacts/045/package"
    ;;
esac
[ "$(normalized_mode "$candidate_result")" = "0600" ] ||
  fail "candidate result must be private mode 0600"
jq -e \
  --arg commit "$source_commit" \
  --arg tree "$source_tree" '
    .schema == "hideout.release-package-candidate-pointer/v1" and
    .result == "passed" and
    .source == {commit:$commit,tree:$tree,dirty:false} and
    .candidateAcceptance == true and
    .publicationStatus == "local-only"
  ' "$candidate_result" >/dev/null ||
  fail "candidate result is not bound to the exact clean source"

package_root="$(dirname -- "$candidate_result")"
summary_relative="$(jq -er '.summary' "$candidate_result")"
summary_sha="$(jq -er '.summarySHA256' "$candidate_result")"
archive_relative="$(jq -er '.archive' "$candidate_result")"
archive_sha="$(jq -er '.archiveSHA256' "$candidate_result")"
safe_relative_path "$summary_relative" ||
  fail "candidate summary path is unsafe"
safe_relative_path "$archive_relative" ||
  fail "candidate archive path is unsafe"
summary="$package_root/$summary_relative"
archive="$package_root/$archive_relative"
verify_sha256 "$summary" "$summary_sha" ||
  fail "candidate summary digest is invalid"
verify_sha256 "$archive" "$archive_sha" ||
  fail "candidate archive digest is invalid"
[ "$(normalized_mode "$summary")" = "0600" ] &&
  [ "$(normalized_mode "$archive")" = "0600" ] ||
  fail "candidate summary and archive must be private mode 0600"
version="$(jq -er '.candidate.version' "$summary")"
tag="$(jq -er '.candidate.tag' "$summary")"
[ "$tag" = "v$version" ] ||
  fail "candidate version/tag binding is invalid"
jq -e \
  --arg commit "$source_commit" \
  --arg tree "$source_tree" \
  --arg archiveSHA256 "$archive_sha" \
  --arg version "$version" \
  --arg tag "$tag" '
    .schema == "hideout.release-package-candidate/v1" and
    .result == "passed" and
    .source.commit == $commit and
    .source.tree == $tree and
    .source.dirty == false and
    .candidate.acceptance == true and
    .candidate.version == $version and
    .candidate.tag == $tag and
    .candidate.archiveSHA256 == $archiveSHA256 and
    .candidate.publicationStatus == "local-only"
  ' "$summary" >/dev/null ||
  fail "candidate summary identity is invalid"

if [ -z "$tap_root" ]; then
  tap_root="$(brew --repository vibe-agi/tap)"
fi
[ -d "$tap_root" ] && [ ! -L "$tap_root" ] ||
  fail "local Homebrew tap is missing or unsafe"
tap_root="$(CDPATH='' cd -- "$tap_root" && pwd -P)"
tap_formula="$tap_root/Formula/hideout.rb"
[ -f "$tap_formula" ] && [ ! -L "$tap_formula" ] ||
  fail "local Homebrew formula is missing or unsafe"
tap_status_before="$(git -C "$tap_root" status --porcelain=v1 --untracked-files=all)"
[ -z "$tap_status_before" ] ||
  fail "local Homebrew tap is not clean"
tap_head_before="$(git -C "$tap_root" rev-parse HEAD)"
tap_tree_before="$(git -C "$tap_root" rev-parse 'HEAD^{tree}')"
tap_formula_sha_before="$(sha256_file "$tap_formula")"

scratch="$(
  mktemp -d "$tmp_base/hideout-publication-absence.XXXXXX"
)"
cleanup() {
  local exit_status=$?
  cleanup_tree "${scratch:-}" "hideout-publication-absence"
  if [ "$exit_status" -eq 0 ]; then
    gate_require_completion "publication-absence"
  fi
}
trap cleanup EXIT

check_remote_tag_absent() {
  local label="$1" output_file query_exit
  output_file="$scratch/tag-$label.raw"
  set +e
  git ls-remote --exit-code --tags \
    "$origin" "refs/tags/$tag" >"$output_file" 2>"$output_file.err"
  query_exit=$?
  set -e
  [ "$query_exit" -eq 2 ] ||
    fail "remote tag query did not prove absence (status=$query_exit)"
  [ ! -s "$output_file" ] ||
    fail "candidate remote tag exists: $tag"
  [ ! -s "$output_file.err" ] ||
    fail "remote tag query emitted an unexpected error"
}

check_release_absent() {
  local label="$1" output_file query_exit
  output_file="$scratch/release-$label.raw"
  set +e
  gh api --include \
    "repos/$source_repository/releases/tags/$tag" \
    >"$output_file" 2>&1
  query_exit=$?
  set -e
  [ "$query_exit" -ne 0 ] ||
    fail "candidate GitHub Release exists: $tag"
  grep -Eq 'HTTP/[0-9.]+ 404|HTTP 404' "$output_file" ||
    fail "GitHub Release query did not return an exact 404"
}

fetch_remote_formula() {
  local destination="$1"
  gh api \
    -H "Accept: application/vnd.github.raw+json" \
    "repos/$homebrew_repository/contents/Formula/hideout.rb" \
    >"$destination"
  [ -s "$destination" ] ||
    fail "remote Homebrew formula response is empty"
}

check_formula_excludes_candidate() {
  local formula="$1"
  for forbidden in "$version" "$tag" "$archive_sha" "$source_commit"; do
    if grep -Fq -- "$forbidden" "$formula"; then
      fail "remote Homebrew formula contains exact candidate material"
    fi
  done
}

check_remote_tag_absent before
check_release_absent before
fetch_remote_formula "$scratch/formula-before.rb"
check_formula_excludes_candidate "$scratch/formula-before.rb"
remote_formula_sha_before="$(sha256_file "$scratch/formula-before.rb")"
[ "$remote_formula_sha_before" = "$tap_formula_sha_before" ] ||
  fail "local Homebrew tap formula is not the observed remote formula"

check_remote_tag_absent after
check_release_absent after
fetch_remote_formula "$scratch/formula-after.rb"
check_formula_excludes_candidate "$scratch/formula-after.rb"
remote_formula_sha_after="$(sha256_file "$scratch/formula-after.rb")"
[ "$remote_formula_sha_after" = "$remote_formula_sha_before" ] ||
  fail "remote Homebrew formula changed during verification"

source_status_after="$(git status --porcelain=v1 --untracked-files=all)"
source_commit_after="$(git rev-parse HEAD)"
source_tree_after="$(git rev-parse 'HEAD^{tree}')"
[ -z "$source_status_after" ] &&
  [ "$source_commit_after" = "$source_commit" ] &&
  [ "$source_tree_after" = "$source_tree" ] ||
  fail "source repository changed during publication verification"

tap_status_after="$(git -C "$tap_root" status --porcelain=v1 --untracked-files=all)"
tap_head_after="$(git -C "$tap_root" rev-parse HEAD)"
tap_tree_after="$(git -C "$tap_root" rev-parse 'HEAD^{tree}')"
tap_formula_sha_after="$(sha256_file "$tap_formula")"
[ -z "$tap_status_after" ] &&
  [ "$tap_head_after" = "$tap_head_before" ] &&
  [ "$tap_tree_after" = "$tap_tree_before" ] &&
  [ "$tap_formula_sha_after" = "$tap_formula_sha_before" ] ||
  fail "local Homebrew tap changed during publication verification"

if [ -L "$out" ]; then
  fail "evidence root must not be a symlink"
fi
mkdir -p "$out"
out="$(CDPATH='' cd -- "$out" && pwd -P)"
case "$out" in
  "$artifact_root"/publication-absence)
    ;;
  *)
    fail "evidence root must be .artifacts/045/publication-absence"
    ;;
esac
chmod 0700 "$out"
run_id="run-$(date -u +'%Y%m%dT%H%M%SZ')-$$"
run_dir="$out/$run_id"
[ ! -e "$run_dir" ] || fail "evidence run already exists"
mkdir "$run_dir"
chmod 0700 "$run_dir"

jq -n \
  --arg observedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg tag "$tag" '
    {
      observedAt:$observedAt,
      query:("refs/tags/" + $tag),
      status:"absent",
      observations:2
    }
  ' >"$run_dir/remote-tag.json"
jq -n \
  --arg observedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg tag "$tag" '
    {
      observedAt:$observedAt,
      tag:$tag,
      httpStatus:404,
      status:"absent",
      observations:2
    }
  ' >"$run_dir/github-release.json"
jq -n \
  --arg observedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg repository "$homebrew_repository" \
  --arg formulaSHA256 "$remote_formula_sha_before" '
    {
      observedAt:$observedAt,
      repository:$repository,
      path:"Formula/hideout.rb",
      formulaSHA256:$formulaSHA256,
      candidateMaterial:"absent",
      stableAcrossObservations:true
    }
  ' >"$run_dir/remote-formula.json"
jq -n \
  --arg path "$tap_root" \
  --arg head "$tap_head_before" \
  --arg tree "$tap_tree_before" \
  --arg formulaSHA256 "$tap_formula_sha_before" '
    {
      path:$path,
      head:$head,
      tree:$tree,
      formulaSHA256:$formulaSHA256,
      clean:true
    }
  ' >"$run_dir/local-tap-before.json"
jq -n \
  --arg path "$tap_root" \
  --arg head "$tap_head_after" \
  --arg tree "$tap_tree_after" \
  --arg formulaSHA256 "$tap_formula_sha_after" '
    {
      path:$path,
      head:$head,
      tree:$tree,
      formulaSHA256:$formulaSHA256,
      clean:true
    }
  ' >"$run_dir/local-tap-after.json"
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
artifacts="$(jq -s . "$artifact_lines")"

result_tmp="$out/.result.$$.json"
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$source_commit" \
  --arg tree "$source_tree" \
  --arg version "$version" \
  --arg tag "$tag" \
  --arg archiveSHA256 "$archive_sha" \
  --arg sourceRepository "$source_repository" \
  --arg homebrewRepository "$homebrew_repository" \
  --arg formulaSHA256 "$remote_formula_sha_before" \
  --arg tapPath "$tap_root" \
  --arg tapHeadBefore "$tap_head_before" \
  --arg tapHeadAfter "$tap_head_after" \
  --arg tapTreeBefore "$tap_tree_before" \
  --arg tapTreeAfter "$tap_tree_after" \
  --arg tapFormulaBefore "$tap_formula_sha_before" \
  --arg tapFormulaAfter "$tap_formula_sha_after" \
  --argjson artifacts "$artifacts" '
    {
      schema:"hideout.publication-absence/v1",
      generatedAt:$generatedAt,
      result:"passed",
      sourceCandidate:{commit:$commit,tree:$tree,dirty:false},
      candidate:{
        version:$version,
        tag:$tag,
        archiveSHA256:$archiveSHA256
      },
      candidateArchiveSHA256:$archiveSHA256,
      observations:{
        remoteTagCreated:false,
        githubReleaseCreated:false,
        homebrewChanged:false,
        packagePublished:false
      },
      remote:{
        sourceRepository:$sourceRepository,
        tagQuery:("refs/tags/" + $tag),
        releaseHTTPStatus:404,
        homebrewRepository:$homebrewRepository,
        formulaPath:"Formula/hideout.rb",
        formulaSHA256:$formulaSHA256
      },
      localTap:{
        path:$tapPath,
        headBefore:$tapHeadBefore,
        headAfter:$tapHeadAfter,
        treeBefore:$tapTreeBefore,
        treeAfter:$tapTreeAfter,
        formulaSHA256Before:$tapFormulaBefore,
        formulaSHA256After:$tapFormulaAfter,
        cleanBefore:true,
        cleanAfter:true
      },
      checks:{
        sourceClean:true,
        candidateDigestVerified:true,
        remoteTagAbsent:true,
        githubReleaseAbsent:true,
        homebrewFormulaUnchanged:true,
        homebrewCandidateAbsent:true,
        localTapUnchanged:true,
        noPublicationMutation:true,
        observationsExactlyFalse:true
      },
      artifacts:$artifacts,
      limitations:[
        "This is a point-in-time read-only observation of the supported GitHub Release and Homebrew channels; it does not authorize publication or claim future absence."
      ]
    }
  ' >"$result_tmp"
chmod 0600 "$result_tmp"
validate_receipt \
  "$result_tmp" "$source_commit" "$source_tree" \
  "$archive_sha" "$version" "$tag" ||
  fail "final receipt semantic validation failed"
while IFS= read -r artifact; do
  path="$(jq -r '.path' <<<"$artifact")"
  expected_sha="$(jq -r '.sha256' <<<"$artifact")"
  expected_bytes="$(jq -r '.bytes' <<<"$artifact")"
  safe_relative_path "$path" ||
    fail "receipt contains an unsafe artifact path"
  file="$out/$path"
  verify_sha256 "$file" "$expected_sha" ||
    fail "receipt artifact digest is invalid: $path"
  [ "$(file_bytes "$file")" = "$expected_bytes" ] &&
    [ "$(normalized_mode "$file")" = "0600" ] ||
    fail "receipt artifact metadata is invalid: $path"
done < <(jq -c '.artifacts[]' "$result_tmp")
mv "$result_tmp" "$out/result.json"

gate_completed=1
printf \
  'publication-absence: passed tag=%s archiveSHA256=%s receipt=%s\n' \
  "$tag" "$archive_sha" "$out/result.json"
