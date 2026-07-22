# Contract: Workspace Execution Evidence

<!-- markdownlint-disable MD013 -->

## Proof Requirements

| Proof ID | Layer | Required result |
| ------- | ------- | ------- |
| `041.workspace-executable.gate0.mechanics` | Gate 0 | Flag contract, unknown-flag refusal, probe wiring, and shell assertions pass |
| `041.workspace-executable.real-gate2.execution` | Real Gate 2 | Clean exact-commit macOS arm64 Lima shared-Portal artifact passes its Go validator |
| `041.workspace-executable.real-gate2.not-run` | Supporting only | Records why the real gate was not run; cannot satisfy release completion |
| `041.workspace-executable.docs.claim-boundary` | Product hardening | Status, design, threat, test-plan, claim, and debt wording agree |

## Real Artifact

The authoritative JSON artifact MUST contain only the fields defined by the
Workspace Execution Evidence entity. Its `checks` inventory is closed and every
entry must be true. At minimum it proves:

- direct interpreted script execution;
- direct Linux arm64 binary execution;
- a workspace-local launcher;
- host checkout write visibility;
- exact-root and escaping-path refusal;
- no guest-local workspace copy;
- no host execution fallback;
- at least 30 successful samples and warm first-output p95 at most 2 seconds;
- the explicit `staticVirtiofs: not-claimed` boundary.

The evaluator rejects dirty, wrong-commit, wrong-platform, wrong-backend,
wrong-mechanism, missing-check, false-check, undersampled, slow, overclaiming,
and unknown-field artifacts.

## Redaction

Artifacts and proof details MUST NOT contain workspace roots, host home paths,
Portal endpoints or credentials, target arguments, command output, file content,
environment secrets, or guest temporary paths.
