#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

GATE_TIMEOUT="${HIDEOUT_LIMA_REAL_RUN_TIMEOUT:-${HIDEOUT_GATE_TIMEOUT:-15m}}"
NETWORK_MODE="${HIDEOUT_LIMA_REAL_RUN_NETWORK:-direct}"
REFERENCE_WORKLOAD_LIMIT_SECONDS="${HIDEOUT_LIMA_REAL_RUN_REFERENCE_LIMIT_SECONDS:-600}"

BOUNDARY_ACTION_SET=(
  "host.open deny"
  "HostFS deny"
  "HostFS reserved-root reject"
  "session lifecycle"
  "network setup"
  "preview.open endpoint exposure"
)

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "lima-real-run: missing required command: $1" >&2
    exit 127
  fi
}

with_timeout() {
  local duration="$1"
  shift
  "$@" &
  local pid=$!
  (
    sleep "$duration"
    if kill -0 "$pid" 2>/dev/null; then
      echo "lima-real-run: command timed out after $duration: $*" >&2
      kill "$pid" 2>/dev/null || true
      sleep 5
      kill -KILL "$pid" 2>/dev/null || true
    fi
  ) &
  local timer=$!
  local status=0
  if wait "$pid"; then
    status=0
  else
    status=$?
  fi
  kill "$timer" 2>/dev/null || true
  wait "$timer" 2>/dev/null || true
  return "$status"
}

fail() {
  echo "lima-real-run: $*" >&2
  exit 1
}

marker() {
  echo "lima-real-run: $1"
}

run_env() {
  local -a env_args=(
    "HIDEOUT_STORE_ROOT=$store"
    "LIMA_HOME=$lima_home"
    "HIDEOUT_BROWSER_PATH=$HIDEOUT_BROWSER_PATH"
    "HIDEOUT_LINUX_SHIM_PATH=$HIDEOUT_LINUX_SHIM_PATH"
    "HIDEOUT_LINUX_HOSTFSD_PATH=$HIDEOUT_LINUX_HOSTFSD_PATH"
  )
  if [ -n "${HIDEOUT_LINUX_TUN2SOCKS_PATH:-}" ]; then
    env_args+=("HIDEOUT_LINUX_TUN2SOCKS_PATH=$HIDEOUT_LINUX_TUN2SOCKS_PATH")
  fi
  if [ -n "${HIDEOUT_SECRET_DEFAULT_PROXY:-}" ]; then
    env_args+=("HIDEOUT_SECRET_DEFAULT_PROXY=$HIDEOUT_SECRET_DEFAULT_PROXY")
  fi
  env "${env_args[@]}" "$@"
}

run_env_exec() {
  local -a env_args=(
    "HIDEOUT_STORE_ROOT=$store"
    "LIMA_HOME=$lima_home"
    "HIDEOUT_BROWSER_PATH=$HIDEOUT_BROWSER_PATH"
    "HIDEOUT_LINUX_SHIM_PATH=$HIDEOUT_LINUX_SHIM_PATH"
    "HIDEOUT_LINUX_HOSTFSD_PATH=$HIDEOUT_LINUX_HOSTFSD_PATH"
  )
  if [ -n "${HIDEOUT_LINUX_TUN2SOCKS_PATH:-}" ]; then
    env_args+=("HIDEOUT_LINUX_TUN2SOCKS_PATH=$HIDEOUT_LINUX_TUN2SOCKS_PATH")
  fi
  if [ -n "${HIDEOUT_SECRET_DEFAULT_PROXY:-}" ]; then
    env_args+=("HIDEOUT_SECRET_DEFAULT_PROXY=$HIDEOUT_SECRET_DEFAULT_PROXY")
  fi
  exec env "${env_args[@]}" "$@"
}

run_env_with_roots() {
  local store_root="$1"
  local lima_root="$2"
  shift 2
  local -a env_args=(
    "HIDEOUT_STORE_ROOT=$store_root"
    "LIMA_HOME=$lima_root"
    "HIDEOUT_BROWSER_PATH=$HIDEOUT_BROWSER_PATH"
    "HIDEOUT_LINUX_SHIM_PATH=$HIDEOUT_LINUX_SHIM_PATH"
    "HIDEOUT_LINUX_HOSTFSD_PATH=$HIDEOUT_LINUX_HOSTFSD_PATH"
  )
  if [ -n "${HIDEOUT_LINUX_TUN2SOCKS_PATH:-}" ]; then
    env_args+=("HIDEOUT_LINUX_TUN2SOCKS_PATH=$HIDEOUT_LINUX_TUN2SOCKS_PATH")
  fi
  if [ -n "${HIDEOUT_SECRET_DEFAULT_PROXY:-}" ]; then
    env_args+=("HIDEOUT_SECRET_DEFAULT_PROXY=$HIDEOUT_SECRET_DEFAULT_PROXY")
  fi
  env "${env_args[@]}" "$@"
}

prepare_linux_shim() {
  if [ -n "${HIDEOUT_LINUX_SHIM_PATH:-}" ]; then
    if [ ! -x "$HIDEOUT_LINUX_SHIM_PATH" ]; then
      fail "HIDEOUT_LINUX_SHIM_PATH is not executable: $HIDEOUT_LINUX_SHIM_PATH"
    fi
    return
  fi

  local arch
  arch="$(go env GOARCH)"
  if command -v "hideout-shim-linux-$arch" >/dev/null 2>&1; then
    HIDEOUT_LINUX_SHIM_PATH="$(command -v "hideout-shim-linux-$arch")"
    export HIDEOUT_LINUX_SHIM_PATH
    return
  fi
  if command -v hideout-shim-linux >/dev/null 2>&1; then
    HIDEOUT_LINUX_SHIM_PATH="$(command -v hideout-shim-linux)"
    export HIDEOUT_LINUX_SHIM_PATH
    return
  fi

  HIDEOUT_LINUX_SHIM_PATH="$bin/hideout-shim-linux-$arch"
  export HIDEOUT_LINUX_SHIM_PATH
  "$hideout" shim build-linux --out "$HIDEOUT_LINUX_SHIM_PATH" --goarch "$arch" --source "$ROOT" >/dev/null
}

prepare_linux_hostfsd() {
  if [ -n "${HIDEOUT_LINUX_HOSTFSD_PATH:-}" ]; then
    if [ ! -x "$HIDEOUT_LINUX_HOSTFSD_PATH" ]; then
      fail "HIDEOUT_LINUX_HOSTFSD_PATH is not executable: $HIDEOUT_LINUX_HOSTFSD_PATH"
    fi
    return
  fi

  local arch
  arch="$(go env GOARCH)"
  if command -v "hideout-hostfsd-linux-$arch" >/dev/null 2>&1; then
    HIDEOUT_LINUX_HOSTFSD_PATH="$(command -v "hideout-hostfsd-linux-$arch")"
    export HIDEOUT_LINUX_HOSTFSD_PATH
    return
  fi
  if command -v hideout-hostfsd-linux >/dev/null 2>&1; then
    HIDEOUT_LINUX_HOSTFSD_PATH="$(command -v hideout-hostfsd-linux)"
    export HIDEOUT_LINUX_HOSTFSD_PATH
    return
  fi

  HIDEOUT_LINUX_HOSTFSD_PATH="$bin/hideout-hostfsd-linux-$arch"
  export HIDEOUT_LINUX_HOSTFSD_PATH
  "$hideout" hostfsd build-linux --out "$HIDEOUT_LINUX_HOSTFSD_PATH" --goarch "$arch" --source "$ROOT" >/dev/null
}

prepare_linux_tun2socks() {
  if [ -n "${HIDEOUT_LINUX_TUN2SOCKS_PATH:-}" ]; then
    if [ ! -x "$HIDEOUT_LINUX_TUN2SOCKS_PATH" ]; then
      fail "HIDEOUT_LINUX_TUN2SOCKS_PATH is not executable: $HIDEOUT_LINUX_TUN2SOCKS_PATH"
    fi
    return
  fi

  local arch
  arch="$(go env GOARCH)"
  if command -v "tun2socks-linux-$arch" >/dev/null 2>&1; then
    HIDEOUT_LINUX_TUN2SOCKS_PATH="$(command -v "tun2socks-linux-$arch")"
    export HIDEOUT_LINUX_TUN2SOCKS_PATH
    return
  fi
  if command -v tun2socks-linux >/dev/null 2>&1; then
    HIDEOUT_LINUX_TUN2SOCKS_PATH="$(command -v tun2socks-linux)"
    export HIDEOUT_LINUX_TUN2SOCKS_PATH
    return
  fi

  echo "lima-real-run: building temporary Linux tun2socks for $arch"
  HIDEOUT_LINUX_TUN2SOCKS_PATH="$bin/tun2socks-linux-$arch"
  local build_dir="$tmp/tun2socks-build"
  mkdir -p "$build_dir"
  (
    cd "$build_dir"
    go mod init hideout-real-run-tun2socks >/dev/null
    go get github.com/xjasonlyu/tun2socks/v2@v2.6.0 >/dev/null
    GOOS=linux GOARCH="$arch" CGO_ENABLED=0 \
      go build -o "$HIDEOUT_LINUX_TUN2SOCKS_PATH" github.com/xjasonlyu/tun2socks/v2
  )
  chmod 0700 "$HIDEOUT_LINUX_TUN2SOCKS_PATH"
  export HIDEOUT_LINUX_TUN2SOCKS_PATH
}

prepare_fake_browser() {
  local browser="$bin/hideout-real-run-browser"
  cat >"$browser" <<'SH'
#!/usr/bin/env bash
set -euo pipefail

target=""
for arg in "$@"; do
  case "$arg" in
    http://*|https://*) target="$arg" ;;
  esac
done
if [ -z "$target" ]; then
  echo "hideout-real-run-browser: missing URL argument" >&2
  exit 2
fi
curl -fsSL --max-time "${HIDEOUT_GATE_BROWSER_TIMEOUT:-15}" -o /dev/null "$target"
SH
  chmod +x "$browser"
  HIDEOUT_BROWSER_PATH="$browser"
  export HIDEOUT_BROWSER_PATH
}

build_binaries() {
  hideout="$bin/hideout"
  lab_target="$bin/hideout-gate-lab-target"
  go build -o "$hideout" ./cmd/hideout
  go build -o "$lab_target" ./cmd/hideout-gate-lab-target
  prepare_fake_browser
  prepare_linux_shim
  prepare_linux_hostfsd
  local arch
  arch="$(go env GOARCH)"
  GOOS=linux GOARCH="$arch" CGO_ENABLED=0 \
    go build -trimpath -o "$workspace/hideout-test-cli" ./cmd/hideout-test-cli
}

start_lab_endpoint() {
  "$lab_target" --mode http --path /workload --status 204 --listen 127.0.0.1:0 >"$tmp/endpoint.addr" 2>"$tmp/endpoint.log" &
  endpoint_pid=$!
  for _ in $(seq 1 100); do
    if [ -s "$tmp/endpoint.addr" ]; then
      endpoint_addr="$(sed -n '1p' "$tmp/endpoint.addr")"
      endpoint_port="${endpoint_addr##*:}"
      endpoint_url="http://host.lima.internal:$endpoint_port/workload"
      endpoint_status="204"
      return
    fi
    if ! kill -0 "$endpoint_pid" 2>/dev/null; then
      echo "lima-real-run: lab endpoint exited early" >&2
      cat "$tmp/endpoint.log" >&2 || true
      exit 1
    fi
    sleep 0.1
  done
  fail "lab endpoint did not publish an address"
}

free_host_port() {
  python3 - <<'PY'
import socket

with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
    s.bind(("127.0.0.1", 0))
    print(s.getsockname()[1])
PY
}

configure_network() {
  run_network_args=()
  init_network_args=()
  network_label="$NETWORK_MODE"
  case "$NETWORK_MODE" in
    direct)
      run_network_args=(--network direct)
      init_network_args=(--network direct)
      init_template="dev"
      ;;
    privacy|tun2socks)
      network_label="privacy"
      if [ -z "${HIDEOUT_SECRET_DEFAULT_PROXY:-}" ]; then
        fail "HIDEOUT_LIMA_REAL_RUN_NETWORK=privacy requires HIDEOUT_SECRET_DEFAULT_PROXY"
      fi
      prepare_linux_tun2socks
      run_network_args=(--network tun2socks --proxy-secret default-proxy)
      init_network_args=(--network tun2socks --proxy-secret default-proxy --mediated-resolver 1.1.1.1)
      init_template="privacy"
      endpoint_url="${HIDEOUT_LIMA_REAL_RUN_ENDPOINT_URL:-https://example.com/}"
      endpoint_status="${HIDEOUT_LIMA_REAL_RUN_EXPECT_STATUS:-200}"
      ;;
    *)
      fail "HIDEOUT_LIMA_REAL_RUN_NETWORK must be direct or privacy"
      ;;
  esac
}

extract_audit_path() {
  awk '/audit: / { print $2; exit }' "$1"
}

extract_session_id() {
  local audit_path="$1"
  basename "$(dirname "$audit_path")"
}

extract_environment_id() {
  awk -F': ' '/^Hideout environment:/ { print $2; exit }' "$1"
}

extract_environment_name() {
  awk -F': ' '/^Hideout environment name:/ { print $2; exit }' "$1"
}

fetch_audit_json() {
  local session_id="$1"
  local out="$2"
  run_env "$hideout" audit show --session "$session_id" --limit 200 --json >"$out"
  python3 - "$out" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as f:
    data = json.load(f)
if not isinstance(data, list):
    raise SystemExit("audit json is not a list")
PY
}

assert_no_forbidden_strings() {
  local file
  for file in "$@"; do
    [ -f "$file" ] || continue
    if [ -n "${HIDEOUT_SECRET_DEFAULT_PROXY:-}" ] && grep -F -- "$HIDEOUT_SECRET_DEFAULT_PROXY" "$file" >/dev/null 2>&1; then
      fail "proxy secret value leaked into $file"
    fi
    if grep -E 'HIDEOUT_CAPABILITY_TOKEN=|HIDEOUT_BROKER_ENDPOINT=|proxy\.url' "$file" >/dev/null 2>&1; then
      fail "control-plane secret marker leaked into $file"
    fi
  done
}

assert_contains() {
  local needle="$1"
  local file="$2"
  if ! grep -Fq -- "$needle" "$file"; then
    echo "lima-real-run: expected $file to contain: $needle" >&2
    echo "lima-real-run: $file contents:" >&2
    cat "$file" >&2 || true
    exit 1
  fi
}

assert_not_contains() {
  local needle="$1"
  local file="$2"
  if grep -Fq -- "$needle" "$file"; then
    echo "lima-real-run: expected $file not to contain: $needle" >&2
    cat "$file" >&2 || true
    exit 1
  fi
}

wait_for_file_contains() {
  local file="$1"
  local needle="$2"
  local timeout_seconds="$3"
  local deadline=$((SECONDS + timeout_seconds))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if [ -f "$file" ] && grep -Fq -- "$needle" "$file"; then
      return 0
    fi
    sleep 1
  done
  return 1
}

latest_session_id() {
  python3 - "$store/sessions" <<'PY'
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
sessions = [p for p in root.glob("ses_*") if p.is_dir()]
if not sessions:
    raise SystemExit(1)
latest = max(sessions, key=lambda p: p.stat().st_mtime_ns)
print(latest.name)
PY
}

environment_id_for_session() {
  python3 - "$store/environments" "$1" <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
session_id = sys.argv[2]
for path in root.glob("env_*/environment.json"):
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except Exception:
        continue
    if data.get("lastSessionId") == session_id:
        print(data.get("id") or path.parent.name)
        raise SystemExit(0)
raise SystemExit(1)
PY
}

assert_directory_empty() {
  local dir="$1"
  local label="$2"
  [ -d "$dir" ] || return
  if find "$dir" -mindepth 1 -print -quit | grep -q .; then
    find "$dir" -mindepth 1 -maxdepth 2 -print >&2 || true
    fail "$label runtime directory is not empty: $dir"
  fi
}

assert_session_runtime_cleaned() {
  local session_id="$1"
  local session_dir="$store/sessions/$session_id"
  local path
  for path in \
    "$session_dir/tmp" \
    "$session_dir/shims" \
    "$session_dir/bootstrap" \
    "$session_dir/identity" \
    "$session_dir/broker.sock" \
    "$session_dir/broker-endpoint.json" \
    "$session_dir/network-plan.json" \
    "$session_dir/network" \
    "/tmp/hideout-$session_id.sock"; do
    if [ -e "$path" ]; then
      fail "session runtime artifact remained after interruption cleanup: $path"
    fi
  done
}

assert_environment_runtime_cleaned() {
  local env_id="$1"
  local runtime="$store/environments/$env_id/runtime"
  local entry name
  [ -d "$runtime" ] || return
  for entry in "$runtime"/*; do
    [ -e "$entry" ] || continue
    name="$(basename "$entry")"
    case "$name" in
      tmp|shims|network|bootstrap)
        assert_directory_empty "$entry" "environment $env_id"
        ;;
      *)
        fail "unexpected environment runtime artifact remained: $entry"
        ;;
    esac
  done
}

assert_port_closed() {
  python3 - "$1" <<'PY'
import socket
import sys

port = int(sys.argv[1])
with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
    sock.settimeout(1)
    if sock.connect_ex(("127.0.0.1", port)) == 0:
        raise SystemExit(f"port {port} is still accepting connections")
PY
}

assert_environment_guidance() {
  local file="$1"
  local env_name="$2"
  assert_contains "run again: hideout run --env $env_name -- <command>" "$file"
  assert_contains "stop: hideout stop $env_name" "$file"
  assert_contains "clean-after-stop: hideout clean --stopped $env_name" "$file"
}

has_entries() {
  local dir="$1"
  [ -d "$dir" ] || return 1
  find "$dir" -mindepth 1 -print -quit | grep -q .
}

backend_prepare_side_effects_present() {
  local store_root="$1"
  local lima_root="$2"
  if has_entries "$store_root/environments"; then
    return 0
  fi
  if has_entries "$store_root/sessions"; then
    return 0
  fi
  if [ -d "$lima_root" ] && find "$lima_root" -mindepth 1 -maxdepth 1 -type d -name 'hideout-*' -print -quit | grep -q .; then
    return 0
  fi
  return 1
}

assert_no_backend_prepare_side_effects() {
  local store_root="$1"
  local lima_root="$2"
  local label="$3"
  if ! backend_prepare_side_effects_present "$store_root" "$lima_root"; then
    return
  fi
  if has_entries "$store_root/environments"; then
    find "$store_root/environments" -mindepth 1 -maxdepth 2 -print >&2 || true
    fail "$label unsafe workspace created environment state"
  fi
  if has_entries "$store_root/sessions"; then
    find "$store_root/sessions" -mindepth 1 -maxdepth 2 -print >&2 || true
    fail "$label unsafe workspace created session state"
  fi
  if [ -d "$lima_root" ] && find "$lima_root" -mindepth 1 -maxdepth 1 -type d -name 'hideout-*' -print -quit | grep -q .; then
    find "$lima_root" -mindepth 1 -maxdepth 1 -type d -name 'hideout-*' -print >&2 || true
    fail "$label unsafe workspace created a Lima instance directory"
  fi
}

assert_backend_prepare_side_effect_detector_positive_control() {
  if ! backend_prepare_side_effects_present "$store" "$lima_home"; then
    fail "backend prepare side-effect detector did not detect the successful reference run state"
  fi
  marker "backend-prepare-side-effect-detector=armed"
}

assert_unsafe_workspace_rejected_before_backend() {
  local label="$1"
  local workspace_path="$2"
  local check_store="$tmp/unsafe-$label-store"
  local check_lima="$tmp/unsafe-$label-lima"
  local stdout="$tmp/unsafe-$label.out"
  local stderr="$tmp/unsafe-$label.err"
  mkdir -p "$check_store" "$check_lima"
  if [ "$workspace_path" = "__store__" ]; then
    workspace_path="$check_store"
  fi
  if with_timeout "$GATE_TIMEOUT" run_env_with_roots "$check_store" "$check_lima" "$hideout" run --backend lima --network direct --workspace "$workspace_path" -- sh -c 'echo should-not-run' >"$stdout" 2>"$stderr"; then
    fail "$label unsafe workspace unexpectedly succeeded"
  fi
  assert_not_contains "should-not-run" "$stdout"
  assert_no_backend_prepare_side_effects "$check_store" "$check_lima" "$label"
}

assert_case_variant_unsafe_workspace_rejected() {
  local case_home="$tmp/case-home"
  local lower_ssh="$case_home/.ssh"
  local upper_ssh="$case_home/.SSH"
  mkdir -p "$lower_ssh"
  if [ ! -d "$upper_ssh" ]; then
    marker "case-variant-unsafe-workspace=skipped-case-sensitive"
    return
  fi
  HOME="$case_home" assert_unsafe_workspace_rejected_before_backend "case-variant-ssh" "$upper_ssh"
  marker "case-variant-unsafe-workspace=rejected"
}

assert_unavailable_helper_fails_closed() {
  local check_store="$tmp/unavailable-helper-store"
  local check_lima="$tmp/unavailable-helper-lima"
  local stdout="$tmp/unavailable-helper.out"
  local stderr="$tmp/unavailable-helper.err"
  mkdir -p "$check_store" "$check_lima"
  if env \
    "HIDEOUT_STORE_ROOT=$check_store" \
    "LIMA_HOME=$check_lima" \
    "HIDEOUT_BROWSER_PATH=$HIDEOUT_BROWSER_PATH" \
    "HIDEOUT_LINUX_SHIM_PATH=$tmp/missing-hideout-shim-linux" \
    "HIDEOUT_LINUX_HOSTFSD_PATH=$HIDEOUT_LINUX_HOSTFSD_PATH" \
    "$hideout" run --backend lima --network direct --workspace "$workspace" -- sh -c 'echo should-not-run' >"$stdout" 2>"$stderr"; then
    fail "unavailable helper path unexpectedly succeeded"
  fi
  assert_not_contains "should-not-run" "$stdout"
  assert_not_contains "native_ok" "$stdout"
  assert_not_contains "weak isolation" "$stderr"
  marker "unavailable-helper=fail-closed"
}

assert_audit_action() {
  python3 - "$1" "$2" <<'PY'
import json
import sys

path, action = sys.argv[1], sys.argv[2]
with open(path, "r", encoding="utf-8") as f:
    events = json.load(f)
if not any(event.get("action") == action for event in events):
    raise SystemExit(f"missing audit action {action}")
PY
}

assert_audit_action_prefix() {
  python3 - "$1" "$2" <<'PY'
import json
import sys

path, prefix = sys.argv[1], sys.argv[2]
with open(path, "r", encoding="utf-8") as f:
    events = json.load(f)
if not any(str(event.get("action", "")).startswith(prefix) for event in events):
    raise SystemExit(f"missing audit action prefix {prefix}")
PY
}

assert_audit_decision() {
  python3 - "$1" "$2" "$3" <<'PY'
import json
import sys

path, action, decision = sys.argv[1], sys.argv[2], sys.argv[3]
with open(path, "r", encoding="utf-8") as f:
    events = json.load(f)
if not any(event.get("action") == action and event.get("decision") == decision for event in events):
    raise SystemExit(f"missing audit action {action} decision {decision}")
PY
}

assert_audit_hostfs_deny() {
  python3 - "$1" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as f:
    events = json.load(f)
for event in events:
    action = str(event.get("action", ""))
    decision = event.get("decision")
    details = event.get("details") or {}
    if action.startswith("host.fs.") and (decision == "deny" or details.get("policyEffect") in {"deny", "none"}):
        raise SystemExit(0)
raise SystemExit("missing denied HostFS audit event")
PY
}

prepare_workspace() {
  printf 'write the deterministic reference output\n' >"$workspace/task.txt"
  expected_content="hideout lima real run ok"
  hostfs_root="$(mktemp -d "${TMPDIR:-/tmp}/hideout-realrun-hostfs.XXXXXX")"
  hostfs_root="$(cd "$hostfs_root" && pwd -P)"
  mkdir -p "$hostfs_root"
  hostfs_allowed="$hostfs_root/allowed.txt"
  hostfs_denied="$hostfs_root/denied.txt"
  printf 'hostfs-allowed\n' >"$hostfs_allowed"
  printf 'hostfs-denied\n' >"$hostfs_denied"
}

run_doctor_and_init() {
  echo "lima-real-run: initializing isolated profile"
  run_env "$hideout" init --no-input --profile default --template "$init_template" --backend lima "${init_network_args[@]}" >/dev/null
  run_env "$hideout" doctor --backend lima --workspace "$workspace" "${run_network_args[@]}" >/dev/null
}

run_reference_workload() {
  echo "lima-real-run: running reference workload"
  local stdout="$tmp/reference.out"
  local stderr="$tmp/reference.err"
  local started=$SECONDS
  if ! with_timeout "$GATE_TIMEOUT" run_env "$hideout" run \
    --verbose \
    --backend lima \
    --workspace "$workspace" \
    "${run_network_args[@]}" \
    -- ./hideout-test-cli workload \
      --task task.txt \
      --output result.txt \
      --expected "$expected_content" \
      --url "$endpoint_url" \
      --expect-status "$endpoint_status" \
    >"$stdout" 2>"$stderr"; then
    echo "lima-real-run: reference workload failed" >&2
    echo "lima-real-run: stdout" >&2
    cat "$stdout" >&2 || true
    echo "lima-real-run: stderr" >&2
    cat "$stderr" >&2 || true
    exit 1
  fi
  local elapsed=$((SECONDS - started))
  if [ "$elapsed" -gt "$REFERENCE_WORKLOAD_LIMIT_SECONDS" ]; then
    fail "reference workload took ${elapsed}s, over limit ${REFERENCE_WORKLOAD_LIMIT_SECONDS}s"
  fi

  assert_contains "workspace-updated=yes" "$stdout"
  assert_contains "success-check=passed" "$stdout"
  assert_contains "endpoint=reachable" "$stdout"
  assert_contains "http_status=$endpoint_status" "$stdout"
  assert_contains "Hideout boundary:" "$stderr"

  local output_content
  output_content="$(cat "$workspace/result.txt")"
  if [ "$output_content" != "$expected_content" ]; then
    fail "host read-only artifact verification failed"
  fi

  reference_audit_path="$(extract_audit_path "$stderr")"
  reference_session_id="$(extract_session_id "$reference_audit_path")"
  reference_environment_id="$(extract_environment_id "$stderr")"
  reference_environment_name="$(extract_environment_name "$stderr")"
  [ -n "$reference_session_id" ] || fail "reference session id missing"
  [ -n "$reference_environment_id" ] || fail "reference environment id missing"
  [ -n "$reference_environment_name" ] || fail "reference environment name missing"
  [ -f "$reference_audit_path" ] || fail "reference audit path does not exist: $reference_audit_path"
  assert_environment_guidance "$stderr" "$reference_environment_name"
  fetch_audit_json "$reference_session_id" "$tmp/reference-audit.json"
  assert_audit_action "$tmp/reference-audit.json" "session.start"
  assert_audit_action "$tmp/reference-audit.json" "session.end"
  assert_audit_action "$tmp/reference-audit.json" "network.setup"
  assert_no_forbidden_strings "$stdout" "$stderr" "$tmp/reference-audit.json"

  marker "workspace-updated=yes"
  marker "success-check=passed"
  marker "network=$network_label"
  marker "endpoint=reachable"
  marker "session=$reference_session_id"
  marker "environment=$reference_environment_id"
  marker "audit=$reference_audit_path"
  marker "boundary=present"
  marker "duration_seconds=$elapsed"
}

run_reusable_workload_step() {
  local step="$1"
  local output_name="$2"
  local expected="$3"
  local stdout="$tmp/reuse-$step.out"
  local stderr="$tmp/reuse-$step.err"
  if ! with_timeout "$GATE_TIMEOUT" run_env "$hideout" run \
    --verbose \
    --backend lima \
    --workspace "$workspace" \
    --env "$reference_environment_name" \
    "${run_network_args[@]}" \
    -- sh -eu -c '
step="$1"
task_file="$2"
output_file="$3"
expected="$4"
endpoint="$5"
status="$6"
if [ "$step" = "second" ]; then
  test "$(cat "$HOME/.hideout-real-run-profile-state")" = "profile-state"
fi
./hideout-test-cli workload \
  --task "$task_file" \
  --output "$output_file" \
  --expected "$expected" \
  --url "$endpoint" \
  --expect-status "$status"
if [ "$step" = "first" ]; then
  printf "profile-state\n" >"$HOME/.hideout-real-run-profile-state"
fi
if [ "$step" = "second" ]; then
  printf "profile-state=preserved\n"
fi
' hideout-reuse "$step" "task.txt" "$output_name" "$expected" "$endpoint_url" "$endpoint_status" \
    >"$stdout" 2>"$stderr"; then
    echo "lima-real-run: reusable workload $step failed" >&2
    echo "lima-real-run: stdout" >&2
    cat "$stdout" >&2 || true
    echo "lima-real-run: stderr" >&2
    cat "$stderr" >&2 || true
    exit 1
  fi

  assert_contains "workspace-updated=yes" "$stdout"
  assert_contains "success-check=passed" "$stdout"
  assert_contains "endpoint=reachable" "$stdout"
  local output_content env_id
  output_content="$(cat "$workspace/$output_name")"
  if [ "$output_content" != "$expected" ]; then
    fail "reusable workload $step artifact verification failed"
  fi
  env_id="$(extract_environment_id "$stderr")"
  if [ "$env_id" != "$reference_environment_id" ]; then
    fail "reusable workload $step used environment $env_id, want $reference_environment_id"
  fi
  assert_environment_guidance "$stderr" "$reference_environment_name"
  if [ "$step" = "second" ]; then
    assert_contains "profile-state=preserved" "$stdout"
  fi
  assert_no_forbidden_strings "$stdout" "$stderr"
}

run_reusable_environment_workflow() {
  echo "lima-real-run: running reusable environment two-run workflow"
  [ -n "$reference_environment_id" ] || fail "reference environment id is required for reusable workflow"
  run_reusable_workload_step "first" "result-reuse-1.txt" "$expected_content reusable first"
  run_reusable_workload_step "second" "result-reuse-2.txt" "$expected_content reusable second"
  marker "reusable-environment=$reference_environment_id"
  marker "profile-state=preserved"
}

run_interrupt_cleanup_one() {
  local label="$1"
  local signal_name="$2"
  local preview_port stdout stderr audit_json session_id env_id status
  preview_port="$(free_host_port)"
  stdout="$tmp/interrupt-$label.out"
  stderr="$tmp/interrupt-$label.err"
  audit_json="$tmp/interrupt-$label-audit.json"

  run_env_exec "$hideout" run \
    --verbose \
    --backend lima \
    --workspace "$workspace" \
    --fs "read:$hostfs_allowed" \
    --preview "127.0.0.1:$preview_port" \
    --network direct \
    -- sh -eu -c '
printf "interrupt-ready\n"
trap "exit 0" INT TERM
while :; do sleep 1; done
' >"$stdout" 2>"$stderr" &
  local pid=$!

  if ! wait_for_file_contains "$stdout" "interrupt-ready" 120; then
    kill "$pid" 2>/dev/null || true
    sleep 2
    kill -KILL "$pid" 2>/dev/null || true
    echo "lima-real-run: interruption stdout" >&2
    cat "$stdout" >&2 || true
    echo "lima-real-run: interruption stderr" >&2
    cat "$stderr" >&2 || true
    fail "$label interruption run did not become ready"
  fi

  kill "-$signal_name" "$pid" 2>/dev/null || true
  status=0
  if wait "$pid"; then
    status=0
  else
    status=$?
  fi
  if [ "$status" -eq 0 ]; then
    marker "interrupt-$label-exit=0"
  else
    marker "interrupt-$label-exit=$status"
  fi

  session_id="$(latest_session_id)"
  [ -n "$session_id" ] || fail "$label interruption session id missing"
  fetch_audit_json "$session_id" "$audit_json"
  assert_audit_action "$audit_json" "session.start"
  assert_audit_action "$audit_json" "session.cleanup"
  assert_audit_action "$audit_json" "preview.open"
  assert_audit_action_prefix "$audit_json" "endpoint.expose."
  assert_no_forbidden_strings "$stdout" "$stderr" "$audit_json"

  env_id="$(environment_id_for_session "$session_id")"
  [ -n "$env_id" ] || fail "$label interruption environment id missing"
  assert_session_runtime_cleaned "$session_id"
  assert_environment_runtime_cleaned "$env_id"
  assert_port_closed "$preview_port"
  marker "interrupt-$label=clean"
}

run_interrupt_cleanup_smoke() {
  echo "lima-real-run: checking interruption cleanup"
  run_interrupt_cleanup_one "sigterm" "TERM"
  run_interrupt_cleanup_one "sigint" "INT"
}

run_boundary_action_set() {
  echo "lima-real-run: running fixed Boundary Action Set"
  local preview_port
  preview_port="$(free_host_port)"
  local stdout="$tmp/boundary.out"
  local stderr="$tmp/boundary.err"
  local -a boundary_network_args=(--network direct)
  if ! with_timeout "$GATE_TIMEOUT" run_env "$hideout" run \
    --verbose \
    --backend lima \
    --workspace "$workspace" \
    --fs "read:$hostfs_allowed" \
    --fs "read:$hostfs_denied" \
    --no-fs "read:$hostfs_denied" \
    --preview "127.0.0.1:$preview_port" \
    "${boundary_network_args[@]}" \
    -- sh -eu -c '
set +e
open http://127.0.0.1:9 >/tmp/hideout-open-deny.out 2>/tmp/hideout-open-deny.err
printf "host_open_exit=%s\n" "$?"
set -e
printf "hostfs_allowed=%s\n" "$(cat "$1")"
if cat "$2" >/dev/null 2>&1; then
  echo "denied HostFS path unexpectedly readable" >&2
  exit 31
fi
printf "hostfs_denied=yes\n"
./hideout-test-cli login --listen "127.0.0.1:$3" --browser-redirect --wait 30s
' hideout-boundary "$hostfs_allowed" "$hostfs_denied" "$preview_port" \
    >"$stdout" 2>"$stderr"; then
    echo "lima-real-run: Boundary Action Set run failed" >&2
    echo "lima-real-run: stdout" >&2
    cat "$stdout" >&2 || true
    echo "lima-real-run: stderr" >&2
    cat "$stderr" >&2 || true
    exit 1
  fi

  assert_contains "hostfs_allowed=hostfs-allowed" "$stdout"
  assert_contains "hostfs_denied=yes" "$stdout"
  assert_contains "Hideout boundary:" "$stderr"

  local audit_path session_id audit_json
  audit_path="$(extract_audit_path "$stderr")"
  session_id="$(extract_session_id "$audit_path")"
  audit_json="$tmp/boundary-audit.json"
  [ -n "$session_id" ] || fail "boundary session id missing"
  [ -f "$audit_path" ] || fail "boundary audit path does not exist: $audit_path"
  fetch_audit_json "$session_id" "$audit_json"
  assert_audit_action "$audit_json" "session.start"
  assert_audit_action "$audit_json" "session.end"
  assert_audit_action "$audit_json" "network.setup"
  assert_audit_decision "$audit_json" "host.open" "deny"
  assert_audit_hostfs_deny "$audit_json"
  assert_audit_action "$audit_json" "preview.open"
  assert_audit_action_prefix "$audit_json" "endpoint.expose."
  assert_no_forbidden_strings "$stdout" "$stderr" "$audit_json"
  marker "evidence=boundary-action-set"
}

run_negative_paths() {
  echo "lima-real-run: checking unsafe workspace fail-closed paths"
  assert_unsafe_workspace_rejected_before_backend "home" "$HOME"
  assert_unsafe_workspace_rejected_before_backend "store" "__store__"
  assert_case_variant_unsafe_workspace_rejected

  echo "lima-real-run: checking HostFS reserved-root grant fail-closed path"
  if with_timeout "$GATE_TIMEOUT" run_env "$hideout" run --backend lima --network direct --workspace "$workspace" --fs "tree:$store" -- sh -c 'echo should-not-run' >"$tmp/reserved-hostfs.out" 2>"$tmp/reserved-hostfs.err"; then
    fail "store-covering HostFS grant unexpectedly succeeded"
  fi
  assert_not_contains "should-not-run" "$tmp/reserved-hostfs.out"

  echo "lima-real-run: checking missing target has no fallback"
  if with_timeout "$GATE_TIMEOUT" run_env "$hideout" run --backend lima --network direct --workspace "$workspace" -- hideout-real-run-missing-command >"$tmp/missing.out" 2>"$tmp/missing.err"; then
    fail "missing target unexpectedly succeeded"
  fi
  assert_contains 'command "hideout-real-run-missing-command" not found in lima backend' "$tmp/missing.err"
  assert_not_contains "weak isolation" "$tmp/missing.err"
  assert_unavailable_helper_fails_closed

  echo "lima-real-run: checking native backend is weak wiring evidence only"
  if ! run_env "$hideout" explain --backend native --allow-weak-isolation --network direct --workspace "$workspace" -- sh -c 'true' >"$tmp/native-explain.out" 2>"$tmp/native-explain.err"; then
    echo "lima-real-run: native weak harness explain failed" >&2
    cat "$tmp/native-explain.out" >&2 || true
    cat "$tmp/native-explain.err" >&2 || true
    exit 1
  fi
  assert_contains "weak isolation" "$tmp/native-explain.out"
  if ! run_env "$hideout" run --backend native --allow-weak-isolation --network direct --workspace "$workspace" -- sh -c 'printf "native_ok=yes\n"' >"$tmp/native.out" 2>"$tmp/native.err"; then
    echo "lima-real-run: native weak harness check failed" >&2
    cat "$tmp/native.out" >&2 || true
    cat "$tmp/native.err" >&2 || true
    exit 1
  fi
  assert_contains "native_ok=yes" "$tmp/native.out"
}

cleanup() {
  if [ -n "${endpoint_pid:-}" ]; then
    kill "$endpoint_pid" 2>/dev/null || true
    wait "$endpoint_pid" 2>/dev/null || true
  fi
  if [ -x "${hideout:-}" ]; then
    HIDEOUT_STORE_ROOT="${store:-}" LIMA_HOME="${lima_home:-}" "$hideout" clean >/dev/null 2>&1 || true
  fi
  rm -rf "${hostfs_root:-}"
  if [ "${HIDEOUT_LIMA_REAL_RUN_KEEP_TMP:-}" = "1" ]; then
    echo "lima-real-run: kept tmp=$tmp"
  else
    rm -rf "$tmp"
  fi
}

main() {
  require_command go
  require_command limactl
  require_command python3
  require_command curl

  tmp="$(mktemp -d "/tmp/ho-rr.XXXXXX")"
  endpoint_pid=""
  hostfs_root=""
  trap cleanup EXIT

  bin="$tmp/bin"
  store="$tmp/store"
  lima_home="$tmp/lima"
  workspace="$tmp/workspace"
  mkdir -p "$bin" "$store" "$lima_home" "$workspace"
  workspace="$(cd "$workspace" && pwd -P)"

  build_binaries
  configure_network
  if [ "$network_label" = "direct" ]; then
    start_lab_endpoint
  fi
  prepare_workspace
  run_doctor_and_init

  marker "boundary-action-set=${BOUNDARY_ACTION_SET[*]}"
  run_reference_workload
  assert_backend_prepare_side_effect_detector_positive_control
  run_reusable_environment_workflow
  run_boundary_action_set
  run_interrupt_cleanup_smoke
  run_negative_paths
  assert_no_forbidden_strings "$tmp"/*.out "$tmp"/*.err "$tmp"/*.json
  marker "evidence=passed"
  marker "passed"
}

main "$@"
