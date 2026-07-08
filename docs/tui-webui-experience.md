# TUI And WebUI Experience

<!-- markdownlint-disable MD013 -->

## Contract

TUI and WebUI are product surfaces over Manager Control Plane. They do not own
runtime authority or policy semantics.

This document follows [architecture-principles.md](architecture-principles.md)
and [manager-control-plane.md](manager-control-plane.md).

## Position

Hideout should support both terminal and browser management because they solve
different user problems.

```text
TUI
  lightweight panel surface: audit observation and session management

WebUI
  fuller management surface: policy editing, environment management,
  audit search
```

Both surfaces must use the same Manager Core/API domain resources. WebUI uses
the local Manager API transport; terminal surfaces may call Manager Core
in-process, but must not rebuild state by reading subsystem files directly.
The steady-state model is daemon-first and implemented (see [STATUS.md](STATUS.md)):
`hideoutd` hosts the Manager API over a store-rooted socket plus a live, redacted
event stream, and also serves the WebUI over a tokened loopback UI transport. The
WebUI panels open an `EventSource` on `/daemon/events` and the TUI consumes the
same stream via `daemon.SubscribeEvents`, so both surfaces apply typed event
payloads to a seeded live-console reducer with no steady-state overview/audit
polling while the stream is healthy. Typed run/session and cleanup rows are live
for daemon-mediated Manager operations; standalone CLI invocations still surface
through audit-tail events unless they go through the daemon. Environment live
events currently cover the Manager stop/clean lifecycle. Both surfaces fall back
to their prior behavior when no daemon runs or the stream closes. The current
proof is a Gate 0 deterministic JavaScript reducer harness, production
emit-source tests, and TUI terminal capture; richer browser-driven UX automation
remains a later product-hardening task.

## TUI Role

The TUI is the always-on local operator surface. It is meant to run in a
separate terminal while another terminal runs `hideout run -- <agent-or-tool>`.
It must behave like a persistent session observer with lightweight session
controls, not like a post-run log printout.

Phase 1 ships a read-only smoke dashboard over Manager overview and redacted
audit data. It can refresh in place and can render a single snapshot for tests,
but it is not the final interactive TUI. The product target is the lightweight
pane: side-by-side panels with keyboard shortcuts for audit observation and
session management, backed by controlled Manager plan/apply actions.
Interactive first-run and doctor flows are product increments, not the current
default init path.

Recommended implementation stack:

```text
Bubble Tea
Bubbles
Lip Gloss
```

Why TUI first:

- users are already in terminal when running unknown CLI tools;
- setup failures can be fixed without context switching;
- session state can be monitored while commands run;
- Go implementation can live close to Hideout's existing code;
- it works before a polished WebUI exists.

Suggested commands:

```bash
hideout tui
hideout tui --profile <name>
hideout tui --once
hideout doctor
hideout doctor --fix --dry-run
```

## WebUI Role

The WebUI is the fuller management surface.

Best use cases:

- audit search and filtering;
- policy editing;
- environment management;
- network route and DNS explanation;
- onboarding and documentation.

The WebUI should not be required for first successful `hideout run`.

## Surface Division

Both TUI and WebUI read the same Manager data model, but the page sets are
intentionally not mirrored: the TUI carries an observation and session
management subset, while the WebUI is the fuller management system.
Differences are coverage and layout, never data model or policy semantics.

TUI page set:

```text
Dashboard
Sessions
Environments
Audit
Doctor
```

WebUI page set:

```text
Dashboard
Profiles
Sessions
Environments
HostFS
Network
OpenTargets
Audit
Doctor
Helpers
Bundles
Policy Scripts
Command Adapters
```

The Helpers page is limited to helper artifacts: discovery status, helper
manifests, and repair plans. It is not a package installation surface.

Command Adapter configuration is a Manager-owned profile resource, not a UI-owned
policy engine. TUI/WebUI surfaces may show enabled adapter IDs, owned command
symbols, digest status, and recent adapter decisions from redacted audit events.
Any add/enable/disable/refresh/remove flow must call the same Manager
plan/apply operations as the CLI and must keep the 008 root-sensitive wording as
command-name intent capture enriched by 009 privilege status, never as an
absolute-path, syscall, setuid, or post-guest-root containment claim.

Adapter-pack visibility is also Manager-owned. TUI/WebUI may list installed
packs, built-in metadata, active revision IDs, lifecycle state, test status, and
profile bindings from Manager overview/API. Install, test, enable, disable,
upgrade, and revoke remain typed Manager operations; the first consumer is CLI,
and any future UI controls must call the same `adapter-pack/plan|apply` routes.
These surfaces must describe packs as local digest-locked extensions, not as a
trusted public marketplace.

## TUI Initial Pages

### Dashboard

Implemented smoke surface shows a persistent local dashboard by default.
`--once` is reserved for scripts, package smoke, and documentation snapshots.
The dashboard shows:

- local refresh time;
- optional profile filter;
- selected profile;
- init next steps;
- capability summary;
- per-profile env policy counts without env values;
- backend status;
- network mode and privacy warning;
- recent active sessions;
- recent reusable environments;
- reusable environment lifecycle command hints;
- session audit and runtime cleanup command hints;
- recent denied audit events;
- recent audit events.

Design-ready additions stay within the panel model: side-by-side panels with
keyboard shortcuts for audit observation and session management, plus doctor
warnings.

### Doctor

Shows checks and repair actions:

- missing Lima;
- missing helper binary;
- missing tun2socks;
- invalid profile;
- stale environment;
- schema metadata repair needed.

### Sessions

Shows:

- running sessions;
- reusable environment records;
- resume IDs;
- command;
- profile;
- workspace;
- audit path;
- environment reuse.
- audit and runtime cleanup command hints.

Design-ready interactive session observer:

- active run list with start time, command, profile, backend, workspace, and
  current environment;
- live event tail grouped by HostFS, host.open, endpoint exposure, network, and
  cleanup;
- keyboard selection of a session and event row;
- detail view for the selected event using the same redacted Manager/audit view
  as `hideout audit show`;
- explicit commands or plan/apply actions for cleanup, stop, and doctor repair.

The live tail comes from daemon event streams in the steady state. TUI now seeds
once from Manager overview/redacted audit, applies typed daemon events locally,
and polls only in daemon-less fallback.

### Environments

Implemented WebUI smoke surface shows capped reusable environment panels and
offers controlled stop/clean plan/apply actions through Manager API. It also
supports a local profile scope so overview cards, environment/session panels,
and recent audit tails can be narrowed to one profile without changing Manager
state. TUI currently renders a capped dashboard summary with copyable
resume/stop/clean command hints; richer terminal lifecycle controls should call
the same Manager environment endpoints rather than reimplementing store or
backend cleanup logic.

## WebUI Initial Pages

### Audit Explorer

Implemented smoke surface:

- local refresh time;
- UI token expiry time;
- optional profile scope filter;
- audit tail;
- denied audit count;
- recent denied audit list using Manager API `decision=deny` filtering;
- capped session and reusable environment panels so long dogfood histories do
  not dominate the page;
- session audit and runtime cleanup command hints for entries that have audit or
  ephemeral runtime state;
- profile env policy plan/apply without echoing public env values;
- basic audit explorer filtering by session, profile, action, decision, and
  limit using the same redacted Manager API view as `hideout audit show`.

Design-ready search by:

- path;
- rule ID;
- time range.

### HostFS

Current smoke surface shows:

- profile grant and deny counts in overview;
- profile HostFS allow/deny plan/apply through Manager API;
- pending HostFS write decisions with claim/apply/discard controls backed by
  Manager `hostfs/write/*` routes;
- operator decision center panels for actionable decisions and informational
  notices. HostFS write controls remain compatibility controls over the generic
  `hostfs.write` decision record; share/export decisions and privilege/background
  notices are observed through the same live-console reducer state;
- CLI hints for listing and adding durable profile HostFS rules.

Later product views should add:

- run-scoped grants for active sessions;
- recent requested paths;
- deny hits;
- richer add/remove/edit rule actions.

### Network

Shows:

- direct vs tun2socks;
- proxy secret presence;
- route verification status;
- DNS policy status;
- leak risk.

### Bundles

Installed bundle status, permission diff, and verification results. Phase 1
scope follows
[policy-config-supply-chain.md](policy-config-supply-chain.md); marketplace
views are Later.

Adapter packs are a narrower implemented local lifecycle for command adapters:
show installed/revoked/built-in packs, active revisions, test status, digest
locks, and profile binding counts. Public marketplace browsing, publisher
trust, and remote revocation views remain Later.

### Policy Editor

Edit:

- profile env policy through Manager plan/apply;
- HostFS rules through Manager plan/apply;
- command proxy rules;
- network mode;
- policy script refs.

## Security Rules

- UI tokens are short-lived.
- Browser UI renders the token expiry time as metadata so operators can
  distinguish stale UI sessions from runtime failures.
- Browser UI binds to localhost by default.
- Browser UI responses use `Cache-Control: no-store`, `Referrer-Policy:
  no-referrer`, frame denial, and a restrictive CSP that permits the embedded
  inline bundle and same-origin Manager API calls only.
- Hideout-minted control-plane credentials are never rendered;
  user/application data shown locally follows the deterministic redaction
  contract in [privacy-run-design.md](privacy-run-design.md).
- Authority-changing UI actions call Manager plan/apply.
- Every apply operation emits audit.
- TUI and WebUI must not read arbitrary host paths except through Manager Core
  or Manager API operations.

## Product Decisions

- `hideout run` must remain close to local command execution. It may print
  target stdout/stderr and, when requested, a concise verbose summary. It must
  not become the monitoring UI.
- `hideout tui` is the terminal monitoring UI. It should be useful as a
  long-lived observer window while a separate terminal runs the target command.
- `hideout tui --once` is the script and smoke-test mode. It is not the product
  interaction model.
- `hideoutd` improves freshness and interaction; it does not change TUI
  authority. The TUI remains a Manager client.
- Audit search is backed by JSONL scan over the redacted Manager audit view;
  Hideout does not maintain a separate indexed audit store.
- `hideout init` applies safe InitTasks now through the CLI. A future
  interactive TUI wizard must use the same InitTask plan/apply contract rather
  than introducing a second initialization path.
- `hideout init --template ... --no-input` is the scripting and CI path.
- WebUI is not required for first successful run.
- Phase 1 WebUI remains read-only or plan/apply-only through Manager API.
- Bundle marketplace views are Later. Phase 1 needs installed bundle status,
  permission diff, and verification results.

## Phase Plan

MVP delivery order: CLI plus `explain` first, the TUI panel second, the WebUI
after that.

### CLI And Explain First

- `hideout run`, `hideout init`, `hideout doctor`, and `explain` remain the
  primary MVP surface;
- boundary evidence ships through CLI, audit, and Boundary Summary before
  either UI grows.

### TUI Panel

- terminal dashboard over Manager overview;
- persistent local refresh by default;
- explicit `--once` snapshot mode for tests and scripts;
- per-profile env policy counts and CLI hints;
- side-by-side panels with keyboard shortcuts for audit observation and
  session management;
- doctor views;
- controlled Manager plan/apply actions for session and environment lifecycle.

### WebUI Management Surface

- read-only dashboard;
- init plan and apply with next-step rendering;
- controlled run plan and apply;
- audit explorer and session detail;
- profile policy editing through Manager plan/apply;
- environment management;
- profile summary, including expected-command diagnostics and the declared
  guest base image reference.

## Open Questions

- Should WebUI be embedded static assets or served from a separate package?
- Which operations need confirmation in TUI before apply?
