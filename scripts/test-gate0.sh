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
  .operatorProxy.url == "redacted"
' "$release_tmp/evidence/manifest.json" >/dev/null
grep -q 'phase1-plan: Gate 2 Lima E2E' "$release_tmp/evidence/test-release-dogfood.log"
if grep -R --fixed-strings "$release_secret" "$release_tmp" >/dev/null 2>&1; then
  echo "gate0: release dogfood evidence leaked operator proxy URL" >&2
  exit 1
fi
rm -rf "$release_tmp"
