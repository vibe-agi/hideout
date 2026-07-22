# Adversarial Report: Workspace Executable Support

<!-- markdownlint-disable MD013 -->

**Date**: 2026-07-22

**Promotion state**: Implemented and promoted for compatible macOS arm64 Lima
automatic/shared Workspace Portal sessions at clean candidate
`1182fa10bec965cbdecb714faf6f7f9b587221e6`.

## Root-Cause Reproduction

The initial real macOS arm64 Lima Workspace Portal probe directly executed an
executable script from the FUSE mount. The Linux FUSE trace reached:

```text
OPEN ... {EXEC,0x20000}
encode portal open flags 0x20020: operation not supported
```

`0x20` is go-fuse `FMODE_EXEC`; `0x20000` is the Linux arm64 large-file bit
already treated as local-only. The client returned `EOPNOTSUPP` before file
content was read. Copying the same executable to guest-local storage made it
run, which isolated the defect to Portal open-flag validation but was not
accepted as a product workaround.

## Mutation Proof: Execution Hint Removal

The Linux allowlist was temporarily changed from:

```go
... | fuse.FMODE_EXEC | portalIgnoredKernelOpenFlags()
```

to an expression that contributed zero execution-hint bits while retaining a
compilable reference to the constant. Running:

```sh
scripts/test-workspace-portal-lima.sh \
  /tmp/hideout-041-fmode-mutation-20260722-1
```

failed at the first direct workspace script with:

```text
/workspace/guest-check.sh: 43: /tmp/hideout-workspace-portal-research/workspace-exec-script: Operation not supported
```

The mutation was then reverted. This establishes that the positive real probe
depends on the new allowlist assertion rather than an unrelated cache, mount,
or helper change.

## Evidence-Judge Negative Fixtures

`TestWorkspaceExecutableValidatorRejectsFalseGreenArtifacts` starts from one
valid closed artifact and verifies rejection of:

- dirty source identity;
- `virtiofs` relabeled as the promoted mechanism;
- a missing direct-binary check;
- a false no-host-fallback check;
- 29 rather than 30 samples;
- warm p95 above 2,000 ms;
- median regression above 1.10;
- static virtiofs relabeled `supported`; and
- an unknown JSON field.

The test and `TestProofRegistryCovers041WithoutLettingNotRunSatisfyRealClaims`
pass locally. The latter proves the supporting `not-run` proof has no exact-real
runtime policy and cannot satisfy the release-candidate requirement.

## Pre-Promotion Diagnostic Probes

The focused Portal probe with the restored implementation directly executed an
interpreted script and Linux arm64 binary while retaining the existing
read/write/cache-invalidation/path/lock checks. Its diagnostic artifact was
dirty and is not promotion evidence.

The packaged product probe was run with reduced counts and `--probe`. It proved
the real default shared path for direct script, binary, relative launcher,
later-session checkout visibility, permission/missing-interpreter/incompatible
format failures, escaping-link refusal, no host fallback, and disjoint
workspace markers. A four-sample alternating timing diagnostic observed direct
execution p95 of approximately 996 ms and median ratio approximately 0.85
against the same script invoked through guest `/bin/sh`. Probe mode emitted no
product-hardening manifest.

## Final Promotion Evidence

The final source candidate was clean and exact-package-bound. These commands
completed successfully:

```sh
scripts/test-workspace-portal-lima.sh \
  /tmp/hideout-041-workspace-portal-clean-1182fa1
scripts/test-workspace-executable-lima-e2e.sh --require-real \
  --samples 30 --iterations 100 \
  --out .hideout-release-evidence/041-workspace-executable-real-gate2-1182fa1
scripts/test-gate2-lima.sh
scripts/test-gate0.sh
```

The focused Portal artifact reports `dirty:false`, direct script and binary
execution, the full filesystem/cache/lock matrix, and 88.855 ms observed
host-to-guest convergence upper bound. The product artifact reports all 13
closed checks true, 100 executions across two disjoint workspaces, 30 timing
samples, 980.621 ms warm first-output p95, and a 0.983 direct/control median
ratio. The aggregate Lima gate retained its static-virtiofs `/tmp` controls and
completed `gate2: passed`; those controls did not contribute to the 041 proof.
Full Gate 0 then passed all Go packages, Linux cross-build contracts, six TLA+
models, packaging, docs truth, and product smoke lanes.

Retained SHA-256 identities are:

- product-hardening manifest:
  `c84bdaa2a42e16b1bb3e8159d2fcc180590dcbcd3ce7543af70ca1bb8cd9159f`;
- workspace-executable artifact:
  `ecf8a35f4ce570c02edf65f75a8e0e6eca4f8628996db35f0d731ea12c86cfc7`;
- exact packaged candidate:
  `c68b0c4eb9c07970527e40f106032684a16727bd9671de56fc174d156771885b`;
- focused Portal correctness artifact:
  `bc7a10229ae794c9529ee01655f31cd2048fd5b588ec033d19bf7a877736d037`.

The product evidence also binds runtime `developer-standard` revision
`2026.07.0`, runtime artifact SHA-256
`79e5d25bfd05c27b4ee7f2ad085d45c15a63aadbe2ab8d1b4ba2c426e1586134`,
and clean runtime build commit `c51aeed1121426ef4ef8bef15105780a20bc23aa`.

## Aggregate Regression Convergence

Clean aggregate runs exposed adjacent timing and networking regressions rather
than a weakened 041 assertion. The final fixes preserve fail-closed behavior:

- an existing Lima instance retries one ambiguous `limactl start` failure once,
  then still requires all normal runtime, SSH, privilege, and session checks;
- Portal readiness and cleanup use bounded 20-second and 10-second waits;
- trusted-decision visibility keeps the exact deny/approve/revoke assertions
  with a bounded 60-second observation window and last-error diagnostics;
- Lima-local bypass aliases are resolved before DNS mediation and pinned only
  as exactly marked `/etc/hosts` entries, while transient DNS timeouts remain
  bounded and retryable.

The clean aggregate run passed after these fixes, closing FR-016 and SC-007
without accepting an unknown state or removing a negative assertion.

Static/dedicated virtiofs remains `not-claimed` regardless of the shared-Portal
result.
