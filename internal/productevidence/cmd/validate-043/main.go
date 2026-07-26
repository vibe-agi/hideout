package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vibe-agi/hideout/internal/productevidence"
)

func main() {
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: validate-043 <product-hardening-evidence.json>")
		os.Exit(2)
	}
	path, err := filepath.Abs(flag.Arg(0))
	if err != nil {
		fail(err)
	}
	manifest, err := productevidence.ReadFile(path)
	if err != nil {
		fail(err)
	}
	if manifest.PackageIdentity == nil {
		fail(fmt.Errorf("manifest has no package identity"))
	}
	var runtime *productevidence.RuntimeBinding
	for index := range manifest.Proofs {
		if manifest.Proofs[index].ProofID == productevidence.Proof043RealReadiness {
			runtime = manifest.Proofs[index].Runtime
			break
		}
	}
	if runtime == nil {
		fail(fmt.Errorf("manifest has no 043 readiness runtime binding"))
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
	requirements := productevidence.RequirementsForFeatures(
		productevidence.Feature030,
		productevidence.Feature032,
		productevidence.Feature039,
		productevidence.Feature043,
	)
	report, err := productevidence.EvaluateManifest(manifest, productevidence.EvaluationOptions{
		Requirements:    requirements,
		Target:          productevidence.RequiredForReleaseCandidate,
		ExpectedCommit:  manifest.Commit,
		ExpectedPackage: manifest.PackageIdentity,
		ExpectedRuntime: &expectedRuntime,
		ArtifactRoot:    filepath.Dir(path),
	})
	if err == nil {
		err = report.RequireSatisfied()
	}
	if err != nil {
		fail(err)
	}
	fmt.Println("validate-043: release-candidate projection and privacy proofs passed")
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "validate-043:", err)
	os.Exit(1)
}
