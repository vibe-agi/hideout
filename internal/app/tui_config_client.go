package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/vibe-agi/hideout/internal/daemon"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/migration"
	tuimodal "github.com/vibe-agi/hideout/internal/tui/modal"
)

const tuiConfigurationResponseMaxBytes = 2 << 20

type tuiConfigurationClient struct {
	storeRoot string
	dial      func(string) (*http.Client, string, string, error)
	secrets   *daemonSecretCommandClient
}

type tuiConfigurationTransportError struct {
	cause error
}

func (err *tuiConfigurationTransportError) Error() string {
	if err == nil || err.cause == nil {
		return "configuration transport failed"
	}
	return "configuration transport failed: " + err.cause.Error()
}

func (err *tuiConfigurationTransportError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

type tuiConfigurationAPIError struct {
	status   int
	code     string
	message  string
	recovery string
}

func (err *tuiConfigurationAPIError) Error() string {
	if err == nil {
		return "configuration request failed"
	}
	message := err.message
	if message == "" {
		message = fmt.Sprintf(
			"configuration request failed with HTTP %d",
			err.status,
		)
	}
	if err.recovery != "" {
		message += "; recovery: " + err.recovery
	}
	return message
}

func (err *tuiConfigurationAPIError) Unwrap() error {
	if err == nil {
		return nil
	}
	switch err.code {
	case "stale-draft", "stale-plan":
		return manager.ErrStaleConfigurationPlan
	case "invalid-plan":
		return manager.ErrInvalidConfigurationPlan
	case "plan-blocked":
		return manager.ErrConfigurationBlocked
	case "mutation-conflict":
		return manager.ErrConfigurationMutationConflict
	case "unsupported-capability":
		return manager.ErrConfigurationProviderUnavailable
	default:
		return nil
	}
}

func newTUIConfigurationClient(
	storeRoot string,
) *tuiConfigurationClient {
	return &tuiConfigurationClient{
		storeRoot: storeRoot,
		secrets: &daemonSecretCommandClient{
			storeRoot: storeRoot,
			dial:      daemon.DialClient,
		},
	}
}

func (client *tuiConfigurationClient) PlanConfiguration(
	ctx context.Context,
	draft manager.ConfigurationDraft,
) (manager.ConfigurationPlan, error) {
	normalized, err := manager.DefaultTypedChangeRegistry().NormalizeDraft(
		draft,
	)
	if err != nil {
		return manager.ConfigurationPlan{}, err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return manager.ConfigurationPlan{}, errors.New(
			"encode configuration draft",
		)
	}
	defer clear(payload)
	var plan manager.ConfigurationPlan
	if err := client.request(
		ctx,
		"/api/v1/profile/transaction/plan",
		payload,
		"profile/transaction/plan",
		false,
		&plan,
	); err != nil {
		return manager.ConfigurationPlan{}, err
	}
	if plan.VerifyDigest() != nil ||
		plan.Profile != normalized.Profile ||
		plan.BaseRevision != normalized.BaseRevision ||
		len(plan.CanonicalChanges) != len(normalized.Changes) {
		return manager.ConfigurationPlan{}, errors.New(
			"Hideout returned a configuration plan for different state",
		)
	}
	for index := range plan.CanonicalChanges {
		if plan.CanonicalChanges[index].Kind !=
			normalized.Changes[index].Kind {
			return manager.ConfigurationPlan{}, errors.New(
				"Hideout returned a configuration plan for different state",
			)
		}
	}
	return plan, nil
}

func (client *tuiConfigurationClient) ApplyConfiguration(
	ctx context.Context,
	request manager.ConfigurationApplyRequest,
) (manager.ConfigurationApplyResult, error) {
	if err := request.Validate(); err != nil {
		return manager.ConfigurationApplyResult{}, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return manager.ConfigurationApplyResult{}, errors.New(
			"encode configuration apply request",
		)
	}
	defer clear(payload)
	var result manager.ConfigurationApplyResult
	if err := client.request(
		ctx,
		"/api/v1/profile/transaction/apply",
		payload,
		"profile/transaction/apply",
		true,
		&result,
	); err != nil {
		return manager.ConfigurationApplyResult{}, err
	}
	if result.Operation.Validate() != nil ||
		result.Operation.ID != request.OperationID ||
		result.Operation.PlanDigest != request.PlanDigest ||
		result.Operation.BaseRevision != request.BaseRevision ||
		result.Operation.Owner != (manager.OperationOwner{
			Kind: "profile",
			ID:   request.Profile,
		}) {
		return manager.ConfigurationApplyResult{}, errors.New(
			"Hideout returned a configuration result for a different operation",
		)
	}
	// Recovery-required responses intentionally carry only the durable
	// operation. A committed result must also carry the new projection.
	if result.Projection.Schema != "" {
		if result.Projection.Schema != manager.ProfileProjectionSchema ||
			result.Projection.Profile != request.Profile ||
			result.Projection.Revision <= request.BaseRevision ||
			result.Projection.Desired.Validate() != nil {
			return manager.ConfigurationApplyResult{}, errors.New(
				"Hideout returned invalid profile state",
			)
		}
	}
	return result, nil
}

func (client *tuiConfigurationClient) ApplyProfileNetwork(
	ctx context.Context,
	options manager.ProfileNetworkOptions,
) (manager.ProfileNetworkResult, error) {
	payload, err := json.Marshal(manager.ProfileNetworkAPIRequest(options))
	if err != nil {
		return manager.ProfileNetworkResult{},
			errors.New("encode profile network request")
	}
	defer clear(payload)
	var result manager.ProfileNetworkResult
	if err := client.request(
		ctx,
		"/api/v1/profile/network/apply",
		payload,
		"profile/network/apply",
		false,
		&result,
	); err != nil {
		return manager.ProfileNetworkResult{}, err
	}
	if result.Network.Profile != options.ProfileName ||
		result.Network.Mode == "" ||
		result.Operation != nil &&
			(result.Operation.Validate() != nil ||
				result.Operation.Owner != (manager.OperationOwner{
					Kind: "profile",
					ID:   options.ProfileName,
				})) {
		return manager.ProfileNetworkResult{},
			errors.New("Hideout returned network state for a different profile")
	}
	return result, nil
}

func (client *tuiConfigurationClient) PlanSecret(
	ctx context.Context,
	draft manager.SecretDraft,
) (manager.SecretPlan, error) {
	if client == nil || client.secrets == nil {
		return manager.SecretPlan{}, manager.ErrSecretProviderUnavailable
	}
	return client.secrets.Plan(ctx, draft)
}

func (client *tuiConfigurationClient) ApplySecret(
	ctx context.Context,
	request manager.SecretApplyRequest,
) (manager.SecretApplyResult, error) {
	if client == nil || client.secrets == nil {
		if request.Value != nil {
			request.Value.Clear()
		}
		return manager.SecretApplyResult{},
			manager.ErrSecretProviderUnavailable
	}
	return client.secrets.Apply(ctx, request)
}

func (client *tuiConfigurationClient) ApplyMigrationAction(
	ctx context.Context,
	request tuimodal.MigrationActionRequest,
) (manager.MigrationOperationProjection, error) {
	operation := request.Operation
	if operation.Validate() != nil || operation.TerminalReceipt != nil ||
		ctx == nil {
		return manager.MigrationOperationProjection{},
			errors.New("migration action request is invalid")
	}
	actionRequest := manager.MigrationOperationActionAPIRequest{
		Revision: operation.Revision,
	}
	action := string(request.Action)
	switch request.Action {
	case tuimodal.MigrationActionResume:
		if len(request.Passphrase) == 0 || !operation.Recovery.Required ||
			len(operation.Recovery.AllowedActions) != 1 ||
			operation.Recovery.AllowedActions[0] != manager.MigrationRecoveryResume {
			return manager.MigrationOperationProjection{},
				errors.New("migration resume request is invalid")
		}
		handle, err := client.createMigrationResumeSecretInput(
			ctx, operation, request.Passphrase,
		)
		if err != nil {
			return manager.MigrationOperationProjection{}, err
		}
		actionRequest.SecretInputHandle = handle.Handle
	case tuimodal.MigrationActionCancel:
		if len(request.Passphrase) != 0 || operation.Recovery.Required {
			return manager.MigrationOperationProjection{},
				errors.New("migration cancellation request is invalid")
		}
		if operation.Kind == manager.MigrationOperationExport {
			retain := request.RetainPartial
			actionRequest.RetainPartial = &retain
		}
	case tuimodal.MigrationActionRecover:
		if len(request.Passphrase) != 0 || !operation.Recovery.Required ||
			len(operation.Recovery.AllowedActions) != 1 ||
			operation.Recovery.AllowedActions[0] == manager.MigrationRecoveryResume ||
			operation.Recovery.AllowedActions[0] == manager.MigrationRecoveryManual {
			return manager.MigrationOperationProjection{},
				errors.New("migration recovery request is invalid")
		}
		actionRequest.Action = operation.Recovery.AllowedActions[0]
	default:
		return manager.MigrationOperationProjection{},
			errors.New("migration action is invalid")
	}
	payload, err := json.Marshal(actionRequest)
	if err != nil {
		return manager.MigrationOperationProjection{},
			errors.New("encode migration action")
	}
	defer clear(payload)
	var projection manager.MigrationOperationProjection
	if err := client.request(
		ctx,
		"/api/v1/migration/operations/"+operation.OperationID+"/"+action,
		payload,
		"migration/operation",
		false,
		&projection,
	); err != nil {
		return manager.MigrationOperationProjection{}, err
	}
	if projection.Validate() != nil ||
		projection.OperationID != operation.OperationID ||
		projection.BundleID != operation.BundleID ||
		projection.Kind != operation.Kind ||
		projection.Revision < operation.Revision {
		return manager.MigrationOperationProjection{},
			errors.New("Hideout returned a migration result for a different operation")
	}
	return projection, nil
}

func (client *tuiConfigurationClient) createMigrationResumeSecretInput(
	ctx context.Context,
	operation manager.MigrationOperationProjection,
	passphrase []byte,
) (manager.MigrationSecretInputHandle, error) {
	purpose := manager.MigrationSecretPurposeImport
	if operation.Kind == manager.MigrationOperationExport {
		purpose = manager.MigrationSecretPurposeExportResume
	}
	return client.createTUIMigrationSecretInput(ctx, manager.MigrationSecretInputAPIRequest{
		Purpose: purpose, OperationID: operation.OperationID,
		Passphrase: string(passphrase),
	}, purpose, operation.BundleID)
}

func (client *tuiConfigurationClient) PlanMigrationExport(
	ctx context.Context,
	request migration.ExportRequest,
	passphrase []byte,
) (tuimodal.MigrationExportSession, error) {
	if ctx == nil || len(passphrase) == 0 ||
		len(passphrase) > migration.MaxPassphraseBytes {
		return tuimodal.MigrationExportSession{},
			errors.New("migration export passphrase is invalid")
	}
	handle, err := client.createTUIMigrationSecretInput(
		ctx,
		manager.MigrationSecretInputAPIRequest{
			Purpose:    manager.MigrationSecretPurposeExportCreate,
			BundlePath: request.OutputPath,
			Passphrase: string(passphrase), Confirmation: string(passphrase),
		},
		manager.MigrationSecretPurposeExportCreate,
		"",
	)
	if err != nil {
		return tuimodal.MigrationExportSession{}, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return tuimodal.MigrationExportSession{},
			errors.New("encode migration export plan request")
	}
	defer clear(payload)
	var plan migration.ExportPlan
	if err := client.request(
		ctx, "/api/v1/migration/export/plan", payload,
		"migration/export/plan", false, &plan,
	); err != nil {
		return tuimodal.MigrationExportSession{}, err
	}
	if manager.VerifyMigrationExportPlan(plan) != nil ||
		plan.Mode != request.Mode || plan.OutputPath != request.OutputPath {
		return tuimodal.MigrationExportSession{},
			errors.New("Hideout returned an export plan for different input")
	}
	return tuimodal.MigrationExportSession{
		Plan: plan, SecretInputHandle: handle.Handle,
	}, nil
}

func (client *tuiConfigurationClient) ApplyMigrationExport(
	ctx context.Context,
	session tuimodal.MigrationExportSession,
) (manager.MigrationApplyResult, error) {
	if ctx == nil || manager.VerifyMigrationExportPlan(session.Plan) != nil ||
		session.SecretInputHandle == "" {
		return manager.MigrationApplyResult{},
			errors.New("migration export apply request is invalid")
	}
	idempotencyKey, err := newMigrationCLIIdempotencyKey()
	if err != nil {
		return manager.MigrationApplyResult{}, err
	}
	request := manager.MigrationExportApplyRequest{
		Schema: manager.MigrationExportApplySchema, Plan: session.Plan,
		Confirmation: manager.MigrationPlanConfirmation{
			PlanDigest: session.Plan.PlanDigest,
			AcceptedRiskAcknowledgements: append(
				[]string(nil), session.Plan.RiskAcknowledgements...,
			),
		},
		SecretInputHandle: session.SecretInputHandle,
		IdempotencyKey:    idempotencyKey,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return manager.MigrationApplyResult{},
			errors.New("encode migration export apply request")
	}
	defer clear(payload)
	var result manager.MigrationApplyResult
	if err := client.request(
		ctx, "/api/v1/migration/export/apply", payload,
		"migration/export/apply", false, &result,
	); err != nil {
		return manager.MigrationApplyResult{}, err
	}
	if !validTUIMigrationApplyResult(result) {
		return manager.MigrationApplyResult{},
			errors.New("Hideout returned an invalid migration export result")
	}
	return result, nil
}

func (client *tuiConfigurationClient) UnlockMigrationImport(
	ctx context.Context,
	bundlePath string,
	passphrase []byte,
) (tuimodal.MigrationImportSession, error) {
	if ctx == nil || bundlePath == "" || len(passphrase) == 0 ||
		len(passphrase) > migration.MaxPassphraseBytes {
		return tuimodal.MigrationImportSession{},
			errors.New("migration import unlock request is invalid")
	}
	inspectHandle, err := client.createTUIMigrationSecretInput(
		ctx,
		manager.MigrationSecretInputAPIRequest{
			Purpose:    manager.MigrationSecretPurposeInspect,
			BundlePath: bundlePath, Passphrase: string(passphrase),
		},
		manager.MigrationSecretPurposeInspect,
		"",
	)
	if err != nil {
		return tuimodal.MigrationImportSession{}, err
	}
	inspectRequest := manager.MigrationInspectAPIRequest{
		BundlePath: bundlePath, SecretInputHandle: inspectHandle.Handle,
	}
	payload, err := json.Marshal(inspectRequest)
	if err != nil {
		return tuimodal.MigrationImportSession{},
			errors.New("encode migration inspection request")
	}
	var inspection manager.MigrationReadOnlyInspection
	requestErr := client.request(
		ctx, "/api/v1/migration/import/inspect", payload,
		"migration/import/inspect", false, &inspection,
	)
	clear(payload)
	if requestErr != nil {
		return tuimodal.MigrationImportSession{}, requestErr
	}
	if inspection.Inventory.Validate() != nil ||
		inspection.Binding.BundleID != inspection.Inventory.BundleID {
		return tuimodal.MigrationImportSession{},
			errors.New("Hideout returned an invalid migration inventory")
	}
	importHandle, err := client.createTUIMigrationSecretInput(
		ctx,
		manager.MigrationSecretInputAPIRequest{
			Purpose:    manager.MigrationSecretPurposeImport,
			BundlePath: bundlePath, Passphrase: string(passphrase),
		},
		manager.MigrationSecretPurposeImport,
		inspection.Binding.BundleID,
	)
	if err != nil {
		return tuimodal.MigrationImportSession{}, err
	}
	return tuimodal.MigrationImportSession{
		Inspection: inspection, SecretInputHandle: importHandle.Handle,
	}, nil
}

func (client *tuiConfigurationClient) PlanMigrationImport(
	ctx context.Context,
	draft migration.ImportDraft,
	secretInputHandle string,
) (migration.ImportPlan, error) {
	if ctx == nil || secretInputHandle == "" {
		return migration.ImportPlan{},
			errors.New("migration import plan request is invalid")
	}
	request := manager.MigrationImportPlanAPIRequest{
		ImportDraft: draft, SecretInputHandle: secretInputHandle,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return migration.ImportPlan{},
			errors.New("encode migration import plan request")
	}
	defer clear(payload)
	var plan migration.ImportPlan
	if err := client.request(
		ctx, "/api/v1/migration/import/plan", payload,
		"migration/import/plan", false, &plan,
	); err != nil {
		return migration.ImportPlan{}, err
	}
	if manager.VerifyMigrationImportPlan(plan) != nil ||
		plan.BundlePath != draft.BundlePath || plan.BundleBinding != draft.BundleBinding {
		return migration.ImportPlan{},
			errors.New("Hideout returned an import plan for different input")
	}
	return plan, nil
}

func (client *tuiConfigurationClient) ApplyMigrationImport(
	ctx context.Context,
	plan migration.ImportPlan,
	secretInputHandle string,
) (manager.MigrationApplyResult, error) {
	if ctx == nil || manager.VerifyMigrationImportPlan(plan) != nil ||
		secretInputHandle == "" || len(plan.Blockers) != 0 ||
		!plan.Compatibility.Available {
		return manager.MigrationApplyResult{},
			errors.New("migration import apply request is invalid")
	}
	idempotencyKey, err := newMigrationCLIIdempotencyKey()
	if err != nil {
		return manager.MigrationApplyResult{}, err
	}
	request := manager.MigrationImportApplyRequest{
		Schema: manager.MigrationImportApplySchema, Plan: plan,
		Confirmation: manager.MigrationPlanConfirmation{
			PlanDigest: plan.PlanDigest,
			AcceptedRiskAcknowledgements: append(
				[]string(nil), plan.RiskAcknowledgements...,
			),
			ApprovedAuthorityProposalIDs: migrationApprovedAuthorityProposalIDsForCLI(
				plan.AuthorityActions,
			),
		},
		SecretInputHandle: secretInputHandle,
		IdempotencyKey:    idempotencyKey,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return manager.MigrationApplyResult{},
			errors.New("encode migration import apply request")
	}
	defer clear(payload)
	var result manager.MigrationApplyResult
	if err := client.request(
		ctx, "/api/v1/migration/import/apply", payload,
		"migration/import/apply", false, &result,
	); err != nil {
		return manager.MigrationApplyResult{}, err
	}
	if !validTUIMigrationApplyResult(result) {
		return manager.MigrationApplyResult{},
			errors.New("Hideout returned an invalid migration import result")
	}
	return result, nil
}

func (client *tuiConfigurationClient) createTUIMigrationSecretInput(
	ctx context.Context,
	request manager.MigrationSecretInputAPIRequest,
	expectedPurpose manager.MigrationSecretPurpose,
	expectedBundleID migration.BundleID,
) (manager.MigrationSecretInputHandle, error) {
	payload, err := json.Marshal(request)
	request.Passphrase = ""
	request.Confirmation = ""
	if err != nil {
		return manager.MigrationSecretInputHandle{},
			errors.New("encode protected migration input")
	}
	defer clear(payload)
	var handle manager.MigrationSecretInputHandle
	if err := client.request(
		ctx,
		"/api/v1/migration/secret-input",
		payload,
		"migration/secret-input",
		false,
		&handle,
	); err != nil {
		return manager.MigrationSecretInputHandle{}, err
	}
	if handle.Validate() != nil || handle.Purpose != expectedPurpose ||
		(expectedBundleID != "" && handle.BundleID != expectedBundleID) {
		return manager.MigrationSecretInputHandle{},
			errors.New("Hideout returned an invalid protected migration handle")
	}
	return handle, nil
}

func validTUIMigrationApplyResult(result manager.MigrationApplyResult) bool {
	return strings.HasPrefix(result.OperationID, "op_") &&
		len(result.OperationID) >= 12 && result.State != "" && result.Next != ""
}

func (client *tuiConfigurationClient) PlanEnvironment(
	ctx context.Context,
	action string,
	request manager.EnvironmentActionAPIRequest,
) (manager.EnvironmentActionPlan, error) {
	if err := validateTUILifecycleRequest(action, request, false); err != nil {
		return manager.EnvironmentActionPlan{}, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return manager.EnvironmentActionPlan{},
			errors.New("encode environment lifecycle request")
	}
	defer clear(payload)
	resource := "environment/" + action + "/plan"
	var plan manager.EnvironmentActionPlan
	if err := client.request(
		ctx,
		"/api/v1/"+resource,
		payload,
		resource,
		false,
		&plan,
	); err != nil {
		return manager.EnvironmentActionPlan{}, err
	}
	if !validTUILifecyclePlan(
		plan,
		action,
		request.IDs[0],
	) {
		return manager.EnvironmentActionPlan{},
			errors.New("Hideout returned a lifecycle plan for a different environment")
	}
	return plan, nil
}

func (client *tuiConfigurationClient) ApplyEnvironment(
	ctx context.Context,
	action string,
	request manager.EnvironmentActionAPIRequest,
) (manager.EnvironmentActionResult, error) {
	if err := validateTUILifecycleRequest(action, request, true); err != nil {
		return manager.EnvironmentActionResult{}, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return manager.EnvironmentActionResult{},
			errors.New("encode environment lifecycle request")
	}
	defer clear(payload)
	resource := "environment/" + action + "/apply"
	var result manager.EnvironmentActionResult
	if err := client.request(
		ctx,
		"/api/v1/"+resource,
		payload,
		resource,
		false,
		&result,
	); err != nil {
		return manager.EnvironmentActionResult{}, err
	}
	if !validTUILifecycleResult(
		result,
		action,
		request.IDs[0],
		request.OperationID,
		request.PlanDigest,
	) {
		return manager.EnvironmentActionResult{},
			errors.New("Hideout returned a lifecycle result for a different operation")
	}
	return result, nil
}

func (client *tuiConfigurationClient) request(
	ctx context.Context,
	path string,
	payload []byte,
	resource string,
	acceptNonTerminal bool,
	target any,
) error {
	if client == nil || ctx == nil {
		return errors.New("configuration client is unavailable")
	}
	dial := client.dial
	if dial == nil {
		dial = daemon.DialClient
	}
	httpClient, base, token, err := dial(client.storeRoot)
	if err != nil {
		return &tuiConfigurationTransportError{cause: err}
	}
	if httpClient == nil {
		return &tuiConfigurationTransportError{
			cause: errors.New("daemon HTTP client is unavailable"),
		}
	}
	defer httpClient.CloseIdleConnections()
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		base+path,
		bytes.NewReader(payload),
	)
	if err != nil {
		return &tuiConfigurationTransportError{cause: err}
	}
	request.Host = "localhost"
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := httpClient.Do(request)
	if err != nil {
		return &tuiConfigurationTransportError{cause: err}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(
		response.Body,
		tuiConfigurationResponseMaxBytes+1,
	))
	if err != nil {
		clear(data)
		return &tuiConfigurationTransportError{cause: err}
	}
	defer clear(data)
	if len(data) > tuiConfigurationResponseMaxBytes {
		return errors.New(
			"configuration response exceeds the safe bound",
		)
	}
	var envelope struct {
		Version      string                   `json:"version"`
		Resource     string                   `json:"resource"`
		Data         json.RawMessage          `json:"data"`
		Errors       []string                 `json:"errors"`
		ErrorDetails []manager.APIErrorDetail `json:"errorDetails"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return errors.New("configuration response is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New(
			"configuration response contains trailing data",
		)
	}
	statusOK := response.StatusCode == http.StatusOK ||
		acceptNonTerminal &&
			response.StatusCode == http.StatusAccepted
	if !statusOK {
		return newTUIConfigurationAPIError(
			response.StatusCode,
			envelope.ErrorDetails,
			envelope.Errors,
		)
	}
	if envelope.Version != manager.APIVersion ||
		envelope.Resource != resource ||
		len(envelope.Data) == 0 ||
		bytes.Equal(envelope.Data, []byte("null")) {
		return errors.New(
			"configuration response contract mismatch",
		)
	}
	if len(envelope.Errors) != 0 {
		return &tuiConfigurationAPIError{
			status:  response.StatusCode,
			message: strings.Join(envelope.Errors, "; "),
		}
	}
	dataDecoder := json.NewDecoder(bytes.NewReader(envelope.Data))
	dataDecoder.DisallowUnknownFields()
	if err := dataDecoder.Decode(target); err != nil {
		return errors.New(
			"configuration response data is invalid",
		)
	}
	if err := dataDecoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New(
			"configuration response data contains trailing content",
		)
	}
	return nil
}

func validateTUILifecycleRequest(
	action string,
	request manager.EnvironmentActionAPIRequest,
	apply bool,
) error {
	if action != manager.EnvironmentActionStop &&
		action != manager.EnvironmentActionClean {
		return errors.New("environment lifecycle action is invalid")
	}
	if len(request.IDs) != 1 ||
		!validTUILifecycleID(request.IDs[0]) ||
		request.Idle != "" ||
		request.StoppedOnly ||
		request.Force {
		return errors.New(
			"environment lifecycle requires one exact environment ID",
		)
	}
	if apply {
		if !validTUILifecycleOperationID(request.OperationID) ||
			!validTUILifecycleDigest(request.PlanDigest) ||
			!request.Confirmed {
			return errors.New(
				"environment lifecycle apply requires the reviewed operation identity",
			)
		}
	} else if request.OperationID != "" ||
		request.PlanDigest != "" || request.Confirmed {
		return errors.New(
			"environment lifecycle plan cannot reuse an operation identity",
		)
	}
	return nil
}

func validTUILifecycleID(value string) bool {
	if len(value) <= len("env_") || len(value) > 128 ||
		!strings.HasPrefix(value, "env_") {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "env_") {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func validTUILifecyclePlan(
	plan manager.EnvironmentActionPlan,
	action string,
	targetID string,
) bool {
	if plan.Action != action ||
		len(plan.RequestedIDs) != 1 ||
		plan.RequestedIDs[0] != targetID ||
		plan.Filter != (manager.EnvironmentActionFilter{}) ||
		plan.Total != 1 ||
		len(plan.Targets)+len(plan.Skipped) != 1 ||
		!validTUILifecycleOperationID(plan.OperationID) ||
		!validTUILifecycleDigest(plan.PlanDigest) {
		return false
	}
	for _, target := range append(
		append(
			[]manager.EnvironmentActionTarget(nil),
			plan.Targets...,
		),
		plan.Skipped...,
	) {
		if target.ID != targetID {
			return false
		}
	}
	return true
}

func validTUILifecycleResult(
	result manager.EnvironmentActionResult,
	action string,
	targetID string,
	operationID string,
	planDigest string,
) bool {
	if !validTUILifecyclePlan(result.Plan, action, targetID) ||
		result.Operation == nil ||
		result.Operation.Validate() != nil ||
		result.Operation.ID != operationID ||
		result.Operation.ID != result.Plan.OperationID ||
		result.Operation.Kind != "environment."+action ||
		result.Operation.Owner != (manager.OperationOwner{
			Kind: "environment", ID: targetID,
		}) ||
		result.Operation.PlanDigest != planDigest ||
		result.Operation.PlanDigest != result.Plan.PlanDigest ||
		result.Operation.Phase != manager.OperationSucceeded ||
		len(result.Applied)+len(result.Skipped) == 0 ||
		len(result.Applied)+len(result.Skipped) > 2 {
		return false
	}
	for _, target := range append(
		append(
			[]manager.EnvironmentActionTarget(nil),
			result.Applied...,
		),
		result.Skipped...,
	) {
		if target.ID != targetID {
			return false
		}
	}
	return true
}

func validTUILifecycleOperationID(value string) bool {
	if len(value) < len("op_")+8 || len(value) > len("op_")+124 ||
		!strings.HasPrefix(value, "op_") {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "op_") {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func validTUILifecycleDigest(value string) bool {
	if len(value) != len("sha256:")+64 ||
		!strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "sha256:") {
		if character < '0' ||
			character > '9' && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func newTUIConfigurationAPIError(
	status int,
	details []manager.APIErrorDetail,
	fallback []string,
) error {
	if len(details) != 1 {
		return &tuiConfigurationAPIError{
			status:  status,
			message: strings.Join(fallback, "; "),
		}
	}
	return &tuiConfigurationAPIError{
		status:   status,
		code:     details[0].Code,
		message:  details[0].Message,
		recovery: details[0].Recovery,
	}
}
