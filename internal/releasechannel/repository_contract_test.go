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

func TestReleaseWorkflowPushTriggersMatchReleaseState(t *testing.T) {
	promotion := readWorkflowContract(t, "hideout-alpha-promote.yml")
	assertStringList(t, promotion.On.Push.Paths, []string{
		".github/release-promotions/public-alpha-*.json",
	})

	publicTruth := readWorkflowContract(t, "hideout-alpha-public-truth.yml")
	assertStringList(t, publicTruth.On.Push.Paths, []string{
		"releases/current.json",
		"releases/receipts/**",
		"README.md",
		"README.zh-CN.md",
		"docs/STATUS.md",
		"docs/support-matrix.md",
		"CHANGELOG.md",
		"scripts/test-doc-truth-smoke.sh",
	})
}

func TestPublicTruthWorkflowSkipsCandidateNeutralRepositoryState(t *testing.T) {
	workflow := readWorkflowContract(t, "hideout-alpha-public-truth.yml")
	job, ok := workflow.Jobs["public-truth"]
	if !ok {
		t.Fatal("public truth workflow must define the public-truth job")
	}

	foundState := false
	guardedSteps := 0
	for _, step := range job.Steps {
		if step.ID == "release_state" {
			foundState = strings.Contains(step.Run, ".current != null") &&
				strings.Contains(step.Run, "published=false") &&
				strings.Contains(step.Run, "$GITHUB_OUTPUT")
		}
		if step.Name == "Install the declared Go toolchain" ||
			step.Name == "Validate inventory, receipt, and generated docs" ||
			step.Name == "Retain bounded post-public proof" {
			guardedSteps++
			if step.If != "steps.release_state.outputs.published == 'true'" {
				t.Errorf("step %q must be gated on a checked-in public release", step.Name)
			}
		}
	}
	if !foundState {
		t.Fatal("public truth workflow must distinguish current:null from a published release")
	}
	if guardedSteps != 3 {
		t.Fatalf("expected 3 receipt-bound public truth steps, got %d", guardedSteps)
	}
}

type workflowContract struct {
	On struct {
		Push struct {
			Paths []string `yaml:"paths"`
		} `yaml:"push"`
	} `yaml:"on"`
	Jobs map[string]struct {
		Steps []struct {
			Name string `yaml:"name"`
			ID   string `yaml:"id"`
			If   string `yaml:"if"`
			Run  string `yaml:"run"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func readWorkflowContract(t *testing.T, name string) workflowContract {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name))
	if err != nil {
		t.Fatal(err)
	}
	var workflow workflowContract
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	return workflow
}

func assertStringList(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected workflow paths:\n got: %q\nwant: %q", got, want)
	}
}
