package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/secrets"
)

func TestSecretSetPlansConfirmsThenReadsStdinAndAppliesWithoutEcho(t *testing.T) {
	const canary = "socks5://canary-user:canary-password@127.0.0.1:7890"
	client := &secretCommandClientFixture{}
	var stdout, stderr bytes.Buffer
	a := app{
		stdout:       &stdout,
		stderr:       &stderr,
		stdin:        strings.NewReader(canary + "\n"),
		secretClient: client,
	}
	if err := a.run([]string{
		"secret", "set", "local-proxy", "--stdin", "--yes",
	}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(client.calls, ","); got != "plan,apply" {
		t.Fatalf("secret calls=%q", got)
	}
	if string(client.value) != canary {
		t.Fatalf("applied secret differs from input")
	}
	if client.request.Schema != manager.SecretApplySchema ||
		client.request.Ref != "local-proxy" ||
		client.request.Action != secrets.ActionSet ||
		!client.request.Confirmed {
		t.Fatalf("secret apply request=%+v", client.request)
	}
	combined := stdout.String() + stderr.String()
	if strings.Contains(combined, canary) ||
		strings.Contains(combined, "canary-password") {
		t.Fatalf("secret command echoed the value: %s", combined)
	}
	for _, want := range []string{
		"Secret change",
		"local-proxy",
		"missing",
		"available",
		"Operation",
		"succeeded",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("secret output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestSecretMutationRejectsArgvValuesWithoutEchoOrPlanning(t *testing.T) {
	const canary = "socks5://argv-user:argv-password@127.0.0.1:7890"
	for _, args := range [][]string{
		{"secret", "set", "local-proxy", canary, "--yes"},
		{"secret", "rotate", "local-proxy", "--value=" + canary, "--yes"},
	} {
		client := &secretCommandClientFixture{}
		var stdout, stderr bytes.Buffer
		a := app{
			stdout: &stdout, stderr: &stderr,
			stdin: strings.NewReader(""), secretClient: client,
		}
		err := a.run(args)
		if err == nil {
			t.Fatalf("argv value was accepted: %v", args[:3])
		}
		combined := err.Error() + stdout.String() + stderr.String()
		if strings.Contains(combined, canary) ||
			strings.Contains(combined, "argv-password") {
			t.Fatalf("argv rejection echoed the value: %s", combined)
		}
		if len(client.calls) != 0 {
			t.Fatalf("invalid argv reached secret authority: %v", client.calls)
		}
	}
}

func TestSecretStdinRequiresExplicitConfirmationBeforeReading(t *testing.T) {
	input := &countingSecretReader{
		reader: strings.NewReader("must-not-be-read\n"),
	}
	client := &secretCommandClientFixture{}
	var stdout, stderr bytes.Buffer
	a := app{
		stdout: &stdout, stderr: &stderr, stdin: input,
		secretClient: client,
		terminalInteractive: func() bool {
			return false
		},
	}
	err := a.run([]string{
		"secret", "set", "local-proxy", "--stdin",
	})
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("noninteractive secret confirmation error=%v", err)
	}
	if input.reads != 0 {
		t.Fatalf("secret input was read before confirmation: %d reads", input.reads)
	}
	if got := strings.Join(client.calls, ","); got != "plan" {
		t.Fatalf("secret calls=%q", got)
	}
}

func TestSecretTTYCancelDoesNotReadOrApply(t *testing.T) {
	client := &secretCommandClientFixture{}
	var stdout, stderr bytes.Buffer
	passwordReads := 0
	a := app{
		stdout:       &stdout,
		stderr:       &stderr,
		stdin:        strings.NewReader("n\n"),
		secretClient: client,
		terminalInteractive: func() bool {
			return true
		},
		secretReadPassword: func() ([]byte, error) {
			passwordReads++
			return []byte("must-not-be-read"), nil
		},
	}
	if err := a.run([]string{
		"secret", "set", "local-proxy",
	}); err != nil {
		t.Fatal(err)
	}
	if passwordReads != 0 {
		t.Fatalf("password reader called after cancellation %d times", passwordReads)
	}
	if got := strings.Join(client.calls, ","); got != "plan" {
		t.Fatalf("secret calls=%q", got)
	}
	if !strings.Contains(stdout.String(), "Cancelled") {
		t.Fatalf("cancellation not visible:\n%s", stdout.String())
	}
}

func TestSecretTTYInputIsHiddenAndSourceBytesAreCleared(t *testing.T) {
	const canary = "socks5://tty-user:tty-password@127.0.0.1:7890"
	raw := []byte(canary)
	client := &secretCommandClientFixture{}
	var stdout, stderr bytes.Buffer
	a := app{
		stdout:       &stdout,
		stderr:       &stderr,
		stdin:        strings.NewReader("yes\n"),
		secretClient: client,
		terminalInteractive: func() bool {
			return true
		},
		secretReadPassword: func() ([]byte, error) {
			return raw, nil
		},
	}
	if err := a.run([]string{
		"secret", "rotate", "local-proxy",
	}); err != nil {
		t.Fatal(err)
	}
	if string(client.value) != canary {
		t.Fatal("hidden value did not reach apply")
	}
	for index, value := range raw {
		if value != 0 {
			t.Fatalf("password source byte %d was not cleared", index)
		}
	}
	combined := stdout.String() + stderr.String()
	if strings.Contains(combined, canary) ||
		strings.Contains(combined, "tty-password") {
		t.Fatalf("TTY command echoed secret: %s", combined)
	}
}

func TestSecretConfirmationDoesNotReadAheadIntoHiddenValue(t *testing.T) {
	reader := strings.NewReader("yes\nnext-secret\n")
	confirmed, err := readSecretConfirmation(reader)
	if err != nil || !confirmed {
		t.Fatalf("confirmation=%t error=%v", confirmed, err)
	}
	remaining, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(remaining) != "next-secret\n" {
		t.Fatalf("confirmation consumed hidden input: %q", remaining)
	}
}

func TestSecretDeleteAppliesWithoutReadingAValue(t *testing.T) {
	client := &secretCommandClientFixture{
		currentAvailability: secrets.AvailabilityAvailable,
		currentGeneration:   3,
	}
	var stdout, stderr bytes.Buffer
	a := app{
		stdout: &stdout, stderr: &stderr,
		stdin: strings.NewReader(""), secretClient: client,
	}
	if err := a.run([]string{
		"secret", "delete", "local-proxy", "--yes",
	}); err != nil {
		t.Fatal(err)
	}
	if client.request.Value != nil || client.request.Action != secrets.ActionDelete {
		t.Fatalf("delete request=%+v", client.request)
	}
	if got := strings.Join(client.calls, ","); got != "plan,apply" {
		t.Fatalf("secret calls=%q", got)
	}
}

func TestSecretListAndStatusRenderMetadataOnly(t *testing.T) {
	const forbidden = "socks5://user:password@private.invalid"
	client := &secretCommandClientFixture{
		references: []secrets.Reference{
			{
				Schema: secrets.SecretReferenceSchema, Ref: "local-proxy",
				Provider: "macos-keychain", Availability: secrets.AvailabilityAvailable,
				Generation: 4, UpdatedAt: time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
			},
		},
	}
	for _, args := range [][]string{
		{"secret", "list"},
		{"secret", "status", "local-proxy"},
	} {
		var stdout, stderr bytes.Buffer
		a := app{
			stdout: &stdout, stderr: &stderr,
			stdin: strings.NewReader(forbidden), secretClient: client,
		}
		if err := a.run(args); err != nil {
			t.Fatal(err)
		}
		combined := stdout.String() + stderr.String()
		for _, want := range []string{
			"local-proxy",
			"available",
			"generation=4",
			"macos-keychain",
		} {
			if !strings.Contains(combined, want) {
				t.Fatalf("%v output missing %q:\n%s", args, want, combined)
			}
		}
		if strings.Contains(combined, forbidden) ||
			strings.Contains(combined, "password@private") {
			t.Fatalf("metadata command read or echoed stdin: %s", combined)
		}
	}
}

func TestDaemonSecretApplyRetriesExactPayloadAfterResponseLoss(t *testing.T) {
	const canary = "socks5://retry-user:retry-password@127.0.0.1:7890"
	plan := secretCommandPlanFixture(
		manager.SecretDraft{
			Schema: manager.SecretDraftSchema,
			Ref:    "local-proxy",
			Action: secrets.ActionSet,
		},
		secrets.AvailabilityMissing,
		0,
	)
	result := secretCommandResultFixture(plan)
	responseData, err := json.Marshal(manager.APIResponse{
		Version:  manager.APIVersion,
		Resource: "secret/apply",
		Data:     result,
		Errors:   []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var payloads [][]byte
	attempts := 0
	httpClient := &http.Client{
		Transport: secretRoundTripper(func(request *http.Request) (*http.Response, error) {
			attempts++
			body, readErr := io.ReadAll(request.Body)
			if readErr != nil {
				return nil, readErr
			}
			payloads = append(payloads, append([]byte(nil), body...))
			if request.Header.Get("Authorization") != "Bearer operator-token" ||
				request.Host != "localhost" {
				return nil, errors.New("missing authenticated daemon request identity")
			}
			if attempts == 1 {
				return nil, errors.New("simulated response loss")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewReader(responseData)),
			}, nil
		}),
	}
	client := &daemonSecretCommandClient{
		storeRoot: t.TempDir(),
		dial: func(string) (*http.Client, string, string, error) {
			return httpClient, "http://localhost", "operator-token", nil
		},
	}
	value := secretTestBuffer(t, canary)
	got, err := client.Apply(context.Background(), manager.SecretApplyRequest{
		Schema: manager.SecretApplySchema, OperationID: plan.OperationID,
		PlanDigest: plan.PlanDigest, Ref: plan.Ref, Action: plan.Action,
		Confirmed: true, Value: value,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Operation.ID != plan.OperationID || attempts != 2 {
		t.Fatalf("apply result=%+v attempts=%d", got, attempts)
	}
	if len(payloads) != 2 ||
		!bytes.Equal(payloads[0], payloads[1]) ||
		!bytes.Contains(payloads[0], []byte("retry-password")) {
		t.Fatalf("response-loss retry payloads differ or omit value")
	}
	assertSecretTestBufferCleared(t, value)
}

func TestSecretApplyFailureKeepsExactOperationRecoveryIdentity(t *testing.T) {
	const canary = "socks5://recovery-user:recovery-password@127.0.0.1:7890"
	client := &secretCommandClientFixture{
		applyErr: errors.New("transport lost after " + canary),
	}
	var stdout, stderr bytes.Buffer
	a := app{
		stdout:       &stdout,
		stderr:       &stderr,
		stdin:        strings.NewReader(canary + "\n"),
		secretClient: client,
	}
	err := a.run([]string{
		"secret", "set", "local-proxy", "--stdin", "--yes",
	})
	if err == nil {
		t.Fatal("uncertain secret apply was reported as successful")
	}
	combined := err.Error() + stdout.String() + stderr.String()
	for _, want := range []string{
		"op_secretfixture001",
		"hideout tui",
		"inspect this exact ID in Operations",
		"do not create a new plan or apply again",
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("recovery guidance missing %q:\n%s", want, combined)
		}
	}
	for _, forbidden := range []string{
		canary,
		"recovery-password",
		"retry the command",
	} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("unsafe recovery output contains %q:\n%s", forbidden, combined)
		}
	}
	if got := strings.Join(client.calls, ","); got != "plan,apply" {
		t.Fatalf("secret calls=%q", got)
	}
}

func TestSecretApplyEncoderRoundTripsEscapesAndClearsInput(t *testing.T) {
	const value = "socks5://user:p\"a\\ss\nword@例子.invalid:7890/\x00"
	buffer := secretTestBuffer(t, value)
	payload, err := encodeSecretApplyPayload(manager.SecretApplyRequest{
		Schema:      manager.SecretApplySchema,
		OperationID: "op_secretfixture001",
		PlanDigest:  "sha256:" + strings.Repeat("a", 64),
		Ref:         "local-proxy",
		Action:      secrets.ActionSet,
		Confirmed:   true,
		Value:       buffer,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer clear(payload)
	assertSecretTestBufferCleared(t, buffer)
	var decoded struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Value != value {
		t.Fatalf("escaped secret did not round trip")
	}
}

func TestDaemonSecretClientDoesNotTrustErrorText(t *testing.T) {
	const canary = "socks5://error-user:error-password@private.invalid"
	body := `{"version":"hideout.manager-api/v1","errors":["` +
		canary +
		`"],"errorDetails":[{"code":"secret-provider-unavailable","message":"` +
		canary +
		`","recovery":"` +
		canary +
		`"}]}`
	client := &daemonSecretCommandClient{
		storeRoot: t.TempDir(),
		dial: func(string) (*http.Client, string, string, error) {
			return &http.Client{
				Transport: secretRoundTripper(func(
					*http.Request,
				) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusServiceUnavailable,
						Status:     "503 Service Unavailable",
						Header:     make(http.Header),
						Body: io.NopCloser(
							strings.NewReader(body),
						),
					}, nil
				}),
			}, "http://localhost", "operator-token", nil
		},
	}
	_, err := client.List(context.Background(), "")
	if err == nil {
		t.Fatal("error response was accepted")
	}
	if strings.Contains(err.Error(), canary) ||
		strings.Contains(err.Error(), "error-password") {
		t.Fatalf("daemon error text escaped into CLI: %v", err)
	}
	if !strings.Contains(err.Error(), "provider is unavailable") {
		t.Fatalf("local stable error guidance missing: %v", err)
	}
}

type secretCommandClientFixture struct {
	calls               []string
	value               []byte
	request             manager.SecretApplyRequest
	references          []secrets.Reference
	currentAvailability string
	currentGeneration   uint64
	applyErr            error
}

func (client *secretCommandClientFixture) List(
	_ context.Context,
	ref string,
) ([]secrets.Reference, error) {
	client.calls = append(client.calls, "list")
	references := append([]secrets.Reference(nil), client.references...)
	if ref == "" {
		return references, nil
	}
	for _, reference := range references {
		if reference.Ref == ref {
			return []secrets.Reference{reference}, nil
		}
	}
	return []secrets.Reference{{
		Schema: secrets.SecretReferenceSchema, Ref: ref,
		Provider: "macos-keychain", Availability: secrets.AvailabilityMissing,
		Reason: "secret-missing",
	}}, nil
}

func (client *secretCommandClientFixture) Plan(
	_ context.Context,
	draft manager.SecretDraft,
) (manager.SecretPlan, error) {
	client.calls = append(client.calls, "plan")
	availability := client.currentAvailability
	if availability == "" {
		availability = secrets.AvailabilityMissing
	}
	return secretCommandPlanFixture(
		draft,
		availability,
		client.currentGeneration,
	), nil
}

func (client *secretCommandClientFixture) Apply(
	_ context.Context,
	request manager.SecretApplyRequest,
) (manager.SecretApplyResult, error) {
	client.calls = append(client.calls, "apply")
	client.request = request
	if request.Value != nil {
		if err := request.Value.Use(func(raw []byte) error {
			client.value = append([]byte(nil), raw...)
			return nil
		}); err != nil {
			return manager.SecretApplyResult{}, err
		}
	}
	if client.applyErr != nil {
		return manager.SecretApplyResult{}, client.applyErr
	}
	plan := secretCommandPlanFixture(
		manager.SecretDraft{
			Schema: manager.SecretDraftSchema,
			Ref:    request.Ref,
			Action: request.Action,
		},
		client.currentAvailability,
		client.currentGeneration,
	)
	plan.OperationID = request.OperationID
	plan.PlanDigest = request.PlanDigest
	return secretCommandResultFixture(plan), nil
}

func secretCommandPlanFixture(
	draft manager.SecretDraft,
	currentAvailability string,
	currentGeneration uint64,
) manager.SecretPlan {
	if currentAvailability == "" {
		currentAvailability = secrets.AvailabilityMissing
	}
	nextAvailability := secrets.AvailabilityAvailable
	if draft.Action == secrets.ActionDelete {
		nextAvailability = secrets.AvailabilityMissing
	}
	return manager.SecretPlan{
		Schema:         manager.SecretPlanSchema,
		OperationID:    "op_secretfixture001",
		PlanDigest:     "sha256:" + strings.Repeat("a", 64),
		Ref:            draft.Ref,
		Action:         draft.Action,
		BaseGeneration: currentGeneration,
		Current: secrets.Reference{
			Schema: secrets.SecretReferenceSchema, Ref: draft.Ref,
			Provider: "macos-keychain", Availability: currentAvailability,
			Generation: currentGeneration, Reason: secretFixtureReason(currentAvailability),
		},
		NextAvailability: nextAvailability,
		NextGeneration:   currentGeneration + 1,
		ExpiresAt: time.Date(
			2026, 7, 29, 13, 0, 0, 0, time.UTC,
		),
	}
}

func secretCommandResultFixture(
	plan manager.SecretPlan,
) manager.SecretApplyResult {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	reason := ""
	updatedAt := now
	if plan.NextAvailability != secrets.AvailabilityAvailable {
		reason = "secret-deleted"
		updatedAt = time.Time{}
	}
	return manager.SecretApplyResult{
		Operation: manager.Operation{
			Schema: manager.OperationSchema, ID: plan.OperationID,
			Kind: "secret." + plan.Action,
			Owner: manager.OperationOwner{
				Kind: "secret",
				ID:   plan.Ref,
			},
			PlanDigest: plan.PlanDigest,
			Phase:      manager.OperationSucceeded,
			Effects:    []manager.EffectResult{},
			Result: &manager.OperationResult{
				Status: manager.OperationSucceeded,
				Code:   "secret-generation-committed",
			},
			Recovery: manager.Recovery{
				Code:    "none",
				Summary: "no recovery required",
			},
			CreatedAt: now,
			UpdatedAt: now,
		},
		Reference: secrets.Reference{
			Schema: secrets.SecretReferenceSchema, Ref: plan.Ref,
			Provider: "macos-keychain", Availability: plan.NextAvailability,
			Generation: plan.NextGeneration, UpdatedAt: updatedAt, Reason: reason,
		},
	}
}

func secretFixtureReason(availability string) string {
	switch availability {
	case secrets.AvailabilityAvailable:
		return ""
	case secrets.AvailabilityLocked:
		return "keychain-locked"
	case secrets.AvailabilityUnavailable:
		return "provider-unavailable"
	default:
		return "secret-missing"
	}
}

func secretTestBuffer(t *testing.T, value string) *secrets.Buffer {
	t.Helper()
	buffer, err := secrets.NewBuffer([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	return buffer
}

func assertSecretTestBufferCleared(
	t *testing.T,
	buffer *secrets.Buffer,
) {
	t.Helper()
	if err := buffer.Use(func([]byte) error { return nil }); !errors.Is(
		err,
		secrets.ErrSecretBufferUsed,
	) {
		t.Fatalf("secret buffer remains usable: %v", err)
	}
}

type countingSecretReader struct {
	reader io.Reader
	reads  int
}

func (reader *countingSecretReader) Read(target []byte) (int, error) {
	reader.reads++
	return reader.reader.Read(target)
}

type secretRoundTripper func(*http.Request) (*http.Response, error)

func (roundTripper secretRoundTripper) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return roundTripper(request)
}

var (
	_ secretCommandClient = (*secretCommandClientFixture)(nil)
	_ http.RoundTripper   = secretRoundTripper(nil)
)
