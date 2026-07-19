package daemon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
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

	mu                   sync.Mutex
	sessionID            string
	environmentID        string
	profile              string
	backend              string
	terminalMode         string
	workspaceID          string
	sessionSnapshotID    string
	commandClass         string
	state                string
	cleanupError         string
	finished             bool
	attachment           workspaceattach.Attachment
	workspaceProvider    workspaceattach.Provider
	workspaceView        workspaceattach.GuestView
	workspaceCleanupOnce sync.Once
	workspaceCleanupErr  error
	notifyWorkspace      func()
}

type sessionStart struct {
	SessionID         string
	EnvironmentID     string
	Profile           string
	Backend           string
	TerminalMode      string
	SessionSnapshotID string
	CommandClass      string
}

func (w *sessionWorker) prepareWorkspaceAttachment(ctx context.Context, factory workspaceattach.ProviderFactory, runSession *manager.RunSession) error {
	if w == nil {
		return errors.New("daemon session worker is required")
	}
	if factory == nil || runSession == nil {
		return errors.New("daemon workspace provider binding is unavailable")
	}
	attachment := runSession.WorkspaceAttachment
	if err := attachment.Validate(); err != nil {
		return fmt.Errorf("invalid daemon-owned workspace attachment: %w", err)
	}
	w.mu.Lock()
	if w.finished {
		w.mu.Unlock()
		return errors.New("daemon session worker already finished")
	}
	if w.attachment.ID != "" {
		w.mu.Unlock()
		return errors.New("daemon session worker already owns a workspace attachment")
	}
	w.attachment = attachment
	w.attachment.State = workspaceattach.AttachmentProviderStarting
	w.workspaceID = attachment.WorkspaceID
	w.profile = runSession.Plan.ProfileName
	w.mu.Unlock()
	w.notifyWorkspaceChange()

	providerSpec, err := workspaceattach.ProviderSpecFromAttachment(attachment, workspaceattach.SelectedLimits())
	if err != nil {
		return err
	}
	provider, err := factory.Start(ctx, providerSpec)
	if err != nil {
		return fmt.Errorf("start workspace provider: %w", err)
	}
	w.mu.Lock()
	w.workspaceProvider = provider
	w.attachment.State = workspaceattach.AttachmentProviderReady
	w.mu.Unlock()
	w.notifyWorkspaceChange()
	credentialHostPath := filepath.Join(runSession.RuntimeSessionDir, "workspace", "credential.bin")
	runtime := workspaceattach.PortalRuntime{
		Endpoint: provider.Endpoint(), CredentialHostPath: credentialHostPath,
		CredentialGuestPath: workspaceattach.PortalCredentialGuestPath,
	}
	if err := runtime.Validate(attachment); err != nil {
		_, _ = provider.Release(context.Background())
		return err
	}
	runSession.WorkspacePortal = &runtime
	return nil
}

func (w *sessionWorker) activateWorkspaceAttachment(ctx context.Context, runSession *manager.RunSession) error {
	if w == nil || runSession == nil {
		return errors.New("daemon workspace view activation is unavailable")
	}
	attachment := runSession.WorkspaceAttachment
	if err := attachment.Validate(); err != nil {
		return fmt.Errorf("invalid boot-bound workspace attachment: %w", err)
	}
	if err := attachment.Incarnation.Validate(true); err != nil {
		return errors.New("workspace view activation requires an observed backend incarnation")
	}
	w.mu.Lock()
	planned := w.attachment
	planned.Incarnation = attachment.Incarnation
	planned.State = attachment.State
	provider := w.workspaceProvider
	if w.finished || provider == nil || planned != attachment || w.workspaceView != nil {
		w.mu.Unlock()
		return errors.New("workspace view activation does not match the prepared provider")
	}
	w.mu.Unlock()
	if err := provider.BindIncarnation(ctx, attachment.Incarnation); err != nil {
		return fmt.Errorf("bind workspace provider incarnation: %w", err)
	}
	view, err := provider.Attach(ctx, workspaceattach.ViewSpec{
		Attachment: attachment, CredentialAudience: workspaceattach.PortalAudience,
	})
	if err != nil {
		return fmt.Errorf("attach workspace guest view: %w", err)
	}
	portalView, ok := view.(workspaceattach.PortalGuestView)
	if !ok {
		_, _ = view.Release(context.Background())
		return errors.New("selected workspace provider returned an incompatible guest view")
	}
	if runSession.WorkspacePortal == nil {
		_, _ = view.Release(context.Background())
		return errors.New("prepared workspace Portal runtime is unavailable")
	}
	if err := portalView.WriteCredential(runSession.WorkspacePortal.CredentialHostPath); err != nil {
		_, _ = view.Release(context.Background())
		return fmt.Errorf("materialize workspace view credential: %w", err)
	}
	if err := runSession.WorkspacePortal.Validate(attachment); err != nil {
		_, _ = view.Release(context.Background())
		return err
	}
	w.mu.Lock()
	w.attachment = attachment
	w.workspaceView = view
	w.attachment.State = workspaceattach.AttachmentViewMounting
	w.mu.Unlock()
	w.notifyWorkspaceChange()
	return nil
}

func (w *sessionWorker) releaseWorkspaceAttachment(ctx context.Context) error {
	if w == nil {
		return nil
	}
	w.workspaceCleanupOnce.Do(func() {
		w.mu.Lock()
		view, provider := w.workspaceView, w.workspaceProvider
		if w.attachment.ID != "" {
			w.attachment.State = workspaceattach.AttachmentDraining
		}
		w.mu.Unlock()
		w.notifyWorkspaceChange()
		var errs []error
		if view != nil {
			if err := view.Flush(ctx); err != nil {
				errs = append(errs, fmt.Errorf("flush workspace view: %w", err))
			}
			if err := view.Revoke(ctx); err != nil {
				errs = append(errs, fmt.Errorf("revoke workspace view: %w", err))
			}
			observation, err := view.Release(ctx)
			if err != nil {
				errs = append(errs, fmt.Errorf("release workspace view: state=%s: %w", observation.State, err))
			} else if observation.State != workspaceattach.ObservationAbsent {
				errs = append(errs, fmt.Errorf("release workspace view: unexpected state=%s", observation.State))
			}
		}
		if provider != nil {
			observation, err := provider.Release(ctx)
			if err != nil {
				errs = append(errs, fmt.Errorf("release workspace provider: state=%s: %w", observation.State, err))
			} else if observation.State != workspaceattach.ObservationAbsent && observation.State != workspaceattach.ObservationReady {
				errs = append(errs, fmt.Errorf("release workspace provider: unexpected state=%s", observation.State))
			}
		}
		w.workspaceCleanupErr = errors.Join(errs...)
		w.mu.Lock()
		if w.attachment.ID != "" {
			now := time.Now().UTC()
			if w.workspaceCleanupErr == nil {
				w.attachment.State = workspaceattach.AttachmentReleased
				w.attachment.CleanupProof = &workspaceattach.CleanupProof{Status: workspaceattach.CleanupAbsent, ObservedAt: now}
			} else {
				w.attachment.State = workspaceattach.AttachmentUnproved
				w.attachment.CleanupProof = &workspaceattach.CleanupProof{
					Status: workspaceattach.CleanupUnproved, ObservedAt: now, ReasonCode: "workspace-cleanup-unproved",
				}
			}
		}
		w.mu.Unlock()
		w.notifyWorkspaceChange()
	})
	return w.workspaceCleanupErr
}

func (w *sessionWorker) notifyWorkspaceChange() {
	if w != nil && w.notifyWorkspace != nil {
		w.notifyWorkspace()
	}
}

type sessionSnapshot struct {
	ConnectionID      string
	SessionID         string
	EnvironmentID     string
	Profile           string
	Backend           string
	TerminalMode      string
	WorkspaceID       string
	SessionSnapshotID string
	CommandClass      string
	State             string
	StartedAt         string
	HasCleanupError   bool
}

type sessionRegistry struct {
	mu                    sync.Mutex
	workers               map[string]*sessionWorker
	capacity              int
	stopping              bool
	now                   func() time.Time
	publishWorkspaceViews func([]manager.WorkspaceViewSnapshot)
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
		startedAt: r.now().UTC(), state: "preparing", notifyWorkspace: r.notifyWorkspaceViews,
	}
	r.workers[connectionID] = w
	return w, nil
}

func (w *sessionWorker) markStarted(start sessionStart) error {
	if w == nil || start.SessionID == "" || start.EnvironmentID == "" || !environment.ValidConfigurationID(start.SessionSnapshotID) {
		return errors.New("daemon session start identity is incomplete")
	}
	w.mu.Lock()
	if w.finished {
		w.mu.Unlock()
		return errors.New("daemon session worker already finished")
	}
	if w.sessionID != "" {
		w.mu.Unlock()
		return errors.New("daemon session worker already started")
	}
	if w.attachment.ID != "" && (w.attachment.SessionID != start.SessionID || w.attachment.EnvironmentID != start.EnvironmentID) {
		w.mu.Unlock()
		return errors.New("backend ready identity does not match daemon-owned workspace attachment")
	}
	if w.attachment.ID != "" {
		w.attachment.State = workspaceattach.AttachmentReady
	}
	w.sessionID = start.SessionID
	w.environmentID = start.EnvironmentID
	w.profile = start.Profile
	w.backend = start.Backend
	w.terminalMode = start.TerminalMode
	w.sessionSnapshotID = start.SessionSnapshotID
	w.commandClass = start.CommandClass
	w.state = "running"
	w.mu.Unlock()
	w.notifyWorkspaceChange()
	return nil
}

func (r *sessionRegistry) setWorkspacePublisher(publish func([]manager.WorkspaceViewSnapshot)) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.publishWorkspaceViews = publish
	r.mu.Unlock()
}

func (r *sessionRegistry) notifyWorkspaceViews() {
	if r == nil {
		return
	}
	r.mu.Lock()
	publish := r.publishWorkspaceViews
	r.mu.Unlock()
	if publish != nil {
		publish(r.workspaceViewSnapshots())
	}
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
			WorkspaceID: worker.workspaceID, SessionSnapshotID: worker.sessionSnapshotID,
			CommandClass: worker.commandClass,
			State:        worker.state, StartedAt: worker.startedAt.Format(time.RFC3339Nano),
			HasCleanupError: worker.cleanupError != "",
		})
		worker.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ConnectionID < out[j].ConnectionID })
	return out
}

func (r *sessionRegistry) workspaceViewSnapshots() []manager.WorkspaceViewSnapshot {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	workers := make([]*sessionWorker, 0, len(r.workers))
	for _, worker := range r.workers {
		workers = append(workers, worker)
	}
	r.mu.Unlock()
	attachments := make([]workspaceattach.Attachment, 0, len(workers))
	for _, worker := range workers {
		worker.mu.Lock()
		attachment := worker.attachment
		worker.mu.Unlock()
		if attachment.ID == "" || attachment.Validate() != nil {
			continue
		}
		attachments = append(attachments, attachment)
	}
	out := make([]manager.WorkspaceViewSnapshot, 0, len(attachments))
	for _, attachment := range attachments {
		item := manager.WorkspaceViewSnapshot{
			Attachment:         attachment.Summary(),
			Profile:            workspaceProfileForAttachment(workers, attachment.SessionID),
			CanonicalHostRoot:  attachment.CanonicalHostRoot,
			RootHandleIdentity: attachment.RootHandleIdentity,
		}
		seen := make(map[string]bool)
		for _, other := range attachments {
			if other.SessionID == attachment.SessionID {
				continue
			}
			notice, err := workspaceattach.BuildRootRelationNotice(attachment, other)
			if err != nil {
				continue
			}
			key := string(notice.Relation) + "\x00" + notice.SelectedPosition + "\x00" + notice.OtherWorkspaceID
			if seen[key] {
				continue
			}
			seen[key] = true
			item.Relations = append(item.Relations, notice)
		}
		sort.Slice(item.Relations, func(i, j int) bool {
			left, right := item.Relations[i], item.Relations[j]
			return string(left.Relation)+left.SelectedPosition+left.OtherWorkspaceID <
				string(right.Relation)+right.SelectedPosition+right.OtherWorkspaceID
		})
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Attachment.SessionID < out[j].Attachment.SessionID
	})
	return out
}

func workspaceProfileForAttachment(workers []*sessionWorker, sessionID string) string {
	for _, worker := range workers {
		worker.mu.Lock()
		matches := worker.attachment.SessionID == sessionID
		profileName := worker.profile
		worker.mu.Unlock()
		if matches {
			return profileName
		}
	}
	return ""
}
