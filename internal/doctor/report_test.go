package doctor

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/hostapppack"
	"github.com/vibe-agi/hideout/internal/recovery"
)

func TestReportSummaryAndRedaction(t *testing.T) {
	b := NewBuilder(Request{Profile: "default", Backend: "native"})
	b.Add("store", "store", "ok", "writable")
	b.Add("proxy", "network", "error", "HIDEOUT_SECRET_PROXY=socks5://example cap_0123456789abcdef0123456789abcdef", WithDetails(map[string]any{
		"machineId": "0123456789abcdef0123456789abcdef",
		"keep":      "value",
	}))
	report := b.Report()
	if !report.Summary.Failed || report.Summary.ExitCode != 1 {
		t.Fatalf("summary did not fail on required error: %+v", report.Summary)
	}
	data := new(bytes.Buffer)
	if err := WriteJSON(data, report); err != nil {
		t.Fatal(err)
	}
	text := data.String()
	for _, leak := range []string{"HIDEOUT_SECRET_PROXY", "cap_0123456789abcdef0123456789abcdef", "0123456789abcdef0123456789abcdef"} {
		if strings.Contains(text, leak) {
			t.Fatalf("report leaked %s:\n%s", leak, text)
		}
	}
	if !strings.Contains(text, `"keep": "value"`) {
		t.Fatalf("report removed user data unexpectedly:\n%s", text)
	}
}

func TestReportRedactsInjectedControlPlaneMaterialAcrossFields(t *testing.T) {
	secretToken := "cap_0123456789abcdef0123456789abcdef"
	machineID := "0123456789abcdef0123456789abcdef"
	b := NewBuilder(Request{Profile: "default", Backend: "native"})
	b.Add("feature-dns", "dns", "warn",
		"HIDEOUT_SECRET_PROXY=socks5://operator:pw@example "+secretToken,
		WithDetails(map[string]any{
			"observedFacts": []string{
				"proxy=socks5://user:pass@example",
				"machineId=" + machineID,
			},
			"nested": map[string]any{
				"HIDEOUT_SECRET_PROXY": "raw-secret",
				"keep":                 "keep-me",
			},
		}),
		WithNextActions("rerun with "+secretToken, "keep user value keep-me"),
		WithEvidenceRefs("/tmp/hideout/hostfs-overlay/objects/"+machineID, "audit:keep-me"),
		WithRequired(false),
	)
	report := b.Report()
	jsonOut := new(bytes.Buffer)
	if err := WriteJSON(jsonOut, report); err != nil {
		t.Fatal(err)
	}
	humanOut := new(bytes.Buffer)
	WriteHuman(humanOut, report)
	combined := jsonOut.String() + "\n" + humanOut.String()
	for _, leak := range []string{
		"HIDEOUT_SECRET_PROXY",
		secretToken,
		machineID,
		"raw-secret",
	} {
		if strings.Contains(combined, leak) {
			t.Fatalf("doctor report leaked %q:\n%s", leak, combined)
		}
	}
	for _, keep := range []string{"keep-me", "audit:keep-me"} {
		if !strings.Contains(combined, keep) {
			t.Fatalf("doctor report removed non-secret %q:\n%s", keep, combined)
		}
	}
}

func TestRecoveryCodeRendersInHumanAndJSON(t *testing.T) {
	b := NewBuilder(Request{Profile: "default", Backend: "native"})
	b.Add("feature-packaging", "packaging", StatusWarn, "external prerequisite missing",
		WithRecovery(recovery.CodePackagePrerequisiteMissing),
		WithRequired(false),
	)
	report := b.Report()
	if len(report.Findings) != 1 {
		t.Fatalf("findings=%d", len(report.Findings))
	}
	finding := report.Findings[0]
	if finding.Code != recovery.CodePackagePrerequisiteMissing {
		t.Fatalf("code=%q", finding.Code)
	}
	if finding.Reason == "" || finding.Hint == "" || len(finding.NextActions) == 0 {
		t.Fatalf("recovery fields not populated: %+v", finding)
	}
	jsonOut := new(bytes.Buffer)
	if err := WriteJSON(jsonOut, report); err != nil {
		t.Fatal(err)
	}
	humanOut := new(bytes.Buffer)
	WriteHuman(humanOut, report)
	for _, out := range []string{jsonOut.String(), humanOut.String()} {
		if !strings.Contains(out, recovery.CodePackagePrerequisiteMissing) {
			t.Fatalf("output missing recovery code:\n%s", out)
		}
		if !strings.Contains(out, finding.Hint) {
			t.Fatalf("output missing recovery hint:\n%s", out)
		}
	}
}

func TestUnknownRecoveryCodeIsNotPublished(t *testing.T) {
	b := NewBuilder(Request{})
	b.Add("unknown-recovery", "test", StatusWarn, "test", WithRecovery("package.typo"))
	finding := b.Report().Findings[0]
	if finding.Code != "" || finding.Reason != "" || finding.Hint != "" || len(finding.NextActions) != 0 {
		t.Fatalf("unknown recovery code escaped registry: %+v", finding)
	}
}

func TestRuntimeRecoveryCodesRenderOnlyThroughRegistry(t *testing.T) {
	if !containsString(SupportedFeatures, "runtime") {
		t.Fatal("runtime is not a supported doctor feature")
	}
	for _, code := range []string{
		recovery.CodeRuntimeSelectionUnsupported,
		recovery.CodeRuntimeCatalogInvalid,
		recovery.CodeRuntimeArtifactUnavailable,
		recovery.CodeRuntimeArtifactDigest,
		recovery.CodeRuntimeDiskInsufficient,
		recovery.CodeRuntimeBoundaryMissing,
		recovery.CodeRuntimeBaselineMissing,
		recovery.CodeRuntimeCommandMissing,
		recovery.CodeRuntimeNetworkDenied,
		recovery.CodeRuntimeDNSFailed,
		recovery.CodeRuntimeRegistryFailed,
		recovery.CodeRuntimePrefixUnwritable,
	} {
		t.Run(code, func(t *testing.T) {
			b := NewBuilder(Request{})
			b.Add("runtime", "runtime", StatusWarn, "observed", WithRecovery(code))
			finding := b.Report().Findings[0]
			if finding.Code != code || finding.Reason == "" || finding.Hint == "" || len(finding.NextActions) != 1 {
				t.Fatalf("runtime recovery did not resolve through registry: %+v", finding)
			}
		})
	}
}

func TestHostAppInspectionFindingsUseSharedFactsWithoutRunningPackageHints(t *testing.T) {
	inspection := hostapppack.Inspection{
		Schema: hostapppack.InspectionVersion,
		Entries: []hostapppack.InspectionEntry{{
			Summary: hostapppack.InspectionSummary{
				Command: "editor", App: "editor", Profile: "privacy", Access: hostapppack.AccessAskEachRun,
				Readiness: "review-required", NextAction: "review the exact permission difference",
			},
			Package:     hostapppack.InspectionPackage{ID: "community.editor", RevisionID: "rev_123", SourceKind: hostapppack.SourceLocal, SourceDigest: "sha256:source", TestStatus: hostapppack.TestNotRun},
			Permissions: hostapppack.InspectionPermissions{Fingerprint: "sha256:permission", Status: "review-required", Diff: []string{"bindings/open-resource/commands: editor -> editor2"}},
			AppIdentity: hostapppack.InspectionAppIdentity{Verification: "unverified", RootClass: "applications", OwnerClass: "operator"},
			Binding:     hostapppack.InspectionBinding{ID: "open-resource", Commands: []string{"editor"}, ResourceKinds: []string{"workspace"}, CapabilityID: hostapppack.CapabilityOpenResource, Grammar: hostapppack.GrammarOpenResourceV1, ResultPolicy: hostapppack.ResultNone, ShadowStatus: "owned"},
			Safety:      hostapppack.InspectionSafety{Posture: "unverified-app"},
			Runtime:     hostapppack.InspectionRuntime{GrantState: "pending"},
			Hint:        &hostapppack.InspectionHint{Untrusted: true, Text: "RUN-ME --capability-token cap_0123456789abcdef0123456789abcdef", URL: "https://example.test"},
		}},
	}
	b := NewBuilder(Request{Profile: "privacy", Backend: "native"})
	b.AddHostAppInspection(inspection, map[string]string{"editor": recovery.CodeHostAppPermissionReviewRequired})
	report := b.Report()
	if len(report.Findings) != 1 {
		t.Fatalf("findings=%+v", report.Findings)
	}
	finding := report.Findings[0]
	if finding.Code != recovery.CodeHostAppPermissionReviewRequired || finding.Status != StatusWarn {
		t.Fatalf("finding=%+v", finding)
	}
	data := new(bytes.Buffer)
	if err := WriteJSON(data, report); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(data.String(), "RUN-ME") || strings.Contains(data.String(), "example.test") || strings.Contains(data.String(), "cap_012345") {
		t.Fatalf("doctor promoted or leaked package hint: %s", data.String())
	}
	for _, want := range []string{"community.editor", "review-required", "unverified-app", "bindings/open-resource/commands"} {
		if !strings.Contains(data.String(), want) {
			t.Fatalf("doctor shared facts lack %q: %s", want, data.String())
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
