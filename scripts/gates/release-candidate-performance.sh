#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"
# shellcheck source=scripts/lib/gate-result.sh
. "$repo_root/scripts/lib/gate-result.sh"
gate_completed=0

umask 077
evidence_out="$repo_root/.artifacts/045/performance"
preflight_only=0
attach_samples=7
attach_warmups=2
browser_samples=5
local_samples=30
local_warmups=5
process_samples=15

usage() {
  printf '%s\n' \
    "Usage: scripts/gates/release-candidate-performance.sh [--preflight] [--out DIR]" \
    "" \
    "Measures production query/render latency, daemon/TUI RSS, five real-" \
    "Chrome freshness samples, seven real-Lima warm attaches, paired reference" \
    "overhead, observer CPU/RSS/event/drop rate, and real quota overshoot." \
    "Evidence is private, immutable, digest-bound, and never published."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --preflight)
      preflight_only=1
      shift
      ;;
    --out)
      [ "$#" -ge 2 ] && [ -n "${2:-}" ] || {
        printf 'release-candidate-performance: --out requires a directory\n' >&2
        exit 2
      }
      evidence_out="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf \
        'release-candidate-performance: unknown argument: %s\n' \
        "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    printf \
      'release-candidate-performance: missing required command: %s\n' \
      "$1" >&2
    exit 1
  }
}

validate_summary() {
  local summary_path="$1"
  local expected_tree_sha="$2"
  local expected_artifact_count="$3"

  jq -e \
    --arg treeSHA256 "$expected_tree_sha" \
    --argjson artifactCount "$expected_artifact_count" '
      .schema == "hideout.release-candidate-performance/v1" and
      .result == "passed" and
      .source.treeSHA256 == $treeSHA256 and
      .source.stableAcrossRun == true and
      .candidate.exactSourceTreeBound == true and
      .methodology.rawSamplesPresent == true and
      .methodology.percentilesIndependentlyRecomputed == true and
      .methodology.unitsStable == true and
      all(.validation[]; . == true) and
      (.claimReceipts | length) == 3 and
      all(.claimReceipts[]; .passed == true) and
      (.artifacts | length) == $artifactCount and
      all(.artifacts[];
        (.sha256 | test("^[a-f0-9]{64}$")) and
        (.bytes | type) == "number" and
        .bytes >= 0 and
        .bytes == (.bytes | floor) and
        .mode == "0600")
    ' "$summary_path" >/dev/null
}

for required_command in \
  awk bash cmp find git go grep jq limactl node perl ps rg sed shasum sort ssh \
  stat tail tr wc; do
  require_command "$required_command"
done

expected_contract_claims='["C05","C06","CL03"]'
if ! jq -e \
  --argjson expected "$expected_contract_claims" '
    [.claims[] |
      select((.judges // []) | index("release-candidate-performance")) |
      .id] | sort == ($expected | sort)
  ' scripts/mutation/045/contracts.json >/dev/null; then
  printf \
    'release-candidate-performance: claim contract set drifted\n' \
    >&2
  exit 1
fi

if [ "$preflight_only" -eq 1 ]; then
  preflight_root="$(mktemp -d /tmp/hideout-performance-preflight.XXXXXX)"
  # Invoked indirectly by the EXIT trap.
  # shellcheck disable=SC2329
  cleanup_preflight() {
    local exit_status=$?
    case "${preflight_root:-}" in
      /tmp/hideout-performance-preflight.*)
        [ ! -d "$preflight_root" ] ||
          find "$preflight_root" -depth -delete
        ;;
      *)
        printf \
          'release-candidate-performance: refusing unexpected preflight cleanup\n' \
          >&2
        ;;
    esac
    if [ "$exit_status" -eq 0 ]; then
      gate_require_completion "release-candidate-performance-preflight"
    fi
  }
  trap cleanup_preflight EXIT
  summary_contract_fixture="$preflight_root/summary.json"
  summary_contract_negative="$preflight_root/summary-negative.json"
  jq -n '
    {
      schema:"hideout.release-candidate-performance/v1",
      result:"passed",
      source:{treeSHA256:"preflight-tree",stableAcrossRun:true},
      candidate:{exactSourceTreeBound:true},
      methodology:{
        rawSamplesPresent:true,
        percentilesIndependentlyRecomputed:true,
        unitsStable:true
      },
      validation:{thresholds:true},
      claimReceipts:[
        {passed:true},
        {passed:true},
        {passed:true}
      ],
      artifacts:[{
        sha256:("0" * 64),
        bytes:0,
        mode:"0600"
      }]
    }
  ' >"$summary_contract_fixture"
  validate_summary "$summary_contract_fixture" "preflight-tree" 1 || {
    printf \
      'release-candidate-performance: empty evidence contract regressed\n' \
      >&2
    exit 1
  }
  jq '.artifacts[0].bytes = -1' \
    "$summary_contract_fixture" >"$summary_contract_negative"
  if validate_summary "$summary_contract_negative" "preflight-tree" 1; then
    printf \
      'release-candidate-performance: negative evidence size was accepted\n' \
      >&2
    exit 1
  fi
  reference_baseline="$preflight_root/reference-baseline.txt"
  reference_observed="$preflight_root/reference-observed.txt"
  reference_result="$preflight_root/reference-result.json"
  reference_failure_log="$preflight_root/reference-failure.log"
  printf '%s\n' 100 101 102 >"$reference_baseline"
  printf '%s\n' 105 106 107 >"$reference_observed"
  # shellcheck source=scripts/lib/gate2-concurrent-performance.sh
  . "$repo_root/scripts/lib/gate2-concurrent-performance.sh"
  gate2_034_finalize_reference_result \
    "$reference_result" "$reference_baseline" "$reference_observed" \
    3 1 1000 "$(printf '0%.0s' {1..64})" >/dev/null
  jq -e '
    def nr($values; $percentile):
      ($values | sort) as $sorted |
      (($sorted | length) * $percentile + 99) / 100 | floor as $rank |
      $sorted[$rank - 1];
    .elapsedOverhead.thresholdPassed == true and
    .elapsedOverhead.threshold == 10 and
    (.elapsedOverhead.samples | length) == 3 and
    .elapsedOverhead.median ==
      nr(.elapsedOverhead.samples; 50) and
    .methodology.samplePairing ==
      "index-aligned-adjacent-counterbalanced" and
    .methodology.overheadAggregation ==
      "nearest-rank-median-of-paired-percent-deltas" and
    (.baseline.samples | length) == 3 and
    (.observed.samples | length) == 3
  ' "$reference_result" >/dev/null || {
    printf \
      'release-candidate-performance: passing reference fixture was rejected\n' \
      >&2
    exit 1
  }
  printf '%s\n' 120 121 122 >"$reference_observed"
  if gate2_034_finalize_reference_result \
    "$reference_result" "$reference_baseline" "$reference_observed" \
    3 1 1000 "$(printf '0%.0s' {1..64})" \
    >"$reference_failure_log" 2>&1; then
    printf \
      'release-candidate-performance: failing reference fixture was accepted\n' \
      >&2
    exit 1
  fi
  if ! jq -e '
      def nr($values; $percentile):
        ($values | sort) as $sorted |
        (($sorted | length) * $percentile + 99) / 100 | floor as $rank |
        $sorted[$rank - 1];
      .elapsedOverhead.thresholdPassed == false and
      .elapsedOverhead.threshold == 10 and
      (.elapsedOverhead.samples | length) == 3 and
      .elapsedOverhead.median ==
        nr(.elapsedOverhead.samples; 50) and
      (.baseline.samples | length) == 3 and
      (.observed.samples | length) == 3
    ' "$reference_result" >/dev/null ||
    ! grep -Fq 'reference median overhead' "$reference_failure_log"; then
    printf \
      'release-candidate-performance: failing reference evidence was not retained\n' \
      >&2
    exit 1
  fi
  paired_baseline="$preflight_root/paired-baseline.txt"
  paired_observed="$preflight_root/paired-observed.txt"
  paired_result="$preflight_root/paired-result.json"
  printf '%s\n' 80 120 100 90 70 60 65 >"$paired_baseline"
  printf '%s\n' 90 87 95 97 76 71 92 >"$paired_observed"
  gate2_034_finalize_reference_result \
    "$paired_result" "$paired_baseline" "$paired_observed" \
    7 1 1000 "$(printf '0%.0s' {1..64})" >/dev/null
  jq -e '
    def nr($values; $percentile):
      ($values | sort) as $sorted |
      (($sorted | length) * $percentile + 99) / 100 | floor as $rank |
      $sorted[$rank - 1];
    .elapsedOverhead.thresholdPassed == true and
    .elapsedOverhead.median ==
      nr(.elapsedOverhead.samples; 50) and
    (
      (
        (.observed.median - .baseline.median) /
        .baseline.median
      ) * 100
    ) > 10
  ' "$paired_result" >/dev/null || {
    printf \
      'release-candidate-performance: paired A/B aggregation regressed\n' \
      >&2
    exit 1
  }
  nested_errexit_marker="$preflight_root/nested-errexit-continued"
  set +e
  bash -c '
    set -e
    nested_failure() {
      false
      : >"$1"
    }
    nested_failure "$1"
  ' release-performance-preflight "$nested_errexit_marker" \
    >"$preflight_root/nested-errexit.log" 2>&1
  nested_errexit_status=$?
  set -e
  if [ "$nested_errexit_status" -eq 0 ] ||
    [ -e "$nested_errexit_marker" ]; then
    printf \
      'release-candidate-performance: nested child failure was not fail-closed\n' \
      >&2
    exit 1
  fi
  bash -n \
    scripts/gates/release-candidate-performance.sh \
    scripts/gates/browser-console.sh \
    scripts/gates/workload-privacy-lima.sh \
    scripts/fixtures/workload-privacy.sh \
    scripts/lib/gate2-concurrent-performance.sh \
    scripts/lib/gate2-concurrent-sessions.sh
  go test -run '^$' \
    ./scripts/gates/performance-local \
    ./scripts/gates/performance-process \
    ./internal/tui/render \
    ./internal/workloadobs/query \
    ./internal/workloadobs/store >/dev/null
  scripts/gates/workload-privacy-lima.sh \
    --preflight --out "$preflight_root/privacy" >/dev/null
  gate_completed=1
  printf 'release-candidate-performance: preflight=passed\n'
  exit 0
fi

[ "$(uname -s)" = "Darwin" ] || {
  printf 'release-candidate-performance: full gate requires macOS\n' >&2
  exit 1
}
[ "$(uname -m)" = "arm64" ] || {
  printf 'release-candidate-performance: full gate requires arm64\n' >&2
  exit 1
}

if [ -L "$evidence_out" ]; then
  printf \
    'release-candidate-performance: evidence directory must not be a symlink\n' \
    >&2
  exit 1
fi
mkdir -p "$evidence_out"
evidence_out="$(cd "$evidence_out" && pwd -P)"
chmod 0700 "$evidence_out"

source_commit="$(git rev-parse HEAD)"
if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then
  source_dirty=true
else
  source_dirty=false
fi
run_id="run-$(date -u +'%Y%m%dT%H%M%SZ')-$$"
run_dir="$evidence_out/$run_id"
[ ! -e "$run_dir" ] || {
  printf \
    'release-candidate-performance: run directory already exists\n' \
    >&2
  exit 1
}
mkdir -p \
  "$run_dir/browser" \
  "$run_dir/lanes" \
  "$run_dir/tests"
chmod 0700 \
  "$run_dir" \
  "$run_dir/browser" \
  "$run_dir/lanes" \
  "$run_dir/tests"

scratch="$(mktemp -d /tmp/hideout-release-performance.XXXXXX)"
cleanup() {
  local exit_status=$?
  case "${scratch:-}" in
    /tmp/hideout-release-performance.*)
      [ ! -d "$scratch" ] || find "$scratch" -depth -delete
      ;;
    *)
      printf \
        'release-candidate-performance: refusing unexpected scratch cleanup\n' \
        >&2
      ;;
  esac
  if [ "$exit_status" -eq 0 ]; then
    gate_require_completion "release-candidate-performance"
  fi
}
trap cleanup EXIT

sha256_file() {
  shasum -a 256 "$1" | awk '{print $1}'
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
    "" | /* | .. | ../* | */.. | */../*) return 1 ;;
    *) return 0 ;;
  esac
}

percentile_file() {
  local values="$1" percentile="$2" count index
  count="$(wc -l <"$values" | tr -d ' ')"
  [ "$count" -gt 0 ] || return 1
  index=$(((count * percentile + 99) / 100))
  sort -n "$values" | sed -n "${index}p"
}

values_json() {
  jq -Rsc 'split("\n") | map(select(length > 0) | tonumber)' "$1"
}

write_source_manifest() {
  local destination="$1"
  local source_path mode bytes sha
  : >"$destination"
  git ls-files --cached --others --exclude-standard |
    LC_ALL=C sort |
    while IFS= read -r source_path; do
      case "$source_path" in
        .artifacts/* | .codegraph/* | hideout) continue ;;
      esac
      case "$source_path" in
        *"	"* | *"
"*)
          printf \
            'release-candidate-performance: unsupported source path: %q\n' \
            "$source_path" >&2
          return 1
          ;;
      esac
      [ -f "$source_path" ] && [ ! -L "$source_path" ] || {
        printf \
          'release-candidate-performance: source is not a regular file: %s\n' \
          "$source_path" >&2
        return 1
      }
      mode="$(file_mode "$source_path")"
      bytes="$(file_bytes "$source_path")"
      sha="$(sha256_file "$source_path")"
      printf '%s\t%s\t%s\t%s\n' \
        "$source_path" "$mode" "$bytes" "$sha"
    done >"$destination"
  [ -s "$destination" ]
}

source_manifest="$run_dir/source-manifest.tsv"
write_source_manifest "$source_manifest"
source_tree_sha="$(sha256_file "$source_manifest")"
source_file_count="$(wc -l <"$source_manifest" | tr -d ' ')"

candidate_dir="$scratch/candidate"
candidate_bin="$candidate_dir/hideout"
mkdir -p "$candidate_dir"
printf 'release-candidate-performance: stage=candidate-build\n'
go build -trimpath -o "$candidate_bin" ./cmd/hideout
candidate_sha="$(sha256_file "$candidate_bin")"
jq -n \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" \
  --arg treeSHA256 "$source_tree_sha" \
  --arg binarySHA256 "$candidate_sha" \
  --arg goVersion "$(go version)" \
  '{
    schema:"hideout.release-performance-candidate/v1",
    source:{commit:$commit,dirty:$dirty,treeSHA256:$treeSHA256},
    binary:{sha256:$binarySHA256,buildMode:"go-build-trimpath"},
    toolchain:$goVersion
  }' >"$run_dir/candidate.json"

printf 'release-candidate-performance: stage=local-query-render\n'
go run ./scripts/gates/performance-local \
  --out "$run_dir/lanes/local-query-render.json" \
  --samples "$local_samples" \
  --warmups "$local_warmups" \
  >"$run_dir/lanes/local-query-render.log" 2>&1
jq -e \
  --argjson samples "$local_samples" '
    def nr($values; $percentile):
      ($values | sort) as $sorted |
      (($sorted | length) * $percentile + 99) / 100 | floor as $rank |
      $sorted[$rank - 1];
    .schema == "hideout.release-performance-local/v1" and
    .result == "passed" and
    .methodology.samples == $samples and
    .methodology.percentile == "nearest-rank-ceiling" and
    .query.unit == "milliseconds" and
    .render.unit == "milliseconds" and
    (.query.samples | length) == $samples and
    (.render.samples | length) == $samples and
    .query.p50 == nr(.query.samples; 50) and
    .query.p95 == nr(.query.samples; 95) and
    .render.p50 == nr(.render.samples; 50) and
    .render.p95 == nr(.render.samples; 95) and
    .query.thresholdPassed == true and
    .render.thresholdPassed == true
  ' "$run_dir/lanes/local-query-render.json" >/dev/null

printf 'release-candidate-performance: stage=daemon-tui-process\n'
set +e
go run ./scripts/gates/performance-process \
  --hideout "$candidate_bin" \
  --store "$scratch/process-store" \
  --out "$run_dir/lanes/daemon-tui-process.json" \
  --samples "$process_samples" \
  >"$run_dir/lanes/daemon-tui-process.log" 2>&1
process_status=$?
set -e
if [ "$process_status" -ne 0 ]; then
  process_failure="$(
    sed -n '$p' "$run_dir/lanes/daemon-tui-process.log"
  )"
  [ -n "$process_failure" ] ||
    process_failure="no terminal reason was recorded"
  printf \
    'release-candidate-performance: daemon/TUI process failed: %s (status=%d log=%s)\n' \
    "$process_failure" "$process_status" \
    "$run_dir/lanes/daemon-tui-process.log" >&2
  exit 1
fi
jq -e \
  --argjson samples "$process_samples" '
    def nr($values; $percentile):
      ($values | sort) as $sorted |
      (($sorted | length) * $percentile + 99) / 100 | floor as $rank |
      $sorted[$rank - 1];
    .schema == "hideout.release-performance-process/v1" and
    .result == "passed" and
    .methodology.samples == $samples and
    .methodology.percentile == "nearest-rank-ceiling" and
    .daemonRSS.unit == "bytes" and
    .tuiRSS.unit == "bytes" and
    .tuiReady.unit == "milliseconds" and
    (.daemonRSS.samples | length) == $samples and
    (.tuiRSS.samples | length) == $samples and
    .daemonRSS.p50 == nr(.daemonRSS.samples; 50) and
    .daemonRSS.p95 == nr(.daemonRSS.samples; 95) and
    .tuiRSS.p50 == nr(.tuiRSS.samples; 50) and
    .tuiRSS.p95 == nr(.tuiRSS.samples; 95) and
    .daemonRSS.thresholdPassed == true and
    .tuiRSS.thresholdPassed == true and
    .tuiReady.thresholdPassed == true
  ' "$run_dir/lanes/daemon-tui-process.json" >/dev/null

store_expected='[
  "TestActiveSegmentRepairsTornTailAndReportsCoverageGap",
  "TestActiveSegmentCRCFailureTruncatesAfterLastValidFrame",
  "TestCorruptSealedSegmentIsQuarantinedAndNeverReturned",
  "TestOwnerRetentionMaxAgePrunesExpiredSealedHistory",
  "TestQuotaPrunesOldestSealedAcrossOwnersAndBoundsOvershoot",
  "TestReusableOwnerQueriesRemainInsideSelectedSession"
]'
store_expected_path="$run_dir/tests/store-recovery.expected.json"
store_observed_path="$run_dir/tests/store-recovery.observed.json"
store_log="$run_dir/tests/store-recovery.go-test.jsonl"
jq -S . <<<"$store_expected" >"$store_expected_path"
store_regex="$(
  jq -r 'map("^" + . + "$") | join("|")' "$store_expected_path"
)"
printf 'release-candidate-performance: stage=quota-recovery-tests\n'
set +e
go test -json -count=1 -run "$store_regex" \
  ./internal/workloadobs/query \
  ./internal/workloadobs/store >"$store_log" 2>&1
store_test_exit=$?
set -e
jq -s \
  --slurpfile expected "$store_expected_path" '
    [.[] |
      select(.Action == "pass" and
        ((.Test // "") as $test |
          ($expected[0] | index($test)) != null)) |
      .Test] | unique | sort
  ' "$store_log" >"$store_observed_path"
if [ "$store_test_exit" -ne 0 ] ||
  ! jq -e -n \
    --slurpfile expected "$store_expected_path" \
    --slurpfile observed "$store_observed_path" '
      ($expected[0] | sort) == ($observed[0] | sort)
    ' >/dev/null; then
  tail -40 "$store_log" >&2
  printf \
    'release-candidate-performance: exact quota/recovery suite failed\n' \
    >&2
  exit 1
fi
jq -n \
  --argjson tests "$store_expected" \
  --arg log "tests/store-recovery.go-test.jsonl" \
  --arg logSHA256 "$(sha256_file "$store_log")" \
  '{
    schema:"hideout.release-performance-test-suite/v1",
    result:"passed",
    exactPassSet:true,
    tests:$tests,
    log:{path:$log,sha256:$logSHA256}
  }' >"$run_dir/tests/store-recovery.result.json"

browser_values="$run_dir/lanes/browser-live-update-ms.txt"
: >"$browser_values"
browser_index=1
while [ "$browser_index" -le "$browser_samples" ]; do
  browser_out="$run_dir/browser/sample-$browser_index"
  browser_log="$run_dir/lanes/browser-$browser_index.log"
  printf \
    'release-candidate-performance: stage=browser sample=%d/%d\n' \
    "$browser_index" "$browser_samples"
  scripts/gates/browser-console.sh \
    --out "$browser_out" >"$browser_log" 2>&1
  browser_summary="$browser_out/summary.json"
  jq -e \
    --arg commit "$source_commit" \
    --argjson dirty "$source_dirty" '
      .schema == "hideout.browser-console-gate/v1" and
      .source == {commit:$commit,dirty:$dirty} and
      .result == "passed" and
      .journeys.authenticatedSSE == "passed" and
      .observed.performance.liveUpdateMs > 0
    ' "$browser_summary" >/dev/null
  jq -r '.observed.performance.liveUpdateMs' \
    "$browser_summary" >>"$browser_values"
  browser_index=$((browser_index + 1))
done
browser_p50="$(percentile_file "$browser_values" 50)"
browser_p95="$(percentile_file "$browser_values" 95)"
awk -v value="$browser_p95" 'BEGIN {exit !(value <= 2000)}' || {
  printf \
    'release-candidate-performance: browser live-update p95 %sms exceeds 2000ms\n' \
    "$browser_p95" >&2
  exit 1
}
jq -n \
  --argjson samples "$(values_json "$browser_values")" \
  --argjson p50 "$browser_p50" \
  --argjson p95 "$browser_p95" \
  --argjson sampleCount "$browser_samples" \
  '{
    schema:"hideout.release-performance-browser/v1",
    result:"passed",
    methodology:{
      sampleCount:$sampleCount,
      journey:"independent-real-Chrome-SSE-visible-update",
      percentile:"nearest-rank-ceiling"
    },
    liveUpdate:{
      unit:"milliseconds",
      samples:$samples,
      p50:$p50,
      p95:$p95,
      thresholdP95:2000,
      thresholdPassed:($p95 <= 2000)
    }
  }' >"$run_dir/lanes/browser-performance.json"

concurrent_dir="$run_dir/lima-concurrent"
concurrent_log="$run_dir/lanes/lima-concurrent.log"
printf \
  'release-candidate-performance: stage=real-lima-attach-reference samples=%d warmups=%d\n' \
  "$attach_samples" "$attach_warmups"
set +e
bash -c '
  set -euo pipefail
  repo_root="$1"
  concurrent_dir="$2"
  attach_samples="$3"
  attach_warmups="$4"
  # shellcheck source=scripts/lib/gate2-concurrent-sessions.sh
  . "$repo_root/scripts/lib/gate2-concurrent-sessions.sh"
  # shellcheck source=scripts/lib/gate2-concurrent-performance.sh
  . "$repo_root/scripts/lib/gate2-concurrent-performance.sh"
  unset HIDEOUT_RELEASE_BINARY
  export HIDEOUT_034_EXTENDED_PERFORMANCE=1
  gate2_concurrent_sessions_run \
    "$repo_root" "$concurrent_dir" \
    "$attach_samples" "$attach_warmups"
' hideout-performance-child \
  "$repo_root" "$concurrent_dir" "$attach_samples" "$attach_warmups" \
  >"$concurrent_log" 2>&1
concurrent_status=$?
set -e
if [ "$concurrent_status" -ne 0 ]; then
  concurrent_failure="$(sed -n '$p' "$concurrent_log")"
  [ -n "$concurrent_failure" ] ||
    concurrent_failure="no terminal reason was recorded"
  printf \
    'release-candidate-performance: real-Lima attach/reference failed: %s (log=%s)\n' \
    "$concurrent_failure" "$concurrent_log" >&2
  exit 1
fi
concurrent_result="$concurrent_dir/result.json"
concurrent_performance="$concurrent_dir/logs/performance.json"
jq -e \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" \
  --argjson samples "$attach_samples" \
  --argjson warmups "$attach_warmups" '
    def nr($values; $percentile):
      ($values | sort) as $sorted |
      (($sorted | length) * $percentile + 99) / 100 | floor as $rank |
      $sorted[$rank - 1];
    .schema == "hideout.concurrent-sessions-performance/v3" and
    .status == "passed" and
    .candidate.commit == $commit and
    .candidate.dirty == $dirty and
    .methodology.samples == $samples and
    .methodology.warmups == $warmups and
    (.warmAttach.samplesMs | length) == $samples and
    .warmAttach.medianMs == nr(.warmAttach.samplesMs; 50) and
    .warmAttach.p95Ms == nr(.warmAttach.samplesMs; 95) and
    .warmAttach.p95Ms <= .methodology.readyThresholdMs and
    (.referenceWorkload.baseline.samples | length) == $samples and
    (.referenceWorkload.observed.samples | length) == $samples and
    .referenceWorkload.baseline.median ==
      nr(.referenceWorkload.baseline.samples; 50) and
    .referenceWorkload.baseline.p95 ==
      nr(.referenceWorkload.baseline.samples; 95) and
    .referenceWorkload.observed.median ==
      nr(.referenceWorkload.observed.samples; 50) and
    .referenceWorkload.observed.p95 ==
      nr(.referenceWorkload.observed.samples; 95) and
    .referenceWorkload.methodology.samplePairing ==
      "index-aligned-adjacent-counterbalanced" and
    .referenceWorkload.methodology.overheadAggregation ==
      "nearest-rank-median-of-paired-percent-deltas" and
    .referenceWorkload.elapsedOverhead.unit == "percent" and
    (.referenceWorkload.elapsedOverhead.samples | length) == $samples and
    .referenceWorkload.elapsedOverhead.samples == [
      range(0; $samples) as $index |
      (
        (
          (
            (
              .referenceWorkload.observed.samples[$index] -
              .referenceWorkload.baseline.samples[$index]
            ) /
            .referenceWorkload.baseline.samples[$index]
          ) * 100000
        ) | round
      ) / 1000
    ] and
    .referenceWorkload.elapsedOverhead.median ==
      nr(.referenceWorkload.elapsedOverhead.samples; 50) and
    .referenceWorkload.elapsedOverhead.threshold == 10 and
    .referenceWorkload.elapsedOverhead.thresholdPassed == true
  ' "$concurrent_performance" >/dev/null
jq -e \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" '
    .schema == "hideout.concurrent-sessions-gate2/v1" and
    .status == "passed" and
    .commit == $commit and
    .dirty == $dirty and
    .candidateAcceptance == ($dirty | not)
  ' "$concurrent_result" >/dev/null

privacy_dir="$run_dir/lima-privacy"
privacy_log="$run_dir/lanes/lima-privacy.log"
printf \
  'release-candidate-performance: stage=real-lima-observer-quota\n'
HIDEOUT_WORKLOAD_PRIVACY_MEASURE_PERFORMANCE=1 \
HIDEOUT_WORKLOAD_PRIVACY_EVENTS_PER_ROUND=7000 \
HIDEOUT_WORKLOAD_PRIVACY_MAXIMUM_ROUNDS=3 \
  scripts/gates/workload-privacy-lima.sh \
    --require-real --out "$privacy_dir" \
    >"$privacy_log" 2>&1
privacy_result="$privacy_dir/result.json"
privacy_summary="$privacy_dir/reports/privacy-summary.json"
jq -e \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" '
    .schema == "hideout.workload-privacy-lima-evidence/v1" and
    .source == {commit:$commit,dirty:$dirty} and
    .result == "passed" and
    .candidateAcceptance == ($dirty | not) and
    (.artifacts | length) == 10 and
    all(.checks[]; . == "passed")
  ' "$privacy_result" >/dev/null
jq -e '
    def nr($values; $percentile):
      ($values | sort) as $sorted |
      (($sorted | length) * $percentile + 99) / 100 | floor as $rank |
      $sorted[$rank - 1];
    .schema == "hideout.workload-privacy-lima-summary/v1" and
    .quota.passed == true and
    .quota.pruned == true and
    .quota.retentionGap == true and
    .quota.activeSegmentAllowanceCount == 1 and
    .quota.activeSegmentAllowanceBytes == 8388608 and
    .quota.usedBytes <=
      (.quota.limitBytes + .quota.activeSegmentAllowanceBytes) and
    .performance.measured == true and
    .performance.methodology.percentile == "nearest-rank-ceiling" and
    .performance.observerCPU.unit == "percent-of-one-guest-vcpu" and
    .performance.observerRSS.unit == "bytes" and
    (.performance.observerCPU.samples | length) >= 5 and
    (.performance.observerRSS.samples | length) ==
      (.performance.observerCPU.samples | length) and
    .performance.observerCPU.p50 ==
      nr(.performance.observerCPU.samples; 50) and
    .performance.observerCPU.p95 ==
      nr(.performance.observerCPU.samples; 95) and
    .performance.observerRSS.p50 ==
      nr(.performance.observerRSS.samples; 50) and
    .performance.observerRSS.p95 ==
      nr(.performance.observerRSS.samples; 95) and
    .performance.observerCPU.p95 <= 200 and
    .performance.observerRSS.p95 <= 268435456 and
    .performance.eventRate.unit == "generated-execs-per-second" and
    .performance.eventRate.generatedEvents >= 7000 and
    .performance.eventRate.value >= 100 and
    .performance.healthyDropRate.unit == "percent" and
    .performance.healthyDropRate.coverageAccounted == true and
    .performance.healthyDropRate.value <= 1
  ' "$privacy_summary" >/dev/null

receipts="$run_dir/claim-receipts.json"
jq -n '[
  {
    claim:"C05",
    passed:true,
    requirements:{
      queryOwnerStable:true,
      pruneOrder:"oldest-sealed",
      foreignOwnerPruned:false,
      historyCompleteAfterPrune:false,
      quotaOvershootActiveSegments:1
    },
    evidence:[
      "tests/store-recovery.result.json",
      "lima-privacy/reports/privacy-summary.json"
    ]
  },
  {
    claim:"C06",
    passed:true,
    requirements:{
      rawSamplesPresent:true,
      percentilesRecomputed:true,
      unitsStable:true,
      thresholdsPassed:true,
      exactCandidateBound:true
    },
    evidence:[
      "source-manifest.tsv",
      "lanes/local-query-render.json",
      "lanes/daemon-tui-process.json",
      "lanes/browser-performance.json",
      "lima-concurrent/logs/performance.json",
      "lima-privacy/reports/privacy-summary.json"
    ]
  },
  {
    claim:"CL03",
    passed:true,
    requirements:{
      corruptFramesReturned:0,
      pruneOrder:"oldest-sealed",
      coverageGapVisible:true,
      quarantinePresent:true
    },
    evidence:[
      "tests/store-recovery.result.json",
      "lima-privacy/reports/privacy-summary.json"
    ]
  }
]' >"$receipts"
if ! jq -e -n \
  --slurpfile contracts scripts/mutation/045/contracts.json \
  --slurpfile receipts "$receipts" \
  --argjson expected "$expected_contract_claims" '
    ($contracts[0].claims |
      map(select((.judges // []) |
        index("release-candidate-performance")))) as $claims |
    ($receipts[0]) as $actual |
    ($claims | map(.id) | sort) == ($expected | sort) and
    ($actual | map(.claim) | sort) == ($expected | sort) and
    all($claims[]; . as $claim |
      ($actual[] | select(.claim == $claim.id)) as $receipt |
      $receipt.passed == true and
      all($claim.requirements[];
        $receipt.requirements[.id] == .expected))
  ' >/dev/null; then
  printf \
    'release-candidate-performance: claim receipt contract mismatch\n' \
    >&2
  exit 1
fi

source_manifest_after="$scratch/source-manifest-after.tsv"
write_source_manifest "$source_manifest_after"
if ! cmp -s "$source_manifest" "$source_manifest_after"; then
  printf \
    'release-candidate-performance: source tree changed during measurement\n' \
    >&2
  exit 1
fi

find "$run_dir" -type d -exec chmod 0700 {} +
find "$run_dir" -type f -exec chmod 0600 {} +
if find "$run_dir" -type l -print -quit | grep -q .; then
  printf \
    'release-candidate-performance: evidence contains a symlink\n' \
    >&2
  exit 1
fi
if find "$run_dir" -type f ! -name '*.png' -print0 |
  xargs -0 rg -a -n \
    'ui_[0-9a-fA-F]{48}|cap_[0-9a-fA-F]{32,}|HIDEOUT_SECRET_[A-Za-z0-9_]+=[^[:space:]]+|((https?|socks5h?)://[^/@[:space:]:]+:[^/@[:space:]]+@)|Authorization:[[:space:]]*Bearer[[:space:]]+[A-Za-z0-9._~-]{8,}' \
    >/dev/null 2>&1; then
  printf \
    'release-candidate-performance: private material reached retained evidence\n' \
    >&2
  exit 1
fi

artifact_lines="$scratch/artifacts.jsonl"
: >"$artifact_lines"
while IFS= read -r evidence_file; do
  relative_path="${evidence_file#"$run_dir"/}"
  safe_relative_path "$relative_path" || {
    printf \
      'release-candidate-performance: unsafe evidence path: %s\n' \
      "$relative_path" >&2
    exit 1
  }
  [ "$(file_mode "$evidence_file")" = "600" ] || {
    printf \
      'release-candidate-performance: evidence mode is not 0600: %s\n' \
      "$relative_path" >&2
    exit 1
  }
  jq -n -c \
    --arg path "$relative_path" \
    --arg sha256 "$(sha256_file "$evidence_file")" \
    --argjson bytes "$(file_bytes "$evidence_file")" \
    '{
      path:$path,
      sha256:$sha256,
      bytes:$bytes,
      mode:"0600"
    }' >>"$artifact_lines"
done < <(find "$run_dir" -type f | LC_ALL=C sort)
artifacts="$scratch/artifacts.json"
jq -s . "$artifact_lines" >"$artifacts"
artifact_count="$(jq 'length' "$artifacts")"

summary="$run_dir/summary.json"
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" \
  --arg treeSHA256 "$source_tree_sha" \
  --arg manifest "source-manifest.tsv" \
  --argjson sourceFiles "$source_file_count" \
  --arg binarySHA256 "$candidate_sha" \
  --argjson local "$(cat "$run_dir/lanes/local-query-render.json")" \
  --argjson process "$(cat "$run_dir/lanes/daemon-tui-process.json")" \
  --argjson browser "$(cat "$run_dir/lanes/browser-performance.json")" \
  --argjson concurrent "$(cat "$concurrent_performance")" \
  --argjson privacy "$(cat "$privacy_summary")" \
  --argjson tests "$(cat "$run_dir/tests/store-recovery.result.json")" \
  --argjson receipts "$(cat "$receipts")" \
  --argjson artifacts "$(cat "$artifacts")" \
  '{
    schema:"hideout.release-candidate-performance/v1",
    generatedAt:$generatedAt,
    result:"passed",
    source:{
      commit:$commit,
      dirty:$dirty,
      treeSHA256:$treeSHA256,
      manifest:$manifest,
      files:$sourceFiles,
      stableAcrossRun:true
    },
    candidate:{
      binarySHA256:$binarySHA256,
      exactSourceTreeBound:true,
      acceptance:($dirty | not)
    },
    methodology:{
      rawSamplesPresent:true,
      percentile:"nearest-rank-ceiling",
      percentilesIndependentlyRecomputed:true,
      unitsStable:true
    },
    metrics:{
      query:$local.query,
      render:$local.render,
      daemonRSS:$process.daemonRSS,
      tuiRSS:$process.tuiRSS,
      tuiReady:$process.tuiReady,
      browserFreshness:$browser.liveUpdate,
      warmAttach:$concurrent.warmAttach,
      referenceWorkload:$concurrent.referenceWorkload,
      observerCPU:$privacy.performance.observerCPU,
      observerRSS:$privacy.performance.observerRSS,
      eventRate:$privacy.performance.eventRate,
      healthyDropRate:$privacy.performance.healthyDropRate,
      quota:$privacy.quota
    },
    recoveryAndQuotaTests:$tests,
    claimReceipts:$receipts,
    validation:{
      localThresholds:true,
      processThresholds:true,
      browserFreshnessP95WithinTwoSeconds:true,
      warmAttachP95WithinTwoSeconds:true,
      referenceMedianOverheadWithinTenPercent:true,
      observerCPUAndRSSWithinBudgets:true,
      healthyDropRateWithinOnePercentAndAccounted:true,
      quotaWithinOneActiveSegment:true,
      exactRecoveryPassSet:true,
      sourceStableAcrossRun:true,
      contractReceiptsExact:true
    },
    artifacts:$artifacts,
    limitations:
      (if $dirty then
        [
          "This is exact dirty-source performance evidence; release-candidate acceptance remains false until the clean installed candidate is rerun."
        ]
      else
        []
      end)
  }' >"$summary"
chmod 0600 "$summary"

if ! validate_summary "$summary" "$source_tree_sha" "$artifact_count"; then
  printf \
    'release-candidate-performance: summary semantic validation failed\n' \
    >&2
  exit 1
fi

summary_relative="$run_id/summary.json"
summary_sha="$(sha256_file "$summary")"
pointer_tmp="$evidence_out/.result.$$.json"
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" \
  --arg treeSHA256 "$source_tree_sha" \
  --arg run "$run_id" \
  --arg summary "$summary_relative" \
  --arg summarySHA256 "$summary_sha" \
  '{
    schema:"hideout.release-candidate-performance-pointer/v1",
    generatedAt:$generatedAt,
    source:{commit:$commit,dirty:$dirty,treeSHA256:$treeSHA256},
    result:"passed",
    run:$run,
    summary:$summary,
    summarySHA256:$summarySHA256,
    candidateAcceptance:($dirty | not)
  }' >"$pointer_tmp"
chmod 0600 "$pointer_tmp"
mv "$pointer_tmp" "$evidence_out/result.json"

gate_completed=1
printf \
  'release-candidate-performance: passed evidence=%s summary-sha256=%s artifacts=%s\n' \
  "$summary" "$summary_sha" "$artifact_count"
