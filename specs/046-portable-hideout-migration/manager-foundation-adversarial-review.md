# Adversarial Review: Migration Manager Foundation

**Batch**: T016–T020 provider boundary, durable operations, claims, secret input,
and shared projections
**Date**: 2026-08-02
**Scope**: `internal/backend/migration.go` and Manager migration foundation

## Fresh-eyes findings

1. Full import capability initially needed an explicit relationship to the
   packaged adoption helper. Capability validation now fails closed when a
   provider advertises full import without a typed helper identity, version,
   guest architecture, and digest. The base `backend.Backend` interface remains
   exactly five methods; migration is an independently discovered ten-method
   optional capability.
2. Destination identity policy is bound to each import operation, not to the
   reusable export bundle. Every imported environment always requests fresh
   Hideout control and backend identity; Safe Clone additionally resets guest
   machine/SSH identity, while Exact Guest Restore records the explicit
   high-risk preservation choice. Consequently one unchanged bundle can seed
   multiple independent destinations without shared mutable bundle state.
3. A claim acquisition can lose the process after writing only a prefix of its
   claim files. Acquisition now preflights every conflict, writes deterministically
   under a process plus filesystem lock, and treats a crash-written prefix owned
   by the same immutable operation as resumable. Release durably changes the
   operation's claim state before deleting claim files, so a crash cannot make
   two operations appear to hold the same resource.
4. Operation replays originally needed a precise immutable boundary. Plan and
   capability digests, base revisions, bundle binding, claim identities, effect
   bindings, identity actions, and warnings are immutable; phase, effect status,
   progress, recovery, decision, and result advance only through validated
   transitions. A commit or rollback decision is one-way and an identical replay
   is a no-op.
5. Secret input must be consumed before invoking fallible code. The handle is
   deleted while holding the store lock, then its replaceable sensitive buffer
   is exposed to exactly one callback and cleared on success, error, or panic.
   Purpose, client, bundle, and stable file identity mismatches consume and clear
   the handle rather than leaving an oracle or reusable credential.
6. Progress and warning text are serializable Manager state, so projection-time
   redaction alone was insufficient. Construction and progress updates now
   redact Hideout secret assignments, URL userinfo, credential assignments, and
   host absolute paths before persistence; validation rejects manually injected
   unredacted text. Public projections omit providers, claim keys, plan digests,
   effect IDs, raw evidence, and error causes.
7. Unknown totals initially risked being rendered like zero remaining work, and
   waiting time risked contaminating throughput. Logical and encoded totals use
   explicit known bits; ETA is emitted only for a completed total or measurable
   logical throughput. Elapsed time combines durable active-work nanoseconds with
   the current active interval, excluding time waiting for confirmation, a
   passphrase, or recovery input.
8. The first public-error implementation treated an empty bundle error code as a
   generic invalid bundle before considering Manager errors. The mapping now
   accepts only registered bundle codes, then evaluates claim, revision,
   decision, secret-input, request, and provider classifications. Causes remain
   available to privileged diagnostics through `Unwrap` but never enter the
   JSON projection.

No unresolved finding remains inside T016–T020. Provider-specific snapshot and
adoption implementations, import/export orchestration, daemon routes, and UI
surfaces remain in their later explicit tasks.

## Red-green and mutation proof

Tests were written against the typed interfaces and state transitions before the
implementations were complete. The following production mutations were then
applied temporarily and each was detected by an unchanged test:

- Removing the requirement that full import advertise an adoption helper made
  `TestMigrationEffectBindingAndCapabilitiesFailClosed` fail because a missing
  helper was accepted.
- Treating an opposite durable commit/rollback decision as an idempotent replay
  made `TestMigrationCommitDecisionIsOneWayAndReplaySafe` fail.
- Removing the plan digest from immutable replay matching made
  `TestMigrationStoreReservesImmutableOperationAndRecoversClaimPrefix` accept a
  stale plan.
- Leaving a secret-input handle in the map during callback execution made
  `TestMigrationSecretInputIsOneShotPurposeClientAndBundleBound` expose a second
  consume as a used sensitive buffer instead of rejecting the capability.
- Disabling URL-userinfo redaction made
  `TestMigrationProjectionIsSharedConcreteAndSecretFree` serialize the sentinel
  proxy password.

All five mutations were removed. The same tests and the complete migration
foundation suite returned green afterward.

## Validation

```text
go test ./internal/backend ./internal/backend/native ./internal/backend/lima
go test ./internal/migration ./internal/manager -run='^TestMigration' -count=1
go vet ./internal/backend ./internal/migration ./internal/manager
git diff --check
```

The tests include concurrent exactly-one claim ownership, interrupted claim
prefix recovery, one-shot concurrent secret consumption, backing-buffer cleanup,
private/corrupt operation-record rejection, monotonic durable progress, explicit
unknown ETA, terminal receipts, audit classification, and credential/path
redaction sentinels.
