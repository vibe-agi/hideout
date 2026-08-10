# Implementation Plan: Portable Hideout Migration

**Branch**: `046-portable-hideout-migration`
**Date**: 2026-08-02
**Spec**: [spec.md](./spec.md)

**Input**: Feature specification from
`/specs/046-portable-hideout-migration/spec.md`

## Summary

Add an encrypted, resumable migration workflow that exports normalized Hideout
configuration and, when requested, the persistent disk state of stopped Lima
environments plus the selected profile's `home`/`config`/`data`/`browser`
application state. The sealed bundle is immutable and destination-neutral. Each
import independently previews host-specific mappings and authority, stages all
artifacts under exact owners, generates fresh Hideout/profile identities, applies
the selected guest identity policy, validates the result, and only then activates
the profile and environment atomically.

The implementation adds a pure Go bundle layer, a durable Manager migration
operation built on the existing plan/review/apply and operation-ledger patterns,
and an optional backend migration provider. Lima is the first full-state provider;
native remains a config-only harness. Two bounded TLA+ models cover export sealing
and multi-destination adoption plus per-cold-start runtime mount readiness, with
Go refinement tests and real Lima gates.

## Technical Context

**Language/Version**: Go 1.25.12; TLA+ checked with the repository-pinned TLC
1.7.4; POSIX shell for gates; existing embedded HTML/CSS/JavaScript for WebUI

**Primary Dependencies**: Go standard library; `golang.org/x/crypto` for
Argon2id, HKDF, and XChaCha20-Poly1305; a pinned
`github.com/klauspost/compress/zstd` release for bounded per-chunk compression;
existing Bubble Tea/Bubbles/Lipgloss v2 UI stack; existing Lima CLI adapter

**Storage**: Append-only encrypted `.hideout-migration` bundles; existing
Manager JSON stores and operation ledger; macOS Keychain-backed secret broker;
Lima instance root disks, profile application roots, and operation-owned staging
directories

**Testing**: `go test`, race tests for new stateful services, fuzz/property tests
for hostile bundle input, shellcheck/gofmt/vet/static gates, bounded TLC models,
Go refinement tests, native config-only integration tests, and real macOS arm64
Lima package-candidate gates

**Target Platform**: macOS arm64 host with Lima 2.1.x/2.2.x and Linux arm64
guests for full-state migration; backend-independent config-only migration where
the existing Hideout host supports it

**Project Type**: Single Go CLI/daemon with Manager API, terminal UI, embedded
WebUI, backend adapters, packaged guest helper, schemas, and formal models

**Performance Goals**: Stream export/import without loading a disk or profile
application-state component into memory;
keep migration-process peak working memory at or below 256 MiB under the initial
supported envelope; preserve sparse regions; emit monotonic byte/component
progress; resume without rereading completed payload records for output

**Constraints**: Source must be provably stopped for full-state capture; no
plaintext disk, profile-state, or secret intermediate; bundle files are
owner-only; incomplete or tampered bundles never activate; passphrases never
appear in argv, environment, logs, audit data, or persisted Manager state; no
imported script/config executes during ordinary validation; unsupported
backend/layout/disk graphs and unsafe profile-state paths fail closed

**Resolved provider constraint**: Stock Lima 2.1.x/2.2.x VZ always attaches its
default user-mode network when no named network is configured. Full-state import
therefore remains fail-closed until a packaged Hideout adoption executor can boot
the staged guest with no network device and expose a revision-bound proof. This
does not block destination-neutral full-state export or config-only migration.

**Scale/Scope**: Initial explicit envelope is at most 32 environments, 4 TiB
aggregate logical persistent disk/profile data, 1,048,576 authenticated payload
records, and 4 MiB plaintext chunks. Values outside the envelope are rejected
before costly allocation or key derivation. Physical two-host acceptance is
required before the feature is marked generally available.

## Constitution Check

*GATE: Passed before Phase 0 research and re-checked after Phase 1 design.*

- **Privacy Boundary — PASS**: Full migration can expose every byte in a guest
  disk and included profile application state, plus endpoint references and
  selected secrets. Capture requires exact stopped-state and profile-source
  stability proof. Host workspaces, activity records, audit history, runtime
  files, profile cache, generated profile identity/configuration, and host-app
  observations are excluded. Unknown layouts, shared disks, invalid paths,
  aliases, special files, absent permissions, and ambiguous ownership fail
  closed. No partial bundle is importable.
- **Typed Authority — PASS**: The Manager owns immutable draft, plan, review,
  confirm, apply, recovery, and rollback state. Go validators parse every bundle
  and proposal. Backend code receives typed requests through an optional migration
  provider; UI JavaScript can only submit proposals that Go independently checks.
- **Workspace And Policy — PASS**: HostFS paths, workspace paths, command adapters,
  network/proxy endpoints, scripts, and pack grants never reactivate merely because
  they were exported. Import presents them as disabled proposals, rejects reserved
  roots and aliases, and requires explicit destination mapping and approval.
- **Generality And Provider Scope — PASS**: The bundle and Manager state machines
  are backend-neutral. Lima layout handling and the adoption helper are explicitly
  version-gated provider details. Native is documented as a weak config-only test
  harness, not evidence for full disk portability.
- **Evidence And Redaction — PASS**: Status, doctor, Manager API, TUI, WebUI, and
  audit events expose phases, byte counts, component IDs, policy choices, and
  redacted failure codes. They never expose passphrases, secret values, wrapped
  key material, proxy credentials, or unredacted authority-bearing URLs.
- **Backend And Distribution — PASS**: Full-state export requires an advertised
  backend migration capability. Lima uses packaged, checksummed helper artifacts
  and typed bootstrap/adoption requests; no network-fetched or stringly shell
  installer is introduced. Package-candidate tests prove the shipped helper.
- **Gates — PASS**: Gate 0 covers format/schema/static/crypto/fuzz/formal/refinement
  work. Native covers only config and operation mechanics. Full claims require
  real Lima lifecycle, disk, identity, crash-cut, sparse-image, secret, and package
  gates, followed by two-physical-host acceptance. Performance qualification is
  deferred until the host is stable and uses migration-scoped resource evidence.
- **Status And Docs — PASS**: Implementation must update `docs/STATUS.md`,
  `docs/privacy-run-design.md`, `docs/threat-model.md`,
  `docs/privacy-run-test-plan.md`, `docs/formal-models.md`, command help, and a
  dedicated migration operator guide before release.

### Post-design re-check

The Phase 1 entities and contracts retain Manager ownership, import-time identity
transformation, destination-side authority review, bounded hostile-input parsing,
staged activation, and provider fail-closed behavior. No constitutional exception
or unowned execution path was introduced.

## Project Structure

### Documentation (this feature)

```text
specs/046-portable-hideout-migration/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── backend-provider.md
│   ├── bundle-format.md
│   ├── manager-api.md
│   └── operator-ux.md
└── tasks.md                 # Created later by /speckit-tasks
```

### Source Code (repository root)

```text
cmd/
└── hideout-migration-adopt/       # Packaged, destination-neutral guest helper

formal/
├── MigrationBundle.tla            # Capture, resume, seal, cancel, crash
├── MigrationBundle.cfg
├── MigrationAdoption.tla          # Multi-destination import and identity
├── MigrationAdoption.cfg
└── cfg/                            # Focused safety/liveness configurations

internal/
├── app/                            # migrate command and command-catalog help
├── backend/
│   ├── migration.go               # Optional typed provider contract
│   ├── lima/                       # Version-gated full-state provider
│   └── native/                     # Config-only harness
├── daemon/                         # Long-running workers and embedded WebUI
├── helperbin/                      # Adoption-helper manifest integration
├── liveconsole/                    # Shared migration projections/actions
├── manager/                        # Plan/apply API, claims, ledger, recovery
├── migration/                      # Pure format, crypto, limits, and validation
├── profilestate/                   # Deterministic capture and exact-owner staging
└── tui/                            # Migration wizard/modal and progress view

schemas/
├── migration-manifest.schema.json
├── migration-operation-projection.schema.json
├── migration-plan.schema.json
└── migration-receipt.schema.json

scripts/gates/
├── migration.sh                    # Gate 0/native contracts
└── migration-lima.sh               # Real package-candidate Lima acceptance
```

**Structure Decision**: Extend the existing single Go project. Keep portable
format and validation in `internal/migration`, authority and durable state in
`internal/manager`, long-running execution in `internal/daemon`, and backend
filesystem/lifecycle details behind an optional provider interface. CLI, TUI, and
WebUI consume the same Manager plan and snapshot rather than implementing separate
migration logic.

## Complexity Tracking

No constitution violations require an exception. The second formal module and the
packaged guest helper are separate because export sealing and destination adoption
have different safety boundaries; combining them would obscure the invariants and
make backend-neutral bundle parsing depend on guest runtime behavior.
