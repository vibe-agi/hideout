#!/usr/bin/env bash
set -euo pipefail

root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd -P)"
cd "$root"
# shellcheck source=scripts/lib/gate-result.sh
. "$root/scripts/lib/gate-result.sh"
gate_completed=0

out="$root/.artifacts/045/local"
inventory="$root/scripts/gates/release-candidate-inventory.json"

usage() {
  printf '%s\n' \
    "Usage: scripts/gates/release-candidate.sh [--out DIR]" \
    "" \
    "Runs the complete local unit, race, fuzz/property, schema, generated," \
    "static, dependency/advisory, migration, and mutation aggregate. Every lane runs and" \
    "writes private digest-bound evidence even when another lane fails." \
    "This command never publishes or accepts an exact release candidate."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --out)
      if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
        printf 'release-candidate-local: --out requires a directory\n' >&2
        exit 2
      fi
      out="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'release-candidate-local: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

for command in \
  awk bash cmp comm find git go gofmt grep jq markdownlint-cli2 sed shellcheck \
  sort stat tr wc xargs; do
  if ! command -v "$command" >/dev/null 2>&1; then
    printf 'release-candidate-local: missing required command: %s\n' \
      "$command" >&2
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
  printf 'release-candidate-local: missing shasum or sha256sum\n' >&2
  return 127
}

file_mode() {
  stat -f '%Lp' "$1" 2>/dev/null ||
    stat -c '%a' "$1" 2>/dev/null
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
mkdir -p \
  "$run_dir/lanes" \
  "$run_dir/dependencies" \
  "$run_dir/mutations/production" \
  "$run_dir/mutations/recovery" \
  "$run_dir/mutations/judge-negative-fixtures"
chmod 0700 \
  "$out" \
  "$run_dir" \
  "$run_dir/lanes" \
  "$run_dir/dependencies" \
  "$run_dir/mutations" \
  "$run_dir/mutations/production" \
  "$run_dir/mutations/recovery" \
  "$run_dir/mutations/judge-negative-fixtures"

scratch="$(mktemp -d "${TMPDIR:-/tmp}/hideout-release-local.XXXXXX")"
cleanup() {
  local exit_status=$?
  rm -rf -- "$scratch"
  if [ "$exit_status" -eq 0 ]; then
    gate_require_completion "release-candidate-local"
  fi
}
trap cleanup EXIT

cp "$inventory" "$run_dir/inventory.json"

if ! jq -e '
  .schema == "hideout.local-release-candidate-inventory/v1" and
  .requiredGoVersion == "go1.25.12" and
  (.requiredLanes | length) == 10 and
  (.requiredLanes | length) == (.requiredLanes | unique | length) and
  (.shellLint | length) >= 30 and
  (.shellLint | length) == (.shellLint | unique | length) and
  all(.shellLint[];
    type == "string" and
    test("^[A-Za-z0-9._/-]+[.]sh$")) and
  (.markdownLint | length) >= 30 and
  (.markdownLint | length) == (.markdownLint | unique | length) and
  all(.markdownLint[];
    type == "string" and
    test("^[A-Za-z0-9._/-]+[.]md$")) and
  (.fuzzTests | length) >= 3 and
  (.fuzzTests | length) ==
    (.fuzzTests | map(.importPath + "::" + .name) | unique | length) and
  all(
    .claimContracts,
    .claimMatrix,
    .productionMutationManifest,
    .productionMutationRunner,
    .recoveryMutationRunner,
    .negativeFixtureRunner;
    type == "string" and length > 0
  )
' "$inventory" >/dev/null; then
  printf 'release-candidate-local: invalid lane inventory\n' >&2
  exit 1
fi

required_go_version="$(jq -er '.requiredGoVersion' "$inventory")"
actual_go_version="$(go env GOVERSION)"
if [ "$actual_go_version" != "$required_go_version" ]; then
  printf 'release-candidate-local: requires %s, got %s\n' \
    "$required_go_version" "$actual_go_version" >&2
  exit 1
fi

lanes='[]'
failed_lanes=0
run_lane() {
  local id="$1"
  shift
  local log="$run_dir/lanes/$id.log"
  local started_at finished_at status result
  started_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
  printf 'release-candidate-local: running %s\n' "$id"
  set +e
  (
    set -e
    "$@"
  ) >"$log" 2>&1
  status=$?
  set -e
  finished_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
  if [ ! -s "$log" ]; then
    printf 'lane produced no output (exit=%d)\n' "$status" >"$log"
  fi
  chmod 0600 "$log"
  if [ "$status" -eq 0 ]; then
    result="passed"
    printf 'release-candidate-local: %s passed\n' "$id"
  else
    result="failed"
    failed_lanes=$((failed_lanes + 1))
    printf 'release-candidate-local: %s failed (exit=%d)\n' \
      "$id" "$status" >&2
    tail -30 "$log" >&2
  fi
  lanes="$(
    jq -c \
      --arg id "$id" \
      --arg result "$result" \
      --arg started_at "$started_at" \
      --arg finished_at "$finished_at" \
      --arg path "$run_id/lanes/$id.log" \
      --arg sha256 "$(sha256_file "$log")" \
      --argjson exit_code "$status" \
      '. + [{
        id: $id,
        result: $result,
        exitCode: $exit_code,
        startedAt: $started_at,
        finishedAt: $finished_at,
        log: {path: $path, sha256: $sha256}
      }]' <<<"$lanes"
  )"
}

unit_lane() {
  go test -json -p 4 -count=1 ./...
}

race_lane() {
  go test -json -race -p 2 -count=1 ./...
}

fuzz_property_lane() {
  jq -r '.fuzzTests[].source' "$inventory" | LC_ALL=C sort -u \
    >"$scratch/expected-fuzz-sources"
  find cmd internal schemas test -type f -name '*_test.go' -print0 |
    xargs -0 sed -n \
      's/^func \(Fuzz[A-Za-z0-9_]*\).*/\1/p' |
    LC_ALL=C sort >"$scratch/discovered-fuzz-names"
  jq -r '.fuzzTests[].name' "$inventory" | LC_ALL=C sort \
    >"$scratch/expected-fuzz-names"
  if [ -n "$(
    comm -3 "$scratch/expected-fuzz-names" "$scratch/discovered-fuzz-names"
  )" ]; then
    printf 'fuzz inventory drifted from repository\n' >&2
    comm -3 "$scratch/expected-fuzz-names" "$scratch/discovered-fuzz-names" >&2
    return 1
  fi

  while IFS= read -r entry; do
    package="$(jq -er '.package' <<<"$entry")"
    name="$(jq -er '.name' <<<"$entry")"
    source="$(jq -er '.source' <<<"$entry")"
    fuzz_time="$(jq -er '.fuzzTime' <<<"$entry")"
    if [ ! -f "$source" ] || [ -L "$source" ] ||
      ! grep -Eq "^func $name\\(" "$source"; then
      printf 'missing fuzz harness %s in %s\n' "$name" "$source" >&2
      return 1
    fi
    printf 'fuzz-property: %s %s (%s)\n' "$package" "$name" "$fuzz_time"
    go test "$package" \
      -run '^$' \
      -fuzz "^${name}$" \
      -fuzztime="$fuzz_time" \
      -parallel=2 ||
      return
  done < <(jq -c '.fuzzTests[]' "$inventory")
}

schema_lane() {
  jq empty formal/inventory.json scripts/gates/release-candidate-inventory.json
  jq empty schemas/*.json
  go test -json ./schemas -count=1
}

generated_lane() {
  local clang="${HIDEOUT_BPF_CLANG:-}"
  local llvm_strip="${HIDEOUT_BPF_LLVM_STRIP:-}"
  if [ -z "$clang" ] &&
    [ -x /opt/homebrew/opt/llvm@19/bin/clang ]; then
    clang=/opt/homebrew/opt/llvm@19/bin/clang
  fi
  if [ -z "$llvm_strip" ] &&
    [ -x /opt/homebrew/opt/llvm@19/bin/llvm-strip ]; then
    llvm_strip=/opt/homebrew/opt/llvm@19/bin/llvm-strip
  fi
  if [ -z "$clang" ] || [ -z "$llvm_strip" ]; then
    printf 'pinned LLVM 19.1.7 commands are unavailable\n' >&2
    return 1
  fi
  HIDEOUT_BPF_CLANG="$clang" \
    HIDEOUT_BPF_LLVM_STRIP="$llvm_strip" \
    scripts/gates/generated.sh
}

static_lane() {
  local lint_path
  go build ./...
  go vet ./...

  find cmd internal schemas test tools -type f -name '*.go' -print0 |
    xargs -0 gofmt -l |
    LC_ALL=C sort >"$scratch/unformatted-go"
  if [ -s "$scratch/unformatted-go" ]; then
    printf 'gofmt required for:\n' >&2
    cat "$scratch/unformatted-go" >&2
    return 1
  fi

  if ! go mod tidy -diff >"$scratch/tidy.diff" 2>&1; then
    printf 'go.mod/go.sum are not tidy:\n' >&2
    cat "$scratch/tidy.diff" >&2
    return 1
  fi
  if [ -s "$scratch/tidy.diff" ]; then
    printf 'go mod tidy unexpectedly emitted a diff:\n' >&2
    cat "$scratch/tidy.diff" >&2
    return 1
  fi

  while IFS= read -r script; do
    bash -n "$script" || return
  done < <(find scripts -type f -name '*.sh' | LC_ALL=C sort)
  jq -r '.shellLint[]' "$inventory" >"$scratch/shell-lint-files"
  while IFS= read -r lint_path; do
    [ -f "$lint_path" ] && [ ! -L "$lint_path" ] || {
      printf 'shell lint inventory path is missing or unsafe: %s\n' \
        "$lint_path" >&2
      return 1
    }
  done <"$scratch/shell-lint-files"
  xargs shellcheck -x <"$scratch/shell-lint-files"
  jq -r '.markdownLint[]' "$inventory" >"$scratch/markdown-lint-files"
  while IFS= read -r lint_path; do
    [ -f "$lint_path" ] && [ ! -L "$lint_path" ] || {
      printf 'Markdown lint inventory path is missing or unsafe: %s\n' \
        "$lint_path" >&2
      return 1
    }
  done <"$scratch/markdown-lint-files"
  xargs markdownlint-cli2 <"$scratch/markdown-lint-files"
  sed -E -n \
    's/^- \*\*(FR-[0-9]+a?)\*\*:.*/\1/p' \
    specs/045-operator-observability-console/spec.md |
    LC_ALL=C sort >"$scratch/spec-functional-requirements"
  sed -E -n \
    's/^\| (FR-[0-9]+a?) \|.*/\1/p' \
    specs/045-operator-observability-console/checklists/acceptance.md |
    LC_ALL=C sort >"$scratch/accepted-functional-requirements"
  sed -E -n \
    's/^- \*\*(SC-[0-9]+)\*\*:.*/\1/p' \
    specs/045-operator-observability-console/spec.md |
    LC_ALL=C sort >"$scratch/spec-success-criteria"
  sed -E -n \
    's/^\| (SC-[0-9]+) \|.*/\1/p' \
    specs/045-operator-observability-console/checklists/acceptance.md |
    LC_ALL=C sort >"$scratch/accepted-success-criteria"
  if [ "$(wc -l <"$scratch/spec-functional-requirements" | tr -d ' ')" -ne 72 ] ||
    [ "$(wc -l <"$scratch/spec-success-criteria" | tr -d ' ')" -ne 15 ] ||
    ! cmp -s \
      "$scratch/spec-functional-requirements" \
      "$scratch/accepted-functional-requirements" ||
    ! cmp -s \
      "$scratch/spec-success-criteria" \
      "$scratch/accepted-success-criteria"; then
    printf 'Feature 045 acceptance identifier set drifted from the spec\n' >&2
    comm -3 \
      "$scratch/spec-functional-requirements" \
      "$scratch/accepted-functional-requirements" >&2 || true
    comm -3 \
      "$scratch/spec-success-criteria" \
      "$scratch/accepted-success-criteria" >&2 || true
    return 1
  fi
  git diff --check
  printf \
    'build, vet, gofmt, module tidy, shell/Markdown lint, syntax, and diff checks passed\n'
}

dependencies_advisory_lane() {
  scripts/gates/dependencies.sh --out "$run_dir/dependencies"
}

mutations_lane() {
  local production_out="$run_dir/mutations/production"
  local recovery_out="$run_dir/mutations/recovery"
  local negative_out="$run_dir/mutations/judge-negative-fixtures"
  local contracts matrix manifest production_runner recovery_runner
  local negative_runner required_count negative_run_id negative_summary_rel
  local negative_run_summary negative_run_dir
  local evidence_path evidence_sha expected_path evidence_spec remainder
  contracts="$(jq -er '.claimContracts' "$inventory")"
  matrix="$(jq -er '.claimMatrix' "$inventory")"
  manifest="$(jq -er '.productionMutationManifest' "$inventory")"
  production_runner="$(jq -er '.productionMutationRunner' "$inventory")"
  recovery_runner="$(jq -er '.recoveryMutationRunner' "$inventory")"
  negative_runner="$(jq -er '.negativeFixtureRunner' "$inventory")"

  for input in \
    "$contracts" \
    "$matrix" \
    "$manifest" \
    "$production_runner" \
    "$recovery_runner" \
    "$negative_runner"; do
    if [ ! -f "$input" ] || [ -L "$input" ]; then
      printf 'mutation input is missing or symlinked: %s\n' "$input" >&2
      return 1
    fi
  done

  "$production_runner" --out "$production_out" || return
  "$recovery_runner" --out "$recovery_out" || return
  "$negative_runner" --out "$negative_out" ||
    return

  required_count="$(jq -er '.claims | length' "$contracts")"
  if ! jq -e \
    --slurpfile mutation_manifest "$manifest" \
    --arg manifest "$manifest" \
    --arg manifest_sha "$(sha256_file "$manifest")" \
    --arg contracts "$contracts" \
    --arg contracts_sha "$(sha256_file "$contracts")" \
    --argjson required "$required_count" '
      .schema == "hideout.045-production-mutation-run/v1" and
      .result == "passed" and
      .manifest == $manifest and
      .manifestSHA256 == $manifest_sha and
      .contracts == $contracts and
      .contractsSHA256 == $contracts_sha and
      .requiredClaims == $required and
      .executed == $required and
      .killed == $required and
      (.proofs | length) == $required and
      (.proofs | map(.id) | unique | length) == $required and
      (.proofs | map(.claimId) | unique | length) == $required and
      all(.proofs[];
        .result == "killed" and
        (.fromSHA256 | test("^[0-9a-f]{64}$")) and
        (.toSHA256 | test("^[0-9a-f]{64}$")) and
        .baseline.result == "passed" and
        .baseline.exitCode == 0 and
        (.baseline.passedTests | length) > 0 and
        .mutant.result == "failed" and
        .mutant.exitCode != 0 and
        (.mutant.failedTests | length) > 0
      ) and
      all(.proofs[];
        . as $proof |
        any($mutation_manifest[0].mutations[];
          .id == $proof.id and
          .claimId == $proof.claimId and
          .description == $proof.description and
          .source == $proof.source
        )
      ) and
      ((.errors // []) | length) == 0
    ' "$production_out/summary.json" >/dev/null; then
    printf 'source-overlay production mutation evidence is invalid\n' >&2
    return 1
  fi

  while IFS=$'\t' read -r mutation_id baseline_log baseline_sha \
    mutant_log mutant_sha; do
    expected_case="$production_out/$mutation_id"
    if [ "$baseline_log" != "$expected_case/baseline.log" ] ||
      [ "$mutant_log" != "$expected_case/mutant.log" ]; then
      printf 'production mutation log path escaped its case: %s\n' \
        "$mutation_id" >&2
      return 1
    fi
    for log_spec in \
      "$baseline_log:$baseline_sha" \
      "$mutant_log:$mutant_sha"; do
      mutation_log="${log_spec%%:*}"
      mutation_log_sha="${log_spec#*:}"
      if [ ! -f "$mutation_log" ] || [ -L "$mutation_log" ] ||
        [ "$(sha256_file "$mutation_log")" != "$mutation_log_sha" ]; then
        printf 'production mutation log is missing or digest-invalid: %s\n' \
          "$mutation_id" >&2
        return 1
      fi
    done

    jq -r \
      --arg id "$mutation_id" \
      '.mutations[] | select(.id == $id) | .killTests[]' \
      "$manifest" | LC_ALL=C sort -u \
      >"$scratch/$mutation_id-expected-kill-tests"
    jq -r 'select(.Action == "pass" and (.Test // "") != "") | .Test' \
      "$baseline_log" | LC_ALL=C sort -u \
      >"$scratch/$mutation_id-baseline-passed"
    jq -r 'select(.Action == "fail" and (.Test // "") != "") | .Test' \
      "$mutant_log" | LC_ALL=C sort -u \
      >"$scratch/$mutation_id-mutant-failed"
    jq -r \
      --arg id "$mutation_id" \
      '.proofs[] | select(.id == $id) | .baseline.passedTests[]' \
      "$production_out/summary.json" | LC_ALL=C sort -u \
      >"$scratch/$mutation_id-summary-passed"
    jq -r \
      --arg id "$mutation_id" \
      '.proofs[] | select(.id == $id) | .mutant.failedTests[]' \
      "$production_out/summary.json" | LC_ALL=C sort -u \
      >"$scratch/$mutation_id-summary-failed"

    if [ -n "$(
      comm -3 \
        "$scratch/$mutation_id-baseline-passed" \
        "$scratch/$mutation_id-summary-passed"
    )" ] ||
      [ -n "$(
        comm -3 \
          "$scratch/$mutation_id-mutant-failed" \
          "$scratch/$mutation_id-summary-failed"
      )" ] ||
      [ -z "$(
        comm -12 \
          "$scratch/$mutation_id-expected-kill-tests" \
          "$scratch/$mutation_id-baseline-passed"
      )" ] ||
      [ -z "$(
        comm -12 \
          "$scratch/$mutation_id-expected-kill-tests" \
          "$scratch/$mutation_id-mutant-failed"
      )" ]; then
      printf 'production mutation test-event evidence is invalid: %s\n' \
        "$mutation_id" >&2
      return 1
    fi
  done < <(
    jq -r '
      .proofs[] |
      [
        .id,
        .baseline.log,
        .baseline.logSHA256,
        .mutant.log,
        .mutant.logSHA256
      ] | @tsv
    ' "$production_out/summary.json"
  )

  if ! jq -e \
    --arg commit "$source_commit" \
    --argjson dirty "$source_dirty" '
      .schema == "hideout.recovery-gate-evidence/v1" and
      .source.commit == $commit and
      .source.dirty == $dirty and
      .result == "passed" and
      .crashMatrix.points == 16 and
      ([.checks | keys[]] | sort) == ["race", "unit"] and
      (.mutationProofs | length) == 3 and
      ([.mutationProofs[].id] | sort) == [
        "duplicate-terminal-event",
        "replay-running-effect",
        "success-without-proof"
      ] and
      all(.mutationProofs[]; .result == "killed")
    ' "$recovery_out/summary.json" >/dev/null; then
    printf 'recovery production mutation evidence is invalid\n' >&2
    return 1
  fi
  while IFS=$'\t' read -r check_id evidence_path evidence_sha; do
    expected_path="$check_id.log"
    if [ "$evidence_path" != "$expected_path" ] ||
      [ ! -f "$recovery_out/$evidence_path" ] ||
      [ -L "$recovery_out/$evidence_path" ] ||
      [ "$(sha256_file "$recovery_out/$evidence_path")" != "$evidence_sha" ]; then
      printf 'recovery check evidence is missing or invalid: %s\n' \
        "$check_id" >&2
      return 1
    fi
  done < <(
    jq -r '
      .checks | to_entries[] |
      [.key, .value.log, .value.sha256] | @tsv
    ' "$recovery_out/summary.json"
  )
  while IFS=$'\t' read -r mutation_id evidence_path evidence_sha; do
    expected_path="mutations/$mutation_id.log"
    if [ "$evidence_path" != "$expected_path" ] ||
      [ ! -f "$recovery_out/$evidence_path" ] ||
      [ -L "$recovery_out/$evidence_path" ] ||
      [ "$(sha256_file "$recovery_out/$evidence_path")" != "$evidence_sha" ]; then
      printf 'recovery mutation evidence is missing or invalid: %s\n' \
        "$mutation_id" >&2
      return 1
    fi
  done < <(
    jq -r '
      .mutationProofs[] | [.id, .log, .sha256] | @tsv
    ' "$recovery_out/summary.json"
  )

  negative_run_id="$(
    jq -er '
      .runId |
      select(test("^run-[0-9]{8}T[0-9]{6}Z-[0-9]+$"))
    ' "$negative_out/summary.json"
  )"
  negative_summary_rel="$(jq -er '.summary' "$negative_out/summary.json")"
  if [ "$negative_summary_rel" != "$negative_run_id/summary.json" ]; then
    printf 'judge-negative latest pointer is invalid\n' >&2
    return 1
  fi
  negative_run_summary="$negative_out/$negative_summary_rel"
  negative_run_dir="${negative_run_summary%/summary.json}"
  if [ ! -f "$negative_run_summary" ] || [ -L "$negative_run_summary" ] ||
    [ "$(sha256_file "$negative_run_summary")" != "$(
      jq -er '.sha256' "$negative_out/summary.json"
    )" ]; then
    printf 'judge-negative evidence is missing or digest-invalid\n' >&2
    return 1
  fi
  if ! jq -e \
    --arg contracts "$contracts" \
    --arg contracts_sha "$(sha256_file "$contracts")" \
    --arg matrix "$matrix" \
    --arg matrix_sha "$(sha256_file "$matrix")" \
    --arg commit "$source_commit" \
    --argjson dirty "$source_dirty" \
    --argjson required "$required_count" '
      .schema == "hideout.045-negative-fixture-evidence/v1" and
      .result == "passed" and
      .source.commit == $commit and
      .source.dirty == $dirty and
      .inputs.contracts == $contracts and
      .inputs.contractsSHA256 == $contracts_sha and
      .inputs.claimMatrix == $matrix and
      .inputs.claimMatrixSHA256 == $matrix_sha and
      .claimFamilies == $required and
      .restoredFixtures == $required and
      (.negativeFixtures | length) == $required and
      (.negativeFixtures | map(.id) | unique | length) == $required and
      (.negativeFixtures | map(.claimId) | unique | length) == $required and
      all(.negativeFixtures[];
        .id == ("N045-" + .claimId) and
        .result == "killed" and
        .restored.result == "passed"
      ) and
      .implementationMutationProofs.accepted == false and
      .claimAcceptance == false
    ' "$negative_run_summary" >/dev/null; then
    printf 'judge-negative mutation evidence is invalid\n' >&2
    return 1
  fi
  while IFS=$'\t' read -r fixture_id \
    negative_receipt negative_receipt_sha \
    negative_evidence negative_evidence_sha \
    negative_log negative_log_sha \
    restored_receipt restored_receipt_sha \
    restored_evidence restored_evidence_sha \
    restored_log restored_log_sha; do
    for evidence_spec in \
      "$negative_receipt:$negative_receipt_sha:$fixture_id/negative/receipt.json" \
      "$negative_evidence:$negative_evidence_sha:$fixture_id/negative/observation.json" \
      "$negative_log:$negative_log_sha:$fixture_id/negative/judge.log" \
      "$restored_receipt:$restored_receipt_sha:$fixture_id/restored/receipt.json" \
      "$restored_evidence:$restored_evidence_sha:$fixture_id/restored/observation.json" \
      "$restored_log:$restored_log_sha:$fixture_id/restored/judge.log"; do
      evidence_path="${evidence_spec%%:*}"
      remainder="${evidence_spec#*:}"
      evidence_sha="${remainder%%:*}"
      expected_path="${remainder#*:}"
      if [ "$evidence_path" != "$expected_path" ] ||
        [ ! -f "$negative_run_dir/$evidence_path" ] ||
        [ -L "$negative_run_dir/$evidence_path" ] ||
        [ "$(
          sha256_file "$negative_run_dir/$evidence_path"
        )" != "$evidence_sha" ]; then
        printf 'judge-negative fixture evidence is invalid: %s\n' \
          "$fixture_id" >&2
        return 1
      fi
    done
  done < <(
    jq -r '
      .negativeFixtures[] |
      [
        .id,
        .negative.receipt,
        .negative.receiptSHA256,
        .negative.evidence,
        .negative.evidenceSHA256,
        .negative.log,
        .negative.logSHA256,
        .restored.receipt,
        .restored.receiptSHA256,
        .restored.evidence,
        .restored.evidenceSHA256,
        .restored.log,
        .restored.logSHA256
      ] | @tsv
    ' "$negative_run_summary"
  )

  jq -r '.claims[].id' "$contracts" | LC_ALL=C sort \
    >"$scratch/required-mutation-claims"
  jq -r '.proofs[].claimId' "$production_out/summary.json" |
    LC_ALL=C sort -u >"$scratch/observed-mutation-claims"
  jq -r '.negativeFixtures[].claimId' "$negative_run_summary" |
    LC_ALL=C sort -u >"$scratch/negative-mutation-claims"
  comm -23 \
    "$scratch/required-mutation-claims" \
    "$scratch/observed-mutation-claims" \
    >"$scratch/missing-mutation-claims"
  comm -3 \
    "$scratch/required-mutation-claims" \
    "$scratch/negative-mutation-claims" \
    >"$scratch/invalid-negative-mutation-claims"

  proofs="$(
    jq -c \
      --arg production_summary "production/summary.json" \
      --arg production_sha "$(sha256_file "$production_out/summary.json")" '
        [
          .proofs[] | {
            id,
            claimId,
            description,
            source,
            fromSHA256,
            toSHA256,
            baseline: {
              logSHA256: .baseline.logSHA256,
              passedTests: .baseline.passedTests
            },
            mutant: {
              logSHA256: .mutant.logSHA256,
              failedTests: .mutant.failedTests
            },
            result: "killed",
            evidence: {
              path: $production_summary,
              sha256: $production_sha
            }
          }
        ]
      ' "$production_out/summary.json"
  )"
  jq -n \
    --arg generated_at "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
    --arg contracts "$contracts" \
    --arg contracts_sha "$(sha256_file "$contracts")" \
    --arg matrix "$matrix" \
    --arg matrix_sha "$(sha256_file "$matrix")" \
    --arg manifest "$manifest" \
    --arg manifest_sha "$(sha256_file "$manifest")" \
    --arg production_summary "production/summary.json" \
    --arg production_sha "$(sha256_file "$production_out/summary.json")" \
    --arg recovery_summary "recovery/summary.json" \
    --arg recovery_sha "$(sha256_file "$recovery_out/summary.json")" \
    --arg negative_latest "judge-negative-fixtures/summary.json" \
    --arg negative_latest_sha "$(sha256_file "$negative_out/summary.json")" \
    --arg negative_summary "judge-negative-fixtures/$negative_summary_rel" \
    --arg negative_sha "$(sha256_file "$negative_run_summary")" \
    --argjson required "$(wc -l <"$scratch/required-mutation-claims" | tr -d ' ')" \
    --argjson covered "$(wc -l <"$scratch/observed-mutation-claims" | tr -d ' ')" \
    --argjson proofs "$proofs" \
    --argjson missing "$(
      jq -R . <"$scratch/missing-mutation-claims" | jq -s .
    )" \
    '{
      schema: "hideout.045-production-mutation-aggregate/v1",
      generatedAt: $generated_at,
      result: (if ($missing | length) == 0 then "passed" else "failed" end),
      inputs: {
        contracts: $contracts,
        contractsSHA256: $contracts_sha,
        claimMatrix: $matrix,
        claimMatrixSHA256: $matrix_sha,
        mutationManifest: $manifest,
        mutationManifestSHA256: $manifest_sha
      },
      requiredClaimFamilies: $required,
      coveredClaimFamilies: $covered,
      proofs: $proofs,
      missingClaimFamilies: $missing,
      evidence: {
        production: {
          path: $production_summary,
          sha256: $production_sha
        },
        recovery: {
          path: $recovery_summary,
          sha256: $recovery_sha
        },
        judgeNegativeFixtures: {
          path: $negative_summary,
          sha256: $negative_sha
        },
        judgeNegativeLatest: {
          path: $negative_latest,
          sha256: $negative_latest_sha
        }
      },
      candidateAcceptance: false,
      limitation:
        "Every claim family has a killed source-overlay production mutant. Recovery trace mutants and judge-negative fixtures remain independent required evidence."
    }' >"$run_dir/mutations/production-summary.json"
  chmod 0600 "$run_dir/mutations/production-summary.json"

  if [ -s "$scratch/missing-mutation-claims" ] ||
    [ -s "$scratch/invalid-negative-mutation-claims" ] ||
    [ "$required_count" -ne 46 ]; then
    printf 'production mutation coverage is incomplete: '
    if [ -s "$scratch/missing-mutation-claims" ]; then
      paste -sd, "$scratch/missing-mutation-claims"
    elif [ -s "$scratch/invalid-negative-mutation-claims" ]; then
      paste -sd, "$scratch/invalid-negative-mutation-claims"
    else
      printf 'required=%s want=46\n' "$required_count"
    fi
    return 1
  fi
  if ! jq -e \
    --argjson required "$required_count" '
      .schema == "hideout.045-production-mutation-aggregate/v1" and
      .result == "passed" and
      .requiredClaimFamilies == $required and
      .coveredClaimFamilies == $required and
      (.proofs | length) == $required and
      (.proofs | map(.claimId) | unique | length) == $required and
      all(.proofs[]; .result == "killed") and
      (.missingClaimFamilies | length) == 0 and
      .candidateAcceptance == false
    ' "$run_dir/mutations/production-summary.json" >/dev/null; then
    printf 'production mutation aggregate failed validation\n' >&2
    return 1
  fi
  printf 'all production and judge-negative mutation claims passed\n'
}

release_blockers_lane() {
  local guarded_script
  {
    # The backticks are literal Markdown delimiters in the table query.
    # shellcheck disable=SC2016
    grep -E '^\| [A-Z0-9]+ \|.*`blocked-integration`' \
      docs/release/045-claim-matrix.md || true
  } |
    sed -E 's/^\| ([A-Z0-9]+) \|.*/\1/' |
    LC_ALL=C sort -u >"$scratch/blocked-integration"
  if [ -s "$scratch/blocked-integration" ]; then
    printf 'required integration blockers remain: '
    paste -sd, "$scratch/blocked-integration"
    return 1
  fi
  gate_completion_guard_self_test
  while IFS= read -r guarded_script; do
    if ! grep -F 'scripts/lib/gate-result.sh' "$guarded_script" >/dev/null ||
      ! grep -F 'gate_completed=0' "$guarded_script" >/dev/null ||
      ! grep -F 'gate_completed=1' "$guarded_script" >/dev/null ||
      ! grep -F 'gate_require_completion' "$guarded_script" >/dev/null; then
      printf \
        'release completion guard is not wired: %s\n' \
        "$guarded_script" >&2
      return 1
    fi
  done <<'EOF'
scripts/gates/release-candidate.sh
scripts/gates/release-candidate-privacy.sh
scripts/gates/release-candidate-ui.sh
scripts/gates/release-candidate-performance.sh
scripts/gates/release-candidate-lima.sh
scripts/gates/dependency-licenses.sh
scripts/gates/formal-verify.sh
scripts/gates/formal.sh
scripts/gates/migration-lima.sh
scripts/gates/migration.sh
scripts/gates/network-rotation-lima.sh
scripts/gates/package-components.sh
scripts/gates/workload-observation-lima.sh
scripts/gates/workload-privacy-lima.sh
scripts/generate-workload-observer-bpf.sh
scripts/mutation/045/run-negative-fixtures.sh
scripts/package-local.sh
scripts/release/build-candidate.sh
scripts/release/test-package-lifecycle.sh
scripts/release/collect-evidence.sh
scripts/release/revalidate-performance-evidence.sh
scripts/release/install-local-candidate.sh
scripts/release/verify-publication-absence.sh
scripts/test-install-smoke.sh
scripts/test-package-smoke.sh
scripts/test-vulnerability-gate.sh
EOF
  scripts/release/build-candidate.sh --preflight
  scripts/gates/migration.sh --preflight
  scripts/gates/migration-lima.sh --preflight
  scripts/release/test-package-lifecycle.sh --preflight
  scripts/release/collect-evidence.sh --preflight
  scripts/release/install-local-candidate.sh --preflight
  scripts/release/verify-publication-absence.sh --preflight
  printf 'no required integration blocker remains\n'
}

run_lane unit unit_lane
run_lane race race_lane
run_lane fuzz-property fuzz_property_lane
run_lane schema schema_lane
run_lane generated generated_lane
run_lane static static_lane
run_lane dependencies-advisory dependencies_advisory_lane
run_lane migration scripts/gates/migration.sh --out "$run_dir/migration"
run_lane mutations mutations_lane
run_lane release-blockers release_blockers_lane

jq -r '.requiredLanes[]' "$inventory" | LC_ALL=C sort \
  >"$scratch/expected-lanes"
jq -r '.[].id' <<<"$lanes" | LC_ALL=C sort >"$scratch/observed-lanes"
if [ -n "$(comm -3 "$scratch/expected-lanes" "$scratch/observed-lanes")" ]; then
  printf 'release-candidate-local: lane execution set drifted\n' >&2
  exit 1
fi

find "$run_dir" -type f -exec chmod 0600 {} +
while IFS= read -r evidence_file; do
  if [ ! -s "$evidence_file" ]; then
    printf 'release-candidate-local: empty artifact: %s\n' \
      "$evidence_file" >&2
    exit 1
  fi
  if [ "$(file_mode "$evidence_file")" != "600" ]; then
    printf 'release-candidate-local: artifact mode is not 0600: %s\n' \
      "$evidence_file" >&2
    exit 1
  fi
done < <(find "$run_dir" -type f | LC_ALL=C sort)

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

if [ "$failed_lanes" -eq 0 ]; then
  result="passed"
else
  result="failed"
fi

unit_passed="$(
  jq -s '
    [
      .[] |
      select(
        .Action == "pass" and
        (.Test // "") != "" and
        (.Test | contains("/") | not)
      )
    ] | length
  ' "$run_dir/lanes/unit.log" 2>/dev/null || printf '0'
)"
race_passed="$(
  jq -s '
    [
      .[] |
      select(
        .Action == "pass" and
        (.Test // "") != "" and
        (.Test | contains("/") | not)
      )
    ] | length
  ' "$run_dir/lanes/race.log" 2>/dev/null || printf '0'
)"

summary="$out/summary.json"
jq -n \
  --arg generated_at "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" \
  --arg run "$run_id" \
  --arg result "$result" \
  --arg go_version "$actual_go_version" \
  --arg inventory_path "$run_id/inventory.json" \
  --arg inventory_sha "$(sha256_file "$inventory")" \
  --arg gate_sha "$(sha256_file "$root/scripts/gates/release-candidate.sh")" \
  --argjson lanes "$lanes" \
  --argjson failed_lanes "$failed_lanes" \
  --argjson unit_passed "$unit_passed" \
  --argjson race_passed "$race_passed" \
  --argjson fuzz_count "$(jq '.fuzzTests | length' "$inventory")" \
  --argjson artifacts "$artifacts" \
  '{
    schema: "hideout.local-release-candidate/v1",
    generatedAt: $generated_at,
    source: {commit: $commit, dirty: $dirty},
    result: $result,
    scope: "full-local-source-aggregate",
    candidateAcceptance: false,
    run: $run,
    toolchain: {go: $go_version},
    inventory: {path: $inventory_path, sha256: $inventory_sha},
    gateSource: {
      path: "scripts/gates/release-candidate.sh",
      sha256: $gate_sha
    },
    lanes: $lanes,
    statistics: {
      requiredLanes: ($lanes | length),
      failedLanes: $failed_lanes,
      topLevelUnitTestsPassed: $unit_passed,
      topLevelRaceTestsPassed: $race_passed,
      fuzzHarnessesExecuted: $fuzz_count
    },
    artifacts: $artifacts,
    limitation:
      "This dirty-aware local source aggregate never substitutes for formal, real-Lima, all-sink privacy, UI, performance, package, install, exact-candidate, signing, notarization, or publication evidence."
  }' >"$summary"
chmod 0600 "$summary"

if ! jq -e \
  --arg result "$result" \
  --argjson lane_count "$(jq '.requiredLanes | length' "$inventory")" '
    .schema == "hideout.local-release-candidate/v1" and
    .result == $result and
    .candidateAcceptance == false and
    (.lanes | length) == $lane_count and
    (.lanes | map(.id) | unique | length) == $lane_count and
    all(.lanes[];
      (.result == "passed" and .exitCode == 0) or
      (.result == "failed" and .exitCode != 0)
    ) and
    (.artifacts | length) > $lane_count
  ' "$summary" >/dev/null; then
  printf 'release-candidate-local: generated summary failed validation\n' >&2
  exit 1
fi

if [ "$failed_lanes" -ne 0 ]; then
  printf \
    'release-candidate-local: result=%s failedLanes=%d evidence=%s\n' \
    "$result" "$failed_lanes" "$summary"
  exit 1
fi
gate_completed=1
printf \
  'release-candidate-local: result=%s failedLanes=%d evidence=%s\n' \
  "$result" "$failed_lanes" "$summary"
