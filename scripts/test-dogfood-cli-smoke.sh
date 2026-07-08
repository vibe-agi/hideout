#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

GATE_TIMEOUT="${HIDEOUT_GATE_TIMEOUT:-15m}"

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "dogfood-cli: missing required command: $1" >&2
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
      echo "dogfood-cli: command timed out after $duration: $*" >&2
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

prepare_linux_shim() {
  if [ -n "${HIDEOUT_LINUX_SHIM_PATH:-}" ]; then
    if [ ! -x "$HIDEOUT_LINUX_SHIM_PATH" ]; then
      echo "dogfood-cli: HIDEOUT_LINUX_SHIM_PATH is not executable: $HIDEOUT_LINUX_SHIM_PATH" >&2
      exit 126
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
      echo "dogfood-cli: HIDEOUT_LINUX_HOSTFSD_PATH is not executable: $HIDEOUT_LINUX_HOSTFSD_PATH" >&2
      exit 126
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

configure_expected_commands() {
  HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
    "$hideout" profile tools default expected add hideout-test-cli >/dev/null
}

prepare_fake_browser() {
  local browser="$bin/hideout-gate-browser"
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
  echo "hideout-gate-browser: missing URL argument" >&2
  exit 2
fi
curl -fsSL --max-time "${HIDEOUT_GATE_BROWSER_TIMEOUT:-15}" -o /dev/null "$target"
SH
  chmod +x "$browser"
  HIDEOUT_BROWSER_PATH="$browser"
  export HIDEOUT_BROWSER_PATH
}

start_auth_api() {
  "$lab_target" --mode auth-api --path /check --listen 127.0.0.1:0 >"$tmp/auth-api.addr" 2>"$tmp/auth-api.log" &
  auth_api_pid=$!
  for _ in $(seq 1 100); do
    if [ -s "$tmp/auth-api.addr" ]; then
      auth_api_addr="$(head -n 1 "$tmp/auth-api.addr")"
      return
    fi
    if ! kill -0 "$auth_api_pid" 2>/dev/null; then
      echo "dogfood-cli: auth-api exited early" >&2
      cat "$tmp/auth-api.log" >&2 || true
      exit 1
    fi
    sleep 0.1
  done
  echo "dogfood-cli: auth-api did not publish an address" >&2
  exit 1
}

free_host_port() {
  python3 - <<'PY'
import socket

with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
    s.bind(("127.0.0.1", 0))
    print(s.getsockname()[1])
PY
}

start_redirect_server() {
  local target="$1"
  "$lab_target" --mode redirect --path /login --target "$target" --listen 127.0.0.1:0 >"$tmp/redirect.addr" 2>"$tmp/redirect.log" &
  redirect_pid=$!
  for _ in $(seq 1 100); do
    if [ -s "$tmp/redirect.addr" ]; then
      redirect_addr="$(head -n 1 "$tmp/redirect.addr")"
      return
    fi
    if ! kill -0 "$redirect_pid" 2>/dev/null; then
      echo "dogfood-cli: redirect server exited early" >&2
      cat "$tmp/redirect.log" >&2 || true
      exit 1
    fi
    sleep 0.1
  done
  echo "dogfood-cli: redirect server did not publish an address" >&2
  exit 1
}

require_command go
require_command limactl
require_command python3
require_command curl

tmp="$(mktemp -d "/tmp/ho-dog.XXXXXX")"
auth_api_pid=""
redirect_pid=""
cleanup() {
  if [ -n "${auth_api_pid:-}" ]; then
    kill "$auth_api_pid" 2>/dev/null || true
    wait "$auth_api_pid" 2>/dev/null || true
  fi
  if [ -n "${redirect_pid:-}" ]; then
    kill "$redirect_pid" 2>/dev/null || true
    wait "$redirect_pid" 2>/dev/null || true
  fi
  if [ -x "${hideout:-}" ]; then
    HIDEOUT_STORE_ROOT="${store:-}" LIMA_HOME="${lima_home:-}" "$hideout" clean >/dev/null 2>&1 || true
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT

bin="$tmp/b"
store="$tmp/s"
lima_home="$tmp/l"
workspace="$tmp/w"
mkdir -p "$bin" "$store" "$lima_home" "$workspace"

hideout="$bin/hideout"
lab_target="$bin/hideout-gate-lab-target"
go build -o "$hideout" ./cmd/hideout
go build -o "$lab_target" ./cmd/hideout-gate-lab-target
prepare_fake_browser
prepare_linux_shim
prepare_linux_hostfsd

arch="$(go env GOARCH)"
GOOS=linux GOARCH="$arch" CGO_ENABLED=0 \
  go build -trimpath -o "$workspace/hideout-test-cli" ./cmd/hideout-test-cli

echo "dogfood-cli: initializing profile"
HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" init --no-input --profile default --template dev --backend lima --network direct >/dev/null
configure_expected_commands

echo "dogfood-cli: verifying user-declared expected command and test CLI presence"
if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
  "$hideout" run --backend lima --workspace "$workspace" -- sh -eu -c '
./hideout-test-cli version
' >"$tmp/runtime.out" 2>"$tmp/runtime.err"; then
  echo "dogfood-cli: runtime smoke failed" >&2
  cat "$tmp/runtime.out" >&2
  cat "$tmp/runtime.err" >&2
  exit 1
fi
cat "$tmp/runtime.out"
grep -q 'hideout-test-cli 1.0' "$tmp/runtime.out"

echo "dogfood-cli: verifying env visibility is user policy controlled"
if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
  "$hideout" run --backend lima --workspace "$workspace" -- ./hideout-test-cli env --key HIDEOUT_STORE_ROOT >"$tmp/env-hidden.out" 2>"$tmp/env-hidden.err"; then
  echo "dogfood-cli: hidden env probe failed" >&2
  cat "$tmp/env-hidden.out" >&2
  cat "$tmp/env-hidden.err" >&2
  exit 1
fi
cat "$tmp/env-hidden.out"
grep -q 'env=HIDEOUT_STORE_ROOT absent' "$tmp/env-hidden.out"
if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
  "$hideout" run --backend lima --workspace "$workspace" --env-var TEST_CLI_VISIBLE=1 -- ./hideout-test-cli env --key TEST_CLI_VISIBLE >"$tmp/env-visible.out" 2>"$tmp/env-visible.err"; then
  echo "dogfood-cli: visible env probe failed" >&2
  cat "$tmp/env-visible.out" >&2
  cat "$tmp/env-visible.err" >&2
  exit 1
fi
cat "$tmp/env-visible.out"
grep -q 'env=TEST_CLI_VISIBLE present len=1' "$tmp/env-visible.out"

echo "dogfood-cli: verifying isolated home and XDG locations"
if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
  "$hideout" run --backend lima --workspace "$workspace" -- ./hideout-test-cli home >"$tmp/home.out" 2>"$tmp/home.err"; then
  echo "dogfood-cli: home probe failed" >&2
  cat "$tmp/home.out" >&2
  cat "$tmp/home.err" >&2
  exit 1
fi
cat "$tmp/home.out"
grep -q 'HOME=/hideout/profile/home' "$tmp/home.out"
grep -q 'XDG_CONFIG_HOME=/hideout/profile/config' "$tmp/home.out"
grep -q 'TOKEN_PATH=/hideout/profile/config/hideout-test-cli/token' "$tmp/home.out"

printf 'hideout-test-cli-token\n' >"$tmp/import-token"
echo "dogfood-cli: seeding profile identity state through profile home import"
if ! env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
  "$hideout" profile home default import --from "$tmp/import-token" --to .config/hideout-test-cli/token --force >"$tmp/home-import.out" 2>"$tmp/home-import.err"; then
  echo "dogfood-cli: profile home import failed" >&2
  cat "$tmp/home-import.out" >&2
  cat "$tmp/home-import.err" >&2
  exit 1
fi
cat "$tmp/home-import.out"
if grep -q "$tmp/import-token" "$tmp/home-import.out" || grep -q 'hideout-test-cli-token' "$tmp/home-import.out"; then
  echo "dogfood-cli: profile home import leaked source path or token" >&2
  exit 1
fi

echo "dogfood-cli: verifying imported profile identity state is visible in guest"
if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
  "$hideout" run --backend lima --workspace "$workspace" -- ./hideout-test-cli status >"$tmp/import-status.out" 2>"$tmp/import-status.err"; then
  echo "dogfood-cli: imported status smoke failed" >&2
  cat "$tmp/import-status.out" >&2
  cat "$tmp/import-status.err" >&2
  exit 1
fi
cat "$tmp/import-status.out"
grep -q 'status=authenticated' "$tmp/import-status.out"

echo "dogfood-cli: running isolated callback login"
if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
  "$hideout" run --backend lima --workspace "$workspace" -- ./hideout-test-cli login --self-callback >"$tmp/login.out" 2>"$tmp/login.err"; then
  echo "dogfood-cli: login smoke failed" >&2
  cat "$tmp/login.out" >&2
  cat "$tmp/login.err" >&2
  exit 1
fi
cat "$tmp/login.out"
grep -q 'login=ok' "$tmp/login.out"

echo "dogfood-cli: verifying preview.open can drive a browser-style callback"
preview_port="$(free_host_port)"
if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
  "$hideout" run --backend lima --workspace "$workspace" --preview "127.0.0.1:$preview_port" -- ./hideout-test-cli login --listen "127.0.0.1:$preview_port" --browser-redirect --wait 30s >"$tmp/preview-login.out" 2>"$tmp/preview-login.err"; then
  echo "dogfood-cli: preview callback smoke failed" >&2
  cat "$tmp/preview-login.out" >&2
  cat "$tmp/preview-login.err" >&2
  exit 1
fi
cat "$tmp/preview-login.out"
grep -q 'login=ok' "$tmp/preview-login.out"
grep -q 'browser=http://127.0.0.1:' "$tmp/preview-login.out"

echo "dogfood-cli: verifying host redirect to localhost does not reach guest callback"
callback_port="$(free_host_port)"
callback_url="http://127.0.0.1:$callback_port/callback?state=gate-state&code=gate-code"
callback_prefix="callback=http://127.0.0.1:$callback_port/callback?"
start_redirect_server "$callback_url"
redirect_url="http://$redirect_addr/login"
with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
  "$hideout" run --backend lima --workspace "$workspace" -- ./hideout-test-cli login --listen "127.0.0.1:$callback_port" --wait 5s --expect-timeout >"$tmp/host-redirect-login.out" 2>"$tmp/host-redirect-login.err" &
host_redirect_login_pid=$!
for _ in $(seq 1 120); do
  if grep -Fq "$callback_prefix" "$tmp/host-redirect-login.out" 2>/dev/null &&
    grep -Fq "code=gate-code" "$tmp/host-redirect-login.out" 2>/dev/null &&
    grep -Fq "state=gate-state" "$tmp/host-redirect-login.out" 2>/dev/null; then
    break
  fi
  if ! kill -0 "$host_redirect_login_pid" 2>/dev/null; then
    break
  fi
  sleep 0.25
done
if ! grep -Fq "$callback_prefix" "$tmp/host-redirect-login.out" ||
  ! grep -Fq "code=gate-code" "$tmp/host-redirect-login.out" ||
  ! grep -Fq "state=gate-state" "$tmp/host-redirect-login.out"; then
  echo "dogfood-cli: host redirect login did not publish expected callback" >&2
  cat "$tmp/host-redirect-login.out" >&2 || true
  cat "$tmp/host-redirect-login.err" >&2 || true
  exit 1
fi
if curl -sS -L --max-time 3 "$redirect_url" >"$tmp/host-redirect-curl.out" 2>"$tmp/host-redirect-curl.err"; then
  echo "dogfood-cli: host redirect unexpectedly reached a host localhost listener" >&2
  cat "$tmp/host-redirect-curl.out" >&2
  exit 1
fi
if ! wait "$host_redirect_login_pid"; then
  echo "dogfood-cli: host redirect boundary login failed" >&2
  cat "$tmp/host-redirect-login.out" >&2
  cat "$tmp/host-redirect-login.err" >&2
  cat "$tmp/host-redirect-curl.err" >&2 || true
  exit 1
fi
cat "$tmp/host-redirect-login.out"
grep -q 'login=timeout-ok' "$tmp/host-redirect-login.out"

echo "dogfood-cli: verifying profile identity state persists across runs"
if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
  "$hideout" run --backend lima --workspace "$workspace" -- ./hideout-test-cli status >"$tmp/status.out" 2>"$tmp/status.err"; then
  echo "dogfood-cli: status smoke failed" >&2
  cat "$tmp/status.out" >&2
  cat "$tmp/status.err" >&2
  exit 1
fi
cat "$tmp/status.out"
grep -q 'status=authenticated' "$tmp/status.out"

start_auth_api
api_port="${auth_api_addr##*:}"
api_url="http://host.lima.internal:$api_port/check"

echo "dogfood-cli: verifying authenticated request from guest"
if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
  "$hideout" run --backend lima --workspace "$workspace" -- ./hideout-test-cli request --url "$api_url" >"$tmp/request.out" 2>"$tmp/request.err"; then
  echo "dogfood-cli: request smoke failed" >&2
  cat "$tmp/request.out" >&2
  cat "$tmp/request.err" >&2
  exit 1
fi
cat "$tmp/request.out"
grep -q 'http_status=200' "$tmp/request.out"
grep -q 'body=auth-ok' "$tmp/request.out"

echo "dogfood-cli: verifying Hideout store cannot be granted through HostFS"
if with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
  "$hideout" run --backend lima --workspace "$workspace" --fs "tree:$store" -- sh -eu -c 'echo should-not-run' >"$tmp/store.out" 2>"$tmp/store.err"; then
  echo "dogfood-cli: store-covering grant unexpectedly succeeded" >&2
  cat "$tmp/store.out" >&2
  exit 1
fi
if ! grep -Eq 'control-plane store|reserved-control-plane|control-plane path' "$tmp/store.err"; then
  echo "dogfood-cli: store-covering grant failed without expected reason" >&2
  cat "$tmp/store.err" >&2
  exit 1
fi

echo "dogfood-cli: passed"
