package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

const (
	networkTransitionPlanRecordSchema = "hideout.network-transition-plan-record.v1"
	networkTransitionPlanRecordDomain = "network-transition-plan-record"
	maxNetworkTransitionRecordBytes   = 256 << 10
)

type networkTransitionPlanRecord struct {
	Schema       string                `json:"schema"`
	OperationID  string                `json:"operationId"`
	Plan         NetworkTransitionPlan `json:"plan"`
	RecordDigest string                `json:"recordDigest"`
}

type networkTransitionPlanRecordAuthority struct {
	Schema      string                `json:"schema"`
	OperationID string                `json:"operationId"`
	Plan        NetworkTransitionPlan `json:"plan"`
}

// Reserve binds one reviewed network transition to the durable operation
// envelope and retains the non-secret plan needed for restart reconciliation.
// The recovery service still has observation authority only; this method does
// not stage, activate, or otherwise mutate a route.
func (service NetworkTransitionRecoveryService) Reserve(
	operationID string,
	plan NetworkTransitionPlan,
) (Operation, error) {
	record, err := newNetworkTransitionPlanRecord(operationID, plan)
	if err != nil {
		return Operation{}, err
	}
	store := service.operationStore()
	operation, created, err := store.Reserve(
		networkTransitionOperationBinding(operationID, plan),
		operationEffectsForNetworkTransition(plan),
	)
	if err != nil {
		return Operation{}, err
	}
	if !created {
		return Operation{}, ErrOperationMismatch
	}
	if err := service.writePlanRecord(record); err != nil {
		_, _ = service.operations().Terminal(
			operation.ID,
			OperationCancelled,
			"network-plan-record-unavailable",
			"The reviewed network transition could not be retained for recovery.",
		)
		return Operation{}, err
	}
	return operation, nil
}

// ReconcileOperation loads the immutable plan bound to an accepted operation
// before asking the observation-only provider for evidence. Missing or corrupt
// authority never causes the route mutation to be replayed.
func (service NetworkTransitionRecoveryService) ReconcileOperation(
	ctx context.Context,
	operationID string,
) (Operation, error) {
	operation, err := service.operationStore().Load(operationID)
	if err != nil {
		return Operation{}, err
	}
	if operation.Terminal() {
		_ = service.removePlanRecord(operationID)
		return operation, nil
	}
	record, err := service.loadPlanRecord(operationID)
	if err != nil {
		if operation.Phase == OperationPlanned {
			return operation, err
		}
		return service.requireRecovery(
			operation,
			"network-plan-record-unavailable",
			"The accepted network transition has no valid immutable recovery plan.",
			"Stop new attaches, inspect the operation store, and recover the environment route explicitly.",
		)
	}
	if !networkOperationMatchesPlan(operation, record.Plan) {
		return service.requireRecovery(
			operation,
			"network-plan-envelope-mismatch",
			"The retained network plan does not match the accepted operation envelope.",
			"Stop new attaches, inspect the operation store, and recover the environment route explicitly.",
		)
	}
	reconciled, reconcileErr := service.Reconcile(
		ctx,
		operationID,
		record.Plan,
	)
	if reconciled.Terminal() {
		_ = service.removePlanRecord(operationID)
	}
	return reconciled, reconcileErr
}

func newNetworkTransitionPlanRecord(
	operationID string,
	plan NetworkTransitionPlan,
) (networkTransitionPlanRecord, error) {
	if !operationIDPattern.MatchString(operationID) ||
		plan.VerifyDigest() != nil {
		return networkTransitionPlanRecord{}, ErrInvalidNetworkTransition
	}
	record := networkTransitionPlanRecord{
		Schema:      networkTransitionPlanRecordSchema,
		OperationID: operationID,
		Plan:        plan,
	}
	var err error
	record.RecordDigest, err = digestNetworkTransitionPlanRecord(record)
	if err != nil {
		return networkTransitionPlanRecord{}, err
	}
	if err := validateNetworkTransitionPlanRecord(record); err != nil {
		return networkTransitionPlanRecord{}, err
	}
	return record, nil
}

func validateNetworkTransitionPlanRecord(
	record networkTransitionPlanRecord,
) error {
	if record.Schema != networkTransitionPlanRecordSchema ||
		!operationIDPattern.MatchString(record.OperationID) ||
		!profileDigestPattern.MatchString(record.RecordDigest) ||
		record.Plan.VerifyDigest() != nil {
		return ErrInvalidNetworkTransition
	}
	expected, err := digestNetworkTransitionPlanRecord(record)
	if err != nil || expected != record.RecordDigest {
		return ErrInvalidNetworkTransition
	}
	return nil
}

func digestNetworkTransitionPlanRecord(
	record networkTransitionPlanRecord,
) (string, error) {
	return CanonicalDigest(
		networkTransitionPlanRecordDomain,
		networkTransitionPlanRecordAuthority{
			Schema:      record.Schema,
			OperationID: record.OperationID,
			Plan:        record.Plan,
		},
	)
}

func networkTransitionOperationBinding(
	operationID string,
	plan NetworkTransitionPlan,
) OperationBinding {
	return OperationBinding{
		ID:   operationID,
		Kind: networkTransitionOperationKind,
		Owner: OperationOwner{
			Kind: "environment",
			ID:   plan.EnvironmentID,
		},
		PlanDigest:   plan.PlanDigest,
		BaseRevision: plan.From.ProxySecretGeneration,
	}
}

func IsNetworkTransitionOperationKind(kind string) bool {
	return kind == networkTransitionOperationKind
}

func (service NetworkTransitionRecoveryService) planPath(
	operationID string,
) string {
	return filepath.Join(
		service.operationStore().Root,
		"operations",
		"network-transition-plans",
		operationID+".json",
	)
}

func (service NetworkTransitionRecoveryService) writePlanRecord(
	record networkTransitionPlanRecord,
) error {
	if err := validateNetworkTransitionPlanRecord(record); err != nil {
		return err
	}
	if err := service.operationStore().ensureDirectory(); err != nil {
		return err
	}
	path := service.planPath(record.OperationID)
	dir := filepath.Dir(path)
	if err := ensurePrivateConfigurationPlanDirectory(dir); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return ErrOperationMismatch
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxNetworkTransitionRecordBytes {
		return errors.New("network transition plan record exceeds size bound")
	}
	temp, err := os.CreateTemp(dir, ".network-transition-plan-*.tmp")
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

func (service NetworkTransitionRecoveryService) loadPlanRecord(
	operationID string,
) (networkTransitionPlanRecord, error) {
	if !operationIDPattern.MatchString(operationID) {
		return networkTransitionPlanRecord{}, ErrInvalidNetworkTransition
	}
	path := service.planPath(operationID)
	info, err := os.Lstat(path)
	if err != nil {
		return networkTransitionPlanRecord{}, err
	}
	if !info.Mode().IsRegular() ||
		info.Mode().Perm() != 0o600 ||
		info.Size() <= 0 ||
		info.Size() > maxNetworkTransitionRecordBytes {
		return networkTransitionPlanRecord{}, errors.New(
			"network transition plan record must be a bounded private regular file",
		)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return networkTransitionPlanRecord{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record networkTransitionPlanRecord
	if err := decoder.Decode(&record); err != nil {
		return networkTransitionPlanRecord{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return networkTransitionPlanRecord{}, errors.New(
				"network transition plan record contains trailing data",
			)
		}
		return networkTransitionPlanRecord{}, err
	}
	if record.OperationID != operationID {
		return networkTransitionPlanRecord{}, ErrOperationMismatch
	}
	if err := validateNetworkTransitionPlanRecord(record); err != nil {
		return networkTransitionPlanRecord{}, err
	}
	return record, nil
}

func (service NetworkTransitionRecoveryService) removePlanRecord(
	operationID string,
) error {
	if !operationIDPattern.MatchString(operationID) {
		return ErrInvalidNetworkTransition
	}
	path := service.planPath(operationID)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncOperationDirectory(filepath.Dir(path))
}
