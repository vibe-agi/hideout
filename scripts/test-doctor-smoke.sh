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

copy_artifacts() {
  if [ -z "${HIDEOUT_DOCTOR_SMOKE_ARTIFACT_DIR:-}" ]; then
    return
  fi
  mkdir -p "$HIDEOUT_DOCTOR_SMOKE_ARTIFACT_DIR"
  for rel in \
    doctor-human.out \
    doctor.json \
    doctor-dns.json \
    doctor-deep.json \
    doctor-packaging.json \
    doctor-decisions.json \
    doctor-redaction.json \
    doctor-redaction.err \
    recovery-codes.json \
    doctor-bad.json \
    doctor-bad.err \
    doctor-evidence.json \
    doctor-with-evidence.json \
    doctor-export.json \
    doctor-export.out \
    fix-dry.out \
    fix-apply.out \
    fix-apply-doctor.json \
    fix-missing.err
  do
    if [ -f "$tmp/$rel" ]; then
      cp "$tmp/$rel" "$HIDEOUT_DOCTOR_SMOKE_ARTIFACT_DIR/$rel"
    fi
  done
}

browser="$tmp/hideout-browser"
cat >"$browser" <<'SH'
#!/usr/bin/env sh
exit 0
SH
chmod 700 "$browser"
go_bin="$(command -v go)"
go_path="$(dirname "$go_bin")"
tool_path="$tmp/no-tools:$go_path"
mkdir -p "$tmp/no-tools"

run_hideout() {
  PATH="$tool_path" HIDEOUT_STORE_ROOT="$store" HIDEOUT_BROWSER_PATH="$browser" "$go_bin" run ./cmd/hideout "$@"
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

run_hideout support recovery-codes --json >"$tmp/recovery-codes.json"
jq -e '
  .schema == "hideout.recovery-codes/v1" and
  ([.codes[] | select(.code == "package.prerequisite.missing")] | length) == 1 and
  ([.codes[] | select(.code == "init.proxy-secret.missing")] | length) == 1
' "$tmp/recovery-codes.json" >/dev/null

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
  ([.findings[] | select(.checkId == "feature-dns" and .status == "warn" and (.details.gateRequired | length >= 1) and (.nextActions | length >= 1))] | length) == 1
' "$tmp/doctor-dns.json" >/dev/null

run_hideout doctor --backend native --workspace "$tmp/workspace" --level deep --format json >"$tmp/doctor-deep.json"
jq -e '
  .level == "deep" and
  ([.findings[] | select(.checkId | startswith("feature-"))] | length) >= 10 and
  ([.findings[] | select(.checkId == "feature-decisions" and .status == "pass")] | length) == 1 and
  ([.findings[] | select(.checkId == "feature-adapters" and (.details.observedFacts | tostring | contains("enabledAdapters")))] | length) == 1
' "$tmp/doctor-deep.json" >/dev/null
run_hideout doctor --backend native --workspace "$tmp/workspace" --feature packaging --format json >"$tmp/doctor-packaging.json"
jq -e '
  ([.findings[] | select(.checkId == "feature-packaging" and .code == "package.prerequisite.missing" and (.details.observedFacts | tostring | contains("external-prerequisite")) and (.nextActions | tostring | contains("hideout package repair")))] | length) == 1
' "$tmp/doctor-packaging.json" >/dev/null
run_hideout doctor --backend native --workspace "$tmp/workspace" --feature decisions --format json >"$tmp/doctor-decisions.json"
jq -e '
  ([.findings[] | select(.checkId == "feature-decisions" and (.details.observedFacts | tostring | contains("timeoutRisk")))] | length) == 1
' "$tmp/doctor-decisions.json" >/dev/null

if HIDEOUT_SECRET_DEFAULT_PROXY="socks5://user:pass@127.0.0.1:1080" \
  run_hideout doctor \
  --backend native \
  --workspace "$tmp/workspace" \
  --network tun2socks \
  --proxy-secret default-proxy \
  --mediated-resolver 1.1.1.1 \
  --format json >"$tmp/doctor-redaction.json" 2>"$tmp/doctor-redaction.err"; then
  :
else
  grep -q 'doctor found errors' "$tmp/doctor-redaction.err"
fi
go run ./cmd/hideout-schema-validate schemas/doctor-report.schema.json "$tmp/doctor-redaction.json"
assert_clean "$tmp/doctor-redaction.json"
assert_clean "$tmp/doctor-redaction.err"

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
run_hideout doctor --fix --apply --backend native >"$tmp/fix-apply.out"
grep -q 'Hideout doctor fix' "$tmp/fix-apply.out"
grep -q 'task profile.create: applied risk=safe' "$tmp/fix-apply.out"
grep -q 'task schema.metadata.write: applied risk=safe' "$tmp/fix-apply.out"
test -f "$fix_store/profiles/default/profile.json"
test -f "$fix_store/install-state.json"
test -f "$fix_store/logs/init-audit.jsonl"
run_hideout doctor --backend native --workspace "$tmp/workspace" --format json >"$tmp/fix-apply-doctor.json"
jq -e '
  .summary.failed == false and
  ([.findings[] | select(.checkId == "profile" and .status == "pass")] | length) == 1
' "$tmp/fix-apply-doctor.json" >/dev/null

if run_hideout doctor --fix --backend native >"$tmp/fix-missing.out" 2>"$tmp/fix-missing.err"; then
  echo "doctor-smoke: doctor --fix without mode unexpectedly succeeded" >&2
  exit 1
fi
grep -q 'doctor --fix requires --dry-run or --apply' "$tmp/fix-missing.err"

copy_artifacts
echo "doctor-smoke: passed"
