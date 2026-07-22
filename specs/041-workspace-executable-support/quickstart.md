# Quickstart: Workspace Executable Support

<!-- markdownlint-disable MD013 -->

## 1. Run The Flag Contract

```bash
go test -count=1 ./internal/workspaceattach
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
  go test -c -o /tmp/hideout-041-workspaceattach-linux-arm64.test \
  ./internal/workspaceattach
```

Expected: local tests pass and the Linux arm64 test binary compiles. The shared
test proves the execution hint does not alter wire semantics; the Linux test
binds the rule to go-fuse's actual constant.

## 2. Run The Focused Real Portal Probe

```bash
scripts/test-workspace-portal-lima.sh \
  /tmp/hideout-041-workspace-portal-correctness
```

Expected: `portal-correctness.json` reports `passed`, includes `exec-script` and
`exec-binary`, and its raw outputs show the script and binary executed from the
Portal mount.

## 3. Run The Product Gate

```bash
scripts/test-workspace-executable-lima-e2e.sh \
  --out .hideout-release-evidence/041-workspace-executable-real-gate2
```

Expected: the packaged candidate runs a direct script, Linux arm64 binary, and
workspace-local launcher through the default shared Workspace Portal for at
least 30 samples; checkout effects and negative boundary checks pass; the
product evidence manifest is accepted by the Go evaluator.

## 4. Run Aggregate Gates

```bash
scripts/test-gate0.sh
scripts/test-gate2-lima.sh
```

Expected: Gate 0 passes on the host. The ordinary aggregate Gate 2 keeps its
explicit `/tmp` helper control because it uses static virtiofs; only the 041
feature gate establishes direct shared-Portal execution.

## 5. Verify The Claim Boundary

Confirm retained evidence identifies `workspace-portal` and macOS arm64 Lima.
Dedicated/static virtiofs must remain documented as `not-claimed`; no evidence
may imply a hidden workspace copy, native host execution, or a VM wall between
shared projects.
