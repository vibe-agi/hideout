# Feature Specification: Trusted Host-IDE Workspace Grant

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `039-trusted-ide-grant`

**Created**: 2026-07-20

**Status**: Draft

**Input**: User description: "Make trusted-host-ide usable for one-shot commands
like `code .` by persisting an operator-granted trust as profile+workspace
policy, replacing the per-live-run decision that deadlocks."

## Context

`trusted-host-ide` mode lets a projected command such as `code .` open the
workspace in the operator's full, native editor (extensions and workspace tasks
enabled) instead of the default safe, isolated editor window. Today the mode is
unusable for one-shot commands: the trusted authorization is raised as a
per-live-run decision, `code .` triggers the open and exits before an operator
can approve it, the decision goes stale, and the operator has no window to act.

A throwaway spike on real Lima (2026-07-20) proved the fix: authorize trusted
IDE as durable operator policy scoped to a profile and workspace — the same
shape as a HostFS profile grant — so a later one-shot `code .` reuses it with no
per-run approval. Safe mode is unchanged and remains the default.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Grant trusted IDE once, open natively thereafter (Priority: P1)

An operator who wants their real editor for a project grants trusted IDE for
that workspace once. From then on, `hideout run -- code .` opens the project in
the full native editor without any per-run prompt or approval, including from a
fresh one-shot run.

**Why this priority**: This is the entire point of the feature — it turns a
capability that is currently impossible to complete (one-shot trusted open) into
a one-time grant plus friction-free reuse. Without it, trusted mode is dead for
the headline `code .` path.

**Independent Test**: On real Lima, in trusted mode with a granted workspace,
run `hideout run -- code .` as a one-shot command and confirm the native editor
opens (extensions enabled, operator's own profile) with exit success and no
prompt.

**Acceptance Scenarios**:

1. **Given** trusted mode is selected and the operator has granted trusted IDE
   for the current workspace, **When** a one-shot `hideout run -- code .` runs,
   **Then** the full native editor opens and the command exits successfully with
   no approval step.
2. **Given** a trusted-IDE grant exists for workspace A, **When** the operator
   runs `code .` in a later, separate run for workspace A, **Then** it reuses
   the grant and opens natively without re-granting.

---

### User Story 2 - Fail closed with a clear path when trusted but ungranted (Priority: P1)

An operator who has selected trusted mode but has not granted the current
workspace runs `code .`. The command refuses (no host effect) and tells the
operator exactly how to grant trusted IDE for this workspace.

**Why this priority**: Fail-closed is the security contract, and a dead-end
refusal (the current stale-decision behavior) is the usability bug this feature
exists to remove. The refusal must name the concrete next command, not leave the
operator guessing.

**Independent Test**: In trusted mode with no grant for the workspace, run
`code .` and confirm it refuses without opening any editor and prints the exact
grant command.

**Acceptance Scenarios**:

1. **Given** trusted mode with no grant for the current workspace, **When**
   `code .` runs, **Then** it refuses with no host application launched and
   names the grant command to run.
2. **Given** the operator then runs the named grant command, **When** they rerun
   `code .`, **Then** it opens natively (transition from refused to granted with
   no other change).

---

### User Story 3 - Revoke and drift return to a safe, re-confirmed state (Priority: P2)

An operator can revoke trusted IDE, and the system automatically re-requires a
grant when the workspace identity or the host application identity changes, so a
standing grant never silently authorizes a different project or a changed editor.

**Why this priority**: A persistent high-authority grant must be visible,
revocable, and self-invalidating on drift, or it becomes an invisible
forever-flag. This is required for the feature to be safe to ship, but the core
grant/reuse loop (US1/US2) can be demonstrated before it.

**Independent Test**: Grant trusted IDE, confirm `code .` opens natively; revoke
(or switch the profile to safe mode); confirm `code .` returns to safe/guided
behavior. Separately, change the workspace and confirm the prior grant does not
apply.

**Acceptance Scenarios**:

1. **Given** a trusted-IDE grant, **When** the operator revokes it (explicit
   revoke or switching the profile to safe mode), **Then** the next `code .`
   returns to the safe launch or the guided grant path.
2. **Given** a trusted-IDE grant for workspace A, **When** the operator runs
   `code .` in a different workspace B, **Then** the grant does not apply and B
   is treated as ungranted.
3. **Given** a trusted-IDE grant, **When** the granted host application's
   identity changes (a different editor build/binding), **Then** the grant no
   longer matches and a new grant is required.
4. **Given** any trusted-IDE grant exists, **When** the operator inspects the
   profile's IDE mode, **Then** the existence of the grant is visible.

---

### Edge Cases

- A guest/agent process that writes the workspace (including `.vscode` files or
  a forged grant file in the workspace) MUST NOT be able to create, refresh, or
  read a trusted-IDE grant.
- The safe (default) mode path is unaffected by any trusted-IDE grant and never
  requires one.
- Switching a profile from trusted to safe MUST drop trusted-IDE grants for that
  profile so a later switch back to trusted re-requires the grant.
- Two workspaces that resolve to the same project identity are one grant; two
  distinct projects are two grants.
- A malformed or unreadable grant record MUST fail closed (treated as no grant),
  never as an implicit allow.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: host application projection (`host.app.open-resource`),
  profile policy. It changes how an existing high-authority capability is
  authorized; it introduces no new host capability and does not touch HostFS
  data-plane, network, or backend authority.
- **Fail-closed behavior**: Trusted mode with no matching grant refuses the open
  with no host launch and names the grant path. A malformed/unreadable grant,
  a workspace-identity mismatch, or a host-app-identity mismatch all resolve to
  "no grant" and refuse. Safe mode remains the default when trusted is not
  selected.
- **User authority and policy**: The trusted-IDE grant is durable operator
  policy, the same shape and lifecycle as a HostFS profile grant: operator
  grants it explicitly, it is stored in the profile, read every run, and
  revocable. It is NOT per-run capability authority (broker tokens, session
  material), which is still regenerated each run. Selecting safe mode revokes.
- **Generality and provider scope**: The grant model is generic to host-app
  projection bindings (keyed by the app binding, not by "VS Code"). The built-in
  VS Code binding is the first consumer; nothing in the grant semantics hard-
  codes a specific editor.
- **Evidence surface**: Granting, reuse, refusal, and revocation are auditable.
  The grant's existence is visible through the profile IDE-mode surface. The
  broker already discloses safe-vs-trusted posture on launch.
- **Secret/redaction boundary**: The grant record and any audit of it contain
  only Core-derived identifiers (workspace identity, app binding reference/
  digest, profile). No host path, host username, capability token, or raw guest
  argv appears in the grant record, audit, or any guest-visible response.
- **Backend/gate expectation**: Native harness for unit/contract behavior; real
  Lima proof for the end-to-end one-shot `code .` grant/reuse/refuse loop (the
  spike already demonstrated this and it must become a repeatable gate).

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: An operator MUST be able to grant trusted IDE for a workspace with
  an explicit command, and the grant MUST persist across runs until revoked or
  invalidated by drift.
- **FR-002**: In trusted mode with a matching grant for the current workspace
  and host-app binding, a one-shot projected open (e.g. `code .`) MUST open the
  full native editor with no per-run approval step.
- **FR-003**: In trusted mode with no matching grant, a projected open MUST fail
  closed with no host launch and MUST name the exact command to grant trusted
  IDE for the current workspace. It MUST NOT leave only a stale, unactionable
  decision.
- **FR-004**: A trusted-IDE grant MUST be keyed by the Core-derived workspace
  identity and the host-app binding identity (including its digest), so a grant
  for one workspace or one app binding never authorizes another.
- **FR-005**: A trusted-IDE grant MUST be stored on the control plane the guest
  cannot reach (profile-owned store). Guest-side writes, including to the
  workspace, MUST NOT be able to create, refresh, or read a grant.
- **FR-006**: Switching a profile to safe mode MUST revoke that profile's
  trusted-IDE grants, and an explicit revoke MUST drop a grant without leaving
  trusted mode.
- **FR-007**: A change in the granted workspace identity or host-app binding
  identity MUST cause the grant to no longer match, re-requiring a grant.
- **FR-008**: The existence of a trusted-IDE grant MUST be visible to the
  operator (e.g. through the profile IDE-mode inspection surface) and the grant,
  reuse, refusal, and revocation MUST be auditable.
- **FR-009**: Safe mode MUST remain the default and MUST be unaffected by the
  presence or absence of any trusted-IDE grant.
- **FR-010**: The grant record, its audit, and any guest-visible response MUST
  contain no host path, host username, capability token, machine identifier, or
  raw guest argv.
- **FR-011**: The parallel projection grant checker that the production path does
  not use MUST be removed or explicitly documented as test-only, so there is one
  authoritative trusted-grant decision path.

### Key Entities *(include if feature involves data)*

- **Trusted-IDE workspace grant**: durable operator policy authorizing trusted
  (native) host-app open for one profile + workspace + app binding. Attributes:
  Core-derived workspace identity, host-app binding reference and digest,
  profile. No host path or secret. Lifecycle: created by explicit operator
  grant, read every run, invalidated by revoke / safe-mode / identity drift.
- **Projection grant check**: the single point that decides, at open time,
  whether a trusted host-app launch is authorized — consulting the persistent
  grant, failing closed otherwise.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On real Lima, after one grant command, a separate one-shot
  `hideout run -- code .` opens the native editor with zero additional prompts
  or approvals.
- **SC-002**: In trusted mode with no grant, `code .` refuses without launching
  any host application and the refusal names the grant command; 100% of
  ungranted trusted opens are refused (no host launch).
- **SC-003**: After a revoke or a switch to safe mode, the next `code .` no
  longer opens the native editor (returns to safe or guided behavior) with no
  other operator action.
- **SC-004**: A grant for one workspace never authorizes a native open in a
  different workspace, and a changed app-binding identity never reuses a prior
  grant (both verified by test).
- **SC-005**: A guest process writing the workspace cannot obtain a native
  trusted launch (verified by an adversarial test).
- **SC-006**: There is exactly one production trusted-grant check path; the
  previously test-only parallel checker is gone or documented as such, verified
  by inspection/test.

## Assumptions

- The built-in VS Code host-app binding is the first and only consumer in this
  slice; the grant model is generic but no additional editor binding is added
  here.
- The workspace identity and host-app binding identity (including digest) used to
  key a grant are already computed and available at the open-time check (spike-
  confirmed).
- Safe mode, the broker safe-vs-trusted disclosure line, and the `ide-mode`
  command already exist and are reused; this feature does not redesign them.
- The first-run projected-command shim timing flake observed during the spike is
  a separate, pre-existing issue tracked in DEBT and is out of scope here.
- Single-operator MVP: no multi-operator or role-based approval; the operator is
  trusted to make the grant decision, which the disclosure and revocability keep
  visible and reversible.
