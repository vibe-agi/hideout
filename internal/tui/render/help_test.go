package render

import (
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/operatorhelp"
)

func TestHelpRendersContextKeysAndCatalogCommands(t *testing.T) {
	catalog := operatorhelp.Catalog{
		Schema: operatorhelp.CatalogSchema,
		Commands: []operatorhelp.Command{
			{
				ID: "activity", Name: "activity",
				TaskGroup:     "Observe",
				Purpose:       "Inspect workload evidence from the authoritative Manager.",
				Syntax:        []string{"hideout activity summary"},
				Examples:      []string{"hideout activity summary --session <id>"},
				Prerequisites: []string{"daemon"},
				Effects:       []string{"read-only"},
				Safety:        []string{"coverage-aware"},
				Recovery:      []string{"inspect coverage"},
				Next:          []string{"hideout tui"},
				Audience:      operatorhelp.AudienceNewUser,
				Stability:     operatorhelp.StabilityStable,
			},
			{
				ID: "secret", Name: "secret",
				TaskGroup:     "Configure",
				Purpose:       "Manage a secret.",
				Syntax:        []string{"hideout secret status <ref>"},
				Examples:      []string{"hideout secret status local-proxy"},
				Prerequisites: []string{"daemon"},
				Effects:       []string{"read-only"},
				Safety:        []string{"value hidden"},
				Recovery:      []string{"retry"},
				Next:          []string{"hideout tui"},
				Audience:      operatorhelp.AudienceOperator,
				Stability:     operatorhelp.StabilityStable,
			},
		},
	}

	got := Help(HelpInput{
		Catalog:    catalog,
		Context:    "Activity",
		CommandIDs: []string{"activity"},
	}, Options{Width: 80, Height: 24, NoColor: true})
	for _, want := range []string{
		"Hideout · Help · Activity",
		"/ filter",
		"r refresh",
		"hideout activity summary",
		"Inspect workload evidence from the authoritative Manager.",
		"hideout help activity",
		"Esc close",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("help missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Manage a secret") {
		t.Fatalf("contextual help rendered unrelated catalog entry:\n%s", got)
	}
}

func TestHelpSanitizesCatalogTextAndFitsWidth(t *testing.T) {
	catalog := operatorhelp.Catalog{
		Schema: operatorhelp.CatalogSchema,
		Commands: []operatorhelp.Command{{
			ID: "run", Name: "run", TaskGroup: "Run",
			Purpose:       "safe\x1b[2J\u202eevil",
			Syntax:        []string{"hideout run -- command"},
			Examples:      []string{"hideout run -- true"},
			Prerequisites: []string{"setup"},
			Effects:       []string{"execute"},
			Safety:        []string{"review"},
			Recovery:      []string{"stop"},
			Next:          []string{"hideout tui"},
			Audience:      operatorhelp.AudienceNewUser,
			Stability:     operatorhelp.StabilityStable,
		}},
	}
	got := Help(HelpInput{
		Catalog: catalog, Context: "Overview", CommandIDs: []string{"run"},
	}, Options{Width: 48, Height: 18, NoColor: true})
	if strings.Contains(got, "\x1b") || strings.Contains(got, "\u202e") {
		t.Fatalf("help retained terminal controls: %q", got)
	}
	for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
		if DisplayWidth(line) > 48 {
			t.Fatalf("line width=%d > 48: %q", DisplayWidth(line), line)
		}
	}
}
