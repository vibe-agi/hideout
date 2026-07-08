# Research: HostFS Write Overlay

<!-- markdownlint-disable MD013 -->

## Decision 1: Guest Write Success Means Durable Staging

**Decision**: A guest write-class syscall returns success only after Hideout durably records the staged operation. Operator approval controls later host apply and does not retroactively change the already completed guest-visible result.

**Rationale**: The product model is a writable overlay view: tools should see their edits inside the guest while the host lower layer remains unchanged. Blocking every guest write on approval would turn ordinary editor and agent workflows into prompt-driven I/O and would make the overlay behave unlike a filesystem.

**Alternatives considered**:

- Block guest syscalls until approval: stronger immediate feedback to the tool, but poor UX and inconsistent with overlay semantics.
- Hybrid block only metadata operations: creates inconsistent guest behavior and more FUSE edge cases without adding host safety, because host mutation is still apply-gated.

## Decision 2: One Staged Operation Per Decision In V1

**Decision**: V1 creates one write decision per staged guest operation. UI surfaces may group pending decisions visually, but Manager apply/discard resolves exactly one operation per claim.

**Rationale**: The spec requires no partial host mutations for create, replace, append, truncate, mkdir, delete, rename, chmod, and chown. A true multi-operation transaction across all of those operations needs rollback and ordering semantics that are larger than 010. Single-operation decisions give clear conflict checks, simple claims, and precise audit.

**Alternatives considered**:

- Batch all writes in a session: better review ergonomics, but hard to guarantee no partial state across mixed operations.
- Batch per path: still creates rename/delete/metadata ordering ambiguity.

## Decision 3: Overlay Grants Are Separate From Read Grants

**Decision**: Existing `read`, `stat`, `list`, `dir`, and `tree` grants remain read-only. 010 adds explicit overlay write authority using the HostFS rule model and new write-class operations.

**Rationale**: `internal/hostfs/hostfs.go` already treats read/list/stat ops distinctly and has write op names reserved (`OpWrite`, `OpCreate`, `OpDelete`, `OpRename`), while `internal/hostfs/service.go` currently returns `ErrUnsupported` for writes. Reusing the rule model preserves deny precedence, reserved-root filtering, run/profile composition, and Manager profile HostFS plan/apply semantics.

**Alternatives considered**:

- Interpret directory read grants as write grants: violates the spec and would silently broaden host authority.
- Add a separate non-HostFS permission system: duplicates policy and reserved-root logic.

## Decision 4: V1 Operation Set And Explicit Refusals

**Decision**: V1 supports staged create file, replace file, append, truncate, mkdir, delete, rename, chmod, and constrained chown. It rejects symlink creation, hardlink creation, xattr mutation, ACL mutation, device nodes, FIFOs, and socket-file creation.

**Rationale**: The user explicitly asked for the broad ordinary file operation set. The refused operations either create path-confusion risk, cross-platform metadata complexity, or special kernel object behavior that needs a separate threat model.

**Alternatives considered**:

- Limit v1 to create/replace only: safer but no longer matches the requested scope.
- Include symlink/hardlink/xattr/ACL: increases attack surface and platform divergence before ordinary file writes are proven.

## Decision 5: Conflict Detection Uses Identity Tuple Plus Hash

**Decision**: Stage records a base snapshot with device/inode where available, file type, size, high-resolution modification time, mode, owner/group when relevant, and content hash for regular-file content operations. Apply rechecks the tuple and hash where applicable; second-resolution modification time is insufficient.

**Rationale**: Local probes in `.tmp/008-010-plan.md` found timestamp granularity can hide rapid changes. The current HostFS service already canonicalizes and evaluates symlinks for reads; write apply needs stronger lower-layer identity checks because it mutates the host.

**Alternatives considered**:

- mtime-only: too weak.
- content-hash everything always: safer but expensive for large files and unnecessary for metadata-only operations; use hashes where content semantics require them.

## Decision 6: Symlink Safety Is Lstat-First And Revalidated

**Decision**: Write-class operations inspect the requested path with `lstat`, record explicit symlink facts, and revalidate both requested path and canonical target at apply. Symlink creation is denied. Writes through unsafe symlinks fail closed.

**Rationale**: Existing read authorization checks both requested and resolved paths. Write operations need stricter behavior because a symlink swap between stage and apply could redirect host mutation.

**Alternatives considered**:

- Follow symlinks like ordinary host writes: too easy to redirect after review.
- Deny every symlink component unconditionally: simpler, but too restrictive for read-compatible host trees. The plan keeps strict revalidation and denies unsafe cases.

## Decision 7: Apply Uses Operation-Specific No-Partial Strategy

**Decision**: File content apply uses temp-file plus fsync plus rename where supported. Create/mkdir/delete/rename/chmod/chown perform full preflight revalidation immediately before mutation and emit one apply result for exactly one staged operation. If an operation cannot guarantee the spec's no-partial behavior on a platform, it fails closed.

**Rationale**: The repo already uses temp-file plus rename for profile/export writes (`internal/profile/profile.go`, `internal/export/export.go`). HostFS write apply should use the same local-host pattern for content while treating metadata/path operations conservatively.

**Alternatives considered**:

- Best-effort apply with cleanup: not acceptable for host mutation.
- Full rollback journal for batches: larger than v1 and unnecessary with one-operation decisions.

## Decision 8: Approval Timeout Defaults To Deny And Discard

**Decision**: Pending decisions have a bounded timeout. Expiry denies, discards the staged artifact, emits `approval-timeout`, and leaves the host unchanged. Retained later review is a future opt-in mode.

**Rationale**: The user requested timeout default deny. This also matches the constitution's fail-closed behavior and 006 daemon prompt non-claim: daemon absence or missing prompts cannot become approval.

**Alternatives considered**:

- Keep pending indefinitely: creates authority-bearing staged state with unclear lifecycle.
- Auto-apply on timeout: violates explicit operator authority.

## Decision 9: Daemon/UI Are Surfaces, Manager/Core Own Authority

**Decision**: CLI, TUI, WebUI, daemon clients, and daemon events may display pending decisions and call authenticated Manager plan/claim/apply/discard routes. They do not receive raw host filesystem authority.

**Rationale**: 006 established the daemon as a local control plane that mounts the same Manager API and broadcasts events. 010 reuses that model and avoids introducing a daemon prompt channel that treats missing UI as approval.

**Alternatives considered**:

- Implement approval inside daemon only: would create a second authority path.
- CLI-only approval: simpler but conflicts with the user's multi-surface approval goal.

## Decision 10: Degraded/Unknown 009 Status Is Evidence, Not Default Deny

**Decision**: 010 may stage/apply when 009 reports `degraded` or `unknown`, but every review/apply surface must show the status and must not claim HostFS write overlay is the only host mutation path.

**Rationale**: The operator owns their base image risk. 009 already distinguishes enforced/degraded/unknown. 010 avoids false claims without blocking the user's requested write workflow by default.

**Alternatives considered**:

- Deny all writes unless 009 is enforced: strongest boundary, but not what the user requested and blocks weak/native development paths.
- Ignore 009 status: would overclaim containment in degraded environments.

## Decision 11: Export Evidence Reuses 005 Boundary

**Decision**: HostFS write overlay audit records flow into existing audit/export. Export/share reasserts control-plane redaction and user-data decisions rather than inventing a separate export channel.

**Rationale**: 005 made export/share the boundary for local audit evidence. HostFS write details include user file paths and previews, so local audit may record them, while sharing must pass through export redaction.

**Alternatives considered**:

- Strip all file paths from local audit: weakens operator evidence.
- Add a bespoke HostFS export format: duplicates 005.
