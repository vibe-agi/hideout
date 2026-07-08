# Contract: Adapter Pack Test Harness

<!-- markdownlint-disable MD013 -->

## Test Vector Shape

```json
{
  "id": "proposes-hostfs-write",
  "adapterId": "example-tool",
  "context": {
    "command": {
      "name": "example-tool",
      "argv": ["example-tool", "write", "file.txt"],
      "cwd": "/workspace"
    }
  },
  "expect": {
    "outcome": "proposeCapability",
    "capability": "host.fs.write.plan",
    "reasonContains": "write"
  }
}
```

## Execution Rules

- Test execution uses the same constrained adapter ABI as runtime evaluation.
- Test contexts are deterministic fixtures.
- Test execution does not expose filesystem, network, process, timer, backend,
  broker token, HostFS apply, privilege setup, or profile mutation APIs.
- Test output is validated through the same strict outcome contract as runtime
  output.
- Test evidence is redacted before audit/export.

## Enablement Rules

- Pack tests must pass before a profile can enable the pack.
- Passing pack tests is not sufficient by itself.
- Core validation must pass independently:
  - manifest schema;
  - digest lock;
  - adapter output schema;
  - command/capability allowlists;
  - duplicate ownership checks;
  - forbidden authority checks;
  - timeout/exception fail-closed behavior.

## Failure Contract

- Missing tests block enablement.
- Failing tests block enablement.
- Nondeterministic or malformed tests block enablement.
- A pack whose tests pass but Core validation fails remains blocked.
