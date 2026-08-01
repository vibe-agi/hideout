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
| Documentation/help truth | `go test ./internal/app -run 'Test(PrivacyAndSecretHelpExplainStartupFallbackMigration\|EnglishAndChineseUserGuidesCoverSupportedJourney\|HelpGoldens)' -count=1 && go test ./internal/releasecompat -count=1 && markdownlint-cli2 docs/README.md docs/STATUS.md docs/privacy-run-design.md docs/threat-model.md docs/privacy-run-test-plan.md docs/formal-models.md docs/support-matrix.md docs/recovery.md docs/activity-observation.md docs/user-guide.md docs/user-guide.zh-CN.md docs/release/045-readiness.md && scripts/test-doc-truth-smoke.sh` | Process exit and doc-truth evidence | Active (T160): current product status, design, threat, test, model, privacy, retention, recovery, help, Keychain migration, and honest process/file/network/DNS coverage are synchronized; T168 terminology/privacy audit and exact T171 candidate binding remain required |
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
| Performance/quota | `HIDEOUT_PERFORMANCE_QUIET_HOST_CONFIRMED=1 scripts/gates/release-candidate-performance.sh` | `.artifacts/045/performance/run-20260801T070125Z-30370/summary.json` (`sha256:9772762738aac4befe5bedbbfe75b9db7f4970e2d250819651e90177656b12a7`) | Active and passing for clean source commit `504514bbbf1feb1a00e05a1f5f6f52e78b5ed881` (T156): thirty real-Lima samples plus two warmups produced 5.683% paired median overhead and a 6.44% one-sided 95% upper bound, both within 10%; all attach, observer CPU/RSS, drop, quota, query/render, daemon/TUI, and browser budgets passed. Three initial snapshots and 770 continuous samples found no sustained external contention. The receipt binds a stable 1,754-file source manifest, 545 private artifacts, and `candidateAcceptance=true`; exact final-package rebinding remains T171 after closure-document commits. |
| Package build | `scripts/release/build-candidate.sh` | `.artifacts/045/package/` | Active (T158): fail-closed clean-tree gate, two independent Go caches, byte-identical archive/manifest/tree proof, exact manifest-derived package inventory, all 9 Go binaries and 6 helper manifests, every repository schema, 8 embedded browser assets, runtime catalog/contract/artifact binding, and final-binary advisory scans. The exact current counts are taken only from the final clean candidate receipt. |
| Install/upgrade/uninstall/reinstall | `scripts/release/test-package-lifecycle.sh` | `.artifacts/045/package-lifecycle/` | Active (T159): consumes the exact T158 archive without rebuilding; verifies the checked-in immutable `v0.1.0-alpha.3` receipt/download, clean install, macOS Keychain and legacy-export guidance, same-candidate reinstall, exact temporary legacy-store discard, old-version upgrade, normal uninstall absence, durable-state/unrelated-file preservation, source stability, private evidence modes/digests, and local-only status. A disposable exact-clean implementation validation passed all 11 lifecycle checks with 23 retained artifacts; accepted main-candidate evidence remains intentionally absent until T163/T171. |
| Evidence binding | `scripts/release/collect-evidence.sh` | `.artifacts/045/evidence.json` and `.artifacts/045/evidence.json.sha256` | Active (T163 implementation): independently resolves every private pointer and digest, extracts and verifies the exact package, validates all 11 required gate identities, and emits package-bound/installed-local/final-ready stages. Final acceptance still requires one clean T171 run. |
| Installed-machine closure | `scripts/release/install-local-candidate.sh --yes-discard-legacy-data` | `.artifacts/045/local-install/` | Active (T164 implementation): consumes the accepted archive without rebuilding, constrains destructive scope to the recognized install and exact current-user store, exercises setup/secret/connect/proxied run/Help/TUI/WebUI/clean/update/uninstall/reinstall, scans retained state for transient secrets, and leaves the exact candidate installed with a fresh direct profile, no environment, and a stopped daemon. Final acceptance requires the exact clean candidate run. |
| Publication absence | `scripts/release/verify-publication-absence.sh` | `.artifacts/045/publication-absence/` | Active (T165 implementation): read-only double observations require tag absence, an exact GitHub Release 404, stable remote formula bytes without candidate material, and an unchanged clean local tap. This gate has no publication authority and its point-in-time receipt must match the exact candidate archive. |
| Final closure | `scripts/release/collect-evidence.sh --require-closure` | `.artifacts/045/evidence.json`, detached digest, and both closure receipts | Active: exits zero only when source, candidate, installed binary, all gates, local-install receipt, and publication-absence receipt are fresh, exact, private, schema-valid, digest-valid, and passing. |

## Local candidate sequence

The final release-candidate run uses this exact order:

```sh
scripts/gates/release-candidate.sh
scripts/gates/formal.sh
scripts/gates/release-candidate-privacy.sh
scripts/gates/release-candidate-ui.sh
HIDEOUT_PERFORMANCE_QUIET_HOST_CONFIRMED=1 \
  scripts/gates/release-candidate-performance.sh
scripts/gates/release-candidate-lima.sh
scripts/release/build-candidate.sh
scripts/release/test-package-lifecycle.sh
scripts/release/collect-evidence.sh
scripts/release/install-local-candidate.sh --yes-discard-legacy-data
scripts/release/verify-publication-absence.sh
scripts/release/collect-evidence.sh --require-closure
```

The package is built only after source-level gates pass. Package lifecycle
tests and installed-machine closure consume that exact artifact; they do not
rebuild it. Evidence collection rejects a commit, package digest, helper
digest, runtime digest, closure schema, closure artifact, or timestamp that
differs from the preceding lanes. The first collection records the
package-bound stage; only the final `--require-closure` collection may record
`final-ready`.

## Host-specific prerequisites

- Gate 0 and local lanes require Go `1.25.12`.
- Generated BPF checks require LLVM/Clang `19.1.7` and cilium/ebpf `bpf2go`
  `v0.22.0`; the latter is built from the pinned Go module.
- TLC uses the repository-pinned `tla2tools` version and digest.
- Terminal journeys require `expect` and a real PTY.
- Browser journeys require the pinned browser-test runtime introduced by their
  owning task.
- Keychain and full candidate gates require macOS arm64.
- Installed-machine closure requires the active Homebrew prefix, an unlocked
  user Keychain, `expect`, `curl`, Python 3, and explicit authorization to
  discard the exact current-user `~/.hideout` store.
- Publication-absence verification requires authenticated read access through
  `gh`, the configured source remote, and a clean local `vibe-agi/tap` checkout.
- Real-Lima lanes require the supported Debian 13 runtime, cgroup v2, and an
  otherwise unrelated-VM-safe fixture environment.
- Performance evidence requires a quiet host: pause unrelated CPU-heavy tests,
  VMs, and emulators before the thirty recorded samples and keep them paused
  until the gate completes. Known contention invalidates the run. The full gate
  fails before measurement unless the operator explicitly sets
  `HIDEOUT_PERFORMANCE_QUIET_HOST_CONFIRMED=1`. It then takes three one-second
  process-name/CPU snapshots and rejects sustained generic, virtualization, or
  build/test contention before building; it records no argv/environment and
  never stops a process. A one-second monitor remains active throughout the
  real-Lima attach/reference interval and rejects two threshold hits by the
  same PID/name in a rolling three-sample window. It excludes only the gate
  process group and Hideout/Lima virtualization processes proven against the
  gate's private runtime, retains no argv/environment/path, and is independently
  reparsed by final collection. A violation terminates only the isolated
  PID-equals-PGID measurement child; its early-installed EXIT cleanup removes
  exact Gate 2 scratch and external processes are never signaled. Private
  host-state snapshots make the run auditable, and a median-only statistical
  pass is insufficient without the one-sided 95% confidence bound.

## Publication boundary

None of these commands may create or push a Git tag, create a GitHub Release,
commit or push a Homebrew formula, or publish a package. Those actions remain
outside this feature and require a separate explicit operator instruction
after the local readiness result.
