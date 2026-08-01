#!/usr/bin/env bash

# Fail-closed helpers for reusing passed lanes from the immediately preceding
# release-candidate-lima aggregate. This file is sourced by the aggregate gate.

release_lima_resume_sha256() {
  shasum -a 256 "$1" | awk '{print $1}'
}

release_lima_resume_file_mode() {
  local raw
  raw="$(stat -f '%Lp' "$1" 2>/dev/null || stat -c '%a' "$1" 2>/dev/null)" ||
    return 1
  case "$raw" in
    "" | *[!0-7]*) return 1 ;;
  esac
  printf '%04o\n' "$((8#$raw))"
}

release_lima_resume_safe_relative_path() {
  case "$1" in
    "" | /* | .. | ../* | */.. | */../* | *$'\t'* | *$'\r'* | *$'\n'*)
      return 1
      ;;
    *) return 0 ;;
  esac
}

release_lima_resume_validate() {
  [ "$#" -eq 4 ] || return 2
  local out="$1" expected_commit="$2" expected_dirty="$3" scratch="$4"
  local pointer="$out/result.json"
  local run_id summary_relative summary_path summary_sha run_dir
  local manifest_rows manifest_paths actual_paths actual_count manifest_count
  local relative expected_sha expected_bytes expected_mode artifact_path

  [ "$expected_dirty" = "false" ] || return 1
  [ -d "$out" ] && [ ! -L "$out" ] || return 1
  [ -f "$pointer" ] && [ ! -L "$pointer" ] || return 1
  [ "$(release_lima_resume_file_mode "$pointer")" = "0600" ] || return 1
  jq -e \
    --arg commit "$expected_commit" \
    --argjson dirty "$expected_dirty" '
      .schema == "hideout.release-candidate-lima-pointer/v1" and
      .result == "failed" and
      .candidateAcceptance == false and
      .source == {commit:$commit,dirty:$dirty} and
      (.runId | test("^run-[0-9]{8}T[0-9]{6}Z-[0-9]+$")) and
      .summary == (.runId + "/summary.json") and
      (.summarySHA256 | test("^[a-f0-9]{64}$"))
    ' "$pointer" >/dev/null || return 1

  run_id="$(jq -er '.runId' "$pointer")" || return 1
  summary_relative="$(jq -er '.summary' "$pointer")" || return 1
  summary_sha="$(jq -er '.summarySHA256' "$pointer")" || return 1
  release_lima_resume_safe_relative_path "$summary_relative" || return 1
  run_dir="$out/$run_id"
  summary_path="$out/$summary_relative"
  [ -d "$run_dir" ] && [ ! -L "$run_dir" ] || return 1
  [ -f "$summary_path" ] && [ ! -L "$summary_path" ] || return 1
  [ "$(release_lima_resume_file_mode "$summary_path")" = "0600" ] || return 1
  [ "$(release_lima_resume_sha256 "$summary_path")" = "$summary_sha" ] || return 1
  [ -z "$(find "$run_dir" -type l -print -quit)" ] || return 1
  [ -z "$(find "$run_dir" ! -type d ! -type f -print -quit)" ] || return 1

  jq -e \
    --arg commit "$expected_commit" \
    --argjson dirty "$expected_dirty" \
    --arg runId "$run_id" '
      . as $summary |
      .schema == "hideout.release-candidate-lima-evidence/v1" and
      .result == "failed" and
      .candidateAcceptance == false and
      .source == {commit:$commit,dirty:$dirty} and
      .runId == $runId and
      [.lanes[].id] == [
        "workload-observation",
        "network-rotation",
        "workload-privacy",
        "concurrent-crash-recovery"
      ] and
      (.lanes | length) == 4 and
      any(.lanes[]; .result == "passed") and
      any(.lanes[]; .result == "failed") and
      all(.lanes[];
        (.result == "passed" or .result == "failed") and
        (.exitCode | type) == "number" and
        .log.path == ("lanes/" + .id + ".log") and
        (.log.sha256 | test("^[a-f0-9]{64}$")) and
        (if .result == "passed"
         then .exitCode == 0 and .evidence != null and
           (.evidence.sha256 | test("^[a-f0-9]{64}$"))
         else .exitCode != 0
         end)) and
      (.validation | keys | sort) == ([
        "workloadObservation",
        "networkRotation",
        "workloadPrivacy",
        "concurrentCrashRecovery"
      ] | sort) and
      all(.validation[]; type == "boolean") and
      ((.lanes[] | select(.id == "workload-observation") | .result == "passed") ==
        .validation.workloadObservation) and
      ((.lanes[] | select(.id == "network-rotation") | .result == "passed") ==
        .validation.networkRotation) and
      ((.lanes[] | select(.id == "workload-privacy") | .result == "passed") ==
        .validation.workloadPrivacy) and
      ((.lanes[] | select(.id == "concurrent-crash-recovery") | .result == "passed") ==
        .validation.concurrentCrashRecovery) and
      ((.lanes[] | select(.id == "workload-observation") |
        .result != "passed" or .evidence.path == "workload/result.json")) and
      ((.lanes[] | select(.id == "network-rotation") |
        .result != "passed" or .evidence.path == "network-rotation/result.json")) and
      ((.lanes[] | select(.id == "workload-privacy") |
        .result != "passed" or .evidence.path == "privacy/result.json")) and
      ((.lanes[] | select(.id == "concurrent-crash-recovery") |
        .result != "passed" or .evidence.path == "concurrent/result.json")) and
      (.artifacts | type) == "array" and
      (.artifacts | length) > 0 and
      ([.artifacts[].path] | unique | length) == (.artifacts | length) and
      all(.artifacts[];
        (.path | type) == "string" and
        (.sha256 | test("^[a-f0-9]{64}$")) and
        (.bytes | type) == "number" and .bytes >= 0 and
        .bytes == (.bytes | floor) and
        .mode == "0600") and
      all(.lanes[];
        . as $lane |
        any($summary.artifacts[];
          .path == $lane.log.path and
          .sha256 == $lane.log.sha256) and
        (if $lane.result == "passed"
         then any($summary.artifacts[];
           .path == $lane.evidence.path and
           .sha256 == $lane.evidence.sha256)
         else true
         end))
    ' "$summary_path" >/dev/null || return 1

  manifest_rows="$scratch/resume-manifest.tsv"
  manifest_paths="$scratch/resume-manifest-paths.txt"
  actual_paths="$scratch/resume-actual-paths.txt"
  jq -r '.artifacts[] | [.path,.sha256,.bytes,.mode] | @tsv' \
    "$summary_path" >"$manifest_rows" || return 1
  : >"$manifest_paths"
  while IFS=$'\t' read -r relative expected_sha expected_bytes expected_mode; do
    release_lima_resume_safe_relative_path "$relative" || return 1
    [ "$relative" != "summary.json" ] || return 1
    artifact_path="$run_dir/$relative"
    [ -f "$artifact_path" ] && [ ! -L "$artifact_path" ] || return 1
    [ "$expected_mode" = "0600" ] || return 1
    [ "$(release_lima_resume_file_mode "$artifact_path")" = "$expected_mode" ] ||
      return 1
    [ "$(release_lima_resume_sha256 "$artifact_path")" = "$expected_sha" ] ||
      return 1
    [ "$(wc -c <"$artifact_path" | tr -d '[:space:]')" = "$expected_bytes" ] ||
      return 1
    printf '%s\n' "$relative" >>"$manifest_paths"
  done <"$manifest_rows"
  LC_ALL=C sort -o "$manifest_paths" "$manifest_paths"

  : >"$actual_paths"
  while IFS= read -r artifact_path; do
    [ "$artifact_path" != "$summary_path" ] || continue
    relative="${artifact_path#"$run_dir"/}"
    release_lima_resume_safe_relative_path "$relative" || return 1
    printf '%s\n' "$relative" >>"$actual_paths"
  done < <(find "$run_dir" -type f -print | LC_ALL=C sort)
  LC_ALL=C sort -o "$actual_paths" "$actual_paths"
  cmp -s "$manifest_paths" "$actual_paths" || return 1
  manifest_count="$(wc -l <"$manifest_paths" | tr -d '[:space:]')"
  actual_count="$(wc -l <"$actual_paths" | tr -d '[:space:]')"
  [ "$manifest_count" = "$actual_count" ] || return 1

  jq -n \
    --arg runId "$run_id" \
    --arg summary "$summary_relative" \
    --arg summarySHA256 "$summary_sha" \
    --argjson passedLanes \
      "$(jq -c '[.lanes[] | select(.result == "passed") | .id]' "$summary_path")" '
      {
        schema:"hideout.release-candidate-lima-resume-source/v1",
        runId:$runId,
        summary:$summary,
        summarySHA256:$summarySHA256,
        passedLanes:$passedLanes
      }
    '
}

release_lima_resume_copy_lane() {
  [ "$#" -eq 3 ] || return 2
  local source_run="$1" destination_run="$2" lane_dir="$3"
  local source="$source_run/$lane_dir" destination="$destination_run/$lane_dir"
  release_lima_resume_safe_relative_path "$lane_dir" || return 1
  [ -d "$source" ] && [ ! -L "$source" ] || return 1
  [ ! -e "$destination" ] && [ ! -L "$destination" ] || return 1
  [ -z "$(find "$source" -type l -print -quit)" ] || return 1
  [ -z "$(find "$source" ! -type d ! -type f -print -quit)" ] || return 1
  cp -R "$source" "$destination" || return 1
  [ -z "$(find "$destination" -type l -print -quit)" ] || return 1
  [ -z "$(find "$destination" ! -type d ! -type f -print -quit)" ] || return 1
  find "$destination" -type d -exec chmod 0700 {} +
  find "$destination" -type f -exec chmod 0600 {} +
}

release_lima_resume_verify_lane_copy() {
  [ "$#" -eq 3 ] || return 2
  local source_summary="$1" destination_run="$2" lane_dir="$3"
  local prefix="$lane_dir/" expected_count actual_count
  local relative expected_sha expected_bytes expected_mode artifact_path
  release_lima_resume_safe_relative_path "$lane_dir" || return 1
  [ -f "$source_summary" ] && [ ! -L "$source_summary" ] || return 1
  [ -d "$destination_run/$lane_dir" ] &&
    [ ! -L "$destination_run/$lane_dir" ] || return 1
  expected_count="$(
    jq -r --arg prefix "$prefix" \
      '[.artifacts[] | select(.path | startswith($prefix))] | length' \
      "$source_summary"
  )" || return 1
  actual_count="$(
    find "$destination_run/$lane_dir" -type f -print |
      wc -l | tr -d '[:space:]'
  )"
  [ "$expected_count" -gt 0 ] && [ "$expected_count" = "$actual_count" ] ||
    return 1
  while IFS=$'\t' read -r relative expected_sha expected_bytes expected_mode; do
    release_lima_resume_safe_relative_path "$relative" || return 1
    artifact_path="$destination_run/$relative"
    [ -f "$artifact_path" ] && [ ! -L "$artifact_path" ] || return 1
    [ "$expected_mode" = "0600" ] &&
      [ "$(release_lima_resume_file_mode "$artifact_path")" = "$expected_mode" ] ||
      return 1
    [ "$(release_lima_resume_sha256 "$artifact_path")" = "$expected_sha" ] ||
      return 1
    [ "$(wc -c <"$artifact_path" | tr -d '[:space:]')" = "$expected_bytes" ] ||
      return 1
  done < <(
    jq -r --arg prefix "$prefix" '
      .artifacts[] |
      select(.path | startswith($prefix)) |
      [.path,.sha256,.bytes,.mode] | @tsv
    ' "$source_summary"
  )
  while IFS= read -r artifact_path; do
    relative="${artifact_path#"$destination_run"/}"
    jq -e --arg path "$relative" \
      'any(.artifacts[]; .path == $path)' \
      "$source_summary" >/dev/null || return 1
  done < <(find "$destination_run/$lane_dir" -type f -print)
}

release_lima_resume_preflight() {
  [ "$#" -eq 1 ] || return 2
  local root="$1"
  local out="$root/out"
  local run_id="run-20260802T000000Z-1"
  local run_dir="$out/$run_id"
  local commit="0123456789abcdef0123456789abcdef01234567"
  local rows="$root/artifacts.jsonl" lane_id lane_result lane_exit lane_log
  local lane_evidence lane_evidence_sha lane_log_sha artifacts_json summary_sha
  local pointer_saved result_saved
  mkdir -p "$run_dir/lanes" "$run_dir/network-rotation"
  chmod 0700 "$root" "$out" "$run_dir" "$run_dir/lanes" \
    "$run_dir/network-rotation"
  for lane_id in workload-observation network-rotation workload-privacy \
    concurrent-crash-recovery; do
    printf 'synthetic lane %s\n' "$lane_id" >"$run_dir/lanes/$lane_id.log"
    chmod 0600 "$run_dir/lanes/$lane_id.log"
  done
  printf '%s\n' '{"schema":"synthetic-network-result/v1","result":"passed"}' \
    >"$run_dir/network-rotation/result.json"
  chmod 0600 "$run_dir/network-rotation/result.json"

  : >"$rows"
  find "$run_dir" -type f -print | LC_ALL=C sort |
    while IFS= read -r lane_evidence; do
      jq -cn \
        --arg path "${lane_evidence#"$run_dir"/}" \
        --arg sha256 "$(release_lima_resume_sha256 "$lane_evidence")" \
        --argjson bytes "$(wc -c <"$lane_evidence" | tr -d '[:space:]')" \
        '{path:$path,sha256:$sha256,bytes:$bytes,mode:"0600"}'
    done >"$rows"
  artifacts_json="$(jq -cs '.' "$rows")"

  local lanes='[]'
  for lane_id in workload-observation network-rotation workload-privacy \
    concurrent-crash-recovery; do
    lane_result=failed
    lane_exit=1
    lane_evidence=null
    if [ "$lane_id" = "network-rotation" ]; then
      lane_result=passed
      lane_exit=0
      lane_evidence_sha="$(release_lima_resume_sha256 \
        "$run_dir/network-rotation/result.json")"
      lane_evidence="$(jq -cn \
        --arg path 'network-rotation/result.json' \
        --arg sha256 "$lane_evidence_sha" \
        '{path:$path,sha256:$sha256}')"
    fi
    lane_log="lanes/$lane_id.log"
    lane_log_sha="$(release_lima_resume_sha256 "$run_dir/$lane_log")"
    lanes="$(jq -cn \
      --argjson current "$lanes" \
      --arg id "$lane_id" \
      --arg result "$lane_result" \
      --argjson exitCode "$lane_exit" \
      --arg log "$lane_log" \
      --arg logSHA256 "$lane_log_sha" \
      --argjson evidence "$lane_evidence" '
        $current + [{
          id:$id,result:$result,exitCode:$exitCode,
          startedAt:"2026-08-02T00:00:00Z",
          finishedAt:"2026-08-02T00:00:01Z",
          log:{path:$log,sha256:$logSHA256},evidence:$evidence
        }]
      ')"
  done
  jq -n \
    --arg commit "$commit" \
    --arg runId "$run_id" \
    --argjson lanes "$lanes" \
    --argjson artifacts "$artifacts_json" '
      {
        schema:"hideout.release-candidate-lima-evidence/v1",
        generatedAt:"2026-08-02T00:00:02Z",
        result:"failed",candidateAcceptance:false,
        source:{commit:$commit,dirty:false},runId:$runId,
        lanes:$lanes,
        validation:{
          workloadObservation:false,networkRotation:true,
          workloadPrivacy:false,concurrentCrashRecovery:false
        },
        claims:{},artifacts:$artifacts,limitations:[]
      }
    ' >"$run_dir/summary.json"
  chmod 0600 "$run_dir/summary.json"
  summary_sha="$(release_lima_resume_sha256 "$run_dir/summary.json")"
  jq -n \
    --arg commit "$commit" \
    --arg runId "$run_id" \
    --arg summary "$run_id/summary.json" \
    --arg summarySHA256 "$summary_sha" '
      {
        schema:"hideout.release-candidate-lima-pointer/v1",
        generatedAt:"2026-08-02T00:00:03Z",
        result:"failed",candidateAcceptance:false,
        source:{commit:$commit,dirty:false},runId:$runId,
        summary:$summary,summarySHA256:$summarySHA256
      }
    ' >"$out/result.json"
  chmod 0600 "$out/result.json"

  release_lima_resume_validate "$out" "$commit" false "$root" >/dev/null ||
    return 1
  if release_lima_resume_validate "$out" "$commit" true "$root" >/dev/null 2>&1; then
    return 1
  fi
  mkdir -p "$root/copied-run"
  chmod 0700 "$root/copied-run"
  release_lima_resume_copy_lane \
    "$run_dir" "$root/copied-run" network-rotation || return 1
  release_lima_resume_verify_lane_copy \
    "$run_dir/summary.json" "$root/copied-run" network-rotation || return 1
  cmp -s \
    "$run_dir/network-rotation/result.json" \
    "$root/copied-run/network-rotation/result.json" || return 1
  [ "$(release_lima_resume_file_mode \
    "$root/copied-run/network-rotation/result.json")" = "0600" ] || return 1
  printf 'unexpected\n' >"$root/copied-run/network-rotation/unexpected.txt"
  chmod 0600 "$root/copied-run/network-rotation/unexpected.txt"
  if release_lima_resume_verify_lane_copy \
    "$run_dir/summary.json" "$root/copied-run" network-rotation; then
    return 1
  fi
  find "$root/copied-run/network-rotation/unexpected.txt" -type f -delete
  result_saved="$root/result.saved"
  cp "$run_dir/network-rotation/result.json" "$result_saved"
  printf 'tamper\n' >>"$run_dir/network-rotation/result.json"
  if release_lima_resume_validate "$out" "$commit" false "$root" >/dev/null 2>&1; then
    return 1
  fi
  cp "$result_saved" "$run_dir/network-rotation/result.json"
  chmod 0644 "$run_dir/network-rotation/result.json"
  if release_lima_resume_validate "$out" "$commit" false "$root" >/dev/null 2>&1; then
    return 1
  fi
  chmod 0600 "$run_dir/network-rotation/result.json"
  pointer_saved="$root/pointer.saved"
  cp "$out/result.json" "$pointer_saved"
  jq '.source.commit = "ffffffffffffffffffffffffffffffffffffffff"' \
    "$pointer_saved" >"$out/result.json"
  chmod 0600 "$out/result.json"
  if release_lima_resume_validate "$out" "$commit" false "$root" >/dev/null 2>&1; then
    return 1
  fi
  cp "$pointer_saved" "$out/result.json"
  chmod 0600 "$out/result.json"
  printf 'release-candidate-lima: resume-preflight=passed\n'
}
