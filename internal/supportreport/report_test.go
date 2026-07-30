package supportreport

import (
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/doctor"
)

func TestValidateAcceptsBoundedDeterministicReport(t *testing.T) {
	report := testReport(t)
	first, err := MarshalValidated(report, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := MarshalValidated(report, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("support report serialization is not deterministic")
	}
	if len(first) > MaxBytes || !strings.HasSuffix(string(first), "\n") {
		t.Fatalf("invalid serialized contract size=%d", len(first))
	}
}

func TestValidateRejectsOversizeAndProtectedMaterial(t *testing.T) {
	t.Run("oversize", func(t *testing.T) {
		report := testReport(t)
		report.Doctor.Findings[0].Summary = strings.Repeat("x", MaxBytes)
		if _, err := MarshalValidated(report, nil); err == nil {
			t.Fatal("oversized report accepted")
		}
	})

	for name, value := range map[string]string{
		"capability-token": "cap_0123456789abcdef0123456789abcdef",
		"ui-token":         "ui_0123456789abcdef0123456789abcdef",
		"proxy":            "socks5://user:password@127.0.0.1:1080",
		"secret-env":       "HIDEOUT_SECRET_DEFAULT_PROXY",
		"home-path":        "/Users/alice/private-project",
		"machine-id":       "01234567-89ab-cdef-0123-456789abcdef",
		"workspace-body":   "PRIVATE_WORKSPACE_SENTINEL_044",
	} {
		t.Run(name, func(t *testing.T) {
			report := testReport(t)
			report.Doctor.Findings[0].Summary = value
			protected := []string(nil)
			if name == "workspace-body" {
				protected = []string{value}
			}
			if _, err := MarshalValidated(report, protected); err == nil {
				t.Fatalf("protected material %q accepted", value)
			}
		})
	}
}

func TestValidateRequiresActivityEvidenceAndIdentityExclusions(t *testing.T) {
	for _, missing := range []string{
		"activity-record",
		"activity-local-path",
		"activity-command-argv",
		"activity-domain",
		"activity-ip",
	} {
		t.Run(missing, func(t *testing.T) {
			report := testReport(t)
			var kept []string
			for _, class := range report.Redaction.ExcludedDataClasses {
				if class != missing {
					kept = append(kept, class)
				}
			}
			report.Redaction.ExcludedDataClasses = kept
			if _, err := MarshalValidated(report, nil); err == nil {
				t.Fatalf("support report accepted missing %q exclusion", missing)
			}
		})
	}
}

func TestRecoveryProjectionIsUniqueAndOmitsMaintainerActions(t *testing.T) {
	entries := RecoveryEntries()
	seen := map[string]bool{}
	if len(entries) == 0 {
		t.Fatal("recovery projection is empty")
	}
	for _, entry := range entries {
		if seen[entry.Code] {
			t.Fatalf("duplicate recovery code %q", entry.Code)
		}
		seen[entry.Code] = true
		for _, action := range entry.NextActions {
			if strings.Contains(action, "scripts/test-") {
				t.Fatalf("maintainer action escaped: %q", action)
			}
		}
	}
}

func testReport(t *testing.T) Report {
	t.Helper()
	builder := doctor.NewBuilder(doctor.Request{Profile: "default", Backend: "lima"})
	builder.Add("network", "network", doctor.StatusPass, "mode=direct; network origin visible")
	return Report{
		Schema:      Schema,
		GeneratedAt: time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC),
		Product: Product{
			Version: "0.1.0-alpha.1", Commit: "0123456789012345678901234567890123456789",
			BuildTime: "2026-07-26T00:00:00Z", HostOS: "darwin", HostArch: "arm64",
		},
		Support: Support{
			Schema: "hideout.support-matrix/v1", Version: "2026-07-alpha",
			Platform: SupportEntry{Subject: "platform/darwin/arm64", Level: "first-class"},
			Backend:  SupportEntry{Subject: "backend/lima", Level: "first-class"},
		},
		Package: Package{Applicability: "not-applicable", Verification: "not-applicable"},
		Doctor:  builder.Report(),
		Recovery: []RecoveryEntry{{
			Code: "runtime.catalog.invalid", NextActions: []string{"hideout package verify <install-prefix>"},
		}},
		Collection: Collection{
			Product: "collected", Support: "collected", Package: "not-applicable",
			Doctor: "collected", Recovery: "collected",
		},
		Redaction: Redaction{
			Mode:                "shareable-support",
			ExcludedDataClasses: shareableExcludedDataClasses(),
		},
		Provenance: Provenance{
			Command:  "hideout support report --out <path>",
			Delivery: "local-file-only", Uploaded: false, MaxBytes: MaxBytes,
		},
	}
}
