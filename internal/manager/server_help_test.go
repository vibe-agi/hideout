package manager

import (
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/operatorhelp"
)

func TestBrowserConsoleRendersSearchableCatalogAndCanonicalCLI(t *testing.T) {
	catalog := operatorhelp.Catalog{
		Schema: operatorhelp.CatalogSchema,
		Commands: []operatorhelp.Command{{
			ID: "run", Name: "run", TaskGroup: "Run safely",
			Purpose:       "CATALOG SENTINEL run one command.",
			Syntax:        []string{"hideout run -- <command>"},
			Examples:      []string{"hideout run -- git status"},
			Prerequisites: []string{"setup"},
			Effects:       []string{"execute"},
			Safety:        []string{"workspace writable"},
			Recovery:      []string{"stop"},
			Next:          []string{"hideout activity summary"},
			Audience:      operatorhelp.AudienceNewUser,
			Stability:     operatorhelp.StabilityStable,
		}},
	}
	html := renderUIHTMLWithCatalog(
		time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		catalog,
	)
	for _, want := range []string{
		`data-panel="help">Help</button>`,
		`id="helpSearch"`,
		`aria-label="Search commands"`,
		`const helpCatalog = `,
		`CATALOG SENTINEL run one command.`,
		`hideout run -- \u003ccommand\u003e`,
		`function renderHelp`,
		`function canonicalCLIForPanel`,
		`hideout help `,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("browser help missing %q", want)
		}
	}
}

func TestBrowserCatalogJSONCannotBreakScriptContext(t *testing.T) {
	catalog := operatorhelp.Catalog{
		Schema: operatorhelp.CatalogSchema,
		Commands: []operatorhelp.Command{{
			ID: "run", Name: "run", TaskGroup: "Run safely",
			Purpose:       `</script><script>alert("x")</script>`,
			Syntax:        []string{"hideout run -- true"},
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
	html := renderUIHTMLWithCatalog(time.Now(), catalog)
	if strings.Contains(html, `</script><script>alert("x")</script>`) {
		t.Fatal("catalog text escaped its JSON script context")
	}
	if !strings.Contains(html, `\u003c/script\u003e`) {
		t.Fatal("catalog text was not HTML-escaped in JSON")
	}
}
