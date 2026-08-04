package manager

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/migration"
)

const defaultMigrationOperationListLimit = 50

type MigrationFullStateCapability struct {
	Available       bool             `json:"available"`
	Backend         string           `json:"backend"`
	ProviderVersion string           `json:"providerVersion"`
	Revision        migration.Digest `json:"revision,omitempty"`
	ReasonCode      string           `json:"reasonCode,omitempty"`
}

type MigrationCapabilitiesResponse struct {
	BundleReadVersions []uint16                     `json:"bundleReadVersions"`
	BundleWriteVersion uint16                       `json:"bundleWriteVersion"`
	ExportModes        []migration.ExportMode       `json:"exportModes"`
	FullState          MigrationFullStateCapability `json:"fullState"`
	Limits             migration.Limits             `json:"limits"`
}

type MigrationSecretInputAPIRequest struct {
	Purpose      MigrationSecretPurpose `json:"purpose"`
	BundlePath   string                 `json:"bundlePath,omitempty"`
	OperationID  string                 `json:"operationId,omitempty"`
	Passphrase   string                 `json:"passphrase"`
	Confirmation string                 `json:"confirmation,omitempty"`
}

type MigrationInspectAPIRequest struct {
	BundlePath        string `json:"bundlePath"`
	SecretInputHandle string `json:"secretInputHandle"`
}

type MigrationImportPlanAPIRequest struct {
	migration.ImportDraft
	SecretInputHandle string `json:"secretInputHandle"`
}

type MigrationImportWorkerRequest struct {
	OperationID       string
	SecretInputHandle string
	ClientBinding     string
}

type MigrationOperationActionAPIRequest struct {
	Revision          uint64                  `json:"revision"`
	SecretInputHandle string                  `json:"secretInputHandle,omitempty"`
	RetainPartial     *bool                   `json:"retainPartial,omitempty"`
	Action            MigrationRecoveryAction `json:"action,omitempty"`
}

// MigrationAPIService is the shared Manager-facing migration control plane.
// Start callbacks enqueue daemon-owned workers and must return before long I/O
// begins. All durable authority remains in Service/Import and MigrationStore.
type MigrationAPIService struct {
	Service    MigrationService
	Import     MigrationImportService
	Inspection MigrationInspectionService

	StartExport func(MigrationExportWorkerRequest) error
	StartImport func(MigrationImportWorkerRequest) error
	Resume      func(string, MigrationOperationActionAPIRequest, string) error
	Cancel      func(string, MigrationOperationActionAPIRequest, string) error
	Recover     func(string, MigrationOperationActionAPIRequest, string) error
}

func (service MigrationAPIService) Capabilities(
	r *http.Request,
) (MigrationCapabilitiesResponse, error) {
	response := MigrationCapabilitiesResponse{
		BundleReadVersions: []uint16{migration.BundleFormatVersion},
		BundleWriteVersion: migration.BundleFormatVersion,
		ExportModes:        []migration.ExportMode{migration.ExportModeConfig},
		Limits:             migration.DefaultLimits(),
		FullState: MigrationFullStateCapability{
			Available: false, Backend: "unavailable",
			ReasonCode: "migration.provider.compatibility_unproved",
		},
	}
	if r == nil || service.Service.Export == nil || service.Service.Import == nil {
		return response, nil
	}
	exportCapability, err := service.Service.Export.MigrationCapabilities(r.Context())
	if err != nil {
		return response, err
	}
	importCapability, err := service.Service.Import.MigrationCapabilities(r.Context())
	if err != nil {
		return response, err
	}
	if err := exportCapability.Validate(); err != nil {
		return response, err
	}
	if err := importCapability.Validate(); err != nil {
		return response, err
	}
	response.FullState.Backend = exportCapability.Provider
	response.FullState.ProviderVersion = exportCapability.ProviderVersion
	response.FullState.Revision = exportCapability.Revision
	response.Limits = exportCapability.Limits
	if exportCapability.Revision == importCapability.Revision &&
		exportCapability.Provider == importCapability.Provider &&
		exportCapability.FullExport && importCapability.FullImport {
		response.FullState.Available = true
		response.FullState.ReasonCode = ""
		response.ExportModes = append(response.ExportModes, migration.ExportModeFull)
		return response, nil
	}
	if exportCapability.Unavailable != nil {
		response.FullState.ReasonCode = exportCapability.Unavailable.Code
	} else if importCapability.Unavailable != nil {
		response.FullState.ReasonCode = importCapability.Unavailable.Code
	}
	return response, nil
}

func (api API) serveMigrationGet(
	w http.ResponseWriter,
	r *http.Request,
	resource string,
) {
	if api.Migrations == nil {
		writeMigrationAPIError(w, ErrMigrationCapabilityUnavailable)
		return
	}
	switch resource {
	case "migration/capabilities":
		capabilities, err := api.Migrations.Capabilities(r)
		if err != nil {
			writeMigrationAPIError(w, err)
			return
		}
		writeMigrationAPIResponse(w, resource, capabilities)
	case "migration/operations":
		api.serveMigrationOperationList(w, r)
	default:
		operationID, action, ok := migrationOperationAPIResource(resource)
		if !ok || action != "" {
			writeMigrationAPIError(w, os.ErrNotExist)
			return
		}
		api.serveMigrationOperation(w, operationID)
	}
}

func (api API) serveMigrationPost(
	w http.ResponseWriter,
	r *http.Request,
	resource string,
) {
	if api.Migrations == nil {
		writeMigrationAPIError(w, ErrMigrationCapabilityUnavailable)
		return
	}
	switch resource {
	case "migration/secret-input":
		api.serveMigrationSecretInput(w, r)
	case "migration/export/plan":
		api.serveMigrationExportPlan(w, r)
	case "migration/export/apply":
		api.serveMigrationExportApply(w, r)
	case "migration/import/inspect":
		api.serveMigrationImportInspect(w, r)
	case "migration/import/plan":
		api.serveMigrationImportPlan(w, r)
	case "migration/import/apply":
		api.serveMigrationImportApply(w, r)
	default:
		operationID, action, ok := migrationOperationAPIResource(resource)
		if !ok || action == "" {
			writeMigrationAPIError(w, os.ErrNotExist)
			return
		}
		api.serveMigrationOperationAction(w, r, operationID, action)
	}
}

func (api API) serveMigrationSecretInput(w http.ResponseWriter, r *http.Request) {
	var request MigrationSecretInputAPIRequest
	if err := decodeStrictJSON(w, r, &request, "invalid migration secret-input request"); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	passphrase := []byte(request.Passphrase)
	confirmation := []byte(request.Confirmation)
	defer clear(passphrase)
	defer clear(confirmation)
	request.Passphrase = ""
	request.Confirmation = ""
	if api.Migrations.Service.SecretInputs == nil ||
		len(passphrase) == 0 || len(passphrase) > migration.MaxPassphraseBytes {
		writeMigrationAPIError(w, ErrMigrationSecretInputInvalid)
		return
	}
	clientBinding := migrationAPIClientBinding(r)
	var (
		bundleID   migration.BundleID
		bundleFile *MigrationBundleFileBinding
	)
	switch request.Purpose {
	case MigrationSecretPurposeExportCreate:
		if request.OperationID != "" || len(confirmation) != len(passphrase) ||
			subtle.ConstantTimeCompare(passphrase, confirmation) != 1 ||
			inspectMigrationOutputPath(request.BundlePath) != nil {
			writeMigrationAPIError(w, ErrMigrationSecretInputInvalid)
			return
		}
		identifier, err := api.Migrations.Service.newMigrationID("migb")
		if err != nil {
			writeMigrationAPIError(w, err)
			return
		}
		bundleID = migration.BundleID(identifier)
	case MigrationSecretPurposeInspect, MigrationSecretPurposeImport,
		MigrationSecretPurposeExportResume:
		requireSealed := request.Purpose != MigrationSecretPurposeExportResume
		bundlePath := request.BundlePath
		var boundOperation MigrationOperation
		if request.Purpose == MigrationSecretPurposeExportResume {
			if request.BundlePath != "" || !operationIDPattern.MatchString(request.OperationID) {
				writeMigrationAPIError(w, ErrMigrationSecretInputInvalid)
				return
			}
			var err error
			boundOperation, err = api.Migrations.Service.Store.Load(request.OperationID)
			if err != nil || boundOperation.Kind != MigrationOperationExport ||
				boundOperation.Recovery.Action != MigrationRecoveryResume {
				writeMigrationAPIError(w, ErrMigrationSecretInputInvalid)
				return
			}
			bundlePath, err = migrationExportResumeArtifactPath(boundOperation)
			if err != nil {
				writeMigrationAPIError(w, err)
				return
			}
		} else if request.Purpose == MigrationSecretPurposeImport && request.OperationID != "" {
			if request.BundlePath != "" || !operationIDPattern.MatchString(request.OperationID) {
				writeMigrationAPIError(w, ErrMigrationSecretInputInvalid)
				return
			}
			var err error
			boundOperation, err = api.Migrations.Service.Store.Load(request.OperationID)
			if err != nil || boundOperation.Kind != MigrationOperationImport ||
				boundOperation.Recovery.Action != MigrationRecoveryResume ||
				boundOperation.BundleFile == nil {
				writeMigrationAPIError(w, ErrMigrationSecretInputInvalid)
				return
			}
			bundlePath = boundOperation.BundlePath
		} else if request.OperationID != "" {
			writeMigrationAPIError(w, ErrMigrationSecretInputInvalid)
			return
		}
		file, binding, public, err := probeMigrationBundleHeaderFile(
			bundlePath, requireSealed,
		)
		if err == nil {
			_ = file.Close()
			_, err = authenticateMigrationBundleHeaderFile(
				bundlePath, binding, passphrase, requireSealed,
			)
		}
		if err != nil {
			// Wrong passphrases and authenticated-header corruption intentionally
			// share one public result.
			writeMigrationAPIError(w, migration.ErrAuthenticationFailed)
			return
		}
		if request.Purpose == MigrationSecretPurposeImport {
			if err := api.Migrations.cacheMigrationImportInspection(
				r.Context(), bundlePath, binding, passphrase,
			); err != nil {
				writeMigrationAPIError(w, migration.ErrAuthenticationFailed)
				return
			}
		}
		bundleID = public.BundleID
		bundleFile = &binding
		if boundOperation.ID != "" && bundleID != boundOperation.Bundle.BundleID {
			writeMigrationAPIError(w, migration.ErrAuthenticationFailed)
			return
		}
		if request.Purpose == MigrationSecretPurposeImport && boundOperation.ID != "" &&
			(boundOperation.BundleFile == nil || *boundOperation.BundleFile != binding) {
			writeMigrationAPIError(w, migration.ErrAuthenticationFailed)
			return
		}
	default:
		writeMigrationAPIError(w, ErrMigrationSecretInputInvalid)
		return
	}
	handle, err := api.Migrations.Service.SecretInputs.Create(MigrationSecretInputRequest{
		Purpose: request.Purpose, ClientBinding: clientBinding,
		BundleID: bundleID, BundleFile: bundleFile, Passphrase: passphrase,
	})
	if err != nil {
		writeMigrationAPIError(w, err)
		return
	}
	writeMigrationAPIResponse(w, "migration/secret-input", handle)
}

func migrationExportResumeArtifactPath(operation MigrationOperation) (string, error) {
	outputPath, err := migrationExportOutputPath(operation)
	if err != nil {
		return "", err
	}
	if exists, err := migrationExportRegularPathExists(outputPath); err != nil {
		return "", err
	} else if exists {
		return outputPath, nil
	}
	partialPath := migrationExportPartialPath(outputPath, operation.ID)
	if exists, err := migrationExportRegularPathExists(partialPath); err != nil {
		return "", err
	} else if exists {
		return partialPath, nil
	}
	return "", ErrMigrationExportResumeRequired
}

func (service MigrationAPIService) cacheMigrationImportInspection(
	ctx context.Context,
	path string,
	expected MigrationBundleFileBinding,
	passphrase []byte,
) error {
	if ctx == nil || service.Inspection.Cache == nil {
		return ErrMigrationInspectionRequired
	}
	file, binding, public, err := openAndBindMigrationBundleFile(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if binding != expected {
		return migration.ErrBundleChanged
	}
	sealed, err := migration.InspectSealedBundle(ctx, file, binding.Size, passphrase)
	if err != nil {
		return err
	}
	after, afterPublic, err := bindOpenMigrationBundleFile(path, file)
	if err != nil {
		return err
	}
	if after != binding || afterPublic != public || sealed.Binding.BundleID != public.BundleID ||
		sealed.Binding.FormatVersion != public.FormatVersion {
		return migration.ErrBundleChanged
	}
	return service.Inspection.Cache.Put(sealed, binding)
}

func (api API) serveMigrationExportPlan(w http.ResponseWriter, r *http.Request) {
	var request migration.ExportRequest
	if err := decodeStrictJSON(w, r, &request, "invalid migration export request"); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := api.Migrations.Service.PlanExport(r.Context(), request)
	if err != nil {
		writeMigrationAPIError(w, err)
		return
	}
	writeMigrationAPIResponse(w, "migration/export/plan", plan)
}

func (api API) serveMigrationExportApply(w http.ResponseWriter, r *http.Request) {
	var request MigrationExportApplyRequest
	if err := decodeStrictJSON(w, r, &request, "invalid migration export apply request"); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	request.ClientBinding = migrationAPIClientBinding(r)
	result, err := api.Migrations.Service.ApplyExport(r.Context(), request)
	if err != nil {
		writeMigrationAPIError(w, err)
		return
	}
	if api.Migrations.StartExport == nil {
		writeMigrationAPIError(w, ErrMigrationCapabilityUnavailable)
		return
	}
	if err := api.Migrations.StartExport(MigrationExportWorkerRequest{
		OperationID: result.OperationID, SecretInputHandle: request.SecretInputHandle,
		SecretPurpose: MigrationSecretPurposeExportCreate,
		ClientBinding: request.ClientBinding,
	}); err != nil {
		writeMigrationAPIError(w, err)
		return
	}
	writeMigrationAPIResponse(w, "migration/export/apply", result)
}

func (api API) serveMigrationImportInspect(w http.ResponseWriter, r *http.Request) {
	var request MigrationInspectAPIRequest
	if err := decodeStrictJSON(w, r, &request, "invalid migration inspect request"); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	probe, err := ProbeMigrationBundleFile(request.BundlePath)
	if err != nil {
		writeMigrationAPIError(w, err)
		return
	}
	inspection, err := api.Migrations.Inspection.Inspect(
		r.Context(), MigrationReadOnlyInspectRequest{
			BundlePath: request.BundlePath, ExpectedFile: probe.BundleFile,
			SecretInputHandle: request.SecretInputHandle,
			ClientBinding:     migrationAPIClientBinding(r),
		},
	)
	if err != nil {
		writeMigrationAPIError(w, err)
		return
	}
	writeMigrationAPIResponse(w, "migration/import/inspect", inspection)
}

func (api API) serveMigrationImportPlan(w http.ResponseWriter, r *http.Request) {
	var request MigrationImportPlanAPIRequest
	if err := decodeStrictJSON(w, r, &request, "invalid migration import plan request"); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := api.Migrations.Import.PlanImport(r.Context(), MigrationImportPlanRequest{
		Draft: request.ImportDraft, SecretInputHandle: request.SecretInputHandle,
		ClientBinding: migrationAPIClientBinding(r),
	})
	if err != nil {
		writeMigrationAPIError(w, err)
		return
	}
	writeMigrationAPIResponse(w, "migration/import/plan", plan)
}

func (api API) serveMigrationImportApply(w http.ResponseWriter, r *http.Request) {
	var request MigrationImportApplyRequest
	if err := decodeStrictJSON(w, r, &request, "invalid migration import apply request"); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	request.ClientBinding = migrationAPIClientBinding(r)
	result, err := api.Migrations.Import.ApplyImport(r.Context(), request)
	if err != nil {
		writeMigrationAPIError(w, err)
		return
	}
	if api.Migrations.StartImport == nil {
		writeMigrationAPIError(w, ErrMigrationCapabilityUnavailable)
		return
	}
	if err := api.Migrations.StartImport(MigrationImportWorkerRequest{
		OperationID: result.OperationID, SecretInputHandle: request.SecretInputHandle,
		ClientBinding: request.ClientBinding,
	}); err != nil {
		writeMigrationAPIError(w, err)
		return
	}
	writeMigrationAPIResponse(w, "migration/import/apply", result)
}

func (api API) serveMigrationOperationList(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	for key, entries := range values {
		if key != "kind" && key != "state" && key != "bundleID" && key != "limit" {
			writeMigrationAPIError(w, ErrMigrationRequestInvalid)
			return
		}
		if len(entries) != 1 {
			writeMigrationAPIError(w, ErrMigrationRequestInvalid)
			return
		}
	}
	limit := defaultMigrationOperationListLimit
	if raw := values.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			writeMigrationAPIError(w, ErrMigrationRequestInvalid)
			return
		}
		limit = parsed
	}
	kind := MigrationOperationKind(values.Get("kind"))
	state := MigrationPhase(values.Get("state"))
	bundleID := migration.BundleID(values.Get("bundleID"))
	if kind != "" && kind != MigrationOperationExport && kind != MigrationOperationImport ||
		state != "" && !validMigrationPhase(MigrationOperationExport, state) &&
			!validMigrationPhase(MigrationOperationImport, state) {
		writeMigrationAPIError(w, ErrMigrationRequestInvalid)
		return
	}
	if bundleID != "" {
		if _, err := migration.ParseBundleID(string(bundleID)); err != nil {
			writeMigrationAPIError(w, ErrMigrationRequestInvalid)
			return
		}
	}
	operations, err := api.Migrations.Service.Store.List(500)
	if err != nil {
		writeMigrationAPIError(w, err)
		return
	}
	now := migrationAPINow(api)
	projections := make([]MigrationOperationProjection, 0, min(limit, len(operations)))
	for _, operation := range operations {
		if kind != "" && operation.Kind != kind || state != "" && operation.Phase != state ||
			bundleID != "" && operation.Bundle.BundleID != bundleID {
			continue
		}
		projection, err := ProjectStoredMigrationOperation(
			api.Migrations.Service.Store, operation, now,
		)
		if err != nil {
			writeMigrationAPIError(w, err)
			return
		}
		projections = append(projections, projection)
		if len(projections) == limit {
			break
		}
	}
	writeMigrationAPIResponse(w, "migration/operations", projections)
}

func (api API) serveMigrationOperation(w http.ResponseWriter, operationID string) {
	operation, err := api.Migrations.Service.Store.Load(operationID)
	if err != nil {
		writeMigrationAPIError(w, err)
		return
	}
	projection, err := ProjectStoredMigrationOperation(
		api.Migrations.Service.Store, operation, migrationAPINow(api),
	)
	if err != nil {
		writeMigrationAPIError(w, err)
		return
	}
	writeMigrationAPIResponse(w, "migration/operation", projection)
}

func (api API) serveMigrationOperationAction(
	w http.ResponseWriter,
	r *http.Request,
	operationID string,
	action string,
) {
	var request MigrationOperationActionAPIRequest
	if err := decodeStrictJSON(w, r, &request, "invalid migration operation action"); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	operation, err := api.Migrations.Service.Store.Load(operationID)
	if err != nil || request.Revision == 0 || request.Revision != operation.Revision {
		if err == nil {
			err = ErrMigrationStoreRevision
		}
		writeMigrationAPIError(w, err)
		return
	}
	clientBinding := migrationAPIClientBinding(r)
	var callback func(string, MigrationOperationActionAPIRequest, string) error
	switch action {
	case "resume":
		if operation.Recovery.Action != MigrationRecoveryResume ||
			request.SecretInputHandle == "" || request.RetainPartial != nil || request.Action != "" {
			writeMigrationAPIError(w, ErrMigrationRequestInvalid)
			return
		}
		callback = api.Migrations.Resume
	case "cancel":
		if request.SecretInputHandle != "" || request.Action != "" ||
			(operation.Kind == MigrationOperationExport && request.RetainPartial == nil) ||
			(operation.Kind == MigrationOperationImport && request.RetainPartial != nil) {
			writeMigrationAPIError(w, ErrMigrationRequestInvalid)
			return
		}
		callback = api.Migrations.Cancel
	case "recover":
		if operation.Recovery.Action == MigrationRecoveryNone ||
			request.Action != operation.Recovery.Action ||
			request.SecretInputHandle != "" || request.RetainPartial != nil {
			writeMigrationAPIError(w, ErrMigrationRequestInvalid)
			return
		}
		callback = api.Migrations.Recover
	default:
		writeMigrationAPIError(w, os.ErrNotExist)
		return
	}
	if callback == nil {
		writeMigrationAPIError(w, ErrMigrationCapabilityUnavailable)
		return
	}
	if err := callback(operationID, request, clientBinding); err != nil {
		writeMigrationAPIError(w, err)
		return
	}
	api.serveMigrationOperation(w, operationID)
}

func migrationOperationAPIResource(resource string) (string, string, bool) {
	parts := strings.Split(resource, "/")
	if len(parts) < 3 || len(parts) > 4 || parts[0] != "migration" ||
		parts[1] != "operations" || !operationIDPattern.MatchString(parts[2]) {
		return "", "", false
	}
	if len(parts) == 3 {
		return parts[2], "", true
	}
	switch parts[3] {
	case "resume", "cancel", "recover":
		return parts[2], parts[3], true
	default:
		return "", "", false
	}
}

func migrationAPIClientBinding(r *http.Request) string {
	credential := ""
	if r != nil {
		credential = r.Header.Get("Authorization")
	}
	digest := sha256.Sum256([]byte("hideout.migration.api-client/v1\x00" + credential))
	return "migclient_" + hex.EncodeToString(digest[:])
}

func migrationAPINow(api API) time.Time {
	if api.Now != nil {
		return api.Now().UTC()
	}
	return time.Now().UTC()
}

func writeMigrationAPIResponse(w http.ResponseWriter, resource string, data any) {
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version: APIVersion, Resource: resource, Data: data, Errors: []string{},
	})
}

func writeMigrationAPIError(w http.ResponseWriter, err error) {
	if errors.Is(err, os.ErrNotExist) {
		writeAPIDetailedError(w, http.StatusNotFound, APIErrorDetail{
			Code: "migration.operation.not_found", Message: "migration operation was not found",
			Recovery: "refresh migration operation history and use an exact operation ID",
		})
		return
	}
	public := ProjectMigrationError(err)
	status := http.StatusUnprocessableEntity
	switch public.Code {
	case "migration.request.invalid":
		status = http.StatusBadRequest
	case "migration.plan.stale", "migration.decision.conflict":
		status = http.StatusConflict
	case "migration.provider.failed":
		status = http.StatusServiceUnavailable
	case "migration.operation.failed":
		status = http.StatusInternalServerError
	}
	message := "migration request could not be completed"
	recovery := "review the stable error code, refresh current state, and retry"
	if strings.HasPrefix(public.Code, "migration.secret_input.") ||
		public.Code == migration.CodeAuthenticationFailed {
		message = "migration bundle unlock was not accepted"
		recovery = "re-enter the passphrase through a new protected secret-input request"
	}
	writeAPIDetailedError(w, status, APIErrorDetail{
		Code: public.Code, Message: message, Recovery: recovery,
	})
}
