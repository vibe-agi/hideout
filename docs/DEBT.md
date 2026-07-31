# Deferred Debt Ledger

<!-- markdownlint-disable MD013 -->

This ledger is the single place where intentionally deferred work with a real
trigger condition is recorded. It exists so that a deferral decided inside one
spec review does not silently disappear when that spec closes.

Rules:

- Every feature's `speckit` converge/analyze step MUST check that new deferred
  items from that slice are recorded here before the slice is marked done.
- Each entry names the condition that makes it due. When the trigger becomes
  true, the entry blocks the triggering work until resolved or explicitly
  re-deferred with a new trigger.
- Entries are removed only by a commit that implements them or a recorded
  decision that retires them. Do not delete entries silently.
- Non-claims (honest statements of what Hideout does not do) are not debt and
  do not belong here; they live in the threat model.

## Security and authority

| Deferred item | Trigger (when it becomes due) | Source |
| --- | --- | --- |
| G1 code enforcement: non-operator-authored grant proposals must pass trust review; today G1 is a planning gate only, not enforced in code | Before any public bundle/recipe ecosystem accepts third-party content | constitution G1; spec 001 review |
| Guest host-key pinning for credential-bearing callback reuse (OAuth callback adapter) | Before any credential-bearing OAuth/callback flow crosses the host-to-guest bridge | threat-model hard gate; 031 review |
| `sanitizeHostFSReadReason` is wired defensively but `UntrustedReason` is never populated; sanitizer must be re-verified when the reason channel is actually wired | Before broker envelope carries untrusted reason text to any UI | 029 acceptance finding ② |
| Per-project HostFS grant carrier: `hostfs.Rule` has no workspace/project binding; `allow/deny --for-this-project`, `--once`, and default-ask semantics need a design ruling | Before per-project grants are promised in docs or the shared-default-VM trust story depends on them | 2026-07-19 review (this ledger's creating slice) |
| Marketplace day-1 triad: signing, revocation, namespace protection | Marketplace/public pack registry launch day | policy-config-supply-chain Trust Model |
| Tier2 imperative environment recipes remain forbidden pending an independent trust design | Before ecosystem-shared imperative environment builds are accepted | ecosystem principles |

## Distribution and runtime

| Deferred item | Trigger (when it becomes due) | Source |
| --- | --- | --- |
| Gate scripts can report success after crashing: the macOS system shell is bash 3.2, which delivers exit status 0 for a `set -u` unbound-variable death when an EXIT trap is installed (the trap itself observes `$? = 0`; explicit exit, `set -e` command failure, and normal exit all propagate correctly). `gate_require_completion` in `scripts/lib/gate-result.sh` is the fail-closed guard. The complete 045 candidate/evidence/package/install path now has an independently self-tested guard at every directly used EXIT-trap boundary, and `release-candidate.sh` rejects missing wiring. Forty-five older shell scripts or sourced lane helpers still have the hazardous shape without this shared guard. | Before any remaining script's green result is used as release evidence, and opportunistically when one is next edited | 2026-07-24 Gate 3 preflight failure; 045 closure audit on 2026-07-31 |
| Real-backend gates inherited guest helpers from whatever `install-local.sh` last left on PATH, so a gate could prove a stale guest binary's behavior (observed: a supervisor predating 043 rejected the `projectionReadiness` control field). All real-backend lanes required by the 045 candidate now build every helper they use from `internal/helperbin/cmd/build-*`. Remaining legacy lanes (`test-*-lima-e2e.sh`, `test-workspace-portal-lima.sh`, `test-runtime-lima.sh`, dogfood) can still resolve helpers through the operator's PATH, and the session supervisor and Workspace Portal do not expose a `build-linux` CLI like the shim and hostfsd. | Before one of those legacy lanes is used as candidate evidence; a shared source-bound helper builder or `build-linux` subcommands would close the class | 2026-07-24 Gate 3 preflight helper-drift failure; 045 real-lane audit on 2026-07-31 |
| Runtime image CVE rebuild cadence and a "runtime is N days old" signal | Before `developer-standard` drops its preview label / any supported claim | 031 review |
| Multi-revision runtime image disk sprawl governance (003 promised list/idle-stop/clean story) | When operators accumulate multiple runtime revisions or report disk pressure | 003 research; 031 review |
| GA self-built image license/redistribution review (SBOM deferred to GA) | Before GA distribution of self-built runtime images | 031/033 reviews |
| `minimumLimaVersion` field has no owner | First Lima release that breaks a supported path | 031 review |
| static/dedicated virtiofs workspace execution: feature 041 fixes the default shared Workspace Portal's rejected Linux `FMODE_EXEC` hint, but named/dedicated Lima environments still use a static `vz` + `virtiofs` workspace whose direct `execve` behavior remains unpromoted; no hidden copy or host fallback is allowed | Before workspace-local execution is claimed for named/dedicated environments or all Lima modes | 2026-07-22 041 research: Portal trace isolated `OPEN {EXEC,0x20000}` rejection and the narrow Portal fix passed script/binary probes; retained 2026-07-20 static-virtiofs observation remains EOPNOTSUPP |
| Historical pre-042 disposable residue migration UX: 042 now automatically converges exact authorized `--rm` records/intents, including daemon crashes, journal cleanup, and `--rm --ephemeral`, but journal-only residue created before the bounded disposal intent existed remains deliberately blocked because its destructive authority cannot be proved. Provide an explicit typed inspect/migrate/clean workflow if real installations report this legacy shape; do not infer authority or add a generic orphan sweep. | First real report of pre-042 journal-only disposable residue, or before claiming hands-off cleanup for installations upgraded from a pre-042 build | 042 clean exact-package real Gate 2 at `666cfa827646bbc6b0d3d9a86b4f5091b83b5dd3`; product manifest SHA-256 `fc1cabf8eb645433145b72ade45175821c3097792900729c1e9c9231e3dccd16`; historical journal-only refusal remains an explicit non-claim |

## Product surface

| Deferred item | Trigger (when it becomes due) | Source |
| --- | --- | --- |
| Guest-to-host projection (adb, DevTools): separate design line with its own threat model | Flagship projection slice after host-to-guest story is told | opentarget-architecture; 2026-07-10 positioning |
| portbridge guest-reachable listener unimplemented (lab-only) | Same as guest-to-host projection | portbridge.go |
| `hideout hostfs migrate-list` exists but is documented nowhere user-facing | First external user hitting a legacy list-rule profile | 029 acceptance minor |
| HostFS per-op RPC performance ceiling for metadata-heavy workloads | Real-user reports of slow metadata operations on large repos | privacy-run-design |
| 011-016 low-priority leftovers: two weak assertions in 011; human-channel redaction symmetry in 015 | Opportunistic; bundle lifecycle or decision-center rework touches those files | 011-016 acceptance |
| One guest write stages two `hostfs.write` decisions (per-op granularity: create + write); operator-facing count reads noisy | Decision-center UX iteration | 2026-07-20 first-run walkthrough |
| Repeated write to a path with an undecided pending decision surfaces as a bare guest `EIO` with no typed explanation | HostFS write-overlay UX iteration | 2026-07-20 first-run walkthrough |

## Feature 045 follow-on work

These items are not part of the current product claim. The owner, user risk,
trigger, and present non-claim are explicit so a future feature cannot silently
promote an observation into a protection promise.

| Deferred item | Owner | User risk while deferred | Trigger (when it becomes due) | Current non-claim |
| --- | --- | --- | --- | --- |
| Optional policy that prevents an action because an explainable activity-risk rule matched | Policy and Manager | A user could mistake a detective risk finding for a firewall or execution block | Before any UI, documentation, or package claims that activity risks prevent commands, file actions, or network access | Risk findings explain observed behavior and policy status; they do not block it |
| Tamper-resistant observation against a workload with effective guest-root control | Runtime isolation and workload observer | Guest root can stop or confuse collectors, reducing confidence if the reduction is ignored | Before supporting hostile guest root or claiming complete observation despite guest-root tampering | Guest-root tampering is outside the trusted observation boundary and must reduce coverage to Partial or Unavailable |

## Resolved ledger decisions

- 2026-07-31: the active “UI E2E lanes are optional” debt is retired for the
  Feature 045 UI claims. `scripts/gates/release-candidate-ui.sh` is a required
  exact-candidate lane for TUI, browser, accessibility, injection, stale-state,
  and recovery behavior; the final evidence collector rejects a missing,
  reduced, or non-passing receipt. This does not make unrelated legacy UI
  smoke scripts release evidence.
