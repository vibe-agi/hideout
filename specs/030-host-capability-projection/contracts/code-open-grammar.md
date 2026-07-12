# Contract: `code-open-v1` Command Grammar

<!-- markdownlint-disable MD013 MD060 -->

Authority-free parsing layer (`internal/cmdgrammar`). Turns guest `code` argv into a proposed `OpenResourceIntent`. Carries zero host authority; never emits a host path or raw argv.

## Accepted forms

| Guest input | Proposed intent |
|-------------|-----------------|
| `code .` | `{appRef: vscode, resources: [{kind: workspace, relativePath: "."}], windowMode: reuse}` |
| `code src/main.go` | `{appRef: vscode, resources: [{kind: workspace, relativePath: "src/main.go"}], windowMode: reuse}` |
| `code -g src/app.ts:12:3` | `{appRef: vscode, resources: [{kind: workspace, relativePath: "src/app.ts"}], location: {line: 12, column: 3}, windowMode: reuse}` |
| `code -n .` | `{... windowMode: new}` |
| `code -r src/x` | `{... windowMode: reuse}` |

## Flag table

| Flag | Meaning | Value shape |
|------|---------|-------------|
| `-g`, `--goto` | open at location | `<file>:<line>:<column>` (line/column positive ints; column optional → default 1) |
| `-n`, `--new-window` | new window | none |
| `-r`, `--reuse-window` | reuse window | none |
| (positional) | resource | workspace-relative path or `.` |
| any other | — | **denied** → `projection.flag.unrecognized` |

## Rules

- `unknownFlags: deny`. A flag not in the table refuses; nothing is passed through.
- Positional/`-g` paths are parsed structurally; `-g file:line:column` is split by the grammar, never string-substituted on the whole argv.
- A path that is absolute-non-workspace, `..`-escaping, or guest-only is emitted as a resource the Core validator will reject (`projection.path.no-host-mapping`); the grammar does not resolve host paths itself.
- The grammar output is a proposal. Go re-decodes and re-validates every field (see `open-resource-intent.schema.json`). Grammar bugs cannot widen authority because Core re-validates.
- Future editor recipes (`cursor`, `zed`, `idea`) are separate grammars producing the same `OpenResourceIntent`; no Core change.

## Test obligations

- Each accepted form maps to the exact intent above.
- Unknown flag / malformed `-g` value → refusal, no intent emitted.
- No grammar output contains a host absolute path or the raw argv.
