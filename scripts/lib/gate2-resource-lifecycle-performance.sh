#!/usr/bin/env bash

gate2_036_short_tmpdir() {
  local root="${HIDEOUT_036_SHORT_TMPDIR:-${TMPDIR:-/tmp}}"
  case "$root" in
    /*) ;;
    *) echo "resource-lifecycle gate2: short temporary root must be absolute: $root" >&2; return 2 ;;
  esac
  [ -d "$root" ] || {
    echo "resource-lifecycle gate2: short temporary root does not exist: $root" >&2
    return 2
  }
  printf '%s\n' "$root"
}

gate2_036_now_seconds() {
  perl -MTime::HiRes=time -e 'printf "%.9f\n", time'
}

gate2_036_percentile() {
  local values="$1" percentile="$2" count index
  count="$(wc -l <"$values" | tr -d ' ')"
  [ "$count" -gt 0 ] || return 1
  index=$(((count * percentile + 99) / 100))
  sort -n "$values" | sed -n "${index}p"
}

gate2_036_values_json() {
  jq -Rsc 'split("\n") | map(select(length > 0) | tonumber)' "$1"
}

gate2_036_fixture_digest() {
  local workspace="$1"
  (
    cd "$workspace"
    {
      git rev-parse 'HEAD^{tree}'
      git diff --no-ext-diff --binary HEAD -- . ':(exclude)src/.hideout-gate-*'
    } | shasum -a 256 | awk '{print $1}'
  )
}

gate2_036_prepare_fixture() {
  local workspace="$1" i
  find "$workspace" -mindepth 1 -maxdepth 1 -exec rm -rf {} +
  mkdir -p "$workspace/src" "$workspace/.hideout-gate-control"
  printf '.hideout-gate-control/\n' >"$workspace/.gitignore"
  for i in $(seq 1 1200); do
    printf 'export const value%04d = %d;\n' "$i" "$i" >"$workspace/src/file-$i.js"
  done
  git -C "$workspace" init -q
  git -C "$workspace" config user.name 'Hideout Gate 036'
  git -C "$workspace" config user.email 'gate036@hideout.invalid'
  git -C "$workspace" add .
  git -C "$workspace" commit -qm baseline
  printf '// modified by lifecycle performance fixture\n' >>"$workspace/src/file-1.js"
}

gate2_036_build_tree() {
  local source="$1" bin="$2" arch="$3"
  mkdir -p "$bin"
  (cd "$source" && go build -o "$bin/hideout" ./cmd/hideout)
  "$bin/hideout" shim build-linux --out "$bin/hideout-shim-linux-$arch" \
    --goarch "$arch" --source "$source" >/dev/null
  "$bin/hideout" hostfsd build-linux --out "$bin/hideout-hostfsd-linux-$arch" \
    --goarch "$arch" --source "$source" >/dev/null
  go -C "$source" run ./internal/helperbin/cmd/build-session-supervisor \
    --out "$bin/hideout-session-supervisor-linux-$arch" --goarch "$arch" --source "$source" >/dev/null
}

gate2_036_command_env() {
  local store="$1" lima_home="$2" bin="$3" arch="$4"
	env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
		HIDEOUT_036_AUTOMATIC_STOP=1 \
		HIDEOUT_LINUX_SHIM_PATH="$bin/hideout-shim-linux-$arch" \
    HIDEOUT_LINUX_HOSTFSD_PATH="$bin/hideout-hostfsd-linux-$arch" \
    HIDEOUT_LINUX_SESSION_SUPERVISOR_PATH="$bin/hideout-session-supervisor-linux-$arch" \
    "${@:5}"
}

gate2_036_wait_file() {
  local path="$1" description="$2" i
  for i in $(seq 1 1200); do
    [ -f "$path" ] && return 0
    sleep 0.01
  done
  echo "resource-lifecycle performance: timed out waiting for $description" >&2
  return 1
}

gate2_036_measure_command() {
  local hideout="$1" store="$2" lima_home="$3" bin="$4" arch="$5" workspace="$6"
  local values="$7" output="$8" errors="$9" start end
  start="$(gate2_036_now_seconds)"
  if ! (
    cd "$workspace"
    gate2_036_command_env "$store" "$lima_home" "$bin" "$arch" \
      "$hideout" run -- git status --short
  ) >"$output" 2>"$errors"; then
    cat "$output" "$errors" >&2
    return 1
  fi
  end="$(gate2_036_now_seconds)"
  grep -q '^ M src/file-1.js$' "$output"
  awk -v start="$start" -v end="$end" 'BEGIN { printf "%.3f\n", (end-start)*1000 }' >>"$values"
}

gate2_036_start_anchor() {
  local hideout="$1" store="$2" lima_home="$3" bin="$4" arch="$5" workspace="$6" marker="$7" release="$8" log="$9"
  local marker_relative="${marker#"$workspace"/}" release_relative="${release#"$workspace"/}"
  (
    cd "$workspace"
    gate2_036_command_env "$store" "$lima_home" "$bin" "$arch" \
      "$hideout" run -- sh -eu -c '
touch "$1"
while [ ! -f "$2" ]; do sleep 0.05; done
' gate2-anchor "$marker_relative" "$release_relative"
  ) >"$log" 2>"$log.err" &
  GATE2_036_ANCHOR_PID=$!
  gate2_036_wait_file "$marker" "warm environment anchor"
}

gate2_036_init_profile() {
  local hideout="$1" store="$2" lima_home="$3" bin="$4" arch="$5" log="$6" template="${7:-dev}"
  gate2_036_command_env "$store" "$lima_home" "$bin" "$arch" \
    "$hideout" init --profile default --template "$template" --backend lima --network direct \
      --runtime developer-standard --no-input >"$log" 2>"$log.err"
}

gate2_036_measure_tree() {
  local label="$1" hideout="$2" store="$3" lima_home="$4" bin="$5" arch="$6"
  local workspace="$7" samples="$8" warmups="$9" values="${10}" logs="${11}"
  local marker="$workspace/.hideout-gate-control/anchor-ready"
  local release="$workspace/.hideout-gate-control/anchor-release" i output errors
  rm -f "$marker" "$release"
  gate2_036_init_profile "$hideout" "$store" "$lima_home" "$bin" "$arch" "$logs/$label-init.out" dev
  gate2_036_start_anchor "$hideout" "$store" "$lima_home" "$bin" "$arch" \
    "$workspace" "$marker" "$release" "$logs/$label-anchor.out"
  for i in $(seq 1 $((warmups + samples))); do
    output="$logs/$label-sample-$i.out"
    errors="$logs/$label-sample-$i.err"
    gate2_036_measure_command "$hideout" "$store" "$lima_home" "$bin" "$arch" \
      "$workspace" "$values" "$output" "$errors"
    if [ "$i" -le "$warmups" ]; then
      sed -i '' -e '$d' "$values"
    fi
  done
  touch "$release"
  wait "$GATE2_036_ANCHOR_PID"
  GATE2_036_ANCHOR_PID=""
  local env_name
  env_name="$(HIDEOUT_STORE_ROOT="$store" "$hideout" env list | awk 'NR == 2 {print $1}')"
  [ -n "$env_name" ]
  HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" stop "$env_name" \
    >"$logs/$label-stop.out" 2>"$logs/$label-stop.err"
  HIDEOUT_STORE_ROOT="$store" "$hideout" daemon stop >/dev/null 2>&1 || true
}

gate2_036_run_performance() (
  root="$1" out="$2" _gate_workspace="$3" lima_home="$4" candidate_bin="$5"
  baseline_commit="$6" samples="$7" warmups="$8" arch="$9"
  short_tmp="$(gate2_036_short_tmpdir)"
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-036-performance.XXXXXX")"
  baseline_root="$tmp/baseline-worktree"
  baseline_bin="$tmp/baseline-bin"
  candidate_store="$(mktemp -d "$short_tmp/h36pc.XXXXXX")"
  baseline_store="$(mktemp -d "$short_tmp/h36pb.XXXXXX")"
  workspace="$(mktemp -d "$(dirname "$root")/hideout-036-performance-workspace.XXXXXX")"
  workspace="$(cd "$workspace" && pwd -P)"
  candidate_lima_home="$(mktemp -d "$short_tmp/h36plc.XXXXXX")"
  baseline_lima_home="$(mktemp -d "$short_tmp/h36plb.XXXXXX")"
  candidate_values="$out/logs/performance-candidate-ms.txt"
  baseline_values="$out/logs/performance-baseline-ms.txt"
  : >"$candidate_values"
  : >"$baseline_values"
  GATE2_036_ANCHOR_PID=""
	GATE2_036_PERF_CANDIDATE_PID=""
	GATE2_036_PERF_BASELINE_PID=""
  gate2_036_performance_cleanup() {
    if [ -n "$GATE2_036_ANCHOR_PID" ]; then
      kill "$GATE2_036_ANCHOR_PID" 2>/dev/null || true
      wait "$GATE2_036_ANCHOR_PID" 2>/dev/null || true
    fi
		for pid in "$GATE2_036_PERF_CANDIDATE_PID" "$GATE2_036_PERF_BASELINE_PID"; do
			[ -z "$pid" ] || kill "$pid" 2>/dev/null || true
			[ -z "$pid" ] || wait "$pid" 2>/dev/null || true
		done
    HIDEOUT_STORE_ROOT="$candidate_store" "$candidate_bin/hideout" daemon stop >/dev/null 2>&1 || true
    [ ! -x "$baseline_bin/hideout" ] || HIDEOUT_STORE_ROOT="$baseline_store" "$baseline_bin/hideout" daemon stop >/dev/null 2>&1 || true
    git -C "$root" worktree remove --force "$baseline_root" >/dev/null 2>&1 || true
    rm -rf "$candidate_store" "$baseline_store" "$candidate_lima_home" \
      "$baseline_lima_home" "$workspace" "$tmp"
  }
  trap gate2_036_performance_cleanup EXIT

  gate2_036_prepare_fixture "$workspace"
  fixture_digest="$(gate2_036_fixture_digest "$workspace")"
  git -C "$root" worktree add --detach "$baseline_root" "$baseline_commit" \
    >"$out/logs/performance-baseline-worktree.out" 2>"$out/logs/performance-baseline-worktree.err"
  gate2_036_build_tree "$baseline_root" "$baseline_bin" "$arch"

  local candidate_marker="$workspace/src/.hideout-gate-candidate-anchor-ready"
  local candidate_release="$workspace/src/.hideout-gate-candidate-anchor-release"
  local baseline_marker="$workspace/src/.hideout-gate-baseline-anchor-ready"
  local baseline_release="$workspace/src/.hideout-gate-baseline-anchor-release"
  local i first_label second_label label output errors values hideout store lima bin
  rm -f "$candidate_marker" "$candidate_release" "$baseline_marker" "$baseline_release"
  gate2_036_init_profile "$candidate_bin/hideout" "$candidate_store" "$candidate_lima_home" \
    "$candidate_bin" "$arch" "$out/logs/candidate-init.out" dev
  gate2_036_start_anchor "$candidate_bin/hideout" "$candidate_store" "$candidate_lima_home" \
    "$candidate_bin" "$arch" "$workspace" "$candidate_marker" "$candidate_release" "$out/logs/candidate-anchor.out"
  GATE2_036_PERF_CANDIDATE_PID="$GATE2_036_ANCHOR_PID"
  gate2_036_init_profile "$baseline_bin/hideout" "$baseline_store" "$baseline_lima_home" \
    "$baseline_bin" "$arch" "$out/logs/baseline-init.out" dev
  gate2_036_start_anchor "$baseline_bin/hideout" "$baseline_store" "$baseline_lima_home" \
    "$baseline_bin" "$arch" "$workspace" "$baseline_marker" "$baseline_release" "$out/logs/baseline-anchor.out"
  GATE2_036_PERF_BASELINE_PID="$GATE2_036_ANCHOR_PID"
  GATE2_036_ANCHOR_PID=""

  for i in $(seq 1 $((warmups + samples))); do
    first_label=candidate
    second_label=baseline
    if [ $((i % 2)) -eq 0 ]; then
      first_label=baseline
      second_label=candidate
    fi
    for label in "$first_label" "$second_label"; do
      if [ "$label" = candidate ]; then
        hideout="$candidate_bin/hideout"; store="$candidate_store"; lima="$candidate_lima_home"
        bin="$candidate_bin"; values="$candidate_values"
      else
        hideout="$baseline_bin/hideout"; store="$baseline_store"; lima="$baseline_lima_home"
        bin="$baseline_bin"; values="$baseline_values"
      fi
      output="$out/logs/$label-sample-$i.out"
      errors="$out/logs/$label-sample-$i.err"
      gate2_036_measure_command "$hideout" "$store" "$lima" "$bin" "$arch" \
        "$workspace" "$values" "$output" "$errors"
      if [ "$i" -le "$warmups" ]; then
        sed -i '' -e '$d' "$values"
      fi
    done
  done
  touch "$candidate_release" "$baseline_release"
  wait "$GATE2_036_PERF_CANDIDATE_PID"
  wait "$GATE2_036_PERF_BASELINE_PID"
	GATE2_036_PERF_CANDIDATE_PID=""
	GATE2_036_PERF_BASELINE_PID=""
  local candidate_env baseline_env
  candidate_env="$(HIDEOUT_STORE_ROOT="$candidate_store" "$candidate_bin/hideout" env list | awk 'NR == 2 {print $1}')"
  baseline_env="$(HIDEOUT_STORE_ROOT="$baseline_store" "$baseline_bin/hideout" env list | awk 'NR == 2 {print $1}')"
  [ -n "$candidate_env" ] && [ -n "$baseline_env" ]
  HIDEOUT_STORE_ROOT="$candidate_store" LIMA_HOME="$candidate_lima_home" "$candidate_bin/hideout" stop "$candidate_env" \
    >"$out/logs/candidate-stop.out" 2>"$out/logs/candidate-stop.err"
  HIDEOUT_STORE_ROOT="$baseline_store" LIMA_HOME="$baseline_lima_home" "$baseline_bin/hideout" stop "$baseline_env" \
    >"$out/logs/baseline-stop.out" 2>"$out/logs/baseline-stop.err"
  HIDEOUT_STORE_ROOT="$candidate_store" "$candidate_bin/hideout" daemon stop >/dev/null 2>&1 || true
  HIDEOUT_STORE_ROOT="$baseline_store" "$baseline_bin/hideout" daemon stop >/dev/null 2>&1 || true
  [ "$(gate2_036_fixture_digest "$workspace")" = "$fixture_digest" ]

  candidate_median="$(gate2_036_percentile "$candidate_values" 50)"
  baseline_median="$(gate2_036_percentile "$baseline_values" 50)"
  observed_delta="$(awk -v candidate="$candidate_median" -v baseline="$baseline_median" 'BEGIN { printf "%.4f", candidate-baseline }')"
  allowed_delta="$(awk -v baseline="$baseline_median" 'BEGIN { value=baseline*0.05; if (value<10) value=10; printf "%.4f", value }')"
  awk -v observed="$observed_delta" -v allowed="$allowed_delta" 'BEGIN { exit !(observed <= allowed) }' || {
    echo "resource-lifecycle performance: median regression ${observed_delta}ms exceeds ${allowed_delta}ms" >&2
    return 1
  }

  candidate_record="$(find "$candidate_store/environments" -name environment.json -type f -print -quit)"
  baseline_record="$(find "$baseline_store/environments" -name environment.json -type f -print -quit)"
  [ -f "$candidate_record" ] && [ -f "$baseline_record" ]
  runtime_family="$(jq -r '.runtime.family' "$candidate_record")"
  runtime_revision="$(jq -r '.runtime.revision' "$candidate_record")"
  runtime_digest="$(jq -r '.runtime.artifactSHA256' "$candidate_record")"
  jq -e --arg family "$runtime_family" --arg revision "$runtime_revision" --arg digest "$runtime_digest" '
    .runtime.family == $family and .runtime.revision == $revision and .runtime.artifactSHA256 == $digest
  ' "$baseline_record" >/dev/null
  runtime_build_commit="$(jq -r '.families[0].revisions[] | select(.id == $revision) |
    .artifacts[] | select(.hostOS == "darwin" and .hostArch == "arm64") | .source.buildCommit' \
    --arg revision "$runtime_revision" "$root/internal/runtimecatalog/catalog.json")"

  jq -n \
    --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
    --arg candidateCommit "$(git -C "$root" rev-parse HEAD)" \
    --argjson candidateDirty "$(cd "$root" && if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then echo true; else echo false; fi)" \
    --arg baselineCommit "$baseline_commit" \
    --arg runtimeFamily "$runtime_family" --arg runtimeRevision "$runtime_revision" \
    --arg runtimeArtifactSHA256 "$runtime_digest" --arg runtimeBuildCommit "$runtime_build_commit" \
    --arg fixtureSHA256 "$fixture_digest" --argjson samples "$samples" --argjson warmups "$warmups" \
    --argjson candidateSamples "$(gate2_036_values_json "$candidate_values")" \
    --argjson baselineSamples "$(gate2_036_values_json "$baseline_values")" \
    --argjson candidateMedianMs "$candidate_median" --argjson baselineMedianMs "$baseline_median" \
    --argjson observedDeltaMs "$observed_delta" --argjson allowedDeltaMs "$allowed_delta" '
    {schema:"hideout.lifecycle-performance/v1",status:"passed",generatedAt:$generatedAt,
     candidate:{commit:$candidateCommit,dirty:$candidateDirty},baseline:{commit:$baselineCommit,dirty:false},
     host:{os:"darwin",arch:"arm64"},
     runtime:{family:$runtimeFamily,revision:$runtimeRevision,artifactSHA256:$runtimeArtifactSHA256,
       buildCommit:$runtimeBuildCommit,buildDirty:false},
     methodology:{command:"hideout run -- git status --short",samples:$samples,warmups:$warmups,
       fixtureSHA256:$fixtureSHA256,sampleOrder:"paired-alternating-ab-ba"},candidateSamplesMs:$candidateSamples,
     baselineSamplesMs:$baselineSamples,candidateMedianMs:$candidateMedianMs,
     baselineMedianMs:$baselineMedianMs,observedDeltaMs:$observedDeltaMs,allowedDeltaMs:$allowedDeltaMs}
  ' >"$out/logs/performance.json"

  gate2_036_performance_cleanup
  trap - EXIT
)
