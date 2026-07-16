package daemon

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

const defaultSessionCapacity = 16

var (
	errSessionRegistryStopping = errors.New("daemon session registry is stopping")
	errSessionCapacity         = errors.New("daemon session capacity reached")
)

type sessionWorker struct {
	connectionID string
	cancel       context.CancelFunc
	done         chan struct{}
	startedAt    time.Time

	mu            sync.Mutex
	sessionID     string
	environmentID string
	profile       string
	backend       string
	terminalMode  string
	workspaceID   string
	commandClass  string
	state         string
	cleanupError  string
	finished      bool
}

type sessionStart struct {
	SessionID     string
	EnvironmentID string
	Profile       string
	Backend       string
	TerminalMode  string
	WorkspaceID   string
	CommandClass  string
}

type sessionSnapshot struct {
	ConnectionID    string
	SessionID       string
	EnvironmentID   string
	Profile         string
	Backend         string
	TerminalMode    string
	WorkspaceID     string
	CommandClass    string
	State           string
	StartedAt       string
	HasCleanupError bool
}

type sessionRegistry struct {
	mu       sync.Mutex
	workers  map[string]*sessionWorker
	capacity int
	stopping bool
	now      func() time.Time
}

func newSessionRegistry(capacity int, now func() time.Time) *sessionRegistry {
	if capacity <= 0 {
		capacity = defaultSessionCapacity
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &sessionRegistry{workers: map[string]*sessionWorker{}, capacity: capacity, now: now}
}

func (r *sessionRegistry) register(connectionID string, cancel context.CancelFunc) (*sessionWorker, error) {
	if r == nil || connectionID == "" || cancel == nil {
		return nil, errors.New("daemon session registration is incomplete")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping {
		return nil, errSessionRegistryStopping
	}
	if len(r.workers) >= r.capacity {
		return nil, errSessionCapacity
	}
	if _, exists := r.workers[connectionID]; exists {
		return nil, errors.New("daemon session connection is already registered")
	}
	w := &sessionWorker{
		connectionID: connectionID, cancel: cancel, done: make(chan struct{}),
		startedAt: r.now().UTC(), state: "preparing",
	}
	r.workers[connectionID] = w
	return w, nil
}

func (w *sessionWorker) markStarted(start sessionStart) error {
	if w == nil || start.SessionID == "" || start.EnvironmentID == "" {
		return errors.New("daemon session start identity is incomplete")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.finished {
		return errors.New("daemon session worker already finished")
	}
	if w.sessionID != "" {
		return errors.New("daemon session worker already started")
	}
	w.sessionID = start.SessionID
	w.environmentID = start.EnvironmentID
	w.profile = start.Profile
	w.backend = start.Backend
	w.terminalMode = start.TerminalMode
	w.workspaceID = start.WorkspaceID
	w.commandClass = start.CommandClass
	w.state = "running"
	return nil
}

func (r *sessionRegistry) finish(connectionID, cleanupError string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	w := r.workers[connectionID]
	delete(r.workers, connectionID)
	r.mu.Unlock()
	if w == nil {
		return
	}
	w.mu.Lock()
	if w.finished {
		w.mu.Unlock()
		return
	}
	w.finished = true
	w.cleanupError = cleanupError
	if cleanupError != "" {
		w.state = "failed"
	} else {
		w.state = "completed"
	}
	close(w.done)
	w.mu.Unlock()
}

func (r *sessionRegistry) cancelConnection(connectionID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	w := r.workers[connectionID]
	r.mu.Unlock()
	if w != nil {
		w.cancel()
	}
}

func (r *sessionRegistry) beginStop() []*sessionWorker {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	r.stopping = true
	workers := make([]*sessionWorker, 0, len(r.workers))
	for _, worker := range r.workers {
		workers = append(workers, worker)
	}
	r.mu.Unlock()
	for _, worker := range workers {
		worker.cancel()
	}
	return workers
}

func (r *sessionRegistry) drain(ctx context.Context) error {
	workers := r.beginStop()
	for _, worker := range workers {
		select {
		case <-worker.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (r *sessionRegistry) snapshots() []sessionSnapshot {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	workers := make([]*sessionWorker, 0, len(r.workers))
	for _, worker := range r.workers {
		workers = append(workers, worker)
	}
	r.mu.Unlock()
	out := make([]sessionSnapshot, 0, len(workers))
	for _, worker := range workers {
		worker.mu.Lock()
		out = append(out, sessionSnapshot{
			ConnectionID: worker.connectionID, SessionID: worker.sessionID,
			EnvironmentID: worker.environmentID, Profile: worker.profile,
			Backend: worker.backend, TerminalMode: worker.terminalMode,
			WorkspaceID: worker.workspaceID, CommandClass: worker.commandClass,
			State: worker.state, StartedAt: worker.startedAt.Format(time.RFC3339Nano),
			HasCleanupError: worker.cleanupError != "",
		})
		worker.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ConnectionID < out[j].ConnectionID })
	return out
}
