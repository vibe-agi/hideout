<!-- markdownlint-disable MD013 -->

# Research: Daemon Live Operations Console

## Decision 1: Keep UI frameworks unchanged in 007

**Decision**: Continue with the existing embedded WebUI HTML/JavaScript and the
existing Go terminal dashboard. Do not migrate to a new browser, desktop, mobile,
or TUI framework in 007.

**Rationale**: The spec's hard contract is seed-plus-event correctness, no
hidden polling, and user-visible proof. Current code already serves WebUI from
`internal/manager/server.go` and TUI from `internal/app/app.go`. Migrating a UI
framework would expand scope without improving the live-state invariant.

**Alternatives considered**:

- TUI framework migration now: useful later for richer terminal interaction, but
  not required to prove payload-driven live state.
- Desktop/mobile shell now: solves packaging, not event correctness.
- Browser framework rewrite: would increase test and distribution surface before
  the event catalog is stable.

## Decision 2: Create `internal/liveconsole` for shared reducer ownership

**Decision**: Add a Go package `internal/liveconsole` that owns the live event
catalog types, seed shape, reducer, stream health states, required-field
validation, and shared fixtures.

**Rationale**: 006 currently uses `internal/daemon.Event` with
`Payload map[string]any` (`internal/daemon/events.go:11-17`) and TUI
subscriptions receive only `chan struct{}` (`internal/daemon/client.go:39-73`).
That is not enough for payload-driven panels. A shared Go package prevents
WebUI, TUI, daemon, and tests from each inventing panel semantics.

**Alternatives considered**:

- Put reducer in `internal/daemon`: creates coupling from UI/client code back to
  daemon internals and makes daemon own rendering semantics.
- Put reducer in `internal/manager`: risks a cycle because daemon already
  constructs Manager API/Core.
- Leave reducer in WebUI JavaScript only: cannot serve TUI and weakens Go-owned
  redaction/drift guards.

## Decision 3: Keep one seed, then typed events

**Decision**: A live client performs one initial seed from existing Manager
overview/audit/status data, then applies typed daemon events while the stream is
healthy. On reconnect or daemon restart, clients perform a new explicit seed.

**Rationale**: 006 event stream is intentionally non-durable and replays no
history (`internal/daemon/events.go:9-10`). Requiring replay would create new
storage semantics outside 007. A new seed after reconnect is compatible with the
existing daemon contract and explicit in the spec.

**Alternatives considered**:

- Durable event log: out of scope and unnecessary for local operator UI.
- Best-effort resume from seq only: unsafe without a durable log.
- Polling fallback while stream is healthy: violates FR-002 and FR-004.

## Decision 4: Typed event payload catalog, not free-form payloads

**Decision**: Upgrade `schemas/daemon-event.schema.json` and Go event types so
each event kind used by current panels has required identity, state, display,
ordering, and redaction-safe fields.

**Rationale**: Current schema requires only `version`, `kind`, and `seq`, with
free-form `payload` (`schemas/daemon-event.schema.json`). Current events publish
generic `path` or audit details. Panels cannot safely update from those payloads
without re-fetching overview. Typed payloads make drift testable.

**Alternatives considered**:

- Keep `payload` free-form and document expected keys: not enforceable.
- Send full overview in every event: easy but defeats live event semantics and
  increases leak surface.
- Send JSON Patch over overview: compact, but harder to reason about redaction
  and panel-specific required fields.

## Decision 5: Reducers fail stale, not partial-live, on missing required data

**Decision**: A known event with missing required fields, unsupported schema
version, detected seq gap, or unknown entity reference marks the affected view
stale/disconnected before any further state is claimed live.

**Rationale**: The constitution requires fail-closed behavior for unverifiable
boundary evidence. A live console that cannot prove its state must not look
fresh. This also gives tests a precise expected state.

**Alternatives considered**:

- Ignore malformed known events: hides schema drift.
- Trigger hidden re-fetch: violates zero steady-state reads.
- Continue rendering previous state as live: misleading.

## Decision 6: WebUI proof uses served HTML plus deterministic JS harness

**Decision**: Test the served WebUI HTML by evaluating its reducer/render logic
with a deterministic JavaScript harness and DOM/fetch/EventSource shims. The
test must prove visible DOM changes from supplied daemon events and that no
overview/audit fetch occurs after seed.

**Rationale**: The repo has no npm/browser automation dependency, and Gate 0
should not depend on a real browser binary. Existing `goja` is already a
project dependency and can execute the WebUI's JavaScript deterministically in
Go tests. The proof is stronger than grep because it observes rendered output,
fetch counts, and event application.

**Alternatives considered**:

- Real browser automation in Gate 0: stronger visual fidelity but expensive and
  host-dependent.
- Grep for EventSource: already done in 006 and insufficient.
- Unit-test reducer only: misses the served WebUI integration path.

## Decision 7: TUI proof uses typed event stream and read instrumentation

**Decision**: Add a TUI live dashboard path that reads one seed, consumes
`liveconsole.Event` values, renders `liveconsole.State`, and tests terminal
output plus overview/audit read counts.

**Rationale**: 006 fixed the parallel timer but still calls `core.Overview` and
`core.AuditEventGroups` on every event (`internal/app/app.go:4539-4568`). 007
must prove that event payloads, not re-fetch, update the terminal.

**Alternatives considered**:

- Keep signal channel and re-render from Manager: violates FR-003/FR-004.
- TUI framework migration first: not necessary to prove read-model correctness.
- Shell-only smoke: too indirect to catch hidden overview reads.

## Decision 8: Event emitters derive payloads from authoritative runtime facts

**Decision**: Daemon event payloads are built from Manager/Core operation
results, daemon background registry/status, environment records, audit tail
events after `audit.RedactDetails`, and daemon stream lifecycle facts.

**Rationale**: Evidence must be derived from authoritative runtime facts, not
recomputed independently. Existing daemon already wires Core observer, audit
tail, and background registry; 007 enriches those events instead of inventing a
new source of truth.

**Alternatives considered**:

- Client-side filesystem reads: violates Manager Control Plane layering.
- Synthetic UI-only state: cannot serve as evidence.
- Backend-specific probes: out of scope because no backend claim changes.

## Decision 9: Gate 0 owns 007 verification

**Decision**: Add live-console package tests, schema validation, WebUI/TUI
visible proof, and a smoke script to Gate 0. No real-Lima, Gate 3, Gate 4, or
dogfood gate is required.

**Rationale**: 007 changes local UI/evidence read models only. It adds no
backend, network, HostFS, endpoint exposure, host-open, or isolation claim.
Gate 0 is the constitutionally correct release gate for docs, schemas, static
contracts, package tests, and local smoke.

**Alternatives considered**:

- Real browser Gate 4: this is not host-open/browser-launch authority.
- Lima Gate 2: no guest boundary behavior changes.
- Release-candidate run for planning: useful before release, not required for
  this feature's merge gate.

## Decision 10: Documentation promotes deferred console scope after implementation

**Decision**: Implementation must update docs that currently describe
payload-driven panel state as deferred: `docs/tui-webui-experience.md`,
`docs/STATUS.md`, `docs/manager-control-plane.md`, and
`docs/privacy-run-test-plan.md`.

**Rationale**: Current docs explicitly say WebUI/TUI are event-triggered
re-fetch only and that zero-read panel state is a follow-on. 007 changes that
status. Leaving docs stale would create the same contract drift seen in prior
features.

**Alternatives considered**:

- Update only STATUS: too narrow; the UI contract and test-plan gate must also
  change.
- Update threat model by default: not necessary unless implementation changes
  claims or non-claims.
