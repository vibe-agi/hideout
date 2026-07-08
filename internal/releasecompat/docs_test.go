package releasecompat

import (
	"path/filepath"
	"testing"
)

func TestRepositoryDocsMatchMatrix(t *testing.T) {
	root := filepath.Join("..", "..")
	findings := CheckRepositoryDocs(root)
	for _, finding := range findings {
		if finding.Status == "error" {
			t.Fatalf("%s: %s", finding.Path, finding.Summary)
		}
	}
}

func TestDocsDriftFindsMissingText(t *testing.T) {
	root := t.TempDir()
	findings := CheckRepositoryDocs(root)
	if !DriftHasErrors(findings) {
		t.Fatal("empty docs root unexpectedly passed drift check")
	}
}
