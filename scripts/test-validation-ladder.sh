#!/usr/bin/env bash
# This structural contract intentionally matches literal shell source that
# contains "$..." expressions. Expanding those expressions here would change
# the contract instead of inspecting it.
# shellcheck disable=SC2016
set -euo pipefail

root="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd -P)"
cd "$root"

line_of() {
  local file="$1" exact="$2" lines count
  lines="$(awk -v exact="$exact" '$0 == exact { print NR }' "$file")"
  count="$(awk 'NF { count += 1 } END { print count + 0 }' <<<"$lines")"
  if [ "$count" -ne 1 ]; then
    printf 'validation-ladder: expected one exact line in %s: %s (found=%s)\n' \
      "$file" "$exact" "$count" >&2
    return 1
  fi
  printf '%s\n' "$lines"
}

assert_before() {
  local label="$1" earlier="$2" later="$3"
  if [ "$earlier" -ge "$later" ]; then
    printf 'validation-ladder: order violation: %s (%s !< %s)\n' \
      "$label" "$earlier" "$later" >&2
    return 1
  fi
}

gate0="scripts/test-gate0.sh"
gate0_static_preflight="$(line_of "$gate0" '  scripts/gates/release-static.sh --preflight || return 1')"
gate0_browser_preflight="$(line_of "$gate0" '  scripts/gates/browser-console.sh --preflight || return 1')"
gate0_contract_ladder="$(line_of "$gate0" '  scripts/test-validation-ladder.sh || return 1')"
gate0_full_static="$(line_of "$gate0" 'scripts/gates/release-static.sh')"
gate0_release_contracts="$(line_of "$gate0" 'gate0_begin_stage release-contracts')"
gate0_ui_stage="$(line_of "$gate0" 'gate0_begin_stage ui-acceptance')"
gate0_ui_required="$(line_of "$gate0" '  scripts/test-ui-e2e.sh --all --require-executed --out "$ui_e2e_tmp"')"
gate0_ui_tolerant="$(line_of "$gate0" '  scripts/test-ui-e2e.sh --all --out "$ui_e2e_tmp"')"
gate0_product_smokes="$(line_of "$gate0" 'gate0_begin_stage product-smokes')"
gate0_release_surface="$(line_of "$gate0" 'gate0_begin_stage release-surface')"
assert_before 'Gate 0 release static preflight before validation contracts' \
  "$gate0_static_preflight" "$gate0_contract_ladder"
assert_before 'Gate 0 release static before browser proof preflight' \
  "$gate0_static_preflight" "$gate0_browser_preflight"
assert_before 'Gate 0 browser proof preflight before validation contracts' \
  "$gate0_browser_preflight" "$gate0_contract_ladder"
assert_before 'Gate 0 release contracts before UI acceptance' \
  "$gate0_release_contracts" "$gate0_ui_stage"
assert_before 'Gate 0 UI stage before required CI proof' \
  "$gate0_ui_stage" "$gate0_ui_required"
assert_before 'Gate 0 UI stage before prerequisite-tolerant local proof' \
  "$gate0_ui_stage" "$gate0_ui_tolerant"
assert_before 'Gate 0 required UI proof before product smokes' \
  "$gate0_ui_required" "$gate0_product_smokes"
assert_before 'Gate 0 local UI proof before product smokes' \
  "$gate0_ui_tolerant" "$gate0_product_smokes"
assert_before 'Gate 0 product smokes before release surface' \
  "$gate0_product_smokes" "$gate0_release_surface"
line_of "scripts/gates/release-candidate.sh" \
  '  scripts/gates/release-static.sh' >/dev/null
[ "$gate0_full_static" -gt "$gate0_contract_ladder" ] || {
  printf 'validation-ladder: full Gate 0 static contract must follow preflight\n' >&2
  exit 1
}

candidate_workflow=".github/workflows/hideout-alpha-candidate.yml"
candidate_static_preflight="$(line_of "$candidate_workflow" '      - name: Run deterministic release preflight before credentials')"
candidate_static_command="$(line_of "$candidate_workflow" '          scripts/gates/release-static.sh --preflight')"
candidate_browser_preflight="$(line_of "$candidate_workflow" '          scripts/gates/browser-console.sh --preflight')"
candidate_credentials="$(line_of "$candidate_workflow" '      - name: Import protected Developer ID and notarization credentials')"
assert_before 'candidate static preflight before protected credentials' \
  "$candidate_static_preflight" "$candidate_credentials"
assert_before 'candidate static command before browser proof preflight' \
  "$candidate_static_command" "$candidate_browser_preflight"
assert_before 'candidate browser proof preflight before protected credentials' \
  "$candidate_browser_preflight" "$candidate_credentials"

for gate0_diagnostic_marker in \
  'set -Eeuo pipefail' \
  'trap '\''gate0_capture_error "$LINENO" "$BASH_COMMAND"'\'' ERR' \
  'gate0_record_failure "$line" "$command"' \
  'currentLane=%s failureLine=%s diagnosticCommand=%q' \
  'scripts/test-validation-ladder.sh || return 1'; do
  if ! grep -Fq "$gate0_diagnostic_marker" "$gate0"; then
    printf 'validation-ladder: Gate 0 diagnostic contract missing: %s\n' \
      "$gate0_diagnostic_marker" >&2
    exit 1
  fi
done

if ! grep -Fq \
    'go run ./internal/productevidence/cmd/validate-021 "$manifest"' \
    scripts/test-ui-e2e.sh; then
  echo 'validation-ladder: required UI run does not close against the proof registry' >&2
  exit 1
fi
if ! grep -Fq \
    '[ "$want_manifest$want_browser$want_tui" = "111" ]' \
    scripts/test-ui-e2e.sh; then
  echo 'validation-ladder: UI registry closure is not bound to the complete lane set' >&2
  exit 1
fi

if grep -Fq 'internal/manager/server.go' scripts/test-live-console-smoke.sh; then
  echo 'validation-ladder: live-console smoke still asserts the retired Manager WebUI owner' >&2
  exit 1
fi
for live_console_binding in \
  'internal/daemon/uiweb_assets/client.js' \
  'internal/daemon/uiweb_assets/state.js' \
  'internal/daemon/uiweb_assets/app.js' \
  'TestBrowserDecisionAndNoticeClients'; do
  if ! grep -Fq "$live_console_binding" scripts/test-live-console-smoke.sh; then
    printf 'validation-ladder: daemon-owned live-console smoke binding missing: %s\n' \
      "$live_console_binding" >&2
    exit 1
  fi
done

candidate="scripts/gates/release-candidate.sh"
candidate_inventory="scripts/gates/release-candidate-inventory.json"
candidate_ladder="$(line_of "$candidate" 'scripts/test-validation-ladder.sh')"
candidate_schema="$(line_of "$candidate" 'run_lane schema schema_lane')"
candidate_static="$(line_of "$candidate" 'run_lane static static_lane')"
candidate_blockers="$(line_of "$candidate" 'run_lane release-blockers release_blockers_lane')"
candidate_generated="$(line_of "$candidate" 'run_lane generated generated_lane')"
candidate_dependencies="$(line_of "$candidate" 'run_lane dependencies-advisory dependencies_advisory_lane')"
candidate_unit="$(line_of "$candidate" 'run_lane unit unit_lane')"
candidate_race="$(line_of "$candidate" 'run_lane race race_lane')"
candidate_fuzz="$(line_of "$candidate" 'run_lane fuzz-property fuzz_property_lane')"
candidate_migration="$(line_of "$candidate" 'run_lane migration scripts/gates/migration.sh --out "$run_dir/migration"')"
candidate_mutations="$(line_of "$candidate" 'run_lane mutations mutations_lane')"
candidate_fail_fast="$(line_of "$candidate" '  return "$status"')"
assert_before 'validation contract before candidate lanes' \
  "$candidate_ladder" "$candidate_schema"
assert_before 'schema before static' "$candidate_schema" "$candidate_static"
assert_before 'static before release blockers' "$candidate_static" "$candidate_blockers"
assert_before 'release blockers before generated checks' \
  "$candidate_blockers" "$candidate_generated"
assert_before 'generated checks before dependency advisories' \
  "$candidate_generated" "$candidate_dependencies"
assert_before 'all deterministic blockers before unit tests' \
  "$candidate_dependencies" "$candidate_unit"
assert_before 'unit tests before race tests' "$candidate_unit" "$candidate_race"
assert_before 'race tests before fuzz/property tests' "$candidate_race" "$candidate_fuzz"
assert_before 'fuzz/property before migration' "$candidate_fuzz" "$candidate_migration"
assert_before 'migration before mutations' "$candidate_migration" "$candidate_mutations"
assert_before 'lane status propagation before lane execution' \
  "$candidate_fail_fast" "$candidate_schema"

lima_candidate="scripts/gates/release-candidate-lima.sh"
lima_blocked_guard="$(line_of "$lima_candidate" '  if [ -n "$blocked_by_lane" ]; then')"
lima_reuse_guard="$(line_of "$lima_candidate" '  if lane_reusable "$lane_id"; then')"
lima_workload="$(line_of "$lima_candidate" \
  "run_or_reuse_lane workload-observation workload \"\$workload_result\" \\")"
lima_rotation="$(line_of "$lima_candidate" \
  "run_or_reuse_lane network-rotation network-rotation \"\$rotation_result\" \\")"
lima_privacy="$(line_of "$lima_candidate" \
  "run_or_reuse_lane workload-privacy privacy \"\$privacy_result\" \\")"
lima_concurrent="$(line_of "$lima_candidate" \
  "run_or_reuse_lane concurrent-crash-recovery concurrent \"\$concurrent_result\" \\")"
assert_before 'real-Lima failure guard before reuse decision' \
  "$lima_blocked_guard" "$lima_reuse_guard"
assert_before 'real-Lima workload before network rotation' \
  "$lima_workload" "$lima_rotation"
assert_before 'real-Lima network rotation before privacy' \
  "$lima_rotation" "$lima_privacy"
assert_before 'real-Lima privacy before concurrency recovery' \
  "$lima_privacy" "$lima_concurrent"
for lima_fail_fast_marker in \
  'record_blocked_lane "$lane_id"' \
  'execution:"not-run"' \
  'reason "blocked-by-prior-failure:$blocked_by_lane"' \
  'all(range($failedIndex + 1; .lanes | length);'; do
  if ! grep -Fq "$lima_fail_fast_marker" "$lima_candidate"; then
    printf 'validation-ladder: real-Lima fail-fast contract missing: %s\n' \
      "$lima_fail_fast_marker" >&2
    exit 1
  fi
done
if ! grep -Fq 'retain_failure_diagnostics || status=1' \
    scripts/gates/workload-observation-lima.sh; then
  printf '%s\n' \
    'validation-ladder: workload failure diagnostics are not retained before cleanup' \
    >&2
  exit 1
fi

wired_lanes="$(awk '$1 == "run_lane" { print $2 }' "$candidate")"
inventoried_lanes="$(jq -r '.requiredLanes[]' "$candidate_inventory")"
if [ "$wired_lanes" != "$inventoried_lanes" ]; then
  printf 'validation-ladder: candidate lane order drifted from its inventory\n' >&2
  diff -u <(printf '%s\n' "$inventoried_lanes") \
    <(printf '%s\n' "$wired_lanes") >&2 || true
  exit 1
fi

formal="scripts/gates/formal.sh"
if ! grep -Fq '    --preflight)' "$formal"; then
  printf 'validation-ladder: formal gate is missing --preflight parsing\n' >&2
  exit 1
fi
formal_judge="$(line_of "$formal" 'judge_mutation_preflight')"
formal_preflight_exit="$(line_of "$formal" 'if [ "$preflight_only" -eq 1 ]; then')"
formal_jar="$(line_of "$formal" 'if [ ! -f "$tla_jar" ]; then')"
formal_tlc="$(line_of "$formal" 'done < <(configuration_entries)')"
assert_before 'formal judge preflight before preflight-only exit' \
  "$formal_judge" "$formal_preflight_exit"
assert_before 'formal preflight-only exit before jar acquisition' \
  "$formal_preflight_exit" "$formal_jar"
assert_before 'formal jar acquisition before TLC execution' "$formal_jar" "$formal_tlc"

runtime_quickstart="scripts/test-runtime-quickstart.sh"
runtime_lima="scripts/test-runtime-lima.sh"
public_candidate="scripts/test-public-alpha-candidate.sh"
line_of "$runtime_quickstart" \
  'gate2_runtime="$(runtime_evidence_unique_binding "$gate2_manifest")"' >/dev/null
line_of "$runtime_quickstart" \
  'gate3_runtime="$(runtime_evidence_unique_binding "$gate3_manifest")"' >/dev/null
line_of "$runtime_lima" 'baseline_id="baseline.git"' >/dev/null
if ! grep -Fq "runtime_evidence_unique_binding \\" "$public_candidate"; then
  printf 'validation-ladder: signed candidate lacks unique runtime selection\n' >&2
  exit 1
fi
if grep -Fq '[.proofs[] | select(.runtime != null)][0].runtime' \
  "$runtime_quickstart" "$public_candidate"; then
  printf 'validation-ladder: positional runtime binding selection returned\n' >&2
  exit 1
fi

# Direct event-stream references are a closed, role-reviewed set. This catches
# protocol changes in a server or client before a remote product-smoke stage can
# discover a forgotten shell/embedded consumer after unrelated work.
event_reference_paths="$({
  git grep -l -F '/daemon/events' -- internal scripts || true
} | awk '$0 !~ /_test\.go$/ && $0 != "scripts/test-validation-ladder.sh" { print }' | LC_ALL=C sort)"
expected_event_reference_paths="$(printf '%s\n' \
  'internal/daemon/client.go' \
  'internal/daemon/server.go' \
  'internal/daemon/uiweb.go' \
  'internal/daemon/uiweb_assets/client.js' \
  'scripts/test-daemon-smoke.sh')"
if [ "$event_reference_paths" != "$expected_event_reference_paths" ]; then
  printf 'validation-ladder: daemon event-stream reference inventory drifted\n' >&2
  diff -u <(printf '%s\n' "$expected_event_reference_paths") \
    <(printf '%s\n' "$event_reference_paths") >&2 || true
  exit 1
fi
for binding in \
  'internal/daemon/client.go:values.Set("since",' \
  'internal/daemon/uiweb_assets/client.js:"&since="' \
  'scripts/test-daemon-smoke.sh:&since=$sequence' \
  'internal/app/tui.go:snapshot.Sequence)'; do
  binding_file="${binding%%:*}"
  binding_marker="${binding#*:}"
  if ! grep -Fq "$binding_marker" "$binding_file"; then
    printf 'validation-ladder: sequence binding missing: %s (%s)\n' \
      "$binding_file" "$binding_marker" >&2
    exit 1
  fi
done

for fail_fast_test in \
  'scripts/test-gate0.sh:go test -failfast -p "$go_test_parallelism" -count=1 ./...' \
  'scripts/gates/release-candidate.sh:go test -json -failfast -p 4 -count=1 ./...' \
  'scripts/gates/release-candidate.sh:go test -json -failfast -race -p 2 -count=1 ./...'; do
  test_file="${fail_fast_test%%:*}"
  test_marker="${fail_fast_test#*:}"
  if ! grep -Fq "$test_marker" "$test_file"; then
    printf 'validation-ladder: package fail-fast contract missing: %s\n' \
      "$test_marker" >&2
    exit 1
  fi
done

if git grep -F 'manager.StartLocalServer' -- internal/app >/dev/null; then
  printf 'validation-ladder: public UI entrypoint returned to a second Manager server\n' >&2
  exit 1
fi
for ui_binding in \
  'internal/app/app.go:resolveURL = daemon.BrowserUIURL' \
  'internal/app/command_catalog.go:Ensures hideoutd is running' \
  'test/e2e/webui/fixture.go:daemon.BrowserUIURL(root, d.Status())' \
  'scripts/test-daemon-smoke.sh:"$bin" ui --no-open --print-url'; do
  ui_file="${ui_binding%%:*}"
  ui_marker="${ui_binding#*:}"
  if ! grep -Fq "$ui_marker" "$ui_file"; then
    printf 'validation-ladder: daemon-owned public UI binding missing: %s\n' \
      "$ui_marker" >&2
    exit 1
  fi
done
if git grep -E 'hideout ui.*--(listen|ttl)' -- internal/app docs scripts \
    ':!scripts/test-validation-ladder.sh' >/dev/null; then
  printf 'validation-ladder: obsolete per-command UI ownership option returned\n' >&2
  exit 1
fi

printf '%s\n' \
  'validation-ladder: passed contracts=13 vmBoots=0 tlcRuns=0 browserRuns=0'
