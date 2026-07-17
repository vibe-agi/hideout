# Data Model: Resource Lifecycle And Final-Session Stop

<!-- markdownlint-disable MD013 MD060 -->

## 1. Stable Environment

Retained configuration and storage identity. It is not itself a live resource.

| Field | Meaning | Validation |
|-------|---------|------------|
| `environmentId` | Existing stable environment ID | Existing environment ID rules |
| `backend` | Configured backend | Existing supported value |
| `instanceName` | Backend instance name | Existing environment record value |
| `recordStatus` | Existing workflow status | Informational; never backend truth |

The existing environment record remains the source of retained configuration.
036 does not add a backend/config version merely to store lifecycle state.

## 2. Backend Incarnation

One observed start of a preserved backend instance.

| Field | Meaning | Validation |
|-------|---------|------------|
| `environmentId` | Stable environment | Must exist |
| `startGeneration` | Host-issued monotonic generation | Positive; allocated under environment lock |
| `instanceName` | Observed backend instance | Must match stable environment |
| `bootId` | Guest kernel identity | Required for Lima running state; canonical UUID |

Canonical identity:

```text
environmentId / startGeneration / instanceName / bootId
```

A provisional incarnation has `environmentId`, `startGeneration`, and expected
`instanceName`, but cannot accept target authority until the observer supplies
the boot ID. An external boot-ID change supersedes the old incarnation.

## 3. Managed Resource

One typed runtime effect or lifecycle root.

| Field | Meaning | Validation |
|-------|---------|------------|
| `kind` | Closed catalog kind | Must be cataloged |
| `id` | Provider-local bounded ID | Non-empty, no secret/path material |
| `generation` | Resource generation | Positive; immutable |
| `owner` | Current lifecycle actor | Valid daemon/provider owner reference |
| `state` | Lifecycle state | Valid transition only |
| `dependencies` | Typed outgoing edges | Existing references; acyclic |
| `persistence` | Metadata persistence class | Catalog-allowed value |
| `closePolicy` | Absence proof rule | Catalog-allowed value |
| `updatedAt` | Bounded operational time | UTC |

Canonical resource identity:

```text
kind / id / generation
```

### States

```text
planned -> starting -> active -> draining -> released
                 \-> failed
unproved nonterminal -> orphaned -> draining -> released
```

- `released`: cleanup or independent absence is proved.
- `failed`: the contract failed and absence is nevertheless proved.
- `orphaned`: ownership/liveness/cleanup cannot be proved; it is nonterminal.
- Reopen allocates a new generation; terminal generations do not move backward.

The existing session owner spelling `failed` maps to lifecycle `orphaned`
because that record is retained specifically when cleanup is not proved.

## 4. Dependency Edge

| Field | Meaning | Validation |
|-------|---------|------------|
| `from` | Dependent resource | Existing nonterminal resource |
| `to` | Required resource/root | Existing resource |
| `stopMode` | `pin` or `drain` | Allowed by source catalog descriptor |

- `pin`: owner-facing demand prevents root stop.
- `drain`: support effect must be closed by its owning provider before stop can
  become eligible, but is not itself user demand.
- The graph must remain acyclic.
- Multiple paths to a root use the more conservative path result.

## 5. Resource Descriptor

One row in the production source catalog.

| Field | Meaning |
|-------|---------|
| `kind` | Stable kind ID |
| `status` | `implemented`, `design-ready`, or `fixture-only` |
| `ownerKinds` | Allowed owner classes |
| `dependencyKinds` | Allowed target kinds and stop modes |
| `persistence` | Allowed metadata persistence |
| `closePolicies` | Allowed absence proof rules |
| `recoveryProbe` | Go-owned provider probe key |
| `publicLabel` | Bounded operator label |
| `productionRegistrar` | Whether current product code can emit this kind |

Only implemented descriptors with a production registrar enter the production
stop predicate. Unknown descriptors fail closed.

Alternative dependency shapes use separate descriptors. In particular,
`host.materialization.snapshot` is a retained host-only design-ready kind with
no edge, while `host.materialization.live-projection` is an ephemeral
design-ready kind with one required backend `pin`. Neither is production-
registrable in 036.

### Initial Implemented Live Kinds

| Kind | Dependency/disposition |
|------|------------------------|
| `backend.incarnation` | Lifecycle root |
| `run.session` | Pins backend incarnation |
| `guest.supervisor` | Drains/co-terminates under session/root rules |
| `guest.target` | Drains/co-terminates under supervisor/root rules |
| `broker.listener` | Pre-stop drain under session |
| `hostfs.read-provider` | Pre-stop drain under session |
| `hostfs.live-read-grant` | Pre-stop drain under provider/session |
| `network.environment-service` | Drains before backend stop |
| `endpoint.run-bridge` | Pins backend while originating run is live |

Daemon connections, daemon workers, guest endpoints, stable environment
records, and audit events are not production lifecycle resource kinds.

### Bounded Product Facts

Facts are non-authoritative classifications outside the live graph:

| Kind | Class | Authority |
|------|-------|-----------|
| `hostapp.handoff` | `handoff` | Host-app provider/audit |
| `hostfs.staged-object` | `retained` | HostFS overlay store |
| `decision.record` | `retained` | Decision store |

Facts have no dependencies and never enter the stop predicate. The journal
keeps at most 64 recent facts; eviction cannot mutate underlying product state.

## 6. Metadata Persistence

| Value | Meaning |
|-------|---------|
| `ephemeral` | Remove after terminal cleanup is proved |
| `retained` | Keep for explicit workflow/retention policy |
| `evidence` | Append-only operator evidence |

Persistence never implies liveness. Recovery discovery entries for ephemeral
resources remain operational journal metadata, not retained product state.

## 7. Close Policy

| Value | Meaning |
|-------|---------|
| `pre-stop-drain` | Owning provider must close and prove absence before stop eligibility |
| `co-terminate-with-root` | Exact observed root stop proves an allowed guest-internal effect absent |
| `survive-root` | No dependency path to root; effect/record remains |
| `external-unmanaged` | Handoff is history only; Hideout has no close handle |

Automatic stop rejects an orphan using `co-terminate-with-root`. Explicit
recovery stop can use that policy only after independent external owner absence
is proved and the descriptor permits it.

## 8. Backend Observation

| Field | Meaning | Validation |
|-------|---------|------------|
| `state` | `running`, `stopped`, `absent`, `unknown` | Closed set |
| `instanceName` | Observed/queried instance | Required except unsupported backend |
| `bootId` | Current guest boot identity | Required only for `running` Lima |
| `observedAt` | Observation time | UTC |
| `reasonCode` | Bounded diagnostic classification | Required for `unknown` |

`unknown` is not stopped. A running observation with a different boot ID
creates/supersedes an incarnation; it does not mutate the old identity.

## 9. Lifecycle Journal

Mutable discovery record under the private store.

| Field | Meaning |
|-------|---------|
| `schema` | `hideout.lifecycle-journal/v1` |
| `environmentId` | Stable environment |
| `startGeneration` | Latest allocated generation |
| `incarnation` | Current/provisional observed identity |
| `resources` | Bounded nonterminal registrations |
| `facts` | At most 64 bounded retained/handoff classifications |
| `idleDeadline` | Current incarnation deadline, if any |
| `stopAttempt` | Current bounded attempt, if any |
| `reconciliation` | Current-daemon progress/result |
| `updatedAt` | UTC write time |

Rules:

- one file per environment under a private store-owned directory;
- symlink-safe ancestor and regular-file checks;
- atomic temp write, sync, rename, and bounded size/count;
- one complete registration-owned planned graph is committed before its first
  provider effect becomes usable;
- routine active/draining/released observations may converge through one
  bounded 500 ms checkpoint because the durable planned graph remains a
  conservative restart envelope;
- boot binding, cleanup/orphan failure, reconciliation, idle deadline, stop
  attempt, and coordinator close are synchronously durable;
- no tokens, raw paths, argv, PIDs, descriptors, proxy values, or handles;
- the journal is never accepted as liveness or authority proof.

`reconciliation` contains the current daemon instance, `pending`, `complete`,
or `blocked` state, observation time, and an optional bounded `reasonCode`.
Blocked production transitions write a reason code before exposing status; raw
provider/backend errors remain outside the journal.

## 10. Idle Deadline

| Field | Meaning |
|-------|---------|
| `incarnation` | Exact root identity |
| `daemonInstanceId` | Scheduler identity, not transferable authority |
| `scheduledAt` | UTC |
| `deadline` | `scheduledAt + 15s` |
| `generation` | Deadline generation for stale-timer rejection |

Any new pin, incarnation change, daemon replacement, or reconciliation
ambiguity invalidates the deadline. A replacement daemon may create a new full
deadline only after complete reconciliation proves idle.

## 11. Stop Attempt

| Field | Meaning |
|-------|---------|
| `attemptId` | Bounded random/monotonic ID |
| `incarnation` | Exact target identity |
| `daemonInstanceId` | Current owner |
| `mode` | `automatic` or `explicit-recovery` |
| `state` | `planned`, `draining`, `invoked`, `observing`, `committed`, `unknown`, `failed` |
| `startedAt` | UTC |
| `observation` | Last typed backend observation |

At most one current attempt exists per incarnation. A replacement daemon does
not adopt it; it independently observes/reconciles and creates a new attempt.

There is no persisted `transitionInFlight` field. The coordinator mutex, active
registration handles, and current stop attempt implement that serialization;
the reducer input exists for model exploration only.

## 12. Derived Environment Activity

Public activity is derived, never independently set:

| Activity | Condition |
|----------|-----------|
| `pinned` | One or more live/unproved pin paths |
| `idle-grace` | No pins, reconciled, current deadline pending |
| `idle-stop-eligible` | Grace expired and predicate otherwise true |
| `blocked-unproved` | Possible VM dependency cannot be disproved |
| `stopping` | Current attempt invoking/observing |
| `stopping-unknown` | Stop result is ambiguous |
| `stopped` | Backend observer reports stopped/valid absent |
| `not-applicable` | No preserved VM lifecycle root |

Status presents pinning/draining live resources, bounded retained facts,
bounded handoff facts, and orphans as separate collections. It does not claim
that facts are the authoritative retained-data inventory. Status also carries
the timestamp of the latest coordinator-held backend observation; it does not
claim that reading status performs a new backend probe.
