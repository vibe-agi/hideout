# Quickstart: Lifecycle Attach Reservation

<!-- markdownlint-disable MD013 -->

## Purpose

Use this sequence to review and validate the 040 implementation. The first
commands prove local mechanics; only the final real-Lima lane can support the
backend/restart/performance claim.

## 1. Inspect The Protocol Artifacts

```sh
sed -n '1,240p' specs/040-lifecycle-attach-reservation/spec.md
sed -n '1,260p' specs/040-lifecycle-attach-reservation/plan.md
sed -n '1,260p' specs/040-lifecycle-attach-reservation/contracts/establishment-protocol.md
```

Confirm that the normative order is allocation, reservation, transition lock,
record/backend revalidation, runtime, durable owner, and promotion. Reject any
path that waits for reconciliation while holding the transition lock.

## 2. Run Targeted Tests

```sh
go test -count=1 ./internal/session ./internal/lifecycle ./internal/manager ./internal/daemon
go test -race -count=1 ./internal/lifecycle ./internal/manager ./internal/daemon
```

The targeted suite must force reconciliation-first, reservation-first,
mutation-first, concurrent compatible runs, cancellation at each establishment
boundary, and restart classification before/after owner durability.

## 3. Run The Lifecycle Gate

```sh
scripts/test-lifecycle-smoke.sh --race \
  --out .hideout-release-evidence/040-attach-reservation-gate0
```

The output must include the 040 mechanics/model/redaction proof entries, at
least 1,000 randomized schedules, and schema-valid artifacts.

## 4. Run Full Gate 0

```sh
scripts/test-gate0.sh
```

Gate 0 must pass build, vet, formatting, all Go tests, TLC models, documentation
truth, schemas, product evidence, UI/status parity, and lifecycle smoke.

## 5. Review Mutation And Negative Evidence

```sh
sed -n '1,260p' specs/040-lifecycle-attach-reservation/adversarial-report.md
```

For each new concurrency assertion, verify the report names the temporary
implementation break, the exact test that turned red, and restoration. For
each judge/gate assertion, verify a retained negative fixture fired.

## 6. Run Real macOS Arm64 Lima Gate 2

```sh
HIDEOUT_036_SHORT_TMPDIR=/tmp \
  scripts/test-lifecycle-lima-e2e.sh --all --require-real --feature-040 \
  --samples 30 --warmups 3 --iterations 100 \
  --out .hideout-release-evidence/040-attach-reservation-real-gate2
```

Accept only clean exact-commit/runtime-bound evidence with reconciliation-first,
reservation-first, cancellation, both restart boundaries, and 30 warm samples.
The nearest-rank p95 and at least 95% of samples must be within 2.0 seconds.

## 7. Completion Audit

```sh
git diff --check
scripts/test-doc-truth-smoke.sh
```

Then verify every task is checked, the convergence pass reports no remaining
work, the cross-artifact analysis has no critical/high issue, the 040 debt row
is retired, and `docs/STATUS.md` identifies local versus real evidence honestly.
