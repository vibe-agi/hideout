# Research: Documentation Truth Gate

<!-- markdownlint-disable MD013 -->

## Decision 1: Use A Human-Readable Claim Boundary Registry

**Decision**: Add `docs/claim-boundaries.md` with compact claim rows and proof
references.

**Rationale**: Reviewers need a readable map from product claims to evidence.
Embedding this only in scripts would hide the policy.

**Alternatives considered**: A JSON-only registry. Rejected because it is harder
to review and duplicates STATUS.

## Decision 2: Curated Command Fixtures First

**Decision**: Add `docs/command-examples.json` for commands the truth gate
checks.

**Rationale**: Extracting every Markdown fence is noisy and unsafe for commands
that mutate state or require real gates. Curated fixtures make safety explicit.

**Alternatives considered**: Full Markdown extraction. Deferred until patterns
stabilize.

## Decision 3: Scan Current Docs Strictly, Historical Specs Narrowly

**Decision**: Scan README, localized README, docs, and specs 021-025 strictly;
old specs are checked only for banned current-product overclaim classes.

**Rationale**: Historical specs contain superseded trails. The gate should catch
dangerous overclaims without forcing rewrites of all history.

## Decision 4: Localized README Is Best-Effort

**Decision**: `README.md` is canonical; `README.zh-CN.md` must visibly declare
that status unless it is maintained in strict parity.

**Rationale**: The Chinese README currently lags fast-moving alpha docs. A
clear canonicality statement is more honest than silent drift.
