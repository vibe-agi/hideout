#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
  echo "usage: scripts/test-workspace-path-identity-lima.sh <absolute-artifact-dir>" >&2
  exit 2
}

[[ $# -eq 1 ]] || usage
artifact_dir="$1"
[[ "$artifact_dir" == /* ]] || usage
[[ ! -e "$artifact_dir" ]] || {
  echo "workspace path-identity artifact directory already exists: $artifact_dir" >&2
  exit 2
}
for tool in go git hideout jq limactl python3 ssh; do
  command -v "$tool" >/dev/null 2>&1 || {
    echo "workspace path-identity probe requires $tool" >&2
    exit 2
  }
done
[[ "$(uname -s)" == "Darwin" && "$(uname -m)" == "arm64" ]] || {
  echo "workspace path-identity Lima probe requires macOS arm64" >&2
  exit 2
}

mkdir -p "$repo_root/.tmp"
work_root="$(mktemp -d "$repo_root/.tmp/workspace-path-identity.XXXXXX")"
bootstrap="$work_root/bootstrap"
host_probe="$work_root/hideout-workspace-probe-host"
guest_probe="$bootstrap/hideout-workspace-probe"
hold_pid=""
server_pids=()

cleanup() {
  touch "$bootstrap/stop" 2>/dev/null || true
  if [[ -n "$hold_pid" ]]; then
    wait "$hold_pid" >/dev/null 2>&1 || true
  fi
  for pid in "${server_pids[@]}"; do
    kill "$pid" >/dev/null 2>&1 || true
    wait "$pid" >/dev/null 2>&1 || true
  done
  find "$work_root" -depth -delete 2>/dev/null || true
}
trap cleanup EXIT

mkdir -p "$artifact_dir/raw" "$bootstrap"
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

cat >"$bootstrap/path_probe.py" <<'PY'
import hashlib
import json
import os
import pathlib
import socket
import subprocess
import sys

workspace_id, output = sys.argv[1:]
physical = f"/hideout/workspaces/{workspace_id}"
base_env = {
    "HOME": "/home/developer",
    "USER": "developer",
    "LOGNAME": "developer",
    "PWD": "/workspace",
    "PATH": "/opt/hideout-fixtures:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin",
}

def run(argv, check=True):
    result = subprocess.run(argv, cwd="/workspace", env=base_env, text=True,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if check and result.returncode != 0:
        raise RuntimeError(f"{argv!r} failed: {result.stderr.strip()}")
    return result

def one_line(value):
    return " ".join(value.strip().split())[:512]

def shell_physical(command):
    return run(["bash", "--noprofile", "--norc", "-c", command]).stdout.strip()

logical_pwd = run(["bash", "--noprofile", "--norc", "-c", "pwd -L"]).stdout.strip()
after_cd_dot = shell_physical("cd . && pwd -P")
after_cd_logical = shell_physical("cd /workspace && pwd -P")
subprocess_cwd = shell_physical("python3 -c 'import os; print(os.getcwd())'")
shell_reentry_cwd = shell_physical(
    "exec bash --noprofile --norc -c 'cd /workspace && pwd -P'"
)

safe_result = run(["git", "config", "--global", "--get-all", "safe.directory"], check=False)
if safe_result.returncode not in (0, 1):
    raise RuntimeError(safe_result.stderr.strip())
safe_directories = [line for line in safe_result.stdout.splitlines() if line]
unbound = run(["git", "-C", "/hideout/workspaces/ws_unbound_fixture", "status"], check=False)
if unbound.returncode == 0 or "dubious ownership" not in unbound.stderr:
    raise RuntimeError("unbound physical Git fixture was not rejected as dubious ownership")

versions = {
    "bash": one_line(run(["bash", "--version"]).stdout.splitlines()[0]),
    "git": one_line(run(["git", "--version"]).stdout),
    "node": one_line(run(["node", "--version"]).stdout),
    "python": one_line(run(["python3", "--version"]).stdout),
    "go": one_line(run(["go", "version"]).stdout),
    "claude": one_line(run(["claude", "--version"]).stdout),
    "codex": one_line(run(["codex", "--version"]).stdout),
}
project_keys = {
    "bash": shell_physical("pwd -P"),
    "git": run(["git", "rev-parse", "--show-toplevel"]).stdout.strip(),
    "node": run(["node", "-p", "process.cwd()"]).stdout.strip(),
    "python": run(["python3", "-c", "import os; print(os.getcwd())"]).stdout.strip(),
    "go": str(pathlib.PurePosixPath(run(["go", "env", "GOMOD"]).stdout.strip()).parent),
    "claude": os.getcwd(),
    "codex": os.getcwd(),
}

observations = []
for tool in ("bash", "git", "node", "python", "go", "claude", "codex"):
    fixture = tool in ("claude", "codex")
    observation = {
        "tool": tool,
        "version": versions[tool],
        "logicalPWD": logical_pwd,
        "physicalCWD": os.getcwd(),
        "projectKey": project_keys[tool],
        "projectKeyMode": "logical-pwd-alias" if tool == "go" else "physical-cwd",
        "representativeFixture": fixture,
        "afterCdDot": after_cd_dot,
        "afterCdLogical": after_cd_logical,
        "subprocessCWD": subprocess_cwd,
        "shellReentryCWD": shell_reentry_cwd,
    }
    if fixture:
        state_key = hashlib.sha256((tool + "\0" + os.getcwd()).encode()).hexdigest()
        state = pathlib.Path.home() / ".h035" / tool / state_key[:16]
        state.mkdir(parents=True, exist_ok=True)
        (state / "history.jsonl").write_text(json.dumps({"cwd": os.getcwd()}) + "\n")
        (state / "cache").mkdir(exist_ok=True)
        socket_path = state / "s"
        socket_path.unlink(missing_ok=True)
        endpoint = socket.socket(socket.AF_UNIX)
        endpoint.bind(str(socket_path))
        endpoint.close()
        observation.update({
            "projectStateKey": state_key,
            "historyState": (state / "history.jsonl").is_file(),
            "cacheState": (state / "cache").is_dir(),
            "socketState": socket_path.exists(),
        })
    observations.append(observation)

with open(output, "w", encoding="utf-8") as stream:
    json.dump({
        "schema": "hideout.workspace-path-identity-input/v2",
        "workspaceId": workspace_id,
        "gitSafeDirectories": safe_directories,
        "unboundGitRejected": True,
        "observations": observations,
    }, stream, indent=2)
    stream.write("\n")
PY

cat >"$bootstrap/root-path-check.sh" <<'GUEST'
#!/bin/bash
set -euo pipefail

workspace_id="$1"
control_name="$2"
mount_root="/hideout/workspaces/$workspace_id"
unbound_root="/hideout/workspaces/ws_unbound_fixture"
bootstrap_shadow="/tmp/hideout-path-bootstrap-$workspace_id"
synthetic_root="/tmp/hideout-path-root-$workspace_id"
portal_pid=""

cleanup_probe() {
  if mountpoint -q "$synthetic_root"; then
    umount -R "$synthetic_root" 2>/dev/null || true
  fi
  if mountpoint -q "$mount_root"; then
    fusermount3 -u "$mount_root" 2>/dev/null || umount "$mount_root" 2>/dev/null || true
  fi
  if [ -n "$portal_pid" ]; then
    kill "$portal_pid" 2>/dev/null || true
    wait "$portal_pid" 2>/dev/null || true
  fi
  if mountpoint -q "$bootstrap_shadow"; then
    umount "$bootstrap_shadow" 2>/dev/null || true
  fi
  find "$synthetic_root" "$bootstrap_shadow" "$unbound_root" "$mount_root" -depth -delete 2>/dev/null || true
}
trap cleanup_probe EXIT INT TERM

mount --make-rprivate /
mkdir -p "$bootstrap_shadow"
mount --bind /workspace "$bootstrap_shadow"
mount --make-private "$bootstrap_shadow"
control="$bootstrap_shadow/$control_name"

mkdir -p /hideout/workspaces "$mount_root"
"$bootstrap_shadow/hideout-workspace-probe" portal-mount \
  --endpoint "$(cat "$control/endpoint.txt")" \
  --credential-file "$control/credential.bin" \
  --mount "$mount_root" \
  --ready-file "$control/path-mount-ready" \
  --allow-other --uid 1000 --gid 1000 \
  >"$control/path-mount.log" 2>&1 &
portal_pid=$!
for _ in $(seq 1 200); do
  mountpoint -q "$mount_root" && break
  kill -0 "$portal_pid" 2>/dev/null || {
    cat "$control/path-mount.log" >&2
    exit 1
  }
  sleep 0.05
done
mountpoint -q "$mount_root" || { echo 'path identity mount was not ready' >&2; exit 1; }

mkdir -p "$unbound_root"
git -C "$unbound_root" init -q
printf 'unbound\n' >"$unbound_root/value.txt"

mkdir -p "$synthetic_root"
mount -t tmpfs -o mode=0755,size=64m hideout-path-root "$synthetic_root"
mkdir -p "$synthetic_root"/{dev,etc,hideout,home/developer,opt/hideout-fixtures,proc,tmp,usr}
mount --rbind /dev "$synthetic_root/dev"
mount --make-rslave "$synthetic_root/dev"
mount --bind /etc "$synthetic_root/etc"
mount --rbind /hideout "$synthetic_root/hideout"
mount --make-rslave "$synthetic_root/hideout"
mount --bind /proc "$synthetic_root/proc"
mount --bind /usr "$synthetic_root/usr"
ln -s usr/bin "$synthetic_root/bin"
ln -s usr/sbin "$synthetic_root/sbin"
ln -s usr/lib "$synthetic_root/lib"
[ ! -e /lib64 ] || ln -s usr/lib64 "$synthetic_root/lib64"
ln -s "$mount_root" "$synthetic_root/workspace"
cp "$bootstrap_shadow/path_probe.py" "$synthetic_root/opt/path_probe.py"
chmod 0555 "$synthetic_root/opt/path_probe.py"
chown 1000:1000 "$synthetic_root/home/developer" "$synthetic_root/tmp"
chmod 0700 "$synthetic_root/home/developer"

for tool in claude codex; do
  cat >"$synthetic_root/opt/hideout-fixtures/$tool" <<FIXTURE
#!/bin/sh
case "\${1-}" in
  --version) printf '%s\n' '$tool representative-project-state/v2' ;;
  *) exit 64 ;;
esac
FIXTURE
  chmod 0555 "$synthetic_root/opt/hideout-fixtures/$tool"
done

chroot "$synthetic_root" /usr/bin/bash --noprofile --norc -c '
  cd /workspace
  exec /usr/bin/setpriv --reuid=developer --regid=developer --init-groups -- \
    /usr/bin/env -i HOME=/home/developer USER=developer LOGNAME=developer \
    PATH=/opt/hideout-fixtures:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin \
    /usr/bin/python3 /opt/path_probe.py "$1" /tmp/path-input.json
' hideout-path-target "$workspace_id"
cp "$synthetic_root/tmp/path-input.json" "$control/path-input.json"
chmod 0600 "$control/path-input.json"
GUEST
chmod 0700 "$bootstrap/root-path-check.sh"

workspace_ids=(
  wrk_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  wrk_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
)
for index in 0 1; do
  control="$bootstrap/control-$index"
  host_root="$work_root/host-root-$index"
  mkdir -p "$control" "$host_root"
  git -C "$host_root" init -q
  git -C "$host_root" config user.name 'Hideout Research'
  git -C "$host_root" config user.email 'research@invalid.example'
  printf 'module example.invalid/workspace%d\n\ngo 1.25\n' "$index" >"$host_root/go.mod"
  printf '{"name":"workspace-%d","private":true}\n' "$index" >"$host_root/package.json"
  printf 'workspace-%d\n' "$index" >"$host_root/README.md"
  git -C "$host_root" add .
  git -C "$host_root" commit -qm 'fixture'

  "$host_probe" portal-serve \
    --root "$host_root" \
    --control-dir "$control" \
    --guest-host host.lima.internal \
    --ttl 30m \
    >"$artifact_dir/raw/portal-server-$index.log" 2>&1 &
  server_pids+=("$!")
done

for index in 0 1; do
  control="$bootstrap/control-$index"
  for _ in $(seq 1 200); do
    [[ -f "$control/ready" ]] && break
    sleep 0.05
  done
  [[ -f "$control/ready" && "$(stat -f '%Lp' "$control/credential.bin")" == 600 ]] || {
    echo "path identity Portal server $index did not become ready" >&2
    exit 1
  }
done

hideout run \
  --backend lima \
  --network direct \
  --workspace "$bootstrap" \
  --guest-workspace /workspace \
  --terminal never \
  -- /workspace/hold.sh \
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
[[ -f "$bootstrap/session-ready" ]] || {
  echo "path identity hold session did not become ready" >&2
  exit 1
}

instance="$(limactl list --json | jq -sr --arg root "$bootstrap" '
  [.[] | select(.status == "Running" and any(.config.mounts[]?; .location == $root))]
  | if length == 1 then .[0].name else error("expected exactly one running probe instance") end
')"
ssh_config="$HOME/.lima/$instance/ssh.config"
ssh_alias="lima-$instance"
[[ -r "$ssh_config" ]] || { echo "missing Lima SSH config for $instance" >&2; exit 1; }

for index in 0 1; do
  workspace_id="${workspace_ids[$index]}"
  ssh -F "$ssh_config" -o ControlMaster=no -o ControlPath=none -o User=root \
    "$ssh_alias" -- \
    unshare --mount --pid --fork --kill-child=KILL --mount-proc=/proc -- \
    /workspace/root-path-check.sh "$workspace_id" "control-$index" \
    >"$artifact_dir/raw/root-path-$index.stdout" 2>"$artifact_dir/raw/root-path-$index.stderr"
  cp "$bootstrap/control-$index/path-input.json" "$artifact_dir/raw/path-input-$index.json"
  "$host_probe" path-identity \
    --input "$bootstrap/control-$index/path-input.json" \
    --output "$artifact_dir/path-identity-$index.json"
  jq -e --arg id "$workspace_id" '
    .schema == "hideout.workspace-path-identity/v2" and
    .result == "passed" and .workspaceId == $id and
    .logicalRoot == "/workspace" and
    .physicalRoot == ("/hideout/workspaces/" + $id) and
    .unboundGitRejected == true and
    ([.observations[] | select(.representativeFixture == true) | .tool] | sort) == ["claude","codex"]
  ' "$artifact_dir/path-identity-$index.json" >/dev/null
done

python3 - "$artifact_dir/path-identity-0.json" "$artifact_dir/path-identity-1.json" <<'PY'
import json
import sys

first, second = (json.load(open(path, encoding="utf-8")) for path in sys.argv[1:])
first_tools = {item["tool"]: item for item in first["observations"]}
second_tools = {item["tool"]: item for item in second["observations"]}
for tool in ("bash", "git", "node", "python", "claude", "codex"):
    if first_tools[tool]["projectKey"] == second_tools[tool]["projectKey"]:
        raise RuntimeError(f"{tool} merged distinct workspace project keys")
for result in (first_tools, second_tools):
    go = result["go"]
    if go["projectKey"] != "/workspace" or go["projectKeyMode"] != "logical-pwd-alias":
        raise RuntimeError("Go logical PWD behavior was not classified")
for tool in ("claude", "codex"):
    if first_tools[tool]["projectStateKey"] == second_tools[tool]["projectStateKey"]:
        raise RuntimeError(f"{tool} merged distinct representative state")
PY

first_root="$(jq -r .physicalRoot "$artifact_dir/path-identity-0.json")"
second_root="$(jq -r .physicalRoot "$artifact_dir/path-identity-1.json")"
[[ "$first_root" != "$second_root" ]] || {
  echo "distinct workspace IDs collapsed to one physical root" >&2
  exit 1
}
if rg -n '/Users/' "$artifact_dir"; then
  echo "path identity evidence contains a host path" >&2
  exit 1
fi

git_dirty=false
[[ -z "$(git -C "$repo_root" status --porcelain=v1 --untracked-files=all)" ]] || git_dirty=true
jq -n \
  --arg schema "hideout.workspace-path-identity-summary/v2" \
  --arg commit "$(git -C "$repo_root" rev-parse HEAD)" \
  --argjson dirty "$git_dirty" \
  --argjson distinct "$([[ "$first_root" != "$second_root" ]] && echo true || echo false)" \
  '{
    schema:$schema,
    result:"passed",
    commit:$commit,
    dirty:$dirty,
    mechanism:"session-private synthetic root with logical symlink to control-owned Portal FUSE mount",
    logicalRoot:"/workspace",
    distinctPhysicalRoots:$distinct,
    goProjectKeyMode:"logical-pwd-alias",
    representativeProjectState:["history","cache","unix-socket"],
    realTools:["bash","git","node","python","go"],
    representativeFixtures:["claude","codex"],
    hostPathsInEvidence:false
  }' >"$artifact_dir/path-identity-summary.json"

touch "$bootstrap/stop"
wait "$hold_pid"
hold_pid=""
printf 'workspace path identity Lima probe passed: %s\n' "$artifact_dir"
