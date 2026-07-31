#!/usr/bin/env bash
set -euo pipefail

root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd -P)"
cd "$root"
# shellcheck source=scripts/lib/gate-result.sh
. "$root/scripts/lib/gate-result.sh"
gate_completed=0

summary=""
evidence_root=""
core_only=false
inventory="$root/formal/inventory.json"

usage() {
  printf '%s\n' \
    "Usage: scripts/gates/formal-verify.sh --summary FILE [--evidence-root DIR]" \
    "" \
    "Fail-closed verifier for one scripts/gates/formal.sh evidence summary." \
    "The internal --core-only mode is reserved for the gate's false-green tests."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --summary)
      if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
        printf 'formal-verify: --summary requires a file\n' >&2
        exit 2
      fi
      summary="$2"
      shift 2
      ;;
    --evidence-root)
      if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
        printf 'formal-verify: --evidence-root requires a directory\n' >&2
        exit 2
      fi
      evidence_root="$2"
      shift 2
      ;;
    --core-only)
      core_only=true
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'formal-verify: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

fail() {
  printf 'formal-verify: %s\n' "$1" >&2
  exit 1
}

for command in awk comm find git grep jq sed sort stat; do
  command -v "$command" >/dev/null 2>&1 ||
    fail "missing-command:$command"
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
  fail "missing-sha256-tool"
}

file_mode() {
  local path="$1"
  stat -f '%Lp' "$path" 2>/dev/null ||
    stat -c '%a' "$path" 2>/dev/null ||
    fail "cannot-read-mode:$path"
}

safe_evidence_path() {
  local path="$1"
  case "$path" in
    '' | /* | *'..'* | *'//'* | *[!A-Za-z0-9._/-]*)
      return 1
      ;;
  esac
  return 0
}

json_config_names() {
  local keyword="$1"
  local config="$2"
  awk -v keyword="$keyword" '$1 == keyword {print $2}' "$config" |
    jq -R . |
    jq -s .
}

[ -n "$summary" ] || fail "summary-required"
[ -f "$summary" ] || fail "summary-not-found"
[ ! -L "$summary" ] || fail "summary-is-symlink"
[ -f "$inventory" ] || fail "inventory-not-found"

if [ -z "$evidence_root" ]; then
  evidence_root="$(dirname "$summary")"
fi
[ -d "$evidence_root" ] || fail "evidence-root-not-found"
evidence_root="$(CDPATH='' cd -- "$evidence_root" && pwd -P)"

scratch="$(mktemp -d "${TMPDIR:-/tmp}/hideout-formal-verify.XXXXXX")"
cleanup() {
  local exit_status=$?
  rm -rf -- "$scratch"
  if [ "$exit_status" -eq 0 ]; then
    gate_require_completion "formal-verify"
  fi
}
trap cleanup EXIT

if ! jq -e '
  .schema == "hideout.formal-gate/v1" and
  .result == "passed" and
  .scope == "bounded-local-formal-preflight" and
  .candidateAcceptance == false and
  (.source.commit | type == "string") and
  (.source.dirty | type == "boolean") and
  (.run | type == "string") and
  (.limitation | type == "string" and length > 0)
' "$summary" >/dev/null; then
  fail "invalid-summary-envelope"
fi

recorded_commit="$(jq -er '.source.commit' "$summary")"
[ "$recorded_commit" = "$(git rev-parse HEAD)" ] ||
  fail "source-commit-mismatch"

run_id="$(jq -er '.run' "$summary")"
case "$run_id" in
  run-[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]T[0-9][0-9][0-9][0-9][0-9][0-9]Z-[0-9]*)
    ;;
  *)
    fail "invalid-run-id"
    ;;
esac
run_dir="$evidence_root/$run_id"
[ -d "$run_dir" ] || fail "run-directory-not-found"
[ ! -L "$run_dir" ] || fail "run-directory-is-symlink"

inventory_sha="$(sha256_file "$inventory")"
[ "$(jq -er '.inventory.sha256' "$summary")" = "$inventory_sha" ] ||
  fail "inventory-digest-mismatch"
inventory_path="$(jq -er '.inventory.path' "$summary")"
safe_evidence_path "$inventory_path" || fail "unsafe-inventory-path"
[ "$inventory_path" = "$run_id/inventory.json" ] ||
  fail "inventory-path-mismatch"
retained_inventory="$evidence_root/$inventory_path"
[ -f "$retained_inventory" ] || fail "retained-inventory-not-found"
[ ! -L "$retained_inventory" ] || fail "retained-inventory-is-symlink"
[ "$(sha256_file "$retained_inventory")" = "$inventory_sha" ] ||
  fail "retained-inventory-digest-mismatch"

jq -r '.configurations[].config' "$inventory" | LC_ALL=C sort \
  >"$scratch/inventory-configs"
find formal -type f -name '*.cfg' -print | LC_ALL=C sort \
  >"$scratch/repository-configs"
if [ -n "$(comm -3 "$scratch/inventory-configs" "$scratch/repository-configs")" ]; then
  fail "repository-configuration-inventory-mismatch"
fi
jq -r '.configurations[].module' "$inventory" | LC_ALL=C sort -u \
  >"$scratch/inventory-modules"
find formal -maxdepth 1 -type f -name '*.tla' -print | LC_ALL=C sort \
  >"$scratch/repository-modules"
if [ -n "$(comm -3 "$scratch/inventory-modules" "$scratch/repository-modules")" ]; then
  fail "repository-module-inventory-mismatch"
fi
if ! jq -e '
  (.configurations | length) ==
    (.configurations | map(.id) | unique | length) and
  (.configurations | length) ==
    (.configurations | map(.config) | unique | length) and
  (.goRefinement.tests | length) ==
    (.goRefinement.tests | map(.package + "::" + .name) | unique | length)
' "$inventory" >/dev/null; then
  fail "inventory-identity-duplicate"
fi

jq -r '
  .goRefinement.tests[] |
  select(.classification == "refinement") |
  .source
' "$inventory" | LC_ALL=C sort -u >"$scratch/refinement-sources"
while IFS= read -r source; do
  [ -f "$source" ] && [ ! -L "$source" ] ||
    fail "go-refinement-source-not-found:$source"
  sed -n 's/^func \(Test[A-Za-z0-9_]*\).*/\1/p' "$source" |
    LC_ALL=C sort >"$scratch/discovered-tests"
  jq -r --arg source "$source" '
    .goRefinement.tests[] |
    select(.classification == "refinement" and .source == $source) |
    .name
  ' "$inventory" | LC_ALL=C sort >"$scratch/inventoried-tests"
  if [ -n "$(comm -3 "$scratch/inventoried-tests" "$scratch/discovered-tests")" ]; then
    fail "go-refinement-inventory-mismatch:$source"
  fi
done <"$scratch/refinement-sources"
while IFS= read -r entry; do
  source="$(jq -er '.source' <<<"$entry")"
  test_name="$(jq -er '.name' <<<"$entry")"
  [ -f "$source" ] && [ ! -L "$source" ] ||
    fail "go-test-source-not-found:$test_name"
  if ! sed -n 's/^func \(Test[A-Za-z0-9_]*\).*/\1/p' "$source" |
    grep -Fxq "$test_name"; then
    fail "go-test-not-found:$test_name"
  fi
done < <(jq -c '.goRefinement.tests[]' "$inventory")

printf '%s\n' \
  scripts/gates/formal.sh \
  scripts/gates/formal-verify.sh \
  schemas/formal-inventory.schema.json |
  LC_ALL=C sort >"$scratch/expected-gate-sources"
jq -r '.gateSources[].path' "$summary" |
  LC_ALL=C sort >"$scratch/observed-gate-sources"
if [ -n "$(comm -3 "$scratch/expected-gate-sources" "$scratch/observed-gate-sources")" ]; then
  fail "gate-source-set-mismatch"
fi
while IFS= read -r source; do
  [ -f "$source" ] && [ ! -L "$source" ] ||
    fail "gate-source-not-found:$source"
  recorded_sha="$(
    jq -er --arg source "$source" \
      '.gateSources[] | select(.path == $source) | .sha256' "$summary"
  )"
  [ "$recorded_sha" = "$(sha256_file "$source")" ] ||
    fail "gate-source-digest-mismatch:$source"
done <"$scratch/expected-gate-sources"

if ! jq -e --arg version "$(jq -r '.tla2tools.version' "$inventory")" \
  --arg sha "$(jq -r '.tla2tools.sha256' "$inventory")" '
    .tools.tla2tools.version == $version and
    .tools.tla2tools.sha256 == $sha and
    (.tools.java | type == "string" and length > 0) and
    (.tools.go | type == "string" and length > 0)
  ' "$summary" >/dev/null; then
  fail "tool-binding-mismatch"
fi

jq -r '.configurations[].id' "$inventory" | LC_ALL=C sort \
  >"$scratch/expected-configurations"
jq -r '.configurations[].id' "$summary" | LC_ALL=C sort \
  >"$scratch/observed-configurations"
if [ -n "$(comm -3 "$scratch/expected-configurations" "$scratch/observed-configurations")" ]; then
  fail "configuration-set-mismatch"
fi
if [ "$(wc -l <"$scratch/observed-configurations" | tr -d ' ')" -ne \
  "$(sort -u "$scratch/observed-configurations" | wc -l | tr -d ' ')" ]; then
  fail "duplicate-configuration-result"
fi

total_invariants=0
total_properties=0
while IFS= read -r entry; do
  id="$(jq -er '.id' <<<"$entry")"
  module="$(jq -er '.module' <<<"$entry")"
  config="$(jq -er '.config' <<<"$entry")"
  kind="$(jq -er '.kind' <<<"$entry")"
  result="$(
    jq -c --arg id "$id" \
      '.configurations[] | select(.id == $id)' "$summary"
  )"
  [ -n "$result" ] || fail "configuration-result-missing:$id"

  if ! jq -e \
    --arg module "$module" \
    --arg config "$config" \
    --arg kind "$kind" '
      .result == "passed" and
      .module == $module and
      .config == $config and
      .kind == $kind
    ' <<<"$result" >/dev/null; then
    fail "configuration-result-invalid:$id"
  fi
  [ "$(jq -er '.counterexamples' <<<"$result")" -eq 0 ] ||
    fail "counterexamples-present:$id"

  [ -f "$module" ] && [ ! -L "$module" ] ||
    fail "model-not-found:$id"
  [ -f "$config" ] && [ ! -L "$config" ] ||
    fail "config-not-found:$id"
  [ "$(jq -er '.moduleSHA256' <<<"$result")" = \
    "$(sha256_file "$module")" ] ||
    fail "model-digest-mismatch:$id"
  [ "$(jq -er '.configSHA256' <<<"$result")" = \
    "$(sha256_file "$config")" ] ||
    fail "config-digest-mismatch:$id"

  expected_invariants="$(json_config_names INVARIANT "$config")"
  expected_properties="$(json_config_names PROPERTY "$config")"
  if ! jq -e \
    --argjson invariants "$expected_invariants" \
    --argjson properties "$expected_properties" '
      .invariants == $invariants and
      .properties == $properties
    ' <<<"$result" >/dev/null; then
    fail "property-inventory-mismatch:$id"
  fi
  total_invariants=$((total_invariants + $(jq 'length' <<<"$expected_invariants")))
  total_properties=$((total_properties + $(jq 'length' <<<"$expected_properties")))

  log_path="$(jq -er '.log.path' <<<"$result")"
  safe_evidence_path "$log_path" || fail "unsafe-log-path:$id"
  case "$log_path" in
    "$run_id"/tlc/*)
      ;;
    *)
      fail "log-path-mismatch:$id"
      ;;
  esac
  log="$evidence_root/$log_path"
  [ -f "$log" ] && [ ! -L "$log" ] ||
    fail "log-not-found:$id"
  [ "$(jq -er '.log.sha256' <<<"$result")" = "$(sha256_file "$log")" ] ||
    fail "log-digest-mismatch:$id"
  grep -Fq 'Model checking completed. No error has been found.' "$log" ||
    fail "tlc-success-marker-missing:$id"
  if grep -Eiq \
    'Invariant .* is violated|Temporal properties were violated|counterexample|^Error:' \
    "$log"; then
    fail "tlc-counterexample-marker:$id"
  fi
done < <(jq -c '.configurations[]' "$inventory")

module_count="$(
  jq -r '.configurations[].module' "$inventory" |
    LC_ALL=C sort -u |
    wc -l |
    tr -d ' '
)"
configuration_count="$(jq '.configurations | length' "$inventory")"
go_test_count="$(jq '.goRefinement.tests | length' "$inventory")"
if ! jq -e \
  --argjson configurations "$configuration_count" \
  --argjson modules "$module_count" \
  --argjson invariants "$total_invariants" \
  --argjson properties "$total_properties" \
  --argjson go_tests "$go_test_count" '
    .inventory.configurationCount == $configurations and
    .inventory.moduleCount == $modules and
    .inventory.invariantCount == $invariants and
    .inventory.propertyCount == $properties and
    .inventory.goTestCount == $go_tests
  ' "$summary" >/dev/null; then
  fail "inventory-count-mismatch"
fi

jq -r '
  .goRefinement.tests[] |
  .package + "::" + .name
' "$inventory" | LC_ALL=C sort >"$scratch/expected-go-tests"
jq -r '
  .goRefinement.tests[] |
  .package + "::" + .name
' "$summary" | LC_ALL=C sort >"$scratch/observed-go-tests"
if [ -n "$(comm -3 "$scratch/expected-go-tests" "$scratch/observed-go-tests")" ]; then
  fail "go-test-set-mismatch"
fi
if ! jq -e '
  .goRefinement.result == "passed" and
  all(.goRefinement.tests[]; .result == "passed")
' "$summary" >/dev/null; then
  fail "go-refinement-not-passed"
fi

go_json_path="$(jq -er '.goRefinement.jsonLog.path' "$summary")"
for log_kind in jsonLog humanLog; do
  log_path="$(jq -er --arg kind "$log_kind" '.goRefinement[$kind].path' "$summary")"
  safe_evidence_path "$log_path" || fail "unsafe-go-log-path:$log_kind"
  case "$log_path" in
    "$run_id"/go/*)
      ;;
    *)
      fail "go-log-path-mismatch:$log_kind"
      ;;
  esac
  log="$evidence_root/$log_path"
  [ -f "$log" ] && [ ! -L "$log" ] ||
    fail "go-log-not-found:$log_kind"
  [ "$(jq -er --arg kind "$log_kind" \
    '.goRefinement[$kind].sha256' "$summary")" = \
    "$(sha256_file "$log")" ] ||
    fail "go-log-digest-mismatch:$log_kind"
done

go_json="$evidence_root/$go_json_path"
if jq -e 'select(.Action == "fail")' "$go_json" >/dev/null; then
  fail "go-refinement-stream-failed"
fi
jq -r '
  select(
    .Action == "pass" and
    (.Test // "") != "" and
    (.Test | contains("/") | not)
  ) |
  .Package + "::" + .Test
' "$go_json" | LC_ALL=C sort -u >"$scratch/passed-go-tests"
if [ -n "$(comm -3 "$scratch/expected-go-tests" "$scratch/passed-go-tests")" ]; then
  fail "go-test-pass-set-mismatch"
fi

jq -r '.goRefinement.packages[].importPath' "$inventory" |
  LC_ALL=C sort >"$scratch/expected-go-packages"
jq -r 'select(.Action == "pass" and (.Test // "") == "") | .Package' \
  "$go_json" | LC_ALL=C sort -u >"$scratch/passed-go-packages"
if [ -n "$(comm -23 "$scratch/expected-go-packages" "$scratch/passed-go-packages")" ]; then
  fail "go-package-pass-set-incomplete"
fi

jq -r '.goRefinement.tests[].source' "$inventory" |
  LC_ALL=C sort -u >"$scratch/expected-go-sources"
jq -r '.goRefinement.sources[].path' "$summary" |
  LC_ALL=C sort >"$scratch/observed-go-sources"
if [ -n "$(comm -3 "$scratch/expected-go-sources" "$scratch/observed-go-sources")" ]; then
  fail "go-source-set-mismatch"
fi
while IFS= read -r source; do
  [ -f "$source" ] && [ ! -L "$source" ] ||
    fail "go-source-not-found:$source"
  recorded_sha="$(
    jq -er --arg source "$source" \
      '.goRefinement.sources[] | select(.path == $source) | .sha256' \
      "$summary"
  )"
  [ "$recorded_sha" = "$(sha256_file "$source")" ] ||
    fail "go-source-digest-mismatch:$source"
done <"$scratch/expected-go-sources"

if [ "$core_only" = true ]; then
  gate_completed=1
  printf 'formal-verify: core evidence accepted\n'
  exit 0
fi

jq -r '.negativeJudgeProofs[].id' "$summary" |
  LC_ALL=C sort >"$scratch/observed-negative-proofs"
printf '%s\n' \
  add-counterexample \
  omit-required-configuration \
  stale-model-digest |
  LC_ALL=C sort >"$scratch/expected-negative-proofs"
if [ -n "$(comm -3 "$scratch/expected-negative-proofs" "$scratch/observed-negative-proofs")" ]; then
  fail "negative-proof-set-mismatch"
fi
if ! jq -e '
  all(.negativeJudgeProofs[];
    .result == "killed" and
    (.expectedDiagnostic | type == "string" and length > 0)
  )
' "$summary" >/dev/null; then
  fail "negative-proof-not-killed"
fi
while IFS= read -r proof; do
  proof_id="$(jq -er '.id' <<<"$proof")"
  for evidence_kind in fixture log; do
    path="$(jq -er --arg kind "$evidence_kind" '.[$kind].path' <<<"$proof")"
    safe_evidence_path "$path" ||
      fail "unsafe-negative-proof-path:$proof_id:$evidence_kind"
    case "$path" in
      "$run_id"/judge/*)
        ;;
      *)
        fail "negative-proof-path-mismatch:$proof_id:$evidence_kind"
        ;;
    esac
    file="$evidence_root/$path"
    [ -f "$file" ] && [ ! -L "$file" ] ||
      fail "negative-proof-file-not-found:$proof_id:$evidence_kind"
    [ "$(jq -er --arg kind "$evidence_kind" '.[$kind].sha256' <<<"$proof")" = \
      "$(sha256_file "$file")" ] ||
      fail "negative-proof-digest-mismatch:$proof_id:$evidence_kind"
  done
  grep -Fq "$(jq -er '.expectedDiagnostic' <<<"$proof")" \
    "$evidence_root/$(jq -er '.log.path' <<<"$proof")" ||
    fail "negative-proof-diagnostic-missing:$proof_id"
done < <(jq -c '.negativeJudgeProofs[]' "$summary")

find "$run_dir" -type l -print -quit | grep -q . &&
  fail "artifact-symlink-present"
find "$run_dir" -type f -print |
  sed "s#^$evidence_root/##" |
  LC_ALL=C sort >"$scratch/actual-artifacts"
jq -r '.artifacts[].path' "$summary" |
  LC_ALL=C sort >"$scratch/recorded-artifacts"
if [ -n "$(comm -3 "$scratch/actual-artifacts" "$scratch/recorded-artifacts")" ]; then
  fail "artifact-set-mismatch"
fi
if [ "$(wc -l <"$scratch/recorded-artifacts" | tr -d ' ')" -ne \
  "$(sort -u "$scratch/recorded-artifacts" | wc -l | tr -d ' ')" ]; then
  fail "duplicate-artifact"
fi
while IFS= read -r artifact; do
  safe_evidence_path "$artifact" || fail "unsafe-artifact-path:$artifact"
  case "$artifact" in
    "$run_id"/*)
      ;;
    *)
      fail "artifact-path-mismatch:$artifact"
      ;;
  esac
  file="$evidence_root/$artifact"
  [ -f "$file" ] && [ ! -L "$file" ] ||
    fail "artifact-not-found:$artifact"
  recorded_sha="$(
    jq -er --arg path "$artifact" \
      '.artifacts[] | select(.path == $path) | .sha256' "$summary"
  )"
  [ "$recorded_sha" = "$(sha256_file "$file")" ] ||
    fail "artifact-digest-mismatch:$artifact"
  [ "$(file_mode "$file")" = "600" ] ||
    fail "artifact-mode-mismatch:$artifact"
done <"$scratch/recorded-artifacts"

[ "$(file_mode "$summary")" = "600" ] ||
  fail "summary-mode-mismatch"

gate_completed=1
printf \
  'formal-verify: accepted configurations=%s invariants=%s properties=%s goTests=%s\n' \
  "$configuration_count" "$total_invariants" "$total_properties" "$go_test_count"
