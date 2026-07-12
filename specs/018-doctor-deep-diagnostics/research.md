# Research: Doctor Deep Diagnostics

<!-- markdownlint-disable MD013 -->

## Decision 1: Treat 018 as an Incremental Doctor Upgrade

**Decision**: Reuse the existing `internal/doctor.Report` and `internal/app.app.doctor` pipeline from 015 rather than introducing a new diagnostic runner.

**Rationale**: `internal/doctor/report.go` already owns finding ids, status normalization, severity, details, next actions, evidence refs, JSON output, evidence writes, and deterministic redaction. `internal/app/app.go` already collects the local facts needed for light checks and feature diagnostics.

**Alternatives considered**:

- Create `internal/doctor/deep` as a second runner. Rejected because it would duplicate rendering/redaction contracts and make human/JSON parity harder.
- Move every existing app-level check into `internal/doctor` first. Rejected as a broad refactor unrelated to 018's troubleshooting goal.

## Decision 2: Human Output Must Be Rendered From the Same Findings

**Decision**: Feature/deep findings must appear in the shared builder and in human output using the same check ids/status/summary/next actions as JSON.

**Rationale**: Current feature diagnostics are builder-only, so JSON can contain findings that human output never shows. 018 FR-012 requires parity.

**Alternatives considered**:

- Keep feature diagnostics JSON-only. Rejected because operators using default human output would miss the deep facts.
- Maintain separate human strings. Rejected because it recreates the parity problem 018 is meant to fix.

## Decision 3: Findings Use Four Statement Buckets

**Decision**: Deep findings use structured `details` keys for `observedFacts`, `candidateCauses`, `gateRequired`, and `nextActions`, while preserving existing top-level `nextActions`.

**Rationale**: This keeps the JSON report stable and machine-checkable without changing every existing finding. It also prevents "likely cause" language from becoming a fake root-cause claim.

**Alternatives considered**:

- Add new top-level fields to every finding. Rejected as wider schema churn than needed.
- Put all text in summary. Rejected because tests could not distinguish facts from candidates or gate markers.

## Decision 4: Feature Diagnostics Are Local Facts or Gate Markers

**Decision**: Each supported feature selector must emit a real local finding or an explicit gate-required marker. DNS, HostFS, Lima/backend, and privilege checks may name the required Gate 2/Gate 3 proof but must not run it.

**Rationale**: Doctor should narrow troubleshooting without substituting local weak harness checks for real release evidence.

**Alternatives considered**:

- Run real backend probes in deep mode. Rejected because it changes doctor from local diagnostics into a slow side-effecting test runner.
- Omit unsupported facts. Rejected because selected features would appear to pass silently.

## Decision 5: Package Diagnostics Reuse 017 Facts

**Decision**: Packaging diagnostics reuse package verification, installed-state migration fields, obsolete package-owned state, repair guidance, and external prerequisite status from 017.

**Rationale**: 017 already separated package-owned files from external prerequisites such as `tun2socks`. Re-reading or recomputing those facts independently risks drift.

**Alternatives considered**:

- Treat all missing helpers as package failures. Rejected because `tun2socks` is explicitly external.
- Ignore obsolete package-owned state until verify. Rejected because 018 should surface actionable repair guidance.

## Decision 6: Decision Diagnostics Remain Non-Secret Counts

**Decision**: Decision diagnostics report aggregate counts for pending, claimed, terminal, timeout-risk, stale-claim, and notices without exposing claim tokens, provider-private refs, or decision payloads.

**Rationale**: Decision queue state is useful for support, but raw decision data can contain user paths, provider refs, or sensitive context.

**Alternatives considered**:

- Dump raw decisions in deep mode. Rejected because it violates the redaction boundary and makes deep doctor unsafe for support.
- Report only total count. Rejected because stale claimed and timeout-risk states need specific next actions.

## Decision 7: Recovery Guidance Is Mostly Advice

**Decision**: 018 does not add new automatic repair. Existing typed safe repairs stay available; package leftovers, stale decisions, gate-required proof, and unsafe cleanup produce next-action commands.

**Rationale**: Doctor should not become a package deleter, environment re-creator, HostFS mutator, or release gate runner. 017 package repair already owns stale file removal.

**Alternatives considered**:

- Add `doctor --fix` handlers for package repair and decision cleanup. Rejected because those have their own typed commands and approval semantics.

## Decision 8: Redaction Tests Must Inject Representative Secrets

**Decision**: Tests inject synthetic broker/UI/claim token shapes, `HIDEOUT_SECRET_*` backing values, proxy URLs, generated machine ids, and runtime credential paths into summaries/details/next actions/evidence refs.

**Rationale**: Empty scans are not proof. 007 and 011-016 reviews showed static grep or absent secret values can create false confidence.

**Alternatives considered**:

- Rely on existing audit redaction unit tests. Rejected because 018 must prove every doctor output mode uses that boundary.

## Decision 9: Exit Semantics Stay Conservative

**Decision**: Warning/degraded findings exit zero unless selected as required; required local errors exit nonzero. Gate-required markers are not release failures by themselves.

**Rationale**: Deep doctor is a troubleshooting tool, not a release gate. It should be useful in CI without making unsupported local proof look like a failed privacy claim.

**Alternatives considered**:

- Make every deep warning nonzero. Rejected because it would make degraded but expected local setups unusable.
- Always exit zero. Rejected because local required errors still need CI visibility.
