package manager

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	workloadstore "github.com/vibe-agi/hideout/internal/workloadobs/store"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const (
	ActivityCleanupPlanSchema   = "hideout.activity-cleanup-plan.v1"
	ActivityCleanupResultSchema = "hideout.activity-cleanup-result.v1"

	ActivityCleanupEnvironmentClean    = "environment-clean"
	ActivityCleanupEnvironmentDelete   = "environment-delete"
	ActivityCleanupEnvironmentRecreate = "environment-recreate"
	ActivityCleanupDisposableTerminal  = "disposable-terminal"

	ActivityCleanupScopeEnvironment = "environment"
	ActivityCleanupScopeSession     = "session"

	ActivityCleanupAbsent           = workloadstore.DeletionAbsent
	ActivityCleanupRecoveryRequired = "recovery-required"
)

var (
	ErrActivityCleanupPlanInvalid = errors.New("activity cleanup plan is invalid")
	ErrActivityCleanupStale       = errors.New("activity cleanup found an owner created after planning")

	activityCleanupSessionPattern = regexp.MustCompile(`^ses_[A-Za-z0-9_-]{1,124}$`)
)

type ActivityCleanupStore interface {
	Owners(context.Context) ([]workloadtypes.ActivityOwner, error)
	DeleteOwner(
		context.Context,
		workloadtypes.ActivityOwner,
	) (workloadstore.DeletionProof, error)
}

type ActivityCleanupPlan struct {
	Schema        string                        `json:"schema"`
	ID            string                        `json:"id"`
	Operation     string                        `json:"operation"`
	Scope         string                        `json:"scope"`
	EnvironmentID string                        `json:"environmentId,omitempty"`
	SessionID     string                        `json:"sessionId,omitempty"`
	Owners        []workloadtypes.ActivityOwner `json:"owners"`
	PlannedAt     time.Time                     `json:"plannedAt"`
}

type ActivityCleanupResult struct {
	Schema             string                        `json:"schema"`
	Plan               ActivityCleanupPlan           `json:"plan"`
	Status             string                        `json:"status"`
	Proofs             []workloadstore.DeletionProof `json:"proofs"`
	RemainingOwnerKeys []string                      `json:"remainingOwnerKeys"`
	CompletedAt        time.Time                     `json:"completedAt"`
}

type ActivityCleanupService struct {
	store ActivityCleanupStore
	now   func() time.Time
}

func NewActivityCleanupService(
	store ActivityCleanupStore,
	now func() time.Time,
) *ActivityCleanupService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &ActivityCleanupService{store: store, now: now}
}

func (service *ActivityCleanupService) PlanEnvironment(
	ctx context.Context,
	environmentID, operation string,
) (ActivityCleanupPlan, error) {
	if !environment.ValidID(environmentID) ||
		!slices.Contains([]string{
			ActivityCleanupEnvironmentClean,
			ActivityCleanupEnvironmentDelete,
			ActivityCleanupEnvironmentRecreate,
		}, operation) {
		return ActivityCleanupPlan{}, ErrActivityCleanupPlanInvalid
	}
	return service.plan(
		ctx, ActivityCleanupScopeEnvironment,
		environmentID, "", operation,
	)
}

func (service *ActivityCleanupService) PlanSession(
	ctx context.Context,
	sessionID, operation string,
) (ActivityCleanupPlan, error) {
	if !activityCleanupSessionPattern.MatchString(sessionID) ||
		!activityCleanupSessionOperation(operation) {
		return ActivityCleanupPlan{}, ErrActivityCleanupPlanInvalid
	}
	return service.plan(
		ctx, ActivityCleanupScopeSession,
		"", sessionID, operation,
	)
}

func (service *ActivityCleanupService) plan(
	ctx context.Context,
	scope, environmentID, sessionID, operation string,
) (ActivityCleanupPlan, error) {
	if service == nil || service.store == nil || ctx == nil {
		return ActivityCleanupPlan{}, ErrActivityCleanupPlanInvalid
	}
	if err := ctx.Err(); err != nil {
		return ActivityCleanupPlan{}, err
	}
	owners, err := service.store.Owners(ctx)
	if err != nil {
		return ActivityCleanupPlan{}, err
	}
	selected := make([]workloadtypes.ActivityOwner, 0)
	for _, owner := range owners {
		if cleanupOwnerMatches(owner, scope, environmentID, sessionID) {
			selected = append(selected, owner)
		}
	}
	sort.Slice(selected, func(left, right int) bool {
		return selected[left].Key() < selected[right].Key()
	})
	at := service.nowUTC()
	plan := ActivityCleanupPlan{
		Schema:    ActivityCleanupPlanSchema,
		Operation: operation, Scope: scope,
		EnvironmentID: environmentID, SessionID: sessionID,
		Owners:    append([]workloadtypes.ActivityOwner(nil), selected...),
		PlannedAt: at,
	}
	plan.ID = activityCleanupPlanID(plan)
	if err := plan.Validate(); err != nil {
		return ActivityCleanupPlan{}, err
	}
	return plan, nil
}

func (service *ActivityCleanupService) Apply(
	ctx context.Context,
	plan ActivityCleanupPlan,
) (ActivityCleanupResult, error) {
	if service == nil || service.store == nil || ctx == nil {
		return ActivityCleanupResult{}, ErrActivityCleanupPlanInvalid
	}
	if err := plan.Validate(); err != nil {
		return ActivityCleanupResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ActivityCleanupResult{}, err
	}
	result := ActivityCleanupResult{
		Schema: ActivityCleanupResultSchema, Plan: cloneActivityCleanupPlan(plan),
		Status:             ActivityCleanupRecoveryRequired,
		Proofs:             []workloadstore.DeletionProof{},
		RemainingOwnerKeys: []string{},
	}
	for _, owner := range plan.Owners {
		proof, err := service.store.DeleteOwner(ctx, owner)
		if err != nil {
			result.CompletedAt = service.nowUTC()
			return result, err
		}
		if err := proof.Validate(); err != nil ||
			!proof.Owner.Equal(owner) {
			result.CompletedAt = service.nowUTC()
			return result, errors.Join(
				ErrActivityCleanupPlanInvalid, err,
			)
		}
		result.Proofs = append(result.Proofs, proof)
	}
	owners, err := service.store.Owners(ctx)
	if err != nil {
		result.CompletedAt = service.nowUTC()
		return result, err
	}
	for _, owner := range owners {
		if cleanupOwnerMatches(
			owner, plan.Scope, plan.EnvironmentID, plan.SessionID,
		) {
			result.RemainingOwnerKeys = append(
				result.RemainingOwnerKeys, owner.Key(),
			)
		}
	}
	slices.Sort(result.RemainingOwnerKeys)
	result.RemainingOwnerKeys = slices.Compact(result.RemainingOwnerKeys)
	result.CompletedAt = service.nowUTC()
	if len(result.RemainingOwnerKeys) != 0 {
		return result, ErrActivityCleanupStale
	}
	result.Status = ActivityCleanupAbsent
	if err := result.Validate(); err != nil {
		return ActivityCleanupResult{}, err
	}
	return result, nil
}

func (plan ActivityCleanupPlan) Validate() error {
	if plan.Schema != ActivityCleanupPlanSchema ||
		plan.ID == "" || plan.ID != activityCleanupPlanID(plan) ||
		plan.PlannedAt.IsZero() || len(plan.Owners) > 1<<20 {
		return ErrActivityCleanupPlanInvalid
	}
	switch plan.Scope {
	case ActivityCleanupScopeEnvironment:
		if !environment.ValidID(plan.EnvironmentID) || plan.SessionID != "" ||
			!slices.Contains([]string{
				ActivityCleanupEnvironmentClean,
				ActivityCleanupEnvironmentDelete,
				ActivityCleanupEnvironmentRecreate,
			}, plan.Operation) {
			return ErrActivityCleanupPlanInvalid
		}
	case ActivityCleanupScopeSession:
		if plan.EnvironmentID != "" ||
			!activityCleanupSessionPattern.MatchString(plan.SessionID) ||
			!activityCleanupSessionOperation(plan.Operation) {
			return ErrActivityCleanupPlanInvalid
		}
	default:
		return ErrActivityCleanupPlanInvalid
	}
	previous := ""
	for _, owner := range plan.Owners {
		key := owner.Key()
		if owner.Validate() != nil || key == "" || key <= previous ||
			!cleanupOwnerMatches(
				owner, plan.Scope, plan.EnvironmentID, plan.SessionID,
			) {
			return ErrActivityCleanupPlanInvalid
		}
		previous = key
	}
	return nil
}

func activityCleanupSessionOperation(operation string) bool {
	return slices.Contains([]string{
		ActivityCleanupDisposableTerminal,
		ActivityCleanupEnvironmentClean,
		ActivityCleanupEnvironmentDelete,
		ActivityCleanupEnvironmentRecreate,
	}, operation)
}

func (result ActivityCleanupResult) Validate() error {
	if result.Schema != ActivityCleanupResultSchema ||
		result.Plan.Validate() != nil ||
		result.CompletedAt.IsZero() ||
		!slices.IsSorted(result.RemainingOwnerKeys) {
		return ErrActivityCleanupPlanInvalid
	}
	switch result.Status {
	case ActivityCleanupAbsent:
		if len(result.RemainingOwnerKeys) != 0 ||
			len(result.Proofs) != len(result.Plan.Owners) {
			return ErrActivityCleanupPlanInvalid
		}
	case ActivityCleanupRecoveryRequired:
	default:
		return ErrActivityCleanupPlanInvalid
	}
	for index, proof := range result.Proofs {
		if proof.Validate() != nil ||
			index >= len(result.Plan.Owners) ||
			!proof.Owner.Equal(result.Plan.Owners[index]) {
			return ErrActivityCleanupPlanInvalid
		}
	}
	for index, key := range result.RemainingOwnerKeys {
		if strings.TrimSpace(key) == "" ||
			index > 0 && key == result.RemainingOwnerKeys[index-1] {
			return ErrActivityCleanupPlanInvalid
		}
	}
	return nil
}

func cleanupOwnerMatches(
	owner workloadtypes.ActivityOwner,
	scope, environmentID, sessionID string,
) bool {
	switch scope {
	case ActivityCleanupScopeEnvironment:
		return owner.Kind == workloadtypes.OwnerReusableEnvironment &&
			owner.EnvironmentID == environmentID
	case ActivityCleanupScopeSession:
		return owner.Kind == workloadtypes.OwnerDisposableSession &&
			owner.SessionID == sessionID
	default:
		return false
	}
}

func activityCleanupPlanID(plan ActivityCleanupPlan) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("hideout.activity-cleanup-plan/v1\x00"))
	_, _ = hash.Write([]byte(plan.Operation))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(plan.Scope))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(plan.EnvironmentID))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(plan.SessionID))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(plan.PlannedAt.UTC().Format(time.RFC3339Nano)))
	for _, owner := range plan.Owners {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(owner.Key()))
	}
	return "op_" + base64.RawURLEncoding.EncodeToString(hash.Sum(nil)[:18])
}

func cloneActivityCleanupPlan(plan ActivityCleanupPlan) ActivityCleanupPlan {
	cloned := plan
	cloned.Owners = append([]workloadtypes.ActivityOwner(nil), plan.Owners...)
	return cloned
}

func (service *ActivityCleanupService) nowUTC() time.Time {
	if service == nil || service.now == nil {
		return time.Now().UTC()
	}
	value := service.now().UTC()
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value
}
