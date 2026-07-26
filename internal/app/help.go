package app

import (
	"fmt"
	"strings"
)

func (a app) helpCommand(args []string) error {
	if len(args) == 0 || (len(args) == 1 && isHelpToken(args[0])) {
		a.primaryUsage()
		return nil
	}
	if len(args) != 1 {
		return fmt.Errorf("usage: hideout help [--all|<topic>]")
	}
	topic := strings.TrimSpace(args[0])
	switch topic {
	case "--all", "all":
		a.allUsage()
	case "setup":
		a.setupUsage()
	case "run":
		a.runUsage("run")
	case "doctor", "readiness":
		a.doctorUsage()
	case "privacy", "connection", "connect":
		a.privacyUsage()
	case "package":
		a.packageUsage()
	case "update", "uninstall":
		a.packageLifecycleUsage()
	case "support", "report":
		a.supportUsage()
	default:
		return fmt.Errorf("unknown help topic %q; use: hideout help --all", topic)
	}
	return nil
}

func (a app) primaryUsage() {
	fmt.Fprintln(a.stdout, "Hideout — run unfamiliar developer tools inside a local VM.")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "First run:")
	fmt.Fprintln(a.stdout, "  hideout setup")
	fmt.Fprintln(a.stdout, "  hideout doctor")
	fmt.Fprintln(a.stdout, "  cd /path/to/project")
	fmt.Fprintln(a.stdout, "  hideout run -- git status --short")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Everyday:")
	fmt.Fprintln(a.stdout, "  hideout run -- <command> [args...]")
	fmt.Fprintln(a.stdout, "  hideout show connection")
	fmt.Fprintln(a.stdout, "  hideout audit show --limit 5")
	fmt.Fprintln(a.stdout, "  hideout support report --out ./hideout-support.json")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Help:")
	fmt.Fprintln(a.stdout, "  hideout help setup|run|doctor|privacy|package|support")
	fmt.Fprintln(a.stdout, "  hideout help --all")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Boundary: macOS arm64 prerelease; the selected project is writable; direct networking does not hide your network origin.")
}

func (a app) setupUsage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  hideout setup")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Review and create the supported default configuration.")
	fmt.Fprintln(a.stdout, "Setup asks once, defaults to no, and does not start a VM or download the runtime.")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Next:")
	fmt.Fprintln(a.stdout, "  hideout doctor")
	fmt.Fprintln(a.stdout, "  cd /path/to/project")
	fmt.Fprintln(a.stdout, "  hideout run -- git status --short")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Automation/advanced:")
	fmt.Fprintln(a.stdout, "  hideout init [flags] --no-input")
}

func (a app) privacyUsage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  hideout show connection")
	fmt.Fprintln(a.stdout, "  hideout connect directly")
	fmt.Fprintln(a.stdout, "  hideout connect through <proxy-secret> using <resolver>")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Direct is the setup default and does not hide your network origin.")
	fmt.Fprintln(a.stdout, "Privacy mode uses Hideout's verified guest helper; you provide the upstream proxy and mediated resolver.")
	fmt.Fprintln(a.stdout, "Missing privacy prerequisites fail closed and never fall back to direct networking.")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Check:")
	fmt.Fprintln(a.stdout, "  hideout doctor --network tun2socks --proxy-secret <ref> --mediated-resolver <ip> --verbose")
}

func (a app) packageLifecycleUsage() {
	fmt.Fprintln(a.stdout, "Homebrew installation:")
	fmt.Fprintln(a.stdout, "  brew upgrade vibe-agi/tap/hideout")
	fmt.Fprintln(a.stdout, "  brew reinstall vibe-agi/tap/hideout")
	fmt.Fprintln(a.stdout, "  brew uninstall vibe-agi/tap/hideout")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Standalone installation:")
	fmt.Fprintln(a.stdout, "  hideout package verify <prefix>")
	fmt.Fprintln(a.stdout, "  hideout package repair --prefix <prefix> --dry-run")
	fmt.Fprintln(a.stdout, "  hideout package uninstall --prefix <prefix> --dry-run")
	fmt.Fprintln(a.stdout, "  hideout package uninstall --prefix <prefix>")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Normal upgrade and uninstall preserve durable state under the reported store path.")
	fmt.Fprintln(a.stdout, "Preview purge, then repeat the exact store path as confirmation:")
	fmt.Fprintln(a.stdout, "  hideout package uninstall --prefix <prefix> --purge --dry-run")
	fmt.Fprintln(a.stdout, "  hideout package uninstall --prefix <prefix> --purge --confirm-purge <exact-store>")
}

func (a app) allUsage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  hideout setup")
	fmt.Fprintln(a.stdout, "  hideout init --template privacy --profile <name> --backend lima --network tun2socks --proxy-secret <ref> --mediated-resolver <ip> --no-input")
	fmt.Fprintln(a.stdout, "  hideout doctor")
	fmt.Fprintln(a.stdout, "  hideout run [flags] -- <command> [args...]")
	fmt.Fprintln(a.stdout, "  hideout show connection [for profile <name>]")
	fmt.Fprintln(a.stdout, "  hideout connect directly [for profile <name>]")
	fmt.Fprintln(a.stdout, "  hideout connect through <proxy-secret> [using <resolver>] [for profile <name>]")
	fmt.Fprintln(a.stdout, "  hideout allow read|write|all <path> [--for-profile <name>]")
	fmt.Fprintln(a.stdout, "  hideout deny read|write|all <path> [--for-profile <name>]")
	fmt.Fprintln(a.stdout, "  hideout allow host-app <command> [--for-profile <name>]   (trust a host app to open this project natively)")
	fmt.Fprintln(a.stdout, "  hideout deny host-app <command> [--for-profile <name>]")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "First run:")
	fmt.Fprintln(a.stdout, "  hideout setup")
	fmt.Fprintln(a.stdout, "  # automation/advanced: hideout init [flags] --no-input")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Run and explain:")
	fmt.Fprintln(a.stdout, "  hideout run [flags] -- <command> [args...]")
	fmt.Fprintln(a.stdout, "  hideout run --explain [flags] -- <command> [args...]")
	fmt.Fprintln(a.stdout, "  hideout adapter-pack <install|list|inspect|test|enable|disable|upgrade|revoke>")
	fmt.Fprintln(a.stdout, "  hideout app <init|add|list|inspect|validate|test|enable>")
	fmt.Fprintln(a.stdout, "  hideout explain [flags] -- <command> [args...]")
	fmt.Fprintln(a.stdout, "  hideout run --preview 127.0.0.1:<guest-port> -- <command>")
	fmt.Fprintln(a.stdout, "  hideout run --fs read:/path --fs dir:/path -- <command>")
	fmt.Fprintln(a.stdout, "  hideout run --no-fs read:/path --no-profile-fs -- <command>")
	fmt.Fprintln(a.stdout, "  hideout run --verbose -- <command>  # print Hideout control-plane progress and summary")
	fmt.Fprintln(a.stdout, "  hideout env list | hideout env inspect <name>")
	fmt.Fprintln(a.stdout, "  hideout session list | hideout session inspect <session-id>")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Profile and HostFS:")
	fmt.Fprintln(a.stdout, "  hideout profile init <name>")
	fmt.Fprintln(a.stdout, "  hideout profile clone <source> <name>")
	fmt.Fprintln(a.stdout, "  hideout profile rotate-identity <name>")
	fmt.Fprintln(a.stdout, "  hideout profile reset <name>")
	fmt.Fprintln(a.stdout, "  hideout profile path <name>")
	fmt.Fprintln(a.stdout, "  hideout profile workspace-path-mode <name> [alias|preserve]")
	fmt.Fprintln(a.stdout, "  hideout profile fs <name> list")
	fmt.Fprintln(a.stdout, "  hideout profile fs <name> add --fs <kind:/path> [--reason <text>]")
	fmt.Fprintln(a.stdout, "  hideout profile fs <name> deny --no-fs <kind:/path> [--reason <text>]")
	fmt.Fprintln(a.stdout, "  hideout profile fs <name> remove <rule-id>")
	fmt.Fprintln(a.stdout, "  hideout profile command-proxy <name> list")
	fmt.Fprintln(a.stdout, "  hideout profile command-proxy <name> add-open <command>")
	fmt.Fprintln(a.stdout, "  hideout profile command-proxy <name> remove <command>")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Inspect and manage:")
	fmt.Fprintln(a.stdout, "  hideout env create|inspect|list|recreate|remove")
	fmt.Fprintln(a.stdout, "  hideout runtime list|inspect|verify")
	fmt.Fprintln(a.stdout, "  hideout stop [--dry-run] [--idle <duration>] [--verbose] [environment-id...]")
	fmt.Fprintln(a.stdout, "  hideout clean [--dry-run] [--stopped] [--idle <duration>] [--verbose] [environment-id...]")
	fmt.Fprintln(a.stdout, "  hideout cleanup [--session <id>] [--dry-run]")
	fmt.Fprintln(a.stdout, "  hideout audit show [--session <id>] [--profile <name>] [--action <name>] [--decision <value>] [--limit N] [--json]")
	fmt.Fprintln(a.stdout, "  hideout audit export --source audit|bundle|boundary-summary|doctor-report --out <path> [--redact <selector>] [--acknowledge-full-fidelity]")
	fmt.Fprintln(a.stdout, "  hideout hostfs write status|plan|claim|apply|discard")
	fmt.Fprintln(a.stdout, "  hideout decision list|inspect|claim|approve|deny|reopen|watch")
	fmt.Fprintln(a.stdout, "  hideout notice list|inspect|ack")
	fmt.Fprintln(a.stdout, "  hideout support matrix [--json]")
	fmt.Fprintln(a.stdout, "  hideout support report --out <path>")
	fmt.Fprintln(a.stdout, "  hideout version")
	fmt.Fprintln(a.stdout, "  hideout ui [--listen 127.0.0.1:0] [--ttl 15m] [--no-open] [--print-url]")
	fmt.Fprintln(a.stdout, "  hideout tui [--profile <name>] [--interval 2s]  # interval is daemon-less fallback only")
	fmt.Fprintln(a.stdout, "  hideout tui --once [--profile <name>]  # script/smoke mode")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Advanced and developer:")
	fmt.Fprintln(a.stdout, "  hideout run --allow-unsafe-workspace -- <command>  # explicit high-risk workspace mount")
	fmt.Fprintln(a.stdout, "  hideout run --backend native --allow-weak-isolation -- <command>  # dev harness only")
	fmt.Fprintln(a.stdout, "  hideout package install|verify|repair|uninstall")
	fmt.Fprintln(a.stdout, "  hideout shim build-linux [--out <path>] [--goarch <arch>] [--source <repo>]")
	fmt.Fprintln(a.stdout, "  hideout hostfsd build-linux [--out <path>] [--goarch <arch>] [--source <repo>]")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Lab probes:")
	fmt.Fprintln(a.stdout, "  hideout lab portbridge loopback --enable-lab --target 127.0.0.1:<port>")
	fmt.Fprintln(a.stdout, "  hideout lab portbridge guest-to-host --enable-lab --target 127.0.0.1:<port>")
	fmt.Fprintln(a.stdout, "  hideout lab portbridge host-to-guest --enable-lab --guest-target 127.0.0.1:<port>")
	fmt.Fprintln(a.stdout, "  hideout lab browser-control --enable-lab --profile <name>")
	fmt.Fprintln(a.stdout, "  hideout lab preview-open --enable-lab --guest-url http://127.0.0.1:<port>")
}
