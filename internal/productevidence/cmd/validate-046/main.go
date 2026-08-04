package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vibe-agi/hideout/internal/productevidence"
)

func main() {
	commit := flag.String("commit", "", "expected full source commit")
	packagePath := flag.String("package-identity", "", "trusted package identity JSON")
	flag.Parse()
	if flag.NArg() != 1 || !productevidence.IsCanonicalCommit(strings.TrimSpace(*commit)) || strings.TrimSpace(*packagePath) == "" {
		fmt.Fprintln(os.Stderr, "usage: validate-046 --commit <full-commit> --package-identity <json> <manifest>")
		os.Exit(2)
	}
	manifestPath, err := filepath.Abs(flag.Arg(0))
	if err != nil {
		fail(err)
	}
	manifest, err := productevidence.ReadFile(manifestPath)
	if err != nil {
		fail(err)
	}
	packageIdentity, err := readPackageIdentity(*packagePath, strings.TrimSpace(*commit))
	if err != nil {
		fail(err)
	}
	err = productevidence.Require046ReleaseComplete(manifest, productevidence.EvaluationOptions{
		ExpectedCommit:  strings.TrimSpace(*commit),
		ExpectedPackage: packageIdentity,
		ArtifactRoot:    filepath.Dir(manifestPath),
	})
	if err != nil {
		fail(err)
	}
	fmt.Println("validate-046: release-candidate passed")
}

func readPackageIdentity(path, commit string) (*productevidence.PackageIdentity, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var identity productevidence.PackageIdentity
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&identity); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil || !errors.Is(err, io.EOF) {
		return nil, errors.New("package identity contains trailing JSON")
	}
	if err := identity.ValidateCandidateCommit(commit); err != nil {
		return nil, err
	}
	return &identity, nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "validate-046:", err)
	os.Exit(1)
}
