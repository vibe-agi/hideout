# Quickstart: Operator Console MVP

<!-- markdownlint-disable MD013 -->

## Scenario 1: WebUI Panels

Requirement coverage: FR-001, FR-003, FR-007, FR-010, SC-001.

Run the WebUI render tests with seeded decisions, notices, doctor status,
package/support status, environments, and background operations.

Expected:

- All required panels render.
- Action Required shows decision/notice counts.
- Empty states are visible.
- Package/support is read-only.

## Scenario 2: TUI Compact Model

Requirement coverage: FR-002, FR-009, SC-002, SC-005.

Run TUI render/watch tests with the same seeded console model.

Expected:

- TUI shows action-required counts and summaries.
- Healthy stream does not run interval polling.
- Closed stream falls back visibly.

## Scenario 3: Existing Decision And Notice Actions

Requirement coverage: FR-004, FR-005, FR-006, SC-003.

Seed a HostFS write decision, an export/share decision, and an informational
notice. Exercise console action handlers.

Expected:

- Decision claim/resolve uses existing Manager routes.
- Notice ack uses existing notice route.
- Stale/expired/denied states are shown.
- Staged HostFS content is not rendered.

## Scenario 4: Explicit Doctor

Requirement coverage: FR-008, SC-007.

Render the console on page load, then invoke the explicit doctor action.

Expected:

- Page load does not run doctor.
- Explicit action runs local light doctor and displays the report status.
- Doctor errors are displayed as status, not automatic repair prompts.

## Scenario 5: Redaction

Requirement coverage: FR-011, SC-006.

Inject token-like values, provider refs, proxy backing values, runtime paths, and
staged HostFS content canaries into model/action fixtures.

Expected:

- WebUI output contains zero raw canaries.
- TUI output contains zero raw canaries.

## Scenario 6: Gate 0 Console Smoke

Requirement coverage: FR-012, FR-014, SC-004, SC-008.

Run:

```sh
scripts/test-gate0.sh
```

Expected:

- Console smoke covers WebUI panels, TUI compact panels, action-required
  visibility, stream fallback, and redaction scan.
