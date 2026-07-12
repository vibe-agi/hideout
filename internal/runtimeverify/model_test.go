package runtimeverify

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/vibe-agi/hideout/internal/environment"
)

func TestReceiptJSONSchemaRequiresActiveBuildIdentity(t *testing.T) {
	schemaPath := filepath.Join("..", "..", "schemas", "runtime-verification.schema.json")
	schemaData, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	schemaDoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaData))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("runtime-verification.schema.json", schemaDoc); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("runtime-verification.schema.json")
	if err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(validReceipt())
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(doc); err != nil {
		t.Fatalf("valid receipt does not satisfy schema: %v", err)
	}
	var missing map[string]any
	if err := json.Unmarshal(data, &missing); err != nil {
		t.Fatal(err)
	}
	delete(missing["instance"].(map[string]any), "activeBuildIdentity")
	if err := schema.Validate(missing); err == nil {
		t.Fatal("schema accepted receipt without active build identity")
	}
}

func TestReceiptValidateRequiresCompleteBoundedContractResults(t *testing.T) {
	receipt := validReceipt()
	if err := receipt.Validate(); err != nil {
		t.Fatalf("valid receipt: %v", err)
	}

	cases := map[string]func(*Receipt){
		"image mismatch":          func(r *Receipt) { r.ImageRef = environment.BuiltinBaseImage },
		"contract mismatch":       func(r *Receipt) { r.ContractDigest = "sha256:" + strings.Repeat("0", 64) },
		"duplicate result":        func(r *Receipt) { r.Results = append(r.Results, r.Results[0]) },
		"failed id mismatch":      func(r *Receipt) { r.FailedIDs = []string{"unknown"} },
		"output too long":         func(r *Receipt) { r.Results[0].VersionOutput = strings.Repeat("x", 513) },
		"control output":          func(r *Receipt) { r.Results[0].VersionOutput = "git\x1b[31m" },
		"false ready":             func(r *Receipt) { r.Status = StatusPreviewReady },
		"missing session":         func(r *Receipt) { r.SessionID = "" },
		"wrong image fact":        func(r *Receipt) { r.Instance.ImageSHA256 = strings.Repeat("9", 64) },
		"missing active identity": func(r *Receipt) { r.Instance.ActiveBuildIdentity = "" },
		"wrong active identity": func(r *Receipt) {
			r.Instance.ActiveBuildIdentity = "sha256:" + strings.Repeat("9", 64)
		},
		"wrong host tuple":  func(r *Receipt) { r.Instance.HostArch = "amd64" },
		"missing boot fact": func(r *Receipt) { r.Instance.BootID = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			got := validReceipt()
			mutate(&got)
			if err := got.Validate(); err == nil {
				t.Fatal("expected failure")
			}
		})
	}
}

func TestNormalizeStripsControlPlaneMaterialAndBoundsOutput(t *testing.T) {
	receipt := validReceipt()
	receipt.Results[0].VersionOutput = "git cap_0123456789abcdef HIDEOUT_SECRET_PROXY=value machineId=0123456789abcdef0123456789abcdef keep-me\x1b"
	receipt.Results[0].Reason = "token=cap_0123456789abcdef keep-reason"
	receipt.Normalize()
	text := receipt.Results[0].VersionOutput + receipt.Results[0].Reason
	for _, forbidden := range []string{"cap_0123456789abcdef", "HIDEOUT_SECRET_PROXY", "0123456789abcdef0123456789abcdef", "\x1b"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("normalized receipt leaked %q: %q", forbidden, text)
		}
	}
	for _, want := range []string{"keep-me", "keep-reason"} {
		if !strings.Contains(text, want) {
			t.Fatalf("normalized receipt removed user data %q: %q", want, text)
		}
	}
}

func validReceipt() Receipt {
	provenance := validProvenance()
	return Receipt{
		Schema:         Schema,
		EnvironmentID:  "env_20260711t000000z0123456789abcdef0123",
		ImageRef:       provenance.ImageRef(),
		Provenance:     provenance,
		ContractDigest: provenance.ContractDigest,
		ObservedAt:     time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC),
		SessionID:      "ses_20260711t000000z0123456789abcdef0123",
		Backend:        "lima",
		BackendReal:    true,
		Running:        true,
		Instance: Instance{
			Name: "hideout-runtime-test", Status: "Running", VMType: "vz",
			HostOS: "darwin", HostArch: "arm64", GuestArch: "aarch64",
			ImageLocation: provenance.ArtifactLocation, ImageSHA256: provenance.ArtifactSHA256,
			ActiveBuildIdentity: provenance.PackageInventoryDigest,
			BootID:              "01234567-89ab-cdef-0123-456789abcdef",
		},
		PrivilegeStatus: "enforced",
		Status:          StatusPreviewFailed,
		Results: []Result{
			{ID: "baseline.git", Class: "baseline", Command: "git", Present: false, Matched: false, Reason: "command-missing"},
		},
		FailedIDs:    []string{"baseline.git"},
		RecoveryCode: "runtime.baseline.missing",
	}
}

func validProvenance() environment.RuntimeProvenance {
	return environment.RuntimeProvenance{
		Family: "developer-standard", Revision: "2026.07.0", CatalogRelease: "2026.07.0",
		ContractID: "developer-standard/v1", ContractDigest: "sha256:" + strings.Repeat("b", 64),
		ArtifactLocation: "https://github.com/vibe-agi/hideout/releases/download/runtime-2026.07.0/developer-standard.qcow2",
		ArtifactSHA256:   strings.Repeat("a", 64), PackageInventoryDigest: "sha256:" + strings.Repeat("c", 64),
		HostOS: "darwin", HostArch: "arm64", GuestArch: "aarch64", Maturity: "preview",
		DownloadBytes: 512 << 20, VirtualBytes: 12 << 30,
	}
}
