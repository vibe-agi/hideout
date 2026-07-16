package releasecompat

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestBuiltinMatrixValidatesRequiredRows(t *testing.T) {
	matrix := BuiltinMatrix()
	if err := ValidateMatrix(matrix); err != nil {
		t.Fatalf("builtin matrix invalid: %v", err)
	}
	for _, subject := range []string{
		"platform/darwin/arm64",
		"platform/linux/amd64",
		"platform/linux/arm64",
		"backend/native",
		"helper/linux-session-supervisor",
		"feature/dns-mediation",
		"feature/hostfs-write-overlay",
		"feature/hostfs-discoverable-namespace",
		"feature/host-capability-projection",
		"feature/supported-cli-runtime",
		"feature/community-host-app-recipes",
		"feature/concurrent-run-sessions",
		"release/public-alpha-package",
		"release/developer-id-notarization",
		"gate/release-candidate",
	} {
		if _, ok := FindEntry(matrix, subject); !ok {
			t.Fatalf("missing subject %s", subject)
		}
	}
}

func TestBuiltinMatrixCarriesPublicAlphaNonClaims(t *testing.T) {
	matrix := BuiltinMatrix()
	want := map[string]bool{
		"public-alpha-maturity":        false,
		"runtime-freshness":            false,
		"privacy-prerequisites":        false,
		"ui-maturity":                  false,
		"cross-workspace-shared-vm":    false,
		"automatic-final-session-stop": false,
		"terminal-emulator-hardening":  false,
	}
	for _, nonClaim := range matrix.NonClaims {
		if _, ok := want[nonClaim.ID]; ok {
			want[nonClaim.ID] = true
		}
	}
	for id, found := range want {
		if !found {
			t.Fatalf("missing public alpha non-claim %s", id)
		}
	}
}

func TestMatrixRejectsMissingGuidanceAndUnknownLevel(t *testing.T) {
	matrix := BuiltinMatrix()
	matrix.Entries[0].Level = "maybe"
	if err := ValidateMatrix(matrix); err == nil {
		t.Fatal("unknown support level accepted")
	}

	matrix = BuiltinMatrix()
	matrix.Entries[1].Guidance = ""
	if err := ValidateMatrix(matrix); err == nil {
		t.Fatal("non-first-class entry without guidance accepted")
	}
}

func TestWriteMatrixJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteMatrixJSON(&buf, BuiltinMatrix()); err != nil {
		t.Fatalf("write matrix json: %v", err)
	}
	var decoded Matrix
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("decode matrix json: %v", err)
	}
	if decoded.Schema != MatrixSchema {
		t.Fatalf("schema=%s", decoded.Schema)
	}
}

func TestCurrentSupportSummaryIncludesPlatformAndBackend(t *testing.T) {
	summary := CurrentSupportSummary("native")
	for _, want := range []string{"platform/", "backend/native:degraded"} {
		if !bytes.Contains([]byte(summary), []byte(want)) {
			t.Fatalf("summary missing %q: %s", want, summary)
		}
	}
}
