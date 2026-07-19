#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
. "$ROOT/scripts/lib/daemon-temp.sh"

command -v jq >/dev/null 2>&1 || { echo "daemon-session-smoke: jq required" >&2; exit 127; }

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

set +e
"$bin" run --backend native --allow-weak-isolation --terminal never --workspace "$workspace" -- \
  sh -c 'printf "stdout\000bytes"; printf stderr >&2; exit 17' \
  >"$tmp/stdout" 2>"$tmp/stderr"
status=$?
set -e
[ "$status" -eq 17 ] || { echo "daemon-session-smoke: exit=$status want=17" >&2; cat "$tmp/stderr" >&2; exit 1; }
[ "$(wc -c <"$tmp/stdout" | tr -d ' ')" -eq 12 ]
printf 'stdout\000bytes' >"$tmp/want-stdout"
cmp "$tmp/want-stdout" "$tmp/stdout"
printf stderr >"$tmp/want-stderr"
cmp "$tmp/want-stderr" "$tmp/stderr"

"$bin" daemon status >"$tmp/status.json"
go run ./cmd/hideout-schema-validate schemas/daemon-status.schema.json "$tmp/status.json"
jq -e '
  .state == "serving" and
  (.transport.sessionSocket | endswith("hideoutd-session.sock")) and
  .transport.sessionProtocol == "hideout.session/v1"
' "$tmp/status.json" >/dev/null

"$bin" daemon stop >/dev/null
[ ! -S "$store/daemon/hideoutd-session.sock" ]

echo "daemon-session-smoke: passed"
