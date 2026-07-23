package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/hostapppack"
	"github.com/vibe-agi/hideout/internal/hostcap"
	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/profile"
	runsession "github.com/vibe-agi/hideout/internal/session"
)

func TestRunServicePreparePreservesCanonicalIntent(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	workspace := t.TempDir()
	p := profile.Default("work")
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	rule := hostfs.Rule{
		ID: "hfs_run_read", HostPath: t.TempDir(), Ops: []hostfs.Op{hostfs.OpRead},
		Scope: hostfs.ScopeRecursiveDir, Reason: "test request parity",
	}
	req := RunServiceRequest{
		Version: RunServiceRequestVersion, ProfileName: "work", Backend: "lima",
		NetworkMode: "direct", Workspace: workspace, GuestWorkspace: "/workspace",
		AllowWeakIsolation: true, Command: []string{"sh", "-c", "printf ok"},
		PublicEnv: map[string]string{"B": "two", "A": "one"}, AuditPath: "off",
		HostFSRun:                  hostfs.Config{Grants: []hostfs.Rule{rule}},
		DisableProfileHostFSGrants: true,
		Terminal:                   TerminalDescriptor{Mode: runsession.TerminalPTY, Rows: 31, Columns: 97, TERM: "xterm-256color"},
	}
	prepared, err := (RunService{Core: New(store)}).Prepare(req)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Review.Version != RunReviewVersion || prepared.Review.PlanVersion != RunPlanVersion {
		t.Fatalf("review identity=%+v", prepared.Review)
	}
	if prepared.Review.PlanDigest == "" || prepared.Review.Profile != "work" || prepared.Review.Workspace != workspace {
		t.Fatalf("review=%+v", prepared.Review)
	}
	if got := prepared.Plan.RuntimeProfile.Env.Public["A"]; got != "one" {
		t.Fatalf("public env A=%q", got)
	}
	if prepared.Plan.GuestWorkspace != "/workspace" || prepared.Plan.NetworkMode != "direct" {
		t.Fatalf("plan=%+v", prepared.Plan)
	}

	reordered := req
	reordered.PublicEnv = map[string]string{"A": "one", "B": "two"}
	again, err := (RunService{Core: New(store)}).Prepare(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if again.Review.PlanDigest != prepared.Review.PlanDigest {
		t.Fatalf("map insertion order changed digest: %s != %s", again.Review.PlanDigest, prepared.Review.PlanDigest)
	}

	changed := req
	changed.AuditPath = "audit.jsonl"
	withAudit, err := (RunService{Core: New(store)}).Prepare(changed)
	if err != nil {
		t.Fatal(err)
	}
	if withAudit.Review.PlanDigest == prepared.Review.PlanDigest {
		t.Fatal("audit intent was not bound into review digest")
	}
}

func TestRunServiceRejectsInvalidTerminalAndContradictoryRequest(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	workspace := t.TempDir()
	service := RunService{Core: New(store)}
	base := RunServiceRequest{
		Version: RunServiceRequestVersion, Workspace: workspace,
		Command: []string{"true"}, Terminal: TerminalDescriptor{Mode: runsession.TerminalNone},
	}
	tests := []struct {
		name string
		edit func(*RunServiceRequest)
	}{
		{name: "wrong version", edit: func(req *RunServiceRequest) { req.Version = "hideout.run-request/v999" }},
		{name: "pty no size", edit: func(req *RunServiceRequest) { req.Terminal = TerminalDescriptor{Mode: runsession.TerminalPTY} }},
		{name: "none with size", edit: func(req *RunServiceRequest) { req.Terminal.Rows = 10 }},
		{name: "bad term", edit: func(req *RunServiceRequest) {
			req.Terminal = TerminalDescriptor{Mode: runsession.TerminalPTY, Rows: 10, Columns: 20, TERM: "xterm;id"}
		}},
		{name: "ephemeral named", edit: func(req *RunServiceRequest) { req.Ephemeral = true; req.EnvironmentName = "shared" }},
		{name: "remove named", edit: func(req *RunServiceRequest) { req.RemoveEnvironment = true; req.EnvironmentName = "shared" }},
		{name: "nul argv", edit: func(req *RunServiceRequest) { req.Command = []string{"bad\x00arg"} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := base
			tc.edit(&req)
			if _, err := service.Prepare(req); err == nil {
				t.Fatal("Prepare succeeded")
			}
		})
	}
}

func TestRunServiceApplyRejectsProfileDriftAndStaleConfirmation(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	workspace := t.TempDir()
	p := profile.Default("work")
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	service := RunService{Core: New(store)}
	req := RunServiceRequest{
		Version: RunServiceRequestVersion, ProfileName: "work", Workspace: workspace,
		Command: []string{"true"}, Terminal: TerminalDescriptor{Mode: runsession.TerminalNone},
	}
	prepared, err := service.Prepare(req)
	if err != nil {
		t.Fatal(err)
	}

	badConfirmation := req
	badConfirmation.Confirmation = &RunConfirmation{
		PlanVersion: prepared.Review.PlanVersion, PlanDigest: "sha256:wrong", Accepted: true,
	}
	_, err = service.Apply(context.Background(), prepared, badConfirmation, RunServiceDependencies{Backend: &applyRunFakeBackend{}})
	if !errors.Is(err, ErrRunPlanStale) {
		t.Fatalf("stale confirmation error=%v", err)
	}

	p.Env.Public = map[string]string{"DRIFT": "changed"}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	_, err = service.Apply(context.Background(), prepared, req, RunServiceDependencies{Backend: &applyRunFakeBackend{}})
	if !errors.Is(err, ErrRunPlanStale) {
		t.Fatalf("profile drift error=%v", err)
	}
}

func TestRunServiceApplyRejectsProjectionCatalogDrift(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	workspace := t.TempDir()
	p := profile.Default("projection-drift")
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	core := New(store)
	service := RunService{Core: core}
	req := RunServiceRequest{
		Version: RunServiceRequestVersion, ProfileName: p.Name, Backend: "native",
		Workspace: workspace, Command: []string{"true"},
		Terminal: TerminalDescriptor{Mode: runsession.TerminalNone},
	}
	prepared, err := service.Prepare(req)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Plan.ProjectionCatalogDigest == "" {
		t.Fatal("review omitted the projection catalog digest")
	}
	if err := core.SetProjectionHostAppMode(p.Name, ProjectionHostAppModeTrusted); err != nil {
		t.Fatal(err)
	}
	current, err := service.Prepare(req)
	if err != nil {
		t.Fatal(err)
	}
	if current.Plan.ProjectionCatalogDigest == prepared.Plan.ProjectionCatalogDigest {
		t.Fatal("host-app catalog change did not change reviewed plan truth")
	}
	_, err = service.Apply(context.Background(), prepared, req, RunServiceDependencies{
		Backend: &applyRunFakeBackend{name: "native"},
	})
	if !errors.Is(err, ErrRunPlanStale) {
		t.Fatalf("projection catalog drift error=%v", err)
	}
}

func TestRunServiceApplyRejectsExternalProjectionCatalogDrift(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	workspace := t.TempDir()
	p := profile.Default("external-projection-drift")
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	core := Core{Store: store, HostAppPlatform: hostcap.PlatformDarwin}
	configureManagerHostAppIdentity(t, &core, root)
	service := RunService{Core: core}
	req := RunServiceRequest{
		Version: RunServiceRequestVersion, ProfileName: p.Name, Backend: "native",
		Workspace: workspace, Command: []string{"true"},
		Terminal: TerminalDescriptor{Mode: runsession.TerminalNone},
	}
	prepared, err := service.Prepare(req)
	if err != nil {
		t.Fatal(err)
	}

	packDir := writeManagerHostAppPack(t, root, "community.review-drift", "review-editor")
	addPlan, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "add", SourceKind: hostapppack.SourceLocal,
		SourcePath: packDir, ProfileName: p.Name,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyHostAppPack(addPlan); err != nil {
		t.Fatal(err)
	}
	current, err := service.Prepare(req)
	if err != nil {
		t.Fatal(err)
	}
	if current.Plan.ProjectionCatalogDigest == prepared.Plan.ProjectionCatalogDigest {
		t.Fatal("enabled external pack did not change reviewed projection truth")
	}
	fake := &applyRunFakeBackend{name: "native"}
	_, err = service.Apply(context.Background(), prepared, req, RunServiceDependencies{Backend: fake})
	if !errors.Is(err, ErrRunPlanStale) {
		t.Fatalf("external projection catalog drift error=%v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("stale external ownership reached backend authority: %v", fake.calls)
	}
}

func TestRunServiceConfirmationDenialFailsBeforeTargetAuthority(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	service := RunService{Core: New(store)}
	req := RunServiceRequest{
		Version: RunServiceRequestVersion, Workspace: t.TempDir(), Command: []string{"true"},
		Terminal: TerminalDescriptor{Mode: runsession.TerminalNone},
	}
	prepared, err := service.Prepare(req)
	if err != nil {
		t.Fatal(err)
	}
	req.Confirmation = &RunConfirmation{
		PlanVersion: prepared.Review.PlanVersion, PlanDigest: prepared.Review.PlanDigest, Accepted: false,
	}
	fake := &applyRunFakeBackend{}
	_, err = service.Apply(context.Background(), prepared, req, RunServiceDependencies{Backend: fake})
	if err == nil || err.Error() != "run confirmation was denied" {
		t.Fatalf("denial error=%v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("backend received authority calls: %v", fake.calls)
	}
}

func TestRunServiceEphemeralIdentityRemainsBoundAcrossRevalidation(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	service := RunService{Core: New(store)}
	req := RunServiceRequest{
		Version: RunServiceRequestVersion, Backend: "native", Workspace: t.TempDir(),
		Ephemeral: true, Command: []string{"true"},
		Terminal: TerminalDescriptor{Mode: runsession.TerminalNone},
	}
	prepared, err := service.Prepare(req)
	if err != nil {
		t.Fatal(err)
	}
	identityID := prepared.Plan.RuntimeProfile.Metadata["identityId"]
	if identityID == "" {
		t.Fatal("ephemeral plan has no identity")
	}
	fake := &applyRunFakeBackend{}
	if _, err := service.Apply(context.Background(), prepared, req, RunServiceDependencies{Backend: fake}); err != nil {
		t.Fatalf("Apply rejected its reviewed ephemeral identity: %v", err)
	}
	if fake.spec.Machine.Profile.Metadata["identityId"] != identityID {
		t.Fatalf("applied identity=%q want reviewed %q", fake.spec.Machine.Profile.Metadata["identityId"], identityID)
	}
}

func TestRunServiceRemoveAndEphemeralCleanIndependentEnvironmentAndIdentity(t *testing.T) {
	setFakeLinuxShim(t)
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	store := profile.Store{Root: root}
	journalStore := lifecycle.JournalStore{Root: root}
	coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store: journalStore, DaemonID: "daemon-disposable-ephemeral", IdleGrace: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	core := New(store)
	core.LifecycleDisposals = coordinator
	service := RunService{Core: core}
	request := RunServiceRequest{
		Version: RunServiceRequestVersion, ProfileName: "default", Backend: "lima",
		Workspace: t.TempDir(), Command: []string{"tool"},
		Ephemeral: true, RemoveEnvironment: true,
	}
	prepared, err := service.Prepare(request)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Review.RequiresConfirmation {
		request.Confirmation = &RunConfirmation{
			PlanVersion: prepared.Review.PlanVersion,
			PlanDigest:  prepared.Review.PlanDigest,
			Accepted:    true,
		}
	}
	identityObserved := false
	provider := &disposableLifecycleApplyBackend{lifecycleApplyBackend: &lifecycleApplyBackend{
		applyRunFakeBackend: &applyRunFakeBackend{
			name: "lima",
			runFunc: func(session *backend.Session) error {
				if _, err := os.Stat(filepath.Join(session.IdentityRoot, "identity.json")); err != nil {
					return fmt.Errorf("ephemeral identity was not materialized for target: %w", err)
				}
				identityObserved = true
				return nil
			},
		},
		journal: journalStore, bootID: "01234567-89ab-cdef-0123-456789abcdef",
	}}
	result, err := service.Apply(context.Background(), prepared, request, RunServiceDependencies{
		Backend: provider, Lifecycle: coordinator,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !identityObserved || provider.runSession.IdentityMode != "ephemeral" {
		t.Fatalf("ephemeral target identity was not used: session=%+v", provider.runSession)
	}
	wantIdentityRoot := filepath.Join(root, "sessions", result.SessionID, "identity")
	if provider.runSession.IdentityRoot != wantIdentityRoot {
		t.Fatalf("identity root=%q want %q", provider.runSession.IdentityRoot, wantIdentityRoot)
	}
	if _, err := os.Stat(wantIdentityRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ephemeral identity survived session cleanup: %v", err)
	}
	if result.EnvironmentDisposition != DisposableRecoveryRemoved ||
		result.EnvironmentID == "" || result.EnvironmentName == "" {
		t.Fatalf("disposable result identity/disposition changed: %+v", result)
	}
	if _, err := (environment.Store{Root: root}).Load(result.EnvironmentID); err == nil {
		t.Fatalf("disposable environment survived: %v", err)
	}
	if _, err := journalStore.Load(result.EnvironmentID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("disposable lifecycle identity survived: %v", err)
	}
}

func TestRunServiceResolvesPreviewIntentInsideManager(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	p := profile.Default("preview")
	p.EndpointExposure.HostToGuest = []profile.EndpointCandidate{{
		ID: "dev", Owner: OpenTargetPreviewOpen, Proto: "tcp", TargetAddress: "127.0.0.1:5173",
	}}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	prepared, err := (RunService{Core: New(store)}).Prepare(RunServiceRequest{
		Version: RunServiceRequestVersion, ProfileName: "preview", Workspace: t.TempDir(),
		Command: []string{"true"}, PreviewTargets: []string{"dev", "http://localhost:3000/app"},
		Terminal: TerminalDescriptor{Mode: runsession.TerminalNone},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Request.EndpointExposures) != 2 || len(prepared.Request.EndpointCandidates) != 1 {
		t.Fatalf("Manager did not resolve preview intent: %+v", prepared.Request)
	}
	if prepared.Request.EndpointCandidates[0].TargetAddress != "127.0.0.1:3000" {
		t.Fatalf("manual preview=%+v", prepared.Request.EndpointCandidates[0])
	}
}
