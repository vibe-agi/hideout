package lima

import (
	"bufio"
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
	"golang.org/x/term"
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
	if !session.IsolationRunStarted {
		t.Fatal("isolated target start was not recorded")
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
	if session.IsolationRunStarted {
		t.Fatal("failed preflight recorded an isolated target start")
	}

	brokenView := &isolatedRunSetupRunner{checkErr: fakeExitStatusError(32), checkStderr: "mount point does not exist\n"}
	err = (Backend{SetupRunner: brokenView, ControlStderr: io.Discard}).Run(context.Background(), session, []string{"sh"}, []string{"PATH=/usr/bin:/bin"})
	if errors.As(err, &notFound) || !strings.Contains(err.Error(), "session isolation command preflight failed") ||
		!strings.Contains(err.Error(), "mount point does not exist") {
		t.Fatalf("view failure was misclassified: %T %v", err, err)
	}
}

func TestPortalWorkspaceRejectsLegacyDirectRunBeforeCreatingASecondView(t *testing.T) {
	runner := &isolatedRunSetupRunner{}
	session := &backend.Session{
		ID: "ses_20260716T120000Z_0123456789abcdef", InstanceName: "hideout-test",
		ConfigPath: "/unused/lima.yaml", GuestWork: "/workspace", RuntimeReady: true, SessionIsolationRequired: true,
		Workspace: backend.WorkspaceAttachmentSpec{Transport: backend.WorkspaceTransportPortal},
	}
	err := (Backend{SetupRunner: runner}).Run(context.Background(), session, []string{"sh"}, []string{"PATH=/usr/bin:/bin"})
	if err == nil || !strings.Contains(err.Error(), "requires the daemon session stream") {
		t.Fatalf("Portal direct-run error=%v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("Portal direct run created duplicate views: %q", runner.commands)
	}
}

func TestIsolatedPTYRunNegotiatesBeforeGuardianAndReusesOneSSHTransport(t *testing.T) {
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
	var ptyNegotiated atomic.Bool
	var guardianStarted atomic.Bool
	var targetStarted atomic.Bool
	var protocolFailed atomic.Bool
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
							if request.Type == "pty-req" {
								if guardianStarted.Load() {
									protocolFailed.Store(true)
								}
								ptyNegotiated.Store(true)
								_ = request.Reply(true, nil)
								continue
							}
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
								if !ptyNegotiated.Load() {
									protocolFailed.Store(true)
								}
								guardianStarted.Store(true)
								_, _ = io.WriteString(channel, "ready\n")
								_, _ = io.Copy(io.Discard, channel)
							} else if strings.Contains(payload.Command, "hideout-session-launcher") {
								if !guardianStarted.Load() {
									protocolFailed.Store(true)
								}
								targetStarted.Store(true)
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
	stdin, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	oldIsTerminal, oldGetSize := terminalIsTerminal, terminalGetSize
	oldMakeRaw, oldRestore := terminalMakeRaw, terminalRestore
	terminalIsTerminal = func(int) bool { return true }
	terminalGetSize = func(int) (int, int, error) { return 132, 43, nil }
	terminalMakeRaw = func(int) (*term.State, error) { return &term.State{}, nil }
	terminalRestore = func(int, *term.State) error { return nil }
	t.Cleanup(func() {
		terminalIsTerminal, terminalGetSize = oldIsTerminal, oldGetSize
		terminalMakeRaw, terminalRestore = oldMakeRaw, oldRestore
	})
	b := Backend{Stdin: stdin, Stdout: stdout, Stderr: io.Discard, ControlStdout: io.Discard, ControlStderr: io.Discard}
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
	if protocolFailed.Load() || !ptyNegotiated.Load() || !guardianStarted.Load() || !targetStarted.Load() {
		t.Fatalf("pty protocol failed=%v negotiated=%v guardian=%v target=%v", protocolFailed.Load(), ptyNegotiated.Load(), guardianStarted.Load(), targetStarted.Load())
	}
}

func TestSessionGuardianIsCoreOwnedExactSessionAndFailClosed(t *testing.T) {
	command, err := SessionGuardianCommand("ses_20260716T120000Z_0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(command, " ")
	for _, required := range []string{"trap '' HUP INT TERM", `printf 'ready\n'`, `read -r -t 2 outcome`, "ping)", "done)", sessionGuardianRoot, "/proc/$pid/stat", "/proc/$pid/cmdline", "current_start", "grep -Fqx", "kill -KILL", "hideout-session-guardian"} {
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
	reader, writer := io.Pipe()
	heartbeat := startSessionGuardianHeartbeat(writer)
	lines := bufio.NewReader(reader)
	line, err := lines.ReadString('\n')
	if err != nil || line != "ping\n" {
		t.Fatalf("first heartbeat=%q err=%v", line, err)
	}
	heartbeat.finish <- true
	line, err = lines.ReadString('\n')
	if err != nil || line != "done\n" {
		t.Fatalf("completion=%q err=%v", line, err)
	}
	if _, err := lines.ReadByte(); !errors.Is(err, io.EOF) {
		t.Fatalf("completion did not close stream: %v", err)
	}
	if err := <-heartbeat.done; err != nil {
		t.Fatalf("heartbeat completion: %v", err)
	}

	reader, writer = io.Pipe()
	heartbeat = startSessionGuardianHeartbeat(writer)
	lines = bufio.NewReader(reader)
	if line, err = lines.ReadString('\n'); err != nil || line != "ping\n" {
		t.Fatalf("disconnect first heartbeat=%q err=%v", line, err)
	}
	heartbeat.finish <- false
	if _, err := lines.ReadByte(); !errors.Is(err, io.EOF) {
		t.Fatalf("disconnect did not close heartbeat stream: %v", err)
	}
	if err := <-heartbeat.done; err != nil {
		t.Fatalf("heartbeat disconnect: %v", err)
	}
}

func TestNormalizeGuardianHeartbeatCompletionIgnoresOnlyExpectedNormalClose(t *testing.T) {
	closed := &os.PathError{Op: "close", Path: "|1", Err: os.ErrClosed}
	if err := normalizeGuardianHeartbeatCompletion(true, closed); err != nil {
		t.Fatalf("normal closed heartbeat=%v", err)
	}
	if err := normalizeGuardianHeartbeatCompletion(false, closed); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("abnormal closed heartbeat=%v", err)
	}
	failure := errors.New("heartbeat protocol failed")
	if err := normalizeGuardianHeartbeatCompletion(true, failure); !errors.Is(err, failure) {
		t.Fatalf("normal non-close heartbeat=%v", err)
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
		RequiredRuntimePaths: []string{
			GuestBootstrapPath,
			GuestSessionDir + "/network/bootstrap.sh",
			GuestSessionDir + "/" + backend.ProjectionReadinessManifestFile,
		},
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
		"ln -s machine/identity.json /hideout/profile/identity.json",
		"readlink /hideout/profile/identity.json",
		"session runtime files did not become visible",
		"'test' '-x' '/hideout/session/bootstrap/bootstrap.sh'",
		"'test' '-x' '/hideout/session/network/bootstrap.sh'",
		"'test' '-f' '/hideout/session/projection-readiness.json'",
		"'test' '!' '-L' '/hideout/session/projection-readiness.json'",
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
	if strings.Contains(script, "rm -f /hideout/profile/identity.json") {
		t.Fatalf("concurrent session identity projection must not remove a sibling's link:\n%s", script)
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
		{"runtime prerequisite escape", func(s *SessionViewSpec) { s.RequiredRuntimePaths = []string{"/tmp/operator.sh"} }},
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

func TestSessionRuntimePrerequisitesDescribeExactSessionFiles(t *testing.T) {
	if deadline := time.Duration(sessionRuntimeReadyAttempts) * sessionRuntimeReadyDelay; deadline != backend.MaxProjectionReadinessDeadline {
		t.Fatalf("session readiness visibility deadline=%s want %s", deadline, backend.MaxProjectionReadinessDeadline)
	}
	session := &backend.Session{
		NetworkBootstrapGuestPath: GuestSessionDir + "/network/bootstrap.sh",
		NetworkCleanupGuestPath:   GuestSessionDir + "/network/cleanup.sh",
		HostFSEnabled:             true,
		ProjectionReadiness:       &backend.ProjectionReadinessExpectation{},
	}
	got := sessionRuntimePrerequisites(session, true)
	want := []string{
		GuestBootstrapPath,
		GuestSessionDir + "/network/bootstrap.sh",
		GuestSessionDir + "/network/cleanup.sh",
		GuestSessionDir + "/shims/hideout-hostfsd",
		GuestSessionSupervisorPath,
		GuestSessionDir + "/" + backend.ProjectionReadinessManifestFile,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runtime prerequisites=%q want=%q", got, want)
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
		"mount -t tmpfs -o mode=0700,size=128m hideout-runtime-private /hideout/runtime",
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

func TestSessionViewConstructsPrivatePortalWorkspaceIdentity(t *testing.T) {
	workspaceID := "wrk_" + strings.Repeat("a", 64)
	physicalRoot := "/hideout/workspaces/" + workspaceID
	workspace := backend.WorkspaceAttachmentSpec{
		HostRoot: "/Users/alice/private/project", GuestRoot: "/workspace",
		Transport: backend.WorkspaceTransportPortal,
		Portal: &backend.WorkspacePortalBinding{
			PhysicalGuestRoot:   physicalRoot,
			Endpoint:            "host.lima.internal:43127",
			CredentialGuestPath: "/hideout/session/workspace/credential.bin",
		},
	}
	command, err := BuildSessionViewCommand(SessionViewSpec{
		SessionID: "ses_20260716T120000Z_0123456789abcdef", TargetUser: "developer",
		GuestWork: "/workspace", Env: []string{"PATH=/hideout/session/shims:/usr/bin", "PWD=/caller-controlled"},
		Command: []string{"git", "status"}, Workspace: workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(command, " ")
	for _, required := range []string{
		"mkdir -p /hideout/workspaces",
		"mount -t tmpfs -o mode=0711,size=16m hideout-workspaces-private /hideout/workspaces",
		"/hideout/session/shims/hideout-workspace-portal",
		"'--endpoint' 'host.lima.internal:43127'",
		"'--credential-file' '/hideout/session/workspace/credential.bin'",
		"'--mount' '" + physicalRoot + "'",
		"workspace_physical=\"$workspace_root\"'" + physicalRoot + "'",
		"mount --rbind '" + physicalRoot + "' \"$workspace_physical\"",
		"ln -s '" + physicalRoot + "' \"$workspace_root/workspace\"",
		"'PWD=/workspace'",
		"for hostfs_root in Users Volumes private; do",
		"ln -s \"/hideout/hostfs/$hostfs_root\" \"$workspace_root/$hostfs_root\"",
		"'chroot' '/hideout/runtime/workspace-rootfs'",
		`[ "$workspace_attempt" -lt 2000 ]`,
		`[ "$workspace_stop_attempt" -lt 1000 ]`,
		"umount -R '/hideout/runtime/workspace-rootfs'",
		"umount '" + physicalRoot + "'",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("Portal session view missing %q:\n%s", required, joined)
		}
	}
	if strings.Index(joined, "mkdir -p /hideout/workspaces") > strings.Index(joined, "mount -t tmpfs -o mode=0711,size=16m hideout-workspaces-private /hideout/workspaces") {
		t.Fatal("Portal staging root was mounted before its private mount point existed")
	}
	if strings.Contains(joined, workspace.HostRoot) || strings.Contains(joined, "/Users/alice") {
		t.Fatalf("Portal session command exposed host root: %s", joined)
	}
	if strings.Contains(joined, "PWD=/caller-controlled") {
		t.Fatalf("Portal session retained a caller-controlled logical cwd: %s", joined)
	}
}

func TestSessionViewRejectsIncompletePortalWorkspace(t *testing.T) {
	workspace := backend.WorkspaceAttachmentSpec{
		HostRoot: t.TempDir(), GuestRoot: "/workspace", Transport: backend.WorkspaceTransportPortal,
	}
	_, err := BuildSessionViewCommand(SessionViewSpec{
		SessionID: "ses_20260716T120000Z_0123456789abcdef", TargetUser: "developer",
		GuestWork: "/workspace", Command: []string{"true"}, Workspace: workspace,
	})
	if err == nil {
		t.Fatal("incomplete Portal workspace was accepted")
	}
}

func TestIsolatedCleanupProvesSessionProcessTreeGone(t *testing.T) {
	runner := &sessionViewSetupRunner{}
	b := Backend{SetupRunner: runner, ControlStdout: io.Discard, ControlStderr: io.Discard}
	session := &backend.Session{
		ID: "ses_20260716T120000Z_0123456789abcdef", InstanceName: "hideout-test",
		PreserveInstance: true, RuntimeReady: true, RunAttempted: true,
		SessionIsolationRequired: true, IsolationRunStarted: true,
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

func TestIsolatedCleanupSkipsGuestProofBeforeTargetStarts(t *testing.T) {
	runner := &sessionViewSetupRunner{err: errors.New("unexpected cleanup proof")}
	b := Backend{SetupRunner: runner, ControlStdout: io.Discard, ControlStderr: io.Discard}
	session := &backend.Session{
		ID: "ses_20260716T120000Z_0123456789abcdef", InstanceName: "hideout-test",
		PreserveInstance: true, RuntimeReady: true, RunAttempted: true,
		SessionIsolationRequired: true,
	}
	if err := b.Cleanup(context.Background(), session); err != nil {
		t.Fatalf("pre-target cleanup: %v", err)
	}
	if len(runner.command) != 0 {
		t.Fatalf("pre-target cleanup issued a guest proof: %q", runner.command)
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
		`network_dir="$service_dir/network"`, `"$network_dir/tun2socks.pid"`,
		`"$network_dir/dns-stub.pid"`, `"$network_dir/dns-stub.ready"`,
		`"$network_dir"/local-bypass-*-route.after`,
		`ip route get "$route_host"`, `*' dev hideout0'|*' dev hideout0 '*`,
		`[ "$bypass_count" -gt 0 ]`, "nameserver 127\\.0\\.0\\.1", session.ExpectedBootID,
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("network verification missing %q: %s", required, joined)
		}
	}
}

func TestEnvironmentNetworkDNSReconfigureSeparatesHelperAndStateDirectories(t *testing.T) {
	runner := &sessionViewSetupRunner{}
	b := Backend{SetupRunner: runner, ControlStdout: io.Discard, ControlStderr: io.Discard}
	session := &backend.Session{
		ID: "ses_20260716T120000Z_0123456789abcdef", InstanceName: "hideout-test",
		ExpectedBootID: "01234567-89ab-cdef-0123-456789abcdef",
	}
	if err := b.ReconfigureEnvironmentNetworkDNS(
		context.Background(), session, "/hideout/runtime/services/network", "1.1.1.1", "9.9.9.9", nil,
	); err != nil {
		t.Fatal(err)
	}
	if len(runner.command) < 3 {
		t.Fatalf("DNS reconfiguration command=%q", runner.command)
	}
	if out, err := exec.Command("sh", "-n", "-c", runner.command[2]).CombinedOutput(); err != nil {
		t.Fatalf("DNS reconfiguration has invalid shell syntax: %v\n%s\n%s", err, out, runner.command[2])
	}
	joined := strings.Join(runner.command, " ")
	for _, required := range []string{
		`network_dir="$service_dir/network"`,
		`helper="$service_dir/hideout-dns-stub"`,
		`pid_file="$network_dir/dns-stub.pid"`,
		`ready_file="$network_dir/dns-stub.ready"`,
		`--ready-file "$ready_file"`,
		`[ "$(sed -n '1p' "$ready_file")" != "$candidate_pid" ]`,
		`wait "$candidate_pid"`,
		`"$network_dir/dns-stub.log"`,
		`"$network_dir/mediated-resolver"`,
		"/proc/sys/kernel/random/boot_id",
		session.ExpectedBootID,
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("DNS reconfiguration missing %q: %s", required, joined)
		}
	}
}

func TestEnvironmentNetworkDNSVerifierBindsExactBootAndResolver(
	t *testing.T,
) {
	runner := &sessionViewSetupRunner{}
	b := Backend{
		SetupRunner:   runner,
		ControlStdout: io.Discard,
		ControlStderr: io.Discard,
	}
	session := &backend.Session{
		ID:           "ses_20260716T120000Z_0123456789abcdef",
		InstanceName: "hideout-test",
		ExpectedBootID: "01234567-89ab-cdef-0123-" +
			"456789abcdef",
	}
	if err := b.VerifyEnvironmentNetworkDNS(
		context.Background(),
		session,
		"/hideout/runtime/services/network",
		"9.9.9.9",
		nil,
	); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.command, " ")
	for _, required := range []string{
		"/proc/sys/kernel/random/boot_id",
		session.ExpectedBootID,
		`"$network_dir/mediated-resolver"`,
		"9.9.9.9",
		`"$network_dir/dns-stub.pid"`,
		`"$network_dir/dns-stub.ready"`,
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf(
				"DNS verification missing %q: %s",
				required,
				joined,
			)
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
	checkStderr   string
	targetErr     error
	waitForCancel bool
	targetEntered chan struct{}
}

func (r *isolatedRunSetupRunner) Check(context.Context, string) error { return nil }

func (r *isolatedRunSetupRunner) Run(ctx context.Context, _ string, _ string, _ []string, command []string, _ io.Reader, stdout io.Writer, stderr io.Writer) error {
	r.commands = append(r.commands, append([]string(nil), command...))
	if len(r.commands) == 1 {
		_, _ = io.WriteString(stderr, r.checkStderr)
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
