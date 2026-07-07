#!/usr/bin/env bash
# Shared per-gate result emission for isolation-evidence gates.
#
# When HIDEOUT_RELEASE_EVIDENCE_DIR is set, an isolation gate calls
# emit_gate_result to write a machine-readable result to
#   $HIDEOUT_RELEASE_EVIDENCE_DIR/gates/<id>.json
# with { id, result, reason, auditPath, boundarySummary, environmentName }.
# The release-dogfood manifest writer aggregates these files into
# isolationGates. When the evidence dir is unset the call is a no-op, so a
# gate's human-readable output is unchanged whether or not it is being
# aggregated. This is the only writer of gate result files: every isolation
# gate emits through it so the format stays identical across gates.
#
# Usage:
#   . "$ROOT/scripts/lib/gate-result.sh"
#   emit_gate_result <id> <backend> <result> [reason] [auditPath] [boundarySummary] [environmentName]
# where result is one of: passed | failed | not-run
# A not-run result requires a non-empty reason. Native MUST NOT appear as the
# backend for a passed isolation claim.

emit_gate_result() {
  local id="${1:-}"
  local backend="${2:-}"
  local result="${3:-}"
  local reason="${4:-}"
  local audit_path="${5:-}"
  local boundary_summary="${6:-}"
  local environment_name="${7:-}"

  if [ -z "$id" ] || [ -z "$backend" ] || [ -z "$result" ]; then
    echo "emit_gate_result: id, backend and result are required" >&2
    return 2
  fi
  case "$result" in
    passed | failed | not-run) ;;
    *)
      echo "emit_gate_result: invalid result '$result' (want passed|failed|not-run)" >&2
      return 2
      ;;
  esac
  if [ "$result" = "not-run" ] && [ -z "$reason" ]; then
    echo "emit_gate_result: not-run requires a reason" >&2
    return 2
  fi
  if [ "$result" = "passed" ] && [ "$backend" = "native" ]; then
    echo "emit_gate_result: native must not satisfy a passed isolation claim" >&2
    return 2
  fi

  # No evidence directory: aggregation is off, emit nothing.
  if [ -z "${HIDEOUT_RELEASE_EVIDENCE_DIR:-}" ]; then
    return 0
  fi

  local gates_dir="$HIDEOUT_RELEASE_EVIDENCE_DIR/gates"
  mkdir -p "$gates_dir"
  jq -n \
    --arg id "$id" \
    --arg backend "$backend" \
    --arg result "$result" \
    --arg reason "$reason" \
    --arg auditPath "$audit_path" \
    --arg boundarySummary "$boundary_summary" \
    --arg environmentName "$environment_name" \
    '{
      id: $id,
      backend: $backend,
      result: $result,
      reason: $reason,
      auditPath: $auditPath,
      boundarySummary: $boundarySummary,
      environmentName: $environmentName
    }' >"$gates_dir/$id.json"
}

# reconcile_isolation_gates <id>... — for each expected gate id that has no
# result file yet, emit a not-run result so every expected isolation gate is
# accounted for in the manifest (never silently absent). No-op when the evidence
# directory is unset.
reconcile_isolation_gates() {
  if [ -z "${HIDEOUT_RELEASE_EVIDENCE_DIR:-}" ]; then
    return 0
  fi
  local id
  for id in "$@"; do
    if [ ! -f "$HIDEOUT_RELEASE_EVIDENCE_DIR/gates/$id.json" ]; then
      emit_gate_result "$id" "lima" "not-run" "gate not run in this evidence bundle"
    fi
  done
}
