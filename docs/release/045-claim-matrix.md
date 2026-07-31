# Feature 045 claim, judge, and mutation matrix

<!-- markdownlint-disable MD013 -->

This inventory is the release-accountability spine for the operator
observability console. It maps every new authority, attribution, redaction,
coverage, help, UI, recovery, and cleanup claim to:

1. a direct green assertion over production behavior;
2. an independent release judge that consumes output or evidence;
3. an implementation mutation that must make the direct assertion red;
4. a false-green or malformed-evidence fixture that must make the judge red;
5. the artifact that records both failures and the restored-green result.

The inventory itself is not mutation evidence. T148 owns every `N045-*`
judge-negative fixture. `scripts/mutation/045/run-production-mutations.sh`
owns one source-overlay `M045-*` proof for each claim family, and T152
independently checks its manifest, logs, digests, and test events. A candidate
cannot count a row as accepted until both halves fire, the unmutated
implementation is green again, and the exact candidate evidence binds the
same source and package digest.

## Status and proof rules

| Status | Meaning |
| --- | --- |
| `green-local` | The direct assertion exists and passes locally; one or more owning aggregate, real-backend, package, or exact-candidate judges may still be required. |
| `active-local-gate` | A repeatable local evidence gate exists and passes, but real-backend, package, or clean-candidate binding may still be required. |
| `active-real-backend-gate` | The repeatable real-backend aggregate exists and passes for the recorded source tree; a dirty-source result still cannot substitute for exact clean-candidate binding. |
| `observed-red` | The named implementation mutant has been observed failing the intended invariant and the unmutated code is restored green. |
| `planned-gate` | The final aggregate judge is reserved in the 045 gate matrix but is not implemented yet. |
| `blocked-integration` | Unit components exist, but the end-to-end production path required by the claim is not wired; the claim is a release blocker. |

Every row now has an observed T148 `N045-*` result. The row dispositions below
continue to describe production behavior and aggregate release-gate status;
they do not become accepted merely because the evidence judge rejects a false
receipt.

T153's current real-Lima aggregate passed all four lanes and all 18 claims at
`.artifacts/045/lima-current/run-20260730T165516Z-92214/summary.json`
(`sha256:23cee7069d8b76e55a26361312f5feecf8980006c041f831e262a2aa0827557b`).
Its 262-file manifest, byte counts, and `0600` modes were independently
rechecked. The source tree was dirty, so the receipt deliberately records
`candidateAcceptance=false`; T163 still owns exact clean-candidate binding.

T154's current UI aggregate passed 18/18 first-use/help, 13/13 real-PTY, and
40/40 shared-console tests plus the real-Chrome child gate at
`.artifacts/045/ui-current-2/run-20260730T171832Z-22682/summary.json`
(`sha256:bfd07ad27130d80459caae9099b71f86076bca712176a71616096c1748f99a17`).
All 10 exact claim receipts and all 24 private aggregate artifacts were
independently rechecked against the current contract set, byte counts,
digests, and `0600` modes; the browser child's summary and 10-artifact
manifest were also rechecked. The source tree was dirty, so the receipt
deliberately records `candidateAcceptance=false`; T164/T167/T171 still own
exact installed-candidate binding.

T155's current privacy aggregate passed 73/73 exact tests, real
Security.framework Keychain, fresh CLI/TUI/WebUI, and real-Lima canary lanes at
`.artifacts/045/privacy-current-2/run-20260730T174215Z-43994/summary.json`
(`sha256:ef43c99eb07f1a9b34e8b4b988735720e85321afd19e04c8815e2b996857efd4`).
All eight exact claim receipts, 52 private aggregate artifacts, 24 UI child
artifacts, and 10 Lima child artifacts were independently rechecked against
the current contract set, exact test-event sets, byte counts, digests, and
`0600` modes. The gate observed zero raw canary hits across API, evidence,
export, index, log, process listing, store, support, and UI sinks, including
real Keychain metadata output. The source tree was dirty, so the receipt
deliberately records `candidateAcceptance=false`; T164/T171 still own exact
installed-candidate binding.

T156's historical performance aggregate passed local query/render, daemon/TUI,
five real-Chrome freshness samples, seven measured real-Lima warm attaches,
paired reference work, observer CPU/RSS/event/loss sampling, recovery, and
real quota pressure under the superseded seven-sample methodology at
`.artifacts/045/performance-current/run-20260730T193602Z-54620/summary.json`
(`sha256:77df91b45ffc3f3e308e67dbbd7aa6c1a72649ad83f755ddf67fb3ce5a31687d`).
The measured reference overhead was 4.298%, warm-attach p95 was 973.290 ms,
browser freshness p95 was 79.797167 ms, and 6/7000 dropped events
(0.085714%) were fully reflected in degraded coverage. All three exact claim
receipts, 172 private artifacts, 1732 source-manifest entries, raw-sample
percentiles, byte counts, digests, and modes were independently rechecked.
That run is diagnostic only: the current release evidence contract requires
thirty recorded real-Lima samples plus warmup, explicit quiet-host
confirmation, a passing three-snapshot sustained-contention preflight, private
diagnostics, and an exact one-sided 95% median upper confidence bound at or
below ten percent. It must be rerun on a quiet host.
The source tree was dirty, so the receipt deliberately records
`candidateAcceptance=false`; T156/T163/T171 still own exact clean-candidate
binding.

T158 activates `scripts/release/build-candidate.sh` as the clean package
judge. It derives `SOURCE_DATE_EPOCH` from the exact commit, rebuilds with two
independent Go caches, and accepts only byte-identical ustar/gzip archives,
package manifests, and file inventories. A disposable clean-snapshot
validation passed the exact 140-file inventory, all 9 manifest-listed Go
binaries, 6 helper manifests, 66 packaged schemas, 8 embedded browser assets,
runtime catalog/contract/artifact binding, package verification, and
symbol-level scans of every final binary. That disposable validation proves
the judge implementation, not the current repository candidate: the main
worktree is still dirty, no T158 candidate pointer was produced, and
T163/T171 retain exact final-candidate binding.

T159 activates `scripts/release/test-package-lifecycle.sh` as the exact
package lifecycle judge. A disposable clean-snapshot run consumed the
byte-bound T158 archive without rebuilding, verified the immutable published
`v0.1.0-alpha.3` receipt and 61 MB download, and passed clean install,
Keychain/legacy-export migration guidance, same-candidate reinstall, exact
temporary-store legacy-data discard, old-to-new upgrade, normal uninstall
absence, and durable/unrelated-data preservation. The result contained 23
private digest-bound artifacts and remained `local-only`. That run validates
the judge implementation but was deleted with its synthetic commit; T163,
T164, T165, and T171 still own accepted main-candidate, installed-machine, and
publication-absence evidence.

T163–T165 add three fail-closed closure judges. The evidence collector
independently resolves private pointers and artifact digests, extracts the
package, validates all required gate identities, and requires schema-valid
installed-machine and publication-absence receipts before it can emit
`stage=final-ready`. The installed-machine judge restricts destructive scope
to the recognized installation and exact current-user store, consumes the
accepted archive without rebuilding, exercises the packaged CLI/TUI/WebUI and
real Lima path, and scans retained state for transient proxy and control
secrets. The publication judge is read-only and double-observes tag, Release,
remote formula, source, and local tap state. Their negative preflights and
receipt schemas are active, but the rows remain unaccepted until the final
clean candidate produces both exact receipts and T171 recollects the manifest
with `--require-closure`.

Implementation mutations change production behavior or a production contract;
they do not delete or weaken the assertion. Judge fixtures change the
candidate input, evidence, schema, digest, or reported outcome; they do not
modify the judge. Every mutation runner must:

- identify its exact `M045-*` or `N045-*` ID in output;
- fail only after reaching the named assertion or judge;
- retain a 0600 log and digest below `.artifacts/045/local/mutations/`;
- restore the source or fixture and rerun the same command green;
- reject `not-run`, `unsupported`, `reduced`, or an unrelated crash as proof.

## Authority and mutation claims

| ID | Claim and requirements | Direct production assertion | Independent release judge | Required mutation and false-green fixture | Current disposition |
| --- | --- | --- | --- | --- | --- |
| A01 | Editing is client-local and has no authority before Manager review and explicit confirmation (FR-009–FR-011). | `go test ./internal/daemon ./internal/app -run 'Test(BrowserProfileTransactionDraftReviewConfirmApplyAndStalePlan\|TUIConfigPTYDraftReviewConfirmApplyAndTerminalEvidence\|ConnectPlanAndApplyUseExactReviewedOperation\|ConnectWithoutTTYOrYesLeavesReviewedPlanUnapplied)' -count=1` | `scripts/gates/release-candidate-ui.sh` must observe no Manager apply request before confirmation and one exact request after confirmation. | `M045-A01`: make draft editing call Apply; direct assertion must report the premature request. `N045-A01`: fixture reports a successful UI state without an apply request; UI judge must reject it. | `active-local-gate`; T154 recorded zero draft apply requests, one confirmed request, and the exact operation ID; T161 added the same invariant to the stable CLI connection path. |
| A02 | A plan binds exact profile revision, canonical changes, digest, and expiry; stale or drifted plans have no effect (FR-012–FR-013). | `go test ./internal/manager -run 'Test(ProfileTransactionRejectsOutOfBandDriftBeforeAnyEffect\|ProfileTransactionRejectsCanonicalReplanDrift\|ProfileTransactionExpiredPlanIsCancelledAndRemoved\|ConfigurationPlanDigestUsesCanonicalChangesAndBindsOperation)' -count=1` | `scripts/gates/release-candidate.sh` must reject a stale base revision, changed canonical change, digest mismatch, and expired plan. | `M045-A02`: bypass one stale-plan comparison; assertion must observe a provider or persistence effect. `N045-A02`: forge a passing mutation receipt with a mismatched base revision; judge must reject it. | `active-local-gate`; source-overlay and judge-negative proofs are wired into T152. |
| A03 | Accepted mutations have stable operation IDs, exact replay, and one conflicting owner (FR-014–FR-015, FR-065). | `go test ./internal/manager -run 'Test(ProfileTransactionConcurrentClientsCommitExactlyOneReviewedPlan\|ProfileTransactionResponseLossRetryReplaysTerminalResult\|ProfileTransactionReportsConflictingMutationKeyOwner\|ProfileTransactionAndAttachUseSameLifecycleMutationKey\|ProfileTransactionInspectPlanRevalidatesExactOperationBinding)' -count=1 && go test ./internal/app -run 'Test(SecretApplyFailureKeepsExactOperationRecoveryIdentity\|SecretApplyOutcomeGuidanceUsesExactOperationWithoutReplay\|ConnectApplyFailureKeepsExactOperationRecoveryIdentity\|ConnectRecoveryGuidanceKeepsExactOperationIdentity)' -count=1` | `scripts/gates/release-candidate.sh` must require one terminal operation, one effect, exact replay, visible blocker ownership, and recovery guidance that retains the reviewed operation ID. | `M045-A03`: generate a fresh operation/effect during retry or discard the operation ID from uncertain CLI recovery; assertion must report duplication or unsafe replacement guidance. `N045-A03`: evidence contains two winners or omits the blocker owner/operation ID; judge must reject it. | `active-local-gate`; source-overlay and judge-negative proofs are wired into T152, and T161 review regressions retain and terminally replay the exact secret/configuration operation ID after an uncertain response. |
| A04 | Secret create/update/rotate/delete uses typed authenticated operations, stores only references in profiles, and never echoes values (FR-018–FR-019). | `go test ./internal/manager ./internal/daemon ./internal/secrets -run 'Test(SecretAPIPlanApplyReplayDeleteAndListNeverExposeValue\|SecretServicePlansWithoutValueAndAppliesExactlyOnce\|SecretRecoveryRequiredResumesAfterProviderBecomesAvailable\|SecretAPIReferenceFeedsConfigurationTransactionWithoutValueEcho)' -count=1` | `scripts/gates/release-candidate-privacy.sh` plus `scripts/gates/keychain-real.sh` must scan API, operation, audit, process, and Keychain metadata output for the canary. | `M045-A04`: copy a secret value into a public plan/result or skip generation invalidation; assertion must expose it or observe stale use. `N045-A04`: inject a secret canary into an otherwise passing privacy artifact; judge must reject it. | `active-real-backend-gate`; T155 proved typed/reference-only authority, generation invalidation, real Keychain mutation, stdin-only process argv, and canary-free public/metadata output. |
| A05 | Network changes stage, probe, activate, prove, commit/drain, or rollback; staging alone never claims changed traffic (FR-020, FR-023). | `go test ./internal/manager -run 'Test(NetworkTransitionRouteStagesProbesActivatesProvesDrainsAndCommits\|NetworkTransitionFailureRollsBackWithoutLeakingProviderError\|NetworkTransitionStageFailureProvesEffectiveState\|NetworkTransitionRollbackUnprovedNeverClaimsPriorEffective\|ProfileTransactionDrivesCheckpointedLiveRouteTransition\|ProfileTransactionCommitsAllLiveEnvironmentRoutesAsOneBatch)' -count=1` | `scripts/gates/release-candidate-lima.sh` must observe the full provider sequence and exact route evidence from the generic configuration transaction. | `M045-A05`: commit after stage or after an unproved probe; assertion must reject false success. `N045-A05`: real-Lima artifact says `committed` without activation/proof evidence; judge must reject it. | `active-real-backend-gate`; T153 observed the exact five-effect sequence and durable terminal evidence on real Lima. |
| A06 | Proxy/direct eligibility changes do not require daemon stop, do not forcibly end active sessions, preserve existing connection bindings, and move only new eligible connections (FR-021–FR-022, SC-013). | `go test ./internal/manager ./internal/network -run 'Test(NetworkTransitionRouteStagesProbesActivatesProvesDrainsAndCommits\|ProfileTransactionCommitsAllLiveEnvironmentRoutesAsOneBatch\|ProfilePostureChangeCommitsDesiredAndPreservesActiveSessions\|EnvironmentGatewayAcceptedConnectionKeepsPreviousRoute\|GatewayRouteAndSecretGenerationBindAtAcceptAcrossSwitchAndRollback\|EnvironmentGatewayConcurrentTransitionWaitIsBounded)' -count=1` plus the real transition journey reserved by T153. | `scripts/gates/release-candidate-lima.sh` must retain one old connection and prove one new connection on the committed route while the daemon PID and session stay stable. | `M045-A06`: drain or rebind the old connection, or require daemon restart. `N045-A06`: omit daemon/session identity or use only post-transition connections; judge must reject incomplete evidence. | `active-real-backend-gate`; T153 retained the existing connection/session and proved only new connections moved to the rotated proxy without daemon or VM recreation. |
| A07 | Stale, disconnected, gap-detected, restarted, or credential-expired clients are read-only until an authoritative reseed (FR-008, FR-024). | `go test ./internal/daemon ./internal/app -run 'Test(BrowserSSEGapIsStickyReadOnlyUntilAuthoritativeReseed\|BrowserSSECredentialRotationExpiresStreamAndRequiresFreshSeed\|BrowserSSEDaemonRestartRejectsOldInstanceUntilReseed\|TUIConfigPTYStalePlanIsReadOnlyWithoutApply)' -count=1` | `scripts/gates/browser-console.sh` and the PTY lane in `scripts/gates/release-candidate-ui.sh`. | `M045-A07`: leave one mutation control enabled in STALE; browser/TUI assertion must observe it. `N045-A07`: browser result claims stale safety without a failed mutation attempt and fresh reseed; judge must reject it. | `active-local-gate`; T154 proved disabled stale controls, one rejected mutation attempt, and authoritative reseed in PTY/browser journeys. |
| A08 | Decision claims have visible bounded leases, explicit release/expiry, authenticated takeover, and provider revocation (FR-064). | `go test ./internal/manager -run 'Test(DecisionClaimLeaseIsVisibleBoundedAndExplicitlyReleasedOnDisconnect\|DecisionClaimExpiryEmitsReleaseAndStaleClaimantFails\|DecisionExpiredClaimTakeoverRequiresExplicitExactRevision\|HostFSWriteDecisionDisconnectReleaseRevokesProviderToken)' -count=1` | `scripts/gates/release-candidate.sh` must validate lease owner, expiry, release event, and zero surviving provider authority. | `M045-A08`: retain a provider token after disconnect or accept stale takeover. `N045-A08`: evidence omits release/expiry while claiming terminal safety; judge must reject it. | `active-local-gate`; source-overlay and judge-negative proofs are wired into T152. |
| A09 | Unexported activity is available only through authenticated local CLI/TUI/loopback WebUI reads (FR-054). | `go test ./internal/workloadobs/store ./internal/manager -run 'Test(UnauthenticatedActivityAPILeaksNoStoreEvidence\|ActivityRouteInventoryIsPrivateReadOnlyAndComplete\|ActivityRoutesRejectAmbiguousInvalidAndUnknownQueriesBeforeProvider)' -count=1` | `scripts/gates/browser-console.sh` proves wrong-token refusal; `scripts/gates/release-candidate-privacy.sh` must probe every local surface. | `M045-A09`: permit a missing/wrong token on one activity route. `N045-A09`: fixture marks auth passed while one unauthenticated response contains owner metadata; judge must reject it. | `active-real-backend-gate`; T155 bound authenticated CLI/TUI/loopback WebUI, zero unauthenticated owner-data responses, non-loopback refusal, and the real-Lima store boundary. |
| A10 | Export/share uses reviewed plan/apply authority, deterministic stripping, explicit path policy, and no publication on apply (FR-060–FR-061, FR-071). | `go test ./internal/manager -run 'Test(ActivityExportPreservesLocalViewButReviewsAndRedactsArtifact\|ActivityExportApplyRejectsUnconfirmedTamperedAndStalePlans\|ActivityExportShareRequiresExistingDecisionAndDoesNotPublishOnApply)' -count=1` | `scripts/gates/release-candidate-privacy.sh` and the final publication-absence judge in T165. | `M045-A10`: allow unconfirmed export, bypass path acknowledgement, or call a publisher. `N045-A10`: fixture contains a publish receipt/tag/tap mutation or an unreviewed host path while claiming local-only success; judge must reject it. | `active-local-gate`; T155 proved reviewed digest-bound plan/apply, explicit path acknowledgement, and zero publication effects; the final publication-absence judge remains T165. |

## Attribution and explainability claims

| ID | Claim and requirements | Direct production assertion | Independent release judge | Required mutation and false-green fixture | Current disposition |
| --- | --- | --- | --- | --- | --- |
| AT01 | One cgroup-backed workload scope contains the target and all descendants, including reparented/forked children, and excludes unrelated guest processes (FR-025–FR-027, SC-006). | `go test ./internal/workloadobs/collector ./cmd/hideout-session-supervisor -run 'Test(ProcessNormalizerAttributesForkExecExitFastChildrenAndRefork\|ProcessNormalizerRejectsCrossCgroupAndUnknownExitWithoutReassignment\|Cgroup)' -count=1` | `scripts/gates/workload-observation-lima.sh` and the concurrent/tamper lane in T153 must compare exact cgroup/session/incarnation identities and unrelated-process canaries. | `M045-AT01`: accept a prefix/substring cgroup match or drop inherited descendants. `N045-AT01`: real artifact contains an unrelated PID or lacks the reparented child; judge must reject it. | `active-real-backend-gate`; T153 passed exact descendant, unrelated-process, concurrent-owner, and tamper assertions. |
| AT02 | Execution identity survives PID reuse and repeated exec, preserves exact parent/guest identity/lifecycle, and never reassigns unknown exits (FR-026, FR-029, FR-031). | `go test ./internal/workloadobs/collector ./internal/workloadobs/types -run 'Test(ProcessNormalizerPIDReuseCreatesDistinctExecutionIdentity\|ProcessNormalizerClosesAndChainsRepeatedExecInSamePID\|ProcessNormalizerUsesExactParentExecutionAndRejectsMismatches\|ExecutionIdentitySurvivesPIDReuseAndRequiresGuestIdentity)' -count=1` | T153 must compare the expected execution tree and stable IDs from the real observer stream to retained history. | `M045-AT02`: key executions only by PID or fabricate an exit status. `N045-AT02`: duplicate execution ID across PID reuse or omit guest identity; judge must reject it. | `active-real-backend-gate`; T153 passed the real observer execution-tree and PID-reuse identity matrix. |
| AT03 | Shared DNS/proxy mediators retain the original workload actor when proved and say unknown when not proved (FR-028). | `go test ./internal/workloadobs/collector -run 'Test(PacketFromKernelRecordAttributesForkedChildToInheritedExecution\|ProxyChunkFromKernelRecordPreservesForkedChildActor\|NormalizeKernelConnectionKeepsMissingActorAndEgressUnknown)' -count=1` | T153 must correlate mediator records to exact execution/session and include an intentionally unattributable sample. | `M045-AT03`: replace missing actor with the mediator process or a prior flow actor. `N045-AT03`: artifact labels an unbound mediator record exact; judge must reject it. | `active-real-backend-gate`; T153 passed exact mediator attribution and intentionally unknown actor samples. |
| AT04 | File facts distinguish supported operations, actor, path state/class, identity/count/bytes/time/outcome; aliases and races are labeled without fabricated canonical paths (FR-032–FR-036). | `go test ./internal/workloadobs/collector -run 'Test(FileNormalizerPreservesSupportedOperationsIdentityCountsAndBytes\|FileNormalizerLabelsAliasesSymlinksAndPathRacesWithoutFabrication\|KernelFileRecordDistinguishesDirectoryCreateAndRemove\|KernelFileRecordKeepsPartialTargetAndDirectoryTypeOnEvidenceLoss)' -count=1` | `scripts/gates/workload-observation-lima.sh` must compare exact file fixtures and coverage limitations. | `M045-AT04`: canonicalize an unresolved alias or collapse a destructive operation. `N045-AT04`: artifact invents a resolved path or omits one required operation; judge must reject it. | `green-local`; real workload gate active. |
| AT05 | Network facts preserve the exact actor, IP, port, protocol, result, interval/count, and effective route when known (FR-037). | `go test ./internal/workloadobs/collector -run 'Test(NormalizeKernelConnectionPreservesExactActorEndpointAndRouteEvidence\|NormalizeKernelConnectionUsesEventCredentialsForExactExecution\|NormalizeKernelConnectionRejectsMismatchedEvidence)' -count=1` | `scripts/gates/workload-observation-lima.sh` and T153 must compare connect4/connect6/TCP/UDP fixtures and effective route evidence. | `M045-AT05`: accept mismatched socket identity or overwrite unknown route with desired route. `N045-AT05`: artifact lacks actor/endpoint evidence or reports desired as effective; judge must reject it. | `active-real-backend-gate`; T153 passed the exact actor/endpoint/route workload matrix. |
| AT06 | DNS facts preserve actor, query, visible answers, outcome, time, and bounded parser uncertainty without retaining packet payload (FR-038). | `go test ./internal/workloadobs/collector -run 'Test(ParserConsumesUDPQueryResponseAndClearsWireBytes\|ParserSupportsAAAAAndSingleFramedTCPMessage\|ParserEmitsNegativeResponseWithoutCreatingCacheEvidence\|ParserRejectsMalformedTruncatedOversizedAndMismatchedEvidence)' -count=1` | Real workload/privacy gates must compare expected DNS facts and prove packet bytes absent from retained stores. | `M045-AT06`: retain packet bytes or associate a mismatched response. `N045-AT06`: artifact includes payload canary or wrong transaction actor; judge must reject it. | `green-local`; real workload/privacy gates active. |
| AT07 | Domain-to-connection attribution is only `exact`, `inferred`, or `unknown`; shared IP, cache, literal IP, encrypted DNS, tunnels, and missing evidence reduce confidence (FR-039–FR-040). | `go test ./internal/workloadobs/collector -run 'Test(NetworkCorrelatorUsesTTLBoundSameExecutionDNSInference\|NetworkCorrelatorDoesNotGuessSharedIPCacheLiteralOrEncryptedDNS\|NetworkCorrelatorUsesValidatedProxyTargetAsExactAndRejectsCrossBoundary)' -count=1` | T153 must judge the full correlation fixture matrix and reject any false exact attribution. | `M045-AT07`: promote inferred/shared-IP evidence to exact. `N045-AT07`: artifact reports an exact domain for the encrypted/literal/shared fixture; judge must reject it. | `active-real-backend-gate`; T153 passed the full exact/inferred/unknown correlation matrix without false exact attribution. |
| AT08 | Risk output is named, versioned, explainable, evidence-linked, deterministic, and separates observed behavior from policy and prevention (FR-041–FR-042). | `go test ./internal/workloadobs/risk -run 'Test(RiskEngineProducesVersionedExplainableDeterministicFindings\|RiskEngineKeepsObservedRiskSeparateFromPolicyDecision\|RiskEngineMapsEvidenceAttributionToHonestConfidence\|RiskEnginePreservesEachDestructiveEvidenceReference)' -count=1` | `scripts/gates/release-candidate.sh` and UI parity evidence must require rule/version/evidence/reason/next-action and distinct policy fields. | `M045-AT08`: replace explainable output with an aggregate score or mark `not-evaluated` as denied/prevented. `N045-AT08`: malformed risk omits an evidence ref or policy distinction; judge must reject it. | `active-local-gate`; T152 binds both mutation halves and T154 binds rule/version/evidence/next-action plus policy-versus-observation UI parity. |

## Redaction, privacy, and storage claims

| ID | Claim and requirements | Direct production assertion | Independent release judge | Required mutation and false-green fixture | Current disposition |
| --- | --- | --- | --- | --- | --- |
| R01 | Process/file observation never persists environment values, terminal input/full output, packet bytes, proxy auth bytes, or file contents (FR-030, FR-034). | `go test ./internal/workloadobs/collector ./internal/workloadobs/store -run 'Test(FileNormalizerRejectsCrossBoundaryAndNeverRetainsContents\|PacketFromKernelRecordClearsPayloadOnEveryFailure\|SOCKS5ParserClearsUsernamePasswordAndSupportsFragmentedAuth\|StoreRejectsUnredactedWrongOwnerAndCancelledWrites)' -count=1` | `scripts/gates/workload-privacy-lima.sh` and T155 scan store segments, indexes, process listings, logs, API/UI, support, export, and evidence. | `M045-R01`: retain one payload/content/auth buffer or add environment capture. `N045-R01`: place each canary in a passing artifact family; privacy judge must name and reject every sink. | `active-real-backend-gate`; T155 proved zero canary hits across all nine required sinks and directly exercised content, environment, DNS packet, proxy-auth, and tunnel-payload non-retention. |
| R02 | Managed values, encodings, URI userinfo, auth fields, sensitive args/query, split forms, and control-plane tokens are removed before persistence (FR-056–FR-057, SC-008). | `go test ./internal/workloadobs/redact ./internal/daemon -run 'Test(RedactorRemovesCredentialCanariesBeforePersistence\|RedactorHandlesSplitEqualsQueryAndAuthorizationForms\|RedactorFailsPrivateForTruncatedURIUserinfo\|CanariesAreAbsentFromEveryPostRedactionSink)' -count=1` | `scripts/gates/release-candidate-privacy.sh` must execute the complete canary matrix and require zero post-redaction hits. | `M045-R02`: skip URI/split/encoded/control token redaction one at a time. `N045-R02`: inject every canary class into each structured sink; judge must reject all fixtures. | `active-real-backend-gate`; T155 passed all eight canary classes, proved pre-persistence redaction, and observed zero persistence after redaction failure. |
| R03 | Authenticated local views preserve full guest/workspace paths by default while export applies separate path policy (FR-055, FR-061). | `go test ./internal/manager -run 'Test(ActivityExportPreservesLocalViewButReviewsAndRedactsArtifact\|ActivityExportPreservePathPolicyRequiresExplicitLocalAcknowledgement)' -count=1` and `scripts/gates/browser-console.sh`. | T155 must compare one exact local path with its reviewed export under each policy. | `M045-R03`: redact the local path or leak the host path into a redacted export. `N045-R03`: evidence uses the same value for local and redacted export without explicit preserve approval; judge must reject it. | `active-real-backend-gate`; T155 preserved the real authenticated local path, removed it from redacted export/support, and required explicit acknowledgement for preserve policy. |
| R04 | UI/docs disclose residual user-data risk and keep host audit and bounded workload store retention/fidelity contracts distinct (FR-058–FR-059, FR-069). | Help/docs contract tests plus `go test ./internal/app -run 'Test(PrivacyAndSecretHelpExplainStartupFallbackMigration\|EnglishAndChineseUserGuidesCoverSupportedJourney)' -count=1`. | T160 documentation truth and T168 terminology/privacy scans must require the disclosure and reject conflated retention claims. | `M045-R04`: remove the residual argv/path/domain disclosure from rendered help. `N045-R04`: docs fixture calls workload metadata a complete audit or claims arbitrary-secret discovery; judge must reject it. | `active-local`; T160 synchronized the product/design/threat/test/formal/privacy/retention/recovery/help/support contracts and passed help, matrix, Markdown, and docs-truth judges; the T168 terminology/privacy scan and exact T171 binding remain required. |
| R05 | Store paths and files are host-private, reject symlink/hardlink/traversal replacement, and are unavailable through unauthenticated APIs (FR-053–FR-054). | `go test ./internal/workloadobs/store -run 'Test(StoreCreatesOnlyHostPrivateDirectoriesAndFiles\|StoreRejectsSymlinkAndHardlinkReplacement\|StoreRejectsTraversalWhileHashingUntrustedOwnerLabels\|UnauthenticatedActivityAPILeaksNoStoreEvidence)' -count=1` | T155 must inspect modes/ownership and attempt target/unauthenticated reads on the exact package. | `M045-R05`: create one segment 0644 or follow a replacement link. `N045-R05`: evidence reports private while mode/owner or target-read probe is wrong; judge must reject it. | `active-real-backend-gate`; T155 proved `0700` directories, `0600` files, replacement-link refusal, target-VM absence, and unauthenticated-read refusal; exact installed-package rerun remains T164/T171. |

## Coverage, ordering, retention, and performance claims

| ID | Claim and requirements | Direct production assertion | Independent release judge | Required mutation and false-green fixture | Current disposition |
| --- | --- | --- | --- | --- | --- |
| C01 | Process, file, network, and DNS expose independent Available/Partial/Unavailable intervals with reason, generation, loss count, and retention gap (FR-043–FR-044). | `go test ./internal/workloadobs/coverage ./internal/daemon -run 'Test(TimelinePreservesLossAndRecoveryAsSeparateIntervals\|TimelineRecordsEveryRequiredCoverageDegradationAsAnInterval\|ActivityServiceRegistersCoverageAtOneBoundaryInstant)' -count=1` | Workload gates and UI parity must require all four subsystem rows and complete interval fields. | `M045-C01`: reuse one global status or omit reason/generation/loss. `N045-C01`: artifact has all four names but one incomplete/shared interval; judge must reject it. | `active-local-gate`; real workload evidence is active and T154 independently rendered all four subsystem states with reason, generation, loss, and retention details. |
| C02 | Event loss, unsupported capability, restart, escape, truncation, schema mismatch, redaction loss, and stream gap downgrade only the affected coverage (FR-045, SC-007). | `go test ./internal/workloadobs/coverage ./internal/daemon -run 'Test(TimelineRecordsEveryRequiredCoverageDegradationAsAnInterval\|ActivityServiceRegistersBeforeIngestAndAccountsEveryLossSource\|ActivityServiceDegradesCoverageWhenRedactionSnapshotIsUnavailable)' -count=1`. | `scripts/gates/workload-privacy-lima.sh`, browser gap evidence, and T153 must inject every degradation and require a visible reason. | `M045-C02`: suppress one degradation transition. `N045-C02`: fixture records a drop/gap but remains Available or omits the reason; judge must reject it. | `active-real-backend-gate`; T153 passed real loss accounting and visible degradation reasons, while the all-sink privacy aggregate remains T155. |
| C03 | Partial or unavailable intervals are never rendered as zero activity or healthy fresh state (FR-046). | `go test ./internal/workloadobs/coverage ./internal/liveconsole ./internal/daemon -run 'Test(TimelineRejectsFalseAvailableAndRecordsRestartAndRetention\|BrowserSSEGapIsStickyReadOnlyUntilAuthoritativeReseed)' -count=1` | Browser/TUI candidate judges must inspect visible coverage text and mutation-disabled state. | `M045-C03`: render Partial as zero events/Available. `N045-C03`: screenshot/result claims healthy while coverage artifact is partial; judge must reject it. | `active-local-gate`; T154 proved reduced coverage stays visibly non-healthy and never enables mutation controls or renders unavailable as zero activity. |
| C04 | Authoritative ordering is monotonic per stream; duplicates/reconnects do not double count and unfillable gaps force reseed (FR-047–FR-048). | `go test ./internal/liveconsole ./internal/daemon ./internal/workloadobs/store -run 'Test(BrowserSSEGapIsStickyReadOnlyUntilAuthoritativeReseed\|SealScopesBoundIndependentPerCPUSequencesWithoutTimelineOrdering)' -count=1` plus reducer sequence tests. | `scripts/gates/browser-console.sh` and T154 require SSE-only healthy updates, sticky gap state, and authoritative reseed. | `M045-C04`: accept a sequence gap or double-apply a duplicate. `N045-C04`: event log skips a sequence while browser result says LIVE without snapshot reseed; judge must reject it. | `active-local-gate`; T154 proved zero duplicate count delta, sticky reseed on gaps, no false LIVE state, and no healthy polling. |
| C05 | Reusable history is exact-incarnation/session scoped; disposable history is lifecycle-scoped; quota/age pruning is ordered, bounded, and exposes a history gap (FR-049–FR-052, SC-010). | `go test ./internal/workloadobs/store ./internal/manager -run 'Test(QuotaPrunesOldestSealedAcrossOwnersAndBoundsOvershoot\|OwnerRetentionMaxAgePrunesExpiredSealedHistory\|ReusableOwnerQueriesRemainInsideSelectedSession\|DisposableActivityCleanupDeletesOnlyTerminalOwnerAndPreservesAudit)' -count=1` | `scripts/gates/workload-privacy-lima.sh` and T156 must bind quota/overshoot/pruning evidence to the exact owner. | `M045-C05`: prune newest/foreign owner or hide retention gap. `N045-C05`: quota artifact exceeds the bound, changes owner, or reports complete history after pruning; judge must reject it. | `historical-real-backend-diagnostic`; the superseded T156 run observed stable query ownership, oldest-sealed pruning, no foreign-owner pruning, a visible history gap, and overshoot bounded by exactly one active segment. Fresh accepted T156 evidence remains required. |
| C06 | Attach, observer CPU/RSS, event/drop rate, storage overshoot, query/render latency, and UI freshness stay within frozen supported budgets (FR-068, SC-003, SC-011). | `go test ./internal/workloadobs -run TestReleaseFrozenDefaultsRemainWiredAcrossPackages -count=1` locks the shared v1 defaults; existing bounded unit tests and `scripts/gates/browser-console.sh` enforce browser thresholds. | `HIDEOUT_PERFORMANCE_QUIET_HOST_CONFIRMED=1 scripts/gates/release-candidate-performance.sh` must evaluate raw samples, percentile and confidence-bound math, quota bounds, private host-state diagnostics, and exact candidate identity. | `M045-C06`: add measured delay/allocation or reduce a quota guard. `N045-C06`: fixture forges percentile/confidence, omits samples, changes units, lacks quiet-host confirmation, or exceeds one threshold while reporting passed; judge must reject it. | `pending-real-backend-gate`; historical T156 runs retained raw samples and passed their then-current frozen thresholds, but their seven-sample methodology is superseded. T157 froze the resulting v1 values and change protocol; fresh thirty-sample quiet-host T156 evidence whose median and one-sided 95% upper confidence bound both pass, plus clean-candidate acceptance, remain T156/T163/T171. |

## Help and UI claims

| ID | Claim and requirements | Direct production assertion | Independent release judge | Required mutation and false-green fixture | Current disposition |
| --- | --- | --- | --- | --- | --- |
| H01 | Primary help leads with the ordinary setup/run/inspect/change/recover journey, copyable examples, and one clear expanded-help route (FR-001–FR-002, SC-001). | `go test ./internal/app -run 'Test(PrimaryHelpShowsOrdinaryJourneyBeforeExpandedIndex\|HelpFindsCommonTaskInAtMostTwoInvocations\|HelpGoldens)' -count=1` | T152/T167 run every documented command from the exact package and compare normalized help goldens. | `M045-H01`: move the ordinary path behind the expanded inventory or corrupt one copyable command. `N045-H01`: package help fixture omits/reorders a required ordinary task while reporting complete; judge must reject it. | `active-local-gate`; T152 mutation proof is wired, while the installed-package quickstart judge remains T167. |
| H02 | Contextual help states purpose, syntax, prerequisites, effects, safety, recovery, next action, audience/stability, and the complete top-level route inventory (FR-003). | `go test ./internal/app -run 'Test(ContextualHelpIsSuccessfulAndWritesNoState\|CommandCatalogCoversEveryTopLevelRoute\|CommandCatalogMetadataIsCompleteAndSearchable\|CommandCatalogValidationRejectsStaleOrAmbiguousEntries)' -count=1` | T152 must compare Cobra routes to the render-only catalog and reject missing/extra/ambiguous entries. | `M045-H02`: remove one metadata field or route. `N045-H02`: fixture preserves count but substitutes an unknown route/duplicate alias; judge must reject it. | `active-local-gate`; source-overlay and judge-negative proofs are wired into T152. |
| H03 | Help is read-only, unknown topics fail with a useful expanded-index hint, and CLI/TUI/Web consume the same detached catalog (FR-001–FR-003, SC-014). | `go test ./internal/app ./internal/daemon -run 'Test(ContextualHelpIsSuccessfulAndWritesNoState\|HelpRejectsUnknownTopicWithExpandedIndexHint\|OperatorHelpProjectionIsValidVisibleAndDetached\|LoopbackUIReceivesRenderOnlyHelpCatalog)' -count=1` | T154/T167 compare help search/output across installed CLI/TUI/WebUI. | `M045-H03`: mutate help to write profile state or give WebUI a divergent catalog. `N045-H03`: fixture has matching labels but a changed command/effect; judge must reject it. | `active-local-gate`; T154 proved zero help writes, useful unknown-topic guidance, and equal catalog/effect projections; installed-package parity remains T167. |
| U01 | The TUI is a persistent alternate-screen, keyboard-driven HUD with prioritized workload/coverage/risk/blocker state, deeper views, Enter dialogs, plain fallback, and terminal restoration (FR-004–FR-005). | `go test ./internal/app -run 'Test(TUIProgramEntersAndRestoresAlternateScreenWithKeyboardNavigation\|TUIProgramRestoresAlternateScreenAfterCtrlC\|TUIProgramRestoresAlternateScreenAfterPanic\|TUIRejectsNonTTYWithOnceRecovery\|TUIOnceIsPlainAndWorksWithoutTTY)' -count=1` | `scripts/gates/release-candidate-ui.sh` must drive a real PTY, inspect stable text, open/close dialogs, and verify terminal restoration. | `M045-U01`: omit alternate-screen restoration, Enter action, or primary workload state. `N045-U01`: PTY capture lacks one escape/visible state but summary says passed; judge must reject it. | `active-local-gate`; T154 drove real PTYs and proved alternate-screen entry/restoration, keyboard dialog access, primary facts, and plain once mode. |
| U02 | Browser deep history exposes execution tree, file/network/DNS facts, correlation, compound filters, retained gaps, bounded DOM, and no healthy polling (FR-006). | `go test ./internal/daemon/uiweb_assets ./internal/manager -run 'Test(ActivityViewModelsCoverOwnerTimelineTreeSubjectsAndEvidence\|ActivityBrowserAPIPathProcessTimeRiskAndExecutionCorrelation\|CompoundFiltersCursorInheritanceRetainedGapAndCorrelation)' -count=1` | `scripts/gates/browser-console.sh`. | `M045-U02`: remove newest-first ordering, one filter, correlation, DOM bound, or add polling. `N045-U02`: browser result omits a fact/filter or exceeds bounds while claiming passed; gate must reject it. | `active-local-gate`; T146 evidence retained under `.artifacts/045/ui/browser/`. |
| U03 | CLI/TUI/Web distinguish desired/effective/transition/evidence, live/next-attach/recreate scope, immutable older snapshots, and stale/blocked/failed/rollback states (FR-007–FR-008, FR-016–FR-017). | `go test ./internal/liveconsole ./internal/app ./internal/daemon/uiweb_assets -run 'Test(ParityFixtureSnapshotAndDetailsExposeIdenticalSurfaceFacts\|TUIConfigurationClientUsesSharedTransactionWithoutValueEcho)' -count=1` plus browser configuration journey. | T154 compares normalized surface facts and state labels against one Manager snapshot/operation. | `M045-U03`: render desired as effective or suppress older/stale/rollback state. `N045-U03`: one surface fixture diverges while top-level counts match; judge must reject it. | `active-local-gate`; T154 proved equal surface facts, distinct desired/effective state, immutable prior snapshots, and all live/next-attach/recreate/stale/blocked/failed/rolled-back labels. |
| U04 | CLI, TUI, and browser enforce Draft→Plan→diff/effects/blockers→Confirm→Apply→terminal evidence and response-loss recovery on their canonical configuration paths (FR-009–FR-015, FR-063). | `go test ./internal/app ./internal/daemon -run 'Test(ConnectPlanAndApplyUseExactReviewedOperation\|ConnectWithoutTTYOrYesLeavesReviewedPlanUnapplied\|ConnectApplyFailureKeepsExactOperationRecoveryIdentity\|TUIConfigPTYDraftReviewConfirmApplyAndTerminalEvidence\|TUIConfigPTYResponseLossKeepsOperationLookup\|BrowserProfileTransactionDraftReviewConfirmApplyAndStalePlan\|BrowserProfileTransactionResponseLossRetryIsIdempotent)' -count=1` | Browser gate active; T154 must run equivalent PTY and first-time journeys and compare operation ID/terminal phase; T167 repeats the installed CLI quickstart. | `M045-U04`: enable Apply before explicit confirmation or treat response as success. `N045-U04`: UI/CLI artifact has no canonical diff/effect evidence or mismatched operation ID; judge must reject it. | `active-local-gate`; T154 proved the complete TUI/browser flow, while T161 added stable CLI plan/apply, zero non-TTY pre-confirmation mutation, exact-ID recovery, and terminal replay; installed-candidate CLI proof remains T167. |
| U05 | Dynamic text is control-safe; tab/dialog focus is keyboard accessible; layout is responsive; rows/dialogs are bounded; credentials leave the URL fragment before requests (FR-004, FR-006, FR-054). | `go test ./internal/daemon/uiweb_assets ./internal/daemon -run 'Test(Presentation\|BrowserCredentialGrammarMatchesManagerIssuer\|HandlerServesTypedAssetsWithStrictBrowserBoundary\|LoopbackUIRejectsForeignHostAndOriginOnDaemonEndpoints)' -count=1` and `scripts/gates/browser-console.sh`. | Browser gate requires zero accessibility violations, keyboard navigation/focus return, mobile layout, max 200 mounted rows, strict auth/Host/Origin refusal, and fragment hygiene. | `M045-U05`: use unsafe HTML, allow unbounded rows, break focus return, keep the token fragment, or bypass the loopback Host/Origin guard. `N045-U05`: result reports zero violations while injected DOM contains the named violation/token or accepts a foreign request origin; judge must reject it. | `active-local-gate`; T154's real-Chrome child passed injection, accessibility, focus, responsive layout, credential hygiene, and the 200-row bound; T161 added the all-daemon-route Host/Origin regression; exact installed-candidate rerun remains T171. |

## Recovery and cleanup claims

| ID | Claim and requirements | Direct production assertion | Independent release judge | Required mutation and false-green fixture | Current disposition |
| --- | --- | --- | --- | --- | --- |
| RC01 | Accepted configuration/observation/lifecycle operations remain queryable after client or daemon restart (FR-062). | `go test ./internal/daemon ./internal/manager -run 'Test(DaemonStartReconcilesAcceptedProfileBeforeServing\|DaemonStartReconcilesAcceptedNetworkWithoutRouteReplay\|DaemonSnapshotReseedsRecoveredOperationWithoutAdoptingOrphan\|StartupOperationRecovery)' -count=1` | `scripts/gates/recovery.sh` and T153 crash/restart lane. | `M045-RC01`: discard an accepted operation or re-execute an already proved effect after restart. `N045-RC01`: artifact omits pre/post operation identity or terminal evidence; judge must reject it. | `active-real-backend-gate`; T153 passed durable operation lookup and reconciliation across real daemon crashes. |
| RC02 | A response, backend return, timer, or single UI sample cannot independently prove completion; durable effect evidence is required (FR-063). | `go test ./internal/manager -run 'Test(OperationRecoveryCrashMatrix\|OperationRecoveryTraceRefinesOperatorConfigurationModel\|OperationRollbackTraceRefinesOperatorConfigurationModel)' -count=1` | `scripts/gates/recovery.sh` requires all 16 crash points and kills `success-without-proof`. | Existing `M045-RC02` mapping: `HIDEOUT_RECOVERY_TRACE_MUTATION=success-without-proof`; invariant failure is retained. `N045-RC02`: forged recovery summary says passed with missing proof/effect artifact; T148 judge must reject it. | Implementation mutant `observed-red`; judge-negative fixture observed by T148. |
| RC03 | Running effects are observed rather than replayed, response retry is exact, and terminal events are emitted at most once (FR-062–FR-066). | Same recovery/refinement suite plus profile/secret/network response-loss tests. | `scripts/gates/recovery.sh`. | Existing mutants map to `M045-RC03A=replay-running-effect` and `M045-RC03B=duplicate-terminal-event`; both are observed red. `N045-RC03`: duplicate-effect or duplicate-terminal evidence fixture must fail the summary judge. | Implementation mutants `observed-red`; judge-negative fixture observed by T148. |
| RC04 | Stale plans, concurrent clients, observer/event loss, daemon crash, rollback, stop proof, and lifecycle deletion satisfy bounded safety and progress (FR-066, SC-004, SC-012). | The exact 12-config, 10-module, and 12-test inventory in `formal/inventory.json`, including production refinement, secret authority-reset, observer tail drain/forced-close accounting, and 16-point crash-matrix traces. | `scripts/gates/formal.sh` records all 76 invariants and 19 liveness properties in `.artifacts/045/formal/`; `scripts/gates/formal-verify.sh` independently rechecks the repository inventory, source/log digests, and exact pass sets. | `M045-RC04`: one named trace mutant per invariant/liveness boundary. `N045-RC04`: omit a required cfg/result, add a counterexample, or bind a stale model digest; all three formal-verifier fixtures are retained killed. | `active-local-formal`; bounded formal and dirty-aware real-Lima evidence pass with `candidateAcceptance=false`, while exact clean-candidate binding remains T163. |
| RC05 | Failed network validation/stage/activate/proof restores or preserves the prior route and exposes actionable recovery without false success (FR-023, FR-066). | Network transition and recovery suites, including `TestProfileTransactionLiveRouteFailureRestoresProfileAndRoute`, `TestProfileTransactionBatchActivationFailureRestoresEveryRoute`, `TestSecretRotateStartupRecoveryCompletesCommittedGenerationWithoutRouteReplay`, and exact-proof rejection tests. | T153 must crash each production network boundary and independently probe effective routing after reconciliation. | `M045-RC05`: claim prior effective route without rollback proof or replay activation. `N045-RC05`: artifact has terminal success/rollback without exact generation/probe evidence; judge must reject it. | `active-real-backend-gate`; T153 crashed all five durable network boundaries, observed rollback/rollback/success/success/success, proved no replay and stable VM boot during reconciliation, then independently probed the recovered route. |
| CL01 | Disposable activity is deleted only after terminal lifecycle proof; reusable activity is not deleted by session exit; audit is preserved (FR-050). | `go test ./internal/manager ./internal/daemon -run 'Test(DisposableActivityCleanupDeletesOnlyTerminalOwnerAndPreservesAudit\|SessionRegistryDeletesOnlyDisposableActivityAfterCleanTerminal\|DaemonActivityCleanupRefusesLiveOwner)' -count=1` | Real privacy/cleanup gates and T153 must inspect exact owner absence plus retained audit after release. | `M045-CL01`: delete a live/reusable owner or retain terminal disposable activity. `N045-CL01`: cleanup artifact checks only session exit, not terminal proof/owner/audit; judge must reject it. | `active-real-backend-gate`; T153 passed exact terminal-owner cleanup and retained-audit assertions. |
| CL02 | Clean/delete/recreate removes exactly the prior reusable incarnation only after destructive lifecycle evidence, never a newly created incarnation (FR-051, SC-009). | `go test ./internal/manager -run 'Test(ActivityCleanupRemovesExactReusableIncarnationsForDestructiveLifecycle\|ActivityCleanupPlanNeverDeletesAnIncarnationCreatedAfterPlanning\|ActivityCleanupRejectsCrossScopeAndTamperedPlans)' -count=1` | `scripts/gates/workload-privacy-lima.sh` and T153 must record old/new incarnation identities and disk/memory results. | `M045-CL02`: delete by environment name/current path instead of planned incarnation. `N045-CL02`: artifact omits new-incarnation preservation or reports only an empty query; judge must reject it. | `active-real-backend-gate`; T153 recorded old/new incarnation identities and proved exact old-incarnation removal with new-incarnation preservation. |
| CL03 | Quota/age/corruption recovery is ordered and explicit: torn/corrupt tails are repaired or quarantined, oldest sealed data is pruned, and coverage reports the gap (FR-045, FR-052). | `go test ./internal/workloadobs/store -run 'Test(ActiveSegmentRepairsTornTailAndReportsCoverageGap\|ActiveSegmentCRCFailureTruncatesAfterLastValidFrame\|CorruptSealedSegmentIsQuarantinedAndNeverReturned\|QuotaPrunesOldestSealedAcrossOwnersAndBoundsOvershoot)' -count=1` | Privacy/performance gates must retain loss/quota/quarantine evidence and exact owner bounds. | `M045-CL03`: return corrupt frames, prune newest, or hide the coverage gap. `N045-CL03`: fixture claims complete history after repair/prune or omits quarantine; judge must reject it. | `active-with-pending-performance-evidence`; T155 and historical T156 diagnostics observed zero corrupt frames returned, quarantine, visible coverage gaps, oldest-sealed pruning, and the one-active-segment measured overshoot bound; fresh T156 performance binding remains required. |
| CL04 | Candidate install/upgrade/reinstall/uninstall and authorized legacy-data discard affect only the exact package/store scope and leave no accidental publication (FR-070–FR-071). | `scripts/gates/package-components.sh` proves the local observer/UI component package and lifecycle mechanics with `candidateAcceptance=false`; `scripts/release/build-candidate.sh` rejects dirty source, rebuild drift, missing or changed package files, helper/UI/runtime digest drift, and final-binary advisories; `scripts/release/test-package-lifecycle.sh` consumes that exact archive and proves scoped install, upgrade, reinstall, discard, and uninstall behavior without rebuilding; `scripts/release/install-local-candidate.sh` repeats the packaged journey on the target machine with exact-scope guards. | `scripts/release/verify-publication-absence.sh` must prove two absent tag/Release observations, stable remote formula bytes without candidate material, and unchanged source/local tap state; `scripts/release/collect-evidence.sh --require-closure` independently validates both closure schemas and every referenced private artifact. | `M045-CL04`: preserve forbidden legacy activity, delete unrelated data, omit observer/UI asset removal, accept rebuild/digest drift, or invoke publication. `N045-CL04`: scope/digest/absence evidence is incomplete or from a different candidate; judge must reject it. | `active-closure-gates`; T158/T159 full judges passed a disposable clean implementation snapshot, while T164/T165 schema/preflight mutations reject false checks, unsafe scope, remaining environments, candidate drift, tap mutation, and observed publication. Final accepted receipts remain T171 work. |

## Requirement coverage

The matrix groups related requirements into independently mutable claim
families; no requirement is silently covered only by a broad aggregate:

| Requirement range | Owning rows |
| --- | --- |
| FR-001–FR-003 | H01–H03 |
| FR-004–FR-008 | U01–U03, A07 |
| FR-009–FR-017 | A01–A03, U03–U04 |
| FR-018–FR-024 | A04–A07, RC05 |
| FR-025–FR-031 | AT01–AT03, R01 |
| FR-032–FR-036 | AT04, R01 |
| FR-037–FR-042 | AT05–AT08 |
| FR-043–FR-048 | C01–C04, CL03 |
| FR-049–FR-054 | C05, CL01–CL03, R05, A09 |
| FR-055–FR-061 | R01–R04, A10 |
| FR-062–FR-066 | A03, A08, RC01–RC05 |
| FR-067 | Every row; enforced by T148 and the final mutation manifest judge. |
| FR-068 | C06 |
| FR-069 | R04 plus T160/T168 documentation judges. |
| FR-070 | Every applicable row plus the exact-candidate orchestrator in T171. |
| FR-071 | A10 and CL04 |

Success criteria SC-001–SC-015 are reconciled separately in T169. Their new
claim mechanisms are already represented here: first-use/help (H01), HUD/UI
(U01–U05), freshness/performance (C06), concurrency/recovery (A03, RC01–RC05),
attribution (AT01–AT08), coverage/loss (C01–C04), privacy (R01–R05), cleanup
(CL01–CL03), live proxy transition (A05–A06/RC05), parity (H03/U03–U04), and
one exact candidate (CL04).

## T148 negative-fixture evidence

The reusable claim judge and all 46 semantic fixture contracts live under
`scripts/mutation/045/`. Run:

```sh
scripts/mutation/045/run-negative-fixtures.sh
```

The latest passing local run is digest-bound from
`.artifacts/045/local/mutations/judge-negative-fixtures/summary.json`. It
retains 46 private negative observations and 46 restored observations. Each
negative receipt has a current contract digest, current matrix digest,
internally valid evidence digest, and one false semantic fact; the judge
rejected it with the exact `N045-*` diagnostic, then accepted the restored
fact.

The summary explicitly records
`implementationMutationProofs.accepted=false` and `claimAcceptance=false`.
This lane therefore cannot substitute for a production mutant, real backend,
exact package, or release-candidate run.

## Known blockers and acceptance boundary

- A05, A06, and RC05 now have production Manager wiring, checkpointed
  multi-environment transitions, exact recovery proof validation, killed
  source-overlay mutants, and passing T153 real gateway/Lima evidence.
- All 46 T148 judge-negative fixtures and all 46 source-overlay production
  mutants are implemented. T152 binds their fresh logs, test events, digests,
  and restored-green results into the passing local release-candidate
  aggregate; exact clean-candidate binding remains T163.
- T154 UI/PTY/browser evidence currently binds a dirty local source tree. It
  proves the source-level journeys, not the exact clean package required by
  FR-070/SC-015.
- T155 all-sink privacy evidence currently binds a dirty local source tree. It
  proves source-built and real-backend behavior, not the exact clean package
  required by FR-070/SC-015.
- Historical T156 performance evidence binds its measured dirty local source
  tree and passed the superseded seven-sample budgets. It is diagnostic only;
  fresh thirty-sample quiet-host evidence and final exact-package binding are
  still required.
- The dirty-source real-Lima, UI, privacy, and performance aggregates pass,
  and the package/evidence/install/publication closure judges are implemented.
  Packaging, lifecycle, installed-machine, publication-absence, and exact
  clean-candidate rows remain unaccepted until the final clean run produces
  matching receipts and the collector passes with `--require-closure`.

No row in this file authorizes a remote tag, GitHub Release, Homebrew change,
or package publication.
