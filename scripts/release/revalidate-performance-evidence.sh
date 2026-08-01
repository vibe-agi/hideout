#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"
# shellcheck source=scripts/lib/gate-result.sh
. "$repo_root/scripts/lib/gate-result.sh"
gate_completed=0

umask 077
artifact_root="$repo_root/.artifacts/045"
performance_root="$artifact_root/performance"
schema_path="$repo_root/schemas/performance-evidence-reuse.schema.json"
assessment_filter="$repo_root/scripts/release/performance-evidence-assessment.jq"
preflight_only=0
check_receipt_path=""
measured_summary_path=""
scratch=""
created_run_dir=""

usage() {
  printf '%s\n' \
    "Usage: scripts/release/revalidate-performance-evidence.sh [--preflight]" \
    "       scripts/release/revalidate-performance-evidence.sh --check <receipt>" \
    "       scripts/release/revalidate-performance-evidence.sh" \
    "" \
    "Reuses a retained performance measurement only when the current clean" \
    "commit differs from the measured commit by the reviewed evidence-only" \
    "set, the normalized measurement entrypoint is byte-identical, every" \
    "retained artifact verifies, and independent child-process CPU plus paired" \
    "wall-clock confidence bounds both remain within the frozen threshold."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --preflight)
      preflight_only=1
      shift
      ;;
    --check)
      [ "$#" -ge 2 ] && [ -n "${2:-}" ] || {
        printf 'performance-revalidate: --check requires a receipt\n' >&2
        exit 2
      }
      check_receipt_path="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'performance-revalidate: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [ "$preflight_only" -eq 1 ] &&
  [ -n "$check_receipt_path" ]; then
  printf 'performance-revalidate: --preflight cannot be combined with full modes\n' >&2
  exit 2
fi

fail() {
  printf 'performance-revalidate: %s\n' "$1" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 ||
    fail "missing required command: $1"
}

for required_command in awk cp date find git go jq mktemp mv sed shasum sort stat; do
  require_command "$required_command"
done

sha256_file() {
  shasum -a 256 "$1" | awk '{print $1}'
}

sha256_stream() {
  shasum -a 256 | awk '{print $1}'
}

file_bytes() {
  stat -f '%z' "$1" 2>/dev/null || stat -c '%s' "$1" 2>/dev/null
}

file_mode() {
  local mode
  mode="$(stat -f '%Lp' "$1" 2>/dev/null || stat -c '%a' "$1" 2>/dev/null)"
  printf '%04o\n' "$((8#$mode))"
}

safe_relative_path() {
  case "$1" in
    "" | /* | .. | ../* | */.. | */../* | *$'\t'* | *$'\n'*) return 1 ;;
    *) return 0 ;;
  esac
}

require_private_file() {
  local required_file="$1"
  [ -f "$required_file" ] && [ ! -L "$required_file" ] ||
    fail "required file is missing, non-regular, or a symlink: $required_file"
  [ "$(file_mode "$required_file")" = "0600" ] ||
    fail "required file is not private mode 0600: $required_file"
}

canonical_repo_file() {
  local candidate="$1" candidate_dir candidate_name canonical
  candidate_dir="$(dirname -- "$candidate")"
  candidate_name="$(basename -- "$candidate")"
  [ -d "$candidate_dir" ] && [ ! -L "$candidate_dir" ] ||
    fail "file parent is missing or unsafe: $candidate"
  canonical="$(CDPATH='' cd -- "$candidate_dir" && pwd -P)/$candidate_name"
  case "$canonical" in
    "$repo_root"/*) printf '%s\n' "$canonical" ;;
    *) fail "file is outside the repository: $candidate" ;;
  esac
}

repo_relative_path() {
  local canonical
  canonical="$(canonical_repo_file "$1")"
  printf '%s\n' "${canonical#"$repo_root"/}"
}

artifact_ref() {
  local referenced_file="$1" relative
  require_private_file "$referenced_file"
  relative="$(repo_relative_path "$referenced_file")"
  jq -nc \
    --arg path "$relative" \
    --arg sha256 "$(sha256_file "$referenced_file")" \
    --argjson bytes "$(file_bytes "$referenced_file")" '
      {path:$path,sha256:$sha256,bytes:$bytes,mode:"0600"}
    '
}

normalize_performance_entrypoint() {
  awk '
    /HIDEOUT_INCREMENTAL_PERFORMANCE_IGNORE_BEGIN/ {
      if (skip) exit 1
      skip = 1
      begin_count++
      next
    }
    /HIDEOUT_INCREMENTAL_PERFORMANCE_IGNORE_END/ {
      if (!skip) exit 1
      skip = 0
      end_count++
      next
    }
    skip {next}
    {
      blank = ($0 == "")
      if (blank && previous_blank) next
      print
      previous_blank = blank
    }
    END {
      if (skip || begin_count != end_count) exit 1
    }
  '
}

validate_approved_entrypoint_ignore_blocks() {
  local entrypoint="$1" begin_count end_count marker_ids
  local final_block_sha order_block_sha
  begin_count="$(
    awk '/HIDEOUT_INCREMENTAL_PERFORMANCE_IGNORE_BEGIN/ {count++}
      END {print count+0}' "$entrypoint"
  )"
  end_count="$(
    awk '/HIDEOUT_INCREMENTAL_PERFORMANCE_IGNORE_END/ {count++}
      END {print count+0}' "$entrypoint"
  )"
  if [ "$begin_count" -eq 0 ] && [ "$end_count" -eq 0 ]; then
    return 0
  fi
  [ "$begin_count" -eq 2 ] && [ "$end_count" -eq 2 ] ||
    return 1
  marker_ids="$(
    awk '
      /HIDEOUT_INCREMENTAL_PERFORMANCE_IGNORE_(BEGIN|END)/ {print $NF}
    ' "$entrypoint" | LC_ALL=C sort -u
  )"
  [ "$marker_ids" = $'final-evidence-preflight\npreflight-order-assertion' ] ||
    return 1
  final_block_sha="$(
    awk '
      /^[[:space:]]*# HIDEOUT_INCREMENTAL_PERFORMANCE_IGNORE_BEGIN final-evidence-preflight$/ {
        if (inside) exit 1
        inside=1
        begin++
        next
      }
      /^[[:space:]]*# HIDEOUT_INCREMENTAL_PERFORMANCE_IGNORE_END final-evidence-preflight$/ {
        if (!inside) exit 1
        inside=0
        end++
        next
      }
      inside {print}
      END {if (inside || begin != 1 || end != 1) exit 1}
    ' "$entrypoint" | sha256_stream
  )" || return 1
  order_block_sha="$(
    awk '
      /^[[:space:]]*# HIDEOUT_INCREMENTAL_PERFORMANCE_IGNORE_BEGIN preflight-order-assertion$/ {
        if (inside) exit 1
        inside=1
        begin++
        next
      }
      /^[[:space:]]*# HIDEOUT_INCREMENTAL_PERFORMANCE_IGNORE_END preflight-order-assertion$/ {
        if (!inside) exit 1
        inside=0
        end++
        next
      }
      inside {print}
      END {if (inside || begin != 1 || end != 1) exit 1}
    ' "$entrypoint" | sha256_stream
  )" || return 1
  [ "$final_block_sha" = \
    "de381095071d85d260adef6cbc52e90743f443dafb48c0855b7b9872727f11a4" ] &&
    [ "$order_block_sha" = \
      "844fcd89c50fc429f1cf5e86cf7fbb9e36c53dc978937ab1fdcd5055749ce672" ]
}

normalized_entrypoint_at_commit() {
  local commit="$1" entrypoint="$scratch/base-performance-entrypoint.sh"
  git show "$commit:scripts/gates/release-candidate-performance.sh" \
    >"$entrypoint"
  validate_approved_entrypoint_ignore_blocks "$entrypoint" ||
    fail "measured entrypoint has an unapproved ignored block"
  normalize_performance_entrypoint <"$entrypoint" |
    sha256_stream
}

normalized_current_entrypoint() {
  validate_approved_entrypoint_ignore_blocks \
    scripts/gates/release-candidate-performance.sh ||
    fail "current entrypoint has an unapproved ignored block"
  normalize_performance_entrypoint \
    <scripts/gates/release-candidate-performance.sh |
    sha256_stream
}

allowed_evidence_only_change() {
  case "$1" in
    docs/release/045-claim-matrix.md | \
      docs/release/045-code-review.md | \
      docs/release/045-readiness.md | \
      schemas/performance-evidence-reuse.schema.json | \
      schemas/release-evidence.schema.json | \
      scripts/gates/release-candidate-inventory.json | \
      scripts/gates/release-candidate-performance.sh | \
      scripts/gates/release-candidate.sh | \
      scripts/release/collect-evidence.sh | \
      scripts/release/performance-evidence-assessment.jq | \
      scripts/release/release-evidence-semantic.jq | \
      scripts/release/release-evidence.jq | \
      scripts/release/revalidate-performance-evidence.sh | \
      specs/045-operator-observability-console/gate-matrix.md | \
      specs/045-operator-observability-console/spec.md | \
      specs/045-operator-observability-console/tasks.md)
      return 0
      ;;
    *) return 1 ;;
  esac
}

collect_impact() {
  local base_commit="$1" current_commit="$2" destination="$3"
  local raw_changes="$scratch/changed-files.tsv"
  local json_lines="$scratch/changed-files.jsonl"
  local status changed_file extra before_sha after_sha changed_count=0

  git diff --name-status --no-renames \
    "$base_commit" "$current_commit" >"$raw_changes"
  : >"$json_lines"
  while IFS=$'\t' read -r status changed_file extra; do
    [ -n "$status" ] || continue
    [ -z "${extra:-}" ] ||
      fail "renamed or malformed change cannot reuse performance evidence"
    safe_relative_path "$changed_file" ||
      fail "changed path is unsafe: $changed_file"
    allowed_evidence_only_change "$changed_file" ||
      fail "product or measurement input changed: $changed_file"
    case "$status" in
      A)
        if git cat-file -e "$base_commit:$changed_file" 2>/dev/null; then
          fail "added-path status disagrees with measured commit: $changed_file"
        fi
        after_sha="$(git show "$current_commit:$changed_file" | sha256_stream)"
        jq -nc \
          --arg path "$changed_file" \
          --arg afterSHA256 "$after_sha" '
            {path:$path,status:"A",afterSHA256:$afterSHA256}
          ' >>"$json_lines"
        ;;
      M)
        before_sha="$(git show "$base_commit:$changed_file" | sha256_stream)"
        after_sha="$(git show "$current_commit:$changed_file" | sha256_stream)"
        [ "$before_sha" != "$after_sha" ] ||
          fail "modified-path digest did not change: $changed_file"
        jq -nc \
          --arg path "$changed_file" \
          --arg beforeSHA256 "$before_sha" \
          --arg afterSHA256 "$after_sha" '
            {
              path:$path,
              status:"M",
              beforeSHA256:$beforeSHA256,
              afterSHA256:$afterSHA256
            }
          ' >>"$json_lines"
        ;;
      *)
        fail "unsupported change status $status for $changed_file"
        ;;
    esac
    changed_count=$((changed_count + 1))
  done <"$raw_changes"
  [ "$changed_count" -gt 0 ] ||
    fail "incremental reuse requires at least one reviewed evidence-only change"
  jq -s 'sort_by(.path)' "$json_lines" >"$destination"
}

verify_summary_artifacts() {
  local summary="$1" summary_dir artifact_count unique_count
  local relative expected_sha expected_bytes expected_mode artifact_file
  local verified=0

  summary_dir="$(CDPATH='' cd -- "$(dirname -- "$summary")" && pwd -P)"
  case "$summary_dir" in
    "$performance_root"/run-*) ;;
    *) fail "measured summary is not in an immutable performance run" ;;
  esac
  [ -z "$(find "$summary_dir" -type l -print -quit)" ] ||
    fail "measured performance run contains a symlink"
  artifact_count="$(jq -er '.artifacts | length' "$summary")"
  unique_count="$(jq -er '[.artifacts[].path] | unique | length' "$summary")"
  [ "$artifact_count" -gt 0 ] && [ "$unique_count" -eq "$artifact_count" ] ||
    fail "measured performance artifact inventory is empty or duplicated"

  while IFS=$'\t' read -r \
    relative expected_sha expected_bytes expected_mode; do
    safe_relative_path "$relative" ||
      fail "measured artifact path is unsafe: $relative"
    [ "$expected_mode" = "0600" ] ||
      fail "measured artifact declared a non-private mode: $relative"
    artifact_file="$summary_dir/$relative"
    require_private_file "$artifact_file"
    [ "$(file_bytes "$artifact_file")" = "$expected_bytes" ] ||
      fail "measured artifact byte count changed: $relative"
    [ "$(sha256_file "$artifact_file")" = "$expected_sha" ] ||
      fail "measured artifact digest changed: $relative"
    verified=$((verified + 1))
  done < <(
    jq -r '
      .artifacts[] |
      [.path,.sha256,(.bytes|tostring),.mode] | @tsv
    ' "$summary"
  )
  [ "$verified" -eq "$artifact_count" ] ||
    fail "measured artifact verification count is incomplete"
  printf '%s\n' "$artifact_count"
}

resolve_measured_summary_from_pointer() {
  local pointer="$performance_root/result.json" relative expected_sha resolved
  require_private_file "$pointer"
  jq -e '
    .schema == "hideout.release-candidate-performance-pointer/v1" and
    .result == "passed" and
    .candidateAcceptance == true and
    .source.dirty == false
  ' "$pointer" >/dev/null ||
    fail "default performance pointer is not an original accepted measurement"
  relative="$(jq -er '.summary' "$pointer")"
  expected_sha="$(jq -er '.summarySHA256' "$pointer")"
  safe_relative_path "$relative" || fail "performance pointer path is unsafe"
  resolved="$performance_root/$relative"
  require_private_file "$resolved"
  [ "$(sha256_file "$resolved")" = "$expected_sha" ] ||
    fail "performance pointer summary digest does not match"
  printf '%s\n' "$resolved"
}

validate_measured_summary() {
  local summary="$1" base_commit assessment_out="$2"
  require_private_file "$summary"
  jq -e '
    .schema == "hideout.release-candidate-performance/v1" and
    .result == "passed" and
    .candidateAcceptance == true and
    .source.dirty == false and
    .source.stableAcrossRun == true and
    (.source.commit | test("^[a-f0-9]{40}$")) and
    (.source.treeSHA256 | test("^[a-f0-9]{64}$")) and
    (.candidate.binarySHA256 | test("^[a-f0-9]{64}$")) and
    .candidate.exactSourceTreeBound == true and
    .candidate.acceptance == true
  ' "$summary" >/dev/null ||
    fail "measured performance summary is not an accepted exact-source result"
  base_commit="$(jq -er '.source.commit' "$summary")"
  git cat-file -e "$base_commit^{commit}" 2>/dev/null ||
    fail "measured performance commit is unavailable"
  jq -e -f "$assessment_filter" "$summary" >"$assessment_out" ||
    fail "measured performance workload CPU/wall assessment failed"
  chmod 0600 "$assessment_out"
}

verify_summary_reference() {
  local receipt="$1" measured_relative measured_file expected_sha expected_bytes
  measured_relative="$(jq -er '.measurement.summary.path' "$receipt")"
  safe_relative_path "$measured_relative" ||
    fail "reuse receipt measured summary path is unsafe"
  case "$measured_relative" in
    .artifacts/045/performance/run-*/summary.json) ;;
    *) fail "reuse receipt points outside an immutable performance run" ;;
  esac
  measured_file="$repo_root/$measured_relative"
  require_private_file "$measured_file"
  expected_sha="$(jq -er '.measurement.summary.sha256' "$receipt")"
  expected_bytes="$(jq -er '.measurement.summary.bytes' "$receipt")"
  [ "$(sha256_file "$measured_file")" = "$expected_sha" ] &&
    [ "$(file_bytes "$measured_file")" = "$expected_bytes" ] ||
    fail "reuse receipt measured summary reference changed"
  printf '%s\n' "$measured_file"
}

verify_pointer_reference() {
  local receipt="$1" measured_summary="$2" pointer_relative pointer_file
  local expected_sha expected_bytes expected_summary expected_summary_sha
  local base_commit
  pointer_relative="$(jq -er '.measurement.pointer.path' "$receipt")"
  safe_relative_path "$pointer_relative" ||
    fail "reuse receipt measured pointer path is unsafe"
  case "$pointer_relative" in
    .artifacts/045/performance/reuse-*/measurement-pointer.json) ;;
    *) fail "reuse receipt pointer copy is outside its immutable reuse run" ;;
  esac
  pointer_file="$repo_root/$pointer_relative"
  require_private_file "$pointer_file"
  expected_sha="$(jq -er '.measurement.pointer.sha256' "$receipt")"
  expected_bytes="$(jq -er '.measurement.pointer.bytes' "$receipt")"
  [ "$(sha256_file "$pointer_file")" = "$expected_sha" ] &&
    [ "$(file_bytes "$pointer_file")" = "$expected_bytes" ] ||
    fail "reuse receipt measured pointer reference changed"
  expected_summary="${measured_summary#"$performance_root"/}"
  expected_summary_sha="$(sha256_file "$measured_summary")"
  base_commit="$(jq -er '.source.commit' "$measured_summary")"
  jq -e \
    --arg commit "$base_commit" \
    --arg summary "$expected_summary" \
    --arg summarySHA256 "$expected_summary_sha" '
      .schema == "hideout.release-candidate-performance-pointer/v1" and
      .source.commit == $commit and
      .source.dirty == false and
      .result == "passed" and
      .summary == $summary and
      .summarySHA256 == $summarySHA256 and
      .candidateAcceptance == true
    ' "$pointer_file" >/dev/null ||
    fail "archived measured pointer is not the original accepted binding"
}

validate_clean_source() {
  [ -z "$(git status --porcelain=v1 --untracked-files=all)" ] ||
    fail "incremental performance revalidation requires a clean source tree"
}

check_receipt() {
  local receipt current_commit current_tree measured_file base_commit
  local assessment_file impact_file artifact_count base_normalized current_normalized
  receipt="$(canonical_repo_file "$1")"
  require_private_file "$receipt"
  validate_clean_source
  current_commit="$(git rev-parse HEAD)"
  current_tree="$(git rev-parse 'HEAD^{tree}')"
  go run ./cmd/hideout-schema-validate "$schema_path" "$receipt" >/dev/null ||
    fail "reuse receipt failed its versioned schema"
  jq -e \
    --arg commit "$current_commit" \
    --arg tree "$current_tree" '
      .source == {commit:$commit,tree:$tree,dirty:false} and
      .result == "passed" and
      .candidateAcceptance == true
    ' "$receipt" >/dev/null ||
    fail "reuse receipt is not bound to the current clean source"

  measured_file="$(verify_summary_reference "$receipt")"
  verify_pointer_reference "$receipt" "$measured_file"
  assessment_file="$scratch/checked-assessment.json"
  validate_measured_summary "$measured_file" "$assessment_file"
  jq -e --slurpfile actual "$assessment_file" '
    .assessment == $actual[0]
  ' "$receipt" >/dev/null ||
    fail "reuse receipt assessment is not independently reproducible"
  base_commit="$(jq -er '.measurement.sourceCommit' "$receipt")"
  jq -e \
    --arg commit "$base_commit" \
    --arg treeSHA256 "$(jq -er '.source.treeSHA256' "$measured_file")" \
    --arg binarySHA256 "$(jq -er '.candidate.binarySHA256' "$measured_file")" '
      .measurement.sourceCommit == $commit and
      .measurement.sourceTreeSHA256 == $treeSHA256 and
      .measurement.sourceDirty == false and
      .measurement.stableAcrossRun == true and
      .measurement.candidateBinarySHA256 == $binarySHA256
    ' "$receipt" >/dev/null ||
    fail "reuse receipt measurement identity is inconsistent"
  [ "$(jq -er '.source.commit' "$measured_file")" = "$base_commit" ] ||
    fail "reuse receipt substituted a different measurement commit"
  git merge-base --is-ancestor "$base_commit" "$current_commit" ||
    fail "measured performance commit is not an ancestor of current source"

  impact_file="$scratch/checked-impact.json"
  collect_impact "$base_commit" "$current_commit" "$impact_file"
  jq -e --slurpfile actual "$impact_file" '
    .impact.policy == "reviewed-evidence-only-v1" and
    .impact.changedFiles == $actual[0] and
    .impact.productOrMeasurementChanges == []
  ' "$receipt" >/dev/null ||
    fail "reuse receipt impact set does not match the exact Git diff"

  base_normalized="$(normalized_entrypoint_at_commit "$base_commit")"
  current_normalized="$(normalized_current_entrypoint)"
  [ "$base_normalized" = "$current_normalized" ] ||
    fail "normalized performance measurement entrypoint changed"
  jq -e \
    --arg base "$base_normalized" \
    --arg current "$current_normalized" '
      .impact.performanceEntrypoint == {
        path:"scripts/gates/release-candidate-performance.sh",
        baseNormalizedSHA256:$base,
        currentNormalizedSHA256:$current,
        identical:true
      }
    ' "$receipt" >/dev/null ||
    fail "reuse receipt entrypoint equivalence is invalid"

  artifact_count="$(verify_summary_artifacts "$measured_file")"
  jq -e --argjson artifactCount "$artifact_count" '
    .artifactVerification == {
      artifactCount:$artifactCount,
      uniquePaths:true,
      allDigestsVerified:true,
      allByteCountsVerified:true,
      allModesPrivate:true
    }
  ' "$receipt" >/dev/null ||
    fail "reuse receipt artifact verification is inconsistent"
}

run_preflight() {
  local fixture_root old_entrypoint current_entrypoint mutant_entrypoint
  local old_sha current_sha mutant_sha
  local receipt_fixture invalid_receipt
  fixture_root="$scratch/preflight"
  mkdir -p "$fixture_root"
  old_entrypoint="$fixture_root/old.sh"
  current_entrypoint="$fixture_root/current.sh"
  mutant_entrypoint="$fixture_root/mutant.sh"
  printf '%s\n' '#!/usr/bin/env bash' 'before' '' 'measurement' >"$old_entrypoint"
  printf '%s\n' \
    '#!/usr/bin/env bash' \
    'before' \
    '' \
    '# HIDEOUT_INCREMENTAL_PERFORMANCE_IGNORE_BEGIN fixture' \
    'evidence-only-preflight' \
    '# HIDEOUT_INCREMENTAL_PERFORMANCE_IGNORE_END fixture' \
    '' \
    'measurement' >"$current_entrypoint"
  printf '%s\n' \
    '#!/usr/bin/env bash' \
    'before' \
    '' \
    '# HIDEOUT_INCREMENTAL_PERFORMANCE_IGNORE_BEGIN fixture' \
    'evidence-only-preflight' \
    '# HIDEOUT_INCREMENTAL_PERFORMANCE_IGNORE_END fixture' \
    '' \
    'changed-measurement' >"$mutant_entrypoint"
  old_sha="$(normalize_performance_entrypoint <"$old_entrypoint" | sha256_stream)"
  current_sha="$(
    normalize_performance_entrypoint <"$current_entrypoint" | sha256_stream
  )"
  mutant_sha="$(
    normalize_performance_entrypoint <"$mutant_entrypoint" | sha256_stream
  )"
  [ "$old_sha" = "$current_sha" ] ||
    fail "evidence-only entrypoint fixture was not normalized"
  [ "$old_sha" != "$mutant_sha" ] ||
    fail "measurement mutant was hidden by entrypoint normalization"
  printf '%s\n' \
    '# HIDEOUT_INCREMENTAL_PERFORMANCE_IGNORE_BEGIN unclosed' \
    'hidden' >"$fixture_root/unclosed.sh"
  if normalize_performance_entrypoint \
    <"$fixture_root/unclosed.sh" >/dev/null 2>&1; then
    fail "unclosed entrypoint ignore marker was accepted"
  fi
  validate_approved_entrypoint_ignore_blocks \
    scripts/gates/release-candidate-performance.sh ||
    fail "approved current entrypoint ignore blocks were rejected"
  if validate_approved_entrypoint_ignore_blocks "$current_entrypoint"; then
    fail "arbitrary entrypoint ignore block was accepted"
  fi
  sed 's/collector drift fails cheaply/collector drift is ignored/' \
    scripts/gates/release-candidate-performance.sh \
    >"$fixture_root/approved-block-mutant.sh"
  if validate_approved_entrypoint_ignore_blocks \
    "$fixture_root/approved-block-mutant.sh"; then
    fail "mutated approved entrypoint ignore block was accepted"
  fi
  allowed_evidence_only_change \
    scripts/release/collect-evidence.sh ||
    fail "reviewed evidence-only path was rejected"
  if allowed_evidence_only_change internal/app/app.go ||
    allowed_evidence_only_change scripts/lib/gate2-concurrent-performance.sh ||
    allowed_evidence_only_change ../escape; then
    fail "product, measurement, or unsafe path was accepted for reuse"
  fi
  receipt_fixture="$fixture_root/receipt.json"
  invalid_receipt="$fixture_root/receipt-invalid.json"
  jq -n '
    def confidence: {
      level:0.95,
      method:"one-sided-exact-binomial-order-statistic",
      rank:20,
      upperBound:5,
      thresholdPassed:true
    };
    def metric($scope): {
      scope:$scope,
      samples:30,
      unit:"percent",
      pairedOverhead:[range(0; 30) | 5],
      median:5,
      threshold:10,
      confidence:confidence
    };
    {
      schema:"hideout.release-candidate-performance-reuse/v1",
      generatedAt:"2026-08-01T00:00:00Z",
      result:"passed",
      candidateAcceptance:true,
      mode:"incremental-content-revalidation",
      source:{commit:("a"*40),tree:("b"*40),dirty:false},
      measurement:{
        sourceCommit:("c"*40),
        sourceTreeSHA256:("d"*64),
        sourceDirty:false,
        stableAcrossRun:true,
        candidateBinarySHA256:("e"*64),
        pointer:{
          path:
            ".artifacts/045/performance/reuse-fixture/measurement-pointer.json",
          sha256:("f"*64),bytes:1,mode:"0600"
        },
        summary:{
          path:".artifacts/045/performance/run-fixture/summary.json",
          sha256:("1"*64),bytes:1,mode:"0600"
        }
      },
      impact:{
        policy:"reviewed-evidence-only-v1",
        changedFiles:[{
          path:"docs/release/045-readiness.md",
          status:"M",
          beforeSHA256:("2"*64),
          afterSHA256:("3"*64)
        }],
        productOrMeasurementChanges:[],
        performanceEntrypoint:{
          path:"scripts/gates/release-candidate-performance.sh",
          baseNormalizedSHA256:("4"*64),
          currentNormalizedSHA256:("4"*64),
          identical:true
        }
      },
      assessment:{
        schema:"hideout.performance-evidence-assessment/v1",
        result:"passed",
        targetWorkloadCPU:(metric("reference-workload-child-process") + {
          source:"getrusage(RUSAGE_CHILDREN)",
          medianUserDeltaMs:1,
          medianSystemDeltaMs:1,
          medianTotalDeltaMs:2,
          medianInvoluntaryContextSwitchDelta:0,
          producerAcceptanceFilter:false,
          independentlyAccepted:true
        }),
        elapsedTime:metric("reference-workload-paired-wall-clock"),
        hostContention:{
          role:"eligibility-and-invalidation-only",
          initialPassed:true,
          continuousPassed:true,
          continuousSamples:30
        },
        observationIntegrity:{noReportedLoss:true}
      },
      artifactVerification:{
        artifactCount:545,
        uniquePaths:true,
        allDigestsVerified:true,
        allByteCountsVerified:true,
        allModesPrivate:true
      },
      limitations:["one","two"]
    }
  ' >"$receipt_fixture"
  chmod 0600 "$receipt_fixture"
  [ "$(file_mode "$receipt_fixture")" = "0600" ] ||
    fail "private file mode was not normalized as octal 0600"
  chmod 0644 "$receipt_fixture"
  [ "$(file_mode "$receipt_fixture")" = "0644" ] ||
    fail "public file mode was not normalized as octal 0644"
  chmod 0600 "$receipt_fixture"
  go run ./cmd/hideout-schema-validate \
    "$schema_path" "$receipt_fixture" >/dev/null ||
    fail "valid incremental performance receipt fixture was rejected"
  jq '.impact.productOrMeasurementChanges = ["internal/app/app.go"]' \
    "$receipt_fixture" >"$invalid_receipt"
  if go run ./cmd/hideout-schema-validate \
    "$schema_path" "$invalid_receipt" >/dev/null 2>&1; then
    fail "performance receipt accepted a product change"
  fi
  jq '.impact.changedFiles[0].status = "D"' \
    "$receipt_fixture" >"$invalid_receipt"
  if go run ./cmd/hideout-schema-validate \
    "$schema_path" "$invalid_receipt" >/dev/null 2>&1; then
    fail "performance receipt accepted a destructive change status"
  fi
  bash -n scripts/release/revalidate-performance-evidence.sh
  gate_completed=1
  printf 'performance-revalidate: preflight=passed\n'
}

cleanup() {
  local exit_status=$?
  if [ "$exit_status" -ne 0 ] && [ -n "${created_run_dir:-}" ]; then
    case "$created_run_dir" in
      "$performance_root"/reuse-*)
        [ ! -d "$created_run_dir" ] ||
          find "$created_run_dir" -depth -delete
        ;;
      *)
        printf \
          'performance-revalidate: refusing unexpected failed-run cleanup: %s\n' \
          "$created_run_dir" >&2
        exit_status=1
        ;;
    esac
  fi
  if [ -n "${scratch:-}" ]; then
    case "$scratch" in
      "${TMPDIR:-/tmp}"/hideout-performance-revalidate.*)
        [ ! -d "$scratch" ] || find "$scratch" -depth -delete
        ;;
      *)
        printf 'performance-revalidate: refusing unexpected scratch cleanup: %s\n' \
          "$scratch" >&2
        exit_status=1
        ;;
    esac
  fi
  if [ "$exit_status" -eq 0 ]; then
    gate_require_completion "performance-revalidate"
  fi
  return "$exit_status"
}

scratch="$(mktemp -d "${TMPDIR:-/tmp}/hideout-performance-revalidate.XXXXXX")"
trap cleanup EXIT

if [ "$preflight_only" -eq 1 ]; then
  run_preflight
  exit 0
fi

if [ -n "$check_receipt_path" ]; then
  check_receipt "$check_receipt_path"
  gate_completed=1
  printf 'performance-revalidate: check=passed receipt=%s\n' \
    "$(canonical_repo_file "$check_receipt_path")"
  exit 0
fi

validate_clean_source
current_commit="$(git rev-parse HEAD)"
current_tree="$(git rev-parse 'HEAD^{tree}')"
measured_pointer_path="$performance_root/result.json"
measured_summary_path="$(resolve_measured_summary_from_pointer)"
assessment_file="$scratch/assessment.json"
validate_measured_summary "$measured_summary_path" "$assessment_file"
base_commit="$(jq -er '.source.commit' "$measured_summary_path")"
[ "$base_commit" != "$current_commit" ] ||
  fail "current source already has exact performance evidence; reuse is unnecessary"
git merge-base --is-ancestor "$base_commit" "$current_commit" ||
  fail "measured performance commit is not an ancestor of current source"
impact_file="$scratch/impact.json"
collect_impact "$base_commit" "$current_commit" "$impact_file"
base_normalized="$(normalized_entrypoint_at_commit "$base_commit")"
current_normalized="$(normalized_current_entrypoint)"
[ "$base_normalized" = "$current_normalized" ] ||
  fail "normalized performance measurement entrypoint changed"
artifact_count="$(verify_summary_artifacts "$measured_summary_path")"
summary_ref="$(artifact_ref "$measured_summary_path")"
generated_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
run_id="reuse-$(date -u +'%Y%m%dT%H%M%SZ')-$$"
run_dir="$performance_root/$run_id"
[ ! -e "$run_dir" ] || fail "reuse run directory already exists"
pointer_copy_relative=".artifacts/045/performance/$run_id/measurement-pointer.json"
pointer_ref="$(
  jq -nc \
    --arg path "$pointer_copy_relative" \
    --arg sha256 "$(sha256_file "$measured_pointer_path")" \
    --argjson bytes "$(file_bytes "$measured_pointer_path")" '
      {path:$path,sha256:$sha256,bytes:$bytes,mode:"0600"}
    '
)"
receipt_tmp="$scratch/summary.json"
jq -n \
  --arg generatedAt "$generated_at" \
  --arg commit "$current_commit" \
  --arg tree "$current_tree" \
  --arg measurementCommit "$base_commit" \
  --arg measurementTreeSHA256 \
    "$(jq -er '.source.treeSHA256' "$measured_summary_path")" \
  --arg candidateBinarySHA256 \
    "$(jq -er '.candidate.binarySHA256' "$measured_summary_path")" \
  --argjson pointer "$pointer_ref" \
  --argjson summary "$summary_ref" \
  --slurpfile changedFiles "$impact_file" \
  --arg baseNormalizedSHA256 "$base_normalized" \
  --arg currentNormalizedSHA256 "$current_normalized" \
  --slurpfile assessment "$assessment_file" \
  --argjson artifactCount "$artifact_count" '
    {
      schema:"hideout.release-candidate-performance-reuse/v1",
      generatedAt:$generatedAt,
      result:"passed",
      candidateAcceptance:true,
      mode:"incremental-content-revalidation",
      source:{commit:$commit,tree:$tree,dirty:false},
      measurement:{
        sourceCommit:$measurementCommit,
        sourceTreeSHA256:$measurementTreeSHA256,
        sourceDirty:false,
        stableAcrossRun:true,
        candidateBinarySHA256:$candidateBinarySHA256,
        pointer:$pointer,
        summary:$summary
      },
      impact:{
        policy:"reviewed-evidence-only-v1",
        changedFiles:$changedFiles[0],
        productOrMeasurementChanges:[],
        performanceEntrypoint:{
          path:"scripts/gates/release-candidate-performance.sh",
          baseNormalizedSHA256:$baseNormalizedSHA256,
          currentNormalizedSHA256:$currentNormalizedSHA256,
          identical:true
        }
      },
      assessment:$assessment[0],
      artifactVerification:{
        artifactCount:$artifactCount,
        uniquePaths:true,
        allDigestsVerified:true,
        allByteCountsVerified:true,
        allModesPrivate:true
      },
      limitations:[
        "This receipt is valid only for the exact retained measurement summary and the exact reviewed evidence-only Git diff recorded here.",
        "Host-wide process samples are eligibility and invalidation evidence; acceptance is independently recomputed from the target workload child-process CPU and paired wall-clock samples."
      ]
    }
  ' >"$receipt_tmp"
chmod 0600 "$receipt_tmp"
go run ./cmd/hideout-schema-validate "$schema_path" "$receipt_tmp" >/dev/null ||
  fail "generated reuse receipt failed its versioned schema"

[ -d "$performance_root" ] && [ ! -L "$performance_root" ] ||
  fail "performance evidence root is missing or unsafe"
mkdir -p "$run_dir"
chmod 0700 "$run_dir"
created_run_dir="$run_dir"
cp "$measured_pointer_path" "$run_dir/measurement-pointer.json"
chmod 0600 "$run_dir/measurement-pointer.json"
[ "$(sha256_file "$run_dir/measurement-pointer.json")" = \
  "$(jq -r '.sha256' <<<"$pointer_ref")" ] ||
  fail "archived measured pointer copy changed"
receipt="$run_dir/summary.json"
mv -- "$receipt_tmp" "$receipt"
chmod 0600 "$receipt"
check_receipt "$receipt"

pointer_tmp="$scratch/result.json"
summary_relative="${receipt#"$performance_root"/}"
jq -n \
  --arg generatedAt "$generated_at" \
  --arg commit "$current_commit" \
  --arg tree "$current_tree" \
  --arg run "$run_id" \
  --arg summary "$summary_relative" \
  --arg summarySHA256 "$(sha256_file "$receipt")" '
    {
      schema:"hideout.release-candidate-performance-pointer/v2",
      generatedAt:$generatedAt,
      source:{commit:$commit,tree:$tree,dirty:false},
      result:"passed",
      mode:"incremental-content-revalidation",
      run:$run,
      summary:$summary,
      summarySHA256:$summarySHA256,
      candidateAcceptance:true
    }
  ' >"$pointer_tmp"
chmod 0600 "$pointer_tmp"
jq -e \
  --arg commit "$current_commit" \
  --arg tree "$current_tree" \
  --arg summary "$summary_relative" \
  --arg digest "$(sha256_file "$receipt")" '
    .schema == "hideout.release-candidate-performance-pointer/v2" and
    .source == {commit:$commit,tree:$tree,dirty:false} and
    .result == "passed" and
    .mode == "incremental-content-revalidation" and
    .summary == $summary and
    .summarySHA256 == $digest and
    .candidateAcceptance == true
  ' "$pointer_tmp" >/dev/null ||
  fail "generated reuse pointer failed semantic validation"
mv -- "$pointer_tmp" "$performance_root/result.json"
chmod 0600 "$performance_root/result.json"
gate_completed=1
printf \
  'performance-revalidate: passed receipt=%s pointer=%s measured-commit=%s current-commit=%s\n' \
  "$receipt" "$performance_root/result.json" "$base_commit" "$current_commit"
