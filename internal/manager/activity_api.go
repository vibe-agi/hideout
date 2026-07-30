package manager

import (
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	workloadrisk "github.com/vibe-agi/hideout/internal/workloadobs/risk"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

var (
	activityOperationPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)
	activityExecutionPattern = regexp.MustCompile(`^exec_[A-Za-z0-9_-]{8,124}$`)
	activityRiskPattern      = regexp.MustCompile(`^(risk_[A-Za-z0-9_-]{8,124}|[a-z][a-z0-9.-]{2,127})$`)
)

func (api API) serveActivityResource(
	w http.ResponseWriter,
	r *http.Request,
	resource string,
) {
	if api.ActivityProvider == nil {
		writeAPIDetailedError(w, http.StatusServiceUnavailable, APIErrorDetail{
			Code:     "activity-unavailable",
			Message:  "the authoritative activity query provider is unavailable",
			Recovery: "retry through the running Hideout daemon",
		})
		return
	}
	switch resource {
	case "activity/summary":
		api.serveActivitySummary(w, r)
	case "activity/events":
		api.serveActivityEvents(w, r)
	case "activity/executions":
		api.serveActivityExecutions(w, r)
	case "activity/coverage":
		api.serveActivityCoverage(w, r)
	case "activity/risks":
		api.serveActivityRisks(w, r)
	default:
		writeAPIError(w, http.StatusNotFound, "unknown manager API resource")
	}
}

func (api API) serveActivitySummary(w http.ResponseWriter, r *http.Request) {
	values, err := parseActivityValues(r, []string{"from", "to"}, nil)
	if err != nil {
		writeInvalidActivityQuery(w, err)
		return
	}
	selector, from, to, err := activityScopeAndTime(values)
	if err != nil {
		writeInvalidActivityQuery(w, err)
		return
	}
	sessionID, err := activityRunSession(values)
	if err != nil {
		writeInvalidActivityQuery(w, err)
		return
	}
	owner, ok := api.resolveActivityOwner(w, r, selector)
	if !ok {
		return
	}
	if !activityRunMatchesOwner(owner, sessionID) {
		writeInvalidActivityQuery(w, errors.New(
			"run does not belong to the selected disposable owner",
		))
		return
	}
	result, err := api.ActivityProvider.ActivitySummary(
		r.Context(),
		ActivitySummaryQuery{
			Owner: owner, SessionID: sessionID, From: from, To: to,
		},
	)
	if err != nil {
		writeActivityQueryError(w, err)
		return
	}
	if !result.Owner.Equal(owner) ||
		!validActivitySummaryResult(result, owner, sessionID) {
		writeInvalidActivityResponse(w)
		return
	}
	writeActivityResponse(w, "activity/summary", result)
}

func (api API) serveActivityEvents(w http.ResponseWriter, r *http.Request) {
	values, err := parseActivityValues(
		r,
		[]string{"from", "to", "cursor", "limit", "path", "domain", "ip"},
		[]string{"kind", "operation", "execution", "risk"},
	)
	if err != nil {
		writeInvalidActivityQuery(w, err)
		return
	}
	selector, from, to, err := activityScopeAndTime(values)
	if err != nil {
		writeInvalidActivityQuery(w, err)
		return
	}
	query := ActivityEventsQuery{
		From: from, To: to, Limit: DefaultOperatorActivityLimit,
	}
	if query.SessionID, err = activityRunSession(values); err != nil {
		writeInvalidActivityQuery(w, err)
		return
	}
	if raw, exists := activitySingle(values, "cursor"); exists {
		if !validActivitySearch(raw, 4096) || raw == "" {
			writeInvalidActivityQuery(w, errors.New("cursor is invalid"))
			return
		}
		query.Cursor = raw
	}
	if raw, exists := activitySingle(values, "limit"); exists {
		limit, parseErr := strconv.Atoi(raw)
		if parseErr != nil || limit < 1 || limit > MaxOperatorActivityLimit {
			writeInvalidActivityQuery(w, errors.New("limit must be between 1 and 500"))
			return
		}
		query.Limit = limit
	}
	query.Kinds, err = normalizedActivityValues(values, "kind", validActivityAPIKind)
	if err == nil {
		query.Operations, err = normalizedActivityValues(
			values, "operation", activityOperationPattern.MatchString,
		)
	}
	if err == nil {
		query.Executions, err = normalizedActivityValues(
			values, "execution", activityExecutionPattern.MatchString,
		)
	}
	if err == nil {
		query.Risks, err = normalizedActivityValues(
			values, "risk", activityRiskPattern.MatchString,
		)
	}
	if err != nil {
		writeInvalidActivityQuery(w, err)
		return
	}
	if query.Path, err = activitySearchValue(values, "path", 4096, false); err != nil {
		writeInvalidActivityQuery(w, err)
		return
	}
	if query.Domain, err = activitySearchValue(values, "domain", 253, true); err != nil {
		writeInvalidActivityQuery(w, err)
		return
	}
	if raw, exists := activitySingle(values, "ip"); exists {
		address, parseErr := netip.ParseAddr(raw)
		if parseErr != nil || address.Zone() != "" {
			writeInvalidActivityQuery(w, errors.New("ip must be an IP literal"))
			return
		}
		query.IP = address.Unmap().String()
	}
	owner, ok := api.resolveActivityOwner(w, r, selector)
	if !ok {
		return
	}
	query.Owner = owner
	if !activityRunMatchesOwner(owner, query.SessionID) {
		writeInvalidActivityQuery(w, errors.New(
			"run does not belong to the selected disposable owner",
		))
		return
	}
	result, err := api.ActivityProvider.ActivityEvents(r.Context(), query)
	if err != nil {
		writeActivityQueryError(w, err)
		return
	}
	if !validActivityEventsPage(
		result, owner, query.SessionID, query.Limit,
	) {
		writeInvalidActivityResponse(w)
		return
	}
	writeActivityResponse(w, "activity/events", result)
}

func (api API) serveActivityExecutions(w http.ResponseWriter, r *http.Request) {
	values, err := parseActivityValues(r, []string{"id", "root"}, nil)
	if err != nil {
		writeInvalidActivityQuery(w, err)
		return
	}
	selector, err := activityOwnerSelector(values)
	if err != nil {
		writeInvalidActivityQuery(w, err)
		return
	}
	query := ActivityExecutionsQuery{}
	if query.SessionID, err = activityRunSession(values); err != nil {
		writeInvalidActivityQuery(w, err)
		return
	}
	if query.ID, _ = activitySingle(values, "id"); query.ID != "" &&
		!activityExecutionPattern.MatchString(query.ID) {
		writeInvalidActivityQuery(w, errors.New("id must be an execution ID"))
		return
	}
	if raw, exists := activitySingle(values, "root"); exists {
		if raw != "true" && raw != "false" {
			writeInvalidActivityQuery(w, errors.New("root must be true or false"))
			return
		}
		query.RootsOnly = raw == "true"
	}
	if query.ID != "" && query.RootsOnly {
		writeInvalidActivityQuery(w, errors.New("id and root=true are mutually exclusive"))
		return
	}
	owner, ok := api.resolveActivityOwner(w, r, selector)
	if !ok {
		return
	}
	query.Owner = owner
	if !activityRunMatchesOwner(owner, query.SessionID) {
		writeInvalidActivityQuery(w, errors.New(
			"run does not belong to the selected disposable owner",
		))
		return
	}
	result, err := api.ActivityProvider.ActivityExecutions(r.Context(), query)
	if err != nil {
		writeActivityQueryError(w, err)
		return
	}
	if !validActivityExecutionsResult(result, owner, query.SessionID) {
		writeInvalidActivityResponse(w)
		return
	}
	writeActivityResponse(w, "activity/executions", result)
}

func (api API) serveActivityCoverage(w http.ResponseWriter, r *http.Request) {
	values, err := parseActivityValues(
		r, []string{"from", "to"}, []string{"subsystem"},
	)
	if err != nil {
		writeInvalidActivityQuery(w, err)
		return
	}
	selector, from, to, err := activityScopeAndTime(values)
	if err != nil {
		writeInvalidActivityQuery(w, err)
		return
	}
	subsystems, err := normalizedActivityValues(
		values, "subsystem", validActivityAPISubsystem,
	)
	if err != nil {
		writeInvalidActivityQuery(w, err)
		return
	}
	sessionID, err := activityRunSession(values)
	if err != nil {
		writeInvalidActivityQuery(w, err)
		return
	}
	owner, ok := api.resolveActivityOwner(w, r, selector)
	if !ok {
		return
	}
	if !activityRunMatchesOwner(owner, sessionID) {
		writeInvalidActivityQuery(w, errors.New(
			"run does not belong to the selected disposable owner",
		))
		return
	}
	result, err := api.ActivityProvider.ActivityCoverage(
		r.Context(),
		ActivityCoverageQuery{
			Owner: owner, SessionID: sessionID,
			From: from, To: to, Subsystems: subsystems,
		},
	)
	if err != nil {
		writeActivityQueryError(w, err)
		return
	}
	if !validActivityCoverageResult(result, owner, sessionID) {
		writeInvalidActivityResponse(w)
		return
	}
	writeActivityResponse(w, "activity/coverage", result)
}

func (api API) serveActivityRisks(w http.ResponseWriter, r *http.Request) {
	values, err := parseActivityValues(
		r, []string{"from", "to"}, []string{"severity", "rule", "execution"},
	)
	if err != nil {
		writeInvalidActivityQuery(w, err)
		return
	}
	selector, from, to, err := activityScopeAndTime(values)
	if err != nil {
		writeInvalidActivityQuery(w, err)
		return
	}
	sessionID, err := activityRunSession(values)
	if err != nil {
		writeInvalidActivityQuery(w, err)
		return
	}
	severities, err := normalizedActivityValues(
		values, "severity", validActivityAPISeverity,
	)
	var rules, executions []string
	if err == nil {
		rules, err = normalizedActivityValues(values, "rule", func(value string) bool {
			return activityRiskPattern.MatchString(value) &&
				!strings.HasPrefix(value, "risk_")
		})
	}
	if err == nil {
		executions, err = normalizedActivityValues(
			values, "execution", activityExecutionPattern.MatchString,
		)
	}
	if err != nil {
		writeInvalidActivityQuery(w, err)
		return
	}
	owner, ok := api.resolveActivityOwner(w, r, selector)
	if !ok {
		return
	}
	if !activityRunMatchesOwner(owner, sessionID) {
		writeInvalidActivityQuery(w, errors.New(
			"run does not belong to the selected disposable owner",
		))
		return
	}
	result, err := api.ActivityProvider.ActivityRisks(
		r.Context(),
		ActivityRisksQuery{
			Owner: owner, SessionID: sessionID, From: from, To: to,
			Severities: severities, Rules: rules, Executions: executions,
		},
	)
	if err != nil {
		writeActivityQueryError(w, err)
		return
	}
	if !validActivityRisksResult(result, owner, sessionID) {
		writeInvalidActivityResponse(w)
		return
	}
	writeActivityResponse(w, "activity/risks", result)
}

func (api API) resolveActivityOwner(
	w http.ResponseWriter,
	r *http.Request,
	selector ActivityOwnerSelector,
) (workloadtypes.ActivityOwner, bool) {
	owner, err := api.ActivityProvider.ResolveActivityOwner(r.Context(), selector)
	if err != nil {
		writeActivityQueryError(w, err)
		return workloadtypes.ActivityOwner{}, false
	}
	if err := owner.Validate(); err != nil || !ownerMatchesSelector(owner, selector) {
		writeInvalidActivityResponse(w)
		return workloadtypes.ActivityOwner{}, false
	}
	return owner, true
}

func parseActivityValues(
	r *http.Request,
	singleKeys, repeatedKeys []string,
) (url.Values, error) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, errors.New("query encoding is invalid")
	}
	singles := map[string]struct{}{
		"environment": {}, "incarnation": {}, "session": {}, "run": {},
	}
	for _, key := range singleKeys {
		singles[key] = struct{}{}
	}
	repeated := make(map[string]struct{}, len(repeatedKeys))
	for _, key := range repeatedKeys {
		repeated[key] = struct{}{}
	}
	for key, entries := range values {
		if _, exists := singles[key]; exists {
			if len(entries) != 1 {
				return nil, fmt.Errorf("query parameter %q must appear once", key)
			}
			continue
		}
		if _, exists := repeated[key]; exists {
			if len(entries) == 0 || len(entries) > 128 {
				return nil, fmt.Errorf("query parameter %q has too many values", key)
			}
			continue
		}
		return nil, fmt.Errorf("unknown query parameter %q", key)
	}
	return values, nil
}

func activityScopeAndTime(
	values url.Values,
) (ActivityOwnerSelector, time.Time, time.Time, error) {
	selector, err := activityOwnerSelector(values)
	if err != nil {
		return ActivityOwnerSelector{}, time.Time{}, time.Time{}, err
	}
	from, err := activityTimeValue(values, "from")
	if err != nil {
		return ActivityOwnerSelector{}, time.Time{}, time.Time{}, err
	}
	to, err := activityTimeValue(values, "to")
	if err != nil {
		return ActivityOwnerSelector{}, time.Time{}, time.Time{}, err
	}
	if !from.IsZero() && !to.IsZero() && to.Before(from) {
		return ActivityOwnerSelector{}, time.Time{}, time.Time{},
			errors.New("to must not precede from")
	}
	return selector, from, to, nil
}

func activityOwnerSelector(values url.Values) (ActivityOwnerSelector, error) {
	selector := ActivityOwnerSelector{
		EnvironmentID:        values.Get("environment"),
		BackendIncarnationID: values.Get("incarnation"),
		SessionID:            values.Get("session"),
	}
	if err := selector.Validate(); err != nil {
		return ActivityOwnerSelector{}, errors.New(
			"provide either session, or both environment and incarnation",
		)
	}
	return selector, nil
}

func activityRunSession(values url.Values) (string, error) {
	sessionID, exists := activitySingle(values, "run")
	if !exists {
		return "", nil
	}
	if !activitySessionIDPattern.MatchString(sessionID) {
		return "", errors.New("run must be a session ID")
	}
	return sessionID, nil
}

func activityRunMatchesOwner(
	owner workloadtypes.ActivityOwner,
	sessionID string,
) bool {
	return sessionID == "" ||
		owner.Kind != workloadtypes.OwnerDisposableSession ||
		owner.SessionID == sessionID
}

func activityTimeValue(values url.Values, key string) (time.Time, error) {
	raw, exists := activitySingle(values, key)
	if !exists {
		return time.Time{}, nil
	}
	value, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be RFC3339", key)
	}
	return value.Round(0).UTC(), nil
}

func activitySingle(values url.Values, key string) (string, bool) {
	entries, exists := values[key]
	if !exists || len(entries) != 1 {
		return "", false
	}
	return entries[0], true
}

func normalizedActivityValues(
	values url.Values,
	key string,
	valid func(string) bool,
) ([]string, error) {
	entries := append([]string(nil), values[key]...)
	sort.Strings(entries)
	previous := ""
	for _, entry := range entries {
		if !valid(entry) || entry == previous {
			return nil, fmt.Errorf("query parameter %q contains an invalid or duplicate value", key)
		}
		previous = entry
	}
	return entries, nil
}

func activitySearchValue(
	values url.Values,
	key string,
	maximum int,
	foldDomain bool,
) (string, error) {
	raw, exists := activitySingle(values, key)
	if !exists {
		return "", nil
	}
	if !validActivitySearch(raw, maximum) || raw == "" {
		return "", fmt.Errorf("%s search is invalid", key)
	}
	if foldDomain {
		raw = strings.ToLower(strings.TrimSuffix(raw, "."))
		if raw == "" {
			return "", fmt.Errorf("%s search is invalid", key)
		}
	}
	return raw, nil
}

func validActivitySearch(value string, maximum int) bool {
	return len(value) <= maximum && utf8.ValidString(value) &&
		!strings.ContainsFunc(value, unicode.IsControl)
}

func validActivityAPIKind(value string) bool {
	switch value {
	case workloadtypes.ActivityProcess, workloadtypes.ActivityFile,
		workloadtypes.ActivityConnection, workloadtypes.ActivityDNS,
		workloadtypes.ActivityRisk:
		return true
	default:
		return false
	}
}

func validActivityAPISubsystem(value string) bool {
	switch value {
	case workloadtypes.SubsystemProcess, workloadtypes.SubsystemFile,
		workloadtypes.SubsystemNetwork, workloadtypes.SubsystemDNS:
		return true
	default:
		return false
	}
}

func validActivityAPISeverity(value string) bool {
	switch value {
	case workloadrisk.SeverityInfo, workloadrisk.SeverityLow,
		workloadrisk.SeverityMedium, workloadrisk.SeverityHigh,
		workloadrisk.SeverityCritical:
		return true
	default:
		return false
	}
}

func writeActivityResponse(w http.ResponseWriter, resource string, data any) {
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version: APIVersion, Resource: resource, Data: data, Errors: []string{},
	})
}

func writeInvalidActivityQuery(w http.ResponseWriter, err error) {
	writeAPIDetailedError(w, http.StatusBadRequest, APIErrorDetail{
		Code: "invalid-activity-query", Field: "query",
		Message:  err.Error(),
		Recovery: "use one exact owner and only the documented bounded filters",
	})
}

func writeInvalidActivityResponse(w http.ResponseWriter) {
	writeAPIDetailedError(w, http.StatusServiceUnavailable, APIErrorDetail{
		Code:     "activity-response-invalid",
		Message:  "the authoritative activity provider returned an invalid exact-owner response",
		Recovery: "update Hideout and retry after checking hideout daemon status",
	})
}

func writeActivityQueryError(w http.ResponseWriter, err error) {
	status := http.StatusServiceUnavailable
	detail := APIErrorDetail{
		Code:     "activity-query-unavailable",
		Message:  "the authoritative activity query is unavailable",
		Recovery: "retry after checking hideout daemon status",
	}
	switch {
	case errors.Is(err, ErrActivityCursorOwnerMismatch):
		status = http.StatusBadRequest
		detail = APIErrorDetail{
			Code:     "activity-cursor-owner-mismatch",
			Message:  "the activity cursor belongs to another exact owner",
			Recovery: "remove the cursor and restart the query for this owner",
		}
	case errors.Is(err, ErrActivityCursorFilterMismatch):
		status = http.StatusBadRequest
		detail = APIErrorDetail{
			Code:     "activity-cursor-filter-mismatch",
			Message:  "the activity cursor belongs to different filters",
			Recovery: "repeat the original filters or remove the cursor",
		}
	case errors.Is(err, ErrActivityCursorStale):
		status = http.StatusConflict
		detail = APIErrorDetail{
			Code:     "activity-cursor-stale",
			Message:  "the activity cursor refers to an older retained snapshot",
			Recovery: "remove the cursor and restart the query",
		}
	case errors.Is(err, ErrActivityCursorInvalid):
		status = http.StatusBadRequest
		detail = APIErrorDetail{
			Code:     "activity-cursor-invalid",
			Message:  "the activity cursor is malformed or unauthenticated",
			Recovery: "remove the cursor and restart the query",
		}
	case errors.Is(err, ErrActivityQueryInvalid):
		status = http.StatusBadRequest
		detail = APIErrorDetail{
			Code:     "invalid-activity-query",
			Message:  "the activity query is invalid",
			Recovery: "review the owner and bounded filters, then retry",
		}
	case errors.Is(err, ErrActivityOwnerNotFound):
		status = http.StatusNotFound
		detail = APIErrorDetail{
			Code:     "activity-owner-not-found",
			Message:  "the exact activity owner was not found",
			Recovery: "refresh environments or sessions and select an exact retained owner",
		}
	case errors.Is(err, ErrActivityExecutionNotFound):
		status = http.StatusNotFound
		detail = APIErrorDetail{
			Code:     "activity-execution-not-found",
			Message:  "the selected execution was not found for this exact owner",
			Recovery: "refresh the execution tree and retry",
		}
	}
	writeAPIDetailedError(w, status, detail)
}

func validActivitySummaryResult(
	result ActivitySummaryResult,
	owner workloadtypes.ActivityOwner,
	sessionID string,
) bool {
	if !result.Owner.Equal(owner) ||
		len(result.HighestRisks) > DefaultActivityHighestRiskLimit {
		return false
	}
	for kind := range result.Counts {
		if !validActivityAPIKind(kind) {
			return false
		}
	}
	return validActivityCoverage(owner, sessionID, result.CurrentCoverage) &&
		validActivityRisks(owner, sessionID, result.HighestRisks)
}

func validActivityEventsPage(
	result ActivityEventsPage,
	owner workloadtypes.ActivityOwner,
	sessionID string,
	limit int,
) bool {
	if len(result.Records) > limit ||
		(result.NextCursor != "" && !result.QueryTruncated) ||
		(result.NextCursor != "" &&
			(!strings.HasPrefix(result.NextCursor, "cur_") ||
				!validActivitySearch(result.NextCursor, 4096))) {
		return false
	}
	for _, record := range result.Records {
		if record.ValidatePersistable() != nil || !record.Owner.Equal(owner) ||
			!activitySessionMatchesScope(owner, sessionID, record.SessionID) {
			return false
		}
	}
	return validActivityCoverage(owner, sessionID, result.Coverage)
}

func validActivityExecutionsResult(
	result ActivityExecutionsResult,
	owner workloadtypes.ActivityOwner,
	sessionID string,
) bool {
	seen := make(map[string]struct{})
	nodes := 0
	var validate func(ActivityExecutionNode, string, int) bool
	validate = func(node ActivityExecutionNode, parent string, depth int) bool {
		nodes++
		if depth > 1024 || nodes > 65536 ||
			node.Execution.Validate() != nil ||
			!node.Execution.Owner.Equal(owner) ||
			!activitySessionMatchesScope(
				owner, sessionID, node.Execution.SessionID,
			) ||
			(parent != "" && node.Execution.ParentExecutionID != parent) {
			return false
		}
		if _, exists := seen[node.Execution.ID]; exists {
			return false
		}
		seen[node.Execution.ID] = struct{}{}
		for kind := range node.ActivityCounts {
			if !validActivityAPIKind(kind) {
				return false
			}
		}
		for _, child := range node.Children {
			if !validate(child, node.Execution.ID, depth+1) {
				return false
			}
		}
		return true
	}
	for _, root := range result.Roots {
		if !validate(root, "", 1) {
			return false
		}
	}
	return validActivityCoverage(owner, sessionID, result.Coverage)
}

func validActivityCoverageResult(
	result ActivityCoverageResult,
	owner workloadtypes.ActivityOwner,
	sessionID string,
) bool {
	return validActivityCoverage(owner, sessionID, result.Intervals) &&
		validActivityCoverage(owner, sessionID, result.Current)
}

func validActivityRisksResult(
	result ActivityRisksResult,
	owner workloadtypes.ActivityOwner,
	sessionID string,
) bool {
	return validActivityRisks(owner, sessionID, result.Findings)
}

func validActivityCoverage(
	owner workloadtypes.ActivityOwner,
	sessionID string,
	intervals []workloadtypes.CoverageInterval,
) bool {
	if len(intervals) > 1<<20 {
		return false
	}
	for _, interval := range intervals {
		if interval.Validate() != nil || !interval.Owner.Equal(owner) ||
			!activitySessionMatchesScope(owner, sessionID, interval.SessionID) {
			return false
		}
	}
	return true
}

func validActivityRisks(
	owner workloadtypes.ActivityOwner,
	sessionID string,
	findings []workloadrisk.Finding,
) bool {
	if len(findings) > 1<<20 {
		return false
	}
	for _, finding := range findings {
		if finding.Validate() != nil || !finding.Owner.Equal(owner) ||
			!activitySessionMatchesScope(owner, sessionID, finding.SessionID) {
			return false
		}
	}
	return true
}

func activitySessionMatchesScope(
	owner workloadtypes.ActivityOwner,
	requestedSessionID string,
	actualSessionID string,
) bool {
	return (requestedSessionID == "" ||
		requestedSessionID == actualSessionID) &&
		(owner.Kind != workloadtypes.OwnerDisposableSession ||
			owner.SessionID == actualSessionID)
}
