package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/daemon"
	"github.com/vibe-agi/hideout/internal/inittask"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/recovery"
)

func TestSetupFreshReviewConfirmAndApply(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var stdout, stderr bytes.Buffer
	var ensureCalls, prepareCalls, applyCalls atomic.Int32
	a := app{
		stdin: &bytes.Buffer{}, stdout: &stdout, stderr: &stderr,
		terminalInteractive: func() bool { return true },
		daemonExecutable:    func() (string, error) { return "/test/hideout", nil },
		ensureDaemon: func(context.Context, daemon.EnsureStartedOptions) (daemon.Status, error) {
			ensureCalls.Add(1)
			return daemon.Status{}, nil
		},
	}
	a.stdin = strings.NewReader("yes\n")
	a.initPrepare = func(_ context.Context, _ profile.Store, request manager.InitServiceRequest) (manager.PreparedInit, error) {
		prepareCalls.Add(1)
		if request.Version != manager.InitServiceRequestVersion || request.Mode != manager.InitModeSetup || request.ProfileName != "" {
			t.Fatalf("setup request = %+v", request)
		}
		return setupPreparedFixture(manager.InitStateFresh), nil
	}
	a.initApply = func(_ context.Context, store profile.Store, _ manager.PreparedInit, confirmation *manager.InitConfirmation) (manager.InitApplyResult, error) {
		applyCalls.Add(1)
		if confirmation == nil || !confirmation.Confirmed || confirmation.PlanDigest != "digest" {
			t.Fatalf("confirmation = %+v", confirmation)
		}
		if err := store.Save(profile.Default("default")); err != nil {
			return manager.InitApplyResult{}, err
		}
		return manager.InitApplyResult{Status: "configured", Result: inittask.Result{Version: inittask.Version}}, nil
	}
	if err := a.run([]string{"setup"}); err != nil {
		t.Fatal(err)
	}
	if ensureCalls.Load() != 1 || prepareCalls.Load() != 1 || applyCalls.Load() != 1 {
		t.Fatalf("calls ensure=%d prepare=%d apply=%d", ensureCalls.Load(), prepareCalls.Load(), applyCalls.Load())
	}
	if _, err := profile.DefaultStore(); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	for _, want := range []string{
		"Set up Hideout", "Lima virtual machine", "developer-standard 2026.07.0",
		"read/write at /workspace", "hidden unless you grant access",
		"projects share one default VM", "hideout env create",
		"does not hide your network origin", "Audit: always on",
		"no VM start or runtime download", "Hideout configuration is ready",
		"hideout run -- code .",
		"hideout run -- codex --version",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("setup output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, home) || strings.Contains(output, "token") || strings.Contains(output, "machine-id") {
		t.Fatalf("setup output leaked control or host material: %s", output)
	}
}

func TestSetupNextStepsKeepsOrdinaryJourneyFirst(t *testing.T) {
	var output bytes.Buffer
	writeSetupNextSteps(&output)
	got := output.String()
	ordered := []string{
		"Next:",
		"hideout doctor",
		"cd /path/to/project",
		"hideout run -- git status --short",
		"More:",
		"hideout run -- code .",
		"Privacy later:",
	}
	last := -1
	for _, want := range ordered {
		index := strings.Index(got, want)
		if index < 0 {
			t.Fatalf("next steps missing %q:\n%s", want, got)
		}
		if index <= last {
			t.Fatalf("next steps are out of order at %q:\n%s", want, got)
		}
		last = index
	}
	firstResult := strings.Index(got, "hideout run -- git status --short")
	if firstResult < 0 || strings.Count(got[:firstResult], "hideout ") != 1 {
		t.Fatalf("first result requires more than setup + doctor before run:\n%s", got)
	}
}

func TestSetupCancelAndNonTerminalPerformNoApply(t *testing.T) {
	for _, tc := range []struct {
		name        string
		interactive bool
		input       string
		wantEnsure  int32
	}{
		{name: "negative", interactive: true, input: "n\n", wantEnsure: 1},
		{name: "empty", interactive: true, input: "\n", wantEnsure: 1},
		{name: "eof", interactive: true, input: "", wantEnsure: 1},
		{name: "yes-followed-by-eof", interactive: true, input: "yes", wantEnsure: 1},
		{name: "escape-control", interactive: true, input: "\x1byes\n", wantEnsure: 1},
		{name: "delete-control", interactive: true, input: "\x7fyes\n", wantEnsure: 1},
		{name: "unicode-control", interactive: true, input: "\u0085yes\n", wantEnsure: 1},
		{name: "non-terminal", interactive: false, input: "yes\n", wantEnsure: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			var ensureCalls, applyCalls atomic.Int32
			a := app{
				stdin: strings.NewReader(tc.input), stdout: new(bytes.Buffer), stderr: new(bytes.Buffer),
				terminalInteractive: func() bool { return tc.interactive },
				daemonExecutable:    func() (string, error) { return "/test/hideout", nil },
				ensureDaemon: func(context.Context, daemon.EnsureStartedOptions) (daemon.Status, error) {
					ensureCalls.Add(1)
					return daemon.Status{}, nil
				},
				initPrepare: func(context.Context, profile.Store, manager.InitServiceRequest) (manager.PreparedInit, error) {
					return setupPreparedFixture(manager.InitStateFresh), nil
				},
				initApply: func(context.Context, profile.Store, manager.PreparedInit, *manager.InitConfirmation) (manager.InitApplyResult, error) {
					applyCalls.Add(1)
					return manager.InitApplyResult{}, nil
				},
			}
			if err := a.run([]string{"setup"}); err == nil {
				t.Fatal("setup unexpectedly succeeded")
			}
			if ensureCalls.Load() != tc.wantEnsure || applyCalls.Load() != 0 {
				t.Fatalf("calls ensure=%d apply=%d", ensureCalls.Load(), applyCalls.Load())
			}
			store, err := profile.DefaultStore()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(store.ProfilePath("default")); !os.IsNotExist(err) {
				t.Fatalf("setup cancellation created profile: %v", err)
			}
		})
	}
}

func TestSetupCancellationPreservesDurableStoreState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store, err := profile.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	for relative, content := range map[string]string{
		"profiles/existing/sentinel":                 "profile-state\n",
		"environments/env_existing/environment.json": "environment-state\n",
		"runtime/cache/artifact":                     "runtime-state\n",
		"product-evidence/existing.json":             "evidence-state\n",
		"audit/existing.jsonl":                       "audit-state\n",
		"onboarding/existing.json":                   "onboarding-state\n",
	} {
		path := filepath.Join(store.Root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before, err := snapshotSetupTree(store.Root)
	if err != nil {
		t.Fatal(err)
	}
	var applyCalls atomic.Int32
	a := app{
		stdin: strings.NewReader("no\n"), stdout: new(bytes.Buffer), stderr: new(bytes.Buffer),
		terminalInteractive: func() bool { return true },
		daemonExecutable:    func() (string, error) { return "/test/hideout", nil },
		ensureDaemon: func(context.Context, daemon.EnsureStartedOptions) (daemon.Status, error) {
			return daemon.Status{}, nil
		},
		initPrepare: func(context.Context, profile.Store, manager.InitServiceRequest) (manager.PreparedInit, error) {
			return setupPreparedFixture(manager.InitStateFresh), nil
		},
		initApply: func(context.Context, profile.Store, manager.PreparedInit, *manager.InitConfirmation) (manager.InitApplyResult, error) {
			applyCalls.Add(1)
			return manager.InitApplyResult{}, nil
		},
	}
	if err := a.run([]string{"setup"}); err == nil {
		t.Fatal("cancelled setup unexpectedly succeeded")
	}
	if applyCalls.Load() != 0 {
		t.Fatalf("cancelled setup sent %d apply requests", applyCalls.Load())
	}
	after, err := snapshotSetupTree(store.Root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("cancelled setup changed durable store state:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func TestConfirmSetupStopsOnContextCancellation(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	promptReader, promptWriter := io.Pipe()
	defer promptReader.Close()
	defer promptWriter.Close()
	ctx, cancel := context.WithCancel(context.Background())
	a := app{stdout: promptWriter}
	done := make(chan error, 1)
	go func() {
		_, err := a.confirmSetup(ctx, bufio.NewReader(reader))
		done <- err
	}()
	const prompt = "Set up this configuration? [y/N]: "
	observed := make([]byte, len(prompt))
	if _, err := io.ReadFull(promptReader, observed); err != nil {
		t.Fatalf("read confirmation prompt: %v", err)
	}
	if string(observed) != prompt {
		t.Fatalf("confirmation prompt = %q", observed)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "setup interrupted") {
			t.Fatalf("cancellation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("confirmation remained blocked after cancellation")
	}
}

func TestSetupReadySendsNoApply(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var stdout bytes.Buffer
	a := app{
		stdin: strings.NewReader("yes\n"), stdout: &stdout, stderr: new(bytes.Buffer),
		terminalInteractive: func() bool { return true },
		initPrepare: func(context.Context, profile.Store, manager.InitServiceRequest) (manager.PreparedInit, error) {
			return setupPreparedFixture(manager.InitStateReady), nil
		},
		initApply: func(context.Context, profile.Store, manager.PreparedInit, *manager.InitConfirmation) (manager.InitApplyResult, error) {
			t.Fatal("ready setup sent apply")
			return manager.InitApplyResult{}, nil
		},
	}
	if err := a.run([]string{"setup"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Already set up") || strings.Contains(stdout.String(), "Set up this configuration?") {
		t.Fatalf("ready output = %s", stdout.String())
	}
	if strings.Contains(stdout.String(), "Backend:") {
		t.Fatalf("ready output invented a persisted backend choice: %s", stdout.String())
	}
}

func TestSetupRejectsEveryNonHelpArgument(t *testing.T) {
	for _, args := range [][]string{{"setup", "--yes"}, {"setup", "--force"}, {"setup", "extra"}} {
		var stdout bytes.Buffer
		a := app{stdin: strings.NewReader("yes\n"), stdout: &stdout, stderr: new(bytes.Buffer)}
		if err := a.run(args); err == nil {
			t.Fatalf("%v unexpectedly accepted", args)
		}
	}
}

func TestInitCommandUsesOnlyDaemonPlanApplyForAdvancedAndDryRun(t *testing.T) {
	for _, tc := range []struct {
		name      string
		args      []string
		input     string
		wantApply int32
	}{
		{
			name:      "non-interactive advanced",
			args:      []string{"init", "--profile", "advanced", "--template", "dev", "--backend", "native", "--network", "direct", "--hostfs-visibility", "none", "--no-input"},
			wantApply: 1,
		},
		{
			name:  "interactive advanced",
			args:  []string{"init", "--profile", "advanced", "--template", "dev", "--backend", "native", "--network", "direct", "--hostfs-visibility", "none"},
			input: "yes\n", wantApply: 1,
		},
		{
			name:      "dry-run",
			args:      []string{"init", "--profile", "advanced", "--template", "dev", "--backend", "native", "--network", "direct", "--hostfs-visibility", "none", "--no-input", "--dry-run"},
			wantApply: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			var ensureCalls, prepareCalls, applyCalls atomic.Int32
			var stdout bytes.Buffer
			a := app{
				stdin: strings.NewReader(tc.input), stdout: &stdout, stderr: new(bytes.Buffer),
				daemonExecutable: func() (string, error) { return "/test/hideout", nil },
				ensureDaemon: func(context.Context, daemon.EnsureStartedOptions) (daemon.Status, error) {
					ensureCalls.Add(1)
					return daemon.Status{}, nil
				},
				initPrepare: func(_ context.Context, _ profile.Store, request manager.InitServiceRequest) (manager.PreparedInit, error) {
					prepareCalls.Add(1)
					if request.Mode != manager.InitModeInit || request.ProfileName != "advanced" || request.Backend != "native" || request.Network != "direct" {
						t.Fatalf("advanced request = %+v", request)
					}
					prepared := setupPreparedFixture(manager.InitStateFresh)
					prepared.Request = request
					prepared.Review.Mode = manager.InitModeInit
					prepared.Review.RequiresConfirmation = !request.NoInput
					prepared.Plan.Profile = "advanced"
					prepared.Plan.Backend = "native"
					return prepared, nil
				},
				initApply: func(_ context.Context, _ profile.Store, prepared manager.PreparedInit, confirmation *manager.InitConfirmation) (manager.InitApplyResult, error) {
					applyCalls.Add(1)
					if prepared.Request.Mode != manager.InitModeInit {
						t.Fatalf("applied mode = %s", prepared.Request.Mode)
					}
					if prepared.Request.NoInput && confirmation != nil {
						t.Fatalf("non-interactive init sent confirmation: %+v", confirmation)
					}
					if !prepared.Request.NoInput && (confirmation == nil || !confirmation.Confirmed) {
						t.Fatalf("interactive init confirmation = %+v", confirmation)
					}
					return manager.InitApplyResult{Status: "configured", Result: inittask.Result{Version: inittask.Version, Plan: prepared.Plan}}, nil
				},
			}
			if err := a.run(tc.args); err != nil {
				t.Fatal(err)
			}
			if ensureCalls.Load() != 1 || prepareCalls.Load() != 1 || applyCalls.Load() != tc.wantApply {
				t.Fatalf("calls ensure=%d prepare=%d apply=%d", ensureCalls.Load(), prepareCalls.Load(), applyCalls.Load())
			}
		})
	}
}

func TestCodedInitErrorUsesTypedDaemonBoundary(t *testing.T) {
	typed := codedInitError(fmt.Errorf("%w: stale socket", errInitDaemonUnavailable))
	if typed == nil || !strings.Contains(typed.Error(), recovery.CodeSetupDaemonUnavailable) {
		t.Fatalf("typed daemon recovery = %v", typed)
	}
	untyped := codedInitError(errors.New("user profile contains the word daemon"))
	if strings.Contains(untyped.Error(), recovery.CodeSetupDaemonUnavailable) {
		t.Fatalf("untyped text was misclassified as daemon recovery: %v", untyped)
	}
}

func TestInitDaemonRequestAuthenticatesAndDecodesStrictly(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "hideout-038-client-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	runtimeDir := filepath.Join(root, "daemon")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "token"), []byte("operator\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", daemon.SocketPath(root))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer operator" || r.Host != "localhost" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version": manager.APIVersion, "resource": "init/plan", "errors": []string{},
			"data": setupPreparedFixture(manager.InitStateFresh),
		})
	})}
	go server.Serve(listener)
	defer server.Close()

	var prepared manager.PreparedInit
	if err := initDaemonRequest(context.Background(), root, "/api/v1/init/plan", manager.InitAPIRequest{
		Request: ptrSetupRequest(),
	}, &prepared); err != nil {
		t.Fatal(err)
	}
	if prepared.Review.PlanDigest != "digest" {
		t.Fatalf("prepared = %+v", prepared)
	}

	if err := os.Remove(filepath.Join(runtimeDir, "token")); err != nil {
		t.Fatal(err)
	}
	if err := initDaemonRequest(context.Background(), root, "/api/v1/init/plan", manager.InitAPIRequest{}, &prepared); err == nil {
		t.Fatal("missing token unexpectedly accepted")
	}
}

func TestInitDaemonRequestHonorsCancellation(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "hideout-038-cancel-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	runtimeDir := filepath.Join(root, "daemon")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "token"), []byte("operator\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", daemon.SocketPath(root))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})}
	go server.Serve(listener)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	var prepared manager.PreparedInit
	err = initDaemonRequest(ctx, root, "/api/v1/init/plan", manager.InitAPIRequest{Request: ptrSetupRequest()}, &prepared)
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("err = %v", err)
	}
}

func TestInitDaemonRequestClassifiesTransportAndProtocolFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body       string
		wantDaemon bool
	}{
		{name: "authentication", status: http.StatusUnauthorized, body: `{"errors":["unauthorized"]}`, wantDaemon: true},
		{name: "missing route", status: http.StatusNotFound, body: `{"errors":["not found"]}`, wantDaemon: true},
		{name: "malformed response", status: http.StatusOK, body: `{`, wantDaemon: true},
		{name: "business request", status: http.StatusBadRequest, body: `{"errors":["invalid setup request"]}`, wantDaemon: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := os.MkdirTemp("/tmp", "hideout-038-protocol-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(root)
			runtimeDir := filepath.Join(root, "daemon")
			if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(runtimeDir, "token"), []byte("operator\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("unix", daemon.SocketPath(root))
			if err != nil {
				t.Fatal(err)
			}
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})}
			go server.Serve(listener)
			defer server.Close()
			var prepared manager.PreparedInit
			err = initDaemonRequest(context.Background(), root, "/api/v1/init/plan", manager.InitAPIRequest{Request: ptrSetupRequest()}, &prepared)
			if err == nil {
				t.Fatal("failure response unexpectedly succeeded")
			}
			if errors.Is(err, errInitDaemonUnavailable) != tc.wantDaemon {
				t.Fatalf("error=%v daemon=%v want=%v", err, errors.Is(err, errInitDaemonUnavailable), tc.wantDaemon)
			}
		})
	}
}

func TestSetupBlockedAndRepairableRenderRecoveryWithoutApply(t *testing.T) {
	for _, tc := range []struct {
		name     string
		state    string
		notice   manager.InitNotice
		wantCode string
	}{
		{
			name: "repairable", state: manager.InitStateRepairable,
			notice: manager.InitNotice{
				Code:    "setup.profile.repair-required",
				Summary: "profile metadata or identity state is incomplete",
				Action:  "hideout doctor --fix --dry-run --profile default --backend lima",
			},
			wantCode: recovery.CodeSetupProfileRepairRequired,
		},
		{
			name: "blocked", state: manager.InitStateBlocked,
			notice: manager.InitNotice{
				Code:    "setup.profile.blocked",
				Summary: "profile state is malformed or unsupported",
				Action:  "hideout doctor --profile default --backend lima",
			},
			wantCode: recovery.CodeSetupProfileBlocked,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			var stdout, stderr bytes.Buffer
			var applyCalls atomic.Int32
			a := app{
				stdin: strings.NewReader("yes\n"), stdout: &stdout, stderr: &stderr,
				terminalInteractive: func() bool { return true },
				daemonExecutable:    func() (string, error) { return "/test/hideout", nil },
				ensureDaemon: func(context.Context, daemon.EnsureStartedOptions) (daemon.Status, error) {
					return daemon.Status{}, nil
				},
			}
			a.initPrepare = func(context.Context, profile.Store, manager.InitServiceRequest) (manager.PreparedInit, error) {
				prepared := setupPreparedFixture(tc.state)
				prepared.Review.Notices = []manager.InitNotice{tc.notice}
				return prepared, nil
			}
			a.initApply = func(context.Context, profile.Store, manager.PreparedInit, *manager.InitConfirmation) (manager.InitApplyResult, error) {
				applyCalls.Add(1)
				return manager.InitApplyResult{}, nil
			}
			err := a.run([]string{"setup"})
			if err == nil || !strings.Contains(err.Error(), tc.wantCode) {
				t.Fatalf("setup error = %v, want recovery code %s", err, tc.wantCode)
			}
			if applyCalls.Load() != 0 {
				t.Fatalf("recovery path reached apply %d times", applyCalls.Load())
			}
			output := stdout.String()
			for _, want := range []string{"Setup needs attention", tc.notice.Summary, tc.notice.Action} {
				if !strings.Contains(output, want) {
					t.Fatalf("recovery output missing %q:\n%s", want, output)
				}
			}
			if strings.Contains(output, "Set up this configuration?") {
				t.Fatalf("recovery path prompted for confirmation:\n%s", output)
			}
			store, storeErr := profile.DefaultStore()
			if storeErr != nil {
				t.Fatal(storeErr)
			}
			if _, statErr := os.Lstat(store.ProfilePath("default")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("recovery path touched the profile: %v", statErr)
			}
		})
	}
}

func ptrSetupRequest() *manager.InitServiceRequest {
	request := manager.SetupInitServiceRequest()
	return &request
}

func setupPreparedFixture(state string) manager.PreparedInit {
	return manager.PreparedInit{
		Request: manager.SetupInitServiceRequest(),
		Review: manager.InitReview{
			Version: manager.InitReviewVersion, PlanVersion: inittask.Version, PlanDigest: "digest",
			Mode: manager.InitModeSetup, State: state, RequiresConfirmation: state == manager.InitStateFresh,
			Profile: "default", Template: "dev", Backend: "lima", Network: "direct",
			Runtime: manager.InitRuntimeReview{
				Family: "developer-standard", Revision: "2026.07.0", Status: "preview", DownloadBytes: 1 << 30,
			},
			Workspace:  manager.InitWorkspaceReview{GuestPath: "/workspace", Mode: "read-write"},
			OtherFiles: "hidden-unless-granted", Audit: "always-on",
		},
		Plan:        inittask.Plan{Version: inittask.Version, Profile: "default", Backend: "lima", Network: "direct"},
		Observation: manager.InitObservation{State: state},
	}
}

type setupTreeEntry struct {
	Mode    os.FileMode
	ModTime time.Time
	Digest  string
}

func snapshotSetupTree(root string) (map[string]setupTreeEntry, error) {
	out := map[string]setupTreeEntry{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entry := setupTreeEntry{Mode: info.Mode(), ModTime: info.ModTime()}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			entry.Digest = hex.EncodeToString(sum[:])
		}
		out[relative] = entry
		return nil
	})
	return out, err
}
