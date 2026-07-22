# Formal Models

<!-- markdownlint-disable MD013 -->

Hideout uses small TLA+ models for lifecycle and concurrency rules that are
easy to state incorrectly in implementation code. These models are bounded
abstractions. They do not prove that the Go implementation refines the model.

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

`StopObservation` pins the stop-path contract behind the 2026-07 false-stop
fix. Its safety claim is conditional on an explicit environment assumption:
transient false inventory readings are bounded and never consecutive.
Weakening the model to accept a single terminal sample (the pre-fix
behavior), or dropping the isolated-anomaly assumption, makes TLC produce
the false-success trace recorded in `DEBT.md`.

`AttachReservation` models the GROUP 3 #10 establishment protocol ahead of
its implementation. Reconciliation judges only observable evidence (durable
owner records plus a liveness probe), so removing the reservation/reconcile
exclusion reproduces the runtime-scrub race the protocol was designed
against. Model success does not replace the Go interleaving tests the DEBT
entry requires when the protocol is implemented.

The state spaces intentionally exclude concrete filesystem paths, file
contents, secrets, Lima commands, and unbounded production identifiers. Those
belong in implementation tests and real backend evidence.

## Running TLC

Run all checked models with:

```bash
scripts/test-formal-models.sh
```

The script pins TLA+ tools `v1.7.4` by SHA-256 and caches the approximately
2.2 MB jar under `~/.cache/hideout/tla`. Java is a development dependency only;
it is not included in the Hideout package and is not required in a target
environment. Gate 0 invokes the same script.

## Refinement Discipline

TLC complements rather than replaces:

- Go parser and validator tests for exact command syntax;
- the production bounded lifecycle checker in `internal/lifecycle`;
- concurrency tests around leases, claims, attach, cleanup, and stop;
- real Gate 2 and Gate 3 backend evidence; and
- browser, PTY, and operator-experience tests.

When TLC finds a counterexample that can occur in the implementation, add the
smallest equivalent trace as a Go regression test before changing the model.

The natural `connect` command maps through `ProfileNetworkPlan` before runtime
network reconciliation. `NetworkTransition` models the later live transition;
it does not make profile persistence itself proof that an existing session has
changed routes.
