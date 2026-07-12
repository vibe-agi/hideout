# Research: Operator Console MVP

<!-- markdownlint-disable MD013 -->

## Decision 1: Reuse Existing WebUI/TUI Stack

**Decision**: Build 019 inside the current Manager-served WebUI, liveconsole reducer, and TUI renderer. Do not introduce Flutter, Bubbletea, React, or a separate frontend build system.

**Rationale**: The project already ships a local WebUI and TUI with daemon event streams, tokened loopback serving, Goja reducer tests, and Gate 0 smoke. A new UI stack would add packaging, attack surface, and test complexity for a polish feature.

**Alternatives considered**:

- Adopt Bubbletea for TUI. Rejected for 019 because the existing TUI renderer already proves the compact model and the current task is usability grouping, not a terminal framework rewrite.
- Adopt Flutter or a web framework. Rejected because 019 is local alpha polish with no hosted UI/public auth.

## Decision 2: WebUI First, TUI Compact Same Model

**Decision**: WebUI gets the richer layout. TUI renders the same console model in compact form.

**Rationale**: The WebUI can show multiple panels and action controls more comfortably. The TUI remains important for terminal workflows and smoke tests but should not block richer WebUI grouping.

**Alternatives considered**:

- TUI parity first. Rejected because approval workflows and grouped panels are easier to operate and verify in WebUI.
- WebUI only. Rejected because TUI is already a product surface and must not drift from the model.

## Decision 3: Action Required Is a View, Not a New Provider

**Decision**: Action-required grouping is derived from existing decisions and notices. Mutations continue through current decision claim/resolve and notice ack routes.

**Rationale**: HostFS write, export/share, and notices already have provider-owned semantics and audits. The console should make them visible, not re-authorize them.

**Alternatives considered**:

- Add a generic console action endpoint. Rejected because it would duplicate Manager authority.

## Decision 4: Doctor Is Explicit

**Decision**: Console may show cached doctor status and offer an explicit "run local doctor" action, but page load must not automatically run doctor.

**Rationale**: 018 doctor is local, but it can still be relatively expensive and can produce support artifacts. Automatic doctor on page load would surprise operators and complicate no-hidden-work guarantees.

**Alternatives considered**:

- Always run light doctor on page load. Rejected because it violates explicit operator control.
- Never show doctor status. Rejected because doctor is now an important support/readiness surface.

## Decision 5: Package And Support Status Are Read-Only

**Decision**: 019 displays package/support status and next commands only. `hideout package repair` remains outside the console.

**Rationale**: 017 package repair has explicit CLI semantics and audit. Moving repair into WebUI/TUI would be a new workflow and approval problem.

**Alternatives considered**:

- Add package repair buttons. Rejected for 019 scope; can be designed later as typed plan/apply if needed.

## Decision 6: Preserve Event Honesty

**Decision**: Healthy daemon streams do not run hidden interval polling. Event payloads and explicit operator refresh actions drive updates; closed/absent streams may use documented fallback behavior.

**Rationale**: 006/007 established this contract after review found hidden polling. 019 must not regress it while adding panels.

**Alternatives considered**:

- Periodic polling for all panels. Rejected because it contradicts the existing live-console contract.

## Decision 7: Test Runtime UI Logic

**Decision**: WebUI tests execute the actual served JavaScript reducer/action logic with Goja; TUI tests execute Go render/watch helpers.

**Rationale**: Previous static greps caused overclaim. 019 needs runtime proof that action grouping, no-polling, and redaction behavior hold.

**Alternatives considered**:

- Static HTML/source checks only. Rejected as insufficient.
- Full browser E2E. Deferred because reducer/action runtime proof is the current project standard and less brittle for Gate 0.
