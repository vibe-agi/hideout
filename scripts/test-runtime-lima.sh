#!/usr/bin/env bash
# Real macOS arm64 acceptance for the promoted developer-standard runtime.
# This script is intentionally not part of Gate 0: it downloads and boots the
# retained artifact, then runs the complete Gate 2 without package/tool
# provisioning. Hideout's required Go-owned Lima system bootstrap still runs.
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

family="${HIDEOUT_RUNTIME_FAMILY:-developer-standard}"
tmp="$(mktemp -d /tmp/h31-runtime.XXXXXX)"
drift_store=""
drift_lima_home=""
cleanup() {
  if [ -n "$drift_store" ] && [ -x "$hideout" ]; then
    HIDEOUT_STORE_ROOT="$drift_store" LIMA_HOME="$drift_lima_home" "$hideout" clean >/dev/null 2>&1 || true
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "runtime-lima: missing required command: $1" >&2
    exit 127
  fi
}
for command in go jq limactl; do require_command "$command"; done

hideout="$tmp/hideout"
go build -o "$hideout" ./cmd/hideout
"$hideout" runtime inspect "$family" --json >"$tmp/inspect.json"

host_os="$(go env GOOS)"
host_arch="$(go env GOARCH)"
if [ "$host_os/$host_arch" != "darwin/arm64" ]; then
  echo "runtime-lima: v1 real acceptance requires darwin/arm64, got $host_os/$host_arch" >&2
  exit 2
fi
artifact_count="$(jq --arg os "$host_os" --arg arch "$host_arch" '[.revision.artifacts[] | select(.hostOS == $os and .hostArch == $arch)] | length' "$tmp/inspect.json")"
if [ "$artifact_count" != "1" ]; then
  echo "runtime-lima: expected exactly one catalog artifact for $host_os/$host_arch, got $artifact_count" >&2
  exit 1
fi
artifact="$(jq -c --arg os "$host_os" --arg arch "$host_arch" '.revision.artifacts[] | select(.hostOS == $os and .hostArch == $arch)' "$tmp/inspect.json")"
url="$(jq -r '.location' <<<"$artifact")"
sha="$(jq -r '.sha256' <<<"$artifact")"
download_bytes="$(jq -r '.downloadBytes' <<<"$artifact")"
virtual_bytes="$(jq -r '.virtualBytes' <<<"$artifact")"

case "$url" in
  https://*.qcow2) ;;
  *) echo "runtime-lima: catalog artifact is not a versioned HTTPS qcow2 URL: $url" >&2; exit 1 ;;
esac
if [ "$url" = "https://example.invalid/"* ] || ! [[ "$sha" =~ ^[0-9a-f]{64}$ ]]; then
  echo "runtime-lima: catalog still contains an unpromoted artifact" >&2
  exit 1
fi
if [ "$download_bytes" -le 0 ] || [ "$download_bytes" -gt $((4 * 1024 * 1024 * 1024)) ] ||
   [ "$virtual_bytes" -le 0 ] || [ "$virtual_bytes" -gt $((16 * 1024 * 1024 * 1024)) ]; then
  echo "runtime-lima: catalog size declaration exceeds the reviewed v1 bounds" >&2
  exit 1
fi

echo "runtime-lima: proving retained image boot and wrong-digest refusal"
HIDEOUT_ENV_IMAGE_URL="$url#sha256:$sha" scripts/test-env-image.sh >"$tmp/env-image.out"
grep -q '^env-image: passed$' "$tmp/env-image.out"

echo "runtime-lima: running complete Gate 2 from the selected runtime"
HIDEOUT_GATE2_RUNTIME_MODE=1 \
HIDEOUT_GATE2_RUNTIME_FAMILY="$family" \
scripts/test-gate2-lima.sh >"$tmp/gate2.out"
grep -q '^runtime_hideout_system_bootstrap=required-and-run$' "$tmp/gate2.out"
grep -q '^runtime_package_tool_provisioning=not-run$' "$tmp/gate2.out"
grep -q '^runtime_contract=passed$' "$tmp/gate2.out"
grep -q '^runtime_package_tool_provisioning_check=passed$' "$tmp/gate2.out"
if grep -Eq '^runtime_(guest_provisioning=not-run|no_guest_provisioning=passed)$' "$tmp/gate2.out"; then
  echo "runtime-lima: Gate 2 emitted an obsolete no-provisioning claim" >&2
  exit 1
fi
grep -q '^gate2: passed$' "$tmp/gate2.out"

echo "runtime-lima: proving mutable-guest drift without target root"
drift_store="$tmp/drift-store"
drift_lima_home="$tmp/drift-lima"
drift_workspace="$tmp/drift-workspace"
mkdir -p "$drift_store" "$drift_lima_home" "$drift_workspace"
HIDEOUT_STORE_ROOT="$drift_store" LIMA_HOME="$drift_lima_home" "$hideout" init \
  --profile runtime-drift --template dev --backend lima --network direct \
  --runtime "$family" --no-input >"$tmp/drift-init.out"
HIDEOUT_STORE_ROOT="$drift_store" LIMA_HOME="$drift_lima_home" "$hideout" env create runtime-drift \
  --profile runtime-drift --backend lima --workspace "$drift_workspace" --runtime "$family" \
  >"$tmp/drift-create.out"
HIDEOUT_STORE_ROOT="$drift_store" LIMA_HOME="$drift_lima_home" "$hideout" run --env runtime-drift -- true \
  >"$tmp/drift-prime.out" 2>"$tmp/drift-prime.err"
baseline_id="$(jq -r '[.contract.observations[] | select(.class == "baseline")][0].id // empty' "$tmp/inspect.json")"
baseline_command="$(jq -r '[.contract.observations[] | select(.class == "baseline")][0].command // empty' "$tmp/inspect.json")"
if [ -z "$baseline_id" ] || [ -z "$baseline_command" ]; then
  echo "runtime-lima: promoted contract has no baseline observation for drift proof" >&2
  exit 1
fi
HIDEOUT_STORE_ROOT="$drift_store" LIMA_HOME="$drift_lima_home" "$hideout" run --env runtime-drift -- \
  sh -eu -c 'mkdir -p "$HOME/.local/bin"; printf "%s\n" "#!/bin/sh" "echo hideout-runtime-drift" >"$HOME/.local/bin/$1"; chmod 0700 "$HOME/.local/bin/$1"' sh "$baseline_command" \
  >"$tmp/drift-mutate.out" 2>"$tmp/drift-mutate.err"
HIDEOUT_STORE_ROOT="$drift_store" LIMA_HOME="$drift_lima_home" "$hideout" runtime verify --env runtime-drift --json \
  >"$tmp/drift-verify.json"
jq -e --arg id "$baseline_id" \
  '.status.status == "preview-failed" and (.status.failedIds | index($id) != null)' \
  "$tmp/drift-verify.json" >/dev/null
HIDEOUT_STORE_ROOT="$drift_store" LIMA_HOME="$drift_lima_home" "$hideout" run --env runtime-drift -- \
  sh -c 'echo runtime_drift_unrelated=passed' >"$tmp/drift-unrelated.out" 2>"$tmp/drift-unrelated.err"
grep -q '^runtime_drift_unrelated=passed$' "$tmp/drift-unrelated.out"
echo "runtime_mutable_guest_drift=passed"

cat "$tmp/env-image.out"
cat "$tmp/gate2.out"
echo "runtime_download_bytes=$download_bytes"
echo "runtime_virtual_bytes=$virtual_bytes"
echo "runtime-lima: passed"
