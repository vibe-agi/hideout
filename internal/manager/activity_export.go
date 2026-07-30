package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	exportboundary "github.com/vibe-agi/hideout/internal/export"
	"github.com/vibe-agi/hideout/internal/profile"
	workloadredact "github.com/vibe-agi/hideout/internal/workloadobs/redact"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const (
	ActivityExportDraftSchema = "hideout.activity-export-draft.v1"
	ActivityExportPlanSchema  = "hideout.activity-export-plan.v1"
	ActivityExportApplySchema = "hideout.activity-export-apply.v1"

	ActivityExportDocumentSchema = "hideout.activity-export.v1"

	ActivityExportPathRedactHost = string(exportboundary.PathPolicyRedactHost)
	ActivityExportPathPreserve   = string(exportboundary.PathPolicyPreserve)

	activityExportPlanDomain     = "activity-export-plan"
	activityExportSourceDomain   = "activity-export-source"
	activityExportArtifactDomain = "activity-export-artifact"
	activityExportPreStage       = "activity.pre-persistence"
	defaultActivityExportTTL     = 15 * time.Minute
	maxActivityExportSelectors   = 128
	maxActivityExportPathBytes   = 4096
)

const activityExportPrivacyNotice = "Unknown application argv, paths, domains, and values may contain user data; review before sharing."

var (
	ErrInvalidActivityExportDraft = errors.New(
		"activity export draft is invalid",
	)
	ErrInvalidActivityExportPlan = errors.New(
		"activity export plan is invalid",
	)
	ErrInvalidActivityExportApply = errors.New(
		"activity export apply request is invalid",
	)
	ErrStaleActivityExportPlan = errors.New(
		"activity export plan is stale",
	)
	ErrActivityExportConfirmationRequired = errors.New(
		"activity export apply requires confirmation",
	)
	ErrActivityExportReviewRequired = errors.New(
		"activity export still requires selected redaction or explicit full-fidelity acknowledgement",
	)
	ErrActivityExportShareApprovalRequired = errors.New(
		"activity share must be applied by approving its evidence.share decision",
	)

	activityExportApplyLocks sync.Map
)

type ActivityExportDraft struct {
	Schema string                `json:"schema"`
	Owner  ActivityOwnerSelector `json:"owner"`

	From       time.Time `json:"from,omitempty"`
	To         time.Time `json:"to,omitempty"`
	Limit      int       `json:"limit,omitempty"`
	Kinds      []string  `json:"kinds,omitempty"`
	Operations []string  `json:"operations,omitempty"`
	Executions []string  `json:"executions,omitempty"`
	Risks      []string  `json:"risks,omitempty"`
	Path       string    `json:"path,omitempty"`
	Domain     string    `json:"domain,omitempty"`
	IP         string    `json:"ip,omitempty"`

	Out                     string   `json:"out"`
	Redact                  []string `json:"redact,omitempty"`
	PolicyProfile           string   `json:"policyProfile,omitempty"`
	PathPolicy              string   `json:"pathPolicy,omitempty"`
	AcknowledgeFullFidelity bool     `json:"acknowledgeFullFidelity,omitempty"`
	Share                   bool     `json:"share,omitempty"`
}

type ActivityExportScope struct {
	From       *time.Time `json:"from,omitempty"`
	To         *time.Time `json:"to,omitempty"`
	Limit      int        `json:"limit"`
	Kinds      []string   `json:"kinds,omitempty"`
	Operations []string   `json:"operations,omitempty"`
	Executions []string   `json:"executions,omitempty"`
	Risks      []string   `json:"risks,omitempty"`
	Path       string     `json:"path,omitempty"`
	Domain     string     `json:"domain,omitempty"`
	IP         string     `json:"ip,omitempty"`
}

type ActivityExportPlan struct {
	Schema      string `json:"schema"`
	OperationID string `json:"operationId"`
	PlanDigest  string `json:"planDigest"`

	Owner          workloadtypes.ActivityOwner `json:"owner"`
	Scope          ActivityExportScope         `json:"scope"`
	RecordCount    int                         `json:"recordCount"`
	QueryTruncated bool                        `json:"queryTruncated"`
	SourceDigest   string                      `json:"sourceDigest"`
	ArtifactDigest string                      `json:"artifactDigest"`

	Redact                  []string `json:"redact,omitempty"`
	PolicyProfile           string   `json:"policyProfile,omitempty"`
	PathPolicy              string   `json:"pathPolicy"`
	Destination             string   `json:"destination"`
	AcknowledgeFullFidelity bool     `json:"acknowledgeFullFidelity,omitempty"`
	Share                   bool     `json:"share"`
	DecisionID              string   `json:"decisionId,omitempty"`
	PrivacyNotice           string   `json:"privacyNotice"`

	Review    exportboundary.Review `json:"review"`
	CreatedAt time.Time             `json:"createdAt"`
	ExpiresAt time.Time             `json:"expiresAt"`
}

type ActivityExportApplyRequest struct {
	Schema    string             `json:"schema"`
	Plan      ActivityExportPlan `json:"plan"`
	Confirmed bool               `json:"confirmed"`
}

type ActivityExportApplyResult struct {
	OperationID string                `json:"operationId"`
	PlanDigest  string                `json:"planDigest"`
	Export      exportboundary.Result `json:"export"`
}

type ActivityExportAPIRequest struct {
	Draft     *ActivityExportDraft `json:"draft,omitempty"`
	Plan      *ActivityExportPlan  `json:"plan,omitempty"`
	Confirmed bool                 `json:"confirmed,omitempty"`
}

type ActivityExportService struct {
	Core     Core
	Provider ActivityProvider
	Now      func() time.Time
	PlanTTL  time.Duration
}

type activityExportDocument struct {
	Schema         string                           `json:"schema"`
	Owner          workloadtypes.ActivityOwner      `json:"owner"`
	Scope          ActivityExportScope              `json:"scope"`
	Records        []workloadtypes.ActivityRecord   `json:"records"`
	Coverage       []workloadtypes.CoverageInterval `json:"coverage"`
	QueryTruncated bool                             `json:"queryTruncated"`
	PrivacyNotice  string                           `json:"privacyNotice"`
}

func (service ActivityExportService) Plan(
	ctx context.Context,
	draft ActivityExportDraft,
) (ActivityExportPlan, error) {
	if err := activityExportContext(ctx); err != nil {
		return ActivityExportPlan{}, err
	}
	normalized, err := normalizeActivityExportDraft(draft)
	if err != nil {
		return ActivityExportPlan{}, err
	}
	if service.Provider == nil ||
		strings.TrimSpace(service.Core.Store.Root) == "" ||
		!filepath.IsAbs(service.Core.Store.Root) {
		return ActivityExportPlan{}, ErrInvalidActivityExportDraft
	}
	owner, err := service.Provider.ResolveActivityOwner(ctx, normalized.Owner)
	if err != nil {
		return ActivityExportPlan{}, err
	}
	if owner.Validate() != nil || !ownerMatchesSelector(owner, normalized.Owner) {
		return ActivityExportPlan{}, errors.Join(
			ErrInvalidActivityExportDraft,
			errors.New("activity provider rebound the exact owner"),
		)
	}
	scope := activityExportScopeFromDraft(normalized)
	document, sourceDigest, err := service.snapshot(ctx, owner, scope)
	if err != nil {
		return ActivityExportPlan{}, err
	}
	createdAt := service.nowUTC()
	opts, err := service.exportOptions(
		document,
		normalized.Out,
		normalized.Redact,
		normalized.PolicyProfile,
		normalized.PathPolicy,
		normalized.AcknowledgeFullFidelity,
		normalized.Share,
		createdAt,
	)
	if err != nil {
		return ActivityExportPlan{}, err
	}
	exportPlan, err := service.Core.PlanExport(opts)
	if err != nil {
		return ActivityExportPlan{}, err
	}
	artifactDigest, err := activityExportArtifactDigest(exportPlan)
	if err != nil {
		return ActivityExportPlan{}, err
	}
	operationID, err := NewOperationID()
	if err != nil {
		return ActivityExportPlan{}, err
	}
	plan := ActivityExportPlan{
		Schema: ActivityExportPlanSchema, OperationID: operationID,
		Owner: owner, Scope: scope,
		RecordCount: len(document.Records), QueryTruncated: document.QueryTruncated,
		SourceDigest: sourceDigest, ArtifactDigest: artifactDigest,
		Redact:        append([]string(nil), normalized.Redact...),
		PolicyProfile: normalized.PolicyProfile,
		PathPolicy:    normalized.PathPolicy, Destination: normalized.Out,
		AcknowledgeFullFidelity: normalized.AcknowledgeFullFidelity,
		Share:                   normalized.Share, DecisionID: exportPlan.DecisionID,
		PrivacyNotice: activityExportPrivacyNotice,
		Review:        exportPlan.Review,
		CreatedAt:     createdAt, ExpiresAt: createdAt.Add(service.planTTL()),
	}
	if err := plan.Seal(); err != nil {
		return ActivityExportPlan{}, err
	}
	return plan, nil
}

func (service ActivityExportService) Apply(
	ctx context.Context,
	request ActivityExportApplyRequest,
) (ActivityExportApplyResult, error) {
	if err := request.Validate(); err != nil {
		return ActivityExportApplyResult{}, err
	}
	if err := activityExportContext(ctx); err != nil {
		return ActivityExportApplyResult{}, err
	}
	if !request.Confirmed {
		return ActivityExportApplyResult{},
			ErrActivityExportConfirmationRequired
	}
	if request.Plan.Review.DecisionRequired {
		return ActivityExportApplyResult{}, ErrActivityExportReviewRequired
	}
	if request.Plan.Share {
		return ActivityExportApplyResult{},
			ErrActivityExportShareApprovalRequired
	}
	if !service.nowUTC().Before(request.Plan.ExpiresAt) {
		return ActivityExportApplyResult{}, ErrStaleActivityExportPlan
	}
	if service.Provider == nil {
		return ActivityExportApplyResult{}, ErrStaleActivityExportPlan
	}
	lock := activityExportApplyLock(
		service.Core.Store.Root + "\x00" + request.Plan.OperationID,
	)
	lock.Lock()
	defer lock.Unlock()

	selector := activityExportSelector(request.Plan.Owner)
	owner, err := service.Provider.ResolveActivityOwner(ctx, selector)
	if err != nil {
		return ActivityExportApplyResult{}, activityExportApplySourceError(err)
	}
	if !owner.Equal(request.Plan.Owner) {
		return ActivityExportApplyResult{}, ErrStaleActivityExportPlan
	}
	document, sourceDigest, err := service.snapshot(
		ctx,
		owner,
		request.Plan.Scope,
	)
	if err != nil {
		return ActivityExportApplyResult{}, activityExportApplySourceError(err)
	}
	if sourceDigest != request.Plan.SourceDigest ||
		len(document.Records) != request.Plan.RecordCount ||
		document.QueryTruncated != request.Plan.QueryTruncated {
		return ActivityExportApplyResult{}, ErrStaleActivityExportPlan
	}
	opts, err := service.exportOptions(
		document,
		request.Plan.Destination,
		request.Plan.Redact,
		request.Plan.PolicyProfile,
		request.Plan.PathPolicy,
		request.Plan.AcknowledgeFullFidelity,
		false,
		request.Plan.CreatedAt,
	)
	if err != nil {
		return ActivityExportApplyResult{}, err
	}
	current, err := service.Core.PlanExport(opts)
	if err != nil {
		return ActivityExportApplyResult{}, err
	}
	artifactDigest, err := activityExportArtifactDigest(current)
	if err != nil {
		return ActivityExportApplyResult{}, err
	}
	if artifactDigest != request.Plan.ArtifactDigest ||
		activityExportReviewDigest(current.Review) !=
			activityExportReviewDigest(request.Plan.Review) {
		return ActivityExportApplyResult{}, ErrStaleActivityExportPlan
	}
	result, err := service.Core.ApplyExport(current, opts)
	if errors.Is(err, exportboundary.ErrReviewedPlanMismatch) {
		return ActivityExportApplyResult{}, errors.Join(
			ErrStaleActivityExportPlan,
			err,
		)
	}
	if err != nil {
		return ActivityExportApplyResult{}, err
	}
	return ActivityExportApplyResult{
		OperationID: request.Plan.OperationID,
		PlanDigest:  request.Plan.PlanDigest,
		Export:      result,
	}, nil
}

func activityExportApplySourceError(err error) error {
	if errors.Is(err, ErrActivityOwnerNotFound) {
		return errors.Join(ErrStaleActivityExportPlan, err)
	}
	return err
}

func (request ActivityExportApplyRequest) Validate() error {
	if request.Schema != ActivityExportApplySchema {
		return ErrInvalidActivityExportApply
	}
	if err := request.Plan.VerifyDigest(); err != nil {
		return errors.Join(ErrInvalidActivityExportApply, err)
	}
	return nil
}

func (plan *ActivityExportPlan) Seal() error {
	if plan == nil {
		return ErrInvalidActivityExportPlan
	}
	plan.PlanDigest = ""
	if err := plan.validate(false); err != nil {
		return err
	}
	digest, err := CanonicalDigest(activityExportPlanDomain, *plan)
	if err != nil {
		return err
	}
	plan.PlanDigest = digest
	return plan.Validate()
}

func (plan ActivityExportPlan) Validate() error {
	return plan.validate(true)
}

func (plan ActivityExportPlan) VerifyDigest() error {
	if err := plan.Validate(); err != nil {
		return err
	}
	provided := plan.PlanDigest
	plan.PlanDigest = ""
	expected, err := CanonicalDigest(activityExportPlanDomain, plan)
	if err != nil {
		return err
	}
	if provided != expected {
		return errors.Join(
			ErrInvalidActivityExportPlan,
			errors.New("plan digest mismatch"),
		)
	}
	return nil
}

func (plan ActivityExportPlan) validate(requireDigest bool) error {
	if plan.Schema != ActivityExportPlanSchema ||
		!operationIDPattern.MatchString(plan.OperationID) ||
		plan.Owner.Validate() != nil ||
		plan.Scope.validate() != nil ||
		plan.RecordCount < 0 ||
		plan.RecordCount > plan.Scope.Limit ||
		!profileDigestPattern.MatchString(plan.SourceDigest) ||
		!profileDigestPattern.MatchString(plan.ArtifactDigest) ||
		plan.Destination == "" ||
		!validActivityExportDestination(plan.Destination) ||
		plan.PrivacyNotice != activityExportPrivacyNotice ||
		plan.CreatedAt.IsZero() ||
		!plan.ExpiresAt.After(plan.CreatedAt) ||
		plan.ExpiresAt.Sub(plan.CreatedAt) > time.Hour ||
		len(plan.Redact) > maxActivityExportSelectors ||
		plan.Review.Source != exportboundary.SourceActivity ||
		plan.Review.RecordCount != plan.RecordCount ||
		!sameActivityExportStrings(plan.Redact, plan.Review.RedactSelectors) {
		return ErrInvalidActivityExportPlan
	}
	if requireDigest {
		if !profileDigestPattern.MatchString(plan.PlanDigest) {
			return ErrInvalidActivityExportPlan
		}
	} else if plan.PlanDigest != "" {
		return ErrInvalidActivityExportPlan
	}
	switch plan.PathPolicy {
	case ActivityExportPathRedactHost:
		if !activityExportReviewHasStage(
			plan.Review,
			exportboundary.PublicEvidenceLocalPathStage,
		) {
			return ErrInvalidActivityExportPlan
		}
	case ActivityExportPathPreserve:
		if !plan.AcknowledgeFullFidelity || plan.Share {
			return ErrInvalidActivityExportPlan
		}
	default:
		return ErrInvalidActivityExportPlan
	}
	if plan.Share {
		if plan.DecisionID == "" || plan.Review.DecisionRequired {
			return ErrInvalidActivityExportPlan
		}
	} else if plan.DecisionID != "" {
		return ErrInvalidActivityExportPlan
	}
	if len(plan.Redact) > 0 {
		if plan.PolicyProfile == "" ||
			profile.ValidateName(plan.PolicyProfile) != nil {
			return ErrInvalidActivityExportPlan
		}
	}
	if err := validateActivityExportSelectors(plan.Redact); err != nil {
		return ErrInvalidActivityExportPlan
	}
	return nil
}

func (scope ActivityExportScope) validate() error {
	if scope.Limit < 1 || scope.Limit > MaxOperatorActivityLimit ||
		(scope.From != nil && scope.To != nil &&
			scope.To.Before(*scope.From)) ||
		!validActivityExportTime(scope.From) ||
		!validActivityExportTime(scope.To) ||
		!activityExportValuesValid(scope.Kinds, validActivityAPIKind) ||
		!activityExportValuesValid(
			scope.Operations,
			activityOperationPattern.MatchString,
		) ||
		!activityExportValuesValid(
			scope.Executions,
			activityExecutionPattern.MatchString,
		) ||
		!activityExportValuesValid(
			scope.Risks,
			activityRiskPattern.MatchString,
		) ||
		!validActivitySearch(scope.Path, 4096) ||
		!validActivitySearch(scope.Domain, 253) ||
		!validActivitySearch(scope.IP, 64) {
		return ErrInvalidActivityExportPlan
	}
	if scope.IP != "" {
		address, err := netip.ParseAddr(scope.IP)
		if err != nil || address.Zone() != "" ||
			address.Unmap().String() != scope.IP {
			return ErrInvalidActivityExportPlan
		}
	}
	if scope.Domain != strings.ToLower(strings.TrimSuffix(scope.Domain, ".")) {
		return ErrInvalidActivityExportPlan
	}
	return nil
}

func (service ActivityExportService) snapshot(
	ctx context.Context,
	owner workloadtypes.ActivityOwner,
	scope ActivityExportScope,
) (activityExportDocument, string, error) {
	page, err := service.Provider.ActivityEvents(ctx, ActivityEventsQuery{
		Owner: owner, From: activityExportTime(scope.From),
		To: activityExportTime(scope.To), Limit: scope.Limit,
		Kinds:      append([]string(nil), scope.Kinds...),
		Operations: append([]string(nil), scope.Operations...),
		Executions: append([]string(nil), scope.Executions...),
		Risks:      append([]string(nil), scope.Risks...),
		Path:       scope.Path, Domain: scope.Domain, IP: scope.IP,
	})
	if err != nil {
		return activityExportDocument{}, "", err
	}
	if !validActivityEventsPage(page, owner, "", scope.Limit) {
		return activityExportDocument{}, "", errors.Join(
			ErrInvalidActivityExportDraft,
			errors.New("activity provider returned invalid export evidence"),
		)
	}
	redactor, err := workloadredact.New(workloadredact.Config{})
	if err != nil {
		return activityExportDocument{}, "", err
	}
	defer redactor.Clear()
	records := make([]workloadtypes.ActivityRecord, len(page.Records))
	for index, record := range page.Records {
		records[index], err = redactor.Activity(record)
		if err != nil {
			return activityExportDocument{}, "", errors.Join(
				ErrInvalidActivityExportDraft,
				fmt.Errorf("redact activity record %s: %w", record.ID, err),
			)
		}
	}
	document := activityExportDocument{
		Schema: ActivityExportDocumentSchema, Owner: owner, Scope: scope,
		Records: records,
		Coverage: append(
			[]workloadtypes.CoverageInterval(nil),
			page.Coverage...,
		),
		QueryTruncated: page.QueryTruncated,
		PrivacyNotice:  activityExportPrivacyNotice,
	}
	if document.Coverage == nil {
		document.Coverage = []workloadtypes.CoverageInterval{}
	}
	digest, err := CanonicalDigest(activityExportSourceDomain, document)
	if err != nil {
		return activityExportDocument{}, "", err
	}
	return document, digest, nil
}

func (service ActivityExportService) exportOptions(
	document activityExportDocument,
	out string,
	selectors []string,
	policyProfile, pathPolicy string,
	acknowledge, share bool,
	createdAt time.Time,
) (ExportOptions, error) {
	activity, err := json.Marshal(document)
	if err != nil {
		return ExportOptions{}, err
	}
	profileName := policyProfile
	if profileName == "" {
		profileName = "default"
	}
	return ExportOptions{
		Source:     exportboundary.SourceActivity,
		Session:    document.Owner.SessionID,
		Profile:    profileName,
		Activity:   activity,
		PathPolicy: exportboundary.PathPolicy(pathPolicy),
		PreRedactionStages: []exportboundary.RedactionStage{{
			Name: activityExportPreStage,
		}},
		Out:                     out,
		Redact:                  append([]string(nil), selectors...),
		PolicyProfile:           policyProfile,
		AcknowledgeFullFidelity: acknowledge,
		Share:                   share,
		CreatedAt:               createdAt.Round(0).UTC(),
	}, nil
}

func normalizeActivityExportDraft(
	draft ActivityExportDraft,
) (ActivityExportDraft, error) {
	if draft.Schema != ActivityExportDraftSchema ||
		draft.Owner.Validate() != nil {
		return ActivityExportDraft{}, ErrInvalidActivityExportDraft
	}
	if draft.Limit == 0 {
		draft.Limit = DefaultOperatorActivityLimit
	}
	if draft.PathPolicy == "" {
		draft.PathPolicy = ActivityExportPathRedactHost
	}
	draft.From = draft.From.Round(0).UTC()
	draft.To = draft.To.Round(0).UTC()
	draft.Out = filepath.Clean(draft.Out)
	if draft.Out == "." {
		draft.Out = ""
	}
	var err error
	draft.Kinds, err = normalizeActivityExportValues(
		draft.Kinds,
		validActivityAPIKind,
	)
	if err == nil {
		draft.Operations, err = normalizeActivityExportValues(
			draft.Operations,
			activityOperationPattern.MatchString,
		)
	}
	if err == nil {
		draft.Executions, err = normalizeActivityExportValues(
			draft.Executions,
			activityExecutionPattern.MatchString,
		)
	}
	if err == nil {
		draft.Risks, err = normalizeActivityExportValues(
			draft.Risks,
			activityRiskPattern.MatchString,
		)
	}
	if err != nil {
		return ActivityExportDraft{}, ErrInvalidActivityExportDraft
	}
	if draft.Domain != "" {
		draft.Domain = strings.ToLower(strings.TrimSuffix(draft.Domain, "."))
	}
	if draft.IP != "" {
		address, parseErr := netip.ParseAddr(draft.IP)
		if parseErr != nil || address.Zone() != "" {
			return ActivityExportDraft{}, ErrInvalidActivityExportDraft
		}
		draft.IP = address.Unmap().String()
	}
	if draft.Limit < 1 || draft.Limit > MaxOperatorActivityLimit ||
		(!draft.From.IsZero() && !draft.To.IsZero() &&
			draft.To.Before(draft.From)) ||
		!validActivitySearch(draft.Path, 4096) ||
		!validActivitySearch(draft.Domain, 253) ||
		!validActivitySearch(draft.IP, 64) ||
		!validActivityExportDestination(draft.Out) ||
		validateActivityExportSelectors(draft.Redact) != nil {
		return ActivityExportDraft{}, ErrInvalidActivityExportDraft
	}
	if len(draft.Redact) > 0 {
		if draft.PolicyProfile == "" ||
			profile.ValidateName(draft.PolicyProfile) != nil {
			return ActivityExportDraft{}, ErrInvalidActivityExportDraft
		}
	} else if draft.PolicyProfile != "" &&
		profile.ValidateName(draft.PolicyProfile) != nil {
		return ActivityExportDraft{}, ErrInvalidActivityExportDraft
	}
	switch draft.PathPolicy {
	case ActivityExportPathRedactHost:
	case ActivityExportPathPreserve:
		if !draft.AcknowledgeFullFidelity || draft.Share {
			return ActivityExportDraft{}, ErrInvalidActivityExportDraft
		}
	default:
		return ActivityExportDraft{}, ErrInvalidActivityExportDraft
	}
	return draft, nil
}

func normalizeActivityExportValues(
	values []string,
	valid func(string) bool,
) ([]string, error) {
	if len(values) > 128 {
		return nil, ErrInvalidActivityExportDraft
	}
	result := append([]string(nil), values...)
	sort.Strings(result)
	for index, value := range result {
		if !valid(value) ||
			(index > 0 && result[index-1] == value) {
			return nil, ErrInvalidActivityExportDraft
		}
	}
	return result, nil
}

func activityExportValuesValid(
	values []string,
	valid func(string) bool,
) bool {
	if len(values) > 128 {
		return false
	}
	for index, value := range values {
		if !valid(value) ||
			(index > 0 && values[index-1] >= value) {
			return false
		}
	}
	return true
}

func validateActivityExportSelectors(selectors []string) error {
	if len(selectors) > maxActivityExportSelectors {
		return ErrInvalidActivityExportDraft
	}
	seen := make(map[string]struct{}, len(selectors))
	for _, selector := range selectors {
		if selector == "" || len(selector) > 512 ||
			strings.TrimSpace(selector) != selector ||
			!utf8.ValidString(selector) ||
			strings.ContainsFunc(selector, unicode.IsControl) {
			return ErrInvalidActivityExportDraft
		}
		if _, exists := seen[selector]; exists {
			return ErrInvalidActivityExportDraft
		}
		seen[selector] = struct{}{}
	}
	return nil
}

func validActivityExportDestination(path string) bool {
	if path == "" || len(path) > maxActivityExportPathBytes ||
		!utf8.ValidString(path) ||
		strings.ContainsFunc(path, unicode.IsControl) {
		return false
	}
	parsed, err := url.Parse(path)
	return err == nil && parsed.Scheme == ""
}

func activityExportScopeFromDraft(
	draft ActivityExportDraft,
) ActivityExportScope {
	return ActivityExportScope{
		From:       activityExportTimePointer(draft.From),
		To:         activityExportTimePointer(draft.To),
		Limit:      draft.Limit,
		Kinds:      append([]string(nil), draft.Kinds...),
		Operations: append([]string(nil), draft.Operations...),
		Executions: append([]string(nil), draft.Executions...),
		Risks:      append([]string(nil), draft.Risks...),
		Path:       draft.Path, Domain: draft.Domain, IP: draft.IP,
	}
}

func activityExportTimePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	normalized := value.Round(0).UTC()
	return &normalized
}

func activityExportTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return value.Round(0).UTC()
}

func validActivityExportTime(value *time.Time) bool {
	if value == nil {
		return true
	}
	return !value.IsZero() &&
		value.Equal(value.Round(0).UTC()) &&
		value.Location() == time.UTC
}

func activityExportSelector(
	owner workloadtypes.ActivityOwner,
) ActivityOwnerSelector {
	if owner.Kind == workloadtypes.OwnerDisposableSession {
		return ActivityOwnerSelector{SessionID: owner.SessionID}
	}
	return ActivityOwnerSelector{
		EnvironmentID:        owner.EnvironmentID,
		BackendIncarnationID: owner.BackendIncarnationID,
	}
}

func activityExportArtifactDigest(
	plan exportboundary.Plan,
) (string, error) {
	return CanonicalDigest(activityExportArtifactDomain, struct {
		Artifact exportboundary.Artifact `json:"artifact"`
		Review   exportboundary.Review   `json:"review"`
	}{
		Artifact: plan.Artifact,
		Review:   plan.Review,
	})
}

func activityExportReviewDigest(review exportboundary.Review) string {
	digest, err := CanonicalDigest(activityExportArtifactDomain, review)
	if err != nil {
		return ""
	}
	return digest
}

func activityExportReviewHasStage(
	review exportboundary.Review,
	name string,
) bool {
	for _, stage := range review.RedactionStages {
		if stage.Name == name {
			return true
		}
	}
	return false
}

func sameActivityExportStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func activityExportContext(ctx context.Context) error {
	if ctx == nil {
		return context.Canceled
	}
	return ctx.Err()
}

func (service ActivityExportService) nowUTC() time.Time {
	if service.Now != nil {
		return service.Now().Round(0).UTC()
	}
	return time.Now().UTC()
}

func (service ActivityExportService) planTTL() time.Duration {
	if service.PlanTTL > 0 && service.PlanTTL <= time.Hour {
		return service.PlanTTL
	}
	return defaultActivityExportTTL
}

func activityExportApplyLock(key string) *sync.Mutex {
	value, _ := activityExportApplyLocks.LoadOrStore(key, &sync.Mutex{})
	return value.(*sync.Mutex)
}
