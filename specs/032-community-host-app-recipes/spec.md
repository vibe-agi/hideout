# Feature Specification: Community Host-App Recipes

**Feature Branch**: `032-community-host-app-recipes`

**Created**: 2026-07-11

**Status**: Implemented

**Input**: Turn the single built-in host-application projection into a generic,
community-contributable recipe lifecycle. Operators may trust local or
exact-commit sources, while Hideout keeps host effects typed, inspectable,
audited, revocable, and unable to broaden silently.

## User Scenarios & Testing

### User Story 1 - Add And Use A Familiar Host App (Priority: P1)

An operator adds a local or exact-commit recipe, sees one plain-language access
summary, explicitly chooses the available access posture for one profile, and
then uses a familiar command such as `cursor .` inside a new Hideout run. The
guest does not need the host executable and never learns its host path or app
identity.

**Why this priority**: This is the product value of the feature: strong guest
isolation without losing normal local-development ergonomics.

**Independent Test**: Install and enable a test recipe for one profile, start a
new real guest run, invoke its command for a workspace resource, and observe the
expected host application effect through the same generic capability used by
the built-in recipe.

**Acceptance Scenarios**:

1. **Given** a valid local recipe and a resolvable host application, **When** the
   operator adds it interactively for one profile, **Then** Hideout shows the
   command, host app, resource classes, return-channel posture, source identity,
   and access choice before any runtime authority is enabled.
2. **Given** a non-interactive add request, **When** the expected source digest
   or explicit access acceptance is absent, **Then** enablement fails before a
   profile binding is created.
3. **Given** an enabled recipe and a new guest run, **When** its projected
   command opens a workspace file or directory, **Then** Core resolves the
   immutable binding and performs only the declared open-resource effect.
4. **Given** a command enabled for profile A, **When** profile B invokes the
   same command, **Then** no binding or host effect is available to profile B.
5. **Given** a session that started before enablement, **When** a recipe is
   enabled, **Then** the old session is unchanged and the operator receives one
   actionable instruction to start a new run.

---

### User Story 2 - Understand, Approve, And Revoke Risk (Priority: P2)

An operator can distinguish a verified app from an explicitly trusted
unverified app, tell whether a Core-reviewed safe posture exists, approve an
elevated launch only for the exact app and run, inspect activity, and revoke the
relationship without losing historical evidence.

**Why this priority**: Community trust is acceptable only when dangerous facts
remain visible and one approval cannot authorize another app or revision.

**Independent Test**: Exercise signed, unsigned, absent, drifted, safe, and
ask-each-run fixtures through CLI and Manager; then approve, deny, timeout,
disable, and revoke while comparing runtime outcomes and audit records.

**Acceptance Scenarios**:

1. **Given** a signed app under an allowed application root, **When** Core
   resolves it, **Then** inspection reports the independently observed identity
   and any package expectation only narrows acceptance.
2. **Given** an unsigned app, **When** the operator explicitly accepts its exact
   canonical identity and digest, **Then** every surface labels it unverified
   and every launch uses the elevated ask-each-run path.
3. **Given** approval for one app, binding, package revision, profile,
   workspace, environment, and session, **When** another identity is requested,
   **Then** the approval does not apply.
4. **Given** a changed permission fingerprint, **When** an update is installed,
   **Then** the active trust is suspended and the permission difference must be
   accepted again.
5. **Given** a disabled, revoked, expired, drifted, or unowned binding, **When**
   the guest invokes its command, **Then** it fails closed without delegating to
   host execution or a shadowed guest binary.

---

### User Story 3 - Open An Already-Authorized HostFS Resource (Priority: P2)

An operator can use the same host-app recipe with a resource already mapped
through an active HostFS portal. The recipe gains no additional HostFS
authority and never receives the underlying host path.

**Why this priority**: A projected editor is useful only if it works with the
resource paths the guest actually sees, while preserving the HostFS boundary.

**Independent Test**: In a real guest, invoke one recipe for an authorized
HostFS content resource and reject see-only, ungranted, stale, ended, symlink-
retargeted, and reserved-root variants.

**Acceptance Scenarios**:

1. **Given** an active same-session HostFS mapping with sufficient content
   authority, **When** the projected app opens that guest path, **Then** Core
   maps and revalidates it without exposing the host path to the guest, recipe,
   decision preview, or public evidence.
2. **Given** name-only visibility, an ended portal, or a changed canonical
   target, **When** the same request occurs, **Then** it is denied before the
   host application launches.
3. **Given** revocation of the underlying HostFS grant, **When** a prior app
   decision is retried, **Then** the resource is revalidated and the stale
   decision cannot restore access.

---

### User Story 4 - Contribute A Recipe Without Core Changes (Priority: P3)

A contributor can scaffold, validate, test, and locally install an ordinary
open-resource recipe without writing a new host capability provider. A
maintainer can add a second built-in recipe as data without creating an
application-specific runtime branch.

**Why this priority**: A community surface is useful only when common recipes
are inexpensive to contribute and still use one auditable authority path.

**Independent Test**: Create two data-defined recipes with distinct commands
and app identities, validate and install one from a mutable source snapshot,
then prove both route through one grammar/provider path and source mutation
does not affect the installed revision.

**Acceptance Scenarios**:

1. **Given** a scaffolded recipe, **When** validation finds an unknown field,
   reserved command, path escape, unsupported resource class, or undeclared
   capability, **Then** it reports a typed error before installation.
2. **Given** a valid mutable local or exact-commit source, **When** it is added,
   **Then** Hideout snapshots regular files into private owned storage before
   digesting, testing, trusting, or enabling them.
3. **Given** the original source changes after installation, **When** the recipe
   runs, **Then** runtime behavior remains bound to the immutable installed
   snapshot.
4. **Given** two built-in recipes, **When** one recipe is removed, **Then** only
   its bindings disappear and no generic provider code changes.

### Edge Cases

- A candidate bundle or executable is a symlink, has a writable ancestor, is
  owned by an unexpected user, or overlaps workspace, HostFS writable roots,
  temporary storage, package intake, or Hideout control state.
- Package identity expectations disagree with the independently observed
  signing identity, or identity changes between review and launch.
- An unsigned app changes bytes after trust but before launch.
- Two packs claim the same command, a pack claims a reserved command, or an
  update changes aliases without changing its display description.
- A guest forges command metadata, binding id, app identity, capability,
  result policy, host path, or unknown intent fields.
- Package launch configuration produces the same dangerous effect as a
  forbidden flag through a settings value.
- A package has no compatible reviewed safe posture, no package tests, no
  publisher identity, or an untrusted installation hint.
- A source contains special files, escaping symlinks, submodules, checkout
  filters, hooks, or installation scripts.
- The selected app is absent, unsupported, unsigned, identity-drifted, or
  replaced while an approval is pending.
- A request targets a guest-only path, a HostFS see-only name, an ended portal,
  a retargeted symlink, or a reserved root.
- Enablement occurs while a prior run is active, or disable/revoke races with a
  launch.

## Constitutional Alignment

- **Authority touched**: Existing `host.app.open-resource`, command projection,
  profile binding, HostFS resource consumption, local package lifecycle,
  Manager decisions, audit, and run lifecycle. No new capability family or
  generic host execution is added.
- **Fail-closed behavior**: Unknown or conflicting commands, mutable or drifted
  package state, unsafe app paths, unverifiable identity, forged binding facts,
  unsupported resource classes, stale grants, unknown fields, and provider
  failure deny before host effect. A projected command never falls through.
- **User authority and policy**: Installing bytes creates no runtime authority.
  Enablement requires explicit exact-revision trust and one profile access
  choice. Safe status requires a compatible Core-reviewed safety profile;
  otherwise v1 uses a visible run-scoped approval. Deny, revoke, and drift win.
- **Generality and provider scope**: Recipes bind ordinary app commands to one
  generic open-resource capability. VS Code, Cursor, and Zed are provider data
  or fixtures, not Core semantics. New host effects remain Core provider work.
- **Evidence surface**: CLI and Manager share lifecycle and inspection models;
  doctor, audit, Boundary Summary, and exported support evidence consume the
  same authoritative binding and observed identity facts. TUI/WebUI lifecycle
  controls are explicitly outside v1.
- **Secret/redaction boundary**: Broker/decision/daemon/proxy credentials,
  repository credentials, raw guest argv, host absolute paths, host username,
  executable path, and mutable source internals do not enter guest responses or
  public evidence. Host-local audit may preserve operator-owned source/app facts
  and export continues through the existing share boundary.
- **Backend/gate expectation**: Gate 0 covers schemas, lifecycle, validation,
  generic dispatch, safety and redaction. Real macOS arm64 Lima Gate 2 must
  prove an external pack, workspace and HostFS mapping, host effect, scoped
  approval, disable/revoke, identity enforcement, and no fallback. Native and
  source-only fixtures are not host-effect evidence.

## Requirements

### Functional Requirements

- **FR-001**: Operators MUST be able to add a host-app recipe from a local
  directory or a repository source pinned to an exact commit.
- **FR-002**: Source intake MUST copy a validated regular-file snapshot into
  private Core-owned storage before digest, tests, trust, enablement, or runtime
  resolution; runtime MUST never read the mutable intake location.
- **FR-003**: Source acquisition MUST reject escaping symlinks, special files,
  submodule recursion, checkout hooks/filters, and package installation hooks.
- **FR-004**: Installing or inspecting a recipe MUST NOT enable a command or
  mutate profile authority. Interactive add MAY combine install, review,
  access choice, and enablement only as one atomic operator flow.
- **FR-005**: Non-interactive enablement MUST require explicit access
  acceptance and MAY require an expected source digest; any mismatch MUST fail
  before authority changes.
- **FR-006**: A recipe MUST declare only commands, a bounded declarative
  open-resource grammar, application expectations, launch syntax, requested
  Core safety profile, resource classes, tests, documentation, and bindings to
  the existing open-resource capability.
- **FR-007**: V1 recipes MUST NOT contain executable hooks, arbitrary shell or
  automation source, JavaScript grammar, raw host argv, a new capability/result
  type, a dynamic provider, profile mutation authority, or an arbitrary host
  data return channel.
- **FR-008**: Core MUST own a stable qualified package/app/binding identity;
  bare shorthand is allowed only when unambiguous.
- **FR-009**: Community recipes MUST identify apps by bounded bundle basenames,
  not arbitrary absolute paths. Core MUST expand them only below a fixed
  application-root allowlist.
- **FR-010**: Immediately before trust and launch, Core MUST resolve symlinks,
  verify bundle/executable containment and ownership, reject unsafe writable
  ancestors, and reject overlap with guest-writable or Hideout control paths.
- **FR-011**: Core MUST independently observe the application signing identity.
  Package Team ID, bundle ID, requirement, or prose MAY only narrow the
  observed identity and MUST NOT authenticate the app by itself.
- **FR-012**: An unsigned application MAY be used only after explicit
  unverified-app trust bound to its canonical identity and a Core-computed,
  stable bundle-tree content digest. Digest traversal MUST be descriptor-safe,
  bounded, reject unsupported file types and out-of-bundle links, and fail if
  the tree changes while measured. The app MUST remain visibly unverified, use
  elevated run-scoped approval, and require re-trust after change.
- **FR-013**: Safe status MUST come only from a Core-owned, named, versioned,
  identity-compatible safety profile that validates the combined effect of
  launch arguments and written configuration.
- **FR-014**: Package-authored flags or settings MUST NOT weaken, replace, or
  emulate around the Core safety floor. A recipe without a compatible safety
  profile MUST use `ask-each-run` in v1.
- **FR-015**: The permission fingerprint MUST cover every authority-bearing
  command, identity, application root/name, executable relative path, launch
  syntax, safety profile/version, resource class, grammar, result policy,
  access policy, and host-data return declaration.
- **FR-016**: Any permission-fingerprint change MUST suspend inherited trust,
  display an explicit permission difference, and require fresh acceptance.
- **FR-017**: Core MUST own a reserved command-name set. Non-reserved conflicts
  MUST fail enablement unless the operator explicitly replaces the owner;
  installation order MUST NOT decide precedence.
- **FR-018**: Each run MUST materialize an immutable command registration that
  binds command, action, package revision, binding, grammar, capability,
  qualified app, profile, and run identity.
- **FR-019**: The broker MUST validate registered-command ownership for every
  open-resource request and derive the app exclusively from that immutable
  binding. Guest/script intent MUST reject app, binding, capability, result,
  resource-kind, host-path, raw-argv, and unknown-field overrides.
- **FR-020**: Core MUST independently decode and validate every intent field;
  security MUST NOT depend on a recipe parser honoring its grammar.
- **FR-021**: An unbound, invalid, unavailable, disabled, revoked, drifted, or
  failed projected command MUST fail closed and MUST NOT invoke generic host
  execution or a shadowed guest binary.
- **FR-022**: Workspace resources MUST map through the current session's
  workspace identity. HostFS resources MUST require an active same-session
  mapping plus sufficient existing content authority; see-only visibility is
  insufficient.
- **FR-023**: Core MUST re-canonicalize and re-authorize the resource immediately
  before launch. Recipes, guest intent, decisions, and public evidence MUST
  never receive the resolved host path.
- **FR-024**: Elevated approval MUST bind the exact capability, app, package
  revision, binding, command, session, profile, workspace, environment, and
  identity. Timeout, owner loss, drift, update, disable, and revoke invalidate
  it.
- **FR-025**: V1 access choices MUST be `safe` when a compatible Core safety
  profile exists or `ask-each-run` otherwise. Persistent profile allowance is
  not a v1 behavior.
- **FR-026**: CLI and Manager MUST share typed add, inspect, enable, disable,
  update, remove/revoke, conflict, permission-diff, and runtime inspection
  models. Missing apps MAY expose package-provided installation hints only as
  clearly untrusted, copy-only text. Every package-provided display string and
  hint MUST be length-bounded and stripped of terminal/control sequences before
  human rendering; machines receive the same bounded value as typed data.
- **FR-027**: Enabling a recipe MUST affect only future runs. Existing sessions
  MUST remain immutable and receive no hot-injected shim or silent recreate.
- **FR-028**: Built-in VS Code behavior MUST migrate to the same pack, grammar,
  binding, identity, safety, decision, inspection, and provider paths used by
  community recipes; app-specific production branches MUST not remain in the
  generic runtime path.
- **FR-029**: Once a stable package/revision operation identity exists, Core
  MUST audit applied install, validation, trust, enable, update, permission
  diff, conflict, disable, revoke, launch, refusal, identity drift, and digest
  mismatch using validated binding facts rather than raw request metadata.
- **FR-030**: Public evidence MUST deterministically remove Hideout control
  credentials and resolved host/executable paths while preserving bounded
  package, app-identity, resource-class, access, outcome, and recovery facts.
- **FR-031**: Recipe-provided tests MUST be optional quality evidence and MUST
  NOT replace Core schema/invariant validation or be presented as a security
  certification.
- **FR-032**: The product MUST provide deterministic scaffold, validate, test,
  add, list, inspect, update, disable, and remove workflows with typed recovery
  for malformed source, app absence, identity failure, conflicts, and drift.
- **FR-033**: V1 completion MUST include real macOS arm64 Lima proof of an
  externally installed pack reaching the existing generic host-app provider;
  embedded-only, native, static-source, or package self-test evidence is
  insufficient.

### Key Entities

- **Host-App Pack**: Immutable installed package revision containing apps,
  bindings, declarative grammars, tests, documentation, source identity, and
  digest; installation alone has no profile authority.
- **Application Expectation**: Package-authored bundle name and optional
  identity constraints that may narrow but never replace Core observation.
- **Observed Application Identity**: Core-derived canonical bundle/executable,
  signing facts, ownership facts, and content identity used for trust and
  launch-time revalidation.
- **Safety Profile**: Core-owned versioned effect floor for a compatible app
  family, covering launch arguments, state layout, settings, and verification.
- **Command Binding**: Immutable per-run mapping from command names and grammar
  to one capability and qualified application revision.
- **Permission Fingerprint**: Canonical digest of every authority-relevant
  package and binding field used to detect trust broadening.
- **Profile Enablement**: Exact package revision, binding set, access choice,
  accepted fingerprint, and conflict ownership for one profile.
- **Run-Scoped App Decision**: Elevated approval bound to the exact app,
  binding, package revision, resource context, and live run identity.
- **Resource Reference**: Host-path-free workspace or active HostFS portal
  reference resolved only by Core from current session state.

## Success Criteria

### Measurable Outcomes

- **SC-001**: A user can add, review, enable, and invoke a valid local recipe in
  one guided flow without editing profile JSON or learning internal app IDs.
- **SC-002**: 100% of non-interactive enables without exact access acceptance,
  and 100% with a supplied digest mismatch, create no profile binding.
- **SC-003**: Two distinct app recipes use the same grammar/provider/runtime
  path, and removing either changes zero behavior for the other.
- **SC-004**: 100% of tested unsafe app paths, writable ancestors, ownership
  failures, guest-writable overlaps, path escapes, and identity drift are
  rejected before a host process starts.
- **SC-005**: 100% of safe launches use a compatible Core-owned safety profile;
  no package-only flag or setting fixture obtains no-prompt safe status.
- **SC-006**: Every authority-bearing field mutation changes the permission
  fingerprint and suspends prior trust; documentation-only changes remain
  visible in source digest without inventing broader permissions.
- **SC-007**: Forged app/binding/capability/host-path/unknown intent fields and
  every failed projection fixture produce zero generic-host or shadowed-guest
  fallback executions.
- **SC-008**: Approval for one app/binding/revision/run authorizes zero launches
  for a different app, command, package revision, profile, workspace,
  environment, identity, or session.
- **SC-009**: A mutable source changed after install causes zero runtime change
  until an explicit update snapshot is reviewed and enabled.
- **SC-010**: Workspace and authorized HostFS resources open successfully in
  real Gate 2, while see-only, ungranted, ended, retargeted, and reserved paths
  produce zero host launches.
- **SC-011**: Enablement changes zero existing session shim inventories; the
  next run contains exactly the newly enabled command set.
- **SC-012**: CLI, Manager, doctor, audit, and Boundary Summary agree on package,
  binding, observed identity status, access posture, outcome, and recovery for
  all representative states.
- **SC-013**: Secret/path injection tests find zero broker, claim, daemon, proxy,
  or repository credentials and zero resolved host/executable paths in guest
  output or public evidence.
- **SC-014**: Real macOS arm64 Gate 2 observes a community-pack command launch
  through the generic provider, scoped approval, disable/revoke fail-closed,
  and built-in VS Code behavior with no application-specific generic branch.

## Assumptions

- Operators may intentionally trust unsigned local/community material; v1
  records and constrains that choice rather than claiming marketplace review.
- V1 sources are local directories and exact-commit repositories only. Archive,
  registry, marketplace, publisher-signing, namespace, and remote-revocation
  systems are deferred until public distribution exists.
- V1 recipe grammar is declarative `open-resource-v1`; constrained JavaScript
  grammar is design-ready only.
- V1 has no dynamic capability providers, arbitrary AppleScript, adb/device
  bridge, raw host execution, or host-to-guest file/result stream.
- V1 supports `safe` and `ask-each-run`; persistent profile-wide allowance is a
  later typed policy, not a permanently approved decision.
- CLI and Manager are v1 lifecycle surfaces. TUI/WebUI controls and real
  Playwright/PTY lifecycle proof are follow-on product polish.
- A host app processing guest-controlled content may have its own extensions,
  automation, vulnerabilities, or trust state. A safety profile reduces known
  effects but does not make the app or workspace safe.
- Existing 030 safe/trusted projection, alias privacy, evidence redaction, and
  no-fallback contracts remain regression requirements.
