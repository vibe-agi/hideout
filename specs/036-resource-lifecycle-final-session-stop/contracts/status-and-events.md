# Lifecycle Status And Event Contract

## Human Status

The compact status view distinguishes backend truth from derived activity:

```text
Environment  default
Backend      running (observed 2026-07-16T05:00:00Z)
Activity     idle-grace (stops in 12s)
VM pins      0
History      1 retained fact, 1 handoff fact
```

Blocked example:

```text
Activity     blocked-unproved
VM pins      1
Reason       session cleanup is not proved complete
Recovery     hideout doctor --feature daemon --level deep
```

## Machine Shape

Machine status includes:

- redacted environment ID;
- backend state and start generation;
- timestamp of the latest coordinator-held backend observation;
- activity classification;
- idle deadline/remaining duration when applicable;
- reconciliation/stop state and one bounded machine-readable reason code;
- counts and bounded summaries for VM pins, drains, historical handoff facts,
  historical retained-state facts, and orphaned/unproved resources; and
- reconciliation state.

`reasonCode` is populated by the production transition that made the activity
blocked or unknown. A renderer must not invent it from prose or fill a
stop-specific fixture field that the persisted reconciliation path cannot
produce.

It excludes credentials, raw host/guest/control paths, descriptors, process
IDs, proxy values, command argv, and terminal/application text.

The `retained` and `handoffs` arrays are bounded journal facts. They are useful
for lifecycle classification and history, but are not current inventories of
the HostFS overlay, decision store, or host processes. Those provider stores
remain authoritative.

Ordinary status reports the latest coordinator-held observation and its
timestamp. It does not claim that every status request performs a new backend
probe. A surface that claims point-in-time backend truth must explicitly refresh
through the backend lifecycle observer first.

## Event Rules

Lifecycle events are typed redacted facts emitted by the coordinator with the
current derived status snapshot:

- resource registered/transitioned/released/orphaned;
- idle grace started/cancelled/expired;
- stop attempt started, stop deferred, environment stopped, or stop unknown;
- backend incarnation superseded; and
- reconciliation completed or blocked.

Events never carry a capability token or executable recovery payload. UIs may
update from the event payload and use existing typed commands for action.

## Surface Parity

CLI human output, machine output, Manager status, daemon status/events, doctor,
TUI/WebUI, and audit must derive activity and recovery class from the same
lifecycle view. A surface may omit detail for compactness but cannot relabel:

- orphaned as failed/released;
- stopping-unknown as stopped;
- retained state as active demand; or
- independent host handoff as a process Hideout still owns.

## Recovery Guidance

Automatic stop provides no force action. Explicit stop guidance is shown only
when the catalog and current owner observations permit bounded recovery.
Corrupt/unclassifiable or live owners remain refusal findings.
