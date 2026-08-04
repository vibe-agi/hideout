package productevidence

import (
	"fmt"
	"sort"
)

var Required021ProofIDs = RequiredProofIDsForFeature(Feature021)

var Required022LocalFastProofIDs = RequiredProofIDsForFeature(Feature022)

var Required023LocalFastProofIDs = RequiredProofIDsForFeature(Feature023)

var Required024LocalFastProofIDs = RequiredProofIDsForFeature(Feature024)

var Required025ProofIDs = RequiredProofIDsForFeature(Feature025)

var Required029ProofIDs = RequiredProofIDsForFeature(Feature029)

var Required030ProofIDs = RequiredProofIDsForFeature(Feature030)

var Required031ProofIDs = RequiredProofIDsForFeature(Feature031)

var Required032ProofIDs = RequiredProofIDsForFeature(Feature032)

var Required033ProofIDs = RequiredProofIDsForFeature(Feature033)

var Required034ProofIDs = RequiredProofIDsForFeature(Feature034)

var Required035ProofIDs = RequiredProofIDsForFeature(Feature035)

var Required036ProofIDs = RequiredProofIDsForFeature(Feature036)

var Required038ProofIDs = RequiredProofIDsForFeature(Feature038)

var Required039ProofIDs = RequiredProofIDsForFeature(Feature039)

var Required040ProofIDs = RequiredProofIDsForFeature(Feature040)

var Required041ProofIDs = RequiredProofIDsForFeature(Feature041)

var Required042ProofIDs = RequiredProofIDsForFeature(Feature042)

var Required043ProofIDs = RequiredProofIDsForFeature(Feature043)

var Required044ProofIDs = RequiredProofIDsForFeature(Feature044)

var Required045ProofIDs = RequiredProofIDsForFeature(Feature045)

var Required046ProofIDs = RequiredProofIDsForFeature(Feature046)

type Aggregate struct {
	Proofs       map[string]ProofEntry
	Claims       map[string][]ProofEntry
	StatusCounts map[string]int
}

func AggregateManifests(manifests ...Manifest) (Aggregate, error) {
	agg := Aggregate{
		Proofs:       map[string]ProofEntry{},
		Claims:       map[string][]ProofEntry{},
		StatusCounts: map[string]int{},
	}
	for i, manifest := range manifests {
		if err := manifest.Validate(); err != nil {
			return Aggregate{}, fmt.Errorf("manifest[%d]: %w", i, err)
		}
		for _, proof := range manifest.Proofs {
			if _, exists := agg.Proofs[proof.ProofID]; exists {
				return Aggregate{}, fmt.Errorf("duplicate proofId %q", proof.ProofID)
			}
			agg.Proofs[proof.ProofID] = proof
			agg.StatusCounts[proof.Status]++
			for _, claim := range proof.CoveredClaims {
				agg.Claims[claim.ClaimID] = append(agg.Claims[claim.ClaimID], proof)
			}
		}
	}
	return agg, nil
}

func (a Aggregate) RequirePassed(proofIDs ...string) error {
	var missing, failed []string
	for _, proofID := range proofIDs {
		proof, ok := a.Proofs[proofID]
		if !ok {
			missing = append(missing, proofID)
			continue
		}
		if proof.Status != StatusPassed || proof.RedactionStatus != RedactionPassed {
			failed = append(failed, fmt.Sprintf("%s(status=%s,redaction=%s)", proofID, proof.Status, proof.RedactionStatus))
		}
	}
	sort.Strings(missing)
	sort.Strings(failed)
	switch {
	case len(missing) > 0 && len(failed) > 0:
		return fmt.Errorf("missing proof ids %v; non-passing proof ids %v", missing, failed)
	case len(missing) > 0:
		return fmt.Errorf("missing proof ids %v", missing)
	case len(failed) > 0:
		return fmt.Errorf("non-passing proof ids %v", failed)
	default:
		return nil
	}
}

func (a Aggregate) RequireClaimCovered(claimIDs ...string) error {
	var missing []string
	for _, claimID := range claimIDs {
		if !claimHasPassingProof(a.Claims[claimID]) {
			missing = append(missing, claimID)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("claims without passing proof %v", missing)
	}
	return nil
}

func claimHasPassingProof(proofs []ProofEntry) bool {
	for _, proof := range proofs {
		if proof.Status == StatusPassed && proof.RedactionStatus == RedactionPassed {
			return true
		}
	}
	return false
}

func Require021Complete(manifest Manifest) error {
	return requireFeatureComplete(manifest, Feature021, RequiredForTargetedCompletion)
}

func Require022LocalFastComplete(manifest Manifest) error {
	return requireFeatureComplete(manifest, Feature022, RequiredForLocalDogfood)
}

func Require023LocalFastComplete(manifest Manifest) error {
	report, err := EvaluateManifest(manifest, EvaluationOptions{
		Requirements: RequirementsForFeature(Feature023),
		Target:       RequiredForLocalDogfood,
	})
	if err != nil {
		return err
	}
	if err := report.RequireSatisfied(); err != nil {
		return err
	}
	agg, err := AggregateManifests(manifest)
	if err != nil {
		return err
	}
	if proof, ok := agg.Proofs[Proof023RealGate2Lifecycle]; ok && proof.Status == StatusPassed {
		return fmt.Errorf("023 local-fast completion evidence must not include passing real Gate 2 proof")
	}
	return nil
}

func Require024LocalFastComplete(manifest Manifest) error {
	return requireFeatureComplete(manifest, Feature024, RequiredForLocalDogfood)
}

func Require025Complete(manifest Manifest) error {
	return requireFeatureComplete(manifest, Feature025, RequiredForLocalDogfood)
}

func Require042Complete(manifest Manifest) error {
	return requireFeatureComplete(manifest, Feature042, RequiredForTargetedCompletion)
}

func Require044TargetedComplete(manifest Manifest) error {
	return requireFeatureComplete(manifest, Feature044, RequiredForTargetedCompletion)
}

func Require044ReleaseComplete(manifest Manifest, opts EvaluationOptions) error {
	opts.Requirements = RequirementsForFeature(Feature044)
	opts.Target = RequiredForReleaseCandidate
	report, err := EvaluateManifest(manifest, opts)
	if err != nil {
		return err
	}
	return report.RequireSatisfied()
}

func Require044PublicComplete(manifest Manifest, opts EvaluationOptions) error {
	opts.Requirements = RequirementsForFeature(Feature044)
	opts.Target = RequiredForPublicRelease
	report, err := EvaluateManifest(manifest, opts)
	if err != nil {
		return err
	}
	return report.RequireSatisfied()
}

func Require045ReleaseComplete(manifest Manifest, opts EvaluationOptions) error {
	opts.Requirements = RequirementsForFeature(Feature045)
	opts.Target = RequiredForReleaseCandidate
	report, err := EvaluateManifest(manifest, opts)
	if err != nil {
		return err
	}
	return report.RequireSatisfied()
}

func Require046ReleaseComplete(manifest Manifest, opts EvaluationOptions) error {
	opts.Requirements = RequirementsForFeature(Feature046)
	opts.Target = RequiredForReleaseCandidate
	report, err := EvaluateManifest(manifest, opts)
	if err != nil {
		return err
	}
	return report.RequireSatisfied()
}

func requireFeatureComplete(manifest Manifest, featureID, target string) error {
	report, err := EvaluateManifest(manifest, EvaluationOptions{
		Requirements: RequirementsForFeature(featureID),
		Target:       target,
	})
	if err != nil {
		return err
	}
	return report.RequireSatisfied()
}
