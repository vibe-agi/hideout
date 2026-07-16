#!/usr/bin/env bash

hideout_mktemp_daemon_store() {
  local base="${HIDEOUT_DAEMON_SHORT_TMPDIR:-/tmp}"
  case "$base" in
    /*) ;;
    *) echo "daemon-temp: short temporary root must be absolute: $base" >&2; return 2 ;;
  esac
  [ -d "$base" ] || {
    echo "daemon-temp: short temporary root does not exist: $base" >&2
    return 2
  }
  mktemp -d "$base/hd.XXXXXX"
}
