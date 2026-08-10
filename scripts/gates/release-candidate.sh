#!/usr/bin/env bash
set -euo pipefail

# This aggregate is verification-only. Do not inherit a workstation-wide
# module-write policy before the later tidy lane gets a chance to inspect it.
export GOFLAGS=-mod=readonly

root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd -P)"
cd "$root"
# shellcheck source=scripts/lib/gate-result.sh
. "$root/scripts/lib/gate-result.sh"
gate_completed=0
gate_review_started=0
gate_review_result=""
gate_stage="initialization"

out="$root/.artifacts/045/local"
inventory="$root/scripts/gates/release-candidate-inventory.json"
preflight_only=0
run_scope="full-gate"

usage() {
  printf '%s\n' \
    "Usage: scripts/gates/release-candidate.sh [--out DIR] [--preflight]" \
    "" \
    "Runs the complete local schema, static, release-blocker, generated," \
    "dependency/advisory, unit, race, fuzz/property, migration, and mutation" \
    "aggregate in diagnostic-cost order. A failing lane writes private digest-bound" \
    "evidence and stops before later work; a passing run executes all ten lanes." \
    "The run records its start/reuse decision and a measured post-run review." \
    "Use --preflight to validate the review protocol and every cheap" \
    "release-blocker preflight without running generated/unit/race/fuzz lanes." \
    "This command never publishes or accepts an exact release candidate."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --out)
      if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
        printf 'release-candidate-local: --out requires a directory\n' >&2
        exit 2
      fi
      out="$2"
      shift 2
      ;;
    --preflight)
      preflight_only=1
      run_scope="preflight-only"
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'release-candidate-local: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

for command in \
  awk bash cmp comm find git go gofmt grep jq markdownlint-cli2 sed shellcheck \
  sort stat tr wc xargs; do
  if ! command -v "$command" >/dev/null 2>&1; then
    printf 'release-candidate-local: missing required command: %s\n' \
      "$command" >&2
    exit 1
  fi
done

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  printf 'release-candidate-local: missing shasum or sha256sum\n' >&2
  return 127
}

sha256_text() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 | awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | awk '{print $1}'
    return
  fi
  printf 'release-candidate-local: missing shasum or sha256sum\n' >&2
  return 127
}

file_mode() {
  stat -f '%Lp' "$1" 2>/dev/null ||
    stat -c '%a' "$1" 2>/dev/null
}

source_commit="$(git rev-parse HEAD)"
source_tree="$(git rev-parse 'HEAD^{tree}')"
if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then
  source_dirty=true
else
  source_dirty=false
fi

gate_review_started_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
gate_review_started_epoch="$(date +%s)"
host_boot_marker=""
if [ -r /proc/sys/kernel/random/boot_id ]; then
  host_boot_marker="$(tr -d '\n' </proc/sys/kernel/random/boot_id)"
elif command -v sysctl >/dev/null 2>&1; then
  host_boot_marker="$(sysctl -n kern.boottime 2>/dev/null || true)"
fi
if [ -n "$host_boot_marker" ]; then
  host_boot_fingerprint="$(printf '%s' "$host_boot_marker" | sha256_text)"
else
  host_boot_fingerprint=""
fi

mkdir -p "$out"
out="$(CDPATH='' cd -- "$out" && pwd -P)"
run_id="run-$(date -u +'%Y%m%dT%H%M%SZ')-$$"
run_dir="$out/$run_id"
mkdir -p \
  "$run_dir/lanes" \
  "$run_dir/dependencies" \
  "$run_dir/mutations/production" \
  "$run_dir/mutations/recovery" \
  "$run_dir/mutations/judge-negative-fixtures"
chmod 0700 \
  "$out" \
  "$run_dir" \
  "$run_dir/lanes" \
  "$run_dir/dependencies" \
  "$run_dir/mutations" \
  "$run_dir/mutations/production" \
  "$run_dir/mutations/recovery" \
  "$run_dir/mutations/judge-negative-fixtures"

scratch="$(mktemp -d "${TMPDIR:-/tmp}/hideout-release-local.XXXXXX")"
gate_review_started=1

start_mode="from-scratch"
start_reason="no authenticated same-candidate lane checkpoint was selected"
host_continuity="not-comparable"
previous_review_json='null'
previous_lanes='[]'
first_failure_epoch=0
first_failure_lane=""
lanes='[]'

write_gate_run_review() {
  local result="$1" failure_layer="${2:-}" failure_reason="${3:-}"
  [ "${gate_review_started:-0}" -eq 1 ] &&
    [ -n "${run_dir:-}" ] && [ -d "$run_dir" ] || return 0
  local finished_at finished_epoch elapsed_seconds diagnostic_seconds
  local after_diagnostic_seconds repeated_lanes rerun_amplification
  local completed_lanes failed_lane_count review_tmp
  finished_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
  finished_epoch="$(date +%s)"
  elapsed_seconds=$((finished_epoch - gate_review_started_epoch))
  if [ "${first_failure_epoch:-0}" -gt 0 ]; then
    diagnostic_seconds=$((first_failure_epoch - gate_review_started_epoch))
    after_diagnostic_seconds=$((finished_epoch - first_failure_epoch))
  else
    diagnostic_seconds="$elapsed_seconds"
    after_diagnostic_seconds=0
  fi
  completed_lanes="$(jq 'length' <<<"${lanes:-[]}")"
  failed_lane_count="$(
    jq '[.[] | select(.result == "failed")] | length' <<<"${lanes:-[]}"
  )"
  repeated_lanes="$(
    jq -n \
      --argjson previous "${previous_lanes:-[]}" \
      --argjson current "${lanes:-[]}" '
        ($previous | map(select(.result == "passed") | .id)) as $passed |
        [$current[].id | select(. as $id | $passed | index($id))] | unique
      '
  )"
  rerun_amplification="$(
    jq -n \
      --argjson previous "${previous_lanes:-[]}" \
      --argjson completed "$completed_lanes" '
        ([$previous[] | select(.result == "failed")] | length) as $failed |
        if $failed > 0 then ($completed / $failed) else null end
      '
  )"
  review_tmp="$run_dir/.run-review.$$.json"
  jq -n \
    --arg result "$result" \
    --arg scope "$run_scope" \
    --arg commit "$source_commit" \
    --arg tree "$source_tree" \
    --argjson dirty "$source_dirty" \
    --arg gateSHA256 "${gate_source_sha:-}" \
    --arg inventorySHA256 "${inventory_sha:-}" \
    --arg goVersion "${actual_go_version:-unknown}" \
    --arg startedAt "$gate_review_started_at" \
    --arg finishedAt "$finished_at" \
    --arg startMode "$start_mode" \
    --arg startReason "$start_reason" \
    --arg hostBootFingerprint "$host_boot_fingerprint" \
    --arg hostContinuity "$host_continuity" \
    --arg stage "$gate_stage" \
    --arg failureLayer "$failure_layer" \
    --arg failureReason "$failure_reason" \
    --arg firstFailureLane "${first_failure_lane:-}" \
    --argjson previousReview "$previous_review_json" \
    --argjson lanes "${lanes:-[]}" \
    --argjson elapsedSeconds "$elapsed_seconds" \
    --argjson diagnosticSeconds "$diagnostic_seconds" \
    --argjson afterDiagnosticSeconds "$after_diagnostic_seconds" \
    --argjson completedLanes "$completed_lanes" \
    --argjson failedLanes "$failed_lane_count" \
    --argjson repeatedLanes "$repeated_lanes" \
    --argjson rerunAmplification "$rerun_amplification" '
      {
        schema:"hideout.gate-run-review/v1",
        gate:"release-candidate-local",
        scope:$scope,
        result:$result,
        candidate:{commit:$commit,tree:$tree,dirty:$dirty},
        bindings:{gateSHA256:$gateSHA256,inventorySHA256:$inventorySHA256,go:$goVersion},
        timing:{
          startedAt:$startedAt,
          finishedAt:$finishedAt,
          elapsedSeconds:$elapsedSeconds,
          timeToFirstUsefulDiagnosticSeconds:$diagnosticSeconds,
          workAfterFirstDiagnosticSeconds:$afterDiagnosticSeconds
        },
        host:{
          bootFingerprint:(if $hostBootFingerprint == "" then null else $hostBootFingerprint end),
          continuity:$hostContinuity
        },
        start:{
          mode:$startMode,
          reason:$startReason,
          checkpointReused:false,
          resultReused:false,
          previousReview:$previousReview
        },
        execution:{stage:$stage,completedLanes:$completedLanes,failedLanes:$failedLanes,lanes:$lanes},
        failure:(if $result == "failed" then {
          firstObservedLayer:$failureLayer,
          firstFailureLane:(if $firstFailureLane == "" then null else $firstFailureLane end),
          reason:$failureReason,
          rootCauseClassification:"pending-post-run-review"
        } else null end),
        rerun:(if $result == "failed" then {
          minimumDiagnosticScope:(if $failedLanes > 0 then "failed-lanes-only" else "failed-preflight-only" end),
          releaseAcceptanceScope:"full-gate",
          withoutCandidateChange:"same-candidate-retry",
          afterCandidateChange:"from-scratch"
        } else null end),
        efficiency:{
          repeatedPassedLanes:$repeatedLanes,
          repeatedPassedLaneCount:($repeatedLanes | length),
          rerunAmplification:$rerunAmplification,
          authenticatedCheckpointHitRate:0,
          preventableWorkAssessment:"pending-post-run-review",
          metrics:[
            "elapsedSeconds",
            "timeToFirstUsefulDiagnosticSeconds",
            "workAfterFirstDiagnosticSeconds",
            "repeatedPassedLaneCount",
            "rerunAmplification",
            "authenticatedCheckpointHitRate"
          ]
        }
      }
    ' >"$review_tmp"
  chmod 0600 "$review_tmp"
  mv "$review_tmp" "$run_dir/run-review.json"
  gate_review_result="$result"
}

gate_run_review_self_test() {
  local self_test_dir="$scratch/run-review-self-test"
  local self_test_status=0
  local saved_run_dir="$run_dir"
  local saved_gate_review_started="$gate_review_started"
  local saved_gate_review_started_at="$gate_review_started_at"
  local saved_gate_review_started_epoch="$gate_review_started_epoch"
  local saved_gate_review_result="$gate_review_result"
  local saved_gate_stage="$gate_stage"
  local saved_start_mode="$start_mode"
  local saved_start_reason="$start_reason"
  local saved_host_continuity="$host_continuity"
  local saved_previous_review_json="$previous_review_json"
  local saved_previous_lanes="$previous_lanes"
  local saved_lanes="$lanes"
  local saved_first_failure_epoch="$first_failure_epoch"
  local saved_first_failure_lane="$first_failure_lane"

  set +e
  mkdir -p "$self_test_dir"
  chmod 0700 "$self_test_dir"
  run_dir="$self_test_dir"
  gate_review_started=1
  gate_review_started_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
  gate_review_started_epoch="$(date +%s)"
  gate_stage="lane:race"
  start_mode="same-candidate-retry"
  start_reason="review writer self-test"
  host_continuity="same-boot-session"
  previous_review_json='{"path":"prior/run-review.json","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","result":"failed"}'
  previous_lanes='[{"id":"unit","result":"passed"},{"id":"race","result":"failed"}]'
  lanes='[{"id":"unit","result":"passed"},{"id":"race","result":"failed"}]'
  first_failure_epoch="$gate_review_started_epoch"
  first_failure_lane="race"

  write_gate_run_review passed "" "" || self_test_status=1
  jq -e '
    .result == "passed" and
    .failure == null and
    .start.mode == "same-candidate-retry" and
    .execution.completedLanes == 2
  ' "$run_dir/run-review.json" >/dev/null || self_test_status=1

  write_gate_run_review failed evidence-judge "self-test rejection" ||
    self_test_status=1
  [ "$(file_mode "$run_dir/run-review.json")" = "600" ] ||
    self_test_status=1
  jq -e '
    .schema == "hideout.gate-run-review/v1" and
    .gate == "release-candidate-local" and
    .result == "failed" and
    .failure.firstObservedLayer == "evidence-judge" and
    .failure.firstFailureLane == "race" and
    .rerun.minimumDiagnosticScope == "failed-lanes-only" and
    .efficiency.repeatedPassedLanes == ["unit"] and
    .efficiency.rerunAmplification == 2 and
    .efficiency.authenticatedCheckpointHitRate == 0
  ' "$run_dir/run-review.json" >/dev/null || self_test_status=1

  run_dir="$saved_run_dir"
  gate_review_started="$saved_gate_review_started"
  gate_review_started_at="$saved_gate_review_started_at"
  gate_review_started_epoch="$saved_gate_review_started_epoch"
  gate_review_result="$saved_gate_review_result"
  gate_stage="$saved_gate_stage"
  start_mode="$saved_start_mode"
  start_reason="$saved_start_reason"
  host_continuity="$saved_host_continuity"
  previous_review_json="$saved_previous_review_json"
  previous_lanes="$saved_previous_lanes"
  lanes="$saved_lanes"
  first_failure_epoch="$saved_first_failure_epoch"
  first_failure_lane="$saved_first_failure_lane"
  set -e
  return "$self_test_status"
}

cleanup() {
  local exit_status=$?
  set +e
  if { [ "$exit_status" -ne 0 ] || [ "${gate_completed:-0}" != "1" ]; } &&
    [ "${gate_review_result:-}" != "failed" ]; then
    write_gate_run_review \
      failed \
      "${gate_failure_layer:-preflight}" \
      "${gate_failure_reason:-unclassified command failure}" || true
  fi
  rm -rf -- "$scratch"
  if [ "$exit_status" -eq 0 ]; then
    gate_require_completion "release-candidate-local"
  fi
}
trap cleanup EXIT

cp "$inventory" "$run_dir/inventory.json"

if ! jq -e '
  .schema == "hideout.local-release-candidate-inventory/v1" and
  .requiredGoVersion == "go1.25.12" and
  (.requiredLanes | length) == 10 and
  (.requiredLanes | length) == (.requiredLanes | unique | length) and
  (.shellLint | length) >= 30 and
  (.shellLint | length) == (.shellLint | unique | length) and
  all(.shellLint[];
    type == "string" and
    test("^[A-Za-z0-9._/-]+[.]sh$")) and
  (.markdownLint | length) >= 30 and
  (.markdownLint | length) == (.markdownLint | unique | length) and
  all(.markdownLint[];
    type == "string" and
    test("^[A-Za-z0-9._/-]+[.]md$")) and
  (.fuzzTests | length) >= 3 and
  (.fuzzTests | length) ==
    (.fuzzTests | map(.importPath + "::" + .name) | unique | length) and
  all(
    .claimContracts,
    .claimMatrix,
    .productionMutationManifest,
    .productionMutationRunner,
    .recoveryMutationRunner,
    .negativeFixtureRunner;
    type == "string" and length > 0
  )
' "$inventory" >/dev/null; then
  printf 'release-candidate-local: invalid lane inventory\n' >&2
  exit 1
fi

required_go_version="$(jq -er '.requiredGoVersion' "$inventory")"
actual_go_version="$(go env GOVERSION)"
if [ "$actual_go_version" != "$required_go_version" ]; then
  printf 'release-candidate-local: requires %s, got %s\n' \
    "$required_go_version" "$actual_go_version" >&2
  exit 1
fi

gate_source_sha="$(sha256_file "$root/scripts/gates/release-candidate.sh")"
inventory_sha="$(sha256_file "$inventory")"
previous_summary="$out/summary.json"
if [ -f "$previous_summary" ] && [ ! -L "$previous_summary" ]; then
  previous_run="$(
    jq -er '
      select(.schema == "hideout.local-release-candidate/v1") |
      .run | select(test("^run-[0-9]{8}T[0-9]{6}Z-[0-9]+$"))
    ' "$previous_summary" 2>/dev/null || true
  )"
  previous_review="$out/${previous_run:-invalid}/run-review.json"
  if [ "$source_dirty" = false ] &&
    [ -n "${previous_run:-}" ] &&
    [ -f "$previous_review" ] && [ ! -L "$previous_review" ] &&
    [ "$(file_mode "$previous_review")" = "600" ] &&
    jq -e \
      --arg commit "$source_commit" \
      --arg tree "$source_tree" \
      --argjson dirty "$source_dirty" \
      --arg gateSHA256 "$gate_source_sha" \
      --arg inventorySHA256 "$inventory_sha" \
      --arg goVersion "$actual_go_version" '
        .schema == "hideout.gate-run-review/v1" and
        .gate == "release-candidate-local" and
        (.result == "passed" or .result == "failed") and
        .candidate == {commit:$commit,tree:$tree,dirty:$dirty} and
        .bindings.gateSHA256 == $gateSHA256 and
        .bindings.inventorySHA256 == $inventorySHA256 and
        .bindings.go == $goVersion and
        (.execution.lanes | type == "array")
      ' "$previous_review" >/dev/null 2>&1; then
    start_mode="same-candidate-retry"
    start_reason="a prior exact-candidate review exists, but this aggregate authenticates no reusable lane checkpoint"
    previous_lanes="$(jq -c '.execution.lanes' "$previous_review")"
    previous_review_json="$(
      jq -n \
        --arg path "$previous_run/run-review.json" \
        --arg sha256 "$(sha256_file "$previous_review")" \
        --arg result "$(jq -er '.result' "$previous_review")" \
        '{path:$path,sha256:$sha256,result:$result}'
    )"
    previous_boot_fingerprint="$(
      jq -r '.host.bootFingerprint // empty' "$previous_review"
    )"
    if [ -n "$host_boot_fingerprint" ] &&
      [ -n "$previous_boot_fingerprint" ]; then
      if [ "$host_boot_fingerprint" = "$previous_boot_fingerprint" ]; then
        host_continuity="same-boot-session"
      else
        host_continuity="different-boot-session"
      fi
    else
      host_continuity="unknown"
    fi
  fi
fi

gate_stage="review-preflight"
gate_run_review_self_test
scripts/test-validation-ladder.sh

jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$source_commit" \
  --arg tree "$source_tree" \
  --argjson dirty "$source_dirty" \
  --arg mode "$start_mode" \
  --arg reason "$start_reason" \
  --arg hostBootFingerprint "$host_boot_fingerprint" \
  --arg hostContinuity "$host_continuity" \
  --arg scope "$run_scope" \
  --argjson previousReview "$previous_review_json" '
    {
      schema:"hideout.gate-run-plan/v1",
      gate:"release-candidate-local",
      generatedAt:$generatedAt,
      candidate:{commit:$commit,tree:$tree,dirty:$dirty},
      start:{
        mode:$mode,
        reason:$reason,
        checkpointReused:false,
        resultReused:false,
        previousReview:$previousReview
      },
      host:{
        bootFingerprint:(if $hostBootFingerprint == "" then null else $hostBootFingerprint end),
        continuity:$hostContinuity
      },
      acceptance:{scope:$scope,requiredLanes:10}
    }
  ' >"$run_dir/run-plan.json"
chmod 0600 "$run_dir/run-plan.json"
printf \
  'release-candidate-local: start=%s host=%s checkpointReused=false plan=%s\n' \
  "$start_mode" "$host_continuity" "$run_dir/run-plan.json"

gate_stage="lane-execution"
gate_failure_layer="harness"
gate_failure_reason="local lane execution ended outside a classified lane result"
failed_lanes=0
run_lane() {
  local id="$1"
  shift
  local log="$run_dir/lanes/$id.log"
  local started_at finished_at started_epoch finished_epoch elapsed_seconds
  local status result
  gate_stage="lane:$id"
  started_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
  started_epoch="$(date +%s)"
  printf 'release-candidate-local: running %s\n' "$id"
  set +e
  (
    set -e
    "$@"
  ) >"$log" 2>&1
  status=$?
  set -e
  finished_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
  finished_epoch="$(date +%s)"
  elapsed_seconds=$((finished_epoch - started_epoch))
  if [ ! -s "$log" ]; then
    printf 'lane produced no output (exit=%d)\n' "$status" >"$log"
  fi
  chmod 0600 "$log"
  if [ "$status" -eq 0 ]; then
    result="passed"
    printf 'release-candidate-local: %s passed\n' "$id"
  else
    result="failed"
    gate_failure_layer="lane-execution"
    gate_failure_reason="lane $id failed with exit $status"
    failed_lanes=$((failed_lanes + 1))
    if [ "$first_failure_epoch" -eq 0 ]; then
      first_failure_epoch="$finished_epoch"
      first_failure_lane="$id"
    fi
    printf 'release-candidate-local: %s failed (exit=%d)\n' \
      "$id" "$status" >&2
    tail -30 "$log" >&2
  fi
  lanes="$(
    jq -c \
      --arg id "$id" \
      --arg result "$result" \
      --arg started_at "$started_at" \
      --arg finished_at "$finished_at" \
      --arg path "$run_id/lanes/$id.log" \
      --arg sha256 "$(sha256_file "$log")" \
      --argjson exit_code "$status" \
      --argjson elapsed_seconds "$elapsed_seconds" \
      '. + [{
        id: $id,
        result: $result,
        exitCode: $exit_code,
        startedAt: $started_at,
        finishedAt: $finished_at,
        elapsedSeconds: $elapsed_seconds,
        log: {path: $path, sha256: $sha256}
      }]' <<<"$lanes"
  )"
  return "$status"
}

lane_runner_self_test() {
  local saved_run_dir="$run_dir" saved_run_id="$run_id"
  local saved_lanes="$lanes" saved_failed_lanes="$failed_lanes"
  local saved_first_failure_epoch="$first_failure_epoch"
  local saved_first_failure_lane="$first_failure_lane"
  local saved_gate_stage="$gate_stage"
  local saved_failure_layer="$gate_failure_layer"
  local saved_failure_reason="$gate_failure_reason"
  local self_test_dir="$scratch/lane-runner-self-test" status=0
  local self_test_status=0

  mkdir -p "$self_test_dir/lanes"
  run_dir="$self_test_dir"
  run_id="lane-runner-self-test"
  lanes='[]'
  failed_lanes=0
  first_failure_epoch=0
  first_failure_lane=""
  if run_lane synthetic-failure bash -c \
    'printf "%s\n" synthetic-lane-failure; exit 23' \
    >"$self_test_dir/stdout" 2>"$self_test_dir/stderr"; then
    status=0
  else
    status=$?
  fi
  if [ "$status" -ne 23 ] || [ "$failed_lanes" -ne 1 ] ||
    [ "$first_failure_lane" != "synthetic-failure" ] ||
    ! jq -e '
      length == 1 and
      .[0].id == "synthetic-failure" and
      .[0].result == "failed" and
      .[0].exitCode == 23
    ' <<<"$lanes" >/dev/null; then
    printf 'release-candidate-local: lane runner failed fail-fast self-test\n' >&2
    self_test_status=1
  fi

  run_dir="$saved_run_dir"
  run_id="$saved_run_id"
  lanes="$saved_lanes"
  failed_lanes="$saved_failed_lanes"
  first_failure_epoch="$saved_first_failure_epoch"
  first_failure_lane="$saved_first_failure_lane"
  gate_stage="$saved_gate_stage"
  gate_failure_layer="$saved_failure_layer"
  gate_failure_reason="$saved_failure_reason"
  if [ "$self_test_status" -eq 0 ]; then
    printf 'release-candidate-local: lane-runner-self-test=passed\n'
  fi
  return "$self_test_status"
}

lane_runner_self_test

unit_lane() {
  go test -json -failfast -p 4 -count=1 ./...
}

race_lane() {
  go test -json -failfast -race -p 2 -count=1 ./...
}

fuzz_property_lane() {
  jq -r '.fuzzTests[].source' "$inventory" | LC_ALL=C sort -u \
    >"$scratch/expected-fuzz-sources"
  find cmd internal schemas test -type f -name '*_test.go' -print0 |
    xargs -0 sed -n \
      's/^func \(Fuzz[A-Za-z0-9_]*\).*/\1/p' |
    LC_ALL=C sort >"$scratch/discovered-fuzz-names"
  jq -r '.fuzzTests[].name' "$inventory" | LC_ALL=C sort \
    >"$scratch/expected-fuzz-names"
  if [ -n "$(
    comm -3 "$scratch/expected-fuzz-names" "$scratch/discovered-fuzz-names"
  )" ]; then
    printf 'fuzz inventory drifted from repository\n' >&2
    comm -3 "$scratch/expected-fuzz-names" "$scratch/discovered-fuzz-names" >&2
    return 1
  fi

  while IFS= read -r entry; do
    package="$(jq -er '.package' <<<"$entry")"
    name="$(jq -er '.name' <<<"$entry")"
    source="$(jq -er '.source' <<<"$entry")"
    fuzz_time="$(jq -er '.fuzzTime' <<<"$entry")"
    if [ ! -f "$source" ] || [ -L "$source" ] ||
      ! grep -Eq "^func $name\\(" "$source"; then
      printf 'missing fuzz harness %s in %s\n' "$name" "$source" >&2
      return 1
    fi
    printf 'fuzz-property: %s %s (%s)\n' "$package" "$name" "$fuzz_time"
    go test "$package" \
      -run '^$' \
      -fuzz "^${name}$" \
      -fuzztime="$fuzz_time" \
      -parallel=2 ||
      return
  done < <(jq -c '.fuzzTests[]' "$inventory")
}

schema_lane() {
  jq empty formal/inventory.json scripts/gates/release-candidate-inventory.json
  jq empty schemas/*.json
  go test -json ./schemas -count=1
}

generated_lane() {
  local clang="${HIDEOUT_BPF_CLANG:-}"
  local llvm_strip="${HIDEOUT_BPF_LLVM_STRIP:-}"
  if [ -z "$clang" ] &&
    [ -x /opt/homebrew/opt/llvm@19/bin/clang ]; then
    clang=/opt/homebrew/opt/llvm@19/bin/clang
  fi
  if [ -z "$llvm_strip" ] &&
    [ -x /opt/homebrew/opt/llvm@19/bin/llvm-strip ]; then
    llvm_strip=/opt/homebrew/opt/llvm@19/bin/llvm-strip
  fi
  if [ -z "$clang" ] || [ -z "$llvm_strip" ]; then
    printf 'pinned LLVM 19.1.7 commands are unavailable\n' >&2
    return 1
  fi
  HIDEOUT_BPF_CLANG="$clang" \
    HIDEOUT_BPF_LLVM_STRIP="$llvm_strip" \
    scripts/gates/generated.sh
}

static_lane() {
  scripts/gates/release-static.sh
}

dependencies_advisory_lane() {
  scripts/gates/dependencies.sh --out "$run_dir/dependencies"
}

mutations_lane() {
  local production_out="$run_dir/mutations/production"
  local recovery_out="$run_dir/mutations/recovery"
  local negative_out="$run_dir/mutations/judge-negative-fixtures"
  local contracts matrix manifest production_runner recovery_runner
  local negative_runner required_count negative_run_id negative_summary_rel
  local negative_run_summary negative_run_dir
  local evidence_path evidence_sha expected_path evidence_spec remainder
  contracts="$(jq -er '.claimContracts' "$inventory")"
  matrix="$(jq -er '.claimMatrix' "$inventory")"
  manifest="$(jq -er '.productionMutationManifest' "$inventory")"
  production_runner="$(jq -er '.productionMutationRunner' "$inventory")"
  recovery_runner="$(jq -er '.recoveryMutationRunner' "$inventory")"
  negative_runner="$(jq -er '.negativeFixtureRunner' "$inventory")"

  for input in \
    "$contracts" \
    "$matrix" \
    "$manifest" \
    "$production_runner" \
    "$recovery_runner" \
    "$negative_runner"; do
    if [ ! -f "$input" ] || [ -L "$input" ]; then
      printf 'mutation input is missing or symlinked: %s\n' "$input" >&2
      return 1
    fi
  done

  "$production_runner" --out "$production_out" || return
  "$recovery_runner" --out "$recovery_out" || return
  "$negative_runner" --out "$negative_out" ||
    return

  required_count="$(jq -er '.claims | length' "$contracts")"
  if ! jq -e \
    --slurpfile mutation_manifest "$manifest" \
    --arg manifest "$manifest" \
    --arg manifest_sha "$(sha256_file "$manifest")" \
    --arg contracts "$contracts" \
    --arg contracts_sha "$(sha256_file "$contracts")" \
    --argjson required "$required_count" '
      .schema == "hideout.045-production-mutation-run/v1" and
      .result == "passed" and
      .manifest == $manifest and
      .manifestSHA256 == $manifest_sha and
      .contracts == $contracts and
      .contractsSHA256 == $contracts_sha and
      .requiredClaims == $required and
      .executed == $required and
      .killed == $required and
      (.proofs | length) == $required and
      (.proofs | map(.id) | unique | length) == $required and
      (.proofs | map(.claimId) | unique | length) == $required and
      all(.proofs[];
        .result == "killed" and
        (.fromSHA256 | test("^[0-9a-f]{64}$")) and
        (.toSHA256 | test("^[0-9a-f]{64}$")) and
        .baseline.result == "passed" and
        .baseline.exitCode == 0 and
        (.baseline.passedTests | length) > 0 and
        .mutant.result == "failed" and
        .mutant.exitCode != 0 and
        (.mutant.failedTests | length) > 0
      ) and
      all(.proofs[];
        . as $proof |
        any($mutation_manifest[0].mutations[];
          .id == $proof.id and
          .claimId == $proof.claimId and
          .description == $proof.description and
          .source == $proof.source
        )
      ) and
      ((.errors // []) | length) == 0
    ' "$production_out/summary.json" >/dev/null; then
    printf 'source-overlay production mutation evidence is invalid\n' >&2
    return 1
  fi

  while IFS=$'\t' read -r mutation_id baseline_log baseline_sha \
    mutant_log mutant_sha; do
    expected_case="$production_out/$mutation_id"
    if [ "$baseline_log" != "$expected_case/baseline.log" ] ||
      [ "$mutant_log" != "$expected_case/mutant.log" ]; then
      printf 'production mutation log path escaped its case: %s\n' \
        "$mutation_id" >&2
      return 1
    fi
    for log_spec in \
      "$baseline_log:$baseline_sha" \
      "$mutant_log:$mutant_sha"; do
      mutation_log="${log_spec%%:*}"
      mutation_log_sha="${log_spec#*:}"
      if [ ! -f "$mutation_log" ] || [ -L "$mutation_log" ] ||
        [ "$(sha256_file "$mutation_log")" != "$mutation_log_sha" ]; then
        printf 'production mutation log is missing or digest-invalid: %s\n' \
          "$mutation_id" >&2
        return 1
      fi
    done

    jq -r \
      --arg id "$mutation_id" \
      '.mutations[] | select(.id == $id) | .killTests[]' \
      "$manifest" | LC_ALL=C sort -u \
      >"$scratch/$mutation_id-expected-kill-tests"
    jq -r 'select(.Action == "pass" and (.Test // "") != "") | .Test' \
      "$baseline_log" | LC_ALL=C sort -u \
      >"$scratch/$mutation_id-baseline-passed"
    jq -r 'select(.Action == "fail" and (.Test // "") != "") | .Test' \
      "$mutant_log" | LC_ALL=C sort -u \
      >"$scratch/$mutation_id-mutant-failed"
    jq -r \
      --arg id "$mutation_id" \
      '.proofs[] | select(.id == $id) | .baseline.passedTests[]' \
      "$production_out/summary.json" | LC_ALL=C sort -u \
      >"$scratch/$mutation_id-summary-passed"
    jq -r \
      --arg id "$mutation_id" \
      '.proofs[] | select(.id == $id) | .mutant.failedTests[]' \
      "$production_out/summary.json" | LC_ALL=C sort -u \
      >"$scratch/$mutation_id-summary-failed"

    if [ -n "$(
      comm -3 \
        "$scratch/$mutation_id-baseline-passed" \
        "$scratch/$mutation_id-summary-passed"
    )" ] ||
      [ -n "$(
        comm -3 \
          "$scratch/$mutation_id-mutant-failed" \
          "$scratch/$mutation_id-summary-failed"
      )" ] ||
      [ -z "$(
        comm -12 \
          "$scratch/$mutation_id-expected-kill-tests" \
          "$scratch/$mutation_id-baseline-passed"
      )" ] ||
      [ -z "$(
        comm -12 \
          "$scratch/$mutation_id-expected-kill-tests" \
          "$scratch/$mutation_id-mutant-failed"
      )" ]; then
      printf 'production mutation test-event evidence is invalid: %s\n' \
        "$mutation_id" >&2
      return 1
    fi
  done < <(
    jq -r '
      .proofs[] |
      [
        .id,
        .baseline.log,
        .baseline.logSHA256,
        .mutant.log,
        .mutant.logSHA256
      ] | @tsv
    ' "$production_out/summary.json"
  )

  if ! jq -e \
    --arg commit "$source_commit" \
    --argjson dirty "$source_dirty" '
      .schema == "hideout.recovery-gate-evidence/v1" and
      .source.commit == $commit and
      .source.dirty == $dirty and
      .result == "passed" and
      .crashMatrix.points == 16 and
      ([.checks | keys[]] | sort) == ["race", "unit"] and
      (.mutationProofs | length) == 3 and
      ([.mutationProofs[].id] | sort) == [
        "duplicate-terminal-event",
        "replay-running-effect",
        "success-without-proof"
      ] and
      all(.mutationProofs[]; .result == "killed")
    ' "$recovery_out/summary.json" >/dev/null; then
    printf 'recovery production mutation evidence is invalid\n' >&2
    return 1
  fi
  while IFS=$'\t' read -r check_id evidence_path evidence_sha; do
    expected_path="$check_id.log"
    if [ "$evidence_path" != "$expected_path" ] ||
      [ ! -f "$recovery_out/$evidence_path" ] ||
      [ -L "$recovery_out/$evidence_path" ] ||
      [ "$(sha256_file "$recovery_out/$evidence_path")" != "$evidence_sha" ]; then
      printf 'recovery check evidence is missing or invalid: %s\n' \
        "$check_id" >&2
      return 1
    fi
  done < <(
    jq -r '
      .checks | to_entries[] |
      [.key, .value.log, .value.sha256] | @tsv
    ' "$recovery_out/summary.json"
  )
  while IFS=$'\t' read -r mutation_id evidence_path evidence_sha; do
    expected_path="mutations/$mutation_id.log"
    if [ "$evidence_path" != "$expected_path" ] ||
      [ ! -f "$recovery_out/$evidence_path" ] ||
      [ -L "$recovery_out/$evidence_path" ] ||
      [ "$(sha256_file "$recovery_out/$evidence_path")" != "$evidence_sha" ]; then
      printf 'recovery mutation evidence is missing or invalid: %s\n' \
        "$mutation_id" >&2
      return 1
    fi
  done < <(
    jq -r '
      .mutationProofs[] | [.id, .log, .sha256] | @tsv
    ' "$recovery_out/summary.json"
  )

  negative_run_id="$(
    jq -er '
      .runId |
      select(test("^run-[0-9]{8}T[0-9]{6}Z-[0-9]+$"))
    ' "$negative_out/summary.json"
  )"
  negative_summary_rel="$(jq -er '.summary' "$negative_out/summary.json")"
  if [ "$negative_summary_rel" != "$negative_run_id/summary.json" ]; then
    printf 'judge-negative latest pointer is invalid\n' >&2
    return 1
  fi
  negative_run_summary="$negative_out/$negative_summary_rel"
  negative_run_dir="${negative_run_summary%/summary.json}"
  if [ ! -f "$negative_run_summary" ] || [ -L "$negative_run_summary" ] ||
    [ "$(sha256_file "$negative_run_summary")" != "$(
      jq -er '.sha256' "$negative_out/summary.json"
    )" ]; then
    printf 'judge-negative evidence is missing or digest-invalid\n' >&2
    return 1
  fi
  if ! jq -e \
    --arg contracts "$contracts" \
    --arg contracts_sha "$(sha256_file "$contracts")" \
    --arg matrix "$matrix" \
    --arg matrix_sha "$(sha256_file "$matrix")" \
    --arg commit "$source_commit" \
    --argjson dirty "$source_dirty" \
    --argjson required "$required_count" '
      .schema == "hideout.045-negative-fixture-evidence/v1" and
      .result == "passed" and
      .source.commit == $commit and
      .source.dirty == $dirty and
      .inputs.contracts == $contracts and
      .inputs.contractsSHA256 == $contracts_sha and
      .inputs.claimMatrix == $matrix and
      .inputs.claimMatrixSHA256 == $matrix_sha and
      .claimFamilies == $required and
      .restoredFixtures == $required and
      (.negativeFixtures | length) == $required and
      (.negativeFixtures | map(.id) | unique | length) == $required and
      (.negativeFixtures | map(.claimId) | unique | length) == $required and
      all(.negativeFixtures[];
        .id == ("N045-" + .claimId) and
        .result == "killed" and
        .restored.result == "passed"
      ) and
      .implementationMutationProofs.accepted == false and
      .claimAcceptance == false
    ' "$negative_run_summary" >/dev/null; then
    printf 'judge-negative mutation evidence is invalid\n' >&2
    return 1
  fi
  while IFS=$'\t' read -r fixture_id \
    negative_receipt negative_receipt_sha \
    negative_evidence negative_evidence_sha \
    negative_log negative_log_sha \
    restored_receipt restored_receipt_sha \
    restored_evidence restored_evidence_sha \
    restored_log restored_log_sha; do
    for evidence_spec in \
      "$negative_receipt:$negative_receipt_sha:$fixture_id/negative/receipt.json" \
      "$negative_evidence:$negative_evidence_sha:$fixture_id/negative/observation.json" \
      "$negative_log:$negative_log_sha:$fixture_id/negative/judge.log" \
      "$restored_receipt:$restored_receipt_sha:$fixture_id/restored/receipt.json" \
      "$restored_evidence:$restored_evidence_sha:$fixture_id/restored/observation.json" \
      "$restored_log:$restored_log_sha:$fixture_id/restored/judge.log"; do
      evidence_path="${evidence_spec%%:*}"
      remainder="${evidence_spec#*:}"
      evidence_sha="${remainder%%:*}"
      expected_path="${remainder#*:}"
      if [ "$evidence_path" != "$expected_path" ] ||
        [ ! -f "$negative_run_dir/$evidence_path" ] ||
        [ -L "$negative_run_dir/$evidence_path" ] ||
        [ "$(
          sha256_file "$negative_run_dir/$evidence_path"
        )" != "$evidence_sha" ]; then
        printf 'judge-negative fixture evidence is invalid: %s\n' \
          "$fixture_id" >&2
        return 1
      fi
    done
  done < <(
    jq -r '
      .negativeFixtures[] |
      [
        .id,
        .negative.receipt,
        .negative.receiptSHA256,
        .negative.evidence,
        .negative.evidenceSHA256,
        .negative.log,
        .negative.logSHA256,
        .restored.receipt,
        .restored.receiptSHA256,
        .restored.evidence,
        .restored.evidenceSHA256,
        .restored.log,
        .restored.logSHA256
      ] | @tsv
    ' "$negative_run_summary"
  )

  jq -r '.claims[].id' "$contracts" | LC_ALL=C sort \
    >"$scratch/required-mutation-claims"
  jq -r '.proofs[].claimId' "$production_out/summary.json" |
    LC_ALL=C sort -u >"$scratch/observed-mutation-claims"
  jq -r '.negativeFixtures[].claimId' "$negative_run_summary" |
    LC_ALL=C sort -u >"$scratch/negative-mutation-claims"
  comm -23 \
    "$scratch/required-mutation-claims" \
    "$scratch/observed-mutation-claims" \
    >"$scratch/missing-mutation-claims"
  comm -3 \
    "$scratch/required-mutation-claims" \
    "$scratch/negative-mutation-claims" \
    >"$scratch/invalid-negative-mutation-claims"

  proofs="$(
    jq -c \
      --arg production_summary "production/summary.json" \
      --arg production_sha "$(sha256_file "$production_out/summary.json")" '
        [
          .proofs[] | {
            id,
            claimId,
            description,
            source,
            fromSHA256,
            toSHA256,
            baseline: {
              logSHA256: .baseline.logSHA256,
              passedTests: .baseline.passedTests
            },
            mutant: {
              logSHA256: .mutant.logSHA256,
              failedTests: .mutant.failedTests
            },
            result: "killed",
            evidence: {
              path: $production_summary,
              sha256: $production_sha
            }
          }
        ]
      ' "$production_out/summary.json"
  )"
  jq -n \
    --arg generated_at "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
    --arg contracts "$contracts" \
    --arg contracts_sha "$(sha256_file "$contracts")" \
    --arg matrix "$matrix" \
    --arg matrix_sha "$(sha256_file "$matrix")" \
    --arg manifest "$manifest" \
    --arg manifest_sha "$(sha256_file "$manifest")" \
    --arg production_summary "production/summary.json" \
    --arg production_sha "$(sha256_file "$production_out/summary.json")" \
    --arg recovery_summary "recovery/summary.json" \
    --arg recovery_sha "$(sha256_file "$recovery_out/summary.json")" \
    --arg negative_latest "judge-negative-fixtures/summary.json" \
    --arg negative_latest_sha "$(sha256_file "$negative_out/summary.json")" \
    --arg negative_summary "judge-negative-fixtures/$negative_summary_rel" \
    --arg negative_sha "$(sha256_file "$negative_run_summary")" \
    --argjson required "$(wc -l <"$scratch/required-mutation-claims" | tr -d ' ')" \
    --argjson covered "$(wc -l <"$scratch/observed-mutation-claims" | tr -d ' ')" \
    --argjson proofs "$proofs" \
    --argjson missing "$(
      jq -R . <"$scratch/missing-mutation-claims" | jq -s .
    )" \
    '{
      schema: "hideout.045-production-mutation-aggregate/v1",
      generatedAt: $generated_at,
      result: (if ($missing | length) == 0 then "passed" else "failed" end),
      inputs: {
        contracts: $contracts,
        contractsSHA256: $contracts_sha,
        claimMatrix: $matrix,
        claimMatrixSHA256: $matrix_sha,
        mutationManifest: $manifest,
        mutationManifestSHA256: $manifest_sha
      },
      requiredClaimFamilies: $required,
      coveredClaimFamilies: $covered,
      proofs: $proofs,
      missingClaimFamilies: $missing,
      evidence: {
        production: {
          path: $production_summary,
          sha256: $production_sha
        },
        recovery: {
          path: $recovery_summary,
          sha256: $recovery_sha
        },
        judgeNegativeFixtures: {
          path: $negative_summary,
          sha256: $negative_sha
        },
        judgeNegativeLatest: {
          path: $negative_latest,
          sha256: $negative_latest_sha
        }
      },
      candidateAcceptance: false,
      limitation:
        "Every claim family has a killed source-overlay production mutant. Recovery trace mutants and judge-negative fixtures remain independent required evidence."
    }' >"$run_dir/mutations/production-summary.json"
  chmod 0600 "$run_dir/mutations/production-summary.json"

  if [ -s "$scratch/missing-mutation-claims" ] ||
    [ -s "$scratch/invalid-negative-mutation-claims" ] ||
    [ "$required_count" -ne 46 ]; then
    printf 'production mutation coverage is incomplete: '
    if [ -s "$scratch/missing-mutation-claims" ]; then
      paste -sd, "$scratch/missing-mutation-claims"
    elif [ -s "$scratch/invalid-negative-mutation-claims" ]; then
      paste -sd, "$scratch/invalid-negative-mutation-claims"
    else
      printf 'required=%s want=46\n' "$required_count"
    fi
    return 1
  fi
  if ! jq -e \
    --argjson required "$required_count" '
      .schema == "hideout.045-production-mutation-aggregate/v1" and
      .result == "passed" and
      .requiredClaimFamilies == $required and
      .coveredClaimFamilies == $required and
      (.proofs | length) == $required and
      (.proofs | map(.claimId) | unique | length) == $required and
      all(.proofs[]; .result == "killed") and
      (.missingClaimFamilies | length) == 0 and
      .candidateAcceptance == false
    ' "$run_dir/mutations/production-summary.json" >/dev/null; then
    printf 'production mutation aggregate failed validation\n' >&2
    return 1
  fi
  printf 'all production and judge-negative mutation claims passed\n'
}

release_blockers_lane() {
  local guarded_script
  {
    # The backticks are literal Markdown delimiters in the table query.
    # shellcheck disable=SC2016
    grep -E '^\| [A-Z0-9]+ \|.*`blocked-integration`' \
      docs/release/045-claim-matrix.md || true
  } |
    sed -E 's/^\| ([A-Z0-9]+) \|.*/\1/' |
    LC_ALL=C sort -u >"$scratch/blocked-integration"
  if [ -s "$scratch/blocked-integration" ]; then
    printf 'required integration blockers remain: '
    paste -sd, "$scratch/blocked-integration"
    return 1
  fi
  gate_completion_guard_self_test
  while IFS= read -r guarded_script; do
    if ! grep -F 'scripts/lib/gate-result.sh' "$guarded_script" >/dev/null ||
      ! grep -F 'gate_completed=0' "$guarded_script" >/dev/null ||
      ! grep -F 'gate_completed=1' "$guarded_script" >/dev/null ||
      ! grep -F 'gate_require_completion' "$guarded_script" >/dev/null; then
      printf \
        'release completion guard is not wired: %s\n' \
        "$guarded_script" >&2
      return 1
    fi
  done <<'EOF'
scripts/gates/release-candidate.sh
scripts/gates/release-candidate-privacy.sh
scripts/gates/release-candidate-ui.sh
scripts/gates/release-candidate-performance.sh
scripts/gates/release-candidate-lima.sh
scripts/gates/dependency-licenses.sh
scripts/gates/formal-verify.sh
scripts/gates/formal.sh
scripts/gates/migration-lima.sh
scripts/gates/migration.sh
scripts/gates/network-rotation-lima.sh
scripts/gates/package-components.sh
scripts/gates/workload-observation-lima.sh
scripts/gates/workload-privacy-lima.sh
scripts/generate-workload-observer-bpf.sh
scripts/mutation/045/run-negative-fixtures.sh
scripts/package-local.sh
scripts/release/build-candidate.sh
scripts/release/test-package-lifecycle.sh
scripts/release/collect-evidence.sh
scripts/release/revalidate-performance-evidence.sh
scripts/release/install-local-candidate.sh
scripts/release/verify-publication-absence.sh
scripts/test-install-smoke.sh
scripts/test-package-smoke.sh
scripts/test-vulnerability-gate.sh
EOF
  scripts/release/build-candidate.sh --preflight
  scripts/gates/migration.sh --preflight
  scripts/gates/migration-lima.sh --preflight
  scripts/release/test-package-lifecycle.sh --preflight
  scripts/release/collect-evidence.sh --preflight
  scripts/release/install-local-candidate.sh --preflight
  scripts/release/verify-publication-absence.sh --preflight
  scripts/gates/formal.sh --preflight --out "$run_dir/formal-preflight"
  printf 'no required integration blocker remains\n'
}

if [ "$preflight_only" -eq 1 ]; then
  gate_stage="release-blocker-preflight"
  gate_failure_layer="preflight"
  gate_failure_reason="release-blocker preflight failed"
  release_blockers_lane
  gate_stage="preflight-complete"
  write_gate_run_review passed "" ""
  gate_completed=1
  printf \
    'release-candidate-local: preflight=passed plan=%s review=%s\n' \
    "$run_dir/run-plan.json" "$run_dir/run-review.json"
  exit 0
fi

run_lane schema schema_lane
run_lane static static_lane
run_lane release-blockers release_blockers_lane
run_lane generated generated_lane
run_lane dependencies-advisory dependencies_advisory_lane
run_lane unit unit_lane
run_lane race race_lane
run_lane fuzz-property fuzz_property_lane
run_lane migration scripts/gates/migration.sh --out "$run_dir/migration"
run_lane mutations mutations_lane

gate_stage="evidence-assembly"
gate_failure_layer="evidence-judge"
gate_failure_reason="local aggregate evidence assembly or validation failed"
jq -r '.requiredLanes[]' "$inventory" | LC_ALL=C sort \
  >"$scratch/expected-lanes"
jq -r '.[].id' <<<"$lanes" | LC_ALL=C sort >"$scratch/observed-lanes"
if [ -n "$(comm -3 "$scratch/expected-lanes" "$scratch/observed-lanes")" ]; then
  printf 'release-candidate-local: lane execution set drifted\n' >&2
  exit 1
fi

find "$run_dir" -type f -exec chmod 0600 {} +
while IFS= read -r evidence_file; do
  if [ ! -s "$evidence_file" ]; then
    printf 'release-candidate-local: empty artifact: %s\n' \
      "$evidence_file" >&2
    exit 1
  fi
  if [ "$(file_mode "$evidence_file")" != "600" ]; then
    printf 'release-candidate-local: artifact mode is not 0600: %s\n' \
      "$evidence_file" >&2
    exit 1
  fi
done < <(find "$run_dir" -type f | LC_ALL=C sort)

artifacts='[]'
while IFS= read -r evidence_file; do
  relative_path="${evidence_file#"$out"/}"
  artifacts="$(
    jq -c \
      --arg path "$relative_path" \
      --arg sha256 "$(sha256_file "$evidence_file")" \
      '. + [{path: $path, sha256: $sha256}]' <<<"$artifacts"
  )"
done < <(find "$run_dir" -type f | LC_ALL=C sort)

if [ "$failed_lanes" -eq 0 ]; then
  result="passed"
else
  result="failed"
fi

unit_passed="$(
  jq -s '
    [
      .[] |
      select(
        .Action == "pass" and
        (.Test // "") != "" and
        (.Test | contains("/") | not)
      )
    ] | length
  ' "$run_dir/lanes/unit.log" 2>/dev/null || printf '0'
)"
race_passed="$(
  jq -s '
    [
      .[] |
      select(
        .Action == "pass" and
        (.Test // "") != "" and
        (.Test | contains("/") | not)
      )
    ] | length
  ' "$run_dir/lanes/race.log" 2>/dev/null || printf '0'
)"

summary="$out/summary.json"
jq -n \
  --arg generated_at "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$source_commit" \
  --arg tree "$source_tree" \
  --argjson dirty "$source_dirty" \
  --arg run "$run_id" \
  --arg result "$result" \
  --arg go_version "$actual_go_version" \
  --arg inventory_path "$run_id/inventory.json" \
  --arg inventory_sha "$inventory_sha" \
  --arg gate_sha "$gate_source_sha" \
  --arg run_plan_path "$run_id/run-plan.json" \
  --arg run_plan_sha "$(sha256_file "$run_dir/run-plan.json")" \
  --argjson lanes "$lanes" \
  --argjson failed_lanes "$failed_lanes" \
  --argjson unit_passed "$unit_passed" \
  --argjson race_passed "$race_passed" \
  --argjson fuzz_count "$(jq '.fuzzTests | length' "$inventory")" \
  --argjson artifacts "$artifacts" \
  '{
    schema: "hideout.local-release-candidate/v1",
    generatedAt: $generated_at,
    source: {commit: $commit, tree: $tree, dirty: $dirty},
    result: $result,
    scope: "full-local-source-aggregate",
    candidateAcceptance: false,
    run: $run,
    toolchain: {go: $go_version},
    inventory: {path: $inventory_path, sha256: $inventory_sha},
    gateSource: {
      path: "scripts/gates/release-candidate.sh",
      sha256: $gate_sha
    },
    runPlan: {path: $run_plan_path, sha256: $run_plan_sha},
    lanes: $lanes,
    statistics: {
      requiredLanes: ($lanes | length),
      failedLanes: $failed_lanes,
      topLevelUnitTestsPassed: $unit_passed,
      topLevelRaceTestsPassed: $race_passed,
      fuzzHarnessesExecuted: $fuzz_count
    },
    artifacts: $artifacts,
    limitation:
      "This dirty-aware local source aggregate never substitutes for formal, real-Lima, all-sink privacy, UI, performance, package, install, exact-candidate, signing, notarization, or publication evidence."
  }' >"$summary"
chmod 0600 "$summary"

if ! jq -e \
  --arg result "$result" \
  --argjson lane_count "$(jq '.requiredLanes | length' "$inventory")" '
    .schema == "hideout.local-release-candidate/v1" and
    .result == $result and
    .candidateAcceptance == false and
    (.lanes | length) == $lane_count and
    (.lanes | map(.id) | unique | length) == $lane_count and
    all(.lanes[];
      (.result == "passed" and .exitCode == 0) or
      (.result == "failed" and .exitCode != 0)
    ) and
    (.artifacts | length) > $lane_count
  ' "$summary" >/dev/null; then
  printf 'release-candidate-local: generated summary failed validation\n' >&2
  exit 1
fi

if [ "$failed_lanes" -ne 0 ]; then
  failed_lane_ids="$(
    jq -r '[.[] | select(.result == "failed") | .id] | join(",")' \
      <<<"$lanes"
  )"
  gate_stage="failed"
  write_gate_run_review \
    failed \
    evidence-judge \
    "lane evidence rejected: $failed_lane_ids"
  printf \
    'release-candidate-local: result=%s failedLanes=%d evidence=%s review=%s\n' \
    "$result" "$failed_lanes" "$summary" "$run_dir/run-review.json"
  exit 1
fi
gate_stage="complete"
write_gate_run_review passed "" ""
gate_completed=1
printf \
  'release-candidate-local: result=%s failedLanes=%d evidence=%s review=%s\n' \
  "$result" "$failed_lanes" "$summary" "$run_dir/run-review.json"
