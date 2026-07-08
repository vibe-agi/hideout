# Contract: Command Adapter ABI

<!-- markdownlint-disable MD013 -->

## Entrypoint

Adapters expose a JavaScript function:

```javascript
function decideCommandAdapter(ctx) {
  return {
    outcome: "deny",
    reason: "example"
  };
}
```

The entrypoint runs inside the existing constrained Goja policy runtime.

## Context

```json
{
  "version": "command-adapter/v1",
  "profile": {"name": "default"},
  "session": {"id": "session-id", "backend": "lima"},
  "adapter": {"id": "root-sensitive", "digest": "sha256:..."},
  "command": {
    "name": "sudo",
    "argv": ["sudo", "apt", "install", "nodejs"],
    "cwd": "/workspace"
  },
  "env": {
    "keys": ["TERM", "PATH"],
    "classes": ["terminal", "path"],
    "count": 2
  },
  "workspace": {
    "guestRoot": "/workspace",
    "mode": "read-write"
  },
  "network": {
    "mode": "tun2socks",
    "mediatedDNS": true
  }
}
```

Rules:

- `command.argv` and `command.cwd` are raw command context.
- `env` never includes raw environment values.
- Context never includes broker tokens, UI tokens, `HIDEOUT_SECRET_*` values,
  generated machine IDs, backend setup handles, or raw profile state.

## Output: Deny

```json
{
  "outcome": "deny",
  "reason": "root-sensitive command requires operator review",
  "exitCode": 126,
  "stderr": "blocked by Hideout command adapter\n",
  "intent": {
    "category": "package-manager",
    "operation": "install",
    "packages": ["nodejs"]
  }
}
```

Rules:

- `reason` is required.
- `exitCode` defaults to a non-zero adapter-denied code when omitted.
- No side effect occurs before this response.

## Output: Simulate

```json
{
  "outcome": "simulate",
  "reason": "tool version is known",
  "exitCode": 0,
  "stdout": "tool-x 1.2.3\n"
}
```

Rules:

- Root-sensitive adapters cannot simulate successful system mutation.
- Simulated output is redacted before audit/UI/export.
- Unknown fields are rejected.

## Output: Rewrite Guest Command

```json
{
  "outcome": "rewriteGuest",
  "reason": "replace alias with installed command",
  "argv": ["tool-x-real", "--version"]
}
```

Rules:

- Rewrite must remain non-privileged guest execution.
- Rewrite cannot request host, backend, network, endpoint, HostFS, privileged
  setup, or environment authority.
- Unsafe rewrites are denied with audit evidence.

## Output: Propose Capability

```json
{
  "outcome": "proposeCapability",
  "reason": "package install requested",
  "capability": "guest.privilege.plan",
  "intent": {
    "category": "package-manager",
    "operation": "install",
    "packages": ["nodejs"]
  },
  "suggestions": [
    "Use a base image with nodejs preinstalled",
    "Run the 009 privilege-separation workflow when available"
  ]
}
```

Rules:

- `capability` must be declared by the adapter profile entry.
- 008 never applies the proposal.
- Unknown capabilities are denied or reported unavailable.

## Strict Validation

Go Core rejects:

- unknown top-level output fields;
- unknown outcome values;
- missing required fields;
- multiple JSON values;
- malformed JSON;
- output beyond configured size limits;
- undeclared capability proposals;
- root-sensitive successful system-mutation simulation;
- rewrite outcomes that add authority.

## JavaScript Restrictions

Adapters cannot:

- read files;
- open network connections;
- spawn processes;
- access raw tokens;
- mutate profile state;
- access timers or nondeterministic APIs beyond the existing deterministic
  policy runtime allowance.
