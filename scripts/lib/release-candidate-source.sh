#!/usr/bin/env bash

# Validate that one exact source commit is both the clean checkout being tested
# and already reachable from the public release branch. This helper performs no
# fetch: callers must decide when remote state is fresh enough for promotion.
validate_release_candidate_source() {
  local repository="$1"
  local candidate_commit="$2"
  local head_commit=""

  case "$candidate_commit" in
    [0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]) ;;
    *)
      echo "release-candidate-source: candidate commit must be a full lowercase SHA-1" >&2
      return 1
      ;;
  esac

  head_commit="$(git -C "$repository" rev-parse HEAD 2>/dev/null)" || {
    echo "release-candidate-source: cannot resolve candidate checkout HEAD" >&2
    return 1
  }
  if [ "$head_commit" != "$candidate_commit" ]; then
    echo "release-candidate-source: package commit is not the checked-out HEAD" >&2
    return 1
  fi
  if [ -n "$(git -C "$repository" status --porcelain --untracked-files=normal)" ]; then
    echo "release-candidate-source: candidate checkout is dirty" >&2
    return 1
  fi
  if ! git -C "$repository" rev-parse --verify refs/remotes/origin/master >/dev/null 2>&1; then
    echo "release-candidate-source: origin/master is unavailable" >&2
    return 1
  fi
  if ! git -C "$repository" merge-base --is-ancestor "$candidate_commit" refs/remotes/origin/master; then
    echo "release-candidate-source: candidate commit is not pushed to origin/master" >&2
    return 1
  fi
}
