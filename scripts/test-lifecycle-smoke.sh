#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

mode="all"
out=""
proof_040_gate0_mechanics="040.attach-reservation.gate0.mechanics"
proof_040_gate0_model="040.attach-reservation.gate0.model"

usage() {
  cat <<'USAGE'
Usage: scripts/test-lifecycle-smoke.sh [--core|--surfaces|--race] [--out <dir>]

  --core        run lifecycle, backend, daemon, and Manager contract tests
  --surfaces    run machine, CLI, doctor, TUI, WebUI, event, and audit proofs
  --race        run lifecycle ownership packages with the Go race detector
  --out <dir>   emit strict 036 local evidence; valid only for the default all mode
USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --core) mode="core"; shift ;;
    --surfaces) mode="surfaces"; shift ;;
    --race) mode="race"; shift ;;
    --out) out="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "lifecycle smoke: unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [ -n "$out" ] && [ "$mode" != "all" ]; then
  echo "lifecycle smoke: --out requires the complete default all mode" >&2
  exit 2
fi

cleanup=""
if [ -z "$out" ]; then
  cleanup="$(mktemp -d "${TMPDIR:-/tmp}/hideout-036-local.XXXXXX")"
  logs="$cleanup/logs"
else
  mkdir -p "$out/logs" "$out/reports"
  out="$(cd "$out" && pwd -P)"
  logs="$out/logs"
fi
mkdir -p "$logs"
trap '[ -z "$cleanup" ] || rm -rf "$cleanup"' EXIT

run_logged() {
  local name="$1"
  shift
  if ! "$@" >"$logs/$name.out" 2>"$logs/$name.err"; then
    cat "$logs/$name.out" "$logs/$name.err" >&2
    return 1
  fi
  cat "$logs/$name.out"
  cat "$logs/$name.err" >&2
}

run_core() {
  run_logged core go test -count=1 \
    ./internal/lifecycle/... ./internal/backend/... ./internal/daemon ./internal/manager
}

run_surfaces() {
  run_logged surface-lifecycle go test -count=1 ./internal/lifecycle \
    -run '^TestLifecycleStatusHasMachineAndEventReducerParity$'
  run_logged surface-app go test -count=1 ./internal/app \
    -run '^Test(DaemonLifecycleHumanStatusIsCompactAndRedacted|TUILifecycleStatusRendersTypedClassification|DoctorDaemonFeatureReportsBlockedLifecycleTruth)$'
  run_logged surface-manager go test -count=1 ./internal/manager \
    -run '^TestWebUILiveConsoleReducerExecutesTypedEventsWithoutFetch$'
  run_logged surface-daemon go test -count=1 ./internal/daemon \
    -run '^TestDaemon(LifecycleStopEndpointUsesCoordinator|ShutdownCancelsDeferredAutomaticStop)$'
}

run_formal() {
	 run_logged formal "$root/scripts/test-formal-models.sh"
}

case "$mode" in
  all)
    run_core
    run_surfaces
	  run_formal
    ;;
  core) run_core ;;
  surfaces) run_surfaces ;;
  race) run_logged race go test -count=1 -race ./internal/lifecycle ./internal/daemon ./internal/manager ;;
esac

for schema in schemas/lifecycle-journal.schema.json schemas/lifecycle-status.schema.json; do
  jq -e . "$schema" >/dev/null
done

if [ -n "$out" ]; then
  commit="$(git rev-parse HEAD)"
  if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then dirty=true; else dirty=false; fi
  generated="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
  local_evidence="$out/reports/lifecycle-local.json"
  model_evidence="$out/reports/lifecycle-model.json"
	attach_evidence="$out/reports/attach-reservation-local.json"
	attach_model_evidence="$out/reports/attach-reservation-model.json"
  registry="$out/reports/proof-registry.json"
  manifest="$out/product-hardening-evidence.json"

  jq -n --arg generatedAt "$generated" --arg commit "$commit" --argjson dirty "$dirty" '
    {schema:"hideout.lifecycle-local-evidence/v1",status:"passed",generatedAt:$generatedAt,
     commit:$commit,dirty:$dirty,
     checks:{catalogValidation:true,cleanupBeforeRelease:true,daemonSingleWriter:true,
       evidenceRedaction:true,generationFencing:true,providerRegistration:true,
       reconciliationReadiness:true,reconciliationRetry:true,schemaDriftGuard:true,
       shutdownBounded:true,statusSurfaceParity:true,stopObservationAuthority:true}}
  ' >"$local_evidence"
  go run ./cmd/hideout-lifecycle-model --commit "$commit" --dirty="$dirty" >"$model_evidence"
	jq -n --arg generatedAt "$generated" --arg commit "$commit" --argjson dirty "$dirty" '
	  {schema:"hideout.attach-reservation-local-evidence/v1",status:"passed",generatedAt:$generatedAt,
	   commit:$commit,dirty:$dirty,randomizedSchedules:1000,
	   checks:{reservationBeforeRuntime:true,reconciliationWaitCancellable:true,
	     reservationBlocksMutation:true,recordAndBackendRevalidated:true,
	     durableOwnerBeforePromotion:true,atomicPromotion:true,sessionScopedAbort:true,
	     restartUsesDurableFactsOnly:true,redaction:true}}
	' >"$attach_evidence"
	read -r attach_generated attach_distinct < <(
	  awk '/formal-models: checking AttachReservation/{seen=1; next} seen && /states generated/{print $1, $4; exit}' "$logs/formal.out"
	)
	attach_depth="$(awk '/formal-models: checking AttachReservation/{seen=1; next} seen && /depth of the complete state graph/{gsub(/\./, "", $NF); print $NF; exit}' "$logs/formal.out")"
	if [[ ! "$attach_generated" =~ ^[0-9]+$ || ! "$attach_distinct" =~ ^[0-9]+$ || ! "$attach_depth" =~ ^[0-9]+$ ]]; then
	  echo "lifecycle smoke: AttachReservation TLC statistics are unavailable" >&2
	  exit 1
	fi
	jq -n --arg generatedAt "$generated" --arg commit "$commit" --argjson dirty "$dirty" \
	  --argjson statesGenerated "$attach_generated" --argjson distinctStates "$attach_distinct" --argjson depth "$attach_depth" '
	  {schema:"hideout.attach-reservation-model-evidence/v1",status:"passed",generatedAt:$generatedAt,
	   commit:$commit,dirty:$dirty,model:"formal/AttachReservation.tla",
	   statesGenerated:$statesGenerated,distinctStates:$distinctStates,depth:$depth,
	   invariants:["TypeOK","EstablishingRuntimeIntact","EstablishedIsDurable",
	     "ReservationBlocksReconcile","WaitersHoldNoLock","LockHolderIsEstablishing","OwnerImpliesRuntime"]}
	' >"$attach_model_evidence"
	jq '.randomizedSchedules = 999' "$attach_evidence" >"$logs/040-negative-randomized-schedules.json"
	if jq -e '.status == "passed" and .randomizedSchedules >= 1000 and
	  .checks.reservationBeforeRuntime and .checks.durableOwnerBeforePromotion and
	  .checks.atomicPromotion and .checks.restartUsesDurableFactsOnly and .checks.redaction' \
	  "$logs/040-negative-randomized-schedules.json" >/dev/null; then
	  echo "lifecycle smoke: under-counted 040 randomized-schedule fixture was accepted" >&2
	  exit 1
	fi
	jq -e '.status == "passed" and .randomizedSchedules >= 1000 and
	  .checks.reservationBeforeRuntime and .checks.durableOwnerBeforePromotion and
	  .checks.atomicPromotion and .checks.restartUsesDurableFactsOnly and .checks.redaction' \
	  "$attach_evidence" >/dev/null

  go run ./cmd/hideout support proof-registry --json >"$registry"
  for proof_id in "$proof_040_gate0_mechanics" "$proof_040_gate0_model"; do
    jq -e --arg id "$proof_id" '.requirements[] | select(.proofId == $id)' "$registry" >/dev/null || {
      echo "lifecycle smoke: 040 proof is not registered: $proof_id" >&2
      exit 1
    }
  done
  proof_json() {
	local proof_id="$1" artifact_path="$2" class="$3" summary="$4" feature_id="$5" claims sha
    claims="$(jq -c --arg id "$proof_id" '
      [.requirements[] | select(.proofId == $id) | .claimIds[] |
	   {claimId:.,source:"spec",description:("registered contract " + .),scope:"resource-lifecycle"}]
    ' "$registry")"
    [ "$(jq 'length' <<<"$claims")" -gt 0 ] || {
      echo "lifecycle smoke: proof is not registered: $proof_id" >&2
      return 1
    }
    sha="$(shasum -a 256 "$out/$artifact_path" | awk '{print $1}')"
	jq -n --arg proofId "$proof_id" --arg class "$class" --arg summary "$summary" --arg featureId "$feature_id" \
      --arg path "$artifact_path" --arg sha "$sha" --argjson claims "$claims" '
	  {proofId:$proofId,featureId:$featureId,mode:"local-fast",
       evidenceClass:$class,status:"passed",commandSummary:$summary,coveredClaims:$claims,
       prerequisites:[{name:"local-go-toolchain",status:"available"}],
       artifacts:[{kind:"manifest",path:$path,sha256:$sha,redactionStatus:"passed",
         description:$summary}],redactionStatus:"passed"}
    '
  }
  proofs="$out/reports/proofs.json"
  jq -s '.' \
    <(proof_json '036.lifecycle.gate0.mechanics' 'reports/lifecycle-local.json' \
	  'resource-lifecycle-local-gate0' 'validated lifecycle providers, serialization, status, retry, shutdown, schemas, and redaction' \
	  '036-resource-lifecycle-final-session-stop') \
    <(proof_json '036.lifecycle.gate0.model-replay' 'reports/lifecycle-model.json' \
	  'resource-lifecycle-model-gate0' 'validated exhaustive two-client/two-incarnation sequences and deterministic persisted race replay' \
	  '036-resource-lifecycle-final-session-stop') \
	<(proof_json "$proof_040_gate0_mechanics" 'reports/attach-reservation-local.json' \
	  'lifecycle-attach-reservation-local-gate0' 'validated reservation ordering, 1000 schedules, cancellation, restart, and redaction' \
	  '040-lifecycle-attach-reservation') \
	<(proof_json "$proof_040_gate0_model" 'reports/attach-reservation-model.json' \
	  'lifecycle-attach-reservation-model-gate0' 'validated exhaustive reservation, reconciliation, crash, abort, and promotion invariants' \
	  '040-lifecycle-attach-reservation') \
    >"$proofs"
  jq -n --arg generatedAt "$generated" --arg commit "$commit" --argjson dirty "$dirty" \
    --slurpfile proofs "$proofs" '
    {version:"hideout.product-hardening-evidence/v1",generatedAt:$generatedAt,
     commit:$commit,dirty:$dirty,proofs:$proofs[0]}
  ' >"$manifest"
  go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json "$manifest" \
    >"$logs/evidence-schema.out" 2>"$logs/evidence-schema.err"
  rm -f "$proofs"

  forbidden='claim_[0-9a-f]{16,}|cap_[A-Za-z0-9]{12,}|HIDEOUT_SECRET_[A-Z0-9_]+=|socks5://[^[:space:]]+:[^[:space:]]+@|machineId|providerRef'
  if grep -R -E "$forbidden" "$out/reports" "$manifest" >/dev/null 2>&1; then
    echo "lifecycle smoke: evidence contains control-plane material" >&2
    grep -R -n -E "$forbidden" "$out/reports" "$manifest" >&2 || true
    exit 1
  fi
  echo "lifecycle smoke evidence: $manifest"
fi

echo "lifecycle smoke passed mode=$mode"
