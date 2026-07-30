#!/usr/bin/env bash
set -euo pipefail

root="$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)"
contracts="$root/scripts/mutation/045/contracts.json"
matrix="$root/docs/release/045-claim-matrix.md"
receipt=""
expected_claim=""

usage() {
  printf '%s\n' \
    "Usage: scripts/mutation/045/judge.sh --receipt FILE [--claim ID]" \
    "" \
    "Validates one Feature 045 claim receipt against the checked-in semantic" \
    "contract and its digest-bound raw observation artifact."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --receipt)
      if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
        printf '045-claim-judge: --receipt requires a file\n' >&2
        exit 2
      fi
      receipt="$2"
      shift 2
      ;;
    --claim)
      if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
        printf '045-claim-judge: --claim requires an ID\n' >&2
        exit 2
      fi
      expected_claim="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf '045-claim-judge: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if ! command -v jq >/dev/null 2>&1; then
  printf '045-claim-judge: missing required command: jq\n' >&2
  exit 1
fi

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  printf '045-claim-judge: missing shasum or sha256sum\n' >&2
  return 127
}

fail() {
  printf '045-claim-judge: %s\n' "$1" >&2
  exit 1
}

[ -f "$receipt" ] || fail "receipt-not-found"
[ ! -L "$receipt" ] || fail "receipt-is-symlink"
[ -f "$contracts" ] || fail "contracts-not-found"
[ -f "$matrix" ] || fail "claim-matrix-not-found"

claim_id="$(jq -er '.claimId | select(type == "string" and length > 0)' "$receipt" 2>/dev/null)" ||
  fail "invalid-claim-id"
fixture_id="$(jq -r '.fixtureId // empty' "$receipt")"
diagnostic_prefix="${fixture_id:-$claim_id}"

if [ -n "$expected_claim" ] && [ "$claim_id" != "$expected_claim" ]; then
  fail "$diagnostic_prefix:unexpected-claim:$claim_id"
fi

contract_sha="$(sha256_file "$contracts")"
matrix_sha="$(sha256_file "$matrix")"
receipt_contract_sha="$(jq -er '.contractSHA256 | select(type == "string")' "$receipt" 2>/dev/null)" ||
  fail "$diagnostic_prefix:missing-contract-digest"
receipt_matrix_sha="$(jq -er '.claimMatrixSHA256 | select(type == "string")' "$receipt" 2>/dev/null)" ||
  fail "$diagnostic_prefix:missing-matrix-digest"
[ "$receipt_contract_sha" = "$contract_sha" ] ||
  fail "$diagnostic_prefix:stale-contract-digest"
[ "$receipt_matrix_sha" = "$matrix_sha" ] ||
  fail "$diagnostic_prefix:stale-matrix-digest"

evidence_path="$(jq -er '.evidence.path | select(type == "string" and length > 0)' "$receipt" 2>/dev/null)" ||
  fail "$diagnostic_prefix:missing-evidence-path"
case "$evidence_path" in
  /* | *'..'* | *'//'*)
    fail "$diagnostic_prefix:unsafe-evidence-path"
    ;;
esac
if [[ ! "$evidence_path" =~ ^[A-Za-z0-9._/-]+$ ]]; then
  fail "$diagnostic_prefix:unsafe-evidence-path"
fi

receipt_dir="$(CDPATH= cd -- "$(dirname -- "$receipt")" && pwd -P)"
evidence="$receipt_dir/$evidence_path"
[ -f "$evidence" ] || fail "$diagnostic_prefix:evidence-not-found"
[ ! -L "$evidence" ] || fail "$diagnostic_prefix:evidence-is-symlink"
resolved_evidence_dir="$(CDPATH= cd -- "$(dirname -- "$evidence")" && pwd -P)"
case "$resolved_evidence_dir/" in
  "$receipt_dir/"*)
    ;;
  *)
    fail "$diagnostic_prefix:evidence-outside-receipt"
    ;;
esac

recorded_evidence_sha="$(jq -er '.evidence.sha256 | select(type == "string")' "$receipt" 2>/dev/null)" ||
  fail "$diagnostic_prefix:missing-evidence-digest"
actual_evidence_sha="$(sha256_file "$evidence")"
[ "$recorded_evidence_sha" = "$actual_evidence_sha" ] ||
  fail "$diagnostic_prefix:evidence-digest-mismatch"

set +e
judge_output="$(
  jq -n -e \
    --arg prefix "$diagnostic_prefix" \
    --slurpfile contracts "$contracts" \
    --slurpfile receipt "$receipt" \
    --slurpfile evidence "$evidence" '
      def reject($code): error($prefix + ":" + $code);
      def digest:
        type == "string" and
        test("^[0-9a-f]{64}$") and
        . != ("0" * 64);
      def commit:
        type == "string" and
        (length == 40 or length == 64) and
        test("^[0-9a-f]+$");
      def unique_strings:
        type == "array" and
        all(.[]; type == "string" and length > 0) and
        length == (unique | length);

      $contracts[0] as $catalog |
      $receipt[0] as $r |
      $evidence[0] as $e |
      ($catalog.claims | map(select(.id == $r.claimId))) as $matches |
      if $catalog.schema != "hideout.045-claim-judge-contracts/v1" then
        reject("invalid-contract-schema")
      elif ($catalog.claims | length) != ($catalog.claims | map(.id) | unique | length) then
        reject("duplicate-contract-claim")
      elif ($matches | length) != 1 then
        reject("unknown-or-duplicate-claim")
      else
        $matches[0] as $contract |
        if $r.schema != "hideout.045-claim-receipt/v1" then
          reject("invalid-receipt-schema")
        elif $r.result != "passed" then
          reject("receipt-not-passed")
        elif ($r.limitations != []) then
          reject("receipt-has-limitations")
        elif ($r.candidate.commit | commit | not) then
          reject("invalid-candidate-commit")
        elif $r.candidate.dirty != false then
          reject("dirty-candidate")
        elif ($r.candidate.packageSHA256 | digest | not) then
          reject("invalid-package-digest")
        elif ($r.judges | unique_strings | not) then
          reject("invalid-judge-list")
        elif $r.judges != $contract.judges then
          reject("judge-list-mismatch")
        elif $e.schema != "hideout.045-raw-claim-observation/v1" then
          reject("invalid-evidence-schema")
        elif $e.claimId != $r.claimId then
          reject("evidence-claim-mismatch")
        elif $e.judges != $contract.judges then
          reject("evidence-judge-mismatch")
        elif $e.result != "passed" then
          reject("evidence-not-passed")
        elif ($e.observations | type) != "array" then
          reject("observations-not-array")
        elif ($e.observations | map(.id) | length) !=
             ($e.observations | map(.id) | unique | length) then
          reject("duplicate-observation")
        elif ($e.observations | map(.id) | sort) !=
             ($contract.requirements | map(.id) | sort) then
          reject("observation-set-mismatch")
        elif any($e.observations[]; .result != "passed") then
          (($e.observations | map(select(.result != "passed")) | .[0].id) // "unknown") as $id |
          reject("observation-not-passed:" + $id)
        else
          [
            $contract.requirements[] as $requirement |
            ($e.observations | map(select(.id == $requirement.id)) | .[0]) as $observation |
            select($observation.actual != $requirement.expected) |
            $requirement.id
          ] as $mismatches |
          if ($mismatches | length) != 0 then
            reject("observation-mismatch:" + $mismatches[0])
          else
            {
              accepted: true,
              claimId: $r.claimId,
              judges: $r.judges,
              observationCount: ($e.observations | length)
            }
          end
        end
      end
    ' 2>&1
)"
judge_status=$?
set -e

if [ "$judge_status" -ne 0 ]; then
  printf '%s\n' "$judge_output" >&2
  exit 1
fi

printf '%s\n' "$judge_output"
