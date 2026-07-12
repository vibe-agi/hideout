# Phase 0 Research: Host Capability Projection

<!-- markdownlint-disable MD013 MD060 -->

All decisions below are grounded in the current codebase (verified) and the reviewed design draft. No open `NEEDS CLARIFICATION` remains.

## D1 — Capability registry is a static Go registry, not a runtime plugin interface

**Decision**: Define `CapabilityDescriptor` (id, riskClass, intentSchema, resourceKinds, resultPolicy, providerRef, decisionPolicy, lifecyclePolicy) and hold all descriptors in a package-owned static Go registry in `internal/hostcap`. Providers are Go functions selected by `providerRef`. Nothing at runtime registers a new descriptor or provider.

**Rationale**: Constitution Principle II — Core owns authority; scripts/config compose but never execute or introduce authority. A runtime registration interface would let untrusted layers add host effects, which is the exact boundary breach the projection model must prevent. A static registry is also trivially testable and diff-reviewable.

**Alternatives considered**: (a) growing the `internal/policy` action `const` block again (`host.open`, `guest.exec`, `network.connect`, `portbridge.host-to-guest`, `endpoint.expose.host-to-guest`) — rejected: switch-statement sprawl and no per-capability metadata (riskClass/resultPolicy/lifecycle). (b) A plugin/registration API — rejected: violates zero-authority-scripts.

## D2 — `code` is a recipe: command binding + declarative grammar + registered appRef

**Decision**: Add a `code` command `Registration` in `internal/cmdproxy` whose `Action` is a new `host.app.open-resource` and whose parsing is a declarative grammar in `internal/cmdgrammar` (positional workspace resource; flags `-g <file:line:col>`, `-n/--new-window`, `-r/--reuse-window`; `unknownFlags: deny`). The grammar emits an app-agnostic `OpenResourceIntent{appRef, resources[], location, windowMode}`. Go re-decodes and field-validates the intent; the grammar/adapter never passes raw argv to the host.

**Rationale**: Constitution Principle II names editors explicitly as things that must not become Core semantics. Core knows only the generic intent; `code -g` syntax lives in the recipe layer. `cursor`/`zed`/`idea` become future recipes over the same `host.app.open-resource` with their own grammar — no Core change. Verified: `cmdproxy.Registration` already models `Name/Aliases/Action/ArgvSchema/AllowedTargets/OwnerType/AdapterID`, so a new binding slots in.

**The transformer is a zero-authority bridge, in two interchangeable forms.** The layer that turns guest argv into an intent is a helper/"承上启下" role: it transforms parameters and may *propose* orchestration, but never performs a host effect. Two equivalent forms produce the SAME typed intent: (a) a declarative Go grammar (v1 uses this for `code`), and (b) a goja JS adapter (`decideCommandAdapter`, 008) — the community-shareable form for other editors and complex commands. To support an app, Core still invokes the app's original CLI (`code`); the transformer only produces `{appRef, resources, location, windowMode}` and Core execs the real binary. It never touches argv or the binary.

**Proposals may request more than one capability (design-ready).** A binding's `AllowedCapabilities` is plural: a transformer may propose, alongside the open, a secondary capability (e.g. a port mapping). Each proposed capability is a separate Core-owned provider Go independently validates and executes — the transformer orchestrates, Go executes every effect. v1 implements only the single open; multi-capability proposals are design-ready.

**Alternatives considered**: raw-argv passthrough — rejected: `docs/script-extension-architecture.md` forbids it with `code .` as the example. JS-executes-the-effect — rejected: JS carries zero authority; it proposes, Go disposes.

## D3 — `host.app.open-resource` is a new broker action mirroring host.open resolution

**Decision**: Add broker action `host.app.open-resource`. It re-decodes the `OpenResourceIntent`, maps each workspace `ResourceRef` to a host path using the session-bound workspace root (the same `HostRoot`-relative resolution + `EvalSymlinks` escape recheck that `host.open` already performs in `internal/broker/broker.go`), resolves `appRef` through the Core app-identity registry, and dispatches to the VS Code launcher. Guest response carries only success/refused + a typed error code; never the host path.

**Rationale**: `host.open` already contains the exact safe resolution (workspace containment, symlink escape recheck, directories allowed). Reusing that pattern keeps the host-path knowledge inside Core and avoids re-implementing path safety. Verified: `broker.go` maps guest→host under `HostRoot`, refuses escapes, and allows regular files and directories.

**Alternatives considered**: overloading `host.open` — rejected: `host.open` is OS-opener semantics (`open`/`xdg-open`), whereas `code` launches a specific registered app with structured location/window args; conflating them would make `host.open` carry app-specific logic.

## D4 — Workspace ResourceRef; host path is Core-only (and this hides the username)

**Decision**: Every guest/adapter/intent/event surface represents a workspace resource as `ResourceRef{kind: "workspace", guestPath: "/workspace/...", relativePath: "..."}`. Only Core resolves it to a host path via the session-bound mapping. The projection audit record stores workspace identity + relative path, never `hostPath`.

**Rationale**: One-directional invariant: Core must know the host path to open the right folder, but that path never flows back to the guest. This is the same invariant that closes the username channel under `pathMode=alias` — the guest sees `/workspace`, the host username lives only in Core. Buys projection safety and username-hiding from one mechanism.

**Alternatives considered**: passing host path to the guest for display — rejected: leaks username/path shape, breaks the privacy claim.

## D5 — Privacy default: privacy/hardened profiles default `pathMode=alias`; drift via existing model

**Decision**: New privacy and hardened profiles default `Workspace.PathMode="alias"` (guest `/workspace`). `dev`/`debug` may keep `preserve`. The low-level `profile.Default` may stay `preserve` until onboarding is audited, but privacy/hardened templates must not inherit `preserve` while claiming path privacy. A `pathMode` change on an existing environment is caught by the existing drift comparator (workspace axis, `run_environment.go` compares `GuestWorkspace`) → fail-closed recreate. No new identity input, no record-version bump.

**Rationale**: Verified: `AliasGuestWorkspace="/workspace"` and `ResolveWorkspaceMapping` exist; `profile.Default` currently sets `preserve` (`profile.go:278`); the drift comparator already compares `GuestWorkspace` (`run_environment.go:256-263`), so flipping `pathMode` changes the resolved `GuestWorkspace` and drifts today. Identity env is already synthetic (`developer` account home `/home/developer` vs process `HOME=/hideout/profile/home`; generated git `developer@example.com`).

**Alternatives considered**: `/Users/fake/workspace` — rejected: fakes a nonexistent host identity with no compat benefit. Record-version bump for pathMode — rejected: already handled by the GuestWorkspace drift axis.

## D6 — Three-channel username verification with per-channel detector self-test + preserve control

**Decision**: The privacy claim is gated on a real-Lima check of three channels — identity environment (`USER`, `LOGNAME`, `HOME`, hostname, generated Git identity/config, git config origin; distinguishing account home `/home/developer` from process `HOME=/hideout/profile/home`), workspace namespace (`pwd`, `realpath`, arguments, errors, generated absolute paths), and mount metadata (`/proc/mounts`, `/proc/self/mountinfo`, `mount`, `findmnt`, source/tag). Each channel's detector is self-tested (prove it matches a deliberately-present host username/home before asserting absence), and a `preserve`-mode control fixture proves the host path shape is exposed there — so neither a broken matcher nor an untested mapping can produce a false green.

**Rationale**: Constitution Principle IV — isolation claims backed by gates; positive tests + fail-closed tests. Mount metadata is Lima-determined guest-observable behavior that source inspection cannot close, so it needs a real-backend check. The self-test + preserve control are the anti-false-green discipline (mirrors the `positive_control` pattern in spec 001).

**Alternatives considered**: source-only assertion — rejected: cannot prove the guest-visible mount representation. Single-channel (path only) check — rejected: username leaks through env/mount too.

## D7 — Safe mode default; trusted-host-ide is an explicit revocable operator grant

**Decision**: The `code` recipe defaults to safe mode: isolated `--user-data-dir` (or dedicated Hideout VS Code profile), `--disable-extensions`, workspace auto-tasks not run, Workspace Trust left enabled, never `--disable-workspace-trust`. Safe mode opens without a per-invocation prompt (default-allowed low-risk capability, like `open`/`xdg-open` registration). `trusted-host-ide` (operator's normal VS Code config) requires an explicit grant through the operator decision center, denied without it, recorded as a visible revocable record, re-affirmed/invalidated on profile/environment identity change, and persisted only in guest-unreachable control-plane state keyed by workspace/profile — never in the guest-writable workspace.

**Rationale**: The workspace is guest-writable; a host IDE opening it auto-runs `.vscode/tasks.json` (`runOn: folderOpen`), folder settings, and recommended extensions when trusted. `docs/privacy-run-design.md:663` already classifies `host.app.open-resource` as high-authority and warns the workspace is an execution payload. VS Code's Workspace Trust is a real default mitigation but depends on operator env state (parent-folder trust, prior allow, non-compliant extensions), so Hideout must not rely on it and must impose safe mode. Persisting the mode in the guest-writable workspace would let the agent flip itself to trusted — hence control-plane-only, guest-unreachable.

**Alternatives considered**: default to operator's normal VS Code — rejected: arms the auto-exec payload with agent-written config. `--disable-workspace-trust` — rejected: disables the protection layer. Per-invocation approval prompt — rejected: kills the low-friction "just works" goal for the safe default.

## D8 — No generic fallback; installed shim exclusively owns the name; PATH shadow inspectable

**Decision**: A hit projected shim whose capability is unbound / provider unavailable / validation fails / flag unknown / path unmapped / app absent-or-drifted fails closed with a typed recovery code and never delegates to host execution or to a real same-named guest binary the shim shadows. An installed projected command name exclusively owns that name; where a real same-named guest binary also exists, the PATH shadow order is explicit and inspectable via doctor/inspection. When no shim is installed, a real same-named guest binary running normally is not a fallback.

**Rationale**: Constitution Principle I — no silent fall back to ambient host authority. The exclusive-ownership + inspectable-shadow rule closes the seam between "shim fails" and "a shadowed real binary exists."

**Alternatives considered**: fall back to the shadowed binary on failure — rejected: turns a failed projection into ambient execution.

## D9 — Result policy is per-capability; `code` = `none`; future channels are typed/bounded

**Decision**: Each `CapabilityDescriptor` declares a `resultPolicy` ∈ {`none`, `bounded-typed`, `stream`, `lease`}. `host.app.open-resource` declares `none` (fire-and-forget; no host data returns to the guest, not even via stdout). adb/AppleScript (design-ready) would declare `bounded-typed`/`lease` with a typed, bounded, audited channel — never ordinary command stdout.

**Rationale**: A host→guest result is a controlled host→guest exfiltration/injection channel and must be typed and audited per capability. `code` is safe precisely because it returns nothing.

**Alternatives considered**: uniform "stdout is the result" — rejected: hides host data return in an unaudited channel.

## D10 — Recovery codes, evidence, and docs

**Decision**: Add `projection.*` recovery codes (e.g. `projection.command.unbound`, `projection.provider.unavailable`, `projection.path.no-host-mapping`, `projection.app.absent`, `projection.app.identity-drift`, `projection.mode.trusted-denied`, `projection.flag.unrecognized`) to the `internal/recovery` registry (matching its `domain.subject.detail` convention). Register 030 proof ids/claim ids in `internal/productevidence`. Update `docs/STATUS.md`, `docs/threat-model.md` (scoped username-hiding claim + adjacent non-claim; projection escape non-claims), a new `docs/host-capability-projection.md`, `docs/privacy-run-test-plan.md`, `docs/claim-boundaries.md`, and README.

**Rationale**: Constitution Principle IV + Development Workflow — typed recovery across surfaces, evidence spine, docs truth for claims/non-claims.

**Alternatives considered**: none; this matches existing 028/026/025 patterns.
