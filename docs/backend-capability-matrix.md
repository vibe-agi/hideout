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

### SSH

Remote sandbox backend.

Expected role:

- run target on a remote machine;
- useful for users who want compute or isolation elsewhere;
- must redefine host interactions because "host" from the target's perspective
  is remote, while product host is local.

SSH is important, but it is not equivalent to local containers.

### Apple Container

Research backend.

Expected role:

- possible macOS-native local sandbox;
- considered only if startup, maintenance, and capability coverage are better
  than Lima for key workflows.

### Docker / Devcontainer

Compatibility backend.

Expected role:

- integrate with existing developer environments;
- not define Hideout's core architecture;
- likely useful for workspace execution, but HostFS, browser, and network
  privacy semantics must still be Hideout-owned.

### Native

Weak development backend.

Expected role:

- fast local tests;
- debugging;
- no strong privacy claim.

Native must always declare weak isolation.

## Capability Matrix

Legend:

```text
required
  Needed for the backend to be a product-supported privacy backend.

optional
  Useful but not required for first support.

lab
  Probe or experimental path only.

no
  Not supported or not safe for this backend.

tbd
  Requires design before product support.
```

| Capability | Lima | Linux Container | SSH | Apple Container | Docker/Devcontainer | Native |
| --- | --- | --- | --- | --- | --- | --- |
| Workspace read/write | required | required | required | required | required | yes |
| Fake home/config/cache/data | required | required | required | required | required | weak |
| Env hiding | required | required | required | required | required | weak |
| Command Proxy shims | required | required | tbd | required | tbd | weak |
| Host Broker | required | required | tbd | required | tbd | yes |
| HostFS read-only | required | required | tbd | tbd | tbd | weak/no |
| HostFS glob/filter list | required | required | tbd | tbd | tbd | weak/no |
| HostFS overlay | later | later | later/tbd | later/tbd | later/tbd | no |
| `host.open.url` | required | required | tbd | required | tbd | yes |
| Isolated browser profile | required | required | local-host only | required | tbd | yes |
| `endpoint.expose.host-to-guest` | design-ready / provider tbd | design-ready / provider tbd | tbd | tbd | tbd | lab |
| `endpoint.expose.guest-to-host` | lab / separate design | lab / separate design | tbd | tbd | tbd | lab |
| `endpoint.observe` | later | later | tbd | tbd | tbd | weak/later |
| Browser control | lab -> product | product | tbd | tbd | tbd | lab |
| Direct network | required | required | required | required | required | yes |
| Tun2socks | product target | product target | tbd | tbd | tbd | no |
| DNS verification | required for privacy mode | required for privacy mode | tbd | tbd | tbd | no |
| Audit | required | required | required | required | required | required |
| Cleanup | required | required | required | required | required | required |
| Warm environment reuse | required | required | tbd | tbd | maybe | n/a |
| Guest helper distribution | required | required | required | required | required | n/a |

## Backend Selection Rules

`--backend auto` should choose the strongest available product backend for the
platform.

Initial recommendation:

```text
macOS -> lima
Linux -> linux-container when available, otherwise explicit native weak mode
Windows -> unsupported or later
```

SSH must be explicit:

```text
hideout run --backend ssh://profile-name -- command
```

Native must require weak isolation acknowledgement for privacy-sensitive paths.

## SSH Backend Semantics

SSH introduces a new host split:

```text
Product host
  The user's local machine running Hideout UI/Manager.

Execution host
  The remote machine reached by SSH.

Target guest
  The environment in which the command runs on the execution host.
```

Open questions for SSH:

- Does HostFS expose local product-host files, remote execution-host files, or
  both?
- Does `host.open.url` open local browser or remote browser?
- How are local workspace changes synchronized?
- How are broker tokens transported?
- How are audit logs collected?

Until these are answered, SSH is design target, not a drop-in backend.

## Backend Capability API

Each backend should expose:

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

Feature code must check capabilities instead of branching on backend names when
possible.

## Phase Plan

### Current Product Path

- Lima as primary backend;
- native as weak test/debug backend.

### Next Product Increment

- formal backend capability API;
- Linux container backend design;
- SSH semantics document;
- backend capability output in `doctor` and Manager API.

### Later

- Apple container decision;
- Docker/devcontainer compatibility;
- Windows backend.

## Open Questions

- Which Linux container substrate should be first?
- Is SSH a backend or a workspace sync plus remote runner product mode?
- Should Docker support be strict Hideout-managed or devcontainer-compatible?
- What minimum capability set qualifies a backend as "privacy backend"?
