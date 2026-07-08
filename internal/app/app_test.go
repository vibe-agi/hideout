package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/vibe-agi/hideout/internal/backend/lima"
	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/cmdproxy"
	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/envpolicy"
	"github.com/vibe-agi/hideout/internal/helperbin"
	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/hostfs/overlay"
	"github.com/vibe-agi/hideout/internal/inittask"
	"github.com/vibe-agi/hideout/internal/manager"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/packagekit"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/session"
)

func TestExplainInitializesProfileAndPrintsBoundary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{"explain", "--profile", "test", "--", "echo", "hi"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Known limitation") {
		t.Fatalf("explain missing limitation: %s", out.String())
	}
	for _, want := range []string{
		"Identity: profileId=prf_",
		"Target command: echo hi",
		"Workspace visibility: guest can read/write mapped workspace contents",
		"HostFS Portal: roots=/hideout/hostfs,/Users,/Volumes,/private default=hidden profileGrants=0 runGrants=0 totalGrants=0 denyRules=0 write=unsupported",
		"HostFS data plane: inactive because no HostFS grants are active",
		"Proxy env in target: absent",
		"HOSTNAME",
		"Identity env: user=developer hostname=devbox timezone=UTC locale=en_US.UTF-8",
		"Machine identity: generated machine-id present in persistent profile identity root (value hidden)",
		"Host broker capability: host.open allows external http/https URLs and mapped workspace files only",
		"Host browser network: localhost, loopback, private, CGNAT, benchmarking, link-local, multicast, .local, and .localhost URL targets are denied before host open",
		"Host browser control: no DevTools or remote-debugging port is exposed to the guest in Phase 1",
		"Browser profile:",
		"Known limitation: Phase 1 does not audit every child process inside the guest.",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("explain output missing %q:\n%s", want, out.String())
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".hideout", "profiles", "test", "profile.json")); err != nil {
		t.Fatalf("profile not initialized: %v", err)
	}
}

func TestInitNoInputCreatesStoreProfileAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{"init", "--no-input", "--backend", "native", "--network", "direct"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("init exit=%d stderr=%s", code, errOut.String())
	}
	for _, want := range []string{
		"Hideout init",
		"task store.create: applied",
		"task schema.metadata.write: applied",
		"task profile.create: applied",
		"audit=",
		"next:",
		"check: hideout doctor --profile default --backend native",
		"smoke: hideout run --profile default --backend native --allow-weak-isolation -- pwd",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("init output missing %q:\n%s", want, out.String())
		}
	}
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	loaded, err := store.Load("default")
	if err != nil {
		t.Fatalf("default profile not initialized: %v", err)
	}
	initialIdentityID := loaded.Metadata["identityId"]
	for _, path := range []string{
		filepath.Join(store.Root, "install-state.json"),
		filepath.Join(store.Root, "logs", "init-audit.jsonl"),
		filepath.Join(store.Root, "runtime"),
		filepath.Join(store.Root, "bin"),
		filepath.Join(store.Root, "profiles", "default", "machine", "machine-id"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("init did not create %s: %v", path, err)
		}
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"init", "--no-input", "--backend", "native", "--network", "direct"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("second init exit=%d stderr=%s", code, errOut.String())
	}
	if strings.Contains(out.String(), "task profile.create: applied") {
		t.Fatalf("second init should be idempotent, got:\n%s", out.String())
	}
	reloaded, err := store.Load("default")
	if err != nil {
		t.Fatalf("reload default profile: %v", err)
	}
	if reloaded.Metadata["identityId"] != initialIdentityID {
		t.Fatalf("idempotent init rotated identity: before=%s after=%s", initialIdentityID, reloaded.Metadata["identityId"])
	}
}

func TestUsageGroupsNewUserAndAdvancedCommands(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Main([]string{"help"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("help exit=%d stderr=%s", code, errOut.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("help should not write stderr: %s", errOut.String())
	}
	text := out.String()
	for _, want := range []string{
		"Usage:",
		"  hideout init [--no-input] [--backend lima|auto] [--network direct]",
		"  hideout doctor",
		"  hideout run [flags] -- <command> [args...]",
		"First run:",
		"Run and explain:",
		"Profile and HostFS:",
		"Inspect and manage:",
		"Advanced and developer:",
		"  hideout run --backend native --allow-weak-isolation -- <command>  # dev harness only",
		"  hideout version",
		"Lab probes:",
		"  hideout lab portbridge loopback --enable-lab --target 127.0.0.1:<port>",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("help output missing %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "First run:") > strings.Index(text, "Advanced and developer:") {
		t.Fatalf("help should show first-run path before advanced commands:\n%s", text)
	}
	if strings.Index(text, "Advanced and developer:") > strings.Index(text, "Lab probes:") {
		t.Fatalf("help should keep lab probes after advanced commands:\n%s", text)
	}
}

func TestHostFSWriteCLIClaimAndDiscard(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	sessionID := "ses_20260708T010000Z_00112233445566778899"
	overlayStore, err := overlay.NewStore(filepath.Join(store.Root, "sessions", sessionID, "hostfs-overlay"))
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target.txt")
	result, err := overlayStore.Stage(overlay.StageRequest{
		SessionID:   sessionID,
		Profile:     "default",
		Backend:     "native",
		Operation:   "create",
		Path:        target,
		GrantID:     "hfs_overlay",
		GrantSource: "profile",
		Data:        []byte("staged"),
		Privilege:   overlay.Privilege{Status: "enforced", Reason: "target-no-sudo"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Main([]string{"hostfs", "write", "status"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("status exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), result.Decision.DecisionID) {
		t.Fatalf("status missing decision: %s", out.String())
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"hostfs", "write", "claim", result.Decision.DecisionID}, &out, &errOut)
	if code != 0 {
		t.Fatalf("claim exit=%d stderr=%s", code, errOut.String())
	}
	var claim overlay.ClaimResponse
	if err := json.Unmarshal(out.Bytes(), &claim); err != nil {
		t.Fatal(err)
	}
	if claim.ClaimToken == "" || claim.DecisionID != result.Decision.DecisionID {
		t.Fatalf("bad claim: %+v body=%s", claim, out.String())
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"hostfs", "write", "status"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("status after claim exit=%d stderr=%s", code, errOut.String())
	}
	if strings.Contains(out.String(), claim.ClaimToken) || strings.Contains(out.String(), "tokenHash") {
		t.Fatalf("status leaked claim token material: %s", out.String())
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"hostfs", "write", "apply", "--claim-token", claim.ClaimToken, result.Decision.DecisionID}, &out, &errOut)
	if code != 0 {
		t.Fatalf("apply exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), `"status": "applied"`) {
		t.Fatalf("apply output mismatch: %s", out.String())
	}
	if body, err := os.ReadFile(target); err != nil || string(body) != "staged" {
		t.Fatalf("apply host body=%q err=%v", body, err)
	}

	discardTarget := filepath.Join(t.TempDir(), "discard.txt")
	discardResult, err := overlayStore.Stage(overlay.StageRequest{
		SessionID:   sessionID,
		Profile:     "default",
		Backend:     "native",
		Operation:   "create",
		Path:        discardTarget,
		GrantID:     "hfs_overlay",
		GrantSource: "profile",
		Data:        []byte("discard"),
		Privilege:   overlay.Privilege{Status: "enforced", Reason: "target-no-sudo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	code = Main([]string{"hostfs", "write", "claim", discardResult.Decision.DecisionID}, &out, &errOut)
	if code != 0 {
		t.Fatalf("discard claim exit=%d stderr=%s", code, errOut.String())
	}
	var discardClaim overlay.ClaimResponse
	if err := json.Unmarshal(out.Bytes(), &discardClaim); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	code = Main([]string{"hostfs", "write", "discard", "--claim-token", discardClaim.ClaimToken, discardResult.Decision.DecisionID}, &out, &errOut)
	if code != 0 {
		t.Fatalf("discard exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), `"status": "discarded"`) {
		t.Fatalf("discard output mismatch: %s", out.String())
	}
}

func TestDecisionAndNoticeCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	sessionID := "ses_20260708T020000Z_00112233445566778899"
	overlayStore, err := overlay.NewStore(filepath.Join(store.Root, "sessions", sessionID, "hostfs-overlay"))
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target.txt")
	result, err := overlayStore.Stage(overlay.StageRequest{
		SessionID:   sessionID,
		Profile:     "default",
		Backend:     "native",
		Operation:   "create",
		Path:        target,
		GrantID:     "hfs_overlay",
		GrantSource: "profile",
		Data:        []byte("staged"),
		Privilege:   overlay.Privilege{Status: "enforced", Reason: "target-no-sudo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	core := manager.New(store)
	if _, err := core.CreateNotice(decision.Notice{
		ID:       "privilege-degraded-default",
		Kind:     decision.KindPrivilegeStatus,
		Severity: decision.NoticeSeverityWarning,
		Status:   "degraded",
		Preview:  decision.Preview{Summary: "target can passwordless sudo"},
		AuditRef: "audit:privilege",
	}); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Main([]string{"decision", "list", "--kind", "hostfs.write"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("decision list exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), result.Decision.DecisionID) || strings.Contains(out.String(), "tokenHash") {
		t.Fatalf("decision list mismatch: %s", out.String())
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"decision", "claim", result.Decision.DecisionID}, &out, &errOut)
	if code != 0 {
		t.Fatalf("decision claim exit=%d stderr=%s", code, errOut.String())
	}
	var claim decision.ClaimResponse
	if err := json.Unmarshal(out.Bytes(), &claim); err != nil {
		t.Fatal(err)
	}
	if claim.ClaimToken == "" {
		t.Fatalf("missing claim token: %s", out.String())
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"decision", "deny", "--claim-token", "claim_wrong", result.Decision.DecisionID}, &out, &errOut)
	if code == 0 || !strings.Contains(errOut.String(), "claimToken is invalid") {
		t.Fatalf("wrong token should fail closed: code=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"decision", "deny", "--claim-token", claim.ClaimToken, result.Decision.DecisionID}, &out, &errOut)
	if code != 0 {
		t.Fatalf("decision deny exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), `"status": "discarded"`) {
		t.Fatalf("decision deny mismatch: %s", out.String())
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"notice", "list", "--severity", "warning"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("notice list exit=%d stderr=%s", code, errOut.String())
	}
	if strings.Contains(out.String(), "defaultOutcome") || strings.Contains(out.String(), "claimToken") {
		t.Fatalf("notice list exposed decision semantics: %s", out.String())
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"notice", "ack", "privilege-degraded-default"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("notice ack exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), `"surface": "cli"`) {
		t.Fatalf("notice ack mismatch: %s", out.String())
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"decision", "watch"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("decision watch exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), `"unackedNotices": 0`) || strings.Contains(out.String(), "hostfs-overlay") {
		t.Fatalf("decision watch diagnostic mismatch: %s", out.String())
	}
}

func TestVersionCommand(t *testing.T) {
	oldVersion, oldCommit, oldBuildTime := Version, Commit, BuildTime
	Version = "test-version"
	Commit = "abc123def456"
	BuildTime = "2026-07-05T00:00:00Z"
	t.Cleanup(func() {
		Version, Commit, BuildTime = oldVersion, oldCommit, oldBuildTime
	})

	for _, args := range [][]string{{"version"}, {"--version"}, {"-v"}} {
		var out, errOut bytes.Buffer
		code := Main(args, &out, &errOut)
		if code != 0 {
			t.Fatalf("%v exit=%d stderr=%s", args, code, errOut.String())
		}
		if errOut.Len() != 0 {
			t.Fatalf("%v should not write stderr: %s", args, errOut.String())
		}
		for _, want := range []string{
			"hideout test-version",
			"commit: abc123def456",
			"builtAt: 2026-07-05T00:00:00Z",
			"go: ",
			"platform: " + runtime.GOOS + "/" + runtime.GOARCH,
		} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("%v version output missing %q:\n%s", args, want, out.String())
			}
		}
	}
}

func TestSubcommandHelpIsSuccessfulAndQuiet(t *testing.T) {
	for _, args := range [][]string{
		{"init", "--help"},
		{"run", "--help"},
		{"doctor", "--help"},
		{"cleanup", "--help"},
		{"audit", "show", "--help"},
		{"profile", "--help"},
		{"profile", "fs", "--help"},
		{"profile", "fs", "default", "add", "--help"},
		{"profile", "fs", "default", "remove", "--help"},
		{"profile", "tools", "--help"},
		{"profile", "tools", "default", "expected", "--help"},
		{"profile", "tools", "default", "expected", "add", "--help"},
		{"profile", "env", "--help"},
		{"profile", "env", "default", "set", "--help"},
		{"profile", "home", "--help"},
		{"profile", "home", "default", "import", "--help"},
		{"profile", "command-proxy", "--help"},
		{"profile", "command-proxy", "default", "add-open", "--help"},
		{"package", "--help"},
		{"package", "verify", "--help"},
		{"shim", "--help"},
		{"shim", "build-linux", "--help"},
		{"hostfsd", "--help"},
		{"hostfsd", "build-linux", "--help"},
		{"lab", "--help"},
		{"lab", "portbridge", "--help"},
		{"lab", "portbridge", "loopback", "--help"},
		{"lab", "browser-control", "--help"},
		{"lab", "preview-open", "--help"},
	} {
		var out, errOut bytes.Buffer
		code := Main(args, &out, &errOut)
		if code != 0 {
			t.Fatalf("%v exit=%d stderr=%s", args, code, errOut.String())
		}
		if errOut.Len() != 0 {
			t.Fatalf("%v should not write stderr: %s", args, errOut.String())
		}
		if !strings.Contains(out.String(), "Usage:") {
			t.Fatalf("%v help missing usage output:\n%s", args, out.String())
		}
		if strings.Contains(out.String(), "flag: help requested") {
			t.Fatalf("%v help leaked flag.ErrHelp:\n%s", args, out.String())
		}
	}
}

func TestProfileLeafHelpIsSpecific(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"profile", "fs", "default", "add", "--help"}, "hideout profile fs <name> add --fs <kind:/path>"},
		{[]string{"profile", "tools", "default", "expected", "add", "--help"}, "hideout profile tools <name> expected add <command>"},
		{[]string{"profile", "home", "default", "import", "--help"}, "hideout profile home <name> import --from <path>"},
		{[]string{"profile", "command-proxy", "default", "add-open", "--help"}, "hideout profile command-proxy <name> add-open <command>"},
	}
	for _, tt := range tests {
		var out, errOut bytes.Buffer
		code := Main(tt.args, &out, &errOut)
		if code != 0 {
			t.Fatalf("%v exit=%d stderr=%s", tt.args, code, errOut.String())
		}
		if !strings.Contains(out.String(), tt.want) {
			t.Fatalf("%v help missing %q:\n%s", tt.args, tt.want, out.String())
		}
		if strings.Contains(out.String(), "hideout init [--no-input]") {
			t.Fatalf("%v should not fall back to top-level usage:\n%s", tt.args, out.String())
		}
	}
}

func TestPrimaryCommandHelpIsSpecific(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"init", "--help"}, "hideout init [flags]"},
		{[]string{"doctor", "--help"}, "hideout doctor --fix [--dry-run] [flags]"},
		{[]string{"run", "--help"}, "hideout run [flags] -- <command> [args...]"},
		{[]string{"explain", "--help"}, "hideout explain [flags] -- <command> [args...]"},
	}
	for _, tt := range tests {
		var out, errOut bytes.Buffer
		code := Main(tt.args, &out, &errOut)
		if code != 0 {
			t.Fatalf("%v exit=%d stderr=%s", tt.args, code, errOut.String())
		}
		if !strings.Contains(out.String(), tt.want) {
			t.Fatalf("%v help missing %q:\n%s", tt.args, tt.want, out.String())
		}
		if strings.Contains(out.String(), "Profile and HostFS:") {
			t.Fatalf("%v should not fall back to top-level usage:\n%s", tt.args, out.String())
		}
	}
}

func TestRunTargetHelpIsNotConsumedByHideout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{"run", "--explain", "--", "echo", "--help"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Target command: echo --help") {
		t.Fatalf("run explain should preserve target --help:\n%s", out.String())
	}
	if strings.Contains(out.String(), "hideout run [flags] -- <command>") {
		t.Fatalf("target --help should not trigger Hideout usage:\n%s", out.String())
	}
}

func TestPackageVerifyAcceptsValidPackage(t *testing.T) {
	root := writeTestPackageRoot(t)
	var out, errOut bytes.Buffer
	code := Main([]string{"package", "verify", root}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "package: ok mode=artifact root=") {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

func TestPackageVerifyRejectsChecksumMismatch(t *testing.T) {
	root := writeTestPackageRoot(t)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{"package", "verify", root}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected failure stdout=%s", out.String())
	}
	if !strings.Contains(errOut.String(), "package checksum mismatch for README.md") {
		t.Fatalf("missing checksum error: %s", errOut.String())
	}
}

func TestPackageVerifyRejectsLayoutFileWithoutChecksum(t *testing.T) {
	root := writeTestPackageRoot(t)
	manifestPath := filepath.Join(root, "package-manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest packagekit.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	var files []packagekit.File
	for _, file := range manifest.Files {
		if file.Path != "bin/hideout-shim" {
			files = append(files, file)
		}
	}
	manifest.Files = files
	data, err = json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{"package", "verify", root}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected failure stdout=%s", out.String())
	}
	if !strings.Contains(errOut.String(), `layout path "bin/hideout-shim" is not covered`) {
		t.Fatalf("missing checksum coverage error: %s", errOut.String())
	}
}

func TestPackageVerifyRejectsSymlinkManifestFile(t *testing.T) {
	root := writeTestPackageRoot(t)
	readme := filepath.Join(root, "README.md")
	target := filepath.Join(root, "README.target")
	data, err := os.ReadFile(readme)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(readme); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("README.target", readme); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{"package", "verify", root}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected failure stdout=%s", out.String())
	}
	if !strings.Contains(errOut.String(), `README.md": must not be a symlink`) {
		t.Fatalf("missing symlink error: %s", errOut.String())
	}
}

func writeTestPackageRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"bin", "schemas", "docs", "packaging"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]struct {
		kind string
		mode os.FileMode
		data string
	}{
		"bin/hideout":                          {kind: "binary", mode: 0o755, data: "#!/bin/sh\n"},
		"bin/hideout-shim":                     {kind: "binary", mode: 0o755, data: "#!/bin/sh\n"},
		"bin/hideout-shim-linux-arm64":         {kind: "linux-helper", mode: 0o755, data: "#!/bin/sh\n"},
		"bin/hideout-hostfsd-linux-arm64":      {kind: "linux-helper", mode: 0o755, data: "#!/bin/sh\n"},
		"install.sh":                           {kind: "installer", mode: 0o755, data: "#!/bin/sh\n"},
		"README.md":                            {kind: "entrypoint", mode: 0o644, data: "readme\n"},
		"README.zh-CN.md":                      {kind: "entrypoint", mode: 0o644, data: "readme zh\n"},
		"schemas/package-manifest.schema.json": {kind: "schema", mode: 0o644, data: "{}\n"},
		"schemas/release-dogfood.schema.json":  {kind: "schema", mode: 0o644, data: "{}\n"},
	}
	manifest := packagekit.Manifest{Schema: packagekit.ArtifactSchema}
	manifest.BuiltAt = "2026-01-01T00:00:00Z"
	manifest.Git.Commit = "test"
	manifest.Target.HostOS = runtime.GOOS
	manifest.Target.HostArch = runtime.GOARCH
	manifest.Target.LinuxGuestArch = runtime.GOARCH
	manifest.Layout.Root = "hideout"
	manifest.Layout.Binaries = []string{
		"bin/hideout",
		"bin/hideout-shim",
		"bin/hideout-shim-linux-arm64",
		"bin/hideout-hostfsd-linux-arm64",
	}
	manifest.Layout.Entrypoints = []string{"install.sh", "README.md", "README.zh-CN.md"}
	manifest.Layout.Directories = []string{"schemas", "docs", "packaging"}
	manifest.Migration.InstallStateSchema = packagekit.InstallStateSchema
	manifest.Migration.FromInstalledSchemas = []string{packagekit.InstallStateSchema}
	for rel, spec := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(spec.data), spec.mode); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(spec.data))
		manifest.Files = append(manifest.Files, packagekit.File{
			Path:       rel,
			Kind:       spec.kind,
			SHA256:     hex.EncodeToString(sum[:]),
			Executable: spec.mode&0o111 != 0,
		})
	}
	slices.SortFunc(manifest.Files, func(a, b packagekit.File) int {
		return strings.Compare(a.Path, b.Path)
	})
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package-manifest.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestDoctorFixDryRunDoesNotCreateProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{"doctor", "--fix", "--dry-run", "--backend", "native"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("doctor --fix --dry-run exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Hideout doctor fix plan") ||
		!strings.Contains(out.String(), "task profile.create: pending") {
		t.Fatalf("doctor fix dry-run output mismatch:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".hideout", "profiles", "default", "profile.json")); !os.IsNotExist(err) {
		t.Fatalf("doctor --fix --dry-run should not create profile, stat err=%v", err)
	}
}

func TestDoctorFixAppliesAndWritesInitAudit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{"doctor", "--fix", "--backend", "native"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("doctor --fix exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	for _, want := range []string{
		"Hideout doctor fix",
		"audit=",
		"task profile.create: applied",
		"next:",
		"check: hideout doctor --profile default --backend native",
		"smoke: hideout run --profile default --backend native --allow-weak-isolation -- pwd",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("doctor fix output missing %q:\n%s", want, out.String())
		}
	}
	auditPath := filepath.Join(home, ".hideout", "logs", "init-audit.jsonl")
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read doctor fix audit: %v", err)
	}
	if !strings.Contains(string(data), `"operation":"doctor.fix.apply"`) ||
		!strings.Contains(string(data), `"taskKind":"profile.create"`) {
		t.Fatalf("doctor fix audit missing expected events: %s", data)
	}
}

func TestInitRejectsLegacyNPMCLIToolFlags(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{
		"init",
		"--no-input",
		"--backend", "native",
		"--network", "direct",
		"--npm-package", "@example/agent-cli@1.2.3",
		"--npm-command", "agent-cli",
		"--npm-command", "agent-helper",
	}, &out, &errOut)
	if code == 0 {
		t.Fatalf("init with legacy npm flags unexpectedly succeeded stdout=%s", out.String())
	}
	if !strings.Contains(errOut.String(), "legacy tool-supply flags are no longer supported") {
		t.Fatalf("init error should explain legacy removal, got %s", errOut.String())
	}
}

func TestWriteInitResultDoesNotSuggestRunWhenPlanHasBlockedTasks(t *testing.T) {
	var out bytes.Buffer
	writeInitResult(&out, "Hideout init", inittask.Result{
		Plan: inittask.Plan{
			Version: "hideout.init/v1",
			Profile: "blocked",
			Backend: "lima",
			Tasks: []inittask.Task{{
				Kind:    "helper.install.linux-shim",
				Status:  "blocked",
				Message: "linux helper source is unavailable",
			}},
			NextSteps: []inittask.NextStep{{
				ID:      "resolve-blocked",
				Label:   "Resolve blocked tasks",
				Command: "hideout doctor --fix --profile blocked --backend lima",
				Message: "Fix blocked tasks above, then rerun doctor fix.",
			}},
		},
		Skipped: []inittask.Task{{
			Kind:    "helper.install.linux-shim",
			Status:  "blocked",
			Message: "linux helper source is unavailable",
		}},
	})
	for _, want := range []string{
		"task helper.install.linux-shim: blocked",
		"next:",
		"resolve: hideout doctor --fix --profile blocked --backend lima",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("init output missing %q:\n%s", want, out.String())
		}
	}
	for _, forbidden := range []string{
		"smoke:",
		"cli:",
	} {
		if strings.Contains(out.String(), forbidden) {
			t.Fatalf("blocked init output should not suggest %q:\n%s", forbidden, out.String())
		}
	}
}

func TestInitConfiguresTun2SocksProxySecretRef(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{
		"init",
		"--no-input",
		"--profile", "privacy",
		"--backend", "native",
		"--network", "tun2socks",
		"--proxy-secret", "default-proxy",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("init tun2socks exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	for _, want := range []string{
		"network: tun2socks",
		"task network.mode.select: applied",
		"set profile network mode to tun2socks",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("init output missing %q:\n%s", want, out.String())
		}
	}
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	loaded, err := store.Load("privacy")
	if err != nil {
		t.Fatalf("load privacy profile: %v", err)
	}
	if loaded.Network.Mode != "tun2socks" || loaded.Network.ProxySecretRef != "default-proxy" {
		t.Fatalf("network settings were not persisted: %+v", loaded.Network)
	}
}

func TestInitTun2SocksRequiresProxySecretRef(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{
		"init",
		"--no-input",
		"--backend", "native",
		"--network", "tun2socks",
	}, &out, &errOut)
	if code == 0 {
		t.Fatalf("init tun2socks without proxy secret unexpectedly succeeded stdout=%s", out.String())
	}
	if !strings.Contains(errOut.String(), "tun2socks network mode requires a proxy secret ref") {
		t.Fatalf("init error should explain missing proxy secret, got %s", errOut.String())
	}
}

func TestDoctorFixRejectsLegacyNPMCLIToolFlagsWithoutState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{
		"doctor",
		"--fix",
		"--dry-run",
		"--backend", "native",
		"--npm-package", "@example/agent-cli@1.2.3",
		"--npm-command", "agent-cli",
	}, &out, &errOut)
	if code == 0 {
		t.Fatalf("doctor --fix --dry-run with legacy npm flags unexpectedly succeeded stdout=%s", out.String())
	}
	if !strings.Contains(errOut.String(), "legacy tool-supply flags are no longer supported") {
		t.Fatalf("doctor fix error should explain legacy removal, got %s", errOut.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".hideout", "profiles", "default", "profile.json")); !os.IsNotExist(err) {
		t.Fatalf("doctor --fix --dry-run should not create profile, stat err=%v", err)
	}
}

func TestDoctorRejectsToolSupplyFlagsWithoutFix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{"doctor", "--backend", "native", "--npm-package", "@example/agent-cli@1.2.3", "--npm-command", "agent-cli"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("doctor without --fix unexpectedly succeeded stdout=%s", out.String())
	}
	if !strings.Contains(errOut.String(), "legacy tool-supply flags are no longer supported") {
		t.Fatalf("doctor error should explain legacy removal, got %s", errOut.String())
	}
}

func TestDoctorFixDryRunPlansLimaHelperRepairs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HIDEOUT_LINUX_SHIM_PATH", "")
	t.Setenv("HIDEOUT_LINUX_HOSTFSD_PATH", "")
	t.Setenv("PATH", "")
	var out, errOut bytes.Buffer
	code := Main([]string{"doctor", "--fix", "--dry-run", "--backend", "lima"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("doctor --fix --dry-run exit=%d stderr=%s", code, errOut.String())
	}
	for _, want := range []string{
		"task helper.install.linux-shim: pending",
		"task helper.install.linux-hostfsd: pending",
		"build hideout-shim linux helper into store",
		"build hideout-hostfsd linux helper into store",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("doctor fix dry-run output missing %q:\n%s", want, out.String())
		}
	}
	for _, path := range []string{
		filepath.Join(home, ".hideout", "bin", "hideout-shim-linux-"+runtime.GOARCH),
		filepath.Join(home, ".hideout", "bin", "hideout-hostfsd-linux-"+runtime.GOARCH),
		filepath.Join(home, ".hideout", "profiles", "default", "profile.json"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("doctor --fix --dry-run should not create %s, stat err=%v", path, err)
		}
	}
}

func TestExplainShowsRunScopedHostFSGrantWithoutPersistingIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	hostFile := filepath.Join(t.TempDir(), "input.txt")
	if err := os.WriteFile(hostFile, []byte("host data"), 0o600); err != nil {
		t.Fatal(err)
	}
	hostDir := filepath.Join(t.TempDir(), "docs")
	if err := os.Mkdir(hostDir, 0o700); err != nil {
		t.Fatal(err)
	}
	hostTree := filepath.Join(t.TempDir(), "assets")
	if err := os.Mkdir(hostTree, 0o700); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{"explain", "--profile", "grant-test", "--fs", "read:" + hostFile, "--fs", "dir:" + hostDir, "--fs", "tree:" + hostTree, "--", "cat", hostFile}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut.String())
	}
	for _, want := range []string{
		"HostFS Portal: roots=/hideout/hostfs,/Users,/Volumes,/private default=hidden profileGrants=0 runGrants=3 totalGrants=3 denyRules=0 write=unsupported",
		"HostFS data plane: enabled for Lima through hideout-hostfsd FUSE; grants do not create backend mounts",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("explain output missing %q:\n%s", want, out.String())
		}
	}
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	loaded, err := store.Load("grant-test")
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}
	if len(loaded.HostFS.Grants) != 0 {
		t.Fatalf("run-scoped grant was persisted into profile: %+v", loaded.HostFS)
	}
}

func TestExplainCanDisableProfileHostFSGrantsForOneRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	p := profile.Default("profile-hostfs-off")
	p.HostFS.Grants = []hostfs.Rule{{
		ID:       "hfs_profile_allow",
		HostPath: "/Users/alice/Downloads/profile.txt",
		Ops:      []hostfs.Op{hostfs.OpRead},
		Scope:    hostfs.ScopeExactFile,
		Reason:   "profile grant",
	}}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Main([]string{"explain", "--profile", "profile-hostfs-off", "--no-profile-fs", "--", "cat", "/Users/alice/Downloads/profile.txt"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut.String())
	}
	for _, want := range []string{
		"HostFS Portal: roots=/hideout/hostfs,/Users,/Volumes,/private default=hidden profileGrants=0 runGrants=0 totalGrants=0 denyRules=0 write=unsupported",
		"HostFS profile grants: disabled for this run; profile deny rules still apply",
		"HostFS data plane: inactive because no HostFS grants are active",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("explain output missing %q:\n%s", want, out.String())
		}
	}
	loaded, err := store.Load("profile-hostfs-off")
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}
	if len(loaded.HostFS.Grants) != 1 {
		t.Fatalf("profile grant should remain persisted: %+v", loaded.HostFS)
	}
}

func TestExplainShowsRunScopedHostFSDeny(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	p := profile.Default("profile-hostfs-deny")
	p.HostFS.Grants = []hostfs.Rule{{
		ID:       "hfs_profile_allow",
		HostPath: "/Users/alice/Downloads/profile.txt",
		Ops:      []hostfs.Op{hostfs.OpRead},
		Scope:    hostfs.ScopeExactFile,
		Reason:   "profile grant",
	}}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Main([]string{"explain", "--profile", "profile-hostfs-deny", "--no-fs", "read:/Users/alice/Downloads/profile.txt", "--", "cat", "/Users/alice/Downloads/profile.txt"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut.String())
	}
	for _, want := range []string{
		"HostFS Portal: roots=/hideout/hostfs,/Users,/Volumes,/private default=hidden profileGrants=1 runGrants=0 totalGrants=1 denyRules=1 write=unsupported",
		"HostFS run denies: 1 temporary deny rule(s) active",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("explain output missing %q:\n%s", want, out.String())
		}
	}
}

func TestProfileFSManageRules(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}

	var addOut, errOut bytes.Buffer
	code := Main([]string{"profile", "fs", "default", "add", "--fs", "dir:/Users/alice/Public", "--reason", "share public files"}, &addOut, &errOut)
	if code != 0 {
		t.Fatalf("add exit=%d stderr=%s", code, errOut.String())
	}
	var added struct {
		Profile  string       `json:"profile"`
		ID       string       `json:"id"`
		Effect   string       `json:"effect"`
		HostPath string       `json:"hostPath"`
		Ops      []hostfs.Op  `json:"ops"`
		Scope    hostfs.Scope `json:"scope"`
		Reason   string       `json:"reason"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(addOut.Bytes()), &added); err != nil {
		t.Fatalf("decode add output: %v\n%s", err, addOut.String())
	}
	if added.Profile != "default" || !strings.HasPrefix(added.ID, "hfs_") || added.Effect != "allow" ||
		added.HostPath != "/Users/alice/Public" || added.Scope != hostfs.ScopeDir || added.Reason != "share public files" {
		t.Fatalf("unexpected add output: %+v", added)
	}
	loaded, err := store.Load("default")
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}
	if len(loaded.HostFS.Grants) != 1 || loaded.HostFS.Grants[0].ID != added.ID {
		t.Fatalf("profile grant not persisted: %+v", loaded.HostFS)
	}

	var denyOut bytes.Buffer
	code = Main([]string{"profile", "fs", "default", "deny", "--no-fs", "read:/Users/alice/Public/private.txt", "--reason", "keep private file hidden"}, &denyOut, &errOut)
	if code != 0 {
		t.Fatalf("deny exit=%d stderr=%s", code, errOut.String())
	}
	var denied struct {
		ID     string `json:"id"`
		Effect string `json:"effect"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(denyOut.Bytes()), &denied); err != nil {
		t.Fatalf("decode deny output: %v\n%s", err, denyOut.String())
	}
	if denied.Effect != "deny" || !strings.HasPrefix(denied.ID, "hfs_") {
		t.Fatalf("unexpected deny output: %+v", denied)
	}

	var listOut bytes.Buffer
	code = Main([]string{"profile", "fs", "default", "list"}, &listOut, &errOut)
	if code != 0 {
		t.Fatalf("list exit=%d stderr=%s", code, errOut.String())
	}
	var listed struct {
		Profile string `json:"profile"`
		Grants  []struct {
			ID string `json:"id"`
		} `json:"grants"`
		Deny []struct {
			ID string `json:"id"`
		} `json:"deny"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(listOut.Bytes()), &listed); err != nil {
		t.Fatalf("decode list output: %v\n%s", err, listOut.String())
	}
	if listed.Profile != "default" || len(listed.Grants) != 1 || listed.Grants[0].ID != added.ID ||
		len(listed.Deny) != 1 || listed.Deny[0].ID != denied.ID {
		t.Fatalf("unexpected list output: %+v", listed)
	}

	var explainOut bytes.Buffer
	code = Main([]string{"explain", "--profile", "default", "--", "cat", "/Users/alice/Public/readme.txt"}, &explainOut, &errOut)
	if code != 0 {
		t.Fatalf("explain exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(explainOut.String(), "profileGrants=1 runGrants=0 totalGrants=1 denyRules=1") {
		t.Fatalf("explain did not include profile HostFS policy:\n%s", explainOut.String())
	}

	var removeOut bytes.Buffer
	code = Main([]string{"profile", "fs", "default", "remove", added.ID}, &removeOut, &errOut)
	if code != 0 {
		t.Fatalf("remove exit=%d stderr=%s", code, errOut.String())
	}
	var removed struct {
		ID      string `json:"id"`
		Removed bool   `json:"removed"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(removeOut.Bytes()), &removed); err != nil {
		t.Fatalf("decode remove output: %v\n%s", err, removeOut.String())
	}
	if removed.ID != added.ID || !removed.Removed {
		t.Fatalf("unexpected remove output: %+v", removed)
	}
	loaded, err = store.Load("default")
	if err != nil {
		t.Fatalf("reload profile: %v", err)
	}
	if len(loaded.HostFS.Grants) != 0 || len(loaded.HostFS.Deny) != 1 {
		t.Fatalf("profile fs remove changed wrong rules: %+v", loaded.HostFS)
	}
}

func TestProfileFSAddRequiresReason(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{"profile", "fs", "default", "add", "--fs", "read:/Users/alice/file.txt"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected add without reason to fail; stdout=%s", out.String())
	}
	if !strings.Contains(errOut.String(), "--reason is required") {
		t.Fatalf("stderr should explain missing reason:\n%s", errOut.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".hideout", "profiles", "default", "profile.json")); !os.IsNotExist(err) {
		t.Fatalf("invalid profile fs add should not create profile state; err=%v", err)
	}
}

func TestProfileEnvManagePolicy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}

	var out, errOut bytes.Buffer
	code := Main([]string{"profile", "env", "default", "set", "SERVICE_TOKEN=secret-value"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("env set exit=%d stderr=%s", code, errOut.String())
	}
	if strings.Contains(out.String(), "secret-value") {
		t.Fatalf("env set output must not echo value: %s", out.String())
	}
	loaded, err := store.Load("default")
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}
	if loaded.Env.Public["SERVICE_TOKEN"] != "secret-value" {
		t.Fatalf("env public was not persisted: %+v", loaded.Env.Public)
	}

	for _, args := range [][]string{
		{"profile", "env", "default", "inherit", "USER_OPT_IN_ENV"},
		{"profile", "env", "default", "deny", "SSH_*"},
	} {
		out.Reset()
		errOut.Reset()
		code = Main(args, &out, &errOut)
		if code != 0 {
			t.Fatalf("%v exit=%d stderr=%s", args, code, errOut.String())
		}
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"profile", "env", "default", "list"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("env list exit=%d stderr=%s", code, errOut.String())
	}
	if strings.Contains(out.String(), "secret-value") {
		t.Fatalf("env list output must not echo values: %s", out.String())
	}
	var listed struct {
		Profile string   `json:"profile"`
		Public  []string `json:"public"`
		Inherit []string `json:"inherit"`
		Deny    []string `json:"deny"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &listed); err != nil {
		t.Fatalf("decode env list: %v\n%s", err, out.String())
	}
	if listed.Profile != "default" ||
		!slices.Contains(listed.Public, "SERVICE_TOKEN") ||
		!slices.Contains(listed.Inherit, "USER_OPT_IN_ENV") ||
		!slices.Contains(listed.Deny, "SSH_*") {
		t.Fatalf("unexpected env list: %+v", listed)
	}

	for _, args := range [][]string{
		{"profile", "env", "default", "unset", "SERVICE_TOKEN"},
		{"profile", "env", "default", "uninherit", "USER_OPT_IN_ENV"},
		{"profile", "env", "default", "undeny", "SSH_*"},
	} {
		out.Reset()
		errOut.Reset()
		code = Main(args, &out, &errOut)
		if code != 0 {
			t.Fatalf("%v exit=%d stderr=%s", args, code, errOut.String())
		}
	}
	loaded, err = store.Load("default")
	if err != nil {
		t.Fatalf("reload profile: %v", err)
	}
	if _, ok := loaded.Env.Public["SERVICE_TOKEN"]; ok ||
		slices.Contains(loaded.Env.Inherit, "USER_OPT_IN_ENV") ||
		slices.Contains(loaded.Env.Deny, "SSH_*") {
		t.Fatalf("env removals not persisted: %+v", loaded.Env)
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"profile", "env", "default", "set", "HIDEOUT_STORE_ROOT=/tmp/store"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("reserved env set should fail; stdout=%s", out.String())
	}
	if !strings.Contains(errOut.String(), "env.public must not expose hideout runtime env") {
		t.Fatalf("reserved env failure should use profile validation, got %s", errOut.String())
	}
}

func TestProfileCommandProxyManageHostOpenSymbols(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}

	var out, errOut bytes.Buffer
	code := Main([]string{"profile", "command-proxy", "default", "add-open", "browser-open"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("command-proxy add-open exit=%d stderr=%s", code, errOut.String())
	}
	var added struct {
		Profile    string `json:"profile"`
		Command    string `json:"command"`
		Route      string `json:"route"`
		Action     string `json:"action"`
		ArgvSchema string `json:"argvSchema"`
		Added      bool   `json:"added"`
		Updated    bool   `json:"updated"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &added); err != nil {
		t.Fatalf("decode command-proxy add output: %v\n%s", err, out.String())
	}
	if added.Profile != "default" || added.Command != "browser-open" || added.Route != "host-broker" ||
		added.Action != "host.open" || added.ArgvSchema != "open-target-v1" || !added.Added || added.Updated {
		t.Fatalf("unexpected command-proxy add output: %+v", added)
	}
	loaded, err := store.Load("default")
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}
	if loaded.CommandProxy.Commands["browser-open"].Action != "host.open" {
		t.Fatalf("configured command proxy was not persisted: %+v", loaded.CommandProxy.Commands)
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"profile", "command-proxy", "default", "list"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("command-proxy list exit=%d stderr=%s", code, errOut.String())
	}
	var listed struct {
		Profile  string `json:"profile"`
		Commands []struct {
			Name       string `json:"name"`
			Route      string `json:"route"`
			Action     string `json:"action"`
			ArgvSchema string `json:"argvSchema"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &listed); err != nil {
		t.Fatalf("decode command-proxy list output: %v\n%s", err, out.String())
	}
	var names []string
	for _, command := range listed.Commands {
		names = append(names, command.Name)
		if command.Name == "browser-open" &&
			(command.Route != "host-broker" || command.Action != "host.open" || command.ArgvSchema != "open-target-v1") {
			t.Fatalf("custom command proxy has wrong details: %+v", command)
		}
	}
	if listed.Profile != "default" || !slices.Contains(names, "open") || !slices.Contains(names, "xdg-open") || !slices.Contains(names, "browser-open") {
		t.Fatalf("unexpected command-proxy list: %+v", listed)
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"profile", "command-proxy", "default", "remove", "browser-open"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("command-proxy remove exit=%d stderr=%s", code, errOut.String())
	}
	var removed struct {
		Profile string `json:"profile"`
		Command string `json:"command"`
		Removed bool   `json:"removed"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &removed); err != nil {
		t.Fatalf("decode command-proxy remove output: %v\n%s", err, out.String())
	}
	if removed.Profile != "default" || removed.Command != "browser-open" || !removed.Removed {
		t.Fatalf("unexpected command-proxy remove output: %+v", removed)
	}
	loaded, err = store.Load("default")
	if err != nil {
		t.Fatalf("reload profile: %v", err)
	}
	if _, ok := loaded.CommandProxy.Commands["browser-open"]; ok {
		t.Fatalf("command proxy was not removed: %+v", loaded.CommandProxy.Commands)
	}
	if _, ok := loaded.CommandProxy.Commands["open"]; !ok {
		t.Fatalf("required open command proxy was removed: %+v", loaded.CommandProxy.Commands)
	}
}

func TestProfileCommandAdapterManageLocalAdapter(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	adapterDir := filepath.Join(store.ProfileDir("default"), "adapters")
	if err := os.MkdirAll(adapterDir, 0o700); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(adapterDir, "tool.js")
	if err := os.WriteFile(scriptPath, []byte(`function decideCommandAdapter(){return {outcome:"deny",reason:"x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Main([]string{"profile", "command-adapter", "default", "add-local", "--id", "tool", "--path", "adapters/tool.js", "--command", "tool-x"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("command-adapter add-local exit=%d stderr=%s", code, errOut.String())
	}
	var added struct {
		Plan struct {
			AdapterID string   `json:"adapterId"`
			Enabled   bool     `json:"enabled"`
			Commands  []string `json:"commands"`
			Digest    string   `json:"digest"`
			Changed   bool     `json:"changed"`
		} `json:"plan"`
		Applied bool `json:"applied"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &added); err != nil {
		t.Fatalf("decode command-adapter add output: %v\n%s", err, out.String())
	}
	if !added.Applied || added.Plan.AdapterID != "tool" || !added.Plan.Enabled || added.Plan.Digest == "" || !slices.Contains(added.Plan.Commands, "tool-x") {
		t.Fatalf("unexpected command-adapter add output: %+v", added)
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"profile", "command-adapter", "default", "list"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("command-adapter list exit=%d stderr=%s", code, errOut.String())
	}
	var listed struct {
		Profile  string `json:"profile"`
		Adapters []struct {
			ID       string   `json:"id"`
			Enabled  bool     `json:"enabled"`
			Commands []string `json:"commands"`
			Digest   string   `json:"digest"`
		} `json:"adapters"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &listed); err != nil {
		t.Fatalf("decode command-adapter list output: %v\n%s", err, out.String())
	}
	if listed.Profile != "default" || len(listed.Adapters) != 1 || listed.Adapters[0].ID != "tool" || !listed.Adapters[0].Enabled {
		t.Fatalf("unexpected command-adapter list: %+v", listed)
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"profile", "command-adapter", "default", "disable", "tool"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("command-adapter disable exit=%d stderr=%s", code, errOut.String())
	}
	if err := os.WriteFile(scriptPath, []byte(`function decideCommandAdapter(){return {outcome:"deny",reason:"changed"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	code = Main([]string{"profile", "command-adapter", "default", "refresh-digest", "tool"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("command-adapter refresh exit=%d stderr=%s", code, errOut.String())
	}
	loaded, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CommandAdapters.Adapters["tool"].Enabled {
		t.Fatal("refresh-digest should not re-enable disabled adapter")
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"profile", "command-adapter", "default", "enable", "tool"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("command-adapter enable exit=%d stderr=%s", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	code = Main([]string{"profile", "command-adapter", "default", "remove", "tool"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("command-adapter remove exit=%d stderr=%s", code, errOut.String())
	}
	loaded, err = store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.CommandAdapters.Adapters["tool"]; ok {
		t.Fatalf("command adapter was not removed: %+v", loaded.CommandAdapters.Adapters)
	}
}

func TestProfileCommandProxyRejectsInvalidCommandWithoutCreatingProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{"profile", "command-proxy", "default", "add-open", "bad/name"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("invalid command proxy unexpectedly succeeded stdout=%s", out.String())
	}
	if !strings.Contains(errOut.String(), "must be a simple command name") {
		t.Fatalf("invalid command proxy error should come from profile validation, got %s", errOut.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".hideout", "profiles", "default", "profile.json")); !os.IsNotExist(err) {
		t.Fatalf("invalid command-proxy add should not create profile state; err=%v", err)
	}
}

func TestProfileCommandProxyCannotRemoveRequiredOpen(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{"profile", "command-proxy", "default", "remove", "open"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("removing required open unexpectedly succeeded stdout=%s", out.String())
	}
	if !strings.Contains(errOut.String(), "open is required") {
		t.Fatalf("remove open error should explain required command, got %s", errOut.String())
	}
}

func TestBuiltInShimAcceptsConfiguredHostOpenSymbols(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Main([]string{"shim", "browser-open"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("shim without target unexpectedly succeeded stdout=%s", out.String())
	}
	if strings.Contains(errOut.String(), "unsupported command proxy") {
		t.Fatalf("built-in shim should not reject configured symbols through default registry: %s", errOut.String())
	}
	if !strings.Contains(errOut.String(), "open target is required") {
		t.Fatalf("built-in shim should normalize configured symbol as open-target-v1, got %s", errOut.String())
	}
}

func TestProfileHomeImportCopiesUserSelectedIdentityFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	sourceDir := t.TempDir()
	sourceFile := filepath.Join(sourceDir, "state.json")
	if err := os.WriteFile(sourceFile, []byte(`{"token":"secret"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Main([]string{"profile", "home", "default", "import", "--from", sourceFile, "--to", ".tool/state.json"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("home import exit=%d stderr=%s", code, errOut.String())
	}
	if strings.Contains(out.String(), sourceFile) || strings.Contains(out.String(), "secret") {
		t.Fatalf("home import output must not leak source path or content: %s", out.String())
	}
	var imported struct {
		Profile string `json:"profile"`
		Kind    string `json:"kind"`
		Dest    string `json:"dest"`
		Files   int    `json:"files"`
		Bytes   int64  `json:"bytes"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &imported); err != nil {
		t.Fatalf("decode home import: %v\n%s", err, out.String())
	}
	if imported.Profile != "default" || imported.Kind != "profile.home.import" ||
		imported.Dest != ".tool/state.json" || imported.Files != 1 || imported.Bytes == 0 {
		t.Fatalf("unexpected home import output: %+v", imported)
	}
	dest := filepath.Join(store.ProfileDir("default"), "home", ".tool", "state.json")
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read imported file: %v", err)
	}
	if string(data) != `{"token":"secret"}` {
		t.Fatalf("imported file content mismatch: %s", data)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("imported public credential-like file should be clamped to 0600, got %s", info.Mode().Perm())
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"profile", "home", "default", "import", "--from", sourceFile, "--to", ".tool/state.json"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("home import without --force should reject existing destination")
	}

	if err := os.WriteFile(sourceFile, []byte(`{"token":"new"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	code = Main([]string{"profile", "home", "default", "import", "--from", sourceFile, "--to", ".tool/state.json", "--force"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("home import --force exit=%d stderr=%s", code, errOut.String())
	}
	data, err = os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read forced import: %v", err)
	}
	if string(data) != `{"token":"new"}` {
		t.Fatalf("forced import did not replace file: %s", data)
	}
}

func TestProfileHomeImportCopiesDirectoriesAndRejectsUnsafeSources(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	sourceDir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(filepath.Join(sourceDir, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "nested", "token"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Main([]string{"profile", "home", "default", "import", "--from", sourceDir, "--to", ".state"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("directory home import exit=%d stderr=%s", code, errOut.String())
	}
	if _, err := os.Stat(filepath.Join(store.ProfileDir("default"), "home", ".state", "nested", "token")); err != nil {
		t.Fatalf("imported directory missing nested file: %v", err)
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"profile", "home", "default", "import", "--from", sourceDir, "--to", "../outside"}, &out, &errOut)
	if code == 0 || !strings.Contains(errOut.String(), "inside profile home") {
		t.Fatalf("escaping destination should fail, exit=%d stderr=%s", code, errOut.String())
	}
	if _, err := os.Stat(filepath.Join(store.ProfileDir("default"), "outside")); !os.IsNotExist(err) {
		t.Fatalf("escaping import should not create outside path, err=%v", err)
	}

	linkPath := filepath.Join(t.TempDir(), "linked-token")
	if err := os.Symlink(filepath.Join(sourceDir, "nested", "token"), linkPath); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	code = Main([]string{"profile", "home", "default", "import", "--from", linkPath, "--to", ".state/link"}, &out, &errOut)
	if code == 0 || !strings.Contains(errOut.String(), "must not be a symlink") {
		t.Fatalf("symlink source should fail, exit=%d stderr=%s", code, errOut.String())
	}
	if strings.Contains(errOut.String(), linkPath) {
		t.Fatalf("symlink failure must not leak full source path: %s", errOut.String())
	}
	if _, err := os.Stat(filepath.Join(store.ProfileDir("default"), "home", ".state", "link")); !os.IsNotExist(err) {
		t.Fatalf("symlink import should not create destination, err=%v", err)
	}
}

func TestProfileHomeImportRejectsSymlinkDestinationParents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	sourceFile := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(sourceFile, []byte(`{"token":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	escapeDir := t.TempDir()
	profileHome := filepath.Join(store.ProfileDir("default"), "home")
	if err := os.MkdirAll(profileHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(escapeDir, filepath.Join(profileHome, ".tool")); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Main([]string{"profile", "home", "default", "import", "--from", sourceFile, "--to", ".tool/state.json", "--force"}, &out, &errOut)
	if code == 0 || !strings.Contains(errOut.String(), "destination must not use a symlink") {
		t.Fatalf("symlink destination parent should fail, exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if _, err := os.Stat(filepath.Join(escapeDir, "state.json")); !os.IsNotExist(err) {
		t.Fatalf("import escaped through destination symlink parent, err=%v", err)
	}
}

func TestProfileHomeImportForceReplacesSymlinkDestinationWithoutFollowing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	sourceFile := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(sourceFile, []byte(`{"token":"new"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	escapeDir := t.TempDir()
	escapeFile := filepath.Join(escapeDir, "state.json")
	if err := os.WriteFile(escapeFile, []byte(`{"token":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	profileHome := filepath.Join(store.ProfileDir("default"), "home")
	if err := os.MkdirAll(filepath.Join(profileHome, ".tool"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(escapeFile, filepath.Join(profileHome, ".tool", "state.json")); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Main([]string{"profile", "home", "default", "import", "--from", sourceFile, "--to", ".tool/state.json", "--force"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("force import over symlink exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if got, err := os.ReadFile(escapeFile); err != nil || string(got) != `{"token":"old"}` {
		t.Fatalf("force import followed destination symlink, content=%q err=%v", got, err)
	}
	dest := filepath.Join(profileHome, ".tool", "state.json")
	info, err := os.Lstat(dest)
	if err != nil {
		t.Fatalf("destination missing: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("destination should be a regular imported file, got symlink")
	}
	if got, err := os.ReadFile(dest); err != nil || string(got) != `{"token":"new"}` {
		t.Fatalf("destination content mismatch content=%q err=%v", got, err)
	}
}

func TestProfileHomeImportAllowsManagedXDGSymlinkDestinations(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	if err := store.Save(profile.Default("default")); err != nil {
		t.Fatalf("save default profile: %v", err)
	}
	sourceFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(sourceFile, []byte("seeded-token"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Main([]string{"profile", "home", "default", "import", "--from", sourceFile, "--to", ".config/hideout-test-cli/token", "--force"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("managed xdg import exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if got, err := os.ReadFile(filepath.Join(store.ProfileDir("default"), "config", "hideout-test-cli", "token")); err != nil || string(got) != "seeded-token" {
		t.Fatalf("managed xdg import wrote wrong target content=%q err=%v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(store.ProfileDir("default"), "home", ".config")); err != nil {
		t.Fatalf("managed .config symlink missing: %v", err)
	}
}

func TestProfileToolsManageExpectedCommandsAndRejectLegacy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}

	var out, errOut bytes.Buffer
	code := Main([]string{"profile", "tools", "default", "expected", "add", "agent-cli"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected add exit=%d stderr=%s", code, errOut.String())
	}
	loaded, err := store.Load("default")
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}
	if !slices.Contains(loaded.Tools.ExpectedCommands, "agent-cli") {
		t.Fatalf("expected command was not persisted: %+v", loaded.Tools.ExpectedCommands)
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"profile", "tools", "default", "list"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("tools list exit=%d stderr=%s", code, errOut.String())
	}
	var listed struct {
		Profile          string   `json:"profile"`
		ExpectedCommands []string `json:"expectedCommands"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &listed); err != nil {
		t.Fatalf("decode tools list: %v\n%s", err, out.String())
	}
	if listed.Profile != "default" ||
		!slices.Contains(listed.ExpectedCommands, "agent-cli") {
		t.Fatalf("unexpected tools list: %+v", listed)
	}

	for _, args := range [][]string{
		{"profile", "tools", "default", "expected", "remove", "agent-cli"},
	} {
		out.Reset()
		errOut.Reset()
		code = Main(args, &out, &errOut)
		if code != 0 {
			t.Fatalf("%v exit=%d stderr=%s", args, code, errOut.String())
		}
	}
	loaded, err = store.Load("default")
	if err != nil {
		t.Fatalf("reload after removals: %v", err)
	}
	if slices.Contains(loaded.Tools.ExpectedCommands, "agent-cli") {
		t.Fatalf("tool removals were not persisted: %+v", loaded.Tools)
	}

	for _, args := range [][]string{
		{"profile", "tools", "default", "preset", "add", "node-dev"},
		{"profile", "tools", "default", "npm", "add", "--package", "@example/test-cli@1.2.3", "--command", "test-cli"},
	} {
		out.Reset()
		errOut.Reset()
		code = Main(args, &out, &errOut)
		if code == 0 || !strings.Contains(errOut.String(), "legacy tool-supply") {
			t.Fatalf("%v should fail with legacy diagnostic, exit=%d stderr=%s", args, code, errOut.String())
		}
	}
}

func TestRunRejectsInvalidHostFSGrantBeforeStateCreation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{"run", "--profile", "bad-grant", "--fs", "write:/tmp/file.txt", "--", "echo", "hi"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected invalid grant to fail; stdout=%s stderr=%s", out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "unsupported --fs kind") {
		t.Fatalf("stderr should explain invalid grant:\n%s", errOut.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".hideout", "profiles", "bad-grant", "profile.json")); !os.IsNotExist(err) {
		t.Fatalf("invalid grant should not create profile state; err=%v", err)
	}
}

func TestParseHostFSRuleFlagDetectsGlobSelectors(t *testing.T) {
	readRule, err := parseHostFSRuleFlag(hostFSFlagInput{
		flagName: "--fs",
		value:    "read:/Users/alice/Downloads/*.txt",
		reason:   "text files",
	})
	if err != nil {
		t.Fatal(err)
	}
	if readRule.Scope != hostfs.ScopeGlob || !reflect.DeepEqual(readRule.Ops, []hostfs.Op{hostfs.OpRead}) {
		t.Fatalf("read glob rule mismatch: %+v", readRule)
	}
	statRule, err := parseHostFSRuleFlag(hostFSFlagInput{
		flagName: "--fs",
		value:    "stat:/Users/alice/Downloads/report-?.md",
		reason:   "reports",
	})
	if err != nil {
		t.Fatal(err)
	}
	if statRule.Scope != hostfs.ScopeGlob || !reflect.DeepEqual(statRule.Ops, []hostfs.Op{hostfs.OpStat}) {
		t.Fatalf("stat glob rule mismatch: %+v", statRule)
	}
	exactRule, err := parseHostFSRuleFlag(hostFSFlagInput{
		flagName: "--fs",
		value:    "read:/Users/alice/Downloads/report.txt",
		reason:   "one file",
	})
	if err != nil {
		t.Fatal(err)
	}
	if exactRule.Scope != hostfs.ScopeExactFile {
		t.Fatalf("exact rule scope=%s", exactRule.Scope)
	}
	if _, err := parseHostFSRuleFlag(hostFSFlagInput{
		flagName: "--fs",
		value:    "dir:/Users/alice/Downloads/*.txt",
		reason:   "bad directory glob",
	}); err == nil || !strings.Contains(err.Error(), "does not support glob path selectors") {
		t.Fatalf("expected dir glob rejection, got %v", err)
	}
}

func TestHostFSGraftsDeriveCompatibilityDirectories(t *testing.T) {
	policy, err := hostfs.Build(hostfs.BuildInput{Run: hostfs.Config{Grants: []hostfs.Rule{
		{
			HostPath: "/Users/alice/Downloads/file.txt",
			Ops:      []hostfs.Op{hostfs.OpRead},
			Scope:    hostfs.ScopeExactFile,
			Reason:   "file",
		},
		{
			HostPath: "/Volumes/Data/public",
			Ops:      []hostfs.Op{hostfs.OpRead, hostfs.OpList},
			Scope:    hostfs.ScopeDir,
			Reason:   "dir",
		},
		{
			HostPath: "/Users/alice/Downloads/*.txt",
			Ops:      []hostfs.Op{hostfs.OpRead},
			Scope:    hostfs.ScopeGlob,
			Reason:   "glob",
		},
		{
			HostPath: "/tmp/not-compatible",
			Ops:      []hostfs.Op{hostfs.OpRead, hostfs.OpList},
			Scope:    hostfs.ScopeDir,
			Reason:   "tmp",
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	got := hostFSGrafts(policy)
	want := []string{"/Users/alice/Downloads", "/Volumes/Data/public"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hostFSGrafts=%v want %v", got, want)
	}
}

func TestExplainAndRunUseConfiguredIdentityEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	p := profile.Default("identity-env")
	p.Identity.User = "alice"
	p.Identity.Hostname = "quietbox"
	p.Identity.Timezone = "Asia/Shanghai"
	p.Identity.Locale = "zh_CN.UTF-8"
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}

	var explainOut, explainErr bytes.Buffer
	code := Main([]string{"explain", "--profile", "identity-env", "--backend", "native", "--", "env"}, &explainOut, &explainErr)
	if code != 0 {
		t.Fatalf("explain exit=%d stderr=%s stdout=%s", code, explainErr.String(), explainOut.String())
	}
	if !strings.Contains(explainOut.String(), "Identity env: user=alice hostname=quietbox timezone=Asia/Shanghai locale=zh_CN.UTF-8") {
		t.Fatalf("explain missing configured identity env:\n%s", explainOut.String())
	}

	var out, errOut bytes.Buffer
	code = Main([]string{
		"run",
		"--profile", "identity-env",
		"--backend", "native",
		"--allow-weak-isolation",
		"--",
		"sh", "-c", `printf 'user=%s\nhost=%s\ntz=%s\nlang=%s\nlc_all=%s\n' "$USER" "$HOSTNAME" "$TZ" "$LANG" "$LC_ALL"`,
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("run exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	for _, want := range []string{
		"user=alice",
		"host=quietbox",
		"tz=Asia/Shanghai",
		"lang=zh_CN.UTF-8",
		"lc_all=zh_CN.UTF-8",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("target env missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunExplainFlagPrintsBoundaryWithoutExecuting(t *testing.T) {
	home := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{
		"run",
		"--backend", "native",
		"--allow-weak-isolation",
		"--explain",
		"--",
		"sh", "-c", "touch " + marker,
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Target command: sh -c touch "+marker) {
		t.Fatalf("run --explain missing target command:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Known limitation") {
		t.Fatalf("run --explain missing boundary details:\n%s", out.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("run --explain should not execute target; marker err=%v", err)
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 0 {
		t.Fatalf("run --explain should not create audit files, got %v", auditFiles)
	}
}

func TestRunSuppressesControlSummaryUnlessVerbose(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var out, errOut bytes.Buffer
	code := Main([]string{
		"run",
		"--backend", "native",
		"--allow-weak-isolation",
		"--",
		"sh", "-c", "printf target-output",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("default run exit=%d stderr=%s", code, errOut.String())
	}
	if got := out.String(); got != "target-output" {
		t.Fatalf("default run stdout=%q", got)
	}
	if strings.Contains(errOut.String(), "Hideout boundary:") ||
		strings.Contains(errOut.String(), "Hideout environment:") ||
		strings.Contains(errOut.String(), "resume: hideout run --resume") {
		t.Fatalf("default run should not print control summary:\n%s", errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{
		"run",
		"--backend", "native",
		"--allow-weak-isolation",
		"--verbose",
		"--",
		"sh", "-c", "printf target-output",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("verbose run exit=%d stderr=%s", code, errOut.String())
	}
	if got := out.String(); got != "target-output" {
		t.Fatalf("verbose run stdout=%q", got)
	}
	if !strings.Contains(errOut.String(), "Hideout boundary:") {
		t.Fatalf("verbose run should print boundary summary:\n%s", errOut.String())
	}
	if !strings.Contains(errOut.String(), "privilege: status=unknown") ||
		!strings.Contains(errOut.String(), "does not claim guest-root containment") {
		t.Fatalf("verbose run should print privilege non-claim:\n%s", errOut.String())
	}
}

func TestAppLimaBackendSuppressesControlOutputUnlessVerbose(t *testing.T) {
	var out, errOut bytes.Buffer
	a := app{stdout: &out, stderr: &errOut}

	be := a.backend("lima", runOptions{})
	limaBackend, ok := be.(lima.Backend)
	if !ok {
		t.Fatalf("backend type=%T want lima.Backend", be)
	}
	if limaBackend.ControlStdout == nil || limaBackend.ControlStderr == nil {
		t.Fatalf("lima control writers must be explicit")
	}
	_, _ = fmt.Fprint(limaBackend.ControlStdout, "control stdout")
	_, _ = fmt.Fprint(limaBackend.ControlStderr, "control stderr")
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Fatalf("default lima control output should be discarded stdout=%q stderr=%q", out.String(), errOut.String())
	}
	if limaBackend.ControlStdout != io.Discard || limaBackend.ControlStderr != io.Discard {
		t.Fatalf("default lima control writers should be io.Discard")
	}

	verbose := a.backend("lima", runOptions{verbose: true})
	verboseLima, ok := verbose.(lima.Backend)
	if !ok {
		t.Fatalf("verbose backend type=%T want lima.Backend", verbose)
	}
	_, _ = fmt.Fprint(verboseLima.ControlStdout, "control stdout")
	_, _ = fmt.Fprint(verboseLima.ControlStderr, "control stderr")
	if out.String() != "control stdout" || errOut.String() != "control stderr" {
		t.Fatalf("verbose lima control output should reach CLI writers stdout=%q stderr=%q", out.String(), errOut.String())
	}
}

func TestAppEnvironmentOperatorSuppressesControlOutputUnlessVerbose(t *testing.T) {
	var out, errOut bytes.Buffer
	a := app{stdout: &out, stderr: &errOut}

	operator := a.environmentOperator(false)
	limaOperator, ok := operator.(lima.Backend)
	if !ok {
		t.Fatalf("operator type=%T want lima.Backend", operator)
	}
	if limaOperator.ControlStdout == nil || limaOperator.ControlStderr == nil {
		t.Fatalf("lima environment control writers must be explicit")
	}
	_, _ = fmt.Fprint(limaOperator.ControlStdout, "control stdout")
	_, _ = fmt.Fprint(limaOperator.ControlStderr, "control stderr")
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Fatalf("default lifecycle control output should be discarded stdout=%q stderr=%q", out.String(), errOut.String())
	}
	if limaOperator.ControlStdout != io.Discard || limaOperator.ControlStderr != io.Discard {
		t.Fatalf("default lifecycle control writers should be io.Discard")
	}

	verbose := a.environmentOperator(true)
	verboseLima, ok := verbose.(lima.Backend)
	if !ok {
		t.Fatalf("verbose operator type=%T want lima.Backend", verbose)
	}
	_, _ = fmt.Fprint(verboseLima.ControlStdout, "control stdout")
	_, _ = fmt.Fprint(verboseLima.ControlStderr, "control stderr")
	if out.String() != "control stdout" || errOut.String() != "control stderr" {
		t.Fatalf("verbose lifecycle control output should reach CLI writers stdout=%q stderr=%q", out.String(), errOut.String())
	}
}

func TestEnvironmentLifecycleCommandsSuppressBackendControlOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fakeBin := t.TempDir()
	fakeLimactl := filepath.Join(fakeBin, "limactl")
	if err := os.WriteFile(fakeLimactl, []byte("#!/bin/sh\necho raw-control-stdout\necho raw-control-stderr >&2\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake limactl: %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	store, err := profile.DefaultStore()
	if err != nil {
		t.Fatalf("default store: %v", err)
	}
	envStore := environment.Store{Root: store.Root}
	rec, err := envStore.Create(environment.Spec{
		Name:           "fixture-env-101",
		ImageRef:       environment.BuiltinBaseImage,
		Profile:        "default",
		Backend:        "lima",
		Workspace:      "/work",
		GuestWorkspace: "/workspace",
		InstanceName:   "hideout-fake",
	})
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	rec.Status = "ready"
	if err := envStore.Save(rec); err != nil {
		t.Fatalf("save environment: %v", err)
	}

	var out, errOut bytes.Buffer
	code := Main([]string{"stop", rec.ID}, &out, &errOut)
	if code != 0 {
		t.Fatalf("stop exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	for _, forbidden := range []string{"raw-control-stdout", "raw-control-stderr"} {
		if strings.Contains(out.String(), forbidden) || strings.Contains(errOut.String(), forbidden) {
			t.Fatalf("stop leaked backend control output %q stdout=%q stderr=%q", forbidden, out.String(), errOut.String())
		}
	}
	if !strings.Contains(out.String(), "stopped: "+rec.ID) {
		t.Fatalf("stop output missing Hideout result:\n%s", out.String())
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"clean", "--stopped", rec.ID}, &out, &errOut)
	if code != 0 {
		t.Fatalf("clean exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	for _, forbidden := range []string{"raw-control-stdout", "raw-control-stderr"} {
		if strings.Contains(out.String(), forbidden) || strings.Contains(errOut.String(), forbidden) {
			t.Fatalf("clean leaked backend control output %q stdout=%q stderr=%q", forbidden, out.String(), errOut.String())
		}
	}
	if !strings.Contains(out.String(), "removed: "+rec.ID) {
		t.Fatalf("clean output missing Hideout result:\n%s", out.String())
	}
}

func TestWriteRunResultSummaryPrintsReusableEnvironmentResumeHint(t *testing.T) {
	var out, errOut bytes.Buffer
	a := app{stdout: &out, stderr: &errOut}
	a.writeRunResultSummary(manager.RunResult{
		EnvironmentID:    "env_20260703t010203zabcdef1234567890",
		EnvironmentName:  "work",
		PreserveInstance: true,
		AuditPath:        "/tmp/hideout/audit.jsonl",
		BoundarySummary: &manager.BoundarySummary{
			Version:   manager.BoundarySummaryVersion,
			Evidence:  "available",
			AuditPath: "/tmp/hideout/audit.jsonl",
			Capabilities: []manager.BoundaryCapabilitySummary{
				{Capability: "host.open", Allowed: 1, Denied: 2},
				{Capability: "hostfs", Allowed: 3, Denied: 4, Unsupported: 5},
				{Capability: "portbridge.host-to-guest", Allowed: 1, Owner: "preview.open", Source: "manual", Lifetime: "run", CloseReason: "session-end", EndpointCategory: "host-loopback"},
			},
		},
	})
	if out.Len() != 0 {
		t.Fatalf("run result summary should not write stdout: %s", out.String())
	}
	for _, want := range []string{
		"Hideout environment: env_20260703t010203zabcdef1234567890",
		"run again: hideout run --env work -- <command>",
		"stop: hideout stop work",
		"clean-after-stop: hideout clean --stopped work",
		"Hideout boundary:",
		"  audit: /tmp/hideout/audit.jsonl",
		"  host.open: allowed=1 denied=2",
		"  hostfs: allowed=3 denied=4 unsupported=5",
		"  portbridge.host-to-guest: allowed=1 denied=0 owner=preview.open source=manual lifetime=run close=session-end endpoint=host-loopback",
	} {
		if !strings.Contains(errOut.String(), want) {
			t.Fatalf("resume summary missing %q:\n%s", want, errOut.String())
		}
	}
	for _, leaked := range []string{"cap_secret", "127.0.0.1:49152", "/Users/alice/private.txt"} {
		if strings.Contains(errOut.String(), leaked) {
			t.Fatalf("run result summary leaked %q:\n%s", leaked, errOut.String())
		}
	}

	errOut.Reset()
	a.writeRunResultSummary(manager.RunResult{
		EnvironmentID:    "env_20260703t010203zabcdef1234567890",
		PreserveInstance: false,
	})
	a.writeRunResultSummary(manager.RunResult{})
	if errOut.Len() != 0 {
		t.Fatalf("non-preserved or absent environment should not print summary: %s", errOut.String())
	}

	a.writeRunResultSummary(manager.RunResult{
		BoundarySummary: &manager.BoundarySummary{
			Version:   manager.BoundarySummaryVersion,
			Evidence:  "disabled",
			AuditPath: "off",
			Capabilities: []manager.BoundaryCapabilitySummary{
				{Capability: "host.open"},
				{Capability: "hostfs"},
			},
		},
	})
	if !strings.Contains(errOut.String(), "Hideout boundary:") ||
		!strings.Contains(errOut.String(), "  audit: disabled - no boundary evidence") ||
		strings.Contains(errOut.String(), "hostfs: allowed=0 denied=0") {
		t.Fatalf("boundary summary without reusable environment missing:\n%s", errOut.String())
	}
}

func TestBuildPreviewOpenOptionsSupportsManualAndProfileCandidates(t *testing.T) {
	p := profile.Default("preview-test")
	p.EndpointExposure.HostToGuest = []profile.EndpointCandidate{{
		ID:            "dev",
		Owner:         manager.OpenTargetPreviewOpen,
		Proto:         "tcp",
		TargetAddress: "127.0.0.1:5173",
	}}
	owners, candidates, exposures, err := buildPreviewOpenOptions(p, []string{"dev", "http://localhost:3000/app"})
	if err != nil {
		t.Fatalf("buildPreviewOpenOptions: %v", err)
	}
	if len(owners) != 1 || owners[0].ID != manager.OpenTargetPreviewOpen {
		t.Fatalf("owners mismatch: %+v", owners)
	}
	if len(candidates) != 1 ||
		candidates[0].Source != manager.EndpointSourceManual ||
		candidates[0].Owner != manager.OpenTargetPreviewOpen ||
		candidates[0].TargetAddress != "127.0.0.1:3000" {
		t.Fatalf("manual candidate mismatch: %+v", candidates)
	}
	if len(exposures) != 2 ||
		exposures[0].CandidateID != "dev" ||
		exposures[1].CandidateID != "manual_preview_2" ||
		exposures[0].Owner != manager.OpenTargetPreviewOpen ||
		exposures[1].Owner != manager.OpenTargetPreviewOpen {
		t.Fatalf("exposures mismatch: %+v", exposures)
	}
}

func TestExplainRequiresTargetCommandBeforeStateCreation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{"explain", "--profile", "missing-command"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected explain without command to fail; stdout=%s stderr=%s", out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "command is required after --") {
		t.Fatalf("stderr should explain missing command:\n%s", errOut.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".hideout", "profiles", "missing-command", "profile.json")); !os.IsNotExist(err) {
		t.Fatalf("missing-command explain should not create profile state; err=%v", err)
	}
	sessionDirs, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*"))
	if err != nil {
		t.Fatalf("glob sessions: %v", err)
	}
	if len(sessionDirs) != 0 {
		t.Fatalf("missing-command explain should not create sessions, got %v", sessionDirs)
	}
}

func TestRunRequiresTargetCommandBeforeStateCreation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{"run", "--profile", "missing-command"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected run without command to fail; stdout=%s stderr=%s", out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "command is required after --") {
		t.Fatalf("stderr should explain missing command:\n%s", errOut.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".hideout", "profiles", "missing-command", "profile.json")); !os.IsNotExist(err) {
		t.Fatalf("missing-command run should not create profile state; err=%v", err)
	}
	sessionDirs, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*"))
	if err != nil {
		t.Fatalf("glob sessions: %v", err)
	}
	if len(sessionDirs) != 0 {
		t.Fatalf("missing-command run should not create sessions, got %v", sessionDirs)
	}
}

func TestLabRequiresExplicitEnablement(t *testing.T) {
	for _, args := range [][]string{
		{"lab", "portbridge", "loopback", "--target", "127.0.0.1:1"},
		{"lab", "portbridge", "guest-to-host", "--target", "127.0.0.1:1"},
		{"lab", "portbridge", "host-to-guest", "--guest-target", "127.0.0.1:1"},
		{"lab", "browser-control", "--profile", "test"},
		{"lab", "preview-open", "--guest-url", "http://127.0.0.1:3000"},
	} {
		t.Run(strings.Join(args[1:], " "), func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := Main(args, &out, &errOut)
			if code == 0 {
				t.Fatalf("expected lab command to require explicit enablement; stdout=%s stderr=%s", out.String(), errOut.String())
			}
			if !strings.Contains(errOut.String(), "requires --enable-lab") {
				t.Fatalf("stderr should explain lab enablement:\n%s", errOut.String())
			}
		})
	}
}

func TestLabPortbridgeLoopbackProbe(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	target, closeTarget := startLabEchoServer(t)
	defer closeTarget()
	var out, errOut bytes.Buffer
	code := Main([]string{
		"lab",
		"portbridge",
		"loopback",
		"--enable-lab",
		"--target",
		target,
		"--send",
		"hello\n",
		"--expect",
		"echo:hello\n",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	for _, want := range []string{
		"Hideout lab: experimental evidence only",
		"capability=portbridge.probe",
		"route=lab-probe",
		"mode=loopback",
		"listen=127.0.0.1:",
		"target=" + target,
		"probe=tcp-forward ok",
		`received="echo:hello\n"`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("lab output missing %q:\n%s", want, out.String())
		}
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 1 {
		t.Fatalf("expected one lab audit file, got %d: %v", len(auditFiles), auditFiles)
	}
	validateAuditJSONLWithSchema(t, auditFiles[0])
	event := lastAuditEventByActionForAppTest(t, auditFiles[0], "portbridge.probe")
	if event.Decision != "allow" || event.Details["subject"] != "lab:portbridge" || event.Details["route"] != "lab-probe" || event.Details["status"] != "ok" {
		t.Fatalf("unexpected lab audit event: %+v", event)
	}
	if event.Details["sendBytes"] != float64(len("hello\n")) || event.Details["receivedBytes"] != float64(len("echo:hello\n")) {
		t.Fatalf("lab audit event should record byte counts, not payloads: %+v", event)
	}
	if strings.Contains(fmt.Sprint(event.Details), "hello") {
		t.Fatalf("lab audit event leaked probe payload: %+v", event)
	}
}

func TestLabPortbridgeDeniesControlPlaneTargetAndStripsAudit(t *testing.T) {
	// Deterministic contract: a lab target that embeds Hideout-minted
	// control-plane material (a cap_ token here) is denied by the lab validator,
	// and the denial audit strips the token. A user-data query value would be
	// accepted and recorded verbatim (see TestLabPortbridgeAllowsUserDataTarget).
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{
		"lab",
		"portbridge",
		"loopback",
		"--enable-lab",
		"--target",
		"127.0.0.1:1?ref=cap_0123456789abcdef0123456789abcdef",
	}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected lab validator denial; stdout=%s stderr=%s", out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "must not reference Hideout control-plane credentials") {
		t.Fatalf("stderr should explain lab validator denial:\n%s", errOut.String())
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 1 {
		t.Fatalf("expected one lab audit file, got %d: %v", len(auditFiles), auditFiles)
	}
	validateAuditJSONLWithSchema(t, auditFiles[0])
	data, err := os.ReadFile(auditFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "cap_0123456789abcdef0123456789abcdef") {
		t.Fatalf("lab audit leaked control-plane token: %s", data)
	}
	for _, want := range []string{`"action":"portbridge.probe"`, `"decision":"error"`, `"target":"127.0.0.1:1?ref=REDACTED"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("lab audit missing %q: %s", want, data)
		}
	}
}

func TestLabPortbridgeAllowsUserDataTarget(t *testing.T) {
	// A user-data query value that heuristics used to flag (token=) is not
	// control-plane material; the lab validator accepts it and the audit records
	// it verbatim as host-local evidence.
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	Main([]string{
		"lab",
		"portbridge",
		"loopback",
		"--enable-lab",
		"--target",
		"127.0.0.1:1?token=abc123",
	}, &out, &errOut)
	if strings.Contains(errOut.String(), "control-plane") {
		t.Fatalf("user-data target must not be denied as control-plane:\n%s", errOut.String())
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 1 {
		t.Fatalf("expected one lab audit file, got %d: %v", len(auditFiles), auditFiles)
	}
	data, err := os.ReadFile(auditFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"target":"127.0.0.1:1?token=abc123"`) {
		t.Fatalf("lab audit should record user-data target verbatim: %s", data)
	}
}

func TestLabPortbridgeDirectionsProbe(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mode       string
		targetFlag string
	}{
		{
			name:       "guest to host",
			mode:       "guest-to-host",
			targetFlag: "target",
		},
		{
			name:       "host to guest",
			mode:       "host-to-guest",
			targetFlag: "guest-target",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			target, closeTarget := startLabEchoServer(t)
			defer closeTarget()
			var out, errOut bytes.Buffer
			code := Main([]string{
				"lab",
				"portbridge",
				tc.mode,
				"--enable-lab",
				"--" + tc.targetFlag,
				target,
				"--send",
				"hello\n",
				"--expect",
				"echo:hello\n",
				"--timeout",
				"2s",
			}, &out, &errOut)
			if code != 0 {
				t.Fatalf("exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
			}
			for _, want := range []string{
				"Hideout lab: experimental evidence only",
				"capability=portbridge.probe",
				"route=lab-probe",
				"mode=" + tc.mode,
				"listen=127.0.0.1:",
				tc.targetFlag + "=" + target,
				"probe=tcp-forward ok",
				`received="echo:hello\n"`,
			} {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("lab output missing %q:\n%s", want, out.String())
				}
			}
			auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
			if err != nil {
				t.Fatalf("glob audit files: %v", err)
			}
			if len(auditFiles) != 1 {
				t.Fatalf("expected one lab audit file, got %d: %v", len(auditFiles), auditFiles)
			}
			validateAuditJSONLWithSchema(t, auditFiles[0])
			event := lastAuditEventByActionForAppTest(t, auditFiles[0], "portbridge.probe")
			if event.Decision != "allow" ||
				event.Details["probe"] != "portbridge."+tc.mode ||
				event.Details["subject"] != "lab:portbridge" ||
				event.Details["route"] != "lab-probe" ||
				event.Details["mode"] != tc.mode ||
				event.Details["status"] != "ok" {
				t.Fatalf("unexpected lab audit event: %+v", event)
			}
			if event.Details[tc.targetFlag] != target || event.Details["targetField"] != tc.targetFlag {
				t.Fatalf("lab audit should record redacted target under %s: %+v", tc.targetFlag, event)
			}
			if event.Details["sendBytes"] != float64(len("hello\n")) || event.Details["receivedBytes"] != float64(len("echo:hello\n")) {
				t.Fatalf("lab audit event should record byte counts, not payloads: %+v", event)
			}
			if event.Details["timeoutMs"] != float64(2000) {
				t.Fatalf("lab audit should record timeout in milliseconds: %+v", event)
			}
			if strings.Contains(fmt.Sprint(event.Details), "hello") {
				t.Fatalf("lab audit event leaked probe payload: %+v", event)
			}
		})
	}
}

func TestLabBrowserControlProbeWritesAudit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	devtools := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			t.Errorf("unexpected browser control path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Browser":"FakeChrome/1.0","webSocketDebuggerUrl":"ws://127.0.0.1/devtools/browser/fake-secret"}`))
	}))
	defer devtools.Close()
	devtoolsURL, err := url.Parse(devtools.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, devtoolsPort, err := net.SplitHostPort(devtoolsURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	fakeBrowser := filepath.Join(t.TempDir(), "fake-browser")
	if err := os.WriteFile(fakeBrowser, []byte(fmt.Sprintf(`#!/bin/sh
profile=""
for arg in "$@"; do
  case "$arg" in
    --user-data-dir=*) profile=${arg#--user-data-dir=} ;;
  esac
done
[ -n "$profile" ] || exit 2
i=0
while [ "$i" -lt 100 ]; do
  /bin/mkdir -p "$profile" || exit 3
  printf '%%s\n/devtools/browser/fake\n' %q > "$profile/DevToolsActivePort" || exit 4
  i=$((i + 1))
  /bin/sleep 0.1
done
`, devtoolsPort)), 0o700); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{
		"lab",
		"browser-control",
		"--enable-lab",
		"--profile",
		"test",
		"--browser-path",
		fakeBrowser,
		"--timeout",
		"10s",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	for _, want := range []string{
		"Hideout lab: experimental evidence only",
		"capability=browser.control.probe",
		"route=lab-probe",
		"mode=browser-control",
		"profile=test",
		"browser-profile=present",
		"control-url=http://127.0.0.1:" + devtoolsPort + "/json/version",
		"browser=FakeChrome/1.0",
		"probe=devtools-version ok",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("lab output missing %q:\n%s", want, out.String())
		}
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 1 {
		t.Fatalf("expected one lab audit file, got %d: %v", len(auditFiles), auditFiles)
	}
	validateAuditJSONLWithSchema(t, auditFiles[0])
	event := lastAuditEventByActionForAppTest(t, auditFiles[0], "browser.control.probe")
	if event.Decision != "allow" ||
		event.Details["probe"] != "browser-control" ||
		event.Details["subject"] != "lab:browser" ||
		event.Details["route"] != "lab-probe" ||
		event.Details["profile"] != "test" ||
		event.Details["browser"] != "FakeChrome/1.0" ||
		event.Details["browserProfile"] != "present" ||
		event.Details["webSocketDebuggerURLPresent"] != true ||
		event.Details["status"] != "ok" {
		t.Fatalf("unexpected lab audit event: %+v", event)
	}
	if event.Details["controlURL"] != "http://127.0.0.1:"+devtoolsPort+"/json/version" {
		t.Fatalf("browser control audit should record loopback control URL: %+v", event)
	}
	if event.Details["browserPath"] != fakeBrowser {
		t.Fatalf("browser control audit should record the operator-supplied browser path verbatim (host-local evidence): %+v", event)
	}
	data, err := os.ReadFile(auditFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "fake-secret") {
		t.Fatalf("browser control audit leaked websocket token: %s", data)
	}
}

func TestLabPreviewOpenProbeWritesAudit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/preview" {
			t.Errorf("unexpected preview path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
		_, _ = w.Write([]byte("secret-preview-body"))
	}))
	defer server.Close()
	var out, errOut bytes.Buffer
	guestURL := server.URL + "/preview"
	code := Main([]string{
		"lab",
		"preview-open",
		"--enable-lab",
		"--guest-url",
		guestURL,
		"--timeout",
		"50ms",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	for _, want := range []string{
		"Hideout lab: experimental evidence only",
		"capability=preview.open.probe",
		"route=lab-probe",
		"mode=preview-open",
		"guest-url=" + guestURL,
		"host-url=http://127.0.0.1:",
		"status-code=204",
		"probe=http-get ok",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("lab output missing %q:\n%s", want, out.String())
		}
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 1 {
		t.Fatalf("expected one lab audit file, got %d: %v", len(auditFiles), auditFiles)
	}
	validateAuditJSONLWithSchema(t, auditFiles[0])
	event := lastAuditEventByActionForAppTest(t, auditFiles[0], "preview.open.probe")
	if event.Decision != "allow" ||
		event.Details["probe"] != "preview-open" ||
		event.Details["subject"] != "lab:preview" ||
		event.Details["route"] != "lab-probe" ||
		event.Details["guestURL"] != guestURL ||
		event.Details["status"] != "ok" ||
		event.Details["httpStatusCode"] != float64(http.StatusNoContent) {
		t.Fatalf("unexpected lab audit event: %+v", event)
	}
	if !strings.HasPrefix(stringValueForAppTest(event.Details["hostURL"]), "http://127.0.0.1:") {
		t.Fatalf("preview audit should record host-visible URL: %+v", event)
	}
	data, err := os.ReadFile(auditFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret-preview-body") {
		t.Fatalf("preview audit leaked response body: %s", data)
	}
}

func startLabEchoServer(t *testing.T) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
					return
				}
				line, err := bufio.NewReader(conn).ReadString('\n')
				if err != nil {
					return
				}
				_, _ = fmt.Fprintf(conn, "echo:%s", line)
			}()
		}
	}()
	return ln.Addr().String(), func() {
		_ = ln.Close()
		<-done
	}
}

func TestExplainEphemeralShowsSessionLocalIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{"explain", "--backend", "native", "--ephemeral", "--", "echo", "hi"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Identity storage: ephemeral session-local") {
		t.Fatalf("explain missing ephemeral identity mode:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "lineage=session-fork") ||
		!strings.Contains(out.String(), "createdFrom=default") ||
		!strings.Contains(out.String(), "sourceIdentityId=id_") {
		t.Fatalf("explain missing ephemeral lineage metadata:\n%s", out.String())
	}
	if !strings.Contains(out.String(), filepath.Join(home, ".hideout", "sessions")) || !strings.Contains(out.String(), filepath.Join("identity", "home")) {
		t.Fatalf("explain should show session-local identity paths:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Machine identity: generated machine-id present in ephemeral session identity root (value hidden)") {
		t.Fatalf("explain should show hidden session machine identity state:\n%s", out.String())
	}
	if strings.Contains(out.String(), "machineId=") {
		t.Fatalf("explain must not expose raw machine-id metadata:\n%s", out.String())
	}
}

func TestExplainLimaEphemeralShowsSessionScopedInstance(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{"explain", "--backend", "lima", "--ephemeral", "--", "echo", "hi"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Lima instance: hideout-default-session-") ||
		!strings.Contains(out.String(), "session scoped") {
		t.Fatalf("lima ephemeral explain should show session-scoped instance:\n%s", out.String())
	}
	if strings.Contains(out.String(), "profile identity scoped") {
		t.Fatalf("lima ephemeral explain should not claim profile identity scoped instance:\n%s", out.String())
	}
}

func TestExplainUsesProfileCommandProxyConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	p := profile.Default("minimal-open")
	delete(p.CommandProxy.Commands, "xdg-open")
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{"explain", "--profile", "minimal-open", "--", "echo", "hi"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Command proxy: open -> host.open") {
		t.Fatalf("explain did not use profile command proxy config:\n%s", out.String())
	}
	if strings.Contains(out.String(), "xdg-open") {
		t.Fatalf("explain included omitted xdg-open proxy:\n%s", out.String())
	}
}

func TestExplainAliasPathModeUsesNeutralGuestWorkspace(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	p := profile.Default("alias-workspace")
	p.Workspace.PathMode = "alias"
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{
		"explain",
		"--profile", "alias-workspace",
		"--backend", "native",
		"--workspace", workspace,
		"--",
		"echo", "hi",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut.String())
	}
	want := "Workspace: host=" + workspace + " guest=/workspace mode=read-write pathMode=alias"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("explain missing alias workspace mapping %q:\n%s", want, out.String())
	}
	if !strings.Contains(out.String(), "Workspace path privacy: alias mode uses a neutral guest path") {
		t.Fatalf("explain missing alias path privacy note:\n%s", out.String())
	}
	if strings.Contains(out.String(), "preserve mode may expose") {
		t.Fatalf("alias explain should not show preserve warning:\n%s", out.String())
	}
}

func TestExplicitGuestWorkspaceOverridesAliasPathMode(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	p := profile.Default("alias-workspace")
	p.Workspace.PathMode = "alias"
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{
		"explain",
		"--profile", "alias-workspace",
		"--backend", "native",
		"--workspace", workspace,
		"--guest-workspace", "/repo",
		"--",
		"echo", "hi",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut.String())
	}
	want := "Workspace: host=" + workspace + " guest=/repo mode=read-write pathMode=alias"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("explicit guest workspace should win, missing %q:\n%s", want, out.String())
	}
}

func TestResolveWorkspaceMappingNormalizesExplicitGuestWorkspace(t *testing.T) {
	workspace := t.TempDir()
	host, guest, err := resolveWorkspaceMapping(workspace, "/repo/./src/..", profile.Default("default"))
	if err != nil {
		t.Fatalf("resolveWorkspaceMapping: %v", err)
	}
	if host != workspace || guest != "/repo" {
		t.Fatalf("mapping host=%q guest=%q, want host=%q guest=/repo", host, guest, workspace)
	}
}

func TestResolveWorkspaceMappingRejectsInvalidGuestWorkspace(t *testing.T) {
	workspace := t.TempDir()
	for name, guestWorkspace := range map[string]string{
		"relative":  "repo",
		"url":       "https://example.com/repo",
		"network":   "//host/repo",
		"backslash": `/tmp\repo`,
		"root":      "/",
		"empty":     " ",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := resolveWorkspaceMapping(workspace, guestWorkspace, profile.Default("default")); err == nil {
				t.Fatal("expected invalid guest workspace to fail")
			}
		})
	}
}

func TestExplainLimaShowsGuestResolution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{"explain", "--backend", "lima", "--profile", "test", "--", "echo", "hi"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "target command resolves inside the Lima guest") {
		t.Fatalf("explain missing lima boundary: %s", out.String())
	}
	if !strings.Contains(out.String(), "Guest home: /hideout/profile/home") {
		t.Fatalf("lima explain should show guest home: %s", out.String())
	}
	if !strings.Contains(out.String(), "Expected commands: none") {
		t.Fatalf("lima explain should show expected commands: %s", out.String())
	}
	if !strings.Contains(out.String(), "Lima instance: hideout-test-env-new") || !strings.Contains(out.String(), "environment scoped") {
		t.Fatalf("lima explain should show environment-scoped instance: %s", out.String())
	}
	if !strings.Contains(out.String(), "Environment: env_new") {
		t.Fatalf("lima explain should show environment summary: %s", out.String())
	}
	if strings.Contains(out.String(), filepath.Join(home, ".hideout", "profiles", "test", "home")) {
		t.Fatalf("lima explain leaked host profile home: %s", out.String())
	}
	if !strings.Contains(out.String(), "tcp://host.lima.internal:<allocated-port>") {
		t.Fatalf("lima explain should show guest broker endpoint: %s", out.String())
	}
	if strings.Contains(out.String(), "native backend does not provide") {
		t.Fatalf("lima explain should not report native limitation: %s", out.String())
	}
	if strings.Contains(out.String(), "host OS identity APIs") {
		t.Fatalf("lima explain should not report native OS identity API limitation: %s", out.String())
	}
}

func TestRunNativeRequiresWeakIsolationFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{"run", "--backend", "native", "--", "echo", "hi"}, &out, &errOut)
	if code == 0 {
		t.Fatal("expected native backend without weak-isolation flag to fail")
	}
	if !strings.Contains(errOut.String(), "weak isolation") {
		t.Fatalf("unexpected stderr: %s", errOut.String())
	}
	endpoints, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "broker-endpoint.json"))
	if err != nil {
		t.Fatalf("glob broker endpoints: %v", err)
	}
	if len(endpoints) != 0 {
		t.Fatalf("native weak-isolation failure should not start broker endpoints: %v", endpoints)
	}
}

func TestRunAutoMissingLimaDoesNotFallbackToNativeHost(t *testing.T) {
	home := t.TempDir()
	marker := filepath.Join(t.TempDir(), "host-fallback-marker")
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")
	var out, errOut bytes.Buffer
	code := Main([]string{"run", "--", "/bin/sh", "-c", "touch " + marker}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected missing lima to fail; stdout=%s stderr=%s", out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "limactl is required for lima backend") {
		t.Fatalf("stderr should report missing lima, got %s", errOut.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("auto backend must not fall back to native host execution; marker err=%v", err)
	}
}

func TestRunNativeMissingCommandReportsBackendContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{"run", "--backend", "native", "--allow-weak-isolation", "--", "hideout-missing-command"}, &out, &errOut)
	if code == 0 {
		t.Fatal("expected missing command to fail")
	}
	for _, want := range []string{
		`target command "hideout-missing-command" not found`,
		"native backend PATH",
		"no fallback was attempted",
	} {
		if !strings.Contains(errOut.String(), want) {
			t.Fatalf("stderr missing %q:\n%s", want, errOut.String())
		}
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 1 {
		t.Fatalf("expected one audit file, got %d: %v", len(auditFiles), auditFiles)
	}
	auditData, err := os.ReadFile(auditFiles[0])
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	validateAuditJSONLWithSchema(t, auditFiles[0])
	for _, want := range []string{`"action":"session.end"`, `"decision":"error"`, "hideout-missing-command", "native backend PATH"} {
		if !strings.Contains(string(auditData), want) {
			t.Fatalf("audit missing %q: %s", want, auditData)
		}
	}
}

func TestRunRejectsInvalidWorkspaceBeforeSessionCreation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	missingWorkspace := filepath.Join(t.TempDir(), "missing")
	var out, errOut bytes.Buffer
	code := Main([]string{
		"run",
		"--backend", "native",
		"--allow-weak-isolation",
		"--workspace", missingWorkspace,
		"--",
		"sh", "-c", "printf should-not-run",
	}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected missing workspace to fail; stdout=%s stderr=%s", out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "workspace") || !strings.Contains(errOut.String(), "not accessible") {
		t.Fatalf("stderr should explain workspace failure:\n%s", errOut.String())
	}
	sessionDirs, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*"))
	if err != nil {
		t.Fatalf("glob sessions: %v", err)
	}
	if len(sessionDirs) != 0 {
		t.Fatalf("workspace failure should happen before session creation, got %v", sessionDirs)
	}
}

func TestRunInvalidProfileFailsBeforeBackendStart(t *testing.T) {
	home := t.TempDir()
	fakeBin := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "limactl.log")
	t.Setenv("HOME", home)
	t.Setenv("PATH", fakeBin)
	if err := os.WriteFile(filepath.Join(fakeBin, "limactl"), []byte(fakeLimactlScript(logPath, "exit 0")), 0o700); err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(home, ".hideout", "profiles", "bad", "profile.json")
	if err := os.MkdirAll(filepath.Dir(profilePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profilePath, []byte("{bad json"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{"run", "--profile", "bad", "--backend", "lima", "--", "/bin/sh", "-c", "true"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected invalid profile to fail; stdout=%s stderr=%s", out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "invalid character") {
		t.Fatalf("stderr should report invalid profile, got %s", errOut.String())
	}
	if data, err := os.ReadFile(logPath); err == nil && len(data) != 0 {
		t.Fatalf("backend should not start for invalid profile, limactl log=%s", data)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read limactl log: %v", err)
	}
	sessionDirs, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*"))
	if err != nil {
		t.Fatalf("glob sessions: %v", err)
	}
	if len(sessionDirs) != 0 {
		t.Fatalf("invalid profile should fail before session creation, got %v", sessionDirs)
	}
}

func TestRunRequiresNetworkConnectCapability(t *testing.T) {
	home := t.TempDir()
	marker := filepath.Join(t.TempDir(), "should-not-run")
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	p := profile.Default("no-network")
	p.Policy.MaxCapabilities = []string{"host.open", "guest.exec"}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{
		"run",
		"--profile", "no-network",
		"--backend", "native",
		"--allow-weak-isolation",
		"--",
		"sh", "-c", "touch " + marker,
	}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected run to fail without network.connect capability; stdout=%s", out.String())
	}
	if !strings.Contains(errOut.String(), `action "network.connect" exceeds profile max capabilities`) {
		t.Fatalf("missing network capability error: %s", errOut.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("target command appears to have run; marker err=%v", err)
	}
}

func TestExplainRejectsRelativeGuestWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{
		"explain",
		"--backend", "native",
		"--guest-workspace", "repo",
		"--",
		"echo", "hi",
	}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected relative guest workspace to fail; stdout=%s stderr=%s", out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "guest workspace") || !strings.Contains(errOut.String(), "must be absolute") {
		t.Fatalf("stderr should explain guest workspace failure:\n%s", errOut.String())
	}
}

func TestAutoBackendDefaultsToLima(t *testing.T) {
	if got := resolveBackendName("auto"); got != "lima" {
		t.Fatalf("auto backend=%s want lima", got)
	}
	if got := resolveBackendName(""); got != "lima" {
		t.Fatalf("empty backend=%s want lima", got)
	}
}

func TestProfileCloneCommandCreatesPolicyClone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	var out, errOut bytes.Buffer
	if code := Main([]string{"profile", "init", "source"}, &out, &errOut); code != 0 {
		t.Fatalf("init exit=%d stderr=%s", code, errOut.String())
	}
	source, err := store.Load("source")
	if err != nil {
		t.Fatalf("load source: %v", err)
	}
	source.Git.UserEmail = "source@example.com"
	source.Policy.ScriptRefs = []profile.ScriptRef{{
		ID:          "command-policy",
		Path:        "policy/nested/command.js",
		Entrypoints: []string{"decideCommand"},
	}}
	if err := store.Save(source); err != nil {
		t.Fatalf("save source: %v", err)
	}
	mustWriteAppTest(t, filepath.Join(store.ProfileDir("source"), "policy", "nested", "command.js"), "function decideCommand() {}\n")
	sourceOnlyIdentityFiles := map[string]string{
		"home":    "token.txt",
		"config":  filepath.Join("app", "config.json"),
		"cache":   filepath.Join("sdk", "cache.db"),
		"data":    filepath.Join("app", "state.json"),
		"browser": "cookie",
		"machine": "source-only-machine-id",
	}
	for dir, rel := range sourceOnlyIdentityFiles {
		mustWriteAppTest(t, filepath.Join(store.ProfileDir("source"), dir, rel), "source identity material\n")
	}
	out.Reset()
	errOut.Reset()
	if code := Main([]string{"profile", "clone", "source", "target"}, &out, &errOut); code != 0 {
		t.Fatalf("clone exit=%d stderr=%s", code, errOut.String())
	}
	targetPath := filepath.Join(home, ".hideout", "profiles", "target", "profile.json")
	if !strings.Contains(out.String(), targetPath) {
		t.Fatalf("clone output should contain target path %s, got %s", targetPath, out.String())
	}
	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read target profile: %v", err)
	}
	if !strings.Contains(string(data), `"lineageMode": "policy-clone"`) || !strings.Contains(string(data), `"createdFrom": "source"`) {
		t.Fatalf("target profile missing clone lineage: %s", data)
	}
	if _, err := os.Stat(filepath.Join(home, ".hideout", "profiles", "target", "identity.json")); err != nil {
		t.Fatalf("target identity metadata missing: %v", err)
	}
	loadedSource, err := store.Load("source")
	if err != nil {
		t.Fatalf("reload source: %v", err)
	}
	loadedTarget, err := store.Load("target")
	if err != nil {
		t.Fatalf("load target: %v", err)
	}
	if loadedTarget.Git.UserEmail != "source@example.com" {
		t.Fatalf("clone did not copy policy fields: %+v", loadedTarget.Git)
	}
	scriptData, err := os.ReadFile(filepath.Join(store.ProfileDir("target"), "policy", "nested", "command.js"))
	if err != nil {
		t.Fatalf("clone should copy policy script file: %v", err)
	}
	if string(scriptData) != "function decideCommand() {}\n" {
		t.Fatalf("cloned policy script mismatch: %q", scriptData)
	}
	if loadedTarget.Metadata["profileId"] == loadedSource.Metadata["profileId"] ||
		loadedTarget.Metadata["identityId"] == loadedSource.Metadata["identityId"] ||
		loadedTarget.Metadata["machineId"] == loadedSource.Metadata["machineId"] {
		t.Fatalf("clone reused source identity material: source=%+v target=%+v", loadedSource.Metadata, loadedTarget.Metadata)
	}
	if loadedTarget.Metadata["sourceIdentityId"] != loadedSource.Metadata["identityId"] {
		t.Fatalf("clone missing source identity lineage: source=%+v target=%+v", loadedSource.Metadata, loadedTarget.Metadata)
	}
	for dir, rel := range sourceOnlyIdentityFiles {
		if _, err := os.Stat(filepath.Join(store.ProfileDir("target"), dir, rel)); !os.IsNotExist(err) {
			t.Fatalf("clone copied source %s identity state %s; err=%v", dir, rel, err)
		}
	}
	identityData, err := os.ReadFile(filepath.Join(store.ProfileDir("target"), "identity.json"))
	if err != nil {
		t.Fatalf("read target identity metadata: %v", err)
	}
	var identity map[string]string
	if err := json.Unmarshal(identityData, &identity); err != nil {
		t.Fatalf("decode target identity metadata: %v", err)
	}
	if identity["identityId"] != loadedTarget.Metadata["identityId"] || identity["machineId"] != loadedTarget.Metadata["machineId"] {
		t.Fatalf("identity.json mismatch: %+v profile=%+v", identity, loadedTarget.Metadata)
	}
}

func TestProfilePathRejectsInvalidProfileName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{"profile", "path", "../outside"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected invalid profile path command to fail; stdout=%s", out.String())
	}
	if !strings.Contains(errOut.String(), "invalid profile name") {
		t.Fatalf("unexpected stderr: %s", errOut.String())
	}
	if strings.Contains(out.String(), "..") {
		t.Fatalf("profile path should not print traversed path: %s", out.String())
	}
}

func TestProfileInitRejectsExistingProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	if code := Main([]string{"profile", "init", "test"}, &out, &errOut); code != 0 {
		t.Fatalf("init exit=%d stderr=%s", code, errOut.String())
	}
	profilePath := filepath.Join(home, ".hideout", "profiles", "test", "profile.json")
	initial, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read initial profile: %v", err)
	}
	out.Reset()
	errOut.Reset()
	code := Main([]string{"profile", "init", "test"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected second init to fail; stdout=%s", out.String())
	}
	if !strings.Contains(errOut.String(), `profile "test" already exists`) {
		t.Fatalf("unexpected stderr: %s", errOut.String())
	}
	after, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read profile after failed init: %v", err)
	}
	if !bytes.Equal(after, initial) {
		t.Fatalf("failed init should not rewrite profile\nbefore=%s\nafter=%s", initial, after)
	}
}

func TestProfileRotateAndResetCommandsChangeIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	if code := Main([]string{"profile", "init", "test"}, &out, &errOut); code != 0 {
		t.Fatalf("init exit=%d stderr=%s", code, errOut.String())
	}
	profilePath := filepath.Join(home, ".hideout", "profiles", "test", "profile.json")
	initial := readProfileMetadataForAppTest(t, profilePath)
	mustWriteAppTest(t, filepath.Join(home, ".hideout", "profiles", "test", "home", "token.txt"), "secret")

	out.Reset()
	errOut.Reset()
	if code := Main([]string{"profile", "rotate-identity", "test"}, &out, &errOut); code != 0 {
		t.Fatalf("rotate exit=%d stderr=%s", code, errOut.String())
	}
	rotated := readProfileMetadataForAppTest(t, profilePath)
	if rotated["identityId"] == initial["identityId"] || rotated["previousIdentityId"] != initial["identityId"] {
		t.Fatalf("rotate metadata mismatch: before=%+v after=%+v output=%s", initial, rotated, out.String())
	}
	if !strings.Contains(out.String(), "previousIdentityId="+initial["identityId"]) {
		t.Fatalf("rotate output missing previous identity: %s", out.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".hideout", "profiles", "test", "identity-archive", initial["identityId"], "home", "token.txt")); err != nil {
		t.Fatalf("rotate should archive old home state: %v", err)
	}

	mustWriteAppTest(t, filepath.Join(home, ".hideout", "profiles", "test", "home", "token.txt"), "secret2")
	out.Reset()
	errOut.Reset()
	if code := Main([]string{"profile", "reset", "test"}, &out, &errOut); code != 0 {
		t.Fatalf("reset exit=%d stderr=%s", code, errOut.String())
	}
	reset := readProfileMetadataForAppTest(t, profilePath)
	if reset["identityId"] == rotated["identityId"] || reset["previousIdentityId"] != rotated["identityId"] {
		t.Fatalf("reset metadata mismatch: before=%+v after=%+v output=%s", rotated, reset, out.String())
	}
	for _, key := range []string{"identityArchive", "identityArchiveId", "identityRotatedAt"} {
		if reset[key] != "" {
			t.Fatalf("reset command retained stale rotate metadata %s: %+v", key, reset)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".hideout", "profiles", "test", "home", "token.txt")); !os.IsNotExist(err) {
		t.Fatalf("reset should delete generated home state; err=%v", err)
	}
}

func TestCleanupCommandRemovesSessionEphemeralStateButKeepsAudit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sessionDir := filepath.Join(home, ".hideout", "sessions", "ses_test")
	mustWriteAppTest(t, filepath.Join(sessionDir, "tmp", "file"), "tmp")
	mustWriteAppTest(t, filepath.Join(sessionDir, "network", "proxy.url"), "socks5://user:pass@127.0.0.1:1080")
	mustWriteAppTest(t, filepath.Join(sessionDir, "network", "bootstrap.sh"), "#!/bin/sh")
	mustWriteAppTest(t, filepath.Join(sessionDir, "shims", "open"), "shim")
	mustWriteAppTest(t, filepath.Join(sessionDir, "bootstrap", "bootstrap.sh"), "#!/bin/sh")
	mustWriteAppTest(t, filepath.Join(sessionDir, "identity", "home", ".gitconfig"), "[user]\n")
	mustWriteAppTest(t, filepath.Join(sessionDir, "broker.sock"), "sock")
	mustWriteAppTest(t, filepath.Join(sessionDir, "broker-endpoint.json"), "{}")
	mustWriteAppTest(t, filepath.Join(sessionDir, "network-plan.json"), "{}")
	mustWriteAppTest(t, filepath.Join(sessionDir, "audit.jsonl"), "{}\n")
	var out, errOut bytes.Buffer
	code := Main([]string{"cleanup"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	if !strings.Contains(out.String(), "cleanup: sessions=1 removed=") ||
		!strings.Contains(out.String(), "audit=preserved") ||
		!strings.Contains(out.String(), "secretState=removed") {
		t.Fatalf("unexpected cleanup output: %s", out.String())
	}
	for _, path := range []string{
		filepath.Join(sessionDir, "tmp"),
		filepath.Join(sessionDir, "network"),
		filepath.Join(sessionDir, "shims"),
		filepath.Join(sessionDir, "bootstrap"),
		filepath.Join(sessionDir, "identity"),
		filepath.Join(sessionDir, "broker.sock"),
		filepath.Join(sessionDir, "broker-endpoint.json"),
		filepath.Join(sessionDir, "network-plan.json"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("session cleanup should remove %s; err=%v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "audit.jsonl")); err != nil {
		t.Fatalf("audit should be kept: %v", err)
	}
}

func TestCleanupCommandDryRunKeepsFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sessionDir := filepath.Join(home, ".hideout", "sessions", "ses_test")
	secretPath := filepath.Join(sessionDir, "network", "proxy.url")
	mustWriteAppTest(t, secretPath, "socks5://user:pass@127.0.0.1:1080")
	var out, errOut bytes.Buffer
	code := Main([]string{"cleanup", "--dry-run"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	if !strings.Contains(out.String(), "cleanup: sessions=1 would remove=") ||
		!strings.Contains(out.String(), "audit=preserved") ||
		!strings.Contains(out.String(), "secretState=would-remove") {
		t.Fatalf("unexpected cleanup output: %s", out.String())
	}
	if _, err := os.Stat(secretPath); err != nil {
		t.Fatalf("dry-run should keep proxy secret: %v", err)
	}
}

func TestCleanupCommandSessionFilterKeepsOtherSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	firstSecret := filepath.Join(home, ".hideout", "sessions", "ses_first", "network", "proxy.url")
	secondSecret := filepath.Join(home, ".hideout", "sessions", "ses_second", "network", "proxy.url")
	firstAudit := filepath.Join(home, ".hideout", "sessions", "ses_first", "audit.jsonl")
	secondAudit := filepath.Join(home, ".hideout", "sessions", "ses_second", "audit.jsonl")
	mustWriteAppTest(t, firstSecret, "socks5://first")
	mustWriteAppTest(t, secondSecret, "socks5://second")
	mustWriteAppTest(t, firstAudit, "{}\n")
	mustWriteAppTest(t, secondAudit, "{}\n")
	var out, errOut bytes.Buffer
	code := Main([]string{"cleanup", "--session", "ses_first"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	if !strings.Contains(out.String(), "cleanup: sessions=1 removed=") ||
		!strings.Contains(out.String(), "audit=preserved") ||
		!strings.Contains(out.String(), "secretState=removed") {
		t.Fatalf("unexpected cleanup output: %s", out.String())
	}
	if _, err := os.Stat(firstSecret); !os.IsNotExist(err) {
		t.Fatalf("selected session secret should be removed; err=%v", err)
	}
	if _, err := os.Stat(secondSecret); err != nil {
		t.Fatalf("other session secret should be kept: %v", err)
	}
	if _, err := os.Stat(firstAudit); err != nil {
		t.Fatalf("selected session audit should be preserved: %v", err)
	}
	if _, err := os.Stat(secondAudit); err != nil {
		t.Fatalf("other session audit should be preserved: %v", err)
	}
}

func TestCleanupAuditDetailsDoNotExposeRemovedPaths(t *testing.T) {
	home := t.TempDir()
	sessionDir := filepath.Join(home, ".hideout", "sessions", "ses_secret")
	details := cleanupAuditDetails(session.CleanupResult{
		Sessions: 1,
		Removed: []string{
			filepath.Join(sessionDir, "tmp"),
			filepath.Join(sessionDir, "network", "proxy.url"),
			filepath.Join(sessionDir, "broker.sock"),
			filepath.Join(sessionDir, "broker-endpoint.json"),
			filepath.Join(sessionDir, "network-plan.json"),
			filepath.Join("/tmp", "hideout-ses_secret.sock"),
		},
	})
	data, err := json.Marshal(details)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, leaked := range []string{
		home,
		sessionDir,
		"proxy.url",
		"broker.sock",
		"broker-endpoint.json",
		"network-plan.json",
		"hideout-ses_secret.sock",
	} {
		if strings.Contains(text, leaked) {
			t.Fatalf("cleanup audit details leaked %q: %s", leaked, text)
		}
	}
	for _, want := range []string{`"sessions":1`, `"removedCount":6`, `"removedTypes"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("cleanup audit details missing %q: %s", want, text)
		}
	}
}

func TestDoctorReportsCoreChecks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	setSafeBrowserPathForAppTest(t)
	var out, errOut bytes.Buffer
	code := Main([]string{"doctor", "--backend", "native"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	for _, want := range []string{
		"Hideout doctor",
		"profile: warn default missing",
		"manager: ok profiles=0",
		"workspace: ok",
		"mount: ok",
		"backend: warn native is weak isolation",
		"env: ok",
		"secretEnv=absent",
		"policy: ok",
		"network: ok mode=direct",
		"broker: ok",
		"hostfs: ok inactive grants=0",
		"host-open: ok",
		"browserProfile=present",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("doctor output missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), filepath.Join(home, ".hideout", "profiles", "default", "browser")) {
		t.Fatalf("doctor output leaked isolated browser profile path:\n%s", out.String())
	}
}

func TestDoctorRejectsGenericBrowserPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HIDEOUT_BROWSER_PATH", filepath.Join(t.TempDir(), "open"))
	var out, errOut bytes.Buffer
	code := Main([]string{"doctor", "--backend", "native"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected generic browser path to fail doctor; stdout=%s", out.String())
	}
	if !strings.Contains(out.String(), "host-open: error browser path must be a direct isolated browser binary") {
		t.Fatalf("doctor did not report unsafe browser path:\nstdout=%s\nstderr=%s", out.String(), errOut.String())
	}
}

func TestDoctorRejectsBrowserPathSymlinkToGenericOpener(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	generic := filepath.Join(dir, "xdg-open")
	if err := os.WriteFile(generic, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	browserPath := filepath.Join(dir, "browser")
	if err := os.Symlink(generic, browserPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("HIDEOUT_BROWSER_PATH", browserPath)
	var out, errOut bytes.Buffer
	code := Main([]string{"doctor", "--backend", "native"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected generic browser symlink to fail doctor; stdout=%s", out.String())
	}
	if !strings.Contains(out.String(), "host-open: error browser path must be a direct isolated browser binary") {
		t.Fatalf("doctor did not report unsafe browser path symlink:\nstdout=%s\nstderr=%s", out.String(), errOut.String())
	}
}

func TestDoctorRejectsUnsupportedBrowserApp(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("HIDEOUT_BROWSER_APP is validated by the darwin app launcher")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HIDEOUT_BROWSER_APP", "Safari")
	var out, errOut bytes.Buffer
	code := Main([]string{"doctor", "--backend", "native"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected unsupported browser app to fail doctor; stdout=%s", out.String())
	}
	if !strings.Contains(out.String(), `host-open: error browser app "Safari" is not a supported isolated browser app`) {
		t.Fatalf("doctor did not report unsupported browser app:\nstdout=%s\nstderr=%s", out.String(), errOut.String())
	}
}

func TestDarwinBrowserAppInstalledInStandardRoots(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Chromium.app"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "Applications", "Google Chrome.app"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !darwinBrowserAppInstalledInRoots("Chromium", "", []string{root}) {
		t.Fatal("expected Chromium app in system root to be detected")
	}
	if !darwinBrowserAppInstalledInRoots("Google Chrome.app", home, nil) {
		t.Fatal("expected user Applications app to be detected")
	}
	if darwinBrowserAppInstalledInRoots("Vivaldi", "", []string{root}) {
		t.Fatal("unexpected missing app detection")
	}
}

func TestCheckEnvRejectsHideoutSecretEnvLeak(t *testing.T) {
	var reports []string
	checkEnv(envpolicy.Result{
		Env: []string{"HIDEOUT_SECRET_DEFAULT_PROXY=socks5://user:pass@127.0.0.1:1080"},
	}, func(name, status, message string) {
		reports = append(reports, name+": "+status+" "+message)
	})
	got := strings.Join(reports, "\n")
	if !strings.Contains(got, "env: error target env contains hideout secret variables") {
		t.Fatalf("expected secret env leak error, got %s", got)
	}
}

func TestDoctorUsesAliasWorkspaceMapping(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	setSafeBrowserPathForAppTest(t)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	p := profile.Default("alias-workspace")
	p.Workspace.PathMode = "alias"
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{
		"doctor",
		"--profile", "alias-workspace",
		"--backend", "native",
		"--workspace", workspace,
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	want := "workspace: ok host=" + workspace + " guest=/workspace mode=read-write pathMode=alias"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("doctor missing alias workspace mapping %q:\n%s", want, out.String())
	}
}

func TestDoctorEphemeralUsesSessionForkIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	setSafeBrowserPathForAppTest(t)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	p := profile.Default("ephemeral-doctor")
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load("ephemeral-doctor")
	if err != nil {
		t.Fatalf("load source profile: %v", err)
	}
	sourceIdentityID := loaded.Metadata["identityId"]
	sourceMachineID := loaded.Metadata["machineId"]

	var out, errOut bytes.Buffer
	code := Main([]string{
		"doctor",
		"--profile", "ephemeral-doctor",
		"--backend", "native",
		"--ephemeral",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	for _, want := range []string{
		"identity: ok mode=ephemeral",
		filepath.Join(home, ".hideout", "sessions"),
		filepath.Join("identity"),
		"lineage=session-fork",
		"sourceIdentityId=" + sourceIdentityID,
		"env: ok",
		"policy: ok",
		"broker: ok",
		"host-open: ok",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("doctor ephemeral output missing %q:\n%s", want, out.String())
		}
	}
	reloaded, err := store.Load("ephemeral-doctor")
	if err != nil {
		t.Fatalf("reload source profile: %v", err)
	}
	if reloaded.Metadata["identityId"] != sourceIdentityID || reloaded.Metadata["machineId"] != sourceMachineID {
		t.Fatalf("doctor --ephemeral mutated persistent identity: before=%s/%s after=%+v", sourceIdentityID, sourceMachineID, reloaded.Metadata)
	}
	identityDirs, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "identity"))
	if err != nil {
		t.Fatalf("glob identity dirs: %v", err)
	}
	if len(identityDirs) != 0 {
		t.Fatalf("doctor should clean ephemeral identity dirs, got %v", identityDirs)
	}
}

func TestDoctorBadProxySecretFailsNetworkCheck(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HIDEOUT_SECRET_DEFAULT_PROXY", "socks4://user:pass@127.0.0.1:1080")
	var out, errOut bytes.Buffer
	code := Main([]string{"doctor", "--network", "tun2socks", "--proxy-secret", "default-proxy"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected doctor to fail for bad proxy secret; stdout=%s", out.String())
	}
	if !strings.Contains(out.String(), "network: error unsupported proxy scheme") {
		t.Fatalf("doctor did not report network error:\n%s", out.String())
	}
	if strings.Contains(out.String(), "user:pass") || strings.Contains(errOut.String(), "user:pass") {
		t.Fatalf("doctor leaked proxy credentials:\nstdout=%s\nstderr=%s", out.String(), errOut.String())
	}
}

func TestDoctorMissingProxySecretDoesNotExposeBackingEnvName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HIDEOUT_SECRET_MISSING_PROXY", "")
	var out, errOut bytes.Buffer
	code := Main([]string{"doctor", "--network", "tun2socks", "--proxy-secret", "missing-proxy"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected doctor to fail for missing proxy secret; stdout=%s", out.String())
	}
	combined := out.String() + errOut.String()
	if !strings.Contains(combined, "secret ref missing-proxy") {
		t.Fatalf("doctor should report proxy secret by ref name:\nstdout=%s\nstderr=%s", out.String(), errOut.String())
	}
	if strings.Contains(combined, "HIDEOUT_SECRET_") {
		t.Fatalf("doctor leaked backing secret env name:\nstdout=%s\nstderr=%s", out.String(), errOut.String())
	}
}

func TestDoctorReportsMissingLima(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")
	var out, errOut bytes.Buffer
	code := Main([]string{"doctor", "--backend", "lima"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected doctor to fail when limactl is missing; stdout=%s", out.String())
	}
	if !strings.Contains(out.String(), "backend: error lima unavailable: limactl is required for lima backend") {
		t.Fatalf("doctor did not report missing lima:\n%s", out.String())
	}
}

func TestDoctorValidatesGeneratedLimaConfig(t *testing.T) {
	home := t.TempDir()
	fakeBin := t.TempDir()
	workspace := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "limactl.log")
	t.Setenv("HOME", home)
	t.Setenv("PATH", fakeBin)
	t.Setenv("HIDEOUT_SECRET_CANARY", "secret")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")
	setSafeBrowserPathForAppTest(t)
	setFakeLinuxShimPathForAppTest(t)
	script := fakeLimactlScript(logPath, `
if [ -n "${HIDEOUT_SECRET_CANARY:-}" ] || [ -n "${HTTP_PROXY:-}" ] || [ -n "${HTTPS_PROXY:-}" ]; then
  echo "host env leaked" >&2
  exit 7
fi
case "$1" in
  validate)
    [ -f "$2" ] || { echo "missing generated YAML" >&2; exit 8; }
    exit 0
    ;;
  *)
    exit 0
    ;;
esac
`)
	if err := os.WriteFile(filepath.Join(fakeBin, "limactl"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{"doctor", "--backend", "lima", "--workspace", workspace}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	if !strings.Contains(out.String(), "lima-config: ok generated YAML validates") {
		t.Fatalf("doctor did not report Lima YAML validation:\n%s", out.String())
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake limactl log: %v", err)
	}
	if !strings.Contains(string(logData), "validate ") {
		t.Fatalf("doctor did not run limactl validate:\n%s", logData)
	}
	if strings.Contains(out.String(), "HIDEOUT_SECRET_CANARY") || strings.Contains(out.String(), fakeBin) {
		t.Fatalf("doctor leaked host env or tool path:\n%s", out.String())
	}
}

func TestDoctorReportsInvalidGeneratedLimaConfig(t *testing.T) {
	home := t.TempDir()
	fakeBin := t.TempDir()
	workspace := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "limactl.log")
	t.Setenv("HOME", home)
	t.Setenv("PATH", fakeBin)
	setSafeBrowserPathForAppTest(t)
	setFakeLinuxShimPathForAppTest(t)
	script := fakeLimactlScript(logPath, `
case "$1" in
  validate)
    echo "bad lima yaml" >&2
    exit 42
    ;;
  *)
    exit 0
    ;;
esac
`)
	if err := os.WriteFile(filepath.Join(fakeBin, "limactl"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{"doctor", "--backend", "lima", "--workspace", workspace}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected doctor to fail for invalid Lima YAML; stdout=%s", out.String())
	}
	if !strings.Contains(out.String(), "lima-config: error generated YAML failed validation: bad lima yaml") {
		t.Fatalf("doctor did not report invalid generated Lima config:\n%s\nstderr=%s", out.String(), errOut.String())
	}
}

func TestDoctorReportsMissingLimaCommandProxyShim(t *testing.T) {
	home := t.TempDir()
	fakeBin := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", fakeBin)
	t.Setenv("HIDEOUT_LINUX_SHIM_PATH", "")
	if err := os.WriteFile(filepath.Join(fakeBin, "limactl"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{"doctor", "--backend", "lima"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected doctor to fail when linux shim is missing; stdout=%s", out.String())
	}
	if !strings.Contains(out.String(), "command-proxy: error prebuilt linux hideout-shim is required") {
		t.Fatalf("doctor did not report missing linux shim:\n%s", out.String())
	}
	if strings.Contains(out.String(), fakeBin) {
		t.Fatalf("doctor should not leak host shim search paths:\n%s", out.String())
	}
}

func TestDoctorReportsMissingLimaHostFSDWhenHostFSGrantsActive(t *testing.T) {
	home := t.TempDir()
	fakeBin := t.TempDir()
	workspace := t.TempDir()
	hostFile := filepath.Join(t.TempDir(), "input.txt")
	if err := os.WriteFile(hostFile, []byte("host data"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", fakeBin)
	t.Setenv("HIDEOUT_LINUX_HOSTFSD_PATH", "")
	setSafeBrowserPathForAppTest(t)
	setFakeLinuxShimPathForAppTest(t)
	if err := os.WriteFile(filepath.Join(fakeBin, "limactl"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	p := profile.Default("hostfs-doctor")
	p.HostFS.Grants = []hostfs.Rule{{
		ID:       "hfs_profile_allow",
		HostPath: hostFile,
		Ops:      []hostfs.Op{hostfs.OpRead},
		Scope:    hostfs.ScopeExactFile,
		Reason:   "doctor hostfs grant",
	}}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{"doctor", "--profile", "hostfs-doctor", "--backend", "lima", "--workspace", workspace}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected doctor to fail when HostFS grant needs missing hostfsd; stdout=%s", out.String())
	}
	if !strings.Contains(out.String(), "hostfs: error grants=1 prebuilt linux hideout-hostfsd is required for Lima HostFS") {
		t.Fatalf("doctor did not report missing hostfsd:\n%s", out.String())
	}
	if strings.Contains(out.String(), hostFile) || strings.Contains(out.String(), fakeBin) {
		t.Fatalf("doctor should not leak HostFS grant path or helper search paths:\n%s", out.String())
	}
}

func TestDoctorReportsLimaHostFSDPresentWhenHostFSGrantsActive(t *testing.T) {
	home := t.TempDir()
	fakeBin := t.TempDir()
	workspace := t.TempDir()
	hostFile := filepath.Join(t.TempDir(), "input.txt")
	if err := os.WriteFile(hostFile, []byte("host data"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", fakeBin)
	setSafeBrowserPathForAppTest(t)
	setFakeLinuxShimPathForAppTest(t)
	setFakeLinuxHostFSDPathForAppTest(t)
	if err := os.WriteFile(filepath.Join(fakeBin, "limactl"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	p := profile.Default("hostfs-doctor-ok")
	p.HostFS.Grants = []hostfs.Rule{{
		ID:       "hfs_profile_allow",
		HostPath: hostFile,
		Ops:      []hostfs.Op{hostfs.OpRead},
		Scope:    hostfs.ScopeExactFile,
		Reason:   "doctor hostfs grant",
	}}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{"doctor", "--profile", "hostfs-doctor-ok", "--backend", "lima", "--workspace", workspace}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "hostfs: ok grants=1 linux hostfsd=present") {
		t.Fatalf("doctor did not report hostfsd present:\n%s", out.String())
	}
}

func TestDoctorReportsBrokenLimaMount(t *testing.T) {
	home := t.TempDir()
	fakeBin := t.TempDir()
	badWorkspace := filepath.Join(t.TempDir(), "workspace-file")
	t.Setenv("HOME", home)
	t.Setenv("PATH", fakeBin)
	if err := os.WriteFile(filepath.Join(fakeBin, "limactl"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(badWorkspace, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{"doctor", "--backend", "lima", "--workspace", badWorkspace}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected doctor to fail for broken mount; stdout=%s", out.String())
	}
	for _, want := range []string{
		"backend: ok lima available",
		"workspace: error workspace",
		"is not a directory",
		"mount: error workspace mapping is unavailable",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("doctor output missing %q:\n%s", want, out.String())
		}
	}
}

func TestDoctorInvalidProfileReportsProfileError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	profilePath := filepath.Join(home, ".hideout", "profiles", "bad", "profile.json")
	if err := os.MkdirAll(filepath.Dir(profilePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profilePath, []byte("{bad json"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{"doctor", "--profile", "bad"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected doctor to fail for invalid profile; stdout=%s", out.String())
	}
	if !strings.Contains(out.String(), "profile: error") {
		t.Fatalf("doctor did not report profile error:\n%s", out.String())
	}
}

func TestDoctorRequiresNetworkConnectCapability(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	p := profile.Default("no-network")
	p.Policy.MaxCapabilities = []string{"host.open", "guest.exec"}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{"doctor", "--profile", "no-network", "--backend", "native"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected doctor to fail without network.connect capability; stdout=%s", out.String())
	}
	if !strings.Contains(out.String(), `policy: error action "network.connect" exceeds profile max capabilities`) {
		t.Fatalf("doctor did not report network capability error:\n%s", out.String())
	}
}

func TestDoctorReportsPolicyScriptFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	p := profile.Default("scripted")
	p.Policy.ScriptRefs = []profile.ScriptRef{{
		ID:          "bad-script",
		Path:        "policy/bad.js",
		Entrypoints: []string{"decideCommand"},
	}}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(store.ProfileDir("scripted"), "policy", "bad.js")
	if err := os.WriteFile(scriptPath, []byte("function decideCommand(ctx) { return hideout.decision.allow({ route: 'host-broker', action: 'host.exec.shell', resources: ['*'], reason: 'bad' }); }"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{"doctor", "--profile", "scripted"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected doctor to fail for policy script; stdout=%s", out.String())
	}
	if !strings.Contains(out.String(), "policy: error script bad-script entrypoint decideCommand") {
		t.Fatalf("doctor did not report policy script error:\n%s", out.String())
	}
}

func TestDoctorRejectsCommandScriptProposalMismatchedToBrokerRequest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	p := profile.Default("scripted-mismatch")
	p.Policy.ScriptRefs = []profile.ScriptRef{{
		ID:          "mismatch",
		Path:        "policy/mismatch.js",
		Entrypoints: []string{"decideCommand"},
	}}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(store.ProfileDir("scripted-mismatch"), "policy", "mismatch.js")
	source := `function decideCommand(ctx) {
  return hideout.decision.allow({ route: "guest-direct", action: "guest.exec", resources: ["guest-command:open"], reason: "wrong request" });
}`
	if err := os.WriteFile(scriptPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{"doctor", "--profile", "scripted-mismatch", "--backend", "native"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected doctor to fail for mismatched policy script proposal; stdout=%s", out.String())
	}
	if !strings.Contains(out.String(), `script proposal action "guest.exec" does not match request action "host.open"`) {
		t.Fatalf("doctor did not report mismatched policy script proposal:\n%s", out.String())
	}
}

func TestDoctorPolicyScriptContextIncludesCommandTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	setSafeBrowserPathForAppTest(t)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	p := profile.Default("scripted-ok")
	p.Policy.ScriptRefs = []profile.ScriptRef{{
		ID:          "target-check",
		Path:        "policy/target-check.js",
		Entrypoints: []string{"decideCommand"},
	}}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(store.ProfileDir("scripted-ok"), "policy", "target-check.js")
	source := `function decideCommand(ctx) {
  if (ctx.command.target !== "https://example.com") {
    return hideout.decision.deny({ route: "deny", action: "host.open", resources: ["url:https"], reason: "missing target" });
  }
  return hideout.decision.auditOnly({ route: "host-broker", action: "host.open", resources: ["url:https"], reason: "doctor target present" });
}`
	if err := os.WriteFile(scriptPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{"doctor", "--profile", "scripted-ok", "--backend", "native"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("doctor exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	if !strings.Contains(out.String(), "policy: ok") {
		t.Fatalf("doctor did not report policy ok:\n%s", out.String())
	}
}

func TestCheckMountPlanValidatesLimaRuntimeMountBoundary(t *testing.T) {
	layout := sessionTestLayout(t)
	p := profile.Default("default")
	workspace := t.TempDir()
	profileDir := filepath.Join(t.TempDir(), "profile")
	var reports []string
	checkMountPlan("lima", p, layout, workspace, "/workspace", profileDir, func(name, status, message string) {
		reports = append(reports, name+": "+status+" "+message)
	})
	got := strings.Join(reports, "\n")
	for _, want := range []string{
		"mount: ok",
		"profileRuntimeOnly=true",
		"sessionRuntimeOnly=true",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("mount plan report missing %q:\n%s", want, got)
		}
	}
}

func TestValidateRuntimeMountRejectsControlPlanePaths(t *testing.T) {
	root := t.TempDir()
	allowed := []string{"home", "cache", "config", "data", "browser", "machine"}
	for _, location := range []string{
		root,
		filepath.Join(root, "profile.json"),
		filepath.Join(root, "policy", "command.js"),
	} {
		if err := validateRuntimeMount("profile", root, location, allowed); err == nil {
			t.Fatalf("expected %s to be rejected", location)
		}
	}
	if err := validateRuntimeMount("profile", root, filepath.Join(root, "home"), allowed); err != nil {
		t.Fatalf("runtime identity mount should be allowed: %v", err)
	}
}

func TestDoctorReportsAuditRedactionScriptFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	p := profile.Default("bad-redaction")
	p.Policy.ScriptRefs = []profile.ScriptRef{{
		ID:          "bad-redaction",
		Path:        "policy/bad-redaction.js",
		Entrypoints: []string{"redactAudit"},
	}}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(store.ProfileDir("bad-redaction"), "policy", "bad-redaction.js")
	if err := os.WriteFile(scriptPath, []byte("function redactAudit(ctx) { return { reason: 'missing details' }; }"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{"doctor", "--profile", "bad-redaction"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected doctor to fail for audit redaction script; stdout=%s", out.String())
	}
	if !strings.Contains(out.String(), "policy: error script bad-redaction entrypoint redactAudit") {
		t.Fatalf("doctor did not report audit redaction script error:\n%s", out.String())
	}
}

func TestUIPrintURLStartsLocalManagerAPIAndExits(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{"ui", "--no-open", "--print-url", "--ttl", "1m"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	for _, want := range []string{
		"Hideout UI: http://127.0.0.1:",
		"/#token=ui_",
		"Manager API: http://127.0.0.1:",
		"/api/v1/overview",
		"Token expires:",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("ui output missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "Press Ctrl-C") {
		t.Fatalf("print-url mode should not block:\n%s", out.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var uiLine, apiLine string
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "Hideout UI: "):
			uiLine = line
		case strings.HasPrefix(line, "Manager API: "):
			apiLine = line
		}
	}
	if !strings.Contains(uiLine, "#token=ui_") {
		t.Fatalf("ui line should carry fragment token:\n%s", out.String())
	}
	if strings.Contains(apiLine, "#token=") || strings.Contains(apiLine, "ui_") {
		t.Fatalf("manager API line must not carry UI token:\n%s", out.String())
	}
}

func TestUIRejectsPublicListenAddress(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{"ui", "--listen", "0.0.0.0:0", "--no-open", "--print-url"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected public bind to fail; stdout=%s", out.String())
	}
	if !strings.Contains(errOut.String(), "127.0.0.1") {
		t.Fatalf("unexpected stderr: %s", errOut.String())
	}
}

func TestTUIRendersTerminalDashboardWithoutStartingWebUI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	p := profile.Default("default")
	p.Tools.ExpectedCommands = []string{"agent-cli", "agent-helper"}
	p.CommandAdapters.Adapters = map[string]profile.CommandAdapter{
		"adapter": {
			Enabled:    true,
			Path:       "adapters/tool.js",
			Digest:     "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Entrypoint: "decideCommandAdapter",
			Commands:   []string{"tool-x"},
		},
	}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	envRec, err := (environment.Store{Root: store.Root}).Create(environment.Spec{
		Name:           "fixture-env-102",
		ImageRef:       environment.BuiltinBaseImage,
		Profile:        "default",
		Backend:        "lima",
		Workspace:      "/tmp/hideout-project",
		GuestWorkspace: "/tmp/hideout-project",
		ProfileID:      p.Metadata["profileId"],
		IdentityID:     p.Metadata["identityId"],
		InstanceName:   "hideout-default-env-test",
	})
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	envRec.Status = "running"
	envRec.LastCommand = "agent-cli"
	if err := (environment.Store{Root: store.Root}).Save(envRec); err != nil {
		t.Fatalf("save environment: %v", err)
	}
	sessionsDir := filepath.Join(store.Root, "sessions")
	for i := 1; i <= 12; i++ {
		sessionID := fmt.Sprintf("ses_20260704T0102%02dZ_%02d", i, i)
		sessionDir := filepath.Join(sessionsDir, sessionID)
		if err := os.MkdirAll(sessionDir, 0o700); err != nil {
			t.Fatalf("create session %s: %v", sessionID, err)
		}
		if i == 12 {
			if err := os.WriteFile(filepath.Join(sessionDir, "audit.jsonl"), []byte("{}\n"), 0o600); err != nil {
				t.Fatalf("write session audit: %v", err)
			}
			if err := os.MkdirAll(filepath.Join(sessionDir, "tmp"), 0o700); err != nil {
				t.Fatalf("create session runtime: %v", err)
			}
		}
	}
	var out, errOut bytes.Buffer
	code := Main([]string{"tui", "--once"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	for _, want := range []string{
		"Hideout TUI",
		"Store: " + store.Root,
		"Updated:",
		"Profiles: 1",
		"Environments: 1",
		"Sessions: 12",
		"Init Next:",
		"Smoke run: hideout run --profile default --backend lima -- pwd",
		"Capabilities: host.open",
		"Profiles\n  - default",
		"expected=agent-cli,agent-helper",
		"commandProxies=open,xdg-open",
		"commandAdapters=adapter:on(tool-x)",
		"next: tools=hideout profile tools default list",
		"next: expect-command=hideout profile tools default expected add <command>",
		"next: command-proxy=hideout profile command-proxy default list",
		"next: add-open=hideout profile command-proxy default add-open <command>",
		"Backends",
		"Network",
		"warning=direct exposes network identity",
		"Environments",
		"fixture-env-102",
		"image=template:_images/ubuntu-lts",
		"next: run=hideout run --env fixture-env-102 -- <command>  stop=hideout stop fixture-env-102  clean-after-stop=hideout clean --stopped fixture-env-102",
		"Sessions",
		"showing newest 10 of 12",
		"ses_20260704T010212Z_12",
		"privilege=unknown",
		"next: audit=hideout audit show --session ses_20260704T010212Z_12  cleanup-check=hideout cleanup --session ses_20260704T010212Z_12 --dry-run",
		"Recent Denied Audit",
		"Recent Audit",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("tui output missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "ses_20260704T010201Z_01") {
		t.Fatalf("tui output should omit oldest sessions when truncating:\n%s", out.String())
	}
	for _, forbidden := range []string{
		"Hideout UI:",
		"Manager API:",
		"ui_",
		"#token=",
	} {
		if strings.Contains(out.String(), forbidden) {
			t.Fatalf("tui output should not start or expose WebUI details %q:\n%s", forbidden, out.String())
		}
	}
}

func TestTUIProfileFilterScopesDashboardAndAudit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	for _, name := range []string{"agent", "smoke"} {
		if err := store.Save(profile.Default(name)); err != nil {
			t.Fatalf("save profile %s: %v", name, err)
		}
	}
	envStore := environment.Store{Root: store.Root}
	for _, spec := range []environment.Spec{
		{Name: "agent-env", ImageRef: environment.BuiltinBaseImage, Profile: "agent", Backend: "lima", Workspace: "/work/agent", GuestWorkspace: "/workspace", InstanceName: "hideout-agent-env"},
		{Name: "smoke-env", ImageRef: environment.BuiltinBaseImage, Profile: "smoke", Backend: "lima", Workspace: "/work/smoke", GuestWorkspace: "/workspace", InstanceName: "hideout-smoke-env"},
	} {
		rec, err := envStore.Create(spec)
		if err != nil {
			t.Fatalf("create environment for %s: %v", spec.Profile, err)
		}
		rec.Status = "ready"
		if err := envStore.Save(rec); err != nil {
			t.Fatalf("save environment for %s: %v", spec.Profile, err)
		}
	}
	sessionsDir := filepath.Join(store.Root, "sessions")
	sessionAudits := map[string]string{
		"ses_20260704T020101Z_agent": `{"time":"2026-07-04T02:01:01Z","session":"ses_20260704T020101Z_agent","profile":"agent","backend":"lima","action":"network.setup","decision":"allow"}`,
		"ses_20260704T020102Z_smoke": `{"time":"2026-07-04T02:01:02Z","session":"ses_20260704T020102Z_smoke","profile":"smoke","backend":"lima","action":"network.setup","decision":"allow"}`,
	}
	for id, line := range sessionAudits {
		if err := os.MkdirAll(filepath.Join(sessionsDir, id), 0o700); err != nil {
			t.Fatalf("create session %s: %v", id, err)
		}
		if err := os.WriteFile(filepath.Join(sessionsDir, id, "audit.jsonl"), []byte(line+"\n"), 0o600); err != nil {
			t.Fatalf("write audit for %s: %v", id, err)
		}
	}

	var out, errOut bytes.Buffer
	code := Main([]string{"tui", "--once", "--profile", "agent"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	for _, want := range []string{
		"Profile filter: agent",
		"Profiles: 1",
		"Environments: 1",
		"Sessions: 1",
		"Profiles\n  - agent",
		"commandProxies=open,xdg-open",
		"tools=hideout profile tools agent list",
		"add-open=hideout profile command-proxy agent add-open <command>",
		"agent-env",
		"ses_20260704T020101Z_agent  profile=agent",
		"Recent Audit\n  - agent  action=network.setup",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("filtered tui output missing %q:\n%s", want, out.String())
		}
	}
	for _, forbidden := range []string{
		"smoke-env",
		"ses_20260704T020102Z_smoke",
		"  - smoke  network=",
		"  - smoke  action=network.setup",
	} {
		if strings.Contains(out.String(), forbidden) {
			t.Fatalf("filtered tui output should omit %q:\n%s", forbidden, out.String())
		}
	}
}

func TestAuditShowFiltersAndRedactsEvents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	auditPath := filepath.Join(home, ".hideout", "sessions", "ses_audit", "audit.jsonl")
	mustWriteAppTest(t, auditPath, strings.Join([]string{
		`{"time":"2026-07-01T00:00:00Z","session":"ses_audit","profile":"default","backend":"lima","action":"host.open","decision":"allow","details":{"target":"https://user:pass@example.com/path?token=abc","capabilityToken":"cap_secret"}}`,
		`{"time":"2026-07-01T00:00:01Z","session":"ses_audit","profile":"default","backend":"lima","action":"host.fs.read","decision":"deny","details":{"path":"/Users/alice/.ssh/id_rsa","policyEffect":"none"}}`,
		`{"time":"2026-07-01T00:00:02Z","session":"ses_other","profile":"default","backend":"lima","action":"host.open","decision":"deny","details":{"target":"https://example.invalid"}}`,
	}, "\n")+"\n")

	var out, errOut bytes.Buffer
	code := Main([]string{"audit", "show", "--session", "ses_audit", "--action", "host.open"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut.String())
	}
	for _, want := range []string{
		"TIME\tSESSION\tPROFILE\tBACKEND\tACTION\tDECISION\tDETAILS",
		"ses_audit",
		"host.open",
		"allow",
		// User URL data is host-local evidence, shown verbatim.
		`https://user:pass@example.com/path?token=abc`,
		// Control-plane field stripped by its self-known name.
		`"capabilityToken":"REDACTED"`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("audit output missing %q:\n%s", want, out.String())
		}
	}
	for _, forbidden := range []string{"cap_secret", "host.fs.read", "ses_other"} {
		if strings.Contains(out.String(), forbidden) {
			t.Fatalf("audit output leaked or failed to filter %q:\n%s", forbidden, out.String())
		}
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"audit", "show", "--session", "ses_audit", "--decision", "deny", "--json"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut.String())
	}
	var events []map[string]any
	if err := json.Unmarshal(out.Bytes(), &events); err != nil {
		t.Fatalf("audit json should decode: %v\n%s", err, out.String())
	}
	if len(events) != 1 || events[0]["action"] != "host.fs.read" || events[0]["decision"] != "deny" {
		t.Fatalf("unexpected json events: %#v", events)
	}
	if strings.Contains(out.String(), "user:pass") || strings.Contains(out.String(), "cap_secret") {
		t.Fatalf("audit json leaked sensitive fields:\n%s", out.String())
	}
}

func TestTUIRejectsInvalidInterval(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{"tui", "--interval", "0s"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected invalid interval to fail; stdout=%s", out.String())
	}
	if !strings.Contains(errOut.String(), "--interval must be positive") {
		t.Fatalf("unexpected stderr: %s", errOut.String())
	}
}

func TestTUIOptionsDefaultToPersistentObserver(t *testing.T) {
	opts, err := parseTUIOptions(nil)
	if err != nil {
		t.Fatalf("parse default tui options: %v", err)
	}
	if !opts.watch || opts.once {
		t.Fatalf("default tui should watch until interrupted: %+v", opts)
	}
	if opts.interval != 2*time.Second {
		t.Fatalf("default interval=%s want 2s", opts.interval)
	}

	opts, err = parseTUIOptions([]string{"--once", "--profile", "agent"})
	if err != nil {
		t.Fatalf("parse once tui options: %v", err)
	}
	if opts.watch || !opts.once || opts.profileName != "agent" {
		t.Fatalf("--once should render one snapshot for the selected profile: %+v", opts)
	}
}

// Daemon-less fallback keeps the prior interval polling behavior. Typed daemon
// stream behavior is covered by app_liveconsole_test.go.
func TestWatchDashboardDaemonlessIntervalPolls(t *testing.T) {
	renders := 0
	ctx, cancel := context.WithTimeout(context.Background(), 160*time.Millisecond)
	defer cancel()
	if err := watchDashboard(ctx, nil, 20*time.Millisecond, func() error {
		renders++
		return nil
	}); err != nil {
		t.Fatalf("watchDashboard: %v", err)
	}
	if renders < 3 {
		t.Fatalf("daemon-less fallback should interval-poll, got %d renders", renders)
	}
}

// If a legacy refresh-signal stream is handed to watchDashboard and closes, it
// falls back to daemon-less interval polling.
func TestWatchDashboardFallsBackToIntervalWhenStreamCloses(t *testing.T) {
	eventCh := make(chan struct{})
	close(eventCh) // stream already ended
	renders := 0
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := watchDashboard(ctx, eventCh, 20*time.Millisecond, func() error {
		renders++
		return nil
	}); err != nil {
		t.Fatalf("watchDashboard: %v", err)
	}
	// After the close it polls on the 20ms interval; 200ms yields several renders.
	if renders < 3 {
		t.Fatalf("after stream close, want interval polling (>=3 renders), got %d", renders)
	}
}

func TestRunNativeExecutesWithWeakIsolationFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SERVICE_TOKEN", "audit-secret")
	t.Setenv("TERM", "xterm-256color")
	var out, errOut bytes.Buffer
	code := Main([]string{"run", "--backend", "native", "--allow-weak-isolation", "--", "sh", "-c", "printf '%s\n%s' \"$HOME\" \"$HOSTNAME\""}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), filepath.Join(home, ".hideout", "profiles", "default", "home")) {
		t.Fatalf("command did not see synthetic HOME: %q", out.String())
	}
	if !strings.Contains(out.String(), "devbox") {
		t.Fatalf("command did not see synthetic HOSTNAME: %q", out.String())
	}
	files, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "broker-endpoint.json"))
	if err != nil {
		t.Fatalf("glob endpoint files: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("automatic cleanup should remove broker endpoint files, got %v", files)
	}
	shimDirs, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "shims"))
	if err != nil {
		t.Fatalf("glob shim dirs: %v", err)
	}
	if len(shimDirs) != 0 {
		t.Fatalf("automatic cleanup should remove shim dirs, got %v", shimDirs)
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 1 {
		t.Fatalf("expected one audit file, got %d: %v", len(auditFiles), auditFiles)
	}
	auditData, err := os.ReadFile(auditFiles[0])
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	validateAuditJSONLWithSchema(t, auditFiles[0])
	if strings.Contains(string(auditData), `"toolPresets"`) {
		t.Fatalf("session.start audit must not include tool presets: %s", auditData)
	}
	for _, want := range []string{
		`"action":"backend.selected"`,
		`"action":"workspace.mapping"`,
		`"action":"env.policy"`,
		`"action":"command.start"`,
		`"action":"network.setup"`,
		`"action":"session.start"`,
		`"action":"session.end"`,
		`"action":"backend.cleanup"`,
		`"action":"session.cleanup"`,
		`"decision":"audit-only"`,
		`"authority":"guest.exec"`,
		`"route":"guest-direct"`,
		`"topLevel":true`,
		`"resolved":"native"`,
		`"weakIsolation":true`,
		`"proxyEnv":"absent"`,
		`"HOSTNAME"`,
		`"TERM"`,
	} {
		if !strings.Contains(string(auditData), want) {
			t.Fatalf("audit missing %q: %s", want, auditData)
		}
	}
	if strings.Contains(string(auditData), "audit-secret") {
		t.Fatalf("audit leaked denied env value: %s", auditData)
	}
	if strings.Contains(string(auditData), "SERVICE_TOKEN") {
		t.Fatalf("audit should not report non-inherited business env as denied: %s", auditData)
	}
	if strings.Contains(string(auditData), "<nil>") {
		t.Fatalf("session.end audit should not contain nil error string: %s", auditData)
	}
}

func TestRunRejectsUnsafeWorkspaceUnlessExplicitlyAllowed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var out, errOut bytes.Buffer
	code := Main([]string{"run", "--backend", "native", "--allow-weak-isolation", "--workspace", home, "--", "echo", "hi"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected unsafe workspace to fail; stdout=%s", out.String())
	}
	if !strings.Contains(errOut.String(), "--allow-unsafe-workspace") {
		t.Fatalf("unsafe workspace error missing override hint: %s", errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"run", "--backend", "native", "--allow-weak-isolation", "--allow-unsafe-workspace", "--workspace", home, "--", "echo", "hi"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("override run exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "hi") {
		t.Fatalf("override run output mismatch: %q", out.String())
	}
}

func TestRunNativeAcceptanceWorkspaceGitAndChildEnv(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HTTP_PROXY", "http://user:pass@proxy.invalid:8080")
	t.Setenv("HTTPS_PROXY", "http://user:pass@proxy.invalid:8443")
	t.Setenv("SERVICE_TOKEN", "service-secret")
	hostGitConfig := filepath.Join(t.TempDir(), "host.gitconfig")
	if err := os.WriteFile(hostGitConfig, []byte("[user]\n  email = real@example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", hostGitConfig)
	if err := os.WriteFile(filepath.Join(workspace, "input.txt"), []byte("workspace-read\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := `set -eu
actual_pwd=$(pwd -P)
expected_pwd=$(cd "$1" && pwd -P)
test "$actual_pwd" = "$expected_pwd"
printf 'read=%s\n' "$(cat input.txt)"
printf 'workspace-write\n' > output.txt
printf 'child_home=%s\n' "$(sh -c 'printf %s "$HOME"')"
printf 'tz=%s\n' "$TZ"
printf 'lang=%s\n' "$LANG"
printf 'lc_all=%s\n' "$LC_ALL"
printf 'xdg_config=%s\n' "$XDG_CONFIG_HOME"
printf 'xdg_cache=%s\n' "$XDG_CACHE_HOME"
printf 'xdg_data=%s\n' "$XDG_DATA_HOME"
home_config_real=$(cd "$HOME/.config" && pwd -P)
xdg_config_real=$(cd "$XDG_CONFIG_HOME" && pwd -P)
test "$home_config_real" = "$xdg_config_real"
home_cache_real=$(cd "$HOME/.cache" && pwd -P)
xdg_cache_real=$(cd "$XDG_CACHE_HOME" && pwd -P)
test "$home_cache_real" = "$xdg_cache_real"
home_data_real=$(cd "$HOME/.local/share" && pwd -P)
xdg_data_real=$(cd "$XDG_DATA_HOME" && pwd -P)
test "$home_data_real" = "$xdg_data_real"
if [ -n "${HTTP_PROXY:-}" ] || [ -n "${HTTPS_PROXY:-}" ] || [ -n "${SERVICE_TOKEN:-}" ]; then
  echo "sensitive env leaked to target" >&2
  exit 24
fi
child_sensitive_env=$(sh -c 'printf "%s|%s|%s" "${HTTP_PROXY:-}" "${HTTPS_PROXY:-}" "${SERVICE_TOKEN:-}"')
if [ "$child_sensitive_env" != "||" ]; then
  echo "sensitive env leaked to child: $child_sensitive_env" >&2
  exit 25
fi
printf 'sensitive_env_absent=yes\n'
printf 'git_config_global=%s\n' "$GIT_CONFIG_GLOBAL"
printf 'git_email=%s\n' "$(git config --global --get user.email)"
if [ -e "$HOME/.ssh" ]; then
  echo "fake HOME unexpectedly contains .ssh" >&2
  exit 23
fi
`
	var out, errOut bytes.Buffer
	code := Main([]string{
		"run",
		"--backend", "native",
		"--allow-weak-isolation",
		"--workspace", workspace,
		"--",
		"sh", "-c", script, "hideout-acceptance", workspace,
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	fakeHome := filepath.Join(home, ".hideout", "profiles", "default", "home")
	profileDir := filepath.Join(home, ".hideout", "profiles", "default")
	for _, want := range []string{
		"read=workspace-read",
		"child_home=" + fakeHome,
		"tz=UTC",
		"lang=en_US.UTF-8",
		"lc_all=en_US.UTF-8",
		"xdg_config=" + filepath.Join(profileDir, "config"),
		"xdg_cache=" + filepath.Join(profileDir, "cache"),
		"xdg_data=" + filepath.Join(profileDir, "data"),
		"sensitive_env_absent=yes",
		"git_config_global=" + filepath.Join(fakeHome, ".gitconfig"),
		"git_email=developer@example.com",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("stdout missing %q: %s", want, out.String())
		}
	}
	written, err := os.ReadFile(filepath.Join(workspace, "output.txt"))
	if err != nil {
		t.Fatalf("workspace output missing: %v", err)
	}
	if string(written) != "workspace-write\n" {
		t.Fatalf("workspace output mismatch: %q", written)
	}
	if _, err := os.Stat(filepath.Join(fakeHome, ".ssh")); !os.IsNotExist(err) {
		t.Fatalf("fake HOME .ssh should not exist by default, err=%v", err)
	}
}

func TestRunLimaDefaultReusesWorkspaceEnvironment(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	fakeBin := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "limactl.log")
	statePath := filepath.Join(t.TempDir(), "limactl.instances")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %q
if [ "$1" = "list" ] && [ "$2" = "--quiet" ]; then
  [ -f %q ] && cat %q
  exit 0
fi
if [ "$1" = "start" ] && [ "$3" = "--name" ]; then
  printf '%%s\n' "$4" > %q
fi
exit 0
`, logPath, statePath, statePath, statePath)
	if err := os.WriteFile(filepath.Join(fakeBin, "limactl"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	linuxShim := filepath.Join(fakeBin, "hideout-shim-linux")
	if err := os.WriteFile(linuxShim, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HIDEOUT_LINUX_SHIM_PATH", linuxShim)

	for i := 0; i < 2; i++ {
		var out, errOut bytes.Buffer
		code := Main([]string{
			"run",
			"--backend", "lima",
			"--workspace", workspace,
			"--",
			"sh", "-c", "true",
		}, &out, &errOut)
		if code != 0 {
			t.Fatalf("run %d exit=%d stdout=%s stderr=%s", i+1, code, out.String(), errOut.String())
		}
	}

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake limactl log: %v", err)
	}
	log := string(logData)
	startLines := limaStartLines(log)
	if len(startLines) != 2 {
		t.Fatalf("expected two lima start calls, got %d:\n%s", len(startLines), log)
	}
	starts := limaStartInstanceNames(log)
	if len(starts) != 1 || !strings.Contains(starts[0], "-env-") {
		t.Fatalf("first default lima run should create one environment instance, starts=%v log=\n%s", starts, log)
	}
	if startLines[1] != "start --tty=false "+starts[0] {
		t.Fatalf("second default lima run should start existing environment by name, starts=%v", startLines)
	}
	if strings.Contains(log, "delete -f") {
		t.Fatalf("default reusable lima environment should not be deleted after run:\n%s", log)
	}

	envStore := environment.Store{Root: filepath.Join(home, ".hideout")}
	records, err := envStore.List()
	if err != nil {
		t.Fatalf("list environments: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected one environment record, got %d: %+v", len(records), records)
	}
	if records[0].Status != "ready" || records[0].LastCommand != "sh -c true" || records[0].LastSessionID == "" {
		t.Fatalf("environment metadata not updated: %+v", records[0])
	}
	if entries, err := os.ReadDir(envStore.ShimDir(records[0].ID)); err != nil {
		t.Fatalf("read environment shim dir: %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("environment runtime shims should be cleared after run, got %v", entries)
	}

	var out, errOut bytes.Buffer
	code := Main([]string{"env", "list"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("env list exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), records[0].ID) || !strings.Contains(out.String(), records[0].Name) {
		t.Fatalf("env list output missing environment metadata:\n%s", out.String())
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"stop", records[0].ID}, &out, &errOut)
	if code != 0 {
		t.Fatalf("stop exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "stopped: "+records[0].ID) {
		t.Fatalf("stop output missing stopped environment:\n%s", out.String())
	}
	stopped, err := envStore.Load(records[0].ID)
	if err != nil {
		t.Fatalf("load stopped environment: %v", err)
	}
	if stopped.Status != "stopped" {
		t.Fatalf("stop should mark environment stopped, got %+v", stopped)
	}
	logData, err = os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake limactl log after stop: %v", err)
	}
	if !strings.Contains(string(logData), "stop "+starts[0]) {
		t.Fatalf("stop should stop reusable lima instance:\n%s", logData)
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"clean", "--stopped", records[0].ID}, &out, &errOut)
	if code != 0 {
		t.Fatalf("clean exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "removed: "+records[0].ID) {
		t.Fatalf("clean output missing removed environment:\n%s", out.String())
	}
	logData, err = os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake limactl log after clean: %v", err)
	}
	if !strings.Contains(string(logData), "delete -f "+starts[0]) {
		t.Fatalf("clean should delete reusable lima instance:\n%s", logData)
	}
	records, err = envStore.List()
	if err != nil {
		t.Fatalf("list environments after clean: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("clean should remove environment records, got %+v", records)
	}
}

func TestStopAndCleanIdleFilters(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := environment.Store{Root: filepath.Join(home, ".hideout")}
	now := time.Now().UTC()
	old, err := store.Create(environment.Spec{
		Name:           "fixture-env-103",
		ImageRef:       environment.BuiltinBaseImage,
		Profile:        "default",
		Backend:        "lima",
		Workspace:      t.TempDir(),
		GuestWorkspace: "/workspace",
		InstanceName:   "hideout-old",
	})
	if err != nil {
		t.Fatalf("Create old: %v", err)
	}
	old.Status = "ready"
	old.LastEndedAt = now.Add(-2 * time.Hour)
	if err := store.Save(old); err != nil {
		t.Fatalf("Save old: %v", err)
	}
	recent, err := store.Create(environment.Spec{
		Name:           "fixture-env-104",
		ImageRef:       environment.BuiltinBaseImage,
		Profile:        "default",
		Backend:        "lima",
		Workspace:      t.TempDir(),
		GuestWorkspace: "/workspace",
		InstanceName:   "hideout-recent",
	})
	if err != nil {
		t.Fatalf("Create recent: %v", err)
	}
	recent.Status = "ready"
	recent.LastEndedAt = now.Add(-5 * time.Minute)
	if err := store.Save(recent); err != nil {
		t.Fatalf("Save recent: %v", err)
	}
	running, err := store.Create(environment.Spec{
		Name:           "fixture-env-105",
		ImageRef:       environment.BuiltinBaseImage,
		Profile:        "default",
		Backend:        "lima",
		Workspace:      t.TempDir(),
		GuestWorkspace: "/workspace",
		InstanceName:   "hideout-running",
	})
	if err != nil {
		t.Fatalf("Create running: %v", err)
	}
	running.Status = "running"
	running.LastEndedAt = now.Add(-3 * time.Hour)
	if err := store.Save(running); err != nil {
		t.Fatalf("Save running: %v", err)
	}

	var out, errOut bytes.Buffer
	code := Main([]string{"stop", "--dry-run", "--idle", "1h"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("stop --idle exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "would stop: "+old.ID) ||
		strings.Contains(out.String(), recent.ID) ||
		strings.Contains(out.String(), running.ID) {
		t.Fatalf("stop --idle selected wrong environments:\n%s", out.String())
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"clean", "--dry-run", "--idle", "1h"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("clean --idle exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "would remove: "+old.ID) ||
		strings.Contains(out.String(), recent.ID) ||
		strings.Contains(out.String(), running.ID) {
		t.Fatalf("clean --idle selected wrong environments:\n%s", out.String())
	}
}

func TestSelectRunEnvironmentRemoveStaysRecordless(t *testing.T) {
	store := environment.Store{Root: t.TempDir()}
	p := profile.Default("default")
	opts := runOptions{
		workspace:      t.TempDir(),
		guestWorkspace: "/workspace",
	}
	opts.removeEnvironment = true
	selected, err := selectRunEnvironment(store, p, "lima", opts, true)
	if err != nil {
		t.Fatalf("selectRunEnvironment: %v", err)
	}
	if selected.Active {
		t.Fatalf("--rm runs stay record-less/disposable: %+v", selected)
	}
}

func TestRunLimaReturnsAndAuditsBackendCleanupFailure(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	fakeBin := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "limactl.log")
	limactl := filepath.Join(fakeBin, "limactl")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %q
case "$*" in
  *'/network/cleanup.sh'*)
    echo 'cleanup failed' >&2
    exit 37
    ;;
esac
exit 0
`, logPath)
	if err := os.WriteFile(limactl, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	linuxShim := filepath.Join(fakeBin, "hideout-shim-linux")
	if err := os.WriteFile(linuxShim, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HIDEOUT_LINUX_SHIM_PATH", linuxShim)

	var out, errOut bytes.Buffer
	code := Main([]string{
		"run",
		"--backend", "lima",
		"--rm",
		"--workspace", workspace,
		"--",
		"sh", "-c", "true",
	}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected cleanup failure exit; stdout=%s stderr=%s", out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "exit status 37") {
		t.Fatalf("stderr should report cleanup failure, got %s", errOut.String())
	}
	calls, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake limactl log: %v", err)
	}
	if !strings.Contains(string(calls), "/hideout/session/network/cleanup.sh") {
		t.Fatalf("fake limactl did not receive cleanup call: %s", calls)
	}
	if !strings.Contains(string(calls), "delete -f hideout-") {
		t.Fatalf("fake limactl did not receive delete call after cleanup failure: %s", calls)
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 1 {
		t.Fatalf("expected one audit file, got %d: %v", len(auditFiles), auditFiles)
	}
	validateAuditJSONLWithSchema(t, auditFiles[0])
	auditData, err := os.ReadFile(auditFiles[0])
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	for _, want := range []string{
		`"action":"session.end"`,
		`"decision":"allow"`,
		`"action":"backend.cleanup"`,
		`"decision":"error"`,
		`"error":"network cleanup: exit status 37"`,
		`"action":"session.cleanup"`,
	} {
		if !strings.Contains(string(auditData), want) {
			t.Fatalf("audit missing %q: %s", want, auditData)
		}
	}
}

func TestRunLimaTun2SocksFailsClosedWithoutSetupIdentity(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	fakeBin := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "limactl.log")
	limactl := filepath.Join(fakeBin, "limactl")
	script := fakeLimactlScript(logPath, "exit 0")
	if err := os.WriteFile(limactl, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	linuxShim := filepath.Join(fakeBin, "hideout-shim-linux")
	if err := os.WriteFile(linuxShim, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HTTP_PROXY", "http://user:pass@proxy.invalid:8080")
	t.Setenv("HTTPS_PROXY", "http://user:pass@proxy.invalid:8443")
	t.Setenv("HIDEOUT_SECRET_DEFAULT_PROXY", "socks5://127.0.0.1:1080")
	t.Setenv("HIDEOUT_LINUX_SHIM_PATH", linuxShim)

	var out, errOut bytes.Buffer
	code := Main([]string{
		"run",
		"--backend", "lima",
		"--workspace", workspace,
		"--network", "tun2socks",
		"--proxy-secret", "default-proxy",
		"--mediated-resolver", "1.1.1.1",
		"--",
		"sh", "-c", "true",
	}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected setup identity failure; stdout=%s stderr=%s", out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "privileged setup identity is unavailable") {
		t.Fatalf("stderr should explain setup identity failure, got %s", errOut.String())
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake limactl log: %v", err)
	}
	log := string(logData)
	if strings.Contains(log, "HTTP_PROXY=") || strings.Contains(log, "HTTPS_PROXY=") || strings.Contains(log, "socks5://127.0.0.1:1080") {
		t.Fatalf("lima shell args leaked proxy env or proxy secret:\n%s", log)
	}
	if strings.Contains(log, "/hideout/session/network/bootstrap.sh") || strings.Contains(log, "hideout-command-check sh") || strings.Contains(log, "sh -c true") {
		t.Fatalf("target/network setup should not run after setup identity failure:\n%s", log)
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 1 {
		t.Fatalf("expected one audit file, got %d: %v", len(auditFiles), auditFiles)
	}
	validateAuditJSONLWithSchema(t, auditFiles[0])
	networkEvent := lastAuditEventByActionForAppTest(t, auditFiles[0], "network.setup")
	if networkEvent.Decision != "audit-only" || networkEvent.Details["mode"] != "tun2socks" || networkEvent.Details["proxySecretRef"] != "default-proxy" {
		t.Fatalf("network.setup audit mismatch: %+v", networkEvent)
	}
	localBypass, ok := networkEvent.Details["localBypass"].([]any)
	if !ok || len(localBypass) != 1 || localBypass[0] != "host.lima.internal" {
		t.Fatalf("network.setup audit missing lima local bypass: %+v", networkEvent.Details)
	}
	setupEvent := lastAuditEventByActionForAppTest(t, auditFiles[0], "hideout.privileged_setup")
	if setupEvent.Decision != "error" || setupEvent.Details["category"] != "network" || setupEvent.Details["status"] != "failed" {
		t.Fatalf("privileged setup failure audit mismatch: %+v", setupEvent)
	}
	auditData, err := os.ReadFile(auditFiles[0])
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if strings.Contains(string(auditData), "user:pass") || strings.Contains(string(auditData), "socks5://127.0.0.1:1080") {
		t.Fatalf("audit leaked proxy secret: %s", auditData)
	}
}

func TestRunAliasPathModeAuditsNeutralGuestWorkspace(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	p := profile.Default("alias-workspace")
	p.Workspace.PathMode = "alias"
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{
		"run",
		"--profile", "alias-workspace",
		"--backend", "native",
		"--allow-weak-isolation",
		"--workspace", workspace,
		"--",
		"sh", "-c", "printf ok",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 1 {
		t.Fatalf("expected one audit file, got %d: %v", len(auditFiles), auditFiles)
	}
	auditData, err := os.ReadFile(auditFiles[0])
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	validateAuditJSONLWithSchema(t, auditFiles[0])
	for _, want := range []string{
		`"action":"workspace.mapping"`,
		`"host":"` + workspace + `"`,
		`"guest":"/workspace"`,
		`"pathMode":"alias"`,
		`"guestWork":"/workspace"`,
	} {
		if !strings.Contains(string(auditData), want) {
			t.Fatalf("audit missing %q: %s", want, auditData)
		}
	}
}

func TestRunNativeEphemeralUsesSessionLocalIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{
		"run",
		"--backend", "native",
		"--allow-weak-isolation",
		"--ephemeral",
		"--",
		"sh", "-c", "identity_root=$(dirname \"$HOME\"); printf 'HOME=%s\n' \"$HOME\"; printf 'MACHINE=%s\n' \"$(cat \"$identity_root/machine/machine-id\")\"; test -f \"$HOME/.gitconfig\"; touch \"$HOME/session-token\"",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	values := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(out.String()))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if ok {
			values[key] = value
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan stdout: %v", err)
	}
	homeLine := values["HOME"]
	if !strings.Contains(homeLine, filepath.Join(home, ".hideout", "sessions")) || !strings.Contains(homeLine, filepath.Join("identity", "home")) {
		t.Fatalf("ephemeral run should use session-local HOME, got stdout %q", out.String())
	}
	if strings.Contains(homeLine, filepath.Join(home, ".hideout", "profiles", "default", "home")) {
		t.Fatalf("ephemeral run used persistent profile home: %q", out.String())
	}
	sessionMachineID := strings.TrimSpace(values["MACHINE"])
	if sessionMachineID == "" {
		t.Fatalf("ephemeral run did not print session machine-id: %q", out.String())
	}
	persistentMachine, err := os.ReadFile(filepath.Join(home, ".hideout", "profiles", "default", "machine", "machine-id"))
	if err != nil {
		t.Fatalf("read persistent machine-id: %v", err)
	}
	persistentMachineID := strings.TrimSpace(string(persistentMachine))
	if sessionMachineID == persistentMachineID {
		t.Fatalf("ephemeral run reused persistent machine-id %q", sessionMachineID)
	}
	loaded, err := (profile.Store{Root: filepath.Join(home, ".hideout")}).Load("default")
	if err != nil {
		t.Fatalf("load persistent profile: %v", err)
	}
	if loaded.Metadata["machineId"] != persistentMachineID {
		t.Fatalf("persistent profile machine-id changed: metadata=%+v file=%q", loaded.Metadata, persistentMachineID)
	}
	if loaded.Metadata["machineId"] == sessionMachineID {
		t.Fatalf("ephemeral run wrote session machine-id back to persistent profile: %+v", loaded.Metadata)
	}
	if _, err := os.Stat(filepath.Join(home, ".hideout", "profiles", "default", "home", "session-token")); !os.IsNotExist(err) {
		t.Fatalf("ephemeral run should not write marker to persistent profile home; err=%v", err)
	}
	identityDirs, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "identity"))
	if err != nil {
		t.Fatalf("glob identity dirs: %v", err)
	}
	if len(identityDirs) != 0 {
		t.Fatalf("automatic cleanup should remove ephemeral identity dirs, got %v", identityDirs)
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 1 {
		t.Fatalf("expected one audit file, got %d: %v", len(auditFiles), auditFiles)
	}
	validateAuditJSONLWithSchema(t, auditFiles[0])
	envPolicy := lastAuditEventByActionForAppTest(t, auditFiles[0], "env.policy")
	if envPolicy.Details["identityMode"] != "ephemeral" ||
		envPolicy.Details["lineageMode"] != "session-fork" ||
		envPolicy.Details["sourceIdentityId"] != loaded.Metadata["identityId"] {
		t.Fatalf("audit missing ephemeral identity lineage: %+v persistent=%+v", envPolicy.Details, loaded.Metadata)
	}
	if envPolicy.Details["identityId"] == "" || envPolicy.Details["identityId"] == loaded.Metadata["identityId"] {
		t.Fatalf("audit identityId should be session identity, not persistent profile identity: %+v persistent=%+v", envPolicy.Details, loaded.Metadata)
	}
}

func TestRunScopedEnvIsUserControlledAndValidated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{"run", "--backend", "native", "--allow-weak-isolation", "--env-var", "TEST_CLI_VISIBLE=1", "--", "sh", "-c", "printf '%s' \"$TEST_CLI_VISIBLE\""}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut.String())
	}
	if out.String() != "1" {
		t.Fatalf("target did not see run-scoped env: %q", out.String())
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 1 {
		t.Fatalf("expected one audit file, got %d: %v", len(auditFiles), auditFiles)
	}
	envPolicy := lastAuditEventByActionForAppTest(t, auditFiles[0], "env.policy")
	public, ok := envPolicy.Details["public"].([]any)
	if !ok || len(public) != 1 || public[0] != "TEST_CLI_VISIBLE" {
		t.Fatalf("env.policy audit missing run-scoped public env: %+v", envPolicy.Details)
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"run", "--backend", "native", "--allow-weak-isolation", "--env-var", "HIDEOUT_STORE_ROOT=/tmp/store", "--", "sh", "-c", "true"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("reserved run-scoped env should fail; stdout=%s", out.String())
	}
	if !strings.Contains(errOut.String(), "env.public must not expose hideout runtime env") {
		t.Fatalf("reserved env failure should come from profile validation, got %s", errOut.String())
	}
}

func TestRunRespectsProfileAuditDisabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	p := profile.Default("quiet")
	p.Audit.Enabled = false
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{
		"run",
		"--profile", "quiet",
		"--backend", "native",
		"--allow-weak-isolation",
		"--",
		"sh", "-c", "printf ok",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 0 {
		t.Fatalf("profile audit disabled should not create audit files, got %v", auditFiles)
	}
	out.Reset()
	errOut.Reset()
	code = Main([]string{"explain", "--profile", "quiet", "--backend", "native", "--", "echo", "hi"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("explain exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Audit: off") {
		t.Fatalf("explain should show profile audit disabled:\n%s", out.String())
	}
}

func TestRunAuditFlagOverridesProfileAuditDisabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	p := profile.Default("quiet")
	p.Audit.Enabled = false
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	var out, errOut bytes.Buffer
	code := Main([]string{
		"run",
		"--profile", "quiet",
		"--backend", "native",
		"--allow-weak-isolation",
		"--audit", auditPath,
		"--",
		"sh", "-c", "printf ok",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	if _, err := os.Stat(auditPath); err != nil {
		t.Fatalf("explicit audit path should be written: %v", err)
	}
	validateAuditJSONLWithSchema(t, auditPath)
}

func TestRunAuditOffOverridesProfileAuditEnabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{
		"run",
		"--backend", "native",
		"--allow-weak-isolation",
		"--audit", "off",
		"--",
		"sh", "-c", "printf ok",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 0 {
		t.Fatalf("--audit off should not create audit files, got %v", auditFiles)
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"explain", "--backend", "native", "--audit", "off", "--", "echo", "hi"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("explain exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Audit: off") {
		t.Fatalf("explain should show explicit audit off:\n%s", out.String())
	}
}

func TestRunNativeAuditRecordsCommandVerbatim(t *testing.T) {
	// The target command and its arguments are user data recorded verbatim in
	// the host-local audit file. Core does not guess which flag values are
	// secrets; redacting them is the operator's audit.redact policy or the
	// export boundary, not storage-time heuristics.
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	code := Main([]string{"run", "--backend", "native", "--allow-weak-isolation", "--", "sh", "-c", "printf ok", "--token", "abc123"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut.String())
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 1 {
		t.Fatalf("expected one audit file, got %d: %v", len(auditFiles), auditFiles)
	}
	auditData, err := os.ReadFile(auditFiles[0])
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if !strings.Contains(string(auditData), "abc123") {
		t.Fatalf("local audit should preserve command argument verbatim: %s", auditData)
	}
	events := auditEventsByActionForAppTest(t, auditFiles[0])
	commandStart := events["command.start"]
	argv, ok := commandStart.Details["argv"].([]any)
	if !ok || len(argv) < 5 || argv[4] != "abc123" {
		t.Fatalf("command.start argv should be recorded verbatim: %+v", commandStart.Details)
	}
	sessionEnd := events["session.end"]
	command, _ := sessionEnd.Details["command"].(string)
	if !strings.Contains(command, "--token abc123") {
		t.Fatalf("session.end command should be recorded verbatim: %+v", sessionEnd.Details)
	}
}

func TestRunNativeOpenUsesBrokerShim(t *testing.T) {
	shimPath := buildShim(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HIDEOUT_SHIM_PATH", shimPath)
	t.Setenv("HIDEOUT_OPEN_DRY_RUN", "1")
	var out, errOut bytes.Buffer
	code := Main([]string{
		"run",
		"--backend", "native",
		"--allow-weak-isolation",
		"--",
		"sh", "-c", "open https://example.com",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".hideout", "profiles", "default", "browser")); err != nil {
		t.Fatalf("isolated browser profile dir missing: %v", err)
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 1 {
		t.Fatalf("expected one audit file, got %d: %v", len(auditFiles), auditFiles)
	}
	auditData, err := os.ReadFile(auditFiles[0])
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	validateAuditJSONLWithSchema(t, auditFiles[0])
	events := readAuditEventsForAppTest(t, auditFiles[0])
	eventsByAction := map[string]auditEventForAppTest{}
	for _, event := range events {
		eventsByAction[event.Action] = event
	}
	for _, action := range []string{"session.start", "workspace.mapping", "command.start", "network.setup", "host.open"} {
		if _, ok := eventsByAction[action]; !ok {
			t.Fatalf("audit missing %s event: %+v", action, events)
		}
	}
	commandStart := eventsByAction["command.start"]
	if commandStart.Decision != "audit-only" ||
		commandStart.Details["program"] != "sh" ||
		commandStart.Details["authority"] != "guest.exec" ||
		commandStart.Details["route"] != "guest-direct" ||
		commandStart.Details["topLevel"] != true {
		t.Fatalf("command.start audit does not prove top-level guest execution: %+v", commandStart)
	}
	workspaceEvent := eventsByAction["workspace.mapping"]
	if workspaceEvent.Decision != "allow" || workspaceEvent.Details["workspaceVisible"] != true || workspaceEvent.Details["readWrite"] != true {
		t.Fatalf("workspace mapping audit does not prove read/write visible workspace: %+v", workspaceEvent)
	}
	networkEvent := eventsByAction["network.setup"]
	if networkEvent.Decision != "allow" || networkEvent.Details["mode"] != "direct" || networkEvent.Details["verified"] != true {
		t.Fatalf("network audit does not prove direct verified setup: %+v", networkEvent)
	}
	sessionStart := eventsByAction["session.start"]
	if sessionStart.Decision != "allow" ||
		sessionStart.Details["workspace"] == "" ||
		sessionStart.Details["guestWork"] == "" ||
		sessionStart.Details["brokerEndpoint"] != "present" ||
		sessionStart.Details["brokerTransport"] == "" {
		t.Fatalf("session.start audit missing workspace or broker presence details: %+v", sessionStart)
	}
	for _, leaked := range []string{"tcp://", "unix://", "broker.sock", "broker-endpoint.json", "network-plan.json", filepath.Join(home, ".hideout", "profiles", "default", "browser")} {
		if strings.Contains(string(auditData), leaked) {
			t.Fatalf("audit leaked control-plane detail %q: %s", leaked, auditData)
		}
	}
	hostOpen := eventsByAction["host.open"]
	if hostOpen.Decision != "allow" ||
		hostOpen.Details["resourceType"] != "url" ||
		hostOpen.Details["browserProfileMode"] != "isolated" ||
		hostOpen.Details["browserProfile"] != "present" {
		t.Fatalf("host.open audit does not prove isolated browser profile: %+v", hostOpen)
	}
	if hostOpen.Details["portBridge"] != "none" ||
		hostOpen.Details["browserControl"] != "disabled" ||
		hostOpen.Details["remoteDebugging"] != "not-exposed" {
		t.Fatalf("host.open audit does not prove browser control channels stayed closed: %+v", hostOpen)
	}
	for _, want := range []string{`"subject":"command:open"`, `"command":"open"`, `"route":"host-broker"`, `"argv":["open","https://example.com"]`} {
		if !strings.Contains(string(auditData), want) {
			t.Fatalf("audit missing command proxy metadata %q: %s", want, auditData)
		}
	}
}

func TestRunNativeOpenRejectsHostLocalBrowserURL(t *testing.T) {
	shimPath := buildShim(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HIDEOUT_SHIM_PATH", shimPath)
	t.Setenv("HIDEOUT_OPEN_DRY_RUN", "1")

	var out, errOut bytes.Buffer
	code := Main([]string{
		"run",
		"--backend", "native",
		"--allow-weak-isolation",
		"--",
		"sh", "-c", "open http://127.0.0.1:3000",
	}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected host-local URL open to fail; stdout=%s stderr=%s", out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "profile policy") {
		t.Fatalf("stderr missing profile policy denial:\n%s", errOut.String())
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 1 {
		t.Fatalf("expected one audit file, got %d: %v", len(auditFiles), auditFiles)
	}
	hostOpen := lastAuditEventByActionForAppTest(t, auditFiles[0], "host.open")
	if hostOpen.Decision != "deny" || hostOpen.Details["target"] != "http://127.0.0.1:3000" {
		t.Fatalf("host.open audit should deny host-local URL before opener: %+v", hostOpen)
	}
	if _, ok := hostOpen.Details["browserProfileMode"]; ok {
		t.Fatalf("host-local URL should fail before opener/browser launch details: %+v", hostOpen)
	}
	if _, ok := hostOpen.Details["browserProfile"]; ok {
		t.Fatalf("host-local URL should not report browser profile launch: %+v", hostOpen)
	}
	if !strings.Contains(fmt.Sprint(hostOpen.Details["error"]), "profile policy") {
		t.Fatalf("host.open audit missing profile policy reason: %+v", hostOpen)
	}
}

func TestRunNativeOpenAllowsMappedWorkspaceFile(t *testing.T) {
	shimPath := buildShim(t)
	home := t.TempDir()
	workspace := t.TempDir()
	mustWriteAppTest(t, filepath.Join(workspace, "doc.txt"), "workspace file")
	t.Setenv("HOME", home)
	t.Setenv("HIDEOUT_SHIM_PATH", shimPath)
	t.Setenv("HIDEOUT_OPEN_DRY_RUN", "1")

	var out, errOut bytes.Buffer
	code := Main([]string{
		"run",
		"--backend", "native",
		"--allow-weak-isolation",
		"--workspace", workspace,
		"--",
		"sh", "-c", "open ./doc.txt",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 1 {
		t.Fatalf("expected one audit file, got %d: %v", len(auditFiles), auditFiles)
	}
	validateAuditJSONLWithSchema(t, auditFiles[0])
	hostOpen := lastAuditEventByActionForAppTest(t, auditFiles[0], "host.open")
	if hostOpen.Decision != "allow" || hostOpen.Details["resourceType"] != "workspace-file" {
		t.Fatalf("host.open did not allow mapped workspace file: %+v", hostOpen)
	}
	wantHostPath, err := filepath.EvalSymlinks(filepath.Join(workspace, "doc.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if hostOpen.Details["hostPath"] != wantHostPath {
		t.Fatalf("host.open mapped wrong host path: %+v", hostOpen)
	}
}

func TestRunNativeOpenRejectsUnmappedFile(t *testing.T) {
	shimPath := buildShim(t)
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HIDEOUT_SHIM_PATH", shimPath)
	t.Setenv("HIDEOUT_OPEN_DRY_RUN", "1")

	var out, errOut bytes.Buffer
	code := Main([]string{
		"run",
		"--backend", "native",
		"--allow-weak-isolation",
		"--workspace", workspace,
		"--",
		"sh", "-c", "open /etc/passwd",
	}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected unmapped file open to fail; stdout=%s stderr=%s", out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "outside workspace") {
		t.Fatalf("stderr missing outside-workspace denial:\n%s", errOut.String())
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 1 {
		t.Fatalf("expected one audit file, got %d: %v", len(auditFiles), auditFiles)
	}
	validateAuditJSONLWithSchema(t, auditFiles[0])
	hostOpen := lastAuditEventByActionForAppTest(t, auditFiles[0], "host.open")
	if hostOpen.Decision != "deny" {
		t.Fatalf("host.open did not deny unmapped file: %+v", hostOpen)
	}
	if !strings.Contains(stringValueForAppTest(hostOpen.Details["target"]), "/etc/passwd") {
		t.Fatalf("host.open audit missing rejected target: %+v", hostOpen)
	}
}

func TestRunNativeOpenUsesUniqueBrokerRequestIDs(t *testing.T) {
	shimPath := buildShim(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HIDEOUT_SHIM_PATH", shimPath)
	t.Setenv("HIDEOUT_OPEN_DRY_RUN", "1")
	var out, errOut bytes.Buffer
	code := Main([]string{
		"run",
		"--backend", "native",
		"--allow-weak-isolation",
		"--",
		"sh", "-c", "open https://example.com/one && open https://example.com/two",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 1 {
		t.Fatalf("expected one audit file, got %d: %v", len(auditFiles), auditFiles)
	}
	validateAuditJSONLWithSchema(t, auditFiles[0])
	ids := hostOpenRequestIDs(t, auditFiles[0])
	if len(ids) != 2 {
		t.Fatalf("expected two host.open request IDs, got %v", ids)
	}
	if ids[0] == ids[1] {
		t.Fatalf("host.open request IDs should be unique, got %v", ids)
	}
	for _, id := range ids {
		if !strings.HasPrefix(id, "req_") {
			t.Fatalf("request ID %q missing req_ prefix", id)
		}
	}
}

func TestRunNativeRejectsDisabledCommandProxyShim(t *testing.T) {
	shimPath := buildShim(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HIDEOUT_SHIM_PATH", shimPath)
	t.Setenv("HIDEOUT_OPEN_DRY_RUN", "1")
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	p := profile.Default("open-only")
	delete(p.CommandProxy.Commands, "xdg-open")
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{
		"run",
		"--profile", "open-only",
		"--backend", "native",
		"--allow-weak-isolation",
		"--",
		"sh", "-c", `"$1" xdg-open https://example.com`, "hideout-shim-test", shimPath,
	}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected disabled command proxy to fail; stdout=%s stderr=%s", out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), `broker request command "xdg-open" is not enabled by profile`) {
		t.Fatalf("stderr missing disabled-command denial:\n%s", errOut.String())
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 1 {
		t.Fatalf("expected one audit file, got %d: %v", len(auditFiles), auditFiles)
	}
	auditData, err := os.ReadFile(auditFiles[0])
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	validateAuditJSONLWithSchema(t, auditFiles[0])
	for _, want := range []string{
		`"action":"host.open"`,
		`"decision":"deny"`,
		`"subject":"command:xdg-open"`,
		`"command":"xdg-open"`,
	} {
		if !strings.Contains(string(auditData), want) {
			t.Fatalf("audit missing %q: %s", want, auditData)
		}
	}
}

func TestRunNativeUsesProfileCommandProxyManagedSymbol(t *testing.T) {
	shimPath := buildShim(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HIDEOUT_SHIM_PATH", shimPath)
	t.Setenv("HIDEOUT_OPEN_DRY_RUN", "1")

	var profileOut, profileErr bytes.Buffer
	if code := Main([]string{"profile", "command-proxy", "custom-open", "add-open", "browser-open"}, &profileOut, &profileErr); code != 0 {
		t.Fatalf("command-proxy add-open exit=%d stderr=%s", code, profileErr.String())
	}

	var out, errOut bytes.Buffer
	code := Main([]string{
		"run",
		"--profile", "custom-open",
		"--backend", "native",
		"--allow-weak-isolation",
		"--",
		"sh", "-c", `"$1" browser-open https://example.com/configured`, "hideout-shim-test", shimPath,
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 1 {
		t.Fatalf("expected one audit file, got %d: %v", len(auditFiles), auditFiles)
	}
	validateAuditJSONLWithSchema(t, auditFiles[0])
	auditData, err := os.ReadFile(auditFiles[0])
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	for _, want := range []string{
		`"action":"host.open"`,
		`"decision":"allow"`,
		`"subject":"command:browser-open"`,
		`"command":"browser-open"`,
		`"argv":["browser-open","https://example.com/configured"]`,
	} {
		if !strings.Contains(string(auditData), want) {
			t.Fatalf("audit missing configured command proxy metadata %q: %s", want, auditData)
		}
	}
}

func TestRunNativeOpenAuditsPolicyScriptParticipation(t *testing.T) {
	shimPath := buildShim(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HIDEOUT_SHIM_PATH", shimPath)
	store := profile.Store{Root: filepath.Join(home, ".hideout")}
	p := profile.Default("scripted-open")
	p.Policy.ScriptRefs = []profile.ScriptRef{{
		ID:          "deny-open",
		Path:        "policy/deny.js",
		Entrypoints: []string{"decideCommand"},
	}}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(store.ProfileDir("scripted-open"), "policy", "deny.js")
	if err := os.WriteFile(scriptPath, []byte("function decideCommand(ctx) { return hideout.decision.deny({ route: 'deny', action: 'host.open', resources: ['url:https'], reason: 'script denied' }); }"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Main([]string{
		"run",
		"--backend", "native",
		"--allow-weak-isolation",
		"--profile", "scripted-open",
		"--",
		"sh", "-c", "open https://example.com",
	}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected script denial to fail run; stdout=%s stderr=%s", out.String(), errOut.String())
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 1 {
		t.Fatalf("expected one audit file, got %d: %v", len(auditFiles), auditFiles)
	}
	auditData, err := os.ReadFile(auditFiles[0])
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	for _, want := range []string{
		`"policyScripts"`,
		`"id":"deny-open"`,
		`"entrypoint":"decideCommand"`,
		`"sha256":"`,
		`"decision":"deny"`,
		`"reason":"script denied"`,
	} {
		if !strings.Contains(string(auditData), want) {
			t.Fatalf("audit missing script metadata %q: %s", want, auditData)
		}
	}
}

func TestTun2SocksFailsClosedBeforeCommandRuns(t *testing.T) {
	home := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	t.Setenv("HOME", home)
	t.Setenv("HIDEOUT_SECRET_DEFAULT_PROXY", "socks5://user:pass@127.0.0.1:1080")
	var out, errOut bytes.Buffer
	code := Main([]string{
		"run",
		"--backend", "native",
		"--allow-weak-isolation",
		"--network", "tun2socks",
		"--proxy-secret", "default-proxy",
		"--mediated-resolver", "1.1.1.1",
		"--",
		"sh", "-c", "touch " + marker,
	}, &out, &errOut)
	if code == 0 {
		t.Fatal("expected tun2socks to fail closed before verification")
	}
	if !strings.Contains(errOut.String(), "tun2socks routing is not verified") {
		t.Fatalf("unexpected stderr: %s", errOut.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("target command appears to have run; marker err=%v", err)
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 1 {
		t.Fatalf("expected one audit file, got %d: %v", len(auditFiles), auditFiles)
	}
	auditData, err := os.ReadFile(auditFiles[0])
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if !strings.Contains(string(auditData), `"action":"network.setup"`) || !strings.Contains(string(auditData), `"decision":"deny"`) {
		t.Fatalf("network deny audit missing: %s", auditData)
	}
	if strings.Contains(string(auditData), "user:pass") {
		t.Fatalf("audit leaked proxy secret: %s", auditData)
	}
	secretFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "network", "proxy.url"))
	if err != nil {
		t.Fatalf("glob secret files: %v", err)
	}
	if len(secretFiles) != 0 {
		t.Fatalf("automatic cleanup should remove proxy secret files, got %v", secretFiles)
	}
}

func TestRunTun2SocksSecretErrorDoesNotExposeBackingEnvName(t *testing.T) {
	home := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	t.Setenv("HOME", home)
	t.Setenv("HIDEOUT_SECRET_MISSING_PROXY", "")
	var out, errOut bytes.Buffer
	code := Main([]string{
		"run",
		"--backend", "native",
		"--allow-weak-isolation",
		"--network", "tun2socks",
		"--proxy-secret", "missing-proxy",
		"--",
		"sh", "-c", "touch " + marker,
	}, &out, &errOut)
	if code == 0 {
		t.Fatal("expected tun2socks setup to fail for missing proxy secret")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("target command should not run after proxy secret failure; marker err=%v", err)
	}
	combined := out.String() + errOut.String()
	if !strings.Contains(combined, "secret ref missing-proxy") {
		t.Fatalf("run should report secret by ref name:\nstdout=%s\nstderr=%s", out.String(), errOut.String())
	}
	if strings.Contains(combined, "HIDEOUT_SECRET_") {
		t.Fatalf("run leaked backing secret env name:\nstdout=%s\nstderr=%s", out.String(), errOut.String())
	}
	auditFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "audit.jsonl"))
	if err != nil {
		t.Fatalf("glob audit files: %v", err)
	}
	if len(auditFiles) != 1 {
		t.Fatalf("expected one audit file, got %d: %v", len(auditFiles), auditFiles)
	}
	validateAuditJSONLWithSchema(t, auditFiles[0])
	auditData, err := os.ReadFile(auditFiles[0])
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if !strings.Contains(string(auditData), "secret ref missing-proxy") {
		t.Fatalf("audit should report secret by ref name: %s", auditData)
	}
	if strings.Contains(string(auditData), "HIDEOUT_SECRET_") {
		t.Fatalf("audit leaked backing secret env name: %s", auditData)
	}
}

func TestNetworkDecisionMarksRuntimeVerificationAsAuditOnly(t *testing.T) {
	got := networkDecision(netpolicy.Plan{
		Mode:          netpolicy.ModeTun2Socks,
		RuntimeVerify: true,
		Verified:      false,
	}, nil)
	if got != "audit-only" {
		t.Fatalf("networkDecision=%q want audit-only", got)
	}
}

func TestExplainNativeTun2SocksShowsFailClosedAndHiddenSecret(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HIDEOUT_SECRET_DEFAULT_PROXY", "socks5://user:pass@127.0.0.1:1080")
	var out, errOut bytes.Buffer
	code := Main([]string{"explain", "--backend", "native", "--network", "tun2socks", "--proxy-secret", "default-proxy", "--", "echo", "hi"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "fail closed until routing is verified") {
		t.Fatalf("explain missing fail closed warning: %s", out.String())
	}
	if !strings.Contains(out.String(), "Network proxy secret: default-proxy (value hidden)") {
		t.Fatalf("explain missing hidden proxy secret ref: %s", out.String())
	}
	if strings.Contains(out.String(), "user:pass") || strings.Contains(errOut.String(), "user:pass") {
		t.Fatalf("explain leaked proxy secret:\nstdout=%s\nstderr=%s", out.String(), errOut.String())
	}
	secretFiles, err := filepath.Glob(filepath.Join(home, ".hideout", "sessions", "*", "network", "proxy.url"))
	if err != nil {
		t.Fatalf("glob secret files: %v", err)
	}
	if len(secretFiles) != 0 {
		t.Fatalf("explain should not write proxy secret files, got %v", secretFiles)
	}
}

func TestExplainTun2SocksSecretErrorDoesNotExposeBackingEnvName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HIDEOUT_SECRET_MISSING_PROXY", "")
	var out, errOut bytes.Buffer
	code := Main([]string{"explain", "--backend", "native", "--network", "tun2socks", "--proxy-secret", "missing-proxy", "--", "echo", "hi"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "secret ref missing-proxy") {
		t.Fatalf("explain should report secret by ref name:\n%s", out.String())
	}
	if strings.Contains(out.String()+errOut.String(), "HIDEOUT_SECRET_") {
		t.Fatalf("explain leaked backing secret env name:\nstdout=%s\nstderr=%s", out.String(), errOut.String())
	}
}

func TestExplainLimaTun2SocksShowsRuntimeVerification(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HIDEOUT_SECRET_DEFAULT_PROXY", "socks5://user:pass@127.0.0.1:1080")
	var out, errOut bytes.Buffer
	code := Main([]string{"explain", "--backend", "lima", "--network", "tun2socks", "--proxy-secret", "default-proxy", "--mediated-resolver", "1.1.1.1", "--", "echo", "hi"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "route verified inside guest before target launch") {
		t.Fatalf("lima tun2socks explain missing runtime verification:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Network plan: engine=tun2socks verified=false runtimeVerify=true failClosed=false") {
		t.Fatalf("lima tun2socks explain missing plan details:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Network local bypass: host.lima.internal") {
		t.Fatalf("lima tun2socks explain missing local bypass:\n%s", out.String())
	}
	if strings.Contains(out.String(), "user:pass") || strings.Contains(errOut.String(), "user:pass") {
		t.Fatalf("explain leaked proxy secret:\nstdout=%s\nstderr=%s", out.String(), errOut.String())
	}
}

func TestMaterializeLimaShimsUsesGuestLocalShim(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(t.TempDir(), "hideout-shim-linux")
	if err := os.WriteFile(source, []byte("fake linux binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HIDEOUT_LINUX_SHIM_PATH", source)
	if err := materializeShims(dir, "lima", cmdproxy.DefaultRegistry(), netpolicy.Plan{Mode: netpolicy.ModeDirect}); err != nil {
		t.Fatalf("materializeShims: %v", err)
	}
	copied, err := os.ReadFile(filepath.Join(dir, "hideout-shim"))
	if err != nil {
		t.Fatalf("read copied shim: %v", err)
	}
	if string(copied) != "fake linux binary" {
		t.Fatalf("copied shim mismatch: %q", copied)
	}
	openScript, err := os.ReadFile(filepath.Join(dir, "open"))
	if err != nil {
		t.Fatalf("read open shim: %v", err)
	}
	if strings.Contains(string(openScript), source) {
		t.Fatalf("lima open shim leaked host shim path: %s", openScript)
	}
	if !strings.Contains(string(openScript), "$shim_dir/hideout-shim") {
		t.Fatalf("lima open shim should call guest-local hideout-shim: %s", openScript)
	}
	if _, err := os.Stat(filepath.Join(dir, "tun2socks")); !os.IsNotExist(err) {
		t.Fatalf("direct mode should not materialize tun2socks, err=%v", err)
	}
}

func TestMaterializeLimaShimsCopiesTun2SocksForTunMode(t *testing.T) {
	dir := t.TempDir()
	shim := filepath.Join(t.TempDir(), "hideout-shim-linux")
	if err := os.WriteFile(shim, []byte("fake linux shim"), 0o700); err != nil {
		t.Fatal(err)
	}
	tun2socks := filepath.Join(t.TempDir(), "tun2socks-linux")
	if err := os.WriteFile(tun2socks, []byte("fake linux tun2socks"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HIDEOUT_LINUX_SHIM_PATH", shim)
	t.Setenv("HIDEOUT_LINUX_TUN2SOCKS_PATH", tun2socks)
	if err := materializeShims(dir, "lima", cmdproxy.DefaultRegistry(), netpolicy.Plan{Mode: netpolicy.ModeTun2Socks}); err != nil {
		t.Fatalf("materializeShims: %v", err)
	}
	copied, err := os.ReadFile(filepath.Join(dir, "tun2socks"))
	if err != nil {
		t.Fatalf("read copied tun2socks: %v", err)
	}
	if string(copied) != "fake linux tun2socks" {
		t.Fatalf("copied tun2socks mismatch: %q", copied)
	}
}

func TestShimBuildLinuxWritesDefaultGuestShim(t *testing.T) {
	goModCache := goEnvValue(t, "GOMODCACHE")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HIDEOUT_LINUX_SHIM_PATH", "")
	t.Setenv("PATH", os.Getenv("PATH"))
	t.Setenv("GOCACHE", filepath.Join(t.TempDir(), "gocache"))
	t.Setenv("GOMODCACHE", goModCache)
	t.Setenv("GOFLAGS", strings.TrimSpace(os.Getenv("GOFLAGS")+" -modcacherw"))
	var out, errOut bytes.Buffer
	code := Main([]string{
		"shim",
		"build-linux",
		"--source",
		filepath.Join("..", ".."),
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	want := filepath.Join(home, ".hideout", "bin", "hideout-shim-linux-"+runtime.GOARCH)
	if strings.TrimSpace(out.String()) != want {
		t.Fatalf("build-linux output=%q want %q", strings.TrimSpace(out.String()), want)
	}
	st, err := os.Stat(want)
	if err != nil {
		t.Fatalf("built linux shim missing: %v", err)
	}
	if st.IsDir() || st.Mode().Perm() != 0o700 {
		t.Fatalf("built linux shim mode mismatch: %s", st.Mode())
	}
	if !helperbin.StoreHelperCurrent(want, "hideout-shim", runtime.GOARCH) {
		t.Fatalf("built linux shim manifest is missing or stale: %s", helperbin.ManifestPath(want))
	}
	t.Setenv("PATH", "")
	if got := resolveLinuxShimPath(); got != want {
		t.Fatalf("resolveLinuxShimPath()=%q want %q", got, want)
	}
	dir := t.TempDir()
	if err := materializeShims(dir, "lima", cmdproxy.DefaultRegistry(), netpolicy.Plan{Mode: netpolicy.ModeDirect}); err != nil {
		t.Fatalf("materializeShims should use default built linux shim: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hideout-shim")); err != nil {
		t.Fatalf("materialized linux shim missing: %v", err)
	}
}

func TestHostFSDBuildLinuxWritesDefaultGuestDaemon(t *testing.T) {
	goModCache := goEnvValue(t, "GOMODCACHE")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HIDEOUT_LINUX_HOSTFSD_PATH", "")
	t.Setenv("PATH", os.Getenv("PATH"))
	t.Setenv("GOCACHE", filepath.Join(t.TempDir(), "gocache"))
	t.Setenv("GOMODCACHE", goModCache)
	t.Setenv("GOFLAGS", strings.TrimSpace(os.Getenv("GOFLAGS")+" -modcacherw"))
	var out, errOut bytes.Buffer
	code := Main([]string{
		"hostfsd",
		"build-linux",
		"--source",
		filepath.Join("..", ".."),
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	want := filepath.Join(home, ".hideout", "bin", "hideout-hostfsd-linux-"+runtime.GOARCH)
	if strings.TrimSpace(out.String()) != want {
		t.Fatalf("build-linux output=%q want %q", strings.TrimSpace(out.String()), want)
	}
	st, err := os.Stat(want)
	if err != nil {
		t.Fatalf("built linux hostfsd missing: %v", err)
	}
	if st.IsDir() || st.Mode().Perm() != 0o700 {
		t.Fatalf("built linux hostfsd mode mismatch: %s", st.Mode())
	}
	if !helperbin.StoreHelperCurrent(want, "hideout-hostfsd", runtime.GOARCH) {
		t.Fatalf("built linux hostfsd manifest is missing or stale: %s", helperbin.ManifestPath(want))
	}
	t.Setenv("PATH", "")
	if got := resolveLinuxHostFSDPath(); got != want {
		t.Fatalf("resolveLinuxHostFSDPath()=%q want %q", got, want)
	}
	dir := t.TempDir()
	if err := materializeHostFSD(dir, "lima", true); err != nil {
		t.Fatalf("materializeHostFSD should use default built linux hostfsd: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hideout-hostfsd")); err != nil {
		t.Fatalf("materialized linux hostfsd missing: %v", err)
	}
}

func TestResolveLinuxHostFSDPathIgnoresMissingEnvPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", "")
	t.Setenv("HIDEOUT_LINUX_HOSTFSD_PATH", filepath.Join(t.TempDir(), "missing-hostfsd"))
	if got := resolveLinuxHostFSDPath(); got != "" {
		t.Fatalf("resolveLinuxHostFSDPath()=%q want empty for missing env path", got)
	}
}

func TestMaterializeHostFSDRequiresPrebuiltGuestDaemonWhenEnabled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HIDEOUT_LINUX_HOSTFSD_PATH", "")
	t.Setenv("PATH", "")
	err := materializeHostFSD(t.TempDir(), "lima", true)
	if err == nil || !strings.Contains(err.Error(), "requires a prebuilt linux hideout-hostfsd") {
		t.Fatalf("expected prebuilt linux hostfsd requirement, got %v", err)
	}
}

func TestMaterializeLimaShimsRequiresPrebuiltGuestShim(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HIDEOUT_LINUX_SHIM_PATH", "")
	t.Setenv("PATH", "")
	err := materializeShims(t.TempDir(), "lima", cmdproxy.DefaultRegistry(), netpolicy.Plan{Mode: netpolicy.ModeDirect})
	if err == nil || !strings.Contains(err.Error(), "requires a prebuilt linux hideout-shim") {
		t.Fatalf("expected prebuilt linux shim requirement, got %v", err)
	}
}

func TestAppendBrokerEnvOmitsLegacySocketForTCP(t *testing.T) {
	env := appendBrokerEnv(nil, broker.TCPEndpoint("host.lima.internal:1234"), "ses_1", "cap_1", "/tmp/hideout.sock")
	got := strings.Join(env, "\n")
	if !strings.Contains(got, broker.EnvEndpoint+"=tcp://host.lima.internal:1234") {
		t.Fatalf("missing endpoint env: %v", env)
	}
	if strings.Contains(got, broker.EnvSock+"=") {
		t.Fatalf("tcp endpoint should not expose legacy socket env: %v", env)
	}
}

func TestBrokerEndpointForDoctorClientUsesLoopbackForUnspecifiedTCP(t *testing.T) {
	for _, endpoint := range []broker.Endpoint{
		broker.TCPEndpoint("0.0.0.0:1234"),
		broker.TCPEndpoint("[::]:1234"),
	} {
		got := brokerEndpointForDoctorClient(endpoint)
		if got.String() != "tcp://127.0.0.1:1234" {
			t.Fatalf("doctor client endpoint=%s want tcp://127.0.0.1:1234", got.String())
		}
	}
	unchanged := brokerEndpointForDoctorClient(broker.TCPEndpoint("host.lima.internal:1234"))
	if unchanged.String() != "tcp://host.lima.internal:1234" {
		t.Fatalf("specific tcp endpoint changed to %s", unchanged.String())
	}
}

func TestCheckBrokerUsesTCPForLima(t *testing.T) {
	layout := sessionTestLayout(t)
	p := profile.Default("default")
	var reports []string
	checkBroker(t.TempDir(), p, "lima", layout, t.TempDir(), "/workspace", t.TempDir(), func(name, status, message string) {
		reports = append(reports, name+": "+status+" "+message)
	})
	got := strings.Join(reports, "\n")
	if !strings.Contains(got, "broker: ok") {
		t.Fatalf("lima broker check did not pass:\n%s", got)
	}
	if !strings.Contains(got, "transport=tcp endpoint=present") {
		t.Fatalf("lima broker check did not report tcp endpoint presence:\n%s", got)
	}
	if strings.Contains(got, "tcp://") || strings.Contains(got, "unix://") {
		t.Fatalf("broker check leaked raw endpoint address:\n%s", got)
	}
}

func TestCheckBrokerOpenToleratesSlowLocalBrokerResponse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var req broker.Request
		if err := json.NewDecoder(conn).Decode(&req); err != nil {
			return
		}
		time.Sleep(300 * time.Millisecond)
		_ = json.NewEncoder(conn).Encode(broker.Response{
			ID:       req.ID,
			Decision: "allow",
			Status:   "ok",
			ExitCode: 0,
		})
	}()

	resp := checkBrokerOpen(context.Background(), broker.TCPEndpoint(ln.Addr().String()), broker.Request{
		ID:              "req_slow_doctor",
		SessionID:       "ses_test",
		CapabilityToken: "cap_test",
		Action:          "host.open",
		Route:           "host-broker",
		Args:            map[string]any{"target": "https://example.com"},
	})
	if resp.Status != "ok" || resp.Decision != "allow" {
		t.Fatalf("slow broker response was not accepted: %+v", resp)
	}
	<-done
}

func TestCheckBrokerReportsStartFailure(t *testing.T) {
	layout := sessionTestLayout(t)
	badParent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(badParent, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	layout.BrokerSock = filepath.Join(badParent, "broker.sock")

	p := profile.Default("default")
	var reports []string
	checkBroker(t.TempDir(), p, "native", layout, t.TempDir(), "/workspace", t.TempDir(), func(name, status, message string) {
		reports = append(reports, name+": "+status+" "+message)
	})
	got := strings.Join(reports, "\n")
	if !strings.Contains(got, "broker: error") {
		t.Fatalf("broker start failure was not reported as an error:\n%s", got)
	}
	if strings.Contains(got, "broker: ok") {
		t.Fatalf("broker start failure should not report ok:\n%s", got)
	}
}

func TestAuditExportProducesCleanArtifactAndLeavesLocalAuditFullFidelity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	storeRoot := filepath.Join(home, ".hideout")
	sessionID := "ses_export_cli"
	auditPath := filepath.Join(storeRoot, "sessions", sessionID, "audit.jsonl")
	mustWriteAppTest(t, auditPath, `{"time":"2026-07-07T00:00:00Z","session":"`+sessionID+`","profile":"default","backend":"native","action":"host.open","decision":"allow","details":{"target":"https://user:pass@example.com/path?token=abc","capabilityToken":"cap_0123456789abcdef0123456789abcdef","machineId":"0123456789abcdef0123456789abcdef"}}`+"\n")

	outPath := filepath.Join(t.TempDir(), "artifact.json")
	var out, errOut bytes.Buffer
	code := Main([]string{
		"audit", "export",
		"--session", sessionID,
		"--out", outPath,
		"--acknowledge-full-fidelity",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("audit export exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	artifact, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(artifact)
	if !strings.Contains(text, "https://user:pass@example.com/path?token=abc") {
		t.Fatalf("export should preserve acknowledged user data:\n%s", text)
	}
	for _, leaked := range []string{"cap_0123456789abcdef", "capabilityToken", "0123456789abcdef0123456789abcdef"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("export leaked control-plane material %q:\n%s", leaked, text)
		}
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"audit", "show", "--session", sessionID, "--json"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("audit show exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "https://user:pass@example.com/path?token=abc") {
		t.Fatalf("local audit show should preserve user data verbatim:\n%s", out.String())
	}
}

func buildShim(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "hideout-shim")
	cmd := exec.Command("go", "build", "-o", out, "../../cmd/hideout-shim")
	cmd.Env = os.Environ()
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build hideout-shim: %v\n%s", err, data)
	}
	return out
}

func fakeLimactlScript(logPath, body string) string {
	return fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %q\n%s\n", logPath, body)
}

func limaStartInstanceNames(log string) []string {
	var names []string
	for _, line := range strings.Split(log, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "start" {
			continue
		}
		for i := 0; i < len(fields)-1; i++ {
			if fields[i] == "--name" {
				names = append(names, fields[i+1])
				break
			}
		}
	}
	return names
}

func limaStartLines(log string) []string {
	var lines []string
	for _, line := range strings.Split(log, "\n") {
		if strings.HasPrefix(line, "start ") {
			lines = append(lines, line)
		}
	}
	return lines
}

func mustWriteAppTest(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func setSafeBrowserPathForAppTest(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hideout-browser")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HIDEOUT_BROWSER_PATH", path)
}

func setFakeLinuxShimPathForAppTest(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hideout-shim-linux")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HIDEOUT_LINUX_SHIM_PATH", path)
}

func setFakeLinuxHostFSDPathForAppTest(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hideout-hostfsd-linux")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HIDEOUT_LINUX_HOSTFSD_PATH", path)
}

func sessionTestLayout(t *testing.T) session.Layout {
	t.Helper()
	layout, err := session.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return layout
}

func readProfileMetadataForAppTest(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Metadata map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Metadata
}

func validateAuditJSONLWithSchema(t *testing.T, path string) {
	t.Helper()
	schema := compileAuditSchemaForAppTest(t)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	line := 0
	for scanner.Scan() {
		line++
		data := scanner.Bytes()
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("decode audit line %d: %v\n%s", line, err, data)
		}
		if err := schema.Validate(doc); err != nil {
			t.Fatalf("audit line %d does not match schema: %v\n%s", line, err, data)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if line == 0 {
		t.Fatalf("audit log is empty: %s", path)
	}
}

func hostOpenRequestIDs(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	var ids []string
	for scanner.Scan() {
		var event struct {
			Action  string         `json:"action"`
			Details map[string]any `json:"details"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("decode audit event: %v\n%s", err, scanner.Text())
		}
		if event.Action != "host.open" {
			continue
		}
		id, _ := event.Details["requestId"].(string)
		ids = append(ids, id)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan audit log: %v", err)
	}
	return ids
}

func goEnvValue(t *testing.T, name string) string {
	t.Helper()
	cmd := exec.Command("go", "env", name)
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("go env %s: %v", name, err)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		t.Fatalf("go env %s returned empty value", name)
	}
	return value
}

type auditEventForAppTest struct {
	Action   string         `json:"action"`
	Decision string         `json:"decision"`
	Details  map[string]any `json:"details"`
}

func readAuditEventsForAppTest(t *testing.T, path string) []auditEventForAppTest {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var events []auditEventForAppTest
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var event auditEventForAppTest
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("decode audit event: %v\n%s", err, scanner.Text())
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

func auditEventsByActionForAppTest(t *testing.T, path string) map[string]auditEventForAppTest {
	t.Helper()
	events := map[string]auditEventForAppTest{}
	for _, event := range readAuditEventsForAppTest(t, path) {
		events[event.Action] = event
	}
	return events
}

func lastAuditEventByActionForAppTest(t *testing.T, path, action string) auditEventForAppTest {
	t.Helper()
	events := readAuditEventsForAppTest(t, path)
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Action == action {
			return events[i]
		}
	}
	t.Fatalf("audit missing action %s: %+v", action, events)
	return auditEventForAppTest{}
}

func stringValueForAppTest(value any) string {
	s, _ := value.(string)
	return s
}

func compileAuditSchemaForAppTest(t *testing.T) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "audit-event.schema.json"))
	if err != nil {
		t.Fatalf("read audit schema: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode audit schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("audit-event.schema.json", doc); err != nil {
		t.Fatalf("add audit schema: %v", err)
	}
	schema, err := compiler.Compile("audit-event.schema.json")
	if err != nil {
		t.Fatalf("compile audit schema: %v", err)
	}
	return schema
}

func TestEnvCreateAndInspect(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir()) // no limactl reachable: create must not need it
	workspace := t.TempDir()
	t.Chdir(workspace)

	var out, errOut bytes.Buffer
	code := Main([]string{"env", "create", "work", "--image", "template:_images/ubuntu-lts"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("env create exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "work") || !strings.Contains(out.String(), "template:_images/ubuntu-lts") {
		t.Fatalf("create output should name environment and image: %s", out.String())
	}

	store := environment.Store{Root: filepath.Join(home, ".hideout")}
	rec, err := store.LoadByName("work")
	if err != nil {
		t.Fatalf("record not written: %v", err)
	}
	if rec.ImageRef != "template:_images/ubuntu-lts" || rec.AutoNamed {
		t.Fatalf("unexpected record: %+v", rec)
	}
	if filepath.Clean(rec.Workspace) != filepath.Clean(mustEvalSymlinks(t, workspace)) && filepath.Clean(rec.Workspace) != filepath.Clean(workspace) {
		t.Fatalf("workspace should pin the invoking directory: %+v", rec)
	}

	out.Reset()
	errOut.Reset()
	code = Main([]string{"env", "inspect", "work"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("env inspect exit=%d stderr=%s", code, errOut.String())
	}
	for _, want := range []string{"work", "template:_images/ubuntu-lts", rec.ID, "lima"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("inspect output missing %q: %s", want, out.String())
		}
	}

	// reserved name
	out.Reset()
	errOut.Reset()
	if code := Main([]string{"env", "create", "Default"}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "reserved") {
		t.Fatalf("reserved name must be rejected: exit=%d stderr=%s", code, errOut.String())
	}
	// collision
	if code := Main([]string{"env", "create", "WORK"}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "exists") {
		t.Fatalf("name collision must be rejected: stderr=%s", errOut.String())
	}
	// digest-less URL guidance
	errOut.Reset()
	if code := Main([]string{"env", "create", "img1", "--image", "https://example.com/dev.img"}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "sha256") {
		t.Fatalf("digest-less URL must fail with sha256 guidance: stderr=%s", errOut.String())
	}
	// credentialed URL rejection
	errOut.Reset()
	if code := Main([]string{"env", "create", "img2", "--image", "https://user:pass@example.com/dev.img#sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "credentials") {
		t.Fatalf("credentialed URL must be rejected: stderr=%s", errOut.String())
	}
}

func mustEvalSymlinks(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestRunSummaryNamesSelectedEnvironment(t *testing.T) {
	var out, errOut bytes.Buffer
	a := app{stdout: &out, stderr: &errOut}
	a.writeRunResultSummary(manager.RunResult{
		EnvironmentID:    "env_20260703t010203zabcdef1234567890",
		EnvironmentName:  "work",
		PreserveInstance: true,
	})
	if !strings.Contains(errOut.String(), "Hideout environment name: work") {
		t.Fatalf("run summary should name the environment: %s", errOut.String())
	}
}

func TestEnvListShowsAllAndTopLevelListIsGone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()
	t.Chdir(workspace)

	var out, errOut bytes.Buffer
	if code := Main([]string{"env", "create", "work", "--image", "template:_images/ubuntu-lts"}, &out, &errOut); code != 0 {
		t.Fatalf("env create: %s", errOut.String())
	}
	// plant an auto-named environment via manager selection
	store, err := profile.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.LoadOrInit("default")
	if err != nil {
		t.Fatal(err)
	}
	autoWS := t.TempDir()
	if _, err := manager.New(store).SelectRunEnvironment(manager.RunPlan{Backend: "lima", Workspace: autoWS, GuestWorkspace: autoWS, RuntimeProfile: p}, manager.RunEnvironmentOptions{Create: true}); err != nil {
		t.Fatal(err)
	}
	// plant a foreign-version record
	oldID := "env_20260701t000000zaabbccddee0000000002"
	oldDir := filepath.Join(store.Root, "environments", oldID)
	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "environment.json"), []byte(`{"version":"hideout.environment/v1","id":"`+oldID+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errOut.Reset()
	if code := Main([]string{"env", "list"}, &out, &errOut); code != 0 {
		t.Fatalf("env list exit != 0: %s", errOut.String())
	}
	text := out.String()
	if !strings.Contains(text, "work") {
		t.Fatalf("env list missing explicit environment: %s", text)
	}
	auto := environment.AutoName("default", autoWS)
	if !strings.Contains(text, auto) || !strings.Contains(text, "auto") {
		t.Fatalf("env list missing auto-named marker for %s: %s", auto, text)
	}
	if !strings.Contains(text, "unsupported-version") || !strings.Contains(text, oldID) {
		t.Fatalf("env list missing unsupported-version row: %s", text)
	}
	if !strings.Contains(text, "template:_images/ubuntu-lts") && !strings.Contains(text, "ubuntu-lts") {
		t.Fatalf("env list missing image column: %s", text)
	}

	// top-level list is gone
	out.Reset()
	errOut.Reset()
	if code := Main([]string{"list"}, &out, &errOut); code == 0 {
		t.Fatalf("top-level list must be removed: %s", out.String())
	}
	// removed run flags are rejected
	errOut.Reset()
	if code := Main([]string{"run", "--resume", "env_x", "--", "true"}, &out, &errOut); code == 0 {
		t.Fatal("--resume must be removed")
	}
	errOut.Reset()
	if code := Main([]string{"run", "--new", "--", "true"}, &out, &errOut); code == 0 {
		t.Fatal("--new must be removed")
	}
}

func TestEnvDestructiveCommandsAndWorkspaceGuard(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()
	t.Chdir(workspace)

	var out, errOut bytes.Buffer
	if code := Main([]string{"env", "create", "boxed"}, &out, &errOut); code != 0 {
		t.Fatalf("env create: %s", errOut.String())
	}

	// dangerous workspace root rejected at create
	errOut.Reset()
	if code := Main([]string{"env", "create", "danger", "--workspace", home}, &out, &errOut); code == 0 {
		t.Fatal("dangerous workspace root must be rejected at env create")
	}

	// mark running: recreate/remove refuse without --force
	store, err := profile.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	envStore := environment.Store{Root: store.Root}
	rec, err := envStore.LoadByName("boxed")
	if err != nil {
		t.Fatal(err)
	}
	rec.Status = "running"
	rec.InstanceName = "" // no real instance: force path must not call limactl
	if err := envStore.Save(rec); err != nil {
		t.Fatal(err)
	}
	errOut.Reset()
	if code := Main([]string{"env", "recreate", "boxed"}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "hideout stop boxed") {
		t.Fatalf("recreate must refuse running guest with stop hint: %s", errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := Main([]string{"env", "recreate", "boxed", "--force"}, &out, &errOut); code != 0 {
		t.Fatalf("forced recreate: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "recreated environment boxed") {
		t.Fatalf("recreate output: %s", out.String())
	}
	out.Reset()
	errOut.Reset()
	if code := Main([]string{"env", "remove", "boxed"}, &out, &errOut); code != 0 {
		t.Fatalf("remove stopped env: %s", errOut.String())
	}
	if _, err := envStore.LoadByName("boxed"); err == nil {
		t.Fatal("record should be gone")
	}
}

func TestRunWarnsShadowedHostFSRules(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()
	t.Chdir(workspace)

	var out, errOut bytes.Buffer
	inside := filepath.Join(workspace, "secrets")
	if err := os.MkdirAll(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	if code := Main([]string{"profile", "fs", "default", "add", "--fs", "dir:" + inside, "--reason", "test"}, &out, &errOut); code != 0 {
		t.Fatalf("profile fs add: %s", errOut.String())
	}
	out.Reset()
	errOut.Reset()
	code := Main([]string{"run", "--backend", "native", "--allow-weak-isolation", "--", "true"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("run: %s", errOut.String())
	}
	if !strings.Contains(errOut.String(), "shadowed by the workspace") {
		t.Fatalf("run should warn about shadowed hostfs rule: %s", errOut.String())
	}
}

// TestDaemonAbsentDegradesGracefully (T013) — with no daemon running, the daemon
// client commands fail closed cleanly (no panic/hang) and existing embedded
// commands are unaffected, proving daemon absence does not regress daemon-less
// operation (FR-006).
func TestDaemonAbsentDegradesGracefully(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var out, errOut bytes.Buffer
	code := Main([]string{"daemon", "status"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("daemon status with no daemon should fail closed, got exit 0")
	}
	if !strings.Contains(errOut.String(), "no running daemon") &&
		!strings.Contains(errOut.String(), "not reachable") {
		t.Fatalf("daemon status should report no running daemon, got: %s", errOut.String())
	}

	// Embedded operation is unaffected.
	out.Reset()
	errOut.Reset()
	if code := Main([]string{"help"}, &out, &errOut); code != 0 {
		t.Fatalf("embedded help should still work with no daemon: exit=%d stderr=%s", code, errOut.String())
	}
}
