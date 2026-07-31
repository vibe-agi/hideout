#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"
# shellcheck source=scripts/lib/gate-result.sh
. "$repo_root/scripts/lib/gate-result.sh"
gate_completed=0

umask 077
out="$repo_root/.artifacts/045/ui"
preflight_only=0

usage() {
  printf '%s\n' \
    "Usage: scripts/gates/release-candidate-ui.sh [--preflight] [--out DIR]" \
    "" \
    "Runs the feature 045 first-use/help, real-PTY TUI, shared-state/parity," \
    "stale/recovery/injection, and real-Chrome browser journeys." \
    "" \
    "Evidence is private, digest-bound, and local. This command never publishes."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --preflight)
      preflight_only=1
      shift
      ;;
    --out)
      [ "$#" -ge 2 ] && [ -n "${2:-}" ] || {
        printf 'release-candidate-ui: --out requires a directory\n' >&2
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
      printf 'release-candidate-ui: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    printf 'release-candidate-ui: missing required command: %s\n' "$1" >&2
    exit 1
  }
}

for required_command in \
  awk bash find git go jq node perl rg shasum stat; do
  require_command "$required_command"
done

find_chrome() {
  if [ -n "${HIDEOUT_CHROME_PATH:-}" ] &&
    [ -x "${HIDEOUT_CHROME_PATH:-}" ]; then
    printf '%s\n' "$HIDEOUT_CHROME_PATH"
    return 0
  fi
  local candidate
  for candidate in \
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
    "/Applications/Chromium.app/Contents/MacOS/Chromium" \
    "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"; do
    if [ -x "$candidate" ]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  local command_name
  for command_name in \
    google-chrome chromium chromium-browser microsoft-edge; do
    if command -v "$command_name" >/dev/null 2>&1; then
      command -v "$command_name"
      return 0
    fi
  done
  return 1
}

chrome="$(find_chrome || true)"
if [ -z "$chrome" ]; then
  printf \
    'release-candidate-ui: Chrome/Chromium is required; set HIDEOUT_CHROME_PATH\n' \
    >&2
  exit 1
fi

expected_contract_claims='[
  "A01", "A07", "AT08", "C01", "C03",
  "C04", "H03", "U01", "U03", "U04"
]'
if ! jq -e \
  --argjson expected "$expected_contract_claims" '
    [.claims[] |
      select((.judges // []) | index("release-candidate-ui")) |
      .id] | sort == ($expected | sort)
  ' scripts/mutation/045/contracts.json >/dev/null; then
  printf \
    'release-candidate-ui: release-candidate-ui claim contract set drifted\n' \
    >&2
  exit 1
fi

if [ "$preflight_only" -eq 1 ]; then
  bash -n \
    scripts/gates/browser-console.sh \
    scripts/gates/release-candidate-ui.sh
  node --check test/e2e/webui/proof.mjs
  scripts/gates/browser-console.sh --help >/dev/null
  "$chrome" --version >/dev/null 2>&1
  go test -run '^$' \
    ./internal/app \
    ./internal/tui/... \
    ./internal/liveconsole \
    ./internal/daemon \
    ./internal/daemon/uiweb_assets >/dev/null
  printf 'release-candidate-ui: preflight=passed\n'
  exit 0
fi

if [ -L "$out" ]; then
  printf 'release-candidate-ui: evidence directory must not be a symlink\n' >&2
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
  printf 'release-candidate-ui: run directory already exists\n' >&2
  exit 1
}
mkdir -p "$run_dir/tests" "$run_dir/lanes"
chmod 0700 "$run_dir" "$run_dir/tests" "$run_dir/lanes"

scratch="$(mktemp -d /tmp/hideout-release-ui.XXXXXX)"
cleanup() {
  local exit_status=$?
  case "${scratch:-}" in
    /tmp/hideout-release-ui.*)
      [ ! -d "$scratch" ] || find "$scratch" -depth -delete
      ;;
    *)
      printf 'release-candidate-ui: refusing unexpected scratch cleanup\n' >&2
      ;;
  esac
  if [ "$exit_status" -eq 0 ]; then
    gate_require_completion "release-candidate-ui"
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

first_use_expected='[
  "TestPrimaryHelpShowsOrdinaryJourneyBeforeExpandedIndex",
  "TestContextualHelpIsSuccessfulAndWritesNoState",
  "TestHelpRejectsUnknownTopicWithExpandedIndexHint",
  "TestCommandCatalogCoversEveryTopLevelRoute",
  "TestCommandCatalogMetadataIsCompleteAndSearchable",
  "TestOperatorHelpProjectionIsValidVisibleAndDetached",
  "TestCommandCatalogValidationRejectsStaleOrAmbiguousEntries",
  "TestHelpGoldens",
  "TestHelpFindsCommonTaskInAtMostTwoInvocations",
  "TestSetupFreshReviewConfirmAndApply",
  "TestSetupNextStepsKeepsOrdinaryJourneyFirst",
  "TestSetupCancelAndNonTerminalPerformNoApply",
  "TestSetupCancellationPreservesDurableStoreState",
  "TestSetupReadySendsNoApply",
  "TestSetupBlockedAndRepairableRenderRecoveryWithoutApply",
  "TestConnectionMutationNamesDesiredEffectiveAndPendingNextAttach",
  "TestOperationRecoveryGuidancePreservesExactIdentityAndProof",
  "TestLoopbackUIReceivesRenderOnlyHelpCatalog"
]'

terminal_expected='[
  "TestTUIProgramEntersAndRestoresAlternateScreenWithKeyboardNavigation",
  "TestTUIProgramRestoresAlternateScreenAfterCtrlC",
  "TestTUIProgramRestoresAlternateScreenAfterPanic",
  "TestTUIRejectsNonTTYWithOnceRecovery",
  "TestTUIOnceIsPlainAndWorksWithoutTTY",
  "TestTUIConfigPTYDraftReviewConfirmApplyAndTerminalEvidence",
  "TestTUIConfigPTYSecretInputNeverEchoesAndCancelClears",
  "TestTUIConfigPTYStalePlanIsReadOnlyWithoutApply",
  "TestTUIConfigPTYResponseLossKeepsOperationLookup",
  "TestTUILifecyclePTYShowsActiveBlockerWithoutApply",
  "TestTUILifecyclePTYCleanTypedConfirmationAndTerminalResult",
  "TestRunClientTerminalAutoRawResizeAndRestore",
  "TestTUIConfigurationClientUsesSharedTransactionWithoutValueEcho"
]'

semantics_expected='[
  "TestModelResizeKeepsQuitAndHelpAvailableBelowMinimum",
  "TestModelHelpModalUsesInjectedCatalogForActiveView",
  "TestModelFocusSessionSelectionAndDetailDrillDown",
  "TestModelSequenceGapMakesConfigReadOnlyUntilAuthoritativeSnapshot",
  "TestModelOperationsSelectionDetailAndExactResponseLossResume",
  "TestActivityViewQueriesManagerTabsAndShowsCorrelatedRiskEvidence",
  "TestActivityViewExplainsUnavailableOwnerAndReducedCoverage",
  "TestModelStaleStateNeverOpensLifecycleMutation",
  "TestOverviewGoldenLayouts",
  "TestOverviewSanitizesTerminalControlAndBidiInput",
  "TestActivityReducedCoverageNeverPresentsNoRowsAsProofOfNoActivity",
  "TestActivitySanitizesObservedFieldsAndFitsTerminal",
  "TestActivityCoverageHUDExplainsHistoryRetentionQuotaAndDamage",
  "TestActivityCoverageRendersIndependentSubsystemEvidence",
  "TestConfigSeparatesDesiredEffectiveTransitionAndScope",
  "TestConfigStaleIsReadOnlyAndSanitizesCapabilityReason",
  "TestOperationsRendersDurableIdentityPhaseAndProgressiveEvidence",
  "TestOperationsResponseLossResumesExactExistingID",
  "TestOperationsSanitizesEvidenceAndRecoveryText",
  "TestOperationDetailExplainsRecoveryRollbackAndStoppingStates",
  "TestHelpSanitizesCatalogTextAndFitsWidth",
  "TestParityFixtureSnapshotAndDetailsExposeIdenticalSurfaceFacts",
  "TestParityFixtureEventStreamConvergesWithAuthoritativeRefresh",
  "TestReducerV2SequenceGapIsStickyReadOnly",
  "TestBrowserProfileTransactionDraftReviewConfirmApplyAndStalePlan",
  "TestBrowserProfileTransactionResponseLossRetryIsIdempotent",
  "TestBrowserSSEGapIsStickyReadOnlyUntilAuthoritativeReseed",
  "TestBrowserSSECredentialRotationExpiresStreamAndRequiresFreshSeed",
  "TestBrowserSSEDaemonRestartRejectsOldInstanceUntilReseed",
  "TestBrowserCredentialGrammarMatchesManagerIssuer",
  "TestBrowserClientRefreshesCredentialOnlyFromClearedFragment",
  "TestBrowserClientRejectsResponseFromSupersededCredential",
  "TestBrowserClientAndAppHaveNoHealthyStreamPolling",
  "TestBrowserConfigurationDraftLayersConfirmationAndTerminalEvidence",
  "TestBrowserEventV2ReducerFailsClosedUntilAuthoritativeReseed",
  "TestPresentationEscapesUntrustedControlTextAndBoundsCollections",
  "TestBrowserConsoleHasKeyboardAccessibleBoundedPresentation",
  "TestHandlerServesTypedAssetsWithStrictBrowserBoundary",
  "TestActivityViewModelsCoverOwnerTimelineTreeSubjectsAndEvidence",
  "TestCompoundFiltersCursorInheritanceRetainedGapAndCorrelation"
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
  printf 'release-candidate-ui: running %s\n' "$suite_id"
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
    printf 'release-candidate-ui: %s passed (%s tests)\n' \
      "$suite_id" "$expected_count"
  else
    failed_suites=$((failed_suites + 1))
    printf \
      'release-candidate-ui: %s failed (exit=%d expected=%s observed=%s)\n' \
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
    --argjson semanticValid "$semantic_valid" '
      {
        schema:"hideout.ui-go-suite/v1",
        id:$id,
        result:$result,
        exitCode:$exitCode,
        startedAt:$startedAt,
        finishedAt:$finishedAt,
        expectedCount:$expectedCount,
        observedCount:$observedCount,
        exactPassSet:$semanticValid,
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
      --argjson suite "$(cat "$result_path")" \
      '. + [$suite]' <<<"$suites_json"
  )"
}

run_go_suite first-use-help "$first_use_expected" \
  ./internal/app ./internal/daemon
run_go_suite terminal-pty "$terminal_expected" \
  ./internal/app
run_go_suite console-semantics "$semantics_expected" \
  ./internal/tui/... \
  ./internal/liveconsole \
  ./internal/daemon \
  ./internal/daemon/uiweb_assets

browser_log="$run_dir/lanes/browser-console.log"
browser_summary="$run_dir/browser/summary.json"
browser_started="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
printf 'release-candidate-ui: running browser-console\n'
set +e
HIDEOUT_CHROME_PATH="$chrome" \
  scripts/gates/browser-console.sh \
  --out "$run_dir/browser" >"$browser_log" 2>&1
browser_exit=$?
set -e
browser_finished="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
[ -s "$browser_log" ] ||
  printf 'browser lane produced no output (exit=%d)\n' \
    "$browser_exit" >"$browser_log"
chmod 0600 "$browser_log"

browser_valid=false
browser_run=""
browser_artifact_count=0
if [ "$browser_exit" -eq 0 ] && [ -f "$browser_summary" ] &&
  jq -e \
    --arg commit "$source_commit" \
    --argjson dirty "$source_dirty" '
      .schema == "hideout.browser-console-gate/v1" and
      .result == "passed" and
      .source == {commit:$commit,dirty:$dirty} and
      all(.journeys[]; . == "passed") and
      .observed.maxMountedRows > 0 and
      .observed.maxMountedRows <= 200 and
      .permissions.textControlPlaneScan == "passed" and
      (.artifacts | length) >= 10
    ' "$browser_summary" >/dev/null; then
  browser_valid=true
  while IFS=$'\t' read -r relative expected_sha; do
    safe_relative_path "$relative" || {
      browser_valid=false
      break
    }
    artifact_path="$run_dir/browser/$relative"
    if [ ! -f "$artifact_path" ] ||
      [ -L "$artifact_path" ] ||
      [ "$(sha256_file "$artifact_path")" != "$expected_sha" ] ||
      [ "$(file_mode "$artifact_path")" != "600" ]; then
      browser_valid=false
      break
    fi
  done < <(jq -r '.artifacts[] | [.path,.sha256] | @tsv' "$browser_summary")
fi
if [ "$browser_valid" = true ]; then
  browser_run="$(jq -er '.run' "$browser_summary")"
  browser_artifact_count="$(jq '.artifacts | length' "$browser_summary")"
  printf 'release-candidate-ui: browser-console passed\n'
else
  printf \
    'release-candidate-ui: browser-console failed (exit=%d)\n' \
    "$browser_exit" >&2
  tail -30 "$browser_log" >&2
fi

first_use_valid=false
terminal_valid=false
semantics_valid=false
if jq -e '
    .id == "first-use-help" and
    .result == "passed" and
    .exactPassSet == true and
    .expectedCount == 18 and
    .observedCount == 18
  ' "$run_dir/tests/first-use-help.result.json" >/dev/null; then
  first_use_valid=true
fi
if jq -e '
    .id == "terminal-pty" and
    .result == "passed" and
    .exactPassSet == true and
    .expectedCount == 13 and
    .observedCount == 13
  ' "$run_dir/tests/terminal-pty.result.json" >/dev/null; then
  terminal_valid=true
fi
if jq -e '
    .id == "console-semantics" and
    .result == "passed" and
    .exactPassSet == true and
    .expectedCount == 40 and
    .observedCount == 40
  ' "$run_dir/tests/console-semantics.result.json" >/dev/null; then
  semantics_valid=true
fi

privacy_scan_valid=true
if find "$run_dir" -type f ! -name '*.png' -print0 |
  xargs -0 rg -a -n \
    'ui_[0-9a-fA-F]{48}|cap_[0-9a-fA-F]{32,}|HIDEOUT_SECRET_[A-Za-z0-9_]+|socks5://[^[:space:]]+:[^@[:space:]]+@' \
    >/dev/null 2>&1; then
  privacy_scan_valid=false
fi
# The single-quoted Perl program must be passed verbatim.
# shellcheck disable=SC2016
if find "$run_dir" -type f ! -name '*.png' -print0 |
  xargs -0 perl -0777 -ne '
    $found = 1 if /[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/;
    END { exit($found ? 0 : 1) }
  ' \
    >/dev/null 2>&1; then
  privacy_scan_valid=false
fi

aggregate_result="passed"
if [ "$failed_suites" -ne 0 ] ||
  [ "$first_use_valid" != true ] ||
  [ "$terminal_valid" != true ] ||
  [ "$semantics_valid" != true ] ||
  [ "$browser_valid" != true ] ||
  [ "$privacy_scan_valid" != true ]; then
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

summary_path="$run_dir/summary.json"
browser_summary_input="$browser_summary"
if [ ! -f "$browser_summary_input" ]; then
  browser_summary_input="$scratch/browser-summary-missing.json"
  printf '{}\n' >"$browser_summary_input"
fi
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg result "$aggregate_result" \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" \
  --arg runId "$run_id" \
  --arg hostOS "$(uname -s)" \
  --arg hostArch "$(uname -m)" \
  --arg goVersion "$(go version)" \
  --arg nodeVersion "$(node --version)" \
  --arg chromeVersion "$("$chrome" --version 2>/dev/null | head -1)" \
  --arg browserStarted "$browser_started" \
  --arg browserFinished "$browser_finished" \
  --arg browserRun "$browser_run" \
  --arg browserSummarySHA256 "$(
    if [ -f "$browser_summary" ]; then
      sha256_file "$browser_summary"
    fi
  )" \
  --arg browserLogSHA256 "$(sha256_file "$browser_log")" \
  --argjson browserExit "$browser_exit" \
  --argjson browserArtifactCount "$browser_artifact_count" \
  --argjson firstUseValid "$first_use_valid" \
  --argjson terminalValid "$terminal_valid" \
  --argjson semanticsValid "$semantics_valid" \
  --argjson browserValid "$browser_valid" \
  --argjson privacyScanValid "$privacy_scan_valid" \
  --argjson suites "$suites_json" \
  --slurpfile browser "$browser_summary_input" \
  --slurpfile artifacts "$artifact_rows" '
    {
      schema:"hideout.release-candidate-ui-evidence/v1",
      generatedAt:$generatedAt,
      result:$result,
      candidateAcceptance:
        ($result == "passed" and ($dirty | not)),
      source:{commit:$commit,dirty:$dirty},
      runId:$runId,
      host:{
        os:$hostOS,
        arch:$hostArch,
        goVersion:$goVersion,
        nodeVersion:$nodeVersion,
        chromeVersion:$chromeVersion
      },
      suites:$suites,
      browser:{
        result:
          (if $browserValid then "passed" else "failed" end),
        startedAt:$browserStarted,
        finishedAt:$browserFinished,
        exitCode:$browserExit,
        run:$browserRun,
        summary:"browser/summary.json",
        summarySHA256:$browserSummarySHA256,
        log:{path:"lanes/browser-console.log",sha256:$browserLogSHA256},
        artifactCount:$browserArtifactCount,
        observed:
          (if ($browser | length) == 1 then $browser[0].observed else null end)
      },
      validation:{
        firstUseAndHelp:$firstUseValid,
        terminalPTY:$terminalValid,
        sharedConsoleSemantics:$semanticsValid,
        realBrowser:$browserValid,
        privateEvidence:$privacyScanValid
      },
      claims:{
        firstTimeJourney:$firstUseValid,
        sharedHelpCatalog:($firstUseValid and $semanticsValid),
        terminalHUD:($terminalValid and $semanticsValid),
        terminalRestoration:$terminalValid,
        terminalConfiguration:$terminalValid,
        staleReadOnly:($terminalValid and $semanticsValid and $browserValid),
        responseLossRecovery:($terminalValid and $semanticsValid),
        surfaceParity:($semanticsValid and $browserValid),
        honestCoverage:$semanticsValid,
        explainableRisk:$semanticsValid,
        controlTextInjectionSafe:($semanticsValid and $privacyScanValid),
        browserHistoryAndFilters:$browserValid,
        browserAccessibility:$browserValid,
        browserCredentialHygiene:$browserValid,
        boundedPresentation:($semanticsValid and $browserValid)
      },
      claimReceipts:{
        A01:{
          passed:($terminalValid and $browserValid),
          requirements:{
            draftApplyRequests:0,
            confirmedApplyRequests:1,
            exactOperationId:true
          }
        },
        A07:{
          passed:($terminalValid and $semanticsValid and $browserValid),
          requirements:{
            controlsEnabledWhileStale:false,
            rejectedMutationAttempts:1,
            authoritativeReseedObserved:true
          }
        },
        AT08:{
          passed:$semanticsValid,
          requirements:{
            ruleAndVersionPresent:true,
            evidenceRefsComplete:true,
            policyAndObservationSeparate:true,
            deterministicOutput:true,
            nextActionPresent:true
          }
        },
        C01:{
          passed:$semanticsValid,
          requirements:{
            coverageSubsystems:["process","file","network","dns"],
            independentIntervals:true,
            reasonGenerationLossRetentionFields:true,
            sharedGlobalInterval:false
          }
        },
        C03:{
          passed:($semanticsValid and $browserValid),
          requirements:{
            partialRenderedHealthy:false,
            unavailableRenderedAsZeroActivity:false,
            mutationEnabledOnReducedCoverage:false,
            coverageReasonVisible:true
          }
        },
        C04:{
          passed:($semanticsValid and $browserValid),
          requirements:{
            duplicateCountDelta:0,
            gapRequiresReseed:true,
            liveWithoutReseed:false,
            healthyPollingRequests:0
          }
        },
        H03:{
          passed:($firstUseValid and $semanticsValid and $browserValid),
          requirements:{
            helpWrites:0,
            unknownTopicExpandedHint:true,
            surfaceCatalogsEqual:true,
            surfaceEffectsEqual:true
          }
        },
        U01:{
          passed:($terminalValid and $semanticsValid),
          requirements:{
            alternateScreenEntered:true,
            alternateScreenRestored:true,
            keyboardDialogReached:true,
            primaryFactsVisible:true,
            onceModePlain:true
          }
        },
        U03:{
          passed:($firstUseValid and $semanticsValid and $browserValid),
          requirements:{
            surfaceFactsEqual:true,
            desiredEffectiveDistinct:true,
            priorSessionSnapshotImmutable:true,
            stateLabelsComplete:[
              "live","next-attach","recreate","stale",
              "blocked","failed","rolled-back"
            ]
          }
        },
        U04:{
          passed:($terminalValid and $semanticsValid and $browserValid),
          requirements:{
            phases:["draft","plan","review","confirm","apply","terminal"],
            preconfirmApplyRequests:0,
            operationIdStable:true,
            terminalEvidencePresent:true,
            responseLossLookupSucceeded:true
          }
        }
      },
      artifacts:$artifacts,
      limitations:
        ([
          "The real-browser lane uses a production Manager/WebUI boundary with a deterministic local fixture; real-Lima workload attribution is owned by T153.",
          "The exact installed-package first-run journey remains T164/T167; this gate binds the recorded source tree."
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
  --argjson contractClaims "$expected_contract_claims" '
    (.result == "passed") ==
      (all(.validation[]; . == true) and
       all(.suites[]; .result == "passed") and
       .browser.result == "passed") and
    (.result != "passed" or all(.claims[]; . == true)) and
    ([.claimReceipts | keys[]] | sort) ==
      ($contractClaims | sort) and
    (.result != "passed" or
      all(.claimReceipts[]; .passed == true)) and
    .claimReceipts.A01.requirements == {
      draftApplyRequests:0,
      confirmedApplyRequests:1,
      exactOperationId:true
    } and
    .claimReceipts.A07.requirements == {
      controlsEnabledWhileStale:false,
      rejectedMutationAttempts:1,
      authoritativeReseedObserved:true
    } and
    .claimReceipts.AT08.requirements == {
      ruleAndVersionPresent:true,
      evidenceRefsComplete:true,
      policyAndObservationSeparate:true,
      deterministicOutput:true,
      nextActionPresent:true
    } and
    .claimReceipts.C01.requirements == {
      coverageSubsystems:["process","file","network","dns"],
      independentIntervals:true,
      reasonGenerationLossRetentionFields:true,
      sharedGlobalInterval:false
    } and
    .claimReceipts.C03.requirements == {
      partialRenderedHealthy:false,
      unavailableRenderedAsZeroActivity:false,
      mutationEnabledOnReducedCoverage:false,
      coverageReasonVisible:true
    } and
    .claimReceipts.C04.requirements == {
      duplicateCountDelta:0,
      gapRequiresReseed:true,
      liveWithoutReseed:false,
      healthyPollingRequests:0
    } and
    .claimReceipts.H03.requirements == {
      helpWrites:0,
      unknownTopicExpandedHint:true,
      surfaceCatalogsEqual:true,
      surfaceEffectsEqual:true
    } and
    .claimReceipts.U01.requirements == {
      alternateScreenEntered:true,
      alternateScreenRestored:true,
      keyboardDialogReached:true,
      primaryFactsVisible:true,
      onceModePlain:true
    } and
    .claimReceipts.U03.requirements == {
      surfaceFactsEqual:true,
      desiredEffectiveDistinct:true,
      priorSessionSnapshotImmutable:true,
      stateLabelsComplete:[
        "live","next-attach","recreate","stale",
        "blocked","failed","rolled-back"
      ]
    } and
    .claimReceipts.U04.requirements == {
      phases:["draft","plan","review","confirm","apply","terminal"],
      preconfirmApplyRequests:0,
      operationIdStable:true,
      terminalEvidencePresent:true,
      responseLossLookupSucceeded:true
    } and
    all(.artifacts[];
      .mode == "0600" and
      (.sha256 | test("^[a-f0-9]{64}$")))
  ' "$summary_path" >/dev/null; then
  printf \
    'release-candidate-ui: aggregate summary is internally inconsistent\n' \
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
      schema:"hideout.release-candidate-ui-pointer/v1",
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

printf 'release-candidate-ui: evidence=%s\n' "$summary_path"
if [ "$aggregate_result" != "passed" ]; then
  printf 'release-candidate-ui: failed\n' >&2
  exit 1
fi
gate_completed=1
printf 'release-candidate-ui: passed\n'
