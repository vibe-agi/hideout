package profile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/profilestate"
)

func TestEnvironmentBatchParticipantHasOneCrossStoreVisibilityPoint(t *testing.T) {
	root := t.TempDir()
	profileStore := Store{Root: root}
	environmentStore := environment.Store{Root: root}
	portable := Default("imported-profile")
	portable.Metadata = nil
	participant := EnvironmentBatchParticipant{
		Store: profileStore, Profiles: []Profile{portable},
	}
	record := profileBatchEnvironmentFixture(t, portable.Name)
	interrupted := errors.New("injected profile finalization interruption")
	failing := profileBatchParticipantFailure{
		delegate: participant, finalizeErr: interrupted,
	}
	if _, err := environmentStore.PublishBatchWithParticipant(
		"op_profile_batch0001", []environment.Record{record}, failing,
	); !errors.Is(err, interrupted) {
		t.Fatalf("post-activation interruption error=%v", err)
	}

	loaded, err := profileStore.Load(portable.Name)
	if err != nil || loaded.Metadata["profileId"] == "" ||
		loaded.Metadata["identityId"] == "" || loaded.Metadata["machineId"] == "" {
		t.Fatalf("activated profile=%+v err=%v", loaded, err)
	}
	environments, err := environmentStore.List()
	if err != nil || len(environments) != 1 || environments[0].ID != record.ID {
		t.Fatalf("activated environments=%+v err=%v", environments, err)
	}
	if err := profileStore.Save(loaded); !errors.Is(err, ErrBatchFinalizationRequired) {
		t.Fatalf("profile mutation during finalization error=%v", err)
	}

	publication, err := environmentStore.PublishBatchWithParticipant(
		"op_profile_batch0001", []environment.Record{record}, participant,
	)
	if err != nil || publication.ParticipantDigest == "" {
		t.Fatalf("replayed publication=%+v err=%v", publication, err)
	}
	if _, err := os.Lstat(filepath.Join(
		profileStore.ProfileDir(portable.Name), profileBatchPendingFile,
	)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("profile pending marker survived finalization: %v", err)
	}
	if err := profileStore.Save(loaded); err != nil {
		t.Fatalf("committed profile remained mutation-blocked: %v", err)
	}
}

func TestEnvironmentBatchParticipantHidesPreparedProfileBeforeActivation(t *testing.T) {
	root := t.TempDir()
	profileStore := Store{Root: root}
	environmentStore := environment.Store{Root: root}
	portable := Default("prepared-profile")
	portable.Metadata = nil
	participant := EnvironmentBatchParticipant{
		Store: profileStore, Profiles: []Profile{portable},
	}
	record := profileBatchEnvironmentFixture(t, portable.Name)
	interrupted := errors.New("injected profile preparation interruption")
	failing := profileBatchParticipantFailure{
		delegate: participant, prepareErr: interrupted,
	}
	if _, err := environmentStore.PublishBatchWithParticipant(
		"op_profile_batch0002", []environment.Record{record}, failing,
	); !errors.Is(err, interrupted) {
		t.Fatalf("pre-activation interruption error=%v", err)
	}
	if _, err := profileStore.Load(portable.Name); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("prepared profile became visible before activation: %v", err)
	}
	if environments, err := environmentStore.List(); err != nil || len(environments) != 0 {
		t.Fatalf("environment prefix became visible: %+v err=%v", environments, err)
	}
	if _, err := environmentStore.PublishBatchWithParticipant(
		"op_profile_batch0002", []environment.Record{record}, participant,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := profileStore.Load(portable.Name); err != nil {
		t.Fatalf("profile did not converge after replay: %v", err)
	}
}

func TestEnvironmentBatchParticipantPreservesImportDerivedMetadata(t *testing.T) {
	root := t.TempDir()
	profileStore := Store{Root: root}
	environmentStore := environment.Store{Root: root}
	const batchID = "op_profile_batch0003"
	portable := Default("derived-profile")
	portable.Metadata = map[string]string{
		"profileId":   "prf_0123456789abcdef0123456789abcdef",
		"identityId":  "id_0123456789abcdef0123456789abcdef",
		"machineId":   "0123456789abcdef0123456789abcdef",
		"createdAt":   "2026-08-02T10:00:00Z",
		"lineageMode": "migration",
		"createdFrom": batchID,
	}
	participant := EnvironmentBatchParticipant{
		Store: profileStore, Profiles: []Profile{portable},
	}
	record := profileBatchEnvironmentFixture(t, portable.Name)
	if _, err := environmentStore.PublishBatchWithParticipant(
		batchID, []environment.Record{record}, participant,
	); err != nil {
		t.Fatal(err)
	}
	loaded, err := profileStore.Load(portable.Name)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"profileId", "identityId", "machineId", "createdAt", "lineageMode", "createdFrom"} {
		if loaded.Metadata[key] != portable.Metadata[key] {
			t.Fatalf("destination metadata %s changed: want=%q got=%q", key, portable.Metadata[key], loaded.Metadata[key])
		}
	}

	foreignRoot := t.TempDir()
	foreignStore := environment.Store{Root: foreignRoot}
	foreignParticipant := EnvironmentBatchParticipant{
		Store: Store{Root: foreignRoot}, Profiles: []Profile{portable},
	}
	foreignRecord := profileBatchEnvironmentFixture(t, portable.Name)
	if _, err := foreignStore.PublishBatchWithParticipant(
		"op_profile_batch_other", []environment.Record{foreignRecord}, foreignParticipant,
	); !errors.Is(err, ErrBatchConflict) {
		t.Fatalf("foreign import metadata error=%v", err)
	}
	if environments, err := foreignStore.List(); err != nil || len(environments) != 0 {
		t.Fatalf("foreign batch became visible: %+v err=%v", environments, err)
	}
}

func TestEnvironmentBatchParticipantPreflightIsReadOnly(t *testing.T) {
	root := t.TempDir()
	profileStore := Store{Root: root}
	environmentStore := environment.Store{Root: root}
	portable := Default("preflight-profile")
	portable.Metadata = nil
	participant := EnvironmentBatchParticipant{
		Store: profileStore, Profiles: []Profile{portable},
	}
	record := profileBatchEnvironmentFixture(t, portable.Name)
	publication, err := environmentStore.PreflightBatchWithParticipant(
		"op_profile_batch0005", []environment.Record{record}, participant,
	)
	if err != nil || publication.Validate() != nil {
		t.Fatalf("preflight publication=%+v error=%v", publication, err)
	}
	for _, path := range []string{
		profileStore.ProfileDir(portable.Name),
		filepath.Join(root, "environments", record.ID),
	} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("preflight mutated %q: %v", path, err)
		}
	}
	if _, err := environmentStore.PublishBatchWithParticipant(
		"op_profile_batch0005", []environment.Record{record}, participant,
	); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentBatchParticipantBindingRejectsProfileMutationOnReplay(t *testing.T) {
	root := t.TempDir()
	profileStore := Store{Root: root}
	environmentStore := environment.Store{Root: root}
	portable := Default("binding-profile")
	portable.Metadata = nil
	participant := EnvironmentBatchParticipant{
		Store: profileStore, Profiles: []Profile{portable},
	}
	record := profileBatchEnvironmentFixture(t, portable.Name)
	if _, err := environmentStore.PublishBatchWithParticipant(
		"op_profile_batch0004", []environment.Record{record}, participant,
	); err != nil {
		t.Fatal(err)
	}

	changed := portable
	changed.Git.UserName = "Different Import"
	changedParticipant := EnvironmentBatchParticipant{
		Store: profileStore, Profiles: []Profile{changed},
	}
	if _, err := environmentStore.PublishBatchWithParticipant(
		"op_profile_batch0004", []environment.Record{record}, changedParticipant,
	); !errors.Is(err, environment.ErrBatchConflict) {
		t.Fatalf("changed participant replay error=%v", err)
	}
}

func TestMigrationEnvironmentBatchParticipantAtomicallyPublishesImportedApplicationState(t *testing.T) {
	root := t.TempDir()
	profileStore := Store{Root: root}
	environmentStore := environment.Store{Root: root}
	const batchID = "op_profile_state_batch01"
	portable := Default("imported-state")
	portable.Metadata = nil
	owner, stage, sentinel := profileBatchImportedStateFixture(
		t, root, batchID, portable.Name,
	)
	participant := EnvironmentBatchParticipant{
		Store: profileStore, Profiles: []Profile{portable},
		ImportedStates: []ImportedState{{
			ProfileName: portable.Name, StagePath: stage, Owner: owner,
		}},
	}
	record := profileBatchEnvironmentFixture(t, portable.Name)

	publication, err := environmentStore.PreflightBatchWithParticipant(
		batchID, []environment.Record{record}, participant,
	)
	if err != nil || publication.Validate() != nil {
		t.Fatalf("preflight publication=%+v error=%v", publication, err)
	}
	if _, err := os.Lstat(profileStore.ProfileDir(portable.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preflight exposed destination profile: %v", err)
	}
	if err := profilestate.VerifyStage(stage, owner); err != nil {
		t.Fatalf("preflight mutated private state stage: %v", err)
	}

	publication, err = environmentStore.PublishBatchWithParticipant(
		batchID, []environment.Record{record}, participant,
	)
	if err != nil || publication.Validate() != nil {
		t.Fatalf("publish publication=%+v error=%v", publication, err)
	}
	final := profileStore.ProfileDir(portable.Name)
	content, err := os.ReadFile(filepath.Join(final, "home", ".claude", "history.jsonl"))
	if err != nil || string(content) != sentinel {
		t.Fatalf("published application state=%q error=%v", content, err)
	}
	if _, err := os.Lstat(stage); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private stage survived atomic rename: %v", err)
	}
	gitConfig, err := os.ReadFile(filepath.Join(final, "home", ".gitconfig"))
	if err != nil || strings.Contains(string(gitConfig), "SOURCE-IDENTITY-MUST-NOT-SURVIVE") {
		t.Fatalf("source-generated identity survived: %q error=%v", gitConfig, err)
	}
	for _, marker := range profilestate.MarkerNames() {
		if _, err := os.Lstat(filepath.Join(final, marker)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("temporary state marker %q survived finalization: %v", marker, err)
		}
	}
	loaded, err := profileStore.Load(portable.Name)
	if err != nil || loaded.Metadata["profileId"] == "" ||
		loaded.Metadata["identityId"] == "" || loaded.Metadata["machineId"] == "" ||
		loaded.Metadata["createdFrom"] != batchID {
		t.Fatalf("fresh destination identity=%+v error=%v", loaded.Metadata, err)
	}
	if _, err := environmentStore.PublishBatchWithParticipant(
		batchID, []environment.Record{record}, participant,
	); err != nil {
		t.Fatalf("completed publication did not replay: %v", err)
	}
}

func TestMigrationEnvironmentBatchParticipantRejectsTamperedImportedStateBeforeVisibility(t *testing.T) {
	root := t.TempDir()
	profileStore := Store{Root: root}
	environmentStore := environment.Store{Root: root}
	const batchID = "op_profile_state_tamper01"
	portable := Default("tampered-state")
	portable.Metadata = nil
	owner, stage, _ := profileBatchImportedStateFixture(t, root, batchID, portable.Name)
	if err := os.WriteFile(
		filepath.Join(stage, "home", ".claude", "history.jsonl"),
		[]byte("tampered\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	participant := EnvironmentBatchParticipant{
		Store: profileStore, Profiles: []Profile{portable},
		ImportedStates: []ImportedState{{
			ProfileName: portable.Name, StagePath: stage, Owner: owner,
		}},
	}
	record := profileBatchEnvironmentFixture(t, portable.Name)
	if _, err := environmentStore.PreflightBatchWithParticipant(
		batchID, []environment.Record{record}, participant,
	); !errors.Is(err, ErrBatchConflict) {
		t.Fatalf("tampered state preflight error=%v", err)
	}
	if _, err := os.Lstat(profileStore.ProfileDir(portable.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tampered state exposed profile: %v", err)
	}
	if records, err := environmentStore.List(); err != nil || len(records) != 0 {
		t.Fatalf("tampered state exposed environments=%+v error=%v", records, err)
	}
}

func profileBatchImportedStateFixture(
	t *testing.T,
	destinationRoot string,
	operationID string,
	profileName string,
) (profilestate.Owner, string, string) {
	t.Helper()
	source := filepath.Join(t.TempDir(), "source-profile")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range profilestate.IncludedRoots() {
		if err := os.Mkdir(filepath.Join(source, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(source, "home", ".local"), 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := "claude-history-survives\n"
	stateDir := filepath.Join(source, "home", ".claude")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(stateDir, "history.jsonl"), []byte(sentinel), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(source, "home", ".gitconfig"),
		[]byte("SOURCE-IDENTITY-MUST-NOT-SURVIVE\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	snapshot, err := profilestate.Capture(source)
	if err != nil {
		t.Fatal(err)
	}
	owner := profilestate.Owner{
		OperationID: operationID, ProfileName: profileName,
		ComponentID:   "profilestate_batch_component",
		ContentDigest: snapshot.Digest(), LogicalBytes: snapshot.LogicalBytes(),
	}
	materializer, err := profilestate.NewMaterializer(
		filepath.Join(destinationRoot, "profiles"), owner,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Write(context.Background(), 31, materializer.Consume); err != nil {
		_ = materializer.Abort()
		t.Fatal(err)
	}
	if err := materializer.Finish(); err != nil {
		_ = materializer.Abort()
		t.Fatal(err)
	}
	return owner, materializer.Path(), sentinel
}

type profileBatchParticipantFailure struct {
	delegate    EnvironmentBatchParticipant
	prepareErr  error
	finalizeErr error
}

func (failure profileBatchParticipantFailure) BindingDigest() (string, error) {
	return failure.delegate.BindingDigest()
}

func (failure profileBatchParticipantFailure) Prepare(
	publication environment.BatchPublication,
) error {
	if err := failure.delegate.Prepare(publication); err != nil {
		return err
	}
	return failure.prepareErr
}

func (failure profileBatchParticipantFailure) Preflight(
	publication environment.BatchPublication,
) error {
	return failure.delegate.Preflight(publication)
}

func (failure profileBatchParticipantFailure) Finalize(
	publication environment.BatchPublication,
) error {
	if failure.finalizeErr != nil {
		return failure.finalizeErr
	}
	return failure.delegate.Finalize(publication)
}

func profileBatchEnvironmentFixture(t *testing.T, profileName string) environment.Record {
	t.Helper()
	return environment.Record{
		Version: environment.RecordVersion, ID: "env_profilebatch0001",
		Name: "imported-environment", ImageRef: environment.BuiltinBaseImage,
		Profile: profileName, Backend: "lima", Mode: environment.ModeDedicated,
		MachineIdentityID:   "sha256:" + strings.Repeat("a", 64),
		BootConfigurationID: "sha256:" + strings.Repeat("b", 64),
		DedicatedWorkspace:  filepath.Join(t.TempDir(), "workspace"),
		DedicatedGuestRoot:  "/workspace", User: "developer",
		InstanceName: "backend-profilebatch0001", Status: environment.StatusStopped,
		CreatedAt: time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC),
	}
}

var _ environment.BatchParticipant = profileBatchParticipantFailure{}
