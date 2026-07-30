package manager

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestTypedChangeRegistryIsClosedAndRejectsSecretValueFields(t *testing.T) {
	registry := DefaultTypedChangeRegistry()
	if _, err := registry.NormalizeDraft(ConfigurationDraft{
		Schema:       ConfigurationDraftSchema,
		Profile:      "default",
		BaseRevision: 1,
		Changes: []TypedChange{{
			Kind:  "future.unknown",
			Value: json.RawMessage(`{"enabled":true}`),
		}},
	}); !errors.Is(err, ErrUnknownTypedChange) {
		t.Fatalf("unknown change error=%v want %v", err, ErrUnknownTypedChange)
	}
	if _, err := registry.NormalizeDraft(ConfigurationDraft{
		Schema:       ConfigurationDraftSchema,
		Profile:      "default",
		BaseRevision: 1,
		Changes: []TypedChange{{
			Kind:  ChangeNetworkProxyRef,
			Value: json.RawMessage(`{"ref":"local-proxy","value":"socks5://user:pass@127.0.0.1:7890"}`),
		}},
	}); !errors.Is(err, ErrInvalidConfigurationDraft) {
		t.Fatalf("secret-bearing proxy change error=%v want %v", err, ErrInvalidConfigurationDraft)
	}
}

func TestTypedChangeRegistrySeparatesPrivateEnvironmentInputFromPublicReview(t *testing.T) {
	const canary = "socks5://operator:private-password@127.0.0.1:7890"
	registry := DefaultTypedChangeRegistry()
	normalized, err := registry.NormalizeDraft(ConfigurationDraft{
		Schema:       ConfigurationDraftSchema,
		Profile:      "default",
		BaseRevision: 1,
		Changes: []TypedChange{{
			Kind: ChangeProfileEnvironment,
			Value: json.RawMessage(
				`{"set":{"LOCAL_PROXY":"` + canary + `"}}`,
			),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(normalized.Changes[0].Value, []byte(canary)) {
		t.Fatalf("private normalized change lost apply value: %s", normalized.Changes[0].Value)
	}

	reviewed, err := registry.ReviewChanges(normalized.Changes)
	if err != nil {
		t.Fatal(err)
	}
	reviewJSON, err := json.Marshal(reviewed)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(reviewJSON, []byte(canary)) ||
		!bytes.Contains(reviewJSON, []byte("[value provided]")) {
		t.Fatalf("public review exposed or omitted the environment marker: %s", reviewJSON)
	}
	reviewedAgain, err := registry.ReviewChanges(reviewed)
	if err != nil {
		t.Fatal(err)
	}
	if !rawChangesEqual(reviewed, reviewedAgain) {
		t.Fatalf("public review is not idempotent: first=%s second=%s", reviewJSON, reviewedAgain[0].Value)
	}
}

func TestConfigurationPlanDigestUsesCanonicalChangesAndBindsOperation(t *testing.T) {
	change, err := NewTypedChange(ChangeNetworkDNS, map[string]any{
		"serverIp": "1.1.1.1",
		"mode":     "doh",
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := ConfigurationPlan{
		Schema:           ConfigurationPlanSchema,
		OperationID:      "op_fixture_plan01",
		Profile:          "default",
		BaseRevision:     1,
		BaseDigest:       digestFixture("a"),
		CanonicalChanges: []TypedChange{change},
		Diff: []ReviewDiff{{
			Kind: ChangeNetworkDNS, Field: "network.dns",
			Before: "system", After: "doh via 1.1.1.1", Scope: "environment",
		}},
		Effects: []PlannedEffect{{
			ID: "persist-profile", Kind: "persist", Scope: "profile",
			Provider: "manager.profile", Summary: "Persist the reviewed DNS setting.",
			ProofRequired: []string{"profile-committed"},
		}},
		Blockers: []Blocker{},
		Warnings: []Warning{},
		Rollback: RollbackPlan{
			Mode: "restore-previous", Summary: "Restore the prior DNS setting.",
			Effects: []string{"persist-profile"},
		},
		ExpiresAt: time.Date(2026, 7, 29, 6, 0, 0, 0, time.UTC),
	}
	if err := plan.Seal(); err != nil {
		t.Fatal(err)
	}
	if err := plan.VerifyDigest(); err != nil {
		t.Fatal(err)
	}
	changed := plan
	changed.OperationID = "op_fixture_plan02"
	if err := changed.VerifyDigest(); !errors.Is(err, ErrInvalidConfigurationPlan) {
		t.Fatalf("changed operation digest error=%v want %v", err, ErrInvalidConfigurationPlan)
	}
}
