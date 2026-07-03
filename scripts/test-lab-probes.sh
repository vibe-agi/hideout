#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "lab-probes: missing required command: $1" >&2
    exit 127
  fi
}

start_target() {
  local mode="$1"
  local name="$2"
  shift 2
  local addr_file="$tmp/$name.addr"
  local log_file="$tmp/$name.log"
  local pid
  "$target_bin" --mode "$mode" "$@" >"$addr_file" 2>"$log_file" &
  pid="$!"
  target_pids+=("$pid")
  for _ in {1..100}; do
    if [ -s "$addr_file" ]; then
      START_TARGET_ADDR="$(sed -n '1p' "$addr_file")"
      return
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "lab-probes: $name target exited early" >&2
      cat "$log_file" >&2 || true
      exit 1
    fi
    sleep 0.05
  done
  echo "lab-probes: $name target did not publish a listen address" >&2
  cat "$log_file" >&2 || true
  exit 1
}

run_hideout_lab() {
  HIDEOUT_STORE_ROOT="$store" "$hideout" lab "$@"
}

latest_audit() {
  find "$store/sessions" -name audit.jsonl -type f -print0 |
    xargs -0 ls -t |
    sed -n '1p'
}

assert_latest_audit() {
  local action="$1"
  local status="$2"
  local audit
  audit="$(latest_audit)"
  grep -q "\"action\":\"$action\"" "$audit"
  grep -q "\"status\":\"$status\"" "$audit"
}

assert_lab_requires_enablement() {
  local name="$1"
  shift
  local out="$tmp/lab-disabled-$name.out"
  local err="$tmp/lab-disabled-$name.err"

  if run_hideout_lab "$@" >"$out" 2>"$err"; then
    echo "lab-probes: lab command unexpectedly ran without --enable-lab: $*" >&2
    exit 1
  fi
  grep -q 'requires --enable-lab' "$err"
  if grep -q 'route=lab-probe' "$out"; then
    echo "lab-probes: disabled lab command emitted probe route: $*" >&2
    cat "$out" >&2
    exit 1
  fi
}

require_command go

tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-lab-probes.XXXXXX")"
target_pids=()
START_TARGET_ADDR=""
cleanup() {
  if [ "${#target_pids[@]}" -gt 0 ]; then
    for pid in "${target_pids[@]}"; do
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    done
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT

bin="$tmp/bin"
store="$tmp/store"
mkdir -p "$bin" "$store"

hideout="$bin/hideout"
target_bin="$bin/hideout-gate-lab-target"
go build -o "$hideout" ./cmd/hideout
go build -o "$target_bin" ./cmd/hideout-gate-lab-target

echo "lab-probes: explicit lab enablement guard"
assert_lab_requires_enablement portbridge-loopback portbridge loopback --target 127.0.0.1:1
assert_lab_requires_enablement portbridge-guest-to-host portbridge guest-to-host --target 127.0.0.1:1
assert_lab_requires_enablement portbridge-host-to-guest portbridge host-to-guest --guest-target 127.0.0.1:1
assert_lab_requires_enablement browser-control browser-control --profile lab-probe --browser-path /bin/false
assert_lab_requires_enablement preview-open preview-open --guest-url http://127.0.0.1:1/
if find "$store/sessions" -name audit.jsonl -print -quit 2>/dev/null | grep -q .; then
  echo "lab-probes: disabled lab command wrote probe audit" >&2
  exit 1
fi

start_target echo echo
echo_addr="$START_TARGET_ADDR"
echo "lab-probes: portbridge loopback"
run_hideout_lab portbridge loopback \
  --enable-lab \
  --target "$echo_addr" \
  --send $'hello\n' \
  --expect $'echo:hello\n' \
  --timeout 2s >"$tmp/portbridge-loopback.out"
grep -q 'probe=tcp-forward ok' "$tmp/portbridge-loopback.out"
assert_latest_audit portbridge.probe ok

echo "lab-probes: portbridge guest-to-host"
run_hideout_lab portbridge guest-to-host \
  --enable-lab \
  --target "$echo_addr" \
  --send $'guest\n' \
  --expect $'echo:guest\n' \
  --timeout 2s >"$tmp/portbridge-guest-to-host.out"
grep -q 'probe=tcp-forward ok' "$tmp/portbridge-guest-to-host.out"
assert_latest_audit portbridge.probe ok

echo "lab-probes: portbridge host-to-guest"
run_hideout_lab portbridge host-to-guest \
  --enable-lab \
  --guest-target "$echo_addr" \
  --send $'host\n' \
  --expect $'echo:host\n' \
  --timeout 2s >"$tmp/portbridge-host-to-guest.out"
grep -q 'probe=tcp-forward ok' "$tmp/portbridge-host-to-guest.out"
assert_latest_audit portbridge.probe ok

start_target http preview --path /preview --status 204
http_addr="$START_TARGET_ADDR"
echo "lab-probes: preview-open"
run_hideout_lab preview-open \
  --enable-lab \
  --guest-url "http://$http_addr/preview" \
  --timeout 2s >"$tmp/preview-open.out"
grep -q 'probe=http-get ok' "$tmp/preview-open.out"
grep -q 'status-code=204' "$tmp/preview-open.out"
assert_latest_audit preview.open.probe ok

start_target devtools devtools
devtools_addr="$START_TARGET_ADDR"
devtools_port="${devtools_addr##*:}"
fake_browser="$tmp/fake-browser"
cat >"$fake_browser" <<'FAKE_BROWSER'
#!/usr/bin/env bash
set -euo pipefail
profile=""
for arg in "$@"; do
  case "$arg" in
    --user-data-dir=*) profile="${arg#--user-data-dir=}" ;;
  esac
done
if [ -z "$profile" ]; then
  exit 2
fi
mkdir -p "$profile"
printf '%s\n/devtools/browser/fake\n' "$HIDEOUT_FAKE_DEVTOOLS_PORT" >"$profile/DevToolsActivePort"
while :; do
  sleep 1
done
FAKE_BROWSER
chmod 0700 "$fake_browser"

echo "lab-probes: browser-control fake handshake"
export HIDEOUT_FAKE_DEVTOOLS_PORT="$devtools_port"
run_hideout_lab browser-control \
  --enable-lab \
  --profile lab-probe \
  --browser-path "$fake_browser" \
  --timeout 3s >"$tmp/browser-control.out"
unset HIDEOUT_FAKE_DEVTOOLS_PORT
grep -q 'probe=devtools-version ok' "$tmp/browser-control.out"
grep -q 'browser=FakeChrome/1.0' "$tmp/browser-control.out"
assert_latest_audit browser.control.probe ok

if [ -n "${HIDEOUT_BROWSER_PATH:-}" ]; then
  echo "lab-probes: browser-control real browser path"
  run_hideout_lab browser-control \
    --enable-lab \
    --profile lab-probe-real \
    --browser-path "$HIDEOUT_BROWSER_PATH" \
    --timeout "${HIDEOUT_LAB_BROWSER_TIMEOUT:-10s}" >"$tmp/browser-control-real.out"
  grep -q 'probe=devtools-version ok' "$tmp/browser-control-real.out"
  assert_latest_audit browser.control.probe ok
else
  echo "lab-probes: real browser probe skipped; set HIDEOUT_BROWSER_PATH to run it"
fi

echo "lab-probes: passed"
