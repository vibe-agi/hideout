#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

command -v jq >/dev/null 2>&1 || { echo "release-hardening-smoke: jq required" >&2; exit 127; }

tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-release-hardening.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT
gomodcache="$(go env GOMODCACHE)"
gocache="$(go env GOCACHE)"

go test ./internal/releasecompat

go run ./cmd/hideout support matrix --json >"$tmp/matrix.json"
go run ./cmd/hideout-schema-validate schemas/support-matrix.schema.json "$tmp/matrix.json"
jq -e '
  .schema == "hideout.support-matrix/v1" and
  .version == "2026-07-alpha" and
  any(.entries[]; .subject == "platform/darwin/arm64" and .level == "first-class") and
  any(.entries[]; .subject == "backend/native" and .level == "degraded") and
  any(.nonClaims[]; .id == "guest-root-containment")
' "$tmp/matrix.json" >/dev/null

go run ./cmd/hideout version >"$tmp/version.out"
grep -q 'supportMatrix: hideout.support-matrix/v1 2026-07-alpha' "$tmp/version.out"
grep -q 'support: platform=platform/' "$tmp/version.out"

home="$tmp/home"
workspace="$tmp/workspace"
mkdir -p "$home" "$workspace"
HOME="$home" GOMODCACHE="$gomodcache" GOCACHE="$gocache" \
  go run ./cmd/hideout doctor --backend native --workspace "$workspace" --format json >"$tmp/doctor.json"
go run ./cmd/hideout-schema-validate schemas/doctor-report.schema.json "$tmp/doctor.json"
jq -e '
  any(.findings[]; .checkId == "support-matrix" and .status == "warn" and (.summary | contains("backend/native:degraded")))
' "$tmp/doctor.json" >/dev/null

go run ./cmd/hideout support readiness --mode local-fast --out "$tmp/readiness-local.json"
go run ./cmd/hideout-schema-validate schemas/release-readiness.schema.json "$tmp/readiness-local.json"
jq -e '
  .schema == "hideout.release-readiness/v1" and
  .mode == "local-fast" and
  .status == "not-release" and
  .releaseReady == false
' "$tmp/readiness-local.json" >/dev/null

if go run ./cmd/hideout support readiness --mode release-candidate --out "$tmp/readiness-rc.json" >"$tmp/rc.out" 2>"$tmp/rc.err"; then
  echo "release-hardening-smoke: release-candidate passed without real gate evidence" >&2
  exit 1
fi
go run ./cmd/hideout-schema-validate schemas/release-readiness.schema.json "$tmp/readiness-rc.json"
jq -e '
  .mode == "release-candidate" and
  .status == "failed" and
  .releaseReady == false and
  all(.gates[]; .status == "missing" and .code == "release.gate-evidence.missing")
' "$tmp/readiness-rc.json" >/dev/null

echo "release-hardening-smoke: passed"
