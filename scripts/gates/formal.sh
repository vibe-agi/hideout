#!/usr/bin/env bash
set -euo pipefail

root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd -P)"
cd "$root"
# shellcheck source=scripts/lib/gate-result.sh
. "$root/scripts/lib/gate-result.sh"
gate_completed=0
gate_started_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
gate_started_epoch="$(date +%s)"
gate_stage="preflight"
gate_current_configuration=""
gate_current_configuration_started_epoch=0
gate_configuration_timings='[]'
gate_review_written=0

out="$root/.artifacts/045/formal"
inventory="$root/formal/inventory.json"
inventory_schema="$root/schemas/formal-inventory.schema.json"
verifier="$root/scripts/gates/formal-verify.sh"
selected_configuration=""
tlc_workers="${HIDEOUT_TLC_WORKERS:-1}"

usage() {
  printf '%s\n' \
    "Usage: scripts/gates/formal.sh [--out DIR] [--workers N]" \
    "       scripts/gates/formal.sh --configuration ID [--workers N]" \
    "" \
    "Runs every repository TLC configuration, every inventoried Go formal/" \
    "refinement test, and false-green verifier fixtures. Writes digest-bound" \
    "local evidence; it does not accept or publish a release candidate." \
    "" \
    "  --configuration ID  Run one inventoried TLC model for diagnosis only." \
    "                      This never emits full formal acceptance evidence." \
    "  --workers N         Use 1..64 TLC workers (default: 1, or" \
    "                      HIDEOUT_TLC_WORKERS when set)."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --out)
      if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
        printf 'formal-gate: --out requires a directory\n' >&2
        exit 2
      fi
      out="$2"
      shift 2
      ;;
    --configuration)
      if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
        printf 'formal-gate: --configuration requires an ID\n' >&2
        exit 2
      fi
      selected_configuration="$2"
      shift 2
      ;;
    --workers)
      if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
        printf 'formal-gate: --workers requires a number\n' >&2
        exit 2
      fi
      tlc_workers="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'formal-gate: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

case "$tlc_workers" in
  '' | *[!0-9]* | 0 | 0*)
    printf 'formal-gate: workers must be an integer from 1 through 64\n' >&2
    exit 2
    ;;
esac
if [ "$tlc_workers" -lt 1 ] || [ "$tlc_workers" -gt 64 ]; then
  printf 'formal-gate: workers must be an integer from 1 through 64\n' >&2
  exit 2
fi

for command in awk comm curl git go java jq sed sort; do
  if ! command -v "$command" >/dev/null 2>&1; then
    printf 'formal-gate: missing required command: %s\n' "$command" >&2
    exit 1
  fi
done

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  printf 'formal-gate: missing shasum or sha256sum\n' >&2
  return 127
}

json_config_names() {
  local keyword="$1"
  local config="$2"
  awk -v keyword="$keyword" '$1 == keyword {print $2}' "$config" |
    jq -R . |
    jq -s .
}

safe_numeric_stat() {
  local value="$1"
  local label="$2"
  case "$value" in
    '' | *[!0-9]*)
      printf 'formal-gate: TLC log omitted numeric %s\n' "$label" >&2
      exit 1
      ;;
  esac
  printf '%s\n' "$value"
}

source_commit="$(git rev-parse HEAD)"
if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then
  source_dirty=true
else
  source_dirty=false
fi

mkdir -p "$out"
out="$(CDPATH='' cd -- "$out" && pwd -P)"
run_id="run-$(date -u +'%Y%m%dT%H%M%SZ')-$$"
run_dir="$out/$run_id"
review_dir="$out/reviews/$run_id"
mkdir -p "$run_dir/tlc" "$run_dir/go" "$run_dir/judge" "$review_dir"
chmod 0700 \
  "$out" "$out/reviews" "$run_dir" "$run_dir/tlc" "$run_dir/go" \
  "$run_dir/judge" "$review_dir"
run_review="$review_dir/run-review.json"

write_formal_run_review() {
  local result="$1" failure_layer="${2:-}" failure_reason="${3:-}"
  local finished_at finished_epoch elapsed_seconds current_elapsed review_tmp
  finished_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
  finished_epoch="$(date +%s)"
  elapsed_seconds=$((finished_epoch - gate_started_epoch))
  current_elapsed=0
  if [ -n "$gate_current_configuration" ] &&
    [ "$gate_current_configuration_started_epoch" -gt 0 ]; then
    current_elapsed=$((finished_epoch - gate_current_configuration_started_epoch))
  fi
  review_tmp="$out/.run-review.$$.json"
  jq -n \
    --arg result "$result" \
    --arg run "$run_id" \
    --arg commit "$source_commit" \
    --argjson dirty "$source_dirty" \
    --arg startedAt "$gate_started_at" \
    --arg finishedAt "$finished_at" \
    --arg stage "$gate_stage" \
    --arg configuration "$gate_current_configuration" \
    --arg selectedConfiguration "$selected_configuration" \
    --arg failureLayer "$failure_layer" \
    --arg failureReason "$failure_reason" \
    --argjson workers "$tlc_workers" \
    --argjson elapsedSeconds "$elapsed_seconds" \
    --argjson currentElapsedSeconds "$current_elapsed" \
    --argjson configurations "$gate_configuration_timings" '
      {
        schema:"hideout.gate-run-review/v1",
        gate:"formal",
        run:$run,
        result:$result,
        candidate:{commit:$commit,dirty:$dirty},
        start:{
          mode:"from-scratch",
          reason:"formal evidence has no authenticated cross-run checkpoint",
          checkpointReused:false,
          resultReused:false,
          restartRequired:false,
          powerCycleRequired:false
        },
        execution:{
          scope:(if $selectedConfiguration == "" then "full-formal" else "single-configuration-diagnostic" end),
          selectedConfiguration:(if $selectedConfiguration == "" then null else $selectedConfiguration end),
          workers:$workers,
          stage:$stage,
          currentConfiguration:(if $configuration == "" then null else $configuration end),
          currentConfigurationElapsedSeconds:$currentElapsedSeconds,
          completedConfigurations:$configurations
        },
        timing:{startedAt:$startedAt,finishedAt:$finishedAt,elapsedSeconds:$elapsedSeconds},
        failure:(if $result == "failed" then {
          firstObservedLayer:$failureLayer,
          reason:$failureReason
        } else null end),
        rerun:(if $result == "failed" then {
          minimumDiagnosticScope:(if $configuration == "" then "failed-stage-only" else ("configuration:" + $configuration) end),
          diagnosticCommand:(if $configuration == "" then null else ("scripts/gates/formal.sh --configuration " + $configuration + " --workers " + ($workers|tostring)) end),
          releaseAcceptanceScope:"full-formal",
          afterCandidateChange:"from-scratch"
        } else null end),
        efficiency:{
          authenticatedCheckpointHitRate:0,
          preventableWorkAssessment:"pending-post-run-review",
          metrics:["elapsedSeconds","completedConfigurations","currentConfigurationElapsedSeconds"]
        }
      }
    ' >"$review_tmp"
  chmod 0600 "$review_tmp"
  mv "$review_tmp" "$run_review"
  gate_review_written=1
}

scratch="$(mktemp -d "${TMPDIR:-/tmp}/hideout-formal-gate.XXXXXX")"
cleanup() {
  local exit_status=$?
  if [ "$exit_status" -ne 0 ]; then
    write_formal_run_review \
      failed "$gate_stage" \
      "formal gate exited before the current stage completed" || true
  elif [ "$gate_review_written" -eq 0 ]; then
    write_formal_run_review passed "" "" || true
  fi
  rm -rf -- "$scratch"
  if [ "$exit_status" -eq 0 ]; then
    gate_require_completion "formal-gate"
  fi
}
trap cleanup EXIT

printf \
  'formal-gate: start=from-scratch checkpointReused=false workers=%s restartRequired=false powerCycleRequired=false review=%s\n' \
  "$tlc_workers" "$run_review"

go run ./cmd/hideout-schema-validate \
  "$inventory_schema" "$inventory" >"$run_dir/inventory-schema.log" 2>&1
printf 'formal inventory schema: passed\n' >>"$run_dir/inventory-schema.log"
cp "$inventory" "$run_dir/inventory.json"

inventory_sha="$(sha256_file "$inventory")"
tla_version="$(jq -er '.tla2tools.version' "$inventory")"
tla_sha="$(jq -er '.tla2tools.sha256' "$inventory")"
tla_url="$(jq -er '.tla2tools.url' "$inventory")"
tla_cache="${HIDEOUT_TLA_CACHE:-${HOME:-/tmp}/.cache/hideout/tla}"
tla_jar="${TLA2TOOLS_JAR:-$tla_cache/tla2tools-$tla_version.jar}"

if [ ! -f "$tla_jar" ]; then
  mkdir -p "$(dirname "$tla_jar")"
  tla_download="$scratch/tla2tools.jar"
  curl --fail --location --silent --show-error \
    "$tla_url" --output "$tla_download"
  if [ "$(sha256_file "$tla_download")" != "$tla_sha" ]; then
    printf 'formal-gate: downloaded tla2tools.jar digest mismatch\n' >&2
    exit 1
  fi
  mv "$tla_download" "$tla_jar"
fi
if [ "$(sha256_file "$tla_jar")" != "$tla_sha" ]; then
  printf 'formal-gate: tla2tools.jar digest mismatch\n' >&2
  exit 1
fi

jq -r '.configurations[].config' "$inventory" | LC_ALL=C sort \
  >"$scratch/inventory-configs"
find formal -type f -name '*.cfg' -print | LC_ALL=C sort \
  >"$scratch/repository-configs"
if [ -n "$(comm -3 "$scratch/inventory-configs" "$scratch/repository-configs")" ]; then
  printf 'formal-gate: configuration inventory drifted from repository\n' >&2
  comm -3 "$scratch/inventory-configs" "$scratch/repository-configs" >&2
  exit 1
fi

jq -r '.configurations[].module' "$inventory" | LC_ALL=C sort -u \
  >"$scratch/inventory-modules"
find formal -maxdepth 1 -type f -name '*.tla' -print | LC_ALL=C sort \
  >"$scratch/repository-modules"
if [ -n "$(comm -3 "$scratch/inventory-modules" "$scratch/repository-modules")" ]; then
  printf 'formal-gate: module inventory drifted from repository\n' >&2
  comm -3 "$scratch/inventory-modules" "$scratch/repository-modules" >&2
  exit 1
fi

if ! jq -e '
  .schema == "hideout.formal-inventory/v1" and
  (.configurations | length) ==
    (.configurations | map(.id) | unique | length) and
  (.configurations | length) ==
    (.configurations | map(.config) | unique | length) and
  (.goRefinement.tests | length) ==
    (.goRefinement.tests | map(.package + "::" + .name) | unique | length) and
  (.goRefinement.packages | length) ==
    (.goRefinement.packages | map(.importPath) | unique | length)
' "$inventory" >/dev/null; then
  printf 'formal-gate: inventory contains duplicate or invalid identities\n' >&2
  exit 1
fi

if [ -n "$selected_configuration" ] && ! jq -e \
  --arg id "$selected_configuration" \
  'any(.configurations[]; .id == $id)' "$inventory" >/dev/null; then
  printf 'formal-gate: unknown configuration: %s\n' \
    "$selected_configuration" >&2
  exit 2
fi

configuration_entries() {
  if [ -n "$selected_configuration" ]; then
    jq -c --arg id "$selected_configuration" \
      '.configurations[] | select(.id == $id)' "$inventory"
    return
  fi
  jq -c '.configurations[]' "$inventory"
}

tlc_results='[]'
configuration_count=0
total_invariants=0
total_properties=0
while IFS= read -r entry; do
  id="$(jq -er '.id' <<<"$entry")"
  module="$(jq -er '.module' <<<"$entry")"
  config="$(jq -er '.config' <<<"$entry")"
  kind="$(jq -er '.kind' <<<"$entry")"
  log="$run_dir/tlc/$id.log"
  gate_stage="formal-model"
  gate_current_configuration="$id"
  gate_current_configuration_started_epoch="$(date +%s)"

  if [ ! -f "$module" ] || [ -L "$module" ] ||
    [ ! -f "$config" ] || [ -L "$config" ]; then
    printf 'formal-gate: unsafe or missing model/config for %s\n' "$id" >&2
    exit 1
  fi

  printf 'formal-gate: checking %s (%s)\n' "$id" "$kind"
  java -XX:+UseParallelGC -cp "$tla_jar" tlc2.TLC \
    -deadlock \
    -workers "$tlc_workers" \
    -metadir "$scratch/tlc-$id" \
    -config "$config" \
    "$module" >"$log" 2>&1

  if ! grep -Fq \
    'Model checking completed. No error has been found.' "$log"; then
    printf 'formal-gate: TLC success marker missing for %s\n' "$id" >&2
    tail -40 "$log" >&2
    exit 1
  fi
  if ! grep -Eq " with ${tlc_workers} workers? on " "$log"; then
    printf 'formal-gate: TLC worker marker mismatch for %s\n' "$id" >&2
    exit 1
  fi
  if grep -Eiq \
    'Invariant .* is violated|Temporal properties were violated|counterexample|^Error:' \
    "$log"; then
    printf 'formal-gate: TLC counterexample/error marker found for %s\n' "$id" >&2
    exit 1
  fi

  invariants="$(json_config_names INVARIANT "$config")"
  properties="$(json_config_names PROPERTY "$config")"
  invariant_count="$(jq 'length' <<<"$invariants")"
  property_count="$(jq 'length' <<<"$properties")"
  if [ "$kind" = "safety-liveness" ] && [ "$property_count" -eq 0 ]; then
    printf 'formal-gate: %s is liveness-classified without PROPERTY checks\n' "$id" >&2
    exit 1
  fi
  if [ "$kind" = "safety" ] && [ "$property_count" -ne 0 ]; then
    printf 'formal-gate: %s has unclassified PROPERTY checks\n' "$id" >&2
    exit 1
  fi

  generated="$(
    awk '/states generated/ {value=$1}
      END {gsub(/,/, "", value); print value}' "$log"
  )"
  distinct="$(
    awk '/states generated/ {
      for (field = 1; field <= NF; field++) {
        if ($field == "distinct") {
          value = $(field - 1)
        }
      }
    }
    END {gsub(/,/, "", value); print value}' "$log"
  )"
  depth="$(
    awk '/depth of the complete state graph/ {value=$NF}
      END {gsub(/[.]/, "", value); print value}' "$log"
  )"
  generated="$(safe_numeric_stat "$generated" "generated-state count for $id")"
  distinct="$(safe_numeric_stat "$distinct" "distinct-state count for $id")"
  depth="$(safe_numeric_stat "$depth" "state-graph depth for $id")"
  configuration_elapsed_seconds=$((
    $(date +%s) - gate_current_configuration_started_epoch
  ))

  relative_log="${log#"$out"/}"
  tlc_results="$(
    jq -c \
      --arg id "$id" \
      --arg module "$module" \
      --arg config "$config" \
      --arg kind "$kind" \
      --arg module_sha "$(sha256_file "$module")" \
      --arg config_sha "$(sha256_file "$config")" \
      --arg log "$relative_log" \
      --arg log_sha "$(sha256_file "$log")" \
      --argjson invariants "$invariants" \
      --argjson properties "$properties" \
      --argjson generated "$generated" \
      --argjson distinct "$distinct" \
      --argjson depth "$depth" \
      --argjson workers "$tlc_workers" \
      --argjson elapsed_seconds "$configuration_elapsed_seconds" \
      '. + [{
        id: $id,
        module: $module,
        config: $config,
        kind: $kind,
        result: "passed",
        moduleSHA256: $module_sha,
        configSHA256: $config_sha,
        invariants: $invariants,
        properties: $properties,
        counterexamples: 0,
        workers: $workers,
        elapsedSeconds: $elapsed_seconds,
        states: {
          generated: $generated,
          distinct: $distinct,
          depth: $depth
        },
        log: {path: $log, sha256: $log_sha}
      }]' <<<"$tlc_results"
  )"
  configuration_count=$((configuration_count + 1))
  total_invariants=$((total_invariants + invariant_count))
  total_properties=$((total_properties + property_count))
  gate_configuration_timings="$(
    jq -c \
      --arg id "$id" \
      --argjson elapsedSeconds "$configuration_elapsed_seconds" \
      --argjson generated "$generated" \
      --argjson distinct "$distinct" \
      '. + [{id:$id,result:"passed",elapsedSeconds:$elapsedSeconds,
        generatedStates:$generated,distinctStates:$distinct}]' \
      <<<"$gate_configuration_timings"
  )"
  printf \
    'formal-gate: configuration=%s result=passed elapsedSeconds=%s generated=%s distinct=%s\n' \
    "$id" "$configuration_elapsed_seconds" "$generated" "$distinct"
  gate_current_configuration=""
  gate_current_configuration_started_epoch=0
done < <(configuration_entries)

if [ -n "$selected_configuration" ]; then
  gate_stage="diagnostic-complete"
  write_formal_run_review passed "" ""
  gate_completed=1
  printf \
    'formal-gate: diagnostic=passed configuration=%s workers=%s evidenceAcceptance=false review=%s\n' \
    "$selected_configuration" "$tlc_workers" "$run_review"
  exit 0
fi

gate_stage="go-refinement"
refinement_sources="$scratch/refinement-sources"
jq -r '
  .goRefinement.tests[] |
  select(.classification == "refinement") |
  .source
' "$inventory" | LC_ALL=C sort -u >"$refinement_sources"
while IFS= read -r source; do
  sed -n 's/^func \(Test[A-Za-z0-9_]*\).*/\1/p' "$source" |
    LC_ALL=C sort >"$scratch/discovered-tests"
  jq -r --arg source "$source" '
    .goRefinement.tests[] |
    select(.classification == "refinement" and .source == $source) |
    .name
  ' "$inventory" | LC_ALL=C sort >"$scratch/inventoried-tests"
  if [ -n "$(comm -3 "$scratch/inventoried-tests" "$scratch/discovered-tests")" ]; then
    printf 'formal-gate: Go refinement inventory drifted for %s\n' "$source" >&2
    comm -3 "$scratch/inventoried-tests" "$scratch/discovered-tests" >&2
    exit 1
  fi
done <"$refinement_sources"

while IFS= read -r entry; do
  source="$(jq -er '.source' <<<"$entry")"
  name="$(jq -er '.name' <<<"$entry")"
  if ! sed -n 's/^func \(Test[A-Za-z0-9_]*\).*/\1/p' "$source" |
    grep -Fxq "$name"; then
    printf 'formal-gate: inventoried Go test is missing: %s\n' "$name" >&2
    exit 1
  fi
done < <(jq -c '.goRefinement.tests[]' "$inventory")

test_pattern="$(
  jq -r '.goRefinement.tests[].name' "$inventory" |
    awk 'BEGIN {first=1} {
      if (!first) {
        printf "|"
      }
      printf "%s", $0
      first=0
    } END {printf "\n"}'
)"
packages=()
while IFS= read -r package; do
  packages+=("$package")
done < <(jq -r '.goRefinement.packages[].path' "$inventory")

printf 'formal-gate: running %s Go formal/refinement tests\n' \
  "$(jq '.goRefinement.tests | length' "$inventory")"
go test -json "${packages[@]}" \
  -run "^(${test_pattern})$" \
  -count=1 >"$run_dir/go/refinement.json"

if jq -e 'select(.Action == "fail")' "$run_dir/go/refinement.json" \
  >/dev/null; then
  printf 'formal-gate: Go refinement stream contains a failure\n' >&2
  exit 1
fi

jq -r '
  select(
    .Action == "pass" and
    (.Test // "") != "" and
    (.Test | contains("/") | not)
  ) |
  .Package + "::" + .Test
' "$run_dir/go/refinement.json" | LC_ALL=C sort -u \
  >"$scratch/passed-go-tests"
jq -r '
  .goRefinement.tests[] |
  .package + "::" + .name
' "$inventory" | LC_ALL=C sort >"$scratch/expected-go-tests"
if [ -n "$(comm -3 "$scratch/expected-go-tests" "$scratch/passed-go-tests")" ]; then
  printf 'formal-gate: Go refinement pass set is incomplete or unexpected\n' >&2
  comm -3 "$scratch/expected-go-tests" "$scratch/passed-go-tests" >&2
  exit 1
fi

jq -r '
  select(
    .Action == "pass" and
    (.Test // "") != "" and
    (.Test | contains("/") | not)
  ) |
  "PASS " + .Package + "::" + .Test
' "$run_dir/go/refinement.json" >"$run_dir/go/refinement.log"
printf 'Go formal/refinement tests: %s passed\n' \
  "$(wc -l <"$scratch/passed-go-tests" | tr -d ' ')" \
  >>"$run_dir/go/refinement.log"

go_tests='[]'
while IFS= read -r entry; do
  go_tests="$(
    jq -c --argjson test "$entry" \
      '. + [$test + {result: "passed"}]' <<<"$go_tests"
  )"
done < <(jq -c '.goRefinement.tests[]' "$inventory")

go_sources='[]'
while IFS= read -r source; do
  go_sources="$(
    jq -c \
      --arg path "$source" \
      --arg sha256 "$(sha256_file "$source")" \
      '. + [{path: $path, sha256: $sha256}]' <<<"$go_sources"
  )"
done < <(jq -r '.goRefinement.tests[].source' "$inventory" | LC_ALL=C sort -u)

java_version="$(java -version 2>&1 | sed -n '1p')"
go_version="$(go version)"

write_summary() {
  local destination="$1"
  local proofs="$2"
  local artifacts="$3"
  jq -n \
    --arg generated_at "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
    --arg commit "$source_commit" \
    --argjson dirty "$source_dirty" \
    --arg run "$run_id" \
    --arg inventory_path "$run_id/inventory.json" \
    --arg inventory_sha "$inventory_sha" \
    --arg tla_version "$tla_version" \
    --arg tla_sha "$tla_sha" \
    --arg java_version "$java_version" \
    --arg go_version "$go_version" \
    --argjson tlc_workers "$tlc_workers" \
    --argjson elapsed_seconds "$(( $(date +%s) - gate_started_epoch ))" \
    --argjson configuration_count "$configuration_count" \
    --argjson module_count "$(wc -l <"$scratch/inventory-modules" | tr -d ' ')" \
    --argjson invariant_count "$total_invariants" \
    --argjson property_count "$total_properties" \
    --argjson go_test_count "$(jq '.goRefinement.tests | length' "$inventory")" \
    --argjson configurations "$tlc_results" \
    --argjson go_tests "$go_tests" \
    --argjson go_sources "$go_sources" \
    --argjson packages "$(jq -c '.goRefinement.packages' "$inventory")" \
    --arg go_json "$run_id/go/refinement.json" \
    --arg go_json_sha "$(sha256_file "$run_dir/go/refinement.json")" \
    --arg go_log "$run_id/go/refinement.log" \
    --arg go_log_sha "$(sha256_file "$run_dir/go/refinement.log")" \
    --arg gate_sha "$(sha256_file "$root/scripts/gates/formal.sh")" \
    --arg verifier_sha "$(sha256_file "$verifier")" \
    --arg inventory_schema_sha "$(sha256_file "$inventory_schema")" \
    --argjson proofs "$proofs" \
    --argjson artifacts "$artifacts" \
    '{
      schema: "hideout.formal-gate/v1",
      generatedAt: $generated_at,
      source: {commit: $commit, dirty: $dirty},
      result: "passed",
      scope: "bounded-local-formal-preflight",
      candidateAcceptance: false,
      run: $run,
      tools: {
        tla2tools: {version: $tla_version, sha256: $tla_sha},
        tlcWorkers: $tlc_workers,
        java: $java_version,
        go: $go_version
      },
      execution: {
        startMode: "from-scratch",
        checkpointReused: false,
        restartRequired: false,
        powerCycleRequired: false,
        elapsedSeconds: $elapsed_seconds
      },
      gateSources: [
        {
          path: "scripts/gates/formal.sh",
          sha256: $gate_sha
        },
        {
          path: "scripts/gates/formal-verify.sh",
          sha256: $verifier_sha
        },
        {
          path: "schemas/formal-inventory.schema.json",
          sha256: $inventory_schema_sha
        }
      ],
      inventory: {
        path: $inventory_path,
        sha256: $inventory_sha,
        configurationCount: $configuration_count,
        moduleCount: $module_count,
        invariantCount: $invariant_count,
        propertyCount: $property_count,
        goTestCount: $go_test_count
      },
      configurations: $configurations,
      goRefinement: {
        result: "passed",
        packages: $packages,
        tests: $go_tests,
        sources: $go_sources,
        jsonLog: {path: $go_json, sha256: $go_json_sha},
        humanLog: {path: $go_log, sha256: $go_log_sha}
      },
      negativeJudgeProofs: $proofs,
      artifacts: $artifacts,
      limitation:
        "This proves the checked-in bounded models and local Go refinement traces only. It is dirty-aware, is not real-Lima proof, and does not accept or publish a release candidate."
    }' >"$destination"
}

preliminary="$scratch/preliminary-summary.json"
gate_stage="evidence-judge"
write_summary "$preliminary" '[]' '[]'
baseline_output="$(
  "$verifier" \
    --summary "$preliminary" \
    --evidence-root "$out" \
    --core-only 2>&1
)"
printf '%s\n' "$baseline_output" >"$run_dir/judge/baseline.log"

negative_proofs='[]'
for mutation in \
  omit-required-configuration \
  add-counterexample \
  stale-model-digest \
  worker-count-mismatch; do
  fixture="$scratch/$mutation.json"
  case "$mutation" in
    omit-required-configuration)
      jq '.configurations |= .[1:]' "$preliminary" >"$fixture"
      diagnostic='formal-verify: configuration-set-mismatch'
      ;;
    add-counterexample)
      jq '.configurations[0].counterexamples = 1' \
        "$preliminary" >"$fixture"
      diagnostic='formal-verify: counterexamples-present:AttachReservation'
      ;;
    stale-model-digest)
      jq '.configurations[0].moduleSHA256 = ("0" * 64)' \
        "$preliminary" >"$fixture"
      diagnostic='formal-verify: model-digest-mismatch:AttachReservation'
      ;;
    worker-count-mismatch)
      jq '
        (if .tools.tlcWorkers == 1 then 2 else 1 end) as $forged |
        .tools.tlcWorkers = $forged |
        .configurations |= map(.workers = $forged)
      ' "$preliminary" >"$fixture"
      diagnostic='formal-verify: tlc-worker-marker-mismatch:AttachReservation'
      ;;
  esac

  set +e
  mutation_output="$(
    "$verifier" \
      --summary "$fixture" \
      --evidence-root "$out" \
      --core-only 2>&1
  )"
  mutation_status=$?
  set -e
  if [ "$mutation_status" -eq 0 ] ||
    ! grep -Fq "$diagnostic" <<<"$mutation_output"; then
    printf 'formal-gate: false-green fixture was not killed: %s\n' \
      "$mutation" >&2
    printf '%s\n' "$mutation_output" >&2
    exit 1
  fi

  retained_fixture="$run_dir/judge/$mutation.json"
  retained_log="$run_dir/judge/$mutation.log"
  cp "$fixture" "$retained_fixture"
  printf '%s\n' "$mutation_output" >"$retained_log"
  negative_proofs="$(
    jq -c \
      --arg id "$mutation" \
      --arg diagnostic "$diagnostic" \
      --arg fixture "${retained_fixture#"$out"/}" \
      --arg fixture_sha "$(sha256_file "$retained_fixture")" \
      --arg log "${retained_log#"$out"/}" \
      --arg log_sha "$(sha256_file "$retained_log")" \
      '. + [{
        id: $id,
        result: "killed",
        expectedDiagnostic: $diagnostic,
        fixture: {path: $fixture, sha256: $fixture_sha},
        log: {path: $log, sha256: $log_sha}
      }]' <<<"$negative_proofs"
  )"
done

find "$run_dir" -type f -exec chmod 0600 {} +
artifacts='[]'
while IFS= read -r evidence_file; do
  relative_path="${evidence_file#"$out"/}"
  artifacts="$(
    jq -c \
      --arg path "$relative_path" \
      --arg sha256 "$(sha256_file "$evidence_file")" \
      '. + [{path: $path, sha256: $sha256}]' <<<"$artifacts"
  )"
done < <(find "$run_dir" -type f | LC_ALL=C sort)

summary="$out/summary.json"
write_summary "$summary" "$negative_proofs" "$artifacts"
chmod 0600 "$summary"
"$verifier" --summary "$summary" --evidence-root "$out"

gate_stage="complete"
write_formal_run_review passed "" ""
gate_completed=1
printf \
  'formal-gate: passed configurations=%d modules=%s invariants=%d properties=%d goTests=%s workers=%s elapsedSeconds=%s evidence=%s review=%s\n' \
  "$configuration_count" \
  "$(wc -l <"$scratch/inventory-modules" | tr -d ' ')" \
  "$total_invariants" \
  "$total_properties" \
  "$(jq '.goRefinement.tests | length' "$inventory")" \
  "$tlc_workers" \
  "$(( $(date +%s) - gate_started_epoch ))" \
  "$summary" \
  "$run_review"
