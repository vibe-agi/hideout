package daemon

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/migration"
)

const maxStartupMigrationOperations = 4096

// migrationWorkerSet owns process-local execution only. Every authoritative
// input, phase, checkpoint, effect, and terminal result is persisted by the
// Manager services before it becomes observable here.
type migrationWorkerSet struct {
	service manager.MigrationService
	imports manager.MigrationImportService
	steps   migrationImportWorkerSteps
	audit   *auditLog
	bus     *eventBus

	actionMu sync.Mutex
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	wait     sync.WaitGroup
	stopping bool
	running  map[string]*migrationWorker
}

type migrationWorker struct {
	kind   string
	cancel context.CancelFunc
	done   chan struct{}
}

// migrationImportWorkerSteps keeps the daemon orchestration boundary small and
// testable. Production steps delegate to Manager, which owns every durable
// phase/effect transition; the daemon only decides which idempotent step is
// eligible after a restart.
type migrationImportWorkerSteps struct {
	load        func(string) (manager.MigrationOperation, error)
	materialize func(
		context.Context,
		manager.MigrationImportMaterializeRequest,
	) (manager.MigrationOperation, error)
	adopt func(
		context.Context,
		manager.MigrationImportAdoptRequest,
	) (manager.MigrationOperation, error)
	verify func(
		context.Context,
		manager.MigrationImportVerifyRequest,
	) (manager.MigrationOperation, error)
	commit func(
		context.Context,
		manager.MigrationImportCommitRequest,
	) (manager.MigrationOperation, error)
	rollback func(
		context.Context,
		manager.MigrationImportRollbackRequest,
	) (manager.MigrationOperation, error)
}

type migrationStartupDisposition string

const (
	migrationStartupPublishTerminal    migrationStartupDisposition = "publish-terminal"
	migrationStartupResumeCancellation migrationStartupDisposition = "resume-cancellation"
	migrationStartupWaitForOperator    migrationStartupDisposition = "wait-for-operator"
	migrationStartupResumeImport       migrationStartupDisposition = "resume-import"
	migrationStartupNeedsSecret        migrationStartupDisposition = "needs-secret"
)

func newMigrationWorkerSet(
	service manager.MigrationService,
	imports manager.MigrationImportService,
	audit *auditLog,
	bus *eventBus,
) *migrationWorkerSet {
	ctx, cancel := context.WithCancel(context.Background())
	return &migrationWorkerSet{
		service: service, imports: imports, steps: newMigrationImportWorkerSteps(imports),
		audit: audit, bus: bus,
		ctx: ctx, cancel: cancel, running: make(map[string]*migrationWorker),
	}
}

func newMigrationImportWorkerSteps(
	imports manager.MigrationImportService,
) migrationImportWorkerSteps {
	return migrationImportWorkerSteps{
		load: imports.Store.Load,
		materialize: func(
			ctx context.Context,
			request manager.MigrationImportMaterializeRequest,
		) (manager.MigrationOperation, error) {
			operation, _, err := imports.MaterializeImportDestination(ctx, request)
			return operation, err
		},
		adopt: func(
			ctx context.Context,
			request manager.MigrationImportAdoptRequest,
		) (manager.MigrationOperation, error) {
			operation, _, err := imports.AdoptImportDestination(ctx, request)
			return operation, err
		},
		verify: func(
			ctx context.Context,
			request manager.MigrationImportVerifyRequest,
		) (manager.MigrationOperation, error) {
			operation, _, err := imports.VerifyImportDestination(ctx, request)
			return operation, err
		},
		commit: func(
			ctx context.Context,
			request manager.MigrationImportCommitRequest,
		) (manager.MigrationOperation, error) {
			operation, _, err := imports.CommitImportDestination(ctx, request)
			return operation, err
		},
		rollback: imports.RollbackImportDestination,
	}
}

func (workers *migrationWorkerSet) Resume(
	operationID string,
	request manager.MigrationOperationActionAPIRequest,
	clientBinding string,
) error {
	if workers == nil || workers.service.SecretInputs == nil ||
		request.SecretInputHandle == "" || request.RetainPartial != nil || request.Action != "" {
		return manager.ErrMigrationRequestInvalid
	}
	workers.actionMu.Lock()
	defer workers.actionMu.Unlock()
	operation, err := workers.service.Store.Load(operationID)
	if err != nil {
		return err
	}
	if operation.Revision != request.Revision {
		return manager.ErrMigrationStoreRevision
	}
	if operation.Recovery.Action != manager.MigrationRecoveryResume {
		return manager.ErrMigrationOperationInvalid
	}
	purpose := manager.MigrationSecretPurposeExportResume
	if operation.Kind == manager.MigrationOperationImport {
		purpose = manager.MigrationSecretPurposeImport
	} else if operation.Kind != manager.MigrationOperationExport {
		return manager.ErrMigrationOperationInvalid
	}
	binding, err := workers.service.SecretInputs.ResolveBinding(
		manager.MigrationSecretInputLookup{
			Handle: request.SecretInputHandle, Purpose: purpose,
			ClientBinding: clientBinding,
		},
	)
	if err != nil {
		return err
	}
	if binding.Handle.BundleID != operation.Bundle.BundleID || binding.BundleFile == nil {
		return manager.ErrMigrationSecretInputMismatch
	}
	if operation.Kind == manager.MigrationOperationImport {
		if operation.BundleFile == nil || *operation.BundleFile != *binding.BundleFile {
			return manager.ErrMigrationSecretInputMismatch
		}
		return workers.StartImport(manager.MigrationImportWorkerRequest{
			OperationID: operation.ID, SecretInputHandle: request.SecretInputHandle,
			ClientBinding: clientBinding,
		})
	}
	return workers.StartExport(manager.MigrationExportWorkerRequest{
		OperationID: operation.ID, SecretInputHandle: request.SecretInputHandle,
		SecretPurpose: manager.MigrationSecretPurposeExportResume,
		BundleFile:    binding.BundleFile, ClientBinding: clientBinding,
	})
}

func (workers *migrationWorkerSet) Cancel(
	operationID string,
	request manager.MigrationOperationActionAPIRequest,
	_ string,
) error {
	if workers == nil || request.SecretInputHandle != "" || request.Action != "" {
		return manager.ErrMigrationRequestInvalid
	}
	workers.actionMu.Lock()
	defer workers.actionMu.Unlock()
	operation, err := workers.service.Store.RequestCancellation(
		operationID, request.Revision, request.RetainPartial,
	)
	if err != nil {
		return err
	}
	workers.mu.Lock()
	running := workers.running[operation.ID]
	if running != nil {
		running.cancel()
	}
	workers.mu.Unlock()
	if running != nil {
		return nil
	}
	return workers.start(operation.ID, string(operation.Kind), func(context.Context) error {
		return nil
	})
}

func (workers *migrationWorkerSet) Recover(
	operationID string,
	request manager.MigrationOperationActionAPIRequest,
	_ string,
) error {
	if workers == nil || request.SecretInputHandle != "" || request.RetainPartial != nil ||
		request.Action == "" {
		return manager.ErrMigrationRequestInvalid
	}
	workers.actionMu.Lock()
	defer workers.actionMu.Unlock()
	operation, err := workers.service.Store.Load(operationID)
	if err != nil {
		return err
	}
	if operation.Revision != request.Revision {
		return manager.ErrMigrationStoreRevision
	}
	if operation.Recovery.Action == manager.MigrationRecoveryNone ||
		request.Action != operation.Recovery.Action {
		return manager.ErrMigrationOperationInvalid
	}
	switch request.Action {
	case manager.MigrationRecoveryFinish:
		if operation.Kind != manager.MigrationOperationImport || operation.Decision == nil ||
			operation.Decision.Value != manager.MigrationDecisionCommit {
			return manager.ErrMigrationOperationInvalid
		}
		return workers.start(operation.ID, "import", func(ctx context.Context) error {
			_, _, err := workers.imports.CommitImportDestination(
				ctx, manager.MigrationImportCommitRequest{OperationID: operation.ID},
			)
			return err
		})
	case manager.MigrationRecoveryRollback:
		if operation.Kind != manager.MigrationOperationImport {
			return manager.ErrMigrationOperationInvalid
		}
		return workers.start(operation.ID, "import", func(ctx context.Context) error {
			_, err := workers.imports.RollbackImportDestination(
				ctx, manager.MigrationImportRollbackRequest{OperationID: operation.ID},
			)
			return err
		})
	case manager.MigrationRecoveryRemovePartial:
		if operation.Kind != manager.MigrationOperationExport || operation.Cancellation == nil {
			return manager.ErrMigrationOperationInvalid
		}
		return workers.start(operation.ID, "export", func(ctx context.Context) error {
			var err error
			if operation.Phase == manager.MigrationPhaseCancelled {
				_, err = workers.service.RemoveRetainedExportPartial(
					ctx, operation.ID, operation.Revision,
				)
			} else {
				_, err = workers.service.FinalizeExportCancellation(ctx, operation.ID)
			}
			return err
		})
	default:
		return manager.ErrMigrationOperationInvalid
	}
}

func (workers *migrationWorkerSet) StartExport(
	request manager.MigrationExportWorkerRequest,
) error {
	if workers == nil {
		return manager.ErrMigrationCapabilityUnavailable
	}
	operation, err := workers.service.Store.Load(request.OperationID)
	if err != nil {
		return err
	}
	if operation.Kind != manager.MigrationOperationExport {
		return manager.ErrMigrationOperationInvalid
	}
	if operation.Terminal() {
		return nil
	}
	return workers.start(operation.ID, "export", func(ctx context.Context) error {
		_, snapshot, err := workers.service.SnapshotExportSource(ctx, operation.ID)
		if err != nil {
			return err
		}
		request.Snapshot = snapshot
		_, err = workers.service.WriteAndSealExportBundle(ctx, request)
		return err
	})
}

func (workers *migrationWorkerSet) StartImport(
	request manager.MigrationImportWorkerRequest,
) error {
	if workers == nil {
		return manager.ErrMigrationCapabilityUnavailable
	}
	operation, err := workers.service.Store.Load(request.OperationID)
	if err != nil {
		return err
	}
	if operation.Kind != manager.MigrationOperationImport {
		return manager.ErrMigrationOperationInvalid
	}
	if operation.Terminal() {
		return nil
	}
	return workers.start(operation.ID, "import", func(ctx context.Context) error {
		return workers.runImport(ctx, request)
	})
}

func (workers *migrationWorkerSet) runImport(
	ctx context.Context,
	request manager.MigrationImportWorkerRequest,
) error {
	steps := workers.steps
	if steps.load == nil {
		steps = newMigrationImportWorkerSteps(workers.imports)
	}
	operation, err := steps.load(request.OperationID)
	if err != nil {
		return err
	}
	if operation.Decision != nil &&
		operation.Decision.Value == manager.MigrationDecisionCommit {
		_, err = steps.commit(
			ctx, manager.MigrationImportCommitRequest{OperationID: operation.ID},
		)
		return err
	}
	if operation.Decision != nil &&
		operation.Decision.Value == manager.MigrationDecisionRollback {
		_, err = steps.rollback(
			ctx, manager.MigrationImportRollbackRequest{OperationID: operation.ID},
		)
		return err
	}
	if operation.DestinationStage == nil ||
		operation.Phase == manager.MigrationPhaseClaiming ||
		operation.Phase == manager.MigrationPhaseMaterializing ||
		operation.Phase == manager.MigrationPhaseRecoverableFailure ||
		(operation.Phase == manager.MigrationPhasePreparingSecrets &&
			len(operation.SecretActions) != 0 && len(operation.PreparedSecrets) == 0) {
		operation, err = steps.materialize(
			ctx,
			manager.MigrationImportMaterializeRequest{
				OperationID: operation.ID, SecretInputHandle: request.SecretInputHandle,
				ClientBinding: request.ClientBinding,
			},
		)
		if err != nil {
			return err
		}
	}
	if operation.Terminal() {
		return nil
	}
	operation, err = steps.adopt(
		ctx, manager.MigrationImportAdoptRequest{OperationID: operation.ID},
	)
	if err != nil {
		return err
	}
	operation, err = steps.verify(
		ctx, manager.MigrationImportVerifyRequest{OperationID: operation.ID},
	)
	if err != nil {
		return err
	}
	_, err = steps.commit(
		ctx, manager.MigrationImportCommitRequest{OperationID: operation.ID},
	)
	return err
}

func (workers *migrationWorkerSet) start(
	operationID string,
	kind string,
	run func(context.Context) error,
) error {
	if run == nil {
		return manager.ErrMigrationRequestInvalid
	}
	workers.mu.Lock()
	if workers.stopping {
		workers.mu.Unlock()
		return errors.New("migration workers are stopping")
	}
	if _, exists := workers.running[operationID]; exists {
		workers.mu.Unlock()
		return nil
	}
	workerContext, cancel := context.WithCancel(workers.ctx)
	worker := &migrationWorker{kind: kind, cancel: cancel, done: make(chan struct{})}
	workers.running[operationID] = worker
	workers.wait.Add(1)
	workers.mu.Unlock()
	workers.publish(operationID, kind, "queued", "")

	go func() {
		defer workers.wait.Done()
		defer cancel()
		workers.publish(operationID, kind, "running", "")
		err := run(workerContext)
		operation, loadErr := workers.service.Store.Load(operationID)
		if loadErr == nil && operation.Cancellation != nil &&
			(operation.Phase == manager.MigrationPhaseCancelling ||
				operation.Phase == manager.MigrationPhaseRecoverableFailure) {
			if operation.Kind == manager.MigrationOperationExport {
				_, err = workers.service.FinalizeExportCancellation(workers.ctx, operationID)
			} else {
				_, err = workers.imports.RollbackImportDestination(
					workers.ctx,
					manager.MigrationImportRollbackRequest{OperationID: operationID},
				)
			}
		}
		if terminalErr := workers.publishTerminalEvidence(operationID); terminalErr != nil {
			err = errors.Join(err, terminalErr)
		}
		if err != nil {
			workers.markRecoverable(operationID)
			workers.publish(
				operationID, kind, "recoverable-failure",
				manager.ProjectMigrationError(err).Code,
			)
		} else {
			workers.publish(operationID, kind, "complete", "")
		}
		workers.mu.Lock()
		delete(workers.running, operationID)
		close(worker.done)
		workers.mu.Unlock()
	}()
	return nil
}

func (workers *migrationWorkerSet) markRecoverable(operationID string) {
	operation, err := workers.service.Store.Load(operationID)
	if err != nil || operation.Terminal() ||
		operation.Phase == manager.MigrationPhaseRecoverableFailure {
		return
	}
	_, _ = workers.service.Store.TransitionPhase(
		operationID, manager.MigrationPhaseRecoverableFailure, nil,
	)
}

// ReconcileStartup never guesses a missing key. Import work after a durable
// stage can continue from operation-owned facts; work that still needs bundle
// bytes and every export wait for a newly purpose-bound secret handle.
func (workers *migrationWorkerSet) ReconcileStartup() error {
	if workers == nil {
		return nil
	}
	operations, err := workers.service.Store.List(maxStartupMigrationOperations)
	if err != nil {
		return err
	}
	for _, operation := range operations {
		switch migrationStartupDispositionForOperation(operation) {
		case migrationStartupPublishTerminal:
			if err := workers.publishTerminalEvidence(operation.ID); err != nil {
				return err
			}
		case migrationStartupResumeCancellation:
			if err := workers.start(
				operation.ID, string(operation.Kind), func(context.Context) error { return nil },
			); err != nil {
				return err
			}
		case migrationStartupWaitForOperator:
			continue
		case migrationStartupResumeImport:
			if err := workers.StartImport(manager.MigrationImportWorkerRequest{
				OperationID: operation.ID,
			}); err != nil {
				return err
			}
		case migrationStartupNeedsSecret:
			workers.markRecoverable(operation.ID)
			workers.publish(operation.ID, string(operation.Kind), "recoverable-failure", "migration.secret_input.required")
		default:
			return manager.ErrMigrationOperationInvalid
		}
	}
	return nil
}

func migrationStartupDispositionForOperation(
	operation manager.MigrationOperation,
) migrationStartupDisposition {
	if operation.Terminal() {
		return migrationStartupPublishTerminal
	}
	if operation.Cancellation != nil {
		return migrationStartupResumeCancellation
	}
	if operation.Phase == manager.MigrationPhaseRecoverableFailure {
		return migrationStartupWaitForOperator
	}
	if operation.Kind == manager.MigrationOperationImport && operation.DestinationStage != nil {
		if migrationImportHasUnpreparedSelectedSecret(operation) {
			return migrationStartupNeedsSecret
		}
		return migrationStartupResumeImport
	}
	return migrationStartupNeedsSecret
}

func migrationImportHasUnpreparedSelectedSecret(
	operation manager.MigrationOperation,
) bool {
	prepared := make(map[migration.OpaqueID]struct{}, len(operation.PreparedSecrets))
	for _, value := range operation.PreparedSecrets {
		prepared[value.SourceRef] = struct{}{}
	}
	for _, action := range operation.SecretActions {
		if action.Transfer != migration.SecretSelectedValue || action.ValueComponentID == "" {
			continue
		}
		if _, exists := prepared[action.SourceRef]; !exists {
			return true
		}
	}
	return false
}

func (workers *migrationWorkerSet) publishTerminalEvidence(operationID string) error {
	operation, err := workers.service.Store.Load(operationID)
	if err != nil {
		return err
	}
	if !operation.Terminal() || operation.Recovery.Action != manager.MigrationRecoveryNone {
		return nil
	}
	evidence, created, err := workers.service.Store.EnsureTerminalEvidence(operationID)
	if err != nil || !created {
		return err
	}
	if workers.audit != nil {
		decision := "allow"
		if evidence.Receipt.TerminalState == manager.MigrationPhaseFailed ||
			evidence.Receipt.TerminalState == manager.MigrationPhaseRolledBack {
			decision = "deny"
		}
		workers.audit.record(string(evidence.AuditEvent.Action), decision, map[string]any{
			"schema":                       evidence.AuditEvent.Schema,
			"operationId":                  evidence.AuditEvent.OperationID,
			"bundleId":                     evidence.AuditEvent.BundleID,
			"kind":                         evidence.AuditEvent.Kind,
			"terminalState":                evidence.AuditEvent.TerminalState,
			"resultCode":                   evidence.AuditEvent.ResultCode,
			"completedComponents":          evidence.AuditEvent.CompletedComponents,
			"totalComponents":              evidence.AuditEvent.TotalComponents,
			"replacements":                 evidence.AuditEvent.Replacements,
			"approvedAuthority":            evidence.AuditEvent.ApprovedAuthority,
			"disabledAuthorityProposalIds": evidence.AuditEvent.DisabledAuthorityProposalIDs,
		})
	}
	return nil
}

func (workers *migrationWorkerSet) Stop(ctx context.Context) {
	if workers == nil {
		return
	}
	workers.mu.Lock()
	if !workers.stopping {
		workers.stopping = true
		workers.cancel()
	}
	workers.mu.Unlock()
	done := make(chan struct{})
	go func() {
		workers.wait.Wait()
		close(done)
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func (workers *migrationWorkerSet) publish(
	operationID string,
	kind string,
	state string,
	code string,
) {
	if workers.bus != nil {
		workers.bus.publishBackground(operationID, "migration-"+kind, state)
	}
	if workers.audit != nil {
		fields := map[string]any{
			"operationId": operationID, "kind": kind, "state": state,
		}
		if code != "" {
			fields["code"] = code
		}
		decision := "allow"
		if state == "recoverable-failure" {
			decision = "deny"
		}
		workers.audit.record("migration.worker", decision, fields)
	}
}

func (workers *migrationWorkerSet) String() string {
	workers.mu.Lock()
	defer workers.mu.Unlock()
	return fmt.Sprintf("migration-workers(%d)", len(workers.running))
}
