<!-- markdownlint-disable MD013 -->

# Research: Unified Named Environments With Declared Base Image

## Decision: Name is the user-facing handle; the internal ID stays

**Rationale**: `environment.Record` already has a random `ID` that keys the
record path and the Lima instance name (`InstanceNameForEnvironment`). Reusing
it avoids touching path and instance derivation. The new `Name` becomes a
unique, conservatively validated user-facing handle (uniqueness checked
case-insensitively so `Default` cannot dodge the reservation), stored on the
record and indexed for lookup. Auto-named and explicit environments differ
only in who chose the string.

**Alternatives considered**:

- Name as the storage/instance key: rejected — breaks record paths and
  instance naming for no benefit, and makes any future rename impossible.
- Separate tables for auto vs explicit environments: rejected — reintroduces
  the dual model this feature removes.

## Decision: Auto-name = sanitized workspace basename + short stable hash

**Rationale**: The auto-name must be deterministic for (profile, cleaned
absolute workspace path), readable in `env list`, and must not silently alias
when a workspace moves. A sanitized basename plus an 8-hex hash of
(profile, cleaned path) satisfies all three: the same checkout always maps to
the same name; a moved workspace produces a different name (and the old
record becomes visible garbage to clean, never a silent alias); collisions
with explicit names are rejected at create because names share one namespace.
Workspace *identity* comparison at use time still goes through
`os.SameFile`-level checks; the hash is only a naming input.

**Alternatives considered**:

- Hash of file identity (device/inode): rejected — not stable across moves,
  backups, or filesystem changes, and unavailable for a deleted path.
- Full path slug: rejected — unbounded length and leaks deep path structure
  into UI columns.

## Decision: Selection rewrite replaces `Store.Latest` with name resolution

**Rationale**: Today `SelectRunEnvironment` picks `store.Latest(spec)` — the
most-recently-used record matching the fingerprint — and silently creates a
new environment when the fingerprint changed. The unified model replaces this
with: explicit `--env <name>` loads by name; no flag derives the auto-name
and loads or creates it. The existing `ValidateEnvironmentRecord` mismatch
check grows into the use-time drift report (backend config version, workspace
by real file identity) and applies to every selection, not only `--resume`.
The pinned image declaration is immutable record data: profile default changes
do not drift existing environments, and URL digest mismatch is a boot-time
verification failure. `--resume <id>` and `--new` disappear with the MRU
model; `--rm` keeps its record-less disposable semantics. `--ephemeral` is a
session-identity modifier and resolves the same reusable environment as a
normal run.

**Alternatives considered**:

- Keep `Latest` as a fallback for unnamed runs: rejected — that is the dual
  model again, and silent fingerprint-keyed derivation is exactly the sprawl
  behavior being removed.
- Treat `--env` as accepting IDs too: rejected — one handle type keeps the
  CLI contract and error messages simple; IDs remain visible in `inspect`.

## Decision: Image declaration is a single string with two accepted forms

**Rationale**: One field, one validation path. Form 1:
`template:<built-in-name>` (the shipped default profile carries
`template:_images/ubuntu-lts`, replacing the hardcode at `lima.go:398`).
Form 2: `https://<host>/<path>.(img|qcow2)#sha256:<64hex>` — the fragment
carries the operator-supplied digest from the distributor's published
checksums. Validation is local: scheme allowlist (https), digest presence and
shape, no embedded credentials (userinfo rejected). Lima consumes form 1 as a
`base` template reference and form 2 as a generated `images` entry with
`location` and `digest`, so download and digest verification ride Lima's
existing mechanism; Hideout never fetches images itself.

**Alternatives considered**:

- Structured object (`{ref, digest}`): rejected for MVP — two fields to
  validate and to teach; the string form matches how operators copy
  image URLs and checksums.
- Hideout-side download/verification: rejected — duplicates Lima's mechanism
  and adds a network component to Core.
- OCI references with tag resolution: rejected per clarification — Lima does
  not consume OCI images and resolution would require a registry client.

## Decision: Pinned image is immutable; drift comparison = backend config version + workspace

**Rationale**: Per clarification, expected-command declarations are live
diagnostics, not identity — `ToolsHash` leaves the drift comparison (the
field can be dropped from the record). The image declaration is copied into
the environment record and is immutable in this slice. A later profile
`environment.baseImage` change does not drift existing environments; changing
an environment image requires remove/create or a future explicit update
feature. URL digest mismatch is a boot-time verification failure. For the
template form the ref string is the pinned declaration and changes to the
backend's concrete template mapping are represented by `backendConfigVersion`.
The PRD identity sentence is updated to match in the docs task.

**Alternatives considered**:

- Keeping `ToolsHash` in identity: rejected per clarification Q1 — a
  diagnostic-only declaration must not force guest destruction.
- Treating image edits as drift: rejected — there is no current image input
  for an existing environment once the declaration is pinned. Recreate rebuilds
  from the pinned declaration; image changes need remove/create or a future
  explicit update surface.

## Decision: Record version bumps; prior records are rejected with guidance

**Rationale**: `Record.Version` already exists and `Store.Save` stamps it.
The version constant bumps; `Load`/`List` treat any other version as
"environment model changed": operations that touch such a record stop with
clean-and-recreate guidance (`hideout env remove <name>` cannot apply to an
unversioned record, so guidance points at `clean`/store cleanup). No
migration code is written, per the clean-change principle.

**Alternatives considered**:

- Silent migration (fill Name from a derived value): rejected — old records
  lack pinned image declarations, so a migrated record would fabricate
  identity it never had.
- Ignoring old records in listings: rejected — invisible state the operator
  cannot discover or clean is worse than a visible guided rejection.

## Decision: Lifecycle commands resolve names; destructive ops fail closed on running guests

**Rationale**: `stop`/`clean` accept names by resolving name → record →
existing lifecycle paths. `env recreate`/`env remove` check instance state
first and refuse with a copyable stop command unless `--force` (which stops,
then proceeds), per clarification Q3. Recreate = teardown of the guest and
instance plus a fresh boot from the record's pinned declaration under the
same name and record identity refresh; it reuses stop/clean internals rather
than new teardown code.

**Alternatives considered**:

- Auto-stop by default: rejected per clarification — destructive actions on a
  possibly-running agent session must be explicit.

## Decision: Shadow warning lives in run planning and doctor

**Rationale**: The warning ("this HostFS rule is shadowed by the workspace")
needs the resolved workspace and the profile rule set — both available in run
planning and doctor. Containment uses the same `os.SameFile`-grade
normalization as the workspace guard. It never blocks: the workspace is the
intentional collaboration surface, and the warning exists to kill the
illusion that a deny rule protects an in-workspace path.

**Alternatives considered**:

- Failing closed on shadowed rules: rejected — shadowing is a UX honesty
  problem, not an authority violation.
- Warning at rule-creation time only: rejected — the workspace differs per
  environment, so creation-time checks cannot know all future workspaces;
  doctor/run-time evaluation is authoritative (creation-time warning can be
  added as a courtesy where the workspace is known).

## Decision: Native backend gets records, no VM lifecycle

**Rationale**: Unified model means `env list` shows native environments too,
with the same declared-image identity fields recorded for consistency; no
instance is booted and stop/recreate degrade to record operations. Today
`SelectRunEnvironment` returns an empty environment for non-Lima backends —
the rewrite gives native the same record path with a no-op instance layer,
which keeps the model single and the weak-harness status explicit.

**Alternatives considered**:

- Keeping native environment-less: rejected — a second model with "sometimes
  there is no environment" leaks into every listing, drift, and evidence
  surface.
