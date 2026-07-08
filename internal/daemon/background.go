package daemon

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/vibe-agi/hideout/internal/manager"
)

// backgroundRun builds the closure that runs an allowed background op against the
// daemon's Core, using the existing typed plan/apply semantics. It rejects any op
// class that is not an existing typed environment operation (FR-010).
func (d *Daemon) backgroundRun(op string, ids []string) (func(context.Context) error, error) {
	core := d.api.Core
	switch op {
	case "environment-stop":
		return func(ctx context.Context) error {
			plan, err := core.PlanEnvironmentStop(manager.EnvironmentActionOptions{IDs: ids})
			if err != nil {
				return err
			}
			_, err = core.ApplyEnvironmentStop(ctx, plan, manager.EnvironmentApplyOptions{})
			return err
		}, nil
	case "environment-clean":
		return func(ctx context.Context) error {
			plan, err := core.PlanEnvironmentClean(manager.EnvironmentActionOptions{IDs: ids})
			if err != nil {
				return err
			}
			_, err = core.ApplyEnvironmentClean(ctx, plan, manager.EnvironmentApplyOptions{})
			return err
		}, nil
	default:
		return nil, fmt.Errorf("daemon: %q is not an allowed background operation (v1: environment stop/clean only)", op)
	}
}

// allowedBackgroundOps is the v1 background-work allowlist: only the existing
// typed environment stop/clean apply operations. Adding any new op class (for
// example a typed session-cleanup) is out of v1 scope, and `run/status` is a read,
// not background work.
var allowedBackgroundOps = map[string]bool{
	"environment-stop":  true,
	"environment-clean": true,
}

type bgOp struct {
	ID     string
	Op     string
	Status string
}

// bgRegistry tracks background operations the daemon runs, with queryable status.
type bgRegistry struct {
	mu      sync.Mutex
	ops     map[string]*bgOp
	seq     int
	wg      sync.WaitGroup
	stop    bool
	ctx     context.Context
	cancel  context.CancelFunc
	publish func(id, op, status string)
}

func newBGRegistry(publish func(id, op, status string)) *bgRegistry {
	ctx, cancel := context.WithCancel(context.Background())
	return &bgRegistry{ops: map[string]*bgOp{}, ctx: ctx, cancel: cancel, publish: publish}
}

func (r *bgRegistry) submit(op string, run func(context.Context) error) (string, error) {
	if !allowedBackgroundOps[op] {
		return "", fmt.Errorf("daemon: %q is not an allowed background operation (v1: environment stop/clean only)", op)
	}
	r.mu.Lock()
	if r.stop {
		r.mu.Unlock()
		return "", fmt.Errorf("daemon: not accepting background work during shutdown")
	}
	r.seq++
	id := fmt.Sprintf("bg-%d", r.seq)
	rec := &bgOp{ID: id, Op: op, Status: "queued"}
	r.ops[id] = rec
	r.wg.Add(1)
	r.mu.Unlock()
	r.publishStatus(id, op, "queued")

	go func() {
		defer r.wg.Done()
		r.setStatus(id, "running")
		err := run(r.ctx)
		if err != nil {
			r.setStatus(id, "failed")
		} else {
			r.setStatus(id, "completed")
		}
	}()
	return id, nil
}

func (r *bgRegistry) setStatus(id, status string) {
	r.mu.Lock()
	var op string
	if rec, ok := r.ops[id]; ok {
		rec.Status = status
		op = rec.Op
	}
	r.mu.Unlock()
	if op != "" {
		r.publishStatus(id, op, status)
	}
}

func (r *bgRegistry) publishStatus(id, op, status string) {
	if r.publish != nil {
		r.publish(id, op, status)
	}
}

func (r *bgRegistry) status(id string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.ops[id]
	if !ok {
		return "", false
	}
	return rec.Status, true
}

func (r *bgRegistry) inventory() []BackgroundStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]BackgroundStatus, 0, len(r.ops))
	for _, rec := range r.ops {
		out = append(out, BackgroundStatus{ID: rec.ID, Op: rec.Op, Status: rec.Status})
	}
	return out
}

// drain marks the registry closed to new work and waits up to timeout for
// in-flight operations to finish. It fails closed: a background op that does not
// finish within the bound is cancelled and marked failed, and drain returns —
// stop never hangs on a stuck operation (finish-or-fail-closed).
func (r *bgRegistry) drain(timeout time.Duration) {
	r.mu.Lock()
	r.stop = true
	r.mu.Unlock()

	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()

	select {
	case <-done:
		return
	case <-time.After(timeout):
	}
	// Timed out: signal cancellation and fail closed for anything not terminal.
	r.cancel()
	r.mu.Lock()
	for _, rec := range r.ops {
		if rec.Status == "queued" || rec.Status == "running" {
			rec.Status = "failed"
		}
	}
	r.mu.Unlock()
	// Give cancelled workers a brief window, then proceed regardless so stop
	// returns even if a worker ignores cancellation.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}
