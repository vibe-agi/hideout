package lima

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"golang.org/x/crypto/ssh"
)

func TestInitialTerminalDimensionsPreserveHostSizeAndBoundFallback(t *testing.T) {
	width, height := initialTerminalDimensions(42, func(fd int) (int, int, error) {
		if fd != 42 {
			t.Fatalf("fd=%d", fd)
		}
		return 132, 43, nil
	})
	if width != 132 || height != 43 {
		t.Fatalf("dimensions=%dx%d", width, height)
	}
	width, height = initialTerminalDimensions(42, func(int) (int, int, error) {
		return 0, 0, errors.New("not a terminal")
	})
	if width != 80 || height != 24 {
		t.Fatalf("fallback dimensions=%dx%d", width, height)
	}
}

func TestIsolatedNonTerminalRunKeepsStreamsExitAndCancellationSessionLocal(t *testing.T) {
	exitErr := errors.New("target exit 37")
	runner := &isolatedRunSetupRunner{targetErr: exitErr}
	var stdout, stderr bytes.Buffer
	b := Backend{SetupRunner: runner, Stdout: &stdout, Stderr: &stderr, Stdin: strings.NewReader("")}
	session := &backend.Session{
		ID: "ses_20260716T120000Z_0123456789abcdef", InstanceName: "hideout-test",
		ConfigPath: "/unused/lima.yaml", GuestWork: "/workspace", RuntimeReady: true,
		SessionIsolationRequired: true, TargetUser: "developer",
	}
	err := b.Run(context.Background(), session, []string{"tool", "argument with spaces"}, []string{"PATH=/usr/bin"})
	if !errors.Is(err, exitErr) {
		t.Fatalf("run error=%v want target exit", err)
	}
	if stdout.String() != "target-stdout\n" || stderr.String() != "target-stderr\n" {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if len(runner.commands) != 2 || !strings.Contains(strings.Join(runner.commands[1], " "), "argument with spaces") {
		t.Fatalf("isolated commands=%q", runner.commands)
	}

	cancelRunner := &isolatedRunSetupRunner{waitForCancel: true, targetEntered: make(chan struct{})}
	b.SetupRunner = cancelRunner
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- b.Run(ctx, session, []string{"tool"}, []string{"PATH=/usr/bin"}) }()
	select {
	case <-cancelRunner.targetEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("isolated target did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("isolated target ignored cancellation")
	}
}

func TestIsolatedCommandPreflightDistinguishesMissingCommandFromViewFailure(t *testing.T) {
	session := &backend.Session{
		ID: "ses_20260716T120000Z_0123456789abcdef", InstanceName: "hideout-test",
		ConfigPath: "/unused/lima.yaml", GuestWork: "/workspace", RuntimeReady: true,
		SessionIsolationRequired: true, TargetUser: "developer",
	}
	missing := &isolatedRunSetupRunner{checkErr: fakeExitStatusError(127)}
	err := (Backend{SetupRunner: missing}).Run(context.Background(), session, []string{"missing-tool"}, []string{"PATH=/usr/bin:/bin"})
	var notFound backend.CommandNotFoundError
	if !errors.As(err, &notFound) || notFound.Command != "missing-tool" {
		t.Fatalf("missing command error=%T %v", err, err)
	}

	brokenView := &isolatedRunSetupRunner{checkErr: fakeExitStatusError(32)}
	err = (Backend{SetupRunner: brokenView}).Run(context.Background(), session, []string{"sh"}, []string{"PATH=/usr/bin:/bin"})
	if errors.As(err, &notFound) || !strings.Contains(err.Error(), "session isolation command preflight failed") {
		t.Fatalf("view failure was misclassified: %T %v", err, err)
	}
}

func TestIsolatedRunReusesOneSSHTransportForPreflightAndTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LIMA_HOME", "")
	clientSigner, clientKey := testSSHSigner(t)
	hostSigner, _ := testSSHSigner(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverConfig := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if bytes.Equal(key.Marshal(), clientSigner.PublicKey().Marshal()) {
			return nil, nil
		}
		return nil, errors.New("unexpected client key")
	}}
	serverConfig.AddHostKey(hostSigner)
	var connections atomic.Int32
	var channels atomic.Int32
	var serverWG sync.WaitGroup
	serverWG.Add(1)
	go func() {
		defer serverWG.Done()
		for {
			raw, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			connections.Add(1)
			serverWG.Add(1)
			go func() {
				defer serverWG.Done()
				conn, incoming, requests, handshakeErr := ssh.NewServerConn(raw, serverConfig)
				if handshakeErr != nil {
					_ = raw.Close()
					return
				}
				defer conn.Close()
				go ssh.DiscardRequests(requests)
				for next := range incoming {
					if next.ChannelType() != "session" {
						_ = next.Reject(ssh.UnknownChannelType, "session required")
						continue
					}
					channel, channelRequests, channelErr := next.Accept()
					if channelErr != nil {
						continue
					}
					channels.Add(1)
					go func() {
						defer channel.Close()
						for request := range channelRequests {
							if request.Type != "exec" {
								_ = request.Reply(false, nil)
								continue
							}
							var payload struct{ Command string }
							if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
								_ = request.Reply(false, nil)
								return
							}
							_ = request.Reply(true, nil)
							if strings.Contains(payload.Command, "hideout-session-guardian") {
								_, _ = io.Copy(io.Discard, channel)
							}
							_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
							return
						}
					}()
				}
			}()
		}
	}()

	identity := filepath.Join(home, "id_lima")
	if err := os.WriteFile(identity, clientKey, 0o600); err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(home, ".lima", "hideout-test")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	config := strings.Join([]string{
		"Host hideout-test", "  HostName 127.0.0.1", "  Port " + port,
		"  User root", "  IdentityFile " + strconv.Quote(identity),
		"  StrictHostKeyChecking no",
	}, "\n")
	if err := os.WriteFile(filepath.Join(configDir, "ssh.config"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	b := Backend{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard, ControlStdout: io.Discard, ControlStderr: io.Discard}
	session := &backend.Session{
		ID: "ses_20260716T120000Z_0123456789abcdef", InstanceName: "hideout-test",
		ConfigPath: "/unused/lima.yaml", GuestWork: "/workspace", RuntimeReady: true, SessionIsolationRequired: true,
		TargetUser: "developer", ExpectedBootID: "01234567-89ab-cdef-0123-456789abcdef",
	}
	if err := b.Run(context.Background(), session, []string{"true"}, []string{"PATH=/usr/bin:/bin"}); err != nil {
		t.Fatal(err)
	}
	if !session.IsolationCleanupProved {
		t.Fatal("owning SSH transport did not retain the cleanup proof")
	}
	_ = listener.Close()
	serverWG.Wait()
	if connections.Load() != 1 || channels.Load() != 4 {
		t.Fatalf("ssh connections=%d channels=%d want one transport with preflight+guardian+target+cleanup-proof channels", connections.Load(), channels.Load())
	}
}

func TestSessionGuardianIsCoreOwnedExactSessionAndFailClosed(t *testing.T) {
	command, err := SessionGuardianCommand("ses_20260716T120000Z_0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(command, " ")
	for _, required := range []string{"trap '' HUP INT TERM", `read -r -t 2 outcome`, "ping)", "done)", sessionGuardianRoot, "/proc/$pid/stat", "/proc/$pid/cmdline", "current_start", "grep -Fqx", "kill -KILL", "hideout-session-guardian"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("guardian command missing %q: %s", required, joined)
		}
	}
	if strings.Contains(joined, "pkill") || strings.Contains(joined, "kill -1") || strings.Contains(joined, "/proc/[0-9]*/environ") {
		t.Fatalf("guardian contains a broad process selector: %s", joined)
	}
	if out, err := exec.Command("bash", "-n", "-c", command[2]).CombinedOutput(); err != nil {
		t.Fatalf("guardian shell syntax: %v\n%s", err, out)
	}
	if _, err := SessionGuardianCommand("ses_../../sibling"); err == nil {
		t.Fatal("guardian accepted an unsafe session identity")
	}
}

func TestSessionGuardianHeartbeatSendsPingDoneAndDisconnect(t *testing.T) {
	finish := make(chan bool, 1)
	heartbeat := &sessionGuardianHeartbeat{ticker: time.NewTicker(time.Millisecond), finish: finish}
	defer heartbeat.ticker.Stop()
	buf := make([]byte, 16)
	n, err := heartbeat.Read(buf)
	if err != nil || string(buf[:n]) != "ping\n" {
		t.Fatalf("heartbeat=%q err=%v", buf[:n], err)
	}
	finish <- true
	n, err = heartbeat.Read(buf)
	if err != nil || string(buf[:n]) != "done\n" {
		t.Fatalf("completion=%q err=%v", buf[:n], err)
	}
	if _, err := heartbeat.Read(buf); !errors.Is(err, io.EOF) {
		t.Fatalf("completion did not close stream: %v", err)
	}

	disconnect := make(chan bool, 1)
	heartbeat = &sessionGuardianHeartbeat{ticker: time.NewTicker(time.Hour), finish: disconnect}
	defer heartbeat.ticker.Stop()
	disconnect <- false
	if _, err := heartbeat.Read(buf); !errors.Is(err, io.EOF) {
		t.Fatalf("disconnect did not close heartbeat stream: %v", err)
	}
}

func TestGuardedSessionViewPublishesImmutableNamespaceParentIdentity(t *testing.T) {
	spec := SessionViewSpec{
		SessionID: "ses_20260716T120000Z_0123456789abcdef", TargetUser: "developer",
		GuestWork: "/workspace", Env: []string{"PATH=/usr/bin:/bin"}, Command: []string{"true"},
		GuardianControl: true,
	}
	command, err := BuildSessionViewCommand(spec)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(command, " ")
	for _, required := range []string{sessionGuardianControlPath(spec.SessionID), `/proc/$$/stat`, `awk '{print $22}'`, `exec "$@"`, "--kill-child=KILL", GuestRuntimeDir + "/sessions/" + spec.SessionID} {
		if !strings.Contains(joined, required) {
			t.Fatalf("guarded launcher missing %q: %s", required, joined)
		}
	}
	if out, err := exec.Command("sh", "-n", "-c", command[2]).CombinedOutput(); err != nil {
		t.Fatalf("guarded launcher shell syntax: %v\n%s", err, out)
	}
}

func TestBuildSessionViewCommandValidatesAndPreservesStructuredTarget(t *testing.T) {
	spec := SessionViewSpec{
		SessionID:            "ses_20260716T120000Z_0123456789abcdef",
		TargetUser:           "developer",
		GuestWork:            "/workspace",
		Env:                  []string{"HOME=/hideout/profile/home", "PATH=/hideout/session/shims:/usr/bin", "VALUE=spaces and ' quotes"},
		Command:              []string{"sh", "-c", "printf '%s' \"$VALUE\"", "argument with spaces"},
		RunBootstrap:         true,
		NetworkBootstrapPath: "/hideout/session/network/bootstrap.sh",
		NetworkCleanupPath:   "/hideout/session/network/cleanup.sh",
		HostFSEnabled:        true,
		HostFSGrafts:         []string{"/Users/example/project"},
	}
	command, err := BuildSessionViewCommand(spec)
	if err != nil {
		t.Fatal(err)
	}
	wantPrefix := []string{"unshare", "--mount", "--pid", "--fork", "--kill-child=KILL", "--mount-proc=/proc", "--", "sh", "-c"}
	if !reflect.DeepEqual(command[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("prefix=%q", command)
	}
	if command[len(command)-1] != GuestRuntimeDir+"/sessions/"+spec.SessionID {
		t.Fatalf("source argument=%q", command[len(command)-1])
	}
	script := command[9]
	for _, required := range []string{
		"mount --make-rprivate /",
		"mount --bind \"$1\" /hideout/session",
		"hideout-runtime-private /hideout/runtime",
		"--reuid=developer",
		"env' '-i'",
		"VALUE=spaces and",
		"cleanup_session_view",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("script missing %q:\n%s", required, script)
		}
	}
	if out, err := exec.Command("sh", "-n", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("session view shell syntax: %v\n%s", err, out)
	}
}

func TestBuildSessionViewCommandRejectsUnsafeInputs(t *testing.T) {
	base := SessionViewSpec{
		SessionID:  "ses_20260716T120000Z_0123456789abcdef",
		TargetUser: "developer",
		GuestWork:  "/workspace",
		Env:        []string{"PATH=/usr/bin"},
		Command:    []string{"true"},
	}
	tests := []struct {
		name string
		edit func(*SessionViewSpec)
	}{
		{"session traversal", func(s *SessionViewSpec) { s.SessionID = "ses_../../root" }},
		{"root target", func(s *SessionViewSpec) { s.TargetUser = "root" }},
		{"relative workdir", func(s *SessionViewSpec) { s.GuestWork = "workspace" }},
		{"bad env", func(s *SessionViewSpec) { s.Env = []string{"A-B=value"} }},
		{"empty argv", func(s *SessionViewSpec) { s.Command = nil }},
		{"network escape", func(s *SessionViewSpec) { s.NetworkBootstrapPath = "/tmp/operator.sh" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := base
			spec.Env = append([]string(nil), base.Env...)
			spec.Command = append([]string(nil), base.Command...)
			tc.edit(&spec)
			if _, err := BuildSessionViewCommand(spec); err == nil {
				t.Fatal("unsafe session view accepted")
			}
		})
	}
}

func TestBuildSessionViewCommandWithoutCleanupHooksIsValidShell(t *testing.T) {
	command, err := BuildSessionViewCommand(SessionViewSpec{
		SessionID: "ses_20260716T120000Z_0123456789abcdef", TargetUser: "developer",
		GuestWork: "/workspace", Env: []string{"PATH=/usr/bin:/bin"}, Command: CommandCheck("sh"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sh", "-n", "-c", command[9]).CombinedOutput(); err != nil {
		t.Fatalf("session view without cleanup hooks has invalid shell syntax: %v\n%s\n%s", err, out, command[9])
	}
	if !strings.Contains(command[9], "cleanup_session_view() {\n:\n}") {
		t.Fatalf("empty cleanup function is not a valid no-op:\n%s", command[9])
	}
}

func TestSessionViewSetupReceivesOnlyOwningBrokerAuthority(t *testing.T) {
	command, err := BuildSessionViewCommand(SessionViewSpec{
		SessionID: "ses_20260716T120000Z_0123456789abcdef", TargetUser: "developer", GuestWork: "/workspace",
		Env: []string{
			"PATH=/hideout/session/shims:/usr/bin:/bin",
			"LD_PRELOAD=/workspace/attacker.so",
			"HIDEOUT_SESSION_ID=ses_20260716T120000Z_0123456789abcdef",
			"HIDEOUT_BROKER_ENDPOINT=tcp://host.lima.internal:12345",
			"HIDEOUT_CAPABILITY_TOKEN=cap_0123456789abcdef",
		},
		Command: []string{"true"}, HostFSEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	script := command[9]
	for _, allowed := range []string{
		"export 'HIDEOUT_BROKER_ENDPOINT=tcp://host.lima.internal:12345'",
		"export 'HIDEOUT_CAPABILITY_TOKEN=cap_0123456789abcdef'",
		"export 'HIDEOUT_SESSION_ID=ses_20260716T120000Z_0123456789abcdef'",
	} {
		if !strings.Contains(script, allowed) {
			t.Fatalf("setup script missing %q:\n%s", allowed, script)
		}
	}
	setupPrefix := script[:strings.Index(script, "cleanup_session_view()")]
	for _, forbidden := range []string{"export 'LD_PRELOAD=", "export 'PATH="} {
		if strings.Contains(setupPrefix, forbidden) {
			t.Fatalf("untrusted target environment was elevated into setup: %q\n%s", forbidden, setupPrefix)
		}
	}
}

func TestSessionViewMasksSiblingRuntimeAndUsesPrivateProcAndMounts(t *testing.T) {
	ownerID := "ses_20260716T120000Z_0123456789abcdef"
	siblingID := "ses_20260716T120001Z_fedcba9876543210"
	command, err := BuildSessionViewCommand(SessionViewSpec{
		SessionID: ownerID, TargetUser: "developer", GuestWork: "/workspace",
		Env: []string{
			"PATH=/hideout/session/shims:/usr/bin",
			"HIDEOUT_SESSION_ID=" + ownerID,
			"HIDEOUT_CAPABILITY_TOKEN=cap_owner_only",
		},
		Command: []string{"sh", "-c", "cat /proc/self/status"}, RunBootstrap: true,
		HostFSEnabled: true, HostFSGrafts: []string{"/Users/example/Documents"},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(command, " ")
	script := command[9]
	for _, required := range []string{
		"unshare --mount --pid --fork --kill-child=KILL --mount-proc=/proc",
		"mount --make-rprivate /",
		"mount --bind \"$1\" /hideout/session",
		"mount -t tmpfs -o mode=000,size=4096 hideout-runtime-private /hideout/runtime",
		"/hideout/session/shims/hideout-hostfsd",
		"cleanup_session_view",
		"'setpriv' '--reuid=developer' '--regid=developer' '--init-groups'",
	} {
		if !strings.Contains(joined+"\n"+script, required) {
			t.Fatalf("session view missing %q:\n%s", required, joined)
		}
	}
	if !strings.Contains(command[len(command)-1], ownerID) || strings.Contains(joined, siblingID) {
		t.Fatalf("session source is not owner-specific: %q", command)
	}
	if strings.Contains(joined, "nsenter") || strings.Contains(joined, "--user") || strings.Contains(joined, "--map-root-user") {
		t.Fatalf("session view accidentally claimed a user/root namespace boundary: %q", command)
	}
}

func TestIsolatedCleanupProvesSessionProcessTreeGone(t *testing.T) {
	runner := &sessionViewSetupRunner{}
	b := Backend{SetupRunner: runner, ControlStdout: io.Discard, ControlStderr: io.Discard}
	session := &backend.Session{
		ID: "ses_20260716T120000Z_0123456789abcdef", InstanceName: "hideout-test",
		PreserveInstance: true, RuntimeReady: true, RunAttempted: true, SessionIsolationRequired: true,
	}
	if err := b.Cleanup(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.command, " ")
	for _, required := range []string{"/proc/[0-9]*/environ", "HIDEOUT_SESSION_ID=$session_id", session.ID} {
		if !strings.Contains(joined, required) {
			t.Fatalf("cleanup proof missing %q: %s", required, joined)
		}
	}
	if !strings.Contains(joined, `2>/dev/null <"$env_file"`) || strings.Contains(joined, `<"$env_file" 2>/dev/null`) {
		t.Fatalf("cleanup proof leaves process-exit races on stderr: %s", joined)
	}
}

func TestIsolatedCleanupReusesProofFromOwningTransport(t *testing.T) {
	runner := &sessionViewSetupRunner{err: errors.New("unexpected cleanup reconnect")}
	b := Backend{SetupRunner: runner, ControlStdout: io.Discard, ControlStderr: io.Discard}
	session := &backend.Session{
		ID: "ses_20260716T120000Z_0123456789abcdef", InstanceName: "hideout-test",
		PreserveInstance: true, RuntimeReady: true, RunAttempted: true,
		SessionIsolationRequired: true, IsolationCleanupProved: true,
	}
	if err := b.Cleanup(context.Background(), session); err != nil {
		t.Fatalf("proved cleanup reconnected: %v", err)
	}
	if len(runner.command) != 0 {
		t.Fatalf("proved cleanup issued another command: %q", runner.command)
	}
}

func TestEnvironmentNetworkReuseVerificationChecksCurrentBootAndRuntimeHealth(t *testing.T) {
	runner := &sessionViewSetupRunner{}
	b := Backend{SetupRunner: runner, ControlStdout: io.Discard, ControlStderr: io.Discard}
	session := &backend.Session{
		ID: "ses_20260716T120000Z_0123456789abcdef", InstanceName: "hideout-test",
		ExpectedBootID: "01234567-89ab-cdef-0123-456789abcdef",
	}
	if err := b.VerifyEnvironmentNetwork(context.Background(), session, "/hideout/runtime/services/network", nil); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.command, " ")
	for _, required := range []string{
		"/proc/sys/kernel/random/boot_id", "ip link show dev hideout0", "ip route show default",
		"tun2socks.pid", "dns-stub.pid", "nameserver 127\\.0\\.0\\.1", session.ExpectedBootID,
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("network verification missing %q: %s", required, joined)
		}
	}
}

func TestSessionViewGuestRootIsAnExplicitNonClaim(t *testing.T) {
	command, err := BuildSessionViewCommand(SessionViewSpec{
		SessionID: "ses_20260716T120000Z_0123456789abcdef", TargetUser: "developer",
		GuestWork: "/workspace", Env: []string{"PATH=/usr/bin"}, Command: []string{"true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(command, " ")
	if !strings.Contains(joined, "unshare --mount --pid") || strings.Contains(joined, "--user") {
		t.Fatalf("ordinary-target namespace shape drifted: %q", command)
	}
	if !strings.Contains(command[9], "'setpriv' '--reuid=developer'") {
		t.Fatalf("target is not explicitly dropped to the profile user: %s", command[9])
	}
	if _, err := BuildSessionViewCommand(SessionViewSpec{
		SessionID: "ses_20260716T120000Z_0123456789abcdef", TargetUser: "root",
		GuestWork: "/workspace", Env: []string{"PATH=/usr/bin"}, Command: []string{"true"},
	}); err == nil {
		t.Fatal("Hideout accepted root as the ordinary target identity")
	}
}

func TestSessionViewPrimitiveProbeIsFixedAndFailsClosed(t *testing.T) {
	runner := &sessionViewSetupRunner{err: errors.New("unshare missing")}
	b := Backend{SetupRunner: runner}
	err := b.checkSessionViewPrimitives(context.Background(), &backend.Session{InstanceName: "hideout-test"})
	if err == nil || !strings.Contains(err.Error(), "session isolation primitives unavailable") {
		t.Fatalf("probe error=%v", err)
	}
	if len(runner.command) != 3 || runner.command[0] != "sh" || !strings.Contains(runner.command[2], "unshare --mount --pid") {
		t.Fatalf("probe command=%q", runner.command)
	}
}

type sessionViewSetupRunner struct {
	command []string
	err     error
}

type isolatedRunSetupRunner struct {
	commands      [][]string
	checkErr      error
	targetErr     error
	waitForCancel bool
	targetEntered chan struct{}
}

func (r *isolatedRunSetupRunner) Check(context.Context, string) error { return nil }

func (r *isolatedRunSetupRunner) Run(ctx context.Context, _ string, _ string, _ []string, command []string, _ io.Reader, stdout io.Writer, stderr io.Writer) error {
	r.commands = append(r.commands, append([]string(nil), command...))
	if len(r.commands) == 1 {
		return r.checkErr
	}
	if r.waitForCancel {
		if r.targetEntered == nil {
			r.targetEntered = make(chan struct{})
		}
		close(r.targetEntered)
		<-ctx.Done()
		return ctx.Err()
	}
	_, _ = io.WriteString(stdout, "target-stdout\n")
	_, _ = io.WriteString(stderr, "target-stderr\n")
	return r.targetErr
}

type fakeExitStatusError int

func (e fakeExitStatusError) Error() string   { return "exit status" }
func (e fakeExitStatusError) ExitStatus() int { return int(e) }

func (r *sessionViewSetupRunner) Check(context.Context, string) error { return r.err }

func (r *sessionViewSetupRunner) Run(_ context.Context, _ string, _ string, _ []string, command []string, _ io.Reader, stdout io.Writer, _ io.Writer) error {
	r.command = append([]string(nil), command...)
	if r.err == nil && stdout != nil {
		_, _ = io.WriteString(stdout, "01234567-89ab-cdef-0123-456789abcdef\n")
	}
	return r.err
}
