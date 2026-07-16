# Research: Concurrent Run Sessions

<!-- markdownlint-disable MD013 MD060 -->

## Decision 1: Formal 034 Is Same-Workspace Concurrency Only

**Decision**: Deliver concurrent sessions only when run planning resolves the
same existing environment and its already-pinned workspace. Preserve the
current static Lima virtiofs workspace declaration.

**Rationale**: The current failure is caused by an environment-wide lock held
for the complete run (`internal/manager/run_apply.go:97-108`) and by replacing
unique session runtime paths with one cleared environment path
(`internal/manager/run_session.go:53-62`; `internal/environment/environment.go:567-610`).
Neither requires dynamic workspace attachment. Keeping the mount unchanged
isolates the daily usability fix from the unproved cross-workspace transport.

**Alternatives considered**:

- Ship the full shared-default environment: rejected because dynamic
  workspace attachment and weaker cross-workspace isolation are a separate
  product/security gate.
- Create another VM for each concurrent command: rejected because it preserves
  the visible latency and resource problem rather than fixing reuse.

## Decision 2: Split Transition Serialization From Session Ownership

**Decision**: Keep one environment transition flock, but hold it only while
starting/reconciling the VM, registering an owner, activating a shared service,
finishing, or stopping. Each run separately holds an exclusive flock on its
own owner file for the complete host-process lifetime.

**Rationale**: A PID or mutable JSON record cannot prove liveness. Existing
HostFS read grants already use an open flock as an OS-owned proof
(`internal/hostfs/readgrant/owner.go:18-58`). A per-session adaptation releases
automatically on process death and allows stop to distinguish live, stale, and
unprovable state. The transition lock still serializes attach versus stop and
concurrent startup, but no longer reserves the VM for the target lifetime.

**Alternatives considered**:

- In-memory Manager registry: rejected because independent CLI processes do
  not share memory.
- Require `hideoutd`: deferred to 036; 034 must work in the current daemon-less
  product path.
- Persist PIDs: rejected because reuse and namespace ambiguity make a PID
  weaker than an open OS lock.

## Decision 3: Use The Existing Environment Mount As A Transport Root

**Decision**: Retain one static host runtime mount, but organize it as:

```text
runtime/
├── services/
│   └── network/
└── sessions/
    └── <session-id>/
        ├── bootstrap/
        ├── network/
        ├── shims/
        └── tmp/
```

The host-side durable session/audit layout remains under `sessions/<id>` in
the Hideout store. Only transient guest-consumed files are duplicated beneath
the environment runtime root.

**Rationale**: Lima config is immutable after VM start and currently mounts
the environment runtime directory at `/hideout/session`
(`internal/backend/lima/lima.go:677,773-781`). Switching back to a unique host
session directory without a guest projection would therefore leave the second
run invisible. Child directories preserve the static mount and avoid mounting
the wider Hideout store.

**Alternatives considered**:

- Mount the whole store into the VM: rejected as a control-plane disclosure.
- Copy shims through an ad hoc guest installer: rejected because the existing
  verified static mount already supplies them.
- Clear and reuse the root: rejected because that is the current collision.

## Decision 4: Lima Uses Mount And PID Namespaces, Then Drops Privilege

**Decision**: The Go-owned Lima backend opens its existing root-control SSH
channel, executes a fixed namespace wrapper using `unshare --mount --pid
--fork --kill-child=KILL --mount-proc`, makes mount propagation private,
bind-mounts only
`/hideout/runtime/sessions/<id>` onto `/hideout/session`, starts the session's
HostFS mount there, and finally runs the requested argv as the existing profile
user with a clean environment. The same authenticated transport opens one
additional Core-owned guardian channel. The host emits a fixed heartbeat during
the target lifetime and sends `done` on normal completion. Heartbeat timeout or
transport EOF reads the root-owned guest-ephemeral launcher record and validates
its PID, Linux process start time, and exact session source argv before killing
the namespace parent. `unshare --kill-child=KILL` then terminates the namespace descendants.
The target cannot modify this identity record. The guardian ignores disconnect
signals long enough to process EOF and carries no target input, broker token,
HostFS authority, network secret, or generic command. The workspace mount is
unchanged and remains shared.

**Rationale**: A 2026-07-16 probe against the supported
`developer-standard-2026.07.0` Lima VM observed `unshare`, `mount`, `setpriv`,
`flock`, and `nsenter`, and proved root-control SSH can create mount and PID
namespaces. A bind probe showed the session child inside while the parent mount
remained unchanged outside. This reuses the 009 separation between Go-owned
setup identity and non-root target (`internal/backend/lima/setup.go:19-58`). A
private `/proc` prevents ordinary targets from enumerating sibling process
state.

Preflight, guardian, target, and the final exact-session cleanup proof use four
channels on one authenticated SSH transport. Reopening root SSH for identity
check and cleanup after every short command was rejected after the real
performance Gate reproduced Lima handshake exhaustion. The owning transport
already proves the root-control identity; only transient handshake EOF remains
eligible for a three-attempt bounded retry on paths that genuinely need a new
setup connection.

Guest fixture timing uses Python's monotonic nanosecond clock. A real Gate run
proved that guest wall time can move backwards during Lima time synchronization;
`date +%s%N` is therefore forbidden as performance evidence even when the
target command itself succeeds.

The real Gate initially exposed that `unshare --kill-child` alone does not bind
the namespace parent to abrupt non-PTY SSH transport loss: a host `SIGKILL`
released its owner flock but could leave the guest target alive. A first
EOF-only guardian also failed because a residual sshd session could keep the
remote stdin open after transport loss. The fixed heartbeat makes host-process
loss explicit without trusting implementation-specific sshd EOF behavior. The
Gate proves the target disappears, its sibling survives, and host owner
liveness reconciles within one second.

The primitive probe initially produced a false negative when the OpenSSH CLI reused a
developer-user ControlMaster. Re-running with ControlMaster disabled reached
UID 0 and succeeded. Production Go SSH does not use that OpenSSH control
socket (`internal/backend/lima/ssh_bridge.go:88-119`). The gate must exercise
the production path, not the misleading CLI shortcut.

**Alternatives considered**:

- Per-session UID: rejected for 034 because the same writable workspace is
  intentionally shared and changing file ownership would break current
  developer semantics. It also does not replace mount/PID isolation.
- Mount namespace alone: rejected because the shared `/proc` still exposes
  same-UID sibling process state.
- New guest supervisor binary: rejected for v1 because trusted Go can build a
  fixed quoted wrapper and exact-session guardian over the existing
  authenticated root SSH transport. A helper may be reconsidered only if
  signal/resize work in 037 needs it.

## Decision 5: Guest-Root Remains A Non-Claim

**Decision**: Claim sibling invisibility only for ordinary non-root target
processes. Do not add a user namespace or claim containment after guest-root
escape. Keep dedicated environments as the VM-wall recommendation.

**Rationale**: Root in the shared guest can inspect or join namespaces and
reach the shared workspace substrate. Existing architecture and threat-model
text already reserve this non-claim (`docs/architecture-principles.md:172-181`;
`docs/threat-model.md:125-134`). 034 improves ordinary process/control
isolation without relabeling it as a VM boundary.

**Alternatives considered**:

- Claim root containment from namespace spelling: rejected as an overclaim.
- Hide the limitation from product docs: rejected because it changes the
  operator's isolation choice.

## Decision 6: Environment Networking Is A Fingerprinted Shared Service

**Decision**: Direct networking needs no service. For `tun2socks`, the first
proved owner materializes and starts one environment-level service while the
transition lock is held. Later sessions independently resolve configuration,
compare a secret-free fingerprint, and reuse it only on exact match. The last
proved owner performs cleanup. Raw proxy material never enters the fingerprint
or public status.

**Rationale**: Network mode, resolver, and proxy reference are profile-bound,
while the existing bootstrap changes VM-global routes, `/etc/resolv.conf`,
iptables, and `hideout0` (`internal/network/network.go:345-438,530-621`).
Starting one copy per PID namespace would kill or overwrite a sibling's
network. A hash over canonical non-secret fields plus the resolved secret hash
detects backing-secret drift without publishing the secret.

**Alternatives considered**:

- Per-session network namespace: deferred; it requires veth/routing ownership
  and is unnecessary for same-profile sessions.
- Blindly reuse by profile name: rejected because a secret backing value can
  change while the reference name stays constant.
- Persist a mutable integer refcount: rejected because active owner flocks are
  the authoritative count and recover correctly after host-process death.

## Decision 7: Warm Attach May Reuse A Live Runtime Observation

**Decision**: The first owner performs the existing Lima start, instance
identity, privilege, and runtime checks. A later owner attaching while that
proved owner and matching activation receipt remain live may skip repeated
`limactl start` and full runtime observation, but must establish authenticated
root SSH and validate the session-isolation primitives before activation.

The performance gate measures host invocation to the first line emitted by a
dedicated no-op target fixture. It builds and records the pre-034 base commit
in a separate worktree, uses the same host/runtime/workspace fixture, runs at
least 30 samples after warm-up, and reports median and p95 rather than timing
shell setup or parsing prose.

**Rationale**: Current warm runs repeat `limactl list`, start, instance
inspection, privilege probing, and the runtime observation batch
(`internal/backend/lima/lima.go:368-434`). That dominates the observed multi-
second latency and makes the 2-second concurrent-attach goal impossible. A
live owner ties the receipt to the currently running instance; when no owner
remains, the next run returns to full verification.

**Alternatives considered**:

- Skip all checks whenever Lima says Running: rejected because external
  mutation or stale records could become authority.
- Keep all probes on every attach: rejected because it defeats the primary
  product workflow and adds no independent fact while the same instance is
  already proved live.

## Decision 8: Unsupported Existing Guests Fail Without Record Migration

**Decision**: Preserve existing environment identity and records. If the
running guest cannot prove the required namespace primitives, deny the run
with typed runtime/recreate guidance. Do not silently use the old globally
visible target path, even for a single session.

**Rationale**: The system has no published compatibility population to justify
retaining a second authority path, while the constitution requires unsupported
backend behavior to fail closed. Keeping the record makes the state visible
and recoverable without pretending it is safe to execute.

**Alternatives considered**:

- Run one legacy session but reject a second: rejected because product behavior
  would depend on arrival order and preserve the control-view weakness.
- Rewrite or delete the environment record: rejected because the operator must
  retain inspect/stop/recreate control over existing state.

## Decision 9: Cleanup And Status Derive From The Owner Registry

**Decision**: A finishing run reacquires the transition lock, cleans only its
namespace/data-plane/runtime child, then closes its owner. The environment
stays `running` while any other owner is live and becomes `ready` or `error`
only when the last owner finishes. Explicit stop acquires the same transition
lock and refuses if any owner lock is live. Stale unlocked records are never
counted as active.

**Rationale**: Current finish clears the entire environment runtime and sets
the record ready after every run (`internal/manager/run_environment.go:204-231`).
That is invalid with siblings. One registry also prevents CLI, Manager,
doctor, and stop from inventing separate liveness interpretations.

**Alternatives considered**:

- Trust `record.Status == running`: rejected because a crashed host process
  can leave it stale.
- Let stop terminate all owners: deferred; 034 preserves explicit
  non-destructive stop and fails closed while work is live.

## Decision 10: Preserve Initial PTY Semantics; Defer Dynamic Resize

**Decision**: Direct Go SSH allocates a PTY only when host stdin/stdout are
terminals, applies the initial width/height, streams one SSH session per run,
and restores the host terminal. Non-interactive runs preserve independent
stdout/stderr and exact exit status. SIGWINCH forwarding, full OSC/CSI
fidelity, and theme changes remain 037.

**Rationale**: 034 must prevent streams and signals crossing sessions, but it
does not need to absorb the full terminal product slice. The existing terminal
bridge already establishes raw mode and initial dimensions
(`internal/backend/lima/lima.go:303-359`); the namespace path must retain those
minimum semantics.

**Alternatives considered**:

- Make full terminal fidelity a 034 gate: rejected as orthogonal scope that
  repeats the earlier oversized-batch failure mode.
- Run all SSH commands with PTY: rejected because it merges stdout/stderr and
  changes non-interactive exit behavior.

## Decision 11: No Automatic Stop Or Daemon Adoption In 034

**Decision**: Releasing the last owner leaves the environment warm and ready.
The operator's existing explicit stop remains the only stop path in 034.

**Rationale**: 003 rejected implicit auto-stop because lifecycle actions must
not destroy active work (`specs/003-unified-named-environments/research.md:128-141`).
A future lease-driven last-owner stop can reconcile that concern, but it needs
one lifecycle owner across CLI exits and daemon restarts. That is 036, not a
hidden prerequisite for concurrency.

**Alternatives considered**:

- Auto-start/adopt `hideoutd`: deferred to 036.
- Stop from whichever CLI observes count zero: rejected due to cross-process
  exit/start races.
