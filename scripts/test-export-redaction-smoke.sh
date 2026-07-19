#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
. "$ROOT/scripts/lib/daemon-temp.sh"

command -v jq >/dev/null 2>&1 || { echo "export-redaction-smoke: jq required" >&2; exit 127; }

tmp="$(hideout_mktemp_daemon_store)"
store="$tmp/store"
cleanup() {
	if [ -d "$store" ]; then
		HIDEOUT_STORE_ROOT="$store" go run ./cmd/hideout daemon stop >/dev/null 2>&1 || true
	fi
	rm -rf "$tmp"
}
trap cleanup EXIT

export HIDEOUT_STORE_ROOT="$store"

go run ./cmd/hideout init --no-input --profile default --template dev --backend native --network direct >/dev/null

profile_path="$store/profiles/default/profile.json"
policy_path="$store/profiles/default/policy/export-redact.js"
jq '.policy.scriptRefs = [{"id":"export-redact","path":"policy/export-redact.js","entrypoints":["redactAudit"]}]' "$profile_path" >"$tmp/profile.json"
mv "$tmp/profile.json" "$profile_path"
cat >"$policy_path" <<'JS'
function redactAudit(ctx) {
  const details = ctx.details;
  const selected = ctx.extra.exportRedaction || [];
  for (let i = 0; i < selected.length; i++) {
    const key = selected[i];
    if (Object.prototype.hasOwnProperty.call(details, key)) {
      details[key] = "REDACTED_BY_POLICY";
    }
  }
  return { details };
}
JS

session_id="ses_export_smoke"
audit_dir="$store/sessions/$session_id"
mkdir -p "$audit_dir"
cat >"$audit_dir/audit.jsonl" <<EOF
{"time":"2026-07-07T00:00:00Z","session":"$session_id","profile":"default","backend":"native","action":"host.open","decision":"allow","details":{"target":"audit-secret","note":"keep-audit","command":"open","capabilityToken":"cap_0123456789abcdef0123456789abcdef","machineId":"0123456789abcdef0123456789abcdef"}}
EOF

assert_clean_artifact() {
  artifact="$1"
  go run ./cmd/hideout-schema-validate schemas/export-artifact.schema.json "$artifact"
  if grep -E 'HIDEOUT_SECRET|capabilityToken|cap_[0-9a-f]{16,}|0123456789abcdef0123456789abcdef' "$artifact" >/dev/null; then
    echo "export-redaction-smoke: control-plane material leaked in $artifact" >&2
    cat "$artifact" >&2
    exit 1
  fi
}

audit_artifact="$tmp/audit-export.json"
go run ./cmd/hideout audit export --session "$session_id" --out "$audit_artifact" --redact target >/dev/null
assert_clean_artifact "$audit_artifact"
jq -e '
  .provenance.source == "audit" and
  (.provenance.redactionStages[] | select(.id == "export-redact" and (.sha256 | test("^[a-f0-9]{64}$")))) and
  (.body[0].details.target == "REDACTED_BY_POLICY") and
  (.body[0].details.note == "keep-audit") and
  (.body[0].details.command == "open")
' "$audit_artifact" >/dev/null

bad_artifact="$tmp/evidentiary-fail.json"
if go run ./cmd/hideout audit export --session "$session_id" --out "$bad_artifact" --redact command >/dev/null 2>"$tmp/evidentiary.err"; then
  echo "export-redaction-smoke: evidentiary redaction unexpectedly passed" >&2
  exit 1
fi
test ! -e "$bad_artifact"
grep -q 'non-redactable evidentiary' "$tmp/evidentiary.err"

bundle_dir="$tmp/bundle"
mkdir -p "$bundle_dir"
cat >"$bundle_dir/test-release-dogfood.log" <<'EOF'
bundle log HIDEOUT_SECRET_PROXY=secret
EOF
cat >"$bundle_dir/manifest.json" <<EOF
{"schema":"hideout.release-dogfood.v1","evidence":{"directory":"$bundle_dir","log":"test-release-dogfood.log"},"gates":["gate0"],"secretField":"bundle-secret","capabilityToken":"cap_0123456789abcdef0123456789abcdef"}
EOF
bundle_artifact="$tmp/bundle-export.json"
go run ./cmd/hideout audit export --source bundle --bundle "$bundle_dir" --policy-profile default --out "$bundle_artifact" --redact secretField >/dev/null
assert_clean_artifact "$bundle_artifact"
jq -e '
  .provenance.source == "bundle" and
  .body.secretField == "REDACTED_BY_POLICY" and
  (.body.evidence.logContent | contains("bundle log")) and
  (.body.evidence.directory? == null)
' "$bundle_artifact" >/dev/null
if grep -q "$bundle_dir" "$bundle_artifact"; then
  echo "export-redaction-smoke: bundle artifact retained local bundle path" >&2
  exit 1
fi

boundary_session="ses_export_boundary"
boundary_audit="$store/sessions/$boundary_session/audit.jsonl"
mkdir -p "$(dirname "$boundary_audit")"
cat >"$boundary_audit" <<EOF
{"time":"2026-07-07T00:00:00Z","session":"$boundary_session","profile":"default","backend":"native","action":"host.open","decision":"allow","details":{"target":"boundary-secret","source":"workspace","command":"open","capabilityToken":"cap_0123456789abcdef0123456789abcdef"}}
EOF
boundary_artifact="$tmp/boundary-export.json"
go run ./cmd/hideout audit export --source boundary-summary --from "$boundary_audit" --policy-profile default --out "$boundary_artifact" --redact target >/dev/null
assert_clean_artifact "$boundary_artifact"
jq -e '
  .provenance.source == "boundary-summary" and
  (.body.audit[0].details.target == "REDACTED_BY_POLICY") and
  (.body.auditPath? == null)
' "$boundary_artifact" >/dev/null
if grep -q "$boundary_audit" "$boundary_artifact"; then
  echo "export-redaction-smoke: boundary artifact retained auditPath" >&2
  exit 1
fi

echo "export-redaction-smoke: passed"
