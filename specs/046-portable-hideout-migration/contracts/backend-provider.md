# Contract: Backend Migration Provider

## Purpose

Full-state migration is an optional backend capability. The existing basic
backend interface remains unchanged. A backend that implements this contract may
advertise full persistent-state capture/adoption only after runtime compatibility
checks pass.

The provider owns backend lifecycle and storage mechanics. It does not parse
bundle ciphertext, handle passphrases/secrets, approve imported authority, choose
identity policy, publish Manager records, or render UI.

## Proposed Go boundary

Names may be adjusted to repository conventions, but the authority split and
semantics are normative.

<!-- markdownlint-disable MD013 -->

```go
// internal/backend/migration.go
type MigrationProvider interface {
    MigrationCapabilities(context.Context) (MigrationCapabilities, error)
    InspectMigrationSource(context.Context, SourceInspectionRequest) (SourceInventory, error)
    SnapshotMigrationSource(context.Context, SourceSnapshotRequest) (SourceSnapshot, error)
    ReadMigrationComponent(context.Context, ComponentReadRequest, func(MigrationExtent) error) error
    ReleaseMigrationSnapshot(context.Context, SnapshotReleaseRequest) error

    InspectMigrationDestination(context.Context, DestinationInspectionRequest) (DestinationInventory, error)
    StageMigrationDestination(context.Context, DestinationStageRequest) (DestinationStage, error)
    AdoptMigrationDestination(context.Context, DestinationAdoptionRequest) (DestinationAdoption, error)
    VerifyMigrationDestination(context.Context, DestinationVerifyRequest) (DestinationProof, error)
    RollbackMigrationDestination(context.Context, DestinationRollbackRequest) error
}
```

<!-- markdownlint-enable MD013 -->

All requests carry `OperationID`, `EffectID`, expected provider capability
revision, and exact object bindings. Results contain typed facts and opaque handles,
not filesystem paths or executable command strings.

## Capabilities

`MigrationCapabilities` contains:

- provider/backend name and semantic version;
- capability revision/digest;
- supported bundle disk representations;
- supported host/guest architecture pairs;
- full export/import availability Booleans;
- supported root and attached disk kinds;
- sparse extent support;
- maximum logical sizes/counts no larger than the bundle limits;
- adoption-helper package ID/digest/guest architecture;
- stable unavailability reason and remediation.

Availability is computed from live facts. For Lima it includes the executable
version, recognized instance/disk layout, virtualization driver, source lifecycle
inspection, helper presence/digest, staging filesystem features, and free-space
probe. A cached capability is invalid when any bound fact changes.

The Manager includes the capability revision in a plan and rechecks it at apply.

## Source inspection

`InspectMigrationSource` is read-only and returns:

- exact provider instance ID and lifecycle observation;
- source configuration revision/digest;
- root disk and every attached persistent disk;
- logical/allocated sizes and known format;
- environment-to-disk attachment graph;
- all known consumers of shared disks and their lifecycle states;
- provider runtime files that will be excluded;
- whether the requested selection is closed and capturable;
- stable blockers/remediation.

The provider must not infer `stopped` from a missing PID alone. It uses the same
exact backend lifecycle proof used by destructive environment operations. Unknown,
transitioning, or contradictory state is not stopped.

Source inspection never starts, stops, edits, snapshots, locks, or mounts an
instance.

## Snapshot operation

`SnapshotMigrationSource` may execute only after the Manager has confirmed a plan
and acquired deterministic claims for all source instances and disk objects.

Normative behavior:

1. Reinspect capability, source revisions, selection closure, and exact stopped
   state.
2. Create an operation-owned provider snapshot under opaque generated names.
3. Capture the root disk and each attached disk once, preserving logical bytes and
   sparse semantics.
4. Normalize configuration separately; exclude host runtime files and identifiers.
5. Sync snapshot metadata and return immutable opaque component handles/digests.
6. Prove no returned snapshot depends on later source writes before allowing the
   Manager to release source claims.

The provider must never write the source instance configuration or persistent disk.
It must not boot the source or snapshot clone. If its copy-on-write primitive does
not provide a stable independent logical snapshot, it keeps the source claims and
stopped requirement until all bytes are read.

Reissuing the same `OperationID`/`EffectID` returns the same completed snapshot
or continues cleanup; it does not create an untracked duplicate. A different
effect binding creates a distinct snapshot.

## Component reading

`ReadMigrationComponent` emits a canonical sequence of typed extents starting at
an authenticated resume cursor:

```go
type MigrationExtent struct {
    Kind          ExtentKind // Data, Zero, or Hole
    LogicalOffset uint64
    Length        uint64     // Data is chunk-bounded; sparse ranges are logical
    Data          []byte     // Data only; bounded to requested chunk size
}
```

Rules:

- Extents cover the component's logical size exactly without overlap.
- A Data callback buffer is invalid after callback return and remains bounded.
- Adjacent `Zero` or `Hole` observations are coalesced before they cross this
  boundary. Payload-free sparse extents may therefore exceed the Data chunk
  size while remaining within the component logical-size bound.
- Provider errors identify opaque component/effect IDs and a stable code, not a
  raw host path.
- The writer independently hashes extents; provider digests are evidence, not a
  substitute for bundle integrity.
- Resume cursor must align to a provider-advertised extent/chunk boundary.
- The provider cannot observe passphrases or encrypted output records.

## Snapshot release

Release is idempotent and restricted to handles owned by the exact operation. It
refuses source instance/disk handles and objects with an unknown ownership marker.
Cleanup reports retained bytes and remediation when deletion cannot be proved.

No broad path or unresolved variable may become a cleanup target.

## Destination inspection

`InspectMigrationDestination` is read-only and evaluates:

- format/backend/architecture compatibility;
- destination name and backend object conflicts;
- disk graph representability;
- required logical and worst-case allocated space;
- filesystem sparse/COW capabilities;
- adoption-helper availability/digest;
- isolated adoption-boot capability;
- current provider capability revision;
- unsupported source configuration facts that must be disabled or remapped.

It receives normalized, already-authenticated manifest facts, not a bundle path
or key.

## Destination staging

`StageMigrationDestination` receives fresh Manager-generated environment and
disk backend identities, typed normalized configuration, a declared disk graph,
an explicit authenticated component-to-disk binding, an opaque operation
staging handle, and component readers. Root-disk identity is the owning
environment backend identity; every attached disk has its own Manager-generated
identity. The provider MUST NOT replace either with an inferred local name. It:

1. Creates only operation-owned opaque objects.
2. Preallocates conservatively or proves sufficient incremental space.
3. Writes canonical disk extents while recomputing logical digests.
4. Syncs every complete component and returns a durable checkpoint.
5. Builds destination configuration without source host/runtime identifiers,
   mounts, endpoint grants, proxy values, or executable imported provisioning.
6. For every attached disk, preserves the authenticated filesystem type,
   emits the fresh Lima disk identity as an object with explicit
   `format: false`, and records the exact original-to-destination guest mount
   mapping. Omitted formatting authority is not accepted as equivalent.
7. Leaves every object stopped and unavailable to normal Hideout operations.

It may not choose a user-facing name, generate a control-plane ID, read a secret
value, or activate the object.

Repeated execution under the same effect binding resumes/verifies the same stage.
Unexpected existing bytes are not accepted merely because sizes match.

## Isolated adoption

`AdoptMigrationDestination` performs the only allowed preactivation boot.

The Manager request fixes the operation/effect binding, stage, environment,
import-time identity policy, authenticated source identity, and checksummed
helper. The provider initializes or validates Lima's destination-local durable
control key under Lima's own `_config` directory lock, generates request/receipt
nonces, returns the exact guest request alongside the receipt, and must replay
that same pair for the same binding. The Manager
validates every fixed field and the receipt/request match; the provider cannot
change the selected policy or permitted actions while filling its temporary
control-channel fields.

The provider constructs an ephemeral boot configuration with:

- no imported HostFS/workspace mounts;
- no public endpoint or port-forward grants;
- no imported proxy, DNS, network, command adapter, or provisioning authority;
- no ordinary Hideout workload/session startup;
- the destination Lima control public key and exact non-root guest user only;
- read-only mounts containing the package-candidate Linux adoption helper and a
  strict adoption request;
- a private writable receipt channel bound by operation/environment nonce;
- external network denied, while retaining only the provider control channel
  needed to prove completion and shutdown.

The fixed helper entry point applies exactly the requested `SafeClone` or
`ExactGuestRestore` actions, rebinds each authenticated original attached-disk
guest path to its fresh Lima mount, then atomically and idempotently adds the
bound destination control key to both the target user's and root's protected
`authorized_keys`. A rebind may replace only an absent or empty source mount
directory with the exact symlink, and only after the guest kernel mount
inventory proves the destination path and authenticated filesystem type.
Conflicting links, files, nonempty directories, unsupported filesystems,
missing mounts, or missing mappings fail without a completion receipt. Existing
guest keys remain guest data. A distinct
`rebind-attached-disk-mounts` result proves the complete mapping, and a distinct
`install-destination-ssh-keys` result makes failure observable before activation.
The same action installs an exact product-owned cloud-init override with
`ssh_deletekeys: false` and `disable_root: false`: Lima changes its cloud-init
instance ID on every boot, so this preserves the already-proved guest host keys
and root control path instead of silently rotating or restricting them again.
The helper never evaluates a string as shell, imports a user script, or accesses
bundle ciphertext. On completion it writes a schema-valid receipt and shuts the
guest down.

The provider waits for exact stopped proof, removes ephemeral adoption channels,
and returns the receipt plus provider observations. Timeout, helper/package digest
mismatch, unexpected network/mount presence, request/receipt nonce mismatch, or
failure to prove shutdown leaves the stage non-runnable and recoverable/rollback
required.

## Verification

`VerifyMigrationDestination` proves:

- all staged component logical digests and attachment edges;
- exact stopped state;
- normalized destination configuration and absence of source runtime identity;
- fresh backend identity supplied by the Manager;
- valid adoption receipt and helper/package binding;
- Safe Clone guest identity differs from source, or Exact Guest Restore guest
  identity equals source;
- adoption mounts, triggers, temporary credentials, and control artifacts are
  absent;
- no imported host/network/script authority is present in backend configuration.

The verification request carries each complete `AdoptionRequest` together with
its matching `AdoptionReceipt`, not the receipt alone. This lets the provider
recheck the authenticated source identity, import-time policy, request and
receipt nonces, permitted actions, destination public material, and exact helper
binding against its operation-owned post-adoption evidence. A receipt whose
fields are internally valid but which is paired with a substituted source
identity is not sufficient evidence.

Because the isolated boot intentionally mutates each root disk, the Lima
provider retains two linked proofs: the pre-adoption authenticated component
checkpoint and a post-shutdown file-identity record written only after the
temporary adoption channel is removed. Attached disks must still match their
original checkpoint exactly. Verification rejects an unknown staging-tree file
or directory, a changed root after the shutdown record, a changed attached disk,
or a top-level/runnable destination object.

A successful provider proof is necessary but not sufficient for activation. The
Manager also checks claims, secrets, profile records, authority decisions, effect
ledger, and plan binding.

## Rollback

Rollback is reverse-ordered, idempotent, and scoped to exact operation-owned
handles. It:

- stops an adoption guest if its operation owns that guest and can prove identity;
- removes ephemeral adoption channels;
- removes staged provider objects and disks that have no committed Manager owner;
- retains and reports any object whose ownership or stopped state cannot be proved;
- never deletes or edits the sealed bundle, source environment, existing active
  environment, or preexisting destination disk.

An inability to prove cleanup produces a durable recovery state and explicit
manual target; it is never reported as successful rollback.

## Lima provider v1

The first full provider supports only an allowlisted Lima compatibility range that
real package-candidate gates have proven. Initial implementation targets Lima
2.1.x and 2.2.x on macOS arm64 with supported Linux arm64 guest disk formats.

Capture uses the official stopped-instance clone behavior for roots and a
version-gated provider-owned snapshot of attached Lima disks. Adoption uses a
fresh opaque instance name and version-gated staged root-disk placement because
Lima exposes no supported cross-host root-instance import command.

Any unrecognized layout, driver, shared-disk graph, architecture, disk format,
Lima minor version, or helper mismatch returns
`migration.provider.compatibility_unproved`. It does not fall through to copying
the entire instance directory.

## Native provider v1

Native advertises config-only migration. It exercises Manager plan/apply,
bundle/config validation, claims, progress, cancellation, and recovery in tests.
It does not advertise persistent-disk capture/adoption and cannot satisfy release
evidence for full migration.

## Provider error contract

Provider errors carry:

- stable code;
- operation/effect binding;
- opaque backend/component reference;
- retryable Boolean;
- cleanup/recovery requirement;
- redacted operator remediation.

Raw command lines, stderr, filesystem paths, usernames, URLs with credentials,
keys, and guest file data remain in bounded privileged diagnostics only when the
existing redaction policy explicitly permits them; they never cross ordinary API,
audit, TUI, or WebUI projections.
