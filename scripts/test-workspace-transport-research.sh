#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/workspace-research.sh
source "$repo_root/scripts/lib/workspace-research.sh"

usage() {
  cat >&2 <<'EOF'
usage: scripts/test-workspace-transport-research.sh baseline|portal <artifact-dir>

Records either the static virtiofs control distribution or the Portal candidate
distribution used by feature 035. The artifact directory must be empty or
absent.
EOF
  exit 2
}

[[ $# -eq 2 ]] || usage
mode="$1"
artifact_dir="$2"
if [[ "$mode" == "portal" ]]; then
  exec "$repo_root/scripts/lib/workspace-portal-performance.sh" "$artifact_dir"
fi
[[ "$mode" == "baseline" ]] || usage
[[ "$artifact_dir" == /* ]] || {
  echo "workspace research artifact directory must be absolute" >&2
  exit 2
}
if [[ -e "$artifact_dir" ]]; then
  echo "workspace research artifact directory already exists: $artifact_dir" >&2
  exit 2
fi

command -v hideout >/dev/null 2>&1 || {
  echo "workspace research requires an installed hideout binary" >&2
  exit 2
}
command -v python3 >/dev/null 2>&1 || {
  echo "workspace research requires host python3 for monotonic timing" >&2
  exit 2
}

fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/hideout-workspace-baseline.XXXXXX")"
research_env_name=""
cleanup() {
  sleep 4
  if [[ -z "$research_env_name" ]]; then
    research_env_name="$(hideout env list 2>/dev/null | awk -F '\t' -v marker="$(basename "$fixture_root")" 'NR > 1 && !found && index($10, marker) != 0 { print $1; found = 1 }')"
  fi
  if [[ -n "$research_env_name" ]]; then
    hideout env remove "$research_env_name" >/dev/null 2>&1 || true
  fi
  find "$fixture_root" -depth -delete 2>/dev/null || true
}
trap cleanup EXIT

mkdir -p "$artifact_dir/raw" "$fixture_root/git" "$fixture_root/package" "$fixture_root/atomic"
"$repo_root/test/fixtures/workspaceattach/generate.sh" git-10k "$fixture_root/git"
"$repo_root/test/fixtures/workspaceattach/generate.sh" package-20k "$fixture_root/package"

cp "$repo_root/test/fixtures/workspaceattach/workload.py" "$fixture_root/workload.py"

fixture_digest="$(workspace_tree_digest "$fixture_root")"
printf '%s\n' "$fixture_digest" >"$artifact_dir/fixture.sha256"

run_args=(
  hideout run
  --backend lima
  --network direct
  --workspace "$fixture_root"
  --guest-workspace /workspace
  --terminal never
  --
)

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
ended = time.monotonic_ns()
if process.returncode != 0:
    sys.stderr.buffer.write(stderr)
    raise SystemExit(process.returncode)
if line != b"workspace-ready\n":
    raise SystemExit(f"unexpected first target output: {line!r}")
print(json.dumps({
    "firstByteMs": (first - started) / 1_000_000,
    "totalMs": (ended - started) / 1_000_000,
    "stderr": stderr.decode("utf-8", "replace"),
}, separators=(",", ":")))
PY
}

# The first command owns the cold control observation. It also validates the
# exact fixture before warm samples are admitted.
measure_first_byte "${run_args[@]}" /bin/sh -c \
  'test "$(find /workspace/git/src -type f | wc -l)" -eq 10000 && printf "workspace-ready\n"' \
  >"$artifact_dir/raw/first-byte-cold.json"

research_env_name="$(hideout env list | awk -F '\t' -v marker="$(basename "$fixture_root")" 'NR > 1 && !found && index($10, marker) != 0 { print $1; found = 1 }')"
hideout env list >"$artifact_dir/raw/environment-list.txt"
if [[ -n "$research_env_name" ]]; then
  hideout env inspect "$research_env_name" >"$artifact_dir/raw/environment.txt"
fi
"${run_args[@]}" python3 -c '
import json, platform, subprocess
def output(*args):
    return subprocess.check_output(args, text=True).strip()
print(json.dumps({
    "git": output("git", "--version"),
    "python": platform.python_version(),
    "kernel": platform.release(),
    "machine": platform.machine(),
    "shell": output("/bin/sh", "--version") if False else "/bin/sh",
}, sort_keys=True, separators=(",", ":")))
' >"$artifact_dir/raw/guest-tool-versions.json"

# Warm the complete CLI/daemon/VM attach path once without recording it.
measure_first_byte "${run_args[@]}" /bin/sh -c 'printf "workspace-ready\n"' >/dev/null
for sample in $(seq 1 30); do
  measure_first_byte "${run_args[@]}" /bin/sh -c 'printf "workspace-ready\n"' \
    | jq -c --argjson sample "$sample" '. + {sample:$sample}' \
    >>"$artifact_dir/raw/first-byte-warm.jsonl"
done

"${run_args[@]}" python3 /workspace/workload.py /workspace 30 \
  >"$artifact_dir/raw/filesystem-warm.tsv"

awk -F '\t' '$1 == "git-status-warm" { print $2 }' \
  "$artifact_dir/raw/filesystem-warm.tsv" >"$artifact_dir/raw/git-status.values"
awk -F '\t' '$1 == "package-scan-warm" { print $2 }' \
  "$artifact_dir/raw/filesystem-warm.tsv" >"$artifact_dir/raw/package-scan.values"
awk -F '\t' '$1 == "atomic-save-operation-warm" { print $2 }' \
  "$artifact_dir/raw/filesystem-warm.tsv" >"$artifact_dir/raw/atomic-save.values"
jq -r '.firstByteMs' "$artifact_dir/raw/first-byte-warm.jsonl" \
  >"$artifact_dir/raw/first-byte.values"

workspace_summarize_samples "$artifact_dir/raw/git-status.values" \
  "$artifact_dir/git-status-summary.json"
workspace_summarize_samples "$artifact_dir/raw/package-scan.values" \
  "$artifact_dir/package-scan-summary.json"
workspace_summarize_samples "$artifact_dir/raw/atomic-save.values" \
  "$artifact_dir/atomic-save-operation-summary.json"
workspace_summarize_samples "$artifact_dir/raw/first-byte.values" \
  "$artifact_dir/first-byte-summary.json"

git_dirty=false
if [[ -n "$(git -C "$repo_root" status --porcelain=v1 --untracked-files=all)" ]]; then
  git_dirty=true
fi

jq -n \
  --arg schema "hideout.workspace-research-baseline/v1" \
  --arg candidate "static-virtiofs" \
  --arg commit "$(git -C "$repo_root" rev-parse HEAD)" \
  --argjson dirty "$git_dirty" \
  --arg fixtureDigest "$fixture_digest" \
  --arg hostArch "$(uname -m)" \
  --arg macosVersion "$(sw_vers -productVersion)" \
  --arg limaVersion "$(limactl --version | head -n 1)" \
  --arg hideoutVersion "$(hideout version | tr '\n' ' ')" \
  --arg hostGitVersion "$(git --version)" \
  '{
    schema:$schema,
    candidate:$candidate,
    commit:$commit,
    dirty:$dirty,
    fixtureDigest:$fixtureDigest,
    hostArch:$hostArch,
    macosVersion:$macosVersion,
    limaVersion:$limaVersion,
    hideoutVersion:$hideoutVersion,
    hostGitVersion:$hostGitVersion,
    guestToolVersions:"raw/guest-tool-versions.json",
    samples:{
      gitStatus:"git-status-summary.json",
      packageScan:"package-scan-summary.json",
      atomicSaveOperation:"atomic-save-operation-summary.json",
      firstByte:"first-byte-summary.json",
      coldFirstByte:"raw/first-byte-cold.json"
    }
  }' >"$artifact_dir/baseline.json"

echo "workspace static virtiofs baseline: $artifact_dir"
