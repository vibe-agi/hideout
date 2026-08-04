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

Full export adds the root disk and every reachable attached persistent disk for
each selected stopped environment. A shared disk closes over all of its
consumers; Hideout refuses a partial or running consumer set.

Every v1 scope excludes host workspace contents, HostFS contents, command/file/
network activity history, audit and release evidence, caches, live processes,
RAM, host applications, and ambient host credentials. Imported workspace paths
and authority-bearing host/network/script settings are proposals and remain
disabled until explicitly mapped or approved on the destination.

A full guest disk is opaque. It can contain application-managed credentials,
private source, SSH material, or other data that Hideout cannot selectively
remove without changing the filesystem. Full export therefore requires
`--ack-guest-content`.

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
configuration bytes, unique disks, logical size, named exclusions, and any stop
plan:

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

Guest identity is selected independently for each environment and each import:

- `safe-clone` is the default. It regenerates the guest machine identity and SSH
  host keys, so the same bundle can be imported on several computers.
- `exact-guest-restore` preserves the guest machine identity and SSH host keys.
  It requires `--ack migration.identity.exact_guest_restore_collision` and is
  intended only when the operator is retiring or isolating the source. Hideout
  cannot prove that a disconnected source remains off.

Import transforms only staged destination disks. The encrypted source bundle is
never modified or consumed.

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
tail. A partial export is never importable. Import stages private backend objects
and publishes an environment only after disk, identity, configuration, secret,
and provider verification. Recovery advertises only the action valid for the
current operation revision.

## Release Evidence and Non-claims

The portable, schema, fuzz, refinement, and no-side-effect inventory gate is:

```sh
scripts/gates/migration.sh
```

It fail-closes on drift from the checked-in migration test inventory across 13 packages,
the separate nine-category hostile-input matrix, and an exact 13-cut durable
restart inventory covering every export/import boundary listed in the feature
quickstart. Six migration fuzz targets consume checked-in wrong-shape,
traversal, sparse-abuse, trailing, and expansion seeds; four TLA+
configurations and seven Go refinement traces remain separate required
evidence.

The full-state release claim additionally consumes the exact clean package
candidate without rebuilding it:

```sh
scripts/gates/migration-lima.sh \
  --candidate-result .artifacts/045/package/result.json
```

That gate uses independent stores, root and attached disks, one unchanged
encrypted bundle, three Safe Clone imports, one Exact Guest Restore import,
materialization/adoption daemon crash recovery, fail-closed missing-executor
compatibility, terminal receipts, identity separation, host-workspace exclusion,
and source immutability. Its current physical-host limitation is recorded in its
evidence; cross-computer acceptance remains required before claiming broad
portability.

Migration performance qualification is separately deferred. Until its quiet-host
process-scoped gate passes, Hideout makes no migration CPU, I/O, peak-memory,
throughput, sparse-efficiency, or duration claim.
