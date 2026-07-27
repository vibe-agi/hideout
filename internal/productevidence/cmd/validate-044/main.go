package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vibe-agi/hideout/internal/productevidence"
)

func main() {
	target := flag.String("target", "targeted-completion", "targeted-completion or release-candidate")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: validate-044 [--target targeted-completion|release-candidate] <manifest>")
		os.Exit(2)
	}
	path, err := filepath.Abs(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "validate-044:", err)
		os.Exit(1)
	}
	manifest, err := productevidence.ReadFile(path)
	if err == nil {
		switch *target {
		case "targeted-completion":
			err = productevidence.Require044TargetedComplete(manifest)
		case "release-candidate":
			err = requireReleaseCandidate(manifest, filepath.Dir(path))
		default:
			err = fmt.Errorf("unsupported target %q", *target)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "validate-044:", err)
		os.Exit(1)
	}
	fmt.Printf("validate-044: %s passed\n", *target)
}

func requireReleaseCandidate(manifest productevidence.Manifest, artifactRoot string) error {
	opts, err := releaseCandidateOptions(manifest, artifactRoot)
	if err != nil {
		return err
	}
	return productevidence.Require044ReleaseComplete(manifest, opts)
}

func releaseCandidateOptions(manifest productevidence.Manifest, artifactRoot string) (productevidence.EvaluationOptions, error) {
	if manifest.PackageIdentity == nil {
		return productevidence.EvaluationOptions{}, fmt.Errorf("manifest has no package identity")
	}
	var runtime *productevidence.RuntimeBinding
	for index := range manifest.Proofs {
		if manifest.Proofs[index].ProofID == productevidence.Proof044RealGate2FirstRun {
			runtime = manifest.Proofs[index].Runtime
			break
		}
	}
	if runtime == nil {
		return productevidence.EvaluationOptions{}, fmt.Errorf("manifest has no 044 Gate 2 runtime binding")
	}
	expectedRuntime := productevidence.RuntimeExpectation{
		Family:         runtime.Family,
		Revision:       runtime.Revision,
		ArtifactSHA256: runtime.ArtifactSHA256,
		HostOS:         runtime.HostOS,
		HostArch:       runtime.HostArch,
		GuestArch:      runtime.GuestArch,
		BuildCommit:    runtime.BuildCommit,
		RequireClean:   true,
	}
	return productevidence.EvaluationOptions{
		Requirements:    productevidence.RequirementsForFeature(productevidence.Feature044),
		Target:          productevidence.RequiredForReleaseCandidate,
		ExpectedCommit:  manifest.Commit,
		ExpectedPackage: manifest.PackageIdentity,
		ExpectedRuntime: &expectedRuntime,
		ArtifactRoot:    artifactRoot,
	}, nil
}
