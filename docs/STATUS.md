# Hideout Implementation Status

<!-- markdownlint-disable MD013 -->

Status source for the current product surface. Detailed contracts remain in the
design documents; this file prevents implementation status from drifting across
many subsystem specs.

Current release state: private alpha / supervised dogfood with a latest local
release-candidate evidence bundle produced on macOS at commit
`1343fc3d2ec9`. Public GA still requires this evidence to be produced for the
final release artifact and explicit release-specific signoff.

Latest local release-candidate evidence:

- manifest:
  `.hideout-release-evidence/release-dogfood-20260705T055426Z-1343fc3d2ec9/manifest.json`
- status: passed
- command: `scripts/test-phase1.sh --release-candidate`
- commit: `1343fc3d2ec9`, dirty: false
- host: Darwin 25.4.0 arm64, macOS 26.4.1
- release artifact: `hideout-darwin-arm64-1343fc3d2ec9.tar.gz`; SHA-256
  `5af57e7bc7f20720c41c7923637a2f6f42bdfacc46487608af22ee0a036d11b3`;
  byte size recorded in the manifest
- gates: Gate 0 static contract, Gate 1 native smoke, Gate 2 Lima E2E, Gate 3
  hidden proxy with operator-supplied proxy, Gate 4 host escape with a real
  browser, capability probe smoke, and generic CLI dogfood smoke
- cleanup evidence: Gate 4 browser processes, Gate 4 temporary directories, and
  Hideout Lima instances all recorded as zero in the manifest
- proxy evidence: operator proxy present; scheme recorded; full proxy URL
  redacted from the manifest and evidence logs

Evidence scope: a release-candidate bundle certifies the exact git commit
recorded in its manifest. Later documentation-only commits do not extend that
certification; before cutting a release artifact, rerun
`scripts/test-release-dogfood.sh` on the release commit and record the new
manifest.

## Implemented Product Paths

| Area | Status |
| --- | --- |
| Lima backend | Reusable environments, workspace mount, identity mounts, helper discovery, tool provisioning, and command execution are implemented for the primary macOS dogfood path. |
| Native backend | Implemented as a weak-isolation development harness only. It is not isolation evidence. |
| Workspace guard | Implemented before backend prepare; rejects host home, Hideout store, credential roots, and browser profile roots unless explicitly overridden. |
| Environment lifecycle | Reuse, list, stop, clean, environment locking, SIGINT/SIGTERM cancellation, and runtime cleanup are implemented. |
| Command Proxy | Phase 1 product path implements registered `host.open` command shims using the `open-target-v1` argv schema. Default profiles register `open` and `xdg-open`; profiles may add more host-open command symbols through CLI or Manager/Web typed command-proxy plan/apply without adding new authority. Generic JS adapter outcomes are design-ready, not implemented as a general user-facing surface. |
| Host open | `host.open` supports external HTTP(S) URLs and workspace-mapped files through a brokered opener, isolated browser profile, local/private URL deny, DNS rebind checks, audit, and Gate 4 coverage. |
| HostFS Portal | Read-only `stat`, `read`, and `list` data plane is implemented for Linux guests through FUSE and broker RPC, with grants, reserved-store rejection, filtered list, and audit. Durable profile HostFS allow/deny rules are managed through the same lower-layer rule model from CLI and typed Manager/Web plan/apply. |
| HostFS write overlay | Later. |
| Profile env policy | Durable `profile.env.public`, `profile.env.inherit`, and `profile.env.deny` are managed from CLI and typed Manager/Web plan/apply. Overview and API responses expose env names only, not public env values. |
| Network | `direct` and guest-side `tun2socks` modes are implemented. Proxy values are hidden from the target env. DNS/privacy-mode hardening remains governed by the network design and gates. |
| Tool supply | `base-dev`, `node-dev`, and user-declared npm globals are provisioned after network bootstrap and before target command checks. Strict proxies must allow required registry egress or provisioning fails closed. |
| Manager API | Local token-protected API implements read-only overview resources, including reusable environments, plus typed `init/plan`, `init/apply`, `run/plan`, `run/apply`, `run/status`, command-proxy plan/apply, profile HostFS rule plan/apply, profile env plan/apply, and reusable environment `stop`/`clean` plan/apply surfaces for TUI/WebUI. Init plans include structured next steps. It is not a raw profile writer, host execution API, or arbitrary VM control API. |
| TUI smoke surface | `hideout tui` renders a capped terminal dashboard from Manager overview and redacted audit data, including local refresh time, optional profile filtering, init next steps, capability summary, per-profile env, tool, command-proxy, and HostFS rule counts with CLI setup hints, network risk, reusable environments with lifecycle command hints, sessions with audit/cleanup command hints, recent denied audit, and recent audit. `--watch` refreshes locally without starting WebUI or minting a UI token. |
| WebUI smoke surface | Embedded local WebUI shows overview/audit/resource summaries, local refresh time, UI token expiry time, optional profile scope filtering, capped reusable environment and session panels with audit/cleanup command hints, init next steps, network risk, denied-audit counts, basic audit filtering, generic tool setup with next-step rendering, controlled run plan/apply, command-proxy plan/apply, profile HostFS rule plan/apply, profile env plan/apply, and reusable environment stop/clean plan/apply through Manager API. It remains a lightweight smoke/operations surface, not the final product UI. |
| Endpoint Exposure | Product `endpoint.expose.host-to-guest` is implemented for declared and run-scoped manual guest-loopback TCP candidates, with active owner validation, backend provider, audit, cleanup, and Boundary Summary. |
| Preview open | Minimal `preview.open` is implemented as the first consumer of host-to-guest exposure. Callback adapters, endpoint observation, and project-declared auto exposure are later/design-ready. |
| Boundary Summary | Default `hideout run` is quiet; `--verbose`, `explain`, `hideout audit show`, Manager API, TUI, or Web UI surfaces show redacted control-plane evidence. |
| Script runtime | Required Phase 1 supports `decideCommand(ctx)` and `redactAudit(ctx)` with constrained goja execution and deterministic time/randomness. Bounded context query APIs are design-ready. |

## Not Yet Productized

| Area | Status |
| --- | --- |
| Generic Command Proxy bindings | Partially implemented for configured `host.open` command symbols only. JS adapters, non-open outcomes, provider descriptors, and bounded context queries are documented but not yet a general product path. |
| Command outcomes beyond `host.open` deny/allow | Design-ready. `simulate`, `rewrite-guest`, and generic `invoke-capability` must fail closed until implemented and gated. |
| Provider descriptors | Design-ready. Provider engines must remain Go-owned; ecosystem descriptors require schema, validator, and trust UX before use. |
| `endpoint.expose.guest-to-host` | Lab/separate design. Required before adb, browser DevTools, host service reachability, or similar workflows. |
| Endpoint observe / AccessSensor | Later. Observation may produce audit and warnings, but must not authorize exposure by itself. |
| Browser control | Lab/separate design. |
| adb / device / simulator workflows | Adapter recipes over future guest-to-host and OpenTarget capabilities; not productized. |
| Host app/resource provider recipes | Design-ready. Core must supply facts and validators; adapters own product risk logic. |
| HostFS overlay apply | Later. |
| Public ecosystem | Later. Bundle composition, revocation, signatures, author tooling, and workspace trust must be promoted before public ecosystem release. |
