# TUI Interaction Contract

## Product shape

`hideout tui` is a live operator HUD, not a dump of internal counters. It uses
one screen, progressive disclosure, consistent keyboard actions, and the same
Manager snapshot/events/plans/operations as CLI and WebUI.

Default layout at a normal terminal size:

```text
┌ Hideout ─ default ─ LIVE ─ session ses_… ─────────────────────── 14:32:08 ┐
│ COMMAND  claude                         CONNECTION  proxy · 1.1.1.1       │
│ STATE    running · 2m14s                COVERAGE    3 available · DNS partial│
│ RISK     HIGH · wrote outside workspace NEXT        inspect file activity │
├ Activity ────────────────────────────────┬ Details ────────────────────────┤
│ 14:32:05 file write  /workspace/a.go  3 │ process  gofmt (pid 182)         │
│ 14:32:03 connect     api.example:443     │ evidence exact · TCP 104.18…    │
│ 14:32:02 exec        git status          │ coverage network available      │
│ …                                          rule file.outside-workspace/v1 │
├ [1] Overview [2] Activity [3] Config [4] Operations [5] Help ─────────────┤
│ ↑↓ select  Enter inspect/edit  / filter  r refresh  ? keys  q quit         │
└─────────────────────────────────────────────────────────────────────────────┘
```

For narrow terminals, Details becomes a full-screen drill-down. Below the
minimum supported size, the UI shows the required dimensions and remains
quit/help capable rather than drawing corrupt content. Color is supplementary;
icons always have text equivalents.

## Views

### Overview

Prioritizes:

1. active top-level command and elapsed/terminal state;
2. effective connection route and transition;
3. coverage by process/file/network/DNS;
4. highest actionable risk/blocker;
5. one safe next action;
6. session/environment identity and stream freshness.

Internal counters, helper generations, mount IDs, and lifecycle journals are
hidden behind Details/Diagnostics.

### Activity

Tabs or filters: `All`, `Commands`, `Files`, `Network`, `DNS`, `Risks`.
Rows show time, actor/command, operation, subject, count, and attribution.
Enter opens correlated detail: execution ancestry, exact/inferred evidence,
coverage interval, risk rule, and next action. `/` filters; filters are
client-local and never mutate Manager state.

### Config

Shows desired, effective, transition, and scope (`live`, `new sessions`,
`restart required`) in separate columns/labels. Selectable fields come only
from Manager capabilities and command catalog. Enter opens an editor modal.

### Operations

Shows active and recent operation ID, kind, owner, phase, effects, evidence,
result, and recovery. Response loss is recoverable by selecting the existing
operation; the UI never offers an unbound “try again” that creates a new ID.

### Help

Task groups, searchable commands, copyable examples, prerequisites, effects,
risk, and recovery are rendered from the command catalog. `?` always opens
contextual keys for the current view/modal.

## Mutation modal state machine

Every configuration/secret control follows:

```text
closed
  -> editing-draft
  -> planning
  -> review
  -> confirming
  -> applying
  -> terminal
```

### Editing

- The draft is TUI memory only.
- Escape cancels with no effect.
- Field validation is immediate but Manager validation is still authoritative.
- Secret inputs are masked, do not support copy-to-clipboard, and are cleared
  on cancel/apply/quit. The value never appears in review.

### Review

The modal must show:

- before and after;
- live/new-session/restart scope;
- affected profiles/environments/sessions/connections;
- blockers and warnings;
- stage/activate/drain/rollback effects;
- operation ID and plan expiry;
- exact recovery if it fails.

The confirm key is disabled for a blocker, stale projection, expired plan, or
changed draft. Editing after review discards the plan and returns to draft.

### Confirm/apply

Enter alone never applies a reviewed change. The review footer uses a distinct
`Apply` action followed by an explicit confirmation (typed profile name for
high-risk effects; `y` for routine effects). Apply sends the exact operation
ID, plan digest, and revision. During transition, closing the modal does not
cancel an owned operation; Operations remains the recovery surface.

### Terminal

Success appears only from a Manager terminal operation with required evidence.
Rolled back, failed, blocked, or unproved are visually and textually distinct.
The modal provides a catalog-backed safe next action.

## Stream health and stale behavior

Header states:

- `LIVE`: matching snapshot and contiguous authenticated event stream;
- `IDLE LIVE`: healthy, no recent events;
- `STALE`: gap, instance/schema mismatch, or refresh in progress;
- `DISCONNECTED`: stream closed;
- `CREDENTIAL EXPIRED`: re-authentication required;
- `DAEMONLESS`: one-shot local read-only compatibility.

Any state except `LIVE`/`IDLE LIVE` is read-only. Open drafts may remain visible
but cannot plan or apply. Reconnection always obtains a fresh snapshot; it
never replays local deltas over new state.

## Keyboard contract

Global:

| Key | Action |
| --- | --- |
| `1..5` | Switch primary view |
| `Tab` / `Shift+Tab` | Next/previous focus region |
| `↑↓` or `j/k` | Select |
| `Enter` | Inspect selected row or edit configurable field |
| `Esc` | Close modal/back/cancel draft |
| `/` | Filter current list |
| `r` | Re-seed when allowed; never blind mutation retry |
| `?` | Context help |
| `q` | Quit when no text/modal intercepts it |
| `Ctrl+C` | Safe quit; does not stop daemon/session/operation |

No single-key destructive shortcut applies an effect. Keys displayed in the
footer are the complete primary set for the current state.

## Command behavior

- `hideout tui`: interactive alternate-screen HUD.
- `hideout tui --profile NAME`: initial profile scope.
- `hideout tui --session ID`: initial session/activity scope.
- `hideout tui --once`: deterministic plain, no alternate screen, read-only.
- `hideout tui --json`: rejected; structured automation uses Manager API or
  explicit CLI query commands.
- non-TTY interactive invocation: print a short recovery message and suggest
  `--once`.

## Accessibility and terminal safety

- Respect `NO_COLOR` and terminal color capability.
- Never rely on red/green alone; include state words and symbols.
- Sanitize all observed strings before rendering: no control sequences, OSC,
  hyperlinks, bidi controls, or unbounded width.
- Bound frame rate and coalesce activity updates without hiding Manager event
  sequence.
- Restore terminal state on normal exit, signal, panic boundary, failed init,
  or daemon disconnect.
- Do not intercept target PTY input; the TUI is a separate operator client.

## Parity requirements

For the same fixture, CLI, TUI, and WebUI must expose identical:

- profile revision, desired/effective/transition values;
- plan digest, effects, blockers, and operation terminal result;
- active command/session state;
- coverage state/reason/interval;
- risk rule/source/confidence;
- secret availability/generation;
- connection route and blocking sessions.

Presentation and navigation may differ. Authority, facts, and success semantics
may not.

## PTY and interaction fixtures

Tests cover:

- resize across normal/narrow/below-minimum dimensions;
- mouse disabled by default and keyboard-only completion;
- modal draft/cancel/review/confirm/apply;
- masked secret input and terminal restoration;
- stale/gap/disconnect during edit, review, and apply;
- response loss followed by operation lookup;
- slow event burst and bounded frame rate;
- control-sequence/path injection;
- `NO_COLOR`, light/dark palettes, Unicode-disabled fallback;
- `--once` stable output and non-TTY recovery.
