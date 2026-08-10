<!-- markdownlint-disable MD013 -->

# Feature Specification: Portable Hideout Migration Between Computers

**Feature Branch**: `046-portable-hideout-migration`

**Created**: 2026-08-02

**Status**: Draft

**Input**: User description: "我希望有迁移能力，就是这台电脑的配置，vm 什么的，能 export 出来，然后另一台电脑能导入，就像两台电脑镜像一样。"

## Scope Interpretation

"Like mirrored computers" means that an operator can make a point-in-time,
private copy of their Hideout-owned setup and continue using it on another
compatible computer. The destination receives the selected profiles,
preferences, environment declarations, and stopped VM filesystem state, while
preserving user-visible environment names where possible. It does not receive
the source computer's Hideout control-plane identity, active sessions, live
process/RAM state, or ambient host authority.

This feature is a one-time export/inspect/import workflow, not continuous or
bidirectional synchronization. Export uses copy semantics: the source remains
usable and is never deleted automatically. The source and destination diverge
independently after import.

This migration bundle is distinct from the existing redacted evidence export.
It is a private, encrypted recovery artifact that may contain an entire guest
disk and therefore may contain application data or secrets stored inside that
guest. It is not safe to publish or attach to a support request.

## Clarifications

### Session 2026-08-02

- Q: Should identity regeneration be one operator-controlled switch? → A: No.
  Hideout control-plane, session, operation, and backend-local identities are
  always regenerated. Each environment instead has an import-time Guest
  Identity Policy: Safe Clone by default, or explicitly confirmed Exact Guest
  Restore.
- Q: Should identity reset happen during import so one bundle can be imported
  into multiple computers? → A: Yes. Export produces one immutable,
  destination-neutral snapshot. Every destination performs its own identity
  derivation, path mapping, secret rebinding, and authority review against a
  staged copy without modifying the bundle.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Continue A Hideout Setup On Another Computer (Priority: P1)

An operator selects their Hideout configuration and one or more environments,
exports a portable migration bundle, transfers that bundle by a method of their
choice, and imports it on another compatible computer. The imported
environment retains its guest filesystem and user-visible configuration, but
starts with fresh local control-plane and runtime identities. Guest OS identity
is handled by the policy selected separately for each destination import.

**Why this priority**: This is the core migration outcome. Configuration export
without a usable destination environment, or a copied VM without its matching
configuration, does not let the operator continue their work.

**Independent Test**: On source and destination test computers with independent
Hideout stores, place unique application, filesystem, and configuration
fixtures in a stopped environment; export, transfer, inspect, and import it;
then run a command on the destination and prove the fixtures survived while
all destination-local identities are fresh.

**Acceptance Scenarios**:

1. **Given** a compatible source and destination and a stopped environment,
   **When** the operator exports configuration plus that environment and
   imports it on the destination, **Then** the destination preserves the
   selected portable configuration, environment name, guest files, installed
   guest tools, and application state, and the environment passes normal
   readiness before it can run.
2. **Given** an environment with an active session or an unproved lifecycle
   transition, **When** it is selected for full migration, **Then** export
   stops before snapshotting, explains the blocker, and offers an explicit
   coordinated stop-and-retry action; it never requires the operator to stop
   the daemon merely to make the export safe.
3. **Given** a completed export and import, **When** the source is used again,
   **Then** its original configuration and environment remain present and
   usable; export has not converted the copy into a destructive move.
4. **Given** an imported environment, **When** its first destination session
   starts, **Then** new machine-incarnation, boot, session, broker, UI,
   workspace, and ephemeral credentials are used rather than values copied
   from the source computer.
5. **Given** one completed bundle imported into two or more destinations using
   Safe Clone, **When** each imported environment becomes runnable, **Then**
   every destination has a distinct Guest OS identity and distinct Hideout
   identities, while the authenticated source bundle remains byte-for-byte
   unchanged.
6. **Given** an operator who is retiring the source VM, **When** they explicitly
   choose Exact Guest Restore during import, **Then** the staged destination
   preserves the guest machine identity and SSH host identity, and the review
   states that Hideout cannot prove the disconnected source will remain off or
   guarantee safe concurrent use.
7. **Given** application state in the selected profile's `home`, `config`,
   `data`, and `browser` roots, **When** a full migration completes, **Then**
   those bytes are preserved under a fresh destination profile while profile
   cache, generated machine identity, and generated Git configuration are not
   copied.

---

### User Story 2 - Inspect And Rebind Before Anything Changes (Priority: P1)

Before importing, an operator sees a plain-language inventory and impact plan:
what is in the bundle, how much space it needs, whether the destination is
compatible, which names collide, which workspace paths need mapping, which
secret references are missing, which Guest Identity Policy applies, and which
imported settings could broaden host authority. The operator resolves blockers
and confirms the exact plan before the destination changes.

**Why this priority**: A bundle from another computer contains stale host paths
and potentially powerful policy. Treating it as trusted local configuration
would silently broaden authority or create an unusable clone.

**Independent Test**: Inspect a bundle whose source paths do not exist, whose
secret provider is locked, and whose environment name collides with an existing
destination environment; prove inspection changes nothing and produces the
required mappings, blockers, choices, and authority review.

**Acceptance Scenarios**:

1. **Given** a valid bundle, **When** the operator inspects it, **Then** the
   product shows bundle identity, source and product compatibility, creation
   time, selected components, encrypted size, required destination space,
   environment state, secret-reference status, path mappings, authority
   proposals, Guest Identity Policy and effects, and conflicts without
   modifying destination state.
2. **Given** source workspace or HostFS paths that do not have a proven safe
   destination equivalent, **When** the import is planned, **Then** those paths
   remain unmapped and unusable until the operator explicitly chooses a safe
   destination path; string substitution never grants access.
3. **Given** imported HostFS, host-app, endpoint, mount, network, or other
   authority-bearing settings, **When** the import plan is reviewed, **Then**
   they are shown as disabled proposals and cannot become effective until the
   operator explicitly reviews and approves them under destination policy.
4. **Given** an existing destination environment with the same name, **When**
   import is planned, **Then** the default is refusal; the operator may choose
   a valid new name or a separately confirmed replace operation with explicit
   rollback behavior.
5. **Given** an incompatible destination, insufficient space, an unavailable
   prerequisite, or an unresolved blocker, **When** import is requested,
   **Then** it fails closed before activation and names the exact corrective
   action; it never silently drops an incompatible component.

---

### User Story 3 - Choose A Clear Privacy And Portability Scope (Priority: P2)

An operator can choose configuration-only migration for a small portable setup
or full migration for selected stopped environments. The review distinguishes
Hideout-owned configuration, guest disk contents, host workspaces, local audit
history, caches, host applications, and secrets so labels such as "all" never
hide what will be copied.

**Why this priority**: A full guest disk may be large and sensitive, while many
operators only need profiles and preferences. Explicit scopes make migration
both practical and honest.

**Independent Test**: Produce configuration-only and full bundles from the same
source, inspect their inventories, and prove that each contains exactly the
selected categories and no excluded host workspace, audit history, cache, or
secret value.

**Acceptance Scenarios**:

1. **Given** an interactive export, **When** the operator chooses components,
   **Then** the interface offers configuration-only and full-environment
   choices, lists every selected environment and estimated size, and expands
   any bulk selection into a visible concrete inventory before confirmation.
2. **Given** a default export with secret references in selected profiles,
   **When** the bundle is created, **Then** reference names and availability
   requirements may be included but secret values are excluded, and the
   destination clearly reports which values must be entered or rebound.
3. **Given** an explicit request to transfer selected Hideout-managed secret
   values, **When** the operator reviews and confirms it through a secure
   interaction, **Then** only the named values enter the encrypted private
   payload, their values never appear in arguments, progress, logs, receipts,
   or inventories, and the destination writes them only to its local secret
   provider.
4. **Given** a full-environment export, **When** the guest disk or included
   profile application state may contain application-managed credentials or
   private data, **Then** the interface names both categories, explains that
   their contents cannot be classified safely, and requires an explicit
   sensitivity acknowledgement.
5. **Given** ordinary host project directories, local command/file/network
   observation history, release evidence, download caches, or installed host
   applications, **When** any migration scope is selected, **Then** those
   categories are excluded from v1 and are named as exclusions rather than
   silently implied by "full".

---

### User Story 4 - Resume Or Recover A Large Migration Safely (Priority: P2)

An operator can interrupt a long VM export or import, restart Hideout or the
computer, and see whether the operation can resume, must roll back, or needs a
specific recovery action. Previously verified work is reused where safe, while
no half-imported environment appears runnable.

**Why this priority**: VM disks are large enough that a process crash, full
disk, unplugged drive, or laptop restart is normal operational risk rather than
an exceptional case.

**Independent Test**: Interrupt export and import at every durable phase,
restart the daemon and computer boundary in the harness, retry with the same
operation, and prove a single terminal result with no duplicated environment,
authority, or secret effect.

**Acceptance Scenarios**:

1. **Given** an interrupted export with intact verified progress, **When** the
   operator resumes it, **Then** the operation continues from the last safe
   checkpoint and does not reprocess already verified data unnecessarily.
2. **Given** an interrupted import before activation, **When** recovery runs,
   **Then** staged content remains unavailable to runs and can be resumed or
   removed without affecting pre-existing destination state.
3. **Given** an interruption during activation, **When** Hideout restarts,
   **Then** it proves whether each planned effect committed and deterministically
   completes or rolls back the operation before allowing the affected names to
   be used.
4. **Given** a cancellation request, **When** cancellation is still safe,
   **Then** temporary data is removed and the operation records cancellation;
   **When** the commit boundary has been crossed, **Then** the product reports
   recovery status instead of claiming an unsafe cancellation.

---

### User Story 5 - Use The Same Understandable Flow Everywhere (Priority: P3)

An operator can start, inspect, confirm, monitor, resume, or recover migration
from the CLI, TUI, or WebUI without learning different meanings. The TUI/WebUI
provide a step-by-step selection and review dialog, while automation can use
the same plan non-interactively.

**Why this priority**: Migration is infrequent and high consequence. It must be
self-explanatory for a first-time user, while remaining scriptable for an
experienced operator.

**Independent Test**: Create equivalent migration plans through CLI, TUI,
WebUI, and automation and prove they have the same inventory, blockers,
effects, confirmation requirements, and terminal receipts.

**Acceptance Scenarios**:

1. **Given** a first-time user in an interactive terminal, **When** they open
   migration, **Then** they can select scope, inspect consequences, map paths,
   rebind secrets, resolve conflicts, and confirm through a guided flow whose
   default focus is the safest valid action.
2. **Given** a long-running operation, **When** the operator views any supported
   surface, **Then** it shows the current phase, concrete item, bytes completed,
   total or "unknown", elapsed time, estimated remaining time or "unknown",
   blockers, and the next available action rather than unexplained scores.
3. **Given** a user reading migration help, **When** they view the default help
   or expanded help, **Then** they see the difference between configuration-only
   and full migration, what is always excluded, the stopped-VM rule, secret
   handling, compatibility, and copyable end-to-end examples.
4. **Given** a non-interactive invocation with an ambiguous bulk selector or
   missing sensitive-data decision, **When** it is evaluated, **Then** it
   refuses with a concrete preview command instead of guessing or prompting on
   a non-terminal input.

### Edge Cases

- The bundle is empty, truncated, tampered with, created by an unsupported
  product version, or decrypted with the wrong key.
- The bundle declares duplicate components, duplicate paths, inconsistent
  sizes, invalid names, impossible state transitions, or content outside its
  declared inventory.
- A crafted bundle attempts path traversal, absolute-path extraction,
  symlink/hard-link escape, special-device creation, sparse-size abuse, or
  excessive expansion.
- Source or destination storage becomes full after preflight but before the
  operation completes.
- An environment starts, stops, recreates, or receives a new session while an
  export is quiescing it.
- The daemon, CLI, TUI, WebUI, backend, or computer exits during each export,
  transfer, validation, activation, rollback, and cleanup boundary.
- The destination is the same computer and store as the source, or the same
  bundle is imported twice.
- The same immutable bundle is imported concurrently into two or more
  destinations, with the same or different Guest Identity Policies.
- Multiple imports race for the same environment name, path binding, secret
  reference, or operation identifier.
- The destination has the same environment name with identical, older, newer,
  running, drifted, or corrupt state.
- The destination differs in host operating system, architecture, backend
  capabilities, virtualization support, product version, or image availability.
- A source workspace was moved, renamed, deleted, replaced at the same path, or
  mapped to the destination home, Hideout store, credential root, or a parent
  of a reserved root.
- A secret was omitted, rotated after export, locked on destination, already
  exists with a different generation, or targets an unavailable provider.
- The guest disk contains credentials, a guest machine identity, SSH host keys,
  device-bound licenses, absolute source-host paths, or services that expect a
  unique network identity.
- Exact Guest Restore is selected and the source VM later runs concurrently on
  a disconnected computer with the same guest-visible identity.
- Imported policy would be broader on the destination because paths,
  applications, ports, or platform permissions differ.
- A configuration-only bundle references an environment whose VM data was not
  included or a base image that is no longer available.
- The transfer medium changes timestamps, filenames, permissions, or outer-file
  metadata while preserving or corrupting payload bytes.
- The source and destination clocks differ materially.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Environment and backend lifecycle, profiles and
  preferences, HostFS/host-app/endpoint/network/mount proposals, secret
  references and optional secret transfer, daemon operations, local storage,
  and CLI/TUI/WebUI/automation. Migration does not add network upload or cloud
  synchronization authority.
- **Fail-closed behavior**: Unknown format or compatibility, incomplete
  inventory, integrity/authentication failure, unsafe path, unproved VM
  quiescence, active transition, unresolved conflict, missing capacity,
  unavailable secret provider, ambiguous recovery, imported authority without
  review, unknown Guest Identity Policy, or inability to apply its required
  destination-local identity effects stops before affected state becomes
  runnable or authoritative.
- **User authority and policy**: Source policy is data, not destination
  authority. Imported grants and other capability-broadening settings are
  disabled proposals until the destination operator reviews and explicitly
  approves them. Deny precedence, reserved-root rules, destination capability
  checks, and high-risk confirmations still apply after path mapping.
- **Generality and provider scope**: The product contract is generic across
  supported backends and secret providers. A provider may advertise
  configuration-only, full-environment, or unsupported migration capability;
  current macOS, Keychain, and Lima behavior are proof targets rather than Core
  semantics. No particular transfer service, cloud drive, agent CLI, proxy,
  editor, or package manager is built into migration meaning.
- **Evidence surface**: Local audit, operation status, inspect/plan output,
  doctor, Manager API, CLI, TUI, and WebUI show bundle identity, selections,
  identity policy, blockers, effects, recovery, and terminal receipts from the
  same authoritative operation state. Public/shareable evidence includes no
  bundle contents, secret values, or raw private payload.
- **Secret/redaction boundary**: Migration bundles are encrypted private
  artifacts. Hideout-minted tokens, source machine/session identities, secret
  values, encryption keys, raw guest bytes, and passphrases never appear in
  arguments, environment variables, logs, progress, audit summaries, UI
  notifications, crash reports, or public evidence. Local path values are
  visible only where needed for the operator's private mapping review and are
  absent from shareable summaries.
- **Backend/gate expectation**: Contract and adversarial tests cover parsing,
  planning, identity, encryption, authority review, recovery, and mutation
  proofs. A full-environment claim additionally requires an installed,
  release-shaped, real macOS arm64 backend gate using independent source and
  destination stores, plus a two-computer acceptance run before promotion.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST provide one migration product path with export,
  inspect, plan, confirm, import, status, resume, cancel, and recover actions.
- **FR-002**: Export MUST support a configuration-only scope and a full scope
  containing configuration plus one or more explicitly selected environment
  VM states.
- **FR-003**: A full-computer selection MUST expand to a reviewable list of
  concrete included and excluded components, environments, and estimated sizes;
  a label such as "all" MUST NOT be the only description of scope.
- **FR-004**: The operator MUST be able to include or exclude individual
  environments without editing source state.
- **FR-005**: Export MUST use copy semantics and MUST NOT delete, disable, stop
  permanently, or transfer ownership of source state after completion.
- **FR-006**: V1 MUST NOT provide live or bidirectional synchronization; each
  imported copy MUST evolve independently after the point-in-time import.
- **FR-007**: V1 migration MUST explicitly exclude ambient host workspace
  contents, local command/file/network observation history, audit history,
  release evidence, caches, installed host applications, host platform
  permissions, active processes, and VM memory state.
- **FR-008**: The migration bundle MUST contain a versioned, bounded inventory
  that identifies its bundle, source compatibility facts, creation time,
  selected components, logical relationships, sizes, and integrity identities;
  it MUST remain destination-neutral and contain no precomputed destination
  identity or approval decision.
- **FR-009**: Every migration bundle MUST be encrypted and authenticated before
  it becomes a completed portable artifact; v1 MUST provide no plaintext
  portable-bundle mode.
- **FR-010**: Unlocking material MUST be accepted only through a secure
  interactive or non-interactive secret-input channel and MUST NOT be accepted
  in command arguments or ordinary environment variables.
- **FR-011**: The completed bundle MUST be readable only by its owner by
  default, and an incomplete, failed, or cancelled export MUST NOT leave a file
  that can be mistaken for a completed bundle.
- **FR-012**: Bundle inspection and import MUST authenticate every selected
  component and MUST reject missing, duplicate, reordered, substituted,
  truncated, or extra content before that content becomes active.
- **FR-013**: Bundle parsing and staging MUST enforce declared entry count,
  individual size, total size, expansion, nesting, path, link, and file-type
  limits before allocation or extraction can affect destination-owned paths.
- **FR-014**: Inspection MUST be read-only and MUST work before any destination
  profile, environment, secret, path binding, or authority proposal is changed.
- **FR-015**: Full environment export MUST require the exact selected VM
  incarnation to be stopped and free of active sessions and unproved dependent
  resources before its state is captured.
- **FR-016**: The system MUST offer an explicit plan to stop eligible selected
  environments and prove quiescence, but MUST NOT force-stop them, recreate
  them, or require daemon shutdown without operator confirmation.
- **FR-017**: The captured environment state MUST represent one proved
  point-in-time filesystem state; a lifecycle change during capture MUST abort
  or invalidate the affected capture.
- **FR-018**: A full import MUST preserve selected guest filesystem contents,
  installed guest tools, guest user configuration, and the selected profile's
  persistent application state under `home/`, `config/`, `data/`, and
  `browser/`. Profile cache, generated profile machine identity, and generated
  Git configuration MUST be excluded; destination-generated profile state and
  identities transformed by the selected Guest Identity Policy MUST be
  recreated during import.
- **FR-019**: Import MUST regenerate all Hideout-owned machine-incarnation,
  boot, session, broker, UI, workspace, operation, endpoint, ephemeral secret,
  and backend-local identities independently on every destination before the
  imported environment is eligible to run; export MUST NOT generate or embed
  those destination-local values.
- **FR-020**: Every full-environment import MUST select a Guest Identity Policy
  per environment. Safe Clone MUST be the default and MUST regenerate the
  staged copy's Guest OS machine identity, SSH host identity, and other
  duplicate-sensitive guest identities the product can truthfully manage.
- **FR-021**: Full-environment import MUST be allowed only when the destination
  satisfies the declared host, architecture, backend, virtualization, format,
  version, and capacity compatibility requirements.
- **FR-022**: When full-environment state is incompatible but configuration is
  portable, the plan MAY offer an explicit configuration-only alternative, but
  MUST NOT silently downgrade or partially import the original selection.
- **FR-023**: Portable non-authority settings and user-facing names MUST retain
  their values unless the operator resolves a named destination conflict.
- **FR-024**: Host paths MUST be imported as unresolved source bindings until
  the operator maps them to destination paths that pass real identity,
  reserved-root, overlap, and policy validation.
- **FR-025**: Every imported setting that can add or broaden HostFS, host-app,
  endpoint, network, mount, environment, or profile authority MUST enter the
  destination as a disabled proposal and require explicit destination review.
- **FR-026**: Secret references and their availability requirements MUST be
  portable without revealing values; secret values MUST be excluded by
  default.
- **FR-027**: The operator MAY explicitly select named Hideout-managed secret
  values for transfer only after a sensitivity review; selected values MUST
  remain inside the authenticated private payload and be written directly into
  the destination secret provider without a plaintext intermediate artifact.
- **FR-028**: Provider-specific opaque secret records MUST NOT be treated as
  portable values; import MUST rebind through the destination provider and
  preserve destination-local generation and availability semantics.
- **FR-029**: Full-environment export MUST disclose that application-managed
  secrets inside an opaque guest disk or included profile application state are
  necessarily included and MUST require a separate explicit acknowledgement
  from Hideout-managed secret selection.
- **FR-030**: Host application packs, executables, platform permissions, and
  other host prerequisites MUST be re-observed on the destination; declarations
  or receipts MAY migrate, but source host binaries or permission state MUST
  NOT be copied or claimed present.
- **FR-031**: Import MUST use a draft, immutable impact plan, explicit review,
  confirmation, and apply flow with one operation identity and a recorded base
  revision for every affected destination object.
- **FR-032**: A name or object conflict MUST refuse by default; rename and
  replace choices MUST show their exact effects, and replace MUST be an
  independently confirmed destructive lifecycle action with a recovery plan.
- **FR-033**: All selected components MUST be fully staged and validated before
  activation; an unresolved component MUST block the plan until it is fixed or
  explicitly removed from the selection.
- **FR-034**: Affected environments and authority MUST remain unavailable until
  the complete confirmed activation reaches a proved terminal state; a crash
  MUST lead to deterministic completion or rollback before affected names can
  be reused.
- **FR-035**: Repeating export, inspect, resume, import, recovery, or status for
  the same operation identity MUST be idempotent and MUST NOT duplicate an
  environment, secret update, path binding, approval, receipt, or backend
  effect.
- **FR-036**: Export and import MUST maintain durable verified checkpoints so a
  safe retry can reuse completed work, while any unverifiable checkpoint is
  discarded and recomputed.
- **FR-037**: Capacity and compatibility preflight MUST account for the bundle,
  staging, validation, rollback, and final destination state rather than only
  the compressed transfer-file size.
- **FR-038**: Data carried by a migration bundle MUST be treated as
  non-executable input; importing it MUST NOT execute bundled hooks, scripts,
  commands, plugins, guest setup steps, or host application actions.
- **FR-039**: CLI, TUI, WebUI, and automation MUST project the same
  authoritative inventory, plan, blockers, effects, confirmations, operation
  state, and terminal result rather than independently deciding migration
  behavior.
- **FR-040**: Interactive surfaces MUST provide a guided selection, inspection,
  path-mapping, secret-rebinding, conflict-resolution, confirmation, progress,
  and recovery flow; keyboard selection in the TUI MUST open the relevant
  review or edit dialog rather than require hidden commands.
- **FR-041**: Long-running status MUST use concrete phases and units, including
  current item, bytes complete, total or explicit unknown, elapsed time,
  remaining-time estimate or explicit unknown, blockers, and next action.
- **FR-042**: Default and expanded help MUST explain scopes, exclusions,
  quiescence, encryption, secrets, compatibility, conflict behavior, and
  recovery with copyable configuration-only and full-migration examples.
- **FR-043**: Non-interactive use MUST require all scope, sensitive-data,
  mapping, conflict, and confirmation decisions explicitly; missing decisions
  MUST fail with a command that produces or reviews the required plan.
- **FR-044**: Every export and import MUST produce a local audit event and
  terminal receipt containing operation and bundle identities, component
  counts, decisions, effects, and result without secret values, unlocking
  material, raw guest content, or duplicated control-plane identifiers.
- **FR-045**: Doctor and operation status MUST distinguish resumable,
  recoverable, blocked, failed, cancelled, rolled-back, and complete states and
  provide a safe next action for every non-terminal state.
- **FR-046**: The bundle format MUST declare its supported compatibility range;
  unsupported newer, older, or unknown required fields MUST fail closed rather
  than be ignored.
- **FR-047**: Migration MUST create only a local portable artifact and consume
  a local artifact supplied by the operator; cloud upload, peer discovery,
  network transfer, remote deletion, and continuous sync are outside v1.
- **FR-048**: The implementation acceptance evidence MUST include positive,
  fail-closed, redaction, interruption, recovery, mutation, and negative-fixture
  proofs for every newly promoted migration boundary.
- **FR-049**: Exact Guest Restore MUST preserve the selected guest machine and
  SSH host identities, require a separate high-risk confirmation, and state
  that Hideout cannot prove a disconnected source was retired or guarantee
  safe simultaneous operation of duplicate guest identities.
- **FR-050**: All identity transformation, path mapping, secret rebinding,
  authority approval, and destination conflict resolution MUST occur against
  staged destination state during import and MUST NOT modify the authenticated
  migration bundle.
- **FR-051**: A completed bundle MUST be reusable for any number of separately
  confirmed compatible imports, and every Safe Clone import MUST derive Guest
  OS and Hideout identities independently from every other destination import.
- **FR-052**: Application-managed or device-bound identities that Hideout
  cannot classify and transform truthfully MUST remain opaque guest-disk data;
  the import review MUST disclose that they may be duplicated or fail after a
  move rather than claim they were reset.
- **FR-053**: Every ordinary cold start of an activated imported environment
  with attached disks MUST, after the fresh destination disks are mounted and
  before the runtime is marked ready or any target command starts, prove each
  authenticated destination mount's exact path, filesystem type, and
  read-write state and idempotently restore its authenticated original guest
  path through a setup identity separate from the target. A missing,
  conflicting, read-only, mistyped, or unmounted binding MUST fail closed, and
  this proof MUST repeat after every stop/start cycle rather than relying on
  the one-time adoption receipt.

### Key Entities *(include if feature involves data)*

- **Migration Bundle**: The completed encrypted private artifact transferred by
  the operator. It has a stable identity, format/compatibility declaration,
  authenticated inventory, selected payload, and completion marker.
- **Migration Inventory**: The reviewable declaration of included and excluded
  categories, environments, logical relationships, sizes, sensitivity,
  compatibility needs, and integrity identities. It contains no secret values.
- **Portable Configuration**: Hideout-owned profiles, preferences, environment
  declarations, and other settings classified as portable data. Authority-
  bearing fields remain proposals rather than becoming active permissions.
- **Environment State**: A proved point-in-time copy of a stopped environment's
  persistent guest state plus the portable declaration needed to adopt it on a
  compatible destination. It excludes live processes, RAM, and source-local
  control-plane identity.
- **Profile Application State**: The bounded deterministic full-mode component
  containing the selected profile's `home`, `config`, `data`, and `browser`
  roots. It may contain credentials; it excludes cache and generated profile
  identity/configuration, is staged under an exact operation owner, and becomes
  visible only with its fresh destination profile.
- **Path Binding Proposal**: A source logical path requirement awaiting an
  operator-selected destination path and destination safety validation.
- **Authority Proposal**: Imported configuration that could add or broaden host
  capability. It is disabled until explicitly approved under destination
  policy.
- **Secret Transfer Selection**: The explicit set of Hideout-managed secret
  references whose values the operator chose to place in the encrypted payload.
  It is distinct from opaque secrets already stored inside a guest disk.
- **Migration Plan**: The immutable destination-specific review of selections,
  compatibility, mappings, conflicts, approvals, effects, rollback, and
  blockers that must be confirmed before apply.
- **Guest Identity Policy**: The per-environment import choice. Safe Clone
  creates a coexistence-safe Guest OS identity by default; Exact Guest Restore
  preserves the source guest identity under an explicit high-risk non-guarantee.
- **Destination Adoption**: The import-only transformation of a staged
  environment into destination-owned state, including identity derivation,
  path mapping, secret rebinding, prerequisite observation, and authority
  review. It never mutates the source bundle.
- **Migration Operation**: The durable export or import state machine, including
  checkpoints, ownership, base revisions, effects, recovery, and exactly one
  terminal result.
- **Migration Receipt**: The secret-free local record proving what was selected,
  decided, applied, refused, rolled back, or completed without embedding the
  private payload.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In 100% of supported end-to-end fixtures, a selected stopped
  environment can be exported from one independent computer/store, imported
  into another, pass readiness, and reproduce every declared portable
  configuration and guest filesystem fixture.
- **SC-002**: In 100% of completed imports, all Hideout-owned machine, boot,
  session, broker, UI, workspace, operation, endpoint, and ephemeral credential
  identities differ from the source values and from values created by every
  other import of the same bundle.
- **SC-003**: 100% of wrong-key, tampered, truncated, duplicated-content,
  traversal, link-escape, unsupported-format, and incompatible-destination
  fixtures are refused before imported state becomes active.
- **SC-004**: 100% of imported authority-bearing settings remain ineffective
  until they receive explicit destination approval, and every rejected or
  unmapped setting remains visibly blocked.
- **SC-005**: Secret-value sentinels selected for transfer appear in 0 command
  arguments, ordinary environment variables, inventories, help, progress,
  logs, receipts, audit summaries, crash output, or plaintext intermediate
  files across positive and injected-failure tests.
- **SC-006**: A default migration transfers 0 Hideout-managed secret values,
  ambient host workspace files, local activity/audit records, caches, host
  applications, active processes, or live VM memory-state payloads.
- **SC-007**: Export changes 0 source configuration, environment, secret, path,
  approval, or lifecycle records after temporary quiescence has ended, verified
  before and after successful, failed, cancelled, and interrupted exports.
- **SC-008**: For every injected interruption phase, restart and recovery reach
  exactly one complete, rolled-back, failed, or cancelled terminal result with
  0 runnable partial environments and 0 duplicate durable effects.
- **SC-009**: Resuming an interrupted large-bundle fixture reuses all intact
  verified checkpoints and does not restart verified completed work from byte
  zero.
- **SC-010**: A first-time operator can complete a configuration-only migration
  in under 10 minutes, excluding manual file transfer and secret re-entry, using
  only default help and the guided flow.
- **SC-011**: At least 90% of representative first-time users can correctly
  identify what "full migration" includes, what it excludes, and whether
  secrets and host workspaces will move before they confirm export.
- **SC-012**: For operations lasting longer than two seconds, every supported
  interactive surface shows a meaningful phase and refreshed concrete progress
  at least once every two seconds while progress is observable, or explicitly
  states why total or remaining time is unknown.
- **SC-013**: Equivalent plans created through CLI, TUI, WebUI, and automation
  produce 100% identical selections, blockers, effects, confirmation needs, and
  terminal classifications for the same authoritative source state.
- **SC-014**: An installed release-shaped full migration passes between two
  independent real-backend stores and a physical two-computer acceptance run
  before the product claims full-environment portability.
- **SC-015**: Importing one unchanged bundle with Safe Clone into at least three
  independent destinations produces three pairwise-distinct Guest OS identities
  and three pairwise-distinct sets of Hideout identities while preserving 100%
  of declared guest filesystem fixtures.
- **SC-016**: In 100% of Exact Guest Restore fixtures, the selected guest
  identity is preserved, the separate high-risk confirmation is recorded, and
  no product surface claims that source retirement or concurrent uniqueness was
  technically enforced.

## Assumptions

- This feature ships before Hideout has external users. V1 therefore has one
  canonical current-release bundle, operation, and API contract: unpublished
  development formats and local state are disposable, receive no compatibility
  shim or in-place upgrade path, and fail closed if encountered. Compatibility
  claims begin only with artifacts produced by the first published migration
  release.
- V1 serves an individual operator migrating between computers they control;
  multi-user sharing, delegated approval, organization policy distribution,
  escrow, and public bundle exchange are out of scope.
- V1 is point-in-time copy, not move or sync. The source is retained, and later
  changes on either computer are not reconciled automatically.
- The first full-environment proof target is the currently supported macOS
  arm64 backend combination. Configuration-only portability may have a wider
  support matrix; every surface reports actual capability instead of assuming
  all backends can copy VM state.
- Host project/workspace contents remain owned and transferred by the operator
  through Git, backup, or another file-transfer tool. Migration carries logical
  bindings for review but never crawls or copies those host trees in v1.
- "Full" means all explicitly selected Hideout-owned configuration and stopped
  environment persistent state, not the entire host computer and not every
  file Hideout has observed.
- Hideout-managed secret values are excluded by default. Explicit encrypted
  secret transfer is optional because re-entry may be unavailable for some
  credentials, but opaque provider records are never copied as if they were
  portable.
- A guest disk is opaque application state and may itself contain credentials,
  SSH keys, caches, licenses, or private files unknown to Hideout. Included
  profile application state may likewise contain credentials or browser
  sessions. Full export protects both payload classes but cannot truthfully
  classify or redact arbitrary application data while promising continuity.
- A destination must re-observe host applications, permissions, runtime
  prerequisites, and backend capability. A source receipt is not proof that a
  destination prerequisite exists.
- User-visible environment names are portable preferences; internal identities
  and runtime credentials are not portable even when the guest filesystem is.
- The export bundle is an immutable reusable source snapshot. Destination-
  specific identity resets and approvals happen on a staged copy during every
  import, allowing one export to seed multiple independently owned computers.
- Safe Clone is the default because the source remains usable. Exact Guest
  Restore is intended for an operator-managed move where the source VM will be
  retired, but without a cross-computer coordinator Hideout can only record the
  operator's confirmation; it cannot enforce retirement remotely.
- The operator transfers the completed local bundle using an external medium or
  service of their choice. Hideout neither uploads it nor asserts the security
  of that external transport.
