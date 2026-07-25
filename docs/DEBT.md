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
| Gate 3's privacy forward proof fails on current HEAD: guest DNS/HTTPS through the TUN never completes (`curl: (6) Could not resolve host`, and a direct DoH probe gets `Recv failure: Connection reset by peer`). Localized 2026-07-24 with hard evidence, not inference: the SOCKS5 fixture now traces every accept (instrumentation verified against a live fixture), and its log is **empty for the whole run** — no guest connection ever reaches the host proxy. The earlier lanes all pass, so the mediation configuration itself is right: `proxy_env_absent=yes`, `dns_mediated=yes`, and the reverse proof confirms every captured connected-subnet resolver is unreachable. The guest side also looks correct: `hideout0` is up, default routes onto it, and the local-bypass route (`192.168.5.2 dev eth0`) keeps host traffic off the TUN. That leaves the environment's authenticated host-loopback egress gateway — the hop between the guest and the loopback-bound proxy — as the unexplained segment; the environment's `runtime/services/network` directory was empty after the run. Reproduces in both default and `HIDEOUT_GATE3_RUNTIME_MODE=1` (the release path), so it is not dev-mode-only scaffolding, and it is not caused by the 2026-07-24 gate changes: it reproduces with the resolver capture in its own dedicated environment. Suspect `8869382` (lazy bootstrap materialization on the reuse/gateway path); unproved. Diagnosability gap to close alongside it: the guest's `dns-stub.log` and `tun2socks.log` are unreadable by the target (0700 session directory) and are deleted with the session, so the privacy path's own diagnostics are invisible both during and after a failed run. | Before 043 alias privacy is promoted, and before any clean Gate 3 is attempted for the release candidate — a matching clean Gate 3 cannot pass while this fails | 2026-07-24 Gate 3 preflight, default and runtime modes |
| Gate scripts can report success after crashing: the macOS system shell is bash 3.2, which delivers exit status 0 for a `set -u` unbound-variable death when an EXIT trap is installed (the trap itself observes `$? = 0`; explicit exit, `set -e` command failure, and normal exit all propagate correctly). A crashed gate therefore looks green to its caller. `gate_require_completion` in `scripts/lib/gate-result.sh` is the fail-closed guard — each gate sets `gate_completed=1` immediately before its final success line and calls the guard from its EXIT trap — and is wired into gate2, gate3, and gate4. About 36 other `scripts/test-*.sh` share the same shape (`set -euo pipefail` plus an EXIT trap) and remain unguarded; each needs its own success line identified. | Before any of those scripts' green result is used as release evidence, and opportunistically when one is next edited | 2026-07-24 Gate 3 preflight: an empty-array expansion killed the run while the script exited 0 |
| Real-backend gates inherited the guest session supervisor and Workspace Portal from whatever `install-local.sh` last left on PATH instead of building them from the candidate source, so a gate could prove a stale guest binary's behavior (observed: a supervisor predating 043 rejected the `projectionReadiness` control field). gate2 and gate3 now build both helpers from `internal/helperbin/cmd/build-*`. Remaining: the other real-backend lanes (`test-*-lima-e2e.sh`, `test-workspace-portal-lima.sh`, `test-runtime-lima.sh`, dogfood) still resolve these helpers through the operator's PATH, and neither helper has a `build-linux` CLI the way the shim and hostfsd do. | Before those lanes' results are used as candidate evidence; a `build-linux` subcommand would close the class | 2026-07-24 Gate 3 preflight helper-drift failure |
| --- | --- | --- |
| tun2socks helper is not packaged; privacy mode requires operator-supplied binary | Before privacy mode is recommended as a default path to real users | 013 leftover; 011-016 review |
| Runtime image CVE rebuild cadence and a "runtime is N days old" signal | Before `developer-standard` drops its preview label / any supported claim | 031 review |
| Multi-revision runtime image disk sprawl governance (003 promised list/idle-stop/clean story) | When operators accumulate multiple runtime revisions or report disk pressure | 003 research; 031 review |
| GA self-built image license/redistribution review (SBOM deferred to GA) | Before GA distribution of self-built runtime images | 031/033 reviews |
| `minimumLimaVersion` field has no owner | First Lima release that breaks a supported path | 031 review |
| Homebrew tap parity is CI-enforced against the release inventory, but the tap's teaching surface (caveats, helper set) has no in-tree comparison baseline. The first real CI comparison proved HEAD-vs-tap text parity is the wrong invariant: the published tap legitimately trails the source formula between releases (the source formula itself only entered the tree after `v0.1.0-alpha.1` shipped, with the local tap hand-edited to satisfy the old check). The enforced anchor is now the release inventory — the official tap must distribute exactly `releases/current.json`'s tag and artifact digest — with `HIDEOUT_TAP_FORMULA`/sibling resolution, explicit local not-run, and `HIDEOUT_REQUIRE_TAP_PARITY=1` failing closed; teaching-surface checks run against the source formula alone. Remaining: the next publication should retain the rendered release-time formula in-tree (or record the tap commit in the release inventory) so caveats/helper parity against the published tap becomes provable rather than skipped. | Next release publication (restore full teaching-surface parity with a release-time baseline); entry survives the first green CI run for that remainder | 038 acceptance; 2026-07-24 first real CI comparison and the release-anchor redesign |
| static/dedicated virtiofs workspace execution: feature 041 fixes the default shared Workspace Portal's rejected Linux `FMODE_EXEC` hint, but named/dedicated Lima environments still use a static `vz` + `virtiofs` workspace whose direct `execve` behavior remains unpromoted; no hidden copy or host fallback is allowed | Before workspace-local execution is claimed for named/dedicated environments or all Lima modes | 2026-07-22 041 research: Portal trace isolated `OPEN {EXEC,0x20000}` rejection and the narrow Portal fix passed script/binary probes; retained 2026-07-20 static-virtiofs observation remains EOPNOTSUPP |
| Historical pre-042 disposable residue migration UX: 042 now automatically converges exact authorized `--rm` records/intents, including daemon crashes, journal cleanup, and `--rm --ephemeral`, but journal-only residue created before the bounded disposal intent existed remains deliberately blocked because its destructive authority cannot be proved. Provide an explicit typed inspect/migrate/clean workflow if real installations report this legacy shape; do not infer authority or add a generic orphan sweep. | First real report of pre-042 journal-only disposable residue, or before claiming hands-off cleanup for installations upgraded from a pre-042 build | 042 clean exact-package real Gate 2 at `666cfa827646bbc6b0d3d9a86b4f5091b83b5dd3`; product manifest SHA-256 `fc1cabf8eb645433145b72ade45175821c3097792900729c1e9c9231e3dccd16`; historical journal-only refusal remains an explicit non-claim |

## Product surface

| Deferred item | Trigger (when it becomes due) | Source |
| --- | --- | --- |
| Guest-to-host projection (adb, DevTools): separate design line with its own threat model | Flagship projection slice after host-to-guest story is told | opentarget-architecture; 2026-07-10 positioning |
| portbridge guest-reachable listener unimplemented (lab-only) | Same as guest-to-host projection | portbridge.go |
| `hideout hostfs migrate-list` exists but is documented nowhere user-facing | First external user hitting a legacy list-rule profile | 029 acceptance minor |
| UI E2E lanes (TUI/browser/HostFS decision) are env-var gated and never run in the default gate | Before any UI behavior claim is made externally | 2026-07-11 verification |
| HostFS per-op RPC performance ceiling for metadata-heavy workloads | Real-user reports of slow metadata operations on large repos | privacy-run-design |
| 011-016 low-priority leftovers: two weak assertions in 011; human-channel redaction symmetry in 015 | Opportunistic; bundle lifecycle or decision-center rework touches those files | 011-016 acceptance |
| One guest write stages two `hostfs.write` decisions (per-op granularity: create + write); operator-facing count reads noisy | Decision-center UX iteration | 2026-07-20 first-run walkthrough |
| Repeated write to a path with an undecided pending decision surfaces as a bare guest `EIO` with no typed explanation | HostFS write-overlay UX iteration | 2026-07-20 first-run walkthrough |
