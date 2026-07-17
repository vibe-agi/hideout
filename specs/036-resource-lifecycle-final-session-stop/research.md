# Research: Resource Lifecycle And Final-Session Stop

## Decision 1: Model Three Independent Relations

**Decision**: Keep lifecycle ownership, runtime dependency, and capability
authorization as separate relations. The lifecycle API can register and close
effects but cannot grant an operation.

**Rationale**: Current Manager providers own authority while daemon workers own
connections. Treating a worker count or lease as permission would move the
security boundary into lifecycle bookkeeping.

**Alternatives considered**:

- Infer liveness from active session counts: rejected because a worker exists
  before its environment is known and providers can outlive intermediate
  phases.
- Put capability handles in the daemon registry: rejected because it would make
  the daemon a second authority plane.

## Decision 2: Use A Daemon-Owned Coordinator With Manager Registration

**Decision**: The daemon is the single lifecycle writer. It supplies a narrow
registrar to `RunServiceDependencies`; Manager registers the planned
environment dependency while holding the existing environment transition lock
and before backend authority becomes usable.

**Rationale**: Independent CLI clients require one serialized timer/stop owner,
but Manager is the only layer that knows when a typed provider becomes usable
or is proved closed. This split preserves both properties.

**Alternatives considered**:

- Let each Manager call own its timer: rejected because concurrent clients can
  race and duplicate stop attempts.
- Register the daemon connection as the VM pin: rejected because connection
  identity precedes environment selection and does not prove provider cleanup.

## Decision 3: Bind Work To A Backend Incarnation

**Decision**: Identify a live root as stable environment ID + host-issued start
generation + backend instance name + observed guest boot ID. Allocate the
generation under the environment lock; bind it only after observation.

**Rationale**: An external `limactl` restart can reuse the instance name. An
environment ID or mutable status alone permits stale timers and leases to act
on a new boot (the ABA problem).

**Alternatives considered**:

- Environment ID only: rejected because it spans many boots.
- Activation receipt only: rejected because it is removed during ordinary
  final-owner cleanup and cannot observe stopped or absent instances.
- Add another Lima configuration version: rejected because lifecycle identity
  is operational state, not backend configuration.

## Decision 4: Add A Narrow Backend Lifecycle Observer

**Decision**: Add a backend-neutral observer returning
`running|stopped|absent|unknown`, instance name, boot ID when running,
observation time, and a bounded reason code. Lima implements it from inventory
plus the guest kernel boot ID. Native reports not applicable.

**Rationale**: `environment.Record.Status` describes Hideout workflow, not
backend truth. Runtime verification additionally assumes a supported image
contract and is too broad. Stop, attach, status, and restart must consume the
same independent fact source.

**Alternatives considered**:

- Trust `limactl stop` exit code: rejected because command completion does not
  prove the final state.
- Reuse supported-runtime verification: rejected because lifecycle observation
  must work for every preserved Lima environment, including older/custom
  images with the fixed Hideout bootstrap contract.

## Decision 5: Store Discovery Metadata, Not Authority

**Decision**: Persist an atomic, store-rooted lifecycle journal containing
generations, typed nonterminal resources, dependency references, idle deadline,
and stop-attempt identity. Provider locks, current daemon memory, and backend
observation remain the sources of liveness proof.

The owning Manager path plans its complete registration subgraph before any
provider effect, then commits that graph in one atomic journal transaction.
Routine state refinements and successful releases may be coalesced into a
bounded 500 ms checkpoint because the committed planned graph remains a safe
over-approximation after a crash. New boot binding, orphan/cleanup failure,
reconciliation, idle deadline, stop attempt, and coordinator close flush
synchronously.

**Rationale**: A daemon restart needs an index of effects to probe, but JSON
cannot prove a child process, socket, bridge, or VM is alive. Treating it as
authority would silently re-adopt stale effects.

**Alternatives considered**:

- Memory only: rejected because restart cannot discover incomplete cleanup.
- Persist credentials/handles: rejected because they are authority and stale
  after daemon loss.
- Append every transition forever: rejected because audit already owns
  immutable evidence; the operational journal must remain bounded.
- Sync every routine state transition independently: rejected because it adds
  multiple filesystem syncs to every warm command without improving the
  conservative restart envelope established by the complete planned graph.

## Decision 6: Use A Closed Resource Catalog And Pure Reducer

**Decision**: One production live-resource catalog declares kind status,
allowed owner, dependencies, persistence class, close policy, one of three
typed recovery probes, and public label. A separate bounded fact catalog
classifies retained/handoff history without dependency edges. A pure reducer
validates live transitions and derives stop eligibility. Tests, schemas,
status, and docs consume the same descriptors.

**Rationale**: Command-name cases or parallel lists will drift. A pure reducer
allows bounded exhaustive exploration of the exact production state machine.

**Alternatives considered**:

- Interface-only provider registration: rejected because unknown kinds and
  invalid close semantics would be discovered only at runtime.
- A second test catalog: rejected because it can turn missing production
  wiring into a false-green test.

## Decision 7: Make Stop Mode An Edge And Close Policy A Kind Rule

**Decision**: Dependency edges use `pin` or `drain`. Resource descriptors use
`pre-stop-drain`, `co-terminate-with-root`, `survive-root`, or
`external-unmanaged` to define absence proof.

**Rationale**: A network service may depend on a VM but must not self-pin it;
its session consumer pins while the service drains. A host app handoff has no
VM edge and no close handle. Persistence is orthogonal to both.

**Alternatives considered**:

- Put `persistent`/`ephemeral` in the stop predicate: rejected because a
  retained HostFS overlay and audit evidence are valid while the VM is stopped.
- Give every live resource a pin: rejected because support services would keep
  their own root alive forever.

## Decision 8: Preserve Current Explicit Recovery Stop Semantics

**Decision**: Automatic stop refuses every orphaned or unclassified possible
VM dependency. Explicit non-destructive stop may use observed root stop to
prove a stale guest-internal resource absent only after the existing
kernel-backed owner is proved absent and the catalog allows co-termination.

**Rationale**: Current explicit stop deliberately recovers failed/stale session
metadata after the VM is stopped. Removing that path would strand operators;
granting it to automatic stop would destroy uncertainty fail-closed behavior.

**Alternatives considered**:

- Treat current `OwnerStateFailed` as terminal failure: rejected because its
  implementation explicitly means cleanup is not proved.
- Force stop all unknown resources: rejected because corrupt/unclassifiable
  owner state may still be live.

## Decision 9: Use A Fixed 15-Second Idle Grace

**Decision**: Start one visible 15-second, incarnation-bound deadline after the
last pin is proved released. New registration cancels it under the environment
lock. A replacement daemon never resumes the old timer and may start one fresh
grace only after complete reconciliation.

**Rationale**: Immediate stop causes repeated short commands to pay VM startup;
an indefinite warm period violates the user's explicit last-session-stop
expectation. Fifteen seconds covers clustered commands while remaining visible
and bounded.

**Alternatives considered**:

- Zero seconds: rejected as excessive churn.
- Five seconds: too short for normal command/agent pauses.
- Thirty seconds: unnecessarily delays expected resource release.
- User-configurable or infinite in 036: deferred to a future visible keep-warm
  policy; hidden exceptions are prohibited.

## Decision 10: Observe Stop Before Committing It

**Decision**: Recheck the graph and incarnation under lifecycle serialization,
reject the attempt if any pin or drain remains live/unproved, persist a
current-daemon stop attempt, invoke backend stop, and commit only after
observing the expected instance stopped or validly absent. Provider authority
cleanup happens on the ordinary Manager/session path before lifecycle metadata
is released dependent-first. Unknown becomes
`stopping-unknown` and blocks attach. Lima uses a 35-second total transaction
bound: the existing stop command has at most 30 seconds and an independent
follow-up observation has at most 5 seconds.

**Rationale**: Backends can time out, return early, or change externally. A
truthful state model must distinguish a requested side effect from its observed
result.

**Alternatives considered**:

- Mark stopped before invocation: rejected because failure creates false
  success.
- Mark stopped on exit zero: rejected because follow-up observation can remain
  running or unknown.

## Decision 11: Keep Host Handoffs And Retained State Outside VM Pins

**Decision**: Successful direct host-app launches become bounded audit/history
only. Retained HostFS overlays, decisions, and evidence survive stop. Current
host-to-guest bridges remain run-scoped and pin through their originating live
run; detached leases remain design-ready only.

**Rationale**: The current launcher intentionally does not own the host GUI
process. Inventing a close handle would overclaim control. Conversely, a live
bridge does depend on the guest and must be closed before stop.

**Alternatives considered**:

- Keep VM alive for every command-originated host effect: rejected because
  origin does not imply dependency.
- Kill host apps at session end: rejected because Hideout neither owns nor can
  reliably identify the independent process.
- Promote current bridges to detached lifetime: rejected because no consumer
  lease exists in production.

## Decision 12: Bound Daemon Shutdown And Leave Warm On Insufficient Time

**Decision**: Stop accepting new work, drain/cancel existing daemon session and
background owners within their bounds, cancel lifecycle deadlines and in-flight
stop callbacks, and wait for those callbacks for at most the coordinator close
bound. Do not initiate a new backend stop during daemon shutdown. Keep running
environments warm, record deferred truth, and never transfer an old stop
attempt.

**Rationale**: Existing daemon shutdown has a smaller outer budget than some
Manager cleanup and backend-stop paths. Starting new stop work while authority
is being torn down, blocking indefinitely, or recording stopped without proof
is worse than leaving a preserved VM running for the replacement daemon to
reconcile.

**Alternatives considered**:

- Extend shutdown without bound: rejected because terminal/service shutdown
  must remain predictable.
- Fire a detached stop goroutine: rejected because it would outlive the daemon
  owner and create transferable authority.

## Decision 13: Stage Enablement Through Shadow Parity

**Decision**: Land the catalog and journal in observation-only mode, prove
parity with existing owner/provider cleanup, run the stop predicate in shadow,
then enable automatic stop only after model/race and real Lima evidence pass.

**Rationale**: Lifecycle tests can be green while a production provider has no
registrar or probe. Shadow comparison against current kernel owner truth catches
that class of implementation theater before side effects are enabled.

**Alternatives considered**:

- Enable timers as soon as unit tests pass: rejected because provider wiring
  and real backend observation are the critical claims.

## Decision 14: Serve Authenticated Status Before Reconciliation Ends

**Decision**: Startup durably marks every eligible environment pending for the
new daemon epoch, then starts authenticated status service before bounded
environment-scoped probes complete. Reconciliation runs with bounded
parallelism. Attach waits for the exact environment's in-flight probe; stop and
destructive mutation fail closed for that environment while it is reconciling.

**Rationale**: Serial 5-second backend observation plus 7-second session-absence
proof can exceed the client's 10-second autostart timeout and grows with stored
environment count. Serving without a persisted pending fence would instead let
attach race unreconciled discovery state.

**Alternatives considered**:

- Increase the client timeout: rejected because latency still grows with
  environment count.
- Serve all operations immediately: rejected because attach could bypass
  restart reconciliation.
- Block the whole daemon on one slow environment: rejected because unrelated
  status and environments do not share that proof dependency.

## Decision 15: Reconciliation Retry Is Environment-Scoped And Authenticated

**Decision**: The daemon exposes one authenticated environment-scoped retry
that uses the same provider factory, backend observer, owner probes, coordinator
fence, and journal transition as startup. Concurrent retries coalesce or fail
as already in flight; no retry consumes persisted state as current proof.

**Rationale**: A transient inventory/provider failure must not require daemon
restart, but retry is control-plane work and must serialize with attach, stop,
and destructive mutation.

## Decision 16: Alternative Materializations Use Distinct Closed Kinds

**Decision**: Model a host-only retained snapshot and a guest-live projection as
different design-ready resource kinds. The live variant requires a pin edge to
the backend incarnation. Both remain unavailable to production registration in
036.

**Rationale**: The catalog deliberately treats every declared dependency rule
as required. Making a root edge optional would allow a VM-dependent effect to
silently disappear from the stop closure.
