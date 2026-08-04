package manager

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestMigrationExportSurfaceGoldenIsOneValidImmutableManagerPlan(t *testing.T) {
	encoded, err := os.ReadFile("../migration/testdata/export-plan-surface-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var plan migration.ExportPlan
	if err := json.Unmarshal(encoded, &plan); err != nil {
		t.Fatal(err)
	}
	provided := plan.PlanDigest
	plan.PlanDigest = ""
	if err := SealMigrationExportPlan(&plan); err != nil {
		t.Fatal(err)
	}
	if plan.PlanDigest != provided {
		t.Fatalf("surface golden plan digest=%s want=%s", provided, plan.PlanDigest)
	}
	if err := VerifyMigrationExportPlan(plan); err != nil {
		t.Fatal(err)
	}
}

// The direct migration API is consumed by the CLI, while the operator
// snapshot is consumed by the TUI and WebUI. Keep both surfaces byte-equivalent
// at the Manager boundary, including the durable terminal receipt.
func TestMigrationCLITUIAndWebUseOneTerminalProjection(t *testing.T) {
	operation := migrationSurfaceTerminalOperation(t)
	store := MigrationStore{Root: t.TempDir()}
	writeMigrationOperationFixture(t, store, operation)
	if _, created, err := store.EnsureTerminalEvidence(operation.ID); err != nil || !created {
		t.Fatalf("publish terminal evidence created=%t err=%v", created, err)
	}
	now := operation.UpdatedAt.Add(time.Second)

	directResponse := httptest.NewRecorder()
	api := API{
		Now: func() time.Time { return now },
		Migrations: &MigrationAPIService{
			Service: MigrationService{Store: store},
		},
	}
	api.serveMigrationOperation(directResponse, operation.ID)
	if directResponse.Code != 200 {
		t.Fatalf("direct migration status=%d body=%s", directResponse.Code, directResponse.Body.String())
	}
	var direct struct {
		Version  string                       `json:"version"`
		Resource string                       `json:"resource"`
		Data     MigrationOperationProjection `json:"data"`
		Errors   []string                     `json:"errors"`
	}
	if err := json.Unmarshal(directResponse.Body.Bytes(), &direct); err != nil {
		t.Fatal(err)
	}
	if direct.Version != APIVersion || direct.Resource != "migration/operation" ||
		len(direct.Errors) != 0 || direct.Data.TerminalReceipt == nil {
		t.Fatalf("direct migration envelope=%+v", direct)
	}

	snapshotService := OperatorSnapshotService{
		Core: Core{Store: profile.Store{Root: store.Root}},
		Overview: OperatorOverviewProviderFunc(func(context.Context) (Overview, error) {
			return Overview{Version: "hideout.manager/v1"}, nil
		}),
		OperationHistory: OperatorOperationProviderFunc(func(int) ([]Operation, error) {
			return []Operation{}, nil
		}),
		Now: func() time.Time { return now },
	}
	snapshot, err := snapshotService.Build(
		context.Background(), OperatorSnapshotQuery{ActivityLimit: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Migrations) != 1 ||
		!reflect.DeepEqual(snapshot.Migrations[0], direct.Data) {
		t.Fatalf("direct=%+v snapshot=%+v", direct.Data, snapshot.Migrations)
	}
	directJSON, err := json.Marshal(direct.Data)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON, err := json.Marshal(snapshot.Migrations[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(directJSON) != string(snapshotJSON) {
		t.Fatalf("surface projection drift\ndirect=%s\nsnapshot=%s", directJSON, snapshotJSON)
	}
	for _, forbidden := range []string{
		"planDigest", "provider", "secretInputHandle", "passphrase", "/Users/",
	} {
		if strings.Contains(string(directJSON), forbidden) {
			t.Fatalf("shared migration projection exposed %q: %s", forbidden, directJSON)
		}
	}
}

func migrationSurfaceTerminalOperation(t *testing.T) MigrationOperation {
	t.Helper()
	operation := migrationVerifiedImportOperationFixture(t)
	committing, _, err := operation.Decide(
		MigrationDecisionCommit, operation.UpdatedAt.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	for index := range committing.Effects {
		committing.Effects[index].Status = MigrationEffectSucceeded
	}
	committing.Phase = MigrationPhaseComplete
	committing.Revision++
	committing.UpdatedAt = committing.UpdatedAt.Add(time.Second)
	for index := range committing.Effects {
		if committing.Effects[index].Kind != MigrationEffectActivate {
			continue
		}
		committing.Effects[index].Evidence = []MigrationEffectEvidence{{
			Code:       migrationDestinationActivationEvidenceCode,
			OpaqueRef:  committing.DestinationStage.StageHandle,
			Digest:     migration.Digest("sha256:" + strings.Repeat("9", 64)),
			Count:      uint64(len(committing.ImportObjects)),
			ObservedAt: committing.UpdatedAt,
		}}
	}
	committing.Recovery = MigrationRecovery{
		Code: "migration.import.complete", Action: MigrationRecoveryNone,
	}
	committing.Result = &MigrationOperationResult{
		Code:          "migration.import.complete",
		ReceiptDigest: migration.Digest("sha256:" + strings.Repeat("9", 64)),
	}
	committing.Progress = MigrationProgress{
		LogicalTotalKnown: true, CompletedLogicalBytes: 100, TotalLogicalBytes: 100,
		ComponentsComplete: 2, ComponentsTotal: 2,
		PhaseStartedAt: operation.CreatedAt, CheckpointAt: committing.UpdatedAt,
	}
	if err := committing.Validate(); err != nil {
		t.Fatal(err)
	}
	return committing
}
