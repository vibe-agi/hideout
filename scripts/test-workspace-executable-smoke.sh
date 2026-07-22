#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-workspace-executable-gate0.XXXXXX")"
cleanup() {
  find "$tmp" -depth -delete 2>/dev/null || true
}
trap cleanup EXIT

go test -count=1 ./internal/workspaceattach \
  -run '^(TestPortalOpenFlagsAllowedLocalHintDoesNotChangeWireSemantics|TestPortalOpenFlagsPreserveNoFollowSemantics)$'
go test -count=1 ./internal/productevidence \
  -run '^(TestProofRegistryCovers041WithoutLettingNotRunSatisfyRealClaims|TestWorkspaceExecutableValidatorRejectsFalseGreenArtifacts)$'
go test -count=1 ./cmd/hideout-workspace-probe ./internal/workspaceattach

GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
  go test -c -o "$tmp/workspaceattach-linux-arm64.test" ./internal/workspaceattach
test -s "$tmp/workspaceattach-linux-arm64.test"

go run ./cmd/hideout support proof-registry --json >"$tmp/proof-registry.json"
jq -e '
  ([.requirements[] | select(.featureId == "041-workspace-executable-support")] | length) == 4 and
  any(.requirements[];
    .proofId == "041.workspace-executable.real-gate2.execution" and
    .layer == "real-gate" and .requiredFor == "release-candidate" and
    .runtimePolicy == "exact-real" and
    .artifactValidator == "workspace-executable/v1") and
  any(.requirements[];
    .proofId == "041.workspace-executable.real-gate2.not-run" and
    .requiredFor == "supporting-only" and .runtimePolicy == "none")
' "$tmp/proof-registry.json" >/dev/null

echo "workspace executable Gate 0 passed (local contracts only; no real Lima claim)"
