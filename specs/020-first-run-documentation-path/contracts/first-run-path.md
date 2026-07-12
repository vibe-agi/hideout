# Contract: First-Run Path

<!-- markdownlint-disable MD013 -->

## Required Commands

The canonical page must include:

- package verify;
- privacy/Lima init;
- doctor deep;
- first `hideout run`;
- audit show;
- HostFS write status and decision list;
- daemon, TUI, and WebUI entry points.

## Forbidden Claims

The canonical page must not:

- use `go run` as a user-facing alpha path;
- describe native as isolation evidence;
- state local doctor output replaces Gate 2/Gate 3;
- state walkthrough success implies release readiness.

## Smoke

`scripts/test-first-run-docs-smoke.sh` owns this contract in Gate 0.
