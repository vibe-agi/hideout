package manager

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"unicode"

	workloadquery "github.com/vibe-agi/hideout/internal/workloadobs/query"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const DefaultActivityHighestRiskLimit = workloadquery.DefaultHighestRiskLimit

var (
	ErrActivityCursorInvalid        = workloadquery.ErrCursorInvalid
	ErrActivityCursorOwnerMismatch  = workloadquery.ErrCursorOwnerMismatch
	ErrActivityCursorFilterMismatch = workloadquery.ErrCursorFilterMismatch
	ErrActivityCursorStale          = workloadquery.ErrCursorStale
	ErrActivityQueryInvalid         = workloadquery.ErrInvalidQuery
	ErrActivityOwnerNotFound        = workloadquery.ErrOwnerNotFound
	ErrActivityExecutionNotFound    = workloadquery.ErrExecutionNotFound

	activityEnvironmentIDPattern = regexp.MustCompile(`^env_[A-Za-z0-9_-]{1,124}$`)
	activitySessionIDPattern     = regexp.MustCompile(`^ses_[A-Za-z0-9_-]{1,124}$`)
)

type ActivityOwnerSelector struct {
	EnvironmentID        string `json:"environmentId,omitempty"`
	BackendIncarnationID string `json:"backendIncarnationId,omitempty"`
	SessionID            string `json:"sessionId,omitempty"`
}

func (selector ActivityOwnerSelector) Validate() error {
	switch {
	case selector.SessionID != "":
		if selector.EnvironmentID != "" || selector.BackendIncarnationID != "" ||
			!activitySessionIDPattern.MatchString(selector.SessionID) {
			return ErrActivityQueryInvalid
		}
	case selector.EnvironmentID != "" || selector.BackendIncarnationID != "":
		if !activityEnvironmentIDPattern.MatchString(selector.EnvironmentID) ||
			!boundedActivitySelector(selector.BackendIncarnationID, 256) {
			return ErrActivityQueryInvalid
		}
	default:
		return ErrActivityQueryInvalid
	}
	return nil
}

type ActivitySummaryQuery = workloadquery.SummaryQuery
type ActivitySummaryResult = workloadquery.SummaryResult
type ActivityEventsQuery = workloadquery.EventsQuery
type ActivityEventsPage = workloadquery.EventsPage
type ActivityExecutionsQuery = workloadquery.ExecutionsQuery
type ActivityExecutionNode = workloadquery.ExecutionNode
type ActivityExecutionsResult = workloadquery.ExecutionsResult
type ActivityCoverageQuery = workloadquery.CoverageQuery
type ActivityCoverageResult = workloadquery.CoverageResult
type ActivityRisksQuery = workloadquery.RisksQuery
type ActivityRisksResult = workloadquery.RisksResult

type ActivityProvider interface {
	ResolveActivityOwner(
		context.Context,
		ActivityOwnerSelector,
	) (workloadtypes.ActivityOwner, error)
	ActivitySummary(context.Context, ActivitySummaryQuery) (ActivitySummaryResult, error)
	ActivityEvents(context.Context, ActivityEventsQuery) (ActivityEventsPage, error)
	ActivityExecutions(
		context.Context,
		ActivityExecutionsQuery,
	) (ActivityExecutionsResult, error)
	ActivityCoverage(context.Context, ActivityCoverageQuery) (ActivityCoverageResult, error)
	ActivityRisks(context.Context, ActivityRisksQuery) (ActivityRisksResult, error)
}

type ActivityOwnerResolver interface {
	ResolveActivityOwner(
		context.Context,
		ActivityOwnerSelector,
	) (workloadtypes.ActivityOwner, error)
}

type ActivityOwnerResolverFunc func(
	context.Context,
	ActivityOwnerSelector,
) (workloadtypes.ActivityOwner, error)

func (resolver ActivityOwnerResolverFunc) ResolveActivityOwner(
	ctx context.Context,
	selector ActivityOwnerSelector,
) (workloadtypes.ActivityOwner, error) {
	if resolver == nil {
		return workloadtypes.ActivityOwner{}, ErrActivityOwnerNotFound
	}
	return resolver(ctx, selector)
}

type ActivityService struct {
	OwnerResolver ActivityOwnerResolver
	Query         *workloadquery.Service
}

func (service ActivityService) ResolveActivityOwner(
	ctx context.Context,
	selector ActivityOwnerSelector,
) (workloadtypes.ActivityOwner, error) {
	if err := selector.Validate(); err != nil {
		return workloadtypes.ActivityOwner{}, err
	}
	if service.OwnerResolver == nil {
		return workloadtypes.ActivityOwner{}, ErrActivityOwnerNotFound
	}
	owner, err := service.OwnerResolver.ResolveActivityOwner(ctx, selector)
	if err != nil {
		return workloadtypes.ActivityOwner{}, err
	}
	if err := owner.Validate(); err != nil || !ownerMatchesSelector(owner, selector) {
		return workloadtypes.ActivityOwner{}, errors.New(
			"resolved activity owner does not match the requested exact owner",
		)
	}
	return owner, nil
}

func (service ActivityService) ActivitySummary(
	ctx context.Context,
	query ActivitySummaryQuery,
) (ActivitySummaryResult, error) {
	if service.Query == nil {
		return ActivitySummaryResult{}, ErrActivityOwnerNotFound
	}
	return service.Query.Summary(ctx, query)
}

func (service ActivityService) ActivityEvents(
	ctx context.Context,
	query ActivityEventsQuery,
) (ActivityEventsPage, error) {
	if service.Query == nil {
		return ActivityEventsPage{}, ErrActivityOwnerNotFound
	}
	return service.Query.Events(ctx, query)
}

func (service ActivityService) ActivityExecutions(
	ctx context.Context,
	query ActivityExecutionsQuery,
) (ActivityExecutionsResult, error) {
	if service.Query == nil {
		return ActivityExecutionsResult{}, ErrActivityOwnerNotFound
	}
	return service.Query.Executions(ctx, query)
}

func (service ActivityService) ActivityCoverage(
	ctx context.Context,
	query ActivityCoverageQuery,
) (ActivityCoverageResult, error) {
	if service.Query == nil {
		return ActivityCoverageResult{}, ErrActivityOwnerNotFound
	}
	return service.Query.Coverage(ctx, query)
}

func (service ActivityService) ActivityRisks(
	ctx context.Context,
	query ActivityRisksQuery,
) (ActivityRisksResult, error) {
	if service.Query == nil {
		return ActivityRisksResult{}, ErrActivityOwnerNotFound
	}
	return service.Query.Risks(ctx, query)
}

func ownerMatchesSelector(
	owner workloadtypes.ActivityOwner,
	selector ActivityOwnerSelector,
) bool {
	if selector.SessionID != "" {
		return owner.Kind == workloadtypes.OwnerDisposableSession &&
			owner.SessionID == selector.SessionID
	}
	return owner.Kind == workloadtypes.OwnerReusableEnvironment &&
		owner.EnvironmentID == selector.EnvironmentID &&
		owner.BackendIncarnationID == selector.BackendIncarnationID
}

func boundedActivitySelector(value string, maximum int) bool {
	return len(value) >= 1 && len(value) <= maximum &&
		strings.TrimSpace(value) == value &&
		!strings.ContainsFunc(value, unicode.IsControl)
}
