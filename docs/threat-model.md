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

008 command capability adapters are constrained scripts bound to explicit
profile command symbols. They can deny, simulate, rewrite non-privileged guest
commands, or propose non-applied Core capabilities after Go validation. They do
not execute authority and are not a root boundary. The built-in root-sensitive
adapter records command-name intent and attaches the current 009 privilege
status; `enforced` means target command execution is non-root/no-sudo with a
separate setup identity, while `degraded` and `unknown` remain audit/risk states.

The per-run broker token is a bearer credential for the guest-side shim and
HostFS daemon. It is not treated as secret from A3 guest root. The security
boundary is the host-side broker validator, HostFS policy, explicit user
grants, and audited TCB execution path, not the assumption that guest root
cannot read the token.

The `hideoutd` daemon is part of the local management TCB (implemented; see
[STATUS.md](STATUS.md)). Its transport is unreachable from real backend guests by
construction; for a weak native target that shares the operator UID, placement is
not a boundary and the operator token is the sole defense. A Unix
socket must live under a runtime subdirectory of the effective Hideout store
root with private ancestors, not under workspace, HostFS grants, passthrough
mounts, or any guest-visible path. This reuses the existing store-reserved
HostFS guard and workspace mount safety guard rather than creating a separate
guest-unreachability mechanism.
Host loopback is not a trust boundary for daemon authority: loopback HTTP is
acceptable only as short-lived browser UI transport with explicit client
tokens, not as an unauthenticated daemon API.

The daemon serves one operator on one machine. Clients authenticate with an
operator token (full access); read-only tokens, per-operation role matrices,
delegated approval channels, and per-subscriber redaction tiers are not
implemented. OS peer credentials are not sufficient by themselves for weak
native-backend targets that share the host UID with operator clients, which is
why a token is required at all. Confirmation-required daemon operations fail
closed until an explicit prompt channel exists. After restart, the daemon must
fail closed for live resources it cannot prove are owned by the current daemon
instance.

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

Concurrent runs for the same pinned workspace share one guest kernel and the
writable workspace. For ordinary non-root target processes, each run has a
separate mount namespace, PID namespace, private `/proc`, runtime child,
broker/data plane, and HostFS authority. This prevents one ordinary target
from reading or signaling sibling control state, but does not filter effects
through the shared workspace and does not contain guest root. Releasing the
last current-incarnation pin starts a bounded grace only after provider drains
complete. Hideout then rechecks owner locks and observed backend identity before
non-destructively stopping that exact Lima instance. Unknown inventory,
orphaned ownership, failed cleanup, or a changed boot identity blocks automatic
stop. This lifecycle mechanism does not strengthen the ordinary-target or
guest-root isolation boundary.

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
  recorded in audit. Outside every explicit discover domain, and for
  force-hidden paths, target-visible lookup remains `ENOENT` and does not
  distinguish denial from absence. Inside an explicit `see*` domain, a visible
  locked node intentionally reveals its name and coarse kind and returns
  `EACCES` for content access; a separate content grant or live exact-file
  approval is required before read succeeds. Every effective discover policy,
  including manually authored broad rules, receives the categorized
  credential/browser/key-root exclusions from Core. A separate exact content
  grant remains usable but cannot make a discover-denied name reappear in a
  parent listing.
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
- A4: command adapters may classify command-name intent and return strict
  outcomes, but Go validates the adapter artifact digest, declared command
  ownership, proposed capability, output schema, and redaction before any broker
  response is accepted.
- A1-A4: Boundary Summary is derived from structured audit facts and must not
  include HostFS backing secrets, broker tokens, proxy secrets, browser
  automation secrets, endpoint secrets, or full sensitive target paths.
- A1-A4: evidence that leaves the local trust zone through
  `hideout audit export` is mediated by the export/share boundary. The boundary
  reasserts deterministic control-plane stripping, resolves referenced local
  evidence inline or refuses, applies the operator's configured `audit.redact`
  policy to selected user/application data, and fails closed without a partial
  artifact when a required decision or redaction stage is missing.

Claims that reference audit evidence assume the default audit-enabled profile
state. Disabling JSONL audit (`audit.enabled=false` or `--audit off`) removes
the persistent evidence trail for that run without disabling policy
enforcement, broker validation, or cleanup; the Audit and Explain contract in
[privacy-run-design.md](privacy-run-design.md) owns that behavior.

## Non-Claims

Hideout Phase 1 does not claim:

- workspace secrets are hidden from the target by default;
- command adapters intercept absolute paths, direct syscalls, setuid binaries,
  or guest-root behavior outside command-name routing;
- 008 root-sensitive intent capture does not block root escalation by itself, and
  009 enforced privilege separation still does not protect after guest root is
  obtained;
- network exfiltration is impossible in `direct` mode;
- protection against a target that has gained guest root: adversary A3 can
  rewrite the guest routing table or resolver configuration to restore a DNS
  bypass, and constraining the target's in-guest network privileges is out of
  scope. (The connected-subnet DNS leak for non-root targets IS closed and
  validated on real Lima — privacy mode blocks the connected-subnet resolver
  routes, points the guest resolver at a guest-local DoH stub that forwards each
  query as DoH/HTTPS to the declared mediated resolver over the privacy path, and
  refuses a connected-subnet-only environment (fail closed), with Gate 3 proving
  it end to end; that closure is a claim owned by
  [network-privacy-architecture.md](network-privacy-architecture.md), not a
  non-claim);
- HostFS write overlay blocks normal workspace writes, provides broad DLP, or
  is the only possible host mutation path when 009 privilege status is
  degraded/unknown;
- the operator decision center provides remote approval, organization roles,
  delegated policy, compliance workflow, or daemon-implied approval. It is a
  local authenticated queue; missing approval, stale claims, provider absence,
  and timeouts fail closed, while notices remain informational only;
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

HostFS therefore has three target-visible states rather than one global
denied/absent claim:

- outside-domain or force-hidden paths are reported as absent;
- explicit discover grants expose names and coarse kinds but keep content
  locked;
- operation-specific grants expose only the operation they authorize.

Name discovery is a real disclosure. Hiding a predictable path such as `.ssh`
does not prove that the target learned nothing: prior knowledge can make the
absence itself informative. Discover never follows or reveals symlink targets,
and it does not contain a guest-root target, filter workspace contents, or
prevent names already shown to a model from leaving its context.

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

## Host Capability Projection

Host Capability Projection (030) lets a guest command that does not exist in the
guest (`code .`) be transformed into a typed intent and executed on the host as a
Core-owned capability. It adds no ambient host authority: every host effect is a
typed, audited, fail-closed Go provider; untrusted grammars/adapters only propose
an intent that Go re-validates; host identity (app id → binary, argv template)
comes only from a Core/package-owned recipe; there is no generic fallback to host
execution or a shadowed guest binary.

Claim:

- A2-A3: a projected command carries no new host authority. The host absolute
  path, host username, decision/claim tokens, and raw guest argv never cross to
  the guest, the grammar/adapter, the intent, the projection event, or exported
  evidence. Only Core resolves the host path from the session-bound workspace
  mapping.
- A2-A3: the default IDE open mode is safe: an isolated editor profile with
  extensions disabled and Workspace Trust left enabled, so a guest-authored
  workspace auto-task does not run on open. `trusted-host-ide` requires an
  explicit, revocable operator grant held in guest-unreachable control-plane
  state.

Workspace username/path privacy (alias mode): for new privacy and hardened Lima
profiles using alias mode, Hideout does not synthesize the host username or host
home path into the target's default workspace path, identity environment,
generated Git identity/config, or verified guest-visible mount metadata. This
claim requires real-backend proof across all three channels (identity
environment, workspace namespace, mount metadata) with a per-channel detector
self-test and a preserve-mode positive control.

Real macOS arm64 Lima Gate 2 and Gate 3 validated these scoped claims on
2026-07-11 at commit `644e6b53daaa`; the worktree was dirty, so the receipt is
real-backend engineering evidence rather than clean release provenance. See
`docs/host-capability-projection.md` for the bounded proof ids and digests.

Required non-claims:

- Hideout does not inspect or remove usernames, host paths, or other identity
  data that the operator, project, dependencies, build artifacts, or tools place
  in workspace content or command output. Alias mode also does not preserve
  general absolute-path identity between guest and host.
- Hideout does not protect the host editor from a malicious workspace; Workspace
  Trust remains the editor's mechanism. Hideout disarms the obvious
  auto-execution vectors by default and records that a guest-writable workspace
  was opened in a host application, but a guest that already has host authority
  through another path is out of scope.
- The username/path privacy claim MUST NOT be shortened to a universal "Hideout
  hides your identity"; it removes known synthesized channels, it is not content
  data-loss prevention or behavioral anonymity.

### Community Host-App Packs (032)

032 expands the package supply-chain and app-selection attack surface around
the existing provider. The controls below are implemented and covered by 032
Gate 0 plus external-pack real macOS arm64 Lima Gate 2 evidence. The current
receipt is dirty private-alpha evidence and does not establish clean release
provenance or marketplace review.

Threats and required controls:

- **Mutable or hostile source**: local and Git intake may change during review,
  contain escaping links or special files, or trigger Git/package hooks. Core
  must copy a bounded regular-file snapshot, isolate exact-commit Git intake,
  compare the apply digest to the plan, and never read the mutable source at
  runtime.
- **Package self-attestation**: a pack may claim a Team ID, bundle ID, signing
  requirement, safe mode, or successful tests. Those fields may only narrow
  Core observations. They cannot authenticate an app, turn a self-signed bundle
  into verified identity, define `safe`, or certify security.
- **Guest-writable app replacement**: a bundle or helper may live under a
  writable ancestor, escape its bundle, overlap workspace/HostFS/temp/store
  state, or change after review. Core must restrict application roots, verify
  ownership and containment, observe signing or exact unsigned content identity,
  and repeat the checks immediately before launch.
- **Safe-floor bypass**: launch flags and settings may have equivalent unsafe
  effects. Only an identity-compatible, named, versioned Core safety profile may
  label the combined final effect `safe`. Otherwise an otherwise valid binding
  uses exact `ask-each-run`; an unsigned app always remains `unverified-app`.
- **Cross-binding selection**: guest intent or package data may try to select a
  different app, binding, capability, result, resource kind, raw argv, or host
  path. The run registration must bind those facts immutably, strict decoding
  must reject overrides, and Core must derive app and provider identity.
- **Command capture and fallback**: a pack may collide with reserved or existing
  commands, then rely on installation order or fallback. Core must require
  explicit owner replacement for allowed conflicts and deny every invalid,
  absent, drifted, disabled, revoked, or unowned projection before host execution
  or a shadowed guest binary.
- **HostFS authority fixation**: an app decision may outlive or widen a portal.
  Core must require active same-session content/tree authority and reauthorize
  immediately before launch. Discover-only `see*` visibility is not open
  authority; HostFS revoke, expiry, retarget, or session end wins.
- **Session mutation**: enablement may try to change an already-running guest.
  Enable applies only to future run compilation. There is no hot shim injection
  or silent recreate; disable/revoke still wins when an old shim makes a new
  request.

Required non-claims:

- Community packs add no JavaScript, shell, hook, raw argv, dynamic provider,
  generic host exec, host result stream, or profile mutation authority.
- `safe` reduces known launch effects; it does not make the app, its extensions,
  the workspace, or opened content trustworthy.
- Explicit unsigned-app trust is operator acceptance of one exact unverified
  digest, not signing verification.
- V1 local/exact-commit trust is not marketplace review, publisher identity,
  namespace ownership, package signing, notarization, or remote revocation.
- Native, local-only, embedded, static-source, and package-self-test evidence
  cannot establish the required real Lima external-pack host effect.

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

The alpha support matrix in `docs/support-matrix.md` is the release-facing
index of these claims and non-claims. It does not add new authority; it records
which claims require real Gate 2/Gate 3 evidence and preserves non-claims such
as guest root containment, workspace write blocking/DLP, native backend
isolation, browser security, public marketplace trust, and unsupported
platforms.

## Phase 1 Status

Required:

- HostFS read/list/stat policy and audit;
- HostFS write overlay for explicit overlay grants: staged guest write-class
  operations, local claimed Manager apply, conflict fail-closed behavior, and
  redacted audit/export evidence;
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

- richer approval UX beyond the current local decision center;
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

## Supported Runtime Claim Boundary

A selected runtime image is guest supply-chain input, not host authority. Core
accepts only a package-catalog revision with a credential-free retained HTTPS
location and exact SHA-256, stores immutable provenance, and observes a bounded
contract as the non-root target. Runtime selection does not add HostFS,
network, endpoint, host-application, command-proxy, script, or guest-root
grants. Runtime artifacts, receipts, logs, and public evidence must contain no
Hideout-minted credential or preauthenticated agent state.

`preview-ready` is only a current real-Lima observation for the exact running
environment. It is not a claim that the image is supported, patched, free of
all vulnerable packages, reproducible, suitable for arbitrary architectures,
or release-ready. Custom images and legacy environments remain explicitly
unverified. The packaged catalog currently contains one retained,
digest-pinned macOS arm64 preview artifact. Catalog presence alone creates no
readiness, maintenance, patch, SBOM, or release claim; those depend on current
verification and the separately defined evidence gates.
