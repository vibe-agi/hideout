# Research: Zero-Friction Setup

## Decision 1: Setup Is A Fixed Init Projection

**Decision**: `hideout setup` maps to the existing default profile, `dev`
template, Lima backend, direct network, exact `developer-standard` runtime,
alias workspace, and no additional HostFS visibility. It accepts no flags.

**Rationale**: The existing default profile already uses alias workspace,
synthetic identity, direct networking, and audit-oriented host capabilities
(`internal/profile/profile.go:269-309`). A fixed projection removes concepts
from first use without creating a second policy vocabulary.

**Alternatives rejected**:

- An interactive wizard merely rearranges the existing complexity.
- A privacy-first default adds proxy and resolver prerequisites before first
  success; that existing lane remains supported separately.
- Flags on setup would duplicate advanced `init` and create two public
  automation contracts.

## Decision 2: All Normal Init Writes Converge On Daemon-Hosted Manager

**Decision**: Both `setup` and normal `init` plan/apply through authenticated
daemon HTTP. There is no embedded writer fallback.

**Rationale**: The current CLI calls `manager.New` and mutates in-process
(`internal/app/app.go:1630-1683`), while API apply replans from options
(`internal/manager/api.go:527-570`). Keeping both would preserve two behaviors
to secure. The daemon already provides race-safe startup, authenticated
readiness, stale-socket handling, and build-mismatch refusal in
`internal/daemon/autostart.go`.

**Alternatives rejected**:

- Routing only setup through the daemon leaves ordinary init as a divergent
  authority path.
- Falling back to embedded Manager after daemon failure violates fail-closed
  control-plane ownership.
- Moving confirmation into the daemon violates the local prompt boundary; the
  initiating CLI remains the confirmer.

## Decision 3: Reuse The RunService Prepare/Apply Pattern

**Decision**: Add a Manager-owned `InitService` with versioned request, review,
prepared plan, canonical digest, and apply-time re-observation. Replace the
existing init plan/apply wire shapes cleanly; no compatibility shim is needed.

**Rationale**: `RunService.Prepare` computes a public review and plan digest,
and `Apply` re-prepares before comparing the reviewed digest
(`internal/manager/run_service.go:116-202`). Init currently lacks that binding.
The project has no external init API users, so maintaining two contracts would
only preserve unsafe behavior.

**Alternatives rejected**:

- Sending only options to apply repeats the current time-of-check/time-of-use
  gap.
- Trusting a client-supplied digest without the prepared semantic plan lets the
  client manufacture authority.
- Storing pending plans server-side adds lifecycle and cleanup state that is
  unnecessary for a local request/confirm/apply round trip.

## Decision 4: Classify Existing State With Pure Reads

**Decision**: Manager classifies setup state as `fresh`, `ready`, `repairable`,
or `blocked` before rendering a setup plan. `ready` is terminal and causes no
apply request. The classifier uses strict `Store.Load` and explicit file
observations only.

**Rationale**: `Store.LoadOrInit` writes missing metadata, materializes identity
state, and creates absent profiles (`internal/profile/profile.go:443-471`). It
therefore cannot support the requirement that repeated setup preserve profile
bytes, metadata, identity files, mtimes, and evidence exactly.

**Alternatives rejected**:

- Normalizing valid old/custom profiles during setup steals operator authority.
- Treating every existing profile as ready hides malformed or incomplete state.
- Automatically repairing partial state turns setup into an implicit repair
  command.

## Decision 5: Revalidate Inside The Existing Profile Mutation Lock

**Decision**: Init apply has exactly one lock-owning Manager method. It acquires
`Core.withProfileMutationLock`, observes current state under the lock, rebuilds
the effective semantic plan, validates the reviewed digest, and invokes a
private lock-assuming apply helper without releasing the lock. Other internal
init callers use the same lock-owning entrypoint rather than nesting the lock.

**Rationale**: The existing lock combines in-process serialization and a
store-rooted, symlink-safe cross-process `flock`
(`internal/manager/profile_lock.go:13-82`). Checking before acquiring it still
allows another process to create or change the profile between validation and
write.

**Alternatives rejected**:

- An in-memory mutex cannot serialize separate CLI or daemon processes.
- Last-writer-wins `LoadOrInit` would let a reviewed create plan continue by
  modifying a profile another process just created.
- A new lock namespace would duplicate an already reviewed primitive.
- Acquiring the same non-reentrant lock in both `InitService.Apply` and
  `Core.ApplyInit` would deadlock instead of strengthening serialization.

## Decision 6: Digest Semantic Effects, Not Incidental Generation Time

**Decision**: The canonical digest covers the request version, plan version,
state classification, profile/backend/network/template/runtime selection,
task identity and effect fields, and other review-relevant inputs. It excludes
incidental timestamps and random values, or carries the exact reviewed value
when that value has authority significance.

**Rationale**: Template rendering creates timestamps, and init apply currently
renders again (`internal/inittask/inittask.go:372-379,911-919`). Recreating such
values during validation would make every apply stale by construction. The
same failure mode is explicitly handled for ephemeral identity in
`RunService.Apply` (`internal/manager/run_service.go:181-192`).

**Alternatives rejected**:

- Hashing raw JSON embeds unstable or secret-bearing details.
- Omitting all task inputs would fail to detect meaningful drift.
- Comparing only the profile name does not bind the operator's review.

## Decision 7: Daemon Startup Is The Only Allowed Cancel Side Effect

**Decision**: Setup may create bounded daemon runtime files and a live local
daemon before confirmation. Negative input, EOF, Ctrl-C, or non-terminal input
must leave no profile, onboarding evidence, VM, runtime artifact, or new
authority.

**Rationale**: Planning must occur under the same Manager authority used for
apply, which may require race-safe daemon startup. Daemon socket/token/lock
state is control-plane runtime state, not setup success or target authority.

**Alternatives rejected**:

- Delaying daemon startup until after confirmation would render a client-owned
  plan rather than the authoritative Manager plan.
- Requiring the operator to start the daemon manually defeats first-run setup.
- Describing daemon files as zero filesystem change would force dishonest tests.

## Decision 8: Setup Does Not Own Runtime Download Or VM Start

**Decision**: Setup records exact runtime provenance only. The first run owns
Lima startup and possible download. Its existing wait UI is extended with
runtime revision and declared size while preserving honest periodic heartbeat.

**Rationale**: Lima already prints a delayed download notice, periodic elapsed
heartbeat, and ready message (`internal/backend/lima/lima.go:756-778`). The
download occurs inside `limactl`; Hideout has no truthful byte-progress source.

**Alternatives rejected**:

- Prewarming in setup makes a short configuration command unexpectedly large
  and slow.
- Fabricating percentages from elapsed time is false evidence.
- Building a second image cache conflicts with the current Lima-owned cache.

## Decision 9: Extend The Existing First-Run Harness With Two Lanes

**Decision**: Keep the privacy/Lima lane and add a direct/setup lane to
`scripts/test-first-run-e2e.sh`. Reuse the exact agent install fixture from
`scripts/test-runtime-agent-install.sh` for a separate-session install/run
proof.

**Rationale**: Privacy and direct setup make different network claims; neither
can prove the other. A second parallel harness would duplicate package install,
evidence provenance, cleanup, and failure semantics.

**Alternatives rejected**:

- Replacing privacy with direct would discard an existing product claim.
- A local-native success cannot prove Lima isolation or runtime behavior.
- Source-code grep or a same-shell `--version` check cannot prove PATH
  persistence across sessions.

## Decision 10: Homebrew Installation Remains Side-Effect-Light

**Decision**: Formula installation continues to invoke package install with
`--skip-init`. Caveats point to `hideout setup`. The source formula and
published tap formula receive a drift check covering caveats and helper
inventory.

**Rationale**: Setup requires local review and default-no confirmation; package
installation cannot safely infer approval. The current source and tap formulas
already drift in Linux helper inventory, demonstrating the need for parity.

**Alternatives rejected**:

- Running setup from Homebrew is non-interactive and violates confirmation.
- Keeping the long init command as primary onboarding preserves the product
  problem this feature solves.
- Treating the external tap as hand-maintained leaves published instructions
  unverifiable.

## Decision 11: Reuse Existing Schemas And Evidence Vocabulary

**Decision**: Add API fields to the existing Manager API schema and register
038 requirements in the existing product-evidence registry. Do not introduce a
new profile, onboarding, audit, or evidence schema version.

**Rationale**: The feature composes existing state and authority. New versions
would create migration code without changing persisted meaning.

**Alternatives rejected**:

- A setup-specific profile format would fork policy semantics.
- A separate evidence manifest would bypass readiness and docs-truth joins.
- Version bumps for additive wire-only fields would preserve compatibility no
  real user requires and increase maintenance.

## Decision 12: Product Output Is Concise; Advanced Detail Stays Available

**Decision**: Setup output shows isolation, runtime, future workspace,
network, other-file visibility, audit, no-download behavior, and exact next
commands. It omits raw store paths, host paths, task inventories, internal IDs,
image URLs, daemon details, and tokens. Existing init dry-run, doctor JSON,
audit, and verbose surfaces remain the detailed views.

**Rationale**: First-run comprehension depends on information hierarchy, not
removing evidence. Control-plane internals are neither actionable nor safe in
the default success path.

**Alternatives rejected**:

- Printing every InitTask exposes implementation vocabulary before value.
- Omitting direct-network disclosure would make setup easier by hiding a
  material privacy fact.
- Calling audit merely `on` implies an unsupported off mode; output says
  `always on`.
