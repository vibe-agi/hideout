#!/usr/bin/env bash
set -euo pipefail

root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd -P)"
review_root="$root/.artifacts/045/formal/reviews"
step_outcome=""

usage() {
  cat <<'USAGE'
Usage: scripts/gates/formal-review-finalize.sh --out DIR --outcome OUTCOME
       scripts/gates/formal-review-finalize.sh --self-test

Finalize the one formal run review left by the current GitHub Actions job.
OUTCOME is success, failure, or cancelled. A running progress receipt is
converted to a failed harness review after interruption; a missing or
contradictory receipt is rejected.
USAGE
}

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
    return
  fi
  sha256sum "$1" | awk '{print $1}'
}

finalize_reviews() {
  local directory="$1" outcome="$2" finalized_at review current_result
  local review_tmp
  local -a reviews=()

  case "$outcome" in
    success | failure | cancelled) ;;
    *)
      printf 'formal-review-finalize: unsupported step outcome: %s\n' \
        "$outcome" >&2
      return 2
      ;;
  esac
  [ -d "$directory" ] || {
    printf 'formal-review-finalize: review directory is missing: %s\n' \
      "$directory" >&2
    return 1
  }
  while IFS= read -r -d '' review; do
    reviews+=("$review")
  done < <(find "$directory" -type f -name run-review.json -print0)
  if [ "${#reviews[@]}" -ne 1 ]; then
    printf \
      'formal-review-finalize: expected one run review, found %s under %s\n' \
      "${#reviews[@]}" "$directory" >&2
    return 1
  fi
  review="${reviews[0]}"
  current_result="$(
    jq -er '
      if .schema == "hideout.gate-run-review/v1" and
          .gate == "formal" and
          (.result == "running" or .result == "passed" or .result == "failed")
      then .result
      else error("invalid formal run review")
      end
    ' "$review"
  )"

  if [ "$outcome" = "success" ]; then
    [ "$current_result" = "passed" ] || {
      printf \
        'formal-review-finalize: successful step retained %s review\n' \
        "$current_result" >&2
      return 1
    }
  elif [ "$current_result" = "running" ]; then
    finalized_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
    review_tmp="${review}.tmp.$$"
    jq \
      --arg outcome "$outcome" \
      --arg finalizedAt "$finalized_at" '
        .execution.currentConfiguration as $configuration |
        .execution.currentConfigurationStartedAt as $configurationStartedAt |
        .execution.workers as $workers |
        .execution.maxHeapMB as $maxHeapMB |
        .result = "failed" |
        .execution.interruptedOutcome = $outcome |
        .execution.currentConfigurationElapsedSeconds =
          (if $configurationStartedAt == null then 0
           else (($finalizedAt | fromdateiso8601) -
             ($configurationStartedAt | fromdateiso8601))
           end) |
        .timing.finishedAt = $finalizedAt |
        .timing.reviewFinalizedAt = $finalizedAt |
        .timing.elapsedSeconds =
          (($finalizedAt | fromdateiso8601) -
            (.timing.startedAt | fromdateiso8601)) |
        .failure = {
          firstObservedLayer:"harness",
          reason:("github-actions-step-" + $outcome +
            " after the last atomic progress receipt")
        } |
        .rerun = {
          minimumDiagnosticScope:
            (if $configuration == null then "failed-stage-only"
             else ("configuration:" + $configuration)
             end),
          diagnosticCommand:
            (if $configuration == null then null
             else ("HIDEOUT_TLC_MAX_HEAP_MB=" + ($maxHeapMB | tostring) +
               " scripts/gates/formal.sh --configuration " +
               $configuration + " --workers " + ($workers | tostring))
             end),
          releaseAcceptanceScope:"full-formal",
          afterCandidateChange:"from-scratch"
        } |
        .efficiency.preventableWorkAssessment =
          "hard-stop progress receipt recovered; review timeout and worker policy before rerun"
      ' "$review" >"$review_tmp"
    chmod 0600 "$review_tmp"
    mv "$review_tmp" "$review"
    current_result="failed"
  elif [ "$current_result" != "failed" ]; then
    printf \
      'formal-review-finalize: %s step retained contradictory %s review\n' \
      "$outcome" "$current_result" >&2
    return 1
  fi

  jq -e \
    --arg expected "$current_result" \
    '.schema == "hideout.gate-run-review/v1" and
      .gate == "formal" and .result == $expected and
      (.candidate.commit | test("^[0-9a-f]{40}$")) and
      (.timing.elapsedSeconds >= 0) and
      (.execution.completedConfigurations | type) == "array"' \
    "$review" >/dev/null
  printf \
    'formal-review-finalize: outcome=%s result=%s review=%s sha256=%s\n' \
    "$outcome" "$current_result" "$review" "$(sha256_file "$review")"
}

self_test() {
  local scratch running_dir passed_dir review
  scratch="$(mktemp -d "${TMPDIR:-/tmp}/hideout-formal-review-finalize.XXXXXX")"
  trap 'rm -rf -- "$scratch"' EXIT

  running_dir="$scratch/running/run-1"
  mkdir -p "$running_dir"
  review="$running_dir/run-review.json"
  jq -n '
    {
      schema:"hideout.gate-run-review/v1",
      gate:"formal",
      result:"running",
      candidate:{commit:("a" * 40),dirty:false},
      execution:{
        scope:"full-formal",
        workers:1,
        maxHeapMB:3072,
        stage:"formal-model",
        currentConfiguration:"WorkloadObservation",
        currentConfigurationStartedAt:"2026-08-04T20:22:04Z",
        currentConfigurationElapsedSeconds:0,
        completedConfigurations:[{id:"SecretTransition",result:"passed"}]
      },
      timing:{
        startedAt:"2026-08-04T20:13:33Z",
        snapshotAt:"2026-08-04T20:22:04Z",
        finishedAt:null,
        elapsedSeconds:511
      },
      efficiency:{preventableWorkAssessment:"pending-post-run-review"}
    }
  ' >"$review"
  chmod 0600 "$review"
  finalize_reviews "$scratch/running" cancelled >/dev/null
  jq -e '
    .result == "failed" and
    .failure.firstObservedLayer == "harness" and
    .execution.interruptedOutcome == "cancelled" and
    .rerun.minimumDiagnosticScope ==
      "configuration:WorkloadObservation" and
    .rerun.diagnosticCommand ==
      "HIDEOUT_TLC_MAX_HEAP_MB=3072 scripts/gates/formal.sh --configuration WorkloadObservation --workers 1" and
    .timing.finishedAt != null and
    .timing.elapsedSeconds >= 511 and
    .execution.currentConfigurationElapsedSeconds >= 0
  ' "$review" >/dev/null

  passed_dir="$scratch/passed/run-2"
  mkdir -p "$passed_dir"
  jq -n '
    {
      schema:"hideout.gate-run-review/v1",
      gate:"formal",
      result:"passed",
      candidate:{commit:("b" * 40),dirty:false},
      execution:{completedConfigurations:[]},
      timing:{elapsedSeconds:1}
    }
  ' >"$passed_dir/run-review.json"
  finalize_reviews "$scratch/passed" success >/dev/null
  if finalize_reviews "$scratch/missing" failure >/dev/null 2>&1; then
    printf 'formal-review-finalize: missing-review fixture was accepted\n' >&2
    return 1
  fi
  rm -rf -- "$scratch"
  trap - EXIT
  printf 'formal-review-finalize self-test: passed\n'
}

if [ "${1:-}" = "--self-test" ]; then
  [ "$#" -eq 1 ] || { usage >&2; exit 2; }
  self_test
  exit 0
fi

while [ "$#" -gt 0 ]; do
  case "$1" in
    --out)
      [ "$#" -ge 2 ] || { usage >&2; exit 2; }
      review_root="$2"
      shift 2
      ;;
    --outcome)
      [ "$#" -ge 2 ] || { usage >&2; exit 2; }
      step_outcome="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'formal-review-finalize: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

[ -n "$step_outcome" ] || { usage >&2; exit 2; }
finalize_reviews "$review_root" "$step_outcome"
