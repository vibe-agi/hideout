# Feature Specification: Shared Default VM Across Workspaces

**Feature Branch**: `[035-shared-default-vm-cross-workspace]`

**Created**: 2026-07-17

**Status**: Implemented

**Input**: Product contract from
`.tmp/035-shared-default-vm-cross-workspace-draft.md`: compatible no-flag Lima
runs reuse one profile-backed VM while each session receives its own exact,
live `/workspace` view and existing lifecycle logic stops the VM only after all
dependent resources are released.

## User Scenarios & Testing

### User Story 1 - Reuse One Warm Machine Across Projects (Priority: P1)

An operator starts commands from two projects with the same compatible profile.
Both commands reuse one automatic machine instead of creating one machine per
directory, while each command starts in its own project at `/workspace`.

**Why this priority**: This removes the most visible VM-sprawl and startup-cost
problem while preserving the ordinary `hideout run -- ...` workflow.

**Independent Test**: Hold one command open in project A, start another in
project B, and prove one environment, one backend instance, one boot identity,
and two correct workspace views.

**Acceptance Scenarios**:

1. **Given** two disjoint projects and one compatible profile, **When** the
   operator starts a command in each project, **Then** both sessions use one
   automatic machine and each reports its own project marker at `/workspace`.
2. **Given** the automatic machine is warm, **When** a third project starts
   during idle grace, **Then** pending stop is cancelled and the existing exact
   machine incarnation is reused.
3. **Given** a machine-level profile or runtime change, **When** another run is
   requested, **Then** the operator receives explicit drift guidance rather
   than an unseen second automatic machine.

---

### User Story 2 - Collaborate Through A Live Exact Workspace (Priority: P1)

The host, agent, shell, and editor observe the selected project as one live
read/write tree. Successful changes are real host project changes, not staged
HostFS writes or copy-back results.

**Why this priority**: Reusing a machine is useful only if normal Git, package,
language, editor, and agent workflows remain correct and fast.

**Independent Test**: Exercise create, edit, atomic replace, rename, delete,
mode, symlink, lock, and watcher workflows in both directions against a fixed
fixture and compare them with the current direct workspace behavior.

**Acceptance Scenarios**:

1. **Given** an attached project, **When** the target successfully changes a
   file, **Then** the host observes the completed change without a later apply
   or merge step.
2. **Given** an attached project, **When** the host performs an editor-style
   atomic save, **Then** the target observes the new file within the declared
   convergence bound.
3. **Given** an unsupported filesystem operation, **When** a tool invokes it,
   **Then** the tool receives a stable truthful error and no success is faked.

---

### User Story 3 - Confine Concurrent Views (Priority: P1)

Each ordinary non-root target receives only the authority of its selected
canonical root. Disjoint projects cannot enumerate or open one another through
the workspace surface. Same and nested roots are described according to their
actual overlap instead of being mislabeled isolated.

**Why this priority**: Dynamic attachment must not turn VM reuse into ambient
access to every active project.

**Independent Test**: Run disjoint, identical, and ancestor/descendant fixtures
concurrently and probe mounts, guessed paths, process views, symlinks, and the
workspace protocol from every session.

**Acceptance Scenarios**:

1. **Given** disjoint projects A and B, **When** either ordinary target probes
   outside its selected root, **Then** the sibling project remains unavailable.
2. **Given** two sessions on the same root, **When** one exits, **Then** the
   sibling retains its view and open handles until its own release.
3. **Given** an ancestor and descendant project, **When** both attach, **Then**
   the ancestor retains its selected authority, the descendant cannot escape to
   the ancestor, and operator surfaces show the asymmetric overlap.

---

### User Story 4 - Stop Only After Every Dependency Releases (Priority: P1)

Closing one session releases only its authority. The existing lifecycle model
decides whether the exact VM incarnation may enter idle grace and stop after
workspace views, bridges, services, and all other dependencies are gone.

**Why this priority**: Early detach or stop causes data loss and breaks sibling
commands; hidden retained resources make automatic stop unsafe.

**Independent Test**: Close sessions and dependent resources in different
orders, inject cleanup ambiguity and daemon restart, and observe the exact
lifecycle transitions and backend incarnation.

**Acceptance Scenarios**:

1. **Given** sessions A and B, **When** A exits, **Then** B remains fully usable
   and the VM does not enter grace.
2. **Given** the final session exits while a VM-dependent bridge remains,
   **When** the bridge later releases, **Then** exactly one grace period and one
   stop occur for the observed incarnation.
3. **Given** daemon restart leaves a workspace resource unproved, **When** a new
   attach or automatic stop is requested, **Then** both fail closed until
   explicit recovery proves absence or stops the exact incarnation.

---

### User Story 5 - Understand Sharing And Recover Safely (Priority: P2)

The operator can distinguish the shared machine from its active workspace
views, understand overlap and blockers, and choose a named environment and
distinct profile when stronger separation is required.

**Why this priority**: Sharing a VM changes the trust model; status that still
shows one environment as one workspace would be misleading and unsafe.

**Independent Test**: Inspect CLI, JSON, Manager UI, TUI, doctor, events, audit,
and exported evidence for two active projects with injected path and secret
sentinels.

**Acceptance Scenarios**:

1. **Given** two active projects, **When** the operator inspects the system,
   **Then** one machine and two human-identifiable workspace views are shown,
   with no environment-level "last workspace" value.
2. **Given** the operator needs a separate guest kernel/root disk, **When** they
   follow the shown command, **Then** a named environment uses a distinct Lima
   instance; guidance also explains that a distinct profile is required to
   separate profile-owned state.
3. **Given** a host permission, root identity, capacity, or cleanup failure,
   **When** doctor or status reports it, **Then** the recovery command exists,
   is typed, and does not leak control-plane secrets or raw paths into public
   evidence.

### Edge Cases

- Two first runs race to create the same automatic shared slot.
- Two sessions attach the same canonical root through different lexical paths.
- Selected roots are equal, nested, disjoint, renamed, deleted, or replaced
  while attach or I/O is in progress.
- A selected filesystem cannot provide stable root identity or required live
  filesystem semantics.
- The profile requests preserve-mode paths, or the selected project contains
  linked-worktree metadata with external absolute host paths.
- The operator requests an unsafe broad root such as host home or a parent of a
  reserved Hideout path.
- Host filesystem access is denied or initially unknown because of platform
  permissions.
- One session exhausts view, handle, request, byte, or enumeration limits while
  a sibling performs I/O or teardown.
- A provider disconnects with reads, writes, flushes, or locks in flight.
- Session credentials rotate, expire, or are revoked during a long-lived view.
- Daemon restart finds a live, absent, stale, or unprovable dynamic view.
- A disk-genesis or isolation input drifts after the stable slot has been
  created, while a boot, service, or session input changes independently.
- Native, disposable, Linux, named-environment, and old alpha records reach the
  mode-selection boundary.
- Two named environments use the same profile and therefore still share the
  profile-owned host state even though their VM/boot identities differ.

## Constitutional Alignment

- **Authority touched**: Environment selection, Lima backend attachment,
  workspace direct read/write authority, daemon session ownership, lifecycle,
  broker/host-app workspace mapping, status/UI, and evidence.
- **Fail-closed behavior**: Unsupported transport/path identity, unstable root
  identity, ambiguous cleanup, unknown backend incarnation, incompatible
  machine posture, unavailable host permission, capacity exhaustion, and stale
  credentials stop before new authority or block reuse/stop as appropriate.
- **User authority and policy**: The operator-selected current project remains
  the intentional direct collaboration root. No workspace may broaden to its
  parent, siblings, all known projects, or host home through shared mode.
  HostFS remains a separate policy and staged-write authority.
- **Generality and provider scope**: The contract is a generic workspace and
  lifecycle model. Named shells, Git, languages, editors, Claude, and Codex are
  representative fixtures only and do not become Core semantics.
- **Evidence surface**: Audit, explain, doctor, Manager API, daemon events,
  CLI/TUI/WebUI, support matrix, test plan, and product evidence all distinguish
  machine state from session workspace state.
- **Secret/redaction boundary**: Canonical host roots and workspace identity
  key remain Core-only; guest/public surfaces receive a non-authoritative
  workspace ID, logical root, redacted state, and bounded display label. Tokens,
  raw credentials, hidden control paths, and raw host roots never enter guest or
  shared evidence.
- **Backend/gate expectation**: Local tests may prove contracts, but shared
  cross-workspace support is promoted only by a real macOS arm64 Lima gate bound
  to the accepted transport/path-identity research artifact and installed
  release-shaped package.

## Requirements

### Functional Requirements

- **FR-001**: Compatible no-flag Lima runs from distinct projects MUST select
  one stable profile-backed automatic slot whose identity excludes project path.
- **FR-002**: Machine identity MUST include only disk-genesis and isolation
  facts: backend, architecture, runtime/image content, target OS user/UID,
  guest machine-id, VM/mount implementation, workspace isolation shape, and
  static-Lima workspace access/path presentation where such a mount exists.
  Shared-Portal/native workspace presentation, boot presentation, environment
  services, and other session inputs MUST NOT become recreate axes.
- **FR-003**: Named environments MUST retain a pinned project and distinct Lima
  instance/boot boundary, while surfaces MUST disclose that same-profile
  identity state remains shared and a distinct profile is needed to separate it.
- **FR-004**: Shared automatic sessions MUST present `/workspace` without a fake
  host path and MUST retain a stable opaque project identity that prevents
  distinct projects from being conflated by path-keyed tools.
- **FR-005**: Each selected project MUST have an immutable session attachment
  with a Core-only canonical root, captured stable root identity, and one
  store-keyed non-authoritative workspace ID used by every subsystem.
- **FR-006**: Product implementation MUST remain blocked until one complete
  exact-root transport and path-identity pair passes the documented research
  gate; an API, prototype, private patch, or mount-only demo is insufficient.
- **FR-007**: Workspace authority MUST be confined to the selected canonical
  root and MUST reject parent, sibling, reserved-root, session, incarnation,
  credential, and symlink escapes.
- **FR-008**: Successful workspace changes MUST be live host changes and MUST
  NOT use HostFS staging, later approval, or copy/merge-on-exit semantics.
- **FR-009**: Shared automatic mode MUST NOT mount host home, `/Users`, a common
  source parent, or all known projects; the unsafe-workspace override MUST NOT
  bypass this shared-slot rule.
- **FR-010**: Each session MUST receive a private workspace view at the logical
  path, with explicit same/nested/disjoint overlap semantics and no static
  selected-workspace or dummy workspace in shared machine configuration.
- **FR-011**: Closing one session MUST NOT detach, flush, unmount, interrupt, or
  stop a sibling workspace view.
- **FR-012**: Dynamic workspace authority MUST be owned only by the established
  daemon session path; an invoking CLI process MUST NOT become an alternate
  owner or fallback executor.
- **FR-013**: Host provider, guest view, and any shared transport service MUST be
  represented as separate closed lifecycle resource kinds matching the actual
  process and dependency topology.
- **FR-014**: Workspace resources MUST be registered before side effects and
  become active only after authenticated proof that provider, private view, and
  session supervisor are ready.
- **FR-015**: Automatic VM stop MUST remain exclusively governed by the existing
  dependency predicate, grace period, and exact-incarnation stop protocol.
- **FR-016**: Ambiguous provider cleanup or restart state MUST block automatic
  stop and every new attach/reuse of that incarnation and MUST NOT modify host
  project content.
- **FR-017**: Broker and host-app project mapping MUST resolve through the
  immutable session attachment, never an environment-level project fallback.
- **FR-018**: Guest, adapter, event, and public lifecycle surfaces MUST NOT
  expose the canonical host root, identity key, or control-plane credential.
- **FR-019**: Operator-local audit MUST correlate environment, session, and the
  authoritative workspace ID and continue to use the existing local/export data
  boundary for user paths.
- **FR-020**: Environment network services MUST serialize configuration
  generations. Proxy-upstream and mediated-DNS changes MUST switch online with
  rollback. A direct/proxy posture change MUST keep the same VM but MUST fail
  closed until no sibling target session is active because the guest route is
  environment-global. Raw credentials and session secret files MUST remain
  outside persisted service state. A service change MUST NOT recreate or
  restart the VM.
- **FR-021**: Unsupported filesystem behavior MUST return stable truthful
  errors and MUST NOT report success while dropping data, durability, or locks.
- **FR-022**: Host-to-target and target-to-host metadata/data convergence MUST
  have explicit tested bounds.
- **FR-023**: A new attach during idle grace MUST cancel pending stop through
  the existing serialized transition before provider side effects begin.
- **FR-024**: Attach during an in-flight stop MUST wait or return the existing
  typed transition result and MUST NOT create an untracked replacement VM.
- **FR-025**: Manager API, CLI/TUI/WebUI, live state, events, status, and doctor
  MUST separate machine environments from session workspace views, provide
  non-authoritative human labels and overlap notices, and explain blockers
  without public raw-path leakage.
- **FR-026**: Previous alpha workspace-bound records MUST fail with one direct
  remove/recreate path; no dual-schema reader or silent migration is permitted.
- **FR-027**: Guest-root containment equivalent to separate VMs MUST remain an
  explicit non-claim; named environments separate VM/kernel/root-disk state and
  distinct profiles additionally separate profile-owned state.
- **FR-028**: Implementation MUST stop at the research decision when no
  candidate pair meets all correctness and bounded performance requirements.
- **FR-029**: Shared machine activation MUST be independent of project facts;
  project attachment and target execution MUST remain separate lifecycle steps.
- **FR-030**: A direct dynamic-share implementation MUST begin with no host
  project exposed, bind every admitted root to captured identity, update active
  roots atomically through an authenticated host-only exact-incarnation control
  path, and expose cleanup state without VM restart.
- **FR-031**: A mediated filesystem implementation MUST provide bounded
  multiplexed operations, explicit handles, cancellation, backpressure,
  independent same-root lock owners, truthful disconnect behavior, and
  session-derived credential renewal/revocation.
- **FR-032**: Every implementation MUST bound concurrent views, handles,
  in-flight operations, queued bytes, and enumeration while preserving sibling
  fairness and teardown progress.
- **FR-033**: Every host process that opens or watches a project root MUST be
  inventoried; host permission failures MUST be typed prerequisites and MUST NOT
  be disguised as Hideout policy denial or approval decisions.
- **FR-034**: Product surfaces MUST disclose that a shared VM also shares guest
  kernel, root disk, global tools/caches, profile state, and cataloged
  machine-global services; private workspace views are not separate VM walls.
- **FR-035**: The shipped default profile MUST use alias mode. Shared selection
  MUST reject explicit preserve mode with executable alias/dedicated guidance
  and diagnose known incompatible external absolute project metadata before
  target start.
- **FR-036**: Native runs MUST use explicit workspace-bound mode; disposable
  `--rm` runs own per-run dedicated disposable records removed by the proved
  teardown; `--ephemeral` MUST use the platform's normal
  environment mode with session-local identity; named environments remain
  dedicated and project pinned; none may silently enter the shared automatic
  slot.
- **FR-037**: Guest ownership MUST be synthetic and non-root without changing
  arbitrary host ownership; unsupported ownership and device operations MUST
  fail truthfully.
- **FR-038**: Stable slot selection MUST be independent of compatibility drift;
  machine posture changes MUST report drift instead of silently creating another
  automatic VM.
- **FR-039**: Runtime receipt, audit, bridge, broker, host-app, ownership,
  supervisor, daemon, API, and environment lifecycle paths MUST explicitly use
  machine facts or the immutable session attachment and MUST have no empty or
  last-project fallback.
- **FR-040**: Shared cross-workspace behavior MUST remain platform-gated.
  macOS arm64 Lima may be promoted only by its real gate; unsupported platforms
  retain truthful workspace-bound behavior until an equivalent gate passes.
- **FR-041**: The selected logical/physical path model MUST be tested against
  project-state identities used by representative shells, version control,
  language tools, and agent CLIs so distinct projects do not silently share
  project-local trust, history, sockets, or cache state.

### Key Entities

- **Shared Slot**: Stable automatic selection for one profile, independent of
  project path and separate from compatibility drift.
- **Machine Compatibility Identity**: Canonical digest of facts that affect the
  reusable machine but not session-only authority.
- **Environment Record**: Machine-scoped state with an explicit shared,
  dedicated, or workspace-bound mode.
- **Workspace Attachment**: Immutable session binding between one canonical host
  root, one logical guest root, one workspace ID, one backend incarnation, and
  its lifecycle resources.
- **Workspace Identity Key**: Private per-store key used only to derive stable,
  non-authoritative workspace IDs.
- **Workspace Provider**: Host-side process/share responsible for the live exact
  root; it is distinct from HostFS authority.
- **Guest Workspace View**: Session-private target-visible view and its proven
  mount/cleanup state.
- **Root Relation**: Same, nested, or disjoint relation between active canonical
  roots, used for truthful status rather than authority expansion.
- **Research Decision Artifact**: Schema-validated, digest-bound evidence
  selecting exactly one transport and path-identity pair or recording that 035
  cannot proceed.

## Success Criteria

### Measurable Outcomes

- **SC-001**: Two distinct compatible projects produce one automatic
  environment ID, one Lima instance name, and one observed boot identity.
- **SC-002**: Two simultaneous disjoint sessions each see the correct project at
  `/workspace` and cannot access the sibling fixture; equal/nested fixtures
  match the declared shared/asymmetric authority relation.
- **SC-003**: Host and target create, edit, atomic-replace, rename, delete, and
  mode changes converge with zero lost updates in the fixed correctness fixture.
- **SC-004**: The correctness suite reports zero silent short writes, false
  flush/durability success, same-root lock violations, rename escapes, or
  symlink escapes.
- **SC-005**: In one target process and VM, alternating paired samples compare
  the Portal with the profile cache's static virtiofs mount. A 10,000-entry
  status workload completes in at most 2 seconds and at most twice the paired
  static-control median; the 20,000-operation package fixture is at most three
  times that paired median; host/target atomic-save visibility p95 is at most
  250 ms. The retained research baseline remains the first-target-byte reference
  and transport-selection provenance, not the Git/package load control.
- **SC-006**: Closing one of two workspace views leaves the sibling process,
  view, open handles, network, HostFS, and terminal usable.
- **SC-007**: After final dependency release, exactly one existing grace period
  and one observed stop occur unless another dependency pins the VM.
- **SC-008**: A cross-project attach during grace cancels stop and adds at most
  1 second to mounted-ready time and no more than baseline p95 plus the greater
  of 500 ms or 15% to warm first-target-byte time.
- **SC-009**: Daemon restart never re-adopts old workspace authority, never
  auto-stops with an unproved view, and refuses new attachment to that
  incarnation until explicit recovery.
- **SC-010**: Workspace-scoped host projections from two sessions resolve to
  their respective canonical roots without transmitting either raw root to the
  target.
- **SC-011**: Existing HostFS staged-write behavior is unchanged by the direct
  workspace implementation.
- **SC-012**: Guest/public outputs contain none of the injected host username,
  canonical path, workspace identity key, capability token, or broker secret.
- **SC-013**: Named environments create distinct Lima instance and boot
  identities, reject project mismatch, and truthfully render same-profile state
  sharing plus the distinct-profile escape path.
- **SC-014**: Old development records fail with one executable remove/recreate
  path and no compatibility fallback.
- **SC-015**: Local gates, the real backend gate, evidence validation,
  documentation lint, build, vet, formatting, diff check, and full tests all
  pass on the exact candidate.
- **SC-016**: Environment/API/browser/TUI runtime output shows one machine and
  two human-identifiable views without treating display labels as authority,
  adding an environment-level project value, or exposing raw host roots.
- **SC-017**: Shared backend configuration and guest mount metadata contain no
  static selected project, broad dummy host directory, or raw host path.
- **SC-018**: Inspect and first-run guidance identify the shared trust domain and
  provide working separate-VM and separate-profile-plus-VM commands.
- **SC-019**: Host permission denial, external absolute project metadata, stale
  root identity, provider overload, unstable filesystem identity, and preserve
  mode each produce distinct typed recovery with no partial attachment.
- **SC-020**: Native/workspace-bound, disposable, shared automatic, and named
  dedicated runs pass a mode matrix with no accidental fallback.
- **SC-021**: At declared admission limits, one abusive session cannot exceed
  bounded memory/handles, starve a sibling, or prevent deterministic teardown.
- **SC-022**: Support documentation and runtime behavior agree on every
  platform; unsupported shared attachment retains documented workspace-bound
  behavior instead of claiming parity.
- **SC-023**: Two projects under one shared profile expose distinct physical
  working-directory/project-state identities while interactive output and host
  projection continue to accept logical `/workspace` paths.

## Assumptions

- 034 remains the sole daemon-mediated run/session owner and 036 remains the
  sole generic resource-lifecycle and final-stop authority.
- The product has no real users requiring compatibility; one explicit alpha
  reset is preferable to a dual schema or hidden migration.
- The first promoted platform is macOS arm64 with Lima and the supported runtime
  image. Native remains a weak harness and Linux remains workspace-bound.
- Phase R is an existence gate, not optional research. Phase I cannot begin or
  be marked complete without one accepted transport/path-identity artifact.
- The selected project is intentional direct read/write collaboration authority;
  content protection or per-write approval inside it is out of scope.
- Same-profile machines and named environments intentionally reuse profile-owned
  identity state. A distinct profile is the product mechanism for separating it.
- Guest-root containment equivalent to one VM per project is not claimed.
- Read-only workspace mode, detached session reattachment, generic sync,
  container parity, workspace DLP, new network modes, and arbitrary guest-chosen
  host paths are outside 035.
- Documentation continues to describe automatic environments as
  workspace-bound until clean real-backend evidence proves the new behavior.
