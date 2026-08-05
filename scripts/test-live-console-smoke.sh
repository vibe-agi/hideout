#!/usr/bin/env bash
set -euo pipefail

# 007/019/027/045 live-console smoke: canonical snapshot/event contracts,
# daemon-owned browser/TUI consumers, action-center routes, no hidden healthy
# polling, and control-plane redaction. This smoke follows the public UI owner;
# it must not assert implementation strings in the retired Manager WebUI.

ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

require_file() {
  local path="$1" description="$2"
  if [ ! -f "$path" ]; then
    printf 'live-console-smoke: missing %s: %s\n' "$description" "$path" >&2
    return 1
  fi
}

require_marker() {
  local path="$1" marker="$2" description="$3"
  if ! grep -Fq "$marker" "$path"; then
    printf 'live-console-smoke: %s is missing in %s\n' \
      "$description" "$path" >&2
    return 1
  fi
}

require_file schemas/live-console-seed.schema.json 'live-console seed schema'
require_file schemas/daemon-event.schema.json 'daemon event schema'
require_file schemas/operator-snapshot.schema.json 'operator snapshot schema'
jq empty \
  schemas/live-console-seed.schema.json \
  schemas/daemon-event.schema.json \
  schemas/operator-snapshot.schema.json

go test ./internal/liveconsole -run 'TestEventCatalog|TestReducer|TestNewStateFromOperatorSnapshot'
go test ./internal/daemon -run 'TestDaemonEndpoint|TestDaemonServesFull.*Route|TestEventBus|TestTerminalEvent|TestSubscribeEvents|TestLoopbackUIServesEventConsumingWebUI|TestOperatorSnapshotMaintainsDecisionBeforeTakingSequenceFence'
go test ./internal/manager -run 'TestManagerRoute|TestOperatorSnapshot|TestCoreExportEmitsLiveOperationEvents|TestCoreCloseRunSessionEmitsCleanupEvent'
go test ./internal/daemon/uiweb_assets -run 'TestBrowserDecisionAndNoticeClients|TestBrowserClientAndAppHaveNoHealthyStreamPolling|TestBrowserPanelAssetsUseBoundedManagerReadsAndEventRefresh|TestBrowserOverviewPutsAuthorityAndRequiredAreasInResponsiveHUD|TestBrowserEventV2Reducer'
go test ./internal/app -run 'TestTUILiveConsole'

require_marker internal/daemon/uiweb_assets/client.js \
  'handlers.event(JSON.parse(message.data))' \
  'daemon WebUI event-stream reducer binding'
require_marker internal/daemon/uiweb_assets/client.js \
  '"&since="' \
  'snapshot-to-stream sequence binding'
require_marker internal/daemon/uiweb_assets/client.js \
  'async function decisionClaim' \
  'authenticated decision claim client'
require_marker internal/daemon/uiweb_assets/client.js \
  'async function noticeAck' \
  'authenticated notice acknowledgement client'
require_marker internal/daemon/uiweb_assets/state.js \
  'function applyEvent(state, input)' \
  'browser canonical event reducer'
require_marker internal/daemon/uiweb_assets/app.js \
  'root.State.applyEvent(state, event)' \
  'browser event-driven rendering'
require_marker internal/daemon/uiweb_assets/app.js \
  'acknowledge.dataset.action = "ack-notice"' \
  'visible notice acknowledgement control'
require_marker internal/daemon/uiweb_assets/app.js \
  'review.dataset.action = "review-decision"' \
  'visible decision review control'
require_marker internal/daemon/uiweb_assets/index.html \
  'data-panel="overview"' \
  'operator overview panel'
require_marker internal/daemon/uiweb_assets/index.html \
  'data-panel="operations"' \
  'operation history panel'

if grep -Eq 'setInterval' \
    internal/daemon/uiweb_assets/client.js \
    internal/daemon/uiweb_assets/state.js \
    internal/daemon/uiweb_assets/app.js; then
  echo 'live-console-smoke: daemon WebUI must not use a healthy-stream polling timer' >&2
  exit 1
fi
if grep -Eq 'cap_[0-9a-f]{32,}|HIDEOUT_SECRET_[A-Z0-9_]+=socks5://' \
    internal/daemon/uiweb_assets/*.js; then
  echo 'live-console-smoke: daemon WebUI source contains control-plane secret material' >&2
  exit 1
fi

echo 'live-console-smoke: passed owner=daemon capability=action-center'
