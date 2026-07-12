# Implementation Plan: First-Run Documentation Path

<!-- markdownlint-disable MD013 -->

## Summary

Create one canonical external-alpha walkthrough and a Gate 0 docs smoke that
keeps first-run commands honest. This feature is docs/smoke only: no new
runtime authority, no new API, no new schema.

## Technical Context

- Existing package path: `install.sh`, `hideout package verify`, `hideout package repair`
- Existing onboarding path: `hideout init --template privacy`
- Existing diagnostics: `hideout doctor --level deep --feature <name>`
- Existing operator console: `hideout daemon start`, `hideout tui`, `hideout ui`
- Existing non-claim sources: `docs/STATUS.md`, `docs/threat-model.md`,
  `docs/support-matrix.md`

## Constitution Check

- Principle I: No new authority. PASS.
- Principle II: Redaction/gate wording references existing Go-owned boundaries. PASS.
- Principle III: Fail-closed recovery wording points to existing doctor/package commands. PASS.
- Principle IV: Tests use deterministic docs smoke. PASS.
- Principle V: Lifecycle docs point at existing package/init/run/daemon lifecycle. PASS.

## Structure

```text
docs/first-run-alpha.md
scripts/test-first-run-docs-smoke.sh
specs/020-first-run-documentation-path/
```

## Complexity Tracking

No new code package or framework.
