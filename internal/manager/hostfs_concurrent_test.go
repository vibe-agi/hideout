package manager

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestConcurrentHostFSSessionsKeepReadsStagedWritesAndCleanupSeparate(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	configureManagerHostAppIdentity(t, &core, t.TempDir())
	workspace := t.TempDir()
	hostRoot := t.TempDir()
	shared := filepath.Join(hostRoot, "shared.txt")
	private := filepath.Join(hostRoot, "first-only.txt")
	if err := os.WriteFile(shared, []byte("lower"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(private, []byte("first-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default", Backend: "native", Workspace: workspace, Command: []string{"hold"},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstSession, err := core.BeginRunSession(plan, RunEnvironment{}, RunSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := core.CloseRunSession(firstSession); err != nil {
			t.Errorf("close first run session: %v", err)
		}
	}()
	secondSession, err := core.BeginRunSession(plan, RunEnvironment{}, RunSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := core.CloseRunSession(secondSession); err != nil {
			t.Errorf("close second run session: %v", err)
		}
	}()
	firstNetwork, err := core.PrepareRunNetwork(firstSession, RunNetworkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	secondNetwork, err := core.PrepareRunNetwork(secondSession, RunNetworkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sharedRules := []hostfs.Rule{
		{
			HostPath: shared, Scope: hostfs.ScopeExactFile, Ops: []hostfs.Op{hostfs.OpRead},
			Reason: "concurrent read fixture",
		},
		{
			HostPath: shared, Scope: hostfs.ScopeExactFile, Overlay: true,
			Ops:    []hostfs.Op{hostfs.OpWrite, hostfs.OpCreate, hostfs.OpAppend, hostfs.OpTruncate},
			Reason: "concurrent overlay fixture",
		},
	}
	firstRules := append([]hostfs.Rule(nil), sharedRules...)
	firstRules = append(firstRules, hostfs.Rule{
		HostPath: private, Scope: hostfs.ScopeExactFile, Ops: []hostfs.Op{hostfs.OpRead}, Reason: "first-only fixture",
	})
	first, err := core.StartRunDataPlane(context.Background(), firstSession, firstNetwork, RunDataPlaneOptions{
		HostFSRun: hostfs.Config{Grants: firstRules},
		Opener:    broker.NoopOpener{},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstClosed := false
	defer func() {
		if !firstClosed {
			_ = core.CloseRunDataPlane(first)
		}
	}()
	second, err := core.StartRunDataPlane(context.Background(), secondSession, secondNetwork, RunDataPlaneOptions{
		HostFSRun: hostfs.Config{Grants: sharedRules}, Opener: broker.NoopOpener{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := core.CloseRunDataPlane(second); err != nil {
			t.Errorf("close second run data plane: %v", err)
		}
	}()

	firstToken := envValueForManagerTest(first.Env, broker.EnvToken)
	secondToken := envValueForManagerTest(second.Env, broker.EnvToken)
	if firstToken == "" || secondToken == "" || firstToken == secondToken || first.BrokerGuestEndpoint == second.BrokerGuestEndpoint {
		t.Fatalf("session broker authority is not unique: first=%+v second=%+v", first.BrokerGuestEndpoint, second.BrokerGuestEndpoint)
	}
	if response := hostFSReadThroughBroker(t, first, firstSession.Layout.ID, firstToken, private); response.Status != "ok" {
		t.Fatalf("first session could not read its private grant: %+v", response)
	}
	if response := hostFSReadThroughBroker(t, second, secondSession.Layout.ID, secondToken, private); response.Status == "ok" {
		t.Fatalf("second session inherited first read grant: %+v", response)
	}
	if response := hostFSReadThroughBroker(t, second, secondSession.Layout.ID, firstToken, shared); response.Status == "ok" || response.Stderr != "broker authorization failed" {
		t.Fatalf("sibling token authorized against second broker: %+v", response)
	}

	requestID, err := broker.NewRequestID()
	if err != nil {
		t.Fatal(err)
	}
	staged := broker.ClientOpenEndpoint(context.Background(), first.BrokerGuestEndpoint, broker.Request{
		ID: requestID, SessionID: firstSession.Layout.ID, CapabilityToken: firstToken,
		Subject: "hostfs:daemon", Route: "host-broker", Action: "host.fs.write.replace",
		Args: map[string]any{"path": shared, "dataBase64": base64.StdEncoding.EncodeToString([]byte("first-staged"))},
	})
	if staged.Status != "ok" || staged.Data["staged"] != true {
		t.Fatalf("first staged write=%+v", staged)
	}
	if got := readHostFSService(t, first.Broker.HostFS, shared); got != "first-staged" {
		t.Fatalf("first overlay read=%q", got)
	}
	if got := readHostFSService(t, second.Broker.HostFS, shared); got != "lower" {
		t.Fatalf("second session observed sibling staged content=%q", got)
	}
	firstStatus, err := core.HostFSWriteStatus(HostFSWriteStatusRequest{Session: firstSession.Layout.ID})
	if err != nil || len(firstStatus.Pending) != 1 {
		t.Fatalf("first pending writes=%+v err=%v", firstStatus.Pending, err)
	}
	secondStatus, err := core.HostFSWriteStatus(HostFSWriteStatusRequest{Session: secondSession.Layout.ID})
	if err != nil || len(secondStatus.Pending) != 0 {
		t.Fatalf("second saw sibling pending write=%+v err=%v", secondStatus.Pending, err)
	}
	if host, err := os.ReadFile(shared); err != nil || string(host) != "lower" {
		t.Fatalf("staged write changed host lower=%q err=%v", host, err)
	}

	if err := core.CloseRunDataPlane(first); err != nil {
		t.Fatal(err)
	}
	firstClosed = true
	if got := readHostFSService(t, second.Broker.HostFS, shared); got != "lower" {
		t.Fatalf("closing first data plane changed sibling view=%q", got)
	}
	if response := hostFSReadThroughBroker(t, second, secondSession.Layout.ID, secondToken, shared); response.Status != "ok" {
		t.Fatalf("closing first data plane broke sibling broker: %+v", response)
	}
	if _, err := first.Broker.HostFS.Read(shared, 0, 0); err == nil {
		// The in-memory service object is not an authority endpoint after broker
		// close; this assertion only guards accidental continued broker access.
		response := hostFSReadThroughBroker(t, first, firstSession.Layout.ID, firstToken, shared)
		if response.Status == "ok" {
			t.Fatalf("closed first broker remained usable: %+v", response)
		}
	}
}

func hostFSReadThroughBroker(t *testing.T, dataPlane RunDataPlane, sessionID, token, path string) broker.Response {
	t.Helper()
	requestID, err := broker.NewRequestID()
	if err != nil {
		t.Fatal(err)
	}
	return broker.ClientOpenEndpoint(context.Background(), dataPlane.BrokerGuestEndpoint, broker.Request{
		ID: requestID, SessionID: sessionID, CapabilityToken: token,
		Subject: "hostfs:daemon", Route: "host-broker", Action: "host.fs.read",
		Args: map[string]any{"path": path, "offset": 0, "size": 0},
	})
}

func readHostFSService(t *testing.T, service *hostfs.Service, path string) string {
	t.Helper()
	if service == nil {
		t.Fatal("HostFS service is nil")
	}
	result, err := service.Read(path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return hostfs.ReadResultDataString(result)
}
