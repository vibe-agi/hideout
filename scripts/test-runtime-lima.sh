#!/usr/bin/env bash
# Real macOS arm64 acceptance for the promoted developer-standard runtime.
# This script is intentionally not part of Gate 0: it downloads and boots the
# retained artifact, then runs the complete Gate 2 without package/tool
# provisioning. Hideout's required Go-owned Lima system bootstrap still runs.
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
. "$ROOT/scripts/lib/lima-temp.sh"
. "$ROOT/scripts/lib/runtime-product-evidence.sh"

family="${HIDEOUT_RUNTIME_FAMILY:-developer-standard}"
evidence_out="${HIDEOUT_RUNTIME_EVIDENCE_OUT:-$ROOT/dist/runtime/evidence/031-runtime-lima}"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/h31-runtime.XXXXXX")"
drift_store=""
drift_lima_home=""
cleanup() {
  if [ -n "$drift_store" ] && [ -x "$hideout" ]; then
    HIDEOUT_STORE_ROOT="$drift_store" LIMA_HOME="$drift_lima_home" "$hideout" clean >/dev/null 2>&1 || true
  fi
  if [ -n "$drift_lima_home" ]; then
    rm -rf "$drift_lima_home"
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
if [ -n "${HIDEOUT_RELEASE_BINARY:-}" ]; then
  [ -x "$HIDEOUT_RELEASE_BINARY" ] || { echo "runtime-lima: HIDEOUT_RELEASE_BINARY is not executable" >&2; exit 126; }
  cp "$HIDEOUT_RELEASE_BINARY" "$hideout"
  chmod 0700 "$hideout"
else
  go build -o "$hideout" ./cmd/hideout
fi
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
candidate_commit="$(jq -r '.source.buildCommit' <<<"$artifact")"
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

build_provenance="${HIDEOUT_RUNTIME_BUILD_PROVENANCE:-}"
if [ -z "$build_provenance" ]; then
  while IFS= read -r candidate; do
    if jq -e --arg sha "$sha" --arg commit "$candidate_commit" \
      '.output.sha256 == $sha and .source.commit == $commit and .source.dirty == false' \
      "$candidate" >/dev/null 2>&1; then
      build_provenance="$candidate"
      break
    fi
  done < <(find "$ROOT/dist/runtime" -mindepth 2 -name build-provenance.json -type f 2>/dev/null | sort)
fi
if [ -z "$build_provenance" ] || [ ! -f "$build_provenance" ]; then
  echo "runtime-lima: clean matching build provenance is required for real evidence" >&2
  exit 2
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
if ! HIDEOUT_GATE2_RUNTIME_MODE=1 \
  HIDEOUT_GATE2_RUNTIME_FAMILY="$family" \
  HIDEOUT_RUNTIME_BUILD_PROVENANCE="$build_provenance" \
  scripts/test-gate2-lima.sh >"$tmp/gate2.out" 2>"$tmp/gate2.err"; then
  echo "runtime-lima: complete Gate 2 failed" >&2
  cat "$tmp/gate2.out" >&2
  cat "$tmp/gate2.err" >&2
  exit 1
fi
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
drift_lima_home="$(hideout_mktemp_lima_home)"
drift_workspace="$tmp/drift-workspace"
mkdir -p "$drift_store" "$drift_workspace"
if ! HIDEOUT_STORE_ROOT="$drift_store" LIMA_HOME="$drift_lima_home" "$hideout" init \
  --profile runtime-drift --template dev --backend lima --network direct \
  --runtime "$family" --no-input >"$tmp/drift-init.out" 2>"$tmp/drift-init.err"; then
  echo "runtime-lima: drift profile init failed" >&2
  cat "$tmp/drift-init.out" "$tmp/drift-init.err" >&2
  exit 1
fi
if ! HIDEOUT_STORE_ROOT="$drift_store" LIMA_HOME="$drift_lima_home" "$hideout" env create runtime-drift \
  --profile runtime-drift --backend lima --workspace "$drift_workspace" --runtime "$family" \
  >"$tmp/drift-create.out" 2>"$tmp/drift-create.err"; then
  echo "runtime-lima: drift environment create failed" >&2
  cat "$tmp/drift-create.out" "$tmp/drift-create.err" >&2
  exit 1
fi
if ! HIDEOUT_STORE_ROOT="$drift_store" LIMA_HOME="$drift_lima_home" "$hideout" run \
  --profile runtime-drift --env runtime-drift --workspace "$drift_workspace" -- true \
  >"$tmp/drift-prime.out" 2>"$tmp/drift-prime.err"; then
  echo "runtime-lima: drift environment prime failed" >&2
  cat "$tmp/drift-prime.out" "$tmp/drift-prime.err" >&2
  exit 1
fi
durable_tool="hideout-runtime-durable-tool"
if ! HIDEOUT_STORE_ROOT="$drift_store" LIMA_HOME="$drift_lima_home" "$hideout" run \
  --profile runtime-drift --env runtime-drift --workspace "$drift_workspace" -- \
  sh -eu -c '
    case "$PATH" in
      /hideout/session/shims:/hideout/profile/home/.local/bin:*) ;;
      *) echo "runtime-lima: unexpected target PATH $PATH" >&2; exit 61 ;;
    esac
    case "$PATH" in *"/Users/"*|*"/opt/homebrew"*) echo "runtime-lima: host PATH leaked" >&2; exit 62 ;; esac
    mkdir -p "$HOME/.local/bin"
    printf "%s\n" "#!/bin/sh" "echo runtime_durable_prefix=passed" >"$HOME/.local/bin/$1"
    chmod 0700 "$HOME/.local/bin/$1"
  ' sh "$durable_tool" >"$tmp/durable-create.out" 2>"$tmp/durable-create.err"; then
  echo "runtime-lima: durable-prefix fixture creation failed" >&2
  cat "$tmp/durable-create.out" "$tmp/durable-create.err" >&2
  exit 1
fi
if ! HIDEOUT_STORE_ROOT="$drift_store" LIMA_HOME="$drift_lima_home" "$hideout" stop runtime-drift \
  >"$tmp/durable-stop.out" 2>"$tmp/durable-stop.err"; then
  echo "runtime-lima: durable-prefix environment stop failed" >&2
  cat "$tmp/durable-stop.out" "$tmp/durable-stop.err" >&2
  exit 1
fi
if ! HIDEOUT_STORE_ROOT="$drift_store" LIMA_HOME="$drift_lima_home" "$hideout" run \
  --profile runtime-drift --env runtime-drift --workspace "$drift_workspace" -- "$durable_tool" \
  >"$tmp/durable-restart.out" 2>"$tmp/durable-restart.err"; then
  echo "runtime-lima: durable-prefix tool did not survive stop/start" >&2
  cat "$tmp/durable-restart.out" "$tmp/durable-restart.err" >&2
  exit 1
fi
grep -q '^runtime_durable_prefix=passed$' "$tmp/durable-restart.out"
echo "runtime_durable_prefix=passed"
baseline_id="$(jq -r '[.contract.observations[] | select(.class == "baseline")][0].id // empty' "$tmp/inspect.json")"
baseline_command="$(jq -r '[.contract.observations[] | select(.class == "baseline")][0].command // empty' "$tmp/inspect.json")"
if [ -z "$baseline_id" ] || [ -z "$baseline_command" ]; then
  echo "runtime-lima: promoted contract has no baseline observation for drift proof" >&2
  exit 1
fi
if ! HIDEOUT_STORE_ROOT="$drift_store" LIMA_HOME="$drift_lima_home" "$hideout" run \
  --profile runtime-drift --env runtime-drift --workspace "$drift_workspace" -- \
  sh -eu -c 'mkdir -p "$HOME/.local/bin"; printf "%s\n" "#!/bin/sh" "echo hideout-runtime-drift" >"$HOME/.local/bin/$1"; chmod 0700 "$HOME/.local/bin/$1"' sh "$baseline_command" \
  >"$tmp/drift-mutate.out" 2>"$tmp/drift-mutate.err"; then
  echo "runtime-lima: drift mutation failed" >&2
  cat "$tmp/drift-mutate.out" "$tmp/drift-mutate.err" >&2
  exit 1
fi
if ! HIDEOUT_STORE_ROOT="$drift_store" LIMA_HOME="$drift_lima_home" "$hideout" runtime verify --env runtime-drift --json \
  >"$tmp/drift-verify.json" 2>"$tmp/drift-verify.err"; then
  echo "runtime-lima: drift verification command failed" >&2
  cat "$tmp/drift-verify.json" "$tmp/drift-verify.err" >&2
  exit 1
fi
if ! jq -e --arg id "$baseline_id" \
  '.status.status == "preview-failed" and (.status.failedIds | index($id) != null)' \
  "$tmp/drift-verify.json" >/dev/null; then
  echo "runtime-lima: drift verification did not report the mutated baseline" >&2
  cat "$tmp/drift-verify.json" >&2
  exit 1
fi
if ! HIDEOUT_STORE_ROOT="$drift_store" LIMA_HOME="$drift_lima_home" "$hideout" run \
  --profile runtime-drift --env runtime-drift --workspace "$drift_workspace" -- \
  sh -c 'echo runtime_drift_unrelated=passed' >"$tmp/drift-unrelated.out" 2>"$tmp/drift-unrelated.err"; then
  echo "runtime-lima: unrelated command after drift failed" >&2
  cat "$tmp/drift-unrelated.out" "$tmp/drift-unrelated.err" >&2
  exit 1
fi
grep -q '^runtime_drift_unrelated=passed$' "$tmp/drift-unrelated.out"
echo "runtime_mutable_guest_drift=passed"

mkdir -p "$evidence_out/logs"
cp "$build_provenance" "$evidence_out/build-provenance.json"
cp "$tmp/env-image.out" "$evidence_out/logs/env-image.out"
cp "$tmp/gate2.out" "$evidence_out/logs/gate2.out"
cp "$tmp/drift-verify.json" "$evidence_out/logs/drift-verify.json"
{
  cat "$tmp/env-image.out"
  cat "$tmp/gate2.out"
  echo "runtime_durable_prefix=passed"
  echo "runtime_mutable_guest_drift=passed"
  echo "runtime_download_bytes=$download_bytes"
  echo "runtime_virtual_bytes=$virtual_bytes"
} >"$evidence_out/logs/runtime-lima.out"

if grep -E 'HIDEOUT_SECRET_[A-Z0-9_]+[=:]|\b(cap|ui|claim)_[0-9a-f]{16,}\b|hostfs-overlay/objects/' \
    "$evidence_out/logs/runtime-lima.out" >/dev/null 2>&1; then
  echo "runtime-lima: public evidence contains control-plane material" >&2
  exit 1
fi
runtime_json="$(runtime_evidence_binding "$tmp/gate2.out")"
registry="$evidence_out/proof-registry.json"
"$hideout" support proof-registry --json >"$registry"
artifact_rel="logs/runtime-lima.out"
artifact_sha="$(runtime_evidence_sha256_file "$evidence_out/$artifact_rel")"
proofs='[]'
proofs="$(runtime_evidence_add_proof "$proofs" "$registry" "031.runtime.real-image" \
  "real-gate" "runtime-real-image" "retained image download, digest refusal, and real boot" \
  "$artifact_rel" "$artifact_sha" "$runtime_json")"
proofs="$(runtime_evidence_add_proof "$proofs" "$registry" "031.runtime.baseline" \
  "real-gate" "runtime-baseline" "actual guest baseline and privilege contract" \
  "$artifact_rel" "$artifact_sha" "$runtime_json")"
proofs="$(runtime_evidence_add_proof "$proofs" "$registry" "031.runtime.readiness-parity" \
  "real-gate" "runtime-readiness-parity" "mutable guest drift and readiness parity" \
  "$artifact_rel" "$artifact_sha")"
proofs="$(runtime_evidence_add_proof "$proofs" "$registry" "031.runtime.boundary-regression" \
  "real-gate" "runtime-boundary-regression" "complete Gate 2 boundary regression on retained runtime" \
  "$artifact_rel" "$artifact_sha" "$runtime_json")"
runtime_evidence_write_manifest "$evidence_out/product-hardening-evidence.json" "$proofs" \
  "${HIDEOUT_RUNTIME_PACKAGE_IDENTITY:-}"
go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json \
  "$evidence_out/product-hardening-evidence.json" >/dev/null

cat "$tmp/env-image.out"
cat "$tmp/gate2.out"
echo "runtime_download_bytes=$download_bytes"
echo "runtime_virtual_bytes=$virtual_bytes"
echo "runtime_evidence=$evidence_out/product-hardening-evidence.json"
echo "runtime-lima: passed"
