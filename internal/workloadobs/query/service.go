package query

import (
	"context"
	"errors"
	"net/netip"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vibe-agi/hideout/internal/workloadobs/risk"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

type Service struct {
	source    Source
	cursorKey []byte
}

func NewService(options Options) (*Service, error) {
	if options.Source == nil ||
		len(options.CursorKey) < 32 || len(options.CursorKey) > 128 {
		return nil, ErrInvalidOptions
	}
	return &Service{
		source: options.Source, cursorKey: append([]byte(nil), options.CursorKey...),
	}, nil
}

func (service *Service) loadSnapshot(
	ctx context.Context,
	owner workloadtypes.ActivityOwner,
) (Snapshot, error) {
	if service == nil || service.source == nil || owner.Validate() != nil {
		return Snapshot{}, ErrInvalidQuery
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	snapshot, err := service.source.Snapshot(ctx, owner)
	if err != nil {
		return Snapshot{}, err
	}
	normalized, err := normalizeSnapshot(snapshot)
	if err != nil {
		return Snapshot{}, err
	}
	if !normalized.Owner.Equal(owner) {
		return Snapshot{}, ErrInvalidSnapshot
	}
	return normalized, nil
}

func normalizeEventsQuery(input EventsQuery) (EventsQuery, eventFilter, bool, error) {
	if input.Owner.Validate() != nil ||
		!validSessionForOwner(input.Owner, input.SessionID) ||
		len(input.Cursor) > maxCursorLength ||
		containsControl(input.Cursor) {
		return EventsQuery{}, eventFilter{}, false, ErrInvalidQuery
	}
	if input.Limit == 0 {
		input.Limit = DefaultLimit
	}
	if input.Limit < 1 || input.Limit > MaximumLimit {
		return EventsQuery{}, eventFilter{}, false, ErrInvalidQuery
	}
	explicit := hasExplicitEventFilter(input)
	filter, err := normalizeEventFilter(eventFilter{
		SessionID: input.SessionID, From: input.From, To: input.To,
		Kinds: input.Kinds, Operations: input.Operations,
		Executions: input.Executions, Risks: input.Risks,
		Path: input.Path, Domain: input.Domain, IP: input.IP,
	})
	if err != nil {
		return EventsQuery{}, eventFilter{}, false, err
	}
	applyEventFilter(&input, filter)
	return input, filter, explicit, nil
}

func normalizeEventFilter(input eventFilter) (eventFilter, error) {
	input.From = normalizeTime(input.From)
	input.To = normalizeTime(input.To)
	if !validSessionID(input.SessionID) ||
		!validTimeRange(input.From, input.To) {
		return eventFilter{}, ErrInvalidQuery
	}
	var err error
	input.Kinds, err = normalizeCodes(input.Kinds, validActivityKind)
	if err != nil {
		return eventFilter{}, err
	}
	input.Operations, err = normalizeCodes(input.Operations, operationPattern.MatchString)
	if err != nil {
		return eventFilter{}, err
	}
	input.Executions, err = normalizeCodes(input.Executions, executionPattern.MatchString)
	if err != nil {
		return eventFilter{}, err
	}
	input.Risks, err = normalizeCodes(input.Risks, riskPattern.MatchString)
	if err != nil {
		return eventFilter{}, err
	}
	if !validSearch(input.Path, 4096) ||
		!validSearch(input.Domain, 253) ||
		!validSearch(input.IP, 64) {
		return eventFilter{}, ErrInvalidQuery
	}
	if input.Domain != "" {
		input.Domain = strings.ToLower(strings.TrimSuffix(input.Domain, "."))
		if input.Domain == "" {
			return eventFilter{}, ErrInvalidQuery
		}
	}
	if input.IP != "" {
		address, err := netip.ParseAddr(input.IP)
		if err != nil {
			return eventFilter{}, ErrInvalidQuery
		}
		input.IP = address.Unmap().String()
	}
	return input, nil
}

func applyEventFilter(query *EventsQuery, filter eventFilter) {
	query.SessionID = filter.SessionID
	query.From, query.To = filter.From, filter.To
	query.Kinds = append([]string(nil), filter.Kinds...)
	query.Operations = append([]string(nil), filter.Operations...)
	query.Executions = append([]string(nil), filter.Executions...)
	query.Risks = append([]string(nil), filter.Risks...)
	query.Path, query.Domain, query.IP = filter.Path, filter.Domain, filter.IP
}

func hasExplicitEventFilter(query EventsQuery) bool {
	return query.SessionID != "" ||
		!query.From.IsZero() || !query.To.IsZero() ||
		len(query.Kinds) != 0 || len(query.Operations) != 0 ||
		len(query.Executions) != 0 || len(query.Risks) != 0 ||
		query.Path != "" || query.Domain != "" || query.IP != ""
}

func normalizeSummaryQuery(input SummaryQuery) (SummaryQuery, error) {
	input.From = normalizeTime(input.From)
	input.To = normalizeTime(input.To)
	if input.Owner.Validate() != nil ||
		!validSessionForOwner(input.Owner, input.SessionID) ||
		!validTimeRange(input.From, input.To) {
		return SummaryQuery{}, ErrInvalidQuery
	}
	return input, nil
}

func normalizeExecutionsQuery(input ExecutionsQuery) (ExecutionsQuery, error) {
	if input.Owner.Validate() != nil ||
		!validSessionForOwner(input.Owner, input.SessionID) ||
		(input.ID != "" && !executionPattern.MatchString(input.ID)) ||
		(input.ID != "" && input.RootsOnly) {
		return ExecutionsQuery{}, ErrInvalidQuery
	}
	return input, nil
}

func normalizeCoverageQuery(input CoverageQuery) (CoverageQuery, error) {
	input.From = normalizeTime(input.From)
	input.To = normalizeTime(input.To)
	if input.Owner.Validate() != nil ||
		!validSessionForOwner(input.Owner, input.SessionID) ||
		!validTimeRange(input.From, input.To) {
		return CoverageQuery{}, ErrInvalidQuery
	}
	var err error
	input.Subsystems, err = normalizeCodes(input.Subsystems, validSubsystem)
	if err != nil {
		return CoverageQuery{}, err
	}
	return input, nil
}

func normalizeRisksQuery(input RisksQuery) (RisksQuery, error) {
	input.From = normalizeTime(input.From)
	input.To = normalizeTime(input.To)
	if input.Owner.Validate() != nil ||
		!validSessionForOwner(input.Owner, input.SessionID) ||
		!validTimeRange(input.From, input.To) {
		return RisksQuery{}, ErrInvalidQuery
	}
	var err error
	input.Severities, err = normalizeCodes(input.Severities, validSeverity)
	if err != nil {
		return RisksQuery{}, err
	}
	input.Rules, err = normalizeCodes(input.Rules, func(value string) bool {
		return riskPattern.MatchString(value) && !strings.HasPrefix(value, "risk_")
	})
	if err != nil {
		return RisksQuery{}, err
	}
	input.Executions, err = normalizeCodes(input.Executions, executionPattern.MatchString)
	if err != nil {
		return RisksQuery{}, err
	}
	return input, nil
}

func normalizeCodes(
	values []string,
	valid func(string) bool,
) ([]string, error) {
	if len(values) > 128 {
		return nil, ErrInvalidQuery
	}
	normalized := append([]string(nil), values...)
	sort.Strings(normalized)
	previous := ""
	for _, value := range normalized {
		if !valid(value) || value == previous {
			return nil, ErrInvalidQuery
		}
		previous = value
	}
	return normalized, nil
}

func validTimeRange(from, to time.Time) bool {
	return from.IsZero() || to.IsZero() || !to.Before(from)
}

func validSessionID(value string) bool {
	return value == "" || sessionPattern.MatchString(value)
}

func validSessionForOwner(
	owner workloadtypes.ActivityOwner,
	sessionID string,
) bool {
	return validSessionID(sessionID) &&
		(sessionID == "" ||
			owner.Kind != workloadtypes.OwnerDisposableSession ||
			owner.SessionID == sessionID)
}

func validSearch(value string, maximum int) bool {
	return len(value) <= maximum && utf8.ValidString(value) && !containsControl(value)
}

func containsControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func validActivityKind(value string) bool {
	switch value {
	case workloadtypes.ActivityProcess, workloadtypes.ActivityFile,
		workloadtypes.ActivityConnection, workloadtypes.ActivityDNS,
		workloadtypes.ActivityRisk:
		return true
	default:
		return false
	}
}

func validSubsystem(value string) bool {
	switch value {
	case workloadtypes.SubsystemProcess, workloadtypes.SubsystemFile,
		workloadtypes.SubsystemNetwork, workloadtypes.SubsystemDNS:
		return true
	default:
		return false
	}
}

func validSeverity(value string) bool {
	switch value {
	case risk.SeverityInfo, risk.SeverityLow, risk.SeverityMedium,
		risk.SeverityHigh, risk.SeverityCritical:
		return true
	default:
		return false
	}
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return errors.Join(ErrInvalidQuery, errors.New("activity query context is nil"))
	}
	return ctx.Err()
}
