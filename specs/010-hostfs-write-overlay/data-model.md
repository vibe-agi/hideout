# Data Model: HostFS Write Overlay

<!-- markdownlint-disable MD013 -->

## Overlay Write Grant

Explicit HostFS policy rule that permits staged write-class operations.

Fields:

- `id`: stable rule identifier.
- `hostPath`: absolute host path selector.
- `guestPath`: optional guest-facing path reference.
- `scope`: exact file, glob, directory, or recursive directory.
- `ops`: write-class operation set, distinct from read-only ops.
- `source`: profile, environment, or run.
- `reason`: operator-supplied explanation.
- `createdAt` / `expiresAt`: optional lifetime metadata.
- `trustSource`: operator-authored or reviewed non-operator proposal.

Validation:

- deny rules override grants;
- reserved Hideout control-plane roots are never grantable;
- read-only grants do not imply overlay write authority;
- non-operator-authored proposals must pass the trust/review gate before enablement.

## Staged Write Operation

One guest write-class request durably captured before returning guest success.

Fields:

- `id`: unique staged operation id.
- `sessionId`: source run session.
- `profile`: active profile name.
- `backend`: backend name.
- `operation`: create, replace, append, truncate, mkdir, delete, rename, chmod, or chown.
- `requestedPath`: path requested through HostFS.
- `canonicalPath`: canonical host target when safely resolvable.
- `destinationPath`: rename destination when applicable.
- `grantId` / `grantSource`: matched overlay authority.
- `baseSnapshot`: lower host state captured at stage time.
- `newSnapshot`: staged content or metadata target.
- `contentObject`: opaque overlay object id for content operations.
- `preview`: capped diff or summary for review.
- `requestedMode`: staged chmod mode when applicable.
- `requestedOwner` / `requestedGroup`: staged chown IDs when applicable.
- `privilegeStatus`: 009 status at staging time.
- `decisionId`: linked write decision.
- `status`: staged, denied, discarded, applied, conflict, expired, failed.

Rules:

- guest success is returned only after this record and any content object are durably written;
- the host lower layer is unchanged while status is `staged`;
- overlay object paths are control-plane data and never target-facing authority paths.

## Base Snapshot

Host lower-layer identity captured for conflict detection.

Fields:

- `exists`: whether the lower object existed.
- `kind`: file, directory, symlink, missing, or unsupported.
- `device` / `inode`: when available.
- `size`: byte length where applicable.
- `mtime`: high-resolution modification time.
- `mode`: permission and type bits.
- `uid` / `gid`: owner/group where available and relevant.
- `contentHash`: content digest for regular-file content operations.
- `linkTarget`: symlink target when the requested path is a symlink.

Rules:

- apply compares current state against the snapshot;
- second-resolution `mtime` alone is never sufficient;
- missing or ambiguous identity data makes conflict checks conservative.

## Write Decision

Reviewable Manager/Core decision for exactly one staged operation in v1.

Fields:

- `id`: decision id.
- `version`: plan version.
- `operationId`: staged operation id.
- `state`: pending, claimed, applied, discarded, expired, conflict, failed.
- `summary`: path, operation, and risk facts for local surfaces.
- `preview`: capped diff or metadata preview.
- `timeoutAt`: time when pending decision denies by default.
- `claim`: current claim lease, if any.
- `privilegeContext`: current and staged 009 status.
- `conflictStatus`: clean, conflict, unknown, unsupported.
- `auditRefs`: local audit references.

Transitions:

```text
pending -> claimed -> applied
pending -> claimed -> discarded
pending -> expired
pending -> conflict
claimed -> conflict
claimed -> failed
```

Rules:

- only one claim can be active;
- apply/discard require decision id, claim token, and expected plan version;
- timeout denies and discards by default.

## Claim Lease

Temporary right for one authenticated local surface to resolve a decision.

Fields:

- `token`: random claim token, never logged.
- `surface`: cli, tui, webui, daemon-client, or manager-client.
- `operator`: local operator identity summary, if available.
- `claimedAt`: time of claim.
- `expiresAt`: claim expiry time.

Rules:

- claim tokens are control-plane secrets;
- stale claim tokens fail closed;
- a different surface may observe but not resolve a claimed decision.

## Apply Result

Outcome of a claimed apply attempt.

Fields:

- `decisionId`.
- `operationId`.
- `decision`: allow or deny.
- `status`: applied, conflict, failed, discarded, expired.
- `changedPaths`: host paths actually changed.
- `conflictReason`: populated for conflict or deny.
- `partialMutationPrevented`: boolean evidence for failure paths.
- `privilegeStatus`: current 009 status at apply.
- `auditRef`.

Rules:

- successful apply changes only the intended host target;
- failed apply leaves no partial host mutation;
- delete/rename/chmod/chown apply only after target identity revalidation.

## Overlay Store

Session-scoped control-plane storage for staged operations.

Fields:

- `root`: session-local overlay directory.
- `objects`: content objects keyed by opaque ids.
- `operations`: staged operation records.
- `decisions`: decision records.
- `locks`: process-safe locks for stage/apply/discard.
- `limits`: maximum preview size, object size, operation count, and age.

Rules:

- store path is hidden from target and evidence surfaces;
- unreadable, unsafe, unlocked, or over-limit store state fails closed;
- session cleanup removes sensitive runtime state but preserves pending overlay
  records and content objects so operator approval can happen after the guest
  run exits;
- apply, discard, timeout, conflict, and failed apply are terminal overlay
  outcomes and clean content objects without removing host-local evidence.

## Privilege Context

009 guest privilege status attached to plan and apply decisions.

Fields:

- `status`: enforced, degraded, or unknown.
- `reason`: status reason.
- `source`: run evidence reference.
- `checkedAt`: time status was observed.

Rules:

- degraded or unknown is not a default write denial;
- degraded or unknown must be visible in every plan/apply surface;
- no exclusive host-mutation-path claim is allowed outside enforced status, and enforced status still is not a broad DLP claim.
