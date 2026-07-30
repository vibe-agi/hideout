package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/vibe-agi/hideout/internal/daemon"
	"github.com/vibe-agi/hideout/internal/operatorhelp"
)

type commandAudience = operatorhelp.Audience

const (
	commandAudienceNewUser   = operatorhelp.AudienceNewUser
	commandAudienceOperator  = operatorhelp.AudienceOperator
	commandAudienceDeveloper = operatorhelp.AudienceDeveloper
)

type commandStability = operatorhelp.Stability

const (
	commandStabilityStable   = operatorhelp.StabilityStable
	commandStabilityAdvanced = operatorhelp.StabilityAdvanced
	commandStabilityLab      = operatorhelp.StabilityLab
	commandStabilityInternal = operatorhelp.StabilityInternal
)

const (
	commandGroupGetStarted   = "Get started"
	commandGroupRunSafely    = "Run safely"
	commandGroupObserve      = "Observe"
	commandGroupConfigure    = "Configure"
	commandGroupEnvironments = "Environments"
	commandGroupDiagnose     = "Diagnose and recover"
	commandGroupInstall      = "Install and update"
	commandGroupDeveloper    = "Developer tools"
	commandGroupLab          = "Lab"
)

type commandFlag = operatorhelp.Flag

type commandSpec = operatorhelp.Command

type commandHandler func(app, []string) error

type commandCatalogEntry struct {
	spec         commandSpec
	handler      commandHandler
	delegateHelp bool
}

type commandCatalog struct {
	entries []commandCatalogEntry
}

func defaultCommandCatalog() commandCatalog {
	installed := []string{"Hideout is installed for the current macOS user."}
	daemonReady := []string{"The Hideout daemon is running; use `hideout daemon start` if a command says it is unavailable."}
	exactRecovery := []string{"Read the reported operation or target ID, fix the stated prerequisite, then retry that exact command."}
	readOnlySafety := []string{"Read-only: this command does not change Hideout, VM, or project state."}

	return commandCatalog{entries: []commandCatalogEntry{
		{
			spec: commandSpec{
				ID: "setup", Name: "setup", TaskGroup: commandGroupGetStarted,
				Purpose:       "Create the supported default profile through a short, confirm-before-write first-run flow.",
				Syntax:        []string{"hideout setup"},
				Examples:      []string{"hideout setup"},
				Prerequisites: installed,
				Effects:       []string{"Writes the supported default configuration only after confirmation; it does not start a VM or download the runtime."},
				Safety:        []string{"Defaults to No at its confirmation prompt and shows the intended setup before writing."},
				Recovery:      []string{"Run `hideout doctor`; rerun setup if the default profile is incomplete."},
				Next:          []string{"hideout doctor", "hideout run -- git status --short"},
				Audience:      commandAudienceNewUser, Stability: commandStabilityStable,
			},
			handler: dispatchSetup,
		},
		{
			spec: commandSpec{
				ID: "run", Name: "run", TaskGroup: commandGroupRunSafely,
				Purpose: "Run one command inside the selected Hideout VM boundary and observe that command plus its descendants.",
				Syntax: []string{
					"hideout run [flags] -- <command> [args...]",
					"hideout run --explain [flags] -- <command> [args...]",
				},
				Flags: []commandFlag{
					{Name: "--profile", Value: "<name>", Help: "select a profile"},
					{Name: "--backend", Value: "<auto|lima|native>", Help: "select the isolation backend"},
					{Name: "--network", Value: "<direct|tun2socks>", Help: "select a connection mode"},
					{Name: "--proxy-secret", Value: "<ref>", Help: "select a daemon-managed proxy secret"},
					{Name: "--workspace", Value: "<path>", Help: "select the host project"},
					{Name: "--fs", Value: "<kind:/path>", Help: "add a run-scoped HostFS allow rule; repeatable"},
					{Name: "--no-fs", Value: "<kind:/path>", Help: "add a run-scoped HostFS deny rule; repeatable"},
					{Name: "--no-profile-fs", Help: "ignore profile HostFS grants for this run"},
					{Name: "--env", Value: "<name>", Help: "reuse a named environment"},
					{Name: "--preview", Value: "<endpoint>", Help: "expose a guest-loopback preview"},
					{Name: "--terminal", Value: "<auto|always|never>", Help: "control terminal attachment"},
					{Name: "--verbose", Help: "show control-plane progress and a boundary summary"},
					{Name: "--explain", Help: "plan without executing"},
					{Name: "--rm", Help: "remove the runtime environment after command exit"},
					{Name: "--ephemeral", Help: "use session-local identity state"},
					{Name: "--allow-unsafe-workspace", Help: "acknowledge a high-risk workspace mount"},
					{Name: "--allow-weak-isolation", Help: "allow the native development harness"},
				},
				Examples: []string{
					"hideout run -- git status --short",
					"hideout run --profile agent --backend lima -- claude",
					"hideout run --preview 127.0.0.1:3000 -- npm run dev",
				},
				Prerequisites: []string{"Run `hideout setup` and `hideout doctor` first; a privacy connection also needs a stored proxy secret."},
				Effects:       []string{"May create or attach a local VM, mounts the selected project writable, and executes only the command after `--`."},
				Safety:        []string{"The project is writable. Direct networking does not hide network origin. Unsafe/native exceptions require explicit flags."},
				Recovery:      []string{"Use `hideout explain -- <command>` to inspect the boundary, `hideout activity summary` for evidence, or `hideout stop`/`hideout clean` for an exact environment."},
				Next:          []string{"hideout activity summary", "hideout tui", "hideout env list"},
				Audience:      commandAudienceNewUser, Stability: commandStabilityStable,
			},
			handler: dispatchRun,
		},
		{
			spec: commandSpec{
				ID: "show", Name: "show", TaskGroup: commandGroupConfigure,
				Purpose:       "Show the desired and effective connection for a profile.",
				Syntax:        []string{"hideout show connection [for profile <name>]"},
				Examples:      []string{"hideout show connection", "hideout show connection for profile agent"},
				Prerequisites: installed,
				Effects:       []string{"Reads connection configuration and current applicability without changing it."},
				Safety:        readOnlySafety,
				Recovery:      []string{"If effective state is unavailable, start the daemon and inspect the pending next-attach state."},
				Next:          []string{"hideout connect directly", "hideout help connect"},
				Audience:      commandAudienceNewUser, Stability: commandStabilityStable,
			},
			handler: dispatchShow,
		},
		{
			spec: commandSpec{
				ID: "connect", Name: "connect", TaskGroup: commandGroupConfigure,
				Purpose: "Plan and set a profile's desired direct or proxied connection.",
				Syntax: []string{
					"hideout connect directly [for profile <name>] [--yes]",
					"hideout connect through <proxy-secret> [using <resolver>] [for profile <name>] [--yes]",
					"hideout connect plan --profile <name> (--direct | --through <proxy-secret> [--dns <resolver>])",
					"hideout connect apply <operation-id> [--yes]",
				},
				Flags: []commandFlag{
					{Name: "--profile", Value: "<name>", Help: "select the affected profile in plan mode"},
					{Name: "--direct", Help: "plan direct networking"},
					{Name: "--through", Value: "<ref>", Help: "plan a daemon-managed proxy secret reference"},
					{Name: "--dns", Value: "<ip>", Help: "select the mediated resolver in plan mode"},
					{Name: "--yes", Help: "confirm the exact displayed plan non-interactively"},
				},
				Examples: []string{
					"hideout connect directly",
					"hideout connect through local-proxy using 1.1.1.1",
					"hideout connect plan --profile default --through local-proxy --dns 1.1.1.1",
				},
				Prerequisites: []string{
					"For a proxied connection, first store the exact ref with `hideout secret set <ref>`.",
					"One-release compatibility applies only to legacy HIDEOUT_SECRET_<REF> exports.",
					"The daemon startup environment is the only legacy source.",
					"Exports made after daemon start cannot apply. Migrate with `hideout secret set <ref>`.",
				},
				Effects: []string{"Planning is read-only. Confirmed apply changes desired connection state; the result says whether activation is live or pending the next eligible attach."},
				Safety: []string{
					"The canonical diff, effects, blockers, and rollback are displayed before confirmation.",
					"A privacy connection fails closed when its proxy secret or mediated resolver cannot be proved; it never silently falls back to direct.",
				},
				Recovery: []string{"Inspect the exact operation in `hideout tui`; restore with a newly reviewed `hideout connect directly` plan or the prior proved proxy ref."},
				Next:     []string{"hideout show connection", "hideout doctor --network tun2socks --proxy-secret <ref> --mediated-resolver <ip>"},
				Audience: commandAudienceNewUser, Stability: commandStabilityStable,
			},
			handler: dispatchConnect,
		},
		{
			spec: commandSpec{
				ID: "doctor", Name: "doctor", TaskGroup: commandGroupGetStarted,
				Purpose: "Check installation, runtime, profile, network, and observation readiness with actionable findings.",
				Syntax: []string{
					"hideout doctor [flags]",
					"hideout doctor --fix (--dry-run|--apply) [flags]",
				},
				Flags: []commandFlag{
					{Name: "--profile", Value: "<name>", Help: "select a profile"},
					{Name: "--backend", Value: "<name>", Help: "select the backend to diagnose"},
					{Name: "--format", Value: "<human|json>", Help: "select output format"},
					{Name: "--level", Value: "<light|deep>", Help: "select diagnostic depth"},
					{Name: "--verbose", Help: "show every human-readable finding"},
					{Name: "--feature", Value: "<name>", Help: "include a feature diagnostic; repeatable"},
					{Name: "--network", Value: "<mode>", Help: "check a connection mode"},
					{Name: "--proxy-secret", Value: "<ref>", Help: "check a proxy secret ref without printing its value"},
					{Name: "--evidence-out", Value: "<path>", Help: "write a redacted report"},
					{Name: "--fix", Help: "prepare safe initialization repairs"},
					{Name: "--dry-run", Help: "show the repair plan"},
					{Name: "--apply", Help: "apply the reviewed repair plan"},
				},
				Examples:      []string{"hideout doctor", "hideout doctor --level deep --verbose", "hideout doctor --fix --dry-run"},
				Prerequisites: installed,
				Effects:       []string{"Checks local prerequisites. Only `--fix --apply` writes the reviewed safe repair plan."},
				Safety:        []string{"Diagnostic output redacts known credentials; deep probes can take longer and explicit HostFS probes may trigger macOS permission prompts."},
				Recovery:      []string{"Follow the first failed finding's command; preview repairs with `--fix --dry-run` before `--apply`."},
				Next:          []string{"hideout run -- git status --short", "hideout support report --out ./hideout-support.json"},
				Audience:      commandAudienceNewUser, Stability: commandStabilityStable,
			},
			handler: dispatchDoctor,
		},
		{
			spec: commandSpec{
				ID: "activity", Name: "activity", TaskGroup: commandGroupObserve,
				Purpose: "Inspect command, file-metadata, network/DNS, coverage, and explainable-risk evidence for one workload owner.",
				Syntax: []string{
					"hideout activity summary [OWNER] [--from RFC3339] [--to RFC3339] [--json]",
					"hideout activity events|executions|coverage|risks [OWNER] [filters] [--json]",
				},
				Flags: []commandFlag{
					{Name: "--session", Value: "<id>", Help: "select one run"},
					{Name: "--environment", Value: "<id>", Help: "select one environment"},
					{Name: "--incarnation", Value: "<id>", Help: "bind an exact environment incarnation"},
					{Name: "--from", Value: "<RFC3339>", Help: "start the time window"},
					{Name: "--to", Value: "<RFC3339>", Help: "end the time window"},
					{Name: "--cursor", Value: "<cursor>", Help: "continue an event page"},
					{Name: "--limit", Value: "<1..500>", Help: "bound returned events"},
					{Name: "--json", Help: "write canonical JSON"},
				},
				Examples:      []string{"hideout activity summary", "hideout activity events --session <id> --kind network", "hideout activity coverage --session <id>"},
				Prerequisites: daemonReady,
				Effects:       []string{"Queries retained metadata for the selected session or exact environment incarnation; it never reads file contents, environment variables, keystrokes, or full PTY output."},
				Safety:        readOnlySafety,
				Recovery:      []string{"If coverage is Partial or Unavailable, inspect its reason and loss counters before relying on an absence of events."},
				Next:          []string{"hideout activity risks", "hideout tui", "hideout audit export --source boundary-summary --out <path>"},
				Audience:      commandAudienceNewUser, Stability: commandStabilityStable,
			},
			handler: dispatchActivity,
		},
		{
			spec: commandSpec{
				ID: "tui", Name: "tui", TaskGroup: commandGroupObserve,
				Purpose: "Open the live terminal HUD for health, workloads, activity, configuration, and operations.",
				Syntax: []string{
					"hideout tui [--profile <name>] [--session <id>]",
					"hideout tui --once [--profile <name>] [--session <id>]",
				},
				Flags: []commandFlag{
					{Name: "--profile", Value: "<name>", Help: "filter to one profile"},
					{Name: "--session", Value: "<id>", Help: "select one session"},
					{Name: "--once", Help: "render once without an interactive terminal"},
					{Name: "--interval", Value: "<duration>", Help: "daemon-less fallback refresh interval"},
				},
				Examples:      []string{"hideout tui", "hideout tui --session <id>", "hideout tui --once"},
				Prerequisites: []string{"Interactive mode needs a terminal; live data and mutations need the running daemon."},
				Effects:       []string{"Reads the Manager projection. Configuration and lifecycle actions still use review and confirmation modals through Manager APIs."},
				Safety:        []string{"When disconnected, the HUD is visibly STALE and mutation controls are disabled."},
				Recovery:      []string{"Press `?` for keys, `Esc` to close a modal, or use `hideout tui --once` when terminal capabilities are unavailable."},
				Next:          []string{"hideout activity summary", "hideout ui"},
				Audience:      commandAudienceNewUser, Stability: commandStabilityStable,
			},
			handler: dispatchTUI,
		},
		{
			spec: commandSpec{
				ID: "ui", Name: "ui", TaskGroup: commandGroupObserve,
				Purpose: "Open the authenticated local browser console.",
				Syntax:  []string{"hideout ui [--listen 127.0.0.1:0] [--ttl 15m] [--no-open] [--print-url]"},
				Flags: []commandFlag{
					{Name: "--listen", Value: "<loopback:port>", Help: "select a loopback listener"},
					{Name: "--ttl", Value: "<duration>", Help: "set the short-lived UI token lifetime"},
					{Name: "--no-open", Help: "do not launch a browser"},
					{Name: "--print-url", Help: "print endpoints and exit"},
				},
				Examples:      []string{"hideout ui", "hideout ui --no-open", "hideout ui --print-url"},
				Prerequisites: installed,
				Effects:       []string{"Starts an authenticated loopback Manager server for the requested lifetime and may open the default browser."},
				Safety:        []string{"The listener must remain loopback-only; do not share its bearer URL or token."},
				Recovery:      []string{"Close the process or wait for expiry; rerun the command to issue a fresh URL."},
				Next:          []string{"hideout tui", "hideout daemon status --human"},
				Audience:      commandAudienceNewUser, Stability: commandStabilityStable,
			},
			handler: dispatchUI,
		},
		{
			spec: commandSpec{
				ID: "secret", Name: "secret", TaskGroup: commandGroupConfigure,
				SearchTerms: []string{"proxy", "credential", "migration"},
				Purpose:     "Set, rotate, inspect, or delete named credentials in the daemon-managed secure store (macOS Keychain on supported Macs).",
				Syntax: []string{
					"hideout secret set|rotate <ref> [--stdin] [--yes]",
					"hideout secret delete <ref> [--yes]",
					"hideout secret status <ref>",
					"hideout secret list",
				},
				Flags: []commandFlag{
					{Name: "--stdin", Help: "read the value from standard input instead of a hidden terminal prompt"},
					{Name: "--yes", Help: "confirm the reviewed mutation non-interactively"},
				},
				Examples:      []string{"hideout secret set local-proxy", "hideout secret status local-proxy", "hideout secret rotate local-proxy"},
				Prerequisites: daemonReady,
				Effects:       []string{"Set, rotate, and delete change one named secret generation; status and list never reveal values."},
				Safety:        []string{"Secret values never belong in argv, environment exports, plans, output, or shell history. Prefer the hidden prompt."},
				Recovery: []string{
					"One-release compatibility applies only to legacy HIDEOUT_SECRET_<REF> exports.",
					"The daemon startup environment is the only legacy source.",
					"Legacy exports are not imported automatically; re-enter once with `hideout secret set <ref>`, then remove the export from shell setup.",
					"Exports made after daemon start cannot apply.",
					"Healthy updates apply through the running daemon.",
					"For healthy updates, stopping or recreating the VM is not required.",
				},
				Next:     []string{"hideout connect through <ref> using <resolver>", "hideout show connection"},
				Audience: commandAudienceNewUser, Stability: commandStabilityStable,
			},
			handler: dispatchSecret,
		},
		{
			spec: commandSpec{
				ID: "env", Name: "env", TaskGroup: commandGroupEnvironments,
				Purpose:       "Create, inspect, list, recreate, or remove reusable environments.",
				Syntax:        []string{"hideout env create|inspect|list|recreate|remove [arguments]"},
				Examples:      []string{"hideout env list", "hideout env inspect <name>", "hideout env recreate <name>"},
				Prerequisites: installed,
				Effects:       []string{"Inspect/list are read-only. Create/recreate/remove change one named environment and its local runtime state."},
				Safety:        []string{"Recreate and remove are destructive to that environment; active sessions and ownership checks can block the action."},
				Recovery:      []string{"Inspect the exact environment first; recreate it from its profile if disposable data can be lost."},
				Next:          []string{"hideout session list", "hideout stop <environment-id>", "hideout clean <environment-id> --dry-run"},
				Audience:      commandAudienceOperator, Stability: commandStabilityStable,
			},
			handler: dispatchEnv,
		},
		{
			spec: commandSpec{
				ID: "session", Name: "session", TaskGroup: commandGroupObserve,
				Purpose:       "List or inspect active and retained run sessions.",
				Syntax:        []string{"hideout session list", "hideout session inspect <session-id>"},
				Examples:      []string{"hideout session list", "hideout session inspect <session-id>"},
				Prerequisites: installed,
				Effects:       []string{"Reads session identity, environment ownership, state, and retained metadata."},
				Safety:        readOnlySafety,
				Recovery:      []string{"Use the exact session ID shown by list; retained data disappears when its owning environment is cleaned."},
				Next:          []string{"hideout activity summary --session <id>", "hideout tui --session <id>"},
				Audience:      commandAudienceNewUser, Stability: commandStabilityStable,
			},
			handler: dispatchSession,
		},
		{
			spec: commandSpec{
				ID: "audit", Name: "audit", TaskGroup: commandGroupObserve,
				Purpose: "Inspect security decisions or export a reviewed, explicitly scoped record.",
				Syntax: []string{
					"hideout audit show [filters] [--limit N] [--json]",
					"hideout audit export --source <source> --out <path> [redaction flags]",
				},
				Flags: []commandFlag{
					{Name: "--session", Value: "<id>", Help: "filter by session"},
					{Name: "--profile", Value: "<name>", Help: "filter by profile"},
					{Name: "--action", Value: "<name>", Help: "filter by action"},
					{Name: "--decision", Value: "<value>", Help: "filter by decision"},
					{Name: "--limit", Value: "<N>", Help: "bound returned rows"},
					{Name: "--json", Help: "write canonical JSON"},
					{Name: "--source", Value: "<source>", Help: "select an export source"},
					{Name: "--out", Value: "<path>", Help: "write an export to an explicit path"},
					{Name: "--redact", Value: "<selector>", Help: "add an export redaction selector"},
					{Name: "--acknowledge-full-fidelity", Help: "explicitly acknowledge a full-fidelity export"},
				},
				Examples:      []string{"hideout audit show --limit 5", "hideout audit show --session <id> --decision deny", "hideout audit export --source boundary-summary --out ./boundary.json"},
				Prerequisites: installed,
				Effects:       []string{"Show is read-only. Export writes a new reviewed file and applies deterministic credential redaction."},
				Safety:        []string{"Local paths can be visible; review every export before sharing it. Full-fidelity export requires explicit acknowledgement."},
				Recovery:      []string{"Delete an unwanted export with normal OS tools; regenerate with narrower filters or additional redaction selectors."},
				Next:          []string{"hideout activity summary", "hideout support report --out ./hideout-support.json"},
				Audience:      commandAudienceOperator, Stability: commandStabilityStable,
			},
			handler: dispatchAudit,
		},
		{
			spec: commandSpec{
				ID: "support", Name: "support", TaskGroup: commandGroupDiagnose,
				Purpose: "Show supported coverage or create a deterministic redacted support report.",
				Syntax: []string{
					"hideout support matrix [--json]",
					"hideout support report --out <path> [filters]",
					"hideout support readiness --mode <mode> [evidence flags]",
				},
				Flags: []commandFlag{
					{Name: "--json", Help: "write canonical JSON"},
					{Name: "--out", Value: "<path>", Help: "write a report to an explicit path"},
					{Name: "--profile", Value: "<name>", Help: "scope a report to one profile"},
					{Name: "--backend", Value: "<name>", Help: "select a backend"},
					{Name: "--workspace", Value: "<path>", Help: "select a workspace"},
					{Name: "--overwrite", Help: "replace the exact output file"},
					{Name: "--mode", Value: "<mode>", Help: "select a readiness judge"},
				},
				Examples:      []string{"hideout support matrix", "hideout support report --out ./hideout-support.json"},
				Prerequisites: installed,
				Effects:       []string{"Matrix is read-only. Report/readiness commands write only explicitly named evidence outputs."},
				Safety:        []string{"Reports redact known secrets, URI userinfo, authentication fields, sensitive arguments/query values, and control-plane tokens; still review before sharing."},
				Recovery:      []string{"Rerun with a new output path or `--overwrite`; use the report's recovery codes to locate the next safe command."},
				Next:          []string{"hideout doctor --level deep", "hideout help package"},
				Audience:      commandAudienceOperator, Stability: commandStabilityStable,
			},
			handler: dispatchSupport,
		},
		{
			spec: commandSpec{
				ID: "stop", Name: "stop", TaskGroup: commandGroupEnvironments,
				Purpose: "Stop selected environments without deleting their retained data.",
				Syntax:  []string{"hideout stop [--dry-run] [--idle <duration>] [--verbose] [environment-id...]"},
				Flags: []commandFlag{
					{Name: "--dry-run", Help: "show canonical targets without stopping them"},
					{Name: "--idle", Value: "<duration>", Help: "select environments idle for at least this duration"},
					{Name: "--verbose", Help: "show target and skip evidence"},
				},
				Examples:      []string{"hideout stop --dry-run <environment-id>", "hideout stop <environment-id>"},
				Prerequisites: []string{"Use `hideout env list` to obtain exact environment IDs."},
				Effects:       []string{"Stops only canonical planned targets; does not delete their local data."},
				Safety:        []string{"Active sessions, unproved ownership, workspace views, and stale plans block apply."},
				Recovery:      []string{"A later `hideout run --env <name> -- ...` can attach or restart the retained environment."},
				Next:          []string{"hideout env inspect <name>", "hideout clean --dry-run <environment-id>"},
				Audience:      commandAudienceNewUser, Stability: commandStabilityStable,
			},
			handler: dispatchStop,
		},
		{
			spec: commandSpec{
				ID: "clean", Name: "clean", TaskGroup: commandGroupEnvironments,
				Purpose: "Delete selected environment runtime and retained observation data after review.",
				Syntax:  []string{"hideout clean [--dry-run] [--stopped] [--idle <duration>] [--verbose] [environment-id...]"},
				Flags: []commandFlag{
					{Name: "--dry-run", Help: "show canonical targets without deleting them"},
					{Name: "--stopped", Help: "select stopped environments"},
					{Name: "--idle", Value: "<duration>", Help: "select environments idle for at least this duration"},
					{Name: "--verbose", Help: "show target and skip evidence"},
				},
				Examples:      []string{"hideout clean --dry-run <environment-id>", "hideout clean <environment-id>"},
				Prerequisites: []string{"Stop the exact environment first and confirm that its retained data is disposable."},
				Effects:       []string{"Deletes canonical selected environment runtime, incarnation-bound observations, and retained local data."},
				Safety:        []string{"Destructive and not reversible. Active sessions, unproved ownership, workspace views, and stale plans block apply."},
				Recovery:      []string{"Create a new environment from the profile; deleted environment data is not recoverable."},
				Next:          []string{"hideout env list", "hideout run -- <command>"},
				Audience:      commandAudienceNewUser, Stability: commandStabilityStable,
			},
			handler: dispatchClean,
		},
		{
			spec: commandSpec{
				ID: "help", Name: "help", Aliases: []string{"-h", "--help"},
				TaskGroup: commandGroupGetStarted,
				Purpose:   "Find a task, inspect one command's effects and recovery, or search the complete catalog.",
				Syntax: []string{
					"hideout help",
					"hideout help <command>",
					"hideout help all [query]",
					"hideout help search <query>",
				},
				Examples:      []string{"hideout help", "hideout help run", "hideout help search proxy", "hideout help all"},
				Prerequisites: installed,
				Effects:       []string{"Prints local documentation only."},
				Safety:        readOnlySafety,
				Recovery:      []string{"Use `hideout help all` to browse by group or `hideout help search <word>` when a topic is unknown."},
				Next:          []string{"hideout setup", "hideout doctor", "hideout run -- <command>"},
				Audience:      commandAudienceNewUser, Stability: commandStabilityStable,
			},
			handler: dispatchHelp,
		},
		{
			spec: commandSpec{
				ID: "version", Name: "version", Aliases: []string{"-v", "--version"},
				TaskGroup:     commandGroupDiagnose,
				Purpose:       "Print binary, source, build, platform, and support-matrix identity.",
				Syntax:        []string{"hideout version [--json]"},
				Flags:         []commandFlag{{Name: "--json", Help: "write machine-readable binary identity"}},
				Examples:      []string{"hideout version", "hideout version --json"},
				Prerequisites: installed,
				Effects:       []string{"Reads the running binary identity only."},
				Safety:        readOnlySafety,
				Recovery:      []string{"Use this identity when comparing a package, local install, or support report."},
				Next:          []string{"hideout support matrix", "hideout doctor"},
				Audience:      commandAudienceNewUser, Stability: commandStabilityStable,
			},
			handler: dispatchVersion,
		},
		{
			spec: commandSpec{
				ID: "init", Name: "init", TaskGroup: commandGroupConfigure,
				Purpose: "Initialize or repair a named profile non-interactively through typed init tasks.",
				Syntax:  []string{"hideout init [flags]"},
				Flags: []commandFlag{
					{Name: "--profile", Value: "<name>", Help: "select the profile"},
					{Name: "--template", Value: "<id>", Help: "select privacy, hardened, dev, or debug"},
					{Name: "--backend", Value: "<name>", Help: "select the backend"},
					{Name: "--network", Value: "<mode>", Help: "select direct or tun2socks"},
					{Name: "--proxy-secret", Value: "<ref>", Help: "select a proxy secret ref"},
					{Name: "--mediated-resolver", Value: "<ip>", Help: "select a mediated DNS resolver"},
					{Name: "--no-input", Help: "disable prompts"},
					{Name: "--dry-run", Help: "show the typed init plan"},
				},
				Examples:      []string{"hideout init --template privacy --profile agent --backend lima --network tun2socks --proxy-secret local-proxy --mediated-resolver 1.1.1.1 --no-input"},
				Prerequisites: installed,
				Effects:       []string{"Writes only the typed profile/init changes in the reviewed plan."},
				Safety:        []string{"Use `--dry-run` first for automation; native backend is a weak development harness."},
				Recovery:      []string{"Run `hideout doctor --profile <name>`; repeat init with the intended template to repair incomplete state."},
				Next:          []string{"hideout doctor --profile <name>", "hideout run --profile <name> -- <command>"},
				Audience:      commandAudienceOperator, Stability: commandStabilityAdvanced,
			},
			handler: dispatchInit,
		},
		{
			spec: commandSpec{
				ID: "explain", Name: "explain", TaskGroup: commandGroupRunSafely,
				Purpose:       "Resolve and print the run boundary without starting the command.",
				Syntax:        []string{"hideout explain [flags] -- <command> [args...]"},
				Examples:      []string{"hideout explain --profile agent --backend lima -- claude"},
				Prerequisites: []string{"The selected profile and workspace must be resolvable."},
				Effects:       []string{"Builds a run plan but does not execute the target command."},
				Safety:        readOnlySafety,
				Recovery:      []string{"Adjust the reported profile, network, workspace, or HostFS prerequisite and explain again."},
				Next:          []string{"hideout run --profile <name> -- <command>", "hideout doctor"},
				Audience:      commandAudienceNewUser, Stability: commandStabilityStable,
			},
			handler: dispatchExplain,
		},
		{
			spec: commandSpec{
				ID: "allow", Name: "allow", TaskGroup: commandGroupConfigure,
				Purpose: "Add an explicit profile rule allowing a path capability or trusted host application.",
				Syntax: []string{
					"hideout allow read|write|all <path> [--for-profile <name>]",
					"hideout allow host-app <command> [--for-profile <name>]",
				},
				Flags:         []commandFlag{{Name: "--for-profile", Value: "<name>", Help: "select the affected profile"}},
				Examples:      []string{"hideout allow read /absolute/path --for-profile agent"},
				Prerequisites: []string{"Understand why the target tool needs the exact path or host app capability."},
				Effects:       []string{"Adds one desired profile permission for later runs."},
				Safety:        []string{"Grant the narrowest operation and path; writable host paths and trusted host apps expand the security boundary."},
				Recovery:      []string{"Remove the same capability with `hideout deny ...` and inspect the profile before the next run."},
				Next:          []string{"hideout explain --profile <name> -- <command>", "hideout profile fs <name> list"},
				Audience:      commandAudienceOperator, Stability: commandStabilityAdvanced,
			},
			handler: dispatchAllow,
		},
		{
			spec: commandSpec{
				ID: "deny", Name: "deny", TaskGroup: commandGroupConfigure,
				Purpose: "Deny a path capability or revoke a trusted host application for a profile.",
				Syntax: []string{
					"hideout deny read|write|all <path> [--for-profile <name>]",
					"hideout deny host-app <command> [--for-profile <name>]",
				},
				Flags:         []commandFlag{{Name: "--for-profile", Value: "<name>", Help: "select the affected profile"}},
				Examples:      []string{"hideout deny write /absolute/path --for-profile agent"},
				Prerequisites: []string{"Use the same canonical path or host app name as the permission being revoked."},
				Effects:       []string{"Adds a deny or removes the selected host-app trust for later runs."},
				Safety:        []string{"Existing attached workloads remain explicit; inspect effective state before assuming a live process changed."},
				Recovery:      []string{"If the rule was too strict, add back only the required narrow capability with `hideout allow`."},
				Next:          []string{"hideout explain --profile <name> -- <command>", "hideout profile fs <name> list"},
				Audience:      commandAudienceOperator, Stability: commandStabilityAdvanced,
			},
			handler: dispatchDeny,
		},
		{
			spec: commandSpec{
				ID: "profile", Name: "profile", TaskGroup: commandGroupConfigure,
				Purpose:       "Manage advanced profile identity, network, filesystem, environment, tool, and command-adapter policy.",
				Syntax:        []string{"hideout profile <init|clone|rotate-identity|reset|path|network|fs|env|home|tools|command-proxy|command-adapter|host-app-mode> ..."},
				Examples:      []string{"hideout profile path default", "hideout profile fs default list", "hideout profile network default direct"},
				Prerequisites: []string{"Use `hideout setup` for the ordinary first-run path; profile is the advanced policy surface."},
				Effects:       []string{"Read subcommands inspect policy; mutation subcommands change one named profile after their own validation."},
				Safety:        []string{"Identity reset, broad filesystem rules, environment inheritance, and command adapters can materially change the boundary."},
				Recovery:      []string{"Inspect the exact profile path and policy, then restore the previous narrow rule or clone a known-good profile."},
				Next:          []string{"hideout doctor --profile <name>", "hideout explain --profile <name> -- <command>"},
				Audience:      commandAudienceOperator, Stability: commandStabilityAdvanced,
			},
			handler:      dispatchProfile,
			delegateHelp: true,
		},
		{
			spec: commandSpec{
				ID: "runtime", Name: "runtime", TaskGroup: commandGroupEnvironments,
				Purpose:       "List, inspect, and verify installed runtime artifacts.",
				Syntax:        []string{"hideout runtime list|inspect|verify [arguments]"},
				Examples:      []string{"hideout runtime list", "hideout runtime verify <runtime-id>"},
				Prerequisites: installed,
				Effects:       []string{"Reads runtime inventory and verifies its identity and digests."},
				Safety:        readOnlySafety,
				Recovery:      []string{"Repair or reinstall the exact package when verification reports a digest or inventory mismatch."},
				Next:          []string{"hideout doctor", "hideout help package"},
				Audience:      commandAudienceOperator, Stability: commandStabilityAdvanced,
			},
			handler: dispatchRuntime,
		},
		{
			spec: commandSpec{
				ID: "cleanup", Name: "cleanup", TaskGroup: commandGroupEnvironments,
				Purpose: "Clean session-scoped transient resources through the existing cleanup authority.",
				Syntax:  []string{"hideout cleanup [--session <id>] [--dry-run]"},
				Flags: []commandFlag{
					{Name: "--session", Value: "<id>", Help: "scope cleanup to one session"},
					{Name: "--dry-run", Help: "show cleanup effects"},
				},
				Examples:      []string{"hideout cleanup --session <id> --dry-run"},
				Prerequisites: []string{"Obtain an exact session ID with `hideout session list`."},
				Effects:       []string{"Removes only cleanup-owned transient resources selected by the canonical plan."},
				Safety:        []string{"Preview first; environment deletion belongs to `hideout clean`, not this command."},
				Recovery:      exactRecovery,
				Next:          []string{"hideout session list", "hideout env list"},
				Audience:      commandAudienceOperator, Stability: commandStabilityAdvanced,
			},
			handler: dispatchCleanup,
		},
		{
			spec: commandSpec{
				ID: "adapter-pack", Name: "adapter-pack", TaskGroup: commandGroupConfigure,
				Purpose:       "Install and govern signed command-adapter packs.",
				Syntax:        []string{"hideout adapter-pack <install|list|inspect|test|enable|disable|upgrade|revoke> ..."},
				Examples:      []string{"hideout adapter-pack list", "hideout adapter-pack inspect <id>", "hideout adapter-pack test <id>"},
				Prerequisites: []string{"Use a trusted adapter-pack source and inspect its declared capabilities."},
				Effects:       []string{"Mutation subcommands change the local adapter-pack registry; list/inspect/test are non-activating checks."},
				Safety:        []string{"Do not enable an untrusted or unverified pack; adapters can mediate root-sensitive commands."},
				Recovery:      []string{"Disable or revoke the exact pack ID, then inspect affected profiles."},
				Next:          []string{"hideout profile command-adapter <name> list", "hideout doctor"},
				Audience:      commandAudienceDeveloper, Stability: commandStabilityAdvanced,
			},
			handler: dispatchAdapterPack,
		},
		{
			spec: commandSpec{
				ID: "app", Name: "app", TaskGroup: commandGroupConfigure,
				Purpose:       "Manage trusted host-application definitions used by explicit profile policy.",
				Syntax:        []string{"hideout app <init|add|list|inspect|validate|test|enable> ..."},
				Examples:      []string{"hideout app list", "hideout app inspect <id>", "hideout app validate <id>"},
				Prerequisites: []string{"Understand which host-native application the target tool is allowed to invoke."},
				Effects:       []string{"Mutation subcommands change one local host-app definition or enablement."},
				Safety:        []string{"A trusted host application executes outside the VM boundary; validate its exact path and arguments."},
				Recovery:      []string{"Disable the exact app definition or revoke it from the affected profile."},
				Next:          []string{"hideout profile host-app-mode <name> safe", "hideout audit show --action host-app"},
				Audience:      commandAudienceDeveloper, Stability: commandStabilityAdvanced,
			},
			handler: dispatchHostApp,
		},
		{
			spec: commandSpec{
				ID: "decision", Name: "decision", TaskGroup: commandGroupDiagnose,
				Purpose:       "Inspect and resolve explicit security decisions.",
				Syntax:        []string{"hideout decision <list|inspect|claim|release|approve|deny|reopen|watch> ..."},
				Examples:      []string{"hideout decision list", "hideout decision inspect <id>", "hideout decision claim --lease 1m <id>", "hideout decision release --claim-token <token> <id>"},
				Prerequisites: []string{"Use the exact decision ID and review its source, preview, and default outcome."},
				Effects:       []string{"Claim creates a visible 5s–5m lease; release/approve/deny/reopen change one decision state; list/inspect/watch observe it."},
				Safety:        []string{"Approval can authorize a host-side effect; live leases cannot be stolen, and expired takeover requires an exact revision."},
				Recovery:      []string{"Inspect the terminal decision and audit evidence; reopen only when the command explicitly permits it."},
				Next:          []string{"hideout audit show --limit 10", "hideout notice list"},
				Audience:      commandAudienceOperator, Stability: commandStabilityAdvanced,
			},
			handler: dispatchDecision,
		},
		{
			spec: commandSpec{
				ID: "notice", Name: "notice", TaskGroup: commandGroupDiagnose,
				Purpose:       "List, inspect, and acknowledge operator notices.",
				Syntax:        []string{"hideout notice list|inspect|ack [arguments]"},
				Examples:      []string{"hideout notice list", "hideout notice inspect <id>", "hideout notice ack <id>"},
				Prerequisites: installed,
				Effects:       []string{"Acknowledge changes one notice state; list and inspect are read-only."},
				Safety:        []string{"Acknowledgement records that the notice was seen; it does not repair the underlying condition."},
				Recovery:      []string{"Follow the notice's recovery command and verify the condition with doctor or the cited operation."},
				Next:          []string{"hideout doctor", "hideout decision list"},
				Audience:      commandAudienceOperator, Stability: commandStabilityAdvanced,
			},
			handler: dispatchNotice,
		},
		{
			spec: commandSpec{
				ID: "hostfs", Name: "hostfs", TaskGroup: commandGroupDiagnose,
				Purpose:       "Inspect and resolve mediated HostFS write operations.",
				Syntax:        []string{"hideout hostfs write <status|plan|claim|apply|discard> ..."},
				Examples:      []string{"hideout hostfs write status", "hideout hostfs write plan <operation-id>"},
				Prerequisites: []string{"Use the exact operation ID and review source, destination, digest, and privilege evidence."},
				Effects:       []string{"Apply can write the reviewed file to the host; discard removes only the staged operation."},
				Safety:        []string{"Host writes require claim, plan review, target revalidation, and exact operation identity."},
				Recovery:      []string{"On mismatch or stale evidence, discard the operation and let the workload create a new reviewed request."},
				Next:          []string{"hideout audit show --action hostfs.write", "hideout decision list"},
				Audience:      commandAudienceOperator, Stability: commandStabilityAdvanced,
			},
			handler: dispatchHostFS,
		},
		{
			spec: commandSpec{
				ID: "daemon", Name: "daemon", TaskGroup: commandGroupDiagnose,
				Purpose: "Start, inspect, reconcile, or stop the per-user Hideout daemon.",
				Syntax:  []string{"hideout daemon start|status|reconcile|stop [flags]"},
				Flags: []commandFlag{
					{Name: "--ttl", Value: "<duration>", Help: "set operator token lifetime on start"},
					{Name: "--human", Help: "render compact status"},
					{Name: "--env", Value: "<name-or-id>", Help: "reconcile one environment"},
				},
				Examples:      []string{"hideout daemon status --human", "hideout daemon start", "hideout daemon reconcile --env <id>"},
				Prerequisites: installed,
				Effects:       []string{"Start/stop change the local control-plane process; reconcile targets one environment. Status is read-only."},
				Safety:        []string{"Stopping the daemon interrupts live control and observation; it is not required for normal proxy-secret or connection updates."},
				Recovery:      []string{"Run `hideout daemon start`, then verify with `hideout daemon status --human`."},
				Next:          []string{"hideout doctor", "hideout tui"},
				Audience:      commandAudienceOperator, Stability: commandStabilityAdvanced,
			},
			handler: dispatchDaemon,
		},
		{
			spec: commandSpec{
				ID: "package", Name: "package", TaskGroup: commandGroupInstall,
				Purpose: "Verify, install, repair, or uninstall a standalone Hideout package.",
				Syntax: []string{
					"hideout package verify <package-root-or-install-prefix>",
					"hideout package install <package-root> --prefix <dir> [flags]",
					"hideout package repair|uninstall --prefix <dir> [flags]",
				},
				Flags: []commandFlag{
					{Name: "--prefix", Value: "<dir>", Help: "select an exact installation prefix"},
					{Name: "--store", Value: "<dir>", Help: "select the durable data store"},
					{Name: "--dry-run", Help: "preview repair or uninstall"},
					{Name: "--purge", Help: "also select durable state for deletion"},
					{Name: "--confirm-purge", Value: "<exact-store>", Help: "confirm the exact durable store path"},
				},
				Examples:      []string{"hideout package verify <prefix>", "hideout package repair --prefix <prefix> --dry-run", "hideout package uninstall --prefix <prefix> --dry-run"},
				Prerequisites: []string{"Homebrew users should use `brew upgrade`, `brew reinstall`, or `brew uninstall`; this command is for standalone packages."},
				Effects:       []string{"Verify is read-only. Repair/install change package-owned files. Normal uninstall preserves durable state; purge deletes the exact confirmed store."},
				Safety:        []string{"Verify first and preview destructive actions. Purge requires the exact store path because deleted state is not recoverable."},
				Recovery:      []string{"Reinstall the verified package. Preserved durable state remains available unless an explicitly confirmed purge completed."},
				Next:          []string{"hideout version", "hideout doctor"},
				Audience:      commandAudienceOperator, Stability: commandStabilityStable,
			},
			handler: dispatchPackage,
		},
		{
			spec: commandSpec{
				ID: "shim", Name: "shim", TaskGroup: commandGroupDeveloper,
				Purpose: "Build the Linux guest command shim from the current source tree.",
				Syntax:  []string{"hideout shim build-linux [--out <path>] [--goarch <arch>] [--source <repo>]"},
				Flags: []commandFlag{
					{Name: "--out", Value: "<path>", Help: "select the output artifact"},
					{Name: "--goarch", Value: "<arch>", Help: "select the Linux architecture"},
					{Name: "--source", Value: "<repo>", Help: "select the source tree"},
				},
				Examples:      []string{"hideout shim build-linux --out ./hideout-shim"},
				Prerequisites: []string{"A Go toolchain and a trusted Hideout source tree are available."},
				Effects:       []string{"Writes one Linux helper artifact to the selected output path."},
				Safety:        []string{"This is a package-development command; verify the resulting digest before embedding it."},
				Recovery:      []string{"Delete the generated artifact or rebuild it from the intended source commit."},
				Next:          []string{"hideout package verify <package-root>", "hideout support release package-identity --archive <path> --out <path>"},
				Audience:      commandAudienceDeveloper, Stability: commandStabilityAdvanced,
			},
			handler: dispatchShim,
		},
		{
			spec: commandSpec{
				ID: "hostfsd", Name: "hostfsd", TaskGroup: commandGroupDeveloper,
				Purpose: "Build the Linux guest HostFS helper from the current source tree.",
				Syntax:  []string{"hideout hostfsd build-linux [--out <path>] [--goarch <arch>] [--source <repo>]"},
				Flags: []commandFlag{
					{Name: "--out", Value: "<path>", Help: "select the output artifact"},
					{Name: "--goarch", Value: "<arch>", Help: "select the Linux architecture"},
					{Name: "--source", Value: "<repo>", Help: "select the source tree"},
				},
				Examples:      []string{"hideout hostfsd build-linux --out ./hideout-hostfsd"},
				Prerequisites: []string{"A Go toolchain and a trusted Hideout source tree are available."},
				Effects:       []string{"Writes one Linux helper artifact to the selected output path."},
				Safety:        []string{"This is a package-development command; verify the resulting digest before embedding it."},
				Recovery:      []string{"Delete the generated artifact or rebuild it from the intended source commit."},
				Next:          []string{"hideout package verify <package-root>", "hideout support release package-identity --archive <path> --out <path>"},
				Audience:      commandAudienceDeveloper, Stability: commandStabilityAdvanced,
			},
			handler: dispatchHostFSD,
		},
		{
			spec: commandSpec{
				ID: "lab", Name: "lab", TaskGroup: commandGroupLab,
				Purpose:       "Run explicitly enabled, unsupported security and connectivity probes.",
				Syntax:        []string{"hideout lab <portbridge|browser-control|preview-open> --enable-lab [flags]"},
				Flags:         []commandFlag{{Name: "--enable-lab", Help: "explicitly acknowledge unsupported lab behavior"}},
				Examples:      []string{"hideout lab portbridge loopback --enable-lab --target 127.0.0.1:<port>"},
				Prerequisites: []string{"Use only disposable development state and understand the probe's boundary."},
				Effects:       []string{"May create temporary listeners, guest/host bridges, or browser-control probes."},
				Safety:        []string{"Lab commands are unsupported, excluded from ordinary product claims, and require `--enable-lab`."},
				Recovery:      []string{"Stop the probe, remove disposable environments, and return to a supported doctor/run journey."},
				Next:          []string{"hideout doctor", "hideout support matrix"},
				Audience:      commandAudienceDeveloper, Stability: commandStabilityLab,
			},
			handler: dispatchLab,
		},
		{
			spec: commandSpec{
				ID: "internal-daemon-serve", Name: daemon.InternalDaemonServeCommand,
				TaskGroup:     commandGroupDeveloper,
				Purpose:       "Serve the private daemon child process.",
				Syntax:        []string{"hideout " + daemon.InternalDaemonServeCommand},
				Examples:      []string{"hideout " + daemon.InternalDaemonServeCommand},
				Prerequisites: installed,
				Effects:       []string{"Runs the private daemon child entrypoint."},
				Safety:        []string{"Internal package entrypoint; not a supported operator command."},
				Recovery:      []string{"Use `hideout daemon start` instead."},
				Next:          []string{"hideout daemon status --human"},
				Audience:      commandAudienceDeveloper, Stability: commandStabilityInternal,
				Hidden: true,
			},
			handler: dispatchInternalDaemonServe,
		},
	}}
}

func defaultOperatorHelpCatalog() operatorhelp.Catalog {
	commands := make([]operatorhelp.Command, 0)
	for _, entry := range defaultCommandCatalog().entries {
		if entry.spec.Hidden {
			continue
		}
		commands = append(commands, entry.spec)
	}
	return (operatorhelp.Catalog{
		Schema:   operatorhelp.CatalogSchema,
		Commands: commands,
	}).Clone()
}

func (catalog commandCatalog) lookup(token string) (commandCatalogEntry, bool) {
	for _, entry := range catalog.entries {
		if token == entry.spec.Name {
			return entry, true
		}
		for _, alias := range entry.spec.Aliases {
			if token == alias {
				return entry, true
			}
		}
	}
	return commandCatalogEntry{}, false
}

func validateCommandCatalog(catalog commandCatalog) error {
	if len(catalog.entries) == 0 {
		return errors.New("command catalog is empty")
	}
	ids := make(map[string]string, len(catalog.entries))
	tokens := make(map[string]string, len(catalog.entries))
	for index, entry := range catalog.entries {
		spec := entry.spec
		where := fmt.Sprintf("command catalog entry %d", index)
		if strings.TrimSpace(spec.ID) == "" {
			return fmt.Errorf("%s has no id", where)
		}
		if prior, exists := ids[spec.ID]; exists {
			return fmt.Errorf("%s duplicates id %q from %s", where, spec.ID, prior)
		}
		ids[spec.ID] = where
		if strings.TrimSpace(spec.Name) == "" {
			return fmt.Errorf("%s has no route", where)
		}
		if entry.handler == nil {
			return fmt.Errorf("%s route %q has no dispatch adapter", where, spec.Name)
		}
		if err := claimCatalogToken(tokens, spec.Name, spec.Name); err != nil {
			return err
		}
		for _, alias := range spec.Aliases {
			if err := claimCatalogToken(tokens, alias, spec.Name); err != nil {
				return err
			}
		}
		switch spec.Audience {
		case commandAudienceNewUser, commandAudienceOperator, commandAudienceDeveloper:
		default:
			return fmt.Errorf("%s route %q has invalid audience %q", where, spec.Name, spec.Audience)
		}
		switch spec.Stability {
		case commandStabilityStable, commandStabilityAdvanced, commandStabilityLab, commandStabilityInternal:
		default:
			return fmt.Errorf("%s route %q has invalid stability %q", where, spec.Name, spec.Stability)
		}
		if spec.Hidden {
			if spec.Stability != commandStabilityInternal {
				return fmt.Errorf("%s hidden route %q is not internal", where, spec.Name)
			}
			continue
		}
		required := map[string]string{
			"task group": spec.TaskGroup,
			"purpose":    spec.Purpose,
		}
		for field, value := range required {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("%s route %q has no %s", where, spec.Name, field)
			}
		}
		requiredLists := map[string][]string{
			"syntax":        spec.Syntax,
			"examples":      spec.Examples,
			"prerequisites": spec.Prerequisites,
			"effects":       spec.Effects,
			"safety":        spec.Safety,
			"recovery":      spec.Recovery,
			"next commands": spec.Next,
		}
		for field, values := range requiredLists {
			if len(values) == 0 {
				return fmt.Errorf("%s route %q has no %s", where, spec.Name, field)
			}
			for _, value := range values {
				if strings.TrimSpace(value) == "" {
					return fmt.Errorf("%s route %q has a blank %s item", where, spec.Name, field)
				}
			}
		}
		flagNames := make(map[string]struct{}, len(spec.Flags))
		for _, flag := range spec.Flags {
			if !strings.HasPrefix(flag.Name, "--") || strings.TrimSpace(flag.Help) == "" {
				return fmt.Errorf("%s route %q has invalid flag metadata for %q", where, spec.Name, flag.Name)
			}
			if _, exists := flagNames[flag.Name]; exists {
				return fmt.Errorf("%s route %q duplicates flag %q", where, spec.Name, flag.Name)
			}
			flagNames[flag.Name] = struct{}{}
		}
	}
	return nil
}

func claimCatalogToken(tokens map[string]string, token, route string) error {
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("route %q has a blank alias", route)
	}
	if prior, exists := tokens[token]; exists {
		return fmt.Errorf("catalog token %q is shared by routes %q and %q", token, prior, route)
	}
	tokens[token] = route
	return nil
}

func dispatchInternalDaemonServe(a app, args []string) error {
	return a.daemonServe(args)
}

func dispatchInit(a app, args []string) error {
	return a.initCommand(args)
}

func dispatchRun(a app, args []string) error {
	if containsCommandHelpToken(args) {
		a.runUsage("run")
		return nil
	}
	return a.runCommand(args, false)
}

func dispatchSetup(a app, args []string) error {
	if containsHelpToken(args) {
		a.setupUsage()
		return nil
	}
	return a.operatorIntent(append([]string{"setup"}, args...))
}

func dispatchShow(a app, args []string) error {
	return a.operatorIntent(append([]string{"show"}, args...))
}

func dispatchConnect(a app, args []string) error {
	return a.connectCommand(args)
}

func dispatchAllow(a app, args []string) error {
	return a.operatorIntent(append([]string{"allow"}, args...))
}

func dispatchDeny(a app, args []string) error {
	return a.operatorIntent(append([]string{"deny"}, args...))
}

func dispatchExplain(a app, args []string) error {
	if containsCommandHelpToken(args) {
		a.runUsage("explain")
		return nil
	}
	return a.runCommand(args, true)
}

func dispatchDoctor(a app, args []string) error {
	return a.doctor(args)
}

func dispatchSupport(a app, args []string) error {
	return a.supportCommand(args)
}

func dispatchProfile(a app, args []string) error {
	return a.profile(args)
}

func dispatchEnv(a app, args []string) error {
	return a.envCommand(args)
}

func dispatchSession(a app, args []string) error {
	return a.sessionCommand(args)
}

func dispatchRuntime(a app, args []string) error {
	return a.runtimeCommand(args)
}

func dispatchStop(a app, args []string) error {
	return a.stopEnvironments(args)
}

func dispatchClean(a app, args []string) error {
	return a.cleanEnvironments(args)
}

func dispatchCleanup(a app, args []string) error {
	return a.cleanup(args)
}

func dispatchAudit(a app, args []string) error {
	return a.auditCommand(args)
}

func dispatchActivity(a app, args []string) error {
	return a.activityCommand(args)
}

func dispatchSecret(a app, args []string) error {
	return a.secretCommand(args)
}

func dispatchAdapterPack(a app, args []string) error {
	return a.adapterPackCommand(args)
}

func dispatchHostApp(a app, args []string) error {
	return a.hostAppCommand(args)
}

func dispatchDecision(a app, args []string) error {
	return a.decisionCommand(args)
}

func dispatchNotice(a app, args []string) error {
	return a.noticeCommand(args)
}

func dispatchUI(a app, args []string) error {
	return a.ui(args)
}

func dispatchDaemon(a app, args []string) error {
	return a.daemonCommand(args)
}

func dispatchTUI(a app, args []string) error {
	return a.tui(args)
}

func dispatchVersion(a app, args []string) error {
	return a.version(args)
}

func dispatchLab(a app, args []string) error {
	return a.lab(args)
}

func dispatchShim(a app, args []string) error {
	return a.shim(args)
}

func dispatchHostFSD(a app, args []string) error {
	return a.hostfsd(args)
}

func dispatchHostFS(a app, args []string) error {
	return a.hostfsCommand(args)
}

func dispatchPackage(a app, args []string) error {
	return a.packageCommand(args)
}

func dispatchHelp(a app, args []string) error {
	return a.helpCommand(args)
}
