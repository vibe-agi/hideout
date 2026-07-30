package manager

import (
	"context"
	"time"

	"github.com/vibe-agi/hideout/internal/lifecycle"
)

// LegacyProfileTransactionAdapter preserves the established profile-specific
// plan/result shapes while routing every supported mutation through
// ProfileTransactionService. Legacy callers implicitly confirm the plan they
// just requested; modern clients should use the generic CAS API directly.
type LegacyProfileTransactionAdapter struct {
	Core         Core
	Now          func() time.Time
	Mutations    lifecycle.MutationCoordinator
	Transactions *ProfileTransactionService
}

func (adapter LegacyProfileTransactionAdapter) PlanEnvironment(
	options ProfileEnvOptions,
) (ProfileEnvPlan, error) {
	plan, err := adapter.Core.PlanProfileEnv(options)
	if err != nil || !plan.Changed {
		return plan, err
	}
	_, err = typedEnvironmentChange(plan, options.Value)
	return plan, err
}

func (adapter LegacyProfileTransactionAdapter) ApplyEnvironment(
	ctx context.Context,
	options ProfileEnvOptions,
) (ProfileEnvResult, error) {
	plan, err := adapter.PlanEnvironment(options)
	if err != nil {
		return ProfileEnvResult{}, err
	}
	if !plan.Changed {
		return adapter.Core.ApplyProfileEnv(plan)
	}
	change, err := typedEnvironmentChange(plan, options.Value)
	if err != nil {
		return ProfileEnvResult{}, err
	}
	applied, err := adapter.service().ApplyCurrent(
		ctx,
		plan.Profile,
		"legacy-profile-env",
		[]TypedChange{change},
	)
	if err != nil {
		return ProfileEnvResult{}, err
	}
	desired := applied.Projection.Desired
	return ProfileEnvResult{
		Version: ProfileEnvPlanVersion,
		Plan:    plan,
		Applied: true,
		Public:  sortedProfileEnvPublicKeys(desired.Env.Public),
		Inherit: sortedStringsForManager(desired.Env.Inherit),
		Deny:    sortedStringsForManager(desired.Env.Deny),
	}, nil
}

func (adapter LegacyProfileTransactionAdapter) PlanNetwork(
	options ProfileNetworkOptions,
) (ProfileNetworkPlan, error) {
	plan, err := adapter.Core.PlanProfileNetwork(options)
	if err != nil || !plan.Changed {
		return plan, err
	}
	_, err = ConfigurationChangesForProfileNetworkPlan(plan)
	return plan, err
}

func (adapter LegacyProfileTransactionAdapter) ApplyNetwork(
	ctx context.Context,
	options ProfileNetworkOptions,
) (ProfileNetworkResult, error) {
	plan, err := adapter.PlanNetwork(options)
	if err != nil {
		return ProfileNetworkResult{}, err
	}
	if !plan.Changed {
		return adapter.Core.ApplyProfileNetwork(plan)
	}
	changes, err := ConfigurationChangesForProfileNetworkPlan(plan)
	if err != nil {
		return ProfileNetworkResult{}, err
	}
	applied, err := adapter.service().ApplyCurrent(
		ctx,
		plan.Profile,
		"legacy-profile-network",
		changes,
	)
	if err != nil {
		return ProfileNetworkResult{}, err
	}
	operation := applied.Operation
	return ProfileNetworkResult{
		Plan:      plan,
		Applied:   true,
		Network:   profileNetworkState(applied.Projection.Desired),
		Operation: &operation,
	}, nil
}

func (adapter LegacyProfileTransactionAdapter) PlanCommandProxy(
	options CommandProxyOptions,
) (CommandProxyPlan, error) {
	plan, err := adapter.Core.PlanCommandProxy(options)
	if err != nil || !plan.Changed {
		return plan, err
	}
	_, err = typedCommandProxyChange(plan)
	return plan, err
}

func (adapter LegacyProfileTransactionAdapter) ApplyCommandProxy(
	ctx context.Context,
	options CommandProxyOptions,
) (CommandProxyResult, error) {
	plan, err := adapter.PlanCommandProxy(options)
	if err != nil {
		return CommandProxyResult{}, err
	}
	if !plan.Changed {
		return adapter.Core.ApplyCommandProxy(plan)
	}
	change, err := typedCommandProxyChange(plan)
	if err != nil {
		return CommandProxyResult{}, err
	}
	applied, err := adapter.service().ApplyCurrent(
		ctx,
		plan.Profile,
		"legacy-command-proxy",
		[]TypedChange{change},
	)
	if err != nil {
		return CommandProxyResult{}, err
	}
	return CommandProxyResult{
		Version:  CommandProxyPlanVersion,
		Plan:     plan,
		Applied:  true,
		Commands: commandProxyNames(applied.Projection.Desired),
	}, nil
}

func (adapter LegacyProfileTransactionAdapter) PlanHostFS(
	options ProfileHostFSOptions,
) (ProfileHostFSPlan, error) {
	plan, err := adapter.Core.PlanProfileHostFS(options)
	if err != nil || plan.Operation == "migrate-list" {
		return plan, err
	}
	_, err = typedHostFSChange(plan)
	return plan, err
}

func (adapter LegacyProfileTransactionAdapter) ApplyHostFS(
	ctx context.Context,
	options ProfileHostFSOptions,
) (ProfileHostFSResult, error) {
	plan, err := adapter.PlanHostFS(options)
	if err != nil {
		return ProfileHostFSResult{}, err
	}
	if plan.Operation == "migrate-list" {
		// This one-release legacy migration is already guarded by a source
		// digest and explicit disclosure confirmation. It is not expressible
		// as one ordinary HostFS add/remove change.
		return adapter.Core.ApplyProfileHostFS(plan)
	}
	change, err := typedHostFSChange(plan)
	if err != nil {
		return ProfileHostFSResult{}, err
	}
	applied, err := adapter.service().ApplyCurrent(
		ctx,
		plan.Profile,
		"legacy-profile-hostfs",
		[]TypedChange{change},
	)
	if err != nil {
		return ProfileHostFSResult{}, err
	}
	desired := applied.Projection.Desired
	plan.GrantsAfter = profileHostFSRuleSummaries(
		desired.HostFS.Grants,
		false,
	)
	plan.DenyAfter = profileHostFSRuleSummaries(
		desired.HostFS.Deny,
		true,
	)
	if plan.Operation == "add" || plan.Operation == "deny" {
		if planned := addedProfileHostFSRule(
			plan,
			plan.Operation == "deny",
		); planned != nil {
			plan.RuleID = planned.ID
			plan.PlannedRule = planned
		}
	}
	return ProfileHostFSResult{
		Version: ProfileHostFSPlanVersion,
		Plan:    plan,
		Applied: true,
		Grants:  append([]ProfileHostFSRuleSummary(nil), plan.GrantsAfter...),
		Deny:    append([]ProfileHostFSRuleSummary(nil), plan.DenyAfter...),
	}, nil
}

func (adapter LegacyProfileTransactionAdapter) PlanCommandAdapter(
	options CommandAdapterOptions,
) (CommandAdapterPlan, error) {
	plan, err := adapter.Core.PlanCommandAdapter(options)
	if err != nil || !plan.Changed {
		return plan, err
	}
	_, err = typedCommandAdapterChange(plan)
	return plan, err
}

func (adapter LegacyProfileTransactionAdapter) ApplyCommandAdapter(
	ctx context.Context,
	options CommandAdapterOptions,
) (CommandAdapterResult, error) {
	plan, err := adapter.PlanCommandAdapter(options)
	if err != nil {
		return CommandAdapterResult{}, err
	}
	if !plan.Changed {
		return adapter.Core.ApplyCommandAdapter(plan)
	}
	change, err := typedCommandAdapterChange(plan)
	if err != nil {
		return CommandAdapterResult{}, err
	}
	applied, err := adapter.service().ApplyCurrent(
		ctx,
		plan.Profile,
		"legacy-command-adapter",
		[]TypedChange{change},
	)
	if err != nil {
		return CommandAdapterResult{}, err
	}
	return CommandAdapterResult{
		Version:  CommandAdapterPlanVersion,
		Plan:     plan,
		Applied:  true,
		Adapters: commandAdapterNames(applied.Projection.Desired),
	}, nil
}

func (adapter LegacyProfileTransactionAdapter) service() *ProfileTransactionService {
	if adapter.Transactions != nil {
		return adapter.Transactions
	}
	service := NewProfileTransactionService(adapter.Core)
	service.Mutations = adapter.Mutations
	if adapter.Now != nil {
		service.now = adapter.Now
	}
	return service
}

func typedEnvironmentChange(
	plan ProfileEnvPlan,
	privateValue string,
) (TypedChange, error) {
	var value map[string]any
	switch plan.Operation {
	case "set":
		value = map[string]any{
			"set": map[string]string{plan.Name: privateValue},
		}
	case "unset":
		value = map[string]any{"unset": []string{plan.Name}}
	case "inherit":
		value = map[string]any{"inherit": []string{plan.Name}}
	case "uninherit":
		value = map[string]any{"uninherit": []string{plan.Name}}
	case "deny":
		value = map[string]any{"deny": []string{plan.Name}}
	case "undeny":
		value = map[string]any{"undeny": []string{plan.Name}}
	default:
		return TypedChange{}, ErrInvalidConfigurationDraft
	}
	return NewTypedChange(ChangeProfileEnvironment, value)
}

// ConfigurationChangesForProfileNetworkPlan converts the established
// profile-network planner result into the closed generic transaction schema.
// Callers can use it to present and explicitly confirm the same canonical
// review before Apply instead of relying on the legacy implicit-confirmation
// adapter.
func ConfigurationChangesForProfileNetworkPlan(
	plan ProfileNetworkPlan,
) ([]TypedChange, error) {
	mode := plan.After.Mode
	if mode == "tun2socks" {
		mode = "proxy"
	}
	posture, err := NewTypedChange(ChangeNetworkPosture, map[string]any{
		"mode": mode,
	})
	if err != nil {
		return nil, err
	}
	changes := []TypedChange{posture}
	if mode == "direct" {
		return changes, nil
	}
	proxyRef, err := NewTypedChange(ChangeNetworkProxyRef, map[string]any{
		"ref": plan.After.ProxySecretRef,
	})
	if err != nil {
		return nil, err
	}
	dns, err := NewTypedChange(ChangeNetworkDNS, map[string]any{
		"mode":     "doh",
		"serverIp": plan.After.MediatedResolver,
	})
	if err != nil {
		return nil, err
	}
	return append(changes, proxyRef, dns), nil
}

func typedCommandProxyChange(
	plan CommandProxyPlan,
) (TypedChange, error) {
	return NewTypedChange(ChangeProfileCommandProxy, map[string]any{
		"operation": plan.Operation,
		"command":   plan.Command,
	})
}

func typedHostFSChange(
	plan ProfileHostFSPlan,
) (TypedChange, error) {
	value := map[string]any{"operation": plan.Operation}
	switch plan.Operation {
	case "add", "deny":
		value["rule"] = plan.Rule
		value["reason"] = plan.Reason
	case "remove":
		value["ruleId"] = plan.RuleID
	default:
		return TypedChange{}, ErrInvalidConfigurationDraft
	}
	return NewTypedChange(ChangeProfileHostFS, value)
}

func typedCommandAdapterChange(
	plan CommandAdapterPlan,
) (TypedChange, error) {
	value := map[string]any{
		"operation": plan.Operation,
		"adapterId": plan.AdapterID,
	}
	if plan.Operation == "add-local" {
		value["path"] = plan.Path
		value["entrypoint"] = plan.Entrypoint
		value["commands"] = plan.Commands
		value["allowedProposalCapabilities"] =
			plan.AllowedProposalCapabilities
	}
	return NewTypedChange(ChangeProfileCommandAdapter, value)
}
