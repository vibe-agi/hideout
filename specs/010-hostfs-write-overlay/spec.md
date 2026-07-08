# Feature Specification: HostFS Write Overlay

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `[010-hostfs-write-overlay]`

**Created**: 2026-07-08

**Status**: Draft

**Input**: User description: "Implement 010 from `.tmp/008-010-plan.md`: add HostFS write overlay so guest writes to workspace-external host paths are staged, reviewed, explicitly approved, conflict-checked, and audited before any host mutation. Support create, replace, append, truncate, mkdir, delete, rename, chmod, and constrained chown. Approval may be resolved from local CLI/TUI/WebUI/Manager surfaces with timeout default deny. Keep JavaScript proposal-only and keep actual apply in Go-owned Manager/Core."

## Clarifications

### Session 2026-07-08

- Q: Which write operations are in v1? -> A: Support create file, replace file, append, truncate, mkdir, delete, rename, chmod, and constrained chown; symlink/hardlink creation, xattr, ACL, device node, FIFO, and socket-file mutation stay out of v1 unless a later clarification promotes them with a separate threat model.
- Q: Where does operator approval happen? -> A: Approval is a typed Manager decision that can be surfaced by authenticated local CLI, TUI, WebUI, or daemon clients; daemon/event streams may broadcast and serialize state, but they do not invent authority or treat missing prompts as approval.
- Q: What happens when no surface approves in time? -> A: Pending approval times out to deny by default, discards content objects, emits `approval-timeout` evidence, and leaves the host unchanged. Session termination or daemon restart may preserve pending review material until timeout or another terminal decision, but they never approve or grant filesystem authority.
- Q: How does 009 degraded or unknown privilege status affect 010? -> A: 010 may still stage and apply through explicit operator authority, but every plan/apply surface must show the status and must not claim HostFS write overlay is the guest's only possible host-mutation path.
- Q: What does a guest write syscall return before operator approval? -> A: It returns success only after the operation is durably staged; approval controls later host apply, and deny/timeout does not retroactively change the completed guest syscall.
- Q: Which chown targets are allowed in v1? -> A: Apply may change owner/group only to IDs captured in the staged plan and safely resolvable on the host without extra privilege; all other chown targets fail closed.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Stage HostFS Writes Without Host Mutation (Priority: P1)

As an operator who grants an untrusted tool limited access to files outside the workspace, I want write-class HostFS operations to land in a staged overlay so the tool can continue its workflow while the real host files remain unchanged until I review the change.

**Why this priority**: This is the MVP. Without a staged overlay, HostFS remains read-only and cannot safely support external-file editing workflows.

**Independent Test**: Grant overlay write authority to a small temporary host tree, perform each supported write-class operation from the guest-visible HostFS path, and verify the guest sees staged results while the host tree remains unchanged before apply.

**Acceptance Scenarios**:

1. **Given** an overlay grant for a host directory, **When** the guest creates, replaces, appends to, or truncates a file under that directory, **Then** Hideout records a durable staged write, returns success to the guest only after staging succeeds, guest reads reflect the staged content, and the host file is unchanged before apply.
2. **Given** an overlay grant for a host directory, **When** the guest creates a directory, deletes a path, renames a path, changes mode, or requests a constrained owner/group change, **Then** Hideout records the staged operation, returns success to the guest only after staging succeeds, and the host filesystem remains unchanged before apply.
3. **Given** a write request that is not covered by an overlay grant, is covered by a deny rule, or targets a reserved Hideout control-plane path, **When** the guest attempts the operation, **Then** Hideout denies before staging and records the denial.

---

### User Story 2 - Review And Resolve Pending Writes From Local Surfaces (Priority: P2)

As an operator using CLI, TUI, WebUI, or daemon-backed workflows, I want pending HostFS writes to appear as explicit decisions with diffs, risk facts, and one-winner approval so multiple surfaces cannot accidentally apply the same host mutation.

**Why this priority**: Staging alone is incomplete. Host mutation must require explicit local authority and must be race-safe across multiple management surfaces.

**Independent Test**: Create a staged write, connect two authenticated local surfaces to the pending decision stream, claim the decision from one surface, and verify only that claimant can apply or discard while the other observes the resolved state.

**Acceptance Scenarios**:

1. **Given** a staged write is pending, **When** authenticated local CLI, TUI, WebUI, or Manager clients observe the decision, **Then** each surface sees the operation kind, path summary, diff or preview, grant source, conflict status, and current guest privilege status.
2. **Given** two authenticated surfaces try to resolve the same pending decision, **When** the first one successfully claims it, **Then** apply or discard requires that claim token and every other surface sees that the decision is no longer claimable by them.
3. **Given** no authenticated surface claims and resolves a pending decision before its timeout, **When** the timeout expires, **Then** Hideout denies by default, discards the staged artifact, emits `approval-timeout` evidence, and leaves the host unchanged.

---

### User Story 3 - Apply Or Discard With Revalidation And Evidence (Priority: P3)

As an operator applying staged writes, I want Hideout to re-check policy, path safety, symlink state, conflicts, metadata validity, and privilege status at apply time so reviewed changes cannot turn into a different host mutation.

**Why this priority**: The security value is in the apply boundary. A stale plan, symlink swap, metadata ambiguity, or partial write would undermine the whole overlay model.

**Independent Test**: Stage changes, mutate the host or symlink state before approval, and verify apply fails closed with no partial host mutation; then apply a clean staged change and verify only the intended host paths change with complete audit evidence.

**Acceptance Scenarios**:

1. **Given** a staged file replacement has a valid claim and no conflict, **When** the operator applies it, **Then** Hideout mutates only the intended host path, records apply evidence, and leaves no partial temp artifact if the write succeeds.
2. **Given** the lower host file, directory identity, path target, grant, symlink resolution, or reserved-root status changed after staging, **When** the claimant applies the decision, **Then** Hideout fails closed, keeps the host unchanged, and records the conflict reason.
3. **Given** the run privilege status is `degraded` or `unknown`, **When** a write decision is reviewed or applied, **Then** Hideout surfaces that status and avoids any claim that HostFS write overlay is the guest's only possible host-mutation path.

---

### Edge Cases

- Existing read-only HostFS grants are present but no overlay write grant exists.
- A profile imports a non-operator-authored HostFS write proposal from a bundle, command adapter, recipe, or project manifest.
- A deny rule overlaps an overlay grant.
- The requested path is under the Hideout store, credential roots, browser profile roots, or another reserved root.
- The requested path contains a symlink, or a symlink is swapped between staging and apply.
- The host file changes within the same timestamp second after staging.
- The source or destination of a rename is deleted, replaced, or moved before apply.
- The destination of a create or rename appears after staging.
- A delete request targets a different inode or file type at apply time.
- A chmod request would set unsafe or unsupported bits.
- A chown request names an owner or group that was not captured in the staged plan, cannot be resolved safely on the host, requires extra host privilege, or would broaden authority outside the approved apply path.
- The overlay store cannot be created, becomes unreadable, exceeds limits, or is interrupted during staging.
- A guest write begins but staging cannot be durably recorded.
- The operator closes every approval surface while a decision is pending.
- The daemon restarts while staged writes are pending.
- The session ends before a pending write is resolved.
- The run has 009 status `degraded` or `unknown`.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: HostFS, brokered guest filesystem access, profile HostFS grants, Manager typed plan/apply, CLI/TUI/WebUI approval surfaces, daemon event/status broadcasting, audit/export evidence, session lifecycle, cleanup, and guest privilege status.
- **Fail-closed behavior**: Hideout denies or stops before host mutation when a write is ungranted, denied by policy, targets a reserved root, uses unsupported operation type, cannot be staged safely, hits an unsafe symlink, has stale claim/version data, times out without approval, conflicts at apply, has invalid metadata, has unsupported chown behavior, loses overlay state, or runs on a backend that cannot support the requested HostFS write contract.
- **User authority and policy**: Existing read grants do not imply write authority. Overlay write grants must be explicit and operator-authored or pass the non-operator trust/review gate before enablement. Deny rules win over grants. JavaScript command adapters may propose `host.fs.write.plan`, but they cannot stage, approve, apply, discard, or execute host filesystem mutation. Apply remains a Go Core and Manager authority.
- **Generality and provider scope**: This is a generic HostFS write overlay for host paths outside the workspace. Editors, agents, package managers, command adapters, and test fixtures may consume it, but none become Core semantics.
- **Evidence surface**: Staged write, pending decision, claim, apply, discard, timeout, conflict, deny, cleanup, and degraded/unknown privilege warnings must be visible through audit and local management surfaces. Export/share evidence must include the same events after deterministic redaction.
- **Secret/redaction boundary**: Broker/UI tokens, `HIDEOUT_SECRET_*` backing values, generated machine IDs, setup credentials, daemon tokens, overlay object store paths, and raw control-plane paths must not be exposed to the target, UI output, audit, logs, or exported artifacts. User file paths and content previews are operator data and may appear in host-local evidence unless export/share redaction removes them.
- **Backend/gate expectation**: Gate 0 covers schemas, policy compilation, validation, redaction, and fail-closed tests. A real Lima HostFS path is required before claiming guest-visible staged writes and apply behavior, because native remains a weak harness and HostFS is a guest/filesystem boundary.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST keep existing HostFS read-only `stat`, `read`, and `list` behavior compatible.
- **FR-002**: System MUST require explicit overlay write grants; existing read grants MUST NOT grant write, metadata, delete, or rename authority.
- **FR-003**: System MUST support staged create file, replace file, append, truncate, mkdir, delete, rename, chmod, and constrained chown operations.
- **FR-004**: System MUST reject symlink creation, hardlink creation, xattr mutation, ACL mutation, device node creation, FIFO creation, and socket-file creation in v1.
- **FR-005**: System MUST deny write-class operations before staging when no overlay grant covers the path, a deny rule matches, a reserved root matches, or path normalization cannot prove the request stays within granted authority.
- **FR-006**: System MUST stage every supported write-class operation in an overlay store and MUST NOT mutate the host filesystem before explicit apply.
- **FR-007**: System MUST make guest reads reflect staged overlay state for the same session while the host lower layer remains unchanged before apply.
- **FR-008**: System MUST record each staged operation with session, profile, operation kind, requested path, canonical target path, grant/rule source, base metadata, new metadata, content hash when applicable, diff or preview, staged artifact identity, and decision status.
- **FR-009**: System MUST provide a typed plan/review surface for pending HostFS write decisions with path summary, operation kind, diff or preview, policy source, conflict status, timeout, and guest privilege status.
- **FR-010**: System MUST allow authenticated local CLI, TUI, WebUI, daemon, or Manager clients to observe pending decisions without giving those clients direct filesystem authority.
- **FR-011**: System MUST use a claim/lease model so only one authenticated claimant can apply or discard a pending decision.
- **FR-012**: System MUST require decision id, claim token, and expected plan version for apply and discard.
- **FR-013**: System MUST default unresolved pending decisions to deny on timeout, discard staged artifacts, leave the host unchanged, and emit `approval-timeout` evidence.
- **FR-014**: System MUST revalidate path, grant, deny rules, symlink resolution, reserved roots, lower-target metadata, conflict state, current guest privilege status, operation validity, rename source and destination invariants, delete target identity, mode constraints, and owner/group constraints at apply time.
- **FR-015**: System MUST detect conflicts using a host identity tuple and content hash where applicable; it MUST NOT rely only on second-resolution modification timestamps.
- **FR-016**: System MUST write host file content through a no-partial apply strategy, using temporary files and atomic rename where the platform supports it, and fail closed without leaving partial host mutations.
- **FR-017**: System MUST apply delete, rename, chmod, and chown only after revalidating that the target identity still matches the staged plan and that the platform can perform the mutation safely.
- **FR-018**: System MUST show `degraded` or `unknown` 009 privilege status in every write plan and apply decision, and MUST NOT claim HostFS write overlay is the only possible host-mutation path in those states.
- **FR-019**: System MUST permit JavaScript command adapters only to propose `host.fs.write.plan`; adapters MUST NOT stage, approve, apply, discard, or execute host filesystem mutation.
- **FR-020**: System MUST audit staged writes, denies, pending decisions, claims, applies, discards, timeouts, conflicts, cleanup, and privilege-status warnings with deterministic control-plane redaction.
- **FR-021**: System MUST include HostFS write overlay evidence in export/share artifacts through the existing export boundary, with control-plane redaction and user-data export decisions applied.
- **FR-022**: System MUST clean up content objects on terminal HostFS write outcomes: timeout, explicit discard, conflict, failed apply, or successful apply. Session termination and daemon restart MUST NOT approve pending decisions, but they MAY preserve pending review material so an authenticated operator can apply or deny it later; overdue preserved decisions MUST default-deny on the next status/worker pass.
- **FR-023**: System MUST fail closed when the overlay store cannot be created, read, written, locked, or bounded safely.
- **FR-024**: System MUST update docs and status so HostFS write overlay claims do not imply workspace write blocking, broad DLP, marketplace trust, guest-root containment, or direct guest-to-host write pass-through.
- **FR-025**: System MUST return success to the guest for a write-class operation only after the requested operation is durably staged; later deny, discard, timeout, or failed host apply MUST NOT retroactively change that completed guest-visible result.
- **FR-026**: System MUST apply chown only when the requested owner/group IDs were captured in the staged plan and can be safely resolved on the host without extra privilege; every other chown target MUST fail closed.

### Key Entities *(include if feature involves data)*

- **Overlay Write Grant**: Explicit operator-authorized HostFS rule that permits staged write-class operations for a bounded host path scope.
- **Staged Write Operation**: One guest write-class request captured in the overlay store, including operation kind, path facts, content or metadata delta, grant source, requested owner/group IDs for chown, and decision status.
- **Base Snapshot**: Apply-time comparison data for the lower host object, including identity tuple, file type, size, high-resolution modification time, mode, owner/group where relevant, and content hash when applicable.
- **Write Decision**: The reviewable plan for one or more staged operations, including diff or preview, risk facts, timeout, privilege status, and resolution state.
- **Claim Lease**: The temporary right held by one authenticated local surface to apply or discard a write decision.
- **Apply Result**: The final outcome of an approved decision, including changed paths, conflicts, partial-write prevention result, and audit reference.
- **Overlay Store**: Session-scoped storage for staged content, whiteouts, metadata, and decision records. It is Hideout control-plane state, not a target-visible authority path.
- **Privilege Context**: The 009 `guest.privilege.status` and reason attached to every write plan and apply decision.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of supported write-class operations in automated tests stage overlay state while leaving host files unchanged before apply.
- **SC-002**: 0 host filesystem mutations occur without an authenticated claimant applying a current decision.
- **SC-003**: 100% of ungranted, denied, reserved-root, unsafe-symlink, unsupported-operation, stale-claim, and timed-out write attempts fail closed with audit evidence.
- **SC-004**: 100% of apply attempts revalidate lower host identity and detect host-side conflicts before mutation.
- **SC-005**: 100% of apply failures tested leave no partial host file content, no partially applied rename/delete/chmod/chown, and no unresolved authority-bearing staged artifact.
- **SC-006**: Multi-surface tests prove that pending decisions are visible to more than one authenticated surface, but only one claim can resolve a decision.
- **SC-007**: Approval timeout tests prove the default outcome is deny, staged artifacts are discarded, and `approval-timeout` evidence is emitted.
- **SC-008**: 100% of write plans and apply decisions in degraded or unknown privilege-status fixtures surface that status and avoid exclusive host-mutation-path claims.
- **SC-009**: Existing HostFS read-only tests and real HostFS smoke paths continue to pass unchanged.
- **SC-010**: Real Lima HostFS evidence demonstrates at least one staged write, one operator apply, one conflict denial, and one reserved-root denial before marking 010 complete.
- **SC-011**: Export/share fixtures containing write overlay evidence pass deterministic control-plane redaction and preserve operator-approved user-data semantics.
- **SC-012**: 100% of guest-visible write successes in tests correspond to durable staged records, and 0 approval denials or timeouts are reported to the guest as if the original write syscall had failed after it already succeeded.
- **SC-013**: Chown tests prove that staged owner/group changes apply only for safely resolvable plan-captured IDs and fail closed for unknown, changed, or privilege-requiring targets.

## Assumptions

- v1 targets HostFS paths outside the workspace. Workspace writes remain the operator's shared collaboration surface and are not blocked by 010.
- Existing HostFS read-only policy, reserved-root checks, brokered access, and go-fuse data plane remain the base architecture.
- Overlay write authority is separate from read authority and uses explicit operator-controlled grants.
- JavaScript adapters from 008 are proposal-only. They may help classify command intent but cannot execute or approve host mutation.
- 009 privilege separation status is available to 010 plans. Degraded or unknown status is allowed with warnings and non-claims; enforced status is stronger evidence but still does not turn HostFS into a broad DLP boundary.
- Pending write decisions are local single-operator decisions. Multi-tenant delegation, organization approvals, and public marketplace trust are out of scope.
- Pending write review may survive session termination or daemon restart so an
  authenticated operator can apply or deny it later. Timeout remains the
  default terminal outcome for overdue unresolved decisions and removes content
  objects.
