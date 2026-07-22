# Research: Shared Default VM Across Workspaces

## Decision 1: Separate Stable Selection From Compatibility

**Decision**: Select one automatic Lima slot from the canonical profile name.
Validate a separate canonical machine-compatibility digest after selection.
Project path and every session-only fact are excluded from both machine
identity and compatibility.

**Rationale**: Current `environment.AutoName` hashes profile plus workspace,
which creates one VM per directory. Embedding the complete posture digest in a
new name would instead create a second hidden VM after profile drift. A stable
slot plus explicit drift preserves one machine and one recovery path.

**Alternatives considered**:

- Keep workspace-derived automatic names: rejected because it is the reported
  VM-sprawl behavior.
- Name by the full profile hash: rejected because HostFS, adapter, host-app, and
  other session policy would cause irrelevant machine churn.
- Name by compatibility digest: rejected because real machine drift would
  silently select a new VM instead of requesting explicit recreate.

## Decision 2: Replace The Environment Shape Cleanly

**Decision**: One record schema has explicit `shared`, `dedicated`, and
`workspace-bound` modes. Shared records contain no workspace binding. Named
environments remain dedicated and pinned; unsupported reusable platforms use a
truthful workspace-bound record. Old alpha records receive one remove/recreate
error and no compatibility reader.

**Rationale**: There are no users requiring migration, and retaining ambiguous
`Workspace` fields on a shared record would permit last-project fallbacks. The
mode matrix preserves truthful behavior on native and platforms that have not
passed the shared transport gate.

**Alternatives considered**:

- Add nullable shared fields beside the old record: rejected because empty
  strings would acquire multiple meanings.
- Silently migrate old records: rejected because changing from one VM per
  workspace to a shared trust domain is a posture change.
- Label unsupported reusable environments dedicated: rejected because they are
  automatic rather than operator-selected separate-VM boundaries.

## Decision 3: Use One Store-Keyed Workspace Identity

**Decision**: Capture a canonical root and stable root file identity, retain an
open-root/provider identity where the selected transport permits it, and derive
one opaque ID with HMAC-SHA256 over a domain separator, canonical root, and root
identity. A private `0600` per-store key owns the derivation. Every subsystem
consumes that ID; none hashes a path independently.

**Rationale**: Current owner, daemon, and host-app paths derive incompatible
path hashes. A keyed digest avoids path-guessing, distinguishes root
replacement, and supports stable local correlation without becoming authority.

**Alternatives considered**:

- Unkeyed path hash: rejected because predictable paths can be guessed and root
  replacement is conflated.
- Random ID per run: rejected because status and audit cannot correlate the
  same project across sessions.
- Path only when file identity is unavailable: rejected because rename and
  replacement races would silently switch authority; shared mode instead gives
  dedicated guidance.

## Decision 4: Model Root Relations, Not A Universal Isolation Claim

**Decision**: Canonical roots are classified `same`, `nested`, or `disjoint`.
Same roots may share a concrete provider with independent session bindings.
Nested roots preserve asymmetric selected authority. Only disjoint roots carry
the symmetric sibling-unavailable claim.

**Rationale**: An ancestor project necessarily includes a selected descendant.
Rejecting common monorepo workflows or calling them mutually isolated would be
less truthful than reporting the actual relation.

**Alternatives considered**:

- Reject every overlap: rejected as unnecessary product friction.
- Deduplicate nested roots: rejected because it broadens the descendant.
- Claim all private views are isolated: rejected because one guest kernel and
  ancestor authority contradict that statement.

## Decision 5: Jointly Gate Transport And Guest Path Identity

**Decision**: Phase R accepts a candidate only when exact-root live filesystem
semantics and project-state identity both pass. Sessions expose logical
`/workspace`, but each receives an opaque physical root tied to `workspaceId`.
Representative shells, Git, language tools, and agent CLIs must retain distinct
project state after logical-path navigation.

**Rationale**: A fast mount can still merge project trust/history/cache if all
tools observe one shared-profile cwd identity. Conversely, a unique path over a
wrong or staged filesystem does not provide normal development semantics.

**Alternatives considered**:

- Fixed `/workspace` bind only: rejected because many tools key state by cwd or
  canonical path.
- Fake `/Users/fake/...`: rejected because it invents host topology and leaks a
  platform shape without restoring host identity.
- Preserve real host path in shared mode: rejected because it exposes username
  and cannot coexist as one logical path model.

## Decision 6: Prefer VZ Only If The Whole Control Path Exists

**Decision**: Research the VZ live multiple-share candidate first because it
can retain virtiofs performance. Acceptance requires an initially empty share,
retained running device, authenticated host-only exact-incarnation mutation,
root-identity-safe URL admission, atomic map/watcher updates, opaque keys,
session-private staging/mount, sibling handle stability, cleanup observation,
and a supportable packaged integration.

**Rationale**: Apple's current Virtualization SDK exposes runtime share
replacement and permits an empty `VZMultipleDirectoryShare`. However, Lima
2.1.4 and its pinned Code-Hex/vz v3.7.1 wrapper configure the share before boot
and expose no supported external running-device control. Lima's optional
`mountInotify` watcher also derives from startup mounts. API existence therefore
does not prove a production path.

**Alternatives considered**:

- Edit Lima YAML and restart: rejected because it interrupts siblings and is
  not hot attachment.
- Private replacement `limactl`: rejected because it creates an unmaintained
  backend fork and distribution surface.
- Mount the multi-share root globally: rejected because ordinary session
  targets could address sibling keys and the selected root would not be private.

**References**:

- [Apple shared directories](https://developer.apple.com/documentation/virtualization/shared-directories)
- [Lima mount documentation](https://lima-vm.io/docs/config/mount/)
- [Lima VZ driver v2.1.4](https://github.com/lima-vm/lima/blob/v2.1.4/pkg/driver/vz/vm_darwin.go)
- [Code-Hex/vz v3.7.1 shared directory wrapper](https://github.com/Code-Hex/vz/blob/v3.7.1/shared_directory.go)

## Decision 7: Keep A Distinct Portal Fallback Candidate

**Decision**: If VZ cannot satisfy the complete gate, evaluate one dedicated
Workspace Portal with a binary multiplexed handle protocol, open-root-relative
resolution, explicit file/directory handles, cancellation, backpressure,
independent same-root lock owners, bounded credentials, and truthful disconnect
semantics. It uses a separate `workspace.direct` authority and never HostFS
overlay actions.

**Rationale**: Existing HostFS go-fuse and broker plumbing prove local
filesystem mediation mechanics, but JSON/base64 path operations, staged writes,
and process-scoped locks cannot satisfy a hot direct workspace. The candidate
must be measured rather than inferred from library APIs; go-fuse documents
limitations around virtiofs unsolicited invalidation.

**Alternatives considered**:

- Reuse HostFS request/response operations: rejected for copying, path races,
  missing open-handle identity, lock collapse, and wrong write authority.
- Watcher-based two-way sync: rejected because conflict, durability, and
  copy-back semantics are a different product.
- Build a custom filesystem library: rejected; a proven kernel/FUSE library is
  required for the core protocol.

**References**:

- [go-fuse kernel protocol package](https://pkg.go.dev/github.com/hanwen/go-fuse/v2/fuse)
- [go-fuse virtiofs package](https://pkg.go.dev/github.com/hanwen/go-fuse/v2/virtiofs)

## Decision 8: Make Phase R A Binary Product Gate

**Decision**: Phase R produces a schema-validated decision artifact bound to
commit, dirty state, host, Lima, runtime digest, fixture/tool versions, raw
samples, and artifact digests. Exactly one pair may be `accepted`. Phase I
refuses an absent, stale, dirty-for-promotion, incomplete, or rejected artifact.
If both candidates fail after one bounded optimization pass, implementation
stops and current behavior remains unchanged.

**Rationale**: Current source inspection can establish feasibility questions
but not correctness, isolation, TCC behavior, cache convergence, or hot-path
performance. The 011-016 and 029 reviews demonstrated that green unit tests can
fix an unproved assumption into the expected result.

**Alternatives considered**:

- Choose VZ from SDK documentation: rejected because the product control path
  is missing.
- Choose Portal from existing go-fuse use: rejected because HostFS does not
  exercise the required data plane.
- Ship both behind flags: rejected because losing research code becomes a
  hidden fallback and doubles the authority surface.

## Decision 9: Extend 034 And 036 Rather Than Add Owners

**Decision**: The 034 daemon session worker is the sole attachment owner.
Manager plans and validates attachment authority. The selected provider and
guest view register as separate 036 resources before effects; an environment
service is added only if the measured topology actually survives sessions.
Activation occurs on the concrete authenticated supervisor-ready callback.
Cleanup is dependent-first and exact-incarnation stop remains solely 036's
decision.

**Rationale**: Independent CLI ownership cannot serialize attach versus stop or
survive process exit. A count-based stop would ignore providers, bridges, and
ambiguous cleanup. Existing 034/036 already solve ownership, reconciliation,
grace cancellation, and observed stop.

**Alternatives considered**:

- Let invoking CLI attach the root: rejected because it creates a second owner
  and breaks restart recovery.
- Stop on active workspace count zero: rejected because a VM-dependent resource
  may remain.
- Mark resources active before backend run starts: rejected because current
  call entry is not proof that a provider, mount, or supervisor is usable.

## Decision 10: Split Machine, Attachment, And Execution Contracts

**Decision**: Replace the mixed backend run input with three conceptual stages:
machine activation contains only machine facts; workspace attach contains exact
session/incarnation/root authority; execution contains target, terminal,
session providers, and guest supervisor. Shared machine configuration has no
selected workspace or dummy mount. Dedicated/workspace-bound modes may retain
their exact static mount behind the same explicit mode contract.

**Rationale**: Current `backend.RunSpec` requires `HostWork`/`GuestWork` and
`ConfigForRunSpec` writes them into Lima YAML, so changing selection alone
cannot make a machine cross-workspace. The split also gives lifecycle a real
point to register and prove attachment.

**Alternatives considered**:

- Leave fields empty in shared mode: rejected because empty strings become a
  hidden semantic branch and current Prepare rejects them.
- Use a dummy workspace mount: rejected because it creates unnecessary host
  authority and leaks static machine/workspace coupling.

## Decision 11: Separate Machine Identity From Mutable Services

**Decision**: Machine identity includes only backend/config/architecture, exact
runtime image content, target OS user/UID, guest machine-id, mount/VM
implementation, and isolation shape. Hostname is a boot reconciliation.
Network route/upstream/resolver is a serialized environment service. Policy,
env, Git, tools, shims, HostFS, adapters, host capabilities, audit, and script
source bytes are immutable session snapshots. Raw credentials remain outside
all public or persisted service identity.

**Rationale**: Recreate is justified by disk genesis or isolation, not by every
configuration field. A single serialized service owner can change proxy
upstream and resolver safely without replacing the disk. Because the current
route is VM-global, changing direct/proxy posture additionally requires no
active sibling target. Per-session snapshots let concurrent runs retain
distinct declarative policy.

**Alternatives considered**:

- Treat a network change as machine mismatch: rejected because it creates VM
  sprawl and interrupts sibling sessions despite a narrower online operation.
- Hash the entire profile: rejected because session-only policy would force
  machine recreation.
- Store raw proxy material in the record: rejected by the control-plane secret
  boundary.

## Decision 12: Preserve Platform Truth And Claim Boundaries

**Decision**: Promote only macOS arm64 Lima after a real installed-package gate.
Native and unpromoted Lima platforms create explicit workspace-bound records;
disposable `--rm` runs remain record-less; `--ephemeral` uses the normal
platform environment with session-local identity; named environments remain
dedicated. Docs claim only private exact-root views for ordinary non-root
targets with disjoint roots. Guest-root containment, project-content anonymity,
profile-state isolation, and separate VM walls remain non-claims.

**Rationale**: A backend or platform name is not evidence. The shared guest
kernel/root disk and profile mounts are intentionally common, and nested roots
cannot be mutually isolated by definition.

**Alternatives considered**:

- Enable by capability guess: rejected because it would claim untested
  isolation and filesystem semantics.
- Force all platforms into dedicated mode: rejected because current automatic
  workspace-bound reuse is truthful and useful.
- Describe named environments as complete project separation: rejected because
  same-profile home/config/cache/data/browser state remains shared.

## Decision 13: Disable Git Parallel Preload Only In Portal Sessions

**Decision**: Shared Workspace Portal sessions set Git's process-scoped
`core.preloadIndex=false` through Core-owned synthetic environment entries.
They do not modify repository, profile-home, or host Git configuration. Static,
dedicated, workspace-bound, native, and record-less sessions retain their
existing Git scheduling behavior. Exact `safe.directory` entries, when a mode
requires them, remain independent and may never become a wildcard.

**Rationale**: The fixed 10,000-entry fixture showed the same 10,172 metadata
operations with and without Git preload, while parallel submission through the
userspace FUSE path increased scheduling cost. A same-VM production-path probe
measured approximately 154 ms Portal median before this policy and 48 ms after
it, against an approximately 54 ms paired static-virtiofs control. This is a
scheduling correction for the selected transport, not a relaxed threshold or
a change to Git trust semantics.

**Alternatives considered**:

- Prefetch directory metadata in the Portal server: rejected after a bounded
  implementation probe produced no material Git improvement and added cache
  invalidation complexity.
- Change global or repository Git configuration: rejected because it mutates
  operator/project state and would outlive the selected session transport.
- Raise the 2x performance threshold: rejected because the measured regression
  had an identified product-side cause and the existing contract remained
  achievable.
