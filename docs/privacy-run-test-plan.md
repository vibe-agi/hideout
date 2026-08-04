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

# Include only the real Lima backend gate in addition to fast gates. The
# supervised Lima real-run reference smoke is an optional Gate 2 step; see
# Gate 2 below.
scripts/test-phase1.sh --lima

# Include only hidden proxy/tun2socks in addition to fast gates.
scripts/test-phase1.sh --proxy

# Release-candidate gates: real browser, operator proxy, and probes.
HIDEOUT_SECRET_DEFAULT_PROXY=socks5://127.0.0.1:<port> \
  scripts/test-phase1.sh --release-candidate

# Same release-candidate proof through the dedicated dogfood entrypoint.
HIDEOUT_SECRET_DEFAULT_PROXY=socks5://127.0.0.1:<port> \
  scripts/test-release-dogfood.sh

# Release-hardening readiness artifact. Local-fast is development evidence,
# not release evidence; candidate mode also requires every registered product
# evidence manifest (repeat --product-evidence as needed).
scripts/test-release-readiness.sh --local-fast --out /tmp/hideout-readiness.json
HIDEOUT_GATE2_EVIDENCE=/path/to/gate2.json \
HIDEOUT_GATE3_EVIDENCE=/path/to/gate3.json \
  scripts/test-release-readiness.sh --release-candidate \
    --package-artifact /path/to/hideout-vX.Y.Z-darwin-arm64.tar.gz \
    --signing-observation /path/to/signing.json \
    --notarization-observation /path/to/notarization.json \
    --product-evidence /path/to/product-hardening.json \
    --out /tmp/hideout-rc.json

# Include command-level capability probe smoke after product gates.
scripts/test-phase1.sh --quick --probes

# Include the generic CLI dogfood smoke. This uses a fake test CLI and fake API
# to verify callback login, profile identity persistence, authenticated request
# flow, and control-plane store denial without binding Hideout to any product.
scripts/test-phase1.sh --dogfood-cli

# Run the declared-image boot gate variant: create a named environment from a
# digest-carrying image URL, boot it, and prove a wrong digest fails closed.
HIDEOUT_ENV_IMAGE_URL='https://<distributor>/<image>.img#sha256:<digest>' \
  scripts/test-phase1.sh --env-image

# Run the operator-supplied real CLI smoke on Lima. The operator provides a
# guest environment where the command is already runnable.
scripts/test-phase1.sh --operator-cli
```

## Development Test Planning

Every implementation change must choose a test scope before coding. The scope
is based on the authority that changed, not on the file name alone.

Delivery discipline (constitution 1.3.0):

- A new assertion is delivered only with a mutation proof: temporarily break
  the guarded implementation, observe the test fail, then restore it. A new
  judge or gate check is delivered only with a negative fixture that shows it
  firing. Green-only assertions have historically pinned defective behavior as
  expected and do not count as coverage.
- An implementation batch ships its own adversarial report — fresh-eyes
  findings, mutation proofs, and negative fixtures — so external review can
  spot-check at a depth matched to the batch's risk instead of re-deriving it.
- Work a slice intentionally defers must land in [DEBT.md](DEBT.md) with a
  concrete trigger condition before the slice is marked done.
- `scripts/test-gate0.sh --quick` is the inner-loop tier only (vet, format,
  cached tests, markdown lint, schema syntax); it never satisfies a gate,
  claim, or commit requirement. The full gate remains mandatory before commit.

Change-to-gate mapping:

| Change area | Minimum local check | Boundary proof | Release-candidate check |
| --- | --- | --- | --- |
| Docs, schemas, and generated examples | Gate 0 | none | Gate 0 |
| Ecosystem foundation, bundle schemas, project manifests, trust, export, or script ABI | Gate 0 and targeted schema tests | Manager plan/apply tests when authority changes | third-party trust checks (schema validation, digest pin, permission diff confirmation) before public ecosystem release |
| First-run initialization, InitTask, helper discovery, `doctor --fix --dry-run\|--apply`, schema metadata repair, or project bootstrap | Gate 0 and targeted InitTask tests | Gate 1 native harness for CLI shape only; Gate 2 when backend preparation changes | Distribution Bootstrap acceptance (install/package smokes, run inside Gate 0) |
| CLI parsing, profile, identity, env, audit, Boundary Summary, cleanup, or doctor | Gate 0 and the native harness | affected package tests | `--required` if behavior is externally visible |
| Command Proxy, Host Broker, `host.open`, file open, or browser launcher | Gate 0, native harness, and Gate 4 dry-run | Gate 2 when guest shims or broker transport change | real-browser Gate 4 |
| HostFS Portal, HostPathGrant, discoverable namespace, read decisions, guest FUSE daemon, or host filesystem RPC | Gate 0, targeted HostFS unit tests, and `scripts/test-hostfs-visibility-e2e.sh --local-fast` | Gate 2 on Linux guest backend with read/list/discover grants and live separate-process approval | Gate 2 HostFS coverage; local-fast cannot satisfy real namespace or live-grant proof |
| Additional passthrough mounts | Gate 0, native harness for CLI shape, and mount contract tests | Gate 2 when backend mount config changes | required if user-facing |
| Lima backend, mounts, guest bootstrap, guest command resolution, base image declarations, or instance lifecycle | Gate 0 and Gate 2; native harness only for shared CLI wiring | Gate 2 on macOS with Lima; `--dogfood-cli` when it affects CLI workflows | `--release-candidate` |
| Environment model: naming, auto-name resolution, drift semantics, record versioning, or image declaration plumbing | Gate 0 and targeted environment/manager/app tests | `scripts/test-env-image.sh` (via `--env-image`) proves declared-image boot, digest-drift fail-closed, and recreate recovery on macOS with Lima | `--release-candidate` remains separate |
| Supervised Lima real-run dogfood slice | Gate 0 and targeted test CLI tests | `scripts/test-lima-real-run.sh` as the optional Gate 2 step on macOS with Lima | `--release-candidate` remains separate |
| Network setup, proxy secrets, route verification, or `tun2socks` | Gate 0, native harness for shared CLI wiring, and Gate 3 | Gate 3 with auto proxy; Gate 2 if bootstrap changes | Gate 3 strict operator proxy |
| Policy scripts, Goja ABI, command adapters, adapter packs, or scriptable extension points | Gate 0 and native harness where CLI-visible | relevant denied and allowed path tests; command-adapter smoke for profile/broker wiring; adapter-pack smoke for registry/test/enable/revoke wiring | `--required` if a required route is affected |
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
- the install and package smokes pass (`scripts/test-install-smoke.sh` and
  `scripts/test-package-smoke.sh`; Gate 0 runs both on every invocation,
  which is how the Distribution Bootstrap acceptance is wired in). Package
  smoke proves artifact verification, installed-prefix verification without
  source checkout paths, helper/schema discovery, missing helper/checksum
  fail-closed behavior, compatible upgrade preservation, incompatible migration
  denial, uninstall dry-run, uninstall preserve, and explicit purge;
- no Required Phase 1 behavior depends on lab commands, Web UI, or a daemon.
- every architecture document that introduces authority has a design status,
  failure behavior, and either a release gate or an explicit Later status.
- `docs/threat-model.md` defines the Phase 1 Lite TCB, claims, non-claims,
  user-authoritative HostFS grant model, loopback boundary, and PortBridge
  invariants before new PortBridge-backed capabilities are promoted.
- RunResult schema includes Boundary Summary as structured data derived from
  audit facts, not as a CLI-only rendering.
- Export/share redaction smoke (`scripts/test-export-redaction-smoke.sh`)
  exercises audit, release-evidence bundle, and Boundary Summary export
  sources; validates `schemas/export-artifact.schema.json`; proves
  control-plane cleanliness, user-selected redaction, reference resolution, and
  evidentiary fail-closed behavior without a real backend.
- `hideoutd` local control-plane smoke (`scripts/test-daemon-smoke.sh`, wired into
  Gate 0) exercises daemon start over a store-rooted guest-unreachable socket,
  token-authenticated Manager parity (including the special-cased GET routes),
  audited unauthenticated refusals with no token material, the daemon status
  endpoint against `schemas/daemon-status.schema.json`, and an ordered stop — with
  no real backend. Daemon lifecycle, auth, event redaction, restart fail-closed,
  and background status are covered by `go test ./internal/daemon/...`.
- Daemon live operations console smoke (`scripts/test-live-console-smoke.sh`,
  wired into Gate 0) validates the typed daemon event and live-console seed
  schemas, catalog/reducer drift guards, production emit-source coverage,
  daemon multi-subscriber/backpressure behavior, WebUI deterministic JavaScript
  reducer/action proof with no post-seed fetches, TUI terminal proof,
  stream-health propagation, 019 Operator Console panel/action-route coverage,
  027 Manager route and daemon endpoint drift guards, runtime-observed WebUI
  action-route recognition, TUI route-boundary checks, and control-plane
  redaction scans — with no real backend, headless browser, or external browser
  dependency.
- Operator decision center smoke (`scripts/test-decision-center-smoke.sh`,
  wired into Gate 0) validates actionable decision vs informational notice
  contracts, public redaction of claim tokens and provider-private refs, local
  export compatibility, `evidence.share` claim/approve release, and CLI/watch
  convergence without a real backend.
- Command-adapter smoke (`scripts/test-command-adapter-smoke.sh`, wired into
  Gate 0) validates the 008 profile schema, command-adapter schema, Manager
  plan/apply path, broker outcomes, root-sensitive intent wording, and digest
  fail-closed behavior without claiming root containment.
- Adapter-pack smoke (`scripts/test-adapter-pack-smoke.sh`, wired into Gate 0)
  validates the 011 local pack lifecycle: manifest/registry schema presence,
  install/list/test/enable/revoke CLI wiring, mandatory tests before enable,
  exact profile binding of pack id/revision/adapter/source digest, runtime
  fail-closed behavior for revoked packs, and preservation of the 008
  non-applied proposal model. 011 is local registry and script lifecycle work;
  it does not require real Lima proof.
- Doctor diagnostics smoke (`scripts/test-doctor-smoke.sh`, wired into Gate 0)
  validates the local doctor report path: human output, JSON schema,
  `--level deep`, selected feature diagnostics, structured next actions and
  gate-required markers, required-failure exit, warning/degraded zero exit,
  explicit doctor report export, deterministic control-plane redaction
  injection, and safe recovery dry-run. It remains local troubleshooting
  evidence and must not replace real Gate 2/Gate 3 proof.
- Release hardening smoke (`scripts/test-release-hardening-smoke.sh`, wired into
  Gate 0) validates the 016 support matrix schema, `hideout support matrix`,
  `hideout version` matrix summary, doctor support-matrix finding,
  compatibility fixture inventory, redacted readiness artifact shape,
  local-fast non-release honesty, release-candidate missing-evidence
  fail-closed behavior, and docs drift/non-claim checks. Full
  `scripts/test-release-readiness.sh --release-candidate` still requires real
  Gate 2 and Gate 3 evidence; Gate 0 smoke must not replace those gates.
- First-run docs smoke (`scripts/test-first-run-docs-smoke.sh`, wired into
  Gate 0) validates the 020 external-alpha walkthrough: package verify, privacy
  Lima init, doctor deep recovery, first run, HostFS write decision visibility,
  daemon/TUI/WebUI entry points, native-only-as-harness wording, and no stale
  `go run` examples in the user-facing path.
- Alpha first-run E2E (`scripts/test-first-run-e2e.sh --local-fast`, wired into
  Gate 0) validates the 022 package-to-first-command path: packaged install
  with `--skip-init`, installed package verification, one weak/dev native
  profile init, one installed-binary command, audit/Boundary capture, schema
  validation, redaction scan, and `hideout.product-hardening-evidence/v1`
  output. This is local package mechanics evidence only. The same script's
  `--real-backend` mode records explicit pass or `not-run` evidence for the
  Lima/privacy path, and `--require-real` is the manual/release-style mode for
  hosts where real proof is mandatory.
- Zero-friction setup (038) adds, rather than replaces, two lanes in the same
  harness. `--setup-local-fast` installs the candidate with `--skip-init` and
  executes the installed `hideout setup` under a real PTY; it proves review,
  confirmation, configuration-only apply, schema validity, and zero Lima
  invocation. `--setup-real-backend --require-real` uses the candidate binary
  and real macOS arm64 Lima to prove `/workspace`, both account-home and target
  `HOME` layers, non-root identity, exact runtime, audit, Boundary evidence,
  final-session stop, exact environment reuse, and an exact-integrity agent
  installed and executed by name in separate sessions. The retained 022
  `--real-backend` privacy lane remains distinct and mandatory for its network
  claim. Local/native and `not-run` output cannot satisfy either real lane.
- UI E2E proof (`scripts/test-ui-e2e.sh`, wired into Gate 0) writes a
  `hideout.product-hardening-evidence/v1` manifest. On hosts with local
  Chrome/Chromium and `script(1)`, targeted runs can require executed browser
  and TUI lanes: the browser lane opens the daemon-served WebUI and performs a
  notice acknowledgement round trip; the TUI lane launches the real `hideout tui`
  process in a terminal harness and proves live event update, no healthy-stream
  interval polling, and stream fallback. Missing prerequisites are recorded as
  `not-run` evidence in non-completion modes. This local UI E2E evidence does
  not claim release readiness; `scripts/test-release-readiness.sh
  --release-candidate` still requires the real release gates.
- HostFS/decision E2E proof (`scripts/test-hostfs-decision-e2e.sh --local-fast`,
  wired into Gate 0) writes a `hideout.product-hardening-evidence/v1` manifest
  for 023. The local-fast lane proves staged overlay decision records,
  claim-race one-winner behavior, approve/deny/timeout outcomes, live-console
  model visibility, coverage-matrix honesty, schema validity, and public
  artifact redaction without claiming real guest HostFS data-plane behavior.
  The same script's `--real-gate2` lane is explicit and prerequisite-gated:
  it either proves the real Lima guest reads staged HostFS content before
  apply while host lower state remains unchanged, or records `not-run`
  evidence. Native/local-fast output must never satisfy a real Gate 2 HostFS
  claim.
- Doctor/package recovery E2E (`scripts/test-doctor-package-recovery-e2e.sh
  --local-fast`, wired into Gate 0) writes a
  `hideout.product-hardening-evidence/v1` manifest for 024. It reuses the
  existing package and doctor smoke paths, proving stale package verify,
  package repair dry-run/apply/verify, durable and unrelated file preservation,
  doctor deep guidance, safe doctor fix dry-run/apply, selected doctor-report
  export, and public artifact redaction. It is local recovery evidence only:
  doctor guidance for Lima, DNS, HostFS, privilege, or release gates remains
  guidance and must not replace real Gate 2/Gate 3 or release-readiness proof.
- Test and evidence spine (026, wired into Gate 0) validates that
  `internal/productevidence` is the shared source of proof ids and evaluation
  rules for 021-025 product-hardening evidence. `hideout support proof-registry
  --json` exposes the same registry to shell gates and docs truth, stale
  evidence is reported as an evaluator result rather than a manifest proof
  status, and product-hardening evidence can appear in release readiness only as
  supporting context that never satisfies real Gate 2/Gate 3 requirements.
- Documentation truth smoke (`scripts/test-doc-truth-smoke.sh`, wired into
  Gate 0) validates `docs/claim-boundaries.md`,
  `docs/command-examples.json`, current README/docs/spec claim boundaries,
  known overclaim patterns, curated command recognition, localized README
  canonicality, and Gate 0/test-plan consistency for 021-024. It writes 025
  product-hardening evidence and is a local docs truth gate only; it cannot
  substitute for release-readiness or real backend evidence.
- Error recovery hint contracts (028) validate the Go-owned
  `hideout.recovery-codes/v1` registry via
  `hideout support recovery-codes --json`, doctor human/JSON parity, package
  and init recovery-code surfacing, release-readiness codes for missing or
  stale evidence, and documentation references checked by doc truth. The v1
  scope is host-observable surfaces only; Manager/daemon-wide typed error
  migration and guest bootstrap internals remain deferred.

Gate 0 enforces the last item with a single phase plan assertion: the required
plan (Gate 0 through Gate 4, printable with `HIDEOUT_PHASE1_PRINT_PLAN=1`) must
stay independent of capability probe smoke, lab commands, Web UI, `hideoutd`,
and daemon-dependent behavior; release-candidate mode may add probe smoke.

Ecosystem contract checks belong in Gate 0 until public bundle support is
promoted. Gate 0 should reject bundle or project schema changes that allow raw
host execution, raw mounts, unrestricted scripts, profile identity export, or
silent authority changes outside Manager plan/apply. A declarative guest base
image reference (name plus digest) is an allowed guest-domain artifact and must
not be rejected by these checks; imperative environment-preparation recipes
remain rejected.

Script extension contract checks belong in Gate 0. Gate 0 should reject script
ABI or SDK changes that expose raw Go standard library packages, host filesystem
handles, network clients, process APIs, environment APIs, backend driver
handles, broker tokens, mutable Manager state, or any capability execution path
outside validated proposals. Command-adapter ABI checks must additionally reject
unknown outcomes, unknown fields, undeclared proposal capabilities, root-sensitive
successful system-mutation simulation, and any 008 wording that claims root
escalation is blocked without 009 enforced privilege separation.

Redaction contract checks belong in Gate 0 and package tests. Redaction is
deterministic: tests must prove Hideout-minted control-plane credentials
(broker `cap_`/UI `ui_` token values, `HIDEOUT_SECRET_*` backing names and
values adjacent to those names, generated machine-id, and Core control-plane
detail field names) never appear in script context, audit, API/WebUI
responses, or exports. Machine-id display IDs are decoupled from generated
machine-id material, and legacy coupled IDs rotate through the profile metadata
path, so the redaction contract is not defeated by identity-ID derivation. Tests
must also prove that user/application data (URLs, argv, query
values, headers) is preserved verbatim on local surfaces — including a bare
proxy-shaped string that carries no `HIDEOUT_SECRET_*` label, which Core
cannot distinguish from a user URL. Keeping raw proxy URLs out of target
output and evidence is a flow obligation on the Hideout-managed proxy secret
flow, checked by this plan's raw-proxy-URL leak assertions, not by a redactor
scan. The same deterministic control-plane rule governs policy and
lab proposal resources: user URLs and query values are accepted, only
control-plane material is rejected. Heuristic user-data redaction (key-name,
flag, header, or query-parameter guessing) must not be reintroduced in audit,
the policy validator, or `schemas/policy.schema.json`.

InitTask contract checks also belong in Gate 0. Gate 0 should reject first-run
or remediation designs that rely on arbitrary shell, `host.exec`, raw mounts,
raw routes, bundle init scripts, project init scripts, or automatic profile
authority mutation outside Manager plan/apply. A declared base image reference
is declarative guest-domain data compiled into the backend prepare plan, not a
prohibited init script; imperative recipes remain rejected.

### Gate 1: Native Development Harness

Purpose: fast local smoke for CLI wiring and privacy semantics that do not
require a VM. This gate is a development harness, not product isolation proof
and not dogfood evidence. It uses the explicit weak native backend and must
always declare weak isolation in output.

Scope:

- `hideout init --template dev --profile native-dev --backend native --network direct --no-input`;
- `profile init`, `profile clone`, `profile path`;
- `run --backend native --allow-weak-isolation`;
- `explain`;
- `doctor`;
- `cleanup`;
- Command Proxy and Host Broker behavior using test openers where possible.

Required checks:

- explicit native dev-template init creates store directories, default profile,
  profile identity, schema metadata, and runtime directories under a temporary
  store;
- repeated doctor repair is idempotent, while template-aware init rejects an
  existing profile instead of rotating identity, enabling bundles, adding HostFS
  grants, or creating new authority;
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
- when the profile declares a base image reference, the gate validates that the
  declared image supplies this minimal toolset before required checks run;
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

- template-aware `hideout init --template dev ... --no-input` and
  `hideout init --template privacy ...` validate Lima prerequisites, helper
  discovery, template evidence, and generated backend metadata without starting
  the VM unless a deep check is explicitly requested;
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

#### Required Gate 2 Step: Zero-Friction Setup

The 038 setup lane consumes a distributed candidate, not a source-tree binary:

```bash
scripts/test-first-run-e2e.sh --setup-real-backend --require-real --out <dir>
```

The proof must contain `038.setup.real-gate2.first-run` and
`038.setup.real-gate2.agent-install-run` as passed results for the exact
candidate. `038.setup.real-gate2.not-run`, native execution, elapsed-time-only
reuse inference, or the separate 022 privacy lane cannot satisfy these claims.

#### Required Gate 2 Step: Host Capability Projection

Gate 0 runs `scripts/test-projection-readiness-smoke.sh` for strict schemas,
mechanics, evaluator negative fixtures, and false-green shell fixtures. It
cannot establish guest visibility or a host effect.

`scripts/test-gate2-lima.sh` requires the strict 043 projection-readiness lane,
which retains the 030 built-in, 032 external-pack, and 039 persistent-grant
proofs from one clean exact package. The evidence wrapper is:

```bash
scripts/test-projection-readiness-lima-e2e.sh --require-real \
  --fresh 10 --warm 30 --out <dir>
```

The lane installs the verified package and fixed Linux helpers, creates fresh
and warm exact session catalogs, and proves:

- 10 fresh and 30 warm projected first targets complete without retry;
- concurrent disjoint catalogs remain session-local;
- the complete manifest, executable type/digest, ready/commit proof, timeout,
  cancellation, and identity/catalog drift boundaries fail closed;
- guest `code -g` reaches the explicit `host.app.open-resource` route and opens
  a code-signed host VS Code bundle with a run-scoped safe user-data directory;
- a folder-open task marker stays absent, extensions are disabled, automatic
  tasks are off, and Workspace Trust is not disabled;
- an external pack passes exact authority/lifecycle/no-fallback checks; and
- persistent trusted mode refuses, succeeds in a separate run after a host
  grant, and refuses again after revoke.

The strict evaluator requires
`043.projection-readiness.real-gate2.readiness`,
`030.projection.real-gate2.code-open`,
`030.projection.real-gate2.trusted-grant`,
`032.host-app-pack.real-gate2.external`, and
`039.trusted-host-app-grant.real-gate2.persistent`. Local, dirty, reduced,
marker-only, package-mismatched, or `not-run` fixtures cannot satisfy them.
Alias privacy remains a separate matching clean Gate 3 promotion.

#### Optional Gate 2 Step: Lima Real-Run Reference Smoke

Purpose: prove one supervised dogfood reference workload in the real Lima
backend without binding Hideout to a product-specific agent. The target is
`hideout-test-cli workload`, compiled for the Linux guest into a temporary
sanitized workspace. It writes and verifies a deterministic workspace artifact,
reaches a declared endpoint through the selected network mode, and prints only
stable non-secret `lima-real-run:` markers; host-side verification is limited
to read-only assertions over the workspace artifact and redacted evidence.

Commands:

```bash
scripts/test-lima-real-run.sh
scripts/test-phase1.sh --lima-real-run
```

The smoke exercises a fixed boundary action set — denied `host.open` for a
localhost/private target, denied HostFS access plus a reserved-store grant
rejection, session start/end and network setup evidence, and one
`preview.open` / `endpoint.expose.host-to-guest` event. Evidence is derived
from `hideout run --verbose` output and
`hideout audit show --session <id> --json`; the smoke must not recompute
boundary facts independently.

Privacy variant: set `HIDEOUT_SECRET_DEFAULT_PROXY` and
`HIDEOUT_LIMA_REAL_RUN_NETWORK=privacy` to route through the existing
`tun2socks` run options; the smoke fails closed if the operator proxy or
guest-side route preparation is unavailable, and the raw proxy URL must not
appear in target output, control output, audit JSON, or smoke logs. Gate 3
remains the dedicated hidden-proxy release gate.

### Supplemental: Generic Test CLI Dogfood Smoke

Purpose: prove the product mechanics needed by a CLI-style tool
without putting a specific third-party product into Hideout code or tests.

The smoke uses `hideout-test-cli`, a fake test CLI binary, and
`hideout-gate-lab-target`, a fake bearer-token API. It verifies:

- the guest runtime needed by the test CLI comes from the declared base image
  or default guest, not from a Hideout-shipped package-installation provider;
  any extra setup runs as an ordinary operator-authored in-boundary
  `hideout run`;
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
  `hideout profile home import --from <host-path> --to
  <relative-profile-home-path>` primitive without exposing source paths or
  credential contents in smoke output; `--force` re-import keeps reruns against
  the same store deterministic;
- that authentication state persists across reused Lima runs for the same
  profile and workspace;
- the target can make an authenticated HTTP request from inside the guest to a
  host-owned fake API;
- run-scoped `--env-var KEY=VALUE` can expose a user-chosen variable while Hideout
  runtime env remains hidden from the target;
- HostFS cannot grant the Hideout control-plane store into the guest.

This smoke is not an adapter for any real product. It is a product-mechanism
proof: guest-local callback flow, typed preview callback reach-back, host
redirect boundary, environment policy, profile-state persistence, network
request, and control-plane store protection.

The smoke runs with `--network direct`. It proves CLI product mechanics over an
already-provisioned guest, not proxy-routed traffic. A base image declaration
change, or a change to `tun2socks` bootstrap, setup env filtering, or proxy
secret handling, must also run Gate 3 or an equivalent smoke that proves the
first `tun2socks`-routed connection.

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

### Gate 3: Hidden Proxy

Purpose: prove the user requirement that proxy configuration is effective for
network traffic but not readable by JavaScript or the target process env.

Prerequisites:

- Lima backend;
- the exact package-owned Linux `tun2socks` v2.6.0 helper for release evidence.
  Gate 3 verifies its package inventory, target, pinned-module provenance,
  upstream MIT license, and digest, ignores ambient `PATH`, and forbids
  `HIDEOUT_LINUX_TUN2SOCKS_PATH` in release mode. Source-development Gate 3 may
  use an explicit override or its isolated pinned temporary build, but that
  evidence cannot satisfy a package candidate;
- a test proxy endpoint; if `HIDEOUT_SECRET_DEFAULT_PROXY` is omitted, the gate
  script starts a temporary host-local SOCKS5 proxy at
  `socks5://127.0.0.1:<port>`; the host gateway consumes this URL and exposes
  only its separate authenticated endpoint to the guest;
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
- `hideout init --template privacy --network tun2socks --proxy-secret <ref>
  --mediated-resolver <ip>` persists only `network.proxySecretRef` plus the
  mediated resolver IP, and `tun2socks` init without a proxy secret ref or
  resolver fails closed before profile mutation;
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

Gate 3 verifies the DNS closure end to end on real Lima: with privacy mode it
confirms the guest resolver is the DoH stub (`dns_mediated=yes`), resolves a name
through that stub and the mediated DoH path (`dns_forward=ok`), completes a
separate HTTPS request through the same endpoint (`https_request=ok`), and
proves that the connected-subnet resolver is blocked while the proxy secret
stays hidden. The same run requires `guest_workspace=/workspace` and emits
`projection_alias_gate3=passed`, so adding projection does not regress the DNS,
network, or privilege boundary. It requires `HIDEOUT_GATE3_MEDIATED_RESOLVER` (a
DoH server IP, default `1.1.1.1`). For developer-host portability, the
self-contained fixture may map that exact Cloudflare anycast destination to
`cloudflare-dns.com` for host-side dialing, and may chain through `HTTPS_PROXY`
via HTTP CONNECT. The guest-visible destination and TLS identity remain
`1.1.1.1`; host proxy values never enter the target environment. A release
candidate still requires the operator-supplied proxy path. The DNS closure and
its architecture are owned by
[network-privacy-architecture.md](network-privacy-architecture.md). The residual
A3 guest-root routing bypass remains a non-claim in
[threat-model.md](threat-model.md). Since 009, the same gate also asserts the
Lima privilege setup evidence: `privilege_status=enforced` and
`privileged_setup=network`, proving privacy-mode route/DNS bootstrap used the
root-control setup identity rather than target-user passwordless sudo.

Suggested target command:

```bash
sh -c 'env | grep -iE "^(http_proxy|https_proxy|all_proxy|no_proxy)=" || echo proxy-env-absent'
```

Pass condition: the target prints `proxy-env-absent` because no proxy variables
exist in its env, while an independent HTTP(S) route check, such as a `curl`
request from the guest, proves traffic uses the configured proxy.

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
Chromium-compatible browser binary and must not be `open` or `xdg-open`. If
`HIDEOUT_BROWSER_PATH` is not set, the script must resolve a direct
Chromium-compatible browser binary itself and invoke it through a gate-owned
temporary launcher. Real-browser Gate 4 must not use generic URL openers such
as `/usr/bin/open` or `xdg-open`; the test must own the browser process
lifetime so cleanup evidence is meaningful.
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
- real-browser Gate 4 must terminate temporary browser processes that use a
  `hideout-gate4.*` isolated profile and remove the matching temporary
  directories. Cleanup must match the test profile path, not the operator's
  normal browser profile;
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
Automatic service-listener discovery (`endpoint.observe`), project-declared
candidates, direct JavaScript endpoint entrypoints, OAuth callback automation,
and guest-to-host exposure remain out of this gate. This does not exclude
Feature 045's bounded workload process-to-IP/port activity evidence.

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
| PortBridge and Endpoint Exposure product path | required | optional | required | no |
| Browser-control lab isolation | partial | optional | optional | maybe |

## Environment Resume Acceptance

The Environment resume model is implemented for the Lima backend and is designed
to be covered by Gate 2 smoke. When Gate 2 is run successfully, it proves reuse
does not weaken privacy boundaries for the covered backend behaviors.

Required checks:

- `hideout run -- <command>` creates the deterministic auto-named environment
  for the current profile and workspace when none exists, while
  `hideout env list` shows it marked as auto-named;
- a second `hideout run -- <command>` from the same workspace reuses that
  environment;
- running from a different workspace resolves a different auto-named
  environment;
- `hideout env create <name> --workspace <ws>` creates an explicit named
  environment with a pinned base image declaration, and `hideout run --env
  <name> -- <command>` runs inside it;
- `hideout run --env <name>` from a conflicting workspace fails closed with a
  workspace drift report before command execution;
- `hideout env recreate <name>` refuses a running guest without `--force` and
  rebuilds under the same name with `--force`;
- `hideout run --rm -- <command>` leaves no reusable environment record;
- Gate 2 runs the real Lima named-environment lifecycle (`env list`, `run
  --env`, `env create`, `env recreate --force`, `--rm`) and verifies
  `hideout env list` reflects the expected records; `--new`, `--resume`, and
  the top-level `hideout list` are removed and are not exercised;
- Gate 2 runs real Lima `hideout stop <name>` and verifies the VM is stopped
  while the environment record remains resumable;
- Gate 2 covers `hideout stop --idle <duration>` and
  `hideout clean --stopped`/`hideout clean --idle <duration>` with temporary
  environments so idle memory release and destructive cleanup are not confused;
- successful reusable Lima runs keep target stdout/stderr clean by default;
  `--verbose` prints a name-based run-again hint on stderr without changing
  target stdout, while `--rm` runs do not print a reusable environment hint;
- every run, including resumed runs, gets a fresh session ID, broker token,
  command proxy shim directory, network plan, proxy secret runtime file, and
  audit context;
- stopped/stale environment cleanup preserves audit by default and never deletes
  the real workspace;
- `hideout env list` shows name, kind, image, backend, workspace, status, start/end
  times, and last command without leaking command secrets, broker tokens, proxy
  URLs, or raw machine IDs.

The Environment gate must run at least once with the Lima backend because the
primary value is preserving guest tool/cache state without preserving previous
runtime authority.

## Manager API Init And Run Acceptance

The minimal Manager API init and run surfaces are design-ready for TUI/WebUI and
automation integration. Required checks:

- `POST /api/v1/init/plan` returns the same `InitPlan` shape as Manager Core,
  accepts and validates a declarative base image reference (name plus digest)
  and expected-command diagnostics, includes structured next steps, and
  performs planning only;
- `POST /api/v1/init/apply` reaches `Core.ApplyInit`, uses typed init tasks
  rather than a raw profile writer, and fails closed for confirmation-required
  tasks because API v1 has no prompt channel;
- `POST /api/v1/run/plan` returns the same `RunPlan` shape as Manager Core and
  performs planning only;
- `POST /api/v1/run/apply` reaches `Core.ApplyRun` through a configured backend
  factory and returns `RunResult`;
- local Manager API `run/apply` receives host-open behavior through a configured
  opener factory so command proxies use the same isolated opener as CLI runs;
- `GET /api/v1/run/status` returns session summaries and rejects invalid session
  filters;
- responses do not expose broker tokens, broker socket paths, proxy secret
  values, raw helper search paths, or arbitrary host file contents;
- the local server binds only to `127.0.0.1`, enforces token and origin/host
  checks, and does not expose lab or host-control routes as API resources.

## HostFS Portal Acceptance

HostFS Portal is covered by the Lima Gate 2 smoke for the Linux guest FUSE data
plane. When that gate is run successfully, it proves that ordinary guest
filesystem APIs can access explicitly granted host paths without exposing
ungranted host filesystem state.

Required checks:

- outside explicit discover/content domains, `ls`, `cat`, Go `os.ReadFile`,
  Python `open()`, and Node `fs.readFileSync` when Node is present for a
  workspace-outside host path fail as missing and do not reveal whether the
  real host path exists;
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
- `read:` and `stat:` paths containing unescaped glob metacharacters (`*`, `?`,
  `[`) are treated as glob selectors using Go `filepath.Match` semantics, while
  paths without unescaped metacharacters remain exact-file selectors;
- glob matching denies case-variant bypasses on case-insensitive host
  filesystems, does not let `*` implicitly expose dotfiles, and supports
  backslash escaping for literal `*`, `?`, `[`, `]`, and backslash in CLI
  selectors;
- `see:`, `see-dir:`, `see-tree:`, `dir:`, and `tree:` reject glob selectors;
  new `list:` input is rejected with guided `migrate-list` recovery instead of
  being silently treated as a broader discover grant;
- glob read/stat grants allow matching files and filtered parent-directory
  listings, do not expose non-matching sibling names, and do not create backend
  passthrough mounts;
- glob deny rules win over matching allow rules and still audit the requested
  path plus matched deny rule ID/source;
- read-only HostFS grants do not permit write, create, delete, rename,
  truncate, chmod, chown, or xattr attempts;
- explicit HostFS overlay grants allow supported write-class operations
  (`create`, `replace`, `append`, `truncate`, `mkdir`, `delete`, `rename`,
  `chmod`, constrained `chown`) to stage durable overlay records while leaving
  host lower files unchanged before apply;
- HostFS write decisions are visible through Manager/CLI/WebUI/TUI surfaces,
  require one winning claim token, default to deny on timeout, and apply only
  after base snapshot revalidation succeeds;
- HostFS write apply conflict, stale claim, missing overlay grant, deny rule,
  reserved root, symlink swap, destination appearance, unsupported metadata, or
  privilege-requiring `chown` fails closed before host mutation;
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
  policy without introducing a second representation; `profile tools`
  converges on recording declared expected guest commands as diagnostics
  data, not installation actions; env list output reports names only and must
  not echo stored values;
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
- audit records first access, deny, directory listing, staged writes, claims,
  apply/discard/timeout/conflict/cleanup, and unsupported write-class attempts
  with the requested host path so the user can inspect what the target program
  probed. Audit records must still avoid raw broker tokens, claim tokens,
  overlay object paths, unrelated filenames, user-provided rule reasons, extra
  symlink target paths, and backend mount implementation paths. HostFS audit
  details include policy effect, safe policy reason, rule ID when matched,
  source, operation, requested path, `hostChanged=false` before apply, and
  `canonicalized=true` when the request passed through a host symlink;
- the guest daemon can be restarted without gaining broader authority;
- Linux guest FUSE adapter works under Lima on macOS and under a Linux host
  backend when that backend exists;
- Windows native HostFS remains skipped or marked Later until a dedicated
  resolver and mount adapter exist.
- Access Sensor remains optional Later work and is not required for HostFS
  grant enforcement or data access.

### HostFS Discoverable Namespace Gate 2 Inventory

029 adds local-fast policy/decision/redaction evidence, but only real Lima Gate
2 may promote guest namespace and same-session retry claims. The real run emits
and the wrapper machine-checks these 20 assertions:

1. Outside-domain lookup returns `ENOENT`.
2. A manually authored broad discover rule still force-hides categorized
   sensitive roots; direct lookup and directory enumeration both return
   `ENOENT`.
3. `see-dir` and `see-tree` return complete coarse names for their declared
   depths without real size/mode disclosure; a discover-denied node stays out of
   parent enumeration even when an exact content grant remains directly usable.
4. `see:` directory lookup succeeds while readdir returns `EACCES` and creates
   no decision.
5. Visible locked file read returns prompt `EACCES`.
6. Explicit read deny returns `EACCES` and creates no read decision.
7. The first eligible read creates one `hostfs.read` decision.
8. An equivalent retry reuses the decision without extending its timeout or
   revision.
9. A separate host CLI process claims and approves the exact-file decision.
10. The same still-running guest reads the expected content on its next retry,
    with no watcher or restart.
11. Real size and mode converge through the one-second FUSE attr bound while
    content authorization is immediate.
12. Deny and forced timeout remain denied; authenticated live-session reopen
    returns to pending without creating content authority.
13. Retargeting an approved symlink fails closed against the prior canonical
    grant.
14. A 4097-entry directory returns `EOVERFLOW`, never a partial successful
    listing.
15. Unauthorized write inside an explicit discover domain returns `EACCES` and
    creates no 029 read decision.
16. A legacy read-only profile without `see*` preserves its prior `EROFS`
    write behavior.
17. Protected-directory host prerequisite failure returns typed `EIO`, not an
    approvable content-lock error.
18. Reopen after session end fails closed and does not recreate the private
    provider directory or lock.
19. Audit and public decision evidence omit content, symlink target, claim or
    capability tokens, and private session-authority paths.
20. Existing 010 staged write, host-lower-before-apply, and authenticated apply
    assertions still pass.

`scripts/test-hostfs-visibility-e2e.sh --real-gate2` emits the two real proof
IDs only after all 20 markers pass and the Gate 2 log digest is recorded. When
real prerequisites are absent it emits only the supporting `not-run` proof.

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

How to run: `scripts/test-gate0.sh` executes `scripts/test-install-smoke.sh`
and `scripts/test-package-smoke.sh` on every invocation (including
`--quick`). The source-tree development install flow is
`scripts/install-local.sh`; the alpha package flow is
`scripts/package-local.sh` plus packaged `install.sh`, backed by
`hideout package install|verify|repair|uninstall`.

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
  and installed safe `doctor --fix --apply` writes current init metadata plus
  `doctor.fix.apply` audit;
- package smoke proves the release-like tarball can be unpacked, its artifact
  manifest checksums match the extracted files, and extracted
  template-aware `hideout init --template dev ... --no-input` plus
  `hideout doctor` run from the unpacked layout;
  package-root `install.sh` fails before copying binaries when the layout,
  helper, or manifest-declared checksum is broken;
- package smoke proves the installed prefix writes an installed-state manifest
  with the actual prefix and verifies without source checkout paths;
- package smoke proves compatible upgrade preserves durable store state,
  obsolete package-owned files are reported rather than silently left behind,
  explicit repair removes only proven obsolete package-owned files,
  incompatible migration fails before mutation, uninstall dry-run removes
  nothing, uninstall without purge preserves durable state, and `--purge` is
  required for durable state deletion;
- package smoke proves the v2.6.0 Linux `tun2socks` helper, provenance manifest,
  and upstream license are package-owned and checksum-covered; missing,
  damaged, wrong-target, explicit release override, and ambient-`PATH`
  substitutions fail closed;
- the existing TUI (`hideout tui --once`) and WebUI (`hideout ui --print-url`)
  render smokes remain in the package smoke as later MVP-ordered checks after
  the unpack, checksum, and init plus doctor proof;
- omitted or `auto` backend first-run repair resolves to Lima, matching
  `hideout run`, and plans Linux helper repair when store helpers are missing;
- `hideout init --template dev --profile native-dev --backend native --network direct --no-input` succeeds without
  using arbitrary shell scripts as an explicit weak-isolation smoke;
- Manager API run status tests must cover a real `run/apply` session, not only
  an empty store, and must prove status exposes only session summaries and
  presence booleans for broker/proxy artifacts;
- typed init repair is idempotent and does not rotate identity, enable bundles, add
  HostFS grants, create passthrough mounts, open host apps, create PortBridge, or
  change network routes;
- `hideout doctor` reports core checks after init;
- `hideout doctor --fix --dry-run` shows safe fixes without executing unsafe
  tasks;
- init planning accepts and validates a declarative base image reference (name
  plus digest) without product-specific Core logic or raw profile edits;
- `hideout init --template privacy --network tun2socks --proxy-secret <ref> --mediated-resolver <ip>` and Manager `init/apply` persist only the proxy secret ref and resolver IP, not a proxy URL or backing env var name;
- `hideout doctor --fix --dry-run --backend lima` includes
  `helper.install.linux-shim`, `helper.install.linux-hostfsd`, and
  `helper.install.linux-session-supervisor` when the official store helpers are
  missing and a source-tree repair is available;
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

- mounts are explicit: no path outside the workspace is mounted unless the user
  declares it, and high-risk roots require the explicit unsafe/high-risk
  override;
- the safety guard rejects the host home, the effective Hideout store root,
  credential roots, browser profile roots, and symlinks or parent directories
  that would mount them, while allowing ordinary project directories under the
  host home;
- the declared mode is enforced (`rw` changes are visible on the host, `ro`
  rejects guest writes) and every mount is visible in `explain`, audit, and UI
  with host path, guest path, mode, and high-risk classification;
- HostFS grants and backend mounts never create each other, and cleanup never
  deletes the mounted host path.

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

`scripts/test-release-dogfood.sh` always writes a release evidence bundle under
`.hideout-release-evidence/` by default; `HIDEOUT_RELEASE_EVIDENCE_DIR` or
`HIDEOUT_RELEASE_EVIDENCE_ROOT` override the location. The bundle contains a
`manifest.json` conforming to `schemas/release-dogfood.schema.json`, the
release-like tarball built from the same worktree before gates run, and
`test-release-dogfood.log` with redacted gate output. The manifest records
`operatorProxy.url` as `redacted`, and the log must not contain the raw
`HIDEOUT_SECRET_DEFAULT_PROXY` value: keeping raw proxy URLs out of evidence is
a flow obligation on the Hideout-managed proxy secret flow. Gate 0 verifies the
recorded release artifact exists in the evidence directory and its SHA-256
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

- `scripts/test-ordinary-user-release.sh --release-candidate` binds the exact
  package to clean-install, real Gate 2 first result, real Gate 3 privacy,
  fully executed UI, signing, notarization, support/report, lifecycle, and docs
  evidence;
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
  `scripts/test-phase1.sh --release-candidate`, using the package-owned helper;
- Gate 4 passes in dry-run automation, and passes once with a real supported
  isolated browser launcher for the external URL case;
- all Capability Probe code remains unreachable from default `hideout run`;
- all known failures are either fixed or explicitly reclassified in the design
  document.

Until these gates pass, the project remains an advanced prototype rather than a
Phase 1 release candidate.

## Gate 031: Supported CLI Runtime Preview

Gate 0 validates the catalog and contract parsers, immutable selection,
profile/environment provenance, probe-only verify plan/apply, status parity,
typed recovery, package ownership, zero effective-authority delta, receipt and
public-evidence redaction, and false-green fixtures. It cannot establish a real
runtime claim.

Real acceptance requires one retained digest-addressed macOS arm64/Linux
aarch64 artifact and clean image-build source state. The enclosing evidence
separately binds the clean verified Hideout package candidate. The complete existing Gate 2
must run with guest package preparation disabled and prove the baseline,
non-root/no-sudo boundary, HostFS visibility/read/write, projection, lifecycle,
and cleanup. Gate 3 must use the same catalog revision and digest, then prove
DoH forwarding, connected-subnet resolver blocking, pinned real-agent registry
HTTPS, target ownership, and zero credential leakage.

Every passing real gate emits a typed runtime binding. Product evidence and
release readiness fail closed for absent fields, a native/local producer,
wrong revision/digest/architecture/environment identity, dirty or stale
candidate state, failed proof, missing artifact, or artifact digest mismatch.
The retained preview asset and both real gates now exist. Final completion still
requires product evidence generated by the clean verified package candidate;
development receipts from a dirty package checkout cannot satisfy that
freshness requirement.

## Gate 032: Community Host-App Recipes

Status: **Implemented.** The three artifact-backed Gate 0 proofs and a clean
exact-package external-pack real macOS arm64 Lima Gate 2 proof are current.

### Gate 0 Topology

`scripts/test-host-app-pack-smoke.sh` is the 032 Gate 0 entrypoint. Targeted
completion requires one same-commit, artifact-backed evidence manifest with all
three registered proofs:

- `032.host-app-pack.gate0.lifecycle`: local and exact-commit snapshot intake,
  read-only inspect/validate, advisory package tests, confirmed atomic add,
  exact enable, update permission diff, disable/remove/revoke, profile scope,
  future-run-only effect, CLI/Manager parity, and sanitized untrusted text;
- `032.host-app-pack.gate0.binding`: immutable command ownership, strict intent,
  Core-derived app/resource/provider facts, HostFS existing-authority checks,
  run-scoped decision identity, disable/revoke invalidation, and no host/guest
  fallback;
- `032.host-app-pack.gate0.identity-safety`: safe application roots and
  executable containment, Core-observed signing identity, exact-digest
  `unverified-app` posture, Core-owned safety profiles, permission fingerprint,
  conflicts, and public-evidence redaction.

The smoke must fail if a required proof is absent, lacks a retained artifact,
has a mismatched digest, covers the wrong commit, or substitutes a package test
for a Core invariant. `scripts/test-doc-truth-smoke.sh` separately requires all
four 032 registry IDs in `docs/claim-boundaries.md` and rejects known community
pack overclaims. Docs truth is documentation evidence, not lifecycle or host
effect evidence.

### Community Host-App Real macOS Arm64 Lima Gate 2

The 032-owned wrapper is
`scripts/test-host-app-pack-e2e.sh --real-gate2 --require-real --out <dir>`.
It reuses low-level 030 projection helpers without altering or claiming the
existing 030 proof IDs.

The wrapper installs an external local pack without rebuilding Hideout and
emits `032.host-app-pack.real-gate2.external` only after one retained real run
proves all of the following:

1. Built-in `code` and the external command use the same generic resolver,
   renderer, provider, binding, decision, inspection, and audit paths.
2. Workspace and already-authorized HostFS content open successfully without a
   guest, recipe, decision preview, response, or public artifact receiving the
   resolved host path.
3. HostFS see-only, ungranted, stale, ended, denied, retargeted, and mismatched
   portal resources fail before host effect.
4. A compatible signed app can use the Core-owned safe profile; an elevated or
   explicitly trusted unverified app uses the exact `ask-each-run` scope.
5. Unsafe roots, writable ancestors, owner mismatch, signing/digest drift, and
   package self-attestation fail closed.
6. Enabling changes only the next run. The old session receives no shim; the
   new run receives exactly the enabled command set.
7. Disable/revoke/remove, conflict, missing provider, and forged binding facts
   never fall through to generic host execution or a shadowed guest binary.
8. Public evidence excludes host username/path, executable path, raw argv,
   mutable source internals, repository credentials, decision tokens, and
   provider-private state.

The real proof requires an external pack, a real Lima guest, the real supported
host app, and observed host effect. Native runs, local-fast tests, embedded-only
recipes, static source checks, package self-tests, and `not-run` records are
false-green fixtures and cannot satisfy it. The clean exact-package proof is
retained with the 043 readiness and 030/039 flow artifacts under
`.hideout-release-evidence/043-projection-readiness-real-gate2/`.

## Gate 034: Concurrent Run Sessions

Status: **Implemented; real proof remains commit- and runtime-bound.** Gate 0
proves local ownership mechanics only. It cannot establish Linux namespace,
ordinary-target isolation, or performance claims.

### 034 Gate 0

`scripts/test-concurrent-sessions-smoke.sh` runs from `scripts/test-gate0.sh`.
It validates strict owner, activation, and environment-service schemas; owner
flock reconciliation; transition-lock races; per-session runtime and HostFS
state; Manager/CLI status parity; explicit-stop refusal; and a native mechanics
fixture with two overlapping commands. That native fixture exercises 034
mechanics only and retains its environment after the final owner exits; it is
neither isolation nor 036 automatic-stop evidence.

`scripts/test-doc-truth-smoke.sh` requires the registered 034 and 036 proof IDs
in `docs/claim-boundaries.md`. Its 034 negative fixtures reject cross-workspace
shared VM support, treating 034 local smoke as final-session-stop proof,
exhaustive terminal-emulator/theme/OSC coverage, guest-root containment, and
native/local substitution for the real Lima gate. Dynamic `SIGWINCH` resize is
implemented and belongs to the positive 034 contract; broader
terminal-emulator hardening remains outside that feature.

### Concurrent Session Real macOS Arm64 Lima Gate 2

Run:

```sh
scripts/test-concurrent-sessions-e2e.sh \
  --real-gate2 \
  --require-real \
  --samples 30 \
  --out .hideout-release-evidence/034-concurrent-sessions
```

The isolation lane must prove three overlapping same-workspace owners share one existing
Lima instance while ordinary targets receive distinct session IDs, private
`/proc`, private runtime children, independent streams, and session-local
HostFS authority, with one full activation plus two warm attaches. It must also
prove a live owner blocks explicit stop, killing
one host owner leaves a sibling and the environment service alive, the last
exit in the pre-036 034 candidate leaves the VM warm, explicit idle stop
succeeds, and guest root can see both ordinary targets as a positive control
for the documented non-claim. Current final-session-stop behavior is proved
only by Gate 036 below. A real PTY lane must additionally prove initial
dimensions, a live `SIGWINCH`
resize, representative full-screen control bytes, Ctrl-C exit status 130,
terminal restoration after daemon loss, client unblocking, target reaping,
restart refusal for unproved owners, explicit recovery, and a successful run
after recovery.
The retained `result.json` is not accepted because it merely exists or says
`passed`: the Go evidence evaluator requires the exact schema/platform/clean
commit identity, all 26 named checks set to true, no extra or missing check,
`guestRootContainment=not-claimed`, and the retained `session-pty.json` digest.

The performance lane keeps one candidate owner live and measures at least 30
independent second-owner invocations against a representative attached
workspace. A host monotonic clock starts before the CLI invocation and stops
only after the target's first `READY` line is observable; acceptance requires
nearest-rank p95 at most 2.0 seconds. Evidence records the exact clean commit,
host, runtime binding, environment/instance identity, fixture digest, raw
samples, median, p95, and the named measurement boundary. The semantic
evaluator independently requires the exact v2 boundary and runtime identity,
at least 30 samples, and recomputes the reported statistics from the retained
array. Workspace filesystem Git/package performance is deliberately not
re-judged here: feature 035 owns the mandatory same-VM alternating paired
Portal/static-VirtioFS gate and its metric-specific thresholds.

### 034 Evidence Refusal

The wrapper emits `hideout.product-hardening-evidence/v1` under the ignored
evidence root. `034.concurrent-sessions.real-gate2.isolation` and
`034.concurrent-sessions.real-gate2.performance` require `mode=real-gate`,
their exact registered evidence classes, retained artifact digests, the exact
candidate commit, a clean verified package candidate at release evaluation,
and the exact promoted runtime binding. A supporting
`034.concurrent-sessions.real-gate2.not-run` record is valid diagnostic evidence
but can never satisfy release readiness.

Evaluator and readiness tests must reject each of these independently:

- a missing isolation or performance proof;
- `status=not-run`, native, local-fast, or synthetic evidence in a real slot;
- dirty or stale source/package identity;
- wrong runtime family, revision, artifact digest, architecture, build commit,
  or environment identity;
- a missing retained artifact, digest mismatch, or path escape; and
- documentation that promotes any 034 non-claim to a supported behavior.

## Gate 035: Shared Default VM Across Workspaces

Status: **Implemented and promoted from clean real macOS arm64 Lima behavior
and performance evidence.** Gate 0 proves deterministic domain contracts only.
It cannot establish a live Workspace Portal filesystem,
cross-workspace ordinary-target isolation, host permission behavior, observed
VM reuse/stop, or performance.

### 035 Gate 0

`scripts/test-shared-workspace-smoke.sh` runs from `scripts/test-gate0.sh`. The
local lane must prove all of the following without source-text-only assertions:

1. one stable automatic slot excludes workspace/session facts and rejects
   machine drift without creating a hidden second VM;
2. shared, dedicated, workspace-bound, and disposable record invariants are
   distinct, with no old-record dual reader;
3. one private store-keyed workspace ID drives root identity, attachment,
   broker/projection mapping, lifecycle, status, events, and evidence;
4. traversal, symlink/rename/root-replacement races, reserved roots, guessed
   sibling IDs, malformed protocol frames, stale credentials, and provider
   overload fail closed;
5. disjoint, nested, ancestor/descendant, and same-root relations retain their
   declared authority and reference-count behavior;
6. filesystem operation, lock/handle, direct-write, watcher/cache, crash, and
   ambiguous-cleanup fixtures match the accepted Workspace Portal matrix;
7. provider/view/service resources commit before effects, activate only after
   authenticated supervisor readiness, and release dependent-first through the
   existing 036 coordinator;
8. daemon restart never re-adopts attachment authority, and attach/stop races
   cannot bypass an unproved incarnation;
9. shared network service state is reference-counted while run secrets remain
   session scoped, and HostFS overlay behavior is unchanged;
10. Manager, CLI, TUI, WebUI, doctor, audit, event, schema, recovery, lifecycle,
    and product-evidence contracts agree on one machine plus distinct views and
    contain no raw host root, workspace key, token, or broker secret; and
11. logical `/workspace`, opaque physical project identity, exact Git
    safe-directory forms, and attachment-bound host projection remain aligned.

The lane emits `035.shared-workspace.gate0.mechanics` and
`035.shared-workspace.docs.claim-boundary`. Passing them alone does not
establish the real cross-workspace claim.

### 035 Real macOS Arm64 Lima Gate 2

Run the release-shaped installed-package wrapper on the exact candidate:

```sh
scripts/test-shared-workspace-lima-e2e.sh \
  --require-real \
  --samples 30 \
  --out .hideout-release-evidence/035-shared-workspace
```

Correctness and path identity are a separate retained stage and do not require
a quiet host or any performance sample:

```sh
scripts/test-shared-workspace-lima-e2e.sh \
  --require-real \
  --non-performance \
  --out .hideout-release-evidence/035-shared-workspace-path
```

This stage uses the installed candidate and packaged Portal helper. Two
concurrent real sessions must prove that logical `/workspace` and the exact
production-shaped physical attachment are the same device/inode; exercise
bidirectional create/read/write/rename/chmod/fsync/delete through both aliases;
retain stable same-root and distinct different-root representative agent state;
deny a sibling physical root; reject shared preserve mode and external Git
metadata before target start; keep Git safe-directory inputs bounded; and show
logical file-path projection without leaking the physical root. It also records
the honest partial-coverage boundaries: relative workspace paths from
path-oriented file hooks are explicitly `aliased`, unavailable process cwd is
tagged `cwd-unavailable`, and physical or sibling argv paths that exceed the
kernel capture width fail closed to the
fixed unbound placeholder with `argv-truncated`, or are omitted with explicit
`argv-unavailable`. Its public summary contains booleans, tool classes, and
fixed limitation identifiers, not the physical root. A retained
divergent-inode negative fixture must fail for that exact expected check; an
incomplete or ambiguously failing fixture is rejected. Passing this stage can
satisfy only the registered 035
behavior proof; it neither creates nor substitutes for the performance proof.
The full wrapper writes and validates this behavior-only manifest before it
starts performance work, so a later measurement or harness failure leaves the
correctness checkpoint intact and the exact package archive can be reused.

Resume only the independent performance stage with the same output directory:

```sh
scripts/test-shared-workspace-lima-e2e.sh \
  --require-real \
  --performance-only \
  --samples 30 \
  --out .hideout-release-evidence/035-shared-workspace-path
```

Resume first validates the retained manifest schema, every behavior artifact
digest, source commit, and exact package identity. It may create a fresh
isolated gate environment, so the environment ID can differ while the immutable
runtime artifact binding must remain equal. A failed performance attempt is
moved under `reports/failed-performance-*` and cannot overwrite the behavior
checkpoint.

The tool matrix records behavior instead of assuming every tool treats cwd the
same way. Bash `pwd -P`, Git, Node and Python use the opaque physical cwd in the
current session view. Go may trust the same-inode logical `$PWD`, so a logical
`go env GOMOD` is classified as `logical-pwd-alias` and carries no fabricated
distinct state claim. Claude and Codex are representative project-state
fixtures unless an explicitly version-bound authenticated dogfood run is also
retained.

The retained behavior artifact must prove one automatic environment, one Lima
instance, and one observed boot serving simultaneous disjoint projects through
the packaged Workspace Portal. Each target sees only its own marker at logical
`/workspace`; same/nested/disjoint roots match their declared relation; host and
guest marker mutations, same-root lock ownership, sibling detach, lifecycle
pins, preview-bridge continuity, and post-restart authority all use the correct
project. Watcher health and performance distributions are not asserted by the
behavior-only checkpoint. Shared Lima YAML and pre-attach mount state contain
no project, parent, home, dummy mount, or hidden fallback transport.

The same topology must prove provider/view lifecycle registration, a real
bridge pin, exact 036 grace/stop, grace cancellation by a new project, daemon
loss and explicit recovery, typed root/TCC/metadata failures, bounded overload,
browser and PTY machine/view rendering, project-state separation for
representative shell/Git/language/agent tools, and absence of injected host or
control sentinels from guest/public artifacts. Guest root is a positive-control
non-claim, never a passing isolation assertion.

The performance artifact uses fixed 10,000-entry Git and 20,000-operation
package fixtures. One target process in one shared VM warms both the Portal
candidate and the profile cache's static virtiofs control, then alternates their
order for at least 30 paired samples. The artifact retains the interleaved
records and both extracted distributions; the semantic evaluator re-derives and
cross-checks them. This controls host load, VM scheduling, runtime, cache window,
and sample order without weakening any threshold. The accepted research
baseline remains the transport-selection provenance and warm first-target-byte
reference only. Acceptance requires:

- the real Portal target has Core-owned process configuration
  `core.preloadIndex=false`, with no persistent Git config mutation or wildcard
  workspace trust; and

- Git status median at most 2.0 seconds and at most 2x static-virtiofs median;
- package fixture median at most 3x static-virtiofs median;
- host/target atomic-save visibility p95 at most 250 ms;
- mounted-ready p95 at most 1 second; and
- warm first-target-byte p95 no greater than retained research baseline p95 plus
  `max(500 ms, 15% of baseline p95)`.

`035.shared-workspace.real-gate2.behavior` and
`035.shared-workspace.real-gate2.performance` require the exact clean commit,
package/runtime/fixture identities, evidence classes, retained artifact paths,
and matching digests. `035.shared-workspace.real-gate2.not-run`, native,
source-tree helpers, local-fast output, missing artifacts, edited summaries,
dirty/stale provenance, threshold relaxation, or a different transport cannot
satisfy promotion.

The retained clean candidate at commit
`1584e18f6eb945b69cebe60851b5d3f3bfbfccd8` passed both real proofs. Its
product-evidence manifest SHA-256 is
`e4ac83a0c8f3d489ac931479d87e666c24c39ea974a110693a46dd6c39564f13`, and
the verified package archive SHA-256 is
`7f6a6e7298163188b48e49747d23941e27a54858019c66b3b8b9038387af886b`.
The 30-sample `git status` median was `49.757 ms` versus `62.208 ms` for
the paired static-virtiofs control. Warm first-target-byte p95 was
`780.694 ms` versus the retained research baseline of `910.457 ms`.

## Gate 036: Resource Lifecycle And Final-Session Stop

Status: **Implemented and promoted from clean real macOS arm64 Lima proof.**
Gate 0 proves the closed resource catalog, transition model, journal, status
parity, fail-closed reconciliation, and redaction. It cannot establish an
observed VM stop or user-command performance.

### Gate 0 And Race Lanes

```sh
scripts/test-lifecycle-smoke.sh
go test -race ./internal/lifecycle ./internal/decision ./internal/daemon ./internal/manager
```

The local lane emits `036.lifecycle.gate0.mechanics` and
`036.lifecycle.gate0.model-replay`. Exhaustive and randomized replay must cover
attach, release, drain, grace, stop, shutdown, restart, generation fencing,
failed cleanup, and corrupt/partial journal states. A lifecycle backend factory
alone must not enable side effects in an alternate daemon composition.

### 040 Real macOS Arm64 Lima Gate 2

```sh
HIDEOUT_036_SHORT_TMPDIR=/tmp \
  scripts/test-lifecycle-lima-e2e.sh --all --require-real \
  --samples 30 --warmups 3 --iterations 100 \
  --out .hideout-release-evidence/036-resource-lifecycle
```

The real lane is one ordered topology. It proves sibling preservation, the
run-bridge pin and close, retained guest disk/profile cache/audit/staged
overlay, independent host-app handoff, PTY-owner crash recovery, 100 real
attach/stop races, new boot generation, stale-owner explicit recovery,
ambiguous-stop refusal, bounded daemon status/shutdown, and exact observed
non-destructive stop after the final pin/drain and 15-second grace. Unknown
inventory, orphaned ownership, failed cleanup, and boot change must never
publish stopped or cross into automatic stop.

The historical 036 performance sub-lane built the exact pre-036 commit and
036 candidate in separate trees, then alternated at least 30 paired samples of
the literal user-visible command `hideout run -- git status --short` on the
same host, runtime artifact, and fixture. Candidate median overhead could
exceed baseline by at most 5% or 10 ms, whichever was larger. Raw arrays,
fixture digest, runtime identity, clean commit identities, and recomputed
medians were retained.

The promoted proof is bound to commit
`0fe099f20e354d5d52187d9c8d5406a367d19d52`, runtime
`developer-standard/2026.07.0` artifact SHA-256
`79e5d25bfd05c27b4ee7f2ad085d45c15a63aadbe2ab8d1b4ba2c426e1586134`,
and evidence manifest SHA-256
`b050dfc5c9230bed526132339e7a707a325d352b6ed6baf37c07af09910029fc`.
All 20 lifecycle checks passed; the 30-sample candidate median was 170.511 ms
against 271.180 ms for pre-036 commit
`127ef937b120f0faa719611abcb3a1816e331266`.

### 036 Evidence Refusal

`036.lifecycle.real-gate2.lifecycle` remains fresh exact-package release
evidence. `036.lifecycle.real-gate2.performance` is validator-backed supporting
historical evidence for the feature-specific registry-migration delta proved
at `0fe099f`; later Portal, attach-reservation, and projection-readiness costs
are owned by their current 035, 040, and 043 gates rather than reassigned to
036. `036.lifecycle.real-gate2.not-run`, native, local-fast, a reduced probe,
command success without backend observation, or an edited `passed` summary
cannot satisfy the lifecycle claim. Documentation must retain the non-claims:
automatic stop does not clean/delete retained state, terminate independent host
apps, preserve detached run bridges, provide guest-root containment, or guess
through unknown ownership/backend state.

## Gate 040: Lifecycle Attach Reservation

Status: **Implemented and promoted from clean real macOS arm64 Lima evidence,
in addition to passing local mechanics, model, mutation, randomized-schedule,
cancellation, restart, schema, and redaction evidence.**

### Gate 0, Model, Mutation, And Race Lanes

```sh
go test -count=1 ./internal/session ./internal/lifecycle ./internal/manager ./internal/daemon
go test -race -count=1 ./internal/lifecycle ./internal/manager ./internal/daemon
scripts/test-lifecycle-smoke.sh \
  --out .hideout-release-evidence/040-attach-reservation-gate0
```

The local lane emits `040.attach-reservation.gate0.mechanics` and
`040.attach-reservation.gate0.model`. It must prove allocation without runtime
publication, reconciliation-first waiting, reservation-first exclusion,
record/backend revalidation, durable owner before promotion, atomic promotion,
session-scoped cancellation, restart without reservation re-adoption, redacted
status/events, and at least 1,000 deterministic seeded schedules. TLC checks
`formal/AttachReservation.tla`; temporarily allowing reconciliation to start
with a held reservation must reproduce `EstablishingRuntimeIntact` failure.
The clean local Gate 0 manifest for candidate
`3555c9a9aa83c885c3c8ee29f1d015ee10c1fe73` has SHA-256
`a0e9dadba70a89ce52e8d5255380ade950ce8e4d8ccb5e7ad96a9cdc21d94d6b`.

### Real macOS Arm64 Lima Gate 2

```sh
HIDEOUT_036_SHORT_TMPDIR=/tmp \
  scripts/test-lifecycle-lima-e2e.sh --all --require-real --feature-040 \
  --samples 30 --warmups 3 --iterations 100 \
  --out .hideout-release-evidence/040-attach-reservation-real-gate2
```

The real lane emits `040.attach-reservation.real-gate2.lifecycle` and
`040.attach-reservation.real-gate2.performance`. It requires the exact clean
candidate and runtime identity, reconciliation-first and reservation-first
orders, cancellation before owner publication, restart before owner, existing
post-owner restart fail-closed recovery, sibling preservation, unknown-stop
refusal, 30 measured warm samples, nearest-rank p95 at most 2.0 seconds, and at
least 95% of samples within 2.0 seconds.

The promoted proof is bound to clean candidate commit
`3555c9a9aa83c885c3c8ee29f1d015ee10c1fe73`, exact pre-040 baseline
`322c3c6cc9561eea21d4ed20ab78172429654c54`, and runtime
`developer-standard/2026.07.0` artifact SHA-256
`79e5d25bfd05c27b4ee7f2ad085d45c15a63aadbe2ab8d1b4ba2c426e1586134`.
All 24 lifecycle checks and 100 attach/stop races passed. All 30 measured warm
samples were within two seconds; candidate median/p95 were 413.921/538.581 ms
against a 408.800 ms baseline median. The evidence manifest SHA-256 is
`5394bfbd78804b5c2d1861406861584a5573e25ec7a58edb4f044c2b40fccefb`.

### 040 Evidence Refusal

`040.attach-reservation.real-gate2.not-run`, dirty-source evidence, a reduced
probe, native/local behavior, the pre-040 036 result, or command success without
the registered lifecycle/performance evidence classes cannot satisfy the real
claim. Gate 040 adds no CLI, configuration, manifest field, guest authority, or
fallback path; it does not strengthen shared-VM isolation or guest-root
containment.

## Gate 041: Shared Workspace Portal Executable Support

Status: **Implemented and promoted from clean exact-package real macOS arm64
Lima evidence.**

### Gate 0 And Mutation Lane

```sh
scripts/test-workspace-executable-smoke.sh
scripts/test-gate0.sh
```

The local lane proves that an allowed local-only open hint does not change the
Portal wire request, an unknown hint still returns `ENOTSUP`, Linux arm64 binds
the rule to go-fuse `FMODE_EXEC`, the current research probe constructs required
admission identity, and the 041 proof registry covers every FR/SC. Its strict
Go evidence judge must reject dirty identity, mechanism drift, missing or false
checks, fewer than 30 samples, p95 over two seconds, median regression over ten
percent, an overclaimed static virtiofs mode, and unknown JSON fields.

Temporarily removing `FMODE_EXEC` from the Linux local allowlist must make the
focused real Portal direct-execution lane fail with `OPEN {EXEC,0x20000}` and
`EOPNOTSUPP`; restoring it must run both an interpreted script and a Linux arm64
binary without adding a wire flag.

### Focused Portal Correctness

```sh
scripts/test-workspace-portal-lima.sh \
  /tmp/hideout-041-workspace-portal-correctness
```

This is real macOS arm64 Lima transport evidence for direct script/binary opens,
ordinary filesystem effects, cache invalidation, escaping symlink refusal, and
lock behavior. It is valuable diagnosis but uses a research helper and cannot
by itself promote packaged product support.

### Clean Product Gate 2

```sh
scripts/test-workspace-executable-lima-e2e.sh --require-real \
  --samples 30 --iterations 100 \
  --out .hideout-release-evidence/041-workspace-executable-real-gate2
scripts/test-gate2-lima.sh
```

The feature gate builds or consumes one verified package, initializes the exact
`developer-standard` runtime, and directly executes a workspace script, Linux
arm64 binary, and relative launcher. It proves checkout write/later-session
visibility, permission/missing-interpreter/incompatible-format preservation,
escaping-link refusal, no host fallback, no copied workspace, and 100 rapidly
repeated executions split across two disjoint workspaces. Thirty alternating
direct/control samples require nearest-rank p95 at most two seconds and direct
median at most 1.10 times the same script invoked through guest `/bin/sh`.

`041.workspace-executable.real-gate2.execution` requires a clean 40-character
candidate commit, exact package and runtime binding, macOS arm64/Lima/aarch64,
`workspace-portal`, a closed all-true check inventory, thresholds, redaction,
and `staticVirtiofs: not-claimed`. The supporting `not-run` proof, native/local
tests, a dirty or reduced probe, the focused research probe, helper execution
from `/tmp`, or edited `passed` JSON cannot satisfy promotion. The legacy
aggregate Gate 2 intentionally copies its helper because that lane uses static
virtiofs; it remains a regression gate and a positive control for the explicit
non-claim, not workspace-execution evidence.

The retained clean candidate is
`1182fa10bec965cbdecb714faf6f7f9b587221e6`. All 13 closed behavior checks and
100 executions across two disjoint workspaces passed. Thirty alternating
samples measured 980.621 ms warm first-output p95 and a 0.983 direct/control
median ratio. The product manifest SHA-256 is
`c84bdaa2a42e16b1bb3e8159d2fcc180590dcbcd3ce7543af70ca1bb8cd9159f`; the
workspace artifact SHA-256 is
`ecf8a35f4ce570c02edf65f75a8e0e6eca4f8628996db35f0d731ea12c86cfc7`;
the exact package SHA-256 is
`c68b0c4eb9c07970527e40f106032684a16727bd9671de56fc174d156771885b`.
The focused Portal probe, integrated Lima Gate 2, and full Gate 0 also passed on
the same source candidate.

## Gate 042: Disposable Orphan Recovery

Status: **Promoted with clean exact-package real macOS arm64 Lima evidence.**
Reduced real probes remain diagnostic evidence only.

### 042 Gate 0, Model, Mutation, And Race Lanes

```sh
scripts/test-disposable-recovery-smoke.sh
go test -race -count=1 ./internal/lifecycle ./internal/manager ./internal/daemon
scripts/test-gate0.sh
```

Gate 0 proves canonical disposable identity, strict intent schema and
forward-only phases, ordinary-finalizer/restart protocol sharing, stable
two-sample absence, record-last convergence, intent-only recovery, bounded
startup workers, redacted status/audit, target-failure disposition, and
`--rm --ephemeral` semantics. At least 100 seeded crash/interleaving schedules
must converge to exact removal or a stable cleanup-required outcome without
touching a sibling.

TLC checks `formal/DisposableRecovery.tla` across authorization, every durable
cut, absence proof, metadata ordering, and blocked outcomes. The mutation lane
must turn red when disposable authorization, live-owner refusal, exact
record/instance matching, the second absence sample, or journal-before-record
ordering is removed. The strict evidence judge rejects unknown fields, dirty
identity, any missing/false closed check, fewer than 30 ordinary runs, nonzero
unauthorized destructive calls, residue, threshold violations, and absent
redaction proof. Gate 0 establishes protocol mechanics, not real Lima cleanup.

### Clean Product macOS Arm64 Lima Gate 2

```sh
scripts/test-disposable-recovery-lima-e2e.sh --require-real \
  --runs 30 --checkpoints 4 \
  --out .hideout-release-evidence/042-disposable-orphan-recovery-real-gate2
scripts/test-gate2-lima.sh
```

The feature producer builds or consumes one verified package from an exact
clean source commit, uses its bound `developer-standard` runtime, and requires
Darwin/arm64, Lima, and an aarch64 guest. Thirty ordinary disposable runs must
include successful target execution, exact target exit 23 with proved removal,
and `--rm --ephemeral`; every run must leave zero exact Lima instance,
environment record, runtime/owner/identity directory, gateway receipt,
lifecycle journal, or coordinator status.

The producer forces actual daemon process death and restart at four ordered
durable cuts: after intent, after stable absence/backend cleanup, during
metadata cleaning, and after journal removal before record removal. Before
restart it retains the expected residue shape; after restart it requires exact
stable absence and zero residue. It also carries the 100 local schedules and
the non-disposable, live/unprovable-owner, identity mismatch, unknown
observation, unstable-absence, malformed-intent, status-only, and legacy
journal-only refusal proofs. Authorized destructive calls must match the
ordinary plus crash inventory; unauthorized destructive calls must be zero.
Daemon status p95 must remain at most 10 seconds and recovery p95 at most 60
seconds, with no timeout.

`042.disposable-recovery.real-gate2.recovery` is satisfied only by the strict
closed artifact and retained `hideout.product-hardening-evidence/v1` manifest
whose source, package, runtime, platform, artifact digest, and redaction facts
all pass the production evaluator. `--probe` never emits product proof.
`042.disposable-recovery.real-gate2.not-run`, native/local execution, dirty
source, fewer runs/checkpoints, command success without inventory observation,
or an edited passed summary cannot satisfy promotion. Gate 042 adds no generic
orphan deletion, non-disposable recovery, new CLI/configuration, target
authority, workspace copy, backend fallback, or isolation claim.

The retained clean candidate is
`666cfa827646bbc6b0d3d9a86b4f5091b83b5dd3`, with exact package SHA-256
`9f5ba6d168471dbbc1bd6fbdf85809483e4bc1ec1685cd5860427f75ad8d78cd`
and runtime `developer-standard/2026.07.0`. Thirty ordinary runs, four actual
daemon kill/restart cuts, 100 local schedules, and all 24 closed checks passed.
The evaluator observed 34 authorized destructive calls, zero unauthorized
calls, zero retained residue, zero timeouts, daemon status p95 `74.320 ms`, and
recovery p95 `498.989 ms`. The product manifest SHA-256 is
`fc1cabf8eb645433145b72ade45175821c3097792900729c1e9c9231e3dccd16`;
the recovery artifact SHA-256 is
`22b708b4560a6ab454c357f4a99f12a921877f758a01774da09cbe1c543e241f`;
and the bound runtime artifact SHA-256 is
`79e5d25bfd05c27b4ee7f2ad085d45c15a63aadbe2ab8d1b4ba2c426e1586134`.

## Gate 045: Operator Observability Console

Status: **implementation gates active; exact release-candidate promotion
pending.**

Feature 045 is accepted only when one clean source identity, one immutable
package archive, and one installed candidate satisfy every lane below. Passing
development evidence from a dirty tree, or a disposable clean implementation
snapshot that is no longer the main candidate, validates the gate
implementation but does not satisfy final release acceptance.

### 045 Required order

Run the source and adversarial gates before packaging:

```sh
HIDEOUT_BPF_CLANG=/opt/homebrew/opt/llvm@19/bin/clang \
HIDEOUT_BPF_LLVM_STRIP=/opt/homebrew/opt/llvm@19/bin/llvm-strip \
  scripts/gates/generated.sh
scripts/mutation/045/run-negative-fixtures.sh
scripts/gates/release-candidate.sh
scripts/gates/formal.sh
scripts/gates/release-candidate-privacy.sh
scripts/gates/release-candidate-ui.sh
HIDEOUT_PERFORMANCE_QUIET_HOST_CONFIRMED=1 \
  scripts/gates/release-candidate-performance.sh
scripts/gates/release-candidate-lima.sh
```

Then build once and test only those bytes:

```sh
scripts/release/build-candidate.sh
scripts/release/test-package-lifecycle.sh
scripts/release/collect-evidence.sh
scripts/release/install-local-candidate.sh --yes-discard-legacy-data
scripts/release/verify-publication-absence.sh
scripts/release/collect-evidence.sh --require-closure
```

The T163–T165 closure tools are active. The first collection binds the exact
package and records `package-bound`; it is not readiness. T164 installs the
exact archive on this machine and repeats the documented setup, secret,
connection, run, Help, TUI, WebUI, cleanup, update, uninstall, and final
reinstall journeys. T165 read-only observations prove that no matching remote
tag, GitHub Release, Homebrew formula change, or supported-channel package
publication occurred. Only the final `--require-closure` collection may emit
`final-ready`. T171 reruns the complete sequence from the final clean tree and
rejects any required `stale`, `reduced`, `not-run`, or `unsupported` result.

### Gate execution and rerun review

Every gate attempt must end with a short execution review, including failed
preflights. The review records:

1. the immutable candidate/tree and whether the attempt started from scratch,
   an authenticated checkpoint, or a same-candidate retry;
2. every reused result or artifact and the exact binding that made reuse safe;
3. elapsed time, completed stages, VM boots, and relevant logical/encoded byte
   counts (plus host-noise facts for performance work);
4. the failure layer: preflight, harness, product, evidence judge, or publish;
5. whether earlier deterministic validation could have prevented the work; and
6. the smallest safe rerun scope, stated explicitly as `from-scratch` when no
   authenticated checkpoint exists.

Expensive gates put syntax, inventory, path-budget, fixture, package-binding,
and semantic-judge checks before VM creation or bulk I/O, then verify each
expensive stage before advancing. A harness change does not automatically
invalidate an already authenticated product checkpoint, while a product change
does invalidate downstream evidence. No result is described as passing merely
because its producer exited zero; the evidence judge for that layer must also
pass.

Track process quality alongside product quality: time to first useful
diagnostic, work after that diagnostic, VM boots before failure, repeated
logical/encoded bytes or already-passing lanes, rerun amplification (`repeated
work / affected work`), authenticated-checkpoint hit rate, and harness/flaky
failure rate. A post-run review that finds preventable work adds or moves a
preflight before the next expensive attempt.

The portable migration real-Lima gate, formal gate, and local release aggregate
retain this contract as `run-review.json`. The local aggregate also writes
`run-plan.json` before its first lane. It recognizes a same-candidate retry only
for a clean source with the same commit, tree, gate digest, inventory digest,
and Go toolchain. A hashed kernel boot marker distinguishes the same boot
session from a restart or power-cycle without exposing the host boot value.
These gates do not claim a reusable cross-run checkpoint unless the gate has an
explicit authenticated receipt, so release acceptance otherwise starts from
the full gate. A failed local lane may first be reproduced in isolation; the
formal gate exposes `--configuration ID` for that diagnostic purpose and still
requires its complete inventory for acceptance. A candidate change invalidates
the old acceptance run, while a reboot alone does not invalidate deterministic,
digest-bound artifacts. Runtime and host-noise evidence is always re-evaluated
after a different boot session.

Remote Gate 0 preserves the complete no-VM contract while scheduling its
formal and non-formal shards independently. Both must pass before the aggregate
`Build, test, and Gate 0` check succeeds. The formal shard records each model's
elapsed time, state counts, worker count, current model on failure, and exact
diagnostic rerun command. Splitting changes scheduling and visibility only; it
does not remove a model, invariant, property, refinement test, or evidence
judge from the complete gate.

The hosted arm64 runner uses one TLC worker. A measured two-worker attempt made
`WorkloadObservation` exceed 66 minutes even though it improved the smaller
`SecretTransition` model, so worker count is a per-runner policy rather than an
assumed monotonic speedup. The formal producer atomically snapshots its review
before and after each model. An always-run finalizer converts a surviving
in-progress receipt into an explicit harness failure after cancellation and
fails closed if no receipt exists; an artifact-upload warning is never accepted
as a run review. The two dominant models run first, reducing time and repeated
work when a runner or worker-policy regression affects either one; acceptance
still verifies the complete inventory independent of execution order.

Verification gates also isolate mutable tool configuration. Gate 0, the local
release aggregate, and the migration gate pin Go's module mode to
`-mod=readonly`, so an ambient developer `GOFLAGS` cannot silently repair or
dirty the candidate. Any required module ledger change is an explicit pre-gate
source change, never a test side effect.

### Local, mutation, and formal acceptance

The local aggregate must pass unit, race, fuzz/property, schema, generated,
static, dependency/license/advisory, recovery, and package-component checks.
Every authority, privacy, attribution, coverage, cleanup, help, UI, and
recovery claim in `docs/release/045-claim-matrix.md` needs both a positive
judge and a retained negative fixture that makes that judge fail.

The formal lane reads `formal/inventory.json` and must run exactly 16
configurations across 12 modules, all 122 inventoried safety invariants, all 28
liveness properties, and all 12 named Go production-refinement/crash tests.
The independent verifier rejects a missing result, counterexample, incomplete
pass set, changed source/log digest, or stale inventory.

### Real Lima workload and recovery acceptance

The macOS arm64 Lima lane must run the real packaged supervisor/observer path,
not a native or fixture substitute. It proves one cgroup-v2 workload owner for
the command after `--` and every attributable descendant; concurrent sessions
must not cross owners. It must observe real process, file-metadata, TCP/UDP
endpoint, and plaintext DNS facts, retain their execution ancestry, and reject
PID-only or post-start cgroup substitution.

The same aggregate must exercise online proxy/secret/DNS transition through
stage, probe, activate, prove, drain, and commit; crash at every durable
network boundary; retry with the same operation identity; independently probe
the effective route; inject observer loss; restart the daemon; reject target
tampering; clean the exact old owner; and preserve an unrelated owner. No
healthy proxy, DNS, or managed-secret edit may require daemon stop or VM
recreation.

Runtime coverage acceptance is intentionally asymmetric:

| Subsystem | Healthy supported reference | Required interpretation |
| --- | --- | --- |
| Process | `Available` after exact cgroup and observer proof | Empty results can support absence only for a fully Available queried interval. |
| File | `Partial` | Recorded open/read/write and related metadata is useful; an empty result is not a no-access proof. |
| Network | `Partial` | Process-to-IP/port evidence is useful; incomplete route attribution prevents a complete route claim. |
| DNS | `Partial` | Plaintext query correlation is useful; encrypted, cached, literal-IP, shared, or external-resolver cases remain unknown. |

An injected sequence gap or drop must degrade the affected interval and expose
its reason and loss count. Killing the observer helper must never produce a
graceful relay drain marker: an unsuccessful helper exit closes the relay
destructively, retains `transport-drop`, and leaves all four current subsystem
intervals unavailable. Target exit must close current coverage as
`Unavailable (target-exited)` without deleting prior evidence. Native or
unproved backends must report observation `Unavailable`, never silently
promote fixture evidence.

### UI and operator-journey acceptance

The UI aggregate must cover first-use help and discovery, real PTY rendering,
the Bubble Tea Overview/Workloads/Activity/Configuration/Operations views,
keyboard and focus behavior, Enter modals, terminal resize, stale read-only
reseed, operation recovery, and CLI/TUI/WebUI fact and operation parity.

Configuration editing must emit no Manager apply before explicit confirmation
and exactly one digest-bound apply afterward. The browser lane must use a real
headless browser against the authenticated loopback server and check keyboard
navigation, labels, responsive layout, bounded rows, injection handling,
credential hygiene, stream gaps, and reconnect/reseed. UI state alone never
proves a mutation succeeded.

### Privacy, retention, and cleanup acceptance

The privacy lane places distinct canaries in managed-secret input and every
supported sensitive field, then scans activity segments/indexes, process
listings, Keychain metadata output, Manager APIs, CLI/TUI/WebUI, daemon logs,
exports, support reports, and release evidence. It must find zero secret-value
hits while retaining useful ordinary local paths. Known secret values and
supported encodings, URI userinfo, authentication fields, sensitive
argument/query values, and Hideout control tokens are deterministically
removed. This does not claim heuristic discovery of arbitrary secrets.

The activity store must use private `0700` directories and `0600` files,
retain no file contents, environment values, keystrokes, full PTY transcript,
or packet/proxy-auth payloads, and expose any aggregation, loss, truncation,
repair, quarantine, or pruning in coverage. Default limits are one 8 MiB
active segment, 256 MiB per exact owner, and 1 GiB globally. Stop preserves
reusable evidence; clean/delete/recreate removes only the proved old
incarnation; successful disposable teardown removes only its exact session.
Ambiguous identity or failed absence proof blocks deletion.

### Performance acceptance

The independently recomputed results must remain within all frozen v1
ceilings: the reference workload's paired median overhead and its exact
nonparametric one-sided 95% median upper confidence bound are each at most
10%, warm attach and browser freshness p95 are at most 2 seconds, and the
documented query/render, daemon/TUI memory, observer CPU/RSS, event/drop
accounting, and one-active-segment quota bounds in
`docs/activity-observation.md` hold. Every measured drop must be reflected in
degraded coverage; silently improving a latency result by discarding evidence
is a failure.

The real-Lima attach and reference-workload methodology uses thirty recorded
adjacent counterbalanced samples after at least one warmup. Before starting,
the release operator must pause unrelated CPU-heavy tests, VMs, and emulators
and keep the host quiet until the gate finishes. The full lane refuses to
start without `HIDEOUT_PERFORMANCE_QUIET_HOST_CONFIRMED=1` and retains private
host-state snapshots at the run start and real-Lima boundaries. Before any
build or measurement, three one-second snapshots retain only PID, parent PID,
CPU, memory, and process name; two sustained threshold hits from the same
high-CPU, virtualization, or build/test process reject the run without stopping
it. During the complete real-Lima attach/reference interval, a one-second
monitor treats generic high CPU as diagnostic and rejects a recognized VM or
build/test/runtime workload only when the same PID/name exceeds its threshold
in all three consecutive samples. Generic load and one- or two-sample
classified transients remain visible in the raw evidence and in the unfiltered
counterbalanced pair distribution; they are not attributed to Hideout CPU and
do not invalidate the run. Only the gate
process group and Hideout/Lima
virtualization processes proven to use the gate's private runtime paths are
excluded. Its private evidence retains PID, parent PID, process group, CPU,
memory, and normalized process name, but no argv, environment, or runtime path;
the final collector independently reparses the digest-bound file. A violation
signals only the measured child after proving it owns a distinct PID-equals-PGID
process group; the child's early-installed EXIT trap removes its exact Gate 2
scratch, while unrelated host processes remain untouched. Known contention
invalidates the run rather than relaxing the ten-percent ceiling, discarding
samples, or selecting favorable measurements afterward. The
daemon/TUI fixture uses a separate `mktemp`-owned short private
store below `/private/tmp`, validates that its resulting Unix socket path is at
most 100 bytes before launch, and rejects an overlong-path fixture in
preflight; this is test placement only and does not relax the product's
fail-closed socket-path boundary. A failing real-Lima observer/quota child is
also reported with its exact status, terminal reason, and retained private log
path rather than disappearing behind the aggregate's `errexit` boundary.

### Package and final evidence acceptance

The clean package gate rejects dirty source identity, rebuild drift,
manifest/tree mismatch, missing or changed files, helper/UI/runtime digest
drift, and any reachable final-binary advisory. Lifecycle testing must consume
that exact archive without rebuilding, validate the immutable prior alpha
download, prove clean install, same-version reinstall, intentionally scoped
legacy-store discard, old-version upgrade, Keychain migration guidance,
ordinary uninstall absence, and preservation of durable or unrelated data.

The final evidence manifest must bind source commit and tree, version, package
and every packaged file digest, helpers, embedded UI assets, runtime, formal
inventory/results, every required gate, limitations, review resolution, local
install, and publication absence. A digest, source, timestamp, platform, or
candidate mismatch fails closed.

### Current evidence versus release acceptance

T151–T157 currently establish passing formal, local/adversarial, real-Lima,
UI/browser, privacy, and performance behavior for the recorded development
source identities. T158–T159 additionally proved the clean build and package
lifecycle judges in a disposable exact-clean implementation snapshot. Those
runs remain useful implementation evidence, but none is an accepted final
main-tree release candidate. The T163–T165 judges and negative preflights are
implemented; their exact package-bound and installed-machine receipts plus
the T171 final clean recollection remain mandatory before this gate may be
promoted.

## Gate 046: Portable Migration

The source-bound non-performance gate is `scripts/gates/migration.sh`. Its
checked-in `scripts/gates/migration-tests.txt` inventory is sorted, unique, and
fail-on-drift: the gate discovers every top-level test containing `Migration`,
`Migrate`, or `ConfigOnly` in the 13 declared packages and requires the exact
closed migration test set to pass. The separate sorted
`scripts/gates/migration-hostile-tests.txt` matrix requires exactly nine
categories—wrong key, truncation, duplication, reordering, sparse abuse,
traversal, special files, expansion, and trailing content—and records their
exact test events. The sorted `scripts/gates/migration-crash-cuts.txt` inventory
is independently fixed at 13 entries: source/import claims, provider snapshot,
bundle header, payload checkpoint, manifest, sealed footer before publish,
import disk checkpoint, provisional secret, adoption, verification, activation
decision, and Manager visibility. Each selector must exist and its exact
top-level or subtest event must pass; count-only or broad-package success is not
accepted. The gate also runs six migration fuzz targets with checked-in hostile
seeds, schema checks, all four migration TLC configurations, and their seven
inventoried Go refinement traces. Release-candidate local evidence includes this
as a separate required `migration` lane; a broad unit run cannot silently stand
in for the explicit inventories.

The full-state candidate gate is:

```sh
scripts/gates/migration-lima.sh \
  --candidate-result .artifacts/045/package/result.json
```

It rejects dirty, unaccepted, rebuilt, or digest-mismatched candidates; installs
the exact archive into a private prefix; and uses one isolated Lima home plus
independent source/destination Manager stores. The source has a root-disk fixture,
an attached-disk fixture, a host-workspace exclusion sentinel, recorded guest
machine/SSH identity, and pre-export root/attached/record hashes. The gate then:

- exports one encrypted owner-only sealed bundle and retains its terminal
  receipt;
- proves a wrong passphrase creates no destination environment;
- authenticates an inventory containing exactly one environment and root plus
  attached disks, with host workspace content excluded;
- imports the same byte-identical bundle into three Safe Clone destinations and
  one separately acknowledged Exact Guest Restore destination;
- kills the exact third-destination daemon during durable materialization,
  proves protected passphrase re-unlock and checkpoint resume, kills its fresh
  daemon again during adoption, and proves startup recovery completes without
  another bundle secret;
- runs all four destinations and verifies both persistent fixtures;
- proves all control and backend identities are fresh and pairwise distinct;
- proves all three Safe Clone machine IDs and SSH host keys are fresh and pairwise
  distinct, while Exact Guest Restore preserves both;
- removes the package-owned zero-network executor only from an isolated copy of
  the installed prefix and proves full import refuses with the stable capability
  code before creating an operation or destination environment;
- proves the source disk/record hashes and bundle hash did not change; and
- writes only private digest-bound summaries and redacted terminal projections,
  with an explicit no-performance limitation.

`scripts/release/collect-evidence.sh` requires this as the distinct
`migration-lima` candidate gate, binds its candidate-pointer/archive/installed
binary hashes to the exact package, and now requires 12 unique release gates.
Preflight validates the gate scripts and release evidence schema without
starting a VM. It also runs one accepted and four rejected synthetic summary
fixtures covering Safe Clone identity uniqueness, daemon-instance uniqueness,
compatibility refusal before effects, and private artifact modes, so those
judges fail before an expensive VM run when their semantics drift. A completed
one-host run establishes provider mechanics across independent stores, not broad
physical portability; the quickstart's source and two physical destination Macs
remain the cross-computer acceptance requirement.

Performance qualification is intentionally deferred while the operator host is
unstable. This does not relax any correctness, encryption, identity,
source-immutability, recovery, redaction, package, or real-backend gate. A
release made before the deferred lane passes MUST state that migration CPU,
I/O, peak memory, throughput, sparse-efficiency, and duration are unqualified;
it MUST NOT reuse whole-host timing as evidence attributable to Hideout.

The deferral closes only when `scripts/gates/migration-performance.sh` runs on a
quiet host, records process-scoped Hideout/backend CPU and I/O alongside host
contention diagnostics, retains the exact package/runtime/fixture identity, and
passes the frozen limits without dropping events or discarding unfavorable
samples. The trigger and non-claim are also recorded in `docs/DEBT.md`.
