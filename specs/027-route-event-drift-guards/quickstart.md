# Quickstart: Route And Event Drift Guards

<!-- markdownlint-disable MD013 -->

## Scenario 1: Manager Route Drift

Requirement coverage: FR-001, FR-003, SC-001, SC-003.

Run:

```bash
go test ./internal/manager -run 'Route|Inventory|Recognizer'
```

Expected result:

- all production-recognized Manager routes are inventoried;
- stale inventory routes fail fixture tests;
- dynamic/member routes are represented by patterns and tested.

## Scenario 2: Daemon Endpoint Separation

Requirement coverage: FR-002, FR-004, SC-002, SC-003.

Run:

```bash
go test ./internal/daemon -run 'Endpoint|Route|Parity'
```

Expected result:

- daemon endpoints are inventoried outside `/api/v1`;
- Manager route parity remains intact;
- daemon endpoints are not counted as Manager routes.

## Scenario 3: Runtime WebUI Action Route Proof

Requirement coverage: FR-005, FR-006, SC-004.

Run:

```bash
go test ./internal/manager -run 'WebUI.*Action.*Runtime|ActionRoutes'
```

Expected result:

- the test executes served JavaScript or shared runtime action descriptors;
- actual requests are recorded;
- covered routes are recognized and classed correctly;
- no source-string grep is accepted as the proof.

## Scenario 4: Live Event Catalog Drift

Requirement coverage: FR-008, FR-009, FR-010, FR-011, SC-005, SC-006.

Run:

```bash
go test ./internal/liveconsole ./internal/daemon -run 'Catalog|Producer|Reducer|Representative'
```

Expected result:

- every production producer kind is cataloged or explicitly remapped;
- every reducer branch has production, seed-only, or test-only status;
- required fields and redaction expectations are tested.

## Scenario 5: Go/JavaScript Reducer Alignment

Requirement coverage: FR-012, FR-014, SC-007.

Run:

```bash
go test ./internal/manager ./internal/app -run 'LiveConsole|TUI|WebUI'
```

Expected result:

- served JavaScript reducer behavior matches Go reducer expectations for
  WebUI-relevant rows;
- unknown kinds remain forward-compatible;
- healthy event streams do not perform steady-state polling.

## Scenario 6: Gate 0

Requirement coverage: FR-013, FR-015, SC-008, SC-009.

Run:

```bash
scripts/test-gate0.sh
```

Expected result:

- UI E2E product-hardening evidence still passes;
- 027 drift guards are part of Gate 0;
- no release-readiness claim is introduced.
