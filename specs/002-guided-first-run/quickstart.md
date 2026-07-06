<!-- markdownlint-disable MD013 -->

# Quickstart: Validate Tool Model Cleanup

This guide validates the `002-guided-first-run` plan after the scope reset to
tool model cleanup. It does not validate guided onboarding or environment
materialization.

## Preconditions

- Work from the repository root.
- Use a checkout with the updated 002 spec artifacts.
- Do not start Lima or network services for this validation unless a later
  implementation task explicitly touches backend diagnostics.

## 1. Run Static And Unit Gates

```bash
go test ./...
scripts/test-gate0.sh
```

Expected outcome:

- Go tests pass.
- Gate 0 passes.
- No test invokes package managers to install guest tools.

## 2. Validate Legacy Tool-Supply Rejection

Run the implementation's targeted app/profile/schema tests once they exist.
They should cover profiles or requests containing old surfaces such as:

```json
{
  "tools": {
    "npmGlobals": ["example-package"],
    "presets": ["example-preset"]
  }
}
```

Expected outcome:

- validation fails;
- diagnostics identify unsupported legacy tool-supply state;
- no profile mutation, backend preparation, package-manager execution, or
  environment setup occurs.

## 3. Validate Expected-Command Acceptance

Use a valid expected-command declaration:

```json
{
  "tools": {
    "expectedCommands": [
      "example-cli"
    ]
  }
}
```

Expected outcome:

- validation passes;
- diagnostics treat `example-cli` as an expectation;
- output does not claim the command was installed or repaired;
- if the selected environment cannot be inspected, status is `not-checkable`,
  not "installed".

## 4. Validate Missing Command Behavior

Configure an expected command that is absent from the selected environment, or
run the relevant unit fixture that simulates absence.

Expected outcome:

- diagnostics report `missing`;
- if that command is required for the requested target run, the run fails
  closed before readiness is claimed;
- Hideout does not fall back to a host command and does not invoke a package
  manager to fix the missing command.

## 5. Scan User-Facing Surfaces

Run repository scans during implementation:

```bash
rg -n "npmGlobals|npm-global|npm package|npm-package|npmCommand|npm-command|tools\\.presets|tool preset|package-manager provider|provider execution" .
```

Expected outcome:

- matches in product code are either removed or negative tests;
- matches in docs/specs are explicit removal or migration notes;
- no quickstart or README instructs users to configure npm globals, tool
  presets, or provider-based installation as Hideout setup.

## 6. Validate Documentation

```bash
markdownlint-cli2 README.md README.zh-CN.md docs specs/002-guided-first-run
```

Expected outcome:

- markdown lint passes;
- 002 docs describe tool-model cleanup rather than guided first-run onboarding;
- docs point real tool materialization to base images, named environment setup,
  or ordinary in-boundary setup runs outside this feature.
