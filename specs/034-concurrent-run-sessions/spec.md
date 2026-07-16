# Feature Specification: Daemon-Owned Concurrent Run Sessions

**Feature Branch**: `034-concurrent-run-sessions`
**Created**: 2026-07-16
**Status**: Implemented
**Input**: Make `hideout` a thin authenticated client, make the local
`hideoutd` role the single Manager and active-session owner, and use a
session-scoped guest supervisor for terminal, process, and isolation state.
Normal runs must remain local-feeling, support multiple simultaneous sessions
in one existing workspace environment, fail closed on ownership loss, and
avoid the current multi-second real-terminal startup delay.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Run Through One Resident Control Plane (Priority: P1)

An operator runs an ordinary command without first managing a daemon. Hideout
connects to or starts the private local control plane, executes the command
through the same canonical Manager behavior used by every other surface, and
returns the command output and status. The invoking CLI does not become a
second environment or session owner.

**Why this priority**: A single owner is the prerequisite for correct
concurrency, cleanup, crash recovery, and later last-session VM stop. Keeping
an embedded executable fallback would preserve the architecture this feature
is intended to replace.

**Independent Test**: Begin with no running daemon, execute a non-interactive
command, prove exactly one authenticated daemon becomes the run owner, verify
output and exit status, then repeat concurrently from another client without
creating a second control-plane owner.

**Acceptance Scenarios**:

1. **Given** no running daemon, **When** the operator invokes a normal run,
   **Then** one private authenticated daemon becomes ready and owns the run
   without requiring a separate setup command.
2. **Given** a healthy daemon, **When** another CLI invokes a run, **Then** it
   uses the existing daemon rather than creating an embedded Manager/backend
   owner.
3. **Given** daemon startup or authentication failure, **When** a run is
   requested, **Then** it fails with stable recovery guidance and does not
   silently execute through a fallback path.
4. **Given** a run requiring operator confirmation, **When** an interactive
   client accepts the canonical preview, **Then** execution is bound to that
   exact plan; a non-interactive client without acceptance fails closed.

---

### User Story 2 - Use Interactive Tools Like Local Commands (Priority: P1)

An operator runs Bash, Claude, Codex, or another terminal application through
the daemon and receives a responsive, correctly sized terminal. Input, output,
resize, signals, EOF, and exit behavior match the invoking terminal closely
enough that the VM boundary is not an everyday usability burden.

**Why this priority**: A client/server run path that breaks interactive tools
or adds several seconds before every command would be architecturally clean but
not product-usable.

**Independent Test**: Start an interactive terminal, verify its initial and
updated dimensions inside the target, exercise input, interruption, EOF and a
full-screen application, and prove the host terminal is restored after normal
exit, target failure, client loss, and daemon loss.

**Acceptance Scenarios**:

1. **Given** a terminal-attached client, **When** the target starts, **Then**
   it observes the current terminal dimensions before rendering.
2. **Given** a live full-screen target, **When** the operator resizes the host
   terminal, **Then** only that target receives the new dimensions and redraws
   without restarting.
3. **Given** terminal input, Ctrl-C, EOF, or an explicit cancellation,
   **When** it is delivered, **Then** the target receives each action once and
   the final exit result remains truthful.
4. **Given** any normal or abnormal session termination, **When** the CLI
   returns, **Then** the host terminal is no longer left in raw or otherwise
   broken state.
5. **Given** a non-interactive invocation, **When** stdout and stderr differ or
   the target exits nonzero, **Then** their separation and exact result survive
   the client/server path.

---

### User Story 3 - Run Multiple Sessions In One Workspace (Priority: P1)

An operator keeps a shell open while simultaneously running an agent, tests,
another shell, or a one-shot command in the same repository. All runs reuse the
project's existing environment and intentionally collaborate through the
selected workspace.

**Why this priority**: Multiple terminals and agents are the normal developer
workflow. One long-lived command must not reserve the entire VM.

**Independent Test**: Start three sessions against the same pinned workspace,
prove all remain live in one environment, exchange workspace changes, close
one session, and verify both siblings remain functional.

**Acceptance Scenarios**:

1. **Given** one active command in an existing environment, **When** another
   command starts from the same pinned workspace, **Then** it starts without an
   environment-busy failure.
2. **Given** three concurrent sessions, **When** one writes a workspace file,
   **Then** the host and other sessions observe the normal direct workspace
   result.
3. **Given** two live sessions, **When** one exits or its client is killed,
   **Then** the other session's process, terminal, workspace, HostFS, and
   network state remain usable.
4. **Given** concurrent runs while a stopped environment is starting,
   **When** activation completes, **Then** they converge on one environment
   instance and distinct sessions.

---

### User Story 4 - Keep Session Authority Separate (Priority: P1)

Concurrent commands share project files but do not share Hideout's per-run
authority. One target cannot obtain another session's broker credential,
HostFS authority, staged write, network secret, host-app lease, control path,
process view, or terminal stream.

**Why this priority**: Moving ownership into a long-lived daemon must not turn
ephemeral per-run capabilities into ambient daemon-wide or VM-wide authority.

**Independent Test**: Give session A HostFS and host-capability authority while
session B has none, probe all ambient process and control surfaces from B, then
close A and prove B remains live without receiving or losing unrelated
authority.

**Acceptance Scenarios**:

1. **Given** session A has a HostFS read decision or staged write, **When**
   session B probes or requests it, **Then** B cannot discover, consume,
   approve, or apply A's authority.
2. **Given** ordinary targets in sibling sessions, **When** either inspects
   environment, descriptors, mounts, processes, shims, and control files,
   **Then** it observes none of the sibling's control-plane state.
3. **Given** one session is cleaned, **When** its mounts, routes, bridges,
   credentials and process tree are removed, **Then** sibling resources are
   unchanged.
4. **Given** guest-root code performs the same probes, **When** results are
   documented, **Then** Hideout does not claim that a shared VM contains it.

---

### User Story 5 - Recover Ownership Truthfully (Priority: P2)

An operator can see which sessions own an environment, cannot stop the VM
underneath live work, and receives bounded recovery when a client, daemon,
guest supervisor, or backend disappears.

**Why this priority**: A resident daemon improves lifecycle truth only if its
own crash and credential lifecycle are handled explicitly rather than hidden
behind stale metadata.

**Independent Test**: Observe two active sessions, refuse an explicit stop,
kill one client, kill the daemon, restart it, inspect orphan/recovery status,
and stop the environment only after cleanup is proved.

**Acceptance Scenarios**:

1. **Given** active sessions, **When** status is requested, **Then** host-local
   surfaces report their identities and lifecycle without exposing credentials
   or raw control paths.
2. **Given** at least one live session, **When** environment stop is requested,
   **Then** it fails closed without interrupting work.
3. **Given** a client connection is lost, **When** ownership reconciliation
   runs, **Then** only that session is cancelled and cleaned; it does not become
   a detached background job.
4. **Given** the daemon crashes, **When** clients and guest sessions observe
   the loss, **Then** clients restore terminals, guest targets do not continue
   with unowned authority, and restart does not silently re-adopt or destroy
   ambiguous resources.
5. **Given** a long-running authorized session, **When** operator credentials
   rotate or expire, **Then** authorized renewal keeps the session controllable
   while stale clients cannot open or retain access.
6. **Given** the final session exits normally, **When** cleanup completes,
   **Then** this feature retains the environment until explicit stop; automatic
   final-session stop remains separate work.

### Edge Cases

- Two clients race to start the daemon.
- Two or more runs arrive while the same stopped environment is activating.
- A run fails after registration but before target authority becomes usable.
- A confirmation plan changes between preview and acceptance.
- A client disconnects while output is backpressured or cleanup is in progress.
- A terminal emits control bytes resembling protocol data.
- A raw terminal input byte and a host signal represent the same Ctrl-C action.
- Terminal dimensions change repeatedly before and during target startup.
- An operator credential expires during a multi-hour interactive session.
- The daemon crashes while multiple targets and capability providers are live.
- A guest supervisor exits without a trustworthy target result.
- Persisted session metadata exists after its live connection is gone.
- Explicit stop races with new session registration.
- A target ignores normal termination and leaves descendants.
- One session's HostFS unmount, network cleanup, or bridge cleanup fails.
- A slow client cannot consume target output at production speed.
- A requested session uses an unsupported protocol, runtime helper, or terminal
  mode.
- Ordinary target code and guest-root code perform the same sibling probe.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: The local daemon becomes the resident runtime for
  existing Manager run, environment, broker, HostFS, network, terminal, and
  host-capability domains. No new generic host, guest, VM, root, or shell action
  is introduced.
- **Fail-closed behavior**: Daemon absence, authentication failure, stale plan,
  missing confirmation, unsupported session protocol, ownership ambiguity,
  isolation failure, credential-renewal failure, active-session stop, and
  incomplete cleanup all deny or terminate the affected operation without
  embedded fallback or false success.
- **User authority and policy**: Manager remains the canonical policy and
  plan/apply authority. The initiating CLI presents pre-run confirmation;
  runtime decisions use the existing claim/resolve/default-deny decision model.
  Terminal text is never approval.
- **Generality and provider scope**: Daemon-owned sessions are generic Hideout
  behavior. Shells, agents, Git, editors, and full-screen tools are fixtures,
  not command-specific Core semantics.
- **Evidence surface**: CLI, Manager, daemon events, audit, doctor, and
  documentation derive session truth from one model. Real terminal and real
  Lima evidence are mandatory for terminal, process, mount, crash, and latency
  claims.
- **Secret/redaction boundary**: Per-run credentials remain session-scoped and
  never appear in client frames, daemon-global status, events, or evidence.
  Existing deterministic local redaction and export boundaries remain in
  force.
- **Backend/gate expectation**: Local model and race tests cannot replace the
  required macOS arm64 Lima and real-terminal gates. Native remains a weak
  development harness.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Every executable normal run MUST use the authenticated local
  daemon session service. Daemon startup or connection failure MUST provide
  typed recovery and MUST NOT fall back to embedded backend execution.
- **FR-002**: The daemon MUST execute runs through the same canonical Manager
  plan/apply and provider contracts used by other product surfaces and MUST
  NOT expose generic host, VM, root, guest-shell, or profile-write authority.
- **FR-003**: The daemon authority transport MUST be private to the local
  operator and structurally unreachable from supported real backend guests.
  Browser UI transport MUST NOT become terminal authority transport.
- **FR-004**: The CLI MUST remain a presentation and terminal client. It MUST
  NOT receive backend credentials, broker tokens, setup credentials, raw
  HostFS handles, proxy secrets, or authority to execute a backend directly.
- **FR-005**: Canonical run planning, drift revalidation, and confirmation
  binding MUST complete before target authority becomes usable. Missing
  required confirmation MUST fail closed.
- **FR-006**: One authenticated client connection MUST own at most one active
  run session. Connection loss MUST trigger ordered cancellation and cleanup,
  not implicit detach or background continuation.
- **FR-007**: Per-run credentials and capability-provider state MUST remain
  session-scoped, non-reusable, absent from daemon-global persistence, and
  unavailable to clients and sibling sessions.
- **FR-008**: A fixed trusted guest session supervisor MUST own the target
  process tree, terminal, session isolation view, signals, and reaping. Client
  input MUST NOT be able to select privileged executables, privileged scripts,
  or arbitrary setup commands.
- **FR-009**: Interactive startup MUST avoid the current known multi-second
  terminal-allocation delay and MUST be evaluated through a real terminal from
  invocation to first target output.
- **FR-010**: The run stream MUST carry bounded, unambiguous terminal data,
  non-interactive output, input, EOF, resize, signals, notices, errors, and
  target completion without polling or silent output loss.
- **FR-011**: Interactive sessions MUST preserve initial dimensions, dynamic
  resize, single delivery of control input, target completion, and host
  terminal restoration. Non-interactive sessions MUST preserve stdout/stderr
  separation and exact exit result.
- **FR-012**: Target-controlled terminal bytes MUST NOT be interpreted as
  authorization or protocol-control messages. Slow-client handling MUST remain
  bounded and truthful.
- **FR-013**: Runs resolving to the same existing environment and pinned
  workspace MUST execute concurrently without changing environment identity,
  backend instance identity, or direct workspace transport.
- **FR-014**: Every session MUST retain unique runtime, shim, broker, HostFS,
  staged-write, network, terminal, process, host-capability, and audit
  ownership.
- **FR-015**: Each ordinary target process tree MUST receive a session-scoped
  process and control view before per-run authority becomes usable.
- **FR-016**: Ordinary target code MUST NOT observe or consume sibling
  environment, credentials, mounts, descriptors, grants, staged writes,
  network state, processes, terminal, or control paths.
- **FR-017**: Guest-root cross-session containment MUST remain an explicit
  non-claim. Operators requiring a VM wall MUST continue to use a separate
  environment.
- **FR-018**: Workspace changes MUST remain immediate direct read/write effects.
  Workspace-external access MUST retain existing HostFS visibility, read,
  decision, and staged-write policy.
- **FR-019**: Environment lifecycle transitions, active-session registration,
  and stop refusal MUST serialize through the daemon without reserving the
  environment for the target-command lifetime.
- **FR-020**: Explicit environment stop MUST fail closed while any live session
  exists. Releasing the final session in this feature MUST NOT automatically
  stop, clean, delete, compact, or recreate the environment.
- **FR-021**: Normal, failed, forced, and partial cleanup of one session MUST
  NOT invalidate a sibling's process, terminal, workspace, HostFS, network, or
  host-capability state.
- **FR-022**: Daemon failure MUST close affected clients, restore their host
  terminals, terminate guest targets through loss of session ownership, and
  leave unproved resources fail-closed rather than silently re-adopted or
  destroyed.
- **FR-023**: CLI, Manager, daemon events, audit, and doctor MUST report active
  session identity, state, cleanup, and recovery from one authoritative model
  without exposing credentials or raw control-plane paths.
- **FR-024**: Product documentation MUST NOT claim daemon-owned concurrent runs
  until real Lima, real terminal, race, crash, redaction, and end-to-end
  evidence pass.
- **FR-025**: Operator credential rotation and session renewal MUST support
  multi-hour daemon and run lifetimes. Expired or stale credentials MUST fail
  new or unrenewed access without requiring routine daemon restart or exposing
  credentials to the guest.

### Key Entities

- **Daemon Runtime**: The single authenticated per-user Manager runtime for one
  Hideout store. It owns environment transition serialization, active session
  workers, status, audit, and recovery.
- **Thin Client Connection**: One authenticated operator connection carrying
  run intent, terminal interaction, and rendered results. Its loss is an
  ownership event, not an implicit detach request.
- **Run Session**: One target execution with immutable session, environment,
  workspace, profile, terminal-mode, owner, and lifecycle facts.
- **Session Worker**: The daemon-owned active object containing one session's
  backend interaction and ephemeral capability providers. Its credentials are
  not daemon-global state.
- **Guest Session Supervisor**: The fixed trusted guest component that creates
  the session process/control view, owns the terminal and target tree, applies
  control actions, and reaps the target.
- **Session Lease**: Live proof binding one client connection, daemon session,
  and guest supervisor. Persisted metadata alone is not liveness proof.
- **Session Authority Set**: Per-run broker, HostFS, network, endpoint,
  host-capability, terminal, process, and audit state unavailable to siblings.
- **Session Summary**: Redacted host-local lifecycle and recovery facts that
  contain no reusable credential or raw authority path.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Starting a run with no daemon creates exactly one authenticated
  daemon owner, and all executable run entry points use that owner with zero
  embedded backend fallbacks.
- **SC-002**: Non-interactive commands preserve byte-exact stdout/stderr
  separation and exact zero, nonzero, and signal-derived completion results.
- **SC-003**: Interactive commands receive correct initial and updated terminal
  dimensions, input, interruption and EOF exactly once, and the host terminal
  is restored after every tested exit and failure path.
- **SC-004**: On the reference real-terminal lane, an already-running
  environment emits the target's first output within 2.0 seconds at p95 from
  host invocation, including client, daemon, session, isolation, and terminal
  setup.
- **SC-005**: Three simultaneous runs in one pinned workspace use one existing
  environment and start without an environment-busy failure.
- **SC-006**: Concurrent sessions observe the same direct workspace file
  effects while reporting distinct session, broker, HostFS, terminal, and
  process ownership.
- **SC-007**: The complete ordinary-target sibling probe set observes zero
  sibling credentials, mounts, descriptors, processes, grants, staged writes,
  network state, terminal data, or control paths.
- **SC-008**: Normal exit or forced client death removes only the owning
  session within the bounded cleanup window and leaves every sibling usable.
- **SC-009**: HostFS authority and staged writes remain unavailable to siblings,
  and staged host mutation remains absent before the owning typed apply.
- **SC-010**: Every stop attempted with a live session is refused, the same stop
  succeeds after proved cleanup, and normal zero-session completion alone does
  not stop the environment.
- **SC-011**: Daemon termination closes every client, restores every tested host
  terminal, leaves zero deliberately headless targets, and restart never treats
  ambiguous metadata as live authority or safe deletion proof.
- **SC-012**: One representative full-screen agent or terminal application
  remains usable through startup, redraw, resize, interruption, and exit.
- **SC-013**: Client streams, status, events, audit, and evidence contain zero
  injected broker tokens, proxy secrets, setup credentials, raw daemon paths,
  or sibling HostFS authority.
- **SC-014**: Documentation truth rejects claims of cross-workspace shared
  default, final-session auto-stop, detach/re-attach, guest-root containment,
  browser terminals, or complete terminal-emulator hardening.
- **SC-015**: Short-lifetime clock-controlled evidence proves credential
  rotation, authorized session renewal, stale-client rejection, and continuous
  authorized control without waiting real hours.

## Assumptions

- Formal 034 replaces the executable daemon-less run path. This intentionally
  supersedes 006's initial explicit-opt-in daemon adoption and the early
  Phase-1 no-daemon run constraint.
- The daemon remains a runtime for existing Manager authority, not a new policy
  or generic execution layer.
- The daemon role may continue to be entered through the main Hideout
  executable; a separately distributed binary is not required by this feature.
- Formal 034 retains existing per-workspace environment identity and writable
  workspace transport. Cross-workspace shared default belongs to 035.
- The environment remains running after final-session cleanup. Automatic
  non-destructive stop belongs to 036.
- Client disconnect ends the owning session. Detach/re-attach requires a later
  explicit ownership design.
- Dynamic resize is part of the 034 usable terminal baseline. Exhaustive
  terminal-emulator behavior, theme transparency, unusual control sequences,
  and any detach/re-attach matrix belong to 037.
- Terminal output remains target-controlled and may exercise the same terminal
  escape surface as a local CLI. This feature does not claim universal terminal
  escape sanitization.
- The current non-root target identity and intentionally shared profile state
  remain unchanged.
- macOS arm64 with Lima is the required isolation and performance evidence
  platform. Native tests remain a weak harness.

## Dependencies

- Existing authenticated local daemon placement, token, audit, event, and
  Manager parity contracts from 006.
- Existing live-console and decision-center behavior from 007 and 012.
- Existing concurrent session layout, environment transition, guest isolation,
  HostFS, network, and cleanup baseline already present in the old 034
  implementation.
- Existing guest privilege separation and root-control setup identity from 009.
- Existing HostFS write/read, host-capability projection, and community recipe
  contracts from 010, 029, 030, and 032.
- Existing supported runtime and package evidence needed to ship a fixed guest
  supervisor.
