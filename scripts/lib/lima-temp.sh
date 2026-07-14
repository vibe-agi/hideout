#!/usr/bin/env bash

hideout_mktemp_lima_home() {
  local base="${HIDEOUT_LIMA_SHORT_TMPDIR:-/tmp}"
  case "$base" in
    /*) ;;
    *) echo "lima-temp: short temporary root must be absolute: $base" >&2; return 2 ;;
  esac
  [ -d "$base" ] || {
    echo "lima-temp: short temporary root does not exist: $base" >&2
    return 2
  }
  mktemp -d "$base/hl.XXXXXX"
}
