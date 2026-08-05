# Activity and Live Event Stream Contract

## Two distinct streams

Hideout uses two streams with different trust and volume properties:

1. **guest observer stream**: privileged guest helper to daemon ingestion;
   bounded metadata, not exposed to clients;
2. **operator event stream**: daemon SSE fan-out at `/daemon/events`; redacted,
   human-scale Manager projections for CLI/TUI/WebUI.

Raw kernel observations are never forwarded to browsers or written directly to
disk.

## Guest observer protocol

### Transport

The session setup creates a daemon-authenticated, session-bound full-duplex
channel separate from target stdin/stdout/stderr. The channel is owned by the
fixed observer and the daemon session worker. It has:

- a 1 MiB maximum frame;
- strict schema and unknown-field rejection;
- bounded send queue and explicit overflow counters;
- owner, session, cgroup, guest boot, and observer-generation binding;
- no target-readable credential or writable endpoint.

The existing session-control channel carries only readiness, coverage, and
terminal observer summaries; high-volume observation frames use the dedicated
channel so they cannot block PTY control.

### Handshake

Guest to daemon:

```json
{
  "type": "observer.hello",
  "schema": "hideout.observer-wire.v1",
  "sessionId": "ses_...",
  "environmentId": "env_...",
  "backendIncarnationId": "inc_...",
  "guestBootId": "...",
  "cgroupId": 3141,
  "observerGeneration": 1,
  "helperDigest": "sha256:...",
  "capabilities": {
    "process": {"state": "Available", "evidence": ["tracepoint.exec"]},
    "file": {"state": "Available", "evidence": ["fentry.vfs", "lsm.mmap"]},
    "network": {"state": "Available", "evidence": ["cgroup.connect4", "cgroup.connect6"]},
    "dns": {"state": "Available", "evidence": ["cgroup.skb", "socket-cookie"]}
  }
}
```

Daemon validates the active session/boundary and replies with accepted owner,
expected next sequence, aggregation/storage bounds, and redaction generation.
The supervisor cannot report target-ready until this exchange either succeeds
or produces explicit reduced coverage.

### Observation frame

Binary framing:

```text
u32 big-endian payload length
u32 crc32c(payload)
canonical JSON payload
```

Payload common fields:

```json
{
  "schema": "hideout.observation.v1",
  "owner": {},
  "sessionId": "ses_...",
  "cgroupId": 3141,
  "observerGeneration": 1,
  "cpu": 2,
  "sequence": 91,
  "monotonicNs": 88200011,
  "kind": "process.exec",
  "payload": {}
}
```

Closed kinds:

- `process.fork`, `process.exec`, `process.exit`, `process.execution`;
- `file.open`, `file.access`, `file.read`, `file.write`, `file.metadata`,
  `file.mmap`, `file.create`, `file.truncate`, `file.unlink`, `file.rename`,
  `file.hardlink`, `file.symlink`, `file.mkdir`, `file.rmdir`;
- `network.connect`, `network.close`;
- `dns.query`, `dns.response`, `proxy.target`;
- `coverage.changed`, `collector.loss`, `collector.heartbeat`,
  `collector.goodbye`.

Per-kind field and list bounds are enforced on both sides. Argv is limited by
argument count and total bytes. DNS parser output contains normalized metadata,
not packet bytes. File messages contain identity/path metadata, not contents.
`process.execution` contains the canonical bounded execution snapshot. The
observer emits it on exec and again when an exit or exec replacement closes the
same stable execution ID. The daemon redacts each snapshot before persistence;
queries retain the latest snapshot for that ID, so exit status is not lost and
PID reuse never overwrites an earlier execution.

### Ordering and loss

- Each `(observerGeneration, cpu)` sequence increases by one.
- Heartbeats include the latest sequence and kernel/ring drop counters for all
  CPUs, even when there is no activity.
- Ordinary heartbeats carry `final: false`. After the collectors have stopped
  accepting work and consumed each explicit ring flush boundary, the observer
  emits one `final: true` heartbeat. Its detailed file counters must satisfy
  `matchedEvents = reservedEvents + ringbufDrops`.
- `collector.goodbye` is accepted only after that exact final counter receipt,
  after the bounded relay queue is empty, and when authenticated stream EOF
  immediately follows it and the bridge reports successful exit. After the
  final receipt, any observer-origin frame permanently invalidates that
  receipt; only relay-owned transport loss and goodbye remain admissible. A
  missing/non-final/inexact receipt, trailing frame, or failed/missing bridge
  exit status fails closed instead of claiming graceful completion.
- A missing sequence, increased counter, invalid frame, reconnect, or
  generation change immediately emits a coverage change for the affected
  interval.
- Backpressure never silently overwrites an event. The observer increments a
  named counter before dropping and sends the loss summary on the reserved
  control queue.
- Host receipt time and guest monotonic time are retained; wall-clock
  correlation is approximate and never used as identity.

### Redaction and persistence

The daemon validates identity, normalizes, redacts, and aggregates in this
order:

```text
wire bounds -> active owner/session check -> sequence/loss accounting
-> typed normalization -> deterministic redaction
-> execution snapshot update or activity aggregation
-> risk rules -> checksummed activity store -> operator projection event
```

If normalization or redaction fails, the raw frame is discarded, the relevant
coverage interval degrades, and only a non-sensitive failure code is audited.

## Operator SSE protocol

### Endpoint

`GET /daemon/events?since=<snapshot.sequence>` with the existing operator
authorization. Exactly one non-negative `since` value is required. Browser
EventSource may also use the existing short-lived query credential. A malformed
or missing sequence returns `400`; a sequence that is no longer current returns
`409` and requires a new authoritative snapshot. Successful responses are
`text/event-stream`, `Cache-Control: no-store`.

The stream has no durable replay. Clients seed through
`GET /api/v1/operator/snapshot` and subscribe with that exact sequence. The
daemon checks the sequence and registers the subscriber under the event-bus
lock before opening HTTP 200. Clients remain read-only until that stream opens.
If an event is missed they must fetch a new snapshot.

### Event v2

```json
{
  "version": "hideout.daemon-event/v2",
  "instanceId": "daemon_...",
  "credentialGeneration": 4,
  "kind": "activity",
  "phase": "appended",
  "seq": 882,
  "entity": {
    "kind": "activity",
    "id": "act_...",
    "profile": "default",
    "session": "ses_..."
  },
  "payload": {
    "operationId": "",
    "summary": {},
    "coverage": []
  }
}
```

New kinds are:

- `profile`, `transition`, `operation`;
- `activity`, `coverage`, `risk`, `capability`;
- existing environment/session/workspace/decision/notice/lifecycle/terminal
  kinds.

Payloads contain bounded projection deltas. An activity event may contain a
small already-redacted aggregate summary and cursor, not argv lists or packet
metadata. Clients query details through Manager.

### Client reducer rules

1. Seed sets `instanceId`, credential generation, and last sequence.
2. Different instance or schema: mark `STALE`, disable mutation, re-seed.
3. Sequence equal/older: ignore without changing domain state.
4. Sequence exactly next: apply typed delta.
5. Sequence gap: mark `STALE`, stop applying authoritative deltas, re-seed.
6. Unknown optional kind: advance sequence and record compatibility notice.
7. Unknown mandatory schema: mark `schema-mismatch`, read-only.
8. Terminal/closed/credential-invalidated: render cause, disable mutation,
   obtain a fresh credential and snapshot.

No client polls while the authenticated stream is healthy. After disconnect,
read-only snapshot refresh may use bounded backoff; mutation remains disabled
until a new stream and snapshot agree.

### Slow subscribers

The fan-out queue is bounded. A slow subscriber receives a terminal event with
reason `subscriber-overflow`, then disconnects. This terminal event does not
consume the global event sequence and cannot create gaps for other
subscribers. The slow client re-seeds.

## Privacy invariants

- Neither stream contains a managed secret value, control credential,
  environment value, file content, terminal bytes, or retained packet payload.
- Operator SSE is safe to render but remains authenticated local operator data;
  it is not a share artifact.
- Guest and host paths may appear only after activity redaction and only in
  query responses; the high-level SSE delta uses bounded summaries.
- Every export still follows reviewed export plan/apply.

## Test vectors

Required contract fixtures:

- valid process/file/network/DNS frames for two concurrent cgroups;
- PID reuse and process exit before userspace resolution;
- reordered, duplicate, missing, oversized, bad-CRC, unknown-schema, and
  observer-restart frames;
- ring/control-queue overflow and heartbeat counter mismatch;
- managed secret in argv, URI userinfo, split sensitive flag, query parameter,
  and control token;
- SSE snapshot/subscribe conflict, event gap, daemon instance change, slow
  subscriber, credential rotation, and unknown optional kind;
- target attempt to write observer transport or escape cgroup;
- daemon crash between activity segment frame and manifest update.
