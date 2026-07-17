package lifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJournalRoundTripIsStrictAndBounded(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	store := JournalStore{Root: root}
	journal := validJournal(t)
	if err := store.Write(journal); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(journal.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.StartGeneration != journal.StartGeneration || loaded.Incarnation == nil || loaded.Incarnation.BootID != testBootID {
		t.Fatalf("unexpected journal: %+v", loaded)
	}
	path := filepath.Join(root, journalDirName, journal.EnvironmentID, journalFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "/Users/") || strings.Contains(string(data), "token") {
		t.Fatalf("journal leaked forbidden material: %s", data)
	}
	if err := os.WriteFile(path, append(data[:len(data)-1], []byte(`,"unknown":true}`)...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(journal.EnvironmentID); err == nil {
		t.Fatal("unknown journal field accepted")
	}
}

func TestJournalRejectsSymlinkedEnvironmentDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	lifecycleDir := filepath.Join(root, journalDirName)
	if err := os.Mkdir(lifecycleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(lifecycleDir, "env_test")); err != nil {
		t.Fatal(err)
	}
	if err := (JournalStore{Root: root}).Write(validJournal(t)); err == nil {
		t.Fatal("symlinked journal ancestor accepted")
	}
}

func TestJournalRejectsTerminalAndOversizedResourceSets(t *testing.T) {
	journal := validJournal(t)
	journal.Resources[0].State = StateReleased
	if err := journal.Validate(); err == nil {
		t.Fatal("terminal resource persisted as live discovery")
	}
	journal = validJournal(t)
	journal.Resources = make([]Resource, maxJournalResources+1)
	if err := journal.Validate(); err == nil {
		t.Fatal("oversized resource inventory accepted")
	}
}

func TestJournalRejectsCrossGenerationAndMalformedStopMetadata(t *testing.T) {
	for name, mutate := range map[string]func(*Journal){
		"resource-owner-generation": func(journal *Journal) {
			journal.Resources[0].Owner.Generation++
		},
		"fact-generation": func(journal *Journal) {
			journal.Facts = []Fact{{
				Kind: KindHostFSStaged, ID: "staged-test", Class: FactRetained,
				Generation: journal.StartGeneration + 1, RecordedAt: journal.UpdatedAt,
			}}
		},
		"deadline-incarnation": func(journal *Journal) {
			other := *journal.Incarnation
			other.BootID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
			journal.IdleDeadline = &IdleDeadline{
				Incarnation: other, DaemonInstanceID: "daemon-test",
				ScheduledAt: journal.UpdatedAt, Deadline: journal.UpdatedAt.Add(time.Second), Generation: 1,
			}
		},
		"stop-observation": func(journal *Journal) {
			journal.StopAttempt = &StopAttempt{
				ID: "stop-test", Incarnation: *journal.Incarnation, DaemonInstanceID: "daemon-test",
				Mode: "automatic", State: "unknown", StartedAt: journal.UpdatedAt,
				Observation: &backendObservationSnapshot{State: "unknown"},
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			journal := validJournal(t)
			mutate(&journal)
			if err := journal.Validate(); err == nil {
				t.Fatal("malformed lifecycle journal was accepted")
			}
		})
	}
}

func validJournal(t *testing.T) Journal {
	t.Helper()
	now := time.Now().UTC()
	incarnation := EnvironmentRef{EnvironmentID: "env_test", StartGeneration: 1, InstanceName: "hideout-test", BootID: testBootID}
	root := testResource(KindBackendIncarnation, incarnation.EnvironmentID, 1, StateActive, "daemon", PersistenceEphemeral, CloseCoTerminateWithRoot)
	root.Incarnation = &incarnation
	return Journal{
		Schema: JournalSchema, EnvironmentID: incarnation.EnvironmentID,
		StartGeneration: 1, Incarnation: &incarnation, Resources: []Resource{root},
		Reconciliation: Reconciliation{DaemonInstanceID: "daemon-test", State: "complete", ObservedAt: now}, UpdatedAt: now,
	}
}
