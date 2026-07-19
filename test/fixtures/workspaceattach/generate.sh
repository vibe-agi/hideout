#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <relations|git-10k|package-20k> <output-dir>" >&2
  exit 2
fi

kind="$1"
output="$2"

if [[ "$output" != /* ]]; then
  echo "workspace fixture output must be absolute" >&2
  exit 2
fi

mkdir -p "$output"

case "$kind" in
  relations)
    mkdir -p "$output/a/nested" "$output/b"
    printf 'workspace-a\n' >"$output/a/marker.txt"
    printf 'workspace-nested\n' >"$output/a/nested/marker.txt"
    printf 'workspace-b\n' >"$output/b/marker.txt"
    ;;
  git-10k)
    git -C "$output" init -q
    for group in $(seq -w 0 99); do
      mkdir -p "$output/src/$group"
      for item in $(seq -w 0 99); do
        printf '%s/%s\n' "$group" "$item" >"$output/src/$group/file-$item.txt"
      done
    done
    find "$output/src" -exec touch -t 202601010000 {} +
    git -C "$output" add .
    GIT_AUTHOR_DATE=2026-01-01T00:00:00Z \
      GIT_COMMITTER_DATE=2026-01-01T00:00:00Z \
      git -C "$output" -c user.name=Hideout -c user.email=fixture@hideout.invalid commit -qm fixture
    ;;
  package-20k)
    for group in $(seq -w 0 199); do
      mkdir -p "$output/node_modules/pkg-$group/lib"
      for item in $(seq -w 0 99); do
        printf 'module.exports=%s;\n' "$item" >"$output/node_modules/pkg-$group/lib/file-$item.js"
      done
    done
    ;;
  *)
    echo "unknown workspace fixture kind: $kind" >&2
    exit 2
    ;;
esac
