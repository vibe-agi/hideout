package productevidence

func HostFSDecisionProof(proofID, mode, summary string, claims []CoveredClaim) ProofEntry {
	return ProofEntry{
		ProofID:        proofID,
		FeatureID:      Feature023,
		Mode:           mode,
		EvidenceClass:  "hostfs-decision-e2e",
		Status:         StatusPassed,
		CommandSummary: summary,
		CoveredClaims:  claims,
		Prerequisites: []PrerequisiteStatus{
			{Name: "hostfs-decision-e2e", Status: "available"},
		},
		RedactionStatus: RedactionPassed,
	}
}

func HostFSDecisionLocalFastProofs() []ProofEntry {
	return []ProofEntry{
		HostFSDecisionProof(
			Proof023LocalFastLifecycle,
			"local-fast",
			"prove local HostFS decision lifecycle and coverage matrix",
			[]CoveredClaim{
				Claim023Lifecycle,
				Claim023Conflict,
				Claim023RealBoundary,
				Claim023Coverage,
			},
		),
		HostFSDecisionProof(
			Proof023LocalFastClaimRace,
			"local-fast",
			"prove exactly one decision claimant wins",
			[]CoveredClaim{Claim023ClaimRace},
		),
		HostFSDecisionProof(
			Proof023LocalFastTimeout,
			"local-fast",
			"prove timeout/default-deny decision outcome",
			[]CoveredClaim{Claim023DecisionOutcomes},
		),
		HostFSDecisionProof(
			Proof023LocalFastVisibility,
			"local-fast",
			"prove CLI/API/WebUI-model/TUI-model decision visibility",
			[]CoveredClaim{Claim023Visibility},
		),
		HostFSDecisionProof(
			Proof023LocalFastRedaction,
			"local-fast",
			"prove public HostFS and decision artifacts are redacted",
			[]CoveredClaim{Claim023Redaction},
		),
	}
}
