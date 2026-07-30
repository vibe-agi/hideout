package render

import (
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestConfigSeparatesDesiredEffectiveTransitionAndScope(t *testing.T) {
	state := configRenderState()
	output := Config(ConfigInput{
		State: state, Selected: 0,
	}, Options{
		Width: 120, Height: 30, NoColor: true, Unicode: false,
	})
	for _, expected := range []string{
		"Config · default · revision 9",
		"FIELD",
		"DESIRED",
		"EFFECTIVE",
		"TRANSITION",
		"SCOPE",
		"Connection mode",
		"proxy",
		"direct",
		"activating",
		"new connections",
		"Environment policy",
		"2 set · 1 inherit · 1 deny",
		"future session snapshot",
		"new sessions",
		"Activity retention",
		"256.0 MiB · 1 day",
		"current owner 48.0 MiB / 256.0 MiB",
		"store (read-only) 320.0 MiB / 1.0 GiB",
		"future owners",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("config output missing %q:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "visible-value") {
		t.Fatalf("configuration view exposed an environment value:\n%s", output)
	}
}

func TestConfigRowsComeOnlyFromAdvertisedManagerCapabilities(t *testing.T) {
	state := configRenderState()
	state.Capabilities = []liveconsole.CapabilityProjection{{
		ID:       manager.CapabilityConfigNetworkDNS,
		Status:   workloadtypes.CoverageAvailable,
		Provider: "manager", Mutable: true,
	}}
	rows := ConfigRows(state)
	if len(rows) != 1 ||
		rows[0].EditorID != manager.ChangeNetworkDNS ||
		!rows[0].Editable {
		t.Fatalf("capability-driven rows=%+v", rows)
	}

	state.Capabilities[0].Status = workloadtypes.CoverageUnavailable
	state.Capabilities[0].Reason = "provider-disabled"
	rows = ConfigRows(state)
	if len(rows) != 1 || rows[0].Editable ||
		!strings.Contains(rows[0].Reason, "provider-disabled") {
		t.Fatalf("unavailable capability row=%+v", rows)
	}
}

func TestConfigStaleIsReadOnlyAndSanitizesCapabilityReason(t *testing.T) {
	state := configRenderState()
	state.ReadOnly = true
	state.RequiresReseed = true
	state.StreamHealth = liveconsole.StreamHealth{
		State:  liveconsole.HealthStale,
		Reason: "\x1b]8;;https://evil.invalid\aSTALE\u202e",
	}
	state.Capabilities[0].Reason = "\x1b[31mprovider unavailable"
	output := Config(ConfigInput{State: state}, Options{
		Width: 80, Height: 24, NoColor: true,
	})
	if !strings.Contains(output, "READ-ONLY") ||
		!strings.Contains(output, "re-seed") ||
		strings.Contains(output, "\x1b") ||
		strings.Contains(output, "\u202e") {
		t.Fatalf("stale configuration rendering is unsafe:\n%q", output)
	}
	for _, row := range ConfigRows(state) {
		if row.Editable {
			t.Fatalf("stale row remained editable: %+v", row)
		}
	}
}

func configRenderState() liveconsole.State {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	desired := profile.Default("default")
	desired.Network.Mode = profile.NetworkModeTun2Socks
	desired.Network.ProxySecretRef = "local-proxy"
	desired.Network.MediatedResolver = "1.1.1.1"
	desired.Env.Public["FIRST"] = "visible-value"
	desired.Env.Public["SECOND"] = "other-value"
	desired.Env.Inherit = []string{"LANG"}
	desired.Env.Deny = []string{"*_TOKEN"}
	desired.Activity = &profile.ActivityConfig{
		Retention: profile.ActivityRetention{
			MaxBytes: 268435456, MaxAgeSeconds: 86400,
		},
	}
	projection := manager.ProfileProjection{
		Schema:  manager.ProfileProjectionSchema,
		Profile: "default", Revision: 9,
		ContentDigest: "sha256:" + strings.Repeat("a", 64),
		Desired:       desired,
		Effective: manager.ProfileEffective{
			Status: manager.EffectiveCurrent,
			Network: &manager.EffectiveNetwork{
				Mode: "direct", DNS: "system", ObservedAt: now,
			},
			Sessions: []manager.EffectiveSessionSnapshot{
				{
					SessionID:       "ses_current",
					SnapshotID:      "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
					ProfileRevision: 9, Current: true,
				},
				{
					SessionID:       "ses_old",
					SnapshotID:      "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
					ProfileRevision: 8, Current: false,
				},
			},
		},
		Transition: &manager.ProfileTransition{
			OperationID: "op_transition0001",
			Kind:        "network", Phase: "activating",
			Blockers:  []string{"one accepted connection keeps its old route"},
			StartedAt: now,
		},
		UpdatedAt: now,
	}
	capabilities := make([]liveconsole.CapabilityProjection, 0)
	for _, capability := range manager.DefaultConfigurationCapabilities(true) {
		capabilities = append(capabilities, liveconsole.CapabilityProjection{
			ID:         capability.ID,
			Status:     workloadtypes.CoverageAvailable,
			Provider:   capability.Provider,
			Mutable:    capability.Mutable,
			ActionRefs: append([]string(nil), capability.ActionRefs...),
		})
	}
	owner, err := workloadtypes.NewReusableOwner(
		"env_current",
		"lima",
		"hideout-current:1:01234567-89ab-cdef-0123-456789abcdef",
	)
	if err != nil {
		panic(err)
	}
	return liveconsole.State{
		Version:              liveconsole.SeedVersionV2,
		DaemonInstanceID:     "daemon_fixture",
		CredentialGeneration: 2,
		LastSeq:              3,
		ProfileScope:         "default",
		Profiles:             []manager.ProfileProjection{projection},
		Overview: manager.Overview{
			Version: "hideout.manager/v1",
			Sessions: []manager.SessionSummary{{
				ID: "ses_current", EnvironmentID: "env_current",
				Profile: "default",
			}},
			Environments: []manager.EnvironmentSummary{{
				ID: "env_current", Profile: "default",
			}},
		},
		ActivityRetention: []manager.OperatorActivityRetentionProjection{{
			Owner: owner, UsedBytes: 48 << 20, LimitBytes: 256 << 20,
			MaxAgeSeconds: 24 * 60 * 60, Reasons: []string{},
		}},
		ActivityStoreRetention: &manager.OperatorActivityStoreRetentionProjection{
			UsedBytes: 320 << 20, LimitBytes: 1 << 30,
			DefaultOwnerLimitBytes: 256 << 20,
			ActiveSegmentBytes:     8 << 20,
			Owners:                 3, Segments: 12,
		},
		Capabilities: capabilities,
		StreamHealth: liveconsole.StreamHealth{
			State: liveconsole.HealthLive,
		},
	}
}
