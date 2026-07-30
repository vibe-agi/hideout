package render

import (
	"testing"

	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestConnectionSummaryNeverPresentsUnobservedDesiredRouteAsEffective(
	t *testing.T,
) {
	desired := profile.Default("default")
	desired.Network = profile.Network{
		Mode:             profile.NetworkModeTun2Socks,
		ProxySecretRef:   "local-proxy",
		MediatedResolver: "1.1.1.1",
	}
	state := liveconsole.State{
		Profiles: []manager.ProfileProjection{{
			Profile: "default",
			Desired: desired,
			Effective: manager.ProfileEffective{
				Status:   manager.EffectiveNotObserved,
				Sessions: []manager.EffectiveSessionSnapshot{},
			},
		}},
	}
	if got := connectionSummary(
		state,
		"default",
	); got != "not observed | desired proxy" {
		t.Fatalf("unobserved connection summary=%q", got)
	}
}

func TestConnectionSummaryShowsProvedSecretGeneration(
	t *testing.T,
) {
	state := liveconsole.State{
		Profiles: []manager.ProfileProjection{{
			Profile: "default",
			Effective: manager.ProfileEffective{
				Status: manager.EffectiveCurrent,
				Network: &manager.EffectiveNetwork{
					Mode:             "proxy",
					ProxySecretRef:   "local-proxy",
					SecretGeneration: 4,
					DNS:              "1.1.1.1",
				},
				Sessions: []manager.EffectiveSessionSnapshot{},
			},
		}},
	}
	if got := connectionSummary(
		state,
		"default",
	); got != "proxy gen 4 | DNS 1.1.1.1" {
		t.Fatalf("effective connection summary=%q", got)
	}
}

func TestConfigEffectiveProxyIncludesSecretGeneration(
	t *testing.T,
) {
	projection := manager.ProfileProjection{
		Effective: manager.ProfileEffective{
			Status: manager.EffectiveCurrent,
			Network: &manager.EffectiveNetwork{
				Mode:             "proxy",
				ProxySecretRef:   "local-proxy",
				SecretGeneration: 4,
			},
			Sessions: []manager.EffectiveSessionSnapshot{},
		},
	}
	if got := configEffectiveValue(
		liveconsole.State{},
		projection,
		manager.ChangeNetworkProxyRef,
	); got != "local-proxy · generation 4" {
		t.Fatalf("effective proxy value=%q", got)
	}
}
