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
| 030 gap re-verify: broker did not validate projection command-name registration (audit field spoofable, not privilege); privacy/hardened default-alias had no unit assertion; pathMode-flip drift test missing; schema-struct parity test missing | Re-verify against current code before the next projection slice; observations date to 2026-07-11 and may be partially fixed | 030 acceptance gap list |

## Distribution and runtime

| Deferred item | Trigger (when it becomes due) | Source |
| --- | --- | --- |
| tun2socks helper is not packaged; privacy mode requires operator-supplied binary | Before privacy mode is recommended as a default path to real users | 013 leftover; 011-016 review |
| Runtime image CVE rebuild cadence and a "runtime is N days old" signal | Before `developer-standard` drops its preview label / any supported claim | 031 review |
| Multi-revision runtime image disk sprawl governance (003 promised list/idle-stop/clean story) | When operators accumulate multiple runtime revisions or report disk pressure | 003 research; 031 review |
| GA self-built image license/redistribution review (SBOM deferred to GA) | Before GA distribution of self-built runtime images | 031/033 reviews |
| `minimumLimaVersion` field has no owner | First Lima release that breaks a supported path | 031 review |
| Homebrew tap parity check hardcodes an absolute local tap path (machine-specific) | Before CI runs docs-smoke or a second development machine appears | 038 acceptance |
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
| First `code .` right after fresh environment creation intermittently fails with "target command \"code\" was not found in PATH"; immediate rerun succeeds. Suspected timing gap between environment readiness and projected-command shim materialization (not grant-related) | Projection reliability pass, or first user report | 2026-07-20 039 spike |
| Repeated write to a path with an undecided pending decision surfaces as a bare guest `EIO` with no typed explanation | HostFS write-overlay UX iteration | 2026-07-20 first-run walkthrough |
