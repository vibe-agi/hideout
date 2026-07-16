#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
. "$ROOT/scripts/lib/daemon-temp.sh"

tmp_home="$(mktemp -d "${TMPDIR:-/tmp}/hideout-adapter-pack-smoke.XXXXXX")"
store="$(hideout_mktemp_daemon_store)"
hideout="$tmp_home/hideout"
cleanup() {
  HIDEOUT_STORE_ROOT="$store" "$hideout" daemon stop >/dev/null 2>&1 || true
  rm -rf "$tmp_home" "$store"
}
trap cleanup EXIT

go test -count=1 ./internal/adapterpack ./internal/cmdadapter ./internal/broker ./internal/manager ./internal/app
go build -o "$hideout" ./cmd/hideout
go build -o "$tmp_home/hideout-shim" ./cmd/hideout-shim

pack_dir="$tmp_home/pack"
mkdir -p "$pack_dir"
cat >"$pack_dir/hideout.adapter-pack.json" <<'JSON'
{
  "schemaVersion": "hideout.adapter-pack/v1",
  "id": "example.pack",
  "version": "1.0.0",
  "description": "Example adapter pack",
  "adapters": [
    {
      "id": "tool",
      "entrypoint": "decideCommandAdapter",
      "script": "adapter.js",
      "commands": ["tool-x"],
      "allowedProposalCapabilities": ["guest.privilege.plan"],
      "description": "Deny unsafe tool invocation"
    }
  ],
  "tests": [
    {
      "id": "denies-unsafe",
      "adapterId": "tool",
      "context": {
        "command": {
          "name": "tool-x",
          "argv": ["tool-x", "--unsafe"],
          "cwd": "/workspace"
        }
      },
      "expect": {
        "outcome": "deny",
        "reasonContains": "blocked"
      }
    }
  ]
}
JSON
cat >"$pack_dir/adapter.js" <<'JS'
function decideCommandAdapter(ctx) {
  return {
    outcome: "deny",
    reason: "blocked by adapter pack",
    exitCode: 77,
    stderr: "adapter pack blocked " + ctx.command.name
  };
}
JS

HIDEOUT_STORE_ROOT="$store" HIDEOUT_SHIM_PATH="$tmp_home/hideout-shim" "$hideout" profile init smoke >/dev/null
HIDEOUT_STORE_ROOT="$store" HIDEOUT_SHIM_PATH="$tmp_home/hideout-shim" "$hideout" adapter-pack install --path "$pack_dir" >"$tmp_home/install.out"
revision_id="$(jq -r '.revision.revisionId' "$tmp_home/install.out")"
source_digest="$(jq -r '.revision.sourceDigest' "$tmp_home/install.out")"
jq -e '
  .version == "hideout.adapter-pack-plan/v1" and
  .applied == true and
  .entry.packId == "example.pack" and
  .revision.validationStatus == "passed"
' "$tmp_home/install.out" >/dev/null

HIDEOUT_STORE_ROOT="$store" HIDEOUT_SHIM_PATH="$tmp_home/hideout-shim" "$hideout" adapter-pack list >"$tmp_home/list.out"
jq -e '.adapterPacks[] | select(.packId == "example.pack" and .state == "installed")' "$tmp_home/list.out" >/dev/null

set +e
HIDEOUT_STORE_ROOT="$store" HIDEOUT_SHIM_PATH="$tmp_home/hideout-shim" \
  "$hideout" adapter-pack enable --profile smoke --pack example.pack --revision "$revision_id" --adapter tool --id pack-tool \
  >"$tmp_home/enable-before-test.out" 2>"$tmp_home/enable-before-test.err"
enable_before_rc=$?
set -e
if [ "$enable_before_rc" -eq 0 ]; then
  echo "adapter-pack-smoke: enable succeeded before pack tests passed" >&2
  exit 1
fi
grep -q "tests must pass" "$tmp_home/enable-before-test.err"

HIDEOUT_STORE_ROOT="$store" HIDEOUT_SHIM_PATH="$tmp_home/hideout-shim" "$hideout" adapter-pack test --revision "$revision_id" example.pack >"$tmp_home/test.out"
jq -e '.test.status == "passed" and .test.passed == 1' "$tmp_home/test.out" >/dev/null

HIDEOUT_STORE_ROOT="$store" HIDEOUT_SHIM_PATH="$tmp_home/hideout-shim" \
  "$hideout" adapter-pack enable --profile smoke --pack example.pack --revision "$revision_id" --adapter tool --id pack-tool \
  >"$tmp_home/enable.out"
jq -e --arg revision "$revision_id" '
  .applied == true and
  .plan.profile == "smoke" and
  .plan.packId == "example.pack" and
  .plan.revisionId == $revision and
  .plan.commandAdapterId == "pack-tool"
' "$tmp_home/enable.out" >/dev/null

jq -e --arg revision "$revision_id" --arg digest "$source_digest" '
  .commandAdapters.adapters["pack-tool"].packId == "example.pack" and
  .commandAdapters.adapters["pack-tool"].packRevisionId == $revision and
  .commandAdapters.adapters["pack-tool"].packAdapterId == "tool" and
  .commandAdapters.adapters["pack-tool"].packLockDigest == $digest
' "$store/profiles/smoke/profile.json" >/dev/null

set +e
HIDEOUT_STORE_ROOT="$store" HIDEOUT_SHIM_PATH="$tmp_home/hideout-shim" \
  "$hideout" run --profile smoke --backend native --allow-weak-isolation -- tool-x --unsafe \
  >"$tmp_home/run.out" 2>"$tmp_home/run.err"
run_rc=$?
set -e
if [ "$run_rc" -eq 0 ]; then
  echo "adapter-pack-smoke: adapter-routed command unexpectedly succeeded" >&2
  exit 1
fi
grep -q "adapter pack blocked tool-x" "$tmp_home/run.err"

HIDEOUT_STORE_ROOT="$store" HIDEOUT_SHIM_PATH="$tmp_home/hideout-shim" "$hideout" adapter-pack revoke example.pack >"$tmp_home/revoke.out"
jq -e '.applied == true and .entry.state == "revoked"' "$tmp_home/revoke.out" >/dev/null

set +e
HIDEOUT_STORE_ROOT="$store" HIDEOUT_SHIM_PATH="$tmp_home/hideout-shim" \
  "$hideout" doctor --profile smoke --backend native \
  >"$tmp_home/doctor.out" 2>"$tmp_home/doctor.err"
doctor_rc=$?
set -e
if [ "$doctor_rc" -eq 0 ]; then
  echo "adapter-pack-smoke: revoked pack did not make broker check fail closed" >&2
  exit 1
fi
if ! grep -q "revoked" "$tmp_home/doctor.out" "$tmp_home/doctor.err"; then
  cat "$tmp_home/doctor.out" "$tmp_home/doctor.err" >&2
  exit 1
fi

echo "adapter-pack-smoke: passed"
