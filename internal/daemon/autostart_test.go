package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/manager"
)

func TestEnsureStartedReturnsAuthenticatedReadyDaemonWithoutStarting(t *testing.T) {
	store := testStore(t)
	var starts atomic.Int32
	status, err := EnsureStarted(context.Background(), EnsureStartedOptions{
		Store: store,
		Probe: func(context.Context, string) (Status, error) {
			return readyDaemonStatus(store.Root), nil
		},
		Starter: func(DaemonStartRequest) error {
			starts.Add(1)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("EnsureStarted: %v", err)
	}
	if status.State != "serving" || starts.Load() != 0 {
		t.Fatalf("status=%+v starts=%d", status, starts.Load())
	}
}

func TestEnsureStartedReplacesIdleDaemonFromAnotherBuildOnly(t *testing.T) {
	newBuild := strings.Repeat("b", 64)
	oldBuild := strings.Repeat("a", 64)

	t.Run("live-sessions-keep-fail-closed", func(t *testing.T) {
		store := testStore(t)
		var starts, stops atomic.Int32
		_, err := EnsureStarted(context.Background(), EnsureStartedOptions{
			Store: store, BuildID: newBuild, Timeout: time.Second,
			Probe: func(context.Context, string) (Status, error) {
				status := readyDaemonStatus(store.Root)
				status.BuildID = oldBuild
				status.Sessions = []SessionStatus{{ID: "session-active"}}
				return status, nil
			},
			Stopper: func(context.Context, string) error {
				stops.Add(1)
				return nil
			},
			Starter: func(DaemonStartRequest) error {
				starts.Add(1)
				return nil
			},
		})
		if err == nil || !strings.Contains(err.Error(), "hideout daemon stop") {
			t.Fatalf("EnsureStarted error = %v", err)
		}
		if starts.Load() != 0 || stops.Load() != 0 {
			t.Fatalf("live-session daemon was disturbed: starts=%d stops=%d", starts.Load(), stops.Load())
		}
	})

	t.Run("idle-daemon-is-replaced", func(t *testing.T) {
		store := testStore(t)
		var starts, stops atomic.Int32
		var stopped, started atomic.Bool
		status, err := EnsureStarted(context.Background(), EnsureStartedOptions{
			Store: store, BuildID: newBuild, Timeout: 2 * time.Second, PollInterval: time.Millisecond,
			Probe: func(context.Context, string) (Status, error) {
				if !stopped.Load() {
					status := readyDaemonStatus(store.Root)
					status.BuildID = oldBuild
					return status, nil
				}
				if !started.Load() {
					return Status{}, errors.New("no daemon is serving")
				}
				status := readyDaemonStatus(store.Root)
				status.BuildID = newBuild
				return status, nil
			},
			Stopper: func(context.Context, string) error {
				stops.Add(1)
				stopped.Store(true)
				return nil
			},
			Starter: func(DaemonStartRequest) error {
				starts.Add(1)
				started.Store(true)
				return nil
			},
		})
		if err != nil {
			t.Fatalf("EnsureStarted: %v", err)
		}
		if status.BuildID != newBuild {
			t.Fatalf("status build = %q, want the replacing CLI build", status.BuildID)
		}
		if stops.Load() != 1 || starts.Load() != 1 {
			t.Fatalf("replacement path stops=%d starts=%d", stops.Load(), starts.Load())
		}
	})

	t.Run("stop-failure-keeps-fail-closed", func(t *testing.T) {
		store := testStore(t)
		var starts atomic.Int32
		_, err := EnsureStarted(context.Background(), EnsureStartedOptions{
			Store: store, BuildID: newBuild, Timeout: time.Second,
			Probe: func(context.Context, string) (Status, error) {
				status := readyDaemonStatus(store.Root)
				status.BuildID = oldBuild
				return status, nil
			},
			Stopper: func(context.Context, string) error {
				return errors.New("ordered shutdown refused")
			},
			Starter: func(DaemonStartRequest) error {
				starts.Add(1)
				return nil
			},
		})
		if err == nil || !strings.Contains(err.Error(), "hideout daemon stop") || !strings.Contains(err.Error(), "automatic replacement failed") {
			t.Fatalf("EnsureStarted error = %v", err)
		}
		if starts.Load() != 0 {
			t.Fatalf("failed replacement caused %d competing starts", starts.Load())
		}
	})
}

func TestEnsureStartedRejectsDaemonWithoutBuildIdentity(t *testing.T) {
	store := testStore(t)
	var starts atomic.Int32
	_, err := EnsureStarted(context.Background(), EnsureStartedOptions{
		Store: store, BuildID: strings.Repeat("b", 64), Timeout: time.Second,
		Probe: func(context.Context, string) (Status, error) {
			status := readyDaemonStatus(store.Root)
			status.BuildID = ""
			return status, nil
		},
		Starter: func(DaemonStartRequest) error {
			starts.Add(1)
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "unidentified") || !strings.Contains(err.Error(), "hideout daemon stop") {
		t.Fatalf("EnsureStarted error = %v", err)
	}
	if starts.Load() != 0 {
		t.Fatalf("unidentified daemon caused %d competing starts", starts.Load())
	}
}

func TestEnsureStartedConcurrentClientsCreateOneProcess(t *testing.T) {
	store := testStore(t)
	var ready atomic.Bool
	var starts atomic.Int32
	opts := EnsureStartedOptions{
		Store:        store,
		Timeout:      2 * time.Second,
		PollInterval: time.Millisecond,
		Probe: func(context.Context, string) (Status, error) {
			if !ready.Load() {
				return Status{}, errors.New("not ready")
			}
			return readyDaemonStatus(store.Root), nil
		},
		Starter: func(DaemonStartRequest) error {
			starts.Add(1)
			time.Sleep(20 * time.Millisecond)
			ready.Store(true)
			return nil
		},
	}

	const clients = 24
	start := make(chan struct{})
	errs := make(chan error, clients)
	var wg sync.WaitGroup
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := EnsureStarted(context.Background(), opts)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent EnsureStarted: %v", err)
		}
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("starter calls = %d, want 1", got)
	}
}

func TestEnsureStartedUsesHiddenRoleAndSelectedStore(t *testing.T) {
	store := testStore(t)
	var ready atomic.Bool
	var request DaemonStartRequest
	status, err := EnsureStarted(context.Background(), EnsureStartedOptions{
		Store:        store,
		Executable:   "/opt/hideout/bin/hideout",
		Timeout:      time.Second,
		PollInterval: time.Millisecond,
		Probe: func(context.Context, string) (Status, error) {
			if !ready.Load() {
				return Status{}, errors.New("not ready")
			}
			return readyDaemonStatus(store.Root), nil
		},
		Starter: func(req DaemonStartRequest) error {
			request = req
			ready.Store(true)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("EnsureStarted: %v", err)
	}
	if status.State != "serving" {
		t.Fatalf("status = %+v", status)
	}
	if request.Executable != "/opt/hideout/bin/hideout" {
		t.Fatalf("executable = %q", request.Executable)
	}
	if len(request.Args) != 1 || request.Args[0] != InternalDaemonServeCommand {
		t.Fatalf("args = %#v", request.Args)
	}
	if got := environmentValue(request.Env, "HIDEOUT_STORE_ROOT"); got != store.Root {
		t.Fatalf("HIDEOUT_STORE_ROOT = %q, want %q", got, store.Root)
	}
}

func TestEnsureStartedWaitsForExistingDaemonLockWithoutCompeting(t *testing.T) {
	store := testStore(t)
	dir, err := ensurePlacement(store.Root)
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(dir, lockName)
	owner, err := acquireLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseLock(owner, lockPath)

	var starts atomic.Int32
	_, err = EnsureStarted(context.Background(), EnsureStartedOptions{
		Store:        store,
		Timeout:      80 * time.Millisecond,
		PollInterval: 2 * time.Millisecond,
		Probe: func(context.Context, string) (Status, error) {
			return Status{}, errors.New("owner is not ready")
		},
		Starter: func(DaemonStartRequest) error {
			starts.Add(1)
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "readiness timed out") {
		t.Fatalf("EnsureStarted error = %v", err)
	}
	if got := starts.Load(); got != 0 {
		t.Fatalf("competing starter calls = %d, want 0", got)
	}
}

func TestEnsureStartedTimesOutWithoutEmbeddedFallbackAndRedactsDiagnostics(t *testing.T) {
	store := testStore(t)
	token, err := manager.NewUIToken()
	if err != nil {
		t.Fatal(err)
	}
	var diagnostics bytes.Buffer
	var starts atomic.Int32
	_, err = EnsureStarted(context.Background(), EnsureStartedOptions{
		Store:        store,
		Timeout:      60 * time.Millisecond,
		PollInterval: 2 * time.Millisecond,
		Diagnostics:  &diagnostics,
		Probe: func(context.Context, string) (Status, error) {
			return Status{}, fmt.Errorf("authentication refused for %s", token)
		},
		Starter: func(req DaemonStartRequest) error {
			starts.Add(1)
			_, _ = fmt.Fprintf(req.Output, "operator token=%s\n", token)
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "readiness timed out") {
		t.Fatalf("EnsureStarted error = %v", err)
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("starter calls = %d, want 1", got)
	}
	if strings.Contains(err.Error(), token) || strings.Contains(diagnostics.String(), token) {
		t.Fatalf("raw operator token escaped diagnostics: err=%q diagnostics=%q", err, diagnostics.String())
	}
	if !strings.Contains(diagnostics.String(), "REDACTED") {
		t.Fatalf("redacted diagnostics missing marker: %q", diagnostics.String())
	}
}

func TestEnsureStartedRejectsUnauthenticatedOrWrongSocketStatus(t *testing.T) {
	store := testStore(t)
	var starts atomic.Int32
	_, err := EnsureStarted(context.Background(), EnsureStartedOptions{
		Store:        store,
		Timeout:      50 * time.Millisecond,
		PollInterval: 2 * time.Millisecond,
		Probe: func(context.Context, string) (Status, error) {
			status := readyDaemonStatus(store.Root)
			status.Transport.Socket = filepath.Join(store.Root, "attacker.sock")
			return status, nil
		},
		Starter: func(DaemonStartRequest) error {
			starts.Add(1)
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "readiness timed out") {
		t.Fatalf("wrong-socket status error = %v", err)
	}
	if starts.Load() != 1 {
		t.Fatalf("starter calls = %d, want 1", starts.Load())
	}
}

func TestDaemonStatusServingAcceptsPhysicalStoreAlias(t *testing.T) {
	root := t.TempDir()
	realStore := filepath.Join(root, "real-store")
	if err := os.MkdirAll(filepath.Join(realStore, runtimeDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	aliasStore := filepath.Join(root, "store-alias")
	if err := os.Symlink(realStore, aliasStore); err != nil {
		t.Fatal(err)
	}

	status := readyDaemonStatus(realStore)
	if !daemonStatusServing(aliasStore, status) {
		t.Fatalf("authenticated status through physical store alias was rejected: %+v", status.Transport)
	}
	status.Transport.Socket = filepath.Join(realStore, runtimeDirName, "other.sock")
	if daemonStatusServing(aliasStore, status) {
		t.Fatal("different daemon socket basename was accepted")
	}
}

func TestStartDetachedDaemonReturnsBeforeChildCompletes(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	output, err := os.CreateTemp(t.TempDir(), "daemon-output-*")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	startedAt := time.Now()
	if err := startDetachedDaemon(DaemonStartRequest{
		Executable: "/bin/sh",
		Args:       []string{"-c", "sleep 0.15; printf ready > " + marker},
		Env:        os.Environ(),
		Output:     output,
	}); err != nil {
		t.Fatalf("startDetachedDaemon: %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed > 100*time.Millisecond {
		t.Fatalf("detached starter blocked for %s", elapsed)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if data, err := os.ReadFile(marker); err == nil && string(data) == "ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("detached child did not complete")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func readyDaemonStatus(storeRoot string) Status {
	buildID, err := currentProcessBuildID()
	if err != nil {
		panic(err)
	}
	return Status{
		Version: statusVersion,
		BuildID: buildID,
		State:   "serving",
		Transport: StatusTransport{
			Socket: socketPathFor(storeRoot), SessionSocket: SessionSocketPath(storeRoot),
			SessionProtocol: SessionProtocolVersion,
		},
	}
}

func environmentValue(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}
