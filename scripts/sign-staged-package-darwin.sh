#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
VZ_RELATIVE="bin/hideout-migration-vz-adopt-darwin-arm64"
VZ_ENTITLEMENTS="$ROOT/packaging/macos/hideout-migration-vz-adopt.entitlements.plist"

usage() {
  cat <<'USAGE'
Usage:
  scripts/sign-staged-package-darwin.sh --stage DIR --identity SHA1 --keychain PATH
  scripts/sign-staged-package-darwin.sh --self-test

Signs every Mach-O in a staged Darwin package. The signer validates helper
provenance before mutation, applies the declared entitlement policy, atomically
rebinds signed helper bytes to their sidecar manifest, and verifies the result.
USAGE
}

sha256_file() {
  shasum -a 256 "$1" | awk '{print $1}'
}

entitlements_json() {
  local binary="$1" document
  document="$(/usr/bin/codesign -d --entitlements :- --xml "$binary" 2>/dev/null)" || {
    printf 'package-signing-darwin: cannot inspect entitlements for %s\n' "$binary" >&2
    return 1
  }
  if [ -z "$document" ]; then
    printf '{}\n'
    return 0
  fi
  printf '%s' "$document" | /usr/bin/plutil -convert json -o - -- -
}

require_entitlement_policy() {
  local relative="$1" binary="$2" entitlements
  entitlements="$(entitlements_json "$binary")"
  if [ "$relative" = "$VZ_RELATIVE" ]; then
    jq -e '
      (keys == ["com.apple.security.virtualization"]) and
      .["com.apple.security.virtualization"] == true
    ' <<<"$entitlements" >/dev/null || {
      printf 'package-signing-darwin: %s lacks its exact virtualization entitlement\n' \
        "$relative" >&2
      return 1
    }
    return 0
  fi
  jq -e 'keys == []' <<<"$entitlements" >/dev/null || {
    printf 'package-signing-darwin: undeclared entitlements on %s\n' "$relative" >&2
    return 1
  }
}

validate_vz_manifest() {
  local binary="$1" expected_sha="$2" manifest="$1.manifest.json"
  if [ ! -f "$manifest" ] || [ -L "$manifest" ]; then
    printf 'package-signing-darwin: VZ helper manifest is missing or unsafe\n' >&2
    return 1
  fi
  jq -e \
    --arg artifact "$(basename "$binary")" --arg sha "$expected_sha" '
    .version == "hideout.helper-manifest/v1" and
    .command == "hideout-migration-vz-adopt" and
    .targetOS == "darwin" and .targetArch == "arm64" and
    .artifact == $artifact and .sha256 == $sha and
    .builder == "go build -mod=readonly -trimpath" and
    .upstreamModule == "github.com/Code-Hex/vz/v3" and
    .upstreamVersion == "v3.7.1" and
    .license == "Apache-2.0" and
    .buildMode == "apple-vz-zero-network-adoption-entitled-v1" and
    .packageOwned == true
  ' "$manifest" >/dev/null || {
    printf 'package-signing-darwin: VZ helper provenance or checksum mismatch\n' >&2
    return 1
  }
}

rebind_vz_manifest() {
  local binary="$1" signed_sha="$2" manifest="$1.manifest.json" temporary
  temporary="$(mktemp "$(dirname "$manifest")/.hideout-vz-manifest.XXXXXX")"
  jq --arg sha "$signed_sha" '.sha256 = $sha' "$manifest" >"$temporary"
  chmod 0644 "$temporary"
  mv -f "$temporary" "$manifest"
  validate_vz_manifest "$binary" "$signed_sha"
}

collect_macho_paths() {
  local package_root="$1" path relative
  MACHO_PATHS=()
  while IFS= read -r path; do
    relative="${path#"$package_root/"}"
    if [[ "$relative" == *$'\n'* ]]; then
      printf 'package-signing-darwin: package path contains a newline\n' >&2
      return 1
    fi
    if /usr/bin/file -b "$path" | grep -q 'Mach-O'; then
      if [ -L "$path" ] || [ ! -x "$path" ]; then
        printf 'package-signing-darwin: Mach-O is not a regular executable: %s\n' \
          "$relative" >&2
        return 1
      fi
      MACHO_PATHS+=("$relative")
    fi
  done < <(find "$package_root" -type f -print | LC_ALL=C sort)
}

require_expected_macho_paths() {
  local required candidate found
  for required in bin/hideout bin/hideout-shim "$VZ_RELATIVE"; do
    found=false
    for candidate in "${MACHO_PATHS[@]}"; do
      if [ "$candidate" = "$required" ]; then
        found=true
        break
      fi
    done
    if [ "$found" != true ]; then
      printf 'package-signing-darwin: required Mach-O is absent: %s\n' "$required" >&2
      return 1
    fi
  done
}

sign_stage() {
  local stage="$1" mode="$2" identity="$3" keychain="$4"
  local metadata package_root signing_mode relative binary unsigned_sha signed_sha detail
  local signed=0 rebound=0

  case "$stage" in
    /*) ;;
    *) printf 'package-signing-darwin: --stage must be absolute\n' >&2; return 2 ;;
  esac
  stage="$(CDPATH='' cd -- "$stage" && pwd -P)"
  metadata="$stage/.package-build.json"
  package_root="$stage/hideout"
  if [ ! -d "$package_root" ] || [ ! -f "$metadata" ] ||
      [ -e "$package_root/package-manifest.json" ]; then
    printf 'package-signing-darwin: stage is missing, unsafe, or already finalized\n' >&2
    return 1
  fi
  jq -e '.hostOS == "darwin" and .hostArch == "arm64"' "$metadata" >/dev/null || {
    printf 'package-signing-darwin: stage is not a Darwin arm64 package\n' >&2
    return 1
  }
  signing_mode="$(jq -er '.signingMode' "$metadata")"
  if [ "$mode" = production ]; then
    if [ "$signing_mode" != developer-id-observed ] ||
        ! [[ "$identity" =~ ^[0-9A-Fa-f]{40}$ ]] || [ ! -f "$keychain" ]; then
      printf 'package-signing-darwin: protected Developer ID inputs are invalid\n' >&2
      return 1
    fi
  elif [ "$signing_mode" != developer-preview-unsigned ]; then
    printf 'package-signing-darwin: ad-hoc self-test cannot sign a release stage\n' >&2
    return 1
  fi

  collect_macho_paths "$package_root"
  require_expected_macho_paths
  for relative in "${MACHO_PATHS[@]}"; do
    binary="$package_root/$relative"
    if [ -e "$binary.manifest.json" ] && [ "$relative" != "$VZ_RELATIVE" ]; then
      printf 'package-signing-darwin: undeclared signed sidecar transform for %s\n' \
        "$relative" >&2
      return 1
    fi
    if [ "$relative" = "$VZ_RELATIVE" ]; then
      unsigned_sha="$(sha256_file "$binary")"
      validate_vz_manifest "$binary" "$unsigned_sha"
    fi
    require_entitlement_policy "$relative" "$binary"
  done

  for relative in "${MACHO_PATHS[@]}"; do
    binary="$package_root/$relative"
    sign_args=(--force --options runtime)
    if [ "$relative" = "$VZ_RELATIVE" ]; then
      sign_args+=(--entitlements "$VZ_ENTITLEMENTS")
    fi
    if [ "$mode" = production ]; then
      sign_args+=(--timestamp --keychain "$keychain" --sign "$identity")
    else
      # Force the test transform to change bytes even when the Go builder's
      # existing ad-hoc signature is otherwise deterministic and identical.
      sign_args+=(
        --timestamp=none
        --identifier "com.vibe-agi.hideout.test.$(basename "$relative")"
        --sign -
      )
    fi
    printf 'package-signing-darwin: signing %s\n' "$relative"
    /usr/bin/codesign "${sign_args[@]}" "$binary"
    /usr/bin/codesign --verify --strict "$binary"
    detail="$(/usr/bin/codesign -dvvv "$binary" 2>&1)"
    grep -Eq '^CodeDirectory .*\bruntime\b' <<<"$detail" || {
      printf 'package-signing-darwin: hardened runtime is absent on %s\n' "$relative" >&2
      return 1
    }
    if [ "$mode" = production ]; then
      grep -Eq '^Authority=Developer ID Application:' <<<"$detail" &&
        grep -Eq '^TeamIdentifier=[A-Z0-9]+$' <<<"$detail" &&
        grep -Eq '^Timestamp=.+$' <<<"$detail" || {
          printf 'package-signing-darwin: Developer ID identity is incomplete on %s\n' \
            "$relative" >&2
          return 1
        }
    fi
    require_entitlement_policy "$relative" "$binary"
    if [ "$relative" = "$VZ_RELATIVE" ]; then
      signed_sha="$(sha256_file "$binary")"
      if [ "$signed_sha" = "$unsigned_sha" ]; then
        printf 'package-signing-darwin: VZ helper signing did not change its bytes\n' >&2
        return 1
      fi
      rebind_vz_manifest "$binary" "$signed_sha"
      rebound=$((rebound + 1))
    fi
    signed=$((signed + 1))
  done
  if [ "$signed" -ne "${#MACHO_PATHS[@]}" ] || [ "$rebound" -ne 1 ]; then
    printf 'package-signing-darwin: signing transform did not close its inventory\n' >&2
    return 1
  fi
  printf 'package-signing-darwin: passed signed=%d rebound=%d mode=%s\n' \
    "$signed" "$rebound" "$mode"
}

create_self_test_stage() {
  local stage="$1" package_root="$1/hideout" relative binary helper_sha
  mkdir -p "$package_root/bin"
  for relative in bin/hideout bin/hideout-shim "$VZ_RELATIVE"; do
    binary="$package_root/$relative"
    cp /usr/bin/true "$binary"
    chmod 0755 "$binary"
  done
  # The fixture starts with the same required entitlement but deliberately lacks
  # hardened runtime so the signing transform must produce different bytes.
  /usr/bin/codesign --force --timestamp=none --sign - \
    --entitlements "$VZ_ENTITLEMENTS" "$package_root/$VZ_RELATIVE" >/dev/null
  helper_sha="$(sha256_file "$package_root/$VZ_RELATIVE")"
  jq -n --arg artifact "$(basename "$VZ_RELATIVE")" --arg sha "$helper_sha" '
    {version:"hideout.helper-manifest/v1",command:"hideout-migration-vz-adopt",
     targetOS:"darwin",targetArch:"arm64",artifact:$artifact,sha256:$sha,
     builder:"go build -mod=readonly -trimpath",builtAt:"2026-01-01T00:00:00Z",
     upstreamModule:"github.com/Code-Hex/vz/v3",upstreamVersion:"v3.7.1",
     license:"Apache-2.0",buildMode:"apple-vz-zero-network-adoption-entitled-v1",
     packageOwned:true}
  ' >"$package_root/$VZ_RELATIVE.manifest.json"
  jq -n '{hostOS:"darwin",hostArch:"arm64",signingMode:"developer-preview-unsigned"}' \
    >"$stage/.package-build.json"
}

run_self_test() {
  local fixture tampered missing_entitlement helper initial_sha signed_sha missing_sha
  self_test_temporary="$(mktemp -d "${TMPDIR:-/tmp}/hideout-package-signing.XXXXXX")"
  fixture="$self_test_temporary/stage"
  tampered="$self_test_temporary/tampered"
  missing_entitlement="$self_test_temporary/missing-entitlement"
  cleanup_self_test() {
    find "$self_test_temporary" -depth -delete
  }
  trap cleanup_self_test EXIT

  create_self_test_stage "$fixture"
  helper="$fixture/hideout/$VZ_RELATIVE"
  initial_sha="$(sha256_file "$helper")"
  cp -R "$fixture" "$tampered"
  printf 'tampered\n' >>"$tampered/hideout/$VZ_RELATIVE"
  if /bin/bash "$0" --stage "$tampered" --ad-hoc-test-only \
      >"$self_test_temporary/tampered.out" 2>"$self_test_temporary/tampered.err"; then
    printf 'package-signing-darwin self-test: pre-sign tampering was accepted\n' >&2
    return 1
  fi
  grep -Fq 'VZ helper provenance or checksum mismatch' "$self_test_temporary/tampered.err"

  cp -R "$fixture" "$missing_entitlement"
  /usr/bin/codesign --force --options runtime --timestamp=none --sign - \
    "$missing_entitlement/hideout/$VZ_RELATIVE" >/dev/null
  missing_sha="$(sha256_file "$missing_entitlement/hideout/$VZ_RELATIVE")"
  jq --arg sha "$missing_sha" '.sha256 = $sha' \
    "$missing_entitlement/hideout/$VZ_RELATIVE.manifest.json" \
    >"$self_test_temporary/missing-entitlement-manifest.json"
  mv "$self_test_temporary/missing-entitlement-manifest.json" \
    "$missing_entitlement/hideout/$VZ_RELATIVE.manifest.json"
  if /bin/bash "$0" --stage "$missing_entitlement" --ad-hoc-test-only \
      >"$self_test_temporary/missing-entitlement.out" \
      2>"$self_test_temporary/missing-entitlement.err"; then
    printf 'package-signing-darwin self-test: missing entitlement was accepted\n' >&2
    return 1
  fi
  grep -Fq 'lacks its exact virtualization entitlement' \
    "$self_test_temporary/missing-entitlement.err"

  /bin/bash "$0" --stage "$fixture" --ad-hoc-test-only \
    >"$self_test_temporary/signing.out"
  signed_sha="$(sha256_file "$helper")"
  [ "$signed_sha" != "$initial_sha" ]
  [ "$(jq -er '.sha256' "$helper.manifest.json")" = "$signed_sha" ]
  require_entitlement_policy "$VZ_RELATIVE" "$helper"
  grep -Fq 'passed signed=3 rebound=1 mode=ad-hoc-test' "$self_test_temporary/signing.out"
  printf 'package-signing-darwin self-test: passed\n'
}

stage=""
identity=""
keychain=""
mode=production
self_test=false
while [ "$#" -gt 0 ]; do
  case "$1" in
    --stage) stage="${2:-}"; shift 2 ;;
    --identity) identity="${2:-}"; shift 2 ;;
    --keychain) keychain="${2:-}"; shift 2 ;;
    --ad-hoc-test-only) mode=ad-hoc-test; shift ;;
    --self-test) self_test=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) printf 'package-signing-darwin: unknown option: %s\n' "$1" >&2; usage >&2; exit 2 ;;
  esac
done

if [ "$self_test" = true ]; then
  if [ -n "$stage$identity$keychain" ] || [ "$mode" != production ]; then
    printf 'package-signing-darwin: --self-test cannot be combined with signing inputs\n' >&2
    exit 2
  fi
  run_self_test
  exit 0
fi
if [ -z "$stage" ]; then
  printf 'package-signing-darwin: --stage is required\n' >&2
  usage >&2
  exit 2
fi
if [ "$mode" = production ] && { [ -z "$identity" ] || [ -z "$keychain" ]; }; then
  printf 'package-signing-darwin: --identity and --keychain are required\n' >&2
  exit 2
fi
if [ "$mode" = ad-hoc-test ] && [ -n "$identity$keychain" ]; then
  printf 'package-signing-darwin: ad-hoc self-test rejects production inputs\n' >&2
  exit 2
fi

sign_stage "$stage" "$mode" "$identity" "$keychain"
