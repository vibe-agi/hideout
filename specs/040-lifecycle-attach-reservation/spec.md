# Feature Specification: Lifecycle Attach Reservation

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `040-lifecycle-attach-reservation`

**Created**: 2026-07-22

**Status**: Implemented

**Input**: User description: "Stabilize run establishment so daemon
reconciliation, environment mutation, cancellation, and restart cannot erase or
misclassify a session while it is being attached."

## Context

A new run currently exposes session runtime state before durable ownership is
established. If daemon reconciliation observes the environment in that window,
it can classify the state as an ownerless orphan and scrub it while the run is
still starting. A seemingly direct fix that makes reconciliation wait on the
environment mutation path creates the opposite failure: the run waits for
reconciliation while reconciliation waits for the run.

The product needs one explicit establishment protocol. A run must reserve its
right to attach before it exposes session runtime state; existing reconciliation
must finish before that reservation is granted, and new reconciliation or
destructive mutation must not begin until the reservation is promoted to normal
session ownership or aborted. Cancellation and daemon restart must leave only
state that the next reconciliation can judge from durable facts.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Start Reliably During Reconciliation (Priority: P1)

An operator starts a command while the daemon is reconciling a reusable
environment. The command waits for the in-progress reconciliation and then
either starts normally or fails with a clear, bounded lifecycle error. Its
session state is never removed while startup is still valid.

**Why this priority**: Losing session state during establishment can turn a
valid run into an unexplained failure and can leave the daemon's view of active
authority inconsistent with reality. This is the safety defect the feature
exists to close.

**Independent Test**: Force reconciliation to begin first, start a run while it
is active, release reconciliation, and verify the run either reaches normal
session ownership or fails before publishing session runtime state. Repeating
the ordering never produces a scrubbed live session or an indefinite wait.

**Acceptance Scenarios**:

1. **Given** reconciliation is already scanning an environment, **When** a new
   run requests attachment, **Then** the run waits without blocking
   reconciliation and proceeds only after reconciliation reaches a terminal
   result.
2. **Given** reconciliation finishes with the environment attachable, **When**
   the waiting run continues, **Then** it establishes session ownership before
   its runtime can be treated as orphaned.
3. **Given** reconciliation finishes with an unproved or blocked result,
   **When** the waiting run continues, **Then** it fails closed with no target
   process, no active-session claim, and no foreign state removed.

---

### User Story 2 - Order Runs And Destructive Mutation Safely (Priority: P2)

An operator starts a run at the same time that another client requests stop,
clean, or another destructive environment mutation. Exactly one operation wins
the establishment boundary. The other receives a bounded refusal or waits
without deadlocking, and the environment never appears both removable and
actively owned.

**Why this priority**: Environment lifecycle actions and run establishment
operate on the same retained resources. Ambiguous ordering can stop or remove a
resource a new run is preparing to use.

**Independent Test**: Force both orderings — run establishment first and
destructive mutation first — and verify that the winner completes, the loser
returns a stable outcome, and no interleaving creates a phantom owner, deleted
live runtime, or lock cycle.

**Acceptance Scenarios**:

1. **Given** a run holds an establishment reservation, **When** stop, clean, or
   reconciliation is requested, **Then** destructive work does not begin until
   the reservation is promoted or aborted.
2. **Given** destructive mutation has already begun, **When** a run requests an
   establishment reservation, **Then** the run cannot publish session state and
   receives a bounded wait or fail-closed result.
3. **Given** two runs establish concurrently, **When** both target the same
   compatible reusable environment, **Then** each receives distinct session
   ownership and neither can remove the other's state.

---

### User Story 3 - Recover Cleanly From Cancellation Or Restart (Priority: P3)

An operator cancels a run during startup, or the daemon restarts before or after
durable ownership is written. The abandoned establishment does not remain as a
live-looking reservation. The next daemon can either prove and clean the
residue or surface a specific blocker without adopting or deleting ambiguous
authority.

**Why this priority**: The establishment window is short, but crashes and
cancellation are normal lifecycle events. Recovery must preserve the same
fail-closed contract as steady-state operation.

**Independent Test**: Cancel or restart at every establishment boundary,
restart reconciliation, and verify that only the interrupted run's owned
residue is removed, durable live ownership is respected, and ambiguous state is
reported rather than silently adopted.

**Acceptance Scenarios**:

1. **Given** a run is cancelled before durable ownership exists, **When** abort
   completes, **Then** only that run's provisional state is removed and its
   establishment reservation is released.
2. **Given** the daemon restarts before ownership is durable, **When** the next
   reconciliation observes the residue, **Then** it may prove and scrub the
   orphan without affecting other sessions.
3. **Given** the daemon restarts after ownership becomes durable, **When** the
   next reconciliation observes it, **Then** it applies the existing liveness
   and fail-closed ownership rules rather than treating it as an unowned
   provisional run.
4. **Given** cleanup cannot be proved, **When** status, doctor, or an environment
   action reports the failure, **Then** the operator receives a stable reason
   and recovery path without control-plane paths, lock names, or credentials.

### Edge Cases

- Cancellation arrives while a run is waiting for an older reconciliation.
- Cancellation arrives after reservation but before any session runtime exists.
- Runtime state is created but durable ownership creation fails.
- Durable ownership succeeds but reservation promotion or status persistence
  fails.
- Two runs reserve the same environment while a stop grace deadline expires.
- Forced stop or clean begins immediately before a reservation request.
- The daemon crashes with only provisional runtime, with both provisional
  runtime and durable owner state, or immediately after promotion.
- Reconciliation returns an unknown backend observation or cannot prove owner
  liveness.
- The environment record changes while a run waits and no longer matches the
  requested profile, runtime, workspace mode, or backend incarnation.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: daemon-owned lifecycle coordination, reusable
  environment mutation, per-run session authority, durable owner evidence, and
  cleanup. No new host capability or operator permission is introduced.
- **Fail-closed behavior**: A run that cannot obtain and promote an
  establishment reservation does not start the target or publish active
  authority. Unknown ownership, backend incarnation, reconciliation, cleanup,
  or mutation state blocks attachment or destructive work before side effects.
- **User authority and policy**: Existing profile, environment, workspace, and
  run policy remain unchanged. The reservation orders internal authority; it
  does not grant access, override a deny, or allow one session to affect
  another.
- **Generality and provider scope**: The contract is generic lifecycle behavior
  for reusable environments. Lima supplies the real isolation proof, but no
  Lima-specific state becomes product semantics.
- **Evidence surface**: Existing lifecycle status, daemon events, environment
  actions, doctor findings, run results, and audit surfaces show establishment
  waits, aborts, blockers, and recovery using stable reason codes derived from
  authoritative coordinator state.
- **Secret/redaction boundary**: Evidence and errors contain no control-plane
  paths, lock-file names, process identifiers, tokens, backend credentials,
  hidden runtime directories, or raw command arguments.
- **Backend/gate expectation**: Native deterministic concurrency tests and
  model checking prove ordering invariants; real macOS arm64 Lima Gate 2 proves
  run/reconciliation/restart behavior and confirms existing warm-attach and
  cleanup claims are preserved. Mutation proofs and negative fixtures are
  required for every new assertion and judge.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: A run MUST obtain an environment-scoped establishment reservation
  before publishing session runtime state that reconciliation could classify as
  orphaned.
- **FR-002**: A reservation request MUST wait for any earlier reconciliation to
  finish without preventing that reconciliation from making progress.
- **FR-003**: While an establishment reservation is active, new reconciliation
  and destructive environment mutation MUST NOT act on that environment until
  the reservation is promoted or aborted.
- **FR-004**: After obtaining a reservation, the run MUST revalidate the current
  environment record and backend incarnation before creating session runtime or
  target-side effects.
- **FR-005**: A run MUST establish durable session ownership before its
  reservation is promoted to the normal active-session lifecycle.
- **FR-006**: Reservation promotion MUST be atomic from the lifecycle
  observer's perspective: the run is either still establishing under the
  reservation or represented by normal durable session ownership, never
  neither.
- **FR-007**: Failure or cancellation at any establishment stage MUST release
  only that run's reservation and remove only state owned by that run.
- **FR-008**: Reservation waits, aborts, and cleanup MUST be bounded by the
  caller's cancellation and timeout; no run, reconciliation, stop, or clean
  operation may wait indefinitely on the protocol.
- **FR-009**: A daemon restart MUST discard in-memory establishment claims and
  let the next reconciliation judge any durable residue using existing
  ownership and backend facts; it MUST NOT silently re-adopt provisional work.
- **FR-010**: Unknown or conflicting ownership, reconciliation, mutation, or
  backend-incarnation state MUST fail closed and surface a stable recovery
  reason.
- **FR-011**: Concurrent compatible runs MUST retain distinct session authority
  and MUST NOT serialize for longer than the establishment boundary requires.
- **FR-012**: Existing final-session stop, stop/clean refusal, grace
  cancellation, shared-workspace, network-service, HostFS, projection, audit,
  and session cleanup behavior MUST remain unchanged outside establishment
  ordering.
- **FR-013**: Lifecycle status, daemon events, doctor, and operator-facing
  errors MUST represent establishment waits, aborts, and blockers without
  exposing control-plane material or raw target arguments.
- **FR-014**: The feature MUST add no new CLI command, profile option, project
  manifest field, guest-visible authority, or implicit fallback path.
- **FR-015**: Every new concurrency assertion MUST have a recorded mutation
  proof, and every new lifecycle judge or gate check MUST have a negative
  fixture that demonstrates fail-closed behavior.

### Key Entities

- **Establishment reservation**: daemon-owned, environment-scoped proof that a
  particular session may cross from allocated intent to durable ownership. It
  has waiting, held, promoted, aborted, and crash-lost outcomes and contains no
  capability material.
- **Provisional session runtime**: session-owned state created only after a
  reservation is held. It is not active authority until durable ownership is
  established.
- **Durable session owner**: the existing observable record and liveness proof
  that replaces the reservation when establishment succeeds.
- **Reconciliation attempt**: the existing environment-scoped observation that
  classifies durable state after restart or ambiguity. It cannot overlap an
  active establishment reservation.
- **Destructive mutation**: stop, clean, or other environment operation that
  could invalidate resources needed by an establishing or active run.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In every forced ordering of reconciliation-first and run-first,
  the run either establishes durable ownership or fails before target launch;
  zero valid establishing runtimes are scrubbed.
- **SC-002**: At least 1,000 randomized establishment/reconciliation/mutation
  schedules complete with no deadlock, cross-session cleanup, phantom active
  session, or unbounded wait.
- **SC-003**: Cancellation at every establishment boundary removes all state
  owned only by the cancelled run within the configured operation timeout and
  leaves all sibling sessions unchanged.
- **SC-004**: Restart checks before and after durable ownership yield a proved
  cleanup, a preserved live owner, or an explicit blocked result in 100% of
  tested cases; no case silently adopts ambiguous work.
- **SC-005**: Across 30 real warm-attach samples, 95% produce first target
  output within the existing 2.0-second contract and reservation handling adds
  no user confirmation or retry step.
- **SC-006**: Real backend tests demonstrate reconciliation-first, run-first,
  cancellation, and daemon-restart behavior with exact source/runtime identity
  and clean evidence provenance.
- **SC-007**: Redaction tests inject control-plane paths, credentials, process
  identifiers, and raw command arguments into failure inputs and observe zero
  occurrences in public lifecycle status, events, doctor output, and exported
  evidence.

## Assumptions

- The existing environment transition serialization, lifecycle coordinator,
  session ownership records, and reconciliation logic remain the underlying
  authorities; this feature changes their establishment ordering rather than
  replacing them.
- The establishment reservation is daemon-local coordination, not durable
  authority. A daemon restart intentionally loses it and relies on durable
  owner and backend facts for recovery.
- Normal concurrent runs remain supported; the reservation is per session and
  blocks only conflicting reconciliation or destructive mutation for the same
  environment.
- Existing lifecycle reason-code, status, doctor, and evidence schemas may be
  extended compatibly if a distinct establishment state is required.
- Disposable-environment crash garbage collection and removed-record journal
  convergence remain owned by the separate `--rm` phase-2 work unless this
  feature's implementation must touch a shared protocol invariant.
