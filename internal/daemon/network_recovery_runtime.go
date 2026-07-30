package daemon

import (
	"context"
	"path/filepath"
	"sync"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/backend/lima"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/manager"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
)

// startupNetworkRuntimeProvider reconstructs only the restricted DNS control
// capability for an exact journaled Lima boot. It is used before the daemon
// accepts sessions and cannot run target commands or manufacture a new
// environment.
type startupNetworkRuntimeProvider struct {
	storeRoot string
	backends  manager.EnvironmentLifecycleBackendFactory
}

func (provider startupNetworkRuntimeProvider) EnvironmentNetworkRuntimeAvailable(
	ctx context.Context,
	environmentID string,
) error {
	lease, err := provider.AcquireEnvironmentNetworkRuntime(
		ctx,
		environmentID,
	)
	if err != nil {
		return err
	}
	lease.Release()
	return nil
}

func (provider startupNetworkRuntimeProvider) AcquireEnvironmentNetworkRuntime(
	ctx context.Context,
	environmentID string,
) (manager.EnvironmentNetworkRuntimeLease, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if provider.backends == nil ||
		!environment.ValidID(environmentID) {
		return nil,
			manager.ErrEnvironmentNetworkRuntimeUnavailable
	}
	store := environment.Store{Root: provider.storeRoot}
	record, err := store.Load(environmentID)
	if err != nil ||
		record.Backend != "lima" ||
		record.InstanceName == "" {
		return nil,
			manager.ErrEnvironmentNetworkRuntimeUnavailable
	}
	state, err := netpolicy.LoadServiceState(
		filepath.Join(
			store.RuntimeNetworkServiceDir(environmentID),
			"state.json",
		),
	)
	if err != nil ||
		state.EnvironmentID != environmentID ||
		state.Mode != netpolicy.ModeTun2Socks ||
		state.BootID == "" {
		return nil,
			manager.ErrEnvironmentNetworkRuntimeUnavailable
	}
	providerBackend, err := provider.backends(record)
	if err != nil {
		return nil,
			manager.ErrEnvironmentNetworkRuntimeUnavailable
	}
	dnsController, controllerOK := providerBackend.(backend.EnvironmentNetworkDNSController)
	dnsVerifier, verifierOK := providerBackend.(backend.EnvironmentNetworkDNSVerifier)
	networkController, networkOK := providerBackend.(backend.EnvironmentNetworkServiceController)
	if !controllerOK || !verifierOK || !networkOK {
		return nil,
			manager.ErrEnvironmentNetworkRuntimeUnavailable
	}
	session := &backend.Session{
		ID:                      "ses_network_recovery",
		EnvironmentID:           environmentID,
		Backend:                 "lima",
		InstanceName:            record.InstanceName,
		ExpectedBootID:          state.BootID,
		PrivilegedSetupRequired: true,
		NetworkPrivilegedSetup:  true,
	}
	guestServiceDir := lima.GuestRuntimeDir + "/services/network"
	controlEnv := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
	registration := manager.EnvironmentNetworkRuntimeRegistration{
		EnvironmentID: environmentID,
		SessionID:     session.ID,
		BootID:        state.BootID,
		ReconfigureDNS: func(
			callCtx context.Context,
			oldResolver string,
			newResolver string,
		) error {
			return dnsController.ReconfigureEnvironmentNetworkDNS(
				callCtx,
				session,
				guestServiceDir,
				oldResolver,
				newResolver,
				controlEnv,
			)
		},
		VerifyDNS: func(
			callCtx context.Context,
			resolver string,
		) error {
			if err := dnsVerifier.VerifyEnvironmentNetworkDNS(
				callCtx,
				session,
				guestServiceDir,
				resolver,
				controlEnv,
			); err != nil {
				return err
			}
			return networkController.VerifyEnvironmentNetwork(
				callCtx,
				session,
				guestServiceDir,
				controlEnv,
			)
		},
	}
	if registration.Validate() != nil {
		return nil,
			manager.ErrEnvironmentNetworkRuntimeUnavailable
	}
	return &startupNetworkRuntimeLease{
		registration: registration,
	}, nil
}

type startupNetworkRuntimeLease struct {
	registration manager.EnvironmentNetworkRuntimeRegistration
	once         sync.Once
}

func (lease *startupNetworkRuntimeLease) EnvironmentID() string {
	return lease.registration.EnvironmentID
}

func (lease *startupNetworkRuntimeLease) SessionID() string {
	return lease.registration.SessionID
}

func (lease *startupNetworkRuntimeLease) BootID() string {
	return lease.registration.BootID
}

func (lease *startupNetworkRuntimeLease) ReconfigureDNS(
	ctx context.Context,
	oldResolver string,
	newResolver string,
) error {
	return lease.registration.ReconfigureDNS(
		ctx,
		oldResolver,
		newResolver,
	)
}

func (lease *startupNetworkRuntimeLease) VerifyDNS(
	ctx context.Context,
	resolver string,
) error {
	return lease.registration.VerifyDNS(ctx, resolver)
}

func (lease *startupNetworkRuntimeLease) Release() {
	if lease == nil {
		return
	}
	lease.once.Do(func() {})
}
