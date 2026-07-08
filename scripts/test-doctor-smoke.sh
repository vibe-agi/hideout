#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

command -v jq >/dev/null 2>&1 || { echo "doctor-smoke: jq required" >&2; exit 127; }

tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-doctor-smoke.XXXXXX")"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT

browser="$tmp/hideout-browser"
cat >"$browser" <<'SH'
#!/usr/bin/env sh
exit 0
SH
chmod 700 "$browser"

run_hideout() {
  HIDEOUT_STORE_ROOT="$store" HIDEOUT_BROWSER_PATH="$browser" go run ./cmd/hideout "$@"
}

assert_clean() {
  path="$1"
  if grep -E 'HIDEOUT_SECRET|cap_[0-9a-f]{16,}|[0-9a-f]{32}' "$path" >/dev/null; then
    echo "doctor-smoke: control-plane material leaked in $path" >&2
    cat "$path" >&2
    exit 1
  fi
}

store="$tmp/default-store"
mkdir -p "$tmp/workspace"

run_hideout doctor --backend native --workspace "$tmp/workspace" >"$tmp/doctor-human.out"
grep -q 'Hideout doctor' "$tmp/doctor-human.out"
grep -q 'store: ok writable' "$tmp/doctor-human.out"
grep -q 'backend: warn native is weak isolation' "$tmp/doctor-human.out"

run_hideout doctor --backend native --workspace "$tmp/workspace" --format json >"$tmp/doctor.json"
go run ./cmd/hideout-schema-validate schemas/doctor-report.schema.json "$tmp/doctor.json"
jq -e '
  .schema == "hideout.doctor-report/v1" and
  .summary.failed == false and
  .summary.exitCode == 0 and
  ([.findings[] | select(.checkId == "backend" and .status == "warn")] | length) == 1
' "$tmp/doctor.json" >/dev/null
assert_clean "$tmp/doctor.json"

run_hideout doctor --backend native --workspace "$tmp/workspace" --feature dns --format json >"$tmp/doctor-dns.json"
jq -e '
  (.features == ["dns"]) and
  ([.findings[] | select(.checkId == "feature-dns" and .status == "skipped")] | length) == 1
' "$tmp/doctor-dns.json" >/dev/null

if run_hideout doctor --backend native --workspace "$tmp/missing" --format json >"$tmp/doctor-bad.json" 2>"$tmp/doctor-bad.err"; then
  echo "doctor-smoke: missing workspace unexpectedly passed" >&2
  cat "$tmp/doctor-bad.json" >&2
  exit 1
fi
go run ./cmd/hideout-schema-validate schemas/doctor-report.schema.json "$tmp/doctor-bad.json"
jq -e '
  .summary.failed == true and
  .summary.exitCode == 1 and
  ([.findings[] | select(.checkId == "workspace" and .status == "error" and .required == true)] | length) >= 1
' "$tmp/doctor-bad.json" >/dev/null

evidence="$tmp/doctor-evidence.json"
run_hideout doctor --backend native --workspace "$tmp/workspace" --format json --evidence-out "$evidence" >"$tmp/doctor-with-evidence.json"
go run ./cmd/hideout-schema-validate schemas/doctor-report.schema.json "$evidence"
assert_clean "$evidence"

exported="$tmp/doctor-export.json"
run_hideout audit export \
  --source doctor-report \
  --doctor-report "$evidence" \
  --out "$exported" \
  --acknowledge-full-fidelity >"$tmp/doctor-export.out"
go run ./cmd/hideout-schema-validate schemas/export-artifact.schema.json "$exported"
jq -e '.provenance.source == "doctor-report" and .body.schema == "hideout.doctor-report/v1"' "$exported" >/dev/null
assert_clean "$exported"

fix_store="$tmp/fix-store"
store="$fix_store"
run_hideout doctor --fix --dry-run --backend native >"$tmp/fix-dry.out"
grep -q 'Hideout doctor fix plan' "$tmp/fix-dry.out"
test ! -e "$fix_store/profiles/default/profile.json"

if run_hideout doctor --fix --backend native >"$tmp/fix-missing.out" 2>"$tmp/fix-missing.err"; then
  echo "doctor-smoke: doctor --fix without mode unexpectedly succeeded" >&2
  exit 1
fi
grep -q 'doctor --fix requires --dry-run or --apply' "$tmp/fix-missing.err"

echo "doctor-smoke: passed"
