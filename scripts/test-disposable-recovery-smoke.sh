#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-disposable-recovery-gate0.XXXXXX")"
cleanup() {
  find "$tmp" -depth -delete 2>/dev/null || true
}
trap cleanup EXIT

go test -count=1 ./internal/environment \
  -run '^TestDisposable'
go test -count=1 ./internal/lifecycle \
  -run '^Test(Disposal|JournalDisposal|CoordinatorDisposal|DisposableRecovery)'
go test -count=1 ./internal/manager \
  -run '^Test(RecoverDisposable|ApplyRunDisposable|DisposableCleanup|RunServiceRemoveAndEphemeral)'
go test -count=1 ./internal/daemon \
  -run '^TestDaemon(RecoversDisposable|BoundsConcurrentDisposable|ShutdownInterruptsDisposable|ConvergesValidIntentOnly)'
go test -count=1 ./internal/productevidence \
  -run '^Test(ProofRegistryCovers042|DisposableRecoveryValidator)'

scripts/test-formal-models.sh >"$tmp/formal.out" 2>"$tmp/formal.err"
cat "$tmp/formal.out"
cat "$tmp/formal.err" >&2
grep -q 'formal-models: checking DisposableRecovery' "$tmp/formal.out"
grep -q 'formal-models: 7 models passed' "$tmp/formal.out"

read -r states_generated distinct_states < <(
  awk '
    /formal-models: checking DisposableRecovery/ { seen = 1; next }
    seen && /states generated, [0-9]+ distinct states found/ {
      print $1, $4
      exit
    }
  ' "$tmp/formal.out"
)
model_depth="$(
  awk '
    /formal-models: checking DisposableRecovery/ { seen = 1; next }
    seen && /depth of the complete state graph/ {
      gsub(/\./, "", $NF)
      print $NF
      exit
    }
  ' "$tmp/formal.out"
)"
if [[ ! "$states_generated" =~ ^[0-9]+$ ||
      ! "$distinct_states" =~ ^[0-9]+$ ||
      ! "$model_depth" =~ ^[0-9]+$ ||
      "$states_generated" -lt 1000 ||
      "$distinct_states" -lt 500 ||
      "$model_depth" -lt 10 ]]; then
  echo "disposable recovery smoke: TLC exploration was incomplete" >&2
  exit 1
fi

go run ./cmd/hideout support proof-registry --json >"$tmp/proof-registry.json"
jq -e '
  ([.requirements[] |
    select(.featureId == "042-disposable-orphan-recovery")] | length) == 5 and
  any(.requirements[];
    .proofId == "042.disposable-recovery.gate0.mechanics" and
    .layer == "gate0" and .requiredFor == "targeted-completion") and
  any(.requirements[];
    .proofId == "042.disposable-recovery.gate0.model" and
    .layer == "gate0" and .requiredFor == "targeted-completion") and
  any(.requirements[];
    .proofId == "042.disposable-recovery.real-gate2.recovery" and
    .layer == "real-gate" and .requiredFor == "release-candidate" and
    .runtimePolicy == "exact-real" and
    .artifactValidator == "disposable-recovery/v1") and
  any(.requirements[];
    .proofId == "042.disposable-recovery.real-gate2.not-run" and
    .requiredFor == "supporting-only" and .runtimePolicy == "none")
' "$tmp/proof-registry.json" >/dev/null

jq -e . schemas/lifecycle-journal.schema.json >/dev/null
jq -e . schemas/lifecycle-status.schema.json >/dev/null

printf 'disposable recovery Gate 0 passed (states=%s distinct=%s depth=%s; no real Lima claim)\n' \
  "$states_generated" "$distinct_states" "$model_depth"
