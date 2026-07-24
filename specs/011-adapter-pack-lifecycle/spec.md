# Feature Specification: Adapter Pack Lifecycle And Local Registry

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `[011-adapter-pack-lifecycle]`

**Created**: 2026-07-08

**Status**: Implemented as a local adapter-pack lifecycle (no public marketplace trust) — the exact claim surface and non-claims live in [docs/STATUS.md](../../docs/STATUS.md)

**Input**: User description: "Implement 011 from `.tmp/011-016-plan.md`: turn the 008 command adapter runtime into a local, testable, digest-locked adapter pack lifecycle. Support local path and exact-commit git sources, a store-wide registry with profile enable bindings, mandatory tests before enable, Core-owned validation as the primary safety gate, built-in adapter metadata, install/upgrade/disable/revoke evidence, and no public marketplace or extra JavaScript authority."

## Clarifications

### Session 2026-07-08

- Q: Which pack sources are in v1? -> A: Local directories and git URLs pinned to an exact commit are in scope; floating branches, remote runtime fetches, recursive submodule trust by default, and public marketplace install are out of scope.
- Q: Where does pack authority live? -> A: The registry is store-wide, but a pack has no runtime authority until a profile explicitly enables it and declares command/capability bindings.
- Q: Are pack-provided tests the security gate? -> A: No. Core-owned validation is the primary enable gate; pack tests are mandatory quality evidence but passing pack-authored tests is never sufficient by itself.
- Q: How are built-in adapters represented? -> A: Built-in adapters remain Core-owned entries with pack-compatible metadata. They are visible through list/test/read-only inspection surfaces but are not mutable registry artifacts.
- Q: Can adapter packs apply HostFS writes or privilege operations? -> A: No. They may classify, deny, simulate, rewrite guest commands, or propose declared capabilities. HostFS write apply, privilege setup, and other authority remain Manager/Core owned.
- Q: Does a profile binding track the latest pack version? -> A: No. Profile enable bindings pin an exact pack lock/revision. Upgrade creates a new candidate and requires explicit re-enable before active profile behavior changes.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Install And Enable A Locked Adapter Pack (Priority: P1)

As a Hideout operator, I want to install a local or pinned-source adapter pack once and explicitly enable it per profile so shared command behavior is reusable without silently broadening any profile's authority.

**Why this priority**: This is the MVP. Without install plus explicit enablement, 008 adapters remain ad hoc profile artifacts rather than a safe reusable ecosystem surface.

**Independent Test**: Install a valid pack into a fresh store, verify it is listed but inactive, enable it for one profile with command/capability bindings, and verify that only that profile can route matching commands through the pack.

**Acceptance Scenarios**:

1. **Given** a valid local adapter pack, **When** the operator installs it, **Then** Hideout records a registry entry, digest lock, source facts, test status, and install evidence without enabling it in any profile.
2. **Given** an installed pack, **When** the operator enables it for one profile with explicit command, capability, and pack lock/revision bindings, **Then** that profile may route only the declared commands through that exact pack revision and every other profile remains unaffected.
3. **Given** a git-sourced pack, **When** the operator installs it, **Then** Hideout requires an exact commit reference and records that commit in the lock evidence before enablement is possible.

---

### User Story 2 - Prove Pack Safety Before Enablement (Priority: P2)

As an operator considering a community-maintained adapter, I want Hideout to validate the pack independently and require deterministic tests so low-quality or tampered packs fail closed before they can affect runtime decisions.

**Why this priority**: Pack reuse is unsafe if enablement trusts pack-authored claims alone. The primary gate must remain Hideout-owned validation.

**Independent Test**: Try to enable packs with schema errors, digest drift, unsupported outcomes, undeclared capabilities, failing tests, and passing self-authored tests that still violate Core constraints; verify every invalid case is rejected before runtime routing.

**Acceptance Scenarios**:

1. **Given** an installed pack whose source changed after locking, **When** the operator tries to enable or run it, **Then** Hideout rejects the pack with digest-mismatch evidence.
2. **Given** a pack whose tests pass but whose manifest, outcome schema, command binding, capability binding, timeout behavior, or exception behavior violates Core constraints, **When** the operator tries to enable it, **Then** Hideout fails closed before profile authority changes.
3. **Given** a valid pack with deterministic tests, **When** the operator enables it, **Then** Hideout records test evidence and enable evidence that distinguish Core validation from pack-authored tests.

---

### User Story 3 - Upgrade, Disable, Revoke, And Inspect Packs (Priority: P3)

As an operator maintaining adapter packs over time, I want to inspect, test, upgrade, disable, or revoke packs with clear audit evidence so stale or risky command behavior can be removed without losing profile clarity.

**Why this priority**: A registry without lifecycle controls becomes sticky authority. Operators need rollback and removal before this can be an alpha-ready ecosystem surface.

**Independent Test**: Install a pack, enable it in a profile, upgrade it to a new pinned source, disable it, revoke it, and verify runtime routing, profile bindings, evidence, and export surfaces reflect each state transition.

**Acceptance Scenarios**:

1. **Given** a profile uses an enabled pack, **When** the operator disables that pack for the profile, **Then** future commands no longer route through it and Hideout records disable evidence.
2. **Given** an installed pack has a newer exact-commit source, **When** the operator upgrades it, **Then** Hideout creates a new candidate lock/test record and does not replace active profile behavior until the new revision passes enablement gates and the profile is explicitly re-enabled to that revision.
3. **Given** a pack is revoked store-wide, **When** any profile would otherwise route through it, **Then** Hideout denies adapter routing and records revoke evidence without falling back to unmediated command behavior.

---

### Edge Cases

- The pack id, version, command names, entrypoint names, or capability names are empty, duplicated, malformed, or contain unsupported characters.
- A pack source path is outside the allowed local source shape, disappears during install, or changes while being locked.
- A git source uses a branch, tag, ambiguous ref, missing commit, shallow state with missing objects, recursive submodule expectation, or unexpected local hook/filter configuration.
- Pack manifest digest and source digest disagree with the generated lock.
- Pack tests are absent, nondeterministic, failing, or pass while Core constraints reject the pack.
- A pack asks for capabilities that the profile did not explicitly allow.
- A pack overlaps a command already owned by another enabled pack or command proxy binding.
- A built-in adapter is listed, tested, or inspected, but an operator tries to mutate it as if it were a registry artifact.
- A disabled or revoked pack is still referenced by a profile.
- A profile is exported or shared while it references store-wide packs.
- A pack attempts to claim root containment, HostFS apply authority, privilege setup authority, process/network/filesystem access, or marketplace trust.
- The registry, lock file, test evidence, audit writer, or profile update cannot be written safely.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Scripts/adapters, profile command adapter bindings, Manager registry lifecycle, local git/path source intake, audit/export evidence, CLI/TUI/WebUI/daemon inspection surfaces, and command routing.
- **Fail-closed behavior**: Hideout denies install, enable, upgrade, or runtime routing when a source is unpinned, lock digests do not match, Core validation fails, tests fail or are missing, an adapter outcome is invalid, a capability is undeclared, a command binding conflicts, a pack is disabled/revoked, source state cannot be verified, or evidence cannot be recorded.
- **User authority and policy**: Store-wide installation grants no runtime authority. A profile gains adapter behavior only through explicit profile enable bindings. Deny rules and existing command policy remain authoritative. Non-operator-authored packs do not gain HostFS, privilege, host-open, network, endpoint, or profile mutation authority merely because they are installed.
- **Generality and provider scope**: This is a generic local adapter pack lifecycle. Specific tools, package managers, agents, editors, and command names may appear only as examples or tests; they do not become Core semantics.
- **Evidence surface**: Install, lock, test, enable, disable, upgrade, revoke, digest mismatch, validation failure, and runtime adapter selection must be visible through audit and local management surfaces. Export/share evidence must include pack references after deterministic redaction.
- **Secret/redaction boundary**: Broker tokens, UI tokens, `HIDEOUT_SECRET_*` backing values, generated machine IDs, hidden store implementation paths, and raw control-plane paths must not appear in pack test output, audit, UI output, logs, or exported artifacts. Pack-authored user-facing text is operator/user data and may be recorded locally unless export/share redaction removes it.
- **Backend/gate expectation**: Gate 0 and focused local smoke tests are sufficient because this feature changes local adapter lifecycle and profile routing rather than guest isolation, DNS, or HostFS data-plane behavior. Real Lima gates are not required unless later implementation changes backend setup or runtime isolation claims.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST support installing adapter packs from local directories and git sources pinned to exact commits.
- **FR-002**: System MUST reject floating git branches, ambiguous git refs, missing commits, remote runtime fetches, and recursive submodule trust by default.
- **FR-003**: System MUST maintain a store-wide adapter pack registry with stable pack identity, version, source facts, lock facts, test status, lifecycle state, and evidence references.
- **FR-004**: System MUST require explicit profile enable bindings before any installed pack can influence runtime command routing, and each binding MUST pin an exact pack lock/revision rather than tracking latest by pack id.
- **FR-005**: System MUST represent built-in adapters as Core-owned entries with pack-compatible read-only metadata and MUST prevent registry mutation of built-in adapter artifacts.
- **FR-006**: System MUST validate pack manifests, command names, entrypoints, source references, declared outcomes, requested capabilities, and profile bindings before install or enablement succeeds.
- **FR-007**: System MUST compute and persist manifest, source, and file digests for installed packs and MUST fail closed when any locked digest no longer matches.
- **FR-008**: System MUST make Core validation the primary enable gate; pack-authored tests MUST NOT be treated as sufficient security evidence.
- **FR-009**: System MUST require pack tests to pass before a pack can be enabled for a profile.
- **FR-010**: System MUST run pack tests deterministically without filesystem, network, process, timer, host mutation, profile mutation, backend handle, broker token, or raw authority access.
- **FR-011**: System MUST reject pack tests, adapter outcomes, or manifests that request unsupported outcomes, undeclared capabilities, raw host execution, HostFS apply authority, privilege setup authority, or mutable profile authority.
- **FR-012**: System MUST prevent two enabled packs, or a pack and existing command binding, from owning the same command in one profile unless a future explicit precedence rule is specified.
- **FR-013**: System MUST support inspecting installed packs, built-in adapter metadata, lock facts, test results, enable bindings, lifecycle state, and evidence references.
- **FR-014**: System MUST support disabling a pack for one profile without deleting the store-wide registry entry.
- **FR-015**: System MUST support revoking a pack store-wide so every profile reference fails closed until the operator resolves or removes that reference.
- **FR-016**: System MUST support upgrading a pack to a new local source or exact git commit by creating a new candidate lock/revision without changing active profile behavior until the new revision passes Core validation, mandatory tests, and explicit profile re-enable.
- **FR-017**: System MUST record install, test, enable, disable, upgrade, revoke, validation failure, digest mismatch, and runtime selection evidence with deterministic control-plane redaction.
- **FR-018**: System MUST include adapter pack identity, version, digest, lifecycle state, and profile binding information in export/share evidence when profiles or audits reference packs.
- **FR-019**: System MUST preserve 008 adapter outcomes: deny, simulate, rewrite guest command, and non-applied capability proposal. It MUST NOT add adapter-applied authority in 011.
- **FR-020**: System MUST preserve 008/009 root-sensitive non-claims: adapter packs may capture command-name intent and risk evidence, but MUST NOT claim absolute-path, syscall, setuid, post-guest-root, or guest-root containment.
- **FR-021**: System MUST ensure HostFS write proposals remain non-applied proposals; actual HostFS staging, claim, apply, and discard remain 010 Manager/Core authority.
- **FR-022**: System MUST fail closed without changing profile authority when registry, lock, test evidence, audit evidence, or profile binding writes cannot be completed safely.
- **FR-023**: System MUST provide a machine-readable pack lifecycle status for local management surfaces and future doctor checks.
- **FR-024**: System MUST update user-facing docs and status so adapter packs are described as local lifecycle-managed extensions, not a public marketplace or trusted publisher system.

### Key Entities *(include if feature involves data)*

- **Adapter Pack**: A local lifecycle-managed collection of adapter metadata, script source, test vectors, declared commands, requested capabilities, and descriptive information.
- **Pack Source**: The local directory or exact-commit git source from which a pack is installed.
- **Pack Lock**: The recorded manifest, source, and file digest facts that define the installed pack contents.
- **Pack Test Result**: Deterministic evidence from running pack-provided tests under Hideout constraints, distinct from Core validation.
- **Pack Registry Entry**: Store-wide record for an installed or built-in pack, including lifecycle state, lock facts, test status, and evidence references.
- **Profile Enable Binding**: Explicit profile-level authority edge that maps installed pack commands, allowed proposal capabilities, and an exact pack lock/revision into one profile.
- **Built-in Adapter Metadata**: Read-only pack-compatible description of a Core-owned adapter.
- **Pack Lifecycle Evidence**: Audit/export facts for install, validation, test, enable, disable, upgrade, revoke, mismatch, and runtime selection outcomes.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of valid local-pack install tests create registry entries and lock evidence while leaving all profiles unchanged until explicit enablement.
- **SC-002**: 100% of exact-commit git-source install tests record the commit and reject floating or ambiguous source references.
- **SC-003**: 100% of tested digest drift cases fail closed before runtime adapter routing.
- **SC-004**: 100% of enablement tests prove Core validation is enforced independently from pack-authored tests.
- **SC-005**: 100% of enabled-pack tests prove profile binding is required and no other profile receives authority from the store-wide install.
- **SC-006**: 100% of invalid outcome, undeclared capability, unsupported authority, command conflict, timeout, and exception fixtures fail closed.
- **SC-007**: 100% of built-in adapter mutation attempts are rejected while built-in metadata remains listable and inspectable.
- **SC-008**: 100% of disable and revoke tests prove future runtime routing stops and evidence is recorded.
- **SC-009**: 100% of upgrade tests prove active profile behavior does not change until the new pack revision passes validation, mandatory tests, and explicit profile re-enable.
- **SC-010**: 100% of pack lifecycle evidence fixtures pass deterministic control-plane redaction and remain export/share safe.
- **SC-011**: Existing 008 command adapter runtime tests continue to pass unchanged for profile-scoped artifacts and built-in root-sensitive intent behavior.
- **SC-012**: Gate 0 or equivalent local smoke proves install, test, enable, disable, revoke, digest mismatch, built-in metadata, and export evidence paths before 011 is marked complete.

## Assumptions

- 011 is local-registry work only. Public marketplace, publisher identity, remote revocation, namespace protection, and signed ecosystem trust are deferred until a public marketplace exists.
- Pack installation and testing are local operator actions. A pack installed from a git source must be pinned to an exact commit before it can be locked or enabled.
- The store-wide registry is a reuse mechanism, not an authority grant. Profile enable bindings are the authority edge and pin exact pack lock/revisions.
- Pack-authored tests are mandatory quality evidence, but the primary safety gate is Hideout-owned validation, digest locks, outcome schema checks, command/capability allowlists, and fail-closed runtime behavior.
- Adapter packs remain constrained script extensions. They do not receive filesystem, network, process, timer, backend, broker token, profile mutation, HostFS apply, or privilege setup authority.
- Built-in adapters remain Core-owned even if exposed through pack-compatible metadata.
- Existing 008 runtime behavior remains supported while 011 adds lifecycle management around it.
