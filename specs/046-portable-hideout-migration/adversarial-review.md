# Adversarial Review: Migration State Refinement

**Batch**: T009 pure export/adoption transitions
**Date**: 2026-08-02
**Scope**: `internal/migration/state.go` and its formal-inventory wiring

## Fresh-eyes findings

1. The initial formal inventory covered the new TLC configurations but could not
   execute migration Go refinements because `internal/migration` and its tests
   were absent. The package and all five test functions are now inventoried; the
   coupled release count is 17.
2. Destination records use copied maps and value records. A transition cannot
   mutate the caller's prior snapshot, which is important when several imports
   read one sealed bundle concurrently.
3. Guest identity policy is copied into `PlannedPolicy` at the destination's
   import plan step. Validation rejects a later policy change. Export state has
   no destination identity field and cannot pre-reset a bundle.
4. Exact Guest Restore deliberately permits the source guest ID on multiple
   destinations. Safe Clone rejects the source ID and every sibling Safe Clone
   ID. Control and backend IDs are always fresh in both modes.
5. Staging, authority approval, and activation are separate transitions. A
   staged record is never runnable, and approved authority becomes effective
   only during activation.

No unresolved finding remains in this batch. Backend effects, persistence, and
bundle parsing are outside T009 and remain open tasks in `tasks.md`.

## Mutation proof

The implementation was temporarily changed so `AdoptionStageDestination` set
`Runnable` to true. The unchanged refinement test failed at the staging step:

```text
migration state invariant is violated:
destination "host-b" is runnable outside active
```

The mutation was removed and the same test returned green. This proves the
preactivation-runnable assertion observes the guarded implementation rather than
only a fixture.

## Negative fixtures

`TestStateInvariantNegativeFixtures` independently rejects:

- source-content digest mutation;
- publication before sealing;
- sealed-bundle digest mutation;
- identity-policy mutation after planning;
- runnable state before activation; and
- effective authority without destination approval.

The trace tests additionally reject an unstopped source claim, duplicate seal,
invalid-bundle staging, duplicate destination names, and retained visible state
after rollback.

## Validation

```text
go vet ./internal/migration
go test -race ./internal/migration -count=1
go test <all formal-inventory packages> -run <all 17 inventoried tests> -count=1
go run ./cmd/hideout-schema-validate schemas/formal-inventory.schema.json formal/inventory.json
```

All commands passed after the mutation was restored.

## 2026-08-05 release-convergence review: profile application state

### Root finding

The original full-mode contract preserved VM disks and portable profile policy,
but not the persistent application state stored outside the VM beneath a profile.
For tools such as Claude this meant history/configuration could disappear even
though the migration was described as preserving application state. The defect
crossed inventory, encryption, staging, atomic visibility, UX, evidence, and
recovery; adding only another copied directory would not have closed it.

### Implemented boundary

- Full mode now has one authenticated `profile-state` component for every
  selected environment/profile binding. Config-only has none.
- Capture includes only `home/`, `config/`, `data/`, and `browser/`; cache,
  profile machine state, and generated Git/config redirect paths are excluded.
- The deterministic archive rejects traversal, escaping symlinks, hard-link
  aliases, special files, source drift, duplicate/noncanonical entries, and
  logical/working-limit overflow. Plaintext streams directly from the stable
  source snapshot into authenticated bundle records; no plaintext migration
  artifact is published.
- Import creates an exact operation/profile/component owner, revalidates record
  order/offset/digest, and publishes preserved roots only by atomically renaming
  the stage into the freshly identified destination profile. Cleanup proves the
  owner marker before recursive removal.
- The TLA+ adoption model now tracks profile-state staging and visibility
  independently from backend state. Safety and liveness configurations assert
  that profile state is stage-owned, never runnable early, and visible exactly
  with activation; both configurations completed without invariant/property
  violations.

### Negative and mutation proof

The new Go fixtures cover source mutation, hard links, escaping links, special
files, fragmented records, content/owner substitution, wrong-owner cleanup, and
cache/generated-state exclusion. Manifest/plan schemas reject missing full-mode
state, state in config-only mode, and partial import-state triples. CLI, TUI, and
Web fixtures consume the shared plan golden; the browser judge also rejects a
full plan with missing profile state.

Three release-worktree mutation runs proved the key new assertions red before
the original implementation was restored:

1. Removing `home/.gitconfig` from the generated-state exclusion made
   `TestMigrationProfileStateCaptureAndMaterializePreservesApplicationStateOnly`
   fail because the
   source-generated file appeared in the destination archive.
2. Dropping the imported-state binding before profile batch preparation made
   `TestMigrationEnvironmentBatchParticipantAtomicallyPublishesImportedApplicationState`
   fail with `profile state stage owner does not match`; no profile/environment
   publication succeeded.
3. Making `StageDestination` leave `profileStateStaged` false made TLC stop at
   depth 4 with `Invariant ProfileStateOwnedByStage is violated`. Restoring the
   transition completed 3,058,408 generated / 992,088 distinct states with no
   error.

The real-Lima preflight now mutation-tests every independent root, attached,
profile-home/config/data/browser, cache, and generated-state judge. Its reusable
post-export checkpoint is bound to candidate commit/tree/archive, encrypted
bundle bytes/digest, source identities and immutability hashes, and canaries;
payload substitution, bundle substitution, and candidate rebinding are rejected.
The checkpoint path, modes, schema, and macOS Keychain secret must all validate
before a post-export resume.

### Release-gate discovery review

The first source-bound pass was green with 211 explicitly inventoried tests,
but the discovery algorithm derived its package search space from that same
inventory and selected tests only by `Migration`, `Migrate`, or `ConfigOnly` in
the function name. That was circular: a migration test in a new package, or a
generically named safety test in a migration-specific file, could remain
unseen. A repository-wide active-source audit exposed 14 such tests, including
four backend contract tests, three durable/hostile Manager tests, and seven
package/evidence integration tests.

The preflight now derives its search space from `go list ./...`, parses only the
active target's test files, and owns both explicit migration names and complete
migration-specific files plus the dedicated profile-state package. The checked-
in inventory is therefore 225 sorted unique tests across 19 packages. The
current generic-name and previously unlisted-package fixtures are retained as
self-proving drift sentinels: reverting either half of the discovery rule makes
preflight fail before any expensive fuzz, TLA+, package, or VM work begins.

### Attached-disk first-boot fidelity closure

The first package-candidate real-Lima run exposed a destructive gap that the
previous model and gate did not express. Staging wrote `additionalDisks` as a
fresh scalar disk name. On Lima 2.2, a fresh name has no matching filesystem
label; because formatting authority was omitted, Lima's guest bootstrap
defaulted to partitioning and formatting the imported bytes. The gate then
looked for its sentinel under any `/mnt/lima-*` path, so it also failed to prove
that applications retained the authenticated source mount path.

The closure carries filesystem type and source guest path in the authenticated
disk edge and durable operation, emits the fresh destination disk as an object
with explicit `format: false`, and rejects omitted/true formatting authority
before first start. Isolated adoption receives the exact source/destination
mount bindings, replaces only an absent or empty source mount with the exact
destination symlink only after `/proc/<pid>/mountinfo` proves the exact mount
point and filesystem type, and receipts `rebind-attached-disk-mounts`;
conflicting, unmounted, mismatched, or nonempty paths fail closed. The TLA+ and
Go refinement now require an abstract disk-fidelity proof before
verification/activation and clear it on rollback.
The real gate verifies the original source path and independently inspects the
fresh Lima disk entry for `format: false` and the authenticated filesystem type.

Focused source-inventory, staging, runtime, helper, protocol, Manager, and
refinement tests cover default `ext4`, preserved supported filesystems, rejected
`swap`, omitted formatting authority, mapping substitution, idempotent exact
links, kernel mount-type mismatch, and refusal to hide a nonempty path. The
exact signed-package real-Lima run remains the publication proof.

### Validation status

Targeted Go packages, JSON schemas, shell syntax/ShellCheck, all 52 real-Lima
semantic preflight fixtures, and both updated MigrationAdoption TLC
configurations pass in the release worktree. This is implementation evidence,
not public release evidence. The exact clean signed package must still pass the
full source migration gate and real-Lima candidate gate before this section can
be promoted as a release claim; performance remains explicitly unqualified.
