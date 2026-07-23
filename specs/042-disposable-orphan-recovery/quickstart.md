# Quickstart: Disposable Orphan Recovery

<!-- markdownlint-disable MD013 -->

## 1. Validate Local Protocol Mechanics

```sh
go test -count=1 ./internal/environment ./internal/lifecycle \
  ./internal/manager ./internal/daemon ./internal/productevidence
go test -race -count=1 ./internal/lifecycle ./internal/manager ./internal/daemon
scripts/test-disposable-recovery-smoke.sh
```

Expected: authorization, exact identity, owner refusal, stable absence, every
durable crash cut, record/journal convergence, redaction, and strict false-green
evidence fixtures pass without using a real backend.

## 2. Model Check The Protocol

```sh
scripts/test-formal-models.sh
```

Expected: `DisposableRecovery` is included and proves no unauthorized
destruction, no success without stable absence, record-last convergence, and
resumability or explicit blockage at every modeled crash point.

## 3. Run Full Gate 0

```sh
scripts/test-gate0.sh
```

Expected: all existing local contracts plus 042 pass. Gate 0 does not establish
real Lima deletion or restart behavior.

## 4. Run Real Lima Recovery

```sh
scripts/test-disposable-recovery-lima-e2e.sh --require-real \
  --runs 30 \
  --out .hideout-release-evidence/042-disposable-orphan-recovery-real-gate2
```

Expected: a clean exact package proves ordinary and target-failure disposal,
forced daemon crash/restart checkpoints, exact stable absence, zero
record/runtime/journal/backend residue, negative refusal cases, and
`--rm --ephemeral`. The strict Go evaluator accepts the artifact.

## 5. Run Aggregate Lima Regression

```sh
scripts/test-gate2-lima.sh
```

Expected: existing run, lifecycle, network, HostFS, projection, workspace, and
`--rm` assertions remain green. Aggregate success supports regression safety but
does not replace the feature-specific clean 042 artifact.

## 6. Verify The Claim Boundary

Confirm:

- only durable disposable authorization enables automatic destruction;
- ordinary environments and untrusted historical residue remain report/block;
- run results still use removed or cleanup-required;
- target failure remains distinct from cleanup proof;
- no new CLI, profile, manifest, workspace, HostFS, network, or target
  authority was introduced.
