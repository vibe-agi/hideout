# Hideout Docs

<!-- markdownlint-disable MD013 -->

This directory is the design and verification source for Hideout. Start here
when reviewing architecture changes.

## Reading Order

1. [architecture-principles.md](architecture-principles.md) defines the product
   and engineering principles. It is the constitution for new design work.
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
| Ecosystem foundation | [ecosystem-foundation-design.md](ecosystem-foundation-design.md) |
| HostFS overlay | [hostfs-overlay-design.md](hostfs-overlay-design.md) |
| Init tasks | [init-task-architecture.md](init-task-architecture.md) |
| Manager control plane | [manager-control-plane.md](manager-control-plane.md) |
| Network privacy | [network-privacy-architecture.md](network-privacy-architecture.md) |
| OpenTarget and host reach-back | [opentarget-architecture.md](opentarget-architecture.md) |
| Policy/config supply chain | [policy-config-supply-chain.md](policy-config-supply-chain.md) |
| Script adapters | [script-extension-architecture.md](script-extension-architecture.md) |
| TUI/Web UI | [tui-webui-experience.md](tui-webui-experience.md) |

## Authority

- `architecture-principles.md` owns principles.
- `privacy-run-design.md` owns the Phase 1 product contract.
- `threat-model.md` owns security claims and non-claims.
- `STATUS.md` owns current implementation status.
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
  `network.tun2socks`, and `portBridge`.

Do not treat similar suffixes as interchangeable. Action names describe
authority requested by a policy decision. Backend capability flags describe what
a backend can implement or verify.
