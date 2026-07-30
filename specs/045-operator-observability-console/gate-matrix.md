# Feature 045 gate matrix

<!-- markdownlint-disable MD013 -->

This file fixes the command spelling and evidence owner for every release
claim. Commands run from the repository root against one exact candidate
commit. A command is passing only when it exits zero, writes fresh evidence
for that commit where required, and contains no required `not-run`, `stale`,
`reduced`, or `unsupported` result.

`Planned` means the command spelling is reserved but its owning implementation
task has not completed. A planned lane is a release blocker, not an optional
or implicitly passing check.

## Required lanes

| Lane | Exact command | Evidence | Activation |
| --- | --- | --- | --- |
| Setup/generated | `HIDEOUT_BPF_CLANG=/opt/homebrew/opt/llvm@19/bin/clang HIDEOUT_BPF_LLVM_STRIP=/opt/homebrew/opt/llvm@19/bin/llvm-strip scripts/gates/generated.sh` | Process exit and generated-byte comparison | Active |
| Gate 0 | `scripts/test-gate0.sh` | Process exit and existing Gate 0 outputs | Active |
| Documentation/help truth | `go test ./internal/app -run 'Test(PrivacyAndSecretHelpExplainStartupFallbackMigration\|EnglishAndChineseUserGuidesCoverSupportedJourney\|HelpGoldens)' -count=1 && go test ./internal/releasecompat -count=1 && markdownlint-cli2 docs/README.md docs/STATUS.md docs/privacy-run-design.md docs/threat-model.md docs/privacy-run-test-plan.md docs/formal-models.md docs/support-matrix.md docs/recovery.md docs/activity-observation.md docs/user-guide.md docs/user-guide.zh-CN.md && scripts/test-doc-truth-smoke.sh` | Process exit and doc-truth evidence | Active (T160): current product status, design, threat, test, model, privacy, retention, recovery, help, Keychain migration, and honest process/file/network/DNS coverage are synchronized; T168 terminology/privacy audit and exact T171 candidate binding remain required |
| Models/refinement | `scripts/gates/formal.sh` | `.artifacts/045/formal/` | Active local bounded-model gate; exact candidate binding remains T163 |
| Privacy/redaction | `scripts/gates/release-candidate-privacy.sh` | `.artifacts/045/privacy/`; current proof: `.artifacts/045/privacy-current-2/result.json` and `.artifacts/045/privacy-current-2/run-20260730T174215Z-43994/summary.json` | Active and passing for the current dirty source tree (T155): 73/73 exact tests, real Keychain, fresh CLI/TUI/WebUI, real Lima, 8/8 claim receipts, and zero canary hits across all 9 required sinks; exact installed-candidate binding remains T164/T171 |
| Network/secret transition | `go test -race ./internal/network ./internal/secrets ./internal/manager && scripts/gates/keychain-real.sh && HIDEOUT_GATE3_RUNTIME_MODE=1 scripts/test-gate3-hidden-proxy.sh` | Test output, Keychain canary result, Gate 3 evidence | Active and passing in T152/T153/T155: exact secret generations, real Keychain behavior, stage/probe/activate/prove/rollback, response-loss replay, and online proxy rotation are covered; exact clean-candidate binding remains T163/T171 |
| Terminal/PTY/TUI | `scripts/gates/release-candidate-ui.sh` | `.artifacts/045/ui/`; current proof: `.artifacts/045/ui-current-2/result.json` and `.artifacts/045/ui-current-2/run-20260730T171832Z-22682/summary.json` | Active and passing for the current dirty source tree (T154): 18/18 first-use/help, 13/13 real-PTY, 40/40 shared-console tests, and all 10 exact claim receipts; exact installed-candidate binding remains T164/T167/T171 |
| Browser | `scripts/gates/browser-console.sh` | `.artifacts/045/ui/browser/`; current child proof is digest-bound by the T154 aggregate | Active and passing in the current T154 aggregate with real Chrome; exact installed-candidate binding remains T171 |
| Lifecycle/recovery | `scripts/test-lifecycle-smoke.sh && scripts/gates/recovery.sh && scripts/test-lifecycle-lima-e2e.sh` | `.artifacts/045/recovery/` and real-Lima lifecycle evidence | Active and passing in T152/T153: the crash matrix, exact-operation reconciliation, stop/clean proof, and real-Lima lifecycle lanes are covered; exact clean-candidate binding remains T163/T171 |
| Workload observation, real Lima | `scripts/gates/workload-observation-lima.sh && scripts/gates/workload-privacy-lima.sh` | `.artifacts/045/workload/` and `.artifacts/045/privacy/` | Active |
| Complete real-Lima candidate | `scripts/gates/release-candidate-lima.sh` | `.artifacts/045/lima-current/result.json` and `.artifacts/045/lima-current/run-20260730T165516Z-92214/summary.json` | Active and passing for the current dirty source tree (T153); exact clean-candidate binding remains T163 |
| Dependency/license/advisory | `scripts/gates/dependencies.sh` | `.artifacts/045/dependencies/` | Active |
| Package component inventory | `scripts/gates/package-components.sh` | `.artifacts/045/package-components/` | Active |
| Mutation proofs and local static/race gates | `scripts/mutation/045/run-negative-fixtures.sh` then `scripts/gates/release-candidate.sh` | `.artifacts/045/local/` | Active and passing locally (T152): 46 source-overlay production mutants and 46 judge-negative fixtures; exact clean-candidate binding remains T163 |
| Performance/quota | `scripts/gates/release-candidate-performance.sh` | `.artifacts/045/performance/`; current proof: `.artifacts/045/performance-current/result.json` and `.artifacts/045/performance-current/run-20260730T193602Z-54620/summary.json` | Active and passing for the measured dirty source tree (T156/T157): raw samples and independently recomputed percentiles passed query/render, daemon/TUI RSS, browser freshness, warm attach, 4.298% reference overhead, observer CPU/RSS, 186.418 exec/s, 0.085714% fully accounted loss, and one-active-segment quota bounds; v1 defaults are frozen in `internal/workloadobs/defaults.go`; exact clean-candidate binding remains T163/T171 |
| Package build | `scripts/release/build-candidate.sh` | `.artifacts/045/package/` | Active (T158): fail-closed clean-tree gate, two independent Go caches, byte-identical archive/manifest/tree proof, exact 140-file package inventory, all 9 Go binaries and 6 helper manifests, 66 schemas, 8 embedded browser assets, runtime catalog/contract/artifact binding, and final-binary advisory scans passed in a disposable exact-clean implementation validation; the current main worktree remains dirty, so accepted main-candidate evidence is intentionally absent until T163/T171 |
| Install/upgrade/uninstall/reinstall | `scripts/release/test-package-lifecycle.sh` | `.artifacts/045/package-lifecycle/` | Active (T159): consumes the exact T158 archive without rebuilding; verifies the checked-in immutable `v0.1.0-alpha.3` receipt/download, clean install, macOS Keychain and legacy-export guidance, same-candidate reinstall, exact temporary legacy-store discard, old-version upgrade, normal uninstall absence, durable-state/unrelated-file preservation, source stability, private evidence modes/digests, and local-only status. A disposable exact-clean implementation validation passed all 11 lifecycle checks with 23 retained artifacts; accepted main-candidate evidence remains intentionally absent until T163/T171. |
| Evidence binding | `scripts/release/collect-evidence.sh` | `.artifacts/045/evidence.json` | Planned: T163 |

## Local candidate sequence

The final release-candidate run uses this exact order:

```sh
scripts/gates/release-candidate.sh
scripts/gates/formal.sh
scripts/gates/release-candidate-privacy.sh
scripts/gates/release-candidate-ui.sh
scripts/gates/release-candidate-performance.sh
scripts/gates/release-candidate-lima.sh
scripts/release/build-candidate.sh
scripts/release/test-package-lifecycle.sh
scripts/release/collect-evidence.sh
```

The package is built only after source-level gates pass. Package lifecycle
tests consume that exact artifact; they do not rebuild it. Evidence collection
rejects a commit, package digest, helper digest, runtime digest, or timestamp
that differs from the preceding lanes.

## Host-specific prerequisites

- Gate 0 and local lanes require Go `1.25.12`.
- Generated BPF checks require LLVM/Clang `19.1.7` and cilium/ebpf `bpf2go`
  `v0.22.0`; the latter is built from the pinned Go module.
- TLC uses the repository-pinned `tla2tools` version and digest.
- Terminal journeys require `expect` and a real PTY.
- Browser journeys require the pinned browser-test runtime introduced by their
  owning task.
- Keychain and full candidate gates require macOS arm64.
- Real-Lima lanes require the supported Debian 13 runtime, cgroup v2, and an
  otherwise unrelated-VM-safe fixture environment.

## Publication boundary

None of these commands may create or push a Git tag, create a GitHub Release,
commit or push a Homebrew formula, or publish a package. Those actions remain
outside this feature and require a separate explicit operator instruction
after the local readiness result.
