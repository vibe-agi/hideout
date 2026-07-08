package doctor

type RecoveryPlan struct {
	Mode             string   `json:"mode"`
	EligibleFindings []string `json:"eligibleFindings,omitempty"`
	RefusedFindings  []string `json:"refusedFindings,omitempty"`
	Result           string   `json:"result"`
}

func BuildRecoveryPlan(report Report, mode string) RecoveryPlan {
	plan := RecoveryPlan{Mode: mode, Result: "pending"}
	for _, finding := range report.Findings {
		if finding.Status == StatusError && finding.Required {
			plan.EligibleFindings = append(plan.EligibleFindings, finding.CheckID)
		}
	}
	if len(plan.EligibleFindings) == 0 {
		plan.Result = "refused"
	}
	return plan
}
