#!/usr/bin/env bash
set -euo pipefail

# 043 Gate 0 contract: exercise the production semantic judge through its
# package-owned fixtures. This lane proves evaluator mechanics only; it cannot
# emit or promote real Lima evidence.

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

echo "projection-readiness-smoke: real producer and aggregate capture syntax"
bash -n scripts/test-projection-readiness-lima-e2e.sh \
  scripts/lib/gate2-projection.sh scripts/lib/strict-projection-evidence.sh \
  scripts/test-gate2-lima.sh scripts/test-host-capability-projection-e2e.sh \
  scripts/test-host-app-pack-e2e.sh
scripts/test-projection-readiness-lima-e2e.sh --help |
  grep -q -- '--fresh <n>'

echo "projection-readiness-smoke: exact candidate fixture"
go test -count=1 ./internal/productevidence \
  -run '^TestProjectionReadinessValidatorAcceptsDerivedExactCandidate$'

echo "projection-readiness-smoke: mandatory false-green fixtures"
smoke_output="$(mktemp "${TMPDIR:-/tmp}/hideout-projection-readiness-smoke.XXXXXX")"
trap 'rm -f "$smoke_output"' EXIT
go test -count=1 -v ./internal/productevidence \
  -run '^TestProjectionReadinessValidatorRejectsFalseGreenArtifacts/(forged_marker_only|wrong_source_package_digest|nine_fresh_samples|edited_p95_summary|missing_external_flow|missing_persistent_grant_flow)$' \
  >"$smoke_output"

for fixture in \
  forged_marker_only \
  wrong_source_package_digest \
  nine_fresh_samples \
  edited_p95_summary \
  missing_external_flow \
  missing_persistent_grant_flow
do
  if ! grep -qF "=== RUN   TestProjectionReadinessValidatorRejectsFalseGreenArtifacts/$fixture" "$smoke_output"; then
    echo "projection-readiness-smoke: fixture did not run: $fixture" >&2
    cat "$smoke_output" >&2
    exit 1
  fi
done

echo "projection-readiness-smoke: PASS (strict judge only; real Gate 2/Gate 3 remain unpromoted)"
