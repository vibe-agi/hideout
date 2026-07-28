#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"

usage() {
  cat <<'USAGE'
Usage:
  scripts/package-local.sh --stage <dir> [release options]
  scripts/package-local.sh --finalize <dir> --out <tar.gz> [--signing-mode <mode>]
  scripts/package-local.sh [--out <tar.gz>] [release options]

Release options:
  --source <dir>
  --version <semver-prerelease>
  --channel alpha|developer-preview
  --tag <v-prefixed-version>
  --workflow <identity>
  --ref <git-ref>
  --signing-mode developer-id-observed|developer-preview-unsigned

The two-phase form stages package content without a package manifest, allowing
host binaries to be signed exactly once. Finalization inventories the frozen
tree, writes the canonical manifest, verifies it, and creates the final archive
without rebuilding or mutating signed binaries.
USAGE
}

source="$ROOT"
stage=""
finalize=""
out=""
version="${HIDEOUT_VERSION:-0.1.0-dev.0}"
channel="${HIDEOUT_RELEASE_CHANNEL:-developer-preview}"
tag=""
workflow="${GITHUB_WORKFLOW_REF:-local-package}"
ref="${GITHUB_REF:-local}"
signing_mode=""
signing_mode_set=0

while [ "$#" -gt 0 ]; do
  case "$1" in
    --stage) stage="${2:-}"; shift 2 ;;
    --finalize) finalize="${2:-}"; shift 2 ;;
    --out) out="${2:-}"; shift 2 ;;
    --source) source="${2:-}"; shift 2 ;;
    --version) version="${2:-}"; shift 2 ;;
    --channel) channel="${2:-}"; shift 2 ;;
    --tag) tag="${2:-}"; shift 2 ;;
    --workflow) workflow="${2:-}"; shift 2 ;;
    --ref) ref="${2:-}"; shift 2 ;;
    --signing-mode) signing_mode="${2:-}"; signing_mode_set=1; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "package-local: unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

source="$(cd "$source" && pwd -P)"
if [ -z "$tag" ]; then
  tag="v$version"
fi
if [ "$tag" != "v$version" ]; then
  echo "package-local: --tag must equal v<version>" >&2
  exit 2
fi
case "$channel" in
  alpha|developer-preview) ;;
  *) echo "package-local: unsupported channel: $channel" >&2; exit 2 ;;
esac
if [ -z "$signing_mode" ]; then
  if [ "$channel" = "alpha" ]; then
    signing_mode="developer-id-observed"
  else
    signing_mode="developer-preview-unsigned"
  fi
fi
case "$signing_mode" in
  developer-id-observed|developer-preview-unsigned) ;;
  *) echo "package-local: unsupported signing mode: $signing_mode" >&2; exit 2 ;;
esac

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    echo "package-local: missing shasum or sha256sum" >&2
    exit 127
  fi
}

verify_linux_helper() {
  local root="$1" arch="$2" command="$3"
  local binary="$root/bin/$command-linux-$arch"
  local manifest="$binary.manifest.json"
  if [ ! -f "$binary" ] || [ -L "$binary" ] || [ ! -x "$binary" ]; then
    echo "package-local: required Linux helper $command is missing or not executable: $binary" >&2
    return 1
  fi
  if [ ! -f "$manifest" ] || [ -L "$manifest" ]; then
    echo "package-local: required Linux helper $command manifest is missing: $manifest" >&2
    return 1
  fi
  if ! jq -e --arg arch "$arch" --arg command "$command" --arg artifact "$(basename "$binary")" '
    .version == "hideout.helper-manifest/v1" and
    .command == $command and
    .targetOS == "linux" and
    .targetArch == $arch and
    .artifact == $artifact and
    (.sha256 | test("^[a-f0-9]{64}$"))
  ' "$manifest" >/dev/null; then
    echo "package-local: Linux helper $command manifest identity is invalid: $manifest" >&2
    return 1
  fi
  local want got
  want="$(jq -er '.sha256' "$manifest")"
  got="$(sha256_file "$binary")"
  if [ "$want" != "$got" ]; then
    echo "package-local: Linux helper $command checksum mismatch: want $want got $got" >&2
    return 1
  fi
}

verify_tun2socks_helper() {
  local root="$1" arch="$2"
  verify_linux_helper "$root" "$arch" "tun2socks"
  local manifest="$root/bin/tun2socks-linux-$arch.manifest.json"
  if ! jq -e '
    .upstreamModule == "github.com/xjasonlyu/tun2socks/v2" and
    .upstreamVersion == "v2.6.0" and
    .license == "MIT" and
    .buildMode == "source-built-pinned-module" and
    .packageOwned == true
  ' "$manifest" >/dev/null; then
    echo "package-local: tun2socks provenance manifest is invalid: $manifest" >&2
    return 1
  fi
  if [ ! -f "$root/third_party/tun2socks/LICENSE" ] ||
    [ -L "$root/third_party/tun2socks/LICENSE" ]; then
    echo "package-local: tun2socks upstream license is missing" >&2
    return 1
  fi
}

absolute_path() {
  case "$1" in
    /*) printf '%s\n' "$1" ;;
    *) printf '%s/%s\n' "$PWD" "$1" ;;
  esac
}

package_kind() {
  case "$1" in
    bin/hideout|bin/hideout-shim) echo binary ;;
    bin/*.manifest.json) echo helper-manifest ;;
    bin/*-linux-*) echo linux-helper ;;
    install.sh) echo installer ;;
    README.md|README.zh-CN.md|CHANGELOG.md|RELEASE_NOTES.md) echo entrypoint ;;
    schemas/*) echo schema ;;
    LICENSE|THIRD_PARTY_NOTICES.md|SECURITY.md|docs/*|third_party/*) echo doc ;;
    host-app/*) echo host-app-core-data ;;
    examples/*) echo host-app-example ;;
    packaging/*) echo packaging ;;
    runtime/catalog.json) echo runtime-catalog ;;
    runtime/contract.json) echo runtime-contract ;;
    runtime/*) echo runtime-build ;;
    *) echo script ;;
  esac
}

write_metadata() {
  local root="$1"
  local commit dirty built_at host_os host_arch guest_arch archive_name catalog_sha runtime_revision runtime_artifact
  commit="$(git -C "$source" rev-parse HEAD 2>/dev/null || printf 'unknown')"
  dirty=false
  if git -C "$source" rev-parse --is-inside-work-tree >/dev/null 2>&1 &&
    [ -n "$(git -C "$source" status --porcelain --untracked-files=normal)" ]; then
    dirty=true
  fi
  built_at="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
  host_os="$(go env GOOS)"
  host_arch="$(go env GOARCH)"
  guest_arch="$host_arch"
  archive_name="hideout-v$version-$host_os-$host_arch.tar.gz"
  catalog_sha="$(sha256_file "$root/hideout/runtime/catalog.json")"
  runtime_revision="$(jq -er --arg os "$host_os" --arg arch "$host_arch" '
    .families[] | select(.id == "developer-standard") | .currentRevision
  ' "$root/hideout/runtime/catalog.json")"
  runtime_artifact="$(jq -er --arg os "$host_os" --arg arch "$host_arch" '
    .families[] | select(.id == "developer-standard") as $family |
    $family.revisions[] | select(.id == $family.currentRevision) |
    .artifacts[] | select(.hostOS == $os and .hostArch == $arch) | .sha256
  ' "$root/hideout/runtime/catalog.json")"
  jq -n \
    --arg version "$version" --arg channel "$channel" --arg tag "$tag" \
    --arg repository "https://github.com/vibe-agi/hideout" \
    --arg commit "$commit" --argjson dirty "$dirty" --arg builtAt "$built_at" \
    --arg workflow "$workflow" --arg ref "$ref" --arg signingMode "$signing_mode" \
    --arg hostOS "$host_os" --arg hostArch "$host_arch" --arg guestArch "$guest_arch" \
    --arg archiveName "$archive_name" \
    --arg runtimeRevision "$runtime_revision" --arg catalogSHA256 "$catalog_sha" \
    --arg runtimeArtifactSHA256 "$runtime_artifact" \
    '{version:$version,channel:$channel,tag:$tag,repository:$repository,
      commit:$commit,dirty:$dirty,builtAt:$builtAt,workflow:$workflow,ref:$ref,
      signingMode:$signingMode,hostOS:$hostOS,hostArch:$hostArch,guestArch:$guestArch,
      archiveName:$archiveName,
      runtimeFamily:"developer-standard",runtimeRevision:$runtimeRevision,
      catalogSHA256:$catalogSHA256,runtimeArtifactSHA256:$runtimeArtifactSHA256}' \
    >"$root/.package-build.json"
}

stage_package() {
  local root="$1" prefix="$1/hideout" archive_name
  if [ -e "$root" ]; then
    echo "package-local: stage path already exists: $root" >&2
    exit 1
  fi
  mkdir -p "$prefix"
  HIDEOUT_VERSION="$version" "$source/scripts/install-local.sh" \
    --prefix "$prefix" --store "$root/.store" --source "$source" --skip-init >/dev/null
  rm -rf "$root/.store"
  install -m 0755 "$source/packaging/install-package.sh" "$prefix/install.sh"
  for file in README.md README.zh-CN.md CHANGELOG.md LICENSE THIRD_PARTY_NOTICES.md SECURITY.md; do
    install -m 0644 "$source/$file" "$prefix/$file"
  done
  mkdir -p "$prefix/third_party/tun2socks"
  install -m 0644 "$source/third_party/tun2socks/LICENSE" \
    "$prefix/third_party/tun2socks/LICENSE"
  cp -R "$source/schemas" "$prefix/schemas"
  cp -R "$source/docs" "$prefix/docs"
  mkdir -p "$prefix/host-app" "$prefix/examples" "$prefix/packaging" "$prefix/runtime"
  cp -R "$source/internal/hostcap/recipes" "$prefix/host-app/recipes"
  cp -R "$source/examples/host-app-packs" "$prefix/examples/host-app-packs"
  cp -R "$source/packaging/homebrew" "$prefix/packaging/homebrew"
  install -m 0644 "$source/internal/runtimecatalog/catalog.json" "$prefix/runtime/catalog.json"
  install -m 0644 "$source/internal/runtimecatalog/contract.json" "$prefix/runtime/contract.json"
  cp -R "$source/runtime/developer-standard" "$prefix/runtime/developer-standard"
  archive_name="hideout-v$version-$(go env GOOS)-$(go env GOARCH).tar.gz"
  "$source/scripts/render-package-release-docs.sh" \
    --package-root "$prefix" --version "$version" --tag "$tag" \
    --channel "$channel" --archive "$archive_name" >/dev/null
  write_metadata "$root"
  printf '%s\n' "$root"
}

finalize_package() {
  local root="$1" archive="$2" prefix="$1/hideout" metadata="$1/.package-build.json"
  if [ ! -d "$prefix" ] || [ ! -f "$metadata" ]; then
    echo "package-local: invalid staged package: $root" >&2
    exit 1
  fi
  if [ -e "$prefix/package-manifest.json" ]; then
    echo "package-local: staged package is already finalized" >&2
    exit 1
  fi
  if [ "$(jq -er '.channel' "$metadata")" = "alpha" ] &&
    [ "$(basename "$archive")" != "$(jq -er '.archiveName' "$metadata")" ]; then
    echo "package-local: alpha archive name does not match staged candidate identity" >&2
    exit 1
  fi

  local guest_arch
  guest_arch="$(jq -er '.guestArch' "$metadata")"
  verify_linux_helper "$prefix" "$guest_arch" "hideout-session-supervisor"
  verify_linux_helper "$prefix" "$guest_arch" "hideout-workspace-portal"
  verify_tun2socks_helper "$prefix" "$guest_arch"

  local files_ndjson="$root/.files.ndjson"
  : >"$files_ndjson"
  while IFS= read -r rel; do
    local kind executable=false
    kind="$(package_kind "$rel")"
    if [ -x "$prefix/$rel" ]; then executable=true; fi
    jq -nc --arg path "$rel" --arg kind "$kind" \
      --arg sha256 "$(sha256_file "$prefix/$rel")" --argjson executable "$executable" \
      '{path:$path,kind:$kind,sha256:$sha256,executable:$executable}' >>"$files_ndjson"
  done < <(cd "$prefix" && find . -type f ! -name package-manifest.json -print | sed 's#^./##' | LC_ALL=C sort)

  local files_json="$root/.files.json"
  jq -s '.' "$files_ndjson" >"$files_json"
  jq -n --slurpfile meta "$metadata" --slurpfile files "$files_json" '
    ($meta[0]) as $m |
    {
      schema:"hideout.package-manifest/v1",
      builtAt:$m.builtAt,
      release:{productVersion:$m.version,channel:$m.channel,tag:$m.tag},
      source:{repository:$m.repository,commit:$m.commit,dirty:$m.dirty},
      build:{workflow:$m.workflow,ref:$m.ref},
      target:{hostOS:$m.hostOS,hostArch:$m.hostArch,linuxGuestArch:$m.guestArch},
      runtime:{family:$m.runtimeFamily,revision:$m.runtimeRevision,
        catalogFileSHA256:$m.catalogSHA256,artifactSHA256:$m.runtimeArtifactSHA256},
      signingSummary:{mode:$m.signingMode},
      layout:{root:"hideout",
        binaries:([$files[0][] | select(.kind == "binary" or .kind == "linux-helper") | .path] | sort),
        entrypoints:["install.sh","README.md","README.zh-CN.md","CHANGELOG.md","RELEASE_NOTES.md"],
        directories:["schemas","docs","host-app","examples","packaging","runtime","third_party"]},
      files:$files[0],
      migration:{installStateSchema:"hideout.package-install-state/v1",
        fromInstalledSchemas:["hideout.package-install-state/v1"],
        minimumPackageSchema:"hideout.package-manifest/v1",
        maximumPackageSchema:"hideout.package-manifest/v1"}
    }' >"$prefix/package-manifest.json"

  go -C "$source" run ./cmd/hideout-schema-validate \
    "$source/schemas/package-manifest.schema.json" "$prefix/package-manifest.json" >/dev/null
  "$prefix/bin/hideout" package verify "$prefix" >/dev/null
  mkdir -p "$(dirname "$archive")"
  COPYFILE_DISABLE=1 tar -C "$root" -czf "$archive" hideout
  printf '%s\n' "$archive"
}

if [ -n "$stage" ] && [ -n "$finalize" ]; then
  echo "package-local: --stage and --finalize are mutually exclusive" >&2
  exit 2
fi

if [ -n "$stage" ]; then
  stage="$(absolute_path "$stage")"
  stage_package "$stage"
  exit 0
fi

if [ -n "$finalize" ]; then
  if [ -z "$out" ]; then
    echo "package-local: --finalize requires --out" >&2
    exit 2
  fi
  finalize="$(cd "$finalize" && pwd -P)"
  out="$(absolute_path "$out")"
  if [ "$signing_mode_set" -eq 1 ]; then
    tmp_meta="$(mktemp "${TMPDIR:-/tmp}/hideout-package-meta.XXXXXX")"
    jq --arg mode "$signing_mode" '.signingMode=$mode' "$finalize/.package-build.json" >"$tmp_meta"
    mv "$tmp_meta" "$finalize/.package-build.json"
  fi
  finalize_package "$finalize" "$out"
  exit 0
fi

tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-package.XXXXXX")"
cleanup() { rm -rf "$tmp"; }
trap cleanup EXIT
if [ -z "$out" ]; then
  out="$source/dist/hideout-v$version-$(go env GOOS)-$(go env GOARCH).tar.gz"
else
  out="$(absolute_path "$out")"
fi
stage_package "$tmp/stage" >/dev/null
finalize_package "$tmp/stage" "$out"
