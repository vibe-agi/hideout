package liveconsole

import (
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/manager"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
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
				EnvironmentID:      "env_fixture",
				WorkspaceID:        "wrk_" + strings.Repeat("a", 64),
				WorkspaceLabel:     "project [aaaaaaaa]",
				GuestWorkspace:     workspaceattach.LogicalWorkspaceRoot,
				WorkspaceTransport: workspaceattach.SelectedTransport,
				WorkspaceViewState: workspaceattach.AttachmentReady,
			},
		},
		Environments: []manager.OperatorEnvironmentProjection{{
			ID: "env_fixture", Name: "fixture", Profile: "default",
			Backend: "lima", Status: "running", Mode: environment.ModeShared,
			SharedSlot:        "default",
			MachineIdentityID: "sha256:" + strings.Repeat("d", 64),
			InstanceName:      "hideout-fixture", ActiveSessions: 2,
			ActiveWorkspaceViews: 1, WorkspaceProviderState: "ready",
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
		Risks:      []manager.RiskFinding{},
		Operations: []manager.Operation{},
		Migrations: []manager.MigrationOperationProjection{{
			Schema:      manager.MigrationOperationProjectionSchema,
			OperationID: "op_migrationconsole1", Revision: 1,
			BundleID: "migb_console1234", Kind: manager.MigrationOperationImport,
			State:      manager.MigrationPhaseMaterializing,
			PhaseLabel: "Copying persistent data",
			Progress: manager.MigrationProgressProjection{
				LogicalTotalKnown: true, CompletedLogicalBytes: 1,
				TotalLogicalBytes: 2, ComponentsComplete: 1, ComponentsTotal: 2,
				PhaseStartedAt: now, CheckpointAt: now,
				ElapsedSeconds: 1, RemainingKnown: true, RemainingSeconds: 1,
			},
			Recovery: manager.MigrationRecoveryProjection{
				Code:           "migration.operation.none",
				AllowedActions: []manager.MigrationRecoveryAction{},
			},
			Warnings: []manager.MigrationNotice{},
			Effects:  []manager.MigrationEffectProjection{},
		}},
		Capabilities: []manager.OperatorCapabilityProjection{},
		NextActions:  []string{"activity.inspect"},
	}

	state, err := NewStateFromOperatorSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Overview.Sessions) != 2 ||
		state.Overview.Sessions[0].ID != "ses_20260729T115900Z_new" ||
		state.Overview.Sessions[0].WorkspaceLabel != "project [aaaaaaaa]" ||
		state.Overview.Sessions[0].WorkspaceViewState != workspaceattach.AttachmentReady {
		t.Fatalf("newest session is not first: %+v", state.Overview.Sessions)
	}
	if len(state.Overview.Environments) != 1 ||
		state.Overview.Environments[0].ID != "env_fixture" ||
		state.Overview.Environments[0].ActiveSessions != 2 ||
		state.Overview.Environments[0].ActiveWorkspaceViews != 1 ||
		state.Overview.Environments[0].Mode != environment.ModeShared ||
		state.Overview.Environments[0].MachineIdentityID == "" ||
		state.Overview.Environments[0].WorkspaceProviderState != "ready" {
		t.Fatalf(
			"environment inventory was not preserved: %+v",
			state.Overview.Environments,
		)
	}
	if !state.ReadOnly || state.StreamHealth.State != HealthDaemonless {
		t.Fatalf("daemon-less state must be explicit and read-only: %+v", state)
	}
	if len(state.Migrations) != 1 ||
		state.Migrations[0].OperationID != "op_migrationconsole1" {
		t.Fatalf("migration projection was not preserved: %+v", state.Migrations)
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
