#!/usr/bin/env bash
set -euo pipefail

# 043 Gate 0 contract: exercise the production semantic judge through its
# package-owned fixtures. This lane proves evaluator mechanics only; it cannot
# emit or promote real Lima evidence.

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

echo "projection-readiness-smoke: real producer and aggregate capture syntax"
bash -n scripts/test-projection-readiness-lima-e2e.sh \
  scripts/promote-projection-privacy.sh \
  scripts/lib/gate2-projection.sh scripts/lib/strict-projection-evidence.sh \
  scripts/test-gate2-lima.sh scripts/test-host-capability-projection-e2e.sh \
  scripts/test-host-app-pack-e2e.sh
scripts/test-projection-readiness-lima-e2e.sh --help |
  grep -q -- '--fresh <n>'
scripts/promote-projection-privacy.sh --help |
  grep -F -- '--gate3-result <gate3-hidden-proxy.json>' >/dev/null
for marker in guest_workspace=/workspace proxy_env_absent=yes dns_mediated=yes \
  connected_subnet_blocked=yes https_request=ok privilege_status=enforced \
  privileged_setup=network projection_alias_gate3=passed gateway_forward_path=passed; do
  grep -F "$marker" scripts/promote-projection-privacy.sh >/dev/null
done
runtime_gate3_builder="$(
  awk '
    /if \[ "\$GATE3_RUNTIME_MODE" = "1" \]; then/ { capture = 1 }
    capture { print }
    capture && /} >"\$runtime_evidence_out\/logs\/runtime-gate3.out"/ { exit }
  ' scripts/test-gate3-hidden-proxy.sh
)"
for marker in projection_alias_gate3=passed gateway_forward_path=passed; do
  grep -F "echo \"$marker\"" <<<"$runtime_gate3_builder" >/dev/null || {
    echo "projection-readiness-smoke: Gate 3 public runtime log omits marker: $marker" >&2
    exit 1
  }
done

echo "projection-readiness-smoke: exact candidate fixture"
go test -count=1 ./internal/productevidence \
  -run '^TestProjection(ReadinessValidatorAcceptsDerivedExactCandidate|PrivacyValidatorRequiresMatchingPassedGate3)$'

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
