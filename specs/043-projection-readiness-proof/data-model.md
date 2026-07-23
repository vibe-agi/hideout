# Data Model: Projection Readiness Proof

<!-- markdownlint-disable MD013 -->

## Projection Catalog Snapshot

Immutable Manager-owned description of the complete command catalog reviewed
for one run.

| Field | Meaning | Validation |
| --- | --- | --- |
| `schema` | Catalog schema identity | Exact supported value |
| `sessionId` | Owning run session | Valid session ID; exact current session |
| `environmentId` | Owning environment | Valid exact environment ID |
| `sessionSnapshotId` | Immutable policy/configuration snapshot | Supported lower-hex digest |
| `commands` | Sorted projected command entries | Unique exact names; bounded by product catalog limit |
| `dispatcher` | Package-owned dispatcher entry | Exact fixed name and relative path |
| `catalogDigest` | Canonical digest of all prior fields | Lower-hex SHA-256; recomputed by every consumer |

The snapshot is derived only after profile commands, built-in bindings, and
enabled external pack bindings have been merged and validated. It carries no
grant decision, host path, application executable, token, or raw target argv.

## Projected Command Entry

One file expected in the exact session shim directory.

| Field | Meaning | Validation |
| --- | --- | --- |
| `name` | Public command name or alias | Exact registered name grammar; unique |
| `relativePath` | Path below the session shim root | One clean basename; no separators or traversal |
| `sha256` | Expected bytes | Lower-hex SHA-256 |
| `kind` | `dispatcher` or `command` | Closed enum |

The dispatcher is included exactly once. Aliases are explicit entries because
they are separately addressable guest commands.

## Projection Readiness Manifest

Strict session-local JSON written atomically after every catalog entry is
materialized.

| Field | Meaning | Validation |
| --- | --- | --- |
| `schema` | Manifest schema identity | Exact supported value |
| `sessionId` | Owning session | Equals catalog/current session |
| `environmentId` | Owning environment | Equals catalog/current environment |
| `sessionSnapshotId` | Reviewed run snapshot | Equals catalog/current session |
| `catalogDigest` | Complete catalog identity | Recomputed exact match |
| `entries` | Dispatcher plus sorted commands | Exact count/order/content match |

Unknown fields, duplicate entries, trailing JSON, symlinked manifest, wrong
permissions/type, or any mismatch make the manifest invalid. The manifest is a
completion marker and proof input, never authority.

## Projection Readiness Expectation

Backend-neutral immutable value carried by `RunSpec` and rebound by Manager on
the returned session.

| Field | Meaning |
| --- | --- |
| `catalog` | Exact snapshot above |
| `manifestRelativePath` | Fixed bounded path below session root |
| `targetProjected` | Whether target command exactly matches a catalog entry |
| `deadline` | Bounded pre-target visibility window |

The backend may prove this expectation or refuse it. It cannot replace or
weaken it.

## Projection Readiness Observation

One authoritative result from the exact guest session view.

| Field | Meaning | Validation/redaction |
| --- | --- | --- |
| `status` | `ready`, `refused`, `timed-out`, `cancelled` | Closed enum |
| `reasonCode` | Stable readiness reason | Closed enum; empty only for `ready` |
| `catalogDigest` | Observed catalog identity | Public digest only |
| `expectedEntries` | Expected count | Non-negative bounded integer |
| `observedEntries` | Validated count | Cannot exceed expected |
| `durationMs` | Pre-target proof duration | Non-negative bounded integer |
| `targetProjected` | Target classification | Copied from reviewed expectation |

No paths, session IDs, tokens, host-app private state, application identity
details, or raw argv are exported in public evidence.

### Reason Codes

- `projection.readiness.manifest-missing`
- `projection.readiness.timeout`
- `projection.readiness.cancelled`
- `projection.readiness.catalog-drift`
- `projection.readiness.identity-drift`
- `projection.readiness.entry-missing`
- `projection.readiness.entry-invalid`
- `projection.readiness.entry-digest-mismatch`

Only missing/not-yet-visible regular files are retryable. Structural,
identity, symlink, and digest failures refuse immediately.

### State Transitions

```text
planned
  -> materializing
  -> manifest-written
  -> session-view-bound
  -> observing
       -> ready
       -> refused
       -> timed-out
       -> cancelled

ready -> manager-validated -> supervisor-committed -> target-started
```

No transition from a terminal failure reaches `supervisor-committed`.
Cancellation before commit is immediate; cancellation after commit follows the
existing target termination protocol.

## 030 Debt Disposition

| Field | Meaning |
| --- | --- |
| `item` | Stable historical observation name |
| `status` | `closed` or `deferred` |
| `proof` | Named current test/evidence |
| `mutation` | Observed red implementation mutation, when assertion is new |
| `remainingGap` | Exact gap when deferred |
| `trigger` | Concrete due condition when deferred |

All four items must appear exactly once before the aggregate debt row changes.

## Projection Release Evidence

Strict artifact produced from one clean exact candidate.

| Group | Required contents |
| --- | --- |
| Candidate | Full source commit, dirty false, exact package digest, runtime package/build/digest |
| Platform | macOS arm64 host, Lima backend, aarch64 guest, observed application identity class |
| Methodology | Fresh/warm sample minima, concurrency shape, p95 method and thresholds |
| Readiness | Closed check map, exact sample artifact, counts, p95, timeouts, retries, fallbacks |
| Projection flows | Closed 030 built-in, 032 external, and 039 persistent-grant checks |
| Privacy | Matching Gate 3 checks or explicit `not-promoted`; never inferred from Gate 2 |
| Redaction | Closed public artifact scan and retained non-claims |

The evaluator recomputes counts and nearest-rank p95 from raw samples, validates
the exact artifact path/digest inventory, and rejects unknown fields.
