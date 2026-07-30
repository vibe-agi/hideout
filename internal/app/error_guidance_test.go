package app

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestMissingSecretGuidanceUsesDaemonManagedSecretCommands(t *testing.T) {
	guidance := guidanceForError(
		fmt.Errorf("code=session.start.failed: secret ref local-proxy is not set"),
		defaultCommandCatalog(),
	)
	if guidance.Code != "secret.missing" {
		t.Fatalf("guidance code=%q", guidance.Code)
	}
	for _, want := range []string{
		"hideout secret set local-proxy",
		"hideout secret status local-proxy",
	} {
		if !containsString(guidance.Next, want) {
			t.Fatalf("missing-secret guidance has no %q: %+v", want, guidance)
		}
	}
	combined := guidance.Reason + "\n" +
		strings.Join(guidance.Next, "\n") + "\n" +
		strings.Join(guidance.Notes, "\n")
	for _, forbidden := range []string{
		"export HIDEOUT_SECRET",
		"daemon stop",
		"recreate",
	} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("missing-secret guidance recommends %q:\n%s", forbidden, combined)
		}
	}
	assertGuidanceCommandsAreCatalogBacked(t, guidance)
}

func TestConnectionMutationNamesDesiredEffectiveAndPendingNextAttach(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := profile.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(profile.Default("default")); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Main(
		[]string{
			"connect", "through", "local-proxy",
			"using", "1.1.1.1", "--yes",
		},
		&stdout,
		&stderr,
	)
	if code != 0 {
		t.Fatalf("connect exit=%d stderr=%s", code, stderr.String())
	}
	for _, want := range []string{
		"Desired:",
		"Effective:",
		"Existing sessions are unchanged",
		"Transition: pending-next-attach",
		"next eligible attach",
		"Next: hideout show connection",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("connection output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestStaleClientGuidanceRefreshesBeforeRetry(t *testing.T) {
	guidance := guidanceForError(
		fmt.Errorf("client revision 7: %w", manager.ErrStaleConfigurationPlan),
		defaultCommandCatalog(),
	)
	if guidance.Code != "configuration.stale-client" ||
		!strings.Contains(guidance.Reason, "no stale plan was applied") {
		t.Fatalf("stale guidance=%+v", guidance)
	}
	if !containsString(guidance.Next, "hideout tui") ||
		!containsString(guidance.Next, "hideout show connection") {
		t.Fatalf("stale guidance does not refresh authority: %+v", guidance)
	}
	assertGuidanceCommandsAreCatalogBacked(t, guidance)
}

func TestUnsupportedCapabilityGuidanceDoesNotSuggestBypass(t *testing.T) {
	guidance := guidanceForError(
		fmt.Errorf("network live update: %w", manager.ErrConfigurationProviderUnavailable),
		defaultCommandCatalog(),
	)
	if guidance.Code != "capability.unsupported" {
		t.Fatalf("unsupported guidance=%+v", guidance)
	}
	if !containsString(guidance.Next, "hideout support matrix") ||
		!containsString(guidance.Next, "hideout version") {
		t.Fatalf("unsupported guidance has no support/version checks: %+v", guidance)
	}
	combined := guidance.Reason + strings.Join(guidance.Next, " ")
	if strings.Contains(combined, "allow-weak-isolation") ||
		strings.Contains(combined, "--enable-lab") {
		t.Fatalf("unsupported guidance suggests a safety bypass: %+v", guidance)
	}
	assertGuidanceCommandsAreCatalogBacked(t, guidance)
}

func TestOperationRecoveryGuidancePreservesExactIdentityAndProof(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code string
	}{
		{
			name: "configuration",
			err:  manager.ErrConfigurationRecoveryRequired,
			code: "operation.recovery-required",
		},
		{
			name: "secret",
			err:  manager.ErrSecretRecoveryRequired,
			code: "operation.recovery-required",
		},
		{
			name: "network",
			err:  manager.ErrNetworkTransitionRecoveryRequired,
			code: "operation.recovery-required",
		},
		{
			name: "terminal proof",
			err:  manager.ErrOperationTerminalUnproved,
			code: "operation.proof-unproved",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			guidance := guidanceForError(
				test.err,
				defaultCommandCatalog(),
			)
			combined := guidance.Reason + "\n" +
				strings.Join(guidance.Next, "\n") + "\n" +
				strings.Join(guidance.Notes, "\n")
			if guidance.Code != test.code ||
				!strings.Contains(combined, "exact") ||
				!strings.Contains(combined, "operation") {
				t.Fatalf("recovery guidance=%+v", guidance)
			}
			if strings.Contains(combined, "new operation") ||
				strings.Contains(combined, "repeat the mutation") ||
				strings.Contains(combined, "doctor --feature operations") {
				t.Fatalf(
					"recovery guidance suggests replacement, replay, or a nonexistent command: %s",
					combined,
				)
			}
			assertGuidanceCommandsAreCatalogBacked(t, guidance)
		})
	}
}

func TestSecretApplyOutcomeGuidanceUsesExactOperationWithoutReplay(t *testing.T) {
	const operationID = "op_secretoutcome01"
	guidance := guidanceForError(
		newSecretApplyOutcomeError(
			operationID,
			errors.New("transport response lost"),
		),
		defaultCommandCatalog(),
	)
	combined := guidance.Reason + "\n" +
		strings.Join(guidance.Next, "\n") + "\n" +
		strings.Join(guidance.Notes, "\n")
	if guidance.Code != "operation.outcome-unknown" ||
		!strings.Contains(combined, operationID) ||
		!strings.Contains(combined, "exact ID") ||
		!strings.Contains(combined, "fresh plan") {
		t.Fatalf("outcome guidance=%+v", guidance)
	}
	for _, forbidden := range []string{
		"retry the command",
		"apply again",
		"create a replacement operation",
	} {
		if strings.Contains(strings.ToLower(combined), forbidden) {
			t.Fatalf("outcome guidance suggests unsafe replay: %s", combined)
		}
	}
	assertGuidanceCommandsAreCatalogBacked(t, guidance)
}

func TestUnknownCommandGuidanceSuggestsCanonicalCommandAndSearch(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Main([]string{"docter"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("unknown command succeeded: %s", stdout.String())
	}
	for _, want := range []string{
		`unknown command "docter"`,
		"Did you mean:",
		"hideout doctor",
		"hideout help search docter",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("unknown-command guidance missing %q:\n%s", want, stderr.String())
		}
	}
}

func TestParserGuidancePointsToCatalogContextWithoutApplying(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := Main(
		[]string{"doctor", "--definitely-not-a-hideout-flag"},
		&stdout,
		&stderr,
	)
	if code == 0 {
		t.Fatalf("unknown flag succeeded: %s", stdout.String())
	}
	for _, want := range []string{
		"flag provided but not defined",
		"code: input.invalid",
		"The command was rejected before its requested effect was applied.",
		"hideout help doctor",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("parser guidance missing %q:\n%s", want, stderr.String())
		}
	}
}

func assertGuidanceCommandsAreCatalogBacked(
	t *testing.T,
	guidance errorGuidance,
) {
	t.Helper()
	catalog := defaultCommandCatalog()
	for _, command := range guidance.Next {
		fields := strings.Fields(command)
		if len(fields) < 2 || fields[0] != "hideout" {
			t.Fatalf("next command is not canonical CLI syntax: %q", command)
		}
		entry, ok := catalog.lookup(fields[1])
		if !ok || entry.spec.Hidden {
			t.Fatalf("next command is not in visible catalog: %q", command)
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
