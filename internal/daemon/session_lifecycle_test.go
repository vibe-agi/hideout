package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/sessionwire"
)

func TestDaemonStopCancelsAndDrainsActiveSession(t *testing.T) {
	setDaemonFakeLinuxShim(t)
	root, err := os.MkdirTemp("/tmp", "hideout-daemon-stop-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	store := profile.Store{Root: filepath.Join(root, "store")}
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := &daemonStreamBackend{block: true}
	d, err := Start(Options{
		Store: store,
		RunServiceBackend: func(manager.RunServiceRequest, manager.RunPlan) (interfaceBackend, error) {
			return fake, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Stop(context.Background()) })

	resultCh := make(chan error, 1)
	go func() {
		result, runErr := RunSessionClient(context.Background(), SessionClientOptions{
			Store: store,
			Request: manager.RunServiceRequest{
				Version: manager.RunServiceRequestVersion, Backend: "native", Workspace: workspace,
				Command: []string{"tool"}, AllowWeakIsolation: true,
				Terminal: manager.TerminalDescriptor{Mode: "none"},
			},
		})
		if runErr == nil || result.Completion.Kind != sessionwire.CompletionCancelled {
			resultCh <- fmt.Errorf("active session result=%+v error=%v", result, runErr)
			return
		}
		resultCh <- nil
	}()

	deadline := time.Now().Add(2 * time.Second)
	for len(d.Status().Sessions) != 1 && time.Now().Before(deadline) {
		select {
		case early := <-resultCh:
			t.Fatalf("session ended before daemon stop: %v", early)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := d.Status().Sessions; len(got) != 1 {
		t.Fatalf("active daemon sessions=%+v", got)
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := d.Stop(stopCtx); err != nil {
		t.Fatalf("ordered daemon stop: %v", err)
	}
	select {
	case err := <-resultCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("session client remained blocked after daemon stop")
	}
	if got := d.sessions.snapshots(); len(got) != 0 {
		t.Fatalf("workers survived daemon stop: %+v", got)
	}
	if _, err := os.Lstat(SessionSocketPath(store.Root)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("session socket survived daemon stop: %v", err)
	}
}

// interfaceBackend aliases the production interface in the test signature so
// this fixture cannot accidentally depend on a concrete backend implementation.
type interfaceBackend = backend.Backend
