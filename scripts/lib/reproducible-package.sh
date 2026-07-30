#!/usr/bin/env bash

# Shared release-build helpers. Callers own `set -euo pipefail`; these
# functions return failures so the caller can add its own command context.

hideout_validate_source_date_epoch() {
  local value="${1-}"
  case "$value" in
    "" | *[!0-9]*)
      printf 'SOURCE_DATE_EPOCH must be a non-negative decimal integer\n' >&2
      return 1
      ;;
  esac

  # Let both supported date implementations reject values outside their
  # representable range. This also avoids shell arithmetic and its octal rules.
  if date -u -r "$value" '+%Y-%m-%dT%H:%M:%SZ' >/dev/null 2>&1; then
    return 0
  fi
  if date -u -d "@$value" '+%Y-%m-%dT%H:%M:%SZ' >/dev/null 2>&1; then
    return 0
  fi
  printf 'SOURCE_DATE_EPOCH is outside the supported date range: %s\n' \
    "$value" >&2
  return 1
}

hideout_timestamp_from_epoch() {
  local value="${1-}"
  hideout_validate_source_date_epoch "$value" || return 1
  if date -u -r "$value" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null; then
    return 0
  fi
  date -u -d "@$value" '+%Y-%m-%dT%H:%M:%SZ'
}

hideout_touch_stamp_from_epoch() {
  local value="${1-}"
  hideout_validate_source_date_epoch "$value" || return 1
  if date -u -r "$value" '+%Y%m%d%H%M.%S' 2>/dev/null; then
    return 0
  fi
  date -u -d "@$value" '+%Y%m%d%H%M.%S'
}

hideout_build_timestamp() {
  if [ "${SOURCE_DATE_EPOCH+x}" = "x" ]; then
    hideout_timestamp_from_epoch "$SOURCE_DATE_EPOCH"
    return
  fi
  date -u '+%Y-%m-%dT%H:%M:%SZ'
}

hideout_normalize_tree_mtime() {
  local root="${1-}" epoch="${2-}" stamp unsafe
  if [ ! -d "$root" ] || [ -L "$root" ]; then
    printf 'reproducible package root must be a non-symlink directory: %s\n' \
      "$root" >&2
    return 1
  fi
  stamp="$(hideout_touch_stamp_from_epoch "$epoch")" || return 1
  unsafe="$(find "$root" ! -type f ! -type d -print -quit)"
  if [ -n "$unsafe" ]; then
    printf 'reproducible package tree contains an unsupported entry: %s\n' \
      "$unsafe" >&2
    return 1
  fi
  find "$root" -depth -exec touch -t "$stamp" {} +
}

hideout_create_reproducible_tar_gz() {
  local parent="${1-}" member="${2-}" archive="${3-}" epoch="${4-}"
  local list archive_tmp
  if [ ! -d "$parent/$member" ] || [ -L "$parent/$member" ]; then
    printf 'reproducible package member is missing or unsafe: %s\n' \
      "$parent/$member" >&2
    return 1
  fi
  case "$member" in
    "" | /* | . | .. | */* | *$'\n'*)
      printf 'reproducible package member must be one safe basename\n' >&2
      return 1
      ;;
  esac
  command -v gzip >/dev/null 2>&1 || {
    printf 'reproducible package creation requires gzip\n' >&2
    return 1
  }
  hideout_normalize_tree_mtime "$parent/$member" "$epoch" || return 1
  mkdir -p "$(dirname "$archive")"
  list="$(mktemp "${TMPDIR:-/tmp}/hideout-package-list.XXXXXX")" ||
    return 1
  archive_tmp="$(mktemp "$(dirname "$archive")/.hideout-archive.XXXXXX")" || {
    find "$list" -depth -delete
    return 1
  }

  if ! (
    cd "$parent"
    find "$member" -print | LC_ALL=C sort >"$list"
    if tar --version 2>&1 | head -n 1 | grep -q 'bsdtar'; then
      COPYFILE_DISABLE=1 tar \
        --format ustar \
        --uid 0 \
        --gid 0 \
        --uname root \
        --gname root \
        --no-xattrs \
        --no-acls \
        --no-fflags \
        --no-recursion \
        -cf - \
        -T "$list"
    else
      tar \
        --format=ustar \
        --owner=0 \
        --group=0 \
        --numeric-owner \
        --no-xattrs \
        --no-acls \
        --no-selinux \
        --no-recursion \
        -cf - \
        -T "$list"
    fi
  ) | gzip -n -9 >"$archive_tmp"; then
    find "$archive_tmp" "$list" -depth -delete
    return 1
  fi
  chmod 0600 "$archive_tmp"
  mv "$archive_tmp" "$archive"
  find "$list" -depth -delete
}
