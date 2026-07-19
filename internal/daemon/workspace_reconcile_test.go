package daemon

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/lifecycle"
)

func TestWorkspaceReconcileProvesPriorDaemonProviderAndViewAbsentWithoutReadoption(t *testing.T) {
	store, record := daemonLifecycleEnvironment(t)
	journalStore := lifecycle.JournalStore{Root: store.Root}
	if err := journalStore.Remove(record.ID); err != nil {
		t.Fatal(err)
	}
	const sessionID = "ses-workspace-restart"
	observation := seedWorkspaceReconcileJournal(t, store.Root, record, sessionID)
	provider := &daemonLifecycleBackend{observation: observation}
	owners, reasons := reconcileRestartResidue(context.Background(), environment.Store{Root: store.Root}, record, observation, provider)
	if len(owners) != 0 || len(reasons) != 0 {
		t.Fatalf("proved-absent restart owners=%+v reasons=%v", owners, reasons)
	}
	if !slices.Contains(provider.proved, sessionID) {
		t.Fatalf("workspace view did not use exact session absence proof: %v", provider.proved)
	}

	coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store: journalStore, DaemonID: "daemon-workspace-new", IdleGrace: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Reconcile(context.Background(), lifecycle.ReconcileInput{
		EnvironmentID: record.ID, InstanceName: record.InstanceName, Observation: observation,
	}); err != nil {
		t.Fatal(err)
	}
	journal, err := journalStore.Load(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if journal.Reconciliation.State != "complete" || journal.Reconciliation.DaemonInstanceID != "daemon-workspace-new" {
		t.Fatalf("workspace reconciliation=%+v", journal.Reconciliation)
	}
	for _, resource := range journal.Resources {
		if resource.Ref.Kind == lifecycle.KindWorkspaceHostProvider || resource.Ref.Kind == lifecycle.KindWorkspaceGuestView {
			t.Fatalf("old workspace authority was re-adopted: %+v", resource)
		}
	}
	if _, available := coordinator.ActiveAttachObservation(context.Background(), record.ID, record.InstanceName); available {
		t.Fatal("reconciled discovery state became active attach authority without a new registration")
	}
}

func TestWorkspaceReconcileBlocksWhenGuestViewAbsenceIsUnproved(t *testing.T) {
	store, record := daemonLifecycleEnvironment(t)
	journalStore := lifecycle.JournalStore{Root: store.Root}
	if err := journalStore.Remove(record.ID); err != nil {
		t.Fatal(err)
	}
	const sessionID = "ses-workspace-unproved"
	observation := seedWorkspaceReconcileJournal(t, store.Root, record, sessionID)
	absenceFailure := errors.New("guest namespace observation unavailable")
	provider := &daemonLifecycleBackend{observation: observation, absenceErr: absenceFailure}
	owners, reasons := reconcileRestartResidue(context.Background(), environment.Store{Root: store.Root}, record, observation, provider)
	if len(owners) != 0 || !slices.Contains(reasons, "workspace-view-absence-unproved") {
		t.Fatalf("unproved workspace restart owners=%+v reasons=%v", owners, reasons)
	}

	coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store: journalStore, DaemonID: "daemon-workspace-blocked", IdleGrace: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Reconcile(context.Background(), lifecycle.ReconcileInput{
		EnvironmentID: record.ID, InstanceName: record.InstanceName, Observation: observation,
		AdditionalUnproved: true,
	}); err != nil {
		t.Fatal(err)
	}
	status := coordinator.Snapshot()[0]
	if status.Activity != lifecycle.ActivityBlocked || status.ReasonCode != "provider-state-unproved" || len(status.Orphans) < 2 {
		t.Fatalf("unproved workspace state did not remain blocked: %+v", status)
	}
	request := lifecycle.AttachRequest{
		EnvironmentID: record.ID, InstanceName: record.InstanceName, SessionID: "ses-workspace-next", Observation: observation,
	}
	if _, err := coordinator.BeginAttach(context.Background(), request); !errors.Is(err, lifecycle.ErrAttachBlocked) {
		t.Fatalf("unproved workspace authority allowed attach: %v", err)
	}
}

func TestWorkspaceProviderAbsenceProofRejectsUnrecognizedOwner(t *testing.T) {
	store, record := daemonLifecycleEnvironment(t)
	journalStore := lifecycle.JournalStore{Root: store.Root}
	if err := journalStore.Remove(record.ID); err != nil {
		t.Fatal(err)
	}
	seedWorkspaceReconcileJournal(t, store.Root, record, "ses-workspace-owner")
	journal, err := journalStore.Load(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	for index := range journal.Resources {
		if journal.Resources[index].Ref.Kind == lifecycle.KindWorkspaceHostProvider {
			journal.Resources[index].Owner.Kind = "session"
			journal.Resources[index].Owner.ID = "ses-workspace-owner"
		}
	}
	// The closed lifecycle catalog rejects this forged owner before it can become
	// restart evidence. This proves the singleton-daemon absence shortcut is not
	// available to another owner class.
	if err := journalStore.Write(journal); err == nil {
		t.Fatal("workspace provider with a non-manager owner entered the durable catalog")
	}
}

func seedWorkspaceReconcileJournal(t *testing.T, storeRoot string, record environment.Record, sessionID string) backend.LifecycleObservation {
	t.Helper()
	observation := backend.LifecycleObservation{
		State: backend.LifecycleRunning, InstanceName: record.InstanceName,
		BootID: "01234567-89ab-cdef-0123-456789abcdef", ObservedAt: time.Now().UTC(),
	}
	coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store: lifecycle.JournalStore{Root: storeRoot}, DaemonID: "daemon-workspace-previous", IdleGrace: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	registration, err := coordinator.BeginAttach(context.Background(), lifecycle.AttachRequest{
		EnvironmentID: record.ID, InstanceName: record.InstanceName, SessionID: sessionID, Observation: observation,
	})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := registration.Register(context.Background(), lifecycle.RegistrationSpec{
		Kind: lifecycle.KindWorkspaceHostProvider, ID: "provider-workspace-restart",
		OwnerKind: "manager", OwnerID: "provider-workspace-restart", State: lifecycle.StatePlanned,
		Dependencies: []lifecycle.DependencySpec{{Ref: registration.Root(), StopMode: lifecycle.StopModeDrain}},
		Persistence:  lifecycle.PersistenceEphemeral, ClosePolicy: lifecycle.ClosePreStopDrain, PossibleVMDependency: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = registration.Register(context.Background(), lifecycle.RegistrationSpec{
		Kind: lifecycle.KindWorkspaceGuestView, ID: "view-workspace-restart",
		OwnerKind: "session", OwnerID: sessionID, State: lifecycle.StatePlanned,
		Dependencies: []lifecycle.DependencySpec{
			{Ref: registration.Root(), StopMode: lifecycle.StopModeDrain},
			{Ref: registration.Session(), StopMode: lifecycle.StopModeDrain},
			{Ref: provider, StopMode: lifecycle.StopModeDrain},
		},
		Persistence: lifecycle.PersistenceEphemeral, ClosePolicy: lifecycle.ClosePreStopDrain, PossibleVMDependency: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registration.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	return observation
}
