# Resource Catalog And State Contract

## Source Of Truth

One Go-owned catalog is the production source for registration, transition
validation, stop evaluation, status labels, schemas, model exploration, drift
tests, and docs truth. Tests must not maintain a second kind list.

## Descriptor Requirements

Each descriptor defines:

- stable resource kind;
- status: `implemented`, `design-ready`, or `fixture-only`;
- allowed owner classes;
- allowed dependency kinds and `pin|drain` modes;
- metadata persistence class;
- allowed close policies;
- one closed typed recovery probe (`backend-observation`, `session-absence`, or
  `network-runtime`);
- bounded public label; and
- whether a production registrar exists.

Only an implemented kind with a production registrar may affect the production
stop predicate. An unknown or incompletely described kind fails closed.

## Registration Contract

1. Registration is performed by a provider through the daemon-owned registrar.
2. Manager registers the planned environment dependency while holding the
   environment transition lock and before target/provider authority is usable.
3. The registry validates identity, owner, generation, kind, dependency
   existence, edge mode, close policy, and acyclicity.
4. The provider's complete planned subgraph is committed by one atomic journal
   transaction before effect usability. A crash before a later routine-state
   checkpoint therefore discovers a conservative planned graph, not a missing
   effect.
5. Release occurs only after the provider's declared cleanup proof succeeds.
6. Duplicate release is idempotent; conflicting ownership/generation fails.
7. Cleanup failure transitions to `orphaned` unless absence is independently
   proved.

Routine `active`, `draining`, and successful-release observations may be
coalesced into a bounded 500 ms journal checkpoint. This is a persistence
optimization only: the in-memory reducer changes immediately, the committed
planned graph remains fail-closed recovery input, and orphan/cleanup failure,
reconciliation, boot binding, idle deadlines, stop attempts, and coordinator
shutdown flush synchronously.

## Stop Predicate

For the exact observed backend incarnation, automatic stop is permitted only
when:

- the backend is observed running as that incarnation;
- no live or unproved pin path exists;
- no lifecycle transition is in progress;
- reconciliation is complete;
- the current incarnation's 15-second grace has expired; and
- the current daemon owns the new serialized stop attempt.

Transition-in-flight is enforced by coordinator serialization and active
handles/attempt state; it is not a second mutable journal flag.

Before backend stop becomes eligible, the owning Manager/session providers must
have closed every `pre-stop-drain` effect. A remaining live or unproved drain
aborts automatic stop; the coordinator does not call provider close callbacks.
After provider cleanup, lifecycle metadata is released dependent-first. Root
stop may prove only cataloged `co-terminate-with-root` resources absent.

## Current Production Classifications

- Live run session/target and run-scoped endpoint bridge: VM-pinning.
- Environment network service: drainable, not self-pinning.
- Broker, HostFS read provider, and live read grant: session-owned drains.
- Direct host app launch: bounded handoff fact, no VM pin.
- HostFS staged object and decision record: bounded retained facts, no VM pin.
- Stable environment state, retained HostFS data, decisions, and audit remain
  authoritative in their existing stores; lifecycle facts do not own them.
- Existing session owner state `failed`: lifecycle `orphaned`, not terminal.

Detached bridges and guest-only projections remain design-ready. Host
materialization is represented by two closed design-ready kinds:

- `host.materialization.snapshot`: retained host-only state with no VM edge;
- `host.materialization.live-projection`: ephemeral live projection with one
  required `pin` edge to `backend.incarnation`.

Neither materialization kind can enter production evaluation in 036. The live
kind is rejected when its root edge is omitted; the snapshot kind never
inherits that edge merely because both belong to one product concept.

## Drift Guards

Tests fail when:

- an implemented producer has no descriptor;
- an implemented descriptor has no registrar/probe;
- a recovery probe is added without daemon dispatch;
- a reducer branch has neither a producer nor an explicit future/test marker;
- schema/docs list differs from catalog output;
- a dependency cycle or unsupported close policy is admitted; or
- shadow evaluation disagrees with existing kernel owner truth.
