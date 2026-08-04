#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"

usage() {
  cat <<'USAGE'
Usage: scripts/gates/generated.sh

Validates checked-in JSON/shell fixtures and proves generated workload-observer
BPF outputs match their sources. This gate requires the pinned LLVM toolchain
selected by Gate 0 or the local release aggregate. It never starts a VM.
USAGE
}

if [ "$#" -ne 0 ]; then
  case "${1:-}" in
    -h | --help)
      [ "$#" -eq 1 ] || {
        usage >&2
        exit 2
      }
      usage
      exit 0
      ;;
    *)
      printf 'generated-gate: unknown option: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
fi

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
