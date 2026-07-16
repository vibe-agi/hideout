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
	Plan                    netpolicy.Plan
	EnvironmentService      bool
	EnvironmentServiceStart bool
	ServiceDir              string
	GuestServiceDir         string
	ServiceStatePath        string
}

func (c Core) PrepareRunNetwork(runSession RunSession, opts RunNetworkOptions) (RunNetwork, error) {
	resolver := opts.Resolver
	if resolver == nil {
		resolver = netpolicy.EnvSecretResolver{}
	}
	spec := netpolicy.Spec{
		Profile:          runSession.Plan.RuntimeProfile,
		Backend:          runSession.Plan.Backend,
		SessionDir:       runSession.RuntimeSessionDir,
		GuestSessionDir:  GuestSessionDirForBackend(runSession.Plan.Backend),
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
	if runSession.Environment.Active && runSession.Plan.RuntimeProfile.Network.Mode == netpolicy.ModeTun2Socks {
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

func (c Core) prepareEnvironmentNetworkService(runSession RunSession, spec netpolicy.Spec, dryRun bool) (RunNetwork, error) {
	store := environment.Store{Root: c.Store.Root}
	serviceDir := store.RuntimeNetworkServiceDir(runSession.Environment.Record.ID)
	guestServiceDir := lima.GuestRuntimeDir + "/services/network"
	statePath := filepath.Join(serviceDir, "state.json")
	spec.SessionDir = serviceDir
	spec.GuestSessionDir = guestServiceDir
	spec.DryRun = true
	candidate, err := netpolicy.Prepare(spec)
	if err != nil {
		return RunNetwork{Plan: candidate, EnvironmentService: true, ServiceDir: serviceDir, GuestServiceDir: guestServiceDir, ServiceStatePath: statePath}, err
	}
	runNetwork := RunNetwork{
		Plan: candidate, EnvironmentService: true, ServiceDir: serviceDir,
		GuestServiceDir: guestServiceDir, ServiceStatePath: statePath,
	}
	state, err := netpolicy.LoadServiceState(statePath)
	if err == nil {
		if state.ConfigurationFingerprint != candidate.ConfigurationFingerprint || state.Mode != candidate.Mode {
			return runNetwork, errors.New("environment network service configuration conflicts with a live owner; wait for all sessions to exit before retrying")
		}
		if state.Status != netpolicy.ServiceReady {
			return runNetwork, fmt.Errorf("environment network service is %s; run hideout doctor --feature sessions", state.Status)
		}
		runNetwork.Plan.Verified = false
		runNetwork.Plan.RuntimeVerify = true
		runNetwork.Plan.Reason = "matching environment network service requires current-boot verification"
		return runNetwork, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return runNetwork, fmt.Errorf("read environment network service: %w", err)
	}
	if dryRun {
		runNetwork.EnvironmentServiceStart = true
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
	state, err = netpolicy.BuildServiceState(runSession.Environment.Record.ID, materialized, netpolicy.ServiceStarting, "", time.Now().UTC(), nil)
	if err != nil {
		return runNetwork, err
	}
	if err := netpolicy.WriteServiceState(statePath, state); err != nil {
		return runNetwork, err
	}
	return runNetwork, nil
}

func (c Core) StartRunNetworkService(ctx context.Context, runSession RunSession, runNetwork *RunNetwork, controller backend.EnvironmentNetworkServiceController, session *backend.Session, env []string) error {
	if runNetwork == nil || !runNetwork.EnvironmentService {
		return nil
	}
	if controller == nil {
		return errors.New("selected backend cannot own the environment network service")
	}
	if session == nil || strings.TrimSpace(session.ExpectedBootID) == "" {
		return errors.New("environment network service requires the activated guest boot identity")
	}
	if !runNetwork.EnvironmentServiceStart {
		state, err := netpolicy.LoadServiceState(runNetwork.ServiceStatePath)
		if err != nil {
			return fmt.Errorf("reload environment network service before reuse: %w", err)
		}
		if state.BootID != session.ExpectedBootID {
			return fmt.Errorf("environment network service belongs to guest boot %q, current boot is %q; stop the environment before retrying", state.BootID, session.ExpectedBootID)
		}
		if err := controller.VerifyEnvironmentNetwork(ctx, session, runNetwork.GuestServiceDir, env); err != nil {
			failed, stateErr := netpolicy.BuildServiceState(runSession.Environment.Record.ID, runNetwork.Plan, netpolicy.ServiceFailed, state.BootID, state.StartedAt, err)
			if stateErr == nil {
				_ = netpolicy.WriteServiceState(runNetwork.ServiceStatePath, failed)
			}
			return fmt.Errorf("verify environment network service: %w", err)
		}
		runNetwork.Plan.Verified = true
		runNetwork.Plan.RuntimeVerify = false
		runNetwork.Plan.Reason = "matching environment network service verified in the current guest boot"
		return nil
	}
	if err := copyNetworkHelper(runSession.RuntimeShimDir, runNetwork.ServiceDir); err != nil {
		return err
	}
	if err := controller.StartEnvironmentNetwork(ctx, session, runNetwork.GuestServiceDir, runNetwork.Plan.GuestBootstrapPath, env); err != nil {
		state, stateErr := netpolicy.BuildServiceState(runSession.Environment.Record.ID, runNetwork.Plan, netpolicy.ServiceFailed, session.ExpectedBootID, time.Now().UTC(), err)
		if stateErr == nil {
			_ = netpolicy.WriteServiceState(runNetwork.ServiceStatePath, state)
		}
		return err
	}
	state, err := netpolicy.BuildServiceState(runSession.Environment.Record.ID, runNetwork.Plan, netpolicy.ServiceReady, session.ExpectedBootID, time.Now().UTC(), nil)
	if err != nil {
		return err
	}
	if err := netpolicy.WriteServiceState(runNetwork.ServiceStatePath, state); err != nil {
		return err
	}
	runNetwork.Plan.Verified = true
	runNetwork.Plan.RuntimeVerify = false
	runNetwork.EnvironmentServiceStart = false
	return nil
}

func stopRunNetworkService(ctx context.Context, runNetwork RunNetwork, controller backend.EnvironmentNetworkServiceController, session *backend.Session, env []string) error {
	if !runNetwork.EnvironmentService {
		return nil
	}
	if controller == nil {
		return errors.New("selected backend cannot clean the environment network service")
	}
	state, err := netpolicy.LoadServiceState(runNetwork.ServiceStatePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	cleaning, err := netpolicy.BuildServiceState(state.EnvironmentID, runNetwork.Plan, netpolicy.ServiceCleaning, state.BootID, state.StartedAt, nil)
	if err != nil {
		return err
	}
	if err := netpolicy.WriteServiceState(runNetwork.ServiceStatePath, cleaning); err != nil {
		return err
	}
	if err := controller.StopEnvironmentNetwork(ctx, session, runNetwork.GuestServiceDir, runNetwork.Plan.GuestCleanupPath, env); err != nil {
		failed, stateErr := netpolicy.BuildServiceState(state.EnvironmentID, runNetwork.Plan, netpolicy.ServiceFailed, state.BootID, state.StartedAt, err)
		if stateErr == nil {
			_ = netpolicy.WriteServiceState(runNetwork.ServiceStatePath, failed)
		}
		return err
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
	return nil
}

func (c Core) discardUnstartedEnvironmentNetworkService(environmentID string) error {
	store := environment.Store{Root: c.Store.Root}
	if err := store.ClearRuntimeServices(environmentID); err != nil {
		return fmt.Errorf("discard unstarted environment network service: %w", err)
	}
	return nil
}

func copyNetworkHelper(sessionShimDir, serviceDir string) error {
	source := filepath.Join(sessionShimDir, "hideout-dns-stub")
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("environment network helper: %w", err)
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return fmt.Errorf("environment network helper: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("environment network helper must be a regular file")
	}
	if info.Size() > maxEnvironmentNetworkHelperBytes {
		return fmt.Errorf("environment network helper exceeds %d bytes", maxEnvironmentNetworkHelperBytes)
	}
	if err := os.MkdirAll(serviceDir, 0o700); err != nil {
		return err
	}
	target := filepath.Join(serviceDir, "hideout-dns-stub")
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	removeTarget := true
	defer func() {
		if removeTarget {
			_ = os.Remove(target)
		}
	}()
	written, copyErr := io.Copy(output, input)
	if copyErr == nil && written != info.Size() {
		copyErr = errors.New("environment network helper changed while being copied")
	}
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil {
		return errors.Join(copyErr, closeErr)
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
