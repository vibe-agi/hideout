# Feature Specification: UI E2E Proof

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `021-ui-e2e-proof`

**Created**: 2026-07-09

**Status**: Implemented as local UI E2E evidence (not release readiness) — the exact claim surface and non-claims live in [docs/STATUS.md](../../docs/STATUS.md)

**Input**: User description: "Continue `.tmp/021-025-product-hardening-plan.md`. 021 proves the current WebUI and TUI operator console from a user-visible perspective: a real browser opens the served WebUI, observes live console state, performs one low-risk action round-trip, and records proof; a real terminal/PTY observes `hideout tui` output from daemon events; both proofs write stable evidence entries that 022-025 can consume. No UI redesign, no new authority, no product browser-control capability."

## Clarifications

### Session 2026-07-09

- Q: What cross-feature evidence format should 021 establish? A: 021 introduces `hideout.product-hardening-evidence/v1`, a small proof manifest consumed by later 022-025 work and by the existing release-readiness flow; it records stable proof ids, modes, covered claims, pass/fail/not-run status, prerequisite reasons, commit/package identity when available, and redaction status.
- Q: Which browser action is required for the low-risk round trip? A: Use notice acknowledgement as the default because it verifies authentication, request payload, response handling, and visible state change without approving HostFS writes or other host mutation. Decision claim may be an additional test; HostFS apply remains out of 021.
- Q: What does a skipped browser or terminal E2E mean? A: It is explicit `not-run` evidence with prerequisites, never a pass. 021 is not complete unless at least one targeted run actually executes the required browser and terminal proof lanes.
- Q: Does 021 add product browser automation or a new UI framework? A: No. Browser automation and PTY control are test-only proof mechanisms. The product remains the existing WebUI/TUI over Manager and daemon surfaces.
- Q: Which proof providers define the implemented 021 contract? A: Browser proof uses a test-only Node Chrome DevTools Protocol driver against local Chrome/Chromium; terminal proof uses `script(1)` to launch the real `hideout tui` command. `hideout tui --interval` is a daemon-less fallback interval and is used in proof only as a regression tripwire for hidden polling.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Prove WebUI Console In A Real Browser (Priority: P1)

An operator or maintainer opens the existing daemon-served WebUI in a real local
browser context and sees the Operator Console as an actual page, not only as
HTML text or a JavaScript reducer harness. The proof shows live console state,
no hidden steady-state polling while the daemon stream is healthy, a visible
update from a live event, and one safe local action round-trip through the
served page.

**Why this priority**: The browser is the richest management surface and the
largest remaining proof gap from 007/019. Existing tests execute reducer logic
and HTML smoke, but they do not prove the user-visible page can connect,
authenticate, update, and perform a low-risk action.

**Independent Test**: Start the local daemon/WebUI proof fixture, open the served
page in a real browser context, drive a representative live event and a notice
acknowledgement, and verify the DOM-visible console state changes while the
proof manifest records the covered claims.

**Acceptance Scenarios**:

1. **Given** a local daemon-served WebUI with a valid operator token, **When** the proof opens the page in a real browser context, **Then** the Operator Console, Action Required, Stream, Decisions, Notices, HostFS Writes, Background, Doctor, Package/Support, and Audit-visible areas are present with stable user-visible text.
2. **Given** the browser page is connected to a healthy daemon event stream, **When** a representative typed event arrives, **Then** the visible console state changes without an overview/audit re-fetch or timer-based polling during the healthy-stream window.
3. **Given** an unacknowledged notice is visible, **When** the browser proof performs the notice acknowledgement action, **Then** the request uses the existing authenticated Manager/daemon route, the response is handled, and the page shows the notice as acknowledged.
4. **Given** a missing, wrong, or expired token, **When** the browser attempts to load data or perform the required action, **Then** the page shows a visible refusal/error state and the proof records a failure rather than silently succeeding.

---

### User Story 2 - Prove TUI Console In A Real Terminal Process (Priority: P2)

An operator or maintainer runs the existing `hideout tui` in a terminal-like
session and observes the compact operator console as terminal output from the
real command process. The proof shows action-required summaries, stream health,
decision/notice/HostFS write rows, doctor/package/support guidance, and visible
fallback when the stream closes.

**Why this priority**: The TUI is the fastest local operator surface and the
place where hidden polling regressions previously appeared. A function-level
render test is not enough to prove the command works as a user sees it.

**Independent Test**: Start `hideout tui` under a pseudo-terminal or equivalent
terminal harness, feed it through a real daemon event source or a deterministic
daemon-event seam that still exercises the command process, and assert the
captured terminal output changes as expected.

**Acceptance Scenarios**:

1. **Given** a running daemon and `hideout tui` attached to a terminal-like session, **When** console seed data is available, **Then** the captured terminal output includes action-required counts, stream health, decisions/notices, HostFS write status, background status, and doctor/package/support command guidance.
2. **Given** the daemon stream is healthy, **When** a representative console event arrives, **Then** the captured terminal output changes from that event without an interval overview/audit polling render.
3. **Given** the stream closes or the credential becomes invalid, **When** the TUI cannot prove live state, **Then** the terminal output shows stale/disconnected/fallback state before using daemon-less behavior.
4. **Given** a deterministic event seam is used for stability, **When** the proof runs, **Then** it still launches the real `hideout tui` process and records that the seam replaces only event timing/source, not the terminal rendering path.

---

### User Story 3 - Record Reusable Product-Hardening Evidence (Priority: P3)

A maintainer needs a stable artifact that later features and documentation truth
checks can reference. Each UI E2E run records what was actually proven, what was
not run, which prerequisites were missing, which product claims were covered,
and whether control-plane redaction checks passed.

**Why this priority**: 021-025 are a proof/truth layer. If each script writes
ad hoc output, 025 can only grep text and cannot map a documentation claim to a
specific proof. The evidence manifest gives 022-025 a common proof vocabulary
without creating another product capability.

**Independent Test**: Run browser and terminal proof modes in passing and
prerequisite-missing configurations, then validate that the manifest contains
stable proof ids, covered claims, pass/fail/not-run status, prerequisite
reasons, redaction status, and artifact references.

**Acceptance Scenarios**:

1. **Given** browser and terminal proof lanes complete, **When** the manifest is inspected, **Then** it contains stable proof ids for the WebUI browser proof and TUI terminal proof, with covered claim ids and artifact references.
2. **Given** a required browser or terminal prerequisite is missing, **When** the proof command runs in a prerequisite-gated mode, **Then** the manifest records `not-run` with the missing prerequisite and no pass claim.
3. **Given** proof artifacts include screenshots, terminal captures, logs, event payloads, or command summaries, **When** redaction validation runs, **Then** Hideout-minted control-plane material is absent and redaction status is recorded.
4. **Given** a later docs truth check needs to validate a UI claim, **When** it reads the manifest, **Then** it can distinguish browser proof, terminal proof, harness/unit proof, local-fast proof, and real-gate proof by evidence class.

### Edge Cases

- What happens when the browser dependency is unavailable? The proof lane records explicit `not-run` evidence with the missing prerequisite; it does not pass and cannot satisfy 021 completion by itself.
- What happens when the terminal/PTY mechanism is unavailable on the host? The terminal lane records `not-run` with platform/prerequisite detail; it does not fall back to a render-only unit test.
- What happens when the daemon is absent, the stream closes, or credentials expire mid-proof? UI surfaces must show visible stale/disconnected/refused state, and the proof records the failure or fallback boundary.
- What happens when a fixture WebUI server is used for DOM proof? The proof must label that it covers browser DOM/reducer behavior only; daemon loopback transport/EventSource integration requires a separate proof lane.
- What happens when event payloads, screenshots, terminal captures, or logs include control-plane-looking values? The proof must fail or record redaction failure before claims are considered covered.
- What happens when the notice acknowledgement route changes? The browser proof must fail because the low-risk action round-trip no longer proves the current Manager route and visible state change.
- What happens when hidden polling or overview/audit re-fetch returns? The browser or terminal proof must fail the healthy-stream no-hidden-polling assertion.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: UI proof, daemon/WebUI/TUI observation, existing Manager notice acknowledgement, evidence/reporting, and test/runtime lifecycle. No new HostFS, network, backend, script, browser-control, remote approval, or product authority is introduced.
- **Fail-closed behavior**: Missing browser/PTY/daemon prerequisites produce `not-run` or failure evidence, not pass claims. Wrong tokens, missing routes, stale streams, redaction failures, hidden polling, and action round-trip failures prevent the corresponding claim from being marked covered.
- **User authority and policy**: The only required browser action is notice acknowledgement by an authenticated local operator surface. It records operator awareness but grants no host authority. HostFS apply, export share approval, adapter proposal approval, and other mutation decisions remain outside 021.
- **Generality and provider scope**: 021 proves generic Hideout WebUI/TUI operator surfaces. It does not select a product UI framework, browser-control provider, terminal UI framework, remote browser workflow, or public test service as Core semantics.
- **Evidence surface**: Product-hardening evidence manifest, browser proof artifacts, terminal capture artifacts, redaction scans, command summaries, docs/test-plan references, and later release-readiness consumption. Evidence is derived from the actual served page/terminal command where required.
- **Secret/redaction boundary**: Browser artifacts, terminal captures, logs, event payloads, request/response summaries, screenshots, and manifests must not expose daemon tokens, UI tokens, claim tokens, proxy secret values, `HIDEOUT_SECRET_*` backing material, generated machine ids, hidden runtime credential paths, or raw staged HostFS content.
- **Backend/gate expectation**: Gate 0 may run local deterministic proof when browser/terminal prerequisites are available. No real Lima, DNS, HostFS, network, or release-readiness claim is introduced. Missing optional local E2E prerequisites are recorded as `not-run`; release candidates consume this evidence but are still governed by 016 and real Gate 2/Gate 3 requirements.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST provide a UI E2E proof path that opens the existing WebUI in a real local browser context and verifies the Operator Console is visibly rendered.
- **FR-002**: Browser proof MUST verify the required console panels are visible: Action Required, Stream, Decisions, Notices, HostFS Writes, Background, Doctor, Package/Support, and Audit-visible state.
- **FR-003**: Browser proof MUST verify that a representative live event changes visible console state while the daemon stream is healthy without hidden steady-state overview/audit re-fetch or timer-based polling.
- **FR-004**: Browser proof MUST perform one low-risk notice acknowledgement action through the served page and verify authentication handling, request/response handling, and visible acknowledged state.
- **FR-005**: Browser proof MUST verify missing, wrong, or expired credentials produce a visible refusal or error rather than a silent success.
- **FR-006**: System MUST provide a TUI E2E proof path that launches the real `hideout tui` command in a terminal-like process and captures user-visible output.
- **FR-007**: TUI proof MUST verify visible action-required, stream health, decisions/notices, HostFS write status, background status, and doctor/package/support guidance.
- **FR-008**: TUI proof MUST verify that representative daemon events update terminal output without interval overview/audit polling while the stream is healthy.
- **FR-009**: TUI proof MUST verify stream closure or credential invalidation produces visible stale/disconnected/fallback state before daemon-less behavior is used.
- **FR-010**: If a deterministic event seam is used, it MUST still exercise the real `hideout tui` process and MUST NOT replace the terminal proof with a function-level render test.
- **FR-011**: System MUST write a `hideout.product-hardening-evidence/v1` manifest or compatible entry for each UI E2E run.
- **FR-012**: Each proof entry MUST include a stable proof id, feature id, mode, evidence class, command summary, covered claim ids, prerequisite status, pass/fail/not-run status, artifact references, redaction status, commit identity, and package identity when applicable.
- **FR-013**: Missing browser or terminal prerequisites MUST produce explicit `not-run` proof entries and MUST NOT mark UI E2E claims covered.
- **FR-014**: Proof artifacts and manifest fields MUST pass deterministic control-plane redaction before they can be used as claim evidence.
- **FR-015**: Browser and TUI proof MUST distinguish real served-page/real terminal proof from reducer harness, static source, fixture-server, or render-only proof.
- **FR-016**: Docs and test-plan updates MUST describe the UI E2E proof boundaries, prerequisites, `not-run` behavior, and relation to 016 release-readiness without implying release readiness from local UI E2E alone.
- **FR-017**: 021 MUST NOT add or require UI redesign, keyboard navigation redesign, policy editor work, remote browser access, real Lima execution, HostFS apply, export/share approval, adapter approval, marketplace trust, or product browser-control/device capability.

### Key Entities *(include if feature involves data)*

- **Product Hardening Evidence Manifest**: Stable proof artifact for 021-025 that records proof entries, covered claims, modes, prerequisite status, artifacts, redaction status, and build/package identity.
- **Proof Entry**: One result for a browser, terminal, local-fast, real-gate, or documentation proof lane. It can pass, fail, or be not-run with an explicit reason.
- **Covered Claim**: A stable claim identifier from docs, specs, STATUS, or test-plan text that a proof entry supports.
- **Browser Proof Run**: A user-visible WebUI verification run in a real browser context, including live update and notice acknowledgement.
- **Terminal Proof Run**: A user-visible TUI verification run against the real `hideout tui` process in a terminal-like session.
- **Low-Risk UI Action**: A local console action that verifies authenticated browser interaction without granting host mutation authority; v1 uses notice acknowledgement.
- **Not-Run Evidence**: Explicit record that a proof lane did not execute because prerequisites were missing or the host mode did not support it.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A browser proof run shows 100% of required console areas named in FR-002 in the visible page.
- **SC-002**: A browser proof run demonstrates at least one live event changes visible console state with zero detected steady-state overview/audit re-fetches while the stream is healthy.
- **SC-003**: A browser proof run completes one notice acknowledgement round-trip and shows the notice acknowledged in the visible page.
- **SC-004**: A terminal proof run captures the real `hideout tui` process and shows 100% of required terminal sections named in FR-007.
- **SC-005**: A terminal proof run demonstrates at least one daemon event changes terminal output with zero detected interval overview/audit polling while the stream is healthy.
- **SC-006**: 100% of missing browser or terminal prerequisite fixtures produce `not-run` evidence and 0 pass claims.
- **SC-007**: 100% of proof entries include stable proof ids, covered claim ids, evidence class, pass/fail/not-run status, prerequisite status, artifact references, and redaction status.
- **SC-008**: Redaction scans find 0 raw Hideout-minted control-plane credential matches in browser artifacts, terminal artifacts, logs, event payload summaries, and evidence manifests.
- **SC-009**: Docs/test-plan references distinguish browser E2E, terminal E2E, reducer harness, fixture-server proof, local-fast proof, and release-readiness evidence with 0 overclaim scan failures.
- **SC-010**: Gate 0 or the documented local UI E2E smoke reports a nonzero failure when a required executed proof lane fails, and reports explicit `not-run` when prerequisites are absent.

## Assumptions

- Browser automation is a test-only proof mechanism. It does not create a product browser automation feature, remote browser access, or browser-control capability.
- Terminal/PTY automation is a test-only proof mechanism. It does not require a TUI framework migration.
- Notice acknowledgement is the default browser action for 021 because it is visible and authenticated while avoiding HostFS apply or other host mutation authority.
- Local deterministic UI E2E may use fixtures for timing and state setup, but the proof must label fixture boundaries and must not claim daemon integration from fixture-only runs.
- The product-hardening evidence manifest is a verification artifact for tests, docs truth, and release-readiness consumption; it is not a new user-facing product capability.
- 021 proves local UI behavior only. Real backend isolation, DNS mediation, HostFS data-plane behavior, and release readiness remain owned by their existing gates and later 022-025 evidence.
