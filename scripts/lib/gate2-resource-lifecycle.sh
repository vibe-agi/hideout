#!/usr/bin/env bash

gate2_036_require() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "resource-lifecycle gate2: missing required command: $1" >&2
    return 127
  }
}

gate2_036_stage() {
  printf '%s\n' "resource-lifecycle gate2: stage=$1" >&2
}

gate2_036_run_env() {
  gate2_036_command_env "$GATE2_036_STORE" "$GATE2_036_LIMA_HOME" "$GATE2_036_BIN" "$GATE2_036_ARCH" "$@"
}

gate2_036_run_target() {
  gate2_036_run_env "$GATE2_036_HIDEOUT" run --profile default --backend lima --network direct \
    --workspace "$GATE2_036_WORKSPACE" --guest-workspace /workspace -- "$@"
}

gate2_036_seed_retained_state() {
  local before="$GATE2_036_TMP/audits-before.txt" after="$GATE2_036_TMP/audits-after.txt"
  find "$GATE2_036_STORE/sessions" -name audit.jsonl -type f -print 2>/dev/null | LC_ALL=C sort >"$before" || true
  gate2_036_run_target sh -eu -c '
printf "guest-disk-036\n" > /var/tmp/hideout-036-retained-disk
cache_root=${XDG_CACHE_HOME:-$HOME/.cache}
mkdir -p "$cache_root/hideout-036"
printf "profile-cache-036\n" > "$cache_root/hideout-036/retained-cache"
' >"$GATE2_036_OUT/logs/retained-seed.out" 2>"$GATE2_036_OUT/logs/retained-seed.err"
  find "$GATE2_036_STORE/sessions" -name audit.jsonl -type f -print 2>/dev/null | LC_ALL=C sort >"$after"
  GATE2_036_RETAINED_AUDIT="$(comm -13 "$before" "$after" | tail -n 1)"
  [ -f "$GATE2_036_RETAINED_AUDIT" ] || {
    echo "resource-lifecycle gate2: retained-state run did not produce an audit file" >&2
    return 1
  }
  GATE2_036_RETAINED_AUDIT_SHA="$(shasum -a 256 "$GATE2_036_RETAINED_AUDIT" | awk '{print $1}')"
}

gate2_036_verify_retained_state_after_restart() {
  gate2_036_run_target sh -eu -c '
cache_root=${XDG_CACHE_HOME:-$HOME/.cache}
test "$(cat /var/tmp/hideout-036-retained-disk)" = guest-disk-036
test "$(cat "$cache_root/hideout-036/retained-cache")" = profile-cache-036
' >"$GATE2_036_OUT/logs/retained-verify.out" 2>"$GATE2_036_OUT/logs/retained-verify.err"
}

gate2_036_wait_daemon() {
  local i
  for i in $(seq 1 300); do
    if gate2_036_run_env "$GATE2_036_HIDEOUT" daemon status >"$GATE2_036_OUT/logs/daemon-status.json" 2>/dev/null; then
      jq -e '.state == "serving"' "$GATE2_036_OUT/logs/daemon-status.json" >/dev/null && return 0
    fi
    sleep 0.02
  done
  echo "resource-lifecycle gate2: daemon did not become authentically ready" >&2
  return 1
}

gate2_036_lifecycle_status() {
  gate2_036_run_env "$GATE2_036_HIDEOUT" daemon status
}

gate2_036_wait_lifecycle() {
  local expression="$1" description="$2" attempts="${3:-700}" i
  for i in $(seq 1 "$attempts"); do
    if gate2_036_lifecycle_status >"$GATE2_036_OUT/logs/lifecycle-current.json" 2>/dev/null &&
      jq -e --arg environmentId "$GATE2_036_ENV_ID" "$expression" \
        "$GATE2_036_OUT/logs/lifecycle-current.json" >/dev/null; then
      return 0
    fi
    sleep 0.1
  done
  echo "resource-lifecycle gate2: timed out waiting for $description" >&2
  cat "$GATE2_036_OUT/logs/lifecycle-current.json" >&2 2>/dev/null || true
  return 1
}

gate2_036_lima_running() {
  LIMA_HOME="$GATE2_036_LIMA_HOME" limactl list --format json --all-fields | jq -s -e --arg name "$GATE2_036_INSTANCE" \
    'any(.[]; .name == $name and (.status | ascii_downcase) == "running")' >/dev/null
}

gate2_036_lima_stopped() {
  LIMA_HOME="$GATE2_036_LIMA_HOME" limactl list --format json --all-fields | jq -s -e --arg name "$GATE2_036_INSTANCE" \
    'any(.[]; .name == $name and (.status | ascii_downcase) == "stopped")' >/dev/null
}

gate2_036_wait_lima_stopped() {
  local i
  for i in $(seq 1 500); do
    gate2_036_lima_stopped && return 0
    sleep 0.1
  done
  echo "resource-lifecycle gate2: Lima instance did not become observed stopped" >&2
  return 1
}

gate2_036_stop_daemon() {
  local started ended
  started="$(gate2_036_now_seconds)"
  gate2_036_run_env "$GATE2_036_HIDEOUT" daemon stop >/dev/null 2>&1 || true
  if [ -n "${GATE2_036_DAEMON_PID:-}" ]; then
    wait "$GATE2_036_DAEMON_PID" 2>/dev/null || true
    GATE2_036_DAEMON_PID=""
  fi
  ended="$(gate2_036_now_seconds)"
  GATE2_036_SHUTDOWN_MS="$(awk -v start="$started" -v end="$ended" 'BEGIN { printf "%.3f", (end-start)*1000 }')"
}

gate2_036_start_daemon() {
  local path_prefix="${1:-}" label="${2:-normal}"
  if [ -n "$path_prefix" ]; then
    PATH="$path_prefix:$PATH" gate2_036_run_env "$GATE2_036_HIDEOUT" daemon start \
      >"$GATE2_036_OUT/logs/daemon-$label.out" 2>"$GATE2_036_OUT/logs/daemon-$label.err" &
  else
    gate2_036_run_env "$GATE2_036_HIDEOUT" daemon start \
      >"$GATE2_036_OUT/logs/daemon-$label.out" 2>"$GATE2_036_OUT/logs/daemon-$label.err" &
  fi
  GATE2_036_DAEMON_PID=$!
  gate2_036_wait_daemon
}

gate2_036_make_limactl_wrapper() {
  local dir="$1" mode="$2" real="$3" state="$4"
  mkdir -p "$dir"
  cat >"$dir/limactl" <<EOF
#!/usr/bin/env bash
set -euo pipefail
real=$(printf '%q' "$real")
state=$(printf '%q' "$state")
mode=$(printf '%q' "$mode")
if [ "\$mode" = slow-once ] && [ "\${1:-}" = list ] && [ ! -e "\$state" ]; then
  /usr/bin/touch "\$state"
  # Stay well inside the production five-second observation timeout. This
  # scenario proves readiness and per-environment waiting; fail-once below
  # separately proves timeout/error blocking and authenticated recovery.
  /bin/sleep 2
fi
if [ "\$mode" = fail-once ] && [ "\${1:-}" = list ] && [ ! -e "\$state" ]; then
  /usr/bin/touch "\$state"
  exit 42
fi
if [ "\$mode" = stop-unknown ] && [ "\${1:-}" = stop ]; then
  "\$real" "\$@"
  /usr/bin/touch "\$state"
  exit 0
fi
if [ "\$mode" = stop-unknown ] && [ "\${1:-}" = list ] && [ -e "\$state" ]; then
  exit 42
fi
exec "\$real" "\$@"
EOF
  chmod 0700 "$dir/limactl"
}

gate2_036_start_marker_session() {
  local label="$1" marker release probe ack
  marker="$GATE2_036_WORKSPACE/.hideout-gate-control/$label-ready"
  release="$GATE2_036_WORKSPACE/.hideout-gate-control/$label-release"
  probe="$GATE2_036_WORKSPACE/.hideout-gate-control/$label-probe"
  ack="$GATE2_036_WORKSPACE/.hideout-gate-control/$label-probe-ack"
  rm -f "$marker" "$release" "$probe" "$ack"
  gate2_036_run_env "$GATE2_036_HIDEOUT" run --profile default --backend lima --network direct \
    --workspace "$GATE2_036_WORKSPACE" --guest-workspace /workspace -- sh -eu -c \
    "touch /workspace/.hideout-gate-control/$label-ready; while [ ! -f /workspace/.hideout-gate-control/$label-release ]; do if [ -f /workspace/.hideout-gate-control/$label-probe ]; then rm -f /workspace/.hideout-gate-control/$label-probe; touch /workspace/.hideout-gate-control/$label-probe-ack; fi; sleep 0.05; done" \
    >"$GATE2_036_OUT/logs/$label.out" 2>"$GATE2_036_OUT/logs/$label.err" &
  case "$label" in
    anchor) GATE2_036_ANCHOR_PID=$! ;;
    second) GATE2_036_SECOND_PID=$! ;;
    *) echo "resource-lifecycle gate2: unsupported marker session $label" >&2; return 2 ;;
  esac
  gate2_036_wait_file "$marker" "$label session"
}

gate2_036_probe_marker_session() {
  local label="$1" probe ack
  probe="$GATE2_036_WORKSPACE/.hideout-gate-control/$label-probe"
  ack="$GATE2_036_WORKSPACE/.hideout-gate-control/$label-probe-ack"
  rm -f "$probe" "$ack"
  touch "$probe"
  gate2_036_wait_file "$ack" "$label session execution probe"
}

gate2_036_release_marker_session() {
  local label="$1" pid
  touch "$GATE2_036_WORKSPACE/.hideout-gate-control/$label-release"
  case "$label" in
    anchor) pid="$GATE2_036_ANCHOR_PID" ;;
    second) pid="$GATE2_036_SECOND_PID" ;;
    *) echo "resource-lifecycle gate2: unsupported marker session $label" >&2; return 2 ;;
  esac
  if [ -n "$pid" ]; then
    wait "$pid"
    case "$label" in
      anchor) GATE2_036_ANCHOR_PID="" ;;
      second) GATE2_036_SECOND_PID="" ;;
    esac
  fi
}

gate2_036_run_attach_stop_races() {
  # Exercise one real attach/stop interleaving at a time. A wider batch turns
  # this lifecycle gate into an SSH saturation test and can exhaust Lima's
  # handshake path before lifecycle serialization is reached.
  local total="$1" batch_size=1 completed=0 batch index
  local -a run_pids stop_pids
  while [ "$completed" -lt "$total" ]; do
    run_pids=()
    stop_pids=()
    batch=$((total - completed))
    [ "$batch" -le "$batch_size" ] || batch="$batch_size"
    for index in $(seq 1 "$batch"); do
      gate2_036_run_target true \
        >"$GATE2_036_OUT/logs/race-run-$((completed+index)).out" \
        2>"$GATE2_036_OUT/logs/race-run-$((completed+index)).err" &
      run_pids+=("$!")
      gate2_036_run_env "$GATE2_036_HIDEOUT" stop "$GATE2_036_ENV_NAME" \
        >"$GATE2_036_OUT/logs/race-stop-$((completed+index)).out" \
        2>"$GATE2_036_OUT/logs/race-stop-$((completed+index)).err" &
      stop_pids+=("$!")
    done
    for index in "${!run_pids[@]}"; do
      if ! wait "${run_pids[$index]}"; then
        cat "$GATE2_036_OUT/logs/race-run-$((completed+index+1)).err" >&2
        return 1
      fi
    done
    for index in "${!stop_pids[@]}"; do
      if wait "${stop_pids[$index]}"; then
        echo "resource-lifecycle gate2: stop won while anchor session was live" >&2
        return 1
      fi
    done
    kill -0 "$GATE2_036_ANCHOR_PID" 2>/dev/null || {
      echo "resource-lifecycle gate2: anchor was interrupted by attach/stop race" >&2
      return 1
    }
    gate2_036_lima_running
    completed=$((completed + batch))
  done
}

gate2_036_vscode_bundle() {
  local candidate
  for candidate in \
    "/Applications/Visual Studio Code.app" \
    "$HOME/Applications/Visual Studio Code.app"; do
    if [ -d "$candidate" ]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

gate2_036_host_app_pids() {
  [ -n "${GATE2_036_HOST_APP_MAIN:-}" ] || return 0
  [ -n "${GATE2_036_HOST_APP_STATE:-}" ] || return 0
  local canonical_state="$GATE2_036_HOST_APP_STATE" canonical_parent
  if canonical_parent="$(CDPATH= cd -- "$(dirname -- "$GATE2_036_HOST_APP_STATE")" 2>/dev/null && pwd -P)"; then
    canonical_state="$canonical_parent/$(basename -- "$GATE2_036_HOST_APP_STATE")"
  fi
  ps axww -o pid=,command= | awk \
    -v main="$GATE2_036_HOST_APP_MAIN" \
    -v raw="$GATE2_036_HOST_APP_STATE" \
    -v canonical="$canonical_state" '
      {
        pid = $1
        command = $0
        sub(/^[[:space:]]*[0-9]+[[:space:]]+/, "", command)
        if (index(command, main) == 1 &&
            (index(command, "--user-data-dir " raw) > 0 ||
             index(command, "--user-data-dir " canonical) > 0)) {
          print pid
        }
      }
    '
}

gate2_036_stop_host_app() {
  local pid i
  while IFS= read -r pid; do
    [ -n "$pid" ] || continue
    kill -TERM "$pid" 2>/dev/null || true
    for i in $(seq 1 50); do
      kill -0 "$pid" 2>/dev/null || break
      sleep 0.1
    done
  done < <(gate2_036_host_app_pids)
  GATE2_036_HOST_APP_PID=""
}

gate2_036_run_host_handoff() {
  local bundle i pid=""
  bundle="$(gate2_036_vscode_bundle)" || {
    echo "resource-lifecycle gate2: Visual Studio Code is required for the real handoff proof" >&2
    return 2
  }
  GATE2_036_HOST_APP_MAIN="$bundle/Contents/MacOS/Code"
  GATE2_036_HOST_APP_STATE="$GATE2_036_STORE/profiles/default/host-app/state"
  mkdir -p "$GATE2_036_WORKSPACE/src"
  printf 'package main\n' >"$GATE2_036_WORKSPACE/src/main.go"
  gate2_036_run_env "$GATE2_036_HIDEOUT" profile host-app-mode default safe \
    >"$GATE2_036_OUT/logs/host-app-mode.out" 2>"$GATE2_036_OUT/logs/host-app-mode.err"

  gate2_036_run_target code -n -g src/main.go:12:3 \
    >"$GATE2_036_OUT/logs/host-app.out" 2>"$GATE2_036_OUT/logs/host-app.err" &
  local run_pid=$!
  for i in $(seq 1 1500); do
    while IFS= read -r pid; do
      [ -n "$pid" ] && break
    done < <(gate2_036_host_app_pids)
    [ -n "$pid" ] && break
    kill -0 "$run_pid" 2>/dev/null || true
    sleep 0.02
  done
  wait "$run_pid"
  [ -n "$pid" ] || {
    echo "resource-lifecycle gate2: test-owned host application process was not observed" >&2
    return 1
  }
  GATE2_036_HOST_APP_PID="$pid"
  gate2_036_wait_lifecycle '
    any(.lifecycle[]?; .environmentId == $environmentId and
      any(.handoffs[]?; .kind == "hostapp.handoff"))
  ' "host application handoff fact"
  gate2_036_wait_final_stop
  kill -0 "$GATE2_036_HOST_APP_PID" 2>/dev/null || {
    echo "resource-lifecycle gate2: host application did not survive VM stop" >&2
    return 1
  }
  grep -qx 'package main' "$GATE2_036_WORKSPACE/src/main.go" || {
    echo "resource-lifecycle gate2: host handoff resource changed across VM stop" >&2
    return 1
  }
  gate2_036_stop_host_app
}

gate2_036_stage_overlay() {
  local lower="$GATE2_036_HOSTFS_ROOT/retained.txt"
  printf 'host-lower-036\n' >"$lower"
  gate2_036_run_env "$GATE2_036_HIDEOUT" run --profile default --backend lima --network direct \
    --workspace "$GATE2_036_WORKSPACE" --guest-workspace /workspace \
    --fs "overlay:$lower" -- sh -eu -c '
printf "guest-staged-036\n" >"$1"
printf "staged=%s\n" "$(cat "$1")"
' gate2-overlay "$lower" >"$GATE2_036_OUT/logs/overlay-stage.out" 2>"$GATE2_036_OUT/logs/overlay-stage.err"
  grep -q 'staged=guest-staged-036' "$GATE2_036_OUT/logs/overlay-stage.out"
  [ "$(cat "$lower")" = 'host-lower-036' ]
  GATE2_036_OVERLAY_FILE="$(find "$GATE2_036_STORE/sessions" -path '*/hostfs-overlay/objects/*' -type f -print -quit)"
  [ -f "$GATE2_036_OVERLAY_FILE" ] || {
    echo "resource-lifecycle gate2: staged HostFS object is missing" >&2
    return 1
  }
  GATE2_036_OVERLAY_SHA="$(shasum -a 256 "$GATE2_036_OVERLAY_FILE" | awk '{print $1}')"
  gate2_036_wait_lifecycle '
    any(.lifecycle[]?; .environmentId == $environmentId and
      any(.retained[]?; .kind == "hostfs.staged-object"))
  ' "retained HostFS lifecycle fact"
}

gate2_036_run_bridge() {
  local port preview_url listen
  port="$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
  )"
  GATE2_036_BRIDGE_PORT="$port"
  rm -f "$GATE2_036_PREVIEW_URL_FILE"
  rm -f "$GATE2_036_WORKSPACE/.hideout-gate-control/bridge-ready" \
    "$GATE2_036_WORKSPACE/.hideout-gate-control/bridge-release"
  gate2_036_run_env "$GATE2_036_HIDEOUT" run --profile default --backend lima --network direct \
    --workspace "$GATE2_036_WORKSPACE" --guest-workspace /workspace \
    --preview "127.0.0.1:$port" -- sh -eu -c '
python3 -m http.server "$1" --bind 127.0.0.1 >/tmp/hideout-036-preview.log 2>&1 &
server=$!
trap '\''kill "$server" 2>/dev/null || true; wait "$server" 2>/dev/null || true'\'' EXIT
touch /workspace/.hideout-gate-control/bridge-ready
while [ ! -f /workspace/.hideout-gate-control/bridge-release ]; do sleep 0.05; done
' gate2-preview "$port" >"$GATE2_036_OUT/logs/bridge.out" 2>"$GATE2_036_OUT/logs/bridge.err" &
  GATE2_036_BRIDGE_PID=$!
  gate2_036_wait_file "$GATE2_036_WORKSPACE/.hideout-gate-control/bridge-ready" "run-scoped bridge"
  gate2_036_wait_file "$GATE2_036_PREVIEW_URL_FILE" "captured host preview URL"
  preview_url="$(tail -n 1 "$GATE2_036_PREVIEW_URL_FILE")"
  case "$preview_url" in
    http://127.0.0.1:[0-9]*/|http://localhost:[0-9]*/) ;;
    *) echo "resource-lifecycle gate2: captured preview URL is not bounded host loopback" >&2; return 1 ;;
  esac
  listen="${preview_url#http://}"
  listen="${listen%/}"
  GATE2_036_BRIDGE_PORT="${listen##*:}"
  python3 - "$preview_url" <<'PY'
import sys
import urllib.request

opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
with opener.open(sys.argv[1], timeout=2) as response:
    if response.status != 200:
        raise SystemExit("preview bridge returned non-200 status")
    response.read(1024)
PY
  gate2_036_wait_lifecycle '
    any(.lifecycle[]?; .environmentId == $environmentId and
      any(.pins[]?; .kind == "endpoint.run-bridge"))
  ' "run-scoped bridge pin"
  if gate2_036_run_env "$GATE2_036_HIDEOUT" stop "$GATE2_036_ENV_NAME" \
    >"$GATE2_036_OUT/logs/bridge-stop.out" 2>"$GATE2_036_OUT/logs/bridge-stop.err"; then
    echo "resource-lifecycle gate2: explicit stop crossed a live bridge pin" >&2
    return 1
  fi
  touch "$GATE2_036_WORKSPACE/.hideout-gate-control/bridge-release"
  wait "$GATE2_036_BRIDGE_PID"
  GATE2_036_BRIDGE_PID=""
  gate2_036_wait_port "$GATE2_036_BRIDGE_PORT" closed
}

gate2_036_prepare_preview_capture() {
  GATE2_036_PREVIEW_URL_FILE="$GATE2_036_TMP/preview-url.private"
  GATE2_036_BROWSER_CAPTURE="$GATE2_036_TMP/hideout-browser-capture"
  cat >"$GATE2_036_BROWSER_CAPTURE" <<EOF
#!/bin/sh
set -eu
for value do target=\$value; done
printf '%s\\n' "\$target" >$(printf '%q' "$GATE2_036_PREVIEW_URL_FILE")
EOF
  chmod 0700 "$GATE2_036_BROWSER_CAPTURE"
}

gate2_036_port_connects() {
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

gate2_036_wait_port() {
  local port="$1" state="$2" i
  for i in $(seq 1 300); do
    if [ "$state" = open ] && gate2_036_port_connects "$port"; then
      return 0
    fi
    if [ "$state" = closed ] && ! gate2_036_port_connects "$port"; then
      return 0
    fi
    sleep 0.02
  done
  echo "resource-lifecycle gate2: bridge port $port did not become $state" >&2
  return 1
}

gate2_036_wait_final_stop() {
  local start end
  start="$(gate2_036_now_seconds)"
  gate2_036_wait_lifecycle '
    any(.lifecycle[]?; .environmentId == $environmentId and
      .activity == "stopped" and .backendState == "stopped")
  ' "observed automatic stop" 600
  gate2_036_wait_lima_stopped
  end="$(gate2_036_now_seconds)"
  GATE2_036_FINAL_STOP_MS="$(awk -v start="$start" -v end="$end" 'BEGIN { printf "%.3f", (end-start)*1000 }')"
  awk -v value="$GATE2_036_FINAL_STOP_MS" 'BEGIN { exit !(value > 0 && value <= 50000) }'
  if [ -n "${GATE2_036_OVERLAY_FILE:-}" ]; then
    [ "$(shasum -a 256 "$GATE2_036_OVERLAY_FILE" | awk '{print $1}')" = "$GATE2_036_OVERLAY_SHA" ]
    [ "$(cat "$GATE2_036_HOSTFS_ROOT/retained.txt")" = 'host-lower-036' ]
  fi
  if [ -n "${GATE2_036_RETAINED_AUDIT:-}" ]; then
    [ -f "$GATE2_036_RETAINED_AUDIT" ]
    [ "$(shasum -a 256 "$GATE2_036_RETAINED_AUDIT" | awk '{print $1}')" = "$GATE2_036_RETAINED_AUDIT_SHA" ]
  fi
  [ -f "$GATE2_036_RECORD" ] || {
    echo "resource-lifecycle gate2: automatic stop removed the retained environment record" >&2
    return 1
  }
  GATE2_036_STOPPED_GENERATION="$(gate2_036_lifecycle_status | jq -r --arg id "$GATE2_036_ENV_ID" \
    '.lifecycle[] | select(.environmentId == $id) | .startGeneration')"
}

gate2_036_test_restart_reconciliation() {
  gate2_036_run_target true >"$GATE2_036_OUT/logs/restart-prime.out" 2>"$GATE2_036_OUT/logs/restart-prime.err"
  local generation_before
  generation_before="$(gate2_036_lifecycle_status | jq -r --arg id "$GATE2_036_ENV_ID" '.lifecycle[] | select(.environmentId == $id) | .startGeneration')"
  [ "$generation_before" -gt "${GATE2_036_STOPPED_GENERATION:-0}" ] || {
    echo "resource-lifecycle gate2: a new backend boot did not supersede the stopped generation" >&2
    return 1
  }
  gate2_036_stop_daemon

  local real_limactl slow_dir slow_state status_start status_end attach_pid cancel_pid cancel_marker
  real_limactl="$(command -v limactl)"
  slow_dir="$GATE2_036_TMP/limactl-slow"
  slow_state="$GATE2_036_TMP/limactl-slow.state"
  gate2_036_make_limactl_wrapper "$slow_dir" slow-once "$real_limactl" "$slow_state"
  status_start="$(gate2_036_now_seconds)"
  gate2_036_start_daemon "$slow_dir" slow
  status_end="$(gate2_036_now_seconds)"
  GATE2_036_STATUS_READY_MS="$(awk -v start="$status_start" -v end="$status_end" 'BEGIN { printf "%.3f", (end-start)*1000 }')"
  awk -v value="$GATE2_036_STATUS_READY_MS" 'BEGIN { exit !(value <= 3000) }'
  gate2_036_wait_lifecycle 'any(.lifecycle[]?; .environmentId == $environmentId and .reconciliation == "pending")' "pending slow reconciliation" 50
  cancel_marker="$GATE2_036_WORKSPACE/.hideout-gate-control/cancelled-wait-target"
  rm -f "$cancel_marker"
  # Invoke the client directly so cancel_pid is the actual client process.
  # Backgrounding the gate2 shell helper would make it a wrapper PID and a
  # signal could leave its hideout child connected long enough to launch.
  env HIDEOUT_STORE_ROOT="$GATE2_036_STORE" LIMA_HOME="$GATE2_036_LIMA_HOME" \
    HIDEOUT_LINUX_SHIM_PATH="$GATE2_036_BIN/hideout-shim-linux-$GATE2_036_ARCH" \
    HIDEOUT_LINUX_HOSTFSD_PATH="$GATE2_036_BIN/hideout-hostfsd-linux-$GATE2_036_ARCH" \
    HIDEOUT_LINUX_SESSION_SUPERVISOR_PATH="$GATE2_036_BIN/hideout-session-supervisor-linux-$GATE2_036_ARCH" \
    "$GATE2_036_HIDEOUT" run --profile default --backend lima --network direct \
      --workspace "$GATE2_036_WORKSPACE" --guest-workspace /workspace -- \
      sh -eu -c 'touch /workspace/.hideout-gate-control/cancelled-wait-target' \
      >"$GATE2_036_OUT/logs/cancelled-wait.out" 2>"$GATE2_036_OUT/logs/cancelled-wait.err" &
  cancel_pid=$!
  sleep 0.2
  kill -0 "$cancel_pid" 2>/dev/null || {
    echo "attach-reservation gate2: cancellation target bypassed pending reconciliation" >&2
    return 1
  }
  kill "$cancel_pid"
  if wait "$cancel_pid"; then
    echo "attach-reservation gate2: cancelled reconciliation waiter returned success" >&2
    return 1
  fi
  gate2_036_run_target true >"$GATE2_036_OUT/logs/slow-attach.out" 2>"$GATE2_036_OUT/logs/slow-attach.err" &
  attach_pid=$!
  sleep 0.2
  kill -0 "$attach_pid" 2>/dev/null || {
    echo "resource-lifecycle gate2: attach bypassed pending reconciliation" >&2
    return 1
  }
  if ! wait "$attach_pid"; then
    gate2_036_lifecycle_status >"$GATE2_036_OUT/logs/slow-attach-lifecycle.json" 2>/dev/null || true
    echo "resource-lifecycle gate2: attach did not recover after the bounded slow reconciliation" >&2
    return 1
  fi
  gate2_036_wait_lifecycle 'any(.lifecycle[]?; .environmentId == $environmentId and .reconciliation == "complete")' "slow reconciliation completion"
  [ ! -e "$cancel_marker" ] || {
    echo "attach-reservation gate2: cancelled waiter launched its target" >&2
    return 1
  }
  gate2_036_lifecycle_status | jq -e --arg environmentId "$GATE2_036_ENV_ID" '
    all(.lifecycle[]? | select(.environmentId == $environmentId); (.establishingSessions // 0) == 0)
  ' >/dev/null
  gate2_036_stop_daemon

  local fail_dir fail_state retry_start retry_end
  fail_dir="$GATE2_036_TMP/limactl-fail"
  fail_state="$GATE2_036_TMP/limactl-fail.state"
  gate2_036_make_limactl_wrapper "$fail_dir" fail-once "$real_limactl" "$fail_state"
  gate2_036_start_daemon "$fail_dir" fail-once
  gate2_036_wait_lifecycle 'any(.lifecycle[]?; .environmentId == $environmentId and .reconciliation == "blocked" and .reasonCode == "inventory-unavailable")' "transient blocked reconciliation"
  retry_start="$(gate2_036_now_seconds)"
  gate2_036_run_env "$GATE2_036_HIDEOUT" daemon reconcile --env "$GATE2_036_ENV_NAME" \
    >"$GATE2_036_OUT/logs/reconcile-retry.out" 2>"$GATE2_036_OUT/logs/reconcile-retry.err"
  gate2_036_wait_lifecycle 'any(.lifecycle[]?; .environmentId == $environmentId and .reconciliation == "complete")' "same-epoch reconciliation retry"
  retry_end="$(gate2_036_now_seconds)"
  GATE2_036_RETRY_MS="$(awk -v start="$retry_start" -v end="$retry_end" 'BEGIN { printf "%.3f", (end-start)*1000 }')"
  awk -v value="$GATE2_036_RETRY_MS" 'BEGIN { exit !(value > 0 && value <= 5000) }'
  local generation_after
  generation_after="$(gate2_036_lifecycle_status | jq -r --arg id "$GATE2_036_ENV_ID" '.lifecycle[] | select(.environmentId == $id) | .startGeneration')"
  [ "$generation_after" -eq "$generation_before" ]
  local first_deadline second_deadline
  first_deadline="$(gate2_036_lifecycle_status | jq -r --arg id "$GATE2_036_ENV_ID" \
    '.lifecycle[] | select(.environmentId == $id) | .idleDeadline')"
  gate2_036_run_env "$GATE2_036_HIDEOUT" daemon reconcile --env "$GATE2_036_ENV_NAME" \
    >"$GATE2_036_OUT/logs/reconcile-repeat.out" 2>"$GATE2_036_OUT/logs/reconcile-repeat.err"
  second_deadline="$(gate2_036_lifecycle_status | jq -r --arg id "$GATE2_036_ENV_ID" \
    '.lifecycle[] | select(.environmentId == $id) | .idleDeadline')"
  [ -n "$first_deadline" ] && [ "$first_deadline" = "$second_deadline" ]
}

gate2_040_test_restart_before_owner() {
	gate2_036_stop_daemon
	local real_limactl slow_dir slow_state attach_pid marker
	real_limactl="$(command -v limactl)"
	slow_dir="$GATE2_036_TMP/limactl-restart-before-owner"
	slow_state="$GATE2_036_TMP/limactl-restart-before-owner.state"
	marker="$GATE2_036_WORKSPACE/.hideout-gate-control/restart-before-owner-target"
	rm -f "$marker"
	gate2_036_make_limactl_wrapper "$slow_dir" slow-once "$real_limactl" "$slow_state"
	gate2_036_start_daemon "$slow_dir" restart-before-owner
	gate2_036_wait_lifecycle 'any(.lifecycle[]?; .environmentId == $environmentId and .reconciliation == "pending")' "restart-before-owner pending reconciliation" 50
	gate2_036_run_target sh -eu -c 'touch /workspace/.hideout-gate-control/restart-before-owner-target' \
	  >"$GATE2_036_OUT/logs/restart-before-owner.out" 2>"$GATE2_036_OUT/logs/restart-before-owner.err" &
	attach_pid=$!
	sleep 0.2
	kill -0 "$attach_pid" 2>/dev/null || {
	  echo "attach-reservation gate2: pre-owner run bypassed reconciliation" >&2
	  return 1
	}
	gate2_036_stop_daemon
	if wait "$attach_pid"; then
	  echo "attach-reservation gate2: pre-owner run survived daemon restart as success" >&2
	  return 1
	fi
	[ ! -e "$marker" ] || {
	  echo "attach-reservation gate2: daemon restart launched a pre-owner target" >&2
	  return 1
	}
	gate2_036_start_daemon "" restart-before-owner-recovery
	gate2_036_wait_lifecycle 'any(.lifecycle[]?; .environmentId == $environmentId and .reconciliation == "complete")' "restart-before-owner recovery"
	gate2_036_run_target true >"$GATE2_036_OUT/logs/restart-before-owner-recovery-run.out" \
	  2>"$GATE2_036_OUT/logs/restart-before-owner-recovery-run.err"
}

gate2_036_test_stop_unknown() {
  gate2_036_run_target true >"$GATE2_036_OUT/logs/unknown-prime.out" 2>"$GATE2_036_OUT/logs/unknown-prime.err"
  gate2_036_stop_daemon
  local wrapper_dir state real_limactl
  wrapper_dir="$GATE2_036_TMP/limactl-unknown"
  state="$GATE2_036_TMP/limactl-stop.state"
  real_limactl="$(command -v limactl)"
  gate2_036_make_limactl_wrapper "$wrapper_dir" stop-unknown "$real_limactl" "$state"
  gate2_036_start_daemon "$wrapper_dir" stop-unknown
  gate2_036_wait_lifecycle '
    any(.lifecycle[]?; .environmentId == $environmentId and
      .activity == "stopping-unknown" and
      .reasonCode == "backend-stop-observation-timeout")
  ' "ambiguous stop observation" 600
  if gate2_036_run_target true \
    >"$GATE2_036_OUT/logs/unknown-attach.out" 2>"$GATE2_036_OUT/logs/unknown-attach.err"; then
    echo "resource-lifecycle gate2: attach entered a stopping-unknown incarnation" >&2
    return 1
  fi
  gate2_036_stop_daemon
  rm -f "$state"
  gate2_036_start_daemon "" recovery
  gate2_036_wait_lifecycle '
    any(.lifecycle[]?; .environmentId == $environmentId and
      .activity == "stopped" and .backendState == "stopped")
  ' "restart recovery of ambiguous stop"
}

gate2_resource_lifecycle_run() {
  local root="$1" out="$2" baseline_commit="$3" samples="$4" warmups="$5" races="$6" short_tmp
  for tool in go jq limactl shasum perl python3 git comm; do gate2_036_require "$tool"; done
  [ "$(uname -s)" = Darwin ] && [ "$(uname -m)" = arm64 ] || {
    echo "resource-lifecycle gate2: real evidence requires macOS arm64" >&2
    return 2
  }

  GATE2_036_OUT="$out"
  short_tmp="$(gate2_036_short_tmpdir)"
  GATE2_036_TMP="$(mktemp -d "${TMPDIR:-/tmp}/hideout-036-gate2.XXXXXX")"
  GATE2_036_STORE="$(mktemp -d "$short_tmp/h36-store.XXXXXX")"
  GATE2_036_WORKSPACE="$GATE2_036_TMP/workspace"
  GATE2_036_HOSTFS_ROOT="$(mktemp -d "$short_tmp/h36-hostfs.XXXXXX")"
  GATE2_036_HOSTFS_ROOT="$(cd "$GATE2_036_HOSTFS_ROOT" && pwd -P)"
  GATE2_036_BIN="$GATE2_036_TMP/bin"
  GATE2_036_LIMA_HOME="${HIDEOUT_036_LIMA_HOME:-${LIMA_HOME:-$HOME/.lima}}"
  GATE2_036_ARCH="$(go env GOARCH)"
  GATE2_036_HIDEOUT="$GATE2_036_BIN/hideout"
  GATE2_036_DAEMON_PID=""
  GATE2_036_ANCHOR_PID=""
  GATE2_036_SECOND_PID=""
  GATE2_036_BRIDGE_PID=""
  GATE2_036_BRIDGE_PORT=""
  GATE2_036_BROWSER_CAPTURE=""
  GATE2_036_PREVIEW_URL_FILE=""
  GATE2_036_HOST_APP_PID=""
  GATE2_036_HOST_APP_MAIN=""
  GATE2_036_HOST_APP_STATE=""
  GATE2_036_OVERLAY_FILE=""
  GATE2_036_OVERLAY_SHA=""
  GATE2_036_RETAINED_AUDIT=""
  GATE2_036_RETAINED_AUDIT_SHA=""
  mkdir -p "$GATE2_036_OUT/logs" "$GATE2_036_WORKSPACE/.hideout-gate-control"
  gate2_036_cleanup() {
    touch "$GATE2_036_WORKSPACE/.hideout-gate-control/anchor-release" \
      "$GATE2_036_WORKSPACE/.hideout-gate-control/second-release" \
      "$GATE2_036_WORKSPACE/.hideout-gate-control/bridge-release" 2>/dev/null || true
    for pid in "$GATE2_036_ANCHOR_PID" "$GATE2_036_SECOND_PID" "$GATE2_036_BRIDGE_PID"; do
      [ -z "$pid" ] || kill "$pid" 2>/dev/null || true
    done
    gate2_036_stop_host_app || true
    gate2_036_stop_daemon || true
    if [ -n "${GATE2_036_INSTANCE:-}" ]; then
      LIMA_HOME="$GATE2_036_LIMA_HOME" limactl stop -f "$GATE2_036_INSTANCE" >/dev/null 2>&1 || true
      LIMA_HOME="$GATE2_036_LIMA_HOME" limactl delete -f "$GATE2_036_INSTANCE" >/dev/null 2>&1 || true
    fi
    if [ "${HIDEOUT_036_KEEP_FAILED_STATE:-0}" = "1" ]; then
      printf '%s\n' "resource-lifecycle gate2: retained store=$GATE2_036_STORE temp=$GATE2_036_TMP hostfs=$GATE2_036_HOSTFS_ROOT" >&2
      return
    fi
    rm -rf "$GATE2_036_STORE" "$GATE2_036_HOSTFS_ROOT" "$GATE2_036_TMP"
  }
  trap gate2_036_cleanup EXIT

  gate2_036_stage build-candidate
  gate2_036_build_tree "$root" "$GATE2_036_BIN" "$GATE2_036_ARCH"
  gate2_036_stage initialize-real-environment
  gate2_036_init_profile "$GATE2_036_HIDEOUT" "$GATE2_036_STORE" "$GATE2_036_LIMA_HOME" \
    "$GATE2_036_BIN" "$GATE2_036_ARCH" "$GATE2_036_OUT/logs/init.out"
  gate2_036_run_target true >"$GATE2_036_OUT/logs/first-run.out" 2>"$GATE2_036_OUT/logs/first-run.err"
  local record
  record="$(find "$GATE2_036_STORE/environments" -name environment.json -type f -print -quit)"
  [ -f "$record" ]
  GATE2_036_RECORD="$record"
  GATE2_036_ENV_ID="$(jq -r '.id' "$record")"
  GATE2_036_ENV_NAME="$(jq -r '.name' "$record")"
  GATE2_036_INSTANCE="$(jq -r '.instanceName' "$record")"

  LIMA_HOME="$GATE2_036_LIMA_HOME" limactl shell "$GATE2_036_INSTANCE" -- cat /proc/sys/kernel/random/boot_id \
    >"$GATE2_036_OUT/logs/boot-id.private"
  grep -Eq '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' "$GATE2_036_OUT/logs/boot-id.private"

  if [ "${HIDEOUT_036_PROBE_SKIP_PROVEN_PREFIX:-0}" != "1" ]; then
    gate2_036_stage retained-state-and-host-handoff
    gate2_036_seed_retained_state
    gate2_036_run_host_handoff
    gate2_036_verify_retained_state_after_restart

    gate2_036_stage pty-crash-and-recovery
    go -C "$root" run ./test/e2e/sessionpty --hideout "$GATE2_036_HIDEOUT" \
      --store "$GATE2_036_STORE" --lima-home "$GATE2_036_LIMA_HOME" \
      --workspace "$GATE2_036_WORKSPACE" --profile default \
      --out "$GATE2_036_OUT/logs/session-pty.json"
    jq -e '.status == "passed" and .daemonCrashClients == 2 and .targetsReaped == true and
      .restartFailedClosed == true and .explicitRecovery == true and .postRecoveryRun == true' \
      "$GATE2_036_OUT/logs/session-pty.json" >/dev/null
  fi

  gate2_036_stage concurrent-sessions-and-attach-stop
  gate2_036_stop_daemon
  gate2_036_prepare_preview_capture
  export HIDEOUT_BROWSER_PATH="$GATE2_036_BROWSER_CAPTURE"
  gate2_036_start_daemon "" bridge-capture
  unset HIDEOUT_BROWSER_PATH
  gate2_036_wait_lifecycle '
    any(.lifecycle[]?; .environmentId == $environmentId and .reconciliation == "complete")
  ' "bridge-capture daemon reconciliation"
  gate2_036_run_target true \
    >"$GATE2_036_OUT/logs/restart-after-pty.out" 2>"$GATE2_036_OUT/logs/restart-after-pty.err"
  gate2_036_start_marker_session anchor
  gate2_036_start_marker_session second
  gate2_036_wait_lifecycle '
    any(.lifecycle[]?; .environmentId == $environmentId and
      ([.pins[]? | select(.kind == "run.session")] | length) >= 2)
  ' "two sibling session pins"

  gate2_036_run_attach_stop_races "$races"
  gate2_036_stage_overlay
  gate2_036_run_bridge
  gate2_036_release_marker_session second
  kill -0 "$GATE2_036_ANCHOR_PID" 2>/dev/null
  gate2_036_probe_marker_session anchor
  gate2_036_lima_running
  gate2_036_release_marker_session anchor
  gate2_036_wait_final_stop

  gate2_036_stage restart-and-ambiguous-stop
  gate2_036_test_restart_reconciliation
	gate2_040_test_restart_before_owner
  gate2_036_wait_final_stop
  gate2_036_test_stop_unknown

  gate2_036_stage exact-baseline-performance
  gate2_036_run_performance "$root" "$out" "$GATE2_036_WORKSPACE" "$GATE2_036_LIMA_HOME" \
    "$GATE2_036_BIN" "$baseline_commit" "$samples" "$warmups" "$GATE2_036_ARCH"

  local commit dirty result_status prefix_checks
  commit="$(git -C "$root" rev-parse HEAD)"
  if [ -n "$(git -C "$root" status --porcelain --untracked-files=normal)" ]; then dirty=true; else dirty=false; fi
  result_status=passed
  prefix_checks=true
  if [ "${probe:-0}" = "1" ]; then
    result_status=probe-passed
  fi
  if [ "${HIDEOUT_036_PROBE_SKIP_PROVEN_PREFIX:-0}" = "1" ]; then
    result_status=probe-passed
    prefix_checks=false
  fi
  jq -n --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" --arg commit "$commit" \
    --arg status "$result_status" --argjson prefixChecks "$prefix_checks" \
    --argjson dirty "$dirty" --argjson races "$races" \
    --argjson finalStopMs "$GATE2_036_FINAL_STOP_MS" --argjson statusReadyMs "$GATE2_036_STATUS_READY_MS" \
    --argjson reconciliationRetryMs "$GATE2_036_RETRY_MS" --argjson shutdownMs "$GATE2_036_SHUTDOWN_MS" '
    {schema:"hideout.lifecycle-real-gate2/v1",status:$status,generatedAt:$generatedAt,
     commit:$commit,dirty:$dirty,backend:"lima",hostOS:"darwin",hostArch:"arm64",
     metrics:{attachStopRaces:$races,finalStopMs:$finalStopMs,statusReadyMs:$statusReadyMs,
       reconciliationRetryMs:$reconciliationRetryMs,shutdownMs:$shutdownMs},
     checks:{attachStopRaceSafe:true,auditEvidenceRetained:$prefixChecks,
       automaticStopNonDestructive:true,bootIdentityObserved:true,
       bridgePinsEnvironment:true,runBridgeClosed:true,exactObservedStop:true,
       explicitStaleRecovery:$prefixChecks,finalSessionStops:true,
       guestDiskRetained:$prefixChecks,hostHandoffIndependent:$prefixChecks,
       newBootGenerationObserved:true,profileCacheRetained:$prefixChecks,reconciliationRetry:true,
       restartFreshGraceAtMostOnce:true,restartNoInheritedAuthority:$prefixChecks,
	   retainedOverlayPreserved:true,siblingSessionPreserved:true,
	   attachWaitsForReconciliation:true,cancellationBeforeOwnerClean:true,
	   restartBeforeOwnerClean:true,restartAfterOwnerFailClosed:$prefixChecks,
       slowProbeDoesNotBlockStatus:true,stopUnknownBlocksAttach:true},
     nonClaims:{guestRootContainment:"not-claimed",hostAppTermination:"not-owned"}}
  ' >"$out/result.json"

  gate2_036_cleanup
  trap - EXIT
}
