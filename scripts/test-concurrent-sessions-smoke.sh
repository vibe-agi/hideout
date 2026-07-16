#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

jq empty \
  schemas/active-session-summary.schema.json \
  schemas/environment-activation-receipt.schema.json \
  schemas/environment-service-state.schema.json

go test ./internal/session ./internal/environment ./internal/network ./internal/backend ./internal/backend/lima ./internal/manager ./internal/recovery
go test ./internal/app -run '^TestRunLimaDefaultReusesWorkspaceEnvironment$'

tmp=$(mktemp -d "${TMPDIR:-/tmp}/hideout-concurrent-smoke.XXXXXX")
pid_one=
pid_two=
cleanup() {
  if [ -n "$pid_one" ]; then kill "$pid_one" 2>/dev/null || true; fi
  if [ -n "$pid_two" ]; then kill "$pid_two" 2>/dev/null || true; fi
  rm -rf "$tmp"
}
trap cleanup EXIT HUP INT TERM

bin="$tmp/hideout"
home="$tmp/home"
workspace="$tmp/workspace"
mkdir -p "$home" "$workspace"
go build -o "$bin" ./cmd/hideout

run_hold() {
  marker=$1
  release=$2
  log=$3
  HOME="$home" "$bin" run \
    --backend native \
    --allow-weak-isolation \
    --workspace "$workspace" \
    -- sh -c 'touch "$1"; while [ ! -f "$2" ]; do sleep 0.05; done' \
    hideout-concurrent-smoke "$marker" "$release" >"$log" 2>&1
}

wait_for_file() {
  path=$1
  attempts=0
  while [ ! -f "$path" ]; do
    attempts=$((attempts + 1))
    if [ "$attempts" -ge 200 ]; then
      echo "hideout: timed out waiting for $path" >&2
      return 1
    fi
    sleep 0.05
  done
}

run_hold "$tmp/one.started" "$tmp/one.release" "$tmp/one.log" &
pid_one=$!
wait_for_file "$tmp/one.started"
run_hold "$tmp/two.started" "$tmp/two.release" "$tmp/two.log" &
pid_two=$!
wait_for_file "$tmp/two.started"

running=$(HOME="$home" "$bin" env list)
printf '%s\n' "$running" | awk -F '\t' 'NR > 1 && $5 == "running" && $6 == "2" { found=1 } END { exit !found }'

touch "$tmp/one.release"
if ! wait "$pid_one"; then
  cat "$tmp/one.log" >&2
  exit 1
fi
pid_one=
kill -0 "$pid_two"
one_left=$(HOME="$home" "$bin" env list)
printf '%s\n' "$one_left" | awk -F '\t' 'NR > 1 && $5 == "running" && $6 == "1" { found=1 } END { exit !found }'

touch "$tmp/two.release"
if ! wait "$pid_two"; then
  cat "$tmp/two.log" >&2
  exit 1
fi
pid_two=
idle=$(HOME="$home" "$bin" env list)
printf '%s\n' "$idle" | awk -F '\t' 'NR > 1 && $5 == "ready" && $6 == "0" { found=1 } END { exit !found }'
if grep -q 'already in use' "$tmp/one.log" "$tmp/two.log"; then
  echo "hideout: concurrent run regressed to the environment-busy error" >&2
  exit 1
fi

echo "concurrent-sessions smoke passed"
