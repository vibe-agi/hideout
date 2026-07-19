#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

# Deterministic 035 Gate 0. This proves domain, race, schema, redaction and
# surface contracts only. It deliberately cannot establish a real Lima mount,
# ordinary-target isolation, host TCC behavior, VM reuse/stop, or performance.

go test -count=1 \
  ./internal/workspaceattach \
  ./internal/workspaceportal \
  ./internal/backend/lima \
  -run '^(TestSelected|TestPortal|TestWorkspace|TestSharedMachine|TestAlias|TestPathIdentity|TestCaptureRoot|TestIdentity|TestAdmission|TestClassifyRoots|TestRootRelation)'

go test -count=1 \
  ./internal/environment \
  ./internal/manager \
  ./internal/daemon \
  ./internal/lifecycle \
  -run '^(TestEnvironmentModes|TestOldAlphaRecord|TestShared|TestTwoDisjoint|TestWorkspace|TestApplyRunWorkspace|TestDaemon.*Workspace|TestOverviewSeparatesShared|TestScopeOverviewKeepsMachine|TestWebUIWorkspace|TestConcurrentSessionEvidence|TestActiveSessionSummary|TestDirectNetwork|TestEnvironmentNetwork|TestLastOwner|TestUnprovableSibling)'

go test -count=1 \
  ./internal/broker \
  ./internal/hostcap \
  ./internal/liveconsole \
  ./internal/doctor \
  ./internal/productevidence \
  -run '^(TestProjectionWorkspaceAuthority|TestOpenBoundResource|TestWorkspaceView|TestWorkspaceDiagnostics|TestProofRegistryCovers035)'

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

echo "shared-workspace Gate 0 passed (local contracts only; no real Lima claim)"
