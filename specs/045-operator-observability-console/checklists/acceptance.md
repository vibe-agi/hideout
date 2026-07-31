# Feature 045 acceptance reconciliation

<!-- markdownlint-disable MD013 -->

This is the requirement-to-implementation reconciliation for
`045-operator-observability-console`. It is intentionally commit-independent:
the implementation map is tracked source, while acceptance of any candidate
comes only from a fresh, exact `.artifacts/045/evidence.json` whose
`stage` is `final-ready`, whose `releaseReadiness` is `true`, and whose source,
package, installed binary, gate, local-install, and publication-absence
identities agree.

The specification has 71 numbered functional requirements plus the constrained
`FR-035a` sub-requirement, and 15 success criteria. Every identifier appears
exactly once below. A claim-row reference means the direct production test,
source mutation, negative evidence fixture, and restored-green result recorded
for that row in [the claim matrix](../../../docs/release/045-claim-matrix.md).
The named candidate gate is additional evidence, not a substitute for that
direct proof.

## Functional requirements

| Requirement | Owning implementation | Direct test and mutation proof | Exact-candidate evidence |
| --- | --- | --- | --- |
| FR-001 | `internal/app/help.go`, `internal/app/command_catalog.go`, `internal/operatorhelp/` | H01, H03 | UI and local-install gates |
| FR-002 | `internal/app/help.go`, `internal/app/command_catalog.go` | H01 | UI and local-install gates |
| FR-003 | `internal/app/guidance.go`, `internal/app/command_catalog.go` | H02, H03 | Local and UI gates |
| FR-004 | `internal/app/tui.go`, `internal/tui/`, `internal/liveconsole/` | U01, U05 | UI and local-install gates |
| FR-005 | `internal/tui/render/`, `internal/manager/operator_snapshot.go` | U01 | UI and performance gates |
| FR-006 | `internal/daemon/uiweb_assets/`, `internal/manager/activity_api.go` | U02, U05 | UI/browser and local-install gates |
| FR-007 | `internal/manager/profile_projection.go`, shared console projections | U03 | UI/browser gate |
| FR-008 | `internal/liveconsole/`, TUI/WebUI reducers | A07, U03 | UI/browser and formal gates |
| FR-009 | `internal/manager/profile_transaction.go`, CLI/TUI/WebUI transaction clients | A01, U04 | UI/browser and formal gates |
| FR-010 | CLI/TUI/WebUI draft models and `internal/manager/profile_transaction.go` | A01 | UI/browser gate |
| FR-011 | `internal/manager/profile_transaction_builder.go`, review renderers | A01, U04 | UI/browser gate |
| FR-012 | `internal/manager/profile_transaction.go`, operation store | A02, U04 | Local, UI, and formal gates |
| FR-013 | Profile revision/CAS validation and transaction apply | A02, U04 | Local, UI, and formal gates |
| FR-014 | `internal/manager/operation*.go`, transaction idempotency | A03, U04 | Local, UI, and formal gates |
| FR-015 | Profile mutation keys, locks, and operation ownership | A03, U04 | Race, UI, and formal gates |
| FR-016 | Profile projection and connection status renderers | U03 | UI and local-install gates |
| FR-017 | Session network snapshots and profile projection | U03 | UI and real-Lima gates |
| FR-018 | `internal/secrets/`, daemon/Manager secret services | A04 | Privacy, Keychain, and local-install gates |
| FR-019 | `internal/app/secret.go`, authenticated secret API/service | A04 | Privacy and local-install gates |
| FR-020 | Manager profile/live network transition state machines | A05 | Network-rotation and formal gates |
| FR-021 | Network transition eligibility and drain logic | A06 | Network-rotation and local-install gates |
| FR-022 | Gateway generation binding and accepted-connection drain logic | A06 | Network-rotation and local-install gates |
| FR-023 | Network transition rollback/recovery stores | A05, RC05 | Recovery, network-rotation, and formal gates |
| FR-024 | Live-console freshness and credential-epoch reducers | A07 | UI/browser and formal gates |
| FR-025 | Session supervisor cgroup scope and process collector | AT01 | Real-Lima observation gate |
| FR-026 | Execution identity and cgroup membership tracking | AT01, AT02 | Real-Lima observation gate |
| FR-027 | Cgroup-scoped process/file/network collectors | AT01 | Real-Lima concurrent-isolation gate |
| FR-028 | DNS/proxy mediator attribution models | AT03 | Real-Lima observation/privacy gates |
| FR-029 | `internal/workloadobs/types/` execution model and process collector | AT02 | Real-Lima observation gate |
| FR-030 | Process relay schema and pre-persistence redaction | R01 | Privacy and real-Lima canary gates |
| FR-031 | Execution lifecycle and observer-loss coverage handling | AT02 | Real-Lima crash/loss gate |
| FR-032 | File collector, fanotify/BPF records, and file operation types | AT04 | Real-Lima observation gate |
| FR-033 | File aggregation and activity record model | AT04 | Real-Lima observation/performance gates |
| FR-034 | File metadata-only collector and redaction boundary | R01 | Privacy and real-Lima canary gates |
| FR-035 | File aggregation windows and destructive-operation preservation | AT04 | Real-Lima observation/performance gates |
| FR-035a | File provider filter and explicit partial-coverage reason | AT04 | Real-Lima observation gate |
| FR-036 | File path resolution state/class and ambiguity labels | AT04 | Real-Lima observation gate |
| FR-037 | Network collector and attributed connection records | AT05 | Real-Lima observation/privacy gates |
| FR-038 | DNS parser/proxy and name-resolution records | AT06 | Real-Lima observation/privacy gates |
| FR-039 | Domain-correlation confidence model | AT07 | Real-Lima observation gate |
| FR-040 | DNS/network uncertainty and degraded confidence rules | AT07 | Real-Lima loss/privacy gate |
| FR-041 | `internal/workloadobs/risk/` named rule results | AT08 | Local, UI, and real-Lima gates |
| FR-042 | Risk observation/policy/prevention separation | AT08 | Local and UI/browser gates |
| FR-043 | `internal/workloadobs/coverage/` independent intervals | C01 | Local, UI, and real-Lima gates |
| FR-044 | Coverage interval reason/generation/loss/gap fields | C01 | Local, UI, and real-Lima gates |
| FR-045 | Collector/store/stream downgrade paths | C02, CL03 | Privacy, recovery, and real-Lima gates |
| FR-046 | Console reduced-coverage rendering | C03 | UI/browser gate |
| FR-047 | Daemon stream sequence and client deduplication | C04 | UI/browser and recovery gates |
| FR-048 | Authoritative seed/reseed reducers | C04 | UI/browser and formal gates |
| FR-049 | Exact owner/incarnation activity store layout | C05 | Privacy and real-Lima gates |
| FR-050 | Disposable owner lifecycle cleanup | C05, CL01 | Privacy and real-Lima cleanup gates |
| FR-051 | Reusable incarnation cleanup plan/apply | C05, CL02 | Privacy and real-Lima cleanup gates |
| FR-052 | Store retention, quota, pruning, and gap records | C05, CL03 | Privacy and performance gates |
| FR-053 | Activity store path/mode/link defenses | R05 | Privacy and package gates |
| FR-054 | Authenticated CLI/TUI/loopback activity APIs | A09, R05, U05 | Privacy, UI/browser, and local-install gates |
| FR-055 | Local path-preserving activity projections | R03 | Privacy and UI/browser gates |
| FR-056 | `internal/workloadobs/redact/` and secret-generation snapshots | R02 | Privacy and local-install gates |
| FR-057 | Encoded/split-field redaction rules | R02 | Privacy mutation/canary gate |
| FR-058 | Activity UI/help and privacy documentation disclosures | R04 | Documentation and UI gates |
| FR-059 | Separate audit/activity retention and fidelity contracts | R04 | Documentation and privacy gates |
| FR-060 | `internal/manager/activity_export.go`, reviewed export authority | A10 | Privacy and local gates |
| FR-061 | Export path policy and authenticated local-path projection | A10, R03 | Privacy and local gates |
| FR-062 | Durable operation store and daemon recovery | RC01, RC03 | Recovery and formal gates |
| FR-063 | Effect evidence and terminal proof validators | RC02, U04 | Local, UI, and formal gates |
| FR-064 | Decision service leases and disconnect release | A08 | Race, UI, and formal gates |
| FR-065 | Profile/lifecycle ownership and blocker projections | A03, RC03 | Race, recovery, and formal gates |
| FR-066 | Recovery state machines and TLA+/Go refinement traces | RC03, RC04, RC05 | Formal, recovery, and real-Lima gates |
| FR-067 | `scripts/mutation/045/` production mutants and negative fixtures | Every claim row | Local mutation aggregate |
| FR-068 | Frozen performance defaults and measurement gates | C06 | Performance and real-Lima gates |
| FR-069 | Status, design, threat, privacy, recovery, support, and user docs | R04 | Documentation/help truth and static gates |
| FR-070 | Candidate, package lifecycle, evidence, install, and closure scripts | CL04 | Final `--require-closure` evidence |
| FR-071 | Publication boundary plus read-only absence verifier | A10, CL04 | Publication-absence and final closure gates |

## Success criteria

| Criterion | Owning claims and measurement | Exact-candidate evidence |
| --- | --- | --- |
| SC-001 | H01/H03; every ordinary task is found within two help invocations | UI and installed-candidate help journey |
| SC-002 | U01/U05; one-screen HUD and bounded keyboard navigation | Real-PTY UI and installed-candidate TUI |
| SC-003 | C06; independently recomputed terminal/browser freshness p95 | Performance gate |
| SC-004 | A02/A03/U04/RC03/RC04; stale/concurrent/retry scenarios | UI, race, recovery, and formal gates |
| SC-005 | AT01–AT07/C01/C02; exact reference-workload expectations | Real-Lima observation/privacy gates |
| SC-006 | AT01/AT02; concurrent and PID-reuse isolation | Real-Lima observation gate |
| SC-007 | C02–C04/CL03; every injected loss changes coverage first | UI, privacy, and real-Lima loss gates |
| SC-008 | R02; zero raw canary matches in every retained sink | Privacy and installed-candidate scans |
| SC-009 | CL02; exact old-incarnation absence/new-incarnation preservation | Real-Lima cleanup gate |
| SC-010 | C05/CL03; quota bound and observable ordered pruning | Performance and privacy gates |
| SC-011 | C06; paired median overhead and loss/resource budgets | Performance gate |
| SC-012 | RC04; all bounded TLA+/Go safety and progress scenarios | Formal gate |
| SC-013 | A06/RC05; live proxy/secret rotation with stable daemon/VM identity | Network-rotation and local-install gates |
| SC-014 | H03/U03/U04; shared authoritative surface parity | UI/browser gate |
| SC-015 | Every claim plus CL04; no stale/reduced/not-run required result | Final `collect-evidence.sh --require-closure` |

## Terminology, privacy, and false-success audit

The release audit covers all feature-added operator-visible strings and
retained evidence under `cmd/`, `internal/`, and `docs/`. Its required
disposition is:

- ordinary help and primary HUD labels use user concepts; implementation terms
  such as cgroup, BPF, fanotify, epoch, CAS, and projection appear only in
  advanced diagnostics, coverage reasons, contracts, or maintainer evidence;
- managed values, URI userinfo, authentication fields, sensitive arguments and
  queries, and UI/control tokens are removed before persistence; process
  listings, logs, stores, UI output, exports, and release evidence are scanned;
- untrusted terminal/browser text is rendered through control-safe plain-text
  paths, never as executable terminal controls or HTML;
- `passed`, `ready`, `effective`, and `complete` require their authoritative
  terminal evidence; staging, a backend return, a timer, or an empty activity
  query cannot independently produce a success claim; and
- the final code-review ledger has no open required finding, while compatibility
  boundaries and unsupported coverage remain explicit non-claims.

The direct regression owners are H01–H03, U01–U05, A04/A07/A09, R01–R05,
C01–C04, RC01–RC05, and CL04. The exact all-sink and user-surface observations
are produced by the privacy, UI, local-install, and final-closure gates.

## Acceptance rule

This reconciliation is complete when its identifier-set check, Go/static/
shell/Markdown/schema/generated checks, and all direct claim proofs pass. A
release candidate is accepted only when the final evidence manifest is
`final-ready`; a mapped row, prior dirty-tree result, or this document alone
never establishes release readiness or authorizes publication.
