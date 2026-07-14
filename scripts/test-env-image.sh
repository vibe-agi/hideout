#!/usr/bin/env bash
# Real-backend gate variant for 003 named environments: prove that a declared
# base image is what the guest boots (digest verified by Lima), and that a
# wrong digest fails closed at first boot.
#
# Requires: limactl, network access to the declared image URL, and an
# operator-supplied image declaration:
#   HIDEOUT_ENV_IMAGE_URL='https://.../image.img#sha256:<digest>' \
#     scripts/test-env-image.sh
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

IMAGE_DECL="${HIDEOUT_ENV_IMAGE_URL:-}"
if [ -z "$IMAGE_DECL" ]; then
  echo "env-image: set HIDEOUT_ENV_IMAGE_URL to a https disk-image URL with a #sha256:<digest> fragment" >&2
  exit 2
fi
case "$IMAGE_DECL" in
  https://*'#sha256:'*) ;;
  *)
    echo "env-image: declaration must be https URL with #sha256:<digest> fragment: $IMAGE_DECL" >&2
    exit 2
    ;;
esac

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "env-image: missing required command: $1" >&2
    exit 127
  fi
}
require_command limactl

# Lima derives Unix socket paths below LIMA_HOME. Keep the real-backend gate
# root short even when macOS TMPDIR is deeply nested.
workdir="$(mktemp -d "${TMPDIR:-/tmp}/h31.XXXXXX")"
export HIDEOUT_STORE_ROOT="$workdir/store"
export LIMA_HOME="$workdir/lima"
workspace="$workdir/workspace"
mkdir -p "$HIDEOUT_STORE_ROOT" "$LIMA_HOME" "$workspace"

bin="$workdir/hideout"
if [ -n "${HIDEOUT_RELEASE_BINARY:-}" ]; then
  [ -x "$HIDEOUT_RELEASE_BINARY" ] || {
    echo "env-image: HIDEOUT_RELEASE_BINARY is not executable" >&2
    exit 126
  }
  cp "$HIDEOUT_RELEASE_BINARY" "$bin"
  chmod 0700 "$bin"
else
  require_command go
  go build -o "$bin" ./cmd/hideout
fi

cleanup() {
  status=$?
  trap - EXIT
  for env_name in imgtest imgbad; do
    (cd "$workspace" && "$bin" stop "$env_name" >/dev/null 2>&1) || true
    (cd "$workspace" && "$bin" clean --stopped "$env_name" >/dev/null 2>&1) || true
  done
  rm -rf "$workdir"
  if [ -e "$workdir" ]; then
    echo "env-image: cleanup left temporary root: $workdir" >&2
    status=1
  fi
  exit "$status"
}
trap cleanup EXIT

"$bin" init --no-input --profile default --template dev --backend lima --network direct

echo "env-image: create named environment from declared image"
(cd "$workspace" && "$bin" env create imgtest --image "$IMAGE_DECL")
(cd "$workspace" && "$bin" env inspect imgtest | tee "$workdir/inspect.txt")
grep -F "$IMAGE_DECL" "$workdir/inspect.txt" >/dev/null

echo "env-image: boot the pinned image and read os-release"
(cd "$workspace" && "$bin" run --env imgtest -- cat /etc/os-release | tee "$workdir/os-release.txt")
test -s "$workdir/os-release.txt"

echo "env-image: wrong digest must fail closed at boot"
bad_digest="0000000000000000000000000000000000000000000000000000000000000000"
bad_decl="${IMAGE_DECL%%#sha256:*}#sha256:${bad_digest}"
(cd "$workspace" && "$bin" env create imgbad --image "$bad_decl")
if (cd "$workspace" && "$bin" run --env imgbad -- true) >"$workdir/bad.log" 2>&1; then
  echo "env-image: run with wrong digest unexpectedly succeeded" >&2
  cat "$workdir/bad.log" >&2
  exit 1
fi
echo "env-image: wrong digest failed closed as required"

echo "env-image: passed"
