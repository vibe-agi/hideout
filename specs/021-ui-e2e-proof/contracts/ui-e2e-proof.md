# Contract: UI E2E Proof

<!-- markdownlint-disable MD013 -->

## Browser Proof

The browser proof MUST execute the served WebUI in a real local browser context.
The implemented provider is a test-only Node driver that controls local
Chrome/Chromium through the Chrome DevTools Protocol. It is not a product
browser-control capability and it is not a runtime dependency.

Required steps:

1. Start a local daemon/WebUI proof fixture or a real local daemon in a temporary
   store.
2. Open the served WebUI page with a valid operator token.
3. Verify visible console areas: Action Required, Stream, Decisions, Notices,
   HostFS Writes, Background, Doctor, Package/Support, and Audit-visible state.
4. Observe a representative typed event and verify visible DOM state changes.
5. Verify that no overview/audit re-fetch or timer-based polling occurs while
   the stream is healthy during the proof window.
6. Create or expose one unacknowledged notice.
7. Click the visible notice acknowledgement control in the browser.
8. Verify the request uses the authenticated existing Manager/daemon route, the
   request payload identifies the notice, the response is handled, and visible
   state shows the notice as acknowledged.
9. Verify missing, wrong, or expired credentials produce a visible refusal or
   error.
10. Write product-hardening evidence entries and redacted artifacts.

Fixture-only browser proof MAY be recorded, but it MUST be labeled and MUST NOT
cover daemon transport/EventSource integration claims.

## Terminal Proof

The terminal proof MUST execute the real `hideout tui` command in a
terminal-like process.
The implemented provider builds a temporary `hideout` binary and launches it
through `script(1)`, using `script -F`/`script -f` where available so the test
can observe terminal output while the process is still running.

Required steps:

1. Start a real daemon event source or a deterministic event seam that still
   launches the actual `hideout tui` process.
2. Launch `hideout tui` under `script(1)` or an equivalent terminal process
   harness.
3. Capture terminal output after initial seed.
4. Verify action-required, stream health, decisions/notices, HostFS write
   status, background status, and doctor/package/support guidance are visible.
5. Deliver a representative console event.
6. Verify terminal output changes because of the event.
7. Verify no interval overview/audit polling render occurs while the stream is
   healthy.
8. Close the stream or invalidate the credential and verify visible
   stale/disconnected/fallback state.
9. Write product-hardening evidence entries and redacted artifacts.

Render-level tests MAY remain as fast unit tests, but they MUST NOT satisfy the
terminal E2E proof by themselves.

## Failure And Not-Run Semantics

- Browser dependency missing: browser lane writes `not-run`, no pass claim.
- PTY unavailable: terminal lane writes `not-run`, no pass claim.
- Daemon startup fails: affected lanes fail unless the command explicitly asked
  for prerequisite reporting only.
- Wrong token unexpectedly succeeds: affected lane fails.
- Hidden polling detected: affected lane fails.
- Redaction failure: affected lane fails for claim coverage.
- Fixture boundary not labeled: affected lane fails because the evidence would
  overclaim.

## Completion Rule

021 is complete only after at least one targeted run produces passed entries for
the required browser lane and the required terminal lane. Gate 0 may include the
same script in prerequisite-tolerant mode, but `not-run` Gate 0 entries alone
are not completion evidence.
