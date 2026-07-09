package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/vibe-agi/hideout/internal/adapterpack"
	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/backend/lima"
	"github.com/vibe-agi/hideout/internal/backend/native"
	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/cmdadapter"
	"github.com/vibe-agi/hideout/internal/cmdproxy"
	"github.com/vibe-agi/hideout/internal/daemon"
	doctorpkg "github.com/vibe-agi/hideout/internal/doctor"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/envpolicy"
	exportboundary "github.com/vibe-agi/hideout/internal/export"
	"github.com/vibe-agi/hideout/internal/helperbin"
	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/hostopen"
	"github.com/vibe-agi/hideout/internal/inittask"
	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/packagekit"
	"github.com/vibe-agi/hideout/internal/policy"
	"github.com/vibe-agi/hideout/internal/portbridge"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/profiletemplate"
	"github.com/vibe-agi/hideout/internal/releasecompat"
	"github.com/vibe-agi/hideout/internal/session"
)

type app struct {
	stdout io.Writer
	stderr io.Writer
	stdin  io.Reader
}

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

func Main(args []string, stdout, stderr io.Writer) int {
	a := app{stdout: stdout, stderr: stderr, stdin: os.Stdin}
	if err := a.run(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			a.usage()
			return 0
		}
		fmt.Fprintln(stderr, "hideout:", err)
		return 1
	}
	return 0
}

func (a app) run(args []string) error {
	if len(args) == 0 {
		a.usage()
		return nil
	}
	switch args[0] {
	case "init":
		return a.initCommand(args[1:])
	case "run":
		return a.runCommand(args[1:], false)
	case "explain":
		return a.runCommand(args[1:], true)
	case "doctor":
		return a.doctor(args[1:])
	case "support":
		return a.supportCommand(args[1:])
	case "profile":
		return a.profile(args[1:])
	case "env":
		return a.envCommand(args[1:])
	case "stop":
		return a.stopEnvironments(args[1:])
	case "clean":
		return a.cleanEnvironments(args[1:])
	case "cleanup":
		return a.cleanup(args[1:])
	case "audit":
		return a.auditCommand(args[1:])
	case "adapter-pack":
		return a.adapterPackCommand(args[1:])
	case "decision":
		return a.decisionCommand(args[1:])
	case "notice":
		return a.noticeCommand(args[1:])
	case "ui":
		return a.ui(args[1:])
	case "daemon":
		return a.daemonCommand(args[1:])
	case "tui":
		return a.tui(args[1:])
	case "version", "--version", "-v":
		a.version()
		return nil
	case "lab":
		return a.lab(args[1:])
	case "shim":
		return a.shim(args[1:])
	case "hostfsd":
		return a.hostfsd(args[1:])
	case "hostfs":
		return a.hostfsCommand(args[1:])
	case "package":
		return a.packageCommand(args[1:])
	case "help", "-h", "--help":
		a.usage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func (a app) usage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  hideout init --template privacy --profile <name> --backend lima --network tun2socks --proxy-secret <ref> --mediated-resolver <ip> --no-input")
	fmt.Fprintln(a.stdout, "  hideout doctor")
	fmt.Fprintln(a.stdout, "  hideout run [flags] -- <command> [args...]")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "First run:")
	fmt.Fprintln(a.stdout, "  hideout init --template privacy --profile default --backend lima --network tun2socks --proxy-secret <ref> --mediated-resolver <ip> --no-input")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Run and explain:")
	fmt.Fprintln(a.stdout, "  hideout run [flags] -- <command> [args...]")
	fmt.Fprintln(a.stdout, "  hideout run --explain [flags] -- <command> [args...]")
	fmt.Fprintln(a.stdout, "  hideout adapter-pack <install|list|inspect|test|enable|disable|upgrade|revoke>")
	fmt.Fprintln(a.stdout, "  hideout explain [flags] -- <command> [args...]")
	fmt.Fprintln(a.stdout, "  hideout run --preview 127.0.0.1:<guest-port> -- <command>")
	fmt.Fprintln(a.stdout, "  hideout run --fs read:/path --fs dir:/path -- <command>")
	fmt.Fprintln(a.stdout, "  hideout run --no-fs read:/path --no-profile-fs -- <command>")
	fmt.Fprintln(a.stdout, "  hideout run --verbose -- <command>  # print Hideout control-plane progress and summary")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Profile and HostFS:")
	fmt.Fprintln(a.stdout, "  hideout profile init <name>")
	fmt.Fprintln(a.stdout, "  hideout profile clone <source> <name>")
	fmt.Fprintln(a.stdout, "  hideout profile rotate-identity <name>")
	fmt.Fprintln(a.stdout, "  hideout profile reset <name>")
	fmt.Fprintln(a.stdout, "  hideout profile path <name>")
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
	fmt.Fprintln(a.stdout, "  hideout stop [--dry-run] [--idle <duration>] [--verbose] [environment-id...]")
	fmt.Fprintln(a.stdout, "  hideout clean [--dry-run] [--stopped] [--idle <duration>] [--verbose] [environment-id...]")
	fmt.Fprintln(a.stdout, "  hideout cleanup [--session <id>] [--dry-run]")
	fmt.Fprintln(a.stdout, "  hideout audit show [--session <id>] [--profile <name>] [--action <name>] [--decision <value>] [--limit N] [--json]")
	fmt.Fprintln(a.stdout, "  hideout audit export --source audit|bundle|boundary-summary|doctor-report --out <path> [--redact <selector>] [--acknowledge-full-fidelity]")
	fmt.Fprintln(a.stdout, "  hideout hostfs write status|plan|claim|apply|discard")
	fmt.Fprintln(a.stdout, "  hideout decision list|inspect|claim|approve|deny|watch")
	fmt.Fprintln(a.stdout, "  hideout notice list|inspect|ack")
	fmt.Fprintln(a.stdout, "  hideout support matrix [--json]")
	fmt.Fprintln(a.stdout, "  hideout version")
	fmt.Fprintln(a.stdout, "  hideout ui [--listen 127.0.0.1:0] [--ttl 15m] [--no-open] [--print-url]")
	fmt.Fprintln(a.stdout, "  hideout tui [--profile <name>] [--interval 2s]")
	fmt.Fprintln(a.stdout, "  hideout tui --once [--profile <name>]  # script/smoke mode")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Advanced and developer:")
	fmt.Fprintln(a.stdout, "  hideout run --allow-unsafe-workspace -- <command>  # explicit high-risk workspace mount")
	fmt.Fprintln(a.stdout, "  hideout run --backend native --allow-weak-isolation -- <command>  # dev harness only")
	fmt.Fprintln(a.stdout, "  hideout package install|verify|uninstall")
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

func (a app) version() {
	fmt.Fprintf(a.stdout, "hideout %s\n", Version)
	fmt.Fprintf(a.stdout, "commit: %s\n", Commit)
	fmt.Fprintf(a.stdout, "builtAt: %s\n", BuildTime)
	fmt.Fprintf(a.stdout, "go: %s\n", runtime.Version())
	fmt.Fprintf(a.stdout, "platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(a.stdout, "supportMatrix: %s %s\n", releasecompat.MatrixSchema, releasecompat.MatrixVersion)
	fmt.Fprintf(a.stdout, "support: %s\n", releasecompat.CurrentSupportSummary("auto"))
}

func (a app) supportUsage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  hideout support matrix [--json]")
	fmt.Fprintln(a.stdout, "  hideout support readiness --mode local-fast|release-candidate [--out <path>] [--gate2-evidence <path>] [--gate3-evidence <path>]")
}

func (a app) supportCommand(args []string) error {
	if len(args) == 0 || containsHelpToken(args) {
		a.supportUsage()
		return nil
	}
	switch args[0] {
	case "matrix":
		return a.supportMatrix(args[1:])
	case "readiness":
		return a.supportReadiness(args[1:])
	default:
		return fmt.Errorf("unknown support command %q", args[0])
	}
}

func (a app) supportMatrix(args []string) error {
	fs := flag.NewFlagSet("support matrix", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "write JSON support matrix")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected support matrix argument %q", fs.Arg(0))
	}
	matrix := releasecompat.BuiltinMatrix()
	if err := releasecompat.ValidateMatrix(matrix); err != nil {
		return err
	}
	if *jsonOut {
		return releasecompat.WriteMatrixJSON(a.stdout, matrix)
	}
	fmt.Fprintln(a.stdout, "Hideout support matrix")
	fmt.Fprintf(a.stdout, "schema: %s\n", matrix.Schema)
	fmt.Fprintf(a.stdout, "version: %s\n", matrix.Version)
	for _, entry := range matrix.Entries {
		fmt.Fprintf(a.stdout, "%s: %s (%s)\n", entry.Subject, entry.Level, entry.Guidance)
	}
	return nil
}

func (a app) supportReadiness(args []string) error {
	fs := flag.NewFlagSet("support readiness", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	mode := fs.String("mode", "local-fast", "local-fast or release-candidate")
	out := fs.String("out", "", "write readiness JSON to path")
	gate2 := fs.String("gate2-evidence", "", "real Gate 2 evidence path")
	gate3 := fs.String("gate3-evidence", "", "real Gate 3 evidence path")
	localStatus := fs.String("local-status", "passed", "passed or failed")
	commit := fs.String("commit", Commit, "commit identifier")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected support readiness argument %q", fs.Arg(0))
	}
	localPassed := true
	switch *localStatus {
	case "passed":
		localPassed = true
	case "failed":
		localPassed = false
	default:
		return fmt.Errorf("unsupported --local-status %q", *localStatus)
	}
	ready, err := releasecompat.BuildReadiness(releasecompat.ReadinessOptions{
		Mode:          *mode,
		Commit:        *commit,
		Gate2Evidence: *gate2,
		Gate3Evidence: *gate3,
		LocalPassed:   localPassed,
	})
	if err != nil {
		return err
	}
	if err := releasecompat.ValidateReadiness(ready); err != nil {
		return err
	}
	if *out != "" {
		if err := writeReadinessFile(*out, ready); err != nil {
			return err
		}
	} else if err := releasecompat.WriteReadinessJSON(a.stdout, ready); err != nil {
		return err
	}
	if !localPassed {
		return errors.New("release readiness local checks failed")
	}
	if ready.Mode == "release-candidate" && !ready.ReleaseReady {
		return errors.New("release readiness is missing required real gate evidence")
	}
	return nil
}

func writeReadinessFile(path string, ready releasecompat.Readiness) error {
	clean := filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(clean), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(clean), "."+filepath.Base(clean)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	keepTemp := true
	defer func() {
		if keepTemp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := releasecompat.WriteReadinessJSON(tmp, ready); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, clean); err != nil {
		return err
	}
	keepTemp = false
	return nil
}

func (a app) initUsage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  hideout init [flags]")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Initialize or repair Hideout machine/profile state through typed init tasks.")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Common:")
	fmt.Fprintln(a.stdout, "  hideout init --template privacy --profile agent --backend lima --network tun2socks --proxy-secret <ref> --mediated-resolver 1.1.1.1 --no-input")
	fmt.Fprintln(a.stdout, "  hideout init --template dev --profile agent-dev --backend native --network direct --no-input")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Flags:")
	fmt.Fprintln(a.stdout, "  --profile <name>          profile to initialize (default: default)")
	fmt.Fprintln(a.stdout, "  --template <id>           privacy, hardened, dev, or debug")
	fmt.Fprintln(a.stdout, "  --backend <name>          auto or lima for product isolation; native is a weak dev harness")
	fmt.Fprintln(a.stdout, "  --network <mode>          direct or tun2socks")
	fmt.Fprintln(a.stdout, "  --proxy-secret <ref>      host-only proxy secret ref for tun2socks")
	fmt.Fprintln(a.stdout, "  --mediated-resolver <ip>  DNS resolver IP for tun2socks DoH mediation")
	fmt.Fprintln(a.stdout, "  --privilege-status <s>    enforced, degraded, or unknown")
	fmt.Fprintln(a.stdout, "  --allow-degraded-template allow visibly degraded hardened fallback")
	fmt.Fprintln(a.stdout, "  --no-input                do not ask for confirmation")
	fmt.Fprintln(a.stdout, "  --dry-run                 print the init plan without applying it")
}

func (a app) runUsage(commandName string) {
	if strings.TrimSpace(commandName) == "" {
		commandName = "run"
	}
	explain := commandName == "explain"
	fmt.Fprintln(a.stdout, "Usage:")
	if explain {
		fmt.Fprintln(a.stdout, "  hideout explain [flags] -- <command> [args...]")
	} else {
		fmt.Fprintln(a.stdout, "  hideout run [flags] -- <command> [args...]")
		fmt.Fprintln(a.stdout, "  hideout run --explain [flags] -- <command> [args...]")
	}
	fmt.Fprintln(a.stdout)
	if explain {
		fmt.Fprintln(a.stdout, "Print the run boundary without executing the command.")
	} else {
		fmt.Fprintln(a.stdout, "Run a command in the selected Hideout boundary.")
	}
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Common:")
	fmt.Fprintln(a.stdout, "  hideout run --profile smoke --backend lima --network direct -- pwd")
	fmt.Fprintln(a.stdout, "  hideout run --profile agent --backend lima -- <command>")
	fmt.Fprintln(a.stdout, "  hideout run --preview 127.0.0.1:<guest-port> -- npm run dev")
	fmt.Fprintln(a.stdout, "  hideout run --fs read:/absolute/file -- <command>")
	fmt.Fprintln(a.stdout, "  hideout explain --profile agent --backend lima -- <command>")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Flags:")
	fmt.Fprintln(a.stdout, "  --profile <name>              profile name (default: default)")
	fmt.Fprintln(a.stdout, "  --backend <name>              auto/lima for isolation; native requires --allow-weak-isolation")
	fmt.Fprintln(a.stdout, "  --network <mode>              direct or tun2socks")
	fmt.Fprintln(a.stdout, "  --proxy-secret <ref>          proxy secret ref for tun2socks")
	fmt.Fprintln(a.stdout, "  --workspace <path>            host workspace (default: current directory)")
	fmt.Fprintln(a.stdout, "  --guest-workspace <path>      guest workspace path")
	fmt.Fprintln(a.stdout, "  --audit <path|off>            audit path or off")
	fmt.Fprintln(a.stdout, "  --fs <kind:/path>             run-scoped HostFS allow rule; may be repeated")
	fmt.Fprintln(a.stdout, "  --no-fs <kind:/path>          run-scoped HostFS deny rule; may be repeated")
	fmt.Fprintln(a.stdout, "  --no-profile-fs               ignore profile HostFS grants for this run")
	fmt.Fprintln(a.stdout, "  --env <name>                  run inside the named environment")
	fmt.Fprintln(a.stdout, "  --env-var KEY=VALUE           run-scoped public environment variable")
	fmt.Fprintln(a.stdout, "  --preview <endpoint|id>       expose a guest-loopback endpoint to the host browser")
	fmt.Fprintln(a.stdout, "  --verbose                     print Hideout control-plane progress and boundary summary")
	fmt.Fprintln(a.stdout, "  --explain                     print the run boundary without executing")
	fmt.Fprintln(a.stdout, "  --rm                          remove the runtime environment after command exit")
	fmt.Fprintln(a.stdout, "  --ephemeral                   use session-local identity state")
	fmt.Fprintln(a.stdout, "  --allow-unsafe-workspace      explicitly allow a high-risk workspace mount")
	fmt.Fprintln(a.stdout, "  --allow-weak-isolation        allow the native development harness")
}

func (a app) doctorUsage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  hideout doctor [flags]")
	fmt.Fprintln(a.stdout, "  hideout doctor --fix (--dry-run|--apply) [flags]")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Check Hideout setup, or apply safe initialization repairs through InitTask.")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Common:")
	fmt.Fprintln(a.stdout, "  hideout doctor --profile default --backend lima --network direct")
	fmt.Fprintln(a.stdout, "  hideout doctor --format json --feature dns")
	fmt.Fprintln(a.stdout, "  hideout doctor --fix --dry-run --profile agent --backend lima")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Flags:")
	fmt.Fprintln(a.stdout, "  --profile <name>          profile name (default: default)")
	fmt.Fprintln(a.stdout, "  --backend <name>          backend to diagnose (default: auto)")
	fmt.Fprintln(a.stdout, "  --format <human|json>     output format (default: human)")
	fmt.Fprintln(a.stdout, "  --level <light|deep>      diagnostic depth (default: light)")
	fmt.Fprintln(a.stdout, "  --feature <name>          include a feature diagnostic; may be repeated")
	fmt.Fprintln(a.stdout, "  --network <mode>          direct or tun2socks")
	fmt.Fprintln(a.stdout, "  --proxy-secret <ref>      proxy secret ref for tun2socks")
	fmt.Fprintln(a.stdout, "  --evidence-out <path>     save a redacted doctor report")
	fmt.Fprintln(a.stdout, "  --workspace <path>        host workspace (default: current directory)")
	fmt.Fprintln(a.stdout, "  --guest-workspace <path>  guest workspace path")
	fmt.Fprintln(a.stdout, "  --ephemeral               diagnose session-local identity state")
	fmt.Fprintln(a.stdout, "  --fix                     apply safe initialization repairs")
	fmt.Fprintln(a.stdout, "  --dry-run                 print the fix plan without applying it")
	fmt.Fprintln(a.stdout, "  --apply                   apply the safe fix plan")
}

func isHelpToken(value string) bool {
	return value == "help" || value == "-h" || value == "--help"
}

func containsHelpToken(values []string) bool {
	return slices.ContainsFunc(values, isHelpToken)
}

func (a app) profileUsage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  hideout profile init <name>")
	fmt.Fprintln(a.stdout, "  hideout profile clone <source> <name>")
	fmt.Fprintln(a.stdout, "  hideout profile rotate-identity <name>")
	fmt.Fprintln(a.stdout, "  hideout profile reset <name>")
	fmt.Fprintln(a.stdout, "  hideout profile path <name>")
	fmt.Fprintln(a.stdout, "  hideout profile fs <name> <list|add|deny|remove>")
	fmt.Fprintln(a.stdout, "  hideout profile env <name> <list|set|unset|inherit|uninherit|deny|undeny>")
	fmt.Fprintln(a.stdout, "  hideout profile home <name> import --from <path> --to <relative-path> [--force]")
	fmt.Fprintln(a.stdout, "  hideout profile tools <name> <list|expected>")
	fmt.Fprintln(a.stdout, "  hideout profile command-proxy <name> <list|add-open|remove>")
	fmt.Fprintln(a.stdout, "  hideout profile command-adapter <name> <list|add-local|add-builtin-root-sensitive|enable|disable|refresh-digest|remove>")
}

func (a app) profileFSUsage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  hideout profile fs <name> list")
	fmt.Fprintln(a.stdout, "  hideout profile fs <name> add --fs <kind:/path> [--reason <text>]")
	fmt.Fprintln(a.stdout, "  hideout profile fs <name> deny --no-fs <kind:/path> [--reason <text>]")
	fmt.Fprintln(a.stdout, "  hideout profile fs <name> remove <rule-id>")
}

func (a app) profileToolsUsage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  hideout profile tools <name> list")
	fmt.Fprintln(a.stdout, "  hideout profile tools <name> expected add <command>")
	fmt.Fprintln(a.stdout, "  hideout profile tools <name> expected remove <command>")
	fmt.Fprintln(a.stdout, "  hideout profile tools <name> expected list")
}

func (a app) profileCommandProxyUsage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  hideout profile command-proxy <name> list")
	fmt.Fprintln(a.stdout, "  hideout profile command-proxy <name> add-open <command>")
	fmt.Fprintln(a.stdout, "  hideout profile command-proxy <name> remove <command>")
}

func (a app) profileCommandAdapterUsage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  hideout profile command-adapter <name> list")
	fmt.Fprintln(a.stdout, "  hideout profile command-adapter <name> add-local --id <id> --path <path> --command <command> [--capability <capability>]")
	fmt.Fprintln(a.stdout, "  hideout profile command-adapter <name> add-builtin-root-sensitive [--id <id>]")
	fmt.Fprintln(a.stdout, "  hideout profile command-adapter <name> enable <id>")
	fmt.Fprintln(a.stdout, "  hideout profile command-adapter <name> disable <id>")
	fmt.Fprintln(a.stdout, "  hideout profile command-adapter <name> refresh-digest <id>")
	fmt.Fprintln(a.stdout, "  hideout profile command-adapter <name> remove <id>")
}

func (a app) adapterPackUsage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  hideout adapter-pack install --path <dir>")
	fmt.Fprintln(a.stdout, "  hideout adapter-pack install --git <url> --commit <sha>")
	fmt.Fprintln(a.stdout, "  hideout adapter-pack list")
	fmt.Fprintln(a.stdout, "  hideout adapter-pack inspect <pack-id>")
	fmt.Fprintln(a.stdout, "  hideout adapter-pack test [--revision <id>] <pack-id>")
	fmt.Fprintln(a.stdout, "  hideout adapter-pack enable --profile <name> --pack <id> --revision <id> --adapter <id> [--id <command-adapter-id>] [--command <cmd>] [--capability <capability>]")
	fmt.Fprintln(a.stdout, "  hideout adapter-pack disable --profile <name> <command-adapter-id>")
	fmt.Fprintln(a.stdout, "  hideout adapter-pack upgrade --path <dir>")
	fmt.Fprintln(a.stdout, "  hideout adapter-pack revoke <pack-id>")
}

func (a app) profileEnvUsage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  hideout profile env <name> list")
	fmt.Fprintln(a.stdout, "  hideout profile env <name> set KEY=VALUE")
	fmt.Fprintln(a.stdout, "  hideout profile env <name> unset KEY")
	fmt.Fprintln(a.stdout, "  hideout profile env <name> inherit KEY")
	fmt.Fprintln(a.stdout, "  hideout profile env <name> uninherit KEY")
	fmt.Fprintln(a.stdout, "  hideout profile env <name> deny PATTERN")
	fmt.Fprintln(a.stdout, "  hideout profile env <name> undeny PATTERN")
}

func (a app) profileHomeUsage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  hideout profile home <name> import --from <path> --to <relative-path> [--force]")
}

func (a app) packageUsage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  hideout package verify <package-root-or-install-prefix>")
	fmt.Fprintln(a.stdout, "  hideout package install <package-root> --prefix <dir> [--store <dir>] [--backend native|lima|auto] [--network direct|tun2socks] [--proxy-secret <ref>] [--mediated-resolver <ip>] [--skip-init]")
	fmt.Fprintln(a.stdout, "  hideout package uninstall --prefix <dir> [--store <dir>] [--dry-run] [--purge]")
}

func (a app) packageCommand(args []string) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		a.packageUsage()
		return nil
	}
	switch args[0] {
	case "verify":
		if len(args) == 2 && isHelpToken(args[1]) {
			a.packageUsage()
			return nil
		}
		if len(args) != 2 {
			return errors.New("usage: hideout package verify <package-root-or-install-prefix>")
		}
		result, err := packagekit.Verify(args[1])
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "package: ok mode=%s root=%s files=%d\n", result.Mode, result.Root, result.Files)
		return nil
	case "install":
		return a.packageInstall(args[1:])
	case "uninstall":
		return a.packageUninstall(args[1:])
	default:
		return fmt.Errorf("unknown package command %q", args[0])
	}
}

func (a app) packageInstall(args []string) error {
	if containsHelpToken(args) {
		a.packageUsage()
		return nil
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return errors.New("usage: hideout package install <package-root> --prefix <dir> [--store <dir>]")
	}
	packageRoot := args[0]
	opts := struct {
		prefix      string
		store       string
		backend     string
		network     string
		proxySecret string
		resolver    string
		skipInit    bool
	}{backend: "auto", network: "direct", resolver: "1.1.1.1"}
	fs := flag.NewFlagSet("package install", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.prefix, "prefix", "", "install prefix")
	fs.StringVar(&opts.store, "store", "", "durable store root")
	fs.StringVar(&opts.backend, "backend", "auto", "backend for init")
	fs.StringVar(&opts.network, "network", "direct", "network mode for init")
	fs.StringVar(&opts.proxySecret, "proxy-secret", "", "proxy secret ref")
	fs.StringVar(&opts.resolver, "mediated-resolver", "1.1.1.1", "mediated DNS resolver IP for tun2socks init")
	fs.BoolVar(&opts.skipInit, "skip-init", false, "skip typed init after install")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: hideout package install <package-root> --prefix <dir> [--store <dir>]")
	}
	if opts.store == "" {
		opts.store = os.Getenv("HIDEOUT_STORE_ROOT")
	}
	if opts.store == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		opts.store = filepath.Join(home, ".hideout")
	}
	result, err := packagekit.Install(packagekit.InstallOptions{
		PackageRoot: packageRoot,
		Prefix:      opts.prefix,
		StoreRoot:   opts.store,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "package: %s prefix=%s store=%s files=%d manifest=%s\n", result.Operation, result.Prefix, result.StoreRoot, result.FilesCopied, result.ManifestPath)
	if !opts.skipInit {
		templateID := profiletemplate.Dev
		if opts.network == "tun2socks" {
			templateID = profiletemplate.Privacy
		}
		initArgs := []string{
			"init",
			"--no-input",
			"--profile", "default",
			"--template", templateID,
			"--backend", opts.backend,
			"--network", opts.network,
		}
		if opts.proxySecret != "" {
			initArgs = append(initArgs, "--proxy-secret", opts.proxySecret)
		}
		if opts.network == "tun2socks" {
			initArgs = append(initArgs, "--mediated-resolver", opts.resolver)
		}
		priorStore := os.Getenv("HIDEOUT_STORE_ROOT")
		if err := os.Setenv("HIDEOUT_STORE_ROOT", result.StoreRoot); err != nil {
			return err
		}
		defer func() {
			if priorStore == "" {
				_ = os.Unsetenv("HIDEOUT_STORE_ROOT")
			} else {
				_ = os.Setenv("HIDEOUT_STORE_ROOT", priorStore)
			}
		}()
		if err := a.initCommand(initArgs[1:]); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(a.stdout, "package: init skipped")
	}
	return nil
}

func (a app) packageUninstall(args []string) error {
	if containsHelpToken(args) {
		a.packageUsage()
		return nil
	}
	opts := struct {
		prefix string
		store  string
		dryRun bool
		purge  bool
	}{}
	fs := flag.NewFlagSet("package uninstall", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.prefix, "prefix", "", "install prefix")
	fs.StringVar(&opts.store, "store", "", "durable store root")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "print uninstall plan without removing files")
	fs.BoolVar(&opts.purge, "purge", false, "remove durable store state")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || opts.prefix == "" {
		return errors.New("usage: hideout package uninstall --prefix <dir> [--store <dir>] [--dry-run] [--purge]")
	}
	result, err := packagekit.Uninstall(packagekit.UninstallOptions{
		Prefix: opts.prefix,
		Store:  opts.store,
		DryRun: opts.dryRun,
		Purge:  opts.purge,
	})
	if err != nil {
		return err
	}
	action := "uninstall"
	if result.DryRun {
		action = "uninstall dry-run"
	}
	fmt.Fprintf(a.stdout, "package: %s prefix=%s files=%d durableState=%s\n", action, result.Prefix, len(result.Files), result.DurableAction)
	for _, rel := range result.Files {
		fmt.Fprintf(a.stdout, "remove %s\n", rel)
	}
	if result.Purge {
		fmt.Fprintf(a.stdout, "purge store=%s\n", result.StoreRoot)
	}
	return nil
}

func (a app) shimUsage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  hideout shim build-linux [--out <path>] [--goarch <arch>] [--source <repo>]")
	fmt.Fprintln(a.stdout, "  hideout shim <open-like-command> [args...]")
}

func (a app) hostfsdUsage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  hideout hostfsd build-linux [--out <path>] [--goarch <arch>] [--source <repo>]")
}

func (a app) labUsage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  hideout lab portbridge loopback --enable-lab --target 127.0.0.1:<port>")
	fmt.Fprintln(a.stdout, "  hideout lab portbridge guest-to-host --enable-lab --target 127.0.0.1:<port>")
	fmt.Fprintln(a.stdout, "  hideout lab portbridge host-to-guest --enable-lab --guest-target 127.0.0.1:<port>")
	fmt.Fprintln(a.stdout, "  hideout lab browser-control --enable-lab --profile <name>")
	fmt.Fprintln(a.stdout, "  hideout lab preview-open --enable-lab --guest-url http://127.0.0.1:<port>")
}

func (a app) labPortbridgeUsage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  hideout lab portbridge loopback --enable-lab --target 127.0.0.1:<port>")
	fmt.Fprintln(a.stdout, "  hideout lab portbridge guest-to-host --enable-lab --target 127.0.0.1:<port>")
	fmt.Fprintln(a.stdout, "  hideout lab portbridge host-to-guest --enable-lab --guest-target 127.0.0.1:<port>")
}

type initCommandOptions struct {
	profileName           string
	backendName           string
	networkMode           string
	proxySecret           string
	mediatedResolver      string
	templateID            string
	privilegeStatus       string
	privilegeReason       string
	privilegeGuidance     string
	privilegeSource       string
	allowDegradedTemplate bool
	explicitProfile       bool
	explicitTemplate      bool
	explicitBackend       bool
	explicitNetwork       bool
	noInput               bool
	dryRun                bool
	tools                 toolSupplyOptions
}

type toolSupplyOptions struct {
	presets     stringListFlag
	npmPackage  string
	npmCommands stringListFlag
}

func (a app) initCommand(args []string) error {
	if containsHelpToken(args) {
		a.initUsage()
		return nil
	}
	opts, err := parseInitCommandOptions(args)
	if err != nil {
		return err
	}
	var initInput *bufio.Reader
	if !opts.noInput {
		initInput = bufio.NewReader(a.stdin)
		if err := a.collectInteractiveInitOptions(&opts, initInput); err != nil {
			return err
		}
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	core := manager.New(store)
	plan, err := core.PlanInit(inittask.Options{
		ProfileName:           opts.profileName,
		Backend:               opts.backendName,
		Network:               opts.networkMode,
		ProxySecretRef:        opts.proxySecret,
		MediatedResolver:      opts.mediatedResolver,
		TemplateID:            opts.templateID,
		PrivilegeStatus:       opts.privilegeStatus,
		PrivilegeReason:       opts.privilegeReason,
		PrivilegeGuidance:     opts.privilegeGuidance,
		PrivilegeSource:       opts.privilegeSource,
		AllowDegradedTemplate: opts.allowDegradedTemplate,
		Onboarding:            true,
		ExplicitProfile:       opts.explicitProfile,
		ExplicitTemplate:      opts.explicitTemplate,
		ExplicitBackend:       opts.explicitBackend,
		ExplicitNetwork:       opts.explicitNetwork,
		NoInput:               opts.noInput,
	})
	if err != nil {
		return err
	}
	if opts.dryRun {
		writeInitPlan(a.stdout, "Hideout init plan", plan)
		return nil
	}
	if !opts.noInput {
		writeInitReview(a.stdout, plan)
		confirmed, err := a.confirmInit(initInput)
		if err != nil {
			return err
		}
		if !confirmed {
			return errors.New("init cancelled")
		}
	}
	result, err := core.ApplyInit(plan, inittask.ApplyOptions{
		NoInput: opts.noInput,
	})
	if err != nil {
		return err
	}
	writeInitResult(a.stdout, "Hideout init", result)
	return nil
}

func parseInitCommandOptions(args []string) (initCommandOptions, error) {
	opts := initCommandOptions{profileName: "default", backendName: "auto"}
	explicit := explicitLongFlags(args)
	opts.explicitProfile = explicit["profile"]
	opts.explicitTemplate = explicit["template"]
	opts.explicitBackend = explicit["backend"]
	opts.explicitNetwork = explicit["network"]
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.profileName, "profile", "default", "profile name")
	fs.StringVar(&opts.templateID, "template", "", "profile template: privacy, hardened, dev, or debug")
	fs.StringVar(&opts.backendName, "backend", "auto", "backend: auto/lima for isolation; native is a dev-only weak harness")
	fs.StringVar(&opts.networkMode, "network", "", "network mode")
	fs.StringVar(&opts.proxySecret, "proxy-secret", "", "proxy secret ref for tun2socks network mode")
	fs.StringVar(&opts.mediatedResolver, "mediated-resolver", "", "mediated DNS resolver IP for tun2socks")
	fs.StringVar(&opts.privilegeStatus, "privilege-status", "", "guest privilege status: enforced, degraded, or unknown")
	fs.StringVar(&opts.privilegeReason, "privilege-reason", "", "guest privilege status reason")
	fs.StringVar(&opts.privilegeGuidance, "privilege-guidance", "", "guest privilege status guidance")
	fs.StringVar(&opts.privilegeSource, "privilege-source", "", "guest privilege status source")
	fs.BoolVar(&opts.allowDegradedTemplate, "allow-degraded-template", false, "allow visibly degraded hardened fallback")
	registerToolSupplyFlags(fs, &opts.tools)
	fs.BoolVar(&opts.noInput, "no-input", false, "do not ask for confirmation")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "print init plan without applying")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() != 0 {
		return opts, fmt.Errorf("unexpected init argument %q", fs.Arg(0))
	}
	if err := opts.tools.validate(); err != nil {
		return opts, err
	}
	return opts, nil
}

func explicitLongFlags(args []string) map[string]bool {
	out := map[string]bool{}
	for _, arg := range args {
		if !strings.HasPrefix(arg, "--") || arg == "--" {
			continue
		}
		name := strings.TrimPrefix(arg, "--")
		if before, _, ok := strings.Cut(name, "="); ok {
			name = before
		}
		out[name] = true
	}
	return out
}

func (a app) collectInteractiveInitOptions(opts *initCommandOptions, reader *bufio.Reader) error {
	if strings.TrimSpace(opts.templateID) == "" {
		opts.templateID = profiletemplate.Recommended().ID
	}
	tmpl, ok := profiletemplate.Lookup(opts.templateID)
	if !ok {
		return fmt.Errorf("unsupported profile template %q", opts.templateID)
	}
	if strings.TrimSpace(opts.backendName) == "" || opts.backendName == "auto" {
		opts.backendName = "lima"
	}
	if strings.TrimSpace(opts.networkMode) == "" {
		opts.networkMode = tmpl.DefaultNetwork
	}
	if opts.networkMode == "tun2socks" {
		if strings.TrimSpace(opts.proxySecret) == "" {
			value, err := a.promptLine(reader, "Proxy secret ref")
			if err != nil {
				return err
			}
			opts.proxySecret = value
		}
		if strings.TrimSpace(opts.mediatedResolver) == "" {
			value, err := a.promptLine(reader, "Mediated resolver IP")
			if err != nil {
				return err
			}
			opts.mediatedResolver = value
		}
	}
	if opts.templateID == profiletemplate.Hardened && strings.TrimSpace(opts.privilegeStatus) == "" {
		value, err := a.promptLine(reader, "Privilege status [enforced/degraded/unknown]")
		if err != nil {
			return err
		}
		opts.privilegeStatus = value
	}
	opts.explicitProfile = true
	opts.explicitTemplate = true
	opts.explicitBackend = true
	opts.explicitNetwork = true
	return nil
}

func (a app) promptLine(reader *bufio.Reader, label string) (string, error) {
	fmt.Fprintf(a.stdout, "%s: ", label)
	value, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	return value, nil
}

func (a app) confirmInit(reader *bufio.Reader) (bool, error) {
	fmt.Fprint(a.stdout, "Create profile? [y/N]: ")
	value, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "y" || value == "yes", nil
}

func writeInitPlan(w io.Writer, title string, plan inittask.Plan) {
	fmt.Fprintln(w, title)
	fmt.Fprintf(w, "storage: %s\n", plan.StoreRoot)
	fmt.Fprintf(w, "profile: %s\n", plan.Profile)
	fmt.Fprintf(w, "backend: %s\n", plan.Backend)
	fmt.Fprintf(w, "network: %s\n", plan.Network)
	writeInitTemplateSummary(w, plan)
	for _, task := range plan.Tasks {
		fmt.Fprintf(w, "task %s: %s risk=%s %s\n", task.Kind, task.Status, task.Risk, task.Message)
	}
}

func writeInitResult(w io.Writer, title string, result inittask.Result) {
	fmt.Fprintln(w, title)
	fmt.Fprintf(w, "storage: %s\n", result.Plan.StoreRoot)
	fmt.Fprintf(w, "profile: %s\n", result.Plan.Profile)
	fmt.Fprintf(w, "backend: %s\n", result.Plan.Backend)
	fmt.Fprintf(w, "network: %s\n", result.Plan.Network)
	writeInitTemplateSummary(w, result.Plan)
	if result.AuditPath != "" {
		fmt.Fprintf(w, "audit=%s\n", result.AuditPath)
	}
	if result.EvidencePath != "" {
		fmt.Fprintf(w, "evidence=%s\n", result.EvidencePath)
	}
	for _, task := range result.Applied {
		fmt.Fprintf(w, "task %s: applied risk=%s %s\n", task.Kind, task.Risk, task.Message)
	}
	for _, task := range result.Skipped {
		fmt.Fprintf(w, "task %s: %s risk=%s %s\n", task.Kind, task.Status, task.Risk, task.Message)
	}
	writeInitNextSteps(w, result.Plan)
}

func writeInitTemplateSummary(w io.Writer, plan inittask.Plan) {
	if plan.TemplateID == "" {
		return
	}
	fmt.Fprintf(w, "template: %s\n", plan.TemplateID)
	fmt.Fprintf(w, "posture: %s\n", plan.EffectivePosture)
	if plan.MediatedResolver != "" {
		fmt.Fprintf(w, "mediatedResolver: %s\n", plan.MediatedResolver)
	}
	if plan.PrivilegeStatus != "" {
		fmt.Fprintf(w, "privilege: %s\n", plan.PrivilegeStatus)
	}
	if plan.EvidencePath != "" {
		fmt.Fprintf(w, "evidencePlan: %s\n", plan.EvidencePath)
	}
	for _, warning := range plan.Warnings {
		fmt.Fprintf(w, "warning: %s\n", warning)
	}
	for _, nonClaim := range plan.NonClaims {
		fmt.Fprintf(w, "non-claim: %s\n", nonClaim)
	}
}

func writeInitReview(w io.Writer, plan inittask.Plan) {
	if len(plan.ReviewLines) == 0 {
		return
	}
	fmt.Fprintln(w, "Review:")
	for _, line := range plan.ReviewLines {
		fmt.Fprintf(w, "  %s\n", line)
	}
}

func writeInitNextSteps(w io.Writer, plan inittask.Plan) {
	if len(plan.NextSteps) == 0 {
		return
	}
	fmt.Fprintln(w, "next:")
	for _, step := range plan.NextSteps {
		if step.Command == "" {
			continue
		}
		if step.ID == "resolve-blocked" {
			fmt.Fprintf(w, "  resolve: %s\n", step.Command)
			continue
		}
		if step.ID == "doctor-check" {
			fmt.Fprintf(w, "  check: %s\n", step.Command)
			continue
		}
		if step.ID == "smoke-run" {
			fmt.Fprintf(w, "  smoke: %s\n", step.Command)
			continue
		}
		if step.ID == "cli-smoke" {
			fmt.Fprintf(w, "  cli: %s\n", step.Command)
			continue
		}
		label := strings.TrimSpace(step.Label)
		if label == "" {
			label = step.ID
		}
		fmt.Fprintf(w, "  %s: %s\n", label, step.Command)
	}
}

type runOptions struct {
	profileName           string
	backendName           string
	networkMode           string
	proxySecret           string
	mediatedResolver      string
	workspace             string
	guestWorkspace        string
	auditPath             string
	allowWeakIsolation    bool
	allowUnsafeWorkspace  bool
	explainOnly           bool
	verbose               bool
	ephemeral             bool
	envName               string
	removeEnvironment     bool
	hostFSGrantFlags      []string
	hostFSDenyFlags       []string
	noProfileHostFSGrants bool
	hostFSRun             hostfs.Config
	envPublic             map[string]string
	previewTargets        []string
	command               []string
}

type runEnvironment = manager.RunEnvironment

func (a app) runCommand(args []string, explainOnly bool) (retErr error) {
	if runHelpRequested(args) {
		if explainOnly {
			a.runUsage("explain")
		} else {
			a.runUsage("run")
		}
		return nil
	}
	opts, err := parseRunOptions(args, explainOnly)
	if err != nil {
		return err
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	core := manager.New(store)
	runPlan, err := core.PlanRun(manager.RunPlanOptions{
		ProfileName:          opts.profileName,
		Backend:              opts.backendName,
		NetworkMode:          opts.networkMode,
		ProxySecretRef:       opts.proxySecret,
		MediatedResolver:     opts.mediatedResolver,
		Workspace:            opts.workspace,
		GuestWorkspace:       opts.guestWorkspace,
		AllowUnsafeWorkspace: opts.allowUnsafeWorkspace,
		Ephemeral:            opts.ephemeral,
		Command:              opts.command,
	})
	if err != nil {
		return err
	}
	runtimeProfile := runPlan.RuntimeProfile
	if len(opts.envPublic) > 0 {
		if runtimeProfile.Env.Public == nil {
			runtimeProfile.Env.Public = map[string]string{}
		}
		for name, value := range opts.envPublic {
			runtimeProfile.Env.Public[name] = value
		}
		if err := runtimeProfile.Validate(); err != nil {
			return err
		}
		runPlan.RuntimeProfile = runtimeProfile
	}
	openTargets, endpointCandidates, endpointExposures, err := buildPreviewOpenOptions(runtimeProfile, opts.previewTargets)
	if err != nil {
		return err
	}
	opts.workspace = runPlan.Workspace
	opts.guestWorkspace = runPlan.GuestWorkspace
	a.warnShadowedHostFSRules(runtimeProfile.HostFS, runPlan.Workspace)
	backendName := runPlan.Backend
	if opts.explainOnly {
		return core.ExplainRun(runPlan, manager.RunExplainOptions{
			Environment: manager.RunEnvironmentOptions{
				EnvName:        opts.envName,
				RemoveAfterRun: opts.removeEnvironment,
			},
		}, func(explanation manager.RunExplanation) error {
			runSession := explanation.Session
			explain := explainText(runtimeProfile, opts, runSession.Layout, runSession.Environment, runSession.Env, runSession.ProfileDir, runSession.IdentityDir)
			fmt.Fprint(a.stdout, explain)
			return nil
		})
	}
	be := a.backend(backendName, opts)
	runCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	result, err := core.ApplyRun(runCtx, runPlan, manager.ApplyRunOptions{
		Backend:                    be,
		RequestedBackend:           opts.backendName,
		AllowWeakIsolation:         opts.allowWeakIsolation,
		Environment:                manager.RunEnvironmentOptions{EnvName: opts.envName, RemoveAfterRun: opts.removeEnvironment, Create: true},
		AuditPath:                  opts.auditPath,
		HostFSRun:                  opts.hostFSRun,
		DisableProfileHostFSGrants: opts.noProfileHostFSGrants,
		OpenTargets:                openTargets,
		EndpointCandidates:         endpointCandidates,
		EndpointExposures:          endpointExposures,
		OpenerForSession: func(runSession manager.RunSession) broker.Opener {
			return hostOpener(runSession.IdentityDir, a.stdout, a.stderr)
		},
	})
	if err != nil {
		return err
	}
	if opts.verbose {
		a.writeRunResultSummary(result)
	}
	return nil
}

func (a app) writeRunResultSummary(result manager.RunResult) {
	if result.BoundarySummary == nil && !result.PreserveInstance {
		return
	}
	if result.EnvironmentID != "" {
		fmt.Fprintf(a.stderr, "Hideout environment: %s\n", result.EnvironmentID)
		if result.EnvironmentName != "" {
			fmt.Fprintf(a.stderr, "Hideout environment name: %s\n", result.EnvironmentName)
		}
		if result.PreserveInstance {
			handle := result.EnvironmentName
			if handle == "" {
				handle = result.EnvironmentID
			}
			if result.EnvironmentName != "" {
				fmt.Fprintf(a.stderr, "run again: hideout run --env %s -- <command>\n", result.EnvironmentName)
			}
			fmt.Fprintf(a.stderr, "stop: hideout stop %s\n", handle)
			fmt.Fprintf(a.stderr, "clean-after-stop: hideout clean --stopped %s\n", handle)
		}
	}
	if result.BoundarySummary == nil {
		return
	}
	fmt.Fprintln(a.stderr, "Hideout boundary:")
	if result.BoundarySummary.Evidence == "disabled" {
		fmt.Fprintln(a.stderr, "  audit: disabled - no boundary evidence")
		return
	}
	if result.BoundarySummary.Evidence == "unavailable" {
		fmt.Fprintln(a.stderr, "  audit: unavailable - no boundary evidence")
		return
	}
	if result.BoundarySummary.AuditPath != "" {
		fmt.Fprintf(a.stderr, "  audit: %s\n", result.BoundarySummary.AuditPath)
	}
	if privilege := result.BoundarySummary.Privilege; privilege != nil {
		fmt.Fprintf(a.stderr, "  privilege: status=%s", dash(privilege.Status))
		if privilege.TargetUID != "" {
			fmt.Fprintf(a.stderr, " targetUID=%s", privilege.TargetUID)
		}
		if privilege.SetupKind != "" {
			fmt.Fprintf(a.stderr, " setup=%s", privilege.SetupKind)
		}
		if privilege.Reason != "" {
			fmt.Fprintf(a.stderr, " reason=%s", privilege.Reason)
		}
		if privilege.NonClaim != "" {
			fmt.Fprintf(a.stderr, " nonClaim=%s", privilege.NonClaim)
		}
		fmt.Fprintln(a.stderr)
	}
	for _, capability := range result.BoundarySummary.Capabilities {
		fmt.Fprintf(a.stderr, "  %s: allowed=%d denied=%d", capability.Capability, capability.Allowed, capability.Denied)
		if capability.Capability == "hostfs" || capability.Unsupported > 0 {
			fmt.Fprintf(a.stderr, " unsupported=%d", capability.Unsupported)
		}
		if capability.Error > 0 {
			fmt.Fprintf(a.stderr, " error=%d", capability.Error)
		}
		if capability.AuditOnly > 0 {
			fmt.Fprintf(a.stderr, " auditOnly=%d", capability.AuditOnly)
		}
		if capability.Owner != "" {
			fmt.Fprintf(a.stderr, " owner=%s", capability.Owner)
		}
		if capability.Source != "" {
			fmt.Fprintf(a.stderr, " source=%s", capability.Source)
		}
		if capability.Lifetime != "" {
			fmt.Fprintf(a.stderr, " lifetime=%s", capability.Lifetime)
		}
		if capability.CloseReason != "" {
			fmt.Fprintf(a.stderr, " close=%s", capability.CloseReason)
		}
		if capability.EndpointCategory != "" {
			fmt.Fprintf(a.stderr, " endpoint=%s", capability.EndpointCategory)
		}
		fmt.Fprintln(a.stderr)
	}
}

func cleanupAuditDetails(result session.CleanupResult) map[string]any {
	return manager.CleanupAuditDetails(result)
}

func cleanupAuditType(path string) string {
	return manager.CleanupAuditType(path)
}

func presence(value string) string {
	if value == "" {
		return "absent"
	}
	return "present"
}

func doctorGuestPrivilegeMessage(ctx context.Context, store profile.Store, profileName string) string {
	overview, err := manager.New(store).Overview(ctx)
	if err != nil {
		return "status unavailable: " + err.Error()
	}
	for i := len(overview.Sessions) - 1; i >= 0; i-- {
		session := overview.Sessions[i]
		if profileName != "" && session.Profile != "" && session.Profile != profileName {
			continue
		}
		if session.GuestPrivilege == nil {
			continue
		}
		return "latest=" + privilegeForTUI(session.GuestPrivilege)
	}
	return "recorded per run; no guest privilege evidence found yet"
}

func runtimeIdentityDir(layout session.Layout, profileDir string, opts runOptions) string {
	return manager.RunIdentityDir(layout, profileDir, opts.ephemeral)
}

func selectRunEnvironment(store environment.Store, p profile.Profile, backendName string, opts runOptions, create bool) (runEnvironment, error) {
	return manager.SelectRunEnvironment(store, p, backendName, opts.workspace, opts.guestWorkspace, opts.ephemeral, manager.RunEnvironmentOptions{
		EnvName:        opts.envName,
		RemoveAfterRun: opts.removeEnvironment,
		Create:         create,
	})
}

func runEnvironmentSpec(p profile.Profile, backendName string, opts runOptions) environment.Spec {
	return manager.RunEnvironmentSpec(p, backendName, opts.workspace, opts.guestWorkspace)
}

func validateEnvironmentRecord(rec environment.Record, spec environment.Spec) error {
	return manager.ValidateEnvironmentRecord(rec, spec)
}

func resolveBackendName(name string) string {
	return manager.ResolveBackendName(name)
}

func resolveWorkspaceMapping(hostWorkspace, guestWorkspace string, p profile.Profile) (string, string, error) {
	return manager.ResolveWorkspaceMapping(hostWorkspace, guestWorkspace, p)
}

func networkDecision(plan netpolicy.Plan, err error) string {
	return manager.NetworkDecision(plan, err)
}

func guestSessionDirForBackend(backendName string) string {
	return manager.GuestSessionDirForBackend(backendName)
}

func localBypassHostsForBackend(backendName string) []string {
	return manager.LocalBypassHostsForBackend(backendName)
}

func brokerEndpointForBackend(backendName string, layout session.Layout) broker.Endpoint {
	return manager.BrokerEndpointForBackend(backendName, layout)
}

func brokerEndpointForGuest(backendName string, listen broker.Endpoint) (broker.Endpoint, error) {
	return manager.BrokerEndpointForGuest(backendName, listen)
}

func brokerEndpointForDoctorClient(endpoint broker.Endpoint) broker.Endpoint {
	if endpoint.Network != broker.EndpointTCP {
		return endpoint
	}
	host, port, err := net.SplitHostPort(endpoint.Address)
	if err != nil {
		return endpoint
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		return broker.TCPEndpoint(net.JoinHostPort("127.0.0.1", port))
	}
	return endpoint
}

func appendBrokerEnv(env []string, endpoint broker.Endpoint, sessionID, token, socket string) []string {
	return manager.AppendBrokerEnv(env, endpoint, sessionID, token, socket)
}

func (a app) backend(name string, opts runOptions) backend.Backend {
	switch name {
	case "lima":
		controlOut := io.Discard
		controlErr := io.Discard
		if opts.verbose {
			controlOut = a.stdout
			controlErr = a.stderr
		}
		return lima.Backend{
			Stdout:        a.stdout,
			Stderr:        a.stderr,
			ControlStdout: controlOut,
			ControlStderr: controlErr,
			Stdin:         os.Stdin,
		}
	default:
		return native.Backend{
			AllowWeakIsolation: opts.allowWeakIsolation,
			Stdout:             a.stdout,
			Stderr:             a.stderr,
			Stdin:              os.Stdin,
		}
	}
}

func (a app) environmentOperator(verbose bool) manager.EnvironmentOperator {
	controlOut := io.Discard
	controlErr := io.Discard
	if verbose {
		controlOut = a.stdout
		controlErr = a.stderr
	}
	return lima.Backend{
		Stdout:        io.Discard,
		Stderr:        io.Discard,
		ControlStdout: controlOut,
		ControlStderr: controlErr,
	}
}

func hostFSGrafts(policy hostfs.EffectivePolicy) []string {
	return manager.HostFSGrafts(policy)
}

func hostFSProfileForRun(p profile.Profile, opts runOptions) hostfs.Config {
	return manager.HostFSProfileForRun(p, opts.noProfileHostFSGrants)
}

func hostOpener(profileDir string, stdout, stderr io.Writer) hostopen.Opener {
	return hostopen.Opener{
		BrowserProfileDir: hostopen.BrowserProfileDir(profileDir),
		BrowserPath:       os.Getenv("HIDEOUT_BROWSER_PATH"),
		BrowserApp:        os.Getenv("HIDEOUT_BROWSER_APP"),
		DryRun:            os.Getenv("HIDEOUT_OPEN_DRY_RUN") == "1",
		Stdout:            stdout,
		Stderr:            stderr,
	}
}

func writeBrokerEndpoint(path string, endpoint broker.Endpoint) error {
	return manager.WriteBrokerEndpoint(path, endpoint)
}

func parseRunOptions(args []string, explainOnly bool) (runOptions, error) {
	opts := runOptions{profileName: "default", backendName: "auto", explainOnly: explainOnly}
	split := slices.Index(args, "--")
	flagArgs := args
	if split >= 0 {
		flagArgs = args[:split]
		opts.command = args[split+1:]
	}
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.profileName, "profile", "default", "profile name")
	fs.StringVar(&opts.backendName, "backend", "auto", "backend")
	fs.StringVar(&opts.networkMode, "network", "", "network mode")
	fs.StringVar(&opts.proxySecret, "proxy-secret", "", "proxy secret ref")
	fs.StringVar(&opts.mediatedResolver, "mediated-resolver", "", "tun2socks mediated DNS resolver IP")
	fs.StringVar(&opts.workspace, "workspace", "", "host workspace")
	fs.StringVar(&opts.guestWorkspace, "guest-workspace", "", "guest workspace")
	fs.StringVar(&opts.auditPath, "audit", "", "audit path or off")
	fs.BoolVar(&opts.allowWeakIsolation, "allow-weak-isolation", false, "allow native weak isolation")
	fs.BoolVar(&opts.allowUnsafeWorkspace, "allow-unsafe-workspace", false, "explicitly allow mounting a sensitive workspace root")
	fs.BoolVar(&opts.explainOnly, "explain", opts.explainOnly, "print the run boundary without executing the command")
	fs.BoolVar(&opts.verbose, "verbose", false, "print Hideout control-plane progress and run summary")
	fs.BoolVar(&opts.ephemeral, "ephemeral", false, "use session-local identity state for this run")
	fs.StringVar(&opts.envName, "env", "", "run inside the named environment")
	fs.BoolVar(&opts.removeEnvironment, "rm", false, "remove the runtime environment after the command")
	var fsFlags stringListFlag
	fs.Var(&fsFlags, "fs", "run-scoped HostFS allow rule such as read:/absolute/path")
	var noFSFlags stringListFlag
	fs.Var(&noFSFlags, "no-fs", "run-scoped HostFS deny rule such as read:/absolute/path")
	fs.BoolVar(&opts.noProfileHostFSGrants, "no-profile-fs", false, "ignore profile HostFS grants for this run")
	var envFlags stringListFlag
	fs.Var(&envFlags, "env-var", "run-scoped public environment variable KEY=VALUE")
	var previewFlags stringListFlag
	fs.Var(&previewFlags, "preview", "open a preview for a profile endpoint candidate id or guest loopback endpoint")
	if err := fs.Parse(flagArgs); err != nil {
		return opts, err
	}
	grantInputs := appendHostFSFlagInputs(nil, "--fs", fsFlags, "run-scoped CLI allow")
	denyInputs := appendHostFSFlagInputs(nil, "--no-fs", noFSFlags, "run-scoped CLI deny")
	opts.hostFSGrantFlags = hostFSFlagValues(grantInputs)
	opts.hostFSDenyFlags = hostFSFlagValues(denyInputs)
	hostFSRun, err := parseHostFSRunPolicyFlags(grantInputs, denyInputs)
	if err != nil {
		return opts, err
	}
	opts.hostFSRun = hostFSRun
	envPublic, err := parseRunEnvFlags(envFlags)
	if err != nil {
		return opts, err
	}
	opts.envPublic = envPublic
	opts.previewTargets = append([]string(nil), previewFlags...)
	if opts.ephemeral && strings.TrimSpace(opts.envName) != "" {
		return opts, errors.New("--ephemeral cannot be used with --env")
	}
	if split < 0 && fs.NArg() > 0 {
		opts.command = fs.Args()
	}
	return opts, nil
}

func runHelpRequested(args []string) bool {
	split := slices.Index(args, "--")
	if split >= 0 {
		args = args[:split]
	}
	return containsHelpToken(args)
}

func parseRunEnvFlags(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for _, raw := range values {
		name, value, ok := strings.Cut(raw, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, errors.New("--env must use KEY=VALUE")
		}
		name = strings.TrimSpace(name)
		if _, exists := out[name]; exists {
			return nil, fmt.Errorf("--env contains duplicate key %q", name)
		}
		out[name] = value
	}
	return out, nil
}

func buildPreviewOpenOptions(p profile.Profile, targets []string) ([]manager.RunOpenTargetOwner, []manager.RunEndpointCandidate, []manager.RunEndpointExposureRequest, error) {
	if len(targets) == 0 {
		return nil, nil, nil, nil
	}
	owners := []manager.RunOpenTargetOwner{{
		ID:   manager.OpenTargetPreviewOpen,
		Kind: manager.OpenTargetPreviewOpen,
	}}
	profileCandidates := map[string]profile.EndpointCandidate{}
	for _, candidate := range p.EndpointExposure.HostToGuest {
		profileCandidates[strings.TrimSpace(candidate.ID)] = candidate
	}
	var runCandidates []manager.RunEndpointCandidate
	var exposures []manager.RunEndpointExposureRequest
	for i, raw := range targets {
		value := strings.TrimSpace(raw)
		if value == "" {
			return nil, nil, nil, errors.New("--preview cannot be empty")
		}
		candidateID := value
		if candidate, ok := profileCandidates[value]; ok {
			owner := strings.TrimSpace(candidate.Owner)
			if owner != manager.OpenTargetPreviewOpen {
				return nil, nil, nil, fmt.Errorf("--preview candidate %q belongs to owner %q, not preview.open", value, owner)
			}
		} else {
			targetAddress, err := normalizePreviewEndpoint(value)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("--preview %q: %w", value, err)
			}
			candidateID = fmt.Sprintf("manual_preview_%d", i+1)
			runCandidates = append(runCandidates, manager.RunEndpointCandidate{
				ID:            candidateID,
				Source:        manager.EndpointSourceManual,
				Owner:         manager.OpenTargetPreviewOpen,
				Proto:         "tcp",
				TargetAddress: targetAddress,
			})
		}
		exposures = append(exposures, manager.RunEndpointExposureRequest{
			CandidateID: candidateID,
			Owner:       manager.OpenTargetPreviewOpen,
			Kind:        manager.OpenTargetPreviewOpen,
			ClosePolicy: "session-end",
		})
	}
	return owners, runCandidates, exposures, nil
}

func normalizePreviewEndpoint(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("endpoint is required")
	}
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err != nil {
			return "", err
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return "", fmt.Errorf("preview URL scheme %q is unsupported", u.Scheme)
		}
		value = u.Host
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return "", fmt.Errorf("must be host:port or http(s) loopback URL: %w", err)
	}
	if host == "localhost" {
		return net.JoinHostPort("127.0.0.1", port), nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", errors.New("preview endpoint must use localhost or a loopback IP")
	}
	return net.JoinHostPort(host, port), nil
}

type stringListFlag []string

func (f *stringListFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *stringListFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

type hostFSFlagInput struct {
	flagName string
	value    string
	reason   string
}

func appendHostFSFlagInputs(dst []hostFSFlagInput, flagName string, values []string, reason string) []hostFSFlagInput {
	for _, value := range values {
		dst = append(dst, hostFSFlagInput{flagName: flagName, value: value, reason: reason})
	}
	return dst
}

func hostFSFlagValues(inputs []hostFSFlagInput) []string {
	values := make([]string, 0, len(inputs))
	for _, input := range inputs {
		values = append(values, input.value)
	}
	return values
}

func parseHostFSRunPolicyFlags(grants, deny []hostFSFlagInput) (hostfs.Config, error) {
	var config hostfs.Config
	for _, input := range grants {
		rule, err := parseHostFSRuleFlag(input)
		if err != nil {
			return hostfs.Config{}, err
		}
		config.Grants = append(config.Grants, rule)
	}
	for _, input := range deny {
		rule, err := parseHostFSRuleFlag(input)
		if err != nil {
			return hostfs.Config{}, err
		}
		config.Deny = append(config.Deny, rule)
	}
	if len(config.Grants) == 0 && len(config.Deny) == 0 {
		return config, nil
	}
	if err := hostfs.ValidateConfig(config, hostfs.SourceRun); err != nil {
		return hostfs.Config{}, err
	}
	return config, nil
}

func parseHostFSRuleFlag(input hostFSFlagInput) (hostfs.Rule, error) {
	return hostfs.ParseRuleSpec(input.flagName, input.value, input.reason)
}

func explainText(p profile.Profile, opts runOptions, layout session.Layout, runEnv runEnvironment, env envpolicy.Result, profileDir, identityDir string) string {
	var b strings.Builder
	backendName := resolveBackendName(opts.backendName)
	registry, registryErr := commandProxyRegistry(p)
	displayEnv := env
	if backendName == "lima" {
		displayEnv.Env = lima.GuestEnv(env.Env)
		displayEnv.Synthetic = guestSyntheticEnv(env.Synthetic)
	}
	netPlan, netErr := netpolicy.Prepare(netpolicy.Spec{
		Profile:          p,
		Backend:          backendName,
		SessionDir:       layout.Dir,
		GuestSessionDir:  guestSessionDirForBackend(backendName),
		TargetEnv:        env.Env,
		Resolver:         netpolicy.EnvSecretResolver{},
		LocalBypassHosts: localBypassHostsForBackend(backendName),
		RuntimeVerify:    backendName == "lima",
		DryRun:           true,
	})
	fmt.Fprintf(&b, "Profile: %s\n", p.Name)
	if p.Metadata["profileId"] != "" || p.Metadata["identityId"] != "" {
		fmt.Fprintf(&b, "Identity: profileId=%s identityId=%s lineage=%s", p.Metadata["profileId"], p.Metadata["identityId"], p.Metadata["lineageMode"])
		if p.Metadata["createdFrom"] != "" {
			fmt.Fprintf(&b, " createdFrom=%s", p.Metadata["createdFrom"])
		}
		if p.Metadata["sourceIdentityId"] != "" {
			fmt.Fprintf(&b, " sourceIdentityId=%s", p.Metadata["sourceIdentityId"])
		}
		fmt.Fprintln(&b)
	}
	fmt.Fprintf(&b, "Backend: %s", backendName)
	if backendName == "native" {
		fmt.Fprintf(&b, " (Phase 1A native backend is weak isolation unless --allow-weak-isolation is used)")
	} else if backendName == "lima" {
		fmt.Fprintf(&b, " (target command resolves inside the Lima guest)")
	}
	fmt.Fprintln(&b)
	if backendName == "lima" {
		scope := "session scoped"
		if runEnv.Active {
			scope = "environment scoped"
			if runEnv.Created {
				scope = "environment scoped, new on run"
			}
			if runEnv.RemoveAfterRun {
				scope = "environment scoped, removed after run"
			}
		}
		fmt.Fprintf(&b, "Lima instance: %s (%s)\n", limaInstanceName(p, layout, opts, runEnv), scope)
		if runEnv.Active {
			fmt.Fprintf(&b, "Environment: %s status=%s workspace=%s\n", runEnv.Record.ID, explainValue(runEnv.Record.Status, "ready"), runEnv.Record.Workspace)
		}
	}
	if opts.ephemeral {
		fmt.Fprintf(&b, "Identity storage: ephemeral session-local at %s\n", identityDir)
	} else {
		fmt.Fprintf(&b, "Identity storage: persistent profile at %s\n", profileDir)
	}
	if len(opts.command) > 0 {
		fmt.Fprintf(&b, "Target command: %s\n", strings.Join(opts.command, " "))
		if backendName == "lima" {
			fmt.Fprintln(&b, "Target resolution: inside Lima guest PATH; no host fallback")
		} else {
			fmt.Fprintln(&b, "Target resolution: native host PATH because weak native backend was explicitly selected")
		}
	}
	fmt.Fprintf(&b, "Workspace: host=%s guest=%s mode=%s pathMode=%s\n", opts.workspace, opts.guestWorkspace, p.Workspace.Mode, p.Workspace.PathMode)
	fmt.Fprintln(&b, "Workspace visibility: guest can read/write mapped workspace contents, including project-local secrets")
	if p.Workspace.PathMode == "alias" {
		fmt.Fprintln(&b, "Workspace path privacy: alias mode uses a neutral guest path for the workspace")
	} else {
		fmt.Fprintln(&b, "Workspace path privacy: preserve mode may expose host path shape")
	}
	hostFSProfile := hostFSProfileForRun(p, opts)
	hostFSPolicy, hostFSErr := hostfs.Build(hostfs.BuildInput{Profile: hostFSProfile, Run: opts.hostFSRun})
	if hostFSErr != nil {
		fmt.Fprintf(&b, "HostFS Portal: invalid policy (%s)\n", hostFSErr)
	} else {
		fmt.Fprintf(&b, "HostFS Portal: roots=/hideout/hostfs,/Users,/Volumes,/private default=hidden profileGrants=%d runGrants=%d totalGrants=%d denyRules=%d write=unsupported\n", len(hostFSProfile.Grants), len(opts.hostFSRun.Grants), len(hostFSPolicy.Grants), len(hostFSPolicy.Deny))
		if opts.noProfileHostFSGrants {
			fmt.Fprintln(&b, "HostFS profile grants: disabled for this run; profile deny rules still apply")
		}
		if len(opts.hostFSRun.Deny) > 0 {
			fmt.Fprintf(&b, "HostFS run denies: %d temporary deny rule(s) active\n", len(opts.hostFSRun.Deny))
		}
		if len(hostFSPolicy.Grants) == 0 {
			fmt.Fprintln(&b, "HostFS data plane: inactive because no HostFS grants are active")
		} else if backendName == "lima" {
			fmt.Fprintln(&b, "HostFS data plane: enabled for Lima through hideout-hostfsd FUSE; grants do not create backend mounts")
		} else {
			fmt.Fprintln(&b, "HostFS data plane: not mounted by the native weak backend")
		}
	}
	fmt.Fprintf(&b, "Guest home: %s\n", displayEnv.Synthetic["HOME"])
	fmt.Fprintf(&b, "Identity env: user=%s hostname=%s timezone=%s locale=%s\n",
		displayEnv.Synthetic["USER"],
		displayEnv.Synthetic["HOSTNAME"],
		displayEnv.Synthetic["TZ"],
		displayEnv.Synthetic["LANG"],
	)
	machineScope := "persistent profile"
	if opts.ephemeral {
		machineScope = "ephemeral session"
	}
	machineStatus := "missing"
	if p.Metadata["machineId"] != "" {
		machineStatus = "present"
	}
	fmt.Fprintf(&b, "Machine identity: generated machine-id %s in %s identity root (value hidden)\n", machineStatus, machineScope)
	fmt.Fprintf(&b, "Config/cache/data: config=%s cache=%s data=%s tmp=%s\n",
		displayEnv.Synthetic["XDG_CONFIG_HOME"],
		displayEnv.Synthetic["XDG_CACHE_HOME"],
		displayEnv.Synthetic["XDG_DATA_HOME"],
		displayEnv.Synthetic["TMPDIR"],
	)
	fmt.Fprintf(&b, "Git identity: name=%s email=%s\n", p.Git.UserName, p.Git.UserEmail)
	fmt.Fprintf(&b, "Synthetic env: %s\n", explainMapKeys(displayEnv.Synthetic))
	fmt.Fprintf(&b, "Inherited env: %s\n", explainList(displayEnv.Inherited))
	fmt.Fprintf(&b, "Denied env observed: %s\n", explainList(displayEnv.Denied))
	fmt.Fprintf(&b, "Denied env patterns: %s\n", explainList(p.Env.Deny))
	fmt.Fprintf(&b, "Proxy env in target: absent\n")
	fmt.Fprintf(&b, "Network: %s", netPlan.Mode)
	if netPlan.Mode == netpolicy.ModeDirect {
		fmt.Fprint(&b, " (host network identity may be visible)")
	} else if netPlan.Mode == netpolicy.ModeTun2Socks {
		if netPlan.RuntimeVerify {
			fmt.Fprint(&b, " (hidden proxy via guest-side tun2socks; route verified inside guest before target launch)")
		} else {
			fmt.Fprint(&b, " (hidden proxy via guest-side tun2socks; fail closed until routing is verified)")
		}
	}
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "Network plan: engine=%s verified=%t runtimeVerify=%t failClosed=%t reason=%s\n", explainValue(netPlan.Engine, "none"), netPlan.Verified, netPlan.RuntimeVerify, netPlan.FailClosed, explainValue(netPlan.Reason, "none"))
	fmt.Fprintf(&b, "Network DNS policy: %s\n", explainValue(netPlan.DNSPolicy, "none"))
	if len(netPlan.LocalBypassHosts) > 0 {
		fmt.Fprintf(&b, "Network local bypass: %s\n", strings.Join(netPlan.LocalBypassHosts, ","))
	}
	if netErr != nil {
		fmt.Fprintf(&b, "Network plan error: %s\n", netErr)
	}
	if p.Network.ProxySecretRef != "" {
		fmt.Fprintf(&b, "Network proxy secret: %s (value hidden)\n", p.Network.ProxySecretRef)
	}
	if netPlan.GuestBootstrapPath != "" {
		fmt.Fprintf(&b, "Network bootstrap: %s\n", netPlan.GuestBootstrapPath)
	}
	fmt.Fprintf(&b, "Expected commands: %s\n", listForTUI(sortedStrings(p.Tools.ExpectedCommands)))
	if registryErr != nil {
		fmt.Fprintf(&b, "Command proxy: invalid (%s) via %s\n", registryErr, explainBrokerEndpoint(backendName, layout))
	} else {
		fmt.Fprintf(&b, "Command proxy: %s via %s\n", explainCommandProxy(registry), explainBrokerEndpoint(backendName, layout))
	}
	fmt.Fprintln(&b, "Command proxy scope: registered commands only; ordinary guest processes are not fully audited in Phase 1")
	fmt.Fprintln(&b, "Host broker capability: host.open allows external http/https URLs and mapped workspace files only")
	fmt.Fprintf(&b, "Browser profile: isolated at %s\n", hostopen.BrowserProfileDir(identityDir))
	fmt.Fprintln(&b, "Host browser profile: real default browser profile is not used by default")
	fmt.Fprintln(&b, "Host browser network: localhost, loopback, private, CGNAT, benchmarking, link-local, multicast, .local, and .localhost URL targets are denied before host open")
	fmt.Fprintln(&b, "Host browser control: no DevTools or remote-debugging port is exposed to the guest in Phase 1")
	fmt.Fprintf(&b, "Audit: %s\n", resolveAuditPath(p, opts, layout))
	fmt.Fprintf(&b, "Session: %s\n", layout.ID)
	if backendName == "native" {
		fmt.Fprintln(&b, "Known limitation: native backend does not provide VM/container filesystem isolation.")
		fmt.Fprintln(&b, "Known limitation: native backend may still expose host OS identity APIs such as kernel hostname, OS user database, and system machine-id.")
	} else if backendName == "lima" {
		fmt.Fprintln(&b, "Known limitation: target command must already exist inside the Lima guest; Hideout does not install guest tools.")
	}
	fmt.Fprintln(&b, "Known limitation: Phase 1 does not audit every child process inside the guest.")
	fmt.Fprintln(&b, "Known limitation: workspace secrets remain visible when they are inside the mounted workspace.")
	return b.String()
}

func guestSyntheticEnv(synthetic map[string]string) map[string]string {
	out := make(map[string]string, len(synthetic))
	for k, v := range synthetic {
		out[k] = v
	}
	out["HOME"] = lima.GuestProfileDir + "/home"
	out["TMPDIR"] = lima.GuestSessionDir + "/tmp"
	out["XDG_CONFIG_HOME"] = lima.GuestProfileDir + "/config"
	out["XDG_CACHE_HOME"] = lima.GuestProfileDir + "/cache"
	out["XDG_DATA_HOME"] = lima.GuestProfileDir + "/data"
	out["PATH"] = lima.GuestSessionDir + "/shims:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
	return out
}

func explainList(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ",")
}

func explainMapKeys(values map[string]string) string {
	if len(values) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return strings.Join(keys, ",")
}

func explainValue(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func resolveAuditPath(p profile.Profile, opts runOptions, layout session.Layout) string {
	return manager.ResolveRunAuditPath(p, opts.auditPath, layout)
}

func explainBrokerEndpoint(backendName string, layout session.Layout) string {
	if backendName == "lima" {
		return "tcp://host.lima.internal:<allocated-port>"
	}
	return broker.UnixEndpoint(layout.BrokerSock).String()
}

func explainCommandProxy(registry cmdproxy.Registry) string {
	return strings.Join(registry.ShimNames(), ", ") + " -> " + cmdproxy.ActionHostOpen
}

func commandProxyRegistry(p profile.Profile) (cmdproxy.Registry, error) {
	return cmdproxy.RegistryFromProfile(p)
}

func materializeShims(dir, backendName string, registry cmdproxy.Registry, netPlan netpolicy.Plan) error {
	return manager.MaterializeCommandProxyShims(dir, backendName, registry, netPlan)
}

func materializeHostFSD(dir, backendName string, enabled bool) error {
	return manager.MaterializeHostFSD(dir, backendName, enabled)
}

func resolveShimPath() string {
	return manager.ResolveShimPath()
}

func resolveLinuxShimPath() string {
	return manager.ResolveLinuxShimPath()
}

func resolveLinuxTun2SocksPath() string {
	return manager.ResolveLinuxTun2SocksPath()
}

func resolveLinuxHostFSDPath() string {
	return manager.ResolveLinuxHostFSDPath()
}

type linuxShimBuildOptions struct {
	out    string
	goarch string
	source string
}

func (a app) shim(args []string) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		a.shimUsage()
		return nil
	}
	if len(args) > 0 && args[0] == "build-linux" {
		if len(args) == 2 && isHelpToken(args[1]) {
			a.shimUsage()
			return nil
		}
		return a.buildLinuxShim(args[1:])
	}
	command, commandArgs, err := cmdproxy.ResolveHostOpenInvocation("hideout-shim", args)
	if err != nil {
		return err
	}
	normalized, err := cmdproxy.NormalizeHostOpenCommand(command, commandArgs, mustGetwd())
	if err != nil {
		return err
	}
	endpoint, err := brokerEndpointFromEnv()
	if err != nil {
		return err
	}
	sessionID := os.Getenv(broker.EnvSession)
	token := os.Getenv(broker.EnvToken)
	if sessionID == "" || token == "" {
		return errors.New("broker environment is missing")
	}
	requestID, err := broker.NewRequestID()
	if err != nil {
		return err
	}
	resp := broker.ClientOpenEndpoint(context.Background(), endpoint, broker.Request{
		ID:              requestID,
		SessionID:       sessionID,
		CapabilityToken: token,
		Subject:         normalized.Subject,
		Command:         normalized.Command,
		Argv:            normalized.Argv,
		Route:           normalized.Route,
		Action:          normalized.Action,
		Args:            normalized.Payload,
	})
	if resp.Stdout != "" {
		fmt.Fprint(a.stdout, resp.Stdout)
	}
	if resp.Stderr != "" {
		fmt.Fprintln(a.stderr, resp.Stderr)
	}
	if resp.ExitCode != 0 {
		return fmt.Errorf("shim %s failed with exit code %d", normalized.Command, resp.ExitCode)
	}
	return nil
}

func (a app) buildLinuxShim(args []string) error {
	opts := linuxShimBuildOptions{
		goarch: runtime.GOARCH,
		source: ".",
	}
	fs := flag.NewFlagSet("shim build-linux", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.out, "out", "", "output path for linux hideout-shim")
	fs.StringVar(&opts.goarch, "goarch", opts.goarch, "linux target GOARCH")
	fs.StringVar(&opts.source, "source", opts.source, "Hideout source repository")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: hideout shim build-linux [--out <path>] [--goarch <arch>] [--source <repo>]")
	}
	if strings.TrimSpace(opts.goarch) == "" {
		return errors.New("linux shim GOARCH is required")
	}
	if strings.TrimSpace(opts.out) == "" {
		var err error
		opts.out, err = defaultLinuxShimPath(opts.goarch)
		if err != nil {
			return err
		}
	}
	if err := buildLinuxShimBinary(opts); err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, opts.out)
	return nil
}

func defaultLinuxShimPath(goarch string) (string, error) {
	return manager.DefaultLinuxShimPath(goarch)
}

func (a app) hostfsd(args []string) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		a.hostfsdUsage()
		return nil
	}
	if len(args) > 0 && args[0] == "build-linux" {
		if len(args) == 2 && isHelpToken(args[1]) {
			a.hostfsdUsage()
			return nil
		}
		return a.buildLinuxHostFSD(args[1:])
	}
	return errors.New("usage: hideout hostfsd build-linux [--out <path>] [--goarch <arch>] [--source <repo>]")
}

func (a app) buildLinuxHostFSD(args []string) error {
	opts := linuxShimBuildOptions{
		goarch: runtime.GOARCH,
		source: ".",
	}
	fs := flag.NewFlagSet("hostfsd build-linux", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.out, "out", "", "output path for linux hideout-hostfsd")
	fs.StringVar(&opts.goarch, "goarch", opts.goarch, "linux target GOARCH")
	fs.StringVar(&opts.source, "source", opts.source, "Hideout source repository")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: hideout hostfsd build-linux [--out <path>] [--goarch <arch>] [--source <repo>]")
	}
	if strings.TrimSpace(opts.goarch) == "" {
		return errors.New("linux hostfsd GOARCH is required")
	}
	if strings.TrimSpace(opts.out) == "" {
		var err error
		opts.out, err = defaultLinuxHostFSDPath(opts.goarch)
		if err != nil {
			return err
		}
	}
	if err := buildLinuxCommandBinary(opts, "hideout-hostfsd"); err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, opts.out)
	return nil
}

func defaultLinuxHostFSDPath(goarch string) (string, error) {
	return manager.DefaultLinuxHostFSDPath(goarch)
}

func buildLinuxShimBinary(opts linuxShimBuildOptions) error {
	return buildLinuxCommandBinary(opts, "hideout-shim")
}

func buildLinuxCommandBinary(opts linuxShimBuildOptions, command string) error {
	return helperbin.BuildLinuxCommand(helperbin.BuildOptions{
		Out:     opts.out,
		GOARCH:  opts.goarch,
		Source:  opts.source,
		Command: command,
	})
}

func brokerEndpointFromEnv() (broker.Endpoint, error) {
	if raw := os.Getenv(broker.EnvEndpoint); raw != "" {
		return broker.ParseEndpoint(raw)
	}
	if sock := os.Getenv(broker.EnvSock); sock != "" {
		return broker.UnixEndpoint(sock), nil
	}
	return broker.Endpoint{}, errors.New("broker environment is missing")
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

func (a app) doctor(args []string) error {
	if containsHelpToken(args) {
		a.doctorUsage()
		return nil
	}
	opts, err := parseDoctorOptions(args)
	if err != nil {
		return err
	}
	if opts.tools.hasChanges() {
		return unsupportedLegacyToolSupplyError()
	}
	if opts.fix {
		return a.doctorFix(opts)
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	failed := false
	doctorReq := doctorpkg.Request{
		Profile:      opts.profileName,
		Backend:      resolveBackendName(opts.backendName),
		Workspace:    opts.workspace,
		Level:        opts.level,
		Features:     opts.features,
		EvidencePath: opts.evidenceOut,
	}
	builder := doctorpkg.NewBuilder(doctorReq)
	humanOutput := opts.format != "json"
	report := func(name, status, message string) {
		builder.Add(name, name, status, message)
		if humanOutput {
			if message == "" {
				fmt.Fprintf(a.stdout, "%s: %s\n", name, status)
			} else {
				fmt.Fprintf(a.stdout, "%s: %s %s\n", name, status, message)
			}
		}
		if status == "error" {
			failed = true
		}
	}
	if humanOutput {
		fmt.Fprintln(a.stdout, "Hideout doctor")
		fmt.Fprintf(a.stdout, "storage: %s\n", store.Root)
	}
	if err := os.MkdirAll(store.Root, 0o700); err != nil {
		report("store", "error", err.Error())
		if opts.evidenceOut != "" {
			if _, writeErr := builder.WriteEvidence(opts.evidenceOut); writeErr != nil {
				return writeErr
			}
		}
		if opts.format == "json" {
			_ = doctorpkg.WriteJSON(a.stdout, builder.Report())
		}
		return errors.New("doctor found errors")
	}
	report("store", "ok", "writable")
	checkManager(store, report)
	report("guest-privilege", "ok", doctorGuestPrivilegeMessage(context.Background(), store, opts.profileName))
	report("support-matrix", doctorSupportMatrixStatus(doctorReq.Backend), doctorSupportMatrixMessage(doctorReq.Backend))

	p, profileLoaded := loadDoctorProfile(store, opts.profileName, report)
	if opts.networkMode != "" {
		p.Network.Mode = opts.networkMode
	}
	if opts.proxySecret != "" {
		p.Network.ProxySecretRef = opts.proxySecret
	}
	if opts.mediatedResolver != "" {
		p.Network.MediatedResolver = opts.mediatedResolver
	}
	if err := p.Validate(); err != nil {
		report("profile", "error", err.Error())
	}
	runtimeProfile := p
	if opts.ephemeral {
		runtimeProfile, err = profile.EphemeralIdentityProfile(p)
		if err != nil {
			report("identity", "error", err.Error())
		}
	}
	workspace, guestWorkspace, err := resolveWorkspaceMapping(opts.workspace, opts.guestWorkspace, runtimeProfile)
	if err != nil {
		report("workspace", "error", err.Error())
	}
	if err == nil {
		if safetyErr := manager.ValidateWorkspaceMountSafety(workspace, store.Root); safetyErr != nil {
			report("workspace", "error", safetyErr.Error())
		}
	}
	checkWorkspace(workspace, guestWorkspace, runtimeProfile, report)
	a.warnShadowedHostFSRules(runtimeProfile.HostFS, workspace)

	layout, err := session.New(store.Root)
	if err != nil {
		report("session", "error", err.Error())
		if failed {
			return errors.New("doctor found errors")
		}
		return nil
	}
	defer os.RemoveAll(layout.Dir)

	backendName := resolveBackendName(opts.backendName)
	checkBackend(backendName, report)
	profileDir := store.ProfileDir(p.Name)
	identityDir := runtimeIdentityDir(layout, profileDir, runOptions{ephemeral: opts.ephemeral})
	if opts.ephemeral && runtimeProfile.Metadata["machineId"] != "" {
		if err := profile.MaterializeIdentityState(identityDir, runtimeProfile); err != nil {
			report("identity", "error", err.Error())
		}
	}
	checkIdentityState(runtimeProfile, identityDir, opts.ephemeral, profileLoaded, report)
	checkMountPlan(backendName, runtimeProfile, layout, workspace, guestWorkspace, identityDir, report)
	checkLimaGeneratedConfig(backendName, runtimeProfile, layout, workspace, guestWorkspace, identityDir, report)
	env := envpolicy.Build(envpolicy.Spec{
		Profile:    runtimeProfile,
		ProfileDir: identityDir,
		SessionDir: layout.Dir,
		ShimDir:    layout.ShimDir,
	})
	checkEnv(env, report)
	checkPolicy(runtimeProfile, profileDir, report)
	checkNetwork(runtimeProfile, backendName, layout, env, report)
	checkBroker(store.Root, runtimeProfile, backendName, layout, workspace, guestWorkspace, profileDir, report)
	checkCommandProxyRuntime(backendName, report)
	checkHostFSRuntime(backendName, runtimeProfile, report)
	checkHostOpen(runtimeProfile, identityDir, report)
	if !profileLoaded {
		report("profile-init", "warn", "run or profile init will materialize profile state")
	}
	a.addDoctorFeatureDiagnostics(doctorReq, store, runtimeProfile, backendName, workspace, builder)
	if opts.evidenceOut != "" {
		evidencePath, err := builder.WriteEvidence(opts.evidenceOut)
		if err != nil {
			return err
		}
		if humanOutput {
			fmt.Fprintf(a.stdout, "doctor-evidence: ok %s\n", evidencePath)
		}
	}
	if opts.format == "json" {
		if err := doctorpkg.WriteJSON(a.stdout, builder.Report()); err != nil {
			return err
		}
	}
	if failed {
		return errors.New("doctor found errors")
	}
	return nil
}

func checkManager(store profile.Store, report func(string, string, string)) {
	overview, err := manager.New(store).Overview(context.Background())
	if err != nil {
		report("manager", "error", err.Error())
		return
	}
	report("manager", "ok", fmt.Sprintf(
		"profiles=%d sessions=%d backends=%d availableBackends=%d commandProxies=%d secrets=%d",
		len(overview.Profiles),
		len(overview.Sessions),
		len(overview.Backends),
		availableBackends(overview.Backends),
		len(overview.Capabilities.CommandProxies),
		len(overview.Secrets),
	))
}

func doctorSupportMatrixStatus(backendName string) string {
	matrix := releasecompat.BuiltinMatrix()
	platform, ok := releasecompat.FindEntry(matrix, releasecompat.CurrentPlatformSubject())
	if !ok || platform.Level == releasecompat.LevelUnsupported {
		return "error"
	}
	backend, ok := releasecompat.FindEntry(matrix, releasecompat.BackendSubject(backendName))
	if !ok || backend.Level == releasecompat.LevelUnsupported {
		return "error"
	}
	if platform.Level == releasecompat.LevelDegraded || backend.Level == releasecompat.LevelDegraded {
		return "warn"
	}
	return "ok"
}

func doctorSupportMatrixMessage(backendName string) string {
	return fmt.Sprintf("matrix=%s %s", releasecompat.MatrixVersion, releasecompat.CurrentSupportSummary(backendName))
}

func (a app) addDoctorFeatureDiagnostics(req doctorpkg.Request, store profile.Store, p profile.Profile, backendName string, workspace string, builder *doctorpkg.Builder) {
	features := selectedDoctorDiagnosticFeatures(req)
	if len(features) == 0 || builder == nil {
		return
	}
	core := manager.New(store)
	overview, overviewErr := core.Overview(context.Background())
	decisionStatus, decisionErr := core.DecisionStatus()
	for _, feature := range features {
		switch feature {
		case "adapters":
			enabled := countEnabledAdapters(p)
			packs := 0
			if overviewErr == nil {
				packs = len(overview.AdapterPacks)
			}
			builder.Add("feature-adapters", "adapters", doctorpkg.StatusPass, fmt.Sprintf("enabledAdapters=%d adapterPacks=%d", enabled, packs), doctorpkg.WithRequired(false))
		case "cleanup":
			if overviewErr != nil {
				builder.Add("feature-cleanup", "cleanup", doctorpkg.StatusWarn, "manager overview unavailable: "+overviewErr.Error(), doctorpkg.WithRequired(false))
				continue
			}
			builder.Add("feature-cleanup", "cleanup", doctorpkg.StatusPass, fmt.Sprintf("sessions=%d environments=%d", len(overview.Sessions), len(overview.Environments)), doctorpkg.WithRequired(false))
		case "daemon":
			builder.Add("feature-daemon", "daemon", doctorpkg.StatusPass, "daemon command and schemas are packaged; runtime availability is checked by daemon smoke", doctorpkg.WithRequired(false))
		case "decisions":
			if decisionErr != nil {
				builder.Add("feature-decisions", "decisions", doctorpkg.StatusWarn, "decision status unavailable: "+decisionErr.Error(), doctorpkg.WithRequired(false))
				continue
			}
			builder.Add("feature-decisions", "decisions", doctorpkg.StatusPass, fmt.Sprintf("pending=%d claimed=%d terminal=%d notices=%d", decisionStatus.PendingDecisions, decisionStatus.ClaimedDecisions, decisionStatus.TerminalDecisions, decisionStatus.UnackedNotices), doctorpkg.WithRequired(false))
		case "dns":
			status, msg := doctorDNSFeatureStatus(p, backendName)
			builder.Add("feature-dns", "dns", status, msg, doctorpkg.WithRequired(false))
		case "export":
			builder.Add("feature-export", "export", doctorpkg.StatusPass, "export/share schema and doctor-report source are available; use hideout audit export for shareable evidence", doctorpkg.WithRequired(false))
		case "hostfs":
			builder.Add("feature-hostfs", "hostfs", doctorpkg.StatusPass, fmt.Sprintf("grants=%d denyRules=%d overlayGrants=%d workspace=%s", len(p.HostFS.Grants), len(p.HostFS.Deny), countOverlayGrants(p), audit.RedactString(workspace)), doctorpkg.WithRequired(false))
		case "lima":
			status, msg := doctorLimaFeatureStatus(overview, overviewErr, backendName)
			builder.Add("feature-lima", "lima", status, msg, doctorpkg.WithRequired(false))
		case "packaging":
			builder.Add("feature-packaging", "packaging", doctorpkg.StatusPass, "package install/verify/uninstall commands are available; package smoke validates artifact ownership", doctorpkg.WithRequired(false))
		case "privilege":
			builder.Add("feature-privilege", "privilege", doctorpkg.StatusPass, doctorGuestPrivilegeMessage(context.Background(), store, req.Profile), doctorpkg.WithRequired(false))
		}
	}
}

func selectedDoctorDiagnosticFeatures(req doctorpkg.Request) []string {
	seen := map[string]bool{}
	var out []string
	add := func(feature string) {
		feature = strings.TrimSpace(feature)
		if feature == "" || seen[feature] {
			return
		}
		seen[feature] = true
		out = append(out, feature)
	}
	for _, feature := range doctorpkg.NormalizeFeatures(req.Features) {
		add(feature)
	}
	if doctorpkg.DeepSelected(req) {
		for _, feature := range doctorpkg.SupportedFeatures {
			add(feature)
		}
	}
	return out
}

func countEnabledAdapters(p profile.Profile) int {
	count := 0
	for _, adapter := range p.CommandAdapters.Adapters {
		if adapter.Enabled {
			count++
		}
	}
	return count
}

func countOverlayGrants(p profile.Profile) int {
	count := 0
	for _, grant := range p.HostFS.Grants {
		if grant.Overlay {
			count++
		}
	}
	return count
}

func doctorDNSFeatureStatus(p profile.Profile, backendName string) (string, string) {
	if p.Network.Mode != "tun2socks" {
		return doctorpkg.StatusWarn, "network mode is " + p.Network.Mode + "; DNS mediation requires tun2socks privacy mode"
	}
	if strings.TrimSpace(p.Network.MediatedResolver) == "" {
		return doctorpkg.StatusWarn, "tun2socks is selected but mediated resolver is not configured"
	}
	if backendName == "native" {
		return doctorpkg.StatusWarn, "mediated resolver configured, but native backend is not DNS isolation evidence"
	}
	return doctorpkg.StatusPass, "tun2socks mediated resolver configured; release claim still requires Gate 3"
}

func doctorLimaFeatureStatus(overview manager.Overview, overviewErr error, backendName string) (string, string) {
	if backendName != "lima" {
		return doctorpkg.StatusWarn, "requested backend is " + backendName + "; Lima proof requires --backend lima"
	}
	if overviewErr != nil {
		return doctorpkg.StatusWarn, "manager overview unavailable: " + overviewErr.Error()
	}
	for _, backend := range overview.Backends {
		if backend.Name != "lima" {
			continue
		}
		if backend.Available {
			return doctorpkg.StatusPass, "lima backend is available"
		}
		return doctorpkg.StatusWarn, "lima backend unavailable: " + backend.Error
	}
	return doctorpkg.StatusWarn, "lima backend status not reported"
}

func availableBackends(backends []manager.BackendSummary) int {
	count := 0
	for _, backend := range backends {
		if backend.Available {
			count++
		}
	}
	return count
}

func limaInstanceName(p profile.Profile, layout session.Layout, opts runOptions, runEnv runEnvironment) string {
	if runEnv.Active && runEnv.InstanceName != "" {
		return runEnv.InstanceName
	}
	return lima.InstanceNameForSession(p.Name, layout.ID)
}

func sortedMapKeys(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

type doctorOptions struct {
	profileName      string
	backendName      string
	format           string
	level            string
	features         stringListFlag
	networkMode      string
	proxySecret      string
	mediatedResolver string
	evidenceOut      string
	workspace        string
	guestWorkspace   string
	ephemeral        bool
	fix              bool
	dryRun           bool
	apply            bool
	tools            toolSupplyOptions
}

func parseDoctorOptions(args []string) (doctorOptions, error) {
	opts := doctorOptions{profileName: "default", backendName: "auto", format: "human", level: doctorpkg.LevelLight}
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.profileName, "profile", "default", "profile name")
	fs.StringVar(&opts.backendName, "backend", "auto", "backend")
	fs.StringVar(&opts.format, "format", "human", "output format: human or json")
	fs.StringVar(&opts.level, "level", doctorpkg.LevelLight, "diagnostic level: light or deep")
	fs.Var(&opts.features, "feature", "include feature diagnostic")
	fs.StringVar(&opts.networkMode, "network", "", "network mode")
	fs.StringVar(&opts.proxySecret, "proxy-secret", "", "proxy secret ref")
	fs.StringVar(&opts.mediatedResolver, "mediated-resolver", "", "tun2socks mediated DNS resolver IP")
	fs.StringVar(&opts.evidenceOut, "evidence-out", "", "write a redacted doctor report")
	fs.StringVar(&opts.workspace, "workspace", "", "host workspace")
	fs.StringVar(&opts.guestWorkspace, "guest-workspace", "", "guest workspace")
	registerToolSupplyFlags(fs, &opts.tools)
	fs.BoolVar(&opts.ephemeral, "ephemeral", false, "diagnose session-local identity state")
	fs.BoolVar(&opts.fix, "fix", false, "apply safe initialization repairs")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "print fix plan without applying")
	fs.BoolVar(&opts.apply, "apply", false, "apply safe initialization repairs")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() != 0 {
		return opts, fmt.Errorf("unexpected doctor argument %q", fs.Arg(0))
	}
	if err := opts.tools.validate(); err != nil {
		return opts, err
	}
	switch opts.format {
	case "human", "json":
	default:
		return opts, fmt.Errorf("unsupported doctor format %q", opts.format)
	}
	if err := doctorpkg.ValidateRequest(doctorpkg.Request{Level: opts.level, Features: opts.features}); err != nil {
		return opts, err
	}
	if opts.dryRun && opts.apply {
		return opts, errors.New("--dry-run and --apply are mutually exclusive")
	}
	return opts, nil
}

func (a app) doctorFix(opts doctorOptions) error {
	if !opts.dryRun && !opts.apply {
		return errors.New("doctor --fix requires --dry-run or --apply")
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	networkMode := opts.networkMode
	if networkMode == "" {
		networkMode = "direct"
	}
	core := manager.New(store)
	plan, err := core.PlanDoctorFix(inittask.Options{
		ProfileName:      opts.profileName,
		Backend:          opts.backendName,
		Network:          networkMode,
		ProxySecretRef:   opts.proxySecret,
		MediatedResolver: opts.mediatedResolver,
		NoInput:          true,
	})
	if err != nil {
		return err
	}
	if opts.dryRun {
		writeInitPlan(a.stdout, "Hideout doctor fix plan", plan)
		return nil
	}
	result, err := core.ApplyDoctorFix(plan, inittask.ApplyOptions{NoInput: true})
	if err != nil {
		return err
	}
	writeInitResult(a.stdout, "Hideout doctor fix", result)
	return nil
}

func registerToolSupplyFlags(fs *flag.FlagSet, opts *toolSupplyOptions) {
	fs.Var(&opts.presets, "tool-preset", "tool preset to add to the profile; may be repeated")
	fs.StringVar(&opts.npmPackage, "npm-package", "", "npm package spec for one global CLI tool")
	fs.Var(&opts.npmCommands, "npm-command", "command expected after npm global install; may be repeated")
}

func (opts toolSupplyOptions) validate() error {
	if opts.hasChanges() {
		return unsupportedLegacyToolSupplyError()
	}
	return nil
}

func (opts toolSupplyOptions) hasChanges() bool {
	return len(opts.presets) > 0 || strings.TrimSpace(opts.npmPackage) != "" || len(opts.npmCommands) > 0
}

func unsupportedLegacyToolSupplyError() error {
	return errors.New("legacy tool-supply flags are no longer supported; install tools in the guest environment and declare expected commands with hideout profile tools <name> expected add <command>")
}

func loadDoctorProfile(store profile.Store, name string, report func(string, string, string)) (profile.Profile, bool) {
	p, err := store.Load(name)
	if err == nil {
		report("profile", "ok", name)
		return p, true
	}
	if errors.Is(err, os.ErrNotExist) {
		report("profile", "warn", fmt.Sprintf("%s missing; using defaults for diagnostics", name))
		return profile.Default(name), false
	}
	report("profile", "error", err.Error())
	return profile.Default(name), false
}

func checkWorkspace(host, guest string, p profile.Profile, report func(string, string, string)) {
	if host == "" {
		report("workspace", "error", "workspace path is empty")
		return
	}
	st, err := os.Stat(host)
	if err != nil {
		report("workspace", "error", err.Error())
		return
	}
	if !st.IsDir() {
		report("workspace", "error", "workspace is not a directory")
		return
	}
	report("workspace", "ok", fmt.Sprintf("host=%s guest=%s mode=%s pathMode=%s", host, guest, p.Workspace.Mode, p.Workspace.PathMode))
}

func checkIdentityState(p profile.Profile, identityDir string, ephemeral, profileLoaded bool, report func(string, string, string)) {
	mode := "persistent"
	if ephemeral {
		mode = "ephemeral"
	}
	if !profileLoaded && !ephemeral {
		report("identity", "warn", "profile identity state is not materialized yet")
		return
	}
	if identityDir == "" {
		report("identity", "error", "identity root is empty")
		return
	}
	machineID := p.Metadata["machineId"]
	if machineID == "" {
		report("identity", "error", "metadata.machineId is missing")
		return
	}
	data, err := os.ReadFile(filepath.Join(identityDir, "machine", "machine-id"))
	if err != nil {
		report("identity", "error", err.Error())
		return
	}
	if strings.TrimSpace(string(data)) != machineID {
		report("identity", "error", "machine-id file does not match runtime identity metadata")
		return
	}
	parts := []string{
		"mode=" + mode,
		"root=" + identityDir,
		"identityId=" + p.Metadata["identityId"],
		"lineage=" + p.Metadata["lineageMode"],
	}
	if p.Metadata["sourceIdentityId"] != "" {
		parts = append(parts, "sourceIdentityId="+p.Metadata["sourceIdentityId"])
	}
	report("identity", "ok", strings.Join(parts, " "))
}

func checkBackend(backendName string, report func(string, string, string)) {
	switch backendName {
	case "lima":
		if err := (lima.Backend{}).Available(context.Background()); err != nil {
			report("backend", "error", "lima unavailable: "+err.Error())
			return
		}
		report("backend", "ok", "lima available")
	case "native":
		report("backend", "warn", "native is weak isolation and requires --backend native --allow-weak-isolation for run")
	default:
		report("backend", "error", fmt.Sprintf("%s is not implemented", backendName))
	}
}

func checkMountPlan(backendName string, p profile.Profile, layout session.Layout, hostRoot, guestRoot, profileDir string, report func(string, string, string)) {
	switch backendName {
	case "native":
		report("mount", "ok", "native weak backend has no VM mount plan; run still requires explicit weak isolation")
	case "lima":
		if hostRoot == "" || guestRoot == "" {
			report("mount", "error", "workspace mapping is unavailable")
			return
		}
		cfg, err := lima.ConfigForRunSpec(backend.RunSpec{
			Profile:    p,
			ImageRef:   p.BaseImageOrBuiltin(),
			HostWork:   hostRoot,
			GuestWork:  guestRoot,
			ProfileDir: profileDir,
			SessionDir: layout.Dir,
		})
		if err != nil {
			report("mount", "error", err.Error())
			return
		}
		workspaceMounts := 0
		for _, m := range cfg.Mounts {
			if m.Location == hostRoot && m.MountPoint == guestRoot && m.Writable {
				workspaceMounts++
			}
			if err := validateRuntimeMount("profile", profileDir, m.Location, []string{"home", "cache", "config", "data", "browser", "machine"}); err != nil {
				report("mount", "error", err.Error())
				return
			}
			if err := validateRuntimeMount("session", layout.Dir, m.Location, []string{"tmp", "shims", "network", "bootstrap"}); err != nil {
				report("mount", "error", err.Error())
				return
			}
			if strings.HasPrefix(filepath.Base(m.Location), ".") && m.Location != hostRoot {
				report("mount", "error", fmt.Sprintf("hidden host path %q must not be mounted by default", m.Location))
				return
			}
		}
		if workspaceMounts != 1 {
			report("mount", "error", fmt.Sprintf("expected one writable workspace mount, got %d", workspaceMounts))
			return
		}
		report("mount", "ok", fmt.Sprintf("lima mounts=%d workspace=%s profileRuntimeOnly=true sessionRuntimeOnly=true", len(cfg.Mounts), guestRoot))
	default:
		report("mount", "error", fmt.Sprintf("%s is not implemented", backendName))
	}
}

func validateRuntimeMount(domain, root, location string, allowedTopLevel []string) error {
	if root == "" || location == "" {
		return nil
	}
	rel, err := filepath.Rel(root, location)
	if err != nil || pathEscapesRoot(rel) {
		return nil
	}
	if rel == "." {
		return fmt.Errorf("%s root %q must not be mounted as a whole", domain, root)
	}
	top := rel
	if before, _, ok := strings.Cut(rel, string(filepath.Separator)); ok {
		top = before
	}
	if !slices.Contains(allowedTopLevel, top) {
		return fmt.Errorf("%s control-plane path %q must not be mounted", domain, location)
	}
	return nil
}

func pathEscapesRoot(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

const limaConfigValidateTimeout = 15 * time.Second

func checkLimaGeneratedConfig(backendName string, p profile.Profile, layout session.Layout, hostRoot, guestRoot, identityRoot string, report func(string, string, string)) {
	if backendName != "lima" {
		return
	}
	if hostRoot == "" || guestRoot == "" {
		return
	}
	limactl, err := exec.LookPath("limactl")
	if err != nil {
		return
	}
	configPath := filepath.Join(layout.Dir, "doctor-lima.yaml")
	limaCfg, err := lima.ConfigForRunSpec(backend.RunSpec{
		Profile:      p,
		ImageRef:     p.BaseImageOrBuiltin(),
		HostWork:     hostRoot,
		GuestWork:    guestRoot,
		ProfileDir:   identityRoot,
		SessionDir:   layout.Dir,
		IdentityRoot: identityRoot,
	})
	if err != nil {
		report("lima-config", "error", "invalid base image declaration: "+err.Error())
		return
	}
	if err := lima.WriteConfig(configPath, limaCfg); err != nil {
		report("lima-config", "error", "could not write generated YAML: "+doctorDiagnostic(nil, err))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), limaConfigValidateTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, limactl, "validate", configPath)
	cmd.Env = lima.HostCommandEnv(os.Environ())
	data, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		report("lima-config", "error", "limactl validate timed out")
		return
	}
	if err != nil {
		report("lima-config", "error", "generated YAML failed validation: "+doctorDiagnostic(data, err))
		return
	}
	report("lima-config", "ok", "generated YAML validates")
}

func doctorDiagnostic(output []byte, err error) string {
	message := strings.TrimSpace(string(output))
	if message == "" && err != nil {
		message = err.Error()
	}
	message = strings.Join(strings.Fields(message), " ")
	if message == "" {
		return "unknown error"
	}
	const maxDiagnosticLen = 240
	if len(message) > maxDiagnosticLen {
		message = message[:maxDiagnosticLen] + "..."
	}
	return message
}

func checkEnv(env envpolicy.Result, report func(string, string, string)) {
	if netpolicy.ContainsProxyEnv(env.Env) {
		report("env", "error", "target env contains proxy variables")
		return
	}
	if containsHideoutSecretEnv(env.Env) {
		report("env", "error", "target env contains hideout secret variables")
		return
	}
	report("env", "ok", fmt.Sprintf("synthetic=%d inherited=%d denied=%d proxyEnv=absent secretEnv=absent", len(env.Synthetic), len(env.Inherited), len(env.Denied)))
}

func containsHideoutSecretEnv(env []string) bool {
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if ok && strings.HasPrefix(name, "HIDEOUT_SECRET_") {
			return true
		}
	}
	return false
}

func checkPolicy(p profile.Profile, profileDir string, report func(string, string, string)) {
	evaluator := policy.NewEvaluator(p)
	if _, err := evaluator.Validate(policy.Proposal{
		Decision:  policy.AuditOnly,
		Route:     policy.GuestDirect,
		Action:    "guest.exec",
		Resources: []string{"guest-command:doctor"},
		Reason:    "doctor top-level command policy check",
	}); err != nil {
		report("policy", "error", err.Error())
		return
	}
	if _, err := evaluator.Validate(networkConnectProposal(p.Network.Mode, "doctor network policy check")); err != nil {
		report("policy", "error", err.Error())
		return
	}
	if _, err := evaluator.EvaluateOpen("https://example.com"); err != nil {
		report("policy", "error", err.Error())
		return
	}
	if err := checkPolicyScripts(p, profileDir, evaluator); err != nil {
		report("policy", "error", err.Error())
		return
	}
	report("policy", "ok", fmt.Sprintf("engine=%s maxCapabilities=%d scripts=%d", p.Policy.Engine, len(p.Policy.MaxCapabilities), len(p.Policy.ScriptRefs)))
}

func networkConnectProposal(mode, reason string) policy.Proposal {
	if mode == "" {
		mode = "direct"
	}
	return policy.Proposal{
		Decision:  policy.AuditOnly,
		Route:     policy.GuestDirect,
		Action:    "network.connect",
		Resources: []string{"network:" + mode},
		Reason:    reason,
	}
}

func checkPolicyScripts(p profile.Profile, profileDir string, evaluator policy.Evaluator) error {
	for _, ref := range p.Policy.ScriptRefs {
		path := ref.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(profileDir, path)
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("script %s: %w", ref.ID, err)
		}
		for _, entrypoint := range ref.Entrypoints {
			switch entrypoint {
			case "decideCommand":
				req := doctorCommandScriptRequest()
				ctx := policy.CommandContext{
					Version: "policy-script/v1",
					Profile: map[string]string{
						"name": p.Name,
					},
					Session: map[string]any{
						"id":          "doctor",
						"interactive": false,
					},
					Subject: "command:open",
					Command: map[string]any{
						"name":   "open",
						"argv":   []string{"open", "https://example.com"},
						"cwd":    "/workspace",
						"target": "https://example.com",
					},
					Workspace: map[string]any{
						"guestRoot":       "/workspace",
						"hostRootVisible": false,
						"mode":            "read-write",
					},
					Env: map[string]any{
						"safe": map[string]string{"TERM": "xterm-256color"},
					},
					Network: map[string]any{
						"mode": p.Network.Mode,
					},
				}
				proposal, err := evaluator.RunCommandScript(string(source), entrypoint, ctx)
				if err != nil {
					return fmt.Errorf("script %s entrypoint %s: %w", ref.ID, entrypoint, err)
				}
				if err := broker.ValidateCommandScriptProposal(req, proposal); err != nil {
					return fmt.Errorf("script %s entrypoint %s: %w", ref.ID, entrypoint, err)
				}
			case "redactAudit":
				ctx := policy.AuditContext{
					Version:  "policy-audit/v1",
					Profile:  map[string]string{"name": p.Name},
					Session:  map[string]any{"id": "doctor"},
					Subject:  "command:open",
					Action:   "host.open",
					Decision: string(policy.Allow),
					Details: map[string]any{
						"target": "https://example.com",
						"argv":   []string{"open", "https://example.com"},
					},
					Extra: map[string]interface{}{
						"status":   "ok",
						"exitCode": 0,
					},
				}
				if _, err := evaluator.RunAuditRedactScript(string(source), entrypoint, ctx); err != nil {
					return fmt.Errorf("script %s entrypoint %s: %w", ref.ID, entrypoint, err)
				}
			default:
				continue
			}
		}
	}
	return nil
}

func doctorCommandScriptRequest() broker.Request {
	return broker.Request{
		ID:              "req_doctor",
		SessionID:       "doctor",
		CapabilityToken: "doctor",
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com"},
	}
}

func checkNetwork(p profile.Profile, backendName string, layout session.Layout, env envpolicy.Result, report func(string, string, string)) {
	plan, err := netpolicy.Prepare(netpolicy.Spec{
		Profile:          p,
		Backend:          backendName,
		SessionDir:       layout.Dir,
		GuestSessionDir:  guestSessionDirForBackend(backendName),
		TargetEnv:        env.Env,
		Resolver:         netpolicy.EnvSecretResolver{},
		LocalBypassHosts: localBypassHostsForBackend(backendName),
		RuntimeVerify:    backendName == "lima",
		DryRun:           true,
	})
	if err != nil {
		report("network", "error", err.Error())
		return
	}
	status := "ok"
	if networkDecision(plan, nil) == "audit-only" {
		status = "warn"
	}
	report("network", status, fmt.Sprintf("mode=%s engine=%s runtimeVerify=%t localBypass=%s reason=%s", plan.Mode, plan.Engine, plan.RuntimeVerify, explainList(plan.LocalBypassHosts), plan.Reason))
}

func checkBroker(storeRoot string, p profile.Profile, backendName string, layout session.Layout, hostRoot, guestRoot, profileDir string, report func(string, string, string)) {
	token, err := broker.NewToken()
	if err != nil {
		report("broker", "error", err.Error())
		return
	}
	registry, err := commandProxyRegistry(p)
	if err != nil {
		report("broker", "error", err.Error())
		return
	}
	adapters, err := cmdadapter.CompileWithResolver(p, profileDir, adapterpack.RuntimeResolver{Store: adapterpack.NewStore(storeRoot)})
	if err != nil {
		report("broker", "error", err.Error())
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	endpoint := brokerEndpointForBackend(backendName, layout)
	server := &broker.Server{
		SessionID:       layout.ID,
		Token:           token,
		Socket:          layout.BrokerSock,
		Endpoint:        endpoint,
		HostRoot:        hostRoot,
		GuestRoot:       guestRoot,
		Profile:         p.Name,
		ProfileDir:      profileDir,
		Backend:         backendName,
		WorkspaceMode:   p.Workspace.Mode,
		NetworkMode:     p.Network.Mode,
		Commands:        registry.ShimNames(),
		CommandAdapters: adapters,
		ScriptRefs:      p.Policy.ScriptRefs,
		Evaluator:       policy.NewEvaluator(p),
		Audit:           audit.NewDiscard(),
		Opener:          broker.NoopOpener{},
	}
	if err := server.StartEndpoint(ctx, endpoint); err != nil {
		report("broker", "error", err.Error())
		return
	}
	defer server.Close()
	resp := checkBrokerOpen(ctx, brokerEndpointForDoctorClient(server.Endpoint), broker.Request{
		ID:              "req_doctor",
		SessionID:       layout.ID,
		CapabilityToken: token,
		Subject:         "command:open",
		Command:         "open",
		Argv:            []string{"open", "https://example.com"},
		Route:           "host-broker",
		Action:          "host.open",
		Args:            map[string]any{"target": "https://example.com"},
	})
	if resp.Status == "broker-unavailable" {
		report("broker", "error", resp.Stderr)
		return
	}
	if resp.Status != "ok" {
		report("broker", "warn", fmt.Sprintf("transport=%s endpoint=present host.open decision=%s status=%s", server.Endpoint.Network, resp.Decision, resp.Status))
	} else {
		report("broker", "ok", fmt.Sprintf("transport=%s endpoint=present host.open=%s", server.Endpoint.Network, resp.Decision))
	}
}

func checkBrokerOpen(ctx context.Context, endpoint broker.Endpoint, req broker.Request) broker.Response {
	deadline := time.Now().Add(5 * time.Second)
	var resp broker.Response
	for {
		reqCtx, reqCancel := context.WithTimeout(ctx, time.Second)
		resp = broker.ClientOpenEndpoint(reqCtx, endpoint, req)
		reqCancel()
		if resp.Status != "broker-unavailable" || time.Now().After(deadline) {
			return resp
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func checkCommandProxyRuntime(backendName string, report func(string, string, string)) {
	switch backendName {
	case "lima":
		if resolveLinuxShimPath() == "" {
			report("command-proxy", "error", "prebuilt linux hideout-shim is required for Lima command proxies")
			return
		}
		report("command-proxy", "ok", "linux shim=present")
	case "native":
		if resolveShimPath() == "" {
			report("command-proxy", "warn", "native hideout-shim not found; registered command proxies will fail")
			return
		}
		report("command-proxy", "ok", "native shim=present")
	}
}

func checkHostFSRuntime(backendName string, p profile.Profile, report func(string, string, string)) {
	hostFSProfile := hostFSProfileForRun(p, runOptions{})
	hostFSPolicy, err := hostfs.Build(hostfs.BuildInput{Profile: hostFSProfile})
	if err != nil {
		report("hostfs", "error", err.Error())
		return
	}
	grants := len(hostFSPolicy.Grants)
	if grants == 0 {
		report("hostfs", "ok", "inactive grants=0")
		return
	}
	switch backendName {
	case "lima":
		if resolveLinuxHostFSDPath() == "" {
			report("hostfs", "error", fmt.Sprintf("grants=%d prebuilt linux hideout-hostfsd is required for Lima HostFS", grants))
			return
		}
		report("hostfs", "ok", fmt.Sprintf("grants=%d linux hostfsd=present", grants))
	case "native":
		report("hostfs", "warn", fmt.Sprintf("grants=%d backend=native dataPlane=not-mounted", grants))
	default:
		report("hostfs", "error", fmt.Sprintf("grants=%d backend=%s is not supported for HostFS", grants, backendName))
	}
}

func checkHostOpen(p profile.Profile, identityDir string, report func(string, string, string)) {
	if !p.HostCapabilities.Open.AllowURLs {
		report("host-open", "ok", "url disabled by profile")
		return
	}
	opener := hostOpener(identityDir, io.Discard, io.Discard)
	launcher, args, err := opener.URLCommand("https://example.com")
	if err != nil {
		status := "error"
		if strings.Contains(err.Error(), "isolated browser launcher requires") {
			status = "warn"
		}
		report("host-open", status, err.Error())
		return
	}
	if runtime.GOOS == "darwin" && os.Getenv("HIDEOUT_BROWSER_PATH") == "" {
		appName := os.Getenv("HIDEOUT_BROWSER_APP")
		if appName == "" {
			appName = "Google Chrome"
		}
		if !darwinBrowserAppInstalled(appName) {
			report("host-open", "error", fmt.Sprintf("browser app %q is not installed in a standard Applications directory; install it or set HIDEOUT_BROWSER_PATH to a direct Chromium-compatible browser binary", appName))
			return
		}
	}
	if _, err := exec.LookPath(launcher); err != nil {
		report("host-open", "error", fmt.Sprintf("browser launcher %q is not executable: %v", launcher, err))
		return
	}
	browserProfile := opener.BrowserProfile()
	if browserProfile == "" {
		report("host-open", "error", "isolated browser profile path is missing")
		return
	}
	if !slices.Contains(args, "--user-data-dir="+browserProfile) {
		report("host-open", "error", "URL launcher does not include isolated browser profile")
		return
	}
	report("host-open", "ok", fmt.Sprintf("url=isolated browserProfile=present launcher=%s", filepath.Base(launcher)))
}

func darwinBrowserAppInstalled(appName string) bool {
	home, _ := os.UserHomeDir()
	return darwinBrowserAppInstalledInRoots(appName, home, []string{"/Applications", "/System/Applications"})
}

func darwinBrowserAppInstalledInRoots(appName, home string, roots []string) bool {
	appName = strings.TrimSpace(appName)
	if appName == "" {
		return false
	}
	appName = strings.TrimSuffix(appName, ".app") + ".app"
	candidates := make([]string, 0, len(roots)+1)
	if strings.TrimSpace(home) != "" {
		candidates = append(candidates, filepath.Join(home, "Applications", appName))
	}
	for _, root := range roots {
		candidates = append(candidates, filepath.Join(root, appName))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

func (a app) profile(args []string) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		a.profileUsage()
		return nil
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	switch args[0] {
	case "init":
		if len(args) != 2 {
			return errors.New("usage: hideout profile init <name>")
		}
		p := profile.Default(args[1])
		if err := store.Create(p); err != nil {
			return err
		}
		fmt.Fprintln(a.stdout, store.ProfilePath(args[1]))
		return nil
	case "clone":
		if len(args) != 3 {
			return errors.New("usage: hideout profile clone <source> <name>")
		}
		p, err := store.ClonePolicy(args[1], args[2])
		if err != nil {
			return err
		}
		fmt.Fprintln(a.stdout, store.ProfilePath(p.Name))
		return nil
	case "rotate-identity":
		if len(args) != 2 {
			return errors.New("usage: hideout profile rotate-identity <name>")
		}
		p, err := store.RotateIdentity(args[1])
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "%s identityId=%s previousIdentityId=%s\n", store.ProfilePath(p.Name), p.Metadata["identityId"], p.Metadata["previousIdentityId"])
		return nil
	case "reset":
		if len(args) != 2 {
			return errors.New("usage: hideout profile reset <name>")
		}
		p, err := store.ResetIdentity(args[1])
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "%s identityId=%s previousIdentityId=%s\n", store.ProfilePath(p.Name), p.Metadata["identityId"], p.Metadata["previousIdentityId"])
		return nil
	case "path":
		if len(args) != 2 {
			return errors.New("usage: hideout profile path <name>")
		}
		if err := profile.ValidateName(args[1]); err != nil {
			return err
		}
		fmt.Fprintln(a.stdout, store.ProfilePath(args[1]))
		return nil
	case "fs":
		return a.profileFS(store, args[1:])
	case "env":
		return a.profileEnv(store, args[1:])
	case "home":
		return a.profileHome(store, args[1:])
	case "tools":
		return a.profileTools(store, args[1:])
	case "command-proxy":
		return a.profileCommandProxy(store, args[1:])
	case "command-adapter":
		return a.profileCommandAdapter(store, args[1:])
	default:
		return fmt.Errorf("unknown profile command %q", args[0])
	}
}

func (a app) profileHome(store profile.Store, args []string) error {
	if len(args) == 0 || containsHelpToken(args) {
		a.profileHomeUsage()
		return nil
	}
	if len(args) < 2 {
		return errors.New("usage: hideout profile home <name> import --from <path> --to <relative-path> [--force]")
	}
	name := args[0]
	command := args[1]
	switch command {
	case "import":
		return a.profileHomeImport(store, name, args[2:])
	default:
		return fmt.Errorf("unknown profile home command %q", command)
	}
}

type profileHomeImportOptions struct {
	from  string
	to    string
	force bool
}

type profileHomeImportOutput struct {
	Profile string `json:"profile"`
	Kind    string `json:"kind"`
	Dest    string `json:"dest"`
	Files   int    `json:"files"`
	Bytes   int64  `json:"bytes"`
}

func parseProfileHomeImportOptions(args []string) (profileHomeImportOptions, error) {
	var opts profileHomeImportOptions
	fs := flag.NewFlagSet("profile home import", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.from, "from", "", "host file or directory to import")
	fs.StringVar(&opts.to, "to", "", "relative path inside profile home")
	fs.BoolVar(&opts.force, "force", false, "replace an existing profile home path")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() != 0 {
		return opts, fmt.Errorf("unexpected profile home import argument %q", fs.Arg(0))
	}
	if strings.TrimSpace(opts.from) == "" {
		return opts, errors.New("--from is required")
	}
	if strings.TrimSpace(opts.to) == "" {
		return opts, errors.New("--to is required")
	}
	return opts, nil
}

func (a app) profileHomeImport(store profile.Store, name string, args []string) error {
	if containsHelpToken(args) {
		a.profileHomeUsage()
		return nil
	}
	opts, err := parseProfileHomeImportOptions(args)
	if err != nil {
		return err
	}
	p, err := store.LoadOrInit(name)
	if err != nil {
		return err
	}
	src, err := filepath.Abs(opts.from)
	if err != nil {
		return err
	}
	destRel, err := cleanProfileHomeDest(opts.to)
	if err != nil {
		return err
	}
	dest := filepath.Join(store.ProfileDir(p.Name), "home", destRel)
	homeRoot := filepath.Join(store.ProfileDir(p.Name), "home")
	if !pathWithinRoot(homeRoot, dest) {
		return fmt.Errorf("profile home import destination %q escapes profile home", opts.to)
	}
	if err := ensureProfileHomeParent(homeRoot, dest); err != nil {
		return err
	}
	if _, err := os.Lstat(dest); err == nil {
		if !opts.force {
			return fmt.Errorf("profile home import destination %q already exists; use --force to replace it", destRel)
		}
		if err := os.RemoveAll(dest); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	stats, err := copyProfileHomePath(homeRoot, src, dest)
	if err != nil {
		_ = os.RemoveAll(dest)
		return err
	}
	return writeJSONLine(a.stdout, profileHomeImportOutput{
		Profile: p.Name,
		Kind:    "profile.home.import",
		Dest:    destRel,
		Files:   stats.files,
		Bytes:   stats.bytes,
	})
}

func cleanProfileHomeDest(value string) (string, error) {
	value = filepath.Clean(strings.TrimSpace(value))
	if value == "." || value == string(filepath.Separator) || filepath.IsAbs(value) {
		return "", fmt.Errorf("profile home destination must be a relative path: %q", value)
	}
	if value == ".." || strings.HasPrefix(value, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("profile home destination must stay inside profile home: %q", value)
	}
	return value, nil
}

func pathWithinRoot(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

type profileHomeCopyStats struct {
	files int
	bytes int64
}

func copyProfileHomePath(homeRoot, src, dst string) (profileHomeCopyStats, error) {
	info, err := os.Lstat(src)
	if err != nil {
		return profileHomeCopyStats{}, errors.New("profile home import source is not accessible")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return profileHomeCopyStats{}, fmt.Errorf("profile home import source %q must not be a symlink", filepath.Base(src))
	}
	if info.IsDir() {
		return copyProfileHomeDir(homeRoot, src, dst)
	}
	if !info.Mode().IsRegular() {
		return profileHomeCopyStats{}, fmt.Errorf("profile home import source %q must be a regular file or directory", filepath.Base(src))
	}
	if err := copyProfileHomeFile(homeRoot, src, dst, info); err != nil {
		return profileHomeCopyStats{}, err
	}
	return profileHomeCopyStats{files: 1, bytes: info.Size()}, nil
}

func copyProfileHomeDir(homeRoot, src, dst string) (profileHomeCopyStats, error) {
	entries, err := os.ReadDir(src)
	if err != nil {
		return profileHomeCopyStats{}, errors.New("profile home import source directory is not readable")
	}
	if err := ensureProfileHomeDir(homeRoot, dst); err != nil {
		return profileHomeCopyStats{}, err
	}
	var stats profileHomeCopyStats
	for _, entry := range entries {
		childStats, err := copyProfileHomePath(homeRoot, filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name()))
		if err != nil {
			return profileHomeCopyStats{}, err
		}
		stats.files += childStats.files
		stats.bytes += childStats.bytes
	}
	return stats, nil
}

func copyProfileHomeFile(homeRoot, src, dst string, info os.FileInfo) error {
	if err := ensureProfileHomeFile(homeRoot, dst); err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return errors.New("profile home import source file is not readable")
	}
	mode := info.Mode().Perm()
	if mode == 0 || mode&0o077 != 0 {
		mode = 0o600
	}
	return os.WriteFile(dst, data, mode)
}

func ensureProfileHomeFile(homeRoot, dst string) error {
	if err := ensureProfileHomeParent(homeRoot, dst); err != nil {
		return err
	}
	info, err := os.Lstat(dst)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("profile home import destination must not use a symlink")
	}
	return nil
}

func ensureProfileHomeDir(homeRoot, dst string) error {
	if err := ensureProfileHomeParent(homeRoot, dst); err != nil {
		return err
	}
	info, err := os.Lstat(dst)
	if errors.Is(err, os.ErrNotExist) {
		return os.Mkdir(dst, 0o700)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("profile home import destination must not use a symlink")
	}
	if !info.IsDir() {
		return errors.New("profile home import destination directory is not a directory")
	}
	return nil
}

func ensureProfileHomeParent(homeRoot, dst string) error {
	return ensureProfileHomeDirPath(homeRoot, filepath.Dir(dst))
}

func ensureProfileHomeDirPath(homeRoot, dir string) error {
	homeRoot = filepath.Clean(homeRoot)
	dir = filepath.Clean(dir)
	if !pathWithinRoot(homeRoot, dir) {
		return errors.New("profile home import destination escapes profile home")
	}
	if err := ensureProfileHomeRoot(homeRoot); err != nil {
		return err
	}
	rel, err := filepath.Rel(homeRoot, dir)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	current := homeRoot
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o700); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			managedTarget, ok, err := managedProfileHomeSymlinkTarget(homeRoot, current)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("profile home import destination must not use a symlink")
			}
			current = managedTarget
			continue
		}
		if !info.IsDir() {
			return errors.New("profile home import destination parent is not a directory")
		}
	}
	return nil
}

func managedProfileHomeSymlinkTarget(homeRoot, linkPath string) (string, bool, error) {
	homeRoot = filepath.Clean(homeRoot)
	linkPath = filepath.Clean(linkPath)
	profileDir := filepath.Dir(homeRoot)
	managed := map[string]string{
		filepath.Join(homeRoot, ".config"):         filepath.Join(profileDir, "config"),
		filepath.Join(homeRoot, ".cache"):          filepath.Join(profileDir, "cache"),
		filepath.Join(homeRoot, ".local", "share"): filepath.Join(profileDir, "data"),
	}
	want, ok := managed[linkPath]
	if !ok {
		return "", false, nil
	}
	target, err := os.Readlink(linkPath)
	if err != nil {
		return "", false, err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(linkPath), target)
	}
	target = filepath.Clean(target)
	if target != want {
		return "", false, nil
	}
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(target, 0o700); err != nil {
			return "", false, err
		}
		return target, true, nil
	}
	if err != nil {
		return "", false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", false, errors.New("profile home import managed XDG target must not be a symlink")
	}
	if !info.IsDir() {
		return "", false, errors.New("profile home import managed XDG target is not a directory")
	}
	return target, true, nil
}

func ensureProfileHomeRoot(homeRoot string) error {
	info, err := os.Lstat(homeRoot)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(homeRoot, 0o700)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("profile home import root must not be a symlink")
	}
	if !info.IsDir() {
		return errors.New("profile home import root is not a directory")
	}
	return nil
}

func (a app) profileEnv(store profile.Store, args []string) error {
	if len(args) == 0 || containsHelpToken(args) {
		a.profileEnvUsage()
		return nil
	}
	if len(args) < 2 {
		return errors.New("usage: hideout profile env <name> <list|set|unset|inherit|uninherit|deny|undeny>")
	}
	name := args[0]
	command := args[1]
	switch command {
	case "list":
		if len(args) != 2 {
			return errors.New("usage: hideout profile env <name> list")
		}
		p, err := store.LoadOrInit(name)
		if err != nil {
			return err
		}
		return writeProfileEnv(a.stdout, p)
	case "set":
		if len(args) != 3 {
			return errors.New("usage: hideout profile env <name> set KEY=VALUE")
		}
		key, value, ok := strings.Cut(args[2], "=")
		if !ok || strings.TrimSpace(key) == "" {
			return errors.New("profile env set requires KEY=VALUE")
		}
		return a.profileEnvSet(store, name, strings.TrimSpace(key), value)
	case "unset":
		if len(args) != 3 {
			return errors.New("usage: hideout profile env <name> unset KEY")
		}
		return a.profileEnvUnset(store, name, args[2])
	case "inherit":
		if len(args) != 3 {
			return errors.New("usage: hideout profile env <name> inherit KEY")
		}
		return a.profileEnvListAdd(store, name, "inherit", args[2])
	case "uninherit":
		if len(args) != 3 {
			return errors.New("usage: hideout profile env <name> uninherit KEY")
		}
		return a.profileEnvListRemove(store, name, "inherit", args[2])
	case "deny":
		if len(args) != 3 {
			return errors.New("usage: hideout profile env <name> deny PATTERN")
		}
		return a.profileEnvListAdd(store, name, "deny", args[2])
	case "undeny":
		if len(args) != 3 {
			return errors.New("usage: hideout profile env <name> undeny PATTERN")
		}
		return a.profileEnvListRemove(store, name, "deny", args[2])
	default:
		return fmt.Errorf("unknown profile env command %q", command)
	}
}

type profileEnvListOutput struct {
	Profile string   `json:"profile"`
	Public  []string `json:"public"`
	Inherit []string `json:"inherit"`
	Deny    []string `json:"deny"`
}

type profileEnvChangeOutput struct {
	Profile string `json:"profile"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Removed bool   `json:"removed,omitempty"`
}

func writeProfileEnv(w io.Writer, p profile.Profile) error {
	return writeJSONLine(w, profileEnvListOutput{
		Profile: p.Name,
		Public:  sortedMapKeys(p.Env.Public),
		Inherit: sortedStrings(p.Env.Inherit),
		Deny:    sortedStrings(p.Env.Deny),
	})
}

func (a app) profileEnvSet(store profile.Store, name, key, value string) error {
	p, err := store.LoadOrInit(name)
	if err != nil {
		return err
	}
	if p.Env.Public == nil {
		p.Env.Public = map[string]string{}
	}
	p.Env.Public[key] = value
	if err := store.Save(p); err != nil {
		return err
	}
	return writeJSONLine(a.stdout, profileEnvChangeOutput{Profile: p.Name, Kind: "env.public", Name: key})
}

func (a app) profileEnvUnset(store profile.Store, name, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("env key is required")
	}
	p, err := store.LoadOrInit(name)
	if err != nil {
		return err
	}
	delete(p.Env.Public, key)
	if err := store.Save(p); err != nil {
		return err
	}
	return writeJSONLine(a.stdout, profileEnvChangeOutput{Profile: p.Name, Kind: "env.public", Name: key, Removed: true})
}

func (a app) profileEnvListAdd(store profile.Store, name, kind, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("env key or pattern is required")
	}
	p, err := store.LoadOrInit(name)
	if err != nil {
		return err
	}
	switch kind {
	case "inherit":
		p.Env.Inherit = appendIfMissing(p.Env.Inherit, value)
	case "deny":
		p.Env.Deny = appendIfMissing(p.Env.Deny, value)
	default:
		return fmt.Errorf("unsupported env list kind %q", kind)
	}
	if err := store.Save(p); err != nil {
		return err
	}
	return writeJSONLine(a.stdout, profileEnvChangeOutput{Profile: p.Name, Kind: "env." + kind, Name: value})
}

func (a app) profileEnvListRemove(store profile.Store, name, kind, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("env key or pattern is required")
	}
	p, err := store.LoadOrInit(name)
	if err != nil {
		return err
	}
	switch kind {
	case "inherit":
		p.Env.Inherit = removeString(p.Env.Inherit, value)
	case "deny":
		p.Env.Deny = removeString(p.Env.Deny, value)
	default:
		return fmt.Errorf("unsupported env list kind %q", kind)
	}
	if err := store.Save(p); err != nil {
		return err
	}
	return writeJSONLine(a.stdout, profileEnvChangeOutput{Profile: p.Name, Kind: "env." + kind, Name: value, Removed: true})
}

func (a app) profileTools(store profile.Store, args []string) error {
	if len(args) == 0 || containsHelpToken(args) {
		a.profileToolsUsage()
		return nil
	}
	if len(args) < 2 {
		return errors.New("usage: hideout profile tools <name> <list|expected>")
	}
	name := args[0]
	command := args[1]
	switch command {
	case "list":
		if len(args) != 2 {
			return errors.New("usage: hideout profile tools <name> list")
		}
		p, err := store.LoadOrInit(name)
		if err != nil {
			return err
		}
		return writeProfileTools(a.stdout, p)
	case "expected":
		return a.profileToolExpected(store, name, args[2:])
	case "preset", "npm":
		return unsupportedLegacyToolSupplyError()
	default:
		return fmt.Errorf("unknown profile tools command %q", command)
	}
}

type profileToolsOutput struct {
	Profile          string   `json:"profile"`
	ExpectedCommands []string `json:"expectedCommands,omitempty"`
}

type profileToolChangeOutput struct {
	Profile string `json:"profile"`
	Kind    string `json:"kind"`
	Command string `json:"command,omitempty"`
	Removed bool   `json:"removed,omitempty"`
}

func writeProfileTools(w io.Writer, p profile.Profile) error {
	return writeJSONLine(w, profileToolsOutput{
		Profile:          p.Name,
		ExpectedCommands: sortedStrings(p.Tools.ExpectedCommands),
	})
}

func (a app) profileToolExpected(store profile.Store, name string, args []string) error {
	if containsHelpToken(args) {
		a.profileToolsUsage()
		return nil
	}
	if len(args) == 0 {
		return errors.New("usage: hideout profile tools <name> expected <add|remove|list>")
	}
	action := args[0]
	switch action {
	case "list":
		if len(args) != 1 {
			return errors.New("usage: hideout profile tools <name> expected list")
		}
		p, err := store.LoadOrInit(name)
		if err != nil {
			return err
		}
		return writeProfileTools(a.stdout, p)
	case "add":
		if len(args) != 2 {
			return errors.New("usage: hideout profile tools <name> expected add <command>")
		}
		return a.profileToolExpectedChange(store, name, args[1], false)
	case "remove":
		if len(args) != 2 {
			return errors.New("usage: hideout profile tools <name> expected remove <command>")
		}
		return a.profileToolExpectedChange(store, name, args[1], true)
	default:
		return fmt.Errorf("unknown profile tools expected command %q", action)
	}
}

func (a app) profileToolExpectedChange(store profile.Store, name, command string, remove bool) error {
	command = strings.TrimSpace(command)
	if err := profile.ValidateExpectedCommandName(command); err != nil {
		return err
	}
	p, err := store.LoadOrInit(name)
	if err != nil {
		return err
	}
	removed := false
	if remove {
		before := len(p.Tools.ExpectedCommands)
		p.Tools.ExpectedCommands = removeString(p.Tools.ExpectedCommands, command)
		removed = len(p.Tools.ExpectedCommands) != before
	} else {
		p.Tools.ExpectedCommands = appendIfMissing(p.Tools.ExpectedCommands, command)
	}
	if err := store.Save(p); err != nil {
		return err
	}
	return writeJSONLine(a.stdout, profileToolChangeOutput{
		Profile: p.Name,
		Kind:    "tools.expectedCommands",
		Command: command,
		Removed: remove && removed,
	})
}

func (a app) profileCommandProxy(store profile.Store, args []string) error {
	if len(args) == 0 || containsHelpToken(args) {
		a.profileCommandProxyUsage()
		return nil
	}
	if len(args) < 2 {
		return errors.New("usage: hideout profile command-proxy <name> <list|add-open|remove>")
	}
	name := args[0]
	command := args[1]
	switch command {
	case "list":
		if len(args) != 2 {
			return errors.New("usage: hideout profile command-proxy <name> list")
		}
		p, err := store.LoadOrInit(name)
		if err != nil {
			return err
		}
		return writeProfileCommandProxy(a.stdout, p)
	case "add-open":
		if len(args) != 3 {
			return errors.New("usage: hideout profile command-proxy <name> add-open <command>")
		}
		return a.profileCommandProxyAddOpen(store, name, args[2])
	case "remove":
		if len(args) != 3 {
			return errors.New("usage: hideout profile command-proxy <name> remove <command>")
		}
		return a.profileCommandProxyRemove(store, name, args[2])
	default:
		return fmt.Errorf("unknown profile command-proxy command %q", command)
	}
}

func (a app) profileCommandAdapter(store profile.Store, args []string) error {
	if len(args) == 0 || containsHelpToken(args) {
		a.profileCommandAdapterUsage()
		return nil
	}
	if len(args) < 2 {
		return errors.New("usage: hideout profile command-adapter <name> <list|add-local|add-builtin-root-sensitive|enable|disable|refresh-digest|remove>")
	}
	name := args[0]
	command := args[1]
	core := manager.New(store)
	switch command {
	case "list":
		if len(args) != 2 {
			return errors.New("usage: hideout profile command-adapter <name> list")
		}
		p, err := store.LoadOrInit(name)
		if err != nil {
			return err
		}
		return writeProfileCommandAdapters(a.stdout, p)
	case "add-local":
		opts, err := parseProfileCommandAdapterAddLocal(name, args[2:])
		if err != nil {
			return err
		}
		plan, err := core.PlanCommandAdapter(opts)
		if err != nil {
			return err
		}
		result, err := core.ApplyCommandAdapter(plan)
		if err != nil {
			return err
		}
		return writeJSONLine(a.stdout, result)
	case "add-builtin-root-sensitive":
		fs := flag.NewFlagSet("profile command-adapter add-builtin-root-sensitive", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		id := fs.String("id", cmdadapter.BuiltinRootSensitiveID, "adapter id")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return fmt.Errorf("unexpected command-adapter argument %q", fs.Arg(0))
		}
		plan, err := core.PlanCommandAdapter(manager.CommandAdapterOptions{
			ProfileName: name,
			Operation:   "add-builtin-root-sensitive",
			AdapterID:   *id,
			Builtin:     cmdadapter.BuiltinRootSensitiveKey,
		})
		if err != nil {
			return err
		}
		result, err := core.ApplyCommandAdapter(plan)
		if err != nil {
			return err
		}
		return writeJSONLine(a.stdout, result)
	case "enable", "disable", "refresh-digest", "remove":
		if len(args) != 3 {
			return fmt.Errorf("usage: hideout profile command-adapter <name> %s <id>", command)
		}
		plan, err := core.PlanCommandAdapter(manager.CommandAdapterOptions{
			ProfileName: name,
			Operation:   command,
			AdapterID:   args[2],
		})
		if err != nil {
			return err
		}
		result, err := core.ApplyCommandAdapter(plan)
		if err != nil {
			return err
		}
		return writeJSONLine(a.stdout, result)
	default:
		return fmt.Errorf("unknown profile command-adapter command %q", command)
	}
}

func parseProfileCommandAdapterAddLocal(profileName string, args []string) (manager.CommandAdapterOptions, error) {
	fs := flag.NewFlagSet("profile command-adapter add-local", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	id := fs.String("id", "", "adapter id")
	path := fs.String("path", "", "adapter script path")
	entrypoint := fs.String("entrypoint", cmdadapter.DefaultEntrypoint, "adapter entrypoint")
	var commands stringListFlag
	var capabilities stringListFlag
	fs.Var(&commands, "command", "command symbol to route; may be repeated")
	fs.Var(&capabilities, "capability", "proposal capability; may be repeated")
	if err := fs.Parse(args); err != nil {
		return manager.CommandAdapterOptions{}, err
	}
	if fs.NArg() != 0 {
		return manager.CommandAdapterOptions{}, fmt.Errorf("unexpected command-adapter argument %q", fs.Arg(0))
	}
	return manager.CommandAdapterOptions{
		ProfileName:                 profileName,
		Operation:                   "add-local",
		AdapterID:                   *id,
		Path:                        *path,
		Entrypoint:                  *entrypoint,
		Commands:                    []string(commands),
		AllowedProposalCapabilities: []string(capabilities),
	}, nil
}

func (a app) adapterPackCommand(args []string) error {
	if len(args) == 0 || containsHelpToken(args) {
		a.adapterPackUsage()
		return nil
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	core := manager.New(store)
	switch args[0] {
	case "install", "upgrade":
		opts, err := parseAdapterPackSource(args[0], args[1:])
		if err != nil {
			return err
		}
		opts.Operation = args[0]
		plan, err := core.PlanAdapterPack(opts)
		if err != nil {
			return err
		}
		result, err := core.ApplyAdapterPack(plan)
		if err != nil {
			return err
		}
		return writeJSONLine(a.stdout, result)
	case "list":
		if len(args) != 1 {
			return errors.New("usage: hideout adapter-pack list")
		}
		packs, err := core.ListAdapterPacks()
		if err != nil {
			return err
		}
		return writeJSONLine(a.stdout, map[string]any{"adapterPacks": packs})
	case "inspect":
		if len(args) != 2 {
			return errors.New("usage: hideout adapter-pack inspect <pack-id>")
		}
		entry, err := core.InspectAdapterPack(args[1])
		if err != nil {
			return err
		}
		return writeJSONLine(a.stdout, entry)
	case "test":
		opts, err := parseAdapterPackTest(args[1:])
		if err != nil {
			return err
		}
		plan, err := core.PlanAdapterPack(opts)
		if err != nil {
			return err
		}
		result, err := core.ApplyAdapterPack(plan)
		if err != nil {
			return err
		}
		return writeJSONLine(a.stdout, result)
	case "enable":
		opts, err := parseAdapterPackEnable(args[1:])
		if err != nil {
			return err
		}
		plan, err := core.PlanAdapterPack(opts)
		if err != nil {
			return err
		}
		result, err := core.ApplyAdapterPack(plan)
		if err != nil {
			return err
		}
		return writeJSONLine(a.stdout, result)
	case "disable":
		opts, err := parseAdapterPackDisable(args[1:])
		if err != nil {
			return err
		}
		plan, err := core.PlanAdapterPack(opts)
		if err != nil {
			return err
		}
		result, err := core.ApplyAdapterPack(plan)
		if err != nil {
			return err
		}
		return writeJSONLine(a.stdout, result)
	case "revoke":
		if len(args) != 2 {
			return errors.New("usage: hideout adapter-pack revoke <pack-id>")
		}
		plan, err := core.PlanAdapterPack(manager.AdapterPackOptions{Operation: "revoke", PackID: args[1]})
		if err != nil {
			return err
		}
		result, err := core.ApplyAdapterPack(plan)
		if err != nil {
			return err
		}
		return writeJSONLine(a.stdout, result)
	default:
		return fmt.Errorf("unknown adapter-pack command %q", args[0])
	}
}

func parseAdapterPackSource(command string, args []string) (manager.AdapterPackOptions, error) {
	fs := flag.NewFlagSet("adapter-pack "+command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("path", "", "local adapter pack directory")
	gitURL := fs.String("git", "", "git repository URL")
	commit := fs.String("commit", "", "exact git commit")
	if err := fs.Parse(args); err != nil {
		return manager.AdapterPackOptions{}, err
	}
	if fs.NArg() != 0 {
		return manager.AdapterPackOptions{}, fmt.Errorf("unexpected adapter-pack argument %q", fs.Arg(0))
	}
	if *path != "" && *gitURL != "" {
		return manager.AdapterPackOptions{}, errors.New("choose either --path or --git")
	}
	if *gitURL != "" {
		return manager.AdapterPackOptions{SourceKind: adapterpack.SourceGit, SourceURL: *gitURL, SourceCommit: *commit}, nil
	}
	return manager.AdapterPackOptions{SourceKind: adapterpack.SourceLocal, SourcePath: *path}, nil
}

func parseAdapterPackTest(args []string) (manager.AdapterPackOptions, error) {
	fs := flag.NewFlagSet("adapter-pack test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	revision := fs.String("revision", "", "revision id")
	if err := fs.Parse(args); err != nil {
		return manager.AdapterPackOptions{}, err
	}
	if fs.NArg() != 1 {
		return manager.AdapterPackOptions{}, errors.New("usage: hideout adapter-pack test [--revision <id>] <pack-id>")
	}
	return manager.AdapterPackOptions{Operation: "test", PackID: fs.Arg(0), RevisionID: *revision}, nil
}

func parseAdapterPackEnable(args []string) (manager.AdapterPackOptions, error) {
	fs := flag.NewFlagSet("adapter-pack enable", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	profileName := fs.String("profile", "default", "profile name")
	packID := fs.String("pack", "", "pack id")
	revisionID := fs.String("revision", "", "revision id")
	adapterID := fs.String("adapter", "", "pack adapter id")
	commandAdapterID := fs.String("id", "", "profile command adapter id")
	var commands stringListFlag
	var capabilities stringListFlag
	fs.Var(&commands, "command", "command symbol to route; may be repeated")
	fs.Var(&capabilities, "capability", "proposal capability; may be repeated")
	if err := fs.Parse(args); err != nil {
		return manager.AdapterPackOptions{}, err
	}
	if fs.NArg() != 0 {
		return manager.AdapterPackOptions{}, fmt.Errorf("unexpected adapter-pack argument %q", fs.Arg(0))
	}
	return manager.AdapterPackOptions{
		Operation:                   "enable",
		ProfileName:                 *profileName,
		PackID:                      *packID,
		RevisionID:                  *revisionID,
		AdapterID:                   *adapterID,
		CommandAdapterID:            *commandAdapterID,
		Commands:                    []string(commands),
		AllowedProposalCapabilities: []string(capabilities),
	}, nil
}

func parseAdapterPackDisable(args []string) (manager.AdapterPackOptions, error) {
	fs := flag.NewFlagSet("adapter-pack disable", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	profileName := fs.String("profile", "default", "profile name")
	if err := fs.Parse(args); err != nil {
		return manager.AdapterPackOptions{}, err
	}
	if fs.NArg() != 1 {
		return manager.AdapterPackOptions{}, errors.New("usage: hideout adapter-pack disable --profile <name> <command-adapter-id>")
	}
	return manager.AdapterPackOptions{Operation: "disable", ProfileName: *profileName, CommandAdapterID: fs.Arg(0)}, nil
}

type profileCommandAdapterOutput struct {
	Profile  string                               `json:"profile"`
	Adapters []profileCommandAdapterAdapterOutput `json:"adapters"`
}

type profileCommandAdapterAdapterOutput struct {
	ID                          string   `json:"id"`
	Enabled                     bool     `json:"enabled"`
	Path                        string   `json:"path,omitempty"`
	Digest                      string   `json:"digest,omitempty"`
	Entrypoint                  string   `json:"entrypoint,omitempty"`
	Commands                    []string `json:"commands,omitempty"`
	AllowedProposalCapabilities []string `json:"allowedProposalCapabilities,omitempty"`
	Builtin                     string   `json:"builtin,omitempty"`
	Description                 string   `json:"description,omitempty"`
}

func writeProfileCommandAdapters(w io.Writer, p profile.Profile) error {
	ids := make([]string, 0, len(p.CommandAdapters.Adapters))
	for id := range p.CommandAdapters.Adapters {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]profileCommandAdapterAdapterOutput, 0, len(ids))
	for _, id := range ids {
		adapter := p.CommandAdapters.Adapters[id]
		commands := append([]string(nil), adapter.Commands...)
		sort.Strings(commands)
		capabilities := append([]string(nil), adapter.AllowedProposalCapabilities...)
		sort.Strings(capabilities)
		out = append(out, profileCommandAdapterAdapterOutput{
			ID:                          id,
			Enabled:                     adapter.Enabled,
			Path:                        adapter.Path,
			Digest:                      adapter.Digest,
			Entrypoint:                  adapter.Entrypoint,
			Commands:                    commands,
			AllowedProposalCapabilities: capabilities,
			Builtin:                     adapter.Builtin,
			Description:                 adapter.Description,
		})
	}
	return writeJSONLine(w, profileCommandAdapterOutput{Profile: p.Name, Adapters: out})
}

type profileCommandProxyCommandOutput struct {
	Name       string `json:"name"`
	Route      string `json:"route"`
	Action     string `json:"action"`
	ArgvSchema string `json:"argvSchema,omitempty"`
}

type profileCommandProxyOutput struct {
	Profile  string                             `json:"profile"`
	Commands []profileCommandProxyCommandOutput `json:"commands"`
}

type profileCommandProxyChangeOutput struct {
	Profile    string `json:"profile"`
	Command    string `json:"command"`
	Route      string `json:"route,omitempty"`
	Action     string `json:"action,omitempty"`
	ArgvSchema string `json:"argvSchema,omitempty"`
	Added      bool   `json:"added,omitempty"`
	Updated    bool   `json:"updated,omitempty"`
	Removed    bool   `json:"removed,omitempty"`
}

func writeProfileCommandProxy(w io.Writer, p profile.Profile) error {
	names := make([]string, 0, len(p.CommandProxy.Commands))
	for name := range p.CommandProxy.Commands {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]profileCommandProxyCommandOutput, 0, len(names))
	for _, name := range names {
		command := p.CommandProxy.Commands[name]
		out = append(out, profileCommandProxyCommandOutput{
			Name:       name,
			Route:      command.Route,
			Action:     command.Action,
			ArgvSchema: command.ArgvSchema,
		})
	}
	return writeJSONLine(w, profileCommandProxyOutput{Profile: p.Name, Commands: out})
}

func (a app) profileCommandProxyAddOpen(store profile.Store, name, commandName string) error {
	commandName = strings.TrimSpace(commandName)
	if commandName == "" {
		return errors.New("command is required")
	}
	next := profile.CommandProxyCommand{
		Route:      cmdproxy.RouteHostBroker,
		Action:     cmdproxy.ActionHostOpen,
		ArgvSchema: cmdproxy.ArgvSchemaOpenV1,
	}
	if err := validateProfileCommandProxyCommandName(commandName, next); err != nil {
		return err
	}
	p, err := store.LoadOrInit(name)
	if err != nil {
		return err
	}
	if p.CommandProxy.Commands == nil {
		p.CommandProxy.Commands = map[string]profile.CommandProxyCommand{}
	}
	previous, exists := p.CommandProxy.Commands[commandName]
	p.CommandProxy.Commands[commandName] = next
	if err := store.Save(p); err != nil {
		return err
	}
	return writeJSONLine(a.stdout, profileCommandProxyChangeOutput{
		Profile:    p.Name,
		Command:    commandName,
		Route:      next.Route,
		Action:     next.Action,
		ArgvSchema: next.ArgvSchema,
		Added:      !exists,
		Updated:    exists && previous != next,
	})
}

func (a app) profileCommandProxyRemove(store profile.Store, name, commandName string) error {
	commandName = strings.TrimSpace(commandName)
	if commandName == "" {
		return errors.New("command is required")
	}
	if commandName == "open" {
		return errors.New("command-proxy open is required and cannot be removed")
	}
	if err := validateProfileCommandProxyCommandName(commandName, profile.CommandProxyCommand{
		Route:      cmdproxy.RouteHostBroker,
		Action:     cmdproxy.ActionHostOpen,
		ArgvSchema: cmdproxy.ArgvSchemaOpenV1,
	}); err != nil {
		return err
	}
	p, err := store.LoadOrInit(name)
	if err != nil {
		return err
	}
	_, exists := p.CommandProxy.Commands[commandName]
	delete(p.CommandProxy.Commands, commandName)
	if err := store.Save(p); err != nil {
		return err
	}
	return writeJSONLine(a.stdout, profileCommandProxyChangeOutput{
		Profile: p.Name,
		Command: commandName,
		Removed: exists,
	})
}

func validateProfileCommandProxyCommandName(commandName string, command profile.CommandProxyCommand) error {
	probe := profile.Default("__command_proxy_validation__")
	probe.CommandProxy.Commands[commandName] = command
	return probe.Validate()
}

func (a app) profileFS(store profile.Store, args []string) error {
	if len(args) == 0 || containsHelpToken(args) {
		a.profileFSUsage()
		return nil
	}
	if len(args) < 2 {
		return errors.New("usage: hideout profile fs <name> <list|add|deny|remove>")
	}
	name := args[0]
	command := args[1]
	switch command {
	case "list":
		if len(args) != 2 {
			return errors.New("usage: hideout profile fs <name> list")
		}
		p, err := store.LoadOrInit(name)
		if err != nil {
			return err
		}
		return writeProfileFSRules(a.stdout, p)
	case "add":
		return a.profileFSAdd(store, name, args[2:], false)
	case "deny":
		return a.profileFSAdd(store, name, args[2:], true)
	case "remove":
		if len(args) != 3 {
			return errors.New("usage: hideout profile fs <name> remove <rule-id>")
		}
		return a.profileFSRemove(store, name, args[2])
	default:
		return fmt.Errorf("unknown profile fs command %q", command)
	}
}

type profileFSAddOptions struct {
	ruleValue string
	reason    string
}

func parseProfileFSAddOptions(args []string, deny bool) (profileFSAddOptions, error) {
	var opts profileFSAddOptions
	fs := flag.NewFlagSet("profile fs", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if deny {
		fs.StringVar(&opts.ruleValue, "no-fs", "", "profile HostFS deny rule")
	} else {
		fs.StringVar(&opts.ruleValue, "fs", "", "profile HostFS allow rule")
	}
	fs.StringVar(&opts.reason, "reason", "", "reason for this HostFS rule")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() != 0 {
		return opts, fmt.Errorf("unexpected profile fs argument %q", fs.Arg(0))
	}
	flagName := "--fs"
	if deny {
		flagName = "--no-fs"
	}
	if strings.TrimSpace(opts.ruleValue) == "" {
		return opts, fmt.Errorf("%s is required", flagName)
	}
	if strings.TrimSpace(opts.reason) == "" {
		return opts, errors.New("--reason is required")
	}
	return opts, nil
}

func (a app) profileFSAdd(store profile.Store, name string, args []string, deny bool) error {
	if containsHelpToken(args) {
		a.profileFSUsage()
		return nil
	}
	opts, err := parseProfileFSAddOptions(args, deny)
	if err != nil {
		return err
	}
	p, err := store.LoadOrInit(name)
	if err != nil {
		return err
	}
	flagName := "--fs"
	reasonPrefix := "profile HostFS allow"
	if deny {
		flagName = "--no-fs"
		reasonPrefix = "profile HostFS deny"
	}
	rule, err := parseHostFSRuleFlag(hostFSFlagInput{
		flagName: flagName,
		value:    opts.ruleValue,
		reason:   opts.reason,
	})
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	rule.ID, err = hostfs.NewRuleID(p.HostFS)
	if err != nil {
		return err
	}
	rule.CreatedAt = &now
	rule.Reason = opts.reason
	if deny {
		p.HostFS.Deny = append(p.HostFS.Deny, rule)
	} else {
		p.HostFS.Grants = append(p.HostFS.Grants, rule)
	}
	if err := store.Save(p); err != nil {
		return err
	}
	item := profileFSRuleOutputFromRule(rule, deny)
	item.Profile = p.Name
	item.Reason = strings.TrimSpace(item.Reason)
	item.Kind = reasonPrefix
	return writeJSONLine(a.stdout, item)
}

func (a app) profileFSRemove(store profile.Store, name, ruleID string) error {
	if strings.TrimSpace(ruleID) == "" {
		return errors.New("rule-id is required")
	}
	p, err := store.LoadOrInit(name)
	if err != nil {
		return err
	}
	var removed *profileFSRuleOutput
	p.HostFS.Grants, removed = removeHostFSRule(p.HostFS.Grants, ruleID, false)
	if removed == nil {
		p.HostFS.Deny, removed = removeHostFSRule(p.HostFS.Deny, ruleID, true)
	}
	if removed == nil {
		return fmt.Errorf("profile HostFS rule %q not found", ruleID)
	}
	if err := store.Save(p); err != nil {
		return err
	}
	removed.Profile = p.Name
	removed.Removed = true
	return writeJSONLine(a.stdout, removed)
}

type profileFSRuleOutput struct {
	Profile   string       `json:"profile,omitempty"`
	ID        string       `json:"id"`
	Kind      string       `json:"kind"`
	Effect    string       `json:"effect"`
	HostPath  string       `json:"hostPath"`
	Ops       []hostfs.Op  `json:"ops,omitempty"`
	Overlay   bool         `json:"overlay,omitempty"`
	Scope     hostfs.Scope `json:"scope"`
	Reason    string       `json:"reason"`
	CreatedAt string       `json:"createdAt,omitempty"`
	Removed   bool         `json:"removed,omitempty"`
}

func writeProfileFSRules(w io.Writer, p profile.Profile) error {
	out := struct {
		Profile string                `json:"profile"`
		Grants  []profileFSRuleOutput `json:"grants"`
		Deny    []profileFSRuleOutput `json:"deny"`
	}{
		Profile: p.Name,
		Grants:  make([]profileFSRuleOutput, 0, len(p.HostFS.Grants)),
		Deny:    make([]profileFSRuleOutput, 0, len(p.HostFS.Deny)),
	}
	for _, rule := range p.HostFS.Grants {
		out.Grants = append(out.Grants, profileFSRuleOutputFromRule(rule, false))
	}
	for _, rule := range p.HostFS.Deny {
		out.Deny = append(out.Deny, profileFSRuleOutputFromRule(rule, true))
	}
	return writeJSONLine(w, out)
}

func profileFSRuleOutputFromRule(rule hostfs.Rule, deny bool) profileFSRuleOutput {
	effect := "allow"
	kind := "profile HostFS allow"
	if deny {
		effect = "deny"
		kind = "profile HostFS deny"
	}
	createdAt := ""
	if rule.CreatedAt != nil {
		createdAt = rule.CreatedAt.Format(time.RFC3339Nano)
	}
	return profileFSRuleOutput{
		ID:        rule.ID,
		Kind:      kind,
		Effect:    effect,
		HostPath:  rule.HostPath,
		Ops:       append([]hostfs.Op(nil), rule.Ops...),
		Overlay:   rule.Overlay,
		Scope:     rule.Scope,
		Reason:    rule.Reason,
		CreatedAt: createdAt,
	}
}

func removeHostFSRule(rules []hostfs.Rule, id string, deny bool) ([]hostfs.Rule, *profileFSRuleOutput) {
	for i, rule := range rules {
		if rule.ID != id {
			continue
		}
		removed := profileFSRuleOutputFromRule(rule, deny)
		out := append([]hostfs.Rule(nil), rules[:i]...)
		out = append(out, rules[i+1:]...)
		return out, &removed
	}
	return rules, nil
}

func writeJSONLine(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func appendIfMissing(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func removeString(values []string, value string) []string {
	out := values[:0]
	for _, existing := range values {
		if existing == value {
			continue
		}
		out = append(out, existing)
	}
	return out
}

func (a app) cleanup(args []string) error {
	fs := flag.NewFlagSet("cleanup", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sessionID := fs.String("session", "", "session id")
	dryRun := fs.Bool("dry-run", false, "show files that would be removed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	result, err := session.CleanupEphemeral(store.Root, *sessionID, *dryRun)
	if err != nil {
		return err
	}
	mode := "removed"
	if *dryRun {
		mode = "would remove"
	}
	secretState := "removed"
	if *dryRun {
		secretState = "would-remove"
	}
	fmt.Fprintf(a.stdout, "cleanup: sessions=%d %s=%d audit=preserved secretState=%s\n", result.Sessions, mode, len(result.Removed), secretState)
	for _, path := range result.Removed {
		fmt.Fprintf(a.stdout, "%s: %s\n", mode, path)
	}
	return nil
}

type auditShowOptions struct {
	session  string
	profile  string
	action   string
	decision string
	limit    int
	json     bool
}

type auditExportOptions struct {
	source                  string
	session                 string
	profile                 string
	action                  string
	decision                string
	limit                   int
	bundle                  string
	doctorReport            string
	from                    string
	out                     string
	redact                  stringListFlag
	policyProfile           string
	acknowledgeFullFidelity bool
	share                   bool
}

func parseAuditShowOptions(args []string) (auditShowOptions, error) {
	fs := flag.NewFlagSet("audit show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := auditShowOptions{limit: 50}
	fs.StringVar(&opts.session, "session", "", "session id")
	fs.StringVar(&opts.profile, "profile", "", "profile name")
	fs.StringVar(&opts.action, "action", "", "audit action")
	fs.StringVar(&opts.decision, "decision", "", "audit decision")
	fs.IntVar(&opts.limit, "limit", opts.limit, "maximum events")
	fs.BoolVar(&opts.json, "json", false, "print redacted JSON events")
	if err := fs.Parse(args); err != nil {
		return auditShowOptions{}, err
	}
	if fs.NArg() != 0 {
		return auditShowOptions{}, errors.New("usage: hideout audit show [--session <id>] [--profile <name>] [--action <name>] [--decision <value>] [--limit N] [--json]")
	}
	if opts.limit <= 0 {
		return auditShowOptions{}, errors.New("--limit must be greater than zero")
	}
	return opts, nil
}

func (a app) auditCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: hideout audit show|export")
	}
	switch args[0] {
	case "show":
		return a.auditShow(args[1:])
	case "export":
		return a.auditExport(args[1:])
	default:
		return fmt.Errorf("unknown audit command %q", args[0])
	}
}

func (a app) auditShow(args []string) error {
	opts, err := parseAuditShowOptions(args)
	if err != nil {
		return err
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	events, err := manager.New(store).AuditEvents(manager.AuditEventFilter{
		Session:  opts.session,
		Profile:  opts.profile,
		Action:   opts.action,
		Decision: opts.decision,
		Limit:    opts.limit,
	})
	if err != nil {
		return err
	}
	if opts.json {
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(events)
	}
	writeAuditShowEvents(a.stdout, events)
	return nil
}

func parseAuditExportOptions(args []string) (auditExportOptions, error) {
	fs := flag.NewFlagSet("audit export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := auditExportOptions{source: string(exportboundary.SourceAudit), limit: 50}
	fs.StringVar(&opts.source, "source", opts.source, "audit, bundle, boundary-summary, or doctor-report")
	fs.StringVar(&opts.session, "session", "", "session id")
	fs.StringVar(&opts.profile, "profile", "", "profile name")
	fs.StringVar(&opts.action, "action", "", "audit action")
	fs.StringVar(&opts.decision, "decision", "", "audit decision")
	fs.IntVar(&opts.limit, "limit", opts.limit, "maximum audit events")
	fs.StringVar(&opts.bundle, "bundle", "", "release evidence bundle directory or manifest")
	fs.StringVar(&opts.doctorReport, "doctor-report", "", "redacted doctor report path")
	fs.StringVar(&opts.from, "from", "", "run audit path for boundary-summary")
	fs.StringVar(&opts.out, "out", "", "local export artifact path")
	fs.Var(&opts.redact, "redact", "detail field selector to redact; may be repeated")
	fs.StringVar(&opts.policyProfile, "policy-profile", "", "profile whose audit.redact policy applies")
	fs.BoolVar(&opts.acknowledgeFullFidelity, "acknowledge-full-fidelity", false, "include residual user data after configured redaction")
	fs.BoolVar(&opts.share, "share", false, "stage a leaving-machine share decision instead of writing immediately")
	if err := fs.Parse(args); err != nil {
		return auditExportOptions{}, err
	}
	if fs.NArg() != 0 {
		return auditExportOptions{}, errors.New("usage: hideout audit export --source audit|bundle|boundary-summary|doctor-report --out <path>")
	}
	if opts.limit <= 0 {
		return auditExportOptions{}, errors.New("--limit must be greater than zero")
	}
	return opts, nil
}

func (a app) auditExport(args []string) error {
	opts, err := parseAuditExportOptions(args)
	if err != nil {
		return err
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	core := manager.New(store)
	managerOpts := manager.ExportOptions{
		Source:                  exportboundary.SourceKind(opts.source),
		Session:                 opts.session,
		Profile:                 opts.profile,
		Action:                  opts.action,
		Decision:                opts.decision,
		Limit:                   opts.limit,
		BundlePath:              opts.bundle,
		DoctorReportPath:        opts.doctorReport,
		From:                    opts.from,
		Out:                     opts.out,
		Redact:                  append([]string(nil), opts.redact...),
		PolicyProfile:           opts.policyProfile,
		AcknowledgeFullFidelity: opts.acknowledgeFullFidelity,
		Share:                   opts.share,
		Commit:                  Commit,
	}
	plan, err := core.PlanExport(managerOpts)
	if err != nil {
		if metaErr := core.RecordExportFailure(managerOpts, err.Error()); metaErr != nil {
			return metaErr
		}
		return err
	}
	fmt.Fprint(a.stdout, plan.Review.Text())
	if opts.share {
		if plan.DecisionID == "" {
			return errors.New("share export did not create a decision")
		}
		fmt.Fprintf(a.stdout, "share decision: %s\n", plan.DecisionID)
		fmt.Fprintf(a.stdout, "next: hideout decision claim %s\n", plan.DecisionID)
		return nil
	}
	if plan.Review.DecisionRequired {
		if !stdinIsTerminal() {
			return errors.New("user data is present; choose --redact or --acknowledge-full-fidelity")
		}
		ok, err := confirmExport()
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("export refused by operator")
		}
		managerOpts.InteractiveConfirmed = true
	}
	result, err := core.ApplyExport(plan, managerOpts)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "export: %s\n", result.ArtifactPath)
	if result.MetaAuditPath != "" {
		fmt.Fprintf(a.stdout, "meta-audit: %s\n", result.MetaAuditPath)
	}
	return nil
}

func (a app) hostfsCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: hideout hostfs write status|plan|claim|apply|discard")
	}
	switch args[0] {
	case "write":
		return a.hostfsWriteCommand(args[1:])
	default:
		return fmt.Errorf("unknown hostfs command %q", args[0])
	}
}

func (a app) hostfsWriteCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: hideout hostfs write status|plan|claim|apply|discard")
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	core := manager.New(store)
	switch args[0] {
	case "status":
		return a.hostfsWriteStatus(core, args[1:])
	case "plan":
		return a.hostfsWritePlan(core, args[1:])
	case "claim":
		return a.hostfsWriteClaim(core, args[1:])
	case "apply":
		return a.hostfsWriteApply(core, args[1:])
	case "discard":
		return a.hostfsWriteDiscard(core, args[1:])
	default:
		return fmt.Errorf("unknown hostfs write command %q", args[0])
	}
}

func (a app) hostfsWriteStatus(core manager.Core, args []string) error {
	fs := flag.NewFlagSet("hostfs write status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sessionID := fs.String("session", "", "session id")
	state := fs.String("state", "", "decision state")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: hideout hostfs write status [--session <id>] [--state <state>]")
	}
	status, err := core.HostFSWriteStatus(manager.HostFSWriteStatusRequest{Session: *sessionID, State: *state})
	if err != nil {
		return err
	}
	return writeIndentedJSON(a.stdout, status)
}

func (a app) hostfsWritePlan(core manager.Core, args []string) error {
	fs := flag.NewFlagSet("hostfs write plan", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	operationID := fs.String("operation", "", "operation id")
	includePreview := fs.Bool("preview", true, "include staged preview")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("usage: hideout hostfs write plan [--preview=false] <operation-id>")
	}
	if *operationID == "" && fs.NArg() == 1 {
		*operationID = fs.Arg(0)
	}
	plan, err := core.PlanHostFSWrite(manager.HostFSWritePlanRequest{OperationID: *operationID, IncludePreview: *includePreview})
	if err != nil {
		return err
	}
	return writeIndentedJSON(a.stdout, plan)
}

func (a app) hostfsWriteClaim(core manager.Core, args []string) error {
	fs := flag.NewFlagSet("hostfs write claim", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	surface := fs.String("surface", "cli", "claiming surface")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: hideout hostfs write claim [--surface cli|tui|webui] <decision-id>")
	}
	claim, err := core.ClaimHostFSWrite(manager.HostFSWriteClaimRequest{
		DecisionID:      fs.Arg(0),
		ExpectedVersion: manager.HostFSWritePlanVersion,
		Surface:         *surface,
	})
	if err != nil {
		return err
	}
	return writeIndentedJSON(a.stdout, claim)
}

func (a app) hostfsWriteApply(core manager.Core, args []string) error {
	fs := flag.NewFlagSet("hostfs write apply", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	claimToken := fs.String("claim-token", "", "claim token returned by claim")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: hideout hostfs write apply --claim-token <token> <decision-id>")
	}
	result, err := core.ApplyHostFSWrite(manager.HostFSWriteApplyRequest{
		DecisionID:      fs.Arg(0),
		ExpectedVersion: manager.HostFSWritePlanVersion,
		ClaimToken:      *claimToken,
	})
	if err != nil {
		return err
	}
	return writeIndentedJSON(a.stdout, result)
}

func (a app) hostfsWriteDiscard(core manager.Core, args []string) error {
	fs := flag.NewFlagSet("hostfs write discard", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	claimToken := fs.String("claim-token", "", "claim token returned by claim")
	reason := fs.String("reason", "operator-denied", "discard reason")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: hideout hostfs write discard --claim-token <token> [--reason <text>] <decision-id>")
	}
	result, err := core.DiscardHostFSWrite(manager.HostFSWriteDiscardRequest{
		DecisionID:      fs.Arg(0),
		ExpectedVersion: manager.HostFSWritePlanVersion,
		ClaimToken:      *claimToken,
		Reason:          *reason,
	})
	if err != nil {
		return err
	}
	return writeIndentedJSON(a.stdout, result)
}

func (a app) decisionCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: hideout decision list|inspect|claim|approve|deny|watch")
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	core := manager.New(store)
	switch args[0] {
	case "list":
		return a.decisionList(core, args[1:])
	case "inspect":
		return a.decisionInspect(core, args[1:])
	case "claim":
		return a.decisionClaim(core, args[1:])
	case "approve":
		return a.decisionResolve(core, args[1:], true)
	case "deny":
		return a.decisionResolve(core, args[1:], false)
	case "watch":
		return a.decisionWatch(core, args[1:])
	default:
		return fmt.Errorf("unknown decision command %q", args[0])
	}
}

func (a app) decisionList(core manager.Core, args []string) error {
	fs := flag.NewFlagSet("decision list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	kind := fs.String("kind", "", "decision kind")
	state := fs.String("state", "", "decision state")
	profileName := fs.String("profile", "", "profile")
	sessionID := fs.String("session", "", "session id")
	includeTerminal := fs.Bool("include-terminal", false, "include terminal decisions")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: hideout decision list [--kind <kind>] [--state <state>] [--include-terminal]")
	}
	decisions, err := core.ListDecisions(manager.DecisionListRequest{
		Kind:            *kind,
		State:           *state,
		Profile:         *profileName,
		Session:         *sessionID,
		IncludeTerminal: *includeTerminal,
	})
	if err != nil {
		return err
	}
	return writeIndentedJSON(a.stdout, decisions)
}

func (a app) decisionInspect(core manager.Core, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: hideout decision inspect <decision-id>")
	}
	d, err := core.InspectDecision(args[0])
	if err != nil {
		return err
	}
	return writeIndentedJSON(a.stdout, d)
}

func (a app) decisionClaim(core manager.Core, args []string) error {
	fs := flag.NewFlagSet("decision claim", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	surface := fs.String("surface", "cli", "claiming surface")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: hideout decision claim [--surface cli|tui|webui] <decision-id>")
	}
	claim, err := core.ClaimDecision(manager.DecisionClaimRequest{
		DecisionID:      fs.Arg(0),
		ExpectedVersion: "hideout.decision/v1",
		Surface:         *surface,
	})
	if err != nil {
		return err
	}
	return writeIndentedJSON(a.stdout, claim)
}

func (a app) decisionResolve(core manager.Core, args []string, approve bool) error {
	name := "decision deny"
	if approve {
		name = "decision approve"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	claimToken := fs.String("claim-token", "", "claim token returned by claim")
	reason := fs.String("reason", "operator-denied", "decision reason")
	if approve {
		*reason = "operator-approved"
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		if approve {
			return errors.New("usage: hideout decision approve --claim-token <token> <decision-id>")
		}
		return errors.New("usage: hideout decision deny --claim-token <token> [--reason <text>] <decision-id>")
	}
	req := manager.DecisionResolveRequest{
		DecisionID:      fs.Arg(0),
		ExpectedVersion: "hideout.decision/v1",
		ClaimToken:      *claimToken,
		Reason:          *reason,
	}
	var (
		result any
		err    error
	)
	if approve {
		result, err = core.ApproveDecision(req)
	} else {
		result, err = core.DenyDecision(req)
	}
	if err != nil {
		return err
	}
	return writeIndentedJSON(a.stdout, result)
}

func (a app) decisionWatch(core manager.Core, args []string) error {
	fs := flag.NewFlagSet("decision watch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	includeTerminal := fs.Bool("include-terminal", true, "include terminal decisions")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: hideout decision watch [--include-terminal=false]")
	}
	decisions, err := core.ListDecisions(manager.DecisionListRequest{IncludeTerminal: *includeTerminal})
	if err != nil {
		return err
	}
	notices, err := core.ListNotices(manager.NoticeListRequest{})
	if err != nil {
		return err
	}
	status, err := core.DecisionStatus()
	if err != nil {
		return err
	}
	return writeIndentedJSON(a.stdout, map[string]any{
		"status":    status,
		"decisions": decisions,
		"notices":   notices,
	})
}

func (a app) noticeCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: hideout notice list|inspect|ack")
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	core := manager.New(store)
	switch args[0] {
	case "list":
		return a.noticeList(core, args[1:])
	case "inspect":
		return a.noticeInspect(core, args[1:])
	case "ack":
		return a.noticeAck(core, args[1:])
	default:
		return fmt.Errorf("unknown notice command %q", args[0])
	}
}

func (a app) noticeList(core manager.Core, args []string) error {
	fs := flag.NewFlagSet("notice list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	kind := fs.String("kind", "", "notice kind")
	severity := fs.String("severity", "", "notice severity")
	profileName := fs.String("profile", "", "profile")
	sessionID := fs.String("session", "", "session id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: hideout notice list [--kind <kind>] [--severity <level>]")
	}
	notices, err := core.ListNotices(manager.NoticeListRequest{
		Kind:     *kind,
		Severity: *severity,
		Profile:  *profileName,
		Session:  *sessionID,
	})
	if err != nil {
		return err
	}
	return writeIndentedJSON(a.stdout, notices)
}

func (a app) noticeInspect(core manager.Core, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: hideout notice inspect <notice-id>")
	}
	n, err := core.InspectNotice(args[0])
	if err != nil {
		return err
	}
	return writeIndentedJSON(a.stdout, n)
}

func (a app) noticeAck(core manager.Core, args []string) error {
	fs := flag.NewFlagSet("notice ack", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	surface := fs.String("surface", "cli", "acknowledging surface")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: hideout notice ack [--surface cli|tui|webui] <notice-id>")
	}
	ack, err := core.AckNotice(manager.NoticeAckRequest{NoticeID: fs.Arg(0), Surface: *surface})
	if err != nil {
		return err
	}
	return writeIndentedJSON(a.stdout, ack)
}

func writeIndentedJSON(w io.Writer, value any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}

func stdinIsTerminal() bool {
	st, err := os.Stdin.Stat()
	return err == nil && (st.Mode()&os.ModeCharDevice) != 0
}

func confirmExport() (bool, error) {
	fmt.Fprint(os.Stderr, "Proceed with export? [y/N] ")
	var answer string
	if _, err := fmt.Fscanln(os.Stdin, &answer); err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func writeAuditShowEvents(w io.Writer, events []audit.Event) {
	if len(events) == 0 {
		fmt.Fprintln(w, "audit: none")
		return
	}
	fmt.Fprintln(w, "TIME\tSESSION\tPROFILE\tBACKEND\tACTION\tDECISION\tDETAILS")
	for _, event := range events {
		ts := "-"
		if !event.Time.IsZero() {
			ts = event.Time.UTC().Format(time.RFC3339Nano)
		}
		details := "{}"
		if len(event.Details) > 0 {
			if data, err := json.Marshal(event.Details); err == nil {
				details = string(data)
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			ts,
			event.Session,
			event.Profile,
			event.Backend,
			event.Action,
			event.Decision,
			details,
		)
	}
}

func (a app) envList(args []string) error {
	fs := flag.NewFlagSet("env list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: hideout env list")
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	overview, err := manager.New(store).Overview(context.Background())
	if err != nil && overview.Version == "" {
		return err
	}
	environments := overview.Environments
	if len(environments) == 0 {
		fmt.Fprintln(a.stdout, "environments: none")
		return nil
	}
	fmt.Fprintln(a.stdout, "NAME\tKIND\tIMAGE\tBACKEND\tSTATUS\tDISK\tLAST_STARTED\tWORKSPACE\tID")
	for _, env := range environments {
		if env.Status == "unsupported-version" {
			fmt.Fprintf(a.stdout, "-\tunsupported-version\t-\t-\t%s\t%s\t-\t-\t%s\n",
				env.Status, environmentDiskUsage(store.Root, env.ID), env.ID)
			continue
		}
		kind := "named"
		if env.AutoNamed {
			kind = "auto"
		}
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			env.Name,
			kind,
			abbreviateImageRef(env.ImageRef),
			env.Backend,
			explainValue(env.Status, "ready"),
			environmentDiskUsage(store.Root, env.ID),
			formatEnvironmentTime(env.LastStartedAt),
			env.Workspace,
			env.ID)
	}
	return nil
}

// abbreviateImageRef keeps listing columns readable: URL digests collapse to
// their first 12 hex characters.
func abbreviateImageRef(ref string) string {
	if ref == "" {
		return "-"
	}
	if i := strings.Index(ref, "#sha256:"); i >= 0 && len(ref) > i+len("#sha256:")+12 {
		return ref[:i+len("#sha256:")+12] + "…"
	}
	return ref
}

// environmentDiskUsage computes the environment directory size at list time;
// disk usage is derived evidence, never stored on the record.
func environmentDiskUsage(storeRoot, id string) string {
	var total int64
	root := filepath.Join(storeRoot, "environments", id)
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	switch {
	case total >= 1<<30:
		return fmt.Sprintf("%.1fGiB", float64(total)/(1<<30))
	case total >= 1<<20:
		return fmt.Sprintf("%.1fMiB", float64(total)/(1<<20))
	case total >= 1<<10:
		return fmt.Sprintf("%.1fKiB", float64(total)/(1<<10))
	default:
		return fmt.Sprintf("%dB", total)
	}
}

// warnShadowedHostFSRules keeps HostFS honest inside the workspace: the
// workspace is a uniform read/write zone that never consults HostFS policy,
// so rules covering in-workspace paths are warned about (once per rule) and
// never enforced or blocking.
func (a app) warnShadowedHostFSRules(cfg hostfs.Config, workspace string) {
	for _, rule := range hostfs.WorkspaceShadowedRules(cfg, workspace) {
		fmt.Fprintf(a.stderr, "warning: hostfs rule %s (%s) is shadowed by the workspace %s and has no effect inside it\n", rule.ID, rule.HostPath, workspace)
	}
}

func (a app) envCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: hideout env <create|inspect|list|recreate|remove> ...")
	}
	switch args[0] {
	case "create":
		return a.envCreate(args[1:])
	case "inspect":
		return a.envInspect(args[1:])
	case "list":
		return a.envList(args[1:])
	case "recreate":
		return a.envDestructive(args[1:], "recreate")
	case "remove":
		return a.envDestructive(args[1:], "remove")
	default:
		return fmt.Errorf("unknown env subcommand %q (expected create, inspect, list, recreate, or remove)", args[0])
	}
}

func (a app) envDestructive(args []string, verb string) error {
	fs := flag.NewFlagSet("env "+verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	force := fs.Bool("force", false, "stop a running guest first, then "+verb)
	verbose := fs.Bool("verbose", false, "show backend control output")
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("usage: hideout env %s <name> [--force]", verb)
	}
	name := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: hideout env %s <name> [--force]", verb)
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	core := manager.New(store)
	applyOpts := manager.EnvironmentApplyOptions{Operator: a.environmentOperator(*verbose)}
	switch verb {
	case "recreate":
		rec, err := core.RecreateEnvironment(context.Background(), name, *force, applyOpts)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "recreated environment %s (%s)\n", rec.Name, rec.ID)
		fmt.Fprintf(a.stdout, "  image: %s\n", rec.ImageRef)
		fmt.Fprintf(a.stdout, "run: hideout run --env %s -- <command>\n", rec.Name)
	case "remove":
		rec, err := core.RemoveEnvironment(context.Background(), name, *force, applyOpts)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "removed environment %s (%s)\n", rec.Name, rec.ID)
	}
	return nil
}

func (a app) envCreate(args []string) error {
	fs := flag.NewFlagSet("env create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	image := fs.String("image", "", "base image declaration (template:<name> or https URL with #sha256:<digest>)")
	workspace := fs.String("workspace", "", "workspace to pin (defaults to the current directory)")
	profileName := fs.String("profile", "default", "profile the environment belongs to")
	backendName := fs.String("backend", "lima", "backend for the environment")
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return errors.New("usage: hideout env create <name> [--image <declaration>] [--workspace <path>] [--profile <p>] [--backend <b>]")
	}
	name := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: hideout env create <name> [--image <declaration>] [--workspace <path>] [--profile <p>] [--backend <b>]")
	}
	ws := strings.TrimSpace(*workspace)
	if ws == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		ws = cwd
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	rec, err := manager.New(store).CreateEnvironment(manager.EnvironmentCreateOptions{
		Name:      name,
		ImageRef:  *image,
		Profile:   *profileName,
		Backend:   *backendName,
		Workspace: ws,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "created environment %s (%s)\n", rec.Name, rec.ID)
	fmt.Fprintf(a.stdout, "  image: %s\n", rec.ImageRef)
	fmt.Fprintf(a.stdout, "  workspace: %s\n", rec.Workspace)
	fmt.Fprintf(a.stdout, "  backend: %s profile: %s\n", rec.Backend, rec.Profile)
	fmt.Fprintf(a.stdout, "run: hideout run --env %s -- <command>\n", rec.Name)
	return nil
}

func (a app) envInspect(args []string) error {
	fs := flag.NewFlagSet("env inspect", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: hideout env inspect <name>")
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	rec, err := manager.New(store).EnvironmentByName(fs.Arg(0))
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "environment: %s\n", rec.Name)
	fmt.Fprintf(a.stdout, "  id: %s\n", rec.ID)
	fmt.Fprintf(a.stdout, "  auto-named: %t\n", rec.AutoNamed)
	fmt.Fprintf(a.stdout, "  status: %s\n", rec.Status)
	fmt.Fprintln(a.stdout, "  identity:")
	fmt.Fprintf(a.stdout, "    image: %s\n", rec.ImageRef)
	fmt.Fprintf(a.stdout, "    backend-config: %s\n", rec.BackendConfigVersion)
	fmt.Fprintf(a.stdout, "    workspace: %s -> %s\n", rec.Workspace, rec.GuestWorkspace)
	fmt.Fprintf(a.stdout, "  backend: %s profile: %s\n", rec.Backend, rec.Profile)
	if rec.InstanceName != "" {
		fmt.Fprintf(a.stdout, "  instance: %s\n", rec.InstanceName)
	}
	return nil
}

func (a app) stopEnvironments(args []string) error {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dryRun := fs.Bool("dry-run", false, "show environments that would be stopped")
	idleValue := fs.String("idle", "", "stop environments whose last run ended at least this long ago")
	verbose := fs.Bool("verbose", false, "show backend control output while stopping environments")
	if err := fs.Parse(args); err != nil {
		return err
	}
	idle, idleSet, err := parseIdleDuration(*idleValue)
	if err != nil {
		return err
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	core := manager.New(store)
	plan, err := core.PlanEnvironmentStop(manager.EnvironmentActionOptions{
		IDs:     fs.Args(),
		Idle:    idle,
		IdleSet: idleSet,
		Now:     time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	if *dryRun {
		a.writeEnvironmentTargets("would stop", plan.Targets)
		a.writeEnvironmentSkipped(plan.Skipped)
		fmt.Fprintf(a.stdout, "stop: environments=%d would stop=%d skipped=%d\n", plan.Total, len(plan.Targets), len(plan.Skipped))
		return nil
	}
	result, err := core.ApplyEnvironmentStop(context.Background(), plan, manager.EnvironmentApplyOptions{
		Operator: a.environmentOperator(*verbose),
	})
	if err != nil {
		return err
	}
	a.writeEnvironmentTargets("stopped", result.Applied)
	a.writeEnvironmentSkipped(result.Skipped)
	fmt.Fprintf(a.stdout, "stop: environments=%d stopped=%d skipped=%d\n", plan.Total, len(result.Applied), len(result.Skipped))
	return nil
}

func (a app) cleanEnvironments(args []string) error {
	fs := flag.NewFlagSet("clean", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dryRun := fs.Bool("dry-run", false, "show environments that would be removed")
	stoppedOnly := fs.Bool("stopped", false, "remove only stopped environments")
	idleValue := fs.String("idle", "", "remove environments whose last run ended at least this long ago")
	verbose := fs.Bool("verbose", false, "show backend control output while cleaning environments")
	if err := fs.Parse(args); err != nil {
		return err
	}
	idle, idleSet, err := parseIdleDuration(*idleValue)
	if err != nil {
		return err
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	core := manager.New(store)
	plan, err := core.PlanEnvironmentClean(manager.EnvironmentActionOptions{
		IDs:         fs.Args(),
		StoppedOnly: *stoppedOnly,
		Idle:        idle,
		IdleSet:     idleSet,
		Now:         time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	if *dryRun {
		a.writeEnvironmentTargets("would remove", plan.Targets)
		a.writeEnvironmentSkipped(plan.Skipped)
		fmt.Fprintf(a.stdout, "clean: environments=%d would remove=%d\n", plan.Total, len(plan.Targets))
		return nil
	}
	result, err := core.ApplyEnvironmentClean(context.Background(), plan, manager.EnvironmentApplyOptions{
		Operator: a.environmentOperator(*verbose),
	})
	if err != nil {
		return err
	}
	a.writeEnvironmentTargets("removed", result.Applied)
	a.writeEnvironmentSkipped(result.Skipped)
	fmt.Fprintf(a.stdout, "clean: environments=%d removed=%d\n", plan.Total, len(result.Applied))
	return nil
}

func parseIdleDuration(value string) (time.Duration, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, false, fmt.Errorf("invalid --idle duration %q: %w", value, err)
	}
	if duration < 0 {
		return 0, false, errors.New("--idle duration must be non-negative")
	}
	return duration, true, nil
}

func (a app) writeEnvironmentTargets(mode string, targets []manager.EnvironmentActionTarget) {
	for _, target := range targets {
		fmt.Fprintf(a.stdout, "%s: %s instance=%s workspace=%s\n", mode, target.ID, target.InstanceName, target.Workspace)
	}
}

func (a app) writeEnvironmentSkipped(targets []manager.EnvironmentActionTarget) {
	for _, target := range targets {
		if target.Reason == "no-lima-instance" {
			fmt.Fprintf(a.stdout, "skipped: %s reason=%s backend=%s workspace=%s\n", target.ID, target.Reason, target.Backend, target.Workspace)
			continue
		}
		fmt.Fprintf(a.stdout, "skipped: %s reason=%s instance=%s workspace=%s\n", target.ID, target.Reason, target.InstanceName, target.Workspace)
	}
}

func formatEnvironmentTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.UTC().Format(time.RFC3339)
}

type uiOptions struct {
	listen   string
	ttl      time.Duration
	noOpen   bool
	printURL bool
}

func parseUIOptions(args []string) (uiOptions, error) {
	opts := uiOptions{
		listen: manager.DefaultUIListenAddr,
		ttl:    15 * time.Minute,
	}
	fs := flag.NewFlagSet("ui", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.listen, "listen", opts.listen, "127.0.0.1 listen address")
	fs.DurationVar(&opts.ttl, "ttl", opts.ttl, "UI token lifetime")
	fs.BoolVar(&opts.noOpen, "no-open", false, "do not open a browser")
	fs.BoolVar(&opts.printURL, "print-url", false, "print URL and exit")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() != 0 {
		return opts, errors.New("usage: hideout ui [--listen 127.0.0.1:0] [--ttl 15m] [--no-open] [--print-url]")
	}
	return opts, nil
}

func (a app) ui(args []string) error {
	opts, err := parseUIOptions(args)
	if err != nil {
		return err
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(store.Root, 0o700); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, err := manager.StartLocalServer(ctx, manager.LocalServerOptions{
		Core:       manager.New(store),
		Addr:       opts.listen,
		TTL:        opts.ttl,
		RunBackend: a.runAPIBackend,
		RunOpener: func(_ manager.RunAPIRequest, _ manager.RunPlan, runSession manager.RunSession) broker.Opener {
			return hostOpener(runSession.IdentityDir, a.stdout, a.stderr)
		},
	})
	if err != nil {
		return err
	}
	defer server.Close()
	fmt.Fprintf(a.stdout, "Hideout UI: %s\n", server.UIURL)
	fmt.Fprintf(a.stdout, "Manager API: %s\n", server.APIURL)
	fmt.Fprintf(a.stdout, "Token expires: %s\n", server.ExpiresAt.Format(time.RFC3339))
	if opts.printURL {
		return nil
	}
	if !opts.noOpen {
		opener := hostopen.Opener{
			BrowserProfileDir: filepath.Join(store.Root, "ui-browser"),
			BrowserPath:       os.Getenv("HIDEOUT_BROWSER_PATH"),
			BrowserApp:        os.Getenv("HIDEOUT_BROWSER_APP"),
			DryRun:            os.Getenv("HIDEOUT_OPEN_DRY_RUN") == "1",
			Stdout:            a.stdout,
			Stderr:            a.stderr,
		}
		if err := opener.OpenURL(context.Background(), server.UIURL); err != nil {
			return err
		}
	}
	fmt.Fprintln(a.stdout, "Press Ctrl-C to stop.")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	select {
	case <-sig:
		return nil
	case err := <-serverError(server):
		return err
	}
}

func (a app) daemonCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: hideout daemon start|status|stop")
	}
	switch args[0] {
	case "start":
		return a.daemonStart(args[1:])
	case "status":
		return a.daemonStatus(args[1:])
	case "stop":
		return a.daemonStop(args[1:])
	default:
		return fmt.Errorf("unknown daemon command %q", args[0])
	}
}

func (a app) daemonStart(args []string) error {
	fs := flag.NewFlagSet("daemon start", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	ttl := fs.Duration("ttl", 15*time.Minute, "operator token TTL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(store.Root, 0o700); err != nil {
		return err
	}
	d, err := daemon.Start(daemon.Options{
		Store:      store,
		TTL:        *ttl,
		RunBackend: a.runAPIBackend,
		RunOpener: func(_ manager.RunAPIRequest, _ manager.RunPlan, runSession manager.RunSession) broker.Opener {
			return hostOpener(runSession.IdentityDir, a.stdout, a.stderr)
		},
	})
	if err != nil {
		if daemon.IsAlreadyRunning(err) {
			fmt.Fprintf(a.stdout, "hideoutd already running for this store: %s\n", daemon.SocketPath(store.Root))
			return nil
		}
		return err
	}
	fmt.Fprintf(a.stdout, "hideoutd serving: %s\n", d.Socket())
	fmt.Fprintf(a.stdout, "token: %s\n", filepath.Join(d.RuntimeDir(), "token"))
	if url := d.UIURL(); url != "" {
		fmt.Fprintf(a.stdout, "WebUI: %s\n", url)
	}
	fmt.Fprintln(a.stdout, "Press Ctrl-C to stop.")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	select {
	case <-sig:
		return d.Stop(context.Background())
	case <-d.Done():
		// Stopped out of band (e.g. via `hideout daemon stop`).
		return nil
	}
}

func (a app) daemonStatus(args []string) error {
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	body, err := a.daemonRequest(store.Root, http.MethodGet, "/daemon/status", nil)
	if err != nil {
		return err
	}
	a.stdout.Write(body)
	return nil
}

func (a app) daemonStop(args []string) error {
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	if _, err := a.daemonRequest(store.Root, http.MethodPost, "/daemon/stop", nil); err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, "hideoutd stopping")
	return nil
}

func (a app) daemonRequest(storeRoot, method, path string, body io.Reader) ([]byte, error) {
	client, base, token, err := daemon.DialClient(storeRoot)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(method, base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Host = "localhost"
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon not reachable: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon request failed (%s): %s", resp.Status, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func (a app) runAPIBackend(req manager.RunAPIRequest, plan manager.RunPlan) (backend.Backend, error) {
	opts := runOptions{
		backendName:        plan.Backend,
		allowWeakIsolation: req.AllowWeakIsolation,
	}
	return a.backend(plan.Backend, opts), nil
}

type tuiOptions struct {
	watch       bool
	once        bool
	interval    time.Duration
	profileName string
}

const tuiDashboardRowLimit = 10

func parseTUIOptions(args []string) (tuiOptions, error) {
	opts := tuiOptions{watch: true, interval: 2 * time.Second}
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&opts.once, "once", false, "render the terminal dashboard once and exit")
	fs.DurationVar(&opts.interval, "interval", opts.interval, "watch refresh interval")
	fs.StringVar(&opts.profileName, "profile", "", "filter dashboard and audit rows to one profile")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() != 0 {
		return opts, errors.New("usage: hideout tui [--profile <name>] [--interval 2s] | hideout tui --once [--profile <name>]")
	}
	if opts.interval <= 0 {
		return opts, errors.New("--interval must be positive")
	}
	if opts.once {
		opts.watch = false
	}
	opts.profileName = strings.TrimSpace(opts.profileName)
	if opts.profileName != "" {
		if err := profile.ValidateName(opts.profileName); err != nil {
			return opts, err
		}
	}
	return opts, nil
}

func (a app) tui(args []string) error {
	opts, err := parseTUIOptions(args)
	if err != nil {
		return err
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	core := manager.New(store)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	renderSnapshot := func(clear bool) error {
		state, snapshotErr := buildTUILiveState(ctx, core, opts.profileName, liveconsole.HealthDaemonless)
		if clear {
			fmt.Fprint(a.stdout, "\033[H\033[2J")
		}
		writeTUIDashboard(a.stdout, state.Overview, state.AuditTail, state.DeniedAuditTail, snapshotErr, opts.profileName)
		return nil
	}
	if !opts.watch {
		return renderSnapshot(false)
	}
	if ch, err := daemon.SubscribeEvents(ctx, store.Root); err == nil {
		state, seedErr := buildTUILiveState(ctx, core, opts.profileName, liveconsole.HealthLive)
		renderLive := func(state liveconsole.State) error {
			if seedErr != nil {
				state.StreamHealth = liveconsole.StreamHealth{State: liveconsole.HealthStale, Reason: seedErr.Error()}
			}
			fmt.Fprint(a.stdout, "\033[H\033[2J")
			writeTUILiveDashboard(a.stdout, state, seedErr, opts.profileName)
			return nil
		}
		return watchLiveDashboard(ctx, ch, opts.interval, &state, renderLive, func() error { return renderSnapshot(true) })
	}
	return watchDashboard(ctx, nil, opts.interval, func() error { return renderSnapshot(true) })
}

func buildTUILiveState(ctx context.Context, core manager.Core, profileName, health string) (liveconsole.State, error) {
	overview, overviewErr := core.Overview(ctx)
	eventGroups, auditErr := core.AuditEventGroups(
		manager.AuditEventFilter{Profile: profileName, Limit: 5},
		manager.AuditEventFilter{Profile: profileName, Decision: "deny", Limit: 5},
	)
	events, deniedEvents := []audit.Event{}, []audit.Event{}
	if len(eventGroups) > 0 {
		events = eventGroups[0]
	}
	if len(eventGroups) > 1 {
		deniedEvents = eventGroups[1]
	}
	seed := liveconsole.BuildSeed(liveconsole.SeedInput{
		Overview:        overview,
		AuditTail:       events,
		DeniedAuditTail: deniedEvents,
		ProfileScope:    profileName,
		StreamHealth:    health,
	})
	return liveconsole.NewState(seed), errors.Join(overviewErr, auditErr)
}

func watchLiveDashboard(ctx context.Context, eventCh <-chan liveconsole.Event, interval time.Duration, state *liveconsole.State, render func(liveconsole.State) error, fallback func() error) error {
	if state == nil {
		return errors.New("live dashboard state is required")
	}
	for {
		if err := render(*state); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-eventCh:
			if !ok {
				if state.StreamHealth.State != liveconsole.HealthCredentialExpired {
					state.StreamHealth = liveconsole.StreamHealth{State: liveconsole.HealthDisconnected, Reason: "event stream closed"}
				}
				if err := render(*state); err != nil {
					return err
				}
				return watchDashboard(ctx, nil, interval, fallback)
			}
			liveconsole.Apply(state, ev)
		}
	}
}

// watchDashboard drives the TUI refresh loop. It renders once up front and then,
// while a daemon event stream is present, refreshes strictly on events (no polling
// timer while the stream is live). Only after the stream ends (channel closed) does
// it fall back to interval polling.
func watchDashboard(ctx context.Context, eventCh <-chan struct{}, interval time.Duration, render func() error) error {
	for {
		if err := render(); err != nil {
			return err
		}
		if eventCh != nil {
			select {
			case <-ctx.Done():
				return nil
			case _, ok := <-eventCh:
				if !ok {
					eventCh = nil // stream ended; fall back to interval polling
				}
			}
			continue
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func writeTUIDashboard(w io.Writer, overview manager.Overview, events []audit.Event, deniedEvents []audit.Event, err error, profileFilter string) {
	fmt.Fprintln(w, "Hideout TUI")
	fmt.Fprintf(w, "Store: %s\n", dash(overview.StorageRoot))
	if profileFilter != "" {
		fmt.Fprintf(w, "Profile filter: %s\n", profileFilter)
	}
	if err != nil {
		fmt.Fprintf(w, "Status: degraded (%s)\n", err)
	} else {
		fmt.Fprintln(w, "Status: ok")
	}
	fmt.Fprintf(w, "Updated: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(w, "Profiles: %d  Environments: %d  Sessions: %d  Audit files: %d\n", len(overview.Profiles), len(overview.Environments), len(overview.Sessions), overview.Audit.SessionAuditFiles)
	fmt.Fprintf(w, "Init: initialized=%t pending=%d profile=%s\n", overview.Init.Initialized, overview.Init.PendingTasks, dash(overview.Init.Profile))
	if len(overview.Init.NextSteps) > 0 {
		fmt.Fprintln(w, "Init Next:")
		for _, step := range overview.Init.NextSteps {
			fmt.Fprintf(w, "  - %s: %s\n", dash(step.Label), dash(step.Command))
		}
	}
	fmt.Fprintf(w, "Capabilities: host.open urls=%t workspaceFiles=%t commandProxies=%s max=%s\n",
		overview.Capabilities.HostOpen.AllowURLs,
		overview.Capabilities.HostOpen.AllowWorkspaceFiles,
		listForTUI(overview.Broker.CommandProxies),
		listForTUI(overview.Capabilities.MaxCapabilities),
	)

	fmt.Fprintln(w, "\nProfiles")
	if len(overview.Profiles) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, p := range overview.Profiles {
		status := "ok"
		if p.ValidationError != "" {
			status = "error: " + p.ValidationError
		}
		fmt.Fprintf(w, "  - %s  network=%s  env=public:%d/inherit:%d/deny:%d  expected=%s  commandProxies=%s  commandAdapters=%s  hostfs=allow:%d/deny:%d  status=%s\n", dash(p.Name), dash(p.NetworkMode), len(p.EnvPublic), len(p.EnvInherit), len(p.EnvDeny), listForTUI(p.ExpectedCommands), listForTUI(p.CommandProxies), commandAdaptersForTUI(p.CommandAdapters), p.HostFSGrants, p.HostFSDeny, status)
		next := profileNextCommandsForTUI(p)
		if len(next) > 0 {
			for _, command := range next {
				fmt.Fprintf(w, "    next: %s\n", command)
			}
		}
	}

	fmt.Fprintln(w, "\nBackends")
	if len(overview.Backends) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, b := range overview.Backends {
		status := "available"
		if !b.Available {
			status = "unavailable: " + b.Error
		}
		fmt.Fprintf(w, "  - %s  isolation=%s  %s\n", dash(b.Name), dash(b.Isolation), status)
	}

	fmt.Fprintln(w, "\nNetwork")
	if len(overview.Network.ProfileDefaults) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, n := range overview.Network.ProfileDefaults {
		mode := networkModeForTUI(n.Mode)
		fmt.Fprintf(w, "  - %s  mode=%s  proxyEnv=%s%s\n", dash(n.Profile), mode, proxyEnvForTUI(n.ProxyEnvVisible), networkWarningForTUI(mode))
	}

	fmt.Fprintln(w, "\nEnvironments")
	if len(overview.Environments) == 0 {
		fmt.Fprintln(w, "  none")
	}
	environments := visibleEnvironmentsForTUI(overview.Environments)
	if len(environments) < len(overview.Environments) {
		fmt.Fprintf(w, "  showing newest %d of %d\n", len(environments), len(overview.Environments))
	}
	for _, env := range environments {
		kind := "named"
		if env.AutoNamed {
			kind = "auto"
		}
		if env.Status == "unsupported-version" {
			fmt.Fprintf(w, "  - %s  status=%s (clean and recreate)\n", dash(env.ID), env.Status)
			continue
		}
		fmt.Fprintf(w, "  - %s (%s)  status=%s  image=%s  backend=%s  profile=%s  workspace=%s  last=%s\n",
			dash(env.Name),
			kind,
			dash(env.Status),
			dash(abbreviateImageRef(env.ImageRef)),
			dash(env.Backend),
			dash(env.Profile),
			dash(env.Workspace),
			dash(env.LastCommand),
		)
		if env.Name != "" {
			fmt.Fprintf(w, "    next: run=hideout run --env %s -- <command>  stop=hideout stop %s  clean-after-stop=hideout clean --stopped %s\n", env.Name, env.Name, env.Name)
		}
	}

	fmt.Fprintln(w, "\nSessions")
	if len(overview.Sessions) == 0 {
		fmt.Fprintln(w, "  none")
	}
	sessions := visibleSessionsForTUI(overview.Sessions)
	if len(sessions) < len(overview.Sessions) {
		fmt.Fprintf(w, "  showing newest %d of %d\n", len(sessions), len(overview.Sessions))
	}
	for _, s := range sessions {
		fmt.Fprintf(w, "  - %s  profile=%s  audit=%t  network=%s  privilege=%s  runtime=%t\n", dash(s.ID), dash(s.Profile), s.HasAudit, dash(s.NetworkMode), privilegeForTUI(s.GuestPrivilege), s.HasEphemeralState)
		next := sessionNextCommandsForTUI(s)
		if len(next) > 0 {
			fmt.Fprintf(w, "    next: %s\n", strings.Join(next, "  "))
		}
	}

	fmt.Fprintln(w, "\nRecent Denied Audit")
	if len(deniedEvents) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, event := range deniedEvents {
		fmt.Fprintf(w, "  - %s  action=%s  session=%s\n", dash(event.Profile), dash(event.Action), dash(event.Session))
	}

	fmt.Fprintln(w, "\nRecent Audit")
	if len(events) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, event := range events {
		fmt.Fprintf(w, "  - %s  action=%s  decision=%s  session=%s\n", dash(event.Profile), dash(event.Action), dash(event.Decision), dash(event.Session))
	}
}

func writeTUILiveDashboard(w io.Writer, state liveconsole.State, err error, profileFilter string) {
	writeTUIDashboard(w, state.Overview, state.AuditTail, state.DeniedAuditTail, err, profileFilter)
	fmt.Fprintf(w, "\nStream: %s", dash(state.StreamHealth.State))
	if state.StreamHealth.Reason != "" {
		fmt.Fprintf(w, " (%s)", state.StreamHealth.Reason)
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "\nBackground")
	if len(state.Background) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, bg := range state.Background {
		fmt.Fprintf(w, "  - %s  op=%s  status=%s\n", dash(bg.ID), dash(bg.Op), dash(bg.Status))
	}

	fmt.Fprintln(w, "\nHostFS Writes")
	if len(state.HostFSWrites) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, row := range state.HostFSWrites {
		fmt.Fprintf(w, "  - %s  op=%s  status=%s  privilege=%s  path=%s\n", dash(row.DecisionID), dash(row.Operation), dash(row.Status), dash(row.PrivilegeStatus), dash(row.Path))
		fmt.Fprintf(w, "    next: claim=hideout hostfs write claim %s  apply=hideout hostfs write apply %s  discard=hideout hostfs write discard %s\n", dash(row.DecisionID), dash(row.DecisionID), dash(row.DecisionID))
	}

	fmt.Fprintln(w, "\nDecisions")
	if len(state.Decisions) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, row := range state.Decisions {
		fmt.Fprintf(w, "  - %s  kind=%s  status=%s  default=%s  profile=%s  session=%s\n", dash(row.ID), dash(row.Kind), dash(row.Status), dash(row.DefaultOutcome), dash(row.Profile), dash(row.Session))
		if row.Reason != "" {
			fmt.Fprintf(w, "    reason=%s\n", row.Reason)
		}
	}

	fmt.Fprintln(w, "\nNotices")
	if len(state.Notices) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, row := range state.Notices {
		fmt.Fprintf(w, "  - %s  kind=%s  status=%s  severity=%s  acknowledged=%t  profile=%s  session=%s\n", dash(row.ID), dash(row.Kind), dash(row.Status), dash(row.Severity), row.Acknowledged, dash(row.Profile), dash(row.Session))
	}

	fmt.Fprintln(w, "\nExports")
	if len(state.ExportOutcomes) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, row := range state.ExportOutcomes {
		fmt.Fprintf(w, "  - status=%s  source=%s  decision=%s  artifact=%s\n", dash(row.Status), dash(row.Source), dash(row.Decision), dash(row.ArtifactPath))
	}

	fmt.Fprintln(w, "\nCleanup")
	if len(state.CleanupOutcomes) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, row := range state.CleanupOutcomes {
		fmt.Fprintf(w, "  - status=%s  sessions=%d  removed=%s  secrets=%s\n", dash(row.Status), row.Sessions, listForTUI(row.Removed), dash(row.SecretState))
	}
}

func filterOverviewForTUI(overview manager.Overview, profileName string) manager.Overview {
	if profileName == "" {
		return overview
	}
	overview.Profiles = filterProfilesForTUI(overview.Profiles, profileName)
	overview.Environments = filterEnvironmentsForTUI(overview.Environments, profileName)
	overview.Sessions = filterSessionsForTUI(overview.Sessions, profileName)
	overview.Network.ProfileDefaults = filterNetworkProfilesForTUI(overview.Network.ProfileDefaults, profileName)
	return overview
}

func filterProfilesForTUI(values []manager.ProfileSummary, profileName string) []manager.ProfileSummary {
	out := values[:0]
	for _, value := range values {
		if value.Name == profileName {
			out = append(out, value)
		}
	}
	return out
}

func filterEnvironmentsForTUI(values []manager.EnvironmentSummary, profileName string) []manager.EnvironmentSummary {
	out := values[:0]
	for _, value := range values {
		if value.Profile == profileName {
			out = append(out, value)
		}
	}
	return out
}

func filterSessionsForTUI(values []manager.SessionSummary, profileName string) []manager.SessionSummary {
	out := values[:0]
	for _, value := range values {
		if value.Profile == profileName {
			out = append(out, value)
		}
	}
	return out
}

func filterNetworkProfilesForTUI(values []manager.ProfileNetworkSummary, profileName string) []manager.ProfileNetworkSummary {
	out := values[:0]
	for _, value := range values {
		if value.Profile == profileName {
			out = append(out, value)
		}
	}
	return out
}

func visibleEnvironmentsForTUI(environments []manager.EnvironmentSummary) []manager.EnvironmentSummary {
	if len(environments) <= tuiDashboardRowLimit {
		return environments
	}
	return environments[:tuiDashboardRowLimit]
}

func visibleSessionsForTUI(sessions []manager.SessionSummary) []manager.SessionSummary {
	if len(sessions) <= tuiDashboardRowLimit {
		return sessions
	}
	return sessions[len(sessions)-tuiDashboardRowLimit:]
}

func sessionNextCommandsForTUI(s manager.SessionSummary) []string {
	if s.ID == "" {
		return nil
	}
	var out []string
	if s.HasAudit {
		out = append(out, "audit=hideout audit show --session "+s.ID)
	}
	if s.HasEphemeralState {
		out = append(out, "cleanup-check=hideout cleanup --session "+s.ID+" --dry-run")
	}
	return out
}

func profileNextCommandsForTUI(p manager.ProfileSummary) []string {
	if p.Name == "" {
		return nil
	}
	return []string{
		"tools=hideout profile tools " + p.Name + " list",
		"expect-command=hideout profile tools " + p.Name + " expected add <command>",
		"env=hideout profile env " + p.Name + " list",
		"set-env=hideout profile env " + p.Name + " set NAME=value",
		"command-proxy=hideout profile command-proxy " + p.Name + " list",
		"add-open=hideout profile command-proxy " + p.Name + " add-open <command>",
		"hostfs=hideout profile fs " + p.Name + " list",
		"add-hostfs=hideout profile fs " + p.Name + " add --fs read:/absolute/file --reason <why>",
	}
}

func dash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func proxyEnvForTUI(visible bool) string {
	if visible {
		return "visible"
	}
	return "hidden"
}

func networkModeForTUI(mode string) string {
	if strings.TrimSpace(mode) == "" {
		return "direct"
	}
	return mode
}

func networkWarningForTUI(mode string) string {
	switch strings.TrimSpace(mode) {
	case "direct":
		return "  warning=direct exposes network identity"
	case "tun2socks":
		return "  warning=proxy hides origin path, not data egress"
	default:
		return ""
	}
}

func listForTUI(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ",")
}

func commandAdaptersForTUI(adapters []manager.CommandAdapterSummary) string {
	if len(adapters) == 0 {
		return "none"
	}
	rows := make([]string, 0, len(adapters))
	for _, adapter := range adapters {
		state := "off"
		if adapter.Enabled {
			state = "on"
		}
		rows = append(rows, fmt.Sprintf("%s:%s(%s)", dash(adapter.ID), state, listForTUI(adapter.Commands)))
	}
	return strings.Join(rows, ",")
}

func privilegeForTUI(summary *manager.BoundaryPrivilegeSummary) string {
	if summary == nil || summary.Status == "" {
		return "unknown"
	}
	parts := []string{summary.Status}
	if summary.TargetUID != "" {
		parts = append(parts, "uid="+summary.TargetUID)
	}
	if summary.SetupKind != "" {
		parts = append(parts, "setup="+summary.SetupKind)
	}
	return strings.Join(parts, ":")
}

type labPortbridgeLoopbackOptions struct {
	enableLab bool
	listen    string
	target    string
	send      string
	expect    string
	timeout   time.Duration
}

type labPortbridgeDirectionOptions struct {
	enableLab bool
	listen    string
	target    string
	send      string
	expect    string
	timeout   time.Duration
}

type labBrowserControlOptions struct {
	enableLab   bool
	profileName string
	browserPath string
	timeout     time.Duration
}

type labPreviewOpenOptions struct {
	enableLab bool
	guestURL  string
	timeout   time.Duration
}

type labOutputField struct {
	key   string
	value string
}

type labProbeNotImplementedError struct {
	command  string
	guidance string
}

func (e labProbeNotImplementedError) Error() string {
	if e.guidance != "" {
		return fmt.Sprintf("hideout lab %s is not implemented; %s", e.command, e.guidance)
	}
	return fmt.Sprintf("hideout lab %s is not implemented", e.command)
}

func isLabProbeNotImplemented(err error) bool {
	var target labProbeNotImplementedError
	return errors.As(err, &target)
}

func (a app) lab(args []string) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		a.labUsage()
		return nil
	}
	switch args[0] {
	case "portbridge":
		return a.labPortbridge(args[1:])
	case "browser-control":
		return a.labBrowserControl(args[1:])
	case "preview-open":
		return a.labPreviewOpen(args[1:])
	default:
		return fmt.Errorf("unknown lab command %q", args[0])
	}
}

func (a app) labPortbridge(args []string) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		a.labPortbridgeUsage()
		return nil
	}
	switch args[0] {
	case "loopback":
		return a.labPortbridgeLoopback(args[1:])
	case "guest-to-host", "host-to-guest":
		return a.labPortbridgeDirection(args[0], args[1:])
	default:
		return fmt.Errorf("unknown lab portbridge command %q", args[0])
	}
}

func (a app) labPortbridgeDirection(mode string, args []string) error {
	opts := labPortbridgeDirectionOptions{
		listen:  "127.0.0.1:0",
		timeout: 2 * time.Second,
	}
	fs := flag.NewFlagSet("lab portbridge "+mode, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&opts.enableLab, "enable-lab", false, "enable lab command execution")
	fs.StringVar(&opts.listen, "listen", opts.listen, "loopback listen address")
	switch mode {
	case "guest-to-host":
		fs.StringVar(&opts.target, "target", "", "explicit host target address")
	case "host-to-guest":
		fs.StringVar(&opts.target, "guest-target", "", "explicit guest target address")
	default:
		return fmt.Errorf("unknown lab portbridge command %q", mode)
	}
	fs.StringVar(&opts.send, "send", "", "bytes to send through the bridge")
	fs.StringVar(&opts.expect, "expect", "", "expected bytes to read through the bridge")
	fs.DurationVar(&opts.timeout, "timeout", opts.timeout, "probe timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: hideout lab portbridge %s --enable-lab --%s 127.0.0.1:<port> [--listen 127.0.0.1:0] [--send bytes --expect bytes]", mode, labPortbridgeTargetFlag(mode))
	}
	if !opts.enableLab && os.Getenv("HIDEOUT_ENABLE_LAB") != "1" {
		return errors.New("hideout lab requires --enable-lab or HIDEOUT_ENABLE_LAB=1")
	}
	if strings.TrimSpace(opts.target) == "" {
		return fmt.Errorf("lab portbridge %s requires --%s", mode, labPortbridgeTargetFlag(mode))
	}
	if opts.expect != "" && opts.send == "" {
		return fmt.Errorf("lab portbridge %s requires --send when --expect is set", mode)
	}
	proposal := labPortbridgeDirectionProposal(mode, opts)
	layout, aw, err := newLabAudit()
	if err != nil {
		return err
	}
	defer aw.Close()
	defer cleanupLabLayout(layout)
	if _, err := policy.ValidateLabProposal(proposal); err != nil {
		return emitLabPortbridgeDirectionProbe(aw, layout, proposal, mode, opts, "", "", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()
	bridge, err := portbridge.Start(ctx, portbridge.Spec{
		ID:            "lab_portbridge_" + strings.ReplaceAll(mode, "-", "_"),
		Direction:     labPortbridgeDirectionValue(mode),
		ListenScope:   portbridge.ListenScopeLoopback,
		ListenAddress: opts.listen,
		TargetAddress: opts.target,
	})
	if err != nil {
		return emitLabPortbridgeDirectionProbe(aw, layout, proposal, mode, opts, "", "", err)
	}
	defer bridge.Close()
	a.printLabProbeEvidence(layout, proposal,
		labOutputField{"mode", mode},
		labOutputField{"listen", bridge.ListenAddress()},
		labOutputField{labPortbridgeTargetFlag(mode), opts.target},
	)
	if opts.send == "" {
		fmt.Fprintln(a.stdout, "probe=tcp-forward skipped")
		return emitLabPortbridgeDirectionProbe(aw, layout, proposal, mode, opts, bridge.ListenAddress(), "", nil)
	}
	got, err := probeTCPBridge(ctx, bridge.ListenAddress(), opts.send, opts.expect, opts.timeout)
	if err != nil {
		return emitLabPortbridgeDirectionProbe(aw, layout, proposal, mode, opts, bridge.ListenAddress(), got, err)
	}
	if opts.expect != "" && got != opts.expect {
		return emitLabPortbridgeDirectionProbe(aw, layout, proposal, mode, opts, bridge.ListenAddress(), got, fmt.Errorf("lab portbridge %s expected %q, got %q", mode, opts.expect, got))
	}
	fmt.Fprintln(a.stdout, "probe=tcp-forward ok")
	if opts.expect != "" {
		fmt.Fprintf(a.stdout, "received=%q\n", got)
	}
	return emitLabPortbridgeDirectionProbe(aw, layout, proposal, mode, opts, bridge.ListenAddress(), got, nil)
}

func labPortbridgeTargetFlag(mode string) string {
	if mode == "host-to-guest" {
		return "guest-target"
	}
	return "target"
}

func labPortbridgeDirectionValue(mode string) portbridge.Direction {
	if mode == "host-to-guest" {
		return portbridge.DirectionHostToGuest
	}
	return portbridge.DirectionGuestToHost
}

func (a app) labPortbridgeLoopback(args []string) error {
	opts := labPortbridgeLoopbackOptions{
		listen:  "127.0.0.1:0",
		timeout: 2 * time.Second,
	}
	fs := flag.NewFlagSet("lab portbridge loopback", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&opts.enableLab, "enable-lab", false, "enable lab command execution")
	fs.StringVar(&opts.listen, "listen", opts.listen, "loopback listen address")
	fs.StringVar(&opts.target, "target", "", "explicit target address")
	fs.StringVar(&opts.send, "send", "", "bytes to send through the bridge")
	fs.StringVar(&opts.expect, "expect", "", "expected bytes to read through the bridge")
	fs.DurationVar(&opts.timeout, "timeout", opts.timeout, "probe timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: hideout lab portbridge loopback --enable-lab --target 127.0.0.1:<port> [--listen 127.0.0.1:0] [--send bytes --expect bytes]")
	}
	if !opts.enableLab && os.Getenv("HIDEOUT_ENABLE_LAB") != "1" {
		return errors.New("hideout lab requires --enable-lab or HIDEOUT_ENABLE_LAB=1")
	}
	if strings.TrimSpace(opts.target) == "" {
		return errors.New("lab portbridge loopback requires --target")
	}
	if opts.expect != "" && opts.send == "" {
		return errors.New("lab portbridge loopback requires --send when --expect is set")
	}
	proposal := labPortbridgeLoopbackProposal(opts)
	layout, aw, err := newLabAudit()
	if err != nil {
		return err
	}
	defer aw.Close()
	defer func() {
		_ = os.RemoveAll(layout.TmpDir)
		_ = os.RemoveAll(layout.ShimDir)
	}()
	if _, err := policy.ValidateLabProposal(proposal); err != nil {
		return emitLabPortbridgeProbe(aw, layout, proposal, opts, "", "", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()
	bridge, err := portbridge.Start(ctx, portbridge.Spec{
		ID:            "lab_portbridge_loopback",
		Direction:     portbridge.DirectionGuestToHost,
		ListenScope:   portbridge.ListenScopeLoopback,
		ListenAddress: opts.listen,
		TargetAddress: opts.target,
	})
	if err != nil {
		return emitLabPortbridgeProbe(aw, layout, proposal, opts, "", "", err)
	}
	defer bridge.Close()
	fmt.Fprintln(a.stdout, "Hideout lab: experimental evidence only")
	fmt.Fprintf(a.stdout, "capability=%s\n", proposal.Action)
	fmt.Fprintf(a.stdout, "route=%s\n", proposal.Route)
	fmt.Fprintln(a.stdout, "mode=loopback")
	fmt.Fprintf(a.stdout, "session=%s\n", layout.ID)
	fmt.Fprintf(a.stdout, "audit=%s\n", layout.AuditPath)
	fmt.Fprintf(a.stdout, "listen=%s\n", bridge.ListenAddress())
	fmt.Fprintf(a.stdout, "target=%s\n", opts.target)
	if opts.send == "" {
		fmt.Fprintln(a.stdout, "probe=tcp-forward skipped")
		return emitLabPortbridgeProbe(aw, layout, proposal, opts, bridge.ListenAddress(), "", nil)
	}
	got, err := probeTCPBridge(ctx, bridge.ListenAddress(), opts.send, opts.expect, opts.timeout)
	if err != nil {
		return emitLabPortbridgeProbe(aw, layout, proposal, opts, bridge.ListenAddress(), got, err)
	}
	if opts.expect != "" && got != opts.expect {
		return emitLabPortbridgeProbe(aw, layout, proposal, opts, bridge.ListenAddress(), got, fmt.Errorf("lab portbridge loopback expected %q, got %q", opts.expect, got))
	}
	fmt.Fprintln(a.stdout, "probe=tcp-forward ok")
	if opts.expect != "" {
		fmt.Fprintf(a.stdout, "received=%q\n", got)
	}
	return emitLabPortbridgeProbe(aw, layout, proposal, opts, bridge.ListenAddress(), got, nil)
}

func labPortbridgeLoopbackProposal(opts labPortbridgeLoopbackOptions) policy.LabProposal {
	return policy.LabProposal{
		Subject:  "lab:portbridge",
		Decision: policy.Allow,
		Route:    policy.LabProbe,
		Action:   policy.ActionPortbridgeProbe,
		Resources: []string{
			"portbridge:loopback",
			"listen:" + opts.listen,
			"target:" + opts.target,
		},
		Reason: "loopback port bridge capability probe",
	}
}

func labPortbridgeDirectionProposal(mode string, opts labPortbridgeDirectionOptions) policy.LabProposal {
	return policy.LabProposal{
		Subject:  "lab:portbridge",
		Decision: policy.Allow,
		Route:    policy.LabProbe,
		Action:   policy.ActionPortbridgeProbe,
		Resources: []string{
			"portbridge:" + mode,
			"listen:" + opts.listen,
			labPortbridgeTargetFlag(mode) + ":" + opts.target,
		},
		Reason: mode + " port bridge capability probe",
	}
}

func (a app) labBrowserControl(args []string) error {
	opts := labBrowserControlOptions{timeout: 2 * time.Second}
	fs := flag.NewFlagSet("lab browser-control", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&opts.enableLab, "enable-lab", false, "enable lab command execution")
	fs.StringVar(&opts.profileName, "profile", "", "explicit Hideout profile name")
	fs.StringVar(&opts.browserPath, "browser-path", os.Getenv("HIDEOUT_BROWSER_PATH"), "direct Chromium-compatible browser binary")
	fs.DurationVar(&opts.timeout, "timeout", opts.timeout, "probe timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: hideout lab browser-control --enable-lab --profile <name> [--browser-path <path>]")
	}
	if !opts.enableLab && os.Getenv("HIDEOUT_ENABLE_LAB") != "1" {
		return errors.New("hideout lab requires --enable-lab or HIDEOUT_ENABLE_LAB=1")
	}
	if strings.TrimSpace(opts.profileName) == "" {
		return errors.New("lab browser-control requires --profile")
	}
	if err := profile.ValidateName(opts.profileName); err != nil {
		return err
	}
	proposal := labBrowserControlProposal(opts)
	layout, aw, err := newLabAudit()
	if err != nil {
		return err
	}
	defer aw.Close()
	defer cleanupLabLayout(layout)
	if _, err := policy.ValidateLabProposal(proposal); err != nil {
		return emitLabBrowserControlProbe(aw, layout, proposal, opts, "", "", false, err)
	}
	if strings.TrimSpace(opts.browserPath) == "" {
		return emitLabBrowserControlProbe(aw, layout, proposal, opts, "", "", false, errors.New("lab browser-control requires --browser-path or HIDEOUT_BROWSER_PATH"))
	}
	browserProfileDir := filepath.Join(layout.TmpDir, "browser-control-profile")
	controlURL, browserName, wsPresent, err := probeLabBrowserControl(context.Background(), opts, browserProfileDir)
	if err != nil {
		return emitLabBrowserControlProbe(aw, layout, proposal, opts, controlURL, browserName, wsPresent, err)
	}
	a.printLabProbeEvidence(layout, proposal,
		labOutputField{"mode", "browser-control"},
		labOutputField{"profile", opts.profileName},
		labOutputField{"browser-profile", "present"},
		labOutputField{"control-url", controlURL},
		labOutputField{"browser", browserName},
		labOutputField{"probe", "devtools-version ok"},
	)
	return emitLabBrowserControlProbe(aw, layout, proposal, opts, controlURL, browserName, wsPresent, nil)
}

func labBrowserControlProposal(opts labBrowserControlOptions) policy.LabProposal {
	return policy.LabProposal{
		Subject:  "lab:browser",
		Decision: policy.Allow,
		Route:    policy.LabProbe,
		Action:   policy.ActionBrowserControlProbe,
		Resources: []string{
			"browser-control:loopback",
			"profile:" + opts.profileName,
		},
		Reason: "browser control capability probe",
	}
}

func probeLabBrowserControl(ctx context.Context, opts labBrowserControlOptions, browserProfileDir string) (string, string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, opts.timeout)
	defer cancel()
	if err := os.MkdirAll(browserProfileDir, 0o700); err != nil {
		return "", "", false, err
	}
	launcher, args, err := labBrowserControlCommand(opts.browserPath, browserProfileDir)
	if err != nil {
		return "", "", false, err
	}
	cmd := exec.CommandContext(ctx, launcher, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return "", "", false, err
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		close(done)
	}()
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(500 * time.Millisecond):
		}
	}()
	port, err := waitForDevToolsActivePort(ctx, browserProfileDir, done)
	if err != nil {
		return "", "", false, err
	}
	controlURL := "http://" + net.JoinHostPort("127.0.0.1", port) + "/json/version"
	browserName, wsPresent, err := probeDevToolsVersion(ctx, controlURL, opts.timeout)
	return controlURL, browserName, wsPresent, err
}

func labBrowserControlCommand(browserPath, browserProfileDir string) (string, []string, error) {
	opener := hostopen.Opener{
		BrowserPath:       browserPath,
		BrowserProfileDir: browserProfileDir,
	}
	launcher, args, err := opener.URLCommand("about:blank")
	if err != nil {
		return "", nil, err
	}
	args = append([]string{
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=0",
	}, args...)
	return launcher, args, nil
}

func waitForDevToolsActivePort(ctx context.Context, browserProfileDir string, browserDone <-chan error) (string, error) {
	path := filepath.Join(browserProfileDir, "DevToolsActivePort")
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) > 0 && strings.TrimSpace(lines[0]) != "" {
				port := strings.TrimSpace(lines[0])
				value, err := strconv.Atoi(port)
				if err == nil && value > 0 && value <= 65535 {
					return port, nil
				}
				return "", fmt.Errorf("browser DevToolsActivePort contains invalid port %q", port)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		select {
		case err, ok := <-browserDone:
			if ok && err != nil {
				return "", fmt.Errorf("browser exited before control endpoint became ready: %w", err)
			}
			return "", errors.New("browser exited before control endpoint became ready")
		case <-ctx.Done():
			return "", fmt.Errorf("browser control endpoint did not become ready: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func probeDevToolsVersion(ctx context.Context, controlURL string, timeout time.Duration) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, controlURL, nil)
	if err != nil {
		return "", false, err
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", false, fmt.Errorf("browser control version endpoint returned HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Browser              string `json:"Browser"`
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, 16*1024))
	if err := dec.Decode(&payload); err != nil {
		return "", false, err
	}
	if strings.TrimSpace(payload.Browser) == "" {
		return "", false, errors.New("browser control version endpoint did not report browser name")
	}
	return payload.Browser, strings.TrimSpace(payload.WebSocketDebuggerURL) != "", nil
}

func (a app) labPreviewOpen(args []string) error {
	opts := labPreviewOpenOptions{timeout: 2 * time.Second}
	fs := flag.NewFlagSet("lab preview-open", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&opts.enableLab, "enable-lab", false, "enable lab command execution")
	fs.StringVar(&opts.guestURL, "guest-url", "", "explicit guest HTTP URL")
	fs.DurationVar(&opts.timeout, "timeout", opts.timeout, "probe timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: hideout lab preview-open --enable-lab --guest-url http://127.0.0.1:<port>")
	}
	if !opts.enableLab && os.Getenv("HIDEOUT_ENABLE_LAB") != "1" {
		return errors.New("hideout lab requires --enable-lab or HIDEOUT_ENABLE_LAB=1")
	}
	if strings.TrimSpace(opts.guestURL) == "" {
		return errors.New("lab preview-open requires --guest-url")
	}
	if err := validateLabGuestURL(opts.guestURL); err != nil {
		return err
	}
	proposal := labPreviewOpenProposal(opts)
	layout, aw, err := newLabAudit()
	if err != nil {
		return err
	}
	defer aw.Close()
	defer cleanupLabLayout(layout)
	if _, err := policy.ValidateLabProposal(proposal); err != nil {
		return emitLabPreviewOpenProbe(aw, layout, proposal, opts, "", 0, err)
	}
	guestURL, err := url.Parse(opts.guestURL)
	if err != nil {
		return emitLabPreviewOpenProbe(aw, layout, proposal, opts, "", 0, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()
	bridge, err := portbridge.Start(ctx, portbridge.Spec{
		ID:            "lab_preview_open",
		Direction:     portbridge.DirectionHostToGuest,
		ListenScope:   portbridge.ListenScopeLoopback,
		ListenAddress: "127.0.0.1:0",
		TargetAddress: net.JoinHostPort(guestURL.Hostname(), guestURL.Port()),
	})
	if err != nil {
		return emitLabPreviewOpenProbe(aw, layout, proposal, opts, "", 0, err)
	}
	defer bridge.Close()
	hostURL := labPreviewHostURL(*guestURL, bridge.ListenAddress())
	statusCode, err := probeLabHTTP(ctx, hostURL, opts.timeout)
	if err != nil {
		return emitLabPreviewOpenProbe(aw, layout, proposal, opts, hostURL, statusCode, err)
	}
	a.printLabProbeEvidence(layout, proposal,
		labOutputField{"mode", "preview-open"},
		labOutputField{"guest-url", opts.guestURL},
		labOutputField{"host-url", hostURL},
		labOutputField{"status-code", fmt.Sprint(statusCode)},
		labOutputField{"probe", "http-get ok"},
	)
	return emitLabPreviewOpenProbe(aw, layout, proposal, opts, hostURL, statusCode, nil)
}

func validateLabGuestURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("lab preview-open guest URL is invalid: %w", err)
	}
	if u.Scheme != "http" {
		return errors.New("lab preview-open guest URL must use http")
	}
	if u.User != nil {
		return errors.New("lab preview-open guest URL must not contain user info")
	}
	host := strings.ToLower(u.Hostname())
	if host != "127.0.0.1" && host != "localhost" {
		return errors.New("lab preview-open guest URL must target 127.0.0.1 or localhost")
	}
	if u.Port() == "" {
		return errors.New("lab preview-open guest URL must include an explicit port")
	}
	return nil
}

func labPreviewHostURL(guestURL url.URL, listenAddress string) string {
	guestURL.Scheme = "http"
	guestURL.Host = listenAddress
	return guestURL.String()
}

func probeLabHTTP(ctx context.Context, target string, timeout time.Duration) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, err
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	return resp.StatusCode, nil
}

func labPreviewOpenProposal(opts labPreviewOpenOptions) policy.LabProposal {
	return policy.LabProposal{
		Subject:  "lab:preview",
		Decision: policy.Allow,
		Route:    policy.LabProbe,
		Action:   policy.ActionPreviewOpenProbe,
		Resources: []string{
			"preview-open:guest-http",
			"guest-url:" + opts.guestURL,
		},
		Reason: "preview open capability probe",
	}
}

func newLabAudit() (session.Layout, *audit.Writer, error) {
	store, err := profile.DefaultStore()
	if err != nil {
		return session.Layout{}, nil, err
	}
	if err := os.MkdirAll(store.Root, 0o700); err != nil {
		return session.Layout{}, nil, err
	}
	layout, err := session.New(store.Root)
	if err != nil {
		return session.Layout{}, nil, err
	}
	aw, err := audit.NewFile(layout.AuditPath)
	if err != nil {
		return session.Layout{}, nil, err
	}
	return layout, aw, nil
}

func cleanupLabLayout(layout session.Layout) {
	_ = os.RemoveAll(layout.TmpDir)
	_ = os.RemoveAll(layout.ShimDir)
}

func (a app) printLabProbeEvidence(layout session.Layout, proposal policy.LabProposal, fields ...labOutputField) {
	fmt.Fprintln(a.stdout, "Hideout lab: experimental evidence only")
	fmt.Fprintf(a.stdout, "capability=%s\n", proposal.Action)
	fmt.Fprintf(a.stdout, "route=%s\n", proposal.Route)
	fmt.Fprintf(a.stdout, "session=%s\n", layout.ID)
	fmt.Fprintf(a.stdout, "audit=%s\n", layout.AuditPath)
	for _, field := range fields {
		fmt.Fprintf(a.stdout, "%s=%s\n", field.key, field.value)
	}
}

func emitLabPortbridgeDirectionProbe(aw *audit.Writer, layout session.Layout, proposal policy.LabProposal, mode string, opts labPortbridgeDirectionOptions, listen, received string, probeErr error) error {
	targetField := labPortbridgeTargetFlag(mode)
	decision := "allow"
	status := "error"
	if opts.send == "" && probeErr == nil {
		status = "skipped"
	} else if probeErr == nil {
		status = "ok"
	} else {
		decision = "error"
	}
	details := map[string]any{
		"probe":         "portbridge." + mode,
		"subject":       proposal.Subject,
		"route":         string(proposal.Route),
		"mode":          mode,
		"listen":        listen,
		targetField:     audit.RedactString(opts.target),
		"sendBytes":     len(opts.send),
		"expectBytes":   len(opts.expect),
		"receivedBytes": len(received),
		"status":        status,
		"timeoutMs":     opts.timeout.Milliseconds(),
		"targetField":   targetField,
	}
	if probeErr != nil {
		details["error"] = probeErr.Error()
	}
	if err := aw.Emit(audit.Event{
		Session:  layout.ID,
		Profile:  "lab",
		Backend:  "native",
		Action:   proposal.Action,
		Decision: decision,
		Details:  details,
	}); err != nil {
		return err
	}
	return probeErr
}

func emitLabBrowserControlProbe(aw *audit.Writer, layout session.Layout, proposal policy.LabProposal, opts labBrowserControlOptions, controlURL, browserName string, wsPresent bool, probeErr error) error {
	decision := "allow"
	status := "ok"
	if probeErr != nil {
		decision = "error"
		status = "error"
	}
	if isLabProbeNotImplemented(probeErr) {
		status = "not-implemented"
	}
	details := map[string]any{
		"probe":                       "browser-control",
		"subject":                     proposal.Subject,
		"route":                       string(proposal.Route),
		"mode":                        "browser-control",
		"profile":                     audit.RedactString(opts.profileName),
		"browserPath":                 opts.browserPath,
		"browserProfile":              "present",
		"controlURL":                  audit.RedactString(controlURL),
		"browser":                     browserName,
		"webSocketDebuggerURLPresent": wsPresent,
		"status":                      status,
		"timeoutMs":                   opts.timeout.Milliseconds(),
	}
	if probeErr != nil {
		details["error"] = probeErr.Error()
		if isLabProbeNotImplemented(probeErr) {
			details["errorType"] = "lab-probe-not-implemented"
		}
	}
	if err := aw.Emit(audit.Event{
		Session:  layout.ID,
		Profile:  "lab",
		Backend:  "native",
		Action:   proposal.Action,
		Decision: decision,
		Details:  details,
	}); err != nil {
		return err
	}
	return probeErr
}

func emitLabPreviewOpenProbe(aw *audit.Writer, layout session.Layout, proposal policy.LabProposal, opts labPreviewOpenOptions, hostURL string, statusCode int, probeErr error) error {
	decision := "allow"
	status := "ok"
	if probeErr != nil {
		decision = "error"
		status = "error"
		if isLabProbeNotImplemented(probeErr) {
			status = "not-implemented"
		}
	}
	details := map[string]any{
		"probe":          "preview-open",
		"subject":        proposal.Subject,
		"route":          string(proposal.Route),
		"mode":           "preview-open",
		"guestURL":       audit.RedactString(opts.guestURL),
		"hostURL":        audit.RedactString(hostURL),
		"httpStatusCode": statusCode,
		"status":         status,
		"timeoutMs":      opts.timeout.Milliseconds(),
	}
	if probeErr != nil {
		details["error"] = probeErr.Error()
		if isLabProbeNotImplemented(probeErr) {
			details["errorType"] = "lab-probe-not-implemented"
		}
	}
	if err := aw.Emit(audit.Event{
		Session:  layout.ID,
		Profile:  "lab",
		Backend:  "native",
		Action:   proposal.Action,
		Decision: decision,
		Details:  details,
	}); err != nil {
		return err
	}
	return probeErr
}

func emitLabPortbridgeProbe(aw *audit.Writer, layout session.Layout, proposal policy.LabProposal, opts labPortbridgeLoopbackOptions, listen, received string, probeErr error) error {
	decision := "allow"
	status := "ok"
	if opts.send == "" && probeErr == nil {
		status = "skipped"
	}
	if probeErr != nil {
		decision = "error"
		status = "error"
	}
	details := map[string]any{
		"probe":         "portbridge.loopback",
		"subject":       proposal.Subject,
		"route":         string(proposal.Route),
		"mode":          "loopback",
		"listen":        listen,
		"target":        audit.RedactString(opts.target),
		"sendBytes":     len(opts.send),
		"expectBytes":   len(opts.expect),
		"receivedBytes": len(received),
		"status":        status,
	}
	if probeErr != nil {
		details["error"] = probeErr.Error()
	}
	if err := aw.Emit(audit.Event{
		Session:  layout.ID,
		Profile:  "lab",
		Backend:  "native",
		Action:   proposal.Action,
		Decision: decision,
		Details:  details,
	}); err != nil {
		return err
	}
	return probeErr
}

func probeTCPBridge(ctx context.Context, address, send, expect string, timeout time.Duration) (string, error) {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return "", err
	}
	if _, err := io.WriteString(conn, send); err != nil {
		return "", err
	}
	if expect == "" {
		return "", nil
	}
	buf := make([]byte, len(expect))
	if _, err := io.ReadFull(conn, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func serverError(server *manager.LocalServer) <-chan error {
	ch := make(chan error, 1)
	go func() {
		ch <- server.Wait()
	}()
	return ch
}
