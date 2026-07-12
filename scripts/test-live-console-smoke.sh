#!/usr/bin/env bash
set -euo pipefail

# 007/019/027 live-console smoke: typed event/seed schema, daemon fan-out,
# route/event drift guards, WebUI/TUI payload-driven proof, operator console
# panels, no hidden polling timer, runtime-verified existing-action routes, and
# control-plane redaction scans.

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

test -f schemas/live-console-seed.schema.json
test -f schemas/daemon-event.schema.json
jq empty schemas/live-console-seed.schema.json schemas/daemon-event.schema.json

go test ./internal/liveconsole -run 'TestEventCatalog|TestReducer'
go test ./internal/daemon -run 'TestDaemonEndpoint|TestDaemonServesFull.*Route|TestEventBus|TestTerminalEvent|TestSubscribeEvents|TestLoopbackUIServesEventConsumingWebUI'
go test ./internal/manager -run 'TestManagerRoute|TestWebUI.*Action|TestWebUILiveConsole|TestCoreExportEmitsLiveOperationEvents|TestCoreCloseRunSessionEmitsCleanupEvent'
go test ./internal/app -run 'TestTUILiveConsole'

grep -q 'applyLiveEvent(JSON.parse(message.data))' internal/manager/server.go
grep -q 'data-panel="operator-console"' internal/manager/server.go
grep -q 'function consoleActionSummary()' internal/manager/server.go
grep -q 'decision/approve' internal/manager/server.go
grep -q 'notice/ack' internal/manager/server.go
if grep -q 'setInterval' internal/manager/server.go; then
  echo "live-console-smoke: WebUI must not use a polling timer" >&2
  exit 1
fi
if grep -q 'setTimeout(function() { pending = false; load(); }' internal/manager/server.go; then
  echo "live-console-smoke: WebUI event stream must not re-fetch overview/audit" >&2
  exit 1
fi
if grep -Eq 'cap_[0-9a-f]{32,}|HIDEOUT_SECRET_[A-Z0-9_]+=socks5://' internal/manager/server.go; then
  echo "live-console-smoke: WebUI source contains control-plane secret material" >&2
  exit 1
fi

echo "live-console-smoke: passed"
