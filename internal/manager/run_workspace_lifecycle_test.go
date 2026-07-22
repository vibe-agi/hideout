package manager

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

func TestApplyRunWorkspaceLifecycleIsDurableBeforeEffectAndReadyBeforePublication(t *testing.T) {
	setFakeLinuxShim(t)
	setFakeLinuxWorkspacePortal(t)
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	store := profile.Store{Root: root}
	if err := store.Save(profile.Default("workspace-lifecycle")); err != nil {
		t.Fatal(err)
	}
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "workspace-lifecycle", Backend: "lima", Workspace: t.TempDir(), Command: []string{"true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	journalStore := lifecycle.JournalStore{Root: root}
	coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store: journalStore, DaemonID: "daemon-workspace-lifecycle", IdleGrace: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	fake := &lifecycleApplyBackend{
		applyRunFakeBackend: &applyRunFakeBackend{name: "lima"}, journal: journalStore,
		bootID: "01234567-89ab-cdef-0123-456789abcdef",
	}
	var events []string
	var workspaceID string
	result, err := core.ApplyRun(context.Background(), plan, ApplyRunOptions{
		Backend: fake, RequestedBackend: "lima", Environment: RunEnvironmentOptions{Create: true}, Lifecycle: coordinator,
		PrepareWorkspaceAttachment: func(runSession *RunSession) error {
			workspaceID = runSession.WorkspaceAttachment.WorkspaceID
			journal, loadErr := journalStore.Load(runSession.Environment.Record.ID)
			if loadErr != nil {
				return loadErr
			}
			provider := requireLifecycleResource(t, journal, runSession.WorkspaceAttachment.ProviderRef)
			view := requireLifecycleResource(t, journal, runSession.WorkspaceAttachment.GuestViewRef)
			assertWorkspaceLifecycleTopology(t, provider, view, runSession.WorkspaceAttachment, journal)
			if state := requireLifecycleState(t, coordinator, lifecycle.KindWorkspaceHostProvider); state != lifecycle.StateStarting {
				t.Fatalf("provider state before effect=%s", state)
			}
			if state := requireLifecycleState(t, coordinator, lifecycle.KindWorkspaceGuestView); state != lifecycle.StateStarting {
				t.Fatalf("view state before effect=%s", state)
			}
			if runSession.WorkspaceAttachment.Incarnation.BootID != "" {
				t.Fatal("workspace provider preparation received boot authority before observation")
			}
			events = append(events, "prepare")
			return runSession.WorkspaceAttachment.Validate()
		},
		ActivateWorkspaceAttachment: func(runSession *RunSession) error {
			if runSession.WorkspaceAttachment.Incarnation.BootID != fake.bootID {
				t.Fatalf("workspace view boot binding=%q", runSession.WorkspaceAttachment.Incarnation.BootID)
			}
			if state := requireLifecycleState(t, coordinator, lifecycle.KindWorkspaceHostProvider); state != lifecycle.StateActive {
				t.Fatalf("provider state at view activation=%s", state)
			}
			if state := requireLifecycleState(t, coordinator, lifecycle.KindWorkspaceGuestView); state != lifecycle.StateStarting {
				t.Fatalf("view state at activation=%s", state)
			}
			events = append(events, "activate")
			return runSession.WorkspaceAttachment.Validate()
		},
		ReleaseWorkspaceAttachment: func(context.Context) error {
			if state := requireLifecycleState(t, coordinator, lifecycle.KindWorkspaceHostProvider); state != lifecycle.StateActive {
				t.Fatalf("provider state before physical release=%s", state)
			}
			if state := requireLifecycleState(t, coordinator, lifecycle.KindWorkspaceGuestView); state != lifecycle.StateActive {
				t.Fatalf("view state before physical release=%s", state)
			}
			events = append(events, "release")
			return nil
		},
		Streams: &backend.RunStreams{Ready: func(backend.SessionReadyProof) error {
			for _, kind := range []lifecycle.ResourceKind{
				lifecycle.KindWorkspaceHostProvider, lifecycle.KindWorkspaceGuestView,
				lifecycle.KindGuestSupervisor, lifecycle.KindGuestTarget,
			} {
				if state := requireLifecycleState(t, coordinator, kind); state != lifecycle.StateActive {
					t.Fatalf("%s state at downstream ready=%s", kind, state)
				}
			}
			events = append(events, "ready")
			return nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(events, []string{"prepare", "activate", "ready", "release"}) {
		t.Fatalf("workspace lifecycle order=%v", events)
	}
	auditBody, err := os.ReadFile(result.AuditPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, correlation := range []string{
		`"session":"` + result.SessionID + `"`,
		`"environmentId":"` + result.EnvironmentID + `"`,
		`"workspaceId":"` + workspaceID + `"`,
	} {
		if !strings.Contains(string(auditBody), correlation) {
			t.Fatalf("workspace mapping audit lost correlation %q: %s", correlation, auditBody)
		}
	}
	journal := waitLifecycleJournal(t, journalStore, result.EnvironmentID, func(journal lifecycle.Journal) bool {
		for _, resource := range journal.Resources {
			if resource.Ref.Kind == lifecycle.KindWorkspaceHostProvider || resource.Ref.Kind == lifecycle.KindWorkspaceGuestView {
				return false
			}
		}
		return true
	})
	for _, resource := range journal.Resources {
		if resource.Ref.Kind == lifecycle.KindWorkspaceHostProvider || resource.Ref.Kind == lifecycle.KindWorkspaceGuestView {
			t.Fatalf("proved cleanup retained workspace lifecycle resource: %+v", resource)
		}
	}
}

func TestPlanRunWorkspaceAttachmentAcceptsPreparedIncarnationBeforePromotion(t *testing.T) {
	workspace := t.TempDir()
	runSession := workspaceRunSessionFixture(workspace, "ses_workspace_prepared")
	incarnation := lifecycle.EnvironmentRef{
		EnvironmentID:   runSession.Environment.Record.ID,
		StartGeneration: 7,
		InstanceName:    runSession.Environment.Record.InstanceName,
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	plan, err := (Core{Store: profile.Store{Root: root}}).PlanRunWorkspaceAttachmentForIncarnation(
		runSession, incarnation, WorkspaceAttachPlanOptions{Now: time.Unix(100, 0)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Attachment.Incarnation != incarnation {
		t.Fatalf("workspace attachment incarnation=%+v want=%+v", plan.Attachment.Incarnation, incarnation)
	}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceLifecycleSameRootReleasePreservesSiblingProvider(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	core := Core{Store: profile.Store{Root: root}}
	workspace := t.TempDir()
	coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store: lifecycle.JournalStore{Root: root}, DaemonID: "daemon-workspace-siblings", IdleGrace: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	observation := backend.LifecycleObservation{
		State: backend.LifecycleRunning, InstanceName: "hideout-default-env-fixture",
		BootID: "01234567-89ab-cdef-0123-456789abcdef", ObservedAt: time.Now().UTC(),
	}
	attach := func(sessionID string) (lifecycle.Registration, workspaceattach.Attachment, runWorkspaceLifecycleRefs) {
		registration, beginErr := coordinator.BeginAttach(context.Background(), lifecycle.AttachRequest{
			EnvironmentID: "env_sharedfixture", InstanceName: observation.InstanceName,
			SessionID: sessionID, Observation: observation,
		})
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		runSession := workspaceRunSessionFixture(workspace, sessionID)
		plan, planErr := core.PlanRunWorkspaceAttachment(runSession, registration, WorkspaceAttachPlanOptions{})
		if planErr != nil {
			t.Fatal(planErr)
		}
		refs, registerErr := registerRunWorkspaceLifecycle(context.Background(), registration, plan.Attachment)
		if registerErr != nil {
			t.Fatal(registerErr)
		}
		if startErr := startRunWorkspaceLifecycle(context.Background(), registration, refs); startErr != nil {
			t.Fatal(startErr)
		}
		if activeErr := registration.Transition(context.Background(), refs.Provider, lifecycle.StateActive); activeErr != nil {
			t.Fatal(activeErr)
		}
		if activeErr := activateLifecycleResource(context.Background(), registration, refs.View); activeErr != nil {
			t.Fatal(activeErr)
		}
		return registration, plan.Attachment, refs
	}
	firstRegistration, firstAttachment, firstRefs := attach("ses_workspace_first")
	secondRegistration, secondAttachment, secondRefs := attach("ses_workspace_second")
	siblingRefs := registerSiblingRunResources(t, secondRegistration, "env_sharedfixture", "ses_workspace_second")
	if firstAttachment.ProviderRef != secondAttachment.ProviderRef {
		t.Fatalf("same root did not share provider: first=%+v second=%+v", firstAttachment.ProviderRef, secondAttachment.ProviderRef)
	}
	if firstAttachment.GuestViewRef == secondAttachment.GuestViewRef {
		t.Fatal("same-root sessions shared a guest view")
	}
	journalStore := lifecycle.JournalStore{Root: root}
	waitLifecycleJournal(t, journalStore, "env_sharedfixture", func(journal lifecycle.Journal) bool {
		return lifecycleResourceState(journal, secondRefs.Provider) == lifecycle.StateActive &&
			lifecycleResourceState(journal, secondRefs.View) == lifecycle.StateActive
	})
	if err := releaseRunWorkspaceLifecycle(context.Background(), firstRegistration, firstRefs, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	journal := waitLifecycleJournal(t, journalStore, "env_sharedfixture", func(journal lifecycle.Journal) bool {
		if lifecycleResourceExists(journal, firstRefs.View) ||
			lifecycleResourceState(journal, secondRefs.Provider) != lifecycle.StateActive ||
			lifecycleResourceState(journal, secondRefs.View) != lifecycle.StateActive {
			return false
		}
		for _, ref := range siblingRefs {
			if lifecycleResourceState(journal, ref) != lifecycle.StateActive {
				return false
			}
		}
		return true
	})
	if state := requireLifecycleResource(t, journal, secondRefs.Provider).State; state != lifecycle.StateActive {
		t.Fatalf("shared provider state after first release=%s", state)
	}
	if state := requireLifecycleResource(t, journal, secondRefs.View).State; state != lifecycle.StateActive {
		t.Fatalf("sibling view state after first release=%s", state)
	}
	if lifecycleResourceExists(journal, firstRefs.View) {
		t.Fatal("released first view remains in lifecycle journal")
	}
	for _, ref := range siblingRefs {
		if state := requireLifecycleResource(t, journal, ref).State; state != lifecycle.StateActive {
			t.Fatalf("first workspace release changed sibling %s state=%s", ref.Kind, state)
		}
	}
	if err := firstRegistration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := releaseRunWorkspaceLifecycle(context.Background(), secondRegistration, secondRefs, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := secondRegistration.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	journal = waitLifecycleJournal(t, journalStore, "env_sharedfixture", func(journal lifecycle.Journal) bool {
		return !lifecycleResourceExists(journal, secondRefs.Provider) && !lifecycleResourceExists(journal, secondRefs.View)
	})
	if lifecycleResourceExists(journal, secondRefs.Provider) || lifecycleResourceExists(journal, secondRefs.View) {
		t.Fatalf("final release retained workspace resources: %+v", journal.Resources)
	}
}

func TestWorkspaceLifecycleCleanupFailureRemainsUnproved(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	core := Core{Store: profile.Store{Root: root}}
	coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store: lifecycle.JournalStore{Root: root}, DaemonID: "daemon-workspace-unproved", IdleGrace: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	registration, err := coordinator.BeginAttach(context.Background(), lifecycle.AttachRequest{
		EnvironmentID: "env_sharedfixture", InstanceName: "hideout-default-env-fixture", SessionID: "ses_workspace_unproved",
		Observation: backend.LifecycleObservation{
			State: backend.LifecycleRunning, InstanceName: "hideout-default-env-fixture",
			BootID: "01234567-89ab-cdef-0123-456789abcdef", ObservedAt: time.Now().UTC(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := core.PlanRunWorkspaceAttachment(workspaceRunSessionFixture(t.TempDir(), "ses_workspace_unproved"), registration, WorkspaceAttachPlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	refs, err := registerRunWorkspaceLifecycle(context.Background(), registration, plan.Attachment)
	if err != nil {
		t.Fatal(err)
	}
	if err := startRunWorkspaceLifecycle(context.Background(), registration, refs); err != nil {
		t.Fatal(err)
	}
	cleanupFailure := errors.New("workspace view absence is unproved")
	if err := releaseRunWorkspaceLifecycle(context.Background(), registration, refs, func(context.Context) error { return cleanupFailure }); !errors.Is(err, cleanupFailure) {
		t.Fatalf("cleanup error=%v", err)
	}
	journal, err := (lifecycle.JournalStore{Root: root}).Load("env_sharedfixture")
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []lifecycle.ResourceRef{refs.View, refs.Provider} {
		if state := requireLifecycleResource(t, journal, ref).State; state != lifecycle.StateOrphaned {
			t.Fatalf("failed cleanup resource %s state=%s", ref.Kind, state)
		}
	}
}

func assertWorkspaceLifecycleTopology(t *testing.T, provider, view lifecycle.Resource, attachment workspaceattach.Attachment, journal lifecycle.Journal) {
	t.Helper()
	if provider.State != lifecycle.StatePlanned || provider.Owner.Kind != "manager" || provider.Owner.ID != attachment.ProviderRef.ID ||
		provider.ClosePolicy != lifecycle.ClosePreStopDrain || len(provider.Dependencies) != 1 || provider.Dependencies[0].Ref.Kind != lifecycle.KindBackendIncarnation {
		t.Fatalf("provider topology=%+v", provider)
	}
	if view.State != lifecycle.StatePlanned || view.Owner.Kind != "session" || view.Owner.ID != attachment.SessionID ||
		view.ClosePolicy != lifecycle.ClosePreStopDrain || len(view.Dependencies) != 3 {
		t.Fatalf("view topology=%+v", view)
	}
	wantDependencies := map[lifecycle.ResourceKind]bool{
		lifecycle.KindBackendIncarnation:    false,
		lifecycle.KindRunSession:            false,
		lifecycle.KindWorkspaceHostProvider: false,
	}
	for _, dependency := range view.Dependencies {
		if _, ok := wantDependencies[dependency.Ref.Kind]; !ok || dependency.StopMode != lifecycle.StopModeDrain {
			t.Fatalf("unexpected view dependency=%+v", dependency)
		}
		wantDependencies[dependency.Ref.Kind] = true
	}
	for kind, found := range wantDependencies {
		if !found {
			t.Fatalf("view missing dependency=%s", kind)
		}
	}
	for _, resource := range journal.Resources {
		if resource.Ref.Kind == lifecycle.KindWorkspaceEnvironmentService {
			t.Fatal("Portal topology registered a nonexistent environment workspace service")
		}
	}
}

func requireLifecycleState(t *testing.T, coordinator *lifecycle.Coordinator, kind lifecycle.ResourceKind) lifecycle.ResourceState {
	t.Helper()
	for _, status := range coordinator.Snapshot() {
		for _, resources := range [][]lifecycle.ResourceSummary{status.Pins, status.Drains, status.Orphans} {
			for _, resource := range resources {
				if resource.Kind == kind {
					return resource.State
				}
			}
		}
	}
	t.Fatalf("lifecycle resource %s is absent", kind)
	return ""
}

func requireLifecycleResource(t *testing.T, journal lifecycle.Journal, ref lifecycle.ResourceRef) lifecycle.Resource {
	t.Helper()
	for _, resource := range journal.Resources {
		if resource.Ref == ref {
			return resource
		}
	}
	t.Fatalf("lifecycle resource %+v is absent", ref)
	return lifecycle.Resource{}
}

func lifecycleResourceExists(journal lifecycle.Journal, ref lifecycle.ResourceRef) bool {
	for _, resource := range journal.Resources {
		if resource.Ref == ref {
			return true
		}
	}
	return false
}

func lifecycleResourceState(journal lifecycle.Journal, ref lifecycle.ResourceRef) lifecycle.ResourceState {
	for _, resource := range journal.Resources {
		if resource.Ref == ref {
			return resource.State
		}
	}
	return ""
}

func waitLifecycleJournal(t *testing.T, store lifecycle.JournalStore, environmentID string, ready func(lifecycle.Journal) bool) lifecycle.Journal {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last lifecycle.Journal
	for {
		journal, err := store.Load(environmentID)
		if err != nil {
			t.Fatal(err)
		}
		last = journal
		if ready(journal) {
			return journal
		}
		if time.Now().After(deadline) {
			t.Fatalf("lifecycle journal did not converge: %+v", last.Resources)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func registerSiblingRunResources(t *testing.T, registration lifecycle.Registration, environmentID, sessionID string) []lifecycle.ResourceRef {
	t.Helper()
	register := func(spec lifecycle.RegistrationSpec) lifecycle.ResourceRef {
		ref, err := registration.Register(context.Background(), spec)
		if err != nil {
			t.Fatal(err)
		}
		if err := activateLifecycleResource(context.Background(), registration, ref); err != nil {
			t.Fatal(err)
		}
		return ref
	}
	root, session := registration.Root(), registration.Session()
	supervisor := register(lifecycle.RegistrationSpec{
		Kind: lifecycle.KindGuestSupervisor, ID: sessionID, OwnerKind: "session", OwnerID: sessionID,
		Dependencies: []lifecycle.DependencySpec{
			{Ref: root, StopMode: lifecycle.StopModeDrain}, {Ref: session, StopMode: lifecycle.StopModeDrain},
		},
		Persistence: lifecycle.PersistenceEphemeral, ClosePolicy: lifecycle.CloseCoTerminateWithRoot, PossibleVMDependency: true,
	})
	return []lifecycle.ResourceRef{
		supervisor,
		register(lifecycle.RegistrationSpec{
			Kind: lifecycle.KindGuestTarget, ID: sessionID, OwnerKind: "session", OwnerID: sessionID,
			Dependencies: []lifecycle.DependencySpec{{Ref: supervisor, StopMode: lifecycle.StopModeDrain}},
			Persistence:  lifecycle.PersistenceEphemeral, ClosePolicy: lifecycle.CloseCoTerminateWithRoot, PossibleVMDependency: true,
		}),
		register(lifecycle.RegistrationSpec{
			Kind: lifecycle.KindHostFSReadProvider, ID: sessionID, OwnerKind: "session", OwnerID: sessionID,
			Dependencies: []lifecycle.DependencySpec{{Ref: session, StopMode: lifecycle.StopModeDrain}},
			Persistence:  lifecycle.PersistenceEphemeral, ClosePolicy: lifecycle.ClosePreStopDrain, PossibleVMDependency: true,
		}),
		register(lifecycle.RegistrationSpec{
			Kind: lifecycle.KindNetworkService, ID: environmentID, OwnerKind: "manager", OwnerID: environmentID,
			Dependencies: []lifecycle.DependencySpec{{Ref: root, StopMode: lifecycle.StopModeDrain}},
			Persistence:  lifecycle.PersistenceEphemeral, ClosePolicy: lifecycle.ClosePreStopDrain, PossibleVMDependency: true,
		}),
		register(lifecycle.RegistrationSpec{
			Kind: lifecycle.KindRunBridge, ID: "bridge-" + sessionID, OwnerKind: "session", OwnerID: sessionID,
			Dependencies: []lifecycle.DependencySpec{
				{Ref: root, StopMode: lifecycle.StopModePin}, {Ref: session, StopMode: lifecycle.StopModeDrain},
			},
			Persistence: lifecycle.PersistenceEphemeral, ClosePolicy: lifecycle.ClosePreStopDrain, PossibleVMDependency: true,
		}),
	}
}
