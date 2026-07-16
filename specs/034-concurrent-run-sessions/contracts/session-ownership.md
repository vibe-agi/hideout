# Contract: Daemon Session Ownership And Recovery

<!-- markdownlint-disable MD013 MD060 -->

## Live Ownership

- One daemon instance owns all executable runs for one store.
- One authenticated client connection owns at most one daemon worker.
- One worker owns one durable owner lock/record and one guest supervisor.
- One supervisor owns one target process tree and optional PTY.
- The connection, worker, owner, and supervisor session IDs must match.

Persisted JSON is evidence, not liveness. Live daemon registry plus kernel lock
is current truth; mismatch or inability to prove either fails closed.

## Concurrency

- Environment transition operations serialize through the daemon and existing
  transition lock.
- Target lifetime does not hold that lock.
- Same existing environment plus same pinned workspace may have up to the
  configured bounded session count.
- Every sibling has a distinct runtime child, broker token, HostFS provider,
  staged-write state, host-capability grants, audit, supervisor, terminal, and
  process view.
- Direct workspace changes are intentionally shared.

## Client Loss

1. Mark only the owning worker cancelling.
2. Stop accepting input/renewal.
3. Cancel backend/supervisor transport.
4. Supervisor kills and reaps target descendants.
5. Close per-run providers and bridges.
6. Prove session-view absence.
7. Remove session runtime/credential state.
8. Record completion/failure and release owner.

Sibling workers and environment-shared services remain live.

## Daemon Stop Or Crash

- Ordered daemon stop refuses new sessions, cancels all workers, waits within a
  bound, closes event/session/API listeners, records audit, and releases lock.
- Unclean process death closes Unix and SSH transports. Guest supervisors must
  terminate their targets on transport/heartbeat loss.
- A new daemon invalidates prior operator credentials.
- Restart inventories stale/unproved owner records and live environments. It
  does not silently re-adopt sessions and does not destroy ambiguous resources.

## Environment Stop

- Stop and new registration share transition serialization.
- Any live worker, live owner lock, or unproved owner state denies stop.
- A final normal session exit leaves the environment warm/ready in 034.
- Automatic last-session stop, clean, delete, compact, or recreate is forbidden
  in this feature.

## Evidence

Public/session status may include session ID, environment ID, profile, backend,
workspace hash, lifecycle state, terminal mode, command class, timestamps, and
redacted cleanup error. It excludes tokens, raw workspace/control paths,
process IDs, file descriptors, proxy values, and setup credentials.
