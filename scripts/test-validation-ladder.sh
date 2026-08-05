#!/usr/bin/env bash
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
gate0_release_contracts="$(line_of "$gate0" 'gate0_begin_stage release-contracts')"
gate0_ui_stage="$(line_of "$gate0" 'gate0_begin_stage ui-acceptance')"
gate0_ui_run="$(line_of "$gate0" 'scripts/test-ui-e2e.sh --all --out "$ui_e2e_tmp"')"
gate0_product_smokes="$(line_of "$gate0" 'gate0_begin_stage product-smokes')"
gate0_release_surface="$(line_of "$gate0" 'gate0_begin_stage release-surface')"
assert_before 'Gate 0 release contracts before UI acceptance' \
  "$gate0_release_contracts" "$gate0_ui_stage"
assert_before 'Gate 0 UI stage before its proof' "$gate0_ui_stage" "$gate0_ui_run"
assert_before 'Gate 0 UI proof before product smokes' \
  "$gate0_ui_run" "$gate0_product_smokes"
assert_before 'Gate 0 product smokes before release surface' \
  "$gate0_product_smokes" "$gate0_release_surface"

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
if ! grep -Fq 'runtime_evidence_unique_binding \' "$public_candidate"; then
  printf 'validation-ladder: signed candidate lacks unique runtime selection\n' >&2
  exit 1
fi
if grep -Fq '[.proofs[] | select(.runtime != null)][0].runtime' \
  "$runtime_quickstart" "$public_candidate"; then
  printf 'validation-ladder: positional runtime binding selection returned\n' >&2
  exit 1
fi

printf '%s\n' \
  'validation-ladder: passed contracts=4 vmBoots=0 tlcRuns=0 browserRuns=0'
