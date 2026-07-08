# Quickstart: HostFS Write Overlay Validation

<!-- markdownlint-disable MD013 -->

## Prerequisites

- Go toolchain available.
- Lima available for real HostFS proof on macOS.
- Existing `hideout-hostfsd` build path works, or the test builds a temporary Linux guest daemon.

## Scenario 1: Existing Read-Only HostFS Does Not Regress

Requirements: FR-001, FR-002, SC-009.

Run:

```bash
go test ./internal/hostfs ./internal/broker ./internal/manager
```

Expected:

- existing stat/read/list tests pass;
- read-only grants do not imply write authority.
- overlay write authority must be configured separately from read-only HostFS grants.

## Scenario 2: Supported Operations Stage Without Host Mutation

Requirements: FR-003, FR-006, FR-007, FR-008, FR-009, FR-025, SC-001, SC-012.

Run targeted unit tests that stage:

- create file;
- replace file;
- append;
- truncate;
- mkdir;
- delete;
- rename;
- chmod;
- constrained chown.

Expected:

- each guest-visible success has a durable staged operation;
- staged records include operation kind, path facts, policy source, base metadata, preview, and decision status;
- plan/review surfaces expose the pending decision for the staged operation;
- guest reads reflect overlay state;
- host lower files are unchanged before apply.

## Scenario 3: Unsupported And Ungranted Writes Deny

Requirements: FR-004, FR-005, FR-023, SC-003.

Run tests for:

- missing overlay grant;
- matching deny rule;
- reserved store/credential root;
- symlink or hardlink creation;
- xattr/ACL/special-file requests;
- unsafe overlay store.

Expected:

- every case fails closed before staging or host mutation;
- audit records the safe reason.

## Scenario 4: Multi-Surface Claim And Timeout

Requirements: FR-010, FR-011, FR-012, FR-013, SC-002, SC-006, SC-007.

Run manager/daemon tests that:

1. create a pending decision;
2. observe it from two authenticated clients;
3. claim from one client;
4. prove the second client cannot resolve it;
5. let another decision time out.

Expected:

- only the claimant can apply/discard;
- no host filesystem mutation occurs before authenticated claimant apply;
- timeout denies, discards staged artifacts, and emits `approval-timeout`.

## Scenario 5: Apply Revalidates And Prevents Partial Mutation

Requirements: FR-014, FR-015, FR-016, FR-017, SC-004, SC-005.

Run tests that mutate lower host state between stage and apply:

- content changed within one timestamp second;
- symlink swapped;
- rename destination appeared;
- delete target identity changed;
- chmod/chown target changed.

Expected:

- apply fails closed with conflict evidence;
- host remains unchanged except in clean allow cases;
- no partial content, rename, delete, chmod, or chown remains after failure.

## Scenario 6: Chown Constraints

Requirements: FR-026, SC-013.

Run tests for:

- plan-captured and safely resolvable owner/group IDs;
- unknown owner/group IDs;
- changed owner/group request after staging;
- privilege-requiring owner/group changes.

Expected:

- only safe plan-captured owner/group targets can apply;
- every other chown target fails closed.

## Scenario 7: 009 Privilege Status Non-Claims

Requirements: FR-018, SC-008.

Run plan/apply fixtures with privilege status:

- enforced;
- degraded;
- unknown.

Expected:

- every plan/apply surface shows status;
- degraded/unknown outputs avoid exclusive host-mutation-path claims.

## Scenario 8: JavaScript Proposal Is Proposal-Only

Requirements: FR-019.

Run command-adapter tests where JavaScript proposes `host.fs.write.plan`.

Expected:

- proposal can create reviewable intent only through Go validation;
- script cannot stage, approve, apply, discard, or mutate host files.

## Scenario 9: Export And Redaction

Requirements: FR-020, FR-021, SC-011.

Run export fixtures containing HostFS write evidence with token-shaped values, claim-token-like values, overlay object paths, user paths, and previews.

Expected:

- control-plane material and overlay object paths are stripped;
- user-data export policy controls path/preview sharing;
- evidence remains structurally valid.

## Scenario 10: Local HostFS Write Overlay Smoke

Requirements: FR-001 through FR-026, SC-001 through SC-013 local contract coverage.

Run:

```bash
scripts/test-hostfs-write-overlay-smoke.sh
```

Expected:

- schemas validate;
- targeted HostFS, broker, Manager, daemon, liveconsole, export, audit,
  session, and app tests pass;
- Linux hostfsd cross-compile test passes.

## Scenario 11: Real Lima HostFS Write Overlay

Requirements: SC-010.

Run:

```bash
scripts/test-gate2-lima.sh
```

Expected on real Lima:

- guest stages at least one write through HostFS;
- host lower file is unchanged before apply;
- authenticated apply changes only the intended host path;
- deny/conflict coverage remains covered by unit tests and Gate 2 read-deny
  checks; Gate 2 proves the real guest FUSE staging/apply path.

## Scenario 12: Gate 0

Requirements: FR-024 and static contract coverage.

Run:

```bash
scripts/test-gate0.sh
```

Expected:

- schemas validate;
- HostFS write overlay smoke is wired into Gate 0 where appropriate;
- docs/status/test-plan checks pass.

## Scenario 13: Cleanup And Restart Deny Unresolved Staging

Requirements: FR-022.

Run tests that leave staged decisions unresolved across:

- timeout;
- explicit discard;
- failed apply;
- session termination before approval.
- daemon restart.

Expected:

- session termination removes sensitive runtime state but does not approve or
  delete pending HostFS write review material;
- daemon restart preserves pending review material without claiming ownership
  or granting authority;
- timeout, discard, conflict, failed apply, and successful apply produce
  terminal outcomes and remove content objects;
- cleanup evidence is emitted without leaking overlay object paths.
