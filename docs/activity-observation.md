# Workload activity observation defaults

<!-- markdownlint-disable MD013 -->

Hideout records bounded metadata for the command after `--` and its
descendants inside the run workload boundary. This document freezes the v1
aggregation, retention, and explainable-risk defaults selected for Feature
045. The code authority is
`internal/workloadobs/defaults.go`.

These defaults do not expand the observation scope. Hideout does not retain
file contents, environment values, keystrokes, a complete PTY transcript, or
packet/proxy-auth payloads. Coverage remains `Partial` or `Unavailable`
whenever loss, unsupported hooks, filtering, ambiguity, corruption, or
retention prevents a complete claim.

## Current supported reference coverage

For the current macOS arm64 + Lima/Debian reference, process observation can
be `Available` after the cgroup-v2 boundary and observer are proved. File,
network, and DNS observation are currently `Partial`: recorded metadata is
useful, but the present hooks cannot prove complete absence. Native or
otherwise unproved backends report all four subsystems `Unavailable`.

Every run carries its own interval, generation, reason, and loss counters.
Target exit closes current availability without erasing earlier evidence; a
gap cannot be repaired by a later healthy interval. The authoritative product
support and non-claim wording is in
[support-matrix.md](support-matrix.md#workload-observation-coverage).
If the target obtains guest root or can tamper with its cgroup or observer, the
affected coverage degrades; Hideout does not claim guest-root containment.

## Frozen v1 defaults

| Boundary | Default | Meaning |
| --- | ---: | --- |
| Observer file-event aggregation | 500 ms | Repetitive, non-destructive file I/O with the same exact semantic key may be coalesced before ActivityRecord hashing and transport. |
| Normalized activity aggregation | 5 s | Inclusive, first-event-anchored grouping window; a rolling chain cannot extend it. |
| Normalized aggregation inputs | 65,536 | Maximum default in-memory input set before failing closed. |
| Active segment | 8 MiB | Maximum temporary quota overshoot is one active segment. |
| Exact-owner retention | 256 MiB | Default bound for one exact environment incarnation or disposable-session owner. |
| Global activity store | 1 GiB | Read-only process-wide safety ceiling across retained owners. |
| Wall-clock TTL | 0 (`owner-lifecycle`) | No time-only expiry by default; byte quotas and exact lifecycle cleanup still apply. |
| Risk minimum evidence | 1 matching observation | A supported risk is not hidden until a count or byte threshold is crossed. |
| Privileged guest identity | UID 0 | One attributed workload execution as UID 0 triggers the root-execution rule. |

The store prunes the oldest sealed segment first. It never trims an individual
record to create a false boundary. Pruning, corruption repair, or quarantine
creates an explicit coverage gap. A reusable environment binds its retention
policy to its exact incarnation on first attach; a later profile edit affects
future owners, not already retained history.

## Aggregation boundaries

The 500 ms observer coalescer exists on the file-syscall hot path. It preserves
the exact owner, session, execution, operation, subject identity/path state,
outcome, coverage evidence, total call count, byte count, first/last time, and
first/last collector sequence. Destructive operations are emitted
individually. Its pending-key set is bounded; overflow fails the collector
instead of silently discarding a semantic group.

The 5 s normalized aggregation policy is the default used by
`internal/workloadobs/aggregate`. It groups only equal semantic evidence and
never crosses an owner, session, execution, coverage interval, outcome,
attribution, route, path state, or policy disposition. Process lifecycle,
risk, coverage, and destructive file records never merge. Network socket
cookies may differ only when the remaining endpoint and attribution evidence
is identical.

The daemon does not perform an undocumented second aggregation after
redaction. Provider records that already contain a count and time interval
retain that interval exactly.

## Explainable risk thresholds

Risk rules are observations, not blocking policy and not a harmfulness score.
The v1 catalog triggers on the first matching redacted fact:

| Rule | Match boundary | Severity | Grouping |
| --- | --- | --- | --- |
| `file.write-outside-workspace/v1` | A file mutation in a resolved path class other than workspace, runtime, or unknown | High | Equal semantic evidence may group; every source Activity ID remains linked. |
| `file.destructive-change/v1` | A destructive flag or truncate/rename/unlink/delete/remove/rmdir operation | High | Preserve every operation as a separate finding. |
| `process.root-execution/v1` | The attributed actor or guest execution identity has UID 0 | Critical | Preserve every execution as a separate finding. |

Attribution confidence (`exact`, `inferred`, or `limited`) and policy
disposition (`allowed-observed`, `denied-prevented`, `policy-violation`, or
`not-evaluated`) remain separate fields. A denied operation does not erase the
observed rule match, and an observed match does not claim Hideout prevented
the action.

## Measurement basis

T156 retained the current dirty-tree measurement at
`.artifacts/045/performance/run-20260731T054812Z-47419/summary.json`
with summary SHA-256
`9363f509e48fbba8f18807e7753922364db5c631bee102be945f8bd1eb9a445e`.
The reference is a mixed developer workload: one process parses 288 MiB of
source payload across 96 files, performs four in-memory SHA-256 passes per
parsed record, and writes bounded derived metadata. This lengthens the measured
interval without multiplying the original file-I/O observation density.
The relevant results were:

- 6.895% median reference-workload overhead against the 10% ceiling;
- 1,227.697 ms warm-attach p95 and 79.219042 ms browser-freshness p95 against
  their 2 s ceilings;
- 41.013333 ms query p95 and 0.944709 ms render p95;
- 34.6% observer CPU p95 (one guest vCPU) and 73,527,296-byte RSS p95;
- 240.715 generated execs/s, with 6/7000 events dropped (0.085714%) and every
  drop reflected in degraded coverage;
- 6,843,409 bytes used under a 1,048,576-byte pressure-test owner quota plus
  exactly one 8,388,608-byte active-segment allowance.

The gate independently recomputed percentiles from raw samples and verified
oldest-sealed pruning, exact-owner isolation, visible history gaps, source
identity, and all retained artifact digests. The final clean installed
candidate must rerun the same gate; this dirty-tree evidence does not grant
release-candidate acceptance.

## Changing a default

Changing any value in the v1 table requires all of the following:

1. update `internal/workloadobs/defaults.go` and increment
   `DefaultsVersion`;
2. increment the affected risk rule or rule-set version when matching or
   severity changes;
3. update the cross-package default-wiring test and this document;
4. rerun unit/race/generated checks, the privacy gate, the real-Lima workload
   gate, and `scripts/gates/release-candidate-performance.sh`;
5. retain raw samples and explain why the new value improves the operator
   outcome without weakening coverage, privacy, exact cleanup, or the
   one-active-segment overshoot invariant.

No default change authorizes remote publication.
