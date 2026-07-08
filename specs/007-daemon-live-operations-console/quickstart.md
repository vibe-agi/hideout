<!-- markdownlint-disable MD013 -->

# Quickstart: Daemon Live Operations Console

## Prerequisites

- Go 1.25 toolchain available.
- Local repository checkout with 006 daemon implementation present.
- No Lima, proxy, browser binary, or external service is required.

## Validation Scenarios

### 1. Schema And Catalog Drift Guard

Requirements: FR-005, FR-006, FR-016, SC-008

Run:

```bash
go test ./internal/liveconsole -run 'TestEventCatalog|TestReducer|TestSeed'
jq empty schemas/daemon-event.schema.json schemas/live-console-seed.schema.json
```

Expected:

- every catalog event kind validates;
- every current panel has a representative event;
- removing a required payload field fails the test/schema validation.

### 2. Reducer Determinism And Stale Behavior

Requirements: FR-007, FR-008, FR-009, SC-004, SC-005

Run:

```bash
go test ./internal/liveconsole -run 'TestReducer'
```

Expected:

- duplicate and old events are ignored deterministically;
- out-of-order/gap/missing required fields mark stale;
- terminal states are not revived by older events;
- recovery requires a new seed.

### 3. Redaction Cleanliness

Requirements: FR-010, SC-003

Run:

```bash
go test ./internal/manager ./internal/app ./internal/daemon -run 'Redact|ControlPlane|Secret|LiveConsoleProof'
```

Expected:

- seeded control-plane tokens/proxy secrets/machine-id fixtures do not appear in
  event JSON, reducer state, rendered WebUI output, terminal captures, or logs;
- local user/application data remains visible locally.

### 4. Daemon Typed Event Stream

Requirements: FR-005, FR-006, FR-015

Run:

```bash
go test ./internal/daemon -run 'Test.*TypedEvent|Test.*Subscribers'
```

Expected:

- daemon publishes typed `liveconsole.Event` payloads;
- export and cleanup events come from `evidence/export/apply` and existing run
  session cleanup, not representative fixtures alone;
- typed run/session and cleanup rows are proven on the daemon-mediated Manager
  path where `Core.Observer` is set;
- `SubscribeEvents` delivers event payloads, not only refresh signals;
- slow subscribers do not block other subscribers;
- terminal events for one subscriber do not create sequence gaps for other
  subscribers.

### 5. WebUI Payload-Driven Reducer Proof

Requirements: FR-001, FR-002, FR-013, SC-001

Run:

```bash
go test ./internal/manager -run 'TestWebUILiveConsole'
```

Expected:

- served WebUI reducer JavaScript is executed by the deterministic harness;
- one seed fetch happens;
- representative event reducers update the panel state/render path without any
  live-handler fetch;
- post-seed overview/audit fetch count remains zero during the healthy-stream
  window;
- manual refresh, when invoked by the test, is counted as a new explicit seed;
- this is a browser-side reducer proof, not a headless-browser UX automation
  run.

### 6. TUI Payload-Driven Visible Proof

Requirements: FR-003, FR-004, FR-014, SC-002

Run:

```bash
go test ./internal/app -run 'TestTUILiveConsole'
```

Expected:

- TUI reads one seed;
- representative events update terminal output;
- no interval overview/audit read occurs while stream is healthy;
- stream closure visibly marks degraded/stale before fallback.

### 7. Manager Authority Remains Plan/Apply

Requirements: FR-011

Run:

```bash
go test ./internal/liveconsole ./internal/manager -run 'ReadOnly|Authority|PlanApply'
```

Expected:

- reducers are read-only and cannot execute authority;
- WebUI action handlers still call existing Manager plan/apply/read endpoints;
- no reducer path writes profile/run/backend state directly.

### 8. Daemon-Less Fallback

Requirements: FR-012, SC-007

Run:

```bash
go test ./internal/app ./internal/manager -run 'DaemonLess|NoDaemon'
```

Expected:

- existing daemon-less WebUI/TUI smoke behavior still works;
- surfaces label fallback correctly and do not claim daemon-backed live state.

### 9. Scope Guard

Requirements: FR-017

Run:

```bash
go test ./internal/liveconsole ./internal/app ./internal/manager -run 'Scope|NoMigration|NoAutoSpawn'
```

Expected:

- implementation does not introduce a UI framework migration;
- no automatic daemon spawning is added;
- prompt channels and marketplace behavior remain out of 007.

### 10. End-To-End Local Smoke

Requirements: FR-001 through FR-017, SC-001 through SC-008

Run:

```bash
scripts/test-live-console-smoke.sh
```

Expected:

- validates typed event/seed schemas;
- runs daemon event-stream and multi-subscriber proof tests;
- captures WebUI JavaScript reducer proof and TUI terminal proof;
- checks no-polling counters through targeted tests/static scans;
- validates schemas and redaction;
- exits without requiring Lima, a browser, or an external service.

### 11. Gate 0

Requirements: all 007 FR/SC

Run:

```bash
scripts/test-gate0.sh
```

Expected:

- existing Gate 0 checks remain green;
- daemon smoke still passes;
- live-console smoke is included;
- markdownlint covers docs and specs;
- JSON schemas parse.

### 12. Final Local Battery

Requirements: implementation readiness

Run:

```bash
go build ./...
go vet ./...
test -z "$(gofmt -l internal cmd)"
git diff --check
go test ./...
scripts/test-gate0.sh
npx markdownlint-cli2 README.md "docs/**/*.md" "specs/007-daemon-live-operations-console/**/*.md"
```

Expected:

- all commands exit 0;
- no generated control-plane secrets appear in live-console artifacts;
- docs no longer describe payload-driven live state as deferred once
  implementation is complete.
