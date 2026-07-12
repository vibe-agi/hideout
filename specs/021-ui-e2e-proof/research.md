# Research: UI E2E Proof

<!-- markdownlint-disable MD013 -->

## Decision 1: Product-Hardening Evidence Manifest

**Decision**: Introduce a reusable `hideout.product-hardening-evidence/v1`
manifest for 021-025 instead of extending each smoke script with ad hoc text.

**Rationale**: 025 needs stable proof ids and covered-claim mappings. Existing
release readiness artifacts are higher-level aggregators, while UI E2E proof
needs lane-level status, artifact references, prerequisites, and redaction
results.

**Alternatives considered**:

- Extend `hideout.release-readiness/v1` directly. Rejected because release
  readiness has release-candidate semantics and must not be satisfied by local
  UI E2E alone.
- Let each script print custom proof text. Rejected because docs truth mapping
  would become another grep-only check.

## Decision 2: Browser Proof Provider

**Decision**: Use a real local browser context through test-only browser
automation. The implemented provider is a small Node driver using the Chrome
DevTools Protocol against a local Chrome/Chromium binary.

**Rationale**: 007 already proved reducer logic through goja. 021 must prove the
served page as a browser would see it, including DOM state, auth behavior, and
EventSource behavior. CDP keeps the proof explicit while avoiding a new npm or
Playwright dependency in this repository. The provider remains outside product
runtime.

**Alternatives considered**:

- Playwright. Rejected for 021 because a direct CDP driver is sufficient for the
  required local browser proof and avoids dependency/bootstrap churn.
- goja-only reducer execution. Rejected because it is not browser proof.
- Static HTML grep. Rejected because it cannot prove requests, visible updates,
  or auth failure.
- Manual browser checklist. Rejected as primary proof because it cannot feed
  025 stable claim mapping.

## Decision 3: Required Browser Action

**Decision**: Use notice acknowledgement as the required low-risk browser action
round trip.

**Rationale**: Notice acknowledgement verifies token handling, request payload,
Manager route dispatch, response handling, and visible state change without
granting HostFS write approval, export share approval, adapter approval, or
other mutation authority.

**Alternatives considered**:

- Decision claim. Rejected for the required path because it creates claim/lease
  semantics and is better tested by 023.
- Explicit refresh. Rejected as insufficient because it proves less of the
  authenticated action path.
- HostFS apply. Rejected because it belongs to 023 and mutates host-visible
  state.

## Decision 4: Real Daemon Versus Fixture Server

**Decision**: Prefer a real daemon/WebUI loopback server for the primary browser
proof. A fixture server may be used only for deterministic DOM/reducer coverage
and must be labeled as such in evidence.

**Rationale**: The core user-facing claim is that the current served page can
connect and update. Fixture-only proof cannot establish tokened daemon transport
or EventSource integration.

**Alternatives considered**:

- Fixture-only browser proof. Rejected because it repeats the 007 proof gap.
- Real daemon only with no fixture support. Rejected because deterministic DOM
  proofs remain useful as secondary artifacts when clearly labeled.

## Decision 5: TUI Proof Harness

**Decision**: Launch the real `hideout tui` command under a pseudo-terminal or
equivalent terminal process harness. Deterministic event seams are allowed only
if the real command process and terminal output path are exercised.

**Rationale**: Render-level tests do not prove the operator can run the TUI.
The proof must catch regressions in command parsing, terminal rendering,
daemon subscription, fallback display, and output visibility.

**Alternatives considered**:

- Function-level renderer snapshots. Rejected because they do not execute the
  command process.
- Shell pipe without PTY semantics. Accepted only if it preserves the same user
  visible output and is explicitly recorded as the harness mode.

## Decision 6: Hidden Polling Measurement

**Decision**: Browser and terminal proofs must measure or instrument that
healthy-stream updates do not trigger overview/audit re-fetch or interval
polling during the proof window.

**Rationale**: 006 and 007 both had failures where event wiring existed but
polling still ran in parallel. The proof must fail when a healthy stream hides
background re-fetch behavior.

**Alternatives considered**:

- Assert EventSource or event subscription exists. Rejected because it does not
  prove polling is absent.
- Rely on unit tests only. Rejected because 021 is specifically user-visible
  proof.

## Decision 7: Artifact Redaction

**Decision**: Proof runs inject or scan canary values that look like Hideout
control-plane material and require redaction success before artifacts can cover
claims.

**Rationale**: Browser screenshots, terminal captures, logs, event summaries,
and manifests are shareable evidence surfaces. They must not leak tokens,
claim tokens, proxy secrets, hidden runtime credential paths, generated
machine ids, or raw staged HostFS content.

**Alternatives considered**:

- Trust current UI data paths. Rejected because proof artifacts add a new
  shareable surface.
- Source grep only. Rejected because previous reviews found empty redaction
  assertions can pass without injected values.

## Decision 8: Gate Placement

**Decision**: Gate 0 validates schemas, docs, and local smoke behavior. It may
record UI E2E lanes as `not-run` when browser/PTY prerequisites are absent, but
021 completion requires a targeted run where both required lanes actually
execute and pass.

**Rationale**: UI E2E depends on local browser and PTY availability. Fast edit
loops should remain practical, but the feature cannot be called complete if its
primary proof never ran.

**Alternatives considered**:

- Make browser/PTY unconditional Gate 0 requirements. Rejected because not all
  development hosts have the dependencies.
- Treat skipped browser/PTY as pass. Rejected because it would recreate the
  overclaim pattern this feature exists to prevent.
