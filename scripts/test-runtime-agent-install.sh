#!/usr/bin/env bash
# Guest-side real package fixture for 031. The host Gate 3 copies this file into
# the mapped workspace and invokes --guest inside the selected runtime.
set -euo pipefail

if [ "${1:-}" != "--guest" ]; then
  echo "usage: test-runtime-agent-install.sh --guest" >&2
  exit 2
fi

package='@openai/codex@0.144.1'
main_integrity='sha512-Xir1zqPfpenhdoAoshN53uonzbBXj18COyzRkFlVZpSNyEl5XtkuYu9oddELePFN7K/0sXUcSO34Ad5IeCXPbw=='
arm_package='@openai/codex@0.144.1-linux-arm64'
arm_integrity='sha512-451o15+XtaXCCb35t/KCyyPqXHnTPxPxtdqEYOnE3e4sH5AfnI/uVJwfdjOksMG6vRLy6R+fLvSDOMguRFLmQw=='
prefix="$HOME/.local"
log="${TMPDIR:-/tmp}/runtime-agent-install.log"

for command in node npm id stat grep find; do
  command -v "$command" >/dev/null 2>&1 || { echo "runtime-agent: missing $command" >&2; exit 127; }
done
uid="$(id -u)"
[ "$uid" -ne 0 ] || { echo "runtime-agent: target must be non-root" >&2; exit 40; }
if command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then
  echo "runtime-agent: target unexpectedly has passwordless sudo" >&2
  exit 41
fi
[ -w "$HOME" ] || { echo "runtime-agent: target home is not writable" >&2; exit 42; }
mkdir -p "$prefix/bin" "$prefix/lib"
[ -w "$prefix" ] || { echo "runtime-agent: durable prefix is not writable" >&2; exit 43; }

rm -rf "$HOME/.npm" "$prefix/lib/node_modules/@openai/codex" "$prefix/bin/codex"
[ ! -e "$HOME/.npm" ] || { echo "runtime-agent: npm cache is not empty" >&2; exit 44; }
[ ! -e "$HOME/.codex/auth.json" ] || { echo "runtime-agent: preauthenticated state is forbidden" >&2; exit 45; }

[ "$(npm view "$package" dist.integrity)" = "$main_integrity" ] || { echo "runtime-agent: main package integrity drift" >&2; exit 46; }
[ "$(npm view "$arm_package" dist.integrity)" = "$arm_integrity" ] || { echo "runtime-agent: arm64 package integrity drift" >&2; exit 47; }
registry="$(npm config get registry)"
[ "$registry" = "https://registry.npmjs.org/" ] || { echo "runtime-agent: unexpected registry $registry" >&2; exit 47; }

npm install --global --prefix "$prefix" "$package" >"$log" 2>&1 || {
  sed -E 's#(https?://)[^/@[:space:]]+@#\1[redacted]@#g' "$log" >&2
  exit 48
}
[ -x "$prefix/bin/codex" ] || { echo "runtime-agent: codex executable missing" >&2; exit 49; }
version="$($prefix/bin/codex --version)"
case "$version" in
  *0.144.1*) ;;
  *) echo "runtime-agent: unexpected codex version: $version" >&2; exit 50 ;;
esac

arm_manifest="$prefix/lib/node_modules/@openai/codex/node_modules/@openai/codex-linux-arm64/package.json"
[ -r "$arm_manifest" ] || { echo "runtime-agent: linux-arm64 optional package missing" >&2; exit 51; }
grep -q '"version"[[:space:]]*:[[:space:]]*"0.144.1-linux-arm64"' "$arm_manifest"
if find "$prefix" -not -user "$uid" -print -quit | grep -q .; then
  echo "runtime-agent: installed files are not target-owned" >&2
  exit 52
fi
[ ! -e "$HOME/.codex/auth.json" ] || { echo "runtime-agent: package install created authentication state" >&2; exit 53; }

if env | grep -E '^(HIDEOUT_SECRET_|HTTP_PROXY=|HTTPS_PROXY=|ALL_PROXY=|OPENAI_API_KEY=)' >/dev/null; then
  echo "runtime-agent: control-plane or host credential reached target environment" >&2
  exit 54
fi
if grep -E 'claim_[0-9a-f]{16,}|cap_[0-9a-f]{16,}|HIDEOUT_SECRET_[A-Z0-9_]+=|socks5h?://[^/@[:space:]]+@' "$log" >/dev/null 2>&1; then
  echo "runtime-agent: install log contains control-plane material" >&2
  exit 55
fi

printf 'runtime_agent_package=%s\n' "$package"
printf 'runtime_agent_version=%s\n' "$version"
printf 'runtime_agent_integrity=passed\n'
printf 'runtime_agent_registry=%s\n' "$registry"
printf 'runtime_agent_arm64_optional=passed\n'
printf 'runtime_agent_target_owner=passed\n'
printf 'runtime_agent_no_sudo=passed\n'
printf 'runtime_agent_no_auth=passed\n'
printf 'runtime_agent_secret_scan=passed\n'
