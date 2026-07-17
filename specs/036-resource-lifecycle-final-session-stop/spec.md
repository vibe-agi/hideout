# Feature Specification: Resource Lifecycle And Final-Session Stop

**Feature Branch**: `036-resource-lifecycle-final-session-stop`
**Created**: 2026-07-16
**Status**: Draft
**Input**: Stop a preserved VM after the final VM-dependent resource ends,
without closing independent host applications or deleting retained state, using
one lifecycle model that remains correct across concurrent attachment, cleanup
failure, backend restart, and daemon restart.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Stop An Unused VM Without Deleting State (Priority: P1)

An operator runs a bounded command or exits the final shell. After a short,
visible idle grace, Hideout stops the unused VM while retaining its environment
disk, caches, staged HostFS changes, decisions, and evidence. The next run
restarts the retained environment rather than recreating it.

**Why this priority**: Leaving every VM running after its final session is a
daily resource and battery cost. Stopping too early or deleting retained state
would be worse, so the stop must be non-destructive and dependency-driven.

**Independent Test**: Run a command in a stopped preserved environment, allow
the final resource to close, observe the 15-second grace and a real backend
stop, verify retained bytes are unchanged, then run again and observe a new
backend incarnation using the same retained environment.

**Acceptance Scenarios**:

1. **Given** one final ordinary command with no surviving VM dependency,
   **When** it exits and ordered cleanup succeeds, **Then** the environment
   enters visible idle grace and stops after 15 seconds.
2. **Given** retained environment data, HostFS overlay objects, decisions, and
   audit evidence, **When** automatic stop completes, **Then** all retained
   content remains unchanged and no clean, delete, compact, or recreate occurs.
3. **Given** an environment stopped automatically, **When** a new run starts,
   **Then** Hideout creates and observes a new backend incarnation before the
   target receives authority.
4. **Given** a named preserved environment rather than a generated default,
   **When** its final dependency ends, **Then** it follows the same visible
   dependency-based stop policy.

---

### User Story 2 - Keep Independent Host Effects Alive (Priority: P1)

An operator opens a host-backed workspace or file in a host application from a
guest command. The command and guest session may end and the VM may stop, while
the host application and original host resource remain usable.

**Why this priority**: Host capability projection is useful only when a
successful host handoff is not incorrectly treated as a permanent VM lease or
closed when the guest session exits.

**Independent Test**: Launch real VS Code with isolated safe-mode state against
a disposable host-backed workspace, end the run, observe VM stop, and verify the
test-owned host process survives and the host resource remains intact without
claiming that Hideout controls GUI behavior.

**Acceptance Scenarios**:

1. **Given** a successful direct handoff of a host-backed workspace,
   **When** the originating run ends, **Then** the handoff does not appear as a
   VM pin and the host application is not terminated by Hideout.
2. **Given** a retained HostFS staged object with no live guest dependency,
   **When** the VM stops, **Then** the object remains available for the existing
   explicit apply or discard workflow.
3. **Given** a host-only effect whose live process is not owned by Hideout,
   **When** status is shown after the run, **Then** Hideout presents bounded
   handoff history rather than claiming a live closeable resource.

---

### User Story 3 - Keep Required Guest Resources Alive (Priority: P1)

An operator keeps an interactive target or an existing run-scoped
host-to-guest endpoint bridge active. Hideout does not stop the exact VM
incarnation while that resource remains live. Ending one sibling resource does
not affect another.

**Why this priority**: Automatic stopping is safe only if every real guest
dependency prevents the VM from disappearing underneath active work.

**Independent Test**: Start two concurrent sessions and a run-scoped endpoint
bridge, close them one at a time, prove the surviving session remains usable,
and observe grace only after the final VM-dependent resource is released.

**Acceptance Scenarios**:

1. **Given** two live sessions in one environment, **When** one exits,
   **Then** the other remains usable and no idle timer is started.
2. **Given** a live run-scoped endpoint bridge, **When** idle eligibility is
   evaluated, **Then** the current VM cannot stop until ordered run cleanup
   closes the bridge.
3. **Given** a drainable environment support service with no remaining demand,
   **When** ordinary provider cleanup succeeds, **Then** its lifecycle metadata
   is released before idle grace; if cleanup is unproved, it blocks stop without
   being reported as independent user demand.
4. **Given** a new attach racing the idle deadline, **When** either operation
   obtains lifecycle serialization first, **Then** the target either binds to a
   proved running/new incarnation or waits for stop and restarts; it never
   enters an incarnation being stopped.

---

### User Story 4 - Refuse Unsafe Stop Under Uncertainty (Priority: P1)

A client, provider, daemon, or backend fails during startup or cleanup. Hideout
reports bounded orphaned or unknown state and does not automatically stop a VM
whose possible live dependencies cannot be disproved.

**Why this priority**: Crash recovery is where a session count or persisted
status most easily becomes false. Uncertainty must prevent automatic action
rather than be converted into false success.

**Independent Test**: Interrupt each lifecycle phase, restart the daemon,
change the backend boot identity, and inject ambiguous stop observation. Prove
that old generations cannot act, automatic stop remains denied under
uncertainty, and explicit recovery works only with independent absence proof.

**Acceptance Scenarios**:

1. **Given** a daemon restart and nonterminal resource records, **When** the new
   daemon starts, **Then** it independently reconciles them and neither adopts
   old authority nor destroys ambiguous resources.
2. **Given** a fully proved idle environment after daemon restart, **When**
   reconciliation completes, **Then** exactly one fresh full grace period may
   begin; an old timer is never resumed as authority.
3. **Given** the backend stop command returns success but stopped state cannot
   be observed, **When** status or attach is requested, **Then** the environment
   remains stopping-unknown and is not reported stopped or attachable.
4. **Given** the backend boot identity changes outside Hideout, **When** it is
   observed, **Then** all prior-generation timers and leases are invalid for the
   new incarnation.
5. **Given** a stale guest-internal resource whose external owner is proved
   absent, **When** the operator explicitly stops the environment, **Then** an
   observed root stop may prove that catalog-approved resource absent; the same
   exception is never used for automatic stop.
6. **Given** a live owner or corrupt/unclassifiable owner state, **When** either
   automatic or explicit stop is requested, **Then** stop fails closed with
   bounded recovery guidance.

---

### User Story 5 - Explain Why An Environment Is Running (Priority: P2)

An operator can determine whether an environment is pinned, in grace, eligible,
blocked, stopping, unknown, or stopped, and which class of resource determines
that result, without seeing credentials or raw control paths.

**Why this priority**: Dependency-driven lifecycle is supportable only when the
operator can distinguish a live demand from retained data, a host handoff, or
an unproved orphan.

**Independent Test**: Construct each activity state through the same production
lifecycle model and compare CLI, machine output, event, doctor, UI, and audit
classification and redaction.

**Acceptance Scenarios**:

1. **Given** live pins, retained state, host handoffs, or orphaned resources,
   **When** status is requested, **Then** each class is displayed separately
   rather than collapsed into an active-session count.
2. **Given** an idle deadline, **When** status is requested, **Then** the
   environment identity, remaining grace, and stop eligibility are bounded and
   consistent across surfaces.
3. **Given** injected control-plane tokens, paths, proxy values, descriptors,
   process IDs, target text, or application arguments, **When** lifecycle
   evidence is rendered, **Then** secret/control material is absent and
   target-controlled text remains bounded.

### Edge Cases

- The final release races a new attach or a second idle timer.
- Two stop requests target the same backend incarnation.
- A stale timer from an old boot fires after an external backend restart.
- Startup fails after provisional generation allocation but before backend
  identity observation.
- Stop returns success but backend observation times out or reports running.
- A daemon exits while sessions are draining and the shutdown budget expires.
- A live resource is known only through a recovery journal after daemon loss.
- A current session record uses the existing `failed` spelling even though
  cleanup is not proved.
- A resource kind is unknown, design-ready, fixture-only, cyclic, missing a
  dependency, or registered with an invalid generation.
- A support service fails to drain before backend stop.
- An independent host application outlives the session that launched it.
- A retained HostFS object exists with no live provider or guest.
- An environment was never booted, is already stopped, is absent, or has a
  backend other than a stop-capable preserved Lima instance.
- A clean daemon shutdown lacks enough time to finish an observed stop.
- One stored environment has a slow or timing-out lifecycle provider while an
  unrelated client needs authenticated daemon status.
- A backend observation is transiently unknown and becomes definitive without
  restarting the daemon.
- A design-ready product concept has both a host-only retained shape and a
  guest-live VM-dependent shape.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Environment, backend, daemon, session, broker, HostFS,
  network, endpoint, host-capability, evidence, and lifecycle coordination. No
  new host or guest capability action is introduced.
- **Fail-closed behavior**: Unknown resource kinds, invalid generations,
  missing dependencies, cycles, live owners, unclassifiable state, failed
  drains, backend identity drift, ambiguous stop results, and incomplete
  restart reconciliation deny automatic stop. Explicit stop remains bounded by
  catalog-approved recovery proof and cannot become a destructive override.
- **User authority and policy**: Manager remains the sole capability authority.
  Lifecycle ownership and liveness records never grant HostFS, network,
  endpoint, host-app, or backend authority. Existing explicit apply/discard,
  stop, clean, and decision workflows remain separate.
- **Generality and provider scope**: The lifecycle model is generic. Lima is the
  first stop-capable preserved backend and the required real proof surface.
  Code, browsers, ADB, editors, shells, and agents are examples or fixtures,
  never command-specific Core lifecycle rules.
- **Evidence surface**: CLI, machine status, Manager, daemon events, doctor,
  TUI/WebUI, audit, model exploration, race tests, and a real Lima gate consume
  one lifecycle classification. Product claims require real backend evidence.
- **Secret/redaction boundary**: Lifecycle records and output exclude tokens,
  raw control paths, raw host/guest paths, proxy values, descriptors, process
  IDs, and application arguments. Target-controlled reason text is bounded and
  deterministically redacted at existing boundaries.
- **Backend/gate expectation**: Gate 0 covers the shared model, schemas,
  provider contracts, race tests, and documentation. A real macOS arm64 Lima
  gate proves boot identity, stop observation, retained-state survival, crash
  recovery, and attach-versus-stop behavior. Native is not VM-stop evidence.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Lifecycle ownership, runtime dependency, and capability authority
  MUST be represented and enforced as separate relations.
- **FR-002**: The daemon MUST be the single writer for live resource lifecycle
  coordination and environment stop attempts.
- **FR-003**: Manager Core MUST remain the sole typed capability authority;
  lifecycle registration or ownership MUST NOT grant operational authority.
- **FR-004**: Every live managed effect MUST have a closed resource kind,
  owner, generation, state, dependency set with per-edge stop modes, metadata
  persistence class, and close policy.
- **FR-005**: Resource dependencies MUST be acyclic and MUST bind to the exact
  backend incarnation when they depend on a preserved VM.
- **FR-006**: Automatic stop MUST be derived from the production dependency
  closure and MUST NOT use command spelling or a mutable resource/session count
  as authority.
- **FR-007**: Planned, starting, active, or draining VM-pinning resources and
  every unproved possibly VM-dependent resource MUST prevent automatic stop.
- **FR-008**: Host-only effects, retained records, and evidence records MUST NOT
  prevent VM stop unless they have an explicit transitive VM dependency.
- **FR-009**: A live effect MUST be registered before it becomes usable and
  MUST be released only after its declared cleanup proof succeeds.
- **FR-010**: Unknown kinds, missing dependencies, invalid generations, cycles,
  invalid transitions, and unproved cleanup MUST fail closed.
- **FR-011**: Stop and attach MUST re-evaluate lifecycle truth under the same
  environment transition serialization.
- **FR-012**: Starting a stopped environment MUST allocate a new provisional
  generation and bind it to a backend-observed instance identity before target
  authority becomes usable.
- **FR-013**: An externally changed backend boot identity MUST supersede the
  prior incarnation and invalidate every prior-generation timer and lease.
- **FR-014**: Automatic stop MUST stop only the backend instance. It MUST NOT
  clean, delete, compact, recreate, discard overlays, remove evidence, or
  terminate an independent host application.
- **FR-015**: Owning providers MUST complete authority cleanup before lifecycle
  release. Lifecycle metadata release MUST then preserve sibling resources and
  follow reverse dependency order for each registration-owned subgraph.
- **FR-016**: A capability with no managed result MUST NOT leave a post-response
  effect that depends on a Hideout runtime resource.
- **FR-017**: Idle grace MUST be 15 seconds, visible, bounded, generation-bound,
  cancellable by new registration, and revalidated before stop.
- **FR-018**: Lifecycle status MUST distinguish live VM pins, independent host
  handoffs, retained state, orphaned/unproved state, idle grace, stop in
  progress, ambiguous stop, and observed stopped. A blocked or ambiguous result
  MUST carry the bounded machine-readable recovery reason produced by the same
  persisted production transition.
- **FR-019**: Public lifecycle output MUST exclude credentials, raw control
  paths, raw host/guest paths, descriptors, process IDs, proxy values, and
  application arguments.
- **FR-020**: One closed production resource catalog and transition model MUST
  drive registration, validation, status, schemas, model exploration, drift
  guards, and documentation proof.
- **FR-021**: Implemented, design-ready, and fixture-only resource kinds MUST be
  distinguished; only implemented kinds with a production registrar MAY affect
  the production stop predicate.
- **FR-022**: Restart discovery metadata MUST be durable, bounded,
  non-authoritative, and contain the complete planned resource subgraph before
  effect usability. Routine state refinements MAY use a bounded checkpoint only
  while that durable planned graph remains a conservative recovery envelope.
  Every implemented live kind MUST map to one closed typed recovery contract,
  and daemon startup MUST dispatch every contract or block reconciliation.
- **FR-023**: A replacement daemon MUST NOT inherit old child authority or an
  old stop attempt. It MAY create a new stop attempt only after complete
  reconciliation of the currently observed incarnation.
- **FR-024**: Backend stop command completion MUST NOT prove lifecycle state.
  Stop, attach, status, and restart reconciliation MUST consume one typed
  backend observation contract.
- **FR-025**: A VM-dependent pin MUST block stop. A live or unproved drainable
  resource MUST also block automatic stop until provider cleanup or restart
  reconciliation proves absence. An allowed guest-internal orphan MAY use
  observed root stop only through explicit recovery.
- **FR-026**: Automatic stop MUST reject every orphaned or unclassified possible
  VM dependency. Explicit non-destructive stop MAY co-terminate a stale
  guest-internal resource only when external owner absence is proved and the
  catalog permits that proof; both operations MUST refuse live or
  unclassifiable owners.
- **FR-027**: The environment-bound planned dependency MUST be registered while
  the environment transition is serialized; a pre-environment client worker
  count MUST NOT be treated as a VM pin.
- **FR-028**: Clean daemon shutdown MUST remain bounded. It MUST cancel idle
  deadlines and in-flight stop callbacks, wait only within a fixed bound, record
  truthful deferred evidence, and leave a running environment warm rather than
  starting a new stop transaction during shutdown.
- **FR-029**: Restart reconciliation MUST NOT make authenticated daemon
  readiness grow serially with environment count. Before serving an attach for
  an environment, the daemon MUST classify that environment as reconciling,
  complete, or blocked. One slow environment MUST NOT prevent authenticated
  status for the store beyond three seconds.
- **FR-030**: A transient blocked reconciliation MUST have a bounded,
  authenticated in-process retry path serialized with attach, stop, and
  destructive mutation. Retry MUST reuse the same typed probes, MUST NOT infer
  success from journal state, and MUST NOT require a daemon restart.
- **FR-031**: Design-ready resources with alternative lifecycle shapes MUST be
  represented by distinct closed kinds or an explicitly validated closed
  alternative. A VM-dependent variant MUST NOT omit its root edge merely
  because a host-only variant of the same product concept exists.

### Key Entities

- **Stable Environment**: Retained configuration and storage identity that may
  survive many backend starts and stops.
- **Backend Incarnation**: One observed running backend instance, identified by
  stable environment, host-issued start generation, backend instance identity,
  and guest boot identity.
- **Managed Resource**: A typed live effect with owner, generation, lifecycle
  state, dependencies, metadata persistence, and close policy.
- **Lifecycle Fact**: A bounded, non-authoritative retained/handoff
  classification with no dependency edge and no role in stop eligibility. The
  underlying decision, overlay, environment, or audit subsystem remains
  authoritative.
- **Dependency Edge**: A relation indicating that one resource requires
  another, classified as demand-pinning or drainable for a lifecycle root.
- **Close Policy**: The catalog-owned rule for proving a resource absent before
  root stop, from root stop, or independent of root stop.
- **Lifecycle Journal Entry**: Durable but non-authoritative discovery metadata
  used to find nonterminal resources after daemon loss.
- **Stop Attempt**: A serialized operation owned by one daemon and bound to one
  complete backend incarnation and observation outcome.
- **Retained Record**: Product data kept independently of current liveness,
  such as environment state, staged HostFS objects, or decisions.
- **Evidence Event**: Append-only operator evidence distinct from mutable
  lifecycle and retained operational records.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Bounded exhaustive exploration finds zero violations of stopped
  root safety, dependency validity, generation isolation, sibling preservation,
  fail-closed uncertainty, and retained-state independence.
- **SC-002**: At least 100 real attach-versus-stop races complete without a
  target entering a stopping/stopped incarnation or a live dependent being
  interrupted.
- **SC-003**: A final ordinary command causes an observed non-destructive Lima
  stop within the 15-second grace plus a 35-second stop-and-observation bound.
- **SC-004**: Two live sessions prevent stop; closing one preserves the other;
  closing the final session permits stop only after cleanup and grace.
- **SC-005**: A real test-owned workspace-backed host-application process
  survives VM stop, the host resource remains intact, and neither is reported as
  a VM pin or a process still owned by Hideout.
- **SC-006**: A current run-scoped guest endpoint bridge prevents stop while its
  originating run is live and is closed during ordered run cleanup.
- **SC-007**: A retained HostFS overlay survives automatic stop byte-for-byte
  and does not appear as a live VM pin.
- **SC-008**: Unproved VM-dependent state prevents automatic stop and receives a
  bounded typed recovery path.
- **SC-009**: Old-generation leases and timers cannot stop, pin, or authorize a
  newly observed backend incarnation.
- **SC-010**: Lifecycle registry migration adds no more than 5% or 10
  milliseconds, whichever is larger, to median warm bounded-command overhead
  in the recorded baseline workload.
- **SC-011**: Human status, machine status, event, doctor, UI, and audit agree on
  activity, pin count, stop eligibility, generation, and recovery class.
- **SC-012**: Lifecycle evidence contains none of the injected control-plane
  credentials, hidden paths, proxy values, descriptors, process IDs, or
  unbounded target text.
- **SC-013**: A drainable environment support service closes through ordinary
  provider cleanup before backend stop; an unproved residual blocks stop but is
  never mislabeled as independent user demand.
- **SC-014**: Stop command success with an unknown follow-up observation never
  reports stopped and prevents attachment until successful reconciliation.
- **SC-015**: Daemon restart creates no inherited authority, completes bounded
  reconciliation, and starts at most one fresh grace period for a proved-idle
  current incarnation.
- **SC-016**: A real or simulated changed Lima boot identity invalidates 100% of
  timers and leases bound to the prior incarnation.
- **SC-017**: Explicit stop recovers a stale catalog-approved guest resource
  only after observed root stop, while automatic and explicit stop both refuse
  live and unclassifiable owners.
- **SC-018**: Every current failed-cleanup session is classified as
  orphaned/unproved until an independent absence proof exists; none is counted
  as terminal merely because its existing record says `failed`.
- **SC-019**: With multiple stored environments and one deliberately slow or
  timing-out lifecycle provider, authenticated daemon status is available in
  at most three seconds, every environment is explicitly classified, and no
  unreconciled environment accepts attach or receives an idle timer.
- **SC-020**: A transiently unknown backend observation can become complete in
  the same daemon epoch through an authenticated retry. Before proof, attach
  and automatic stop remain blocked; afterward a proved-idle incarnation gets
  at most one fresh full grace period.
- **SC-021**: Catalog validation accepts a host-only materialization fixture
  without a VM edge and a guest-live projection fixture with a required pin
  edge, rejects the guest-live fixture when that edge is omitted, and keeps both
  design-ready kinds out of production registration.

## Assumptions

- Feature 034 is the production baseline: executable runs use one authenticated
  daemon, per-session owner locks, guest supervisors, ordered run cleanup, and
  environment transition serialization.
- Automatic stop applies to preserved Lima environments with a real lifecycle
  observer. Native runs have no VM lifecycle root and are not evidence.
- The current run-scoped endpoint bridge remains run-scoped. Detached browser,
  editor, ADB, and guest-only projection leases are design-ready fixtures only.
- Direct host-app handoff is fire-and-forget after a successful audited launch;
  Hideout does not claim it can terminate or retract content from the host app.
- Current HostFS overlay apply/discard authority and evidence/export boundaries
  remain unchanged.
- A future visible keep-warm preference is separate work; 036 does not create
  environment-name exceptions or hidden infinite keepalive.
- Automatic clean, delete, compaction, environment recreation, detached guest
  jobs, session reattachment, cross-workspace sharing, generic file
  materialization, and bidirectional synchronization remain out of scope.
