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
  local, fast, keyboard-first, setup and monitoring

WebUI
  visual, searchable, better for audit and policy editing
```

Both surfaces must use the same Manager Core/API domain resources. WebUI uses
the local Manager API transport; terminal surfaces may call Manager Core
in-process, but must not rebuild state by reading subsystem files directly.

## TUI Role

The TUI is the low-friction local control surface. Phase 1 ships a read-only
dashboard over Manager overview and redacted audit data. Interactive first-run
and doctor flows are product increments, not the current default init path.

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
hideout tui --watch
hideout doctor
hideout doctor --fix --dry-run
```

## WebUI Role

The WebUI is the richer inspection and editing surface.

Best use cases:

- audit search and filtering;
- session timeline;
- HostFS access graph;
- policy editing;
- OpenTarget topology;
- network route and DNS explanation;
- onboarding and documentation.

The WebUI should not be required for first successful `hideout run`.

## Shared Resource Pages

Both TUI and WebUI should represent the same resources:

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
Install
Policy Scripts
```

Surface differences are layout differences, not data model differences.

## TUI Initial Pages

### Dashboard

Implemented smoke surface shows:

- local refresh time;
- selected profile;
- init next steps;
- capability summary;
- backend status;
- network mode and privacy warning;
- recent active sessions;
- recent reusable environments;
- reusable environment lifecycle command hints;
- session audit and runtime cleanup command hints;
- recent denied audit events;
- recent audit events.

Design-ready additions:

- install/doctor warnings.

### Doctor

Shows checks and repair actions:

- missing Lima;
- missing helper binary;
- missing tun2socks;
- invalid profile;
- stale environment;
- schema metadata repair needed.

### HostFS

Shows:

- profile grants;
- profile deny rules;
- run-scoped grants for active sessions;
- recent requested paths;
- deny hits;
- add/remove rule actions.

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

### Environments

Implemented WebUI smoke surface shows capped reusable environment panels and
offers controlled stop/clean plan/apply actions through Manager API. TUI
currently renders a capped dashboard summary with copyable resume/stop/clean
command hints; richer terminal lifecycle controls should call the same Manager
environment endpoints rather than reimplementing store or backend cleanup logic.

### Network

Shows:

- direct vs tun2socks;
- proxy secret presence;
- route verification status;
- DNS policy status;
- leak risk.

## WebUI Initial Pages

### Audit Explorer

Implemented smoke surface:

- local refresh time;
- audit tail;
- denied audit count;
- recent denied audit list using Manager API `decision=deny` filtering;
- capped session and reusable environment panels so long dogfood histories do
  not dominate the page;
- session audit and runtime cleanup command hints for entries that have audit or
  ephemeral runtime state;
- basic audit explorer filtering by session, profile, action, decision, and
  limit using the same redacted Manager API view as `hideout audit show`.

Design-ready search by:

- path;
- rule ID;
- time range.

### Policy Editor

Edit:

- HostFS rules;
- command proxy rules;
- network mode;
- policy script refs.

### Session Timeline

Show:

- command start/end;
- broker events;
- HostFS access;
- OpenTargets;
- network setup;
- doctor warnings.

## Security Rules

- UI tokens are short-lived.
- Browser UI binds to localhost by default.
- No sensitive secret values are rendered.
- Authority-changing UI actions call Manager plan/apply.
- Every apply operation emits audit.
- TUI and WebUI must not read arbitrary host paths except through Manager Core
  or Manager API operations.

## Product Decisions

- `hideout init` applies safe InitTasks now through the CLI. A future
  interactive TUI wizard must use the same InitTask plan/apply contract rather
  than introducing a second initialization path.
- `hideout init --no-input` is the scripting and CI path.
- WebUI is not required for first successful run.
- Phase 1 WebUI remains read-only or plan/apply-only through Manager API.
- Bundle marketplace views are Later. Phase 1 needs installed bundle status,
  permission diff, and verification results.

## Phase Plan

### TUI First Increment

- terminal dashboard over Manager overview;
- optional local watch refresh;

### TUI Next Increment

- doctor;
- sessions;
- HostFS rules;
- recent audit events.

### WebUI First Increment

- read-only dashboard;
- init/tool setup plan and apply;
- init next-step rendering;
- controlled run plan and apply;
- audit explorer;
- session detail;
- profile summary, including tool presets and user-declared npm globals.

### Later

- full policy editor;
- interactive OpenTarget topology;
- HostFS overlay diff/review;
- browser-control monitor.

## Open Questions

- Should WebUI be embedded static assets or served from a separate package?
- Which operations need confirmation in TUI before apply?
- Should audit search be backed by JSONL scan first or an indexed store?
