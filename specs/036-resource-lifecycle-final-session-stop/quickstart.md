# Quickstart: Validate Resource Lifecycle And Final-Session Stop

This guide validates 036 after implementation. Local tests prove the shared
model and fail-closed contracts. Only the real macOS arm64 Lima lane proves VM
identity and final-session stop.

## Prerequisites

- Go toolchain and repository dependencies installed.
- Existing package/runtime verification passes.
- For real proof: macOS arm64, supported Lima, the pinned runtime, and no
  unrelated Hideout sessions using the disposable test store.
- Use disposable workspaces and stores for lifecycle fault injection.

The real runner executes the scenarios below as one ordered topology because
later restart/recovery assertions depend on facts created by earlier sessions.
It intentionally has no `--scenario` shortcut. Run it once and retain the
result directory:

```bash
export HIDEOUT_036_EVIDENCE="$PWD/.hideout-release-evidence/036"
scripts/test-lifecycle-lima-e2e.sh --all --require-real \
  --samples 30 --warmups 3 --iterations 100 \
  --out "$HIDEOUT_036_EVIDENCE"
```

## 1. Catalog, Reducer, And Journal

```bash
go test ./internal/lifecycle/... ./internal/backend/... ./internal/daemon ./internal/manager
scripts/test-lifecycle-smoke.sh
```

Verify that the production catalog is the only kind inventory, dependency
cycles and illegal transitions fail, journal paths cannot escape the store,
and exhaustive exploration finds no unsafe stopped state. A current session
owner record spelled `failed` must classify as orphaned/unproved.

Coverage: FR-001, FR-004, FR-005, FR-006, FR-010, FR-020, FR-021, SC-001,
SC-018.

## 2. Final One-Shot Command

Inspect the final-command assertions from the retained real lane:

```bash
jq -e '.checks.finalSessionStops and .checks.automaticStopNonDestructive and
  .checks.guestDiskRetained and .checks.profileCacheRetained and
  .checks.auditEvidenceRetained and .checks.exactObservedStop' \
  "$HIDEOUT_036_EVIDENCE/result.json"
```

Expected sequence: current incarnation observed running, planned dependency
registered before authority, command exits, cleanup proves release, a visible
15-second grace starts, and the exact instance is observed stopped within the
35-second Lima transaction bound. A guest-disk marker, profile-cache marker,
completed audit record, environment record, and staged overlay remain intact.
Re-running verifies the markers and starts a new generation/boot.

The lane compares the exact pre-036 baseline commit with the exact candidate on
the same host, runtime artifact, fixture, and alternating paired sample order;
median warm overhead must remain within 5% or 10 ms, whichever is larger.

Coverage: FR-002, FR-007, FR-009, FR-011, FR-012, FR-014, FR-017, FR-024,
FR-027, SC-003, SC-009, SC-010.

## 3. Retained HostFS State

```bash
jq -e '.checks.retainedOverlayPreserved' \
  "$HIDEOUT_036_EVIDENCE/result.json"
```

Stage a disposable HostFS overlay change but do not apply it. End the final
guest dependency, observe VM stop, compare the staged bytes, then use the
existing explicit apply/discard workflow. No automatic operation may apply,
discard, clean, or delete it.

Coverage: FR-008, FR-014, FR-025, SC-007.

## 4. Independent Host-App Handoff

```bash
jq -e '.checks.hostHandoffIndependent' \
  "$HIDEOUT_036_EVIDENCE/result.json"
```

Use a test-owned, isolated safe-mode instance of the installed Visual Studio
Code application against a disposable host-backed workspace. After the launch
handoff, end the run and observe VM stop. The host process must remain alive,
status must classify only bounded handoff history, and Hideout must not claim or
attempt to terminate the host process.

Coverage: FR-008, FR-014, FR-016, SC-005.

## 5. Sibling Sessions

```bash
jq -e '.checks.siblingSessionPreserved' \
  "$HIDEOUT_036_EVIDENCE/result.json"
```

Start two sessions in one environment. Close one and prove the other still
executes. No grace may exist until the second session and all its provider
cleanup complete. Closing the final session starts exactly one grace.

Coverage: FR-007, FR-009, FR-011, FR-015, FR-027, SC-004.

## 6. Endpoint Bridge And Network Drain

```bash
jq -e '.checks.bridgePinsEnvironment and .checks.runBridgeClosed' \
  "$HIDEOUT_036_EVIDENCE/result.json"

go test ./internal/manager -run \
  'Test(ApplyRunPlansEnvironmentNetworkBeforeProviderStart|StopEnvironmentNetworkServiceCleansOnlyServiceDirectory)'
```

The current run-scoped bridge accepts a real loopback connection while the
originating run is active, pins the VM, and refuses connections after ordered
run cleanup. Production-path Go tests separately prove that an environment
network support service is a drain rather than a self-pin and that provider
cleanup removes it before lifecycle release. No detached bridge claim is made.

Coverage: FR-007, FR-015, FR-025, SC-006, SC-013.

## 7. Attach Versus Stop Race

```bash
jq -e '.checks.attachStopRaceSafe and .metrics.attachStopRaces >= 100' \
  "$HIDEOUT_036_EVIDENCE/result.json"
```

Each iteration starts one real attach concurrently with one explicit stop while
a sibling pin remains live. Stop must not cross that dependency, the attach must
complete, and the sibling must remain usable. Separate final-command and restart
steps prove observed stop, new-incarnation attachment, and stale-generation
rejection without turning this lane into an SSH saturation benchmark.

Coverage: FR-006, FR-011, FR-017, FR-023, FR-027, SC-002.

## 8. New Backend Incarnation And Generation Fencing

```bash
jq -e '.checks.newBootGenerationObserved' \
  "$HIDEOUT_036_EVIDENCE/result.json"

go test ./internal/lifecycle ./internal/daemon -run \
  'Test.*(Boot|Generation|Incarnation|External)'
```

The real lane stops and restarts the retained Lima environment and proves that
the next run binds a strictly newer host-issued generation and a newly observed
boot identity. Production reducer and reconciliation tests inject an external
boot-identity change and prove every old deadline, resource generation, and
stop attempt becomes invalid for the new boot.

Coverage: FR-005, FR-012, FR-013, FR-023, SC-009, SC-016.

## 9. Ambiguous Stop Observation

```bash
go test ./internal/lifecycle/... ./internal/manager -run 'Stop.*Unknown|Attach.*Stopping'
```

Inject a successful stop command followed by observer timeout/malformed
inventory. Status must remain `stopping-unknown`; attachment is refused until
definitive reconciliation. No test may substitute an environment record status
for backend observation.

Coverage: FR-018, FR-024, SC-014.

## 10. Daemon Restart Reconciliation

```bash
jq -e '.checks.restartNoInheritedAuthority and
  .checks.restartFreshGraceAtMostOnce and .checks.reconciliationRetry and
  .checks.slowProbeDoesNotBlockStatus' "$HIDEOUT_036_EVIDENCE/result.json"
```

The real lane kills the daemon with two active PTY sessions, exercises slow and
transiently failed startup reconciliation, retries in the same epoch, and
checks repeated reconciliation cannot replace a fresh idle deadline. Local
transition tests cover draining, grace, and stop-attempt interruptions. In all
cases the journal remains discovery only and the replacement daemon does not
inherit child or stop authority.

Coverage: FR-010, FR-022, FR-023, SC-015.

## 11. Orphan And Explicit Recovery Stop

```bash
jq -e '.checks.explicitStaleRecovery' \
  "$HIDEOUT_036_EVIDENCE/result.json"

go test ./internal/lifecycle ./internal/daemon ./internal/manager -run \
  'Test.*(FailedCleanup|FailedOwner|Orphan|ExplicitStop)'
```

The real lane kills the daemon behind active sessions, proves automatic attach
and stop remain fail closed, and permits explicit non-destructive recovery only
after external owner absence. Production-path local tests separately retain a
record explicitly spelled `failed`, classify it orphaned/unproved, and verify
live, corrupt, and otherwise unclassified owner state all refuse stop.

Coverage: FR-010, FR-018, FR-025, FR-026, SC-008, SC-017, SC-018.

## 12. Bounded Daemon Shutdown

```bash
go test ./internal/daemon ./internal/lifecycle/... -run 'Shutdown|Budget|Deferred'
```

Give shutdown less time than provider drain/backend observation requires. The
daemon must finish within its outer budget, leave the environment warm, and
record deferred/unknown truth. It must not launch an unowned stop goroutine or
publish stopped.

Coverage: FR-022, FR-023, FR-028.

## 13. Status, Events, UI, And Redaction

```bash
scripts/test-lifecycle-smoke.sh --surfaces
```

Construct pinned, grace, retained, handoff, orphaned, stopping-unknown, and
stopped states through the production reducer. CLI, machine status, Manager,
daemon events, doctor, TUI/WebUI, and audit must agree. Inject tokens, raw
paths, proxy values, descriptors, PIDs, argv, and long target text; none may
escape the documented boundary.

Coverage: FR-018, FR-019, FR-020, SC-011, SC-012.

## 14. Named And Generated Preserved Environments

```bash
go test ./internal/lifecycle ./internal/manager ./internal/daemon \
  -run 'Environment|Final|AutomaticStop'
```

Run the same final-session sequence once with a generated default environment
and once with a named preserved environment. Both stop after final release and
grace. Neither is cleaned/deleted, and no name-specific keepalive exception is
accepted.

Coverage: FR-014, FR-017, SC-003.

## 15. Full Gate

```bash
go build ./...
go vet ./...
test -z "$(gofmt -l internal cmd)"
git diff --check
go test ./...
scripts/test-gate0.sh
scripts/test-lifecycle-lima-e2e.sh --all --require-real \
  --samples 30 --warmups 3 --iterations 100 \
  --out .hideout-release-evidence/036
```

Gate 0 proves contracts and local behavior only. Product status remains
unimplemented until the real Lima lane records all required scenarios against
the exact candidate commit and runtime identity.
