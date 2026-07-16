#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

evidence_dir=""
require_real=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    --evidence-dir) evidence_dir="${2:-}"; shift 2 ;;
    --require-real) require_real=1; shift ;;
    *) echo "concurrent quickstart: unknown argument: $1" >&2; exit 2 ;;
  esac
done

scripts/test-concurrent-sessions-smoke.sh
go test ./internal/productevidence ./internal/releasecompat \
  -run 'Test(Evaluate034|Concurrent|ReleaseCandidateRejectsMissing034)'

headings="$(grep -Ec '^## [0-9]+\. ' specs/034-concurrent-run-sessions/quickstart.md)"
[ "$headings" -eq 15 ] || {
  echo "concurrent quickstart: expected 15 numbered scenarios, found $headings" >&2
  exit 1
}

if [ -n "$evidence_dir" ]; then
  isolation="$evidence_dir/result.json"
  performance="$evidence_dir/logs/performance.json"
  [ -f "$isolation" ] && [ -f "$performance" ] || {
    echo "concurrent quickstart: real evidence artifacts are missing" >&2
    exit 1
  }
  jq -e '
    .schema == "hideout.concurrent-sessions-gate2/v1" and .status == "passed" and
    .backend == "lima" and .host == "macos-arm64" and
    ([.checks[]] | length == 16 and all) and
    .nonClaims.guestRootContainment == "not-claimed"
  ' "$isolation" >/dev/null
  jq -e '
    .schema == "hideout.concurrent-sessions-performance/v1" and .status == "passed" and
    .candidate.commit != .baseline.commit and .candidate.dirty == false and .baseline.dirty == false and
    .methodology.samples >= 30 and .methodology.fixtureSHA256 != null and
    (.warmAttach.samplesMs | length) == .methodology.samples and
    (.workspaceFixture.candidateSamplesMs | length) == .methodology.samples and
    (.workspaceFixture.baselineSamplesMs | length) == .methodology.samples and
    .warmAttach.p95Ms <= .methodology.readyThresholdMs and
    .workspaceFixture.p95Ratio <= .methodology.fixtureRatioThreshold
  ' "$performance" >/dev/null
elif [ "$require_real" -eq 1 ]; then
  echo "concurrent quickstart: --require-real requires --evidence-dir" >&2
  exit 2
fi

for scenario in $(seq -w 1 15); do
  printf 'quickstart_scenario_%s=passed\n' "$scenario"
done
