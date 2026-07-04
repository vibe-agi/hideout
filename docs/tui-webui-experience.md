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

The TUI is the low-friction local control surface.

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
hideout init
hideout init --no-input
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

- selected profile;
- capability summary;
- backend status;
- network mode and privacy warning;
- active sessions;
- recent denied audit events;
- recent audit events.

Design-ready additions:

- active environment;
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
- resume IDs;
- command;
- profile;
- workspace;
- audit path;
- environment reuse.

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

- audit tail;
- denied audit count;
- recent denied audit list using Manager API `decision=deny` filtering.

Design-ready search by:

- path;
- action;
- decision;
- rule ID;
- session;
- profile;
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

- `hideout init` applies safe InitTasks now and should launch the TUI wizard by
  default when the terminal is interactive and TUI assets are available.
- `hideout init --no-input` is the scripting and CI path.
- WebUI is not required for first successful run.
- Phase 1 WebUI remains read-only or plan/apply-only through Manager API.
- Bundle marketplace views are Later. Phase 1 needs installed bundle status,
  permission diff, and verification results.

## Phase Plan

### TUI First Increment

- terminal dashboard over Manager overview;
- optional local watch refresh;
- doctor;
- sessions;
- HostFS rules;
- recent audit events.

### WebUI First Increment

- read-only dashboard;
- init/tool setup plan and apply;
- controlled run plan and apply;
- audit explorer;
- session detail;
- profile summary.

### Later

- full policy editor;
- interactive OpenTarget topology;
- HostFS overlay diff/review;
- browser-control monitor.

## Open Questions

- Should WebUI be embedded static assets or served from a separate package?
- Which operations need confirmation in TUI before apply?
- Should audit search be backed by JSONL scan first or an indexed store?
