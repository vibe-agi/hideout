package manager

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/recovery"
	"github.com/vibe-agi/hideout/internal/session"
)

func TestActiveSessionSummaryBuilderIsAuthoritativeAndPathFree(t *testing.T) {
	root := t.TempDir()
	store := environment.Store{Root: root}
	env, err := store.Create(environment.Spec{
		Name:              "owners",
		ImageRef:          environment.BuiltinBaseImage,
		Profile:           "default",
		Backend:           "lima",
		Mode:              environment.ModeWorkspaceBound,
		MachineIdentityID: testEnvironmentMachineIdentityID(), BootConfigurationID: testEnvironmentBootConfigurationID(),
		BoundWorkspace: "/Users/private/project",
		BoundGuestRoot: "/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := session.OwnerRecord{
		Schema:            session.ActiveSessionSchema,
		SessionID:         "ses_20260716T120000Z_0123456789abcdef",
		EnvironmentID:     env.ID,
		Profile:           "default",
		Backend:           "lima",
		WorkspaceID:       "wrk_" + strings.Repeat("a", 64),
		SessionSnapshotID: testSessionSnapshotID(),
		State:             session.OwnerStateRunning,
		TerminalMode:      session.TerminalPTY,
		StartedAt:         now,
		UpdatedAt:         now,
		CommandClass:      "bash",
	}
	owner, err := session.AcquireOwner(store.OwnerRoot(env.ID), record)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	core := New(profile.Store{Root: root})
	active, err := core.ActiveSessionSummaries()
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != record.SessionID || active[0].OwnerStatus != session.OwnerLive {
		t.Fatalf("active=%+v", active)
	}
	if active[0].SessionSnapshotID != record.SessionSnapshotID {
		t.Fatalf("active session snapshot=%q want %q", active[0].SessionSnapshotID, record.SessionSnapshotID)
	}
	overview, err := core.Overview(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Sessions) != 1 || overview.Sessions[0].Path != "" || overview.Sessions[0].EnvironmentID != env.ID {
		t.Fatalf("sessions=%+v", overview.Sessions)
	}
	if overview.Sessions[0].SessionSnapshotID != record.SessionSnapshotID {
		t.Fatalf("overview session snapshot=%q want %q", overview.Sessions[0].SessionSnapshotID, record.SessionSnapshotID)
	}
	if len(overview.Environments) != 1 || overview.Environments[0].ActiveSessions != 1 || overview.Environments[0].OwnerHealth != "live" {
		t.Fatalf("environments=%+v", overview.Environments)
	}
	data, err := json.Marshal(overview.Sessions[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"/Users/private", "owner.lock", "\"pid\"", "cap_", "HIDEOUT_SECRET_"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("summary leaked %q: %s", forbidden, data)
		}
	}
}

func TestNewAttachRefusesStaleOrFailedOwnerEvidence(t *testing.T) {
	root := t.TempDir()
	store := environment.Store{Root: root}
	env, err := store.Create(environment.Spec{
		Name: "blocked-attach", ImageRef: environment.BuiltinBaseImage, Profile: "default", Backend: "lima",
		Mode: environment.ModeWorkspaceBound, MachineIdentityID: testEnvironmentMachineIdentityID(), BootConfigurationID: testEnvironmentBootConfigurationID(), BoundWorkspace: t.TempDir(), BoundGuestRoot: "/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := session.OwnerRecord{
		Schema: session.ActiveSessionSchema, SessionID: "ses_20260716T120000Z_0123456789abcdef",
		EnvironmentID: env.ID, Profile: "default", Backend: "lima", WorkspaceID: "wrk_" + strings.Repeat("a", 64), SessionSnapshotID: testSessionSnapshotID(),
		State: session.OwnerStateRunning, TerminalMode: session.TerminalNone,
		StartedAt: now, UpdatedAt: now, CommandClass: "bash",
	}
	owner, err := session.AcquireOwner(store.OwnerRoot(env.ID), record)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Update(session.OwnerStateFailed, "cleanup failed"); err != nil {
		t.Fatal(err)
	}
	if err := owner.Release(); err != nil {
		t.Fatal(err)
	}

	err = requireAttachableEnvironmentOwners(store, env.ID)
	var ownerErr *EnvironmentOwnerError
	if !errors.As(err, &ownerErr) || ownerErr.Code != recovery.CodeSessionCleanupFailed {
		t.Fatalf("attach error=%T %v", err, err)
	}
	if _, statErr := os.Stat(filepath.Join(store.OwnerRoot(env.ID), record.SessionID)); statErr != nil {
		t.Fatalf("failed evidence was removed: %v", statErr)
	}
}

func TestRunStatusServesProfileScopedPathFreeOwnerModel(t *testing.T) {
	root := t.TempDir()
	store := environment.Store{Root: root}
	env, err := store.Create(environment.Spec{
		Name: "api-owners", ImageRef: environment.BuiltinBaseImage, Profile: "default", Backend: "lima",
		Mode: environment.ModeWorkspaceBound, MachineIdentityID: testEnvironmentMachineIdentityID(), BootConfigurationID: testEnvironmentBootConfigurationID(), BoundWorkspace: "/Users/private/project", BoundGuestRoot: "/workspace", InstanceName: "hideout-api-owners",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareRuntimeRoot(env.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	owner, err := session.AcquireOwner(store.OwnerRoot(env.ID), session.OwnerRecord{
		Schema: session.ActiveSessionSchema, SessionID: "ses_20260716T120000Z_0123456789abcdef",
		EnvironmentID: env.ID, Profile: "default", Backend: "lima", WorkspaceID: "wrk_" + strings.Repeat("a", 64), SessionSnapshotID: testSessionSnapshotID(),
		State: session.OwnerStateRunning, TerminalMode: session.TerminalPTY,
		StartedAt: now, UpdatedAt: now, CommandClass: "bash",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	api := NewAPI(New(profile.Store{Root: root}), "operator-token", time.Minute)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/run/status?profile=default", nil)
	request.Host = "127.0.0.1"
	request.Header.Set("Authorization", "Bearer operator-token")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{`"ownerStatus":"live"`, `"state":"running"`, `"terminalMode":"pty"`, `"environmentId":"` + env.ID + `"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("run/status missing %q: %s", want, body)
		}
	}
	for _, forbidden := range []string{"/Users/private", "owner.lock", `"pid"`, "cap_", "HIDEOUT_SECRET_"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("run/status leaked %q: %s", forbidden, body)
		}
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/run/status?profile=other", nil)
	request.Host = "127.0.0.1"
	request.Header.Set("Authorization", "Bearer operator-token")
	response = httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"sessions":[]`) {
		t.Fatalf("profile scope failed: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestActiveSessionSummarySurfacesUnprovableOwnerWithoutRawRecord(t *testing.T) {
	root := t.TempDir()
	store := environment.Store{Root: root}
	env, err := store.Create(environment.Spec{
		Name: "broken-owner", ImageRef: environment.BuiltinBaseImage, Profile: "default", Backend: "lima",
		Mode: environment.ModeWorkspaceBound, MachineIdentityID: testEnvironmentMachineIdentityID(), BootConfigurationID: testEnvironmentBootConfigurationID(), BoundWorkspace: t.TempDir(), BoundGuestRoot: "/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	id := "ses_20260716T120000Z_abcdefabcdefabcd"
	dir := filepath.Join(store.OwnerRoot(env.ID), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "owner.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	active, err := New(profile.Store{Root: root}).ActiveSessionSummaries()
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].OwnerStatus != session.OwnerUnprovable || active[0].ID != id {
		t.Fatalf("active=%+v", active)
	}
}
