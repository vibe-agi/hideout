#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
. "$ROOT/scripts/lib/daemon-temp.sh"

# These tests use real local PTYs and fail if terminal state is simulated with
# ordinary pipes. The expect lane additionally crosses the daemon transport.
go test ./internal/app -run 'TestRunClientTerminalAutoRawResizeAndRestore$' -count=1
go test ./internal/backend/native -run 'TestRunWithStreamsAllocatesPTYWithInitialSize$' -count=1

if ! command -v expect >/dev/null 2>&1; then
  echo "daemon-session-pty: expect unavailable; daemon E2E PTY lane not-run"
  exit 0
fi

tmp="$(hideout_mktemp_daemon_store)"
store="$tmp/s"
bin="$tmp/hideout"
workspace="$tmp/workspace"
cleanup() {
  HIDEOUT_STORE_ROOT="$store" "$bin" daemon stop >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT

go build -o "$bin" ./cmd/hideout
mkdir -p "$workspace"
export HIDEOUT_STORE_ROOT="$store"
"$bin" init --no-input --profile default --template dev --backend native --network direct >/dev/null

HIDEOUT_PTY_BIN="$bin" HIDEOUT_PTY_WORKSPACE="$workspace" expect <<'EXPECT'
set timeout 15
set stty_init "rows 24 columns 80"
log_user 0
spawn -noecho $env(HIDEOUT_PTY_BIN) run --backend native --allow-weak-isolation --terminal always --workspace $env(HIDEOUT_PTY_WORKSPACE) -- sh -c {printf 'initial:'; stty size; trap 'printf "resized:"; stty size; exit 0' WINCH; printf 'ready\n'; while :; do sleep 1; done}
expect {
  -re {initial:24 80.*ready} {}
  timeout {puts stderr "daemon-session-pty: initial size not observed"; exit 2}
}
stty rows 31 columns 97 < $spawn_out(slave,name)
exec kill -WINCH [exp_pid -i $spawn_id]
expect {
  -re {resized:31 97} {}
  timeout {puts stderr "daemon-session-pty: dynamic resize not observed"; exit 3}
}
expect eof
set status [lindex [wait] 3]
if {$status != 0} {puts stderr "daemon-session-pty: resize target exit=$status"; exit 4}

spawn -noecho $env(HIDEOUT_PTY_BIN) run --backend native --allow-weak-isolation --terminal always --workspace $env(HIDEOUT_PTY_WORKSPACE) -- sh -c {trap 'printf "caught-int\n"; exit 130' INT; printf 'ready\n'; while :; do sleep 1; done}
expect {
  -re {ready} {}
  timeout {puts stderr "daemon-session-pty: signal target not ready"; exit 5}
}
send "\003"
expect {
  -re {caught-int} {}
  timeout {puts stderr "daemon-session-pty: Ctrl-C did not reach target"; exit 6}
}
expect eof
set status [lindex [wait] 3]
if {$status != 130} {puts stderr "daemon-session-pty: signal target exit=$status want=130"; exit 7}
EXPECT

echo "daemon-session-pty: passed"
