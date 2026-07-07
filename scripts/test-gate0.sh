#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

go test -count=1 ./...
scripts/test-install-smoke.sh
scripts/test-package-smoke.sh
markdownlint-cli2 'docs/*.md'
jq empty schemas/*.json
test -f schemas/init-plan.schema.json
test -f schemas/init-audit-event.schema.json
test -f schemas/helper-manifest.schema.json
test -f schemas/run-plan.schema.json
test -f schemas/run-result.schema.json
test -f schemas/release-dogfood.schema.json
test -f schemas/export-artifact.schema.json
test -f schemas/daemon-status.schema.json
test -f schemas/daemon-event.schema.json
test -f packaging/homebrew/hideout.rb
if command -v ruby >/dev/null 2>&1; then
  ruby -c packaging/homebrew/hideout.rb >/dev/null
fi
grep -q 'head "https://github.com/vibe-agi/hideout.git", branch: "master"' packaging/homebrew/hideout.rb
grep -q '"init", "--no-input"' packaging/homebrew/hideout.rb
grep -q 'Initialization Is Planned, Not Scripted' docs/architecture-principles.md
grep -q 'bundle.installScript' docs/ecosystem-foundation-design.md
grep -q 'project.initScript' docs/ecosystem-foundation-design.md

phase1_required_plan="$(HIDEOUT_PHASE1_PRINT_PLAN=1 scripts/test-phase1.sh --required)"
printf '%s\n' "$phase1_required_plan" | grep -q 'Gate 0 static contract'
printf '%s\n' "$phase1_required_plan" | grep -q 'Gate 1 native smoke'
printf '%s\n' "$phase1_required_plan" | grep -q 'Gate 2 Lima E2E'
printf '%s\n' "$phase1_required_plan" | grep -q 'Gate 3 hidden proxy'
printf '%s\n' "$phase1_required_plan" | grep -q 'Gate 4 host escape boundary dry-run'
if printf '%s\n' "$phase1_required_plan" | grep -Eiq 'Capability probe|lab|Web UI|hideoutd|daemon'; then
  echo "gate0: required Phase 1 plan must not depend on lab commands, Web UI, or daemon" >&2
  printf '%s\n' "$phase1_required_plan" >&2
  exit 1
fi

release_tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-release-evidence-smoke.XXXXXX")"
release_secret="socks5://user:pass@127.0.0.1:7890"
HIDEOUT_PHASE1_PRINT_PLAN=1 \
  HIDEOUT_SECRET_DEFAULT_PROXY="$release_secret" \
  HIDEOUT_RELEASE_EVIDENCE_DIR="$release_tmp/evidence" \
  scripts/test-release-dogfood.sh >"$release_tmp/stdout" 2>"$release_tmp/stderr"
test -f "$release_tmp/evidence/manifest.json"
test -f "$release_tmp/evidence/test-release-dogfood.log"
jq -e '
  .schema == "hideout.release-dogfood.v1" and
  .status == "passed" and
  .command == "scripts/test-phase1.sh --release-candidate" and
  .operatorProxy.provided == true and
  .operatorProxy.scheme == "socks5" and
  .operatorProxy.url == "redacted" and
  (.releaseArtifact.file | test("^hideout-[A-Za-z0-9_.-]+\\.tar\\.gz$")) and
  (.releaseArtifact.sha256 | test("^[a-f0-9]{64}$")) and
  (.releaseArtifact.bytes > 0) and
  (.releaseArtifact.hideoutVersion.version | length > 0) and
  (.releaseArtifact.hideoutVersion.commit == .git.commit) and
  (.releaseArtifact.hideoutVersion.builtAt | length > 0) and
  (.releaseArtifact.hideoutVersion.go | length > 0) and
  (.releaseArtifact.hideoutVersion.platform | test("^[^/]+/[^/]+$")) and
  .cleanup.gate4BrowserProcesses == 0 and
  .cleanup.gate4TempDirs == 0 and
  .cleanup.hideoutLimaInstances == 0
' "$release_tmp/evidence/manifest.json" >/dev/null
release_artifact_file="$(jq -r '.releaseArtifact.file' "$release_tmp/evidence/manifest.json")"
test -f "$release_tmp/evidence/$release_artifact_file"
if command -v shasum >/dev/null 2>&1; then
  release_artifact_sha="$(shasum -a 256 "$release_tmp/evidence/$release_artifact_file" | awk '{print $1}')"
elif command -v sha256sum >/dev/null 2>&1; then
  release_artifact_sha="$(sha256sum "$release_tmp/evidence/$release_artifact_file" | awk '{print $1}')"
else
  echo "gate0: missing shasum or sha256sum" >&2
  exit 127
fi
test "$release_artifact_sha" = "$(jq -r '.releaseArtifact.sha256' "$release_tmp/evidence/manifest.json")"
release_artifact_extract="$(mktemp -d "${TMPDIR:-/tmp}/hideout-release-artifact-extract.XXXXXX")"
tar -xzf "$release_tmp/evidence/$release_artifact_file" -C "$release_artifact_extract"
"$release_artifact_extract/hideout/bin/hideout" version >"$release_tmp/artifact-version.out"
grep -qx "hideout $(jq -r '.releaseArtifact.hideoutVersion.version' "$release_tmp/evidence/manifest.json")" "$release_tmp/artifact-version.out"
grep -qx "commit: $(jq -r '.releaseArtifact.hideoutVersion.commit' "$release_tmp/evidence/manifest.json")" "$release_tmp/artifact-version.out"
grep -qx "builtAt: $(jq -r '.releaseArtifact.hideoutVersion.builtAt' "$release_tmp/evidence/manifest.json")" "$release_tmp/artifact-version.out"
grep -qx "go: $(jq -r '.releaseArtifact.hideoutVersion.go' "$release_tmp/evidence/manifest.json")" "$release_tmp/artifact-version.out"
grep -qx "platform: $(jq -r '.releaseArtifact.hideoutVersion.platform' "$release_tmp/evidence/manifest.json")" "$release_tmp/artifact-version.out"
rm -rf "$release_artifact_extract"
grep -q 'phase1-plan: Gate 2 Lima E2E' "$release_tmp/evidence/test-release-dogfood.log"
if grep -R --fixed-strings "$release_secret" "$release_tmp" >/dev/null 2>&1; then
  echo "gate0: release dogfood evidence leaked operator proxy URL" >&2
  exit 1
fi
rm -rf "$release_tmp"

# Isolation-evidence machine-readable contract (no Lima): per-gate emission,
# manifest aggregation shape, and release-dogfood schema for isolationGates /
# environmentSnapshot.
scripts/test-isolation-evidence-smoke.sh

# Export/share redaction boundary (no Lima): three source surfaces, schema,
# control-plane cleanliness, user selection, and evidentiary fail-closed.
scripts/test-export-redaction-smoke.sh

# hideoutd local control-plane boundary (no Lima): lifecycle, guest-unreachable
# socket placement, token auth + audited refusals, Manager parity, event stream,
# and ordered stop.
scripts/test-daemon-smoke.sh
