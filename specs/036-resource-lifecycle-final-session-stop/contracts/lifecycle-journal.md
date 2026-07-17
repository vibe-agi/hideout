# Lifecycle Journal Contract

## Purpose

The journal is a bounded restart discovery index. It is not a lock, lease,
credential, backend handle, liveness oracle, or capability store.

## Location And File Safety

- Stored under the existing private store in an environment-scoped lifecycle
  directory.
- Every ancestor and final component is checked against symlink traversal and
  unexpected file type.
- Directory mode is private; files are regular and private.
- Writes use a same-directory temporary file, bounded JSON encoding, sync, and
  atomic rename.
- Unknown fields, duplicate identities, invalid generations, invalid state,
  missing dependencies, and cycles fail closed.

## Shape

```json
{
  "schema": "hideout.lifecycle-journal/v1",
  "environmentId": "env_example",
  "startGeneration": 4,
  "incarnation": {
    "instanceName": "hideout-default-env-example",
    "bootId": "01234567-89ab-cdef-0123-456789abcdef"
  },
  "resources": [],
  "facts": [],
  "reconciliation": {
    "daemonInstanceId": "daemon-example",
    "state": "complete",
    "observedAt": "2026-07-16T05:00:00Z"
  },
  "updatedAt": "2026-07-16T05:00:00Z"
}
```

Optional `idleDeadline` and `stopAttempt` objects follow the data model. Only
nonterminal live-resource registrations needed for recovery discovery are
stored. `facts` contains at most 64 recent retained/handoff classifications;
facts are non-authoritative, have no dependency edges, and may be evicted
without changing the underlying product state.

Blocked reconciliation also carries one bounded machine-readable `reasonCode`.
That code is the source for operator recovery classification across status and
events; raw provider/backend error text is never persisted in the journal.

## Commit And Checkpoint Semantics

Before any registration-owned provider effect becomes usable, Manager plans
the complete subgraph and the coordinator commits it with one atomic journal
write. That planned graph is the conservative restart envelope.

Routine `active`, `draining`, and successful-release observations update the
in-memory reducer immediately and may share a checkpoint scheduled within 500
ms. If the daemon dies before that checkpoint, reconciliation sees the older
planned graph and must prove absence; it cannot infer that an effect never
started. New boot binding, orphan/cleanup failure, reconciliation state, idle
deadline, stop attempt, and coordinator close are written synchronously.

## Boundedness

Implementation constants must bound:

- encoded file size;
- resources per environment;
- dependencies per resource;
- IDs and reason codes;
- recent non-authoritative facts; and
- reconciliation duration.

Exceeding a bound blocks new automatic lifecycle action and emits typed
recovery status; it never drops a possible VM dependency to make the graph look
idle.

## Forbidden Fields

The journal must not contain:

- operator, broker, UI, or provider tokens;
- raw host or guest paths;
- command argv or terminal content;
- process IDs or file descriptors;
- proxy secret values or references usable as credentials;
- sockets, SSH handles, or reusable capability handles.

## Restart Semantics

1. A replacement daemon loads journal entries as discovery hints.
2. It invalidates old timers and stop-attempt ownership.
3. Every implemented live kind maps to `backend-observation`,
   `session-absence`, or `network-runtime`; daemon startup dispatches all three
   typed contracts and blocks on an unhandled probe.
4. Kernel owner locks and backend observation remain authoritative facts.
5. Until every possible VM dependency is classified, automatic stop is
   disabled for that environment.
6. Proved idle state receives a fresh full 15-second grace; the old deadline is
   not resumed.
