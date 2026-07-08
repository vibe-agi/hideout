#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

tmp_home="$(mktemp -d "${TMPDIR:-/tmp}/hideout-command-adapter-smoke.XXXXXX")"
cleanup() {
  rm -rf "$tmp_home"
}
trap cleanup EXIT

go test -count=1 ./internal/cmdadapter ./internal/cmdproxy ./internal/broker ./internal/manager

go build -o "$tmp_home/hideout-shim" ./cmd/hideout-shim

HIDEOUT_STORE_ROOT="$tmp_home" HIDEOUT_SHIM_PATH="$tmp_home/hideout-shim" go run ./cmd/hideout profile init smoke >/dev/null
HIDEOUT_STORE_ROOT="$tmp_home" HIDEOUT_SHIM_PATH="$tmp_home/hideout-shim" go run ./cmd/hideout profile command-adapter smoke add-builtin-root-sensitive >"$tmp_home/add.out"
jq -e '
  .version == "hideout.command-adapter-plan/v1" and
  .applied == true and
  .plan.adapterId == "root-sensitive" and
  .plan.builtin == "root-sensitive" and
  (.plan.commands | index("sudo")) and
  (.plan.allowedProposalCapabilities | index("guest.privilege.plan"))
' "$tmp_home/add.out" >/dev/null

HIDEOUT_STORE_ROOT="$tmp_home" HIDEOUT_SHIM_PATH="$tmp_home/hideout-shim" go run ./cmd/hideout profile command-adapter smoke list >"$tmp_home/list.out"
jq -e '
  .profile == "smoke" and
  (.adapters[] | select(.id == "root-sensitive" and .enabled == true and .builtin == "root-sensitive"))
' "$tmp_home/list.out" >/dev/null

set +e
HIDEOUT_STORE_ROOT="$tmp_home" HIDEOUT_SHIM_PATH="$tmp_home/hideout-shim" \
  go run ./cmd/hideout run --profile smoke --backend native --allow-weak-isolation -- sudo apt install nodejs \
  >"$tmp_home/run.out" 2>"$tmp_home/run.err"
run_rc=$?
set -e
if [ "$run_rc" -eq 0 ]; then
  echo "command-adapter-smoke: root-sensitive command unexpectedly succeeded" >&2
  exit 1
fi
if ! rg -q "root-sensitive package-manager command captured as target intent" "$tmp_home/run.err"; then
  echo "command-adapter-smoke: root-sensitive stderr evidence missing" >&2
  cat "$tmp_home/run.err" >&2
  exit 1
fi
audit_path="$(find "$tmp_home/sessions" -name audit.jsonl -print | head -n 1)"
if [ -z "$audit_path" ]; then
  echo "command-adapter-smoke: audit log missing" >&2
  exit 1
fi
jq -e '
  select(.action == "broker.request") |
  .details.requestedAction == "command.adapter" and
  .details.adapterId == "root-sensitive" and
  .details.outcome == "proposeCapability" and
  .details.status == "proposal-unavailable" and
  .details.separationStatus == "unknown" and
  (.details.nonClaim | test("does not claim guest-root containment")) and
  .details.proposal.capability == "guest.privilege.plan"
' "$audit_path" >/dev/null
jq -e '
  select(.action == "target.root_attempt") |
  .details.command == "sudo" and
  .details.separationStatus == "unknown" and
  (.details.nonClaim | test("does not claim guest-root containment"))
' "$audit_path" >/dev/null

if rg -n "008 (blocks|enforces|provides) root|008 provides root containment|008 enforces root containment|command adapter.*root containment" \
  specs/008-command-capability-adapters docs | rg -v "not claim|0 automated|no claim|never claims|must not" >/dev/null; then
  echo "command-adapter-smoke: 008 docs must not claim root containment" >&2
  exit 1
fi

echo "command-adapter-smoke: passed"
