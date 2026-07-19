#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=scripts/lib/workspace-research.sh
source "$repo_root/scripts/lib/workspace-research.sh"

[[ $# -eq 1 && "$1" == /* ]] || {
  echo "usage: scripts/test-workspace-transport-research.sh portal <absolute-artifact-dir>" >&2
  exit 2
}
artifact_dir="$1"
[[ ! -e "$artifact_dir" ]] || {
  echo "workspace Portal performance artifact directory already exists: $artifact_dir" >&2
  exit 2
}
for tool in go git hideout jq limactl python3; do
  command -v "$tool" >/dev/null 2>&1 || {
    echo "workspace Portal performance probe requires $tool" >&2
    exit 2
  }
done

baseline_root="$repo_root/dist/workspace-research/035/baseline-static-virtiofs"
[[ -r "$baseline_root/baseline.json" && -r "$baseline_root/fixture.sha256" ]] || {
  echo "workspace Portal performance probe requires the static baseline" >&2
  exit 2
}

mkdir -p "$repo_root/.tmp"
work_root="$(mktemp -d "$repo_root/.tmp/workspace-portal-performance.XXXXXX")"
bootstrap="$work_root/bootstrap"
control="$bootstrap/control"
host_root="$work_root/host-root"
host_probe="$work_root/hideout-workspace-probe-host"
guest_probe="$bootstrap/hideout-workspace-probe"
server_pid=""
hold_pid=""
atomic_pid=""

cleanup() {
  touch "$bootstrap/stop" 2>/dev/null || true
  for pid in "$atomic_pid" "$hold_pid" "$server_pid"; do
    if [[ -n "$pid" ]]; then
      kill "$pid" >/dev/null 2>&1 || true
      wait "$pid" >/dev/null 2>&1 || true
    fi
  done
  find "$work_root" -depth -delete 2>/dev/null || true
}
trap cleanup EXIT

mkdir -p "$artifact_dir/raw" "$bootstrap" "$control" "$host_root/git" "$host_root/package" "$host_root/atomic"
"$repo_root/test/fixtures/workspaceattach/generate.sh" git-10k "$host_root/git"
"$repo_root/test/fixtures/workspaceattach/generate.sh" package-20k "$host_root/package"
cp "$repo_root/test/fixtures/workspaceattach/workload.py" "$host_root/workload.py"
cp "$repo_root/test/fixtures/workspaceattach/workload.py" "$bootstrap/workload.py"

fixture_digest="$(workspace_tree_digest "$host_root")"
baseline_fixture_digest="$(tr -d '\n' <"$baseline_root/fixture.sha256")"
[[ "$fixture_digest" == "$baseline_fixture_digest" ]] || {
  echo "workspace Portal fixture differs from the static baseline" >&2
  echo "baseline=$baseline_fixture_digest candidate=$fixture_digest" >&2
  exit 1
}
printf '%s\n' "$fixture_digest" >"$artifact_dir/fixture.sha256"
printf 'host-ready\n' >"$host_root/atomic/host-value.txt"
printf 'guest-ready\n' >"$host_root/atomic/guest-value.txt"

(
  cd "$repo_root"
  go build -o "$host_probe" ./cmd/hideout-workspace-probe
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
    go build -o "$guest_probe" ./cmd/hideout-workspace-probe
)

cat >"$bootstrap/hold.sh" <<'GUEST'
#!/bin/sh
set -eu
printf 'ready\n' >/workspace/session-ready
while [ ! -f /workspace/stop ]; do sleep 0.05; done
GUEST
chmod 0700 "$bootstrap/hold.sh"

cat >"$bootstrap/portal_perf.py" <<'PY'
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile
import time

mode, endpoint, credential, control = sys.argv[1:5]
probe = "/workspace/hideout-workspace-probe"


def start_mount(label):
    mount = pathlib.Path(tempfile.mkdtemp(prefix=f"hideout-portal-{label}-"))
    ready = pathlib.Path(control) / f"{label}-ready-{os.getpid()}-{time.monotonic_ns()}"
    started = time.monotonic_ns()
    process = subprocess.Popen(
        [probe, "portal-mount", "--endpoint", endpoint, "--credential-file", credential,
         "--mount", str(mount), "--ready-file", str(ready)],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.PIPE,
        text=True,
    )
    deadline = time.monotonic() + 10
    while not ready.exists():
        if process.poll() is not None:
            raise RuntimeError(f"portal mount exited: {process.stderr.read()}")
        if time.monotonic() >= deadline:
            process.kill()
            raise RuntimeError("portal mount readiness timed out")
        time.sleep(0.002)
    return mount, ready, process, (time.monotonic_ns() - started) / 1_000_000


def stop_mount(mount, ready, process):
    command = shutil.which("fusermount3") or shutil.which("fusermount")
    if not command:
        raise RuntimeError("fusermount is missing")
    subprocess.run([command, "-u", str(mount)], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
    try:
        process.wait(timeout=2)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=2)
        raise RuntimeError("portal mount did not terminate after unmount")
    ready.unlink(missing_ok=True)
    mount.rmdir()


def atomic_replace(path, value):
    temporary = path.with_name(path.name + f".tmp.{os.getpid()}")
    with open(temporary, "w", encoding="utf-8") as stream:
        stream.write(value + "\n")
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, path)
    descriptor = os.open(path.parent, os.O_RDONLY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


if mode == "attach":
    mount, ready, process, _ = start_mount("attach-warmup")
    stop_mount(mount, ready, process)
    for sample in range(1, 31):
        mount, ready, process, elapsed = start_mount(f"attach-{sample}")
        print(f"mount-ready-warm\t{elapsed:.6f}", flush=True)
        stop_mount(mount, ready, process)
elif mode == "filesystem":
    mount, ready, process, _ = start_mount("filesystem")
    try:
        subprocess.run(["python3", "/workspace/workload.py", str(mount), "30"], check=True)
    finally:
        stop_mount(mount, ready, process)
elif mode == "first-byte":
    mount, ready, process, _ = start_mount("first-byte")
    try:
        value = (mount / "git" / "src" / "00" / "file-00.txt").read_text(encoding="utf-8")
        if value != "00/00\n":
            raise RuntimeError("first-byte fixture changed")
        print("workspace-ready", flush=True)
    finally:
        stop_mount(mount, ready, process)
elif mode == "atomic":
    mount, ready, process, _ = start_mount("atomic")
    pathlib.Path(control, "atomic-ready").write_text("ready\n", encoding="utf-8")
    try:
        for sample in range(1, 31):
            expected = f"host-{sample}"
            trigger = pathlib.Path(control, f"host-go-{sample}")
            while not trigger.exists():
                time.sleep(0.001)
            deadline = time.monotonic() + 3
            while (mount / "atomic" / "host-value.txt").read_text(encoding="utf-8").strip() != expected:
                if time.monotonic() >= deadline:
                    raise RuntimeError(f"host mutation {sample} did not converge")
                time.sleep(0.001)
            pathlib.Path(control, f"host-seen-{sample}").write_text("seen\n", encoding="utf-8")

            go = pathlib.Path(control, f"guest-go-{sample}")
            while not go.exists():
                time.sleep(0.001)
            pathlib.Path(control, f"guest-ready-{sample}").write_text("ready\n", encoding="utf-8")
            acknowledge = pathlib.Path(control, f"guest-ack-{sample}")
            while not acknowledge.exists():
                time.sleep(0.001)
            atomic_replace(mount / "atomic" / "guest-value.txt", f"guest-{sample}")
            pathlib.Path(control, f"guest-done-{sample}").write_text("done\n", encoding="utf-8")
    finally:
        stop_mount(mount, ready, process)
elif mode == "saturation":
    mount, ready, process, _ = start_mount("saturation")
    try:
        scanner = subprocess.Popen([
            "python3", "-c",
            "import os,sys; root=sys.argv[1]; [os.stat(os.path.join(d,n),follow_symlinks=False) for d,_,fs in os.walk(root) for n in fs]",
            str(mount / "package" / "node_modules"),
        ])
        samples = []
        target = mount / "git" / ".git" / "HEAD"
        for _ in range(100):
            started = time.monotonic_ns()
            target.stat()
            samples.append((time.monotonic_ns() - started) / 1_000_000)
        if scanner.wait(timeout=30) != 0:
            raise RuntimeError("saturation scanner failed")
        for value in samples:
            print(f"saturation-metadata\t{value:.6f}", flush=True)
    finally:
        started = time.monotonic_ns()
        stop_mount(mount, ready, process)
        print(f"saturation-teardown\t{(time.monotonic_ns() - started) / 1_000_000:.6f}", flush=True)
else:
    raise SystemExit(f"unknown Portal performance mode: {mode}")
PY

"$host_probe" portal-serve \
  --root "$host_root" \
  --control-dir "$control" \
  --guest-host host.lima.internal \
  --ttl 45m \
  >"$artifact_dir/raw/portal-server.log" 2>&1 &
server_pid=$!
for _ in $(seq 1 200); do
  [[ -f "$control/ready" ]] && break
  kill -0 "$server_pid" 2>/dev/null || {
    cat "$artifact_dir/raw/portal-server.log" >&2
    exit 1
  }
  sleep 0.05
done
[[ -f "$control/ready" && "$(stat -f '%Lp' "$control/credential.bin")" == 600 ]] || {
  echo "workspace Portal performance server did not become ready" >&2
  exit 1
}
endpoint="$(cat "$control/endpoint.txt")"
credential=/workspace/control/credential.bin

run_args=(
  hideout run
  --backend lima
  --network direct
  --workspace "$bootstrap"
  --guest-workspace /workspace
  --terminal never
  --
)

"${run_args[@]}" /workspace/hold.sh \
  >"$artifact_dir/raw/hold.stdout" 2>"$artifact_dir/raw/hold.stderr" &
hold_pid=$!
for _ in $(seq 1 1200); do
  [[ -f "$bootstrap/session-ready" ]] && break
  kill -0 "$hold_pid" 2>/dev/null || {
    cat "$artifact_dir/raw/hold.stderr" >&2
    exit 1
  }
  sleep 0.05
done
[[ -f "$bootstrap/session-ready" ]] || { echo "Portal performance hold session was not ready" >&2; exit 1; }

"${run_args[@]}" python3 /workspace/portal_perf.py attach "$endpoint" "$credential" /workspace/control \
  >"$artifact_dir/raw/attach-warm.tsv"
"${run_args[@]}" python3 /workspace/portal_perf.py filesystem "$endpoint" "$credential" /workspace/control \
  >"$artifact_dir/raw/filesystem-warm.tsv"

measure_first_byte() {
  python3 - "$@" <<'PY'
import json
import subprocess
import sys
import time

started = time.monotonic_ns()
process = subprocess.Popen(sys.argv[1:], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
line = process.stdout.readline()
first = time.monotonic_ns()
stdout_tail, stderr = process.communicate()
if process.returncode != 0:
    sys.stderr.buffer.write(stderr)
    raise SystemExit(process.returncode)
if line != b"workspace-ready\n":
    raise SystemExit(f"unexpected first target output: {line!r}")
print(json.dumps({
    "firstByteMs": (first - started) / 1_000_000,
    "stderr": stderr.decode("utf-8", "replace"),
}, separators=(",", ":")))
PY
}

measure_first_byte "${run_args[@]}" python3 /workspace/portal_perf.py first-byte "$endpoint" "$credential" /workspace/control >/dev/null
for sample in $(seq 1 30); do
  measure_first_byte "${run_args[@]}" python3 /workspace/portal_perf.py first-byte "$endpoint" "$credential" /workspace/control \
    | jq -c --argjson sample "$sample" '. + {sample:$sample}' \
    >>"$artifact_dir/raw/first-byte-warm.jsonl"
done

"${run_args[@]}" python3 /workspace/portal_perf.py atomic "$endpoint" "$credential" /workspace/control \
  >"$artifact_dir/raw/atomic.stdout" 2>"$artifact_dir/raw/atomic.stderr" &
atomic_pid=$!
for _ in $(seq 1 400); do
  [[ -f "$control/atomic-ready" ]] && break
  kill -0 "$atomic_pid" 2>/dev/null || {
    cat "$artifact_dir/raw/atomic.stderr" >&2
    exit 1
  }
  sleep 0.01
done
[[ -f "$control/atomic-ready" ]] || { echo "Portal atomic probe was not ready" >&2; exit 1; }

: >"$artifact_dir/raw/atomic-host-to-guest.values"
: >"$artifact_dir/raw/atomic-guest-to-host.values"
for sample in $(seq 1 30); do
  host_started_ns="$(python3 -c 'import time; print(time.time_ns())')"
  printf 'host-%s\n' "$sample" >"$host_root/atomic/host-value.next"
  mv "$host_root/atomic/host-value.next" "$host_root/atomic/host-value.txt"
  printf 'go\n' >"$control/host-go-$sample"
  for _ in $(seq 1 3000); do
    [[ -f "$control/host-seen-$sample" ]] && break
    sleep 0.001
  done
  [[ -f "$control/host-seen-$sample" ]] || { echo "host mutation sample $sample did not converge" >&2; exit 1; }
  host_finished_ns="$(python3 -c 'import time; print(time.time_ns())')"
  python3 - "$host_started_ns" "$host_finished_ns" <<'PY' >>"$artifact_dir/raw/atomic-host-to-guest.values"
import sys
print(f"{(int(sys.argv[2]) - int(sys.argv[1])) / 1_000_000:.6f}")
PY

  printf 'go\n' >"$control/guest-go-$sample"
  for _ in $(seq 1 3000); do
    [[ -f "$control/guest-ready-$sample" ]] && break
    sleep 0.001
  done
  [[ -f "$control/guest-ready-$sample" ]] || { echo "guest mutation sample $sample did not become ready" >&2; exit 1; }
  guest_started_ns="$(python3 -c 'import time; print(time.time_ns())')"
  printf 'go\n' >"$control/guest-ack-$sample"
  for _ in $(seq 1 3000); do
    [[ "$(cat "$host_root/atomic/guest-value.txt" 2>/dev/null || true)" == "guest-$sample" ]] && break
    sleep 0.001
  done
  [[ "$(cat "$host_root/atomic/guest-value.txt")" == "guest-$sample" ]] || { echo "guest mutation sample $sample did not converge" >&2; exit 1; }
  guest_finished_ns="$(python3 -c 'import time; print(time.time_ns())')"
  python3 - "$guest_started_ns" "$guest_finished_ns" <<'PY' >>"$artifact_dir/raw/atomic-guest-to-host.values"
import sys
print(f"{(int(sys.argv[2]) - int(sys.argv[1])) / 1_000_000:.6f}")
PY
done
wait "$atomic_pid"
atomic_pid=""

"${run_args[@]}" python3 /workspace/portal_perf.py saturation "$endpoint" "$credential" /workspace/control \
  >"$artifact_dir/raw/saturation.tsv"

awk -F '\t' '$1 == "git-status-warm" { print $2 }' "$artifact_dir/raw/filesystem-warm.tsv" >"$artifact_dir/raw/git-status.values"
awk -F '\t' '$1 == "package-scan-warm" { print $2 }' "$artifact_dir/raw/filesystem-warm.tsv" >"$artifact_dir/raw/package-scan.values"
awk -F '\t' '$1 == "mount-ready-warm" { print $2 }' "$artifact_dir/raw/attach-warm.tsv" >"$artifact_dir/raw/mount-ready.values"
jq -r '.firstByteMs' "$artifact_dir/raw/first-byte-warm.jsonl" >"$artifact_dir/raw/first-byte.values"
awk -F '\t' '$1 == "saturation-metadata" { print $2 }' "$artifact_dir/raw/saturation.tsv" >"$artifact_dir/raw/saturation-metadata.values"

for metric in git-status package-scan mount-ready first-byte atomic-host-to-guest atomic-guest-to-host saturation-metadata; do
  workspace_summarize_samples "$artifact_dir/raw/$metric.values" "$artifact_dir/$metric-summary.json"
done

python3 - "$baseline_root" "$artifact_dir" <<'PY'
import json
import pathlib
import sys

baseline = pathlib.Path(sys.argv[1])
candidate = pathlib.Path(sys.argv[2])

def load(path):
    with open(path, encoding="utf-8") as stream:
        return json.load(stream)

base = {
    "git-status": load(baseline / "git-status-summary.json"),
    "package-scan": load(baseline / "package-scan-summary.json"),
    "first-byte": load(baseline / "first-byte-summary.json"),
}
current = {name: load(candidate / f"{name}-summary.json") for name in (
    "git-status", "package-scan", "atomic-host-to-guest", "atomic-guest-to-host",
    "mount-ready", "first-byte",
)}
passed = {
    "git-status": current["git-status"]["medianMs"] <= 2000 and current["git-status"]["medianMs"] <= 2 * base["git-status"]["medianMs"],
    "package-scan": current["package-scan"]["medianMs"] <= 3 * base["package-scan"]["medianMs"],
    "atomic-host-to-guest": current["atomic-host-to-guest"]["p95Ms"] <= 250,
    "atomic-guest-to-host": current["atomic-guest-to-host"]["p95Ms"] <= 250,
    "mount-ready": current["mount-ready"]["p95Ms"] <= 1000,
    "first-byte": current["first-byte"]["p95Ms"] <= base["first-byte"]["p95Ms"] + max(500, .15 * base["first-byte"]["p95Ms"]),
}
metrics = []
for name in ("git-status", "package-scan", "atomic-host-to-guest", "atomic-guest-to-host", "mount-ready", "first-byte"):
    metric = {"id": name, "candidate": current[name], "passed": passed[name]}
    if name in base:
        metric["baseline"] = base[name]
    metrics.append(metric)
with open(candidate / "performance.json", "w", encoding="utf-8") as stream:
    json.dump({
        "schema": "hideout.workspace-portal-performance/v1",
        "result": "passed" if all(passed.values()) else "failed",
        "thresholdsPassed": all(passed.values()),
        "metrics": metrics,
        "saturation": {
            "metadata": load(candidate / "saturation-metadata-summary.json"),
            "teardownMs": float(next(line.split("\t")[1] for line in open(candidate / "raw/saturation.tsv", encoding="utf-8") if line.startswith("saturation-teardown\t"))),
        },
    }, stream, indent=2)
    stream.write("\n")
PY

git_dirty=false
[[ -z "$(git -C "$repo_root" status --porcelain=v1 --untracked-files=all)" ]] || git_dirty=true
jq -n \
  --arg schema "hideout.workspace-portal-performance-provenance/v1" \
  --arg commit "$(git -C "$repo_root" rev-parse HEAD)" \
  --argjson dirty "$git_dirty" \
  --arg fixtureDigest "$fixture_digest" \
  --arg hostArch "$(uname -m)" \
  --arg macosVersion "$(sw_vers -productVersion)" \
  --arg limaVersion "$(limactl --version | head -n 1)" \
  '{schema:$schema,commit:$commit,dirty:$dirty,fixtureDigest:$fixtureDigest,hostArch:$hostArch,macosVersion:$macosVersion,limaVersion:$limaVersion}' \
  >"$artifact_dir/provenance.json"

touch "$bootstrap/stop"
wait "$hold_pid"
hold_pid=""
printf 'workspace Portal performance evidence: %s (%s)\n' "$artifact_dir" "$(jq -r .result "$artifact_dir/performance.json")"
