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

Further model, randomized-schedule, and real-backend evidence will be appended
as the corresponding tasks complete.

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
