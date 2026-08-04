#!/usr/bin/env bash

# Independent real-product workspace path and correctness stage for feature 035.
# The caller owns the installed candidate, isolated store/Lima home, and output.

gate2_035_path_required_checks_json() {
  printf '%s\n' '["productionWorkspaceIdentity","logicalPhysicalSameObject","logicalWritePhysicalRead","physicalWriteLogicalRead","atomicRenameAcrossAliases","modeAcrossAliases","flushAcrossAliases","deleteAcrossAliases","repeatedDeleteAcrossAliases","logicalPwdStable","physicalCwdOpaque","nestedCdStable","subprocessCwdOpaque","distinctRootProjectState","sameRootProjectStateStable","siblingPhysicalRootDenied","goLogicalPwdAliasClassified","boundedGitSafeDirectories","preserveModeSharedRejected","externalGitMetadataRejected","resolvedFileAuditLogical","relativeFileAliasExplicit","processAuditLogical","processCwdUnavailableExplicit","physicalArgvCaptureLimitationExplicit","siblingArgvFailClosed","physicalPathAbsentFromActivity"]'
}

gate2_035_path_limitations_json() {
  printf '%s\n' '["process-cwd-unavailable","physical-workspace-argv-exceeds-kernel-capture-width","relative-workspace-file-path-alias"]'
}

gate2_035_path_correctness_judge() {
  local report="$1"
  local required limitations
  required="$(gate2_035_path_required_checks_json)"
  limitations="$(gate2_035_path_limitations_json)"
  jq -e --argjson required "$required" --argjson limitations "$limitations" '
    .schema == "hideout.shared-workspace-path-correctness/v1" and
    .status == "passed" and
    (.tools == ["bash", "claude", "codex", "git", "go", "node", "python"]) and
    (.representativeAgents == ["claude", "codex"]) and
    (.limitations == $limitations) and
    ((.checks | keys) == ($required | sort)) and
    all(.checks[]; . == true)
  ' "$report" >/dev/null
}

gate2_035_path_negative_fixture_judge() {
  local report="$1"
  local required limitations
  required="$(gate2_035_path_required_checks_json)"
  limitations="$(gate2_035_path_limitations_json)"
  jq -e --argjson required "$required" --argjson limitations "$limitations" '
    . as $report |
    .schema == "hideout.shared-workspace-path-correctness/v1" and
    .status == "failed" and
    (.tools == ["bash", "claude", "codex", "git", "go", "node", "python"]) and
    (.representativeAgents == ["claude", "codex"]) and
    (.limitations == $limitations) and
    ((.checks | keys) == ($required | sort)) and
    all($required[];
      if . == "logicalPhysicalSameObject" then
        $report.checks[.] == false
      else
        $report.checks[.] == true
      end)
  ' "$report" >/dev/null
}

gate2_035_path_activity_judge() {
  local activity="$1"
  jq -e '
    (.records // .data.records // []) as $records |
    any($records[]?;
      .kind == "file" and .subject.pathClass == "workspace" and
      (((.subject.path // "") == "/workspace") or
       ((.subject.path // "") | startswith("/workspace/")))) and
    all($records[]? | select(.kind == "file" and .subject.pathClass == "workspace");
      (((.subject.path // "") == "/workspace") or
       ((.subject.path // "") | startswith("/workspace/"))) and
      (((.subject.targetPath // "") | length) == 0 or
       (.subject.targetPath == "/workspace") or
       (.subject.targetPath | startswith("/workspace/")))) and
    ((["metadata", "mkdir", "rename", "rmdir", "unlink"] -
      ([$records[]? | select(
        .kind == "file" and .subject.pathState == "aliased") |
        .operation] | unique)) | length) == 0 and
    any($records[]?;
      .kind == "process" and
      any(.subject.argv[]?; startswith("/workspace"))) and
    all($records[]? | select(.kind == "process");
      if ((.subject.cwd // "") | length) == 0 then
        any(.truncation[]?; . == "cwd-unavailable")
      else
        ((.subject.cwd == "/workspace") or
         (.subject.cwd | startswith("/workspace/")))
      end) and
    any($records[]?;
      .kind == "process" and
      (.subject.argv[0] // "") == "physical-probe" and
      (((.subject.argv[1] // "") == "[UNBOUND_WORKSPACE_PATH]" and
        any(.truncation[]?; . == "argv-truncated")) or
       ((.subject.argv | length) == 1 and
        any(.truncation[]?; . == "argv-unavailable")))) and
    any($records[]?;
      .kind == "process" and
      (.subject.argv[0] // "") == "sibling-probe" and
      (((.subject.argv[1] // "") == "[UNBOUND_WORKSPACE_PATH]" and
        any(.truncation[]?; . == "argv-truncated")) or
       ((.subject.argv | length) == 1 and
        any(.truncation[]?; . == "argv-unavailable")))) and
    ([ $records[]? |
      .subject.path?, .subject.targetPath?, .subject.cwd?,
      .subject.executable?, .subject.argv[]? ] |
      map(select(type == "string") | contains("/hideout/workspaces")) |
      any | not)
  ' "$activity" >/dev/null
}

gate2_shared_workspace_path_correctness() (
  set -euo pipefail

  if [ "$#" -ne 6 ]; then
    echo "usage: gate2_shared_workspace_path_correctness <out> <store> <lima-home> <hideout> <profile> <fixture-root>" >&2
    return 2
  fi
  local out="$1" store="$2" lima_home="$3" hideout="$4" profile="$5" fixture_root="$6"
  local workspace_a="$fixture_root/path-a" workspace_b="$fixture_root/path-b"
  local workspace_external="$fixture_root/path-external-metadata"
  local external_gitdir="$fixture_root/path-external-gitdir"
  local a_pid="" b_pid="" profile_mode_changed=0

  mkdir -p "$out/logs" "$workspace_a" "$workspace_b" "$workspace_external" "$external_gitdir"
  out="$(cd "$out" && pwd -P)"
  fixture_root="$(cd "$fixture_root" && pwd -P)"
  workspace_a="$fixture_root/path-a"
  workspace_b="$fixture_root/path-b"
  workspace_external="$fixture_root/path-external-metadata"
  external_gitdir="$fixture_root/path-external-gitdir"

  gate2_035_path_command() {
    env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$@"
  }

  cat >"$out/logs/path-probe.py" <<'PY'
import hashlib
import json
import os
import pathlib
import socket
import subprocess
import sys

label = sys.argv[1]
mode = sys.argv[2]
logical = pathlib.Path("/workspace")
physical = pathlib.Path(os.getcwd())
physical_text = str(physical)
workspace_id = physical.name
if not physical_text.startswith("/hideout/workspaces/wrk_") or len(workspace_id) != 68:
    raise RuntimeError("physical workspace identity is not production-shaped")
if not workspace_id.startswith("wrk_") or any(c not in "0123456789abcdef" for c in workspace_id[4:]):
    raise RuntimeError("physical workspace identity is malformed")

def output(argv):
    return subprocess.check_output(argv, cwd=logical, text=True, stderr=subprocess.STDOUT).strip()

def first_line(argv):
    return output(argv).splitlines()[0]

project_keys = {
    "bash": output(["bash", "--noprofile", "--norc", "-c", "pwd -P"]),
    "git": output(["git", "rev-parse", "--show-toplevel"]),
    "node": output(["node", "-e", "process.stdout.write(process.cwd())"]),
    "python": output(["python3", "-c", "import os; print(os.getcwd())"]),
}
versions = {
    "bash": first_line(["bash", "--version"]),
    "git": first_line(["git", "--version"]),
    "node": first_line(["node", "--version"]),
    "python": first_line(["python3", "--version"]),
    "go": first_line(["go", "version"]),
}
for tool, key in project_keys.items():
    if key != physical_text:
        raise RuntimeError(f"{tool} project key did not use the opaque cwd")

tools = {
    tool: {"version": versions[tool], "projectKey": project_keys[tool], "representativeFixture": False}
    for tool in project_keys
}
go_module = output(["go", "env", "GOMOD"])
if go_module != "/workspace/go.mod":
    raise RuntimeError("Go PWD behavior changed; review the path identity contract")
tools["go"] = {
    "version": versions["go"],
    "projectKey": go_module,
    "projectKeyMode": "logical-pwd-alias",
    "statefulProjectIdentity": False,
    "representativeFixture": False,
}
safe_result = subprocess.run(
    ["git", "config", "--global", "--get-all", "safe.directory"],
    cwd=logical,
    text=True,
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
    check=False,
)
if safe_result.returncode not in (0, 1):
    raise RuntimeError("Git safe.directory inspection failed")
safe_directories = [line for line in safe_result.stdout.splitlines() if line]
if any(value not in ("/workspace", physical_text) for value in safe_directories):
    raise RuntimeError("Git safe.directory contains an unbound path")
if "*" in safe_directories:
    raise RuntimeError("Git safe.directory contains a wildcard")
state_root = pathlib.Path.home() / ".h035"
for tool in ("claude", "codex"):
    state_key = hashlib.sha256((tool + "\0" + physical_text).encode()).hexdigest()
    state = state_root / tool / state_key[:16]
    state.mkdir(parents=True, exist_ok=True)
    (state / "history.jsonl").write_text(json.dumps({"cwd": physical_text}) + "\n")
    (state / "cache").mkdir(exist_ok=True)
    socket_path = state / "s"
    socket_path.unlink(missing_ok=True)
    endpoint = socket.socket(socket.AF_UNIX)
    endpoint.bind(str(socket_path))
    endpoint.close()
    tools[tool] = {
        "version": f"{tool} representative-project-state/v2",
        "projectKey": physical_text,
        "stateKey": state_key,
        "representativeFixture": True,
        "history": (state / "history.jsonl").is_file(),
        "cache": (state / "cache").is_dir(),
        "socket": socket_path.exists(),
    }

checks = {
    "logicalPhysicalSameObject": False,
    "logicalWritePhysicalRead": False,
    "physicalWriteLogicalRead": False,
    "atomicRenameAcrossAliases": False,
    "modeAcrossAliases": False,
    "flushAcrossAliases": False,
    "deleteAcrossAliases": False,
}
delete_stress_iterations = 0
if mode == "full":
    logical_stat = os.stat(logical)
    physical_stat = os.stat(physical)
    checks["logicalPhysicalSameObject"] = (
        logical_stat.st_dev == physical_stat.st_dev and logical_stat.st_ino == physical_stat.st_ino
    )
    tree_logical = logical / ".hideout-alias-tree"
    tree_physical = physical / ".hideout-alias-tree"
    tree_logical.mkdir()
    logical_file = tree_logical / "logical.txt"
    payload = b"logical-to-physical\n"
    with logical_file.open("wb") as stream:
        stream.write(payload)
        stream.flush()
        os.fsync(stream.fileno())
    checks["logicalWritePhysicalRead"] = (tree_physical / "logical.txt").read_bytes() == payload
    physical_file = tree_physical / "physical.txt"
    physical_file.write_bytes(b"physical-to-logical\n")
    checks["physicalWriteLogicalRead"] = (tree_logical / "physical.txt").read_bytes() == b"physical-to-logical\n"
    renamed = tree_logical / "renamed.txt"
    os.replace(physical_file, renamed)
    checks["atomicRenameAcrossAliases"] = (tree_physical / "renamed.txt").read_bytes() == b"physical-to-logical\n"
    os.chmod(renamed, 0o640)
    checks["modeAcrossAliases"] = (os.stat(tree_physical / "renamed.txt").st_mode & 0o777) == 0o640
    directory_fd = os.open(tree_physical, os.O_RDONLY | os.O_DIRECTORY)
    os.fsync(directory_fd)
    os.close(directory_fd)
    checks["flushAcrossAliases"] = True
    os.unlink(tree_physical / "logical.txt")
    os.unlink(renamed)
    os.rmdir(tree_logical)
    checks["deleteAcrossAliases"] = not tree_physical.exists()
elif mode == "repeat":
    checks = {"repeatedDeleteAcrossAliases": True}
    for index in range(100):
        tree_logical = logical / f".hideout-delete-stress-{index}"
        tree_physical = physical / f".hideout-delete-stress-{index}"
        tree_logical.mkdir()
        (tree_physical / "entry").write_text("delete-stress\n")
        os.unlink(tree_logical / "entry")
        os.rmdir(tree_logical)
        delete_stress_iterations += 1
        if tree_physical.exists():
            checks["repeatedDeleteAcrossAliases"] = False
            break

result = {
    "label": label,
    "mode": mode,
    "workspaceId": workspace_id,
    "physicalRoot": physical_text,
    "gitSafeDirectories": safe_directories,
    "deleteStressIterations": delete_stress_iterations,
    "tools": tools,
    "checks": checks,
}
(logical / f".hideout-path-{label}.json").write_text(json.dumps(result, sort_keys=True) + "\n")
PY

  cat >"$out/logs/sibling-check.py" <<'PY'
import os
import sys

try:
    descriptor = os.open(sys.argv[1], os.O_RDONLY | os.O_DIRECTORY)
except (FileNotFoundError, PermissionError):
    pass
else:
    os.close(descriptor)
    raise RuntimeError("sibling physical workspace was reachable")
PY

  cat >"$out/logs/path-session.sh" <<'SH'
#!/bin/bash
set -eEu
trap 'status=$?; echo "workspace path session failed line=$LINENO command=$BASH_COMMAND status=$status" >&2' ERR
label="$1"
test "$PWD" = /workspace
physical="$(pwd -P)"
test "$(realpath /workspace)" = "$physical"
mkdir -p /workspace/.hideout-nested
cd /workspace/.hideout-nested
test "$PWD" = /workspace/.hideout-nested
test "$(pwd -L)" = /workspace/.hideout-nested
test "$(pwd -P)" = "$physical/.hideout-nested"
cd ..
rmdir /workspace/.hideout-nested
python3 /workspace/.hideout-path-probe.py "$label" full
touch "/workspace/.hideout-path-$label.ready"
while [ ! -f "/workspace/.hideout-path-$label.sibling-root" ]; do sleep 0.02; done
sibling="$(cat "/workspace/.hideout-path-$label.sibling-root")"
python3 /workspace/.hideout-sibling-check.py "$sibling"
for attempt in 1 2 3; do
  HIDEOUT_PATH_PROBE="$sibling" \
    bash -c 'exec -a sibling-probe /bin/true "$HIDEOUT_PATH_PROBE"'
  sleep 0.10
done
python3 -c 'import pathlib,sys; assert pathlib.Path(sys.argv[1]).read_text().strip()' "$physical/README.md"
for attempt in 1 2 3; do
  HIDEOUT_PATH_PROBE="$physical/README.md" \
    bash -c 'exec -a physical-probe /bin/true "$HIDEOUT_PATH_PROBE"'
  sleep 0.10
done
# Let the bounded collector pipeline publish the final controlled probes before
# the host releases the session.
sleep 0.75
printf 'audit-rename\n' > /workspace/.hideout-audit-source
mv "$physical/.hideout-audit-source" /workspace/.hideout-audit-target
rm /workspace/.hideout-audit-target
touch "/workspace/.hideout-path-$label.sibling-denied"
while [ ! -f /workspace/.hideout-path-release ]; do sleep 0.02; done
SH
  chmod 0700 "$out/logs/path-session.sh"

  gate2_035_path_prepare_workspace() {
    local root="$1" label="$2"
    git -C "$root" init -q
    git -C "$root" config user.name 'Hideout Gate 035'
    git -C "$root" config user.email 'gate035@invalid.example'
    printf 'module example.invalid/%s\n\ngo 1.25\n' "$label" >"$root/go.mod"
    printf '{"name":"%s","private":true}\n' "$label" >"$root/package.json"
    printf '%s\n' "$label" >"$root/README.md"
    git -C "$root" add go.mod package.json README.md
    git -C "$root" commit -qm 'path fixture'
    cp "$out/logs/path-probe.py" "$root/.hideout-path-probe.py"
    cp "$out/logs/sibling-check.py" "$root/.hideout-sibling-check.py"
    cp "$out/logs/path-session.sh" "$root/.hideout-path-session.sh"
  }

  gate2_035_path_prepare_workspace "$workspace_a" path-a
  gate2_035_path_prepare_workspace "$workspace_b" path-b

  # Invoked through the EXIT trap below.
  # shellcheck disable=SC2329
  gate2_035_path_cleanup() {
    if [ "$profile_mode_changed" -eq 1 ]; then
      gate2_035_path_command "$hideout" profile workspace-path-mode "$profile" alias \
        >/dev/null 2>&1 || true
      profile_mode_changed=0
    fi
    touch "$workspace_a/.hideout-path-release" "$workspace_b/.hideout-path-release" 2>/dev/null || true
    local pid
    for pid in "$a_pid" "$b_pid"; do
      if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
        kill "$pid" 2>/dev/null || true
        wait "$pid" 2>/dev/null || true
      fi
    done
  }
  trap gate2_035_path_cleanup EXIT

  # Both incompatible path modes must fail before an untrusted target starts.
  profile_mode_changed=1
  gate2_035_path_command "$hideout" profile workspace-path-mode "$profile" preserve \
    >"$out/logs/preserve-mode.out" 2>"$out/logs/preserve-mode.err"
  if gate2_035_path_command "$hideout" run --profile "$profile" --backend lima \
    --network direct --workspace "$workspace_a" --guest-workspace /workspace \
    --terminal never -- sh -c 'touch /workspace/.hideout-preserve-target-started' \
    >"$out/logs/preserve-reject.out" 2>"$out/logs/preserve-reject.err"; then
    echo "shared-workspace gate2: shared default accepted preserve path mode" >&2
    return 1
  fi
  test ! -e "$workspace_a/.hideout-preserve-target-started"
  grep -F 'workspace pathMode=alias' \
    "$out/logs/preserve-reject.out" "$out/logs/preserve-reject.err" >/dev/null
  gate2_035_path_command "$hideout" profile workspace-path-mode "$profile" alias \
    >"$out/logs/alias-mode.out" 2>"$out/logs/alias-mode.err"
  profile_mode_changed=0

  printf 'gitdir: %s\n' "$external_gitdir" >"$workspace_external/.git"
  if gate2_035_path_command "$hideout" run --profile "$profile" --backend lima \
    --network direct --workspace "$workspace_external" --guest-workspace /workspace \
    --terminal never -- sh -c 'touch /workspace/.hideout-external-metadata-target-started' \
    >"$out/logs/external-metadata.out" 2>"$out/logs/external-metadata.err"; then
    echo "shared-workspace gate2: alias mode accepted external Git metadata" >&2
    return 1
  fi
  test ! -e "$workspace_external/.hideout-external-metadata-target-started"
  grep -F 'code=workspace.metadata.external' \
    "$out/logs/external-metadata.out" "$out/logs/external-metadata.err" >/dev/null

  gate2_035_path_start() {
    local label="$1" workspace="$2"
    env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run --verbose \
      --profile "$profile" --backend lima --network direct --workspace "$workspace" \
      --guest-workspace /workspace --terminal never -- \
      bash /workspace/.hideout-path-session.sh "$label" \
      >"$out/logs/$label.out" 2>"$out/logs/$label.err" &
    case "$label" in
      a) a_pid=$! ;;
      b) b_pid=$! ;;
      *) return 2 ;;
    esac
  }

  gate2_035_path_start a "$workspace_a"
  gate2_035_path_start b "$workspace_b"
  gate2_035_wait_file "$workspace_a/.hideout-path-a.ready" "path A readiness" 6000 "$a_pid"
  gate2_035_wait_file "$workspace_b/.hideout-path-b.ready" "path B readiness" 6000 "$b_pid"
  cp "$workspace_a/.hideout-path-a.json" "$out/logs/a.path-result.json"
  cp "$workspace_b/.hideout-path-b.json" "$out/logs/b.path-result.json"
  local physical_a physical_b
  physical_a="$(jq -r '.physicalRoot' "$out/logs/a.path-result.json")"
  physical_b="$(jq -r '.physicalRoot' "$out/logs/b.path-result.json")"
  test "$physical_a" != "$physical_b"
  printf '%s\n' "$physical_b" >"$workspace_a/.hideout-path-a.sibling-root"
  printf '%s\n' "$physical_a" >"$workspace_b/.hideout-path-b.sibling-root"
  gate2_035_wait_file "$workspace_a/.hideout-path-a.sibling-denied" "path A sibling denial" 6000 "$a_pid"
  gate2_035_wait_file "$workspace_b/.hideout-path-b.sibling-denied" "path B sibling denial" 6000 "$b_pid"
  touch "$workspace_a/.hideout-path-release" "$workspace_b/.hideout-path-release"
  gate2_035_wait_process "$a_pid" "path A"
  a_pid=""
  gate2_035_wait_process "$b_pid" "path B"
  b_pid=""

  gate2_035_path_session_id() {
    local stderr_path="$1" audit_path
    audit_path="$(awk '/audit: / { print $2; exit }' "$stderr_path")"
    test -n "$audit_path"
    basename "$(dirname "$audit_path")"
  }

  local label session activity_ready attempt activity_path
  for label in a b; do
    session="$(gate2_035_path_session_id "$out/logs/$label.err")"
    activity_path="$out/logs/$label.activity.json"
    activity_ready=0
    attempt=0
    while [ "$attempt" -lt 30 ]; do
      env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
        "$hideout" activity events --session "$session" --limit 500 --json \
        >"$activity_path" 2>"$out/logs/$label.activity.err" || true
      if gate2_035_path_activity_judge "$activity_path" 2>/dev/null; then
        activity_ready=1
        break
      fi
      attempt=$((attempt + 1))
      sleep 1
    done
    test "$activity_ready" -eq 1 || {
      echo "shared-workspace gate2: $label activity paths were not attachment-normalized" >&2
      return 1
    }
  done

  env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run \
    --profile "$profile" --backend lima --network direct --workspace "$workspace_a" \
    --guest-workspace /workspace --terminal never -- \
    python3 /workspace/.hideout-path-probe.py a-repeat repeat \
    >"$out/logs/a-repeat.out" 2>"$out/logs/a-repeat.err"
  cp "$workspace_a/.hideout-path-a-repeat.json" "$out/logs/a-repeat.path-result.json"

  python3 - "$out/logs/a.path-result.json" \
    "$out/logs/b.path-result.json" \
    "$out/logs/a-repeat.path-result.json" <<'PY'
import json
import sys

a, b, repeat = (json.load(open(path)) for path in sys.argv[1:])
expected = {"bash", "git", "node", "python", "go", "claude", "codex"}
if set(a["tools"]) != expected or set(b["tools"]) != expected or set(repeat["tools"]) != expected:
    raise RuntimeError("representative tool matrix is incomplete")
for name in expected - {"go"}:
    if a["tools"][name]["projectKey"] == b["tools"][name]["projectKey"]:
        raise RuntimeError(f"{name} merged distinct project identities")
    if a["tools"][name]["projectKey"] != repeat["tools"][name]["projectKey"]:
        raise RuntimeError(f"{name} changed the same-root project identity")
for result in (a, b, repeat):
    go = result["tools"]["go"]
    if go["projectKey"] != "/workspace/go.mod" or go["projectKeyMode"] != "logical-pwd-alias" or go["statefulProjectIdentity"]:
        raise RuntimeError("Go logical PWD behavior was not classified honestly")
for name in ("claude", "codex"):
    if a["tools"][name]["stateKey"] == b["tools"][name]["stateKey"]:
        raise RuntimeError(f"{name} merged distinct project state")
    if a["tools"][name]["stateKey"] != repeat["tools"][name]["stateKey"]:
        raise RuntimeError(f"{name} changed same-root project state")
    if not all(a["tools"][name][field] for field in ("history", "cache", "socket")):
        raise RuntimeError(f"{name} representative state is incomplete")
for result in (a, b):
    failed = sorted(name for name, passed in result["checks"].items() if not passed)
    if failed:
        raise RuntimeError(
            f'{result["label"]} logical/physical file equivalence failed: {failed}'
        )
if repeat["deleteStressIterations"] != 100 or repeat["checks"] != {"repeatedDeleteAcrossAliases": True}:
    raise RuntimeError(
        "same-root repeated logical/physical delete invalidation failed: "
        f'{repeat["deleteStressIterations"]} {repeat["checks"]}'
    )
PY

  local required_checks limitations
  required_checks="$(gate2_035_path_required_checks_json)"
  limitations="$(gate2_035_path_limitations_json)"
  jq -n --argjson required "$required_checks" --argjson limitations "$limitations" '{
    schema:"hideout.shared-workspace-path-correctness/v1",status:"passed",
    tools:["bash","claude","codex","git","go","node","python"],
    representativeAgents:["claude","codex"],
    limitations:$limitations,
    checks:(reduce $required[] as $key ({}; .[$key] = true))
  }' >"$out/path-correctness.json"
  gate2_035_path_correctness_judge "$out/path-correctness.json"
  jq '.status = "failed" | .checks.logicalPhysicalSameObject = false' "$out/path-correctness.json" \
    >"$out/negative-divergent-inode.json"
  if ! gate2_035_path_negative_fixture_judge "$out/negative-divergent-inode.json"; then
    echo "shared-workspace gate2: divergent inode fixture was not rejected for the expected reason" >&2
    return 1
  fi
  trap - EXIT
)
