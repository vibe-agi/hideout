# Feature Specification: Zero-Friction Setup

**Feature Branch**: `038-zero-friction-setup`
**Created**: 2026-07-19
**Status**: Implemented (evidence from a dirty worktree at `48af97e`;
not clean release provenance — regenerate real evidence after commit)
**Input**: Add one short, honest first-run setup path for the public macOS
alpha. A new operator should be able to install Hideout, review one fixed
default posture, confirm once, run a real command in Lima, and install and run
a tested agent inside the boundary without first learning templates, profiles,
backends, network modes, or runtime catalogs.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Prepare A Fresh Installation (Priority: P1)

An operator installs Hideout, runs `hideout setup`, reviews one concise plan,
confirms it, and receives the exact next commands needed to enter a project and
run useful work.

**Why this priority**: The current product can enforce the intended boundary,
but first-time users must understand internal architecture before seeing any
value. A single opinionated setup path is the shortest route from installation
to a credible first success.

**Independent Test**: Starting from an installed candidate package and an empty
Hideout store, run setup in a real terminal, confirm once, verify one valid
default profile and setup evidence, and prove that setup neither starts a VM
nor downloads the runtime.

**Acceptance Scenarios**:

1. **Given** a fresh supported installation, **When** the operator runs setup,
   **Then** one review names the fixed isolation, runtime, workspace, network,
   HostFS, and audit posture before any profile is created.
2. **Given** the operator confirms, **When** setup completes, **Then** exactly
   one valid default profile exists and the result distinguishes configuration
   readiness from real execution proof.
3. **Given** setup succeeds, **When** the operator reads the result, **Then** it
   contains runnable doctor, first-command, tested-agent-install,
   tested-agent-run, and privacy follow-up commands without internal IDs or raw
   host paths.
4. **Given** setup runs to completion, **When** backend and runtime activity is
   inspected, **Then** no VM was started and no runtime artifact was
   downloaded by setup.

---

### User Story 2 - Understand The Boundary Before Accepting (Priority: P1)

An operator can decide whether the fixed default is appropriate without first
learning Hideout's complete policy vocabulary.

**Why this priority**: Low friction must not hide material security or privacy
facts. The operator needs a short explanation of what is writable, visible,
networked, and deferred before approving the configuration.

**Independent Test**: Capture the real terminal review and verify that all
material facts are visible before confirmation while injected control-plane
values remain absent.

**Acceptance Scenarios**:

1. **Given** the review is displayed, **When** the operator reads it, **Then**
   it says the project selected by a later run is read/write at `/workspace`.
2. **Given** the review is displayed, **When** the operator reads it, **Then**
   it says files outside that project remain hidden unless separately granted.
3. **Given** direct networking is selected, **When** the review describes it,
   **Then** it says the operator's normal network origin is not hidden.
4. **Given** the runtime is not yet present, **When** the review describes
   first use, **Then** it states the exact preview revision, declared size, and
   possible first-run download without claiming that setup downloads it.
5. **Given** unsupported guarantees are not established, **When** the review
   is rendered, **Then** it does not claim guest-root containment, private
   networking, workspace write filtering, or complete agent compatibility.

---

### User Story 3 - Cancel Or Automate Without Surprise (Priority: P1)

An operator who declines, reaches end-of-input, interrupts setup, or invokes it
without a terminal gets no durable setup state. Automation continues to use
the explicit advanced initialization command.

**Why this priority**: A default-no confirmation is meaningful only when every
negative path is bounded and when a non-interactive caller cannot accidentally
turn convenience into implicit approval.

**Independent Test**: Exercise negative input, empty input, end-of-input,
interruption, and non-terminal input. Verify that no profile, passing setup
evidence, VM, or runtime download exists afterward. Separately verify the
documented explicit automation command.

**Acceptance Scenarios**:

1. **Given** the confirmation prompt, **When** the operator submits anything
   other than an affirmative answer, **Then** setup is cancelled and creates
   no profile or passing setup evidence.
2. **Given** setup has no input terminal, **When** it is invoked, **Then** it
   fails with guidance to use explicit non-interactive initialization and does
   not infer approval.
3. **Given** the local control service was not running, **When** setup starts it
   to obtain a plan and the operator later cancels, **Then** only bounded
   control-service runtime state may remain; no profile, VM, runtime,
   onboarding evidence, or new authority may remain.
4. **Given** an automation caller uses the explicit initialization surface,
   **When** it supplies all required choices and non-interactive approval,
   **Then** it receives the same authority-relevant plan as the equivalent
   interactive setup defaults.

---

### User Story 4 - Re-run Setup Safely (Priority: P1)

An operator can re-run setup to inspect current readiness without having an
existing valid profile normalized, completed, or overwritten.

**Why this priority**: A convenience command that silently repairs metadata or
resets customization would make repeated use unsafe and would turn setup into
a second profile authority.

**Independent Test**: Run setup twice, customize the profile, and run setup
again. Verify byte-equivalent profile data, unchanged metadata and identity
state, unchanged relevant directory timestamps, and no new passing evidence.

**Acceptance Scenarios**:

1. **Given** a valid default profile, **When** setup plans, **Then** it returns
   `Already set up` from read-only observations and sends no apply request.
2. **Given** a valid customized profile, **When** setup is repeated, **Then**
   it reports the effective posture rather than comparing it unfavorably with
   or resetting it to setup defaults.
3. **Given** malformed, unsafe, or unprovable partial profile state, **When**
   setup plans, **Then** it fails closed with one typed recovery path and does
   not overwrite the state.
4. **Given** another process creates the default profile after review,
   **When** the confirmed plan reaches apply, **Then** apply rejects the stale
   plan before mutation and never continues by loading the new profile.

---

### User Story 5 - Complete A Real First Run (Priority: P1)

An operator follows the documented path and executes a useful command in a real
supported VM without relying on a source checkout or weak local fallback.

**Why this priority**: Configuration success is not product success. The first
run must prove that the packaged product, retained runtime, workspace mapping,
identity, audit, and lifecycle work together on the supported platform.

**Independent Test**: Install the candidate package on macOS arm64, run setup,
enter a dedicated Git fixture, execute `hideout run -- git status --short` in
Lima, and observe the workspace, synthetic identity, runtime provenance,
audit, Boundary evidence, and final lifecycle state.

**Acceptance Scenarios**:

1. **Given** the first real run may wait for a large runtime, **When** it starts,
   **Then** Hideout prints the exact runtime identity and declared size, says
   first use may download it, and emits a bounded heartbeat without fabricated
   byte or percentage progress.
2. **Given** a compatible retained environment already exists, **When** a
   subsequent run starts, **Then** it reuses the exact environment and runtime
   provenance without a new instance-creation operation.
3. **Given** a retained environment has mismatched runtime provenance,
   **When** a run starts, **Then** it is rejected or refreshed only through the
   existing verified runtime flow; elapsed time is never treated as proof.
4. **Given** the target starts, **When** identity is inspected, **Then** the
   account is a synthetic non-root user whose account home is
   `/home/developer`, while target state uses
   `HOME=/hideout/profile/home` and the project is presented at `/workspace`.
5. **Given** real backend prerequisites are absent, **When** evidence is
   produced, **Then** the real first-run result is failed or `not-run` and is
   never replaced by a native/local success.

---

### User Story 6 - Install And Run A Tested Agent (Priority: P1)

An operator can use the default direct connection to install a tested agent as
the non-root target into persistent profile-owned state, end that session, and
run the agent by name in a separate Hideout session.

**Why this priority**: A developer runtime that boots but cannot install and
find the CLI it exists to run has not completed onboarding. This journey also
proves that the low-prerequisite direct network default handles a common first-
run package-registry workflow.

**Independent Test**: Use one exact-version, exact-integrity named agent
fixture. Install it under the persistent target-local prefix through direct
networking, end the run, execute its version command by normal path lookup in a
separate run, and verify no host credentials or pre-authenticated state were
imported.

**Acceptance Scenarios**:

1. **Given** a prepared default profile, **When** the fixture is installed,
   **Then** installation succeeds without root or `sudo` and all installed
   files are target-owned.
2. **Given** the installation run has ended, **When** a new run invokes the
   agent by name, **Then** the expected pinned version runs without reinstall
   or an absolute binary path.
3. **Given** setup and agent installation complete, **When** target and host
   state are inspected, **Then** no agent login state, host credential, proxy
   credential, or Hideout control-plane secret was imported.
4. **Given** the named agent fixture passes, **When** product claims are
   written, **Then** it is described as a compatibility fixture rather than a
   guarantee that every agent or package manager works.

---

### User Story 7 - Recover From A Failed First Run (Priority: P2)

An operator receives one bounded cause and one runnable next action when setup
or the first run cannot proceed.

**Why this priority**: The first command after installation is where missing
prerequisites, package corruption, daemon state, runtime mismatch, and partial
configuration are most likely to surface. Raw subsystem errors make the
product appear unreliable even when it has failed safely.

**Independent Test**: Exercise missing backend prerequisites, unsupported
runtime, package corruption, profile conflict, insufficient disk, stale local
control socket, control-service build mismatch, authentication failure, plan
drift, and real backend failure. Verify actionable output and zero hidden
fallback.

**Acceptance Scenarios**:

1. **Given** a known failure, **When** it reaches the operator, **Then** output
   contains one stable recovery code where the public recovery registry covers
   the surface, one concise reason, and one runnable next action.
2. **Given** the local control service cannot start or authenticate, **When**
   setup is attempted, **Then** it identifies stale socket, build mismatch, or
   readiness failure where observable and does not fall back to embedded
   profile or backend authority.
3. **Given** apply fails after a bounded partial effect, **When** recovery is
   shown, **Then** setup reports the observed state honestly and does not claim
   rollback or delete unrelated store state.
4. **Given** real backend execution fails, **When** evidence is written,
   **Then** the real result is failed or `not-run`, never replaced by a weaker
   backend result.

---

### User Story 8 - Keep Advanced Initialization Consistent (Priority: P2)

An advanced or automated operator keeps the existing explicit initialization
choices while setup and initialization share one authenticated control-plane
authority and one plan/apply behavior.

**Why this priority**: Setup is a projection of existing authority, not a new
writer. Leaving normal initialization on a separate embedded mutation path
would create two behaviors to secure and would allow them to drift.

**Independent Test**: Produce the fixed setup plan and the equivalent explicit
initialization plan, compare every authority-relevant effect, then apply each
through the same authenticated control plane and observe equivalent profile,
audit, evidence, and recovery behavior.

**Acceptance Scenarios**:

1. **Given** the fixed setup choices and equivalent explicit initialization
   choices, **When** both are planned, **Then** their authority-relevant plans
   are equivalent apart from interactive presentation.
2. **Given** normal setup or explicit initialization, **When** either applies a
   profile mutation, **Then** it uses the same authenticated Manager authority
   and cross-process serialization rather than an embedded fallback writer.
3. **Given** an advanced operator selects a different valid template, backend,
   network posture, runtime, or profile, **When** explicit initialization is
   used, **Then** those choices remain available and setup's fixed defaults do
   not replace them.

### Edge Cases

- Setup begins with an empty store while another process creates the default
  profile during review.
- The default profile is valid but customized away from setup defaults.
- The default profile is truncated, has unknown fields, is symlinked through an
  unsafe ancestor, or lacks provable ownership.
- The local control service is absent, stale, from another build, cannot
  authenticate the client, or exits during review/apply.
- The runtime catalog or a required local prerequisite changes after review
  but before confirmation.
- Generated timestamps, random identities, or other one-time values differ
  between plan preparation and apply revalidation.
- Input is negative, empty, end-of-file, interrupted, control-byte-containing,
  or non-terminal.
- The runtime artifact has the expected name but the wrong digest, or the
  retained environment records a different runtime revision.
- The supported backend is missing, disk capacity is insufficient, package
  verification fails, or first backend start fails after configuration is
  already ready.
- The real test lane has no network access or the external package registry is
  unavailable.
- The agent fixture installs successfully but is not found in a later session,
  has the wrong ownership, or discovers pre-existing authentication state.
- Setup is invoked from a project containing host-user names or secrets; setup
  does not inspect or sanitize user workspace content.
- The existing privacy first-run lane and the new direct setup lane produce
  different network claims; neither result may satisfy the other's proof.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Profile, environment defaults, runtime selection,
  network posture, daemon/control-plane routing, init lifecycle, package
  onboarding, audit, evidence, and first-run UI. No new HostFS, host-open,
  endpoint, script, or host-application capability is introduced.
- **Fail-closed behavior**: Missing prerequisites, malformed or unsafe profile
  state, stale reviewed plans, runtime drift, daemon startup/authentication
  failure, unsupported platforms, and absent real-backend proof stop before
  profile or backend effects. No embedded or weak-backend fallback is allowed.
- **User authority and policy**: The operator reviews one fixed plan and must
  affirm locally. Existing customized profiles remain user-authoritative and
  read-only to setup. Advanced and automation choices remain on explicit
  initialization surfaces. Setup grants no later HostFS, host-app, endpoint,
  or decision authority.
- **Generality and provider scope**: Setup is a generic Hideout product path.
  Homebrew is the supported alpha distribution path, Lima is the first real
  isolation proof backend, and the pinned agent package is a named
  compatibility fixture only. None becomes generic Core policy semantics.
- **Evidence surface**: Human setup output, audit, onboarding evidence,
  doctor, authenticated Manager results, product evidence, documentation truth,
  packaged terminal tests, and a real backend gate describe the same outcome.
- **Secret/redaction boundary**: Setup output, audit, evidence, docs, and test
  artifacts exclude daemon tokens, capability tokens, proxy secrets, generated
  machine identity, raw host-user paths, and other Hideout control material.
  User workspace content remains user data and is not inspected or heuristically
  redacted by setup.
- **Backend/gate expectation**: Gate 0 proves parser, plan equivalence, stale
  rejection, cancellation, pure-read reruns, recovery, and redaction. Packaged
  terminal proof validates the distributed command. A real macOS arm64 Lima
  gate proves runtime, workspace, identity, audit, lifecycle, and agent
  install/run behavior. The existing privacy-network lane remains separate and
  cannot be replaced by the direct setup lane.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The product MUST recognize exactly `hideout setup` as the
  interactive first-run command and MUST reject additional arguments without a
  generic fallback.
- **FR-002**: Setup MUST select the default profile, developer template, Lima
  backend, direct network posture, exact `developer-standard` runtime,
  `/workspace` alias presentation, no outside HostFS visibility, and always-on
  audit through existing typed configuration authority.
- **FR-003**: Setup MUST render one concise review before any profile mutation.
- **FR-004**: The review MUST disclose writable workspace access, direct
  network non-privacy, hidden-by-default outside paths, preview runtime
  maturity, declared first-use size, possible first-run download, and always-on
  audit.
- **FR-005**: Setup MUST ask exactly once through the initiating local terminal
  and MUST default to rejection.
- **FR-006**: Negative confirmation, empty input, end-of-input, interruption,
  and non-terminal invocation MUST create no profile, passing setup evidence,
  VM, runtime download, or new authority. Starting or refreshing bounded local
  daemon runtime files in order to plan is the only permitted setup-side
  operational effect before confirmation.
- **FR-007**: Automation MUST use explicit non-interactive initialization;
  setup MUST NOT expose an implicit unattended approval flag.
- **FR-008**: Setup and normal explicit initialization MUST use the same
  authenticated daemon-hosted Manager plan/apply authority and MUST NOT retain
  a normal embedded profile mutation fallback.
- **FR-009**: Daemon startup, build, readiness, or authentication failure MUST
  fail closed with actionable recovery and no backend fallback.
- **FR-010**: Apply MUST be bound to the exact effect-relevant plan reviewed by
  the operator and MUST reject profile, runtime, and prerequisite drift before
  mutation.
- **FR-011**: Every setup or initialization profile mutation MUST use the same
  safe cross-process profile serialization and store-path protections.
- **FR-012**: Setup MUST NOT start a VM, prewarm an environment, or download a
  runtime artifact.
- **FR-013**: Setup MUST select and record the exact supported runtime revision
  and MUST revalidate that provenance during apply.
- **FR-014**: A valid existing default profile MUST make setup terminally
  `Already set up` during planning, using strictly read-only observations and
  no apply request.
- **FR-015**: Setup MUST NOT overwrite, normalize, recreate, delete, complete
  metadata for, materialize identity for, or write new passing evidence for an
  existing valid profile.
- **FR-016**: Malformed, unsafe, partial, unsupported, or conflicting state
  MUST fail closed with one bounded recovery path.
- **FR-017**: Setup success MUST distinguish configuration readiness from real
  backend execution and isolation proof.
- **FR-018**: Setup success MUST provide runnable doctor, first-command,
  exact-version tested-agent-install, tested-agent-run, and privacy follow-up
  commands.
- **FR-019**: Direct-network output MUST NOT imply privacy, mediation,
  anonymity, or hidden network origin.
- **FR-020**: Setup MUST NOT grant HostFS, trusted IDE, endpoint, adapter-pack,
  community host-app, guest-root, or later decision authority.
- **FR-021**: Canonical READMEs, first-run documentation, CLI help, package
  caveats, and the published package-manager formula MUST present setup as the
  primary path and explicit initialization as the advanced path.
- **FR-022**: Package installation MUST remain side-effect-light and MUST NOT
  run setup, start the daemon, create a profile, start a VM, or download the
  runtime automatically.
- **FR-023**: The existing first-run evidence harness MUST add a direct/setup
  real-backend lane while retaining the existing privacy-network lane; the two
  lanes MUST remain distinct because they prove different network claims.
- **FR-024**: Real first-run proof MUST use the distributed candidate binary and
  a real supported isolation backend; source-tree and native/local-fast proof
  MUST remain labeled weak.
- **FR-025**: Environment/runtime reuse MUST be exact-provenance-bound and MUST
  be proved by stable environment identity and observed operations, not elapsed
  time. Mismatch MUST fail or use the existing verified refresh path.
- **FR-026**: Setup review, result, audit, and evidence MUST use deterministic
  control-plane redaction and MUST preserve user data according to existing
  local/export boundaries.
- **FR-027**: Successful setup MUST use existing initialization audit and
  onboarding evidence; it MUST NOT introduce a second `setup-complete` marker
  or weaker source of truth. Cancellation MUST NOT emit passing setup evidence.
- **FR-028**: Documentation truth and product evidence MUST map setup claims to
  stable local and real proof identifiers and MUST never treat `not-run` as
  passed.
- **FR-029**: Explicit initialization MUST retain advanced profile, template,
  backend, network, runtime, and automation choices; setup is a fixed projection
  and not a replacement for those controls.
- **FR-030**: The feature MUST NOT introduce a new persisted schema or version
  constant when existing profile, init, audit, evidence, and runtime contracts
  can represent the required facts.
- **FR-031**: The fixed setup plan MUST be authority-equivalent to explicit
  initialization with the same fixed choices, apart from local setup
  presentation and confirmation.
- **FR-032**: Before a potentially long first run, the product MUST display the
  exact runtime identity and declared size, an honest possible-download notice,
  and a bounded heartbeat; it MUST NOT fabricate byte or percentage progress.
- **FR-033**: Real first-run proof MUST install one named, exact-version,
  exact-integrity agent fixture as the non-root target through direct
  networking and execute it by name in a separate session using persistent
  target state.
- **FR-034**: Setup and the agent fixture MUST NOT create agent authentication
  state or import host credentials, proxy credentials, or Hideout control-plane
  credentials.
- **FR-035**: Stale socket, daemon build mismatch, daemon cold-start failure,
  and mid-operation daemon failure MUST produce actionable recovery, zero
  unreviewed profile mutation, and no destructive store-reset guidance.

### Key Entities

- **Setup State**: The authoritative pre-review classification of the default
  profile as fresh, ready, repairable, or blocked. It determines whether setup
  may propose mutation, must remain read-only, or must stop.
- **Reviewed Setup Plan**: The complete effect-relevant configuration and
  prerequisite facts shown to the operator, together with a stable identity
  that binds confirmation to apply without carrying authority or secrets.
- **Fixed Setup Choices**: The product-owned defaults for profile, template,
  backend, network, runtime, workspace presentation, HostFS visibility, and
  audit. They contain no workspace path, proxy credential, agent credential, or
  later capability grant.
- **Runtime Provenance**: The exact runtime family, revision, artifact identity,
  maturity, and declared size selected at setup and revalidated at first use.
- **Setup Evidence**: Existing initialization audit/onboarding facts plus
  registered local and real proof results. It records whether proof passed,
  failed, or was not run without becoming profile authority.
- **Agent Compatibility Fixture**: One named, pinned, integrity-checked package
  used only to prove direct registry access, non-root installation, persistent
  target-local state, and later command discovery.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: From an installed supported package and empty store, an operator
  reaches configuration-ready state with one setup command, one review, and one
  affirmative response.
- **SC-002**: Setup causes zero VM starts, zero environment prewarms, and zero
  runtime artifact downloads in both local and real observation.
- **SC-003**: All five negative paths (negative answer, empty answer,
  end-of-input, interruption, and non-terminal input) create zero profiles and
  zero passing setup proofs.
- **SC-004**: Re-running setup against a valid customized profile changes zero
  profile bytes, metadata fields, identity artifacts, relevant directory
  timestamps, or passing evidence records.
- **SC-005**: Every injected effect-relevant change between review and apply is
  rejected before profile mutation.
- **SC-006**: The real supported-platform lane completes package installation,
  setup, doctor, and a useful Git command inside the supported VM using only
  distributed artifacts.
- **SC-007**: Real first-run evidence proves `/workspace`, both synthetic
  account and target-state identity layers, exact runtime provenance, audit,
  Boundary evidence, and final lifecycle state.
- **SC-008**: A compatible retained environment is reused with the same stable
  identity and no new instance-creation operation; a provenance mismatch never
  passes through reuse. Timing alone satisfies neither outcome.
- **SC-009**: Setup review, success output, audit, and proof artifacts contain
  zero injected control-plane secrets, capability tokens, proxy values,
  generated machine identifiers, or raw host-user paths.
- **SC-010**: English and Chinese READMEs, first-run docs, CLI help, package
  caveats, the published formula, status/support language, and documentation
  truth checks present one consistent primary setup sequence.
- **SC-011**: Zero native/local-fast results can satisfy the registered real
  setup/Lima proof requirement.
- **SC-012**: At least one packaged terminal test executes the real setup
  command and at least one test observes the actual Manager apply result; source
  text matching alone satisfies no setup requirement.
- **SC-013**: All required build, static, unit, schema, documentation, package,
  Gate 0, local-fast, and real first-run checks pass before 038 is marked
  implemented.
- **SC-014**: The real direct-network lane installs the pinned agent as
  non-root, and a separate run resolves it from persistent target state,
  reports the expected version, and finds zero imported authentication or
  secret material.
- **SC-015**: Fixed setup and equivalent explicit initialization produce
  identical authority-relevant plans and equivalent profile, audit, evidence,
  and recovery outcomes.
- **SC-016**: Real identity proof separately observes a synthetic non-root
  account with account home `/home/developer` and target state with
  `HOME=/hideout/profile/home`; neither observation alone is accepted as the
  complete identity proof.

## Assumptions

- The public alpha remains scoped to macOS arm64 with Homebrew as the primary
  distribution path and Lima as the first-class real isolation backend.
- The package manager installs required host prerequisites but does not perform
  setup or first-run effects.
- Direct networking is the fixed low-prerequisite first-success default.
  Privacy networking remains an explicit follow-up with separate prerequisites
  and separate real evidence.
- Setup does not bind a project path. Each later run selects its project from
  the operator's current directory and presents it as `/workspace`.
- Audit is always enabled and is not represented as an optional toggle.
- Starting or refreshing the local daemon to plan is an allowed bounded
  operational side effect before confirmation. It is not profile authority, a
  VM start, or setup success, and normal daemon lifecycle owns its cleanup.
- The cross-process profile mutation lock and safe store-root discipline are
  prerequisites for implementing setup plan/apply.
- The existing privacy first-run lane remains required for its privacy-network
  claim. The new direct setup lane extends the same evidence harness rather than
  creating a parallel script or replacing privacy coverage.
- Because there are no adopted profiles requiring behavioral compatibility,
  normal explicit initialization may cleanly converge onto the daemon-hosted
  Manager path in 038 instead of preserving a permanent embedded mutation path.
- The named agent and package manager are compatibility fixtures and examples,
  not product-wide support guarantees or Core policy concepts.
- First-run runtime waiting can expose identity, declared size, possible
  download, and heartbeat, but not byte-level progress unless the backend later
  supplies authoritative progress events.
- Existing advanced initialization remains available for custom profiles,
  templates, backends, network postures, runtimes, and non-interactive use.
