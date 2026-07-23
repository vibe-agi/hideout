# Adversarial Report: Disposable Orphan Recovery

<!-- markdownlint-disable MD013 -->

## Baseline

Baseline commit: `a492642` (specification commit; product parent `7377e2a`).

The following focused baseline passed before implementation:

```text
go test ./internal/manager ./internal/daemon ./internal/lifecycle ./internal/environment
```

The green baseline preserves two known gaps:

1. A proved normal-return `--rm` run removes the environment record directly
   from Manager, after finishing its lifecycle registration. The lifecycle
   coordinator is not part of that removal transaction, so the environment can
   converge to zero records while retaining one lifecycle journal identity.
2. Daemon startup can inventory and report a live resource whose owner is
   absent, but generic orphan reporting never supplies destructive authority.
   It therefore cannot recover a disposable backend instance stranded by a
   process or daemon crash.

These are convergence and recovery gaps, not permission to generalize cleanup.
Existing ordinary orphan behavior remains report-only.

## Authority Boundary

Automatic destruction requires a previously persisted, validated disposal
intent whose authority is `run-rm` and whose immutable identity matches an
environment record that was explicitly created with `Disposable=true`.
The intent is written while that trusted record exists and before destructive
backend work begins.

The following shortcuts are explicitly rejected:

- an environment name beginning with `rm-`;
- `StatusError`, `stopped`, `cleanup-required`, or any other mutable status;
- an orphan report or missing live process by itself;
- a lifecycle journal without a valid disposal intent;
- a missing environment record without a valid intent that already binds its
  exact prior record digest, backend, instance name, and generation;
- backend cleanup success without two exact, consecutive absent observations;
- an empty or unreadable owner inventory;
- inferred authority from a target-command failure or from `--ephemeral`.

The persisted ordering is journal-first and record-last:

```text
record + journal
  -> record + validated planned intent
  -> record + backend-absent intent
  -> record + metadata-cleaning intent
  -> remove lifecycle journal/coordinator identity
  -> remove environment record
```

An interrupted implementation may retain `record + intent`, `record-only`, or
no identity. A valid intent-only historical shape is eligible only for
metadata convergence after the exact instance is already proved stably absent;
it cannot authorize another backend cleanup call. A legacy journal-only shape
without the new validated intent remains blocked and is not claimed as
automatically recoverable.

## Evidence Log

### Stable-absence mutation

The stable absence loop was temporarily weakened from two observations to one
and the exact transient-absence test was run:

```text
go test -count=1 \
  -run 'TestRecoverDisposableEnvironmentRetainsBlockedIntentOnCleanupOrProofFailure/transient_absence' \
  -v ./internal/manager
```

The mutation failed red with `unproved recovery returned success`. Restoring
the two-observation requirement returns the test to green. This establishes
that the positive recovery result depends on the stable-absence judge rather
than only cleanup callback success or one transient inventory sample.

### Implementation-red authorization and convergence mutations

The remaining implementation boundaries were each weakened independently,
exercised through the narrow corresponding test, and restored before the next
mutation:

| Temporary implementation mutation | Negative fixture and observed red result |
| --- | --- |
| Ignore the record's `Disposable` bit | `RefusesUnauthorizedOrUnprovedMatrix/non-disposable` failed with `unauthorized recovery succeeded` and a `removed` outcome. |
| Treat an `rm-*` name as authority | `RefusesUnauthorizedOrUnprovedMatrix/name-only` failed with `unauthorized recovery succeeded` and a `removed` outcome. |
| Ignore a live session owner | `RefusesUnauthorizedOrUnprovedMatrix/live-owner` failed with `unauthorized recovery succeeded` and a `removed` outcome. |
| Skip the durable intent's backend, instance, and record-digest match | `RefusesUnauthorizedOrUnprovedMatrix/durable-identity-mismatch` failed with `unauthorized recovery succeeded` and a `removed` outcome. |
| Remove the environment record before lifecycle metadata | `TestApplyRunDisposableLifecycleProtocolRemovesJournalBeforeRecord` failed with `record-last ordering was not observed`; both ordering observations were false. |
| Label a backend cleanup failure as `removed` | `RetainsBlockedIntentOnCleanupOrProofFailure/cleanup_failure` failed with `Status:removed`, `ReasonCode:backend-cleanup-failed`, and the record and journal still retained. |

Every restored narrow fixture passed. Together with the stable-absence and TLA+
mutations, these implementation-red results cover every destructive
authorization, proof, ordering, and disposition assertion required by the
closed 042 evidence contract.

### Closed authority and zero-call matrix

The Manager recovery matrix independently removed or contradicted each proof
and asserted both retained records and zero typed cleanup calls:

```text
go test -count=1 \
  -run 'TestRecoverDisposableEnvironmentRefusesUnauthorizedOrUnprovedMatrix' \
  -v ./internal/manager
```

The passing cases are:

- non-disposable record;
- `rm-*` name without the disposable bit;
- error status without the disposable bit;
- live session owner;
- structurally unprovable owner;
- unknown backend observation;
- observation for a foreign instance;
- durable intent/current-record digest mismatch.

Daemon tests separately prove that valid intent-only residue invokes no backend
cleanup and converges only after two exact absent observations. The existing
legacy journal-only test remains blocked with
`environment-record-unavailable`. Public lifecycle status exposes only the
closed disposal phase/reason fields; record digest and instance identity are
absent, and an injected unregistered `cap_*` reason is rejected.

### Record-last convergence and randomized replay

The ordinary managed-Lima `--rm` finalizer now enters the same durable disposal
transaction as startup recovery. An observing test proves that the environment
record is still present when lifecycle metadata is removed, and that the
journal is absent before Manager removes the record. An injected journal
removal failure leaves both a disposable error record and a blocked intent with
the closed `journal-removal-failed` reason.

The deterministic randomized replay lane used seed `1089767348` and completed
100 schedules spanning restart cuts at every forward phase plus bounded blocked
revalidation:

```text
go test -count=1 \
  -run 'TestCoordinatorDisposalRestartShapes|TestDisposableRecoveryRandomizedCrashReplay' \
  -v ./internal/lifecycle
```

Invariant counts: `complete=100`, `residual-journals=0`, and every schedule
observed the record marker after journal removal before deleting it last.
Separate restart-shape cases cover record+intent, record-only, valid
intent-only, and legacy journal-only. Coordinator startup never manufactures
authority for the legacy shape.

### Formal authorization and convergence model

`formal/DisposableRecovery.tla` models trusted-record admission, persisted
intent, exact owner/identity authority, bounded false absence observations,
every durable crash cut, blocked outcomes, metadata convergence, and
record-last deletion. With `MaxFalse=3`, TLC completed the restored model with
`1629 states generated`, `731 distinct states`, depth `19`, and no invariant
violations.

The `~lastSampleFalse` guard was then removed temporarily. TLC failed red on
`StableAbsenceBeforeMetadata` after two false absent samples against a backend
that remained present, producing a five-state counterexample. Restoring the
guard returned the full model to green. The model is part of
`scripts/test-formal-models.sh`, and the feature smoke requires at least 1,000
generated states, 500 distinct states, and depth 10.

### Strict evidence false-green fixtures

The 042 artifact decoder is closed and the production evaluator, not shell
exit alone, decides whether the real claim is satisfied:

```text
go test -count=1 ./internal/productevidence \
  -run '^Test(ProofRegistryCovers042|DisposableRecoveryValidator)'
```

The negative fixtures independently prove rejection of:

- dirty candidate identity;
- a missing member of the exact 24-check inventory;
- one unauthorized destructive call;
- one retained environment record;
- one retained lifecycle journal;
- 29 rather than 30 ordinary runs;
- 99 rather than 100 crash schedules;
- one recovery timeout; and
- an unknown JSON field.

The retained production manifest was also re-evaluated through the same Go
registry and artifact validator:

```text
HIDEOUT_042_EVIDENCE_DIR=<absolute-evidence-dir> \
  go test -count=1 ./internal/productevidence \
  -run '^TestRetainedDisposableRecoveryEvidencePassesProductionEvaluator$'
```

It passed. A relative evidence path is intentionally not used in this command
because Go executes the package test from the package source directory; the
producer canonicalizes its output directory before invoking the evaluator.

## Defects Exposed By Real Crash Probes

The real producer found three defects that the earlier fake-provider tests did
not expose:

1. Killing the daemon during journal persistence could leave a private
   `.journal-<digits>` temporary beside `journal.json`. Backend deletion and
   absence proof then succeeded, but lifecycle removal failed because the
   directory was non-empty. `JournalStore.Remove` now converges only bounded,
   private, regular temporary files matching the store's own name grammar.
   Unknown, symlinked, non-private, or oversized entries block removal and
   preserve the durable journal. Repeated removal of an already absent
   directory remains idempotent.
2. `--rm --ephemeral` materialized public `identity.json` at the host profile
   root, while Lima mounted only bounded profile subdirectories. A fake test
   inspected the host path and therefore reported a false green even though the
   real guest saw no `/hideout/profile/identity.json`. The same bounded public
   metadata is now projected through `machine/identity.json`, and provisioned
   and per-session views publish an idempotent relative symlink without
   exposing the profile root. The real guest then observed both public identity
   metadata and the independent machine ID.
3. Restart from durable `metadata-cleaning` attempted to persist
   `backend-absent` again, violating the forward-only transition graph.
   Recovery now performs typed backend cleanup only from `planned`, re-proves
   absence at every resumed phase, advances only from the immediate prior
   phase, and makes zero destructive calls if an instance reappears after an
   absence phase.

The regression tests added for these findings are:

```text
go test -count=1 ./internal/profile ./internal/backend/lima \
  ./internal/lifecycle ./internal/manager ./internal/daemon
go test -race -count=1 \
  ./internal/lifecycle ./internal/manager ./internal/daemon
```

They include `TestJournalRemoveConvergesOnlySafeStaleWriteTemps`,
`TestRecoverDisposableEnvironmentResumesDurableForwardPhases`, and
`TestRecoverDisposableEnvironmentDoesNotDeleteInstanceReappearingAfterAbsenceProof`.
The targeted and race suites passed after the fixes.

## Clean Exact-Package Real Gate 2

The promoted candidate was frozen before status/claim documentation changed:

```text
scripts/test-disposable-recovery-lima-e2e.sh --require-real \
  --runs 30 --checkpoints 4 \
  --out .hideout-release-evidence/042-disposable-orphan-recovery-real-gate2-666cfa8
```

Identity:

- source commit:
  `666cfa827646bbc6b0d3d9a86b4f5091b83b5dd3`;
- source dirty: `false`;
- verified package SHA-256:
  `9f5ba6d168471dbbc1bd6fbdf85809483e4bc1ec1685cd5860427f75ad8d78cd`;
- host/backend/guest: `darwin` / `arm64` / `lima` / `aarch64`;
- runtime: `developer-standard/2026.07.0`;
- runtime artifact SHA-256:
  `79e5d25bfd05c27b4ee7f2ad085d45c15a63aadbe2ab8d1b4ba2c426e1586134`;
- runtime build commit:
  `c51aeed1121426ef4ef8bef15105780a20bc23aa`;
- runtime build dirty: `false`.

Observed result:

- 30 ordinary runs, including success, exact target exit 23, and
  `--rm --ephemeral`;
- four actual daemon kills/restarts: after intent, after stable absence/backend
  cleanup, during metadata cleaning, and after journal removal;
- 100 local crash/interleaving schedules;
- all 24 closed checks `true`;
- 34 authorized destructive calls and zero unauthorized calls;
- zero environment records, lifecycle journals, backend instances, gateways,
  runtime receipts, and owner records;
- daemon status p95 `74.320 ms`;
- recovery p95 `498.989 ms`;
- zero recovery timeouts.

Retained public evidence:

- product manifest SHA-256:
  `fc1cabf8eb645433145b72ade45175821c3097792900729c1e9c9231e3dccd16`;
- disposable recovery artifact SHA-256:
  `22b708b4560a6ab454c357f4a99f12a921877f758a01774da09cbe1c543e241f`;
- proof ID: `042.disposable-recovery.real-gate2.recovery`;
- evidence class: `disposable-recovery-real-gate2`;
- redaction status: `passed`.

The reduced probes used while debugging emitted no product proof. Native,
dirty, reduced, `not-run`, command-only, and hand-edited artifacts remain
unable to satisfy the registered real requirement.

## Remaining Non-Claims

- Ordinary/reusable lifecycle orphans remain report-only. There is no generic
  orphan sweep, adoption rule, or missing-record inference.
- Historical journal-only residue without a trustworthy exact 042 disposal
  intent is not automatically recovered. A future migration UX must not infer
  destructive authority.
- 042 adds no CLI/configuration, target-visible authority, workspace copy,
  backend fallback, implicit environment selection, or profile-policy change.
- `--ephemeral` remains session-local identity cleanup; it is not the source of
  disposable environment authority.
- The proof is macOS arm64 Lima specific and establishes no native isolation,
  shared-VM wall, guest-root containment, workspace filtering/DLP, or general
  backend support.

## Full-Gate Convergence

The first full Gate 0 run at clean commit
`b2a2d131b6956717e109dfc4613e4aa3ef983aec` passed all production packages
except `internal/releasecompat`. Registering
`disposable-recovery/v1` had updated the production evidence evaluator, but the
release-readiness test builder that synthesizes every registered semantic
artifact did not yet know how to construct 042 evidence. Its failures were
explicitly `unknown semantic validator "disposable-recovery/v1"`, so 042 could
not silently enter release-candidate aggregation.

`418b1766092a6f2bf5191f77004e49f9d5581f1d` added the closed 24-check,
sample/timing/destructive-call/residue/non-claim fixture to that cross-feature
builder. The exact clean command then passed:

```text
scripts/test-gate0.sh
```

The passing run covered:

- all Go packages and end-to-end test packages;
- seven TLA+ models, including DisposableRecovery at 1629 generated states,
  731 distinct states, and depth 19;
- installation, package, standalone-install, and runtime catalog contracts;
- all 24 documentation files through markdownlint;
- documentation truth and release hardening;
- daemon/session/PTY, concurrent session, shared workspace, 041 executable,
  042 recovery, lifecycle, live-console, decision, adapter, host-app,
  privilege, HostFS, onboarding, doctor, first-run, and UI Gate 0 lanes.

The first aggregate Lima run at that commit reached the new
`--rm --ephemeral` lane and then exited at a direct text grep. The environment
had been removed, but the aggregate expected the optional human line
`Hideout disposable environment removed`. Default quiet runs without a
Boundary Summary intentionally suppress successful summaries; only
`cleanup-required` must always speak. The strict 042 producer already judged
the authoritative structured `env.dispose` audit and inventory, exposing the
aggregate assertion as a false negative rather than a product failure.

`ebb1db151e0ff6ef834e74732e4f036e1786acfb` changed both new aggregate
disposable lanes to require exactly one additional audited `allow/removed`
disposition plus unchanged record/journal/Lima/runtime/identity inventory. It
also strengthened the failed-target lane from “any nonzero” to exact exit 23.
The exact clean command then passed end to end:

```text
scripts/test-gate2-lima.sh
```

The aggregate pass included doctor, real workspace/environment/Git execution,
projection privacy channels, safe/trusted/persistent host-app grants,
HostFS-backed projection, discoverable namespace, live read decisions, write
overlay, no-host-fallback, ephemeral identity, named lifecycle, ordinary
`--rm`, `--rm --ephemeral`, and failed-target `--rm`. Its final output was:

```text
rm_ephemeral_convergence=passed
rm_target_failure_convergence=passed
gate2: passed
```

`bash -n` and `git diff --check` passed for the aggregate correction. The local
environment did not provide `shellcheck`; the repository's full Gate 0 shell
and behavior checks plus the successful real aggregate run are the retained
verification for that script.
