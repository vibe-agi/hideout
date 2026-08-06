# Portable Migration

<!-- markdownlint-disable MD013 -->

Hideout migration creates one encrypted, destination-neutral file that can be
inspected and imported more than once. It is a copy operation: export never
deletes the source, and later changes on the source and destinations do not
synchronize.

Use `hideout help migrate` for the exact command surface shipped by the current
binary. The capability result, not this document alone, decides whether a
specific backend can perform a full-state migration:

```sh
hideout migrate capabilities
```

## What v1 Copies

Configuration-only export contains:

- selected environment declarations;
- the normalized portable profiles they reference; and
- selected Hideout-managed secret values only when every named value and the
  separate secret-transfer acknowledgement were supplied.

Full export adds two kinds of persistent state for each selected stopped
environment:

- the root disk and every reachable attached persistent disk; and
- the referenced profile's application state under `home/`, `config/`,
  `data/`, and `browser/`.

For each attached disk, the authenticated graph also preserves its supported
filesystem type and original Lima guest mount path. A fresh destination disk
handle is an implementation identity; it must not rename the path applications
inside the guest use.

A shared disk closes over all of its consumers; Hideout refuses a partial or
running consumer set. Profile application state is captured as a bounded,
deterministically ordered component and is named separately in inventory and
size estimates because it may contain login history, browser sessions, tokens,
or other application-managed credentials.

Every v1 scope excludes host workspace contents, HostFS contents, command/file/
network activity history, audit and release evidence, Hideout profile caches,
live processes, RAM, host applications, and ambient host credentials. Full
profile-state capture also excludes generated profile machine identity and
Hideout-generated Git configuration; import recreates them for the destination.
An opaque guest disk can still contain its own caches or credentials. Imported
workspace paths and authority-bearing host/network/script settings are proposals
and remain disabled until explicitly mapped or approved on the destination.

A full guest disk is opaque, and included profile application state is private
application data. Either can contain application-managed credentials, private
source, SSH material, browser sessions, or other data that Hideout cannot
classify safely. Full export therefore requires `--ack-guest-content`.

## Clean v1 Compatibility Policy

There is no installed user base whose unpublished development state must be
preserved. The first supported release consequently has one canonical format:

- writers emit migration bundle format v1 only;
- readers accept format v1 only and reject unknown fields, trailing data,
  noncanonical portable profiles, and old or future versions;
- old development bundles must be re-exported with the release binary; and
- old development environment/store records must be cleaned and recreated
  instead of being guessed through a dual reader.

This is fail-closed behavior, not an automatic data upgrade. Future compatibility
work requires an explicit versioned design and release gate.

## Export

Preview a configuration-only export:

```sh
hideout migrate export \
  --environment dev \
  --mode config \
  --out ./dev-config.hideout-migration \
  --preview
```

Apply the reviewed plan:

```sh
hideout migrate export \
  --environment dev \
  --mode config \
  --out ./dev-config.hideout-migration \
  --yes
```

For a full stopped-VM copy, first review the concrete environments, portable
configuration bytes, profile application-state bytes, unique disks, total
logical size, named exclusions, and any stop plan:

```sh
hideout migrate export \
  --environment dev \
  --out ./dev.hideout-migration \
  --ack-guest-content \
  --preview
```

If the review offers an eligible coordinated stop, `--stop` authorizes only
that separately planned lifecycle operation. It does not stop the daemon:

```sh
hideout migrate export \
  --environment dev \
  --out ./dev.hideout-migration \
  --ack-guest-content \
  --stop \
  --yes
```

`--all` is an explicit selector. Hideout expands it into named environments and
disks before confirmation; it never means host data or ambient authority.

The completed bundle is one owner-only file. The hidden terminal prompt is the
default passphrase input. Automation may use `--passphrase-stdin` from a private
file descriptor. Never place a passphrase in argv, a URL, or an environment
variable.

## Inspect and Import

Inspection authenticates the bundle and makes no profile, environment, disk, or
Keychain change:

```sh
hideout migrate inspect ./dev.hideout-migration
hideout migrate inspect ./dev.hideout-migration --json
```

Use the authenticated source reference printed by inspection to make destination
choices explicit:

```sh
hideout migrate import ./dev.hideout-migration \
  --environment SOURCE_REF \
  --name SOURCE_REF=dev-copy \
  --policy SOURCE_REF=safe-clone \
  --preview
```

Then apply that reviewed scope:

```sh
hideout migrate import ./dev.hideout-migration \
  --environment SOURCE_REF \
  --name SOURCE_REF=dev-copy \
  --policy SOURCE_REF=safe-clone \
  --yes
```

Interactive preview may begin without a selector so the operator can inspect
the inventory. Noninteractive `--yes` must repeat `--environment` or say
`--all`; Hideout refuses to guess the import scope.

Name collisions refuse by default. `--name`, `--workspace`, `--secret`, and
`--approve` resolve exact authenticated proposals. `--replace` produces a
separate destructive review and cannot silently replace an environment.

## Identity Policy

Every import creates fresh Hideout environment/control, backend instance,
operation, session, broker, workspace, and ephemeral credential identities.
This rule has no switch.

The destination profile is also fresh. Its preserved `home/`, `config/`,
`data/`, and `browser/` roots are published atomically with that profile, while
profile machine identity and generated Git configuration are recreated and
cache remains absent. This destination-local reset is independent of the guest
identity policy below.

Guest identity is selected independently for each environment and each import:

- `safe-clone` is the default. It regenerates the guest machine identity and SSH
  host keys, so the same bundle can be imported on several computers.
- `exact-guest-restore` preserves the guest machine identity and SSH host keys.
  It requires `--ack migration.identity.exact_guest_restore_collision` and is
  intended only when the operator is retiring or isolating the source. Hideout
  cannot prove that a disconnected source remains off.

Import transforms only staged destination disks. The encrypted source bundle is
never modified or consumed.

For imported attached disks, Hideout writes an explicit Lima object entry with
`format: false` and the authenticated filesystem type. It never relies on
Lima's omitted-field default, which may initialize a disk whose label does not
match its fresh destination handle. During the isolated adoption boot, Hideout
first verifies the exact destination mount point and filesystem type against the
guest kernel mount inventory, then binds the original guest mount path to that
fresh destination mount and receipts the exact mapping. An absent, occupied,
conflicting, unsupported, or unproved path fails before activation rather than
hiding files or guessing.

## Encryption and Local Trust

Bundle records use Argon2id-derived key wrapping, HKDF-separated keys, and
XChaCha20-Poly1305 authenticated encryption. Authentication covers the manifest,
record ordering, logical digests, checkpoints, and sealed footer. A wrong key,
changed byte, torn tail, reordered record, unknown version, or trailing content
fails closed.

Encryption protects a bundle at rest and in operator-chosen transfer. It does
not protect against malware running as the same local macOS account while the
operator unlocks the bundle, a weak/reused passphrase, screen or keyboard
capture, or secrets already stored inside an opaque guest disk. A migration
bundle is private recovery material; do not publish it or attach it to a support
request.

## Progress and Recovery

Operations are durable and expose one shared CLI/TUI/Web/API projection:

```sh
hideout migrate status
hideout migrate status OPERATION_ID
hideout migrate resume OPERATION_ID
hideout migrate cancel OPERATION_ID --retain-partial
hideout migrate cancel OPERATION_ID --remove-partial --yes
hideout migrate recover OPERATION_ID
```

Resume authenticates the retained checkpoint and discards an unverified torn
tail. A partial export is never importable. Import stages private backend
objects and exact-owner profile application state, then publishes the fresh
profile and environment only after profile-state digest, disk, identity,
configuration, attached-disk non-format/mount binding, secret, and provider
verification. Rollback removes only the operation-bound stage. Recovery
advertises only the action valid for the current operation revision.

## Release Evidence and Non-claims

The portable, schema, fuzz, refinement, and no-side-effect inventory gate is:

```sh
scripts/gates/migration.sh
```

It fail-closes on drift from the checked-in 225-test migration inventory across
19 packages, the separate nine-category hostile-input matrix, and an exact
13-cut durable restart inventory covering every export/import boundary listed
in the feature quickstart. Discovery scans every active repository package and
owns both explicitly migration-named tests and every test in migration-specific
files or the dedicated profile-state package; a new package or generically
named safety test therefore cannot disappear behind the old inventory. Six
migration fuzz targets consume checked-in wrong-shape, traversal, sparse-abuse,
trailing, and expansion seeds; four TLA+ configurations and seven Go refinement
traces remain separate required evidence.

The full-state release claim additionally consumes the exact clean package
candidate without rebuilding it:

```sh
scripts/gates/migration-lima.sh \
  --candidate-result .artifacts/045/package/result.json
```

That gate uses independent stores, distinct root-disk, attached-disk, and
profile `home`/`config`/`data`/`browser` sentinels, explicit cache/generated-state
negative controls, one unchanged encrypted bundle, three Safe Clone imports,
one Exact Guest Restore import, materialization/adoption daemon crash recovery,
fail-closed missing-executor compatibility, terminal receipts, identity
separation, exact original attached-disk path, explicit `format: false` plus
filesystem type, host-workspace exclusion, and source immutability. After export it
retains a candidate-bound, secret-authenticated checkpoint so a post-export
failure can resume without repeating source setup and export; invalid or stale
checkpoints fail closed. Its current physical-host limitation is recorded in
its evidence; cross-computer acceptance remains required before claiming broad
portability.

Migration performance qualification is separately deferred. Until its quiet-host
process-scoped gate passes, Hideout makes no migration CPU, I/O, peak-memory,
throughput, sparse-efficiency, or duration claim.
