package lifecycle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
