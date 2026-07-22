# Adversarial Report: Lifecycle Attach Reservation

<!-- markdownlint-disable MD013 -->

**Feature**: `040-lifecycle-attach-reservation`

**Status**: Implemented and promoted; all required local/model/real evidence
and the final artifact audit are complete.

## Fresh-Eyes Findings

| Severity | Requirement | Finding | Disposition |
| --- | --- | --- | --- |
| Critical | FR-009, FR-010, FR-012; US3/AC3 | Restart reconciliation used the absence of an old guest session to delete a durable stale owner automatically. That turned post-owner restart into destructive recovery instead of the existing fail-closed explicit-recovery contract. | Fixed in `internal/daemon/lifecycle.go`; `TestDaemonRestartRetainsStaleRunningOwnerForExplicitRecovery` retains the owner/runtime and returns `owner-requires-explicit-recovery`. |
| High | FR-010, FR-012; US2/AC1 | After the restart fix, the full pre-checkpoint provider graph remained orphaned even when fresh facts proved every non-owner provider absent. Explicit stop therefore could not co-terminate the one catalog-approved `run.session` orphan. | Fixed in `internal/lifecycle/reconcile.go`; proved-absent residuals are pruned and only stale session owners remain blockers. The daemon test asserts exactly one `run.session` orphan. |
| High | FR-007, FR-008; SC-003 | The real cancellation judge killed a background shell-function wrapper rather than the actual CLI. The child stayed connected and could launch the target, producing a false failure unrelated to server cancellation. | Fixed in `scripts/lib/gate2-resource-lifecycle.sh`; the background PID is now the exact `hideout run` process and cancelling it cancels the daemon request context. |
| Medium | FR-010, FR-013 | Reservation admission surfaced the generic attach-blocked error before Manager could preserve the established stale-owner recovery code. | Fixed with typed `lifecycle.AttachBlockedError`; Manager maps the owner reason to the existing `session.cleanup.failed` recovery path without a duplicate preflight owner scan. |
| Medium | FR-011; SC-005 | Idle-to-warm reservation admission synchronously persisted grace cancellation, adding about 17 ms per registration benchmark operation. | Fixed by cancelling timer authority synchronously in memory and persisting it with the following registration commit. Three 30-iteration benchmark runs fell to 8.788, 8.702, and 8.479 ms/op. |
| Medium | SC-005, SC-006 | The historical pre-036 binary rejects inherited empty environment values, so the shared performance harness could fail before comparison. In addition, using the pre-036 commit for 040 would attribute all post-036 drift to this feature. | Fixed with a deterministic non-empty target environment and explicit `--feature-040` mode. The 040 mode uses exact pre-040 commit `322c3c6cc9561eea21d4ed20ab78172429654c54` and emits only 040 proofs. |

No unresolved implementation defect was found in the final fresh-eyes pass.
Clean source provenance was satisfied by the retained exact evidence snapshot
and is recorded under the real gate.

## Mutation Proofs

Every mutation below was temporary, observed red, restored immediately, and
then included in the green targeted/race suite.

| Broken production invariant | Red command | Observed failure excerpt |
| --- | --- | --- |
| Allow `BeginReconciliation` while a reservation is held. | `go test -count=1 ./internal/lifecycle -run '^TestEstablishmentBlocksNewReconciliationUntilAbort$'` | `reconciliation crossed reservation: started=true err=<nil>` |
| Allow `ForgetEnvironment` while a reservation is held. | `go test -count=1 ./internal/lifecycle -run '^TestEstablishmentBlocksEveryConflictingLifecycleOperation/forget$'` | `forget removed lifecycle state` |
| Make Manager allocation call materializing `session.New`. | `go test -count=1 ./internal/manager -run '^TestApplyRunReservationFailurePublishesNoRuntimeOrTarget$'` | `reservation failure published global session runtime` |
| Promote the reservation before durable owner creation. | `go test -count=1 ./internal/manager -run '^TestApplyRunEstablishmentOrdersReservationLockRuntimeOwnerAndPromotion$'` | `durable live session owner missing before establishment promotion` |
| Disable the run-session cleanup defer. | `go test -count=1 ./internal/manager -run '^TestApplyRunCancellationCleansEveryEstablishmentBoundary$'` | promotion and post-promotion cases retained global runtime |
| Remove the atomic promotion handle insertion. | `go test -count=1 ./internal/lifecycle -run '^TestEstablishmentPrepareAndPromoteAreSingleUse$'` | `promotion was not atomic: reservations=0 handle=false` |

Restored green commands:

```sh
go test -count=1 ./internal/session ./internal/lifecycle ./internal/manager ./internal/daemon
go test -race -count=1 ./internal/lifecycle ./internal/manager ./internal/daemon
```

The latest targeted race run passed all three packages. A final full-gate rerun
is recorded separately below.

## Negative Fixtures

| Judge | Retained negative fixture | Fail-closed result |
| --- | --- | --- |
| Reservation identity validator | `TestEstablishmentRejectsInvalidAndMismatchedIdentity` | Invalid or cross-environment/session identities are rejected before state insertion. |
| Fresh backend observation judge | `TestEstablishmentPrepareRejectsUnknownObservationWithoutRegistration` | Unknown observation cannot prepare or register a runtime. |
| Conflicting lifecycle admission | `TestEstablishmentBlocksEveryConflictingLifecycleOperation` | Reconcile, direct reconcile, stop, forget, idle expiry, and destructive mutation cannot cross a reservation. |
| Stale durable-owner recovery | `TestDaemonRestartRetainsStaleRunningOwnerForExplicitRecovery` | Restart retains the owner/runtime and reports `owner-requires-explicit-recovery`; it does not silently adopt or scrub it. |
| Public status/event schema | `TestEstablishmentStatusAndEventsRedactControlMaterial` and `TestBuildStatusRedactsControlMaterial` | Injected paths, lock names, credentials, PIDs, and raw argv are absent from public output. |
| Randomized-count evidence judge | `.hideout-release-evidence/040-attach-reservation-gate0/logs/040-negative-randomized-schedules.json` | The otherwise valid fixture contains 999 schedules and is rejected by the `>= 1000` assertion. |
| Real-proof registry | `TestProofRegistryCovers040WithoutLettingNotRunSatisfyRealClaims` | `not-run` remains supporting-only and cannot satisfy either real lifecycle or performance proof. |
| Clean real-evidence provenance | Dirty `--probe --feature-040` result retained under `.hideout-release-evidence/040-attach-reservation-real-gate2-dirty-diagnostic` | Diagnostics passed, but no product-hardening manifest or release proof was emitted; only the later clean non-probe run was promoted. |

No negative fixture is represented as passing product evidence.

## Randomized Schedule Evidence

Command:

```sh
go test -race -count=1 ./internal/lifecycle \
  -run '^TestAttachReservationRandomizedSchedules$'
```

- deterministic seed: `103787037` (`36036017 XOR 0x40a77ac`);
- schedules: 1,000;
- suite deadlock bound: 30 seconds; individual reservation waits use a
  one-second context bound;
- schedule families: reservation-first reconciliation, reservation-first
  mutation, mutation-first reservation, cancelled reconciliation wait, and
  two compatible sibling reservations with randomized promote/abort order;
- post-schedule invariants: no reservation, active handle, mutation,
  reconciliation, or stop timer remains; cleanup is environment-scoped.

The retained under-count fixture has SHA-256
`8151c7ed0fde14ce849aca249d9a13b2dff8aefc1c2bf900803f5690d8fc308d`.

The refreshed local mechanics report has SHA-256
`21ef5365d9f9c23ed2f13566073ac75fef705bce554635088e107d5e589db054`;
the model report has SHA-256
`f6c23687d79e0f990382a9e6777c5ce1b2b8e35e25caebd0f3e7398a225e0b49`;
and their schema-valid product-evidence aggregate has SHA-256
`a0e9dadba70a89ce52e8d5255380ade950ce8e4d8ccb5e7ad96a9cdc21d94d6b`.
All are bound to clean evidence commit
`3555c9a9aa83c885c3c8ee29f1d015ee10c1fe73` with `dirty=false`.

## TLC Model Evidence

Exact command:

```sh
scripts/test-formal-models.sh
```

`formal/AttachReservation.tla` completed exhaustive breadth-first checking with
530 generated states, 161 distinct states, depth 14, and zero violations of
`TypeOK`, `EstablishingRuntimeIntact`, `EstablishedIsDurable`,
`ReservationBlocksReconcile`, `WaitersHoldNoLock`,
`LockHolderIsEstablishing`, and `OwnerImpliesRuntime`.

For the negative proof, `ReconcileStart` was temporarily changed from requiring
`reservedRuns = {}` to `TRUE`, and `ReservationBlocksReconcile` was removed from
the configured invariant list so TLC could reach the downstream safety check.
TLC then violated `EstablishingRuntimeIntact` in a seven-state trace after 278
generated/94 distinct states at depth 7. The model and configuration were
restored; the exact command above then passed again.

## Real macOS Arm64 Lima Evidence

The clean non-probe Gate 2 passed against:

- exact candidate source commit:
  `3555c9a9aa83c885c3c8ee29f1d015ee10c1fe73`, `dirty=false`;
- candidate tree: `9708e19b58811c2f1e8f4794db0cfd38cc96391a`;
- exact pre-040 baseline commit:
  `322c3c6cc9561eea21d4ed20ab78172429654c54`, `dirty=false`;
- runtime family/revision: `developer-standard/2026.07.0`;
- runtime artifact SHA-256:
  `79e5d25bfd05c27b4ee7f2ad085d45c15a63aadbe2ab8d1b4ba2c426e1586134`;
- runtime build commit: `c51aeed1121426ef4ef8bef15105780a20bc23aa`,
  `buildDirty=false`.

The candidate is retained by `refs/hideout/evidence/040-candidate`. A bundle
containing the exact snapshot delta and ref is retained as
`source-snapshot-3555c9a9aa83.bundle`, SHA-256
`b934ebaf76a679d90b167d649cea53fcc5c7a659ca02ee12cf060fe9d7e790a9`.
The current branch and index were unchanged by snapshot creation.

The ignored evidence retains the exact private environment/instance/boot
tuple. The public audit identifies environment
`env_20260722t112026z126278514f184d00d89e`; the private boot-identity artifact
has SHA-256
`60022c09a7d5557e8164d95e3b1efaf5e3a41f54b573da8d77e1bcf91b1c9a26`.
The backend instance name and raw boot UUID are intentionally not copied into
the committed report.

Exact promoted command:

```sh
HIDEOUT_036_SHORT_TMPDIR=/tmp \
  scripts/test-lifecycle-lima-e2e.sh --all --require-real --feature-040 \
  --samples 30 --warmups 3 --iterations 100 \
  --out .hideout-release-evidence/040-attach-reservation-real-gate2
```

All 24 lifecycle checks passed, including reconciliation-first wait,
reservation-first exclusion, 100 attach/stop races, cancellation before owner,
restart before owner, post-owner fail-closed restart recovery, sibling
preservation, unknown-stop refusal, retained state, and exact observed stop.
`result.json` has SHA-256
`8b5d52a110182b76e09c8011eda1a49671e27b44bada663069ed95ff3a45f7c7`.

All 30 measured candidate samples were within two seconds. The candidate
median was 413.921 ms, nearest-rank p95 was 538.581 ms, and the exact pre-040
baseline median was 408.800 ms. The observed 5.121 ms delta was within the
20.440 ms harness bound. `logs/performance.json` has SHA-256
`e78bfd4a61e2fd54c1ab0d5607bec5d9a0f75c2ed5b3ed5eb2ae9870aa8ea747`.

The schema-valid product evidence manifest contains only the registered 040
real lifecycle and performance proofs. Its SHA-256 is
`5394bfbd78804b5c2d1861406861584a5573e25ec7a58edb4f044c2b40fccefb`;
each referenced artifact digest was independently recomputed.

## Requirement Audit

| Requirement | Implementation and proof | Result |
| --- | --- | --- |
| FR-001 | `session.Allocate`, `Coordinator.ReserveAttach`, Manager ordering tests | Pass |
| FR-002 | cancellable wait in `establishment.go`; `TestEstablishmentWaitsForEarlierReconciliationWithoutBlockingIt` | Pass |
| FR-003 | reservation blockers in coordinator/reconcile paths; conflict matrix tests | Pass |
| FR-004 | locked record reload plus fresh observation and prepared incarnation; Manager ordering/workspace tests | Pass |
| FR-005 | durable owner before `Promote`; ordering test and mutation proof | Pass |
| FR-006 | single critical-section reservation-to-handle promotion; lifecycle test, TLC, mutation proof | Pass |
| FR-007 | scoped idempotent abort plus Manager cleanup defer; cancellation matrix and sibling tests | Pass |
| FR-008 | caller contexts, bounded cleanup, no transition lock during reconciliation wait | Pass |
| FR-009 | reservations are memory-only and dropped by `Close`; replacement-coordinator and daemon restart tests | Pass |
| FR-010 | typed blocked reasons, unknown-observation rejection, explicit stale-owner recovery | Pass |
| FR-011 | multi-reservation map and independent sibling promote/abort; randomized schedules and clean 30-sample Gate 2 | Pass |
| FR-012 | existing registration/providers and final-session behavior preserved; full clean Gate 0 and real Gate 2 | Pass |
| FR-013 | derived establishing status, stable event/recovery codes, redaction/schema tests | Pass |
| FR-014 | existing CLI/config/manifest contracts unchanged; `New` and legacy planning compatibility retained | Pass |
| FR-015 | mutation table and negative fixtures above | Pass |
| SC-001 | deterministic interleavings, mutation proofs, TLC | Pass |
| SC-002 | 1,000 deterministic randomized schedules under race detection | Pass |
| SC-003 | cancellation at every establishment boundary with sibling snapshots | Pass |
| SC-004 | restart before/after owner plus ambiguous cleanup/stale-owner blockers | Pass |
| SC-005 | 30/30 clean real warm samples within two seconds; p95 538.581 ms | Pass |
| SC-006 | clean exact source/runtime real topology and digest-bound manifest | Pass |
| SC-007 | injected control material absent from status/events/schema/evidence | Pass |

### Acceptance Scenarios

| Scenario | Primary proof | Result |
| --- | --- | --- |
| US1/AC1 reconciliation-first wait | `TestEstablishmentWaitsForEarlierReconciliationWithoutBlockingIt` | Pass |
| US1/AC2 attachable continuation reaches ownership | `TestApplyRunEstablishmentOrdersReservationLockRuntimeOwnerAndPromotion` | Pass |
| US1/AC3 blocked continuation launches nothing | `TestApplyRunReservationFailurePublishesNoRuntimeOrTarget` | Pass |
| US2/AC1 reservation-first blocks destructive work | `TestEstablishmentBlocksEveryConflictingLifecycleOperation` | Pass |
| US2/AC2 mutation-first gives bounded reservation outcome | randomized replay plus transition-lock ordering tests | Pass |
| US2/AC3 compatible runs keep distinct authority | `TestEstablishmentConcurrentReservationsPromoteAndAbortIndependently` | Pass |
| US3/AC1 cancellation removes only caller state | `TestApplyRunCancellationCleansEveryEstablishmentBoundary` | Pass |
| US3/AC2 restart-before-owner uses fresh facts | `TestRestartDropsUnpromotedReservationAndUsesOnlyFreshFacts` | Pass |
| US3/AC3 restart-after-owner uses existing ownership rules | `TestDaemonRestartRetainsStaleRunningOwnerForExplicitRecovery` | Pass |
| US3/AC4 ambiguous cleanup is stable and redacted | blocked-reason, status/event, and schema-redaction tests | Pass |

## Deferred Work Audit

No 040 implementation requirement is intentionally deferred. Existing
unrelated project debt remains in `docs/DEBT.md` with its own trigger and was
not broadened by this feature. The removed attach-reservation debt row is
implemented and locally proved.

No work required by 040 remains deferred. The clean non-probe Gate 2 emitted
both digest-bound real proofs, and the exact candidate remains reproducible
through its evidence ref and retained source bundle.
