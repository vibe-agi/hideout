package profiletemplate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/profile"
)

func TestCatalogInvariants(t *testing.T) {
	templates := Catalog()
	if len(templates) != 4 {
		t.Fatalf("templates=%d want 4", len(templates))
	}
	recommended := 0
	seen := map[string]bool{}
	for _, tmpl := range templates {
		if seen[tmpl.ID] {
			t.Fatalf("duplicate template id %q", tmpl.ID)
		}
		seen[tmpl.ID] = true
		if tmpl.Recommended {
			recommended++
			if tmpl.ID != Privacy {
				t.Fatalf("recommended template=%q want privacy", tmpl.ID)
			}
		}
		req := validRequest(tmpl.ID)
		rendered, err := Render(req)
		if err != nil {
			t.Fatalf("render %s: %v", tmpl.ID, err)
		}
		if err := rendered.Profile.Validate(); err != nil {
			t.Fatalf("profile %s does not validate: %v", tmpl.ID, err)
		}
		if len(rendered.Profile.HostFS.Grants) != 0 {
			t.Fatalf("%s HostFS grants=%v want none", tmpl.ID, rendered.Profile.HostFS.Grants)
		}
		if len(rendered.Profile.CommandAdapters.Adapters) != 0 {
			t.Fatalf("%s command adapters=%v want none", tmpl.ID, rendered.Profile.CommandAdapters.Adapters)
		}
	}
	if recommended != 1 {
		t.Fatalf("recommended templates=%d want 1", recommended)
	}
	for _, id := range []string{Privacy, Hardened, Dev, Debug} {
		if !seen[id] {
			t.Fatalf("missing template %s", id)
		}
	}
}

func TestRequestValidation(t *testing.T) {
	_, err := Render(Request{
		NoInput: true,
		Store:   profile.Store{Root: t.TempDir()},
	})
	if err == nil || !strings.Contains(err.Error(), "--template") || !strings.Contains(err.Error(), "--profile") {
		t.Fatalf("missing explicit choices error=%v", err)
	}

	req := validRequest(Privacy)
	req.Network = "direct"
	_, err = Render(req)
	if err == nil || !strings.Contains(err.Error(), "requires --network tun2socks") {
		t.Fatalf("privacy direct should fail, got %v", err)
	}

	req = validRequest(Dev)
	req.Network = "direct"
	req.ProxySecretRef = "env:HIDEOUT_PROXY_URL"
	_, err = Render(req)
	if err == nil || !strings.Contains(err.Error(), "direct network mode does not use") {
		t.Fatalf("direct proxy should fail, got %v", err)
	}

	req = validRequest(Privacy)
	req.MediatedResolver = "not-an-ip"
	_, err = Render(req)
	if err == nil || !strings.Contains(err.Error(), "not an IP address") {
		t.Fatalf("bad resolver should fail, got %v", err)
	}
}

func TestExistingProfileCollisionFails(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	if err := store.Create(profile.Default("taken")); err != nil {
		t.Fatal(err)
	}
	req := validRequest(Dev)
	req.Store = store
	req.ProfileName = "taken"
	req.CheckExistingProfile = true
	_, err := Render(req)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("collision should fail, got %v", err)
	}
}

func TestHardenedPrivilegeRules(t *testing.T) {
	req := validRequest(Hardened)
	req.Privilege.Status = PrivilegeEnforced
	rendered, err := Render(req)
	if err != nil {
		t.Fatalf("hardened enforced: %v", err)
	}
	if rendered.EffectivePosture != "hardened" || rendered.Profile.Metadata["templateDegraded"] != "false" {
		t.Fatalf("bad enforced posture: %s metadata=%v", rendered.EffectivePosture, rendered.Profile.Metadata)
	}

	req = validRequest(Hardened)
	req.Privilege.Status = PrivilegeDegraded
	_, err = Render(req)
	if err == nil || !strings.Contains(err.Error(), "hardened requires enforced") {
		t.Fatalf("hardened degraded should fail, got %v", err)
	}

	req.AllowDegradedTemplate = true
	rendered, err = Render(req)
	if err != nil {
		t.Fatalf("hardened degraded fallback: %v", err)
	}
	if rendered.EffectivePosture != "hardened-degraded" || rendered.Profile.Metadata["templateDegraded"] != "true" {
		t.Fatalf("bad degraded posture: %s metadata=%v", rendered.EffectivePosture, rendered.Profile.Metadata)
	}
	if !contains(rendered.NonClaims, "not a hardened privilege boundary") {
		t.Fatalf("degraded fallback missing non-claim: %v", rendered.NonClaims)
	}

	req = validRequest(Hardened)
	req.Backend = "native"
	req.Privilege.Status = PrivilegeEnforced
	_, err = Render(req)
	if err == nil || !strings.Contains(err.Error(), "native backend") {
		t.Fatalf("native hardened enforced should fail, got %v", err)
	}
}

func TestEvidenceRedactionAndWrite(t *testing.T) {
	req := validRequest(Privacy)
	req.ProxySecretRef = "proxy-url"
	req.Privilege.Reason = "machineId=0123456789abcdef0123456789abcdef token=cap_0123456789abcdef0123456789abcdef"
	req.Now = time.Date(2026, 7, 9, 1, 2, 3, 0, time.UTC)
	rendered, err := Render(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(rendered.Evidence)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, forbidden := range []string{
		"HIDEOUT_SECRET_PROXY",
		"0123456789abcdef0123456789abcdef",
		"cap_0123456789abcdef0123456789abcdef",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("evidence leaked %q: %s", forbidden, text)
		}
	}
	path := filepath.Join(t.TempDir(), "profile", "onboarding-evidence.json")
	if err := WriteEvidence(path, rendered.Evidence); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("evidence not written: %v", err)
	}
}

func TestReviewNamesHighImpactDefaults(t *testing.T) {
	rendered, err := Render(validRequest(Privacy))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(rendered.Review.Lines, "\n")
	for _, want := range []string{
		"recommended template: privacy",
		"HostFS: none-by-default",
		"adapter packs: none-by-default",
		"privilege:",
		"non-claim:",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("review missing %q:\n%s", want, text)
		}
	}
}

func validRequest(templateID string) Request {
	req := Request{
		ProfileName:           "agent",
		TemplateID:            templateID,
		Backend:               "lima",
		Network:               "tun2socks",
		ProxySecretRef:        "proxy-url",
		MediatedResolver:      "1.1.1.1",
		Privilege:             PrivilegeFact{Status: PrivilegeUnknown, Source: "injected-test"},
		NoInput:               true,
		ExplicitProfile:       true,
		ExplicitTemplate:      true,
		ExplicitBackend:       true,
		ExplicitNetwork:       true,
		CheckExistingProfile:  true,
		AllowDegradedTemplate: false,
		Now:                   time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC),
	}
	if templateID == Hardened {
		req.Privilege.Status = PrivilegeEnforced
	}
	if templateID == Dev || templateID == Debug {
		req.Backend = "native"
		req.Network = "direct"
		req.ProxySecretRef = ""
		req.MediatedResolver = ""
	}
	return req
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
