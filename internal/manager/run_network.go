package manager

import (
	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/backend/lima"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
)

type RunNetworkOptions struct {
	Resolver netpolicy.SecretResolver
	Verified bool
	DryRun   bool
}

type RunNetwork struct {
	Plan netpolicy.Plan
}

func (c Core) PrepareRunNetwork(runSession RunSession, opts RunNetworkOptions) (RunNetwork, error) {
	resolver := opts.Resolver
	if resolver == nil {
		resolver = netpolicy.EnvSecretResolver{}
	}
	netPlan, netErr := netpolicy.Prepare(netpolicy.Spec{
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
	})
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
			"mode":           netPlan.Mode,
			"engine":         netPlan.Engine,
			"dnsPolicy":      netPlan.DNSPolicy,
			"proxySecretRef": netPlan.ProxySecretRef,
			"verified":       netPlan.Verified,
			"runtimeVerify":  netPlan.RuntimeVerify,
			"failClosed":     netPlan.FailClosed,
			"reason":         netPlan.Reason,
			"localBypass":    append([]string(nil), netPlan.LocalBypassHosts...),
			"networkPlan":    presence(netPlan.ManifestPath),
		},
	})
	return RunNetwork{Plan: netPlan}, netErr
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
