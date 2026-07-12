#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

usage() {
  cat <<'USAGE'
Usage:
  scripts/test-phase1.sh [--quick|--required|--all|--isolation-evidence|--release-candidate|--lima|--lima-real-run|--env-image|--proxy|--real-browser|--probes|--dogfood-cli|--operator-cli]

Modes:
  --quick         Run fast local gates: Gate 0, Gate 1, Gate 4 dry-run.
  --required      Run required automated gates without real browser launch:
                  Gate 0, Gate 1, Gate 2, Gate 3, Gate 4 dry-run.
  --all           Same as --required unless --real-browser is also set.
  --release-candidate
                  Run release-candidate gates: Gate 0-4, real browser,
                  operator-supplied proxy, capability probe smoke, and generic
                  CLI dogfood smoke.
  --lima          Include Gate 2 Lima E2E.
  --env-image     Run the declared-image boot gate variant (needs
                  HIDEOUT_ENV_IMAGE_URL and Lima).
  --lima-real-run
                  Include the supervised Lima real-run reference smoke after Gate 2.
  --proxy         Include Gate 3 hidden proxy.
  --real-browser  Run Gate 4 with real external URL browser launch.
  --probes        Run capability probe CLI smoke after product gates.
  --dogfood-cli  Run generic CLI dogfood smoke on Lima.
  --operator-cli Run operator-supplied real CLI smoke on Lima.
  -h, --help      Show this help.

Default:
  --quick
USAGE
}

run_gate() {
  local name="$1"
  shift
  echo "phase1: $name"
  "$@"
}

# Expected isolation gates the evidence bundle must account for.
ISOLATION_GATE_IDS="gate2-lima gate3-hidden-proxy gate4-host-escape env-image"
isolation_gate_failed=0

# run_isolation_gate <id> <name> <cmd...>
# When collecting evidence (HIDEOUT_RELEASE_EVIDENCE_DIR set) the gate runs
# non-fatally and the orchestrator records passed/failed for <id>, so a failed
# gate becomes an explicit "failed" entry instead of vanishing from the manifest.
# Otherwise it behaves like run_gate (fatal under set -e).
run_isolation_gate() {
  local id="$1" name="$2"
  shift 2
  echo "phase1: $name"
  if [ -z "${HIDEOUT_RELEASE_EVIDENCE_DIR:-}" ]; then
    "$@"
    return
  fi
  . scripts/lib/gate-result.sh
  # Capture the gate's output so the per-gate result carries real references:
  # the named environment it exercised and the retained evidence log / boundary
  # summary (the gate's own store is ephemeral, so these are the durable refs).
  local out; out="$(mktemp "${TMPDIR:-/tmp}/hideout-gate-out.XXXXXX")"
  local status=0
  if "$@" >"$out" 2>&1; then status=0; else status=$?; fi
  cat "$out"
  local env_name audit_ref boundary_ref
  env_name="$(grep -oE 'Hideout environment name: .+' "$out" | head -n1 | sed 's/^Hideout environment name: //')"
  audit_ref="$HIDEOUT_RELEASE_EVIDENCE_DIR/test-release-dogfood.log"
  grep -q 'Boundary Summary' "$out" && boundary_ref="boundary-summary:present" || boundary_ref=""
  local runtime_family runtime_revision runtime_sha runtime_environment runtime_host_os runtime_host_arch runtime_guest_arch runtime_commit runtime_dirty runtime_required
  runtime_family="$(sed -n 's/^runtime_family=//p' "$out" | tail -n1)"
  runtime_revision="$(sed -n 's/^runtime_revision=//p' "$out" | tail -n1)"
  runtime_sha="$(sed -n 's/^runtime_artifact_sha256=//p' "$out" | tail -n1)"
  runtime_environment="$(sed -n 's/^runtime_environment_id=//p' "$out" | tail -n1)"
  runtime_host_os="$(sed -n 's/^runtime_host_os=//p' "$out" | tail -n1)"
  runtime_host_arch="$(sed -n 's/^runtime_host_arch=//p' "$out" | tail -n1)"
  runtime_guest_arch="$(sed -n 's/^runtime_guest_arch=//p' "$out" | tail -n1)"
  runtime_commit="$(sed -n 's/^runtime_candidate_commit=//p' "$out" | tail -n1)"
  runtime_dirty="$(sed -n 's/^runtime_candidate_dirty=//p' "$out" | tail -n1)"
  runtime_required=0
  [ -n "$runtime_family" ] && runtime_required=1
  rm -f "$out"
  if [ "$status" -eq 0 ]; then
    HIDEOUT_RUNTIME_EVIDENCE_REQUIRED="$runtime_required" \
      HIDEOUT_RUNTIME_EVIDENCE_FAMILY="$runtime_family" \
      HIDEOUT_RUNTIME_EVIDENCE_REVISION="$runtime_revision" \
      HIDEOUT_RUNTIME_EVIDENCE_ARTIFACT_SHA256="$runtime_sha" \
      HIDEOUT_RUNTIME_EVIDENCE_ENVIRONMENT_ID="$runtime_environment" \
      HIDEOUT_RUNTIME_EVIDENCE_HOST_OS="$runtime_host_os" \
      HIDEOUT_RUNTIME_EVIDENCE_HOST_ARCH="$runtime_host_arch" \
      HIDEOUT_RUNTIME_EVIDENCE_GUEST_ARCH="$runtime_guest_arch" \
      HIDEOUT_RUNTIME_EVIDENCE_CANDIDATE_COMMIT="$runtime_commit" \
      HIDEOUT_RUNTIME_EVIDENCE_CANDIDATE_DIRTY="$runtime_dirty" \
      emit_gate_result "$id" "lima" "passed" "" "$audit_ref" "$boundary_ref" "$env_name"
  else
    emit_gate_result "$id" "lima" "failed" "gate exited $status" "$audit_ref" "$boundary_ref" "$env_name"
    isolation_gate_failed=1
  fi
}

print_plan() {
  echo "phase1-plan: Gate 0 static contract"
  echo "phase1-plan: Gate 1 native smoke"
  if [ "$include_lima" -eq 1 ]; then
    echo "phase1-plan: Gate 2 Lima E2E"
    if [ "$runtime_gate_mode" -eq 1 ]; then
      echo "phase1-plan: Gate 2 exact runtime family $runtime_gate_family"
    fi
  fi
  if [ "$include_env_image" -eq 1 ]; then
    echo "phase1-plan: env-image declared-image gate"
  fi
  if [ "$include_lima_real_run" -eq 1 ]; then
    echo "phase1-plan: Lima real-run reference smoke"
  fi
  if [ "$include_proxy" -eq 1 ]; then
    if [ "$require_operator_proxy" -eq 1 ]; then
      echo "phase1-plan: Gate 3 hidden proxy with operator-supplied proxy"
    else
      echo "phase1-plan: Gate 3 hidden proxy"
    fi
    if [ "$runtime_gate_mode" -eq 1 ]; then
      echo "phase1-plan: Gate 3 exact runtime family $runtime_gate_family"
    fi
  fi
  if [ "$real_browser" -eq 1 ]; then
    echo "phase1-plan: Gate 4 real browser launcher preflight"
    echo "phase1-plan: Gate 4 host escape boundary with real browser external URL"
  else
    echo "phase1-plan: Gate 4 host escape boundary dry-run"
  fi
  if [ "$include_probes" -eq 1 ]; then
    echo "phase1-plan: Capability probe CLI smoke"
  fi
  if [ "$include_dogfood_cli" -eq 1 ]; then
    echo "phase1-plan: Generic CLI dogfood smoke"
  fi
  if [ "$include_operator_cli" -eq 1 ]; then
    echo "phase1-plan: Operator-supplied real CLI smoke"
  fi
}

mode="quick"
include_lima=0
include_lima_real_run=0
include_env_image=0
include_proxy=0
real_browser=0
include_probes=0
include_dogfood_cli=0
include_operator_cli=0
require_operator_proxy=0
runtime_gate_mode=0
runtime_gate_family="${HIDEOUT_RELEASE_RUNTIME_FAMILY:-developer-standard}"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --quick)
      mode="quick"
      include_lima=0
      include_proxy=0
      ;;
    --required|--all)
      mode="required"
      include_lima=1
      include_proxy=1
      ;;
    --release-candidate)
      mode="release-candidate"
      include_lima=1
      include_proxy=1
      real_browser=1
      include_env_image=1
      include_probes=1
      include_dogfood_cli=1
      require_operator_proxy=1
      runtime_gate_mode=1
      ;;
    --isolation-evidence)
      # Enable the isolation-sensitive gates (Gate 2/3/4 + env-image). Each emits
      # a per-gate result via scripts/lib/gate-result.sh when
      # HIDEOUT_RELEASE_EVIDENCE_DIR is set; the release-dogfood manifest writer
      # aggregates them into isolationGates. The gate0/gate1 baseline still runs;
      # the probes and dogfood-cli gates are not selected.
      mode="isolation-evidence"
      include_lima=1
      include_proxy=1
      real_browser=1
      include_env_image=1
      require_operator_proxy=1
      ;;
    --lima)
      include_lima=1
      ;;
    --env-image)
      include_lima=1
      include_env_image=1
      shift
      ;;
    --lima-real-run)
      include_lima=1
      include_lima_real_run=1
      ;;
    --proxy)
      include_proxy=1
      ;;
    --real-browser)
      real_browser=1
      ;;
    --probes)
      include_probes=1
      ;;
    --dogfood-cli)
      include_dogfood_cli=1
      include_lima=1
      ;;
    --operator-cli)
      include_operator_cli=1
      include_lima=1
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "phase1: unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
  shift
done

echo "phase1: mode=$mode lima=$include_lima lima_real_run=$include_lima_real_run proxy=$include_proxy real_browser=$real_browser probes=$include_probes dogfood_cli=$include_dogfood_cli operator_cli=$include_operator_cli operator_proxy=$require_operator_proxy runtime=$runtime_gate_mode runtime_family=$runtime_gate_family"

if [ "${HIDEOUT_PHASE1_PRINT_PLAN:-}" = "1" ]; then
  print_plan
  exit 0
fi

if [ "$runtime_gate_mode" -eq 1 ]; then
  if [ -z "${HIDEOUT_RUNTIME_BUILD_PROVENANCE:-}" ] || [ ! -f "$HIDEOUT_RUNTIME_BUILD_PROVENANCE" ]; then
    echo "phase1: runtime release gates require HIDEOUT_RUNTIME_BUILD_PROVENANCE" >&2
    exit 2
  fi
fi

if [ "$require_operator_proxy" -eq 1 ] && [ -z "${HIDEOUT_SECRET_DEFAULT_PROXY:-}" ]; then
  echo "phase1: mode=$mode requires operator-supplied HIDEOUT_SECRET_DEFAULT_PROXY" >&2
  exit 2
fi

if [ "$require_operator_proxy" -eq 1 ]; then
  run_gate "Gate 3 operator proxy preflight" env HIDEOUT_GATE3_REQUIRE_OPERATOR_PROXY=1 scripts/test-gate3-hidden-proxy.sh --preflight-only
fi

if [ "$real_browser" -eq 1 ]; then
  run_gate "Gate 4 real browser launcher preflight" env HIDEOUT_GATE4_REAL_BROWSER=1 scripts/test-gate4-host-escape.sh --preflight-only
fi

run_gate "Gate 0 static contract" scripts/test-gate0.sh
run_gate "Gate 1 native smoke" scripts/test-gate1-native.sh

if [ "$include_lima" -eq 1 ]; then
  run_isolation_gate "gate2-lima" "Gate 2 Lima E2E" env \
    HIDEOUT_GATE2_RUNTIME_MODE="$runtime_gate_mode" \
    HIDEOUT_GATE2_RUNTIME_FAMILY="$runtime_gate_family" \
    scripts/test-gate2-lima.sh
fi

if [ "$include_env_image" -eq 1 ]; then
  if [ -n "${HIDEOUT_ENV_IMAGE_URL:-}" ]; then
    run_isolation_gate "env-image" "Env-image declared-image gate" scripts/test-env-image.sh
  else
    echo "phase1: env-image gate not-run (no HIDEOUT_ENV_IMAGE_URL declared)"
    if [ -n "${HIDEOUT_RELEASE_EVIDENCE_DIR:-}" ]; then
      . scripts/lib/gate-result.sh
      emit_gate_result "env-image" "lima" "not-run" "no image URL declared"
    fi
  fi
fi

if [ "$include_lima_real_run" -eq 1 ]; then
  run_gate "Lima real-run reference smoke" scripts/test-lima-real-run.sh
fi

if [ "$include_proxy" -eq 1 ]; then
  if [ "$require_operator_proxy" -eq 1 ]; then
    run_isolation_gate "gate3-hidden-proxy" "Gate 3 hidden proxy with operator-supplied proxy" env \
      HIDEOUT_GATE3_REQUIRE_OPERATOR_PROXY=1 \
      HIDEOUT_GATE3_RUNTIME_MODE="$runtime_gate_mode" \
      HIDEOUT_GATE3_RUNTIME_FAMILY="$runtime_gate_family" \
      scripts/test-gate3-hidden-proxy.sh
  else
    run_isolation_gate "gate3-hidden-proxy" "Gate 3 hidden proxy" env \
      HIDEOUT_GATE3_RUNTIME_MODE="$runtime_gate_mode" \
      HIDEOUT_GATE3_RUNTIME_FAMILY="$runtime_gate_family" \
      scripts/test-gate3-hidden-proxy.sh
  fi
fi

if [ "$real_browser" -eq 1 ]; then
  run_isolation_gate "gate4-host-escape" "Gate 4 host escape boundary with real browser external URL" env HIDEOUT_GATE4_REAL_BROWSER=1 scripts/test-gate4-host-escape.sh
else
  run_isolation_gate "gate4-host-escape" "Gate 4 host escape boundary dry-run" scripts/test-gate4-host-escape.sh
fi

if [ "$include_probes" -eq 1 ]; then
  run_gate "Capability probe CLI smoke" scripts/test-lab-probes.sh
fi

if [ "$include_dogfood_cli" -eq 1 ]; then
  run_gate "Generic CLI dogfood smoke" scripts/test-dogfood-cli-smoke.sh
fi

if [ "$include_operator_cli" -eq 1 ]; then
  run_gate "Operator-supplied real CLI smoke" scripts/test-operator-cli-smoke.sh
fi

# Account for every expected isolation gate in the evidence bundle: any gate
# without a result (not selected in this run) is recorded not-run, and the run
# fails if any isolation gate failed.
if [ -n "${HIDEOUT_RELEASE_EVIDENCE_DIR:-}" ]; then
  . scripts/lib/gate-result.sh
  # shellcheck disable=SC2086
  reconcile_isolation_gates $ISOLATION_GATE_IDS
  if [ "$isolation_gate_failed" -ne 0 ]; then
    echo "phase1: one or more isolation gates failed" >&2
    exit 1
  fi
fi

echo "phase1: passed"
