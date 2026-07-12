# Implementation Plan: Supported CLI Runtime

<!-- markdownlint-disable MD013 MD060 -->

**Branch**: `031-supported-cli-runtime` | **Date**: 2026-07-11 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/031-supported-cli-runtime/spec.md`

## Summary

Deliver one explicit `developer-standard` preview runtime for macOS arm64 and
the Lima backend. A package-owned, embedded catalog resolves the operator-facing
runtime name to one immutable Hideout-built Debian-based QCOW2 artifact and one
declarative tool contract. Manager pins the resolved artifact and catalog
provenance into the environment record before creation. Lima observes the
actual running guest before every target command, persists one host-only
verification receipt, blocks missing boundary prerequisites and exact target
commands, and surfaces baseline drift without silently provisioning or falling
back to host tools.

The image is built before publication on a native Linux arm64 builder from a
versioned Debian cloud image plus digest-pinned Node.js and Go archives and a
reviewed Debian package set. The build emits the QCOW2, package inventory,
component manifest, SBOM status, checksums, size measurements, and provenance.
031 does not publish credentials, run first-boot package installation, flip the
default runtime, implement OAuth, own a second image cache, or add host
authority. Real Gate 2 and Gate 3 bind boundary and clean-cache agent-install
evidence to the exact catalog revision and image digest.

## Technical Context

**Language/Version**: Go 1.25; POSIX shell for package, image-build, and gate orchestration

**Primary Dependencies**: Existing standard library, Lima/limactl, JSON Schema validator, Debian cloud image, `qemu-img`, `virt-resize`/`virt-customize` on the image builder, Node.js 22 LTS, Go 1.26, npm registry fixture `@openai/codex`

**Storage**: Existing profile and environment JSON stores; additive runtime provenance in `environment.json`; host-only `runtime-verification.json` beside the environment record; embedded and package-copied catalog/contract JSON; external versioned QCOW2 release asset

**Testing**: Go unit/contract/integration tests; package and docs smoke tests; short-root declared-image smoke; real macOS arm64 Lima Gate 2 and Gate 3 with clean caches

**Target Platform**: Product claim is macOS arm64 host, Lima VZ backend, Linux aarch64 guest; native remains a weak mechanics harness

**Project Type**: Go CLI and local Manager control plane with external guest image build/release artifacts

**Performance Goals**: Published image at most 4 GiB compressed and 16 GiB virtual/expanded; warm ready environment reaches target execution within 120 seconds; one runtime verification shell round trip per run; catalog resolution is local and deterministic

**Constraints**: Explicit opt-in only; no first-boot provisioning; no silent image/default migration; no host credential or preauthenticated agent state in image; no shell fragments in runtime catalog; no second Hideout image cache; exact image URL and SHA-256 required; full baseline and privilege/network proof must use real Lima

**Scale/Scope**: One runtime family, one revision, one host/guest architecture tuple, one real agent install fixture, four user stories, 20 FRs, and 13 SCs

## Constitution Check

*GATE: Passed before research and re-checked after Phase 1 design.*

- **Privacy Boundary - PASS**: The feature changes guest image selection,
  reusable environment identity, guest readiness observation, and evidence. It
  grants no HostFS, endpoint, host-app, command-proxy, network, script, or root
  authority. Unsupported architecture, ambiguous selector, catalog drift,
  digest failure, missing boundary prerequisite, or missing exact command fails
  closed. A missing non-boundary baseline tool makes readiness visibly failed
  but does not invent authority or block an unrelated present command.
- **Typed Authority - PASS**: Existing typed init/profile and environment-create
  paths receive a validated runtime selector. Manager owns catalog resolution,
  provenance, status, receipts, recovery, and audit. The Lima backend owns only
  generic guest observations and exact command execution. No JavaScript or
  ecosystem pack participates.
- **Workspace And Policy - PASS**: Workspace, HostFS, environment filtering,
  proxy-secret handling, and effective policy remain unchanged. A runtime
  comparison test asserts zero policy-authority delta. The generic target PATH
  adds the durable user executable prefix only; it does not inherit host PATH.
- **Generality And Provider Scope - PASS**: Core understands generic runtime
  families, revisions, artifacts, observations, and receipts. Debian, Node.js,
  Go, npm, and `@openai/codex` are named image-build or evidence fixtures. No
  provider-specific package-manager logic enters Manager or backend semantics.
- **Evidence And Redaction - PASS**: Catalog inspection, environment provenance,
  current verification, doctor, Manager status, audit, Boundary Summary,
  product-evidence proofs, and Gate 2/3 manifests derive from the same typed
  model. Receipts contain command names and bounded version output, never
  proxy credentials, tokens, machine identity, host paths, or login state.
- **Backend And Distribution - PASS**: The package carries catalog and contract
  metadata, not the image blob. Lima retains download/cache ownership. The
  image is built before publication; no runtime InitTask or target command
  mutates the image. Native can validate local mechanics but cannot claim
  preview readiness.
- **Gates - PASS**: Gate 0 validates schemas, catalog/contract/package parity,
  selection, provenance, receipt, status, recovery, secret fixtures, docs, and
  the short-root image smoke. Real Gate 2 proves exact-image boot, tools,
  non-root/no-sudo, HostFS, projection, and environment behavior. Real Gate 3
  proves clean-cache agent installation through tun2socks and mediated DNS.
- **Status And Docs - PASS**: Update README quickstart, `docs/STATUS.md`,
  `docs/privacy-run-design.md`, `docs/privacy-run-test-plan.md`,
  `docs/threat-model.md`, `docs/claim-boundaries.md`, and package/docs indexes.
  Preview wording remains distinct from supported/release-ready.

## Design Decisions

### Runtime Source And Build

- Use Debian 13 `genericcloud` arm64 as the reviewed base. The real spike booted
  version `20260706-2531` under Hideout/Lima with UID 1000 and no passwordless
  sudo. Ubuntu was rejected for a modified distributable because Canonical's
  trademark policy adds debranding/rebuild obligations that are unnecessary
  for this slice.
- Publish a Hideout-built derivative rather than calling the base image a
  developer runtime. Build on native Linux arm64 with libguestfs; resize the
  disk, install the reviewed Debian package set, install digest-pinned Node.js
  and Go archives, remove builder state, normalize permissions, and compact the
  result. The guest never performs package installation on first boot.
- Keep the image outside the Hideout tarball and address it by a versioned HTTPS
  release URL plus SHA-256. The catalog records the upstream base URL and
  SHA-512, independently measured base SHA-256, output SHA-256, package
  inventory digest, SBOM status, source/license review, sizes, and build source.
- A workflow may build a candidate, but only a separately reviewed promotion
  updates the package catalog. A moving `latest`, CI artifact URL, local file,
  OCI reference, or unretained daily image cannot enter the catalog.

### Selection And Environment Identity

- `--runtime developer-standard` is accepted by `hideout init` and `hideout env
  create`; it is mutually exclusive with `--image`. Init stores the stable
  family selector and resolved provenance in the profile. Environment creation
  always resolves to and stores the concrete image declaration before writing
  the environment record.
- Add optional `RuntimeProvenance` fields to profile/environment models without
  changing existing record versions. Records without provenance remain custom
  or unmanaged. Existing records never infer runtime identity by matching a
  current catalog URL.
- Catalog changes do not mutate profiles or environments. A profile pinned to a
  removed revision fails with recovery guidance; an existing environment keeps
  its concrete image and recorded provenance but cannot claim current preview
  readiness if its catalog identity is unavailable.

### Verification And Command Execution

- The catalog contract contains only simple command observations: stable IDs,
  class (`boundary` or `baseline`), a simple command name, and bounded direct
  argv for version output. It rejects paths, shell interpreters, `-c`, control
  characters, environment mutations, redirections, and unknown fields.
- Manager passes the validated contract and an observation sink in `RunSpec`.
  Lima starts the guest, probes privilege, then executes image-owned boundary
  and baseline observations before network/bootstrap or HostFS setup. This
  ordering turns a missing setup prerequisite into the typed runtime failure
  instead of letting a later setup script fail first. Package/session helper
  availability remains owned by the existing setup checks. After setup, the
  existing exact target-command check remains authoritative; the runtime probe
  does not replace it.
- Missing boundary observations stop before target execution. Missing baseline
  observations persist `preview-failed`, emit audit/notice and visible warning,
  then permit an unrelated present exact command. Missing exact commands still
  return `backend.CommandNotFoundError`, now mapped to the shared 028 recovery
  record.
- Verification receipts live beside, not inside, the guest-mounted environment
  runtime directory. They bind environment ID, image ref, provenance, contract
  digest, observation time, privilege state, results, and overall status. A
  stopped environment renders `not-running` with last-observed context rather
  than presenting stale success as current.
- Add `/hideout/profile/home/.local/bin` ahead of system paths but after
  run-scoped shims in the Lima target PATH. This is a generic durable user
  executable prefix; no package-manager behavior or host PATH is imported.
- Before Lima starts a catalog-selected image download, Manager compares free
  space on the Lima data filesystem with the catalog's declared download plus
  virtual/working budget. An indeterminate or insufficient result fails before
  `limactl start`; it does not reserve space or claim byte-accurate progress.

### Product Surface

- `hideout runtime list` and `hideout runtime inspect developer-standard`
  expose package-owned catalog facts without starting a guest.
- `hideout runtime verify --env <name>` runs the same Manager/backend
  verification contract used before ordinary target execution. It never
  installs or repairs tools.
- `hideout env inspect`, doctor `--feature runtime`, Manager environment rows,
  run output, and Boundary Summary use one shared status builder. Machine output
  carries stable recovery records; human output includes executable commands.
- The canonical evidence fixture installs
  `@openai/codex@0.144.1` with npm 10 into `$HOME/.local`, clears npm caches
  first, and runs `$HOME/.local/bin/codex --version`. Login is explicitly not
  part of 031.

## Project Structure

### Documentation (this feature)

```text
specs/031-supported-cli-runtime/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── runtime-catalog.md
│   ├── runtime-image-build.md
│   ├── runtime-selection-status.md
│   └── runtime-verification.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/
├── runtimecatalog/
│   ├── catalog.go
│   ├── catalog.json
│   ├── contract.go
│   └── catalog_test.go
├── runtimeverify/
│   ├── model.go
│   ├── store.go
│   ├── status.go
│   └── *_test.go
├── environment/
├── profile/
├── backend/
│   └── lima/
├── manager/
├── app/
├── doctor/
├── packagekit/
├── productevidence/
└── recovery/

runtime/
└── developer-standard/
    ├── build.sh
    ├── packages.txt
    ├── sources.lock.json
    ├── verify-image.sh
    └── README.md

schemas/
├── runtime-catalog.schema.json
└── runtime-verification.schema.json

scripts/
├── package-local.sh
├── test-runtime-smoke.sh
├── test-runtime-image-build.sh
├── test-runtime-lima.sh
├── test-gate2-lima.sh
├── test-gate3-hidden-proxy.sh
└── test-env-image.sh

docs/
├── STATUS.md
├── privacy-run-design.md
├── privacy-run-test-plan.md
├── threat-model.md
└── claim-boundaries.md
```

**Structure Decision**: Two new internal packages separate immutable catalog
validation from mutable environment observations. Image-build inputs live in a
top-level `runtime/` tree because they produce a separately published guest
artifact, not a host binary. Existing Manager, backend, package, evidence, and
recovery packages receive narrow extensions rather than a second control plane.

## Phase Delivery

1. **Catalog and build contract**: schemas, source locks, deterministic build
   inputs, candidate image, artifact verification, package inclusion.
2. **Selection and provenance**: profile/env options, catalog resolution,
   additive records, mutual exclusion, inspection surfaces.
3. **Actual-guest verification**: bounded probe, receipts, status builder,
   privilege/policy preservation, missing-command recovery.
4. **Real agent path**: durable user prefix, docs, clean-cache direct and
   privacy installs, distinct recovery fixtures.
5. **Evidence and promotion**: Gate 0, exact-image Gate 2/3, product-evidence
   registry, docs/status truth, final adversarial review.

## Complexity Tracking

No constitution violation is required. The two new internal packages remove a
real ownership ambiguity: immutable release metadata is not mutable runtime
state, and backend observations are not catalog truth. The separate image-build
tree is required because a QCOW2 release has different tools, provenance, and
distribution lifecycle from the Go host package.
