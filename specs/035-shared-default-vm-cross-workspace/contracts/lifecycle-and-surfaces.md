# Contract: Lifecycle And Product Surfaces

## Resource Topology

Required closed kinds:

```text
workspace.host-provider
workspace.guest-view
```

The accepted Portal topology has no separate workspace environment service.
The host provider is Manager-owned and may serve multiple session views for
the same running backend incarnation. Each guest view remains session-owned.
`workspace.environment-service` is therefore not a production kind.

Measured dependency shape:

```text
workspace.host-provider        -> backend.incarnation (drain)
workspace.guest-view           -> backend.incarnation (drain)
workspace.guest-view           -> run.session (drain)
workspace.guest-view           -> workspace.host-provider (drain)
```

Both kinds are ephemeral and close before backend stop. The host provider uses
its own absence probe because session absence is not proof for a provider that
can survive one guardian. The guest view has a distinct probe so restart
reconciliation never infers its absence from provider state.

## Stop And Restart

- Planned attach cancels idle grace under existing 036 serialization before
  provider side effects.
- Attach during stop waits or returns the existing typed stop result; it cannot
  create a replacement VM.
- Automatic stop remains the 036 graph predicate plus grace and exact-
  incarnation backend observation.
- Restart never re-adopts old attachment authority or tokens.
- A provider probe may prove absence; it cannot grant a live view.
- Unproved provider/view state blocks attach, reuse, and automatic stop until
  existing explicit non-destructive recovery resolves the incarnation.

## Environment Summary

Shared rows contain:

- environment ID/name/mode/shared-slot label;
- machine compatibility ID and backend instance/lifecycle state;
- active session and workspace-view counts;
- selected transport service state when real; and
- redacted blockers.

They contain no selected, last, empty-placeholder, or actionable workspace.

Dedicated/workspace-bound rows may render their pinned binding under the
existing operator-local path boundary.

## Session Summary

Session rows contain environment/session/workspace IDs, a non-authoritative
basename-plus-short-ID label, logical guest root, transport, view state,
relation notices and redacted blockers. Profile filtering applies to these rows
and their actions.

No guest/shared summary contains canonical host root, root identity, identity
key, provider credential, socket, descriptor, or raw control path.

## Events And Audit

- Events identify machine and workspace-view transitions separately.
- A workspace label is never authority and cannot be sent back to select a root.
- Operator-local audit may record user paths verbatim under the existing local
  evidence contract and correlates by environment/session/workspace ID.
- Export/share continues through the existing lossy decision boundary.
- Lifecycle diagnostics carry stable code, reason, hint, next actions and
  evidence references without target-controlled control material.

## Manager And UI Parity

CLI, TUI, WebUI and automation consume the same Manager summaries and actions.
Runtime tests must show two simultaneous workspace rows under one environment,
correct profile scoping, relation notices, blockers, and no hidden steady-state
polling when the healthy event stream is active.

## Trust-Domain Notice

Shared mode discloses that sessions reuse one guest kernel, root disk, global
tools/caches, profile-mounted state and compatible VM-global services. Private
workspace views are not separate VM walls. Guidance provides:

- one named-environment command for a distinct VM/kernel/root disk; and
- a distinct-profile plus named-environment recipe when profile-owned state
  must also be separate.
