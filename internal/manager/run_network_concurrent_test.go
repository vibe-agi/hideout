package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/backend/lima"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/session"
)

func TestDirectNetworkUsesOneReusableEnvironmentService(t *testing.T) {
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
	if !first.EnvironmentService || !first.EnvironmentServiceStart || first.EnvironmentServiceAction != networkServiceStart {
		t.Fatalf("first direct service=%+v", first)
	}
	controller := &recordingNetworkServiceController{}
	bootID := "01234567-89ab-cdef-0123-456789abcdef"
	if err := core.StartRunNetworkService(context.Background(), firstSession, &first, controller, &backend.Session{ID: firstSession.Layout.ID, ExpectedBootID: bootID}, nil); err != nil {
		t.Fatal(err)
	}
	second, err := core.PrepareRunNetwork(secondSession, RunNetworkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !second.EnvironmentService || second.EnvironmentServiceStart || second.EnvironmentServiceAction != networkServiceReuse {
		t.Fatalf("second direct service=%+v", second)
	}
	if first.ServiceDir != second.ServiceDir || first.Plan.GatewayID != second.Plan.GatewayID {
		t.Fatalf("direct service identity drifted: first=%+v second=%+v", first, second)
	}
	if err := core.StartRunNetworkService(context.Background(), secondSession, &second, controller, &backend.Session{ID: secondSession.Layout.ID, ExpectedBootID: bootID}, nil); err != nil {
		t.Fatal(err)
	}
	if controller.directVerifies != 2 || controller.starts != 0 || controller.stops != 0 {
		t.Fatalf("direct service verification=%+v", controller)
	}
}

func TestEnvironmentNetworkServiceReusesMatchingFingerprintAndSwitchesProxyOnline(t *testing.T) {
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
	wantFirstSecret := filepath.Join(firstSession.RuntimeSessionDir, "network", "proxy.url")
	wantFirstGuestSecret := lima.GuestRuntimeDir + "/sessions/" + firstSession.Layout.ID + "/network/proxy.url"
	if first.Plan.ProxySecretPath != wantFirstSecret || first.Plan.GuestProxySecretPath != wantFirstGuestSecret {
		t.Fatalf("first secret authority host=%q guest=%q want=%q %q", first.Plan.ProxySecretPath, first.Plan.GuestProxySecretPath, wantFirstSecret, wantFirstGuestSecret)
	}
	for _, artifact := range []string{first.Plan.ManifestPath, first.Plan.BootstrapPath, first.Plan.CleanupPath} {
		rel, relErr := filepath.Rel(first.ServiceDir, artifact)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Fatalf("environment service artifact escaped shared root: path=%q rel=%q err=%v", artifact, rel, relErr)
		}
	}
	assertDirectoryExcludesRawNetworkSecret(t, first.ServiceDir, "socks5://user:one@127.0.0.1:1080")
	state, err := network.LoadServiceState(first.ServiceStatePath)
	if err != nil || state.Status != network.ServiceStarting {
		t.Fatalf("starting state=%+v err=%v", state, err)
	}
	helper := []byte("environment-dns-helper")
	if err := os.WriteFile(filepath.Join(firstSession.RuntimeShimDir, "hideout-dns-stub"), helper, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(firstSession.RuntimeShimDir, "tun2socks"), []byte("tun2socks-bin"), 0o700); err != nil {
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
	if _, err := os.Lstat(wantFirstSecret); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("consumed session secret remains after service start: %v", err)
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
	wantSecondSecret := filepath.Join(secondSession.RuntimeSessionDir, "network", "proxy.url")
	if second.Plan.ProxySecretPath != wantSecondSecret || second.Plan.ProxySecretPath == first.Plan.ProxySecretPath {
		t.Fatalf("reuse secret authority=%q want distinct session path %q", second.Plan.ProxySecretPath, wantSecondSecret)
	}
	if err := core.StartRunNetworkService(context.Background(), secondSession, &second, controller, &backend.Session{ID: secondSession.Layout.ID, ExpectedBootID: bootID}, nil); err != nil {
		t.Fatal(err)
	}
	if controller.verifies != 1 || !second.Plan.Verified {
		t.Fatalf("reuse was not runtime verified: controller=%+v plan=%+v", controller, second.Plan)
	}
	// The reuse path materializes the bootstrap secret so a failed current-boot
	// verification can self-heal, but a successful verification must leave no
	// consumed session secret behind.
	if _, err := os.Lstat(wantSecondSecret); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reuse left a consumed session secret behind: %v", err)
	}
	driftResolver := network.EnvSecretResolver{Env: []string{network.SecretEnvName("shared-proxy") + "=socks5://user:two@127.0.0.1:1080"}}
	drift, err := core.PrepareRunNetwork(secondSession, RunNetworkOptions{Resolver: driftResolver})
	if err != nil {
		t.Fatal(err)
	}
	if drift.EnvironmentServiceAction != networkServiceGateway || drift.EnvironmentServiceStart {
		t.Fatalf("proxy switch plan=%+v", drift)
	}
	if err := core.StartRunNetworkService(context.Background(), secondSession, &drift, controller, &backend.Session{ID: secondSession.Layout.ID, ExpectedBootID: bootID}, nil); err != nil {
		t.Fatal(err)
	}
	if controller.starts != 1 || controller.stops != 0 || controller.verifies != 2 {
		t.Fatalf("proxy switch restarted guest service: controller=%+v", controller)
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

func TestEnvironmentNetworkServiceSwitchesDirectProxyAndDNSWithoutRecreate(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	p := profile.Default("switchable")
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	var err error
	p, err = store.Load(p.Name)
	if err != nil {
		t.Fatal(err)
	}
	core := New(store)
	workspace := t.TempDir()
	controller := &recordingNetworkServiceController{}
	bootID := "01234567-89ab-cdef-0123-456789abcdef"
	begin := func() (RunSession, error) {
		plan, err := core.PlanRun(RunPlanOptions{ProfileName: p.Name, Backend: "lima", Workspace: workspace, Command: []string{"true"}})
		if err != nil {
			return RunSession{}, err
		}
		runEnvironment, err := core.SelectRunEnvironment(plan, RunEnvironmentOptions{Create: true})
		if err != nil {
			return RunSession{}, err
		}
		return core.BeginRunSession(plan, runEnvironment, RunSessionOptions{})
	}
	apply := func(session RunSession, resolver network.SecretResolver) RunNetwork {
		t.Helper()
		runNetwork, err := core.PrepareRunNetwork(session, RunNetworkOptions{Resolver: resolver})
		if err != nil {
			t.Fatal(err)
		}
		if runNetwork.Plan.Mode == network.ModeTun2Socks {
			if err := os.WriteFile(filepath.Join(session.RuntimeShimDir, "hideout-dns-stub"), []byte("helper"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(session.RuntimeShimDir, "tun2socks"), []byte("tun2socks-bin"), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := core.StartRunNetworkService(context.Background(), session, &runNetwork, controller, &backend.Session{ID: session.Layout.ID, ExpectedBootID: bootID}, nil); err != nil {
			t.Fatal(err)
		}
		return runNetwork
	}

	directSession, err := begin()
	if err != nil {
		t.Fatal(err)
	}
	defer core.CloseRunSession(directSession)
	direct := apply(directSession, nil)
	if direct.EnvironmentServiceAction != networkServiceStart {
		t.Fatalf("initial direct action=%q", direct.EnvironmentServiceAction)
	}
	environmentID := directSession.Environment.Record.ID
	machineID := directSession.Environment.Record.MachineIdentityID
	bootConfigurationID := directSession.Environment.Record.BootConfigurationID

	p.Network.Mode = network.ModeTun2Socks
	p.Network.ProxySecretRef = "switch-proxy"
	p.Network.MediatedResolver = "1.1.1.1"
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	proxyResolver := network.EnvSecretResolver{Env: []string{network.SecretEnvName("switch-proxy") + "=socks5://user:one@127.0.0.1:1080"}}
	proxySession, err := begin()
	if err != nil {
		t.Fatal(err)
	}
	defer core.CloseRunSession(proxySession)
	proxy := apply(proxySession, proxyResolver)
	if proxySession.Environment.Record.ID != environmentID || proxy.EnvironmentServiceAction != networkServiceEnableProxy || controller.starts != 1 {
		t.Fatalf("direct-to-proxy recreated or missed live enable: env=%q action=%q controller=%+v", proxySession.Environment.Record.ID, proxy.EnvironmentServiceAction, controller)
	}

	p.Network.MediatedResolver = "9.9.9.9"
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	dnsSession, err := begin()
	if err != nil {
		t.Fatal(err)
	}
	defer core.CloseRunSession(dnsSession)
	dns := apply(dnsSession, proxyResolver)
	if dnsSession.Environment.Record.ID != environmentID || dns.EnvironmentServiceAction != networkServiceDNS || len(controller.dnsSwitches) != 1 || controller.dnsSwitches[0] != [2]string{"1.1.1.1", "9.9.9.9"} {
		t.Fatalf("DNS switch=%+v controller=%+v", dns, controller)
	}

	p.Network.Mode = network.ModeDirect
	p.Network.ProxySecretRef = ""
	p.Network.MediatedResolver = ""
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	finalSession, err := begin()
	if err != nil {
		t.Fatal(err)
	}
	defer core.CloseRunSession(finalSession)
	final := apply(finalSession, nil)
	if finalSession.Environment.Record.ID != environmentID || final.EnvironmentServiceAction != networkServiceDisableProxy || controller.stops != 1 || controller.directVerifies < 2 {
		t.Fatalf("proxy-to-direct recreated or missed live disable: env=%q action=%q controller=%+v", finalSession.Environment.Record.ID, final.EnvironmentServiceAction, controller)
	}
	if finalSession.Environment.Record.MachineIdentityID != machineID || finalSession.Environment.Record.BootConfigurationID != bootConfigurationID {
		t.Fatalf("network switch changed machine/boot identity: before=%s/%s after=%s/%s", machineID, bootConfigurationID, finalSession.Environment.Record.MachineIdentityID, finalSession.Environment.Record.BootConfigurationID)
	}
	state, err := network.LoadServiceState(final.ServiceStatePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.ConfigurationID != finalSession.Environment.Configuration.Layers.ServicesID || state.Status != network.ServiceReady || state.Mode != network.ModeDirect {
		t.Fatalf("final service state=%+v desired=%s", state, finalSession.Environment.Configuration.Layers.ServicesID)
	}
	summaries := environmentSummaries(store.Root)
	if len(summaries) != 1 || summaries[0].ServiceConfigurationID != state.ConfigurationID || summaries[0].ServiceFingerprint != state.ConfigurationFingerprint || summaries[0].ServiceStatus != string(network.ServiceReady) {
		t.Fatalf("environment service summary=%+v state=%+v", summaries, state)
	}
}

func TestEnvironmentNetworkPostureSwitchWaitsForSiblingSessionWithoutRecreate(t *testing.T) {
	core, store, runSession, _ := preparedReadyEnvironmentNetworkService(t)
	configuration, err := RuntimeConfigurationForProfile(runSession.Plan.RuntimeProfile, runSession.Plan.Backend, runSession.Environment.Record.Mode)
	if err != nil {
		t.Fatal(err)
	}
	runSession.Environment.Configuration = configuration
	now := time.Now().UTC()
	sibling, err := session.AcquireOwner(store.OwnerRoot(runSession.Environment.Record.ID), session.OwnerRecord{
		Schema: session.ActiveSessionSchema, SessionID: "ses_20260716T120001Z_fedcba9876543210", EnvironmentID: runSession.Environment.Record.ID,
		Profile: runSession.Plan.ProfileName, Backend: runSession.Plan.Backend, WorkspaceID: "wrk_" + strings.Repeat("b", 64), SessionSnapshotID: testSessionSnapshotID(),
		State: session.OwnerStateRunning, TerminalMode: session.TerminalNone,
		StartedAt: now, UpdatedAt: now, CommandClass: "sleep",
	})
	if err != nil {
		t.Fatal(err)
	}

	blocked, err := core.PrepareRunNetwork(runSession, RunNetworkOptions{})
	if err == nil || !strings.Contains(err.Error(), "requires no active sibling sessions") {
		t.Fatalf("posture switch with a sibling session err=%v plan=%+v", err, blocked)
	}
	if blocked.EnvironmentServiceAction != networkServiceDisableProxy {
		t.Fatalf("blocked action=%q", blocked.EnvironmentServiceAction)
	}
	if _, statErr := os.Stat(blocked.PreviousPlan.CleanupPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("blocked transition materialized shared cleanup state: %v", statErr)
	}
	if err := sibling.Close(); err != nil {
		t.Fatal(err)
	}

	retry, err := core.PrepareRunNetwork(runSession, RunNetworkOptions{})
	if err != nil {
		t.Fatalf("posture switch did not become attachable after sibling exit: %v", err)
	}
	if retry.EnvironmentServiceAction != networkServiceDisableProxy || retry.GatewayChange == nil {
		t.Fatalf("retry=%+v", retry)
	}
	if err := retry.GatewayChange.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func assertDirectoryExcludesRawNetworkSecret(t *testing.T, root, secret string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Name() == "proxy.url" {
			return fmt.Errorf("shared service contains session secret file %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), secret) || strings.Contains(string(data), network.SecretEnvName("shared-proxy")) {
			return fmt.Errorf("shared service artifact %s contains raw session credential material", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func writeNetworkShimHelpers(t *testing.T, shimDir string) {
	t.Helper()
	if err := os.MkdirAll(shimDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tun2socks", "hideout-dns-stub"} {
		if err := os.WriteFile(filepath.Join(shimDir, name), []byte(name+"-bin"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEnvironmentNetworkServiceSelfHealsStaleReuse(t *testing.T) {
	// A reuse whose current-boot verification fails — the guest rebooted or was
	// recreated, or a prior teardown was unclean — self-heals by re-establishing
	// the privacy network fresh instead of forcing the operator to stop the
	// environment. The idempotent bootstrap reconciles any stale guest remnant
	// before the fresh setup, so the run ends on a verified private network.
	core, _, runSession, runNetwork := preparedReadyEnvironmentNetworkService(t)
	writeNetworkShimHelpers(t, runSession.RuntimeShimDir)
	controller := &recordingNetworkServiceController{verifyErr: errors.New("tun process is gone")}
	current := &backend.Session{ID: runSession.Layout.ID, ExpectedBootID: "01234567-89ab-cdef-0123-456789abcdef"}
	if err := core.StartRunNetworkService(context.Background(), runSession, &runNetwork, controller, current, nil); err != nil {
		t.Fatalf("stale reuse did not self-heal: %v", err)
	}
	// A genuinely-broken network fails all verify retries before the self-heal
	// re-establishes it once; the retries exist so a transient flake would NOT
	// reach this destructive path.
	if controller.verifies != 3 || controller.starts != 1 {
		t.Fatalf("reuse self-heal did not re-establish the network after verify retries: controller=%+v", controller)
	}
	state, loadErr := network.LoadServiceState(runNetwork.ServiceStatePath)
	if loadErr != nil || state.Status != network.ServiceReady {
		t.Fatalf("self-healed state=%+v err=%v", state, loadErr)
	}

	// When the re-establishment itself fails, the run fails closed and records
	// the failure rather than proceeding on an unverified network.
	core, _, runSession, runNetwork = preparedReadyEnvironmentNetworkService(t)
	writeNetworkShimHelpers(t, runSession.RuntimeShimDir)
	controller = &recordingNetworkServiceController{
		verifyErr: errors.New("tun process is gone"),
		startErr:  errors.New("setup identity rejected network start"),
	}
	if err := core.StartRunNetworkService(context.Background(), runSession, &runNetwork, controller, current, nil); err == nil ||
		!strings.Contains(err.Error(), "setup identity rejected network start") {
		t.Fatalf("failed re-establishment err=%v", err)
	}
	state, loadErr = network.LoadServiceState(runNetwork.ServiceStatePath)
	if loadErr != nil || state.Status != network.ServiceFailed {
		t.Fatalf("failed re-establish state=%+v err=%v", state, loadErr)
	}
}

func TestEnvironmentNetworkServiceRejectsStaleBootForNonReuseTransition(t *testing.T) {
	// Non-reuse transitions (DNS switch, proxy restart, posture changes) mutate
	// the previously established guest network. If the guest booted anew that
	// network is gone, so they must fail closed rather than mutate nothing.
	core, _, runSession, runNetwork := preparedReadyEnvironmentNetworkService(t)
	runNetwork.EnvironmentServiceAction = networkServiceRestartProxy
	runNetwork.EnvironmentServiceStart = true
	controller := &recordingNetworkServiceController{}
	staleBoot := &backend.Session{ID: runSession.Layout.ID, ExpectedBootID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	err := core.StartRunNetworkService(context.Background(), runSession, &runNetwork, controller, staleBoot, nil)
	if err == nil || !strings.Contains(err.Error(), "belongs to guest boot") || controller.verifies != 0 || controller.starts != 0 {
		t.Fatalf("stale boot non-reuse transition err=%v controller=%+v", err, controller)
	}
}

func TestEnvironmentNetworkDNSFailureRestoresProvedPreviousGeneration(t *testing.T) {
	core, _, runSession, runNetwork := preparedReadyEnvironmentNetworkService(t)
	previous := *runNetwork.PreviousServiceState
	runNetwork.Plan.MediatedResolver = "9.9.9.9"
	runNetwork.Plan.ConfigurationFingerprint = strings.Repeat("c", 64)
	runNetwork.EnvironmentServiceAction = networkServiceDNS
	runNetwork.EnvironmentServiceStart = true
	if err := os.WriteFile(filepath.Join(runSession.RuntimeShimDir, "hideout-dns-stub"), []byte("helper"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runSession.RuntimeShimDir, "tun2socks"), []byte("tun2socks-bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	controller := &recordingNetworkServiceController{dnsErr: backend.EnvironmentServiceReconfigureError{
		Operation: "reconfigure environment DNS", RollbackProved: true, Cause: errors.New("replacement failed"),
	}}
	err := core.StartRunNetworkService(
		context.Background(), runSession, &runNetwork, controller,
		&backend.Session{ID: runSession.Layout.ID, ExpectedBootID: previous.BootID}, nil,
	)
	if err == nil || !strings.Contains(err.Error(), "replacement failed") {
		t.Fatalf("DNS switch error=%v", err)
	}
	state, loadErr := network.LoadServiceState(runNetwork.ServiceStatePath)
	if loadErr != nil || state.Status != network.ServiceReady || state.ConfigurationFingerprint != previous.ConfigurationFingerprint || state.Resolver != previous.Resolver {
		t.Fatalf("restored state=%+v err=%v", state, loadErr)
	}
}

func TestEnvironmentNetworkServiceStartFailureScrubsSessionSecret(t *testing.T) {
	core, runSession, runNetwork := preparedStartingEnvironmentNetworkService(t)
	controller := &recordingNetworkServiceController{startErr: errors.New("setup identity rejected network start")}
	err := core.StartRunNetworkService(
		context.Background(), runSession, &runNetwork, controller,
		&backend.Session{ID: runSession.Layout.ID, ExpectedBootID: "01234567-89ab-cdef-0123-456789abcdef"}, nil,
	)
	if err == nil || !strings.Contains(err.Error(), "setup identity rejected") {
		t.Fatalf("network start error=%v", err)
	}
	if _, err := os.Lstat(runNetwork.Plan.ProxySecretPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed network start retained session secret: %v", err)
	}
	state, err := network.LoadServiceState(runNetwork.ServiceStatePath)
	if err != nil || state.Status != network.ServiceFailed {
		t.Fatalf("failed network state=%+v err=%v", state, err)
	}
}

func TestEnvironmentNetworkServiceRefusesReadyWithUnremovableSessionSecret(t *testing.T) {
	core, runSession, runNetwork := preparedStartingEnvironmentNetworkService(t)
	if err := os.Remove(runNetwork.Plan.ProxySecretPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(runNetwork.Plan.ProxySecretPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runNetwork.Plan.ProxySecretPath, "retained"), []byte("not-a-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := core.StartRunNetworkService(
		context.Background(), runSession, &runNetwork, &recordingNetworkServiceController{},
		&backend.Session{ID: runSession.Layout.ID, ExpectedBootID: "01234567-89ab-cdef-0123-456789abcdef"}, nil,
	)
	if err == nil || !strings.Contains(err.Error(), "remove consumed session network secret") {
		t.Fatalf("unremovable session secret error=%v", err)
	}
	if runNetwork.Plan.Verified || !runNetwork.EnvironmentServiceStart {
		t.Fatalf("service became ready with retained secret: %+v", runNetwork)
	}
	state, stateErr := network.LoadServiceState(runNetwork.ServiceStatePath)
	if stateErr != nil || state.Status != network.ServiceFailed {
		t.Fatalf("retained-secret state=%+v err=%v", state, stateErr)
	}
}

func preparedStartingEnvironmentNetworkService(t *testing.T) (Core, RunSession, RunNetwork) {
	t.Helper()
	store := profile.Store{Root: t.TempDir()}
	p := profile.Default("privacy")
	p.Network.Mode = network.ModeTun2Socks
	p.Network.ProxySecretRef = "shared-proxy"
	p.Network.MediatedResolver = "1.1.1.1"
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{ProfileName: p.Name, Backend: "lima", Workspace: t.TempDir(), Command: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	runEnvironment, err := core.SelectRunEnvironment(plan, RunEnvironmentOptions{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	runSession, err := core.BeginRunSession(plan, runEnvironment, RunSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = core.CloseRunSession(runSession) })
	resolver := network.EnvSecretResolver{Env: []string{network.SecretEnvName("shared-proxy") + "=socks5://user:one@127.0.0.1:1080"}}
	runNetwork, err := core.PrepareRunNetwork(runSession, RunNetworkOptions{Resolver: resolver, Verified: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runSession.RuntimeShimDir, "hideout-dns-stub"), []byte("helper"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runSession.RuntimeShimDir, "tun2socks"), []byte("tun2socks-bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	return core, runSession, runNetwork
}

func preparedReadyEnvironmentNetworkService(t *testing.T) (Core, environment.Store, RunSession, RunNetwork) {
	t.Helper()
	core, store, record := concurrentLifecycleFixture(t)
	plan, err := core.PlanRun(RunPlanOptions{ProfileName: record.Profile, Backend: record.Backend, Workspace: record.HostWorkspace(), Command: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	runEnvironment := RunEnvironment{Active: true, Record: record, RuntimeDir: store.RuntimeDir(record.ID), PreserveInstance: true}
	runSession, err := core.BeginRunSession(plan, runEnvironment, RunSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = core.CloseRunSession(runSession) })
	servicePlan := network.Plan{
		Mode: network.ModeTun2Socks, ConfigurationFingerprint: strings.Repeat("b", 64),
		GatewayID: "gw_test", ConfigurationID: testEnvironmentServiceConfigurationID(), MediatedResolver: "1.1.1.1",
	}
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
		PreviousServiceState: &state, EnvironmentServiceAction: networkServiceReuse,
	}
}

func TestLastOwnerReconcilesCrashedSiblingAndRetainsNetworkService(t *testing.T) {
	core, store, record := concurrentLifecycleFixture(t)
	currentID := "ses_20260716T120000Z_0123456789abcdef"
	staleID := "ses_20260716T115900Z_fedcba9876543210"
	now := time.Now().UTC()
	owner, err := session.AcquireOwner(store.OwnerRoot(record.ID), session.OwnerRecord{
		Schema: session.ActiveSessionSchema, SessionID: currentID, EnvironmentID: record.ID,
		Profile: record.Profile, Backend: record.Backend, WorkspaceID: "wrk_" + strings.Repeat("a", 64), SessionSnapshotID: testSessionSnapshotID(),
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
		Profile: record.Profile, Backend: record.Backend, WorkspaceID: "wrk_" + strings.Repeat("a", 64), SessionSnapshotID: testSessionSnapshotID(),
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
		GatewayID: "gw_test", ConfigurationID: testEnvironmentServiceConfigurationID(), MediatedResolver: "1.1.1.1",
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
	runEnvironment := RunEnvironment{Active: true, Record: record, RuntimeDir: store.RuntimeDir(record.ID), PreserveInstance: true}
	var held *environment.Lock
	_, err = core.finishConcurrentRunEnvironment(context.Background(), &held, runEnvironment, owner, currentID, nil, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The environment network service is environment-scoped: the last owner
	// leaving must NOT tear it down. It is retained across idle-grace so a
	// later same-boot run reuses it, and is scrubbed only once the guest is
	// observed stopped by the daemon lifecycle reconciliation.
	if _, err := network.LoadServiceState(statePath); err != nil {
		t.Fatalf("environment network service state was not retained for reuse: %v", err)
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
		Profile: record.Profile, Backend: record.Backend, WorkspaceID: "wrk_" + strings.Repeat("a", 64), SessionSnapshotID: testSessionSnapshotID(),
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
	_, err = core.finishConcurrentRunEnvironment(
		context.Background(), &held, runEnvironment, owner, currentID, nil, false, nil,
	)
	if !errors.Is(err, session.ErrOwnerUnprovable) {
		t.Fatalf("unprovable sibling cleanup error=%v", err)
	}
	if _, err := os.Stat(activationPath); err != nil {
		t.Fatalf("activation receipt was removed with unprovable sibling: %v", err)
	}
}

func TestEnvironmentLockFailureStillFinishesLifecycleRegistration(t *testing.T) {
	core, store, record := concurrentLifecycleFixture(t)
	sessionID := "ses_20260716T120000Z_0123456789abcdef"
	if _, err := store.PrepareSessionRuntime(record.ID, sessionID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	owner, err := session.AcquireOwner(store.OwnerRoot(record.ID), session.OwnerRecord{
		Schema: session.ActiveSessionSchema, SessionID: sessionID, EnvironmentID: record.ID,
		Profile: record.Profile, Backend: record.Backend, WorkspaceID: "wrk_" + strings.Repeat("a", 64), SessionSnapshotID: testSessionSnapshotID(),
		State: session.OwnerStateRunning, TerminalMode: session.TerminalNone,
		StartedAt: now, UpdatedAt: now, CommandClass: "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	registration := newRecordingLifecycleRegistration()
	runEnvironment := RunEnvironment{
		Active: true, Record: record, RuntimeDir: store.RuntimeDir(record.ID), PreserveInstance: true,
	}
	// Make transition-lock acquisition fail immediately. The owner keeps its
	// open lock descriptor, so this exercises the cleanup failure path without
	// waiting for the normal bounded lock timeout.
	if err := os.Rename(filepath.Dir(store.RuntimeDir(record.ID)), filepath.Join(t.TempDir(), "removed-environment")); err != nil {
		t.Fatal(err)
	}
	var held *environment.Lock
	_, err = core.finishConcurrentRunEnvironment(
		context.Background(), &held, runEnvironment, owner, sessionID, nil, false, nil, registration,
	)
	if err == nil {
		t.Fatal("missing environment lock unexpectedly produced successful cleanup")
	}
	registration.mu.Lock()
	defer registration.mu.Unlock()
	if registration.finishCalls != 1 || registration.finishCleanupErr == nil {
		t.Fatalf("lifecycle finish calls=%d cleanupErr=%v", registration.finishCalls, registration.finishCleanupErr)
	}
}

func TestStopEnvironmentNetworkServiceCleansOnlyServiceDirectory(t *testing.T) {
	serviceDir := t.TempDir()
	plan := network.Plan{
		Mode: network.ModeTun2Socks, ConfigurationFingerprint: strings.Repeat("a", 64),
		GatewayID: "gw_test", ConfigurationID: testEnvironmentServiceConfigurationID(), MediatedResolver: "1.1.1.1",
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
	if err := (Core{}).stopRunNetworkService(context.Background(), runNetwork, controller, &backend.Session{ID: "ses_test"}, nil); err != nil {
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
	// tun2socks is the first helper copyNetworkHelper copies, so an oversize one
	// must be rejected before any target is left behind.
	source := filepath.Join(shimDir, "tun2socks")
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
	if _, statErr := os.Stat(filepath.Join(serviceDir, "tun2socks")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("partial target stat error=%v", statErr)
	}
}

type recordingNetworkServiceController struct {
	starts         int
	verifies       int
	directVerifies int
	stops          int
	startErr       error
	verifyErr      error
	dnsErr         error
	dnsSwitches    [][2]string
}

func (c *recordingNetworkServiceController) ReconfigureEnvironmentNetworkDNS(_ context.Context, _ *backend.Session, _ string, oldResolver, newResolver string, _ []string) error {
	c.dnsSwitches = append(c.dnsSwitches, [2]string{oldResolver, newResolver})
	return c.dnsErr
}

func (c *recordingNetworkServiceController) VerifyEnvironmentNetwork(context.Context, *backend.Session, string, []string) error {
	c.verifies++
	return c.verifyErr
}

func (c *recordingNetworkServiceController) VerifyDirectEnvironmentNetwork(context.Context, *backend.Session, string, []string) error {
	c.directVerifies++
	return c.verifyErr
}

func (c *recordingNetworkServiceController) StartEnvironmentNetwork(context.Context, *backend.Session, string, string, []string) error {
	c.starts++
	return c.startErr
}

func (c *recordingNetworkServiceController) StopEnvironmentNetwork(context.Context, *backend.Session, string, string, []string) error {
	c.stops++
	return nil
}
