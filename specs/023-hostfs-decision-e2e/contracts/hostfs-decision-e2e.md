# Contract: HostFS Decision E2E Runner

<!-- markdownlint-disable MD013 -->

## Command Shape

```bash
scripts/test-hostfs-decision-e2e.sh \
  [--local-fast|--real-gate2] \
  [--require-real] \
  [--out <dir>] \
  [--operation <name>] \
  [--package <path>]
```

## Modes

### Local-Fast

Required behavior:

- creates temporary host fixtures and a temporary store;
- stages deterministic HostFS write decisions through existing overlay and
  decision code paths where possible;
- proves CLI/API decision visibility;
- proves generic decision claim/resolve/timeout behavior;
- proves WebUI/TUI model visibility without claiming browser click proof;
- writes `hideout.product-hardening-evidence/v1`;
- validates schema and redaction.

Pass claims allowed:

- decision state correctness;
- claim race/loser observation;
- approve/deny/timeout semantics;
- public redaction;
- local model visibility.

Pass claims prohibited:

- real Linux guest FUSE behavior;
- real Gate 2 HostFS data-plane proof;
- guest-root containment;
- workspace DLP/blocking.

### Real Gate 2

Required behavior when prerequisites are available:

- uses the existing Gate 2 Lima HostFS path;
- proves guest reads staged content before apply;
- proves host lower file or directory is unchanged before apply;
- applies through existing HostFS write claim/apply;
- proves final host lower state matches the staged plan;
- emits product-hardening evidence with real-gate coverage.

When prerequisites are unavailable:

- writes `not-run` proof with prerequisite names;
- exits zero unless `--require-real` is set;
- exits non-zero with `--require-real`;
- must not fall back to native/local-fast as a real Gate 2 pass.

## Required Proof IDs

Local-fast:

- `023.hostfs-decision.local-fast.lifecycle`
- `023.hostfs-decision.local-fast.claim-race`
- `023.hostfs-decision.local-fast.timeout`
- `023.hostfs-decision.local-fast.visibility`
- `023.hostfs-decision.local-fast.redaction`

Real Gate 2:

- `023.hostfs-decision.real-gate2.lifecycle`
- `023.hostfs-decision.real-gate2.not-run`

Optional expanded proof ids may append operation names, for example
`023.hostfs-decision.real-gate2.replace`.

## Artifact Requirements

Each proof entry must include:

- command summary;
- backend/mode;
- covered claims;
- prerequisite status;
- redaction status;
- referenced artifacts with sha256 where files exist;
- operation coverage matrix or a linked artifact containing it.

## Fail-Closed Conditions

The runner must fail or write failed evidence when:

- local-fast pass proof contains real Gate 2 claims;
- real mode falls back to native;
- host lower changes before apply;
- guest/read view does not reflect staged content for a claimed real operation;
- apply mutates an unexpected host path;
- stale/conflict apply succeeds;
- two claimants both win;
- timeout/default-deny applies provider side effects;
- public artifacts contain claim tokens, provider refs, private overlay object
  ids, broker/UI tokens, `HIDEOUT_SECRET_*`, generated machine-id material, or
  control-plane field names.
