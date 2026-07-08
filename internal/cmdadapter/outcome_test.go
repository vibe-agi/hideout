package cmdadapter

import "testing"

func TestValidateOutcomeRejectsUnknownAndUndeclaredCapability(t *testing.T) {
	adapter := RuntimeAdapter{ID: "adapter"}
	if err := ValidateOutcome(Outcome{Outcome: "mystery", Reason: "x"}, adapter); err == nil {
		t.Fatal("expected unknown outcome rejection")
	}
	err := ValidateOutcome(Outcome{
		Outcome:    OutcomeProposeCapability,
		Reason:     "package install",
		Capability: CapabilityGuestPrivPlan,
		Intent:     map[string]any{"category": "package-manager"},
	}, adapter)
	if err == nil {
		t.Fatal("expected undeclared capability rejection")
	}
}

func TestValidateOutcomeRootSensitiveCannotSimulateSuccess(t *testing.T) {
	adapter := RuntimeAdapter{ID: "root-sensitive", RootSensitive: true}
	err := ValidateOutcome(Outcome{
		Outcome:  OutcomeSimulate,
		Reason:   "pretend install succeeded",
		ExitCode: 0,
	}, adapter)
	if err == nil {
		t.Fatal("expected root-sensitive successful simulation rejection")
	}
}

func TestValidateOutcomeRewriteGuestStaysNonPrivileged(t *testing.T) {
	adapter := RuntimeAdapter{ID: "adapter"}
	if err := ValidateOutcome(Outcome{
		Outcome: OutcomeRewriteGuest,
		Reason:  "rewrite",
		Argv:    []string{"tool-real", "--version"},
	}, adapter); err != nil {
		t.Fatalf("safe rewrite rejected: %v", err)
	}
	if err := ValidateOutcome(Outcome{
		Outcome: OutcomeRewriteGuest,
		Reason:  "rewrite to sudo",
		Argv:    []string{"sudo", "apt", "install", "nodejs"},
	}, adapter); err == nil {
		t.Fatal("expected root-sensitive rewrite rejection")
	}
}
