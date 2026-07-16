# Contract: Session Ownership And Lifecycle

<!-- markdownlint-disable MD013 MD060 -->

## Authority

The environment transition lock serializes lifecycle mutation. A separate
per-session flock proves each live host owner. Neither lock contains broker,
HostFS, network, endpoint, or host-app authority.

## Attach

Manager MUST, under the transition lock:

1. reconcile unlocked stale owner records;
2. reject any unprovable owner state;
3. validate the pinned environment and shared-service fingerprint;
4. create and exclusively lock the new owner record;
5. start or validate the running backend and required service;
6. persist `preparing`, then release the transition lock.

Parallel starts wait with context cancellation rather than returning the old
`environment ... is already in use` target-lifetime error.

## Run

The owner lock remains open from registration through target and ordered
cleanup. After the session view is active, Manager records `running`. A host
crash releases the flock automatically; the JSON becomes stale evidence, not
live evidence.

## Finish

The finishing process reacquires the transition lock and records `cleaning`.
It removes only its runtime child and authority. The environment remains
`running` if any sibling owner is live. With no sibling, it becomes `ready` or
`error` based on actual cleanup.

## Stop

Explicit stop takes the transition lock and probes every owner:

- one or more `live` owners: deny with active count and copyable retry advice;
- any `unprovable` owner: deny and direct the operator to doctor/repair;
- stale unlocked records only: reconcile, audit, then continue;
- no active or unprovable owners: use the existing non-destructive stop.

Stop never signals a session in 034. Releasing the last owner never invokes
stop automatically.

## Race Requirements

- Attach and stop cannot both commit.
- Two starts of a stopped environment converge on one backend start.
- A finishing owner cannot clean the shared service after a sibling has
  registered.
- Stale metadata cannot increase active count.
- Lock acquisition respects caller cancellation and has no unbounded wait.

## Evidence

Audit records bounded owner state transitions and environment/service outcome.
It does not record lock paths, PIDs, tokens, raw command argv, or proxy
material. Manager API, CLI, TUI/WebUI event payloads, and doctor consume the
same summary builder.
