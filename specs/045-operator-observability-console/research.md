# Phase 0 Research: Operator Observability Console

## Scope and evidence

This research resolves the technical choices needed by
[spec.md](./spec.md). It is based on the current repository, current package
versions as of 2026-07-28, the supported Lima/Debian runtime, and primary
platform documentation. Decisions favor a narrow typed authority, explicit
degradation, and incremental compatibility.

## Decision 1: Keep the current CLI parser, introduce one declarative command

catalog

**Decision**: Do not perform a wholesale Cobra migration in this feature.
Hideout currently dispatches commands in `internal/app/app.go` with a switch
and command-local `flag.FlagSet` values; Cobra is not a dependency. Introduce a
Go-owned command catalog containing task group, path, one-line purpose,
prerequisites, effect, examples, risk, recovery, audience level, aliases, and
handler. Move flag metadata into catalog-owned command builders as each command
is touched. Render primary, contextual, advanced, TUI, and browser help from
that catalog. Keep `help --all` as a compatibility alias, but make
`hideout help all` the visible spelling and make its output grouped and
searchable.

**Rationale**: Information architecture, copyable examples, effects, recovery,
and progressive disclosure are the user problem. A parser replacement would
change every command's edge behavior while still requiring the same content
work. A catalog can eliminate the current manual help drift and incrementally
replace the switch without breaking scripts.

**Alternatives considered**:

- **Migrate everything to Cobra now**: rejected because duplicated flag
  definitions or `DisableFlagParsing` adapters would create two sources of
  truth; a correct all-at-once migration has a large blast radius unrelated to
  the safety console.
- **Only rewrite `help.go` strings**: rejected because hand-written output
  would drift again and could not power TUI/Web contextual help.

## Decision 2: Bubble Tea v2 owns presentation, `liveconsole` remains the

projection

**Decision**: Use `charm.land/bubbletea/v2` v2.0.8,
`charm.land/bubbles/v2` v2.1.1, and `charm.land/lipgloss/v2` v2.0.5. Create an
`internal/tui` package with an Elm-style model, typed messages, reusable panels,
tables, viewport, filter input, status bar, and modal stack. Keep
`internal/liveconsole.State` and its sequence-gap/stale reducer as the shared
domain projection. Default interactive mode uses the alternate screen;
`hideout tui --once` remains a deterministic non-interactive plain snapshot
for scripts and tests.

**Rationale**: Bubble Tea supplies lifecycle, terminal capability handling,
input, resize, and composable update/view behavior. The existing reducer
already correctly seeds from Manager state and detects event gaps. Keeping
domain state outside the view prevents a second source of authority and lets
TUI, WebUI, and CLI parity tests use the same fixtures.

**Alternatives considered**:

- **Continue manual ANSI rendering**: rejected because focus, resize, modal,
  scrolling, accessible color fallback, and reliable event handling would all
  remain bespoke.
- **Put Manager calls directly in components**: rejected because components
  would mutate state without a canonical plan and become hard to test.

Primary references:

- <https://github.com/charmbracelet/bubbletea>
- <https://pkg.go.dev/charm.land/bubbletea/v2>

## Decision 3: A profile revision and operation ledger form the mutation

protocol

**Decision**: Introduce a canonical `ProfileProjection` digest/revision and a
single `ProfileTransactionService`. `Plan` validates a client draft against
the current revision and returns a canonical plan digest, effects, blockers,
restart posture, and a server-issued operation ID. `Apply` accepts the
operation ID, plan digest, and expected revision. Under the existing profile
mutation lock it replans, compares the canonical digest, and either:

1. returns the stored terminal result for an already-completed operation;
2. rejects a stale/different plan with no effect; or
3. records ownership, performs typed effects, commits a new revision and
   terminal result, then publishes one event.

Operation records are bounded and contain no secret values.

**Rationale**: The existing init service already demonstrates canonical review
digests and replan-under-lock. Generalizing that pattern provides CAS and
response-loss safety across CLI, TUI, and WebUI without exposing file writes.

**Alternatives considered**:

- **Compare only before-state values**: rejected because independent fields
  and clients can race without one authoritative revision.
- **Client-generated plan JSON as authority**: rejected because a stale or
  modified client could change effects after review.
- **Hold a lock while a modal is open**: rejected because clients can
  disconnect and block all operators.

## Decision 4: macOS Keychain is the secret source of truth

**Decision**: Add a narrow `secrets.Store` interface. On macOS, implement it
with Security.framework generic-password items using build-tagged cgo:

- service: `com.vibe-agi.hideout.secret`;
- account: validated Hideout secret reference;
- value: opaque bytes;
- accessibility: after first unlock, this device only;
- metadata exposed to Manager: reference, availability, generation, updated
  time, and provider reason only.

The daemon opens the store on demand, so a value can be added or rotated while
it is running. Secret creation/update reads from a TTY or stdin, never an argv
flag. Startup environment variables remain a deprecated, read-only
compatibility fallback for one release and are never copied into profile or
activity storage. Tests use an in-memory provider; non-macOS builds return a
typed unsupported capability.

**Rationale**: The failure reported by the user occurs because the daemon
snapshots its own environment at start. Keychain provides operator-owned,
encrypted-at-rest storage and live resolution without daemon restart. Direct
Security.framework calls avoid putting the value in a `security` subprocess
argument.

**Alternatives considered**:

- **Keep daemon environment variables**: rejected because process environment
  is immutable from the child daemon's perspective and encourages shell
  history/config leakage.
- **Invoke `/usr/bin/security -w <value>`**: rejected because the value can
  appear in process arguments.
- **Store encrypted blobs with an app-managed key**: rejected because it merely
  moves the master-secret problem into Hideout.

Primary references:

- <https://developer.apple.com/documentation/security/keychain-services>
- <https://developer.apple.com/documentation/security/ksecclassgenericpassword>

## Decision 5: Network reconfiguration is a live transactional effect

**Decision**: Reuse the current environment network service's live route
pointer and connection binding. A connection plan stages a resolved secret
generation and candidate DNS/proxy service, performs non-secret reachability
and protocol checks, atomically activates the desired route for new
connections, and proves effective state. Existing accepted connections keep
their bound route. Failed validation/activation/proof rolls back the desired
and effective route when safe; otherwise it records `failed` with exact
recovery. Posture changes that cannot preserve active sessions return explicit
blockers instead of stopping the daemon or recreating the VM.

**Rationale**: The code already supports gateway and DNS service actions and
preserves existing connections across route-pointer updates. The missing
pieces are live secret resolution, a canonical transaction, and surfaced
stage/activate/rollback evidence.

**Alternatives considered**:

- **Require `hideout daemon stop`**: rejected because daemon restart is an
  artifact of environment-based secret injection, not a network requirement.
- **Recreate the VM for every proxy change**: rejected because eligible route
  changes are already runtime-scoped and existing connections can drain.

## Decision 6: A per-session cgroup is the workload identity

**Decision**: The privileged guest launcher creates one non-delegated cgroup v2
leaf under a Hideout-owned subtree for each session. It opens the leaf and uses
Go's `syscall.SysProcAttr.UseCgroupFD`/`CgroupFD` clone3 path to place the
non-root target into the cgroup atomically at exec. Descendants inherit
membership. The supervisor and observer stay outside the leaf. The session
wire readiness message carries cgroup identity and observer coverage, and
cleanup proves the leaf empty before removal.

Stable execution identity combines environment incarnation, session ID, guest
boot ID, cgroup kernel ID, PID/TID, and monotonic exec sequence/start time.
Guest-root or cgroup delegation explicitly degrades tamper coverage.

**Rationale**: A process tree inferred only from parent PID races with fast
fork/exec and reparenting. A non-delegated cgroup is inherited by all
descendants, excludes unrelated services, and is directly usable by cgroup BPF
hooks.

**Alternatives considered**:

- **Observe the top PID and recursively scan `/proc`**: rejected because it
  loses short-lived children and is ambiguous across PID reuse.
- **Put the supervisor in the target cgroup**: rejected because its own control
  and cleanup traffic would be falsely attributed.
- **One VM-wide workload**: rejected because concurrent sessions require zero
  cross-attribution.

Primary references:

- <https://docs.kernel.org/admin-guide/cgroup-v2.html>
- <https://docs.ebpf.io/linux/program-type/BPF_PROG_TYPE_CGROUP_SOCK_ADDR/>

## Decision 7: A packaged hybrid observer provides evidence and explicit

fallbacks

**Decision**: Package `hideout-observer` in the runtime image. Its Go userland
uses `github.com/cilium/ebpf` v0.22.0 and embeds build-time CO-RE objects. No
compiler or downloaded program is used at runtime.

The supported provider uses:

- tracepoints/fentry for fork, exec, exit, file descriptor and byte
  aggregation;
- cgroup sock-address hooks for connect/sendmsg actor and destination;
- cgroup skb plus socket-cookie correlation for DNS query/response metadata;
- tracing/LSM file hooks for resolved identity, open/access/write/metadata,
  mmap, unlink, rename, and path reconstruction;
- ring buffers with sequence and per-CPU loss counters.

Every hook is probed. Missing BTF, helper, BPF LSM, cgroup hook, privilege, or
queue capacity changes only the affected subsystem/interval to `Partial` or
`Unavailable`. A fanotify provider can retain useful file metadata when the
full file hook set is unavailable, but it never claims full coverage because
fanotify may merge events, overflow, and miss memory-mapped access.

The observer only emits evidence; it cannot approve, deny, or alter workload
operations.

**Rationale**: eBPF provides cgroup-aware, low-overhead attribution for
short-lived descendants. A compiled, embedded artifact is reproducible and
fits existing helper packaging. The fallback preserves usefulness while
keeping coverage honest.

**Alternatives considered**:

- **`strace`/ptrace every process**: rejected for overhead, behavioral impact,
  and incomplete descendant/race handling.
- **auditd**: rejected because it is VM-global, policy-heavy, and difficult to
  isolate safely between concurrent sessions.
- **fanotify alone**: rejected because its documented gaps cannot support the
  full file-coverage claim.
- **One observer process per target inside the cgroup**: rejected because it
  attributes observer traffic to the workload and gives the target more
  opportunities to tamper.

Primary references:

- <https://github.com/cilium/ebpf>
- <https://pkg.go.dev/github.com/cilium/ebpf>
- <https://docs.kernel.org/bpf/prog_lsm.html>
- <https://man7.org/linux/man-pages/man7/fanotify.7.html>

## Decision 8: DNS/domain attribution is evidence-graded

**Decision**: Record the process and destination IP/port at connect time.
Parse only DNS metadata needed for query name, type, response addresses, TTL,
and response code from workload-cgroup DNS traffic; discard packet bytes
immediately. Correlate a domain to a connection only within the same
session/execution and a bounded TTL-aware interval:

- `exact`: same socket or validated proxy protocol supplies the name;
- `inferred`: a unique recent DNS answer for the same execution maps to the
  destination;
- `unknown`: literal IP, encrypted/external resolver, cache ambiguity, shared
  address, unsupported proxy, or missing event.

Never infer from a VM-global cache. SOCKS/HTTP proxy target metadata may be
parsed transiently only by a versioned, positively identified protocol parser;
otherwise report the proxy endpoint and `mediated/unknown`.

**Rationale**: DNS and connections are not inherently one-to-one. Explicit
evidence grades let users see useful domains without turning temporal
coincidence into fact.

**Alternatives considered**:

- **Label the most recent DNS name as exact**: rejected because CDNs, caches,
  concurrent children, and shared IPs create false attribution.
- **Persist packet payloads for later analysis**: rejected by the privacy
  boundary.

## Decision 9: Redact before persistence, preserve local paths

**Decision**: The activity ingestion boundary receives a per-operation
redaction snapshot containing known managed secret byte sequences and the
global control-token registry. Before an event can be serialized, a
deterministic Go redactor:

1. removes exact managed/deprecated secret values, including supported encoded
   forms and values split from recognized flags;
2. strips URI userinfo;
3. replaces authentication headers/fields, sensitive flags, and sensitive
   query parameters;
4. removes session/daemon/control capability tokens;
5. bounds and marks truncation.

Guest and host paths remain visible in authenticated local activity views.
Export/share is a separate reviewed plan with stricter host-path policy.
Unknown argv/path text is disclosed as potentially sensitive rather than
classified by guesswork. A redaction failure drops the record and degrades
coverage; it does not persist raw input.

**Rationale**: The existing local audit intentionally preserves arbitrary
user data. Silently changing it would violate its contract. A separate
presentation-safe activity plane can enforce a stronger pre-persistence
boundary while still answering “who touched what.”

**Alternatives considered**:

- **Redact only when rendering**: rejected because raw credentials would
  remain at rest and in indexes.
- **Hide every path**: rejected because paths are central to local
  investigation and the user explicitly accepts local path visibility.
- **Attempt generic secret classification**: rejected because false confidence
  is worse than a visible privacy disclosure.

## Decision 10: Store bounded, checksummed activity segments on the host

**Decision**: The daemon owns the activity store. Raw kernel events cross an
authenticated, bounded observer stream and are normalized, redacted, and
aggregated before disk. Records use length-framed canonical JSON with CRC32C;
sealed segments receive a SHA-256 manifest entry. The index contains only
non-secret time/session/execution/type/path-host/domain/IP/risk fields.

The frozen v1 limits are an 8 MiB active segment, 256 MiB per exact
incarnation, and 1 GiB total across retained incarnations. T156 measured the
real workload and pressure-test quota path; T157 binds the selected values,
aggregation windows, and risk thresholds to
`internal/workloadobs/defaults.go` and `docs/activity-observation.md`. A future
change must rerun the same raw-sample gate while retaining the invariant that
quota overshoot is at most one active segment. Oldest sealed segments prune
first and produce a coverage interval with reason `retention-pruned`.

Reusable activity belongs to
`(environmentID, backend incarnation/boot identity)`. Disposable activity
belongs to the session. Lifecycle cleanup deletes only that exact owner and
records/validates absence. Files and directories are 0600/0700.

**Rationale**: Framed append-only files tolerate abrupt daemon loss, are easy
to inspect and repair, and avoid a cgo or large embedded database dependency.
Aggregation controls volume before persistence. Exact ownership makes deletion
safe across reusable slot recreation.

**Alternatives considered**:

- **SQLite**: deferred because the query scale does not yet justify an
  additional database/migration/recovery authority.
- **One unbounded JSONL file**: rejected because torn writes, quota, targeted
  cleanup, and index integrity would be difficult to prove.
- **Store inside the guest**: rejected because the workload could tamper with
  evidence and VM cleanup could race export.

## Decision 11: Coverage is an interval state machine, not a badge

**Decision**: Track process, file, network, and DNS coverage independently as
`Available`, `Partial`, or `Unavailable`, with stable reason, evidence, start
sequence/time, and optional end. `Available` starts only after target-cgroup
registration, all required hooks are attached, and the observer-ready
handshake succeeds. Sequence gaps, ring loss, parser errors, schema mismatch,
observer/daemon restart, unproved actor/path, quota pruning, or capability loss
close the interval before a later UI projection may claim completeness.

Observation remains detective: a run may continue with reduced coverage, but
the pre-run review and live HUD must show it. A future “require full
observation” enforcement policy is outside this feature.

**Rationale**: A single current boolean would hide historical gaps and could
turn a restart into false completeness. Intervals preserve exactly when and
why evidence is incomplete.

**Alternatives considered**:

- **Fail every run if one observer is unavailable**: rejected because
  observation is not currently an enforcement authority and unsupported
  backends must remain usable with honest degradation.
- **Show only cumulative drop counts**: rejected because users could not know
  which activity interval is affected.

## Decision 12: Formal models divide configuration, secrets, and observation

**Decision**: Add three bounded TLA+ modules:

- `OperatorConfiguration`: multi-client draft/plan/revision/CAS,
  operation ownership, response loss, idempotent retry, transition, rollback,
  and disconnect;
- `SecretTransition`: secret availability/generation, route stage/activate,
  active-connection preservation, failure, and rollback;
- `WorkloadObservation`: cgroup membership, event sequence/loss, aggregation,
  coverage intervals, retention, exact-owner cleanup, observer tail drain,
  exact final-counter receipt, authenticated clean EOF and successful bridge
  exit after durable goodbye,
  forced-close degradation, and stale projections.

Extend request workflow refinement to connect terminal operation evidence and
decision-lease release. TLC configurations check invariants and liveness under
explicit weak-fairness assumptions. Go tests replay model-style traces against
the production state machines.

**Rationale**: One monolithic model would create an intractable state space and
blur independent authority boundaries. Shared constants and refinement
fixtures connect the modules without merging their state.

**Alternatives considered**:

- **Only add Go concurrency tests**: rejected because rare retry/crash
  interleavings and progress assumptions need exhaustive bounded exploration.
- **Only check invariants**: rejected because the goal includes completion,
  lease release, rollback, and cleanup progress.

## Decision 13: Release claims are evidence-gated

**Decision**: Build the feature behind explicit capability/status labels until
all applicable gates pass. Required evidence includes:

- model checks and Go refinement;
- unit, race, fuzz/property, mutation, and negative fixtures;
- PTY resizing/input/stale-mode and WebUI parity;
- real Lima concurrent session/PID-reuse/drop/crash/cleanup workloads;
- Keychain locked/unlocked/update/rotation and online proxy transition;
- dependency/license/advisory scan, package manifest, clean install,
  upgrade/uninstall, and performance;
- final code review with no required reduced or stale result.

The workflow may create a local artifact and evidence manifest. It must not
create a remote tag, GitHub Release, or Homebrew publication without a new
explicit operator instruction.

**Rationale**: A polished screen is not proof of observation, privacy, cleanup,
or recovery. The constitution requires executable evidence for each new claim.

## Decision 14: Reproducibility is checked before signing

**Decision**: The local package gate derives `SOURCE_DATE_EPOCH` from the
exact clean commit, propagates it into the host binary and every helper
manifest, normalizes package modes and mtimes, writes a sorted owner-normalized
ustar stream, and uses a timestamp-free gzip header. It builds twice with
independent Go caches and requires byte-identical archives, manifests, and
inventories. The accepted local artifact is then verified independently for
its complete file set, package and helper digests, embedded browser assets,
runtime catalog/contract/artifact identity, and every final Go binary.

Developer ID signing and notarization remain a later exact-byte observation:
timestamped external signatures are not treated as reproducible unsigned build
output. The signing workflow stages once, signs the frozen Mach-O files, and
finalizes once; later evidence must bind those resulting bytes instead of
rebuilding them.

**Rationale**: Comparing only a generated manifest can miss build-ID,
filesystem-metadata, archive-order, or gzip-header drift. Rebuilding after
signing would instead invalidate the exact signature/notarization identity.

## Resolved unknowns and deferred items

There are no specification blockers. T156/T157 used historical diagnostic
measurements to freeze the v1 storage defaults, aggregation windows, and rule
thresholds; accepted T156 evidence still requires the current explicitly
confirmed quiet-host thirty-sample rerun, a passing automatic sustained-host-
contention preflight, private host-state diagnostics, and both the paired
median and its exact one-sided 95% upper confidence bound to remain within ten
percent. The paired data remains unfiltered: bounded one- or two-sample host
transients are retained and absorbed by the counterbalanced thirty-pair
distribution, while three consecutive threshold hits by the same external
PID/name invalidate the run as sustained contention. T158 froze the clean,
reproducible unsigned package boundary.
Changing either requires fresh evidence but does not alter the surrounding
safety contracts. Mandatory
blocking based on observation, remote multi-operator access, encrypted-DNS
decryption, full PTY recording, arbitrary secret classification, and a
complete Cobra migration are explicitly outside this feature.
