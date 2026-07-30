package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vibe-agi/hideout/internal/daemon"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/secrets"
	"golang.org/x/term"
)

const (
	secretCommandTimeout   = 60 * time.Second
	secretResponseMaxBytes = 1 << 20
)

type secretCommandClient interface {
	List(context.Context, string) ([]secrets.Reference, error)
	Plan(context.Context, manager.SecretDraft) (manager.SecretPlan, error)
	Apply(
		context.Context,
		manager.SecretApplyRequest,
	) (manager.SecretApplyResult, error)
}

type secretMutationOptions struct {
	action   string
	ref      string
	useStdin bool
	yes      bool
}

type daemonSecretCommandClient struct {
	storeRoot string
	dial      func(string) (*http.Client, string, string, error)
}

type secretAPIRequestError struct {
	status   int
	code     string
	message  string
	recovery string
}

type secretTransportError struct {
	cause error
}

type secretApplyOutcomeError struct {
	operationID string
	detail      string
	cause       error
}

type secretAPIErrorWire struct {
	Code     string          `json:"code"`
	Field    json.RawMessage `json:"field,omitempty"`
	Message  json.RawMessage `json:"message"`
	Recovery json.RawMessage `json:"recovery,omitempty"`
}

func (a app) secretCommand(args []string) error {
	if len(args) == 0 || containsHelpToken(args) {
		a.secretUsage()
		return nil
	}
	switch args[0] {
	case secrets.ActionSet, secrets.ActionRotate, secrets.ActionDelete:
		options, err := parseSecretMutationOptions(args[0], args[1:])
		if err != nil {
			return err
		}
		return a.mutateSecret(options)
	case "list":
		if len(args) != 1 {
			return errors.New("usage: hideout secret list")
		}
		return a.listSecrets("")
	case "status":
		if len(args) != 2 || secrets.ValidateRef(args[1]) != nil {
			return errors.New("usage: hideout secret status <ref>")
		}
		return a.listSecrets(args[1])
	default:
		return errors.New(
			"unknown secret command; use: hideout secret set|rotate|delete|status|list",
		)
	}
}

func (a app) secretUsage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  hideout secret set <ref> [--stdin] [--yes]")
	fmt.Fprintln(a.stdout, "  hideout secret rotate <ref> [--stdin] [--yes]")
	fmt.Fprintln(a.stdout, "  hideout secret delete <ref> [--yes]")
	fmt.Fprintln(a.stdout, "  hideout secret status <ref>")
	fmt.Fprintln(a.stdout, "  hideout secret list")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Secret values never belong in command arguments, plans, output, or history.")
	fmt.Fprintln(a.stdout, "Without --stdin, set/rotate prompt invisibly in a terminal after plan review.")
	fmt.Fprintln(a.stdout, "For a pipe without exporting the value:")
	fmt.Fprintln(a.stdout, "  read -r -s PROXY_URL")
	fmt.Fprintf(a.stdout, "  printf '%%s' \"$PROXY_URL\" | hideout secret set local-proxy --stdin --yes\n")
	fmt.Fprintln(a.stdout, "  unset PROXY_URL")
	fmt.Fprintln(a.stdout, "Healthy updates apply through the running daemon; stopping or recreating the VM is not required.")
	fmt.Fprintln(a.stdout, "On macOS, the daemon-managed secure store is your Keychain; Hideout never copies the value into its profile or local data store.")
	fmt.Fprintln(a.stdout, "One-release compatibility: HIDEOUT_SECRET_<REF> is read only from the daemon startup environment.")
	fmt.Fprintln(a.stdout, "Legacy exports are not imported automatically. Re-enter once with `hideout secret set <ref>`, then remove the export from your shell setup.")
	fmt.Fprintln(a.stdout, "Exports made after daemon start cannot apply.")
}

func parseSecretMutationOptions(
	action string,
	args []string,
) (secretMutationOptions, error) {
	options := secretMutationOptions{action: action}
	for _, argument := range args {
		switch argument {
		case "--stdin":
			if options.useStdin {
				return options, secretMutationUsageError(action)
			}
			options.useStdin = true
		case "--yes":
			if options.yes {
				return options, secretMutationUsageError(action)
			}
			options.yes = true
		default:
			if strings.HasPrefix(argument, "-") ||
				options.ref != "" ||
				secrets.ValidateRef(argument) != nil {
				return options, secretMutationUsageError(action)
			}
			options.ref = argument
		}
	}
	if options.ref == "" ||
		action == secrets.ActionDelete && options.useStdin {
		return options, secretMutationUsageError(action)
	}
	return options, nil
}

func secretMutationUsageError(action string) error {
	if action == secrets.ActionDelete {
		return errors.New(
			"usage: hideout secret delete <ref> [--yes]",
		)
	}
	return fmt.Errorf(
		"usage: hideout secret %s <ref> [--stdin] [--yes]; values are never accepted in argv",
		action,
	)
}

func (a app) mutateSecret(options secretMutationOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), secretCommandTimeout)
	defer cancel()
	client, err := a.secretAuthority(ctx)
	if err != nil {
		return err
	}
	plan, err := client.Plan(ctx, manager.SecretDraft{
		Schema: manager.SecretDraftSchema,
		Ref:    options.ref,
		Action: options.action,
	})
	if err != nil {
		return secretCommandError(err)
	}
	if plan.Ref != options.ref || plan.Action != options.action {
		return errors.New("secret service returned a mismatched plan")
	}
	writeSecretPlanReview(a.stdout, plan)
	if len(plan.Blockers) != 0 {
		return errors.New(
			"secret plan is blocked; resolve the listed blockers and review a new plan",
		)
	}
	if options.useStdin && !options.yes {
		return errors.New(
			"stdin secret input requires --yes after reviewing the plan",
		)
	}
	if !options.yes {
		if !a.isInteractiveTerminal() {
			return errors.New(
				"secret apply requires confirmation; review the plan and rerun with --yes",
			)
		}
		fmt.Fprint(a.stdout, "Apply this exact secret plan? [y/N]: ")
		confirmed, confirmErr := readSecretConfirmation(a.stdin)
		if confirmErr != nil {
			return confirmErr
		}
		if !confirmed {
			fmt.Fprintln(a.stdout, "Cancelled; no changes applied.")
			return nil
		}
	}

	var value *secrets.Buffer
	if options.action != secrets.ActionDelete {
		value, err = a.readSecretValue(options.useStdin)
		if err != nil {
			return err
		}
		defer value.Clear()
	}
	result, err := client.Apply(ctx, manager.SecretApplyRequest{
		Schema:      manager.SecretApplySchema,
		OperationID: plan.OperationID,
		PlanDigest:  plan.PlanDigest,
		Ref:         plan.Ref,
		Action:      plan.Action,
		Confirmed:   true,
		Value:       value,
	})
	if err != nil {
		return newSecretApplyOutcomeError(plan.OperationID, err)
	}
	fmt.Fprintf(
		a.stdout,
		"Secret %s %s generation=%d\n",
		result.Reference.Ref,
		result.Reference.Availability,
		result.Reference.Generation,
	)
	fmt.Fprintf(
		a.stdout,
		"Operation %s %s\n",
		result.Operation.ID,
		result.Operation.Phase,
	)
	return nil
}

func (a app) listSecrets(ref string) error {
	ctx, cancel := context.WithTimeout(context.Background(), secretCommandTimeout)
	defer cancel()
	client, err := a.secretAuthority(ctx)
	if err != nil {
		return err
	}
	references, err := client.List(ctx, ref)
	if err != nil {
		return secretCommandError(err)
	}
	if len(references) == 0 {
		fmt.Fprintln(a.stdout, "No managed secrets.")
		return nil
	}
	for _, reference := range references {
		fmt.Fprintf(
			a.stdout,
			"%s  %s  generation=%d  provider=%s",
			reference.Ref,
			reference.Availability,
			reference.Generation,
			reference.Provider,
		)
		if !reference.UpdatedAt.IsZero() {
			fmt.Fprintf(
				a.stdout,
				"  updated=%s",
				reference.UpdatedAt.UTC().Format(time.RFC3339),
			)
		}
		if reference.Reason != "" {
			fmt.Fprintf(a.stdout, "  reason=%s", reference.Reason)
		}
		fmt.Fprintln(a.stdout)
	}
	return nil
}

func (a app) secretAuthority(
	ctx context.Context,
) (secretCommandClient, error) {
	store, err := profile.DefaultStore()
	if err != nil {
		return nil, err
	}
	if err := a.ensureSecretDaemon(ctx, store); err != nil {
		return nil, err
	}
	if a.secretClient != nil {
		return a.secretClient, nil
	}
	return &daemonSecretCommandClient{
		storeRoot: store.Root,
		dial:      daemon.DialClient,
	}, nil
}

func (a app) ensureSecretDaemon(
	ctx context.Context,
	store profile.Store,
) error {
	if a.secretClient != nil && a.ensureDaemon == nil {
		return nil
	}
	executableFn := a.daemonExecutable
	if executableFn == nil {
		executableFn = runExecutable
	}
	executable, err := executableFn()
	if err != nil {
		return fmt.Errorf("resolve hideout executable: %w", err)
	}
	ensure := a.ensureDaemon
	if ensure == nil {
		ensure = ensureRunDaemon
	}
	if _, err := ensure(ctx, daemon.EnsureStartedOptions{
		Store:       store,
		Executable:  executable,
		BuildID:     daemonBuildID(),
		Diagnostics: a.stderr,
	}); err != nil {
		return fmt.Errorf(
			"secret service requires the running Hideout daemon: %w",
			err,
		)
	}
	return nil
}

func readSecretConfirmation(reader io.Reader) (bool, error) {
	if reader == nil {
		return false, errors.New("secret confirmation input is unavailable")
	}
	answer := make([]byte, 16)
	defer clear(answer)
	length := 0
	for length < len(answer) {
		count, err := reader.Read(answer[length : length+1])
		if count > 0 {
			length++
			if answer[length-1] == '\n' {
				break
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return false, err
		}
		if count == 0 {
			return false, io.ErrNoProgress
		}
	}
	if length == len(answer) && answer[length-1] != '\n' {
		return false, errors.New("secret confirmation response is too long")
	}
	trimmed := trimASCIIWhitespace(answer[:length])
	switch len(trimmed) {
	case 1:
		return trimmed[0] == 'y' || trimmed[0] == 'Y', nil
	case 3:
		return (trimmed[0] == 'y' || trimmed[0] == 'Y') &&
			(trimmed[1] == 'e' || trimmed[1] == 'E') &&
			(trimmed[2] == 's' || trimmed[2] == 'S'), nil
	default:
		return false, nil
	}
}

func (a app) readSecretValue(useStdin bool) (*secrets.Buffer, error) {
	if useStdin {
		raw, err := io.ReadAll(io.LimitReader(
			a.stdin,
			manager.SecretRequestBodyLimit+1,
		))
		if err != nil {
			clear(raw)
			return nil, errors.New("read secret value from stdin")
		}
		owned := raw
		defer clear(owned)
		if int64(len(raw)) > manager.SecretRequestBodyLimit {
			return nil, errors.New("secret value from stdin exceeds the safe input bound")
		}
		raw = trimOneSecretLineEnding(raw)
		return newSecretInputBuffer(raw)
	}
	if !a.isInteractiveTerminal() {
		return nil, errors.New(
			"secret value requires a terminal; pipe it to hideout secret with --stdin --yes",
		)
	}
	fmt.Fprint(a.stderr, "Secret value (input hidden): ")
	var raw []byte
	var err error
	if a.secretReadPassword != nil {
		raw, err = a.secretReadPassword()
	} else {
		input, ok := a.stdin.(*os.File)
		if !ok || !term.IsTerminal(int(input.Fd())) {
			fmt.Fprintln(a.stderr)
			return nil, errors.New("secret terminal input is unavailable")
		}
		raw, err = term.ReadPassword(int(input.Fd()))
	}
	fmt.Fprintln(a.stderr)
	if err != nil {
		clear(raw)
		return nil, errors.New("read hidden secret value")
	}
	defer clear(raw)
	return newSecretInputBuffer(raw)
}

func trimOneSecretLineEnding(raw []byte) []byte {
	if len(raw) != 0 && raw[len(raw)-1] == '\n' {
		raw = raw[:len(raw)-1]
		if len(raw) != 0 && raw[len(raw)-1] == '\r' {
			raw = raw[:len(raw)-1]
		}
	}
	return raw
}

func trimASCIIWhitespace(value []byte) []byte {
	start := 0
	for start < len(value) {
		switch value[start] {
		case ' ', '\t', '\n', '\r':
			start++
		default:
			goto trimEnd
		}
	}
trimEnd:
	end := len(value)
	for end > start {
		switch value[end-1] {
		case ' ', '\t', '\n', '\r':
			end--
		default:
			return value[start:end]
		}
	}
	return value[start:end]
}

func newSecretInputBuffer(raw []byte) (*secrets.Buffer, error) {
	if !utf8.Valid(raw) {
		return nil, errors.New("secret value must be valid UTF-8 text")
	}
	buffer, err := secrets.NewBuffer(raw)
	if err != nil {
		return nil, errors.New("secret value must be non-empty and bounded")
	}
	return buffer, nil
}

func writeSecretPlanReview(w io.Writer, plan manager.SecretPlan) {
	fmt.Fprintln(w, "Secret change")
	fmt.Fprintf(w, "  Reference   %s\n", plan.Ref)
	fmt.Fprintf(w, "  Action      %s\n", plan.Action)
	fmt.Fprintf(
		w,
		"  Current     %s generation=%d\n",
		plan.Current.Availability,
		plan.BaseGeneration,
	)
	fmt.Fprintf(
		w,
		"  After       %s generation=%d\n",
		plan.NextAvailability,
		plan.NextGeneration,
	)
	fmt.Fprintf(w, "  Operation   %s\n", plan.OperationID)
	if len(plan.AffectedProfiles) != 0 {
		fmt.Fprintf(
			w,
			"  Profiles    %s\n",
			strings.Join(plan.AffectedProfiles, ", "),
		)
	}
	if len(plan.AffectedEnvironments) != 0 {
		fmt.Fprintf(
			w,
			"  Environments %s\n",
			strings.Join(plan.AffectedEnvironments, ", "),
		)
	}
	for _, blocker := range plan.Blockers {
		fmt.Fprintf(w, "  BLOCKED     %s: %s\n", blocker.Code, blocker.Summary)
	}
	for _, warning := range plan.Warnings {
		fmt.Fprintf(w, "  Warning     %s: %s\n", warning.Code, warning.Summary)
	}
	fmt.Fprintln(w, "  Value       hidden; never stored in the plan or output")
}

func (client *daemonSecretCommandClient) List(
	ctx context.Context,
	ref string,
) ([]secrets.Reference, error) {
	values := url.Values{}
	if ref != "" {
		values.Set("ref", ref)
	}
	path := "/api/v1/secrets"
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var references []secrets.Reference
	if err := client.request(
		ctx,
		http.MethodGet,
		path,
		nil,
		"secrets",
		&references,
	); err != nil {
		return nil, err
	}
	if len(references) > 4096 {
		return nil, errors.New("secret service returned too many references")
	}
	for index, reference := range references {
		if err := reference.Validate(); err != nil ||
			ref != "" && reference.Ref != ref ||
			index > 0 && references[index-1].Ref >= reference.Ref {
			return nil, errors.New("secret service returned invalid metadata")
		}
	}
	if ref != "" && len(references) != 1 {
		return nil, errors.New("secret service omitted requested metadata")
	}
	return references, nil
}

func (client *daemonSecretCommandClient) Plan(
	ctx context.Context,
	draft manager.SecretDraft,
) (manager.SecretPlan, error) {
	if err := draft.Validate(); err != nil {
		return manager.SecretPlan{}, errors.New("secret draft is invalid")
	}
	payload, err := json.Marshal(draft)
	if err != nil {
		return manager.SecretPlan{}, errors.New("encode secret draft")
	}
	defer clear(payload)
	var plan manager.SecretPlan
	if err := client.request(
		ctx,
		http.MethodPost,
		"/api/v1/secret/plan",
		payload,
		"secret/plan",
		&plan,
	); err != nil {
		return manager.SecretPlan{}, err
	}
	if err := plan.VerifyDigest(); err != nil ||
		plan.Ref != draft.Ref ||
		plan.Action != draft.Action {
		return manager.SecretPlan{}, errors.New("secret service returned an invalid plan")
	}
	return plan, nil
}

func (client *daemonSecretCommandClient) Apply(
	ctx context.Context,
	request manager.SecretApplyRequest,
) (manager.SecretApplyResult, error) {
	if request.Value != nil {
		defer request.Value.Clear()
	}
	if err := request.Validate(); err != nil {
		return manager.SecretApplyResult{}, errors.New("secret apply request is invalid")
	}
	payload, err := encodeSecretApplyPayload(request)
	if err != nil {
		return manager.SecretApplyResult{}, err
	}
	defer clear(payload)
	var result manager.SecretApplyResult
	for attempt := 0; attempt < 2; attempt++ {
		result = manager.SecretApplyResult{}
		err = client.request(
			ctx,
			http.MethodPost,
			"/api/v1/secret/apply",
			payload,
			"secret/apply",
			&result,
		)
		if err == nil {
			if result.Operation.Validate() != nil ||
				result.Reference.Validate() != nil ||
				result.Operation.ID != request.OperationID ||
				result.Operation.PlanDigest != request.PlanDigest ||
				result.Operation.Owner.Kind != "secret" ||
				result.Operation.Owner.ID != request.Ref ||
				result.Reference.Ref != request.Ref {
				return manager.SecretApplyResult{}, errors.New(
					"secret service returned an invalid apply result",
				)
			}
			return result, nil
		}
		if attempt != 0 || !retryableSecretApplyError(err) {
			return manager.SecretApplyResult{}, err
		}
	}
	return manager.SecretApplyResult{}, err
}

func (client *daemonSecretCommandClient) request(
	ctx context.Context,
	method string,
	path string,
	payload []byte,
	resource string,
	target any,
) error {
	dial := client.dial
	if dial == nil {
		dial = daemon.DialClient
	}
	httpClient, base, token, err := dial(client.storeRoot)
	if err != nil {
		return &secretTransportError{cause: err}
	}
	if httpClient == nil {
		return &secretTransportError{
			cause: errors.New("daemon HTTP client is unavailable"),
		}
	}
	defer httpClient.CloseIdleConnections()
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	httpRequest, err := http.NewRequestWithContext(
		ctx,
		method,
		base+path,
		body,
	)
	if err != nil {
		return &secretTransportError{cause: err}
	}
	httpRequest.Host = "localhost"
	httpRequest.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	response, err := httpClient.Do(httpRequest)
	if err != nil {
		return &secretTransportError{cause: err}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(
		response.Body,
		secretResponseMaxBytes+1,
	))
	if err != nil {
		clear(data)
		return &secretTransportError{cause: err}
	}
	defer clear(data)
	if len(data) > secretResponseMaxBytes {
		return errors.New("secret service response exceeds the safe bound")
	}
	var envelope struct {
		Version      string               `json:"version"`
		Resource     string               `json:"resource"`
		Data         json.RawMessage      `json:"data"`
		Errors       []json.RawMessage    `json:"errors"`
		ErrorDetails []secretAPIErrorWire `json:"errorDetails"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return errors.New("secret service response is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("secret service response contains trailing data")
	}
	defer clear(envelope.Data)
	defer func() {
		for index := range envelope.Errors {
			clear(envelope.Errors[index])
		}
		for index := range envelope.ErrorDetails {
			clear(envelope.ErrorDetails[index].Field)
			clear(envelope.ErrorDetails[index].Message)
			clear(envelope.ErrorDetails[index].Recovery)
		}
	}()
	if response.StatusCode != http.StatusOK {
		return secretRequestError(response.StatusCode, envelope.ErrorDetails)
	}
	if envelope.Version != manager.APIVersion ||
		envelope.Resource != resource ||
		len(envelope.Errors) != 0 ||
		len(envelope.Data) == 0 ||
		bytes.Equal(envelope.Data, []byte("null")) {
		return errors.New("secret service response contract mismatch")
	}
	dataDecoder := json.NewDecoder(bytes.NewReader(envelope.Data))
	dataDecoder.DisallowUnknownFields()
	if err := dataDecoder.Decode(target); err != nil {
		return errors.New("secret service response data is invalid")
	}
	if err := dataDecoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("secret service response data contains trailing content")
	}
	return nil
}

func secretRequestError(
	status int,
	details []secretAPIErrorWire,
) error {
	if len(details) == 1 {
		message, recovery, ok := localSecretErrorText(details[0].Code)
		if !ok {
			return &secretAPIRequestError{
				status: status, code: "secret-request-failed",
				message:  fmt.Sprintf("secret request failed with HTTP %d", status),
				recovery: "check hideout daemon status and retry",
			}
		}
		return &secretAPIRequestError{
			status: status, code: details[0].Code,
			message: message, recovery: recovery,
		}
	}
	return &secretAPIRequestError{
		status:   status,
		code:     "secret-request-failed",
		message:  fmt.Sprintf("secret request failed with HTTP %d", status),
		recovery: "check hideout daemon status and retry",
	}
}

func localSecretErrorText(code string) (string, string, bool) {
	switch code {
	case "invalid-secret-query":
		return "secret metadata query is invalid",
			"use one valid secret reference", true
	case "invalid-secret-request":
		return "secret request is invalid",
			"create and review a fresh secret plan", true
	case "secret-operation-not-found":
		return "secret operation was not found",
			"create and review a new secret plan", true
	case "stale-secret-plan":
		return "secret metadata changed after review",
			"refresh secret status and review a new plan", true
	case "secret-operation-conflict":
		return "secret operation no longer matches the reviewed plan",
			"refresh secret status and create a new operation", true
	case "secret-plan-expired":
		return "secret plan expired before confirmation",
			"create and review a fresh secret plan", true
	case "secret-confirmation-required":
		return "secret apply requires explicit confirmation",
			"review the plan and confirm the exact operation", true
	case "secret-value-required":
		return "this secret operation requires a value",
			"retry set or rotate through a protected prompt or standard input", true
	case "secret-plan-blocked":
		return "secret plan has active blockers",
			"resolve the blockers shown by a fresh plan", true
	case "secret-recovery-required":
		return "secret operation could not be proved complete",
			"inspect the exact operation before attempting another mutation", true
	case "secret-provider-locked":
		return "secret provider is locked",
			"unlock the login keychain and retry", true
	case "secret-provider-unavailable":
		return "secret provider is unavailable",
			"check daemon and login keychain availability, then retry", true
	case "secret-not-found":
		return "secret reference is missing",
			"create a set plan for this secret reference", true
	case "secret-provider-response-invalid":
		return "secret provider returned invalid metadata",
			"update Hideout and retry through the running daemon", true
	case "secret-service-failed":
		return "secret service request failed",
			"retry through the running Hideout daemon", true
	case "request-too-large":
		return "secret request exceeds the route limit",
			"use a shorter secret value", true
	default:
		return "", "", false
	}
}

func (err *secretAPIRequestError) Error() string {
	if err == nil {
		return ""
	}
	if err.recovery == "" {
		return err.message
	}
	return err.message + "; " + err.recovery
}

func (err *secretTransportError) Error() string {
	return "secret daemon request was interrupted"
}

func (err *secretTransportError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

func newSecretApplyOutcomeError(
	operationID string,
	cause error,
) error {
	detail := ""
	var requestError *secretAPIRequestError
	if errors.As(cause, &requestError) {
		detail = requestError.Error()
	}
	return &secretApplyOutcomeError{
		operationID: operationID,
		detail:      detail,
		cause:       cause,
	}
}

func (err *secretApplyOutcomeError) Error() string {
	if err == nil {
		return ""
	}
	message := "secret apply was not confirmed successful for operation " +
		err.operationID
	if err.detail != "" {
		message += "; " + err.detail
	}
	return message +
		"; open hideout tui and inspect this exact ID in Operations; " +
		"do not create a new plan or apply again until authoritative " +
		"terminal evidence is shown"
}

func (err *secretApplyOutcomeError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

func retryableSecretApplyError(err error) bool {
	var transport *secretTransportError
	if errors.As(err, &transport) {
		return true
	}
	var requestError *secretAPIRequestError
	if !errors.As(err, &requestError) {
		return false
	}
	switch requestError.code {
	case "secret-service-failed", "secret-recovery-required":
		return true
	default:
		return false
	}
}

func secretCommandError(err error) error {
	if err == nil {
		return nil
	}
	var transport *secretTransportError
	if errors.As(err, &transport) {
		return errors.New(
			"secret daemon request was interrupted; inspect hideout daemon status and retry this read or planning request",
		)
	}
	return err
}

func encodeSecretApplyPayload(
	request manager.SecretApplyRequest,
) ([]byte, error) {
	payload := make([]byte, 0, 512)
	payload = append(payload, `{"schema":`...)
	payload = strconv.AppendQuote(payload, request.Schema)
	payload = append(payload, `,"operationId":`...)
	payload = strconv.AppendQuote(payload, request.OperationID)
	payload = append(payload, `,"planDigest":`...)
	payload = strconv.AppendQuote(payload, request.PlanDigest)
	payload = append(payload, `,"ref":`...)
	payload = strconv.AppendQuote(payload, request.Ref)
	payload = append(payload, `,"action":`...)
	payload = strconv.AppendQuote(payload, request.Action)
	payload = append(payload, `,"confirmed":`...)
	payload = strconv.AppendBool(payload, request.Confirmed)
	if request.Value != nil {
		payload = append(payload, `,"value":`...)
		if err := request.Value.Use(func(raw []byte) error {
			var appendErr error
			payload, appendErr = appendSecretJSONString(payload, raw)
			return appendErr
		}); err != nil {
			clear(payload)
			return nil, errors.New("encode secret value")
		}
	}
	payload = append(payload, '}')
	if len(payload) > int(manager.SecretRequestBodyLimit) {
		clear(payload)
		return nil, errors.New(
			"secret value is too large for the protected Manager request",
		)
	}
	return payload, nil
}

func appendSecretJSONString(
	target []byte,
	value []byte,
) ([]byte, error) {
	if !utf8.Valid(value) {
		return target, errors.New("secret value must be valid UTF-8 text")
	}
	target = append(target, '"')
	const hex = "0123456789abcdef"
	for _, character := range value {
		switch character {
		case '"', '\\':
			target = append(target, '\\', character)
		case '\b':
			target = append(target, '\\', 'b')
		case '\f':
			target = append(target, '\\', 'f')
		case '\n':
			target = append(target, '\\', 'n')
		case '\r':
			target = append(target, '\\', 'r')
		case '\t':
			target = append(target, '\\', 't')
		default:
			if character < 0x20 {
				target = append(
					target,
					'\\',
					'u',
					'0',
					'0',
					hex[character>>4],
					hex[character&0x0f],
				)
				continue
			}
			target = append(target, character)
		}
	}
	target = append(target, '"')
	return target, nil
}

var _ secretCommandClient = (*daemonSecretCommandClient)(nil)
