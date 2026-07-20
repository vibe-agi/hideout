#!/usr/bin/env bash
# Sourced by test-gate2-lima.sh after its shared helpers and globals exist.

projection_not_run() {
  local reason="$1"
  printf 'projection_real_gate2=not-run reason=%s\n' "$reason"
  if [ "${HIDEOUT_GATE2_REQUIRE_PROJECTION:-0}" = "1" ]; then
    echo "gate2: projection proof required but unavailable: $reason" >&2
    return 1
  fi
  return 0
}

projection_output_contains_host_identity() {
  local path="$1"
  grep -F -- "$HOME/" "$path" >/dev/null 2>&1 ||
    grep -F -- "HOME=$HOME" "$path" >/dev/null 2>&1 ||
    grep -F -- "USER=$(id -un)" "$path" >/dev/null 2>&1 ||
    grep -F -- "LOGNAME=$(id -un)" "$path" >/dev/null 2>&1 ||
    grep -F -- "user.name=$(id -un)" "$path" >/dev/null 2>&1
}

projection_expect_line() {
  local path="$1"
  local expected="$2"
  local description="$3"
  if grep -qxF -- "$expected" "$path"; then
    return 0
  fi
  echo "gate2: projection $description mismatch; expected line: $expected" >&2
  cat "$path" >&2
  return 1
}

# Return only isolated VS Code main processes started with the test-owned
# user-data directory. Matching the executable at argv[0] avoids observing the
# ps/awk probe itself, while accepting both /tmp and its canonical /private/tmp
# spelling handles macOS path canonicalization.
projection_safe_app_pids() {
  [ -n "${projection_safe_data_dir:-}" ] || return 0
  [ -n "${projection_vscode_main:-}" ] || return 0
  local canonical_data_dir="$projection_safe_data_dir"
  local canonical_parent
  if canonical_parent="$(CDPATH= cd -- "$(dirname -- "$projection_safe_data_dir")" 2>/dev/null && pwd -P)"; then
    canonical_data_dir="$canonical_parent/$(basename -- "$projection_safe_data_dir")"
  fi
  ps axww -o pid=,command= | awk \
    -v main="$projection_vscode_main" \
    -v raw="$projection_safe_data_dir" \
    -v canonical="$canonical_data_dir" '
      {
        pid = $1
        command = $0
        sub(/^[[:space:]]*[0-9]+[[:space:]]+/, "", command)
        rawArg = "--user-data-dir " raw
        canonicalArg = "--user-data-dir " canonical
        if (index(command, main) == 1 &&
            (index(command, rawArg) > 0 || index(command, canonicalArg) > 0)) {
          print pid
        }
      }
    '
}

# Stop only the isolated VS Code main process. Sending a signal to every
# matching Electron child makes the surviving main process report a renderer
# crash to the operator. The main process owns orderly child shutdown.
projection_stop_safe_app() {
  local pid i
  while IFS= read -r pid; do
    [ -n "$pid" ] || continue
    kill -TERM "$pid" 2>/dev/null || true
    for i in $(seq 1 50); do
      kill -0 "$pid" 2>/dev/null || break
      sleep 0.1
    done
    if kill -0 "$pid" 2>/dev/null; then
      echo "gate2: isolated VS Code process did not stop cleanly; leaving it for operator cleanup" >&2
    fi
  done < <(projection_safe_app_pids)
}

projection_prepare_privacy_network() {
  local arch
  arch="$(go env GOARCH)"
  if [ -z "${HIDEOUT_LINUX_TUN2SOCKS_PATH:-}" ]; then
    HIDEOUT_LINUX_TUN2SOCKS_PATH="$bin/tun2socks-linux-$arch"
    local build_dir="$tmp/projection-tun2socks-build"
    mkdir -p "$build_dir"
    (
      cd "$build_dir"
      go mod init hideout-gate2-projection-tun2socks >/dev/null
      go get github.com/xjasonlyu/tun2socks/v2@v2.6.0 >/dev/null
      GOOS=linux GOARCH="$arch" CGO_ENABLED=0 \
        go build -o "$HIDEOUT_LINUX_TUN2SOCKS_PATH" github.com/xjasonlyu/tun2socks/v2
    )
    chmod 0700 "$HIDEOUT_LINUX_TUN2SOCKS_PATH"
    export HIDEOUT_LINUX_TUN2SOCKS_PATH
  fi
  if [ -z "${HIDEOUT_LINUX_DNS_STUB_PATH:-}" ]; then
    HIDEOUT_LINUX_DNS_STUB_PATH="$bin/hideout-dns-stub-linux-$arch"
    GOOS=linux GOARCH="$arch" CGO_ENABLED=0 \
      go build -trimpath -o "$HIDEOUT_LINUX_DNS_STUB_PATH" ./cmd/hideout-dns-stub
    chmod 0700 "$HIDEOUT_LINUX_DNS_STUB_PATH"
    export HIDEOUT_LINUX_DNS_STUB_PATH
  fi

  local proxy_bin="$bin/hideout-gate-socks5"
  local proxy_args=(--listen 127.0.0.1:0 --url-host host.lima.internal)
  go build -o "$proxy_bin" ./cmd/hideout-gate-socks5
  case "${HTTPS_PROXY:-${HTTP_PROXY:-}}" in
    http://*) proxy_args+=(--use-env-http-proxy) ;;
  esac
  "$proxy_bin" "${proxy_args[@]}" \
    >"$tmp/projection-proxy.url" 2>"$tmp/projection-proxy.err" &
  projection_proxy_pid=$!
  local i
  for i in $(seq 1 100); do
    if [ -s "$tmp/projection-proxy.url" ]; then
      HIDEOUT_SECRET_PROJECTION_PROXY="$(sed -n '1p' "$tmp/projection-proxy.url")"
      export HIDEOUT_SECRET_PROJECTION_PROXY
      return 0
    fi
    if ! kill -0 "$projection_proxy_pid" 2>/dev/null; then
      echo "gate2: projection SOCKS fixture exited early" >&2
      cat "$tmp/projection-proxy.err" >&2 || true
      return 1
    fi
    sleep 0.1
  done
  echo "gate2: projection SOCKS fixture did not publish a URL" >&2
  return 1
}

latest_projection_audit() {
  local mode="$1"
  local candidate
  local found=""
  while IFS= read -r candidate; do
    if grep -q '"action":"host.app.open-resource"' "$candidate" && grep -q "\"mode\":\"$mode\"" "$candidate"; then
      found="$candidate"
    fi
  done < <(find "$store/sessions" -type f -name audit.jsonl 2>/dev/null | sort)
  [ -n "$found" ] || return 1
  printf '%s\n' "$found"
}

last_host_app_event() {
  local audit_path="$1"
  local pack_id="$2"
  local mode="${3:-}"
  jq -c --arg pack "$pack_id" --arg mode "$mode" '
    select(
      .action == "host.app.open-resource" and
      .details.packId == $pack and
      ($mode == "" or .details.mode == $mode)
    )
  ' "$audit_path" | tail -n1
}

run_projection_gate2() {
  if [ "${HIDEOUT_GATE2_SKIP_PROJECTION:-0}" = "1" ]; then
    projection_not_run "explicitly skipped by HIDEOUT_GATE2_SKIP_PROJECTION"
    return
  fi
  if [ "$(uname -s)" != "Darwin" ]; then
    projection_not_run "host is not macOS"
    return
  fi
  local vscode_bundle
  if ! vscode_bundle="$(projection_vscode_bundle)"; then
    projection_not_run "Visual Studio Code app bundle is absent"
    return
  fi
  if ! /usr/bin/codesign --verify --strict \
    --test-requirement='=identifier "com.microsoft.VSCode" and anchor apple generic and certificate leaf[subject.OU] = "UBF8T346G9"' \
    "$vscode_bundle" >/dev/null 2>&1; then
    projection_not_run "Visual Studio Code identity verification failed"
    return
  fi
  projection_vscode_main="$vscode_bundle/Contents/MacOS/Code"
  if ! command -v ps >/dev/null 2>&1 || ! command -v awk >/dev/null 2>&1; then
    projection_not_run "ps or awk is unavailable for host-effect observation"
    return
  fi

  echo "gate2: running host capability projection privacy channels"
  # Keep profile-derived Lima instance names below macOS UNIX_PATH_MAX even
  # when the gate's isolated LIMA_HOME itself has a long temporary prefix.
  local profile_name="g2p"
  local control_profile="g2pc"
  projection_workspace="$(mktemp -d "$HOME/hideout-gate2-projection.XXXXXX")"
  projection_control_workspace="$(mktemp -d "$HOME/hideout-gate2-projection-control.XXXXXX")"
  projection_trusted_workspace="$(mktemp -d "$HOME/hideout-gate2-projection-trusted.XXXXXX")"
  projection_grant_workspace="$(mktemp -d "$HOME/hideout-gate2-projection-grant.XXXXXX")"
  # 032 isolates safe state by qualified app and run beneath this Core-owned
  # base. Match the base, then discover the exact materialized settings file.
  projection_safe_data_dir="$store/profiles/$profile_name/host-app/state"

  projection_prepare_privacy_network

  # The daemon captures its secret environment once, at autostart
  # (internal/daemon/autostart.go environmentWithStoreRoot(os.Environ(), ...)),
  # and run network setup resolves --proxy-secret from that frozen daemon env
  # (internal/manager/run_network.go). The earlier smoke lane already
  # auto-started the daemon before this projection proxy secret existed, so the
  # live daemon cannot resolve "projection-proxy". Stop it here; the projection
  # init just below re-autostarts a daemon that inherits the freshly exported
  # HIDEOUT_SECRET_PROJECTION_PROXY. VMs persist across a daemon stop.
  HIDEOUT_STORE_ROOT="$store" "$hideout" daemon stop >/dev/null 2>&1 || true

  if ! HIDEOUT_STORE_ROOT="$store" \
    HIDEOUT_SECRET_PROJECTION_PROXY="$HIDEOUT_SECRET_PROJECTION_PROXY" \
    "$hideout" init --no-input --profile "$profile_name" \
      --template privacy --backend lima --network tun2socks \
      --proxy-secret projection-proxy --mediated-resolver 1.1.1.1 \
      >"$tmp/projection-init.out" 2>"$tmp/projection-init.err"; then
    echo "gate2: projection privacy profile init failed" >&2
    cat "$tmp/projection-init.out" "$tmp/projection-init.err" >&2
    return 1
  fi
  if ! HIDEOUT_STORE_ROOT="$store" "$hideout" init --no-input --profile "$control_profile" \
    --template dev --backend lima --network direct >"$tmp/projection-control-init.out" 2>"$tmp/projection-control-init.err"; then
    echo "gate2: projection preserve-control profile init failed" >&2
    cat "$tmp/projection-control-init.out" "$tmp/projection-control-init.err" >&2
    return 1
  fi
  # Profiles default to privacy-preserving alias workspace paths. This is the
  # positive control that must EXPOSE the host path to prove the detector works,
  # so opt it into preserve explicitly.
  if ! HIDEOUT_STORE_ROOT="$store" "$hideout" profile workspace-path-mode "$control_profile" preserve \
    >>"$tmp/projection-control-init.out" 2>>"$tmp/projection-control-init.err"; then
    echo "gate2: projection preserve-control workspace-path-mode preserve failed" >&2
    cat "$tmp/projection-control-init.out" "$tmp/projection-control-init.err" >&2
    return 1
  fi

  local detector_control="$tmp/projection-detector-control.out"
  printf 'USER=%s\nHOME=%s\npath=%s/project\n' "$(id -un)" "$HOME" "$HOME" >"$detector_control"
  if ! projection_output_contains_host_identity "$detector_control"; then
    echo "gate2: projection identity detector self-test did not detect injected host identity" >&2
    return 1
  fi

  local alias_out="$tmp/projection-alias.out"
  local alias_err="$tmp/projection-alias.err"
  if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
    "$hideout" run --verbose --profile "$profile_name" --backend lima --network tun2socks \
      --proxy-secret projection-proxy --workspace "$projection_workspace" -- sh -eu -c '
printf "pwd=%s\n" "$PWD"
printf "realpath=%s\n" "$(realpath .)"
printf "USER=%s\n" "${USER:-}"
printf "LOGNAME=%s\n" "${LOGNAME:-}"
printf "HOME=%s\n" "$HOME"
printf "guest_passwd=%s\n" "$(getent passwd "$(id -un)" 2>/dev/null || true)"
printf "user.name=%s\n" "$(git config --global --get user.name)"
printf "user.email=%s\n" "$(git config --global --get user.email)"
printf "%s\n" "---proc-mounts---"
cat /proc/mounts
printf "%s\n" "---mountinfo---"
cat /proc/self/mountinfo
printf "%s\n" "---mount---"
mount
if command -v findmnt >/dev/null 2>&1; then
  printf "%s\n" "---findmnt---"
  findmnt -R /workspace || true
fi
test -z "${HIDEOUT_SECRET_PROJECTION_PROXY:-}"
printf "projection_proxy_secret_absent=yes\n"
' >"$alias_out" 2>"$alias_err"; then
    echo "gate2: projection alias privacy probe failed" >&2
    cat "$alias_out" "$alias_err" >&2
    return 1
  fi
  projection_expect_line "$alias_out" 'pwd=/workspace' 'workspace alias'
  projection_expect_line "$alias_out" 'realpath=/workspace' 'workspace realpath alias'
  projection_expect_line "$alias_out" 'USER=developer' 'synthetic USER'
  projection_expect_line "$alias_out" 'HOME=/hideout/profile/home' 'synthetic HOME'
  projection_expect_line "$alias_out" 'user.name=Developer' 'synthetic Git name'
  projection_expect_line "$alias_out" 'user.email=developer@example.com' 'synthetic Git email'
  projection_expect_line "$alias_out" 'projection_proxy_secret_absent=yes' 'projection proxy secret stripping'
  if projection_output_contains_host_identity "$alias_out"; then
    echo "gate2: alias-mode guest surfaces leaked synthesized host identity" >&2
    grep -n -F -- "$HOME" "$alias_out" >&2 || true
    return 1
  fi

  local preserve_out="$tmp/projection-preserve-control.out"
  # The shared default Lima environment only supports alias workspaces. A
  # preserve-mode positive control must therefore run in a dedicated named
  # environment: dedicated envs honor the profile's preserve pathMode and are
  # isolation-capable, unlike the shared default or a record-less run.
  local preserve_env="g2pcp"
  if ! HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
    "$hideout" env create "$preserve_env" --profile "$control_profile" --backend lima --workspace "$projection_control_workspace" \
    >"$tmp/projection-preserve-env.out" 2>"$tmp/projection-preserve-env.err"; then
    echo "gate2: preserve-mode positive control dedicated env create failed" >&2
    cat "$tmp/projection-preserve-env.out" "$tmp/projection-preserve-env.err" >&2
    return 1
  fi
  if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
    "$hideout" run --env "$preserve_env" --profile "$control_profile" --backend lima --workspace "$projection_control_workspace" -- sh -eu -c 'printf "pwd=%s\n" "$PWD"' \
    >"$preserve_out" 2>"$tmp/projection-preserve-control.err"; then
    echo "gate2: preserve-mode positive control failed" >&2
    cat "$preserve_out" "$tmp/projection-preserve-control.err" >&2
    return 1
  fi
  if ! projection_output_contains_host_identity "$preserve_out"; then
    echo "gate2: preserve-mode positive control did not expose the host path to the detector" >&2
    cat "$preserve_out" >&2
    return 1
  fi
  printf 'projection_privacy_three_channel=passed\n'

  run_projection_safe_code "$profile_name"
  run_projection_hostfs_resource "$profile_name"
  run_projection_trusted_lifecycle "$profile_name"
  run_projection_persistent_grant_lifecycle "$profile_name"
  if [ -n "${HIDEOUT_GATE2_EXTERNAL_HOST_APP_PACK:-}" ]; then
    run_projection_external_pack "$profile_name" "$HIDEOUT_GATE2_EXTERNAL_HOST_APP_PACK"
  fi
}

# run_projection_persistent_grant_lifecycle proves the 039 durable grant path
# (grant once on the host -> later one-shot runs open natively -> revoke).
# It is distinct from run_projection_trusted_lifecycle, which exercises the
# live run-bound decision fallback. The grant command derives the workspace
# identity from its working directory, so it is invoked with cwd == the run's
# --workspace so both derive the same workspaceID (proven equal; see 039 T008a).
run_projection_persistent_grant_lifecycle() {
  local profile_name="$1"
  echo "gate2: running persistent host-app grant lifecycle (039)"
  HIDEOUT_STORE_ROOT="$store" "$hideout" profile host-app-mode "$profile_name" trusted >/dev/null

  # 1. Trusted mode, no grant: the one-shot open refuses (no host launch),
  #    records a promotion request, and names the exact grant command.
  local refuse1="$tmp/projection-grant-refuse1.out"
  set +e
  with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
    "$hideout" run --profile "$profile_name" --backend lima --network tun2socks \
      --proxy-secret projection-proxy --workspace "$projection_grant_workspace" -- \
      code -n . >"$refuse1" 2>&1
  local rc1=$?
  set -e
  if [ "$rc1" = "0" ]; then
    echo "gate2: ungranted trusted one-shot open should have refused" >&2
    cat "$refuse1" >&2
    return 1
  fi
  if ! grep -q "hideout allow host-app code" "$refuse1"; then
    echo "gate2: 039 step1 ungranted refusal did not name the grant command:" >&2
    cat "$refuse1" >&2
    return 1
  fi

  # 2. Operator grants on the host from inside the project dir (cwd == the run
  #    workspace) under the privacy profile, promoting the recorded request into
  #    a durable grant.
  local grant_out="$tmp/projection-grant.out"
  if ! ( cd "$projection_grant_workspace" && HIDEOUT_STORE_ROOT="$store" \
      "$hideout" allow host-app code --for-profile "$profile_name" ) >"$grant_out" 2>&1; then
    echo "gate2: 039 step2 grant command failed:" >&2
    cat "$grant_out" >&2
    return 1
  fi
  if ! grep -q "allowed for this project" "$grant_out"; then
    echo "gate2: 039 step2 grant did not confirm:" >&2
    cat "$grant_out" >&2
    return 1
  fi

  # 3. A separate one-shot run reuses the durable grant and opens natively.
  local reuse_out="$tmp/projection-grant-reuse.out"
  if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
    "$hideout" run --profile "$profile_name" --backend lima --network tun2socks \
      --proxy-secret projection-proxy --workspace "$projection_grant_workspace" -- \
      code -n . >"$reuse_out" 2>&1; then
    echo "gate2: granted one-shot reuse should have opened natively" >&2
    cat "$reuse_out" >&2
    return 1
  fi
  if ! grep -q "opened in your trusted host app" "$reuse_out"; then
    echo "gate2: 039 step3 granted reuse did not open natively:" >&2
    cat "$reuse_out" >&2
    return 1
  fi

  # 4. Revoke removes the durable grant under the privacy profile.
  local revoke_out="$tmp/projection-grant-revoke.out"
  if ! ( cd "$projection_grant_workspace" && HIDEOUT_STORE_ROOT="$store" \
      "$hideout" deny host-app code --for-profile "$profile_name" ) >"$revoke_out" 2>&1; then
    echo "gate2: 039 step4 revoke command failed:" >&2
    cat "$revoke_out" >&2
    return 1
  fi
  if ! grep -q "revoked for this project" "$revoke_out"; then
    echo "gate2: 039 step4 revoke did not confirm:" >&2
    cat "$revoke_out" >&2
    return 1
  fi

  # 5. After revoke the one-shot open fails closed again with the named path.
  local refuse2="$tmp/projection-grant-refuse2.out"
  set +e
  with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
    "$hideout" run --profile "$profile_name" --backend lima --network tun2socks \
      --proxy-secret projection-proxy --workspace "$projection_grant_workspace" -- \
      code -n . >"$refuse2" 2>&1
  local rc2=$?
  set -e
  if [ "$rc2" = "0" ]; then
    echo "gate2: revoked trusted one-shot open should have refused" >&2
    cat "$refuse2" >&2
    return 1
  fi
  if ! grep -q "hideout allow host-app code" "$refuse2"; then
    echo "gate2: 039 step5 revoked refusal did not name the grant command:" >&2
    cat "$refuse2" >&2
    return 1
  fi

  HIDEOUT_STORE_ROOT="$store" "$hideout" profile host-app-mode "$profile_name" safe >/dev/null
  printf 'projection_persistent_grant=passed\n'
}

run_projection_safe_code() {
  local profile_name="$1"
  echo "gate2: running safe code projection"
  local safe_marker="$tmp/projection-safe-task-marker"
  mkdir -p "$projection_workspace/.vscode" "$projection_workspace/src"
  printf 'package main\n' >"$projection_workspace/src/main.go"
  cat >"$projection_workspace/.vscode/tasks.json" <<JSON
{
  "version": "2.0.0",
  "tasks": [{
    "label": "must-not-auto-run",
    "type": "shell",
    "command": "printf unsafe > '$safe_marker'",
    "runOptions": {"runOn": "folderOpen"},
    "problemMatcher": []
  }]
}
JSON
  HIDEOUT_STORE_ROOT="$store" "$hideout" profile host-app-mode "$profile_name" safe >/dev/null
  local safe_run_status=0
  local safe_process_pid=""
  with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
    "$hideout" run --profile "$profile_name" --backend lima --network tun2socks \
      --proxy-secret projection-proxy --workspace "$projection_workspace" -- code -n -g src/main.go:12:3 \
    >"$tmp/projection-safe.out" 2>"$tmp/projection-safe.err" &
  projection_run_pid=$!
  while kill -0 "$projection_run_pid" 2>/dev/null; do
    while IFS= read -r safe_process_pid; do
      [ -n "$safe_process_pid" ] && break
    done < <(projection_safe_app_pids)
    [ -n "$safe_process_pid" ] && break
    sleep 0.02
  done
  if wait "$projection_run_pid"; then
    safe_run_status=0
  else
    safe_run_status=$?
  fi
  projection_run_pid=""
  if [ -z "$safe_process_pid" ]; then
    while IFS= read -r safe_process_pid; do
      [ -n "$safe_process_pid" ] && break
    done < <(projection_safe_app_pids)
  fi
  if [ "$safe_run_status" -ne 0 ]; then
    echo "gate2: safe code projection failed" >&2
    cat "$tmp/projection-safe.out" "$tmp/projection-safe.err" >&2
    return 1
  fi
  local safe_settings
  safe_settings="$(find "$projection_safe_data_dir" -type f -path '*/User/settings.json' | sort | tail -n1)"
  if [ -z "$safe_settings" ]; then
    echo "gate2: safe VS Code settings were not materialized" >&2
    return 1
  fi
  if [ -z "$safe_process_pid" ]; then
    echo "gate2: safe VS Code main process was not observed during the projection command" >&2
    printf 'gate2: expected safe state base: %s\n' "$projection_safe_data_dir" >&2
    find "$projection_safe_data_dir" -maxdepth 6 -type f -print 2>/dev/null | sort >&2 || true
    return 1
  fi
  sleep 5
  if [ -e "$safe_marker" ]; then
    echo "gate2: safe projection allowed folder-open task auto-execution" >&2
    return 1
  fi
  jq -e '."security.workspace.trust.enabled" == true and ."task.allowAutomaticTasks" == "off"' \
    "$safe_settings" >/dev/null
	local safe_audit safe_event
	if ! safe_audit="$(latest_projection_audit safe)"; then
	  echo "gate2: safe projection audit was not found" >&2
	  return 1
	fi
	safe_event="$(last_host_app_event "$safe_audit" builtin.vscode safe)"
	[ -n "$safe_event" ]
	grep -q '"relativeTarget":"src/main.go"' <<<"$safe_event"
	grep -q '"outcome":"launched"' <<<"$safe_event"
	if grep -q -- '--disable-workspace-trust' <<<"$safe_event"; then
    echo "gate2: safe projection disabled Workspace Trust" >&2
    return 1
  fi
  printf 'projection_code_open=passed\n'
  printf 'projection_workspace_resource=passed\n'

  # Stop only the isolated safe-profile process. A trusted launch uses the
  # operator's normal IDE and is intentionally not killed by the gate.
  projection_stop_safe_app
}

run_projection_hostfs_resource() {
  local profile_name="$1"
  echo "gate2: running HostFS-backed host-app projection"
  local portal_target="/hideout/hostfs$hostfs_file"
  local ungranted_target="/hideout/hostfs$hostfs_ungranted"
  local basename_target
  basename_target="$(basename "$hostfs_file")"

  HIDEOUT_STORE_ROOT="$store" "$hideout" profile host-app-mode "$profile_name" safe >/dev/null
  if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
	  "$hideout" run --profile "$profile_name" --backend lima --network tun2socks \
	    --proxy-secret projection-proxy --workspace "$projection_workspace" \
	    --fs "read:$hostfs_file" -- code -n "$portal_target" \
      >"$tmp/projection-hostfs-authorized.out" 2>"$tmp/projection-hostfs-authorized.err"; then
    echo "gate2: authorized HostFS host-app projection failed" >&2
    cat "$tmp/projection-hostfs-authorized.out" "$tmp/projection-hostfs-authorized.err" >&2
    return 1
  fi
	local authorized_audit authorized_event
	if ! authorized_audit="$(latest_projection_audit safe)"; then
	  echo "gate2: authorized HostFS projection audit was not found" >&2
	  return 1
	fi
	authorized_event="$(last_host_app_event "$authorized_audit" builtin.vscode safe)"
	[ -n "$authorized_event" ]
	grep -q '"resourceClass":"hostfs-portal"' <<<"$authorized_event"
	grep -q "\"relativeTarget\":\"$basename_target\"" <<<"$authorized_event"
	grep -q '"outcome":"launched"' <<<"$authorized_event"
	if grep -nF -- "$hostfs_file" <<<"$authorized_event" >/dev/null || \
	  grep -nE '/hideout/hostfs|cap_[A-Za-z0-9]{12,}|claim_[A-Za-z0-9]{12,}|hostfs-read/(grants|state|owner|provider)' <<<"$authorized_event" >/dev/null; then
	  echo "gate2: HostFS projection audit leaked a lower path or authority token" >&2
	  printf '%s\n' "$authorized_event" >&2
    return 1
  fi
  printf 'projection_hostfs_authorized=passed\n'

  if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
    "$hideout" run --profile "$profile_name" --backend lima --network tun2socks \
      --proxy-secret projection-proxy --workspace "$projection_workspace" \
      --fs "see:$hostfs_file" -- sh -eu -c '
if code "$1" >/dev/null 2>&1; then
  echo "see-only HostFS resource launched unexpectedly" >&2
  exit 91
fi
printf "projection_hostfs_see_only_denied=passed\n"
' gate2-hostfs-see "$portal_target" \
      >"$tmp/projection-hostfs-see-only.out" 2>"$tmp/projection-hostfs-see-only.err"; then
    echo "gate2: HostFS see-only projection refusal failed" >&2
    cat "$tmp/projection-hostfs-see-only.out" "$tmp/projection-hostfs-see-only.err" >&2
    return 1
  fi
  grep -q '^projection_hostfs_see_only_denied=passed$' "$tmp/projection-hostfs-see-only.out"

  if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
    "$hideout" run --profile "$profile_name" --backend lima --network tun2socks \
      --proxy-secret projection-proxy --workspace "$projection_workspace" \
      --fs "see:$hostfs_file" -- sh -eu -c '
if code "$1" >/dev/null 2>&1; then
  echo "ungranted HostFS resource launched unexpectedly" >&2
  exit 92
fi
printf "projection_hostfs_ungranted_denied=passed\n"
' gate2-hostfs-ungranted "$ungranted_target" \
      >"$tmp/projection-hostfs-ungranted.out" 2>"$tmp/projection-hostfs-ungranted.err"; then
    echo "gate2: ungranted HostFS projection refusal failed" >&2
    cat "$tmp/projection-hostfs-ungranted.out" "$tmp/projection-hostfs-ungranted.err" >&2
    return 1
  fi
  grep -q '^projection_hostfs_ungranted_denied=passed$' "$tmp/projection-hostfs-ungranted.out"

  if HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
    "$hideout" run --profile "$profile_name" --backend lima --network tun2socks \
      --proxy-secret projection-proxy --workspace "$projection_workspace" \
      --fs "read:$store" -- true \
      >"$tmp/projection-hostfs-reserved.out" 2>"$tmp/projection-hostfs-reserved.err"; then
    echo "gate2: reserved Hideout store unexpectedly became HostFS authority" >&2
    return 1
  fi
  printf 'projection_hostfs_reserved_denied=passed\n'
}

run_projection_trusted_lifecycle() {
  local profile_name="$1"
  echo "gate2: running trusted IDE decision lifecycle"
  HIDEOUT_STORE_ROOT="$store" "$hideout" profile host-app-mode "$profile_name" trusted >/dev/null
  (
    set +e
    HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
      "$hideout" run --profile "$profile_name" --backend lima --network tun2socks \
        --proxy-secret projection-proxy --workspace "$projection_trusted_workspace" -- sh -c '
set +e
code -n . >/dev/null 2>&1
printf "%s\n" "$?" > trusted-first.rc
: > trusted-ready
attempt=0
approved_rc=1
while [ "$attempt" -lt 100 ]; do
  code -n . >/dev/null 2>&1
  approved_rc=$?
  [ "$approved_rc" -eq 0 ] && break
  attempt=$((attempt + 1))
  sleep 0.2
done
printf "%s\n" "$approved_rc" > trusted-approved.rc
: > trusted-approved-ready
[ "$approved_rc" -eq 0 ] || exit 93
# Guest-to-host workspace writes are observable, but host-to-guest filesystem
# notifications are not a reliable control channel under every VZ/virtiofs
# version. Poll the broker through its deduplicated open path until the
# host-side gate revokes the exact grant, without using a host-written signal.
attempt=0
revoked_rc=0
while [ "$attempt" -lt 100 ]; do
  code -n . >/dev/null 2>&1
  revoked_rc=$?
  [ "$revoked_rc" -ne 0 ] && break
  attempt=$((attempt + 1))
  sleep 0.2
done
printf "%s\n" "$revoked_rc" > trusted-revoked.rc
[ "$revoked_rc" -ne 0 ] || exit 94
' >"$tmp/projection-trusted.out" 2>"$tmp/projection-trusted.err"
  ) &
  projection_run_pid=$!
  wait_for_file "$projection_trusted_workspace/trusted-ready" "trusted projection first refusal"
  wait_for_projection_decision "$profile_name" "$tmp/projection-decisions.json"
  local decision_id
  decision_id="$(jq -r '[.[] | select(.state == "pending" or .state == "claimed")] | last | .id // empty' "$tmp/projection-decisions.json")"
  if [ -z "$decision_id" ]; then
    echo "gate2: trusted IDE decision id missing" >&2
    return 1
  fi
  local claim_json claim_token
  claim_json="$(HIDEOUT_STORE_ROOT="$store" "$hideout" decision claim --surface gate2 "$decision_id")"
  claim_token="$(printf '%s' "$claim_json" | jq -r '.claimToken')"
  HIDEOUT_STORE_ROOT="$store" "$hideout" decision approve --claim-token "$claim_token" "$decision_id" >/dev/null
  wait_for_file "$projection_trusted_workspace/trusted-approved-ready" "trusted projection approval"
  test "$(cat "$projection_trusted_workspace/trusted-first.rc")" != "0"
  test "$(cat "$projection_trusted_workspace/trusted-approved.rc")" = "0"
  HIDEOUT_STORE_ROOT="$store" "$hideout" decision revoke "$decision_id" >/dev/null
  if ! wait "$projection_run_pid"; then
    echo "gate2: trusted projection guest flow failed" >&2
    cat "$tmp/projection-trusted.out" "$tmp/projection-trusted.err" >&2
    return 1
  fi
  projection_run_pid=""
  test "$(cat "$projection_trusted_workspace/trusted-revoked.rc")" != "0"
  local trusted_session trusted_audit
  trusted_session="$(HIDEOUT_STORE_ROOT="$store" "$hideout" decision inspect "$decision_id" | jq -r '.source.session')"
  trusted_audit="$store/sessions/$trusted_session/audit.jsonl"
  grep -q '"mode":"trusted-host-app"' "$trusted_audit"
  grep -q '"outcome":"launched"' "$trusted_audit"
  grep -q '"code":"projection.mode.trusted-denied"' "$trusted_audit"
  HIDEOUT_STORE_ROOT="$store" "$hideout" profile host-app-mode "$profile_name" safe >/dev/null
  printf 'projection_trusted_grant=passed\n'
}

latest_external_host_app_audit() {
  local candidate found=""
  while IFS= read -r candidate; do
    if grep -q '"action":"host.app.open-resource"' "$candidate" &&
      grep -q '"packId":"test.external-vscode"' "$candidate"; then
      found="$candidate"
    fi
  done < <(find "$store/sessions" -type f -name audit.jsonl 2>/dev/null | sort)
  [ -n "$found" ] || return 1
  printf '%s\n' "$found"
}

wait_for_external_host_app_decision() {
  local profile_name="$1" output="$2" i
  for i in $(seq 1 180); do
    if HIDEOUT_STORE_ROOT="$store" "$hideout" decision list \
      --kind host-app.open-resource --profile "$profile_name" --include-terminal >"$output" 2>"$output.err"; then
      if jq -e 'any(.[];
        (.state == "pending" or .state == "claimed") and
        .proposedAction.binding.packId == "test.external-vscode" and
        .proposedAction.binding.command == "hcode")' "$output" >/dev/null; then
        return 0
      fi
    fi
    sleep 1
  done
  echo "gate2: timed out waiting for external host-app decision" >&2
  cat "$output" "$output.err" >&2 2>/dev/null || true
  return 1
}

run_projection_external_invalidation() {
  local profile_name="$1" revision_id="$2" operation="$3"
  local tag="external-$operation"
  rm -f "$projection_external_workspace/$tag-"*.signal \
    "$projection_external_workspace/$tag-"*.rc \
    "$projection_external_workspace/$tag-"*.ready
  (
    set +e
    HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
      "$hideout" run --profile "$profile_name" --backend lima --network tun2socks \
        --proxy-secret projection-proxy --workspace "$projection_external_workspace" -- sh -c '
set +e
tag="$1"
hcode -n . >/dev/null 2>&1
printf "%s\n" "$?" > "$tag-first.rc"
: > "$tag-first.ready"
while [ ! -f "$tag-approve.signal" ]; do sleep 0.1; done
hcode -n . >/dev/null 2>&1
printf "%s\n" "$?" > "$tag-approved.rc"
: > "$tag-approved.ready"
while [ ! -f "$tag-invalidate.signal" ]; do sleep 0.1; done
hcode -n . >/dev/null 2>&1
printf "%s\n" "$?" > "$tag-invalidated.rc"
' gate2-external-invalidation "$tag" \
      >"$tmp/$tag.out" 2>"$tmp/$tag.err"
  ) &
  projection_run_pid=$!
  wait_for_file "$projection_external_workspace/$tag-first.ready" "$operation external first refusal"
  wait_for_external_host_app_decision "$profile_name" "$tmp/$tag-decisions.json"
  local decision_id claim_json claim_token
  decision_id="$(jq -r '[.[] | select(
    (.state == "pending" or .state == "claimed") and
    .proposedAction.binding.packId == "test.external-vscode" and
    .proposedAction.binding.command == "hcode"
  )] | sort_by(.createdAt) | last | .id // empty' "$tmp/$tag-decisions.json")"
  [ -n "$decision_id" ]
  claim_json="$(HIDEOUT_STORE_ROOT="$store" "$hideout" decision claim --surface gate2-external "$decision_id")"
  claim_token="$(printf '%s' "$claim_json" | jq -er '.claimToken')"
  HIDEOUT_STORE_ROOT="$store" "$hideout" decision approve --claim-token "$claim_token" "$decision_id" >/dev/null
  : >"$projection_external_workspace/$tag-approve.signal"
  wait_for_file "$projection_external_workspace/$tag-approved.ready" "$operation external approval"
  test "$(cat "$projection_external_workspace/$tag-first.rc")" != "0"
  test "$(cat "$projection_external_workspace/$tag-approved.rc")" = "0"

  case "$operation" in
    disable)
      HIDEOUT_STORE_ROOT="$store" "$hideout" app disable --profile "$profile_name" \
        --pack test.external-vscode --revision "$revision_id" --yes >/dev/null
      ;;
    revoke)
      HIDEOUT_STORE_ROOT="$store" "$hideout" app revoke --pack test.external-vscode \
        --revision "$revision_id" --yes >/dev/null
      ;;
    *) echo "gate2: invalid external lifecycle operation $operation" >&2; return 2 ;;
  esac
  : >"$projection_external_workspace/$tag-invalidate.signal"
  if ! wait "$projection_run_pid"; then
    echo "gate2: external $operation guest flow failed" >&2
    cat "$tmp/$tag.out" "$tmp/$tag.err" >&2
    return 1
  fi
  projection_run_pid=""
  test "$(cat "$projection_external_workspace/$tag-invalidated.rc")" != "0"
  printf 'host_app_external_%s_no_fallback=passed\n' "$operation"
}

run_projection_external_pack() {
  local profile_name="$1" pack_dir="$2"
  echo "gate2: running external community host-app pack lifecycle"
  if [ ! -f "$pack_dir/hideout.host-app-pack.json" ]; then
    echo "gate2: external host-app fixture is missing" >&2
    return 1
  fi
  pack_dir="$(cd "$pack_dir" && pwd -P)"
  projection_external_workspace="$(mktemp -d "$HOME/hideout-gate2-external-host-app.XXXXXX")"
  mkdir -p "$projection_external_workspace/src"
  printf 'package main\n' >"$projection_external_workspace/src/main.go"

  (
    set +e
    HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
      "$hideout" run --profile "$profile_name" --backend lima --network tun2socks \
        --proxy-secret projection-proxy --workspace "$projection_external_workspace" -- sh -eu -c '
if command -v hcode >/dev/null 2>&1; then exit 91; fi
: > external-old-before.ready
while [ ! -f external-install.signal ]; do sleep 0.1; done
if command -v hcode >/dev/null 2>&1; then exit 92; fi
: > external-old-after.ready
' >"$tmp/external-old-session.out" 2>"$tmp/external-old-session.err"
  ) &
  projection_run_pid=$!
  wait_for_file "$projection_external_workspace/external-old-before.ready" "external pre-install session"
  HIDEOUT_STORE_ROOT="$store" "$hideout" app add --path "$pack_dir" --profile "$profile_name" --yes \
    >"$tmp/external-add.out" 2>"$tmp/external-add.err"
  : >"$projection_external_workspace/external-install.signal"
  wait_for_file "$projection_external_workspace/external-old-after.ready" "external old-session immutability"
  wait "$projection_run_pid"
  projection_run_pid=""
  printf 'host_app_external_old_session_immutable=passed\n'

	local revision_id external_audit external_event
  revision_id="$(HIDEOUT_STORE_ROOT="$store" "$hideout" app list --json | jq -er '.hostAppPacks[] | select(.packId == "test.external-vscode") | .activeRevisionId')"
	if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
	  "$hideout" run --profile "$profile_name" --backend lima --network tun2socks \
	    --proxy-secret projection-proxy --workspace "$projection_external_workspace" -- hcode -n -g src/main.go:12:3 \
      >"$tmp/external-workspace.out" 2>"$tmp/external-workspace.err"; then
    echo "gate2: external workspace command failed" >&2
    cat "$tmp/external-workspace.out" "$tmp/external-workspace.err" >&2
    return 1
  fi
	external_audit="$(latest_external_host_app_audit)"
	external_event="$(last_host_app_event "$external_audit" test.external-vscode safe)"
	[ -n "$external_event" ]
	grep -q '"command":"hcode"' <<<"$external_event"
	grep -q '"resourceClass":"workspace"' <<<"$external_event"
	grep -q '"relativeTarget":"src/main.go"' <<<"$external_event"
	grep -q '"outcome":"launched"' <<<"$external_event"
  printf 'host_app_external_workspace=passed\n'

  local portal_target="/hideout/hostfs$hostfs_file"
  if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
	  "$hideout" run --profile "$profile_name" --backend lima --network tun2socks \
	    --proxy-secret projection-proxy --workspace "$projection_external_workspace" \
	    --fs "read:$hostfs_file" -- hcode -n "$portal_target" \
      >"$tmp/external-hostfs.out" 2>"$tmp/external-hostfs.err"; then
    echo "gate2: external HostFS command failed" >&2
    cat "$tmp/external-hostfs.out" "$tmp/external-hostfs.err" >&2
    return 1
  fi
	external_audit="$(latest_external_host_app_audit)"
	external_event="$(last_host_app_event "$external_audit" test.external-vscode safe)"
	[ -n "$external_event" ]
	grep -q '"resourceClass":"hostfs-portal"' <<<"$external_event"
	grep -q '"outcome":"launched"' <<<"$external_event"
	if grep -nF -- "$hostfs_file" <<<"$external_event" >/dev/null ||
	  grep -nE '/hideout/hostfs|cap_[A-Za-z0-9]{12,}|claim_[A-Za-z0-9]{12,}' <<<"$external_event" >/dev/null; then
    echo "gate2: external HostFS evidence leaked lower path or authority" >&2
    return 1
  fi
  printf 'host_app_external_hostfs=passed\n'

  local bad_pack="$tmp/external-bad-identity"
  mkdir -p "$bad_pack"
  jq '.id = "test.external-vscode-bad" |
      .apps[0].expectedTeamId = "AAAAAAAAAA" |
      .bindings[0].commands = ["bad-hcode"]' \
    "$pack_dir/hideout.host-app-pack.json" >"$bad_pack/hideout.host-app-pack.json"
  if HIDEOUT_STORE_ROOT="$store" "$hideout" app add --path "$bad_pack" --profile "$profile_name" --yes \
    >"$tmp/external-bad.out" 2>"$tmp/external-bad.err"; then
    echo "gate2: package-declared mismatched app identity was accepted" >&2
    return 1
  fi
  if HIDEOUT_STORE_ROOT="$store" "$hideout" app list --json | jq -e 'any(.hostAppPacks[]; .packId == "test.external-vscode-bad")' >/dev/null; then
    echo "gate2: failed identity plan partially installed a pack" >&2
    return 1
  fi
  printf 'host_app_external_unsafe_identity_denied=passed\n'

  local elevated_pack="$tmp/external-elevated"
  mkdir -p "$elevated_pack"
  jq '.version = "1.1.0" | .bindings[0].requestedAccess = "ask-each-run"' \
    "$pack_dir/hideout.host-app-pack.json" >"$elevated_pack/hideout.host-app-pack.json"
  HIDEOUT_STORE_ROOT="$store" "$hideout" app update --path "$elevated_pack" --profile "$profile_name" \
    --pack test.external-vscode --access ask-each-run --yes >"$tmp/external-update.out"
  revision_id="$(HIDEOUT_STORE_ROOT="$store" "$hideout" app list --json | jq -er '.hostAppPacks[] | select(.packId == "test.external-vscode") | .activeRevisionId')"
  run_projection_external_invalidation "$profile_name" "$revision_id" disable

  if with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
    "$hideout" run --profile "$profile_name" --backend lima --network tun2socks \
      --proxy-secret projection-proxy --workspace "$projection_external_workspace" -- sh -c 'command -v hcode' \
      >"$tmp/external-disabled.out" 2>"$tmp/external-disabled.err"; then
    echo "gate2: disabled projected command appeared in a new session" >&2
    return 1
  fi
  HIDEOUT_STORE_ROOT="$store" "$hideout" app enable --profile "$profile_name" --pack test.external-vscode \
    --revision "$revision_id" --access ask-each-run --yes >/dev/null
  run_projection_external_invalidation "$profile_name" "$revision_id" revoke
  if with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
    "$hideout" run --profile "$profile_name" --backend lima --network tun2socks \
      --proxy-secret projection-proxy --workspace "$projection_external_workspace" -- sh -c 'command -v hcode' \
      >"$tmp/external-revoked.out" 2>"$tmp/external-revoked.err"; then
    echo "gate2: revoked projected command appeared in a new session" >&2
    return 1
  fi
  # External safe recipes share the same run-scoped isolated state root as the
  # built-in recipe. Stop only their recorded main process; terminating every
  # matching Electron child produces a visible renderer-crash dialog.
  projection_stop_safe_app
  printf 'host_app_external_gate2=passed\n'
}
