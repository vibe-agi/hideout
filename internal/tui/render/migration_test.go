package render

import (
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
)

func TestMigrationHUDUsesConcreteUnitsUnknownsAndActionableRecovery(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	state := liveconsole.State{
		StreamHealth: liveconsole.StreamHealth{State: liveconsole.HealthLive},
		Capabilities: []liveconsole.CapabilityProjection{{
			ID: "migration.manage", Status: "available", Mutable: true,
		}},
		Migrations: []manager.MigrationOperationProjection{{
			Schema:      manager.MigrationOperationProjectionSchema,
			OperationID: "op_hudmigration1234", Revision: 4,
			BundleID: "migb_hudmigration1234", Kind: manager.MigrationOperationImport,
			State:      manager.MigrationPhaseRecoverableFailure,
			PhaseLabel: "Waiting for recovery",
			Progress: manager.MigrationProgressProjection{
				CompletedLogicalBytes: 2 << 20, CompletedEncodedBytes: 1 << 20,
				ComponentsComplete: 1, CurrentItem: "environment 1 of 2",
				PhaseStartedAt: now.Add(-65 * time.Second), CheckpointAt: now,
				ElapsedSeconds: 65,
			},
			Recovery: manager.MigrationRecoveryProjection{
				Required: true, Code: "migration.operation.resume",
				AllowedActions: []manager.MigrationRecoveryAction{
					manager.MigrationRecoveryResume,
				},
				NextAction: "Resume the same migration operation.",
			},
			Effects: []manager.MigrationEffectProjection{{
				Kind:   manager.MigrationEffectStage,
				Status: manager.MigrationEffectRunning,
			}},
			Warnings:      []manager.MigrationNotice{},
			LastErrorCode: "migration.operation.resume",
		}},
	}

	wide := Migration(MigrationInput{
		State: state, DetailOpen: true,
	}, Options{Width: 120, Height: 30, NoColor: true})
	for _, want := range []string{
		"INVENTORY", "PROGRESS", "ACTION",
		"2.0 MiB / total unknown", "Components 1 / total unknown",
		"Elapsed 1m05s", "ETA unknown", "environment 1 of 2",
		"migration.operation.resume", "Resume the same migration oper",
	} {
		if !strings.Contains(wide, want) {
			t.Fatalf("wide migration HUD missing %q:\n%s", want, wide)
		}
	}
	for _, forbidden := range []string{"password", "/Users/", "providerHandle", "planDigest"} {
		if strings.Contains(wide, forbidden) {
			t.Fatalf("wide migration HUD leaked %q:\n%s", forbidden, wide)
		}
	}

	narrow := Migration(MigrationInput{
		State: state, DetailOpen: true,
	}, Options{Width: 44, Height: 12, NoColor: true})
	for _, want := range []string{
		"Waiting for recovery", "total unknown", "ETA unknown", "NEXT",
	} {
		if !strings.Contains(narrow, want) {
			t.Fatalf("narrow migration HUD missing %q:\n%s", want, narrow)
		}
	}
}

func TestMigrationRowsRejectInvalidUnredactedProjection(t *testing.T) {
	state := liveconsole.State{Migrations: []manager.MigrationOperationProjection{{
		Schema:      manager.MigrationOperationProjectionSchema,
		OperationID: "op_invalidmigration1", Revision: 1,
		BundleID: "migb_invalidmigration1", Kind: manager.MigrationOperationImport,
		State:      manager.MigrationPhaseMaterializing,
		PhaseLabel: "Copying persistent data",
		Progress: manager.MigrationProgressProjection{
			CurrentItem: "socks5://user:password@example.test",
		},
		Recovery: manager.MigrationRecoveryProjection{
			Code:           "migration.operation.none",
			AllowedActions: []manager.MigrationRecoveryAction{},
		},
	}}}
	if rows := MigrationRows(MigrationInput{State: state}); len(rows) != 0 {
		t.Fatalf("invalid migration projection reached HUD: %+v", rows)
	}
}
