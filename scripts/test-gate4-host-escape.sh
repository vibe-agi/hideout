#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "gate4: missing required command: $1" >&2
    exit 127
  fi
}

preflight_real_browser_launcher() {
  if [ "${HIDEOUT_GATE4_REAL_BROWSER:-}" != "1" ]; then
    return
  fi

  if [ -n "${HIDEOUT_BROWSER_PATH:-}" ]; then
    local launcher="$HIDEOUT_BROWSER_PATH"
    if [[ "$launcher" == */* ]]; then
      if [ ! -x "$launcher" ]; then
        echo "gate4: HIDEOUT_BROWSER_PATH is not executable: $launcher" >&2
        exit 2
      fi
    elif ! command -v "$launcher" >/dev/null 2>&1; then
      echo "gate4: HIDEOUT_BROWSER_PATH is not on PATH: $launcher" >&2
      exit 2
    fi
    case "$(basename "$launcher")" in
      open|xdg-open)
        echo "gate4: HIDEOUT_BROWSER_PATH must be a direct Chromium-compatible browser binary, not $launcher" >&2
        exit 2
        ;;
    esac
    return
  fi

  case "$(uname -s)" in
    Darwin)
      local app="${HIDEOUT_BROWSER_APP:-Google Chrome}"
      if ! /usr/bin/open -Ra "$app" >/dev/null 2>&1; then
        echo "gate4: browser app is not registered or launchable: $app; set HIDEOUT_BROWSER_PATH to a direct Chromium-compatible browser binary" >&2
        exit 2
      fi
      ;;
    Linux)
      if ! command -v chromium >/dev/null 2>&1 && ! command -v google-chrome >/dev/null 2>&1; then
        echo "gate4: real browser mode requires HIDEOUT_BROWSER_PATH or chromium/google-chrome on PATH" >&2
        exit 2
      fi
      ;;
  esac
}

if [ "${1:-}" = "--preflight-only" ]; then
  HIDEOUT_GATE4_REAL_BROWSER=1
  preflight_real_browser_launcher
  echo "gate4: real browser launcher preflight passed"
  exit 0
fi

latest_audit() {
  find "$home/.hideout/sessions" -name audit.jsonl -type f -print0 |
    xargs -0 ls -t |
    sed -n '1p'
}

run_hideout_dry() {
  HOME="$home" HIDEOUT_SHIM_PATH="$shim" HIDEOUT_OPEN_DRY_RUN=1 "$hideout" "$@"
}

run_hideout_browser() {
  if [ "${HIDEOUT_GATE4_REAL_BROWSER:-}" = "1" ]; then
    HOME="$home" HIDEOUT_SHIM_PATH="$shim" "$hideout" "$@"
  else
    run_hideout_dry "$@"
  fi
}

expect_open_denied() {
  local name="$1"
  local target="$2"
  local want="$3"
  local out="$tmp/deny-$name.out"
  local err="$tmp/deny-$name.err"

  if run_hideout_dry run --backend native --allow-weak-isolation --workspace "$workspace" -- sh -c 'open "$1"' hideout-open "$target" >"$out" 2>"$err"; then
    echo "gate4: denied open unexpectedly succeeded: $target" >&2
    exit 1
  fi
  grep -q "$want" "$err"
  audit="$(latest_audit)"
  grep -q '"action":"host.open"' "$audit"
  grep -q '"decision":"deny"' "$audit"
}

require_command go
require_command jq
preflight_real_browser_launcher

tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-gate4.XXXXXX")"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT

bin="$tmp/bin"
home="$tmp/home"
workspace="$tmp/workspace"
mkdir -p "$bin" "$home" "$workspace"

hideout="$bin/hideout"
shim="$bin/hideout-shim"
go build -o "$hideout" ./cmd/hideout
go build -o "$shim" ./cmd/hideout-shim

if [ "${HIDEOUT_GATE4_REAL_BROWSER:-}" = "1" ]; then
  echo "gate4: real browser mode enabled for external URL launch only"
else
  echo "gate4: dry-run browser mode; set HIDEOUT_GATE4_REAL_BROWSER=1 for real browser launch"
fi

echo "gate4: verifying external URL host.open route"
run_hideout_browser run --backend native --allow-weak-isolation --workspace "$workspace" -- sh -c 'open https://example.com'
audit="$(latest_audit)"
grep -q '"action":"host.open"' "$audit"
grep -q '"decision":"allow"' "$audit"
grep -q '"resourceType":"url"' "$audit"
grep -q '"browserProfileMode":"isolated"' "$audit"
grep -q '"browserProfile":"present"' "$audit"
grep -q '"portBridge":"none"' "$audit"
grep -q '"browserControl":"disabled"' "$audit"
grep -q '"remoteDebugging":"not-exposed"' "$audit"
grep -q '"subject":"command:open"' "$audit"
grep -q '"route":"host-broker"' "$audit"
test -d "$home/.hideout/profiles/default/browser"

echo "gate4: verifying host-local URL denial"
deny_url_targets=(
  "http://127.0.0.1:3000"
  "http://10.0.0.10"
  "http://100.64.0.10"
  "http://198.18.0.1"
  "http://169.254.1.10"
  "http://224.0.0.1"
  "http://[fc00::1]"
  "http://printer.local"
  "http://app.localhost"
  "http://host.lima.internal:3000"
)
deny_index=0
for target in "${deny_url_targets[@]}"; do
  deny_index=$((deny_index + 1))
  expect_open_denied "local-url-$deny_index" "$target" 'profile policy'
  audit="$(latest_audit)"
  grep -Fq "\"target\":\"$target\"" "$audit"
done

echo "gate4: verifying workspace file open mapping"
printf 'workspace file\n' > "$workspace/doc.txt"
run_hideout_dry run --backend native --allow-weak-isolation --workspace "$workspace" -- sh -c 'open ./doc.txt'
audit="$(latest_audit)"
grep -q '"action":"host.open"' "$audit"
grep -q '"decision":"allow"' "$audit"
grep -q '"resourceType":"workspace-file"' "$audit"
grep -q '"target":".*/doc.txt"' "$audit"
grep -q '"hostPath":".*/doc.txt"' "$audit"

echo "gate4: verifying unmapped file denial"
expect_open_denied "outside-workspace" "/etc/passwd" 'outside workspace'

echo "gate4: verifying unsafe file target denial"
ln -s /etc/passwd "$workspace/link-out"
mkfifo "$workspace/pipe"
expect_open_denied "symlink-escape" "$workspace/link-out" 'resolves outside workspace'
expect_open_denied "special-file" "$workspace/pipe" 'not a regular file or directory'
expect_open_denied "remote-file-url" "file://remote-host$workspace/doc.txt" 'file URL host "remote-host" is denied'
expect_open_denied "query-file-url" "file://localhost$workspace/doc.txt?download=1" 'must not include query or fragment'
expect_open_denied "fragment-file-url" "file://localhost$workspace/doc.txt#fragment" 'must not include query or fragment'
expect_open_denied "encoded-file-url" "file://localhost$workspace/src%2f..%2fsecret.txt" 'encoded path separators'

echo "gate4: verifying disabled command proxy direct shim invocation denial"
HOME="$home" "$hideout" profile init open-only >/dev/null
profile_json="$home/.hideout/profiles/open-only/profile.json"
jq 'del(.commandProxy.commands["xdg-open"])' "$profile_json" > "$tmp/open-only.profile.json"
mv "$tmp/open-only.profile.json" "$profile_json"
if run_hideout_dry run --profile open-only --backend native --allow-weak-isolation --workspace "$workspace" -- sh -c '"$1" xdg-open https://example.com' hideout-shim-test "$shim" >"$tmp/disabled.out" 2>"$tmp/disabled.err"; then
  echo "gate4: disabled xdg-open shim unexpectedly succeeded" >&2
  exit 1
fi
grep -q 'broker request command "xdg-open" is not enabled by profile' "$tmp/disabled.err"
audit="$(latest_audit)"
grep -q '"action":"host.open"' "$audit"
grep -q '"decision":"deny"' "$audit"
grep -q '"command":"xdg-open"' "$audit"

echo "gate4: passed"
