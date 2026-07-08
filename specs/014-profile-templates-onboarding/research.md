# Research: Profile Templates And Onboarding

<!-- markdownlint-disable MD013 -->

## Decision 1: Extend InitTask Instead Of Creating A Separate Writer

**Decision**: Template onboarding extends `inittask.Options`, `Plan`, and
`ApplyMachine`. It does not write profiles directly from `internal/app`.

**Rationale**: Existing `hideout init` already owns store creation, profile
creation, network mode selection, helper discovery, init audit, and next steps.
Keeping templates in this path preserves typed authority and avoids a second
profile mutation surface.

**Grounding**: `internal/app/app.go` dispatches `hideout init` through
`manager.PlanInit` and `ApplyInit`. `internal/inittask/inittask.go` owns
`profile.create`, `network.mode.select`, helper tasks, and init audit.

**Alternatives Rejected**:

- `hideout profile init --template`: too narrow; it would skip store/network
  initialization and evidence.
- Shell onboarding wrapper: violates Go-owned typed authority.

## Decision 2: Template Definitions Are Static Go Data

**Decision**: Built-in templates live in `internal/profiletemplate` as static Go
definitions and render by transforming `profile.Default(name)`.

**Rationale**: Templates are product posture presets, not marketplace content.
Static Go definitions let tests assert every template validates and that no
template grants HostFS or adapter-pack authority by default.

**Alternatives Rejected**:

- JSON files in docs or scripts: easier to drift and harder to validate in
  compile-time unit tests.
- Adapter-pack-provided templates: out of scope; 011 packs are optional and
  cannot shape first-run defaults.

## Decision 3: `privacy` And `hardened` Require Tun2socks Inputs In CI

**Decision**: Non-interactive `privacy` and `hardened` onboarding require
explicit `--backend`, `--network tun2socks`, `--proxy-secret`, and
`--mediated-resolver`.

**Rationale**: `privacy` is the recommendation, but external alpha setup must
not silently guess proxy authority or DNS mediation details, and must not allow
`direct` to satisfy a privacy-named posture. Existing init normalization
already fails `tun2socks` without a proxy secret; 014 adds the mediated resolver
carrier to the same explicit-choice rule.

**Alternatives Rejected**:

- Default direct network for privacy: undermines the template name.
- Allowing `privacy --network direct`: creates an overclaiming profile.
- Default public resolver without operator input: surprises users and weakens
  auditability.

## Decision 4: Hardened Is Enforced-Only

**Decision**: `hardened` succeeds only when privilege status is `enforced`.
`degraded` or `unknown` fail before profile creation unless the operator
explicitly requests a degraded fallback that is visibly marked in metadata and
evidence.

**Rationale**: 009 established `enforced`, `degraded`, and `unknown` privilege
states and prohibited converting degraded state into a boundary claim. The
template name must not reintroduce the old overclaim.

**Alternatives Rejected**:

- `hardened` plus warning: repeats the 009 naming problem.
- Silent fallback to privacy: hides an important security fact.

## Decision 5: Interactive Prompt Is A Thin Consumer Of The Same Plan

**Decision**: CLI interactive mode builds the same onboarding request as
non-interactive mode, shows a review, asks for confirmation, and then calls the
same manager plan/apply path.

**Rationale**: Interactive behavior must be testable without changing product
semantics. Prompt text can be tested through injected stdin/stdout while profile
output is byte-equivalent to non-interactive choices.

**Alternatives Rejected**:

- Interactive-only wizard state machine: hard to reuse from CI and tests.
- Daemon prompt channel: 006 explicitly did not claim daemon-mediated prompts.

## Decision 6: Evidence Summary Is Local Profile Evidence

**Decision**: Successful onboarding writes
`profiles/<name>/onboarding-evidence.json` and appends init audit as today.
Cancellation and failed confirmation write no success evidence summary.

**Rationale**: Evidence belongs with the profile it describes. A durable local
JSON file is easy for docs, smoke, and future export surfaces to inspect.

**Alternatives Rejected**:

- Only CLI output: not durable.
- Only init audit: too noisy and task-oriented for first-run posture review.

## Decision 7: Docs Lead With Packaged Alpha Onboarding

**Decision**: README/docs show `hideout init --template privacy ...` as the
recommended path and label advanced customization separately.

**Rationale**: 013 made packaged commands the alpha product path. 014 should
not send first-run users back to raw profile JSON or source checkout flows.

**Alternatives Rejected**:

- Keep docs centered on `profile init`: less safe and less explanatory.
- Hide dev/debug templates: useful for local development when honestly labeled.
