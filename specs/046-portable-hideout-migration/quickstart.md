# Quickstart: Validate Portable Hideout Migration

> This is the implementation acceptance guide for the current canonical v1
> command surface. Run it with an exact clean package candidate; source-tree
> binaries do not establish a release claim.

## What this proves

The primary scenario exports one stopped environment on computer A, imports the
same unchanged bundle on computers B and C, and proves:

- files stored inside every persistent VM disk survive;
- persistent profile application state survives while cache/generated identity
  does not;
- host workspace contents are not embedded in the bundle;
- each destination gets fresh Hideout/control/backend identity;
- Safe Clone creates pairwise-distinct guest machine IDs and SSH host keys;
- imported host/network/script authority stays disabled until approved;
- the bundle remains byte-identical and reusable;
- failure or restart never exposes a partially imported environment.

Use non-production test environments and package-candidate binaries. The source
and destinations must be supported macOS arm64/Lima combinations advertised by
`hideout migrate capabilities`.

## 1. Prepare persistent fixtures on computer A

Inside the source VM, create fixtures on the root disk and on each attached
persistent disk:

```sh
mkdir -p /var/lib/hideout-migration-fixture
printf '%s\n' 'persistent root fixture' > /var/lib/hideout-migration-fixture/root.txt
dd if=/dev/zero of=/var/lib/hideout-migration-fixture/sparse.bin bs=1 count=0 seek=8G
sha256sum /var/lib/hideout-migration-fixture/root.txt
cat /etc/machine-id
ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub
```

For each attached disk, create a file, record its path, bytes, SHA-256, mode,
owner, and an xattr/symlink fixture supported by its filesystem. Record the source
guest machine ID and SSH host-key fingerprint outside the VM.

Create a separate host workspace fixture under a mounted workspace. Record its
hash; it must not appear when the bundle is inspected or imported without an
explicit destination mapping.

Resolve the selected profile directory from `hideout profile path PROFILE`, then
create distinct fixtures under `home/`, `config/`, `data/`, and `browser/`.
Also create negative controls under `cache/`, `machine/`, and
`home/.gitconfig`. Record hashes for all fixtures. The first four must survive;
cache must be absent, and machine identity/generated Git configuration must be
destination-generated rather than copied.

## 2. Review source eligibility

```sh
hideout migrate capabilities
hideout migrate export \
  --environment dev \
  --mode full \
  --out dev.hideout-migration \
  --ack-guest-content \
  --preview
```

Expected review:

- `dev`, its profile application-state component, and all reachable persistent
  disks are listed with separate logical-byte estimates.
- Host workspace contents, command/activity history, audit history, live process
  state, RAM, logs, profile cache, generated profile identity/configuration, and
  host runtime identity are listed as excluded. The review separately warns that
  included profile application state may contain credentials.
- The plan blocks while `dev` or any consumer of a shared disk is running.
- It prints the exact stop remediation; it never stops the VM automatically.
- Secrets are references only because no `--include-secret` was supplied.

Stop the environment using the command printed by the review, then regenerate the
plan. Confirm that every shared-disk consumer is selected and stopped or that the
plan fails closed.

## 3. Export and seal

```sh
hideout migrate export \
  --environment dev \
  --mode full \
  --out dev.hideout-migration \
  --ack-guest-content \
  --yes
```

Enter and confirm a migration passphrase at the protected prompt. Do not place it
in a shell variable, argument, or environment variable.

In another terminal:

```sh
hideout migrate status
hideout migrate status MIGRATION_OPERATION_ID
```

Expected behavior:

- Status reports source-stop requirement, logical/encoded bytes, component count,
  current component, elapsed time, and checkpoint age.
- Once the provider's immutable snapshots exist, status explicitly says the
  source may run again.
- The final path appears only after authentication and sealing succeed.
- The source VM, disks, profile application state, and profile record are
  unchanged.

Record the final bundle digest:

```sh
shasum -a 256 dev.hideout-migration
```

## 4. Inspect without mutation

```sh
hideout migrate inspect dev.hideout-migration
```

Expected output identifies the environment, profile application-state component,
every persistent disk, logical and encoded sizes, compatibility requirements,
excluded classes, secret-reference status, and Safe Clone default. It must not
reveal disk/profile file content, secret values, credentials, or host paths
containing embedded credentials.

Before entering the passphrase, make one byte-level copy and corrupt it. Inspection
of that copy must fail as authentication/corruption without creating profiles,
Keychain entries, instances, disks, drafts with authority, or operations that can
activate.

## 5. Transfer the unchanged bundle

Copy `dev.hideout-migration` to computers B and C using the operator's chosen
transport. Verify the same SHA-256 on all three computers. Hideout does not modify
or consume the bundle during import.

## 6. Preview Safe Clone on computer B

```sh
hideout migrate import dev.hideout-migration --preview
```

Choose or enter:

- source environment `dev`;
- destination name `dev-clone-b`;
- a valid destination host folder for each workspace, or leave that proposal
  disabled;
- destination secret references, without importing values by default;
- Safe Clone guest identity (the default);
- no imported network/endpoint/script/host-app authority unless the test case is
  explicitly validating an approval.

Expected review groups copied state, new identities, preserved opaque identity,
unresolved choices, and disabled grants. Preview creates no backend object, disk,
secret, profile, or environment.

## 7. Import on computer B

```sh
hideout migrate import dev.hideout-migration \
  --environment SOURCE_REF \
  --name SOURCE_REF=dev-clone-b \
  --policy SOURCE_REF=safe-clone \
  --yes
```

Confirm the immutable plan after resolving all blockers. Observe progress with:

```sh
hideout migrate status MIGRATION_OPERATION_ID
```

Expected behavior:

- Materialization uses fresh opaque backend objects.
- The only preactivation boot is isolated and runs the packaged adoption helper.
- The imported profile/environment is not visible/runnable until disks,
  profile-state digest and exact owner, identity receipt, secrets, claims, and
  configuration all verify.
- Completion does not automatically start the environment.
- Every later cold start proves the fresh attached-disk mounts and restores the
  authenticated original guest paths through root control before the runtime
  becomes ready or the first target command starts; the one-time adoption
  receipt alone is not sufficient.

Start it only after completion using the ordinary Hideout run/attach flow. Inside
the VM, verify:

```sh
sha256sum /var/lib/hideout-migration-fixture/root.txt
stat /var/lib/hideout-migration-fixture/sparse.bin
cat /etc/machine-id
ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub
```

Verify every attached-disk fixture at the same guest path recorded on computer
A, including modes, owners, symlinks, and xattrs. Inspect the destination Lima
configuration and require the fresh attached-disk object to carry explicit
`format: false` and the authenticated filesystem type. The original path must
resolve to that fresh destination mount, not merely to any `/mnt/lima-*`
directory. Stop and start the imported environment once more and repeat the
same path check; a cold boot must not depend on a symlink left by isolated
adoption. The guest machine ID and SSH fingerprint must differ from computer A.

Resolve the destination profile path named by the receipt. Verify the `home`,
`config`, `data`, and `browser` fixture hashes. Prove the cache fixture is absent,
the source machine fixture was not copied, and generated Git configuration does
not contain the source negative-control value.

Confirm the host workspace fixture is absent until its destination host folder is
explicitly mapped. Confirm disabled endpoint/proxy/script/pack proposals remain
ineffective.

## 8. Import the same bundle on computer C

Recheck that the bundle SHA-256 still matches computer A, then import:

```sh
hideout migrate import dev.hideout-migration \
  --environment SOURCE_REF \
  --name SOURCE_REF=dev-clone-c \
  --policy SOURCE_REF=safe-clone \
  --yes
```

Repeat the persistent fixture checks. Assert:

- B and C have the same expected disk and included profile application-state
  fixture bytes as A.
- A, B, and C have distinct Hideout environment/control/backend IDs.
- Safe Clone guest machine IDs are pairwise distinct across A, B, and C.
- Safe Clone SSH host-key fingerprints are pairwise distinct.
- The bundle SHA-256 is unchanged after both imports.

This is the defining multi-destination acceptance case.

## 9. Validate Exact Guest Restore separately

Use a separate destination name on an isolated test computer:

```sh
hideout migrate import dev.hideout-migration \
  --environment SOURCE_REF \
  --name SOURCE_REF=dev-exact \
  --policy SOURCE_REF=exact-guest-restore \
  --ack migration.identity.exact_guest_restore_collision \
  --yes
```

Without the exact acknowledgement, planning/apply must fail. With it, the review
must state that Hideout cannot prove source retirement or safe simultaneous use.

After completion, the guest machine ID and SSH host-key fingerprint must equal
computer A's recorded values, while Hideout environment/control/backend IDs remain
fresh. Do not run source and exact-restored copies together outside the isolated
acceptance setup.

## 10. Validate config-only fallback

On a destination where full-state capability is unavailable:

```sh
hideout migrate export \
  --environment dev \
  --mode config \
  --out dev-config.hideout-migration \
  --yes
```

The review must clearly say that VM disk files are not included. Import may create
validated environment/profile definitions only after path/authority review. It
must not imply that persistent guest files were restored.

## 11. Exercise interruption and recovery

Run deterministic test-only crash cuts after each durable phase:

- source claims acquired;
- provider snapshot created;
- bundle header synced;
- payload/checkpoint record synced;
- manifest written;
- footer written before final rename;
- import claims acquired;
- disk component synced;
- provisional secret prepared;
- adoption helper completed;
- provider verification completed;
- activation decision recorded;
- Manager visibility committed.

For export, `resume` must authenticate/truncate a torn tail and continue the same
operation. No partial artifact may pass `inspect` as sealed or be imported.

For import, recovery must advertise only `finish` or `rollback` actions justified
by durable state. Before commit, no staged environment may run. After a recorded
commit decision, recovery must finish the exact commit or report retained state;
it must not create a second environment. Rollback must leave existing environments
and the sealed bundle unchanged.

`scripts/gates/migration-crash-cuts.txt` is the normative fail-on-drift mapping
from these 13 cuts to exact Go test events. Adding, removing, renaming, or
replacing a cut requires changing the checked-in inventory and the gate's closed
expected-ID set together.

## 12. Run implementation gates

Fast and formal checks:

```sh
go test ./internal/migration ./internal/manager ./internal/backend/native
scripts/gates/migration.sh
scripts/gates/formal.sh
scripts/test-gate0.sh
```

Real provider acceptance of the exact macOS arm64 package candidate:

```sh
scripts/gates/migration-lima.sh \
  --candidate-result .artifacts/045/package/result.json
```

The Lima gate must use the packaged candidate binary and helper, not source-tree
fallbacks. The installed-candidate lane covers root/attached disks, identity
policies, host-workspace exclusion, multi-import, source immutability, and
cleanup. Sparse/shared-disk/crash-cut proofs remain required in the source gate
and physical cross-computer acceptance before a broad portability claim.

Do not infer full-state readiness from native tests. Do not run performance
qualification while unrelated high-load work makes the host unstable; when run,
record migration-process CPU/I/O/memory and host-noise evidence separately.
