# Hideout Implementation Status

<!-- markdownlint-disable MD013 -->

Status source for the current product surface. Detailed contracts remain in the
design documents; this file prevents implementation status from drifting across
many subsystem specs.

Current release state: private alpha / supervised dogfood with a latest local
release-candidate evidence bundle produced on macOS at commit
`5f3c0b7a4f0d`. Public GA still requires this evidence to be produced for the
final release artifact and explicit release-specific signoff.

Latest local release-candidate evidence: manifest at
`.hideout-release-evidence/release-dogfood-20260705T064523Z-5f3c0b7a4f0d/manifest.json`
(status passed, `scripts/test-phase1.sh --release-candidate`). A bundle
certifies only the exact commit recorded in its manifest; rerun
`scripts/test-release-dogfood.sh` on the release commit before cutting an
artifact.

MVP scope: a professional individual operator on their own machine; the
user-facing vocabulary is environments, workspaces, permissions, and audit.

## Implemented Product Paths

| Area | Status |
| --- | --- |
| Lima backend | Reusable environments, workspace mount, identity mounts, helper discovery, and command execution are implemented for the primary macOS dogfood path. Guest tool supply is moving to declared base image plus operator-run in-boundary setup; the npm provisioning step still runs today and its removal is in progress (002). |
| Lima real-run reference smoke | `scripts/test-lima-real-run.sh` and `scripts/test-phase1.sh --lima-real-run` provide a supervised reference workload over the real Lima backend: deterministic workspace artifact, guest-side success check, selected network evidence, fixed Boundary Action Set, and redacted audit/Boundary Summary checks. This is dogfood evidence for the reference slice, not unattended daily-use, GA, release-candidate, TUI/WebUI, or product-specific agent support. |
| Native backend | Implemented as a weak-isolation development harness only. It is not isolation evidence. |
| Workspace guard | Implemented before backend prepare; rejects host home, Hideout store, credential roots, and browser profile roots unless explicitly overridden. |
| Environment lifecycle | Reuse, list, stop, clean, environment locking, SIGINT/SIGTERM cancellation, and runtime cleanup are implemented. |
| Command Proxy | Phase 1 product path implements registered `host.open` command shims using the `open-target-v1` argv schema. Default profiles register `open` and `xdg-open`; profiles may add more host-open command symbols through CLI or Manager/Web typed command-proxy plan/apply without adding new authority. Generic JS adapter outcomes are design-ready, not implemented as a general user-facing surface. |
| Host open | `host.open` supports external HTTP(S) URLs and workspace-mapped files through a brokered opener, isolated browser profile, local/private URL deny, DNS rebind checks, audit, and Gate 4 coverage. |
| HostFS Portal | Read-only `stat`, `read`, and `list` data plane is implemented for Linux guests through FUSE and broker RPC, with grants, reserved-store rejection, filtered list, and audit. Durable profile HostFS allow/deny rules are managed through the same lower-layer rule model from CLI and typed Manager/Web plan/apply. |
| HostFS write overlay | Later. |
| Profile env policy | Durable `profile.env.public`, `profile.env.inherit`, and `profile.env.deny` are managed from CLI and typed Manager/Web plan/apply. Overview and API responses expose env names only, not public env values. |
| Network | `direct` and guest-side `tun2socks` modes are implemented. Proxy values are hidden from the target env. DNS/privacy-mode hardening remains governed by the network design and gates. |
| Tool supply | The npm provisioning path is being removed; the code still exists today. Ownership split: `002-guided-first-run` (`specs/002-guided-first-run/`) removes the first-run surface (init/doctor materialization flags, help text, examples); removing the `npm-global` provider execution path, `profile.tools.npmGlobals` storage, the `base-dev`/`node-dev` presets, and the `profile tools npm` subcommands is follow-up work in the same milestone. Target state: guest tools come from the declared base image plus operator-authored in-boundary setup run as an ordinary `hideout run`; Hideout ships no package-installation providers. |
| Manager API | Local token-protected API implements read-only overview resources, including reusable environments, plus typed `init/plan`, `init/apply`, `run/plan`, `run/apply`, `run/status`, command-proxy plan/apply, profile HostFS rule plan/apply, profile env plan/apply, and reusable environment `stop`/`clean` plan/apply surfaces for TUI/WebUI. Init plans include structured next steps. It is not a raw profile writer, host execution API, or arbitrary VM control API. |
| TUI smoke surface | Implemented as the lightweight panel: `hideout tui` renders a capped persistent terminal dashboard from Manager overview and redacted audit data, with `--once` as the script/snapshot mode. Panel scope and planned interactions are defined in [tui-webui-experience.md](tui-webui-experience.md); tool-count panels follow the 002 npm-provisioning removal. |
| WebUI smoke surface | Implemented as the fuller management surface over the Manager API: overview/audit summaries plus typed plan/apply operations. Scope is defined in [tui-webui-experience.md](tui-webui-experience.md); tool-setup panels follow the 002 npm-provisioning removal. It is not the final product UI. |
| Endpoint Exposure | Product `endpoint.expose.host-to-guest` is implemented for declared and run-scoped manual guest-loopback TCP candidates, with active owner validation, backend provider, audit, cleanup, and Boundary Summary. |
| Preview open | Minimal `preview.open` is implemented as the first consumer of host-to-guest exposure. Callback adapters, endpoint observation, and project-declared auto exposure are later/design-ready. |
| Boundary Summary | Default `hideout run` is quiet; `--verbose`, `explain`, `hideout audit show`, Manager API, TUI, or Web UI surfaces show redacted control-plane evidence. |
| Script runtime | Required Phase 1 supports `command.decide` and `audit.redact` domain entrypoints through the `decideCommand(ctx)` and `redactAudit(ctx)` goja ABI, with constrained execution and deterministic time/randomness. Bounded context query APIs are design-ready. |

## Not Yet Productized

| Area | Status |
| --- | --- |
| Generic Command Proxy bindings | Partially implemented for configured `host.open` command symbols only. JS adapters, non-open outcomes, and bounded context queries are documented but not yet a general product path. |
| `hideoutd` daemon | Design-ready. Current CLI, TUI, and WebUI use embedded Manager Core or a command-scoped local WebUI server. A per-user daemon for persistent event streams, prompt channels, background cleanup, and local API serving is not implemented. |
| Generic tool provider declarations | Dropped as a product direction: Hideout ships no package-installation providers (npm removal in progress, 002); a declaration, if ever added, is a thin expected-commands diagnostic record. The declarative guest base-environment artifact class (base image references; guest domain, shareable through the ecosystem) is the accepted direction. Imperative environment recipes remain out of scope. |
| Command outcomes beyond `host.open` deny/allow | Design-ready. `simulate`, `rewrite-guest`, and generic `invoke-capability` must fail closed until implemented and gated. |
| `endpoint.expose.guest-to-host` | Lab/separate design. Required before adb, browser DevTools, host service reachability, or similar workflows. |
| Endpoint observe / AccessSensor | Later. Observation may produce audit and warnings, but must not authorize exposure by itself. |
| Browser control | Lab/separate design. |
| adb / device / simulator workflows | Adapter recipes over future guest-to-host and OpenTarget capabilities; not productized. |
| Host app/resource provider recipes | Design-ready. Core must supply facts and validators; adapters own product risk logic. |
| HostFS overlay apply | Later. |
| machine-id / identityId coupling | Known issue (pre-dates deterministic redaction). `machineIDFromIdentityID` returns the `id_`-stripped identityId body verbatim, so the generated guest machine-id is byte-identical to the identityId body. Audit and Manager/WebUI preserve `identityId` as a traceability identifier while stripping `machineId`, so the machine-id strip is defeated (stripping `id_` from any shown identityId yields the raw machine-id). machine-id is generated fake identity material, not a credential, and today's surfaces are the 0600 local audit and the authenticated local Manager, but this falsifies the "must not expose raw machine-id" claim and would make exported/shared audit carry a linkable guest identity. Fix belongs in identity generation (derive machine-id by one-way hash or independent random, decoupled from identityId), plus routing `internal/inittask` audit through the deterministic redactor; both are follow-up decisions because they change identity derivation. |
| Deterministic redaction | Implemented. Hideout-minted control-plane credentials are stripped exactly (HIDEOUT_SECRET_* namespace, `cap_`/`ui_` token values, Core control-plane field names, generated machine-id); user/application data stays verbatim in local audit, local authenticated Manager/WebUI views, and `command.decide`/`audit.redact` script context. Storage-time heuristic key/pattern/URL redaction was removed. User-data redaction is owned by `audit.redact` policy and the export/share boundary. The dedicated export/share redaction surface (deterministic control-plane strip plus user-selected redaction on bundle export) is design-ready. |
| Public ecosystem | Later. Trust MVP is two-tier: local artifacts, and third-party artifacts behind digest pinning, a permission diff, and one explicit confirmation. Marketplace day-1: signing, revocation/kill-switch, publisher identity, and namespace protection — designed when the marketplace launches, not before. |
