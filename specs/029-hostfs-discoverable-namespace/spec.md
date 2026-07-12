# Feature Specification: HostFS Discoverable Namespace

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `029-hostfs-discoverable-namespace`

**Created**: 2026-07-10

**Status**: Implemented

**Input**: User description: "Let isolated CLIs and coding agents navigate explicitly selected host paths without receiving file content or mutation authority. Add per-root HostFS visibility rules, immediate permission denial plus asynchronous exact-file read approval, honest three-state path semantics, bounded metadata disclosure, and real Gate 2 proof, based on the accepted `.tmp/029-hostfs-discoverable-namespace-draft.md` review baseline."

## Clarifications

### Session 2026-07-10

- Q: Is visibility a global mode or ordinary policy? -> A: It is a per-root HostFS capability. `none`, `landmarks`, and `home-tree` are convenience presets that expand into explicit rules.
- Q: Does a denied read wait for an operator? -> A: No. V1 returns immediate `EACCES`, creates or coalesces an asynchronous decision when eligible, and requires the target to retry.
- Q: What may a read approval grant? -> A: One exact canonical file for the current live session. Broader or persistent access remains an explicit profile plan/apply operation.
- Q: How are path visibility selectors expressed? -> A: `see:` exposes one node, `see-dir:` exposes one directory level, and `see-tree:` exposes a lazily traversed tree. V1 rejects glob forms.
- Q: What happens to legacy `list:` rules? -> A: They are rejected with guided migration because aliasing them to `see-dir:` would silently change both name breadth and metadata disclosure.
- Q: When do the new `EACCES` distinctions apply? -> A: Only within an explicit discover-rule domain. Paths visible only because of legacy stat/read/directory/overlay behavior keep their existing unauthorized-operation collapse.
- Q: How quickly does approval become usable? -> A: Content authority is recognized on the next retry in the same running session; real stat metadata converges within one second and never controls authorization.
- Q: Who owns decision state? -> A: The typed Manager/Core provider owns deduplication, terminal state, limits, and reopen. The HostFS broker only proposes and maps the validated result.
- Q: How are broad visibility and macOS protected directories handled? -> A: Broad name visibility requires explicit acknowledgement, access remains lazy, and host prerequisite/TCC failures are reported as non-approvable I/O failures.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Navigate Visible Host Paths Without Reading (Priority: P1)

An operator allows selected host paths to be visible to an isolated CLI or
coding agent. The target can navigate names and coarse node kinds much as it
would on the host, but it cannot read content, inspect full host metadata,
follow symlink targets, or mutate files without separate authority.

**Why this priority**: Path discovery is the usability gap that makes an
otherwise isolated CLI feel unlike a local CLI. It must be useful before any
approval workflow is added and must not broaden read or write authority.

**Independent Test**: Run a real isolated target against fixture roots using
exact, one-level, and recursive visibility rules. Verify complete scoped names,
coarse metadata only, hidden sensitive roots, immediate denial of unauthorized
content, and unchanged behavior outside the visibility domain.

**Acceptance Scenarios**:

1. **Given** an exact `see:` rule for a file, **When** the target navigates its
   synthetic ancestors and looks up the file, **Then** the file name and coarse
   kind are visible while its content and full metadata remain unavailable.
2. **Given** a `see-dir:` rule, **When** the target lists that directory,
   **Then** every non-hidden immediate child is returned with coarse metadata or
   the listing fails as incomplete.
3. **Given** a `see-tree:` rule, **When** the target traverses a descendant
   directory, **Then** names are discovered lazily within the declared scope
   without pre-indexing unrelated paths.
4. **Given** a directory covered only by exact `see:`, **When** the target tries
   to enumerate it, **Then** enumeration returns `EACCES`, not an empty success,
   and no read decision is created.
5. **Given** a reserved, discover-denied, or outside-domain path with no
   separate applicable content grant, **When** the target looks it up, **Then**
   it remains target-visible as `ENOENT`.

---

### User Story 2 - Approve One Locked Read And Retry (Priority: P2)

When a target tries to read an approval-eligible visible file, the operation
fails immediately and the operator receives one bounded `hostfs.read` decision.
The operator can approve or deny it from an authenticated supported surface.
Approval lets the same running target retry that exact file for the remainder
of the session without changing the persistent profile.

**Why this priority**: Visibility becomes substantially more useful when the
operator can grant one requested read without recreating the environment or
granting an entire directory. The workflow must remain asynchronous so a human
approval delay cannot hang arbitrary target processes.

**Independent Test**: In a real running session, read one visible locked file,
observe one decision across operator surfaces, approve it from a separate local
control process, retry from the unchanged target, and verify exact-file access,
metadata convergence, timeout/deny behavior, and session cleanup.

**Acceptance Scenarios**:

1. **Given** a visible locked file with no explicit read deny or prior terminal
   outcome, **When** the target reads it, **Then** the read returns immediate
   `EACCES` and exactly one pending decision is created.
2. **Given** the same pending request is retried repeatedly, **When** the
   decision is unresolved, **Then** retries neither create duplicate decisions
   nor extend its timeout or claim lease.
3. **Given** an authenticated operator approves the exact-file request,
   **When** the same running target retries, **Then** content access succeeds on
   that retry and ordinary granted metadata is visible within one second.
4. **Given** an explicit read deny, terminal denial, timeout, exact-directory
   enumeration denial, or request-capacity refusal, **When** the target retries,
   **Then** no false pending approval is reported.
5. **Given** a terminal decision for a live session, **When** an authenticated
   operator explicitly reopens it, **Then** a new audited revision and deadline
   are created; target retries alone cannot reopen it.
6. **Given** an ended, orphaned, or unprovable session, **When** an operator
   attempts to reopen its decision, **Then** the operation fails closed and no
   grant is created.

---

### User Story 3 - Choose Visibility Without Overclaiming Privacy (Priority: P3)

An operator can retain full hiding, enable a small useful visibility preset, or
explicitly acknowledge broader name disclosure. The product explains that file
names are user data, preserves existing profiles unless new visibility rules
are added, distinguishes host prerequisite failures from approval requests, and
records honest evidence for the resulting posture.

**Why this priority**: A discoverable namespace intentionally changes an
existing existence-hiding claim. Onboarding, diagnostics, audit, and docs must
make the new disclosure domain explicit so usability does not become a silent
security regression.

**Independent Test**: Exercise no-visibility, confirmed landmarks, and broad
home-tree fixtures; compare a legacy profile with no `see*` rules; inject
protected-directory failure and control-plane secrets; and validate posture,
errors, audit, evidence, and docs claims.

**Acceptance Scenarios**:

1. **Given** an existing profile with no visibility rule, **When** it is loaded
   and used, **Then** it exposes no additional real directory entries and keeps
   its prior unauthorized-operation behavior.
2. **Given** interactive onboarding, **When** the operator selects and confirms
   `landmarks`, **Then** one-level rules are created only for the selected
   landmarks; cancellation creates no rule.
3. **Given** non-interactive onboarding with no visibility choice, **When** the
   profile is created, **Then** visibility defaults to `none`.
4. **Given** a broad home-tree request, **When** the operator has not explicitly
   acknowledged name disclosure, **Then** the request fails before rules are
   created.
5. **Given** a protected host directory cannot be accessed, **When** HostFS
   evaluates it, **Then** the result is a typed non-approvable I/O prerequisite
   failure rather than an approval-required `EACCES`.
6. **Given** exported evidence or docs claims, **When** they are validated,
   **Then** they state the explicit visibility domain, retain hidden-path
   non-claims, and contain no file content or control-plane secret.

### Edge Cases

- A directory changes while it is being enumerated. The system must return an
  incomplete-list error rather than silently omit entries and report success.
- A directory exceeds the documented per-directory entry limit. It must return
  `EOVERFLOW`; it must not truncate silently or insert a synthetic sentinel
  filename.
- Recursive traversal reaches its documented depth bound while deeper host
  entries exist. Traversal at that boundary must report an incomplete result;
  it must not make deeper entries look absent.
- A `see-dir:` child is itself a directory. The child is visible, but listing
  that child requires matching one-level or recursive visibility scope.
- A symlink is visible. Discovery may reveal only `kind=symlink`; it must not
  reveal or follow the target. Content access re-evaluates the canonical target.
- A symlink is retargeted after approval. The previous exact-path grant must not
  authorize the new target.
- A read request matches both discover allow and explicit read deny. The file is
  visible and read returns `EACCES`, but no decision is created.
- A visible file lacks write-overlay authority. Inside an explicit visibility
  domain, write returns unauthorized `EACCES` and does not create a read
  decision; grant-implied-only legacy visibility retains prior behavior.
- Decision rate or pending capacity is exhausted. The target receives a
  retryable capacity error without a decision reference; a bounded retry hint
  appears only when the system can state one honestly.
- A host prerequisite or protected-directory access fails. The target receives
  a non-approvable I/O failure, while diagnostics identify the host prerequisite
  without claiming the path is absent.
- An operator approves after the source session ends. The approval/reopen fails
  and no durable or profile grant is produced.
- Hidden predictable paths, such as common credential directories, may still be
  inferred from prior knowledge. Hiding prevents direct enumeration and access;
  it does not prove non-existence.

## Scope And Non-Goals

### In Scope

- Per-root exact, one-level, and lazy recursive visibility rules.
- Coarse name/kind visibility below read authority.
- Three-state target behavior for hidden, visible locked, and granted paths.
- Typed HostFS errors with stable retry and decision-reference semantics.
- Asynchronous exact-file read decisions and live session-scoped grants.
- Decision deduplication, bounded capacity, default-deny timeout, and
  operator-only reopen.
- Explicit visibility presets, sensitive-root classification, TCC-aware
  diagnostics, audit, Boundary Summary, docs truth, and proof registration.
- Gate 0 coverage plus real Gate 2 proof for guest-visible HostFS behavior.

### Out Of Scope

- Blocking a filesystem call while waiting for a human decision.
- Rich guest-side approval notifications or custom prose for arbitrary POSIX
  tools.
- Directory-scoped approval from a read decision; persistent broad reads remain
  explicit profile policy.
- Content previews in read decisions.
- Recursive indexing, search databases, or eager home scanning.
- Full host metadata, ownership, ACL, extended attribute, or symlink-target
  disclosure for locked nodes.
- A new HostFS execute capability or a claim that host binaries execute through
  HostFS.
- Broad home visibility or workspace-external HostFS grants enabled silently by
  the privacy template.
- Workspace interception, workspace DLP, or replacement of the direct workspace
  mount.
- Guest-root containment, TCC repair, silent TCC probing, or JavaScript-owned
  filesystem authority.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: HostFS visibility and read policy, profile/run rules,
  the HostFS broker, session lifecycle, Manager decisions, CLI/TUI/WebUI,
  daemon event visibility, doctor, audit, Boundary Summary, and evidence.
- **Fail-closed behavior**: Missing/ambiguous rules, reserved or force-hidden
  paths, explicit read deny, incomplete enumeration, malformed/stale session
  grants, exhausted decision capacity, ended sessions, host prerequisites,
  unknown typed errors, and unsupported backend behavior deny or return a
  typed error before content authority is granted.
- **User authority and policy**: Visibility and persistent read rules are
  explicit operator-authored policy. Deny and reserved-root rules win. A read
  decision can grant only one exact canonical file to one live session and
  cannot mutate the persistent profile.
- **Generality and provider scope**: The feature is generic HostFS behavior for
  untrusted CLIs and agents. It does not encode a named agent, editor, package
  manager, or backend quirk as product semantics. macOS protected-directory
  behavior is a host prerequisite, not Core policy.
- **Evidence surface**: Structured local audit, decision records, Manager
  status, live-console state, Boundary Summary, doctor output, onboarding
  posture, product-evidence registry, docs truth, Gate 0, and real Gate 2.
- **Secret/redaction boundary**: Target and public evidence must not receive
  capability/claim tokens, session-grant storage paths, file content, symlink
  targets, hidden control-plane paths, generated machine identity, or
  `HIDEOUT_SECRET_*` material. Host paths and target explanations are local user
  data and remain subject to export/share policy when leaving the machine.
- **Backend/gate expectation**: Local tests may prove policy, decision,
  redaction, and error-record behavior. Guest-visible namespace, errno, retry,
  metadata convergence, and symlink claims require real Lima Gate 2 evidence.
  Native/local-fast results cannot satisfy those claims.

## Requirements *(mandatory)*

### Functional Requirements

#### Visibility Policy And Namespace

- **FR-001**: The system MUST represent visibility as explicit per-root HostFS
  policy, not as a separate global authority mode.
- **FR-002**: `see:/path` MUST expose one exact node and only the synthetic
  ancestors required to reach it; exact directory visibility MUST NOT authorize
  enumeration.
- **FR-003**: `see-dir:/directory` MUST expose the directory and a complete set
  of non-hidden immediate children with coarse metadata.
- **FR-004**: `see-tree:/directory` MUST expose non-hidden descendant names
  lazily and MUST NOT require eager recursive indexing.
- **FR-005**: V1 visibility selectors MUST reject glob syntax. Legacy `list:`
  rules MUST fail with guided migration and MUST NOT be silently aliased to
  `see-dir:`.
- **FR-006**: Reserved control-plane roots MUST take precedence over every
  visibility and content grant. Explicit discover-deny rules MUST take
  precedence over discover allows for the same path, while existing explicit
  content-grant precedence remains operation-specific. A discover deny MUST
  suppress broad enumeration and read-decision creation, but MUST NOT revoke a
  separately applicable operator-authored exact content grant outside reserved
  roots or the exact lookup needed to use that grant.
- **FR-007**: Existing stat/read/directory/overlay rules MAY retain only the
  visibility already required for their operation, staged nodes, and synthetic
  ancestors; they MUST NOT expose additional real directory entries.
- **FR-008**: Locked discovery results MUST expose only name, coarse kind,
  locked state, and generic capability labels. They MUST omit real size, mode,
  owner, group, timestamps, inode/device identity, extended attributes,
  content, and symlink target.
- **FR-009**: Every successful directory listing MUST be complete relative to
  its declared visibility domain. Entry overflow, child-inspection failure,
  protected-directory failure, or inconsistent enumeration MUST return an
  explicit error rather than empty/partial success, silent truncation, or a
  synthetic sentinel entry.

#### Target-Visible Errors And Compatibility

- **FR-010**: Outside-domain and force-hidden paths MUST remain target-visible
  as `ENOENT`; visible locked content MUST return `EACCES`; valid content grants
  MUST allow the requested content operation.
- **FR-011**: The new `EACCES` distinctions MUST apply only inside an explicit
  discover-rule domain. A legacy profile with no `see*` rule MUST preserve its
  prior unauthorized-operation collapse.
- **FR-012**: Readdir on an exact-visible directory MUST return non-retryable
  `EACCES` and MUST NOT create a read decision.
- **FR-013**: Unauthorized write to a node inside an explicit discover domain
  MUST return non-retryable `EACCES` without creating a 029 read decision.
  Existing 010 overlay/write authority and grant-implied-only legacy behavior
  MUST remain unchanged.
- **FR-014**: HostFS failures MUST carry a stable typed record containing a
  validated error code, target errno, retryability, and optional public decision
  reference and bounded retry hint. Human prose MUST NOT determine errno.
- **FR-015**: `retryable=true` MUST mean that the unchanged request may progress
  later after time or external control-plane state changes; it MUST NOT imply a
  decision exists. A decision reference MUST appear only for a real
  pending/claimed decision.
- **FR-016**: Host prerequisite/protected-directory failure MUST use a typed
  non-approvable I/O error, not approval-required `EACCES`.

#### Read Decision And Session Authority

- **FR-017**: An approval-eligible read of a visible locked file MUST return
  immediate `EACCES` without waiting for operator resolution and MUST create or
  coalesce one asynchronous `hostfs.read` decision.
- **FR-018**: Explicit read deny, terminal deny/timeout, exact-directory readdir
  denial, and request-capacity refusal MUST NOT create a pending read decision.
- **FR-019**: An approved read decision MUST grant only the exact canonical file
  to the source live session and MUST NOT mutate persistent profile policy.
- **FR-020**: The same running target MUST recognize approved content authority
  on its next retry. Ordinary granted stat metadata MUST converge within one
  second, and cached metadata MUST never grant or continue denying content.
- **FR-021**: Repeated equivalent requests MUST share one decision, MUST NOT
  extend decision timeout or claim lease, and MUST retain terminal deny/timeout
  for the session until an authenticated operator explicitly reopens it.
- **FR-022**: V1 MUST allow at most eight pending `hostfs.read` decisions per
  session and at most eight new read decisions in any rolling 60-second interval
  for that session. Untrusted target explanation text MUST be bounded, marked
  untrusted, deterministically redacted for control-plane material, and rendered
  as plain text.
- **FR-023**: Capacity refusal MUST be retryable without a false decision
  reference. A retry interval MUST appear only when the system can state a
  bounded interval honestly.
- **FR-024**: Deduplication, terminal state, rate/pending accounting, and reopen
  MUST belong to the typed Manager/Core read provider. The broker MUST NOT own
  or mutate decision lifecycle state.
- **FR-025**: Reopen MUST require an authenticated operator, create a new audited
  revision/deadline, and fail closed for ended, orphaned, or unprovable sessions.
- **FR-026**: Session read authority MUST be bounded by session identity,
  canonical path, requested operation, decision, issuance, and expiry; malformed,
  stale, mismatched, unreadable, expired, or retargeted authority state MUST fail
  closed.
- **FR-027**: V1 MUST NOT claim that arbitrary POSIX tools receive a rich
  approval explanation. The target-visible portable contract is immediate
  permission denial followed by retry after out-of-band operator approval.

#### Onboarding, Diagnostics, Evidence, And Claims

- **FR-028**: Visibility presets MUST expand to ordinary per-root rules.
  `privacy` and omitted non-interactive selection MUST remain `none`;
  `landmarks` MUST use confirmed one-level rules; broad home-tree visibility
  MUST require explicit acknowledgement that names may enter target/model
  context.
- **FR-029**: Sensitive-root classification MUST have one categorized source of
  truth shared by workspace checks and visibility. Whole-home workspace risk
  MUST NOT become a whole-home visibility deny. Broad discovery MUST exclude
  maintained credential/browser roots in v1 for both presets and manually
  authored discover rules. Core MUST compile those exclusions into every
  effective discover policy rather than relying on template expansion, while an
  operator-authored exact content grant outside reserved roots retains existing
  HostFS authority and only the exact visibility necessary to use that grant.
- **FR-030**: Visibility access MUST remain lazy. Diagnostics MUST report
  protected-directory state as unknown until observed or explicitly probed and
  MUST warn before a probe that may trigger a host permission prompt.
- **FR-031**: Routine discovery audit MUST be bounded and aggregated by session,
  root, operation, and outcome. Detailed events MUST cover first decision,
  resolution, timeout, suppression, reopen, and activation failure without
  recording file content, symlink target, claim/capability token, or private
  session-authority path.
- **FR-032**: Threat-model and claim-boundary docs MUST replace the global
  denied/absent claim with scoped three-state semantics and MUST state that
  omission cannot prove a predictable sensitive path does not exist.
- **FR-033**: Product evidence MUST register stable 029 proof identifiers for
  policy, typed errors, decisions, redaction, docs truth, real namespace, and
  live session grant behavior. Local-fast output MUST NOT satisfy real Gate 2
  claims.

### V1 Typed HostFS Error Vocabulary

| Code | Target errno | Retryable | Decision reference |
| --- | --- | --- | --- |
| `hostfs.path.hidden` | `ENOENT` | no | never |
| `hostfs.read.approval-required` | `EACCES` | yes | required |
| `hostfs.read.denied` | `EACCES` | no | never |
| `hostfs.read.request-limited` | `EACCES` | yes | never |
| `hostfs.directory.not-enumerable` | `EACCES` | no | never |
| `hostfs.directory.incomplete` | `EOVERFLOW` | no | never |
| `hostfs.write.unauthorized` | `EACCES` | no | never |
| `hostfs.host.prerequisite-failed` | `EIO` | no | never |
| `hostfs.operation.unsupported` | `EROFS` | no | never |
| `broker.unavailable` | `EIO` | yes | never |

### Required Proof IDs

- `029.hostfs-visibility.unit.policy`
- `029.hostfs-visibility.unit.typed-errno`
- `029.hostfs-visibility.local-fast.decision-lifecycle`
- `029.hostfs-visibility.local-fast.redaction`
- `029.hostfs-visibility.real-gate2.namespace`
- `029.hostfs-visibility.real-gate2.live-grant`
- `029.hostfs-visibility.real-gate2.not-run`
- `029.hostfs-visibility.docs.claim-boundary`

### Key Entities *(include if feature involves data)*

- **Visibility Rule**: Operator-authored HostFS policy naming a canonical root,
  exact/one-level/recursive scope, source, subject, lifetime, reason, and deny or
  allow effect.
- **Discovered Node**: Target-visible name and coarse kind within an explicit
  visibility domain, with locked/capability posture but no content or full host
  metadata.
- **Typed HostFS Error**: Stable error code, errno, temporal retryability,
  optional public decision reference, and optional bounded retry interval.
- **Read Decision**: Actionable exact-file request with source session/profile,
  path, policy source, timeout, claim state, operator outcome, revision, and
  audit reference.
- **Session Read Grant**: Exact canonical file authority associated with one
  approved decision and one live session, bounded by issue/expiry state and
  never persisted into profile policy.
- **Sensitive Root Catalog**: Categorized roots shared by workspace safety and
  visibility without treating the whole host home as force-hidden.
- **Visibility Preset**: Explicit convenience selection that expands to
  ordinary rules and records disclosure warnings/non-claims.
- **Visibility Evidence**: Structured proof and audit references distinguishing
  local policy/decision tests from real guest HostFS behavior.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Exact, one-level, and recursive visibility fixtures expose 100% of
  expected non-hidden names within scope and 0 names outside scope.
- **SC-002**: Locked-node fixtures expose 0 real size, mode, owner/group,
  timestamp, inode/device, extended-attribute, content, or symlink-target values.
- **SC-003**: 100% of overflow, child-inspection failure, and protected-directory
  fixtures return an explicit error rather than a successful partial listing.
- **SC-004**: 100% of outside-domain, reserved, and discover-denied lookup
  fixtures without a separate applicable content grant retain `ENOENT`; 100% of
  approval-eligible locked reads return immediate `EACCES` without waiting for
  a human resolution.
- **SC-005**: Repeated equivalent locked reads create exactly one decision and
  never extend its timeout or claim lease.
- **SC-006**: In real Gate 2, an approved exact-file read succeeds on the next
  retry in the unchanged running session, and ordinary granted stat metadata is
  visible within one second.
- **SC-007**: 100% of explicit-deny, timeout, capacity, ended-session, orphaned,
  and unprovable-session fixtures create no unauthorized read grant.
- **SC-008**: Concurrency/abuse tests never exceed eight pending decisions or
  eight newly created decisions in any rolling 60-second interval for one
  session, and request-limited results contain no decision reference.
- **SC-009**: Legacy profiles without `see*` expose 0 additional real directory
  entries and preserve prior unauthorized-operation results in regression tests.
- **SC-010**: Privacy and omitted non-interactive onboarding create 0
  workspace-external visibility rules; confirmed landmarks create only the
  declared one-level rules.
- **SC-011**: Host prerequisite fixtures produce typed non-approvable I/O errors
  in 100% of cases and never create a read decision.
- **SC-012**: Audit, UI, diagnostics, Boundary Summary, and evidence fixtures
  contain 0 injected control-plane secrets, file-content values, symlink targets,
  claim tokens, or private session-authority paths.
- **SC-013**: Gate 0 covers selector, policy, typed error, decision, limits,
  redaction, migration, and docs truth; real Gate 2 covers all promoted
  guest-visible namespace, retry, metadata, symlink, and compatibility claims.
- **SC-014**: Docs truth rejects any claim that visibility is enabled silently,
  discover grants content/execute authority, hidden predictable paths reveal no
  information, arbitrary tools receive rich approval prose, retryable means a
  decision exists, or local-fast evidence proves real HostFS behavior.

## Assumptions

- The primary product path is an isolation-capable backend; native remains a
  weak development harness and cannot prove HostFS guest behavior.
- V1 approvals are exact-file and session-scoped. Operators who need directory
  or persistent reads author explicit profile policy.
- File names and coarse kinds are user data and may enter an external model
  context after the operator enables visibility.
- The privacy template remains zero workspace-external HostFS authority by
  default; interactive landmarks are a separate explicit choice.
- A documented finite directory-entry limit and preset traversal-depth limit
  are required; the exact operational values may be finalized during planning.
  Reaching either limit while more entries exist is an explicit incomplete
  result, never apparent absence.
- Target explanation text, when available, is untrusted and limited to 512
  UTF-8 bytes in v1.
- Hidden predictable names are a non-claim, not a reason for broad discovery to
  expose maintained credential roots or for any grant to expose reserved
  control-plane roots.
- Existing HostFS write overlay, workspace, privilege, DNS, and export/share
  contracts remain authoritative and are not broadened by visibility.
