# Quickstart: Daemon-Owned Concurrent Run Sessions

<!-- markdownlint-disable MD013 MD060 -->

This guide defines the acceptance/evidence scenarios. It is not a claim that a
pipe-backed local test proves terminal or Lima behavior.

## Prerequisites

- macOS arm64 with supported Lima and the pinned developer runtime;
- a clean package or development build containing the Linux session supervisor;
- a profile and one existing same-workspace reusable environment;
- a real terminal capable of resize events;
- Gate output directory outside the source tree.

## 1. Prove Auto-Start And One Resident Owner

Stop the daemon, invoke a normal non-interactive run, and concurrently invoke a
second run from another process. Inspect daemon and run status.

Expected:

- one daemon lock owner and one Manager runtime become ready;
- both clients use the same daemon instance;
- neither client constructs or executes an embedded backend;
- daemon/auth/start failure returns stable recovery and no target side effect.

Coverage: FR-001, FR-002, FR-003, FR-004, FR-019; SC-001.

## 2. Prove Confirmation Binding

Use a fixture whose plan requires confirmation. Capture the review, mutate a
plan input, attempt stale acceptance, then accept the current review. Repeat
without an interactive client.

Expected:

- stale acceptance never starts the target;
- accepted execution matches the reviewed plan digest;
- missing confirmation defaults to deny;
- terminal text cannot act as confirmation.

Coverage: FR-002, FR-005; SC-001, SC-013.

## 3. Prove Non-Interactive Stream And Exit Fidelity

Run a target that writes distinct binary fixtures to stdout and stderr, exits
0, exits a selected nonzero code, and terminates from a signal.

Expected:

- stdout and stderr remain byte-exact and separate;
- no terminal frame or control data enters either stream;
- client process status represents the exact target completion;
- target success plus cleanup failure returns failure, not false success.

Coverage: FR-010, FR-011, FR-012; SC-002.

## 4. Prove Real PTY Startup And Initial Terminal State

From a real terminal, use the exact product command and a target that emits a
monotonic first-byte marker plus `test -t`, `stty size`, and TERM observations.
Warm the environment, collect at least 20 complete invocation samples, and
calculate p50/p95.

Expected:

- target owns a guest PTY but the SSH channel did not request one;
- initial rows/columns match the host before first render;
- p95 host invocation to first target byte is at most 2.0 seconds;
- no command-name-specific fast path exists.

Coverage: FR-008, FR-009, FR-011; SC-003, SC-004.

## 5. Prove Resize And Full-Screen Use

Run a full-screen fixture and one representative agent/terminal application.
Resize the host repeatedly, enter input, interrupt once, and exit.

Expected:

- only the owning PTY receives each size;
- redraw remains usable;
- Ctrl-C arrives once;
- host terminal state is restored after normal and nonzero exit.

Coverage: FR-010, FR-011; SC-003, SC-012.

## 6. Prove Same-Workspace Concurrency

Start a shell, an agent-like fixture, and a one-shot command simultaneously in
the same existing environment and workspace. Write and read a shared workspace
file from each.

Expected:

- all three use one environment/backend instance;
- no environment-busy error occurs;
- workspace effects are immediate and shared;
- session IDs, runtime children, providers, supervisors, terminals, and process
  views are distinct.

Coverage: FR-013, FR-014, FR-018, FR-019; SC-005, SC-006.

## 7. Prove Sibling Authority Isolation

Give session A HostFS read/staged-write and host-app authority while session B
has none. Probe B's environment, mounts, `/proc`, descriptors, shims, broker,
HostFS, decisions, network state, terminal, and control paths.

Expected:

- B observes zero sibling control/authority state;
- B cannot read/apply A's staged write;
- the host lower file remains unchanged before A's typed apply;
- ordinary-target results are claimed; guest-root results are recorded only as
  the existing non-claim.

Coverage: FR-007, FR-014, FR-015, FR-016, FR-017, FR-018; SC-006, SC-007, SC-009.

## 8. Prove One-Session Cleanup And Sibling Survival

With two active sessions, exit one normally, then repeat by killing one client
while output is active.

Expected:

- only the owning target/process view/providers/runtime child are removed;
- cleanup completes within the documented bound;
- the sibling remains interactive and retains its workspace, HostFS, network,
  terminal, and host-capability state;
- no detached target remains.

Coverage: FR-006, FR-021, FR-022; SC-008.

## 9. Prove Stop Serialization

Attempt environment stop while one and then several sessions are live. Exit all
sessions, prove cleanup, and stop again.

Expected:

- every live or unproved owner refuses stop;
- a new attach racing stop has one serialized winner;
- stop succeeds only after proved zero-session state;
- normal final-session exit alone leaves the environment running.

Coverage: FR-019, FR-020, FR-023; SC-010.

## 10. Prove Daemon Loss

Start two real PTY clients and kill the daemon process without an ordered stop.
Observe clients, guest processes, owner records, and daemon restart.

Expected:

- both clients unblock and restore terminal state;
- both guest supervisors terminate and reap their targets;
- no deliberately headless target remains;
- restart reports stale/unproved state without adopting or deleting it;
- recovery is bounded and explicit.

Coverage: FR-008, FR-022, FR-023; SC-011.

## 11. Prove Credential Rotation And Renewal

Use a clock-controlled short token/lease duration. Keep one authorized session
active through multiple rotations, retain a stale token copy, and try new and
renewal access with both credentials.

Expected:

- the current client renews continuously without daemon restart;
- prior token works only inside bounded grace;
- stale token cannot open or renew after grace;
- failed renewal cancels only its session;
- no operator or per-run token appears in guest, status, event, audit, or
  evidence output.

Coverage: FR-003, FR-004, FR-007, FR-025; SC-013, SC-015.

## 12. Prove Protocol Bounds And Backpressure

Send invalid versions, unknown mandatory frames, wrong-direction frames,
oversized control/data frames, duplicate start/completion, target bytes that
look like frames, and a client that stops reading.

Expected:

- each malformed connection fails closed without target authority or daemon
  failure;
- terminal bytes remain data;
- slow-client behavior is bounded and never silently drops output or reports
  success;
- sibling sessions remain unaffected.

Coverage: FR-003, FR-006, FR-010, FR-012, FR-021; SC-008, SC-013.

## 13. Prove Helper Distribution And Runtime Refusal

Verify package/install/helper manifests, then test missing, wrong-architecture,
stale-digest, unsupported-protocol, and unsupported-PTY fixtures.

Expected:

- the exact package-owned helper is materialized;
- every invalid helper/runtime case fails before target authority;
- no download, workspace helper, profile helper, or generic privileged command
  fallback occurs.

Coverage: FR-008, FR-009, FR-024; SC-001, SC-013.

## 14. Prove One Authoritative Status Model

During preparation, running, cleaning, failure, and stale recovery, compare CLI,
Manager API, daemon events, audit, doctor, TUI/WebUI summary, and schemas.

Expected:

- lifecycle identities/states agree;
- credentials, PIDs, raw control paths, and injected secret fixtures are absent;
- status does not infer liveness solely from persisted JSON.

Coverage: FR-007, FR-023, FR-024; SC-013.

## 15. Prove Documentation Truth And Scope

Run docs-truth checks against README, status, threat model, architecture,
support matrix, test plan, and 034 artifacts.

Expected:

- daemon-owned same-workspace concurrency, dynamic resize, client-loss
  cancellation, and measured latency are claimed only with matching evidence;
- cross-workspace shared default, automatic final-session stop, detach/attach,
  guest-root containment, browser terminals, and complete terminal-emulator
  hardening remain explicit non-claims.

Coverage: FR-017, FR-020, FR-024; SC-014.
