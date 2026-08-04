#!/usr/bin/env bash
set -euo pipefail

root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd -P)"
cd "$root"
# shellcheck source=scripts/lib/gate-result.sh
# shellcheck disable=SC1091
. "$root/scripts/lib/gate-result.sh"
gate_completed=0

out="$root/.artifacts/046/migration"
fuzz_time="${HIDEOUT_MIGRATION_FUZZ_TIME:-1s}"
fuzz_procs="${HIDEOUT_MIGRATION_FUZZ_PROCS:-2}"
migration_test_inventory="$root/scripts/gates/migration-tests.txt"
hostile_test_inventory="$root/scripts/gates/migration-hostile-tests.txt"
crash_cut_inventory="$root/scripts/gates/migration-crash-cuts.txt"
preflight_only=0

usage() {
  printf '%s\n' \
    "Usage: scripts/gates/migration.sh [--out DIR]" \
    "       scripts/gates/migration.sh --preflight" \
    "" \
    "Runs migration schemas, portable Go and offline Lima checks, bounded fuzz smoke, the four" \
    "migration TLC configurations, and their inventoried refinement tests." \
    "This local gate starts no VM and makes no real-backend migration claim."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --out)
      if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
        printf 'migration-gate: --out requires a directory\n' >&2
        exit 2
      fi
      out="$2"
      shift 2
      ;;
    --preflight)
      preflight_only=1
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'migration-gate: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

for command in awk bash comm curl find git go grep java jq mktemp sed shellcheck sort tr uniq wc; do
  if ! command -v "$command" >/dev/null 2>&1; then
    printf 'migration-gate: missing required command: %s\n' "$command" >&2
    exit 1
  fi
done
if ! printf '%s\n' "$fuzz_time" | grep -Eq '^[1-9][0-9]*(ms|s)$'; then
  printf 'migration-gate: invalid HIDEOUT_MIGRATION_FUZZ_TIME: %s\n' "$fuzz_time" >&2
  exit 2
fi
case "$fuzz_procs" in
  1 | 2 | 3 | 4) ;;
  *)
    printf 'migration-gate: HIDEOUT_MIGRATION_FUZZ_PROCS must be 1..4\n' >&2
    exit 2
    ;;
esac

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  printf 'migration-gate: missing shasum or sha256sum\n' >&2
  return 127
}

discover_migration_tests() {
  packages_file="$1"
  destination="$2"
  : >"$destination"
  while IFS= read -r package; do
    go test "$package" -list 'Migration|Migrate|ConfigOnly' |
      awk -v package="$package" \
        '/^(Test|Example)[A-Za-z0-9_]+$/ {print package " " $0}' \
        >>"$destination"
  done <"$packages_file"
  LC_ALL=C sort -o "$destination" "$destination"
}

prepare_migration_test_inventory() {
  if [ ! -f "$migration_test_inventory" ] || [ -L "$migration_test_inventory" ]; then
    printf 'migration-gate: migration test inventory is missing or unsafe\n' >&2
    return 1
  fi
  expected="$scratch/expected-migration-tests"
  packages="$scratch/migration-test-packages"
  discovered="$scratch/discovered-migration-tests"
  awk '
    /^#/ || NF == 0 {next}
    NF != 2 {exit 2}
    {
      if (substr($1, 1, 2) != "./") exit 2
      path = substr($1, 3)
      count = split(path, parts, "/")
      if (count == 0) exit 2
      for (part_index = 1; part_index <= count; part_index++) {
        if (parts[part_index] == "" || parts[part_index] == "." ||
            parts[part_index] == ".." ||
            parts[part_index] !~ /^[A-Za-z0-9_.-]+$/) exit 2
      }
      if ($2 !~ /^(Test|Example)[A-Za-z0-9_]+$/) exit 2
    }
    {print}
  ' "$migration_test_inventory" >"$expected" || {
    printf 'migration-gate: migration test inventory syntax is invalid\n' >&2
    return 1
  }
  expected_count="$(wc -l <"$expected" | tr -d ' ')"
  unique_count="$(LC_ALL=C sort -u "$expected" | wc -l | tr -d ' ')"
  if ! LC_ALL=C sort -c "$expected" 2>/dev/null ||
    [ "$unique_count" -ne "$expected_count" ]; then
    printf 'migration-gate: migration test inventory must be sorted and unique\n' >&2
    return 1
  fi
  awk '{print $1}' "$expected" | uniq >"$packages"
  discover_migration_tests "$packages" "$discovered"
  if [ -n "$(comm -3 "$expected" "$discovered")" ]; then
    printf 'migration-gate: migration test inventory drifted\n' >&2
    comm -3 "$expected" "$discovered" >&2
    return 1
  fi
}

prepare_hostile_test_inventory() {
  if [ ! -f "$hostile_test_inventory" ] || [ -L "$hostile_test_inventory" ]; then
    printf 'migration-gate: hostile-input test inventory is missing or unsafe\n' >&2
    return 1
  fi
  hostile_expected="$scratch/expected-hostile-tests"
  awk '
    /^#/ || NF == 0 {next}
    NF != 3 {exit 2}
    {
      if ($1 !~ /^[a-z][a-z0-9-]*$/) exit 2
      if (substr($2, 1, 2) != "./") exit 2
      package_path = substr($2, 3)
      count = split(package_path, parts, "/")
      if (count == 0) exit 2
      for (part_index = 1; part_index <= count; part_index++) {
        if (parts[part_index] == "" || parts[part_index] == "." ||
            parts[part_index] == ".." ||
            parts[part_index] !~ /^[A-Za-z0-9_.-]+$/) exit 2
      }
      if ($3 !~ /^Test[A-Za-z0-9_]+$/) exit 2
    }
    {print}
  ' "$hostile_test_inventory" >"$hostile_expected" || {
    printf 'migration-gate: hostile-input test inventory syntax is invalid\n' >&2
    return 1
  }
  hostile_count="$(wc -l <"$hostile_expected" | tr -d ' ')"
  if [ "$hostile_count" -ne 9 ] ||
    ! LC_ALL=C sort -c "$hostile_expected" 2>/dev/null ||
    [ "$(awk '{print $1}' "$hostile_expected" | LC_ALL=C sort -u | wc -l | tr -d ' ')" -ne 9 ]; then
    printf 'migration-gate: hostile-input inventory must contain nine sorted unique categories\n' >&2
    return 1
  fi
  while read -r category package test_name; do
    listed="$(go test "$package" -list "^${test_name}$" |
      awk '/^Test[A-Za-z0-9_]+$/ {print}')"
    if [ "$listed" != "$test_name" ]; then
      printf 'migration-gate: hostile-input test is missing: %s %s (%s)\n' \
        "$package" "$test_name" "$category" >&2
      return 1
    fi
  done <"$hostile_expected"
}

prepare_crash_cut_inventory() {
  if [ ! -f "$crash_cut_inventory" ] || [ -L "$crash_cut_inventory" ]; then
    printf 'migration-gate: durable crash-cut inventory is missing or unsafe\n' >&2
    return 1
  fi
  crash_cut_expected="$scratch/expected-crash-cuts"
  crash_cut_ids="$scratch/crash-cut-ids"
  expected_crash_cut_ids="$scratch/expected-crash-cut-ids"
  awk '
    /^#/ || NF == 0 {next}
    NF != 4 {exit 2}
    {
      if ($1 !~ /^(export|import)-[a-z0-9-]+$/) exit 2
      if (substr($2, 1, 2) != "./") exit 2
      package_path = substr($2, 3)
      count = split(package_path, parts, "/")
      if (count == 0) exit 2
      for (part_index = 1; part_index <= count; part_index++) {
        if (parts[part_index] == "" || parts[part_index] == "." ||
            parts[part_index] == ".." ||
            parts[part_index] !~ /^[A-Za-z0-9_.-]+$/) exit 2
      }
      if ($3 !~ /^Test[A-Za-z0-9_]+$/) exit 2
      if ($4 != "-" && $4 !~ /^[A-Za-z0-9_]+$/) exit 2
    }
    {print}
  ' "$crash_cut_inventory" >"$crash_cut_expected" || {
    printf 'migration-gate: durable crash-cut inventory syntax is invalid\n' >&2
    return 1
  }
  awk '{print $1}' "$crash_cut_expected" >"$crash_cut_ids"
  printf '%s\n' \
    export-bundle-header-synced \
    export-footer-written-before-final-rename \
    export-manifest-written \
    export-payload-checkpoint-record-synced \
    export-provider-snapshot-created \
    export-source-claims-acquired \
    import-activation-decision-recorded \
    import-adoption-helper-completed \
    import-claims-acquired \
    import-disk-component-synced \
    import-manager-visibility-committed \
    import-provider-verification-completed \
    import-provisional-secret-prepared >"$expected_crash_cut_ids"
  crash_cut_count="$(wc -l <"$crash_cut_expected" | tr -d ' ')"
  if [ "$crash_cut_count" -ne 13 ] ||
    ! LC_ALL=C sort -c "$crash_cut_expected" 2>/dev/null ||
    [ "$(LC_ALL=C sort -u "$crash_cut_ids" | wc -l | tr -d ' ')" -ne 13 ] ||
    [ -n "$(comm -3 "$expected_crash_cut_ids" "$crash_cut_ids")" ]; then
    printf 'migration-gate: durable crash-cut inventory must match the exact 13 sorted cuts\n' >&2
    comm -3 "$expected_crash_cut_ids" "$crash_cut_ids" >&2 || true
    return 1
  fi
  while read -r _ package test_name _; do
    listed="$(go test "$package" -list "^${test_name}$" |
      awk '/^Test[A-Za-z0-9_]+$/ {print}')"
    if [ "$listed" != "$test_name" ]; then
      printf 'migration-gate: durable crash-cut test is missing: %s %s\n' \
        "$package" "$test_name" >&2
      return 1
    fi
  done <"$crash_cut_expected"
}

run_migration_test_inventory() {
  prepare_migration_test_inventory || return 1
  : >"$migration_test_log"
  while IFS= read -r package; do
    expected_names="$scratch/expected-$(printf '%s' "$package" | tr '/.' '__')"
    observed_names="$scratch/observed-$(printf '%s' "$package" | tr '/.' '__')"
    package_log="$scratch/results-$(printf '%s' "$package" | tr '/.' '__').json"
    awk -v package="$package" '$1 == package {print $2}' "$expected" >"$expected_names"
    pattern="$(awk 'BEGIN {first=1} {
      if (!first) printf "|"
      printf "%s", $0
      first=0
    } END {printf "\n"}' "$expected_names")"
    printf 'migration-gate: running %s inventoried tests in %s\n' \
      "$(wc -l <"$expected_names" | tr -d ' ')" "$package"
    go test -json "$package" -run "^(${pattern})$" -count=1 >"$package_log"
    jq -r '
      select(.Action == "pass" and (.Test // "") != "") |
      .Test
    ' "$package_log" | LC_ALL=C sort -u >"$observed_names"
    if [ -n "$(comm -23 "$expected_names" "$observed_names")" ]; then
      printf 'migration-gate: inventoried tests did not all pass in %s\n' "$package" >&2
      comm -23 "$expected_names" "$observed_names" >&2
      return 1
    fi
    jq -c --arg package "$package" '. + {inventoryPackage: $package}' \
      "$package_log" >>"$migration_test_log"
  done <"$packages"
  wc -l <"$expected" | tr -d ' ' >"$scratch/migration-test-count"
  wc -l <"$packages" | tr -d ' ' >"$scratch/migration-package-count"
}

run_hostile_test_inventory() {
  prepare_hostile_test_inventory || return 1
  : >"$hostile_mutation_log"
  while read -r category package test_name; do
    package_log="$scratch/hostile-${category}.json"
    printf 'migration-gate: hostile input %s via %s\n' "$category" "$test_name"
    go test -json "$package" -run "^${test_name}$" -count=1 >"$package_log"
    if ! jq -e --arg name "$test_name" '
      select(.Action == "pass" and .Test == $name)
    ' "$package_log" >/dev/null; then
      printf 'migration-gate: hostile-input test did not pass: %s\n' \
        "$test_name" >&2
      return 1
    fi
    jq -c \
      --arg category "$category" \
      --arg package "$package" \
      '. + {hostileCategory: $category, inventoryPackage: $package}' \
      "$package_log" >>"$hostile_mutation_log"
  done <"$hostile_expected"
  printf '%s\n' "$hostile_count" >"$scratch/hostile-test-count"
}

run_crash_cut_inventory() {
  prepare_crash_cut_inventory || return 1
  : >"$crash_cut_log"
  while read -r cut_id package test_name subtest_name; do
    selector="^${test_name}$"
    expected_event="$test_name"
    if [ "$subtest_name" != "-" ]; then
      selector="${selector}/^${subtest_name}$"
      expected_event="${test_name}/${subtest_name}"
    fi
    result_log="$scratch/crash-cut-${cut_id}.json"
    printf 'migration-gate: durable crash cut %s via %s\n' "$cut_id" "$expected_event"
    go test -json "$package" -run "$selector" -count=1 >"$result_log"
    if ! jq -e --arg name "$expected_event" '
      select(.Action == "pass" and .Test == $name)
    ' "$result_log" >/dev/null; then
      printf 'migration-gate: durable crash-cut test did not pass: %s\n' \
        "$cut_id" >&2
      return 1
    fi
    jq -c \
      --arg crashCutID "$cut_id" \
      --arg package "$package" \
      --arg selector "$selector" \
      '. + {
        crashCutID: $crashCutID,
        inventoryPackage: $package,
        inventorySelector: $selector
      }' "$result_log" >>"$crash_cut_log"
  done <"$crash_cut_expected"
  printf '%s\n' "$crash_cut_count" >"$scratch/crash-cut-count"
}

if [ "$preflight_only" -eq 1 ]; then
  scratch="$(mktemp -d "${TMPDIR:-/tmp}/hideout-migration-preflight.XXXXXX")"
  migration_test_log="$scratch/migration-tests.jsonl"
  # Invoked indirectly by the EXIT trap.
  # shellcheck disable=SC2329
  cleanup_preflight() {
    local exit_status=$?
    case "${scratch:-}" in
      "${TMPDIR:-/tmp}"/hideout-migration-preflight.*)
        [ ! -d "$scratch" ] || find "$scratch" -depth -delete
        ;;
      *)
        printf 'migration-gate: refusing unexpected preflight cleanup: %s\n' \
          "${scratch:-}" >&2
        exit_status=1
        ;;
    esac
    exit "$exit_status"
  }
  trap cleanup_preflight EXIT
  prepare_migration_test_inventory
  prepare_hostile_test_inventory
  prepare_crash_cut_inventory
  bash -n scripts/gates/migration.sh scripts/gates/migration-lima.sh
  shellcheck scripts/gates/migration.sh scripts/gates/migration-lima.sh
  printf 'migration-gate: preflight=passed tests=%s packages=%s hostile=%s crash-cuts=%s\n' \
    "$(wc -l <"$expected" | tr -d ' ')" \
    "$(wc -l <"$packages" | tr -d ' ')" \
    "$hostile_count" \
    "$crash_cut_count"
  exit 0
fi

mkdir -p "$out"
out="$(CDPATH='' cd -- "$out" && pwd -P)"
run_id="run-$(date -u +'%Y%m%dT%H%M%SZ')-$$"
run_dir="$out/$run_id"
mkdir -p "$run_dir/tlc"
chmod 0700 "$out" "$run_dir" "$run_dir/tlc"

scratch="$(mktemp -d "${TMPDIR:-/tmp}/hideout-migration-gate.XXXXXX")"
cleanup() {
  exit_status=$?
  rm -rf -- "$scratch"
  if [ "$exit_status" -eq 0 ]; then
    gate_require_completion "migration-gate"
  fi
}
trap cleanup EXIT

schema_log="$run_dir/schema.log"
portable_log="$run_dir/portable.log"
fuzz_log="$run_dir/fuzz.log"
refinement_log="$run_dir/refinement.json"
migration_test_log="$run_dir/migration-tests.jsonl"
hostile_mutation_log="$run_dir/hostile-mutations.jsonl"
crash_cut_log="$run_dir/crash-cuts.jsonl"
migration_test_count=0
migration_package_count=0
hostile_test_count=0
crash_cut_count=0

printf 'migration-gate: validating migration schemas\n'
{
  jq empty \
    schemas/migration-manifest.schema.json \
    schemas/migration-operation-projection.schema.json \
    schemas/migration-plan.schema.json \
    schemas/migration-receipt.schema.json
  go test ./schemas -run '^TestMigrationSchemas' -count=1
} 2>&1 | tee "$schema_log"

printf 'migration-gate: running portable core and Manager boundary checks\n'
{
  go vet ./internal/migration ./internal/backend ./internal/backend/native ./internal/backend/lima ./internal/manager ./internal/app
  go test ./internal/migration -count=1
  run_migration_test_inventory
  run_hostile_test_inventory
  run_crash_cut_inventory
} 2>&1 | tee "$portable_log"
migration_test_count="$(tr -d ' ' <"$scratch/migration-test-count")"
migration_package_count="$(tr -d ' ' <"$scratch/migration-package-count")"
hostile_test_count="$(tr -d ' ' <"$scratch/hostile-test-count")"
crash_cut_count="$(tr -d ' ' <"$scratch/crash-cut-count")"

expected_fuzz="$scratch/expected-fuzz"
discovered_fuzz="$scratch/discovered-fuzz"
printf '%s\n' \
  FuzzMigrationFrame \
  FuzzMigrationManifest \
  FuzzMigrationPrivateHeader \
  FuzzMigrationPublicHeader \
  FuzzMigrationTrailer \
  FuzzMigrationZstdRecord | LC_ALL=C sort >"$expected_fuzz"
sed -n 's/^func \(FuzzMigration[A-Za-z0-9_]*\).*/\1/p' \
  internal/migration/fuzz_test.go | LC_ALL=C sort >"$discovered_fuzz"
if [ -n "$(comm -3 "$expected_fuzz" "$discovered_fuzz")" ]; then
  printf 'migration-gate: fuzz target inventory drifted\n' >&2
  comm -3 "$expected_fuzz" "$discovered_fuzz" >&2
  exit 1
fi

: >"$fuzz_log"
while IFS= read -r fuzz_name; do
  printf 'migration-gate: fuzz smoke %s (%s)\n' "$fuzz_name" "$fuzz_time"
  GOMAXPROCS="$fuzz_procs" go test ./internal/migration \
    -run '^$' \
    -fuzz "^${fuzz_name}$" \
    -fuzztime "$fuzz_time" \
    -parallel 1 2>&1 | tee -a "$fuzz_log"
done <"$expected_fuzz"

inventory="formal/inventory.json"
inventory_schema="schemas/formal-inventory.schema.json"
{
  go run ./cmd/hideout-schema-validate "$inventory_schema" "$inventory"
  printf 'formal-inventory=passed sha256=%s\n' "$(sha256_file "$inventory")"
} >"$run_dir/formal-inventory.log" 2>&1

migration_configs="$scratch/migration-configs"
jq -c '.configurations[] | select(.id | startswith("Migration"))' \
  "$inventory" >"$migration_configs"
if [ "$(wc -l <"$migration_configs" | tr -d ' ')" -ne 4 ]; then
  printf 'migration-gate: formal inventory must contain four migration configurations\n' >&2
  exit 1
fi

tla_version="$(jq -er '.tla2tools.version' "$inventory")"
tla_sha="$(jq -er '.tla2tools.sha256' "$inventory")"
tla_url="$(jq -er '.tla2tools.url' "$inventory")"
tla_cache="${HIDEOUT_TLA_CACHE:-${HOME:-/tmp}/.cache/hideout/tla}"
tla_jar="${TLA2TOOLS_JAR:-$tla_cache/tla2tools-$tla_version.jar}"
if [ ! -f "$tla_jar" ]; then
  mkdir -p "$tla_cache"
  tla_download="$scratch/tla2tools.jar"
  curl --fail --location --silent --show-error "$tla_url" --output "$tla_download"
  if [ "$(sha256_file "$tla_download")" != "$tla_sha" ]; then
    printf 'migration-gate: downloaded tla2tools.jar digest mismatch\n' >&2
    exit 1
  fi
  mv "$tla_download" "$tla_jar"
fi
if [ "$(sha256_file "$tla_jar")" != "$tla_sha" ]; then
  printf 'migration-gate: tla2tools.jar digest mismatch\n' >&2
  exit 1
fi

: >"$scratch/tlc-results.jsonl"
while IFS= read -r entry; do
  id="$(jq -er '.id' <<<"$entry")"
  module="$(jq -er '.module' <<<"$entry")"
  config="$(jq -er '.config' <<<"$entry")"
  if [ ! -f "$module" ] || [ -L "$module" ] ||
    [ ! -f "$config" ] || [ -L "$config" ]; then
    printf 'migration-gate: unsafe or missing model/config for %s\n' "$id" >&2
    exit 1
  fi
  printf 'migration-gate: checking %s\n' "$id"
  tlc_log="$run_dir/tlc/$id.log"
  java -XX:+UseSerialGC -cp "$tla_jar" tlc2.TLC \
    -deadlock \
    -workers 1 \
    -metadir "$scratch/tlc-$id" \
    -config "$config" \
    "$module" >"$tlc_log" 2>&1
  if ! grep -Fq \
    'Model checking completed. No error has been found.' "$tlc_log"; then
    printf 'migration-gate: TLC success marker missing for %s\n' "$id" >&2
    tail -40 "$tlc_log" >&2
    exit 1
  fi
  if grep -Eiq \
    'Invariant .* is violated|Temporal properties were violated|counterexample|^Error:' \
    "$tlc_log"; then
    printf 'migration-gate: TLC counterexample/error marker found for %s\n' "$id" >&2
    exit 1
  fi
  jq -nc \
    --arg id "$id" \
    --arg path "tlc/$id.log" \
    --arg sha256 "$(sha256_file "$tlc_log")" '
      {id:$id,path:$path,sha256:$sha256,result:"passed"}
    ' >>"$scratch/tlc-results.jsonl"
done <"$migration_configs"
tlc_results="$(jq -s . "$scratch/tlc-results.jsonl")"

expected_refinements="$scratch/expected-refinements"
discovered_refinements="$scratch/discovered-refinements"
jq -r '
  .goRefinement.tests[] |
  select(.source == "internal/migration/state_refinement_test.go") |
  .name
' "$inventory" | LC_ALL=C sort >"$expected_refinements"
sed -n 's/^func \(Test[A-Za-z0-9_]*\).*/\1/p' \
  internal/migration/state_refinement_test.go | LC_ALL=C sort \
  >"$discovered_refinements"
if [ "$(wc -l <"$expected_refinements" | tr -d ' ')" -ne 7 ] ||
  [ -n "$(comm -3 "$expected_refinements" "$discovered_refinements")" ]; then
  printf 'migration-gate: refinement inventory drifted\n' >&2
  comm -3 "$expected_refinements" "$discovered_refinements" >&2
  exit 1
fi
refinement_pattern="$({
  awk 'BEGIN {first=1} {
    if (!first) {
      printf "|"
    }
    printf "%s", $0
    first=0
  } END {printf "\n"}' "$expected_refinements"
})"
printf 'migration-gate: running seven Go refinement traces\n'
go test -json ./internal/migration \
  -run "^(${refinement_pattern})$" \
  -count=1 >"$refinement_log"
if jq -e 'select(.Action == "fail")' "$refinement_log" >/dev/null; then
  printf 'migration-gate: refinement stream contains a failure\n' >&2
  exit 1
fi
jq -r '
  select(.Action == "pass" and (.Test // "") != "") |
  .Test
' "$refinement_log" | LC_ALL=C sort -u >"$scratch/passed-refinements"
if [ -n "$(comm -3 "$expected_refinements" "$scratch/passed-refinements")" ]; then
  printf 'migration-gate: refinement pass set is incomplete or unexpected\n' >&2
  comm -3 "$expected_refinements" "$scratch/passed-refinements" >&2
  exit 1
fi

if find "$run_dir" -type f ! -size +0 -print -quit | grep -q .; then
  printf 'migration-gate: retained evidence contains an empty artifact\n' >&2
  find "$run_dir" -type f ! -size +0 -print >&2
  exit 1
fi
chmod 0600 "$run_dir"/*.log "$run_dir"/*.json "$run_dir"/*.jsonl "$run_dir"/tlc/*.log
source_commit="$(git rev-parse HEAD)"
if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then
  source_dirty=true
else
  source_dirty=false
fi
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" \
  --arg fuzzTime "$fuzz_time" \
  --arg schemaSHA256 "$(sha256_file "$schema_log")" \
  --arg portableSHA256 "$(sha256_file "$portable_log")" \
  --arg fuzzSHA256 "$(sha256_file "$fuzz_log")" \
  --arg formalInventorySHA256 "$(sha256_file "$inventory")" \
  --arg formalInventoryValidationSHA256 "$(sha256_file "$run_dir/formal-inventory.log")" \
  --arg refinementSHA256 "$(sha256_file "$refinement_log")" \
  --arg migrationTestInventorySHA256 "$(sha256_file "$migration_test_inventory")" \
  --arg migrationTestResultsSHA256 "$(sha256_file "$migration_test_log")" \
  --arg hostileTestInventorySHA256 "$(sha256_file "$hostile_test_inventory")" \
  --arg hostileTestResultsSHA256 "$(sha256_file "$hostile_mutation_log")" \
  --arg crashCutInventorySHA256 "$(sha256_file "$crash_cut_inventory")" \
  --arg crashCutResultsSHA256 "$(sha256_file "$crash_cut_log")" \
  --argjson migrationTestCount "$migration_test_count" \
  --argjson migrationPackageCount "$migration_package_count" \
  --argjson hostileTestCount "$hostile_test_count" \
  --argjson crashCutCount "$crash_cut_count" \
  --argjson tlcResults "$tlc_results" \
  '{
    schema: "hideout.migration-foundation-gate/v1",
    generatedAt: $generatedAt,
    source: {commit: $commit, dirty: $dirty},
    result: "passed",
    checks: {
      schemas: {count: 4, logSHA256: $schemaSHA256},
      portablePackages: {count: $migrationPackageCount, logSHA256: $portableSHA256},
      migrationTests: {
        count: $migrationTestCount,
        inventorySHA256: $migrationTestInventorySHA256,
        resultsSHA256: $migrationTestResultsSHA256
      },
      hostileMutations: {
        count: $hostileTestCount,
        inventorySHA256: $hostileTestInventorySHA256,
        resultsSHA256: $hostileTestResultsSHA256
      },
      durableCrashCuts: {
        count: $crashCutCount,
        inventorySHA256: $crashCutInventorySHA256,
        resultsSHA256: $crashCutResultsSHA256
      },
      fuzzTargets: {count: 6, timeEach: $fuzzTime, logSHA256: $fuzzSHA256},
      formalConfigurations: {
        count: 4,
        inventorySHA256: $formalInventorySHA256,
        validationLogSHA256: $formalInventoryValidationSHA256,
        results: $tlcResults
      },
      refinementTests: {count: 7, logSHA256: $refinementSHA256}
    },
    claimBoundary:
      "Local portable-core and bounded-model evidence only; no VM, real backend, physical-host transfer, or release candidate was exercised."
  }' >"$run_dir/summary.json"
chmod 0600 "$run_dir/summary.json"
if ! jq -e '
  .schema == "hideout.migration-foundation-gate/v1" and
  .result == "passed" and
  .checks.schemas.count == 4 and
  .checks.portablePackages.count == 13 and
  .checks.migrationTests.count == 191 and
  .checks.hostileMutations.count == 9 and
  .checks.durableCrashCuts.count == 13 and
  .checks.fuzzTargets.count == 6 and
  .checks.formalConfigurations.count == 4 and
  (.checks.formalConfigurations.results | length) == 4 and
  ([.checks.formalConfigurations.results[].id] | unique | length) == 4 and
  all(.checks.formalConfigurations.results[];
    .result == "passed" and (.sha256 | test("^[a-f0-9]{64}$"))) and
  .checks.refinementTests.count == 7
' "$run_dir/summary.json" >/dev/null; then
  printf 'migration-gate: generated summary failed validation\n' >&2
  exit 1
fi

# shellcheck disable=SC2034 # consumed by the sourced EXIT guard
gate_completed=1
printf 'migration-gate: passed evidence=%s\n' "$run_dir/summary.json"
