package manager

import (
	"errors"
	"net/http"
	"os"
)

func (api API) serveProfileTransactionPlan(
	w http.ResponseWriter,
	r *http.Request,
) {
	var draft ConfigurationDraft
	if err := decodeStrictJSON(
		w,
		r,
		&draft,
		"invalid configuration draft",
	); err != nil {
		writeConfigurationTransactionError(w, err, true)
		return
	}
	plan, err := api.profileTransactionService().Plan(r.Context(), draft)
	if err != nil {
		writeConfigurationTransactionError(w, err, true)
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "profile/transaction/plan",
		Data:     plan,
		Errors:   []string{},
	})
}

func (api API) serveProfileTransactionApply(
	w http.ResponseWriter,
	r *http.Request,
) {
	var request ConfigurationApplyRequest
	if err := decodeStrictJSON(
		w,
		r,
		&request,
		"invalid configuration apply request",
	); err != nil {
		writeConfigurationTransactionError(w, err, false)
		return
	}
	result, err := api.profileTransactionService().Apply(
		r.Context(),
		request,
	)
	if err != nil {
		if errors.Is(err, ErrConfigurationRecoveryRequired) &&
			result.Operation.ID != "" {
			writeAPIJSON(w, http.StatusAccepted, APIResponse{
				Version:  APIVersion,
				Resource: "profile/transaction/apply",
				Data:     publicConfigurationApplyResult(result),
				Errors:   []string{err.Error()},
			})
			return
		}
		writeConfigurationTransactionError(w, err, false)
		return
	}
	status := http.StatusOK
	if !result.Operation.Terminal() {
		status = http.StatusAccepted
	}
	writeAPIJSON(w, status, APIResponse{
		Version:  APIVersion,
		Resource: "profile/transaction/apply",
		Data:     publicConfigurationApplyResult(result),
		Errors:   []string{},
	})
}

func (api API) profileTransactionService() *ProfileTransactionService {
	if api.ProfileTransactions != nil {
		return api.ProfileTransactions
	}
	service := NewProfileTransactionService(api.Core)
	service.Mutations = api.LifecycleMutations
	if api.Now != nil {
		service.now = api.Now
	}
	return service
}

func (api API) legacyProfileTransactions() LegacyProfileTransactionAdapter {
	return LegacyProfileTransactionAdapter{
		Core:         api.Core,
		Now:          api.Now,
		Mutations:    api.LifecycleMutations,
		Transactions: api.profileTransactionService(),
	}
}

func publicConfigurationApplyResult(
	result ConfigurationApplyResult,
) ConfigurationApplyResult {
	public := result
	public.Projection.Desired.Env.Public = make(
		map[string]string,
		len(result.Projection.Desired.Env.Public),
	)
	for name := range result.Projection.Desired.Env.Public {
		public.Projection.Desired.Env.Public[name] = "[value provided]"
	}
	return public
}

func addedProfileHostFSRule(
	plan ProfileHostFSPlan,
	deny bool,
) *ProfileHostFSRuleSummary {
	before := plan.GrantsBefore
	after := plan.GrantsAfter
	if deny {
		before = plan.DenyBefore
		after = plan.DenyAfter
	}
	known := make(map[string]struct{}, len(before))
	for _, rule := range before {
		known[rule.ID] = struct{}{}
	}
	for _, rule := range after {
		if _, exists := known[rule.ID]; exists {
			continue
		}
		added := rule
		return &added
	}
	return nil
}

func writeConfigurationTransactionError(
	w http.ResponseWriter,
	err error,
	planning bool,
) {
	status := http.StatusInternalServerError
	detail := APIErrorDetail{
		Code:     "configuration-failed",
		Message:  "configuration transaction failed",
		Recovery: "inspect daemon status and retry",
	}
	switch {
	case errors.Is(err, ErrInvalidConfigurationDraft),
		errors.Is(err, ErrUnknownTypedChange),
		errors.Is(err, ErrConfigurationNoChange):
		status = http.StatusBadRequest
		detail.Code = "invalid-draft"
		detail.Message = err.Error()
		detail.Recovery = "refresh the profile and submit a supported change with a real state difference"
	case errors.Is(err, ErrInvalidConfigurationPlan),
		errors.Is(err, ErrConfigurationConfirmationRequired),
		errors.Is(err, ErrConfigurationPlanExpired):
		status = http.StatusBadRequest
		detail.Code = "invalid-plan"
		detail.Message = err.Error()
		detail.Recovery = "request and confirm a fresh configuration plan"
	case errors.Is(err, os.ErrNotExist):
		status = http.StatusNotFound
		detail.Code = "profile-not-found"
		if !planning {
			detail.Code = "operation-not-found"
		}
		detail.Message = err.Error()
		detail.Recovery = "refresh profiles and operations before retrying"
	case errors.Is(err, ErrStaleConfigurationPlan),
		errors.Is(err, ErrStaleProfileRevision):
		status = http.StatusConflict
		detail.Code = "stale-plan"
		if planning {
			detail.Code = "stale-draft"
		}
		detail.Message = err.Error()
		detail.Recovery = "refresh the profile projection and review a new plan"
	case errors.Is(err, ErrOperationMismatch):
		status = http.StatusConflict
		detail.Code = "operation-mismatch"
		detail.Message = err.Error()
		detail.Recovery = "retry with the exact operation ID, profile, revision, and plan digest"
	case errors.Is(err, ErrConfigurationMutationConflict):
		status = http.StatusConflict
		detail.Code = "mutation-conflict"
		detail.Message = err.Error()
		detail.Recovery = "inspect the owning operation and retry after it reaches a terminal phase"
		var conflict *ConfigurationMutationConflictError
		if errors.As(err, &conflict) && conflict.Recovery != "" {
			detail.Recovery = conflict.Recovery
		}
	case errors.Is(err, ErrConfigurationBlocked):
		status = http.StatusConflict
		detail.Code = "plan-blocked"
		detail.Message = err.Error()
		detail.Recovery = "resolve the reviewed blockers and request a new plan"
	case errors.Is(err, ErrConfigurationProviderUnavailable):
		status = http.StatusUnprocessableEntity
		detail.Code = "unsupported-capability"
		detail.Message = err.Error()
		detail.Recovery = "update Hideout or use a provider supported by this daemon"
	default:
		if err != nil {
			detail.Message = err.Error()
		}
	}
	writeAPIDetailedError(w, status, detail)
}
