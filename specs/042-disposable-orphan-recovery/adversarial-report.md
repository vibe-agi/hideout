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

Implementation, mutation, model, randomized-schedule, and real-backend evidence
will be appended here as the corresponding tasks complete.
