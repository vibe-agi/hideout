# Contract: Doctor And Package Recovery E2E Runner

<!-- markdownlint-disable MD013 -->

## Command Shape

```bash
scripts/test-doctor-package-recovery-e2e.sh \
  [--local-fast] \
  [--out <dir>] \
  [--package <path>]
```

## Local-Fast Mode

Required behavior:

- run the existing package repair path or package smoke fixture;
- prove stale package-owned leftovers are detected before repair;
- prove package repair dry-run does not mutate;
- prove package repair apply removes only package-owned obsolete files;
- prove package verify passes after repair;
- run the existing doctor diagnostic/recovery path or doctor smoke fixture;
- prove doctor deep guidance and safe repair dry-run/apply behavior;
- export one selected doctor report through the 005 export boundary;
- write `hideout.product-hardening-evidence/v1`;
- validate schema and redaction.

Pass claims allowed:

- package verify/repair loop correctness;
- safe doctor fix loop correctness;
- guidance-only doctor finding behavior;
- human/JSON/export redaction.

Pass claims prohibited:

- release readiness;
- real Gate 2/Gate 3 proof;
- automatic dependency installation;
- unsafe repair success;
- guest-root containment or network/DNS closure.

## Required Proof IDs

- `024.recovery.package.repair-loop`
- `024.recovery.doctor.safe-fix-loop`
- `024.recovery.doctor.guidance-only`
- `024.recovery.redaction`

## Artifact Requirements

Each proof entry must include:

- command summary;
- local mode;
- covered claims;
- prerequisite status;
- redaction status;
- referenced package/doctor logs or JSON reports.

## Fail-Closed Conditions

The runner must fail when:

- package verify accepts a proven obsolete package-owned file;
- package repair dry-run removes a file;
- package repair apply removes unrelated files or durable store state;
- verify after package repair does not pass;
- doctor fix dry-run mutates profile/install state;
- doctor fix apply runs without explicit apply mode;
- guidance-only findings are counted as fixed or release-ready;
- doctor export fails schema validation;
- public artifacts contain control-plane material.
