#!/usr/bin/env bash
set -euo pipefail

repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
cd "$repo_root"

out="$repo_root/.artifacts/045/ui/browser"
preflight=0

usage() {
  printf '%s\n' \
    "Usage: scripts/gates/browser-console.sh [--preflight] [--out DIR]" \
    "" \
    "Runs the required real-browser operator-console journey: parity, exact-" \
    "owner history, auth refusal, credential rotation, SSE loss/read-only," \
    "Draft→Plan→Confirm→Apply, keyboard/accessibility, responsive layout," \
    "bounded DOM, and measured performance. Evidence remains local."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --preflight)
      preflight=1
      shift
      ;;
    --out)
      if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
        printf 'browser-console-gate: --out requires a directory\n' >&2
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
      printf 'browser-console-gate: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

for command in go jq git; do
  if ! command -v "$command" >/dev/null 2>&1; then
    printf \
      'browser-console-gate: missing required command: %s\n' \
      "$command" >&2
    exit 1
  fi
done

expected_browser_proof_ids() {
  go run ./cmd/hideout support proof-registry --json |
    jq -ce '
      if .schema == "hideout.proof-registry/v1" then
        [.requirements[] |
          select(
            .featureId == "021-ui-e2e-proof" and
            (.proofId | startswith("021.webui.browser."))
          ) |
          .proofId
        ] |
        sort |
        if length > 0 and ((unique | length) == length) then .
        else error("browser proof registry is empty or duplicated")
        end
      else error("proof registry schema mismatch")
      end
    '
}

validate_browser_proof_records() {
  local expected="$1"
  jq -s -e --argjson expected "$expected" '
    (map(.proofId) | sort) == $expected and
    (map(.proofId) | unique | length) == length and
    all(.[];
      .featureId == "021-ui-e2e-proof" and
      .mode == "browser-e2e" and
      .status == "passed" and
      .redactionStatus == "passed" and
      (.artifacts | length) >= 7 and
      all(.artifacts[];
        (.kind | IN(
          "screenshot", "docs-report", "log", "event-summary"
        )) and
        (.sha256 | test("^[a-f0-9]{64}$")) and
        .redactionStatus == "passed"
      )
    )
  ' >/dev/null
}

browser_proof_ids="$(expected_browser_proof_ids)" || {
  printf 'browser-console-gate: canonical browser proof inventory is invalid\n' >&2
  exit 1
}

run_preflight() {
  local fixture first missing extra duplicate
  first="$(jq -er '.[0]' <<<"$browser_proof_ids")"
  fixture="$(
    jq -cn --argjson ids "$browser_proof_ids" '
      $ids[] as $proofId |
      {
        proofId:$proofId,
        featureId:"021-ui-e2e-proof",
        mode:"browser-e2e",
        status:"passed",
        redactionStatus:"passed",
        artifacts:[range(0; 7) | {
          kind:"log", sha256:("a" * 64), redactionStatus:"passed"
        }]
      }
    '
  )"
  validate_browser_proof_records "$browser_proof_ids" <<<"$fixture" || {
    printf 'browser-console-gate: exact registry fixture was rejected\n' >&2
    return 1
  }

  missing="$(jq -c --arg proofId "$first" '
    select(.proofId != $proofId)
  ' <<<"$fixture")"
  if validate_browser_proof_records "$browser_proof_ids" <<<"$missing"; then
    printf 'browser-console-gate: missing proof fixture was accepted\n' >&2
    return 1
  fi

  extra="$fixture"$'\n'"$(jq -cn '
    {
      proofId:"021.webui.browser.unregistered",
      featureId:"021-ui-e2e-proof",
      mode:"browser-e2e",
      status:"passed",
      redactionStatus:"passed",
      artifacts:[range(0; 7) | {
        kind:"log", sha256:("b" * 64), redactionStatus:"passed"
      }]
    }
  ')"
  if validate_browser_proof_records "$browser_proof_ids" <<<"$extra"; then
    printf 'browser-console-gate: extra proof fixture was accepted\n' >&2
    return 1
  fi

  duplicate="$fixture"$'\n'"$(jq -c --arg proofId "$first" '
    select(.proofId == $proofId)
  ' <<<"$fixture")"
  if validate_browser_proof_records "$browser_proof_ids" <<<"$duplicate"; then
    printf 'browser-console-gate: duplicate proof fixture was accepted\n' >&2
    return 1
  fi

  printf \
    'browser-console-gate: preflight=passed proofs=%s vmBoots=0 browserRuns=0\n' \
    "$(jq 'length' <<<"$browser_proof_ids")"
}

if [ "$preflight" -eq 1 ]; then
  run_preflight
  exit 0
fi

command -v node >/dev/null 2>&1 || {
  printf 'browser-console-gate: missing required command: node\n' >&2
  exit 1
}

find_chrome() {
  if [ -n "${HIDEOUT_CHROME_PATH:-}" ] &&
    [ -x "${HIDEOUT_CHROME_PATH:-}" ]; then
    printf '%s\n' "$HIDEOUT_CHROME_PATH"
    return 0
  fi
  for candidate in \
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
    "/Applications/Chromium.app/Contents/MacOS/Chromium" \
    "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"; do
    if [ -x "$candidate" ]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  for command in google-chrome chromium chromium-browser microsoft-edge; do
    if command -v "$command" >/dev/null 2>&1; then
      command -v "$command"
      return 0
    fi
  done
  return 1
}

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  printf 'browser-console-gate: missing shasum or sha256sum\n' >&2
  return 127
}

file_mode() {
  if stat -f '%Lp' "$1" >/dev/null 2>&1; then
    stat -f '%Lp' "$1"
    return
  fi
  stat -c '%a' "$1"
}

chrome="$(find_chrome || true)"
if [ -z "$chrome" ]; then
  printf \
    'browser-console-gate: Chrome/Chromium is required; set HIDEOUT_CHROME_PATH\n' \
    >&2
  exit 1
fi

source_commit="$(git rev-parse HEAD)"
if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then
  source_dirty=true
else
  source_dirty=false
fi

run_id="run-$(date -u +'%Y%m%dT%H%M%SZ')-$$"
run_dir="$out/$run_id"
mkdir -p "$run_dir"
chmod 0700 "$out" "$run_dir"

gate_log="$run_dir/gate.log"
printf \
  'browser-console-gate: running real Chrome journey evidence=%s\n' \
  "$run_dir"
HIDEOUT_UI_E2E_BROWSER=1 \
  HIDEOUT_UI_E2E_OUT="$run_dir" \
  HIDEOUT_UI_E2E_ARTIFACT_PREFIX="$run_id" \
  HIDEOUT_CHROME_PATH="$chrome" \
  go test -tags=hideout_e2e ./test/e2e/webui \
    -run '^TestBrowserProofPasses$' -count=1 -v 2>&1 |
  tee "$gate_log"

result="$run_dir/browser-result.json"
proofs="$run_dir/proofs.jsonl"
for required in \
  "$result" \
  "$proofs" \
  "$run_dir/accessibility.json" \
  "$run_dir/performance.json" \
  "$run_dir/network-summary.json" \
  "$run_dir/dom-summary.txt" \
  "$run_dir/webui-console.png" \
  "$run_dir/webui-stale.png" \
  "$run_dir/webui-mobile.png"; do
  if [ ! -s "$required" ]; then
    printf \
      'browser-console-gate: required evidence is missing or empty: %s\n' \
      "$required" >&2
    exit 1
  fi
done

if ! jq -e '
  .panelsVisible == [
    "Overview", "Timeline", "Executions", "Files", "Network & DNS",
    "Coverage", "Risks", "Operations", "Migration", "Configuration", "Help"
  ] and
  .liveUpdateObserved == true and
  .hiddenPollingDetected == false and
  .activity.recordCount > 200 and
  .activity.factsMatched == true and
  .activity.executionTree == true and
  .activity.guestIdentity == true and
  .activity.correlation == true and
  .activity.boundedDOM == true and
  .activity.filtersExercised == [
    "kind", "operation", "execution", "path",
    "domain", "ip", "risk-and-time"
  ] and
  .actionRoundTrip.action == "profile.transaction.apply" and
  .actionRoundTrip.requestObserved == true and
  .actionRoundTrip.payloadValidated == true and
  .actionRoundTrip.responseHandled == true and
  .actionRoundTrip.visibleStateChanged == true and
  .actionRoundTrip.revisionAdvanced == true and
  .actionRoundTrip.terminalPhase == "succeeded" and
  (.actionRoundTrip.operationId | startswith("op_")) and
  .authFailureObserved == true and
  .credentialRefreshObserved == true and
  .staleReadOnlyObserved == true and
  .keyboardNavigation == true and
  .responsiveLayout == true and
  .accessibilityViolations == [] and
  .domNodeCount > 0 and .domNodeCount <= 15000 and
  .maxMountedRows > 0 and .maxMountedRows <= 200 and
  .performance.loadToLiveMs > 0 and
  .performance.loadToLiveMs <= 5000 and
  .performance.maxFilterMs <= 2000 and
  .performance.liveUpdateMs <= 3000 and
  .performance.configurationMs <= 5000
' "$result" >/dev/null; then
  printf 'browser-console-gate: browser result failed semantic validation\n' >&2
  exit 1
fi

if ! validate_browser_proof_records "$browser_proof_ids" <"$proofs"; then
  printf 'browser-console-gate: product proof records are invalid\n' >&2
  exit 1
fi

while IFS="$(printf '\t')" read -r artifact_path expected_hash; do
  [ -n "$artifact_path" ] || continue
  artifact="$out/$artifact_path"
  if [ ! -s "$artifact" ] ||
    [ "$(sha256_file "$artifact")" != "$expected_hash" ]; then
    printf \
      'browser-console-gate: proof artifact digest mismatch: %s\n' \
      "$artifact_path" >&2
    exit 1
  fi
done < <(jq -r '[.artifacts[] | [.path, .sha256] | @tsv] | .[]' "$proofs")

if find "$run_dir" -type f ! -name '*.png' -print0 |
  xargs -0 rg -a -n \
    'ui_[0-9a-fA-F]{48}|cap_[0-9a-fA-F]{32,}|HIDEOUT_SECRET_[A-Za-z0-9_]+' \
    >/dev/null 2>&1; then
  printf \
    'browser-console-gate: control-plane material reached text evidence\n' \
    >&2
  exit 1
fi

find "$run_dir" -type f -exec chmod 0600 {} +
while IFS= read -r evidence_file; do
  if [ "$(file_mode "$evidence_file")" != "600" ]; then
    printf \
      'browser-console-gate: evidence mode is not 0600: %s\n' \
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

go_version="$(go version)"
node_version="$(node --version)"
jq_version="$(jq --version)"
chrome_version="$("$chrome" --version 2>/dev/null | head -n 1)"
summary="$out/summary.json"
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" \
  --arg run "$run_id" \
  --arg goVersion "$go_version" \
  --arg nodeVersion "$node_version" \
  --arg jqVersion "$jq_version" \
  --arg chromePath "$chrome" \
  --arg chromeVersion "$chrome_version" \
  --argjson browser "$(cat "$result")" \
  --argjson artifacts "$artifacts" \
  '{
    schema: "hideout.browser-console-gate/v1",
    generatedAt: $generatedAt,
    source: {commit: $commit, dirty: $dirty},
    result: "passed",
    run: $run,
    command:
      "scripts/gates/browser-console.sh",
    prerequisites: {
      go: $goVersion,
      node: $nodeVersion,
      jq: $jqVersion,
      chrome: {path: $chromePath, version: $chromeVersion}
    },
    journeys: {
      parityAndHistory: "passed",
      configurationTransaction: "passed",
      authenticatedSSE: "passed",
      wrongCredentialRefusal: "passed",
      credentialRotation: "passed",
      staleReadOnly: "passed",
      keyboardAndAccessibility: "passed",
      responsiveLayout: "passed",
      boundedDOMAndPerformance: "passed"
    },
    observed: {
      panels: $browser.panelsVisible,
      records: $browser.activity.recordCount,
      filters: $browser.activity.filtersExercised,
      operation: {
        id: $browser.actionRoundTrip.operationId,
        phase: $browser.actionRoundTrip.terminalPhase
      },
      domNodeCount: $browser.domNodeCount,
      maxMountedRows: $browser.maxMountedRows,
      performance: $browser.performance
    },
    artifacts: $artifacts,
    permissions: {
      directories: "0700",
      files: "0600",
      textControlPlaneScan: "passed"
    },
    claimBoundary:
      "Local real-browser evidence over the production Manager/WebUI boundary; it does not prove real-Lima observation, package identity, signing, notarization, or publication readiness."
  }' >"$summary"
chmod 0600 "$summary"

if ! jq -e '
  .schema == "hideout.browser-console-gate/v1" and
  .result == "passed" and
  (.artifacts | length) >= 10 and
  all(.journeys[]; . == "passed") and
  .observed.maxMountedRows <= 200 and
  .permissions.textControlPlaneScan == "passed"
' "$summary" >/dev/null; then
  printf 'browser-console-gate: summary failed validation\n' >&2
  exit 1
fi

printf \
  'browser-console-gate: passed evidence=%s run=%s\n' \
  "$summary" "$run_id"
