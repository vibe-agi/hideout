# Interrupted Operations and Recovery

<!-- markdownlint-disable MD013 -->

This guide explains what an operator should do when a Hideout request loses
its response, the daemon exits, a provider cannot prove completion, a decision
client disconnects, or another operation blocks a mutation.

Recovery never creates authority. It may only continue or classify the exact
reviewed operation, decision, environment incarnation, or cleanup intent that
already exists. Do not edit Hideout's operation records, lifecycle journals,
profile files, Keychain metadata, lock files, or daemon state to make a warning
disappear.

## The Short Version

After confirming a mutation:

1. Keep the reported `op_...` operation ID.
2. Do not create a replacement plan merely because the response disappeared.
3. Check the control plane with `hideout daemon status --human`.
4. Open `hideout tui`, press `4` for **Operations**, select the exact ID, and
   press Enter. Read its phase, effects, evidence, result, and stored recovery
   action.
5. Fix only the named prerequisite. Let the running client retry its identical
   request, or let daemon startup reconcile accepted work after an actual daemon
   crash.
6. Treat `recovery-required` and `rollback-unproved` as unsafe unknown states,
   never as success.

Starting a stopped daemon is safe:

```bash
hideout daemon start
hideout daemon status --human
```

Stopping a healthy daemon is not a normal configuration or proxy update step.
It interrupts live control and observation. In particular, changing a managed
secret or the desired connection does not require `hideout daemon stop`.

## What the Operation Record Means

An operation ID is bound to one kind, owner, canonical plan digest, and base
revision. Reusing that ID with different input is rejected. A new ID is a new
request, not a retry.

| Phase | Meaning | Operator rule |
| --- | --- | --- |
| `planned` | Review exists, but apply has not been accepted. | No provider effect is authorized. Refresh or let the plan expire before reviewing a fresh plan. |
| `claimed`, `staging`, `activating`, `proving` | Apply was accepted and may have crossed an effect boundary. | Preserve the ID. Do not replay the provider mutation. |
| `rolling-back` | Restoration is in progress but is not yet proved. | Pause conflicting work and wait for evidence. |
| `recovery-required` | Completion or non-completion cannot currently be proved. | Follow the stored recovery action for this exact ID. |
| `succeeded` | Every required effect has durable success evidence. | This is the only successful terminal state. |
| `failed`, `cancelled`, `rolled-back` | A terminal non-success result has the evidence required for that result. | Read the result and decide whether a separately reviewed new request is appropriate. |
| `rollback-unproved` | Rollback ended without adequate restoration proof. | Do not repeat the original mutation; inspect the provider state manually. |

`recovery-required` is deliberately non-terminal. `rollback-unproved` is a
terminal classification, but it is not a healthy state and never becomes green.

## Failure Boundaries

The durable record separates request acceptance, provider effects, proof,
terminal publication, events, and the client response.

| Interruption | Authoritative interpretation |
| --- | --- |
| Before a plan is persisted | No operation exists and no provider authority was created. |
| After planning but before confirmed apply | The operation remains `planned`; startup recovery leaves it untouched. |
| After accepted apply but before an effect starts | The exact operation can continue without inventing a new request. |
| While an effect is `running` | The provider must be observed or reconciled; the effect must not be called blindly again. |
| After provider return but before durable evidence | Provider state, generation, route binding, or exact absence must prove the result. A return code alone is insufficient. |
| After evidence but before terminal publication | Recovery may publish the terminal result from the durable evidence. |
| After terminal publication but before event delivery | The terminal operation is authoritative. A fresh snapshot re-seeds clients; no effect is replayed. |
| After terminal publication but before the client response | Repeating the identical request returns the stored terminal result. |
| During rollback | Success is forbidden. Restoration needs its own durable proof. |

The client may perform a bounded immediate retry using the same serialized
request after a transport failure. Manager returns the stored result for an
already terminal operation and refuses an operation ID whose binding differs.

## Inspect Before Acting

Use read-only surfaces first:

```bash
hideout daemon status --human
hideout tui
hideout env list
hideout session list
hideout show connection
hideout secret status <ref>
```

In the TUI, press `4`, select the operation, and press Enter. The retained
Operations view is bounded, so it is not an archival ledger. If the exact ID is
not present, refresh the authenticated snapshot; absence from the view does not
prove that the operation never existed.

The current public CLI has no generic `hideout operation retry <id>` command.
Daemon startup automatically scans accepted non-terminal operations, but a
healthy running daemon does not expose a generic operator-triggered reconcile
for arbitrary configuration operations. If an operation remains
`recovery-required` after startup, keep it blocked and follow its provider-
specific recovery action. Do not substitute a newly generated operation ID.

## Provider-Specific Recovery

### Profile and configuration

The daemon can reconcile an accepted profile transaction against its durable
plan and the canonical profile revision. A stale revision is a refusal, not
permission to overwrite the newer profile.

Do not hand-edit the profile to imitate a terminal result. If an operation
remains unproved after daemon startup, preserve its ID and inspect the stored
recovery action.

### Managed secrets and the macOS Keychain

First inspect metadata only:

```bash
hideout secret status <ref>
```

Unlock the login Keychain if the operation reports a locked provider. Hideout
reconciles a provider commit by exact operation identity and generation. It
does not treat "an item exists" as proof that this operation wrote it, and it
does not blindly repeat an ambiguous delete.

If recovery says `secret-value-required`, the provider has proved that the
accepted set/rotate did not commit, but the value is needed again. The current
public CLI does not provide a generic exact-operation resume input. Keep the
operation and its ID; do not pass the value in argv, export it into the daemon
environment, or create a replacement mutation and call that a retry.

Normal secret changes use the running daemon:

```bash
hideout secret set <ref>
hideout secret rotate <ref>
```

These commands use hidden terminal input by default. They are for newly
reviewed operations, not recovery of a different accepted ID.

When a rotate affects live environment gateways, Hideout treats the route
proofs and Keychain write as one ordered operation. Every environment must
durably finish stage, probe, activate, route proof, and existing-connection
drain evidence before the Keychain generation may change.

If the daemon exits during that operation, the next daemon first acquires the
store's singleton lock. Graceful shutdown closes every gateway before releasing
that lock. After process death, the former daemon can no longer execute or own
the in-memory registry, and the OS tears down its process-owned descriptors.
Startup recovery may then classify, but never replay, the interrupted mutation:

- an exact new Keychain generation plus every required pre-commit route proof
  becomes `secret-generation-committed-network-authority-reset`;
- an exact unchanged Keychain generation invalidates the old daemon's staged
  routes and terminates without writing the secret; and
- a mismatched generation, ambiguous provider result, or missing required route
  proof remains `recovery-required`.

The `network-authority-reset` evidence identifies the new daemon authority and
observation time; it contains no secret. After a reset there is no live gateway
to adopt, so Effective networking is `not-observed` until a later eligible
attach constructs and proves a new gateway from the current Keychain
generation.

### Connection and network route

Pause new attaches when the stored recovery action says the route is unproved.
Use `hideout show connection` to inspect desired configuration and use network
activity only as supporting evidence when coverage is Available. Neither view
alone proves the live route binding.

Restart recovery is observation-only: it cannot stage or activate a route while
trying to discover whether an earlier effect completed. If route generation,
probe evidence, or the operation envelope does not match, the operation remains
blocked.

The connection command always updates reviewed Desired configuration. When an
eligible environment gateway is live, Manager stages, probes, activates, and
proves the new route online for newly accepted connections; connections already
accepted by that gateway keep their immutable prior route until they close. If
no live gateway can be proved, the change is explicitly pending for the next
eligible attach rather than being presented as Effective. A healthy
desired-proxy, DNS, or managed-secret update does not require stopping the
daemon or recreating the VM.

### Workload activity and coverage gaps

Inspect the exact run before drawing a conclusion from its history:

```bash
hideout activity summary --session <id>
hideout activity coverage --session <id>
hideout activity events --session <id> --limit 100
```

Coverage is an interval claim. A sequence gap, explicit drop, observer or
daemon restart, schema rejection, retention pruning, corruption repair,
uncertain attribution, target exit, or cleanup uncertainty can make one or
more subsystems `Partial` or `Unavailable`. A later healthy interval does not
repair the missing history, and an empty event query proves absence only when
the complete queried time range and subsystem are `Available`.

Do not restart the daemon merely to clear a coverage warning: that interrupts
live control and observation and may create another gap. Preserve the run ID,
coverage interval, reason, generation, and loss counters. If the target has
exited, `Unavailable (target-exited)` is the correct current state; retained
earlier intervals remain historical evidence. Start a new run only when a new
execution is actually intended, not as a way to rewrite the old run's result.

Activity-store recovery truncates a torn active tail after the last valid
frame or quarantines a corrupt sealed segment. It never returns corrupt
records as evidence. Repair, quarantine, or quota pruning emits a visible
coverage gap. Do not edit segment or index files to suppress it.

Reusable evidence belongs to the exact environment incarnation and survives a
normal stop. Disposable evidence belongs to the exact session. `clean`,
remove, recreate, or successful disposable teardown deletes only the proved
old owner; a live, mismatched, or ambiguous owner blocks deletion. If cleanup
is blocked, inspect the lifecycle operation and owner identity rather than
manually deleting the activity directory.

### Environment stop, clean, and delete

Inspect the exact environment and active sessions:

```bash
hideout env inspect <name>
hideout session list
```

Lifecycle reconciliation has its own typed command:

```bash
hideout daemon reconcile --env <name-or-id>
```

This command retries lifecycle observation for one environment. It is not a
generic configuration-operation retry. Stop success requires stable backend
absence; clean/delete additionally require exact metadata and retained-data
cleanup evidence. A backend command returning zero, a single missing inventory
row, or a changed boot identity is not enough.

Never use `clean`, `env remove`, or forced cleanup merely to clear an unknown
status. Preview the exact destructive target and resolve every ownership
blocker first.

## Decision Claims and Leases

A decision claim is temporary exclusive UI ownership, not approval. The
default lease is one minute; accepted leases are bounded from 5 seconds to
5 minutes. A live claim cannot be stolen.

Inspect and explicitly release a claim with its opaque token:

```bash
hideout decision inspect <decision-id>
hideout decision release --claim-token <token> --revision <n> <decision-id>
```

Well-behaved clients release on close or disconnect. Expiry is the backstop:
the next authenticated decision-center maintenance pass returns an expired
claim to pending and records the release.

Taking over an expired claim requires both an explicit takeover and the exact
current revision:

```bash
hideout decision claim \
  --takeover \
  --revision <n> \
  --lease 1m \
  <decision-id>
```

A stale claimant cannot approve or deny after release, expiry, or takeover.
Claim tokens are credentials; do not paste them into logs or support reports.

## Mutation Blockers

Configuration, attach, reconciliation, stop, cleanup, and lifecycle mutation
use shared serialization keys. A blocker reports:

- the blocking owner kind and bounded ID;
- its current phase;
- when it started; and
- the safe recovery action.

That metadata explains who owns the key. It does not authorize takeover,
cleanup, or cancellation.

Common actions are:

- `establishing`: wait for attach establishment to finish;
- `reconciling`: wait, or use the exact environment reconciliation command
  after the prior attempt is blocked;
- `active` session: end the owning session before destructive work;
- `stopping`: wait for stable stop evidence;
- `cleanup` or unknown ownership: inspect lifecycle status and keep destructive
  actions disabled until ownership is proved.

Do not remove lock files, journals, owner records, or VM metadata to bypass a
blocker.

## When Manual Inspection Is Required

For `rollback-unproved`, `provider-completion-unproved`,
`network-transition-state-unproved`, envelope mismatch, changed incarnation,
or unknown ownership:

1. Stop new work that could change the same resource.
2. Preserve the operation ID, owner, phase, evidence codes, and recovery code.
3. Inspect the provider using read-only commands first.
4. Do not infer success from desired configuration, process exit, a provider
   return code, or one inventory sample.
5. Create a redacted local support report if escalation is needed:

   ```bash
   hideout support report --out ./hideout-support.json
   ```

6. Review that file before sharing it. Local paths can be visible; known
   credentials, URI userinfo, authentication fields, sensitive argument/query
   values, and Hideout control tokens are removed at the export boundary.

Manual inspection may establish what happened, but it does not itself rewrite
the operation ledger. If the supported provider cannot bind that evidence to
the exact operation, Hideout must remain unproved.

## Recovery Evidence and Verification

The local crash matrix exercises interruptions before and after persist,
claim, stage, activate, proof, commit, event, and response:

```bash
scripts/gates/recovery.sh
```

This gate proves local Manager/daemon recovery mechanics and mutation judges.
It is not real-Lima evidence and does not by itself establish release-candidate
behavior.
