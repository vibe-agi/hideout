package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/manager"
)

type browserManagerEnvelope struct {
	Version      string                   `json:"version"`
	Resource     string                   `json:"resource"`
	Data         json.RawMessage          `json:"data"`
	Errors       []string                 `json:"errors"`
	ErrorDetails []manager.APIErrorDetail `json:"errorDetails"`
}

type browserManagerResponse struct {
	Status   int
	Body     []byte
	Envelope browserManagerEnvelope
}

func TestBrowserProfileTransactionDraftReviewConfirmApplyAndStalePlan(
	t *testing.T,
) {
	d := startTestDaemon(t)
	client := &http.Client{Timeout: 3 * time.Second}

	before := browserProfileProjection(t, client, d, "default")
	beforeProfile, err := d.store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	beforeOperations, err := (manager.OperationStore{
		Root: d.store.Root,
	}).List(32)
	if err != nil {
		t.Fatal(err)
	}

	const reviewedValue = "browser-local-draft-value"
	reviewedChange, err := manager.NewTypedChange(
		manager.ChangeProfileEnvironment,
		map[string]any{
			"set": map[string]string{
				"BROWSER_REVIEWED": reviewedValue,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	reviewedDraft := manager.ConfigurationDraft{
		Schema:       manager.ConfigurationDraftSchema,
		Profile:      "default",
		BaseRevision: before.Revision,
		ClientNonce:  "browser-review-confirm",
		Changes:      []manager.TypedChange{reviewedChange},
	}

	// Building and editing the draft is browser-local state. It must not reserve
	// an operation or mutate the authoritative profile before review.
	afterLocalDraft, err := d.store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	afterLocalOperations, err := (manager.OperationStore{
		Root: d.store.Root,
	}).List(32)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterLocalDraft, beforeProfile) ||
		len(afterLocalOperations) != len(beforeOperations) {
		t.Fatalf(
			"client-local draft changed authority: profileChanged=%t operations=%d->%d",
			!reflect.DeepEqual(afterLocalDraft, beforeProfile),
			len(beforeOperations),
			len(afterLocalOperations),
		)
	}

	planResponse := browserManagerJSON(
		t,
		client,
		d,
		http.MethodPost,
		"/api/v1/profile/transaction/plan",
		reviewedDraft,
	)
	if planResponse.Status != http.StatusOK {
		t.Fatalf(
			"review status=%d body=%s",
			planResponse.Status,
			planResponse.Body,
		)
	}
	if bytes.Contains(planResponse.Body, []byte(reviewedValue)) {
		t.Fatalf("canonical review exposed private draft value: %s", planResponse.Body)
	}
	var reviewedPlan manager.ConfigurationPlan
	browserDecodeData(t, planResponse.Envelope, &reviewedPlan)
	if err := reviewedPlan.VerifyDigest(); err != nil {
		t.Fatalf("canonical review digest: %v", err)
	}
	if reviewedPlan.OperationID == "" ||
		reviewedPlan.BaseRevision != before.Revision ||
		len(reviewedPlan.Diff) == 0 ||
		len(reviewedPlan.Effects) == 0 ||
		reviewedPlan.Rollback.Mode != "restore-previous" ||
		reviewedPlan.Rollback.Summary == "" ||
		len(reviewedPlan.Rollback.Effects) == 0 {
		t.Fatalf("canonical review is incomplete: %+v", reviewedPlan)
	}
	afterReview := browserProfileProjection(t, client, d, "default")
	if afterReview.Revision != before.Revision ||
		afterReview.ContentDigest != before.ContentDigest {
		t.Fatalf(
			"review mutated profile: before=%+v after=%+v",
			before,
			afterReview,
		)
	}

	unconfirmed := browserManagerJSON(
		t,
		client,
		d,
		http.MethodPost,
		"/api/v1/profile/transaction/apply",
		browserConfigurationApplyRequest(reviewedPlan, false),
	)
	browserRequireErrorCode(
		t,
		unconfirmed,
		http.StatusBadRequest,
		"invalid-plan",
	)
	afterUnconfirmed := browserProfileProjection(t, client, d, "default")
	if afterUnconfirmed.Revision != before.Revision ||
		afterUnconfirmed.ContentDigest != before.ContentDigest {
		t.Fatalf("unconfirmed review changed profile: %+v", afterUnconfirmed)
	}

	const staleValue = "must-never-overwrite-newer-state"
	staleChange, err := manager.NewTypedChange(
		manager.ChangeProfileEnvironment,
		map[string]any{
			"set": map[string]string{
				"BROWSER_STALE": staleValue,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	stalePlanResponse := browserManagerJSON(
		t,
		client,
		d,
		http.MethodPost,
		"/api/v1/profile/transaction/plan",
		manager.ConfigurationDraft{
			Schema:       manager.ConfigurationDraftSchema,
			Profile:      "default",
			BaseRevision: before.Revision,
			ClientNonce:  "browser-stale-review",
			Changes:      []manager.TypedChange{staleChange},
		},
	)
	if stalePlanResponse.Status != http.StatusOK {
		t.Fatalf(
			"second review status=%d body=%s",
			stalePlanResponse.Status,
			stalePlanResponse.Body,
		)
	}
	var stalePlan manager.ConfigurationPlan
	browserDecodeData(t, stalePlanResponse.Envelope, &stalePlan)

	appliedResponse := browserManagerJSON(
		t,
		client,
		d,
		http.MethodPost,
		"/api/v1/profile/transaction/apply",
		browserConfigurationApplyRequest(reviewedPlan, true),
	)
	if appliedResponse.Status != http.StatusOK {
		t.Fatalf(
			"confirmed apply status=%d body=%s",
			appliedResponse.Status,
			appliedResponse.Body,
		)
	}
	if bytes.Contains(appliedResponse.Body, []byte(reviewedValue)) {
		t.Fatalf("apply response exposed private draft value: %s", appliedResponse.Body)
	}
	var applied manager.ConfigurationApplyResult
	browserDecodeData(t, appliedResponse.Envelope, &applied)
	if applied.Operation.ID != reviewedPlan.OperationID ||
		applied.Operation.Phase != manager.OperationSucceeded ||
		applied.Projection.Revision != before.Revision+1 {
		t.Fatalf("confirmed apply result=%+v", applied)
	}

	staleResponse := browserManagerJSON(
		t,
		client,
		d,
		http.MethodPost,
		"/api/v1/profile/transaction/apply",
		browserConfigurationApplyRequest(stalePlan, true),
	)
	browserRequireErrorCode(
		t,
		staleResponse,
		http.StatusConflict,
		"stale-plan",
	)
	current, err := d.store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if current.Env.Public["BROWSER_REVIEWED"] != reviewedValue {
		t.Fatalf("confirmed value was not committed: %+v", current.Env.Public)
	}
	if _, exists := current.Env.Public["BROWSER_STALE"]; exists {
		t.Fatalf("stale plan overwrote newer state: %+v", current.Env.Public)
	}

	appliedOperation := browserOperation(
		t,
		client,
		d,
		reviewedPlan.OperationID,
	)
	if appliedOperation.Phase != manager.OperationSucceeded ||
		appliedOperation.ID != reviewedPlan.OperationID {
		t.Fatalf("operation lookup lost terminal success: %+v", appliedOperation)
	}
	staleOperation := browserOperation(t, client, d, stalePlan.OperationID)
	if staleOperation.Phase != manager.OperationCancelled ||
		staleOperation.Result == nil ||
		staleOperation.Result.Code != "stale-plan" {
		t.Fatalf("stale operation lacks durable zero-effect result: %+v", staleOperation)
	}
}

func TestBrowserProfileTransactionResponseLossRetryIsIdempotent(t *testing.T) {
	d := startTestDaemon(t)
	client := &http.Client{Timeout: 3 * time.Second}
	before := browserProfileProjection(t, client, d, "default")
	beforeOperations, err := (manager.OperationStore{
		Root: d.store.Root,
	}).List(32)
	if err != nil {
		t.Fatal(err)
	}

	const value = "response-loss-must-apply-once"
	change, err := manager.NewTypedChange(
		manager.ChangeProfileEnvironment,
		map[string]any{
			"set": map[string]string{"BROWSER_RESPONSE_LOSS": value},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	planResponse := browserManagerJSON(
		t,
		client,
		d,
		http.MethodPost,
		"/api/v1/profile/transaction/plan",
		manager.ConfigurationDraft{
			Schema:       manager.ConfigurationDraftSchema,
			Profile:      "default",
			BaseRevision: before.Revision,
			ClientNonce:  "browser-response-loss",
			Changes:      []manager.TypedChange{change},
		},
	)
	if planResponse.Status != http.StatusOK {
		t.Fatalf("review status=%d body=%s", planResponse.Status, planResponse.Body)
	}
	var plan manager.ConfigurationPlan
	browserDecodeData(t, planResponse.Envelope, &plan)
	request := browserConfigurationApplyRequest(plan, true)

	loss := &loseFirstCompletedResponseTransport{
		Base:       http.DefaultTransport,
		TargetPath: "/api/v1/profile/transaction/apply",
	}
	lossClient := &http.Client{Transport: loss, Timeout: 3 * time.Second}
	lostRequest := browserManagerRequest(
		t,
		d,
		http.MethodPost,
		"/api/v1/profile/transaction/apply",
		request,
	)
	lostResponse, lostErr := lossClient.Do(lostRequest)
	if lostResponse != nil {
		_ = lostResponse.Body.Close()
		t.Fatalf("response-loss transport returned a response: %+v", lostResponse)
	}
	if lostErr == nil || !strings.Contains(lostErr.Error(), "injected response loss") {
		t.Fatalf("first apply did not lose its completed response: %v", lostErr)
	}
	if !loss.Lost.Load() {
		t.Fatal("response-loss transport never observed a completed apply")
	}

	// Looking up the stable operation ID is safe immediately after a lost
	// response; retrying the exact apply request must then return that same
	// terminal result without another profile revision or effect.
	afterLossOperation := browserOperation(t, client, d, plan.OperationID)
	if afterLossOperation.Phase != manager.OperationSucceeded {
		t.Fatalf("lost response did not leave a terminal operation: %+v", afterLossOperation)
	}
	retryResponse := browserManagerJSON(
		t,
		client,
		d,
		http.MethodPost,
		"/api/v1/profile/transaction/apply",
		request,
	)
	if retryResponse.Status != http.StatusOK {
		t.Fatalf(
			"idempotent retry status=%d body=%s",
			retryResponse.Status,
			retryResponse.Body,
		)
	}
	if bytes.Contains(retryResponse.Body, []byte(value)) {
		t.Fatalf("idempotent response exposed private value: %s", retryResponse.Body)
	}
	var retried manager.ConfigurationApplyResult
	browserDecodeData(t, retryResponse.Envelope, &retried)
	if retried.Operation.ID != plan.OperationID ||
		retried.Operation.Phase != manager.OperationSucceeded ||
		retried.Projection.Revision != before.Revision+1 {
		t.Fatalf("idempotent retry returned a different outcome: %+v", retried)
	}
	finalProjection := browserProfileProjection(t, client, d, "default")
	if finalProjection.Revision != before.Revision+1 {
		t.Fatalf(
			"response loss duplicated profile commit: revision=%d want=%d",
			finalProjection.Revision,
			before.Revision+1,
		)
	}
	current, err := d.store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if current.Env.Public["BROWSER_RESPONSE_LOSS"] != value {
		t.Fatalf("response-loss final profile=%+v", current.Env.Public)
	}
	afterOperations, err := (manager.OperationStore{
		Root: d.store.Root,
	}).List(32)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterOperations) != len(beforeOperations)+1 {
		t.Fatalf(
			"retry created duplicate operations: before=%d after=%d",
			len(beforeOperations),
			len(afterOperations),
		)
	}
}

type loseFirstCompletedResponseTransport struct {
	Base       http.RoundTripper
	TargetPath string
	Lost       atomic.Bool
}

func (transport *loseFirstCompletedResponseTransport) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	base := transport.Base
	if base == nil {
		base = http.DefaultTransport
	}
	response, err := base.RoundTrip(request)
	if err != nil {
		return response, err
	}
	if request.URL.Path != transport.TargetPath ||
		request.Method != http.MethodPost ||
		!transport.Lost.CompareAndSwap(false, true) {
		return response, nil
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	return nil, errors.New("injected response loss after server completion")
}

func browserProfileProjection(
	t *testing.T,
	client *http.Client,
	d *Daemon,
	profileName string,
) manager.ProfileProjection {
	t.Helper()
	response := browserManagerJSON(
		t,
		client,
		d,
		http.MethodGet,
		"/api/v1/profiles/"+profileName+"/projection",
		nil,
	)
	if response.Status != http.StatusOK {
		t.Fatalf(
			"profile projection status=%d body=%s",
			response.Status,
			response.Body,
		)
	}
	var projection manager.ProfileProjection
	browserDecodeData(t, response.Envelope, &projection)
	return projection
}

func browserOperation(
	t *testing.T,
	client *http.Client,
	d *Daemon,
	operationID string,
) manager.Operation {
	t.Helper()
	response := browserManagerJSON(
		t,
		client,
		d,
		http.MethodGet,
		"/api/v1/operations/"+operationID,
		nil,
	)
	if response.Status != http.StatusOK {
		t.Fatalf(
			"operation lookup status=%d body=%s",
			response.Status,
			response.Body,
		)
	}
	var operation manager.Operation
	browserDecodeData(t, response.Envelope, &operation)
	return operation
}

func browserManagerJSON(
	t *testing.T,
	client *http.Client,
	d *Daemon,
	method string,
	path string,
	body any,
) browserManagerResponse {
	t.Helper()
	request := browserManagerRequest(t, d, method, path, body)
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s %s: %v", method, path, err)
	}
	var envelope browserManagerEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode %s %s response %q: %v", method, path, raw, err)
	}
	if envelope.Version != manager.APIVersion {
		t.Fatalf(
			"%s %s version=%q want=%q",
			method,
			path,
			envelope.Version,
			manager.APIVersion,
		)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("%s %s may be cached", method, path)
	}
	return browserManagerResponse{
		Status:   response.StatusCode,
		Body:     raw,
		Envelope: envelope,
	}
}

func browserManagerRequest(
	t *testing.T,
	d *Daemon,
	method string,
	path string,
	body any,
) *http.Request {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	base := strings.TrimSuffix(strings.Split(d.UIURL(), "#")[0], "/")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	request, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+d.Token())
	request.Header.Set("Origin", base)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}

func browserDecodeData(
	t *testing.T,
	envelope browserManagerEnvelope,
	target any,
) {
	t.Helper()
	if len(envelope.Errors) != 0 {
		t.Fatalf("unexpected Manager errors: %+v", envelope)
	}
	if err := json.Unmarshal(envelope.Data, target); err != nil {
		t.Fatalf("decode Manager data: %v; envelope=%+v", err, envelope)
	}
}

func browserRequireErrorCode(
	t *testing.T,
	response browserManagerResponse,
	status int,
	code string,
) {
	t.Helper()
	if response.Status != status ||
		len(response.Envelope.Errors) == 0 ||
		len(response.Envelope.ErrorDetails) != 1 ||
		response.Envelope.ErrorDetails[0].Code != code {
		t.Fatalf(
			"Manager error status=%d details=%+v body=%s; want %d %s",
			response.Status,
			response.Envelope.ErrorDetails,
			response.Body,
			status,
			code,
		)
	}
}

func browserConfigurationApplyRequest(
	plan manager.ConfigurationPlan,
	confirmed bool,
) manager.ConfigurationApplyRequest {
	return manager.ConfigurationApplyRequest{
		Schema:       manager.ConfigurationApplySchema,
		OperationID:  plan.OperationID,
		Profile:      plan.Profile,
		BaseRevision: plan.BaseRevision,
		PlanDigest:   plan.PlanDigest,
		Confirmed:    confirmed,
	}
}
