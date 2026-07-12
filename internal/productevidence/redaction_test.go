package productevidence

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestManifestSanitizesControlPlaneMaterial(t *testing.T) {
	m := validManifest()
	m.Commit = "cap_0123456789abcdef0123456789abcdef"
	m.Proofs[0].CommandSummary = "HIDEOUT_SECRET_DEFAULT_PROXY=socks5://127.0.0.1:1 keep-me"
	m.Proofs[0].CoveredClaims[0].Description = "machineId=0123456789abcdef0123456789abcdef keep-me"
	m.Proofs[0].Prerequisites[0].Reason = "ui_0123456789abcdef0123456789abcdef"

	sanitized := m.Sanitized()
	data, err := json.Marshal(sanitized)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{
		"cap_0123456789abcdef0123456789abcdef",
		"ui_0123456789abcdef0123456789abcdef",
		"socks5://127.0.0.1:1",
		"0123456789abcdef0123456789abcdef",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("sanitized manifest leaked %q in %s", forbidden, text)
		}
	}
	if !strings.Contains(text, "keep-me") {
		t.Fatalf("sanitized manifest removed user data: %s", text)
	}
	if err := sanitized.Validate(); err != nil {
		t.Fatalf("sanitized manifest rejected: %v", err)
	}
}

func TestValidateRejectsUnredactedControlPlaneMaterial(t *testing.T) {
	m := validManifest()
	m.Proofs[0].CommandSummary = "cap_0123456789abcdef0123456789abcdef"
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "control-plane") {
		t.Fatalf("unredacted manifest err=%v, want control-plane rejection", err)
	}
}

func TestRuntimeBindingSanitizesInjectedControlPlaneCredential(t *testing.T) {
	m := validManifest()
	m.Proofs[0].Runtime = &RuntimeBinding{
		Schema: RuntimeBindingSchema, Family: "developer-standard", Revision: "2026.07.0",
		ArtifactSHA256: strings.Repeat("a", 64), EnvironmentID: "env_20260711t000000z0123456789abcdef0123",
		HostOS: "darwin", HostArch: "arm64", GuestArch: "aarch64",
		CandidateCommit: "candidate cap_0123456789abcdef0123456789abcdef keep-commit",
	}
	sanitized := m.Sanitized()
	data, err := json.Marshal(sanitized)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "cap_0123456789abcdef0123456789abcdef") {
		t.Fatalf("runtime evidence leaked injected credential: %s", data)
	}
	if !strings.Contains(string(data), "keep-commit") {
		t.Fatalf("runtime evidence removed ordinary candidate context: %s", data)
	}
	if err := sanitized.Validate(); err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("credential-injected runtime commit must remain invalid after sanitization: %v", err)
	}
}
