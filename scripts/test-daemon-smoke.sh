#!/usr/bin/env bash
set -euo pipefail

# hideoutd local control-plane smoke (no Lima). Exercises start -> auth refusal
# (audited) -> parity/status -> event stream -> ordered stop over the store socket.

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
. "$ROOT/scripts/lib/daemon-temp.sh"

command -v jq >/dev/null 2>&1 || { echo "daemon-smoke: jq required" >&2; exit 127; }

tmp="$(hideout_mktemp_daemon_store)"
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

# Initialize through the already-running authenticated daemon. Since 038, init
# itself auto-starts hideoutd, so starting the lifecycle fixture first avoids a
# competing process and verifies the production Manager path.
"$bin" init --no-input --profile default --template dev --backend native --network direct >/dev/null

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

# The public UI command must reuse this daemon's live console and exit without
# invalidating its URL. Compare it with the daemon-owned entrypoint, then use the
# command result for every browser probe below.
daemon_ui_url="$(sed -n 's/^WebUI: //p' "$tmp/start.out" | head -n1)"
[ -n "$daemon_ui_url" ] || { echo "daemon-smoke: no daemon WebUI URL printed" >&2; cat "$tmp/start.out" >&2; exit 1; }
"$bin" ui --no-open --print-url >"$tmp/ui-command.out"
ui_url="$(sed -n 's/^Hideout UI: //p' "$tmp/ui-command.out" | head -n1)"
[ "$ui_url" = "$daemon_ui_url" ] || {
  echo "daemon-smoke: hideout ui did not resolve the running daemon console" >&2
  exit 1
}
ui_base="${ui_url%%/#*}"
curl --fail --silent --show-error "$ui_base/" >"$tmp/ui-index.html"
grep -Fq 'src="/assets/client.js"' "$tmp/ui-index.html" || {
  echo "daemon-smoke: WebUI does not load its event client" >&2
  exit 1
}
grep -Fq 'src="/assets/app.js"' "$tmp/ui-index.html" || {
  echo "daemon-smoke: WebUI does not load its console application" >&2
  exit 1
}
curl --fail --silent --show-error \
  "$ui_base/assets/client.js" >"$tmp/ui-client.js"
grep -Fq 'new EventSource(' "$tmp/ui-client.js" || {
  echo "daemon-smoke: WebUI event client does not open the event stream" >&2
  exit 1
}
curl --fail --silent --show-error \
  "$ui_base/assets/app.js" >"$tmp/ui-app.js"
grep -Fq 'root.Client.events({' "$tmp/ui-app.js" || {
  echo "daemon-smoke: WebUI console does not subscribe to the event client" >&2
  exit 1
}
# The event endpoint is SSE (streams). Bind the probe to the authoritative
# snapshot sequence just like a real client. A concurrent producer may advance
# the bus between the two requests, so 409 means reseed and retry, never omit
# the sequence contract.
code=""
sequence=""
for _ in 1 2 3; do
  curl_sock -H 'Host: localhost' -H "Authorization: Bearer $token" \
    "http://localhost/api/v1/operator/snapshot?activityLimit=1" \
    >"$tmp/operator-snapshot.json"
  sequence="$(jq -er '.data.sequence | select(type == "number" and floor == . and . >= 0)' \
    "$tmp/operator-snapshot.json")"
  code="$(curl -s --max-time 2 -o /dev/null -w '%{http_code}' \
    "$ui_base/daemon/events?token=$token&since=$sequence" || true)"
  [ "$code" = "409" ] || break
done
[ "$code" = "200" ] || {
  echo "daemon-smoke: sequence-bound WebUI event endpoint want 200 got $code (since=$sequence)" >&2
  exit 1
}
code="$(curl -sS --max-time 2 -o /dev/null -w '%{http_code}' \
  "$ui_base/daemon/events?token=wrong&since=$sequence" || true)"
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
