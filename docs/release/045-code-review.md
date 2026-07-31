# Feature 045 final code review

<!-- markdownlint-disable MD013 -->

## Disposition

The final source, security, and operator-UX review found twenty-one required
issues. All twenty-one were resolved in the review-close worktree, and their
focused regression judges pass. There is no open required review finding.

This report is not itself a release-candidate attestation. At review close, the
reviewed tree was still the large, dirty implementation worktree on `master`;
T163 and later tasks must bind a clean commit, exact package, installed binary,
full gates, and final publication-absence proof before readiness can be
claimed.

## Reviewed source identity

| Field | Value |
| --- | --- |
| Review date | 2026-07-31 |
| Base `HEAD` | `636b8d477d0dcd966a65a95eef35a27c2deb6471` |
| Branch | `master` |
| Worktree at review close | Dirty implementation tree; exact source identity is bound only by the later candidate freeze |
| Candidate status | At review close: not a candidate; exact clean identity remains T163/T171 |
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
    simplification rather than suppressing the diagnostics; and
11. audited nested release-gate failure propagation, structured failure
    evidence, benchmark duration, and fixed-threshold enforcement; and
12. profiled the system-wide BPF file-I/O hook and verified that target
    cgroup rejection precedes every hot-path metadata-cache access; and
13. repeated the real-Lima network-rotation lane from fresh instances, retained
    the first failing workload stderr, and distinguished its dedicated
    VirtioFS workspace from the separate shared-workspace Portal path rather
    than accepting a retry;
14. checked promoted-runtime rejection and the complete module against both
    Darwin host and Linux guest build constraints; and
15. audited DNS listener startup, online replacement, rollback, reuse, and
    evidence-count binding for process-liveness substitutions and stale
    constants; and
16. held a real dedicated-VirtioFS workload across the daemon session-renewal
    boundary, pressured rapid guest creates, and compared the failing and
    passing Lima mount configurations instead of accepting an intermittent
    retry.

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
| CR045-011 | Low | The real Chrome configuration journey selected the review action by obsolete user-facing copy, so the intended terminology improvement made the test dereference a missing button before it could exercise plan/apply. | WebUI E2E maintainers | Give the existing review button a non-authoritative stable `data-action` hook and select that hook in the browser proof, while retaining explicit missing/disabled failure checks. | `scripts/gates/browser-console.sh`, `scripts/gates/release-candidate-ui.sh`, and `scripts/gates/release-candidate-privacy.sh` all pass with the real browser, Keychain, and Lima lanes. |
| CR045-012 | Medium | The real-Lima reference workload was too short for a stable fixed 10% comparison on a busy developer host. On threshold failure it exited before writing structured result evidence, and the nested Gate 2 caller discarded the nonzero status and wrote its own passed receipt. The outer performance aggregate still rejected the missing/failed evidence, so this could not publish a false-green candidate, but the child receipt and diagnosis were wrong. The first duration fix enlarged each file and unintentionally multiplied observed `vfs_read` traffic; a clean rerun then exposed 25.441% overhead. | Release performance gate maintainers | Restore the original 32 KiB file payload and I/O density, lengthen the mixed workload with four in-memory SHA-256 passes per parsed record, finalize structured evidence before enforcing the immutable threshold, execute the nested gate in a fresh fail-closed Bash child, explicitly propagate the reference result, surface its terminal reason, and add passing/failing preflight fixtures. | Performance preflight positive/negative fixtures and nested-child `errexit` self-test, Bash syntax and ShellCheck, three stable standalone duration samples, a complete real-Lima diagnostic measuring 6.895% reference median overhead, and the final exact performance aggregate. |
| CR045-013 | Low | The real-Lima network-rotation gate correctly proved the internal secret commit at generation 2, then rejected the successful CLI status read because it still searched for the obsolete human-facing label `generation=2` after the operator terminology had changed to `version=2`. | Network-rotation gate maintainers | Keep internal operation evidence on the protocol field `generation`, validate CLI status through one exact `version=N` parser, and exercise that parser with current and obsolete terminology fixtures plus the focused app output contract. | Network-rotation preflight, `TestSecretListAndStatusRenderMetadataOnly`, Bash syntax and ShellCheck, a complete dirty-tree real-Lima rotation/crash-recovery diagnostic, and the final exact Lima aggregate. |
| CR045-014 | Medium | The system-wide BPF `vfs_read`/`vfs_write` hooks looked up and populated `observed_files` before checking the target cgroup. Non-target guest reads could therefore churn the target's bounded inode-identity cache and add avoidable system-wide overhead, although the later reservation guard prevented those events from being exported. | Workload file-observer maintainers | Reject a nil file or non-target cgroup before any file metadata lookup/cache mutation, regenerate the pinned LLVM 19.1.7 BPF object and manifest, and add a real-kernel regression that holds a non-target read descriptor open while proving its exact device/inode never enters the target map. | Reproducible BPF generation check, Linux-arm64 compilation, `TestFileEventReaderRealKernel`, complete real-Lima workload-observation proof with unrelated noise excluded, and the 6.895% real-Lima performance diagnostic. |
| CR045-015 | Medium | The tun2socks bootstrap used a fixed 200 ms sleep and `kill -0` as its readiness claim. A live process did not prove that the packaged tun2socks engine had opened the TUN device and created its network stack before Hideout changed the default route and launched the target. | Network bootstrap maintainers | Configure the pinned helper's `tun-post-up` hook to write a private one-shot readiness marker only after stack creation, remove any stale marker before start, wait for it with a bounded process-aware loop before changing the default route, and delete it during cleanup. | `TestTun2SocksRuntimeVerificationPlan`, `TestPrepareEnvironmentNetworkBindsManagedSecretGeneration`, shell syntax, network-rotation preflight, and two consecutive fresh-instance real-Lima rotation/crash-recovery passes. |
| CR045-016 | Low | The network-rotation gate reported only that the workload exited before its first request, then deleted the private work directory. It discarded the child exit status and stderr that distinguished a workspace permission race from a proxy failure, making a release blocker non-actionable. | Network-rotation gate maintainers | Reap the failed workload for its exact status, copy its private stderr into the run's mode-0600 evidence directory before cleanup, and point the terminal failure at that retained diagnostic without rendering secret material. | The exercised failure path retained status 2 and the exact `/workspace/session-before` error, Bash syntax and preflight pass, and both post-fix real-Lima runs retained normal success evidence without secret leakage. |
| CR045-017 | Medium | The promoted-runtime gate compared an `example.invalid` URL with a glob inside single-bracket `test`, where the glob was a literal. A placeholder URL with a syntactically valid digest therefore passed the intended fail-closed promotion boundary and reached the real-image lane. | Runtime release-gate maintainers | Use Bash `[[ ... == pattern ]]` matching, assert the exact guard form from the runtime smoke, reject the obsolete literal comparison, and exercise a representative placeholder fixture. | `scripts/test-runtime-smoke.sh`, Bash syntax, ShellCheck error-level scan, and the exact release-candidate lane. |
| CR045-018 | Medium | Portable Manager code referenced a signing observer supplied only by a Darwin-suffixed source file. Host builds passed, but whole-module Linux guest type-checking failed before any runtime behavior could be exercised, while the existing real-Lima compile list did not include the affected package graph. | Host capability and Gate 0 maintainers | Add a non-Darwin fail-closed observer returning the stable app-absent code and make full Gate 0 type-check every package and test for Linux/arm64. | Darwin hostcap/Manager tests, `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go vet ./...`, and the complete Gate 0 lane. |
| CR045-019 | Medium | Both initial DNS mediation and online resolver rotation treated a 200 ms sleep plus `kill -0` as proof that the replacement helper had bound UDP and TCP port 53. A live but unbound helper could leave the target or an in-flight session without working DNS, and rollback made the same unsupported assumption. | DNS mediation and Lima backend maintainers | Have the DNS helper publish a private marker containing its exact PID only after both listeners bind; require marker/PID equality before resolver redirection, activation, rollback proof, and reuse; remove stale markers; and terminate/reap a child that never becomes ready. | `TestPublishReadyMarkerBindsExactProcessAndPrivateMode`, `TestPublishReadyMarkerRejectsUnsafeTarget`, network/bootstrap and Lima command-contract tests, Linux helper build, and the final fresh-instance network-rotation lane. |
| CR045-020 | Low | The final review table had grown beyond its original seven findings, but the release evidence writer and semantic validator still hard-coded `requiredFindings: 7`. A digest-valid manifest could therefore disagree with the exact review report it referenced. | Release evidence maintainers | Parse the review rows once, reject empty, duplicate, out-of-order, or non-contiguous finding IDs, feed that validated count into the generated manifest, and validate the same value instead of repeating a stale constant. | Positive, gap, duplicate, and empty collector preflight fixtures; syntax checks; and the exact clean evidence collection with detached-digest verification. |
| CR045-021 | Medium | Hideout forced Lima's experimental `mountInotify` path on every VZ instance. Lima reflects each host notification back into the guest as `Chtimes`; under rapid dedicated-VirtioFS guest creates this produced a transient `EACCES` even while `/workspace` remained writable, target-owned, and the immediately following create succeeded. Fast network-rotation runs usually ended before the race was exercised. | Lima backend and network-rotation gate maintainers | Return `mountInotify` to Lima's safe disabled default, retain the existing nonclaim for host-originated filesystem notifications, and keep one active workload alive across the 30-second session-renewal boundary before requiring 64 consecutive workspace creates. Do not retry failed target syscalls. | Retained failure `run-20260731T080513Z-1596` failed at create 2 with `uid=1000`, mode `0700`, exact VirtioFS mountinfo, and an immediate successful probe; `TestPrepareWritesLimaYAML`; and `run-20260731T081241Z-36867` proves 40 seconds, 64 writes, online rotation, and all five real crash-recovery boundaries with `mountInotify=false`. |

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
- Review-close dirty-tree test results are engineering evidence only. They
  cannot establish an exact package or release claim.

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
