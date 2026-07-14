#!/usr/bin/env bash
# Seed one exact catalog artifact into a fresh Lima cache without copying or
# downloading the image bytes. The caller still owns the fresh HOME and Lima
# instance; only the content-addressed, digest-verified image blob is reused.

hideout_seed_verified_runtime_cache() {
  if [ "$#" -ne 4 ]; then
    echo "verified-runtime-cache: expected <catalog> <shared-cache> <target-cache> <require-cache>" >&2
    return 2
  fi

  local catalog="$1" shared_cache="$2" target_cache="$3" require_cache="$4"
  local artifact url expected key source target actual metadata
  case "$require_cache" in
    0 | 1) ;;
    *) echo "verified-runtime-cache: require-cache must be 0 or 1" >&2; return 2 ;;
  esac
  [ -f "$catalog" ] || {
    echo "verified-runtime-cache: runtime catalog is missing" >&2
    return 1
  }

  artifact="$(jq -ce '
    [.families[] | select(.id == "developer-standard") | . as $family |
      .revisions[] | select(.id == $family.currentRevision) | .artifacts[] |
      select(.hostOS == "darwin" and .hostArch == "arm64")] |
    if length == 1 then .[0] else error("expected one darwin/arm64 runtime artifact") end
  ' "$catalog")" || return
  url="$(jq -r '.location' <<<"$artifact")"
  expected="$(jq -r '.sha256' <<<"$artifact")"
  if [ -z "$url" ] || ! printf '%s' "$expected" | grep -Eq '^[0-9a-f]{64}$'; then
    echo "verified-runtime-cache: catalog artifact identity is invalid" >&2
    return 1
  fi

  key="$(printf '%s' "$url" | shasum -a 256 | awk '{print $1}')"
  source="$shared_cache/download/by-url-sha256/$key"
  target="$target_cache/download/by-url-sha256/$key"
  if [ ! -f "$source/data" ]; then
    printf 'download-required\n'
    if [ "$require_cache" = "1" ]; then
      echo "verified-runtime-cache: verified runtime cache is required but missing" >&2
      return 1
    fi
    return 0
  fi

  actual="$(shasum -a 256 "$source/data" | awk '{print $1}')"
  if [ "$actual" != "$expected" ]; then
    echo "verified-runtime-cache: shared runtime cache digest mismatch" >&2
    return 1
  fi

  rm -rf "$target"
  mkdir -p "$target"
  for metadata in url sha256.digest time type; do
    if [ -f "$source/$metadata" ]; then
      cp "$source/$metadata" "$target/$metadata"
    fi
  done
  if ! cp -c "$source/data" "$target/data" 2>/dev/null; then
    rm -rf "$target"
    printf 'clone-unavailable\n'
    if [ "$require_cache" = "1" ]; then
      echo "verified-runtime-cache: verified runtime cache cannot be cloned into the fresh HOME" >&2
      return 1
    fi
    return 0
  fi

  printf 'verified-clone\n'
}
