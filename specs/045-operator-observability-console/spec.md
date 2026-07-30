# Feature Specification: Operator Observability Console

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `045-operator-observability-console`

**Created**: 2026-07-28

**Status**: Draft

**Input**: User description: "Complete Hideout as a release-ready local AI CLI
security and observability console with approachable help, a real-time HUD,
safe editable configuration, online secret and proxy changes, workload-scoped
command/file/network observation, honest coverage, lifecycle-bound retention,
formal concurrency and recovery contracts, and release evidence."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Understand A Live Run At A Glance (Priority: P1)

A professional individual operator runs an unfamiliar or agentic command in
one terminal and opens Hideout's terminal console in another. Without knowing
Hideout's internal generations, leases, workers, or backend mechanics, the
operator can immediately tell what is running, whether isolation and network
posture are healthy, what activity is occurring, whether observation is
complete enough to trust, what is blocked, and what action is available.

**Why this priority**: Hideout cannot be an effective safety boundary if its
primary live surface looks like an unexplained internal metrics dump. The
operator needs an honest operational answer before deeper investigation or
configuration is useful.

**Independent Test**: Start one supported isolated workload, open the terminal
console using only product help, and verify that its command, session,
environment, effective network posture, observation coverage, recent activity,
risk findings, blockers, and next actions are understandable without consulting
source documentation.

**Acceptance Scenarios**:

1. **Given** one healthy active workload, **When** the operator opens the
   console, **Then** the first view identifies the workload, current state,
   effective connection posture, observation coverage, recent meaningful
   activity, and any action required.
2. **Given** no active workload, **When** the console opens, **Then** it shows
   an intentional idle state and a runnable next command rather than empty or
   ambiguous panels.
3. **Given** multiple concurrent workloads, **When** the operator changes the
   selected session, **Then** every process, file, network, risk, and lifecycle
   panel changes to the selected workload without mixing evidence.
4. **Given** a warning, blocker, failed operation, or unproved state, **When**
   it appears, **Then** the console explains the affected resource, stable
   reason, confidence or proof state, and next safe action.
5. **Given** the operator needs technical detail, **When** they open the
   selected row, **Then** the console progressively reveals timestamps,
   identifiers, evidence, and raw structured fields without making those
   details the default dashboard.

---

### User Story 2 - Investigate What A Workload Did (Priority: P1)

An operator can inspect the initial command and every attributable descendant,
then correlate process execution with file and network activity over time. The
view answers who performed an action, what was affected, how often it happened,
when it happened, whether it succeeded, and how confidently Hideout knows the
relationship.

**Why this priority**: A top-level command name is insufficient for an agentic
tool that launches shells, package managers, scripts, language runtimes, and
other helpers. Explainable activity is the basis for detecting surprising or
dangerous behavior.

**Independent Test**: Run a deterministic workload whose descendants execute
commands, read and mutate files, query names, connect to addresses, exit, and
spawn a re-parented descendant. Verify the process tree, file aggregation,
network timeline, attribution confidence, filters, and coverage disclosure.

**Acceptance Scenarios**:

1. **Given** a target command launches descendants, **When** the operator opens
   command activity, **Then** every observed execution is attached to a stable
   execution identity and parent relationship that is not confused by PID
   reuse or re-parenting.
2. **Given** a process opens, reads, writes, creates, truncates, renames,
   deletes, or changes metadata on files within the provider's advertised
   retained scope, **When** file activity is viewed, **Then** the operator sees
   actor, path, path class, operation, count, byte count when known, first and
   last time, and outcome without file contents; any deliberate relevance
   filter is disclosed by Partial coverage evidence.
3. **Given** a process performs many small reads or writes, **When** the
   activity is rendered, **Then** repetitive operations are aggregated without
   hiding destructive operations or fabricating precision.
4. **Given** a process performs DNS and connection activity, **When** network
   activity is viewed, **Then** the operator sees the process, queried domain
   when observed, destination IP, port, protocol, result, route, and an
   `exact`, `inferred`, or `unknown` attribution label.
5. **Given** a domain shares an address with other domains or the workload uses
   encrypted name resolution or a literal IP, **When** Hideout cannot prove a
   domain-to-connection relationship, **Then** it shows the IP and uncertainty
   rather than asserting a domain.
6. **Given** an unrelated guest or system process performs activity, **When**
   the selected workload is inspected, **Then** that activity is excluded or
   explicitly shown as a mediated system action rather than attributed to the
   workload.
7. **Given** an explainable risk rule matches observed facts, **When** the
   finding is shown, **Then** it names the behavior, source facts, severity,
   reason, and next action without declaring the entire command malicious or
   implying that observation blocked it.

---

### User Story 3 - Change Configuration Safely From The Console (Priority: P1)

An operator can inspect and edit common profile, connection, environment, and
capability settings from the terminal console. Editing creates a private draft;
the operator reviews one authoritative difference and its live, next-attach,
restart, or recreate effect before applying it. The same operation has
identical meaning from CLI, terminal, and browser surfaces.

**Why this priority**: A useful control console must support common changes,
but a convenient form must not become a raw profile writer or a second
authority path.

**Independent Test**: Edit a network setting, update a proxy secret, change a
profile environment rule, and request an environment lifecycle action from the
terminal console. Concurrently mutate the same profile from another client and
exercise disconnect, retry, validation failure, rollback, and stale-plan
rejection.

**Acceptance Scenarios**:

1. **Given** a visible editable setting, **When** the operator presses Enter
   and changes fields, **Then** only a local draft changes until an
   authoritative plan has been generated and confirmed.
2. **Given** a valid draft, **When** the operator requests review, **Then** the
   canonical before/after difference, affected layer, expected effect,
   blockers, policy decisions, and rollback behavior are shown.
3. **Given** another client changed the profile after review, **When** the old
   plan is applied, **Then** it is rejected as stale without overwriting the
   newer value, and the operator is prompted to review a fresh plan.
4. **Given** an apply response is lost, **When** the same operation is retried,
   **Then** Hideout returns or resumes the original outcome without applying
   the mutation twice.
5. **Given** a new proxy value or secret generation, **When** it validates,
   **Then** the resident control plane stages and applies it without requiring
   a daemon restart or exposing the value in the profile or interface.
6. **Given** proxy validation or activation fails, **When** the transition
   completes, **Then** existing effective traffic is preserved or restored and
   the failed desired state is not reported as effective.
7. **Given** a direct-to-proxy or proxy-to-direct posture change has active
   sessions, **When** the operator applies it, **Then** Hideout shows the
   blocking sessions and waits for safe eligibility rather than killing them
   or stopping the daemon by default.
8. **Given** existing connections accepted before an online route change,
   **When** a new route becomes effective, **Then** the UI explains that
   existing connections retain their prior route and new eligible connections
   use the new route.
9. **Given** the console's authoritative event stream is stale or disconnected,
   **When** the operator opens an editable control, **Then** mutation is
   disabled until a fresh authoritative snapshot is established.

---

### User Story 4 - Find The Supported Operation Quickly (Priority: P1)

A first-time operator can discover setup, running a command, connecting
through a proxy, opening the terminal console, checking readiness, stopping or
cleaning an environment, updating, uninstalling, and reporting a problem from
task-oriented help. Experienced operators can still reach the complete
advanced inventory without crowding the default path.

**Why this priority**: Powerful controls and evidence have little value when
the user cannot discover the supported sequence or understand an error such as
a missing secret reference.

**Independent Test**: Starting with no documentation open, use only top-level
and contextual help to complete the supported first run, configure a named
proxy secret, open both consoles, understand a pending next-attach change,
inspect activity, and find stop, cleanup, update, uninstall, and support paths.

**Acceptance Scenarios**:

1. **Given** a new user runs Hideout with no arguments or requests help,
   **When** primary help renders, **Then** it presents the supported journey,
   common tasks, copyable examples, and one clearly named advanced index.
2. **Given** the user requests help for a command, **When** contextual help
   renders, **Then** it explains purpose, syntax, common examples, effect,
   safety notes, and likely next commands without requiring an expanded dump.
3. **Given** a named secret reference is unavailable, **When** a run or
   connection plan fails, **Then** the error distinguishes the reference from
   its value and gives a safe, copyable command for setting or inspecting
   availability.
4. **Given** a desired connection differs from the effective connection,
   **When** status or help is shown, **Then** it identifies the current route,
   requested route, eligibility condition, existing-session behavior, and
   whether a restart is unnecessary.
5. **Given** an experienced user asks for all commands, **When** the expanded
   index is requested, **Then** every supported advanced and laboratory
   surface remains discoverable and is grouped by purpose and stability.

---

### User Story 5 - Trust Coverage, Privacy, And Retention (Priority: P1)

The operator can tell whether process, file, network, and DNS observation is
available, partial, or unavailable. Local evidence remains private to the
operator, contains no file contents or captured terminal input, and disappears
with the exact environment incarnation unless the operator explicitly exports
it through the review boundary.

**Why this priority**: A silent observation gap is more dangerous than an
honest absence of coverage, and high-volume activity data must not create a new
indefinite secret archive.

**Independent Test**: Run supported, degraded, collector-loss, encrypted-DNS,
capacity-limit, daemon-restart, environment-recreate, and explicit-export
scenarios with injected credential fixtures. Verify coverage transitions,
storage ownership and permissions, retention, deletion, local visibility, and
export redaction.

**Acceptance Scenarios**:

1. **Given** each observation domain, **When** its state is rendered, **Then**
   it is `Available`, `Partial`, or `Unavailable` with a reason, start time,
   dropped-event count, and retention gap when applicable.
2. **Given** a collector drops events or restarts, **When** the loss is
   detected, **Then** coverage becomes partial and the console never renders
   the affected interval as zero activity.
3. **Given** a reusable environment supports several sessions, **When**
   activity is retained, **Then** it is scoped to the exact environment
   incarnation and each originating session.
4. **Given** the environment is cleaned, deleted, or recreated, **When**
   lifecycle cleanup is proved complete, **Then** the prior incarnation's
   workload activity store is removed with no target-controlled residue.
5. **Given** the configured storage bound is reached, **When** old segments
   are removed, **Then** the bound is respected and affected historical
   coverage becomes explicitly partial.
6. **Given** local activity contains guest or workspace paths, **When** the
   authenticated local operator views it, **Then** complete local paths remain
   visible; target processes and unauthenticated clients cannot read the store.
7. **Given** activity contains managed secret values, URI credentials,
   authentication fields, or declared sensitive arguments and query
   parameters, **When** it is persisted or rendered in the workload activity
   surface, **Then** those values are deterministically removed.
8. **Given** arbitrary user data that cannot be classified reliably, **When**
   local activity is shown, **Then** the product states that unknown
   application secrets cannot be guaranteed redacted and requires explicit
   review before export.
9. **Given** an export or share request, **When** it is reviewed, **Then**
   control-plane secrets and selected user data are redacted, host-specific
   paths follow export policy, and no artifact is released without the
   existing authority and evidence checks.

---

### User Story 6 - Investigate Deep History In The Browser (Priority: P2)

An operator who needs more space and richer filtering opens the local browser
console to investigate sessions, process trees, paths, risks, domains,
connections, configuration transitions, decisions, and lifecycle history. The
browser provides greater depth without introducing different state or
authority from the terminal console.

**Why this priority**: A browser is better suited to long timelines, compound
filters, comparisons, and configuration review, while the terminal remains the
fast operational surface.

**Independent Test**: Produce several sessions with overlapping commands,
paths, domains, risks, coverage gaps, and configuration operations. Use the
browser to filter and correlate them, then perform one supported mutation and
compare its plan, result, audit, and effective state with CLI and terminal
views.

**Acceptance Scenarios**:

1. **Given** retained activity for several sessions, **When** the operator
   filters by session, process, time, operation, path, domain, IP, outcome,
   risk, or coverage, **Then** the results remain correctly scoped and link
   back to their authoritative session and environment.
2. **Given** the same selected session in terminal and browser consoles,
   **When** both are fresh, **Then** desired, effective, transition, evidence,
   risk, and coverage states agree.
3. **Given** the browser performs a supported mutation, **When** it is
   reviewed and applied, **Then** it uses the same canonical plan, stale-state
   rejection, operation identity, policy, audit, and rollback semantics as the
   CLI.
4. **Given** browser credentials expire or the event stream develops a gap,
   **When** the operator continues viewing, **Then** the interface becomes
   visibly stale and read-only until reauthenticated and reseeded.

---

### User Story 7 - Recover Without False Success (Priority: P1)

An operator can recover from daemon, observer, backend, client, or network
failure without losing track of whether a change, stop, cleanup, or deletion
actually happened. Completed operations are proved, incomplete operations are
resumable or rolled back, and unknown state is never shown as success.

**Why this priority**: A management console increases risk if it turns command
acceptance, a single observation, or a disconnected response into a green
success state.

**Independent Test**: Inject failures before and after plan acceptance,
configuration persistence, network activation, stop request, backend absence,
metadata cleanup, event publication, and client response. Restart the daemon
and retry using the original operation identity.

**Acceptance Scenarios**:

1. **Given** a daemon crash after a mutation was accepted, **When** the daemon
   restarts, **Then** it reconstructs an authoritative terminal, resumable, or
   rollback state without inferring success from the client request.
2. **Given** a stop request returned but stable terminal evidence is not yet
   available, **When** status is rendered, **Then** it remains stopping or
   unproved rather than stopped.
3. **Given** cleanup or deletion was authorized before a crash, **When** the
   operation is retried, **Then** it resumes the durable intent and performs no
   duplicate destructive provider call.
4. **Given** a client closes a claimed decision dialog or disconnects, **When**
   another authenticated client attempts to act, **Then** the original claim
   is released or expires within its visible lease and cannot retain
   unbounded authority.
5. **Given** an attach is establishing or reconciliation owns the affected
   resource, **When** a conflicting mutation is requested, **Then** the
   console shows the owner and blocker and does not damage either operation.

---

### User Story 8 - Produce One Verified Release Candidate (Priority: P1)

A release operator can build one clean candidate and prove that its help,
console interactions, configuration authority, online proxy changes,
observation scope, privacy behavior, recovery, performance, packaging, and
real isolation match the product claims.

**Why this priority**: Source-level tests and simulated activity do not prove
the behavior users receive from the packaged product or from a real isolated
backend.

**Independent Test**: Build an exact clean package candidate once, install it
on a supported clean machine, run the complete ordinary-user and adversarial
workflows against the real isolated backend, verify mutation proofs and
negative fixtures, uninstall or upgrade it, and bind all results to the exact
candidate identity.

**Acceptance Scenarios**:

1. **Given** an assertion for authority, attribution, redaction, recovery,
   retention, help, or UI state, **When** its guarded implementation is
   deliberately broken in a test fixture, **Then** the associated judge fails
   before the candidate is accepted.
2. **Given** a candidate with a missing helper, collector capability, model
   result, real-backend proof, privacy proof, UI journey, performance budget,
   or redaction result, **When** readiness is evaluated, **Then** publication
   is blocked and no weaker result substitutes for it.
3. **Given** all required evidence passes, **When** the candidate is inspected,
   **Then** documentation, help, support boundaries, package contents, binary
   identity, and evidence describe the same version and behavior.
4. **Given** the goal completes without separate publication authorization,
   **When** release readiness is reported, **Then** a verified candidate and
   evidence are produced but no remote tag, release, or package-manager
   publication is created.

### Edge Cases

- A target exits before the observer becomes ready, or an observer becomes
  ready after the target has already created descendants.
- A child double-forks, changes process group, is re-parented, exits quickly,
  or reuses a previously observed PID.
- A target attempts to move outside its workload boundary or gains guest-root
  authority that weakens observation.
- A process performs work through a shared resolver, proxy, broker, filesystem
  portal, or another daemon that is not itself part of the workload.
- A file is accessed through a hard link, symlink, rename, deleted-open handle,
  memory mapping, generated path, or mount alias.
- A path cannot be resolved, exceeds event limits, contains invalid encoding,
  or changes between observation and display.
- A high-volume compiler, package install, dependency scan, or repository walk
  creates more events than can be rendered or retained individually.
- A DNS answer is cached, shared, stale, returns several addresses, is bypassed
  by a literal IP, or is hidden inside encrypted application traffic.
- Several domains share one destination address, or a connection is made
  through a tunnel whose final destination is not observable.
- A secret appears in URI userinfo, a recognized authentication field, a
  split command argument, a query parameter, percent-encoding, Unicode, or a
  value known to the managed secret store.
- Arbitrary application content resembles a credential but is not covered by
  a deterministic redaction schema.
- The client misses an event, receives a duplicate event, reconnects after
  compaction, or observes a schema version it does not understand.
- CLI, terminal, and browser clients plan or apply conflicting changes to the
  same profile or different layers at the same time.
- The daemon crashes after persisting desired configuration but before live
  activation, or after activation but before returning the response.
- A posture transition waits while new attaches are attempted.
- The environment is deleted while activity segments are open, while an
  export is being reviewed, or after deletion evidence but before metadata
  cleanup.
- A custom audit destination exists outside the managed environment activity
  store and therefore follows a distinct retention contract.
- Native or another weak backend cannot provide the supported workload
  observation boundary.
- The terminal is narrow, lacks color, uses a screen reader, disconnects, or
  receives input while a modal or stale-state warning is active.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Profile and network configuration, secret reference
  lifecycle, environment stop/clean, session attach and observation, decision
  claims, UI/daemon event delivery, local evidence storage, export/share, and
  release evidence. Workload observation itself is evidence-only and grants no
  new host, guest, filesystem, network, or execution authority.
- **Fail-closed behavior**: Unsupported, stale, ambiguous, unauthenticated, or
  policy-denied mutations do not apply. Missing secret material does not fall
  back to direct networking. Unknown route, stop, cleanup, ownership, or
  operation state is not reported as effective or complete. Observation loss
  does not silently terminate a user's workload by default because observation
  is not enforcement; instead its affected domain immediately becomes partial
  or unavailable and cannot support a stronger claim.
- **User authority and policy**: The authenticated local operator authors
  drafts and confirms canonical Manager plans. Existing deny precedence,
  capability review, claim leases, HostFS rules, network policy, and lifecycle
  ownership remain authoritative. Target processes may generate observable
  activity but cannot alter configuration, classify their own coverage, erase
  retained evidence, approve decisions, or broaden authority.
- **Generality and provider scope**: The workload model applies to arbitrary
  developer tools and agentic CLIs. A named agent, proxy, shell, editor,
  package manager, browser, workload, or backend may be an example or gate
  fixture but does not become Core product semantics.
- **Evidence surface**: Task-oriented help, explain, doctor, structured local
  audit, workload activity, Boundary Summary, Manager snapshot and event
  stream, terminal and browser consoles, operation history, coverage records,
  performance reports, mutation proofs, negative fixtures, real-backend gates,
  and release-candidate evidence derive from authoritative facts.
- **Secret/redaction boundary**: The workload activity store is a separate
  presentation-safe evidence domain and never records environment values,
  terminal input, complete terminal output, or file contents. It
  deterministically strips managed values and declared credential schemas
  before persistence. Existing full-fidelity local audit remains a distinct
  host-local evidentiary contract; anything leaving the local trust boundary
  passes through export/share review and redaction. Control-plane credentials
  never enter either public surface.
- **Backend/gate expectation**: Static contracts, model checks, local
  authority, UI behavior, redaction, lifecycle, mutation proofs, and negative
  fixtures require the local gate. Workload-boundary, process, file, network,
  cleanup, and performance claims require a real supported isolated backend.
  Privacy-route claims additionally require the real proxy and mediated-DNS
  gate. The exact packaged candidate must pass terminal and browser journeys,
  ordinary-user installation, clean upgrade/uninstall, and all applicable
  release-candidate gates.

## Requirements *(mandatory)*

### Functional Requirements

#### Operator Experience And Shared State

- **FR-001**: The system MUST provide task-oriented primary help for setup,
  run, connect, status, terminal console, browser console, stop, clean, update,
  uninstall, and support reporting.
- **FR-002**: Primary help MUST provide copyable common examples and one
  discoverable expanded index without displaying the full advanced and
  laboratory inventory by default.
- **FR-003**: Contextual help MUST state each operation's effect, prerequisites,
  safety boundary, common recovery, and likely next action.
- **FR-004**: The terminal console MUST provide a persistent, keyboard-driven
  operational dashboard with visible focus, selection, navigation help, and
  progressive detail.
- **FR-005**: The terminal dashboard MUST prioritize active workload state,
  effective connection, observation coverage, meaningful recent activity,
  explainable risks, blockers, and recommended actions over implementation
  counters.
- **FR-006**: The browser console MUST provide deeper history, correlation,
  filtering, and configuration views while retaining the same state and
  authority semantics as CLI and terminal clients.
- **FR-007**: All clients MUST distinguish desired configuration, effective
  configuration, transition state, and completion evidence.
- **FR-008**: All clients MUST render stale, disconnected, blocked, failed,
  rolling-back, unproved, and terminal states distinctly.
- **FR-009**: Every state-changing control exposed in a console MUST have the
  same canonical operation and audit semantics as its supported CLI path.

#### Configuration, Secret, And Network Transactions

- **FR-010**: Editing in a console MUST create a client-local draft with no
  authority or server-side side effect.
- **FR-011**: A draft MUST be converted into a canonical review containing
  before and after values, affected configuration layer, effect category,
  policy outcome, blockers, validation, and rollback expectations before
  confirmation.
- **FR-012**: Every configuration plan MUST bind to the exact profile revision
  and relevant authoritative state from which it was produced.
- **FR-013**: Applying a stale plan MUST fail without changing desired or
  effective state.
- **FR-014**: Every accepted mutation MUST have a stable operation identity
  whose retry cannot apply its side effects twice.
- **FR-015**: At most one conflicting mutation MAY own a profile transition at
  a time; non-conflicting behavior MUST be explicitly defined rather than
  inferred independently by clients.
- **FR-016**: Configuration status MUST identify whether a change is live,
  effective on the next eligible attach, requires a safe service or boot
  transition, or requires environment recreation.
- **FR-017**: Existing session configuration snapshots MUST remain immutable;
  the interface MUST identify which sessions retain an older snapshot.
- **FR-018**: The system MUST provide an operator-owned secure secret lifecycle
  that stores references in profiles, keeps values out of target environments
  and routine evidence, and makes a new validated generation available to the
  resident control plane without restart.
- **FR-019**: Creating, updating, inspecting availability, rotating, and
  deleting a secret reference MUST use typed authenticated operations and
  MUST NOT echo the secret value.
- **FR-020**: A network change MUST follow an observable stage, activate,
  commit or rollback transition; staging MUST NOT be reported as changed
  traffic.
- **FR-021**: A direct-to-proxy or proxy-to-direct transition MUST wait for its
  safe eligibility condition without stopping the daemon or forcibly ending
  active sessions by default.
- **FR-022**: Existing accepted connections MUST retain their bound route, and
  new eligible connections MUST use the newly committed route.
- **FR-023**: Failure to validate, stage, activate, or prove a network change
  MUST preserve or restore the prior effective route and expose a recovery
  action.
- **FR-024**: A stale or disconnected client MUST be read-only until it has
  reseeded all authoritative resources needed by the requested mutation.

#### Workload Scope And Process Observation

- **FR-025**: Every supported isolated run MUST have one authoritative workload
  scope containing the initial target and all descendants, including
  descendants that re-parent or change process group.
- **FR-026**: Workload membership MUST remain unambiguous across PID reuse,
  fast process exit, concurrent sessions, and environment reuse.
- **FR-027**: Unrelated guest and system processes MUST NOT be attributed to a
  workload solely because they share an environment.
- **FR-028**: Actions performed through a shared mediator MUST identify the
  mediator and original workload actor when provable, and otherwise identify
  attribution as unknown.
- **FR-029**: Each observed execution MUST contain a stable execution identity,
  session, environment incarnation, parent execution identity when known,
  executable, redacted argument vector, working directory, guest identity,
  start time, end time, exit result, and duration when known.
- **FR-030**: Process observation MUST NOT persist environment values, terminal
  input, or complete terminal output.
- **FR-031**: Process exit, observer loss, or daemon loss MUST NOT cause a
  still-running descendant to be silently reassigned to another session.

#### File, Network, And Risk Observation

- **FR-032**: Supported file observation MUST distinguish open, read, write,
  create, truncate, rename, unlink, directory creation/removal, and metadata
  changes.
- **FR-033**: File activity MUST identify the workload actor, best authoritative
  path, file identity when available, path class, operation, call count, byte
  count when known, first and last time, and outcome.
- **FR-034**: File activity MUST NOT retain file contents.
- **FR-035**: Repetitive file operations MUST be aggregated within documented
  bounds while preserving destructive operations and the ability to explain
  counts and time ranges.
- **FR-035a**: A provider MAY filter non-mutating system-runtime reads before
  transport to keep the operator view and bounded relay useful, but it MUST
  retain every observed mutation, retain user/security-relevant paths, and
  disclose the exact filter as `Partial` coverage evidence.
- **FR-036**: Unresolvable, aliased, truncated, or ambiguous paths MUST be
  labeled rather than replaced with fabricated canonical paths.
- **FR-037**: Supported network observation MUST identify workload actor,
  destination IP, port, protocol, connection result, first and last time,
  count, and effective route when known.
- **FR-038**: Supported name-resolution observation MUST identify workload
  actor, queried name, answer addresses when visible, outcome, and time.
- **FR-039**: Domain-to-connection attribution MUST be labeled `exact`,
  `inferred`, or `unknown`, and no inferred association may be rendered as an
  observed fact.
- **FR-040**: Encrypted name resolution, literal IPs, shared addresses,
  tunnels, proxy mediation, cache reuse, and missing correlation MUST reduce
  attribution confidence rather than create a false domain history.
- **FR-041**: Risk findings MUST be produced from named, versioned,
  explainable rules and include supporting facts, severity, reason, time, and
  next action.
- **FR-042**: Risk findings MUST distinguish observed behavior from policy
  allow, policy deny, and actual prevention; an unexplained aggregate
  harmfulness score MUST NOT be the primary decision.

#### Coverage, Delivery, Retention, And Access

- **FR-043**: Process, file, network, and name-resolution observation MUST each
  expose `Available`, `Partial`, or `Unavailable` coverage independently.
- **FR-044**: Coverage MUST include a stable reason, the affected interval,
  collector generation, dropped-event count, and retention gap when
  applicable.
- **FR-045**: Event loss, unsupported capabilities, collector restart,
  untrusted workload escape, storage truncation, schema mismatch, and stream
  gap MUST downgrade the affected coverage.
- **FR-046**: A partial or unavailable interval MUST NOT be presented as an
  interval with zero activity.
- **FR-047**: Activity events MUST have monotonic ordering within their
  authoritative stream, and duplicates, reconnects, and gaps MUST be handled
  without double-counting or silent omission.
- **FR-048**: The console MUST seed from an authoritative snapshot and consume
  authoritative events while healthy; after an unfillable gap it MUST reseed
  before resuming fresh status.
- **FR-049**: Reusable-environment activity MUST be retained under the exact
  environment incarnation and separated by originating session.
- **FR-050**: Activity for a non-reusable run MUST be removed with its
  session-owned lifecycle unless it has entered an explicitly reviewed export.
- **FR-051**: Cleaning, deleting, or recreating an environment MUST remove the
  exact prior incarnation's activity after destructive lifecycle evidence is
  complete.
- **FR-052**: Activity storage MUST be bounded by an operator-visible limit;
  pruning MUST be ordered, observable, and reflected as partial historical
  coverage.
- **FR-053**: The activity store MUST be host-private, writable only by its
  trusted owner, and unavailable to target workloads.
- **FR-054**: Only the authenticated local operator MAY view unexported
  activity through CLI, terminal, or loopback browser surfaces.

#### Privacy, Redaction, And Export

- **FR-055**: Local authenticated activity views MUST preserve full guest and
  workspace paths unless the operator configures a stricter presentation
  policy.
- **FR-056**: Workload activity MUST deterministically remove managed secret
  values, URI userinfo, authentication fields, declared sensitive argument
  values, declared sensitive query parameters, and control-plane material
  before persistence.
- **FR-057**: Redaction MUST handle supported encoding and split-field forms
  without claiming to discover arbitrary application secrets.
- **FR-058**: The product MUST visibly disclose that unclassified local argv,
  paths, domains, and application values may contain user data.
- **FR-059**: Existing full-fidelity host-local audit and the new bounded
  workload activity store MUST have distinct documented retention, fidelity,
  and redaction contracts.
- **FR-060**: Export and share MUST use the existing reviewed authority path,
  deterministic control-plane stripping, selected user-data redaction,
  bounded artifacts, and fail-closed behavior.
- **FR-061**: Host-specific absolute paths MUST follow export policy even when
  they remain visible in the authenticated local console.

#### Recovery, Proof, Performance, And Release

- **FR-062**: Accepted configuration, stop, clean, delete, decision, and
  observation operations MUST have queryable state after client or daemon
  restart.
- **FR-063**: A client response, backend command return, single terminal
  sample, or UI timer MUST NOT independently prove an operation complete.
- **FR-064**: Decision claims MUST have visible bounded leases and MUST release
  or expire when the deciding client closes or disconnects.
- **FR-065**: Conflicting attach, reconcile, configuration, stop, and cleanup
  operations MUST expose their owner and blocker and preserve existing
  lifecycle exclusion rules.
- **FR-066**: Safety and liveness contracts MUST cover stale plans, concurrent
  clients, lost responses, duplicate retries, observer loss, event gaps,
  daemon crashes, network rollback, stable stop proof, and lifecycle-bound
  deletion.
- **FR-067**: Every new assertion MUST have a demonstrated mutation proof, and
  every new judge MUST have a negative fixture.
- **FR-068**: Supported reference workloads MUST meet documented attach,
  runtime overhead, memory, storage, event-loss, and UI freshness budgets on
  the real supported backend.
- **FR-069**: Documentation and product status MUST identify supported
  coverage, unavoidable attribution uncertainty, guest-root limitations,
  observation-versus-enforcement boundaries, retention, redaction, recovery,
  and backend requirements.
- **FR-070**: Release readiness MUST require all applicable local,
  real-backend, privacy, terminal, browser, packaging, clean-install,
  upgrade/uninstall, dependency, advisory, redaction, and performance evidence
  for one exact clean candidate.
- **FR-071**: Producing a verified candidate MUST NOT publish a remote tag,
  release, or package-manager update without separate explicit operator
  authorization.

### Key Entities

- **Workload Scope**: The authoritative membership boundary for one run,
  linked to its session and exact environment incarnation, with start, end,
  collector generation, and escape or degradation state.
- **Process Execution**: One executable incarnation with stable identity,
  parent relationship, command metadata, lifecycle, and workload membership.
- **File Activity**: A bounded aggregation of file metadata operations by
  process, file identity or path, operation, time window, counts, bytes, and
  outcome.
- **Network Activity**: A bounded aggregation of connection facts by process,
  destination, route, time window, count, bytes when known, and outcome.
- **Name Resolution Activity**: An observed query and answer set linked to a
  process and optionally correlated with network activity at an explicit
  confidence level.
- **Coverage State**: Per-domain availability, reason, interval, generation,
  dropped-event count, retention gap, and last authoritative evidence.
- **Risk Finding**: A versioned explainable rule result linked to immutable
  source facts, severity, reason, time, and recommended action.
- **Configuration Draft**: Client-local proposed values carrying no authority.
- **Configuration Plan**: Canonical reviewed difference bound to exact
  revisions, policy decisions, effect category, blockers, and expiry.
- **Operation**: Idempotent authoritative mutation or lifecycle transition
  with stable identity, owner, phase, evidence, result, and recovery state.
- **Secret Reference And Generation**: A non-secret profile identifier linked
  to operator-owned secret availability and rotation generation without
  exposing its value.
- **Activity Segment**: Host-private bounded retained observations belonging
  to one environment incarnation and time interval, with integrity,
  compaction, and deletion state.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A first-time operator can discover and complete setup, first run,
  connection configuration, terminal-console launch, readiness, stop/clean,
  update/uninstall, and support-report paths using no more than two help
  invocations per task.
- **SC-002**: From the terminal dashboard, an operator can identify the active
  command, effective connection, coverage, highest-priority risk or blocker,
  and next action within one screen, and can open selected activity detail in
  no more than three keyboard actions.
- **SC-003**: While the local event stream is healthy, 95% of authoritative
  state and activity changes become visible in terminal and browser consoles
  within two seconds.
- **SC-004**: Across bounded concurrent CLI, terminal, and browser mutation
  scenarios, 100% of stale plans are rejected, no reviewed value is replaced
  by an unreviewed value, and no operation retry applies side effects twice.
- **SC-005**: In supported full-coverage reference workloads, 100% of expected
  process executions, destructive file operations, test DNS queries, and test
  connections are attributed to the correct session; any deliberate loss or
  unsupported observation prevents an `Available` claim.
- **SC-006**: Zero unrelated-session events are attributed to the selected
  workload in concurrent and PID-reuse reference scenarios.
- **SC-007**: Every injected collector drop, event gap, unsupported capability,
  schema mismatch, and retention truncation changes the affected coverage to
  partial or unavailable before the UI can present the affected interval as
  complete.
- **SC-008**: Credential fixtures covering managed values, URI userinfo,
  authentication fields, sensitive flags, sensitive query parameters, and
  control-plane tokens produce zero raw matches in persisted workload
  activity, console output, exported artifacts, and release evidence.
- **SC-009**: Environment clean, delete, and recreate tests leave zero activity
  segments for the prior exact incarnation after lifecycle completion, while
  never deleting another incarnation's evidence.
- **SC-010**: Default activity retention never exceeds its configured storage
  bound by more than one active bounded segment, and every historical deletion
  is reflected in coverage and operation evidence.
- **SC-011**: The supported developer reference workload completes with no
  more than 10% median elapsed-time overhead from enabled default observation,
  no unreported event loss, and no regression beyond existing attach and
  interactive freshness budgets.
- **SC-012**: All defined bounded safety and progress scenarios complete
  without a counterexample, including stale-client, crash, retry, rollback,
  observation-loss, and cleanup traces.
- **SC-013**: Updating a proxy secret and eligible route completes without
  daemon restart or VM recreation in 100% of healthy transition tests; blocked
  posture changes identify every active blocking session.
- **SC-014**: Terminal, browser, and CLI parity tests produce identical
  desired, effective, transition, blocker, coverage, risk source, and terminal
  operation states for the same authoritative scenario.
- **SC-015**: One exact clean package candidate passes every required local,
  real-backend, privacy, terminal, browser, packaging, upgrade/uninstall,
  redaction, dependency, advisory, performance, mutation-proof, and negative
  fixture gate with no required result failed, stale, reduced, or `not-run`.

## Assumptions

- Hideout remains a local single-operator product. Delegated roles, remote
  management, cloud retention, organization policy, and multi-tenant evidence
  access are outside this feature.
- A supported Linux guest backend is required for full workload observation.
  Native and unsupported backends may expose partial or unavailable coverage
  but cannot establish the same isolation or attribution claim.
- Workload observation is detective evidence, not preventive enforcement.
  Risk findings do not retroactively stop a file or network operation. Future
  mandatory blocking requires a separate authority and policy design.
- The target normally runs without guest-root authority. If it gains guest
  root or can tamper with the observation boundary, the affected coverage and
  security claim degrade explicitly.
- Activity performed through a shared external daemon cannot always be
  attributed to the initiating process. Such events remain mediated or
  unknown rather than being guessed.
- Local authenticated paths are operator data and remain visible by default.
  Public or shared artifacts follow stricter path-redaction policy.
- The bounded workload activity store is distinct from existing full-fidelity
  local audit. Existing custom audit destinations are not implicitly deleted
  with an environment.
- The environment incarnation, not a display name or reusable slot, is the
  retention and deletion identity. Non-reusable runs use session lifecycle.
- File contents, environment values, terminal input, complete terminal output,
  packet payloads, and arbitrary screen recording are outside the observation
  scope.
- Default storage and aggregation bounds will be selected from measured real
  workloads during planning and must satisfy the measurable outcomes above.
- Named agent CLIs, proxies, package managers, shells, and development tools
  are compatibility and test fixtures, not privileged product identities.
- Remote publication remains a separate explicitly authorized action after
  release-candidate evidence is complete.
