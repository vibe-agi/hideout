# Contract: First-Run E2E Script

## Command Shape

022 introduces or upgrades a local proof runner:

```bash
scripts/test-first-run-e2e.sh \
  [--local-fast] \
  [--real-backend] \
  [--require-real] \
  [--out <dir>] \
  [--package <path>]
```

Default behavior:

- Build or locate a package artifact.
- Extract it into a temp package root.
- Run the packaged `install.sh` with `--skip-init`.
- Add the installed prefix to PATH.
- Run installed `hideout package verify <prefix>`.
- Run the selected explicit `hideout init ...` command exactly once.
- Run one low-risk installed-binary command.
- Capture audit and Boundary evidence.
- Write `product-hardening-evidence.json`.

## Modes

### Local-Fast

Local-fast mode is the default Gate 0 path.

Required behavior:

- May use native backend.
- Uses a weak/dev profile, not the privacy template with native/direct.
- MUST mark evidence as weak/native/dev-only.
- MUST NOT claim Lima, DNS mediation, HostFS isolation, or privilege
  separation evidence.
- MUST pass only when install, verify, init, run, audit, Boundary, docs order,
  and redaction checks pass.

### Real Backend

Real-backend mode is explicit.

Required behavior:

- Uses the documented Lima/privacy posture.
- Checks prerequisites before running.
- If prerequisites are absent, writes `not-run` evidence.
- If `--require-real` is set, missing prerequisites make the script exit
  non-zero.
- MUST NOT fall back to native and still pass.

## Failure Fixtures

The implementation must provide targeted fixture modes or test cases for:

- duplicate `default` profile before documented init;
- missing package manifest;
- package checksum mismatch;
- missing package-owned helper;
- stale package-owned obsolete file;
- unsafe or reserved workspace/store path;
- real-backend prerequisites missing.

Each fixture must produce failed or `not-run` evidence and no passing proof for
the affected claim.

## Evidence Output

Output directory contents:

```text
<out>/
├── product-hardening-evidence.json
├── logs/
│   ├── install.log
│   ├── verify.log
│   ├── init.log
│   ├── run.stdout
│   ├── run.stderr
│   └── audit.log
└── reports/
    ├── docs-order.txt
    └── prerequisites.json
```

Artifact names may differ, but the manifest must reference every artifact
needed to prove a passing first-run claim.

## Fail-Closed Rules

- Missing artifact or schema validation failure blocks pass evidence.
- `install.sh` without `--skip-init` is not the canonical 022 path.
- Duplicate profile blocks clean first-run evidence.
- Missing audit or Boundary evidence blocks the audit/Boundary proof.
- Raw control-plane material in evidence blocks pass evidence.
- Real backend mode without real backend execution is failed or `not-run`, not
  local-fast success.
