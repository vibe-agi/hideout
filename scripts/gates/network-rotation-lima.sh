#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"
# shellcheck source=scripts/lib/gate-result.sh
. "$repo_root/scripts/lib/gate-result.sh"

require_real=0
preflight_only=0
out="$repo_root/.artifacts/045/network-rotation"
gate_timeout="${HIDEOUT_NETWORK_ROTATION_TIMEOUT:-20m}"
gate_completed=0
current_stage="bootstrap"
run_id=""
evidence_root=""
work_root=""
lima_home=""
store_root=""
workspace=""
hideout=""
run_pid=""
http_pid=""
proxy_one_pid=""
proxy_two_pid=""
crash_daemon_pid=""
normal_daemon_pid=""
matrix_config_pid=""
secret_ref=""
secret_set=0
keychain_service=""
keychain_item_created=0
environment_name="rotation"
environment_id=""
source_commit="$(git rev-parse HEAD)"
if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then
  source_dirty=true
else
  source_dirty=false
fi

usage() {
  printf '%s\n' \
    "Usage: scripts/gates/network-rotation-lima.sh [--require-real] [--preflight] [--out DIR]" \
    "" \
    "Runs one active workload through two independent local SOCKS5 upstreams" \
    "and proves that a managed-secret rotation preserves one accepted old" \
    "connection while moving new connections online without restarting the" \
    "daemon, recreating the VM, or replacing the target. It then crashes a" \
    "gate-only production daemon at every durable network boundary and proves" \
    "restart reconciliation against the same real Lima incarnation."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --require-real)
      require_real=1
      shift
      ;;
    --preflight)
      preflight_only=1
      shift
      ;;
    --out)
      if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
        printf 'network-rotation-lima: --out requires a directory\n' >&2
        exit 2
      fi
      out="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'network-rotation-lima: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

fail() {
  printf 'network-rotation-lima: %s\n' "$*" >&2
  exit 1
}

not_run() {
  if [ "$require_real" -eq 1 ]; then
    fail "$*"
  fi
  printf 'network-rotation-lima: not-run: %s\n' "$*"
  exit 77
}

require_command() {
  command -v "$1" >/dev/null 2>&1 ||
    not_run "missing required command: $1"
}

secret_status_has_version() {
  local status_file="$1" expected_version="$2"
  grep -Eq \
    "(^|[[:space:]])version=${expected_version}([[:space:]]|$)" \
    "$status_file"
}

with_timeout() {
  local duration="$1"
  shift
  "$@" &
  local command_pid=$!
  (
    sleep "$duration"
    if kill -0 "$command_pid" 2>/dev/null; then
      kill "$command_pid" 2>/dev/null || true
      sleep 2
      kill -KILL "$command_pid" 2>/dev/null || true
    fi
  ) &
  local timer_pid=$!
  local status=0
  if wait "$command_pid"; then
    status=0
  else
    status=$?
  fi
  kill "$timer_pid" 2>/dev/null || true
  wait "$timer_pid" 2>/dev/null || true
  return "$status"
}

run_hideout() {
  env \
    "HIDEOUT_STORE_ROOT=$store_root" \
    "LIMA_HOME=$lima_home" \
    "HIDEOUT_LINUX_SHIM_PATH=$linux_shim" \
    "HIDEOUT_LINUX_HOSTFSD_PATH=$linux_hostfsd" \
    "HIDEOUT_LINUX_SESSION_SUPERVISOR_PATH=$linux_supervisor" \
    "HIDEOUT_LINUX_OBSERVER_PATH=$linux_observer" \
    "HIDEOUT_LINUX_WORKSPACE_PORTAL_PATH=$linux_workspace_portal" \
    "HIDEOUT_LINUX_TUN2SOCKS_PATH=$linux_tun2socks" \
    "HIDEOUT_LINUX_DNS_STUB_PATH=$linux_dns_stub" \
    "$hideout" "$@"
}

wait_for_file() {
  local path="$1"
  local timeout_seconds="$2"
  local description="$3"
  local deadline
  deadline=$(($(date +%s) + timeout_seconds))
  while [ ! -f "$path" ]; do
    if [ -n "${run_pid:-}" ] && ! kill -0 "$run_pid" 2>/dev/null; then
      local run_status=0
      if wait "$run_pid"; then
        run_status=0
      else
        run_status=$?
      fi
      run_pid=""
      local diagnostic=""
      if [ -n "${evidence_root:-}" ] &&
        [ -d "$evidence_root/logs" ] &&
        [ -f "${work_root:-}/run.err" ]; then
        local diagnostic_candidate
        diagnostic_candidate="$evidence_root/logs/active-workload.stderr.log"
        if cp "$work_root/run.err" "$diagnostic_candidate" &&
          chmod 0600 "$diagnostic_candidate"; then
          diagnostic="$diagnostic_candidate"
        fi
      fi
      if [ -n "$diagnostic" ]; then
        fail "workload exited with status $run_status before $description; private stderr evidence: $diagnostic"
      fi
      fail "workload exited with status $run_status before $description"
    fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
      fail "timed out waiting for $description"
    fi
    sleep 0.2
  done
}

wait_for_gate_file() {
  local file_path="$1"
  local process_id="$2"
  local timeout_seconds="$3"
  local description="$4"
  local deadline
  deadline=$(($(date +%s) + timeout_seconds))
  while [ ! -f "$file_path" ]; do
    kill -0 "$process_id" 2>/dev/null ||
      fail "$description process exited before publishing evidence"
    if [ "$(date +%s)" -ge "$deadline" ]; then
      fail "timed out waiting for $description"
    fi
    sleep 0.1
  done
}

wait_for_process_exit() {
  local process_id="$1"
  local timeout_seconds="$2"
  local description="$3"
  local deadline
  deadline=$(($(date +%s) + timeout_seconds))
  while :; do
    local process_state
    process_state="$(
      (ps -o state= -p "$process_id" 2>/dev/null || true) |
        tr -d '[:space:]'
    )"
    case "$process_state" in
      '' | Z*) return 0 ;;
    esac
    if [ "$(date +%s)" -ge "$deadline" ]; then
      fail "timed out waiting for $description to exit"
    fi
    sleep 0.1
  done
}

network_guest_snapshot() {
  local output_path="$1"
  local raw_path="$output_path.raw"
  [ -n "${instance_name:-}" ] ||
    fail "guest network snapshot requires an exact Lima instance"
  # The single-quoted program is intentionally passed verbatim to the guest.
  # shellcheck disable=SC2016
  LIMA_HOME="$lima_home" limactl shell \
    --tty=false --workdir / "$instance_name" -- \
    sh -eu -c '
network_dir=/hideout/runtime/services/network/network
resolver=$(cat "$network_dir/mediated-resolver")
dns_pid=$(cat "$network_dir/dns-stub.pid")
test -d "/proc/$dns_pid"
boot_id=$(cat /proc/sys/kernel/random/boot_id)
printf "%s\n%s\n%s\n" "$resolver" "$dns_pid" "$boot_id"
' >"$raw_path"
  local resolver dns_pid boot_id
  resolver="$(sed -n '1p' "$raw_path" | tr -d '\r')"
  dns_pid="$(sed -n '2p' "$raw_path" | tr -d '\r')"
  boot_id="$(sed -n '3p' "$raw_path" | tr -d '\r')"
  case "$resolver" in
    1.1.1.1 | 9.9.9.9) ;;
    *) fail "guest reported an unexpected mediated resolver" ;;
  esac
  case "$dns_pid" in
    '' | *[!0-9]*) fail "guest reported an invalid DNS service PID" ;;
  esac
  case "$boot_id" in
    ????????-????-????-????-????????????) ;;
    *) fail "guest reported an invalid boot identity" ;;
  esac
  jq -n \
    --arg resolver "$resolver" \
    --argjson dnsPid "$dns_pid" \
    --arg bootId "$boot_id" \
    '{resolver:$resolver,dnsPid:$dnsPid,bootId:$bootId}' \
    >"$output_path"
  chmod 0600 "$raw_path" "$output_path"
}

start_network_crash_daemon() {
  local effect="$1"
  local ready_path="$2"
  local marker_path="$3"
  local log_path="$4"
  rm -f "$ready_path" "$marker_path"
  env \
    "HIDEOUT_STORE_ROOT=$store_root" \
    "LIMA_HOME=$lima_home" \
    "HIDEOUT_LINUX_SHIM_PATH=$linux_shim" \
    "HIDEOUT_LINUX_HOSTFSD_PATH=$linux_hostfsd" \
    "HIDEOUT_LINUX_SESSION_SUPERVISOR_PATH=$linux_supervisor" \
    "HIDEOUT_LINUX_OBSERVER_PATH=$linux_observer" \
    "HIDEOUT_LINUX_WORKSPACE_PORTAL_PATH=$linux_workspace_portal" \
    "HIDEOUT_LINUX_TUN2SOCKS_PATH=$linux_tun2socks" \
    "HIDEOUT_LINUX_DNS_STUB_PATH=$linux_dns_stub" \
    "HIDEOUT_REAL_LIMA_NETWORK_CRASH_GATE=1" \
    "HIDEOUT_NETWORK_CRASH_EFFECT=$effect" \
    "HIDEOUT_NETWORK_CRASH_READY=$ready_path" \
    "HIDEOUT_NETWORK_CRASH_MARKER=$marker_path" \
    "HIDEOUT_NETWORK_CRASH_SECRET_FILE=$work_root/proxy-two.url" \
    "HIDEOUT_NETWORK_CRASH_SECRET_REF=$secret_ref" \
    "HIDEOUT_NETWORK_CRASH_SECRET_GENERATION=2" \
    "$crash_daemon" \
    -test.run '^TestRealLimaNetworkCrashGateDaemon$' \
    -test.v >"$log_path" 2>&1 &
  crash_daemon_pid=$!
  wait_for_gate_file \
    "$ready_path" "$crash_daemon_pid" 180 \
    "network crash daemon readiness"
  jq -e \
    --arg effect "$effect" \
    --argjson pid "$crash_daemon_pid" '
      .schema == "hideout.network-crash-daemon-ready/v1" and
      .effect == $effect and
      .pid == $pid and
      (.socket | type == "string" and length > 0)
    ' "$ready_path" >/dev/null ||
    fail "network crash daemon readiness evidence is invalid"
}

start_normal_recovery_daemon() {
  local log_path="$1"
  run_hideout daemon start >"$log_path" 2>&1 &
  normal_daemon_pid=$!
  local attempt=0
  while [ "$attempt" -lt 1800 ]; do
    if run_hideout daemon status >"$log_path.status" 2>"$log_path.status.err"; then
      chmod 0600 "$log_path.status" "$log_path.status.err"
      return 0
    fi
    kill -0 "$normal_daemon_pid" 2>/dev/null ||
      fail "normal daemon exited during startup reconciliation"
    attempt=$((attempt + 1))
    sleep 0.1
  done
  fail "timed out waiting for normal daemon startup reconciliation"
}

stop_normal_recovery_daemon() {
  [ -n "${normal_daemon_pid:-}" ] || return 0
  run_hideout daemon stop >/dev/null 2>&1 ||
    fail "normal recovery daemon refused an explicit stop"
  wait_for_process_exit "$normal_daemon_pid" 60 "normal recovery daemon"
  local status=0
  set +e
  wait "$normal_daemon_pid"
  status=$?
  set -e
  normal_daemon_pid=""
  [ "$status" -eq 0 ] ||
    fail "normal recovery daemon exited with status $status"
}

wait_for_lifecycle_settled() {
  local environment="$1"
  local output_path="$2"
  local temporary_path="$output_path.tmp"
  local attempt=0
  while [ "$attempt" -lt 1200 ]; do
    if run_hideout daemon status \
      >"$temporary_path" 2>"$output_path.err" &&
      jq -e --arg environment "$environment" '
        any(.lifecycle[]?;
          .environmentId == $environment and
          .reconciliation != "pending")
      ' "$temporary_path" >/dev/null; then
      mv "$temporary_path" "$output_path"
      chmod 0600 "$output_path" "$output_path.err"
      return 0
    fi
    attempt=$((attempt + 1))
    sleep 0.05
  done
  fail "lifecycle reconciliation did not settle after daemon crash"
}

capture_failed_activity() {
  local session_path="$workspace/session-before"
  [ -f "$session_path" ] || return 0
  local failed_session
  failed_session="$(sed -n '1p' "$session_path")"
  case "$failed_session" in
    ses_*) ;;
    *) return 0 ;;
  esac
  run_hideout activity coverage \
    --session "$failed_session" --json \
    >"$work_root/failure-activity-coverage.json" \
    2>"$work_root/failure-activity-coverage.err" || true
  run_hideout activity events \
    --session "$failed_session" --limit 500 --json \
    >"$work_root/failure-activity-events.json" \
    2>"$work_root/failure-activity-events.err" || true
}

proxy_connect_count() {
  local log_path="$1"
  local count
  count="$(grep -c 'hideout-gate-socks5: connect_established$' "$log_path" 2>/dev/null || true)"
  case "$count" in
    '' | *[!0-9]*) fail "proxy connection count is invalid" ;;
  esac
  printf '%s\n' "$count"
}

scan_file_pattern_absent() {
  local label="$1"
  local pattern_file="$2"
  local scan_file="$3"
  local scan_status=0
  set +e
  grep -a -F -f "$pattern_file" -- "$scan_file" >/dev/null 2>&1
  scan_status=$?
  set -e
  case "$scan_status" in
    0) fail "$label reached a non-secret evidence surface" ;;
    1) return 0 ;;
    *) fail "$label scan could not inspect $scan_file" ;;
  esac
}

scan_pattern_absent() {
  local label="$1"
  local pattern_file="$2"
  shift 2
  [ -s "$pattern_file" ] || fail "empty $label pattern file"
  for scan_target in "$@"; do
    if [ -f "$scan_target" ]; then
      scan_file_pattern_absent "$label" "$pattern_file" "$scan_target"
      continue
    fi
    [ -d "$scan_target" ] ||
      fail "$label scan target is not a regular file or directory"
    scan_file_sequence=$((scan_file_sequence + 1))
    local file_list="$work_root/scan-files-$scan_file_sequence.list"
    find "$scan_target" -type f -print0 >"$file_list" ||
      fail "$label scan could not enumerate $scan_target"
    chmod 0600 "$file_list"
    while IFS= read -r -d '' scan_file; do
      scan_file_pattern_absent "$label" "$pattern_file" "$scan_file"
    done <"$file_list"
  done
}

scan_process_value_absent() {
  local label="$1"
  local pattern_file="$2"
  local scan_status=0
  set +e
  # This must inspect full argv plus environments; pgrep cannot do that.
  # shellcheck disable=SC2009
  ps axeww -o command= |
    grep -F -f "$pattern_file" >/dev/null 2>&1
  scan_status=$?
  set -e
  case "$scan_status" in
    0) fail "$label appeared in a process argument or environment" ;;
    1) return 0 ;;
    *) fail "$label process scan failed with exit $scan_status" ;;
  esac
}

purge_isolated_keychain_item() {
  [ "${keychain_item_created:-0}" -eq 1 ] || return 0
  [ -n "${keychain_service:-}" ] && [ -n "${secret_ref:-}" ] || return 1
  local delete_status=0
  with_timeout 15 security delete-generic-password \
    -s "$keychain_service" -a "$secret_ref" \
    >/dev/null 2>&1 || delete_status=$?
  if [ "$delete_status" -ne 0 ]; then
    local find_status=0
    with_timeout 5 security find-generic-password \
      -s "$keychain_service" -a "$secret_ref" \
      >/dev/null 2>&1 || find_status=$?
    [ "$find_status" -eq 44 ] || return 1
  fi
  keychain_item_created=0
}

write_failure_evidence() {
  local status="$1"
  [ -n "${out:-}" ] || return 0
  mkdir -p "$out"
  chmod 0700 "$out" 2>/dev/null || true
  local failure_path="$out/result.json"
  if [ -n "${evidence_root:-}" ] && [ -d "$evidence_root" ]; then
    failure_path="$evidence_root/summary.json"
  fi
  jq -n \
    --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
    --arg commit "$source_commit" \
    --argjson dirty "$source_dirty" \
    --arg stage "${current_stage:-unknown}" \
    --argjson exitCode "$status" \
    '{
      schema: "hideout.network-rotation-lima-evidence/v1",
      generatedAt: $generatedAt,
      source: {commit: $commit, dirty: $dirty},
      result: "failed",
      failure: {stage: $stage, exitCode: $exitCode}
    }' >"$failure_path" 2>/dev/null || true
  chmod 0600 "$failure_path" 2>/dev/null || true
  if [ -n "${evidence_root:-}" ] && [ -d "$evidence_root" ]; then
    find "$evidence_root" -type f -exec chmod 0600 {} + 2>/dev/null || true
  fi
  if [ "$failure_path" != "$out/result.json" ] &&
    [ -f "$failure_path" ]; then
    local failure_sha
    failure_sha="$(gate_sha256_file "$failure_path" 2>/dev/null || true)"
    local pointer_tmp
    pointer_tmp="$(mktemp "$out/.result.XXXXXX" 2>/dev/null || true)"
    if [ -n "$pointer_tmp" ]; then
      jq -n \
        --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
        --arg commit "$source_commit" \
        --argjson dirty "$source_dirty" \
        --arg runId "$run_id" \
        --arg summary "$run_id/summary.json" \
        --arg summarySHA256 "$failure_sha" \
        '{
          schema: "hideout.network-rotation-lima-pointer/v1",
          generatedAt: $generatedAt,
          source: {commit: $commit, dirty: $dirty},
          result: "failed",
          candidateAcceptance: false,
          runId: $runId,
          summary: $summary,
          summarySHA256: $summarySHA256
        }' >"$pointer_tmp" 2>/dev/null || true
      chmod 0600 "$pointer_tmp" 2>/dev/null || true
      mv "$pointer_tmp" "$out/result.json" 2>/dev/null || true
    fi
  fi
}

cleanup() {
  local status=$?
  trap - EXIT
  set +e
  set +u

  if [ -n "${workspace:-}" ]; then
    : >"$workspace/rotate-go" 2>/dev/null
    : >"$workspace/release" 2>/dev/null
  fi
  if [ -n "${run_pid:-}" ] && kill -0 "$run_pid" 2>/dev/null; then
    kill "$run_pid" 2>/dev/null
    wait "$run_pid" 2>/dev/null
  fi
  run_pid=""

  for process_id in \
    "${matrix_config_pid:-}" \
    "${crash_daemon_pid:-}" \
    "${normal_daemon_pid:-}"; do
    if [ -n "$process_id" ] && kill -0 "$process_id" 2>/dev/null; then
      kill "$process_id" 2>/dev/null
      wait "$process_id" 2>/dev/null
    fi
  done
  matrix_config_pid=""
  crash_daemon_pid=""
  normal_daemon_pid=""

  if [ -x "${hideout:-}" ] && [ -n "${store_root:-}" ]; then
    if [ -n "${environment_id:-}" ]; then
      run_hideout stop "$environment_id" >/dev/null 2>&1 || true
    fi
    run_hideout profile network default direct >/dev/null 2>&1 || true
    if [ "${secret_set:-0}" -eq 1 ]; then
      run_hideout secret delete "$secret_ref" --yes >/dev/null 2>&1 || true
      secret_set=0
    fi
    if [ -n "${environment_id:-}" ]; then
      run_hideout clean "$environment_id" >/dev/null 2>&1 || true
    else
      run_hideout clean >/dev/null 2>&1 || true
    fi
    run_hideout daemon stop >/dev/null 2>&1 || true
  fi
  purge_isolated_keychain_item || status=1

  for process_id in "$proxy_one_pid" "$proxy_two_pid" "$http_pid"; do
    if [ -n "$process_id" ] && kill -0 "$process_id" 2>/dev/null; then
      kill "$process_id" 2>/dev/null
      wait "$process_id" 2>/dev/null
    fi
  done

  if [ -n "${lima_home:-}" ] && [ -d "$lima_home" ] &&
    command -v limactl >/dev/null 2>&1; then
    LIMA_HOME="$lima_home" limactl list --json 2>/dev/null |
      jq -r '.name // empty' 2>/dev/null |
      while IFS= read -r cleanup_instance; do
        [ -n "$cleanup_instance" ] || continue
        LIMA_HOME="$lima_home" limactl delete \
          --force --tty=false "$cleanup_instance" >/dev/null 2>&1 || true
      done
  fi
  if [ -n "${lima_home:-}" ] && [ -d "$lima_home" ]; then
    case "$lima_home" in
      "${tmp_base:-/tmp}"/ho-nr-lima.*)
        find "$lima_home" -depth -delete
        ;;
      *)
        printf 'network-rotation-lima: refusing unexpected Lima cleanup path %s\n' \
          "$lima_home" >&2
        status=1
        ;;
    esac
  fi
  if [ "${HIDEOUT_NETWORK_ROTATION_KEEP_TMP:-0}" = "1" ]; then
    [ -n "${work_root:-}" ] &&
      printf 'network-rotation-lima: retained sensitive debug directory %s\n' \
        "$work_root" >&2
  elif [ -n "${work_root:-}" ] && [ -d "$work_root" ]; then
    case "$work_root" in
      "${tmp_base:-/tmp}"/hideout-network-rotation.*)
        find "$work_root" -depth -delete
        ;;
      *)
        printf 'network-rotation-lima: refusing unexpected cleanup path %s\n' \
          "$work_root" >&2
        status=1
        ;;
    esac
  fi

  if [ "$gate_completed" != "1" ]; then
    [ "$status" -ne 0 ] || status=1
    write_failure_evidence "$status"
    gate_require_completion "network-rotation-lima"
  fi
  exit "$status"
}

require_command go
require_command jq
require_command python3
require_command shasum
require_command bash
require_command ps
if [ "$preflight_only" -eq 0 ]; then
  require_command limactl
  require_command security
  [ "$(uname -s)" = "Darwin" ] ||
    not_run "real reference lane requires macOS"
  [ "$(uname -m)" = "arm64" ] ||
    not_run "real reference lane requires arm64"
fi

if [ -L "$out" ]; then
  fail "evidence directory must not be a symlink"
fi
mkdir -p "$out"
out="$(cd "$out" && pwd -P)"
chmod 0700 "$out"

tmp_base="${HIDEOUT_NETWORK_ROTATION_TMPDIR:-/tmp}"
mkdir -p "$tmp_base"
tmp_base="$(cd "$tmp_base" && pwd -P)"
work_root="$(mktemp -d "$tmp_base/hideout-network-rotation.XXXXXX")"
lima_home="$(mktemp -d "$tmp_base/ho-nr-lima.XXXXXX")"
store_root="$work_root/store"
workspace="$work_root/workspace"
bin_dir="$work_root/bin"
http_root="$work_root/http"
mkdir -p "$store_root" "$workspace" "$bin_dir" "$http_root"
chmod 0700 \
  "$work_root" "$lima_home" "$store_root" "$workspace" "$bin_dir" "$http_root"
trap cleanup EXIT

hideout="$bin_dir/hideout"
linux_shim="$bin_dir/hideout-shim-linux-arm64"
linux_hostfsd="$bin_dir/hideout-hostfsd-linux-arm64"
linux_supervisor="$bin_dir/hideout-session-supervisor-linux-arm64"
linux_observer="$bin_dir/hideout-observer-linux-arm64"
linux_workspace_portal="$bin_dir/hideout-workspace-portal-linux-arm64"
linux_tun2socks="$bin_dir/tun2socks-linux-arm64"
linux_dns_stub="$bin_dir/hideout-dns-stub-linux-arm64"
proxy_binary="$bin_dir/hideout-gate-socks5"
crash_daemon="$bin_dir/hideout-network-crash-daemon.test"

keychain_service="$(
  python3 - "$store_root" <<'PY'
import hashlib
import os
import sys

root = os.path.realpath(sys.argv[1])
digest = hashlib.sha256(
    b"hideout.keychain-store.v1\0" + root.encode("utf-8")
).hexdigest()[:24]
print("com.vibe-agi.hideout.secret.store." + digest)
PY
)"

current_stage="product-preflight"
go build -trimpath -o "$hideout" ./cmd/hideout
go test -c -trimpath -o "$crash_daemon" ./internal/app
"$hideout" help secret >"$work_root/secret-help.txt"
"$hideout" help connect >"$work_root/connect-help.txt"
go test ./internal/app \
  -run '^TestSecretListAndStatusRenderMetadataOnly$' \
  -count=1 >"$work_root/secret-status-contract.log"
printf '%s\n' \
  'local-proxy  available  version=2  provider=macos-keychain' \
  >"$work_root/secret-status-contract.txt"
secret_status_has_version "$work_root/secret-status-contract.txt" 2 ||
  fail "current secret status version contract was rejected"
printf '%s\n' \
  'local-proxy  available  generation=2  provider=macos-keychain' \
  >"$work_root/secret-status-obsolete.txt"
if secret_status_has_version "$work_root/secret-status-obsolete.txt" 2; then
  fail "obsolete secret generation terminology was accepted"
fi
bash -n "$0"
if [ "$preflight_only" -eq 1 ]; then
  gate_completed=1
  printf 'network-rotation-lima: preflight=passed\n'
  exit 0
fi

run_id="run-$(date -u +'%Y%m%dT%H%M%SZ')-$$"
evidence_root="$out/$run_id"
[ ! -e "$evidence_root" ] ||
  fail "evidence run directory already exists"
mkdir "$evidence_root" "$evidence_root/logs"
chmod 0700 "$evidence_root" "$evidence_root/logs"

current_stage="go-refinement"
go test ./internal/manager \
  -run '^(TestSecretRotateCommitsGenerationAndAllLiveRoutesTogether|TestSecretRotateRouteFailureKeepsOldGenerationAndRestoresAllRoutes)$' \
  -count=1 -v >"$evidence_root/logs/network-refinement.log" 2>&1

current_stage="linux-helper-build"
"$hideout" shim build-linux \
  --out "$linux_shim" --goarch arm64 --source "$repo_root" >/dev/null
"$hideout" hostfsd build-linux \
  --out "$linux_hostfsd" --goarch arm64 --source "$repo_root" >/dev/null
go run ./internal/helperbin/cmd/build-session-supervisor \
  --out "$linux_supervisor" --goarch arm64 --source "$repo_root" >/dev/null
go run ./internal/helperbin/cmd/build-observer \
  --out "$linux_observer" --goarch arm64 --source "$repo_root" >/dev/null
go run ./internal/helperbin/cmd/build-workspace-portal \
  --out "$linux_workspace_portal" --goarch arm64 --source "$repo_root" >/dev/null
go run ./internal/helperbin/cmd/build-tun2socks \
  --out "$linux_tun2socks" --goarch arm64 --source "$repo_root" >/dev/null
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
  go build -trimpath -o "$linux_dns_stub" ./cmd/hideout-dns-stub
go build -trimpath -o "$proxy_binary" ./cmd/hideout-gate-socks5
chmod 0700 \
  "$linux_shim" "$linux_hostfsd" "$linux_supervisor" "$linux_observer" \
  "$linux_workspace_portal" "$linux_tun2socks" "$linux_dns_stub" \
  "$proxy_binary" "$crash_daemon"

printf '%s\n' "online route fixture" >"$http_root/fixture.txt"
python3 -u - "$http_root" \
  >"$work_root/http.out" 2>"$work_root/http.log" <<'PY' &
import functools
import http.server
import sys


class PersistentHandler(http.server.SimpleHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, _format, *_args):
        host, port = self.client_address
        print(
            f"fixture_request conn={host}:{port} path={self.path}",
            file=sys.stderr,
            flush=True,
        )


root = sys.argv[1]
handler = functools.partial(PersistentHandler, directory=root)
server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
host, port = server.server_address
print(f"Serving HTTP on {host} port {port}", flush=True)
server.serve_forever()
PY
http_pid=$!
for _ in $(seq 1 100); do
  grep -Eq 'Serving HTTP on .* port [0-9]+' "$work_root/http.out" && break
  kill -0 "$http_pid" 2>/dev/null ||
    fail "local HTTP fixture exited before publishing"
  sleep 0.05
done
http_port="$(
  sed -nE 's/.* port ([0-9]+).*/\1/p' "$work_root/http.out" |
    head -1
)"
case "$http_port" in
  '' | *[!0-9]*) fail "local HTTP fixture did not publish a port" ;;
esac

start_proxy() {
  local label="$1"
  local url_path="$work_root/proxy-$label.url"
  local log_path="$work_root/proxy-$label.log"
  "$proxy_binary" \
    --listen 127.0.0.1:0 \
    --url-host 127.0.0.1 \
    --authenticated \
    --map-connect "1.1.1.1:443=127.0.0.1:$http_port" \
    >"$url_path" 2>"$log_path" &
  local process_id=$!
  case "$label" in
    one) proxy_one_pid="$process_id" ;;
    two) proxy_two_pid="$process_id" ;;
    *) fail "unknown proxy fixture label: $label" ;;
  esac
  for _ in $(seq 1 100); do
    if [ -s "$url_path" ]; then
      chmod 0600 "$url_path" "$log_path"
      return 0
    fi
    kill -0 "$process_id" 2>/dev/null ||
      fail "proxy $label exited before publishing"
    sleep 0.05
  done
  fail "proxy $label did not publish its private URL"
}

validate_proxy_url() {
  local proxy_url="$1"
  local pattern_file="$2"
  local authority credentials endpoint username password host port
  case "$proxy_url" in
    socks5://*) authority="${proxy_url#socks5://}" ;;
    *) fail "local proxy fixture did not publish a SOCKS5 URL" ;;
  esac
  case "$authority" in
    *@*) ;;
    *) fail "local proxy fixture URL is missing credentials" ;;
  esac
  credentials="${authority%@*}"
  endpoint="${authority##*@}"
  username="${credentials%%:*}"
  password="${credentials#*:}"
  host="${endpoint%:*}"
  port="${endpoint##*:}"
  [ -n "$username" ] && [ -n "$password" ] ||
    fail "local proxy fixture URL has empty credentials"
  [ "$host" = "127.0.0.1" ] ||
    fail "local proxy fixture URL is not loopback-only"
  case "$port" in
    '' | *[!0-9]*) fail "local proxy fixture URL has an invalid port" ;;
  esac
  printf '%s\n%s\n%s\n' \
    "$proxy_url" "$username" "$password" >"$pattern_file"
  chmod 0600 "$pattern_file"
}

start_proxy one
start_proxy two
proxy_one_url="$(sed -n '1p' "$work_root/proxy-one.url")"
proxy_two_url="$(sed -n '1p' "$work_root/proxy-two.url")"
proxy_one_patterns="$work_root/proxy-one.patterns"
proxy_two_patterns="$work_root/proxy-two.patterns"
validate_proxy_url "$proxy_one_url" "$proxy_one_patterns"
validate_proxy_url "$proxy_two_url" "$proxy_two_patterns"
[ "$proxy_one_url" != "$proxy_two_url" ] ||
  fail "proxy fixtures unexpectedly share one credential or endpoint"

secret_ref="rotation-proxy-$(python3 -c 'import secrets; print(secrets.token_hex(8))')"
current_stage="managed-secret-and-profile"
run_hideout init \
  --no-input --profile default --template dev \
  --backend lima --network direct >"$work_root/init.out"
keychain_item_created=1
printf '%s' "$proxy_one_url" |
  run_hideout secret set "$secret_ref" --stdin --yes \
    >"$work_root/secret-set.out" 2>"$work_root/secret-set.err"
secret_set=1
run_hideout profile network default tun2socks \
  --proxy-secret "$secret_ref" --mediated-resolver 1.1.1.1 \
  >"$work_root/profile-network.out" 2>"$work_root/profile-network.err"
run_hideout env create "$environment_name" \
  --profile default --backend lima --workspace "$workspace" \
  >"$work_root/env-create.out" 2>"$work_root/env-create.err"

current_stage="active-route-before-rotation"
# The single-quoted target program is intentionally passed verbatim to the VM.
# shellcheck disable=SC2016
with_timeout "$gate_timeout" run_hideout run \
  --verbose --env "$environment_name" --profile default \
  --backend lima --workspace "$workspace" \
  --network tun2socks --proxy-secret "$secret_ref" \
  --mediated-resolver 1.1.1.1 \
  -- sh -eu -c '
workspace=/workspace
url=http://1.1.1.1:443/fixture.txt
for name in HTTP_PROXY HTTPS_PROXY ALL_PROXY NO_PROXY \
  http_proxy https_proxy all_proxy no_proxy; do
  eval "value=\${$name:-}"
  [ -z "$value" ] || {
    echo "proxy environment leaked to target: $name" >&2
    exit 51
  }
done

transport_result=not-visible
if [ -e /run/hideout-observer-streams ]; then
  if sh -c '"'"': > /run/hideout-observer-streams/target-write'"'"' 2>/dev/null; then
    echo "target wrote observer transport" >&2
    exit 52
  fi
  transport_result=write-denied
fi
printf "observer_transport_tamper=%s\n" "$transport_result"

cgroup_result=not-visible
if [ -e /sys/fs/cgroup/cgroup.procs ]; then
  if sh -c '"'"'pid=$(cut -d " " -f 1 /proc/self/stat); printf "%s\n" "$pid" > /sys/fs/cgroup/cgroup.procs'"'"' 2>/dev/null; then
    echo "target escaped its cgroup" >&2
    exit 53
  fi
  cgroup_result=write-denied
fi
printf "cgroup_escape_tamper=%s\n" "$cgroup_result"

printf "%s\n" "$$" >"$workspace/target-pid-before"
cat /proc/sys/kernel/random/boot_id >"$workspace/boot-before"
printf "%s\n" "${HIDEOUT_SESSION_ID:-}" >"$workspace/session-before"

workspace_write_failure() {
  failure_label=$1
  echo "workspace write failed: $failure_label" >&2
  id >&2 || true
  stat -c "workspace mode=%a uid=%u gid=%g type=%F" "$workspace" >&2 ||
    true
  grep " /workspace " /proc/self/mountinfo >&2 || true
  if : >"$workspace/write-probe-after-failure"; then
    echo "workspace write probe unexpectedly succeeded" >&2
  else
    echo "workspace write probe also failed" >&2
  fi
  exit 55
}

bash -eu -c '"'"'
workspace=$1
target_host=1.1.1.1
exec 8<>"/dev/tcp/$target_host/443"
request() {
  request_path=$1
  printf "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: keep-alive\r\n\r\n" \
    "$request_path" "$target_host" >&8
  while IFS= read -r response_line <&8; do
    if [ "$response_line" = "online route fixture" ]; then
      return 0
    fi
  done
  echo "held route connection ended before a complete response" >&2
  return 1
}
request "/fixture.txt?phase=held-before"
: >"$workspace/held-before-done"
while [ ! -f "$workspace/rotate-go" ]; do sleep 0.05; done
request "/fixture.txt?phase=held-after"
: >"$workspace/held-after-done"
exec 8>&-
'"'"' held-route "$workspace" &
held_route_pid=$!
while [ ! -f "$workspace/held-before-done" ]; do
  if ! kill -0 "$held_route_pid" 2>/dev/null; then
    wait "$held_route_pid"
    exit 54
  fi
  sleep 0.05
done
curl -fsS --max-time 20 "$url?phase=before" >/dev/null
: >"$workspace/before-done"
while [ ! -f "$workspace/rotate-go" ]; do sleep 0.05; done
curl -fsS --max-time 20 "$url?phase=after" >/dev/null
wait "$held_route_pid"
test -f "$workspace/held-after-done"

# Keep the already-rotated workload alive across the 30-second daemon session
# renewal boundary, then pressure fresh workspace creates. This catches
# transport or authority failures that a fast happy-path run would finish
# before exercising.
if ! date +%s >"$workspace/renewal-boundary-before"; then
  workspace_write_failure renewal-boundary-before
fi
sleep 40
if ! date +%s >"$workspace/renewal-boundary-after"; then
  workspace_write_failure renewal-boundary-after
fi
workspace_stress_index=0
while [ "$workspace_stress_index" -lt 64 ]; do
  workspace_stress_index=$((workspace_stress_index + 1))
  if ! printf "%s\n" "$workspace_stress_index" \
    >"$workspace/workspace-stress-$workspace_stress_index"; then
    workspace_write_failure "workspace-stress-$workspace_stress_index"
  fi
done
if ! printf "%s\n" "$$" >"$workspace/target-pid-after"; then
  workspace_write_failure target-pid-after
fi
if ! cat /proc/sys/kernel/random/boot_id >"$workspace/boot-after"; then
  workspace_write_failure boot-after
fi
if ! printf "%s\n" "${HIDEOUT_SESSION_ID:-}" >"$workspace/session-after"; then
  workspace_write_failure session-after
fi
: >"$workspace/after-done"
while [ ! -f "$workspace/release" ]; do sleep 0.05; done
' >"$work_root/run.out" 2>"$work_root/run.err" &
run_pid=$!

wait_for_file "$workspace/before-done" 600 "pre-rotation request"
grep -Eq '^observer_transport_tamper=(not-visible|write-denied)$' \
  "$work_root/run.out" ||
  fail "target observer-transport tamper was not rejected"
grep -Eq '^cgroup_escape_tamper=(not-visible|write-denied)$' \
  "$work_root/run.out" ||
  fail "target cgroup escape was not rejected"

run_hideout daemon status >"$work_root/daemon-before.json"
run_hideout env list >"$work_root/env-before.txt"
environment_id="$(
  awk -F'\t' -v name="$environment_name" \
    'NR > 1 && $1 == name {print $15; exit}' \
    "$work_root/env-before.txt"
)"
case "$environment_id" in
  env_*) ;;
  *) fail "active environment identity is missing" ;;
esac
environment_record="$store_root/environments/$environment_id/environment.json"
[ -f "$environment_record" ] ||
  fail "active environment record is missing"
cp "$environment_record" "$work_root/environment-before.json"
instance_name="$(jq -r '.instanceName // empty' "$environment_record")"
[ -n "$instance_name" ] ||
  fail "active environment instance name is missing"
LIMA_HOME="$lima_home" limactl list --json |
  jq -c --arg instance "$instance_name" \
    'select(.name == $instance)' >"$work_root/lima-before.json"
jq -e '.status == "Running"' "$work_root/lima-before.json" >/dev/null ||
  fail "active Lima instance was not running before rotation"

proxy_one_before="$(proxy_connect_count "$work_root/proxy-one.log")"
proxy_two_before="$(proxy_connect_count "$work_root/proxy-two.log")"
[ "$proxy_one_before" -gt 0 ] ||
  fail "pre-rotation route did not reach proxy one"
[ "$proxy_two_before" -eq 0 ] ||
  fail "proxy two was used before its secret generation was reviewed"

current_stage="online-secret-rotation"
printf '%s' "$proxy_two_url" |
  run_hideout secret rotate "$secret_ref" --stdin --yes \
    >"$work_root/secret-rotate.out" 2>"$work_root/secret-rotate.err"
rotation_operation="$(
  awk '/^Operation / {print $2; exit}' "$work_root/secret-rotate.out"
)"
case "$rotation_operation" in
  op_*) ;;
  *) fail "rotation operation identity is missing" ;;
esac
operation_path="$store_root/operations/$rotation_operation.json"
[ -f "$operation_path" ] ||
  fail "rotation operation record is missing"
cp "$operation_path" "$work_root/rotation-operation.json"
jq -e --arg ref "$secret_ref" --arg environment "$environment_id" '
  ([.effects[] | select(.provider == "manager.network")]) as $network |
  ("network." + $environment + ".") as $prefix |
  [
    "network-candidate-staged",
    "network-candidate-probed",
    "network-route-activated",
    "network-route-proved",
    "network-existing-connections-draining"
  ] as $codes |
  .kind == "secret.rotate" and
  .phase == "succeeded" and
  .owner.kind == "secret" and
  .owner.id == $ref and
  (.effects | length) == 6 and
  all(.effects[]; .phase == "succeeded" and (.evidence | length) > 0) and
  ($network | length) == 5 and
  ($network | map(.id)) == [
    ($prefix + "stage"),
    ($prefix + "probe"),
    ($prefix + "activate"),
    ($prefix + "prove"),
    ($prefix + "drain")
  ] and
  ($network | map(.evidence[0].code)) == $codes and
  all($network[0:2][];
    (.evidence | length) == 1 and
    (.evidence[0].value |
      contains("environment:" + $environment + ":") and
      contains(":route-generation:2:secret-generation:2:") and
      endswith(":connections-retained:0"))) and
  all($network[2:5][];
    (.evidence | length) == 1 and
    (.evidence[0].value |
      contains("environment:" + $environment + ":") and
      contains(":route-generation:2:secret-generation:2:") and
      endswith(":connections-retained:1"))) and
  (.effects[5].id == "secret-write") and
  (.effects[5].provider == "macos-keychain") and
  (.effects[5].evidence | length) == 1 and
  (.effects[5].evidence[0].code == "secret-generation-committed") and
  (.effects[5].evidence[0].value |
    contains("secret:" + $ref + "@generation:2:available"))
' "$work_root/rotation-operation.json" >/dev/null ||
  fail "rotation operation did not prove the exact stage/probe/activate/prove/drain and secret commit sequence"
run_hideout secret status "$secret_ref" >"$work_root/secret-status.out"
secret_status_has_version "$work_root/secret-status.out" 2 ||
  fail "managed secret version did not advance"

run_hideout daemon status >"$work_root/daemon-after.json"
run_hideout env list >"$work_root/env-after.txt"
cp "$environment_record" "$work_root/environment-after.json"
LIMA_HOME="$lima_home" limactl list --json |
  jq -c --arg instance "$instance_name" \
    'select(.name == $instance)' >"$work_root/lima-after.json"
jq -e --slurpfile before "$work_root/daemon-before.json" '
  .instanceId == $before[0].instanceId and
  .startedAt == $before[0].startedAt and
  .buildId == $before[0].buildId
' "$work_root/daemon-after.json" >/dev/null ||
  fail "daemon identity changed during online rotation"
jq -e --slurpfile before "$work_root/environment-before.json" '
  .id == $before[0].id and
  .instanceName == $before[0].instanceName and
  .machineIdentityId == $before[0].machineIdentityId and
  .bootConfigurationId == $before[0].bootConfigurationId
' "$work_root/environment-after.json" >/dev/null ||
  fail "environment identity changed during online rotation"
jq -e --slurpfile before "$work_root/lima-before.json" '
  .name == $before[0].name and
  .status == "Running" and
  .arch == $before[0].arch
' "$work_root/lima-after.json" >/dev/null ||
  fail "Lima instance changed during online rotation"

proxy_one_after_rotation="$(proxy_connect_count "$work_root/proxy-one.log")"
proxy_two_after_rotation="$(proxy_connect_count "$work_root/proxy-two.log")"
[ "$proxy_one_after_rotation" -eq "$proxy_one_before" ] ||
  fail "old proxy received a new connection during candidate activation"
[ "$proxy_two_after_rotation" -gt "$proxy_two_before" ] ||
  fail "new proxy did not receive the staged-route probe"

current_stage="post-rotation-new-connection"
: >"$workspace/rotate-go"
wait_for_file "$workspace/after-done" 120 "post-rotation request"
proxy_one_final="$(proxy_connect_count "$work_root/proxy-one.log")"
proxy_two_final="$(proxy_connect_count "$work_root/proxy-two.log")"
[ "$proxy_one_final" -eq "$proxy_one_after_rotation" ] ||
  fail "post-rotation connection still used the old proxy"
[ "$proxy_two_final" -gt "$proxy_two_after_rotation" ] ||
  fail "post-rotation workload connection did not use the new proxy"

cmp -s "$workspace/target-pid-before" "$workspace/target-pid-after" ||
  fail "target process was replaced during online rotation"
cmp -s "$workspace/boot-before" "$workspace/boot-after" ||
  fail "VM boot identity changed during online rotation"
cmp -s "$workspace/session-before" "$workspace/session-after" ||
  fail "session identity changed during online rotation"
[ -s "$workspace/session-before" ] ||
  fail "target session identity is empty"
renewal_boundary_before="$(
  sed -n '1p' "$workspace/renewal-boundary-before"
)"
renewal_boundary_after="$(
  sed -n '1p' "$workspace/renewal-boundary-after"
)"
case "$renewal_boundary_before:$renewal_boundary_after" in
  *[!0-9:]* | :* | *:) fail "session renewal boundary timestamps are invalid" ;;
esac
renewal_boundary_seconds=$((renewal_boundary_after - renewal_boundary_before))
[ "$renewal_boundary_seconds" -ge 35 ] ||
  fail "active workload did not cross the session renewal boundary"
workspace_stress_writes="$(
  find "$workspace" -maxdepth 1 -type f -name 'workspace-stress-*' |
    wc -l |
    tr -d '[:space:]'
)"
[ "$workspace_stress_writes" -eq 64 ] ||
  fail "post-renewal workspace create stress was incomplete"

: >"$workspace/release"
if wait "$run_pid"; then
  run_pid=""
else
  run_status=$?
  run_pid=""
  capture_failed_activity
  fail "active workload failed after online rotation with exit $run_status"
fi
grep -q 'phase=before' "$work_root/http.log" ||
  fail "HTTP fixture did not observe the pre-rotation request"
grep -q 'phase=after' "$work_root/http.log" ||
  fail "HTTP fixture did not observe the post-rotation request"
held_before_connection="$(
  awk '
    $1 == "fixture_request" &&
    $3 == "path=/fixture.txt?phase=held-before" {
      sub(/^conn=/, "", $2)
      print $2
      exit
    }
  ' "$work_root/http.log"
)"
held_after_connection="$(
  awk '
    $1 == "fixture_request" &&
    $3 == "path=/fixture.txt?phase=held-after" {
      sub(/^conn=/, "", $2)
      print $2
      exit
    }
  ' "$work_root/http.log"
)"
[ -n "$held_before_connection" ] &&
  [ "$held_before_connection" = "$held_after_connection" ] ||
  fail "accepted pre-rotation connection did not survive on its prior route"

current_stage="network-crash-matrix-setup"
run_hideout daemon stop \
  >"$evidence_root/logs/crash-matrix-original-daemon-stop.out" \
  2>"$evidence_root/logs/crash-matrix-original-daemon-stop.err"
chmod 0600 \
  "$evidence_root/logs/crash-matrix-original-daemon-stop.out" \
  "$evidence_root/logs/crash-matrix-original-daemon-stop.err"

matrix_rows="$work_root/network-crash-matrix.jsonl"
: >"$matrix_rows"
chmod 0600 "$matrix_rows"
matrix_sequence=0
for crash_effect in \
  network-stage \
  network-probe \
  network-activate \
  network-prove \
  network-drain; do
  matrix_sequence=$((matrix_sequence + 1))
  effect_label="${crash_effect#network-}"
  current_stage="network-crash-$effect_label"
  case "$crash_effect" in
    network-stage)
      effect_index=0
      evidence_code="network-candidate-staged"
      pre_operation_phase="proving"
      expected_terminal_phase="rolled-back"
      expected_terminal_code="network-route-restored"
      ;;
    network-probe)
      effect_index=1
      evidence_code="network-candidate-probed"
      pre_operation_phase="proving"
      expected_terminal_phase="rolled-back"
      expected_terminal_code="network-route-restored"
      ;;
    network-activate)
      effect_index=2
      evidence_code="network-route-activated"
      pre_operation_phase="proving"
      expected_terminal_phase="succeeded"
      expected_terminal_code="profile-committed"
      ;;
    network-prove)
      effect_index=3
      evidence_code="network-route-proved"
      pre_operation_phase="proving"
      expected_terminal_phase="succeeded"
      expected_terminal_code="profile-committed"
      ;;
    network-drain)
      effect_index=4
      evidence_code="network-existing-connections-draining"
      pre_operation_phase="proving"
      expected_terminal_phase="succeeded"
      expected_terminal_code="profile-committed"
      ;;
    *)
      fail "unrecognized crash effect"
      ;;
  esac

  ready_path="$evidence_root/logs/crash-$effect_label-ready.json"
  marker_path="$evidence_root/logs/crash-$effect_label-marker.json"
  crash_daemon_log="$evidence_root/logs/crash-$effect_label-daemon.log"
  normal_daemon_log="$evidence_root/logs/crash-$effect_label-recovery-daemon.log"
  start_network_crash_daemon \
    "$crash_effect" \
    "$ready_path" \
    "$marker_path" \
    "$crash_daemon_log"
  chmod 0600 "$ready_path" "$crash_daemon_log"

  daemon_before_path="$evidence_root/logs/crash-$effect_label-daemon-before.json"
  run_hideout daemon status >"$daemon_before_path"
  chmod 0600 "$daemon_before_path"
  current_resolver="$(
    jq -er '.network.mediatedResolver' \
      "$store_root/profiles/default/profile.json"
  )"
  case "$current_resolver" in
    1.1.1.1) desired_resolver="9.9.9.9" ;;
    9.9.9.9) desired_resolver="1.1.1.1" ;;
    *) fail "profile has an unexpected mediated resolver before crash matrix" ;;
  esac

  matrix_ready="$workspace/crash-$effect_label-session-ready"
  matrix_guest_ready="/workspace/crash-$effect_label-session-ready"
  rm -f "$matrix_ready"
  # The single-quoted target program is intentionally passed verbatim to the VM.
  # shellcheck disable=SC2016
  run_hideout run \
    --verbose --env "$environment_name" --profile default \
    --backend lima --workspace "$workspace" \
    --network tun2socks --proxy-secret "$secret_ref" \
    --mediated-resolver "$current_resolver" \
    -- sh -eu -c '
ready=$1
phase=$2
curl -fsS --max-time 20 \
  "http://1.1.1.1:443/fixture.txt?phase=crash-${phase}-before" \
  >/dev/null
: >"$ready"
while :; do sleep 1; done
' crash-boundary "$matrix_guest_ready" "$effect_label" \
    >"$evidence_root/logs/crash-$effect_label-session.out" \
    2>"$evidence_root/logs/crash-$effect_label-session.err" &
  run_pid=$!
  wait_for_file "$matrix_ready" 600 \
    "$crash_effect active-session readiness"

  baseline_snapshot="$evidence_root/logs/crash-$effect_label-baseline.json"
  network_guest_snapshot "$baseline_snapshot"
  jq -e --arg resolver "$current_resolver" '
    .resolver == $resolver
  ' "$baseline_snapshot" >/dev/null ||
    fail "$crash_effect baseline resolver does not match the profile"

  config_out="$evidence_root/logs/crash-$effect_label-config.out"
  config_err="$evidence_root/logs/crash-$effect_label-config.err"
  run_hideout profile network default tun2socks \
    --proxy-secret "$secret_ref" \
    --mediated-resolver "$desired_resolver" \
    >"$config_out" 2>"$config_err" &
  matrix_config_pid=$!

  wait_for_gate_file \
    "$marker_path" "$crash_daemon_pid" 180 \
    "$crash_effect boundary crash"
  chmod 0600 "$marker_path" "$config_out" "$config_err"
  wait_for_process_exit \
    "$crash_daemon_pid" 30 "$crash_effect daemon"
  crash_daemon_status=0
  set +e
  wait "$crash_daemon_pid"
  crash_daemon_status=$?
  set -e
  crash_daemon_pid=""
  [ "$crash_daemon_status" -eq 86 ] ||
    fail "$crash_effect daemon exited with $crash_daemon_status instead of 86"

  wait_for_process_exit \
    "$matrix_config_pid" 30 "$crash_effect configuration client"
  config_status=0
  set +e
  wait "$matrix_config_pid"
  config_status=$?
  set -e
  matrix_config_pid=""
  [ "$config_status" -ne 0 ] ||
    fail "$crash_effect configuration client falsely received success"

  wait_for_process_exit "$run_pid" 60 "$crash_effect session client"
  session_status=0
  set +e
  wait "$run_pid"
  session_status=$?
  set -e
  run_pid=""
  [ "$session_status" -ne 0 ] ||
    fail "$crash_effect session client falsely survived daemon process death"

  jq -e \
    --arg effect "$crash_effect" \
    --arg environment "$environment_id" \
    --arg code "$evidence_code" '
      .schema == "hideout.network-crash-boundary/v1" and
      .environmentId == $environment and
      .kind == "dns" and
      .effect == $effect and
      .status == "succeeded" and
      (.planDigest | test("^sha256:[a-f0-9]{64}$")) and
      (.evidence | length) == 1 and
      .evidence[0].code == $code
    ' "$marker_path" >/dev/null ||
    fail "$crash_effect marker lacks exact production boundary evidence"

  marker_plan_digest="$(jq -er '.planDigest' "$marker_path")"
  plan_matches="$work_root/crash-$effect_label-plan-matches"
  : >"$plan_matches"
  for plan_candidate in \
    "$store_root"/operations/configuration-plans/*.json; do
    [ -f "$plan_candidate" ] || continue
    if jq -e --arg digest "$marker_plan_digest" '
      any(.networkTransitions[]?; .planDigest == $digest)
    ' "$plan_candidate" >/dev/null; then
      printf '%s\n' "$plan_candidate" >>"$plan_matches"
    fi
  done
  [ "$(wc -l <"$plan_matches" | tr -d '[:space:]')" -eq 1 ] ||
    fail "$crash_effect did not bind exactly one private reviewed plan"
  plan_path="$(sed -n '1p' "$plan_matches")"
  operation_id="$(jq -er '.plan.operationId' "$plan_path")"
  case "$operation_id" in
    op_*) ;;
    *) fail "$crash_effect plan has no canonical operation identity" ;;
  esac
  operation_path="$store_root/operations/$operation_id.json"
  [ -f "$operation_path" ] ||
    fail "$crash_effect durable operation is missing before recovery"
  plan_evidence="$evidence_root/logs/crash-$effect_label-plan.json"
  operation_before="$evidence_root/logs/crash-$effect_label-operation-before.json"
  cp "$plan_path" "$plan_evidence"
  cp "$operation_path" "$operation_before"
  chmod 0600 "$plan_evidence" "$operation_before"

  operation_effect_id="network.$environment_id.$effect_label"
  jq -e \
    --arg phase "$pre_operation_phase" \
    --arg target "$operation_effect_id" \
    --arg environment "$environment_id" \
    --argjson index "$effect_index" \
    --slurpfile marker "$marker_path" '
      ([.effects[] |
        select(.id | startswith("network." + $environment + "."))]) as $network |
      [
        ("network." + $environment + ".stage"),
        ("network." + $environment + ".probe"),
        ("network." + $environment + ".activate"),
        ("network." + $environment + ".prove"),
        ("network." + $environment + ".drain")
      ] as $order |
      .phase == $phase and
      ($network | length) == 5 and
      (($network | map(.id) | sort) == ($order | sort)) and
      any($network[];
        .id == $target and
        .phase == "succeeded" and
        .evidence == $marker[0].evidence) and
      all($order[0:($index + 1)][];
        . as $id |
        any($network[];
          .id == $id and .phase == "succeeded")) and
      all($order[($index + 1):][];
        . as $id |
        any($network[];
          .id == $id and .phase == "pending"))
    ' "$operation_before" >/dev/null ||
    fail "$crash_effect operation envelope was not durably checkpointed exactly"

  crash_snapshot="$evidence_root/logs/crash-$effect_label-after-crash.json"
  network_guest_snapshot "$crash_snapshot"
  if [ "$effect_index" -lt 2 ]; then
    expected_effective_resolver="$current_resolver"
  else
    expected_effective_resolver="$desired_resolver"
  fi
  jq -e --arg resolver "$expected_effective_resolver" '
    .resolver == $resolver
  ' "$crash_snapshot" >/dev/null ||
    fail "$crash_effect guest resolver does not match the exact crash boundary"
  jq -e --slurpfile baseline "$baseline_snapshot" '
    .bootId == $baseline[0].bootId and
    (if .resolver == $baseline[0].resolver
     then .dnsPid == $baseline[0].dnsPid
     else .dnsPid != $baseline[0].dnsPid
     end)
  ' "$crash_snapshot" >/dev/null ||
    fail "$crash_effect guest mutation or boot evidence is inconsistent"

  start_normal_recovery_daemon "$normal_daemon_log"
  chmod 0600 \
    "$normal_daemon_log" \
    "$normal_daemon_log.status" \
    "$normal_daemon_log.status.err"
  daemon_after_path="$evidence_root/logs/crash-$effect_label-daemon-after.json"
  cp "$normal_daemon_log.status" "$daemon_after_path"
  operation_after="$evidence_root/logs/crash-$effect_label-operation-after.json"
  [ -f "$operation_path" ] ||
    fail "$crash_effect operation disappeared during reconciliation"
  cp "$operation_path" "$operation_after"
  profile_after="$evidence_root/logs/crash-$effect_label-profile-after.json"
  cp "$store_root/profiles/default/profile.json" "$profile_after"
  recovery_snapshot="$evidence_root/logs/crash-$effect_label-after-recovery.json"
  network_guest_snapshot "$recovery_snapshot"
  chmod 0600 "$daemon_after_path" "$operation_after" "$profile_after"

  jq -e \
    --arg phase "$expected_terminal_phase" \
    --arg code "$expected_terminal_code" '
      .phase == $phase and
      .result.status == $phase and
      .result.code == $code and
      .recovery.code == "operation-terminal"
    ' "$operation_after" >/dev/null ||
    fail "$crash_effect reconciliation did not reach the exact terminal result"
  if [ "$expected_terminal_phase" = "succeeded" ]; then
    jq -e \
      --arg environment "$environment_id" '
        [.effects[] |
          select(.id | startswith("network." + $environment + "."))] as $network |
        ($network | length) == 5 and
        all($network[];
          .phase == "succeeded" and
          (.evidence | length) == 1)
      ' "$operation_after" >/dev/null ||
      fail "$crash_effect committed recovery lacks all exact effect evidence"
  else
    jq -e \
      --arg environment "$environment_id" \
      --argjson index "$effect_index" '
        [.effects[] |
          select(.id | startswith("network." + $environment + "."))] as $network |
        [
          ("network." + $environment + ".stage"),
          ("network." + $environment + ".probe"),
          ("network." + $environment + ".activate"),
          ("network." + $environment + ".prove"),
          ("network." + $environment + ".drain")
        ] as $order |
        all($order[0:($index + 1)][];
          . as $id |
          any($network[];
            .id == $id and
            .phase == "rolled-back" and
            (.evidence | length) == 1 and
            .evidence[0].code == "network-route-restored")) and
        all($order[($index + 1):][];
          . as $id |
          any($network[];
            .id == $id and .phase == "pending"))
      ' "$operation_after" >/dev/null ||
      fail "$crash_effect prior-route recovery lacks exact rollback evidence"
  fi
  jq -e --arg resolver "$expected_effective_resolver" '
    .network.mediatedResolver == $resolver
  ' "$profile_after" >/dev/null ||
    fail "$crash_effect recovered profile disagrees with effective DNS"
  cmp -s "$crash_snapshot" "$recovery_snapshot" ||
    fail "$crash_effect recovery replayed DNS or changed the VM boot"
  jq -e --slurpfile before "$daemon_before_path" '
    .instanceId != $before[0].instanceId and
    .buildId == $before[0].buildId
  ' "$daemon_after_path" >/dev/null ||
    fail "$crash_effect did not restart under a new daemon identity"

  failed_closed_out="$evidence_root/logs/crash-$effect_label-stale-owner.out"
  failed_closed_err="$evidence_root/logs/crash-$effect_label-stale-owner.err"
  failed_closed_status=0
  set +e
  run_hideout run \
    --env "$environment_name" --profile default \
    --backend lima --workspace "$workspace" \
    --network tun2socks --proxy-secret "$secret_ref" \
    --mediated-resolver "$expected_effective_resolver" \
    -- true >"$failed_closed_out" 2>"$failed_closed_err"
  failed_closed_status=$?
  set -e
  if [ "$failed_closed_status" -eq 0 ] ||
    ! grep -Eq 'session[.]cleanup[.]failed|explicit recovery' \
      "$failed_closed_err"; then
    fail "$crash_effect replacement attach did not fail closed on the stale owner"
  fi

  settled_path="$evidence_root/logs/crash-$effect_label-lifecycle-settled.json"
  wait_for_lifecycle_settled "$environment_id" "$settled_path"
  explicit_stop_out="$evidence_root/logs/crash-$effect_label-explicit-stop.out"
  explicit_stop_err="$evidence_root/logs/crash-$effect_label-explicit-stop.err"
  run_hideout stop "$environment_id" \
    >"$explicit_stop_out" 2>"$explicit_stop_err"
  owner_root="$store_root/environments/$environment_id/owners"
  if [ -d "$owner_root" ] &&
    find "$owner_root" -type f -print -quit | grep -q .; then
    fail "$crash_effect explicit lifecycle recovery retained a stale owner"
  fi
  LIMA_HOME="$lima_home" limactl list --json |
    jq -s -e --arg instance "$instance_name" '
      any(.[]; .name == $instance and .status == "Stopped")
    ' >/dev/null ||
    fail "$crash_effect explicit lifecycle recovery did not stop the exact VM"

  proxy_before_recovery_probe="$(
    proxy_connect_count "$work_root/proxy-two.log"
  )"
  probe_path="/fixture.txt?phase=crash-$effect_label-recovered"
  # The single-quoted target program is intentionally passed verbatim to the VM.
  # shellcheck disable=SC2016
  run_hideout run \
    --env "$environment_name" --profile default \
    --backend lima --workspace "$workspace" \
    --network tun2socks --proxy-secret "$secret_ref" \
    --mediated-resolver "$expected_effective_resolver" \
    -- sh -eu -c '
url=$1
curl -fsS --max-time 20 "$url" >/dev/null
' recovery-route \
    "http://1.1.1.1:443$probe_path" \
    >"$evidence_root/logs/crash-$effect_label-route-probe.out" \
    2>"$evidence_root/logs/crash-$effect_label-route-probe.err"
  proxy_after_recovery_probe="$(
    proxy_connect_count "$work_root/proxy-two.log"
  )"
  [ "$proxy_after_recovery_probe" -gt "$proxy_before_recovery_probe" ] ||
    fail "$crash_effect independent recovered route did not use the effective proxy"
  grep -F "path=$probe_path" "$work_root/http.log" >/dev/null ||
    fail "$crash_effect independent recovered request did not reach the fixture"
  post_probe_snapshot="$evidence_root/logs/crash-$effect_label-after-probe.json"
  network_guest_snapshot "$post_probe_snapshot"
  jq -e \
    --arg resolver "$expected_effective_resolver" \
    --slurpfile recovered "$recovery_snapshot" '
      .resolver == $resolver and
      .bootId != $recovered[0].bootId
    ' "$post_probe_snapshot" >/dev/null ||
    fail "$crash_effect explicit lifecycle recovery did not establish a fresh proved boot"

  post_probe_owner_root="$store_root/environments/$environment_id/owners"
  post_probe_owner_attempt=0
  while [ "$post_probe_owner_attempt" -lt 600 ]; do
    if [ ! -d "$post_probe_owner_root" ] ||
      ! find "$post_probe_owner_root" -type f -print -quit |
        grep -q .; then
      break
    fi
    post_probe_owner_attempt=$((post_probe_owner_attempt + 1))
    sleep 0.05
  done
  [ "$post_probe_owner_attempt" -lt 600 ] ||
    fail "$crash_effect post-recovery route owner did not reconcile"
  run_hideout stop "$environment_id" \
    >"$evidence_root/logs/crash-$effect_label-post-probe-stop.out" \
    2>"$evidence_root/logs/crash-$effect_label-post-probe-stop.err" ||
    fail "$crash_effect post-recovery VM stop was not proved"
  LIMA_HOME="$lima_home" limactl list --json |
    jq -s -e --arg instance "$instance_name" '
      any(.[]; .name == $instance and .status == "Stopped")
    ' >/dev/null ||
    fail "$crash_effect post-recovery VM remained running"

  jq -cn \
    --arg effect "$crash_effect" \
    --arg operationId "$operation_id" \
    --arg from "$current_resolver" \
    --arg desired "$desired_resolver" \
    --arg effective "$expected_effective_resolver" \
    --arg terminalPhase "$expected_terminal_phase" \
    --arg terminalCode "$expected_terminal_code" \
    --argjson index "$effect_index" \
    --argjson crashExitCode "$crash_daemon_status" \
    --argjson configExitCode "$config_status" \
    --argjson sessionExitCode "$session_status" \
    --argjson failedClosedExitCode "$failed_closed_status" \
    --argjson proxyBefore "$proxy_before_recovery_probe" \
    --argjson proxyAfter "$proxy_after_recovery_probe" \
    '{
      effect:$effect,
      effectIndex:$index,
      operationId:$operationId,
      fromResolver:$from,
      desiredResolver:$desired,
      effectiveResolver:$effective,
      terminalPhase:$terminalPhase,
      terminalCode:$terminalCode,
      crashExitCode:$crashExitCode,
      lostClients:{
        configuration:$configExitCode,
        session:$sessionExitCode
      },
      independentRouteProbe:{
        proxyConnectionsBefore:$proxyBefore,
        proxyConnectionsAfter:$proxyAfter,
        passed:($proxyAfter > $proxyBefore)
      },
      staleOwnerFailedClosed:($failedClosedExitCode != 0),
      explicitLifecycleRecovery:true,
      freshBootAfterLifecycleRecovery:true,
      exactBoundary:true,
      noMutationReplay:true,
      daemonIdentityChanged:true,
      vmBootPreservedThroughNetworkReconciliation:true
    }' >>"$matrix_rows"

  stop_normal_recovery_daemon
done

matrix_summary="$evidence_root/logs/network-crash-matrix.json"
jq -s '
  {
    schema:"hideout.network-crash-matrix/v1",
    result:
      (if length == 5 and
          (map(.effect) == [
            "network-stage",
            "network-probe",
            "network-activate",
            "network-prove",
            "network-drain"
          ]) and
          all(.[];
            .crashExitCode == 86 and
            .lostClients.configuration != 0 and
            .lostClients.session != 0 and
            .independentRouteProbe.passed == true and
            .staleOwnerFailedClosed == true and
            .explicitLifecycleRecovery == true and
            .freshBootAfterLifecycleRecovery == true and
            .exactBoundary == true and
            .noMutationReplay == true and
            .daemonIdentityChanged == true and
            .vmBootPreservedThroughNetworkReconciliation == true)
       then "passed"
       else "failed"
       end),
    boundaries:.
  }
' "$matrix_rows" >"$matrix_summary"
chmod 0600 "$matrix_summary"
jq -e '
  .result == "passed" and
  (.boundaries | length) == 5 and
  [.boundaries[].terminalPhase] ==
    ["rolled-back","rolled-back","succeeded","succeeded","succeeded"]
' "$matrix_summary" >/dev/null ||
  fail "network crash boundary matrix is incomplete"

current_stage="logical-secret-cleanup"
LIMA_HOME="$lima_home" limactl list --json \
  >"$evidence_root/logs/final-lima-stopped.json"
jq -s -e --arg instance "$instance_name" '
  any(.[]; .name == $instance and .status == "Stopped")
' "$evidence_root/logs/final-lima-stopped.json" >/dev/null ||
  fail "final environment was not stopped before logical cleanup"
start_normal_recovery_daemon \
  "$evidence_root/logs/final-cleanup-daemon.log"
run_hideout profile network default direct \
  >"$evidence_root/logs/profile-direct.out" \
  2>"$evidence_root/logs/profile-direct.err" || {
  cat "$evidence_root/logs/profile-direct.err" >&2
  fail "profile direct cleanup failed"
}
run_hideout secret delete "$secret_ref" --yes \
  >"$evidence_root/logs/secret-delete.out" \
  2>"$evidence_root/logs/secret-delete.err" || {
  cat "$evidence_root/logs/secret-delete.err" >&2
  fail "managed secret logical deletion failed"
}
secret_set=0
purge_isolated_keychain_item ||
  fail "isolated Keychain item remained after logical deletion"
stop_normal_recovery_daemon

current_stage="privacy-and-evidence"
scan_process_value_absent "proxy one credential material" "$proxy_one_patterns"
scan_process_value_absent "proxy two credential material" "$proxy_two_patterns"
scan_file_sequence=0
for scan_target in \
  "$store_root" \
  "$work_root/run.out" \
  "$work_root/run.err" \
  "$work_root/secret-set.out" \
  "$work_root/secret-set.err" \
  "$work_root/secret-rotate.out" \
  "$work_root/secret-rotate.err" \
  "$work_root/rotation-operation.json"; do
  scan_pattern_absent \
    "proxy-one credential material" "$proxy_one_patterns" "$scan_target"
  scan_pattern_absent \
    "proxy-two credential material" "$proxy_two_patterns" "$scan_target"
done

for artifact_name in \
  run.out \
  run.err \
  secret-set.out \
  secret-set.err \
  secret-rotate.out \
  secret-rotate.err \
  secret-status.out \
  daemon-before.json \
  daemon-after.json \
  environment-before.json \
  environment-after.json \
  lima-before.json \
  lima-after.json \
  rotation-operation.json; do
  cp "$work_root/$artifact_name" "$evidence_root/logs/$artifact_name"
  chmod 0600 "$evidence_root/logs/$artifact_name"
done
cp "$work_root/proxy-one.log" "$evidence_root/logs/proxy-one.log"
cp "$work_root/proxy-two.log" "$evidence_root/logs/proxy-two.log"
cp "$work_root/http.log" "$evidence_root/logs/http.log"
chmod 0600 \
  "$evidence_root/logs/proxy-one.log" \
  "$evidence_root/logs/proxy-two.log" \
  "$evidence_root/logs/http.log" \
  "$evidence_root/logs/network-refinement.log"

artifacts_path="$work_root/artifacts.json"
find "$evidence_root/logs" -maxdepth 1 -type f -print |
  sort |
  while IFS= read -r artifact_path; do
    jq -cn \
      --arg path "logs/$(basename "$artifact_path")" \
      --arg sha256 "$(gate_sha256_file "$artifact_path")" \
      --argjson bytes "$(wc -c <"$artifact_path" | tr -d '[:space:]')" \
      '{path:$path, sha256:$sha256, bytes:$bytes, mode:"0600"}'
  done | jq -s '.' >"$artifacts_path"

jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" \
  --arg environmentID "$environment_id" \
  --arg instanceName "$instance_name" \
  --arg operationID "$rotation_operation" \
  --arg daemonInstanceID "$(jq -r '.instanceId' "$work_root/daemon-after.json")" \
  --arg bootID "$(sed -n '1p' "$workspace/boot-after")" \
  --arg sessionID "$(sed -n '1p' "$workspace/session-after")" \
  --argjson proxyOneBefore "$proxy_one_before" \
  --argjson proxyOneFinal "$proxy_one_final" \
  --argjson proxyTwoBefore "$proxy_two_before" \
  --argjson proxyTwoAfterRotation "$proxy_two_after_rotation" \
  --argjson proxyTwoFinal "$proxy_two_final" \
  --argjson renewalBoundarySeconds "$renewal_boundary_seconds" \
  --argjson workspaceStressWrites "$workspace_stress_writes" \
  --arg heldHTTPConnection "$held_before_connection" \
  --slurpfile crashMatrix "$matrix_summary" \
  --slurpfile artifacts "$artifacts_path" '
  {
    schema: "hideout.network-rotation-lima-evidence/v1",
    generatedAt: $generatedAt,
    source: {commit:$commit, dirty:$dirty},
    result: "passed",
    candidateAcceptance: ($dirty | not),
    identities: {
      environmentId:$environmentID,
      instanceName:$instanceName,
      daemonInstanceId:$daemonInstanceID,
      bootId:$bootID,
      sessionId:$sessionID,
      operationId:$operationID
    },
    checks: {
      realLima:true,
      managedSecretGenerationAdvanced:true,
      canonicalOperationSucceeded:true,
      fullNetworkTransitionSequenceProved:true,
      networkCrashBoundaryMatrix:true,
      liveRouteEffectProved:true,
      existingConnectionRetainsPriorRoute:true,
      newConnectionsUseRotatedProxy:true,
      oldProxyReceivesNoNewConnections:true,
      daemonNotRestarted:true,
      vmNotRecreated:true,
      targetNotReplaced:true,
      sessionRenewalBoundaryCrossed:true,
      workspacePostRenewalStress:true,
      observerTransportTamperRejected:true,
      cgroupEscapeRejected:true,
      proxyEnvironmentHidden:true,
      secretAbsentFromProcessAndEvidenceSurfaces:true
    },
    connectionCounts: {
      proxyOneBefore:$proxyOneBefore,
      proxyOneFinal:$proxyOneFinal,
      proxyTwoBefore:$proxyTwoBefore,
      proxyTwoAfterRotation:$proxyTwoAfterRotation,
      proxyTwoFinal:$proxyTwoFinal
    },
    workspaceContinuity: {
      renewalBoundarySeconds:$renewalBoundarySeconds,
      stressWrites:$workspaceStressWrites
    },
    routeProof: {
      heldHTTPConnection:$heldHTTPConnection,
      beforePath:"/fixture.txt?phase=held-before",
      afterPath:"/fixture.txt?phase=held-after"
    },
    networkCrashMatrix:$crashMatrix[0],
    artifacts:$artifacts[0],
    limitations:
      ([] +
      if $dirty then
        ["This binds a dirty development checkout; it is not release-candidate provenance."]
      else
        []
      end)
  }' >"$evidence_root/summary.json"
chmod 0600 "$evidence_root/summary.json"

for scan_target in "$evidence_root/summary.json" "$evidence_root/logs"; do
  scan_pattern_absent \
    "proxy-one credential material" "$proxy_one_patterns" "$scan_target"
  scan_pattern_absent \
    "proxy-two credential material" "$proxy_two_patterns" "$scan_target"
done
jq -e '
  .result == "passed" and
  all(.checks[]; . == true) and
  .connectionCounts.proxyOneFinal ==
    .connectionCounts.proxyOneBefore and
  .connectionCounts.proxyTwoAfterRotation >
    .connectionCounts.proxyTwoBefore and
  .connectionCounts.proxyTwoFinal >
    .connectionCounts.proxyTwoAfterRotation and
  .checks.existingConnectionRetainsPriorRoute == true and
  .workspaceContinuity.renewalBoundarySeconds >= 35 and
  .workspaceContinuity.stressWrites == 64 and
  .checks.networkCrashBoundaryMatrix == true and
  .networkCrashMatrix.result == "passed" and
  (.networkCrashMatrix.boundaries | length) == 5 and
  (.networkCrashMatrix.boundaries | map(.effect)) == [
    "network-stage",
    "network-probe",
    "network-activate",
    "network-prove",
    "network-drain"
  ] and
  all(.networkCrashMatrix.boundaries[];
    .crashExitCode == 86 and
    .exactBoundary == true and
    .noMutationReplay == true and
    .daemonIdentityChanged == true and
    .vmBootPreservedThroughNetworkReconciliation == true and
    .staleOwnerFailedClosed == true and
    .explicitLifecycleRecovery == true and
    .freshBootAfterLifecycleRecovery == true and
    .independentRouteProbe.passed == true) and
  (.routeProof.heldHTTPConnection |
    test("^127[.]0[.]0[.]1:[0-9]+$")) and
  .routeProof.beforePath == "/fixture.txt?phase=held-before" and
  .routeProof.afterPath == "/fixture.txt?phase=held-after" and
  (.artifacts | length) >= 10 and
  all(.artifacts[]; .mode == "0600" and
    (.sha256 | test("^[a-f0-9]{64}$")))
' "$evidence_root/summary.json" >/dev/null ||
  fail "network rotation evidence manifest validation failed"

summary_sha="$(gate_sha256_file "$evidence_root/summary.json")"
result_tmp="$(mktemp "$out/.result.XXXXXX")"
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" \
  --arg runId "$run_id" \
  --arg summary "$run_id/summary.json" \
  --arg summarySHA256 "$summary_sha" \
  '{
    schema: "hideout.network-rotation-lima-pointer/v1",
    generatedAt: $generatedAt,
    source: {commit: $commit, dirty: $dirty},
    result: "passed",
    candidateAcceptance: ($dirty | not),
    runId: $runId,
    summary: $summary,
    summarySHA256: $summarySHA256
  }' >"$result_tmp"
chmod 0600 "$result_tmp"
mv "$result_tmp" "$out/result.json"
find "$evidence_root" -type f -exec chmod 0600 {} +

gate_completed=1
printf 'network-rotation-lima: evidence=%s/summary.json\n' "$evidence_root"
printf 'network-rotation-lima: passed\n'
