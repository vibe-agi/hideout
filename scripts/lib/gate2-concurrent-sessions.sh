#!/usr/bin/env bash

gate2_concurrent_sessions_not_run() {
  local out="$1"
  mkdir -p "$out"
  cat >"$out/result.json" <<'EOF'
{
  "schema": "hideout.concurrent-sessions-gate2/v1",
  "status": "not-run",
  "reason": "real macOS arm64 Lima concurrency gate was not requested"
}
EOF
}

gate2_034_require() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "concurrent-sessions gate2: missing required command: $1" >&2
    return 127
  }
}

gate2_034_wait_file() {
  local path="$1" description="$2" attempts="${3:-600}" i
  i=0
  while [ "$i" -lt "$attempts" ]; do
    [ -f "$path" ] && return 0
    sleep 0.1
    i=$((i + 1))
  done
  echo "concurrent-sessions gate2: timed out waiting for $description ($path)" >&2
  return 1
}

gate2_034_sha256() {
  shasum -a 256 "$1" | awk '{print $1}'
}

gate2_034_dirty() {
  if [ -n "$(git status --porcelain --untracked-files=normal 2>/dev/null)" ]; then
    printf 'true\n'
  else
    printf 'false\n'
  fi
}

gate2_034_delete_temp_tree() {
  local target="$1"
  [ -e "$target" ] || return 0
  [ -d "$target" ] && [ ! -L "$target" ] || {
    echo "concurrent-sessions gate2: refusing non-directory cleanup target: $target" >&2
    return 1
  }
  case "$(basename "$target")" in
    hideout-034-gate2.* | h34-store.* | h34.* | hideout-034-hostfs.*)
      find "$target" -depth -delete
      ;;
    *)
      echo "concurrent-sessions gate2: refusing unexpected cleanup target: $target" >&2
      return 1
      ;;
  esac
}

gate2_concurrent_sessions_run() {
  local root="$1" out="$2" samples="$3" warmups="$4"
  gate2_034_require go
  gate2_034_require jq
  gate2_034_require limactl
  gate2_034_require shasum
  gate2_034_require ssh
  [ "$(uname -s)" = "Darwin" ] || {
    echo "concurrent-sessions gate2: real claim requires macOS" >&2
    return 2
  }
  [ "$(uname -m)" = "arm64" ] || {
    echo "concurrent-sessions gate2: real claim requires arm64" >&2
    return 2
  }

  mkdir -p "$out/logs"
  out="$(cd "$out" && pwd -P)"
  chmod 0700 "$out" "$out/logs"
  local tmp store workspace hostfs_root hostfs_file bin hideout lima_home profile short_tmp
  # Lima's socket path has a 104-byte platform limit. Keep this explicit
  # override separate from ordinary TMPDIR-backed test artifacts. The daemon
  # session socket has the same constraint, so its store belongs here too.
  short_tmp="${HIDEOUT_LIMA_SHORT_TMPDIR:-/tmp}"
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-034-gate2.XXXXXX")"
  store="$(mktemp -d "$short_tmp/h34-store.XXXXXX")"
  workspace="$tmp/workspace"
  hostfs_root="$(mktemp -d "$short_tmp/hideout-034-hostfs.XXXXXX")"
  hostfs_root="$(cd "$hostfs_root" && pwd -P)"
  hostfs_file="$hostfs_root/overlay.txt"
  bin="$tmp/bin"
  lima_home="$(mktemp -d "$short_tmp/h34.XXXXXX")"
  profile="g34"
  mkdir -p "$store" "$workspace" "$hostfs_root" "$bin"
  printf 'host-lower\n' >"$hostfs_file"
  printf 'gate2-a-marker-034\n' >"$workspace/a.command-marker"
  hideout="$bin/hideout"
	local arch
	arch="$(go env GOARCH)"
	if [ -n "${HIDEOUT_RELEASE_BINARY:-}" ]; then
		hideout="$HIDEOUT_RELEASE_BINARY"
		[ -x "$hideout" ] || { echo "concurrent-sessions gate2: packaged hideout binary is not executable" >&2; return 2; }
		: "${HIDEOUT_LINUX_SHIM_PATH:?packaged Linux shim is required}"
		: "${HIDEOUT_LINUX_HOSTFSD_PATH:?packaged Linux HostFS helper is required}"
		: "${HIDEOUT_LINUX_SESSION_SUPERVISOR_PATH:?packaged Linux session supervisor is required}"
		: "${HIDEOUT_LINUX_OBSERVER_PATH:?packaged Linux observer is required}"
		: "${HIDEOUT_LINUX_WORKSPACE_PORTAL_PATH:?packaged Linux workspace portal is required}"
		[ -x "$HIDEOUT_LINUX_SHIM_PATH" ] && [ -x "$HIDEOUT_LINUX_HOSTFSD_PATH" ] &&
			[ -x "$HIDEOUT_LINUX_SESSION_SUPERVISOR_PATH" ] &&
			[ -x "$HIDEOUT_LINUX_OBSERVER_PATH" ] &&
			[ -x "$HIDEOUT_LINUX_WORKSPACE_PORTAL_PATH" ] || {
			echo "concurrent-sessions gate2: packaged Linux helpers are not executable" >&2
			return 2
		}
		bin="$(dirname "$hideout")"
		else
			(
				cd "$root" || exit
			go build -o "$hideout" ./cmd/hideout
		)
		HIDEOUT_LINUX_SHIM_PATH="$bin/hideout-shim-linux-$arch"
		HIDEOUT_LINUX_HOSTFSD_PATH="$bin/hideout-hostfsd-linux-$arch"
		HIDEOUT_LINUX_SESSION_SUPERVISOR_PATH="$bin/hideout-session-supervisor-linux-$arch"
		HIDEOUT_LINUX_OBSERVER_PATH="$bin/hideout-observer-linux-$arch"
		HIDEOUT_LINUX_WORKSPACE_PORTAL_PATH="$bin/hideout-workspace-portal-linux-$arch"
		"$hideout" shim build-linux --out "$HIDEOUT_LINUX_SHIM_PATH" --goarch "$arch" --source "$root" >/dev/null
		"$hideout" hostfsd build-linux --out "$HIDEOUT_LINUX_HOSTFSD_PATH" --goarch "$arch" --source "$root" >/dev/null
		go -C "$root" run ./internal/helperbin/cmd/build-session-supervisor \
			--out "$HIDEOUT_LINUX_SESSION_SUPERVISOR_PATH" --goarch "$arch" --source "$root" >/dev/null
		go -C "$root" run ./internal/helperbin/cmd/build-observer \
			--out "$HIDEOUT_LINUX_OBSERVER_PATH" --goarch "$arch" --source "$root" >/dev/null
		go -C "$root" run ./internal/helperbin/cmd/build-workspace-portal \
			--out "$HIDEOUT_LINUX_WORKSPACE_PORTAL_PATH" --goarch "$arch" --source "$root" >/dev/null
	fi
	export HIDEOUT_LINUX_SHIM_PATH HIDEOUT_LINUX_HOSTFSD_PATH
	export HIDEOUT_LINUX_SESSION_SUPERVISOR_PATH HIDEOUT_LINUX_OBSERVER_PATH
	export HIDEOUT_LINUX_WORKSPACE_PORTAL_PATH

	local first_pid="" second_pid="" third_pid="" instance=""
  GATE2_034_CLEANUP_TMP="$tmp"
  GATE2_034_CLEANUP_STORE="$store"
  GATE2_034_CLEANUP_WORKSPACE="$workspace"
  GATE2_034_CLEANUP_HOSTFS_ROOT="$hostfs_root"
  GATE2_034_CLEANUP_HIDEOUT="$hideout"
  GATE2_034_CLEANUP_LIMA_HOME="$lima_home"
		GATE2_034_CLEANUP_FIRST_PID=""
		GATE2_034_CLEANUP_SECOND_PID=""
		GATE2_034_CLEANUP_THIRD_PID=""
  local gate2_034_completed=0
  gate2_034_cleanup() {
    # The function is also the EXIT trap and must capture the triggering status.
    # shellcheck disable=SC2320
    local cleanup_status=$?
		touch "$GATE2_034_CLEANUP_WORKSPACE/a.release" \
			"$GATE2_034_CLEANUP_WORKSPACE/b.probe" \
			"$GATE2_034_CLEANUP_WORKSPACE/b.release" \
			"$GATE2_034_CLEANUP_WORKSPACE/c.release" 2>/dev/null || true
		for pid in "$GATE2_034_CLEANUP_FIRST_PID" "$GATE2_034_CLEANUP_SECOND_PID" "$GATE2_034_CLEANUP_THIRD_PID"; do
      if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
        kill "$pid" 2>/dev/null || true
        wait "$pid" 2>/dev/null || true
      fi
    done
    if [ "${HIDEOUT_034_KEEP_TMP:-0}" = "1" ]; then
      echo "concurrent-sessions gate2: retained tmp=$GATE2_034_CLEANUP_TMP lima=$GATE2_034_CLEANUP_LIMA_HOME" >&2
    else
      if [ -x "$GATE2_034_CLEANUP_HIDEOUT" ]; then
        HIDEOUT_STORE_ROOT="$GATE2_034_CLEANUP_STORE" LIMA_HOME="$GATE2_034_CLEANUP_LIMA_HOME" \
          "$GATE2_034_CLEANUP_HIDEOUT" clean >/dev/null 2>&1 || true
      fi
      local cleanup_instance
      while IFS= read -r cleanup_instance; do
        [ -n "$cleanup_instance" ] || continue
        LIMA_HOME="$GATE2_034_CLEANUP_LIMA_HOME" limactl delete --force --tty=false "$cleanup_instance" >/dev/null 2>&1 || true
      done < <(LIMA_HOME="$GATE2_034_CLEANUP_LIMA_HOME" limactl list --quiet 2>/dev/null || true)
      gate2_034_delete_temp_tree "$GATE2_034_CLEANUP_TMP"
      gate2_034_delete_temp_tree "$GATE2_034_CLEANUP_STORE"
      gate2_034_delete_temp_tree "$GATE2_034_CLEANUP_LIMA_HOME"
      gate2_034_delete_temp_tree "$GATE2_034_CLEANUP_HOSTFS_ROOT"
    fi
    if [ "$cleanup_status" -eq 0 ] &&
      [ "$gate2_034_completed" != "1" ]; then
      echo "concurrent-sessions gate2: run ended before its success line" >&2
      exit 1
    fi
  }
  trap gate2_034_cleanup EXIT

  HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" init \
    --profile "$profile" --template dev --backend lima --network direct \
    --runtime developer-standard --no-input >"$out/logs/init.out" 2>"$out/logs/init.err"

  # The single-quoted target program is intentionally passed verbatim to the VM.
  # shellcheck disable=SC2016
  HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run \
    --verbose --profile "$profile" --backend lima --network direct --workspace "$workspace" --guest-workspace /workspace \
    --fs "overlay:$hostfs_file" -- sh -eu -c '
printf "%s\n" "$HIDEOUT_SESSION_ID" > /workspace/a.session
printf "%s\n" "$$" > /workspace/a.pid
readlink /proc/self/ns/pid > /workspace/a.pid-ns
readlink /proc/self/ns/mnt > /workspace/a.mnt-ns
id -u > /workspace/a.uid
grep " /proc " /proc/mounts > /workspace/a.proc-mount
printf "a-fd\n" > /workspace/a.fd
exec 9</workspace/a.fd
printf "first-staged\n" > "$1"
cat "$1" > /workspace/a.overlay
touch /workspace/a.ready
while [ ! -f /workspace/b.ready ]; do sleep 0.05; done
sibling=$(cat /workspace/b.session)
if grep -a -R -F "$sibling" /proc/[0-9]*/environ >/dev/null 2>&1; then
  echo "sibling environment visible through proc" >&2
  exit 41
fi
if [ -e "/hideout/runtime/sessions/$sibling" ]; then
  echo "sibling runtime visible" >&2
  exit 42
fi
touch /workspace/a.checked
while [ ! -f /workspace/a.release ]; do sleep 0.05; done
' "$(cat "$workspace/a.command-marker")" "$hostfs_file" >"$out/logs/a.out" 2>"$out/logs/a.err" &
  first_pid=$!
  GATE2_034_CLEANUP_FIRST_PID="$first_pid"
  gate2_034_wait_file "$workspace/a.ready" "first session"

  # The single-quoted target program is intentionally passed verbatim to the VM.
  # shellcheck disable=SC2016
  HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run \
    --verbose --profile "$profile" --backend lima --network direct --workspace "$workspace" --guest-workspace /workspace \
    --fs "read:$hostfs_file" -- sh -eu -c '
printf "%s\n" "$HIDEOUT_SESSION_ID" > /workspace/b.session
printf "%s\n" "$$" > /workspace/b.pid
readlink /proc/self/ns/pid > /workspace/b.pid-ns
readlink /proc/self/ns/mnt > /workspace/b.mnt-ns
id -u > /workspace/b.uid
test "$HIDEOUT_SESSION_ID" != "$(cat /workspace/a.session)"
test "$(cat "$1")" = "host-lower"
grep " /proc " /proc/mounts > /workspace/b.proc-mount
ip route show > /workspace/b.route-before
if grep -a -R -F "$(cat /workspace/a.session)" /proc/[0-9]*/environ >/dev/null 2>&1; then
  echo "sibling environment visible through proc" >&2
  exit 43
fi
marker=$(cat /workspace/a.command-marker)
if grep -a -R -F "$marker" /proc/[0-9]*/cmdline >/dev/null 2>&1; then
  echo "sibling command line visible through proc" >&2
  exit 45
fi
if find /proc/[0-9]*/fd -type l -exec readlink {} \; 2>/dev/null | grep -Fqx /workspace/a.fd; then
  echo "sibling file descriptor visible through proc" >&2
  exit 46
fi
if [ -e "/hideout/runtime/sessions/$(cat /workspace/a.session)" ]; then
  echo "sibling runtime visible" >&2
  exit 44
fi
touch /workspace/b.ready
while [ ! -f /workspace/a.checked ]; do sleep 0.05; done
while [ ! -f /workspace/b.probe ]; do sleep 0.05; done
ip route show > /workspace/b.route-after
cmp /workspace/b.route-before /workspace/b.route-after
printf "sibling-alive\n" > /workspace/b.alive
while [ ! -f /workspace/b.release ]; do sleep 0.05; done
' gate2-b "$hostfs_file" >"$out/logs/b.out" 2>"$out/logs/b.err" &
	second_pid=$!
	GATE2_034_CLEANUP_SECOND_PID="$second_pid"
	gate2_034_wait_file "$workspace/b.ready" "second session"
	gate2_034_wait_file "$workspace/a.checked" "bidirectional namespace checks"
	test "$(cat "$workspace/a.pid-ns")" != "$(cat "$workspace/b.pid-ns")"
	test "$(cat "$workspace/a.mnt-ns")" != "$(cat "$workspace/b.mnt-ns")"
	test "$(cat "$workspace/a.uid")" -ne 0
	test "$(cat "$workspace/b.uid")" -ne 0
	grep -Eq 'proc .*/proc proc ' "$workspace/a.proc-mount"
	grep -Eq 'proc .*/proc proc ' "$workspace/b.proc-mount"

		# The single-quoted target program is intentionally passed verbatim to the VM.
		# shellcheck disable=SC2016
		HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run \
		--verbose --profile "$profile" --backend lima --network direct --workspace "$workspace" --guest-workspace /workspace \
		-- sh -eu -c '
printf "%s\n" "$HIDEOUT_SESSION_ID" > /workspace/c.session
readlink /proc/self/ns/pid > /workspace/c.pid-ns
readlink /proc/self/ns/mnt > /workspace/c.mnt-ns
id -u > /workspace/c.uid
test "$HIDEOUT_SESSION_ID" != "$(cat /workspace/a.session)"
test "$HIDEOUT_SESSION_ID" != "$(cat /workspace/b.session)"
touch /workspace/c.ready
while [ ! -f /workspace/c.release ]; do sleep 0.05; done
' >"$out/logs/c.out" 2>"$out/logs/c.err" &
	third_pid=$!
	GATE2_034_CLEANUP_THIRD_PID="$third_pid"
	gate2_034_wait_file "$workspace/c.ready" "third session"
	test "$(cat "$workspace/c.pid-ns")" != "$(cat "$workspace/a.pid-ns")"
	test "$(cat "$workspace/c.pid-ns")" != "$(cat "$workspace/b.pid-ns")"
	test "$(cat "$workspace/c.mnt-ns")" != "$(cat "$workspace/a.mnt-ns")"
	test "$(cat "$workspace/c.mnt-ns")" != "$(cat "$workspace/b.mnt-ns")"
	test "$(cat "$workspace/c.uid")" -ne 0

	test "$(cat "$hostfs_file")" = "host-lower"
	test "$(cat "$workspace/a.overlay")" = "first-staged"
	HIDEOUT_STORE_ROOT="$store" "$hideout" env list >"$out/logs/env-active.out"
	awk 'NR == 2 { exit !($7 == 3 && $6 == "running") }' "$out/logs/env-active.out"
  local env_name
  env_name="$(awk 'NR == 2 {print $1}' "$out/logs/env-active.out")"
  [ -n "$env_name" ]
  if HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" stop "$env_name" \
    >"$out/logs/stop-live.out" 2>"$out/logs/stop-live.err"; then
    echo "concurrent-sessions gate2: stop accepted live owners" >&2
    return 1
  fi
  grep -E 'active session|active owner|session owner' "$out/logs/stop-live.err" >/dev/null

  instance="$(jq -r '.instanceName' "$store/environments"/*/environment.json)"
  [ -n "$instance" ] && [ "$instance" != "null" ]
  local root_probe ssh_config
  ssh_config="$(LIMA_HOME="$lima_home" limactl list --format '{{.SSHConfigFile}}' "$instance")"
  [ -s "$ssh_config" ]
  root_probe="$(ssh -F "$ssh_config" \
    -o BatchMode=yes \
    -o User=root \
    -o ControlMaster=no \
    -o ControlPath=none \
    "lima-$instance" sh -s <<'ROOTSH'
seen=0
for f in /proc/[0-9]*/environ; do
  if grep -a -q HIDEOUT_SESSION_ID 2>/dev/null <"$f"; then seen=$((seen + 1)); fi
done
printf "%s\n" "$seen"
ROOTSH
  )"
  root_probe="$(printf '%s\n' "$root_probe" | tr -d '\r' | tail -n1)"
  case "$root_probe" in
    ''|*[!0-9]*) echo "concurrent-sessions gate2: guest-root non-claim probe was not observable" >&2; return 1 ;;
  esac
	[ "$root_probe" -ge 3 ] || {
		echo "concurrent-sessions gate2: guest-root positive-control did not observe all three ordinary targets" >&2
		return 1
	}
	printf 'guest-root visibility count=%s (non-claim only)\n' "$root_probe" >"$out/logs/guest-root-nonclaim.out"

	touch "$workspace/c.release"
	wait "$third_pid"
	third_pid=""
	GATE2_034_CLEANUP_THIRD_PID=""
	HIDEOUT_STORE_ROOT="$store" "$hideout" env list >"$out/logs/env-two.out"
	awk 'NR == 2 { exit !($7 == 2 && $6 == "running") }' "$out/logs/env-two.out"

	local interrupted_session terminated_probe owner_release_start owner_release_end owner_release_ms owner_reconciled
	local owner_reconcile_attempt terminated_probe_attempt
	interrupted_session="$(cat "$workspace/a.session")"
	owner_release_start="$(gate2_034_now_seconds)"
	kill -KILL "$first_pid"
	if wait "$first_pid"; then
    echo "concurrent-sessions gate2: interrupted first session returned success unexpectedly" >&2
    return 1
  fi
	first_pid=""
	GATE2_034_CLEANUP_FIRST_PID=""
	owner_reconciled=0
	owner_reconcile_attempt=0
	while [ "$owner_reconcile_attempt" -lt 20 ]; do
		if HIDEOUT_STORE_ROOT="$store" "$hideout" env list >"$out/logs/env-owner-reconcile.out" 2>"$out/logs/env-owner-reconcile.err" &&
			awk 'NR == 2 { exit !($7 == 1 && $6 == "running") }' "$out/logs/env-owner-reconcile.out"; then
			owner_reconciled=1
			break
		fi
		sleep 0.02
		owner_reconcile_attempt=$((owner_reconcile_attempt + 1))
	done
	owner_release_end="$(gate2_034_now_seconds)"
	owner_release_ms="$(awk -v start="$owner_release_start" -v end="$owner_release_end" 'BEGIN { printf "%.3f", (end-start)*1000 }')"
	printf '%s\n' "$owner_release_ms" >"$out/logs/owner-reconcile-ms.txt"
		if [ "$owner_reconciled" -ne 1 ] ||
			! awk -v elapsed="$owner_release_ms" \
				'BEGIN { exit !(elapsed > 0 && elapsed <= 1000) }'; then
			echo "concurrent-sessions gate2: host owner liveness did not reconcile within one second (${owner_release_ms}ms)" >&2
			return 1
		fi
	terminated_probe=""
	terminated_probe_attempt=0
	while [ "$terminated_probe_attempt" -lt 100 ]; do
		terminated_probe="$(ssh -F "$ssh_config" -o BatchMode=yes -o User=root -o ControlMaster=no -o ControlPath=none \
			"lima-$instance" sh -s -- "$interrupted_session" <<'ROOTSH'
session_id=$1
for env_file in /proc/[0-9]*/environ; do
  [ -r "$env_file" ] || continue
  if tr '\000' '\n' 2>/dev/null <"$env_file" | grep -Fqx "HIDEOUT_SESSION_ID=$session_id"; then
    printf 'live\n'
    exit 0
  fi
done
printf 'gone\n'
ROOTSH
		)"
		terminated_probe="$(printf '%s\n' "$terminated_probe" | tr -d '\r' | tail -n1)"
		[ "$terminated_probe" = "gone" ] && break
		sleep 0.1
		terminated_probe_attempt=$((terminated_probe_attempt + 1))
	done
	[ "$terminated_probe" = "gone" ] || {
		echo "concurrent-sessions gate2: interrupted target process tree remains" >&2
		return 1
	}
  touch "$workspace/b.probe"
  gate2_034_wait_file "$workspace/b.alive" "surviving sibling"
  kill -0 "$second_pid"
  HIDEOUT_STORE_ROOT="$store" "$hideout" env list >"$out/logs/env-one.out"
  awk 'NR == 2 { exit !($7 == 1 && $6 == "running") }' "$out/logs/env-one.out"

  touch "$workspace/b.release"
  wait "$second_pid"
  second_pid=""
  GATE2_034_CLEANUP_SECOND_PID=""
  HIDEOUT_STORE_ROOT="$store" "$hideout" env list >"$out/logs/env-ready.out"
  awk 'NR == 2 { exit !($7 == 0 && $6 == "ready") }' "$out/logs/env-ready.out"
	if find "$store/environments" -path '*/owners/*' -mindepth 4 -print -quit | grep -q .; then
		echo "concurrent-sessions gate2: stale owner evidence remains after exact cleanup" >&2
		return 1
	fi
  LIMA_HOME="$lima_home" limactl list --json | jq -s -e --arg name "$instance" \
    'any(.[]; .name == $name and .status == "Running")' >/dev/null

  HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" stop "$env_name" \
    >"$out/logs/stop-final.out" 2>"$out/logs/stop-final.err"
  LIMA_HOME="$lima_home" limactl list --json | jq -s -e --arg name "$instance" \
    'any(.[]; .name == $name and .status == "Stopped")' >/dev/null

	  gate2_034_run_performance "$root" "$out" "$lima_home" "$workspace" \
	    "$hideout" "$store" "$profile" "$samples" "$warmups" "$arch"

		local session_pty_evidence session_pty_sha256
		session_pty_evidence="$out/logs/session-pty.json"
		(
			cd "$root" || exit
		go run ./test/e2e/sessionpty \
			--hideout "$hideout" \
			--store "$store" \
			--lima-home "$lima_home" \
			--workspace "$workspace" \
			--profile "$profile" \
			--out "$session_pty_evidence"
	)
	jq -e '
		.status == "passed" and
		.initialSize == "24x80" and
		.resizedSize == "31x97" and
		.fullscreenFixture == true and
		.interruptExit == 130 and
		.daemonCrashClients == 2 and
		.terminalRestore == true and
		.targetsReaped == true and
		.restartFailedClosed == true and
		.explicitRecovery == true and
		.postRecoveryRun == true
	' "$session_pty_evidence" >/dev/null
	session_pty_sha256="$(gate2_034_sha256 "$session_pty_evidence")"

		: >"$out/logs/gate2.out"
	cat >>"$out/logs/gate2.out" <<'EOF'
three_same_workspace_owners=passed
ordinary_target_private_proc=passed
hostfs_overlay_session_local=passed
sibling_interruption_isolated=passed
explicit_stop_live_refused=passed
last_session_no_auto_stop=passed
guest_root_containment=non-claim
warm_attach_performance=passed
static_workspace_performance=passed
real_lima_pty_resize=passed
fullscreen_terminal_fixture=passed
ctrl_c_exact_exit=passed
daemon_crash_clients_unblocked=passed
daemon_crash_terminal_restore=passed
daemon_crash_targets_reaped=passed
restart_stale_owner_fail_closed=passed
explicit_recovery=passed
EOF

	local commit dirty
	commit="$(git -C "$root" rev-parse HEAD)"
	dirty="$(cd "$root" && gate2_034_dirty)"
	jq -n \
			--arg commit "$commit" --argjson dirty "$dirty" \
			--argjson ownerReconcileMs "$owner_release_ms" \
			--arg sessionPTYEvidenceSHA256 "$session_pty_sha256" \
			--arg generated "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
			'{schema:"hideout.concurrent-sessions-gate2/v1",status:"passed",generatedAt:$generated,
			  commit:$commit,dirty:$dirty,backend:"lima",host:"macos-arm64",
			  candidateAcceptance:($dirty | not),
			  metrics:{ownerReconcileMs:$ownerReconcileMs},artifacts:{sessionPTYEvidenceSHA256:$sessionPTYEvidenceSHA256},
			  checks:{threeSameWorkspaceOwners:true,distinctSessionIds:true,distinctPidNamespaces:true,
			    distinctMountNamespaces:true,nonRootTargets:true,privateProc:true,siblingPidHidden:true,
			    siblingRuntimeHidden:true,guestRootPositiveControl:true,hostfsOverlaySessionLocal:true,
			    forcedInterruptionTargetGone:true,siblingSurvivedInterruption:true,ownerReconciled:true,
			    stopRefusedWithLiveOwners:true,lastSessionPreservedVm:true,explicitStopStoppedVm:true,
			    realPTYInitialSize:true,realPTYResize:true,fullscreenFixture:true,interruptExitExact:true,
			    daemonCrashClientsUnblocked:true,daemonCrashTerminalRestored:true,daemonCrashTargetsReaped:true,
			    restartStaleOwnerFailedClosed:true,explicitRecovery:true,postRecoveryRun:true},
		  nonClaims:{guestRootContainment:"not-claimed"}}' \
    >"$out/result.json"
  find "$out" -type d -exec chmod 0700 {} +
  find "$out" -type f -exec chmod 0600 {} +
  gate2_034_completed=1
  echo "concurrent-sessions Gate 2 passed: $out/result.json"
  gate2_034_cleanup
  trap - EXIT
}
