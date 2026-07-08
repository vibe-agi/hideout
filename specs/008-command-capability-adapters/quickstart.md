# Quickstart: Command Capability Adapters

<!-- markdownlint-disable MD013 -->

## 1. Default Profile Compatibility

Goal: prove existing command proxy behavior remains compatible.

Steps:

1. Load the default profile.
2. Compile command proxy registry.
3. Verify `open` and `xdg-open` route to existing `host.open` behavior.
4. Run existing command-proxy tests.

Requirements: FR-004, FR-019, SC-003.

## 2. Add A Local Adapter

Goal: prove explicit enablement and digest pinning.

Steps:

1. Create a local adapter script under a profile directory.
2. Plan `hideout profile command-adapter <profile> add-local ...`.
3. Confirm the plan shows adapter ID, digest, command matches, and allowed
   proposal capabilities.
4. Apply the plan.
5. List adapters and verify the adapter is enabled only after apply.

Requirements: FR-001, FR-002, FR-018, SC-007.

## 3. Adapter Deny Outcome

Goal: prove registered commands route through the adapter and fail before target
execution when denied.

Steps:

1. Register command `tool-x` to a local adapter.
2. Have the adapter return `deny`.
3. Invoke `tool-x --version` through `hideout-shim`.
4. Assert non-zero response, no target command execution, and adapter audit.

Requirements: FR-003, FR-007, FR-008, FR-015, SC-001.

## 4. Invalid Output Fail-Closed

Goal: prove strict schema behavior.

Cases:

- malformed JSON;
- unknown output field;
- unknown outcome;
- missing required reason;
- undeclared capability proposal;
- output over limit;
- script timeout or throw.

Expected result: deny before target command execution with audit evidence.

Requirements: FR-007, FR-009, SC-001, SC-002.

## 5. Digest Mismatch Fail-Closed

Goal: prove changed local artifacts cannot run silently.

Steps:

1. Enable a local adapter.
2. Modify the script bytes.
3. Invoke the registered command.
4. Assert fail-closed response and audit reason `digest-mismatch`.
5. Run explicit `refresh-digest` plan/apply and verify the command can run
   again.

Requirements: FR-001, FR-002, FR-007, SC-002.

## 6. Rewrite Guest Command Safety

Goal: prove rewrite remains non-privileged guest execution.

Steps:

1. Adapter returns `rewriteGuest` from `tool-x` to `tool-x-real`.
2. Verify broker executes the rewritten guest command path only.
3. Return a rewrite that attempts host, network, HostFS, backend, or privileged
   authority.
4. Verify the unsafe rewrite is denied.

Requirements: FR-008, FR-010, SC-001.

## 7. Capability Proposal Is Non-Applied

Goal: prove adapter proposals do not execute authority in 008.

Steps:

1. Adapter proposes `guest.privilege.plan` with provider-specific intent.
2. Verify response/evidence records a non-applied proposal.
3. Verify no privileged setup, HostFS write, host execution, endpoint exposure,
   or network mutation occurs.

Requirements: FR-012, SC-008.

## 8. Root-Sensitive Intent Capture

Goal: prove the built-in root-sensitive adapter classifies representative
commands without claiming root containment.

Commands:

- `sudo apt install nodejs`
- `apt install nodejs`
- `iptables -F`
- `resolvectl dns`
- `systemctl restart ssh`

Expected result: each command is classified, denied or proposed, audited, and
labelled `intent-only` or `unknown`.

Requirements: FR-011, FR-013, FR-014, FR-020, SC-004, SC-005.

## 9. Redaction

Goal: prove control-plane material cannot leave the Go evidence path.

Steps:

1. Inject broker-token-shaped values, `HIDEOUT_SECRET_*` names, machine-id-like
   values, and UI-token-shaped values into adapter audit/simulation/proposal
   fixtures.
2. Emit local adapter evidence.
3. Export evidence.
4. Assert control-plane values are stripped or replaced according to existing
   redaction rules.

Requirements: FR-016, SC-006.

## 10. JavaScript Sandbox

Goal: prove adapters cannot escape constrained Goja execution.

Cases:

- attempt file read;
- attempt network fetch;
- attempt process spawn;
- attempt raw token access;
- attempt mutable profile access.

Expected result: unavailable API or fail-closed script error with audit.

Requirements: FR-017.

## 11. Manager Parity

Goal: prove CLI and Manager plan/apply use the same product path.

Steps:

1. Build adapter plan through Manager Core.
2. Build the equivalent CLI plan.
3. Assert the plan fields match.
4. Apply through Manager and verify CLI list shows the same state.
5. Verify drift/digest mismatch handling is identical.

Requirements: FR-002, FR-018, SC-007.

## 12. Gate 0 And Smoke

Goal: prove product cleanliness.

Run:

```sh
go build ./...
go vet ./...
gofmt -l internal cmd
git diff --check
go test ./...
scripts/test-gate0.sh
scripts/test-command-adapter-smoke.sh
```

Expected result: all commands pass. The smoke may include a Lima command-name
root-sensitive path, but it must not claim root isolation.

Requirements: SC-001, SC-002, SC-003, SC-004, SC-005, SC-006, SC-007, SC-008.
