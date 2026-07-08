# Contract: Compatibility Routes

<!-- markdownlint-disable MD013 -->

## HostFS Write Compatibility

Existing 010 routes remain available:

```text
hideout hostfs write status
hideout hostfs write plan
hideout hostfs write claim
hideout hostfs write apply
hideout hostfs write discard

GET  /api/v1/hostfs/write/status
POST /api/v1/hostfs/write/plan
POST /api/v1/hostfs/write/claim
POST /api/v1/hostfs/write/apply
POST /api/v1/hostfs/write/discard
```

012 requirement:

- compatibility routes read/write the generic `hostfs.write` decision record;
- compatibility routes do not maintain a separate decision lifecycle;
- compatibility responses may preserve 010 field names, but they must include
  enough version/source facts to prove the generic decision center is the source
  of truth;
- claim token rules, timeout rules, provider apply validation, audit, and
  redaction must match the generic route behavior.

## Export Compatibility

Pure local export remains unchanged:

```text
hideout audit export --source audit --out local.json --acknowledge-full-fidelity
```

Share/leaving-machine export adds a decision step:

```text
hideout audit export --share --source audit --out share.json
hideout decision claim <decision-id>
hideout decision approve --claim-token <token> <decision-id>
```

012 requirement:

- no share artifact is released until the decision is approved;
- denial/timeout leaves no released share artifact;
- local-only export does not create a decision record.

## Adapter Proposal Compatibility

Existing `proposal-unavailable` behavior remains for unpromoted capabilities.
When a promoted provider exists, the proposal creates an `adapter.proposal`
decision.

012 requirement:

- JavaScript adapter output never applies authority directly;
- unsupported or undeclared capability proposals fail closed before entering an
  actionable state;
- decision approval still calls a Go-owned provider and may fail closed at
  provider validation time.
