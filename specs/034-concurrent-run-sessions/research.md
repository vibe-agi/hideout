# Research: Daemon-Owned Concurrent Run Sessions

<!-- markdownlint-disable MD013 MD060 -->

## Decision 1: Replace The Old 034 Execution Path In Place

**Decision**: Keep feature number 034 and replace its executable run path. The
existing same-workspace environment, owner-record, runtime-child, namespace,
and shared-network work remains the baseline. Normal `hideout run` no longer
constructs Manager Core or a backend in the invoking process.

**Rationale**: The old 034 proved concurrent session mechanics, but every CLI
process still owned a complete Manager/backend lifecycle. That leaves
cross-process ownership awkward, makes a final-session stop race-prone, and
forces every interactive run through a slow SSH PTY allocation. There is no
external compatibility population requiring two executable paths.

**Alternatives considered**:

- Add a second feature number while retaining old 034: rejected because it
  would leave two contradictory ownership contracts.
- Keep embedded execution as a fallback: rejected because daemon failure would
  silently change authority and cleanup ownership.

## Decision 2: `hideoutd` Is A Resident Role Of The Product Binary

**Decision**: One operator-private daemon is mandatory for executable runs.
`hideout run` connects to it or races safely to auto-start it. The installed
`hideout` executable enters an internal daemon-serving role; 034 does not add a
second host distributable merely to obtain a different process name. Explicit
daemon start/status/stop remain operator controls.

**Rationale**: One composition root avoids package duplication while preserving
the C/S architecture. The daemon already mounts the canonical Manager API and
holds the per-store lock. Auto-start removes daemon administration from normal
use, while the lock and readiness probe ensure concurrent clients converge on
one process.

**Alternatives considered**:

- Require `hideout daemon start` before every run: rejected as product friction.
- Start an in-process daemon goroutine in the client: rejected because it dies
  with the client and does not establish one cross-terminal owner.
- Ship a separate `hideoutd` binary: deferred until packaging or service-manager
  integration proves a concrete need.

## Decision 3: Use A Dedicated Private Session Socket

**Decision**: Keep the existing authenticated HTTP Manager socket for bounded
request/response and SSE surfaces. Add a second 0600 Unix socket under the same
validated store-rooted daemon runtime directory for full-duplex run sessions.
The guest cannot reach either socket. Browser loopback transport never mounts
the session protocol.

**Rationale**: Terminal data needs full duplex, backpressure, half-close, and
long-lived ownership. HTTP handler hijacking would couple terminal correctness
to `net/http`, while SSE cannot carry input or reliable binary output. A
separate socket keeps the authority surface narrow and lets the daemon tie one
connection to one run.

**Alternatives considered**:

- SSE plus POST input: rejected because it introduces polling/races and cannot
  make connection loss a single ownership fact.
- WebSocket over loopback: rejected because browser reachability and origin
  policy are unnecessary exposure for a local terminal.
- Reuse the Manager socket with HTTP Upgrade: viable, but rejected for v1
  because a dedicated listener is simpler to bound, test, and shut down.

## Decision 4: One Bounded Binary Frame Contract

**Decision**: Add `internal/sessionwire` with a versioned, length-prefixed frame
format used on both client-to-daemon and daemon-to-guest-supervisor links. A
frame has a fixed type byte, bounded payload length, and opaque payload bytes.
Structured control payloads use strict JSON; terminal/stdout/stderr payloads
remain raw bytes. Writers serialize frames, readers reject unknown mandatory
types and oversized payloads, and queues are bounded.

**Rationale**: Length framing prevents target-controlled terminal bytes from
becoming control messages. Reusing one codec avoids two subtly different
resize, signal, EOF, completion, and error contracts. Kernel/socket
backpressure is preferable to dropping output; a blocked client can be
cancelled within a fixed bound.

**Alternatives considered**:

- JSON lines for all data: rejected because arbitrary output needs escaping and
  inflates hot-path traffic.
- Terminal escape sentinels: rejected because the target controls those bytes.
- gRPC: rejected as unnecessary dependency and surface area for a private
  single-process protocol.

## Decision 5: Rotate Operator Credentials And Renew Session Leases

**Decision**: Replace the daemon's one-shot static expiry with a credential
manager that atomically rotates the token file, accepts the immediately prior
token only for a short bounded grace period, and validates Manager, SSE, and
session requests through one callback. A connected run receives no backend or
capability credential. Its client periodically re-reads the operator token and
sends a renewal proof; failure to renew before the session lease deadline
cancels only that run.

**Rationale**: The existing 15-minute static token eventually makes every API
unusable until daemon restart. Rotation plus lease renewal supports multi-hour
runs while invalidating copied stale tokens. The operator token remains on the
host client/daemon link and is never forwarded to the guest.

**Alternatives considered**:

- Give tokens a multi-day TTL: rejected because it postpones rather than solves
  rotation and stale-client access.
- Issue a reusable per-run secret to the client: rejected because connection
  ownership plus renewed operator authentication is sufficient.
- Let established sessions run forever after initial auth: rejected because
  credential revocation would have no effect.

## Decision 6: Manager Owns One Canonical Run Service

**Decision**: Introduce one structured run request and one Manager run-service
entry point that performs plan, drift revalidation, confirmation binding, data
plane construction, backend execution, audit, and cleanup. The daemon session
worker invokes it. Existing Manager HTTP run apply delegates to the same
service for non-interactive use. The CLI parses flags and renders confirmation,
but does not load profiles or execute a backend for normal runs.

The HTTP adapter is intentionally not a renewable terminal transport. It
revalidates the credential accepted on that request against the rotating daemon
credential for the run lifetime and cancels when that unrenewed credential
leaves grace. Multi-hour or interactive clients MUST use the session socket and
renew frames. This keeps the route parity-locked without creating an
indefinitely authorized, non-renewable execution path.

**Rationale**: The current CLI supports more run options than `RunAPIRequest`,
including run-scoped HostFS, public environment variables, audit selection, and
preview endpoints. Moving those options into one strict request avoids losing
features in the thin-client path and makes CLI/API parity structural.

**Alternatives considered**:

- Duplicate current `runCommand` orchestration in `internal/daemon`: rejected
  because confirmation and option behavior would drift.
- Have the daemon call back into `internal/app`: rejected because `app` is a
  presentation/composition package, not a lifecycle authority layer.
- Send a shell command string: rejected because argv and typed options must stay
  structured.

## Decision 7: A Fixed Linux Supervisor Owns The Guest PTY

**Decision**: Add the package-owned Linux helper
`hideout-session-supervisor`. The daemon opens authenticated root-control SSH
without requesting an SSH PTY and starts only a Go-built fixed session-view
launcher. The launcher creates the existing private mount/PID view and runs the
fixed helper. The helper validates a strict start payload, starts the target as
the configured non-root user, allocates a guest PTY when requested, applies
window size, forwards signals, reaps descendants, and emits a typed completion.
For non-interactive mode it preserves separate stdout and stderr pipes.

The helper uses `github.com/creack/pty`, a narrow established Go PTY library,
for allocation and window-size changes. It does not introduce a terminal
emulator or parser.

The helper is built, checksummed, packaged, verified, and materialized through
the same helper pipeline as `hideout-shim`, `hideout-hostfsd`, and the DNS stub.
Go module changes are resolved with `go mod tidy`; module files are not edited
by hand.

**Rationale**: Real measurements showed the target itself takes roughly 0.3
seconds, while OpenSSH accepts `pty-req` roughly 3.9 seconds after the request.
A non-PTY SSH exec starts promptly. Allocating the PTY inside the guest removes
that serialization point and gives Hideout direct control over resize and
process-group signaling. A fixed helper is easier to validate than adding more
dynamic privileged shell behavior.

**Alternatives considered**:

- Keep SSH `RequestPty`: rejected by the measured latency and lack of robust
  dynamic resize ownership.
- Use the guest `script` utility: rejected because it does not provide a clean
  structured resize/signal/stdout-stderr protocol.
- Let the client select a helper or privileged argv: rejected as generic guest
  root authority.

## Decision 8: Terminal Mode Is Capability-Based, Not Command-Based

**Decision**: `auto` allocates a PTY only when the client's input and output are
terminals. Explicit `always` and `never` overrides are supported. The client
sends only terminal mode, rows, columns, and a validated bounded `TERM`; it does
not forward host username, paths, theme, shell environment, or arbitrary
terminal variables. SIGWINCH sends resize frames. In raw PTY mode Ctrl-C is an
input byte and is not also synthesized as a signal. Non-PTY host signals use
typed signal/cancel frames.

**Rationale**: Bash, Claude, Codex, and full-screen tools need PTY behavior;
scripts need exact stdout/stderr and exit status. A command-name list would be
incomplete and would add hidden product semantics. Theme switching and broad
OSC/CSI compatibility remain 037, but ordinary terminal response bytes pass
through unchanged.

**Alternatives considered**:

- Always allocate a PTY: rejected because it merges stdout/stderr and changes
  automation behavior.
- Detect known interactive commands: rejected because unknown/community tools
  would be wrong by default.
- Forward the complete host environment: rejected as unnecessary identity and
  control-plane disclosure.

## Decision 9: The Daemon Is The Only Live Session Registry

**Decision**: The daemon keeps the live worker registry and serializes
environment attach/stop transitions. Existing per-session owner flocks and
redacted records remain durable evidence and crash detection, but no independent
CLI process holds them. The environment transition lock is held only for
activation, registration, service transition, finish, and stop; never for the
target lifetime. Same-workspace sessions retain unique runtime children and
the existing direct virtiofs workspace.

**Rationale**: In-memory ownership is now valid because every executable run
uses one daemon. Kernel owner locks still prove process death and support
restart diagnosis. This preserves the useful old 034 concurrency model while
removing competing process owners.

**Alternatives considered**:

- Delete owner records and rely only on daemon memory: rejected because daemon
  crash would erase the evidence needed to fail closed.
- Hold the environment lock for the run lifetime: rejected because it recreates
  the one-session limitation.

## Decision 10: Connection And Daemon Loss Terminate, Never Detach

**Decision**: Each accepted client connection owns one cancellable worker. EOF,
lease expiry, protocol failure, or bounded write failure cancels that worker.
The daemon closes the supervisor transport; supervisor EOF kills and reaps the
target process group; the existing exact-session cleanup proof runs when the
daemon is alive. Daemon shutdown cancels all workers and waits within a bound.
After an unclean daemon death, restart reports stale/unproved owner records and
does not silently re-adopt or destroy them.

**Rationale**: 034 has no detach feature, so continuing without a controlling
client would create hidden work and authority. Binding guest liveness to the
daemon's SSH transport also makes daemon death observable without a separate
SSH PTY guardian channel.

**Alternatives considered**:

- Leave targets running after client loss: rejected as implicit detach.
- Let restart adopt sessions based on metadata: rejected because metadata is
  not liveness or credential proof.
- Delete ambiguous state automatically: rejected because cleanup may not have
  completed.

## Decision 11: Same-Workspace Concurrency Is The 034 Cut Line

**Decision**: 034 supports multiple concurrent sessions only when selection
resolves the same existing environment and pinned workspace. It retains the
current environment after the final session. Cross-workspace shared default is
035, lease-driven final-session stop is 036, and exhaustive terminal/theme/
OSC compatibility is 037.

**Rationale**: The daily user problem is opening a shell and an agent in one
repository. Dynamic cross-workspace transport and automatic lifecycle policy
have different risk profiles. Keeping them out prevents another oversized
multi-feature implementation.

**Alternatives considered**:

- Share one VM across arbitrary workspaces now: rejected because transport and
  weaker isolation need their own evidence.
- Auto-stop after the last worker now: rejected because it changes lifecycle
  policy independently of session ownership.

## Decision 12: Real Terminal Evidence Measures The Whole Product Path

**Decision**: Gate 0 covers wire codec, malformed frames, auth rotation,
auto-start races, Manager parity, terminal client restoration, helper behavior,
session races, crash fixtures, redaction, packaging, and docs. The real macOS
arm64 Lima lane uses an actual PTY and measures host invocation to first target
byte for at least 20 warm samples; p95 must be at most 2.0 seconds. It also
proves resize, two simultaneous interactive sessions, non-interactive stream
separation, client kill, daemon kill, sibling survival, and guest process/mount
isolation.

**Rationale**: Pipe-based tests produced a false sub-second conclusion while
the user's real terminal consistently took about five seconds. Timing an
internal helper or non-TTY path is not product evidence.

**Alternatives considered**:

- Use only Go unit tests: rejected because PTY allocation, terminal restoration,
  Lima SSH, and process namespaces are backend facts.
- Measure only the target command: rejected because users pay client, daemon,
  activation, isolation, and terminal setup latency.
