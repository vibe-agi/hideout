package manager

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestMigrationAuthorityConfirmationRequiresExactApprovedProposalIDs(t *testing.T) {
	digest := migration.Digest("sha256:" + strings.Repeat("a", 64))
	approved := []migration.OpaqueID{"authority_network001"}
	confirmation := MigrationPlanConfirmation{
		PlanDigest: digest, ApprovedAuthorityProposalIDs: approved,
	}
	if err := validateMigrationConfirmation(digest, nil, approved, confirmation); err != nil {
		t.Fatal(err)
	}
	confirmation.ApprovedAuthorityProposalIDs = nil
	if err := validateMigrationConfirmation(digest, nil, approved, confirmation); !errors.Is(err, ErrMigrationConfirmationRequired) {
		t.Fatalf("missing approval confirmation error=%v", err)
	}
}

func TestMigrationExportAuthorityProposalsAreDisabledAndEnvironmentBound(t *testing.T) {
	source := profile.Default("source")
	proposals, refs, err := migrationAuthorityProposalsForProfile(
		"op_migration_authority_export", "env_authority_source1", source,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals) != 4 || len(refs) != 4 {
		t.Fatalf("default authority proposals=%+v refs=%v", proposals, refs)
	}
	classes := make([]string, len(proposals))
	for index := range proposals {
		classes[index] = proposals[index].Class
	}
	slices.Sort(classes)
	if !slices.Equal(classes, []string{"endpoint", "environment", "host-app", "network"}) {
		t.Fatalf("default authority classes=%v", classes)
	}
	for index, proposal := range proposals {
		if proposal.State != "disabled" || proposal.SourceSummary == "" ||
			proposal.ProposalID != refs[index] {
			t.Fatalf("proposal is not closed and deterministic: %+v refs=%v", proposal, refs)
		}
	}
}

func TestMigrationAuthorityPlanAndApplyRequireExactEnvironmentBoundApproval(t *testing.T) {
	source := profile.Default("source")
	proposals, refs, err := migrationAuthorityProposalsForProfile(
		"op_migration_authority_plan", "environment_source1", source,
	)
	if err != nil {
		t.Fatal(err)
	}
	manifest := migration.Manifest{
		Environments: []migration.EnvironmentSnapshot{{
			SourceEnvironmentRef:  "environment_source1",
			AuthorityProposalRefs: refs,
		}},
		AuthorityProposals: proposals,
	}
	var network migration.AuthorityProposal
	for _, proposal := range proposals {
		if proposal.Class == "network" {
			network = proposal
		}
	}
	draft := migration.ImportDraft{
		SelectedEnvironmentRefs: []migration.OpaqueID{"environment_source1"},
		AuthorityDecisions: []migration.AuthorityDecision{{
			ProposalID: network.ProposalID, Decision: migrationAuthorityDecisionApproved,
			DestinationValue: network.SourceSummary,
		}},
	}
	actions, disabled, blockers, err := planMigrationAuthorityActions(
		draft, manifest, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(blockers) != 0 || len(actions) != 1 ||
		actions[0].EnvironmentRef != "environment_source1" ||
		actions[0].ProposalID != network.ProposalID ||
		actions[0].Class != "network" || !actions[0].Approved {
		t.Fatalf("approved authority was not frozen exactly: actions=%+v blockers=%+v", actions, blockers)
	}
	if len(disabled) != 3 || slices.Contains(disabled, network.ProposalID) {
		t.Fatalf("disabled proposal set=%v", disabled)
	}

	destination, err := migration.NormalizePortableProfile(source)
	if err != nil {
		t.Fatal(err)
	}
	profileValue, err := destination.DestinationProfile("clone")
	if err != nil {
		t.Fatal(err)
	}
	if err := applyMigrationAuthorityActions(
		&profileValue, "environment_source1", actions, nil,
	); err != nil {
		t.Fatal(err)
	}
	if profileValue.Network != source.Network ||
		!slices.Contains(profileValue.Policy.MaxCapabilities, "network.connect") {
		t.Fatalf("network authority was not applied: %+v", profileValue)
	}
	if profileValue.HostCapabilities.Open.AllowURLs {
		t.Fatal("unapproved host-app authority became effective")
	}
}

func TestMigrationAuthorityApprovalRejectsNonCanonicalAndUnsupportedValues(t *testing.T) {
	source := profile.Default("source")
	proposals, refs, err := migrationAuthorityProposalsForProfile(
		"op_migration_authority_reject", "environment_source1", source,
	)
	if err != nil {
		t.Fatal(err)
	}
	manifest := migration.Manifest{
		Environments: []migration.EnvironmentSnapshot{{
			SourceEnvironmentRef:  "environment_source1",
			AuthorityProposalRefs: refs,
		}},
		AuthorityProposals: proposals,
	}
	var network migration.AuthorityProposal
	for _, proposal := range proposals {
		if proposal.Class == "network" {
			network = proposal
		}
	}
	draft := migration.ImportDraft{
		SelectedEnvironmentRefs: []migration.OpaqueID{"environment_source1"},
		AuthorityDecisions: []migration.AuthorityDecision{{
			ProposalID: network.ProposalID, Decision: migrationAuthorityDecisionApproved,
			DestinationValue: "{ \"mode\": \"direct\" }",
		}},
	}
	if _, _, _, err := planMigrationAuthorityActions(draft, manifest, nil); err == nil {
		t.Fatal("non-canonical destination authority was accepted")
	}

	unsupported := migration.AuthorityProposal{
		ProposalID: "proposal_script001", Class: "script",
		SourceSummary: `{"id":"policy","path":"policy.js"}`, State: "disabled",
	}
	manifest.Environments[0].AuthorityProposalRefs = []migration.OpaqueID{unsupported.ProposalID}
	manifest.AuthorityProposals = []migration.AuthorityProposal{unsupported}
	draft.AuthorityDecisions[0] = migration.AuthorityDecision{
		ProposalID: unsupported.ProposalID, Decision: migrationAuthorityDecisionApproved,
		DestinationValue: unsupported.SourceSummary,
	}
	actions, disabled, blockers, err := planMigrationAuthorityActions(draft, manifest, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 0 || !slices.Equal(disabled, []migration.OpaqueID{unsupported.ProposalID}) ||
		len(blockers) != 1 || blockers[0].Code != "migration.authority.approval_unavailable" {
		t.Fatalf("unsupported authority did not fail closed: actions=%+v disabled=%v blockers=%+v", actions, disabled, blockers)
	}
}
