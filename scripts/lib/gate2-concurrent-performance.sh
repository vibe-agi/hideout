#!/usr/bin/env bash

gate2_034_now_seconds() {
  perl -MTime::HiRes=clock_gettime,CLOCK_MONOTONIC \
    -e 'printf "%.9f\n", clock_gettime(CLOCK_MONOTONIC)'
}

gate2_034_percentile() {
  local values="$1" percentile="$2" count index
  count="$(wc -l <"$values" | tr -d ' ')"
  [ "$count" -gt 0 ] || return 1
  index=$(((count * percentile + 99) / 100))
  sort -n "$values" | sed -n "${index}p"
}

gate2_034_median_upper_confidence_rank() {
  local samples="$1"

  awk -v samples="$samples" '
    function combination(n, k, result, position) {
      if (k > n - k) k = n - k
      result = 1
      for (position = 1; position <= k; position++)
        result = result * (n - k + position) / position
      return result
    }
    BEGIN {
      if (samples < 1 || samples > 1000) exit 1
      denominator = 2 ^ samples
      for (rank = 1; rank <= samples; rank++) {
        upper_tail = 0
        for (count = rank; count <= samples; count++)
          upper_tail += combination(samples, count) / denominator
        if (upper_tail <= 0.05 + 0.000000000001) {
          print rank
          exit 0
        }
      }
      exit 1
    }
  '
}

gate2_034_values_json() {
  jq -Rsc 'split("\n") | map(select(length > 0) | tonumber)' "$1"
}

gate2_034_extract_reference_coverage_sample() {
  local coverage_path="$1" sample_index="$2" recorded="$3"

  jq -c \
    --argjson sampleIndex "$sample_index" \
    --argjson recorded "$recorded" '
      ([.intervals[], .current[]] | unique_by(.id)) as $coverage |
      ([$coverage[].sessionId] | unique) as $sessions |
      def terminal_counter($code):
        ([
          $coverage[] |
          select(.reason == "target-exited") |
          .evidence[]? |
          select(.code == $code) |
          .value
        ] | unique) as $values |
        if ($values | length) != 1 or
            ($values[0] | type) != "string" or
            (($values[0] | test("^(0|[1-9][0-9]*)$")) | not)
        then error("missing or divergent terminal counter: " + $code)
        else ($values[0] | tonumber)
        end;
      if ($sessions | length) != 1 then
        error("reference coverage session identity diverged")
      else
        {
          sampleIndex: $sampleIndex,
          recorded: ($recorded == 1),
          sessionId: $sessions[0],
          droppedEventCount:
            ([$coverage[].droppedEventCount] | max // 0),
          ringOverflow: (any($coverage[]; .reason == "ring-overflow")),
          kernelDropped: terminal_counter("kernel-dropped"),
          ringDropped: terminal_counter("ring-dropped"),
          localDropped: {
            process: terminal_counter("local-process-dropped"),
            file: terminal_counter("local-file-dropped"),
            network: terminal_counter("local-network-dropped"),
            dns: terminal_counter("local-dns-dropped")
          },
          fileCollectorCounters: {
            matchedEvents: terminal_counter("file-matched-events"),
            reservedEvents: terminal_counter("file-reserved-events"),
            ringbufDrops: terminal_counter("file-ringbuf-drops"),
            stateDrops: terminal_counter("file-state-drops"),
            stateDegradations:
              terminal_counter("file-state-degradations"),
            pathFailures: terminal_counter("file-path-failures"),
            identityFailures: terminal_counter("file-identity-failures")
          }
        }
      end
    ' "$coverage_path"
}

gate2_034_finalize_reference_result() {
  local output="$1" baseline_values="$2" observed_values="$3"
  local samples="$4" warmups="$5" reference_uid="$6"
  local reference_digest="$7" coverage_values="$8" resource_values="$9"
  local bpf_object_sha="${10}"
  local baseline_median baseline_p95 observed_median observed_p95
  local baseline_samples_json observed_samples_json
  local overhead_samples_json overhead_percent coverage_samples_json
  local resource_samples_json confidence_json confidence_rank confidence_upper

  baseline_samples_json="$(gate2_034_values_json "$baseline_values")"
  observed_samples_json="$(gate2_034_values_json "$observed_values")"
  baseline_median="$(gate2_034_percentile "$baseline_values" 50)"
  baseline_p95="$(gate2_034_percentile "$baseline_values" 95)"
  observed_median="$(gate2_034_percentile "$observed_values" 50)"
  observed_p95="$(gate2_034_percentile "$observed_values" 95)"
  coverage_samples_json="$(jq -s '.' "$coverage_values")"
  resource_samples_json="$(jq -s '.' "$resource_values")"
  jq -e \
    --argjson samples "$samples" \
    --argjson warmups "$warmups" '
      length == ($samples + $warmups) and
      ([.[].sampleIndex] == [range(1; $samples + $warmups + 1)]) and
      ([.[] | select(.recorded)] | length) == $samples and
      ([.[].sessionId] | unique | length) == length and
      all(.[];
        .droppedEventCount == 0 and .ringOverflow == false and
        .kernelDropped == 0 and .ringDropped == 0 and
        .localDropped.process == 0 and
        .localDropped.file == 0 and
        .localDropped.network == 0 and
        .localDropped.dns == 0 and
        .fileCollectorCounters.matchedEvents > 0 and
        .fileCollectorCounters.matchedEvents ==
          (.fileCollectorCounters.reservedEvents +
            .fileCollectorCounters.ringbufDrops) and
        .fileCollectorCounters.ringbufDrops == 0 and
        .fileCollectorCounters.stateDrops == 0 and
        .fileCollectorCounters.stateDegradations >= 0 and
        .fileCollectorCounters.pathFailures >= 0 and
        .fileCollectorCounters.identityFailures >= 0 and
        (.sessionId | test("^ses_[A-Za-z0-9_-]+$")))
    ' <<<"$coverage_samples_json" >/dev/null || {
    echo "concurrent-sessions performance: reference coverage samples are invalid" >&2
    return 1
  }
  jq -e \
    --argjson samples "$samples" \
    --argjson warmups "$warmups" '
      length == ($samples + $warmups) and
      ([.[].sampleIndex] == [range(1; $samples + $warmups + 1)]) and
      ([.[] | select(.recorded)] | length) == $samples and
      all(.[];
        (.sampleIndex | type) == "number" and
        .sampleIndex == (.sampleIndex | floor) and
        (.recorded | type) == "boolean" and
        all([.baseline, .observed][];
          (.userMs | type) == "number" and .userMs >= 0 and
          (.systemMs | type) == "number" and .systemMs >= 0 and
          (.voluntaryContextSwitches | type) == "number" and
          .voluntaryContextSwitches >= 0 and
          .voluntaryContextSwitches ==
            (.voluntaryContextSwitches | floor) and
          (.involuntaryContextSwitches | type) == "number" and
          .involuntaryContextSwitches >= 0 and
          .involuntaryContextSwitches ==
            (.involuntaryContextSwitches | floor)))
    ' <<<"$resource_samples_json" >/dev/null || {
    echo "concurrent-sessions performance: reference resource samples are invalid" >&2
    return 1
  }
  if [ "${#bpf_object_sha}" -ne 64 ]; then
    echo "concurrent-sessions performance: invalid file observer object digest" >&2
    return 1
  fi
  case "$bpf_object_sha" in
    *[!0-9a-f]*)
      echo "concurrent-sessions performance: invalid file observer object digest" >&2
      return 1
      ;;
  esac
  overhead_samples_json="$(
    jq -cn \
      --argjson baseline "$baseline_samples_json" \
      --argjson observed "$observed_samples_json" \
      --argjson expected "$samples" '
      if $expected <= 0 or
          ($baseline | length) != $expected or
          ($observed | length) != $expected or
          any($baseline[]; . <= 0) or
          any($observed[]; . <= 0)
      then
        error("reference workload samples are incomplete or invalid")
      else
        [
          range(0; $expected) as $index |
          (
            (
              (
                ($observed[$index] - $baseline[$index]) /
                $baseline[$index]
              ) * 100000
            ) | round
          ) / 1000
        ]
      end
    '
  )"
  overhead_percent="$(
    printf '%s\n' "$overhead_samples_json" |
      jq -r '
        sort |
        .[((length * 50 + 99) / 100 | floor) - 1]
      '
  )"
  confidence_json='null'
  confidence_rank=''
  confidence_upper=''
  if [ "$samples" -ge 30 ]; then
    confidence_rank="$(gate2_034_median_upper_confidence_rank "$samples")" || {
      echo "concurrent-sessions performance: cannot derive median confidence rank" >&2
      return 1
    }
    confidence_upper="$(
      jq -nr \
        --argjson values "$overhead_samples_json" \
        --argjson rank "$confidence_rank" \
        '$values | sort | .[$rank - 1]'
    )"
    confidence_json="$(
      jq -cn \
        --argjson rank "$confidence_rank" \
        --argjson upperBound "$confidence_upper" '
          {
            level: 0.95,
            method: "one-sided-exact-binomial-order-statistic",
            rank: $rank,
            upperBound: $upperBound,
            thresholdPassed: ($upperBound <= 10)
          }
        '
    )"
  fi
  jq -n \
    --arg unit "milliseconds" \
    --arg clock "guest-python-time.monotonic_ns" \
    --arg order "alternating-baseline-observed" \
    --arg pairing "index-aligned-adjacent-counterbalanced" \
    --arg overheadAggregation \
      "nearest-rank-median-of-paired-percent-deltas" \
    --arg fixturePreparation \
      "once-via-control-before-all-warmup-and-recorded-samples" \
    --arg pairProximity \
      "adjacent-halves-reuse-one-immutable-warmed-source-with-no-drain-sleep" \
    --arg backgroundObserverPolicy \
      "concurrent-anchor-plus-arm-equivalent-inert-baseline-session" \
    --arg percentile "nearest-rank-ceiling" \
    --arg uid "$reference_uid" \
    --arg digest "$reference_digest" \
    --arg bpfObjectSHA256 "$bpf_object_sha" \
    --argjson samples "$samples" \
    --argjson warmups "$warmups" \
    --argjson baselineSamples "$baseline_samples_json" \
    --argjson observedSamples "$observed_samples_json" \
    --argjson overheadSamples "$overhead_samples_json" \
    --argjson baselineMedian "$baseline_median" \
    --argjson baselineP95 "$baseline_p95" \
    --argjson observedMedian "$observed_median" \
    --argjson observedP95 "$observed_p95" \
    --argjson overhead "$overhead_percent" \
    --argjson confidence "$confidence_json" \
    --argjson coverageSamples "$coverage_samples_json" \
    --argjson resourceSamples "$resource_samples_json" \
    '{
      methodology: {
        workload:
          "single Python process parses 288MiB of source payload across 96 files, performs four in-memory SHA-256 passes per record, and writes bounded derived metadata",
        samples: $samples,
        warmups: $warmups,
        sampleOrder: $order,
        samplePairing: $pairing,
        overheadAggregation: $overheadAggregation,
        fixturePreparation: $fixturePreparation,
        pairProximity: $pairProximity,
        backgroundObserverPolicy: $backgroundObserverPolicy,
        clock: $clock,
        percentile: $percentile,
        uid: ($uid | tonumber),
        outputSHA256: $digest
      },
      baseline: {
        unit: $unit,
        samples: $baselineSamples,
        median: $baselineMedian,
        p95: $baselineP95
      },
      observed: {
        unit: $unit,
        samples: $observedSamples,
        median: $observedMedian,
        p95: $observedP95
      },
      observationIntegrity: {
        fileBPFObjectSHA256: $bpfObjectSHA256,
        coverageSamples: $coverageSamples,
        fileCollectorCounters: [
          $coverageSamples[] |
          {sampleIndex, recorded, sessionId} + .fileCollectorCounters
        ],
        noReportedLoss: all($coverageSamples[];
          .droppedEventCount == 0 and .ringOverflow == false and
          .kernelDropped == 0 and .ringDropped == 0 and
          .localDropped.process == 0 and
          .localDropped.file == 0 and
          .localDropped.network == 0 and
          .localDropped.dns == 0 and
          .fileCollectorCounters.matchedEvents > 0 and
          .fileCollectorCounters.matchedEvents ==
            (.fileCollectorCounters.reservedEvents +
              .fileCollectorCounters.ringbufDrops) and
          .fileCollectorCounters.ringbufDrops == 0 and
              .fileCollectorCounters.stateDrops == 0)
      },
      resourceUsage: {
        scope: "reference-workload-child-process",
        source: "getrusage(RUSAGE_CHILDREN)",
        cpuTimeUnit: "milliseconds",
        contextSwitchUnit: "count",
        acceptanceFilter: false,
        samples: $resourceSamples
      },
      elapsedOverhead: {
        unit: "percent",
        samples: $overheadSamples,
        median: $overhead,
        threshold: 10,
        confidence: $confidence,
        thresholdPassed:
          ($overhead <= 10 and
            ($confidence == null or $confidence.thresholdPassed))
      }
    }' >"$output"

  awk -v value="$overhead_percent" \
    'BEGIN {exit !(value <= 10.0)}' || {
    echo \
      "concurrent-sessions performance: reference median overhead ${overhead_percent}% exceeds 10%" \
      >&2
    return 1
  }
  if [ -n "$confidence_upper" ]; then
    awk -v value="$confidence_upper" \
      'BEGIN {exit !(value <= 10.0)}' || {
      echo \
        "concurrent-sessions performance: reference one-sided 95% median upper bound ${confidence_upper}% exceeds 10%" \
        >&2
      return 1
    }
  fi
}

gate2_034_reference_fixture_setup() {
  cat <<'EOF'
umask 077
work_root="$(mktemp -d /var/tmp/hideout-reference.XXXXXX)"
cleanup_failed_setup() {
  setup_status=$?
  trap - EXIT
  if [ "$setup_status" -eq 0 ]; then
    exit 0
  fi
  case "$work_root" in
    /var/tmp/hideout-reference.[[:alnum:]][[:alnum:]][[:alnum:]][[:alnum:]][[:alnum:]][[:alnum:]])
      find "$work_root" -depth -delete
      ;;
    *)
      printf 'reference fixture: refusing unexpected failed-setup cleanup path\n' >&2
      ;;
  esac
  exit "$setup_status"
}
trap cleanup_failed_setup EXIT
/usr/bin/python3 - "$work_root" <<'PY'
import json
import os
import sys

root = sys.argv[1]
source = os.path.join(root, "source")
os.mkdir(source)
payload = "x" * 32768
for index in range(96):
    path = os.path.join(source, f"unit-{index:03d}.json")
    with open(path, "w", encoding="utf-8") as handle:
        json.dump(
            {"index": index, "name": f"unit-{index:03d}", "payload": payload},
            handle,
            separators=(",", ":"),
            sort_keys=True,
        )
PY
cat >"$work_root/worker.py" <<'PY'
import hashlib
import json
import os
import sys

root = sys.argv[1]
source = os.path.join(root, "source")
paths = sorted(os.path.join(source, name) for name in os.listdir(source))
digest = hashlib.sha256()
total = 0
hash_passes = 4
for iteration in range(96):
    for path in paths:
        with open(path, "rb") as handle:
            data = handle.read()
        value = json.loads(data)
        total += value["index"]
        for _ in range(hash_passes):
            digest.update(hashlib.sha256(data).digest())
    with open(os.path.join(root, "derived.json"), "w", encoding="utf-8") as handle:
        json.dump(
            {"iteration": iteration, "total": total, "digest": digest.hexdigest()},
            handle,
            separators=(",", ":"),
            sort_keys=True,
        )
print(digest.hexdigest())
PY
printf 'root=%s\n' "$work_root"
EOF
}

gate2_034_reference_workload() {
  cat <<'EOF'
work_root="${1:-}"
case "$work_root" in
  /var/tmp/hideout-reference.[[:alnum:]][[:alnum:]][[:alnum:]][[:alnum:]][[:alnum:]][[:alnum:]]) ;;
  *)
    printf 'reference workload: refusing unexpected fixture path\n' >&2
    exit 1
    ;;
esac
[ -d "$work_root" ] && [ ! -L "$work_root" ] &&
  [ -d "$work_root/source" ] && [ ! -L "$work_root/source" ] &&
  [ -f "$work_root/worker.py" ] && [ ! -L "$work_root/worker.py" ] || {
  printf 'reference workload: fixture identity is invalid\n' >&2
  exit 1
}
/usr/bin/python3 - "$work_root" <<'PY'
import os
import resource
import subprocess
import sys
import time

root = sys.argv[1]
usage_before = resource.getrusage(resource.RUSAGE_CHILDREN)
started = time.monotonic_ns()
completed = subprocess.run(
    ["/usr/bin/python3", os.path.join(root, "worker.py"), root],
    check=True,
    stdout=subprocess.PIPE,
    stderr=None,
    text=True,
)
finished = time.monotonic_ns()
usage_after = resource.getrusage(resource.RUSAGE_CHILDREN)
elapsed_ms = (finished - started) / 1_000_000
user_ms = (usage_after.ru_utime - usage_before.ru_utime) * 1_000
system_ms = (usage_after.ru_stime - usage_before.ru_stime) * 1_000
digest = completed.stdout.strip()
if len(digest) != 64 or any(
    value not in "0123456789abcdef" for value in digest
):
    raise SystemExit("worker returned an invalid digest")
print(f"uid={os.getuid()}")
print(f"digest={digest}")
print(f"elapsed_ms={elapsed_ms:.3f}")
print(f"user_ms={user_ms:.3f}")
print(f"system_ms={system_ms:.3f}")
print(
    "voluntary_context_switches="
    f"{usage_after.ru_nvcsw - usage_before.ru_nvcsw}"
)
print(
    "involuntary_context_switches="
    f"{usage_after.ru_nivcsw - usage_before.ru_nivcsw}"
)
PY
if [ -n "${HIDEOUT_SESSION_ID:-}" ]; then
  printf 'session_id=%s\n' "$HIDEOUT_SESSION_ID"
fi
EOF
}

gate2_034_reference_result() {
  local output="$1" key="$2"
  awk -F= -v key="$key" '$1 == key {print $2; found=1} END {exit !found}' \
    "$output" | tr -d '\r' | tail -n1
}

gate2_034_wait_active_session_count() {
  local hideout="$1" store="$2" expected="$3" output="$4" errors="$5"
  local attempts=0

  while [ "$attempts" -lt 100 ]; do
    if HIDEOUT_STORE_ROOT="$store" "$hideout" env list \
      >"$output" 2>"$errors" &&
      awk -v expected="$expected" \
        'NR == 2 {
          exit !($7 == expected && $6 == "running" && $9 == "ready")
        }' \
        "$output"; then
      return 0
    fi
    sleep 0.05
    attempts=$((attempts + 1))
  done
  return 1
}

gate2_034_measure_reference_baseline() {
  local hideout="$1" store="$2" lima_home="$3" profile="$4"
  local workspace="$5" shim="$6" hostfsd="$7" instance="$8"
  local output="$9" errors="${10}" workload="${11}" fixture_root="${12}"
  local sample_index="${13}" marker guest_marker inert_output inert_errors
  local inert_pid attempts status=0

  marker="$workspace/.hideout-gate-control/reference-inert-$sample_index.ready"
  guest_marker="/workspace/.hideout-gate-control/reference-inert-$sample_index.ready"
  inert_output="$output.inert.out"
  inert_errors="$errors.inert.err"
  case "$marker" in
    */.hideout-gate-control/reference-inert-*.ready) ;;
    *)
      echo "concurrent-sessions performance: invalid inert baseline marker" >&2
      return 1
      ;;
  esac
  rm -f "$marker"
  HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
    HIDEOUT_LINUX_SHIM_PATH="$shim" \
    HIDEOUT_LINUX_HOSTFSD_PATH="$hostfsd" \
    "$hideout" run --profile "$profile" --backend lima --network direct \
      --workspace "$workspace" --guest-workspace /workspace -- \
      /usr/bin/python3 -c '
import pathlib
import signal
import sys
pathlib.Path(sys.argv[1]).touch()
signal.pause()
' "$guest_marker" >"$inert_output" 2>"$inert_errors" &
  inert_pid=$!
  attempts=0
  while [ ! -f "$marker" ]; do
    if ! kill -0 "$inert_pid" 2>/dev/null; then
      wait "$inert_pid" 2>/dev/null || true
      cat "$inert_output" "$inert_errors" >&2
      echo "concurrent-sessions performance: inert baseline session exited before ready" >&2
      return 1
    fi
    attempts=$((attempts + 1))
    if [ "$attempts" -ge 600 ]; then
      status=1
      break
    fi
    sleep 0.05
  done
  if [ "$status" -eq 0 ] &&
    ! gate2_034_wait_active_session_count \
      "$hideout" "$store" 2 \
      "$output.inert-env-before.out" "$errors.inert-env-before.err"; then
    status=1
  fi
  if [ "$status" -eq 0 ]; then
    LIMA_HOME="$lima_home" limactl shell --tty=false --workdir / \
      "$instance" -- sh -eu -c "$workload" reference-workload "$fixture_root" \
      >"$output" 2>"$errors" || status=$?
  fi
  kill "$inert_pid" 2>/dev/null || true
  wait "$inert_pid" 2>/dev/null || true
  if ! gate2_034_wait_active_session_count \
    "$hideout" "$store" 1 \
    "$output.inert-env-after.out" "$errors.inert-env-after.err"; then
    status=1
  fi
  rm -f "$marker"
  if [ "$status" -ne 0 ]; then
    cat "$inert_output" "$inert_errors" >&2
    echo "concurrent-sessions performance: inert baseline arm failed" >&2
    return "$status"
  fi
}

gate2_034_measure_reference_observed() {
  local hideout="$1" store="$2" lima_home="$3" profile="$4"
  local workspace="$5" shim="$6" hostfsd="$7"
  local output="$8" errors="$9" workload="${10}" fixture_root="${11}"
  local session_id coverage_output
  HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
    HIDEOUT_LINUX_SHIM_PATH="$shim" \
    HIDEOUT_LINUX_HOSTFSD_PATH="$hostfsd" \
    "$hideout" run --profile "$profile" --backend lima --network direct \
      --workspace "$workspace" --guest-workspace /workspace -- \
      sh -eu -c "$workload" reference-workload "$fixture_root" \
      >"$output" 2>"$errors"
  session_id="$(gate2_034_reference_result "$output" session_id)"
  case "$session_id" in
    ses_*) ;;
    *)
      echo "concurrent-sessions performance: invalid observed reference session" >&2
      return 1
      ;;
  esac
  case "$session_id" in
    *[!A-Za-z0-9_-]*)
      echo "concurrent-sessions performance: invalid observed reference session" >&2
      return 1
      ;;
  esac
  coverage_output="$output.coverage.json"
  HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
    "$hideout" activity coverage --session "$session_id" --json \
      >"$coverage_output" 2>>"$errors"
  jq -e '
    (.intervals | type) == "array" and
    (.current | type) == "array" and
    (([.intervals[], .current[]] | unique_by(.id)) as $coverage |
      ($coverage | length) >= 4 and
      all($coverage[];
        .droppedEventCount == 0 and .reason != "ring-overflow"))
  ' "$coverage_output" >/dev/null || {
    echo "concurrent-sessions performance: observed reference coverage reported loss" >&2
    return 1
  }
}

gate2_034_prepare_reference_fixture() {
  local lima_home="$1" instance="$2" output="$3" errors="$4"
  local setup
  setup="$(gate2_034_reference_fixture_setup)"
  LIMA_HOME="$lima_home" limactl shell --tty=false --workdir / \
    "$instance" -- sh -eu -c "$setup" \
    >"$output" 2>"$errors"
}

gate2_034_cleanup_reference_fixture() {
  local lima_home="$1" instance="$2" fixture_root="$3"
  local output="$4" errors="$5"
  # The single-quoted program expands positional parameters inside the guest.
  # shellcheck disable=SC2016
  LIMA_HOME="$lima_home" limactl shell --tty=false --workdir / \
    "$instance" -- sh -eu -c '
work_root="${1:-}"
case "$work_root" in
  /var/tmp/hideout-reference.[[:alnum:]][[:alnum:]][[:alnum:]][[:alnum:]][[:alnum:]][[:alnum:]]) ;;
  *)
    printf "reference fixture: refusing unexpected cleanup path\n" >&2
    exit 1
    ;;
esac
[ -d "$work_root" ] && [ ! -L "$work_root" ] || {
  printf "reference fixture: cleanup identity is invalid\n" >&2
  exit 1
}
find "$work_root" -depth -delete
' reference-cleanup "$fixture_root" >"$output" 2>"$errors"
}

gate2_034_run_reference_workload() {
  local out="$1" lima_home="$2" instance="$3"
  local hideout="$4" store="$5" profile="$6" workspace="$7"
  local shim="$8" hostfsd="$9" samples="${10}" warmups="${11}"
  local repo_root="${12}"
  local workload i record baseline_output baseline_errors
  local observed_output observed_errors baseline_ms observed_ms
  local baseline_uid observed_uid baseline_digest observed_digest
  local baseline_user_ms observed_user_ms baseline_system_ms observed_system_ms
  local baseline_voluntary observed_voluntary
  local baseline_involuntary observed_involuntary
  local reference_uid="" reference_digest=""
  local baseline_values observed_values coverage_values resource_values fixture_root
  local setup_output setup_errors cleanup_output cleanup_errors
  local bpf_object_sha

  workload="$(gate2_034_reference_workload)"
  baseline_values="$out/logs/performance-reference-baseline-ms.txt"
  observed_values="$out/logs/performance-reference-observed-ms.txt"
  coverage_values="$out/logs/performance-reference-coverage.jsonl"
  resource_values="$out/logs/performance-reference-resources.jsonl"
  setup_output="$out/logs/performance-reference-setup.out"
  setup_errors="$out/logs/performance-reference-setup.err"
  cleanup_output="$out/logs/performance-reference-cleanup.out"
  cleanup_errors="$out/logs/performance-reference-cleanup.err"
  : >"$baseline_values"
  : >"$observed_values"
  : >"$coverage_values"
  : >"$resource_values"
  bpf_object_sha="$(jq -r '.objectSHA256' \
    "$repo_root/internal/workloadobs/collector/bpf/file_observer.generated.json")"

  gate2_034_prepare_reference_fixture \
    "$lima_home" "$instance" "$setup_output" "$setup_errors"
  fixture_root="$(gate2_034_reference_result "$setup_output" root)"
  case "$fixture_root" in
    /var/tmp/hideout-reference.[[:alnum:]][[:alnum:]][[:alnum:]][[:alnum:]][[:alnum:]][[:alnum:]]) ;;
    *)
      cat "$setup_output" "$setup_errors" >&2
      echo "concurrent-sessions performance: invalid reference fixture root" >&2
      return 1
      ;;
  esac

  i=1
  while [ "$i" -le $((warmups + samples)) ]; do
    if [ "$i" -le "$warmups" ]; then record=0; else record=1; fi
    baseline_output="$out/logs/performance-reference-baseline-$i.out"
    baseline_errors="$out/logs/performance-reference-baseline-$i.err"
    observed_output="$out/logs/performance-reference-observed-$i.out"
    observed_errors="$out/logs/performance-reference-observed-$i.err"

    if [ $((i % 2)) -eq 1 ]; then
      gate2_034_measure_reference_baseline \
        "$hideout" "$store" "$lima_home" "$profile" "$workspace" \
        "$shim" "$hostfsd" "$instance" \
        "$baseline_output" "$baseline_errors" \
        "$workload" "$fixture_root" "$i"
      gate2_034_measure_reference_observed \
        "$hideout" "$store" "$lima_home" "$profile" "$workspace" \
        "$shim" "$hostfsd" "$observed_output" "$observed_errors" \
        "$workload" "$fixture_root"
    else
      gate2_034_measure_reference_observed \
        "$hideout" "$store" "$lima_home" "$profile" "$workspace" \
        "$shim" "$hostfsd" "$observed_output" "$observed_errors" \
        "$workload" "$fixture_root"
      gate2_034_measure_reference_baseline \
        "$hideout" "$store" "$lima_home" "$profile" "$workspace" \
        "$shim" "$hostfsd" "$instance" \
        "$baseline_output" "$baseline_errors" \
        "$workload" "$fixture_root" "$i"
    fi

    baseline_ms="$(gate2_034_reference_result "$baseline_output" elapsed_ms)"
    observed_ms="$(gate2_034_reference_result "$observed_output" elapsed_ms)"
    baseline_uid="$(gate2_034_reference_result "$baseline_output" uid)"
    observed_uid="$(gate2_034_reference_result "$observed_output" uid)"
    baseline_digest="$(gate2_034_reference_result "$baseline_output" digest)"
    observed_digest="$(gate2_034_reference_result "$observed_output" digest)"
    baseline_user_ms="$(gate2_034_reference_result "$baseline_output" user_ms)"
    observed_user_ms="$(gate2_034_reference_result "$observed_output" user_ms)"
    baseline_system_ms="$(gate2_034_reference_result "$baseline_output" system_ms)"
    observed_system_ms="$(gate2_034_reference_result "$observed_output" system_ms)"
    baseline_voluntary="$(gate2_034_reference_result \
      "$baseline_output" voluntary_context_switches)"
    observed_voluntary="$(gate2_034_reference_result \
      "$observed_output" voluntary_context_switches)"
    baseline_involuntary="$(gate2_034_reference_result \
      "$baseline_output" involuntary_context_switches)"
    observed_involuntary="$(gate2_034_reference_result \
      "$observed_output" involuntary_context_switches)"
    [ "$baseline_uid" = "$observed_uid" ] &&
      [ "$baseline_digest" = "$observed_digest" ] || {
      echo "concurrent-sessions performance: reference workload identity diverged" >&2
      return 1
    }
    if [ -n "$reference_uid" ] &&
      { [ "$baseline_uid" != "$reference_uid" ] ||
        [ "$baseline_digest" != "$reference_digest" ]; }; then
      echo "concurrent-sessions performance: reference fixture changed between pairs" >&2
      return 1
    fi
    case "$baseline_ms:$observed_ms:$baseline_uid:$baseline_digest" in
      *[!0-9.:a-f]* | :* | *:)
        echo "concurrent-sessions performance: invalid reference workload output" >&2
        return 1
        ;;
    esac
    reference_uid="$baseline_uid"
    reference_digest="$baseline_digest"
    if ! jq -cn \
      --arg sampleIndex "$i" \
      --arg recorded "$record" \
      --arg baselineUserMs "$baseline_user_ms" \
      --arg observedUserMs "$observed_user_ms" \
      --arg baselineSystemMs "$baseline_system_ms" \
      --arg observedSystemMs "$observed_system_ms" \
      --arg baselineVoluntary "$baseline_voluntary" \
      --arg observedVoluntary "$observed_voluntary" \
      --arg baselineInvoluntary "$baseline_involuntary" \
      --arg observedInvoluntary "$observed_involuntary" '
        ($sampleIndex | tonumber) as $index |
        ($baselineUserMs | tonumber) as $baselineUser |
        ($observedUserMs | tonumber) as $observedUser |
        ($baselineSystemMs | tonumber) as $baselineSystem |
        ($observedSystemMs | tonumber) as $observedSystem |
        ($baselineVoluntary | tonumber) as $baselineVoluntaryCount |
        ($observedVoluntary | tonumber) as $observedVoluntaryCount |
        ($baselineInvoluntary | tonumber) as $baselineInvoluntaryCount |
        ($observedInvoluntary | tonumber) as $observedInvoluntaryCount |
        if any([
          $baselineUser, $observedUser,
          $baselineSystem, $observedSystem,
          $baselineVoluntaryCount, $observedVoluntaryCount,
          $baselineInvoluntaryCount, $observedInvoluntaryCount
        ][]; . < 0) or
          any([
            $baselineVoluntaryCount, $observedVoluntaryCount,
            $baselineInvoluntaryCount, $observedInvoluntaryCount
          ][]; . != floor)
        then error("invalid reference resource sample")
        else {
          sampleIndex: $index,
          recorded: ($recorded == "1"),
          baseline: {
            userMs: $baselineUser,
            systemMs: $baselineSystem,
            voluntaryContextSwitches: $baselineVoluntaryCount,
            involuntaryContextSwitches: $baselineInvoluntaryCount
          },
          observed: {
            userMs: $observedUser,
            systemMs: $observedSystem,
            voluntaryContextSwitches: $observedVoluntaryCount,
            involuntaryContextSwitches: $observedInvoluntaryCount
          }
        } end
      ' >>"$resource_values"; then
      echo "concurrent-sessions performance: invalid reference resource output" >&2
      return 1
    fi
    gate2_034_extract_reference_coverage_sample \
      "$observed_output.coverage.json" "$i" "$record" \
      >>"$coverage_values"
    if [ "$record" = "1" ]; then
      printf '%s\n' "$baseline_ms" >>"$baseline_values"
      printf '%s\n' "$observed_ms" >>"$observed_values"
    fi
    i=$((i + 1))
  done

  if ! gate2_034_cleanup_reference_fixture \
    "$lima_home" "$instance" "$fixture_root" \
    "$cleanup_output" "$cleanup_errors"; then
    cat "$cleanup_output" "$cleanup_errors" >&2
    echo "concurrent-sessions performance: reference fixture cleanup failed" >&2
    return 1
  fi

  gate2_034_finalize_reference_result \
    "$out/logs/performance-reference.json" \
    "$baseline_values" "$observed_values" \
    "$samples" "$warmups" "$reference_uid" "$reference_digest" \
    "$coverage_values" "$resource_values" "$bpf_object_sha"
}

gate2_034_prepare_fixture() {
  local workspace="$1" i package
  case "$workspace" in
    */hideout-034-gate2.*/workspace) ;;
    *)
      echo "concurrent-sessions performance: refusing unexpected fixture path" >&2
      return 1
      ;;
  esac
  find "$workspace" -mindepth 1 -depth -delete
  mkdir -p "$workspace/src" "$workspace/node_modules"
	printf '.hideout-gate-control/\n' >"$workspace/.gitignore"
  i=1
  while [ "$i" -le 1200 ]; do
    printf 'export const value%04d = %d;\n' "$i" "$i" >"$workspace/src/file-$i.js"
    i=$((i + 1))
  done
  i=1
  while [ "$i" -le 600 ]; do
    package="$workspace/node_modules/pkg-$i"
    mkdir -p "$package"
    printf '{"name":"pkg-%04d","version":"1.0.0","main":"index.js"}\n' "$i" >"$package/package.json"
    printf 'module.exports = %d;\n' "$i" >"$package/index.js"
    i=$((i + 1))
  done
  git -C "$workspace" init -q
  git -C "$workspace" config user.name 'Hideout Gate 034'
  git -C "$workspace" config user.email 'gate034@hideout.invalid'
  git -C "$workspace" add .
  git -C "$workspace" commit -qm baseline
  printf '// modified by the 034 performance fixture\n' >>"$workspace/src/file-1.js"
}

gate2_034_fixture_digest() {
	local workspace="$1"
	(
		cd "$workspace" || exit
		{
			git rev-parse 'HEAD^{tree}'
			git diff --no-ext-diff --binary HEAD -- . ':(exclude).hideout-gate-control'
		} | shasum -a 256 | awk '{print $1}'
	)
}

gate2_034_measure_sample() {
  local hideout="$1" store="$2" lima_home="$3" profile="$4" workspace="$5"
  local shim="$6" hostfsd="$7" output="$8" errors="$9"
  local ready_values="${10}" record_ready="${11}"
  local start ready pid attempts=0
  start="$(gate2_034_now_seconds)"
  HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
    HIDEOUT_LINUX_SHIM_PATH="$shim" HIDEOUT_LINUX_HOSTFSD_PATH="$hostfsd" \
    "$hideout" run --profile "$profile" --backend lima --network direct \
      --workspace "$workspace" --guest-workspace /workspace -- \
      sh -eu -c 'printf "READY\n"' >"$output" 2>"$errors" &
  pid=$!
  while ! grep -q '^READY$' "$output" 2>/dev/null; do
    if ! kill -0 "$pid" 2>/dev/null; then
      wait "$pid" || true
      cat "$output" "$errors" >&2
      echo "concurrent-sessions performance: sample exited before READY" >&2
      return 1
    fi
    attempts=$((attempts + 1))
    if [ "$attempts" -ge 1200 ]; then
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
      echo "concurrent-sessions performance: timed out waiting for READY" >&2
      return 1
    fi
    sleep 0.01
  done
  ready="$(gate2_034_now_seconds)"
  if ! wait "$pid"; then
    cat "$output" "$errors" >&2
    return 1
  fi
  if [ "$record_ready" = "1" ]; then
    awk -v start="$start" -v ready="$ready" 'BEGIN { printf "%.3f\n", (ready-start)*1000 }' >>"$ready_values"
  fi
}

gate2_034_run_performance() {
  local root="$1" out="$2" lima_home="$3" workspace="$4"
  local candidate_hideout="$5" candidate_store="$6" candidate_profile="$7"
  local samples="$8" warmups="$9" arch="${10}"
  local candidate_bin
  local candidate_anchor_pid candidate_marker candidate_env candidate_instance
  local i record output errors owner_ready
  local ready_values ready_median ready_p95
  local runtime_family runtime_revision runtime_digest runtime_build_commit candidate_commit candidate_dirty
	local fixture_digest candidate_record control_dir
  local extended_performance=false reference_evidence='null'
  local host_quiet_confirmed=false

  if [ "${HIDEOUT_PERFORMANCE_QUIET_HOST_CONFIRMED:-0}" = "1" ]; then
    host_quiet_confirmed=true
  fi
  if [ "${HIDEOUT_034_EXTENDED_PERFORMANCE:-0}" = "1" ] &&
    [ "$host_quiet_confirmed" != "true" ]; then
    echo "concurrent-sessions performance: quiet host must be explicitly confirmed" >&2
    return 1
  fi

  candidate_bin="$(dirname "$candidate_hideout")"
	control_dir="$workspace/.hideout-gate-control"
  candidate_marker="$control_dir/candidate-anchor-ready"
  ready_values="$out/logs/performance-ready-ms.txt"
  : >"$ready_values"

  gate2_034_prepare_fixture "$workspace"
	mkdir -p "$control_dir"
	fixture_digest="$(gate2_034_fixture_digest "$workspace")"
	candidate_commit="$(git -C "$root" rev-parse HEAD)"

  HIDEOUT_STORE_ROOT="$candidate_store" LIMA_HOME="$lima_home" \
    HIDEOUT_LINUX_SHIM_PATH="$candidate_bin/hideout-shim-linux-$arch" \
    HIDEOUT_LINUX_HOSTFSD_PATH="$candidate_bin/hideout-hostfsd-linux-$arch" \
    "$candidate_hideout" run --profile "$candidate_profile" --backend lima --network direct \
      --workspace "$workspace" --guest-workspace /workspace -- sh -eu -c '
touch /workspace/.hideout-gate-control/candidate-anchor-ready
exec sleep infinity
' >"$out/logs/performance-anchor.out" 2>"$out/logs/performance-anchor.err" &
  candidate_anchor_pid=$!
  GATE2_034_CLEANUP_FIRST_PID="$candidate_anchor_pid"
  gate2_034_wait_file "$candidate_marker" "performance anchor"
  candidate_record="$(
    find "$candidate_store/environments" -name environment.json \
      -type f -print -quit
  )"
  [ -f "$candidate_record" ] || {
    echo "concurrent-sessions performance: environment runtime provenance is missing" >&2
    return 1
  }
  candidate_instance="$(jq -r '.instanceName' "$candidate_record")"

  i=1
  while [ "$i" -le $((warmups + samples)) ]; do
    if [ "$i" -le "$warmups" ]; then record=0; else record=1; fi
    output="$out/logs/performance-candidate-$i.out"
    errors="$out/logs/performance-candidate-$i.err"
    gate2_034_measure_sample "$candidate_hideout" "$candidate_store" "$lima_home" \
      "$candidate_profile" "$workspace" "$candidate_bin/hideout-shim-linux-$arch" \
      "$candidate_bin/hideout-hostfsd-linux-$arch" "$output" "$errors" \
      "$ready_values" "$record"
    i=$((i + 1))
  done
	[ "$(gate2_034_fixture_digest "$workspace")" = "$fixture_digest" ] || {
		echo "concurrent-sessions performance: candidate changed the tracked workspace fixture" >&2
		return 1
	}

  if [ "${HIDEOUT_034_EXTENDED_PERFORMANCE:-0}" = "1" ]; then
    gate2_034_run_reference_workload \
      "$out" "$lima_home" "$candidate_instance" \
      "$candidate_hideout" "$candidate_store" "$candidate_profile" \
      "$workspace" "$candidate_bin/hideout-shim-linux-$arch" \
      "$candidate_bin/hideout-hostfsd-linux-$arch" "$samples" "$warmups" \
      "$root"
    jq -e '.elapsedOverhead.thresholdPassed == true' \
      "$out/logs/performance-reference.json" >/dev/null ||
      return 1
    reference_evidence="$(cat "$out/logs/performance-reference.json")"
    extended_performance=true
  fi

  kill "$candidate_anchor_pid" 2>/dev/null || true
  wait "$candidate_anchor_pid" 2>/dev/null || true
  # Read by gate2_034_cleanup, which is defined in the sourcing script.
  # shellcheck disable=SC2034
  GATE2_034_CLEANUP_FIRST_PID=""
  candidate_env="$(HIDEOUT_STORE_ROOT="$candidate_store" "$candidate_hideout" env list | awk 'NR == 2 {print $1}')"
  owner_ready=0
  i=0
  while [ "$i" -lt 100 ]; do
    if HIDEOUT_STORE_ROOT="$candidate_store" "$candidate_hideout" env list \
      >"$out/logs/performance-env-ready.out" \
      2>"$out/logs/performance-env-ready.err" &&
      awk 'NR == 2 { exit !($7 == 0 && $6 == "ready") }' \
        "$out/logs/performance-env-ready.out"; then
      owner_ready=1
      break
    fi
    sleep 0.05
    i=$((i + 1))
  done
  if [ "$owner_ready" -ne 1 ]; then
    echo "concurrent-sessions performance: anchor owner did not reconcile before explicit stop" >&2
    return 1
  fi

  HIDEOUT_STORE_ROOT="$candidate_store" LIMA_HOME="$lima_home" \
    "$candidate_hideout" stop "$candidate_env" >/dev/null

  ready_median="$(gate2_034_percentile "$ready_values" 50)"
  ready_p95="$(gate2_034_percentile "$ready_values" 95)"

  awk -v value="$ready_p95" 'BEGIN { exit !(value <= 2000.0) }' || {
    echo "concurrent-sessions performance: ready p95 ${ready_p95}ms exceeds 2000ms" >&2
    return 1
  }
	runtime_family="$(jq -r '.runtime.family' "$candidate_record")"
	runtime_revision="$(jq -r '.runtime.revision' "$candidate_record")"
	runtime_digest="$(jq -r '.runtime.artifactSHA256' "$candidate_record")"
  runtime_build_commit="$(jq -r '.families[0].revisions[] | select(.id == $revision) | .artifacts[] | select(.hostOS == "darwin" and .hostArch == "arm64") | .source.buildCommit' --arg revision "$runtime_revision" "$root/internal/runtimecatalog/catalog.json")"
  candidate_dirty="$(cd "$root" && gate2_034_dirty)"

  jq -n \
    --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
    --arg candidateCommit "$candidate_commit" --argjson candidateDirty "$candidate_dirty" \
    --arg hostOS "darwin" --arg hostArch "arm64" \
    --arg runtimeFamily "$runtime_family" --arg runtimeRevision "$runtime_revision" \
    --arg runtimeArtifactSHA256 "$runtime_digest" --arg runtimeBuildCommit "$runtime_build_commit" \
    --arg candidateEnvironmentId "$(jq -r '.id' "$candidate_store/environments"/*/environment.json | head -n1)" \
    --arg candidateInstance "$candidate_instance" \
    --arg fixtureSHA256 "$fixture_digest" \
    --arg candidateSampling "per-run-host-invocation-to-first-target-byte" \
    --arg measurementClock "host-monotonic-observed-first-byte" \
    --arg hostContentionPolicy \
      "operator-confirmed-quiet-host-known-contention-invalidates-run" \
    --argjson hostQuietConfirmed "$host_quiet_confirmed" \
    --argjson samples "$samples" --argjson warmups "$warmups" \
    --argjson readySamples "$(gate2_034_values_json "$ready_values")" \
    --argjson readyMedian "$ready_median" --argjson readyP95 "$ready_p95" \
    --argjson extended "$extended_performance" \
    --argjson reference "$reference_evidence" \
    '{schema:
        (if $extended then
          "hideout.concurrent-sessions-performance/v4"
        else
          "hideout.concurrent-sessions-performance/v2"
        end),
      status:"passed",generatedAt:$generatedAt,
      candidate:{commit:$candidateCommit,dirty:$candidateDirty,environmentId:$candidateEnvironmentId,instance:$candidateInstance},
      host:{os:$hostOS,arch:$hostArch},
      runtime:{family:$runtimeFamily,revision:$runtimeRevision,artifactSHA256:$runtimeArtifactSHA256,buildCommit:$runtimeBuildCommit,buildDirty:false},
      methodology:({samples:$samples,warmups:$warmups,readyThresholdMs:2000,
        fixtureSHA256:$fixtureSHA256,candidateSampling:$candidateSampling,
        measurementClock:$measurementClock} +
        if $extended then {
          hostContentionPolicy:$hostContentionPolicy,
          hostQuietConfirmed:$hostQuietConfirmed
        } else {} end),
      warmAttach:{samplesMs:$readySamples,medianMs:$readyMedian,p95Ms:$readyP95}} +
      if $extended then {referenceWorkload:$reference} else {} end' \
    >"$out/logs/performance.json"
}
