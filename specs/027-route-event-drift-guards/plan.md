# Implementation Plan: Route And Event Drift Guards

<!-- markdownlint-disable MD013 -->

**Branch**: `027-route-event-drift-guards` | **Date**: 2026-07-09 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/027-route-event-drift-guards/spec.md`

## Summary

Add cheap but production-grounded drift guards for existing route and live-event
surfaces. Manager `/api/v1` and daemon-specific endpoints get shared route
metadata/recognition used by production dispatch or by a production-adjacent
recognizer. Live-console events get a structured catalog tying producer kinds,
remaps, required fields, redaction, reducer coverage, and seed/test-only status
together. WebUI/TUI action guards move from source-string assertions to runtime
request/action observation. The feature adds no new route, event, UI panel, or
authority.

## Technical Context

**Language/Version**: Go 1.x project with embedded JavaScript snippets executed
in tests through goja.

**Primary Dependencies**: Standard library HTTP/router code, existing
`github.com/dop251/goja` tests, existing daemon/liveconsole/manager packages.

**Storage**: N/A. No durable state or schema migration.

**Testing**: `go test ./internal/manager ./internal/daemon ./internal/liveconsole ./internal/app`, UI E2E package tests, Gate 0.

**Target Platform**: Local developer/dogfood host. No backend-specific runtime
dependency.

**Project Type**: Go CLI/local control plane with embedded WebUI/TUI surfaces.

**Performance Goals**: Route/event validation must be unit-test level and not
materially slow Gate 0.

**Constraints**: No OpenAPI generator, no frontend framework change, no route
framework rewrite, no new UI/daemon authority, no healthy-stream polling.

**Scale/Scope**: Existing Manager route surface, daemon endpoint surface,
current liveconsole event kinds, current WebUI/TUI action surfaces.

## Constitution Check

*GATE: Pass before Phase 0 research. Re-check after Phase 1 design.*

- **Privacy Boundary**: Only route metadata, event metadata, reducer tests, and
  UI action tests are touched. No host/filesystem/network/backend authority is
  added. Drift validation fails closed before product-hardening proof can pass.
- **Typed Authority**: Existing Manager and daemon handlers remain the only
  authority executors. Route/event catalogs only classify and test those paths.
- **Workspace And Policy**: No workspace, HostFS grant, proxy secret, profile,
  or policy mutation.
- **Generality And Provider Scope**: Generic Hideout route/event maintenance
  guard. No provider, browser, agent, package manager, or backend quirk becomes
  product semantics.
- **Evidence And Redaction**: Evidence is local unit/smoke output plus UI E2E
  product-hardening evidence. Catalogs include redaction expectation; tests
  continue to reject control-plane material.
- **Backend And Distribution**: Native/real backend behavior is unchanged. No
  helper binary or package artifact added.
- **Gates**: Gate 0 is required. Existing UI E2E product-hardening lane remains
  local proof only, not release readiness.
- **Status And Docs**: Update `docs/privacy-run-test-plan.md` and
  `docs/STATUS.md` to describe route/event drift guards after implementation.

**Post-design re-check**: PASS. Research and contracts keep all changes inside
metadata, validation helpers, and tests; no product authority is expanded.

## Project Structure

### Documentation (this feature)

```text
specs/027-route-event-drift-guards/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── route-inventory.md
│   └── live-event-catalog.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/manager/
├── api.go                    # route recognizer/table consumed by dispatch
├── api_routes_test.go        # Manager route drift tests
├── server.go                 # WebUI action runtime still served here
└── server_liveconsole_test.go # runtime WebUI action-route proof

internal/daemon/
├── server.go                 # daemon endpoint inventory/recognizer
├── server_test.go            # daemon endpoint separation + Manager parity
└── events.go                 # producer-kind declarations checked by catalog

internal/liveconsole/
├── catalog.go                # structured event catalog
├── catalog_test.go           # catalog/schema/reducer/source drift tests
├── reducer.go                # reducer branch coverage remains here
└── validate.go               # required-field validation tied to catalog

internal/app/
└── app_liveconsole_test.go   # TUI event/action route checks where applicable

scripts/
└── test-live-console-smoke.sh # Gate 0 smoke remains the aggregate lane
```

**Structure Decision**: Keep the existing packages. Add small shared route and
event metadata in the package that owns the behavior instead of introducing a
new framework or generator.

## Complexity Tracking

No constitutional violations.
