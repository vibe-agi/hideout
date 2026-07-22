# Research: Lifecycle Attach Reservation

<!-- markdownlint-disable MD013 -->

## Decision 1: Reserve Before The Transition Lock

**Decision**: A reusable lifecycle-managed run allocates an opaque session ID,
waits for any earlier reconciliation, and obtains a daemon-owned reservation
before it acquires `environment.Store.LockContext`.

**Rationale**: The current order creates environment runtime under the
transition lock and then calls `BeginAttach`, whose first action is to wait for
reconciliation. Making reconciliation acquire the transition lock would form
the cycle `run: transition lock -> reconciliation` and `reconcile:
reconciliation fence -> transition lock`. Reserving first lets reconciliation
finish while the run holds no transition lock, then prevents a later reconcile
from entering the publication window.

**Alternatives rejected**:

- Make reconciliation acquire the transition lock: introduces the proved lock
  cycle.
- Create the owner before waiting for reconciliation: publishes durable
  authority against a stale/unproved incarnation.
- Retry after an ownerless runtime is scrubbed: treats a safety violation as a
  transient error and risks cross-session cleanup.

## Decision 2: Use A Two-Step Reservation (`Prepare`, Then `Promote`)

**Decision**: A held reservation validates a fresh `AttachRequest` and captures
the coordinator's exact `EnvironmentRef` through `Prepare`. It remains an
establishment reservation until Manager writes the durable owner, then
`Promote` atomically replaces the reservation with the normal registration.

**Rationale**: Shared-workspace planning needs the observed incarnation before
the owner record can be constructed, but the normal registration must not
become active before the owner is durable. Separating preparation from
promotion supplies typed incarnation truth without creating premature active
session authority.

**Alternatives rejected**:

- Promote before owner creation: recreates the ownerless active-session window.
- Put workspace host paths or backend handles into the reservation: expands
  authority and leaks provider concerns into lifecycle coordination.
- Create a provisional durable journal row: restart could silently re-adopt
  incomplete work and would require a new durable authority schema.

## Decision 3: Keep Reservations In Memory Only

**Decision**: `registryEnvironment` owns an in-memory map keyed by session ID.
The journal records no reservation or provisional owner.

**Rationale**: A reservation is coordination for one daemon process, not
authority. On daemon crash, its disappearance guarantees that the replacement
daemon classifies only environment runtime, owner records, lifecycle journal,
and backend observations. Runtime without an owner may be proved orphaned and
scrubbed; a durable owner remains subject to existing liveness and explicit
recovery rules.

**Alternatives rejected**:

- Persist reservations: requires lease expiry/re-adoption semantics and can
  preserve phantom authority after crash.
- Treat runtime existence as ownership: runtime is precisely the state whose
  ownerless classification caused the race.

## Decision 4: Allow Compatible Reservations To Coexist

**Decision**: Multiple distinct session reservations may be held for one
environment. They block reconciliation, idle stop, explicit stop, forget, and
destructive mutation as a group, but do not block one another. Each is prepared
and promoted independently under the coordinator mutex.

**Rationale**: Existing reusable environments support concurrent runs. A
single global establishment mutex would unnecessarily serialize policy
snapshotting, workspace capture, and owner creation. Environment transition
locking remains the short cross-process serialization point for record/backend
revalidation and runtime publication.

**Alternatives rejected**:

- One reservation per environment: violates the minimal-serialization and
  concurrent-run requirements.
- Let mutation race reservations and clean up the loser: risks deleting a
  runtime after its caller has crossed the publication boundary.

## Decision 5: Refuse New Conflicting Work Instead Of Adding Another Wait Graph

**Decision**: Existing reconciliation completes before reservation admission.
Once a reservation is held, new reconciliation, stop, forget, idle-stop expiry,
and destructive mutation return their existing bounded activity/refusal result
without starting destructive work. Reservation acquisition fails closed when a
mutation/stop is already active and observes caller cancellation while waiting
for reconciliation.

**Rationale**: Refusal is bounded and preserves existing operator semantics.
Adding bidirectional waits between stop, mutation, registration, and
cross-process locks would enlarge the deadlock surface without improving
safety.

## Decision 6: Split Session Allocation From Materialization

**Decision**: `internal/session` exposes side-effect-free layout allocation and
explicit layout preparation. `session.New` remains a compatibility wrapper.
Manager allocates only the ID/path values before reserving; global session
directories and environment runtime directories are created after reservation,
transition-lock acquisition, record reload, and backend-incarnation proof.

**Rationale**: Reservation requires a stable session identity, while FR-001
forbids publishing runtime first. Making allocation side-effect-free creates a
clean protocol boundary and makes cancellation-before-reservation leave no
filesystem residue.

## Decision 7: Preserve The Existing Durable Owner As The Authority Barrier

**Decision**: `runsession.AcquireOwner` remains the first durable session
authority. Manager installs cleanup immediately after owner acquisition and
calls reservation promotion only afterward. Promotion creates the existing
planned lifecycle session/resource graph atomically under the coordinator
mutex; provider effects still require their existing commit barriers.

**Rationale**: The owner record already supplies liveness, workspace,
configuration snapshot, terminal mode, and recovery semantics. A second owner
format would duplicate judges and weaken restart clarity.

## Decision 8: Expose Derived, Redacted Establishment Evidence

**Decision**: Lifecycle status adds an optional establishment count and an
`establishing` activity when appropriate. Events use a closed set of bounded
kinds/reason codes for wait, reserve, prepare, promote, and abort. No public
surface includes the reservation's session ID, transition-lock name, runtime
path, PID, credential, backend handle, or raw command.

**Rationale**: Operators need to distinguish a bounded establishment wait from
an orphan/unknown block, but an in-memory coordination object is not itself a
public authority record. Existing status/event propagation gives CLI, Manager,
TUI, WebUI, and doctor one authoritative source.

## Decision 9: Prove Both Safety And Liveness

**Decision**: Keep `formal/AttachReservation.tla` as the exhaustive abstract
proof; add deterministic Go barrier tests for both race orderings and every
cancellation boundary; run at least 1,000 seeded randomized schedules and the
Go race detector; record mutation proofs and negative fixtures; use real Lima
for backend identity, restart, cleanup provenance, and 30-sample performance.

**Rationale**: TLC proves the abstract invariants but cannot prove Go lock
ordering, context propagation, filesystem cleanup, provider integration,
redaction, or real backend behavior. Each layer supplies distinct evidence.

## Known Boundaries

- Reservations protect lifecycle-managed reusable run establishment only; they
  do not add authority to native, explain-only, or disposable cleanup paths.
- The `--rm` crash-orphan garbage-collection and removed-journal convergence
  work remains separately tracked in `docs/DEBT.md`.
- Forced process death can leave runtime or an owner; replacement-daemon
  reconciliation must classify that durable residue and never infer a lost
  reservation.
