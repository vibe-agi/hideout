# Feature Specification: Concurrent Run Sessions

**Feature Branch**: `034-concurrent-run-sessions`
**Created**: 2026-07-16
**Status**: Implemented
**Input**: Allow multiple `hideout run` sessions to use the same existing
per-workspace environment concurrently without weakening workspace, HostFS,
session-authority, lifecycle, or evidence boundaries.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Run Multiple Commands In One Workspace (Priority: P1)

An operator can keep an interactive shell open and simultaneously run an
agent, tests, another shell, or a one-shot command against the same project.
All runs use the project's existing reusable environment instead of failing
because another command is already active.

**Why this priority**: A single long-lived shell currently blocks the primary
developer workflow. Removing that restriction is the smallest independent
improvement and does not require cross-workspace VM sharing.

**Independent Test**: Start three long-lived commands from one workspace,
prove that all three remain active in the same environment, exchange ordinary
workspace file changes, close one command, and verify that the other two
continue normally.

**Acceptance Scenarios**:

1. **Given** an already-running reusable environment with one active command,
   **When** the operator starts a second command from the same pinned
   workspace, **Then** the second command starts without an
   `environment ... is already in use` failure.
2. **Given** three concurrent sessions in one workspace, **When** one session
   writes a workspace file, **Then** the host and the other sessions observe
   the normal direct workspace result.
3. **Given** two live sessions, **When** one exits normally, **Then** the other
   remains functional and the environment remains running.
4. **Given** a previously stopped reusable environment, **When** concurrent
   runs arrive during startup, **Then** they converge on one truthful
   environment instance rather than creating or corrupting competing startup
   state.

---

### User Story 2 - Keep Session Authority Separate (Priority: P1)

Concurrent commands intentionally share project files but do not share
Hideout's per-run authority. A command cannot obtain another session's broker
credential, HostFS grant, staged write, network secret, control path, process
state, or terminal stream.

**Why this priority**: Concurrency is unacceptable if it turns per-run
credentials or host capabilities into environment-global ambient authority.

**Independent Test**: Give one session HostFS and staged-write authority while
a sibling session has none, probe all ambient session and process surfaces
from the sibling, then close the first session and prove that the sibling's own
resources remain usable.

**Acceptance Scenarios**:

1. **Given** two sessions in one environment, **When** session A receives a
   HostFS read grant or staged write, **Then** session B cannot discover,
   consume, approve, or apply that authority.
2. **Given** concurrent ordinary target processes, **When** either target
   inspects ambient files, environment, descriptors, mounts, and visible
   processes, **Then** it cannot recover the sibling session's control-plane
   state.
3. **Given** two active sessions, **When** one session cleans its mounts,
   network state, bridges, or process tree, **Then** the sibling session's
   corresponding resources remain functional.
4. **Given** one host-side run process terminates unexpectedly, **When** the
   system reconciles ownership, **Then** stale metadata does not become proof
   of a live session and no sibling authority is removed.

---

### User Story 3 - Observe And Control Active Ownership (Priority: P2)

An operator can see the active sessions that own an environment and receives a
clear refusal when attempting to stop an environment that is still in use.
After all owners exit, the operator can stop the environment explicitly.

**Why this priority**: Concurrent ownership must be understandable and must not
make lifecycle commands race with active work.

**Independent Test**: Observe two active session summaries, attempt and fail an
explicit stop, close the sessions, reconcile an abruptly terminated owner, and
then stop the retained environment successfully.

**Acceptance Scenarios**:

1. **Given** two active sessions, **When** the operator inspects environment or
   session status, **Then** host-local surfaces report two distinct session
   identities and the observed environment state.
2. **Given** at least one proved active owner, **When** the operator requests
   environment stop, **Then** the request fails closed with stable recovery
   guidance and does not interrupt any session.
3. **Given** no proved active owners, **When** the operator explicitly stops
   the environment, **Then** the existing non-destructive stop behavior
   remains available.
4. **Given** the final session exits, **When** no explicit stop was requested,
   **Then** this feature leaves the retained environment running and does not
   claim automatic last-session stop.

### Edge Cases

- Two or more runs arrive while the same stopped environment is starting.
- A run fails after registering ownership but before target authority becomes
  usable.
- A session exits while another session is still preparing.
- An explicit stop races with new session registration.
- Session metadata exists but its operating-system owner is gone.
- A target ignores normal termination and leaves descendants running.
- One session's HostFS unmount or bridge cleanup fails.
- Identical sessions share an environment-level network/helper service and one
  exits first.
- A session requests configuration incompatible with the running environment.
- A non-interactive command, an interactive shell, and a full-screen terminal
  program run concurrently.
- The host terminal closes or the host-side process receives an uncatchable
  termination.
- Existing environment records have any currently supported lifecycle state,
  including new, created, ready, running, stopped, error, and
  unsupported-version.
- Ordinary target code and guest-root target code attempt the same
  cross-session probe; only the ordinary-target result is a containment claim.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Environment lifecycle, per-run broker authority,
  HostFS, network setup, process ownership, terminal streams, Manager status,
  audit, and doctor are touched. No new host capability action is introduced.
- **Fail-closed behavior**: Unsupported session isolation, ambiguous ownership,
  incompatible runtime configuration, active-owner stop, failed mount setup,
  and cleanup conflicts deny activation or lifecycle mutation before authority
  is broadened.
- **User authority and policy**: The selected workspace remains the intentional
  direct read/write collaboration surface. Workspace-external files still
  require existing HostFS policy, and HostFS deny, discover, read, decision,
  and staged-write rules remain session-bound.
- **Generality and provider scope**: Concurrent run ownership is generic
  Hideout behavior. Shells, agents, Git, editors, and package managers are
  examples and evidence fixtures, not Core command semantics.
- **Evidence surface**: Manager models, CLI status, audit, doctor, and
  documentation truth report active ownership and cleanup outcomes from one
  authoritative session model. Real Lima evidence is required for isolation
  claims.
- **Secret/redaction boundary**: Broker credentials, proxy secrets, generated
  machine identity, raw control-plane paths, HostFS claim tokens, and sibling
  session authority never appear in target-visible output or evidence.
  Host-local audit retains user data according to the existing deterministic
  redaction and export contracts.
- **Backend/gate expectation**: Local tests may prove models and races, but
  concurrent process visibility, mount separation, HostFS cleanup, and
  environment behavior require real macOS arm64 Lima evidence. Native remains
  a weak development harness.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Runs resolving to the same existing environment and pinned
  workspace MUST be allowed to execute concurrently.
- **FR-002**: The feature MUST preserve existing automatic environment
  identity, workspace pinning, backend instance identity, and record format.
  An existing guest that lacks the required session-view primitives MUST keep
  its record but fail with typed recreate/runtime guidance; it MUST NOT run
  through the former globally visible target path.
- **FR-003**: Environment startup, setup mutation, session-owner registration,
  and explicit stop MUST be serialized without reserving the environment for
  the entire target-command lifetime.
- **FR-004**: Every run MUST retain a unique session identity and unique
  runtime, shim, broker, HostFS, staged-write, terminal, process, and audit
  ownership.
- **FR-005**: Concurrent sessions MUST retain immediate direct read/write
  workspace behavior, and workspace writes MUST NOT create HostFS decisions.
- **FR-006**: Workspace-external host paths MUST continue through existing
  HostFS policy, and write-class operations MUST retain staged
  operator-controlled apply semantics.
- **FR-007**: Each ordinary target process tree MUST receive a session-scoped
  view of process and control state before per-run authority becomes usable.
- **FR-008**: Ordinary target code MUST NOT obtain a sibling session's
  environment, broker credential, HostFS authority, staged write, network
  secret, descriptor, process state, mount, or control path through ambient
  guest visibility.
- **FR-009**: Guest-root cross-session containment MUST remain an explicit
  non-claim, and operators requiring a VM-level wall MUST continue to use a
  separate environment.
- **FR-010**: Active-session liveness MUST be derived from an operating-system
  owned lifetime signal rather than mutable metadata or a PID value alone.
- **FR-011**: Explicit environment stop MUST fail closed while any proved
  active session owner exists and MUST provide stable host-local recovery
  guidance.
- **FR-012**: Releasing the final session owner MUST NOT automatically stop,
  clean, delete, compact, or recreate the environment in this feature.
- **FR-013**: Session cleanup MUST terminate only the owning process tree and
  remove only the owning mounts, bridges, endpoints, credentials, and
  temporary state.
- **FR-014**: Environment-level network or helper services MAY be shared only
  when effective configuration is identical, ownership is explicit, and
  cleanup is reference counted; incompatible requests MUST fail closed.
- **FR-015**: Concurrent interactive and non-interactive sessions MUST retain
  independent streams, initial terminal dimensions, exit status, and host
  terminal restoration. Dynamic resize completion is outside this feature.
- **FR-016**: Manager, CLI, audit, and doctor MUST report active-session
  identity, count, lifecycle state, and cleanup failures from the same
  authoritative model.
- **FR-017**: Ownership ambiguity, session-view setup failure, cleanup failure,
  and shared-service conflict MUST fail closed with typed recovery guidance
  and without claiming successful teardown.
- **FR-018**: Product documentation MUST distinguish this same-workspace
  concurrency feature from cross-workspace shared-default reuse, automatic
  last-session stop, complete dynamic terminal resize, and guest-root
  containment.

### Key Entities

- **Reusable Environment**: The existing stored environment and backend
  instance pinned to one workspace, profile, runtime, and guest identity. It
  may own multiple concurrent sessions but retains its existing identity and
  explicit stop lifecycle.
- **Run Session**: One command execution with immutable session identity,
  environment identity, workspace identity, owner, timestamps, terminal mode,
  and lifecycle state.
- **Session Ownership**: Operating-system-backed proof that one host-side run
  still owns an active session. It carries no broker, HostFS, network, or
  host-app authority.
- **Session Authority Set**: The per-run broker, shims, HostFS grants and
  staged writes, network secrets, bridges, terminal stream, process tree, and
  audit handles that cannot be consumed by a sibling session.
- **Environment Service**: A reusable helper, runtime, profile-state, DNS, or
  proxy service that can be shared only under identical effective
  configuration and explicit reference-counted ownership.
- **Session Summary**: A redacted host-local status record containing session,
  environment, workspace, lifecycle, and terminal-mode facts without
  credentials or raw control-plane paths.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Three simultaneous runs in one workspace use one existing
  environment and complete startup without an environment-busy failure.
- **SC-002**: Concurrent sessions observe the same direct workspace file
  effects while reporting distinct session, runtime, broker, and process-owner
  identities.
- **SC-003**: The full ordinary-target cross-session probe set observes zero
  sibling credentials, control paths, descriptors, process state, mounts,
  HostFS grants, staged writes, or network secrets.
- **SC-004**: Closing any one of two sessions leaves the remaining command,
  workspace, HostFS view, environment service, and terminal stream functional.
- **SC-005**: HostFS authority and staged writes are unavailable to sibling
  sessions, and staged host mutation remains absent before the owning typed
  apply.
- **SC-006**: Every explicit stop attempted with a live owner is refused, and
  the same stop succeeds after all owners are gone.
- **SC-007**: Forced host-process termination releases liveness proof within
  one second, and stale metadata never keeps the active-session count above
  the observed owner count.
- **SC-008**: Environment records, backend instance identity, and static
  workspace mount declarations remain byte-for-byte or semantically unchanged
  except for additive session-summary state explicitly defined by the plan.
- **SC-009**: Concurrent non-interactive commands preserve exact exit status,
  and two simultaneous terminal streams cannot consume or signal each other.
- **SC-010**: Audit and evidence contain zero injected broker tokens, proxy
  secrets, generated machine IDs, HostFS claim tokens, or sibling
  control-plane paths.
- **SC-011**: Documentation truth validation rejects claims that this feature
  provides cross-workspace shared VM reuse, automatic final-session stop,
  complete dynamic resize, or guest-root session containment.
- **SC-012**: On the reference real-Lima lane, an already-running second
  session emits the test target's first ready marker within 2.0 seconds at p95
  from host invocation, and
  same-workspace Git and package filesystem fixtures complete within 1.25x the
  pre-feature static-workspace baseline.

## Assumptions

- Formal 034 covers concurrent sessions only when they resolve to the same
  existing per-workspace environment and pinned workspace.
- The existing writable workspace transport remains unchanged; dynamic
  cross-workspace attachment is a separately gated follow-up.
- The existing non-root target identity and intentionally shared profile
  state remain unchanged unless planning proves an additional identity layer
  is required without breaking workspace ownership.
- The existing environment remains running after the final session until the
  operator requests stop. Automatic daemon adoption and final-session stop are
  separate follow-up work.
- Complete dynamic terminal resize, OSC/CSI fidelity, and appearance-theme
  behavior are separate follow-up work; this feature preserves only the
  concurrent stream behavior it explicitly claims.
- HostFS, network, endpoint, decision, export, adapter, and host-app authority
  contracts remain unchanged and are consumed rather than redefined.
- macOS arm64 with Lima is the required isolation evidence platform. Linux
  unit/integration coverage may support implementation but does not replace
  the real backend gate.
