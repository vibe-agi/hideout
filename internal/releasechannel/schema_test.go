package releasechannel

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestPublicReleaseSchemasAcceptModelsAndRejectUnknownFields(t *testing.T) {
	root, release := releaseFixture(t)
	_ = root
	bundle := bundleFixture(testDigest, 1)
	receipt := receiptFixture(release)
	inventory := inventoryFixture(release)
	verification := PackageVerificationObservation{
		Schema: PackageVerificationSchema, ObservedAt: time.Now(), Status: "passed",
		Mode: "artifact", Files: 1, Package: packageIdentityFromRelease(release),
		PackageManifestSHA256: testDigest,
	}

	tests := []struct {
		name   string
		schema string
		value  any
	}{
		{name: "public release", schema: "public-release.schema.json", value: release},
		{name: "evidence bundle", schema: "public-evidence-bundle.schema.json", value: bundle},
		{name: "publication receipt", schema: "publication-receipt.schema.json", value: receipt},
		{name: "published inventory", schema: "published-release-inventory.schema.json", value: inventory},
		{name: "package verification", schema: "release-package-verification.schema.json", value: verification},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema := compileReleaseSchema(t, tt.schema)
			if err := validateSchemaValue(schema, tt.value); err != nil {
				t.Fatalf("valid model rejected: %v", err)
			}

			data, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatal(err)
			}
			var object map[string]any
			if err := json.Unmarshal(data, &object); err != nil {
				t.Fatal(err)
			}
			object["unexpected"] = true
			if err := validateSchemaValue(schema, object); err == nil {
				t.Fatal("unknown top-level property unexpectedly passed")
			}
		})
	}
}

func TestPublicReleaseSchemaRejectsAbbreviatedCommitAndIncompleteIdentity(t *testing.T) {
	_, release := releaseFixture(t)
	schema := compileReleaseSchema(t, "public-release.schema.json")

	release.Source.Commit = testCommit[:12]
	if err := validateSchemaValue(schema, release); err == nil {
		t.Fatal("abbreviated source commit unexpectedly passed")
	}

	_, release = releaseFixture(t)
	release.Signing.TeamID = ""
	if err := validateSchemaValue(schema, release); err == nil {
		t.Fatal("incomplete signing identity unexpectedly passed")
	}
}

func compileReleaseSchema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	for _, resource := range []string{
		"public-release.schema.json",
		"public-evidence-bundle.schema.json",
		"publication-receipt.schema.json",
		"published-release-inventory.schema.json",
		"release-package-verification.schema.json",
	} {
		data, err := os.ReadFile(filepath.Join("..", "..", "schemas", resource))
		if err != nil {
			t.Fatal(err)
		}
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("parse %s: %v", resource, err)
		}
		url := "https://hideout.local/schemas/" + resource
		if err := compiler.AddResource(url, document); err != nil {
			t.Fatalf("add %s: %v", resource, err)
		}
	}
	schema, err := compiler.Compile("https://hideout.local/schemas/" + name)
	if err != nil {
		t.Fatalf("compile %s: %v", name, err)
	}
	return schema
}

func validateSchemaValue(schema *jsonschema.Schema, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return err
	}
	return schema.Validate(document)
}

func receiptFixture(release PublicRelease) PublicationReceipt {
	receipt := PublicationReceipt{
		Schema: PublicationReceiptSchema, Status: "public-verified",
		ObservedAt: time.Date(2026, 7, 13, 1, 0, 0, 0, time.UTC),
		Version:    release.Version, Tag: release.Tag, SourceCommit: release.Source.Commit,
		ReleaseID: 123, URL: "https://github.com/vibe-agi/hideout/releases/tag/" + release.Tag,
		Prerelease: true, Immutable: true, Package: packageIdentityFromRelease(release),
		EvidenceSHA256: testDigest, ProofStatus: "satisfied",
	}
	for _, name := range []string{
		"hideout-v0.1.0-alpha.1-darwin-arm64.tar.gz",
		"hideout-v0.1.0-alpha.1-evidence.tar.gz",
		"hideout-v0.1.0-alpha.1-release.json",
		"SHA256SUMS",
	} {
		receipt.Assets = append(receipt.Assets, DownloadedAsset{
			Name: name, Bytes: 1, APISHA256: testDigest, DownloadSHA256: testDigest,
		})
	}
	return receipt
}

func inventoryFixture(release PublicRelease) PublishedInventory {
	return PublishedInventory{
		Schema:      PublishedInventorySchema,
		GeneratedAt: time.Date(2026, 7, 13, 2, 0, 0, 0, time.UTC),
		Current: &InventoryEntry{
			Version: release.Version, Tag: release.Tag, Maturity: release.Maturity,
			Platform: "darwin/arm64", Backend: "lima",
			Package:       packageIdentityFromRelease(release),
			ReleaseURL:    "https://github.com/vibe-agi/hideout/releases/tag/" + release.Tag,
			ReceiptSHA256: testDigest, SupportMatrix: "2026-07-13",
			NonClaims: []string{"workspace-dlp"},
		},
	}
}
