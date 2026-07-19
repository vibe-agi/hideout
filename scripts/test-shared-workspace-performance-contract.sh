#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-shared-performance-contract.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT
baseline="$tmp/baseline"
control="$tmp/control"
candidate="$tmp/candidate"
output="$tmp/output"
mkdir -p "$baseline" "$control" "$candidate"
digest="$(printf fixture | shasum -a 256 | awk '{print $1}')"
printf '%s\n' "$digest" >"$baseline/fixture.sha256"
printf '%s\n' "$digest" >"$control/fixture.sha256"
printf '%s\n' "$digest" >"$candidate/fixture.sha256"

summary() {
  local root="$1" name="$2" samples="$3" median="$4" p95="$5"
  printf '{"samples":%s,"medianMs":%s,"p95Ms":%s}\n' "$samples" "$median" "$p95" >"$root/$name-summary.json"
}
summary "$baseline" first-byte 30 400 500
summary "$control" git-status 30 500 700
summary "$control" package-scan 30 1000 1200
summary "$candidate" git-status 30 900 1100
summary "$candidate" package-scan 30 2500 2700
summary "$candidate" first-byte 30 700 900
summary "$candidate" atomic-host-to-guest 30 20 40
summary "$candidate" atomic-guest-to-host 30 25 45
summary "$candidate" mount-ready 30 500 800
summary "$candidate" saturation-metadata 100 4 8
cat >"$candidate/correctness.json" <<'JSON'
{"schema":"hideout.shared-workspace-correctness/v1","hostCreateVisible":true,"targetCreateVisible":true,"hostAtomicReplaceVisible":true,"targetAtomicReplaceVisible":true,"renameVisible":true,"deleteVisible":true,"modeVisible":true,"flushDurable":true,"sameRootLocksConflict":true,"rootEscapeRejected":true,"symlinkEscapeRejected":true,"watcherStreamHealthy":true,"silentShortWrites":0,"falseSuccesses":0,"hostWatcherSamples":30,"targetWatcherSamples":30}
JSON
printf '{"teardownMs":900}\n' >"$candidate/saturation.json"

"$ROOT/scripts/lib/gate2-shared-workspace-performance.sh" "$baseline" "$control" "$candidate" "$output"
jq -e '.result == "passed" and .thresholdsPassed == true and .correctness.passed == true and
  ([.metrics[] | select(.id == "git-status" or .id == "package-scan") |
    .referenceKind] | all(. == "paired-static-virtiofs")) and
  ([.metrics[] | select(.id == "first-byte") | .referenceKind] |
    all(. == "retained-research-baseline"))' \
  "$output/shared-workspace-evaluation.json" >/dev/null

summary "$candidate" git-status 30 1100 1200
if "$ROOT/scripts/lib/gate2-shared-workspace-performance.sh" "$baseline" "$control" "$candidate" "$tmp/unpaired" >/dev/null 2>&1; then
  echo "shared-workspace performance contract ignored the paired static control" >&2
  exit 1
fi
summary "$candidate" git-status 30 900 1100

summary "$candidate" git-status 30 2001 2100
if "$ROOT/scripts/lib/gate2-shared-workspace-performance.sh" "$baseline" "$control" "$candidate" "$tmp/failing" >/dev/null 2>&1; then
  echo "shared-workspace performance contract accepted an exceeded fixed threshold" >&2
  exit 1
fi

summary "$candidate" git-status 29 500 700
if "$ROOT/scripts/lib/gate2-shared-workspace-performance.sh" "$baseline" "$control" "$candidate" "$tmp/undersampled" >/dev/null 2>&1; then
  echo "shared-workspace performance contract accepted an undersampled distribution" >&2
  exit 1
fi

echo "shared-workspace-performance-contract: passed"
