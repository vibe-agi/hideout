# Quickstart: Operator Decision Center

<!-- markdownlint-disable MD013 -->

## Prerequisites

- Local checkout with Go toolchain.
- Existing 010 HostFS write overlay tests and smoke fixtures.
- Existing 011 adapter pack smoke fixtures.
- Existing 005 export fixtures.

## Validation Battery

```sh
go test ./internal/decision ./internal/manager ./internal/daemon ./internal/app
scripts/test-decision-center-smoke.sh
scripts/test-gate0.sh
npx --yes markdownlint-cli2 README.md 'docs/**/*.md' 'specs/012-operator-decision-center/**/*.md'
git diff --check
```

## Scenario 1: HostFS Write Appears In Generic Decisions

Requirements: FR-001, FR-003, FR-005, FR-009, FR-011, FR-013, FR-018, SC-001,
SC-004.

1. Stage a HostFS write operation through existing overlay test fixture.
2. List generic decisions.
3. Verify one `hostfs.write` decision exists with redacted preview and provider
   facts.
4. Claim it through the generic decision route.
5. Attempt a second claim and verify stale/claimed failure.
6. Apply it through the generic route and verify HostFS provider validation and
   audit.
7. Query the existing `hostfs/write/status` route and verify it reflects the
   same terminal state.

## Scenario 2: HostFS Compatibility Does Not Diverge

Requirements: FR-005, FR-018, SC-001.

1. Stage a HostFS write decision.
2. Claim through `hostfs/write/claim`.
3. Inspect through the generic decision route.
4. Verify the generic decision shows claimed state and does not expose claim
   token or token hash.
5. Discard through the generic route.
6. Verify `hostfs/write/status` shows discarded state.

## Scenario 3: Adapter Proposal Admission

Requirements: FR-006, FR-011, FR-013, SC-002.

1. Run an adapter that proposes an undeclared capability.
2. Verify the proposal fails closed and does not create an actionable decision.
3. Run an adapter that proposes a declared but unpromoted capability.
4. Verify evidence records non-applied proposal and no provider decision exists.
5. Add a promoted provider fixture when available and verify it creates an
   `adapter.proposal` decision whose approval still calls provider validation.

## Scenario 4: Share Export Decision

Requirements: FR-007, FR-008, FR-013, FR-015, FR-016, SC-003, SC-007.

1. Run a pure local audit export and verify no decision is created.
2. Run a share/leaving-machine export request.
3. Verify an `evidence.share` decision appears with redacted review preview.
4. Deny it and verify no share artifact is released.
5. Repeat and approve it with a valid claim token.
6. Verify release succeeds only after redaction/evidentiary validation.

## Scenario 5: Timeout Default Deny

Requirements: FR-012, FR-016, FR-020, SC-005.

1. Create a decision with a short per-kind timeout.
2. Wait past the timeout.
3. Attempt claim/apply.
4. Verify the decision resolves to default denial/discard/no-release and
   evidence records timeout.

## Scenario 6: Notices Are Not Decisions

Requirements: FR-002, FR-004, FR-010, FR-016, SC-006.

1. Emit a privilege degraded notice.
2. Emit a daemon background status notice.
3. List notices and decisions.
4. Verify notices contain severity/status/ack state and no claim/defaultOutcome
   fields.
5. Acknowledge both notices.
6. Verify acknowledgement evidence and no provider side effects.

## Scenario 7: Watch/Event Surfaces

Requirements: FR-014, FR-015, FR-019, SC-008.

1. Start daemon event stream.
2. Create one decision and one notice.
3. Verify CLI/TUI/WebUI consumers receive update signals.
4. Resolve the decision and acknowledge the notice.
5. Verify every surface converges on terminal/ack state and no event carries
   claim token material.

## Scenario 8: Persistence Failure Is Fail-Closed

Requirements: FR-016, FR-020, SC-009.

1. Inject a decision store write failure.
2. Attempt create/claim/apply.
3. Verify provider state does not change and audit records failure when
   possible.
4. Inject audit write failure before provider apply.
5. Verify authority fails closed before provider side effects.

## Scenario 9: Redaction Scans

Requirements: FR-015, FR-016, SC-007.

1. Create decision and notice previews containing fake broker/UI/claim tokens,
   generated machine-id-shaped values, proxy values, overlay object paths, and
   hidden store paths.
2. Inspect store records, API responses, watch events, UI output, audit, and
   export artifacts.
3. Verify all control-plane values are absent while user-facing resource facts
   needed for review remain.

## Scenario 10: Docs And Status

Requirements: FR-021, SC-010.

1. Run markdownlint over docs and specs.
2. Verify `docs/STATUS.md` marks operator decision center as implemented only
   after code is complete.
3. Verify docs describe decision and notice classes separately.
4. Verify test plan lists decision-center smoke under Gate 0 and no real-Lima
   claim is added.

## Scenario 11: Diagnostic Status

Requirements: FR-017.

1. Create one pending decision and one unacknowledged notice.
2. Run the machine-readable decision/notice status path used by doctor or future
   diagnostics.
3. Verify it reports counts, stale/timeout risk, oldest pending age, and
   redacted representative ids without reading provider-private state directly.
4. Resolve the decision and acknowledge the notice.
5. Verify the diagnostic status updates from the generic decision/notice store
   rather than from HostFS, adapter, export, privilege, or daemon-private files.
