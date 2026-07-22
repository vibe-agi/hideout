# Adversarial Report: Workspace Executable Support

<!-- markdownlint-disable MD013 -->

**Date**: 2026-07-22

**Promotion state**: Implementation candidate; clean exact-package evidence is
not recorded until the candidate commit and full gates pass.

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

## Dirty Diagnostic Probes

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

## Remaining Promotion Evidence

Before 041 is promoted, retain and identify:

1. full Gate 0 on the candidate commit;
2. restored focused Portal correctness evidence;
3. `scripts/test-workspace-executable-lima-e2e.sh --require-real` with at least
   30 samples and 100 disjoint executions on a clean exact package;
4. the integrated `scripts/test-gate2-lima.sh` with its static-virtiofs `/tmp`
   controls explicit and unable to satisfy the 041 proof; and
5. final artifact/package/manifest hashes plus a converged FR/SC/task audit.

Static/dedicated virtiofs remains `not-claimed` regardless of the shared-Portal
result.
