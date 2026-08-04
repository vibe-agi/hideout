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
