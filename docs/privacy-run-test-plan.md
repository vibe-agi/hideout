# Hideout Phase 1 Test Plan

<!-- markdownlint-disable MD013 -->

## Purpose

This document turns the Phase 1 contract in
[privacy-run-design.md](privacy-run-design.md) into an executable test plan.
The design document remains the authority. If this test plan conflicts with the
design, the design wins and this file must be corrected.

The goal is to prove that `hideout run` is a usable local privacy runner:

- the target command runs in a real backend boundary for product and dogfood
  claims;
- generated identity replaces host identity outside the workspace;
- proxy credentials can be used by Hideout without entering target env;
- registered host escapes go through Command Proxy and Host Broker;
- unsupported or ambiguous authority fails closed;
- audit, explain, doctor, and cleanup make the boundary observable.

## Execution Strategy

Phase 1 testing is intentionally split by cost:

- local edit loop: run the fast gates after normal code changes;
- backend proof: run the Lima and hidden proxy gates when touching backend,
  network, broker, or profile isolation behavior;
- dogfood proof: run the generic agent smoke before relying on an autonomous
  CLI workflow for daily work;
- release candidate: run every required gate, the real external URL browser
  launch path for Gate 4, an operator-supplied proxy for Gate 3, capability
  probe smoke, and the generic CLI dogfood smoke.

The aggregate entrypoint is:

```bash
scripts/test-phase1.sh
```

By default it runs the local edit loop: Gate 0, the native development harness,
and Gate 4 dry-run. This default is for engineering speed; it is not enough to
claim dogfood readiness or product isolation.

Useful modes:

```bash
# Fast local verification.
scripts/test-phase1.sh --quick

# Required automated gates, excluding real browser launch.
scripts/test-phase1.sh --required

# Include only the real Lima backend gate in addition to fast gates.
scripts/test-phase1.sh --lima

# Include only hidden proxy/tun2socks in addition to fast gates.
scripts/test-phase1.sh --proxy

# Release-candidate gates: real browser, operator proxy, and probes.
HIDEOUT_SECRET_DEFAULT_PROXY=socks5://host.lima.internal:<port> \
  scripts/test-phase1.sh --release-candidate

# Same release-candidate proof through the dedicated dogfood entrypoint.
HIDEOUT_SECRET_DEFAULT_PROXY=socks5://host.lima.internal:<port> \
  scripts/test-release-dogfood.sh

# Compatibility shape: all required gates plus real browser external URL launch.
# This is not sufficient for a release candidate because it does not require an
# operator-supplied proxy or capability probe smoke.
scripts/test-phase1.sh --all --real-browser

# Include command-level capability probe smoke after product gates.
scripts/test-phase1.sh --quick --probes

# Include the generic CLI dogfood smoke. This uses a fake test CLI and fake API
# to verify callback login, profile identity persistence, authenticated request
# flow, and control-plane store denial without binding Hideout to any product.
scripts/test-phase1.sh --dogfood-cli

# Optional real CLI operator smoke. The operator supplies the package, command,
# auth env keys, and request command; Hideout still does not encode product
# semantics in Core or automated gates.
HIDEOUT_OPERATOR_NPM_PACKAGE=<npm-spec> \
HIDEOUT_OPERATOR_COMMAND=<command> \
HIDEOUT_OPERATOR_ENV_KEYS=TOKEN_ENV \
HIDEOUT_OPERATOR_REQUEST_COMMAND='<guest command that proves one request>' \
  scripts/test-phase1.sh --operator-cli
```

## Development Test Planning

Every implementation change must choose a test scope before coding. The scope
is based on the authority that changed, not on the file name alone.

Change-to-gate mapping:

| Change area | Minimum local check | Boundary proof | Release-candidate check |
| --- | --- | --- | --- |
| Docs, schemas, and generated examples | Gate 0 | none | Gate 0 |
| Ecosystem foundation, bundle schemas, project manifests, trust, export, or script ABI | Gate 0 and targeted schema tests | Manager plan/apply tests when authority changes | bundle supply-chain gate before public ecosystem release |
| First-run initialization, InitTask, helper discovery, `doctor --fix`, schema metadata repair, or project bootstrap | Gate 0 and targeted InitTask tests | Gate 1 native harness for CLI shape only; Gate 2 when backend preparation changes | Distribution Bootstrap gate |
| CLI parsing, profile, identity, env, audit, Boundary Summary, cleanup, or doctor | Gate 0 and the native harness | affected package tests | `--required` if behavior is externally visible |
| Command Proxy, Host Broker, `host.open`, file open, or browser launcher | Gate 0, native harness, and Gate 4 dry-run | Gate 2 when guest shims or broker transport change | real-browser Gate 4 |
| HostFS Portal, HostPathGrant, guest FUSE daemon, or host filesystem RPC | Gate 0 and targeted HostFS unit tests | Gate 2 on Linux guest backend with read/list grants | HostFS grant gate when promoted |
| Additional passthrough mounts | Gate 0, native harness for CLI shape, and mount contract tests | Gate 2 when backend mount config changes | required if user-facing |
| Lima backend, mounts, guest bootstrap, guest command resolution, tool presets, or instance lifecycle | Gate 0 and Gate 2; native harness only for shared CLI wiring | Gate 2 on macOS with Lima; `--dogfood-cli` when it affects CLI workflows | `--release-candidate` |
| Network setup, proxy secrets, route verification, or `tun2socks` | Gate 0, native harness for shared CLI wiring, and Gate 3 | Gate 3 with auto proxy; Gate 2 if bootstrap changes | Gate 3 strict operator proxy |
| Policy scripts, Goja ABI, or scriptable extension points | Gate 0 and native harness where CLI-visible | relevant denied and allowed path tests | `--required` if a required route is affected |
| Manager core, run API, or Web UI | targeted manager tests and Gate 0 | run plan/apply/status tests and redaction checks when execution authority changes | optional product smoke |
| Endpoint Exposure product actions | Gate 0 and targeted Manager/PortBridge tests for implemented directions | candidate validation, direction-specific exposure validation, lifecycle, audit, cleanup, Boundary Summary, and backend fail-closed tests | `--required` when a new direction or consumer becomes user-facing |
| Browser Control or other lab probes | package tests and `scripts/test-lab-probes.sh` | probe audit evidence only | probe smoke in `--release-candidate` |
| `preview.open` product path | Gate 0 and targeted Manager/PortBridge tests | Gate 2 when backend transport changes; dogfood CLI smoke when callback-style reach-back changes | `--release-candidate` |

The testing order for new capabilities is:

1. Unit contract: validate policy decisions, schema shape, redaction, path
   normalization, and fail-closed cases without starting a backend.
2. Native harness: prove CLI wiring, generated identity, hidden env, audit,
   `explain`, `doctor`, and cleanup behavior with the weak native backend. This
   does not prove isolation, guest filesystem behavior, mount semantics, network
   privacy, HostFS/FUSE behavior, or real backend lifecycle.
3. Backend proof: run the Lima or `tun2socks` gate for any behavior that depends
   on guest boundaries, mounts, bootstrap, proxy routing, or command shims.
4. Capability probe: for experimental host capabilities, add a lab command and
   probe script before promoting the behavior into `hideout run`.
5. Release candidate: run `scripts/test-release-dogfood.sh` or
   `scripts/test-phase1.sh --release-candidate` only after the lower-cost gates
   pass and the operator proxy and browser launcher prerequisites are
   available.

Do not promote a capability from probe to Required Phase 1 because a manual demo
worked once. Promotion requires an updated design classification, an automated
gate or package test, audit evidence, and a fail-closed test for the denied
path.

Definition of done for a Phase 1 change:

- the selected test scope is written in the change notes or PR description;
- new behavior has at least one positive test and one fail-closed or redaction
  test when it touches privacy boundaries;
- `scripts/test-phase1.sh --quick` passes before handing work to another
  developer;
- `--required` or the narrower backend gate is run before merging changes that
  affect Lima, network, broker, command proxy, or identity isolation;
- `scripts/test-phase1.sh --dogfood-cli` passes before claiming a local
  autonomous CLI workflow is dogfood-ready;
- release-candidate evidence records the exact command, host prerequisites, and
  whether Gate 3 used an operator-supplied proxy.
- `scripts/test-release-dogfood.sh` writes a named evidence bundle containing a
  manifest and redacted log; it records proxy presence and scheme, never the
  full proxy URL.

## Test Gates

Phase 1 has five gates. A release candidate must pass all Required gates.

### Gate 0: Static Contract

Purpose: prove schemas, docs, and package tests are internally consistent.

Commands:

```bash
scripts/test-gate0.sh
```

Required evidence:

- all Go packages pass;
- markdown docs lint with zero errors, including every file under `docs/`;
- all JSON schemas parse;
- no Required Phase 1 behavior depends on lab commands, Web UI, or a daemon.
- every architecture document that introduces authority has a design status,
  failure behavior, and either a release gate or an explicit Later status.
- `docs/threat-model.md` defines the Phase 1 Lite TCB, claims, non-claims,
  user-authoritative HostFS grant model, loopback boundary, and PortBridge
  invariants before new PortBridge-backed capabilities are promoted.
- RunResult schema includes Boundary Summary as structured data derived from
  audit facts, not as a CLI-only rendering.

Gate 0 enforces the last item with the phase plan preflight:

```bash
HIDEOUT_PHASE1_PRINT_PLAN=1 scripts/test-phase1.sh --required
```

The required plan must include Gate 0 through Gate 4 and must not include
Capability Probe smoke, lab commands, Web UI, `hideoutd`, or daemon-dependent
behavior. Release-candidate mode may include probe smoke, but required automated
gates must remain independent of probes and optional management surfaces.

Ecosystem contract checks belong in Gate 0 until public bundle support is
promoted. Gate 0 should reject bundle or project schema changes that allow raw
host execution, raw mounts, unrestricted scripts, profile identity export, or
silent authority changes outside Manager plan/apply.

Script extension contract checks belong in Gate 0. Gate 0 should reject script
ABI or SDK changes that expose raw Go standard library packages, host filesystem
handles, network clients, process APIs, environment APIs, backend driver
handles, broker tokens, mutable Manager state, or any capability execution path
outside validated proposals.

InitTask contract checks also belong in Gate 0. Gate 0 should reject first-run
or remediation designs that rely on arbitrary shell, `host.exec`, raw mounts,
raw routes, bundle init scripts, project init scripts, or automatic profile
authority mutation outside Manager plan/apply.

### Gate 1: Native Development Harness

Purpose: fast local smoke for CLI wiring and privacy semantics that do not
require a VM. This gate is a development harness, not product isolation proof
and not dogfood evidence. It uses the explicit weak native backend and must
always declare weak isolation in output.

Scope:

- `hideout init --no-input --backend native --network direct`;
- `profile init`, `profile clone`, `profile path`;
- `run --backend native --allow-weak-isolation`;
- `explain`;
- `doctor`;
- `cleanup`;
- Command Proxy and Host Broker behavior using test openers where possible.

Required checks:

- explicit native init creates store directories, default profile, profile
  identity, schema metadata, and runtime directories under a temporary store;
- repeated init is idempotent and does not rotate identity, enable bundles,
  add HostFS grants, or create new authority;
- `doctor --fix --dry-run` reports safe fixes without applying unsafe actions;
- `pwd` resolves to the configured workspace;
- workspace read/write works;
- `HOME`, `TMPDIR`, XDG vars, git global config, timezone, locale, and synthetic
  identity env are generated values;
- denied env vars and proxy env vars are absent from the target env;
- missing target command reports backend context and does not fall back to a host
  binary outside the selected env;
- `--ephemeral` uses session-local identity and does not mutate persistent
  profile identity;
- `explain` reports workspace path privacy, command proxy registration, audit
  mode, hidden proxy plan, and secret refs without leaking backing secret env
  names or values;
- Manager `ExplainRun` owns explain-only environment selection, session
  creation, and cleanup; CLI must not reassemble that lifecycle directly;
- `doctor` reports core checks and distinguishes invalid profile, missing or bad
  proxy secret, missing Lima tooling, invalid generated Lima YAML, broken mount,
  policy capability failure, policy script failure, audit redaction script
  failure, and unsafe browser launcher configuration;
- cleanup removes tmp, shims, broker endpoint files, network files, proxy secret
  files, and ephemeral identity while preserving audit;
- cleanup dry-run and `--session` filtering keep non-selected session state
  intact while still reporting secret-bearing cleanup state.
- verbose run-end Boundary Summary reports the audit path and HostFS /
  `host.open` / implemented PortBridge or product endpoint exposure allowed,
  denied, unsupported, audit-only, or error counts from the structured audit
  facts;
- product endpoint exposure summary entries include non-secret source class and
  close reason, such as `declared`, `manual`, `observed`, `first-request`,
  `ttl`, `process-exit`, or `session-end`;
- Boundary Summary output does not include broker tokens, proxy secrets, HostFS
  backing secrets, browser automation secrets, or full sensitive requested
  paths.
- when audit is disabled, Boundary Summary reports that no boundary evidence is
  available instead of rendering zero-count capability evidence.

Non-goal:

- Native smoke does not prove OS isolation. It only proves product wiring and
  policy behavior.

Command:

```bash
scripts/test-gate1-native.sh
```

### Gate 2: Lima End-to-End

Purpose: prove the first real macOS isolation backend.

Prerequisites:

- macOS host;
- `limactl` available;
- optional `HIDEOUT_LINUX_SHIM_PATH` points to an executable Linux
  `hideout-shim`; if absent, the gate script builds a temporary Linux shim for
  the current `GOARCH`;
- optional `HIDEOUT_LINUX_HOSTFSD_PATH` points to an executable Linux
  `hideout-hostfsd`; if absent, the gate script builds a temporary Linux
  HostFS daemon for the current `GOARCH`;
- generated Lima YAML validates;
- guest has shell, git, and a minimal HTTP client such as `curl`;
- guest has `python3` for HostFS ordinary API coverage;
- optional Node HostFS API coverage is skipped by the default lightweight gate
  when Node is absent from the guest. Set `HIDEOUT_GATE2_REQUIRE_NODE=1` to make
  the gate prepare `nodejs` inside the Lima guest with the guest package manager
  and then fail if `node` is still unavailable.

The gate script uses a temporary `HIDEOUT_STORE_ROOT` so Hideout profile/session
state is isolated without changing the host `HOME` used by `limactl`.

Command:

```bash
scripts/test-gate2-lima.sh
```

Long-running Lima commands use `HIDEOUT_GATE_TIMEOUT`, defaulting to `15m`.
Increase it for first-time image downloads:

```bash
HIDEOUT_GATE_TIMEOUT=45m scripts/test-gate2-lima.sh
```

Run the full HostFS ordinary API check, including Node `fs.readFileSync`, with:

```bash
HIDEOUT_GATE2_REQUIRE_NODE=1 HIDEOUT_GATE_TIMEOUT=45m scripts/test-gate2-lima.sh
```

Required checks:

- default `hideout init --no-input` and `hideout init --backend lima` validate
  Lima prerequisites, helper discovery, and generated backend metadata without
  starting the VM unless a deep check is explicitly requested;
- initialized helper discovery is reused by the first Lima run without falling
  back to host binaries;
- `hideout run --backend lima -- pwd` reports guest workspace or alias;
- command resolution happens inside Lima;
- missing commands report Lima context and no host fallback;
- workspace read/write survives host/guest mount mapping;
- fake home, git config, XDG dirs, machine-id, hostname, timezone, and locale
  are visible in guest;
- host home and host `~/.ssh` are not readable by default;
- child processes inherit the same env and filesystem boundary;
- HostFS `--fs read:`, `--fs dir:`, and `--fs tree:` grants are readable
  through ordinary guest filesystem APIs: shell `cat`, Python `open()`, and
  Node `fs.readFileSync` when Node is available or required. Ungranted and
  `--no-fs` denied paths are not readable;
- Lima default runs reuse the matching environment instance, while every run
  still refreshes session authority: broker token, shim directory, network plan,
  proxy secret runtime files, and audit context;
- `--ephemeral` and `--rm` delete the runtime instance or environment according
  to their documented lifecycle;
- `doctor` distinguishes missing Lima, invalid YAML, broken mount, invalid
  profile, bad proxy secret, broker failure, and policy script failure.

### Supplemental: Generic Test CLI Dogfood Smoke

Purpose: prove the product mechanics needed by a CLI-style tool
without putting a specific third-party product into Hideout code or tests.

The smoke uses `hideout-test-cli`, a fake test CLI binary, and
`hideout-gate-lab-target`, a fake bearer-token API. It verifies:

- a profile can opt into the generic `node-dev` tool preset and receive
  `node`/`npm` in the Lima guest;
- a profile can declare a generic npm global package and required command names
  without Hideout knowing the tool's business semantics;
- a target can run a local callback listener, complete a callback, and store
  its own authentication state under the isolated profile identity home;
- `preview.open` can expose a declared guest-local callback listener to the
  host browser path, allowing a browser-style callback to complete through a
  typed host-to-guest endpoint exposure;
- `preview.open` waits for the mapped HTTP endpoint to respond before launching
  the host browser path, so target-created preview servers and callback
  listeners can be ready before the browser reaches the mapped endpoint;
- a host-owned redirect to `localhost:<guest-listener-port>` does not complete
  the guest callback, proving host loopback and guest loopback remain separate
  without a typed endpoint exposure owner;
- profile identity home can be seeded through the generic
  `hideout profile home import` primitive without exposing source paths or
  credential contents in smoke output;
- that authentication state persists across reused Lima runs for the same
  profile and workspace;
- the target can make an authenticated HTTP request from inside the guest to a
  host-owned fake API;
- run-scoped `--env KEY=VALUE` can expose a user-chosen variable while Hideout
  runtime env remains hidden from the target;
- HostFS cannot grant the Hideout control-plane store into the guest.

This smoke is not an adapter for any real product. It is a product-mechanism
proof: runtime supply, user-declared tool installation, guest-local callback
flow, typed preview callback reach-back, host redirect boundary, environment
policy, profile-state persistence, network request, and control-plane store
protection.

The smoke runs with `--network direct`. It proves generic tool supply and CLI
mechanics, not proxy-routed first-time provisioning. Changes to tool
provisioning order, setup env filtering, `tun2socks` bootstrap, or proxy secret
handling must also run Gate 3 or an equivalent `tun2socks` provisioning smoke.

The automated smoke sets `HIDEOUT_BROWSER_PATH` to a fake browser shim that
accepts the normal Chromium-style arguments and follows the host-visible URL
with `curl`. This keeps the gate deterministic and avoids opening the
operator's real browser while still exercising the same Hideout opener path.

The smoke has two positive callback paths: a guest-internal self-callback that
proves profile identity persistence without host reach-back, and a
`preview.open` browser-style callback that proves typed host-to-guest endpoint
exposure. The positive preview callback uses a same-origin relative redirect so
it stays inside the Hideout-owned mapped endpoint; OAuth-style absolute
loopback redirect automation is a separate adapter problem, not a
`preview.open` responsibility. The smoke also runs a controlled host redirect
that behaves like
`https://httpbin.org/redirect-to?url=http://localhost:9000`, but without
depending on the public internet: after a host browser or host HTTP client
follows the redirect, `localhost` targets host loopback, not guest loopback. That
negative check must keep failing unless a typed owner, endpoint candidate, and
`endpoint.expose.host-to-guest` request explicitly create a Hideout-owned
mapping.

Command:

```bash
scripts/test-phase1.sh --dogfood-cli
```

### Supplemental: Operator CLI Smoke

Purpose: let maintainers dogfood a real CLI without committing product-specific
logic, package names, API keys, or workflow semantics into Hideout.

The operator-supplied smoke is explicitly opt-in:

```bash
HIDEOUT_OPERATOR_NPM_PACKAGE=<npm-spec> \
HIDEOUT_OPERATOR_COMMAND=<command> \
HIDEOUT_OPERATOR_VERSION_ARGS='--version' \
HIDEOUT_OPERATOR_ENV_KEYS=TOKEN_ENV \
HIDEOUT_OPERATOR_HOME_IMPORTS=$'/host/state.json=.tool/state.json\n/host/state=.tool/state' \
HIDEOUT_OPERATOR_AUTH_COMMAND='<optional guest login command>' \
HIDEOUT_OPERATOR_STATUS_COMMAND='<guest command that proves login state>' \
HIDEOUT_OPERATOR_REQUEST_COMMAND='<guest command that proves one request>' \
  scripts/test-phase1.sh --operator-cli
```

Real-product values must stay outside the repository. Put them in the invoking
shell, a local ignored wrapper, or a secrets manager, then call the generic
operator smoke. Do not add a product-specific smoke script, package name, API
key, account identifier, or prompt fixture to Hideout Core, docs, or default
gates.

When a real CLI requires pre-existing local auth state, the operator may seed
that state with `hideout profile home <profile> import --from <host-path> --to
<relative-profile-home-path>`. This is still operator-controlled test setup, not
a product-specific Hideout default. The import command must not print source
paths or credential contents, and the smoke must prove subsequent status/request
commands read state from the isolated profile identity home.
`HIDEOUT_OPERATOR_HOME_IMPORTS` is a newline-separated list of
`<host-path>=<profile-home-relative-path>` entries. The smoke imports these
entries with `--force` before the first run, so rerunning a local smoke against
the same temporary or operator-selected store remains deterministic.

Required checks:

- the profile is configured through Hideout-managed tool setup (`hideout init`
  or `doctor --fix` tool flags, or lower-level `hideout profile tools`), not by
  editing profile JSON directly;
- `node-dev` and the user-declared npm tool are provisioned into a Lima guest;
- the first run creates a tool-matched reusable environment and the second run
  reuses the same environment;
- operator-selected env keys are passed through `--env` only for the run, while
  Hideout runtime env remains controlled by the normal env policy;
- an optional operator-supplied auth command can run inside the isolated profile
  identity home, using the operator's terminal for login flows such as browser
  plus paste-code;
- an optional operator-supplied status command proves that auth state persisted
  in the reusable environment/profile identity, not in the host home;
- an optional operator-supplied request command succeeds from inside the guest;
- HostFS grants covering the Hideout store are rejected.

This smoke is not part of the default automated gate because it depends on
operator-held credentials or accounts. It is the correct place to verify a real
tool before daily dogfood, while `--dogfood-cli` remains the product-mechanism
gate that can run in CI and release-candidate verification.

### Gate 3: Hidden Proxy

Purpose: prove the user requirement that proxy configuration is effective for
network traffic but not readable by JavaScript or the target process env.

Prerequisites:

- Lima backend;
- guest-side `tun2socks`; optional `HIDEOUT_LINUX_TUN2SOCKS_PATH` points to an
  executable Linux `tun2socks`; if absent, the gate script uses
  `tun2socks-linux-<goarch>`/`tun2socks-linux` from `PATH` or builds a temporary
  Linux `tun2socks` from `github.com/xjasonlyu/tun2socks/v2@v2.6.0` in an
  isolated temporary module;
- a test proxy endpoint; if `HIDEOUT_SECRET_DEFAULT_PROXY` is omitted, the gate
  script starts a temporary host-local SOCKS5 proxy and exposes it to the guest
  as `socks5://host.lima.internal:<port>`;
- `network.proxySecretRef` configured;
- optional `HIDEOUT_LINUX_SHIM_PATH` points to an executable Linux
  `hideout-shim`; if absent, the gate script builds a temporary Linux shim for
  the current `GOARCH`;
- optional `HIDEOUT_SECRET_DEFAULT_PROXY` set to the proxy URL for the
  `default-proxy` secret ref; if omitted, the gate script sets it from the
  temporary SOCKS5 proxy URL;
- target env has no `HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY`, `NO_PROXY`, or
  lowercase equivalents.

The gate script uses a temporary `HIDEOUT_STORE_ROOT` so proxy secret and audit
checks inspect only gate-owned Hideout state.

Command with an auto-started local test proxy:

```bash
scripts/test-gate3-hidden-proxy.sh
```

Command with an externally supplied proxy:

```bash
HIDEOUT_SECRET_DEFAULT_PROXY=socks5://127.0.0.1:1080 \
  scripts/test-gate3-hidden-proxy.sh
```

Release-candidate Gate 3 mode requires the external proxy to be supplied by the
operator and fails before setup if the secret value is missing or not a URL with
a supported scheme and host. Supported schemes are `http`, `https`, `socks5`,
and `socks5h`:

```bash
HIDEOUT_GATE3_REQUIRE_OPERATOR_PROXY=1 \
HIDEOUT_SECRET_DEFAULT_PROXY=socks5://127.0.0.1:1080 \
  scripts/test-gate3-hidden-proxy.sh
```

The aggregate `scripts/test-phase1.sh --release-candidate` and
`scripts/test-release-dogfood.sh` run the same operator proxy preflight before
expensive gates.

Long-running Lima and proxy route checks use `HIDEOUT_GATE_TIMEOUT`, defaulting
to `15m`.

Required checks:

- network init selects mode and stores only non-secret configuration plus
  SecretRef references;
- direct mode init makes `explain` and `doctor` report that network identity is
  visible;
- `hideout init --network tun2socks --proxy-secret <ref>` persists only
  `network.proxySecretRef`, and `tun2socks` init without a proxy secret ref
  fails closed before profile mutation;
- `tun2socks` mode init never writes proxy values into profile, audit, or
  target env;
- init does not change system routes; route setup belongs to per-session
  network bootstrap;
- proxy secret resolves from a secret ref, not from target env;
- `network/proxy.url` is mode `0600`, created exclusively, not written through
  symlinks, omitted from audit, and removed after bootstrap reads it;
- target process cannot read proxy env vars;
- HTTP(S) request egress is routed through `tun2socks`;
- bootstrap fails closed if `tun2socks`, `/dev/net/tun`, route verification, or
  proxy endpoint route protection fails;
- cleanup stops `tun2socks`, restores the prior default route when known, removes
  `hideout0`, and deletes runtime proxy files.

Suggested target command:

```bash
node -e 'console.log({
  http: process.env.HTTP_PROXY,
  https: process.env.HTTPS_PROXY,
  all: process.env.ALL_PROXY,
  no: process.env.NO_PROXY
})'
```

Pass condition: all printed values are absent or empty while an independent
HTTP(S) route check proves traffic uses the configured proxy.

### Gate 4: Host Escape Boundary

Purpose: prove registered host escapes are explicit, typed, audited, and narrow.

Default command:

```bash
scripts/test-gate4-host-escape.sh
```

The default command uses `HIDEOUT_OPEN_DRY_RUN=1` internally so it does not open
a browser during automated boundary checks. To prove a real isolated browser
launch for the external URL case only, run:

```bash
HIDEOUT_GATE4_REAL_BROWSER=1 scripts/test-gate4-host-escape.sh
```

The real-browser mode must still dry-run file opens and denial checks so the gate
does not open arbitrary local files while proving the URL launcher path.
Before the external URL launch, the gate performs a no-side-effect browser
launcher preflight. `HIDEOUT_BROWSER_PATH` must point to a direct
Chromium-compatible browser binary and must not be `open` or `xdg-open`; without
`HIDEOUT_BROWSER_PATH`, macOS app mode must resolve the configured
`HIDEOUT_BROWSER_APP` or the default `Google Chrome`.
The aggregate `scripts/test-phase1.sh --release-candidate` and
`scripts/test-release-dogfood.sh` run this preflight before expensive gates so a
bad browser launcher fails early without opening a browser.

Required checks:

- `open https://example.com` routes through Command Proxy to Host Broker with
  `route=host-broker`;
- the installed `hideout-shim` binary and the built-in `hideout shim` command
  normalize configured `open-target-v1` command symbols the same way; neither
  entrypoint may hard-code only the default `open`/`xdg-open` symbols before the
  broker's session command registry validates the request;
- URL open uses an isolated browser profile and never the real browser profile;
- `open http://127.0.0.1:<port>`, private ranges, CGNAT, benchmarking ranges,
  link-local, multicast, `.local`, `.localhost`, and known host gateway aliases
  fail closed before opener execution;
- redirect isolation after opener execution is covered by the generic dogfood
  CLI smoke: external URLs that later redirect to `localhost` are host browser
  behavior and must not be used as guest callback proof;
- `host.open` does not create a PortBridge, expose DevTools, expose a
  remote-debugging socket, or create a guest-visible browser control channel;
  URL-open audit must record `portBridge=none`, `browserControl=disabled`, and
  `remoteDebugging=not-exposed`;
- workspace file open maps guest paths to host workspace files only;
- file open rejects host paths outside workspace, special files, symlink escapes,
  remote `file://` hosts, query/fragment file URLs, and encoded path separators;
- disabled `open` or `xdg-open` command proxies fail closed even if the shim is
  invoked directly.

## Capability Probe Gates

Capability probes are not product gates for `hideout run`, but they must be
reproducible before a future OpenTarget is promoted.

Command-level smoke for all current lab probes:

```bash
scripts/test-lab-probes.sh
```

This script builds temporary gate helpers, runs the PortBridge and Preview Open
probes against local loopback targets, and runs Browser Control against a fake
DevTools-compatible browser handshake. If `HIDEOUT_BROWSER_PATH` points to a
real Chromium-compatible browser binary, the script also runs a real browser
control handshake. The fake handshake is automation evidence for CLI, policy,
audit, and protocol shape; the real browser run is the stronger capability
evidence before promoting browser-control behavior.

### PortBridge And Endpoint Exposure Checks

Evidence:

- loopback forwarding test proves byte copy, bidirectional cleanup, cancellation,
  and deny paths;
- lab command requires explicit `--enable-lab`;
- probe audit uses lab action names;
- product `endpoint.expose.host-to-guest` uses profile-declared or run-scoped
  manual endpoint candidates, a direction-specific product action, route,
  active owner validation, run-scoped Manager lifecycle, backend provider,
  audit, cleanup, and Boundary Summary;
- JavaScript policy may reference `candidateId` and policy fields, but cannot
  supply raw host addresses, guest addresses, direction, owner IDs, backend
  endpoints, or provider handles;
- project-declared candidates from the workspace require review or ask behavior
  and must not auto-expose without user approval;
- observed-only candidates are audit-only or ask and must not auto-expose;
- backends without a host-to-guest provider fail closed before backend prepare.

Current Phase 1 evidence includes the product host-to-guest path for
profile-declared and manual candidates: candidate validation, active owner
registry checks, policy validation, native round-trip tests, Lima SSH
direct-tcpip provider tests, checks that reusable instance YAML does not persist
product forwards, backend fail-closed tests, audit, cleanup, and Boundary
Summary. Lima SSH tests must also lock the Phase 1 host-key posture: default
Lima `ssh.config` settings may use an explicit loopback-only unpinned callback,
but the bridge must not silently accept arbitrary non-loopback SSH endpoints.
Endpoint observation, project-declared candidates, direct JavaScript endpoint
entrypoints, OAuth callback automation, and guest-to-host exposure remain out of
this gate.

Commands:

```bash
hideout lab portbridge loopback --enable-lab --target 127.0.0.1:<port>
hideout lab portbridge guest-to-host --enable-lab --target 127.0.0.1:<port>
hideout lab portbridge host-to-guest --enable-lab --guest-target 127.0.0.1:<port>
scripts/test-lab-probes.sh
```

Product path:

```text
endpoint candidate declared by profile or approved project policy
  -> adapter proposal references candidateId
  -> manager validates endpoint.expose.host-to-guest
  -> PortBridge provider materializes the exact mapping
```

### Probe B: Browser Control

Evidence:

- isolated browser profile is used;
- loopback-only control endpoint is discovered;
- minimal handshake succeeds;
- no `host.open` path exposes the endpoint;
- failure is typed as lab-only, not product fallback.

Command:

```bash
hideout lab browser-control --enable-lab --profile <name> --browser-path <path>
HIDEOUT_BROWSER_PATH=/path/to/chromium scripts/test-lab-probes.sh
```

### Probe C: Preview Open

Evidence:

- one explicit guest HTTP service maps to one host-visible URL;
- mapping is owned by an OpenTarget proposal;
- no general host-local or guest-local network access is granted;
- cleanup closes the mapping.

Command:

```bash
hideout lab preview-open --enable-lab --guest-url http://127.0.0.1:<port>
scripts/test-lab-probes.sh
```

## Traceability Matrix

| Requirement | Unit | CLI Smoke | Lima E2E | Manual |
| --- | --- | --- | --- | --- |
| Profile schema and defaults | required | required | required | no |
| Profile clone regenerates identity | required | required | optional | no |
| Ephemeral identity isolation | required | required | required | no |
| Env denylist and synthetic env | required | required | required | no |
| Proxy env hidden from target | required | required | required | no |
| Tun2socks egress works | partial | no | required | maybe |
| Workspace read/write | partial | required | required | no |
| Missing command no host fallback | required | required | required | no |
| Command Proxy envelope | required | required | required | no |
| Host Broker URL/file policy | required | required | required | no |
| Isolated browser profile | required | optional | required | maybe |
| No DevTools from `host.open` | required | required | required | no |
| Audit redaction | required | required | required | no |
| Doctor diagnostics | required | required | required | no |
| Cleanup removes secret-bearing state | required | required | required | no |
| PortBridge and Endpoint Exposure product path | required | optional | optional | no |
| Browser-control lab isolation | partial | optional | optional | maybe |

## Environment Resume Acceptance

The Environment resume model is implemented for the Lima backend and is designed
to be covered by Gate 2 smoke. When Gate 2 is run successfully, it proves reuse
does not weaken privacy boundaries for the covered backend behaviors.

Required checks:

- `hideout run -- <command>` creates an environment for the current
  profile/workspace when none exists, while `hideout list` exposes the resume
  ID;
- a second `hideout run -- <command>` from the same workspace reuses that
  environment;
- running from a different workspace creates or selects a different environment;
- `hideout run --new -- <command>` creates a new environment for the same
  profile/workspace without changing profile identity;
- `hideout run --resume <id> -- <command>` resumes the exact environment only
  from the original workspace or a child directory;
- `--resume <id>` from an unrelated workspace fails closed before command
  execution;
- `hideout run --rm -- <command>` removes the reusable environment after the
  command exits;
- Gate 2 runs real Lima `--resume`, `--new`, and `--resume <id> --rm` commands
  and verifies `hideout list` reflects the expected environment records;
- Gate 2 runs real Lima `hideout stop <id>` and verifies the VM is stopped while
  the environment record remains resumable;
- Gate 2 covers `hideout stop --idle <duration>` and
  `hideout clean --stopped`/`hideout clean --idle <duration>` with temporary
  environments so idle memory release and destructive cleanup are not confused;
- successful reusable Lima runs keep target stdout/stderr clean by default;
  `--verbose` prints a resume hint on stderr without changing target stdout,
  while `--rm` runs do not print a reusable resume hint;
- every run, including resumed runs, gets a fresh session ID, broker token,
  command proxy shim directory, network plan, proxy secret runtime file, and
  audit context;
- stopped/stale environment cleanup preserves audit by default and never deletes
  the real workspace;
- `hideout list` shows ID, profile, backend, workspace, status, start/end
  times, and last command without leaking command secrets, broker tokens, proxy
  URLs, or raw machine IDs.

The Environment gate must run at least once with the Lima backend because the
primary value is preserving guest tool/cache state without preserving previous
runtime authority.

## Manager API Init And Run Acceptance

The minimal Manager API init and run surfaces are design-ready for TUI/WebUI and
automation integration. Required checks:

- `POST /api/v1/init/plan` returns the same `InitPlan` shape as Manager Core,
  accepts generic tool presets and user-declared npm global tools, includes
  structured next steps, and performs planning only;
- `POST /api/v1/init/apply` reaches `Core.ApplyInit`, uses typed init tasks
  rather than a raw profile writer, persists generic tool supply policy, and
  fails closed for confirmation-required tasks because API v1 has no prompt
  channel;
- `POST /api/v1/run/plan` returns the same `RunPlan` shape as Manager Core and
  performs planning only;
- `POST /api/v1/run/apply` reaches `Core.ApplyRun` through a configured backend
  factory and returns `RunResult`;
- local Manager API `run/apply` receives host-open behavior through a configured
  opener factory so command proxies use the same isolated opener as CLI runs;
- `GET /api/v1/run/status` returns session summaries and rejects invalid session
  filters;
- responses do not expose broker tokens, broker socket paths, proxy secret
  values, raw helper search paths, package-manager credentials, or arbitrary
  host file contents;
- the local server binds only to `127.0.0.1`, enforces token and origin/host
  checks, and does not expose lab or host-control routes as API resources.

## HostFS Portal Acceptance

HostFS Portal is covered by the Lima Gate 2 smoke for the Linux guest FUSE data
plane. When that gate is run successfully, it proves that ordinary guest
filesystem APIs can access explicitly granted host paths without exposing
ungranted host filesystem state.

Required checks:

- without a grant, `ls`, `cat`, Go `os.ReadFile`, Python `open()`, and Node
  `fs.readFileSync` when Node is present for a workspace-outside host path fail
  as missing and do not reveal whether the real host path exists;
- HostFS mount roots exist before the target command starts and remain empty
  unless grants expose entries;
- Lima starts the Linux `hideout-hostfsd` FUSE daemon only when active HostFS
  grants exist, fails before the target command when the daemon or `/dev/fuse`
  is unavailable, and cleans up the FUSE mount after the run;
- `doctor --backend lima` reports HostFS inactive when no grants are active,
  reports an error when active profile HostFS grants require a missing Linux
  `hideout-hostfsd`, and reports the daemon present without leaking helper
  search paths or granted host paths;
- Lima creates narrow compatibility graft symlinks for active grant entry
  points when preserved guest paths would otherwise bypass `/hideout/hostfs`;
  grafts must not replace existing guest paths, persist across runs as
  authority, or expose data without a matching broker grant;
- `hideout hostfsd build-linux` produces the Linux guest daemon in the default
  store location and that location is discoverable by `hideout run`;
- exact-file read grant allows `stat`, `open`, and read for that file;
- `read:` and `stat:` paths containing Go glob metacharacters (`*`, `?`, `[`)
  are treated as glob selectors using Go `filepath.Match` semantics, while
  paths without metacharacters remain exact-file selectors;
- `list:`, `dir:`, and `tree:` reject glob selectors instead of silently
  treating them as broader directory grants;
- glob read/stat grants allow matching files and filtered parent-directory
  listings, do not expose non-matching sibling names, and do not create backend
  passthrough mounts;
- glob deny rules win over matching allow rules and still audit the requested
  path plus matched deny rule ID/source;
- HostFS v1 write, create, delete, rename, truncate, chmod, chown, and xattr
  attempts fail as read-only or unsupported;
- HostFS read grants are live views, not snapshots; host-side changes during a
  guest read use normal host OS read semantics and must not be described as
  consistent snapshots;
- profile, environment, and run grants compose into the effective visible
  HostFS view;
- repeated CLI `--fs` flags compose run-scoped HostPathGrant records, do not
  persist into the profile, and do not create backend passthrough mounts;
- CLI `--no-fs` adds run-scoped deny rules that reduce effective HostFS
  authority for the current run without mutating the profile;
- CLI `--no-profile-fs` ignores profile HostFS grants for the
  current run, keeps profile deny rules active, and does not mutate the profile;
- `hideout profile fs <profile> add|deny|list|remove` manages durable profile
  HostFS rules using the same `--fs` and `--no-fs` grammar as run-scoped rules;
- Manager API `profile/hostfs/plan|apply` manages durable profile HostFS rules
  using the same HostFS rule grammar and profile validator as CLI
  `profile fs`, performs planning without creating profile state, and rejects
  raw host command or raw profile-writer request shapes;
- `hideout profile env` and `hideout profile tools` manage durable profile
  policy without introducing a second representation; env list output reports
  names only and must not echo stored values;
- Manager API `profile/env/plan|apply` manages durable profile env policy
  using the same profile validator as CLI `profile env`, performs planning
  without creating profile state, rejects raw host command or raw profile-writer
  request shapes, and must not echo public env values in responses;
- profile HostFS rules have stable unique IDs suitable for remove/edit
  operations by CLI, manager APIs, and future Web UI;
- deny rules win over allow grants regardless of whether the allow came from
  profile, environment, run, or script output;
- sensitive user-owned paths are hidden by default but become visible when the
  user explicitly grants them; Hideout must not block user intent with
  path-name-based credential guesses;
- exact-file read grant does not reveal sibling filenames through parent
  directory listing;
- non-recursive directory grant lists only the granted directory entries and
  does not traverse into child directories unless explicitly granted;
- recursive directory grant requires explicit syntax and is audited as broader
  authority;
- host symlink targets cannot escape the granted scope;
- path canonicalization happens on the host side before policy evaluation;
- HostFS RPC requires the current session token and cannot be replayed by a
  previous session or environment;
- audit records first access, deny, directory listing, and unsupported
  write-class attempts with the requested host path so the user can inspect what
  the target program probed. Audit records must still avoid raw broker tokens,
  unrelated filenames, user-provided rule reasons, extra symlink target paths,
  and backend mount implementation paths. HostFS audit details include policy
  effect, safe policy reason, rule ID when matched, source, operation, requested
  path, `policyEffect=unsupported` for read-only write attempts, and
  `canonicalized=true` when the request passed through a host symlink;
- the guest daemon can be restarted without gaining broader authority;
- Linux guest FUSE adapter works under Lima on macOS and under a Linux host
  backend when that backend exists;
- Windows native HostFS remains skipped or marked Later until a dedicated
  resolver and mount adapter exist.
- Access Sensor remains optional Later work and is not required for HostFS
  grant enforcement or data access.

Required implementation tests:

- host path resolver tests for canonicalization, symlink escape, case handling
  where relevant;
- grant matcher tests for exact-file, directory, recursive directory, expired
  grant, wrong subject, wrong operation, wrong session, deny precedence, and
  sensitive user-owned path grants;
- FUSE integration smoke for `ls`, `cat`, Go `os.ReadFile`, Python `open`, and
  Node `fs` when Node is present;
- broker authorization tests for invalid token, mismatched session, unknown
  grant, denied operation, and unsupported write operation;
- audit redaction tests for denied paths and directory enumeration.
- future Access Sensor tests, when implemented, must prove warning/audit output
  without granting host filesystem access or revealing hidden host path
  existence to the target.

## Distribution Bootstrap Acceptance

Distribution Bootstrap is the release-candidate proof that Hideout can start
from a clean installation without manual hidden setup.

Required checks:

- start from a clean temporary store and packaged or release-like artifact
  layout;
- source-tree install smoke proves `scripts/install-local.sh` can install
  `hideout`, the host shim, Linux guest helpers, helper manifests, and typed
  init metadata into a temporary prefix/store;
- source-tree install smoke proves `scripts/install-local.sh --network tun2socks
  --proxy-secret <ref>` passes only the proxy secret ref into InitTask and does
  not persist the raw operator proxy URL;
- install smoke proves installed `doctor --fix --dry-run` does not create state
  and installed safe `doctor --fix` writes current init metadata plus
  `doctor.fix.apply` audit;
- package smoke proves a release-like tarball contains binaries, helper
  manifests, a package manifest with schema/build/git/target/layout metadata,
  package-relative manifest paths that match the extracted layout, SHA-256
  checksums for critical package files that match the extracted files, English
  and Chinese README entrypoints, a package-root installer, schemas, docs,
  packaging metadata, can run extracted `hideout init --no-input` plus
  `hideout doctor`, can render extracted `hideout tui`, start extracted
  `hideout ui --print-url` without opening a browser or blocking, can run
  package-root `install.sh` into a separate temporary prefix/store, can discover
  packaged Lima Linux helpers from the installed prefix without rebuilding from
  the source tree, and verifies `install.sh --skip-init` copies binaries without
  writing init state;
- package smoke proves package-root `install.sh` fails before copying binaries
  when the extracted package layout is missing `package-manifest.json`, the host
  shim, Linux guest shim, Linux HostFS daemon, or when a manifest-declared
  checksum does not match the extracted file;
- Gate 0 statically validates the draft Homebrew formula and its
  `hideout init --no-input` formula smoke contract when Ruby is available;
- omitted or `auto` backend first-run repair resolves to Lima, matching
  `hideout run`, and plans Linux helper repair when store helpers are missing;
- `hideout init --no-input --backend native --network direct` succeeds without
  using arbitrary shell scripts as an explicit weak-isolation smoke;
- Manager API run status tests must cover a real `run/apply` session, not only
  an empty store, and must prove status exposes only session summaries and
  presence booleans for broker/proxy artifacts;
- `hideout init` is idempotent and does not rotate identity, enable bundles, add
  HostFS grants, create passthrough mounts, open host apps, create PortBridge, or
  change network routes;
- `hideout doctor` reports core checks after init;
- `hideout doctor --fix --dry-run` shows safe fixes without executing unsafe
  tasks;
- `hideout init --npm-package <spec> --npm-command <name>` and
  `hideout doctor --fix --dry-run --npm-package <spec> --npm-command <name>`
  plan generic CLI tool supply without product-specific logic;
- `hideout init --network tun2socks --proxy-secret <ref>` and Manager
  `init/apply` persist only the proxy secret ref, not a proxy URL or backing env
  var name;
- `hideout doctor --fix --dry-run --backend lima` includes
  `helper.install.linux-shim` and `helper.install.linux-hostfsd` when the
  official store helpers are missing and a source-tree repair is available;
- one safe repair can be applied through InitTask plan/apply and emits
  `hideout.init-audit/v1` JSONL under `logs/init-audit.jsonl`;
- `hideout run` and Manager `run/apply` apply pending lightweight
  store/profile/schema metadata InitTasks after backend availability succeeds
  and before any session/backend prepare side effect, emit `run.init.apply`, do
  not build backend helpers as run auto init, and do not write init audit when no
  InitTask is pending;
- dry-run init or repair plans do not create init audit events;
- helper binary discovery checks official store path and explicit development
  overrides, and fails closed on missing or mismatched helpers;
- store-built Linux helpers have sibling `hideout.helper-manifest/v1` manifests
  with command, target OS/arch, artifact name, and matching SHA-256;
- default store helpers without a current manifest are treated as pending repair
  by InitTask, while explicit development override paths may bypass store
  manifests;
- project and bundle setup hints compile into `InitRequirement` or `InitPlan`
  records, not executable scripts;
- stale or invalid install-state metadata plans `schema.metadata.write`, rewrites
  current metadata, and emits init audit without preserving draft metadata
  semantics;
- cleanup and uninstall-like operations never delete the workspace or profile
  identity unless explicitly confirmed.

This gate should stay narrow. It proves first-run product completeness and
InitTask boundaries, not every backend-specific capability.

## Additional Passthrough Mount Acceptance

Additional passthrough mounts are Design-ready until implemented. They are
separate from HostFS and represent explicit broad filesystem sharing.

Required checks:

- no path outside the workspace is mounted unless the user explicitly declares a
  mount;
- workspace safety guard rejects the host home, the effective Hideout store
  root, credential roots, browser profile roots, symlinks to those roots, and
  parent directories that would mount them into the guest;
- workspace safety guard allows ordinary project directories under the host home
  when they do not contain the protected roots;
- the explicit unsafe-workspace override is required before such a high-risk
  workspace can be mounted;
- `rw` mounts permit read/write from the guest and changes are visible on the
  host;
- `ro` mounts permit reads and reject guest writes;
- mounted paths are shown in `explain`, audit, and UI with host path, guest path,
  mode, and high-risk classification;
- high-risk mount roots such as home, root, Docker sockets, SSH agent sockets,
  browser profiles, keychains, and credential directories require explicit
  high-risk policy handling;
- HostFS grants do not create backend mounts and backend mounts do not create
  HostFS grants;
- cleanup never deletes the mounted host path.

## Test Artifacts

Each end-to-end run should preserve the following when debugging is explicitly
enabled:

- command line and profile name;
- backend name and version evidence;
- `hideout explain` output;
- `hideout doctor` output;
- audit JSONL;
- redacted network plan;
- redacted Lima YAML validation output;
- lab probe audit when a lab command is used.

`scripts/test-release-dogfood.sh` always writes a release evidence bundle. By
default it is placed under `.hideout-release-evidence/`; set
`HIDEOUT_RELEASE_EVIDENCE_DIR` for an exact output path or
`HIDEOUT_RELEASE_EVIDENCE_ROOT` for a different parent directory. The bundle
contains:

- `manifest.json` with command, git revision, host prerequisites, tool versions,
  gate list, exit code, operator proxy presence, and the generated release-like
  tarball file name, SHA-256, and byte size. It must conform to
  `schemas/release-dogfood.schema.json`;
- `hideout-<os>-<arch>-<commit>.tar.gz`, the release-like artifact built from
  the same worktree before gates run;
- `test-release-dogfood.log` with redacted gate output.

The manifest must record `operatorProxy.url` as `redacted` and the log must not
contain the raw `HIDEOUT_SECRET_DEFAULT_PROXY` value. Gate 0 verifies that the
recorded release artifact exists in the evidence directory and that its SHA-256
matches the manifest.

The following must never be copied into normal diagnostic exports:

- proxy URLs or credentials;
- host credential files;
- raw machine-id values;
- browser cookies or local storage;
- generated private identity material unless the user explicitly exports
  identity.

## Release Decision

Phase 1 is releasable only when:

- `scripts/test-phase1.sh --release-candidate` passes on a macOS developer
  machine with Lima, a real supported browser launcher, and an operator-supplied
  proxy in `HIDEOUT_SECRET_DEFAULT_PROXY`;
- `scripts/test-release-dogfood.sh` passes on the same machine as the named
  dogfood-ready evidence bundle;
- Gate 0 passes in CI or local release verification;
- Gate 1 native development harness passes on a developer machine as
  engineering evidence only; it does not replace Lima or release-candidate
  gates for product isolation;
- Gate 2 passes on macOS with Lima;
- Gate 3 passes with the auto-started test proxy in normal required automation;
- Gate 3 passes in strict operator proxy mode during
  `scripts/test-phase1.sh --release-candidate`;
- Gate 4 passes in dry-run automation, and passes once with a real supported
  isolated browser launcher for the external URL case;
- all Capability Probe code remains unreachable from default `hideout run`;
- all known failures are either fixed or explicitly reclassified in the design
  document.

Until these gates pass, the project remains an advanced prototype rather than a
Phase 1 release candidate.
