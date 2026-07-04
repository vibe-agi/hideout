#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

GATE_TIMEOUT="${HIDEOUT_GATE_TIMEOUT:-15m}"
OPERATOR_ENV_FLAGS=()

usage() {
  cat <<'USAGE'
Usage:
  HIDEOUT_OPERATOR_NPM_PACKAGE=<npm-spec> \
  HIDEOUT_OPERATOR_COMMAND=<command> \
    scripts/test-operator-cli-smoke.sh

Optional:
  HIDEOUT_OPERATOR_PROFILE=default
  HIDEOUT_OPERATOR_WORKSPACE=<path>
  HIDEOUT_OPERATOR_STORE=<path>
  HIDEOUT_OPERATOR_LIMA_HOME=<path>
  HIDEOUT_OPERATOR_VERSION_ARGS='--version'
  HIDEOUT_OPERATOR_AUTH_COMMAND='<guest shell command>'
  HIDEOUT_OPERATOR_STATUS_COMMAND='<guest shell command>'
  HIDEOUT_OPERATOR_REQUEST_COMMAND='<guest shell command>'
  HIDEOUT_OPERATOR_ENV_KEYS='KEY1,KEY2'

This is an operator-supplied smoke for any real CLI. Hideout does not encode
the CLI's product semantics; the operator supplies package, command, env, and
auth/request behavior.
USAGE
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "operator-cli: missing required command: $1" >&2
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
      echo "operator-cli: command timed out after $duration: $*" >&2
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
      echo "operator-cli: HIDEOUT_LINUX_SHIM_PATH is not executable: $HIDEOUT_LINUX_SHIM_PATH" >&2
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
      echo "operator-cli: HIDEOUT_LINUX_HOSTFSD_PATH is not executable: $HIDEOUT_LINUX_HOSTFSD_PATH" >&2
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

operator_env_flags() {
  local keys="${HIDEOUT_OPERATOR_ENV_KEYS:-}"
  OPERATOR_ENV_FLAGS=()
  if [ -z "$keys" ]; then
    return
  fi
  local old_ifs="$IFS"
  IFS=,
  read -r -a parsed_keys <<<"$keys"
  IFS="$old_ifs"
  local key value
  for key in "${parsed_keys[@]}"; do
    key="${key#"${key%%[![:space:]]*}"}"
    key="${key%"${key##*[![:space:]]}"}"
    if [ -z "$key" ]; then
      continue
    fi
    value="${!key:-}"
    if [ -z "$value" ]; then
      echo "operator-cli: requested env key $key is not set in host env" >&2
      exit 2
    fi
    OPERATOR_ENV_FLAGS+=(--env "$key=$value")
  done
}

extract_environment_id() {
  sed -n 's/^Hideout environment: \(env_[A-Za-z0-9_]*\)$/\1/p' "$1" | tail -n 1
}

run_hideout() {
  local label="$1"
  local stdout="$2"
  local stderr="$3"
  shift 3
  if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" "$@" >"$stdout" 2>"$stderr"; then
    echo "operator-cli: $label failed" >&2
    echo "operator-cli: stdout" >&2
    cat "$stdout" >&2
    echo "operator-cli: stderr" >&2
    cat "$stderr" >&2
    exit 1
  fi
}

run_operator_guest() {
  local label="$1"
  local stdout="$2"
  local stderr="$3"
  local new_env="$4"
  shift 4
  local run_args=(run --backend lima --profile "$profile_name" --workspace "$workspace")
  if [ "$new_env" = "1" ]; then
    run_args+=(--new)
  fi
  if [ "${#OPERATOR_ENV_FLAGS[@]}" -gt 0 ]; then
    run_args+=("${OPERATOR_ENV_FLAGS[@]}")
  fi
  run_args+=(-- "$@")
  run_hideout "$label" "$stdout" "$stderr" "${run_args[@]}"
}

run_operator_guest_interactive() {
  local label="$1"
  shift
  local run_args=(run --backend lima --profile "$profile_name" --workspace "$workspace")
  if [ "${#OPERATOR_ENV_FLAGS[@]}" -gt 0 ]; then
    run_args+=("${OPERATOR_ENV_FLAGS[@]}")
  fi
  run_args+=(-- "$@")
  echo "operator-cli: $label"
  if ! env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" "${run_args[@]}"; then
    echo "operator-cli: $label failed" >&2
    exit 1
  fi
}

if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
  usage
  exit 0
fi

package="${HIDEOUT_OPERATOR_NPM_PACKAGE:-}"
command_name="${HIDEOUT_OPERATOR_COMMAND:-}"
if [ -z "$package" ] || [ -z "$command_name" ]; then
  usage >&2
  exit 2
fi

require_command go
require_command limactl

tmp="$(mktemp -d "/tmp/ho-operator-cli.XXXXXX")"
cleanup_store=0
cleanup_lima=0
cleanup() {
  if [ "$cleanup_store" -eq 1 ] && [ -x "${hideout:-}" ]; then
    HIDEOUT_STORE_ROOT="${store:-}" LIMA_HOME="${lima_home:-}" "$hideout" clean >/dev/null 2>&1 || true
  fi
  if [ "$cleanup_lima" -eq 1 ]; then
    rm -rf "${lima_home:-}"
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT

bin="$tmp/bin"
workspace="${HIDEOUT_OPERATOR_WORKSPACE:-$tmp/workspace}"
store="${HIDEOUT_OPERATOR_STORE:-$tmp/store}"
lima_home="${HIDEOUT_OPERATOR_LIMA_HOME:-$tmp/lima}"
profile_name="${HIDEOUT_OPERATOR_PROFILE:-default}"
version_args="${HIDEOUT_OPERATOR_VERSION_ARGS:---version}"
mkdir -p "$bin" "$workspace" "$store" "$lima_home"

if [ -z "${HIDEOUT_OPERATOR_STORE:-}" ]; then
  cleanup_store=1
fi
if [ -z "${HIDEOUT_OPERATOR_LIMA_HOME:-}" ]; then
  cleanup_lima=1
fi

hideout="$bin/hideout"
go build -o "$hideout" ./cmd/hideout
prepare_linux_shim
prepare_linux_hostfsd
operator_env_flags

echo "operator-cli: initializing profile"
HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" init --no-input --backend lima --network direct >/dev/null
HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" profile tools "$profile_name" preset add node-dev >/dev/null
HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" profile tools "$profile_name" npm add --package "$package" --command "$command_name" >/dev/null

echo "operator-cli: first run creates a tool-matched Lima environment"
run_operator_guest "first version run" "$tmp/version1.out" "$tmp/version1.err" 1 \
  sh -lc "$command_name $version_args"
cat "$tmp/version1.out"
env1="$(extract_environment_id "$tmp/version1.err")"
if [ -z "$env1" ]; then
  echo "operator-cli: first run did not print an environment id" >&2
  cat "$tmp/version1.err" >&2
  exit 1
fi

echo "operator-cli: second run reuses the same Lima environment"
run_operator_guest "second version run" "$tmp/version2.out" "$tmp/version2.err" 0 \
  sh -lc "$command_name $version_args"
cat "$tmp/version2.out"
env2="$(extract_environment_id "$tmp/version2.err")"
if [ "$env2" != "$env1" ]; then
  echo "operator-cli: environment was not reused: first=$env1 second=$env2" >&2
  exit 1
fi

if [ -n "${HIDEOUT_OPERATOR_AUTH_COMMAND:-}" ]; then
  run_operator_guest_interactive "running operator-supplied auth command" \
    sh -lc "$HIDEOUT_OPERATOR_AUTH_COMMAND"
fi

if [ -n "${HIDEOUT_OPERATOR_STATUS_COMMAND:-}" ]; then
  echo "operator-cli: running operator-supplied status command"
  run_operator_guest "status run" "$tmp/status.out" "$tmp/status.err" 0 \
    sh -lc "$HIDEOUT_OPERATOR_STATUS_COMMAND"
  cat "$tmp/status.out"
  env_status="$(extract_environment_id "$tmp/status.err")"
  if [ "$env_status" != "$env1" ]; then
    echo "operator-cli: status run did not reuse environment: first=$env1 status=$env_status" >&2
    exit 1
  fi
fi

if [ -n "${HIDEOUT_OPERATOR_REQUEST_COMMAND:-}" ]; then
  echo "operator-cli: running operator-supplied request command"
  run_operator_guest "request run" "$tmp/request.out" "$tmp/request.err" 0 \
    sh -lc "$HIDEOUT_OPERATOR_REQUEST_COMMAND"
  cat "$tmp/request.out"
  env3="$(extract_environment_id "$tmp/request.err")"
  if [ "$env3" != "$env1" ]; then
    echo "operator-cli: request run did not reuse environment: first=$env1 request=$env3" >&2
    exit 1
  fi
fi

echo "operator-cli: verifying Hideout store cannot be granted through HostFS"
if HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run --backend lima --profile "$profile_name" --workspace "$workspace" --fs "tree:$store" -- sh -c true >"$tmp/store-grant.out" 2>"$tmp/store-grant.err"; then
  echo "operator-cli: store HostFS grant unexpectedly succeeded" >&2
  exit 1
fi
if ! grep -q 'Hideout control-plane store' "$tmp/store-grant.err"; then
  echo "operator-cli: store HostFS grant failed without reserved-store evidence" >&2
  cat "$tmp/store-grant.err" >&2
  exit 1
fi

echo "operator-cli: passed environment=$env1"
