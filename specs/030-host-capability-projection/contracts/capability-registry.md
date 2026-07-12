# Contract: Capability Registry & `host.app.open-resource`

<!-- markdownlint-disable MD013 MD060 -->

## Registry (Core-owned, static)

- Package `internal/hostcap` exposes `Registry()` returning a static, package-owned set of `CapabilityDescriptor`. No runtime registration API is exported.
- Lookup by capability id returns the descriptor and its Go provider (`providerRef` → func). Unknown id → typed error (`projection.command.unbound` upstream).
- Registry validation (at package init / a `Validate()` covered by test): unique ids, known enums, resolvable `providerRef`, and every `design-ready` descriptor is refused if reached at dispatch.

## v1 descriptors

| id | riskClass | resultPolicy | resourceKinds | decisionPolicy | status |
|----|-----------|--------------|---------------|----------------|--------|
| `host.app.open-resource` (safe) | low | none | [workspace] | default-allow-audited | implemented |
| `host.app.open-resource` (trusted-host-ide facet) | high | none | [workspace] | operator-grant | implemented |
| `host.service.bridge` (adb) | high | lease | [device,endpoint] | operator-grant | design-ready |
| `host.automation.invoke` (applescript) | high | bounded-typed | [] | operator-grant | design-ready |

> The two facets of `host.app.open-resource` are one capability with mode-dependent risk/decision policy (safe = low/default-allow-audited, trusted = high/operator-grant). Design-ready rows exist in the model to prove the registry accommodates the full vision; they MUST fail closed if dispatched in v1.

## Provider contract: `host.app.open-resource`

Input: a validated `OpenResourceIntent` (see `open-resource-intent.schema.json`) plus the session context (workspace root, profile, session id, active IdeMode).

Behavior (Go, Core):

1. Re-decode intent strictly (unknown fields rejected).
2. Resolve `AppRef` through the Core app-identity registry → host app + safe/trusted launch profile. Absent → `projection.app.absent`; drift → `projection.app.identity-drift`.
3. For each `ResourceRef`: map to host path under the session workspace root, `EvalSymlinks` escape recheck, confirm existence. Escape / guest-only / missing → `projection.path.no-host-mapping`.
4. Enforce mode: `safe` (low) launches with the isolated safe launch profile; `trusted-host-ide` (high) requires a live operator grant, else `projection.mode.trusted-denied`.
5. Launch the host app (result policy `none`; no host output returned to guest).
6. De-duplicate/rate-limit identical `(appRef, host target, window mode)` within a short window.
7. Emit the `ide.open` audit record (no host path / username / token / raw argv).

Output to guest: `{outcome: "launched" | "refused", code?: "<recovery code>"}`. Never a host path.

## Invariants (test obligations)

- No generic fallback: any failure in steps 1–5 refuses; never delegates to host execution or a shadowed guest binary.
- Host identity only from Core registry: `AppRef` is an id; a binary path / bundle id supplied in the intent is rejected.
- Result policy `none`: the guest receives no host bytes beyond outcome/code.
- Redaction: audit + any exported evidence contain no host path/username/token/argv.
