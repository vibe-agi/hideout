#!/usr/bin/env bash
set -euo pipefail

root="$(CDPATH='' cd -- "$(dirname -- "$0")/../../.." && pwd)"
cd "$root"
# shellcheck source=scripts/lib/gate-result.sh
. "$root/scripts/lib/gate-result.sh"
gate_completed=0

contracts="$root/scripts/mutation/045/contracts.json"
matrix="$root/docs/release/045-claim-matrix.md"
judge="$root/scripts/mutation/045/judge.sh"
out_root="$root/.artifacts/045/local/mutations/judge-negative-fixtures"

usage() {
  printf '%s\n' \
    "Usage: scripts/mutation/045/run-negative-fixtures.sh [--out DIR]" \
    "" \
    "Generates one digest-consistent semantic negative fixture for every" \
    "Feature 045 claim contract, proves the judge rejects it for the intended" \
    "reason, then proves the restored fixture is accepted."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --out)
      if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
        printf '045-negative-fixtures: --out requires a directory\n' >&2
        exit 2
      fi
      out_root="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf '045-negative-fixtures: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

for command in jq awk sort comm; do
  if ! command -v "$command" >/dev/null 2>&1; then
    printf '045-negative-fixtures: missing required command: %s\n' "$command" >&2
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
  printf '045-negative-fixtures: missing shasum or sha256sum\n' >&2
  return 127
}

run_id="run-$(date -u +'%Y%m%dT%H%M%SZ')-$$"
run_dir="$out_root/$run_id"
mkdir -p "$run_dir"
chmod 0700 "$out_root" "$run_dir"

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/hideout-045-negative-fixtures.XXXXXX")"
cleanup() {
  local exit_status=$?
  find "$tmp_dir" -depth -delete
  if [ "$exit_status" -eq 0 ]; then
    gate_require_completion "045-negative-fixtures"
  fi
}
trap cleanup EXIT

awk -F'|' '
  /^\| (A|AT|R|C|H|U|RC|CL)[0-9][0-9] / {
    gsub(/^ +| +$/, "", $2)
    print $2
  }
' "$matrix" | sort >"$tmp_dir/matrix-claims"
jq -r '.claims[].id' "$contracts" | sort >"$tmp_dir/contract-claims"
jq -r '.claims[].negative.id' "$contracts" | sort >"$tmp_dir/fixture-ids"

if [ -n "$(comm -3 "$tmp_dir/matrix-claims" "$tmp_dir/contract-claims")" ]; then
  printf '045-negative-fixtures: contract claim IDs drifted from the claim matrix\n' >&2
  comm -3 "$tmp_dir/matrix-claims" "$tmp_dir/contract-claims" >&2
  exit 1
fi

claim_count="$(wc -l <"$tmp_dir/matrix-claims" | tr -d ' ')"
[ "$claim_count" -eq 46 ] || {
  printf '045-negative-fixtures: expected 46 claim families, found %s\n' "$claim_count" >&2
  exit 1
}

if [ "$(sort -u "$tmp_dir/fixture-ids" | wc -l | tr -d ' ')" -ne "$claim_count" ]; then
  printf '045-negative-fixtures: negative fixture IDs are missing or duplicated\n' >&2
  exit 1
fi

contract_sha="$(sha256_file "$contracts")"
matrix_sha="$(sha256_file "$matrix")"
candidate_commit="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
candidate_package_sha="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
results='[]'

while IFS= read -r claim; do
  claim_id="$(jq -er '.id' <<<"$claim")"
  fixture_id="$(jq -er '.negative.id' <<<"$claim")"
  broken_requirement="$(jq -er '.negative.requirement' <<<"$claim")"
  case_dir="$run_dir/$fixture_id"
  negative_dir="$case_dir/negative"
  restored_dir="$case_dir/restored"
  mkdir -p "$negative_dir" "$restored_dir"
  chmod 0700 "$case_dir" "$negative_dir" "$restored_dir"

  jq -n \
    --argjson contract "$claim" \
    '{
      schema: "hideout.045-raw-claim-observation/v1",
      claimId: $contract.id,
      judges: $contract.judges,
      result: "passed",
      observations: [
        $contract.requirements[] |
        {id: .id, result: "passed", actual: .expected}
      ]
    }' >"$restored_dir/observation.json"

  jq \
    --arg requirement "$broken_requirement" \
    --argjson actual "$(jq -c '.negative.actual' <<<"$claim")" \
    '(.observations[] | select(.id == $requirement) | .actual) = $actual' \
    "$restored_dir/observation.json" >"$negative_dir/observation.json"

  for variant in negative restored; do
    variant_dir="$case_dir/$variant"
    evidence_sha="$(sha256_file "$variant_dir/observation.json")"
    jq -n \
      --arg fixture_id "$fixture_id" \
      --arg claim_id "$claim_id" \
      --argjson judges "$(jq -c '.judges' <<<"$claim")" \
      --arg contract_sha "$contract_sha" \
      --arg matrix_sha "$matrix_sha" \
      --arg commit "$candidate_commit" \
      --arg package_sha "$candidate_package_sha" \
      --arg evidence_sha "$evidence_sha" \
      --arg variant "$variant" \
      '{
        schema: "hideout.045-claim-receipt/v1",
        claimId: $claim_id,
        judges: $judges,
        result: "passed",
        limitations: [],
        contractSHA256: $contract_sha,
        claimMatrixSHA256: $matrix_sha,
        candidate: {
          commit: $commit,
          dirty: false,
          packageSHA256: $package_sha
        },
        evidence: {
          path: "observation.json",
          sha256: $evidence_sha
        }
      } + if $variant == "negative" then
        {fixtureId: $fixture_id}
      else
        {}
      end' >"$variant_dir/receipt.json"
    chmod 0600 "$variant_dir/observation.json" "$variant_dir/receipt.json"
  done

  negative_log="$negative_dir/judge.log"
  set +e
  "$judge" --receipt "$negative_dir/receipt.json" --claim "$claim_id" \
    >"$negative_log" 2>&1
  negative_status=$?
  set -e
  chmod 0600 "$negative_log"
  if [ "$negative_status" -eq 0 ]; then
    printf '045-negative-fixtures: %s unexpectedly passed\n' "$fixture_id" >&2
    exit 1
  fi
  expected_diagnostic="$fixture_id:observation-mismatch:$broken_requirement"
  if ! grep -Fq "$expected_diagnostic" "$negative_log"; then
    printf \
      '045-negative-fixtures: %s failed outside its semantic judge; expected %s\n' \
      "$fixture_id" "$expected_diagnostic" >&2
    exit 1
  fi

  restored_log="$restored_dir/judge.log"
  "$judge" --receipt "$restored_dir/receipt.json" --claim "$claim_id" \
    >"$restored_log" 2>&1
  chmod 0600 "$restored_log"
  grep -Fq '"accepted": true' "$restored_log" || {
    printf '045-negative-fixtures: restored fixture did not pass: %s\n' "$fixture_id" >&2
    exit 1
  }

  results="$(
    jq -c \
      --arg id "$fixture_id" \
      --arg claim "$claim_id" \
      --arg requirement "$broken_requirement" \
      --arg diagnostic "$expected_diagnostic" \
      --arg negative_receipt "$fixture_id/negative/receipt.json" \
      --arg negative_receipt_sha "$(sha256_file "$negative_dir/receipt.json")" \
      --arg negative_evidence "$fixture_id/negative/observation.json" \
      --arg negative_evidence_sha "$(sha256_file "$negative_dir/observation.json")" \
      --arg negative_log "$fixture_id/negative/judge.log" \
      --arg negative_log_sha "$(sha256_file "$negative_log")" \
      --arg restored_receipt "$fixture_id/restored/receipt.json" \
      --arg restored_receipt_sha "$(sha256_file "$restored_dir/receipt.json")" \
      --arg restored_evidence "$fixture_id/restored/observation.json" \
      --arg restored_evidence_sha "$(sha256_file "$restored_dir/observation.json")" \
      --arg restored_log "$fixture_id/restored/judge.log" \
      --arg restored_log_sha "$(sha256_file "$restored_log")" \
      '. + [{
        id: $id,
        claimId: $claim,
        brokenRequirement: $requirement,
        result: "killed",
        expectedDiagnostic: $diagnostic,
        negative: {
          receipt: $negative_receipt,
          receiptSHA256: $negative_receipt_sha,
          evidence: $negative_evidence,
          evidenceSHA256: $negative_evidence_sha,
          log: $negative_log,
          logSHA256: $negative_log_sha
        },
        restored: {
          result: "passed",
          receipt: $restored_receipt,
          receiptSHA256: $restored_receipt_sha,
          evidence: $restored_evidence,
          evidenceSHA256: $restored_evidence_sha,
          log: $restored_log,
          logSHA256: $restored_log_sha
        }
      }]' <<<"$results"
  )"
done < <(jq -c '.claims[]' "$contracts")

source_commit="$(git rev-parse HEAD)"
if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then
  source_dirty=true
else
  source_dirty=false
fi

jq -n \
  --arg generated_at "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg run_id "$run_id" \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" \
  --arg contract_sha "$contract_sha" \
  --arg matrix_sha "$matrix_sha" \
  --argjson claim_count "$claim_count" \
  --argjson fixtures "$results" \
  '{
    schema: "hideout.045-negative-fixture-evidence/v1",
    generatedAt: $generated_at,
    runId: $run_id,
    source: {
      commit: $commit,
      dirty: $dirty
    },
    inputs: {
      contracts: "scripts/mutation/045/contracts.json",
      contractsSHA256: $contract_sha,
      claimMatrix: "docs/release/045-claim-matrix.md",
      claimMatrixSHA256: $matrix_sha
    },
    result: "passed",
    claimFamilies: $claim_count,
    negativeFixtures: $fixtures,
    restoredFixtures: $claim_count,
    implementationMutationProofs: {
      result: "not-evaluated",
      accepted: false
    },
    claimAcceptance: false,
    claimBoundary:
      "This proves every Feature 045 claim-receipt judge rejects one digest-consistent semantic false green and accepts the restored observation. It does not prove implementation mutants, real Lima, exact-package identity, or release readiness."
  }' >"$run_dir/summary.json"
chmod 0600 "$run_dir/summary.json"

if ! jq -e \
  --argjson count "$claim_count" '
    .schema == "hideout.045-negative-fixture-evidence/v1" and
    .result == "passed" and
    .claimFamilies == $count and
    (.negativeFixtures | length) == $count and
    ([.negativeFixtures[].id] | length) ==
      ([.negativeFixtures[].id] | unique | length) and
    all(.negativeFixtures[];
      .result == "killed" and
      .restored.result == "passed"
    ) and
    .implementationMutationProofs.accepted == false and
    .claimAcceptance == false
  ' "$run_dir/summary.json" >/dev/null; then
  printf '045-negative-fixtures: generated summary failed validation\n' >&2
  exit 1
fi

jq -n \
  --arg run_id "$run_id" \
  --arg summary "$run_id/summary.json" \
  --arg sha256 "$(sha256_file "$run_dir/summary.json")" \
  '{
    schema: "hideout.045-negative-fixture-latest/v1",
    runId: $run_id,
    summary: $summary,
    sha256: $sha256
  }' >"$out_root/summary.json"
chmod 0600 "$out_root/summary.json"

gate_completed=1
printf \
  '045-negative-fixtures: passed claims=%s fixtures=%s evidence=%s\n' \
  "$claim_count" "$claim_count" "$run_dir/summary.json"
