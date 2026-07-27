#!/usr/bin/env bash

# Put a deterministic, failing Go command ahead of any host toolchain. Release
# package journeys can then retain the normal macOS system PATH while proving
# that no package operation attempted to invoke Go.
release_install_go_tripwire() {
  local bin_dir="$1"

  mkdir -p "$bin_dir"
  printf '%s\n' \
    '#!/bin/sh' \
    ': > "${0%/*}/go.invoked"' \
    'exit 127' >"$bin_dir/go"
  chmod 755 "$bin_dir/go"
}

release_go_tripwire_invoked() {
  local bin_dir="$1"

  [ -e "$bin_dir/go.invoked" ]
}

release_clear_go_tripwire() {
  local bin_dir="$1"

  rm -f "$bin_dir/go.invoked"
}
