package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/recovery"
)

const (
	environmentActionPlanRecordSchema = "hideout.environment-action-plan-record.v1"
	environmentActionPlanDomain       = "environment-action-plan"
	environmentActionRecordDomain     = "environment-action-plan-record"
	environmentActionResultDomain     = "environment-action-result"
	maxEnvironmentActionRecordBytes   = 2 << 20
)

type EnvironmentActionExecutor func(
	context.Context,
	EnvironmentActionPlan,
) (EnvironmentActionResult, error)

type EnvironmentActionProver func(
	context.Context,
	string,
	environment.Record,
	EnvironmentActionTarget,
) ([]EvidenceRef, error)

// EnvironmentActionService binds stop and clean review, execution, proof, and
// response-loss recovery to the shared durable operation ledger.
type EnvironmentActionService struct {
	Core        Core
	Operations  OperationService
	Now         func() time.Time
	ApplyStop   EnvironmentActionExecutor
	ApplyClean  EnvironmentActionExecutor
	ApplyDelete EnvironmentActionExecutor
	Prove       EnvironmentActionProver
}

type environmentActionEffectBinding struct {
	EffectID      string `json:"effectId"`
	EnvironmentID string `json:"environmentId,omitempty"`
	Skipped       bool   `json:"skipped,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type environmentActionRecordSnapshot struct {
	EnvironmentID string             `json:"environmentId"`
	Record        environment.Record `json:"record"`
}

type environmentActionPlanRecord struct {
	Schema       string                            `json:"schema"`
	Plan         EnvironmentActionPlan             `json:"plan"`
	Effects      []environmentActionEffectBinding  `json:"effects"`
	Records      []environmentActionRecordSnapshot `json:"records"`
	RecordDigest string                            `json:"recordDigest"`
	Result       *EnvironmentActionResult          `json:"result,omitempty"`
	ResultDigest string                            `json:"resultDigest,omitempty"`
	CreatedAt    time.Time                         `json:"createdAt"`
}

type environmentActionPlanRecordAuthority struct {
	Schema  string                            `json:"schema"`
	Plan    EnvironmentActionPlan             `json:"plan"`
	Effects []environmentActionEffectBinding  `json:"effects"`
	Records []environmentActionRecordSnapshot `json:"records"`
}

type environmentActionTerminalError struct {
	code    string
	summary string
}

func (err *environmentActionTerminalError) Error() string {
	if err == nil {
		return ErrOperationTerminalUnproved.Error()
	}
	return "code=" + err.code + ": " + err.summary
}

func (service *EnvironmentActionService) Prepare(
	action string,
	options EnvironmentActionOptions,
) (EnvironmentActionPlan, error) {
	if service == nil {
		return EnvironmentActionPlan{}, errors.New(
			"environment action service is unavailable",
		)
	}
	var (
		plan EnvironmentActionPlan
		err  error
	)
	switch action {
	case EnvironmentActionStop:
		plan, err = service.Core.PlanEnvironmentStop(options)
	case EnvironmentActionClean:
		plan, err = service.Core.PlanEnvironmentClean(options)
	case EnvironmentActionDelete:
		plan, err = service.Core.PlanEnvironmentClean(options)
		plan.Action = EnvironmentActionDelete
		plan.Force = options.Force
	default:
		return EnvironmentActionPlan{}, errors.New(
			"environment action is invalid",
		)
	}
	if err != nil {
		return plan, fmt.Errorf("plan environment action: %w", err)
	}
	if action != EnvironmentActionDelete && options.Force {
		return plan, errors.New(
			"force is valid only for an environment delete plan",
		)
	}
	operationID, err := NewOperationID()
	if err != nil {
		return EnvironmentActionPlan{}, fmt.Errorf(
			"allocate environment operation identity: %w", err,
		)
	}
	plan.OperationID = operationID
	plan.PlanDigest = ""
	plan.PlanDigest, err = environmentActionPlanDigest(plan)
	if err != nil {
		return EnvironmentActionPlan{}, fmt.Errorf(
			"digest environment action plan: %w", err,
		)
	}
	record, operationEffects, err := service.planRecord(plan)
	if err != nil {
		return EnvironmentActionPlan{}, fmt.Errorf(
			"capture environment action authority: %w", err,
		)
	}
	binding := environmentActionOperationBinding(plan)
	operationStore := service.operationStore()
	if _, created, err := operationStore.Reserve(
		binding, operationEffects,
	); err != nil {
		return EnvironmentActionPlan{}, fmt.Errorf(
			"reserve environment operation: %w", err,
		)
	} else if !created {
		return EnvironmentActionPlan{}, ErrOperationMismatch
	}
	if err := service.writePlanRecord(record, false); err != nil {
		_, _ = service.operations().Terminal(
			plan.OperationID,
			OperationCancelled,
			"plan-record-unavailable",
			"The environment action plan could not be retained.",
		)
		return EnvironmentActionPlan{}, fmt.Errorf(
			"persist environment action authority: %w", err,
		)
	}
	return plan, nil
}

func (service *EnvironmentActionService) Apply(
	ctx context.Context,
	action string,
	request EnvironmentActionAPIRequest,
) (EnvironmentActionResult, error) {
	if service == nil {
		return EnvironmentActionResult{}, errors.New(
			"environment action service is unavailable",
		)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return EnvironmentActionResult{}, err
	}
	if request.OperationID == "" && request.PlanDigest == "" {
		options, err := environmentActionOptionsFromAPIRequest(request)
		if err != nil {
			return EnvironmentActionResult{}, err
		}
		plan, err := service.Prepare(action, options)
		if err != nil {
			return EnvironmentActionResult{}, err
		}
		request.OperationID = plan.OperationID
		request.PlanDigest = plan.PlanDigest
		request.Confirmed = true
	} else if request.OperationID == "" || request.PlanDigest == "" {
		return EnvironmentActionResult{}, ErrOperationMismatch
	}
	if !request.Confirmed {
		return EnvironmentActionResult{}, errors.New(
			"environment action requires confirmation of the reviewed plan",
		)
	}

	release := acquireProfileTransactionApplyLock(
		service.Core.Store.Root + "\x00environment-action\x00" +
			request.OperationID,
	)
	defer release()
	record, err := service.loadPlanRecord(request.OperationID)
	if err != nil {
		return EnvironmentActionResult{}, err
	}
	if record.Plan.Action != action ||
		record.Plan.PlanDigest != request.PlanDigest ||
		!environmentActionRequestMatchesPlan(request, record.Plan) {
		return EnvironmentActionResult{}, ErrOperationMismatch
	}
	operation, err := service.operationStore().Load(request.OperationID)
	if err != nil {
		return EnvironmentActionResult{}, err
	}
	if !operation.Matches(environmentActionOperationBinding(record.Plan)) {
		return EnvironmentActionResult{}, ErrOperationMismatch
	}
	if operation.Terminal() {
		return terminalEnvironmentActionResult(record, operation)
	}
	return service.applyAccepted(ctx, record, operation)
}

func (service *EnvironmentActionService) ReconcileOperation(
	ctx context.Context,
	operationID string,
) (Operation, error) {
	if service == nil {
		return Operation{}, errors.New(
			"environment action service is unavailable",
		)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	release := acquireProfileTransactionApplyLock(
		service.Core.Store.Root + "\x00environment-action\x00" + operationID,
	)
	defer release()
	record, err := service.loadPlanRecord(operationID)
	if err != nil {
		return Operation{}, err
	}
	operation, err := service.operationStore().Load(operationID)
	if err != nil || operation.Terminal() {
		return operation, err
	}
	if !operation.Matches(environmentActionOperationBinding(record.Plan)) {
		return operation, ErrOperationMismatch
	}
	if operationEffectStatusPresent(operation, EffectPending) &&
		!operationEffectStatusPresent(operation, EffectRunning) {
		result, applyErr := service.applyAccepted(
			ctx,
			record,
			operation,
		)
		if result.Operation != nil {
			return *result.Operation, applyErr
		}
		current, loadErr := service.operationStore().Load(operation.ID)
		return current, errors.Join(applyErr, loadErr)
	}
	return service.proveAccepted(ctx, record, operation, nil)
}

func (service *EnvironmentActionService) ReconcilePending(
	ctx context.Context,
) error {
	operations, err := service.operationStore().List(defaultMaxOperations)
	if err != nil {
		return err
	}
	for _, operation := range operations {
		if operation.Terminal() || operation.Phase == OperationPlanned ||
			!IsEnvironmentActionOperationKind(operation.Kind) {
			continue
		}
		_, _ = service.ReconcileOperation(ctx, operation.ID)
	}
	return nil
}

func (service *EnvironmentActionService) applyAccepted(
	ctx context.Context,
	record environmentActionPlanRecord,
	operation Operation,
) (EnvironmentActionResult, error) {
	var err error
	switch operation.Phase {
	case OperationPlanned:
		operation, err = service.operations().Transition(
			operation.ID, OperationClaimed,
		)
		if err == nil {
			operation, err = service.operations().Transition(
				operation.ID, OperationStaging,
			)
		}
	case OperationClaimed:
		operation, err = service.operations().Transition(
			operation.ID, OperationStaging,
		)
	case OperationRecoveryRequired:
		operation, err = service.operations().Transition(
			operation.ID, OperationProving,
		)
	case OperationStaging, OperationProving:
	default:
		err = fmt.Errorf(
			"%w: environment action phase %s",
			ErrInvalidTransition, operation.Phase,
		)
	}
	if err != nil {
		return EnvironmentActionResult{Plan: record.Plan}, err
	}

	executeTargets := make([]EnvironmentActionTarget, 0)
	for _, binding := range record.Effects {
		effect := operationEffect(operation, binding.EffectID)
		if effect == nil {
			return EnvironmentActionResult{Plan: record.Plan},
				ErrOperationMismatch
		}
		if binding.Skipped {
			if effect.Status == EffectSucceeded {
				continue
			}
			var execute bool
			operation, execute, err = service.operations().BeginEffect(
				operation.ID, binding.EffectID,
				environmentActionProvider(record.Plan.Action),
			)
			if err != nil {
				return EnvironmentActionResult{Plan: record.Plan}, err
			}
			if execute {
				operation, err = service.operations().FinishEffect(
					operation.ID, binding.EffectID,
					environmentActionProvider(record.Plan.Action),
					EffectSucceeded,
					[]EvidenceRef{{
						Code: "environment-action-skipped",
						Ref:  binding.EnvironmentID + ":" + binding.Reason,
					}},
				)
				if err != nil {
					return EnvironmentActionResult{Plan: record.Plan}, err
				}
			}
			continue
		}
		if effect.Status == EffectSucceeded {
			continue
		}
		var execute bool
		operation, execute, err = service.operations().BeginEffect(
			operation.ID, binding.EffectID,
			environmentActionProvider(record.Plan.Action),
		)
		if err != nil {
			return EnvironmentActionResult{Plan: record.Plan}, err
		}
		if execute {
			target, ok := environmentActionTarget(
				record.Plan.Targets, binding.EnvironmentID,
			)
			if !ok {
				return EnvironmentActionResult{Plan: record.Plan},
					ErrOperationMismatch
			}
			executeTargets = append(executeTargets, target)
		}
	}

	var applyErr error
	if len(executeTargets) != 0 {
		executor := service.executor(record.Plan.Action)
		if executor == nil {
			applyErr = errors.New(
				"environment action provider is unavailable",
			)
		} else {
			direct := record.Plan
			direct.Targets = append(
				[]EnvironmentActionTarget(nil), executeTargets...,
			)
			direct.Skipped = nil
			direct.Total = len(direct.Targets)
			_, applyErr = executor(ctx, direct)
		}
	}
	if applyErr != nil && len(executeTargets) == 1 &&
		EnvironmentRecoveryCode(applyErr) != "" {
		return service.finishRefused(
			record,
			operation,
			executeTargets[0],
			applyErr,
		)
	}
	if operation.Phase == OperationStaging {
		operation, err = service.operations().Transition(
			operation.ID, OperationProving,
		)
		if err != nil {
			return EnvironmentActionResult{Plan: record.Plan}, err
		}
	}
	return service.finishProof(ctx, record, operation, applyErr)
}

// finishRefused records a proved, side-effect-free ownership refusal as a
// terminal failed operation. It is deliberately limited to a single newly
// executed target: a multi-target provider may have completed earlier targets,
// so that case still enters proof/recovery instead of claiming atomic refusal.
func (service *EnvironmentActionService) finishRefused(
	record environmentActionPlanRecord,
	operation Operation,
	target EnvironmentActionTarget,
	cause error,
) (EnvironmentActionResult, error) {
	code := EnvironmentRecoveryCode(cause)
	if code == "" {
		return EnvironmentActionResult{Plan: record.Plan}, cause
	}
	var effectID string
	for _, binding := range record.Effects {
		if !binding.Skipped && binding.EnvironmentID == target.ID {
			effectID = binding.EffectID
			break
		}
	}
	if effectID == "" {
		return EnvironmentActionResult{Plan: record.Plan},
			errors.Join(cause, ErrOperationMismatch)
	}
	var err error
	operation, err = service.operations().FinishEffect(
		operation.ID,
		effectID,
		environmentActionProvider(record.Plan.Action),
		EffectFailed,
		[]EvidenceRef{{
			Code:       code,
			Ref:        target.ID,
			ObservedAt: service.nowUTC(),
		}},
	)
	if err != nil {
		return EnvironmentActionResult{Plan: record.Plan},
			errors.Join(cause, err)
	}
	summary := environmentActionRefusalSummary(code)
	operation, err = service.operations().Terminal(
		operation.ID,
		OperationFailed,
		code,
		summary,
	)
	if err != nil {
		return EnvironmentActionResult{Plan: record.Plan},
			errors.Join(cause, err)
	}
	return EnvironmentActionResult{
		Plan:      record.Plan,
		Applied:   []EnvironmentActionTarget{},
		Skipped:   append([]EnvironmentActionTarget(nil), record.Plan.Skipped...),
		Operation: &operation,
	}, cause
}

func environmentActionRefusalSummary(code string) string {
	switch code {
	case recovery.CodeEnvironmentActiveSessions:
		return "Active sessions still own the environment; exit them and retry the reviewed action."
	case recovery.CodeSessionOwnerUnprovable:
		return "Session ownership could not be proved absent; reconcile ownership before retrying."
	case recovery.CodeSessionCleanupFailed:
		return "Session ownership cleanup is incomplete; recover it before retrying."
	default:
		return "The environment action was safely refused before its destructive effect began."
	}
}

func (service *EnvironmentActionService) proveAccepted(
	ctx context.Context,
	record environmentActionPlanRecord,
	operation Operation,
	cause error,
) (Operation, error) {
	if operation.Phase == OperationRecoveryRequired {
		var err error
		operation, err = service.operations().Transition(
			operation.ID, OperationProving,
		)
		if err != nil {
			return operation, err
		}
	}
	result, err := service.finishProof(ctx, record, operation, cause)
	if result.Operation != nil {
		return *result.Operation, err
	}
	current, loadErr := service.operationStore().Load(operation.ID)
	if loadErr != nil {
		return operation, errors.Join(err, loadErr)
	}
	return current, err
}

func (service *EnvironmentActionService) finishProof(
	ctx context.Context,
	record environmentActionPlanRecord,
	operation Operation,
	cause error,
) (EnvironmentActionResult, error) {
	var proofFailed bool
	for _, binding := range record.Effects {
		if binding.Skipped {
			continue
		}
		effect := operationEffect(operation, binding.EffectID)
		if effect == nil {
			return EnvironmentActionResult{Plan: record.Plan},
				ErrOperationMismatch
		}
		if effect.Status == EffectSucceeded {
			continue
		}
		if effect.Status != EffectRunning || service.Prove == nil {
			proofFailed = true
			continue
		}
		snapshot, ok := environmentActionSnapshot(
			record.Records, binding.EnvironmentID,
		)
		target, targetOK := environmentActionTarget(
			record.Plan.Targets, binding.EnvironmentID,
		)
		if !ok || !targetOK {
			return EnvironmentActionResult{Plan: record.Plan},
				ErrOperationMismatch
		}
		evidence, proofErr := service.Prove(
			ctx, record.Plan.Action, snapshot, target,
		)
		if proofErr != nil || len(evidence) == 0 {
			proofFailed = true
			continue
		}
		var err error
		operation, err = service.operations().FinishEffect(
			operation.ID, binding.EffectID,
			environmentActionProvider(record.Plan.Action),
			EffectSucceeded, evidence,
		)
		if err != nil {
			return EnvironmentActionResult{Plan: record.Plan}, err
		}
	}
	if proofFailed {
		operation, err := service.operations().RequireRecovery(
			operation.ID,
			"lifecycle-terminal-unproved",
			"The environment action was accepted, but its terminal backend and metadata evidence is incomplete.",
			"Retry the same operation ID after backend inventory is available.",
		)
		if err != nil {
			return EnvironmentActionResult{Plan: record.Plan}, err
		}
		return EnvironmentActionResult{
				Plan: record.Plan, Operation: &operation,
			},
			fmt.Errorf(
				"%w: lifecycle-terminal-unproved",
				ErrOperationTerminalUnproved,
			)
	}

	result := environmentActionResultFromRecord(record)
	if err := service.storeResult(&record, result); err != nil {
		return EnvironmentActionResult{Plan: record.Plan}, err
	}
	code := environmentActionSuccessCode(record.Plan.Action)
	summary := "The environment action reached its proved terminal state."
	terminal, err := service.operations().Terminal(
		operation.ID, OperationSucceeded, code, summary,
	)
	if err != nil {
		return EnvironmentActionResult{Plan: record.Plan}, err
	}
	result.Operation = &terminal
	_ = cause // Provider text is deliberately neither persisted nor returned.
	return result, nil
}

func environmentActionSuccessCode(action string) string {
	switch action {
	case EnvironmentActionStop:
		return "environment-stopped"
	case EnvironmentActionClean:
		return "environment-cleaned"
	case EnvironmentActionDelete:
		return "environment-deleted"
	default:
		return "environment-action-completed"
	}
}

func (service *EnvironmentActionService) planRecord(
	plan EnvironmentActionPlan,
) (environmentActionPlanRecord, []EffectResult, error) {
	store := environment.Store{Root: service.Core.Store.Root}
	record := environmentActionPlanRecord{
		Schema:    environmentActionPlanRecordSchema,
		Plan:      cloneEnvironmentActionPlan(plan),
		Effects:   []environmentActionEffectBinding{},
		Records:   []environmentActionRecordSnapshot{},
		CreatedAt: service.nowUTC(),
	}
	effects := make([]EffectResult, 0, plan.Total+1)
	index := 0
	add := func(target EnvironmentActionTarget, skipped bool) error {
		effectID := "environment-" + strconv.Itoa(index)
		index++
		binding := environmentActionEffectBinding{
			EffectID: effectID, EnvironmentID: target.ID,
			Skipped: skipped, Reason: target.Reason,
		}
		record.Effects = append(record.Effects, binding)
		kind := "cleanup"
		if plan.Action == EnvironmentActionStop {
			kind = "drain"
		}
		if skipped {
			kind = "prove"
		}
		effects = append(effects, EffectResult{
			ID: effectID, Kind: kind,
			Provider: environmentActionProvider(plan.Action),
			Status:   EffectPending,
		})
		snapshot, err := store.Load(target.ID)
		if err != nil {
			return err
		}
		record.Records = append(
			record.Records,
			environmentActionRecordSnapshot{
				EnvironmentID: target.ID, Record: snapshot,
			},
		)
		return nil
	}
	for _, target := range plan.Targets {
		if err := add(target, false); err != nil {
			return environmentActionPlanRecord{}, nil, err
		}
	}
	for _, target := range plan.Skipped {
		if err := add(target, true); err != nil {
			return environmentActionPlanRecord{}, nil, err
		}
	}
	if len(effects) == 0 {
		record.Effects = append(record.Effects, environmentActionEffectBinding{
			EffectID: "environment-empty", Skipped: true,
			Reason: "selection-empty",
		})
		effects = append(effects, EffectResult{
			ID: "environment-empty", Kind: "prove",
			Provider: environmentActionProvider(plan.Action),
			Status:   EffectPending,
		})
	}
	var err error
	record.RecordDigest, err = environmentActionRecordDigest(record)
	return record, effects, err
}

func (service *EnvironmentActionService) executor(
	action string,
) EnvironmentActionExecutor {
	if action == EnvironmentActionStop {
		return service.ApplyStop
	}
	if action == EnvironmentActionClean {
		return service.ApplyClean
	}
	if action == EnvironmentActionDelete {
		return service.ApplyDelete
	}
	return nil
}

func (service *EnvironmentActionService) operations() OperationService {
	operations := service.Operations
	if operations.Store.Root == "" {
		operations.Store = service.operationStore()
	}
	if operations.Observer == nil {
		operations.Observer = service.Core.Observer
	}
	return operations
}

func (service *EnvironmentActionService) operationStore() OperationStore {
	store := service.Operations.Store
	if store.Root == "" {
		store.Root = service.Core.Store.Root
	}
	if store.Now == nil {
		store.Now = service.Now
	}
	return store
}

func (service *EnvironmentActionService) nowUTC() time.Time {
	if service.Now != nil {
		return service.Now().Round(0).UTC()
	}
	return time.Now().Round(0).UTC()
}

func environmentActionProvider(action string) string {
	return "daemon.lifecycle." + action
}

func environmentActionOperationBinding(
	plan EnvironmentActionPlan,
) OperationBinding {
	ownerID := "selection-empty"
	ids := make([]string, 0, len(plan.Targets)+len(plan.Skipped))
	for _, target := range plan.Targets {
		ids = append(ids, target.ID)
	}
	for _, target := range plan.Skipped {
		ids = append(ids, target.ID)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) == 1 {
		ownerID = ids[0]
	} else if len(plan.PlanDigest) >= 23 {
		ownerID = "selection-" + plan.PlanDigest[7:23]
	}
	return OperationBinding{
		ID: plan.OperationID, Kind: "environment." + plan.Action,
		Owner:      OperationOwner{Kind: "environment", ID: ownerID},
		PlanDigest: plan.PlanDigest,
	}
}

func environmentActionPlanDigest(
	plan EnvironmentActionPlan,
) (string, error) {
	plan.PlanDigest = ""
	return CanonicalDigest(environmentActionPlanDomain, plan)
}

func environmentActionRecordDigest(
	record environmentActionPlanRecord,
) (string, error) {
	return CanonicalDigest(
		environmentActionRecordDomain,
		environmentActionPlanRecordAuthority{
			Schema: record.Schema, Plan: record.Plan,
			Effects: record.Effects, Records: record.Records,
		},
	)
}

func (service *EnvironmentActionService) planPath(
	operationID string,
) string {
	return filepath.Join(
		service.Core.Store.Root,
		"operations",
		"environment-plans",
		operationID+".json",
	)
}

func (service *EnvironmentActionService) writePlanRecord(
	record environmentActionPlanRecord,
	replace bool,
) error {
	if err := validateEnvironmentActionPlanRecord(record); err != nil {
		return err
	}
	if err := service.operationStore().ensureDirectory(); err != nil {
		return err
	}
	dir := filepath.Dir(service.planPath(record.Plan.OperationID))
	if err := ensurePrivateConfigurationPlanDirectory(dir); err != nil {
		return err
	}
	path := service.planPath(record.Plan.OperationID)
	if !replace {
		if _, err := os.Lstat(path); err == nil {
			return ErrOperationMismatch
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxEnvironmentActionRecordBytes {
		return errors.New("environment action record exceeds size bound")
	}
	temp, err := os.CreateTemp(dir, ".environment-plan-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	keep := true
	defer func() {
		if keep {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	keep = false
	return syncOperationDirectory(dir)
}

func (service *EnvironmentActionService) loadPlanRecord(
	operationID string,
) (environmentActionPlanRecord, error) {
	if !operationIDPattern.MatchString(operationID) {
		return environmentActionPlanRecord{}, ErrInvalidOperation
	}
	path := service.planPath(operationID)
	info, err := os.Lstat(path)
	if err != nil {
		return environmentActionPlanRecord{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 ||
		info.Size() <= 0 || info.Size() > maxEnvironmentActionRecordBytes {
		return environmentActionPlanRecord{}, errors.New(
			"environment action record must be a bounded private regular file",
		)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return environmentActionPlanRecord{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record environmentActionPlanRecord
	if err := decoder.Decode(&record); err != nil {
		return environmentActionPlanRecord{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return environmentActionPlanRecord{}, errors.New(
				"environment action record contains trailing data",
			)
		}
		return environmentActionPlanRecord{}, err
	}
	if record.Plan.OperationID != operationID {
		return environmentActionPlanRecord{}, ErrOperationMismatch
	}
	if err := validateEnvironmentActionPlanRecord(record); err != nil {
		return environmentActionPlanRecord{}, err
	}
	return record, nil
}

func validateEnvironmentActionPlanRecord(
	record environmentActionPlanRecord,
) error {
	if record.Schema != environmentActionPlanRecordSchema ||
		record.CreatedAt.IsZero() ||
		!operationIDPattern.MatchString(record.Plan.OperationID) ||
		!profileDigestPattern.MatchString(record.Plan.PlanDigest) ||
		len(record.Effects) == 0 ||
		len(record.Effects) > maxOperationEffects {
		return ErrInvalidOperation
	}
	expectedPlanDigest, err := environmentActionPlanDigest(record.Plan)
	if err != nil || expectedPlanDigest != record.Plan.PlanDigest {
		return ErrOperationMismatch
	}
	expectedRecordDigest, err := environmentActionRecordDigest(record)
	if err != nil || expectedRecordDigest != record.RecordDigest {
		return ErrOperationMismatch
	}
	seenEffects := map[string]bool{}
	for _, binding := range record.Effects {
		if binding.EffectID == "" || seenEffects[binding.EffectID] {
			return ErrInvalidOperation
		}
		seenEffects[binding.EffectID] = true
		if binding.EnvironmentID == "" && binding.Reason != "selection-empty" {
			return ErrInvalidOperation
		}
	}
	for _, snapshot := range record.Records {
		if snapshot.EnvironmentID != snapshot.Record.ID ||
			snapshot.Record.Validate() != nil {
			return ErrInvalidOperation
		}
	}
	if record.Result == nil {
		if record.ResultDigest != "" {
			return ErrInvalidOperation
		}
		return nil
	}
	if record.Result.Operation != nil ||
		record.Result.Plan.OperationID != record.Plan.OperationID ||
		record.Result.Plan.PlanDigest != record.Plan.PlanDigest {
		return ErrOperationMismatch
	}
	expectedResultDigest, err := CanonicalDigest(
		environmentActionResultDomain, record.Result,
	)
	if err != nil || expectedResultDigest != record.ResultDigest {
		return ErrOperationMismatch
	}
	return nil
}

func (service *EnvironmentActionService) storeResult(
	record *environmentActionPlanRecord,
	result EnvironmentActionResult,
) error {
	result.Operation = nil
	digest, err := CanonicalDigest(environmentActionResultDomain, result)
	if err != nil {
		return err
	}
	record.Result = &result
	record.ResultDigest = digest
	return service.writePlanRecord(*record, true)
}

func terminalEnvironmentActionResult(
	record environmentActionPlanRecord,
	operation Operation,
) (EnvironmentActionResult, error) {
	if operation.Phase == OperationFailed && operation.Result != nil &&
		operation.Result.Code != "" {
		return EnvironmentActionResult{
				Plan: record.Plan, Operation: &operation,
			},
			&environmentActionTerminalError{
				code:    operation.Result.Code,
				summary: operation.Result.Summary,
			}
	}
	if operation.Phase != OperationSucceeded || record.Result == nil {
		return EnvironmentActionResult{
				Plan: record.Plan, Operation: &operation,
			},
			ErrOperationTerminalUnproved
	}
	result := cloneEnvironmentActionResult(*record.Result)
	result.Operation = &operation
	return result, nil
}

func environmentActionRequestMatchesPlan(
	request EnvironmentActionAPIRequest,
	plan EnvironmentActionPlan,
) bool {
	options, err := environmentActionOptionsFromAPIRequest(request)
	if err != nil {
		return false
	}
	return slices.Equal(cleanEnvironmentIDs(options.IDs), plan.RequestedIDs) &&
		environmentActionFilter(options) == plan.Filter &&
		options.Force == plan.Force
}

func environmentActionResultFromRecord(
	record environmentActionPlanRecord,
) EnvironmentActionResult {
	result := EnvironmentActionResult{
		Plan:    cloneEnvironmentActionPlan(record.Plan),
		Applied: []EnvironmentActionTarget{},
		Skipped: append(
			[]EnvironmentActionTarget(nil), record.Plan.Skipped...,
		),
	}
	for _, target := range record.Plan.Targets {
		applied := target
		if record.Plan.Action == EnvironmentActionStop {
			applied.Status = environment.StatusStopped
		}
		result.Applied = append(result.Applied, applied)
	}
	return result
}

func cloneEnvironmentActionPlan(
	plan EnvironmentActionPlan,
) EnvironmentActionPlan {
	cloned := plan
	cloned.RequestedIDs = cloneEnvironmentActionStrings(plan.RequestedIDs)
	cloned.Targets = cloneEnvironmentActionTargets(plan.Targets)
	cloned.Skipped = cloneEnvironmentActionTargets(plan.Skipped)
	return cloned
}

func cloneEnvironmentActionStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append(make([]string, 0, len(values)), values...)
}

func cloneEnvironmentActionTargets(
	values []EnvironmentActionTarget,
) []EnvironmentActionTarget {
	if values == nil {
		return nil
	}
	return append(
		make([]EnvironmentActionTarget, 0, len(values)),
		values...,
	)
}

func cloneEnvironmentActionResult(
	result EnvironmentActionResult,
) EnvironmentActionResult {
	cloned := result
	cloned.Plan = cloneEnvironmentActionPlan(result.Plan)
	cloned.Applied = append([]EnvironmentActionTarget(nil), result.Applied...)
	cloned.Skipped = append([]EnvironmentActionTarget(nil), result.Skipped...)
	if result.Operation != nil {
		operation := *result.Operation
		operation.Effects = append([]EffectResult(nil), result.Operation.Effects...)
		cloned.Operation = &operation
	}
	return cloned
}

func environmentActionTarget(
	targets []EnvironmentActionTarget,
	environmentID string,
) (EnvironmentActionTarget, bool) {
	for _, target := range targets {
		if target.ID == environmentID {
			return target, true
		}
	}
	return EnvironmentActionTarget{}, false
}

func environmentActionSnapshot(
	snapshots []environmentActionRecordSnapshot,
	environmentID string,
) (environment.Record, bool) {
	for _, snapshot := range snapshots {
		if snapshot.EnvironmentID == environmentID {
			return snapshot.Record, true
		}
	}
	return environment.Record{}, false
}

func operationEffect(
	operation Operation,
	effectID string,
) *EffectResult {
	for index := range operation.Effects {
		if operation.Effects[index].ID == effectID {
			return &operation.Effects[index]
		}
	}
	return nil
}

func operationEffectStatusPresent(
	operation Operation,
	status string,
) bool {
	for _, effect := range operation.Effects {
		if effect.Status == status {
			return true
		}
	}
	return false
}

func IsEnvironmentActionOperationKind(kind string) bool {
	return strings.HasPrefix(kind, "environment.") &&
		(kind == "environment."+EnvironmentActionStop ||
			kind == "environment."+EnvironmentActionClean ||
			kind == "environment."+EnvironmentActionDelete)
}
