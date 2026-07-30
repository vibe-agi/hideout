#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"

jq empty \
  formal/inventory.json \
  formal/cfg/shared-constants.json \
  internal/workloadobs/testdata/credential-canaries.json \
  internal/workloadobs/testdata/expected-activity.json \
  internal/workloadobs/testdata/fixture.json \
  schemas/activity-owner.schema.json \
  schemas/activity-record.schema.json \
  schemas/coverage-interval.schema.json \
  schemas/daemon-event-v2.schema.json \
  schemas/formal-inventory.schema.json \
  schemas/observer-frame.schema.json \
  schemas/operation.schema.json \
  schemas/operator-snapshot.schema.json \
  schemas/profile-projection.schema.json

sh -n internal/workloadobs/testdata/reference-workload.sh
go test ./schemas
scripts/generate-workload-observer-bpf.sh --check
