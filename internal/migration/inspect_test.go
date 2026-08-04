package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestInspectSealedBundleAuthenticatesStrictManifestAndOmitsPlaintext(t *testing.T) {
	manifest, records := sealedInspectionManifestFixture(t)
	bundle := writeSealedInspectionFixture(t, manifest, records, nil)
	public, err := InspectPublicBundle(bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil {
		t.Fatal(err)
	}
	if public.BundleID != manifest.BundleID || public.FormatVersion != BundleFormatVersion ||
		public.CreatedAt != "2026-08-02T00:00:00Z" ||
		public.EncodedBytes != uint64(len(bundle)) || public.HeaderDigest.Validate() != nil ||
		!public.TrailerPresent {
		t.Fatalf("public inspection=%+v", public)
	}
	inspection, err := InspectSealedBundle(
		context.Background(), bytes.NewReader(bundle), int64(len(bundle)),
		[]byte("inspection passphrase"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.CreatedAt != "2026-08-02T00:00:00Z" ||
		inspection.Binding.BundleID != manifest.BundleID ||
		inspection.Binding.FormatVersion != BundleFormatVersion ||
		inspection.Binding.FileDigest != digestBytes(bundle) ||
		inspection.Binding.ManifestDigest != inspection.Summary.ManifestDigest ||
		inspection.Binding.CompletionDigest.Validate() != nil ||
		inspection.Summary.RecordCount != uint64(len(records)+2) ||
		inspection.Manifest.BundleID != manifest.BundleID {
		t.Fatalf("inspection binding drifted: %+v", inspection)
	}
	if err := VerifySealedBundleFile(
		context.Background(), bytes.NewReader(bundle), int64(len(bundle)), inspection.Binding,
	); err != nil {
		t.Fatalf("verify sealed binding: %v", err)
	}
	changed := inspection.Binding
	changed.FileDigest = digestForTest("e")
	if err := VerifySealedBundleFile(
		context.Background(), bytes.NewReader(bundle), int64(len(bundle)), changed,
	); !errors.Is(err, ErrBundleChanged) {
		t.Fatalf("changed file binding error=%v", err)
	}
	projected, err := json.Marshal(inspection)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"inspection passphrase", "super-secret-selected-value", "fixture profile payload",
	} {
		if bytes.Contains(projected, []byte(forbidden)) {
			t.Fatalf("inspection retained record plaintext %q: %s", forbidden, projected)
		}
	}
}

func TestAuthenticateBundleHeaderAcceptsPartialAndRejectsWrongPassphrase(t *testing.T) {
	manifest, records := sealedInspectionManifestFixture(t)
	bundle := writeSealedInspectionFixture(t, manifest, records, nil)
	prologueValue, err := decodePrologue(bundle[:PrologueSize])
	if err != nil {
		t.Fatal(err)
	}
	headerEnd := PrologueSize + int(prologueValue.HeaderLength)
	partial := append([]byte(nil), bundle[:headerEnd]...)
	inspection, err := AuthenticateBundleHeader(
		bytes.NewReader(partial), int64(len(partial)), []byte("inspection passphrase"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.BundleID != manifest.BundleID || inspection.TrailerPresent ||
		inspection.HeaderDigest.Validate() != nil {
		t.Fatalf("partial header inspection=%+v", inspection)
	}
	if _, err := AuthenticateBundleHeader(
		bytes.NewReader(partial), int64(len(partial)), []byte("wrong passphrase"),
	); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("wrong passphrase error=%v", err)
	}
}

func TestInspectSealedBundleRejectsHostileManifestClosureAndRecordBindings(t *testing.T) {
	base, records := sealedInspectionManifestFixture(t)
	for name, mutate := range map[string]func(*Manifest){
		"architecture": func(manifest *Manifest) {
			manifest.SourceProduct.GuestArch = "arm64"
		},
		"guest-user": func(manifest *Manifest) {
			manifest.Environments[0].GuestUser = "root"
		},
		"guest-path-traversal": func(manifest *Manifest) {
			manifest.Environments[0].WorkspaceProposals[0].GuestPath = "/workspace/../../etc"
		},
		"graph": func(manifest *Manifest) {
			manifest.DiskEdges = nil
		},
		"record-range": func(manifest *Manifest) {
			manifest.ComponentIndex[1].FirstRecord++
			manifest.ComponentIndex[1].LastRecord++
		},
		"record-component-substitution": func(manifest *Manifest) {
			manifest.ComponentIndex[0].ComponentID = "component_profile9999"
			manifest.Environments[0].ProfileComponentID = "component_profile9999"
		},
		"disk-digest-substitution": func(manifest *Manifest) {
			manifest.ComponentIndex[1].ContentDigest = digestForTest("f")
		},
		"duplicate-secret": func(manifest *Manifest) {
			manifest.SecretEntries = append(manifest.SecretEntries, manifest.SecretEntries[0])
		},
	} {
		t.Run(name, func(t *testing.T) {
			manifest := cloneInspectionManifest(t, base)
			mutate(&manifest)
			bundle := writeSealedInspectionFixture(t, manifest, records, nil)
			_, err := InspectSealedBundle(
				context.Background(), bytes.NewReader(bundle), int64(len(bundle)),
				[]byte("inspection passphrase"),
			)
			if !errors.Is(err, ErrCorruptBundle) {
				t.Fatalf("hostile manifest error=%v", err)
			}
		})
	}
}

func TestInspectSealedBundleRejectsUnknownManifestFieldsAndCancellation(t *testing.T) {
	manifest, records := sealedInspectionManifestFixture(t)
	manifestBytes, err := canonicalMarshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(manifestBytes, &document); err != nil {
		t.Fatal(err)
	}
	document["futureAuthority"] = "must-not-be-ignored"
	hostile, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	bundle := writeSealedInspectionFixture(t, manifest, records, hostile)
	if _, err := InspectSealedBundle(
		context.Background(), bytes.NewReader(bundle), int64(len(bundle)),
		[]byte("inspection passphrase"),
	); !errors.Is(err, ErrCorruptBundle) {
		t.Fatalf("unknown manifest field error=%v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := InspectSealedBundle(
		ctx, bytes.NewReader(bundle), int64(len(bundle)),
		[]byte("inspection passphrase"),
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled inspection error=%v", err)
	}
}

func TestInspectSealedBundleRejectsNoncanonicalMetadataRecords(t *testing.T) {
	manifest, records := sealedInspectionManifestFixture(t)
	records[0].Plaintext = []byte("{\n  \"schema\": \"fixture.profile/v1\", \"value\": \"fixture profile payload\"\n}")
	manifest.ComponentIndex[0].LogicalBytes = uint64(len(records[0].Plaintext))
	manifest.ComponentIndex[0].ContentDigest = digestBytes(records[0].Plaintext)
	bundle := writeSealedInspectionFixture(t, manifest, records, nil)
	if _, err := InspectSealedBundle(
		context.Background(), bytes.NewReader(bundle), int64(len(bundle)),
		[]byte("inspection passphrase"),
	); !errors.Is(err, ErrCorruptBundle) {
		t.Fatalf("noncanonical metadata error=%v", err)
	}
}

func sealedInspectionManifestFixture(
	t *testing.T,
) (Manifest, []RecordInput) {
	t.Helper()
	profile := []byte(`{"schema":"fixture.profile/v1","value":"fixture profile payload"}`)
	diskBytes := bytes.Repeat([]byte("disk"), 1024)
	secret := []byte("super-secret-selected-value")
	digester, err := NewLogicalDigester(uint64(len(diskBytes)))
	if err != nil {
		t.Fatal(err)
	}
	if err := digester.WriteExtent(Extent{
		Kind: ExtentData, LogicalOffset: 0, Length: uint64(len(diskBytes)), Data: diskBytes,
	}); err != nil {
		t.Fatal(err)
	}
	diskDigest, err := digester.Finish()
	if err != nil {
		t.Fatal(err)
	}
	records := []RecordInput{
		{Type: RecordMetadata, ComponentID: "component_profile0001", Plaintext: profile},
		{Type: RecordRawChunk, ComponentID: "component_disk000001", Plaintext: diskBytes},
		{Type: RecordSecretValue, ComponentID: "component_secret0001", Plaintext: secret},
	}
	manifest := Manifest{
		Schema: manifestSchema, BundleID: "migb_inspection0001", FormatVersion: BundleFormatVersion,
		SourceProduct: SourceProduct{
			Version: "0.1.0", HostOS: "darwin", HostArch: "arm64",
			Backend: "lima", BackendVersion: "2.2.0", GuestArch: "aarch64",
		},
		Environments: []EnvironmentSnapshot{{
			SourceEnvironmentRef: "environment_source1", DisplayNameHint: "dev",
			Runtime: "linux", GuestUser: "developer", Backend: "lima", Mode: ExportModeFull,
			ProfileComponentID: "component_profile0001",
			WorkspaceProposals: []WorkspaceProposal{{
				ProposalID: "workspace_source01", GuestPath: "/workspace",
				HostPathHint: "[destination path required]", State: "disabled",
			}},
			AuthorityProposalRefs: []OpaqueID{"proposal_network01"},
			GuestIdentityEvidence: GuestIdentityEvidence{
				MachineIDDigest:   digestForTest("6"),
				SSHHostKeyDigests: []Digest{digestForTest("7")},
			},
			DiskRefs: []OpaqueID{"disk_root0001"},
		}},
		DiskObjects: []DiskObject{{
			DiskID: "disk_root0001", Role: DiskRoleRoot, Format: "raw",
			LogicalBytes: uint64(len(diskBytes)), AllocatedBytesHint: uint64(len(diskBytes)),
			ContentDigest: diskDigest,
			Provider: ProviderDiskFacts{
				Name: "source-root", Kind: "lima-root", Features: []string{"sparse"},
			},
		}},
		DiskEdges: []DiskEdge{{
			EnvironmentRef: "environment_source1", DiskID: "disk_root0001",
			Attachment: DiskRoleRoot, GuestPath: "/", ReadOnly: false,
		}},
		SecretEntries: []SecretEntry{{
			SecretRef: "secret_proxy0001", DisplayName: "proxy credential",
			Provider: "keychain", RequiredAvailability: "available",
			EnvironmentRefs: []OpaqueID{"environment_source1"}, Transfer: SecretSelectedValue,
			ValueComponentID: "component_secret0001",
		}},
		AuthorityProposals: []AuthorityProposal{{
			ProposalID: "proposal_network01", Class: "network",
			SourceSummary: "disabled network proposal", State: "disabled",
		}},
		ComponentIndex: []ComponentIndexEntry{
			{
				ComponentID: "component_profile0001", Kind: "profile",
				LogicalBytes: uint64(len(profile)), FirstRecord: 0, LastRecord: 0,
				RecordCount: 1, ContentDigest: digestBytes(profile),
			},
			{
				ComponentID: "component_disk000001", Kind: "disk", DiskID: "disk_root0001",
				LogicalBytes: uint64(len(diskBytes)), FirstRecord: 1, LastRecord: 1,
				RecordCount: 1, ContentDigest: diskDigest,
			},
			{
				ComponentID: "component_secret0001", Kind: "secret-value",
				LogicalBytes: uint64(len(secret)), FirstRecord: 2, LastRecord: 2,
				RecordCount: 1, ContentDigest: digestBytes(secret),
			},
		},
		ExcludedClasses: []string{
			"activity-history", "host-workspace-content", "unselected-secret-values",
		},
		RequiredCapabilities: []RequiredCapability{{
			ID: "full-import", Provider: "lima", MinimumVersion: "2.2.0",
		}},
	}
	return manifest, records
}

func writeSealedInspectionFixture(
	t *testing.T,
	manifest Manifest,
	records []RecordInput,
	rawManifest []byte,
) []byte {
	t.Helper()
	var output bytes.Buffer
	writer, err := NewWriter(&output, WriterOptions{
		BundleID: manifest.BundleID, CreatedAt: "2026-08-02T00:00:00Z",
		KDF: unitKDFParameters(), Limits: DefaultLimits(),
		Random:     bytes.NewReader(deterministicRandomFixture(4096)),
		Passphrase: []byte("inspection passphrase"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for _, record := range records {
		if _, err := writer.Append(record); err != nil {
			t.Fatal(err)
		}
	}
	if rawManifest == nil {
		rawManifest, err = canonicalMarshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Seal(rawManifest); err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), output.Bytes()...)
}

func cloneInspectionManifest(t *testing.T, manifest Manifest) Manifest {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var clone Manifest
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func TestManifestValidationRejectsUnsortedAndUnreferencedSecretComponents(t *testing.T) {
	manifest, _ := sealedInspectionManifestFixture(t)
	manifest.ExcludedClasses = []string{"runtime-state", "activity-history"}
	if err := manifest.Validate(DefaultLimits()); !errors.Is(err, ErrCorruptBundle) {
		t.Fatalf("unsorted manifest error=%v", err)
	}
	manifest, _ = sealedInspectionManifestFixture(t)
	manifest.SecretEntries = nil
	if err := manifest.Validate(DefaultLimits()); !errors.Is(err, ErrCorruptBundle) {
		t.Fatalf("unreferenced secret component error=%v", err)
	}
	if strings.Contains(errString(manifest.Validate(DefaultLimits())), "super-secret") {
		t.Fatal("manifest validation leaked secret material")
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
