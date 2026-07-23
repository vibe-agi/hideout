#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
. "$ROOT/scripts/lib/gate-result.sh"
. "$ROOT/scripts/lib/daemon-temp.sh"
. "$ROOT/scripts/lib/lima-temp.sh"

GATE_TIMEOUT="${HIDEOUT_GATE_TIMEOUT:-15m}"
GATE2_RUNTIME_MODE="${HIDEOUT_GATE2_RUNTIME_MODE:-0}"
GATE2_RUNTIME_FAMILY="${HIDEOUT_GATE2_RUNTIME_FAMILY:-developer-standard}"

case "$GATE2_RUNTIME_MODE" in
  0 | 1) ;;
  *)
    echo "gate2: HIDEOUT_GATE2_RUNTIME_MODE must be 0 or 1" >&2
    exit 2
    ;;
esac
if [ "$GATE2_RUNTIME_MODE" = "1" ]; then
  # Runtime acceptance proves the selected image already carries Node. The
  # legacy optional helper must not turn a missing image contract into
  # first-boot provisioning.
  HIDEOUT_GATE2_REQUIRE_NODE=1
  export HIDEOUT_GATE2_REQUIRE_NODE
fi

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

wait_for_file() {
  local path="$1"
  local description="$2"
  local attempts="${3:-180}"
  local i
  for i in $(seq 1 "$attempts"); do
    if [ -f "$path" ]; then
      return 0
    fi
    sleep 1
  done
  echo "gate2: timed out waiting for $description: $path" >&2
  return 1
}

snapshot_disposable_inventory() {
  local label="$1"
  HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" env list >"$tmp/$label-env-list.out"
  awk -F'\t' 'NR > 1 { print $1 "\t" $15 }' "$tmp/$label-env-list.out" |
    sort >"$tmp/$label-environments.txt"
  LIMA_HOME="$lima_home" limactl list 2>/dev/null |
    awk 'NR > 1 { print $1 }' |
    sort >"$tmp/$label-lima-instances.txt"
  if [ -d "$store/lifecycle" ]; then
    for lifecycle_dir in "$store/lifecycle"/*; do
      [ -d "$lifecycle_dir" ] || continue
      basename "$lifecycle_dir"
    done | sort >"$tmp/$label-lifecycle-identities.txt"
  else
    : >"$tmp/$label-lifecycle-identities.txt"
  fi
}

assert_disposable_inventory_unchanged() {
  local before="$1"
  local after="$2"
  local lane="$3"
  local kind
  for kind in environments lima-instances lifecycle-identities; do
    if ! cmp -s "$tmp/$before-$kind.txt" "$tmp/$after-$kind.txt"; then
      echo "gate2: $lane changed $kind inventory" >&2
      diff "$tmp/$before-$kind.txt" "$tmp/$after-$kind.txt" >&2 || true
      return 1
    fi
  done
  if awk -F'\t' 'NR > 1 && $1 ~ /^rm-/ { found = 1 } END { exit found ? 0 : 1 }' "$tmp/$after-env-list.out"; then
    echo "gate2: $lane retained a disposable environment record" >&2
    cat "$tmp/$after-env-list.out" >&2
    return 1
  fi
}

disposal_audit_count() {
  local decision="$1"
  local disposition="$2"
  local audit="$store/logs/environment-audit.jsonl"
  if [ ! -f "$audit" ]; then
    printf '0\n'
    return
  fi
  jq -s --arg decision "$decision" --arg disposition "$disposition" '
    [.[] | select(.action == "env.dispose" and .decision == $decision and
      .details.disposition == $disposition)] | length
  ' "$audit"
}

assert_removed_audit_increment() {
  local before="$1"
  local lane="$2"
  local after
  after="$(disposal_audit_count allow removed)"
  if [ "$after" -ne $((before + 1)) ]; then
    echo "gate2: $lane did not add exactly one audited removed disposition (before=$before after=$after)" >&2
    return 1
  fi
}

wait_for_hostfs_read_decision() {
  local path="$1"
  local output="$2"
  local i
  for i in $(seq 1 180); do
    if HIDEOUT_STORE_ROOT="$store" "$hideout" decision list --kind hostfs.read --include-terminal >"$output" 2>"$output.err"; then
      if HOSTFS_READ_PATH="$path" jq -e 'any(.[]; .proposedAction.path == env.HOSTFS_READ_PATH)' "$output" >/dev/null; then
        return 0
      fi
    fi
    sleep 1
  done
  echo "gate2: timed out waiting for hostfs.read decision for $path" >&2
  cat "$output" "$output.err" >&2 2>/dev/null || true
  return 1
}

hostfs_read_decision_id() {
  local path="$1"
  local input="$2"
  HOSTFS_READ_PATH="$path" jq -r '[.[] | select(.proposedAction.path == env.HOSTFS_READ_PATH)] | sort_by(.createdAt) | last | .id // empty' "$input"
}

wait_for_projection_decision() {
  local profile_name="$1"
  local output="$2"
  local i
  for i in $(seq 1 180); do
    if HIDEOUT_STORE_ROOT="$store" "$hideout" decision list \
      --kind host-app.open-resource --profile "$profile_name" --include-terminal >"$output" 2>"$output.err"; then
      if jq -e 'any(.[]; .state == "pending" or .state == "claimed")' "$output" >/dev/null; then
        return 0
      fi
    fi
    sleep 1
  done
  echo "gate2: timed out waiting for trusted host-app decision" >&2
  cat "$output" "$output.err" >&2 2>/dev/null || true
  return 1
}

projection_vscode_bundle() {
  for candidate in \
    "/Applications/Visual Studio Code.app" \
    "$HOME/Applications/Visual Studio Code.app"; do
    if [ -d "$candidate" ]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

. "$ROOT/scripts/lib/gate2-projection.sh"

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
  HIDEOUT_LINUX_HOSTFSD_PATH="$bin/hideout-hostfsd-linux-$arch"
  export HIDEOUT_LINUX_HOSTFSD_PATH
  "$hideout" hostfsd build-linux --out "$HIDEOUT_LINUX_HOSTFSD_PATH" --goarch "$arch" --source "$ROOT" >/dev/null
}

prepare_guest_node() {
  if [ "$GATE2_RUNTIME_MODE" = "1" ]; then
    echo "gate2: prepare_guest_node is forbidden in runtime acceptance mode" >&2
    exit 49
  fi
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

# The store lives below this root and owns two Unix sockets. macOS's default
# TMPDIR is long enough to exceed sockaddr_un once the daemon filenames are
# appended, so use the repository's short daemon-safe temporary root.
tmp="$(hideout_mktemp_daemon_store)"
named_guard_pid=""
visibility_run_pid=""
projection_run_pid=""
projection_proxy_pid=""
projection_safe_data_dir=""
projection_workspace=""
projection_control_workspace=""
projection_trusted_workspace=""
projection_external_workspace=""
cleanup_lima_instances() {
  if [ -z "${lima_home:-}" ] || [ ! -d "$lima_home" ]; then
    return
  fi

  while IFS= read -r instance; do
    if [ -z "$instance" ]; then
      continue
    fi
    LIMA_HOME="$lima_home" limactl delete --force --tty=false "$instance" >/dev/null 2>&1 || true
  done < <(LIMA_HOME="$lima_home" limactl list --quiet 2>/dev/null || true)
}
cleanup() {
	if [ -n "${projection_proxy_pid:-}" ] && kill -0 "$projection_proxy_pid" 2>/dev/null; then
		kill "$projection_proxy_pid" 2>/dev/null || true
		wait "$projection_proxy_pid" 2>/dev/null || true
	fi
	if [ -n "${projection_run_pid:-}" ] && kill -0 "$projection_run_pid" 2>/dev/null; then
		kill "$projection_run_pid" 2>/dev/null || true
		wait "$projection_run_pid" 2>/dev/null || true
	fi
	if [ -n "${projection_safe_data_dir:-}" ]; then
		projection_stop_safe_app
	fi
  if [ -n "${visibility_run_pid:-}" ] && kill -0 "$visibility_run_pid" 2>/dev/null; then
    kill "$visibility_run_pid" 2>/dev/null || true
    wait "$visibility_run_pid" 2>/dev/null || true
  fi
  if [ -n "${named_guard_pid:-}" ] && kill -0 "$named_guard_pid" 2>/dev/null; then
    kill "$named_guard_pid" 2>/dev/null || true
    wait "$named_guard_pid" 2>/dev/null || true
  fi
	if [ -n "${hostfs_protected_dir:-}" ]; then
		chmod 700 "$hostfs_protected_dir" 2>/dev/null || true
	fi
	if [ "${HIDEOUT_GATE2_KEEP_TMP:-0}" = "1" ]; then
		echo "gate2: retained diagnostic directory: $tmp" >&2
		echo "gate2: retained diagnostic Lima home: ${lima_home:-unset}" >&2
	else
		if [ -x "${hideout:-}" ]; then
			HIDEOUT_STORE_ROOT="${store:-}" LIMA_HOME="${lima_home:-}" "$hideout" clean >/dev/null 2>&1 || true
		fi
		cleanup_lima_instances
		rm -rf "${hostfs_root:-}" "${hostfs_visibility_root:-}"
		rm -rf "${projection_workspace:-}" "${projection_control_workspace:-}" "${projection_trusted_workspace:-}" "${projection_external_workspace:-}"
		rm -rf "$tmp"
		rm -rf "${lima_home:-}"
	fi
}
trap cleanup EXIT

bin="$tmp/bin"
store="$tmp/store"
lima_home="$(hideout_mktemp_lima_home)"
# Every hideout invocation in this gate (including sourced lane libraries)
# must resolve the same lima world: the daemon inherits its lima home from
# whichever command starts it, and clients now fail closed on a lima-home
# mismatch. Export once instead of trusting per-call prefixes.
export LIMA_HOME="$lima_home"
workspace="$tmp/workspace"
mkdir -p "$bin" "$store" "$workspace"
# The daemon requires a private store root (lifecycle journal inventory,
# internal/lifecycle/journal.go requirePrivateDir). mkdir leaves group/other
# bits, so make the store 0700 to match how real `hideout setup` creates it.
chmod 0700 "$store"

hideout="$bin/hideout"
if [ -n "${HIDEOUT_RELEASE_BINARY:-}" ]; then
  [ -x "$HIDEOUT_RELEASE_BINARY" ] || { echo "gate2: HIDEOUT_RELEASE_BINARY is not executable" >&2; exit 126; }
  cp "$HIDEOUT_RELEASE_BINARY" "$hideout"
  chmod 0700 "$hideout"
else
  go build -o "$hideout" ./cmd/hideout
fi
prepare_linux_shim
prepare_linux_hostfsd

if [ "$GATE2_RUNTIME_MODE" = "1" ]; then
  echo "gate2: selecting immutable runtime $GATE2_RUNTIME_FAMILY"
  HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" init \
    --profile default --template dev --backend lima --network direct \
    --runtime "$GATE2_RUNTIME_FAMILY" --no-input >"$tmp/runtime-init.out"
  grep -q "$GATE2_RUNTIME_FAMILY" "$store/profiles/default/profile.json"
fi

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
hostfs_write_file="$hostfs_root/write.txt"
hostfs_write_dir="$hostfs_root/write-created"
printf 'hostfs-read\n' > "$hostfs_file"
printf 'hostfs-dir\n' > "$hostfs_dir/visible.txt"
printf 'hostfs-tree\n' > "$hostfs_tree/nested/visible.txt"
printf 'hostfs-glob\n' > "$hostfs_glob_dir/visible.txt"
printf 'hostfs-jpg\n' > "$hostfs_glob_dir/hidden.jpg"
printf 'hostfs-hidden\n' > "$hostfs_ungranted"
printf 'hostfs-denied\n' > "$hostfs_run_denied"
printf 'hostfs-before\n' > "$hostfs_write_file"

hostfs_visibility_root="$(mktemp -d "${TMPDIR:-/tmp}/hideout-gate2-hostfs-visibility.XXXXXX")"
hostfs_visibility_root="$(cd "$hostfs_visibility_root" && pwd -P)"
hostfs_list_root="$hostfs_visibility_root/list"
hostfs_tree_root="$hostfs_visibility_root/tree"
hostfs_exact_dir="$hostfs_visibility_root/exact-dir"
hostfs_overflow_dir="$hostfs_visibility_root/overflow"
hostfs_protected_dir="$hostfs_visibility_root/protected"
hostfs_outside_root="$hostfs_visibility_root/outside"
hostfs_live_root="$hostfs_visibility_root/live"
hostfs_discover_denied_dir="$hostfs_list_root/explicit-hidden"
mkdir -p \
	"$hostfs_list_root/.ssh" \
	"$hostfs_discover_denied_dir" \
  "$hostfs_list_root/subdir" \
  "$hostfs_tree_root/nested" \
  "$hostfs_exact_dir" \
  "$hostfs_overflow_dir" \
  "$hostfs_protected_dir" \
  "$hostfs_outside_root" \
  "$hostfs_live_root"
hostfs_locked_file="$hostfs_list_root/locked.txt"
hostfs_read_denied_file="$hostfs_list_root/read-denied.txt"
hostfs_explicit_write_file="$hostfs_list_root/write-denied.txt"
hostfs_hidden_file="$hostfs_list_root/.ssh/id_test"
hostfs_discover_denied_readable="$hostfs_discover_denied_dir/readable.txt"
hostfs_outside_file="$hostfs_outside_root/outside.txt"
hostfs_legacy_file="$hostfs_visibility_root/legacy.txt"
hostfs_protected_file="$hostfs_protected_dir/unavailable.txt"
printf 'visibility-locked-content\n' >"$hostfs_locked_file"
printf 'visibility-read-denied\n' >"$hostfs_read_denied_file"
printf 'visibility-write-denied\n' >"$hostfs_explicit_write_file"
printf 'visibility-hidden\n' >"$hostfs_hidden_file"
printf 'visibility-explicit-read\n' >"$hostfs_discover_denied_readable"
printf 'visibility-outside\n' >"$hostfs_outside_file"
printf 'visibility-legacy\n' >"$hostfs_legacy_file"
printf 'visibility-list-child\n' >"$hostfs_list_root/visible.txt"
printf 'visibility-subdir-child\n' >"$hostfs_list_root/subdir/child.txt"
printf 'visibility-tree-child\n' >"$hostfs_tree_root/nested/child.txt"
printf 'visibility-exact-dir-child\n' >"$hostfs_exact_dir/child.txt"
printf 'visibility-protected\n' >"$hostfs_protected_file"
chmod 0640 "$hostfs_locked_file"
for i in $(seq 1 4097); do
  : >"$hostfs_overflow_dir/entry-$i"
done
chmod 000 "$hostfs_protected_dir"

hostfs_live_approve="$hostfs_live_root/approve.txt"
hostfs_live_deny="$hostfs_live_root/deny.txt"
hostfs_live_timeout="$hostfs_live_root/timeout.txt"
hostfs_live_target_a="$hostfs_live_root/target-a.txt"
hostfs_live_target_b="$hostfs_live_root/target-b.txt"
hostfs_live_link="$hostfs_live_root/link.txt"
printf 'approved-live-content-029\n' >"$hostfs_live_approve"
printf 'deny-live-content-029\n' >"$hostfs_live_deny"
printf 'timeout-live-content-029\n' >"$hostfs_live_timeout"
printf 'symlink-target-a-029\n' >"$hostfs_live_target_a"
printf 'symlink-target-b-029\n' >"$hostfs_live_target_b"
chmod 0640 "$hostfs_live_approve"
ln -s "$hostfs_live_target_a" "$hostfs_live_link"
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
  SERVICE_TOKEN="gate2-secret" \
  HIDEOUT_ENABLE_LAB=1 \
  HIDEOUT_SECRET_DEFAULT_PROXY="socks5://user:pass@proxy.invalid:1080" \
  "$hideout" run --verbose --backend lima --workspace "$workspace" -- sh -eu -c '
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
for name in HTTP_PROXY HTTPS_PROXY ALL_PROXY NO_PROXY http_proxy https_proxy all_proxy no_proxy SERVICE_TOKEN HIDEOUT_ENABLE_LAB HIDEOUT_SECRET_DEFAULT_PROXY; do
  eval "value=\${$name:-}"
  if [ -n "$value" ]; then
    echo "sensitive env leaked: $name" >&2
    exit 42
  fi
done
child_sensitive_env=$(sh -c '\''printf "%s|%s|%s|%s" "${HTTP_PROXY:-}" "${HTTPS_PROXY:-}" "${SERVICE_TOKEN:-}" "${HIDEOUT_ENABLE_LAB:-}"'\'')
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
grep -q 'Hideout environment name: ' "$stderr"
grep -q 'run again: hideout run --env ' "$stderr"
test "$(cat "$workspace/output.txt")" = "workspace-write"

# Surface the run's environment name and Boundary Summary (from --verbose, on
# either stream) so the evidence orchestrator records real references, matching
# the isolation-evidence contract (env name applicable + Boundary Summary ref).
grep -h 'Hideout environment name:' "$stdout" "$stderr" 2>/dev/null | head -n1 || true
grep -qh 'Hideout boundary:' "$stdout" "$stderr" 2>/dev/null && echo "Boundary Summary present" || true

if [ "$GATE2_RUNTIME_MODE" = "1" ]; then
  # Starting a Lima guest always runs Hideout's Go-owned system bootstrap
  # (identity, sudo policy, machine identity, and control-plane plumbing).
  # Runtime acceptance forbids only the legacy package/tool provisioning path.
  echo "runtime_hideout_system_bootstrap=required-and-run"
  echo "runtime_package_tool_provisioning=not-run"
else
  prepare_guest_node
fi

if [ "$GATE2_RUNTIME_MODE" = "1" ]; then
  runtime_env_name="$(grep -h 'Hideout environment name:' "$stdout" "$stderr" 2>/dev/null | tail -n1 | sed 's/^.*Hideout environment name: //')"
  if [ -z "$runtime_env_name" ]; then
    echo "gate2: runtime mode could not resolve the managed environment name" >&2
    exit 49
  fi
  if ! with_timeout "$GATE_TIMEOUT" env \
    HIDEOUT_STORE_ROOT="$store" \
    LIMA_HOME="$lima_home" \
    "$hideout" runtime verify --env "$runtime_env_name" --json \
      >"$tmp/runtime-verify.json" 2>"$tmp/runtime-verify.err"; then
    echo "gate2: explicit runtime verification failed" >&2
    cat "$tmp/runtime-verify.err" >&2
    exit 49
  fi
  runtime_environment_id="$(jq -r '.environmentId // empty' "$tmp/runtime-verify.json")"
  runtime_receipt=""
  if [ -n "$runtime_environment_id" ]; then
    runtime_receipt="$store/environments/$runtime_environment_id/runtime-verification.json"
  fi
  if ! jq -e '.status.status == "preview-ready"' "$tmp/runtime-verify.json" >/dev/null; then
    echo "gate2: runtime verification did not reach preview-ready" >&2
    cat "$tmp/runtime-verify.json" >&2
    if [ -n "$runtime_receipt" ] && [ -f "$runtime_receipt" ]; then
      echo "gate2: runtime verification receipt" >&2
      cat "$runtime_receipt" >&2
    fi
    exit 49
  fi
  if [ -z "$runtime_environment_id" ]; then
    echo "gate2: explicit runtime verification returned no environment identity" >&2
    exit 49
  fi
  if [ ! -f "$runtime_receipt" ]; then
    echo "gate2: runtime mode produced no verification receipt" >&2
    exit 49
  fi
  jq -e \
    --arg family "$GATE2_RUNTIME_FAMILY" \
    '.schema == "hideout.runtime-verification/v1" and
     .provenance.family == $family and
     .backend == "lima" and .backendReal == true and .running == true and
     .privilegeStatus == "enforced" and .status == "preview-ready" and
     (.results | length > 0) and all(.results[]; .present and .matched)' \
    "$runtime_receipt" >/dev/null
  runtime_evidence_markers "$runtime_receipt"
  echo "runtime_contract=passed"
fi

run_projection_gate2
if [ -n "${HIDEOUT_PROJECTION_READINESS_CAPTURE_DIR:-}" ]; then
  projection_capture="$HIDEOUT_PROJECTION_READINESS_CAPTURE_DIR"
  test -f "$projection_capture/readiness-samples.tsv"
  test -f "$projection_capture/runtime-binding.json"
  awk -F '\t' \
    -v fresh="${HIDEOUT_PROJECTION_READINESS_FRESH:-10}" \
    -v warm="${HIDEOUT_PROJECTION_READINESS_WARM:-30}" '
    NR == 1 {
      expected = "lane\tindex\tduration_ms\tfirst_target\toperator_retries\ttarget_retries\tfallbacks\ttimeouts\tunauthorized_host_effects\tcross_session_access"
      if ($0 != expected) exit 1
      next
    }
    $1 == "fresh" { fresh_count++; if ($2 != fresh_count || $4 != "projected") exit 1 }
    $1 == "warm" { warm_count++; if ($2 != warm_count || $4 != "projected") exit 1 }
    $1 == "cancellation" { cancel_count++; if ($2 != 1 || $4 != "none") exit 1 }
    {
      for (i = 5; i <= 10; i++) if ($i != 0) exit 1
    }
    END {
      if (fresh_count != fresh || warm_count != warm || cancel_count != 1) exit 1
    }
  ' "$projection_capture/readiness-samples.tsv"
  jq -e '
    .schema == "hideout.runtime-evidence-binding/v1" and
    .hostOS == "darwin" and .hostArch == "arm64" and .guestArch == "aarch64" and
    .buildDirty == false and
    (.artifactSHA256 | test("^[a-f0-9]{64}$")) and
    (.buildCommit | test("^[a-f0-9]{12,40}$"))
  ' "$projection_capture/runtime-binding.json" >/dev/null
  echo "projection_readiness_capture_contract=passed"
fi

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
# This check exercises HostFS read from a compiled Go program (os.ReadFile),
# not shared Workspace Portal execution. This legacy Gate 2 topology uses a
# static Lima virtiofs workspace, whose direct execution remains explicitly
# unpromoted by feature 041, so copy the helper to an exec-capable guest path.
cp ./hideout-gate-fsread /tmp/hideout-gate-fsread
/tmp/hideout-gate-fsread --read "$1" --deny "$5"
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

cat >"$workspace/hostfs-visibility-namespace.py" <<'PY'
import errno
import os
import stat
import sys
import time

(
    outside_path,
    hidden_path,
    list_root,
    tree_root,
    exact_dir,
    locked_file,
    read_denied_file,
    overflow_dir,
    explicit_write_file,
	legacy_file,
	protected_dir,
	discover_denied_readable,
) = sys.argv[1:]


def expect_errno(label, expected, action):
    try:
        action()
    except OSError as exc:
        if exc.errno != expected:
            raise AssertionError(f"{label}: errno={exc.errno}, want {expected}") from exc
        print(f"{label}=errno-{exc.errno}")
        return
    raise AssertionError(f"{label}: operation unexpectedly succeeded")


expect_errno("outside_domain", errno.ENOENT, lambda: os.stat(outside_path))
expect_errno("force_hidden", errno.ENOENT, lambda: os.stat(hidden_path))
expect_errno("force_hidden_list", errno.ENOENT, lambda: os.listdir(os.path.dirname(hidden_path)))

expected = {"locked.txt", "read-denied.txt", "subdir", "visible.txt", "write-denied.txt"}
names = set(os.listdir(list_root))
if names != expected:
    raise AssertionError(f"see-dir names={sorted(names)}, want {sorted(expected)}")
for name in names:
    info = os.lstat(os.path.join(list_root, name))
    if info.st_size != 0:
        raise AssertionError(f"coarse entry {name} leaked size {info.st_size}")
    expected_mode = 0o777 if stat.S_ISDIR(info.st_mode) else 0o666
    if stat.S_IMODE(info.st_mode) != expected_mode:
        raise AssertionError(f"coarse entry {name} mode={oct(stat.S_IMODE(info.st_mode))}, want {oct(expected_mode)}")
if os.listdir(tree_root) != ["nested"] or os.listdir(os.path.join(tree_root, "nested")) != ["child.txt"]:
    raise AssertionError("see-tree did not expose the complete recursive fixture")
print("coarse_complete=yes")

if open(discover_denied_readable, "r", encoding="utf-8").read() != "visibility-explicit-read\n":
    raise AssertionError("discover deny revoked separately granted exact content")
print("discover_denied_exact_read=yes")

if not stat.S_ISDIR(os.stat(exact_dir).st_mode):
    raise AssertionError("exact-visible directory lookup did not succeed")
expect_errno("exact_dir_readdir", errno.EACCES, lambda: os.listdir(exact_dir))

locked = os.stat(locked_file)
if locked.st_size != 0:
    raise AssertionError(f"locked stat leaked size {locked.st_size}")
started = time.monotonic()
expect_errno("locked_read", errno.EACCES, lambda: open(locked_file, "rb").read())
if time.monotonic() - started >= 2:
    raise AssertionError("locked read waited instead of returning prompt EACCES")
expect_errno("explicit_read_deny", errno.EACCES, lambda: open(read_denied_file, "rb").read())
expect_errno("overflow", errno.EOVERFLOW, lambda: os.listdir(overflow_dir))


def write_one(path):
    fd = os.open(path, os.O_WRONLY)
    try:
        os.write(fd, b"x")
    finally:
        os.close(fd)


expect_errno("explicit_write_deny", errno.EACCES, lambda: write_one(explicit_write_file))
expect_errno("legacy_write_collapse", errno.EROFS, lambda: write_one(legacy_file))
expect_errno("host_prerequisite", errno.EIO, lambda: os.listdir(protected_dir))
PY

echo "gate2: running hostfs discoverable namespace smoke"
if ! with_timeout "$GATE_TIMEOUT" env \
	HOME="$hostfs_list_root" \
	HIDEOUT_STORE_ROOT="$store" \
  LIMA_HOME="$lima_home" \
  "$hideout" run --backend lima --workspace "$workspace" \
    --fs "see-dir:$hostfs_list_root" \
    --fs "see-tree:$hostfs_tree_root" \
    --fs "see:$hostfs_exact_dir" \
    --fs "see-dir:$hostfs_overflow_dir" \
    --fs "see-dir:$hostfs_protected_dir" \
	  --fs "read:$hostfs_legacy_file" \
	  --fs "read:$hostfs_discover_denied_readable" \
	  --no-fs "see-tree:$hostfs_discover_denied_dir" \
	  --no-fs "read:$hostfs_read_denied_file" \
    -- python3 ./hostfs-visibility-namespace.py \
      "$hostfs_outside_file" \
      "$hostfs_hidden_file" \
      "$hostfs_list_root" \
      "$hostfs_tree_root" \
      "$hostfs_exact_dir" \
      "$hostfs_locked_file" \
      "$hostfs_read_denied_file" \
      "$hostfs_overflow_dir" \
      "$hostfs_explicit_write_file" \
	    "$hostfs_legacy_file" \
	    "$hostfs_protected_dir" \
	    "$hostfs_discover_denied_readable" \
      >"$tmp/hostfs-visibility-namespace.out" 2>"$tmp/hostfs-visibility-namespace.err"; then
  echo "gate2: HostFS discoverable namespace smoke failed" >&2
  cat "$tmp/hostfs-visibility-namespace.out" "$tmp/hostfs-visibility-namespace.err" >&2
  exit 1
fi
cat "$tmp/hostfs-visibility-namespace.out"
grep -q 'outside_domain=errno-2' "$tmp/hostfs-visibility-namespace.out"
grep -q 'force_hidden=errno-2' "$tmp/hostfs-visibility-namespace.out"
grep -q 'force_hidden_list=errno-2' "$tmp/hostfs-visibility-namespace.out"
grep -q 'coarse_complete=yes' "$tmp/hostfs-visibility-namespace.out"
grep -q 'discover_denied_exact_read=yes' "$tmp/hostfs-visibility-namespace.out"
grep -q 'exact_dir_readdir=errno-13' "$tmp/hostfs-visibility-namespace.out"
grep -q 'locked_read=errno-13' "$tmp/hostfs-visibility-namespace.out"
grep -q 'explicit_read_deny=errno-13' "$tmp/hostfs-visibility-namespace.out"
grep -q 'overflow=errno-75' "$tmp/hostfs-visibility-namespace.out"
grep -q 'explicit_write_deny=errno-13' "$tmp/hostfs-visibility-namespace.out"
grep -q 'legacy_write_collapse=errno-30' "$tmp/hostfs-visibility-namespace.out"
grep -q 'host_prerequisite=errno-5' "$tmp/hostfs-visibility-namespace.out"
HIDEOUT_STORE_ROOT="$store" "$hideout" decision list --kind hostfs.read --include-terminal >"$tmp/hostfs-visibility-namespace-decisions.json"
HOSTFS_LOCKED_PATH="$hostfs_locked_file" HOSTFS_DENIED_PATH="$hostfs_read_denied_file" jq -e '
  ([.[] | select(.proposedAction.path == env.HOSTFS_LOCKED_PATH)] | length) == 1 and
  ([.[] | select(.proposedAction.path == env.HOSTFS_DENIED_PATH)] | length) == 0
' "$tmp/hostfs-visibility-namespace-decisions.json" >/dev/null
for marker in 1 2 3 4 5 6 14 15 16 17; do
  printf 'hostfs_visibility_%s=passed\n' "$marker"
done

cat >"$workspace/hostfs-visibility-live.py" <<'PY'
import errno
import os
from pathlib import Path
import shutil
import stat
import subprocess
import sys
import time

workspace, approve_path, deny_path, timeout_path, link_path = sys.argv[1:]
workspace = Path(workspace)

# This legacy Gate 2 topology uses a static Lima virtiofs workspace. Feature 041
# deliberately does not promote direct execution for that mechanism; the
# feature-specific shared-Portal gate owns the direct-execution assertion.
fsread_bin = "/tmp/hideout-gate-fsread"
shutil.copyfile("hideout-gate-fsread", fsread_bin)
os.chmod(fsread_bin, 0o755)


def marker(name):
    (workspace / name).write_text("yes\n")


def wait_marker(name, timeout=180):
    deadline = time.monotonic() + timeout
    path = workspace / name
    while time.monotonic() < deadline:
        if path.exists():
            return
        time.sleep(0.05)
    raise AssertionError(f"timed out waiting for {name}")


def expect_eacces(path):
    started = time.monotonic()
    try:
        Path(path).read_bytes()
    except OSError as exc:
        if exc.errno != errno.EACCES:
            raise AssertionError(f"read {path}: errno={exc.errno}, want EACCES") from exc
    else:
        raise AssertionError(f"read {path} unexpectedly succeeded")
    if time.monotonic() - started >= 2:
        raise AssertionError(f"read {path} did not fail promptly")


def broker_read(path, success, expected=""):
    proc = subprocess.run(
        [fsread_bin, "--broker-read", path],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        check=False,
    )
    if success:
        if proc.returncode != 0 or f"hostfs_broker={expected}" not in proc.stdout:
            raise AssertionError(f"broker read failed rc={proc.returncode} out={proc.stdout!r} err={proc.stderr!r}")
    elif proc.returncode == 0:
        raise AssertionError(f"broker read unexpectedly succeeded: {proc.stdout!r}")


expect_eacces(approve_path)
marker("vis-approve-first")
wait_marker("vis-approve-repeat")
expect_eacces(approve_path)
marker("vis-approve-requested")
wait_marker("vis-approve-go")
if Path(approve_path).read_text() != "approved-live-content-029\n":
    raise AssertionError("approved content mismatch")
marker("vis-approve-content")
deadline = time.monotonic() + 1.0
while True:
    info = os.stat(approve_path)
    if info.st_size == len(b"approved-live-content-029\n") and stat.S_IMODE(info.st_mode) == 0o640:
        break
    if time.monotonic() >= deadline:
        raise AssertionError(f"ordinary stat metadata did not converge: size={info.st_size} mode={oct(stat.S_IMODE(info.st_mode))}")
    time.sleep(0.02)
marker("vis-approve-done")

expect_eacces(deny_path)
marker("vis-deny-requested")
wait_marker("vis-deny-go")
expect_eacces(deny_path)
marker("vis-deny-verified")
wait_marker("vis-reopen-go")
expect_eacces(deny_path)
marker("vis-reopen-verified")
wait_marker("vis-deny-redone")

expect_eacces(timeout_path)
marker("vis-timeout-requested")
wait_marker("vis-timeout-go")
expect_eacces(timeout_path)
marker("vis-timeout-verified")

broker_read(link_path, False)
marker("vis-link-requested")
wait_marker("vis-link-go")
broker_read(link_path, True, "symlink-target-a-029")
marker("vis-link-read")
wait_marker("vis-link-retarget-go")
broker_read(link_path, False)
marker("vis-link-retargeted")
PY

rm -f "$workspace"/vis-*
echo "gate2: running HostFS live read-decision smoke"
# The marker handshake files are written by the GUEST, so the script's marker
# base must be the guest workspace path (/workspace in alias mode), which maps to
# the host "$workspace" the wait_for_file loops below poll. Passing the host path
# fails in the isolated session: the host path is only reachable through the
# read-oriented hostfs FUSE, so creating a marker file there returns ENOENT.
env \
  HIDEOUT_STORE_ROOT="$store" \
  LIMA_HOME="$lima_home" \
  "$hideout" run --backend lima --workspace "$workspace" \
    --fs "see-dir:$hostfs_live_root" \
    -- python3 ./hostfs-visibility-live.py \
      /workspace \
      "$hostfs_live_approve" \
      "$hostfs_live_deny" \
      "$hostfs_live_timeout" \
      "$hostfs_live_link" \
      >"$tmp/hostfs-visibility-live.out" 2>"$tmp/hostfs-visibility-live.err" &
visibility_run_pid=$!

wait_for_file "$workspace/vis-approve-first" "first approval-eligible read"
wait_for_hostfs_read_decision "$hostfs_live_approve" "$tmp/hostfs-live-approve-list-before.json"
approve_id="$(hostfs_read_decision_id "$hostfs_live_approve" "$tmp/hostfs-live-approve-list-before.json")"
test -n "$approve_id"
HIDEOUT_STORE_ROOT="$store" "$hideout" decision inspect "$approve_id" >"$tmp/hostfs-live-approve-before.json"
approve_timeout_before="$(jq -r '.timeoutAt' "$tmp/hostfs-live-approve-before.json")"
approve_revision_before="$(jq -r '.revision' "$tmp/hostfs-live-approve-before.json")"
touch "$workspace/vis-approve-repeat"
wait_for_file "$workspace/vis-approve-requested" "deduplicated approval-eligible retry"
HIDEOUT_STORE_ROOT="$store" "$hideout" decision list --kind hostfs.read --include-terminal >"$tmp/hostfs-live-approve-list-after.json"
HOSTFS_READ_PATH="$hostfs_live_approve" jq -e '([.[] | select(.proposedAction.path == env.HOSTFS_READ_PATH)] | length) == 1' "$tmp/hostfs-live-approve-list-after.json" >/dev/null
HIDEOUT_STORE_ROOT="$store" "$hideout" decision inspect "$approve_id" >"$tmp/hostfs-live-approve-after.json"
test "$(jq -r '.timeoutAt' "$tmp/hostfs-live-approve-after.json")" = "$approve_timeout_before"
test "$(jq -r '.revision' "$tmp/hostfs-live-approve-after.json")" = "$approve_revision_before"
printf 'hostfs_visibility_7=passed\n'
printf 'hostfs_visibility_8=passed\n'

HIDEOUT_STORE_ROOT="$store" "$hideout" decision claim "$approve_id" >"$tmp/hostfs-live-approve-claim.json"
approve_claim="$(jq -r '.claimToken' "$tmp/hostfs-live-approve-claim.json")"
test -n "$approve_claim"
HIDEOUT_STORE_ROOT="$store" "$hideout" decision approve --claim-token "$approve_claim" "$approve_id" >"$tmp/hostfs-live-approve-result.json"
jq -e '.status == "applied" and .providerResult.activated == true and .providerResult.scope == "exact-file"' "$tmp/hostfs-live-approve-result.json" >/dev/null
touch "$workspace/vis-approve-go"
wait_for_file "$workspace/vis-approve-content" "same-session approved content retry"
wait_for_file "$workspace/vis-approve-done" "bounded ordinary stat convergence"
printf 'hostfs_visibility_9=passed\n'
printf 'hostfs_visibility_10=passed\n'
printf 'hostfs_visibility_11=passed\n'

wait_for_file "$workspace/vis-deny-requested" "deniable read request"
wait_for_hostfs_read_decision "$hostfs_live_deny" "$tmp/hostfs-live-deny-list.json"
deny_id="$(hostfs_read_decision_id "$hostfs_live_deny" "$tmp/hostfs-live-deny-list.json")"
HIDEOUT_STORE_ROOT="$store" "$hideout" decision claim "$deny_id" >"$tmp/hostfs-live-deny-claim.json"
deny_claim="$(jq -r '.claimToken' "$tmp/hostfs-live-deny-claim.json")"
HIDEOUT_STORE_ROOT="$store" "$hideout" decision deny --claim-token "$deny_claim" "$deny_id" >"$tmp/hostfs-live-deny-result.json"
jq -e '.status == "denied"' "$tmp/hostfs-live-deny-result.json" >/dev/null
touch "$workspace/vis-deny-go"
wait_for_file "$workspace/vis-deny-verified" "terminal deny retry"
HIDEOUT_STORE_ROOT="$store" "$hideout" decision reopen --reason gate2-reconsidered "$deny_id" >"$tmp/hostfs-live-reopen-result.json"
jq -e '.status == "pending"' "$tmp/hostfs-live-reopen-result.json" >/dev/null
touch "$workspace/vis-reopen-go"
wait_for_file "$workspace/vis-reopen-verified" "reopened pending retry"
HIDEOUT_STORE_ROOT="$store" "$hideout" decision claim "$deny_id" >"$tmp/hostfs-live-deny-reclaim.json"
deny_reclaim="$(jq -r '.claimToken' "$tmp/hostfs-live-deny-reclaim.json")"
HIDEOUT_STORE_ROOT="$store" "$hideout" decision deny --claim-token "$deny_reclaim" "$deny_id" >"$tmp/hostfs-live-deny-redone.json"
touch "$workspace/vis-deny-redone"

wait_for_file "$workspace/vis-timeout-requested" "timeout read request"
wait_for_hostfs_read_decision "$hostfs_live_timeout" "$tmp/hostfs-live-timeout-list.json"
timeout_id="$(hostfs_read_decision_id "$hostfs_live_timeout" "$tmp/hostfs-live-timeout-list.json")"
timeout_file="$store/operator-center/decisions/$timeout_id.json"
jq '.timeoutAt = "2000-01-01T00:00:00Z"' "$timeout_file" >"$timeout_file.tmp"
chmod 0600 "$timeout_file.tmp"
mv "$timeout_file.tmp" "$timeout_file"
HIDEOUT_STORE_ROOT="$store" "$hideout" decision list --kind hostfs.read --include-terminal >"$tmp/hostfs-live-timeout-after.json"
HOSTFS_TIMEOUT_ID="$timeout_id" jq -e 'any(.[]; .id == env.HOSTFS_TIMEOUT_ID and .state == "timed-out")' "$tmp/hostfs-live-timeout-after.json" >/dev/null
touch "$workspace/vis-timeout-go"
wait_for_file "$workspace/vis-timeout-verified" "timed-out read retry"
printf 'hostfs_visibility_12=passed\n'

wait_for_file "$workspace/vis-link-requested" "symlink broker read request"
wait_for_hostfs_read_decision "$hostfs_live_link" "$tmp/hostfs-live-link-list.json"
link_id="$(hostfs_read_decision_id "$hostfs_live_link" "$tmp/hostfs-live-link-list.json")"
HIDEOUT_STORE_ROOT="$store" "$hideout" decision claim "$link_id" >"$tmp/hostfs-live-link-claim.json"
link_claim="$(jq -r '.claimToken' "$tmp/hostfs-live-link-claim.json")"
HIDEOUT_STORE_ROOT="$store" "$hideout" decision approve --claim-token "$link_claim" "$link_id" >"$tmp/hostfs-live-link-result.json"
touch "$workspace/vis-link-go"
wait_for_file "$workspace/vis-link-read" "approved canonical symlink read"
rm "$hostfs_live_link"
ln -s "$hostfs_live_target_b" "$hostfs_live_link"
touch "$workspace/vis-link-retarget-go"
wait_for_file "$workspace/vis-link-retargeted" "symlink retarget denial"
printf 'hostfs_visibility_13=passed\n'

if wait "$visibility_run_pid"; then
  visibility_run_pid=""
else
  live_status=$?
  visibility_run_pid=""
  echo "gate2: HostFS live read-decision guest failed with status $live_status" >&2
  cat "$tmp/hostfs-visibility-live.out" "$tmp/hostfs-visibility-live.err" >&2
  exit "$live_status"
fi
cat "$tmp/hostfs-visibility-live.out"
live_session="$(jq -r '.source.session' "$tmp/hostfs-live-approve-after.json")"
if [ -z "$live_session" ] || [ "$live_session" = "null" ]; then
  echo "gate2: HostFS live decision did not surface its session identity" >&2
  cat "$tmp/hostfs-live-approve-after.json" >&2
  exit 1
fi
if HIDEOUT_STORE_ROOT="$store" "$hideout" decision reopen --reason ended-session-must-fail "$deny_id" >"$tmp/hostfs-live-ended-reopen.out" 2>"$tmp/hostfs-live-ended-reopen.err"; then
  echo "gate2: ended-session HostFS read decision reopened unexpectedly" >&2
  exit 1
fi
if [ -d "$store/sessions/$live_session/hostfs-read" ]; then
  echo "gate2: ended session retained HostFS read authority: $live_session" >&2
  find "$store/sessions/$live_session/hostfs-read" -maxdepth 1 -print >&2 || true
  exit 1
fi
printf 'hostfs_visibility_18=passed\n'

operator_audit="$store/operator-center/audit.jsonl"
test -f "$operator_audit"
jq -e 'select(.details.kind == "hostfs.read")' "$operator_audit" >/dev/null
if grep -En 'approved-live-content-029|symlink-target-[ab]-029|claim_[0-9a-f]{16,}|cap_[A-Za-z0-9]{12,}|hostfs-read/(grants|state|owner|provider)' \
  "$operator_audit" \
  "$tmp/hostfs-live-approve-after.json" \
  "$tmp/hostfs-live-deny-result.json" \
  "$tmp/hostfs-live-reopen-result.json" \
  "$tmp/hostfs-live-timeout-after.json" \
  "$tmp/hostfs-live-link-result.json" >/dev/null; then
  echo "gate2: HostFS read evidence leaked content, token, symlink target, or private authority path" >&2
  exit 1
fi
printf 'hostfs_visibility_19=passed\n'

echo "gate2: running hostfs write overlay smoke"
if ! with_timeout "$GATE_TIMEOUT" env \
  HIDEOUT_STORE_ROOT="$store" \
  LIMA_HOME="$lima_home" \
  "$hideout" run --backend lima --workspace "$workspace" \
    --fs "overlay:$hostfs_write_file" \
    --fs "overlay-dir:$hostfs_root" \
    -- sh -eu -c '
dump_hostfs_debug() {
  echo "hostfs_debug_mounts:" >&2
  grep " /hideout/hostfs " /proc/mounts >&2 || true
  echo "hostfs_debug_log:" >&2
  cat /hideout/session/tmp/hostfsd.log >&2 2>/dev/null || true
}
if command -v python3 >/dev/null 2>&1; then
  python3 -c "import pathlib, sys; pathlib.Path(sys.argv[1]).write_text(\"hostfs-after\\n\"); print(\"hostfs_overlay_guest=\" + pathlib.Path(sys.argv[1]).read_text().strip())" "$1" || {
    dump_hostfs_debug
    exit 1
  }
else
  printf "hostfs-after\n" > "$1" || {
    dump_hostfs_debug
    exit 1
  }
	  printf "hostfs_overlay_guest=%s\n" "$(cat "$1")"
	fi
mkdir "$2" || {
  dump_hostfs_debug
  exit 1
}
test -d "$2" || {
  dump_hostfs_debug
  exit 1
}
printf "hostfs_overlay_dir_guest=yes\n"
' gate2-hostfs-write "$hostfs_write_file" "$hostfs_write_dir" >"$tmp/hostfs-write.out" 2>"$tmp/hostfs-write.err"; then
  echo "gate2: hostfs write overlay guest smoke failed" >&2
  echo "gate2: stdout" >&2
  cat "$tmp/hostfs-write.out" >&2
  echo "gate2: stderr" >&2
  cat "$tmp/hostfs-write.err" >&2
  exit 1
fi
cat "$tmp/hostfs-write.out"
grep -q 'hostfs_overlay_guest=hostfs-after' "$tmp/hostfs-write.out"
grep -q 'hostfs_overlay_dir_guest=yes' "$tmp/hostfs-write.out"
test "$(cat "$hostfs_write_file")" = "hostfs-before"
test ! -e "$hostfs_write_dir"

HIDEOUT_STORE_ROOT="$store" "$hideout" hostfs write status >"$tmp/hostfs-write-status.json"
HOSTFS_WRITE_FILE="$hostfs_write_file" jq -e '
  .pending[] |
  select(.path == env.HOSTFS_WRITE_FILE and (.operation == "replace" or .operation == "create" or .operation == "append" or .operation == "truncate"))
' "$tmp/hostfs-write-status.json" >/dev/null
decision_id="$(HOSTFS_WRITE_FILE="$hostfs_write_file" jq -r '
  .pending[] |
  select(.path == env.HOSTFS_WRITE_FILE and (.operation == "replace" or .operation == "create" or .operation == "append")) |
  .decisionId
' "$tmp/hostfs-write-status.json" | head -n 1)"
if [ -z "$decision_id" ]; then
  decision_id="$(HOSTFS_WRITE_FILE="$hostfs_write_file" jq -r '
    .pending[] |
    select(.path == env.HOSTFS_WRITE_FILE and .operation == "truncate") |
    .decisionId
  ' "$tmp/hostfs-write-status.json" | head -n 1)"
fi
if [ -z "$decision_id" ]; then
  echo "gate2: no HostFS write decision found" >&2
  cat "$tmp/hostfs-write-status.json" >&2
  exit 1
fi
HIDEOUT_STORE_ROOT="$store" "$hideout" hostfs write claim "$decision_id" >"$tmp/hostfs-write-claim.json"
claim_token="$(jq -r '.claimToken' "$tmp/hostfs-write-claim.json")"
test -n "$claim_token"
HIDEOUT_STORE_ROOT="$store" "$hideout" hostfs write apply --claim-token "$claim_token" "$decision_id" >"$tmp/hostfs-write-apply.json"
jq -e '.status == "applied" and .decision == "allow"' "$tmp/hostfs-write-apply.json" >/dev/null
test "$(cat "$hostfs_write_file")" = "hostfs-after"
dir_decision_id="$(HOSTFS_WRITE_DIR="$hostfs_write_dir" jq -r '
  .pending[] |
  select(.path == env.HOSTFS_WRITE_DIR and .operation == "mkdir") |
  .decisionId
' "$tmp/hostfs-write-status.json" | head -n 1)"
if [ -z "$dir_decision_id" ]; then
  echo "gate2: no HostFS write directory decision found" >&2
  cat "$tmp/hostfs-write-status.json" >&2
  exit 1
fi
HIDEOUT_STORE_ROOT="$store" "$hideout" hostfs write claim "$dir_decision_id" >"$tmp/hostfs-write-dir-claim.json"
dir_claim_token="$(jq -r '.claimToken' "$tmp/hostfs-write-dir-claim.json")"
test -n "$dir_claim_token"
HIDEOUT_STORE_ROOT="$store" "$hideout" hostfs write apply --claim-token "$dir_claim_token" "$dir_decision_id" >"$tmp/hostfs-write-dir-apply.json"
jq -e '.status == "applied" and .decision == "allow"' "$tmp/hostfs-write-dir-apply.json" >/dev/null
test -d "$hostfs_write_dir"
latest_audit="$(find "$store/sessions" -name audit.jsonl -print | sort | tail -n 1)"
test -n "$latest_audit"
jq -e 'select(.action == "host.fs.overlay.apply" and .details.decisionId == "'"$decision_id"'")' "$latest_audit" >/dev/null
jq -e 'select(.action == "host.fs.overlay.apply" and .details.decisionId == "'"$dir_decision_id"'")' "$latest_audit" >/dev/null
if grep -En 'claim_[0-9a-f]|hostfs-overlay/objects|hfwobj_' "$latest_audit" "$tmp/hostfs-write-status.json" "$tmp/hostfs-write-apply.json" "$tmp/hostfs-write-dir-apply.json" >/dev/null; then
  echo "gate2: HostFS write evidence leaked claim token or overlay object path" >&2
  exit 1
fi
printf "hostfs_write_overlay=applied\n"
printf "hostfs_write_dir_overlay=applied\n"
printf 'hostfs_visibility_20=passed\n'

echo "gate2: running missing-command no-host-fallback smoke"
if with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run --backend lima --workspace "$workspace" -- hideout-missing-command >"$tmp/missing.out" 2>"$tmp/missing.err"; then
  echo "gate2: missing command unexpectedly succeeded" >&2
  exit 1
fi
# No host fallback: the miss must be reported by the guest supervisor, which
# only runs inside the guest and resolves the target command against the guest
# PATH. Its signature message ("target command %q was not found in PATH",
# cmd/hideout-session-supervisor/process_linux.go) is distinct from the host
# backend's "executable file not found in PATH", so matching it proves the
# lookup stayed in the guest rather than falling back to the host.
if ! grep -q 'target command "hideout-missing-command" was not found in PATH' "$tmp/missing.err"; then
  echo "gate2: missing-command stderr did not surface the guest-supervisor not-found error (no host fallback)" >&2
  cat "$tmp/missing.err" >&2
  exit 1
fi

echo "gate2: running ephemeral identity smoke"
if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run --backend lima --ephemeral --workspace "$workspace" -- sh -eu -c '
identity_root=$(dirname "$HOME")
printf "ephemeral_home=%s\n" "$HOME"
machine_id="$(cat "$identity_root/machine/machine-id")"
if [ "${#machine_id}" -ne 32 ] || ! printf '%s' "$machine_id" | grep -Eq '^[0-9a-f]{32}$'; then
  echo "invalid ephemeral machine identity" >&2
  exit 1
fi
printf "ephemeral_machine=present\n"
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

echo "gate2: running named-environment lifecycle smoke"
HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" env list >"$tmp/env-list-before.out"
env_name="$(awk -F'\t' 'NR > 1 && $2 != "unsupported-version" { print $1; exit }' "$tmp/env-list-before.out")"
if [ -z "$env_name" ]; then
  echo "gate2: no reusable lima environment found" >&2
  cat "$tmp/env-list-before.out" >&2
  exit 1
fi

# Re-selecting the same workspace resolves the same named environment.
if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run --backend lima --workspace "$workspace" --env "$env_name" -- sh -eu -c '
printf "reselect_ok=yes\n"
' >"$tmp/env-reselect.out" 2>"$tmp/env-reselect.err"; then
  echo "gate2: run --env reselect failed" >&2
  cat "$tmp/env-reselect.out" "$tmp/env-reselect.err" >&2
  exit 1
fi
grep -q 'reselect_ok=yes' "$tmp/env-reselect.out"
env_id="$(awk -F'\t' -v name="$env_name" 'NR > 1 && $1 == name { print $15; exit }' "$tmp/env-list-before.out")"
env_instance="$(awk -F'"' '/"instanceName"/ { print $4; exit }' "$store/environments/$env_id/environment.json")"
if [ -z "$env_instance" ]; then
  echo "gate2: environment record is missing instanceName" >&2
  cat "$store/environments/$env_id/environment.json" >&2
  exit 1
fi

# stop by name releases the VM but keeps the record resumable.
HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" stop "$env_name" >"$tmp/env-stop.out"
grep -q "stopped: $env_id" "$tmp/env-stop.out"
# hideout stop requires stable terminal observations before returning. Confirm
# independently that the preserved instance remains Stopped for three samples;
# Absent is not success here because stop must keep the instance resumable.
lima_status="unknown"
stop_samples=""
stable_stopped=0
for _sample in $(seq 1 15); do
  lima_status="$(LIMA_HOME="$lima_home" limactl list | awk -v name="$env_instance" 'NR > 1 && $1 == name { print $2; exit }')"
  [ -z "$lima_status" ] && lima_status="Absent"
  stop_samples="$stop_samples ${_sample}s=${lima_status}"
  if [ "$lima_status" = "Stopped" ]; then
    stable_stopped=$((stable_stopped + 1))
  else
    stable_stopped=0
  fi
  if [ "$stable_stopped" -ge 3 ]; then
    break
  fi
  sleep 1
done
echo "gate2: post-stop lima status timeline:$stop_samples" >&2
if [ "$stable_stopped" -lt 3 ]; then
  echo "gate2: lima instance was not stopped by hideout stop; status=$lima_status instance=$env_instance" >&2
  LIMA_HOME="$lima_home" limactl list >&2 || true
  exit 1
fi

# Re-running by name after stop boots the same environment again.
if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run --backend lima --workspace "$workspace" --env "$env_name" -- sh -eu -c '
printf "resume_after_stop=yes\n"
' >"$tmp/env-resume-after-stop.out" 2>"$tmp/env-resume-after-stop.err"; then
  echo "gate2: run --env after stop failed" >&2
  cat "$tmp/env-resume-after-stop.out" "$tmp/env-resume-after-stop.err" >&2
  exit 1
fi
grep -q 'resume_after_stop=yes' "$tmp/env-resume-after-stop.out"

# Explicit named environment in a fresh workspace is a distinct environment.
named_ws="$workspace-named"
mkdir -p "$named_ws"
HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" env create gate2-named --backend lima --workspace "$named_ws" >"$tmp/env-create.out"
grep -q 'created environment gate2-named' "$tmp/env-create.out"
if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run --backend lima --workspace "$named_ws" --env gate2-named -- sh -eu -c '
printf "named_ok=yes\n"
' >"$tmp/env-named.out" 2>"$tmp/env-named.err"; then
  echo "gate2: run --env gate2-named failed" >&2
  cat "$tmp/env-named.out" "$tmp/env-named.err" >&2
  exit 1
fi
grep -q 'named_ok=yes' "$tmp/env-named.out"
HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" env list >"$tmp/env-list-named.out"
named_env_id="$(awk -F'\t' 'NR > 1 && $1 == "gate2-named" { print $15; exit }' "$tmp/env-list-named.out")"
if [ -z "$named_env_id" ]; then
  echo "gate2: gate2-named environment missing from list" >&2
  cat "$tmp/env-list-named.out" >&2
  exit 1
fi

# recreate refuses a running guest without --force, then rebuilds under the
# same name with --force.
env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run --backend lima --workspace "$named_ws" --env gate2-named -- sh -eu -c '
printf "named_guard_started=yes\n"
sleep 120
' >"$tmp/env-named-guard.out" 2>"$tmp/env-named-guard.err" &
named_guard_pid=$!
named_status=""
for _ in $(seq 1 90); do
  named_status="$(awk -F'"' '/"status"/ { print $4; exit }' "$store/environments/$named_env_id/environment.json" 2>/dev/null || true)"
  if [ "$named_status" = "running" ]; then
    break
  fi
  if ! kill -0 "$named_guard_pid" 2>/dev/null; then
    echo "gate2: named guard run exited before environment became running" >&2
    cat "$tmp/env-named-guard.out" "$tmp/env-named-guard.err" >&2 || true
    exit 1
  fi
  sleep 1
done
if [ "$named_status" != "running" ]; then
  echo "gate2: gate2-named did not become running; status=$named_status" >&2
  cat "$store/environments/$named_env_id/environment.json" >&2 || true
  exit 1
fi
if HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" env recreate gate2-named >"$tmp/env-recreate-refuse.out" 2>"$tmp/env-recreate-refuse.err"; then
  echo "gate2: recreate of a running guest should refuse without --force" >&2
  exit 1
fi
grep -q 'hideout stop gate2-named' "$tmp/env-recreate-refuse.err"
HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" env recreate gate2-named --force >"$tmp/env-recreate.out" 2>"$tmp/env-recreate.err"
wait "$named_guard_pid" 2>/dev/null || true
named_guard_pid=""
grep -q 'recreated environment gate2-named' "$tmp/env-recreate.out"

# --rm owns a per-run dedicated disposable environment. The run must succeed,
# prove its teardown (no cleanup-required disposition), remove the disposable
# record, delete the disposable lima instance, and print no resume hint.
HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" env list >"$tmp/env-list-before-rm.out"
before_rm_count="$(awk -F'\t' 'NR > 1' "$tmp/env-list-before-rm.out" | wc -l | tr -d ' ')"
LIMA_HOME="$lima_home" limactl list 2>/dev/null | awk 'NR > 1 { print $1 }' | sort >"$tmp/lima-instances-before-rm.txt"
if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run --backend lima --workspace "$workspace" --rm -- sh -eu -c '
printf "rm_ok=yes\n"
' >"$tmp/env-rm.out" 2>"$tmp/env-rm.err"; then
  echo "gate2: --rm run failed" >&2
  cat "$tmp/env-rm.out" "$tmp/env-rm.err" >&2
  exit 1
fi
grep -q 'rm_ok=yes' "$tmp/env-rm.out"
if grep -q 'run again: hideout run --env' "$tmp/env-rm.err"; then
  echo "gate2: --rm should not print a reusable environment hint" >&2
  cat "$tmp/env-rm.err" >&2
  exit 1
fi
if grep -q 'disposable cleanup required' "$tmp/env-rm.err"; then
  echo "gate2: --rm teardown was not proved (cleanup-required disposition)" >&2
  cat "$tmp/env-rm.err" >&2
  exit 1
fi
HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" env list >"$tmp/env-list-after-rm.out"
after_rm_count="$(awk -F'\t' 'NR > 1' "$tmp/env-list-after-rm.out" | wc -l | tr -d ' ')"
if [ "$after_rm_count" -ne "$before_rm_count" ]; then
  echo "gate2: --rm changed the environment record count (before=$before_rm_count after=$after_rm_count)" >&2
  cat "$tmp/env-list-after-rm.out" >&2
  exit 1
fi
if awk -F'\t' 'NR > 1 && $1 ~ /^rm-/ { found = 1 } END { exit found ? 0 : 1 }' "$tmp/env-list-after-rm.out"; then
  echo "gate2: --rm retained its disposable environment record" >&2
  cat "$tmp/env-list-after-rm.out" >&2
  exit 1
fi
LIMA_HOME="$lima_home" limactl list 2>/dev/null | awk 'NR > 1 { print $1 }' | sort >"$tmp/lima-instances-after-rm.txt"
if ! cmp -s "$tmp/lima-instances-before-rm.txt" "$tmp/lima-instances-after-rm.txt"; then
  echo "gate2: --rm changed the lima instance inventory" >&2
  diff "$tmp/lima-instances-before-rm.txt" "$tmp/lima-instances-after-rm.txt" >&2 || true
  exit 1
fi

# --rm and --ephemeral are orthogonal: the target sees the session-local
# identity while finalization removes that identity, its dedicated environment
# record, lifecycle journal, and exact Lima instance.
echo "gate2: running --rm --ephemeral convergence smoke"
snapshot_disposable_inventory "before-rm-ephemeral"
rm_ephemeral_audit_before="$(disposal_audit_count allow removed)"
if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run \
  --backend lima --workspace "$workspace" --rm --ephemeral -- sh -eu -c '
identity_root=$(dirname "$HOME")
test -f "$identity_root/identity.json"
test -f "$identity_root/machine/machine-id"
printf "rm_ephemeral_ok=yes\n"
' >"$tmp/env-rm-ephemeral.out" 2>"$tmp/env-rm-ephemeral.err"; then
  echo "gate2: --rm --ephemeral run failed" >&2
  cat "$tmp/env-rm-ephemeral.out" "$tmp/env-rm-ephemeral.err" >&2
  exit 1
fi
grep -q 'rm_ephemeral_ok=yes' "$tmp/env-rm-ephemeral.out"
assert_removed_audit_increment "$rm_ephemeral_audit_before" "--rm --ephemeral"
if grep -Eq 'run again: hideout run --env|disposable cleanup required' "$tmp/env-rm-ephemeral.err"; then
  echo "gate2: --rm --ephemeral advertised retained state" >&2
  cat "$tmp/env-rm-ephemeral.err" >&2
  exit 1
fi
snapshot_disposable_inventory "after-rm-ephemeral"
assert_disposable_inventory_unchanged "before-rm-ephemeral" "after-rm-ephemeral" "--rm --ephemeral"
identity_residue="$(find "$store/sessions" -type d -name identity -print 2>/dev/null || true)"
if [ -n "$identity_residue" ]; then
  echo "gate2: --rm --ephemeral retained session identity state" >&2
  printf '%s\n' "$identity_residue" >&2
  exit 1
fi
printf 'rm_ephemeral_convergence=passed\n'

# A target failure remains the command result, but does not cancel authorized
# disposable cleanup. The same exact record/journal/instance inventory must be
# restored before the CLI returns the target failure.
echo "gate2: running failed-target --rm convergence smoke"
snapshot_disposable_inventory "before-rm-target-failure"
rm_target_audit_before="$(disposal_audit_count allow removed)"
rm_target_status=0
if with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" run \
  --backend lima --workspace "$workspace" --rm -- sh -c '
printf "rm_target_started=yes\n"
exit 23
' >"$tmp/env-rm-target-failure.out" 2>"$tmp/env-rm-target-failure.err"; then
  echo "gate2: failed --rm target unexpectedly succeeded" >&2
  exit 1
else
  rm_target_status=$?
fi
if [ "$rm_target_status" -ne 23 ]; then
  echo "gate2: failed --rm target returned $rm_target_status, want 23" >&2
  cat "$tmp/env-rm-target-failure.out" "$tmp/env-rm-target-failure.err" >&2
  exit 1
fi
grep -q 'rm_target_started=yes' "$tmp/env-rm-target-failure.out"
assert_removed_audit_increment "$rm_target_audit_before" "failed-target --rm"
if grep -Eq 'run again: hideout run --env|disposable cleanup required' "$tmp/env-rm-target-failure.err"; then
  echo "gate2: failed-target --rm advertised retained state" >&2
  cat "$tmp/env-rm-target-failure.err" >&2
  exit 1
fi
snapshot_disposable_inventory "after-rm-target-failure"
assert_disposable_inventory_unchanged \
  "before-rm-target-failure" "after-rm-target-failure" "failed-target --rm"
printf 'rm_target_failure_convergence=passed\n'

# clean by name removes the named environment record and any remaining backend
# instance. The preceding recreate leaves it ready, so do not apply the
# --stopped filter here.
HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" stop gate2-named >"$tmp/env-stop-named.out" 2>"$tmp/env-stop-named.err" || true
HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" clean gate2-named >"$tmp/env-clean-named.out"
HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$hideout" env list >"$tmp/env-list-after.out"
if awk -F'\t' 'NR > 1 && $1 == "gate2-named" { found = 1 } END { exit found ? 0 : 1 }' "$tmp/env-list-after.out"; then
  echo "gate2: cleaned named environment is still listed" >&2
  cat "$tmp/env-list-after.out" >&2
  exit 1
fi

if [ "$GATE2_RUNTIME_MODE" = "1" ]; then
  echo "runtime_package_tool_provisioning_check=passed"
fi
echo "gate2: passed"
