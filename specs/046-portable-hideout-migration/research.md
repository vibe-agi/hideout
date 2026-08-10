# Research: Portable Hideout Migration

## Research outcome

The design is implementable without changing Hideout's authority model. A bundle
is an immutable, destination-neutral snapshot, while each import is a separate
Manager operation that performs identity generation, path/secret rebinding,
authority review, backend adoption, and atomic activation. Full-state migration
is initially a version-gated Lima capability; all unsupported cases remain
available as explicit config-only migration or fail closed.

No product requirement remains marked `NEEDS CLARIFICATION`. Provider support is
still subject to a mandatory real-Lima compatibility spike and gate before its
capability can be advertised.

## Decision 1: Transform on import, never on export

**Decision**: Export captures a stable logical source snapshot and seals it. It
does not mint destination identities, bind destination paths, install destination
keys, or consume the source. Every import creates a new operation and applies its
own transformations to an operation-owned staged copy.

**Rationale**:

- One unchanged bundle can be imported into multiple computers.
- Retry does not mutate the only recovery artifact.
- The source remains usable and is never treated as transferred ownership.
- Destination-specific authority is reviewable on the destination that will use
  it.

**Rejected alternatives**:

- Reset identity during export: makes the artifact destination-specific and
  prevents faithful Exact Guest Restore.
- Mutate the sealed bundle after import: destroys reproducibility and makes retry
  and integrity evidence ambiguous.
- Treat export as a move: disconnected hosts cannot prove that the source has been
  retired.

## Decision 2: Separate portable data from authority execution

**Decision**: Add `internal/migration` as a side-effect-free format, crypto,
limits, and validation package. The Manager owns drafts, immutable plans, claims,
confirmation, operation state, audit evidence, recovery, and rollback. The daemon
runs long work. An optional typed backend migration provider owns backend-specific
capture and adoption.

**Rationale**: This matches the existing Manager operation ledger and
plan/review/apply pattern, keeps hostile bundle parsing testable without Lima, and
prevents UI or imported configuration from directly invoking backend authority.

**Rejected alternatives**:

- Add migration methods to the mandatory base backend interface: would claim disk
  portability for backends that cannot prove it.
- Run `limactl` directly from CLI/TUI/WebUI: bypasses Manager claims,
  recovery, and audit state.
- Reuse the current public evidence export: that format is intentionally redacted
  and cannot represent private persistent VM state.

## Decision 3: Use a framed, encrypted, append-only bundle

**Decision**: The initial format is one owner-only
`.hideout-migration` file with:

1. A small public header containing magic, format version, random bundle ID,
   cryptographic suite, salt, bounded KDF parameters, and a wrapped random bundle
   master key.
2. Independently framed and authenticated records for metadata, sparse extents,
   compressed payload chunks, selected secret values, and checkpoints.
3. An encrypted final manifest/index.
4. A final authenticated completion footer binding the ordered prefix digest and
   manifest record. Only a file with a valid footer is sealed and importable.

Each record binds bundle ID, record type, component ID, ordinal, logical offset,
declared plaintext length, and digest as AEAD associated data. Output is written
to an O_EXCL-created `0600` partial path and atomically renamed to the requested
final path after sealing. Existing files are never overwritten implicitly.

**Cryptography**:

- Generate a random 256-bit bundle master key.
- Derive a key-encryption key from an interactively supplied passphrase using
  Argon2id. Start with RFC 9106's memory-constrained profile: 64 MiB, three passes,
  and four lanes, while recording parameters in the header and enforcing strict
  upper bounds before allocation.
- Wrap the master key and encrypt records with XChaCha20-Poly1305. Use a fresh
  random 192-bit nonce for each record.
- Derive purpose-separated subkeys with HKDF-SHA-256.
- Compress each ordinary data chunk independently with zstd before encryption;
  zero and hole records carry no compressed payload.

Primary references are [RFC 9106](https://www.rfc-editor.org/rfc/rfc9106.html),
[RFC 8439](https://www.rfc-editor.org/rfc/rfc8439.html),
[RFC 5869](https://www.rfc-editor.org/rfc/rfc5869.html),
[RFC 8878](https://www.rfc-editor.org/rfc/rfc8878.html), and the Go
[`chacha20poly1305`](https://pkg.go.dev/golang.org/x/crypto/chacha20poly1305) and
[`argon2`](https://pkg.go.dev/golang.org/x/crypto/argon2) package documentation.
`github.com/klauspost/compress/zstd` will be added as an explicitly pinned module.

**Rationale**: Independent records bound peak memory, localize corruption,
support sparse disks and deterministic progress, and provide safe crash recovery
without plaintext intermediate images.

**Rejected alternatives**:

- A tar/zip around a whole encrypted disk: poor resumability and unsafe unbounded
  metadata/decompression behavior.
- Encrypting only the outer file after creating a plaintext archive: violates the
  no-plaintext-intermediate requirement.
- OS-keychain-only encryption: prevents deliberate cross-computer transport.
- A user-provided raw key in argv or environment: leaks through process and shell
  surfaces.

## Decision 4: Bound parsing and memory before expensive work

**Decision**: The v1 envelope is:

- 32 environments per bundle.
- 4 TiB aggregate logical persistent data.
- 1,048,576 payload records.
- 4 MiB plaintext chunks.
- 16 MiB maximum manifest plaintext.
- 1 MiB maximum non-payload record plaintext.
- 256 MiB maximum migration-process working set under normal supported inputs,
  including KDF and compression buffers.

The reader validates magic, version, integer overflow, KDF parameters, record
lengths, counts, component ordering, declared decompressed size, and aggregate
limits before allocating or deriving keys. Decompression must produce exactly the
authenticated declared size. Unknown required record types and trailing bytes
after the footer fail closed; explicitly optional extension records may be skipped
only according to the format version contract.

**Rationale**: A migration file is hostile input even when it appears to come from
another Hideout installation.

## Decision 5: Resume from authenticated checkpoints

**Decision**: Export and import write durable phase/checkpoint state to the
Manager operation ledger. The partial bundle also contains encrypted checkpoint
records. On resume the operator re-enters the passphrase; Hideout authenticates
the header and last durable checkpoint, scans/truncates an unauthenticated tail,
and continues from the first incomplete component. It never trusts a sidecar
offset by itself and never rewrites authenticated completed records.

Cancel stops at a record boundary and retains or removes the partial artifact only
according to an explicit operator choice. A sealed bundle is immutable. Import
resume is idempotent for the same operation ID; starting another import from the
same bundle intentionally creates another destination and fresh identities.

**Rejected alternative**: Treat any existing partial file as appendable based
only on file length. A torn or substituted tail could then be accepted as prior
work.

## Decision 6: Capture a quiescent Lima snapshot, not live files

**Decision**: The Lima provider proves every selected environment is stopped and
claims its instance and attached disk graph. It uses Lima's stopped-instance clone
operation to create an operation-owned copy-on-write preparation instance. Once
the clone and any attached-disk snapshots are complete, the source may run again;
export streams only from the frozen operation-owned copies.

Lima documents that `limactl clone` requires a stopped source. Its implementation
omits transient runtime files, clears the VZ identifier, and uses copy-on-write
copying when available. See the official
[clone reference](https://lima-vm.io/docs/reference/limactl_clone/),
[internals documentation](https://lima-vm.io/docs/dev/internals/), and Lima 2.2.0
[`clone.go`](https://github.com/lima-vm/lima/blob/v2.2.0/pkg/instance/clone.go).

The bundle includes normalized Hideout/Lima configuration and every selected
persistent root or attached disk payload. It excludes host mount contents,
`ssh.config`, sockets, PIDs, logs, runtime caches, VZ host identifiers, and other
recreatable host runtime files.

For attached disks, the provider constructs an ownership/reference graph. A disk
shared only by selected, stopped environments is captured once and referenced by
each consumer. A disk used by an unselected or running instance, a disk outside
recognized provider-owned location, or an unprovable graph blocks full-state
export rather than silently omitting data. Lima's disk concepts are documented in
the official [disk reference](https://lima-vm.io/docs/config/disk/).

**Required compatibility spike**: Lima has no supported cross-host root-instance
export/import command. The provider therefore remains unavailable until a real
gate proves the version-specific root and attached-disk snapshot paths for every
advertised Lima minor version. Unknown layouts fail closed; config-only export
continues to work.

## Decision 7: Stage Lima adoption under a fresh backend identity

**Decision**: Import never installs source runtime files as a runnable Lima
instance. The provider:

1. Creates a fresh opaque destination instance identity and operation-owned
   staging directory.
2. Materializes and verifies disk extents there.
3. Generates destination configuration from normalized source facts plus reviewed
   mappings, omitting host identifiers and runtime files.
4. Performs one isolated adoption boot with host mounts, external endpoints,
   provisioning scripts, and ordinary workload startup disabled.
5. Supplies a strict data-only adoption request through a private read-only mount
   and receives a nonce-bound receipt.
6. Proves the guest shut down, verifies the receipt and resulting identity policy,
   removes the adoption channel, and reports the staged instance as adoptable.

The destination Hideout package contains a checksummed Linux
`hideout-migration-adopt` helper. The provider exposes that packaged binary and
request to the isolated boot through ephemeral read-only mounts and invokes one
fixed product-owned entry point; neither artifact is added to the sealed bundle.
The helper accepts a versioned data schema, not a shell program, and removes the
guest-side one-shot trigger after success. This keeps the captured source disks
byte-for-byte unchanged by migration until the destination adoption boot.

The compatibility gate must prove that the restricted preparation/adoption boots
cannot use exported host mounts, public endpoint grants, proxy credentials, or
user provisioning. Failure to prove isolation disables the full-state capability.

**Implementation finding (2026-08-02)**: Stock Lima 2.1.x/2.2.x with the VZ
driver cannot provide this proof. Its driver attaches the default user-mode
network when no named user network is configured; `networks: []`, plain mode,
disabled DNS, and disabled port forwarding do not mean “no network device.” The
production provider therefore advertises full-state export but keeps full-state
import disabled with `migration.provider.adoption_isolation_unproved` until the
Hideout package contains a dedicated no-network adoption executor. The executor's
stable proof identity is part of the provider capability revision. A test-only
prober may exercise staging contracts, but it is package-private and cannot enable
production import through configuration.

The destination verifier is implemented independently of that executor. It
requires the complete adoption request/receipt pair plus an operation-owned
post-shutdown evidence record, rechecks the pre-adoption stage digest graph,
requires attached disks to retain their checkpointed file identity, binds each
root's current file identity to the post-adoption record, regenerates the
authority-free configuration, rejects any unknown or temporary staging-tree
artifact, and proves that no destination object has become a top-level Lima
instance or disk. This does not weaken the blocker above: production full import
remains disabled until T034 supplies the no-network executor that is exclusively
authorized to create those evidence records.

**Rejected alternatives**:

- Copy the source Lima directory wholesale: leaks host/runtime identity and relies
  on unstable internal paths.
- Start the imported machine normally and repair it afterward: exposes duplicate
  identity and imported authority before validation.
- Treat an empty Lima network list as isolation: the VZ driver still attaches its
  default user-mode network, so this would be an unproved security claim.
- Offline-mount Linux filesystems directly on macOS: adds a privileged filesystem
  stack and still does not cover every supported disk format.

## Decision 8: Make identity policy per import

**Decision**:

- Hideout environment, session, backend instance, boot configuration, operation,
  and control-plane identities are always generated fresh during each import.
- `SafeClone` is the default guest policy. The isolated adoption helper regenerates
  `/etc/machine-id` and SSH host keys, and the Manager proves the resulting guest
  identity differs from the source and every sibling import from the bundle.
- `ExactGuestRestore` is an advanced per-environment policy. It preserves guest
  machine identity and SSH host keys, requires explicit typed acknowledgement,
  and permanently records that Hideout cannot prove source retirement or prevent
  collisions if copies run together.
- Unknown opaque application/device identities inside the filesystem are
  preserved. Preview warns that Hideout cannot safely enumerate or rewrite them.

**Rationale**: Identity behavior differs by destination and use case, so export
is the wrong time to choose it. There is deliberately no switch that preserves
Hideout control-plane identity.

## Decision 9: Treat imported host authority as disabled proposals

**Decision**: Pure, inert profile values may be carried forward after schema
validation. Every host- or network-authority-bearing value becomes a disabled
proposal until explicitly mapped and approved on the destination, including:

- workspace and HostFS paths;
- passthrough mounts and reserved-root exceptions;
- proxy/network/endpoint bindings;
- host application packs and command adapters;
- policy/provisioning script references;
- secret references and endpoint URLs.

Path mappings are canonicalized and checked with the existing reserved-root and
alias protections. Imported code is displayed as data and is not executed by
inspect, preview, validation, or ordinary import staging. The immutable import
plan records the source fact, destination proposal, risk class, validation result,
and explicit approval.

**Rejected alternative**: Reactivate values whose path happens to exist. Existence
does not prove that a destination user granted the same authority.

## Decision 10: Transfer secrets only by explicit selection

**Decision**: Default bundles contain logical secret references and redacted
metadata only. The operator may explicitly select individual transferable secret
values for encrypted inclusion. Provider-opaque/non-exportable records are never
copied and are reported before confirmation.

At import, selected values are decrypted only in memory and written directly to
fresh operation-scoped Keychain entries through the existing secret broker. New
profile records reference those fresh keys. Until the Manager activation commit,
the entries are unreachable from an active environment; rollback deletes them.
Crash recovery reconciles entries by operation ID. Values never appear in bundle
inspection, snapshots, progress, audit events, argv, environment, or plaintext
files.

**Rejected alternatives**:

- Copy every secret automatically: violates least authority and provider export
  restrictions.
- Reuse destination secret names and overwrite values: makes rollback destructive.

## Decision 11: Commit visibility, not backend directory renames

**Decision**: Import claims all requested environment names, backend objects,
profile names, workspace mappings, and secret destinations before materialization.
It stages them under fresh opaque identities. All validation and adoption completes
before one Manager activation commit makes the new environment records visible.

Backend assets may retain their opaque generated names; user-facing names live in
Manager records. This avoids an unavoidably partial multi-directory rename. Before
commit, normal Hideout operations reject the claimed names and cannot run staged
instances. After commit, every referenced asset already exists. Recovery either
finishes a recorded commit or removes all operation-owned artifacts; it never
guesses from partial filesystem presence.

**Rationale**: Atomic namespace visibility is achievable even when a backend has
no multi-object transaction.

## Decision 12: Model export and adoption separately in TLA+

**Decision**: Add two small models rather than one state-space-heavy model.

`MigrationBundle` models stopped proof, claims, snapshot, chunk/checkpoint writes,
cancel, crash, resume, manifest seal, and cleanup. Its key invariants are:

- source state is never mutated;
- only a complete authenticated prefix can resume;
- a sealed bundle is immutable;
- an unsealed/tampered bundle is never importable;
- each durable effect occurs at most once per operation binding.

`MigrationAdoption` models one immutable bundle imported to at least two
destinations under Safe Clone and Exact Guest Restore. It includes name claims,
authority review, staging, secret preparation, adoption boot, receipt validation,
commit, rollback, crash, recovery, and ordinary stop/boot/rebind/first-target
ordering. Its key invariants are:

- no staged/failed object is runnable;
- control-plane and backend identities are fresh and pairwise distinct;
- Safe Clone guest identities differ from the source and sibling imports;
- Exact Guest Restore preserves guest identity and makes no uniqueness claim;
- unapproved imported authority is never effective;
- an import policy cannot change after plan confirmation;
- a name has at most one live claimant;
- durable adoption disk fidelity does not imply per-boot mount readiness;
- no target starts until the current boot has proved and rebound every imported
  attached-disk path, and stopping clears that proof;
- a valid operation reaches complete or rolled-back under eventual provider and
  daemon availability.

Each model gets focused safety and liveness configurations plus Go refinement
tests against production transition functions. `formal/inventory.json` and
`docs/formal-models.md` remain the gate-owned inventory.

## Decision 13: Present one workflow in CLI, TUI, and WebUI

**Decision**: Add `hideout migrate export|inspect|import|status|resume|cancel|recover`
using the repository's command catalog and custom flag handling; Hideout does not
use Cobra. TUI and WebUI render the same Manager draft/plan/snapshot and submit
same typed actions. The UI starts with plain-language choices:

- configuration only or full VM state;
- what will and will not be copied;
- Safe Clone or advanced Exact Guest Restore;
- unresolved destination paths/secrets/authority;
- concrete bytes, components, current phase, elapsed time, and next action.

Editable rows open a terminal modal or WebUI dialog. Automation may use noninteractive
flags only when every required choice and acknowledgement is explicit; `--yes`
cannot accept unresolved or high-risk authority by itself.

## Decision 14: Layer evidence and release gates

**Decision**:

- Gate 0: format round trips, schema checks, deterministic framing, hostile input,
  fuzzing, KDF/record bounds, crypto misuse tests, redaction, static analysis,
  formal safety/liveness, refinement, and generated-artifact consistency.
- Native: config-only operation, claim, cancel, resume, rollback, and UI/API
  contract tests. It is not evidence for disk fidelity.
- Real macOS arm64 Lima: package-candidate helper, stopped proof, root and attached
  disk fixtures, sparse extents, multi-environment sharing, two imports from one
  unchanged bundle, Safe Clone identity uniqueness, Exact Guest Restore warning
  and preservation, authority isolation, crash cuts at every durable phase, and
  cleanup.
- Physical acceptance: export on one supported Mac and import the same unchanged
  bundle on two other supported Macs, byte-check persistent fixtures, and prove
  pairwise control/backend/Safe Clone guest identity separation.

Performance qualification is not run while the workstation is unstable. When it
resumes, migration-process CPU, I/O, memory, and throughput are measured alongside
host-noise evidence; unrelated system-wide slowdown alone is not the product
metric.
