# Data Model: Portable Hideout Migration

## Design rules

- Source records describe facts; destination records grant authority.
- A sealed bundle is immutable and reusable.
- Every import has a new operation ID, claims, identities, staging root, and
  decision record even when it reads the same bundle.
- Persistent guest payload is opaque to the host except for provider metadata and
  byte integrity. Hideout does not reinterpret files inside the disk during normal
  export.
- Secret values and passphrases never enter ordinary JSON state.
- Staged backend objects are not runnable through Hideout until one activation
  commit publishes their Manager records.

## Core entities

### MigrationBundleHeader

Public, fixed-size facts needed to reject unsupported input and derive the bundle
key.

| Field | Type | Rule |
| --- | --- | --- |
| `format_version` | integer | Must be explicitly supported |
| `bundle_id` | 128-bit ID | Unique per fresh export |
| `created_at` | UTC time | Informational; grants no authority |
| `suite` | enum | Exact KDF, wrap, AEAD, hash, compression suite |
| `salt` | bytes | Random and suite-sized |
| `kdf` | bounded parameters | Validated before allocation or derivation |
| `wrapped_master_key` | bytes | AEAD under passphrase-derived key |

The header contains no environment name, host path, secret name, or disk label.

### MigrationRecord

An independently authenticated frame.

| Field | Type | Rule |
| --- | --- | --- |
| `record_type` | enum | Known required type or explicitly skippable extension |
| `sequence` | unsigned integer | Starts at zero and increases exactly by one |
| `component_id` | opaque ID | Declared by the encrypted manifest |
| `ordinal` | unsigned integer | Strictly ordered within the component |
| `logical_offset` | integer | Nonoverlapping and within component |
| `plaintext_length` | unsigned integer | Within type and aggregate limits |
| `encoded_length` | unsigned integer | Fits file and configured bounds |
| `plaintext_digest` | SHA-256 | Bound into associated data and manifest |
| `nonce` | 192-bit random value | Unique within the bundle |
| `ciphertext` | bytes | XChaCha20-Poly1305 result |

Payload variants are `DataChunk`, `ZeroExtent`, and `HoleExtent`. Other record
types include metadata, selected secret material, checkpoint, final manifest, and
completion footer.

### MigrationManifest

Encrypted canonical index of everything the bundle claims to contain.

| Field | Type | Rule |
| --- | --- | --- |
| `bundle_id` | ID | Equals header and footer binding |
| `source_product_version` | version | Compatibility evidence only |
| `source_host_facts` | typed facts | OS/arch/backend; no host ID |
| `environments` | list | 1–32 normalized environment snapshots |
| `disk_objects` | list | Each persistent object captured once |
| `disk_edges` | list | Environment-to-disk attachment graph |
| `secret_entries` | list | Refs and optional value-record IDs |
| `component_index` | list | Ordered sizes, record ranges, and digests |
| `excluded_classes` | list | Explicit proof of data intentionally not copied |
| `required_capabilities` | list | Features destination must prove |

Canonical encoding, normalization, ordering, and size rules are versioned by the
format contract and checked before a plan can be created.

### EnvironmentSnapshot

Normalized source facts, never a destination Manager record.

| Field | Meaning |
| --- | --- |
| `source_environment_ref` | Bundle-local opaque reference |
| `display_name_hint` | Unclaimed source name hint |
| `runtime` / `backend` / `mode` | Compatibility inputs |
| `guest_user` | Authenticated source state; never guessed at import |
| `image_provenance` | Image reference/digest facts where known |
| `profile_snapshot` | Strictly validated inert profile values |
| `workspace_proposals` | Source guest paths and redacted host-path hints |
| `authority_proposals` | Disabled host/network/script/pack/endpoint proposals |
| `guest_identity_evidence` | Facts used to prove policy outcome |
| `disk_refs` | References into the manifest disk graph |

Source environment, machine, boot, session, and backend instance IDs are not
eligible destination IDs.

### DiskObject

| Field | Meaning |
| --- | --- |
| `disk_id` | Bundle-local opaque ID |
| `role` | `Root` or `Attached` |
| `format` | Provider-declared raw/qcow2-compatible kind |
| `logical_size` | Size including sparse regions |
| `allocated_size_hint` | Progress/space estimate only |
| `content_digest` | Digest over canonical logical extents |
| `provider_metadata` | Strict, non-authoritative compatibility facts |
| `consumers` | Selected environment references and attachment attributes |

Every persistent disk reachable from a selected full-state environment must occur
exactly once. Unknown or unclaimed reachable storage makes the export invalid.

### ExportOperation

Durable Manager-owned execution state.

| Field | Meaning |
| --- | --- |
| `operation_id` | Fresh ID and idempotency binding |
| `selection` | Explicit environments, mode, disks, selected secrets |
| `source_revisions` | Records/config revisions confirmed by the operator |
| `source_inventory_digest` | Authenticated stopped-disk graph |
| `claims` | Source/backend/disk and output-path claims |
| `snapshot_handles` | Provider-owned immutable snapshot references |
| `partial_path` | Owner-only output path; never accepted as sealed |
| `checkpoint` | Last authenticated record/component boundary |
| `phase` | Export state-machine phase |
| `evidence` | Redacted progress, warnings, and stable error code |

The passphrase, derived keys, bundle master key, and secret values are absent.

### ImportDraft

Mutable destination-side choices before confirmation.

| Field | Meaning |
| --- | --- |
| `draft_id` | Ephemeral/durable draft identity |
| `bundle_id` / `bundle_digest` | Exact sealed input binding |
| `selected_environments` | Subset to import, closed over shared disk graph |
| `name_mappings` | Source hints to new destination names |
| `workspace_mappings` | Explicit destination host/guest path decisions |
| `secret_mappings` | Existing ref, value import, or unresolved |
| `identity_policies` | Per-environment `SafeClone` or `ExactGuestRestore` |
| `authority_decisions` | Approve, edit, or reject each disabled proposal |
| `compatibility_evidence` | Backend, space, helper, schema, path checks |

A draft may be edited and revalidated. It cannot execute authority.

### ImportPlan

Immutable Go-validated plan produced from a draft.

| Field | Meaning |
| --- | --- |
| `plan_id` / `plan_digest` | Immutable review binding |
| `bundle_path` | Authenticated local input; hidden from public views |
| `base_revisions` | Destination store/provider facts used by validation |
| `bundle_binding` | Bundle ID, prefix digest, manifest digest, format version |
| `objects` | Exact profiles, environments, disks, and secret refs to create |
| `claims` | Names, backend objects, paths, and secret destinations |
| `identity_actions` | Fresh host IDs and guest policy |
| `authority_actions` | Exact approved destination grants |
| `risk_acknowledgements` | Typed high-risk acknowledgements |
| `expected_effects` | Ordered, typed, recovery-aware effects |

Any edit creates a new plan and invalidates the prior confirmation.

### ImportOperation

| Field | Meaning |
| --- | --- |
| `operation_id` | Fresh for each destination import |
| `plan_binding` | Exact immutable plan/digest |
| `phase` | Import state-machine phase |
| `claims` | Exclusive durable claim records |
| `staging_root` | `0700` Manager-owned operation directory |
| `provider_handles` | Opaque staged backend object IDs |
| `destination_disk_identities` | Fresh ID for every imported disk |
| `secret_handles` | Fresh provisional Keychain refs, never values |
| `adoption_receipts` | Typed nonce-bound guest results |
| `effect_ledger` | At-most-once effect and compensation state |
| `activation_commit` | Absent or one durable visibility commit |
| `evidence` | Redacted progress and stable error/recovery code |

Two operations reading the same bundle have no shared mutable state.
Each root disk identity is also bound to its owning destination environment.

### GuestIdentityPolicy

| Value | Guest outcome | Required acknowledgement |
| --- | --- | --- |
| `SafeClone` | New machine and SSH identity | Ordinary confirmation |
| `ExactGuestRestore` | Preserve machine and SSH identity | Collision warning |

The policy is chosen per environment in the draft and becomes immutable in the
confirmed plan. Hideout control-plane identities are never governed by this enum.

### AuthorityProposal

| Field | Meaning |
| --- | --- |
| `proposal_id` | Stable within bundle/import |
| `class` | Typed authority class |
| `source_fact` | Redacted source value/evidence |
| `destination_value` | Edited and canonicalized destination value |
| `risk` | Typed risk category |
| `validation` | Go-owned result and stable reason code |
| `decision` | `Disabled`, `Approved`, or `Rejected` |

`Disabled` is the import default. Only `Approved` proposals appear in
`authority_actions`.

### AdoptionRequest and AdoptionReceipt

The request is a strict, versioned, data-only document delivered during isolated
boot. It contains operation/environment nonces, identity policy, expected source
identity digest, destination SSH public material, and permitted actions. It
contains no shell fragment, proxy secret, workspace mount, or endpoint grant.

The receipt contains the same nonces, helper/package version, action results,
post-adoption identity digest, failure code if any, and a completion marker. The
host verifies schema, nonce, operation binding, expected policy outcome, and guest
shutdown before treating it as evidence. A receipt alone never activates an
environment.

### ProviderAdoptionEvidence

The destination provider durably binds the complete request/receipt pair to the
operation, adoption effect, capability revision, stage-owner digest, fresh
backend identity, exact root component, pre-adoption content digest,
post-adoption file identity, and shutdown proof. It is created only after the
guest is stopped and the temporary request/receipt/helper channel is absent.

This record is destination-local and operation-owned; it is not written into the
portable bundle. Therefore one immutable bundle can be imported repeatedly on
different computers, with each import choosing and proving its own Safe Clone or
Exact Guest Restore policy. The pre-adoption checkpoint remains the proof of the
authenticated bundle bytes; the provider evidence bridges the controlled root
mutation caused by adoption. Attached disks remain byte/checkpoint-identical.

### ProvisionalSecret

| Field | Meaning |
| --- | --- |
| `operation_id` | Owner/recovery binding |
| `new_secret_ref` | Fresh destination Keychain name |
| `logical_source_ref` | Redacted bundle-local mapping |
| `state` | `Prepared`, `Referenced`, or `Deleted` |

The value exists only in the bundle ciphertext, process memory, and destination
Keychain. Rollback deletes `Prepared` entries. Activation makes already-prepared
refs reachable; it does not overwrite an existing destination secret.

## Relationships

```text
MigrationBundleHeader 1──1 MigrationManifest
MigrationManifest     1──* EnvironmentSnapshot
MigrationManifest     1──* DiskObject
EnvironmentSnapshot   *──* DiskObject       (through disk_edges)

MigrationBundle       1──* ImportDraft      (across destinations)
ImportDraft           1──1 ImportPlan       (latest validated version only)
ImportPlan            1──1 ImportOperation
ImportOperation       1──* ProviderHandle
ImportOperation       1──* ProvisionalSecret
ImportOperation       1──* AdoptionReceipt
ImportOperation       1──1 ActivationCommit (at most one)
```

## Export state machine

```text
Draft
  -> Validating
  -> Claiming
  -> Snapshotting
  -> Writing
  -> Sealing
  -> Complete

Any executing phase -> RecoverableFailure -> same or next safe phase
Any pre-Sealing phase -> Cancelling -> Cancelled
Sealing crash -> RecoverableFailure -> authenticate/truncate -> Sealing
```

Rules:

- `Complete` requires a valid completion footer and atomic final-path publication.
- Source claims are released only after provider snapshot handles are independent
  or after cleanup.
- Cancel and failure never modify the source and never publish a partial bundle.
- Retrying an effect with the same operation/effect binding is idempotent.

## Import state machine

```text
Draft
  -> Validating
  -> AwaitingConfirmation
  -> Claiming
  -> Materializing
  -> PreparingSecrets
  -> Adopting
  -> Verifying
  -> Committing
  -> Complete

Executing phase -> RecoverableFailure -> recover/finish or rollback
Pre-Committing phase -> Cancelling -> RollingBack -> RolledBack
Committing crash -> RecoverableFailure -> finish recorded commit or rollback
```

Rules:

- `AwaitingConfirmation` exposes the entire immutable plan.
- No staged object is runnable through Hideout before `Complete`.
- `Committing` has one durable decision. Recovery follows that decision rather
  than operator inference from filesystem remnants.
- Rollback never changes the sealed input bundle or an existing active environment.
- Starting again with a new operation ID is a new clone, not a resume.

## Cross-entity invariants

1. A bundle is importable iff header, ordered records, manifest, prefix digest,
   and completion footer all authenticate and all declared limits hold.
2. Export effects do not write the source environment or its persistent disks.
3. Every persistent disk edge from a selected full-state environment resolves to
   exactly one captured `DiskObject`.
4. Destination control-plane and backend identities never equal source identities
   and are pairwise unique across imports.
5. `SafeClone` guest identity never equals the source or another Safe Clone result
   from the same bundle.
6. `ExactGuestRestore` guest identity equals the source; no cross-host uniqueness
   guarantee is asserted.
7. No authority proposal is effective unless its exact destination value was
   validated, included in the confirmed plan, and explicitly approved.
8. A plan, identity policy, bundle binding, and effect list cannot change after
   confirmation.
9. A destination name/backend object/secret destination has at most one live
   claim.
10. No unsealed, tampered, failed, rolling-back, or merely staged object is
    runnable.
11. Each durable effect and compensation is applied at most once for its operation
    binding.
12. Secret plaintext is absent from serializable entities, progress, logs, audit,
    errors, and UI snapshots.

## Retention and cleanup

- A sealed bundle is user-owned and never removed by import cleanup.
- Export partial files and provider snapshots are operation-owned. Completed,
  cancelled, and failed operations report whether they remain and provide an
  explicit recover/remove action.
- Import staging, provisional secrets, adoption mounts, and temp backend objects
  are operation-owned and removed after rollback or after their active records no
  longer need them.
- Migration operation/audit evidence follows the Manager store lifetime. It holds
  only redacted metadata, never disk content or secret values.
