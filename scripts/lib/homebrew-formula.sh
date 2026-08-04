#!/usr/bin/env bash

# The source formula is also the next-release template. Normally it must match
# the immutable formula snapshot for the current public release byte-for-byte.
# During a package-component candidate transition, the only permitted
# pre-render deltas are preservation entries for newly packaged Linux helpers.
# Once the next receipt is rendered, its snapshot contains these lines and the
# exact comparison succeeds again.
homebrew_formula_matches_published_snapshot() {
  local source_formula="$1"
  local formula_snapshot="$2"
  local observer_line='             "bin/hideout-observer-linux-arm64",'
  local adoption_line='             "bin/hideout-migration-adopt-linux-arm64",'
  local host_codesign_line='    system "/usr/bin/codesign", "--verify", "--strict",'
  local host_codesign_path_line='           package_root/"bin/hideout-migration-vz-adopt-darwin-arm64"'

  if cmp <(tail -n +3 "$source_formula") "$formula_snapshot" >/dev/null 2>&1; then
    return 0
  fi
  if [ "$(grep -Fxc -- "$observer_line" "$source_formula" || true)" != "1" ] ||
     [ "$(grep -Fxc -- "$observer_line" "$formula_snapshot" || true)" != "0" ] ||
     [ "$(grep -Fxc -- "$adoption_line" "$source_formula" || true)" != "1" ] ||
     [ "$(grep -Fxc -- "$adoption_line" "$formula_snapshot" || true)" != "0" ] ||
     [ "$(grep -Fxc -- "$host_codesign_line" "$source_formula" || true)" != "1" ] ||
     [ "$(grep -Fxc -- "$host_codesign_line" "$formula_snapshot" || true)" != "0" ] ||
     [ "$(grep -Fxc -- "$host_codesign_path_line" "$source_formula" || true)" != "1" ] ||
     [ "$(grep -Fxc -- "$host_codesign_path_line" "$formula_snapshot" || true)" != "0" ]; then
    return 1
  fi
  cmp \
    <(tail -n +3 "$source_formula" |
      grep -Fvx -- "$observer_line" |
      grep -Fvx -- "$adoption_line" |
      grep -Fvx -- "$host_codesign_line" |
      grep -Fvx -- "$host_codesign_path_line") \
    "$formula_snapshot" >/dev/null 2>&1
}
