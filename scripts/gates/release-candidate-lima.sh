#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"
# shellcheck source=scripts/lib/gate-result.sh
. "$repo_root/scripts/lib/gate-result.sh"
gate_completed=0
# shellcheck source=scripts/lib/gate2-concurrent-sessions.sh
. "$repo_root/scripts/lib/gate2-concurrent-sessions.sh"
# shellcheck source=scripts/lib/gate2-concurrent-performance.sh
. "$repo_root/scripts/lib/gate2-concurrent-performance.sh"
# shellcheck source=scripts/lib/release-candidate-lima-resume.sh
. "$repo_root/scripts/lib/release-candidate-lima-resume.sh"

umask 077
out="$repo_root/.artifacts/045/lima"
preflight_only=0
resume_passed=0
concurrent_samples="${HIDEOUT_LIMA_CONCURRENT_SAMPLES:-1}"
concurrent_warmups="${HIDEOUT_LIMA_CONCURRENT_WARMUPS:-0}"

usage() {
  printf '%s\n' \
    "Usage: scripts/gates/release-candidate-lima.sh [--preflight] [--resume-passed] [--out DIR] [--concurrent-samples N] [--concurrent-warmups N]" \
    "" \
    "Runs every required real macOS/arm64 Lima lane for feature 045:" \
    "concurrent observation and attribution, online proxy rotation, injected" \
    "loss/quota/cleanup, target tamper, concurrent isolation, daemon crash," \
    "stale-owner recovery, and post-recovery execution." \
    "" \
    "--resume-passed revalidates and reuses passed lanes from the immediately" \
    "preceding clean failed aggregate only when commit, digests, modes, and inventory" \
    "remain exact; failed or unproved lanes execute normally." \
    "" \
    "This command writes private digest-bound evidence and never publishes."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --preflight)
      preflight_only=1
      shift
      ;;
    --resume-passed)
      resume_passed=1
      shift
      ;;
    --out)
      [ "$#" -ge 2 ] && [ -n "${2:-}" ] || {
        printf 'release-candidate-lima: --out requires a directory\n' >&2
        exit 2
      }
      out="$2"
      shift 2
      ;;
    --concurrent-samples)
      [ "$#" -ge 2 ] && [ -n "${2:-}" ] || {
        printf 'release-candidate-lima: --concurrent-samples requires a value\n' >&2
        exit 2
      }
      concurrent_samples="$2"
      shift 2
      ;;
    --concurrent-warmups)
      [ "$#" -ge 2 ] && [ -n "${2:-}" ] || {
        printf 'release-candidate-lima: --concurrent-warmups requires a value\n' >&2
        exit 2
      }
      concurrent_warmups="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'release-candidate-lima: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

case "$concurrent_samples:$concurrent_warmups" in
  *[!0-9:]* | :* | *:)
    printf 'release-candidate-lima: sample bounds must be integers\n' >&2
    exit 2
    ;;
esac
if [ "$concurrent_samples" -lt 1 ]; then
  printf 'release-candidate-lima: --concurrent-samples must be positive\n' >&2
  exit 2
fi

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    printf 'release-candidate-lima: missing required command: %s\n' "$1" >&2
    exit 1
  }
}

for required_command in \
  awk bash cmp find git go jq limactl perl security shasum ssh stat; do
  require_command "$required_command"
done
[ "$(uname -s)" = "Darwin" ] || {
  printf 'release-candidate-lima: real candidate lane requires macOS\n' >&2
  exit 1
}
[ "$(uname -m)" = "arm64" ] || {
  printf 'release-candidate-lima: real candidate lane requires arm64\n' >&2
  exit 1
}

scratch_parent="$(CDPATH='' cd -- "${TMPDIR:-/tmp}" && pwd -P)"
if [ "$preflight_only" -eq 1 ]; then
  [ "$resume_passed" -eq 0 ] || {
    printf 'release-candidate-lima: --preflight and --resume-passed are mutually exclusive\n' >&2
    exit 2
  }
  scratch_preflight="$(mktemp -d "$scratch_parent/hideout-lima-preflight.XXXXXX")"
  # Invoked indirectly by the EXIT trap.
  # shellcheck disable=SC2329
  cleanup_preflight() {
    local exit_status=$?
    case "${scratch_preflight:-}" in
      "$scratch_parent"/hideout-lima-preflight.*)
        [ ! -d "$scratch_preflight" ] ||
          find "$scratch_preflight" -depth -delete
        ;;
    esac
    if [ "$exit_status" -eq 0 ]; then
      gate_require_completion "release-candidate-lima-preflight"
    fi
  }
  trap cleanup_preflight EXIT
  scripts/gates/workload-observation-lima.sh \
    --require-real --preflight --out "$scratch_preflight/workload"
  scripts/gates/network-rotation-lima.sh \
    --require-real --preflight --out "$scratch_preflight/network-rotation"
  scripts/gates/workload-privacy-lima.sh \
    --require-real --preflight --out "$scratch_preflight/privacy"
  release_lima_resume_preflight "$scratch_preflight/resume"
  bash -n \
    scripts/lib/gate2-concurrent-sessions.sh \
    scripts/lib/gate2-concurrent-performance.sh \
    scripts/lib/release-candidate-lima-resume.sh \
    scripts/gates/release-candidate-lima.sh
  gate_completed=1
  printf 'release-candidate-lima: preflight=passed\n'
  exit 0
fi

if [ -L "$out" ]; then
  printf 'release-candidate-lima: evidence directory must not be a symlink\n' >&2
  exit 1
fi
mkdir -p "$out"
out="$(cd "$out" && pwd -P)"
chmod 0700 "$out"

source_commit="$(git rev-parse HEAD)"
if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then
  source_dirty=true
else
  source_dirty=false
fi
scratch="$(mktemp -d "$scratch_parent/hideout-release-lima.XXXXXX")"
cleanup() {
  local exit_status=$?
  case "${scratch:-}" in
    "$scratch_parent"/hideout-release-lima.*)
      [ ! -d "$scratch" ] || find "$scratch" -depth -delete
      ;;
    *)
      printf 'release-candidate-lima: refusing unexpected scratch cleanup\n' >&2
      ;;
  esac
  if [ "$exit_status" -eq 0 ]; then
    gate_require_completion "release-candidate-lima"
  fi
}
trap cleanup EXIT

resume_source_json='null'
resume_source_run_dir=""
reused_lanes_json='[]'
if [ "$resume_passed" -eq 1 ]; then
  if ! resume_source_json="$(
    release_lima_resume_validate \
      "$out" "$source_commit" "$source_dirty" "$scratch"
  )"; then
    printf '%s\n' \
      'release-candidate-lima: previous aggregate is not eligible for passed-lane reuse' \
      >&2
    exit 1
  fi
  resume_source_run_id="$(jq -er '.runId' <<<"$resume_source_json")"
  resume_source_run_dir="$out/$resume_source_run_id"
  printf 'release-candidate-lima: resume source=%s\n' "$resume_source_run_id"
fi

run_id="run-$(date -u +'%Y%m%dT%H%M%SZ')-$$"
run_dir="$out/$run_id"
[ ! -e "$run_dir" ] || {
  printf 'release-candidate-lima: run directory already exists\n' >&2
  exit 1
}
mkdir -p "$run_dir/lanes"
chmod 0700 "$run_dir" "$run_dir/lanes"

sha256_file() {
  shasum -a 256 "$1" | awk '{print $1}'
}

file_mode() {
  stat -f '%Lp' "$1" 2>/dev/null ||
    stat -c '%a' "$1" 2>/dev/null
}

lanes_json='[]'
failed_lanes=0
run_lane() {
  local lane_id="$1"
  local lane_result_path="$2"
  shift 2
  local lane_log="$run_dir/lanes/$lane_id.log"
  local lane_started lane_finished lane_exit lane_state
  local lane_result_rel="" lane_result_sha=""
  lane_started="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
  printf 'release-candidate-lima: running %s\n' "$lane_id"
  set +e
  (
    set -e
    "$@"
  ) >"$lane_log" 2>&1
  lane_exit=$?
  set -e
  lane_finished="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
  [ -s "$lane_log" ] ||
    printf 'lane produced no output (exit=%d)\n' "$lane_exit" >"$lane_log"
  chmod 0600 "$lane_log"
  if [ "$lane_exit" -eq 0 ]; then
    lane_state="passed"
    printf 'release-candidate-lima: %s passed\n' "$lane_id"
  else
    lane_state="failed"
    failed_lanes=$((failed_lanes + 1))
    printf 'release-candidate-lima: %s failed (exit=%d)\n' \
      "$lane_id" "$lane_exit" >&2
    tail -30 "$lane_log" >&2
  fi
  if [ -f "$lane_result_path" ]; then
    lane_result_rel="${lane_result_path#"$run_dir"/}"
    lane_result_sha="$(sha256_file "$lane_result_path")"
  fi
  lanes_json="$(
    jq -c \
      --arg id "$lane_id" \
      --arg result "$lane_state" \
      --arg startedAt "$lane_started" \
      --arg finishedAt "$lane_finished" \
      --arg log "lanes/$lane_id.log" \
      --arg logSHA256 "$(sha256_file "$lane_log")" \
      --arg evidence "$lane_result_rel" \
      --arg evidenceSHA256 "$lane_result_sha" \
      --argjson exitCode "$lane_exit" \
      '. + [{
        id:$id,
        execution:"executed",
        result:$result,
        exitCode:$exitCode,
        startedAt:$startedAt,
        finishedAt:$finishedAt,
        log:{path:$log,sha256:$logSHA256},
        evidence:
          (if $evidence == "" then null
           else {path:$evidence,sha256:$evidenceSHA256}
           end)
      }]' <<<"$lanes_json"
  )"
}

lane_reusable() {
  local lane_id="$1"
  [ "$resume_passed" -eq 1 ] || return 1
  jq -e --arg id "$lane_id" \
    '.passedLanes | index($id) != null' \
    <<<"$resume_source_json" >/dev/null || return 1
  if [ "$lane_id" = "concurrent-crash-recovery" ]; then
    jq -e \
      --argjson samples "$concurrent_samples" \
      --argjson warmups "$concurrent_warmups" '
        .methodology.samples == $samples and
        .methodology.warmups == $warmups
      ' "$resume_source_run_dir/concurrent/logs/performance.json" \
      >/dev/null || return 1
  fi
}

reuse_lane() {
  local lane_id="$1" lane_dir="$2" lane_result_path="$3"
  local lane_log="$run_dir/lanes/$lane_id.log"
  local lane_started lane_finished lane_result_rel lane_result_sha
  local source_summary_sha source_lane_log_sha
  lane_started="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
  source_summary_sha="$(jq -er '.summarySHA256' <<<"$resume_source_json")"
  source_lane_log_sha="$(
    jq -er --arg id "$lane_id" \
      '.lanes[] | select(.id == $id) | .log.sha256' \
      "$resume_source_run_dir/summary.json"
  )"
  printf 'release-candidate-lima: revalidating %s from %s\n' \
    "$lane_id" "$resume_source_run_id"
  release_lima_resume_copy_lane \
    "$resume_source_run_dir" "$run_dir" "$lane_dir"
  release_lima_resume_verify_lane_copy \
    "$resume_source_run_dir/summary.json" "$run_dir" "$lane_dir"
  lane_finished="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
  jq -n \
    --arg laneId "$lane_id" \
    --arg sourceRunId "$resume_source_run_id" \
    --arg sourceSummarySHA256 "$source_summary_sha" \
    --arg sourceLaneLogSHA256 "$source_lane_log_sha" '
      {
        schema:"hideout.release-candidate-lima-lane-reuse/v1",
        result:"passed",
        laneId:$laneId,
        sourceRunId:$sourceRunId,
        sourceSummarySHA256:$sourceSummarySHA256,
        sourceLaneLogSHA256:$sourceLaneLogSHA256
      }
    ' >"$lane_log"
  chmod 0600 "$lane_log"
  [ -f "$lane_result_path" ] && [ ! -L "$lane_result_path" ] || {
    printf 'release-candidate-lima: reused %s result is unavailable\n' \
      "$lane_id" >&2
    exit 1
  }
  lane_result_rel="${lane_result_path#"$run_dir"/}"
  lane_result_sha="$(sha256_file "$lane_result_path")"
  lanes_json="$(
    jq -c \
      --arg id "$lane_id" \
      --arg startedAt "$lane_started" \
      --arg finishedAt "$lane_finished" \
      --arg log "lanes/$lane_id.log" \
      --arg logSHA256 "$(sha256_file "$lane_log")" \
      --arg evidence "$lane_result_rel" \
      --arg evidenceSHA256 "$lane_result_sha" \
      --arg sourceRunId "$resume_source_run_id" \
      --arg sourceSummarySHA256 "$source_summary_sha" \
      '. + [{
        id:$id,
        execution:"reused",
        result:"passed",
        exitCode:0,
        startedAt:$startedAt,
        finishedAt:$finishedAt,
        log:{path:$log,sha256:$logSHA256},
        evidence:{path:$evidence,sha256:$evidenceSHA256},
        reuse:{
          sourceRunId:$sourceRunId,
          sourceSummarySHA256:$sourceSummarySHA256
        }
      }]' <<<"$lanes_json"
  )"
  reused_lanes_json="$(
    jq -c --arg id "$lane_id" '. + [$id]' <<<"$reused_lanes_json"
  )"
  printf 'release-candidate-lima: %s reused\n' "$lane_id"
}

run_or_reuse_lane() {
  local lane_id="$1" lane_dir="$2" lane_result_path="$3"
  shift 3
  if lane_reusable "$lane_id"; then
    reuse_lane "$lane_id" "$lane_dir" "$lane_result_path"
    return
  fi
  run_lane "$lane_id" "$lane_result_path" "$@"
}

workload_result="$run_dir/workload/result.json"
rotation_result="$run_dir/network-rotation/result.json"
privacy_result="$run_dir/privacy/result.json"
concurrent_result="$run_dir/concurrent/result.json"

run_or_reuse_lane workload-observation workload "$workload_result" \
  scripts/gates/workload-observation-lima.sh \
  --require-real --out "$run_dir/workload"
run_or_reuse_lane network-rotation network-rotation "$rotation_result" \
  scripts/gates/network-rotation-lima.sh \
  --require-real --out "$run_dir/network-rotation"
run_or_reuse_lane workload-privacy privacy "$privacy_result" \
  scripts/gates/workload-privacy-lima.sh \
  --require-real --out "$run_dir/privacy"
run_or_reuse_lane concurrent-crash-recovery concurrent "$concurrent_result" \
  gate2_concurrent_sessions_run \
  "$repo_root" "$run_dir/concurrent" \
  "$concurrent_samples" "$concurrent_warmups"

safe_relative_path() {
  case "$1" in
    "" | /* | .. | ../* | */.. | */../*) return 1 ;;
    *) return 0 ;;
  esac
}

manifest_sequence=0
verify_artifact_manifest() {
  local base="$1"
  local manifest="$2"
  jq -e '.artifacts | type == "array" and length > 0' "$manifest" \
    >/dev/null || return 1
  manifest_sequence=$((manifest_sequence + 1))
  local rows="$scratch/manifest-$manifest_sequence.tsv"
  jq -r '
    .artifacts[] |
    [.path, .sha256, (.mode // "0600")] |
    @tsv
  ' "$manifest" >"$rows" || return 1
  local relative expected_sha expected_mode artifact_path
  while IFS=$'\t' read -r relative expected_sha expected_mode; do
    safe_relative_path "$relative" || return 1
    artifact_path="$base/$relative"
    [ -f "$artifact_path" ] && [ ! -L "$artifact_path" ] || return 1
    [ "$(sha256_file "$artifact_path")" = "$expected_sha" ] || return 1
    [ "$expected_mode" = "0600" ] || return 1
  done <"$rows"
}

verify_workload_observation() {
  [ -f "$workload_result" ] || return 1
  jq -e \
    --arg commit "$source_commit" \
    --argjson dirty "$source_dirty" '
      .schema == "hideout.workload-observation-lima-pointer/v1" and
      .result == "passed" and
      .source == {commit:$commit,dirty:$dirty} and
      .candidateAcceptance == ($dirty | not)
    ' "$workload_result" >/dev/null || return 1
  local summary_rel summary_path
  summary_rel="$(jq -er '.summary' "$workload_result")" || return 1
  safe_relative_path "$summary_rel" || return 1
  summary_path="$run_dir/workload/$summary_rel"
  [ -f "$summary_path" ] || return 1
  [ "$(sha256_file "$summary_path")" = \
    "$(jq -er '.summarySHA256' "$workload_result")" ] || return 1
  jq -e \
    --arg commit "$source_commit" \
    --argjson dirty "$source_dirty" '
      .schema == "hideout.workload-observation-lima-evidence/v1" and
      .result == "passed" and
      .source == {commit:$commit,dirty:$dirty} and
      .candidateAcceptance == ($dirty | not) and
      .assertions.networkDNSFieldsComplete == true and
      .assertions.honestUnknownDomainAndRoute == true and
      .assertions.fullDomainCorrelationMatrix == true and
      .assertions.mediatorActorMatrix == true and
      .assertions.intentionallyUnattributableSample == true and
      .linuxAttributionTests == [
        "TestNetworkCorrelatorNormalizesConnect4Connect6UDPAndTCP",
        "TestNetworkCorrelatorUsesTTLBoundSameExecutionDNSInference",
        "TestNetworkCorrelatorDoesNotGuessSharedIPCacheLiteralOrEncryptedDNS",
        "TestNetworkCorrelatorUsesValidatedProxyTargetAsExactAndRejectsCrossBoundary",
        "TestNormalizeKernelConnectionPreservesExactActorEndpointAndRouteEvidence",
        "TestNormalizeKernelConnectionKeepsMissingActorAndEgressUnknown",
        "TestNormalizeKernelConnectionUsesEventCredentialsForExactExecution",
        "TestNormalizeKernelConnectionAttributesUnexecedChildToInheritedExecution",
        "TestNormalizeKernelConnectionRejectsMismatchedEvidence",
        "TestPacketFromKernelRecordAttributesForkedChildToInheritedExecution",
        "TestProxyChunkFromKernelRecordPreservesForkedChildActor"
      ] and
      all(.assertions[]; . == true)
    ' "$summary_path" >/dev/null || return 1
  verify_artifact_manifest "$(dirname "$summary_path")" "$summary_path"
}

verify_network_rotation() {
  [ -f "$rotation_result" ] || return 1
  jq -e \
    --arg commit "$source_commit" \
    --argjson dirty "$source_dirty" '
      .schema == "hideout.network-rotation-lima-pointer/v1" and
      .result == "passed" and
      .source == {commit:$commit,dirty:$dirty} and
      .candidateAcceptance == ($dirty | not)
    ' "$rotation_result" >/dev/null || return 1
  local summary_rel summary_path
  summary_rel="$(jq -er '.summary' "$rotation_result")" || return 1
  safe_relative_path "$summary_rel" || return 1
  summary_path="$run_dir/network-rotation/$summary_rel"
  [ -f "$summary_path" ] || return 1
  [ "$(sha256_file "$summary_path")" = \
    "$(jq -er '.summarySHA256' "$rotation_result")" ] || return 1
  jq -e \
    --arg commit "$source_commit" \
    --argjson dirty "$source_dirty" '
      .schema == "hideout.network-rotation-lima-evidence/v1" and
      .result == "passed" and
      .source == {commit:$commit,dirty:$dirty} and
      .candidateAcceptance == ($dirty | not) and
      all(.checks[]; . == true) and
      .checks.fullNetworkTransitionSequenceProved == true and
      .checks.networkCrashBoundaryMatrix == true and
      .checks.existingConnectionRetainsPriorRoute == true and
      .networkCrashMatrix.result == "passed" and
      (.networkCrashMatrix.boundaries | length) == 5 and
      (.networkCrashMatrix.boundaries | map(.effect)) == [
        "network-stage",
        "network-probe",
        "network-activate",
        "network-prove",
        "network-drain"
      ] and
      [.networkCrashMatrix.boundaries[].terminalPhase] ==
        ["rolled-back","rolled-back","succeeded","succeeded","succeeded"] and
      all(.networkCrashMatrix.boundaries[];
        .crashExitCode == 86 and
        .exactBoundary == true and
        .noMutationReplay == true and
        .daemonIdentityChanged == true and
        .vmBootPreservedThroughNetworkReconciliation == true and
        .staleOwnerFailedClosed == true and
        .explicitLifecycleRecovery == true and
        .freshBootAfterLifecycleRecovery == true and
        .independentRouteProbe.passed == true) and
      .connectionCounts.proxyOneFinal ==
        .connectionCounts.proxyOneBefore and
      .connectionCounts.proxyTwoFinal >
        .connectionCounts.proxyTwoAfterRotation and
      (.routeProof.heldHTTPConnection |
        test("^127[.]0[.]0[.]1:[0-9]+$")) and
      .routeProof.beforePath == "/fixture.txt?phase=held-before" and
      .routeProof.afterPath == "/fixture.txt?phase=held-after"
    ' "$summary_path" >/dev/null || return 1
  verify_artifact_manifest "$(dirname "$summary_path")" "$summary_path"
}

verify_workload_privacy() {
  [ -f "$privacy_result" ] || return 1
  jq -e \
    --arg commit "$source_commit" \
    --argjson dirty "$source_dirty" '
      .schema == "hideout.workload-privacy-lima-evidence/v1" and
      .result == "passed" and
      .source == {commit:$commit,dirty:$dirty} and
      .candidateAcceptance == ($dirty | not) and
      .checks.exactOwnerRecreate == "passed" and
      .checks.auditPreservation == "passed" and
      all(.checks[]; . == "passed") and
      (.artifacts | length) == 10
    ' "$privacy_result" >/dev/null || return 1
  local privacy_summary="$run_dir/privacy/reports/privacy-summary.json"
  [ -f "$privacy_summary" ] || return 1
  jq -e '
    .cleanup.passed == true and
    .cleanup.reusableOwnerRetainedAfterSessionExit == true and
    .cleanup.exactOwnerDirectoryAbsent == true and
    .cleanup.exactOwnerQueryRejected == true and
    .cleanup.environmentRecordAbsent == true and
    .cleanup.limaInstanceAbsent == true and
    .cleanup.auditPreserved == true and
    .cleanup.recreatedEnvironmentDifferent == true and
    .cleanup.recreatedIncarnationDifferent == true and
    .cleanup.recreatedInstanceDifferent == true and
    .cleanup.recreatedOwnerQueryableBeforeOwnCleanup == true and
    .cleanup.recreatedOwnerRemovedOnlyByOwnCleanup == true and
    .redaction.processListingScanned == true and
    .redaction.keychainMetadataScanned == true and
    all(.redaction.sinkCanaryHits[]; . == 0) and
    all(.redaction.canaryClassHits[]; . == 0) and
    .redaction.redactionFailurePersistedRecords == 0 and
    all([
      .cleanup.newEnvironmentSHA256,
      .cleanup.newIncarnationSHA256,
      .cleanup.newInstanceSHA256,
      .cleanup.retainedAuditSHA256
    ][]; test("^[a-f0-9]{64}$"))
  ' "$privacy_summary" >/dev/null || return 1
  verify_artifact_manifest "$run_dir/privacy" "$privacy_result"
}

verify_concurrent_crash_recovery() {
  [ -f "$concurrent_result" ] || return 1
  jq -e \
    --arg commit "$source_commit" \
    --argjson dirty "$source_dirty" '
      .schema == "hideout.concurrent-sessions-gate2/v1" and
      .status == "passed" and
      .commit == $commit and
      .dirty == $dirty and
      .candidateAcceptance == ($dirty | not) and
      all(.checks[]; . == true)
    ' "$concurrent_result" >/dev/null || return 1
  local pty_path="$run_dir/concurrent/logs/session-pty.json"
  local performance_path="$run_dir/concurrent/logs/performance.json"
  [ -f "$pty_path" ] && [ -f "$performance_path" ] || return 1
  [ "$(sha256_file "$pty_path")" = \
    "$(jq -er '.artifacts.sessionPTYEvidenceSHA256' "$concurrent_result")" ] ||
    return 1
  jq -e \
    --arg commit "$source_commit" \
    --argjson dirty "$source_dirty" \
    --argjson samples "$concurrent_samples" \
    --argjson warmups "$concurrent_warmups" '
      .status == "passed" and
      .candidate.commit == $commit and
      .candidate.dirty == $dirty and
      .methodology.samples == $samples and
      .methodology.warmups == $warmups and
      .warmAttach.p95Ms <= .methodology.readyThresholdMs
    ' "$performance_path" >/dev/null || return 1
}

workload_valid=false
rotation_valid=false
privacy_valid=false
concurrent_valid=false
if verify_workload_observation; then workload_valid=true; fi
if verify_network_rotation; then rotation_valid=true; fi
if verify_workload_privacy; then privacy_valid=true; fi
if verify_concurrent_crash_recovery; then concurrent_valid=true; fi

aggregate_result="passed"
if [ "$failed_lanes" -ne 0 ] ||
  [ "$workload_valid" != true ] ||
  [ "$rotation_valid" != true ] ||
  [ "$privacy_valid" != true ] ||
  [ "$concurrent_valid" != true ]; then
  aggregate_result="failed"
fi

find "$run_dir" -type d -exec chmod 0700 {} +
find "$run_dir" -type f -exec chmod 0600 {} +
artifact_rows="$scratch/artifacts.jsonl"
: >"$artifact_rows"
find "$run_dir" -type f -print | LC_ALL=C sort |
  while IFS= read -r artifact_path; do
    relative="${artifact_path#"$run_dir"/}"
    jq -cn \
      --arg path "$relative" \
      --arg sha256 "$(sha256_file "$artifact_path")" \
      --argjson bytes "$(wc -c <"$artifact_path" | tr -d '[:space:]')" \
      '{path:$path,sha256:$sha256,bytes:$bytes,mode:"0600"}'
  done >"$artifact_rows"

summary_path="$run_dir/summary.json"
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg result "$aggregate_result" \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" \
  --arg runId "$run_id" \
  --arg hostOS "$(uname -s)" \
  --arg hostArch "$(uname -m)" \
  --arg limaVersion "$(limactl --version 2>&1 | head -1)" \
  --argjson workloadValid "$workload_valid" \
  --argjson rotationValid "$rotation_valid" \
  --argjson privacyValid "$privacy_valid" \
  --argjson concurrentValid "$concurrent_valid" \
  --argjson lanes "$lanes_json" \
  --argjson resumeSource "$resume_source_json" \
  --argjson reusedLanes "$reused_lanes_json" \
  --slurpfile artifacts "$artifact_rows" '
    {
      schema:"hideout.release-candidate-lima-evidence/v1",
      generatedAt:$generatedAt,
      result:$result,
      candidateAcceptance:
        ($result == "passed" and ($dirty | not)),
      source:{commit:$commit,dirty:$dirty},
      runId:$runId,
      host:{os:$hostOS,arch:$hostArch,limaVersion:$limaVersion},
      lanes:$lanes,
      progressiveReuse:
        (if $resumeSource == null then
          {mode:"none",reusedLanes:[]}
        else
          {
            mode:"exact-previous-failed-aggregate",
            sourceRunId:$resumeSource.runId,
            sourceSummarySHA256:$resumeSource.summarySHA256,
            reusedLanes:$reusedLanes
          }
        end),
      validation:{
        workloadObservation:$workloadValid,
        networkRotation:$rotationValid,
        workloadPrivacy:$privacyValid,
        concurrentCrashRecovery:$concurrentValid
      },
      claims:{
        concurrentObservation:$workloadValid,
        exactAttribution:$workloadValid,
        pidReuse:$workloadValid,
        onlineProxyRotation:$rotationValid,
        networkBoundaryCrashRecovery:$rotationValid,
        targetTamperRejected:$rotationValid,
        lossAccounting:$privacyValid,
        quotaEnforced:$privacyValid,
        exactOwnerCleanup:$privacyValid,
        newIncarnationPreserved:$privacyValid,
        auditPreserved:$privacyValid,
        prePersistenceRedaction:$privacyValid,
        concurrentIsolation:$concurrentValid,
        crashClientsUnblocked:$concurrentValid,
        terminalRestored:$concurrentValid,
        staleOwnerFailedClosed:$concurrentValid,
        explicitRecovery:$concurrentValid,
        postRecoveryRun:$concurrentValid
      },
      artifacts:$artifacts,
      limitations:
        ([
          "The concurrent lane uses a minimal warm-attach sample here; the statistically sized performance claim belongs to T156.",
          "Each lane builds from the exact recorded source tree; packaged-candidate identity is established later by T158 and T163."
        ] +
        if $dirty then
          ["This binds a dirty development checkout and cannot accept an exact release candidate."]
        else
          []
        end)
    }
  ' >"$summary_path"
chmod 0600 "$summary_path"

jq -e '
  (.result == "passed") ==
    (all(.validation[]; . == true) and
     all(.lanes[]; .result == "passed")) and
  all(.lanes[];
    (.execution == "executed" or .execution == "reused") and
    (if .execution == "reused"
     then (.reuse.sourceRunId | type) == "string" and
       (.reuse.sourceSummarySHA256 | test("^[a-f0-9]{64}$"))
     else (.reuse // null) == null
     end)) and
  ([.lanes[] | select(.execution == "reused") | .id] ==
    .progressiveReuse.reusedLanes) and
  (if .progressiveReuse.mode == "none"
   then .progressiveReuse.reusedLanes == []
   else .progressiveReuse.mode == "exact-previous-failed-aggregate" and
     (.progressiveReuse.sourceSummarySHA256 |
       test("^[a-f0-9]{64}$"))
   end) and
  (.result != "passed" or all(.claims[]; . == true)) and
  all(.artifacts[];
    .mode == "0600" and
    (.sha256 | test("^[a-f0-9]{64}$")))
' "$summary_path" >/dev/null || {
  printf 'release-candidate-lima: aggregate summary is internally inconsistent\n' >&2
  exit 1
}

summary_sha="$(sha256_file "$summary_path")"
result_tmp="$(mktemp "$out/.result.XXXXXX")"
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg result "$aggregate_result" \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" \
  --arg runId "$run_id" \
  --arg summary "$run_id/summary.json" \
  --arg summarySHA256 "$summary_sha" '
    {
      schema:"hideout.release-candidate-lima-pointer/v1",
      generatedAt:$generatedAt,
      result:$result,
      candidateAcceptance:
        ($result == "passed" and ($dirty | not)),
      source:{commit:$commit,dirty:$dirty},
      runId:$runId,
      summary:$summary,
      summarySHA256:$summarySHA256
    }
  ' >"$result_tmp"
chmod 0600 "$result_tmp"
mv "$result_tmp" "$out/result.json"
find "$run_dir" -type d -exec chmod 0700 {} +
find "$run_dir" -type f -exec chmod 0600 {} +

printf 'release-candidate-lima: evidence=%s\n' "$summary_path"
if [ "$aggregate_result" != "passed" ]; then
  printf 'release-candidate-lima: failed\n' >&2
  exit 1
fi
gate_completed=1
printf 'release-candidate-lima: passed\n'
