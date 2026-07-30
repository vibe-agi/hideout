package manager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/profile"
	runsession "github.com/vibe-agi/hideout/internal/session"
)

func TestConcurrentSessionEvidenceRedactsControlMaterialAndStaysSessionLocal(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	configureManagerHostAppIdentity(t, &core, t.TempDir())
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default", Backend: "native", Workspace: t.TempDir(), Command: []string{"hold"},
	})
	if err != nil {
		t.Fatal(err)
	}

	first := openConcurrentAuditSession(t, core, plan)
	defer func() {
		if _, err := core.CloseRunSession(first); err != nil {
			t.Errorf("close first run session: %v", err)
		}
	}()
	second := openConcurrentAuditSession(t, core, plan)
	defer func() {
		if _, err := core.CloseRunSession(second); err != nil {
			t.Errorf("close second run session: %v", err)
		}
	}()
	firstNetwork, err := core.PrepareRunNetwork(first, RunNetworkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	secondNetwork, err := core.PrepareRunNetwork(second, RunNetworkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	firstPlane, err := core.StartRunDataPlane(context.Background(), first, firstNetwork, RunDataPlaneOptions{Opener: broker.NoopOpener{}})
	if err != nil {
		t.Fatal(err)
	}
	firstPlaneClosed := false
	defer func() {
		if !firstPlaneClosed {
			if err := core.CloseRunDataPlane(firstPlane); err != nil {
				t.Errorf("close first run data plane: %v", err)
			}
		}
	}()
	secondPlane, err := core.StartRunDataPlane(context.Background(), second, secondNetwork, RunDataPlaneOptions{Opener: broker.NoopOpener{}})
	if err != nil {
		t.Fatal(err)
	}
	secondPlaneClosed := false
	defer func() {
		if !secondPlaneClosed {
			if err := core.CloseRunDataPlane(secondPlane); err != nil {
				t.Errorf("close second run data plane: %v", err)
			}
		}
	}()

	firstToken := envValueForManagerTest(firstPlane.Env, broker.EnvToken)
	secondToken := envValueForManagerTest(secondPlane.Env, broker.EnvToken)
	claimToken := "claim_0123456789abcdef0123456789abcdef"
	proxySecret := "socks5://operator:private@127.0.0.1:1080"
	machineID := "0123456789abcdef0123456789abcdef"
	if firstToken == "" || secondToken == "" || firstToken == secondToken {
		t.Fatalf("invalid session tokens: first=%q second=%q", firstToken, secondToken)
	}

	for _, fixture := range []struct {
		session     RunSession
		plane       RunDataPlane
		planeClosed *bool
		token       string
	}{
		{session: first, plane: firstPlane, planeClosed: &firstPlaneClosed, token: firstToken},
		{session: second, plane: secondPlane, planeClosed: &secondPlaneClosed, token: secondToken},
	} {
		if err := fixture.session.Audit.Emit(audit.Event{
			Session: fixture.session.Layout.ID, Profile: "default", Backend: "native",
			Action: "concurrency.redaction.probe", Decision: "audit-only",
			Details: map[string]any{
				"brokerToken":      fixture.token,
				"claimToken":       claimToken,
				"machineId":        machineID,
				"HIDEOUT_SECRET_X": proxySecret,
				"brokerTransport":  fixture.plane.BrokerGuestEndpoint.Network,
				"brokerEndpoint":   "present",
			},
		}); err != nil {
			t.Fatal(err)
		}
		if err := core.CloseRunDataPlane(fixture.plane); err != nil {
			t.Fatal(err)
		}
		*fixture.planeClosed = true
		if err := fixture.session.Audit.Close(); err != nil {
			t.Fatal(err)
		}
		fixture.session.Audit = audit.NewDiscard()
	}

	firstBody := readConcurrentAudit(t, first.AuditPath)
	secondBody := readConcurrentAudit(t, second.AuditPath)
	for label, body := range map[string]string{"first": firstBody, "second": secondBody} {
		for _, forbidden := range []string{
			firstToken, secondToken, claimToken, proxySecret, machineID,
			first.Layout.BrokerSock, second.Layout.BrokerSock,
			first.Layout.HostFSReadOwnerLock, second.Layout.HostFSReadOwnerLock,
		} {
			if forbidden != "" && strings.Contains(body, forbidden) {
				t.Fatalf("%s audit leaked control material %q: %s", label, forbidden, body)
			}
		}
		if !strings.Contains(body, `"brokerEndpoint":"present"`) || !strings.Contains(body, `"brokerTransport":"unix"`) {
			t.Fatalf("%s audit lost bounded transport posture: %s", label, body)
		}
	}
	if strings.Contains(firstBody, second.Layout.ID) || strings.Contains(secondBody, first.Layout.ID) {
		t.Fatalf("sibling audit identity leaked: first=%s second=%s", firstBody, secondBody)
	}
}

func TestActiveSessionSummaryRedactsRawOwnerPathsAndCleanupMaterial(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	environmentStore := environment.Store{Root: store.Root}
	record, err := environmentStore.Create(environment.Spec{
		Name: "redaction", ImageRef: environment.BuiltinBaseImage, Profile: "default", Backend: "native",
		Mode: environment.ModeWorkspaceBound, MachineIdentityID: testEnvironmentMachineIdentityID(), BootConfigurationID: testEnvironmentBootConfigurationID(), BoundWorkspace: t.TempDir(), BoundGuestRoot: "/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := environmentStore.PrepareRuntimeRoot(record.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	owner, err := runsession.AcquireOwner(environmentStore.OwnerRoot(record.ID), runsession.OwnerRecord{
		Schema: runsession.ActiveSessionSchema, SessionID: "ses_20260716T120000Z_00112233445566778899",
		EnvironmentID: record.ID, Profile: "default", Backend: "native",
		WorkspaceID: "wrk_" + strings.Repeat("a", 64), SessionSnapshotID: testSessionSnapshotID(), State: runsession.OwnerStatePreparing,
		TerminalMode: runsession.TerminalNone, StartedAt: now, UpdatedAt: now, CommandClass: "hold",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := owner.Release(); err != nil {
			t.Errorf("release session owner: %v", err)
		}
	}()
	rawRuntime := environmentStore.RuntimeSessionDir(record.ID, owner.Record().SessionID)
	rawLock := filepath.Join(environmentStore.OwnerRoot(record.ID), owner.Record().SessionID, "owner.lock")
	cleanupMessage := "failed at " + rawRuntime + " lock=" + rawLock + " token=cap_0123456789abcdef0123456789abcdef"
	if err := owner.Update(runsession.OwnerStateFailed, cleanupMessage); err != nil {
		t.Fatal(err)
	}

	summaries, err := New(store).ActiveSessionSummaries()
	if err != nil || len(summaries) != 1 {
		t.Fatalf("summaries=%+v err=%v", summaries, err)
	}
	encoded, err := json.Marshal(summaries[0])
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, forbidden := range []string{rawRuntime, rawLock, "cap_0123456789abcdef0123456789abcdef", store.Root} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("active summary leaked %q: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, "[environment-state]") || !strings.Contains(text, "REDACTED") {
		t.Fatalf("active summary lost bounded recovery context: %s", text)
	}
}

func openConcurrentAuditSession(t *testing.T, core Core, plan RunPlan) RunSession {
	t.Helper()
	runSession, err := core.BeginRunSession(plan, RunEnvironment{}, RunSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	runSession, err = core.OpenRunSessionAudit(runSession, RunAuditOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return runSession
}

func readConcurrentAudit(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
