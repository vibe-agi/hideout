# Feature Specification: Ordinary User Release

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `044-ordinary-user-release`

**Created**: 2026-07-26

**Status**: Draft

**Input**: User description: "建立一个‘普通用户发布收口’版本"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Reach A First Useful Result (Priority: P1)

A professional individual Mac user who is comfortable running terminal
commands, but does not know Hideout's internal architecture, installs the
official package, accepts one honest setup review, and runs a useful command in
a selected project without installing build tools or reading design documents.

**Why this priority**: A release is not self-service when the user must
understand profiles, backends, runtime catalogs, helper binaries, or release
gates before seeing the product work.

**Independent Test**: On a clean supported Mac with no Hideout state and no
source checkout, install the exact release candidate, follow only the commands
printed by the product, and complete setup, readiness checking, and one real
project command.

**Acceptance Scenarios**:

1. **Given** a clean supported Mac, **When** the user installs the official
   package, **Then** all Hideout-owned executables, schemas, notices, and
   runtime helpers needed by the default path are installed and verifiable
   without Go or a source checkout.
2. **Given** an empty Hideout store, **When** the user runs setup and confirms
   the review, **Then** the product creates the supported default
   configuration without starting a VM or downloading the retained runtime.
3. **Given** setup has completed, **When** the user follows the printed next
   steps, **Then** the product identifies readiness in plain language and
   provides one runnable first command.
4. **Given** the first VM start may download a large retained runtime, **When**
   the run takes longer than an immediate startup, **Then** the product states
   the exact runtime identity and declared size and emits honest bounded
   progress without fabricating download percentages.
5. **Given** the first real command completes, **When** the user inspects the
   result, **Then** it is clear that the selected project was writable, other
   host files were hidden by default, audit was enabled, and direct networking
   did not hide network origin.

---

### User Story 2 - Find The Right Command Without Learning Internals (Priority: P1)

A new user can ask for help about setup, running, readiness, privacy, updating,
uninstalling, and reporting a problem without first seeing the complete
developer and laboratory command inventory.

**Why this priority**: The existing command set is broad enough that exposing
every internal and advanced surface in the primary help screen obscures the
small supported journey.

**Independent Test**: Starting with no documentation open, use only top-level
and contextual help to locate the setup, run, doctor, privacy follow-up,
upgrade, uninstall, and support-report paths; separately verify that advanced
commands remain discoverable through an explicit expanded index.

**Acceptance Scenarios**:

1. **Given** a new user runs Hideout with no arguments or requests help,
   **When** the primary help is rendered, **Then** it prioritizes the supported
   install-to-first-run journey and clearly points to an expanded command
   index.
2. **Given** the user requests help for setup, doctor, run, privacy, package,
   or support reporting, **When** contextual help is rendered, **Then** it
   exits successfully and provides examples appropriate to that command.
3. **Given** an experienced user needs an advanced or laboratory surface,
   **When** the expanded command index is requested, **Then** no existing
   command is hidden or removed.
4. **Given** help text describes a security boundary, **When** it is compared
   with the support matrix and claim-boundary documentation, **Then** direct
   network, workspace sharing, guest-root, shared-VM, and platform wording is
   consistent.

---

### User Story 3 - Understand And Recover From Failure (Priority: P1)

A user who encounters a missing prerequisite, damaged install, incomplete
setup, runtime problem, network failure, or stale local state receives a short
answer describing what is ready, what failed, and the next safe command.

**Why this priority**: Structured diagnostics already exist, but a normal user
should not need to interpret internal check identifiers, release-gate
instructions, or a long list of passing subsystems.

**Independent Test**: Exercise a healthy installation and representative
failures for package integrity, missing setup, unavailable backend, runtime
provenance, direct networking, privacy prerequisites, and stale recoverable
state. Verify concise default output, stable recovery codes where registered,
and unchanged detailed machine-readable evidence.

**Acceptance Scenarios**:

1. **Given** all required checks pass, **When** the user runs the default
   readiness command, **Then** it reports a clear ready state, the effective
   isolation and network posture, and one suggested run command without
   printing every passing internal check.
2. **Given** one or more checks warn or fail, **When** readiness is rendered,
   **Then** every user-actionable problem has one concise reason and at least
   one runnable next action.
3. **Given** a finding requires release evidence rather than user repair,
   **When** default readiness is rendered, **Then** it does not instruct the
   ordinary user to run repository test scripts; detailed and
   machine-readable modes retain the evidence distinction.
4. **Given** a safe repair is available, **When** the user previews or applies
   it, **Then** the existing typed repair plan remains authoritative and no
   destructive or authority-broadening action is inferred.
5. **Given** the failure is not safely repairable, **When** the product
   responds, **Then** it fails closed and preserves state for inspection.

---

### User Story 4 - Create A Safe Support Report (Priority: P1)

A user can create one bounded, shareable support artifact without manually
collecting audit files or deciding which internal fields are safe to publish.

**Why this priority**: Public users need a practical way to report failures,
while Hideout must not turn support into a path for leaking credentials,
workspace contents, raw host paths, or control-plane material.

**Independent Test**: Inject known control-plane secrets, proxy credentials,
host-user paths, workspace content, and representative diagnostic failures;
generate the support artifact with one command and verify its contents,
schema, size bound, provenance, and redaction.

**Acceptance Scenarios**:

1. **Given** a local installation, **When** the user requests a support
   artifact at an explicit destination, **Then** the product records binary
   identity, platform/support status, package integrity status when
   applicable, a bounded doctor report, and recovery guidance.
2. **Given** audit and workspace data exist, **When** the support artifact is
   generated, **Then** raw audit events and workspace file contents are absent
   unless a separate explicit full-fidelity export is requested through the
   existing export boundary.
3. **Given** known secrets and host-specific paths are present, **When** the
   artifact is scanned, **Then** zero control-plane tokens, proxy values,
   generated machine identifiers, or raw host-user paths are present.
4. **Given** collection of one optional fact fails, **When** the artifact is
   finalized, **Then** the failure is represented honestly and the artifact
   never claims that missing evidence passed.

---

### User Story 5 - Use Privacy Without Assembling Hidden Helpers (Priority: P1)

A user who explicitly chooses privacy networking supplies their own upstream
proxy and mediated resolver, but does not have to find, compile, or manually
place a compatible guest networking helper.

**Why this priority**: The upstream proxy is intentionally operator-owned;
the exact helper used inside Hideout's guest boundary is a product dependency
and cannot remain an undocumented assembly task in a self-service release.

**Independent Test**: Install the exact candidate on a clean supported Mac
without Go and without any external guest helper on `PATH`, configure a privacy
profile with an operator proxy and resolver, verify package ownership and
third-party notices, and complete the real privacy gate.

**Acceptance Scenarios**:

1. **Given** the official package is installed, **When** package verification
   runs, **Then** the exact supported guest privacy helper is present,
   executable, checksummed, attributed, and covered by the package manifest.
2. **Given** no external helper exists on the host, **When** a valid privacy
   profile runs, **Then** Hideout uses its verified package-owned helper.
3. **Given** an explicit development override is supplied, **When** the helper
   is resolved, **Then** the override remains development-only, is clearly
   identified in diagnostics, and cannot silently broaden the packaged
   release claim.
4. **Given** the proxy secret, resolver, helper integrity, or real privacy proof
   is absent, **When** privacy mode is requested, **Then** Hideout fails closed
   without falling back to direct networking.
5. **Given** the default setup path is used, **When** networking is described,
   **Then** direct mode remains the low-prerequisite default and privacy remains
   an explicit follow-up rather than an implied default guarantee.

---

### User Story 6 - Upgrade Or Remove Without Losing Work (Priority: P2)

A Homebrew or standalone-package user can determine the installed package
state, follow the correct upgrade path, repair package-owned files, and remove
Hideout while understanding which durable data is preserved or purged.

**Why this priority**: Installation is only the beginning of the product
lifecycle. Ambiguous ownership or deletion semantics make an early release
unsafe to adopt.

**Independent Test**: Install an older supported package fixture, create
durable profile/audit state and unrelated prefix files, upgrade to the exact
candidate, perform repair and uninstall dry-runs, uninstall normally, and
separately exercise explicit purge.

**Acceptance Scenarios**:

1. **Given** a Homebrew installation, **When** the user asks how to update or
   remove Hideout, **Then** the product and canonical documentation point to
   the Homebrew-owned operation and do not mutate the Cellar independently.
2. **Given** a standalone installation, **When** package status or verification
   is requested, **Then** package identity, integrity, ownership, and the
   applicable repair/upgrade path are clear.
3. **Given** a supported older package, **When** it is upgraded, **Then** all
   durable user state and unrelated files are preserved.
4. **Given** a normal uninstall, **When** removal completes, **Then**
   package-owned files are removed and durable state is preserved with an
   explicit path and purge command.
5. **Given** explicit purge is requested, **When** its preview and confirmation
   requirements are satisfied, **Then** only the exact owned durable store is
   removed and the destructive result is observable.

---

### User Story 7 - Publish One Exact Self-Service Candidate (Priority: P1)

A release operator can prove that the package offered to users is the exact
clean candidate that passed installation, first-run, recovery, privacy,
upgrade/uninstall, UI, isolation, signing, and notarization checks.

**Why this priority**: Local source-tree success cannot establish a public
release. Ordinary users receive package bytes, so all promoted claims must bind
to those bytes.

**Independent Test**: From a clean public commit, build the package once, retain
it, run every required gate against its exact identity, sign and notarize the
same bytes, validate anonymous download identity, and generate the publication
receipt.

**Acceptance Scenarios**:

1. **Given** a dirty, private-only, unpushed, unsigned, unnotarized, stale, or
   rebuilt candidate, **When** release readiness runs, **Then** publication is
   blocked.
2. **Given** an exact retained candidate, **When** ordinary-user acceptance is
   run, **Then** install, setup, concise readiness, first real run, support
   artifact, privacy helper, upgrade, uninstall, and UI journeys all use the
   same package identity.
3. **Given** any required real gate is missing, failed, or `not-run`, **When**
   readiness is evaluated, **Then** no weaker local result can promote the
   corresponding claim.
4. **Given** all gates pass, **When** the release is published, **Then** release
   notes, support matrix, README, package-manager formula, checksums, evidence,
   and publication receipt describe one version and one artifact digest.

### Edge Cases

- The user invokes setup help, doctor help, or support help in a non-interactive
  shell.
- The package is installed by Homebrew but package-owned state is inspected
  from a symlinked executable path.
- The installed binary is valid but one guest helper or third-party notice is
  missing, non-executable, or has a mismatched digest.
- The default profile is absent, customized, malformed, or created by another
  process while setup is waiting for confirmation.
- The runtime download is slow, interrupted, unavailable, stale, or lacks
  sufficient disk capacity.
- The local daemon is stale, from another build, unavailable, or exits while a
  diagnostic or support artifact is being collected.
- Several warnings exist but only one blocks the next useful command.
- A doctor finding is actionable for maintainers but not for the local user.
- A proxy exists on host loopback, on a remote hostname, requires credentials,
  or cannot be reached; none may cause a direct-network fallback.
- An explicit helper override points to a directory, wrong platform binary,
  incompatible version, or modified file.
- Support collection encounters hostile filenames, symlinks, oversized files,
  raw audit data, or injected values that resemble control-plane fields.
- Upgrade is attempted across an unsupported migration range or after
  package-owned files were locally modified.
- Uninstall is requested while sessions or retained environments exist.
- Signing, notarization, CI, or anonymous-download verification succeeds for
  bytes other than the retained gate-tested candidate.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Installation/package ownership, first-run UI,
  diagnostics, export/share redaction, network helper supply, profile network
  selection, runtime lifecycle, daemon observations, uninstall lifecycle, and
  release evidence. No new HostFS, host-app, endpoint, browser, or generic host
  execution authority is introduced.
- **Fail-closed behavior**: Missing or mismatched package artifacts, helper
  provenance, proxy secrets, mediated DNS, backend support, runtime
  provenance, repair authority, release evidence, signature, or notarization
  blocks the affected action. Privacy never degrades to direct networking, and
  recovery never infers destructive cleanup.
- **User authority and policy**: Setup retains one explicit default-no
  confirmation. Privacy remains an explicit operator choice. Repair uses the
  existing typed safe-task plan. Purge remains separately explicit and cannot
  be triggered by upgrade, repair, support collection, or normal uninstall.
- **Generality and provider scope**: The journey is the generic Hideout product
  path for a professional individual operator. Homebrew, Lima, the pinned
  privacy helper, one proxy fixture, VS Code, and one agent CLI are named
  distribution/backend/compatibility fixtures; they do not become generic Core
  authority semantics.
- **Evidence surface**: Concise human readiness, full doctor JSON, package
  verification, redacted support artifact, audit, Boundary Summary, Manager
  facts, UI E2E, product evidence, Gate 2, Gate 3, signing/notarization
  observations, and publication receipt must derive from authoritative runtime
  facts.
- **Secret/redaction boundary**: Support, help, release evidence, logs, and
  diagnostics must exclude daemon and capability tokens, proxy credential
  values, secret backing environment names, generated machine identifiers, raw
  host-user paths, workspace contents, and hidden implementation paths.
  Full-fidelity local audit remains available only through its existing
  explicit export boundary.
- **Backend/gate expectation**: Gate 0 proves static contracts, package content,
  help, concise diagnostics, support artifact, upgrade, uninstall, mutation
  proofs, and negative fixtures. Exact-package macOS arm64 Gate 2 proves first
  run, lifecycle, workspace, projection, and UI-backed paths. Exact-package
  Gate 3 proves privacy helper supply, proxy/DNS forwarding, redaction, and
  privilege evidence. Signing, notarization, anonymous download, and
  publication receipt remain mandatory for public status.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The release MUST target professional individual operators on
  macOS arm64 and MUST NOT imply a non-technical, unsupported-platform, GA, or
  unattended-operation promise.
- **FR-002**: The official install path MUST require neither Go nor a source
  checkout and MUST install every Hideout-owned artifact required by the
  supported default journey.
- **FR-003**: Package installation MUST remain non-interactive and
  side-effect-light: it MUST NOT create a profile, start the daemon or VM, or
  download the retained runtime.
- **FR-004**: Interactive setup MUST remain the single primary configuration
  path and MUST preserve the review, default-no confirmation, no-VM, no-runtime-
  download, plan binding, and idempotence requirements established by feature
  038.
- **FR-005**: The primary help surface MUST prioritize setup, first run,
  readiness, connection posture, audit, update/uninstall, and support reporting
  and MUST link to an explicit expanded command index.
- **FR-006**: Contextual help for every command named in the primary journey
  MUST exit successfully without mutating state.
- **FR-007**: The expanded help surface MUST retain discoverability for every
  supported advanced and laboratory command; help simplification MUST NOT
  remove or rename command authority.
- **FR-008**: Default human doctor output MUST present overall readiness,
  effective isolation/network posture, user-actionable warnings or failures,
  and next commands without enumerating every passing internal check.
- **FR-009**: Detailed human and machine-readable doctor modes MUST retain every
  authoritative finding, evidence requirement, severity, recovery code,
  candidate cause, and next action.
- **FR-010**: Default doctor output MUST NOT instruct installed-package users to
  run source-tree gate scripts; maintainer evidence guidance remains available
  only in explicitly detailed output.
- **FR-011**: A healthy default installation MUST produce one unambiguous ready
  result and one runnable project command.
- **FR-012**: Each user-actionable failure MUST present one concise reason and
  at least one safe runnable next action; known public failures MUST retain
  stable recovery codes.
- **FR-013**: Safe repair MUST continue to use the typed initialization task
  planner, with explicit dry-run and apply modes and no inferred destructive or
  authority-broadening action.
- **FR-014**: The product MUST provide one command that writes a bounded,
  shareable support artifact to an explicit destination.
- **FR-015**: The default support artifact MUST include binary identity,
  support status, applicable package integrity, bounded doctor findings,
  recovery guidance, provenance, and collection failures.
- **FR-016**: The default support artifact MUST exclude raw audit events,
  workspace content, raw host-user paths, secret values and backing names,
  generated machine identity, and control-plane fields or credentials.
- **FR-017**: Support artifact generation MUST be read-only apart from the
  explicit output file and MUST reject symlink, non-regular, unsafe-parent,
  oversized, and overwrite-ambiguous destinations.
- **FR-018**: Support output MUST use a versioned, strictly validated contract
  and deterministic redaction shared with the existing export boundary.
- **FR-019**: The official package MUST own, checksum, verify, attribute, and
  install the exact guest privacy helper required by the supported architecture.
- **FR-020**: The package MUST include applicable third-party license and notice
  material for the privacy helper and its distributed dependencies.
- **FR-021**: Package verification and doctor packaging diagnostics MUST
  distinguish package-owned, explicit development override, missing, damaged,
  and incompatible privacy-helper states.
- **FR-022**: A privacy profile MUST use the verified package-owned helper when
  no explicit development override is supplied.
- **FR-023**: Missing helper integrity, proxy secret, mediated resolver, gateway
  proof, or real privacy evidence MUST fail closed and MUST NOT fall back to
  direct networking.
- **FR-024**: Direct networking MUST remain the default setup posture and every
  primary description MUST state that it exposes the normal network origin.
- **FR-025**: Homebrew users MUST receive Homebrew-owned upgrade, repair, and
  uninstall guidance; Hideout MUST NOT mutate package-manager-owned files
  independently.
- **FR-026**: Standalone users MUST be able to verify package identity and
  integrity, preview repair, upgrade within the declared migration range, and
  preview uninstall from installed artifacts.
- **FR-027**: Upgrade and normal uninstall MUST preserve all durable user state
  and unrelated files; explicit purge MUST remain separately visible and
  bounded to the exact owned store.
- **FR-028**: The ordinary-user acceptance lane MUST install and exercise the
  exact retained candidate without Go, source-root lookup, developer helper
  lookup, or pre-existing Hideout state.
- **FR-029**: Every new assertion MUST include a recorded mutation proof and
  every new judge or gate condition MUST include a negative fixture.
- **FR-030**: Candidate evidence MUST cover install, setup, help, doctor,
  support artifact, first real run, privacy networking, upgrade, repair,
  uninstall, UI, cleanup, signing, notarization, and anonymous package identity.
- **FR-031**: Release readiness MUST reject dirty, private-only, unpushed,
  rebuilt, stale, unsigned, unnotarized, failed, or `not-run` candidate
  evidence.
- **FR-032**: README, Chinese README, first-run guide, distribution guide,
  support matrix, claim boundaries, status, changelog, package caveats, release
  notes, and machine-readable release inventory MUST describe one supported
  journey and one exact release identity.
- **FR-033**: The published candidate MUST remain a prerelease until evidence
  demonstrates a separately specified GA support and maintenance promise.
- **FR-034**: Deferred work discovered by this slice MUST be recorded in the
  debt ledger with a concrete trigger before 044 is marked implemented.
- **FR-035**: The implementation batch MUST include an adversarial report that
  records fresh-eyes findings, mutation proofs, negative fixtures, and the
  exact commands and artifacts used for acceptance.

### Key Entities

- **Ordinary User Journey**: The bounded sequence from official installation
  through setup, readiness, first project command, privacy follow-up, support,
  update, and removal.
- **Readiness Summary**: A human-oriented projection of the authoritative doctor
  report containing overall state, effective boundary, actionable findings,
  and next commands without becoming a second diagnostic source of truth.
- **Support Artifact**: A versioned, bounded, deterministically redacted and
  strictly validated shareable record of product identity, observed readiness,
  applicable package integrity, and recovery guidance.
- **Packaged Privacy Helper**: The exact attributed guest executable, manifest
  entry, digest, platform identity, and notice material required for supported
  privacy networking.
- **Installed Package State**: The owned files, immutable package identity,
  migration range, installation mechanism, durable-store relationship, and
  applicable repair/update/removal guidance.
- **Ordinary User Release Candidate**: One clean public commit and one retained
  package identity to which all local, real-backend, privacy, UI, signing,
  notarization, download, and publication evidence is bound.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A clean supported Mac user completes install, setup, readiness,
  and one real project command using no source checkout, no Go toolchain, and no
  documentation beyond commands printed by the product.
- **SC-002**: Excluding external package download duration, the primary journey
  requires no more than four user-entered Hideout commands before the first
  useful result.
- **SC-003**: Primary help presents the supported journey within its first 20
  non-blank lines, and 100% of advanced commands remain discoverable from one
  explicit expanded index.
- **SC-004**: Contextual help for setup, run, doctor, connection, package, and
  support exits successfully and performs zero durable writes.
- **SC-005**: A healthy default doctor run shows one ready result, boundary
  summary, and next command in no more than 20 non-blank lines; detailed and
  JSON modes retain 100% of authoritative findings.
- **SC-006**: Each tested blocking failure displays at least one runnable safe
  next action, and zero default user diagnostics instruct users to run
  repository gate scripts.
- **SC-007**: One support command creates one schema-valid artifact within the
  declared size limit, and adversarial scans find zero injected secrets,
  control-plane fields, raw host-user paths, or workspace file contents.
- **SC-008**: The exact candidate package passes verification on a clean host
  with no external privacy helper and successfully supplies the helper for the
  real privacy gate.
- **SC-009**: Removing, modifying, replacing, or making the packaged privacy
  helper non-executable causes package verification or privacy readiness to
  fail before target execution.
- **SC-010**: Upgrade and normal uninstall preserve 100% of fixture durable
  state and unrelated files; purge removes durable state only after its
  explicit preview and selection.
- **SC-011**: The exact retained package completes ordinary-user acceptance,
  Gate 0, clean Gate 2, clean Gate 3, required UI E2E, signing, notarization,
  anonymous download verification, and publication receipt with zero failed or
  `not-run` required evidence.
- **SC-012**: Every new assertion has an observed red mutation, every new judge
  has a firing negative fixture, and the adversarial report links those
  observations to the corresponding requirement.
- **SC-013**: English and Chinese primary documentation, command help, package
  caveats, support matrix, status, changelog, release notes, and release
  inventory agree on platform, maturity, default network posture, privacy
  prerequisites, data preservation, version, and package digest.
- **SC-014**: Candidate-created test environments, sessions, temporary support
  artifacts, and package fixtures have zero unaccounted residue after the
  acceptance run.

## Assumptions

- “Ordinary user” means a professional individual operator on an Apple Silicon
  Mac who can use a terminal but is not expected to understand Hideout's
  internal architecture. A GUI-only, non-technical consumer journey is outside
  this version.
- The next candidate remains a prerelease and is expected to use the next
  sequential alpha identity unless release policy selects a stricter maturity
  after all evidence exists.
- Homebrew remains the primary public distribution path, Lima remains the
  first-class isolation backend, and direct networking remains the default.
- The operator continues to own and choose the upstream proxy service and
  mediated resolver. Packaging the guest helper does not make an upstream proxy
  a Hideout service.
- The retained runtime remains a separately downloaded preview artifact; this
  feature improves its first-use explanation but does not create an automatic
  runtime patch-response promise.
- Existing 013 package lifecycle, 024 doctor/recovery evidence, 033 publication
  contract, 038 setup/first-run path, and 040-043 lifecycle/projection evidence
  are prerequisites to be reused and re-proved against the exact candidate,
  not duplicated.
- Automatic background updates, Windows, Intel Mac, a polished remote UI,
  marketplace trust, guest-root containment, workspace DLP, and a GA support
  SLA remain outside this release.
