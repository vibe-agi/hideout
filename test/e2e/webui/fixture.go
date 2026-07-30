package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/daemon"
	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

const noticeID = "ui-e2e-notice"

const (
	workspaceFixtureKey       = "workspace-view-e2e"
	workspaceFixtureReady     = "ready"
	workspaceFixtureReleaseA  = "release-a"
	workspaceFixturePrivateA  = "/Users/private/workspace-a"
	workspaceFixturePrivateB  = "/Users/private/workspace-b"
	workspaceFixtureRootToken = "cap_0123456789abcdef0123456789abcdef"
)

type Fixture struct {
	StoreRoot         string                 `json:"storeRoot"`
	UIURL             string                 `json:"uiURL"`
	BaseURL           string                 `json:"baseURL"`
	Token             string                 `json:"token"`
	NoticeID          string                 `json:"noticeId"`
	ControlURL        string                 `json:"-"`
	ControlKey        string                 `json:"-"`
	EnvironmentID     string                 `json:"environmentId"`
	MachineIdentityID string                 `json:"machineIdentityId"`
	BrowserEvidence   BrowserConsoleEvidence `json:"browserEvidence"`

	daemon         *daemon.Daemon
	controlServer  *httptest.Server
	browserCleanup func() error
}

type BrowserConsoleEvidence struct {
	SessionID            string `json:"sessionId"`
	EnvironmentID        string `json:"environmentId"`
	BackendIncarnationID string `json:"backendIncarnationId"`
	ExecutionID          string `json:"executionId"`
	FilePath             string `json:"filePath"`
	Domain               string `json:"domain"`
	IP                   string `json:"ip"`
	RiskID               string `json:"riskId"`
	From                 string `json:"from"`
	To                   string `json:"to"`
	RecordCount          int    `json:"recordCount"`
}

func StartFixture() (Fixture, error) {
	root, err := os.MkdirTemp("/tmp", "hdui")
	if err != nil {
		return Fixture{}, err
	}
	if err := os.Chmod(root, 0o700); err != nil {
		_ = os.RemoveAll(root)
		return Fixture{}, err
	}
	store := profile.Store{Root: root}
	defaultProfile, err := store.LoadOrInit("default")
	if err != nil {
		_ = os.RemoveAll(root)
		return Fixture{}, err
	}
	configuration, err := manager.RuntimeConfigurationForProfile(defaultProfile, "lima", environment.ModeShared)
	if err != nil {
		_ = os.RemoveAll(root)
		return Fixture{}, err
	}
	environmentRecord, err := (environment.Store{Root: root}).Create(environment.Spec{
		Name: environment.SharedDisplayName("default"), AutoNamed: true,
		ImageRef: environment.BuiltinBaseImage, Profile: "default", Backend: "lima",
		Mode: environment.ModeShared, SharedSlot: environment.SharedSlotID("default"),
		MachineIdentityID:   configuration.Layers.MachineID,
		BootConfigurationID: configuration.Layers.BootID,
		InstanceName:        "hideout-ui-e2e-shared",
	})
	if err != nil {
		_ = os.RemoveAll(root)
		return Fixture{}, err
	}
	environmentRecord.Status = environment.StatusRunning
	if err := (environment.Store{Root: root}).Save(environmentRecord); err != nil {
		_ = os.RemoveAll(root)
		return Fixture{}, err
	}
	core := manager.New(store)
	if _, err := core.CreateNotice(decision.Notice{
		ID:       noticeID,
		Kind:     decision.KindPrivilegeStatus,
		Severity: decision.NoticeSeverityWarning,
		Status:   "degraded",
		Source:   decision.Source{Profile: "default", Backend: "native", Surface: "ui-e2e"},
		Payload:  map[string]any{"reason": "ui-e2e-visible-notice"},
		Preview:  decision.Preview{Summary: "UI E2E notice requires acknowledgement"},
		AuditRef: "audit:notice:" + noticeID,
	}); err != nil {
		_ = os.RemoveAll(root)
		return Fixture{}, err
	}
	d, err := daemon.Start(daemon.Options{
		Store:           store,
		TTL:             5 * time.Minute,
		CredentialGrace: time.Millisecond,
	})
	if err != nil {
		_ = os.RemoveAll(root)
		return Fixture{}, err
	}
	base := d.UIURL()
	if idx := indexFragment(base); idx >= 0 {
		base = base[:idx]
	}
	base = strings.TrimRight(base, "/")
	fixture := Fixture{
		StoreRoot:         root,
		UIURL:             d.UIURL(),
		BaseURL:           base,
		Token:             d.Token(),
		NoticeID:          noticeID,
		ControlKey:        workspaceFixtureKey,
		EnvironmentID:     environmentRecord.ID,
		MachineIdentityID: environmentRecord.MachineIdentityID,
		daemon:            d,
	}
	fixture.controlServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("X-Hideout-E2E-Key") != workspaceFixtureKey {
			http.Error(w, "fixture authorization failed", http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/workspace-views/"):
			state := strings.TrimPrefix(r.URL.Path, "/workspace-views/")
			if err := fixture.PublishWorkspaceViews(state); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/browser-console/live":
			if err := fixture.PublishBrowserConsoleLiveRecord(); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/browser-console/gap":
			if err := fixture.PublishBrowserConsoleSequenceGap(); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/browser-console/rotate":
			token, err := fixture.RotateBrowserConsoleCredential()
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"token": token})
		default:
			http.NotFound(w, r)
		}
	}))
	fixture.ControlURL = fixture.controlServer.URL
	return fixture, nil
}

type browserConsoleEvidenceSeeder interface {
	SeedBrowserConsoleEvidence(string) ([]byte, func() error, error)
}

type browserConsoleLivePublisher interface {
	PublishBrowserConsoleLiveRecord() error
	PublishBrowserConsoleSequenceGap() error
	RotateBrowserConsoleCredential() (string, error)
}

func (f *Fixture) SeedBrowserConsoleEvidence() error {
	if f == nil || f.daemon == nil {
		return errors.New("browser console fixture daemon is unavailable")
	}
	publisher, ok := any(f.daemon).(browserConsoleEvidenceSeeder)
	if !ok {
		return errors.New(
			"browser console evidence requires -tags=hideout_e2e",
		)
	}
	encoded, cleanup, err := publisher.SeedBrowserConsoleEvidence(
		f.EnvironmentID,
	)
	if err != nil {
		return err
	}
	var evidence BrowserConsoleEvidence
	if err := json.Unmarshal(encoded, &evidence); err != nil {
		_ = cleanup()
		return err
	}
	f.BrowserEvidence = evidence
	f.browserCleanup = cleanup
	return nil
}

func (f Fixture) PublishBrowserConsoleLiveRecord() error {
	publisher, ok := any(f.daemon).(browserConsoleLivePublisher)
	if !ok {
		return errors.New(
			"browser console live evidence requires -tags=hideout_e2e",
		)
	}
	return publisher.PublishBrowserConsoleLiveRecord()
}

func (f Fixture) PublishBrowserConsoleSequenceGap() error {
	publisher, ok := any(f.daemon).(browserConsoleLivePublisher)
	if !ok {
		return errors.New(
			"browser console gap evidence requires -tags=hideout_e2e",
		)
	}
	return publisher.PublishBrowserConsoleSequenceGap()
}

func (f Fixture) RotateBrowserConsoleCredential() (string, error) {
	publisher, ok := any(f.daemon).(browserConsoleLivePublisher)
	if !ok {
		return "", errors.New(
			"browser console credential evidence requires -tags=hideout_e2e",
		)
	}
	return publisher.RotateBrowserConsoleCredential()
}

func (f *Fixture) Close() {
	if f.controlServer != nil {
		f.controlServer.Close()
	}
	if f.browserCleanup != nil {
		_ = f.browserCleanup()
		f.browserCleanup = nil
	}
	if f.daemon != nil {
		_ = f.daemon.Stop(context.Background())
	}
	if f.StoreRoot != "" {
		_ = os.RemoveAll(f.StoreRoot)
	}
}

type workspaceViewEvidencePublisher interface {
	PublishWorkspaceViewEvidence([]manager.WorkspaceViewSnapshot)
}

// PublishWorkspaceViews drives the production event fan-out from the E2E-only
// daemon method. Normal builds intentionally do not implement the interface.
func (f Fixture) PublishWorkspaceViews(state string) error {
	publisher, ok := any(f.daemon).(workspaceViewEvidencePublisher)
	if !ok {
		return errors.New("workspace-view evidence publisher requires -tags=hideout_e2e")
	}
	views, err := f.workspaceViewSnapshots(state)
	if err != nil {
		return err
	}
	publisher.PublishWorkspaceViewEvidence(views)
	return nil
}

func (f Fixture) workspaceViewSnapshots(state string) ([]manager.WorkspaceViewSnapshot, error) {
	const (
		workspaceA = "wrk_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		workspaceB = "wrk_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	view := func(attachmentID, sessionID, workspaceID, label, privateRoot string, relation workspaceattach.RootRelationNotice) manager.WorkspaceViewSnapshot {
		return manager.WorkspaceViewSnapshot{
			Attachment: workspaceattach.AttachmentSummary{
				Schema: workspaceattach.AttachmentSummarySchema, AttachmentID: attachmentID,
				SessionID: sessionID, EnvironmentID: f.EnvironmentID, WorkspaceID: workspaceID,
				DisplayLabel: label, LogicalGuestRoot: workspaceattach.LogicalWorkspaceRoot,
				Transport: workspaceattach.SelectedTransport, State: workspaceattach.AttachmentReady,
				CreatedAt: time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC),
			},
			Profile: "default", Relations: []workspaceattach.RootRelationNotice{relation},
			CanonicalHostRoot: privateRoot, RootHandleIdentity: "private-root-handle-" + label,
			ProviderCredential: workspaceFixtureRootToken,
		}
	}
	relationA := workspaceattach.RootRelationNotice{
		Relation: workspaceattach.RootDisjoint, SelectedPosition: workspaceattach.RelationPositionPeer,
		WorkspaceID: workspaceA, OtherWorkspaceID: workspaceB,
	}
	relationB := workspaceattach.RootRelationNotice{
		Relation: workspaceattach.RootDisjoint, SelectedPosition: workspaceattach.RelationPositionPeer,
		WorkspaceID: workspaceB, OtherWorkspaceID: workspaceA,
	}
	first := view("att_ui_e2e_a", "ses_ui_e2e_a", workspaceA, "project-a [aaaaaaaa]", workspaceFixturePrivateA, relationA)
	second := view("att_ui_e2e_b", "ses_ui_e2e_b", workspaceB, "project-b [bbbbbbbb]", workspaceFixturePrivateB, relationB)
	switch state {
	case workspaceFixtureReady:
		return []manager.WorkspaceViewSnapshot{first, second}, nil
	case workspaceFixtureReleaseA:
		first.Attachment.State = workspaceattach.AttachmentReleased
		first.Attachment.CleanupProof = &workspaceattach.CleanupProof{
			Status:     workspaceattach.CleanupAbsent,
			ObservedAt: time.Date(2026, 7, 17, 0, 1, 0, 0, time.UTC),
		}
		first.Relations = nil
		return []manager.WorkspaceViewSnapshot{first}, nil
	default:
		return nil, errors.New("unknown workspace-view fixture state")
	}
}

// WorkspacePrivateSentinels returns values that must never cross the daemon's
// public workspace-view projection.
func WorkspacePrivateSentinels() []string {
	return []string{workspaceFixturePrivateA, workspaceFixturePrivateB, "private-root-handle", workspaceFixtureRootToken}
}

func (f Fixture) StopDaemon(ctx context.Context) error {
	if f.daemon == nil {
		return nil
	}
	return f.daemon.Stop(ctx)
}

func indexFragment(s string) int {
	for i := range s {
		if s[i] == '#' {
			return i
		}
	}
	return -1
}
