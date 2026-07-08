# Quickstart: Guest Privilege Separation And Risk Audit

<!-- markdownlint-disable MD013 -->

## 1. Status Classification Unit Tests

Goal: prove status classification cannot overclaim.

Run:

```sh
go test ./internal/privilege
```

Expected:

- non-root plus no passwordless sudo plus separate setup path is `enforced`;
- passwordless sudo is `degraded`;
- unsupported backend/check errors are `unknown`;
- `enforced` is impossible with missing checks.

Requirements: FR-001 through FR-009, SC-001 through SC-003.

## 2. Setup Credential Redaction

Goal: prove setup secrets remain control-plane material.

Run targeted tests that inject setup private-key-shaped values, setup token
values, broker tokens, UI tokens, and `HIDEOUT_SECRET_*` names into setup
evidence fixtures.

Expected:

- target env does not contain setup material;
- audit/UI/export output strips setup secrets;
- setup credential paths are summarized as redacted control-plane classes.

Requirements: FR-010, FR-011, SC-004.

## 3. Fake Lima Setup Path Tests

Goal: prove the Lima runner uses separate target and setup paths in plan/apply
without requiring a real VM.

Run:

```sh
go test ./internal/backend/lima ./internal/manager
```

Expected:

- target command runner still uses the target identity;
- requested network/DNS/HostFS setup uses the setup path when available;
- missing setup identity fails requested setup closed;
- existing shared-sudo path is reported as `degraded`.

Requirements: FR-005 through FR-017.

## 4. Root-Sensitive Intent Evidence

Goal: prove 008 adapter evidence is connected to 009 status without becoming a
root boundary claim.

Run:

```sh
scripts/test-command-adapter-smoke.sh
```

Expected:

- command-name `sudo` is denied or produces a non-applied proposal;
- evidence labels it as target root intent;
- evidence does not claim absolute-path or syscall interception.

Requirements: FR-013, FR-014, SC-008.

## 5. Real Lima Enforced Proof

Goal: prove enforced status on a real Lima environment.

Run the 009 real Lima smoke on a Hideout-created environment that supports
dual identity.

```sh
scripts/test-privilege-separation-smoke.sh --real-enforced
```

Expected evidence:

- target `id -u != 0`;
- target `sudo -n true` fails;
- target `/usr/bin/sudo -n true` fails when present;
- Hideout setup identity succeeds for required setup;
- status is `enforced`;
- no setup credential material appears in target env or audit.

Requirements: FR-001 through FR-005, FR-010, FR-011, FR-020, SC-005.

## 6. Real Lima Degraded Proof

Goal: prove passwordless-sudo images are surfaced honestly.

Run the 009 degraded smoke. The script creates a temporary managed Lima
environment, externally restores target passwordless sudo through the setup/root
path to simulate a weak or pre-009 base image, then runs Hideout again through
the normal product path.

```sh
scripts/test-privilege-separation-smoke.sh --real-degraded
```

Expected evidence:

- target user is non-root;
- `sudo -n true` or `/usr/bin/sudo -n true` succeeds;
- status is `degraded`;
- warning includes recreate/base-image guidance;
- evidence does not claim guest-root containment.

Requirements: FR-006, FR-008, FR-009, FR-020, SC-006, SC-008.

## 7. Gate 3 DNS Regression

Goal: prove DNS mediation remains closed after setup path changes.

Run:

```sh
scripts/test-gate3-hidden-proxy.sh
```

Expected:

- proxy env absent;
- DNS mediated through DoH stub;
- connected-subnet resolver blocked;
- privilege status is `enforced`;
- privileged network setup uses the root-control setup identity;
- HTTPS request succeeds;
- Gate 3 passes.

Requirements: FR-015, FR-017, SC-007.

## 8. Final Battery

Run:

```sh
go build ./...
go vet ./...
test -z "$(gofmt -l internal cmd)"
git diff --check
npx --yes markdownlint-cli2 README.md docs/**/*.md specs/009-guest-privilege-separation-risk-audit/**/*.md
go test ./...
scripts/test-gate0.sh
scripts/test-privilege-separation-smoke.sh
```

Expected: all commands pass, and real Lima evidence is attached for enforced
and degraded status before 009 is marked complete.

Requirements: all FR/SC.
