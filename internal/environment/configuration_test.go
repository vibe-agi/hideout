package environment

import (
	"strings"
	"testing"
)

func TestMachineIdentityUsesImageContentNotDistributionMetadata(t *testing.T) {
	first, err := ImageIdentityFor("https://one.example/runtime.qcow2#sha256:"+strings.Repeat("a", 64), nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ImageIdentityFor("https://two.example/mirror.qcow2#sha256:"+strings.Repeat("a", 64), nil)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("distribution location changed content identity: first=%+v second=%+v", first, second)
	}

	base := MachineIdentity{
		Schema: MachineIdentitySchema, Backend: "lima", Image: first,
		TargetUser: "developer", TargetUID: 1000, GuestMachineID: strings.Repeat("c", 32),
		VMType: "vz", MountType: "virtiofs", WorkspaceIsolation: "workspace-portal",
	}
	firstID, err := base.ID()
	if err != nil {
		t.Fatal(err)
	}
	base.Image.Value = strings.Repeat("b", 64)
	secondID, err := base.ID()
	if err != nil {
		t.Fatal(err)
	}
	if firstID == secondID {
		t.Fatal("image content change did not change machine identity")
	}
}

func TestConfigurationLayersMapChangesToLifecycleImpact(t *testing.T) {
	base := Configuration{Layers: ConfigurationLayers{MachineID: "machine-a", BootID: "boot-a", ServicesID: "services-a", SessionID: "session-a"}}
	tests := []struct {
		name   string
		mutate func(*ConfigurationLayers)
		want   ChangeImpact
	}{
		{name: "service", mutate: func(value *ConfigurationLayers) { value.ServicesID = "services-b" }, want: ImpactLive},
		{name: "session", mutate: func(value *ConfigurationLayers) { value.SessionID = "session-b" }, want: ImpactNewSession},
		{name: "boot", mutate: func(value *ConfigurationLayers) { value.BootID = "boot-b" }, want: ImpactReconfigure},
		{name: "machine", mutate: func(value *ConfigurationLayers) { value.MachineID = "machine-b" }, want: ImpactRecreate},
		{name: "mixed", mutate: func(value *ConfigurationLayers) {
			value.ServicesID, value.SessionID, value.BootID = "services-b", "session-b", "boot-b"
		}, want: ImpactReconfigure},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := base
			test.mutate(&current.Layers)
			if got := RequiredImpact(CompareConfigurations(base, current)); got != test.want {
				t.Fatalf("impact=%q want=%q", got, test.want)
			}
		})
	}
}

func TestEnvironmentServiceConfigurationIsIndependentFromMachineIdentity(t *testing.T) {
	direct := EnvironmentServiceConfiguration{
		Schema:  EnvironmentServiceConfigurationSchema,
		Network: NetworkServiceConfiguration{Egress: "direct"},
	}
	proxy := EnvironmentServiceConfiguration{
		Schema:  EnvironmentServiceConfigurationSchema,
		Network: NetworkServiceConfiguration{Egress: "proxy", ProxySecretRef: "default-proxy", Resolver: "1.1.1.1"},
	}
	directID, err := direct.ID()
	if err != nil {
		t.Fatal(err)
	}
	proxyID, err := proxy.ID()
	if err != nil {
		t.Fatal(err)
	}
	if directID == proxyID {
		t.Fatal("different service generations share an identity")
	}
	changes := CompareConfigurations(
		Configuration{Layers: ConfigurationLayers{MachineID: "m", BootID: "b", ServicesID: directID, SessionID: "s"}},
		Configuration{Layers: ConfigurationLayers{MachineID: "m", BootID: "b", ServicesID: proxyID, SessionID: "s"}},
	)
	if RequiredImpact(changes) != ImpactLive {
		t.Fatalf("network service change impact=%q", RequiredImpact(changes))
	}
}
