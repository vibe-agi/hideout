#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

doc="docs/first-run-alpha.md"
test -f "$doc"

grep -q 'docs/first-run-alpha.md' README.md
grep -q 'first-run-alpha.md' docs/README.md

grep -q 'brew install vibe-agi/tap/hideout' "$doc"
grep -q 'hideout package verify "$(brew --prefix hideout)"' "$doc"
grep -q '^hideout setup$' "$doc"
grep -q 'hideout init \\' "$doc"
grep -q -- '--profile default' "$doc"
grep -q -- '--network direct' "$doc"
grep -q -- '--template privacy' "$doc"
grep -q -- '--backend lima' "$doc"
grep -q -- '--network tun2socks' "$doc"
grep -q -- '--mediated-resolver 1.1.1.1' "$doc"
grep -q 'hideout connect through default-proxy using 1.1.1.1' "$doc"
grep -q 'hideout show connection' "$doc"
grep -q '^hideout doctor$' "$doc"
grep -q 'hideout doctor --verbose' "$doc"
grep -q 'hideout run -- pwd' "$doc"
grep -q 'hideout hostfs write status' "$doc"
grep -q 'hideout decision list' "$doc"
grep -q 'hideout daemon start' "$doc"
grep -q 'hideout tui --profile default' "$doc"
grep -q 'hideout ui --no-open --print-url' "$doc"

grep -q 'weak harness only' "$doc"
grep -q 'replacement for that gate' "$doc"
grep -q 'does not by itself make a release candidate' "$doc"
grep -q 'Workspace writes are intentionally' "$doc"
grep -q 'approximately 1 GB first-use download' "$doc"
grep -q 'Direct networking does not hide' "$doc"
grep -q 'never changes a requested privacy profile to direct' "$doc"

grep -q '^hideout setup$' README.md
grep -q '^hideout setup$' README.zh-CN.md

# Published-tap parity anchors the official vibe-agi/homebrew-tap checkout to
# the receipt-rendered release formula snapshot. The snapshot proves the
# release identity and the complete teaching/helper surface without comparing
# the evolving next-candidate formula to an older public tap. The tap location
# comes from HIDEOUT_TAP_FORMULA, falling back to a sibling ../homebrew-tap
# checkout. Without a tap checkout the parity slice records not-run locally,
# while HIDEOUT_REQUIRE_TAP_PARITY=1 (the CI posture) fails closed instead of
# skipping: a skipped parity check is not parity proof.
source_formula="packaging/homebrew/hideout.rb"
test -f "$source_formula"
release_tag="$(jq -er '.current.tag' releases/current.json)"
release_version="$(jq -er '.current.version' releases/current.json)"
release_sha="$(jq -er '.current.package.artifactSHA256' releases/current.json)"
test "$release_tag" = "v$release_version"
formula_snapshot="releases/formulas/${release_tag}.rb"
test -f "$formula_snapshot"
grep -Fq "releases/download/$release_tag/hideout-$release_tag-darwin-arm64.tar.gz" "$formula_snapshot"
grep -Fq "sha256 \"$release_sha\"" "$formula_snapshot"
cmp <(tail -n +3 "$source_formula") "$formula_snapshot"
tap_formula="${HIDEOUT_TAP_FORMULA:-}"
if [ -z "$tap_formula" ] && [ -f "$ROOT/../homebrew-tap/Formula/hideout.rb" ]; then
  tap_formula="$ROOT/../homebrew-tap/Formula/hideout.rb"
fi
if [ -n "$tap_formula" ]; then
  if [ ! -f "$tap_formula" ]; then
    echo "first-run-docs-smoke: tap formula is missing at $tap_formula" >&2
    exit 1
  fi
  if ! cmp "$formula_snapshot" "$tap_formula" >/dev/null 2>&1; then
    echo "first-run-docs-smoke: official tap differs from the receipt-rendered formula snapshot for $release_tag" >&2
    exit 1
  fi
elif [ "${HIDEOUT_REQUIRE_TAP_PARITY:-0}" = "1" ]; then
  echo "first-run-docs-smoke: tap parity is required but no official tap formula was found; set HIDEOUT_TAP_FORMULA or check out vibe-agi/homebrew-tap next to this repository" >&2
  exit 1
else
  echo "first-run-docs-smoke: tap parity not-run (no official tap checkout; source-formula checks still enforced)" >&2
fi
grep -q 'hideout setup' "$source_formula"
if grep -q 'hideout init --template dev' "$source_formula"; then
  echo "first-run-docs-smoke: source formula still teaches long default init" >&2
  exit 1
fi
for helper in \
  hideout-dns-stub-linux-arm64 \
  hideout-hostfsd-linux-arm64 \
  hideout-session-supervisor-linux-arm64 \
  hideout-workspace-portal-linux-arm64 \
  hideout-shim-linux-arm64 \
  tun2socks-linux-arm64; do
  grep -q "$helper" "$source_formula"
done

# The rendered agent example must stay synchronized with the pinned fixture:
# bumping the fixture version without updating user-facing text fails here.
agent_package="$(sed -n "s/^package='\(@openai\/codex@[0-9.]*\)'\$/\1/p" scripts/test-runtime-agent-install.sh)"
test -n "$agent_package"
grep -qF "$agent_package" internal/app/setup.go
grep -qF "$agent_package" "$doc"

if grep -Eq 'go run|--backend native.*isolation evidence|native.*privacy path' "$doc" README.md; then
  echo "first-run-docs-smoke: user-facing first-run docs contain stale or overclaiming command text" >&2
  exit 1
fi

echo "first-run-docs-smoke: passed"
