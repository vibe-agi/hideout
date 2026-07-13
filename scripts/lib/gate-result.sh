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

gate_sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    echo "gate-result: missing shasum or sha256sum" >&2
    return 127
  fi
}

runtime_evidence_markers() {
  local receipt="${1:-}"
  if [ -z "$receipt" ] || [ ! -f "$receipt" ]; then
    echo "runtime_evidence_markers: receipt path is required" >&2
    return 2
  fi
  local commit="${HIDEOUT_RUNTIME_BUILD_COMMIT:-}"
  local dirty="${HIDEOUT_RUNTIME_BUILD_DIRTY:-}"
  local build_provenance="${HIDEOUT_RUNTIME_BUILD_PROVENANCE:-}"
  if [ -n "$build_provenance" ]; then
    if [ ! -f "$build_provenance" ]; then
      echo "runtime_evidence_markers: build provenance does not exist: $build_provenance" >&2
      return 2
    fi
    if ! jq -e '
      .schema == "hideout.runtime-build-provenance/v1" and
      (.source.commit | test("^[a-f0-9]{12,40}$")) and
      (.source.dirty | type == "boolean") and
      (.output.sha256 | test("^[a-f0-9]{64}$"))
    ' "$build_provenance" >/dev/null; then
      echo "runtime_evidence_markers: build provenance is incomplete or invalid" >&2
      return 2
    fi
    local built_digest receipt_digest
    commit="$(jq -r '.source.commit' "$build_provenance")"
    dirty="$(jq -r '.source.dirty' "$build_provenance")"
    built_digest="$(jq -r '.output.sha256' "$build_provenance")"
    receipt_digest="$(jq -r '.provenance.artifactSHA256 // empty' "$receipt")"
    if [ "$built_digest" != "$receipt_digest" ]; then
      echo "runtime_evidence_markers: receipt artifact digest does not match build provenance" >&2
      return 2
    fi
    echo "runtime_build_provenance_sha256=$(gate_sha256_file "$build_provenance")"
  else
    if [ -z "$commit" ]; then
      commit="$(git rev-parse --short=12 HEAD)"
    fi
    if [ -z "$dirty" ]; then
      if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then dirty=true; else dirty=false; fi
    fi
  fi
  case "$dirty" in true | false) ;; *) echo "runtime_evidence_markers: dirty must be true or false" >&2; return 2 ;; esac
  jq -r \
    --arg commit "$commit" \
    --argjson dirty "$dirty" \
    '"runtime_family=" + .provenance.family,
     "runtime_revision=" + .provenance.revision,
     "runtime_artifact_sha256=" + .provenance.artifactSHA256,
     "runtime_environment_id=" + .environmentId,
     "runtime_host_os=" + .provenance.hostOS,
     "runtime_host_arch=" + .provenance.hostArch,
     "runtime_guest_arch=" + .provenance.guestArch,
     "runtime_build_commit=" + $commit,
     "runtime_build_dirty=" + ($dirty|tostring)' \
    "$receipt"
}

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

  local runtime_json="null"
  local runtime_declared=0
  for value in \
    "${HIDEOUT_RUNTIME_EVIDENCE_FAMILY:-}" \
    "${HIDEOUT_RUNTIME_EVIDENCE_REVISION:-}" \
    "${HIDEOUT_RUNTIME_EVIDENCE_ARTIFACT_SHA256:-}" \
    "${HIDEOUT_RUNTIME_EVIDENCE_ENVIRONMENT_ID:-}" \
    "${HIDEOUT_RUNTIME_EVIDENCE_BUILD_COMMIT:-}"; do
    [ -n "$value" ] && runtime_declared=1
  done
  if [ "${HIDEOUT_RUNTIME_EVIDENCE_REQUIRED:-0}" = "1" ]; then
    runtime_declared=1
  fi
  if [ "$runtime_declared" -eq 1 ]; then
    local runtime_family="${HIDEOUT_RUNTIME_EVIDENCE_FAMILY:-}"
    local runtime_revision="${HIDEOUT_RUNTIME_EVIDENCE_REVISION:-}"
    local runtime_sha="${HIDEOUT_RUNTIME_EVIDENCE_ARTIFACT_SHA256:-}"
    local runtime_environment="${HIDEOUT_RUNTIME_EVIDENCE_ENVIRONMENT_ID:-}"
    local runtime_host_os="${HIDEOUT_RUNTIME_EVIDENCE_HOST_OS:-}"
    local runtime_host_arch="${HIDEOUT_RUNTIME_EVIDENCE_HOST_ARCH:-}"
    local runtime_guest_arch="${HIDEOUT_RUNTIME_EVIDENCE_GUEST_ARCH:-}"
    local runtime_commit="${HIDEOUT_RUNTIME_EVIDENCE_BUILD_COMMIT:-}"
    local runtime_dirty="${HIDEOUT_RUNTIME_EVIDENCE_BUILD_DIRTY:-}"
    if [ -z "$runtime_family" ] || [ -z "$runtime_revision" ] || [ -z "$runtime_environment" ] ||
      [ -z "$runtime_host_os" ] || [ -z "$runtime_host_arch" ] || [ -z "$runtime_guest_arch" ] ||
      [ -z "$runtime_commit" ] || ! printf '%s' "$runtime_sha" | grep -Eq '^[0-9a-f]{64}$'; then
      echo "emit_gate_result: runtime evidence requires complete family/revision/digest/environment/architecture/commit fields" >&2
      return 2
    fi
    case "$runtime_dirty" in
      true | false) ;;
      *) echo "emit_gate_result: runtime image build dirty state must be true or false" >&2; return 2 ;;
    esac
    runtime_json="$(jq -n \
      --arg schema "hideout.runtime-evidence-binding/v1" \
      --arg family "$runtime_family" \
      --arg revision "$runtime_revision" \
      --arg artifactSHA256 "$runtime_sha" \
      --arg environmentId "$runtime_environment" \
      --arg hostOS "$runtime_host_os" \
      --arg hostArch "$runtime_host_arch" \
      --arg guestArch "$runtime_guest_arch" \
      --arg buildCommit "$runtime_commit" \
      --argjson buildDirty "$runtime_dirty" \
      '{schema:$schema,family:$family,revision:$revision,artifactSHA256:$artifactSHA256,
        environmentId:$environmentId,hostOS:$hostOS,hostArch:$hostArch,guestArch:$guestArch,
        buildCommit:$buildCommit,buildDirty:$buildDirty}')"
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
    --argjson runtime "$runtime_json" \
    '{
      id: $id,
      backend: $backend,
      result: $result,
      reason: $reason,
      auditPath: $auditPath,
      boundarySummary: $boundarySummary,
      environmentName: $environmentName
    } + if $runtime == null then {} else {runtime: $runtime} end' >"$gates_dir/$id.json"
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
