# Implementation Plan: Operator Console MVP

<!-- markdownlint-disable MD013 -->

**Branch**: `019-operator-console-mvp` | **Date**: 2026-07-09 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/019-operator-console-mvp/spec.md`

## Summary

Organize existing local-control-plane state and actions into an operator console for WebUI and TUI without adding authority. The implementation extends the current daemon/live-console surface: `internal/liveconsole.State` already carries overview, decisions, notices, HostFS writes, background work, export, cleanup, stream health, audit tails, and redacted reducer state; `internal/app.writeTUILiveDashboard` already renders compact live state; `internal/manager/server.go` already serves the WebUI and executes the real JavaScript reducer in tests. 019 adds a shared console model over those facts, WebUI action-required/status panels, compact TUI summaries, explicit light-doctor trigger/cached report display, read-only package/support status, and tests that execute real WebUI/TUI code paths.

## Technical Context

**Language/Version**: Go 1.25 module plus existing inline WebUI JavaScript served by Manager.

**Primary Dependencies**: Existing standard library, Goja test harness already used for WebUI reducer tests, existing Manager/daemon/liveconsole/decision packages. No Flutter, Bubbletea, frontend framework, or new daemon transport dependency.

**Storage**: Existing Hideout store decision/notice records, daemon event stream seed, Manager overview, doctor report cache/evidence when explicitly created, package install state, support matrix, and local audit.

**Testing**: `go test ./...`, targeted liveconsole/manager/app tests, Goja execution of served WebUI console reducer/action logic, TUI render/watch tests, console smoke in Gate 0, markdownlint, gofmt, vet, diff-check.

**Target Platform**: macOS and Linux local operator surfaces. WebUI remains loopback/daemon-local; TUI remains terminal/local.

**Project Type**: Single Go CLI/local-control-plane application with embedded local WebUI.

**Performance Goals**: Console renders from existing overview/seed data quickly and must not run hidden steady-state polling while a healthy stream is active. Explicit refresh/doctor action may run local commands on demand.

**Constraints**: No new authority. All mutations go through existing Manager decision/notice routes, typed plan/apply routes, or explicit CLI/package/doctor commands. Page load must not run doctor automatically. Package/support is read-only in 019.

**Scale/Scope**: Single-operator local machine with small-to-moderate counts of decisions, notices, environments, background operations, doctor findings, and status panels.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **Privacy Boundary**: 019 reads and renders existing local state. It touches decision/notice routes only through existing claim/resolve/ack actions and does not add host/network/backend/HostFS/package/script authority.
- **Typed Authority**: Existing Manager and daemon routes remain the only execution path. WebUI/TUI can request existing actions but cannot invent action names or bypass claim tokens.
- **Workspace And Policy**: No workspace mounts, HostFS grants, profile policy, proxy secrets, or package state are changed by the console itself. HostFS writes remain decisions over existing overlay records.
- **Generality And Provider Scope**: Panels use generic product concepts. Backend-specific labels remain facts, not new semantics.
- **Evidence And Redaction**: WebUI/TUI output, reducer tests, console smoke, and existing Manager audit prove behavior. Rendering redacts claim tokens/provider refs/control-plane data and never displays staged HostFS content.
- **Backend And Distribution**: No new backend capability or helper. Uses existing WebUI/TUI/daemon packaging.
- **Gates**: Gate 0 with unit tests and console smoke. No real Lima gate because no isolation claim changes.
- **Status And Docs**: Update `docs/STATUS.md`, `docs/tui-webui-experience.md`, `docs/manager-control-plane.md`, and `docs/privacy-run-test-plan.md`.

## Project Structure

### Documentation (this feature)

```text
specs/019-operator-console-mvp/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── console-model.md
│   └── console-actions.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/liveconsole/
├── model.go                 # console rows/status fields if needed
├── reducer.go               # event-to-console state reducer
└── *_test.go

internal/manager/
├── server.go                # WebUI panels and JS action wiring
├── server_liveconsole_test.go
└── api_test.go              # existing decision/notice route tests

internal/app/
├── app.go                   # TUI compact console rendering
├── app_liveconsole_test.go
└── app_test.go

scripts/test-live-console-smoke.sh
scripts/test-gate0.sh
docs/
```

**Structure Decision**: Keep the console in existing WebUI/TUI/liveconsole packages. 019 is a presentation/workflow layer over current authority; a new UI framework or process would add distribution and authority surface without solving the MVP.

## Phase 0 Research

See [research.md](research.md).

## Phase 1 Design

See [data-model.md](data-model.md), [contracts/console-model.md](contracts/console-model.md), [contracts/console-actions.md](contracts/console-actions.md), and [quickstart.md](quickstart.md).

## Constitution Check Post-Design

- **Privacy Boundary**: PASS. Console surfaces render existing facts and use existing actions only.
- **Typed Authority**: PASS. Action contracts route to Manager decision/notice endpoints and do not add providers.
- **Workspace And Policy**: PASS. No policy or HostFS authority is broadened.
- **Generality And Provider Scope**: PASS. Provider/backend details remain labels and status facts.
- **Evidence And Redaction**: PASS. Tests execute real JS/Go rendering and include redaction scans.
- **Backend And Distribution**: PASS. No new helper or backend capability.
- **Gates**: PASS. Gate 0 is sufficient.
- **Status And Docs**: PASS. Status/test-plan/UI docs are in scope.

## Complexity Tracking

No constitution violations. No new UI framework is introduced; this intentionally uses the existing WebUI/TUI stack.
