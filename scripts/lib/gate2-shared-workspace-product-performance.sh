#!/usr/bin/env bash
# shellcheck disable=SC2329

# The cleanup function is invoked indirectly through a subshell EXIT trap.

# Release-shaped 035 performance/correctness driver. The caller must source
# workspace-research.sh, gate2-shared-workspace.sh, and
# gate2-shared-workspace-path.sh before invoking it. Test orchestration lives in
# this source tree, but every product effect goes through the installed hideout
# binary and its manifest-verified packaged Portal helper.

gate2_shared_workspace_measure_product() (
  set -euo pipefail

  if [ "$#" -ne 10 ]; then
    echo "usage: gate2_shared_workspace_measure_product <repo> <out> <store> <lima-home> <hideout> <profile> <fixture-root> <samples> <baseline> <path-correctness>" >&2
    return 2
  fi
  local repo="$1" out="$2" store="$3" lima_home="$4" hideout="$5" profile="$6"
  local fixture_root="$7" samples="$8" baseline="$9" path_correctness_report="${10}"
  local perf="$fixture_root/performance" hold="$fixture_root/performance-hold"
  local atomic="$fixture_root/atomic-live"
  local profile_cache="$store/profiles/$profile/cache"
  local control="$profile_cache/035-static-virtiofs-control"
  local paired_driver="$profile_cache/035-paired-workload.py"
  local hold_pid="" atomic_pid="" saturation_pid=""

  mkdir -p "$out/raw" "$out/filesystem-control/raw" \
    "$perf/git" "$perf/package" "$perf/atomic" \
    "$hold" "$atomic"
  declare -F gate2_035_path_correctness_judge >/dev/null || {
    echo "shared-workspace Gate 2 path correctness judge is unavailable" >&2
    return 2
  }
  gate2_035_path_correctness_judge "$path_correctness_report"
  cp "$path_correctness_report" "$out/path-correctness.json"
  "$repo/test/fixtures/workspaceattach/generate.sh" git-10k "$perf/git"
  "$repo/test/fixtures/workspaceattach/generate.sh" package-20k "$perf/package"
  cp "$repo/test/fixtures/workspaceattach/workload.py" "$perf/workload.py"

  # The control is a second copy of the exact fixed fixture under the profile
  # cache's static virtiofs mount. Candidate and control are sampled by one
  # process in one VM, alternating order on every observation.
  rm -rf "$control"
  mkdir -p "$control/git" "$control/package" "$control/atomic"
  "$repo/test/fixtures/workspaceattach/generate.sh" git-10k "$control/git"
  "$repo/test/fixtures/workspaceattach/generate.sh" package-20k "$control/package"
  cp "$repo/test/fixtures/workspaceattach/workload.py" "$control/workload.py"
  cp "$repo/test/fixtures/workspaceattach/paired_workload.py" "$paired_driver"
  chmod 600 "$paired_driver"

  local fixture_digest control_digest baseline_digest
  fixture_digest="$(workspace_tree_digest "$perf")"
  control_digest="$(workspace_tree_digest "$control")"
  baseline_digest="$(tr -d '\n' <"$baseline/fixture.sha256")"
  [ "$fixture_digest" = "$baseline_digest" ] || {
    echo "shared-workspace Gate 2 fixture differs from the accepted baseline" >&2
    return 1
  }
  [ "$control_digest" = "$fixture_digest" ] || {
    echo "shared-workspace Gate 2 static control differs from the candidate fixture" >&2
    return 1
  }
  printf '%s\n' "$fixture_digest" >"$out/fixture.sha256"
  printf '%s\n' "$control_digest" >"$out/filesystem-control/fixture.sha256"
  printf 'host-ready\n' >"$perf/atomic/host-value.txt"
  printf 'guest-ready\n' >"$perf/atomic/guest-value.txt"

  gate2_035_product_command() {
    env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$@"
  }

  gate2_035_product_run() {
    local workspace="$1"
    shift
    gate2_035_product_command "$hideout" run --profile "$profile" --backend lima \
      --network direct --workspace "$workspace" --guest-workspace /workspace \
      --terminal never -- "$@"
  }

  gate2_035_product_cleanup() {
    touch "$hold/release" "$atomic/release" "$perf/saturation.release" 2>/dev/null || true
    local pid
    for pid in "$hold_pid" "$atomic_pid" "$saturation_pid"; do
      if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
        kill "$pid" 2>/dev/null || true
        wait "$pid" 2>/dev/null || true
      fi
    done
  }
  trap gate2_035_product_cleanup EXIT

  gate2_035_product_run "$hold" sh -eu -c '
touch /workspace/ready
while [ ! -f /workspace/release ]; do sleep 0.02; done
' >"$out/raw/hold.out" 2>"$out/raw/hold.err" &
  hold_pid=$!
  gate2_035_wait_file "$hold/ready" "performance hold session" 6000 "$hold_pid"

  # One real product attachment owns both steady-state filesystem lanes. The
  # workload warms each side once, then alternates Portal and static virtiofs
  # order per sample so host/VM load drift cannot favor one side.
  gate2_035_product_run "$perf" python3 \
    /hideout/profile/cache/035-paired-workload.py \
    /workspace /hideout/profile/cache/035-static-virtiofs-control "$samples" \
    >"$out/raw/filesystem-paired.tsv" 2>"$out/raw/filesystem-paired.err"
  awk -F '\t' '$1 == "git-status" && $3 == "candidate" { print $4 }' "$out/raw/filesystem-paired.tsv" \
    >"$out/raw/git-status.values"
  awk -F '\t' '$1 == "git-status" && $3 == "control" { print $4 }' "$out/raw/filesystem-paired.tsv" \
    >"$out/filesystem-control/raw/git-status.values"
  awk -F '\t' '$1 == "package-scan" && $3 == "candidate" { print $4 }' "$out/raw/filesystem-paired.tsv" \
    >"$out/raw/package-scan.values"
  awk -F '\t' '$1 == "package-scan" && $3 == "control" { print $4 }' "$out/raw/filesystem-paired.tsv" \
    >"$out/filesystem-control/raw/package-scan.values"

  # The Python driver observes attachment activation from authenticated daemon
  # status and target first-byte separately. It never infers mount readiness
  # from source text or from a locally compiled probe.
  python3 "$repo/test/fixtures/workspaceattach/product_timing.py" \
    --samples "$samples" --hideout "$hideout" --store "$store" \
    --lima-home "$lima_home" --profile "$profile" --workspace "$perf" \
    --mount-ready-out "$out/raw/mount-ready.values" \
    --first-byte-out "$out/raw/first-byte.values"

  cat >"$atomic/atomic.py" <<'PY'
import os
import pathlib
import sys
import time

root = pathlib.Path("/workspace")
(root / "ready").touch()
samples = int(sys.argv[1])
for sample in range(1, samples + 1):
    while not (root / f"host-go-{sample}").exists():
        time.sleep(.001)
    expected = f"host-{sample}"
    while (root / "host-value.txt").read_text().strip() != expected:
        time.sleep(.001)
    (root / f"host-seen-{sample}").touch()
    while not (root / f"guest-go-{sample}").exists():
        time.sleep(.001)
    temporary = root / f"guest-value.tmp.{os.getpid()}"
    with temporary.open("w") as stream:
        stream.write(f"guest-{sample}\n")
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, root / "guest-value.txt")
    descriptor = os.open(root, os.O_RDONLY)
    os.fsync(descriptor)
    os.close(descriptor)
    (root / f"guest-done-{sample}").touch()
PY
  printf 'host-0\n' >"$atomic/host-value.txt"
  printf 'guest-0\n' >"$atomic/guest-value.txt"
  gate2_035_product_run "$atomic" python3 /workspace/atomic.py "$samples" \
    >"$out/raw/atomic.out" 2>"$out/raw/atomic.err" &
  atomic_pid=$!
  gate2_035_wait_file "$atomic/ready" "atomic visibility probe" 6000 "$atomic_pid"
	  python3 - "$atomic" "$samples" \
	    "$out/raw/atomic-host-to-guest.values" \
	    "$out/raw/atomic-guest-to-host.values" <<'PY'
import os
import pathlib
import sys
import time

root = pathlib.Path(sys.argv[1])
samples = int(sys.argv[2])
host_values = pathlib.Path(sys.argv[3])
guest_values = pathlib.Path(sys.argv[4])

def wait_for(path, description):
    deadline = time.monotonic() + 300
    while not path.is_file():
        if time.monotonic() >= deadline:
            raise TimeoutError(f"timed out waiting for {description}")
        time.sleep(.001)

with host_values.open("w") as host_stream, guest_values.open("w") as guest_stream:
    for sample in range(1, samples + 1):
        started = time.monotonic_ns()
        temporary = root / "host-value.next"
        temporary.write_text(f"host-{sample}\n")
        os.replace(temporary, root / "host-value.txt")
        (root / f"host-go-{sample}").touch()
        wait_for(root / f"host-seen-{sample}", f"host-to-guest atomic sample {sample}")
        host_stream.write(f"{(time.monotonic_ns() - started) / 1_000_000:.6f}\n")

        started = time.monotonic_ns()
        (root / f"guest-go-{sample}").touch()
        wait_for(root / f"guest-done-{sample}", f"guest-to-host atomic sample {sample}")
        if (root / "guest-value.txt").read_text().strip() != f"guest-{sample}":
            raise RuntimeError(f"guest-to-host atomic sample {sample} has the wrong value")
        guest_stream.write(f"{(time.monotonic_ns() - started) / 1_000_000:.6f}\n")
PY
  gate2_035_wait_process "$atomic_pid" "atomic visibility probe"
  atomic_pid=""

  # Saturate one real attachment while a sibling measures metadata latency.
  # Release-to-process-exit is retained as the teardown bound.
  gate2_035_product_run "$perf" python3 -c '
import os, pathlib
root=pathlib.Path("/workspace")
(root/"saturation.ready").touch()
while not (root/"saturation.release").exists():
    for current, _, files in os.walk(root/"package"/"node_modules"):
        for name in files:
            os.stat(os.path.join(current, name), follow_symlinks=False)
' >"$out/raw/saturation-owner.out" 2>"$out/raw/saturation-owner.err" &
  saturation_pid=$!
  gate2_035_wait_file "$perf/saturation.ready" "saturation owner" 6000 "$saturation_pid"
  gate2_035_product_run "$perf" python3 -c '
import pathlib,time
path=pathlib.Path("/workspace/git/.git/HEAD")
for _ in range(100):
    started=time.monotonic_ns()
    path.stat()
    print(f"{(time.monotonic_ns()-started)/1000000:.6f}")
' >"$out/raw/saturation-metadata.values" 2>"$out/raw/saturation-sibling.err"
	  local teardown_ms
	  teardown_ms="$(python3 - "$perf/saturation.release" "$saturation_pid" <<'PY'
import pathlib
import subprocess
import sys
import time

release = pathlib.Path(sys.argv[1])
pid = sys.argv[2]
started = time.monotonic_ns()
release.touch()
deadline = time.monotonic() + 30
while True:
    observed = subprocess.run(
        ["ps", "-o", "state=", "-p", pid], capture_output=True, text=True, check=False
    )
    state = observed.stdout.strip()
    if observed.returncode != 0 or not state or state.startswith("Z"):
        break
    if time.monotonic() >= deadline:
        raise TimeoutError("timed out waiting for saturation owner teardown")
    time.sleep(.001)
print(f"{(time.monotonic_ns() - started) / 1_000_000:.6f}")
PY
	  )"
	  gate2_035_wait_process "$saturation_pid" "saturation owner teardown"
	  saturation_pid=""
  jq -n --argjson teardownMs "$teardown_ms" '{teardownMs:$teardownMs}' \
    >"$out/saturation.json"

  for metric in git-status package-scan mount-ready first-byte \
    atomic-host-to-guest atomic-guest-to-host saturation-metadata; do
    workspace_summarize_samples "$out/raw/$metric.values" \
      "$out/$metric-summary.json"
  done
  for metric in git-status package-scan; do
    workspace_summarize_samples "$out/filesystem-control/raw/$metric.values" \
      "$out/filesystem-control/$metric-summary.json"
  done

  touch "$hold/release"
  gate2_035_wait_process "$hold_pid" "performance hold session"
  hold_pid=""
  trap - EXIT
)
