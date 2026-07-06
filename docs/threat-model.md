# Hideout Threat Model Lite

<!-- markdownlint-disable MD013 -->

## Contract

This document is the Phase 1 threat model entrypoint for Hideout. It is a
Lite threat model, not a formal proof. It defines the security vocabulary,
trusted computing base, claims, non-claims, user-authoritative HostFS grant
model, and PortBridge invariants required before new host reach-back
capabilities are promoted.

[privacy-run-design.md](privacy-run-design.md) remains the Phase 1 product
contract. If this document conflicts with that design, the design wins until
both documents are intentionally revised.

## Scope

Hideout confines untrusted developer tools, AI agents first, while allowing
typed, audited, revocable reach-back to the host. The product replaces ambient
host authority with brokered capability.

This document covers:

- host identity and credentials;
- host files outside the workspace;
- host browser and opener authority;
- hidden proxy and setup secrets;
- HostFS, Host Broker, OpenTarget, and future Endpoint Exposure authority;
- audit evidence required to explain what happened.

This document does not cover:

- a complete remote attacker model;
- kernel or hypervisor compromise;
- malicious host administrators;
- formal cryptographic verification of bundles or scripts;
- Windows backend claims.

## Trusted Computing Base

Hideout's TCB is intentionally small. Components in the TCB may enforce policy,
hold secrets, or create host-side authority.

TCB:

- `hideout` binary and Manager Core;
- Profile, Environment, Session, Policy, Audit, HostFS, Network, and Broker
  packages inside the Hideout binary;
- host broker process and per-run broker token handling;
- host-side opener implementations selected by Hideout;
- HostFS host service and guest FUSE/shim protocol when HostFS is enabled;
- backend adapter code that prepares the sandbox boundary;
- the selected backend runtime, such as Lima, for its isolation claims;
- `tun2socks` and route setup helpers when network privacy is enabled.

Not in TCB:

- target commands and their dependencies;
- policy bundles, recipes, and scripts;
- Goja script code;
- CLI, TUI, or WebUI presentation code;
- workspace contents;
- guest base image contents, including images referenced by shared artifacts;
- browser pages opened by the target;
- remote package registries, npm scripts, or project build tools.

Scripts and bundles may classify intent and propose policy decisions. They must
not execute host authority directly. New authority must be represented as a Core
primitive, validated by Go code, audited, and then executed by a TCB component.

The per-run broker token is a bearer credential for the guest-side shim and
HostFS daemon. It is not treated as secret from A3 guest root. The security
boundary is the host-side broker validator, HostFS policy, explicit user
grants, and audited TCB execution path, not the assumption that guest root
cannot read the token.

The `hideoutd` daemon is part of the local management TCB when enabled. Its
transport must be unreachable from backend guests by construction. A Unix
socket must live under a runtime subdirectory of the effective Hideout store
root with private ancestors, not under workspace, HostFS grants, passthrough
mounts, or any guest-visible path. This reuses the existing store-reserved
HostFS guard and workspace mount safety guard rather than creating a separate
guest-unreachability mechanism.
Host loopback is not a trust boundary for daemon authority: loopback HTTP is
acceptable only as short-lived browser UI transport with explicit client
tokens, not as an unauthenticated daemon API.

The daemon serves one operator on one machine. Clients authenticate with an
operator token (full access) or an optional read-only token; there are no
per-operation role matrices, delegated approval channels, or per-subscriber
redaction tiers. OS peer credentials are not sufficient by themselves for
weak native-backend targets that share the host UID with operator clients,
which is why a token is required at all. Approval is the operator confirming
interactively, recorded in audit against the Manager-computed canonical
request. After restart, the daemon must fail closed for live resources it
cannot prove still belong to an active session.

## Assets

Hideout protects the following assets by default:

- host identity: username, hostname, home path, git global identity, machine
  identifiers, timezone, locale, and similar fingerprinting surfaces;
- host credentials: SSH keys, cloud credentials, API tokens, cookies, browser
  profiles, keychains, proxy credentials, package manager tokens, and signing
  material;
- host files outside the workspace;
- hidden setup secrets used by Hideout but not exposed to the target env;
- browser automation endpoints and debugging ports;
- HostFS backing paths and grant implementation details;
- broker tokens, broker endpoints, proxy bootstrap files, and route secrets;
- audit integrity and run/session identity.

The workspace is intentionally shared unless a later workspace-filtering
feature is enabled. Project-local secrets inside the workspace are not protected
by the Phase 1 default workspace model.

When the shared default environment ships, sessions from different workspaces
share one guest kernel: cross-workspace isolation inside that environment is
session mount-namespace level, which is weaker than the VM boundary between
environments. Guest-root target code (A3) in a shared environment may reach
other attached workspaces. Operators who need the VM-level wall between
projects create a dedicated named environment.

## Adversaries

Phase 1 designs against:

- A1: curious target code that probes environment variables, host paths, git
  config, network settings, machine identity, or browser state;
- A2: malicious target code running as the target user inside the guest;
- A3: malicious target code that gains guest root inside the sandbox;
- A4: malicious policy bundle or script attempting to request or hide
  authority, including a shared artifact that declares a malicious guest base
  image — the image's worst case degrades to A2/A3 (code inside the boundary),
  which is why declarative image references need no host trust review;
- A5: malicious workspace content that influences tools running inside the
  guest.

Phase 1 does not claim protection against:

- A6: host root or a compromised host OS;
- A7: backend runtime escape or kernel/hypervisor compromise;
- A8: a user intentionally granting broad host access;
- A9: exfiltration through an allowed network route after data is already
  visible to the target.

Guest root must not be able to read host credentials merely because it is root
inside the guest. It may, however, control anything intentionally mounted into
or created inside that guest boundary.

Workspace mounts and HostFS grants have different escape models. A workspace
mount is a user-selected subtree exposed through the guest namespace and mount
boundary. HostFS is a host-canonical portal: the host broker must resolve host
symlinks, re-check the resolved path against grants and deny rules, and audit
the operation. These mechanisms must not be collapsed into a single generic file
provider unless the implementation preserves both escape models explicitly.

The HostFS reserved-store guard does not protect paths that enter through the
workspace mount. Phase 1 therefore treats workspace safety as a separate mount
invariant: before backend prepare, Hideout canonicalizes the workspace and
rejects host home, the effective Hideout store root, credential roots, browser
profile roots, and parent directories that would mount those roots into the
guest. A normal project directory under the home directory remains allowed when
it does not contain those protected roots.

## Claims

Hideout Phase 1 makes these claims when using an isolation-capable backend and
the default policy profile:

- A1-A3: target code does not receive the real host home, host identity files,
  or host proxy credentials through ambient environment variables.
- A1-A3: target code cannot read host files outside the workspace unless an
  explicit HostFS grant allows the requested operation.
- A1-A3: HostFS denied, absent, and unsupported operations fail closed and are
  recorded in audit. The target-visible HostFS response does not reveal whether
  a denied path exists; audit records the policy reason for the human operator.
- A1-A3: `host.open` can open allowed external URLs and workspace files through
  the Host Broker, but it is not generic host command execution.
- A1-A3: default profile policy denies `host.open` localhost, loopback, private
  network, link-local, multicast, unspecified URL targets, and known host
  gateway aliases. A profile owner may explicitly opt into local or private
  network URL opens.
- A1-A3: browser control channels, remote debugging ports, and real browser
  profiles are not exposed by `host.open`.
- A1-A3: hidden proxy setup may affect system networking for the run without
  placing proxy secrets in the target environment.
- A1-A3: per-run authority is session-scoped. Broker tokens, shim directories,
  network bootstrap files, proxy secret files, HostFS materialization,
  OpenTarget lifetimes, and PortBridge-backed endpoint transport lifetimes do
  not belong to reusable environments.
- A4: policy bundles and scripts may classify intent and propose decisions, but
  they cannot execute host authority or bypass Go-side validators for Core
  primitives.
- A1-A4: Boundary Summary is derived from structured audit facts and must not
  include HostFS backing secrets, broker tokens, proxy secrets, browser
  automation secrets, endpoint secrets, or full sensitive target paths.

Claims that reference audit evidence assume the default audit-enabled profile
state. Disabling JSONL audit (`audit.enabled=false` or `--audit off`) removes
the persistent evidence trail for that run without disabling policy
enforcement, broker validation, or cleanup; the Audit and Explain contract in
[privacy-run-design.md](privacy-run-design.md) owns that behavior.

## Non-Claims

Hideout Phase 1 does not claim:

- workspace secrets are hidden from the target by default;
- network exfiltration is impossible in `direct` mode;
- DNS leak protection in `tun2socks` mode. Route verification proves the
  default-route swap and the proxy endpoint bypass, but backend-specific DNS
  verification has not shipped: a guest resolver on a connected backend subnet
  can bypass the TUN default route. The DNS policy and its release gates are
  owned by [network-privacy-architecture.md](network-privacy-architecture.md);
- HostFS write overlay is product-ready;
- Endpoint Exposure, Browser Control, Preview Open, adb, simulator, or IDE
  integrations are product-ready only when separately promoted;
- identifying user/application secrets embedded in runtime data. Redaction is
  deterministic over Hideout-minted control-plane credentials; local audit is
  full-fidelity host-local evidence for user data;
- policy scripts are trusted code;
- OS-level isolation for the native backend. Native is only a weak development
  harness and is not dogfood or release evidence for backend isolation;
- opening an allowed external URL prevents the remote website from observing the
  host browser's normal network and browser behavior.

When privacy and compatibility conflict, Hideout fails closed and records the
denied or unsupported attempt.

## HostFS Grant Authority

Hideout does not decide which user-owned files are too sensitive for the user to
share. Host files outside the workspace are hidden by default, but the user is
the authority that decides which paths become visible through HostFS grants.

The following classes are sensitive, but still user-controlled:

- SSH keys and agent sockets;
- browser profiles, cookies, login databases, extension state, and browser
  debugging endpoints on macOS and Linux;
- OS keychains, credential stores, and secret stores;
- cloud provider credentials and package manager tokens such as `.npmrc`,
  `.pypirc`, `.netrc`, Maven settings, Gradle properties, pip config, Cargo
  credentials, and RubyGems credentials;
- signing keys, Android debug keys, iOS certificates, provisioning profiles, and
  app store API keys such as `.android`, `.keystore`, `.jks`, `.p12`, `.pfx`,
  `.p8`, `.mobileprovision`, and `.provisionprofile`;
- Docker, container, VM, and orchestration sockets or admin APIs.

These paths are not exposed by default. If the user explicitly grants one of
them, Hideout should treat that as an intentional capability decision, record it
in policy and audit, and preserve the same redaction rules as any other
sensitive path. This keeps the product user-authoritative instead of
paternalistic.

Built-in non-overridable denies must be limited to Hideout's own control-plane
integrity, and they must be store-aware rather than broad path-name guesses.
Examples include active broker endpoints, proxy secret materialization, and
session runtime files created by Hideout. Hideout broker tokens, proxy secret
files, HostFS backing material, and internal session runtime files are
control-plane assets, not user file categories. A future implementation that
adds reserved roots must document the exact invariant it protects and add tests
that prove the reserved root cannot be used to bypass Broker, Policy, or Audit.

## Loopback Boundary

Host loopback and guest loopback are different trust domains.

Guest loopback (`127.0.0.1` inside the guest) belongs to processes inside the
sandbox. Host loopback (`127.0.0.1` on the host) may expose host developer
servers, browser debugging ports, databases, admin consoles, mobile tooling, or
other privileged services.

Therefore:

- default `host.open` profile policy denies localhost, loopback, private network
  URL targets, and known host gateway aliases such as `host.docker.internal`,
  `host.lima.internal`, and `host.containers.internal`;
- `preview.open` must not be implemented as a localhost/private-network
  exception inside `host.open`;
- a host browser preview of a guest service must be represented by an
  independent typed OpenTarget, such as `preview.open`, with an owned
  PortBridge endpoint created by Hideout;
- the mapped endpoint is trusted only because Hideout created it, owns its
  lifetime, audits it, and cleans it up, not because loopback is generally safe.
- backends must disable or override default automatic host-to-guest exposure of
  guest listeners. A guest-local listener becoming host-visible because of
  backend defaults is an unowned PortBridge and violates this boundary.

This distinction prevents product features from eroding the `host.open` browser
privacy boundary one compatibility exception at a time.

Implementation implication: a `preview.open` mapped endpoint may itself be a
host loopback URL such as `127.0.0.1:<port>`. If it were passed through the
`host.open` URL evaluator, it would be denied by design. Therefore preview
authorization must come from the owning OpenTarget and the PortBridge record,
not from a localhost exception in `host.open`.

Redirect implication: `host.open` evaluates the URL requested by the target
before launching the host browser. It does not claim to sandbox every subsequent
browser navigation. An allowed external URL can redirect the host browser to a
host-local URL such as `http://localhost:<port>`, and that request belongs to
the host browser and host loopback, not to guest loopback. This behavior must
not be used as a guest callback mechanism. CLI login flows that need no
PortBridge must use browser plus paste-code, profile-home seeding, or another
explicit user-controlled mechanism. A browser callback into a guest-local
listener requires a typed owner and PortBridge-backed product path such as a
future `preview.open`.

## PortBridge Invariants

PortBridge is a generic transport primitive. It is not an adb, browser, preview,
or IDE feature by itself.

Before a PortBridge direction is promoted to a product path, it must satisfy:

- explicit owning capability, such as `preview.open` or a future adapter-defined
  OpenTarget;
- structured endpoint model: direction, listen scope, target scope, owner,
  lifetime, and endpoint category are data, not opaque strings;
- no raw host port exposure without an owning typed capability;
- backend-default port forwarding must be disabled or explicitly ignored before
  product code runs;
- endpoint observation must not itself create reachability. Observed endpoints
  are evidence and candidates only; exposure is a separate transaction with an
  active owner, policy decision, validator, audit, and cleanup;
- no bridge to broker tokens, browser debugging endpoints, VM control sockets,
  or Hideout control-plane endpoints unless an owning typed capability
  explicitly models that authority;
- per-run lifetime by default, with cleanup independent of reusable
  environments;
- reusable backend instance configuration must not persist product
  host-to-guest forwards. A provider such as Lima must use run-owned SSH
  direct-tcpip channels or an equivalent closeable mechanism;
- Lima's generated SSH configuration commonly disables guest sshd host-key
  checking for the host-loopback SSH port. Phase 1 preview accepts this only as
  an explicit loopback-local transport posture equivalent to Lima's own local
  trust boundary. It does not claim protection from a hostile host-side process
  that can hijack the loopback SSH port. Before this bridge carries OAuth
  callback codes, tokens, or other credential-bearing callback traffic, the
  provider must add guest host-key pinning or an equivalent authenticated
  channel;
- audit records for creation, use when observable, denial, error, and cleanup;
- Boundary Summary entries that expose counts and endpoint category, not secret
  endpoint values;
- fail-closed validation in Go before any script or bundle can depend on the
  primitive.

`endpoint.expose.host-to-guest` is the first direction to productize because it
supports host browser preview of a guest dev server and local callback flows.
This direction exposes a guest-side service to host loopback. It must still have
an owner, lifetime, audit, cleanup, and backend-specific provider, but it does
not grant the guest reachability to host services.
The Phase 1 product scope is narrower than the full direction: profile-declared
or run-scoped manual candidates owned by `preview.open`.

`endpoint.expose.guest-to-host` has a larger authority surface because it gives
guest code reachability to a host-side service. It needs separate product design
before use by browser automation, adb, IDE, database, simulator, or similar
adapters. It must not inherit the host-to-guest validator or risk posture.

Endpoint observation and endpoint exposure have separate trust models. An
observed guest listener can be created by malicious guest code with perfect
freshness and process ownership. Those properties may support denial or user
explanation, but they are not sufficient evidence for automatic exposure.
Observed-only candidates default to audit-only or ask, and must fail closed when
no prompt channel exists.

Endpoint observation is Later work and expands the trusted observation surface.
The observer, guest socket enumeration mechanism, and any Access Sensor
integration become part of the evidence path. Observation should record only
candidate metadata needed for policy or explanation, such as endpoint class,
source, process class, and timing. It must not record forwarded bytes, callback
query strings, broker tokens, proxy secrets, or raw backend handles.

## Evidence Requirements

New authority must produce evidence at three layers:

- policy evidence: why the request was allowed, denied, unsupported, or
  audit-only;
- audit evidence: structured JSONL facts with redaction applied before storage;
- user evidence: Boundary Summary at run end, derived from the same structured
  facts rather than recomputing behavior.

Boundary Summary is intentionally lossy. It should answer "what classes of host
authority were touched?" without disclosing sensitive target paths, broker
tokens, proxy secrets, backing paths, or endpoint secrets.

Redaction in evidence is deterministic, not heuristic. Hideout-minted
control-plane credentials (broker `cap_` / UI `ui_` token values,
`HIDEOUT_SECRET_*` backing names and values adjacent to those names, generated
machine-id, and Core's own control-plane detail field names) never enter audit
or summaries; these namespaces, values, and field names are self-known, so
stripping them is exact. Raw proxy URLs stay out of evidence as a flow
obligation: the Hideout-managed proxy secret flow must not emit them, and the
redactor strips the `HIDEOUT_SECRET_*`-labeled forms in which they can appear
— it does not scan for arbitrary proxy-shaped strings, because a bare
`socks5://...` value is indistinguishable from a user URL. User/application
request data is recorded verbatim in host-local audit because Core cannot
reliably identify user secrets, and false redaction breaks both policy and
evidence fidelity. One deliberate exception to "never enter script context":
`command.decide` scripts receive the canonicalized request `target` and `argv`
raw, because the requesting guest already possesses them and real policy needs
them; if a guest embeds its own token there, the script sees it, but audit
still strips control-plane token shapes. Audit that leaves the machine passes
the export boundary: deterministic control-plane stripping plus user-owned
redaction.

## Phase 1 Status

Required:

- HostFS read/list/stat policy and audit;
- `host.open` external URL and workspace file broker path;
- run-scoped `portbridge.host-to-guest` transport validation, audit, cleanup,
  Boundary Summary evidence, and fail-closed behavior when a backend provider is
  missing;
- product `endpoint.expose.host-to-guest` over profile-declared and run-scoped
  manual candidates, with active OpenTarget owner validation, backend provider
  fail-closed behavior, audit, cleanup, and Boundary Summary evidence;
- minimal `preview.open` over the product host-to-guest exposure path, without
  changing `host.open` localhost/private-network denial;
- hidden env and proxy-secret handling;
- Boundary Summary for HostFS, `host.open`, implemented PortBridge transport
  events, product Endpoint Exposure events, and `preview.open`;
- this Threat Model Lite.

Design-ready or later:

- HostFS write overlay;
- interactive approval;
- endpoint observation;
- project-declared endpoint candidates and workspace trust review;
- direct JS endpoint exposure proposal entrypoints and richer candidate
  snapshots for adapter scripts;
- OAuth/local callback automation, gated on authenticated Lima SSH channel
  pinning or equivalent protection before callback credentials traverse the
  bridge;
- Browser Control;
- adb, simulator, and IDE adapters;
- additional endpoint exposure directions and provider promotion beyond
  host-to-guest;
- marketplace trust machinery — signing, revocation/kill-switch, publisher
  identity, and namespace protection are day-1 requirements when a public
  marketplace launches, and are not designed ahead of that launch.
