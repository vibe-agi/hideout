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

gate2_034_values_json() {
  jq -Rsc 'split("\n") | map(select(length > 0) | tonumber)' "$1"
}

gate2_034_finalize_reference_result() {
  local output="$1" baseline_values="$2" observed_values="$3"
  local samples="$4" warmups="$5" reference_uid="$6"
  local reference_digest="$7"
  local baseline_median baseline_p95 observed_median observed_p95
  local overhead_percent

  baseline_median="$(gate2_034_percentile "$baseline_values" 50)"
  baseline_p95="$(gate2_034_percentile "$baseline_values" 95)"
  observed_median="$(gate2_034_percentile "$observed_values" 50)"
  observed_p95="$(gate2_034_percentile "$observed_values" 95)"
  overhead_percent="$(
    awk -v baseline="$baseline_median" -v observed="$observed_median" '
      BEGIN {
        if (baseline <= 0) exit 1
        printf "%.3f\n", ((observed-baseline)/baseline)*100
      }
    '
  )"
  jq -n \
    --arg unit "milliseconds" \
    --arg clock "guest-python-time.monotonic_ns" \
    --arg order "alternating-baseline-observed" \
    --arg percentile "nearest-rank-ceiling" \
    --arg uid "$reference_uid" \
    --arg digest "$reference_digest" \
    --argjson samples "$samples" \
    --argjson warmups "$warmups" \
    --argjson baselineSamples "$(gate2_034_values_json "$baseline_values")" \
    --argjson observedSamples "$(gate2_034_values_json "$observed_values")" \
    --argjson baselineMedian "$baseline_median" \
    --argjson baselineP95 "$baseline_p95" \
    --argjson observedMedian "$observed_median" \
    --argjson observedP95 "$observed_p95" \
    --argjson overhead "$overhead_percent" \
    '{
      methodology: {
        workload:
          "single Python process parses 288MiB of source payload across 96 files, performs four in-memory SHA-256 passes per record, and writes bounded derived metadata",
        samples: $samples,
        warmups: $warmups,
        sampleOrder: $order,
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
      elapsedOverhead: {
        unit: "percent",
        median: $overhead,
        threshold: 10,
        thresholdPassed: ($overhead <= 10)
      }
    }' >"$output"

  awk -v value="$overhead_percent" \
    'BEGIN {exit !(value <= 10.0)}' || {
    echo \
      "concurrent-sessions performance: reference median overhead ${overhead_percent}% exceeds 10%" \
      >&2
    return 1
  }
}

gate2_034_reference_workload() {
  cat <<'EOF'
work_root="$(mktemp -d /var/tmp/hideout-reference.XXXXXX)"
reference_completed=0
cleanup_reference() {
  cleanup_status=$?
  case "$work_root" in
    /var/tmp/hideout-reference.*)
      find "$work_root" -depth -delete
      ;;
    *)
      printf 'reference workload: refusing unexpected cleanup path\n' >&2
      ;;
  esac
  if [ "$cleanup_status" -eq 0 ] && [ "$reference_completed" != "1" ]; then
    printf 'reference workload: run ended before its success line\n' >&2
    exit 1
  fi
}
trap cleanup_reference EXIT HUP INT TERM
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
# Preparation is deliberately outside the measured interval. Let the observed
# pipeline drain it so the paired sample measures the reference workload, not
# fixture construction.
sleep 1
/usr/bin/python3 - "$work_root" <<'PY'
import os
import subprocess
import sys
import time

root = sys.argv[1]
started = time.monotonic_ns()
completed = subprocess.run(
    ["/usr/bin/python3", os.path.join(root, "worker.py"), root],
    check=True,
    stdout=subprocess.PIPE,
    stderr=None,
    text=True,
)
elapsed_ms = (time.monotonic_ns() - started) / 1_000_000
digest = completed.stdout.strip()
if len(digest) != 64 or any(
    value not in "0123456789abcdef" for value in digest
):
    raise SystemExit("worker returned an invalid digest")
print(f"uid={os.getuid()}")
print(f"digest={digest}")
print(f"elapsed_ms={elapsed_ms:.3f}")
PY
reference_completed=1
EOF
}

gate2_034_reference_result() {
  local output="$1" key="$2"
  awk -F= -v key="$key" '$1 == key {print $2; found=1} END {exit !found}' \
    "$output" | tr -d '\r' | tail -n1
}

gate2_034_measure_reference_baseline() {
  local lima_home="$1" instance="$2" output="$3" errors="$4"
  local workload="$5"
  LIMA_HOME="$lima_home" limactl shell --tty=false --workdir / \
    "$instance" -- sh -eu -c "$workload" \
    >"$output" 2>"$errors"
}

gate2_034_measure_reference_observed() {
  local hideout="$1" store="$2" lima_home="$3" profile="$4"
  local workspace="$5" shim="$6" hostfsd="$7"
  local output="$8" errors="$9" workload="${10}"
  HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
    HIDEOUT_LINUX_SHIM_PATH="$shim" \
    HIDEOUT_LINUX_HOSTFSD_PATH="$hostfsd" \
    "$hideout" run --profile "$profile" --backend lima --network direct \
      --workspace "$workspace" --guest-workspace /workspace -- \
      sh -eu -c "$workload" >"$output" 2>"$errors"
}

gate2_034_run_reference_workload() {
  local out="$1" lima_home="$2" instance="$3"
  local hideout="$4" store="$5" profile="$6" workspace="$7"
  local shim="$8" hostfsd="$9" samples="${10}" warmups="${11}"
  local workload i record baseline_output baseline_errors
  local observed_output observed_errors baseline_ms observed_ms
  local baseline_uid observed_uid baseline_digest observed_digest
  local reference_uid="" reference_digest=""
  local baseline_values observed_values

  workload="$(gate2_034_reference_workload)"
  baseline_values="$out/logs/performance-reference-baseline-ms.txt"
  observed_values="$out/logs/performance-reference-observed-ms.txt"
  : >"$baseline_values"
  : >"$observed_values"

  i=1
  while [ "$i" -le $((warmups + samples)) ]; do
    if [ "$i" -le "$warmups" ]; then record=0; else record=1; fi
    baseline_output="$out/logs/performance-reference-baseline-$i.out"
    baseline_errors="$out/logs/performance-reference-baseline-$i.err"
    observed_output="$out/logs/performance-reference-observed-$i.out"
    observed_errors="$out/logs/performance-reference-observed-$i.err"

    if [ $((i % 2)) -eq 1 ]; then
      gate2_034_measure_reference_baseline \
        "$lima_home" "$instance" "$baseline_output" "$baseline_errors" \
        "$workload"
      gate2_034_measure_reference_observed \
        "$hideout" "$store" "$lima_home" "$profile" "$workspace" \
        "$shim" "$hostfsd" "$observed_output" "$observed_errors" \
        "$workload"
    else
      gate2_034_measure_reference_observed \
        "$hideout" "$store" "$lima_home" "$profile" "$workspace" \
        "$shim" "$hostfsd" "$observed_output" "$observed_errors" \
        "$workload"
      gate2_034_measure_reference_baseline \
        "$lima_home" "$instance" "$baseline_output" "$baseline_errors" \
        "$workload"
    fi

    baseline_ms="$(gate2_034_reference_result "$baseline_output" elapsed_ms)"
    observed_ms="$(gate2_034_reference_result "$observed_output" elapsed_ms)"
    baseline_uid="$(gate2_034_reference_result "$baseline_output" uid)"
    observed_uid="$(gate2_034_reference_result "$observed_output" uid)"
    baseline_digest="$(gate2_034_reference_result "$baseline_output" digest)"
    observed_digest="$(gate2_034_reference_result "$observed_output" digest)"
    [ "$baseline_uid" = "$observed_uid" ] &&
      [ "$baseline_digest" = "$observed_digest" ] || {
      echo "concurrent-sessions performance: reference workload identity diverged" >&2
      return 1
    }
    case "$baseline_ms:$observed_ms:$baseline_uid:$baseline_digest" in
      *[!0-9.:a-f]* | :* | *:)
        echo "concurrent-sessions performance: invalid reference workload output" >&2
        return 1
        ;;
    esac
    reference_uid="$baseline_uid"
    reference_digest="$baseline_digest"
    if [ "$record" = "1" ]; then
      printf '%s\n' "$baseline_ms" >>"$baseline_values"
      printf '%s\n' "$observed_ms" >>"$observed_values"
    fi
    i=$((i + 1))
  done

  gate2_034_finalize_reference_result \
    "$out/logs/performance-reference.json" \
    "$baseline_values" "$observed_values" \
    "$samples" "$warmups" "$reference_uid" "$reference_digest"
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
      "$candidate_bin/hideout-hostfsd-linux-$arch" "$samples" "$warmups"
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
    --argjson samples "$samples" --argjson warmups "$warmups" \
    --argjson readySamples "$(gate2_034_values_json "$ready_values")" \
    --argjson readyMedian "$ready_median" --argjson readyP95 "$ready_p95" \
    --argjson extended "$extended_performance" \
    --argjson reference "$reference_evidence" \
    '{schema:
        (if $extended then
          "hideout.concurrent-sessions-performance/v3"
        else
          "hideout.concurrent-sessions-performance/v2"
        end),
      status:"passed",generatedAt:$generatedAt,
      candidate:{commit:$candidateCommit,dirty:$candidateDirty,environmentId:$candidateEnvironmentId,instance:$candidateInstance},
      host:{os:$hostOS,arch:$hostArch},
      runtime:{family:$runtimeFamily,revision:$runtimeRevision,artifactSHA256:$runtimeArtifactSHA256,buildCommit:$runtimeBuildCommit,buildDirty:false},
      methodology:{samples:$samples,warmups:$warmups,readyThresholdMs:2000,
        fixtureSHA256:$fixtureSHA256,candidateSampling:$candidateSampling,
        measurementClock:$measurementClock},
      warmAttach:{samplesMs:$readySamples,medianMs:$readyMedian,p95Ms:$readyP95}} +
      if $extended then {referenceWorkload:$reference} else {} end' \
    >"$out/logs/performance.json"
}
