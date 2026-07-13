# Research: Supported CLI Runtime

<!-- markdownlint-disable MD013 MD060 -->

## Decision 1: Build A Hideout Runtime From Debian 13 Genericcloud

**Decision**: Use the versioned Debian 13 arm64 `genericcloud` QCOW2 as the
base for one Hideout-built `developer-standard` image. The first candidate base
is `20260706-2531`; its official SHA-512 is recorded in the source lock and its
independently measured SHA-256 is
`865a923004318e1eaba93816e12f5652ab4a0f93507fb213cfcfebd4105b63a6`.

**Rationale**: A real 2026-07-11 spike downloaded the 335,872,000-byte base via
the existing URL-plus-SHA-256 image declaration, booted it with Lima VZ, and
observed Debian 13.5, target UID 1000, no passwordless sudo, `curl`, and
`python3`. Debian publishes arm64 cloud images for local QEMU use, Debian main
requires free redistribution/derived-work rights, and the trademark policy
allows truthful nominative description without implying endorsement.

**Alternatives rejected**:

- Canonical Ubuntu derivative: the unmodified image booted, but Canonical's
  [IP policy](https://ubuntu.com/legal/intellectual-property-policy) adds
  debranding/rebuild obligations for redistributed modified images.
- Upstream Lima template alias: moving data, no stable tool contract.
- OCI/devcontainer image: current Hideout accepts bootable disk images, not OCI.
- First-boot provisioning: opaque, mutable, network-dependent, and contrary to
  the declarative image carve-out.

**Sources**:

- [Debian cloud image download](https://www.debian.org/distrib/)
- [Debian redistribution FAQ](https://www.debian.org/doc/manuals/debian-faq/redistributing.en.html)
- [Debian trademark policy](https://www.debian.org/trademark)
- [Debian archive policy](https://www.debian.org/doc/debian-policy/ch-archive)

## Decision 2: Build On Native Linux arm64 Before Publication

**Decision**: Build the derivative on a native Linux arm64 worker using
`qemu-img`, `virt-resize`, and `virt-customize`. The builder consumes only
versioned source locks, produces a new QCOW2, then runs a separate offline
inspection and boot verification before promotion.

**Rationale**: Native-architecture libguestfs avoids cross-architecture
emulation ambiguity. The official base disk is 3 GiB virtual and must be
expanded before installing the reviewed developer set. `virt-customize` is an
image-build tool and operates on a shut-down guest; it is not shipped or invoked
by `hideout run`.

**Alternatives rejected**:

- Build on user first boot: makes runtime readiness depend on mutable mirrors.
- Build inside Hideout's macOS package: adds libguestfs/QEMU product
  prerequisites and makes first run worse.
- Treat an ad hoc local build as catalog-ready: cannot provide retained URL or
  exact-candidate evidence.

## Decision 3: Use A Small Reviewed Multi-Language Baseline

**Decision**: Install a reviewed exact-version Debian-main package set for
shell/core, Git, TLS/download, JSON/archive, Python/pip/venv, native build,
network setup, SSH, and FUSE from a locked Debian snapshot/Release digest.
Install Node.js `v22.23.1` and Go `1.26.5` from their official digest-pinned
arm64 archives into `/usr/local`.

**Rationale**: Debian's base already proves Lima compatibility but is not a
developer runtime. Node 22 LTS with npm 10 avoids observed Node 24/npm 11
optional-dependency regressions for the selected real agent. Exact external
archive digests are stable build inputs:

- Node `v22.23.1` arm64 SHA-256:
  `0294e8b915ab75f92c7513d2fcb830ae06e10684e6c603e99a87dbf8835389c1`
- Go `1.26.5` arm64 SHA-256:
  `fe4789e92b1f33358680864bbe8704289e7bb5fc207d80623c308935bd696d49`

**Sources**:

- [Node.js v22.23.1 archive](https://nodejs.org/en/download/archive/v22.23.1)
- [Go downloads](https://go.dev/dl/)

**Alternatives rejected**:

- Debian Node package: currently too old for the real agent fixture.
- Node 24/npm 11: current upstream regressions make it a poor preview baseline.
- Universal all-language image: download and maintenance cost exceeds the v1
  product need.

## Decision 4: Keep Image Publication Separate From The Host Package

**Decision**: Publish the QCOW2 as a version-addressed HTTPS release asset. Ship
only the catalog, runtime contract, build source locks, and documentation in the
normal Hideout package. Lima continues to own download and caching.

**Rationale**: The host package remains reasonably sized and uses the existing
image declaration/cache mechanism. A package release can point to one reviewed
runtime revision without introducing a second downloader or cache.

**Alternatives rejected**:

- Embed the image in the tarball: multi-hundred-megabyte host package and
  duplicated cache ownership.
- Add a Hideout download cache: conflicts with established Lima ownership.
- Moving `latest` URL: cannot support immutable provenance.

## Decision 5: Make Runtime Metadata Package-Owned And Embedded

**Decision**: Keep the canonical catalog and contract as JSON under
`internal/runtimecatalog`, embed them in the Go binary, and copy the exact same
bytes into `share/hideout/runtime` during packaging. Gate 0 proves embedded,
source, packaged, and schema-validated representations match.

**Rationale**: Runtime selection remains available in source builds and cannot
be silently replaced after package verification. The external copy makes
support inspection and package checks possible without becoming a second
runtime truth source.

**Alternatives rejected**:

- Fetch catalog from a service: remote policy and availability dependency.
- Load an editable store catalog: creates an unreviewed runtime source.
- Compile all facts as Go constants: poor transparency and schema evolution.

## Decision 6: Resolve Before Creation And Pin Provenance

**Decision**: Resolve `developer-standard` to one concrete artifact before
profile/environment apply. Store stable selector data in the profile and exact
`RuntimeProvenance` in each environment. Never infer provenance by matching a
record's URL against the current catalog.

**Rationale**: Existing environment identity already pins `ImageRef`; additive
provenance explains why that ref was chosen. Explicit provenance avoids silently
promoting legacy/custom records and prevents catalog updates from changing a
running environment.

**Alternatives rejected**:

- Store only family name: mutable resolution changes environment identity.
- Match old URLs to current catalog: false preview claims after catalog drift.
- Bump every record version: additive fields preserve old records honestly.

## Decision 7: Observe The Real Guest On Every Run

**Decision**: Add a generic bounded runtime contract to `backend.RunSpec` and a
Lima observation step after guest start/privilege observation but before
network/bootstrap, HostFS setup, and the exact target-command check. Persist the
result through a Manager-owned sink.

**Rationale**: Reusable guests can drift. Catalog facts and historical receipts
cannot prove current commands. One batched guest observation keeps the cost
bounded, while the existing `CommandCheck` remains authoritative for the exact
requested command. Probing before setup ensures a missing image-owned setup
prerequisite is classified by the runtime contract rather than by whichever
setup script happens to fail first.

**Alternatives rejected**:

- Trust image catalog: false green after guest mutation.
- Add a second exact-command check in Manager: dispatch drift and duplicated
  error semantics.
- Probe from the host filesystem: cannot observe the guest target PATH.

## Decision 8: Separate Blocking Prerequisites From Baseline Readiness

**Decision**: Classify observations as `boundary` or `baseline`. Missing
boundary commands stops target execution. Missing baseline commands records
`preview-failed` and visibly warns, but an unrelated present exact target may
continue. Missing the exact requested command always stops through the existing
backend error.

**Rationale**: A broken setup prerequisite makes the boundary unverifiable; a
missing language tool means the runtime drifted but does not justify blocking a
different valid guest command. This preserves truthful status without turning
the runtime contract into broad command policy.

**Alternatives rejected**:

- Block on every baseline miss: makes unrelated recovery commands impossible.
- Keep preview-ready after a miss: false green.
- Auto-install missing tools: prohibited imperative provisioning authority.

## Decision 9: Store Receipts Outside Guest-Mounted Runtime State

**Decision**: Store `runtime-verification.json` beside `environment.json`, with
mode 0600 under the private environment directory. Do not place it in the
environment `runtime/` directory mounted at `/hideout/session`.

**Rationale**: The target must not rewrite evidence about its own readiness.
The receipt is host control-plane state, not guest content. It records bounded
version output and IDs, not credentials or host paths.

**Alternatives rejected**:

- Store in the guest image: mutable and stale.
- Store in mounted session runtime: target-visible and writable.
- Store only in audit: difficult current-state reads and no atomic replacement.

## Decision 10: Use One Generic Durable User Executable Prefix

**Decision**: Put `/hideout/profile/home/.local/bin` in the target PATH after
run-scoped shims and before system directories. The canonical install uses
`npm install --global --prefix "$HOME/.local"` and does not use sudo.

**Rationale**: Profile home is durable across ordinary stop/start and writable
by the target. The PATH addition is package-manager-neutral and keeps user tools
out of the immutable image and root-owned prefixes.

**Alternatives rejected**:

- `/usr/local`: requires root and couples tools to disposable image state.
- Host global install: violates the boundary and varies by machine.
- npm-specific Core configuration: provider-specific semantics in trusted Core.

## Decision 11: Use Codex CLI As A Named Evidence Fixture

**Decision**: Pin `@openai/codex@0.144.1` for the v1 registry install fixture,
record npm integrity
`sha512-Xir1zqPfpenhdoAoshN53uonzbBXj18COyzRkFlVZpSNyEl5XtkuYu9oddELePFN7K/0sXUcSO34Ad5IeCXPbw==`,
install with empty npm caches, and verify `codex --version`. Authentication is
not attempted.

**Rationale**: It is a real agent CLI with Linux arm64 distribution and an
official npm path. It exercises the exact Node/npm and privacy-network behavior
ordinary users need without adding login or browser dependencies.

**Source**: [OpenAI Codex CLI README](https://github.com/openai/codex/blob/main/README.md)

**Alternatives rejected**:

- Dummy package: cannot prove the product hypothesis.
- Latest version: moving input and non-repeatable evidence.
- Interactive login: callback/lease ownership is a separate feature.

## Decision 12: Keep Preview, Supported, And Release Claims Separate

**Decision**: 031 can produce `preview-ready` only. `supported` and
`release-ready` require a refresh pipeline, stale policy, published SBOM and
component governance, clean candidate provenance, and release evidence that are
outside v1.

**Rationale**: A one-time good image does not establish maintenance. The UI and
docs must not turn boot/tool success into a patch or support promise.

**Alternatives rejected**:

- Call the first image supported: maintenance overclaim.
- Keep all status binary: hides meaningful evidence maturity.

## Open External Promotion Dependency

The repository can implement and verify the build/promotion workflow locally,
but a real catalog entry requires a retained HTTPS asset and digest. GitHub CLI
authentication was unavailable during planning. Implementation may proceed to
a candidate artifact, but the feature cannot be marked Implemented or
`preview-ready` until the promoted asset is reachable and real Gate 2/Gate 3
evidence binds to it.
