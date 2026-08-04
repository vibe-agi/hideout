package manager

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/migration"
)

func TestMigrationBundleInspectionProjectionKeepsPathsAndRedactsCredentials(t *testing.T) {
	manifest := managerMigrationManifestFixture("migb_projection0001")
	manifest.Environments[0].ImageProvenance = &migration.ImageProvenance{
		Reference: "https://alice:image-secret@example.test/base.qcow2",
		Digest:    migration.Digest("sha256:" + strings.Repeat("a", 64)),
	}
	manifest.Environments[0].WorkspaceProposals = []migration.WorkspaceProposal{{
		ProposalID: "workspace_source01", GuestPath: "/workspace",
		HostPathHint: "/Users/alice/dev", State: "disabled",
	}}
	manifest.Environments[0].AuthorityProposalRefs = []migration.OpaqueID{"proposal_proxy001"}
	manifest.AuthorityProposals = []migration.AuthorityProposal{{
		ProposalID: "proposal_proxy001", Class: "proxy",
		SourceSummary: "socks5://proxy-user:proxy-secret@127.0.0.1:7890",
		State:         "disabled",
	}}
	manifest.SecretEntries = []migration.SecretEntry{{
		SecretRef: "secret_proxy0001", DisplayName: "password=display-secret",
		Provider: "keychain", RequiredAvailability: "available",
		EnvironmentRefs:  []migration.OpaqueID{"environment_source1"},
		Transfer:         migration.SecretSelectedValue,
		ValueComponentID: "component_secret0001",
	}}
	manifest.ComponentIndex = append(manifest.ComponentIndex, migration.ComponentIndexEntry{
		ComponentID: "component_secret0001", Kind: "secret-value", LogicalBytes: 32,
		FirstRecord: 3, LastRecord: 3, RecordCount: 1,
		ContentDigest: migration.Digest("sha256:" + strings.Repeat("b", 64)),
	})
	inspection := sealedManagerInspectionFixture(manifest)

	projection, err := ProjectMigrationBundleInspection(inspection)
	if err != nil {
		t.Fatal(err)
	}
	if err := projection.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(projection.Environments) != 1 ||
		projection.Environments[0].WorkspaceProposals[0].GuestPath != "/workspace" ||
		projection.Environments[0].WorkspaceProposals[0].HostPathHint != "/Users/alice/dev" ||
		projection.Components.Disks != 2 || projection.Components.SecretValues != 1 ||
		len(projection.Secrets) != 1 || !projection.Secrets[0].ValueIncluded ||
		len(projection.Warnings) != 3 {
		t.Fatalf("projection lost useful inventory: %+v", projection)
	}
	encoded, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"image-secret", "proxy-secret", "display-secret", "sha256:",
		"machineIdDigest", "sshHostKeyDigests", "contentDigest", "valueComponentId",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("inspection projection leaked %q: %s", forbidden, encoded)
		}
	}
	for _, required := range []string{"/workspace", "/Users/alice/dev", "REDACTED"} {
		if !strings.Contains(string(encoded), required) {
			t.Fatalf("inspection projection hid useful field %q: %s", required, encoded)
		}
	}
	if manifest.AuthorityProposals[0].SourceSummary !=
		"socks5://proxy-user:proxy-secret@127.0.0.1:7890" {
		t.Fatal("projection mutated the authenticated manifest")
	}
}

func TestMigrationBundleInspectionProjectionRejectsUnsealedAndAggregateDrift(t *testing.T) {
	manifest := managerMigrationManifestFixture("migb_projection0002")
	inspection := sealedManagerInspectionFixture(manifest)
	inspection.Summary.Sealed = false
	if _, err := ProjectMigrationBundleInspection(inspection); !errors.Is(err, ErrMigrationPlanInvalid) {
		t.Fatalf("unsealed projection error=%v", err)
	}
	inspection = sealedManagerInspectionFixture(manifest)
	inspection.Summary.LogicalBytes++
	if _, err := ProjectMigrationBundleInspection(inspection); !errors.Is(err, ErrMigrationPlanInvalid) {
		t.Fatalf("logical aggregate drift error=%v", err)
	}
}

func sealedManagerInspectionFixture(
	manifest migration.Manifest,
) migration.SealedBundleInspection {
	binding := migration.BundleBinding{
		BundleID: manifest.BundleID, FormatVersion: migration.BundleFormatVersion,
		FileDigest:       migration.Digest("sha256:" + strings.Repeat("1", 64)),
		ManifestDigest:   migration.Digest("sha256:" + strings.Repeat("2", 64)),
		CompletionDigest: migration.Digest("sha256:" + strings.Repeat("3", 64)),
	}
	var logical uint64
	for _, disk := range manifest.DiskObjects {
		logical += disk.LogicalBytes
	}
	last := manifest.ComponentIndex[len(manifest.ComponentIndex)-1]
	return migration.SealedBundleInspection{
		CreatedAt: "2026-08-02T00:00:00Z", Binding: binding, Manifest: manifest,
		Summary: migration.BundleSummary{
			BundleID: manifest.BundleID, Sealed: true,
			RecordCount: last.LastRecord + 3, LogicalBytes: logical, EncodedBytes: logical + 4096,
			PrefixDigest: binding.FileDigest, ManifestDigest: binding.ManifestDigest,
		},
	}
}
