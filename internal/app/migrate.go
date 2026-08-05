package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/daemon"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/profile"
	"golang.org/x/term"
)

const migrationCLIResponseLimit = 8 << 20

type migrationAPIEnvelope struct {
	Version      string                   `json:"version"`
	Resource     string                   `json:"resource"`
	Data         json.RawMessage          `json:"data"`
	Errors       []string                 `json:"errors"`
	ErrorDetails []manager.APIErrorDetail `json:"errorDetails,omitempty"`
}

type migrationCLIAPIError struct {
	Detail manager.APIErrorDetail
}

func (err migrationCLIAPIError) Error() string {
	message := err.Detail.Code + ": " + err.Detail.Message
	if err.Detail.Recovery != "" {
		message += "; recovery: " + err.Detail.Recovery
	}
	return message
}

// safeMigrationCLIAPIError treats the Manager's stable code as authority but
// never renders server-supplied prose. A backend cause, proxy URL, or opaque
// guest byte accidentally copied into message/recovery therefore cannot cross
// the CLI boundary.
func safeMigrationCLIAPIError(
	detail manager.APIErrorDetail,
) (migrationCLIAPIError, bool) {
	if detail.Validate() != nil {
		return migrationCLIAPIError{}, false
	}
	safe := manager.APIErrorDetail{Code: detail.Code, Field: detail.Field}
	switch {
	case detail.Code == "migration.operation.not_found":
		safe.Message = "migration operation was not found"
		safe.Recovery = "refresh migration operation history and use an exact operation ID"
	case detail.Code == migration.CodeAuthenticationFailed ||
		strings.HasPrefix(detail.Code, "migration.secret_input."):
		safe.Message = "migration bundle unlock was not accepted"
		safe.Recovery = "re-enter the passphrase through a new protected secret-input request"
	case strings.HasPrefix(detail.Code, "migration."):
		safe.Message = "migration request could not be completed"
		safe.Recovery = "review the stable error code, refresh current state, and retry"
	default:
		safe.Message = "Hideout request could not be completed"
		safe.Recovery = "refresh current state and retry"
	}
	return migrationCLIAPIError{Detail: safe}, true
}

func (a app) migrateCommand(args []string) error {
	if len(args) == 0 || containsHelpToken(args) {
		a.migrateUsage()
		return nil
	}
	switch args[0] {
	case "capabilities":
		return a.migrateCapabilities(args[1:])
	case "export":
		return a.migrateExport(args[1:])
	case "inspect":
		return a.migrateInspect(args[1:])
	case "import":
		return a.migrateImport(args[1:])
	case "status":
		return a.migrateStatus(args[1:])
	case "resume":
		return a.migrateResume(args[1:])
	case "cancel":
		return a.migrateCancel(args[1:])
	case "recover":
		return a.migrateRecover(args[1:])
	default:
		return errors.New("unknown migrate command; use: hideout migrate export|inspect|import|status|resume|cancel|recover|capabilities")
	}
}

func (a app) migrateUsage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  hideout migrate capabilities [--json]")
	fmt.Fprintln(a.stdout, "  hideout migrate export (--environment <name>...|--all) --mode config --out <file> [--include-secret <ref> --ack-secret-transfer] [--preview|--yes]")
	fmt.Fprintln(a.stdout, "  hideout migrate export (--environment <name>...|--all) --out <file> --ack-guest-content [--include-secret <ref> --ack-secret-transfer] [--stop] [--preview|--yes]")
	fmt.Fprintln(a.stdout, "  hideout migrate inspect <bundle> [--passphrase-stdin] [--json]")
	fmt.Fprintln(a.stdout, "  hideout migrate import <bundle> [--environment <source-ref>...|--all] [--name <source>=<destination> | --replace <source>] [--secret <source>=<existing-ref>|import:<new-ref> --ack-secret-transfer] [--approve <proposal-id>[=<destination-json>]] [--policy <source>=<policy>] [--preview|--yes]")
	fmt.Fprintln(a.stdout, "  hideout migrate status [operation-id] [--json]")
	fmt.Fprintln(a.stdout, "  hideout migrate resume <operation-id> [--passphrase-stdin] [--json]")
	fmt.Fprintln(a.stdout, "  hideout migrate cancel <operation-id> (--retain-partial|--remove-partial) [--yes] [--json]")
	fmt.Fprintln(a.stdout, "  hideout migrate recover <operation-id> [--action finish|rollback|remove-partial] [--yes] [--json]")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Start safely:")
	fmt.Fprintln(a.stdout, "  hideout migrate export --environment dev --mode config --out ./dev-config.hideout-migration --preview")
	fmt.Fprintln(a.stdout, "  hideout migrate export --environment dev --out ./dev.hideout-migration --ack-guest-content --preview")
	fmt.Fprintln(a.stdout, "  hideout migrate inspect ./dev.hideout-migration")
	fmt.Fprintln(a.stdout, "  hideout migrate import ./dev.hideout-migration --preview")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Start with --preview. Repeat the reviewed command with --yes only when its concrete plan is unblocked.")
	fmt.Fprintln(a.stdout, "Full export copies the complete persistent disk graph of stopped selected VMs into one encrypted file; config mode copies no VM disk.")
	fmt.Fprintln(a.stdout, "If a selected VM is running, preview shows an exact stop plan. --stop authorizes that separate action; it never force-stops, recreates, or shuts down the daemon.")
	fmt.Fprintln(a.stdout, "Always excluded: host workspace contents, activity/audit history, caches, installed host apps, active processes, VM memory, sessions, and local control-plane identities.")
	fmt.Fprintln(a.stdout, "A guest disk is opaque and may itself contain credentials or private application data. Safe Clone is the default import identity policy.")
	fmt.Fprintln(a.stdout, "Enter passphrases only at the hidden prompt or with --passphrase-stdin; never place one in argv or an environment variable.")
}

func (a app) migrateCapabilities(args []string) error {
	fs := flag.NewFlagSet("migrate capabilities", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "write canonical JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return errors.New("usage: hideout migrate capabilities [--json]")
	}
	store, err := a.migrationStore(context.Background())
	if err != nil {
		return err
	}
	var capabilities manager.MigrationCapabilitiesResponse
	if err := a.migrationAPI(store, http.MethodGet, "/api/v1/migration/capabilities", nil, "migration/capabilities", &capabilities); err != nil {
		return err
	}
	if *jsonOut {
		return writeIndentedJSON(a.stdout, capabilities)
	}
	fmt.Fprintf(a.stdout, "Bundle format: read=%v write=%d\n", capabilities.BundleReadVersions, capabilities.BundleWriteVersion)
	fmt.Fprintf(a.stdout, "Export modes: %s\n", joinMigrationModes(capabilities.ExportModes))
	if capabilities.FullState.Available {
		fmt.Fprintf(a.stdout, "Full VM state: Available (%s %s)\n", capabilities.FullState.Backend, capabilities.FullState.ProviderVersion)
	} else {
		fmt.Fprintf(a.stdout, "Full VM state: Unavailable (%s)\n", capabilities.FullState.ReasonCode)
	}
	return nil
}

func addDoctorMigrationStatus(
	store profile.Store,
	report func(string, string, string),
) {
	operationsRoot := filepath.Join(store.Root, "migration", "operations")
	if _, err := os.Lstat(operationsRoot); errors.Is(err, os.ErrNotExist) {
		return
	} else if err != nil {
		report("migration", "error", "migration operation ledger is unreadable")
		return
	}
	migrationStore := manager.MigrationStore{Root: store.Root}
	operations, err := migrationStore.List(4096)
	if err != nil {
		report("migration", "error", "migration operation ledger failed validation")
		return
	}
	terminal := 0
	recovery := 0
	for _, operation := range operations {
		if operation.Terminal() {
			if operation.Recovery.Action == manager.MigrationRecoveryNone {
				terminal++
				evidence, err := migrationStore.LoadTerminalEvidence(operation.ID)
				if err != nil || evidence.Receipt.OperationID != operation.ID {
					report("migration", "error", "a terminal migration is missing its validated receipt")
					return
				}
			} else {
				recovery++
			}
			continue
		}
		if operation.Recovery.Action != manager.MigrationRecoveryNone {
			recovery++
		}
	}
	if recovery != 0 {
		report("migration", "warn", fmt.Sprintf(
			"%d operation(s) require an explicit migration recovery action; %d terminal receipt(s) validated",
			recovery, terminal,
		))
		return
	}
	report("migration", "ok", fmt.Sprintf(
		"%d operation(s); %d terminal receipt(s) validated", len(operations), terminal,
	))
}

func (a app) migrateExport(args []string) error {
	fs := flag.NewFlagSet("migrate export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var environments, includedSecrets stringListFlag
	fs.Var(&environments, "environment", "environment name; repeatable")
	fs.Var(&includedSecrets, "include-secret", "Hideout-managed secret ref whose value is encrypted into the bundle; repeatable")
	all := fs.Bool("all", false, "expand every current environment into the review")
	out := fs.String("out", "", "absolute or current-directory-relative output file")
	mode := fs.String("mode", string(migration.ExportModeFull), "full or config")
	stop := fs.Bool("stop", false, "stop eligible selected VMs after a separate review")
	ackGuestContent := fs.Bool(
		"ack-guest-content", false,
		"acknowledge that opaque guest disks may contain credentials and device-bound identities",
	)
	ackSecretTransfer := fs.Bool(
		"ack-secret-transfer", false,
		"acknowledge explicit transfer of the named Hideout-managed secret values",
	)
	preview := fs.Bool("preview", false, "render the plan without applying")
	yes := fs.Bool("yes", false, "apply the exact displayed plan")
	passphraseStdin := fs.Bool("passphrase-stdin", false, "read passphrase from stdin")
	jsonOut := fs.Bool("json", false, "write canonical JSON")
	idempotency := fs.String("idempotency-key", "", "reuse an exact client-generated apply key")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *out == "" ||
		(*all && len(environments.Values()) != 0) || (*preview && *yes) ||
		(*jsonOut && !*preview && !*yes) {
		return errors.New("usage: hideout migrate export (--environment <name>...|--all) --out <file> [--mode full|config] [--stop] [--preview|--yes]")
	}
	exportMode := migration.ExportMode(*mode)
	if exportMode != migration.ExportModeFull && exportMode != migration.ExportModeConfig {
		return errors.New("--mode must be full or config")
	}
	if exportMode == migration.ExportModeConfig && *stop {
		return errors.New("--stop applies only to --mode full; configuration export never changes VM lifecycle")
	}
	if exportMode == migration.ExportModeConfig && *ackGuestContent {
		return errors.New("--ack-guest-content applies only to --mode full")
	}
	if exportMode == migration.ExportModeFull && !*ackGuestContent {
		return errors.New("full export requires --ack-guest-content after reviewing that opaque VM disks may contain credentials and device-bound identities")
	}
	secretRefs := normalizedMigrationStrings(includedSecrets.Values())
	if (len(secretRefs) != 0) != *ackSecretTransfer {
		return errors.New("--include-secret and --ack-secret-transfer must be used together so secret values are never selected implicitly")
	}
	store, err := a.migrationStore(context.Background())
	if err != nil {
		return err
	}
	names := environments.Values()
	if *all {
		if !*preview && !*yes && !a.isInteractiveTerminal() {
			return errors.New("non-interactive --all requires --preview or --yes so the concrete inventory cannot be accepted implicitly")
		}
		var summaries []manager.EnvironmentSummary
		if err := a.migrationAPI(store, http.MethodGet, "/api/v1/environments", nil, "environments", &summaries); err != nil {
			return err
		}
		for _, summary := range summaries {
			if summary.Name != "" {
				names = append(names, summary.Name)
			}
		}
	}
	names = normalizedMigrationStrings(names)
	if len(names) == 0 {
		return errors.New("select at least one environment with --environment, or use --all --preview")
	}
	output, err := filepath.Abs(*out)
	if err != nil {
		return err
	}
	request := migration.ExportRequest{
		Schema: manager.MigrationExportRequestSchema, Mode: exportMode,
		EnvironmentNames: names, IncludeSecretRefs: secretRefs,
		OutputPath: filepath.Clean(output), RiskAcknowledgements: []string{},
	}
	if exportMode == migration.ExportModeFull {
		request.RiskAcknowledgements = []string{manager.MigrationRiskOpaqueGuestContent}
	}
	if len(secretRefs) != 0 {
		request.RiskAcknowledgements = append(
			request.RiskAcknowledgements, manager.MigrationRiskSelectedSecrets,
		)
		sort.Strings(request.RiskAcknowledgements)
	}
	if exportMode == migration.ExportModeFull {
		ready, err := a.prepareMigrationExportQuiescence(
			store, names, *stop, *preview, *yes, *jsonOut,
		)
		if err != nil {
			return err
		}
		if !ready {
			return nil
		}
	}
	var plan migration.ExportPlan
	if err := a.migrationAPI(store, http.MethodPost, "/api/v1/migration/export/plan", request, "migration/export/plan", &plan); err != nil {
		return err
	}
	if *jsonOut && *preview {
		return writeIndentedJSON(a.stdout, plan)
	}
	if !*jsonOut {
		writeMigrationExportPlan(a.stdout, plan, names)
	}
	if *preview {
		return nil
	}
	if !*yes {
		confirmed, err := a.confirmMigration("Create this exact encrypted migration bundle?")
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Fprintln(a.stdout, "Cancelled; no bundle or operation was created.")
			return nil
		}
	}
	passphrase, err := a.readMigrationPassphrase(*passphraseStdin, true)
	if err != nil {
		return err
	}
	defer clear(passphrase)
	handle, err := a.createMigrationSecretInput(
		store, manager.MigrationSecretPurposeExportCreate, plan.OutputPath,
		passphrase, passphrase,
	)
	if err != nil {
		return err
	}
	key := *idempotency
	if key == "" {
		key, err = newMigrationCLIIdempotencyKey()
		if err != nil {
			return err
		}
	}
	apply := manager.MigrationExportApplyRequest{
		Schema: manager.MigrationExportApplySchema, Plan: plan,
		Confirmation: manager.MigrationPlanConfirmation{
			PlanDigest:                   plan.PlanDigest,
			AcceptedRiskAcknowledgements: append([]string(nil), plan.RiskAcknowledgements...),
		},
		SecretInputHandle: handle.Handle, IdempotencyKey: key,
	}
	var result manager.MigrationApplyResult
	if err := a.migrationAPI(store, http.MethodPost, "/api/v1/migration/export/apply", apply, "migration/export/apply", &result); err != nil {
		return err
	}
	if *jsonOut {
		return writeIndentedJSON(a.stdout, result)
	}
	writeMigrationApplyResult(a.stdout, result)
	return nil
}

// prepareMigrationExportQuiescence gives VM lifecycle changes their own
// review/confirmation boundary. The export plan is requested only after this
// action succeeds, so --yes for bundle creation never silently grants stop
// authority.
func (a app) prepareMigrationExportQuiescence(
	store profile.Store,
	names []string,
	allowStop, preview, yes, jsonOut bool,
) (bool, error) {
	var summaries []manager.EnvironmentSummary
	if err := a.migrationAPI(
		store, http.MethodGet, "/api/v1/environments", nil,
		"environments", &summaries,
	); err != nil {
		return false, err
	}
	selected, err := selectMigrationEnvironmentSummaries(summaries, names)
	if err != nil {
		return false, err
	}
	needsQuiescence := false
	ids := make([]string, 0, len(selected))
	for _, summary := range selected {
		ids = append(ids, summary.ID)
		if summary.Status != environment.StatusStopped {
			needsQuiescence = true
		}
	}
	if !needsQuiescence {
		return true, nil
	}

	request := manager.EnvironmentActionAPIRequest{IDs: ids}
	var plan manager.EnvironmentActionPlan
	if err := a.migrationEnvironmentActionAPI(
		store, "/api/v1/environment/stop/plan", request,
		"environment/stop/plan", &plan,
	); err != nil {
		return false, fmt.Errorf("prepare migration quiescence plan: %w", err)
	}
	if !daemonEnvironmentActionPlanMatches(
		plan, request, manager.EnvironmentActionStop,
	) {
		return false, errors.New("migration quiescence plan does not match the selected environments")
	}
	if jsonOut && preview {
		if err := writeIndentedJSON(a.stdout, plan); err != nil {
			return false, err
		}
	} else if !jsonOut {
		writeMigrationQuiescencePlan(a.stdout, plan)
	}
	for _, skipped := range plan.Skipped {
		if skipped.Reason != "already-stopped" {
			return false, fmt.Errorf(
				"environment %q cannot be made export-ready by this plan (%s); use --mode config or resolve its lifecycle first",
				migrationEnvironmentTargetLabel(skipped), skipped.Reason,
			)
		}
	}
	if len(plan.Targets) == 0 {
		return false, errors.New("selected environments are not all stopped, but no eligible stop action was available")
	}
	if preview {
		if !jsonOut {
			fmt.Fprintln(a.stdout, "Next: rerun the same command with --stop; add --yes only after reviewing both the stop and export plans.")
		}
		return false, nil
	}
	if !allowStop {
		return false, errors.New("full export requires a separate VM stop authorization; review with --preview, then rerun with --stop")
	}
	if !yes {
		confirmed, err := a.confirmMigration("Stop these exact VM incarnations for migration capture?")
		if err != nil {
			return false, err
		}
		if !confirmed {
			fmt.Fprintln(a.stdout, "Cancelled; no VM was stopped and no export was created.")
			return false, nil
		}
	}
	request.OperationID = plan.OperationID
	request.PlanDigest = plan.PlanDigest
	request.Confirmed = true
	var result manager.EnvironmentActionResult
	if err := a.migrationEnvironmentActionAPI(
		store, "/api/v1/environment/stop/apply", request,
		"environment/stop/apply", &result,
	); err != nil {
		return false, fmt.Errorf(
			"migration quiescence was not established; no snapshot was started: %w", err,
		)
	}
	if !daemonEnvironmentActionResultMatches(
		result, plan, manager.EnvironmentActionStop,
	) || len(result.Applied) != len(plan.Targets) {
		return false, errors.New("migration quiescence result does not prove the reviewed stop plan")
	}
	return true, nil
}

func selectMigrationEnvironmentSummaries(
	summaries []manager.EnvironmentSummary,
	names []string,
) ([]manager.EnvironmentSummary, error) {
	byName := make(map[string]manager.EnvironmentSummary, len(summaries))
	for _, summary := range summaries {
		if summary.Name != "" {
			byName[summary.Name] = summary
		}
	}
	selected := make([]manager.EnvironmentSummary, 0, len(names))
	for _, name := range names {
		summary, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("selected environment %q does not exist", name)
		}
		selected = append(selected, summary)
	}
	return selected, nil
}

func (a app) migrationEnvironmentActionAPI(
	store profile.Store,
	path string,
	request manager.EnvironmentActionAPIRequest,
	expectedResource string,
	out any,
) error {
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	defer clear(payload)
	call := a.migrationRequest
	if call == nil {
		call = a.migrationDaemonRequest
	}
	data, err := call(store.Root, http.MethodPost, path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	if len(data) > migrationCLIResponseLimit {
		return errors.New("environment action response exceeds the local client limit")
	}
	defer clear(data)
	var envelope migrationAPIEnvelope
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil ||
		envelope.Version != manager.APIVersion ||
		envelope.Resource != expectedResource {
		return errors.New("environment action Manager response contract mismatch")
	}
	if len(envelope.ErrorDetails) != 0 {
		if len(envelope.ErrorDetails) == 1 {
			if safe, ok := safeMigrationCLIAPIError(envelope.ErrorDetails[0]); ok {
				return safe
			}
		}
		return errors.New("environment action was rejected without a valid stable error")
	}
	if len(envelope.Errors) != 0 {
		return errors.New("environment action was rejected without a valid stable error")
	}
	if len(envelope.Data) == 0 || json.Unmarshal(envelope.Data, out) != nil {
		return errors.New("decode environment action Manager response data")
	}
	return nil
}

func writeMigrationQuiescencePlan(
	w io.Writer,
	plan manager.EnvironmentActionPlan,
) {
	fmt.Fprintln(w, "Migration VM stop preview:")
	for _, target := range plan.Targets {
		fmt.Fprintf(
			w, "  stop: %s (status=%s, instance=%s)\n",
			migrationEnvironmentTargetLabel(target), target.Status, target.InstanceName,
		)
	}
	for _, target := range plan.Skipped {
		fmt.Fprintf(
			w, "  unchanged: %s (%s)\n",
			migrationEnvironmentTargetLabel(target), target.Reason,
		)
	}
	fmt.Fprintf(w, "  Operation: %s\n  Plan digest: %s\n", plan.OperationID, plan.PlanDigest)
	fmt.Fprintln(w, "  No VM will be force-stopped or recreated; hideoutd remains running.")
}

func migrationEnvironmentTargetLabel(target manager.EnvironmentActionTarget) string {
	if target.Profile != "" {
		return target.Profile + "/" + target.ID
	}
	return target.ID
}

func (a app) migrateInspect(args []string) error {
	fs := flag.NewFlagSet("migrate inspect", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	passphraseStdin := fs.Bool("passphrase-stdin", false, "read passphrase from stdin")
	jsonOut := fs.Bool("json", false, "write canonical JSON")
	args, err := migrationInterspersedFlagArgs(args, nil)
	if err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		return errors.New("usage: hideout migrate inspect <bundle> [--passphrase-stdin] [--json]")
	}
	bundle, err := filepath.Abs(fs.Arg(0))
	if err != nil {
		return err
	}
	store, err := a.migrationStore(context.Background())
	if err != nil {
		return err
	}
	passphrase, err := a.readMigrationPassphrase(*passphraseStdin, false)
	if err != nil {
		return err
	}
	defer clear(passphrase)
	inspection, err := a.inspectMigrationBundle(store, filepath.Clean(bundle), passphrase)
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeIndentedJSON(a.stdout, inspection)
	}
	writeMigrationInspection(a.stdout, inspection)
	return nil
}

type migrateImportOptions struct {
	bundle             string
	environments       []string
	all                bool
	names              []string
	replacements       []string
	policies           []string
	workspaces         []string
	secretMappings     []string
	authorityApprovals []string
	acknowledgements   []string
	ackSecretTransfer  bool
	preview            bool
	yes                bool
	passphraseStdin    bool
	jsonOut            bool
	idempotency        string
}

func (a app) migrateImport(args []string) error {
	options, err := parseMigrateImportOptions(args)
	if err != nil {
		return err
	}
	store, err := a.migrationStore(context.Background())
	if err != nil {
		return err
	}
	passphrase, err := a.readMigrationPassphrase(options.passphraseStdin, false)
	if err != nil {
		return err
	}
	defer clear(passphrase)
	inspection, err := a.inspectMigrationBundle(store, options.bundle, passphrase)
	if err != nil {
		return err
	}
	if !options.jsonOut {
		writeMigrationInspection(a.stdout, inspection)
	}
	handle, err := a.createMigrationSecretInput(
		store, manager.MigrationSecretPurposeImport, options.bundle,
		passphrase, nil,
	)
	if err != nil {
		return err
	}
	draft, err := migrationImportDraftFromCLI(inspection, options)
	if err != nil {
		return err
	}
	request := manager.MigrationImportPlanAPIRequest{
		ImportDraft: draft, SecretInputHandle: handle.Handle,
	}
	var plan migration.ImportPlan
	if err := a.migrationAPI(store, http.MethodPost, "/api/v1/migration/import/plan", request, "migration/import/plan", &plan); err != nil {
		return err
	}
	if !options.jsonOut {
		writeMigrationImportPlan(a.stdout, plan)
	}
	if len(options.replacements) != 0 {
		replacementPlan, decisions, applied, err := a.prepareMigrationImportReplacements(
			store, plan, options.replacements, options.preview, options.yes,
			!options.jsonOut,
		)
		if err != nil {
			return err
		}
		if options.preview {
			if options.jsonOut {
				return writeIndentedJSON(a.stdout, struct {
					ImportPlan      migration.ImportPlan           `json:"importPlan"`
					ReplacementPlan *manager.EnvironmentActionPlan `json:"replacementPlan,omitempty"`
				}{ImportPlan: plan, ReplacementPlan: replacementPlan})
			}
			return nil
		}
		if !applied {
			return nil
		}
		draft.ConflictDecisions = decisions
		request.ImportDraft = draft
		if err := a.migrationAPI(
			store, http.MethodPost, "/api/v1/migration/import/plan", request,
			"migration/import/plan", &plan,
		); err != nil {
			return fmt.Errorf("re-plan import after the confirmed replacement: %w", err)
		}
		if !options.jsonOut {
			fmt.Fprintln(a.stdout, "Post-replacement import plan:")
			writeMigrationImportPlan(a.stdout, plan)
		}
	} else if options.jsonOut && options.preview {
		return writeIndentedJSON(a.stdout, plan)
	}
	if len(plan.Blockers) != 0 {
		return errors.New("migration import plan is blocked; resolve every listed blocker and create a new plan")
	}
	if options.preview {
		return nil
	}
	if !options.yes {
		confirmed, err := a.confirmMigration("Import and publish this exact verified plan?")
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Fprintln(a.stdout, "Cancelled; no destination object was created.")
			return nil
		}
	}
	key := options.idempotency
	if key == "" {
		key, err = newMigrationCLIIdempotencyKey()
		if err != nil {
			return err
		}
	}
	apply := manager.MigrationImportApplyRequest{
		Schema: manager.MigrationImportApplySchema, Plan: plan,
		Confirmation: manager.MigrationPlanConfirmation{
			PlanDigest:                   plan.PlanDigest,
			AcceptedRiskAcknowledgements: append([]string(nil), plan.RiskAcknowledgements...),
			ApprovedAuthorityProposalIDs: migrationApprovedAuthorityProposalIDsForCLI(
				plan.AuthorityActions,
			),
		},
		SecretInputHandle: handle.Handle, IdempotencyKey: key,
	}
	var result manager.MigrationApplyResult
	if err := a.migrationAPI(store, http.MethodPost, "/api/v1/migration/import/apply", apply, "migration/import/apply", &result); err != nil {
		return err
	}
	if options.jsonOut {
		return writeIndentedJSON(a.stdout, result)
	}
	writeMigrationApplyResult(a.stdout, result)
	return nil
}

// prepareMigrationImportReplacements gives deletion its own immutable Manager
// plan, confirmation, operation, and terminal proof. Import planning remains
// read-only and never receives ambient authority to overwrite a current owner.
func (a app) prepareMigrationImportReplacements(
	store profile.Store,
	plan migration.ImportPlan,
	sourceRefs []string,
	preview, yes, renderHuman bool,
) (*manager.EnvironmentActionPlan, []migration.ConflictDecision, bool, error) {
	requested := make(map[migration.OpaqueID]struct{}, len(sourceRefs))
	for _, raw := range sourceRefs {
		ref := migration.OpaqueID(raw)
		if _, err := migration.ParseOpaqueID(raw); err != nil {
			return nil, nil, false, fmt.Errorf("--replace source ref %q is invalid", raw)
		}
		if _, duplicate := requested[ref]; duplicate {
			return nil, nil, false, fmt.Errorf("--replace source ref %q is duplicated", raw)
		}
		requested[ref] = struct{}{}
	}
	conflicts := make(map[migration.OpaqueID]migration.ConflictAction, len(requested))
	for _, action := range plan.ConflictActions {
		if action.Kind != "environment-name" || action.Decision != "refuse" {
			continue
		}
		if _, selected := requested[action.SourceRef]; selected {
			conflicts[action.SourceRef] = action
		}
	}
	if len(conflicts) != len(requested) {
		return nil, nil, false, errors.New("every --replace source must have one current environment-name conflict in the authenticated import plan")
	}
	for _, blocker := range plan.Blockers {
		if blocker.Code != "migration.destination.name_conflict" {
			return nil, nil, false, errors.New("replacement was not started because the import plan has another blocker; resolve all path, secret, profile, authority, compatibility, and capacity blockers first")
		}
		if _, selected := requested[blocker.SourceRef]; !selected {
			return nil, nil, false, errors.New("replacement was not started because an unselected destination conflict remains")
		}
	}
	ids := make([]string, 0, len(conflicts))
	orderedRefs := make([]migration.OpaqueID, 0, len(conflicts))
	for ref, action := range conflicts {
		if action.ExistingStatus != environment.StatusStopped {
			return nil, nil, false, fmt.Errorf(
				"replacement target %s is %s; stop it first, then create a new preview",
				action.DestinationName, action.ExistingStatus,
			)
		}
		ids = append(ids, action.ExistingEnvironmentID)
		orderedRefs = append(orderedRefs, ref)
	}
	sort.Strings(ids)
	slices.Sort(orderedRefs)
	deleteRequest := manager.EnvironmentActionAPIRequest{IDs: ids}
	var deletePlan manager.EnvironmentActionPlan
	if err := a.migrationEnvironmentActionAPI(
		store, "/api/v1/environment/delete/plan", deleteRequest,
		"environment/delete/plan", &deletePlan,
	); err != nil {
		return nil, nil, false, fmt.Errorf("prepare separately confirmed replacement: %w", err)
	}
	if !daemonEnvironmentActionPlanMatches(
		deletePlan, deleteRequest, manager.EnvironmentActionDelete,
	) || deletePlan.Force || len(deletePlan.Targets) != len(ids) ||
		len(deletePlan.Skipped) != 0 {
		return nil, nil, false, errors.New("replacement delete plan does not exactly match the stopped conflict owners")
	}
	if renderHuman {
		writeMigrationReplacementPlan(a.stdout, deletePlan)
	}
	if !preview {
		if !yes {
			confirmed, err := a.confirmMigration(
				"Permanently delete these exact stopped destination VMs? This action has no automatic rollback; recovery requires a separately retained migration bundle.",
			)
			if err != nil {
				return nil, nil, false, err
			}
			if !confirmed {
				fmt.Fprintln(a.stdout, "Cancelled; no destination environment was deleted and no import was started.")
				return &deletePlan, nil, false, nil
			}
		}
		deleteRequest.OperationID = deletePlan.OperationID
		deleteRequest.PlanDigest = deletePlan.PlanDigest
		deleteRequest.Confirmed = true
		var result manager.EnvironmentActionResult
		if err := a.migrationEnvironmentActionAPI(
			store, "/api/v1/environment/delete/apply", deleteRequest,
			"environment/delete/apply", &result,
		); err != nil {
			return &deletePlan, nil, false, fmt.Errorf("replacement delete did not reach a proved terminal state: %w", err)
		}
		if !daemonEnvironmentActionResultMatches(
			result, deletePlan, manager.EnvironmentActionDelete,
		) || len(result.Applied) != len(deletePlan.Targets) {
			return &deletePlan, nil, false, errors.New("replacement delete result does not prove the reviewed lifecycle plan")
		}
	}
	if !preview && renderHuman {
		fmt.Fprintln(a.stdout, "Replacement lifecycle action completed:")
	}
	decisions := make([]migration.ConflictDecision, 0, len(orderedRefs))
	for _, ref := range orderedRefs {
		decisions = append(decisions, migration.ConflictDecision{
			SourceRef: ref, Decision: "replace",
			LifecycleOperationID: deletePlan.OperationID,
			LifecyclePlanDigest:  migration.Digest(deletePlan.PlanDigest),
		})
	}
	return &deletePlan, decisions, !preview, nil
}

func writeMigrationReplacementPlan(
	w io.Writer,
	plan manager.EnvironmentActionPlan,
) {
	fmt.Fprintln(w, "Separate destructive replacement plan:")
	for _, target := range plan.Targets {
		fmt.Fprintf(w, "  permanently delete: %s (status=%s, instance=%s)\n", target.ID, target.Status, target.InstanceName)
	}
	fmt.Fprintf(w, "  Operation: %s\n  Plan digest: %s\n", plan.OperationID, plan.PlanDigest)
	fmt.Fprintln(w, "  Rollback: none after deletion; recover by importing a separately retained migration bundle.")
}

func parseMigrateImportOptions(args []string) (migrateImportOptions, error) {
	var options migrateImportOptions
	fs := flag.NewFlagSet("migrate import", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var environments, names, replacements, policies, workspaces, secrets, approvals, acknowledgements stringListFlag
	fs.Var(&environments, "environment", "source environment ref; repeatable")
	fs.BoolVar(&options.all, "all", false, "select every authenticated environment in the bundle")
	fs.Var(&names, "name", "source=destination; repeatable")
	fs.Var(&replacements, "replace", "source ref whose exact existing environment may be deleted by a separate reviewed lifecycle action; repeatable")
	fs.Var(&policies, "policy", "source=guest identity policy; repeatable")
	fs.Var(&workspaces, "workspace", "proposal=path|disabled; repeatable")
	fs.Var(&secrets, "secret", "source=destination-ref; repeatable")
	fs.Var(&approvals, "approve", "proposal-id[=destination-json]; repeatable and exact")
	fs.Var(&acknowledgements, "ack", "exact risk code; repeatable")
	fs.BoolVar(&options.ackSecretTransfer, "ack-secret-transfer", false, "acknowledge importing explicitly bundled secret values")
	fs.BoolVar(&options.preview, "preview", false, "render the plan without applying")
	fs.BoolVar(&options.yes, "yes", false, "apply the exact displayed plan")
	fs.BoolVar(&options.passphraseStdin, "passphrase-stdin", false, "read passphrase from stdin")
	fs.BoolVar(&options.jsonOut, "json", false, "write canonical JSON")
	fs.StringVar(&options.idempotency, "idempotency-key", "", "reuse an exact client-generated apply key")
	args, err := migrationInterspersedFlagArgs(args, map[string]bool{
		"--environment": true, "--name": true, "--policy": true,
		"--replace":   true,
		"--workspace": true, "--secret": true, "--ack": true,
		"--approve":         true,
		"--idempotency-key": true,
	})
	if err != nil {
		return options, err
	}
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 || options.preview && options.yes {
		return options, errors.New("usage: hideout migrate import <bundle> [mapping flags] [--preview|--yes]")
	}
	if options.jsonOut && !options.preview && !options.yes {
		return options, errors.New("--json import requires --preview or --yes so prompts cannot corrupt JSON output")
	}
	if options.all && len(environments.Values()) != 0 {
		return options, errors.New("import scope is ambiguous: use either --all or repeated --environment <source-ref>, not both")
	}
	if options.yes && !options.all && len(environments.Values()) == 0 {
		return options, errors.New("non-interactive import apply requires an explicit scope; run `hideout migrate import <bundle> --preview`, then repeat with every chosen --environment <source-ref> or with --all --yes")
	}
	bundle, err := filepath.Abs(fs.Arg(0))
	if err != nil {
		return options, err
	}
	options.bundle = filepath.Clean(bundle)
	options.environments = environments.Values()
	options.names = names.Values()
	options.replacements = normalizedMigrationStrings(replacements.Values())
	options.policies = policies.Values()
	options.workspaces = workspaces.Values()
	options.secretMappings = secrets.Values()
	options.authorityApprovals = approvals.Values()
	options.acknowledgements = normalizedMigrationStrings(acknowledgements.Values())
	if options.ackSecretTransfer {
		options.acknowledgements = append(
			options.acknowledgements, manager.MigrationRiskSelectedSecrets,
		)
		options.acknowledgements = normalizedMigrationStrings(options.acknowledgements)
	}
	return options, nil
}

func (a app) migrateStatus(args []string) error {
	fs := flag.NewFlagSet("migrate status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "write canonical JSON")
	args, err := migrationInterspersedFlagArgs(args, nil)
	if err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil || fs.NArg() > 1 {
		return errors.New("usage: hideout migrate status [operation-id] [--json]")
	}
	store, err := a.migrationStore(context.Background())
	if err != nil {
		return err
	}
	resource := "/api/v1/migration/operations"
	expected := "migration/operations"
	if fs.NArg() == 1 {
		resource += "/" + fs.Arg(0)
		expected = "migration/operation"
		var projection manager.MigrationOperationProjection
		if err := a.migrationAPI(store, http.MethodGet, resource, nil, expected, &projection); err != nil {
			return err
		}
		if *jsonOut {
			return writeIndentedJSON(a.stdout, projection)
		}
		writeMigrationOperation(a.stdout, projection)
		return nil
	}
	var projections []manager.MigrationOperationProjection
	if err := a.migrationAPI(store, http.MethodGet, resource, nil, expected, &projections); err != nil {
		return err
	}
	if *jsonOut {
		return writeIndentedJSON(a.stdout, projections)
	}
	if len(projections) == 0 {
		fmt.Fprintln(a.stdout, "No migration operations.")
		return nil
	}
	for _, projection := range projections {
		writeMigrationOperation(a.stdout, projection)
	}
	return nil
}

func (a app) migrateResume(args []string) error {
	fs := flag.NewFlagSet("migrate resume", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	passphraseStdin := fs.Bool("passphrase-stdin", false, "read passphrase from stdin")
	jsonOut := fs.Bool("json", false, "write canonical JSON")
	args, err := migrationInterspersedFlagArgs(args, nil)
	if err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		return errors.New("usage: hideout migrate resume <operation-id> [--passphrase-stdin] [--json]")
	}
	store, err := a.migrationStore(context.Background())
	if err != nil {
		return err
	}
	operation, err := a.fetchMigrationOperation(store, fs.Arg(0))
	if err != nil {
		return err
	}
	if len(operation.Recovery.AllowedActions) != 1 ||
		operation.Recovery.AllowedActions[0] != manager.MigrationRecoveryResume {
		return errors.New("this operation does not currently advertise resume; inspect `hideout migrate status <operation-id>`")
	}
	passphrase, err := a.readMigrationPassphrase(*passphraseStdin, false)
	if err != nil {
		return err
	}
	defer clear(passphrase)
	handle, err := a.createMigrationResumeSecretInput(
		store, operation.OperationID, operation.Kind, passphrase,
	)
	if err != nil {
		return err
	}
	var updated manager.MigrationOperationProjection
	err = a.migrationAPI(
		store, http.MethodPost,
		"/api/v1/migration/operations/"+operation.OperationID+"/resume",
		manager.MigrationOperationActionAPIRequest{
			Revision: operation.Revision, SecretInputHandle: handle.Handle,
		},
		"migration/operation", &updated,
	)
	if err != nil {
		return err
	}
	return writeMigrationOperationResult(a.stdout, updated, *jsonOut)
}

func (a app) migrateCancel(args []string) error {
	fs := flag.NewFlagSet("migrate cancel", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	retainPartial := fs.Bool("retain-partial", false, "retain an export partial for inspection")
	removePartial := fs.Bool("remove-partial", false, "remove an operation-owned export partial")
	yes := fs.Bool("yes", false, "confirm the displayed cancellation effect")
	jsonOut := fs.Bool("json", false, "write canonical JSON")
	args, err := migrationInterspersedFlagArgs(args, nil)
	if err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 ||
		(*retainPartial && *removePartial) || (*jsonOut && !*yes) {
		return errors.New("usage: hideout migrate cancel <operation-id> (--retain-partial|--remove-partial) [--yes] [--json]")
	}
	store, err := a.migrationStore(context.Background())
	if err != nil {
		return err
	}
	operation, err := a.fetchMigrationOperation(store, fs.Arg(0))
	if err != nil {
		return err
	}
	var retain *bool
	impact := "roll back only this operation's unpublished staged destination data"
	if operation.Kind == manager.MigrationOperationExport {
		if *retainPartial == *removePartial {
			return errors.New("export cancellation requires exactly one of --retain-partial or --remove-partial; there is no deletion default")
		}
		value := *retainPartial
		retain = &value
		if value {
			impact = "retain the operation-owned partial bundle; release its snapshot and claims"
		} else {
			impact = "remove the operation-owned partial bundle; release its snapshot and claims"
		}
	} else if *retainPartial || *removePartial {
		return errors.New("partial-file choices apply only to export cancellation")
	}
	if !*jsonOut {
		fmt.Fprintf(a.stdout, "Cancellation preview for %s:\n  %s\n  Published bundles and pre-existing destinations are never removed.\n", operation.OperationID, impact)
	}
	if !*yes {
		confirmed, err := a.confirmMigration("Request this exact cancellation?")
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Fprintln(a.stdout, "Cancelled; the migration operation was not changed.")
			return nil
		}
	}
	var updated manager.MigrationOperationProjection
	err = a.migrationAPI(
		store, http.MethodPost,
		"/api/v1/migration/operations/"+operation.OperationID+"/cancel",
		manager.MigrationOperationActionAPIRequest{
			Revision: operation.Revision, RetainPartial: retain,
		},
		"migration/operation", &updated,
	)
	if err != nil {
		return err
	}
	return writeMigrationOperationResult(a.stdout, updated, *jsonOut)
}

func (a app) migrateRecover(args []string) error {
	fs := flag.NewFlagSet("migrate recover", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	actionFlag := fs.String("action", "", "exact currently-advertised action")
	yes := fs.Bool("yes", false, "confirm the displayed recovery effect")
	jsonOut := fs.Bool("json", false, "write canonical JSON")
	args, err := migrationInterspersedFlagArgs(args, map[string]bool{"--action": true})
	if err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 || (*jsonOut && !*yes) {
		return errors.New("usage: hideout migrate recover <operation-id> [--action finish|rollback|remove-partial] [--yes] [--json]")
	}
	store, err := a.migrationStore(context.Background())
	if err != nil {
		return err
	}
	operation, err := a.fetchMigrationOperation(store, fs.Arg(0))
	if err != nil {
		return err
	}
	if len(operation.Recovery.AllowedActions) != 1 {
		return errors.New("this operation has no single Manager-advertised recovery action")
	}
	action := operation.Recovery.AllowedActions[0]
	if *actionFlag != "" && manager.MigrationRecoveryAction(*actionFlag) != action {
		return errors.New("--action does not match the current Manager-advertised recovery action; refresh status")
	}
	if action == manager.MigrationRecoveryResume {
		return errors.New("this operation needs a protected re-unlock; use `hideout migrate resume <operation-id>`")
	}
	if action != manager.MigrationRecoveryFinish &&
		action != manager.MigrationRecoveryRollback &&
		action != manager.MigrationRecoveryRemovePartial {
		return errors.New("the advertised recovery action requires manual operator intervention")
	}
	if !*jsonOut {
		fmt.Fprintf(a.stdout, "Recovery preview for %s:\n  action: %s\n  %s\n", operation.OperationID, action, operation.Recovery.NextAction)
	}
	if !*yes {
		confirmed, err := a.confirmMigration("Run this exact advertised recovery action?")
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Fprintln(a.stdout, "Cancelled; recovery was not started.")
			return nil
		}
	}
	var updated manager.MigrationOperationProjection
	err = a.migrationAPI(
		store, http.MethodPost,
		"/api/v1/migration/operations/"+operation.OperationID+"/recover",
		manager.MigrationOperationActionAPIRequest{
			Revision: operation.Revision, Action: action,
		},
		"migration/operation", &updated,
	)
	if err != nil {
		return err
	}
	return writeMigrationOperationResult(a.stdout, updated, *jsonOut)
}

func (a app) fetchMigrationOperation(
	store profile.Store,
	operationID string,
) (manager.MigrationOperationProjection, error) {
	var projection manager.MigrationOperationProjection
	err := a.migrationAPI(
		store, http.MethodGet, "/api/v1/migration/operations/"+operationID,
		nil, "migration/operation", &projection,
	)
	return projection, err
}

func writeMigrationOperationResult(
	w io.Writer,
	projection manager.MigrationOperationProjection,
	jsonOut bool,
) error {
	if jsonOut {
		return writeIndentedJSON(w, projection)
	}
	writeMigrationOperation(w, projection)
	return nil
}

// migrationInterspersedFlagArgs preserves the documented CLI shape where
// flags may appear before or after the bundle/operation positional. The
// standard library flag parser stops at the first positional, so normalize
// only the declared long flags before handing it the same arguments.
func migrationInterspersedFlagArgs(
	args []string,
	valueFlags map[string]bool,
) ([]string, error) {
	flags := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		value := args[index]
		if value == "--" {
			positionals = append(positionals, args[index+1:]...)
			break
		}
		if !strings.HasPrefix(value, "-") || value == "-" {
			positionals = append(positionals, value)
			continue
		}
		flags = append(flags, value)
		name := value
		if separator := strings.IndexByte(name, '='); separator >= 0 {
			name = name[:separator]
			continue
		}
		if !valueFlags[name] {
			continue
		}
		if index+1 >= len(args) || args[index+1] == "--" {
			return nil, fmt.Errorf("%s requires a value", name)
		}
		index++
		flags = append(flags, args[index])
	}
	return append(flags, positionals...), nil
}

func (a app) migrationStore(ctx context.Context) (profile.Store, error) {
	store, err := profile.DefaultStore()
	if err != nil {
		return profile.Store{}, err
	}
	if a.migrationRequest != nil && a.ensureDaemon == nil {
		return store, nil
	}
	executableFn := a.daemonExecutable
	if executableFn == nil {
		executableFn = os.Executable
	}
	executable, err := executableFn()
	if err != nil {
		return profile.Store{}, fmt.Errorf("resolve hideout executable: %w", err)
	}
	ensure := a.ensureDaemon
	if ensure == nil {
		ensure = daemon.EnsureStarted
	}
	if _, err := ensure(ctx, daemon.EnsureStartedOptions{
		Store: store, Executable: executable, BuildID: daemonBuildID(), Diagnostics: a.stderr,
	}); err != nil {
		return profile.Store{}, fmt.Errorf("migration requires the running Hideout daemon: %w", err)
	}
	return store, nil
}

func (a app) migrationAPI(
	store profile.Store,
	method string,
	path string,
	request any,
	expectedResource string,
	out any,
) error {
	var payload []byte
	var err error
	if request != nil {
		payload, err = json.Marshal(request)
		if err != nil {
			return err
		}
		defer clear(payload)
	}
	call := a.migrationRequest
	if call == nil {
		call = a.migrationDaemonRequest
	}
	var body io.Reader
	if request != nil {
		body = bytes.NewReader(payload)
	}
	data, err := call(store.Root, method, path, body)
	if err != nil {
		return err
	}
	if len(data) > migrationCLIResponseLimit {
		return errors.New("migration response exceeds the local client limit")
	}
	defer clear(data)
	var envelope migrationAPIEnvelope
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return errors.New("decode migration Manager response")
	}
	if envelope.Version != manager.APIVersion {
		return errors.New("migration Manager response contract mismatch")
	}
	if len(envelope.Errors) != 0 || len(envelope.ErrorDetails) != 0 {
		if len(envelope.ErrorDetails) == 1 {
			if safe, ok := safeMigrationCLIAPIError(envelope.ErrorDetails[0]); ok {
				return safe
			}
		}
		return errors.New("migration Manager rejected the request without a valid stable error")
	}
	if envelope.Resource != expectedResource || len(envelope.Data) == 0 {
		return errors.New("migration Manager response contract mismatch")
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return errors.New("decode migration Manager response data")
	}
	return nil
}

func (a app) migrationDaemonRequest(
	storeRoot, method, requestPath string,
	body io.Reader,
) ([]byte, error) {
	client, base, token, err := daemon.DialClient(storeRoot)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequest(method, base+requestPath, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Host = "localhost"
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("migration Manager is not reachable: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, migrationCLIResponseLimit+1))
	if err != nil {
		clear(data)
		return nil, errors.New("read migration Manager response")
	}
	if len(data) > migrationCLIResponseLimit {
		clear(data)
		return nil, errors.New("migration response exceeds the local client limit")
	}
	if len(data) == 0 && (response.StatusCode < 200 || response.StatusCode >= 300) {
		return nil, fmt.Errorf("migration Manager request failed (%s)", response.Status)
	}
	return data, nil
}

func (a app) createMigrationSecretInput(
	store profile.Store,
	purpose manager.MigrationSecretPurpose,
	bundlePath string,
	passphrase []byte,
	confirmation []byte,
) (manager.MigrationSecretInputHandle, error) {
	request := manager.MigrationSecretInputAPIRequest{
		Purpose: purpose, BundlePath: bundlePath,
		Passphrase: string(passphrase), Confirmation: string(confirmation),
	}
	var handle manager.MigrationSecretInputHandle
	err := a.migrationAPI(
		store, http.MethodPost, "/api/v1/migration/secret-input", request,
		"migration/secret-input", &handle,
	)
	request.Passphrase = ""
	request.Confirmation = ""
	return handle, err
}

func (a app) createMigrationResumeSecretInput(
	store profile.Store,
	operationID string,
	kind manager.MigrationOperationKind,
	passphrase []byte,
) (manager.MigrationSecretInputHandle, error) {
	purpose := manager.MigrationSecretPurposeExportResume
	if kind == manager.MigrationOperationImport {
		purpose = manager.MigrationSecretPurposeImport
	} else if kind != manager.MigrationOperationExport {
		return manager.MigrationSecretInputHandle{}, errors.New("unsupported migration resume kind")
	}
	request := manager.MigrationSecretInputAPIRequest{
		Purpose:     purpose,
		OperationID: operationID, Passphrase: string(passphrase),
	}
	var handle manager.MigrationSecretInputHandle
	err := a.migrationAPI(
		store, http.MethodPost, "/api/v1/migration/secret-input", request,
		"migration/secret-input", &handle,
	)
	request.Passphrase = ""
	return handle, err
}

func (a app) inspectMigrationBundle(
	store profile.Store,
	bundlePath string,
	passphrase []byte,
) (manager.MigrationReadOnlyInspection, error) {
	handle, err := a.createMigrationSecretInput(
		store, manager.MigrationSecretPurposeInspect, bundlePath, passphrase, nil,
	)
	if err != nil {
		return manager.MigrationReadOnlyInspection{}, err
	}
	var inspection manager.MigrationReadOnlyInspection
	err = a.migrationAPI(
		store, http.MethodPost, "/api/v1/migration/import/inspect",
		manager.MigrationInspectAPIRequest{
			BundlePath: bundlePath, SecretInputHandle: handle.Handle,
		},
		"migration/import/inspect", &inspection,
	)
	return inspection, err
}

func (a app) readMigrationPassphrase(useStdin, confirm bool) ([]byte, error) {
	read := func(prompt string) ([]byte, error) {
		if useStdin {
			raw, err := io.ReadAll(io.LimitReader(a.stdin, migration.MaxPassphraseBytes+2))
			if err != nil {
				clear(raw)
				return nil, errors.New("read migration passphrase from stdin")
			}
			raw = trimOneSecretLineEnding(raw)
			if len(raw) == 0 || len(raw) > migration.MaxPassphraseBytes {
				clear(raw)
				return nil, errors.New("migration passphrase must be non-empty and bounded")
			}
			return raw, nil
		}
		if !a.isInteractiveTerminal() {
			return nil, errors.New("migration passphrase requires a terminal; pipe it with --passphrase-stdin after reviewing the plan")
		}
		fmt.Fprint(a.stderr, prompt)
		var raw []byte
		var err error
		if a.secretReadPassword != nil {
			raw, err = a.secretReadPassword()
		} else if input, ok := a.stdin.(*os.File); ok && term.IsTerminal(int(input.Fd())) {
			raw, err = term.ReadPassword(int(input.Fd()))
		} else {
			err = errors.New("migration terminal input is unavailable")
		}
		fmt.Fprintln(a.stderr)
		if err != nil || len(raw) == 0 || len(raw) > migration.MaxPassphraseBytes {
			clear(raw)
			return nil, errors.New("read hidden migration passphrase")
		}
		return raw, nil
	}
	first, err := read("Migration bundle passphrase (input hidden): ")
	if err != nil {
		return nil, err
	}
	if !confirm || useStdin {
		return first, nil
	}
	second, err := read("Confirm migration bundle passphrase: ")
	if err != nil {
		clear(first)
		return nil, err
	}
	defer clear(second)
	if len(first) != len(second) || subtle.ConstantTimeCompare(first, second) != 1 {
		clear(first)
		return nil, errors.New("migration passphrase confirmation did not match")
	}
	return first, nil
}

func (a app) confirmMigration(prompt string) (bool, error) {
	if !a.isInteractiveTerminal() {
		return false, errors.New("migration apply requires an interactive confirmation or --yes after reviewing --preview")
	}
	fmt.Fprintf(a.stdout, "%s [y/N]: ", prompt)
	return readSecretConfirmation(a.stdin)
}

func migrationImportDraftFromCLI(
	inspection manager.MigrationReadOnlyInspection,
	options migrateImportOptions,
) (migration.ImportDraft, error) {
	available := make(map[migration.OpaqueID]manager.MigrationBundleEnvironmentProjection)
	for _, environment := range inspection.Inventory.Environments {
		available[environment.SourceRef] = environment
	}
	selected := make([]migration.OpaqueID, 0, len(available))
	if len(options.environments) == 0 {
		for ref := range available {
			selected = append(selected, ref)
		}
	} else {
		for _, raw := range options.environments {
			ref := migration.OpaqueID(raw)
			if _, exists := available[ref]; !exists {
				return migration.ImportDraft{}, fmt.Errorf("--environment source ref %q is not in the authenticated bundle", raw)
			}
			selected = append(selected, ref)
		}
	}
	slices.Sort(selected)
	selected = slices.Compact(selected)
	nameOverrides, err := migrationMappingValues(options.names)
	if err != nil {
		return migration.ImportDraft{}, fmt.Errorf("--name: %w", err)
	}
	policyOverrides, err := migrationMappingValues(options.policies)
	if err != nil {
		return migration.ImportDraft{}, fmt.Errorf("--policy: %w", err)
	}
	workspaceOverrides, err := migrationMappingValues(options.workspaces)
	if err != nil {
		return migration.ImportDraft{}, fmt.Errorf("--workspace: %w", err)
	}
	secretOverrides, err := migrationMappingValues(options.secretMappings)
	if err != nil {
		return migration.ImportDraft{}, fmt.Errorf("--secret: %w", err)
	}
	authorityApprovals, err := migrationAuthorityApprovalValues(
		options.authorityApprovals, inspection.Inventory.AuthorityProposals,
	)
	if err != nil {
		return migration.ImportDraft{}, fmt.Errorf("--approve: %w", err)
	}
	draft := migration.ImportDraft{
		Schema: manager.MigrationImportDraftSchema, BundlePath: options.bundle,
		BundleBinding: inspection.Binding, SelectedEnvironmentRefs: selected,
		NameMappings: []migration.NameMapping{}, ConflictDecisions: []migration.ConflictDecision{},
		WorkspaceMappings: []migration.WorkspaceMapping{},
		SecretMappings:    []migration.SecretMapping{}, IdentityPolicies: []migration.IdentitySelection{},
		AuthorityDecisions:   []migration.AuthorityDecision{},
		RiskAcknowledgements: append([]string(nil), options.acknowledgements...),
	}
	replacementRefs := make(map[string]struct{}, len(options.replacements))
	for _, raw := range options.replacements {
		ref := migration.OpaqueID(raw)
		if _, exists := available[ref]; !exists || !slices.Contains(selected, ref) {
			return migration.ImportDraft{}, fmt.Errorf("--replace source ref %q is not in the selected authenticated inventory", raw)
		}
		if _, duplicate := replacementRefs[raw]; duplicate {
			return migration.ImportDraft{}, fmt.Errorf("--replace source ref %q is duplicated", raw)
		}
		if _, renamed := nameOverrides[raw]; renamed {
			return migration.ImportDraft{}, fmt.Errorf("--replace and --name cannot both select source ref %q", raw)
		}
		replacementRefs[raw] = struct{}{}
	}
	for _, ref := range selected {
		environment := available[ref]
		name := environment.DisplayNameHint
		if override, exists := nameOverrides[string(ref)]; exists {
			name = override
			delete(nameOverrides, string(ref))
		}
		policy := migration.GuestIdentitySafeClone
		if override, exists := policyOverrides[string(ref)]; exists {
			policy = migration.GuestIdentityPolicy(override)
			delete(policyOverrides, string(ref))
		}
		if policy != migration.GuestIdentitySafeClone && policy != migration.GuestIdentityExactRestore {
			return migration.ImportDraft{}, fmt.Errorf("unsupported guest identity policy %q", policy)
		}
		draft.NameMappings = append(draft.NameMappings, migration.NameMapping{
			SourceRef: ref, DestinationName: name,
		})
		draft.IdentityPolicies = append(draft.IdentityPolicies, migration.IdentitySelection{
			SourceRef: ref, Policy: policy,
		})
		for _, proposal := range environment.WorkspaceProposals {
			value, exists := workspaceOverrides[string(proposal.ProposalID)]
			if exists {
				delete(workspaceOverrides, string(proposal.ProposalID))
			}
			mapping := migration.WorkspaceMapping{
				ProposalID: proposal.ProposalID, Decision: "disabled",
			}
			if exists && value != "disabled" {
				path, err := filepath.Abs(value)
				if err != nil {
					return migration.ImportDraft{}, err
				}
				mapping.Decision = "mapped"
				mapping.DestinationPath = filepath.Clean(path)
			}
			draft.WorkspaceMappings = append(draft.WorkspaceMappings, mapping)
		}
		for _, proposalID := range environment.AuthorityProposalIDs {
			decision := migration.AuthorityDecision{
				ProposalID: proposalID, Decision: "disabled",
			}
			if destinationValue, approved := authorityApprovals[proposalID]; approved {
				decision.Decision = "approved"
				decision.DestinationValue = destinationValue
				delete(authorityApprovals, proposalID)
			}
			draft.AuthorityDecisions = append(draft.AuthorityDecisions, decision)
		}
	}
	if len(nameOverrides) != 0 || len(policyOverrides) != 0 ||
		len(workspaceOverrides) != 0 || len(authorityApprovals) != 0 {
		return migration.ImportDraft{}, errors.New("one or more mapping keys do not belong to the selected authenticated inventory")
	}
	for _, secret := range inspection.Inventory.Secrets {
		mapping := migration.SecretMapping{
			SourceRef: secret.SecretRef, Decision: "unresolved",
		}
		if destination, exists := secretOverrides[string(secret.SecretRef)]; exists {
			if imported, found := strings.CutPrefix(destination, "import:"); found {
				if !secret.ValueIncluded || strings.TrimSpace(imported) == "" {
					return migration.ImportDraft{}, fmt.Errorf(
						"--secret %s requests an imported value that is not present in the authenticated bundle",
						secret.SecretRef,
					)
				}
				mapping.Decision = "import-value"
				mapping.DestinationRef = strings.TrimSpace(imported)
			} else {
				mapping.Decision = "existing-ref"
				mapping.DestinationRef = destination
			}
			delete(secretOverrides, string(secret.SecretRef))
		}
		draft.SecretMappings = append(draft.SecretMappings, mapping)
	}
	if len(secretOverrides) != 0 {
		return migration.ImportDraft{}, errors.New("one or more --secret keys are not in the authenticated inventory")
	}
	hasImportedSecret := false
	for _, mapping := range draft.SecretMappings {
		hasImportedSecret = hasImportedSecret || mapping.Decision == "import-value"
	}
	if hasImportedSecret != options.ackSecretTransfer {
		return migration.ImportDraft{}, errors.New(
			"import:<new-ref> secret mappings and --ack-secret-transfer must be used together",
		)
	}
	sort.Slice(draft.WorkspaceMappings, func(i, j int) bool {
		return draft.WorkspaceMappings[i].ProposalID < draft.WorkspaceMappings[j].ProposalID
	})
	sort.Slice(draft.AuthorityDecisions, func(i, j int) bool {
		return draft.AuthorityDecisions[i].ProposalID < draft.AuthorityDecisions[j].ProposalID
	})
	sort.Slice(draft.SecretMappings, func(i, j int) bool {
		return draft.SecretMappings[i].SourceRef < draft.SecretMappings[j].SourceRef
	})
	return draft, nil
}

func migrationMappingValues(values []string) (map[string]string, error) {
	out := make(map[string]string, len(values))
	for _, value := range values {
		key, mapped, ok := strings.Cut(value, "=")
		key = strings.TrimSpace(key)
		mapped = strings.TrimSpace(mapped)
		if !ok || key == "" || mapped == "" {
			return nil, errors.New("each mapping must be KEY=VALUE")
		}
		if _, exists := out[key]; exists {
			return nil, fmt.Errorf("mapping key %q is repeated", key)
		}
		out[key] = mapped
	}
	return out, nil
}

func migrationAuthorityApprovalValues(
	values []string,
	proposals []manager.MigrationBundleAuthorityProjection,
) (map[migration.OpaqueID]string, error) {
	available := make(map[migration.OpaqueID]string, len(proposals))
	for _, proposal := range proposals {
		available[proposal.ProposalID] = proposal.SourceSummary
	}
	approved := make(map[migration.OpaqueID]string, len(values))
	for _, raw := range values {
		proposalIDText, destinationValue, hasValue := strings.Cut(raw, "=")
		proposalID := migration.OpaqueID(strings.TrimSpace(proposalIDText))
		if proposalID == "" {
			return nil, errors.New("proposal ID is required")
		}
		if _, duplicate := approved[proposalID]; duplicate {
			return nil, fmt.Errorf("proposal %q is repeated", proposalID)
		}
		sourceSummary, exists := available[proposalID]
		if !exists {
			return nil, fmt.Errorf("proposal %q is not in the authenticated bundle", proposalID)
		}
		if !hasValue {
			destinationValue = sourceSummary
		} else {
			destinationValue = strings.TrimSpace(destinationValue)
		}
		if destinationValue == "" {
			return nil, fmt.Errorf("proposal %q requires a destination value", proposalID)
		}
		approved[proposalID] = destinationValue
	}
	return approved, nil
}

func migrationApprovedAuthorityProposalIDsForCLI(
	actions []migration.AuthorityAction,
) []migration.OpaqueID {
	ids := make([]migration.OpaqueID, len(actions))
	for index, action := range actions {
		ids[index] = action.ProposalID
	}
	return ids
}

func newMigrationCLIIdempotencyKey() (string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	defer clear(raw)
	return "migcli_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func normalizedMigrationStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return slices.Compact(out)
}

func joinMigrationModes(modes []migration.ExportMode) string {
	values := make([]string, len(modes))
	for index, mode := range modes {
		values[index] = string(mode)
	}
	return strings.Join(values, ", ")
}

func writeMigrationExportPlan(w io.Writer, plan migration.ExportPlan, names []string) {
	fmt.Fprintf(w, "Migration export preview (%s)\n", plan.Mode)
	fmt.Fprintf(w, "  Environments (%d): %s\n", len(names), strings.Join(names, ", "))
	fmt.Fprintf(w, "  Included: %s\n", strings.Join(plan.IncludedClasses, ", "))
	if plan.EstimatedPayloadComplete {
		fmt.Fprintf(w, "  Payload estimate: %d bytes (complete logical payload)\n", plan.EstimatedPayloadLogicalBytes)
	} else {
		fmt.Fprintf(w, "  Payload estimate: at least %d bytes (selected secret value sizes hidden)\n", plan.EstimatedPayloadLogicalBytes)
	}
	for _, estimate := range plan.EnvironmentEstimates {
		diskRefs := make([]string, len(estimate.DiskRefs))
		for index, ref := range estimate.DiskRefs {
			diskRefs[index] = string(ref)
		}
		if len(diskRefs) == 0 {
			diskRefs = []string{"none"}
		}
		fmt.Fprintf(
			w,
			"  Environment %s (%s): %d bytes; portable config=%d bytes; profile state=%d bytes; disks=%s\n",
			estimate.DisplayName, estimate.EnvironmentRef, estimate.EstimatedLogicalBytes,
			estimate.PortableConfigLogicalBytes, estimate.ProfileStateLogicalBytes,
			strings.Join(diskRefs, ", "),
		)
	}
	for _, estimate := range plan.DiskEstimates {
		consumers := make([]string, len(estimate.Consumers))
		for index, ref := range estimate.Consumers {
			consumers[index] = string(ref)
		}
		fmt.Fprintf(
			w,
			"  Disk %s (%s): logical=%d bytes; allocated hint=%d bytes; used by=%s\n",
			estimate.DiskRef, estimate.Role, estimate.LogicalBytes,
			estimate.AllocatedBytesHint, strings.Join(consumers, ", "),
		)
	}
	fmt.Fprintf(w, "  Persistent disks: %d\n", len(plan.DiskRefs))
	fmt.Fprintf(w, "  Output: %s\n", plan.OutputPath)
	fmt.Fprintf(w, "  Always excluded: %s\n", strings.Join(plan.ExcludedClasses, ", "))
	for _, warning := range plan.Warnings {
		fmt.Fprintf(w, "  Warning [%s]: %s\n", warning.Code, warning.Summary)
	}
	fmt.Fprintf(w, "  Confirmation: %s\n", plan.ConfirmationText)
}

func writeMigrationInspection(w io.Writer, inspection manager.MigrationReadOnlyInspection) {
	value := inspection.Inventory
	fmt.Fprintf(w, "Migration bundle %s (format %d, sealed=%t)\n", value.BundleID, value.FormatVersion, value.Sealed)
	fmt.Fprintf(w, "  Source: Hideout %s, %s/%s, %s %s\n", value.Source.ProductVersion, value.Source.HostOS, value.Source.HostArch, value.Source.Backend, value.Source.BackendVersion)
	fmt.Fprintf(w, "  Size: encoded=%d logical=%d bytes\n", value.EncodedBytes, value.LogicalBytes)
	fmt.Fprintf(
		w,
		"  Components: profiles=%d profile application states=%d environments=%d disks=%d secret values=%d provider metadata=%d total=%d\n",
		value.Components.Profiles, value.Components.ProfileStates,
		value.Components.Environments, value.Components.Disks,
		value.Components.SecretValues, value.Components.ProviderMetadata,
		value.Components.Total,
	)
	for _, environment := range value.Environments {
		fmt.Fprintf(w, "  Environment: %s  source=%s  disks=%d\n", environment.DisplayNameHint, environment.SourceRef, len(environment.DiskIDs))
	}
	fmt.Fprintf(w, "  Excluded: %s\n", strings.Join(value.ExcludedClasses, ", "))
	if len(value.Secrets) != 0 {
		fmt.Fprintf(w, "  Secret references requiring review: %d (values are never displayed)\n", len(value.Secrets))
	}
	for _, warning := range value.Warnings {
		fmt.Fprintf(w, "  Warning [%s]: %s\n", warning.Code, warning.Summary)
	}
}

func writeMigrationImportPlan(w io.Writer, plan migration.ImportPlan) {
	fmt.Fprintf(w, "Migration import preview\n")
	for _, object := range plan.Objects {
		fmt.Fprintf(w, "  %s -> %s\n", object.SourceRef, object.DestinationName)
	}
	for _, identity := range plan.IdentityActions {
		fmt.Fprintf(w, "  Guest identity %s: %s (Hideout/control/backend identities are always fresh)\n", identity.SourceRef, identity.GuestPolicy)
	}
	fmt.Fprintf(w, "  Disabled authority proposals: %d\n", len(plan.DisabledProposals))
	for _, blocker := range plan.Blockers {
		fmt.Fprintf(w, "  BLOCKED [%s]: %s", blocker.Code, blocker.Summary)
		if blocker.Remediation != "" {
			fmt.Fprintf(w, " — %s", blocker.Remediation)
		}
		fmt.Fprintln(w)
	}
	for _, risk := range plan.RiskAcknowledgements {
		fmt.Fprintf(w, "  Accepted exact risk: %s\n", risk)
	}
}

func writeMigrationApplyResult(w io.Writer, result manager.MigrationApplyResult) {
	fmt.Fprintf(w, "Migration operation %s accepted (state=%s).\n", result.OperationID, result.State)
	fmt.Fprintf(w, "Next: %s\n", result.Next)
}

func writeMigrationOperation(w io.Writer, projection manager.MigrationOperationProjection) {
	progress := projection.Progress
	total := "unknown"
	if progress.LogicalTotalKnown {
		total = fmt.Sprintf("%d", progress.TotalLogicalBytes)
	}
	remaining := "unknown"
	if progress.RemainingKnown {
		remaining = (time.Duration(progress.RemainingSeconds) * time.Second).String()
	}
	fmt.Fprintf(w, "%s  %s/%s  phase=%s\n", projection.OperationID, projection.Kind, projection.State, projection.PhaseLabel)
	fmt.Fprintf(w, "  progress: %d/%s bytes, components %d/%d, remaining %s\n", progress.CompletedLogicalBytes, total, progress.ComponentsComplete, progress.ComponentsTotal, remaining)
	if projection.Recovery.Required {
		fmt.Fprintf(w, "  recovery: %s — %s\n", projection.Recovery.Code, projection.Recovery.NextAction)
	}
	if projection.TerminalReceipt != nil {
		receipt := projection.TerminalReceipt
		fmt.Fprintf(
			w, "  receipt: %s, effects succeeded=%t, claims released=%t, completed=%s\n",
			receipt.ResultCode, receipt.AllEffectsSucceeded, receipt.ClaimsReleased,
			receipt.CompletedAt.UTC().Format(time.RFC3339),
		)
	}
}
