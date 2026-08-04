#!/usr/bin/env bash
set -euo pipefail

repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
cd "$repo_root"
# shellcheck source=scripts/lib/gate-result.sh
. "$repo_root/scripts/lib/gate-result.sh"
gate_completed=0

out="$repo_root/.artifacts/045/package-components"

usage() {
  printf '%s\n' \
    "Usage: scripts/gates/package-components.sh [--out DIR]" \
    "" \
    "Builds a local package preflight and proves observer/helper, embedded UI," \
    "license, component-contract, package, and installed-lifecycle bindings." \
    "This is local evidence only and never publishes a release."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --out)
      if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
        printf 'package-components-gate: --out requires a directory\n' >&2
        exit 2
      fi
      out="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'package-components-gate: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

for command in go jq git tar; do
  if ! command -v "$command" >/dev/null 2>&1; then
    printf 'package-components-gate: missing command: %s\n' "$command" >&2
    exit 1
  fi
done

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  printf 'package-components-gate: missing shasum or sha256sum\n' >&2
  return 127
}

source_commit="$(git rev-parse HEAD)"
if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then
  source_dirty=true
else
  source_dirty=false
fi
run_id="run-$(date -u +'%Y%m%dT%H%M%SZ')-$$"
run_dir="$out/$run_id"
mkdir -p "$run_dir"
chmod 0700 "$out" "$run_dir"

package_components_tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-package-components.XXXXXX")"
cleanup() {
  local exit_status=$?
  find "$package_components_tmp" -depth -delete
  if [ "$exit_status" -eq 0 ]; then
    gate_require_completion "package-components-gate"
  fi
}
trap cleanup EXIT

go test \
  ./internal/packagekit \
  ./internal/helperbin \
  ./internal/daemon/uiweb_assets \
  ./internal/supportreport \
  -count=1 2>&1 | tee "$run_dir/go-tests.log"

stage="$package_components_tmp/stage"
archive="$package_components_tmp/hideout.tar.gz"
scripts/package-local.sh --stage "$stage" >"$run_dir/package-stage.log"
scripts/package-local.sh --finalize "$stage" --out "$archive" \
  >"$run_dir/package-finalize.log"
mkdir -p "$package_components_tmp/extracted"
tar -xzf "$archive" -C "$package_components_tmp/extracted"
package_root="$package_components_tmp/extracted/hideout"
guest_arch="$(go env GOARCH)"
host_os="$(go env GOOS)"
host_arch="$(go env GOARCH)"

"$package_root/bin/hideout" package verify "$package_root" \
  >"$run_dir/package-verify.log"
{
  go run ./cmd/hideout-schema-validate \
    "$package_root/schemas/package-manifest.schema.json" \
    "$package_root/package-manifest.json"
  printf 'package manifest schema: passed\n'
} >"$run_dir/package-schema.log"
{
  go run ./cmd/hideout-schema-validate \
    "$package_root/schemas/package-components.schema.json" \
    "$package_root/runtime/package-components.json"
  printf 'package component schema: passed\n'
} >"$run_dir/component-schema.log"
{
  go run ./cmd/hideout-schema-validate \
    "$package_root/schemas/embedded-asset-manifest.schema.json" \
    "$package_root/runtime/browser-console.assets.json"
  printf 'embedded asset schema: passed\n'
} >"$run_dir/asset-schema.log"

observer_binary="$package_root/bin/hideout-observer-linux-$guest_arch"
observer_manifest="$observer_binary.manifest.json"
adoption_binary="$package_root/bin/hideout-migration-adopt-linux-$guest_arch"
adoption_manifest="$adoption_binary.manifest.json"
host_adoption_binary="$package_root/bin/hideout-migration-vz-adopt-$host_os-$host_arch"
host_adoption_manifest="$host_adoption_binary.manifest.json"
asset_manifest="$package_root/runtime/browser-console.assets.json"
component_contract="$package_root/runtime/package-components.json"
package_manifest="$package_root/package-manifest.json"

if [ "$(jq -er '.sha256' "$observer_manifest")" != \
  "$(sha256_file "$observer_binary")" ]; then
  printf 'package-components-gate: observer digest binding failed\n' >&2
  exit 1
fi
if ! jq -e --arg arch "$guest_arch" '
  .version == "hideout.helper-manifest/v1" and
  .command == "hideout-observer" and
  .targetOS == "linux" and
  .targetArch == $arch and
  .builder == "go build -trimpath" and
  .license == "Apache-2.0" and
  .buildMode == "embedded-core-bpf" and
  .packageOwned == true
' "$observer_manifest" >/dev/null; then
  printf 'package-components-gate: observer provenance binding failed\n' >&2
  exit 1
fi
if [ "$(jq -er '.sha256' "$host_adoption_manifest")" != \
  "$(sha256_file "$host_adoption_binary")" ]; then
  printf 'package-components-gate: host migration executor digest binding failed\n' >&2
  exit 1
fi
if ! jq -e --arg os "$host_os" --arg arch "$host_arch" '
  .version == "hideout.helper-manifest/v1" and
  .command == "hideout-migration-vz-adopt" and
  .targetOS == $os and
  .targetArch == $arch and
  .builder == "go build -mod=readonly -trimpath" and
  .upstreamModule == "github.com/Code-Hex/vz/v3" and
  .upstreamVersion == "v3.7.1" and
  .license == "Apache-2.0" and
  .buildMode == "apple-vz-zero-network-adoption-entitled-v1" and
  .packageOwned == true
' "$host_adoption_manifest" >/dev/null ||
  ! "$host_adoption_binary" --probe | jq -e \
    --arg os "$host_os" --arg arch "$host_arch" '
      .schema == "hideout.migration-vz-adopt-probe/v1" and
      .protocol == "hideout.migration-vz-adopt/v1" and
      .version == "1.0.0" and
      .hostOS == $os and .hostArch == $arch and
      .hypervisor == "apple-virtualization-framework" and
      .networkDeviceCount == 0 and
      .controlChannel == "virtiofs-private" and
      .bootTrigger == "nocloud-fixed-cloud-boothook"
    ' >/dev/null; then
  printf 'package-components-gate: host migration executor provenance/probe failed\n' >&2
  exit 1
fi
if [ "$(jq -er '.sha256' "$adoption_manifest")" != \
  "$(sha256_file "$adoption_binary")" ]; then
  printf 'package-components-gate: migration adoption helper digest binding failed\n' >&2
  exit 1
fi
if ! jq -e --arg arch "$guest_arch" '
  .version == "hideout.helper-manifest/v1" and
  .command == "hideout-migration-adopt" and
  .targetOS == "linux" and
  .targetArch == $arch and
  .builder == "go build -trimpath" and
  .license == "Apache-2.0" and
  .buildMode == "strict-data-only-adoption-v1" and
  .packageOwned == true
' "$adoption_manifest" >/dev/null; then
  printf 'package-components-gate: migration adoption helper provenance binding failed\n' >&2
  exit 1
fi
if [ "$(jq -er '.containerSHA256' "$asset_manifest")" != \
  "$(sha256_file "$package_root/bin/hideout")" ]; then
  printf 'package-components-gate: browser container binding failed\n' >&2
  exit 1
fi
if [ "$(jq -er '.embeddedAssets[0].manifestSHA256' "$package_manifest")" != \
  "$(sha256_file "$asset_manifest")" ]; then
  printf 'package-components-gate: browser manifest binding failed\n' >&2
  exit 1
fi
if ! cmp -s runtime/package-components.json "$component_contract"; then
  printf 'package-components-gate: packaged component contract drifted\n' >&2
  exit 1
fi
if [ ! -f "$package_root/LICENSES/GPL-2.0-only.txt" ]; then
  printf 'package-components-gate: observer GPL license text is missing\n' >&2
  exit 1
fi
if [ ! -f "$package_root/third_party/vz/LICENSE" ] ||
  [ -L "$package_root/third_party/vz/LICENSE" ]; then
  printf 'package-components-gate: Code-Hex/vz license text is missing\n' >&2
  exit 1
fi

{
  if command -v ruby >/dev/null 2>&1; then
    ruby -c packaging/homebrew/hideout.rb
  fi
  grep -Fq '"bin/hideout-observer-linux-arm64"' \
    packaging/homebrew/hideout.rb
  grep -Fq '"bin/hideout-migration-adopt-linux-arm64"' \
    packaging/homebrew/hideout.rb
  grep -Fq '             "bin/hideout-migration-vz-adopt-darwin-arm64",' \
    packaging/homebrew/hideout.rb
  grep -Fq 'package_root/"bin/hideout-migration-vz-adopt-darwin-arm64"' \
    packaging/homebrew/hideout.rb
  printf 'Homebrew helper executable preservation: passed\n'
} >"$run_dir/homebrew-formula.log"

scripts/test-package-smoke.sh >"$run_dir/package-lifecycle.log" 2>&1

cp "$package_manifest" "$run_dir/package-manifest.json"
cp "$observer_manifest" "$run_dir/hideout-observer.manifest.json"
cp "$adoption_manifest" "$run_dir/hideout-migration-adopt.manifest.json"
cp "$host_adoption_manifest" \
  "$run_dir/hideout-migration-vz-adopt.manifest.json"
cp "$asset_manifest" "$run_dir/browser-console.assets.json"
cp "$component_contract" "$run_dir/package-components.json"

for evidence_file in "$run_dir"/*; do
  if [ ! -s "$evidence_file" ]; then
    printf \
      'package-components-gate: required evidence is empty: %s\n' \
      "$evidence_file" >&2
    exit 1
  fi
done

find "$run_dir" -type f -exec chmod 0600 {} +
artifacts='[]'
while IFS= read -r evidence_file; do
  relative_path="${evidence_file#"$out"/}"
  artifacts="$(
    jq -c \
      --arg path "$relative_path" \
      --arg sha256 "$(sha256_file "$evidence_file")" \
      '. + [{path: $path, sha256: $sha256}]' <<<"$artifacts"
  )"
done < <(find "$run_dir" -type f | LC_ALL=C sort)

summary="$out/summary.json"
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" \
  --arg run "$run_id" \
  --arg observerSHA256 "$(sha256_file "$observer_binary")" \
  --arg observerManifestSHA256 "$(sha256_file "$observer_manifest")" \
  --arg adoptionSHA256 "$(sha256_file "$adoption_binary")" \
  --arg adoptionManifestSHA256 "$(sha256_file "$adoption_manifest")" \
  --arg hostAdoptionSHA256 "$(sha256_file "$host_adoption_binary")" \
  --arg hostAdoptionManifestSHA256 "$(sha256_file "$host_adoption_manifest")" \
  --arg containerSHA256 "$(sha256_file "$package_root/bin/hideout")" \
  --arg assetManifestSHA256 "$(sha256_file "$asset_manifest")" \
  --arg componentContractSHA256 "$(sha256_file "$component_contract")" \
  --arg packageManifestSHA256 "$(sha256_file "$package_manifest")" \
  --argjson assetCount "$(jq '.assets | length' "$asset_manifest")" \
  --argjson artifacts "$artifacts" \
  '{
    schema: "hideout.package-components-gate/v1",
    generatedAt: $generatedAt,
    source: {commit: $commit, dirty: $dirty},
    result: "passed",
    run: $run,
    scope: "local-component-preflight",
    candidateAcceptance: false,
    checks: {
      observerBinaryAndManifest: "passed",
      observerLicenseBoundary: "passed",
      migrationAdoptionHelperAndManifest: "passed",
      hostMigrationExecutorAndManifest: "passed",
      browserAssetInventory: "passed",
      browserContainerBinding: "passed",
      packageComponentContract: "passed",
      homebrewHelperExecutablePreservation: "passed",
      packageSchemaAndVerification: "passed",
      installUpgradeUninstallLifecycle: "passed"
    },
    inventory: {
      observerSHA256: $observerSHA256,
      observerManifestSHA256: $observerManifestSHA256,
      adoptionSHA256: $adoptionSHA256,
      adoptionManifestSHA256: $adoptionManifestSHA256,
      hostAdoptionSHA256: $hostAdoptionSHA256,
      hostAdoptionManifestSHA256: $hostAdoptionManifestSHA256,
      containerSHA256: $containerSHA256,
      assetManifestSHA256: $assetManifestSHA256,
      componentContractSHA256: $componentContractSHA256,
      packageManifestSHA256: $packageManifestSHA256,
      browserAssetCount: $assetCount
    },
    artifacts: $artifacts,
    limitation:
      "This dirty-aware local preflight proves component/package mechanics only. Exact clean candidate acceptance remains owned by T158-T163."
  }' >"$summary"
chmod 0600 "$summary"

gate_completed=1
printf 'package-components-gate: passed run=%s evidence=%s\n' \
  "$run_id" "$summary"
