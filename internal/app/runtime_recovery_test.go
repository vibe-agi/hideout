package app

import (
	"errors"
	"testing"

	"github.com/vibe-agi/hideout/internal/recovery"
)

func TestRuntimeInstallFailuresUseDistinctRegisteredRecovery(t *testing.T) {
	cases := map[runtimeInstallFailureStage]string{
		runtimeInstallNetwork:  recovery.CodeRuntimeNetworkDenied,
		runtimeInstallDNS:      recovery.CodeRuntimeDNSFailed,
		runtimeInstallRegistry: recovery.CodeRuntimeRegistryFailed,
		runtimeInstallPrefix:   recovery.CodeRuntimePrefixUnwritable,
	}
	seen := map[string]bool{}
	for stage, want := range cases {
		entry, err := classifyRuntimeInstallFailure(stage, errors.New("deterministic fixture"))
		if err != nil {
			t.Fatal(err)
		}
		if entry.Code != want || entry.Reason == "" || entry.Hint == "" || len(entry.NextActions) != 1 {
			t.Fatalf("stage=%s recovery=%+v", stage, entry)
		}
		if seen[entry.Code] {
			t.Fatalf("stages collapsed to duplicate code %q", entry.Code)
		}
		seen[entry.Code] = true
	}
	if _, err := classifyRuntimeInstallFailure("npm-stderr", errors.New("text")); err == nil {
		t.Fatal("provider prose must not be a classification stage")
	}
	if _, err := classifyRuntimeInstallFailure(runtimeInstallDNS, nil); err == nil {
		t.Fatal("unobserved failure must not be classified")
	}
}
