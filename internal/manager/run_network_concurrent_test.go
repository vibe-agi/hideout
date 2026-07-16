package manager

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
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/session"
)

func TestDirectNetworkPlansRemainSessionLocal(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{ProfileName: "default", Backend: "lima", Workspace: t.TempDir(), Command: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.EnsureRunInitialized(plan); err != nil {
		t.Fatal(err)
	}
	runEnvironment, err := core.SelectRunEnvironment(plan, RunEnvironmentOptions{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	firstSession, err := core.BeginRunSession(plan, runEnvironment, RunSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer core.CloseRunSession(firstSession)
	secondSession, err := core.BeginRunSession(plan, runEnvironment, RunSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer core.CloseRunSession(secondSession)
	first, err := core.PrepareRunNetwork(firstSession, RunNetworkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := core.PrepareRunNetwork(secondSession, RunNetworkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if first.EnvironmentService || second.EnvironmentService {
		t.Fatalf("direct network unexpectedly became an environment service: first=%+v second=%+v", first, second)
	}
	firstRoot := filepath.Dir(first.Plan.ManifestPath)
	secondRoot := filepath.Dir(second.Plan.ManifestPath)
	if firstRoot == secondRoot || firstRoot != firstSession.RuntimeSessionDir || secondRoot != secondSession.RuntimeSessionDir {
		t.Fatalf("direct network runtime is not session-local: first=%q second=%q", firstRoot, secondRoot)
	}
}

func TestEnvironmentNetworkServiceReusesMatchingFingerprintAndRejectsSecretDrift(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	p := profile.Default("privacy")
	p.Network.Mode = network.ModeTun2Socks
	p.Network.ProxySecretRef = "shared-proxy"
	p.Network.MediatedResolver = "1.1.1.1"
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{ProfileName: "privacy", Backend: "lima", Workspace: t.TempDir(), Command: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	runEnvironment, err := core.SelectRunEnvironment(plan, RunEnvironmentOptions{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	firstSession, err := core.BeginRunSession(plan, runEnvironment, RunSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer core.CloseRunSession(firstSession)
	resolver := network.EnvSecretResolver{Env: []string{network.SecretEnvName("shared-proxy") + "=socks5://user:one@127.0.0.1:1080"}}
	first, err := core.PrepareRunNetwork(firstSession, RunNetworkOptions{Resolver: resolver, Verified: true})
	if err != nil {
		t.Fatal(err)
	}
	if !first.EnvironmentService || !first.EnvironmentServiceStart || first.Plan.ConfigurationFingerprint == "" {
		t.Fatalf("first service=%+v", first)
	}
	state, err := network.LoadServiceState(first.ServiceStatePath)
	if err != nil || state.Status != network.ServiceStarting {
		t.Fatalf("starting state=%+v err=%v", state, err)
	}
	helper := []byte("environment-dns-helper")
	if err := os.WriteFile(filepath.Join(firstSession.RuntimeShimDir, "hideout-dns-stub"), helper, 0o700); err != nil {
		t.Fatal(err)
	}
	controller := &recordingNetworkServiceController{}
	bootID := "01234567-89ab-cdef-0123-456789abcdef"
	if err := core.StartRunNetworkService(context.Background(), firstSession, &first, controller, &backend.Session{ID: firstSession.Layout.ID, ExpectedBootID: bootID}, nil); err != nil {
		t.Fatal(err)
	}
	if controller.starts != 1 || first.EnvironmentServiceStart || !first.Plan.Verified {
		t.Fatalf("first owner did not establish the environment service: controller=%+v plan=%+v", controller, first)
	}
	ready, err := network.LoadServiceState(first.ServiceStatePath)
	if err != nil || ready.Status != network.ServiceReady || ready.BootID != bootID {
		t.Fatalf("ready state=%+v err=%v", ready, err)
	}
	copied, err := os.ReadFile(filepath.Join(first.ServiceDir, "hideout-dns-stub"))
	if err != nil || string(copied) != string(helper) {
		t.Fatalf("copied helper=%q err=%v", copied, err)
	}

	secondSession, err := core.BeginRunSession(plan, runEnvironment, RunSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer core.CloseRunSession(secondSession)
	second, err := core.PrepareRunNetwork(secondSession, RunNetworkOptions{Resolver: resolver})
	if err != nil {
		t.Fatal(err)
	}
	if second.EnvironmentServiceStart || second.Plan.Verified || !second.Plan.RuntimeVerify || second.Plan.ConfigurationFingerprint != first.Plan.ConfigurationFingerprint {
		t.Fatalf("matching reuse=%+v", second)
	}
	if err := core.StartRunNetworkService(context.Background(), secondSession, &second, controller, &backend.Session{ID: secondSession.Layout.ID, ExpectedBootID: bootID}, nil); err != nil {
		t.Fatal(err)
	}
	if controller.verifies != 1 || !second.Plan.Verified {
		t.Fatalf("reuse was not runtime verified: controller=%+v plan=%+v", controller, second.Plan)
	}
	driftResolver := network.EnvSecretResolver{Env: []string{network.SecretEnvName("shared-proxy") + "=socks5://user:two@127.0.0.1:1080"}}
	if _, err := core.PrepareRunNetwork(secondSession, RunNetworkOptions{Resolver: driftResolver}); err == nil || !strings.Contains(err.Error(), "conflicts with a live owner") {
		t.Fatalf("secret drift error=%v", err)
	}
	data, err := os.ReadFile(first.ServiceStatePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"user:one", "shared-proxy", "HIDEOUT_SECRET_"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("state leaked %q: %s", forbidden, data)
		}
	}
}

func TestEnvironmentNetworkServiceRejectsStaleBootAndFailedRuntimeHealth(t *testing.T) {
	core, _, runSession, runNetwork := preparedReadyEnvironmentNetworkService(t)
	controller := &recordingNetworkServiceController{}
	staleBoot := &backend.Session{ID: runSession.Layout.ID, ExpectedBootID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	err := core.StartRunNetworkService(context.Background(), runSession, &runNetwork, controller, staleBoot, nil)
	if err == nil || !strings.Contains(err.Error(), "belongs to guest boot") || controller.verifies != 0 {
		t.Fatalf("stale boot reuse err=%v controller=%+v", err, controller)
	}

	core, _, runSession, runNetwork = preparedReadyEnvironmentNetworkService(t)
	controller = &recordingNetworkServiceController{verifyErr: errors.New("tun process is gone")}
	current := &backend.Session{ID: runSession.Layout.ID, ExpectedBootID: "01234567-89ab-cdef-0123-456789abcdef"}
	err = core.StartRunNetworkService(context.Background(), runSession, &runNetwork, controller, current, nil)
	if err == nil || !strings.Contains(err.Error(), "verify environment network service") {
		t.Fatalf("health verification err=%v", err)
	}
	state, loadErr := network.LoadServiceState(runNetwork.ServiceStatePath)
	if loadErr != nil || state.Status != network.ServiceFailed || !strings.Contains(state.LastError, "tun process is gone") {
		t.Fatalf("failed state=%+v err=%v", state, loadErr)
	}
}

func preparedReadyEnvironmentNetworkService(t *testing.T) (Core, environment.Store, RunSession, RunNetwork) {
	t.Helper()
	core, store, record := concurrentLifecycleFixture(t)
	plan, err := core.PlanRun(RunPlanOptions{ProfileName: record.Profile, Backend: record.Backend, Workspace: record.Workspace, Command: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	runEnvironment := RunEnvironment{Active: true, Record: record, RuntimeDir: store.RuntimeDir(record.ID), PreserveInstance: true}
	runSession, err := core.BeginRunSession(plan, runEnvironment, RunSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = core.CloseRunSession(runSession) })
	servicePlan := network.Plan{Mode: network.ModeTun2Socks, ConfigurationFingerprint: strings.Repeat("b", 64)}
	serviceDir := store.RuntimeNetworkServiceDir(record.ID)
	state, err := network.BuildServiceState(record.ID, servicePlan, network.ServiceReady, "01234567-89ab-cdef-0123-456789abcdef", time.Now().UTC(), nil)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(serviceDir, "state.json")
	if err := network.WriteServiceState(statePath, state); err != nil {
		t.Fatal(err)
	}
	return core, store, runSession, RunNetwork{
		Plan: servicePlan, EnvironmentService: true, ServiceDir: serviceDir,
		GuestServiceDir: "/hideout/runtime/services/network", ServiceStatePath: statePath,
	}
}

func TestLastOwnerReconcilesCrashedSiblingBeforeNetworkServiceCleanup(t *testing.T) {
	core, store, record := concurrentLifecycleFixture(t)
	currentID := "ses_20260716T120000Z_0123456789abcdef"
	staleID := "ses_20260716T115900Z_fedcba9876543210"
	now := time.Now().UTC()
	owner, err := session.AcquireOwner(store.OwnerRoot(record.ID), session.OwnerRecord{
		Schema: session.ActiveSessionSchema, SessionID: currentID, EnvironmentID: record.ID,
		Profile: record.Profile, Backend: record.Backend, WorkspaceID: strings.Repeat("a", 64),
		State: session.OwnerStateRunning, TerminalMode: session.TerminalNone,
		StartedAt: now, UpdatedAt: now, CommandClass: "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{currentID, staleID} {
		if _, err := store.PrepareSessionRuntime(record.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	staleDir := filepath.Join(store.OwnerRoot(record.ID), staleID)
	if err := os.MkdirAll(staleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	staleRecord := session.OwnerRecord{
		Schema: session.ActiveSessionSchema, SessionID: staleID, EnvironmentID: record.ID,
		Profile: record.Profile, Backend: record.Backend, WorkspaceID: strings.Repeat("a", 64),
		State: session.OwnerStateRunning, TerminalMode: session.TerminalNone,
		StartedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute), CommandClass: "true",
	}
	data, err := json.Marshal(staleRecord)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staleDir, "session.json"), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staleDir, "owner.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	serviceDir := store.RuntimeNetworkServiceDir(record.ID)
	servicePlan := network.Plan{
		Mode: network.ModeTun2Socks, ConfigurationFingerprint: strings.Repeat("b", 64),
		GuestCleanupPath: "/hideout/runtime/services/network/cleanup.sh",
	}
	state, err := network.BuildServiceState(record.ID, servicePlan, network.ServiceReady, "01234567-89ab-cdef-0123-456789abcdef", now, nil)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(serviceDir, "state.json")
	if err := network.WriteServiceState(statePath, state); err != nil {
		t.Fatal(err)
	}
	controller := &recordingNetworkServiceController{}
	runNetwork := RunNetwork{
		Plan: servicePlan, EnvironmentService: true, ServiceDir: serviceDir,
		GuestServiceDir: "/hideout/runtime/services/network", ServiceStatePath: statePath,
	}
	runEnvironment := RunEnvironment{Active: true, Record: record, RuntimeDir: store.RuntimeDir(record.ID), PreserveInstance: true}
	var held *environment.Lock
	err = core.finishConcurrentRunEnvironment(context.Background(), &held, runEnvironment, owner, currentID, nil, func(ctx context.Context) error {
		return stopRunNetworkService(ctx, runNetwork, controller, &backend.Session{ID: currentID}, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	if controller.stops != 1 {
		t.Fatalf("network service stop calls=%d", controller.stops)
	}
	for _, id := range []string{currentID, staleID} {
		if _, err := os.Stat(store.RuntimeSessionDir(record.ID, id)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("runtime child %s remains: %v", id, err)
		}
	}
	owners, err := session.ListOwners(store.OwnerRoot(record.ID))
	if err != nil || len(owners) != 0 {
		t.Fatalf("owners after crash reconciliation=%+v err=%v", owners, err)
	}
}

func TestUnprovableSiblingBlocksSharedServiceAndActivationCleanup(t *testing.T) {
	core, store, record := concurrentLifecycleFixture(t)
	currentID := "ses_20260716T120000Z_0123456789abcdef"
	if _, err := store.PrepareSessionRuntime(record.ID, currentID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	owner, err := session.AcquireOwner(store.OwnerRoot(record.ID), session.OwnerRecord{
		Schema: session.ActiveSessionSchema, SessionID: currentID, EnvironmentID: record.ID,
		Profile: record.Profile, Backend: record.Backend, WorkspaceID: strings.Repeat("a", 64),
		State: session.OwnerStateRunning, TerminalMode: session.TerminalNone,
		StartedAt: now, UpdatedAt: now, CommandClass: "sleep",
	})
	if err != nil {
		t.Fatal(err)
	}

	unprovableID := "ses_20260716T120001Z_fedcba9876543210"
	unprovableDir := filepath.Join(store.OwnerRoot(record.ID), unprovableID)
	if err := os.MkdirAll(unprovableDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unprovableDir, "session.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unprovableDir, "owner.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	activationPath := filepath.Join(store.RuntimeDir(record.ID), "activation.json")
	if err := os.WriteFile(activationPath, []byte("retained-until-owner-set-is-proved\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runEnvironment := RunEnvironment{
		Active: true, Record: record, RuntimeDir: store.RuntimeDir(record.ID), PreserveInstance: true,
	}
	var held *environment.Lock
	serviceCleanupCalls := 0
	err = core.finishConcurrentRunEnvironment(
		context.Background(), &held, runEnvironment, owner, currentID, nil,
		func(context.Context) error {
			serviceCleanupCalls++
			return nil
		},
	)
	if !errors.Is(err, session.ErrOwnerUnprovable) {
		t.Fatalf("unprovable sibling cleanup error=%v", err)
	}
	if serviceCleanupCalls != 0 {
		t.Fatalf("shared service cleanup ran with unprovable sibling: %d", serviceCleanupCalls)
	}
	if _, err := os.Stat(activationPath); err != nil {
		t.Fatalf("activation receipt was removed with unprovable sibling: %v", err)
	}
}

func TestStopEnvironmentNetworkServiceCleansOnlyServiceDirectory(t *testing.T) {
	serviceDir := t.TempDir()
	plan := network.Plan{
		Mode: network.ModeTun2Socks, ConfigurationFingerprint: strings.Repeat("a", 64),
		GuestCleanupPath: "/hideout/runtime/services/network/cleanup.sh",
	}
	state, err := network.BuildServiceState("env_20260716t120000z0123456789abcdef", plan, network.ServiceReady, "01234567-89ab-cdef-0123-456789abcdef", time.Now().UTC(), nil)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(serviceDir, "state.json")
	if err := network.WriteServiceState(statePath, state); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(serviceDir, "sibling-proof"), []byte("service-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	controller := &recordingNetworkServiceController{}
	runNetwork := RunNetwork{
		Plan: plan, EnvironmentService: true, ServiceDir: serviceDir,
		GuestServiceDir: "/hideout/runtime/services/network", ServiceStatePath: statePath,
	}
	if err := stopRunNetworkService(context.Background(), runNetwork, controller, &backend.Session{ID: "ses_test"}, nil); err != nil {
		t.Fatal(err)
	}
	if controller.stops != 1 {
		t.Fatalf("stop calls=%d", controller.stops)
	}
	entries, err := os.ReadDir(serviceDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("service directory entries=%v err=%v", entries, err)
	}
}

func TestDiscardUnstartedEnvironmentNetworkServiceScrubsMaterial(t *testing.T) {
	core, store, record := concurrentLifecycleFixture(t)
	serviceDir := store.RuntimeNetworkServiceDir(record.ID)
	for name, content := range map[string]string{
		"state.json":       "starting\n",
		"proxy.url":        "socks5://secret@127.0.0.1:1080\n",
		"bootstrap.sh":     "secret bootstrap\n",
		"hideout-dns-stub": "helper\n",
	} {
		if err := os.WriteFile(filepath.Join(serviceDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := core.discardUnstartedEnvironmentNetworkService(record.ID); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(serviceDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("unstarted service material remains: %+v", entries)
	}
}

func TestCopyNetworkHelperRejectsOversizeWithoutLeavingPartialTarget(t *testing.T) {
	shimDir := t.TempDir()
	serviceDir := filepath.Join(t.TempDir(), "network")
	source := filepath.Join(shimDir, "hideout-dns-stub")
	file, err := os.OpenFile(source, os.O_CREATE|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxEnvironmentNetworkHelperBytes + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	err = copyNetworkHelper(shimDir, serviceDir)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversize helper error=%v", err)
	}
	if _, statErr := os.Stat(filepath.Join(serviceDir, "hideout-dns-stub")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("partial target stat error=%v", statErr)
	}
}

type recordingNetworkServiceController struct {
	starts    int
	verifies  int
	stops     int
	verifyErr error
}

func (c *recordingNetworkServiceController) VerifyEnvironmentNetwork(context.Context, *backend.Session, string, []string) error {
	c.verifies++
	return c.verifyErr
}

func (c *recordingNetworkServiceController) StartEnvironmentNetwork(context.Context, *backend.Session, string, string, []string) error {
	c.starts++
	return nil
}

func (c *recordingNetworkServiceController) StopEnvironmentNetwork(context.Context, *backend.Session, string, string, []string) error {
	c.stops++
	return nil
}
