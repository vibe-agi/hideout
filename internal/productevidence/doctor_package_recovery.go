package productevidence

func DoctorPackageRecoveryProof(proofID, summary string, claims []CoveredClaim) ProofEntry {
	return ProofEntry{
		ProofID:        proofID,
		FeatureID:      Feature024,
		Mode:           "local-fast",
		EvidenceClass:  "doctor-package-recovery-e2e",
		Status:         StatusPassed,
		CommandSummary: summary,
		CoveredClaims:  claims,
		Prerequisites: []PrerequisiteStatus{
			{Name: "doctor-package-recovery-e2e", Status: "available"},
		},
		RedactionStatus: RedactionPassed,
	}
}

func DoctorPackageRecoveryLocalFastProofs() []ProofEntry {
	return []ProofEntry{
		DoctorPackageRecoveryProof(
			Proof024PackageRepairLoop,
			"prove package stale verify, repair dry-run, repair apply, and verify clean",
			[]CoveredClaim{Claim024PackageRepair, Claim024PackagePreservation, Claim024LocalOnly},
		),
		DoctorPackageRecoveryProof(
			Proof024DoctorSafeFixLoop,
			"prove doctor deep, safe fix dry-run, explicit fix apply, and rerun",
			[]CoveredClaim{Claim024DoctorSafeFix, Claim024LocalOnly},
		),
		DoctorPackageRecoveryProof(
			Proof024DoctorGuidance,
			"prove non-auto-fix doctor findings remain guidance with next actions",
			[]CoveredClaim{Claim024DoctorGuidance, Claim024LocalOnly},
		),
		DoctorPackageRecoveryProof(
			Proof024Redaction,
			"prove recovery logs, doctor export, and product evidence are redacted",
			[]CoveredClaim{Claim024Redaction, Claim024DoctorExport},
		),
	}
}
