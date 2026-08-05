# Formal Models

<!-- markdownlint-disable MD013 -->

Hideout uses small TLA+ models for lifecycle and concurrency rules that are
easy to state incorrectly in implementation code. These models are bounded
abstractions. TLC alone does not prove that the Go implementation refines a
model. The repository gate therefore combines TLC with 27 named Go
production-refinement and crash-matrix tests; those tests establish explicit
trace correspondences and regression boundaries, not a machine-checked
whole-program refinement proof.

The operator command parser follows the same separation of concerns:

```text
constrained phrase
  -> typed Go intent
  -> Manager plan
  -> abstract lifecycle action
  -> Manager apply or rollback
  -> evidence
```

Parsing carries no authority. A parsed command still passes through the
existing Manager planner, policy checks, decision workflow, and apply path.
There is no generic host-command or capability fallback.

`internal/operatorintent` owns and tests this grammar contract. The top-level
CLI currently maps `setup`, `show connection`, `connect`, and profile-scoped
`allow`/`deny` through this intent layer; `allow`/`deny` reuse the Manager
profile HostFS planner and carry an authority-parity test against the advanced
`profile fs` surface. `--once` and `--for-this-project` parse but fail closed
pending the scope design recorded in `docs/DEBT.md`. Existing commands retain
their current dispatch; the remaining intent entries are not activated until
each has an explicit Manager plan mapping and behavioral parity tests.

## Model Inventory

| Model | Contract checked |
| --- | --- |
| `ResourceLifecycle` | Sessions own resources uniquely; the last-session grace period cannot stop an environment while a live session or proved resource remains. |
| `ConfigurationLifecycle` | Machine, boot, service, and session changes affect only their declared layer; session snapshots remain write-once. |
| `NetworkTransition` | Route changes follow stage, activate, commit or rollback; posture changes require connection quiescence. |
| `RequestWorkflow` | Requests have one claimant, terminal states carry no claimant, and an ended session cannot retain pending authority. |
| `StopObservation` | Environment stop separates actual VM state from inventory samples: success requires stable terminal proof and is never reported while the bound incarnation still runs; anomalous samples and boot changes fail closed. |
| `AttachReservation` | The attach-establishment reservation excludes reconciliation without the rejected lock cycle; reconcile scrubs only provably orphaned residue, cancellation removes only the run's own state, and daemon-crash residue stays scrubbable. |
| `DisposableRecovery` | Disposable owner cleanup binds an exact lifecycle generation and cannot remove retained evidence before backend absence and metadata cleanup are proved. |
| `MigrationBundle` | A stopped source is claimed only until an independent snapshot exists; authenticated checkpoints survive crash, unverified tails are discarded, only a complete footer publishes, cancellation never publishes, an explicitly retained partial remains non-importable until separately removed, tampering blocks import, and export never changes source content. |
| `MigrationAdoption` | One immutable bundle may feed several destination operations; name claims remain exclusive; conflicts default to refusal; rename preserves the existing owner; replacement requires a separate confirmation and destructive effect followed by a fresh import plan; staged objects never run; control/backend IDs are fresh; Safe Clone guest IDs are pairwise fresh; Exact Guest Restore preserves identity without a uniqueness claim; and imported authority stays disabled without approval. |
| `OperatorConfiguration` | Multiple clients share CAS revisions, canonical operation identity, leases, crash recovery, rollback evidence, and fail-closed terminal publication. |
| `SecretTransition` | Live secret rotation proves route stage/probe/activate/prove/drain before the provider generation changes; daemon authority reset closes connections, exact committed or unchanged generations reconcile without replay, and mismatches remain recovery-required. |
| `WorkloadObservation` / `WorkloadObservationLiveness` | The full boundary proves owner isolation, known loss, retention truncation, coverage degradation, exact cleanup, and observer tail-drain safety. A second configuration checks the same combined state machine's weak-fair transition/progress properties without multiplying the full safety graph. |

`WorkloadObservation` retains both owner kinds, both process identities,
`MaxSequence = 2`, and every cross-subsystem invariant. Its liveness companion
retains both owners and processes but uses the separately declared
`livenessMaxSequence = 1`: one admitted tail covers non-empty relay, loss,
prune, cleanup, graceful drain, and forced-close progress. The production
queue refinement still exercises two admitted frames. This is a scheduling
boundary over one module, not a split into unrelated models.

`StopObservation` pins the stop-path contract behind the 2026-07 false-stop
fix. Its safety claim is conditional on an explicit environment assumption:
transient false inventory readings are bounded and never consecutive.
Weakening the model to accept a single terminal sample (the pre-fix
behavior), or dropping the isolated-anomaly assumption, makes TLC produce
the false-success trace recorded in `DEBT.md`.

`AttachReservation` models the establishment protocol implemented by the
lifecycle coordinator. Reconciliation judges only observable evidence
(durable owner records plus a liveness probe), so removing the
reservation/reconcile exclusion reproduces the runtime-scrub race. Model
success does not replace the Go interleaving tests.

`MigrationBundle` and `MigrationAdoption` are the feature 046 design preflight.
They deliberately put identity transformation on each import rather than in
the reusable export. The adoption model checks Safe Clone uniqueness across
destinations but does not assert uniqueness for Exact Guest Restore: without a
cross-computer coordinator, source retirement is an operator statement rather
than a fact Hideout can prove. Pure production-shaped Go transitions now refine
the export, crash/resume, cancellation, multi-destination identity, authority,
conflict, and rollback traces. The Lima snapshot, staging, adoption, verification,
and cleanup effects are implemented, but TLC does not prove those provider calls;
`scripts/gates/migration.sh` checks the refinement inventory and the exact-package
`scripts/gates/migration-lima.sh` supplies separate real-backend evidence.

Imported-Lima first-start reconciliation does not add a transition before
`active`: staging still has no host mounts and is never runnable or visible.
After the atomic visibility commit, the backend may admit only
destination-generated profile/runtime mounts and an explicitly approved
workspace mapping before it starts the stopped instance. This remains within
`StagedStateNeverRuns`, `RunnableIffActive`, and `AuthorityRequiresApproval`.
Exact root-disk size, fail-closed image sentinel, and authenticated runtime-image
provenance are concrete provider refinements covered by the closed migration
test inventory; they are intentionally not abstracted as new TLA+ identities.
Likewise, installing the destination Lima control key is an atomic sub-action of
the already modeled isolated adoption step: the staged guest remains stopped and
networkless, completion requires the matching action receipt, and no imported
runtime authority becomes effective. The product-owned cloud-init override is a
concrete refinement that prevents Lima's changing boot instance ID from undoing
the receipted host identity or root control key. It therefore preserves
`StagedStateNeverRuns`, `RunnableIffActive`, and `AuthorityRequiresApproval`
without adding another abstract state transition.

The state spaces intentionally exclude concrete filesystem paths, file
contents, secrets, Lima commands, and unbounded production identifiers. Those
belong in implementation tests and real backend evidence.

## Running TLC

Run the complete inventoried model and Go-refinement gate with:

```bash
scripts/gates/formal.sh
```

The gate reads `formal/inventory.json`, pins TLA+ tools `v1.7.4` by SHA-256,
runs all 17 configurations across 12 modules and all 27 inventoried Go tests,
checks the exact set of 138 safety invariants and 28 liveness properties,
verifies the evidence independently, and writes private digest-bound output
below `.artifacts/045/formal/`. Java is a development dependency only; it is
not included in the Hideout package and is not required in a target
environment. Gate 0 invokes this same formal gate. Hosted acceptance uses one
TLC worker and requests a 3072 MiB heap; evidence records both the request and
TLC's actual usable heap for every configuration. TLC progress is streamed to
the job log while the same bytes remain digest-bound in the private artifact.

## Refinement Discipline

TLC complements rather than replaces:

- Go parser and validator tests for exact command syntax;
- the production bounded lifecycle checker in `internal/lifecycle`;
- concurrency tests around leases, claims, attach, cleanup, and stop;
- real Gate 2 and Gate 3 backend evidence; and
- browser, PTY, and operator-experience tests.

When TLC finds a counterexample that can occur in the implementation, add the
smallest equivalent trace as a Go regression test before changing the model.

The natural `connect` command maps through the generic profile transaction,
which coordinates a checkpointed network batch for every eligible live
environment. `NetworkTransition` and `SecretTransition` model that runtime
authority; profile persistence alone is still never proof that a route became
Effective.

## Feature 045 Refinement Map

The three 045 models deliberately meet production code at narrow, reviewable
trace boundaries:

| Model | Production correspondence | Required adverse traces |
| --- | --- | --- |
| `OperatorConfiguration` | Manager canonicalizes a plan, binds it to a base revision and operation ID, claims its mutation keys, records ordered effects/evidence, and publishes one terminal result. API, TUI, and WebUI are clients of that same operation model. | Concurrent stale client, identical retry, changed-input operation-ID reuse, daemon exit before/after each durable boundary, rollback, response loss, and terminal reseed. |
| `SecretTransition` | A managed-secret generation and every eligible live gateway participate in one ordered stage → probe → activate → prove → drain → provider-commit transition. Startup recovery observes exact route and provider generations without replaying an ambiguous effect. | Failure or crash at every network boundary, unchanged/committed/mismatched provider generation, old-authority reset, rollback proof, and idempotent retry. |
| `WorkloadObservation` | The supervisor establishes one exact cgroup owner; observer frames become owner-bound activity and coverage intervals; target exit seals the producer; completion follows only after the admitted relay tail is durably consumed, an exact final counter receipt fences later observer frames, goodbye is accepted, and the authenticated transport proves clean EOF plus successful bridge exit; the store applies deterministic redaction, bounded retention, and exact lifecycle cleanup. Full safety uses `MaxSequence = 2`; weak-fair transition/progress checking uses `MaxSequence = 1`. Both configurations retain the reusable/disposable owners and parent/reused-PID identities, so cleanup and relay interleavings remain in one combined state machine. | Sequence gap, explicit drop, observer/daemon restart, target exit with a pending tail, exact final receipt, post-final observer frame, graceful drain and clean transport close, forced-close degradation, retention pruning before completion, cleanup before forced close, corruption repair/quarantine, ambiguous owner, disposable teardown, and reusable-incarnation replacement. |

The production tests named in `formal/inventory.json` must remain present and
green. The formal verifier rejects a missing configuration or test, a changed
model/log digest, a counterexample, or an incomplete invariant/liveness pass
set. Real cgroup, eBPF, Lima, Keychain, terminal, and browser behavior remains
the responsibility of the corresponding non-formal release gates.
