# TUI E2E Proof Assets

This directory contains the 021 terminal proof harness. The proof builds a
temporary `hideout` binary, launches the real `hideout tui` command under
`script(1)`, verifies visible operator-console output, injects a daemon event
through an existing Manager route, checks that a healthy daemon stream does not
interval-poll, observes stream-closed fallback, and writes redacted
product-hardening evidence.

Render-only tests cannot satisfy the TUI lane.
