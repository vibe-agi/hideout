# Research: First-Run Documentation Path

<!-- markdownlint-disable MD013 -->

## Decision 1: Primary Path

Use privacy on Lima as the canonical external-alpha path.

Rationale: `docs/STATUS.md` identifies Lima as the primary product path and
native as a weak harness. Privacy requires proxy/DNS inputs but matches the
product's actual security story.

## Decision 2: Native Placement

Show native only as a fast development harness.

Rationale: Native does not produce isolation evidence and must not be a default
alpha quickstart.

## Decision 3: Proof Boundary

The first-run page must not imply release readiness.

Rationale: Release readiness is controlled by support/readiness evidence and
real Gate 2/Gate 3 proofs, not by a local walkthrough.

## Decision 4: Smoke Scope

Use text scans for canonical commands and overclaim markers.

Rationale: 020 does not change behavior; deterministic docs smoke is the right
gate and keeps examples from drifting.
