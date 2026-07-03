#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

usage() {
  cat <<'USAGE'
Usage:
  scripts/test-phase1.sh [--quick|--required|--all|--release-candidate|--lima|--proxy|--real-browser|--probes|--dogfood-cli]

Modes:
  --quick         Run fast local gates: Gate 0, Gate 1, Gate 4 dry-run.
  --required      Run required automated gates without real browser launch:
                  Gate 0, Gate 1, Gate 2, Gate 3, Gate 4 dry-run.
  --all           Same as --required unless --real-browser is also set.
  --release-candidate
                  Run release-candidate gates: Gate 0-4, real browser,
                  operator-supplied proxy, and capability probe smoke.
  --lima          Include Gate 2 Lima E2E.
  --proxy         Include Gate 3 hidden proxy.
  --real-browser  Run Gate 4 with real external URL browser launch.
  --probes        Run capability probe CLI smoke after product gates.
  --dogfood-cli  Run generic CLI dogfood smoke on Lima.
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

print_plan() {
  echo "phase1-plan: Gate 0 static contract"
  echo "phase1-plan: Gate 1 native smoke"
  if [ "$include_lima" -eq 1 ]; then
    echo "phase1-plan: Gate 2 Lima E2E"
  fi
  if [ "$include_proxy" -eq 1 ]; then
    if [ "$require_operator_proxy" -eq 1 ]; then
      echo "phase1-plan: Gate 3 hidden proxy with operator-supplied proxy"
    else
      echo "phase1-plan: Gate 3 hidden proxy"
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
}

mode="quick"
include_lima=0
include_proxy=0
real_browser=0
include_probes=0
include_dogfood_cli=0
require_operator_proxy=0

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
      include_probes=1
      require_operator_proxy=1
      ;;
    --lima)
      include_lima=1
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

echo "phase1: mode=$mode lima=$include_lima proxy=$include_proxy real_browser=$real_browser probes=$include_probes dogfood_cli=$include_dogfood_cli operator_proxy=$require_operator_proxy"

if [ "${HIDEOUT_PHASE1_PRINT_PLAN:-}" = "1" ]; then
  print_plan
  exit 0
fi

if [ "$require_operator_proxy" -eq 1 ] && [ -z "${HIDEOUT_SECRET_DEFAULT_PROXY:-}" ]; then
  echo "phase1: --release-candidate requires operator-supplied HIDEOUT_SECRET_DEFAULT_PROXY" >&2
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
  run_gate "Gate 2 Lima E2E" scripts/test-gate2-lima.sh
fi

if [ "$include_proxy" -eq 1 ]; then
  if [ "$require_operator_proxy" -eq 1 ]; then
    run_gate "Gate 3 hidden proxy with operator-supplied proxy" env HIDEOUT_GATE3_REQUIRE_OPERATOR_PROXY=1 scripts/test-gate3-hidden-proxy.sh
  else
    run_gate "Gate 3 hidden proxy" scripts/test-gate3-hidden-proxy.sh
  fi
fi

if [ "$real_browser" -eq 1 ]; then
  run_gate "Gate 4 host escape boundary with real browser external URL" env HIDEOUT_GATE4_REAL_BROWSER=1 scripts/test-gate4-host-escape.sh
else
  run_gate "Gate 4 host escape boundary dry-run" scripts/test-gate4-host-escape.sh
fi

if [ "$include_probes" -eq 1 ]; then
  run_gate "Capability probe CLI smoke" scripts/test-lab-probes.sh
fi

if [ "$include_dogfood_cli" -eq 1 ]; then
  run_gate "Generic CLI dogfood smoke" scripts/test-dogfood-cli-smoke.sh
fi

echo "phase1: passed"
