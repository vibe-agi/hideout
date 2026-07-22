# Feature Specification: Workspace Executable Support

**Feature Branch**: `041-workspace-executable-support`

**Created**: 2026-07-22

**Status**: Implemented

**Input**: User description: "Advance Hideout toward self-service use by making
ordinary guest-compatible executables and scripts stored in the selected
workspace run reliably on the promoted shared Workspace Portal path, including
project-local tool launchers such as `node_modules/.bin`, without weakening the
workspace or host-authority boundary."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Run Project-Local Tools (Priority: P1)

As an operator running a coding agent or unfamiliar CLI in Hideout, I can run
an executable stored in the selected project just as I can in an ordinary Linux
development checkout, so project test, build, lint, and helper commands do not
fail merely because the project is the mounted workspace.

**Why this priority**: Project-local executables are part of normal development
workflows. Without them, many otherwise supported agent tasks stop at the first
package-manager launcher or compiled project tool.

**Independent Test**: In a supported isolated environment, create executable
scripts and guest-compatible binaries inside the selected workspace, invoke
them directly and through a project-local launcher directory, and verify their
exit status, output, arguments, working directory, and file effects.

**Acceptance Scenarios**:

1. **Given** an executable script in the selected workspace whose interpreter
   exists in the guest, **When** the target invokes it directly, **Then** it runs
   with the expected arguments, working directory, output, and exit status.
2. **Given** a guest-compatible executable in the selected workspace, **When**
   the target invokes it directly, **Then** it runs without a mount-related
   operation-not-supported failure.
3. **Given** a project command that resolves through a workspace-local launcher
   directory, **When** the target runs the command, **Then** the same selected
   workspace content is executed and its observable workspace writes remain in
   the host checkout.

---

### User Story 2 - Preserve Workspace And Session Boundaries (Priority: P1)

As a security-conscious operator, I retain the existing exact-workspace,
session-authority, and shared-VM boundaries while project-local execution is
enabled, so convenience does not create a hidden host mount, ambient host
execution path, or cross-workspace authority.

**Why this priority**: Running workspace content is useful only if it preserves
the boundary Hideout promises. A compatibility path that silently broadens host
authority is a product failure.

**Independent Test**: Run executable content from two disjoint workspaces in
concurrent sessions, attempt traversal and stale-attachment cases, and verify
that each target sees and executes only its own exact workspace attachment and
that all outside-workspace access still requires existing typed authority.

**Acceptance Scenarios**:

1. **Given** two concurrent sessions for disjoint workspaces in a compatible
   shared environment, **When** each invokes a same-named workspace tool,
   **Then** each executes only the file from its own attachment.
2. **Given** an executable path outside the selected workspace, **When** a
   target attempts to reach it through the workspace execution path, **Then**
   the attempt is denied or remains subject to the existing explicit authority.
3. **Given** a released, replaced, or mismatched workspace attachment, **When**
   execution is attempted, **Then** it fails closed before running stale or
   differently owned content.

---

### User Story 3 - Receive Actionable Compatibility Failures (Priority: P2)

As an operator, when a workspace file cannot run because it is not executable,
is built for the wrong operating system or architecture, lacks its declared
interpreter, or the selected backend cannot support the capability, I receive a
stable explanation and recovery direction instead of an unexplained low-level
I/O error.

**Why this priority**: Some project files are inherently incompatible with the
guest. Clear classification prevents compatibility limits from being mistaken
for data corruption or a broken isolation boundary.

**Independent Test**: Exercise representative permission, format,
interpreter, stale-attachment, and backend-capability failures and verify that
human and structured status preserve the true class without leaking hidden
host paths.

**Acceptance Scenarios**:

1. **Given** a non-executable file, **When** direct execution is attempted,
   **Then** the operator sees a permission-class failure and the file is not run.
2. **Given** a host-platform binary that is incompatible with the guest,
   **When** execution is attempted, **Then** the operator sees an
   incompatibility-class failure and no host fallback occurs.
3. **Given** a backend or attachment outside the promoted shared Workspace
   Portal scope, **When** an operator consults support documentation or retained
   evidence, **Then** it is explicitly non-claimed and Hideout does not silently
   substitute a copied workspace or native host execution.

---

### User Story 4 - Keep Normal Workspace Semantics (Priority: P2)

As a developer, executing project-local tools preserves the selected checkout's
ordinary read/write behavior, file identity expectations, and version-control
visibility, so generated files and edits appear where the operator expects and
no hidden divergent project copy becomes the source of truth.

**Why this priority**: A tool that runs against a stale or hidden copy may report
success while leaving the real checkout unchanged, which is worse than an
explicit compatibility failure.

**Independent Test**: Have a workspace executable read, create, modify, rename,
and remove project files, then verify the selected host checkout observes the
same results and that a later session observes the updated content.

**Acceptance Scenarios**:

1. **Given** a workspace executable that writes project files, **When** it
   completes, **Then** the changes are visible in the selected host checkout.
2. **Given** one session changes an executable or its inputs, **When** a later
   compatible session starts, **Then** it observes the current checkout rather
   than a stale hidden copy.

### Edge Cases

- The executable bit is added or removed while a session is active.
- A script uses a relative or environment-based interpreter declaration.
- A launcher is a relative symlink within the workspace, an absolute symlink,
  a dangling symlink, or attempts to escape the selected root.
- The target executable is replaced between lookup and execution.
- Two sessions execute and modify different tools in the same workspace.
- Two disjoint workspaces contain the same relative executable path.
- The selected workspace contains a binary for macOS, the wrong guest
  architecture, or an unsupported executable format.
- The declared interpreter is missing or lacks execute permission.
- The workspace path contains spaces, non-ASCII characters, or a deeply nested
  launcher hierarchy.
- The backend supports ordinary workspace reads and writes but cannot prove
  direct execution semantics.
- The daemon or guest attachment process exits while execution is starting.
- A project contains a very large dependency tree; the solution must not copy
  the full workspace on every run or create an unbounded retained duplicate.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Workspace, backend, UI/daemon diagnostics, and runtime
  lifecycle. The feature adds no HostFS, host-open, endpoint, network, profile,
  or script-runtime authority.
- **Fail-closed behavior**: Unsupported backends, stale or mismatched
  attachments, escaping symlinks, incompatible files, missing interpreters, and
  ambiguous execution state stop before a hidden host fallback or broader mount
  is used. A raw mount-level failure alone is not considered an adequate
  operator-facing result.
- **User authority and policy**: Only the already selected workspace is in
  scope. Existing workspace safety checks, explicit high-risk override,
  outside-workspace HostFS policy, deny precedence, and session-scoped authority
  remain unchanged. File execute permission is never added implicitly.
- **Generality and provider scope**: The transport rule is generic to the
  Workspace Portal, while the promoted product claim is limited to compatible
  macOS arm64 Lima shared-mode attachments. Package managers, agents, and build
  systems are examples and test fixtures, not Core product semantics.
- **Evidence surface**: Run summary, audit/status and doctor expose capability
  state and stable failure classes; the real backend gate proves direct
  execution, checkout effects, concurrent isolation, and no fallback.
- **Secret/redaction boundary**: Evidence must not contain hidden physical mount
  paths, operator home paths, workspace content, command output, environment
  secrets, control-plane tokens, or raw target arguments beyond existing local
  audit policy.
- **Backend/gate expectation**: Local mechanics and mutation/negative fixtures
  are Gate 0 support only. Product support requires clean exact-commit real
  macOS arm64 Lima evidence through the existing shared Workspace Portal.
  Native and static/dedicated virtiofs remain unpromoted for workspace-local
  execution and do not inherit this capability.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Hideout MUST support direct execution of guest-compatible files
  with execute permission that reside inside the exact selected workspace on
  the promoted macOS arm64 Lima shared Workspace Portal path.
- **FR-002**: Hideout MUST support workspace-local executable scripts when
  their declared interpreter is available and executable in the guest.
- **FR-003**: Hideout MUST preserve target arguments, environment policy,
  working directory, standard streams, terminal behavior, exit status, and
  signal behavior for workspace-local commands.
- **FR-004**: Workspace-local launchers and relative links MUST resolve against
  the same selected workspace content visible to the session.
- **FR-005**: Execution MUST preserve direct, observable read/write effects in
  the selected host checkout and MUST NOT silently make a stale or divergent
  project copy authoritative.
- **FR-006**: The execution path MUST preserve exact workspace identity,
  attachment incarnation, session ownership, and concurrent disjoint-workspace
  separation.
- **FR-007**: The feature MUST NOT add ambient host execution, implicit HostFS
  grants, broader host mounts, host PATH lookup, or automatic execute permission.
- **FR-008**: Absolute links, traversal, replaced attachments, and other paths
  that cannot be proven to remain within the selected workspace MUST fail
  closed or continue through an already authorized non-workspace capability.
- **FR-009**: The promoted path MUST preserve ordinary Linux permission,
  missing-interpreter, and incompatible-format failures without translating
  them into a Portal `EOPNOTSUPP` failure.
- **FR-010**: Static/dedicated virtiofs and other unproved mechanisms MUST remain
  explicit non-claims with actionable documentation; the implementation MUST
  NOT introduce an automatic copy or host-execution fallback.
- **FR-011**: Human support documentation, retained evidence, and gate output
  MUST agree on the promoted and non-promoted workspace mechanisms.
- **FR-012**: The implementation MUST avoid an unconditional full-workspace copy
  on each run and MUST bound any retained derived data with explicit ownership
  and cleanup behavior.
- **FR-013**: Concurrent sessions MUST be able to execute same-named tools from
  their own workspace attachments without cross-session or cross-workspace
  substitution.
- **FR-014**: Release evidence MUST bind the source commit, package, runtime,
  backend, host platform, workspace mechanism, and resulting execution checks.
- **FR-015**: New gate assertions MUST have mutation proofs, and new evidence
  judges MUST reject at least one negative fixture that previously could appear
  green.
- **FR-016**: Existing supported run, HostFS, lifecycle, shared-workspace,
  package, privacy, and no-host-fallback behavior MUST remain unchanged.

### Key Entities

- **Workspace execution capability**: The observed support state for executing
  guest-compatible files from one exact workspace attachment, including backend
  and workspace-mechanism identity plus a stable unsupported reason.
- **Executable workspace target**: A target path proven to belong to the
  session's selected workspace, with its permission and compatibility outcome;
  it grants no authority beyond that workspace.
- **Execution proof**: Redacted evidence binding the exact product/runtime
  candidate to positive execution, checkout-effect, isolation, failure, and
  performance checks.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: All supported direct script, guest-binary, relative-link, and
  project-local launcher scenarios complete with the expected output and exit
  status on the promoted real backend.
- **SC-002**: A representative project-local test command that previously
  failed solely because its launcher lived in the workspace completes
  successfully in 30 out of 30 clean real-backend samples.
- **SC-003**: At least 95% of warm workspace-executable samples begin producing
  target output within the existing two-second warm-run objective, with no more
  than 10% median regression against an alternating same-candidate control that
  invokes the same workspace script through the guest shell.
- **SC-004**: One hundred concurrent or rapidly repeated executions across at
  least two disjoint workspaces produce zero cross-workspace substitutions,
  stale attachment executions, or leaked outside-workspace paths.
- **SC-005**: Permission, incompatible-format, missing-interpreter,
  stale-attachment, and unsupported-backend negative cases all fail before any
  host fallback or unintended workspace executes, and every asserted failure
  class is observed red under mutation.
- **SC-006**: Workspace changes made by the target are visible in the selected
  host checkout and to a later session in every retained correctness sample.
- **SC-007**: Full Gate 0, race-sensitive package tests, the existing real
  shared-workspace/lifecycle regressions, documentation truth checks, and the
  new exact-commit real execution gate all pass.

## Assumptions

- The target user is the existing professional individual operator on a
  supported macOS arm64 Lima installation.
- "Executable" means a file the Linux guest can ordinarily execute: its mode,
  format, architecture, and interpreter must already be valid. Hideout does not
  translate host binaries or install missing interpreters.
- The selected workspace remains intentionally readable and writable by the
  target; this feature does not add DLP, immutable checkout semantics, or
  protection from malicious workspace content.
- Compatible automatic workspaces may share one guest kernel. Dedicated named
  environments remain the separate trust-domain mechanism and their static
  virtiofs workspace-execution behavior is outside the 041 promotion claim.
- The current exact `/workspace` presentation and session-scoped attachment
  identity remain product requirements even if the underlying workspace
  mechanism changes.
- Detach, guest-root containment, Windows, automatic package installation, and
  arbitrary outside-workspace executable discovery remain out of scope.
