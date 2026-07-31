#!/usr/bin/env bash

# The source formula is also the next-release template. Normally it must match
# the immutable formula snapshot for the current public release byte-for-byte.
# During the observer candidate transition, the only permitted pre-render
# delta is preservation of the newly packaged Linux observer executable. Once
# the next receipt is rendered, its snapshot contains this line and the exact
# comparison succeeds again.
homebrew_formula_matches_published_snapshot() {
  local source_formula="$1"
  local formula_snapshot="$2"
  local observer_line='             "bin/hideout-observer-linux-arm64",'

  if cmp <(tail -n +3 "$source_formula") "$formula_snapshot" >/dev/null 2>&1; then
    return 0
  fi
  if [ "$(grep -Fxc -- "$observer_line" "$source_formula" || true)" != "1" ] ||
     [ "$(grep -Fxc -- "$observer_line" "$formula_snapshot" || true)" != "0" ]; then
    return 1
  fi
  cmp \
    <(tail -n +3 "$source_formula" | grep -Fvx -- "$observer_line") \
    "$formula_snapshot" >/dev/null 2>&1
}
