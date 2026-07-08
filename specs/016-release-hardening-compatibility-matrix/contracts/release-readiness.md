# Contract: Release Readiness

<!-- markdownlint-disable MD013 -->

## Command Surface

```text
scripts/test-release-readiness.sh --local-fast [--out <path>]
scripts/test-release-readiness.sh --release-candidate [--out <path>]
```

Optional environment:

- `HIDEOUT_GATE2_EVIDENCE`: path to real Gate 2 evidence or manifest.
- `HIDEOUT_GATE3_EVIDENCE`: path to real Gate 3 evidence or manifest.
- `HIDEOUT_RELEASE_DOGFOOD_EVIDENCE`: path to release dogfood manifest that
  includes Gate 2 and Gate 3.

## Local-Fast Mode

Local-fast mode may run:

- build/vet/gofmt/diff-check
- `go test ./...`
- Gate 0
- package smoke
- doctor smoke
- markdown/schema validation

Local-fast mode must set:

- `mode=local-fast`
- `evidenceClass=local-fast`
- `releaseReady=false`
- `status=not-release`

## Release-Candidate Mode

Release-candidate mode must:

- Require real Gate 2 and Gate 3 evidence for matrix subjects that need them.
- Accept an existing release dogfood manifest when it proves those gates.
- Fail closed when required evidence is absent or invalid.
- Write a readiness artifact even on failure when `--out` is supplied.

## Readiness JSON Shape

Top-level fields:

- `schema`
- `generatedAt`
- `mode`
- `evidenceClass`
- `releaseReady`
- `status`
- `commit`
- `platform`
- `matrix`
- `commands`
- `gates`
- `nonClaims`
- `redaction`

Command fields:

- `name`
- `status`
- `summary`

Gate fields:

- `id`
- `required`
- `status`
- `evidencePath`
- `summary`

## Redaction And Share Safety

Readiness artifacts are summaries. They must not inline raw command output or
environment variables. All free text goes through deterministic control-plane
redaction before JSON encoding.

## Exit Semantics

- Local-fast returns non-zero only when local checks fail.
- Release-candidate returns non-zero when required real-gate evidence is
  missing, invalid, or failed.
- Missing optional evidence can be `not-run`, but required release gates cannot
  be `not-run` when `releaseReady=true`.
