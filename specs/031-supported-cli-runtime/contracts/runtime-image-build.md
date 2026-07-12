# Runtime Image Build Contract

<!-- markdownlint-disable MD013 MD060 -->

## Boundary

The image builder creates guest-domain data before release. It is not invoked
by `hideout init`, `env create`, `run`, doctor repair, or Manager apply. The
published image carries no live Hideout authority.

## Locked Inputs

`runtime/developer-standard/sources.lock.json` must bind:

- versioned Debian genericcloud arm64 URL, official SHA-512, and measured
  SHA-256;
- sorted Debian package names with exact versions, snapshot timestamp,
  timestamp-matched package endpoint, and verified Release-file digest;
- Node.js archive URL/version/SHA-256;
- Go archive URL/version/SHA-256;
- build container/tool identity;
- output virtual-size target;
- source commit and dirty state.

Every download is verified before use. The build rejects a dirty source for a
promoted artifact; dirty builds may produce local development candidates only.

## Build Steps

1. Check free space and native aarch64 builder identity. A direct local build
   records `native-unpinned`; only retained CI provenance may bind the locked
   builder image declaration for promotion review.
2. Download and verify locked base.
3. Create a new output image; never mutate the retained base in place.
4. Expand the root filesystem and partition to the reviewed virtual size.
5. Resolve the complete Debian package closure on the builder from the locked,
   Release-bound `Packages.xz`. Verify every downloaded package digest and size
   against that index, then install the verified closure through libguestfs
   with guest networking disabled. Missing versions, payload drift, or a
   changed Release digest fail closed; the builder never silently substitutes
   a newer mirror package.
6. Install verified Node.js and Go archives into `/usr/local`.
7. Create no target user, credential, token, profile, workspace, HostFS grant,
   proxy config, agent login, or preinstalled agent CLI.
8. Remove package indexes, temporary files, logs, SSH host keys, machine ID,
   cloud-init instance data, and builder credentials; leave regeneration to
   normal guest boot/Hideout identity setup.
9. Normalize ownership and executable permissions; compact and validate QCOW2.
10. Produce outputs below without publishing them automatically.

The builder must not download and execute remote shell scripts.

## Required Outputs

- `developer-standard-<revision>-linux-aarch64.qcow2`
- `SHA256SUMS`
- `package-inventory.txt` and digest
- `component-manifest.json` with source/version/license metadata
- SBOM plus digest when available, otherwise explicit unavailable status
- `build-provenance.json` with inputs, tool versions, commit, dirty state,
  timing, sizes, and result
- `verification-report.json` from offline inspection and clean boot probe

No output may contain credentials, local absolute builder paths, generated host
identity, or mutable `latest` references.

## Candidate Verification

Before promotion:

- `qemu-img check` succeeds;
- measured compressed and virtual sizes meet the catalog budget;
- offline filesystem inspection finds every expected binary and no forbidden
  credential/state fixture;
- clean Lima boot with URL-plus-SHA-256 succeeds;
- actual guest contract passes as target UID 1000 with passwordless sudo absent;
- existing HostFS/network helpers and privileged setup work;
- image digest in all reports is identical.

## Promotion

Promotion is a separate, explicit operation:

1. Upload to a retained versioned HTTPS asset.
2. Download the uploaded bytes to a clean machine and re-check SHA-256.
3. Update catalog with that URL/digest and reviewed metadata.
4. Package catalog/contract.
5. Run real Gate 2 and Gate 3 against the uploaded asset.
6. Bind product evidence to clean candidate commit, package digest, runtime
   revision, image digest, and gate manifests.

CI build success alone does not promote or make the runtime preview-ready.

## Claim Boundary

V1 may claim a digest-pinned preview runtime passed the declared tests. It does
not claim bit-for-bit reproducibility, automatic security patching, an update
SLA, supported status, or release readiness.
