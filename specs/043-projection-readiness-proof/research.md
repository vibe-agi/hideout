# Research: Projection Readiness Proof

<!-- markdownlint-disable MD013 -->

## D1 - Reuse The Existing Session-View Barrier

**Decision**: Extend the existing exact session-view runtime-prerequisite
barrier to the complete projected-command catalog. Do not add a Manager sleep,
global guest probe, or target retry.

**Rationale**: `BuildSessionViewCommand` already binds
`/hideout/runtime/sessions/<session>` to `/hideout/session`, validates boot
identity, and can poll required paths 100 times at 20 ms. That is the closest
boundary before the fixed supervisor reports ready and before target commit.
The missing piece is catalog coverage: current prerequisites include bootstrap,
network, HostFSD, Workspace Portal, and supervisor files, but not the dispatcher
and projected commands.

**Alternatives considered**:

- Host `fsync`/`stat`: rejected because it cannot prove guest mount visibility.
- Fixed sleep: rejected because it proves neither identity nor completeness.
- Retry after command-not-found: rejected because target execution may already
  have side effects.
- Guest-global `command -v`: rejected because it can resolve ambient or foreign
  state outside the exact session view.

## D2 - Manager Owns The Complete Catalog

**Decision**: Manager derives one immutable catalog snapshot from the final
`RunDataPlane.Registry` after built-in and enabled external host-app bindings
are compiled. The snapshot is carried through the reviewed run and backend
session; Lima never reconstructs it from the profile.

**Rationale**: Manager starts with the profile registry, then adds built-in and
external host-app registrations before materializing shims. Lima `Prepare`
currently rebuilds only `RegistryFromProfile`, so its bootstrap view can omit
`code` and external pack commands. One Manager-owned snapshot eliminates that
split truth.

**Alternatives considered**:

- Let Lima compile host-app packs: rejected because backend code would acquire
  Manager policy/catalog responsibility.
- Keep profile-only bootstrap plus a separate `code` special case: rejected
  because external packs would retain the same race and an editor would become
  Core semantics.

## D3 - Manifest Written Last Is The Completion Marker

**Decision**: Materialize the dispatcher and all command entries first, hash
their exact bytes, then atomically write a strict session-local readiness
manifest last. The manifest binds session, environment, session snapshot, and
catalog digest.

**Rationale**: Directory entry visibility can lag across the guest mount. A
last-written manifest provides one bounded completion marker, while entry
digests prevent a visible-but-stale or partially rewritten catalog from
satisfying readiness. Manager rebinds the expectation after backend `Prepare`
just as it already rebinds the immutable session snapshot.

**Alternatives considered**:

- Presence-only marker: rejected because stale or foreign entry content could
  pass.
- Timestamp/mtime: rejected because it is not a stable identity or integrity
  boundary.
- Persist the manifest as user policy: rejected because readiness is
  session-local evidence, not durable authority.

## D4 - Exact Entry And Identity Proof

**Decision**: Inside the bound session view, require the manifest, dispatcher,
and every catalog entry to be regular, executable, non-symlink files with exact
digests. Manifest session/snapshot/catalog identities and the current
environment instance/boot proof must match before ready commit.

**Rationale**: Existing `test -x` follows symlinks and checks only a subset of
files. The target sees only `/hideout/session/shims`, so the proof must describe
that exact source rather than a global PATH. Existing boot binding and hidden
sibling runtime preserve the broader session-isolation boundary.

**Alternatives considered**:

- Check only the target command: rejected because the reviewed session catalog
  and broker advertise the complete set.
- Check paths but not bytes: rejected because a stale entry with the correct
  name could satisfy readiness.

## D5 - Ready/Commit Carries The Catalog Digest

**Decision**: Extend authenticated session readiness with the catalog digest,
disposition, observed entry count, and bounded duration. Manager validates it
against the reviewed expectation before activating the target lifecycle or
publishing daemon `Started`.

**Rationale**: The existing supervisor protocol already separates
`SupervisorReady` from `SupervisorCommit`. This is the correct fail-closed
target side-effect boundary and avoids a new control protocol.

**Alternatives considered**:

- Audit success after the run: rejected because it cannot prevent a target from
  starting on a false ready.
- Trust a guest boolean: rejected because Manager must compare the exact
  catalog and session identities it reviewed.

## D6 - Pre-Commit Cancellation Is Immediate

**Decision**: Before readiness/commit, cancellation closes the owning SSH
session immediately and records `projection.readiness.cancelled`; after commit,
the existing graceful target cancellation bound remains.

**Rationale**: Before commit no target is running, so there is no process tree
to drain. The current generic path may wait up to five seconds for a supervisor
that has not started reading frames, which violates the two-second readiness
cancellation criterion.

**Alternatives considered**:

- Use the post-commit five-second path everywhere: rejected because it is slow
  and semantically unnecessary before target authority exists.

## D7 - Bind Catalog Drift Into Review

**Decision**: Include the projection catalog digest in prepared run/review
truth, recompile it at apply, and refuse stale plans. The final data-plane
registry must equal the reviewed digest.

**Rationale**: Session snapshot identity covers profile command proxy/adapters
but does not fully cover enabled external host-app pack ownership. Readiness
cannot silently swap a reviewed catalog for ambient current state.

**Alternatives considered**:

- Accept current catalog at apply: rejected because command ownership and
  application binding could change after review.

## D8 - Dispose Of The Four 030 Debt Observations Individually

**Decision**:

1. Broker registration implementation is present, but add a registry/binding
   mismatch fixture whose only denying layer is `LookupExact`.
2. Add direct alias assertions for all four current templates.
3. Retain the existing pathMode flip test that proves recreate impact and
   machine/session drift.
4. Retain existing strict unbound-intent schema tests, and repair the still
   drifted `CapabilityDescriptor` JSON/schema contract, including
   `residualPolicy`.

**Rationale**: The old aggregate ledger row mixes fixed implementation, missing
mutation-sensitive assertions, and an actual schema defect. Evidence-backed
per-item disposition is more truthful than either keeping or deleting the row
wholesale.

**Alternatives considered**:

- Mark all four closed from task checkboxes: rejected because descriptor schema
  parity is demonstrably false.
- Revert development/debug templates to preserve: rejected because all current
  new templates intentionally use alias; 043 verifies current behavior.

## D9 - Use One Strict Exact-Package Evidence Set

**Decision**: Add a unified 043 producer that consumes one clean verified
package/runtime, runs the new readiness lanes plus existing 030/032/039 flows,
and emits semantic JSON/TSV artifacts judged by Go. Upgrade the reused proof
requirements to exact source/package/runtime binding.

**Rationale**: Existing 030/032 wrappers mostly wrap aggregate marker logs and
do not semantically validate exact package/runtime identity. The 039 persistent
grant lane exists but has no registered feature proof. The mature 041/042
producer/validator pattern already solves these false-green risks.

**Alternatives considered**:

- Relabel the latest aggregate Gate 2 pass as clean feature proof: rejected
  because marker text lacks feature artifact semantics and package/runtime
  binding.
- Keep separate packages per flow: rejected because cross-proof candidate
  identity could drift.

## D10 - Gate 3 Is Conditional And Narrow

**Decision**: Gate 3 belongs to 043 only for clean alias-privacy promotion. It
does not gate the mechanical first-attempt fix, external pack, or persistent
grant proof. When prerequisites are unavailable, those flows may complete but
privacy documentation must retain its old dirty provenance.

**Rationale**: Alias privacy includes proxy/DNS/subnet/privilege facts that Gate
2 does not establish. Conversely, readiness is a session-runtime visibility
property and must not be blocked or falsely proved by unrelated network setup.

**Alternatives considered**:

- Require Gate 3 for all 043 completion: rejected because it conflates
  projection readiness with network privacy.
- Promote privacy from Gate 2 alias markers alone: rejected because the
  adjacent Gate 3 claim would remain unproved.

## D11 - Mutation And Negative-Fixture Coverage

**Decision**: Observe implementation-red failures for complete catalog
admission, last-written manifest, regular/non-symlink/digest checks,
ready/commit digest validation, broker registration, template defaults,
pathMode drift, and descriptor parity. Add evaluator fixtures for dirty source,
package/runtime mismatch, missing/false checks, reduced samples, edited p95,
unknown fields, and `not-run`.

**Rationale**: Constitution IV requires every new assertion to demonstrate that
breaking the guarded implementation turns its exact test red, and every new
judge to reject a false-green artifact.

**Alternatives considered**:

- Green-only tests or shell exit status: rejected as non-evidence.
