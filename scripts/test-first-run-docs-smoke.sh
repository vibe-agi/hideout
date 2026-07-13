#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

doc="docs/first-run-alpha.md"
test -f "$doc"

grep -q 'docs/first-run-alpha.md' README.md
grep -q 'first-run-alpha.md' docs/README.md

grep -q 'hideout package verify "$HOME/.local"' "$doc"
grep -q './install.sh --skip-init' "$doc"
grep -q 'hideout init \\' "$doc"
grep -q -- '--template dev' "$doc"
grep -q -- '--profile alpha-direct' "$doc"
grep -q -- '--network direct' "$doc"
grep -q -- '--template privacy' "$doc"
grep -q -- '--backend lima' "$doc"
grep -q -- '--network tun2socks' "$doc"
grep -q -- '--mediated-resolver 1.1.1.1' "$doc"
grep -q 'hideout doctor --profile default --backend lima --level deep' "$doc"
grep -q 'hideout doctor --profile alpha-direct --backend lima --level deep' "$doc"
grep -q 'hideout run --profile alpha-direct --backend lima -- pwd' "$doc"
grep -q 'hideout hostfs write status' "$doc"
grep -q 'hideout decision list' "$doc"
grep -q 'hideout daemon start' "$doc"
grep -q 'hideout tui --profile alpha-direct' "$doc"
grep -q 'hideout ui --no-open --print-url' "$doc"

grep -q 'weak harness only' "$doc"
grep -q 'replacement for that gate' "$doc"
grep -q 'does not by itself make a release candidate' "$doc"
grep -q 'Workspace writes are intentionally' "$doc"
grep -q 'approximately 1 GB first-use download' "$doc"
grep -q 'Direct networking does not hide' "$doc"
grep -q 'never changes a requested privacy profile to direct' "$doc"

if grep -Eq 'go run|--backend native.*isolation evidence|native.*privacy path' "$doc" README.md; then
  echo "first-run-docs-smoke: user-facing first-run docs contain stale or overclaiming command text" >&2
  exit 1
fi

echo "first-run-docs-smoke: passed"
