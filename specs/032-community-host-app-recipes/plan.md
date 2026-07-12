# Implementation Plan: Community Host-App Recipes

<!-- markdownlint-disable MD013 MD060 -->

**Branch**: `032-community-host-app-recipes` | **Date**: 2026-07-11 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/032-community-host-app-recipes/spec.md`

## Summary

Generalize the existing `host.app.open-resource` projection from one embedded
VS Code recipe into an immutable, profile-scoped host-app pack lifecycle. V1
accepts local directories and exact-commit git sources, snapshots them into a
private store, validates a declarative open-resource grammar, independently
resolves the selected macOS app under Core-owned roots, and binds every future
run to an exact package/app/binding/permission identity. Community data may
select an existing capability and request a Core safety profile; it cannot
define host execution, authenticate itself, supply app identity at invocation,
weaken safe mode, or create a result channel. CLI and Manager share plan/apply
models; Gate 0 proves lifecycle and fail-closed invariants, while real macOS
arm64 Lima Gate 2 proves one externally installed pack reaches the same generic
provider for workspace and already-authorized HostFS resources.

## Technical Context

**Language/Version**: Go 1.25; POSIX shell for package and real-gate orchestration; JSON/JSON Schema for pack and evidence contracts

**Primary Dependencies**: Existing standard library, `golang.org/x/sys/unix` file locking, system `git` with isolated configuration, macOS `codesign`/filesystem metadata, existing Manager/decision/broker/HostFS/command-shim infrastructure

**Storage**: New private `host-app-packs/` registry with immutable revision snapshots, profile enablement records, permission acceptance, observed app identity, optional quality-test records, and retained lifecycle audit; existing profile/session/decision/audit stores remain authoritative for their domains

**Testing**: Go unit/contract/integration tests, JSON Schema tests, source-intake and unsigned-bundle race/TOCTOU tests, Manager/CLI parity tests including exact-commit Git intake, bounded performance tests, package/docs/Gate 0 smoke, and a distinct real macOS arm64 Lima Gate 2 host-effect proof that reuses 030 helpers without changing 030 evidence semantics

**Target Platform**: Product host-app launch support is macOS arm64; Linux/native exercise parse/lifecycle mechanics only and cannot satisfy host-effect claims

**Project Type**: Go CLI plus local Manager control plane and brokered guest-to-host capability provider

**Performance Goals**: Local pack inspect/plan completes within 500 ms excluding git acquisition and host signature checks; runtime binding compilation for up to 64 commands completes within 100 ms; launch validation adds no unbounded wait and preserves existing dedup behavior

**Constraints**: No generic host exec, no package hooks, no JavaScript grammar in v1, no guest-selected app identity, no package-defined safe state, no mutable source reads at runtime, no host path in public/guest data, no fallback, no new helper binary, and no authority for an already-running session

**Scale/Scope**: One professional operator; at most 32 installed packs, 16 retained revisions per pack, 16 apps and 32 bindings per pack, 64 projected command symbols per profile, 256 regular source files and 4 MiB snapshot bytes per revision

## Constitution Check

*GATE: Passed before research and re-checked after Phase 1 design.*

- **Privacy Boundary - PASS**: The feature touches an existing host-app escape,
  profile command authority, HostFS resource consumption, and package
  lifecycle. Unknown bindings, source drift, unsafe app roots, identity drift,
  missing content authority, and provider errors fail before launch. No request
  can fall through to generic host execution or a shadowed guest command.
- **Typed Authority - PASS**: Manager owns typed app add/enable/update/disable/
  remove plans and applies. The existing Go-owned `host.app.open-resource`
  provider remains the only host effect. Community JSON is strict data and is
  re-decoded into a binding-owned intent; it cannot carry app identity, host
  paths, capability overrides, scripts, or raw argv. No JavaScript participates
  in v1.
- **Workspace And Policy - PASS**: Workspace resolution stays session-bound.
  HostFS resources are consumed only after the existing policy proves active
  content authority; see-only visibility, reserved roots, ended portals, and
  canonical drift deny. The feature creates no HostFS grant or mount.
- **Generality And Provider Scope - PASS**: The product abstraction is a
  generic open-resource recipe and immutable binding. VS Code becomes built-in
  recipe/safety data and remains a named provider, not Core semantics. Cursor
  and other app names appear only in examples or test packs.
- **Evidence And Redaction - PASS**: Lifecycle, permission diff, observed app
  identity, launch/refusal, and revoke events come from authoritative state.
  CLI, Manager, doctor, audit, and Boundary Summary share the same inspection
  model. Tokens, raw argv, repository credentials, executable/host paths, and
  host username stay out of guest/public output; export uses the existing 005
  boundary.
- **Backend And Distribution - PASS**: No new product helper or first-boot
  setup exists. App pack schemas and built-in pack/safety data join the package
  checksum/install/repair/uninstall lifecycle. Native is a weak mechanics
  harness; real app-effect proof requires macOS arm64 Lima.
- **Gates - PASS**: Gate 0 covers source snapshots, schemas, app-root and
  identity adversarial fixtures, safety floor, immutable binding dispatch,
  scoped decisions, no fallback, redaction, package lifecycle, and 030
  regression. Gate 2 covers external-pack install, actual guest shim, host
  launch observation, workspace/HostFS mapping, safe/elevated scope, identity,
  disable/revoke, old-session immutability, and public evidence.
- **Status And Docs - PASS**: Implementation updates `docs/STATUS.md`,
  `docs/privacy-run-design.md`, `docs/threat-model.md`,
  `docs/privacy-run-test-plan.md`, `docs/claim-boundaries.md`, the projection
  subsystem doc, docs index, command examples, README ecosystem wording, and
  the product support matrix only after real proof.

Post-design re-check: all eight items remain PASS. The separate host-app pack
package removes authority ambiguity with adapter packs; the shared source
snapshot utility is authority-free infrastructure. No constitution waiver or
new product authority is introduced.

## Project Structure

### Documentation (this feature)

```text
specs/032-community-host-app-recipes/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── app-identity-safety.md
│   ├── app-lifecycle.md
│   ├── host-app-pack.md
│   └── open-resource-binding.md
├── checklists/
│   └── requirements.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/
├── packsnapshot/                 # authority-free local/git immutable intake
├── hostapppack/                  # manifest, registry, fingerprint, tests, evidence
├── hostcap/
│   ├── recipes/                  # built-in pack plus Core safety profiles
│   ├── appregistry.go            # assembled qualified app catalog
│   ├── appidentity.go            # root/ownership/signing/digest observation
│   ├── binding.go                # immutable per-run app binding
│   ├── openresource.go           # existing generic provider, bound intent only
│   └── appopen/                  # generic argv/state rendering and safety floor
├── cmdgrammar/
│   └── openresource.go           # declarative app-independent parser
├── cmdproxy/
│   └── cmdproxy.go               # registrations assembled from active bindings
├── broker/
│   ├── hostapp.go                # registered-command/binding validation
│   └── broker.go
├── manager/
│   ├── host_app_packs.go         # install/add/update/disable/remove plan/apply
│   ├── host_app_catalog.go       # profile/run catalog and inspection
│   ├── hostcap_projection.go     # app-scoped decision identity
│   ├── projection_inspection.go
│   └── run_dataplane.go          # immutable bindings/shims per new run
├── app/
│   └── app.go                    # `hideout app` consumer of Manager Core
└── recovery/
    └── registry.go

schemas/
├── host-app-pack.schema.json
├── host-app-pack-registry.schema.json
├── host-app-enablement.schema.json
├── host-app-inspection.schema.json
└── open-resource-intent.schema.json

scripts/
├── test-host-app-pack-smoke.sh
├── test-host-app-pack-e2e.sh
├── test-host-capability-projection-e2e.sh
├── test-host-capability-projection-smoke.sh
├── test-gate0.sh
└── lib/gate2-projection.sh

docs/
├── host-capability-projection.md
├── privacy-run-design.md
├── privacy-run-test-plan.md
├── threat-model.md
├── claim-boundaries.md
└── STATUS.md
```

**Structure Decision**: Add one domain package, `internal/hostapppack`, because
community app recipes have a distinct authority/trust lifecycle from
authority-free JavaScript adapter packs. Extract only source copying/git
hardening/digest primitives into `internal/packsnapshot`; both package types
keep separate manifests, registries, validators, and enablement semantics.
Refactor the existing `hostcap`, command grammar/proxy, broker, Manager, and app
layers in place so built-in and community recipes cannot form parallel runtime
paths.

## Complexity Tracking

No constitution violations require justification. The new `hostapppack`
package is an ownership boundary, not a parallel capability system: it owns
untrusted package lifecycle while `hostcap` retains all host-effect authority.
