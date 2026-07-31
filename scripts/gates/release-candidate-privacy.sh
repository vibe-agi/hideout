#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"
# shellcheck source=scripts/lib/gate-result.sh
. "$repo_root/scripts/lib/gate-result.sh"
gate_completed=0

umask 077
out="$repo_root/.artifacts/045/privacy"
preflight_only=0

usage() {
  printf '%s\n' \
    "Usage: scripts/gates/release-candidate-privacy.sh [--preflight] [--out DIR]" \
    "" \
    "Runs exact secret/privacy tests, real macOS Keychain and Lima canaries," \
    "and fresh CLI/TUI/WebUI journeys. Every retained sink is scanned and" \
    "digest-bound. This command is local-only and never publishes."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --preflight)
      preflight_only=1
      shift
      ;;
    --out)
      [ "$#" -ge 2 ] && [ -n "${2:-}" ] || {
        printf 'release-candidate-privacy: --out requires a directory\n' >&2
        exit 2
      }
      out="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf \
        'release-candidate-privacy: unknown argument: %s\n' \
        "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    printf \
      'release-candidate-privacy: missing required command: %s\n' \
      "$1" >&2
    exit 1
  }
}

for required_command in \
  awk bash find git go jq perl rg security shasum stat; do
  require_command "$required_command"
done

expected_contract_claims='[
  "A04", "A09", "A10", "R01",
  "R02", "R03", "R05", "CL03"
]'
if ! jq -e \
  --argjson expected "$expected_contract_claims" '
    [.claims[] |
      select((.judges // []) | index("release-candidate-privacy")) |
      .id] | sort == ($expected | sort)
  ' scripts/mutation/045/contracts.json >/dev/null; then
  printf \
    'release-candidate-privacy: claim contract set drifted\n' \
    >&2
  exit 1
fi

scratch_parent="$(CDPATH='' cd -- "${TMPDIR:-/tmp}" && pwd -P)"
if [ "$preflight_only" -eq 1 ]; then
  preflight_out="$(mktemp -d "$scratch_parent/hideout-privacy-preflight.XXXXXX")"
  # Invoked indirectly by the EXIT trap.
  # shellcheck disable=SC2329
  cleanup_preflight() {
    local exit_status=$?
    case "${preflight_out:-}" in
      "$scratch_parent"/hideout-privacy-preflight.*)
        [ ! -d "$preflight_out" ] ||
          find "$preflight_out" -depth -delete
        ;;
      *)
        printf \
          'release-candidate-privacy: refusing unexpected preflight cleanup\n' \
          >&2
        ;;
    esac
    if [ "$exit_status" -eq 0 ]; then
      gate_require_completion "release-candidate-privacy-preflight"
    fi
  }
  trap cleanup_preflight EXIT
  bash -n \
    scripts/gates/release-candidate-privacy.sh \
    scripts/gates/release-candidate-ui.sh \
    scripts/gates/workload-privacy-lima.sh \
    scripts/gates/keychain-real.sh
  go test -run '^$' \
    ./internal/app \
    ./internal/daemon \
    ./internal/daemon/uiweb_assets \
    ./internal/manager \
    ./internal/secrets \
    ./internal/supportreport \
    ./internal/workloadobs/collector \
    ./internal/workloadobs/collector/dns \
    ./internal/workloadobs/redact \
    ./internal/workloadobs/store >/dev/null
  scripts/gates/keychain-real.sh --preflight >/dev/null
  scripts/gates/release-candidate-ui.sh \
    --preflight --out "$preflight_out/ui" >/dev/null
  scripts/gates/workload-privacy-lima.sh \
    --preflight --out "$preflight_out/lima" >/dev/null
  gate_completed=1
  printf 'release-candidate-privacy: preflight=passed\n'
  exit 0
fi

[ "$(uname -s)" = "Darwin" ] || {
  printf \
    'release-candidate-privacy: full gate requires macOS\n' >&2
  exit 1
}
[ "$(uname -m)" = "arm64" ] || {
  printf \
    'release-candidate-privacy: full gate requires arm64\n' >&2
  exit 1
}

if [ -L "$out" ]; then
  printf \
    'release-candidate-privacy: evidence directory must not be a symlink\n' \
    >&2
  exit 1
fi
mkdir -p "$out"
out="$(cd "$out" && pwd -P)"
chmod 0700 "$out"

source_commit="$(git rev-parse HEAD)"
if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then
  source_dirty=true
else
  source_dirty=false
fi
run_id="run-$(date -u +'%Y%m%dT%H%M%SZ')-$$"
run_dir="$out/$run_id"
[ ! -e "$run_dir" ] || {
  printf \
    'release-candidate-privacy: run directory already exists\n' >&2
  exit 1
}
mkdir -p "$run_dir/tests" "$run_dir/lanes"
chmod 0700 "$run_dir" "$run_dir/tests" "$run_dir/lanes"

scratch="$(mktemp -d "$scratch_parent/hideout-release-privacy.XXXXXX")"
cleanup() {
  local exit_status=$?
  case "${scratch:-}" in
    "$scratch_parent"/hideout-release-privacy.*)
      [ ! -d "$scratch" ] || find "$scratch" -depth -delete
      ;;
    *)
      printf \
        'release-candidate-privacy: refusing unexpected scratch cleanup\n' \
        >&2
      ;;
  esac
  if [ "$exit_status" -eq 0 ]; then
    gate_require_completion "release-candidate-privacy"
  fi
}
trap cleanup EXIT

sha256_file() {
  shasum -a 256 "$1" | awk '{print $1}'
}

file_mode() {
  stat -f '%Lp' "$1" 2>/dev/null ||
    stat -c '%a' "$1" 2>/dev/null
}

safe_relative_path() {
  case "$1" in
    "" | /* | .. | ../* | */.. | */../*) return 1 ;;
    *) return 0 ;;
  esac
}

secret_expected='[
  "TestSecretAPIRoutesAreStrictPrivateAndBounded",
  "TestSecretAPIPlanApplyReplayDeleteAndListNeverExposeValue",
  "TestSecretAPIRejectsAmbiguousJSONAndClearsParsedValueOnError",
  "TestSecretApplyDecoderClearsValueWhenLaterFieldIsInvalid",
  "TestSecretAPINeverEchoesProviderErrors",
  "TestSecretServicePlansWithoutValueAndAppliesExactlyOnce",
  "TestSecretRecoveryRequiredResumesAfterProviderBecomesAvailable",
  "TestSecretAPIReferenceFeedsConfigurationTransactionWithoutValueEcho",
  "TestCredentialRotationFeedsGenerationBoundRedactionSnapshots",
  "TestKeychainStoreSetRotateDeleteAndMissingLifecycle",
  "TestKeychainStoreRecoversCommittedSetAndDeleteAfterResponseLoss",
  "TestKeychainReconcileDistinguishesCommitNegativeProofAndUnknown",
  "TestKeychainStoreReportsLockedMissingAndCorruptWithoutLeaking",
  "TestKeychainEnvelopeRejectsSecretBearingTombstoneAndTrailingData",
  "TestSecretSetPlansConfirmsThenReadsStdinAndAppliesWithoutEcho",
  "TestSecretMutationRejectsArgvValuesWithoutEchoOrPlanning",
  "TestSecretStdinRequiresExplicitConfirmationBeforeReading",
  "TestSecretTTYCancelDoesNotReadOrApply",
  "TestSecretTTYInputIsHiddenAndSourceBytesAreCleared",
  "TestSecretConfirmationDoesNotReadAheadIntoHiddenValue",
  "TestSecretListAndStatusRenderMetadataOnly",
  "TestDaemonSecretApplyRetriesExactPayloadAfterResponseLoss",
  "TestSecretApplyEncoderRoundTripsEscapesAndClearsInput",
  "TestDaemonSecretClientDoesNotTrustErrorText",
  "TestTUIConfigPTYSecretInputNeverEchoesAndCancelClears"
]'

sink_expected='[
  "TestCanariesAreAbsentFromEveryPostRedactionSink",
  "TestCanaryMatrixCoversManagedEncodingsAndCredentialSyntax",
  "TestRedactorRemovesCredentialCanariesBeforePersistence",
  "TestRedactorRemovesExecutionCanariesBeforePersistence",
  "TestRedactorHandlesSplitEqualsQueryAndAuthorizationForms",
  "TestRedactorFailsPrivateForTruncatedURIUserinfo",
  "TestRedactorCoversFileTargetPathBeforePersistence",
  "TestRedactorTruncatesBeforeScanningAndFailsClosed",
  "TestFileNormalizerRejectsCrossBoundaryAndNeverRetainsContents",
  "TestPacketFromKernelRecordClearsPayloadOnEveryFailure",
  "TestProxyChunkFromKernelRecordClearsPayloadOnEveryFailure",
  "TestSOCKS5ParserClearsUsernamePasswordAndSupportsFragmentedAuth",
  "TestSOCKS5ParserEmitsValidatedDomainAndStopsInspectingTunnelPayload",
  "TestParserRejectsCrossBoundaryAndEncryptedMetadataWithoutPayload",
  "TestActivityServiceDegradesCoverageWhenRedactionSnapshotIsUnavailable",
  "TestActivityServiceIngestsStrictGuestRecordWithHostCoverageAndRedaction",
  "TestActivityServicePersistsRedactedExecutionUpdates",
  "TestActivityServiceIngestBatchRedactsAndDurablyGroupsRecords",
  "TestUnauthenticatedActivityAPILeaksNoStoreEvidence",
  "TestActivityRoutesRejectAmbiguousInvalidAndUnknownQueriesBeforeProvider",
  "TestActivityRouteInventoryIsPrivateReadOnlyAndComplete",
  "TestStartLocalServerRejectsNonLoopbackBind",
  "TestLoopbackUIServesEventConsumingWebUI",
  "TestHandlerServesTypedAssetsWithStrictBrowserBoundary",
  "TestWebUILiveConsoleHealthAndRedactionContracts",
  "TestOverviewSummarizesDomainsWithoutSecretValues",
  "TestAPIExposesDomainResourcesWithoutSecretValues",
  "TestConcurrentSessionEvidenceRedactsControlMaterialAndStaysSessionLocal"
]'

boundary_expected='[
  "TestActivityExportPreservesLocalViewButReviewsAndRedactsArtifact",
  "TestActivityExportSelectedUserDataUsesExistingPolicyReview",
  "TestActivityExportPreservePathPolicyRequiresExplicitLocalAcknowledgement",
  "TestActivityExportApplyRejectsUnconfirmedTamperedAndStalePlans",
  "TestActivityExportApplyTreatsLifecycleCleanupAsStale",
  "TestActivityExportShareRequiresExistingDecisionAndDoesNotPublishOnApply",
  "TestActivityExportAPIRoutesRequirePlanThenExplicitApply",
  "TestSupportReportRequiresExplicitSafeOutputAndDoesNotCreateStore",
  "TestSupportReportWritesLocalBoundedSourceReport",
  "TestSupportReportRefusesExistingOutputWithoutOverwrite",
  "TestValidateRequiresActivityEvidenceAndIdentityExclusions",
  "TestSupportReportSchemaAcceptsCanonicalAndRejectsUnknownFields",
  "TestStoreCreatesOnlyHostPrivateDirectoriesAndFiles",
  "TestStoreRejectsSymlinkAndHardlinkReplacement",
  "TestStoreRejectsTraversalWhileHashingUntrustedOwnerLabels",
  "TestStoreRejectsUnredactedWrongOwnerAndCancelledWrites",
  "TestActiveSegmentRepairsTornTailAndReportsCoverageGap",
  "TestActiveSegmentCRCFailureTruncatesAfterLastValidFrame",
  "TestCorruptSealedSegmentIsQuarantinedAndNeverReturned",
  "TestQuotaPrunesOldestSealedAcrossOwnersAndBoundsOvershoot"
]'

suites_json='[]'
failed_suites=0

run_go_suite() {
  local suite_id="$1"
  local expected_json="$2"
  shift 2
  local expected_path="$run_dir/tests/$suite_id.expected.json"
  local observed_path="$run_dir/tests/$suite_id.observed.json"
  local result_path="$run_dir/tests/$suite_id.result.json"
  local log_path="$run_dir/tests/$suite_id.go-test.jsonl"
  local regex started_at finished_at exit_code suite_state
  local expected_count observed_count semantic_valid

  jq -S . <<<"$expected_json" >"$expected_path"
  regex="$(jq -r 'map("^" + . + "$") | join("|")' "$expected_path")"
  started_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
  printf 'release-candidate-privacy: running %s\n' "$suite_id"
  set +e
  go test -json -run "$regex" -count=1 "$@" >"$log_path" 2>&1
  exit_code=$?
  set -e
  finished_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
  [ -s "$log_path" ] ||
    printf '{"Action":"output","Output":"empty go test log"}\n' >"$log_path"

  semantic_valid=false
  if jq -s \
    --slurpfile expected "$expected_path" '
      [.[] |
        select(.Action == "pass" and
          ((.Test // "") as $test |
            ($expected[0] | index($test)) != null)) |
        .Test] |
      unique |
      sort
    ' "$log_path" >"$observed_path" 2>/dev/null &&
    jq -e -n \
      --slurpfile expected "$expected_path" \
      --slurpfile observed "$observed_path" '
        ($expected[0] | sort) == ($observed[0] | sort)
      ' >/dev/null; then
    semantic_valid=true
  fi
  [ -f "$observed_path" ] || printf '[]\n' >"$observed_path"

  expected_count="$(jq 'length' "$expected_path")"
  observed_count="$(jq 'length' "$observed_path" 2>/dev/null || printf '0')"
  suite_state="failed"
  if [ "$exit_code" -eq 0 ] && [ "$semantic_valid" = true ]; then
    suite_state="passed"
    printf \
      'release-candidate-privacy: %s passed (%s tests)\n' \
      "$suite_id" "$expected_count"
  else
    failed_suites=$((failed_suites + 1))
    printf \
      'release-candidate-privacy: %s failed (exit=%d expected=%s observed=%s)\n' \
      "$suite_id" "$exit_code" "$expected_count" "$observed_count" >&2
    tail -30 "$log_path" >&2
  fi

  chmod 0600 "$expected_path" "$observed_path" "$log_path"
  jq -n \
    --arg id "$suite_id" \
    --arg result "$suite_state" \
    --arg startedAt "$started_at" \
    --arg finishedAt "$finished_at" \
    --arg log "tests/$suite_id.go-test.jsonl" \
    --arg logSHA256 "$(sha256_file "$log_path")" \
    --arg expected "tests/$suite_id.expected.json" \
    --arg expectedSHA256 "$(sha256_file "$expected_path")" \
    --arg observed "tests/$suite_id.observed.json" \
    --arg observedSHA256 "$(sha256_file "$observed_path")" \
    --argjson exitCode "$exit_code" \
    --argjson expectedCount "$expected_count" \
    --argjson observedCount "$observed_count" \
    --argjson exactPassSet "$semantic_valid" '
      {
        schema:"hideout.privacy-go-suite/v1",
        id:$id,
        result:$result,
        exitCode:$exitCode,
        startedAt:$startedAt,
        finishedAt:$finishedAt,
        expectedCount:$expectedCount,
        observedCount:$observedCount,
        exactPassSet:$exactPassSet,
        artifacts:{
          log:{path:$log,sha256:$logSHA256},
          expected:{path:$expected,sha256:$expectedSHA256},
          observed:{path:$observed,sha256:$observedSHA256}
        }
      }
    ' >"$result_path"
  chmod 0600 "$result_path"
  suites_json="$(
    jq -c \
      --argjson suite "$(jq -c . "$result_path")" \
      '. + [$suite]' <<<"$suites_json"
  )"
}

run_go_suite secret-authority "$secret_expected" \
  ./internal/app \
  ./internal/daemon \
  ./internal/manager \
  ./internal/secrets
run_go_suite redaction-access-sinks "$sink_expected" \
  ./internal/daemon \
  ./internal/daemon/uiweb_assets \
  ./internal/manager \
  ./internal/workloadobs/collector \
  ./internal/workloadobs/collector/dns \
  ./internal/workloadobs/redact \
  ./internal/workloadobs/store
run_go_suite export-store-recovery "$boundary_expected" \
  ./internal/app \
  ./internal/manager \
  ./internal/supportreport \
  ./internal/workloadobs/store

keychain_log="$run_dir/lanes/keychain-real.log"
keychain_started="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
printf 'release-candidate-privacy: running real Keychain lane\n'
set +e
scripts/gates/keychain-real.sh --require-real \
  >"$keychain_log" 2>&1
keychain_exit=$?
set -e
keychain_finished="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
[ -s "$keychain_log" ] ||
  printf 'real Keychain lane produced no output (exit=%d)\n' \
    "$keychain_exit" >"$keychain_log"
chmod 0600 "$keychain_log"
keychain_valid=false
if [ "$keychain_exit" -eq 0 ] &&
  rg -F \
    'keychain-real: status=passed provider=Security.framework generic-password' \
    "$keychain_log" >/dev/null; then
  keychain_valid=true
else
  tail -30 "$keychain_log" >&2
fi

ui_log="$run_dir/lanes/release-candidate-ui.log"
ui_pointer="$run_dir/ui/result.json"
ui_started="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
printf 'release-candidate-privacy: running fresh UI surface lane\n'
set +e
scripts/gates/release-candidate-ui.sh \
  --out "$run_dir/ui" >"$ui_log" 2>&1
ui_exit=$?
set -e
ui_finished="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
[ -s "$ui_log" ] ||
  printf 'UI lane produced no output (exit=%d)\n' \
    "$ui_exit" >"$ui_log"
chmod 0600 "$ui_log"

ui_valid=false
ui_summary=""
if [ "$ui_exit" -eq 0 ] && [ -f "$ui_pointer" ] &&
  jq -e \
    --arg commit "$source_commit" \
    --argjson dirty "$source_dirty" '
      .schema == "hideout.release-candidate-ui-pointer/v1" and
      .result == "passed" and
      .source == {commit:$commit,dirty:$dirty} and
      .candidateAcceptance == ($dirty | not)
    ' "$ui_pointer" >/dev/null; then
  ui_summary_rel="$(jq -er '.summary' "$ui_pointer")"
  if safe_relative_path "$ui_summary_rel"; then
    ui_summary="$run_dir/ui/$ui_summary_rel"
  fi
  if [ -f "$ui_summary" ] &&
    [ ! -L "$ui_summary" ] &&
    [ "$(sha256_file "$ui_summary")" = \
      "$(jq -er '.summarySHA256' "$ui_pointer")" ] &&
    jq -e '
      .result == "passed" and
      all(.validation[]; . == true) and
      all(.claimReceipts[]; .passed == true)
    ' "$ui_summary" >/dev/null; then
    ui_valid=true
  fi
fi
if [ "$ui_valid" != true ]; then
  tail -30 "$ui_log" >&2
fi

lima_log="$run_dir/lanes/workload-privacy-lima.log"
lima_result="$run_dir/lima/result.json"
lima_started="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
printf 'release-candidate-privacy: running real Lima all-sink lane\n'
set +e
scripts/gates/workload-privacy-lima.sh \
  --require-real \
  --out "$run_dir/lima" >"$lima_log" 2>&1
lima_exit=$?
set -e
lima_finished="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
[ -s "$lima_log" ] ||
  printf 'real Lima lane produced no output (exit=%d)\n' \
    "$lima_exit" >"$lima_log"
chmod 0600 "$lima_log"

lima_valid=false
if [ "$lima_exit" -eq 0 ] && [ -f "$lima_result" ] &&
  jq -e \
    --arg commit "$source_commit" \
    --argjson dirty "$source_dirty" '
      .schema == "hideout.workload-privacy-lima-evidence/v1" and
      .result == "passed" and
      .source == {commit:$commit,dirty:$dirty} and
      .candidateAcceptance == ($dirty | not) and
      all(.checks[]; . == "passed") and
      (.artifacts | length) == 10
    ' "$lima_result" >/dev/null &&
  jq -e '
      .redaction.passed == true and
      .redaction.prePersistence == true and
      .redaction.processListingScanned == true and
      .redaction.keychainMetadataScanned == true and
      (.redaction.sinkCanaryHits | keys | sort) == [
        "api","evidence","export","index","log",
        "process","store","support"
      ] and
      all(.redaction.sinkCanaryHits[]; . == 0) and
      all(.redaction.canaryClassHits[]; . == 0) and
      .redaction.redactionFailurePersistedRecords == 0 and
      .localPath.authenticatedLocalView == true and
      .localPath.shareableSupportExcluded == true and
      .permissions.directoryMode == "0700" and
      .permissions.fileMode == "0600" and
      .permissions.targetReadSucceeded == false
    ' "$run_dir/lima/reports/privacy-summary.json" >/dev/null; then
  lima_valid=true
  while IFS=$'\t' read -r relative expected_sha; do
    safe_relative_path "$relative" || {
      lima_valid=false
      break
    }
    artifact_path="$run_dir/lima/$relative"
    if [ ! -f "$artifact_path" ] ||
      [ -L "$artifact_path" ] ||
      [ "$(sha256_file "$artifact_path")" != "$expected_sha" ] ||
      [ "$(file_mode "$artifact_path")" != "600" ]; then
      lima_valid=false
      break
    fi
  done < <(jq -r '.artifacts[] | [.path,.sha256] | @tsv' "$lima_result")
fi
if [ "$lima_valid" != true ]; then
  tail -30 "$lima_log" >&2
fi

secret_valid=false
sink_valid=false
boundary_valid=false
if jq -e '
    .id == "secret-authority" and
    .result == "passed" and
    .exactPassSet == true and
    .expectedCount == .observedCount
  ' "$run_dir/tests/secret-authority.result.json" >/dev/null; then
  secret_valid=true
fi
if jq -e '
    .id == "redaction-access-sinks" and
    .result == "passed" and
    .exactPassSet == true and
    .expectedCount == .observedCount
  ' "$run_dir/tests/redaction-access-sinks.result.json" >/dev/null; then
  sink_valid=true
fi
if jq -e '
    .id == "export-store-recovery" and
    .result == "passed" and
    .exactPassSet == true and
    .expectedCount == .observedCount
  ' "$run_dir/tests/export-store-recovery.result.json" >/dev/null; then
  boundary_valid=true
fi

private_evidence_valid=true
if find "$run_dir" -type f ! -name '*.png' -print0 |
  xargs -0 rg -a -n \
    'ui_[0-9a-fA-F]{48}|cap_[0-9a-fA-F]{32,}|HIDEOUT_SECRET_[A-Za-z0-9_]+=[^[:space:]]+|((https?|socks5h?)://[^/@[:space:]:]+:[^/@[:space:]]+@)|Authorization:[[:space:]]*Bearer[[:space:]]+[A-Za-z0-9._~-]{8,}' \
    >/dev/null 2>&1; then
  private_evidence_valid=false
fi
# The single-quoted Perl program must be passed verbatim.
# shellcheck disable=SC2016
if find "$run_dir" -type f ! -name '*.png' -print0 |
  xargs -0 perl -0777 -ne '
    $found = 1 if /[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/;
    END { exit($found ? 0 : 1) }
  ' >/dev/null 2>&1; then
  private_evidence_valid=false
fi

aggregate_result="passed"
if [ "$failed_suites" -ne 0 ] ||
  [ "$secret_valid" != true ] ||
  [ "$sink_valid" != true ] ||
  [ "$boundary_valid" != true ] ||
  [ "$keychain_valid" != true ] ||
  [ "$ui_valid" != true ] ||
  [ "$lima_valid" != true ] ||
  [ "$private_evidence_valid" != true ]; then
  aggregate_result="failed"
fi

find "$run_dir" -type d -exec chmod 0700 {} +
find "$run_dir" -type f -exec chmod 0600 {} +
artifact_rows="$scratch/artifacts.jsonl"
: >"$artifact_rows"
find "$run_dir" -type f -print | LC_ALL=C sort |
  while IFS= read -r artifact_path; do
    relative="${artifact_path#"$run_dir"/}"
    jq -cn \
      --arg path "$relative" \
      --arg sha256 "$(sha256_file "$artifact_path")" \
      --argjson bytes "$(wc -c <"$artifact_path" | tr -d '[:space:]')" \
      '{path:$path,sha256:$sha256,bytes:$bytes,mode:"0600"}'
  done >"$artifact_rows"

ui_summary_sha=""
if [ -n "$ui_summary" ] && [ -f "$ui_summary" ]; then
  ui_summary_sha="$(sha256_file "$ui_summary")"
fi
lima_result_sha=""
if [ -f "$lima_result" ]; then
  lima_result_sha="$(sha256_file "$lima_result")"
fi

summary_path="$run_dir/summary.json"
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg result "$aggregate_result" \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" \
  --arg runId "$run_id" \
  --arg hostOS "$(uname -s)" \
  --arg hostArch "$(uname -m)" \
  --arg goVersion "$(go version)" \
  --arg keychainStarted "$keychain_started" \
  --arg keychainFinished "$keychain_finished" \
  --arg keychainLogSHA256 "$(sha256_file "$keychain_log")" \
  --arg uiStarted "$ui_started" \
  --arg uiFinished "$ui_finished" \
  --arg uiSummary "${ui_summary#"$run_dir"/}" \
  --arg uiSummarySHA256 "$ui_summary_sha" \
  --arg limaStarted "$lima_started" \
  --arg limaFinished "$lima_finished" \
  --arg limaResult "lima/result.json" \
  --arg limaResultSHA256 "$lima_result_sha" \
  --argjson keychainExit "$keychain_exit" \
  --argjson uiExit "$ui_exit" \
  --argjson limaExit "$lima_exit" \
  --argjson secretValid "$secret_valid" \
  --argjson sinkValid "$sink_valid" \
  --argjson boundaryValid "$boundary_valid" \
  --argjson keychainValid "$keychain_valid" \
  --argjson uiValid "$ui_valid" \
  --argjson limaValid "$lima_valid" \
  --argjson privateEvidenceValid "$private_evidence_valid" \
  --argjson suites "$suites_json" \
  --slurpfile artifacts "$artifact_rows" '
    def zeroSinks: {
      api:0,evidence:0,export:0,index:0,log:0,
      process:0,store:0,support:0,ui:0
    };
    def zeroClasses: {
      authField:0,controlToken:0,encoded:0,managedValue:0,
      sensitiveArg:0,sensitiveQuery:0,splitForm:0,uriUserinfo:0
    };
    {
      schema:"hideout.release-candidate-privacy-evidence/v1",
      generatedAt:$generatedAt,
      result:$result,
      candidateAcceptance:
        ($result == "passed" and ($dirty | not)),
      source:{commit:$commit,dirty:$dirty},
      runId:$runId,
      host:{os:$hostOS,arch:$hostArch,goVersion:$goVersion},
      suites:$suites,
      lanes:[
        {
          id:"keychain-real",
          result:(if $keychainValid then "passed" else "failed" end),
          exitCode:$keychainExit,
          startedAt:$keychainStarted,
          finishedAt:$keychainFinished,
          evidence:{
            path:"lanes/keychain-real.log",
            sha256:$keychainLogSHA256
          }
        },
        {
          id:"ui-surfaces",
          result:(if $uiValid then "passed" else "failed" end),
          exitCode:$uiExit,
          startedAt:$uiStarted,
          finishedAt:$uiFinished,
          evidence:{
            path:$uiSummary,
            sha256:$uiSummarySHA256
          }
        },
        {
          id:"workload-privacy-lima",
          result:(if $limaValid then "passed" else "failed" end),
          exitCode:$limaExit,
          startedAt:$limaStarted,
          finishedAt:$limaFinished,
          evidence:{
            path:$limaResult,
            sha256:$limaResultSHA256
          }
        }
      ],
      validation:{
        exactSecretAuthorityTests:$secretValid,
        exactRedactionAndAccessTests:$sinkValid,
        exactExportStoreRecoveryTests:$boundaryValid,
        realKeychain:$keychainValid,
        realLimaAllSink:$limaValid,
        freshCLITUIWebSurfaces:$uiValid,
        privateEvidence:$privateEvidenceValid
      },
      claims:{
        typedSecretAuthority:($secretValid and $keychainValid),
        authenticatedLocalAccess:($sinkValid and $uiValid),
        reviewedExportAuthority:$boundaryValid,
        zeroPostRedactionCanaries:
          ($sinkValid and $limaValid and $uiValid and $privateEvidenceValid),
        completeCanaryClasses:($sinkValid and $limaValid),
        localPathAndExportPolicy:($boundaryValid and $limaValid),
        hostPrivateStore:($boundaryValid and $limaValid),
        corruptionAndRetentionEvidence:$boundaryValid
      },
      claimReceipts:{
        A04:{
          passed:($secretValid and $keychainValid and $limaValid),
          requirements:{
            authenticatedTypedOperation:true,
            profileStoresReferenceOnly:true,
            publicSecretCanaryHits:0,
            generationInvalidated:true
          }
        },
        A09:{
          passed:($sinkValid and $uiValid and $limaValid),
          requirements:{
            authenticatedLocalSurfaces:["cli","tui","web"],
            unauthenticatedResponsesWithOwnerData:0,
            nonLoopbackExposure:false
          }
        },
        A10:{
          passed:$boundaryValid,
          requirements:{
            reviewedPlanApplied:true,
            pathPolicyAcknowledged:true,
            planDigestBound:true,
            publicationEffects:0
          }
        },
        R01:{
          passed:
            ($sinkValid and $limaValid and $uiValid and
             $privateEvidenceValid),
          requirements:{
            sinkCanaryHits:zeroSinks,
            fileContentFields:0,
            packetPayloadFields:0
          }
        },
        R02:{
          passed:($sinkValid and $limaValid and $privateEvidenceValid),
          requirements:{
            canaryClassHits:zeroClasses,
            redactionBeforePersistence:true,
            redactionFailurePersistedRecords:0
          }
        },
        R03:{
          passed:($boundaryValid and $limaValid),
          requirements:{
            localPathPreserved:true,
            redactedExportContainsHostPath:false,
            preservePolicyRequiresAcknowledgement:true
          }
        },
        R05:{
          passed:($boundaryValid and $limaValid and $sinkValid),
          requirements:{
            directoryMode:"0700",
            fileMode:"0600",
            linkReplacementAccepted:false,
            targetReadSucceeded:false,
            unauthenticatedReadSucceeded:false
          }
        },
        CL03:{
          passed:$boundaryValid,
          requirements:{
            corruptFramesReturned:0,
            pruneOrder:"oldest-sealed",
            coverageGapVisible:true,
            quarantinePresent:true
          }
        }
      },
      artifacts:$artifacts,
      limitations:
        ([
          "The exact installed-package privacy rerun remains T164/T171; this gate binds the recorded source tree.",
          "Local authenticated paths intentionally remain visible; reviewed export applies its separate path policy."
        ] +
        if $dirty then
          ["This binds a dirty development checkout and cannot accept an exact release candidate."]
        else
          []
        end)
    }
  ' >"$summary_path"
chmod 0600 "$summary_path"

if ! jq -e \
  --argjson contractClaims "$expected_contract_claims" \
  --slurpfile contracts scripts/mutation/045/contracts.json '
    def contractRequirements:
      ($contracts[0].claims |
        map(select((.judges // []) |
          index("release-candidate-privacy"))) |
        map({
          key:.id,
          value:(.requirements |
            map({key:.id,value:.expected}) |
            from_entries)
        }) |
        from_entries);
    (.result == "passed") ==
      (all(.validation[]; . == true) and
       all(.suites[]; .result == "passed") and
       all(.lanes[]; .result == "passed")) and
    (.result != "passed" or all(.claims[]; . == true)) and
    ([.claimReceipts | keys[]] | sort) ==
      ($contractClaims | sort) and
    (.result != "passed" or
      all(.claimReceipts[]; .passed == true)) and
    (.claimReceipts |
      with_entries(.value = .value.requirements)) ==
      contractRequirements and
    all(.artifacts[];
      .mode == "0600" and
      (.sha256 | test("^[a-f0-9]{64}$")) and
      .bytes >= 0)
  ' "$summary_path" >/dev/null; then
  printf \
    'release-candidate-privacy: aggregate summary is internally inconsistent\n' \
    >&2
  exit 1
fi

summary_sha="$(sha256_file "$summary_path")"
result_tmp="$(mktemp "$out/.result.XXXXXX")"
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg result "$aggregate_result" \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" \
  --arg runId "$run_id" \
  --arg summary "$run_id/summary.json" \
  --arg summarySHA256 "$summary_sha" '
    {
      schema:"hideout.release-candidate-privacy-pointer/v1",
      generatedAt:$generatedAt,
      result:$result,
      candidateAcceptance:
        ($result == "passed" and ($dirty | not)),
      source:{commit:$commit,dirty:$dirty},
      runId:$runId,
      summary:$summary,
      summarySHA256:$summarySHA256
    }
  ' >"$result_tmp"
chmod 0600 "$result_tmp"
mv "$result_tmp" "$out/result.json"
find "$run_dir" -type d -exec chmod 0700 {} +
find "$run_dir" -type f -exec chmod 0600 {} +

printf \
  'release-candidate-privacy: evidence=%s\n' \
  "$summary_path"
if [ "$aggregate_result" != "passed" ]; then
  printf 'release-candidate-privacy: failed\n' >&2
  exit 1
fi
gate_completed=1
printf 'release-candidate-privacy: passed\n'
