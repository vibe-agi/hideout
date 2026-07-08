#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

jq empty schemas/hostfs-write-decision.schema.json
jq empty schemas/hostfs-write-event.schema.json

go test -count=1 \
  ./internal/hostfs \
  ./internal/hostfs/overlay \
  ./internal/broker \
  ./internal/manager \
  ./internal/daemon \
  ./internal/liveconsole \
  ./internal/export \
  ./internal/audit \
  ./internal/session \
  ./internal/app

GOOS=linux GOARCH="$(go env GOARCH)" go test -c ./cmd/hideout-hostfsd -o /tmp/hideout-hostfsd-010-smoke.test
rm -f /tmp/hideout-hostfsd-010-smoke.test

echo "hostfs-write-overlay-smoke: passed"
