# Feature Specification: Route And Event Drift Guards

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `027-route-event-drift-guards`

**Created**: 2026-07-09

**Status**: Implemented — the exact claim surface and non-claims live in [docs/STATUS.md](../../docs/STATUS.md)

**Input**: User description: "Implement the 027 portion of `.tmp/026-028-internal-hardening-plan.md`: add practical drift guards for Manager routes, daemon endpoints, WebUI/TUI action routes, live-event producers, and live-console reducers without adding new authority or UI features."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Keep Route Inventories Tied To Production Dispatch (Priority: P1)

A maintainer changing Manager or daemon endpoints needs route inventories and
tests that are derived from, or consumed by, production dispatch logic. Adding a
route in code while forgetting the documented inventory must fail locally.

**Why this priority**: Route-count drift has already escaped review. Route
guards built as test-only hand-written lists do not catch implementation routes
that were never added to the list.

**Independent Test**: Run route catalog tests that enumerate Manager
`/api/v1` routes and daemon-specific routes from a production source of truth or
shared recognizer. The tests must fail for an inventory-only route, a recognized
route missing from inventory, and a daemon endpoint counted as a Manager route.

**Acceptance Scenarios**:

1. **Given** the production Manager route recognizer knows a route, **When**
   the route inventory omits it, **Then** the drift guard fails and reports the
   missing route.
2. **Given** the route inventory lists a route, **When** production dispatch
   does not recognize it, **Then** the drift guard fails and reports the stale
   inventory entry.
3. **Given** a daemon endpoint outside `/api/v1`, **When** route classification
   runs, **Then** it is documented as daemon-specific and is not counted as a
   Manager route.

---

### User Story 2 - Prove Live Events Have Honest Producers And Reducers (Priority: P2)

A maintainer adding or changing a live-console event needs a catalog that names
the event kind, producer kind, remap behavior, required fields, redaction
expectation, production source, and reducer coverage. Reducer branches without
real emit sources must be explicitly seed-only or test-only.

**Why this priority**: Previous reviews found reducer cases that existed
without production emit sources and Go/JavaScript reducer behavior that could
diverge.

**Independent Test**: Run live-event catalog tests that check every cataloged
production event has an emit source and reducer coverage, every reducer branch
has a catalog entry, every producer kind maps to a catalog row or explicit
remap, and unknown-kind forward compatibility is tested.

**Acceptance Scenarios**:

1. **Given** a production event kind, **When** the event catalog is validated,
   **Then** it names required fields, redaction expectation, producer source,
   and reducer coverage for Go and JavaScript where relevant.
2. **Given** a reducer branch for a kind with no producer, **When** no seed-only
   or test-only marker exists, **Then** the drift guard fails.
3. **Given** a producer kind that remaps to another event kind, **When** catalog
   validation runs, **Then** the remap must be declared and exercised by a test.

---

### User Story 3 - Verify UI Action Routes At Runtime (Priority: P3)

WebUI and TUI actions should remain thin consumers of existing Manager or daemon
routes. Tests must observe actual runtime requests rather than source-code grep,
so broken fetch paths, request methods, token wiring, or daemon/Manager class
mix-ups fail.

**Why this priority**: Source-string checks previously looked green while
runtime action wiring could still be wrong.

**Independent Test**: Run a WebUI runtime action-route test with a fixture
server or browser request interception that records actual `fetch` requests,
plus a TUI action-route check for existing action invocations where practical.

**Acceptance Scenarios**:

1. **Given** the WebUI renders an action button, **When** a browser/runtime
   harness clicks it, **Then** the recorded request method and path must match a
   recognized Manager or daemon route.
2. **Given** the WebUI targets an unknown path, **When** the runtime guard runs,
   **Then** the test fails without relying on source text grep.
3. **Given** the TUI exposes an existing action route, **When** the action is
   invoked in the test seam or PTY harness, **Then** it targets a recognized
   route and does not invent authority outside the Manager/daemon route
   classes.

### Edge Cases

- Existing switch-based handlers may remain, but tests must consume a
  production recognizer/table, not a purely test-only list.
- Daemon endpoints such as lifecycle, event stream, and background work are
  product endpoints but not Manager `/api/v1` routes.
- WebUI may build request paths dynamically; route guards must inspect runtime
  requests or a shared action descriptor rather than static JavaScript text.
- Event producer operation kinds may intentionally remap to another event kind;
  remaps are valid only when cataloged and tested.
- Unknown event kinds must preserve forward compatibility: they may be ignored
  while stream sequence and live/stale state remain coherent.
- Seed-only panels are allowed, but they must be explicitly marked so a panel
  is not mistaken for a live event surface.
- Healthy daemon event streams must not reintroduce steady-state polling.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Manager route metadata, daemon endpoint metadata,
  live-console event metadata, WebUI/TUI action wiring tests, and local evidence
  checks. No host, filesystem, network, backend, profile, script, or approval
  authority is added.
- **Fail-closed behavior**: Missing route inventory, stale inventory route,
  daemon/Manager class confusion, uncataloged event producer, reducer branch
  without source/seed/test marker, missing required event fields, and unknown UI
  action routes fail local validation before any product-hardening proof can
  pass.
- **User authority and policy**: Existing Manager/daemon routes and operator
  decisions remain unchanged. UI surfaces still call only existing typed routes.
- **Generality and provider scope**: Generic Hideout route/event infrastructure.
  No named app, package manager, browser, terminal, agent, backend quirk, or
  proxy port becomes Core semantics.
- **Evidence surface**: Route catalog tests, live-event catalog tests, runtime
  WebUI/TUI action-route proof, UI E2E product-hardening evidence, Gate 0 smoke,
  and docs/test-plan updates.
- **Secret/redaction boundary**: Event and UI action tests must not leak
  operator tokens, claim tokens, secret refs, machine IDs, or hidden
  control-plane paths. Redaction expectations are part of the event catalog.
- **Backend/gate expectation**: Gate 0 and local product-hardening E2E cover
  this feature. It adds no real Lima, DNS, HostFS data-plane, privilege, or
  release readiness claim.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST expose a production-derived Manager route
  inventory or recognizer for all Manager `/api/v1` routes.
- **FR-002**: The system MUST expose a production-derived daemon endpoint
  inventory or recognizer for daemon-specific routes outside `/api/v1`.
- **FR-003**: Route drift tests MUST fail when a production-recognized route is
  absent from inventory or an inventory route is not recognized by production
  dispatch.
- **FR-004**: Route classification MUST keep Manager `/api/v1` routes separate
  from daemon-specific endpoints.
- **FR-005**: WebUI action-route tests MUST observe runtime requests or shared
  action descriptors; source-code grep MUST NOT be the proof.
- **FR-006**: WebUI action-route validation MUST reject unknown route targets,
  wrong Manager/daemon route class, and unsupported methods for covered
  actions.
- **FR-007**: TUI action-route validation MUST cover existing action invocations
  where the TUI exposes an action path or command surface.
- **FR-008**: The system MUST define a live-event catalog covering event kind,
  producer kind, remap/default behavior, required fields, redaction
  expectation, production source, reducer coverage, and seed/test-only status.
- **FR-009**: Every production event producer kind MUST map to a catalog entry
  or an explicitly cataloged remap.
- **FR-010**: Every reducer branch MUST map to a production event, a seed-only
  row, or a test-only row.
- **FR-011**: Event catalog validation MUST fail when a production event lacks
  required fields, redaction expectation, producer source, or reducer coverage.
- **FR-012**: Go and JavaScript reducer expectations MUST remain aligned for
  WebUI-relevant event kinds and unknown-kind behavior.
- **FR-013**: Existing UI E2E product-hardening lanes MUST keep passing and
  remain local product-hardening evidence only.
- **FR-014**: Healthy daemon event streams MUST NOT reintroduce steady-state
  overview/audit polling.
- **FR-015**: Gate 0 MUST include 027 route/event drift validation without
  materially increasing normal Gate 0 runtime.

### Key Entities *(include if feature involves data)*

- **Route Inventory Entry**: Route method, path pattern, route class
  (`manager-api` or `daemon-specific`), dispatch owner, and optional action
  surface references.
- **Route Recognizer**: Production-consumed or production-derived helper that
  classifies method/path pairs without duplicating a test-only route list.
- **UI Action Route**: Runtime WebUI or TUI action target with method, path,
  route class, token/auth expectation, and action owner.
- **Live Event Catalog Entry**: Event kind, producer kind, remap/default rule,
  required fields, redaction expectation, production source, reducer coverage,
  and seed/test-only marker.
- **Reducer Coverage Record**: Evidence that Go reducer and, where applicable,
  JavaScript reducer behavior is tested for an event kind.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of Manager `/api/v1` routes are recognized and inventoried
  from the same production truth source.
- **SC-002**: 100% of daemon-specific endpoints are inventoried separately from
  Manager routes.
- **SC-003**: Drift fixtures for missing inventory route, stale inventory route,
  and daemon/Manager misclassification fail as expected.
- **SC-004**: 100% of covered WebUI runtime action requests target recognized
  routes with expected methods and route classes.
- **SC-005**: 100% of production event producer kinds are cataloged or
  explicitly remapped.
- **SC-006**: 100% of reducer branches are tied to production, seed-only, or
  test-only catalog rows.
- **SC-007**: Go and JavaScript reducer behavior remains aligned for all
  WebUI-relevant catalog rows and unknown event kinds.
- **SC-008**: Existing UI E2E product-hardening evidence still passes without
  claiming release readiness.
- **SC-009**: Gate 0 includes 027 validation and completes successfully.

## Assumptions

- A small production route table is preferred if it can stay scoped; otherwise a
  production recognizer shared by dispatch and tests is acceptable.
- Event production source declarations are manual because static discovery would
  be fragile; validation makes the manual declarations accountable.
- Seed-only status is a first-class state, not a loophole for pretending panels
  are live.
- 027 may reorganize route/event metadata and tests, but it must not add new
  user-visible product capability.
