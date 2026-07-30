package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/manager"
	workloadrisk "github.com/vibe-agi/hideout/internal/workloadobs/risk"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const activityClientResponseLimit = 32 << 20

// ActivityClient is an authenticated read-only client for the daemon-hosted
// Manager activity routes. It carries only the local store location; each
// request reads the current rotating operator credential through DialClient.
type ActivityClient struct {
	storeRoot string
}

var _ manager.ActivityProvider = (*ActivityClient)(nil)

func NewActivityClient(storeRoot string) *ActivityClient {
	return &ActivityClient{storeRoot: storeRoot}
}

func (client *ActivityClient) ResolveActivityOwner(
	ctx context.Context,
	selector manager.ActivityOwnerSelector,
) (workloadtypes.ActivityOwner, error) {
	if client == nil || selector.Validate() != nil {
		return workloadtypes.ActivityOwner{}, manager.ErrActivityQueryInvalid
	}
	values := activitySelectorValues(selector)
	result, err := fetchActivityResource[manager.ActivitySummaryResult](
		ctx,
		client.storeRoot,
		"activity/summary",
		values,
	)
	if err != nil {
		return workloadtypes.ActivityOwner{}, err
	}
	if result.Owner.Validate() != nil ||
		!activityOwnerMatchesSelector(result.Owner, selector) {
		return workloadtypes.ActivityOwner{}, errors.New(
			"activity summary returned a mismatched exact owner",
		)
	}
	return result.Owner, nil
}

func (client *ActivityClient) ActivitySummary(
	ctx context.Context,
	query manager.ActivitySummaryQuery,
) (manager.ActivitySummaryResult, error) {
	if client == nil || query.Owner.Validate() != nil ||
		!validActivityClientSessionScope(query.Owner, query.SessionID) ||
		(!query.From.IsZero() && !query.To.IsZero() && query.To.Before(query.From)) {
		return manager.ActivitySummaryResult{}, manager.ErrActivityQueryInvalid
	}
	values := activityOwnerValues(query.Owner)
	addActivityRun(values, query.SessionID)
	addActivityTimeRange(values, query.From, query.To)
	result, err := fetchActivityResource[manager.ActivitySummaryResult](
		ctx,
		client.storeRoot,
		"activity/summary",
		values,
	)
	if err != nil {
		return manager.ActivitySummaryResult{}, err
	}
	if err := validateActivityClientSummary(
		result, query.Owner, query.SessionID,
	); err != nil {
		return manager.ActivitySummaryResult{}, err
	}
	return result, nil
}

func (client *ActivityClient) ActivityEvents(
	ctx context.Context,
	query manager.ActivityEventsQuery,
) (manager.ActivityEventsPage, error) {
	if client == nil || query.Owner.Validate() != nil ||
		!validActivityClientSessionScope(query.Owner, query.SessionID) ||
		query.Limit < 0 || query.Limit > manager.MaxOperatorActivityLimit ||
		(!query.From.IsZero() && !query.To.IsZero() && query.To.Before(query.From)) {
		return manager.ActivityEventsPage{}, manager.ErrActivityQueryInvalid
	}
	values := activityOwnerValues(query.Owner)
	addActivityRun(values, query.SessionID)
	addActivityTimeRange(values, query.From, query.To)
	if query.Cursor != "" {
		values.Set("cursor", query.Cursor)
	}
	if query.Limit != 0 {
		values.Set("limit", strconv.Itoa(query.Limit))
	}
	addActivityValues(values, "kind", query.Kinds)
	addActivityValues(values, "operation", query.Operations)
	addActivityValues(values, "execution", query.Executions)
	addActivityValues(values, "risk", query.Risks)
	if query.Path != "" {
		values.Set("path", query.Path)
	}
	if query.Domain != "" {
		values.Set("domain", query.Domain)
	}
	if query.IP != "" {
		values.Set("ip", query.IP)
	}
	result, err := fetchActivityResource[manager.ActivityEventsPage](
		ctx,
		client.storeRoot,
		"activity/events",
		values,
	)
	if err != nil {
		return manager.ActivityEventsPage{}, err
	}
	limit := query.Limit
	if limit == 0 {
		limit = manager.DefaultOperatorActivityLimit
	}
	if err := validateActivityClientEvents(
		result, query.Owner, query.SessionID, limit,
	); err != nil {
		return manager.ActivityEventsPage{}, err
	}
	return result, nil
}

func (client *ActivityClient) ActivityExecutions(
	ctx context.Context,
	query manager.ActivityExecutionsQuery,
) (manager.ActivityExecutionsResult, error) {
	if client == nil || query.Owner.Validate() != nil ||
		!validActivityClientSessionScope(query.Owner, query.SessionID) ||
		(query.ID != "" && query.RootsOnly) {
		return manager.ActivityExecutionsResult{}, manager.ErrActivityQueryInvalid
	}
	values := activityOwnerValues(query.Owner)
	addActivityRun(values, query.SessionID)
	if query.ID != "" {
		values.Set("id", query.ID)
	}
	if query.RootsOnly {
		values.Set("root", "true")
	}
	result, err := fetchActivityResource[manager.ActivityExecutionsResult](
		ctx,
		client.storeRoot,
		"activity/executions",
		values,
	)
	if err != nil {
		return manager.ActivityExecutionsResult{}, err
	}
	if err := validateActivityClientExecutions(
		result, query.Owner, query.SessionID,
	); err != nil {
		return manager.ActivityExecutionsResult{}, err
	}
	return result, nil
}

func (client *ActivityClient) ActivityCoverage(
	ctx context.Context,
	query manager.ActivityCoverageQuery,
) (manager.ActivityCoverageResult, error) {
	if client == nil || query.Owner.Validate() != nil ||
		!validActivityClientSessionScope(query.Owner, query.SessionID) ||
		(!query.From.IsZero() && !query.To.IsZero() && query.To.Before(query.From)) {
		return manager.ActivityCoverageResult{}, manager.ErrActivityQueryInvalid
	}
	values := activityOwnerValues(query.Owner)
	addActivityRun(values, query.SessionID)
	addActivityTimeRange(values, query.From, query.To)
	addActivityValues(values, "subsystem", query.Subsystems)
	result, err := fetchActivityResource[manager.ActivityCoverageResult](
		ctx,
		client.storeRoot,
		"activity/coverage",
		values,
	)
	if err != nil {
		return manager.ActivityCoverageResult{}, err
	}
	if validateActivityClientCoverage(
		query.Owner, query.SessionID, result.Intervals,
	) != nil ||
		validateActivityClientCoverage(
			query.Owner, query.SessionID, result.Current,
		) != nil {
		return manager.ActivityCoverageResult{}, errors.New(
			"activity coverage response failed exact-owner validation",
		)
	}
	return result, nil
}

func (client *ActivityClient) ActivityRisks(
	ctx context.Context,
	query manager.ActivityRisksQuery,
) (manager.ActivityRisksResult, error) {
	if client == nil || query.Owner.Validate() != nil ||
		!validActivityClientSessionScope(query.Owner, query.SessionID) ||
		(!query.From.IsZero() && !query.To.IsZero() && query.To.Before(query.From)) {
		return manager.ActivityRisksResult{}, manager.ErrActivityQueryInvalid
	}
	values := activityOwnerValues(query.Owner)
	addActivityRun(values, query.SessionID)
	addActivityTimeRange(values, query.From, query.To)
	addActivityValues(values, "severity", query.Severities)
	addActivityValues(values, "rule", query.Rules)
	addActivityValues(values, "execution", query.Executions)
	result, err := fetchActivityResource[manager.ActivityRisksResult](
		ctx,
		client.storeRoot,
		"activity/risks",
		values,
	)
	if err != nil {
		return manager.ActivityRisksResult{}, err
	}
	if err := validateActivityClientRisks(
		query.Owner, query.SessionID, result.Findings,
	); err != nil {
		return manager.ActivityRisksResult{}, err
	}
	return result, nil
}

func fetchActivityResource[T any](
	ctx context.Context,
	storeRoot, resource string,
	values url.Values,
) (T, error) {
	var zero T
	if ctx == nil || strings.TrimSpace(storeRoot) == "" {
		return zero, manager.ErrActivityQueryInvalid
	}
	client, base, token, err := DialClient(storeRoot)
	if err != nil {
		return zero, err
	}
	target := base + "/api/v1/" + resource
	if len(values) > 0 {
		target += "?" + values.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return zero, err
	}
	request.Host = "localhost"
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return zero, err
	}
	defer response.Body.Close()
	var envelope struct {
		Version      string                   `json:"version"`
		Resource     string                   `json:"resource"`
		Data         json.RawMessage          `json:"data"`
		Errors       []string                 `json:"errors"`
		ErrorDetails []manager.APIErrorDetail `json:"errorDetails"`
	}
	decoder := json.NewDecoder(io.LimitReader(
		response.Body,
		activityClientResponseLimit,
	))
	if err := decoder.Decode(&envelope); err != nil {
		return zero, fmt.Errorf("decode %s response: %w", resource, err)
	}
	if response.StatusCode != http.StatusOK {
		return zero, activityClientAPIError(
			response.StatusCode,
			envelope.Errors,
			envelope.ErrorDetails,
		)
	}
	if envelope.Version != manager.APIVersion || envelope.Resource != resource ||
		len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return zero, errors.New("activity response contract mismatch")
	}
	var result T
	if err := json.Unmarshal(envelope.Data, &result); err != nil {
		return zero, fmt.Errorf("decode %s data: %w", resource, err)
	}
	return result, nil
}

func activityClientAPIError(
	status int,
	messages []string,
	details []manager.APIErrorDetail,
) error {
	code := ""
	message := ""
	if len(details) > 0 {
		code = details[0].Code
		message = details[0].Message
	}
	if message == "" {
		message = strings.Join(messages, "; ")
	}
	if message == "" {
		message = http.StatusText(status)
	}
	var sentinel error
	switch code {
	case "activity-cursor-invalid":
		sentinel = manager.ErrActivityCursorInvalid
	case "activity-cursor-owner-mismatch":
		sentinel = manager.ErrActivityCursorOwnerMismatch
	case "activity-cursor-filter-mismatch":
		sentinel = manager.ErrActivityCursorFilterMismatch
	case "activity-cursor-stale":
		sentinel = manager.ErrActivityCursorStale
	case "activity-owner-not-found":
		sentinel = manager.ErrActivityOwnerNotFound
	case "activity-execution-not-found":
		sentinel = manager.ErrActivityExecutionNotFound
	case "invalid-activity-query":
		sentinel = manager.ErrActivityQueryInvalid
	}
	responseErr := fmt.Errorf("activity Manager request failed (%d): %s", status, message)
	if sentinel == nil {
		return responseErr
	}
	return errors.Join(sentinel, responseErr)
}

func activitySelectorValues(selector manager.ActivityOwnerSelector) url.Values {
	values := url.Values{}
	if selector.SessionID != "" {
		values.Set("session", selector.SessionID)
		return values
	}
	values.Set("environment", selector.EnvironmentID)
	values.Set("incarnation", selector.BackendIncarnationID)
	return values
}

func activityOwnerValues(owner workloadtypes.ActivityOwner) url.Values {
	if owner.Kind == workloadtypes.OwnerDisposableSession {
		return activitySelectorValues(manager.ActivityOwnerSelector{
			SessionID: owner.SessionID,
		})
	}
	return activitySelectorValues(manager.ActivityOwnerSelector{
		EnvironmentID:        owner.EnvironmentID,
		BackendIncarnationID: owner.BackendIncarnationID,
	})
}

func activityOwnerMatchesSelector(
	owner workloadtypes.ActivityOwner,
	selector manager.ActivityOwnerSelector,
) bool {
	if selector.SessionID != "" {
		return owner.Kind == workloadtypes.OwnerDisposableSession &&
			owner.SessionID == selector.SessionID
	}
	return owner.Kind == workloadtypes.OwnerReusableEnvironment &&
		owner.EnvironmentID == selector.EnvironmentID &&
		owner.BackendIncarnationID == selector.BackendIncarnationID
}

func addActivityTimeRange(values url.Values, from, to time.Time) {
	if !from.IsZero() {
		values.Set("from", from.Round(0).UTC().Format(time.RFC3339Nano))
	}
	if !to.IsZero() {
		values.Set("to", to.Round(0).UTC().Format(time.RFC3339Nano))
	}
}

func addActivityRun(values url.Values, sessionID string) {
	if sessionID != "" {
		values.Set("run", sessionID)
	}
}

func addActivityValues(values url.Values, key string, entries []string) {
	for _, entry := range entries {
		values.Add(key, entry)
	}
}

func validateActivityClientSummary(
	result manager.ActivitySummaryResult,
	owner workloadtypes.ActivityOwner,
	sessionID string,
) error {
	if !result.Owner.Equal(owner) ||
		len(result.HighestRisks) > manager.DefaultActivityHighestRiskLimit ||
		validateActivityClientCoverage(
			owner, sessionID, result.CurrentCoverage,
		) != nil ||
		validateActivityClientRisks(
			owner, sessionID, result.HighestRisks,
		) != nil {
		return errors.New("activity summary response failed exact-owner validation")
	}
	return nil
}

func validateActivityClientEvents(
	result manager.ActivityEventsPage,
	owner workloadtypes.ActivityOwner,
	sessionID string,
	limit int,
) error {
	if len(result.Records) > limit ||
		(result.NextCursor != "" && !result.QueryTruncated) ||
		validateActivityClientCoverage(
			owner, sessionID, result.Coverage,
		) != nil {
		return errors.New("activity events response failed exact-owner validation")
	}
	for _, record := range result.Records {
		if record.ValidatePersistable() != nil || !record.Owner.Equal(owner) ||
			!activityClientSessionMatches(
				owner, sessionID, record.SessionID,
			) {
			return errors.New("activity event response failed exact-owner validation")
		}
	}
	return nil
}

func validateActivityClientExecutions(
	result manager.ActivityExecutionsResult,
	owner workloadtypes.ActivityOwner,
	sessionID string,
) error {
	if err := validateActivityClientCoverage(
		owner, sessionID, result.Coverage,
	); err != nil {
		return err
	}
	seen := make(map[string]struct{})
	var validate func(manager.ActivityExecutionNode, string, int) error
	validate = func(node manager.ActivityExecutionNode, parentID string, depth int) error {
		if depth > 1024 ||
			node.Execution.Validate() != nil ||
			!node.Execution.Owner.Equal(owner) ||
			!activityClientSessionMatches(
				owner, sessionID, node.Execution.SessionID,
			) ||
			(parentID != "" && node.Execution.ParentExecutionID != parentID) {
			return errors.New("activity execution response failed exact-owner validation")
		}
		if _, exists := seen[node.Execution.ID]; exists {
			return errors.New("activity execution response contains a duplicate")
		}
		seen[node.Execution.ID] = struct{}{}
		for _, child := range node.Children {
			if err := validate(child, node.Execution.ID, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	for _, root := range result.Roots {
		if err := validate(root, "", 1); err != nil {
			return err
		}
	}
	return nil
}

func validateActivityClientCoverage(
	owner workloadtypes.ActivityOwner,
	sessionID string,
	values []workloadtypes.CoverageInterval,
) error {
	for _, interval := range values {
		if interval.Validate() != nil || !interval.Owner.Equal(owner) ||
			!activityClientSessionMatches(
				owner, sessionID, interval.SessionID,
			) {
			return errors.New("activity coverage response failed exact-owner validation")
		}
	}
	return nil
}

func validateActivityClientRisks(
	owner workloadtypes.ActivityOwner,
	sessionID string,
	values []workloadrisk.Finding,
) error {
	for _, finding := range values {
		if finding.Validate() != nil || !finding.Owner.Equal(owner) ||
			!activityClientSessionMatches(
				owner, sessionID, finding.SessionID,
			) {
			return errors.New("activity risk response failed exact-owner validation")
		}
	}
	return nil
}

func activityClientSessionMatches(
	owner workloadtypes.ActivityOwner,
	requestedSessionID string,
	actualSessionID string,
) bool {
	return (requestedSessionID == "" ||
		requestedSessionID == actualSessionID) &&
		(owner.Kind != workloadtypes.OwnerDisposableSession ||
			owner.SessionID == actualSessionID)
}

func validActivityClientSessionScope(
	owner workloadtypes.ActivityOwner,
	sessionID string,
) bool {
	if sessionID == "" {
		return true
	}
	return (manager.ActivityOwnerSelector{
		SessionID: sessionID,
	}).Validate() == nil &&
		(owner.Kind != workloadtypes.OwnerDisposableSession ||
			owner.SessionID == sessionID)
}
