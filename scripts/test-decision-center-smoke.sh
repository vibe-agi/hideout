#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

tmp_home="$(mktemp -d "${TMPDIR:-/tmp}/hideout-decision-center-smoke.XXXXXX")"
cleanup() {
  rm -rf "$tmp_home"
}
trap cleanup EXIT

go test -count=1 ./internal/decision ./internal/manager ./internal/daemon ./internal/liveconsole ./internal/app

export HIDEOUT_STORE_ROOT="$tmp_home"

go run ./cmd/hideout profile init smoke >/dev/null
mkdir -p "$tmp_home/sessions/ses_decision"
cat >"$tmp_home/sessions/ses_decision/audit.jsonl" <<'JSONL'
{"time":"2026-07-08T00:00:00Z","session":"ses_decision","profile":"smoke","backend":"native","action":"host.open","decision":"allow","details":{"target":"https://example.com/share","note":"keep-me"}}
JSONL

local_out="$tmp_home/local-export.json"
go run ./cmd/hideout audit export --source audit --session ses_decision --out "$local_out" --acknowledge-full-fidelity >"$tmp_home/local.out"
test -f "$local_out"
go run ./cmd/hideout decision list --kind evidence.share --include-terminal >"$tmp_home/local-decisions.json"
jq -e 'length == 0' "$tmp_home/local-decisions.json" >/dev/null

share_out="$tmp_home/share-export.json"
go run ./cmd/hideout audit export --share --source audit --session ses_decision --out "$share_out" --acknowledge-full-fidelity >"$tmp_home/share.out"
test ! -e "$share_out"
share_decision="$(awk '/^share decision:/ {print $3}' "$tmp_home/share.out")"
test -n "$share_decision"
go run ./cmd/hideout decision inspect "$share_decision" >"$tmp_home/share-decision.json"
jq -e '.kind == "evidence.share" and .state == "pending" and (.providerRef.provider // "") == ""' "$tmp_home/share-decision.json" >/dev/null

go run ./cmd/hideout decision claim "$share_decision" >"$tmp_home/share-claim.json"
claim_token="$(jq -r '.claimToken' "$tmp_home/share-claim.json")"
test -n "$claim_token"
go run ./cmd/hideout decision approve --claim-token "$claim_token" "$share_decision" >"$tmp_home/share-approve.json"
jq -e '.status == "applied" and .decision == "allow"' "$tmp_home/share-approve.json" >/dev/null
test -f "$share_out"
grep -q "https://example.com/share" "$share_out"
if grep -q "claim_" "$share_out"; then
  echo "decision-center-smoke: share export leaked claim token" >&2
  exit 1
fi

go run ./cmd/hideout decision watch >"$tmp_home/watch.json"
jq -e '.status.terminalDecisions >= 1 and ([.decisions[].id] | index("'"$share_decision"'")) != null' "$tmp_home/watch.json" >/dev/null

echo "decision-center-smoke: passed"
