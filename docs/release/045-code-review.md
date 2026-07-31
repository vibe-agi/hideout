# Feature 045 final code review

<!-- markdownlint-disable MD013 -->

## Disposition

The final source, security, and operator-UX review found ten required issues.
All ten are resolved in the current worktree, and their focused regression
judges pass. There is no open required review finding.

This is not yet a release-candidate attestation. The reviewed tree is still the
large, dirty implementation worktree on `master`; T163 and later tasks must
bind a clean commit, exact package, installed binary, full gates, and final
publication-absence proof before readiness can be claimed.

## Reviewed source identity

| Field | Value |
| --- | --- |
| Review date | 2026-07-31 |
| Base `HEAD` | `5aeb6bd8677b4bce3df5e40e016cd2bc864b4787` |
| Branch | `master` |
| Worktree at review close | Dirty; 107 tracked files changed, 1,112 additions, 536 deletions, and one untracked file |
| Candidate status | Not a candidate; exact clean identity remains T163/T171 |
| Publication authority | None; no remote tag, GitHub Release, Homebrew mutation, or package publication is authorized |

The size counters describe the review-close worktree only. They are not a
manifest and must not be copied into final evidence; the clean evidence
collector recomputes the exact candidate inventory.

## Review method

The review followed authority and data flow rather than package order:

1. traced CLI, TUI, WebUI, Manager API, daemon control, and compatibility
   mutation entry points through canonical plan/apply and durable operations;
2. audited response-loss, stale-plan, crash, rollback, and recovery paths for
   exact operation identity and unsafe replay guidance;
3. checked local HTTP Host, Origin, authentication, method, body-size, strict
   JSON, cache, and browser rendering boundaries;
4. reviewed root helper selection, session view, cgroup placement, observer
   relay authentication, target startup ordering, and cleanup;
5. reviewed activity ownership, private persistence, coverage/loss accounting,
   path visibility, deterministic redaction, retention, and deletion;
6. reviewed secret value flow across CLI input, Keychain, daemon memory, API,
   operation/audit records, process arguments, target environment, UI, and
   export;
7. reviewed lifecycle/configuration lock order, revision/CAS, canonical replan,
   effect checkpoints, terminal evidence, and reconciliation;
8. checked help and first-use language against the implemented commands and
   Desired/Effective/Transition/Evidence semantics; and
9. inspected packaging, dependency, advisory, helper digest, evidence, and
   publication boundaries; and
10. ran static analysis over production and test code, then reviewed every
    unreachable symbol, ignored error, ineffective assignment, and suspicious
    simplification rather than suppressing the diagnostics.

Severity means:

- **High**: can violate a required authority, secret, idempotency, or reviewed
  mutation invariant;
- **Medium**: weakens a defense-in-depth boundary or permits avoidable
  resource/contract ambiguity;
- **Low**: documentation or evidence drift that can produce a false release
  conclusion without directly changing runtime authority.

## Required findings

| ID | Severity | Finding | Owner | Resolution | Direct evidence |
| --- | --- | --- | --- | --- | --- |
| CR045-001 | High | The privileged session supervisor validated and hashed `/hideout/session/shims/hideout-observer`, then reopened the pathname for execution. A writable projection therefore left a validation/use race at a root helper boundary. | Session supervisor | Open the helper once with `O_NOFOLLOW`, validate the opened inode, hash that descriptor, and execute the same descriptor through `/proc/self/fd/3`; retain the fixed guest path only as `argv[0]`. | `TestVerifiedObserverCommandExecutesOpenedInodeAfterPathReplacement`, `TestOpenVerifiedObserverHelperRejectsSymlink`, full Linux-arm64 supervisor test binary, and Linux-arm64 `go vet`. |
| CR045-002 | High | A secret Apply response error discarded the reviewed operation ID and generic guidance could suggest rerunning the mutation, which might duplicate an accepted operation after response loss. | CLI secret client | Wrap every uncertain Apply outcome with its exact operation ID, render no arbitrary provider detail, and direct the operator to inspect that ID before creating any new plan. | `TestSecretApplyFailureKeepsExactOperationRecoveryIdentity`, `TestSecretApplyOutcomeGuidanceUsesExactOperationWithoutReplay`, full `internal/app` tests. |
| CR045-003 | Medium | Loopback `/daemon/*` routes were mounted outside the exact Host/Origin checks applied to Manager API and static UI routes. Token auth remained, but the DNS-rebinding/foreign-origin defense was incomplete. | Daemon loopback server | Wrap the entire loopback mux in one exact listener-Host and same-origin guard and apply no-store/nosniff headers uniformly. | `TestLoopbackUIRejectsForeignHostAndOriginOnDaemonEndpoints`, full `internal/daemon` tests. |
| CR045-004 | Medium | Daemon lifecycle/background control handlers accepted unbounded or non-strict JSON, and the SSE route did not explicitly reject non-GET methods. | Daemon control server | Add a 64 KiB bounded strict decoder with unknown-field and trailing-content rejection to every daemon control mutation, stable 400/413 errors, explicit SSE GET enforcement, and uniform no-store/nosniff JSON headers. | `TestDaemonControlRoutesBoundAndStrictlyDecodeRequests`, full `internal/daemon` tests. |
| CR045-005 | High | The stable CLI connection path planned and immediately confirmed internally; the documented `hideout connect plan` command did not exist. A non-TTY caller could mutate without first receiving the canonical diff/effects/blockers/rollback, and an exact retry could not reconstruct a terminal binding after the private plan was intentionally removed. | CLI configuration client and Manager transaction service | Add `connect plan` and exact-ID `connect apply`; make natural interactive commands review then prompt; require `--yes` in non-TTY use; use the generic Manager transaction API; retain exact operation identity on uncertain/recovery outcomes; revalidate durable plan bindings; and reconstruct only an exact idempotent terminal retry from the validated operation. | `TestConnectPlanAndApplyUseExactReviewedOperation`, `TestConnectWithoutTTYOrYesLeavesReviewedPlanUnapplied`, `TestConnectApplyFailureKeepsExactOperationRecoveryIdentity`, `TestConnectRecoveryGuidanceKeepsExactOperationIdentity`, `TestProfileTransactionInspectPlanRevalidatesExactOperationBinding`, full `internal/app` and `internal/manager` tests. |
| CR045-006 | Low | The gate matrix still labelled completed network/secret and lifecycle/recovery work as planned, which could make release status disagree with task and evidence records. | Release documentation | Mark the lanes active/passing with their T152/T153/T155 evidence while preserving the clean-candidate T163/T171 requirement. Update the claim matrix with the review regressions. | Markdown lint, documentation/help truth tests, and `git diff --check`; exact candidate doc truth remains T167/T169/T171. |
| CR045-007 | Medium | Static analysis found ignored output, lock-release, daemon/session cleanup, and rollback errors; ineffective assignments and redundant copies; and obsolete production helpers left unreachable after the Manager/console refactor. An unlock failure could be reported as success, while dead security-sensitive paths made the reviewed and advisory-reachable runtime surface ambiguous. | App, Manager, and daemon maintainers | Join fallible lock releases into the owning operation result, check output and cleanup failures, correct the test lifecycle ordering exposed by those checks, remove unreachable production/test helpers, and simplify ineffective assignments and identical API conversions. Do not add lint suppressions. | `golangci-lint run --max-issues-per-linter=0 --max-same-issues=0 ./...`, `go test -p 4 ./... -count=1`, Linux-arm64 supervisor vet, and the complete static lane all pass. |
| CR045-008 | High | Bash 3.2 can turn a `set -u` crash into exit status zero when an EXIT trap is installed. Several final candidate, evidence, package, install, and direct sub-gate scripts cleaned up in EXIT traps without an independent completion proof, so a crash could be accepted as green. | Release engineering | Add one shared fail-closed completion guard, set the proof only immediately before each success line, exercise the exact Bash failure mode in a child-shell self-test, and make the release-blocker lane reject missing guard wiring across every directly used 045 EXIT-trap boundary. | `gate_completion_guard_self_test`, `bash -n`, ShellCheck, all release preflights, and the final exact orchestrator. |
| CR045-009 | Low | Ordinary help, TUI, and WebUI paths exposed control-plane terms such as Manager projection, authoritative re-seed, incarnation, generation, and capability without explaining the action a user should take. Protocol fields were correct, but the primary experience obscured current state, VM ownership, secret version, and recovery. | CLI/TUI/WebUI operator experience | Keep API/schema/flag compatibility, but render ordinary actions as verified state, refresh, exact VM instance, secret version, collector run, setting, and Hideout review. Preserve advanced identifiers only where needed for exact diagnostics or copyable flags. | Focused app/TUI/WebUI suites, help and golden tests, Markdown lint, control-text safety tests, and installed-candidate quickstart validation. |
| CR045-010 | Low | Nine broker success-path tests resolved `example.com` through the machine's external DNS before reaching the behavior under test. A resolver outage could therefore report a product regression or block a release even though production correctly failed closed. | Broker test maintainers | Inject one deterministic public test address only into the named success-path tests. Keep DNS-policy and local-address rejection tests on their existing resolver paths so production resolution and fail-closed boundaries remain covered. | `go test ./internal/broker -count=1`, `go test -p 4 ./... -count=1`, and full no-limit static analysis all pass without external DNS. |

## Closure terminology and false-success audit

The closure pass treats wire compatibility separately from operator language:

- schema names, JSON fields, API resources, Go type names, and the existing
  `--incarnation` flag remain stable;
- the flag help and user guides explain that `--incarnation` is the exact VM
  instance ID;
- primary help, TUI, WebUI, and routine human output use action-oriented terms
  and retain technical identifiers only in expanded evidence;
- secret values remain absent from help, errors, UI, logs, process arguments,
  and release artifacts; only references and versions are rendered;
- untrusted terminal and browser text still passes through the existing
  control-safe rendering paths; and
- no status, response, cleanup trap, or display sample is accepted as success
  without its independently validated terminal evidence.

## Confirmed invariants and negative review results

No additional required issue was found in these reviewed areas:

- Manager profile transactions bind canonical changes to revision, base/target
  digest, expiry, plan digest, operation ID, mutation keys, durable effects,
  canonical replan, rollback, and terminal proof.
- TUI and WebUI retain the exact operation on response loss, disable mutation
  while stale, and do not silently replan or replay with a new ID.
- Manager API routes uniformly enforce exact route inventory, method/body
  contracts, authentication, Host/Origin boundaries, and no-store output.
- WebUI dynamic content is created with typed DOM APIs and `textContent`; no
  `innerHTML`, `outerHTML`, `insertAdjacentHTML`, `eval`, local storage, or
  session storage authority path was found. The bearer fragment is removed
  before requests.
- Activity store and indexes use exact owner/incarnation binding, private
  directories/files, atomic replacement, digest metadata, bounded retention,
  explicit gaps, and exact-owner cleanup.
- Workload attribution is limited to the top-level command and descendants in
  the run cgroup. It records process/exec, file metadata, attributed IP/port,
  and DNS metadata, while excluding environment values, keystrokes, full PTY,
  file content, and unsupported inference.
- Managed secrets are absent from public plans/results, profile values, target
  environment, process argv, routine evidence, and browser rendering. Locked,
  missing, or corrupt managed storage fails closed rather than falling through
  to a legacy value.
- Activity redaction deterministically removes known secret generations, URI
  userinfo, authentication fields, sensitive flags/query values, and
  control-plane tokens before persistence or export.
- No inverse activity-service/session lock order was found.
- The exact session view hides the shared runtime root behind a private tmpfs;
  sibling target sessions cannot rewrite another session's helper projection.
- Release/package scripts remain local and scoped; the current immutable
  Homebrew `v0.1.0-alpha.3` receipt is not rewritten by candidate validation.

## Compatibility boundaries and nonclaims

These are intentional boundaries, not unresolved required findings:

- Existing advanced `hideout profile ...` commands and legacy typed
  `/api/v1/profile/*/apply` routes remain compatibility adapters as required by
  the Manager API contract. They still use the shared durable
  revision/CAS/operation transaction internally, but their explicit invocation
  is the legacy confirmation contract. They are not the canonical console
  editing UX. New CLI connection, TUI, and WebUI configuration use the generic
  visible Draft → Plan → Review → Confirm → Apply flow.
- Advanced identity, profile import, adapter-pack, host-app, decision, export,
  and environment lifecycle commands keep their owning typed review/apply
  protocols; they are not raw profile writes exposed through the new consoles.
- The loopback browser UI is a local single-user interface, not a remote or
  multi-tenant administration plane.
- Workload observation is metadata with explicit Available/Partial/Unavailable
  coverage, not syscall-complete behavior proof or prevention.
- A guest-root workload can rewrite its own guest routing/resolver state; no
  guest-root containment claim is made.
- Current dirty-tree test results are engineering evidence only. They cannot
  establish an exact package or release claim.

## Review-close judges

The following focused judges passed against the review-close worktree:

```sh
go test ./internal/app ./internal/manager ./internal/daemon -count=1
go test -p 4 ./... -count=1
golangci-lint run --max-issues-per-linter=0 --max-same-issues=0 ./...
go test -race ./internal/app \
  -run 'Test(ConnectPlanAndApplyUseExactReviewedOperation|ConnectWithoutTTYOrYesLeavesReviewedPlanUnapplied|ConnectApplyFailureKeepsExactOperationRecoveryIdentity|SecretApplyFailureKeepsExactOperationRecoveryIdentity)' \
  -count=1
go test -race ./internal/daemon \
  -run 'Test(LoopbackUIRejectsForeignHostAndOriginOnDaemonEndpoints|DaemonControlRoutesBoundAndStrictlyDecodeRequests)' \
  -count=1
go vet ./internal/app ./internal/manager ./internal/daemon
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go vet ./cmd/hideout-session-supervisor
```

The exact Linux-arm64 supervisor test binary was also copied into the running
Lima guest and its full package test suite passed there, including descriptor
execution after pathname replacement and symlink rejection.

The closure review reran full tests, no-limit static analysis, vet including the
Linux-arm64 supervisor, generated/schema checks, formatting, shell and Markdown
lint, acceptance identifier reconciliation, and diff checks after the final
fixes. T163 and later tasks must still rerun the complete release matrix against
one clean candidate; this report does not substitute for those gates.
