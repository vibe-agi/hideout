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
grep -q 'hideout doctor --profile default --backend lima --level deep' "$doc"
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

source_formula="packaging/homebrew/hideout.rb"
tap_formula="/Users/null/Code/github/vibe-agi/homebrew-tap/Formula/hideout.rb"
test -f "$source_formula"
test -f "$tap_formula"
for formula in "$source_formula" "$tap_formula"; do
  grep -q 'hideout setup' "$formula"
  if grep -q 'hideout init --template dev' "$formula"; then
    echo "first-run-docs-smoke: formula still teaches long default init" >&2
    exit 1
  fi
  for helper in \
    hideout-dns-stub-linux-arm64 \
    hideout-hostfsd-linux-arm64 \
    hideout-session-supervisor-linux-arm64 \
    hideout-workspace-portal-linux-arm64 \
    hideout-shim-linux-arm64; do
    grep -q "$helper" "$formula"
  done
done
if ! diff -u \
    <(sed -n '/^  def caveats$/,/^  end$/p' "$source_formula") \
    <(sed -n '/^  def caveats$/,/^  end$/p' "$tap_formula"); then
  echo "first-run-docs-smoke: source and published formula caveats drifted" >&2
  exit 1
fi

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
