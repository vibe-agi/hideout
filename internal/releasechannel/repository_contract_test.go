package releasechannel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPublicBugFormRequiresSafeDiagnosticIdentity(t *testing.T) {
	type formItem struct {
		Type       string `yaml:"type"`
		ID         string `yaml:"id"`
		Attributes struct {
			Value string `yaml:"value"`
		} `yaml:"attributes"`
		Validations struct {
			Required bool `yaml:"required"`
		} `yaml:"validations"`
	}
	var form struct {
		Body []formItem `yaml:"body"`
	}
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "ISSUE_TEMPLATE", "bug.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, &form); err != nil {
		t.Fatal(err)
	}

	required := map[string]bool{
		"version": false, "package_digest": false, "platform": false,
		"recovery_code": false, "behavior": false, "reproduction": false,
	}
	seen := map[string]bool{}
	var notices string
	for _, item := range form.Body {
		if item.Type == "markdown" {
			notices += " " + strings.ToLower(item.Attributes.Value)
		}
		if _, ok := required[item.ID]; ok {
			required[item.ID] = item.Validations.Required
		}
		seen[item.ID] = true
	}
	if !seen["doctor"] {
		t.Error("bug form must request a bounded doctor summary")
	}
	for id, ok := range required {
		if !ok {
			t.Errorf("bug form field %q must exist and be required", id)
		}
	}
	if !strings.Contains(notices, "do not report vulnerabilities") ||
		!strings.Contains(notices, "secrets") || !strings.Contains(notices, "security.md") {
		t.Fatal("bug form must direct vulnerabilities and secrets to SECURITY.md")
	}
}

func TestIssueConfigurationUsesPrivateVulnerabilityReporting(t *testing.T) {
	var config struct {
		BlankIssuesEnabled bool `yaml:"blank_issues_enabled"`
		ContactLinks       []struct {
			Name string `yaml:"name"`
			URL  string `yaml:"url"`
		} `yaml:"contact_links"`
	}
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "ISSUE_TEMPLATE", "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.BlankIssuesEnabled {
		t.Fatal("blank public issues must remain disabled")
	}
	const privateURL = "https://github.com/vibe-agi/hideout/security/advisories/new"
	for _, link := range config.ContactLinks {
		if link.URL == privateURL {
			return
		}
	}
	t.Fatalf("issue configuration must link to %s", privateURL)
}
