# Contract: Disposable Recovery Observability And Evidence

<!-- markdownlint-disable MD013 -->

## Lifecycle Status

For a retained candidate, derived lifecycle status may expose:

- environment ID;
- activity `disposing` or `blocked`;
- disposal source;
- phase;
- bounded reason code;
- backend cleanup invoked boolean;
- absence observation count;
- record/journal/runtime removed booleans.

It must not expose instance names, record digests, owner/session IDs, filesystem
paths, lock/PID details, backend command lines, target arguments, or credentials.

## Audit And Events

Closed action inventory:

- `lifecycle.disposal-planned`
- `lifecycle.disposal-backend-absent`
- `lifecycle.disposal-blocked`
- `lifecycle.disposal-resumed`
- `lifecycle.disposal-removed`

Each event carries environment ID, source, phase, generation, and optional
bounded reason. Removed is emitted only after both journal and record absence
are confirmed. Blocked uses deny; planned/resumed/backend-absent/removed use
allow only for the transition actually proved.

## Run Result

Existing `environmentDisposition` remains:

- `removed`: backend, runtime, journal, and record removal proved;
- `cleanup-required`: any proof or cleanup step unproved.

Target result remains independent and may be returned alongside either
disposition.

## Product Evidence

The 042 real artifact uses a strict closed schema and requires:

- clean exact source/package/runtime identity;
- `darwin`, `arm64`, `lima`, guest `aarch64`;
- ordinary success and target-failure disposal;
- forced daemon crash and restart at each supported checkpoint;
- exact instance stable absence;
- no record, owner/runtime, journal, coordinator status, or Lima inventory
  residue after success;
- non-disposable, live-owner, unprovable-owner, identity-mismatch, unknown,
  unstable-absence, and malformed-intent negative checks with zero destructive
  calls where destruction is not authorized;
- 30 ordinary residue-free runs;
- `--rm --ephemeral`;
- deterministic redaction.

`not-run`, native, dirty, reduced, hand-edited, open-inventory, or missing-check
evidence cannot satisfy the real claim.

## Mutation And Negative Fixtures

Record at least these red mutations:

- ignore `Disposable`;
- infer authorization from `rm-*`;
- permit a live owner;
- skip record digest/instance match;
- accept one absence sample;
- remove record before journal;
- label cleanup failure removed.

The strict evidence judge must reject unknown fields, dirty identity, missing or
false checks, fewer than 30 ordinary runs, nonzero unauthorized destructive
calls, residue counts, overlong recovery, and absent redaction proof.
