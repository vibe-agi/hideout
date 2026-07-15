package releasecompat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type DriftFinding struct {
	ID      string
	Path    string
	Status  string
	Summary string
}

func CheckRepositoryDocs(root string) []DriftFinding {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	var findings []DriftFinding
	findings = append(findings,
		requireText(root, "README.md", "macOS", "README mentions macOS install/support context"),
		requireText(root, "README.md", "hideout support matrix", "README mentions support matrix command"),
		requireText(root, "README.md", "--backend native` is a development harness, not an isolation boundary", "README preserves native non-claim"),
		requireText(root, "docs/STATUS.md", "Release hardening and compatibility matrix", "STATUS includes 016 status"),
		requireText(root, "docs/backend-capability-matrix.md", "HostFS overlay | required", "backend matrix marks HostFS overlay current for Lima"),
		requireText(root, "docs/threat-model.md", "guest root", "threat model preserves guest-root non-claim"),
		requireText(root, "docs/privacy-run-test-plan.md", "test-release-readiness.sh", "test plan includes release readiness"),
		requireText(root, "docs/support-matrix.md", MatrixVersion, "support matrix docs include current matrix version"),
	)
	for _, id := range RequiredNonClaimIDs() {
		findings = append(findings, requireText(root, "docs/support-matrix.md", id, "support matrix docs include required non-claim "+id))
	}
	return findings
}

func requireText(root, rel, needle, summary string) DriftFinding {
	path := filepath.Join(root, rel)
	data, err := os.ReadFile(path)
	if err != nil {
		return DriftFinding{ID: stableFindingID(rel, needle), Path: rel, Status: "error", Summary: err.Error()}
	}
	if !strings.Contains(string(data), needle) {
		return DriftFinding{ID: stableFindingID(rel, needle), Path: rel, Status: "error", Summary: fmt.Sprintf("missing %q: %s", needle, summary)}
	}
	return DriftFinding{ID: stableFindingID(rel, needle), Path: rel, Status: "pass", Summary: summary}
}

func stableFindingID(path, needle string) string {
	value := strings.ToLower(path + "-" + needle)
	replacer := strings.NewReplacer("/", "-", ".", "-", " ", "-", "`", "", "_", "-", "|", "-", "'", "")
	value = replacer.Replace(value)
	for strings.Contains(value, "--") {
		value = strings.ReplaceAll(value, "--", "-")
	}
	return strings.Trim(value, "-")
}

func DriftHasErrors(findings []DriftFinding) bool {
	for _, finding := range findings {
		if finding.Status == "error" {
			return true
		}
	}
	return false
}
