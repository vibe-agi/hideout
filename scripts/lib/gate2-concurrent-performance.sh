#!/usr/bin/env bash

gate2_034_now_seconds() {
  perl -MTime::HiRes=time -e 'printf "%.9f\n", time'
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
  local ready_values="${10}" fixture_values="${11}" record_ready="${12}"
  local start ready fixture_ns pid attempts=0
  start="$(gate2_034_now_seconds)"
  HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
    HIDEOUT_LINUX_SHIM_PATH="$shim" HIDEOUT_LINUX_HOSTFSD_PATH="$hostfsd" \
    "$hideout" run --profile "$profile" --backend lima --network direct \
      --workspace "$workspace" --guest-workspace /workspace -- sh -eu -c '
printf "READY\n"
start=$(python3 -c "import time; print(time.monotonic_ns())")
git status --short >/dev/null
find node_modules -type f -name package.json -exec cat {} + | wc -c >/dev/null
end=$(python3 -c "import time; print(time.monotonic_ns())")
printf "FIXTURE_NS=%s\n" "$((end - start))"
' >"$output" 2>"$errors" &
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
  fixture_ns="$(sed -n 's/^FIXTURE_NS=//p' "$output" | tail -n1)"
  case "$fixture_ns" in
    ''|*[!0-9]*) echo "concurrent-sessions performance: invalid fixture duration" >&2; return 1 ;;
  esac
  if [ "$record_ready" = "1" ]; then
    awk -v start="$start" -v ready="$ready" 'BEGIN { printf "%.3f\n", (ready-start)*1000 }' >>"$ready_values"
  fi
  awk -v ns="$fixture_ns" 'BEGIN { printf "%.3f\n", ns/1000000 }' >>"$fixture_values"
}

gate2_034_build_tree() {
  local source="$1" bin="$2" arch="$3"
  mkdir -p "$bin"
  (
    cd "$source"
    go build -o "$bin/hideout" ./cmd/hideout
  )
  "$bin/hideout" shim build-linux --out "$bin/hideout-shim-linux-$arch" \
    --goarch "$arch" --source "$source" >/dev/null
  "$bin/hideout" hostfsd build-linux --out "$bin/hideout-hostfsd-linux-$arch" \
    --goarch "$arch" --source "$source" >/dev/null
}

gate2_034_run_performance() {
  local root="$1" out="$2" tmp="$3" lima_home="$4" workspace="$5"
  local candidate_hideout="$6" candidate_store="$7" candidate_profile="$8"
  local baseline_commit="$9" samples="${10}" warmups="${11}" arch="${12}"
  local candidate_bin baseline_root baseline_bin baseline_store baseline_profile
  local candidate_anchor_pid candidate_release candidate_marker candidate_env candidate_instance
  local baseline_env baseline_instance i record output errors
  local ready_values candidate_fixture_values baseline_fixture_values
  local ready_median ready_p95 candidate_median candidate_p95 baseline_median baseline_p95 ratio
  local runtime_family runtime_revision runtime_digest runtime_build_commit candidate_commit candidate_dirty
	local fixture_digest candidate_record baseline_record control_dir

  candidate_bin="$(dirname "$candidate_hideout")"
  baseline_root="$tmp/baseline-worktree"
  baseline_bin="$tmp/baseline-bin"
  baseline_store="$tmp/baseline-store"
  baseline_profile="g34b"
	control_dir="$workspace/.hideout-gate-control"
  candidate_release="$control_dir/candidate-anchor-release"
  candidate_marker="$control_dir/candidate-anchor-ready"
  ready_values="$out/logs/performance-ready-ms.txt"
  candidate_fixture_values="$out/logs/performance-candidate-fixture-ms.txt"
  baseline_fixture_values="$out/logs/performance-baseline-fixture-ms.txt"
  : >"$ready_values"
  : >"$candidate_fixture_values"
  : >"$baseline_fixture_values"

  gate2_034_prepare_fixture "$workspace"
	mkdir -p "$control_dir"
	fixture_digest="$(gate2_034_fixture_digest "$workspace")"
	candidate_commit="$(git -C "$root" rev-parse HEAD)"
	[ "$baseline_commit" != "$candidate_commit" ] || {
		echo "concurrent-sessions performance: baseline commit must differ from candidate" >&2
		return 2
	}
  git -C "$root" worktree add --detach "$baseline_root" "$baseline_commit" \
    >"$out/logs/baseline-worktree.out" 2>"$out/logs/baseline-worktree.err"
  GATE2_034_CLEANUP_BASELINE_WORKTREE="$baseline_root"
  GATE2_034_CLEANUP_BASELINE_STORE="$baseline_store"
  gate2_034_build_tree "$baseline_root" "$baseline_bin" "$arch"

  HIDEOUT_STORE_ROOT="$candidate_store" LIMA_HOME="$lima_home" \
    HIDEOUT_LINUX_SHIM_PATH="$candidate_bin/hideout-shim-linux-$arch" \
    HIDEOUT_LINUX_HOSTFSD_PATH="$candidate_bin/hideout-hostfsd-linux-$arch" \
    "$candidate_hideout" run --profile "$candidate_profile" --backend lima --network direct \
      --workspace "$workspace" --guest-workspace /workspace -- sh -eu -c '
touch /workspace/.hideout-gate-control/candidate-anchor-ready
while [ ! -f /workspace/.hideout-gate-control/candidate-anchor-release ]; do sleep 0.05; done
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
      "$ready_values" "$candidate_fixture_values" "$record"
    if [ "$record" = "0" ]; then
      sed -i '' -e '$d' "$candidate_fixture_values"
    fi
  done
	[ "$(gate2_034_fixture_digest "$workspace")" = "$fixture_digest" ] || {
		echo "concurrent-sessions performance: candidate changed the tracked workspace fixture" >&2
		return 1
	}

  touch "$candidate_release"
  wait "$candidate_anchor_pid"
  GATE2_034_CLEANUP_FIRST_PID=""
  candidate_env="$(HIDEOUT_STORE_ROOT="$candidate_store" "$candidate_hideout" env list | awk 'NR == 2 {print $1}')"
  candidate_instance="$(jq -r '.instanceName' "$candidate_store/environments"/*/environment.json | head -n1)"
  HIDEOUT_STORE_ROOT="$candidate_store" LIMA_HOME="$lima_home" \
    "$candidate_hideout" stop "$candidate_env" >/dev/null

  mkdir -p "$baseline_store"
  HIDEOUT_STORE_ROOT="$baseline_store" LIMA_HOME="$lima_home" \
    "$baseline_bin/hideout" init --profile "$baseline_profile" --template dev \
      --backend lima --network direct --runtime developer-standard --no-input \
      >"$out/logs/performance-baseline-init.out" \
      2>"$out/logs/performance-baseline-init.err"
  for i in $(seq 1 $((warmups + samples))); do
    if [ "$i" -le "$warmups" ]; then record=0; else record=1; fi
    output="$out/logs/performance-baseline-$i.out"
    errors="$out/logs/performance-baseline-$i.err"
    gate2_034_measure_sample "$baseline_bin/hideout" "$baseline_store" "$lima_home" \
      "$baseline_profile" "$workspace" "$baseline_bin/hideout-shim-linux-$arch" \
      "$baseline_bin/hideout-hostfsd-linux-$arch" "$output" "$errors" \
      "$tmp/baseline-ready-unused.txt" "$baseline_fixture_values" 0
    if [ "$record" = "0" ]; then
      sed -i '' -e '$d' "$baseline_fixture_values"
    fi
  done
	[ "$(gate2_034_fixture_digest "$workspace")" = "$fixture_digest" ] || {
		echo "concurrent-sessions performance: baseline changed the tracked workspace fixture" >&2
		return 1
	}
  baseline_env="$(HIDEOUT_STORE_ROOT="$baseline_store" "$baseline_bin/hideout" env list | awk 'NR == 2 {print $1}')"
  baseline_instance="$(jq -r '.instanceName' "$baseline_store/environments"/*/environment.json | head -n1)"
  HIDEOUT_STORE_ROOT="$baseline_store" LIMA_HOME="$lima_home" \
    "$baseline_bin/hideout" stop "$baseline_env" >/dev/null

  ready_median="$(gate2_034_percentile "$ready_values" 50)"
  ready_p95="$(gate2_034_percentile "$ready_values" 95)"
  candidate_median="$(gate2_034_percentile "$candidate_fixture_values" 50)"
  candidate_p95="$(gate2_034_percentile "$candidate_fixture_values" 95)"
  baseline_median="$(gate2_034_percentile "$baseline_fixture_values" 50)"
  baseline_p95="$(gate2_034_percentile "$baseline_fixture_values" 95)"
  ratio="$(awk -v candidate="$candidate_p95" -v baseline="$baseline_p95" 'BEGIN { printf "%.4f", candidate/baseline }')"

  awk -v value="$ready_p95" 'BEGIN { exit !(value <= 2000.0) }' || {
    echo "concurrent-sessions performance: ready p95 ${ready_p95}ms exceeds 2000ms" >&2
    return 1
  }
  awk -v value="$ratio" 'BEGIN { exit !(value <= 1.25) }' || {
    echo "concurrent-sessions performance: fixture ratio ${ratio} exceeds 1.25" >&2
    return 1
  }

	candidate_record="$(find "$candidate_store/environments" -name environment.json -type f -print -quit)"
	baseline_record="$(find "$baseline_store/environments" -name environment.json -type f -print -quit)"
	[ -f "$candidate_record" ] && [ -f "$baseline_record" ] || {
		echo "concurrent-sessions performance: environment runtime provenance is missing" >&2
		return 1
	}
	runtime_family="$(jq -r '.runtime.family' "$candidate_record")"
	runtime_revision="$(jq -r '.runtime.revision' "$candidate_record")"
	runtime_digest="$(jq -r '.runtime.artifactSHA256' "$candidate_record")"
	jq -e --arg family "$runtime_family" --arg revision "$runtime_revision" --arg digest "$runtime_digest" '
		.runtime.family == $family and .runtime.revision == $revision and .runtime.artifactSHA256 == $digest
	' "$baseline_record" >/dev/null || {
		echo "concurrent-sessions performance: baseline and candidate runtime artifacts differ" >&2
		return 1
	}
  runtime_build_commit="$(jq -r '.families[0].revisions[] | select(.id == $revision) | .artifacts[] | select(.hostOS == "darwin" and .hostArch == "arm64") | .source.buildCommit' --arg revision "$runtime_revision" "$root/internal/runtimecatalog/catalog.json")"
  candidate_dirty="$(cd "$root" && gate2_034_dirty)"

  jq -n \
    --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
    --arg candidateCommit "$candidate_commit" --argjson candidateDirty "$candidate_dirty" \
    --arg baselineCommit "$baseline_commit" --arg hostOS "darwin" --arg hostArch "arm64" \
    --arg runtimeFamily "$runtime_family" --arg runtimeRevision "$runtime_revision" \
    --arg runtimeArtifactSHA256 "$runtime_digest" --arg runtimeBuildCommit "$runtime_build_commit" \
    --arg candidateEnvironmentId "$(jq -r '.id' "$candidate_store/environments"/*/environment.json | head -n1)" \
    --arg candidateInstance "$candidate_instance" \
    --arg baselineEnvironmentId "$(jq -r '.id' "$baseline_store/environments"/*/environment.json | head -n1)" \
    --arg baselineInstance "$baseline_instance" \
		--arg fixtureSHA256 "$fixture_digest" \
    --argjson samples "$samples" --argjson warmups "$warmups" \
    --argjson readySamples "$(gate2_034_values_json "$ready_values")" \
    --argjson candidateFixtureSamples "$(gate2_034_values_json "$candidate_fixture_values")" \
    --argjson baselineFixtureSamples "$(gate2_034_values_json "$baseline_fixture_values")" \
    --argjson readyMedian "$ready_median" --argjson readyP95 "$ready_p95" \
    --argjson candidateMedian "$candidate_median" --argjson candidateP95 "$candidate_p95" \
    --argjson baselineMedian "$baseline_median" --argjson baselineP95 "$baseline_p95" \
    --argjson ratio "$ratio" \
    '{schema:"hideout.concurrent-sessions-performance/v1",status:"passed",generatedAt:$generatedAt,
      candidate:{commit:$candidateCommit,dirty:$candidateDirty,environmentId:$candidateEnvironmentId,instance:$candidateInstance},
      baseline:{commit:$baselineCommit,dirty:false,environmentId:$baselineEnvironmentId,instance:$baselineInstance},
      host:{os:$hostOS,arch:$hostArch},
      runtime:{family:$runtimeFamily,revision:$runtimeRevision,artifactSHA256:$runtimeArtifactSHA256,buildCommit:$runtimeBuildCommit,buildDirty:false},
      methodology:{samples:$samples,warmups:$warmups,readyThresholdMs:2000,fixtureRatioThreshold:1.25,fixtureSHA256:$fixtureSHA256},
      warmAttach:{samplesMs:$readySamples,medianMs:$readyMedian,p95Ms:$readyP95},
      workspaceFixture:{candidateSamplesMs:$candidateFixtureSamples,baselineSamplesMs:$baselineFixtureSamples,
        candidateMedianMs:$candidateMedian,candidateP95Ms:$candidateP95,
        baselineMedianMs:$baselineMedian,baselineP95Ms:$baselineP95,p95Ratio:$ratio}}' \
    >"$out/logs/performance.json"
}
