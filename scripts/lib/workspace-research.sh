#!/usr/bin/env bash
set -euo pipefail

workspace_sha256() {
  local path="$1"
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$path" | awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$path" | awk '{print $1}'
    return
  fi
  echo "workspace research requires shasum or sha256sum" >&2
  return 127
}

workspace_tree_digest() {
  local root="$1"
  local listing
  local -a hash_command
  if command -v shasum >/dev/null 2>&1; then
    hash_command=(shasum -a 256)
  elif command -v sha256sum >/dev/null 2>&1; then
    hash_command=(sha256sum)
  else
    echo "workspace research requires shasum or sha256sum" >&2
    return 127
  fi
  listing="$(mktemp "${TMPDIR:-/tmp}/hideout-workspace-tree.XXXXXX")"
  (
    cd "$root"
    find . -type d -name .git -prune -o -type f -print0 |
      LC_ALL=C sort -z |
      xargs -0 "${hash_command[@]}"
  ) >"$listing"
  workspace_sha256 "$listing"
  rm -f "$listing"
}

workspace_record_sample() {
  local output="$1"
  local label="$2"
  local milliseconds="$3"
  case "$milliseconds" in
    ''|*[!0-9.]* )
      echo "invalid millisecond sample: $milliseconds" >&2
      return 2
      ;;
  esac
  printf '%s\t%s\n' "$label" "$milliseconds" >>"$output"
}

workspace_percentile() {
  local samples="$1"
  local percentile="$2"
  awk -v p="$percentile" '
    { values[NR] = $1 }
    END {
      if (NR == 0) exit 2
      rank = int((p * NR + 99) / 100)
      if (rank < 1) rank = 1
      if (rank > NR) rank = NR
      print values[rank]
    }
  ' < <(LC_ALL=C sort -n "$samples")
}

workspace_summarize_samples() {
  local input="$1"
  local output="$2"
  local values
  values="$(mktemp "${TMPDIR:-/tmp}/hideout-workspace-values.XXXXXX")"
  awk -F '\t' 'NF == 1 { print $1; next } { print $2 }' "$input" >"$values"
  local count median p95
  count="$(wc -l <"$values" | tr -d ' ')"
  median="$(workspace_percentile "$values" 50)"
  p95="$(workspace_percentile "$values" 95)"
  printf '{"samples":%s,"medianMs":%s,"p95Ms":%s}\n' "$count" "$median" "$p95" >"$output"
  rm -f "$values"
}
