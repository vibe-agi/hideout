# Quickstart: HostFS And Decision E2E

<!-- markdownlint-disable MD013 -->

## Scenario 1: Local-Fast HostFS Decision Proof

Purpose: prove local decision lifecycle and visibility without claiming real
HostFS guest data-plane behavior.

Command:

```bash
scripts/test-hostfs-decision-e2e.sh --local-fast --out /tmp/hideout-hostfs-decision
go run ./cmd/hideout-schema-validate \
  schemas/product-hardening-evidence.schema.json \
  /tmp/hideout-hostfs-decision/product-hardening-evidence.json
```

Expected:

- evidence contains all required local-fast 023 proof ids;
- claim race has one winner and one loser;
- approve, deny, and timeout/default-deny outcomes are represented;
- CLI/API/WebUI-model/TUI-model visibility artifacts agree on decision state;
- public artifacts contain no claim tokens, provider-private refs, private
  overlay object ids, or control-plane material;
- evidence does not claim real Gate 2 HostFS proof.

## Scenario 2: Real Gate 2 HostFS Proof Is Explicit

Purpose: prove real guest HostFS staging only when prerequisites exist.

Command:

```bash
scripts/test-hostfs-decision-e2e.sh --real-gate2 --out /tmp/hideout-hostfs-real
```

Expected without prerequisites:

- evidence contains `023.hostfs-decision.real-gate2.not-run`;
- missing prerequisites are named;
- no native/local-fast pass substitutes for real proof.

Expected with prerequisites:

- evidence contains `023.hostfs-decision.real-gate2.lifecycle`;
- guest reads staged content before apply;
- host lower state is unchanged before apply;
- apply changes only the planned host path;
- coverage matrix lists exactly which write classes were proven.

## Scenario 3: Real Gate 2 Required

Purpose: support release-style/manual runs that require real HostFS proof.

Command:

```bash
scripts/test-hostfs-decision-e2e.sh \
  --real-gate2 \
  --require-real \
  --out /tmp/hideout-hostfs-real-required
```

Expected:

- missing prerequisites exit non-zero and still write `not-run` evidence;
- a passing result requires actual real Gate 2 execution.

## Scenario 4: Conflict Fails Closed

Purpose: prove host lower mutation between staging and apply blocks apply.

Command:

```bash
scripts/test-hostfs-decision-e2e.sh \
  --local-fast \
  --operation replace \
  --out /tmp/hideout-hostfs-conflict
```

Expected:

- staged content is recorded;
- fixture mutates the lower host file before apply;
- apply fails closed with conflict/stale status;
- lower file keeps the conflicting content;
- evidence records failed/denied apply, not success.

## Scenario 5: Gate 0 Integration

Purpose: keep local HostFS/decision E2E in the normal local gate.

Command:

```bash
scripts/test-gate0.sh
```

Expected:

- local-fast 023 proof runs or is explicitly represented by the HostFS write
  overlay smoke;
- real Gate 2 is not required by Gate 0;
- docs/test-plan distinguish local-fast and real HostFS proof.
