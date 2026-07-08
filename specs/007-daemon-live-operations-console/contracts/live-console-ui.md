<!-- markdownlint-disable MD013 -->

# Contract: Live Console UI

## Purpose

This contract defines how WebUI and TUI surfaces consume daemon events as live
state. It replaces the 006 event-triggered re-fetch behavior with seed-plus-event
reduction for current operations panels.

## Shared Client Sequence

1. Authenticate to the existing local daemon transport.
2. Read one seed containing current Manager overview, recent audit tails, daemon
   status, and stream-health baseline.
3. Subscribe to daemon live events.
4. Apply typed events through the shared reducer.
5. Render panel state from reducer output.
6. On stream end, credential expiry, schema mismatch, or event gap, mark stale
   or disconnected before any further state is claimed live.
7. Recover only through explicit new seed plus authenticated stream.

## WebUI Requirements

- The daemon-served WebUI must render existing panels from reducer state.
- After the startup seed, healthy-stream event handlers must not call
  `overview`, `audit/events`, or another state re-fetch endpoint.
- Manual refresh is allowed and starts a new seed; it is not hidden polling.
- Existing UI actions still call the existing Manager API plan/apply endpoints.
- The page must visibly indicate `live`, `idle-live`, `stale`,
  `disconnected`, `credential-expired`, `schema-mismatch`, and
  `daemon-less` states.
- Tests must execute the served HTML/JavaScript with a deterministic harness,
  feed events, and assert visible DOM text changes without post-seed reads.

## TUI Requirements

- The daemon-backed watch dashboard must render from reducer state.
- After the startup seed, a healthy event stream must not run an interval
  overview/audit poll.
- Stream closure or credential expiry must visibly mark live mode degraded
  before falling back to daemon-less interval behavior.
- `--once` remains a snapshot mode over existing Manager reads.
- Tests must feed events, capture terminal output, and assert visible text
  changes without post-seed overview/audit reads while the stream is healthy.

## No-Polling Definition

During a healthy-stream window after seed:

- WebUI fetch count for `overview` is exactly the seed count.
- WebUI fetch count for `audit/events` is exactly the seed count, except
  operator-initiated audit explorer filters.
- TUI overview/audit read count is exactly the seed count.
- No timer may cause a render/read while stream is healthy and idle.

## Stale And Recovery States

Clients must show a stale/disconnected state for:

- missing required event fields;
- unsupported event schema version;
- sequence gap;
- stream terminal event;
- credential expiry;
- daemon stop/restart;
- unknown entity reference that cannot be reconciled from current state.

Recovery requires a new explicit seed. A recovery seed must be observable in
test instrumentation so it cannot be confused with hidden polling.

## User-Visible Proof

Required proof artifacts:

- WebUI: visible DOM text before and after representative events, fetch counters
  proving no post-seed overview/audit reads, and redaction scan of the rendered
  output.
- TUI: terminal output before and after representative events, read counters
  proving no post-seed overview/audit reads, and redaction scan of captured
  output.

Representative event sequence must include at least:

- environment status change;
- session/run status change;
- background operation status change;
- audit event;
- denied audit event;
- terminal/stale transition.

## Out Of Scope

- Full visual redesign.
- UI framework migration.
- Automatic daemon spawning.
- Prompt/approval channels through the daemon.
- Remote or multi-user operation.
- Durable event replay.
