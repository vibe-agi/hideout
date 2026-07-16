package manager

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestApplyRunAllowsThreeOverlappingOwnersWithOneActivation(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	workspace := t.TempDir()
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default",
		Backend:     "native",
		Workspace:   workspace,
		Command:     []string{"hold"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.EnsureRunInitialized(plan); err != nil {
		t.Fatal(err)
	}
	runEnvironment, err := core.SelectRunEnvironment(plan, RunEnvironmentOptions{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	backend := newConcurrentRunBackend()
	outcomes := make(chan concurrentRunOutcome, 3)
	start := func() {
		go func() {
			result, err := core.ApplyRun(context.Background(), plan, ApplyRunOptions{
				Backend:            backend,
				RequestedBackend:   "native",
				AllowWeakIsolation: true,
				Environment:        RunEnvironmentOptions{Create: true},
				Opener:             broker.NoopOpener{},
			})
			outcomes <- concurrentRunOutcome{result: result, err: err}
		}()
	}

	start()
	first := receiveSessionStart(t, backend.started)
	start()
	start()
	second := receiveSessionStart(t, backend.started)
	third := receiveSessionStart(t, backend.started)
	if first == second || first == third || second == third {
		t.Fatalf("session IDs are not unique: %q %q %q", first, second, third)
	}
	starts, warm := backend.activationCounts()
	if starts != 1 || warm != 2 {
		t.Fatalf("activation counts full=%d warm=%d", starts, warm)
	}
	active, err := core.ActiveSessionSummaries()
	if err != nil || len(active) != 3 {
		t.Fatalf("active=%+v err=%v", active, err)
	}
	envStore := environment.Store{Root: store.Root}
	for _, id := range []string{first, second, third} {
		if _, err := envStore.PrepareSessionRuntime(runEnvironment.Record.ID, id); err == nil {
			t.Fatalf("live runtime child %s was unexpectedly reusable", id)
		}
	}

	backend.release(first)
	firstOutcome := receiveOutcome(t, outcomes)
	if firstOutcome.err != nil || firstOutcome.result.SessionID != first {
		t.Fatalf("first outcome=%+v err=%v", firstOutcome.result, firstOutcome.err)
	}
	active, err = core.ActiveSessionSummaries()
	if err != nil || len(active) != 2 {
		t.Fatalf("active after one exit=%+v err=%v", active, err)
	}
	if _, err := envStore.PrepareSessionRuntime(runEnvironment.Record.ID, second); err == nil {
		t.Fatal("first cleanup removed or made sibling runtime reusable")
	}
	backend.release(second)
	backend.release(third)
	for range 2 {
		outcome := receiveOutcome(t, outcomes)
		if outcome.err != nil {
			t.Fatalf("sibling outcome=%+v err=%v", outcome.result, outcome.err)
		}
	}
	active, err = core.ActiveSessionSummaries()
	if err != nil || len(active) != 0 {
		t.Fatalf("owners remain=%+v err=%v", active, err)
	}
	record, err := envStore.Load(runEnvironment.Record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "ready" {
		t.Fatalf("final environment status=%q", record.Status)
	}
}

func TestStoppedEnvironmentSimultaneousRunsSerializeOneActivation(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default", Backend: "native", Workspace: t.TempDir(), Command: []string{"hold"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.EnsureRunInitialized(plan); err != nil {
		t.Fatal(err)
	}
	runEnvironment, err := core.SelectRunEnvironment(plan, RunEnvironmentOptions{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	environmentStore := environment.Store{Root: store.Root}
	record, err := environmentStore.Load(runEnvironment.Record.ID)
	if err != nil {
		t.Fatal(err)
	}
	record.Status = "stopped"
	if err := environmentStore.Save(record); err != nil {
		t.Fatal(err)
	}

	fake := newConcurrentRunBackend()
	fake.activateEntered = make(chan struct{}, 1)
	fake.activateRelease = make(chan struct{})
	outcomes := make(chan concurrentRunOutcome, 3)
	launch := make(chan struct{})
	for range 3 {
		go func() {
			<-launch
			result, err := core.ApplyRun(context.Background(), plan, ApplyRunOptions{
				Backend: fake, RequestedBackend: "native", AllowWeakIsolation: true,
				Environment: RunEnvironmentOptions{Create: true}, Opener: broker.NoopOpener{},
			})
			outcomes <- concurrentRunOutcome{result: result, err: err}
		}()
	}
	close(launch)
	select {
	case <-fake.activateEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("no first activation entered")
	}
	select {
	case id := <-fake.started:
		t.Fatalf("target %s started before activation completed", id)
	case <-time.After(100 * time.Millisecond):
	}
	close(fake.activateRelease)

	ids := make([]string, 0, 3)
	for range 3 {
		ids = append(ids, receiveSessionStart(t, fake.started))
	}
	full, warm := fake.activationCounts()
	if full != 1 || warm != 2 {
		t.Fatalf("simultaneous activation counts full=%d warm=%d", full, warm)
	}
	for _, id := range ids {
		fake.release(id)
	}
	for range 3 {
		outcome := receiveOutcome(t, outcomes)
		if outcome.err != nil {
			t.Fatalf("simultaneous run outcome=%+v", outcome)
		}
	}
}

func TestApplyRunRetainsFailedOwnerWhenCleanupIsUnproved(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	workspace := t.TempDir()
	plan, err := core.PlanRun(RunPlanOptions{ProfileName: "default", Backend: "native", Workspace: workspace, Command: []string{"hold"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.EnsureRunInitialized(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := core.SelectRunEnvironment(plan, RunEnvironmentOptions{Create: true}); err != nil {
		t.Fatal(err)
	}
	fake := newConcurrentRunBackend()
	fake.cleanupErr = errors.New("cleanup failed with cap_0123456789abcdef")
	outcomeCh := make(chan concurrentRunOutcome, 1)
	go func() {
		result, err := core.ApplyRun(context.Background(), plan, ApplyRunOptions{
			Backend: fake, RequestedBackend: "native", AllowWeakIsolation: true,
			Environment: RunEnvironmentOptions{Create: true}, Opener: broker.NoopOpener{},
		})
		outcomeCh <- concurrentRunOutcome{result: result, err: err}
	}()
	id := <-fake.started
	fake.release(id)
	outcome := <-outcomeCh
	if outcome.err == nil || !strings.Contains(outcome.err.Error(), "cleanup failed") {
		t.Fatalf("cleanup failure outcome=%+v", outcome)
	}
	active, err := core.ActiveSessionSummaries()
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != id || active[0].OwnerStatus != "stale" || active[0].State != "failed" {
		t.Fatalf("failed owner summary=%+v", active)
	}
	if strings.Contains(active[0].CleanupError, "cap_0123456789abcdef") {
		t.Fatalf("failed owner leaked control material: %+v", active[0])
	}
	environments, err := (environment.Store{Root: store.Root}).List()
	if err != nil || len(environments) != 1 || environments[0].Status != "error" {
		t.Fatalf("environment after cleanup failure=%+v err=%v", environments, err)
	}
	cleanPlan, err := core.PlanEnvironmentClean(EnvironmentActionOptions{IDs: []string{environments[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = core.ApplyEnvironmentClean(t.Context(), cleanPlan, EnvironmentApplyOptions{Operator: &fakeEnvironmentOperator{}})
	if err == nil || EnvironmentRecoveryCode(err) != "session.cleanup.failed" {
		t.Fatalf("clean accepted failed cleanup owner: %v", err)
	}
}

func TestCancelingOneConcurrentRunDoesNotInterruptSibling(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default", Backend: "native", Workspace: t.TempDir(), Command: []string{"hold"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.EnsureRunInitialized(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := core.SelectRunEnvironment(plan, RunEnvironmentOptions{Create: true}); err != nil {
		t.Fatal(err)
	}
	fake := newConcurrentRunBackend()
	type canceledOutcome struct {
		id  string
		err error
	}
	outcomes := make(chan canceledOutcome, 2)
	contexts := make([]context.CancelFunc, 0, 2)
	for range 2 {
		ctx, cancel := context.WithCancel(context.Background())
		contexts = append(contexts, cancel)
		go func() {
			result, err := core.ApplyRun(ctx, plan, ApplyRunOptions{
				Backend: fake, RequestedBackend: "native", AllowWeakIsolation: true,
				Environment: RunEnvironmentOptions{Create: true}, Opener: broker.NoopOpener{},
			})
			outcomes <- canceledOutcome{id: result.SessionID, err: err}
		}()
	}
	firstID := receiveSessionStart(t, fake.started)
	secondID := receiveSessionStart(t, fake.started)
	contexts[0]()
	var canceled canceledOutcome
	select {
	case canceled = <-outcomes:
	case <-time.After(5 * time.Second):
		t.Fatal("canceled run did not finish")
	}
	if !errors.Is(canceled.err, context.Canceled) {
		t.Fatalf("canceled outcome=%+v", canceled)
	}
	siblingID := secondID
	if canceled.id == secondID {
		siblingID = firstID
	}
	active, err := core.ActiveSessionSummaries()
	if err != nil || len(active) != 1 || active[0].ID != siblingID {
		t.Fatalf("cancellation affected sibling: active=%+v err=%v", active, err)
	}
	select {
	case outcome := <-outcomes:
		t.Fatalf("sibling exited with canceled session: %+v", outcome)
	case <-time.After(150 * time.Millisecond):
	}
	fake.release(siblingID)
	select {
	case sibling := <-outcomes:
		if sibling.err != nil || sibling.id != siblingID {
			t.Fatalf("sibling outcome=%+v", sibling)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sibling did not finish independently")
	}
	for _, cancel := range contexts {
		cancel()
	}
}

func receiveSessionStart(t *testing.T, started <-chan string) string {
	t.Helper()
	select {
	case id := <-started:
		return id
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for target start")
		return ""
	}
}

func receiveOutcome(t *testing.T, outcomes <-chan concurrentRunOutcome) concurrentRunOutcome {
	t.Helper()
	select {
	case outcome := <-outcomes:
		return outcome
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for run outcome")
		return concurrentRunOutcome{}
	}
}

type concurrentRunOutcome struct {
	result RunResult
	err    error
}

type concurrentRunBackend struct {
	mu              sync.Mutex
	fullStarts      int
	warmStarts      int
	activationOwner string
	activateEntered chan struct{}
	activateRelease chan struct{}
	cleanupErr      error
	releases        map[string]chan struct{}
	started         chan string
}

func newConcurrentRunBackend() *concurrentRunBackend {
	return &concurrentRunBackend{releases: map[string]chan struct{}{}, started: make(chan string, 3)}
}

func (b *concurrentRunBackend) Name() string                    { return "native" }
func (b *concurrentRunBackend) Available(context.Context) error { return nil }

func (b *concurrentRunBackend) Prepare(_ context.Context, spec backend.RunSpec) (*backend.Session, error) {
	b.mu.Lock()
	b.releases[spec.SessionID] = make(chan struct{})
	b.mu.Unlock()
	return &backend.Session{
		ID: spec.SessionID, EnvironmentID: spec.EnvironmentID, Backend: b.Name(),
		HostWork: spec.HostWork, GuestWork: spec.GuestWork, SessionDir: spec.SessionDir,
		RuntimeRoot: spec.RuntimeRoot, PreserveInstance: true,
	}, nil
}

func (b *concurrentRunBackend) Activate(_ context.Context, session *backend.Session, _ []string) error {
	b.mu.Lock()
	b.fullStarts++
	b.activationOwner = session.ID
	entered := b.activateEntered
	release := b.activateRelease
	b.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if release != nil {
		<-release
	}
	session.RuntimeReady = true
	return nil
}

func (b *concurrentRunBackend) WarmActivationOwner(*backend.Session) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.activationOwner, nil
}

func (b *concurrentRunBackend) WarmActivate(_ context.Context, session *backend.Session, _ []string) error {
	b.mu.Lock()
	b.warmStarts++
	b.mu.Unlock()
	session.RuntimeReady = true
	return nil
}

func (b *concurrentRunBackend) Run(ctx context.Context, session *backend.Session, _ []string, _ []string) error {
	b.mu.Lock()
	release := b.releases[session.ID]
	b.mu.Unlock()
	b.started <- session.ID
	select {
	case <-release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *concurrentRunBackend) Cleanup(context.Context, *backend.Session) error { return b.cleanupErr }

func (b *concurrentRunBackend) release(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if release := b.releases[id]; release != nil {
		close(release)
		delete(b.releases, id)
	}
}

func (b *concurrentRunBackend) activationCounts() (int, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.fullStarts, b.warmStarts
}
