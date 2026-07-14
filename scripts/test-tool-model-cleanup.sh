#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

legacy_pattern='npmGlobals|npm-global|npm package|npm-package|npmCommand|npm-command|tools\.presets|tool preset|package-manager provider|provider execution|toolPresets|node-dev|base-dev'
transition_pattern='being removed|removal in progress|code still exists today|currently npm-based|npm-backed subcommands still exist'

allowed_legacy_hit() {
  local path="$1"
  case "$path" in
    ./scripts/test-tool-model-cleanup.sh) return 0 ;;
    ./specs/002-guided-first-run/*) return 0 ;;
    ./internal/app/app.go) return 0 ;;
    ./internal/decision/doc.go) return 0 ;;
    ./internal/profile/profile.go) return 0 ;;
    ./internal/inittask/inittask.go) return 0 ;;
    ./internal/app/*_test.go) return 0 ;;
    ./internal/backend/lima/*_test.go) return 0 ;;
    ./internal/inittask/*_test.go) return 0 ;;
    ./internal/manager/*_test.go) return 0 ;;
    ./internal/profile/*_test.go) return 0 ;;
    ./internal/profile/testdata/tool_model/*) return 0 ;;
    ./specs/007-daemon-live-operations-console/plan.md) return 0 ;;
    ./specs/012-operator-decision-center/plan.md) return 0 ;;
  esac
  return 1
}

fail=0
while IFS= read -r hit; do
  path="${hit%%:*}"
  case "$path" in
    ./*) ;;
    *) path="./$path" ;;
  esac
  if ! allowed_legacy_hit "$path"; then
    echo "tool-model-cleanup: disallowed legacy tool-model reference: $hit" >&2
    fail=1
  fi
done < <(git grep -nE "$legacy_pattern" -- . ':(exclude).codegraph/**' || true)

while IFS= read -r hit; do
  echo "tool-model-cleanup: stale transition wording: $hit" >&2
  fail=1
done < <(git grep -nE "$transition_pattern" -- README.md README.zh-CN.md docs \
  specs/002-guided-first-run ':(exclude)specs/002-guided-first-run/tasks.md' || true)

if ! grep -n '"expectedCommands"' schemas/profile.schema.json >/dev/null; then
  echo "tool-model-cleanup: profile schema does not define tools.expectedCommands" >&2
  fail=1
fi

if grep -En '"presets"|"npmGlobals"' schemas/profile.schema.json schemas/init-plan.schema.json >/dev/null; then
  echo "tool-model-cleanup: schema still exposes legacy tool model fields" >&2
  fail=1
fi

exit "$fail"
