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
5. [privacy-run-test-plan.md](privacy-run-test-plan.md) defines gates and
   release evidence.

Subsystem documents:

| Area | Document |
| --- | --- |
| Backend capability | [backend-capability-matrix.md](backend-capability-matrix.md) |
| Distribution and first run | [distribution-bootstrap.md](distribution-bootstrap.md) |
| Ecosystem foundation | [ecosystem-foundation-design.md](ecosystem-foundation-design.md): canonical resource model, policy composition, Hideoutfile contract, guest base-environment artifact class (declarative base image references; distinct from imperative environment recipes, which remain prohibited), and ecosystem delivery sequence. |
| HostFS overlay | [hostfs-overlay-design.md](hostfs-overlay-design.md) |
| Init tasks | [init-task-architecture.md](init-task-architecture.md) |
| Manager control plane | [manager-control-plane.md](manager-control-plane.md) |
| Network privacy | [network-privacy-architecture.md](network-privacy-architecture.md) |
| OpenTarget and host reach-back | [opentarget-architecture.md](opentarget-architecture.md) |
| Policy/config supply chain | [policy-config-supply-chain.md](policy-config-supply-chain.md): authoring, source resolution, install, update, trust, override, and export behavior for the ecosystem model. |
| Script adapters | [script-extension-architecture.md](script-extension-architecture.md) |
| TUI/Web UI | [tui-webui-experience.md](tui-webui-experience.md) |

## Authority

- `.specify/memory/constitution.md` owns Spec Kit planning gates and summarizes
  the non-negotiable rules for generated specs, plans, and task lists.
- `architecture-principles.md` owns principles.
- `privacy-run-design.md` owns the Phase 1 product contract.
- `threat-model.md` owns security claims and non-claims.
- `STATUS.md` owns current implementation status.
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

Do not treat similar suffixes as interchangeable. Action names describe
authority requested by a policy decision. Backend capability flags describe what
a backend can implement or verify.
