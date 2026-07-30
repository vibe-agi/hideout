package manager

const (
	CapabilityConfigNetworkPosture    = "config.network.posture"
	CapabilityConfigNetworkProxyRef   = "config.network.proxy-ref"
	CapabilityConfigNetworkDNS        = "config.network.dns"
	CapabilityConfigEnvironment       = "config.profile.environment"
	CapabilityConfigHostFS            = "config.profile.hostfs"
	CapabilityConfigCommandProxy      = "config.profile.command-proxy"
	CapabilityConfigCommandAdapter    = "config.profile.command-adapter"
	CapabilityConfigActivityRetention = "config.activity.retention"
	CapabilitySecretManage            = "secret.manage"
)

// DefaultConfigurationCapabilities is the Manager-owned list of mutation
// surfaces safe for capability-driven clients to present. Capability IDs are
// stable, lower-case projection identifiers; they deliberately remain
// separate from typed-change discriminators carried in mutation bodies.
func DefaultConfigurationCapabilities(
	includeSecretLifecycle bool,
) []OperatorCapabilityProjection {
	values := []OperatorCapabilityProjection{
		configurationCapability(
			CapabilityConfigNetworkPosture,
			"config.network",
		),
		configurationCapability(
			CapabilityConfigNetworkProxyRef,
			"config.network",
		),
		configurationCapability(
			CapabilityConfigNetworkDNS,
			"config.network",
		),
		configurationCapability(
			CapabilityConfigEnvironment,
			"config.environment",
		),
		configurationCapability(
			CapabilityConfigHostFS,
			"config.hostfs",
		),
		configurationCapability(
			CapabilityConfigCommandProxy,
			"config.command-proxy",
		),
		configurationCapability(
			CapabilityConfigCommandAdapter,
			"config.command-adapter",
		),
		configurationCapability(
			CapabilityConfigActivityRetention,
			"config.activity-retention",
		),
	}
	if includeSecretLifecycle {
		values = append(values, OperatorCapabilityProjection{
			ID:         CapabilitySecretManage,
			State:      OperatorCapabilityAvailable,
			Provider:   "keychain",
			Mutable:    true,
			ActionRefs: []string{"secret.manage"},
		})
	}
	return values
}

func configurationCapability(
	id string,
	action string,
) OperatorCapabilityProjection {
	return OperatorCapabilityProjection{
		ID:         id,
		State:      OperatorCapabilityAvailable,
		Provider:   "manager",
		Mutable:    true,
		ActionRefs: []string{action},
	}
}
