package daemon

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/manager"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
)

type daemonDNSRecoveryBackend struct {
	*daemonLifecycleBackend
	session   *backend.Session
	workdir   string
	resolvers []string
	verifies  int
}

func (provider *daemonDNSRecoveryBackend) StartEnvironmentNetwork(
	context.Context,
	*backend.Session,
	string,
	string,
	[]string,
) error {
	return nil
}

func (provider *daemonDNSRecoveryBackend) VerifyEnvironmentNetwork(
	_ context.Context,
	session *backend.Session,
	workdir string,
	_ []string,
) error {
	provider.session = session
	provider.workdir = workdir
	provider.verifies++
	return nil
}

func (provider *daemonDNSRecoveryBackend) VerifyDirectEnvironmentNetwork(
	context.Context,
	*backend.Session,
	string,
	[]string,
) error {
	return nil
}

func (provider *daemonDNSRecoveryBackend) StopEnvironmentNetwork(
	context.Context,
	*backend.Session,
	string,
	string,
	[]string,
) error {
	return nil
}

func (provider *daemonDNSRecoveryBackend) ReconfigureEnvironmentNetworkDNS(
	context.Context,
	*backend.Session,
	string,
	string,
	string,
	[]string,
) error {
	return nil
}

func (provider *daemonDNSRecoveryBackend) VerifyEnvironmentNetworkDNS(
	_ context.Context,
	session *backend.Session,
	workdir string,
	resolver string,
	_ []string,
) error {
	provider.session = session
	provider.workdir = workdir
	provider.resolvers = append(provider.resolvers, resolver)
	return nil
}

func TestStartupNetworkRuntimeBindsExactEnvironmentBootAndDNSOnlyControl(
	t *testing.T,
) {
	store, record := daemonLifecycleEnvironment(t)
	const bootID = "01234567-89ab-cdef-0123-456789abcdef"
	networkPlan := netpolicy.Plan{
		Mode:                     netpolicy.ModeTun2Socks,
		MediatedResolver:         "1.1.1.1",
		GatewayID:                "gateway-recovery",
		ConfigurationFingerprint: strings.Repeat("a", 64),
		ConfigurationID: "sha256:" +
			strings.Repeat("b", 64),
	}
	state, err := netpolicy.BuildServiceState(
		record.ID,
		networkPlan,
		netpolicy.ServiceSwitching,
		bootID,
		time.Now().Add(-time.Minute).UTC(),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := netpolicy.WriteServiceState(
		filepath.Join(
			(environment.Store{Root: store.Root}).
				RuntimeNetworkServiceDir(record.ID),
			"state.json",
		),
		state,
	); err != nil {
		t.Fatal(err)
	}
	backendProvider := &daemonDNSRecoveryBackend{
		daemonLifecycleBackend: &daemonLifecycleBackend{},
	}
	runtimes := startupNetworkRuntimeProvider{
		storeRoot: store.Root,
		backends: func(
			observed environment.Record,
		) (manager.EnvironmentLifecycleBackend, error) {
			if observed.ID != record.ID {
				t.Fatalf("factory record=%+v", observed)
			}
			return backendProvider, nil
		},
	}
	if err := runtimes.EnvironmentNetworkRuntimeAvailable(
		context.Background(),
		record.ID,
	); err != nil {
		t.Fatal(err)
	}
	lease, err := runtimes.AcquireEnvironmentNetworkRuntime(
		context.Background(),
		record.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if lease.EnvironmentID() != record.ID ||
		lease.SessionID() != "ses_network_recovery" ||
		lease.BootID() != bootID {
		t.Fatalf("startup DNS lease identity is not exact")
	}
	if err := lease.VerifyDNS(
		context.Background(),
		"9.9.9.9",
	); err != nil {
		t.Fatal(err)
	}
	if backendProvider.session == nil ||
		backendProvider.session.EnvironmentID != record.ID ||
		backendProvider.session.InstanceName != record.InstanceName ||
		backendProvider.session.ExpectedBootID != bootID ||
		!backendProvider.session.PrivilegedSetupRequired ||
		len(backendProvider.resolvers) != 1 ||
		backendProvider.resolvers[0] != "9.9.9.9" ||
		backendProvider.verifies != 1 ||
		backendProvider.workdir !=
			"/hideout/runtime/services/network" {
		t.Fatalf(
			"startup DNS backend binding session=%+v resolvers=%v verifies=%d workdir=%q",
			backendProvider.session,
			backendProvider.resolvers,
			backendProvider.verifies,
			backendProvider.workdir,
		)
	}
}

var _ manager.EnvironmentLifecycleBackend = (*daemonDNSRecoveryBackend)(nil)
var _ backend.EnvironmentNetworkServiceController = (*daemonDNSRecoveryBackend)(nil)
var _ backend.EnvironmentNetworkDNSController = (*daemonDNSRecoveryBackend)(nil)
var _ backend.EnvironmentNetworkDNSVerifier = (*daemonDNSRecoveryBackend)(nil)
