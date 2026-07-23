package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
)

func TestDisposalIntentValidationAndTransitionsAreClosed(t *testing.T) {
	now := time.Date(2026, 7, 23, 1, 2, 3, 0, time.UTC)
	valid := DisposalIntent{
		Schema:       DisposalIntentSchema,
		Authority:    DisposalAuthorityRunRM,
		Backend:      "lima",
		InstanceName: "hideout-default-env-disposable",
		RecordDigest: strings.Repeat("a", 64),
		Generation:   7,
		State:        DisposalStatePlanned,
		RequestedAt:  now,
		UpdatedAt:    now,
	}
	if err := valid.Validate(7); err != nil {
		t.Fatalf("valid intent rejected: %v", err)
	}

	for name, mutate := range map[string]func(*DisposalIntent){
		"schema":       func(intent *DisposalIntent) { intent.Schema = "hideout.disposal-intent/v2" },
		"authority":    func(intent *DisposalIntent) { intent.Authority = "name-prefix" },
		"backend":      func(intent *DisposalIntent) { intent.Backend = "" },
		"instance":     func(intent *DisposalIntent) { intent.InstanceName = "" },
		"digest-case":  func(intent *DisposalIntent) { intent.RecordDigest = strings.Repeat("A", 64) },
		"digest-short": func(intent *DisposalIntent) { intent.RecordDigest = "abc" },
		"generation":   func(intent *DisposalIntent) { intent.Generation++ },
		"state":        func(intent *DisposalIntent) { intent.State = "removed" },
		"timestamp":    func(intent *DisposalIntent) { intent.UpdatedAt = now.Add(-time.Second) },
		"planned-reason": func(intent *DisposalIntent) {
			intent.ReasonCode = "backend-observation-unproved"
		},
		"blocked-without-reason": func(intent *DisposalIntent) {
			intent.State = DisposalStateBlocked
		},
	} {
		t.Run(name, func(t *testing.T) {
			intent := valid
			mutate(&intent)
			if err := intent.Validate(7); err == nil {
				t.Fatalf("invalid intent accepted: %+v", intent)
			}
		})
	}

	allowed := [][2]string{
		{DisposalStatePlanned, DisposalStatePlanned},
		{DisposalStatePlanned, DisposalStateBackendAbsent},
		{DisposalStatePlanned, DisposalStateBlocked},
		{DisposalStateBackendAbsent, DisposalStateMetadataCleaning},
		{DisposalStateBackendAbsent, DisposalStateBlocked},
		{DisposalStateMetadataCleaning, DisposalStateBlocked},
		{DisposalStateBlocked, DisposalStatePlanned},
	}
	for _, transition := range allowed {
		if err := ValidateDisposalTransition(transition[0], transition[1]); err != nil {
			t.Errorf("%s -> %s rejected: %v", transition[0], transition[1], err)
		}
	}
	for _, transition := range [][2]string{
		{DisposalStatePlanned, DisposalStateMetadataCleaning},
		{DisposalStateBackendAbsent, DisposalStatePlanned},
		{DisposalStateMetadataCleaning, DisposalStatePlanned},
		{DisposalStateBlocked, DisposalStateBackendAbsent},
		{"unknown", DisposalStatePlanned},
	} {
		if err := ValidateDisposalTransition(transition[0], transition[1]); err == nil {
			t.Errorf("%s -> %s accepted", transition[0], transition[1])
		}
	}
}

func TestJournalDisposalIntentRoundTripRejectsUnknownNestedFields(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	store := JournalStore{Root: root}
	journal := validJournal(t)
	journal.Disposal = validDisposalIntent(journal.StartGeneration, journal.UpdatedAt)
	if err := store.Write(journal); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(journal.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Disposal == nil || loaded.Disposal.RecordDigest != journal.Disposal.RecordDigest {
		t.Fatalf("disposal intent lost: %+v", loaded.Disposal)
	}

	path := filepath.Join(root, journalDirName, journal.EnvironmentID, journalFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	document["disposal"].(map[string]any)["unknown"] = true
	data, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(journal.EnvironmentID); err == nil {
		t.Fatal("unknown disposal intent field accepted")
	}
}

func validDisposalIntent(generation uint64, now time.Time) *DisposalIntent {
	return &DisposalIntent{
		Schema: DisposalIntentSchema, Authority: DisposalAuthorityRunRM,
		Backend: "lima", InstanceName: "hideout-default-env-disposable",
		RecordDigest: strings.Repeat("a", 64), Generation: generation,
		State: DisposalStatePlanned, RequestedAt: now, UpdatedAt: now,
	}
}

func TestCoordinatorDisposalPersistsResumesAndCompletesEveryCrashCut(t *testing.T) {
	root := privateLifecycleRoot(t)
	now := time.Date(2026, 7, 23, 2, 0, 0, 0, time.UTC)
	request := testDisposalRequest()

	coordinator := disposalCoordinatorAt(t, root, "daemon-one", now)
	intent, err := coordinator.BeginDisposal(context.Background(), request)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if intent.State != DisposalStatePlanned || intent.Generation != 1 {
		t.Fatalf("planned intent=%+v", intent)
	}
	assertStoredDisposalState(t, root, request.EnvironmentID, DisposalStatePlanned)
	if _, err := coordinator.BeginDisposal(context.Background(), request); !errors.Is(err, ErrMutationBlockedByActivity) {
		t.Fatalf("parallel disposal error=%v", err)
	}
	if _, err := coordinator.BeginAttach(context.Background(), AttachRequest{
		EnvironmentID: request.EnvironmentID, InstanceName: request.InstanceName, SessionID: "ses-blocked",
		Observation: backend.LifecycleObservation{
			State: backend.LifecycleRunning, InstanceName: request.InstanceName,
			BootID: testBootID, ObservedAt: now,
		},
	}); !errors.Is(err, ErrAttachBlocked) {
		t.Fatalf("attach during disposal error=%v", err)
	}

	if err := coordinator.Close(); err != nil {
		t.Fatal(err)
	}
	coordinator = disposalCoordinatorAt(t, root, "daemon-two", now.Add(time.Second))
	intent, err = coordinator.BeginDisposal(context.Background(), request)
	if err != nil || intent.State != DisposalStatePlanned {
		t.Fatalf("resume planned intent=%+v err=%v", intent, err)
	}
	if err := coordinator.AdvanceDisposal(context.Background(), request.EnvironmentID, request.RecordDigest, DisposalStateBackendAbsent); err != nil {
		t.Fatalf("advance backend absent: %v", err)
	}
	assertStoredDisposalState(t, root, request.EnvironmentID, DisposalStateBackendAbsent)

	if err := coordinator.Close(); err != nil {
		t.Fatal(err)
	}
	coordinator = disposalCoordinatorAt(t, root, "daemon-three", now.Add(2*time.Second))
	intent, err = coordinator.BeginDisposal(context.Background(), request)
	if err != nil || intent.State != DisposalStateBackendAbsent {
		t.Fatalf("resume backend-absent intent=%+v err=%v", intent, err)
	}
	if err := coordinator.AdvanceDisposal(context.Background(), request.EnvironmentID, request.RecordDigest, DisposalStateMetadataCleaning); err != nil {
		t.Fatalf("advance metadata cleaning: %v", err)
	}
	assertStoredDisposalState(t, root, request.EnvironmentID, DisposalStateMetadataCleaning)

	if err := coordinator.Close(); err != nil {
		t.Fatal(err)
	}
	coordinator = disposalCoordinatorAt(t, root, "daemon-four", now.Add(3*time.Second))
	intent, err = coordinator.BeginDisposal(context.Background(), request)
	if err != nil || intent.State != DisposalStateMetadataCleaning {
		t.Fatalf("resume metadata-cleaning intent=%+v err=%v", intent, err)
	}
	if err := coordinator.CompleteDisposalMetadata(context.Background(), request.EnvironmentID, request.RecordDigest); err != nil {
		t.Fatalf("complete metadata: %v", err)
	}
	if _, err := (JournalStore{Root: root}).Load(request.EnvironmentID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("journal survived complete disposal: %v", err)
	}
}

func TestCoordinatorDisposalFailsClosedAndBlockedIntentCanBeRevalidated(t *testing.T) {
	root := privateLifecycleRoot(t)
	now := time.Date(2026, 7, 23, 3, 0, 0, 0, time.UTC)
	coordinator := disposalCoordinatorAt(t, root, "daemon-one", now)
	request := testDisposalRequest()

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := coordinator.BeginDisposal(cancelled, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled begin error=%v", err)
	}
	if _, err := (JournalStore{Root: root}).Load(request.EnvironmentID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled begin persisted journal: %v", err)
	}

	if _, err := coordinator.BeginDisposal(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.BlockDisposal(context.Background(), request.EnvironmentID, request.RecordDigest, DisposalReasonOwnerMetadataUnproved); err != nil {
		t.Fatalf("block: %v", err)
	}
	journal := assertStoredDisposalState(t, root, request.EnvironmentID, DisposalStateBlocked)
	if journal.Disposal.ReasonCode != DisposalReasonOwnerMetadataUnproved {
		t.Fatalf("block reason=%q", journal.Disposal.ReasonCode)
	}
	statuses := coordinator.Snapshot()
	if len(statuses) != 1 || statuses[0].DisposalPhase != DisposalStateBlocked ||
		statuses[0].DisposalReasonCode != DisposalReasonOwnerMetadataUnproved {
		t.Fatalf("disposal status=%+v", statuses)
	}
	statusData, err := json.Marshal(statuses[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(statusData), request.RecordDigest) ||
		strings.Contains(string(statusData), request.InstanceName) {
		t.Fatalf("public disposal status leaked durable identity: %s", statusData)
	}
	if err := coordinator.BlockDisposal(context.Background(), request.EnvironmentID, request.RecordDigest, "cap_0123456789abcdef"); err == nil {
		t.Fatal("unregistered disposal reason code was accepted")
	}

	mismatch := request
	mismatch.RecordDigest = strings.Repeat("b", 64)
	if _, err := coordinator.BeginDisposal(context.Background(), mismatch); err == nil {
		t.Fatal("mismatched record digest resumed durable authority")
	}
	assertStoredDisposalState(t, root, request.EnvironmentID, DisposalStateBlocked)

	intent, err := coordinator.BeginDisposal(context.Background(), request)
	if err != nil || intent.State != DisposalStatePlanned || intent.ReasonCode != "" {
		t.Fatalf("revalidated intent=%+v err=%v", intent, err)
	}
	if err := coordinator.AdvanceDisposal(context.Background(), request.EnvironmentID, mismatch.RecordDigest, DisposalStateBackendAbsent); err == nil {
		t.Fatal("mismatched lease advanced disposal")
	}
	if err := coordinator.AdvanceDisposal(context.Background(), request.EnvironmentID, request.RecordDigest, DisposalStateMetadataCleaning); err == nil {
		t.Fatal("skipped backend-absent transition")
	}
}

func TestCoordinatorDisposalAdmissionRejectsActiveHandle(t *testing.T) {
	coordinator, _ := newTestCoordinator(t, false, nil)
	registration, err := coordinator.BeginAttach(context.Background(), testAttachRequest(backend.LifecycleRunning, testBootID))
	if err != nil {
		t.Fatal(err)
	}
	request := testDisposalRequest()
	request.EnvironmentID = registration.Incarnation().EnvironmentID
	request.InstanceName = registration.Incarnation().InstanceName
	request.Generation = registration.Incarnation().StartGeneration
	if _, err := coordinator.BeginDisposal(context.Background(), request); !errors.Is(err, ErrMutationBlockedByActivity) {
		t.Fatalf("active handle admission error=%v", err)
	}
}

func privateLifecycleRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func disposalCoordinatorAt(t *testing.T, root, daemonID string, now time.Time) *Coordinator {
	t.Helper()
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Store: JournalStore{Root: root}, DaemonID: daemonID,
		IdleGrace: DefaultIdleGrace, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}

func testDisposalRequest() DisposalRequest {
	return DisposalRequest{
		EnvironmentID: "env-disposable", Backend: "lima",
		InstanceName: "hideout-default-env-disposable",
		RecordDigest: strings.Repeat("a", 64), Generation: 1,
	}
}

func assertStoredDisposalState(t *testing.T, root, environmentID, state string) Journal {
	t.Helper()
	journal, err := (JournalStore{Root: root}).Load(environmentID)
	if err != nil {
		t.Fatal(err)
	}
	if journal.Disposal == nil || journal.Disposal.State != state {
		t.Fatalf("journal disposal=%+v want state %q", journal.Disposal, state)
	}
	return journal
}
