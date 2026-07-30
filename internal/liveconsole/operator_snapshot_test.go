package liveconsole

import (
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/manager"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestOperatorSnapshotStateStartsOnNewestSessionAndPreservesReadOnlyHealth(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	owner, err := workloadtypes.NewReusableOwner(
		"env_fixture",
		"lima",
		"hideout-fixture:1:01234567-89ab-cdef-0123-456789abcdef",
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := manager.OperatorSnapshot{
		Schema:               manager.OperatorSnapshotSchema,
		GeneratedAt:          now,
		InstanceID:           "daemon_fixture",
		CredentialGeneration: 1,
		Sequence:             0,
		StreamHealth: manager.OperatorStreamHealth{
			State: manager.OperatorHealthDaemonless,
		},
		Profiles: []manager.ProfileProjection{},
		Sessions: []manager.OperatorSessionProjection{
			{
				ID: "ses_20260729T115800Z_old", Profile: "default",
				State: "running", Command: "old", StartedAt: now.Add(-2 * time.Minute),
			},
			{
				ID: "ses_20260729T115900Z_new", Profile: "default",
				State: "running", Command: "new", StartedAt: now.Add(-time.Minute),
			},
		},
		Environments: []manager.OperatorEnvironmentProjection{{
			ID: "env_fixture", Name: "fixture", Profile: "default",
			Backend: "lima", Status: "running",
			InstanceName: "hideout-fixture", ActiveSessions: 2,
			OwnerHealth: "live", CreatedAt: now.Add(-time.Hour),
		}},
		Activity: []manager.ActivityProjection{},
		Coverage: []manager.CoverageProjection{},
		ActivityRetention: []manager.OperatorActivityRetentionProjection{{
			Owner: owner, UsedBytes: 1024, LimitBytes: 4096,
			MaxAgeSeconds: 3600, Reasons: []string{},
		}},
		ActivityStoreRetention: &manager.OperatorActivityStoreRetentionProjection{
			UsedBytes: 1024, LimitBytes: 1 << 30,
			DefaultOwnerLimitBytes: 256 << 20,
			ActiveSegmentBytes:     8 << 20,
			Owners:                 1, Segments: 1,
		},
		Risks:        []manager.RiskFinding{},
		Operations:   []manager.Operation{},
		Capabilities: []manager.OperatorCapabilityProjection{},
		NextActions:  []string{"activity.inspect"},
	}

	state, err := NewStateFromOperatorSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Overview.Sessions) != 2 ||
		state.Overview.Sessions[0].ID != "ses_20260729T115900Z_new" {
		t.Fatalf("newest session is not first: %+v", state.Overview.Sessions)
	}
	if len(state.Overview.Environments) != 1 ||
		state.Overview.Environments[0].ID != "env_fixture" ||
		state.Overview.Environments[0].ActiveSessions != 2 {
		t.Fatalf(
			"environment inventory was not preserved: %+v",
			state.Overview.Environments,
		)
	}
	if !state.ReadOnly || state.StreamHealth.State != HealthDaemonless {
		t.Fatalf("daemon-less state must be explicit and read-only: %+v", state)
	}
	if len(state.ActivityRetention) != 1 ||
		state.ActivityRetention[0].MaxAgeSeconds != 3600 ||
		state.ActivityStoreRetention == nil ||
		state.ActivityStoreRetention.LimitBytes != 1<<30 {
		t.Fatalf("retention projections were not preserved: %+v", state)
	}
}

func TestOperationHistoryNextActionUsesAnImplementedSurface(t *testing.T) {
	action := consoleNextAction("doctor.operations")
	if action.ID != "doctor.operations" ||
		action.Label != "inspect operation history" ||
		action.Command != "hideout tui" {
		t.Fatalf("operation history action is not executable: %+v", action)
	}
}
