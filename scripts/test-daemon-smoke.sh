#!/usr/bin/env bash
set -euo pipefail

# hideoutd local control-plane smoke (no Lima). Exercises start -> auth refusal
# (audited) -> parity/status -> event stream -> ordered stop over the store socket.

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

command -v jq >/dev/null 2>&1 || { echo "daemon-smoke: jq required" >&2; exit 127; }

tmp="$(mktemp -d "${TMPDIR:-/tmp}/hd-smoke.XXXXXX")"
store="$tmp/store"
daemon_pid=""
cleanup() {
  if [ -n "$daemon_pid" ]; then kill "$daemon_pid" 2>/dev/null || true; wait "$daemon_pid" 2>/dev/null || true; fi
  rm -rf "$tmp"
}
trap cleanup EXIT

export HIDEOUT_STORE_ROOT="$store"
install -d -m 700 "$store"

bin="$tmp/hideout"
go build -o "$bin" ./cmd/hideout

"$bin" init --no-input --profile default --template dev --backend native --network direct >/dev/null

# Start the daemon in the background.
"$bin" daemon start >"$tmp/start.out" 2>&1 &
daemon_pid=$!

sock="$store/daemon/hideoutd.sock"
token_file="$store/daemon/token"
for _ in $(seq 1 50); do
  [ -S "$sock" ] && [ -s "$token_file" ] && break
  sleep 0.1
done
[ -S "$sock" ] || { echo "daemon-smoke: socket not created" >&2; cat "$tmp/start.out" >&2; exit 1; }
token="$(tr -d '\n' <"$token_file")"

curl_sock() { curl -sS --unix-socket "$sock" "$@"; }

# Auth refusal (no token) -> 401.
code="$(curl_sock -o /dev/null -w '%{http_code}' -H 'Host: localhost' "http://localhost/api/v1/overview")"
[ "$code" = "401" ] || { echo "daemon-smoke: unauth request want 401 got $code" >&2; exit 1; }

# Authenticated Manager parity: overview + the special-cased GET routes served.
for path in overview audit/events run/status; do
  code="$(curl_sock -o /dev/null -w '%{http_code}' -H 'Host: localhost' -H "Authorization: Bearer $token" "http://localhost/api/v1/$path")"
  [ "$code" = "200" ] || { echo "daemon-smoke: /api/v1/$path want 200 got $code" >&2; exit 1; }
done

# Daemon status endpoint (separate surface) validates against the schema.
curl_sock -H 'Host: localhost' -H "Authorization: Bearer $token" "http://localhost/daemon/status" >"$tmp/status.json"
go run ./cmd/hideout-schema-validate schemas/daemon-status.schema.json "$tmp/status.json"
jq -e '.version == "hideout.daemon-status/v1" and .state == "serving"' "$tmp/status.json" >/dev/null

# Background work product entry: submit an env clean; endpoint returns an id.
bg="$(curl_sock -H 'Host: localhost' -H "Authorization: Bearer $token" -H 'Content-Type: application/json' -d '{"op":"environment-clean"}' "http://localhost/daemon/background")"
echo "$bg" | jq -e '.id and .op == "environment-clean"' >/dev/null || { echo "daemon-smoke: background submit failed: $bg" >&2; exit 1; }
# A non-env op class is rejected at the endpoint.
code="$(curl_sock -o /dev/null -w '%{http_code}' -H 'Host: localhost' -H "Authorization: Bearer $token" -H 'Content-Type: application/json' -d '{"op":"session-cleanup"}' "http://localhost/daemon/background")"
[ "$code" = "400" ] || { echo "daemon-smoke: session-cleanup should be rejected, got $code" >&2; exit 1; }

# WebUI loopback transport: the daemon printed a WebUI URL whose page consumes the
# event stream via EventSource.
ui_url="$(sed -n 's/^WebUI: //p' "$tmp/start.out" | head -n1)"
[ -n "$ui_url" ] || { echo "daemon-smoke: no WebUI URL printed" >&2; cat "$tmp/start.out" >&2; exit 1; }
ui_base="${ui_url%%/#*}"
curl -sS "$ui_base/" | grep -q "EventSource" || { echo "daemon-smoke: WebUI does not consume the event stream" >&2; exit 1; }
# The event endpoint is SSE (streams); cap it and read just the status.
code="$(curl -sS --max-time 2 -o /dev/null -w '%{http_code}' "$ui_base/daemon/events?token=$token" || true)"
[ "$code" = "200" ] || { echo "daemon-smoke: WebUI event endpoint (query token) want 200 got $code" >&2; exit 1; }
code="$(curl -sS --max-time 2 -o /dev/null -w '%{http_code}' "$ui_base/daemon/events?token=wrong" || true)"
[ "$code" = "401" ] || { echo "daemon-smoke: WebUI event endpoint wrong token want 401 got $code" >&2; exit 1; }

# Auth refusal was recorded in the daemon-local audit log, without token material.
grep -q '"action":"daemon.auth"' "$store/daemon/daemon-audit.jsonl"
if grep -q 'Bearer' "$store/daemon/daemon-audit.jsonl"; then
  echo "daemon-smoke: daemon audit leaked token material" >&2; exit 1
fi

# Ordered stop.
"$bin" daemon stop >/dev/null 2>&1 || true
for _ in $(seq 1 50); do [ -S "$sock" ] || break; sleep 0.1; done
[ -S "$sock" ] && { echo "daemon-smoke: socket remained after stop" >&2; exit 1; }
wait "$daemon_pid" 2>/dev/null || true
daemon_pid=""

echo "daemon-smoke: passed"
