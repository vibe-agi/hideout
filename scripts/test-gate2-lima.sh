#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

GATE_TIMEOUT="${HIDEOUT_GATE_TIMEOUT:-15m}"

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "gate2: missing required command: $1" >&2
    exit 127
  fi
}

with_timeout() {
  local duration="$1"
  shift
  "$@" &
  local pid=$!
  (
    sleep "$duration"
    if kill -0 "$pid" 2>/dev/null; then
      echo "gate2: command timed out after $duration: $*" >&2
      kill "$pid" 2>/dev/null || true
      sleep 5
      kill -KILL "$pid" 2>/dev/null || true
    fi
  ) &
  local timer=$!
  local status=0
  if wait "$pid"; then
    status=0
  else
    status=$?
  fi
  kill "$timer" 2>/dev/null || true
  wait "$timer" 2>/dev/null || true
  return "$status"
}

prepare_linux_shim() {
  if [ -n "${HIDEOUT_LINUX_SHIM_PATH:-}" ]; then
    if [ ! -x "$HIDEOUT_LINUX_SHIM_PATH" ]; then
      echo "gate2: HIDEOUT_LINUX_SHIM_PATH is not executable: $HIDEOUT_LINUX_SHIM_PATH" >&2
      exit 126
    fi
    return
  fi

  local arch
  arch="$(go env GOARCH)"
  if command -v "hideout-shim-linux-$arch" >/dev/null 2>&1; then
    HIDEOUT_LINUX_SHIM_PATH="$(command -v "hideout-shim-linux-$arch")"
    export HIDEOUT_LINUX_SHIM_PATH
    return
  fi
  if command -v hideout-shim-linux >/dev/null 2>&1; then
    HIDEOUT_LINUX_SHIM_PATH="$(command -v hideout-shim-linux)"
    export HIDEOUT_LINUX_SHIM_PATH
    return
  fi

  HIDEOUT_LINUX_SHIM_PATH="$bin/hideout-shim-linux-$arch"
  export HIDEOUT_LINUX_SHIM_PATH
  "$hideout" shim build-linux --out "$HIDEOUT_LINUX_SHIM_PATH" --goarch "$arch" --source "$ROOT" >/dev/null
}

prepare_linux_hostfsd() {
  if [ -n "${HIDEOUT_LINUX_HOSTFSD_PATH:-}" ]; then
    if [ ! -x "$HIDEOUT_LINUX_HOSTFSD_PATH" ]; then
      echo "gate2: HIDEOUT_LINUX_HOSTFSD_PATH is not executable: $HIDEOUT_LINUX_HOSTFSD_PATH" >&2
      exit 126
    fi
    return
  fi

  local arch
  arch="$(go env GOARCH)"
  if command -v "hideout-hostfsd-linux-$arch" >/dev/null 2>&1; then
    HIDEOUT_LINUX_HOSTFSD_PATH="$(command -v "hideout-hostfsd-linux-$arch")"
    export HIDEOUT_LINUX_HOSTFSD_PATH
    return
  fi
  if command -v hideout-hostfsd-linux >/dev/null 2>&1; then
    HIDEOUT_LINUX_HOSTFSD_PATH="$(command -v hideout-hostfsd-linux)"
    export HIDEOUT_LINUX_HOSTFSD_PATH
    return
  fi

  HIDEOUT_LINUX_HOSTFSD_PATH="$bin/hideout-hostfsd-linux-$arch"
  export HIDEOUT_LINUX_HOSTFSD_PATH
  "$hideout" hostfsd build-linux --out "$HIDEOUT_LINUX_HOSTFSD_PATH" --goarch "$arch" --source "$ROOT" >/dev/null
}

prepare_guest_node() {
  if [ "${HIDEOUT_GATE2_REQUIRE_NODE:-}" != "1" ]; then
    return
  fi

  echo "gate2: ensuring node in lima guest"
  if ! with_timeout "$GATE_TIMEOUT" env \
    HIDEOUT_STORE_ROOT="$store" \
    LIMA_HOME="$lima_home" \
    "$hideout" run --backend lima --workspace "$workspace" -- sh -eu -c '
if command -v node >/dev/null 2>&1; then
  node -v
  exit 0
fi
if command -v nodejs >/dev/null 2>&1; then
  nodejs -v
  sudo -n ln -sf "$(command -v nodejs)" /usr/local/bin/node
  exit 0
fi
if command -v apt-get >/dev/null 2>&1; then
  command -v sudo >/dev/null 2>&1 || { echo "sudo is required to install nodejs" >&2; exit 127; }
  sudo -n apt-get update
  sudo -n env DEBIAN_FRONTEND=noninteractive apt-get install -y nodejs
  command -v node >/dev/null 2>&1 || { echo "node command missing after nodejs install" >&2; exit 127; }
  node -v
  exit 0
fi
echo "no supported guest package manager for nodejs" >&2
exit 127
' >"$tmp/node-prepare.out" 2>"$tmp/node-prepare.err"; then
    echo "gate2: node preparation failed" >&2
    echo "gate2: stdout" >&2
    cat "$tmp/node-prepare.out" >&2
    echo "gate2: stderr" >&2
    cat "$tmp/node-prepare.err" >&2
    exit 1
  fi
  cat "$tmp/node-prepare.out"
}

require_command go
require_command limactl

tmp="$(mktemp -d "/tmp/hideout-gate2.XXXXXX")"
cleanup() {
  if [ -x "${hideout:-}" ]; then
    HIDEOUT_STORE_ROOT="${store:-}" LIMA_HOME="${lima_home:-}" "$hideout" clean >/dev/null 2>&1 || true
  fi
  rm -rf "${hostfs_root:-}"
  rm -rf "$tmp"
}
trap cleanup EXIT

bin="$tmp/bin"
store="$tmp/store"
lima_home="$tmp/lima"
workspace="$tmp/workspace"
mkdir -p "$bin" "$store" "$lima_home" "$workspace"

hideout="$bin/hideout"
go build -o "$hideout" ./cmd/hideout
prepare_linux_shim
prepare_linux_hostfsd

printf 'workspace-read\n' > "$workspace/input.txt"

hostfs_root="$(mktemp -d "${TMPDIR:-/tmp}/hideout-gate2-hostfs.XXXXXX")"
hostfs_root="$(cd "$hostfs_root" && pwd -P)"
mkdir -p "$hostfs_root/dir" "$hostfs_root/tree/nested" "$hostfs_root/hidden" "$hostfs_root/glob"
hostfs_file="$hostfs_root/read.txt"
hostfs_dir="$hostfs_root/dir"
hostfs_tree="$hostfs_root/tree"
hostfs_glob_dir="$hostfs_root/glob"
hostfs_ungranted="$hostfs_root/hidden/secret.txt"
hostfs_run_denied="$hostfs_root/denied.txt"
printf 'hostfs-read\n' > "$hostfs_file"
printf 'hostfs-dir\n' > "$hostfs_dir/visible.txt"
printf 'hostfs-tree\n' > "$hostfs_tree/nested/visible.txt"
printf 'hostfs-glob\n' > "$hostfs_glob_dir/visible.txt"
printf 'hostfs-jpg\n' > "$hostfs_glob_dir/hidden.jpg"
printf 'hostfs-hidden\n' > "$hostfs_ungranted"
printf 'hostfs-denied\n' > "$hostfs_run_denied"
GOOS=linux GOARCH="$(go env GOARCH)" CGO_ENABLED=0 \
  go build -trimpath -o "$workspace/hideout-gate-fsread" ./cmd/hideout-gate-fsread

echo "gate2: running doctor"
HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" doctor --backend lima --workspace "$workspace"

echo "gate2: running lima workspace/env/git smoke"
stdout="$tmp/run.out"
stderr="$tmp/run.err"
if ! with_timeout "$GATE_TIMEOUT" env \
  HIDEOUT_STORE_ROOT="$store" \
  LIMA_HOME="$lima_home" \
  HTTP_PROXY="http://user:pass@proxy.invalid:8080" \
  HTTPS_PROXY="http://user:pass@proxy.invalid:8443" \
  ALL_PROXY="socks5://user:pass@proxy.invalid:1080" \
  NO_PROXY="localhost,127.0.0.1" \
  http_proxy="http://user:pass@proxy.invalid:8080" \
  https_proxy="http://user:pass@proxy.invalid:8443" \
  all_proxy="socks5://user:pass@proxy.invalid:1080" \
  no_proxy="localhost,127.0.0.1" \
  GITHUB_TOKEN="gate2-secret" \
  HIDEOUT_ENABLE_LAB=1 \
  HIDEOUT_SECRET_DEFAULT_PROXY="socks5://user:pass@proxy.invalid:1080" \
  "$hideout" run --backend lima --workspace "$workspace" -- sh -eu -c '
actual_pwd=$(pwd)
printf "pwd=%s\n" "$actual_pwd"
printf "read=%s\n" "$(cat input.txt)"
printf "workspace-write\n" > output.txt
printf "home=%s\n" "$HOME"
printf "tmpdir=%s\n" "$TMPDIR"
printf "xdg_config=%s\n" "$XDG_CONFIG_HOME"
printf "xdg_cache=%s\n" "$XDG_CACHE_HOME"
printf "xdg_data=%s\n" "$XDG_DATA_HOME"
printf "git_email=%s\n" "$(git config --global --get user.email)"
printf "child_home=%s\n" "$(sh -c "printf %s \"\$HOME\"")"
for name in HTTP_PROXY HTTPS_PROXY ALL_PROXY NO_PROXY http_proxy https_proxy all_proxy no_proxy GITHUB_TOKEN HIDEOUT_ENABLE_LAB HIDEOUT_SECRET_DEFAULT_PROXY; do
  eval "value=\${$name:-}"
  if [ -n "$value" ]; then
    echo "sensitive env leaked: $name" >&2
    exit 42
  fi
done
child_sensitive_env=$(sh -c '\''printf "%s|%s|%s|%s" "${HTTP_PROXY:-}" "${HTTPS_PROXY:-}" "${GITHUB_TOKEN:-}" "${HIDEOUT_ENABLE_LAB:-}"'\'')
if [ "$child_sensitive_env" != "|||" ]; then
  echo "sensitive env leaked to child: $child_sensitive_env" >&2
  exit 43
fi
printf "sensitive_env_absent=yes\n"
test ! -e "$HOME/.ssh"
' >"$stdout" 2>"$stderr"; then
  echo "gate2: lima workspace/env/git smoke failed" >&2
  echo "gate2: stdout" >&2
  cat "$stdout" >&2
  echo "gate2: stderr" >&2
  cat "$stderr" >&2
  exit 1
fi

cat "$stdout"

grep -q 'read=workspace-read' "$stdout"
grep -q 'git_email=developer@example.com' "$stdout"
grep -q 'home=/hideout/profile/home' "$stdout"
grep -q 'child_home=/hideout/profile/home' "$stdout"
grep -q 'sensitive_env_absent=yes' "$stdout"
grep -q 'Hideout environment: env_' "$stderr"
grep -q 'resume: hideout run --resume env_' "$stderr"
test "$(cat "$workspace/output.txt")" = "workspace-write"

prepare_guest_node

echo "gate2: running hostfs grant smoke"
if ! with_timeout "$GATE_TIMEOUT" env \
  HIDEOUT_STORE_ROOT="$store" \
  LIMA_HOME="$lima_home" \
  "$hideout" run --backend lima --workspace "$workspace" \
    --fs "read:$hostfs_file" \
    --fs "dir:$hostfs_dir" \
    --fs "tree:$hostfs_tree" \
    --fs "read:$hostfs_glob_dir/*.txt" \
    --fs "read:$hostfs_run_denied" \
    --no-fs "read:$hostfs_run_denied" \
    -- sh -eu -c '
printf "hostfs_file=%s\n" "$(cat "$1")"
printf "hostfs_dir=%s\n" "$(cat "$2/visible.txt")"
printf "hostfs_list=%s\n" "$(ls "$2")"
printf "hostfs_tree=%s\n" "$(cat "$3/nested/visible.txt")"
printf "hostfs_glob=%s\n" "$(cat "$4/visible.txt")"
printf "hostfs_glob_list=%s\n" "$(ls "$4")"
if command -v python3 >/dev/null 2>&1; then
  python3 -c "import pathlib, sys; print(\"hostfs_python=\" + pathlib.Path(sys.argv[1]).read_text().strip())" "$1"
else
  echo "python3 missing in guest" >&2
  exit 46
fi
if command -v node >/dev/null 2>&1; then
  node -e "const fs = require(\"fs\"); process.stdout.write(\"hostfs_node=\" + fs.readFileSync(process.argv[1], \"utf8\"))" "$1"
else
  if [ "${HIDEOUT_GATE2_REQUIRE_NODE:-}" = "1" ]; then
    echo "node missing in guest" >&2
    exit 47
  fi
  printf "hostfs_node=skip\n"
fi
./hideout-gate-fsread --read "$1" --deny "$5"
if cat "$5" >/dev/null 2>&1; then
  echo "ungranted hostfs path unexpectedly readable" >&2
  exit 44
fi
if cat "$4/hidden.jpg" >/dev/null 2>&1; then
  echo "non-matching hostfs glob path unexpectedly readable" >&2
  exit 48
fi
if cat "$6" >/dev/null 2>&1; then
  echo "run-denied hostfs path unexpectedly readable" >&2
  exit 45
fi
printf "hostfs_denied=yes\n"
' gate2-hostfs "$hostfs_file" "$hostfs_dir" "$hostfs_tree" "$hostfs_glob_dir" "$hostfs_ungranted" "$hostfs_run_denied" >"$tmp/hostfs.out" 2>"$tmp/hostfs.err"; then
  echo "gate2: hostfs grant smoke failed" >&2
  echo "gate2: stdout" >&2
  cat "$tmp/hostfs.out" >&2
  echo "gate2: stderr" >&2
  cat "$tmp/hostfs.err" >&2
  exit 1
fi
cat "$tmp/hostfs.out"
grep -q 'hostfs_file=hostfs-read' "$tmp/hostfs.out"
grep -q 'hostfs_dir=hostfs-dir' "$tmp/hostfs.out"
grep -q 'hostfs_list=visible.txt' "$tmp/hostfs.out"
grep -q 'hostfs_tree=hostfs-tree' "$tmp/hostfs.out"
grep -q 'hostfs_glob=hostfs-glob' "$tmp/hostfs.out"
grep -q 'hostfs_glob_list=visible.txt' "$tmp/hostfs.out"
grep -q 'hostfs_python=hostfs-read' "$tmp/hostfs.out"
grep -q 'hostfs_go=hostfs-read' "$tmp/hostfs.out"
grep -q 'hostfs_go_denied=yes' "$tmp/hostfs.out"
if [ "${HIDEOUT_GATE2_REQUIRE_NODE:-}" = "1" ]; then
  grep -q 'hostfs_node=hostfs-read' "$tmp/hostfs.out"
else
  grep -Eq 'hostfs_node=(hostfs-read|skip)' "$tmp/hostfs.out"
fi
grep -q 'hostfs_denied=yes' "$tmp/hostfs.out"

echo "gate2: running missing-command no-host-fallback smoke"
if with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run --backend lima --workspace "$workspace" -- hideout-missing-command >"$tmp/missing.out" 2>"$tmp/missing.err"; then
  echo "gate2: missing command unexpectedly succeeded" >&2
  exit 1
fi
if ! grep -q 'command "hideout-missing-command" not found in lima backend' "$tmp/missing.err"; then
  echo "gate2: missing-command stderr did not contain expected backend context" >&2
  cat "$tmp/missing.err" >&2
  exit 1
fi

echo "gate2: running ephemeral identity smoke"
if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run --backend lima --ephemeral --workspace "$workspace" -- sh -eu -c '
identity_root=$(dirname "$HOME")
printf "ephemeral_home=%s\n" "$HOME"
printf "ephemeral_machine=%s\n" "$(cat "$identity_root/machine/machine-id")"
test -f "$HOME/.gitconfig"
' >"$tmp/ephemeral.out" 2>"$tmp/ephemeral.err"; then
  echo "gate2: ephemeral identity smoke failed" >&2
  echo "gate2: stdout" >&2
  cat "$tmp/ephemeral.out" >&2
  echo "gate2: stderr" >&2
  cat "$tmp/ephemeral.err" >&2
  exit 1
fi
cat "$tmp/ephemeral.out"
grep -q 'ephemeral_home=/hideout/profile/home' "$tmp/ephemeral.out"

echo "gate2: running environment resume/new/rm smoke"
HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" list >"$tmp/env-list-before.out"
env_id="$(awk -v ws="$workspace" 'NR > 1 && $2 == "default" && $3 == "lima" && $8 == ws { print $1; exit }' "$tmp/env-list-before.out")"
if [ -z "$env_id" ]; then
  echo "gate2: no reusable lima environment found for workspace" >&2
  cat "$tmp/env-list-before.out" >&2
  exit 1
fi
if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run --backend lima --workspace "$workspace" --resume "$env_id" -- sh -eu -c '
printf "resume_ok=yes\n"
' >"$tmp/env-resume.out" 2>"$tmp/env-resume.err"; then
  echo "gate2: environment resume failed" >&2
  echo "gate2: stdout" >&2
  cat "$tmp/env-resume.out" >&2
  echo "gate2: stderr" >&2
  cat "$tmp/env-resume.err" >&2
  exit 1
fi
grep -q 'resume_ok=yes' "$tmp/env-resume.out"

if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run --backend lima --workspace "$workspace" --new -- sh -eu -c '
printf "new_ok=yes\n"
' >"$tmp/env-new.out" 2>"$tmp/env-new.err"; then
  echo "gate2: environment --new failed" >&2
  echo "gate2: stdout" >&2
  cat "$tmp/env-new.out" >&2
  echo "gate2: stderr" >&2
  cat "$tmp/env-new.err" >&2
  exit 1
fi
grep -q 'new_ok=yes' "$tmp/env-new.out"
HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" list >"$tmp/env-list-after-new.out"
new_env_id="$(awk -v ws="$workspace" -v old="$env_id" 'NR > 1 && $2 == "default" && $3 == "lima" && $8 == ws && $1 != old { print $1; exit }' "$tmp/env-list-after-new.out")"
if [ -z "$new_env_id" ]; then
  echo "gate2: --new did not create a second environment" >&2
  cat "$tmp/env-list-after-new.out" >&2
  exit 1
fi

if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run --backend lima --workspace "$workspace" --resume "$new_env_id" --rm -- sh -eu -c '
printf "rm_ok=yes\n"
' >"$tmp/env-rm.out" 2>"$tmp/env-rm.err"; then
  echo "gate2: environment --resume --rm failed" >&2
  echo "gate2: stdout" >&2
  cat "$tmp/env-rm.out" >&2
  echo "gate2: stderr" >&2
  cat "$tmp/env-rm.err" >&2
  exit 1
fi
grep -q 'rm_ok=yes' "$tmp/env-rm.out"
if grep -q 'resume: hideout run --resume' "$tmp/env-rm.err"; then
  echo "gate2: --rm should not print a reusable environment resume hint" >&2
  cat "$tmp/env-rm.err" >&2
  exit 1
fi
HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" list >"$tmp/env-list-after-rm.out"
if awk -v id="$new_env_id" 'NR > 1 && $1 == id { found = 1 } END { exit found ? 0 : 1 }' "$tmp/env-list-after-rm.out"; then
  echo "gate2: --rm environment is still listed" >&2
  cat "$tmp/env-list-after-rm.out" >&2
  exit 1
fi
grep -q "$env_id" "$tmp/env-list-after-rm.out"

echo "gate2: passed"
