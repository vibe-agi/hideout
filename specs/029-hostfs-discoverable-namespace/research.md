# Research: HostFS Discoverable Namespace

<!-- markdownlint-disable MD013 -->

## Decision 1: Model Visibility As A Separate HostFS Operation

**Decision**: Add `hostfs.OpDiscover` and evaluate it separately from
`OpStat`, `OpRead`, and `OpList`. Parse `see:`, `see-dir:`, and `see-tree:` into
`OpDiscover` rules with exact, directory, and recursive-directory scopes. Do
not normalize discover into stat.

**Rationale**: `internal/hostfs/hostfs.go` currently normalizes read/list into
stat and `Service.Stat` returns real size, mode, and time. Reusing stat as the
visibility authority would make coarse metadata impossible to enforce and
would blur a deliberate information boundary. A dedicated operation keeps the
existing policy source/subject/TTL/deny machinery while allowing a result shape
below content/stat authority.

**Alternatives considered**:

- A global `none|landmarks|tree` mode: rejected because it cannot express one
  visible root while the rest remains hidden and would create a second policy
  engine.
- Reuse `stat`: rejected because current stat is full metadata authority.
- Add a separate visibility config object: rejected because it would duplicate
  HostFS precedence, plan/apply, source, subject, TTL, and audit behavior.

## Decision 2: Use Explicit Selector Semantics And Batch Legacy Migration

**Decision**: `see:` exposes exactly one node, `see-dir:` exposes one level,
and `see-tree:` exposes lazy recursive names. V1 rejects all discover globs.
New `list:` input and stored legacy list-only rules return a typed migration
error. Migration is a single typed `profile/hostfs/plan|apply` operation named
`migrate-list` with a mapping for every legacy rule:

```text
hideout profile fs <profile> migrate-list \
  --map <rule-id>=see-dir [--map <rule-id>=see-tree ...] \
  --reason <text>
```

The plan must cover every legacy `OpList` rule, show old versus new name and
metadata disclosure, and require confirmation. Apply reads the raw profile
with strict JSON decoding, accepts only the known legacy-list validation
condition, replaces all mapped rules in memory, performs full current profile
validation, and writes once under the existing profile mutation lock.

**Rationale**: `ParseRuleSpec` currently accepts `list:` as `OpList` plus
directory scope, while `Store.Load` immediately calls full profile validation.
Silently aliasing list to see-dir broadens both names and metadata. Migrating
one rule at a time cannot work if loading rejects the remaining legacy rules,
so an all-covered batch is the smallest fail-closed repair path.

**Alternatives considered**:

- Alias `list:` to `see-dir:`: rejected as a silent disclosure expansion.
- Keep `list:` indefinitely: rejected because two overlapping directory-name
  semantics would remain public.
- Directly edit `profile.json`: rejected because it bypasses Manager planning,
  validation, locking, and audit.

## Decision 3: Compile Visibility Through A Dedicated Policy Result

**Decision**: Add a Go-owned visibility evaluation result that distinguishes
`hidden`, `exact-visible`, `enumerable`, and `content-granted`, including the
winning rule ID/source and whether the path is inside an explicit discover
domain. Reserved roots are absolute. Explicit discover denies beat discover
allows and suppress broad enumeration/read proposals, but do not revoke a
separately applicable exact content grant outside reserved roots or the exact
lookup needed to use it. Existing operation grants continue to materialize
only the exact nodes, staged nodes, and synthetic ancestors they already
require.

**Rationale**: `EffectivePolicy.Decide` currently answers one operation and
`Service.syntheticDir` derives ancestors from grants. A typed visibility result
prevents scattered combinations of `Decide(OpStat)`, `Decide(OpRead)`, and
`Decide(OpList)` from disagreeing about the new three-state contract. The
explicit-domain bit is also required to preserve legacy unauthorized-operation
collapse outside `see*` policy.

**Alternatives considered**:

- Infer visibility separately in each service method: rejected because stat,
  list, read, and write would drift.
- Change all legacy visible nodes to EACCES: rejected because 029 promises no
  target-visible regression for profiles without explicit discover rules.

## Decision 4: Bound Namespace Construction And Return Complete Or Error

**Decision**: Set public V1 limits to 4096 entries per directory, 32 relative
components below a `see-tree:` root, and four concurrent host enumeration calls
per session. Discovery is lazy. A list reads the complete candidate set, uses
no-follow metadata inspection for every included child, and returns
`hostfs.directory.incomplete`/`EOVERFLOW` if a limit is exceeded or any child
cannot be classified. Exact-visible directories return
`hostfs.directory.not-enumerable`/`EACCES`.

**Rationale**: `Service.List` currently skips `DirEntry.Info` failures and can
therefore report a false complete list. An ordinary tool cannot distinguish
that from genuine absence. Fixed limits bound host work and make denial
deterministic; 4096 is sufficient for normal user directories while stopping
accidental huge namespaces, 32 components covers normal source trees, and four
concurrent calls bounds attacker-controlled host metadata work without making
single-process traversal serial.

**Alternatives considered**:

- Silent truncation or sentinel entries: rejected because both corrupt
  existence semantics for ordinary filesystem clients.
- Eager indexing: rejected for latency, TCC, privacy, and freshness reasons.
- Unbounded traversal: rejected as an attacker-controlled host resource path.

## Decision 5: Share One Categorized Sensitive-Root Catalog

**Decision**: Extract the roots currently assembled by
`workspaceSensitiveRoots` in `internal/manager/run_plan.go` into
`internal/hostpathrisk`. Each entry has a category and applicable boundary:
`home-boundary`, `control-plane`, `credential`, `browser`, or `system-key`.
Workspace validation consumes all relevant entries. `hostfs.Build` compiles
control-plane/credential/browser/system-key entries into every effective policy
that contains a discover grant, including manually authored rules; presets do
not own this enforcement. The whole-home `home-boundary` entry is not a
visibility deny. Reserved control-plane roots remain absolute. Explicit
operator-authored exact content grants outside reserved roots retain their
existing authority but do not put discover-denied names back into parent
enumeration.

**Rationale**: The existing list already covers `.ssh`, `.aws`, `.kube`,
Keychains, and browser profiles, but also includes the entire home specifically
to prevent using it as a workspace. Reusing the unclassified list would make a
home-tree preset empty; copying selected paths would create drift.

**Alternatives considered**:

- A new visibility-only list: rejected as a second hand-maintained security
  catalog.
- Force-hide every workspace-sensitive root: rejected because whole-home has a
  different threat and explicit exact content grants are constitutionally
  operator-authoritative outside the reserved store.

## Decision 6: Make Typed Broker Errors The Only Errno Authority

**Decision**: Add an optional top-level `error` object to `broker.Response` and
`schemas/broker-envelope.schema.json` with `code`, allowlisted `errno`,
`retryable`, optional public `decisionRef`, and optional bounded
`retryAfterMs`. HostFS failure responses require this object. The Linux helper
maps only known code/errno pairs and maps malformed or unknown records to EIO.
After host/helper packaging is aligned, remove stderr string matching as an
errno authority; stderr remains human context only.

**Rationale**: `cmd/hideout-hostfsd/main_linux.go` currently derives ENOENT and
EROFS by matching `Response.Stderr`. That cannot safely represent hidden,
locked, incomplete, prerequisite, and capacity states. The broker envelope is
already the authenticated host-to-helper contract and the schema is packaged,
so an additive typed object is the correct compatibility boundary.

**Alternatives considered**:

- Add more stderr strings: rejected as non-typed, localization-sensitive
  authority.
- Pass arbitrary numeric errno: rejected because compromised/malformed input
  could inject unintended kernel behavior.
- Create a second HostFS transport: rejected because the existing broker is
  already authenticated and audited.

## Decision 7: Keep FUSE Presentation Separate From Enforcement

**Decision**: Keep `NullPermissions: true`, direct I/O for file reads, and
broker authorization on every content operation. Set FUSE `EntryTimeout` and
`AttrTimeout` to one second and `NegativeTimeout` to zero. Locked nodes use
deterministic coarse attributes (size zero, zero timestamps, stable kind), but
an active content grant returns ordinary real metadata. Cache state never
authorizes content.

**Rationale**: The current helper already uses `NullPermissions` and direct
I/O. Enabling `default_permissions` or encoding locks as mode `0000` would let
the kernel deny after approval until cache expiry and would make mode bits an
unintended policy source. A one-second positive TTL meets the spec's bounded
presentation convergence; zero negative TTL prevents a formerly hidden lookup
from remaining negative after an explicit policy/run change.

**Alternatives considered**:

- Stable coarse attrs even after approval: rejected because common tools use
  size/time after content authority exists.
- Zero TTL for every entry/attr: rejected as unnecessary host/broker traffic.
- `default_permissions`: rejected because policy belongs to the broker.

## Decision 8: Keep Read Approval As An Immediate-Deny Provider Workflow

**Decision**: Add decision kind `hostfs.read`. An eligible broker read returns
EACCES immediately and asks a Manager-owned provider to create or return one
decision keyed by session ID, canonical path, and operation. The decision
times out after five minutes with default deny. Approval can grant only the
exact canonical regular file to the source session. There is no content
preview, directory escalation, profile mutation, or blocking syscall.

**Rationale**: Human response time is incompatible with the existing 10-second
hostfsd broker timeout and normal POSIX expectations. The generic decision
center already supplies claim, lease, timeout, audit, live events, and
authenticated CLI/WebUI routes. Five minutes matches the existing HostFS write
and evidence-share decision posture.

**Alternatives considered**:

- Block FUSE read until approval: rejected because it would hang tools, create
  retry storms, and require a new long-lived RPC lifecycle.
- Approve a directory: rejected because it silently converts an observed file
  request into broad content authority.
- Mutate the profile: rejected because session-local recovery must not become
  durable policy.

## Decision 9: Persist Provider State Under A Cross-Process Lock

**Decision**: Add `internal/hostfs/readgrant` with a provider state file and an
active-grant manifest beneath `sessions/<id>/hostfs-read/`. A provider lock
serializes create, limits, terminal memory, reopen, and apply across processes;
broker readers take a shared lock. Decision IDs are opaque deterministic hashes
of session/canonical-path/operation. Limits are eight pending decisions and
eight new decisions per rolling 60 seconds per session. Target reason text is
plain UTF-8, maximum 512 bytes, marked untrusted, and redacted before storage or
rendering.

**Rationale**: The generic decision store has file locking, but provider rate
history, deterministic key ownership, grant activation, and reopen validation
are provider semantics. Process-local mutexes are insufficient because the run,
CLI, daemon, and WebUI can be separate processes. A private session directory
keeps state out of the guest and lets cleanup revoke all authority.

**Alternatives considered**:

- Store limits in broker memory: rejected because a daemon/CLI process could
  bypass or forget them.
- Create a new random decision per retry: rejected as an approval-flood path.
- Use the generic decision JSON as the grant itself: rejected because it does
  not prove canonical path, operation, session ownership, or activation state.

## Decision 10: Prove Session Liveness With A Held Owner Lock

**Decision**: `StartRunDataPlane` creates and exclusively locks
`sessions/<id>/hostfs-read/owner.lock` for the lifetime of the data plane. A
separate approval/reopen process proves liveness only when its nonblocking
exclusive lock attempt fails with lock contention and the session/endpoint
metadata matches. If the lock can be acquired, is missing, is unreadable, or
ownership cannot be proven, apply/reopen fails closed. The owner lock and all
read-provider files are ephemeral cleanup paths.

**Rationale**: The existing broker endpoint file can remain after abnormal
termination and is therefore not proof that a run owns live resources. An OS
advisory lock is released on process exit, works across CLI/daemon processes,
and introduces no heartbeat, watcher, polling loop, or new control route.

**Alternatives considered**:

- Endpoint-file existence: rejected as stale-state re-adoption.
- PID files: rejected because PIDs can be reused and do not prove control-plane
  ownership.
- Heartbeats: rejected because they add background polling and ambiguous grace
  windows.
- Authenticated broker control route: valid but larger; rejected for V1 because
  the accepted contract prefers private atomic artifacts and check-before-deny.

## Decision 11: Activate Grants With Check-Before-Deny

**Decision**: The active grant manifest is versioned and atomically replaced.
Each entry binds session, decision ID/revision, operation=`read`, requested path,
canonical path, visibility grant ID/source, issue time, and expiry. Approval is
serialized under the provider lock, revalidates the claim, current policy,
reserved roots, regular-file type, canonical path, and owner lock, emits the
required audit, then publishes an active manifest. Broker read takes the shared
lock, re-canonicalizes the request, validates the manifest, and checks it before
returning any otherwise approval-required or terminal denial. Grant expiry is
24 hours after approval or session end, whichever comes first.

**Rationale**: The HostFS service currently captures a fixed policy at data
plane start. Check-before-deny makes approval visible on the next retry across
processes without a watcher or restart and keeps stale allow impossible. A
24-hour hard cap bounds forgotten long-running sessions while covering normal
and overnight developer runs.

**Alternatives considered**:

- Watch the file: rejected because event loss and stale caches could delay or
  preserve authority.
- Poll in the background: rejected by the no-polling product direction.
- In-memory callback: rejected because operator approval may occur in another
  process.
- No expiry until cleanup: rejected because abnormal cleanup failure would
  leave an unnecessarily durable authority artifact, even though session IDs
  are not re-adopted.

## Decision 12: Make Reopen A Provider-Specific Typed Action

**Decision**: Extend the shared Manager route inventory with authenticated
`POST decision/reopen` and `POST decisions/{id}/reopen`. The request carries
decision ID, expected decision version, and reason; only terminal
`hostfs.read` decisions support it in V1. The provider checks the live owner
lock, clears no active grant, creates a new revision and five-minute deadline,
and emits audit/live events. Target retries cannot reopen.

**Rationale**: Terminal memory is needed to stop repeated denied requests from
harassing the operator, but it must not become permanent operator lockout.
Reopen is authority-changing and therefore belongs in Manager API/CLI/WebUI,
not in the broker or daemon event channel.

**Alternatives considered**:

- Let the target retry recreate a decision: rejected as an abuse bypass.
- Generic reopen for every decision kind: rejected because other providers
  have different side effects and terminal-state semantics.
- Reuse claim: rejected because a terminal decision cannot be safely claimed.

## Decision 13: Expand Presets Into Typed Init/Profile Plans

**Decision**: Add visibility selection `none|landmarks|home-tree` to InitTask
and profile-template requests, plans, review lines, evidence, and profile
metadata. Omitted noninteractive selection is `none`. Interactive onboarding
may recommend `landmarks` but applies only after the existing final profile
confirmation. Landmarks expand to `see-dir:` for selected Desktop, Documents,
and Downloads roots that exist or are explicitly selected; no eager scan is
performed. `home-tree` requires an explicit
`--acknowledge-hostfs-name-disclosure` flag and expands to one `see-tree:` rule
plus categorized discover denies.

**Rationale**: Init already uses typed plan/review/apply and refuses hidden
noninteractive choices. Presets are convenience policy, not a new runtime mode.
Making the acknowledgement part of the plan prevents broad metadata disclosure
from being smuggled through config or a template default.

**Alternatives considered**:

- Enable landmarks in privacy by default: rejected as a change to the existing
  zero workspace-external HostFS posture.
- Put preset logic in the helper: rejected because operator policy belongs to
  host Core.
- Eagerly verify roots during init: rejected because it can trigger TCC and
  violates lazy access.

## Decision 14: Treat TCC As An Explicit Host Prerequisite Probe

**Decision**: Normal `doctor --feature hostfs` reports configured protected
roots as `unknown/unprobed`. Add optional
`--probe-hostfs-root <absolute-root>`; it requires the HostFS feature, prints a
warning to stderr before host access, performs one bounded no-follow
enumeration probe, and records observed facts. Permission failure becomes
`hostfs.host.prerequisite-failed` and never creates a read decision. Doctor
does not claim to repair or silently determine macOS consent.

**Rationale**: There is no truthful portable API that silently predicts a TCC
prompt or consent state. Probing Desktop/Documents/Downloads can itself trigger
the OS dialog, so operator intent must be explicit and evidence must distinguish
unprobed from observed failure.

**Alternatives considered**:

- Probe during init: rejected because first run would cause an unexplained OS
  prompt.
- Treat EACCES as a Hideout decision: rejected because operator approval inside
  Hideout cannot repair host OS consent.
- Infer from filesystem mode bits: rejected because TCC is not represented
  reliably by those bits.

## Decision 15: Reuse Generic UI State, Add Only Reopen Wiring

**Decision**: `hostfs.read` uses existing generic decision list/inspect,
claim/approve/deny, live event, profile filtering, WebUI plain-text rendering,
and TUI decision rows. Add a Reopen command/button only for eligible terminal
read decisions and show untrusted reason with a fixed label. The daemon only
broadcasts and authenticates existing Manager routes.

**Rationale**: 006, 007, 012, and 019 already established a shared decision
center and event reducer. A provider-specific parallel queue would duplicate
claim tokens, profile scope, redaction, and timeout behavior.

**Alternatives considered**:

- A HostFS read-only UI panel and API: rejected as duplicate authority.
- Daemon prompt/implicit approval: rejected by the existing daemon prompt
  non-claim.

## Decision 16: Promote Claims Only Through Split Local And Real Evidence

**Decision**: Register the eight stable proof IDs from the spec. Unit, typed
errno, decision lifecycle, redaction, and docs proof are local
targeted-completion requirements. Real namespace and live-grant proof are
real-gate targeted-completion requirements and must reference a real Gate 2 log
artifact with digest. A prerequisite-gated not-run proof is supporting only and
cannot satisfy either real requirement. `scripts/test-hostfs-visibility-e2e.sh`
aggregates local-fast or real-gate evidence; real mode invokes and validates the
029 markers emitted by `scripts/test-gate2-lima.sh`.

**Rationale**: The product-evidence registry already distinguishes requirement
layer, required-for target, freshness, and artifact policy. Prior work showed
that local green tests can otherwise be mistaken for real isolation proof.

**Alternatives considered**:

- Add only unit tests: rejected because FUSE errno, cache, and cross-process
  retry are backend behavior.
- Treat not-run as completion: rejected because it documents absence of proof.
- Create a second evidence schema: rejected because the existing registry and
  manifest can express the distinction.
