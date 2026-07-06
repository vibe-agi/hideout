<!--
Sync Impact Report
Version change: 1.1.0 -> 1.2.0
Modified principles:
- II. Typed Authority Through Manager And Go Core: added the positive default
  that new flexible product judgments live in constrained JS decision points
  over Go primitives, not in compiled Go.
- IV. Evidence And Gates Are Product Requirements: replaced heuristic
  redaction wording with the deterministic redaction contract (Hideout-minted
  control-plane credentials stripped exactly; user/application data verbatim
  in host-local audit).
- Product Constraints: added the prosumer/MVP scope constraint; added the
  declarative guest base image carve-out to the ecosystem authority bullet;
  aligned the product definition with the mediation-first North Star.
Added sections:
- none
Removed sections:
- none
Templates requiring updates:
- ✅ .specify/templates/plan-template.md (no changes required)
- ✅ .specify/templates/spec-template.md (no changes required)
- ✅ .specify/templates/tasks-template.md (no changes required)
- ✅ docs/README.md
Follow-up TODOs: none
-->

# Hideout Constitution

## Core Principles

### I. Privacy Boundary Wins

Hideout MUST fail closed whenever a request cannot be represented by explicit
policy, typed capability, backend support, audit evidence, and cleanup behavior.
Compatibility MUST NOT silently fall back to ambient host authority. Missing
guest commands, unavailable helpers, ambiguous host paths, unverifiable network
routes, unknown script entrypoints, unsupported backend capabilities, and
unpromoted host reach-back paths MUST deny or stop before side effects.

Rationale: Hideout exists to replace ambient host authority with auditable,
bounded authority. A convenient fallback that escapes policy is a product
failure, not a compatibility feature.

### II. Typed Authority Through Manager And Go Core

Every host escape MUST terminate in a typed Go-owned capability provider after
Manager planning and Go-side validation. CLI, TUI, WebUI, the `hideoutd` daemon,
scripts, bundles, project manifests, and configuration MAY describe intent,
classify risk, and request bounded proposals; they MUST NOT execute authority,
invent action names, bypass validators, write raw profile state, or expose raw
host execution.

Rationale: Core owns authority and invariants. JavaScript and configuration own
logic, classification, and composition. This separation keeps extension points
useful without moving the security boundary into user code.

The default direction is positive as well as restrictive: a new flexible
product judgment SHOULD be built as a Go primitive plus a constrained JS
decision point. Hard-coding such a judgment into compiled Go requires a
recorded reason in plan Complexity Tracking. Capability execution, validators,
redaction, and transport remain Go-owned always.

Provider-specific integrations, package managers, agent CLIs, browsers,
editors, proxy ports, and test fixtures MUST NOT be described as generic
Hideout product semantics. They MAY appear only as named providers, examples,
lab/smoke fixtures, or operator-local setup, with the generic authority
contract kept separate from that specific implementation.

### III. Workspace Is Shared, Everything Else Is Policy

The project workspace is the intentional read/write collaboration surface and
MAY expose project-local secrets to the target. Hideout MUST reject workspaces
or passthrough mounts that are the host home, Hideout store, credential roots,
browser profile roots, or parents that would mount those roots into the guest
unless the operator uses an explicit high-risk override. Host files outside the
workspace MUST enter through HostFS or another typed authority. Deny rules MUST
win over allow rules.

Operator-authored HostFS grants created interactively or through explicit CLI,
TUI, WebUI, or Manager plan/apply operations remain user-authoritative except
for Hideout control-plane reserved roots. Non-operator-authored HostFS grant
proposals, including proposals from imports, bundles, recipes, project
manifests, templates, generated plans, or ecosystem artifacts, MUST NOT inherit
that user-authoritative status merely because a user installed or opened the
artifact. They MUST pass through a trust and review gate before they can add or
broaden host file authority.

Rationale: Workspace mounts and HostFS grants have different escape models.
Keeping them separate preserves normal development ergonomics while preventing
the workspace from becoming a disguised host-home mount.

### IV. Evidence And Gates Are Product Requirements

Material boundary decisions MUST be observable through structured audit,
Boundary Summary, `explain`, `doctor`, Manager API, TUI, or WebUI. Evidence MUST
be derived from authoritative runtime facts, not recomputed independently.
Redaction is deterministic, not heuristic: Hideout-minted control-plane
credentials (broker and UI token values, `HIDEOUT_SECRET_*` backing material,
generated machine-id, and Core control-plane field names) MUST never appear in
evidence, and hidden implementation paths stay out of user-facing output.
User/application request data, including target URLs and callback values, is
recorded verbatim in host-local audit; hiding sensitive target values is the
job of the lossy Boundary Summary and the export/share boundary, not of
storage-time guessing.
Any feature that crosses filesystem, network, backend, host-open, endpoint
exposure, script, or lifecycle boundaries MUST include positive tests and
fail-closed or redaction tests. Product isolation claims MUST be backed by the
relevant release gates, not by the weak native harness.

Rationale: A privacy runner that cannot show what happened cannot be trusted.
Tests and release evidence are part of the capability contract.

### V. Installability And Runtime Lifecycle Are Core

A feature is not product-complete if users must manually assemble hidden helper
binaries, bootstrap scripts, backend prerequisites, schemas, runtime
directories, or cleanup steps. First-run setup and repair MUST use typed
InitTask plans, not arbitrary shell scripts. Per-run authority MUST be
regenerated for each `hideout run` even when a warm environment is reused, and
normal interruption MUST allow ordered teardown of commands, bridges, HostFS,
network state, audit, environments, and session-local secrets.

Rationale: Hideout is more than one binary. Distribution, first-run repair,
session authority, and cleanup are part of the privacy boundary and must be
designed as product behavior.

## Product Constraints

Hideout runs untrusted developer tools and agentic CLIs inside a backend
boundary and mediates every host capability through typed, audited, fail-closed
gates; privacy outcomes are benefits of those gates. New features MUST preserve
these constraints:

- Hideout targets a professional individual operator on their own machine.
  Multi-tenant operation, organization policy distribution, approval workflows,
  delegated roles, and compliance reporting are out of scope. Security
  machinery MUST map to a threat the target operator actually faces; ecosystem
  trust machinery (signing, revocation, publisher identity, namespace
  protection) becomes a day-1 requirement when a public marketplace launches
  and is not designed ahead of that launch.

- Go Core and backend adapters are the trusted computing base for policy
  enforcement, host capability execution, secrets, and backend setup.
- Scripts are constrained Goja entrypoints. They MAY decide, redact, classify,
  or propose within supplied context, but MUST NOT access filesystem, network,
  process APIs, timers, mutable Hideout state, broker tokens, or backend
  handles.
- Backend names are substrate choices, not product semantics. A backend MAY be
  selected only when its capability matrix and gates support the requested
  product behavior. `--backend native` is a weak development harness, not
  isolation evidence.
- Network privacy hides proxy credentials and network origin when using
  tun2socks, but it is not a data-loss prevention system. Direct mode MUST be
  described as exposing normal network identity.
- Public ecosystem artifacts are not authority. Bundles, recipes, scripts, and
  project manifests MUST pass through parse, validate, verify, plan, apply, and
  effective-policy compilation before they affect runtime authority. Any
  non-operator-authored proposal that broadens HostFS, host app, endpoint,
  network, environment, profile, or mount authority requires an explicit trust
  and review step before enablement. One carve-out: a declarative guest base
  image reference (name plus digest) is guest-domain data, not host authority —
  backends consume it to start the guest and a bad image is contained by the
  boundary, so it does not trigger this host trust gate. Ecosystem-shared
  imperative environment preparation steps that Hideout would execute remain
  prohibited until a dedicated trust design.
- Documentation authority is layered: this constitution governs Spec Kit
  planning; `docs/architecture-principles.md` owns detailed principles;
  `docs/privacy-run-design.md` owns the Phase 1 product contract;
  `docs/threat-model.md` owns claims and non-claims; `docs/STATUS.md` owns
  current implementation status.

## Development Workflow

New work MUST follow the capability lifecycle:

```text
Probe -> Design Contract -> Product Path -> Release Gate
```

- Probe code MAY live behind `hideout lab`, tests, or internal packages, but it
  MUST NOT become a default `hideout run` path.
- A Design Contract MUST define domain model, authority shape, policy schema,
  audit fields, backend capability requirements, failure behavior, and status.
- A Product Path MUST use the same Manager model from CLI, TUI, WebUI, and
  automation. UI-only or CLI-only policy is not allowed.
- A Release Gate MUST cover each promoted privacy boundary. Gate 0 is required
  for docs, schemas, and static contracts. Gate 1 native smoke may prove shared
  CLI and Manager wiring, but it is not isolation evidence. Backend, network,
  browser, HostFS, endpoint exposure, and dogfood claims require their
  corresponding product gates.
- Plans and tasks MUST state which authority surfaces are touched, which gates
  are required, and which docs/status files are updated.
- Changes that alter implemented product status MUST update `docs/STATUS.md`.
  Changes that alter claims or non-claims MUST update `docs/threat-model.md`.
  Changes that introduce authority MUST update the relevant design contract and
  test plan before implementation is considered complete.

## Governance

This constitution supersedes generated feature specs, implementation plans, and
task lists. It does not replace the detailed architecture documents listed in
Product Constraints; instead, it provides the planning-level rules those
documents must satisfy.

Amendments require:

1. a written rationale that names the changed principle or section;
2. a semantic version bump;
3. synchronization of affected Spec Kit templates and architecture documents;
4. a review of `docs/STATUS.md`, `docs/threat-model.md`, and
   `docs/privacy-run-test-plan.md` when product status, claims, or gates change.

Versioning policy:

- MAJOR for backward-incompatible principle changes, removals, or redefinitions.
- MINOR for new principles, new governance sections, or materially expanded
  required guidance.
- PATCH for clarifications, typo fixes, and non-semantic refinements.

Compliance review is required during planning and before merge. A plan that
violates the constitution MAY proceed only if the violation is recorded in
Complexity Tracking with a concrete reason and a rejected simpler alternative.
Privacy-boundary violations that weaken fail-closed behavior, typed authority,
or audit evidence are not waivable.

**Version**: 1.2.0 | **Ratified**: 2026-07-05 | **Last Amended**: 2026-07-06
