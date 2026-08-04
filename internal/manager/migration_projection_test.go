package manager

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/migration"
)

func TestMigrationProjectionIsSharedConcreteAndSecretFree(t *testing.T) {
	operation := migrationImportOperationFixture()
	operation.Phase = MigrationPhaseMaterializing
	progress := MigrationProgress{
		LogicalTotalKnown: true, CompletedLogicalBytes: 25 << 20,
		TotalLogicalBytes: 100 << 20,
		EncodedTotalKnown: true, CompletedEncodedBytes: 10 << 20,
		TotalEncodedBytes:  40 << 20,
		ComponentsComplete: 1, ComponentsTotal: 4,
		CurrentItem:     "copy socks5://user:password@example.test /Users/alice/private HIDEOUT_SECRET_PROXY=value",
		PhaseStartedAt:  operation.CreatedAt,
		ActiveWorkNanos: int64(2 * time.Second),
		ActiveSince:     operation.CreatedAt.Add(8 * time.Second),
		CheckpointAt:    operation.CreatedAt.Add(7 * time.Second),
	}
	var err error
	operation, _, err = operation.WithProgress(
		progress, operation.CreatedAt.Add(8*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	warning, err := NewMigrationNotice(
		"migration.identity.opaque",
		"credential://alice:secret@example.test /Users/alice/license",
	)
	if err != nil {
		t.Fatal(err)
	}
	operation.Warnings = []MigrationNotice{warning}
	projection, err := ProjectMigrationOperation(
		operation, operation.CreatedAt.Add(10*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if projection.PhaseLabel != "Copying persistent data" ||
		projection.Revision != operation.Revision ||
		projection.Recovery.Required || len(projection.Recovery.AllowedActions) != 0 ||
		projection.Progress.ElapsedSeconds != 4 ||
		projection.Progress.ThroughputBytesPerSecond != 25<<20/4 ||
		!projection.Progress.RemainingKnown ||
		projection.Progress.RemainingSeconds != 12 {
		t.Fatalf("projection progress=%+v label=%q", projection.Progress, projection.PhaseLabel)
	}
	encoded, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"password", "secret@example", "/Users/alice", "HIDEOUT_SECRET_PROXY",
		"backend.lima", "sha256:",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("projection leaked %q: %s", forbidden, encoded)
		}
	}
	if !strings.Contains(projection.Progress.CurrentItem, "REDACTED") ||
		!strings.Contains(projection.Progress.CurrentItem, "[path]") {
		t.Fatalf("current item was not safely useful: %q", projection.Progress.CurrentItem)
	}
}

func TestMigrationProgressAndNoticeRejectUnredactedDurableText(t *testing.T) {
	operation := migrationImportOperationFixture()
	operation.Warnings = []MigrationNotice{{
		Code:    "migration.identity.opaque",
		Summary: "socks5://user:password@example.test /Users/alice/private",
	}}
	if err := operation.Validate(); !errors.Is(err, ErrMigrationOperationInvalid) {
		t.Fatalf("unredacted warning error=%v", err)
	}
	operation = migrationImportOperationFixture()
	operation.Progress = MigrationProgress{
		CurrentItem: "password=secret", PhaseStartedAt: operation.CreatedAt,
	}
	if err := operation.Validate(); !errors.Is(err, ErrMigrationProgressInvalid) {
		t.Fatalf("unredacted progress error=%v", err)
	}
}

func TestMigrationProgressCannotRegressOrClaimUnknownETA(t *testing.T) {
	operation := migrationImportOperationFixture()
	operation.Phase = MigrationPhaseMaterializing
	operation.Progress = MigrationProgress{
		LogicalTotalKnown: true, CompletedLogicalBytes: 10, TotalLogicalBytes: 100,
		ComponentsComplete: 1, ComponentsTotal: 2,
		PhaseStartedAt: operation.CreatedAt,
		CheckpointAt:   operation.CreatedAt,
	}
	operation.UpdatedAt = operation.CreatedAt
	regressed := operation.Progress
	regressed.CompletedLogicalBytes--
	if _, _, err := operation.WithProgress(
		regressed, operation.CreatedAt.Add(time.Second),
	); !errors.Is(err, ErrMigrationProgressInvalid) {
		t.Fatalf("regressed progress error=%v", err)
	}

	unknown := operation.Clone()
	unknown.Progress = MigrationProgress{
		CurrentItem: "waiting for provider size", PhaseStartedAt: operation.CreatedAt,
	}
	projection, err := ProjectMigrationOperation(
		unknown, operation.CreatedAt.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Progress.LogicalTotalKnown || projection.Progress.RemainingKnown {
		t.Fatalf("unknown total invented an ETA: %+v", projection.Progress)
	}
}

func TestMigrationTerminalReceiptAndAuditContainDecisionsNotSecrets(t *testing.T) {
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
		if committing.Effects[index].Kind == MigrationEffectActivate {
			committing.Effects[index].Evidence = []MigrationEffectEvidence{{
				Code:      migrationDestinationActivationEvidenceCode,
				OpaqueRef: committing.DestinationStage.StageHandle,
				Digest:    migration.Digest("sha256:" + strings.Repeat("9", 64)),
				Count:     uint64(len(committing.ImportObjects)), ObservedAt: committing.UpdatedAt,
			}}
		}
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
	committing.AuthorityActions = []migration.AuthorityAction{{
		ProposalID: "authority_network001", EnvironmentRef: "source_environment1",
		Class: "network", DestinationValue: `{"mode":"direct"}`, Approved: true,
	}}
	committing.DisabledProposals = []migration.OpaqueID{"authority_script0001"}
	if err := committing.Validate(); err != nil {
		t.Fatal(err)
	}
	receipt, err := BuildMigrationReceipt(committing)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Decision != MigrationDecisionCommit ||
		receipt.IdentityPolicies.SafeClone != 1 ||
		receipt.IdentityPolicies.ExactGuestRestore != 1 ||
		!receipt.AllEffectsSucceeded || len(receipt.ApprovedAuthority) != 1 ||
		receipt.ApprovedAuthority[0].ProposalID != "authority_network001" ||
		!slices.Equal(
			receipt.DisabledAuthorityProposalIDs,
			[]migration.OpaqueID{"authority_script0001"},
		) {
		t.Fatalf("receipt=%+v", receipt)
	}
	auditEvent, err := BuildMigrationAuditEvent(
		committing, MigrationAuditImportCommitted, committing.UpdatedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal([]any{receipt, auditEvent})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"passphrase", "secretInputHandle", "privateKey", "backend.lima",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("receipt/audit leaked %q: %s", forbidden, encoded)
		}
	}
	store := MigrationStore{Root: t.TempDir()}
	writeMigrationOperationFixture(t, store, committing)
	evidence, created, err := store.EnsureTerminalEvidence(committing.ID)
	if err != nil || !created {
		t.Fatalf("first terminal publication created=%t evidence=%+v err=%v", created, evidence, err)
	}
	replayed, created, err := store.EnsureTerminalEvidence(committing.ID)
	if err != nil || created || !reflect.DeepEqual(replayed, evidence) {
		t.Fatalf("terminal replay created=%t evidence=%+v err=%v", created, replayed, err)
	}
	loaded, err := store.LoadTerminalEvidence(committing.ID)
	if err != nil || !reflect.DeepEqual(loaded, evidence) {
		t.Fatalf("terminal evidence load=%+v err=%v", loaded, err)
	}
	info, err := os.Lstat(store.TerminalEvidencePath(committing.ID))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("terminal evidence mode=%v err=%v", info, err)
	}
}

func TestMigrationPublicErrorProjectionUsesOnlyStableCodes(t *testing.T) {
	cause := errors.New("socks5://user:password@127.0.0.1:7890 /Users/alice/private")
	providerErr := &backend.MigrationProviderError{
		Code: "migration.provider.snapshot_failed", Cause: cause,
		Retryable: true, RecoveryRequired: true,
	}
	projected := ProjectMigrationError(providerErr)
	if projected.Code != "migration.provider.snapshot_failed" ||
		!projected.Retryable || !projected.RecoveryRequired {
		t.Fatalf("provider projection=%+v", projected)
	}
	encoded, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "password") || strings.Contains(string(encoded), "/Users") {
		t.Fatalf("public error leaked cause: %s", encoded)
	}
	if got := ProjectMigrationError(ErrMigrationClaimConflict).Code; got != "migration.plan.stale" {
		t.Fatalf("claim conflict code=%q", got)
	}
}
