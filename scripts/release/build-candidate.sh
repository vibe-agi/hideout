#!/usr/bin/env bash
set -euo pipefail

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd -P)"
cd "$repo_root"
source "$repo_root/scripts/lib/reproducible-package.sh"

umask 077
export LC_ALL=C
export TZ=UTC

tmp_base="${TMPDIR:-/tmp}"
tmp_base="${tmp_base%/}"
out="$repo_root/.artifacts/045/package"
version="${HIDEOUT_CANDIDATE_VERSION:-0.1.0-alpha.4}"
preflight_only=0

usage() {
  printf '%s\n' \
    "Usage: scripts/release/build-candidate.sh [--preflight] [--out DIR]" \
    "                                                [--version VERSION]" \
    "" \
    "Builds the exact clean, unsigned local release candidate twice with" \
    "independent Go build caches. It accepts only byte-identical archives and" \
    "verifies every package, binary, helper, schema, embedded browser asset," \
    "runtime, manifest, and vulnerability binding. Evidence stays local." \
    "" \
    "This command never creates a commit, tag, remote release, or Homebrew" \
    "publication. A dirty source tree is a hard failure in full mode."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --preflight)
      preflight_only=1
      shift
      ;;
    --out)
      if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
        printf 'build-candidate: --out requires a directory\n' >&2
        exit 2
      fi
      out="$2"
      shift 2
      ;;
    --version)
      if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
        printf 'build-candidate: --version requires a value\n' >&2
        exit 2
      fi
      version="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'build-candidate: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ ! "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*$ ]]; then
  printf 'build-candidate: version must be a prerelease semantic version\n' >&2
  exit 2
fi

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    printf 'build-candidate: missing required command: %s\n' "$1" >&2
    return 1
  }
}

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
    return
  fi
  sha256sum "$1" | awk '{print $1}'
}

file_mode() {
  stat -f '%Lp' "$1" 2>/dev/null ||
    stat -c '%a' "$1" 2>/dev/null
}

file_bytes() {
  stat -f '%z' "$1" 2>/dev/null ||
    stat -c '%s' "$1" 2>/dev/null
}

safe_relative_path() {
  case "$1" in
    "" | /* | . | .. | ../* | */.. | */../* | *$'\n'* | *$'\t'*)
      return 1
      ;;
  esac
}

validate_summary() {
  local summary_path="$1" expected_commit="$2"
  local expected_archive_sha="$3" expected_manifest_sha="$4"
  jq -e \
    --arg commit "$expected_commit" \
    --arg archiveSHA256 "$expected_archive_sha" \
    --arg manifestSHA256 "$expected_manifest_sha" '
      .schema == "hideout.release-package-candidate/v1" and
      .result == "passed" and
      .source.commit == $commit and
      .source.dirty == false and
      .source.stableAcrossRun == true and
      .candidate.acceptance == true and
      .candidate.archiveSHA256 == $archiveSHA256 and
      .candidate.packageManifestSHA256 == $manifestSHA256 and
      .candidate.publicationStatus == "local-only" and
      .reproducibility.independentBuildCaches == true and
      .reproducibility.archiveBytesIdentical == true and
      .reproducibility.packageManifestBytesIdentical == true and
      .reproducibility.packageTreeInventoryIdentical == true and
      .validation.exactFileSet == true and
      .validation.allManifestDigests == true and
      .validation.allGoBinaries == true and
      .validation.helperProvenance == true and
      .validation.schemas == true and
      .validation.embeddedBrowserConsole == true and
      .validation.runtimeBinding == true and
      .validation.packageVerification == true and
      .validation.binaryVulnerabilityScan == true and
      (.inventory.binaryCount | type) == "number" and
      .inventory.binaryCount > 0 and
      (.inventory.helperManifestCount | type) == "number" and
      .inventory.helperManifestCount > 0 and
      (.inventory.schemaCount | type) == "number" and
      .inventory.schemaCount > 0 and
      (.inventory.browserAssetCount | type) == "number" and
      .inventory.browserAssetCount > 0 and
      (.artifacts | length) > 0 and
      all(.artifacts[];
        (.path | type) == "string" and
        (.path | length) > 0 and
        (.sha256 | test("^[a-f0-9]{64}$")) and
        (.bytes | type) == "number" and
        .bytes >= 0 and
        .bytes == (.bytes | floor) and
        .mode == "0600")
    ' "$summary_path" >/dev/null
}

validate_runtime_binding() {
  local manifest="$1" catalog="$2" contract="$3"
  local catalog_sha contract_sha artifact_sha revision
  catalog_sha="$(sha256_file "$catalog")"
  contract_sha="$(sha256_file "$contract")"
  artifact_sha="$(jq -er '.runtime.artifactSHA256' "$manifest")"
  revision="$(jq -er '.runtime.revision' "$manifest")"
  [ "$(jq -er '.runtime.catalogFileSHA256' "$manifest")" = "$catalog_sha" ] &&
    jq -e \
      --arg contractSHA256 "$contract_sha" \
      --arg artifactSHA256 "$artifact_sha" \
      --arg revision "$revision" '
        .families
        | map(select(.id == "developer-standard"))
        | length == 1 and
          .[0].currentRevision == $revision and
          (
            .[0].revisions
            | map(select(.id == $revision))
            | length == 1 and
              .[0].contractDigest == ("sha256:" + $contractSHA256) and
              (
                .[0].artifacts
                | map(select(
                    .hostOS == "darwin" and
                    .hostArch == "arm64" and
                    .sha256 == $artifactSHA256
                  ))
                | length
              ) == 1
          )
      ' "$catalog" >/dev/null
}

validate_artifact_manifest() {
  local evidence_root="$1" run_directory="$2" summary_path="$3"
  local expected_list actual_list path digest bytes mode actual_mode
  expected_list="$(mktemp "$tmp_base/hideout-artifacts-expected.XXXXXX")"
  actual_list="$(mktemp "$tmp_base/hideout-artifacts-actual.XXXXXX")"
  jq -r '.artifacts[].path' "$summary_path" | LC_ALL=C sort \
    >"$expected_list"
  (
    cd "$evidence_root"
    find "$(basename "$run_directory")" \
      -type f \
      -print |
      awk -v omit="$(basename "$run_directory")/summary.json" \
        '$0 != omit' |
      LC_ALL=C sort
  ) >"$actual_list"
  if ! cmp -s "$expected_list" "$actual_list"; then
    printf 'build-candidate: evidence artifact set is not exact\n' >&2
    diff -u "$expected_list" "$actual_list" >&2 || true
    find "$expected_list" "$actual_list" -depth -delete
    return 1
  fi
  while IFS=$'\t' read -r path digest bytes mode; do
    safe_relative_path "$path" || {
      find "$expected_list" "$actual_list" -depth -delete
      return 1
    }
    if [ ! -f "$evidence_root/$path" ] ||
      [ -L "$evidence_root/$path" ] ||
      [ "$(sha256_file "$evidence_root/$path")" != "$digest" ] ||
      [ "$(file_bytes "$evidence_root/$path")" -ne "$bytes" ]; then
      printf 'build-candidate: evidence artifact drifted: %s\n' \
        "$path" >&2
      find "$expected_list" "$actual_list" -depth -delete
      return 1
    fi
    actual_mode="$(file_mode "$evidence_root/$path")"
    if [ "$mode" != "0600" ] || [ "$actual_mode" != "600" ]; then
      printf 'build-candidate: evidence artifact mode drifted: %s\n' \
        "$path" >&2
      find "$expected_list" "$actual_list" -depth -delete
      return 1
    fi
  done < <(
    jq -r \
      '.artifacts[] | [.path,.sha256,(.bytes|tostring),.mode] | @tsv' \
      "$summary_path"
  )
  find "$expected_list" "$actual_list" -depth -delete
}

cleanup_tree() {
  local path="${1-}" prefix="${2-}"
  case "$path" in
    "$tmp_base"/"$prefix".*)
      [ ! -e "$path" ] || find "$path" -depth -delete
      ;;
    *)
      printf 'build-candidate: refusing unexpected cleanup target: %s\n' \
        "$path" >&2
      return 1
      ;;
  esac
}

for command in awk bash cmp date diff find git go gzip jq mktemp sed \
  shasum stat tar; do
  require_command "$command"
done

if [ "$preflight_only" -eq 1 ]; then
  preflight_root="$(
    mktemp -d "$tmp_base/hideout-build-candidate-preflight.XXXXXX"
  )"
  cleanup_preflight() {
    cleanup_tree \
      "${preflight_root:-}" \
      "hideout-build-candidate-preflight"
  }
  trap cleanup_preflight EXIT

  for build in a b; do
    mkdir -p "$preflight_root/$build/hideout/bin"
    printf 'candidate fixture\n' \
      >"$preflight_root/$build/hideout/bin/hideout"
    chmod 0755 "$preflight_root/$build/hideout/bin/hideout"
    hideout_create_reproducible_tar_gz \
      "$preflight_root/$build" \
      hideout \
      "$preflight_root/$build.tar.gz" \
      1785456000
  done
  if ! cmp -s "$preflight_root/a.tar.gz" "$preflight_root/b.tar.gz"; then
    printf 'build-candidate: reproducible archive preflight failed\n' >&2
    exit 1
  fi

  printf 'mutated fixture\n' \
    >"$preflight_root/b/hideout/bin/hideout"
  hideout_create_reproducible_tar_gz \
    "$preflight_root/b" \
    hideout \
    "$preflight_root/b-mutated.tar.gz" \
    1785456000
  if cmp -s \
    "$preflight_root/a.tar.gz" \
    "$preflight_root/b-mutated.tar.gz"; then
    printf 'build-candidate: archive mutation was not detected\n' >&2
    exit 1
  fi

  mkdir -p "$preflight_root/unsafe/hideout"
  ln -s outside "$preflight_root/unsafe/hideout/link"
  if hideout_create_reproducible_tar_gz \
    "$preflight_root/unsafe" \
    hideout \
    "$preflight_root/unsafe.tar.gz" \
    1785456000 >/dev/null 2>&1; then
    printf 'build-candidate: unsafe archive entry was accepted\n' >&2
    exit 1
  fi

  fixture_summary="$preflight_root/summary.json"
  fixture_archive_sha="$(sha256_file "$preflight_root/a.tar.gz")"
  fixture_manifest_sha="$(printf manifest | shasum -a 256 | awk '{print $1}')"
  jq -n \
    --arg commit "0123456789abcdef0123456789abcdef01234567" \
    --arg archiveSHA256 "$fixture_archive_sha" \
    --arg manifestSHA256 "$fixture_manifest_sha" '
      {
        schema:"hideout.release-package-candidate/v1",
        result:"passed",
        source:{commit:$commit,dirty:false,stableAcrossRun:true},
        candidate:{
          acceptance:true,
          archiveSHA256:$archiveSHA256,
          packageManifestSHA256:$manifestSHA256,
          publicationStatus:"local-only"
        },
        reproducibility:{
          independentBuildCaches:true,
          archiveBytesIdentical:true,
          packageManifestBytesIdentical:true,
          packageTreeInventoryIdentical:true
        },
        validation:{
          exactFileSet:true,
          allManifestDigests:true,
          allGoBinaries:true,
          helperProvenance:true,
          schemas:true,
          embeddedBrowserConsole:true,
          runtimeBinding:true,
          packageVerification:true,
          binaryVulnerabilityScan:true
        },
        inventory:{
          binaryCount:1,
          helperManifestCount:1,
          schemaCount:1,
          browserAssetCount:1
        },
        artifacts:[{
          path:"candidate.tar.gz",
          sha256:$archiveSHA256,
          bytes:0,
          mode:"0600"
        }]
      }
    ' >"$fixture_summary"
  validate_summary \
    "$fixture_summary" \
    "0123456789abcdef0123456789abcdef01234567" \
    "$fixture_archive_sha" \
    "$fixture_manifest_sha"
  jq '.source.dirty = true' \
    "$fixture_summary" >"$preflight_root/summary-dirty.json"
  if validate_summary \
    "$preflight_root/summary-dirty.json" \
    "0123456789abcdef0123456789abcdef01234567" \
    "$fixture_archive_sha" \
    "$fixture_manifest_sha"; then
    printf 'build-candidate: dirty summary fixture was accepted\n' >&2
    exit 1
  fi
  jq '.reproducibility.archiveBytesIdentical = false' \
    "$fixture_summary" >"$preflight_root/summary-reproducibility.json"
  if validate_summary \
    "$preflight_root/summary-reproducibility.json" \
    "0123456789abcdef0123456789abcdef01234567" \
    "$fixture_archive_sha" \
    "$fixture_manifest_sha"; then
    printf 'build-candidate: non-reproducible fixture was accepted\n' >&2
    exit 1
  fi

  printf '{"schema":"fixture"}\n' >"$preflight_root/runtime-contract.json"
  fixture_contract_sha="$(
    sha256_file "$preflight_root/runtime-contract.json"
  )"
  fixture_artifact_sha="$(
    printf runtime-artifact | shasum -a 256 | awk '{print $1}'
  )"
  jq -n \
    --arg contractSHA256 "$fixture_contract_sha" \
    --arg artifactSHA256 "$fixture_artifact_sha" '
      {
        families:[{
          id:"developer-standard",
          currentRevision:"fixture-r1",
          revisions:[{
            id:"fixture-r1",
            contractDigest:("sha256:" + $contractSHA256),
            artifacts:[{
              hostOS:"darwin",
              hostArch:"arm64",
              sha256:$artifactSHA256
            }]
          }]
        }]
      }
    ' >"$preflight_root/runtime-catalog.json"
  fixture_catalog_sha="$(
    sha256_file "$preflight_root/runtime-catalog.json"
  )"
  jq -n \
    --arg catalogSHA256 "$fixture_catalog_sha" \
    --arg artifactSHA256 "$fixture_artifact_sha" '
      {
        runtime:{
          revision:"fixture-r1",
          catalogFileSHA256:$catalogSHA256,
          artifactSHA256:$artifactSHA256
        }
      }
    ' >"$preflight_root/runtime-manifest.json"
  validate_runtime_binding \
    "$preflight_root/runtime-manifest.json" \
    "$preflight_root/runtime-catalog.json" \
    "$preflight_root/runtime-contract.json"
  jq '.runtime.catalogFileSHA256 = ("0" * 64)' \
    "$preflight_root/runtime-manifest.json" \
    >"$preflight_root/runtime-manifest-negative.json"
  if validate_runtime_binding \
    "$preflight_root/runtime-manifest-negative.json" \
    "$preflight_root/runtime-catalog.json" \
    "$preflight_root/runtime-contract.json"; then
    printf 'build-candidate: runtime digest mismatch was accepted\n' >&2
    exit 1
  fi

  fixture_evidence="$preflight_root/evidence"
  fixture_run="$fixture_evidence/run-fixture"
  mkdir -p "$fixture_run/vulnerability"
  printf 'candidate bytes\n' >"$fixture_run/candidate.tar.gz"
  printf '{"result":"passed"}\n' \
    >"$fixture_run/vulnerability/summary.json"
  chmod 0600 \
    "$fixture_run/candidate.tar.gz" \
    "$fixture_run/vulnerability/summary.json"
  jq -n \
    --arg candidateSHA256 "$(
      sha256_file "$fixture_run/candidate.tar.gz"
    )" \
    --argjson candidateBytes "$(
      file_bytes "$fixture_run/candidate.tar.gz"
    )" \
    --arg vulnerabilitySHA256 "$(
      sha256_file "$fixture_run/vulnerability/summary.json"
    )" \
    --argjson vulnerabilityBytes "$(
      file_bytes "$fixture_run/vulnerability/summary.json"
    )" '
      {
        artifacts:[
          {
            path:"run-fixture/candidate.tar.gz",
            sha256:$candidateSHA256,
            bytes:$candidateBytes,
            mode:"0600"
          },
          {
            path:"run-fixture/vulnerability/summary.json",
            sha256:$vulnerabilitySHA256,
            bytes:$vulnerabilityBytes,
            mode:"0600"
          }
        ]
      }
    ' >"$fixture_run/summary.json"
  chmod 0600 "$fixture_run/summary.json"
  validate_artifact_manifest \
    "$fixture_evidence" \
    "$fixture_run" \
    "$fixture_run/summary.json"
  printf 'drift\n' >>"$fixture_run/candidate.tar.gz"
  if validate_artifact_manifest \
    "$fixture_evidence" \
    "$fixture_run" \
    "$fixture_run/summary.json" >/dev/null 2>&1; then
    printf 'build-candidate: evidence artifact drift was accepted\n' >&2
    exit 1
  fi

  bash -n \
    scripts/lib/reproducible-package.sh \
    scripts/install-local.sh \
    scripts/package-local.sh \
    scripts/release/build-candidate.sh
  go test ./internal/helperbin \
    -run 'TestHelperManifest(SourceDateEpochIsDeterministic|RejectsInvalidSourceDateEpoch)' \
    -count=1 >/dev/null
  printf 'build-candidate: preflight=passed\n'
  exit 0
fi

if [ "$(uname -s)" != "Darwin" ] || [ "$(uname -m)" != "arm64" ]; then
  printf 'build-candidate: full gate requires Darwin/arm64\n' >&2
  exit 1
fi

required_go_version="go$(awk '$1 == "go" {print $2; exit}' go.mod)"
actual_go_version="$(GOWORK=off go env GOVERSION)"
if [ "$actual_go_version" != "$required_go_version" ]; then
  printf 'build-candidate: Go version is %s, want %s\n' \
    "$actual_go_version" "$required_go_version" >&2
  exit 1
fi

source_status="$(git status --porcelain=v1 --untracked-files=all)"
if [ -n "$source_status" ]; then
  printf '%s\n' \
    "build-candidate: exact candidate requires a completely clean source tree" \
    "$source_status" >&2
  exit 1
fi
source_commit="$(git rev-parse --verify HEAD)"
source_tree="$(git rev-parse --verify HEAD^{tree})"
if [[ ! "$source_commit" =~ ^[a-f0-9]{40}$ ]] ||
  [[ ! "$source_tree" =~ ^[a-f0-9]{40}$ ]]; then
  printf 'build-candidate: source commit/tree identity is invalid\n' >&2
  exit 1
fi
source_epoch="$(git show -s --format=%ct "$source_commit")"
hideout_validate_source_date_epoch "$source_epoch"
candidate_built_at="$(hideout_timestamp_from_epoch "$source_epoch")"

submodule_status="$(git submodule status --recursive)"
if printf '%s\n' "$submodule_status" | grep -Eq '^[+-U]'; then
  printf 'build-candidate: submodule state is not exact\n' >&2
  exit 1
fi

scratch="$(
  mktemp -d "$tmp_base/hideout-build-candidate.XXXXXX"
)"
cleanup() {
  cleanup_tree "${scratch:-}" "hideout-build-candidate"
}
trap cleanup EXIT

source_manifest() {
  local destination="$1" path
  : >"$destination"
  while IFS= read -r -d '' path; do
    if [ -L "$path" ] || [ ! -f "$path" ]; then
      printf 'build-candidate: tracked source entry is unsafe: %s\n' \
        "$path" >&2
      return 1
    fi
    printf '%s\t%s\t%s\t%s\n' \
      "$(file_mode "$path")" \
      "$(file_bytes "$path")" \
      "$(sha256_file "$path")" \
      "$path" >>"$destination"
  done < <(git ls-files -z)
}

source_manifest "$scratch/source-before.tsv"
source_manifest_sha="$(sha256_file "$scratch/source-before.tsv")"
source_file_count="$(awk 'END {print NR + 0}' "$scratch/source-before.tsv")"

export SOURCE_DATE_EPOCH="$source_epoch"
export GOWORK=off
export GOFLAGS="-mod=readonly -buildvcs=true"

archive_name="hideout-v$version-darwin-arm64.tar.gz"
workflow_identity="scripts/release/build-candidate.sh"
ref_identity="commit:$source_commit"

for build in a b; do
  mkdir -p "$scratch/cache-$build"
  export GOCACHE="$scratch/cache-$build"
  scripts/package-local.sh \
    --source "$repo_root" \
    --out "$scratch/candidate-$build.tar.gz" \
    --version "$version" \
    --channel developer-preview \
    --tag "v$version" \
    --workflow "$workflow_identity" \
    --ref "$ref_identity" \
    --signing-mode developer-preview-unsigned \
    >"$scratch/package-$build.log" 2>&1
  mkdir -p "$scratch/extracted-$build"
  tar -xzf "$scratch/candidate-$build.tar.gz" \
    -C "$scratch/extracted-$build"
done

if ! cmp -s \
  "$scratch/candidate-a.tar.gz" \
  "$scratch/candidate-b.tar.gz"; then
  printf 'build-candidate: independent package archives differ\n' >&2
  exit 1
fi

compare_regular_tree() {
  local source_root="$1" package_root="$2" label="$3"
  local source_list="$scratch/$label-source-files.txt"
  local package_list="$scratch/$label-package-files.txt"
  local relative
  if [ -L "$source_root" ] || [ -L "$package_root" ] ||
    [ ! -d "$source_root" ] || [ ! -d "$package_root" ]; then
    printf 'build-candidate: %s tree is missing or unsafe\n' "$label" >&2
    return 1
  fi
  (
    cd "$source_root"
    find . -type f -print | sed 's#^\./##' | LC_ALL=C sort
  ) >"$source_list"
  (
    cd "$package_root"
    find . -type f -print | sed 's#^\./##' | LC_ALL=C sort
  ) >"$package_list"
  if ! cmp -s "$source_list" "$package_list"; then
    printf 'build-candidate: packaged %s file set drifted\n' "$label" >&2
    diff -u "$source_list" "$package_list" >&2 || true
    return 1
  fi
  while IFS= read -r relative; do
    safe_relative_path "$relative" || return 1
    if ! cmp -s \
      "$source_root/$relative" \
      "$package_root/$relative"; then
      printf 'build-candidate: packaged %s content drifted: %s\n' \
        "$label" "$relative" >&2
      return 1
    fi
  done <"$source_list"
}

verify_package() {
  local build="$1" package_root="$scratch/extracted-$build/hideout"
  local manifest="$package_root/package-manifest.json"
  local prefix="$scratch/verify-$build"
  local expected_binaries expected_files actual_files
  local path digest executable kind actual_digest binary_count
  local helper_count schema_count browser_count
  local binary_path observer_manifest tun_manifest catalog_sha contract_sha

  if [ ! -d "$package_root" ] || [ -L "$package_root" ] ||
    [ ! -f "$manifest" ] || [ -L "$manifest" ]; then
    printf 'build-candidate: build %s package root is unsafe\n' "$build" >&2
    return 1
  fi

  expected_binaries="$(
    jq -nc --arg arch "arm64" '[
      "bin/hideout",
      "bin/hideout-shim",
      ("bin/hideout-dns-stub-linux-" + $arch),
      ("bin/hideout-hostfsd-linux-" + $arch),
      ("bin/hideout-observer-linux-" + $arch),
      ("bin/hideout-session-supervisor-linux-" + $arch),
      ("bin/hideout-shim-linux-" + $arch),
      ("bin/hideout-workspace-portal-linux-" + $arch),
      ("bin/tun2socks-linux-" + $arch)
    ] | sort'
  )"
  if ! jq -e \
    --arg commit "$source_commit" \
    --arg version "$version" \
    --arg builtAt "$candidate_built_at" \
    --arg workflow "$workflow_identity" \
    --arg ref "$ref_identity" \
    --argjson binaries "$expected_binaries" '
      .schema == "hideout.package-manifest/v1" and
      .builtAt == $builtAt and
      .release == {
        productVersion:$version,
        channel:"developer-preview",
        tag:("v" + $version)
      } and
      .source == {
        repository:"https://github.com/vibe-agi/hideout",
        commit:$commit,
        dirty:false
      } and
      .build == {workflow:$workflow,ref:$ref} and
      .target == {
        hostOS:"darwin",
        hostArch:"arm64",
        linuxGuestArch:"arm64"
      } and
      .signingSummary.mode == "developer-preview-unsigned" and
      .layout.root == "hideout" and
      (.layout.binaries | sort) == $binaries and
      ([.files[].path] == ([.files[].path] | sort)) and
      ([.files[].path] | unique | length) == (.files | length) and
      all(.files[];
        (.path | test("^[A-Za-z0-9._/+ -]+$")) and
        (.path | startswith("/") | not) and
        (.path | contains("..") | not) and
        (.kind | type) == "string" and
        (.sha256 | test("^[a-f0-9]{64}$")) and
        (.executable | type) == "boolean")
    ' "$manifest" >/dev/null; then
    printf 'build-candidate: build %s manifest identity is invalid\n' \
      "$build" >&2
    return 1
  fi

  if find "$package_root" -type l -print -quit | grep -q . ||
    find "$package_root" ! -type f ! -type d -print -quit | grep -q .; then
    printf 'build-candidate: build %s contains an unsafe file type\n' \
      "$build" >&2
    return 1
  fi

  expected_files="$prefix-expected-files.txt"
  actual_files="$prefix-actual-files.txt"
  jq -r '.files[].path' "$manifest" >"$expected_files"
  (
    cd "$package_root"
    find . -type f ! -name package-manifest.json -print |
      sed 's#^\./##' |
      LC_ALL=C sort
  ) >"$actual_files"
  if ! cmp -s "$expected_files" "$actual_files"; then
    printf 'build-candidate: build %s package file set is not exact\n' \
      "$build" >&2
    diff -u "$expected_files" "$actual_files" >&2 || true
    return 1
  fi

  : >"$prefix-files.sha256"
  : >"$prefix-binaries.sha256"
  : >"$prefix-helpers.sha256"
  : >"$prefix-schemas.sha256"
  : >"$prefix-runtime.sha256"
  while IFS=$'\t' read -r path digest executable kind; do
    safe_relative_path "$path" || {
      printf 'build-candidate: unsafe manifest path: %s\n' "$path" >&2
      return 1
    }
    if [ ! -f "$package_root/$path" ] ||
      [ -L "$package_root/$path" ]; then
      printf 'build-candidate: manifest file is missing or unsafe: %s\n' \
        "$path" >&2
      return 1
    fi
    actual_digest="$(sha256_file "$package_root/$path")"
    if [ "$actual_digest" != "$digest" ]; then
      printf 'build-candidate: manifest digest mismatch: %s\n' \
        "$path" >&2
      return 1
    fi
    if [ "$executable" = "true" ]; then
      [ -x "$package_root/$path" ] || {
        printf 'build-candidate: declared executable is not executable: %s\n' \
          "$path" >&2
        return 1
      }
    elif [ -x "$package_root/$path" ]; then
      printf 'build-candidate: undeclared executable file: %s\n' \
        "$path" >&2
      return 1
    fi
    printf '%s  %s\n' "$actual_digest" "$path" >>"$prefix-files.sha256"
    case "$kind" in
      binary | linux-helper)
        printf '%s  %s\n' "$actual_digest" "$path" \
          >>"$prefix-binaries.sha256"
        ;;
      helper-manifest)
        printf '%s  %s\n' "$actual_digest" "$path" \
          >>"$prefix-helpers.sha256"
        ;;
      schema)
        printf '%s  %s\n' "$actual_digest" "$path" \
          >>"$prefix-schemas.sha256"
        ;;
      runtime-catalog | runtime-contract | runtime-build)
        printf '%s  %s\n' "$actual_digest" "$path" \
          >>"$prefix-runtime.sha256"
        ;;
    esac
  done < <(
    jq -r '.files[] | [.path,.sha256,(.executable|tostring),.kind] | @tsv' \
      "$manifest"
  )

  "$package_root/bin/hideout" package verify "$package_root" \
    >"$prefix-package-verify.log"
  "$package_root/bin/hideout" version --json \
    >"$prefix-version.json"
  if ! jq -e \
    --arg version "$version" \
    --arg commit "$source_commit" \
    --arg builtAt "$candidate_built_at" \
    --arg goVersion "$required_go_version" '
      .productVersion == $version and
      .sourceCommit == $commit and
      .builtAt == $builtAt and
      .goVersion == $goVersion
    ' "$prefix-version.json" >/dev/null; then
    printf 'build-candidate: build %s binary identity is invalid\n' \
      "$build" >&2
    return 1
  fi

  binary_count=0
  : >"$prefix-go-version-m.txt"
  while IFS= read -r path; do
    safe_relative_path "$path" || return 1
    binary_count=$((binary_count + 1))
    {
      printf '%s\n' "### $path"
      go version -m "$package_root/$path"
    } >>"$prefix-go-version-m.txt"
    if [ "$(go version -m "$package_root/$path" |
      awk 'NR == 1 {print $NF}')" != "$required_go_version" ]; then
      printf 'build-candidate: build %s binary has wrong Go version: %s\n' \
        "$build" "$path" >&2
      return 1
    fi
  done < <(
    jq -r '
      .files[]
      | select(.kind == "binary" or .kind == "linux-helper")
      | .path
    ' "$manifest"
  )
  if [ "$binary_count" -ne 9 ]; then
    printf 'build-candidate: build %s binary count=%s want=9\n' \
      "$build" "$binary_count" >&2
    return 1
  fi

  helper_count=0
  while IFS= read -r path; do
    helper_count=$((helper_count + 1))
    binary_path="${path%.manifest.json}"
    if ! jq -e \
      --arg arch "arm64" \
      --arg artifact "$(basename "$binary_path")" \
      --arg binarySHA256 "$(sha256_file "$package_root/$binary_path")" \
      --arg builtAt "$candidate_built_at" '
        .version == "hideout.helper-manifest/v1" and
        .targetOS == "linux" and
        .targetArch == $arch and
        .artifact == $artifact and
        .sha256 == $binarySHA256 and
        .builtAt == $builtAt and
        (.builder | startswith("go build"))
      ' "$package_root/$path" >/dev/null; then
      printf 'build-candidate: build %s helper binding failed: %s\n' \
        "$build" "$path" >&2
      return 1
    fi
    go run ./cmd/hideout-schema-validate \
      schemas/helper-manifest.schema.json \
      "$package_root/$path" >/dev/null
  done < <(jq -r '.files[] | select(.kind == "helper-manifest") | .path' \
    "$manifest")
  if [ "$helper_count" -ne 6 ]; then
    printf 'build-candidate: build %s helper manifest count=%s want=6\n' \
      "$build" "$helper_count" >&2
    return 1
  fi

  observer_manifest="$package_root/bin/hideout-observer-linux-arm64.manifest.json"
  if ! jq -e '
    .command == "hideout-observer" and
    .builder == "go build -trimpath" and
    .license == "Apache-2.0" and
    .buildMode == "embedded-core-bpf" and
    .packageOwned == true
  ' "$observer_manifest" >/dev/null; then
    printf 'build-candidate: build %s observer provenance failed\n' \
      "$build" >&2
    return 1
  fi
  tun_manifest="$package_root/bin/tun2socks-linux-arm64.manifest.json"
  if ! jq -e '
    .command == "tun2socks" and
    .upstreamModule == "github.com/xjasonlyu/tun2socks/v2" and
    .upstreamVersion == "v2.6.0" and
    .license == "MIT" and
    .buildMode == "source-built-pinned-module" and
    .packageOwned == true
  ' "$tun_manifest" >/dev/null; then
    printf 'build-candidate: build %s tun2socks provenance failed\n' \
      "$build" >&2
    return 1
  fi

  go run ./cmd/hideout-schema-validate \
    schemas/package-manifest.schema.json "$manifest" >/dev/null
  go run ./cmd/hideout-schema-validate \
    schemas/package-components.schema.json \
    "$package_root/runtime/package-components.json" >/dev/null
  go run ./cmd/hideout-schema-validate \
    schemas/embedded-asset-manifest.schema.json \
    "$package_root/runtime/browser-console.assets.json" >/dev/null
  go run ./cmd/hideout-schema-validate \
    schemas/runtime-catalog.schema.json \
    "$package_root/runtime/catalog.json" >/dev/null
  compare_regular_tree \
    "$repo_root/schemas" \
    "$package_root/schemas" \
    "schemas-$build"
  schema_count="$(find "$package_root/schemas" -type f | wc -l |
    tr -d ' ')"

  "$package_root/bin/hideout" package embedded-assets \
    >"$prefix-browser-console.regenerated.json"
  if ! cmp -s \
    "$prefix-browser-console.regenerated.json" \
    "$package_root/runtime/browser-console.assets.json"; then
    printf 'build-candidate: build %s embedded UI manifest drifted\n' \
      "$build" >&2
    return 1
  fi
  browser_count="$(
    jq -er '.assets | length' \
      "$package_root/runtime/browser-console.assets.json"
  )"
  if [ "$browser_count" -ne 8 ]; then
    printf 'build-candidate: build %s browser asset count=%s want=8\n' \
      "$build" "$browser_count" >&2
    return 1
  fi
  if [ "$(
    jq -er '.containerSHA256' \
      "$package_root/runtime/browser-console.assets.json"
  )" != "$(sha256_file "$package_root/bin/hideout")" ]; then
    printf 'build-candidate: build %s browser container binding failed\n' \
      "$build" >&2
    return 1
  fi

  if ! cmp -s \
    internal/runtimecatalog/catalog.json \
    "$package_root/runtime/catalog.json" ||
    ! cmp -s \
      internal/runtimecatalog/contract.json \
      "$package_root/runtime/contract.json" ||
    ! cmp -s \
      runtime/package-components.json \
      "$package_root/runtime/package-components.json"; then
    printf 'build-candidate: build %s runtime contract files drifted\n' \
      "$build" >&2
    return 1
  fi
  compare_regular_tree \
    "$repo_root/runtime/developer-standard" \
    "$package_root/runtime/developer-standard" \
    "runtime-build-$build"
  catalog_sha="$(sha256_file "$package_root/runtime/catalog.json")"
  contract_sha="$(sha256_file "$package_root/runtime/contract.json")"
  if ! validate_runtime_binding \
    "$manifest" \
    "$package_root/runtime/catalog.json" \
    "$package_root/runtime/contract.json"; then
    printf 'build-candidate: build %s runtime digest binding failed\n' \
      "$build" >&2
    return 1
  fi

  jq -n \
    --arg build "$build" \
    --arg packageManifestSHA256 "$(sha256_file "$manifest")" \
    --arg fileInventorySHA256 "$(sha256_file "$prefix-files.sha256")" \
    --arg binaryInventorySHA256 \
      "$(sha256_file "$prefix-binaries.sha256")" \
    --arg helperInventorySHA256 \
      "$(sha256_file "$prefix-helpers.sha256")" \
    --arg schemaInventorySHA256 \
      "$(sha256_file "$prefix-schemas.sha256")" \
    --arg runtimeInventorySHA256 \
      "$(sha256_file "$prefix-runtime.sha256")" \
    --arg browserManifestSHA256 "$(
      sha256_file "$package_root/runtime/browser-console.assets.json"
    )" \
    --arg catalogSHA256 "$catalog_sha" \
    --arg contractSHA256 "$contract_sha" \
    --arg runtimeArtifactSHA256 "$(jq -er '.runtime.artifactSHA256' "$manifest")" \
    --argjson fileCount "$(jq '.files | length' "$manifest")" \
    --argjson binaryCount "$binary_count" \
    --argjson helperManifestCount "$helper_count" \
    --argjson schemaCount "$schema_count" \
    --argjson browserAssetCount "$browser_count" '
      {
        schema:"hideout.release-package-inventory/v1",
        build:$build,
        packageManifestSHA256:$packageManifestSHA256,
        inventories:{
          files:$fileInventorySHA256,
          binaries:$binaryInventorySHA256,
          helperManifests:$helperInventorySHA256,
          schemas:$schemaInventorySHA256,
          runtime:$runtimeInventorySHA256
        },
        embeddedBrowserManifestSHA256:$browserManifestSHA256,
        runtime:{
          catalogSHA256:$catalogSHA256,
          contractSHA256:$contractSHA256,
          artifactSHA256:$runtimeArtifactSHA256
        },
        counts:{
          files:$fileCount,
          binaries:$binaryCount,
          helperManifests:$helperManifestCount,
          schemas:$schemaCount,
          browserAssets:$browserAssetCount
        }
      }
    ' >"$prefix-inventory.json"
}

verify_package a
verify_package b

if ! cmp -s \
  "$scratch/extracted-a/hideout/package-manifest.json" \
  "$scratch/extracted-b/hideout/package-manifest.json"; then
  printf 'build-candidate: package manifests are not reproducible\n' >&2
  exit 1
fi
for inventory in files binaries helpers schemas runtime; do
  if ! cmp -s \
    "$scratch/verify-a-$inventory.sha256" \
    "$scratch/verify-b-$inventory.sha256"; then
    printf 'build-candidate: %s inventory is not reproducible\n' \
      "$inventory" >&2
    exit 1
  fi
done

mkdir -p "$scratch/evidence/vulnerability"
scripts/test-vulnerability-gate.sh \
  --package-root "$scratch/extracted-a/hideout" \
  --evidence-dir "$scratch/evidence/vulnerability" \
  >"$scratch/evidence/vulnerability.log" 2>&1
if ! jq -e '
  .schema == "hideout.vulnerability-gate-evidence/v1" and
  .result == "passed" and
  .scanner.scanLevel == "symbol" and
  .executed.packageBinaryCount == 9
' "$scratch/evidence/vulnerability/summary.json" >/dev/null; then
  printf 'build-candidate: binary vulnerability evidence is invalid\n' >&2
  exit 1
fi

source_manifest "$scratch/source-after.tsv"
if [ "$(git rev-parse --verify HEAD)" != "$source_commit" ] ||
  [ "$(git rev-parse --verify HEAD^{tree})" != "$source_tree" ] ||
  [ -n "$(git status --porcelain=v1 --untracked-files=all)" ] ||
  ! cmp -s "$scratch/source-before.tsv" "$scratch/source-after.tsv"; then
  printf 'build-candidate: source tree changed during candidate build\n' >&2
  exit 1
fi

cp "$scratch/candidate-a.tar.gz" \
  "$scratch/evidence/$archive_name"
cp "$scratch/extracted-a/hideout/package-manifest.json" \
  "$scratch/evidence/package-manifest.json"
cp "$scratch/extracted-b/hideout/package-manifest.json" \
  "$scratch/evidence/package-manifest-rebuild.json"
cp "$scratch/extracted-a/hideout/runtime/browser-console.assets.json" \
  "$scratch/evidence/browser-console.assets.json"
cp "$scratch/extracted-a/hideout/runtime/package-components.json" \
  "$scratch/evidence/package-components.json"
cp "$scratch/source-before.tsv" \
  "$scratch/evidence/source-manifest.tsv"
for build in a b; do
  cp "$scratch/package-$build.log" \
    "$scratch/evidence/package-$build.log"
  cp "$scratch/verify-$build-inventory.json" \
    "$scratch/evidence/inventory-$build.json"
  cp "$scratch/verify-$build-files.sha256" \
    "$scratch/evidence/files-$build.sha256"
  cp "$scratch/verify-$build-binaries.sha256" \
    "$scratch/evidence/binaries-$build.sha256"
  cp "$scratch/verify-$build-helpers.sha256" \
    "$scratch/evidence/helpers-$build.sha256"
  cp "$scratch/verify-$build-schemas.sha256" \
    "$scratch/evidence/schemas-$build.sha256"
  cp "$scratch/verify-$build-runtime.sha256" \
    "$scratch/evidence/runtime-$build.sha256"
  cp "$scratch/verify-$build-go-version-m.txt" \
    "$scratch/evidence/go-version-m-$build.txt"
  cp "$scratch/verify-$build-package-verify.log" \
    "$scratch/evidence/package-verify-$build.log"
  cp "$scratch/verify-$build-version.json" \
    "$scratch/evidence/version-$build.json"
done

archive_sha="$(sha256_file "$scratch/evidence/$archive_name")"
manifest_sha="$(
  sha256_file "$scratch/evidence/package-manifest.json"
)"
binary_count="$(
  jq -er '.counts.binaries' "$scratch/evidence/inventory-a.json"
)"
helper_count="$(
  jq -er '.counts.helperManifests' "$scratch/evidence/inventory-a.json"
)"
schema_count="$(
  jq -er '.counts.schemas' "$scratch/evidence/inventory-a.json"
)"
browser_count="$(
  jq -er '.counts.browserAssets' "$scratch/evidence/inventory-a.json"
)"

run_id="run-$(date -u +'%Y%m%dT%H%M%SZ')-$$"
if [ -L "$out" ]; then
  printf 'build-candidate: evidence root must not be a symlink\n' >&2
  exit 1
fi
mkdir -p "$out"
out="$(cd "$out" && pwd -P)"
chmod 0700 "$out"
run_dir="$out/$run_id"
if [ -e "$run_dir" ]; then
  printf 'build-candidate: evidence run already exists: %s\n' \
    "$run_dir" >&2
  exit 1
fi
mkdir "$run_dir"
cp -R "$scratch/evidence/." "$run_dir/"
find "$run_dir" -type d -exec chmod 0700 {} +
find "$run_dir" -type f -exec chmod 0600 {} +

artifact_lines="$scratch/artifacts.jsonl"
: >"$artifact_lines"
while IFS= read -r evidence_file; do
  relative="${evidence_file#"$out"/}"
  jq -nc \
    --arg path "$relative" \
    --arg sha256 "$(sha256_file "$evidence_file")" \
    --argjson bytes "$(file_bytes "$evidence_file")" '
      {path:$path,sha256:$sha256,bytes:$bytes,mode:"0600"}
    ' >>"$artifact_lines"
done < <(find "$run_dir" -type f | LC_ALL=C sort)
artifacts="$scratch/artifacts.json"
jq -s . "$artifact_lines" >"$artifacts"

summary="$run_dir/summary.json"
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$source_commit" \
  --arg tree "$source_tree" \
  --arg sourceManifestSHA256 "$source_manifest_sha" \
  --argjson sourceFiles "$source_file_count" \
  --arg version "$version" \
  --arg tag "v$version" \
  --arg builtAt "$candidate_built_at" \
  --arg archive "$run_id/$archive_name" \
  --arg archiveSHA256 "$archive_sha" \
  --arg packageManifest "$run_id/package-manifest.json" \
  --arg packageManifestSHA256 "$manifest_sha" \
  --arg runtimeCatalogSHA256 "$(
    jq -er '.runtime.catalogSHA256' "$run_dir/inventory-a.json"
  )" \
  --arg runtimeContractSHA256 "$(
    jq -er '.runtime.contractSHA256' "$run_dir/inventory-a.json"
  )" \
  --arg runtimeArtifactSHA256 "$(
    jq -er '.runtime.artifactSHA256' "$run_dir/inventory-a.json"
  )" \
  --argjson binaryCount "$binary_count" \
  --argjson helperManifestCount "$helper_count" \
  --argjson schemaCount "$schema_count" \
  --argjson browserAssetCount "$browser_count" \
  --argjson artifacts "$(cat "$artifacts")" '
    {
      schema:"hideout.release-package-candidate/v1",
      generatedAt:$generatedAt,
      result:"passed",
      source:{
        commit:$commit,
        tree:$tree,
        dirty:false,
        stableAcrossRun:true,
        manifestSHA256:$sourceManifestSHA256,
        files:$sourceFiles
      },
      candidate:{
        acceptance:true,
        version:$version,
        tag:$tag,
        builtAt:$builtAt,
        archive:$archive,
        archiveSHA256:$archiveSHA256,
        packageManifest:$packageManifest,
        packageManifestSHA256:$packageManifestSHA256,
        channel:"developer-preview",
        signingMode:"developer-preview-unsigned",
        publicationStatus:"local-only"
      },
      reproducibility:{
        sourceDateEpochFromCommit:true,
        independentBuildCaches:true,
        archiveBytesIdentical:true,
        packageManifestBytesIdentical:true,
        packageTreeInventoryIdentical:true
      },
      validation:{
        exactFileSet:true,
        allManifestDigests:true,
        allGoBinaries:true,
        helperProvenance:true,
        schemas:true,
        embeddedBrowserConsole:true,
        runtimeBinding:true,
        packageVerification:true,
        binaryVulnerabilityScan:true
      },
      inventory:{
        binaryCount:$binaryCount,
        helperManifestCount:$helperManifestCount,
        schemaCount:$schemaCount,
        browserAssetCount:$browserAssetCount,
        runtimeCatalogSHA256:$runtimeCatalogSHA256,
        runtimeContractSHA256:$runtimeContractSHA256,
        runtimeArtifactSHA256:$runtimeArtifactSHA256
      },
      artifacts:$artifacts,
      limitations:[
        "This is an unsigned local candidate. Developer ID signing, notarization, remote publication, and Homebrew publication require separate authorized workflows."
      ]
    }
  ' >"$summary"
chmod 0600 "$summary"

validate_summary \
  "$summary" \
  "$source_commit" \
  "$archive_sha" \
  "$manifest_sha" || {
  printf 'build-candidate: final summary semantic validation failed\n' >&2
  exit 1
}
validate_artifact_manifest "$out" "$run_dir" "$summary"

summary_sha="$(sha256_file "$summary")"
pointer_tmp="$out/.result.$$.json"
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$source_commit" \
  --arg tree "$source_tree" \
  --arg run "$run_id" \
  --arg summary "$run_id/summary.json" \
  --arg summarySHA256 "$summary_sha" \
  --arg archive "$run_id/$archive_name" \
  --arg archiveSHA256 "$archive_sha" '
    {
      schema:"hideout.release-package-candidate-pointer/v1",
      generatedAt:$generatedAt,
      source:{commit:$commit,tree:$tree,dirty:false},
      result:"passed",
      run:$run,
      summary:$summary,
      summarySHA256:$summarySHA256,
      archive:$archive,
      archiveSHA256:$archiveSHA256,
      candidateAcceptance:true,
      publicationStatus:"local-only"
    }
  ' >"$pointer_tmp"
chmod 0600 "$pointer_tmp"
mv "$pointer_tmp" "$out/result.json"

printf \
  'build-candidate: passed archive=%s sha256=%s summary=%s\n' \
  "$run_dir/$archive_name" \
  "$archive_sha" \
  "$summary"
