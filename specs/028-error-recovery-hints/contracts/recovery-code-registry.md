# Contract: Recovery Code Registry

<!-- markdownlint-disable MD013 -->

## Registry JSON

```json
{
  "schema": "hideout.recovery-codes/v1",
  "codes": [
    {
      "code": "package.obsolete-leftover",
      "subsystem": "package",
      "severity": "warning",
      "reason": "package-owned files from a previous install are still present",
      "hint": "run package repair after inspecting leftovers",
      "nextActions": ["hideout package repair --prefix <dir> --dry-run"]
    }
  ]
}
```

Rules:

- `code` is unique and stable.
- `reason` and `hint` are redacted before rendering.
- `nextActions` are guidance only.
- Unknown code references fail docs truth or unit tests.

## Doctor Finding Fields

Doctor findings may add:

```json
{
  "code": "package.prerequisite.missing",
  "reason": "external prerequisite tun2socks is missing",
  "hint": "install tun2socks or expose it on PATH before privacy-mode runs"
}
```

Rules:

- Existing uncoded findings remain valid.
- Coded findings must use a registered code.
- Human and JSON output must agree on code.

## Selected CLI Output

Selected CLI surfaces include `code=<public-code>` or a clearly labeled code
line. Tests must assert the code, not long prose.

Rules:

- Codes do not change exit status.
- Hints do not perform repair.
- Local product-hardening proof remains local proof only.
