package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/manager"
)

// T037/FR-010: background work is reachable through the product HTTP endpoint, and
// non-env op classes are rejected there too.
func TestBackgroundEndpointSubmitsAndRejects(t *testing.T) {
	d := startTestDaemon(t)
	// Accepted: environment-clean.
	code, body := daemonPost(t, d, backgroundPath, `{"op":"environment-clean"}`, d.Token())
	if code != http.StatusOK {
		t.Fatalf("submit env clean: want 200, got %d: %s", code, body)
	}
	var resp struct{ ID, Op, Status string }
	if err := json.Unmarshal(body, &resp); err != nil || resp.ID == "" || resp.Op != "environment-clean" {
		t.Fatalf("unexpected submit response: %s", body)
	}
	// Rejected: a non-env op class.
	if code, _ := daemonPost(t, d, backgroundPath, `{"op":"session-cleanup"}`, d.Token()); code != http.StatusBadRequest {
		t.Fatalf("session-cleanup should be rejected at the endpoint, got %d", code)
	}
	// Requires auth.
	if code, _ := daemonPost(t, d, backgroundPath, `{"op":"environment-clean"}`, ""); code != http.StatusUnauthorized {
		t.Fatalf("background endpoint without token: want 401, got %d", code)
	}
}

// T040/FR-012: a stuck background op does not hang stop — it is cancelled and
// failed within the bound (finish-or-fail-closed).
func TestStopFailsClosedOnStuckBackgroundOp(t *testing.T) {
	d := startTestDaemon(t)
	started := make(chan struct{})
	id, err := d.SubmitBackground("environment-stop", func(ctx context.Context) error {
		close(started)
		<-ctx.Done() // respects cancellation from a bounded drain
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	// Drain with a short bound; the op is cancelled and failed, stop returns.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	doneCh := make(chan error, 1)
	go func() { doneCh <- d.Stop(ctx) }()
	select {
	case <-doneCh:
	case <-time.After(4 * time.Second):
		t.Fatal("Stop hung on a stuck background op (must fail closed)")
	}
	if status, _ := d.BackgroundStatusOf(id); status != "failed" && status != "completed" {
		t.Fatalf("stuck op should be failed closed, got %s", status)
	}
}

// T034: an env stop/clean apply submitted as background work transitions status
// through completion; daemon stop drains it.
func TestBackgroundEnvCleanTransitionsStatus(t *testing.T) {
	d := startTestDaemon(t)
	core := d.api.Core
	id, err := d.SubmitBackground("environment-clean", func(ctx context.Context) error {
		plan, err := core.PlanEnvironmentClean(manager.EnvironmentActionOptions{StoppedOnly: true})
		if err != nil {
			return err
		}
		_, err = core.ApplyEnvironmentClean(ctx, plan, manager.EnvironmentApplyOptions{})
		return err
	})
	if err != nil {
		t.Fatalf("SubmitBackground: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		status, ok := d.BackgroundStatusOf(id)
		if !ok {
			t.Fatalf("background op %s not found", id)
		}
		if status == "completed" {
			break
		}
		if status == "failed" {
			t.Fatalf("background env clean failed")
		}
		if time.Now().After(deadline) {
			t.Fatalf("background op did not complete, last status=%s", status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// It appears in the status inventory.
	found := false
	for _, b := range d.Status().Background {
		if b.ID == id && b.Op == "environment-clean" {
			found = true
		}
	}
	if !found {
		t.Fatalf("background op not in status inventory: %+v", d.Status().Background)
	}
}

// T036: only env stop/clean are allowed as background work; any other op class is
// rejected (no new op class).
func TestBackgroundRejectsNonEnvOps(t *testing.T) {
	d := startTestDaemon(t)
	for _, op := range []string{"session-cleanup", "run-status", "run-apply"} {
		if _, err := d.SubmitBackground(op, func(context.Context) error { return nil }); err == nil {
			t.Fatalf("op %q should be rejected as out of v1 background scope", op)
		}
	}
	// The allowed ops are accepted.
	if _, err := d.SubmitBackground("environment-stop", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("environment-stop should be allowed: %v", err)
	}
}

// T036 (shutdown): daemon stop drains background work — no headless continuation.
func TestBackgroundDrainedOnStop(t *testing.T) {
	d := startTestDaemon(t)
	started := make(chan struct{})
	release := make(chan struct{})
	id, err := d.SubmitBackground("environment-stop", func(context.Context) error {
		close(started)
		<-release
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	go func() { time.Sleep(50 * time.Millisecond); close(release) }()
	if err := d.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if status, _ := d.BackgroundStatusOf(id); status != "completed" && status != "failed" {
		t.Fatalf("background op left in non-terminal status after stop: %s", status)
	}
}
