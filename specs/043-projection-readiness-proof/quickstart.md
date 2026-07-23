# Quickstart: Projection Readiness Proof

<!-- markdownlint-disable MD013 -->

## Prerequisites

- Go 1.25 toolchain
- repository dependencies used by Gate 0
- for real promotion: macOS arm64, Lima, the declared
  `developer-standard` runtime, signed supported host application, and packaged
  aarch64 Linux helpers
- for privacy promotion: the existing Gate 3 proxy/resolver prerequisites

## 1. Baseline

Before implementation, retain the focused baseline:

```sh
go test ./internal/cmdproxy ./internal/broker ./internal/hostcap \
  ./internal/profiletemplate ./internal/backend/lima \
  ./internal/manager ./internal/daemon ./internal/productevidence
```

Record the known gap without claiming a deterministic local reproduction: the
complete Manager catalog is not part of the daemon supervisor's existing
runtime prerequisites, and the capability descriptor does not satisfy its
public schema.

## 2. Focused Gate 0

```sh
scripts/test-projection-readiness-smoke.sh
go test -race -count=1 \
  ./internal/backend/lima ./internal/manager ./internal/daemon
```

Expected:

- final built-in/external catalog is present in the manifest;
- exact guest session view validates all entries before ready/commit;
- timeout, cancellation, identity/catalog drift, symlink, and digest failures
  launch no target or host effect;
- ordinary guest commands keep existing lookup behavior;
- the four 030 debt observations have direct current dispositions;
- descriptor and intent values satisfy strict public schemas.

## 3. Mutation Proofs

For each new assertion, temporarily weaken exactly one implementation guard,
run its narrow negative fixture, observe red, restore, and rerun green.

Required mutations include:

- omit one projected command from the expectation;
- write the manifest before the last entry;
- accept symlink or wrong digest;
- accept a ready proof with another catalog digest;
- omit broker exact registry lookup;
- stop asserting one template alias;
- remove pathMode from recreate identity;
- omit `residualPolicy` from descriptor schema parity.

Record commands and observed failures in `adversarial-report.md`.

## 4. Evidence Judge

```sh
go test -count=1 ./internal/productevidence \
  -run 'ProjectionReadiness|HostAppPack|ProofRegistry'
go test -count=1 ./internal/releasecompat
```

Expected: valid strict fixtures pass; dirty, mismatched, reduced, incomplete,
edited, unknown-field, and `not-run` fixtures fail for the intended reason.

## 5. Full Local Gate

```sh
scripts/test-gate0.sh
```

Gate 0 proves mechanics and contracts only. It does not prove guest visibility
or a real host application effect.

## 6. Clean Exact-Package Real Gate 2

Run only from a clean source candidate:

```sh
scripts/test-projection-readiness-lima-e2e.sh --require-real \
  --fresh 10 --warm 30 \
  --out .hideout-release-evidence/043-projection-readiness-real-gate2
```

Expected retained result:

- 10/10 fresh first targets succeed without retry;
- 30/30 warm new sessions succeed without retry;
- concurrent disjoint catalogs remain isolated;
- readiness p95 is at most two seconds;
- built-in safe projection, 032 external pack, and 039 durable grant/revoke
  flows pass;
- target retries, ambient fallbacks, unauthorized host effects, timeouts, and
  residue are zero;
- package, runtime, host, guest, artifact, and redaction identities are exact.

Then run the aggregate regression gate against the same candidate:

```sh
scripts/test-gate2-lima.sh
```

## 7. Conditional Clean Privacy Gate

When the supported privacy prerequisites are present:

```sh
scripts/test-gate3-hidden-proxy.sh
```

Retain the matching package/runtime identity and 043 privacy artifact. If this
gate cannot run, do not promote clean alias privacy; retain that limitation in
status and claim documents.

## 8. Final Convergence

```sh
markdownlint-cli2 'specs/043-projection-readiness-proof/**/*.md'
scripts/test-doc-truth-smoke.sh
git diff --check
```

Review every FR, SC, and acceptance scenario against implementation and
retained evidence. Update `docs/DEBT.md` item by item and preserve any remaining
triggered debt or non-claim explicitly.
