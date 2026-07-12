package recovery

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRegistryContainsV1CodesOnce(t *testing.T) {
	if err := Validate(All()); err != nil {
		t.Fatalf("registry invalid: %v", err)
	}
	want := []string{
		CodePackageObsoleteLeftover,
		CodePackagePrerequisiteMissing,
		CodeInitProxySecretMissing,
		CodeInitMediatedResolverMissing,
		CodePrivilegeStatusDegraded,
		CodeReleaseGateEvidenceMissing,
		CodeReleaseEvidenceStale,
		CodeHostFSReservedRootDenied,
		CodeDecisionClaimExpired,
		CodeRuntimeSelectionUnsupported,
		CodeRuntimeCatalogInvalid,
		CodeRuntimeArtifactUnavailable,
		CodeRuntimeArtifactDigest,
		CodeRuntimeDiskInsufficient,
		CodeRuntimeBoundaryMissing,
		CodeRuntimeBaselineMissing,
		CodeRuntimeCommandMissing,
		CodeRuntimeNetworkDenied,
		CodeRuntimeDNSFailed,
		CodeRuntimeRegistryFailed,
		CodeRuntimePrefixUnwritable,
	}
	seen := map[string]int{}
	for _, entry := range All() {
		seen[entry.Code]++
	}
	for _, code := range want {
		if seen[code] != 1 {
			t.Fatalf("code %s count=%d", code, seen[code])
		}
	}
}

func TestRuntimeRecoveryCodesAreCompleteAndActionable(t *testing.T) {
	codes := []string{
		CodeRuntimeSelectionUnsupported,
		CodeRuntimeCatalogInvalid,
		CodeRuntimeArtifactUnavailable,
		CodeRuntimeArtifactDigest,
		CodeRuntimeDiskInsufficient,
		CodeRuntimeBoundaryMissing,
		CodeRuntimeBaselineMissing,
		CodeRuntimeCommandMissing,
		CodeRuntimeNetworkDenied,
		CodeRuntimeDNSFailed,
		CodeRuntimeRegistryFailed,
		CodeRuntimePrefixUnwritable,
	}
	for _, code := range codes {
		entry, ok := Lookup(code)
		if !ok {
			t.Fatalf("runtime recovery code %q is not registered", code)
		}
		if entry.Subsystem != "runtime" || entry.Reason == "" || entry.Hint == "" || len(entry.NextActions) != 1 || len(entry.DocsRefs) == 0 {
			t.Fatalf("runtime recovery code %q is incomplete: %+v", code, entry)
		}
		if !strings.HasPrefix(entry.NextActions[0], "hideout ") {
			t.Fatalf("runtime recovery action is not a Hideout command: %+v", entry)
		}
	}
}

func TestHostAppRecoveryCodesAreCompleteAndActionable(t *testing.T) {
	codes := []string{
		CodeHostAppSourceInvalid,
		CodeHostAppDigestMismatch,
		CodeHostAppCommandConflict,
		CodeHostAppIdentityInvalid,
		CodeHostAppSafetyUnavailable,
		CodeHostAppPermissionReviewRequired,
		CodeHostAppPortalUnavailable,
		CodeHostAppBindingRevoked,
		CodeHostAppNewRunRequired,
	}
	for _, code := range codes {
		entry, ok := Lookup(code)
		if !ok {
			t.Fatalf("host-app recovery code %q is not registered", code)
		}
		if entry.Subsystem != "host-app" || entry.Reason == "" || entry.Hint == "" || len(entry.NextActions) == 0 || len(entry.DocsRefs) == 0 {
			t.Fatalf("host-app recovery code %q is incomplete: %+v", code, entry)
		}
		for _, action := range entry.NextActions {
			if !strings.HasPrefix(action, "hideout ") {
				t.Fatalf("host-app recovery action is not a Hideout command: %+v", entry)
			}
		}
	}
}

func TestRegistryJSONDeterministic(t *testing.T) {
	var a, b bytes.Buffer
	if err := WriteJSON(&a); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(&b); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Fatalf("registry JSON is not deterministic")
	}
	var view RegistryView
	if err := json.Unmarshal(a.Bytes(), &view); err != nil {
		t.Fatalf("decode registry JSON: %v", err)
	}
	if view.Schema != Schema {
		t.Fatalf("schema=%s", view.Schema)
	}
	for i := 1; i < len(view.Codes); i++ {
		if strings.Compare(view.Codes[i-1].Code, view.Codes[i].Code) > 0 {
			t.Fatalf("codes not sorted: %s before %s", view.Codes[i-1].Code, view.Codes[i].Code)
		}
	}
}

func TestLookup(t *testing.T) {
	entry, ok := Lookup(CodePackageObsoleteLeftover)
	if !ok {
		t.Fatalf("lookup %s failed", CodePackageObsoleteLeftover)
	}
	if entry.Subsystem != "package" || entry.Hint == "" {
		t.Fatalf("unexpected entry: %+v", entry)
	}
	if _, ok := Lookup("missing.code"); ok {
		t.Fatal("unknown code lookup succeeded")
	}
}

func TestValidateRejectsDuplicateAndControlPlaneActions(t *testing.T) {
	entries := All()
	entries = append(entries, entries[0])
	if err := Validate(entries); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate rejection, got %v", err)
	}
	bad := []Code{{Code: "test.bad", Subsystem: "test", Severity: "error", Reason: "bad", Hint: "bad", NextActions: []string{"echo HIDEOUT_SECRET_PROXY"}}}
	if err := Validate(bad); err == nil || !strings.Contains(err.Error(), "control-plane") {
		t.Fatalf("expected control-plane action rejection, got %v", err)
	}
}

func TestReleaseRecoveryUsesReadinessEvidenceFlags(t *testing.T) {
	for _, code := range []string{CodeReleaseGateEvidenceMissing, CodeReleaseEvidenceStale} {
		entry, ok := Lookup(code)
		if !ok {
			t.Fatal(code)
		}
		actions := strings.Join(entry.NextActions, "\n")
		if !strings.Contains(actions, "--gate2-evidence") || !strings.Contains(actions, "--gate3-evidence") || strings.Contains(actions, " --gate2 ") || strings.Contains(actions, " --gate3 ") {
			t.Fatalf("%s has invalid readiness flags: %s", code, actions)
		}
	}
}
