# Feature Specification: Disposable Orphan Recovery

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `042-disposable-orphan-recovery`

**Created**: 2026-07-23

**Status**: Draft

**Input**: User description: "Continue overall Hideout convergence by finishing
proof-gated crash recovery for `--rm` disposable environments, eliminating
orphan lifecycle journals, and retaining fail-closed behavior for every
non-disposable or unproved resource."

## Context

`hideout run --rm` already creates a distinct persisted disposable environment,
deletes its Lima instance after an ordinary run, confirms stable instance
absence, and retains the environment in an error state when teardown cannot be
proved. This closes the normal-return path, including target-command failure.

Two gaps remain. First, a daemon or client crash can strand a disposable
environment before the ordinary finalizer completes. Restart reconciliation
reports unowned resources but intentionally never destroys them, even though
`Disposable=true` is durable operator pre-authorization for eventual removal.
Second, the successful finalizer can remove the environment record while
leaving its lifecycle journal behind. A later daemon correctly treats that
journal as unproved, but the state never converges.

The product needs one resumable disposal protocol shared by ordinary `--rm`
completion and restart recovery. It must bind the exact disposable record and
backend instance before destructive work, prove that no live owner remains,
prove backend termination and stable absence, clear only that environment's
runtime authority, and converge the environment record and lifecycle metadata.
At every uncertain boundary it must retain enough durable state for a later
retry or explicit operator recovery.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Recover A Disposable Run After A Crash (Priority: P1)

An operator starts a command with `--rm`, and Hideout or its client crashes
before normal teardown completes. When the daemon next starts, it recognizes
the exact pre-authorized disposable environment, safely completes its teardown,
and removes the residue without requiring the operator to discover internal
files or backend identifiers.

**Why this priority**: A supposedly disposable run that survives a crash can
consume disk, retain a running guest, and block later lifecycle work. Automatic
recovery is the core unfinished product promise of `--rm`.

**Independent Test**: Interrupt a disposable run at each durable recovery
boundary, restart the daemon, and verify that every fully proved case removes
the exact instance, environment record, runtime authority, and lifecycle
metadata while leaving unrelated environments unchanged.

**Acceptance Scenarios**:

1. **Given** a supported disposable environment whose prior daemon and session
   owners are proved gone, **When** restart recovery observes its exact backend
   instance, **Then** recovery terminates that instance if needed, proves stable
   absence, and removes all state owned by the disposable environment.
2. **Given** a disposable environment whose backend instance is already
   absent, **When** restart recovery runs, **Then** it completes metadata
   cleanup idempotently without recreating or starting the instance.
3. **Given** recovery is interrupted after destructive work begins, **When** a
   later daemon retries, **Then** it resumes from durable facts and reaches the
   same removed or explicitly blocked outcome.

---

### User Story 2 - Refuse Ambiguous Or Unauthorized Cleanup (Priority: P1)

An operator can trust that automatic orphan recovery never broadens into a
general environment sweeper. Ordinary environments, live sessions, corrupted
records, mismatched instances, and unknown backend observations remain intact
and surface a stable cleanup-required reason.

**Why this priority**: Automatic destruction is acceptable only because
`--rm` records explicit pre-authorization for one exact environment. A false
positive would be data loss and a privacy-boundary failure.

**Independent Test**: Mutate each required proof independently and verify that
recovery performs zero destructive backend calls, retains the environment or
journal evidence, and reports a bounded reason without hidden paths or process
details.

**Acceptance Scenarios**:

1. **Given** an otherwise identical environment without durable disposable
   authorization, **When** restart recovery scans it, **Then** no automatic
   destructive action occurs.
2. **Given** a live or unprovable session owner, **When** recovery evaluates a
   disposable environment, **Then** cleanup is blocked before backend
   destruction.
3. **Given** an unknown backend state or an instance identity mismatch,
   **When** recovery evaluates the candidate, **Then** it retains recovery
   evidence and reports a stable fail-closed reason.
4. **Given** a lifecycle journal with no trustworthy disposable intent or
   corresponding record, **When** the daemon starts, **Then** it remains
   blocked for explicit recovery rather than being deleted as harmless residue.

---

### User Story 3 - Keep Record And Journal State Convergent (Priority: P2)

An operator completes an ordinary `--rm` run or a recovered run and observes no
environment row, no runtime residue, and no lifecycle status for that removed
environment. A crash during metadata cleanup never leaves an unclassifiable
journal-only identity.

**Why this priority**: A removed record with a surviving journal turns proven
success into a permanent reconciliation blocker. The record and journal must
be parts of one resumable disposal outcome.

**Independent Test**: Inject failure or process loss between every environment,
runtime, gateway, and lifecycle metadata transition. After restart, verify the
state either converges to complete removal or retains a classifiable disposable
record/intent with a retryable reason.

**Acceptance Scenarios**:

1. **Given** ordinary `--rm` teardown is fully proved, **When** final metadata
   cleanup completes, **Then** neither the environment inventory nor lifecycle
   inventory contains the removed identity.
2. **Given** lifecycle metadata cleanup succeeds but final record removal
   fails, **When** the daemon restarts, **Then** the retained disposable record
   is recognized as a recovery candidate rather than an ordinary reusable
   environment.
3. **Given** the environment record is unavailable while lifecycle metadata
   remains, **When** the metadata contains a valid exact disposal intent,
   **Then** recovery may finish only the already-authorized cleanup after
   re-establishing all required absence proofs.
4. **Given** any metadata identity is unsafe, malformed, or inconsistent,
   **When** recovery evaluates it, **Then** no record or journal is silently
   discarded.

---

### User Story 4 - Preserve `--rm` Run Semantics (Priority: P3)

An operator continues to use `--rm` with ordinary and ephemeral identity modes.
Target exit status still surfaces, cleanup disposition remains visible, and the
new crash protocol does not change workspace, network, HostFS, or identity
authority.

**Why this priority**: Recovery must finish the existing disposable contract,
not redefine `--rm`, `--ephemeral`, or the authority granted to a target.

**Independent Test**: Run successful and failing targets with `--rm`, including
the supported `--rm --ephemeral` combination, and verify exact target status,
disposition, identity isolation, and absence of backend/record/journal residue.

**Acceptance Scenarios**:

1. **Given** a target exits unsuccessfully, **When** disposable teardown is
   proved, **Then** the target failure is returned and the environment is still
   removed.
2. **Given** teardown cannot be proved, **When** the run returns, **Then** the
   result names a cleanup-required disposition and retains a copyable operator
   recovery path.
3. **Given** `--rm --ephemeral`, **When** the run completes, **Then** disposable
   environment cleanup and session-local identity cleanup both occur without
   merging the two concepts.

### Edge Cases

- The daemon crashes before a disposal intent is durable.
- The daemon crashes after intent is durable but before backend cleanup starts.
- Backend deletion returns an error after actually deleting the instance.
- The first absence observation succeeds and the second reports running or
  unknown.
- A session owner lock is live, stale, unreadable, malformed, or changes while
  recovery is evaluating it.
- The environment record changes between inventory and transition-lock
  acquisition.
- The record is disposable but its backend, instance name, or durable intent
  does not match the observed resource.
- The lifecycle journal is absent before cleanup, disappears concurrently, or
  cannot be removed safely.
- Runtime, gateway, owner, or lifecycle cleanup fails after backend absence is
  proved.
- Record removal fails after every other cleanup step succeeds.
- Multiple stranded disposable environments are discovered at once.
- A new daemon shuts down while recovery work is in progress.
- A normal reusable environment and a disposable environment reference
  similarly named backend instances.
- Historical journal-only residue predates the durable disposal protocol.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: daemon-owned environment lifecycle, destructive backend
  cleanup, session ownership evidence, environment-scoped gateway/runtime
  cleanup, and lifecycle metadata removal. No new target or host capability is
  introduced.
- **Fail-closed behavior**: Missing disposable authorization, live or
  unprovable ownership, malformed identity, unknown backend state, unstable
  absence, cleanup failure, or shutdown interruption blocks final removal and
  retains classifiable recovery evidence. No generic orphan is destroyed.
- **User authority and policy**: `--rm` remains the explicit operator request
  that creates durable one-environment disposal authorization. Recovery cannot
  infer authorization from an `rm-` name, status string, backend inventory, or
  missing record, and it cannot override profile, workspace, HostFS, network,
  or deny policy.
- **Generality and provider scope**: The recovery protocol is generic lifecycle
  behavior for a backend capable of exact observation and cleanup. The promoted
  product claim remains limited to the compatible macOS arm64 Lima path with
  real evidence; native behavior is deterministic mechanics only.
- **Evidence surface**: Run results, environment and lifecycle status, daemon
  events, audit, doctor/recovery guidance, and retained product evidence expose
  removed, retrying, or cleanup-required outcomes using stable reason codes
  derived from authoritative recovery facts.
- **Secret/redaction boundary**: Evidence contains no owner paths, lock names,
  process identifiers, daemon sockets, runtime directories, backend command
  lines, tokens, proxy credentials, raw target arguments, or workspace content.
- **Backend/gate expectation**: Gate 0 must cover protocol invariants, seeded
  crash schedules, mutations, negative fixtures, and finite-state model
  checking. A clean exact-package macOS arm64 Lima Gate 2 must prove ordinary
  cleanup, daemon-crash recovery, journal convergence, and `--rm --ephemeral`.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Automatic destructive recovery MUST consider only a supported,
  validated environment with durable disposable authorization or a durable
  disposal intent that was created from such an environment.
- **FR-002**: A generated name, error status, absent record, orphan report, or
  backend instance alone MUST NOT be treated as disposable authorization.
- **FR-003**: Recovery MUST acquire current daemon singleton ownership and the
  exact environment lifecycle transition boundary before destructive work.
- **FR-004**: Recovery MUST reload and revalidate the disposable record and its
  exact backend instance identity after acquiring the transition boundary.
- **FR-005**: Recovery MUST prove that no live or unprovable session owner
  remains before invoking destructive backend cleanup.
- **FR-006**: Recovery MUST record a durable, bounded disposal intent binding
  the authorized environment, exact backend instance, and recovery generation
  before destructive work begins.
- **FR-007**: A durable disposal intent MUST be idempotent, must not broaden its
  bound identity, and must remain distinguishable from ordinary lifecycle
  state after a crash.
- **FR-008**: A running exact disposable instance MAY be terminated only through
  the existing typed backend cleanup path after FR-001 through FR-007 pass.
- **FR-009**: An already stopped or absent exact instance MUST be handled
  idempotently without starting, recreating, or adopting it.
- **FR-010**: Backend cleanup success alone MUST NOT prove disposal; recovery
  MUST observe the exact instance absent in two consecutive bounded
  observations.
- **FR-011**: Any running, stopped, absent, or unknown observation whose
  instance identity does not match the durable intent MUST fail closed.
- **FR-012**: After stable backend absence, recovery MUST close only the
  environment-scoped gateway and clear only the disposable environment's
  session owners, session runtime, activation, service, and related authority.
- **FR-013**: Environment and lifecycle metadata MUST use a resumable ordering
  in which a crash cannot leave an unclassifiable journal-only identity.
- **FR-014**: A fully removed environment MUST have no environment record,
  owner/runtime state, lifecycle journal, coordinator status, or exact backend
  instance.
- **FR-015**: If cleanup fails before full removal, recovery MUST retain or
  recreate a classifiable disposable recovery state, mark the outcome
  cleanup-required, and preserve an explicit retry or clean path.
- **FR-016**: Restart recovery MUST be bounded and cancellable; daemon status
  and operator control surfaces MUST remain available while multiple candidates
  are evaluated.
- **FR-017**: Recovery MUST serialize with attach, stop, clean, idle expiry, and
  other destructive mutation for the same environment while allowing unrelated
  environments to progress.
- **FR-018**: Ordinary successful `--rm` completion and crash recovery MUST use
  the same disposal protocol and convergence rules.
- **FR-019**: Target-command failure MUST remain independent from teardown
  proof: a failed target is still disposed when cleanup is proved, and its exit
  failure still surfaces.
- **FR-020**: `--rm` and `--ephemeral` MUST remain orthogonal and supported
  together without weakening environment cleanup or session-local identity
  cleanup.
- **FR-021**: Ordinary reusable environments and historical orphan evidence
  without trustworthy disposable authorization MUST retain the existing
  report/block/explicit-recovery behavior.
- **FR-022**: Audit, status, doctor, and run results MUST expose stable
  redacted recovery phases, outcomes, and reason codes without control-plane
  material.
- **FR-023**: Every new recovery assertion MUST have a recorded mutation proof,
  and every new intent/evidence judge MUST have a negative fixture that
  demonstrates rejection.
- **FR-024**: The feature MUST add no target-visible authority, copied
  workspace, backend fallback, implicit environment selection, or new
  operator-facing command.

### Key Entities

- **Disposable Environment**: The existing one-run environment record carrying
  explicit disposable authorization, backend identity, exact instance
  identity, runtime provenance, status, and last run/session facts.
- **Disposal Intent**: A bounded durable statement derived from a validated
  disposable environment before destructive work. It binds one environment,
  one backend instance, one recovery generation, current phase, and redacted
  outcome/reason facts; it grants no authority beyond the original `--rm`.
- **Recovery Proof Set**: Authoritative observations for daemon ownership,
  transition serialization, session-owner state, backend instance identity,
  termination, stable absence, and environment-scoped metadata cleanup.
- **Recovery Outcome**: One of removed, cleanup-required, or interrupted. It
  identifies the exact environment, durable phase reached, stable reason code,
  and whether operator action is required without exposing hidden paths.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For at least 100 seeded crash/interleaving schedules spanning
  every durable disposal boundary, every run converges to complete exact
  removal or an explicit cleanup-required state; none produces an
  unclassifiable residue or affects another environment.
- **SC-002**: A clean real-backend test interrupts disposal at every supported
  crash checkpoint and proves 100% of authorized candidates are recovered on
  restart while all unauthorized, live-owner, identity-mismatch, and unknown
  cases make zero destructive calls.
- **SC-003**: Thirty consecutive ordinary disposable runs complete with zero
  remaining environment records, exact backend instances, runtime/owner
  directories, or lifecycle journal identities.
- **SC-004**: A responsive backend reaches a removed or cleanup-required
  restart outcome within 60 seconds per candidate, while daemon status becomes
  available within 10 seconds of daemon start.
- **SC-005**: Every successful disposal leaves environment inventory,
  lifecycle inventory, and backend inventory agreeing that the exact disposable
  identity is absent.
- **SC-006**: Successful and failing targets, including `--rm --ephemeral`,
  preserve exact target result and produce the correct removed or
  cleanup-required disposition in all retained real-backend scenarios.
- **SC-007**: Mutation testing demonstrates that removing disposable
  authorization, live-owner refusal, exact-instance matching, stable-absence
  confirmation, or metadata convergence causes the corresponding gate to fail.
- **SC-008**: All public recovery evidence passes deterministic redaction and
  contains zero control-plane paths, lock/process details, credentials, raw
  target arguments, or workspace content.

## Assumptions

- `--rm` remains an explicit per-run operator request; no profile or project
  default creates disposable authorization.
- The existing dedicated disposable environment and exact instance identity are
  retained rather than converting `--rm` to a record-less or shared-machine
  path.
- The backend used for automatic crash recovery can observe an exact instance,
  perform typed cleanup, and support bounded stable-absence confirmation.
- Acquiring the per-store daemon singleton lock proves the previous daemon
  process no longer owns in-memory authority; it does not by itself prove
  session, guest, or backend absence.
- Historical journal-only residue without a valid durable disposal intent may
  require explicit operator recovery and is not retroactively trusted.
- Bulk cleanup of unknown backend instances, general orphan adoption, crash
  recovery for non-disposable environments, and a new garbage-collection CLI
  are outside this feature.
