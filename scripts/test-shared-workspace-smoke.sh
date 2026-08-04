#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/lib/gate2-shared-workspace-path.sh
. "$ROOT/scripts/lib/gate2-shared-workspace-path.sh"
# shellcheck source=scripts/lib/gate2-shared-workspace.sh
. "$ROOT/scripts/lib/gate2-shared-workspace.sh"

# Deterministic 035 Gate 0. This proves domain, race, schema, redaction and
# surface contracts only. It deliberately cannot establish a real Lima mount,
# ordinary-target isolation, host TCC behavior, VM reuse/stop, or performance.

go test -count=1 \
	./internal/workspacepath \
  ./internal/workspaceattach \
  ./internal/workspaceportal \
  ./internal/backend/lima \
	-run '^(TestSelected|TestPortal|TestWorkspace|TestSharedMachine|TestAlias|TestPathIdentity|TestResolveGuest|TestBinding|TestCaptureRoot|TestIdentity|TestAdmission|TestClassifyRoots|TestRootRelation)'

go test -count=1 \
  ./internal/environment \
  ./internal/manager \
  ./internal/daemon \
  ./internal/lifecycle \
	-run '^(TestEnvironmentModes|TestOldAlphaRecord|TestShared|TestPromotedSharedSelection|TestPlanRunRejectsAlias|TestTwoDisjoint|TestWorkspace|TestApplyRunWorkspace|TestDaemon.*Workspace|TestOverviewSeparatesShared|TestScopeOverviewKeepsMachine|TestWebUIWorkspace|TestConcurrentSessionEvidence|TestActiveSessionSummary|TestDirectNetwork|TestEnvironmentNetwork|TestLastOwner|TestUnprovableSibling)'

go test -count=1 \
  ./internal/broker \
  ./internal/hostcap \
  ./internal/liveconsole \
  ./internal/doctor \
  ./internal/productevidence \
	-run '^(TestProjectionWorkspaceAuthority|TestNormalizeBrokerRequestCWD|TestMapGuestPath|TestOpenBoundResource|TestWorkspaceView|TestWorkspaceDiagnostics|TestProofRegistryCovers035|TestSharedWorkspace)'

# macOS cannot execute the Linux observer/supervisor tests, but Gate 0 must at
# least type-check both Linux test binaries before a packaged Lima run.
linux_compile_tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-035-linux-compile.XXXXXX")"
cleanup_linux_compile() {
  find "$linux_compile_tmp" -depth -delete 2>/dev/null || true
}
trap cleanup_linux_compile EXIT
GOOS=linux GOARCH=arm64 go test -c \
  -o "$linux_compile_tmp/hideout-observer.test" ./cmd/hideout-observer
GOOS=linux GOARCH=arm64 go test -c \
  -o "$linux_compile_tmp/hideout-session-supervisor.test" ./cmd/hideout-session-supervisor

# The transition and admission owners are shared across CLI processes. Run the
# focused contention/model checks under the race detector rather than relying
# on an ordinary all-package pass to imply race coverage.
go test -race -count=1 \
  ./internal/workspaceattach \
  ./internal/lifecycle \
  -run '^(TestIdentityKeyConcurrentCreation|TestPortalMultiplexesBinaryFramesOutOfOrder|TestPortalSaturationDoesNotStarveSiblingOrTeardown|TestWorkspaceAttach)'

for schema in \
  schemas/workspace-attachment.schema.json \
  schemas/environment-summary.schema.json \
  schemas/daemon-status.schema.json \
  schemas/daemon-event.schema.json \
  schemas/lifecycle-journal.schema.json \
  schemas/lifecycle-status.schema.json; do
  test -f "$schema"
  jq empty "$schema"
done

# Prove the evidence judges fail closed on incomplete positives and on a
# negative fixture that fails for any reason other than the intended inode
# divergence. Product evidence validation repeats this independently in Go.
judge_tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-035-path-judge.XXXXXX")"
required_checks="$(gate2_035_path_required_checks_json)"
path_limitations="$(gate2_035_path_limitations_json)"
(exit 17) &
readiness_pid=$!
readiness_started=$SECONDS
if gate2_035_wait_file "$judge_tmp/never-ready" "intentional early exit" 100 \
  "$readiness_pid" 2>"$judge_tmp/readiness.err"; then
  echo "shared-workspace Gate 0: readiness wait ignored an exited owner" >&2
  exit 1
fi
test "$((SECONDS - readiness_started))" -lt 5
grep -F 'owner exited before readiness (status=17)' "$judge_tmp/readiness.err" >/dev/null
jq -n --argjson required "$required_checks" --argjson limitations "$path_limitations" '{
  schema:"hideout.shared-workspace-path-correctness/v1",status:"passed",
  tools:["bash","claude","codex","git","go","node","python"],
  representativeAgents:["claude","codex"],
  limitations:$limitations,
  checks:(reduce $required[] as $key ({}; .[$key] = true))
}' >"$judge_tmp/positive.json"
gate2_035_path_correctness_judge "$judge_tmp/positive.json"
jq 'del(.checks.resolvedFileAuditLogical)' "$judge_tmp/positive.json" >"$judge_tmp/incomplete.json"
if gate2_035_path_correctness_judge "$judge_tmp/incomplete.json"; then
  echo "shared-workspace Gate 0: incomplete path evidence was accepted" >&2
  exit 1
fi
jq '.status = "failed" | .checks.logicalPhysicalSameObject = false' \
  "$judge_tmp/positive.json" >"$judge_tmp/negative.json"
gate2_035_path_negative_fixture_judge "$judge_tmp/negative.json"
jq '.checks.resolvedFileAuditLogical = false' "$judge_tmp/negative.json" \
  >"$judge_tmp/wrong-negative.json"
if gate2_035_path_negative_fixture_judge "$judge_tmp/wrong-negative.json"; then
  echo "shared-workspace Gate 0: ambiguous negative path fixture was accepted" >&2
  exit 1
fi
jq -n '{records:[
  {kind:"file",operation:"open",subject:{kind:"file",path:"/workspace/a",pathClass:"workspace",pathState:"resolved"}},
  {kind:"file",operation:"metadata",subject:{kind:"file",path:"a",pathState:"aliased"}},
  {kind:"file",operation:"mkdir",subject:{kind:"file",path:"/d",pathState:"aliased"}},
  {kind:"file",operation:"rename",subject:{kind:"file",path:"a",targetPath:"b",pathState:"aliased"}},
  {kind:"file",operation:"rmdir",subject:{kind:"file",path:"/d",pathState:"aliased"}},
  {kind:"file",operation:"unlink",subject:{kind:"file",path:"b",pathState:"aliased"}},
  {kind:"process",subject:{kind:"process",argv:["bash","/workspace/a"]},truncation:["cwd-unavailable"]},
  {kind:"process",subject:{kind:"process",argv:["physical-probe","[UNBOUND_WORKSPACE_PATH]"]},truncation:["argv-truncated","cwd-unavailable"]},
  {kind:"process",subject:{kind:"process",argv:["sibling-probe"]},truncation:["argv-unavailable","cwd-unavailable"]}
]}' >"$judge_tmp/activity.json"
gate2_035_path_activity_judge "$judge_tmp/activity.json"
jq '.records |= map(select(.operation != "unlink"))' "$judge_tmp/activity.json" \
  >"$judge_tmp/activity-missing-alias.json"
if gate2_035_path_activity_judge "$judge_tmp/activity-missing-alias.json"; then
  echo "shared-workspace Gate 0: incomplete relative-path coverage was accepted" >&2
  exit 1
fi
jq '(.records[] | select(.kind == "process" and .subject.argv[0] == "physical-probe") |
  .subject.argv[1]) = "/hideout/workspaces/wrk_leaked"' "$judge_tmp/activity.json" \
  >"$judge_tmp/activity-leak.json"
if gate2_035_path_activity_judge "$judge_tmp/activity-leak.json"; then
  echo "shared-workspace Gate 0: physical workspace activity leak was accepted" >&2
  exit 1
fi
find "$judge_tmp" -depth -delete
cleanup_linux_compile
trap - EXIT

echo "shared-workspace Gate 0 passed (local contracts only; no real Lima claim)"
