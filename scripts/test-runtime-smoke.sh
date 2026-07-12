#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-runtime-smoke.XXXXXX")"
cleanup() { rm -rf "$tmp"; }
trap cleanup EXIT

jq empty \
  schemas/runtime-catalog.schema.json \
  schemas/runtime-verification.schema.json \
  internal/runtimecatalog/catalog.json \
  internal/runtimecatalog/contract.json \
  runtime/developer-standard/sources.lock.json

test -s runtime/developer-standard/packages.txt
test -s runtime/developer-standard/packages.lock
runtime/developer-standard/test-build.sh

# These packages must run their complete test sets. A -run filter previously
# let the catalog package report success with "[no tests to run]".
go test ./internal/runtimecatalog
go test ./internal/runtimeverify ./internal/productevidence ./internal/releasecompat ./internal/app -run 'Runtime'

go build -o "$tmp/hideout" ./cmd/hideout
family_count="$(jq '.families | length' internal/runtimecatalog/catalog.json)"
observation_count="$(jq '.observations | length' internal/runtimecatalog/contract.json)"
if [ "$family_count" -eq 0 ]; then
  [ "$observation_count" -eq 0 ] || {
    echo "runtime-smoke: empty catalog has a non-empty contract" >&2
    exit 1
  }
  "$tmp/hideout" runtime list --json >"$tmp/runtime-list.json"
  jq -e '
    .schema == "hideout.runtime-catalog/v1" and
    .catalogRelease == "development-unpromoted" and
    ((.families // []) | length == 0)
  ' "$tmp/runtime-list.json" >/dev/null
  echo "runtime_catalog_state=not-promoted"
else
  [ "$observation_count" -gt 0 ] || {
    echo "runtime-smoke: promoted catalog has no contract observations" >&2
    exit 1
  }
  "$tmp/hideout" runtime list --json >"$tmp/runtime-list.json"
  jq -e --argjson count "$family_count" '.families | length == $count' "$tmp/runtime-list.json" >/dev/null
  echo "runtime_catalog_state=promoted-usable"
fi

bash -n scripts/test-runtime-lima.sh scripts/test-runtime-agent-install.sh scripts/test-gate2-lima.sh scripts/test-gate3-hidden-proxy.sh
test -x scripts/test-runtime-lima.sh
test -x scripts/test-runtime-agent-install.sh
grep -q 'HIDEOUT_GATE2_RUNTIME_MODE=1' scripts/test-runtime-lima.sh
grep -q 'prepare_guest_node is forbidden in runtime acceptance mode' scripts/test-gate2-lima.sh
grep -q 'runtime_mutable_guest_drift=passed' scripts/test-runtime-lima.sh
grep -q 'runtime_hideout_system_bootstrap=required-and-run' scripts/test-gate2-lima.sh
grep -q 'runtime_package_tool_provisioning=not-run' scripts/test-gate2-lima.sh
grep -q 'runtime_package_tool_provisioning_check=passed' scripts/test-gate2-lima.sh
if grep -Eq 'runtime_(guest_provisioning=not-run|no_guest_provisioning=passed)' scripts/test-gate2-lima.sh scripts/test-runtime-lima.sh; then
  echo "runtime-smoke: obsolete no-provisioning claim remains" >&2
  exit 1
fi
grep -q '@openai/codex@0.144.1' scripts/test-runtime-agent-install.sh
grep -q 'runtime_agent_secret_scan=passed' scripts/test-runtime-agent-install.sh
grep -q 'HIDEOUT_GATE3_RUNTIME_MODE' scripts/test-gate3-hidden-proxy.sh
grep -q 'runtime_agent_privacy=passed' scripts/test-gate3-hidden-proxy.sh
grep -q 'catalog still contains an unpromoted artifact' scripts/test-runtime-lima.sh
grep -q 'RuntimePolicyExactReal' internal/productevidence/registry.go
grep -q 'local fixture' internal/productevidence/evaluate_test.go
grep -q 'dirty gate' internal/releasecompat/readiness_test.go

echo "runtime-smoke: catalog state and verification contracts passed"
