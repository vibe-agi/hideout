package manager

import (
	"context"
	"errors"
	"testing"

	"github.com/vibe-agi/hideout/internal/hostfs"
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
	if fake.spec.Profile.Metadata["identityId"] != identityID {
		t.Fatalf("applied identity=%q want reviewed %q", fake.spec.Profile.Metadata["identityId"], identityID)
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
