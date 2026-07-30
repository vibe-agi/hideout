package uiweb_assets

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

func TestPresentationEscapesUntrustedControlTextAndBoundsCollections(
	t *testing.T,
) {
	runtime := goja.New()
	value, err := runtime.RunString(`
var window = {HideoutConsole:{}};
` + mustAsset("presentation.js") + `
const Presentation = window.HideoutConsole.Presentation;
const cyclic = {};
cyclic.self = cyclic;
const source = Array.from({length:205}, (_value,index) => index);
const defaultBound = Presentation.bounded(source);
const smallBound = Presentation.bounded(source, 2);
JSON.stringify({
  controls:Presentation.safeText("line\n\t\u001b\u202e\ud800"),
  limited:Presentation.safeText("abcdef", 3),
  markup:Presentation.safeText("<b>&</b>"),
  cyclic:Presentation.valueLabel(cyclic),
  array:Presentation.valueLabel(["one\n", "two\u202e"]),
  defaultBound,
  smallBound,
  sourceLength:source.length,
  limits:{
    rows:Presentation.DOM_ROW_LIMIT,
    dialog:Presentation.DIALOG_ROW_LIMIT
  }
});
`)
	if err != nil {
		t.Fatalf("run browser presentation helpers: %v", err)
	}
	var proof struct {
		Controls     string `json:"controls"`
		Limited      string `json:"limited"`
		Markup       string `json:"markup"`
		Cyclic       string `json:"cyclic"`
		Array        string `json:"array"`
		DefaultBound struct {
			Items   []int `json:"items"`
			Omitted int   `json:"omitted"`
			Total   int   `json:"total"`
		} `json:"defaultBound"`
		SmallBound struct {
			Items   []int `json:"items"`
			Omitted int   `json:"omitted"`
			Total   int   `json:"total"`
		} `json:"smallBound"`
		SourceLength int `json:"sourceLength"`
		Limits       struct {
			Rows   int `json:"rows"`
			Dialog int `json:"dialog"`
		} `json:"limits"`
	}
	if err := json.Unmarshal([]byte(value.String()), &proof); err != nil {
		t.Fatal(err)
	}
	if proof.Controls !=
		`line\n\t\u{001B}\u{202E}\u{D800}` {
		t.Fatalf("control text was not visibly escaped: %q", proof.Controls)
	}
	if proof.Limited != "abc… [truncated]" {
		t.Fatalf("bounded text=%q", proof.Limited)
	}
	if proof.Markup != "<b>&</b>" {
		t.Fatalf("text-only markup changed unexpectedly: %q", proof.Markup)
	}
	if proof.Cyclic != "[unrenderable value]" ||
		proof.Array != `one\n, two\u{202E}` {
		t.Fatalf(
			"structured values were not safely rendered: cyclic=%q array=%q",
			proof.Cyclic,
			proof.Array,
		)
	}
	if len(proof.DefaultBound.Items) != 200 ||
		proof.DefaultBound.Omitted != 5 ||
		proof.DefaultBound.Total != 205 ||
		len(proof.SmallBound.Items) != 2 ||
		proof.SmallBound.Omitted != 203 ||
		proof.SmallBound.Total != 205 ||
		proof.SourceLength != 205 ||
		proof.Limits.Rows != 200 ||
		proof.Limits.Dialog != 100 {
		t.Fatalf("collection bounds are not deterministic: %+v", proof)
	}
}

func TestBrowserConsoleHasKeyboardAccessibleBoundedPresentation(t *testing.T) {
	html := mustAsset("index.html")
	app := mustAsset("app.js")
	style := mustAsset("style.css")

	for _, marker := range []string{
		`class="skip-link" href="#console-main"`,
		`id="consoleAnnouncement"`,
		`role="tablist"`,
		`aria-describedby="tabInstructions"`,
		`role="tabpanel"`,
		`class="panel-body history-body" tabindex="0"`,
		`id="dialogTitle" tabindex="-1"`,
		`id="dialogClose" type="button"`,
	} {
		if !strings.Contains(html, marker) {
			t.Fatalf("browser console shell missing accessibility marker %q", marker)
		}
	}
	if got := strings.Count(html, `role="tab"`); got != 10 {
		t.Fatalf("tab count=%d want=10", got)
	}
	if got := strings.Count(html, `role="tabpanel"`); got != 10 {
		t.Fatalf("tabpanel count=%d want=10", got)
	}
	for _, marker := range []string{
		`case "ArrowRight":`,
		`case "ArrowLeft":`,
		`case "Home":`,
		`case "End":`,
		`candidate.setAttribute("aria-selected", String(active))`,
		`dialogReturnFocus.focus()`,
		`target.setAttribute("aria-busy", String(detailLoading))`,
		`presentation.bounded`,
		`DOM_ROW_LIMIT`,
	} {
		if !strings.Contains(app, marker) {
			t.Fatalf("browser console behavior missing %q", marker)
		}
	}
	for _, marker := range []string{
		".sr-only",
		".skip-link:focus",
		".history-body",
		"max-height: min(68vh, 860px)",
		"@media (max-width: 620px)",
		"@media (prefers-reduced-motion: reduce)",
	} {
		if !strings.Contains(style, marker) {
			t.Fatalf("browser console responsive style missing %q", marker)
		}
	}
	for _, name := range []string{
		"state.js",
		"client.js",
		"activity.js",
		"config.js",
		"presentation.js",
		"app.js",
	} {
		source := mustAsset(name)
		for _, forbidden := range []string{
			".innerHTML",
			".outerHTML",
			"insertAdjacentHTML",
			"document.write",
		} {
			if strings.Contains(source, forbidden) {
				t.Fatalf("%s contains HTML parser sink %q", name, forbidden)
			}
		}
	}
}
