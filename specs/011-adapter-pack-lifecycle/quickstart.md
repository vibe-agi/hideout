# Quickstart: Adapter Pack Lifecycle And Local Registry

<!-- markdownlint-disable MD013 -->

This guide defines validation scenarios for 011. Command names may be adjusted
during implementation, but the outcomes must remain equivalent.

## Prerequisites

- Fresh temporary Hideout store.
- One default profile.
- A local valid adapter pack fixture.
- A local invalid adapter pack fixture.
- A local git repository fixture with an exact commit.

## Scenario 1: Install Does Not Grant Authority

Requirements: FR-001, FR-003, FR-004, SC-001, SC-005.

1. Install a valid local pack.
2. List registry entries.
3. Inspect the default profile.
4. Run a command that the pack could own.

Expected:

- registry contains the pack and lock evidence;
- profile has no enable binding;
- command routing is unchanged or denied according to pre-existing profile
  policy;
- audit records install evidence only.

## Scenario 2: Enable Pins Exact Revision

Requirements: FR-004, FR-006, FR-012, FR-016, SC-005, SC-009.

1. Install and test a valid pack.
2. Enable one adapter for one profile with a specific revision id.
3. Upgrade the pack to a new exact commit or modified local candidate.
4. Run the original command before re-enable.

Expected:

- profile binding records `packId`, `revisionId`, adapter id, commands, and
  capabilities;
- the profile keeps using the old pinned revision;
- the new revision is visible as a candidate;
- explicit re-enable is required before behavior changes.

## Scenario 3: Core Validation Beats Pack Tests

Requirements: FR-008, FR-009, FR-010, FR-011, SC-004, SC-006.

1. Install a pack whose self-authored tests pass.
2. Make the manifest or adapter output request unsupported authority.
3. Add fixtures that time out or throw exceptions.
4. Attempt to enable the pack.

Expected:

- enablement fails closed;
- failure reason cites Core validation, not test failure;
- timeout and exception fixtures fail closed;
- profile authority is unchanged;
- evidence distinguishes pack test status from Core validation status.

## Scenario 4: Digest Drift Fails Closed

Requirements: FR-007, FR-017, FR-022, SC-003.

1. Install, test, and enable a local pack.
2. Modify the installed source or locked artifact bytes.
3. Run a command owned by that pack.

Expected:

- runtime compile detects digest mismatch;
- command routing fails closed before adapter execution;
- local audit records mismatch evidence;
- no fallback to unmediated command behavior occurs.

## Scenario 5: Disable And Revoke

Requirements: FR-014, FR-015, FR-017, SC-008.

1. Disable a pack for one profile.
2. Verify the registry entry remains installed.
3. Re-enable the profile binding.
4. Revoke the pack store-wide.
5. Run the command again.

Expected:

- disable stops runtime routing for that profile only;
- revoke stops runtime routing for every profile reference;
- both state changes emit evidence;
- revoked references fail closed until resolved or removed.

## Scenario 6: Git Source Rules

Requirements: FR-001, FR-002, SC-002.

1. Install from an exact commit.
2. Attempt install from a branch or tag.
3. Attempt install requiring recursive submodule trust.
4. Attempt install with local hook/filter configuration that would change the
   checkout outside the pinned tree digest.

Expected:

- exact commit succeeds and records commit evidence;
- floating or ambiguous refs fail before lock;
- recursive submodule trust is not enabled by default;
- hook/filter configuration is not accepted as authority and cannot replace the
  digest lock;
- missing system git produces a clear local prerequisite error.

## Scenario 7: Built-In Metadata

Requirements: FR-005, FR-020, SC-007, SC-011.

1. List built-in adapter metadata.
2. Run built-in adapter tests or inspection.
3. Attempt to mutate built-in metadata as a registry artifact.

Expected:

- built-in root-sensitive metadata is visible;
- non-claim wording is preserved;
- mutation is rejected;
- existing 008 built-in runtime behavior still passes.

## Scenario 8: Export And Redaction

Requirements: FR-017, FR-018, SC-010.

1. Produce lifecycle evidence for install, test, enable, disable, revoke, and a
   digest mismatch.
2. Export the relevant audit slice or profile evidence.

Expected:

- exported artifact includes pack identity, revision, digest summary, lifecycle
  state, and profile binding references;
- control-plane secrets and hidden store paths are stripped;
- pack-authored user-facing text is preserved only according to export/share
  redaction rules.

## Scenario 9: Write Failure Leaves Authority Unchanged

Requirements: FR-022.

1. Attempt install, test evidence recording, audit evidence recording, and
   profile enable with injected registry/profile/audit write failures.
2. Inspect the registry, profile, and audit evidence after each failure.

Expected:

- no partial registry or profile authority is left behind;
- runtime routing remains unchanged;
- failure evidence is emitted when the evidence path itself is available;
- missing evidence capacity fails closed rather than treating the operation as
  successful.

## Final Battery

Before 011 is complete:

```bash
go build ./...
go vet ./...
gofmt -l internal cmd
git diff --check
go test ./...
scripts/test-gate0.sh
scripts/test-adapter-pack-smoke.sh
npx --yes markdownlint-cli2 README.md 'docs/**/*.md' 'specs/011-adapter-pack-lifecycle/**/*.md'
```

Real Lima Gate 2/Gate 3 is not required for 011 unless implementation changes
guest backend, HostFS data plane, DNS/network, or privilege separation claims.
