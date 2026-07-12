package productevidence

func DocsTruthProof(proofID, summary string, claims []CoveredClaim) ProofEntry {
	return ProofEntry{
		ProofID:        proofID,
		FeatureID:      Feature025,
		Mode:           "docs",
		EvidenceClass:  "documentation-truth-gate",
		Status:         StatusPassed,
		CommandSummary: summary,
		CoveredClaims:  claims,
		Prerequisites: []PrerequisiteStatus{
			{Name: "docs-truth-smoke", Status: "available"},
		},
		RedactionStatus: RedactionPassed,
	}
}

func DocsTruthProofs() []ProofEntry {
	return []ProofEntry{
		DocsTruthProof(
			Proof025ClaimBoundaries,
			"validate claim-boundary registry and required 021-024 proof references",
			[]CoveredClaim{Claim025ClaimBoundaries},
		),
		DocsTruthProof(
			Proof025OverclaimScan,
			"scan current docs for known overclaim patterns",
			[]CoveredClaim{Claim025OverclaimScan},
		),
		DocsTruthProof(
			Proof025CommandExamples,
			"validate curated command examples and safety classifications",
			[]CoveredClaim{Claim025CommandExamples},
		),
		DocsTruthProof(
			Proof025CrossDoc,
			"validate README, localized README, test plan, STATUS, and Gate 0 consistency",
			[]CoveredClaim{Claim025CrossDoc},
		),
	}
}
