package overlay

const (
	ActionStage    = "host.fs.overlay.stage"
	ActionDeny     = "host.fs.overlay.deny"
	ActionPending  = "host.fs.overlay.pending"
	ActionClaim    = "host.fs.overlay.claim"
	ActionRelease  = "host.fs.overlay.claim-release"
	ActionExpiry   = "host.fs.overlay.claim-expired"
	ActionApply    = "host.fs.overlay.apply"
	ActionDiscard  = "host.fs.overlay.discard"
	ActionTimeout  = "host.fs.overlay.timeout"
	ActionConflict = "host.fs.overlay.conflict"
	ActionCleanup  = "host.fs.overlay.cleanup"
)

func StageDetails(op Operation) map[string]any {
	details := map[string]any{
		"operation":       op.Operation,
		"path":            op.RequestedPath,
		"ruleId":          op.GrantID,
		"source":          op.GrantSource,
		"operationId":     op.ID,
		"decisionId":      op.DecisionID,
		"hostChanged":     false,
		"privilegeStatus": op.Privilege.Status,
	}
	if op.DestinationPath != "" {
		details["destinationPath"] = op.DestinationPath
	}
	return details
}

func PendingDetails(decision Decision) map[string]any {
	details := map[string]any{
		"operation":       decision.Operation,
		"path":            decision.Path,
		"operationId":     decision.OperationID,
		"decisionId":      decision.DecisionID,
		"state":           decision.State,
		"hostChanged":     false,
		"privilegeStatus": decision.Privilege.Status,
	}
	if decision.DestinationPath != "" {
		details["destinationPath"] = decision.DestinationPath
	}
	if decision.Policy.GrantID != "" {
		details["ruleId"] = decision.Policy.GrantID
	}
	if decision.Policy.Source != "" {
		details["source"] = decision.Policy.Source
	}
	return details
}

func ClaimDetails(decision Decision, surface string) map[string]any {
	details := PendingDetails(decision)
	details["surface"] = surface
	return details
}

func ResultDetails(result Result, operation, path, destinationPath string) map[string]any {
	details := map[string]any{
		"operation":                operation,
		"path":                     path,
		"operationId":              result.OperationID,
		"decisionId":               result.DecisionID,
		"decision":                 result.Decision,
		"status":                   result.Status,
		"changedPaths":             result.ChangedPaths,
		"partialMutationPrevented": result.PartialMutationPrevented,
		"privilegeStatus":          result.Privilege.Status,
	}
	if destinationPath != "" {
		details["destinationPath"] = destinationPath
	}
	if result.ConflictReason != "" {
		details["conflictReason"] = result.ConflictReason
	}
	return details
}

func TimeoutDetails(decision Decision) map[string]any {
	details := map[string]any{
		"operationId":     decision.OperationID,
		"decisionId":      decision.DecisionID,
		"decision":        DecisionDeny,
		"reason":          "approval-timeout",
		"stagedDiscarded": true,
		"privilegeStatus": decision.Privilege.Status,
		"hostChanged":     false,
		"operation":       decision.Operation,
		"path":            decision.Path,
	}
	if decision.DestinationPath != "" {
		details["destinationPath"] = decision.DestinationPath
	}
	return details
}
