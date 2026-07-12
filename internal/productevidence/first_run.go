package productevidence

func FirstRunProof(proofID, mode, summary string, claims []CoveredClaim) ProofEntry {
	return ProofEntry{
		ProofID:        proofID,
		FeatureID:      Feature022,
		Mode:           mode,
		EvidenceClass:  "first-run-e2e",
		Status:         StatusPassed,
		CommandSummary: summary,
		CoveredClaims:  claims,
		Prerequisites: []PrerequisiteStatus{
			{Name: "first-run-e2e", Status: "available"},
		},
		RedactionStatus: RedactionPassed,
	}
}

func FirstRunLocalFastProofs() []ProofEntry {
	return []ProofEntry{
		FirstRunProof(
			Proof022LocalFastInstall,
			"local-fast",
			"install package with --skip-init",
			[]CoveredClaim{Claim022LocalFast},
		),
		FirstRunProof(
			Proof022LocalFastVerify,
			"local-fast",
			"verify installed package before success",
			[]CoveredClaim{Claim022LocalFast},
		),
		FirstRunProof(
			Proof022LocalFastInit,
			"local-fast",
			"initialize one weak/dev first-run profile",
			[]CoveredClaim{Claim022LocalFast, Claim022SingleInit},
		),
		FirstRunProof(
			Proof022LocalFastRun,
			"local-fast",
			"run one installed-binary command",
			[]CoveredClaim{Claim022LocalFast},
		),
		FirstRunProof(
			Proof022LocalFastAuditBoundary,
			"local-fast",
			"capture audit and Boundary evidence",
			[]CoveredClaim{Claim022AuditBoundary},
		),
		FirstRunProof(
			Proof022DocsOrder,
			"docs",
			"docs use --skip-init before explicit init",
			[]CoveredClaim{Claim022DocsOrder},
		),
		FirstRunProof(
			Proof022FailureFixtures,
			"local-fast",
			"representative failure fixtures reject pass claims",
			[]CoveredClaim{Claim022FailureFixtures},
		),
	}
}
