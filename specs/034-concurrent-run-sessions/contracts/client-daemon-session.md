# Contract: Client To Daemon Run Session

<!-- markdownlint-disable MD013 MD060 -->

## Placement And Authentication

- The session listener is a 0600 Unix socket beneath the validated 0700 daemon
  runtime directory inside the Hideout store.
- Supported Lima guests cannot reach this directory or socket.
- The first frame is an authenticated protocol hello. A missing, malformed,
  expired, or stale token closes the connection and records a redacted daemon
  auth refusal.
- One authenticated connection may submit one run request. It cannot switch
  session identity or attach to another active session.
- Browser loopback and SSE endpoints never proxy this contract.

## Startup

1. Client resolves terminal mode and captures rows/columns/TERM.
2. Client connects or races to auto-start the daemon and waits for authenticated
   readiness within a bound.
3. Client sends `hello` and one strict `run-request`.
4. Daemon builds the canonical Manager plan and either sends a review or fails.
5. If confirmation is required, only a matching explicit `confirm` binds the
   reviewed plan. Missing/negative confirmation closes without target authority.
6. Daemon sends `started` only after the worker, owner, providers, supervisor,
   and target are ready enough to accept the advertised stream contract.

## Streaming

- PTY mode uses `stdin`, `stdin-eof`, `resize`, `signal`, `cancel`, and
  `terminal` frames.
- Non-PTY mode uses the same input controls and distinct `stdout`/`stderr`
  frames.
- A frame has a bounded length. Data frames may contain arbitrary bytes,
  including NUL and terminal escape sequences.
- Writers serialize frames. No implementation may infer controls from data
  payloads or silently discard output.
- Unknown frame types below `0x80` fail closed. Unknown types at or above
  `0x80` are ignorable extensions only; their declared bounded payload must be
  consumed without interpreting it.
- Bounded backpressure either pauses the upstream target or cancels the session
  with a typed error; it never reports success after loss.
- In raw PTY mode Ctrl-C is input data. A client must not also send a signal for
  the same keystroke.

## Resize And Signals

- Initial rows/columns are applied before the target renders.
- Every valid resize updates only the owning supervisor PTY.
- Invalid or zero dimensions are rejected or normalized before forwarding.
- Non-PTY signal/cancel actions are structured. Unsupported signals fail closed.
- Terminal theme and broad ambient environment propagation are not part of 034.

## Credential Renewal

- The daemon advertises a bounded renewal interval, never a raw backend or
  per-run credential.
- The client re-reads the current operator token file and sends `renew` before
  its lease deadline.
- A valid current or grace token extends only this connection lease.
- Failed renewal cancels only this run. A copied stale token cannot create a new
  connection or renew after grace.

The Manager HTTP `run/apply` adapter has no renewal channel. It revalidates the
request's original credential and cancels when that credential leaves rotation
grace. Clients requiring an interactive or multi-hour run use this session
socket contract.

## Completion And Loss

- Exactly one `completion` frame terminates a successful protocol exchange.
- Exact target exit status and signal-derived result are preserved.
- A cleanup failure overrides target success and is reported separately from
  target stderr.
- Client EOF, protocol violation, lease expiry, or bounded output failure
  cancels the run. There is no implicit detach.
- The client restores host terminal state on completion, refusal, daemon loss,
  read/write error, signal, and panic-safe deferred paths.
