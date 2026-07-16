# Quickstart: Concurrent Run Sessions

<!-- markdownlint-disable MD013 MD060 -->

These scenarios are the operator-facing acceptance path. Real isolation and
performance results require macOS arm64 with Lima; native runs are mechanics
fixtures only.

The executable mapping is `scripts/test-concurrent-sessions-quickstart.sh`.
Without arguments it runs the local mechanics, backend integration, evaluator,
and release-readiness checks. Completion uses `--require-real --evidence-dir
<retained-034-dir>` so scenarios 1-5, 7-9, 11, and 12 are also bound to the
real isolation/performance artifacts. Scenario 6 is intentionally a
Gate 0/backend integration claim, and scenario 10 is a terminal-backend claim.

## 1. Prepare One Reusable Environment

Use one initialized profile and one workspace. Start a shell so the existing
environment is running. Record its environment and instance IDs.

Expected: the workspace remains the existing static direct mount, the target
is non-root, and the first session reports `running`.

## 2. Start A Concurrent Command

While the shell remains open, run a second one-shot command and a third shell
from the same workspace.

Expected: no `environment ... is already in use` error; all three summaries
name the same environment/instance and distinct session IDs.

## 3. Share Workspace Effects

Create a file in session A, read it in session B, modify it on the host, and
read the new value in session C.

Expected: direct read/write behavior is unchanged and no HostFS decision is
created for workspace activity.

## 4. Prove Runtime And Process Separation

Give session A a recognizable non-secret marker in its session environment
and keep a distinctive process alive. From B, inspect `/hideout/session`,
mounts, `/proc`, descriptors, process command lines, and environment.

Expected: B sees only its own runtime child/process tree and cannot recover A's
marker or control paths. The workspace remains visible by design.

## 5. Prove HostFS Separation

Enable a HostFS read grant and staged write only in A. Probe the same path from
B, then apply the staged write through A's existing decision flow.

Expected: B cannot read, claim, approve, or apply A's authority. The host lower
file changes only after A's typed apply.

## 6. Prove Shared Network Lifetime

Run two sessions with the same privacy profile and verified `tun2socks`
configuration. End A while B continues network and DNS probes.

Expected: one environment service is reported; B remains functional after A
exits; cleanup occurs only after the last owner. A changed secret/configuration
for a new session is refused without exposing either value.

Executable proof: the Gate 0/backend integration tests in
`internal/manager/run_network_concurrent_test.go` cover boot-bound service
state, current-runtime health verification, conflict refusal, sibling
reference counting, and final cleanup. The 034 real Gate 2 lane uses direct
networking and therefore makes only a network non-interference claim.

## 7. Refuse Stop While Active

With two sessions active, plan and apply environment stop through CLI and
Manager API.

Expected: both surfaces refuse from the same owner model, report the active
count and stable recovery guidance, and signal no target.

## 8. Reconcile Abrupt Host Exit

Terminate one host-side `hideout run` process without allowing ordinary
cleanup. Keep the sibling alive and inspect status within one second.

Expected: the killed owner's flock is no longer live, stale JSON is not counted
as active, the sibling remains usable, and the next transition performs bounded
session/service reconciliation.

## 9. Preserve Explicit Stop

Exit all sessions without requesting stop, then inspect the environment and
finally stop it explicitly.

Expected: the VM remains warm/ready after the last exit; explicit stop now
succeeds. No automatic stop, clean, remove, or recreate occurs.

## 10. Exercise Independent Streams

Run two non-interactive commands that interleave unique stdout/stderr markers
and exit with different codes. Then run two PTY shells with different initial
dimensions.

Expected: markers, input, signals, exit codes, and initial dimensions remain
session-specific; the host terminal is restored. Dynamic resize is not claimed.

Executable proof: `internal/backend/lima/session_view_test.go` and the existing
terminal bridge tests exercise independent non-TTY streams, PTY allocation,
initial dimensions, cancellation, and restoration. These are backend
integration proofs, not claims inferred from the non-PTY real Gate 2 script.

## 11. Measure Warm Attach And Workspace Performance

With one owner live, measure at least 30 second-session starts. Measure host
invocation to the first line from a dedicated no-op ready-marker target. Build
the pre-034 commit in a separate worktree and compare Git status plus a package
metadata fixture on the same host, runtime digest, workspace, and warmed VM.
Guest fixture duration uses a monotonic clock; VM wall-clock synchronization is
never accepted as a performance timer.

Expected: marker latency is at most 2.0 seconds p95 and fixture duration is at
most 1.25x baseline. Measurements record both exact commits, runtime digest,
host, instance, sample count, warm-up count, median, and p95.

## 12. Validate Evidence And Claims

Run Gate 0, the 034 real Gate 2 lane, schema validation, docs truth, audit
export, and support/readiness aggregation.

Expected: injected control-plane credentials and sibling paths are absent;
proof binds exact commit/digests; docs reject cross-workspace reuse, automatic
last-session stop, full resize, and guest-root containment claims.
