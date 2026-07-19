#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
  echo "usage: scripts/test-workspace-portal-lima.sh <absolute-artifact-dir>" >&2
  exit 2
}

[[ $# -eq 1 ]] || usage
artifact_dir="$1"
[[ "$artifact_dir" == /* ]] || usage
[[ ! -e "$artifact_dir" ]] || {
  echo "workspace Portal artifact directory already exists: $artifact_dir" >&2
  exit 2
}
[[ "$(uname -s)" == "Darwin" && "$(uname -m)" == "arm64" ]] || {
  echo "workspace Portal Lima probe requires macOS arm64" >&2
  exit 2
}
for tool in go hideout jq python3; do
  command -v "$tool" >/dev/null 2>&1 || {
    echo "workspace Portal Lima probe requires $tool" >&2
    exit 2
  }
done

mkdir -p "$repo_root/.tmp"
work_root="$(mktemp -d "$repo_root/.tmp/workspace-portal-lima.XXXXXX")"
bootstrap="$work_root/bootstrap"
control="$bootstrap/control"
host_root="$work_root/host-root"
host_probe="$work_root/hideout-workspace-probe-host"
guest_probe="$bootstrap/hideout-workspace-probe"
guest_script="$bootstrap/guest-check.sh"
server_pid=""
guest_pid=""

cleanup() {
  if [[ -f "$control/portal-mount.log" && ! -f "$artifact_dir/raw/portal-mount.log" ]]; then
    cp "$control/portal-mount.log" "$artifact_dir/raw/portal-mount.log" 2>/dev/null || true
  fi
  if [[ -n "$guest_pid" ]]; then
    kill "$guest_pid" >/dev/null 2>&1 || true
    wait "$guest_pid" >/dev/null 2>&1 || true
  fi
  if [[ -n "$server_pid" ]]; then
    kill "$server_pid" >/dev/null 2>&1 || true
    wait "$server_pid" >/dev/null 2>&1 || true
  fi
  find "$work_root" -depth -delete 2>/dev/null || true
}
trap cleanup EXIT

mkdir -p "$artifact_dir/raw" "$control" "$host_root"
printf 'host-original\n' >"$host_root/original.txt"
printf 'delete-me\n' >"$host_root/delete-by-host.txt"
printf 'rename-me\n' >"$host_root/rename-by-host.txt"

(
  cd "$repo_root"
  go build -o "$host_probe" ./cmd/hideout-workspace-probe
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
    go build -o "$guest_probe" ./cmd/hideout-workspace-probe
)

"$host_probe" prerequisites \
  --root "$host_root" \
  --output "$artifact_dir/prerequisites.json"
jq -e '
  .schema == "hideout.workspace-host-prerequisites/v1" and
  .status == "passed" and
  .tccStatus == "available" and
  .scope == "probed-root-only" and
  ([.principals[] | select(.state == "observed")] | length) == 1
' "$artifact_dir/prerequisites.json" >/dev/null

cat >"$guest_script" <<'GUEST'
#!/bin/sh
set -eu

mountpoint=/tmp/hideout-workspace-portal-research
portal_pid=""
cleanup_guest() {
  if mountpoint -q "$mountpoint"; then
    fusermount3 -u "$mountpoint" 2>/dev/null || fusermount -u "$mountpoint" 2>/dev/null || true
  fi
  if [ -n "$portal_pid" ]; then
    kill "$portal_pid" 2>/dev/null || true
    wait "$portal_pid" 2>/dev/null || true
  fi
}
trap cleanup_guest EXIT INT TERM

mkdir -p "$mountpoint"
/workspace/hideout-workspace-probe portal-mount \
  --endpoint "$(cat /workspace/control/endpoint.txt)" \
  --credential-file /workspace/control/credential.bin \
  --mount "$mountpoint" \
  --ready-file /workspace/control/mount-ready \
  >/workspace/control/portal-mount.log 2>&1 &
portal_pid=$!

for _ in $(seq 1 200); do
  mountpoint -q "$mountpoint" && break
  kill -0 "$portal_pid" 2>/dev/null || {
    echo "portal mount exited before readiness" >&2
    exit 1
  }
  sleep 0.05
done
mountpoint -q "$mountpoint" || {
  echo "portal mount did not become ready" >&2
  exit 1
}

test "$(cat "$mountpoint/original.txt")" = host-original
# Prime the FUSE directory cache before the host mutates directory membership.
ls -1 "$mountpoint" >/dev/null
printf 'mounted\n' >/workspace/control/guest-mounted
for _ in $(seq 1 200); do
  [ -f /workspace/control/host-mutated ] && break
  sleep 0.01
done
[ -f /workspace/control/host-mutated ] || {
  echo "host mutation trigger was not observed" >&2
  exit 1
}

converged=false
for _ in $(seq 1 200); do
  if [ "$(cat "$mountpoint/original.txt")" = host-updated ] &&
     [ "$(cat "$mountpoint/created-by-host.txt")" = created ] &&
     [ ! -e "$mountpoint/delete-by-host.txt" ] &&
     [ "$(cat "$mountpoint/renamed-by-host.txt")" = rename-me ] &&
     [ ! -e "$mountpoint/rename-by-host.txt" ]; then
    converged=true
    break
  fi
  sleep 0.01
done
[ "$converged" = true ] || {
  echo "host mutation did not invalidate the guest cache" >&2
  ls -la "$mountpoint" >&2 || true
  for path in original.txt created-by-host.txt delete-by-host.txt rename-by-host.txt renamed-by-host.txt; do
    if [ -e "$mountpoint/$path" ]; then
      printf '%s=present:' "$path" >&2
      cat "$mountpoint/$path" >&2 || true
    else
      printf '%s=absent\n' "$path" >&2
    fi
  done
  exit 1
}
printf 'observed\n' >/workspace/control/host-observed
uname -m >/workspace/control/guest-machine

mkdir "$mountpoint/nested"
printf 'guest-write\n' >"$mountpoint/nested/value.tmp"
mv "$mountpoint/nested/value.tmp" "$mountpoint/nested/value.txt"
ln -s value.txt "$mountpoint/nested/link.txt"
test "$(readlink "$mountpoint/nested/link.txt")" = value.txt
chmod 640 "$mountpoint/nested/value.txt"
truncate -s 5 "$mountpoint/nested/value.txt"
sync "$mountpoint/nested/value.txt"
rm "$mountpoint/nested/link.txt"

: >"$mountpoint/lock.txt"
(
  flock -x 9
  printf 'held\n' >/workspace/control/lock-held
  while [ ! -f /workspace/control/release-lock ]; do sleep 0.01; done
) 9>"$mountpoint/lock.txt" &
lock_pid=$!
for _ in $(seq 1 200); do
  [ -f /workspace/control/lock-held ] && break
  sleep 0.01
done
[ -f /workspace/control/lock-held ] || exit 1
if flock -n "$mountpoint/lock.txt" -c true; then
  echo "second lock owner unexpectedly acquired the lock" >&2
  exit 1
fi
printf 'release\n' >/workspace/control/release-lock
wait "$lock_pid"

if cat "$mountpoint/missing.txt" >/dev/null 2>&1; then
  echo "missing path unexpectedly opened" >&2
  exit 1
fi
if ln -s ../../outside "$mountpoint/escape-link" >/dev/null 2>&1; then
  echo "escaping symlink unexpectedly succeeded" >&2
  exit 1
fi

printf 'passed\n' >/workspace/control/guest-result
GUEST
chmod 0700 "$guest_script"

"$host_probe" portal-serve \
  --root "$host_root" \
  --control-dir "$control" \
  --guest-host host.lima.internal \
  --ttl 30m \
  >"$artifact_dir/raw/portal-server.log" 2>&1 &
server_pid=$!

for _ in $(seq 1 200); do
  [[ -f "$control/ready" ]] && break
  kill -0 "$server_pid" 2>/dev/null || {
    cat "$artifact_dir/raw/portal-server.log" >&2
    exit 1
  }
  sleep 0.05
done
[[ -f "$control/ready" && -f "$control/credential.bin" ]] || {
  echo "workspace Portal server did not become ready" >&2
  exit 1
}
[[ "$(stat -f '%Lp' "$control/credential.bin")" == "600" ]] || {
  echo "workspace Portal credential mode is not 0600" >&2
  exit 1
}

hideout run \
  --backend lima \
  --network direct \
  --workspace "$bootstrap" \
  --guest-workspace /workspace \
  --terminal never \
  -- /workspace/guest-check.sh \
  >"$artifact_dir/raw/guest.stdout" 2>"$artifact_dir/raw/guest.stderr" &
guest_pid=$!

for _ in $(seq 1 600); do
  [[ -f "$control/guest-mounted" ]] && break
  kill -0 "$guest_pid" 2>/dev/null || {
    wait "$guest_pid" || true
    cat "$artifact_dir/raw/guest.stderr" >&2
    exit 1
  }
  sleep 0.05
done
[[ -f "$control/guest-mounted" ]] || {
  echo "guest Portal mount did not report readiness" >&2
  exit 1
}

mutation_started_ns="$(python3 -c 'import time; print(time.time_ns())')"
printf 'host-updated\n' >"$host_root/original.next"
mv "$host_root/original.next" "$host_root/original.txt"
printf 'created\n' >"$host_root/created-by-host.txt"
rm "$host_root/delete-by-host.txt"
mv "$host_root/rename-by-host.txt" "$host_root/renamed-by-host.txt"
printf 'mutated\n' >"$control/host-mutated"

for _ in $(seq 1 200); do
  [[ -f "$control/host-observed" ]] && break
  kill -0 "$guest_pid" 2>/dev/null || break
  sleep 0.01
done
[[ -f "$control/host-observed" ]] || {
  echo "guest did not observe the host mutation" >&2
  exit 1
}
mutation_finished_ns="$(python3 -c 'import time; print(time.time_ns())')"

wait "$guest_pid"
guest_pid=""
[[ "$(cat "$control/guest-result")" == "passed" ]] || {
  echo "workspace Portal guest result is not passed" >&2
  exit 1
}
[[ "$(cat "$host_root/original.txt")" == "host-updated" ]] || exit 1
[[ "$(stat -f '%Lp' "$host_root/nested/value.txt")" == "640" ]] || exit 1
[[ "$(stat -f '%z' "$host_root/nested/value.txt")" == "5" ]] || exit 1
[[ "$(cat "$host_root/nested/value.txt")" == "guest" ]] || exit 1
[[ ! -e "$host_root/nested/link.txt" && ! -e "$host_root/escape-link" ]] || exit 1

cp "$control/portal-mount.log" "$artifact_dir/raw/portal-mount.log"
git_dirty=false
if [[ -n "$(git -C "$repo_root" status --porcelain=v1 --untracked-files=all)" ]]; then
  git_dirty=true
fi
convergence_ms="$(python3 - "$mutation_started_ns" "$mutation_finished_ns" <<'PY'
import sys
elapsed = int(sys.argv[2]) - int(sys.argv[1])
if elapsed < 0:
    raise SystemExit("negative host-to-guest convergence interval")
print(f"{elapsed / 1_000_000:.6f}")
PY
)"

jq -n \
  --arg schema "hideout.workspace-portal-lima-correctness/v1" \
  --arg candidate "workspace-portal" \
  --arg commit "$(git -C "$repo_root" rev-parse HEAD)" \
  --argjson dirty "$git_dirty" \
  --arg convergenceMs "$convergence_ms" \
  --arg guestMachine "$(cat "$control/guest-machine")" \
  '{
    schema:$schema,
    candidate:$candidate,
    result:"passed",
    commit:$commit,
    dirty:$dirty,
    guestMachine:$guestMachine,
    hostToGuestConvergenceUpperBoundMs:($convergenceMs|tonumber),
    operations:["lookup","open","read","create","write","fsync","mkdir","rename","symlink","readlink","chmod","truncate","unlink","flock"],
    cacheInvalidationChecks:["host-replace","host-create","host-delete","host-rename"],
    negativeChecks:["missing-path","escaping-symlink","second-lock-owner"],
    controlMaterialInEvidence:false
  }' >"$artifact_dir/portal-correctness.json"

printf 'workspace Portal Lima correctness passed: %s\n' "$artifact_dir"
