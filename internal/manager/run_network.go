package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/backend/lima"
	"github.com/vibe-agi/hideout/internal/environment"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
)

const maxEnvironmentNetworkHelperBytes = int64(64 << 20)

type RunNetworkOptions struct {
	Resolver netpolicy.SecretResolver
	Verified bool
	DryRun   bool
}

type RunNetwork struct {
	Plan                     netpolicy.Plan
	EnvironmentService       bool
	EnvironmentServiceStart  bool
	EnvironmentServiceAction string
	ServiceDir               string
	GuestServiceDir          string
	ServiceStatePath         string
	PreviousServiceState     *netpolicy.ServiceState
	PreviousPlan             netpolicy.Plan
	Gateway                  *netpolicy.GatewayRegistry
	GatewayChange            *netpolicy.GatewayChange

	// materializeSpec, when non-nil, defers bootstrap materialization on the
	// reuse/gateway path. Healthy reuse leaves the shared guest network
	// untouched, so it never needs the per-session secret or bootstrap scripts
	// written to disk; only reuse self-heal re-establishes, and it materializes
	// from this spec on demand (see StartRunNetworkService).
	materializeSpec *netpolicy.Spec
}

const (
	networkServiceStart        = "start"
	networkServiceReuse        = "reuse"
	networkServiceGateway      = "switch-gateway"
	networkServiceEnableProxy  = "enable-proxy"
	networkServiceDisableProxy = "disable-proxy"
	networkServiceRestartProxy = "restart-proxy"
	networkServiceDNS          = "switch-dns"
)

func (c Core) PrepareRunNetwork(runSession RunSession, opts RunNetworkOptions) (RunNetwork, error) {
	resolver := opts.Resolver
	if resolver == nil {
		resolver = netpolicy.EnvSecretResolver{}
	}
	spec := netpolicy.Spec{
		Profile:          runSession.Plan.RuntimeProfile,
		Backend:          runSession.Plan.Backend,
		ArtifactDir:      runSession.RuntimeSessionDir,
		GuestArtifactDir: GuestSessionDirForBackend(runSession.Plan.Backend),
		SecretDir:        runSession.RuntimeSessionDir,
		GuestSecretDir:   GuestSessionDirForBackend(runSession.Plan.Backend),
		TargetEnv:        runSession.Env.Env,
		Resolver:         resolver,
		LocalBypassHosts: LocalBypassHostsForBackend(runSession.Plan.Backend),
		Verified:         opts.Verified,
		RuntimeVerify:    runSession.Plan.Backend == "lima",
		DryRun:           opts.DryRun,
	}
	var (
		runNetwork RunNetwork
		netErr     error
	)
	if runSession.Environment.Active && runSession.Plan.Backend == "lima" {
		spec.ConfigurationID = runSession.Environment.Configuration.Layers.ServicesID
		if strings.TrimSpace(spec.ConfigurationID) == "" {
			return RunNetwork{}, errors.New("environment network service configuration identity is unavailable")
		}
		runNetwork, netErr = c.prepareEnvironmentNetworkService(runSession, spec, opts.DryRun)
	} else {
		runNetwork.Plan, netErr = netpolicy.Prepare(spec)
	}
	netPlan := runNetwork.Plan
	aw := runSession.Audit
	if aw == nil {
		aw = audit.NewDiscard()
	}
	_ = aw.Emit(audit.Event{
		Session:  runSession.Layout.ID,
		Profile:  runSession.Plan.ProfileName,
		Backend:  runSession.Plan.Backend,
		Action:   "network.setup",
		Decision: NetworkDecision(netPlan, netErr),
		Details: map[string]any{
			"mode":               netPlan.Mode,
			"engine":             netPlan.Engine,
			"dnsPolicy":          netPlan.DNSPolicy,
			"proxySecretRef":     netPlan.ProxySecretRef,
			"verified":           netPlan.Verified,
			"runtimeVerify":      netPlan.RuntimeVerify,
			"failClosed":         netPlan.FailClosed,
			"reason":             netPlan.Reason,
			"localBypass":        append([]string(nil), netPlan.LocalBypassHosts...),
			"networkPlan":        presence(netPlan.ManifestPath),
			"environmentService": runNetwork.EnvironmentService,
			"serviceStart":       runNetwork.EnvironmentServiceStart,
		},
	})
	return runNetwork, netErr
}

func (c Core) requireExclusiveNetworkPostureTransition(runSession RunSession) error {
	store := environment.Store{Root: c.Store.Root}
	live, err := liveOwnerIDs(store.OwnerRoot(runSession.Environment.Record.ID))
	if err != nil {
		return fmt.Errorf("prove exclusive environment network posture transition: %w", err)
	}
	for sessionID := range live {
		if sessionID != runSession.Layout.ID {
			return fmt.Errorf("environment network posture switch requires no active sibling sessions; session %s is still active", sessionID)
		}
	}
	return nil
}

func (c Core) prepareEnvironmentNetworkService(runSession RunSession, spec netpolicy.Spec, dryRun bool) (RunNetwork, error) {
	store := environment.Store{Root: c.Store.Root}
	serviceDir := store.RuntimeNetworkServiceDir(runSession.Environment.Record.ID)
	guestServiceDir := lima.GuestRuntimeDir + "/services/network"
	statePath := filepath.Join(serviceDir, "state.json")
	spec.ArtifactDir = serviceDir
	spec.GuestArtifactDir = guestServiceDir
	spec.SecretDir = runSession.RuntimeSessionDir
	spec.GuestSecretDir = lima.GuestRuntimeDir + "/sessions/" + runSession.Layout.ID
	// The environment network privileged setup runs before per-session shims are
	// mounted, so its bootstrap must find the network helpers in the service dir
	// (copyNetworkHelper places them here), not the per-session shim dir.
	spec.GuestHelperDir = guestServiceDir
	spec.DryRun = true
	candidate, err := netpolicy.Prepare(spec)
	if err != nil {
		return RunNetwork{Plan: candidate, EnvironmentService: true, ServiceDir: serviceDir, GuestServiceDir: guestServiceDir, ServiceStatePath: statePath}, err
	}
	registry := c.NetworkGateways
	if registry == nil {
		registry = netpolicy.NewGatewayRegistry()
	}
	binding, gatewayChange, err := registry.Stage(runSession.Environment.Record.ID, candidate.UpstreamProxyURL)
	if err != nil {
		return RunNetwork{Plan: candidate, EnvironmentService: true, ServiceDir: serviceDir, GuestServiceDir: guestServiceDir, ServiceStatePath: statePath}, err
	}
	rollbackGateway := true
	defer func() {
		if rollbackGateway {
			_ = gatewayChange.Rollback()
		}
	}()
	guestGatewayHost := "127.0.0.1"
	if runSession.Plan.Backend == "lima" {
		guestGatewayHost = "host.lima.internal"
	}
	gatewayURL, err := binding.ProxyURL(guestGatewayHost)
	if err != nil {
		return RunNetwork{}, err
	}
	spec.GatewayID = binding.ID
	if candidate.Mode == netpolicy.ModeTun2Socks {
		spec.GatewayProxyURL = gatewayURL
	}
	candidate, err = netpolicy.Prepare(spec)
	if err != nil {
		return RunNetwork{Plan: candidate, EnvironmentService: true, ServiceDir: serviceDir, GuestServiceDir: guestServiceDir, ServiceStatePath: statePath}, err
	}
	runNetwork := RunNetwork{
		Plan: candidate, EnvironmentService: true, ServiceDir: serviceDir,
		GuestServiceDir: guestServiceDir, ServiceStatePath: statePath,
		Gateway: registry, GatewayChange: gatewayChange,
	}
	state, err := netpolicy.LoadServiceState(statePath)
	if err == nil {
		if state.Status != netpolicy.ServiceReady {
			return runNetwork, fmt.Errorf("environment network service is %s; run hideout doctor --feature sessions", state.Status)
		}
		previous := state
		runNetwork.PreviousServiceState = &previous
		runNetwork.PreviousPlan = previousNetworkServicePlan(state, serviceDir, guestServiceDir)
		switch {
		case state.ConfigurationFingerprint == candidate.ConfigurationFingerprint && state.Mode == candidate.Mode && state.GatewayID == candidate.GatewayID:
			runNetwork.EnvironmentServiceAction = networkServiceReuse
		case state.Mode == netpolicy.ModeDirect && candidate.Mode == netpolicy.ModeDirect && state.ConfigurationFingerprint == candidate.ConfigurationFingerprint:
			// Direct guests do not consume the host gateway endpoint. A daemon or
			// Core restart may mint a new gateway without requiring guest work.
			runNetwork.EnvironmentServiceAction = networkServiceGateway
		case state.Mode == netpolicy.ModeTun2Socks && candidate.Mode == netpolicy.ModeTun2Socks && state.GatewayID == candidate.GatewayID && state.Resolver == candidate.MediatedResolver:
			runNetwork.EnvironmentServiceAction = networkServiceGateway
		case state.Mode == netpolicy.ModeDirect && candidate.Mode == netpolicy.ModeTun2Socks:
			runNetwork.EnvironmentServiceAction = networkServiceEnableProxy
		case state.Mode == netpolicy.ModeTun2Socks && candidate.Mode == netpolicy.ModeDirect:
			runNetwork.EnvironmentServiceAction = networkServiceDisableProxy
		case state.Mode == netpolicy.ModeTun2Socks && candidate.Mode == netpolicy.ModeTun2Socks && state.GatewayID == candidate.GatewayID:
			runNetwork.EnvironmentServiceAction = networkServiceDNS
		default:
			runNetwork.EnvironmentServiceAction = networkServiceRestartProxy
		}
		runNetwork.EnvironmentServiceStart = runNetwork.EnvironmentServiceAction != networkServiceReuse && runNetwork.EnvironmentServiceAction != networkServiceGateway
		if runNetwork.EnvironmentServiceAction == networkServiceEnableProxy || runNetwork.EnvironmentServiceAction == networkServiceDisableProxy {
			if err := c.requireExclusiveNetworkPostureTransition(runSession); err != nil {
				return runNetwork, err
			}
		}
		if runNetwork.EnvironmentServiceAction == networkServiceReuse || runNetwork.EnvironmentServiceAction == networkServiceGateway {
			if !dryRun {
				// Defer bootstrap materialization. Healthy reuse leaves the shared
				// guest network in place, so writing the per-session secret and
				// bootstrap scripts here is wasted work and an unnecessary
				// secret-on-disk window. Only reuse self-heal (a failed current-boot
				// verification) re-establishes the network; StartRunNetworkService
				// materializes from this spec at that point (the idempotent bootstrap
				// reconciles any stale guest remnant first).
				lazySpec := spec
				runNetwork.materializeSpec = &lazySpec
			}
			runNetwork.Plan.Verified = false
			runNetwork.Plan.RuntimeVerify = candidate.Mode == netpolicy.ModeTun2Socks
			runNetwork.Plan.Reason = "environment network service requires current-boot verification"
			rollbackGateway = false
			return runNetwork, nil
		}
		if dryRun {
			rollbackGateway = false
			return runNetwork, nil
		}
		spec.DryRun = false
		materialized, materializeErr := netpolicy.Prepare(spec)
		runNetwork.Plan = materialized
		if materializeErr != nil {
			return runNetwork, materializeErr
		}
		if err := writePreviousNetworkCleanup(runNetwork.PreviousPlan); err != nil {
			return runNetwork, err
		}
		rollbackGateway = false
		return runNetwork, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return runNetwork, fmt.Errorf("read environment network service: %w", err)
	}
	if dryRun {
		runNetwork.EnvironmentServiceStart = true
		runNetwork.EnvironmentServiceAction = networkServiceStart
		rollbackGateway = false
		return runNetwork, nil
	}
	spec.DryRun = false
	materialized, err := netpolicy.Prepare(spec)
	if err != nil {
		runNetwork.Plan = materialized
		return runNetwork, err
	}
	runNetwork.Plan = materialized
	runNetwork.EnvironmentServiceStart = true
	runNetwork.EnvironmentServiceAction = networkServiceStart
	state, err = netpolicy.BuildServiceState(runSession.Environment.Record.ID, materialized, netpolicy.ServiceStarting, "", time.Now().UTC(), nil)
	if err != nil {
		return runNetwork, err
	}
	if err := netpolicy.WriteServiceState(statePath, state); err != nil {
		return runNetwork, err
	}
	rollbackGateway = false
	return runNetwork, nil
}

func previousNetworkServicePlan(state netpolicy.ServiceState, serviceDir, guestServiceDir string) netpolicy.Plan {
	return netpolicy.Plan{
		Mode: state.Mode, MediatedResolver: state.Resolver, GatewayID: state.GatewayID, ConfigurationID: state.ConfigurationID,
		CleanupPath:      filepath.Join(serviceDir, "network", "previous-cleanup.sh"),
		GuestCleanupPath: guestServiceDir + "/network/previous-cleanup.sh",
	}
}

func writePreviousNetworkCleanup(plan netpolicy.Plan) error {
	if strings.TrimSpace(plan.CleanupPath) == "" {
		return errors.New("previous environment network cleanup path is required")
	}
	if err := os.MkdirAll(filepath.Dir(plan.CleanupPath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(plan.CleanupPath, []byte(netpolicy.CleanupScript(plan)), 0o700)
}

func (c Core) StartRunNetworkService(ctx context.Context, runSession RunSession, runNetwork *RunNetwork, controller backend.EnvironmentNetworkServiceController, session *backend.Session, env []string) error {
	if runNetwork == nil || !runNetwork.EnvironmentService {
		return nil
	}
	action := runNetwork.EnvironmentServiceAction
	if action == "" {
		if runNetwork.EnvironmentServiceStart {
			action = networkServiceStart
		} else {
			action = networkServiceReuse
		}
	}
	requiresController := runNetwork.Plan.Mode == netpolicy.ModeTun2Socks ||
		(runNetwork.PreviousServiceState != nil && runNetwork.PreviousServiceState.Mode == netpolicy.ModeTun2Socks) ||
		action == networkServiceDNS || action == networkServiceRestartProxy
	if controller == nil && requiresController {
		return errors.New("selected backend cannot own the environment network service")
	}
	if session == nil || strings.TrimSpace(session.ExpectedBootID) == "" {
		return errors.New("environment network service requires the activated guest boot identity")
	}
	gatewayResolved := false
	gatewayActivated := false
	keepActivatedGatewayOnFailure := false
	defer func() {
		if !gatewayResolved && runNetwork.GatewayChange != nil {
			_ = runNetwork.GatewayChange.Rollback()
		}
	}()
	activateGateway := func() error {
		if runNetwork.GatewayChange == nil || gatewayActivated {
			return nil
		}
		if err := runNetwork.GatewayChange.Activate(); err != nil {
			return err
		}
		gatewayActivated = true
		return nil
	}
	commitGateway := func() error {
		if runNetwork.GatewayChange == nil {
			gatewayResolved = true
			return nil
		}
		if err := runNetwork.GatewayChange.Commit(); err != nil {
			return err
		}
		gatewayResolved = true
		return nil
	}
	preserveExplicitDirectRoute := func(operationErr error) error {
		if !keepActivatedGatewayOnFailure || !gatewayActivated {
			return operationErr
		}
		return errors.Join(operationErr, commitGateway())
	}
	startedAt := time.Now().UTC()
	if runNetwork.PreviousServiceState != nil {
		startedAt = runNetwork.PreviousServiceState.StartedAt
		if runNetwork.PreviousServiceState.BootID != session.ExpectedBootID && action != networkServiceReuse {
			// Non-reuse transitions mutate the previously established guest network;
			// if the guest booted anew that network is gone, so fail closed. A reuse
			// on a changed boot self-heals below by re-establishing from scratch.
			return fmt.Errorf("environment network service belongs to guest boot %q, current boot is %q; stop the environment before retrying", runNetwork.PreviousServiceState.BootID, session.ExpectedBootID)
		}
		if action != networkServiceReuse {
			switching, err := netpolicy.BuildServiceState(runSession.Environment.Record.ID, runNetwork.Plan, netpolicy.ServiceSwitching, session.ExpectedBootID, startedAt, nil)
			if err != nil {
				return err
			}
			if err := netpolicy.WriteServiceState(runNetwork.ServiceStatePath, switching); err != nil {
				return err
			}
		}
	}
	fail := func(operationErr error) error {
		runNetwork.Plan.Verified = false
		runNetwork.Plan.RuntimeVerify = runNetwork.Plan.Mode == netpolicy.ModeTun2Socks
		failed, stateErr := netpolicy.BuildServiceState(runSession.Environment.Record.ID, runNetwork.Plan, netpolicy.ServiceFailed, session.ExpectedBootID, startedAt, operationErr)
		if stateErr == nil {
			_ = netpolicy.WriteServiceState(runNetwork.ServiceStatePath, failed)
		}
		return operationErr
	}
	copyHelper := func() error {
		return copyNetworkHelper(runSession.RuntimeShimDir, runNetwork.ServiceDir)
	}
	materialize := func() error {
		if runNetwork.materializeSpec == nil {
			return nil
		}
		matSpec := *runNetwork.materializeSpec
		matSpec.DryRun = false
		materialized, err := netpolicy.Prepare(matSpec)
		if err != nil {
			return err
		}
		// Preserve the reuse-path plan flags computed during prepare.
		materialized.Verified = false
		materialized.RuntimeVerify = runNetwork.Plan.RuntimeVerify
		materialized.Reason = runNetwork.Plan.Reason
		runNetwork.Plan = materialized
		return nil
	}
	establishTun := func() error {
		if err := activateGateway(); err != nil {
			return err
		}
		if err := copyHelper(); err != nil {
			return err
		}
		return controller.StartEnvironmentNetwork(ctx, session, runNetwork.GuestServiceDir, runNetwork.Plan.GuestBootstrapPath, env)
	}
	var operationErr error
	switch action {
	case networkServiceReuse, networkServiceGateway:
		if runNetwork.Plan.Mode == netpolicy.ModeTun2Socks {
			operationErr = verifyReusedEnvironmentNetwork(ctx, controller, session, runNetwork.GuestServiceDir, env)
			if operationErr != nil && action == networkServiceReuse {
				// Self-heal only after the verify retries above are exhausted: the
				// persisted privacy network is genuinely invalid on the current guest
				// (rebooted, recreated, or an unclean prior teardown), so re-establish
				// it fresh (the idempotent bootstrap reconciles any stale remnant
				// first). The retries ensure a transient guest-probe flake does not
				// tear down and rebuild a healthy shared network a sibling session may
				// still be using. Materialize the deferred bootstrap artifacts first;
				// the healthy-reuse path left them unwritten.
				if operationErr = materialize(); operationErr == nil {
					operationErr = establishTun()
				}
			} else if operationErr != nil {
				operationErr = fmt.Errorf("verify environment network service: %w", operationErr)
			}
		} else if controller != nil {
			operationErr = controller.VerifyDirectEnvironmentNetwork(ctx, session, runNetwork.GuestServiceDir, env)
			if operationErr != nil {
				operationErr = fmt.Errorf("verify direct environment network service: %w", operationErr)
			}
		}
		if operationErr == nil && action == networkServiceGateway {
			operationErr = activateGateway()
		}
	case networkServiceStart, networkServiceEnableProxy:
		if runNetwork.Plan.Mode == netpolicy.ModeTun2Socks {
			operationErr = establishTun()
		} else if controller != nil {
			operationErr = controller.VerifyDirectEnvironmentNetwork(ctx, session, runNetwork.GuestServiceDir, env)
			if operationErr == nil {
				operationErr = activateGateway()
			}
		}
	case networkServiceDisableProxy:
		operationErr = activateGateway()
		if operationErr == nil {
			// This is the explicit privacy-posture commit point. Once direct is
			// selected, a later cleanup failure must not restore a proxy state claim
			// while the guest may already have restored its direct default route.
			keepActivatedGatewayOnFailure = true
			operationErr = controller.StopEnvironmentNetwork(ctx, session, runNetwork.GuestServiceDir, runNetwork.PreviousPlan.GuestCleanupPath, env)
		}
		if operationErr == nil {
			operationErr = controller.VerifyDirectEnvironmentNetwork(ctx, session, runNetwork.GuestServiceDir, env)
		}
	case networkServiceDNS:
		dnsController, ok := controller.(backend.EnvironmentNetworkDNSController)
		if !ok {
			operationErr = errors.New("selected backend cannot reconfigure the environment DNS service")
		} else if operationErr = copyHelper(); operationErr == nil {
			operationErr = dnsController.ReconfigureEnvironmentNetworkDNS(ctx, session, runNetwork.GuestServiceDir, runNetwork.PreviousServiceState.Resolver, runNetwork.Plan.MediatedResolver, env)
			if operationErr == nil {
				operationErr = controller.VerifyEnvironmentNetwork(ctx, session, runNetwork.GuestServiceDir, env)
			}
		}
	case networkServiceRestartProxy:
		operationErr = activateGateway()
		if operationErr == nil {
			operationErr = controller.StopEnvironmentNetwork(ctx, session, runNetwork.GuestServiceDir, runNetwork.PreviousPlan.GuestCleanupPath, env)
		}
		if operationErr == nil {
			if operationErr = copyHelper(); operationErr == nil {
				operationErr = controller.StartEnvironmentNetwork(ctx, session, runNetwork.GuestServiceDir, runNetwork.Plan.GuestBootstrapPath, env)
			}
		}
	default:
		operationErr = fmt.Errorf("unsupported environment network service action %q", action)
	}
	secretCleanupErr := removeRunNetworkSecret(runNetwork.Plan.ProxySecretPath)
	if operationErr != nil || secretCleanupErr != nil {
		operationErr = preserveExplicitDirectRoute(operationErr)
		rollbackProved := action == networkServiceGateway
		var reconfigureErr backend.EnvironmentServiceReconfigureError
		if errors.As(operationErr, &reconfigureErr) {
			rollbackProved = reconfigureErr.RollbackProved
		}
		if rollbackProved && secretCleanupErr == nil && runNetwork.PreviousServiceState != nil {
			if stateErr := netpolicy.WriteServiceState(runNetwork.ServiceStatePath, *runNetwork.PreviousServiceState); stateErr != nil {
				return fail(errors.Join(operationErr, stateErr))
			}
			return operationErr
		}
		return fail(errors.Join(operationErr, secretCleanupErr))
	}
	state, err := netpolicy.BuildServiceState(runSession.Environment.Record.ID, runNetwork.Plan, netpolicy.ServiceReady, session.ExpectedBootID, startedAt, nil)
	if err != nil {
		return preserveExplicitDirectRoute(err)
	}
	if err := netpolicy.WriteServiceState(runNetwork.ServiceStatePath, state); err != nil {
		return preserveExplicitDirectRoute(err)
	}
	runNetwork.Plan.Verified = true
	runNetwork.Plan.RuntimeVerify = false
	runNetwork.EnvironmentServiceStart = false
	if err := commitGateway(); err != nil {
		return fail(err)
	}
	return nil
}

func removeRunNetworkSecret(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove consumed session network secret: %w", err)
	}
	return nil
}

// verifyReusedEnvironmentNetwork verifies a reused environment network, retrying a
// few times with a short backoff so a transient guest-probe failure does not drive
// the destructive self-heal (which tears down and rebuilds a shared network a
// sibling session may still be using). It returns the last verification error.
func verifyReusedEnvironmentNetwork(ctx context.Context, controller backend.EnvironmentNetworkServiceController, session *backend.Session, guestServiceDir string, env []string) error {
	const attempts = 3
	var err error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(300 * time.Millisecond):
			}
		}
		if err = controller.VerifyEnvironmentNetwork(ctx, session, guestServiceDir, env); err == nil {
			return nil
		}
	}
	return err
}

func (c Core) stopRunNetworkService(ctx context.Context, runNetwork RunNetwork, controller backend.EnvironmentNetworkServiceController, session *backend.Session, env []string) error {
	if !runNetwork.EnvironmentService {
		return nil
	}
	state, err := netpolicy.LoadServiceState(runNetwork.ServiceStatePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if controller == nil && state.Mode == netpolicy.ModeTun2Socks {
		return errors.New("selected backend cannot clean the environment network service")
	}
	activePlan := netpolicy.Plan{
		Mode: state.Mode, MediatedResolver: state.Resolver, GatewayID: state.GatewayID, ConfigurationID: state.ConfigurationID,
		ConfigurationFingerprint: state.ConfigurationFingerprint,
		CleanupPath:              filepath.Join(runNetwork.ServiceDir, "network", "cleanup.sh"),
		GuestCleanupPath:         runNetwork.GuestServiceDir + "/network/cleanup.sh",
	}
	cleaning, err := netpolicy.BuildServiceState(state.EnvironmentID, activePlan, netpolicy.ServiceCleaning, state.BootID, state.StartedAt, nil)
	if err != nil {
		return err
	}
	if err := netpolicy.WriteServiceState(runNetwork.ServiceStatePath, cleaning); err != nil {
		return err
	}
	if controller != nil && state.Mode == netpolicy.ModeTun2Socks {
		if err := controller.StopEnvironmentNetwork(ctx, session, runNetwork.GuestServiceDir, activePlan.GuestCleanupPath, env); err != nil {
			failed, stateErr := netpolicy.BuildServiceState(state.EnvironmentID, activePlan, netpolicy.ServiceFailed, state.BootID, state.StartedAt, err)
			if stateErr == nil {
				_ = netpolicy.WriteServiceState(runNetwork.ServiceStatePath, failed)
			}
			return err
		}
	}
	entries, err := os.ReadDir(runNetwork.ServiceDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(runNetwork.ServiceDir, entry.Name())); err != nil {
			return err
		}
	}
	registry := runNetwork.Gateway
	if registry == nil {
		registry = c.NetworkGateways
	}
	if registry != nil {
		return registry.CloseEnvironment(state.EnvironmentID)
	}
	return nil
}

func (c Core) discardUnstartedEnvironmentNetworkService(environmentID string) error {
	store := environment.Store{Root: c.Store.Root}
	if err := store.ClearRuntimeServices(environmentID); err != nil {
		return fmt.Errorf("discard unstarted environment network service: %w", err)
	}
	return nil
}

// copyNetworkHelper co-locates the network helpers with the environment network
// service bootstrap. The privileged setup runs before per-session shims are
// mounted, so tun2socks and hideout-dns-stub must live in the service dir the
// bootstrap can reach; the guest bootstrap prepends this dir to PATH.
func copyNetworkHelper(sessionShimDir, serviceDir string) error {
	if err := os.MkdirAll(serviceDir, 0o700); err != nil {
		return err
	}
	for _, name := range []string{"tun2socks", "hideout-dns-stub"} {
		if err := copyNetworkHelperFile(sessionShimDir, serviceDir, name); err != nil {
			return err
		}
	}
	return nil
}

func copyNetworkHelperFile(sessionShimDir, serviceDir, name string) error {
	source := filepath.Join(sessionShimDir, name)
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("environment network helper %s: %w", name, err)
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return fmt.Errorf("environment network helper %s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("environment network helper %s must be a regular file", name)
	}
	if info.Size() > maxEnvironmentNetworkHelperBytes {
		return fmt.Errorf("environment network helper %s exceeds %d bytes", name, maxEnvironmentNetworkHelperBytes)
	}
	target := filepath.Join(serviceDir, name)
	output, err := os.CreateTemp(serviceDir, "."+name+"-")
	if err != nil {
		return err
	}
	temporary := output.Name()
	removeTarget := true
	defer func() {
		if removeTarget {
			_ = os.Remove(temporary)
		}
	}()
	if err := output.Chmod(0o700); err != nil {
		_ = output.Close()
		return err
	}
	written, copyErr := io.Copy(output, input)
	if copyErr == nil && written != info.Size() {
		copyErr = fmt.Errorf("environment network helper %s changed while being copied", name)
	}
	if copyErr == nil {
		copyErr = output.Sync()
	}
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil {
		return errors.Join(copyErr, closeErr)
	}
	if err := os.Rename(temporary, target); err != nil {
		return err
	}
	removeTarget = false
	return nil
}

func NetworkDecision(plan netpolicy.Plan, err error) string {
	if err != nil || plan.FailClosed {
		return "deny"
	}
	if plan.RuntimeVerify && !plan.Verified {
		return "audit-only"
	}
	return "allow"
}

func GuestSessionDirForBackend(backendName string) string {
	if backendName == "lima" {
		return lima.GuestSessionDir
	}
	return ""
}

func LocalBypassHostsForBackend(backendName string) []string {
	if backendName == "lima" {
		return []string{"host.lima.internal"}
	}
	return nil
}

func presence(value string) string {
	if value == "" {
		return "absent"
	}
	return "present"
}
