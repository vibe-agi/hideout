#!/usr/bin/env bash
set -euo pipefail

root="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
cd "$root"

out="$root/.artifacts/045/recovery"

usage() {
  printf '%s\n' \
    "Usage: scripts/gates/recovery.sh [--out DIR]" \
    "" \
    "Runs the durable-operation crash matrix, production refinement traces," \
    "targeted race checks, and negative trace mutants. Evidence is local only."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --out)
      if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
        printf 'recovery-gate: --out requires a directory\n' >&2
        exit 2
      fi
      out="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'recovery-gate: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

for command in go jq; do
  if ! command -v "$command" >/dev/null 2>&1; then
    printf 'recovery-gate: missing required command: %s\n' "$command" >&2
    exit 1
  fi
done

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  printf 'recovery-gate: missing shasum or sha256sum\n' >&2
  return 127
}

mkdir -p "$out/mutations"
chmod 0700 "$out" "$out/mutations"

unit_log="$out/unit.log"
race_log="$out/race.log"
positive_pattern='TestOperationRecoveryCrashMatrix|TestOperationRecoveryTraceRefinesOperatorConfigurationModel|TestOperationRollbackTraceRefinesOperatorConfigurationModel|TestOperationRecoveryRefinementMutationJudge|TestProfileTransactionCrashBoundariesResumeWithoutDuplicateEffect|TestProfileTransactionResponseLossRetryReplaysTerminalResult|TestStartupOperationRecovery|TestDaemonStartReconcilesAccepted'
race_pattern='TestOperationRecoveryCrashMatrix|TestOperationRecoveryTraceRefinesOperatorConfigurationModel|TestOperationRollbackTraceRefinesOperatorConfigurationModel|TestOperationRecoveryRefinementMutationJudge|TestStartupOperationRecovery|TestDaemonStartReconcilesAccepted'

printf 'recovery-gate: running crash matrix and production refinement traces\n'
go test ./internal/manager ./internal/daemon \
  -run "$positive_pattern" \
  -count=1 2>&1 | tee "$unit_log"
chmod 0600 "$unit_log"

printf 'recovery-gate: running targeted race traces\n'
go test -race ./internal/manager ./internal/daemon \
  -run "$race_pattern" \
  -count=1 2>&1 | tee "$race_log"
chmod 0600 "$race_log"

mutations='[]'
for mutant in \
  replay-running-effect \
  success-without-proof \
  duplicate-terminal-event; do
  mutation_log="$out/mutations/$mutant.log"
  printf 'recovery-gate: proving mutant is killed: %s\n' "$mutant"
  set +e
  env "HIDEOUT_RECOVERY_TRACE_MUTATION=$mutant" \
    go test ./internal/manager \
    -run '^TestOperationRecoveryRefinementMutationJudge$' \
    -count=1 >"$mutation_log" 2>&1
  mutation_status=$?
  set -e
  chmod 0600 "$mutation_log"
  if [ "$mutation_status" -eq 0 ]; then
    printf 'recovery-gate: mutant survived: %s\n' "$mutant" >&2
    exit 1
  fi
  if ! grep -Fq "recovery-mutation-fixture=$mutant" "$mutation_log" ||
    ! grep -Fq 'recovery invariant violated:' "$mutation_log"; then
    printf \
      'recovery-gate: mutant %s failed without reaching its invariant judge\n' \
      "$mutant" >&2
    exit 1
  fi
  mutations="$(
    jq -c \
      --arg id "$mutant" \
      --arg log "mutations/$mutant.log" \
      --arg sha256 "$(sha256_file "$mutation_log")" \
      '. + [{
        id: $id,
        result: "killed",
        log: $log,
        sha256: $sha256
      }]' <<<"$mutations"
  )"
done

source_commit="$(git rev-parse HEAD)"
if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then
  source_dirty=true
else
  source_dirty=false
fi

jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" \
  --arg unitSHA256 "$(sha256_file "$unit_log")" \
  --arg raceSHA256 "$(sha256_file "$race_log")" \
  --argjson mutations "$mutations" \
  '{
    schema: "hideout.recovery-gate-evidence/v1",
    generatedAt: $generatedAt,
    source: {
      commit: $commit,
      dirty: $dirty
    },
    result: "passed",
    crashMatrix: {
      boundaries: [
        "persist", "claim", "stage", "activate",
        "proof", "commit", "event", "response"
      ],
      sides: ["before", "after"],
      points: 16
    },
    checks: {
      unit: {
        log: "unit.log",
        sha256: $unitSHA256
      },
      race: {
        log: "race.log",
        sha256: $raceSHA256
      }
    },
    mutationProofs: $mutations,
    provedAssertions: [
      "unconfirmed work never gains provider authority",
      "running effects are observed rather than blindly replayed",
      "terminal success requires durable effect evidence",
      "exact response replay does not repeat provider effects",
      "terminal operation events are emitted at most once"
    ],
    claimBoundary:
      "Local crash/refinement evidence only; this is not real Lima or release-candidate proof."
  }' >"$out/summary.json"
chmod 0600 "$out/summary.json"

if ! jq -e '
  .schema == "hideout.recovery-gate-evidence/v1" and
  .result == "passed" and
  .crashMatrix.points == 16 and
  (.mutationProofs | length) == 3 and
  all(.mutationProofs[]; .result == "killed")
' "$out/summary.json" >/dev/null; then
  printf 'recovery-gate: generated evidence failed validation\n' >&2
  exit 1
fi

printf \
  'recovery-gate: passed crashPoints=16 mutations=3 evidence=%s\n' \
  "$out/summary.json"
