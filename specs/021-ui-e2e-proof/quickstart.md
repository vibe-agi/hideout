# Quickstart: UI E2E Proof

<!-- markdownlint-disable MD013 -->

This quickstart describes verification scenarios for 021. Commands are
implementation targets; exact flags may be adjusted during tasks, but the proof
semantics must remain the same.

## Scenario 1: Evidence Schema Validates

Requirements: FR-011, FR-012, SC-007.

1. Run a local proof command that writes an evidence manifest.
2. Validate the manifest against `schemas/product-hardening-evidence.schema.json`.
3. Assert every proof has a stable proof id, status, evidence class, covered
   claims, prerequisites, artifacts, and redaction status.

Expected command shape:

```bash
scripts/test-ui-e2e.sh --manifest-only --out /tmp/hideout-ui-evidence
go run ./cmd/hideout-schema-validate \
  schemas/product-hardening-evidence.schema.json \
  /tmp/hideout-ui-evidence/product-hardening-evidence.json
```

## Scenario 2: Browser E2E Passes With Notice Ack

Requirements: FR-001, FR-002, FR-003, FR-004, SC-001, SC-002, SC-003.

1. Start the proof fixture.
2. Open the served WebUI in a real browser context.
3. Verify all required console areas are visible.
4. Deliver a representative typed event.
5. Verify visible DOM state changes and no hidden polling occurs.
6. Click the notice acknowledgement action.
7. Verify the route, payload, response, and acknowledged visible state.
8. Record passed proof entries.

Expected command shape:

```bash
scripts/test-ui-e2e.sh --browser --out /tmp/hideout-ui-evidence
```

## Scenario 3: Browser Auth Refusal Is Visible

Requirements: FR-005, SC-010.

1. Open the WebUI with a missing or wrong token.
2. Attempt the required load or acknowledgement path.
3. Verify the page shows a refusal/error state.
4. Record failed proof if the action silently succeeds.

## Scenario 4: Missing Browser Is Not-Run, Not Pass

Requirements: FR-013, SC-006.

1. Run the proof command in a mode where browser dependency discovery is forced
   to fail.
2. Assert the manifest contains browser proof entries with `status=not-run`.
3. Assert those entries do not satisfy browser covered claims.

## Scenario 5: TUI PTY E2E Passes

Requirements: FR-006, FR-007, FR-008, SC-004, SC-005.

1. Launch the real `hideout tui` command under a PTY or equivalent terminal
   process harness.
2. Capture the initial terminal console.
3. Deliver a representative daemon event.
4. Verify terminal output changes without interval overview/audit polling while
   the stream is healthy.
5. Record passed proof entries.

Expected command shape:

```bash
scripts/test-ui-e2e.sh --tui --out /tmp/hideout-ui-evidence
```

## Scenario 6: TUI Stream Closure Is Visible

Requirements: FR-009, FR-010.

1. Start `hideout tui` under the terminal harness.
2. Close the daemon event stream or invalidate the credential.
3. Verify the captured terminal output shows stale, disconnected, or fallback
   state before daemon-less behavior.
4. If a deterministic event seam is used, assert the real command process still
   ran.

## Scenario 7: Missing PTY Is Not-Run, Not Pass

Requirements: FR-013, SC-006.

1. Force PTY/terminal harness discovery to fail.
2. Assert terminal proof entries are `not-run`.
3. Assert no terminal covered claim is satisfied by render-only unit tests.

## Scenario 8: Artifact Redaction

Requirements: FR-014, SC-008.

1. Inject canary values that match Hideout control-plane material.
2. Produce browser artifacts, terminal artifacts, logs, event summaries, and
   evidence manifest entries.
3. Assert canary values are absent or redacted.
4. Assert redaction failure prevents covered-claim satisfaction.

## Scenario 9: Proof Boundary Documentation

Requirements: FR-015, FR-016, FR-017, SC-009.

1. Scan docs and test-plan updates.
2. Assert browser E2E, terminal E2E, reducer harness, fixture-server proof,
   local-fast proof, and release-readiness evidence are distinguished.
3. Assert docs do not claim release readiness from local UI E2E alone.
4. Assert docs do not describe browser automation as a product capability.

## Scenario 10: All UI Proof Lanes

Requirements: all 021 FR and SC.

1. Run the full local UI E2E proof command.
2. Require browser and terminal proof lanes to pass in the targeted completion
   mode.
3. Validate the manifest and redaction results.
4. Fail the run if any required executed lane fails or if any required lane is
   `not-run` in completion mode.

Expected command shape:

```bash
scripts/test-ui-e2e.sh --all --require-executed --out /tmp/hideout-ui-evidence
```
