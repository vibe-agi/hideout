#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

usage() {
  cat >&2 <<'USAGE'
usage: scripts/test-ui-e2e.sh [--manifest-only] [--browser] [--tui] [--all] --out DIR [--require-executed]

Writes hideout.product-hardening-evidence/v1 proof output. Browser proof runs
when Node and a local Chrome/Chromium-compatible browser are available. TUI proof
runs when Go and a local script(1)-compatible terminal harness are available.
Missing browser/TUI prerequisites are explicit not-run evidence.
USAGE
}

out=""
require_executed=0
want_manifest=0
want_browser=0
want_tui=0

while [ "$#" -gt 0 ]; do
  case "$1" in
    --manifest-only)
      want_manifest=1
      ;;
    --browser)
      want_browser=1
      ;;
    --tui)
      want_tui=1
      ;;
    --all)
      want_manifest=1
      want_browser=1
      want_tui=1
      ;;
    --require-executed)
      require_executed=1
      ;;
    --out)
      shift
      if [ "$#" -eq 0 ]; then
        usage
        exit 2
      fi
      out="$1"
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "test-ui-e2e: unknown argument $1" >&2
      usage
      exit 2
      ;;
  esac
  shift
done

if [ "$want_manifest$want_browser$want_tui" = "000" ]; then
  want_manifest=1
fi
if [ -z "$out" ]; then
  echo "test-ui-e2e: --out is required" >&2
  usage
  exit 2
fi
command -v jq >/dev/null 2>&1 || { echo "test-ui-e2e: jq required" >&2; exit 127; }

mkdir -p "$out"
out="$(cd "$out" && pwd -P)"
manifest="$out/product-hardening-evidence.json"

commit="$(git rev-parse HEAD 2>/dev/null || printf 'unknown')"
dirty=false
if [ -n "$(git status --porcelain --untracked-files=normal 2>/dev/null)" ]; then
  dirty=true
fi
generated_at="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

proofs_file="$out/.proofs.jsonl"
: >"$proofs_file"

add_schema_proof() {
  jq -n \
    --arg proofId "021.evidence.schema" \
    --arg featureId "021-ui-e2e-proof" \
    --arg commandSummary "scripts/test-ui-e2e.sh --manifest-only --out <evidence-dir>" \
    '{
      proofId: $proofId,
      featureId: $featureId,
      mode: "schema",
      evidenceClass: "local-fast",
      status: "passed",
      commandSummary: $commandSummary,
      coveredClaims: [{
        claimId: "021.FR-011",
        source: "spec",
        description: "UI proof writes a product-hardening evidence manifest",
        scope: "evidence"
      }],
      prerequisites: [{name: "schema-validator", status: "available"}],
      artifacts: [{
        kind: "schema",
        path: "schemas/product-hardening-evidence.schema.json",
        redactionStatus: "passed",
        description: "Product-hardening evidence schema"
      }],
      redactionStatus: "passed"
    }' >>"$proofs_file"
}

add_not_run_proof() {
  proof_id="$1"
  mode="$2"
  scope="$3"
  prerequisite="$4"
  reason="$5"
  jq -n \
    --arg proofId "$proof_id" \
    --arg featureId "021-ui-e2e-proof" \
    --arg mode "$mode" \
    --arg scope "$scope" \
    --arg prerequisite "$prerequisite" \
    --arg reason "$reason" \
    --arg commandSummary "scripts/test-ui-e2e.sh --$scope --out <evidence-dir>" \
    '{
      proofId: $proofId,
      featureId: $featureId,
      mode: $mode,
      evidenceClass: "local-ui-e2e",
      status: "not-run",
      commandSummary: $commandSummary,
      coveredClaims: [{
        claimId: "021.FR-013",
        source: "spec",
        description: "Missing UI E2E prerequisites record not-run evidence",
        scope: $scope
      }],
      prerequisites: [{
        name: $prerequisite,
        status: "skipped",
        reason: $reason
      }],
      artifacts: [],
      redactionStatus: "not-run",
      notRunReason: $reason
    }' >>"$proofs_file"
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  shasum -a 256 "$1" | awk '{print $1}'
}

run_docs_boundary_scan() {
  report="$out/docs-boundary.txt"
  : >"$report"
  require_doc_text() {
    pattern="$1"
    file="$2"
    description="$3"
    if ! grep -Eq "$pattern" "$file"; then
      echo "missing: $description ($file / $pattern)" >>"$report"
      return 1
    fi
    echo "ok: $description" >>"$report"
  }
  forbid_doc_text() {
    pattern="$1"
    file="$2"
    description="$3"
    if grep -Eq "$pattern" "$file"; then
      echo "forbidden: $description ($file / $pattern)" >>"$report"
      return 1
    fi
    echo "ok: absent $description" >>"$report"
  }

  ok=0
  require_doc_text 'test-only automation' docs/tui-webui-experience.md 'browser automation is test-only, not product authority' || ok=1
  require_doc_text 'script\(1\)' docs/tui-webui-experience.md 'TUI proof launches a real terminal process' || ok=1
  require_doc_text 'fallback interval, not the normal live refresh mechanism' docs/tui-webui-experience.md 'TUI interval is fallback-only' || ok=1
  require_doc_text 'product-hardening evidence manifest' docs/tui-webui-experience.md 'UI proof writes product-hardening evidence' || ok=1
  require_doc_text 'local UI E2E evidence' docs/privacy-run-test-plan.md 'local UI E2E is not release readiness' || ok=1
  forbid_doc_text 'richer browser-driven UX automation remains a later product-hardening task' docs/tui-webui-experience.md 'stale browser E2E deferred claim' || ok=1
  if [ "$ok" -ne 0 ]; then
    cat "$report" >&2
    exit 1
  fi
  jq -n \
    --arg proofId "021.docs.boundary" \
    --arg featureId "021-ui-e2e-proof" \
    --arg sha "$(sha256_file "$report")" \
    '{
      proofId: $proofId,
      featureId: $featureId,
      mode: "docs",
      evidenceClass: "local-fast",
      status: "passed",
      commandSummary: "scripts/test-ui-e2e.sh --all --out <evidence-dir>",
      coveredClaims: [
        {
          claimId: "021.FR-015",
          source: "docs",
          description: "Docs distinguish browser E2E, terminal E2E, reducer harness, fixture proof, local-fast proof, and release readiness",
          scope: "docs"
        },
        {
          claimId: "021.FR-016",
          source: "docs",
          description: "Docs do not claim release readiness from local UI E2E alone",
          scope: "docs"
        },
        {
          claimId: "021.FR-017",
          source: "docs",
          description: "Docs do not describe browser automation as product authority",
          scope: "docs"
        }
      ],
      prerequisites: [{name: "docs-scan", status: "available"}],
      artifacts: [{
        kind: "docs-report",
        path: "docs-boundary.txt",
        sha256: $sha,
        redactionStatus: "passed",
        description: "UI E2E docs boundary scan"
      }],
      redactionStatus: "passed"
    }' >>"$proofs_file"
}

find_chrome() {
  if [ -n "${HIDEOUT_CHROME_PATH:-}" ] && [ -x "${HIDEOUT_CHROME_PATH:-}" ]; then
    printf '%s\n' "$HIDEOUT_CHROME_PATH"
    return 0
  fi
  for candidate in \
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
    "/Applications/Chromium.app/Contents/MacOS/Chromium" \
    "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"
  do
    if [ -x "$candidate" ]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  for name in google-chrome chromium chromium-browser microsoft-edge; do
    if command -v "$name" >/dev/null 2>&1; then
      command -v "$name"
      return 0
    fi
  done
  return 1
}

run_browser_proof() {
  if [ "${HIDEOUT_UI_E2E_DISABLE_BROWSER:-0}" = "1" ]; then
    add_not_run_proof "021.webui.browser.console" "browser-e2e" "browser" "browser-disabled" "browser proof disabled by HIDEOUT_UI_E2E_DISABLE_BROWSER"
    return
  fi
  if ! command -v node >/dev/null 2>&1; then
    add_not_run_proof "021.webui.browser.console" "browser-e2e" "browser" "node" "node is required for the WebUI browser proof"
    return
  fi
  chrome="$(find_chrome || true)"
  if [ -z "$chrome" ]; then
    add_not_run_proof "021.webui.browser.console" "browser-e2e" "browser" "chrome" "Chrome or Chromium is required for the WebUI browser proof"
    return
  fi
  browser_dir="$out/browser"
  mkdir -p "$browser_dir"
  HIDEOUT_UI_E2E_BROWSER=1 \
    HIDEOUT_UI_E2E_OUT="$browser_dir" \
    HIDEOUT_UI_E2E_ARTIFACT_PREFIX="browser" \
    HIDEOUT_CHROME_PATH="$chrome" \
    go test -tags=hideout_e2e ./test/e2e/webui -run TestBrowserProofPasses -count=1
  if [ ! -s "$browser_dir/proofs.jsonl" ]; then
    echo "test-ui-e2e: browser proof did not write proofs.jsonl" >&2
    exit 1
  fi
  cat "$browser_dir/proofs.jsonl" >>"$proofs_file"
}

run_tui_proof() {
  if [ "${HIDEOUT_UI_E2E_DISABLE_TUI:-0}" = "1" ]; then
    add_not_run_proof "021.tui.pty.console" "pty-e2e" "tui" "tui-disabled" "TUI proof disabled by HIDEOUT_UI_E2E_DISABLE_TUI"
    return
  fi
  if ! command -v go >/dev/null 2>&1; then
    add_not_run_proof "021.tui.pty.console" "pty-e2e" "tui" "go" "go is required for the TUI PTY proof"
    return
  fi
  script_bin="$(command -v script || true)"
  if [ -z "$script_bin" ]; then
    add_not_run_proof "021.tui.pty.console" "pty-e2e" "tui" "script" "script(1) is required for the TUI PTY proof"
    return
  fi
  tui_dir="$out/tui"
  mkdir -p "$tui_dir"
  HIDEOUT_UI_E2E_TUI=1 \
    HIDEOUT_UI_E2E_OUT="$tui_dir" \
    HIDEOUT_UI_E2E_ARTIFACT_PREFIX="tui" \
    HIDEOUT_TUI_SCRIPT_PATH="$script_bin" \
    go test -tags=hideout_e2e ./test/e2e/tui -run TestTUIProofPasses -count=1
  if [ ! -s "$tui_dir/proofs.jsonl" ]; then
    echo "test-ui-e2e: TUI proof did not write proofs.jsonl" >&2
    exit 1
  fi
  cat "$tui_dir/proofs.jsonl" >>"$proofs_file"
}

if [ "$want_manifest" -eq 1 ]; then
  add_schema_proof
fi
if [ "$want_browser" -eq 1 ]; then
  run_browser_proof
fi
if [ "$want_tui" -eq 1 ]; then
  run_tui_proof
fi
run_docs_boundary_scan

jq -s \
  --arg generatedAt "$generated_at" \
  --arg commit "$commit" \
  --argjson dirty "$dirty" \
  '{
    version: "hideout.product-hardening-evidence/v1",
    generatedAt: $generatedAt,
    commit: $commit,
    dirty: $dirty,
    proofs: .
  }' "$proofs_file" >"$manifest"
rm -f "$proofs_file"

go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json "$manifest"

if [ "$require_executed" -eq 1 ] && jq -e 'any(.proofs[]; .status == "not-run")' "$manifest" >/dev/null; then
  echo "test-ui-e2e: required proof lane did not execute" >&2
  exit 1
fi

echo "ui-e2e: evidence $manifest"
