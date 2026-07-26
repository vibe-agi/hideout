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

gate2_034_prepare_fixture() {
  local workspace="$1" i package
  find "$workspace" -mindepth 1 -maxdepth 1 -exec rm -rf {} +
  mkdir -p "$workspace/src" "$workspace/node_modules"
	printf '.hideout-gate-control/\n' >"$workspace/.gitignore"
  for i in $(seq 1 1200); do
    printf 'export const value%04d = %d;\n' "$i" "$i" >"$workspace/src/file-$i.js"
  done
  for i in $(seq 1 600); do
    package="$workspace/node_modules/pkg-$i"
    mkdir -p "$package"
    printf '{"name":"pkg-%04d","version":"1.0.0","main":"index.js"}\n' "$i" >"$package/package.json"
    printf 'module.exports = %d;\n' "$i" >"$package/index.js"
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
		cd "$workspace"
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
  local i record output errors
  local ready_values ready_median ready_p95
  local runtime_family runtime_revision runtime_digest runtime_build_commit candidate_commit candidate_dirty
	local fixture_digest candidate_record control_dir

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

  for i in $(seq 1 $((warmups + samples))); do
    if [ "$i" -le "$warmups" ]; then record=0; else record=1; fi
    output="$out/logs/performance-candidate-$i.out"
    errors="$out/logs/performance-candidate-$i.err"
    gate2_034_measure_sample "$candidate_hideout" "$candidate_store" "$lima_home" \
      "$candidate_profile" "$workspace" "$candidate_bin/hideout-shim-linux-$arch" \
      "$candidate_bin/hideout-hostfsd-linux-$arch" "$output" "$errors" \
      "$ready_values" "$record"
  done
	[ "$(gate2_034_fixture_digest "$workspace")" = "$fixture_digest" ] || {
		echo "concurrent-sessions performance: candidate changed the tracked workspace fixture" >&2
		return 1
	}

  kill "$candidate_anchor_pid" 2>/dev/null || true
  wait "$candidate_anchor_pid" 2>/dev/null || true
  GATE2_034_CLEANUP_FIRST_PID=""
  candidate_env="$(HIDEOUT_STORE_ROOT="$candidate_store" "$candidate_hideout" env list | awk 'NR == 2 {print $1}')"
  candidate_instance="$(jq -r '.instanceName' "$candidate_store/environments"/*/environment.json | head -n1)"
  HIDEOUT_STORE_ROOT="$candidate_store" LIMA_HOME="$lima_home" \
    "$candidate_hideout" stop "$candidate_env" >/dev/null

  ready_median="$(gate2_034_percentile "$ready_values" 50)"
  ready_p95="$(gate2_034_percentile "$ready_values" 95)"

  awk -v value="$ready_p95" 'BEGIN { exit !(value <= 2000.0) }' || {
    echo "concurrent-sessions performance: ready p95 ${ready_p95}ms exceeds 2000ms" >&2
    return 1
  }
	candidate_record="$(find "$candidate_store/environments" -name environment.json -type f -print -quit)"
	[ -f "$candidate_record" ] || {
		echo "concurrent-sessions performance: environment runtime provenance is missing" >&2
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
    '{schema:"hideout.concurrent-sessions-performance/v2",status:"passed",generatedAt:$generatedAt,
      candidate:{commit:$candidateCommit,dirty:$candidateDirty,environmentId:$candidateEnvironmentId,instance:$candidateInstance},
      host:{os:$hostOS,arch:$hostArch},
      runtime:{family:$runtimeFamily,revision:$runtimeRevision,artifactSHA256:$runtimeArtifactSHA256,buildCommit:$runtimeBuildCommit,buildDirty:false},
      methodology:{samples:$samples,warmups:$warmups,readyThresholdMs:2000,
        fixtureSHA256:$fixtureSHA256,candidateSampling:$candidateSampling,
        measurementClock:$measurementClock},
      warmAttach:{samplesMs:$readySamples,medianMs:$readyMedian,p95Ms:$readyP95}}' \
    >"$out/logs/performance.json"
}
