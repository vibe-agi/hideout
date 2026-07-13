#!/usr/bin/env bash

retained_gate2_sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    echo "retained-gate2-evidence: missing shasum or sha256sum" >&2
    return 127
  fi
}

# resolve_retained_gate2_log <manifest> <expected-commit>
# Prints the verified real Gate 2 log path from a same-commit, clean 031
# product-evidence manifest. The manifest, rather than a caller-supplied log,
# owns the artifact path and digest.
resolve_retained_gate2_log() {
  local manifest="${1:-}"
  local expected_commit="${2:-}"
  if [ -z "$manifest" ] || [ -z "$expected_commit" ]; then
    echo "retained-gate2-evidence: manifest and expected commit are required" >&2
    return 2
  fi
  if [ ! -f "$manifest" ]; then
    echo "retained-gate2-evidence: manifest does not exist: $manifest" >&2
    return 2
  fi

  go run ./cmd/hideout-schema-validate \
    schemas/product-hardening-evidence.schema.json "$manifest" >/dev/null

  local artifact
  artifact="$(jq -er --arg commit "$expected_commit" '
    select(.version == "hideout.product-hardening-evidence/v1") |
    select(.commit == $commit and .dirty == false) |
    select(.packageIdentity.name == "hideout" and .packageIdentity.version == $commit) |
    [.proofs[] |
      select(.proofId == "031.runtime.boundary-regression") |
      select(.mode == "real-gate" and .status == "passed" and .redactionStatus == "passed") |
      select(.runtime.schema == "hideout.runtime-evidence-binding/v1" and .runtime.buildDirty == false) |
      .artifacts[] |
      select(.kind == "log" and .redactionStatus == "passed") |
      [.path, .sha256]] |
    select(length == 1) |
    .[0] | @tsv
  ' "$manifest")" || {
    echo "retained-gate2-evidence: manifest lacks one clean same-commit boundary-regression log" >&2
    return 2
  }

  local relative expected_sha manifest_dir path path_parent
  IFS=$'\t' read -r relative expected_sha <<<"$artifact"
  case "$relative" in
    /* | ../* | */../* | */..)
      echo "retained-gate2-evidence: artifact path escapes evidence root: $relative" >&2
      return 2
      ;;
  esac
  manifest_dir="$(cd "$(dirname "$manifest")" && pwd -P)"
  path="$manifest_dir/$relative"
  path_parent="$(cd "$(dirname "$path")" && pwd -P)" || {
    echo "retained-gate2-evidence: artifact parent is unavailable: $relative" >&2
    return 2
  }
  case "$path_parent" in
    "$manifest_dir" | "$manifest_dir"/*) ;;
    *)
      echo "retained-gate2-evidence: artifact resolves outside evidence root: $relative" >&2
      return 2
      ;;
  esac
  path="$path_parent/$(basename "$relative")"
  if [ -L "$path" ]; then
    echo "retained-gate2-evidence: artifact must not be a symlink: $relative" >&2
    return 2
  fi
  if [ ! -f "$path" ]; then
    echo "retained-gate2-evidence: artifact does not exist: $path" >&2
    return 2
  fi
  if [ "$(retained_gate2_sha256_file "$path")" != "$expected_sha" ]; then
    echo "retained-gate2-evidence: artifact digest mismatch: $path" >&2
    return 2
  fi
  printf '%s\n' "$path"
}
