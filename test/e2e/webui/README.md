# WebUI E2E Proof Assets

This directory contains the 021 browser proof driver. The proof opens the
daemon-served WebUI in a real local Chrome/Chromium context via CDP, exercises
the visible operator console, verifies live updates without hidden overview or
audit polling, acknowledges a notice through the existing Manager route, checks
wrong-token refusal, and writes redacted product-hardening evidence.

Reducer-only, static-source, or fixture-only tests cannot satisfy the browser
E2E lane.
