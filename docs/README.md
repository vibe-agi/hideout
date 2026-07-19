# Hideout Docs

<!-- markdownlint-disable MD013 -->

This directory is the design and verification source for Hideout. Start here
when reviewing architecture changes.

## Reading Order

1. [architecture-principles.md](architecture-principles.md) defines the detailed
   product and engineering principles for architecture work.
2. [privacy-run-design.md](privacy-run-design.md) defines the Phase 1 product
   contract and detailed subsystem contracts.
3. [threat-model.md](threat-model.md) defines claims, non-claims, TCB, and host
   reach-back invariants.
4. [STATUS.md](STATUS.md) summarizes current implementation status. Update this
   file when code changes the delivered product surface.
5. [support-matrix.md](support-matrix.md) defines the alpha platform/backend,
   gate-required feature, schema/ABI, and non-claim matrix.
6. [privacy-run-test-plan.md](privacy-run-test-plan.md) defines gates and
   release evidence.
7. [first-run-alpha.md](first-run-alpha.md) is the canonical external-alpha
   first-run path for package install, privacy init, doctor, first run, and
   recovery.

Subsystem documents:

| Area | Document |
| --- | --- |
| Backend capability | [backend-capability-matrix.md](backend-capability-matrix.md) |
| Distribution and first run | [distribution-bootstrap.md](distribution-bootstrap.md) |
| External alpha first run | [first-run-alpha.md](first-run-alpha.md) |
| Formal lifecycle models | [formal-models.md](formal-models.md) |
| Ecosystem foundation | [ecosystem-foundation-design.md](ecosystem-foundation-design.md): canonical resource model, policy composition, Hideoutfile contract, guest base-environment artifact class (declarative base image references; distinct from imperative environment recipes, which remain prohibited), and ecosystem delivery sequence. |
| HostFS overlay | [hostfs-overlay-design.md](hostfs-overlay-design.md) |
| Host capability projection | [host-capability-projection.md](host-capability-projection.md) |
| Community Host-App Recipes | [host-app-recipes.md](host-app-recipes.md): implemented 032 operator/contributor lifecycle, authority boundary, CLI shape, and proof requirements. |
| Supported CLI runtime preview | [031 spec](../specs/031-supported-cli-runtime/spec.md), [runtime contracts](../specs/031-supported-cli-runtime/contracts/runtime-catalog.md) |
| Init tasks | [init-task-architecture.md](init-task-architecture.md) |
| Manager control plane | [manager-control-plane.md](manager-control-plane.md) |
| Network privacy | [network-privacy-architecture.md](network-privacy-architecture.md) |
| OpenTarget and host reach-back | [opentarget-architecture.md](opentarget-architecture.md) |
| Policy/config supply chain | [policy-config-supply-chain.md](policy-config-supply-chain.md): authoring, source resolution, install, update, trust, override, and export behavior for the ecosystem model. |
| Script adapters | [script-extension-architecture.md](script-extension-architecture.md) |
| Support matrix | [support-matrix.md](support-matrix.md) |
| TUI/Web UI | [tui-webui-experience.md](tui-webui-experience.md) |

## Authority

- `.specify/memory/constitution.md` owns Spec Kit planning gates and summarizes
  the non-negotiable rules for generated specs, plans, and task lists.
- `architecture-principles.md` owns principles.
- `privacy-run-design.md` owns the Phase 1 product contract.
- `threat-model.md` owns security claims and non-claims.
- `STATUS.md` owns current implementation status.
- `support-matrix.md` mirrors the Go-owned alpha support matrix and release
  readiness/non-claim posture.
- `ecosystem-foundation-design.md` owns the ecosystem resource model,
  effective policy composition order, project manifest authority model, the
  guest base-environment artifact class (declarative base image references,
  not imperative recipes), and ecosystem delivery sequence.
- `policy-config-supply-chain.md` owns supply-chain operations for that model.
- Subsystem documents must not create authority that conflicts with those files.

When a status sentence in a subsystem document becomes stale, update
[STATUS.md](STATUS.md) and replace the stale sentence with a link or a narrow
contract statement.

## Status Terms

```text
implemented
  Code exists and is covered by local tests or gates.

implementing / claim pending
  Code or contracts may exist, but required completion evidence is unfinished
  and the feature must not be described as implemented or validated.

product path
  Implemented user-facing path for supported backends.

design-ready
  Contract shape exists; code may be partial or absent.

lab
  Diagnostic or probe path only; not product authority.

later
  Not part of the current product path.
```

## Naming Registries

Hideout uses separate registries for different layers:

- policy and broker action names use forms such as `host.open`,
  `host.fs.read`, and `endpoint.expose.host-to-guest`;
- backend capability flags use forms such as `filesystem.hostfs.read`,
  `network.tun2socks`, `guestPrivilege`, and `portBridge`;
- privilege evidence uses `guest.privilege.status`,
  `hideout.privileged_setup`, `hideout.privileged_cleanup`, and
  `target.root_attempt`;
- capability decision, command-adapter outcome, and route vocabularies are owned
  by [privacy-run-design.md](privacy-run-design.md): decisions
  `allow/deny/ask/audit-only`; 008 adapter outcomes
  `deny/simulate/rewriteGuest/proposeCapability`; routes
  `guest-direct/guest-exec/host-broker/fake/deny/portbridge/lab-probe`. Other
  documents must reuse these words instead of coining synonyms.
- local adapter-pack schema, registry, lifecycle state, and profile binding
  vocabulary are owned by the 011 contracts and implemented through
  `hideout.adapter-pack/v1`, `hideout.adapter-pack-registry/v1`, and Manager
  `adapter-pack/*` routes. This is not public marketplace terminology.
- community host-app pack source, revision, binding, access, and lifecycle
  vocabulary is owned by the 032 contracts and
  [host-app-recipes.md](host-app-recipes.md). It is separate from adapter packs:
  v1 host-app recipes carry no JavaScript and can bind only the existing
  `host.app.open-resource` provider. The 032 lifecycle is implemented with
  Gate 0 and external-pack real Gate 2 evidence.
- operator decision center vocabulary is owned by the 012 contracts and typed
  feature providers: actionable decisions (`hostfs.write`, `adapter.proposal`,
  `evidence.share`, `host-app.open-resource`)
  are not informational notices (`privilege.status`, `background.status`);
  claim tokens and provider refs are never public record fields.
- package lifecycle vocabulary is owned by the 013 contracts: package artifact
  manifests describe extracted tarballs; installed-state manifests describe a
  concrete install prefix and are the authority for verify, upgrade, and
  uninstall ownership.

Do not treat similar suffixes as interchangeable. Action names describe
authority requested by a policy decision. Backend capability flags describe what
a backend can implement or verify.
