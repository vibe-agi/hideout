#!/usr/bin/env bash
# shellcheck disable=SC2016,SC2329

# The single-quoted programs and predicates below execute in the guest or are
# passed verbatim to jq. Cleanup functions are installed indirectly as traps by
# callers of this sourced Gate 2 library.

# Real macOS arm64/Lima relation probes for feature 035. The caller owns the
# installed binary, isolated store, Lima home, and initialized profile. This
# library deliberately emits no product claim when it is merely sourced.

gate2_shared_workspace_not_run() {
  local out="$1"
  mkdir -p "$out"
  cat >"$out/relations.json" <<'EOF'
{
  "schema": "hideout.shared-workspace-relations/v1",
  "status": "not-run",
  "reason": "real macOS arm64 Lima shared-workspace relation gate was not requested"
}
EOF
}

gate2_035_wait_file() {
  local path="$1" description="$2" attempts="${3:-6000}" pid="${4:-}" i state status
  i=0
  while [ "$i" -lt "$attempts" ]; do
    [ -f "$path" ] && return 0
    if [ -n "$pid" ]; then
      state="$(ps -o state= -p "$pid" 2>/dev/null | tr -d '[:space:]')"
      if [ -z "$state" ] || [ "${state#Z}" != "$state" ]; then
        if wait "$pid"; then
          status=0
        else
          status=$?
        fi
        echo "shared-workspace gate2: $description owner exited before readiness (status=$status)" >&2
        return 1
      fi
    fi
    sleep 0.1
    i=$((i + 1))
  done
  echo "shared-workspace gate2: timed out waiting for $description" >&2
  return 1
}

gate2_035_wait_process() {
  local pid="$1" description="$2"
  if ! wait "$pid"; then
    echo "shared-workspace gate2: $description failed" >&2
    return 1
  fi
}

gate2_shared_workspace_relations() (
  set -euo pipefail

  if [ "$#" -ne 6 ]; then
    echo "usage: gate2_shared_workspace_relations <out> <store> <lima-home> <hideout> <profile> <fixture-root>" >&2
    return 2
  fi
  local out="$1" store="$2" lima_home="$3" hideout="$4" profile="$5" fixture_root="$6"
  local disjoint_a="$fixture_root/disjoint-a" disjoint_b="$fixture_root/disjoint-b"
  local same="$fixture_root/same" ancestor="$fixture_root/ancestor" descendant="$fixture_root/ancestor/descendant"
  local disjoint_a_pid="" disjoint_b_pid="" same_owner_pid="" same_contender_pid=""
  local ancestor_pid="" descendant_pid=""

  mkdir -p "$out/logs" "$disjoint_a" "$disjoint_b" "$same" "$descendant"
  out="$(cd "$out" && pwd -P)"
  fixture_root="$(cd "$fixture_root" && pwd -P)"
  disjoint_a="$fixture_root/disjoint-a"
  disjoint_b="$fixture_root/disjoint-b"
  same="$fixture_root/same"
  ancestor="$fixture_root/ancestor"
  descendant="$ancestor/descendant"

  printf 'disjoint-a-035\n' >"$disjoint_a/only-a.txt"
  printf 'disjoint-b-035\n' >"$disjoint_b/only-b.txt"
  printf 'ancestor-035\n' >"$ancestor/parent-only.txt"
  printf 'descendant-035\n' >"$descendant/child-only.txt"

  cat >"$same/lock-owner.py" <<'PY'
import fcntl
from pathlib import Path
import time

lock = open("lock.txt", "a+")
fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
Path("owner.ready").touch()
while not Path("owner.release").exists():
    time.sleep(0.02)
PY
  cat >"$same/lock-contender.py" <<'PY'
import fcntl
from pathlib import Path
import time

lock = open("lock.txt", "a+")
try:
    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
except BlockingIOError:
    Path("contender.blocked").touch()
else:
    raise AssertionError("same-root sibling unexpectedly shared the lock owner")
while not Path("owner.release").exists():
    time.sleep(0.02)
deadline = time.monotonic() + 10
while True:
    try:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        break
    except BlockingIOError:
        if time.monotonic() >= deadline:
            raise
        time.sleep(0.02)
Path("contender.acquired").touch()
PY

  gate2_035_cleanup() {
    touch "$disjoint_a/release" "$disjoint_b/release" \
      "$same/owner.release" "$ancestor/release" "$descendant/release" 2>/dev/null || true
    local pid
    for pid in "$disjoint_a_pid" "$disjoint_b_pid" "$same_owner_pid" \
      "$same_contender_pid" "$ancestor_pid" "$descendant_pid"; do
      if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
        kill "$pid" 2>/dev/null || true
        wait "$pid" 2>/dev/null || true
      fi
    done
  }
  trap gate2_035_cleanup EXIT

  HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run \
    --profile "$profile" --backend lima --network direct --workspace "$disjoint_a" \
    --guest-workspace /workspace -- sh -eu -c '
test "$(cat only-a.txt)" = "disjoint-a-035"
test ! -e only-b.txt
test -L /workspace
case "$(pwd -P)" in /hideout/workspaces/wrk_*) ;; *) exit 1 ;; esac
pwd -P > physical-project-key
cat /proc/sys/kernel/random/boot_id > boot.id
touch ready
while [ ! -f release ]; do sleep 0.05; done
' >"$out/logs/disjoint-a.out" 2>"$out/logs/disjoint-a.err" &
  disjoint_a_pid=$!

  HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run \
    --profile "$profile" --backend lima --network direct --workspace "$disjoint_b" \
    --guest-workspace /workspace -- sh -eu -c '
test "$(cat only-b.txt)" = "disjoint-b-035"
test ! -e only-a.txt
test -L /workspace
case "$(pwd -P)" in /hideout/workspaces/wrk_*) ;; *) exit 1 ;; esac
pwd -P > physical-project-key
cat /proc/sys/kernel/random/boot_id > boot.id
touch ready
while [ ! -f release ]; do
  if [ -f probe ]; then
    test "$(cat only-b.txt)" = "disjoint-b-035"
    rm -f probe
    touch probe.ack
  fi
  sleep 0.05
done
' >"$out/logs/disjoint-b.out" 2>"$out/logs/disjoint-b.err" &
  disjoint_b_pid=$!

  gate2_035_wait_file "$disjoint_a/ready" "disjoint A readiness" 6000 "$disjoint_a_pid"
  gate2_035_wait_file "$disjoint_b/ready" "disjoint B readiness" 6000 "$disjoint_b_pid"
  cmp "$disjoint_a/boot.id" "$disjoint_b/boot.id"
  test "$(cat "$disjoint_a/physical-project-key")" != "$(cat "$disjoint_b/physical-project-key")"

  HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run \
    --profile "$profile" --backend lima --network direct --workspace "$same" \
    --guest-workspace /workspace -- python3 ./lock-owner.py \
    >"$out/logs/same-owner.out" 2>"$out/logs/same-owner.err" &
  same_owner_pid=$!
  gate2_035_wait_file "$same/owner.ready" "same-root lock owner" 6000 "$same_owner_pid"

  HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run \
    --profile "$profile" --backend lima --network direct --workspace "$same" \
    --guest-workspace /workspace -- python3 ./lock-contender.py \
    >"$out/logs/same-contender.out" 2>"$out/logs/same-contender.err" &
  same_contender_pid=$!
  gate2_035_wait_file "$same/contender.blocked" "same-root independent lock conflict" 6000 "$same_contender_pid"
  touch "$same/owner.release"
  gate2_035_wait_process "$same_owner_pid" "same-root owner"
  same_owner_pid=""
  gate2_035_wait_process "$same_contender_pid" "same-root contender"
  same_contender_pid=""
  test -f "$same/contender.acquired"

  HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run \
    --profile "$profile" --backend lima --network direct --workspace "$ancestor" \
    --guest-workspace /workspace -- sh -eu -c '
test "$(cat parent-only.txt)" = "ancestor-035"
test "$(cat descendant/child-only.txt)" = "descendant-035"
cat /proc/sys/kernel/random/boot_id > ancestor.boot
touch ancestor.ready
while [ ! -f release ]; do sleep 0.05; done
' >"$out/logs/ancestor.out" 2>"$out/logs/ancestor.err" &
  ancestor_pid=$!

  HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run \
    --profile "$profile" --backend lima --network direct --workspace "$descendant" \
    --guest-workspace /workspace -- sh -eu -c '
test "$(cat child-only.txt)" = "descendant-035"
test ! -e ../parent-only.txt
test ! -e /parent-only.txt
cat /proc/sys/kernel/random/boot_id > descendant.boot
touch descendant.ready
while [ ! -f release ]; do sleep 0.05; done
' >"$out/logs/descendant.out" 2>"$out/logs/descendant.err" &
  descendant_pid=$!

  gate2_035_wait_file "$ancestor/ancestor.ready" "ancestor readiness" 6000 "$ancestor_pid"
  gate2_035_wait_file "$descendant/descendant.ready" "descendant readiness" 6000 "$descendant_pid"
  cmp "$ancestor/ancestor.boot" "$descendant/descendant.boot"
  cmp "$disjoint_a/boot.id" "$ancestor/ancestor.boot"

  local environment_count instance_count
	environment_count="$(find "$store/environments" -mindepth 2 -maxdepth 2 -name environment.json -type f | wc -l | tr -d ' ')"
  instance_count="$(LIMA_HOME="$lima_home" limactl list --quiet | sed '/^$/d' | wc -l | tr -d ' ')"
  test "$environment_count" -eq 1
  test "$instance_count" -eq 1

  touch "$disjoint_a/release"
  gate2_035_wait_process "$disjoint_a_pid" "disjoint A sibling detach"
  disjoint_a_pid=""
  touch "$disjoint_b/probe"
  gate2_035_wait_file "$disjoint_b/probe.ack" "disjoint B post-detach execution probe" 6000 "$disjoint_b_pid"

	  jq -n \
	    --argjson environments "$environment_count" \
	    --argjson instances "$instance_count" \
	    '{
	      schema: "hideout.shared-workspace-relations/v1",
	      status: "passed",
	      environmentCount: $environments,
	      instanceCount: $instances,
	      checks: {
	        oneEnvironment: true,
	        oneInstance: true,
	        sameBootAcrossDisjointRoots: true,
	        distinctPhysicalProjectKeys: true,
	        sameBootAcrossNestedRoots: true,
	        disjointBidirectionalUnavailable: true,
	        siblingDetachPreservedExecution: true,
	        sameRootLockOwnersIndependent: true,
	        nestedAuthorityEnforced: true
	      }
	    }' >"$out/relations.json"

  touch "$disjoint_b/release" "$ancestor/release" "$descendant/release"
  gate2_035_wait_process "$disjoint_b_pid" "disjoint B"
  disjoint_b_pid=""
  gate2_035_wait_process "$ancestor_pid" "ancestor"
  ancestor_pid=""
  gate2_035_wait_process "$descendant_pid" "descendant"
  descendant_pid=""
  trap - EXIT
)

# Exercises feature 035's integration with the already-ratified 036 lifecycle
# protocol. The caller supplies an isolated store/Lima home and owns final
# artifact retention. This function observes real daemon state and a real Lima
# instance; it never upgrades a local-only run into release evidence.
gate2_shared_workspace_lifecycle() (
  set -euo pipefail

  if [ "$#" -ne 6 ]; then
    echo "usage: gate2_shared_workspace_lifecycle <out> <store> <lima-home> <hideout> <profile> <fixture-root>" >&2
    return 2
  fi
  local out="$1" store="$2" lima_home="$3" hideout="$4" profile="$5" fixture_root="$6"
  local workspace_a="$fixture_root/lifecycle-a" workspace_b="$fixture_root/lifecycle-b"
  local workspace_bridge="$fixture_root/lifecycle-bridge" workspace_cancel="$fixture_root/lifecycle-cancel"
  local workspace_restart="$fixture_root/lifecycle-restart" record="" environment_id=""
  local instance_name="" daemon_pid="" a_pid="" b_pid="" bridge_pid="" cancel_pid="" restart_pid=""
  local browser_capture="$fixture_root/browser-capture" preview_url_file="$fixture_root/preview-url.private"
  local generation_before="" generation_after="" stopped_generation="" old_refs="[]"
  local preview_port preview_url lifecycle_started lifecycle_stopped

  mkdir -p "$out/logs" "$workspace_a" "$workspace_b" "$workspace_bridge" \
    "$workspace_cancel" "$workspace_restart"
  out="$(cd "$out" && pwd -P)"
  fixture_root="$(cd "$fixture_root" && pwd -P)"
  workspace_a="$fixture_root/lifecycle-a"
  workspace_b="$fixture_root/lifecycle-b"
  workspace_bridge="$fixture_root/lifecycle-bridge"
  workspace_cancel="$fixture_root/lifecycle-cancel"
  workspace_restart="$fixture_root/lifecycle-restart"
  browser_capture="$fixture_root/browser-capture"
  preview_url_file="$fixture_root/preview-url.private"

  cat >"$browser_capture" <<EOF
#!/bin/sh
set -eu
target=""
for value do target=\$value; done
printf '%s\\n' "\$target" >>$(printf '%q' "$preview_url_file")
EOF
  chmod 0700 "$browser_capture"

  gate2_035_lifecycle_command() {
    env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$@"
  }

  gate2_035_lifecycle_status() {
    gate2_035_lifecycle_command "$hideout" daemon status
  }

  gate2_035_lifecycle_wait_status() {
    local expression="$1" description="$2" attempts="${3:-700}" i
    i=0
    while [ "$i" -lt "$attempts" ]; do
      if gate2_035_lifecycle_status >"$out/logs/lifecycle-current.json" 2>/dev/null &&
        jq -e --arg environmentId "$environment_id" "$expression" \
          "$out/logs/lifecycle-current.json" >/dev/null; then
        return 0
      fi
      sleep 0.1
      i=$((i + 1))
    done
    echo "shared-workspace gate2: timed out waiting for $description" >&2
    cat "$out/logs/lifecycle-current.json" >&2 2>/dev/null || true
    return 1
  }

  gate2_035_lifecycle_wait_daemon() {
    for _ in $(seq 1 300); do
      if gate2_035_lifecycle_status >"$out/logs/daemon-status.json" 2>/dev/null &&
        jq -e '.state == "serving"' "$out/logs/daemon-status.json" >/dev/null; then
        return 0
      fi
      sleep 0.02
    done
    echo "shared-workspace gate2: daemon did not become ready" >&2
    return 1
  }

  gate2_035_lifecycle_start_daemon() {
    local label="$1"
    HIDEOUT_BROWSER_PATH="$browser_capture" gate2_035_lifecycle_command \
      "$hideout" daemon start >"$out/logs/daemon-$label.out" 2>"$out/logs/daemon-$label.err" &
    daemon_pid=$!
    gate2_035_lifecycle_wait_daemon
  }

  gate2_035_lifecycle_stop_daemon() {
    gate2_035_lifecycle_command "$hideout" daemon stop >/dev/null 2>&1 || true
    if [ -n "$daemon_pid" ]; then
      wait "$daemon_pid" 2>/dev/null || true
      daemon_pid=""
    fi
  }

  gate2_035_lifecycle_start_marker() {
    local label="$1" workspace="$2" pid
    rm -f "$workspace/.hideout-035-$label-ready" "$workspace/.hideout-035-$label-release" \
      "$workspace/.hideout-035-$label-probe" "$workspace/.hideout-035-$label-probe-ack" \
      "$workspace/.hideout-035-$label-boot"
    gate2_035_lifecycle_command "$hideout" run --profile "$profile" --backend lima --network direct \
      --workspace "$workspace" --guest-workspace /workspace -- sh -eu -c '
label="$1"
cat /proc/sys/kernel/random/boot_id >".hideout-035-$label-boot"
touch ".hideout-035-$label-ready"
while [ ! -f ".hideout-035-$label-release" ]; do
  if [ -f ".hideout-035-$label-probe" ]; then
    rm -f ".hideout-035-$label-probe"
    touch ".hideout-035-$label-probe-ack"
  fi
  sleep 0.05
done
' gate2-marker "$label" >"$out/logs/$label.out" 2>"$out/logs/$label.err" &
    pid=$!
    case "$label" in
      lifecycle-a) a_pid="$pid" ;;
      lifecycle-b) b_pid="$pid" ;;
      lifecycle-cancel) cancel_pid="$pid" ;;
      lifecycle-restart) restart_pid="$pid" ;;
      *) echo "shared-workspace gate2: unsupported marker $label" >&2; return 2 ;;
    esac
    gate2_035_wait_file "$workspace/.hideout-035-$label-ready" "$label readiness" 6000 "$pid"
  }

  gate2_035_lifecycle_probe_marker() {
    local label="$1" workspace="$2" pid=""
    case "$label" in
      lifecycle-a) pid="$a_pid" ;;
      lifecycle-b) pid="$b_pid" ;;
      lifecycle-cancel) pid="$cancel_pid" ;;
      lifecycle-restart) pid="$restart_pid" ;;
    esac
    rm -f "$workspace/.hideout-035-$label-probe" "$workspace/.hideout-035-$label-probe-ack"
    touch "$workspace/.hideout-035-$label-probe"
    gate2_035_wait_file "$workspace/.hideout-035-$label-probe-ack" "$label execution probe" 6000 "$pid"
  }

  gate2_035_lifecycle_release_marker() {
    local label="$1" workspace="$2" pid=""
    touch "$workspace/.hideout-035-$label-release"
    case "$label" in
      lifecycle-a) pid="$a_pid" ;;
      lifecycle-b) pid="$b_pid" ;;
      lifecycle-cancel) pid="$cancel_pid" ;;
      lifecycle-restart) pid="$restart_pid" ;;
    esac
    if [ -n "$pid" ]; then
      gate2_035_wait_process "$pid" "$label"
    fi
    case "$label" in
      lifecycle-a) a_pid="" ;;
      lifecycle-b) b_pid="" ;;
      lifecycle-cancel) cancel_pid="" ;;
      lifecycle-restart) restart_pid="" ;;
    esac
  }

  gate2_035_lifecycle_lima_state() {
    local wanted="$1"
    LIMA_HOME="$lima_home" limactl list --format json --all-fields | jq -s -e \
      --arg name "$instance_name" --arg wanted "$wanted" \
      'any(.[]; .name == $name and (.status | ascii_downcase) == $wanted)' >/dev/null
  }

  gate2_035_lifecycle_wait_lima_state() {
    local wanted="$1"
    for _ in $(seq 1 600); do
      gate2_035_lifecycle_lima_state "$wanted" && return 0
      sleep 0.1
    done
    echo "shared-workspace gate2: Lima instance did not become $wanted" >&2
    return 1
  }

  gate2_035_lifecycle_port_connects() {
    python3 - "$1" <<'PY'
import socket
import sys

try:
    with socket.create_connection(("127.0.0.1", int(sys.argv[1])), timeout=0.2):
        pass
except OSError:
    raise SystemExit(1)
PY
  }

  gate2_035_lifecycle_cleanup() {
    touch "$workspace_a/.hideout-035-lifecycle-a-release" \
      "$workspace_b/.hideout-035-lifecycle-b-release" \
      "$workspace_bridge/.hideout-035-bridge-release" \
      "$workspace_cancel/.hideout-035-lifecycle-cancel-release" \
      "$workspace_restart/.hideout-035-lifecycle-restart-release" 2>/dev/null || true
    local pid
    for pid in "$a_pid" "$b_pid" "$bridge_pid" "$cancel_pid" "$restart_pid"; do
      if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
        kill "$pid" 2>/dev/null || true
        wait "$pid" 2>/dev/null || true
      fi
    done
    gate2_035_lifecycle_stop_daemon
  }
  trap gate2_035_lifecycle_cleanup EXIT

  gate2_035_lifecycle_command "$hideout" daemon stop >/dev/null 2>&1 || true
	  gate2_035_lifecycle_start_daemon initial
	  lifecycle_started="$SECONDS"

  gate2_035_lifecycle_start_marker lifecycle-a "$workspace_a"
  for _ in $(seq 1 300); do
    record="$(find "$store/environments" -name environment.json -type f -print -quit)"
    [ -f "$record" ] && break
    sleep 0.1
  done
  [ -f "$record" ]
  environment_id="$(jq -r '.id' "$record")"
  instance_name="$(jq -r '.instanceName' "$record")"
  gate2_035_lifecycle_start_marker lifecycle-b "$workspace_b"
  cmp "$workspace_a/.hideout-035-lifecycle-a-boot" "$workspace_b/.hideout-035-lifecycle-b-boot"
  gate2_035_lifecycle_wait_status '
    any(.lifecycle[]?; .environmentId == $environmentId and
      ([.pins[]?, .drains[]?] | map(select(.kind == "workspace.guest-view" and .state == "active")) | length) >= 2)
  ' "two active workspace views"
  generation_before="$(jq -r --arg id "$environment_id" \
    '.lifecycle[] | select(.environmentId == $id) | .startGeneration' "$out/logs/lifecycle-current.json")"

  preview_port="$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
)"
  rm -f "$preview_url_file" "$workspace_bridge/.hideout-035-bridge-ready" \
    "$workspace_bridge/.hideout-035-bridge-release"
  gate2_035_lifecycle_command "$hideout" run --profile "$profile" --backend lima --network direct \
    --workspace "$workspace_bridge" --guest-workspace /workspace --preview "127.0.0.1:$preview_port" \
    -- sh -eu -c '
python3 -m http.server "$1" --bind 127.0.0.1 >/tmp/hideout-035-preview.log 2>&1 &
server=$!
trap '\''kill "$server" 2>/dev/null || true; wait "$server" 2>/dev/null || true'\'' EXIT
cat /proc/sys/kernel/random/boot_id >.hideout-035-bridge-boot
touch .hideout-035-bridge-ready
while [ ! -f .hideout-035-bridge-release ]; do sleep 0.05; done
' gate2-preview "$preview_port" >"$out/logs/bridge.out" 2>"$out/logs/bridge.err" &
  bridge_pid=$!
  gate2_035_wait_file "$workspace_bridge/.hideout-035-bridge-ready" "bridge session readiness" 6000 "$bridge_pid"
  gate2_035_wait_file "$preview_url_file" "captured preview URL" 6000 "$bridge_pid"
  preview_url="$(tail -n 1 "$preview_url_file")"
  preview_port="${preview_url%/}"
  preview_port="${preview_port##*:}"
  gate2_035_lifecycle_port_connects "$preview_port"
  gate2_035_lifecycle_wait_status '
    any(.lifecycle[]?; .environmentId == $environmentId and
      any(.pins[]?; .kind == "endpoint.run-bridge" and .state == "active"))
  ' "active bridge pin"

  gate2_035_lifecycle_release_marker lifecycle-a "$workspace_a"
  gate2_035_lifecycle_probe_marker lifecycle-b "$workspace_b"
  gate2_035_lifecycle_release_marker lifecycle-b "$workspace_b"
  kill -0 "$bridge_pid"
  gate2_035_lifecycle_port_connects "$preview_port"
  gate2_035_lifecycle_lima_state running

  touch "$workspace_bridge/.hideout-035-bridge-release"
  gate2_035_wait_process "$bridge_pid" "bridge session"
  bridge_pid=""
  gate2_035_lifecycle_wait_status '
    any(.lifecycle[]?; .environmentId == $environmentId and .activity == "idle-grace")
  ' "first idle grace"

  gate2_035_lifecycle_start_marker lifecycle-cancel "$workspace_cancel"
  cmp "$workspace_a/.hideout-035-lifecycle-a-boot" \
    "$workspace_cancel/.hideout-035-lifecycle-cancel-boot"
  gate2_035_lifecycle_wait_status '
    any(.lifecycle[]?; .environmentId == $environmentId and .activity == "pinned" and
      (.idleDeadline == null) and
      any([.pins[]?, .drains[]?][]?; .kind == "workspace.guest-view" and .state == "active"))
  ' "grace cancellation by a cross-workspace attach"
  gate2_035_lifecycle_release_marker lifecycle-cancel "$workspace_cancel"
  gate2_035_lifecycle_wait_status '
    any(.lifecycle[]?; .environmentId == $environmentId and .activity == "stopped" and
      .backendState == "stopped" and
      ([.pins[]?, .drains[]?, .orphans[]?] | map(select(.kind == "workspace.host-provider" or .kind == "workspace.guest-view")) | length) == 0)
  ' "exact final stop" 700
  gate2_035_lifecycle_wait_lima_state stopped
  stopped_generation="$(jq -r --arg id "$environment_id" \
    '.lifecycle[] | select(.environmentId == $id) | .startGeneration' "$out/logs/lifecycle-current.json")"
  [ "$stopped_generation" = "$generation_before" ]

  gate2_035_lifecycle_start_marker lifecycle-restart "$workspace_restart"
  gate2_035_lifecycle_wait_status '
    any(.lifecycle[]?; .environmentId == $environmentId and .activity == "pinned" and
      .reconciliation == "complete")
  ' "post-stop fresh attachment"
  generation_after="$(jq -r --arg id "$environment_id" \
    '.lifecycle[] | select(.environmentId == $id) | .startGeneration' "$out/logs/lifecycle-current.json")"
  [ "$generation_after" -gt "$generation_before" ]
  old_refs="$(jq -c --arg id "$environment_id" '
    [.lifecycle[] | select(.environmentId == $id) |
      [.pins[]?, .drains[]?, .orphans[]?][] |
      select(.kind == "workspace.host-provider" or .kind == "workspace.guest-view") |
      {kind: .kind, id: .id}] | unique
  ' "$out/logs/lifecycle-current.json")"
  [ "$(jq 'length' <<<"$old_refs")" -ge 2 ]

  kill -KILL "$daemon_pid"
  wait "$daemon_pid" 2>/dev/null || true
  daemon_pid=""
  touch "$workspace_restart/.hideout-035-lifecycle-restart-release"
  wait "$restart_pid" 2>/dev/null || true
  restart_pid=""
  gate2_035_lifecycle_start_daemon restarted
  gate2_035_lifecycle_wait_status '
    any(.lifecycle[]?; .environmentId == $environmentId and .reconciliation == "complete")
  ' "workspace absence reconciliation after daemon restart"
  jq -e --arg id "$environment_id" --argjson old "$old_refs" '
    [.lifecycle[] | select(.environmentId == $id) |
      [.pins[]?, .drains[]?, .orphans[]?][] |
      select(.state == "active") | {kind: .kind, id: .id}] as $current |
    all($old[]; . as $prior | all($current[]; .kind != $prior.kind or .id != $prior.id))
  ' "$out/logs/lifecycle-current.json" >/dev/null

  gate2_035_lifecycle_start_marker lifecycle-restart "$workspace_restart"
  gate2_035_lifecycle_wait_status '
    any(.lifecycle[]?; .environmentId == $environmentId and .activity == "pinned")
  ' "fresh post-reconciliation workspace attach"
  gate2_035_lifecycle_release_marker lifecycle-restart "$workspace_restart"
  gate2_035_lifecycle_wait_status '
    any(.lifecycle[]?; .environmentId == $environmentId and .activity == "stopped" and
      .backendState == "stopped")
  ' "post-restart final stop" 700
  gate2_035_lifecycle_wait_lima_state stopped
	  lifecycle_stopped="$SECONDS"

	  jq -n \
	    --argjson firstGeneration "$generation_before" \
	    --argjson restartGeneration "$generation_after" \
	    --argjson elapsedSeconds "$((lifecycle_stopped - lifecycle_started))" \
    '{
	      schema: "hideout.shared-workspace-lifecycle/v1",
	      status: "passed",
	      firstGeneration: $firstGeneration,
      restartGeneration: $restartGeneration,
      elapsedSeconds: $elapsedSeconds,
      checks: {
        siblingDetachPreservedExecution: true,
        bridgePinnedMachine: true,
        graceCancelledByCrossWorkspaceAttach: true,
        graceCancelReusedBoot: true,
        exactFinalStopObserved: true,
        restartDidNotReadoptWorkspaceAuthority: true,
        postReconciliationAttachUsedFreshAuthority: true
      }
    }' >"$out/lifecycle.json"

  gate2_035_lifecycle_stop_daemon
  trap - EXIT
)
