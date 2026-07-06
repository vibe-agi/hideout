# Backend Capability Matrix

<!-- markdownlint-disable MD013 -->

## Contract

Backend is the execution substrate for Hideout. It is not the product boundary
by itself. Hideout maps backend-specific features into a consistent privacy
runtime contract.

This document follows [architecture-principles.md](architecture-principles.md).

## Backend Principle

Users may choose a backend, but Hideout still treats the backend as a sandbox
substrate with explicit capabilities.

The product must never assume:

```text
container == safe
ssh == unsafe
native == private
```

Instead each backend declares a capability matrix, and each feature checks that
matrix before it runs.

## Backend Classes

### Lima

Primary macOS backend.

Expected role:

- local Linux guest on macOS;
- strong default isolation;
- HostFS through Linux FUSE guest daemon;
- Command Proxy through guest shims;
- good first product path.

### Linux Container

Primary future Linux backend.

Expected role:

- local Linux isolation on Linux hosts;
- should match Lima feature semantics where possible;
- may use container runtime, namespaces, or a dedicated sandbox runner.

### Native

Weak development harness.

Expected role:

- fast local tests;
- debugging;
- no strong privacy claim.

Native is not a product privacy backend and must not be counted as dogfood or
release evidence for isolation, filesystem, network, mount, HostFS, or guest
lifecycle behavior. Native must always declare weak isolation.

## Capability Matrix

Legend:

```text
required
  Needed for the backend to be a product-supported privacy backend.

optional
  Useful but not required for first support.

lab
  Probe or experimental path only.

later
  Product direction is understood, but not implemented in the current product
  path.

weak
  Development harness behavior only. It must not be counted as isolation
  evidence.

no
  Not supported or not safe for this backend.

tbd
  Requires design before product support.
```

| Capability | Lima | Linux Container | Native |
| --- | --- | --- | --- |
| Workspace read/write | required | required | weak |
| Fake home/config/cache/data | required | required | weak |
| Env hiding | required | required | weak |
| Command Proxy shims | required | required | weak |
| Host Broker | required | required | weak |
| HostFS read-only | required | required | weak |
| HostFS glob/filter list | required | required | weak |
| HostFS overlay | later | later | no |
| In-process policy filesystem server | no | later | no |
| Declarative base image reference (guest-domain artifact) | required | required | no |
| `host.open` | required | required | weak |
| Isolated browser profile | required | required | weak |
| `endpoint.expose.host-to-guest` | required | tbd | weak |
| `endpoint.expose.guest-to-host` | lab | lab | lab |
| `endpoint.observe` | later | later | weak |
| Browser control | lab | tbd | lab |
| Direct network | required | required | weak |
| Tun2socks | required | required | no |
| DNS verification | required | required | no |
| Audit | required | required | required |
| Cleanup | required | required | required |
| Warm environment reuse | required | required | no |
| Guest helper distribution | required | required | no |

Matrix rows are eligibility requirements, not implementation status;
[STATUS.md](STATUS.md) owns delivery state. Known open item: DNS verification is
`required` for Lima but has not shipped, so `tun2socks` currently carries an
explicit DNS non-claim in [threat-model.md](threat-model.md). Closing that row
is release-gate work, not a reason to relax the requirement.

Guest helper distribution covers Hideout's own helper binaries, such as the
guest shim and `hideout-hostfsd`; it is not a tool installation channel. The
in-process policy filesystem server row is a Later virtiofs-grade performance
path for the HostFS grant channel: Apple's virtiofs server in Lima's vz stack
is closed to Hideout, a Linux container backend can host such a server
naturally, and a QEMU-based backend could possibly support one.

## Backend Selection Rules

Current Phase 1 `--backend auto` resolves the same way as `hideout run`: Lima on
supported macOS hosts. Future backend selection may choose the strongest
available product backend for each platform only after that backend has matching
isolation evidence and gate coverage.

Initial recommendation:

```text
macOS -> lima
Linux -> linux-container when available, otherwise explicit native weak mode
Windows -> unsupported or later
```

Native must require weak isolation acknowledgement for privacy-sensitive paths.

## Backend Capability API

The current Go backend interface is the Phase 1 contract owned by
[privacy-run-design.md](privacy-run-design.md): `Name()`, `Available()`,
`Prepare()`, `Run()`, and `Cleanup()`.

A future formal backend capability API should extend it toward:

```text
Name()
Probe()
Prepare()
Run()
Stop()
Clean()
Capabilities()
Explain()
Doctor()
```

Capabilities should include:

```text
filesystem.workspace
filesystem.hostfs.read
filesystem.hostfs.overlay
guestImage
commandProxy
hostBroker
hostOpen
portBridge
browserControl
network.direct
network.tun2socks
network.dnsVerify
warmReuse
guestHelperInstall
```

Once the capability API ships, feature code must check capabilities instead of
branching on backend names. Current code still branches on backend names in
places; each such branch is migration debt for the capability API increment,
not license for new name-based product semantics.

## Phase Plan

### Current Product Path

- Lima as primary backend;
- native as weak test/debug backend.

### Next Product Increment

- formal backend capability API;
- Linux container backend design;
- backend capability output in `doctor` and Manager API.

### Later

- other backend candidate decisions;
- Windows backend.

## Other Backend Candidates

SSH, Apple Container, and Docker/devcontainer are candidates, not product
paths, and stay out of the capability matrix until each has a design contract
and gate coverage. SSH splits "host" into a local product host and a remote
execution host, so HostFS sourcing, `host.open` locality, workspace
synchronization, and broker/audit transport must be redesigned before it can be
a backend. Apple Container is considered only if it beats Lima on startup,
maintenance, and capability coverage for key workflows. Docker/devcontainer
would integrate with existing developer environments while HostFS, browser, and
network privacy semantics remain Hideout-owned.

## Open Questions

- Which Linux container substrate should be first?
- What minimum capability set qualifies a backend as "privacy backend"?
